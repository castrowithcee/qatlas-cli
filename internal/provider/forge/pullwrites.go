package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The pull request change tools act on pull requests whose base repository lies inside the targets. Create
// binds the base repository by the path; every tool on an existing index first reads the pull request (the
// evidence read), so the canonical base repository of the answer is checked against the targets before
// anything is sent. Each change sends one request of a fixed method and path and is never repeated.

const (
	pullSchemaHead = `{"type":"string","minLength":1,"maxLength":300}`
	// pullSchemaFullSHA is a complete commit ID, as the head of a pull request is reported.
	pullSchemaFullSHA = `{"type":"string","pattern":"^([0-9a-fA-F]{40}|[0-9a-fA-F]{64})$"}`
	pullSchemaDo      = `{"type":"string","enum":["merge","rebase","rebase-merge","squash","fast-forward-only",` +
		`"manually-merged"]}`
	pullSchemaStyle = `{"type":"string","enum":["merge","rebase"]}`

	maxPullTitle   = 255
	maxMergeTitle  = 255
	maxMergeText   = 64 << 10
	maxReviewerIDs = 20
)

var mergeStyles = []string{"merge", "rebase", "rebase-merge", "squash", "fast-forward-only", "manually-merged"}

func checkFullSHA(name, value string) error {
	if len(value) != 40 && len(value) != 64 {
		return invalidArgument(name + " must be a complete commit ID of 40 or 64 hexadecimal digits")
	}
	if checkCommitID(value) != nil {
		return invalidArgument(name + " must be a complete commit ID of 40 or 64 hexadecimal digits")
	}
	return nil
}

// checkHead validates the head of a new pull request: a branch of the base repository, or OWNER:BRANCH for a
// branch of a fork.
func checkHead(value string) error {
	branch := value
	if owner, rest, ok := strings.Cut(value, ":"); ok {
		if !validName(owner) {
			return invalidArgument("head must be a branch name or OWNER:BRANCH")
		}
		branch = rest
	}
	if strings.Contains(branch, ":") {
		return invalidArgument("head must be a branch name or OWNER:BRANCH")
	}
	return checkBranch("head", branch)
}

func sameCommit(a, b string) bool { return strings.EqualFold(a, b) }

func pullHead(pr object) string {
	head, ok := pr.child("head")
	if !ok {
		return ""
	}
	sha, _ := head.text("sha")
	return sha
}

// pullWrite are the arguments of pull-requests.create and pull-requests.update. A pointer tells a member that
// is left out from one that is set to its empty value, which clears it.
type pullWrite struct {
	Repository      string    `json:"repository"`
	Number          int64     `json:"number"`
	Title           *string   `json:"title"`
	Body            *string   `json:"body"`
	Head            *string   `json:"head"`
	Base            *string   `json:"base"`
	Assignees       *[]string `json:"assignees"`
	Labels          *[]int64  `json:"labels"`
	Milestone       *int64    `json:"milestone"`
	DueDate         *string   `json:"due_date"`
	UnsetDueDate    *bool     `json:"unset_due_date"`
	AllowMaintainer *bool     `json:"allow_maintainer_edit"`
	Reviewers       *[]string `json:"reviewers"`
	TeamReviewers   *[]string `json:"team_reviewers"`
	ContentVersion  *int64    `json:"content_version"`
}

