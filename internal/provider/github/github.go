// Package github implements controlled planning access to GitHub.
//
// A connection binds one token to exactly one user or organization project, or to exactly one repository.
// A project connection lists compact, server-side filtered items of that project page by page, reads the
// full content of one selected item, and maintains its items and their field values; a repository
// connection reads, creates, and changes issues of that repository and reads or writes the comments of one
// issue on explicit request. A project connection may also name repositories: only issues of those are
// created for or added to its project. Nothing here accepts a free filter expression, a GraphQL document, a
// route, an owner, or a project from an agent, and a repository argument only selects among the configured
// ones: every request stays inside the configured targets.
//
// A change is sent at most once. Several field values of one item are written in small, serial batches of
// aliased mutations after the project, its fields, and their options were resolved once, and the answer
// names what was written, what failed, and what may have happened without a confirmation.
//
// Issue titles, bodies, labels, and field values arrive from the provider and are treated as untrusted
// data: they are normalised into a stable Qatlas shape, passed through the output encoders, and never
// rendered or stored.
package github

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Provider is the provider name used in the configuration and as the operation namespace.
const Provider = "github"

// defaultBaseURL is the REST root of GitHub.com. GitHub Enterprise Server configures https://HOST/api/v3
// instead; the agent never supplies one.
const defaultBaseURL = "https://api.github.com"

// roleToken is the single secret role a GitHub credential must supply. It is used as a bearer token.
const roleToken = "token"

// apiVersion pins the REST API version every request asks for.
const apiVersion = "2022-11-28"

// dataSensitivity classifies results as planning data of the configured GitHub project or repository.
const dataSensitivity = "github-planning-data"

// Bounds of one request. A page holds 30 entries unless the caller asks for fewer or more, and never more
// than the 100 GitHub serves at once. A filtered list scans at most maxScanRequests server pages before it
// hands the caller a cursor to continue.
const (
	defaultLimit     = 30
	maxLimit         = 100
	maxFilterValues  = 10
	maxScanRequests  = 5
	maxResponseBytes = 4 << 20
	defaultTimeout   = 30 * time.Second
	cursorBinding    = 12
	maxCursorLength  = 1024
)

// minInterval spaces the requests that share one token. GitHub asks integrations to send requests
// serially; its primary budget is counted per token.
const minInterval = 200 * time.Millisecond

// maxHold bounds how long a reported rate-limit reset may delay the next request of this process.
const maxHold = time.Minute

// mutationInterval spaces a request after a change of the same token, as GitHub asks of integrations that
// write.
const mutationInterval = time.Second

// limiters holds the rate-limit budget of every token this process has used.
var limiters = ratelimit.NewRegistry(minInterval)

var readRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: dataSensitivity,
}

// Input patterns shared by the schemas. The backslashes are doubled because the patterns are embedded in
// JSON schema strings. A filter value may not carry a quote, a comma, a backslash, an asterisk, or a
// control character, because those are the separators and wildcards of the project filter grammar.
const (
	filterValuePattern = `^[^\"',\\\\*\\x00-\\x1f\\x7f]+$`
	loginPattern       = `^[A-Za-z0-9][A-Za-z0-9_-]{0,99}$`
	repositoryPattern  = `^[A-Za-z0-9][A-Za-z0-9_-]{0,99}/[A-Za-z0-9._-]{1,100}$`
	cursorPattern      = `^[A-Za-z0-9_-]+$`
	nodeIDPattern      = `^[A-Za-z0-9_=+/-]{4,200}$`
)

const filterValueSchema = `{"type":"string","minLength":1,"maxLength":100,"pattern":"` + filterValuePattern + `"}`

const filterListSchema = `{"type":"array","maxItems":10,"items":` + filterValueSchema + `}`

const cursorSchema = `{"type":"string","minLength":1,"maxLength":1024,"pattern":"` + cursorPattern + `"}`

const stringListSchema = `{"type":"array","items":{"type":"string"}}`

// itemProperties is the compact item projection shared by the list and the detail.
const itemProperties = `"id":{"type":"string"},"type":{"type":"string"},"title":{"type":"string"},` +
	`"number":{"type":"integer"},"repository":{"type":"string"},"state":{"type":"string"},` +
	`"status":{"type":"string"},"fields":{"type":"object"},"assignees":` + stringListSchema + `,` +
	`"labels":` + stringListSchema + `,"url":{"type":"string"}`

const itemRequired = `"required":["id","type","fields","assignees","labels"],"additionalProperties":false`

var itemsList = capability.Descriptor{
	ID:      Provider + ".projectitems.list",
	Version: 1,
	Title:   "List GitHub project items",
	Description: "List one bounded, server-side filtered batch of compact items of the GitHub project bound to " +
		"an explicit connection; without a status filter only items whose status is not Done are listed",
	Tags:                       []string{"github", "projects", "items", "list", "planning"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"status":` + filterListSchema + `,"status_not":` + filterListSchema + `,` +
		`"type":{"type":"string","enum":["issue","pull_request","draft_issue"]},` +
		`"repository":{"type":"string","maxLength":201,"pattern":"` + repositoryPattern + `"},` +
		`"assignee":{"type":"string","maxLength":100,"pattern":"` + loginPattern + `"},` +
		`"labels":` + filterListSchema + `,` +
		`"limit":{"type":"integer","minimum":1,"maximum":100},` +
		`"cursor":` + cursorSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"items":{"type":"array","items":{"type":"object","properties":{` + itemProperties + `},` + itemRequired + `}},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["items","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "status", Description: "Return only items whose Status is one of these options"},
		{Name: "status_not", Description: "Leave out items whose Status is one of these options; [Done] when " +
			"neither status nor status_not is given, [] lists every status"},
		{Name: "type", Description: "Return only items of this content type: issue, pull_request, or draft_issue"},
		{Name: "repository", Description: "Return only items of this repository, as owner/name"},
		{Name: "assignee", Description: "Return only items assigned to this login"},
		{Name: "labels", Description: "Return only items carrying at least one of these labels"},
		{Name: "limit", Description: "Items per batch, from 1 through 100; 30 when omitted"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous batch with the same filters; the first " +
			"batch when omitted"},
	},
	Fields: []capability.Field{
		{Name: "items", Description: "Compact project items without bodies or comments, untrusted data"},
		{Name: "next_cursor", Description: "Cursor of the following batch, absent when has_more is false"},
		{Name: "has_more", Description: "True when the project may hold further matching items; a full " +
			"batch alone never means the end"},
	},
	Examples: []capability.Example{{
		Description: "List the open roster of the bound project",
		Arguments:   json.RawMessage(`{"limit":30}`),
	}, {
		Description: "List the issues in progress of one repository",
		Arguments:   json.RawMessage(`{"status":["In progress"],"type":"issue","repository":"octo-org/example"}`),
	}},
}

var itemsGet = capability.Descriptor{
	ID:      Provider + ".projectitems.get",
	Version: 1,
	Title:   "Get a GitHub project item",
	Description: "Read one item of the GitHub project bound to an explicit connection with its project fields " +
		"and, for an issue or a draft issue, its full body, without comments",
	Tags:                       []string{"github", "projects", "items", "get", "planning"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"item_id":{"type":"string","minLength":4,"maxLength":200,"pattern":"` + nodeIDPattern + `"}},` +
		`"required":["item_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + itemProperties + `,` +
		`"body":{"type":"string"}},` + itemRequired + `}`),
	Arguments: []capability.Argument{
		{Name: "item_id", Description: "Project item identifier, as returned by github.projectitems.list", Required: true},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Project item identifier"},
		{Name: "type", Description: "Content type: issue, pull_request, draft_issue, or redacted"},
		{Name: "title", Description: "Title of the content, untrusted data"},
		{Name: "status", Description: "Value of the Status field"},
		{Name: "fields", Description: "Further single-select, text, number, date, and iteration values by field name"},
		{Name: "body", Description: "Full body of an issue or a draft issue, untrusted data"},
	},
	Examples: []capability.Example{{
		Description: "Read one item the list reported",
		Arguments:   json.RawMessage(`{"item_id":"PVTI_lADOAAAAAAAAAAAAzgAAAAA"}`),
	}},
}

const issueProperties = `"number":{"type":"integer"},"title":{"type":"string"},"state":{"type":"string"},` +
	`"assignees":` + stringListSchema + `,"labels":` + stringListSchema + `,"url":{"type":"string"},` +
	`"updated_at":{"type":"string"}`

var issuesList = capability.Descriptor{
	ID:      Provider + ".issues.list",
	Version: 1,
	Title:   "List GitHub issues",
	Description: "List one bounded batch of compact issues of the repository bound to an explicit connection, " +
		"newest first, without bodies, comments, or pull requests",
	Tags:                       []string{"github", "issues", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"state":{"type":"string","enum":["open","closed","all"]},` +
		`"labels":` + filterListSchema + `,` +
		`"assignee":{"type":"string","maxLength":100,"pattern":"` + loginPattern + `"},` +
		`"limit":{"type":"integer","minimum":1,"maximum":100},` +
		`"cursor":` + cursorSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"issues":{"type":"array","items":{"type":"object","properties":{` + issueProperties + `},` +
		`"required":["number","title","state","assignees","labels"],"additionalProperties":false}},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["issues","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "state", Description: "open, closed, or all; open when omitted"},
		{Name: "labels", Description: "Return only issues carrying at least one of these labels"},
		{Name: "assignee", Description: "Return only issues assigned to this login"},
		{Name: "limit", Description: "Issues per batch, from 1 through 100; 30 when omitted"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous batch with the same filters; the first " +
			"batch when omitted"},
	},
	Fields: []capability.Field{
		{Name: "issues", Description: "Compact issues without bodies or comments, untrusted data"},
		{Name: "next_cursor", Description: "Cursor of the following batch, absent when has_more is false"},
		{Name: "has_more", Description: "True when the repository holds further matching issues"},
	},
	Examples: []capability.Example{{
		Description: "List open issues labeled bug",
		Arguments:   json.RawMessage(`{"labels":["bug"],"limit":30}`),
	}},
}