// body builds the JSON body of a create or an update from the arguments that are set, and refuses a member the
// product does not document or the operation does not take.
func (w pullWrite) body(p *product, creating bool) (map[string]any, error) {
	body := map[string]any{}
	if w.Title != nil {
		if length := utf8.RuneCountInString(*w.Title); length < 1 || length > maxPullTitle || !plainText(*w.Title) {
			return nil, invalidArgument("title must be 1 to 255 characters without control characters")
		}
		body["title"] = *w.Title
	} else if creating {
		return nil, invalidArgument("title is required")
	}
	if w.Body != nil {
		if utf8.RuneCountInString(*w.Body) > 65536 || !utf8.ValidString(*w.Body) {
			return nil, invalidArgument("body must be valid text of at most 65536 characters")
		}
		body["body"] = *w.Body
	}
	if creating {
		if w.Head == nil || w.Base == nil {
			return nil, invalidArgument("head and base are required")
		}
		if err := checkHead(*w.Head); err != nil {
			return nil, err
		}
		body["head"] = *w.Head
	} else if w.Head != nil {
		return nil, invalidArgument("head is set when the pull request is created")
	}
	if w.Base != nil {
		if err := checkBranch("base", *w.Base); err != nil {
			return nil, err
		}
		if strings.Contains(*w.Base, ":") {
			return nil, invalidArgument("base must be a branch name of the repository")
		}
		body["base"] = *w.Base
	}
	if w.Assignees != nil {
		if len(*w.Assignees) > maxAssignees {
			return nil, invalidArgument("assignees takes at most 10 user names")
		}
		for _, login := range *w.Assignees {
			if !validName(login) {
				return nil, invalidArgument("assignees takes user names")
			}
		}
		body["assignees"] = *w.Assignees
	}
	if w.Labels != nil {
		if err := checkIDs("labels", *w.Labels, maxLabelIDs); err != nil {
			return nil, err
		}
		body["labels"] = *w.Labels
	}
	if w.Milestone != nil {
		if *w.Milestone < 0 {
			return nil, invalidArgument("milestone must be a milestone ID, or 0 to remove it")
		}
		body["milestone"] = *w.Milestone
	}
	if w.DueDate != nil {
		if *w.DueDate == "" {
			return nil, invalidArgument("due_date must be a time in RFC 3339 format; unset_due_date removes it")
		}
		if err := checkTime("due_date", *w.DueDate); err != nil {
			return nil, err
		}
		body["due_date"] = *w.DueDate
	}
	if w.UnsetDueDate != nil {
		if creating {
			return nil, invalidArgument("unset_due_date removes the due date of an existing pull request")
		}
		if *w.UnsetDueDate && w.DueDate != nil {
			return nil, invalidArgument("pass due_date or unset_due_date, not both")
		}
		body["unset_due_date"] = *w.UnsetDueDate
	}
	if w.AllowMaintainer != nil {
		body["allow_maintainer_edit"] = *w.AllowMaintainer
	}
	for name, list := range map[string]*[]string{"reviewers": w.Reviewers, "team_reviewers": w.TeamReviewers} {
		if list == nil {
			continue
		}
		if p != gitea {
			return nil, otherProduct(p, name)
		}
		if !creating {
			return nil, invalidArgument(name + " is set when the pull request is created")
		}
		if len(*list) > maxReviewerIDs {
			return nil, invalidArgument(name + " takes at most 20 names")
		}
		for _, entry := range *list {
			if !validName(entry) {
				return nil, invalidArgument(name + " takes user or team names")
			}
		}
		body[name] = *list
	}
	if w.ContentVersion != nil {
		if p != gitea {
			return nil, otherProduct(p, "content_version")
		}
		if creating || *w.ContentVersion < 0 {
			return nil, invalidArgument("content_version is the version of an existing pull request")
		}
		body["content_version"] = *w.ContentVersion
	}
	if len(body) == 0 {
		return nil, invalidArgument("pass at least one member to change")
	}
	return body, nil
}