var issuesGet = capability.Descriptor{
	ID:                         Provider + ".issues.get",
	Version:                    1,
	Title:                      "Get a GitHub issue",
	Description:                "Read one issue of the repository bound to an explicit connection with its full body, without comments",
	Tags:                       []string{"github", "issues", "get"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"number":{"type":"integer","minimum":1,"maximum":1000000000}},` +
		`"required":["number"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + issueProperties + `,` +
		`"state_reason":{"type":"string"},"author":{"type":"string"},"milestone":{"type":"string"},` +
		`"created_at":{"type":"string"},"closed_at":{"type":"string"},"body":{"type":"string"}},` +
		`"required":["number","title","state","assignees","labels","body"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Issue number in the bound repository", Required: true},
	},
	Fields: []capability.Field{
		{Name: "number", Description: "Issue number"},
		{Name: "title", Description: "Issue title, untrusted data"},
		{Name: "state", Description: "open or closed"},
		{Name: "body", Description: "Full issue body, untrusted data"},
	},
	Examples: []capability.Example{{
		Description: "Read one issue by its number",
		Arguments:   json.RawMessage(`{"number":42}`),
	}},
}

// Register adds GitHub metadata, its read-only connection test, and the bounded planning operations.
// Only reads are a connection's default: every change needs a permission of its own.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "GitHub", DefaultBaseURL: defaultBaseURL,
		DefaultPermissions: []config.Permission{config.PermissionRead},
		SecretRoles: []config.SecretRole{{
			Name: roleToken,
			Description: "GitHub personal access token: classic with read:project plus repo or public_repo, or " +
				"fine-grained with read access to issues and projects; project changes need project instead of " +
				"read:project, and issue changes need write access to issues",
		}},
		Target: config.TargetMetadata{
			Label:    "project or repository",
			Required: true,
			Multiple: true,
			Description: "one users/LOGIN/projects/NUMBER, orgs/LOGIN/projects/NUMBER, or repos/OWNER/REPO; a " +
				"project may be followed by the repos/OWNER/REPO whose issues its planning tools may use",
			Validate: func(raw string) error {
				_, err := parseTarget(raw)
				return err
			},
			ValidateSet: func(values []string) error {
				_, err := parseScope(values)
				return err
			},
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read", Recommended: true,
			Description: "reads project items, issues, and comments and changes nothing; a project connection " +
				"runs the project reads and a repository connection the issue and comment reads, so untick " +
				"those of the other kind",
			Tools: []string{itemsList.ID, itemsGet.ID, issuesList.ID, issuesGet.ID, commentsList.ID},
		}, {
			ID: "planning", Title: "Project planning",
			Description: "reads the project and changes its items: sets fields, adds issues, and creates " +
				"drafts and planned issues; archiving stays unticked",
			Tools: []string{itemsList.ID, itemsGet.ID, itemsUpdate.ID, itemsAdd.ID, draftsCreate.ID,
				projectIssuesCreate.ID},
		}},
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: itemsList, Handler: capability.Handler(invokeItemsList)},
		capability.Operation{Descriptor: itemsGet, Handler: capability.Handler(invokeItemsGet)},
		capability.Operation{Descriptor: issuesList, Handler: capability.Handler(invokeIssuesList)},
		capability.Operation{Descriptor: issuesGet, Handler: capability.Handler(invokeIssuesGet)},
		capability.Operation{Descriptor: issuesCreate, Handler: capability.Handler(invokeIssuesCreate)},
		capability.Operation{Descriptor: issuesUpdate, Handler: capability.Handler(invokeIssuesUpdate)},
		capability.Operation{Descriptor: issuesClose, Handler: capability.Handler(invokeIssuesClose)},
		capability.Operation{Descriptor: issuesReopen, Handler: capability.Handler(invokeIssuesReopen)},
		capability.Operation{Descriptor: commentsList, Handler: capability.Handler(invokeCommentsList)},
		capability.Operation{Descriptor: commentsCreate, Handler: capability.Handler(invokeCommentsCreate)},
		capability.Operation{Descriptor: itemsUpdate, Handler: capability.Handler(invokeItemsUpdate)},
		capability.Operation{Descriptor: itemsAdd, Handler: capability.Handler(invokeItemsAdd)},
		capability.Operation{Descriptor: itemsArchive, Handler: capability.Handler(invokeItemsArchive)},
		capability.Operation{Descriptor: draftsCreate, Handler: capability.Handler(invokeDraftsCreate)},
		capability.Operation{Descriptor: projectIssuesCreate, Handler: capability.Handler(invokeProjectIssuesCreate)},
	)
}

func invokeItemsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options ItemListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, providerError("list project items", "the validated arguments could not be read")
	}
	bound, err := requireKind(resolved, kindProject, itemsList.ID)
	if err != nil {
		return nil, err
	}
	// The arguments and the cursor are checked before a credential is resolved, so a refused request never
	// becomes a provider call.
	after, err := options.normalize(bound)
	if err != nil {
		return nil, err
	}
	client, err := Open(resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.listItems(ctx, options, after)
}

func invokeItemsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		ItemID string `json:"item_id"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, providerError("get project item", "the validated arguments could not be read")
	}
	if _, err := requireKind(resolved, kindProject, itemsGet.ID); err != nil {
		return nil, err
	}
	client, err := Open(resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetItem(ctx, arguments.ItemID)
}

func invokeIssuesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options IssueListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, providerError("list issues", "the validated arguments could not be read")
	}
	bound, err := requireKind(resolved, kindRepository, issuesList.ID)
	if err != nil {
		return nil, err
	}
	after, err := options.normalize(bound)
	if err != nil {
		return nil, err
	}
	client, err := Open(resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.listIssues(ctx, options, after)
}

func invokeIssuesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, providerError("get issue", "the validated arguments could not be read")
	}
	if _, err := requireKind(resolved, kindRepository, issuesGet.ID); err != nil {
		return nil, err
	}
	client, err := Open(resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetIssue(ctx, arguments.Number)
}

// requireKind refuses a tool on a connection whose target is of the other kind, before any credential is
// resolved: a repository connection offers no project and a project connection no repository.
func requireKind(resolved *config.Resolved, kind targetKind, tool string) (target, error) {
	bound, err := requireScope(resolved, kind, tool)
	return bound.target, err
}

// requireScope is requireKind for the tools that also need the repositories of a project connection.
func requireScope(resolved *config.Resolved, kind targetKind, tool string) (scope, error) {
	if resolved == nil {
		return scope{}, providerError("open", "no connection was selected")
	}
	bound, err := scopeOf(resolved)
	if err != nil {
		return scope{}, providerError("open", err.Error())
	}
	if bound.target.kind != kind {
		return scope{}, &capability.UnsupportedError{Connection: resolved.Name, Capability: tool}
	}
	return bound, nil
}

// targetKind tells a project connection from a repository connection.
type targetKind int

const (
	kindProject targetKind = iota + 1
	kindRepository
)

// target is the one GitHub object a connection is bound to.
type target struct {
	kind   targetKind
	scope  string // users or orgs for a project
	owner  string
	number int
	repo   string
}

// scope is everything one connection binds: its target and, for a project, the repositories whose issues
// the planning tools may create for or add to that project.
type scope struct {
	target       target
	repositories []target
}

// repository returns the configured repository an owner/name argument selects. GitHub compares names
// without case; the configured spelling is used from then on.
func (s scope) repository(value string) (target, error) {
	for _, repository := range s.repositories {
		if strings.EqualFold(repository.owner+"/"+repository.repo, value) {
			return repository, nil
		}
	}
	return target{}, invalidRequest("repository is not one of the repositories this connection may plan in")
}

func scopeOf(resolved *config.Resolved) (scope, error) {
	values := resolved.Targets
	if len(values) == 0 {
		values = []string{resolved.Target}
	}
	return parseScope(values)
}

// parseScope reads the target list of one connection: exactly one project or one repository, or one
// project together with the repositories it may plan in. No other combination is accepted, so a connection
// always has exactly one target a tool acts on.
func parseScope(values []string) (scope, error) {
	if len(values) == 1 {
		bound, err := parseTarget(values[0])
		return scope{target: bound}, err
	}
	var bound scope
	seen := map[string]bool{}
	for _, value := range values {
		parsed, err := parseTarget(value)
		if err != nil {
			return scope{}, err
		}
		key := strings.ToLower(parsed.String())
		if seen[key] {
			return scope{}, errors.New("the GitHub target list names a target more than once")
		}
		seen[key] = true
		if parsed.kind == kindRepository {
			bound.repositories = append(bound.repositories, parsed)
			continue
		}
		if bound.target.kind != 0 {
			return scope{}, errors.New("a GitHub target list may name only one project")
		}
		bound.target = parsed
	}
	if bound.target.kind != kindProject {
		return scope{}, errors.New("several GitHub targets must be one project and the repositories it plans in")
	}
	return bound, nil
}