// pullWriteProperties are the members of the body a create or an update takes.
func pullWriteProperties(p *product, creating bool) (string, []capability.Argument) {
	properties := repositoryProperty
	arguments := []capability.Argument{repositoryArgument}
	if !creating {
		properties += `,"number":` + issueSchemaNumber
		arguments = append(arguments, pullNumberArgument)
	}
	properties += `,"title":` + issueSchemaTitle + `,"body":` + issueSchemaBody + `,"base":` + pullSchemaBranch +
		`,"assignees":{"type":"array","maxItems":10,"items":` + issueSchemaLogin + `},"labels":{"type":"array",` +
		`"maxItems":20,"items":` + issueSchemaID + `},"milestone":{"type":"integer","minimum":0,"maximum":` +
		`9007199254740991},"due_date":` + issueSchemaTime + `,"allow_maintainer_edit":{"type":"boolean"}`
	arguments = append(arguments,
		capability.Argument{Name: "title", Required: creating, Description: "Title, 1 to 255 characters"},
		capability.Argument{Name: "body", Description: "Body in Markdown, at most 65536 characters; stored as given"},
		capability.Argument{Name: "base", Required: creating, Description: "Branch the changes go into; on an " +
			"update the pull request is retargeted to it"},
		capability.Argument{Name: "assignees", Description: "User names of the assignees, at most 10; they " +
			"replace the current assignees on an update and an empty list removes them"},
		capability.Argument{Name: "labels", Description: "Numeric label IDs, at most 20; they replace the " +
			"current labels on an update"},
		capability.Argument{Name: "milestone", Description: "Numeric ID of the milestone; 0 removes it on an update"},
		capability.Argument{Name: "due_date", Description: "Due date, RFC 3339"},
		capability.Argument{Name: "allow_maintainer_edit", Description: "True lets maintainers push to the head branch"})
	if creating {
		properties += `,"head":` + pullSchemaHead
		arguments = append(arguments, capability.Argument{Name: "head", Required: true, Description: "Branch the " +
			"changes come from, or OWNER:BRANCH for a branch of a fork; the head repository is not checked against " +
			"the targets"})
	} else {
		properties += `,"unset_due_date":{"type":"boolean"}`
		arguments = append(arguments, capability.Argument{Name: "unset_due_date", Description: "True removes the " +
			"due date; not together with due_date"})
	}
	switch {
	case p == gitea && creating:
		properties += `,"reviewers":{"type":"array","maxItems":20,"items":` + issueSchemaLogin + `},` +
			`"team_reviewers":{"type":"array","maxItems":20,"items":` + issueSchemaLogin + `}`
		arguments = append(arguments,
			capability.Argument{Name: "reviewers", Description: "User names asked for a review, at most 20; Gitea only"},
			capability.Argument{Name: "team_reviewers", Description: "Team names asked for a review, at most 20; " +
				"Gitea only"})
	case p == gitea:
		properties += `,"content_version":{"type":"integer","minimum":0,"maximum":9007199254740991}`
		arguments = append(arguments, capability.Argument{Name: "content_version", Description: "content_version " +
			"of the pull request you read; Gitea then refuses the update when the title or body changed since; " +
			"Gitea only"})
	}
	return properties, arguments
}