func (t target) String() string {
	if t.kind == kindRepository {
		return "repos/" + t.owner + "/" + t.repo
	}
	return t.scope + "/" + t.owner + "/projects/" + strconv.Itoa(t.number)
}

// parseTarget reads users/LOGIN/projects/NUMBER, orgs/LOGIN/projects/NUMBER, or repos/OWNER/REPO. No other
// form is accepted, so a target always names exactly one project or one repository. The error never quotes
// the value.
func parseTarget(raw string) (target, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return target{}, errors.New("a GitHub connection needs a project or a repository target")
	}
	if len(trimmed) > 256 {
		return target{}, errors.New("the GitHub target is too long")
	}
	parts := strings.Split(trimmed, "/")
	switch {
	case len(parts) == 4 && (parts[0] == "users" || parts[0] == "orgs") && parts[2] == "projects":
		if !validLogin(parts[1]) {
			return target{}, errors.New("the GitHub target does not name a usable owner login")
		}
		number, ok := projectNumber(parts[3])
		if !ok {
			return target{}, errors.New("the GitHub target does not name a usable project number")
		}
		return target{kind: kindProject, scope: parts[0], owner: parts[1], number: number}, nil
	case len(parts) == 3 && parts[0] == "repos":
		if !validLogin(parts[1]) || !validRepoName(parts[2]) {
			return target{}, errors.New("the GitHub target does not name a usable repository")
		}
		return target{kind: kindRepository, owner: parts[1], repo: parts[2]}, nil
	}
	return target{}, errors.New("a GitHub target must be users/LOGIN/projects/NUMBER, " +
		"orgs/LOGIN/projects/NUMBER, or repos/OWNER/REPO")
}

func projectNumber(value string) (int, bool) {
	if value == "" || len(value) > 9 || value[0] == '0' {
		return 0, false
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 {
		return 0, false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	return number, true
}

// validLogin mirrors loginPattern: an alphanumeric first character, then letters, digits, '-' or '_'.
func validLogin(value string) bool {
	if value == "" || len(value) > 100 {
		return false
	}
	for i, r := range value {
		alnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !alnum && (i == 0 || (r != '-' && r != '_')) {
			return false
		}
	}
	return true
}

func validRepoName(value string) bool {
	if value == "" || len(value) > 100 || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func validRepository(value string) bool {
	owner, name, ok := strings.Cut(value, "/")
	return ok && validLogin(owner) && validRepoName(name)
}

// safeFilterValue mirrors filterValuePattern and additionally refuses padding, so a value always stays one
// quoted term of the project filter grammar.
func safeFilterValue(value string) bool {
	if value == "" || len([]rune(value)) > 100 || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\'' || r == ',' || r == '\\' || r == '*' {
			return false
		}
	}
	return true
}

func checkFilterList(name string, values []string) error {
	if len(values) > maxFilterValues {
		return invalidRequest(fmt.Sprintf("%s accepts at most %d values", name, maxFilterValues))
	}
	for _, value := range values {
		if !safeFilterValue(value) {
			return invalidRequest(name + " contains a value a filter cannot carry")
		}
	}
	return nil
}

func normalizeLimit(limit int) (int, error) {
	if limit == 0 {
		return defaultLimit, nil
	}
	if limit < 1 || limit > maxLimit {
		return 0, invalidRequest(fmt.Sprintf("limit must be between 1 and %d", maxLimit))
	}
	return limit, nil
}

// fingerprint binds a cursor to the target and the normalized filters of the request that produced it. The
// limit is not part of it: a continuation stays exact whatever batch size the next request asks for.
func fingerprint(parts ...any) []byte {
	encoded, _ := json.Marshal(parts)
	sum := sha256.Sum256(encoded)
	return sum[:cursorBinding]
}

// encodeCursor wraps a GitHub connection cursor into the opaque, filter-bound Qatlas cursor.
func encodeCursor(binding []byte, after string) string {
	return base64.RawURLEncoding.EncodeToString(append(append([]byte(nil), binding...), after...))
}

// decodeCursor returns the GitHub connection cursor a Qatlas cursor continues after, or the empty string for
// the first batch. A cursor that is malformed or belongs to other filters or another target is refused.
func decodeCursor(binding []byte, cursor string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if len(cursor) > maxCursorLength || err != nil || len(decoded) <= cursorBinding ||
		!bytes.Equal(decoded[:cursorBinding], binding) {
		return "", invalidRequest("cursor is not a next_cursor of this list")
	}
	return string(decoded[cursorBinding:]), nil
}

// endpoints are the REST root and the GraphQL endpoint of one configured GitHub service.
type endpoints struct {
	rest    string
	graphql string
}

// endpointsOf derives both API endpoints from the configured base URL. GitHub.com and GitHub Enterprise
// Cloud with data residency are bare https API origins (https://api.github.com, https://api.SUBDOMAIN.ghe.com)
// whose GraphQL endpoint is /graphql. GitHub Enterprise Server serves REST at https://HOST/api/v3 and GraphQL
// at https://HOST/api/graphql. Userinfo, a query, a fragment, or any other path is refused.
func endpointsOf(raw string) (endpoints, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return endpoints{}, errors.New("a GitHub service needs a usable https base URL")
	}
	if parsed.Scheme != "https" {
		return endpoints{}, errors.New("a GitHub service must use https")
	}
	if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Opaque != "" || parsed.ForceQuery {
		return endpoints{}, errors.New("a GitHub service must be an https URL without user, query, or fragment")
	}
	origin := "https://" + parsed.Host
	switch strings.TrimRight(parsed.Path, "/") {
	case "":
		if !strings.HasPrefix(strings.ToLower(parsed.Hostname()), "api.") {
			return endpoints{}, errors.New("a GitHub service is https://api.github.com or, for GitHub " +
				"Enterprise Server, https://HOST/api/v3")
		}
		return endpoints{rest: origin, graphql: origin + "/graphql"}, nil
	case "/api/v3":
		return endpoints{rest: origin + "/api/v3", graphql: origin + "/api/graphql"}, nil
	}
	return endpoints{}, errors.New("a GitHub Enterprise Server base URL must end in /api/v3")
}

// Client binds one GitHub token to the endpoints of one configured service, to the one target of its
// connection and the repositories that target may plan in, and to the rate limit that token shares.
type Client struct {
	endpoints endpoints
	target    target
	scope     scope
	auth      string
	http      *http.Client
	limiter   *ratelimit.Limiter
}

// Open resolves the token of one selected connection and returns a client for its configured target.
func Open(resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return open(resolved, secrets, red, nil)
}

// open is the internal seam. A caller may supply the rate limiter, and the package's own tests replace the
// transport, so no test ever reaches GitHub.
func open(resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	lim *ratelimit.Limiter) (*Client, error) {
	if resolved == nil {
		return nil, providerError("open", "no connection was selected")
	}
	bound, err := scopeOf(resolved)
	if err != nil {
		return nil, providerError("open", err.Error())
	}
	base := resolved.BaseURL
	if strings.TrimSpace(base) == "" {
		base = defaultBaseURL
	}
	api, err := endpointsOf(base)
	if err != nil {
		return nil, providerError("open", err.Error())
	}
	if secrets == nil {
		return nil, providerError("open", "no credential resolver was configured")
	}
	value, err := secrets.Resolve(resolved.Credential, resolved.Secrets, roleToken)
	if err != nil {
		return nil, err
	}
	if !validToken(value.Secret) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: "open", Message: "the GitHub token is unusable"}
	}
	if red != nil {
		red.Add(value.Secret, "Bearer "+value.Secret)
	}
	if lim == nil {
		lim = limiters.For(value.Secret)
	}
	return &Client{endpoints: api, target: bound.target, scope: bound, auth: "Bearer " + value.Secret,
		http: newHTTPClient(), limiter: lim}, nil
}

// transport carries every GitHub request. A nil value is Go's default transport; the package's own tests
// replace it with a local test server.
var transport http.RoundTripper

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   defaultTimeout,
		Transport: transport,
		// The token travels in the Authorization header, so no redirect is followed: a redirect could only
		// move a credential to a place the user never configured.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// TestConnection performs the smallest authenticated read of the configured target: the project with its
// field definitions, or the repository. Success proves that this token can see the target; it does not prove
// that every tool is authorized, because GitHub checks each resource and scope on every request.
func TestConnection(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (provider.Class, error) {
	client, err := Open(resolved, secrets, red)
	if err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return "", err
	}
	const op = "test connection"
	if client.target.kind == kindProject {
		_, err = client.project(ctx, op)
	} else {
		var repository struct {
			FullName string `json:"full_name"`
		}
		err = client.rest(ctx, op, "/repos/"+url.PathEscape(client.target.owner)+"/"+
			url.PathEscape(client.target.repo), &repository)
	}
	if err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return provider.ClassProviderError, nil
	}
	return provider.ClassOK, nil
}