var pullRequestsCreate = &tool{
	group: "pull-requests", action: "create", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties, arguments := pullWriteProperties(p, true)
		return capability.Descriptor{
			Title: "Create a " + p.name + " pull request",
			Description: "Create exactly one pull request into a repository inside the connection's targets; a " +
				"repeated call fails or creates a second one. The head may be a branch of a fork, whose " +
				"repository is shown by identifiers only.",
			Tags:         []string{p.id, "pull-requests", "create"},
			Risk:         changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, p.id+pullSensitivity),
			InputSchema:  inputSchema(properties, "title", "head", "base"),
			OutputSchema: json.RawMessage(schemaOf(p, pullRequestMembers, "id", "number")),
			Arguments:    arguments,
			Fields:       fieldsOf(p, pullRequestMembers),
			Examples: []capability.Example{{Description: "Open a pull request",
				Arguments: json.RawMessage(`{"repository":"example/project","title":"Fix crash","head":"feature","base":"main"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments pullWrite
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(arguments.Repository)
		if err != nil {
			return nil, err
		}
		body, err := arguments.body(p, true)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var created object
			if err := c.change(ctx, "create pull request", http.MethodPost, pullsPath(repo), body, &created); err != nil {
				return nil, err
			}
			return project(p, pullRequestMembers, created), nil
		}, nil
	},
}

var pullRequestsUpdate = &tool{
	group: "pull-requests", action: "update", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties, arguments := pullWriteProperties(p, false)
		return capability.Descriptor{
			Title: "Update a " + p.name + " pull request",
			Description: "Replace the title, body, base branch, assignees, labels, milestone, or due date of one " +
				"pull request of a repository inside the connection's targets; members left out stay unchanged. " +
				"The state is changed by pull-requests.close and pull-requests.reopen.",
			Tags:         []string{p.id, "pull-requests", "update"},
			Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+pullSensitivity),
			InputSchema:  inputSchema(properties, "number"),
			OutputSchema: json.RawMessage(schemaOf(p, pullRequestMembers, "id", "number")),
			Arguments:    arguments,
			Fields:       fieldsOf(p, pullRequestMembers),
			Examples: []capability.Example{{Description: "Retitle a pull request",
				Arguments: json.RawMessage(`{"repository":"example/project","number":7,"title":"Fix the crash on start"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments pullWrite
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		if _, err := allowed.chooseRepository(arguments.Repository); err != nil {
			return nil, err
		}
		body, err := arguments.body(p, false)
		if err != nil {
			return nil, err
		}
		return onPull(allowed, arguments.Repository, arguments.Number, "update pull request",
			func(ctx context.Context, c *Client, _ target, path string, _ object) (any, error) {
				var updated object
				if err := c.change(ctx, "update pull request", http.MethodPatch, path, body, &updated); err != nil {
					return nil, err
				}
				return project(p, pullRequestMembers, updated), nil
			})
	},
}

var pullChangedMember = member{name: "changed", kind: kindFlag, note: "False when the pull request already had " +
	"the requested state and nothing was sent"}

// pullStateTool is pull-requests.close and pull-requests.reopen: a pull request that already has the requested
// state is a success without a request. A merged pull request cannot be reopened.
func pullStateTool(action, state, verb string) *tool {
	members := func() []member { return append(append([]member(nil), pullRequestMembers...), pullChangedMember) }
	return &tool{
		group: "pull-requests", action: action, since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title: verb + " a " + p.name + " pull request",
				Description: verb + " one pull request of a repository inside the connection's targets; one that " +
					"already has this state is returned unchanged and nothing is sent. A merged pull request " +
					"cannot be reopened.",
				Tags:         []string{p.id, "pull-requests", action, "update"},
				Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+pullSensitivity),
				InputSchema:  inputSchema(pullNumberProperties, "number"),
				OutputSchema: json.RawMessage(schemaOf(p, members(), "id", "number")),
				Arguments:    []capability.Argument{repositoryArgument, pullNumberArgument},
				Fields:       fieldsOf(p, members()),
				Examples: []capability.Example{{Description: verb + " a pull request",
					Arguments: json.RawMessage(`{"repository":"example/project","number":7}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var arguments struct {
				Repository string `json:"repository"`
				Number     int64  `json:"number"`
			}
			if err := decodeArguments(raw, &arguments); err != nil {
				return nil, err
			}
			return onPull(allowed, arguments.Repository, arguments.Number, action+" pull request",
				func(ctx context.Context, c *Client, _ target, path string, pr object) (any, error) {
					if current, _ := pr.text("state"); current == state {
						result := project(p, pullRequestMembers, pr)
						result["changed"] = false
						return result, nil
					}
					if merged, _ := pr.flag("merged"); merged && state == "open" {
						return nil, invalidArgument("a merged pull request cannot be reopened")
					}
					var updated object
					if err := c.change(ctx, action+" pull request", http.MethodPatch, path,
						map[string]any{"state": state}, &updated); err != nil {
						return nil, err
					}
					result := project(p, pullRequestMembers, updated)
					result["changed"] = true
					return result, nil
				})
		},
	}
}

var (
	pullRequestsClose  = pullStateTool("close", "closed", "Close")
	pullRequestsReopen = pullStateTool("reopen", "open", "Reopen")
)

var pullRequestsCancelAutoMerge = &tool{
	group: "pull-requests", action: "cancel-auto-merge", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Cancel the scheduled merge of a " + p.name + " pull request",
			Description: "Withdraw the merge that one pull request of a repository inside the connection's " +
				"targets has scheduled for when its checks succeed. The instance answers as not found when " +
				"nothing is scheduled.",
			Tags:        []string{p.id, "pull-requests", "cancel-auto-merge", "update"},
			Risk:        changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+pullSensitivity),
			InputSchema: inputSchema(pullNumberProperties, "number"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"},` +
				`"cancelled":{"type":"boolean"}},"required":["number","cancelled"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, pullNumberArgument},
			Fields: []capability.Field{{Name: "number", Description: "Index of the pull request"},
				{Name: "cancelled", Description: "True once the instance withdrew the scheduled merge"}},
			Examples: []capability.Example{{Description: "Withdraw a scheduled merge",
				Arguments: json.RawMessage(`{"repository":"example/project","number":7}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository string `json:"repository"`
			Number     int64  `json:"number"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		return onPull(allowed, arguments.Repository, arguments.Number, "cancel auto merge",
			func(ctx context.Context, c *Client, _ target, path string, _ object) (any, error) {
				if err := c.change(ctx, "cancel auto merge", http.MethodDelete, path+"/merge", nil, nil); err != nil {
					return nil, err
				}
				return map[string]any{"number": arguments.Number, "cancelled": true}, nil
			})
	},
}

// mergeArguments are the arguments of pull-requests.merge.
type mergeArguments struct {
	Repository   string  `json:"repository"`
	Number       int64   `json:"number"`
	SHA          string  `json:"sha"`
	Do           string  `json:"do"`
	CommitID     *string `json:"merge_commit_id"`
	Title        *string `json:"merge_title"`
	Message      *string `json:"merge_message"`
	WhenChecks   *bool   `json:"merge_when_checks_succeed"`
	DeleteBranch *bool   `json:"delete_branch_after_merge"`
}

// body builds the merge body in the spelling of the product: Forgejo documents Do, MergeCommitID,
// MergeMessageField, and MergeTitleField, Gitea their snake_case forms. force_merge is never sent.
func (a mergeArguments) body(p *product) (map[string]any, error) {
	if err := checkFullSHA("sha", a.SHA); err != nil {
		return nil, err
	}
	if !contains(mergeStyles, a.Do) {
		return nil, invalidArgument("do must be one of " + strings.Join(mergeStyles, ", "))
	}
	names := map[string]string{"do": "do", "id": "merge_commit_id", "message": "merge_message_field",
		"title": "merge_title_field"}
	if p == forgejo {
		names = map[string]string{"do": "Do", "id": "MergeCommitID", "message": "MergeMessageField",
			"title": "MergeTitleField"}
	}
	body := map[string]any{names["do"]: a.Do, "head_commit_id": a.SHA}
	if a.Do == "manually-merged" {
		if a.CommitID == nil {
			return nil, invalidArgument("manually-merged needs merge_commit_id, the commit that merged the changes")
		}
	} else if a.CommitID != nil {
		return nil, invalidArgument("merge_commit_id belongs to do manually-merged")
	}
	if a.CommitID != nil {
		if err := checkCommitID(*a.CommitID); err != nil {
			return nil, invalidArgument("merge_commit_id must be a commit ID of 7 to 64 hexadecimal digits")
		}
		body[names["id"]] = *a.CommitID
	}
	if a.Title != nil {
		if utf8.RuneCountInString(*a.Title) > maxMergeTitle || !plainText(*a.Title) {
			return nil, invalidArgument("merge_title must be at most 255 characters without control characters")
		}
		body[names["title"]] = *a.Title
	}
	if a.Message != nil {
		if len(*a.Message) > maxMergeText || !utf8.ValidString(*a.Message) {
			return nil, invalidArgument("merge_message must be valid text of at most 64 KiB")
		}
		body[names["message"]] = *a.Message
	}
	if a.WhenChecks != nil {
		body["merge_when_checks_succeed"] = *a.WhenChecks
	}
	if a.DeleteBranch != nil {
		body["delete_branch_after_merge"] = *a.DeleteBranch
	}
	return body, nil
}

// headMoved names both commits when the head of a pull request is not the one the caller expected.
func headMoved(number int64, expected, current string) error {
	if current == "" {
		current = "unknown"
	}
	return invalidArgument("the head branch of pull request " + strconv.FormatInt(number, 10) + " is at " + current +
		", not at the expected " + expected + "; read the pull request again and pass its current head commit, or " +
		"accept the current one and call again")
}

// mergeFailure maps the status of a refused merge to what it means; no provider text enters the message. A
// conflict reads the pull request again to name the current head.
func mergeFailure(ctx context.Context, c *Client, allowed allowlist, path string, number int64, expected string,
	status int, err error) error {
	const op = "merge pull request"
	fail := func(message string) error {
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: c.product.name + " " + message}
	}
	switch status {
	case http.StatusConflict:
		current := ""
		if pr, readErr := readPull(ctx, c, allowed, op, path); readErr == nil {
			current = pullHead(pr)
		}
		if current != "" && sameCommit(current, expected) {
			return fail("refused to merge pull request " + strconv.FormatInt(number, 10) + " with a conflict " +
				"although its head is still the expected " + expected + "; read the pull request and its checks")
		}
		return headMoved(number, expected, current)
	case http.StatusMethodNotAllowed:
		return fail("refused to merge pull request " + strconv.FormatInt(number, 10) + " because it is not " +
			"mergeable in its current state, for example branch protection, a missing required review, a failing " +
			"check, a conflict, or a style the repository does not allow; check its merge state before trying again")
	case http.StatusLocked:
		return fail("refused to merge pull request " + strconv.FormatInt(number, 10) + " because the repository is " +
			"archived or locked")
	case http.StatusRequestEntityTooLarge:
		return fail("refused to merge pull request " + strconv.FormatInt(number, 10) + " because the repository " +
			"owner's quota is exceeded")
	}
	return err
}

var pullMergeMembers = []member{
	{name: "number", kind: kindInteger, note: "Index of the pull request"},
	{name: "merged", kind: kindFlag, note: "True once the pull request is merged"},
	{name: "changed", kind: kindFlag, note: "False when the pull request was already merged with the expected " +
		"head and nothing was sent"},
	{name: "auto_merge_scheduled", kind: kindFlag, note: "True when the merge was scheduled for when the checks " +
		"succeed and has not happened yet"},
	{name: "merge_commit_sha", kind: kindText, note: "Commit the merge created, when the instance reports it"},
	{name: "head_sha", kind: kindText, note: "Head commit the merge was checked against"},
}

var pullRequestsMerge = &tool{
	group: "pull-requests", action: "merge", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties := pullNumberProperties + `,"sha":` + pullSchemaFullSHA + `,"do":` + pullSchemaDo +
			`,"merge_commit_id":` + pullSchemaSHA + `,"merge_title":` + issueSchemaTitle + `,"merge_message":` +
			`{"type":"string","maxLength":65536},"merge_when_checks_succeed":{"type":"boolean"},` +
			`"delete_branch_after_merge":{"type":"boolean"}`
		return capability.Descriptor{
			Title: "Merge a " + p.name + " pull request",
			Description: "Merge one pull request of a repository inside the connection's targets, only while its " +
				"head is still sha. Qatlas reads the pull request first: one already merged with exactly this head " +
				"is a success without a request, one merged with another head or whose head is not sha is " +
				"refused, and otherwise exactly one merge request is sent and never repeated. The instance applies " +
				"its protections, so a merge it does not allow fails. A merge cannot be undone. Only a connection " +
				"whose tools list names it offers this tool.",
			Tags:                  []string{p.id, "pull-requests", "merge", "update"},
			Risk:                  changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+pullSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(properties, "number", "sha", "do"),
			OutputSchema:          json.RawMessage(schemaOf(p, pullMergeMembers, "number", "merged")),
			Arguments: []capability.Argument{repositoryArgument, pullNumberArgument,
				{Name: "sha", Required: true, Description: "Complete head commit the pull request must still have"},
				{Name: "do", Required: true, Description: "How to merge, one of " + strings.Join(mergeStyles, ", ")},
				{Name: "merge_commit_id", Description: "Commit that merged the changes; required by, and only for, " +
					"manually-merged"},
				{Name: "merge_title", Description: "Title of the merge commit, at most 255 characters"},
				{Name: "merge_message", Description: "Message of the merge commit, at most 64 KiB"},
				{Name: "merge_when_checks_succeed", Description: "True schedules the merge for when the checks " +
					"succeed; pull-requests.cancel-auto-merge withdraws it"},
				{Name: "delete_branch_after_merge", Description: "True deletes the head branch after the merge"}},
			Fields: fieldsOf(p, pullMergeMembers),
			Examples: []capability.Example{{Description: "Squash-merge a pull request at its head",
				Arguments: json.RawMessage(`{"repository":"example/project","number":7,` +
					`"sha":"0123456789abcdef0123456789abcdef01234567","do":"squash"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments mergeArguments
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		if _, err := allowed.chooseRepository(arguments.Repository); err != nil {
			return nil, err
		}
		body, err := arguments.body(p)
		if err != nil {
			return nil, err
		}
		return onPull(allowed, arguments.Repository, arguments.Number, "merge pull request",
			func(ctx context.Context, c *Client, _ target, path string, pr object) (any, error) {
				head := pullHead(pr)
				if merged, _ := pr.flag("merged"); merged {
					if !sameCommit(head, arguments.SHA) {
						return nil, invalidArgument("pull request " + strconv.FormatInt(arguments.Number, 10) +
							" is already merged with head commit " + head + ", not " + arguments.SHA)
					}
					result := map[string]any{"number": arguments.Number, "merged": true, "changed": false,
						"head_sha": head}
					if sha, ok := pr.text("merge_commit_sha"); ok && sha != "" {
						result["merge_commit_sha"] = sha
					}
					return result, nil
				}
				if !sameCommit(head, arguments.SHA) {
					return nil, headMoved(arguments.Number, arguments.SHA, head)
				}
				status, err := c.changeQuery(ctx, "merge pull request", http.MethodPost, path+"/merge", nil, body, nil)
				if err != nil {
					return nil, mergeFailure(ctx, c, allowed, path, arguments.Number, arguments.SHA, status, err)
				}
				result := map[string]any{"number": arguments.Number, "merged": false, "changed": true,
					"head_sha": arguments.SHA}
				// The merge request is answered without a body; the pull request is read once more for the result.
				if after, readErr := readPull(ctx, c, allowed, "merge pull request", path); readErr == nil {
					merged, _ := after.flag("merged")
					result["merged"] = merged
					if sha, ok := after.text("merge_commit_sha"); ok && sha != "" {
						result["merge_commit_sha"] = sha
					}
				}
				if scheduled := arguments.WhenChecks; scheduled != nil && *scheduled && result["merged"] == false {
					result["auto_merge_scheduled"] = true
				}
				return result, nil
			})
	},
}

var pullBranchUpdateMembers = []member{
	{name: "number", kind: kindInteger, note: "Index of the pull request"},
	{name: "updated", kind: kindFlag, note: "True once the instance updated the head branch"},
	{name: "head_sha", kind: kindText, note: "Head commit the update was checked against, before the update"},
}

var pullRequestBranchesUpdate = &tool{
	group: "pull-request-branches", action: "update", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties := pullNumberProperties + `,"expected_head_sha":` + pullSchemaFullSHA + `,"style":` + pullSchemaStyle
		return capability.Descriptor{
			Title: "Update the head branch of a " + p.name + " pull request",
			Description: "Bring the base branch of one pull request of a repository inside the connection's targets " +
				"into its head branch by a merge or a rebase, which changes the head branch. The instance takes no " +
				"head condition, so Qatlas compares expected_head_sha with the head it read just before the request; " +
				"a push in between is not detected. Only a connection whose tools list names it offers this tool.",
			Tags:                  []string{p.id, "pull-request-branches", "update"},
			Risk:                  changeRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent, p.id+pullSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(properties, "number", "expected_head_sha"),
			OutputSchema:          json.RawMessage(schemaOf(p, pullBranchUpdateMembers, "number", "updated")),
			Arguments: []capability.Argument{repositoryArgument, pullNumberArgument,
				{Name: "expected_head_sha", Required: true, Description: "Complete head commit the pull request " +
					"must still have"},
				{Name: "style", Description: "merge or rebase; the instance default when omitted"}},
			Fields: fieldsOf(p, pullBranchUpdateMembers),
			Examples: []capability.Example{{Description: "Merge the base branch into the head branch",
				Arguments: json.RawMessage(`{"repository":"example/project","number":7,` +
					`"expected_head_sha":"0123456789abcdef0123456789abcdef01234567","style":"merge"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository      string `json:"repository"`
			Number          int64  `json:"number"`
			ExpectedHeadSHA string `json:"expected_head_sha"`
			Style           string `json:"style"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		if _, err := allowed.chooseRepository(arguments.Repository); err != nil {
			return nil, err
		}
		if err := checkFullSHA("expected_head_sha", arguments.ExpectedHeadSHA); err != nil {
			return nil, err
		}
		query := url.Values{}
		if arguments.Style != "" {
			if arguments.Style != "merge" && arguments.Style != "rebase" {
				return nil, invalidArgument("style must be merge or rebase")
			}
			query.Set("style", arguments.Style)
		}
		return onPull(allowed, arguments.Repository, arguments.Number, "update pull request branch",
			func(ctx context.Context, c *Client, _ target, path string, pr object) (any, error) {
				head := pullHead(pr)
				if !sameCommit(head, arguments.ExpectedHeadSHA) {
					return nil, headMoved(arguments.Number, arguments.ExpectedHeadSHA, head)
				}
				status, err := c.changeQuery(ctx, "update pull request branch", http.MethodPost, path+"/update", query,
					nil, nil)
				if err != nil {
					return nil, branchUpdateFailure(c, arguments.Number, status, err)
				}
				return map[string]any{"number": arguments.Number, "updated": true, "head_sha": head}, nil
			})
	},
}

// branchUpdateFailure names what a refused branch update means; no provider text enters the message.
func branchUpdateFailure(c *Client, number int64, status int, err error) error {
	const op = "update pull request branch"
	name := "pull request " + strconv.FormatInt(number, 10)
	switch status {
	case http.StatusConflict:
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: c.product.name +
			" could not update the head branch of " + name + " without a conflict; resolve it in the branches"}
	case http.StatusUnprocessableEntity:
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: c.product.name +
			" rejected the update of the head branch of " + name + ", for example because it is already up to " +
			"date or the token may not push to it"}
	case http.StatusRequestEntityTooLarge:
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: c.product.name +
			" refused the update of the head branch of " + name + " because the owner's quota is exceeded"}
	}
	return err
}