// graphQLError is the part of a GraphQL error this provider inspects. The message is read for
// classification only and never copied, because GitHub echoes requested names into it. Path names the
// aliased mutation of a batch the error belongs to.
type graphQLError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Path    []any  `json:"path"`
}

// graphQLEnvelope is the answer of one GraphQL request before its data is read.
type graphQLEnvelope struct {
	Data   json.RawMessage `json:"data"`
	Errors []graphQLError  `json:"errors"`
}

// graphql performs one bounded GraphQL query. GitHub answers many failures with HTTP 200 and an errors
// list, so errors are classified even when data is present: a partial answer is never passed on.
func (c *Client) graphql(ctx context.Context, op, query string, variables map[string]any, out any) error {
	return c.graphqlRequest(ctx, op, query, variables, out, false)
}

// mutate performs one GraphQL mutation that must succeed as a whole. It is sent once and never repeated.
func (c *Client) mutate(ctx context.Context, op, document string, variables map[string]any, out any) error {
	return c.graphqlRequest(ctx, op, document, variables, out, true)
}

func (c *Client) graphqlRequest(ctx context.Context, op, document string, variables map[string]any, out any,
	change bool) error {
	envelope, err := c.post(ctx, op, document, variables, change)
	if err != nil {
		return err
	}
	if len(envelope.Errors) > 0 {
		return graphQLFailure(op, envelope.Errors, change)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" || json.Unmarshal(envelope.Data, out) != nil {
		return invalidResponse(op, change)
	}
	return nil
}

// post sends one GraphQL document and returns the undecoded answer.
func (c *Client) post(ctx context.Context, op, document string, variables map[string]any,
	change bool) (graphQLEnvelope, error) {
	var envelope graphQLEnvelope
	payload, err := json.Marshal(map[string]any{"query": document, "variables": variables})
	if err != nil {
		return envelope, providerError(op, "the request could not be built")
	}
	err = c.do(ctx, op, http.MethodPost, c.endpoints.graphql, payload, &envelope, change)
	return envelope, err
}

func graphQLFailure(op string, errs []graphQLError, change bool) *provider.Error {
	for _, e := range errs {
		message := strings.ToLower(e.Message)
		switch {
		case e.Type == "RATE_LIMITED" || strings.Contains(message, "rate limit"):
			return &provider.Error{Class: provider.ClassRateLimited, Op: op, Message: "GitHub rate-limited the operation"}
		case e.Type == "FORBIDDEN" || e.Type == "INSUFFICIENT_SCOPES":
			return &provider.Error{Class: provider.ClassPermission, Op: op, Message: permissionMessage(change)}
		case e.Type == "NOT_FOUND":
			return &provider.Error{Class: provider.ClassProviderError, Op: op,
				Message: "GitHub does not hold this resource or does not show it to this token"}
		case strings.Contains(message, "argument 'query'") || strings.Contains(message, "argument \"query\""):
			return &provider.Error{Class: provider.ClassProviderError, Op: op,
				Message: "this GitHub server does not support filtered project item queries"}
		}
	}
	if change {
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: "GitHub rejected the change"}
	}
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: "GitHub rejected the query"}
}

func permissionMessage(change bool) string {
	if change {
		return "this GitHub token may not change this resource; check its scopes or permissions"
	}
	return "this GitHub token may not read this resource; check its scopes or permissions"
}

// uncertain is appended to a failure of a change whose request may have reached GitHub: the change may
// have been applied although no confirmation arrived. Qatlas never repeats such a request by itself.
const uncertain = "; the change may have been applied, read the current state before repeating it"

func invalidResponse(op string, change bool) *provider.Error {
	message := "GitHub returned an invalid response"
	if change {
		message += uncertain
	}
	return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: message}
}

// rest performs one bounded REST read below the configured REST root.
func (c *Client) rest(ctx context.Context, op, path string, out any) error {
	return c.do(ctx, op, http.MethodGet, c.endpoints.rest+path, nil, out, false)
}

// restChange sends one REST change below the configured REST root, once, and decodes the answer.
func (c *Client) restChange(ctx context.Context, op, method, path string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	return c.do(ctx, op, method, c.endpoints.rest+path, payload, out, true)
}

// do sends one request with the shared authentication, version, and size rules and decodes the answer. A
// change is never repeated: every failure after its request may have reached GitHub says so, and the next
// request of this token waits mutationInterval.
func (c *Client) do(ctx context.Context, op, method, endpoint string, payload []byte, out any, change bool) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return &provider.Error{Class: provider.ClassTimeout, Op: op,
			Message: "the request ended while it waited for the GitHub rate limit"}
	}
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", "qatlas-cli")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(req)
	if change {
		defer c.limiter.HoldFor(mutationInterval)
	}
	if err != nil {
		failure := provider.Transport(op, "GitHub", err)
		if change && (failure.Class == provider.ClassTimeout || failure.Cause == provider.CauseConnectionReset ||
			failure.Cause == provider.CauseUnknown) {
			failure.Message += uncertain
		}
		return failure
	}
	defer response.Body.Close()
	c.observeRateLimit(response.Header)

	if response.StatusCode < 200 || response.StatusCode > 299 {
		return c.statusError(op, response, change)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		message := "the GitHub response could not be read within the size limit"
		if change {
			message += uncertain
		}
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: message}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return invalidResponse(op, change)
	}
	return nil
}

// observeRateLimit reads the primary budget headers. When the budget of this token is spent, the next
// request of this process waits for the reported reset instead of running into a refusal.
func (c *Client) observeRateLimit(header http.Header) {
	if strings.TrimSpace(header.Get("X-RateLimit-Remaining")) != "0" {
		return
	}
	reset, err := strconv.ParseInt(strings.TrimSpace(header.Get("X-RateLimit-Reset")), 10, 64)
	if err != nil {
		return
	}
	c.limiter.HoldFor(capHold(time.Until(time.Unix(reset, 0))))
}

func capHold(hold time.Duration) time.Duration {
	if hold > maxHold {
		return maxHold
	}
	return hold
}

// statusError maps an HTTP status to a stable class. The provider message is read only to recognise a
// secondary rate limit and is never copied. A server error after a change leaves its outcome open.
func (c *Client) statusError(op string, response *http.Response, change bool) error {
	err := c.classifyStatus(op, response, change)
	if change && response.StatusCode >= 500 {
		err.Message += uncertain
	}
	return err
}

func (c *Client) classifyStatus(op string, response *http.Response, change bool) *provider.Error {
	status := response.StatusCode
	snippet, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	retry := retryAfter(response.Header)
	limited := status == http.StatusTooManyRequests ||
		strings.TrimSpace(response.Header.Get("X-RateLimit-Remaining")) == "0" ||
		response.Header.Get("Retry-After") != "" ||
		bytes.Contains(bytes.ToLower(snippet), []byte("rate limit"))

	switch {
	case status == http.StatusUnauthorized:
		return &provider.Error{Class: provider.ClassAuth, Op: op, Message: "GitHub rejected the token"}
	case (status == http.StatusForbidden || status == http.StatusTooManyRequests) && limited:
		c.limiter.HoldFor(capHold(retry))
		message := "GitHub rate-limited the operation"
		if retry > 0 {
			message += fmt.Sprintf("; retry after %d seconds", int((retry+time.Second-1)/time.Second))
		}
		return &provider.Error{Class: provider.ClassRateLimited, Op: op, Message: message}
	case status == http.StatusForbidden:
		return &provider.Error{Class: provider.ClassPermission, Op: op, Message: permissionMessage(change)}
	case status == http.StatusNotFound:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "GitHub does not hold this resource or does not show it to this token"}
	case status >= 300 && status < 400:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "GitHub answered with a redirect, which Qatlas does not follow; the resource may have moved"}
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: "GitHub rejected the request as invalid"}
	case status == http.StatusGatewayTimeout:
		return &provider.Error{Class: provider.ClassTimeout, Op: op, Message: "GitHub did not answer in time"}
	}
	return &provider.Error{Class: provider.ClassProviderError, Op: op,
		Message: fmt.Sprintf("GitHub rejected the operation (HTTP %d)", status)}
}

// retryAfter reads how long GitHub asks a client to wait: Retry-After in seconds, or the reset of a spent
// primary budget. Zero means GitHub named no time.
func retryAfter(header http.Header) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(header.Get("Retry-After"))); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if strings.TrimSpace(header.Get("X-RateLimit-Remaining")) == "0" {
		if reset, err := strconv.ParseInt(strings.TrimSpace(header.Get("X-RateLimit-Reset")), 10, 64); err == nil {
			if wait := time.Until(time.Unix(reset, 0)); wait > 0 {
				return wait
			}
		}
	}
	return 0
}

func providerError(op, message string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

func invalidRequest(message string) error {
	return &application.InvalidRequestError{Message: message}
}

// validToken keeps an obviously unusable value out of a request. The real check is GitHub's.
func validToken(value string) bool {
	if len(value) < 8 || len(value) > 4096 {
		return false
	}
	for _, r := range value {
		// A header value may not carry control characters, and a GitHub token never does.
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}
