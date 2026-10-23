package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The project tools act on the project boards of Gitea 28 and later, on three levels that the handler picks from
// the arguments: a repository (repository), an organization (owner as orgs/NAME), or a user (owner as
// users/NAME). Columns and issues hang on the project's path. Forgejo has no project API.
//
// Every level has its own fixed path. For a user, the API serves only the public list of another user; every
// other operation works on the account behind the token (/user/projects), so users/NAME is accepted for them only
// when the evidence read of the account names that user.
//
// A project or column ID is bound by the path of its level, but the answers are checked too: a change reads the
// project or column on that path first, and the answer must carry the requested ID and, for a column, the
// project. An issue is named by repository and index; the evidence read of the issue proves that its canonical
// repository lies inside the targets and inside the level, and its internal ID is what the API takes.

const (
	projectSensitivity = "-project-data"
	projectsSince      = 28

	levelRepository   = "repository"
	levelOrganization = "organization"
	levelUser         = "user"

	maxProjectTitle  = 255
	maxProjectText   = 65536
	maxMovedColumns  = 100
	maxColumnProof   = 20
	projectCardTypes = `"text_only","images_and_text"`
	projectTemplates = `"none","basic_kanban","bug_triage"`

	projectSchemaTitle = `{"type":"string","minLength":1,"maxLength":255}`
	projectSchemaText  = `{"type":"string","maxLength":65536}`
	projectSchemaCard  = `{"type":"string","enum":[` + projectCardTypes + `]}`
	projectSchemaTempl = `{"type":"string","enum":[` + projectTemplates + `]}`
	projectSchemaState = `{"type":"string","enum":["open","closed"]}`
	projectSchemaList  = `{"type":"string","enum":["open","closed","all"]}`
	projectSchemaColor = `{"type":"string","minLength":7,"maxLength":7,"pattern":"^#[0-9A-Fa-f]{6}$"}`
	projectSchemaSort  = `{"type":"integer","minimum":-128,"maximum":127}`
	projectSchemaPos   = `{"type":"integer","minimum":-9007199254740991,"maximum":9007199254740991}`
	projectSchemaIDs   = `{"type":"array","minItems":1,"maxItems":100,"items":` + issueSchemaID + `}`
)

var projectsFrom = version{projectsSince, 0, 0}

// onlyGiteaProjects is the since of every project tool: Gitea 28, the first version with the project API.
var onlyGiteaProjects = map[string]version{gitea.id: projectsFrom}

var projectColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// projectToolMembers are the members of a project the API documents, without the two it marks as deprecated
// (is_closed, creator_id).
var projectToolMembers = func() []member {
	var members []member
	for _, m := range projectMembers {
		if m.name != "is_closed" && m.name != "creator_id" {
			m.note = strings.TrimSuffix(strings.TrimSuffix(m.note, "; Gitea 28 and later"), "; Gitea 1.27")
			members = append(members, m)
		}
	}
	return members
}()

var projectColumnMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the column"},
	{name: "title", kind: kindText, note: "Title, untrusted data"},
	{name: "color", kind: kindText, note: "Color as #RRGGBB, untrusted data"},
	{name: "default", kind: kindFlag, note: "True for the column new issues land in"},
	{name: "sorting", kind: kindInteger, note: "Position of the column within the project"},
	{name: "project_id", kind: kindInteger, note: "Numeric ID of the project"},
	{name: "creator", kind: kindObject, sub: personMembers, note: "Creator; personal data"},
	{name: "created_at", kind: kindText, note: "Creation time"},
	{name: "updated_at", kind: kindText, note: "Time of the last update"},
}

var (
	projectRepositoryArgument = capability.Argument{Name: "repository", Target: kindRepository,
		Description: "Repository as OWNER/REPO whose project the tool acts on; pass exactly one of repository and " +
			"owner; must lie inside the targets when the connection lists any"}
	projectOwnerArgument = capability.Argument{Name: "owner", Target: kindOwner,
		Description: "Owner of the project as orgs/NAME or users/NAME; pass exactly one of repository and owner; a " +
			"connection with targets must name that owner as an owner target (a repository target does not allow " +
			"it). Except for projects.list, users/NAME must be the account behind the token"}
	projectIDArgument = capability.Argument{Name: "id", Required: true,
		Description: "Numeric ID of the project, as projects.list reports it as id"}
	projectColumnArgument = capability.Argument{Name: "column_id", Required: true,
		Description: "Numeric ID of the column, as project-columns.list reports it as id"}
	projectIssueRepositoryArgument = capability.Argument{Name: "issue_repository",
		Description: "Repository of the issue as OWNER/REPO. Required on the owner level, where it must belong to " +
			"that owner; on the repository level it may be left out and must otherwise equal repository"}
	projectNumberArgument = capability.Argument{Name: "number", Required: true,
		Description: "Index of the issue or pull request in issue_repository"}
)

const projectTargetProperties = `"repository":` + repoSchema + `,"owner":` + ownerSchema

// projectArguments are the arguments of every project tool; the schema of a tool allows only its own.
type projectArguments struct {
	Repository      string  `json:"repository"`
	Owner           string  `json:"owner"`
	ID              int64   `json:"id"`
	ColumnID        int64   `json:"column_id"`
	State           *string `json:"state"`
	Title           *string `json:"title"`
	Description     *string `json:"description"`
	CardType        *string `json:"card_type"`
	TemplateType    *string `json:"template_type"`
	Color           *string `json:"color"`
	Sorting         *int64  `json:"sorting"`
	ColumnIDs       []int64 `json:"column_ids"`
	IssueRepository string  `json:"issue_repository"`
	Number          int64   `json:"number"`
	Position        *int64  `json:"position"`
	Limit           int     `json:"limit"`
	Cursor          string  `json:"cursor"`
}

// projectLevel is the level a call acts on, with the fixed path of its projects.
type projectLevel struct {
	kind string
	base string // path of the projects, up to and including /projects
	name string // repository as OWNER/REPO, owner as orgs/NAME or users/NAME
	// owner is the user or organization name of the level; for a repository the owner of the repository.
	owner string
	// login is the user the evidence read of the account must name; empty when no such proof is needed.
	login string
}

// ownsRepository reports whether the canonical OWNER/REPO lies on this level.
func (l projectLevel) ownsRepository(fullName string) bool {
	if l.kind == levelRepository {
		return strings.EqualFold(fullName, l.name)
	}
	owner, _, ok := strings.Cut(fullName, "/")
	return ok && strings.EqualFold(owner, l.owner)
}

// namesOwner reports whether the list admits the owner by an owner target of the same scope.
func (a allowlist) namesOwner(owner target) bool {
	if len(a) == 0 {
		return true
	}
	for _, entry := range a {
		if entry.kind == kindOwner && entry.scope == owner.scope && strings.EqualFold(entry.owner, owner.owner) {
			return true
		}
	}
	return false
}

// level chooses the level from exactly one of repository and owner and checks it against the targets. listing
// selects the public list of another user, which the API serves for reading only.
func (a projectArguments) level(allowed allowlist, listing bool) (projectLevel, error) {
	if (a.Repository == "") == (a.Owner == "") {
		return projectLevel{}, invalidArgument("pass exactly one of repository and owner")
	}
	if a.Repository != "" {
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return projectLevel{}, err
		}
		return projectLevel{kind: levelRepository, name: repo.owner + "/" + repo.repo, owner: repo.owner,
			base: "/repos/" + escape(repo.owner) + "/" + escape(repo.repo) + "/projects"}, nil
	}
	owner, err := parseOwnerArgument(a.Owner)
	if err != nil {
		return projectLevel{}, err
	}
	if !allowed.namesOwner(owner) {
		return projectLevel{}, outsideTargets("owner")
	}
	switch {
	case owner.scope == "orgs":
		return projectLevel{kind: levelOrganization, name: owner.String(), owner: owner.owner,
			base: "/orgs/" + escape(owner.owner) + "/projects"}, nil
	case listing:
		return projectLevel{kind: levelUser, name: owner.String(), owner: owner.owner,
			base: "/users/" + escape(owner.owner) + "/projects"}, nil
	}
	return projectLevel{kind: levelUser, name: owner.String(), owner: owner.owner, login: owner.owner,
		base: "/user/projects"}, nil
}

func (l projectLevel) project(id int64) string { return l.base + "/" + strconv.FormatInt(id, 10) }
func (l projectLevel) columns(id int64) string { return l.project(id) + "/columns" }
func (l projectLevel) column(id, column int64) string {
	return l.columns(id) + "/" + strconv.FormatInt(column, 10)
}

// verifyAccount is the evidence read of a change on the user level: the path /user/projects acts on the account
// behind the token, so the user named by owner must be that account.
func (l projectLevel) verifyAccount(ctx context.Context, c *Client, op string) error {
	if l.login == "" {
		return nil
	}
	var account object
	if _, err := c.get(ctx, op, "/user", nil, &account); err != nil {
		return err
	}
	login, ok := account.text("login")
	if !ok || login == "" {
		return provider.InvalidResponse(op, c.product.name+" returned an account without a login")
	}
	if !strings.EqualFold(login, l.login) {
		return invalidArgument("projects of a user can be read by name only; every other operation acts on the " +
			"account behind the token, which is not the user in owner")
	}
	return nil
}

func checkProjectID(a projectArguments) error {
	if a.ID < 1 {
		return invalidArgument("id must be the numeric ID of a project")
	}
	return nil
}

func checkColumnID(a projectArguments) error {
	if a.ColumnID < 1 {
		return invalidArgument("column_id must be the numeric ID of a column")
	}
	return nil
}

// readProject is the evidence read before a change and the whole of projects.get.
func readProject(ctx context.Context, c *Client, op string, l projectLevel, id int64) (object, error) {
	var project object
	if _, err := c.get(ctx, op, l.project(id), nil, &project); err != nil {
		return nil, err
	}
	if got, ok := project.integer("id"); !ok || got != id {
		return nil, provider.InvalidResponse(op, c.product.name+" returned a project without the requested ID")
	}
	return project, nil
}

// readColumn reads a column on the path of its project; the answer must carry the column ID and the project.
func readColumn(ctx context.Context, c *Client, op string, l projectLevel, id, column int64) (object, error) {
	var answer object
	if _, err := c.get(ctx, op, l.column(id, column), nil, &answer); err != nil {
		return nil, err
	}
	got, ok := answer.integer("id")
	owner, hasProject := answer.integer("project_id")
	if !ok || got != column || !hasProject || owner != id {
		return nil, provider.InvalidResponse(op, c.product.name+" returned a column without the requested IDs")
	}
	return answer, nil
}

// issueRepository validates issue_repository against the level and the targets before any request.
func (a projectArguments) issueRepository(allowed allowlist, l projectLevel) (target, error) {
	if a.Number < 1 {
		return target{}, invalidArgument("number must be the index of an issue or pull request")
	}
	if l.kind == levelRepository && a.IssueRepository == "" {
		owner, repo, _ := strings.Cut(l.name, "/")
		return target{kind: kindRepository, owner: owner, repo: repo}, nil
	}
	if a.IssueRepository == "" {
		return target{}, invalidArgument("issue_repository is required on the owner level")
	}
	repo, err := allowed.chooseRepository(a.IssueRepository)
	if err != nil {
		return target{}, err
	}
	if !l.ownsRepository(repo.owner + "/" + repo.repo) {
		return target{}, invalidArgument("issue_repository must be the repository of the project, or belong to " +
			"its owner")
	}
	return repo, nil
}

// bindIssue is the evidence read of an issue or pull request: the canonical repository of the answer must lie
// inside the targets and the level, and the internal ID it carries is what the project API takes.
func bindIssue(ctx context.Context, c *Client, allowed allowlist, op string, l projectLevel, repo target,
	number int64) (int64, error) {
	var issue object
	if _, err := c.get(ctx, op, issuePath(repo, number), nil, &issue); err != nil {
		return 0, err
	}
	if got, ok := issue.integer("number"); !ok || got != number {
		return 0, provider.InvalidResponse(op, c.product.name+" returned an issue without its index")
	}
	name, ok := issueRepository(issue)
	if !ok || !allowed.allowsFullName(name) {
		return 0, outsideTargets("issue_repository")
	}
	if !l.ownsRepository(name) {
		return 0, invalidArgument("issue_repository must be the repository of the project, or belong to its owner")
	}
	id, ok := issue.integer("id")
	if !ok || id < 1 {
		return 0, provider.InvalidResponse(op, c.product.name+" returned an issue without an ID")
	}
	return id, nil
}

func checkProjectText(name string, value *string, limit int, plain bool) error {
	if value == nil {
		return nil
	}
	length := utf8.RuneCountInString(*value)
	if length > limit || !utf8.ValidString(*value) || strings.ContainsRune(*value, 0) || (plain && !plainText(*value)) {
		return invalidArgument(name + " must be valid text of at most " + strconv.Itoa(limit) + " characters" +
			map[bool]string{true: " without control characters", false: ""}[plain])
	}
	return nil
}

// projectBody builds the body of a create or update of a project from the members that are set.
func (a projectArguments) projectBody(creating bool) (map[string]any, error) {
	body := map[string]any{}
	if a.Title != nil {
		if utf8.RuneCountInString(*a.Title) < 1 {
			return nil, invalidArgument("title must be 1 to 255 characters without control characters")
		}
		if err := checkProjectText("title", a.Title, maxProjectTitle, true); err != nil {
			return nil, err
		}
		body["title"] = *a.Title
	} else if creating {
		return nil, invalidArgument("title is required")
	}
	if err := checkProjectText("description", a.Description, maxProjectText, false); err != nil {
		return nil, err
	}
	if a.Description != nil {
		body["description"] = *a.Description
	}
	if a.CardType != nil {
		if !contains([]string{"text_only", "images_and_text"}, *a.CardType) {
			return nil, invalidArgument("card_type must be text_only or images_and_text")
		}
		body["card_type"] = *a.CardType
	}
	if a.TemplateType != nil {
		if !creating || !contains([]string{"none", "basic_kanban", "bug_triage"}, *a.TemplateType) {
			return nil, invalidArgument("template_type is none, basic_kanban, or bug_triage and is set on creation only")
		}
		body["template_type"] = *a.TemplateType
	}
	if a.State != nil {
		if creating || !contains([]string{"open", "closed"}, *a.State) {
			return nil, invalidArgument("state must be open or closed and is set on an existing project")
		}
		body["state"] = *a.State
	}
	if len(body) == 0 {
		return nil, invalidArgument("pass at least one member to change")
	}
	return body, nil
}

// columnBody builds the body of a create or update of a column.
func (a projectArguments) columnBody(creating bool) (map[string]any, error) {
	body := map[string]any{}
	if a.Title != nil {
		if utf8.RuneCountInString(*a.Title) < 1 {
			return nil, invalidArgument("title must be 1 to 255 characters without control characters")
		}
		if err := checkProjectText("title", a.Title, maxProjectTitle, true); err != nil {
			return nil, err
		}
		body["title"] = *a.Title
	} else if creating {
		return nil, invalidArgument("title is required")
	}
	if a.Color != nil {
		if !projectColor.MatchString(*a.Color) {
			return nil, invalidArgument("color must be 6 hexadecimal digits after a #")
		}
		body["color"] = *a.Color
	}
	if a.Sorting != nil {
		if creating || *a.Sorting < -128 || *a.Sorting > 127 {
			return nil, invalidArgument("sorting is a position from -128 through 127 and is set on an existing column")
		}
		body["sorting"] = *a.Sorting
	}
	if len(body) == 0 {
		return nil, invalidArgument("pass at least one member to change")
	}
	return body, nil
}

// projectSpec is one project tool. build checks the arguments against the level and returns the operation; the
// wrapper has already chosen the level and, for a user, added the evidence read of the account in front of it.
type projectSpec struct {
	group, action string
	title         string
	summary       string
	risk          func(p *product) capability.Risk
	listedOnly    bool
	properties    string
	required      []string
	arguments     []capability.Argument
	output        func(p *product) string
	fields        func(p *product) []capability.Field
	example       string
	listing       bool
	build         func(p *product, allowed allowlist, l projectLevel, a projectArguments) (run, error)
}

func (s projectSpec) tool() *tool {
	return &tool{
		group: s.group, action: s.action, since: onlyGiteaProjects,
		describe: func(p *product) capability.Descriptor {
			arguments := append([]capability.Argument{projectRepositoryArgument, projectOwnerArgument}, s.arguments...)
			return capability.Descriptor{
				Title: s.title, Description: s.summary,
				Tags:                  []string{p.id, s.group, s.action},
				Risk:                  s.risk(p),
				RequiresToolAllowList: s.listedOnly,
				InputSchema:           inputSchema(projectTargetProperties+s.properties, s.required...),
				OutputSchema:          json.RawMessage(s.output(p)),
				Arguments:             arguments,
				Fields:                s.fields(p),
				Examples: []capability.Example{{Description: s.title,
					Arguments: json.RawMessage(s.example)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var arguments projectArguments
			if err := decodeArguments(raw, &arguments); err != nil {
				return nil, err
			}
			l, err := arguments.level(allowed, s.listing)
			if err != nil {
				return nil, err
			}
			operation, err := s.build(p, allowed, l, arguments)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				if err := l.verifyAccount(ctx, c, s.action+" "+s.group); err != nil {
					return nil, err
				}
				return operation(ctx, c)
			}, nil
		},
	}
}

func projectRead(p *product) capability.Risk { return readRisk(p.id + projectSensitivity) }

func projectChange(effect capability.Effect, idempotency capability.Idempotency) func(p *product) capability.Risk {
	return func(p *product) capability.Risk { return changeRisk(effect, idempotency, p.id+projectSensitivity) }
}

func issueChange(p *product) capability.Risk {
	return changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+issueSensitivity)
}

func projectObjectOutput(p *product) string { return schemaOf(p, projectToolMembers, "id", "title") }
func projectObjectFields(p *product) []capability.Field {
	return fieldsOf(p, projectToolMembers)
}
func columnObjectOutput(p *product) string { return schemaOf(p, projectColumnMembers, "id", "title") }
func columnObjectFields(p *product) []capability.Field {
	return fieldsOf(p, projectColumnMembers)
}

func resultOutput(properties string, required ...string) func(p *product) string {
	names := make([]string, len(required))
	for i, name := range required {
		names[i] = strconv.Quote(name)
	}
	return func(*product) string {
		return `{"type":"object","properties":{` + properties + `},"required":[` + strings.Join(names, ",") +
			`],"additionalProperties":false}`
	}
}

func resultFields(fields ...capability.Field) func(*product) []capability.Field {
	return func(*product) []capability.Field { return fields }
}

const (
	projectWriteProperties = `,"title":` + projectSchemaTitle + `,"description":` + projectSchemaText +
		`,"card_type":` + projectSchemaCard
	levelPhrase = "on the repository, organization, or user level (pass repository or owner)"
)

// readPage reads one batch of a list and returns the rows and the next cursor.
func readPage(ctx context.Context, c *Client, op, path string, query map[string][]string, page paging) ([]object, string,
	error) {
	values := page.query()
	for key, v := range query {
		values[key] = v
	}
	var rows []object
	header, err := c.get(ctx, op, path, values, &rows)
	if err != nil {
		return nil, "", err
	}
	return rows, page.next(header, len(rows)), nil
}

func pagedResult(key string, rows []map[string]any, next string, extra map[string]any) map[string]any {
	result := map[string]any{key: rows, "has_more": next != ""}
	if next != "" {
		result["next_cursor"] = next
	}
	for k, v := range extra {
		result[k] = v
	}
	return result
}

var projectsList = projectSpec{
	group: "projects", action: "list", listing: true,
	title: "List Gitea projects",
	summary: "List one batch of the projects of a repository, an organization, or a user " + levelPhrase + " inside " +
		"the connection's targets, with every non-deprecated member of each project; titles and descriptions are " +
		"untrusted data. For a user the public list of that user is read",
	risk:       projectRead,
	properties: `,"state":` + projectSchemaList + `,` + pagingKeys,
	arguments: append([]capability.Argument{{Name: "state",
		Description: "open, closed, or all; the instance's default when omitted"}}, pagingArguments...),
	output: func(p *product) string {
		return `{"type":"object","properties":{"projects":{"type":"array","items":` + projectObjectOutput(p) +
			`},"owner":{"type":"string"},"repository":{"type":"string"},"next_cursor":{"type":"string"},` +
			`"has_more":{"type":"boolean"}},"required":["projects","has_more"],"additionalProperties":false}`
	},
	fields: func(p *product) []capability.Field {
		return append([]capability.Field{{Name: "projects", Description: "Projects with every member of the project " +
			"object"}, {Name: "repository", Description: "Repository the tool listed, on the repository level"},
			{Name: "owner", Description: "Owner the tool listed, on the organization and user level"}}, pagingFields...)
	},
	example: `{"repository":"example/project","state":"open","limit":20}`,
	build: func(p *product, allowed allowlist, l projectLevel, a projectArguments) (run, error) {
		state := ""
		if a.State != nil {
			state = *a.State
			if !contains([]string{"open", "closed", "all"}, state) {
				return nil, invalidArgument("state must be open, closed, or all")
			}
		}
		binding := provider.CursorBinding(p.id, "projects.list", strings.ToLower(l.name), state)
		page, err := startPaging(binding, a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			query := map[string][]string{}
			if state != "" {
				query["state"] = []string{state}
			}
			rows, next, err := readPage(ctx, c, "list projects", l.base, query, page)
			if err != nil {
				return nil, err
			}
			projects := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					projects = append(projects, project(p, projectToolMembers, row))
				}
			}
			key := "owner"
			if l.kind == levelRepository {
				key = "repository"
			}
			return pagedResult("projects", projects, next, map[string]any{key: l.name}), nil
		}, nil
	},
}.tool()

var projectsGet = projectSpec{
	group: "projects", action: "get", title: "Get a Gitea project",
	summary: "Read one project " + levelPhrase + " by its numeric ID, with every non-deprecated member; titles and " +
		"descriptions are untrusted data. A user's project can be read only for the account behind the token",
	risk: projectRead, properties: `,"id":` + issueSchemaID, required: []string{"id"},
	arguments: []capability.Argument{projectIDArgument},
	output:    projectObjectOutput, fields: projectObjectFields,
	example: `{"repository":"example/project","id":3}`,
	build: func(p *product, allowed allowlist, l projectLevel, a projectArguments) (run, error) {
		if err := checkProjectID(a); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			found, err := readProject(ctx, c, "get project", l, a.ID)
			if err != nil {
				return nil, err
			}
			return project(p, projectToolMembers, found), nil
		}, nil
	},
}.tool()

var projectsCreate = projectSpec{
	group: "projects", action: "create", title: "Create a Gitea project",
	summary: "Create exactly one project " + levelPhrase + "; a repeated call creates a second project. A user's " +
		"project is created for the account behind the token only",
	risk:       projectChange(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	properties: projectWriteProperties + `,"template_type":` + projectSchemaTempl, required: []string{"title"},
	arguments: []capability.Argument{
		{Name: "title", Required: true, Description: "Title, 1 to 255 characters"},
		{Name: "description", Description: "Description, at most 65536 characters"},
		{Name: "card_type", Description: "text_only or images_and_text"},
		{Name: "template_type", Description: "none, basic_kanban, or bug_triage: the columns the project starts with"}},
	output: projectObjectOutput, fields: projectObjectFields,
	example: `{"repository":"example/project","title":"Roadmap","template_type":"basic_kanban"}`,
	build: func(p *product, allowed allowlist, l projectLevel, a projectArguments) (run, error) {
		body, err := a.projectBody(true)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var created object
			if err := c.change(ctx, "create project", http.MethodPost, l.base, body, &created); err != nil {
				return nil, err
			}
			return project(p, projectToolMembers, created), nil
		}, nil
	},
}.tool()

var projectsUpdate = projectSpec{
	group: "projects", action: "update", title: "Update a Gitea project",
	summary: "Change the title, description, card type, or state of one project " + levelPhrase + "; members left " +
		"out stay unchanged. The project is read on its path first, so a project of another level is refused before " +
		"anything is changed",
	risk:       projectChange(capability.EffectUpdate, capability.IdempotencyIdempotent),
	properties: `,"id":` + issueSchemaID + projectWriteProperties + `,"state":` + projectSchemaState,
	required:   []string{"id"},
	arguments: []capability.Argument{projectIDArgument,
		{Name: "title", Description: "Title, 1 to 255 characters"},
		{Name: "description", Description: "Description, at most 65536 characters; an empty text removes it"},
		{Name: "card_type", Description: "text_only or images_and_text"},
		{Name: "state", Description: "open or closed"}},
	output: projectObjectOutput, fields: projectObjectFields,
	example: `{"repository":"example/project","id":3,"state":"closed"}`,
	build: func(p *product, allowed allowlist, l projectLevel, a projectArguments) (run, error) {
		if err := checkProjectID(a); err != nil {
			return nil, err
		}
		body, err := a.projectBody(false)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if _, err := readProject(ctx, c, "update project", l, a.ID); err != nil {
				return nil, err
			}
			var updated object
			if err := c.change(ctx, "update project", http.MethodPatch, l.project(a.ID), body, &updated); err != nil {
				return nil, err
			}
			return project(p, projectToolMembers, updated), nil
		}, nil
	},
}.tool()

var projectsDelete = projectSpec{
	group: "projects", action: "delete", title: "Delete a Gitea project",
	summary: "Delete one project " + levelPhrase + " for good, with its columns; the issues stay but leave the " +
		"board, and this cannot be undone. Only a connection whose tools list names it offers this tool. The " +
		"project is read on its path first",
	risk: projectChange(capability.EffectDelete, capability.IdempotencyIdempotent), listedOnly: true,
	properties: `,"id":` + issueSchemaID, required: []string{"id"},
	arguments: []capability.Argument{projectIDArgument},
	output:    resultOutput(`"id":{"type":"integer"},"deleted":{"type":"boolean"}`, "id", "deleted"),
	fields: resultFields(capability.Field{Name: "id", Description: "Numeric ID of the project"},
		capability.Field{Name: "deleted", Description: "True once the instance deleted the project"}),
	example: `{"repository":"example/project","id":3}`,
	build: func(p *product, allowed allowlist, l projectLevel, a projectArguments) (run, error) {
		if err := checkProjectID(a); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if _, err := readProject(ctx, c, "delete project", l, a.ID); err != nil {
				return nil, err
			}
			if err := c.change(ctx, "delete project", http.MethodDelete, l.project(a.ID), nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"id": a.ID, "deleted": true}, nil
		}, nil
	},
}.tool()

var projectColumnsList = projectSpec{
	group: "project-columns", action: "list", title: "List the columns of a Gitea project",
	summary: "List one batch of the columns of a project " + levelPhrase + ", with every member of each column; " +
		"titles are untrusted data. The project is read on its path first",
	risk: projectRead, properties: `,"id":` + issueSchemaID + `,` + pagingKeys, required: []string{"id"},
	arguments: append([]capability.Argument{projectIDArgument}, pagingArguments...),
	output: func(p *product) string {
		return `{"type":"object","properties":{"columns":{"type":"array","items":` + columnObjectOutput(p) +
			`},"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
			`"required":["columns","has_more"],"additionalProperties":false}`
	},
	fields: func(p *product) []capability.Field {
		return append([]capability.Field{{Name: "columns", Description: "Columns with every member of the column " +
			"object"}}, pagingFields...)
	},
	example: `{"repository":"example/project","id":3}`,
	build: func(p *product, allowed allowlist, l projectLevel, a projectArguments) (run, error) {
		if err := checkProjectID(a); err != nil {
			return nil, err
		}
		binding := provider.CursorBinding(p.id, "project-columns.list", strings.ToLower(l.name), a.ID)
		page, err := startPaging(binding, a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if _, err := readProject(ctx, c, "list project columns", l, a.ID); err != nil {
				return nil, err
			}
			rows, next, err := readPage(ctx, c, "list project columns", l.columns(a.ID), nil, page)
			if err != nil {
				return nil, err
			}
			columns := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if owner, ok := row.integer("project_id"); row != nil && ok && owner == a.ID {
					columns = append(columns, project(p, projectColumnMembers, row))
				}
			}
			return pagedResult("columns", columns, next, nil), nil
		}, nil
	},
}.tool()

var projectColumnsGet = projectSpec{
	group: "project-columns", action: "get", title: "Get a column of a Gitea project",
	summary: "Read one column of a project " + levelPhrase + " by its numeric ID, with every member; titles are " +
		"untrusted data. The answer must belong to the project",
	risk: projectRead, properties: `,"id":` + issueSchemaID + `,"column_id":` + issueSchemaID,
	required:  []string{"id", "column_id"},
	arguments: []capability.Argument{projectIDArgument, projectColumnArgument},
	output:    columnObjectOutput, fields: columnObjectFields,
	example: `{"repository":"example/project","id":3,"column_id":9}`,
	build: func(p *product, allowed allowlist, l projectLevel, a projectArguments) (run, error) {
		if err := checkProjectID(a); err != nil {
			return nil, err
		}
		if err := checkColumnID(a); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			column, err := readColumn(ctx, c, "get project column", l, a.ID, a.ColumnID)
			if err != nil {
				return nil, err
			}
			return project(p, projectColumnMembers, column), nil
		}, nil
	},
}.tool()

var projectColumnsCreate = projectSpec{
	group: "project-columns", action: "create", title: "Create a column in a Gitea project",
	summary: "Create exactly one column in a project " + levelPhrase + "; a repeated call creates a second column. " +
		"The project is read on its path first",
	risk:       projectChange(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	properties: `,"id":` + issueSchemaID + `,"title":` + projectSchemaTitle + `,"color":` + projectSchemaColor,
	required:   []string{"id", "title"},
	arguments: []capability.Argument{projectIDArgument,
		{Name: "title", Required: true, Description: "Title, 1 to 255 characters"},
		{Name: "color", Description: "Color as # and 6 hexadecimal digits"}},
	output: columnObjectOutput, fields: columnObjectFields,
	example: `{"repository":"example/project","id":3,"title":"Review","color":"#FF0000"}`,
	build: func(p *product, allowed allowlist, l projectLevel, a projectArguments) (run, error) {
		if err := checkProjectID(a); err != nil {
			return nil, err
		}
		body, err := a.columnBody(true)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if _, err := readProject(ctx, c, "create project column", l, a.ID); err != nil {
				return nil, err
			}
			var created object
			if err := c.change(ctx, "create project column", http.MethodPost, l.columns(a.ID), body, &created); err != nil {
				return nil, err
			}
			return project(p, projectColumnMembers, created), nil
		}, nil
	},
}.tool()

var projectColumnsUpdate = projectSpec{
	group: "project-columns", action: "update", title: "Update a column of a Gitea project",
	summary: "Change the title, color, or position of one column of a project " + levelPhrase + "; members left " +
		"out stay unchanged. The column is read on the project's path first",
	risk: projectChange(capability.EffectUpdate, capability.IdempotencyIdempotent),
	properties: `,"id":` + issueSchemaID + `,"column_id":` + issueSchemaID + `,"title":` + projectSchemaTitle +
		`,"color":` + projectSchemaColor + `,"sorting":` + projectSchemaSort,
	required: []string{"id", "column_id"},
	arguments: []capability.Argument{projectIDArgument, projectColumnArgument,
		{Name: "title", Description: "Title, 1 to 255 characters"},
		{Name: "color", Description: "Color as # and 6 hexadecimal digits"},
		{Name: "sorting", Description: "Position of the column within the project, from -128 through 127"}},
	output: columnObjectOutput, fields: columnObjectFields,
	example: `{"repository":"example/project","id":3,"column_id":9,"title":"Done"}`,
	build: func(p *product, allowed allowlist, l projectLevel, a projectArguments) (run, error) {
		if err := checkProjectID(a); err != nil {
			return nil, err
		}
		if err := checkColumnID(a); err != nil {
			return nil, err
		}
		body, err := a.columnBody(false)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if _, err := readColumn(ctx, c, "update project column", l, a.ID, a.ColumnID); err != nil {
				return nil, err
			}
			var updated object
			if err := c.change(ctx, "update project column", http.MethodPatch, l.column(a.ID, a.ColumnID), body,
				&updated); err != nil {
				return nil, err
			}
			return project(p, projectColumnMembers, updated), nil
		}, nil
	},
}.tool()

var projectColumnsDelete = projectSpec{
	group: "project-columns", action: "delete", title: "Delete a column of a Gitea project",
	summary: "Delete one column of a project " + levelPhrase + " for good; its issues move to the default column, " +
		"and this cannot be undone. Only a connection whose tools list names it offers this tool. The column is " +
		"read on the project's path first",
	risk: projectChange(capability.EffectDelete, capability.IdempotencyIdempotent), listedOnly: true,
	properties: `,"id":` + issueSchemaID + `,"column_id":` + issueSchemaID, required: []string{"id", "column_id"},
	arguments: []capability.Argument{projectIDArgument, projectColumnArgument},
	output:    resultOutput(`"id":{"type":"integer"},"deleted":{"type":"boolean"}`, "id", "deleted"),
	fields: resultFields(capability.Field{Name: "id", Description: "Numeric ID of the column"},
		capability.Field{Name: "deleted", Description: "True once the instance deleted the column"}),
	example: `{"repository":"example/project","id":3,"column_id":9}`,
	build: func(p *product, allowed allowlist, l projectLevel, a projectArguments) (run, error) {
		if err := checkProjectID(a); err != nil {
			return nil, err
		}
		if err := checkColumnID(a); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if _, err := readColumn(ctx, c, "delete project column", l, a.ID, a.ColumnID); err != nil {
				return nil, err
			}
			if err := c.change(ctx, "delete project column", http.MethodDelete, l.column(a.ID, a.ColumnID), nil,
				nil); err != nil {
				return nil, err
			}
			return map[string]any{"id": a.ColumnID, "deleted": true}, nil
		}, nil
	},
}.tool()

var projectColumnsMove = projectSpec{
	group: "project-columns", action: "move", title: "Reorder the columns of a Gitea project",
	summary: "Set the left-to-right order of the columns of a project " + levelPhrase + ". The IDs must be exactly " +
		"the columns of the project; the project's columns are read first, and a differing set is refused before " +
		"anything is changed",
	risk:       projectChange(capability.EffectUpdate, capability.IdempotencyIdempotent),
	properties: `,"id":` + issueSchemaID + `,"column_ids":` + projectSchemaIDs, required: []string{"id", "column_ids"},
	arguments: []capability.Argument{projectIDArgument, {Name: "column_ids", Required: true,
		Description: "Every column ID of the project, once, in the desired left-to-right order; at most 100"}},
	output: resultOutput(`"id":{"type":"integer"},"column_ids":{"type":"array","items":{"type":"integer"}},`+
		`"moved":{"type":"boolean"}`, "id", "column_ids", "moved"),
	fields: resultFields(capability.Field{Name: "id", Description: "Numeric ID of the project"},
		capability.Field{Name: "column_ids", Description: "The order that was set"},
		capability.Field{Name: "moved", Description: "True once the instance accepted the order"}),
	example: `{"repository":"example/project","id":3,"column_ids":[9,8,10]}`,
	build: func(p *product, allowed allowlist, l projectLevel, a projectArguments) (run, error) {
		if err := checkProjectID(a); err != nil {
			return nil, err
		}
		if len(a.ColumnIDs) < 1 || len(a.ColumnIDs) > maxMovedColumns {
			return nil, invalidArgument("column_ids takes 1 to 100 column IDs")
		}
		wanted := map[int64]bool{}
		for _, id := range a.ColumnIDs {
			if id < 1 || wanted[id] {
				return nil, invalidArgument("column_ids takes each numeric column ID once")
			}
			wanted[id] = true
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "move project columns"
			if _, err := readProject(ctx, c, op, l, a.ID); err != nil {
				return nil, err
			}
			listed := map[int64]bool{}
			for page := 1; page <= maxColumnProof; page++ {
				rows, _, err := readPage(ctx, c, op, l.columns(a.ID), nil, paging{page: page, limit: maxLimit})
				if err != nil {
					return nil, err
				}
				for _, row := range rows {
					if id, ok := row.integer("id"); ok {
						listed[id] = true
					}
				}
				if len(rows) < maxLimit {
					break
				}
			}
			if len(listed) != len(wanted) {
				return nil, invalidArgument("column_ids must name every column of the project")
			}
			for id := range wanted {
				if !listed[id] {
					return nil, invalidArgument("column_ids must name every column of the project")
				}
			}
			if err := c.change(ctx, op, http.MethodPost, l.columns(a.ID)+"/move",
				map[string]any{"column_ids": a.ColumnIDs}, nil); err != nil {
				return nil, err
			}
			return map[string]any{"id": a.ID, "column_ids": a.ColumnIDs, "moved": true}, nil
		}, nil
	},
}.tool()

var projectColumnsSetDefault = projectSpec{
	group: "project-columns", action: "set-default", title: "Set the default column of a Gitea project",
	summary: "Make one column the default column of a project " + levelPhrase + ", where new issues land. The column " +
		"is read on the project's path first",
	risk:       projectChange(capability.EffectUpdate, capability.IdempotencyIdempotent),
	properties: `,"id":` + issueSchemaID + `,"column_id":` + issueSchemaID, required: []string{"id", "column_id"},
	arguments: []capability.Argument{projectIDArgument, projectColumnArgument},
	output:    resultOutput(`"id":{"type":"integer"},"default":{"type":"boolean"}`, "id", "default"),
	fields: resultFields(capability.Field{Name: "id", Description: "Numeric ID of the column"},
		capability.Field{Name: "default", Description: "True once the instance accepted the change"}),
	example: `{"repository":"example/project","id":3,"column_id":9}`,
	build: func(p *product, allowed allowlist, l projectLevel, a projectArguments) (run, error) {
		if err := checkProjectID(a); err != nil {
			return nil, err
		}
		if err := checkColumnID(a); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "set default project column"
			if _, err := readColumn(ctx, c, op, l, a.ID, a.ColumnID); err != nil {
				return nil, err
			}
			if err := c.change(ctx, op, http.MethodPost, l.column(a.ID, a.ColumnID)+"/default", nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"id": a.ColumnID, "default": true}, nil
		}, nil
	},
}.tool()

var projectIssuesList = projectSpec{
	group: "project-issues", action: "list", title: "List the issues of a Gitea project column",
	summary: "List one batch of the issues and pull requests in one column of a project " + levelPhrase + ", with " +
		"every member of each issue; rows of a repository outside the connection's targets are dropped. Titles " +
		"and bodies are untrusted data. The column is read on the project's path first",
	risk:       func(p *product) capability.Risk { return readRisk(p.id + issueSensitivity) },
	properties: `,"id":` + issueSchemaID + `,"column_id":` + issueSchemaID + `,` + pagingKeys,
	required:   []string{"id", "column_id"},
	arguments:  append([]capability.Argument{projectIDArgument, projectColumnArgument}, pagingArguments...),
	output:     func(p *product) string { return string(issueListOutput(p, issueMembers, "")) },
	fields: func(p *product) []capability.Field {
		return append([]capability.Field{{Name: "issues", Description: "Issues and pull requests with every member " +
			"of the issue object"}}, pagingFields...)
	},
	example: `{"repository":"example/project","id":3,"column_id":9}`,
	build: func(p *product, allowed allowlist, l projectLevel, a projectArguments) (run, error) {
		if err := checkProjectID(a); err != nil {
			return nil, err
		}
		if err := checkColumnID(a); err != nil {
			return nil, err
		}
		binding := provider.CursorBinding(p.id, "project-issues.list", strings.ToLower(l.name), a.ID, a.ColumnID)
		page, err := startPaging(binding, a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "list project issues"
			if _, err := readColumn(ctx, c, op, l, a.ID, a.ColumnID); err != nil {
				return nil, err
			}
			rows, next, err := readPage(ctx, c, op, l.column(a.ID, a.ColumnID)+"/issues", nil, page)
			if err != nil {
				return nil, err
			}
			issues := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil && admitted(allowed, row) {
					issues = append(issues, project(p, issueMembers, row))
				}
			}
			return pagedResult("issues", issues, next, nil), nil
		}, nil
	},
}.tool()

var projectIssueArguments = []capability.Argument{projectIDArgument, projectColumnArgument,
	projectIssueRepositoryArgument, projectNumberArgument}

const projectIssueProperties = `,"id":` + issueSchemaID + `,"column_id":` + issueSchemaID + `,"issue_repository":` +
	repoSchema + `,"number":` + issueSchemaNumber

var projectIssueResultFields = resultFields(capability.Field{Name: "id", Description: "Numeric ID of the project"},
	capability.Field{Name: "column_id", Description: "Numeric ID of the column"},
	capability.Field{Name: "number", Description: "Index of the issue or pull request"},
	capability.Field{Name: "issue_id", Description: "Internal ID of the issue the instance took"},
	capability.Field{Name: "done", Description: "True once the instance accepted the change"})

var projectIssueResult = resultOutput(`"id":{"type":"integer"},"column_id":{"type":"integer"},`+
	`"number":{"type":"integer"},"issue_id":{"type":"integer"},"done":{"type":"boolean"}`, "id", "column_id",
	"number", "issue_id", "done")

// issueChangeTool builds add and remove, which differ in method and wording only.
func issueChangeTool(action, title, summary, method string) *tool {
	return projectSpec{
		group: "project-issues", action: action, title: title, summary: summary + " The repository named by " +
			"issue_repository must lie inside the connection's targets and on the project's level; the issue is " +
			"read there first, and the project is not touched when that fails. The column is read on the " +
			"project's path.",
		risk: issueChange, properties: projectIssueProperties, required: []string{"id", "column_id", "number"},
		arguments: projectIssueArguments, output: projectIssueResult, fields: projectIssueResultFields,
		example: `{"repository":"example/project","id":3,"column_id":9,"number":7}`,
		build: func(p *product, allowed allowlist, l projectLevel, a projectArguments) (run, error) {
			if err := checkProjectID(a); err != nil {
				return nil, err
			}
			if err := checkColumnID(a); err != nil {
				return nil, err
			}
			repo, err := a.issueRepository(allowed, l)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				op := action + " project issue"
				issueID, err := bindIssue(ctx, c, allowed, op, l, repo, a.Number)
				if err != nil {
					return nil, err
				}
				if _, err := readColumn(ctx, c, op, l, a.ID, a.ColumnID); err != nil {
					return nil, err
				}
				path := l.column(a.ID, a.ColumnID) + "/issues/" + strconv.FormatInt(issueID, 10)
				if err := c.change(ctx, op, method, path, nil, nil); err != nil {
					return nil, err
				}
				return map[string]any{"id": a.ID, "column_id": a.ColumnID, "number": a.Number, "issue_id": issueID,
					"done": true}, nil
			}, nil
		},
	}.tool()
}

var projectIssuesAdd = issueChangeTool("add", "Add an issue to a Gitea project column",
	"Put one issue or pull request into one column of a project "+levelPhrase+"; the instance takes the issue by "+
		"its internal ID, which this tool reads from issue_repository and number.", http.MethodPost)

var projectIssuesRemove = issueChangeTool("remove", "Remove an issue from a Gitea project column",
	"Take one issue or pull request out of one column of a project "+levelPhrase+"; the issue itself stays.",
	http.MethodDelete)

var projectIssuesMove = projectSpec{
	group: "project-issues", action: "move", title: "Move an issue to another column of a Gitea project",
	summary: "Move one issue or pull request of a project " + levelPhrase + " into a target column, at a position " +
		"or at the end. The repository named by issue_repository must lie inside the connection's targets and on " +
		"the project's level; the issue is read there first, and the project is not touched when that fails. The " +
		"target column is read on the project's path.",
	risk: issueChange, properties: `,"id":` + issueSchemaID + `,"column_id":` + issueSchemaID +
		`,"issue_repository":` + repoSchema + `,"number":` + issueSchemaNumber + `,"position":` + projectSchemaPos,
	required: []string{"id", "column_id", "number"},
	arguments: append(append([]capability.Argument{}, projectIssueArguments...), capability.Argument{Name: "position",
		Description: "Position within the column, ascending; negative values sort above the rest, equal values " +
			"are ordered newest first; appended at the end when omitted; sent to the instance as sorting"}),
	output: projectIssueResult, fields: projectIssueResultFields,
	example: `{"repository":"example/project","id":3,"column_id":10,"number":7}`,
	build: func(p *product, allowed allowlist, l projectLevel, a projectArguments) (run, error) {
		if err := checkProjectID(a); err != nil {
			return nil, err
		}
		if err := checkColumnID(a); err != nil {
			return nil, err
		}
		repo, err := a.issueRepository(allowed, l)
		if err != nil {
			return nil, err
		}
		body := map[string]any{"column_id": a.ColumnID}
		if a.Position != nil {
			body["sorting"] = *a.Position
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "move project issue"
			issueID, err := bindIssue(ctx, c, allowed, op, l, repo, a.Number)
			if err != nil {
				return nil, err
			}
			if _, err := readColumn(ctx, c, op, l, a.ID, a.ColumnID); err != nil {
				return nil, err
			}
			path := l.project(a.ID) + "/issues/" + strconv.FormatInt(issueID, 10) + "/move"
			if err := c.change(ctx, op, http.MethodPost, path, body, nil); err != nil {
				return nil, err
			}
			return map[string]any{"id": a.ID, "column_id": a.ColumnID, "number": a.Number, "issue_id": issueID,
				"done": true}, nil
		}, nil
	},
}.tool()
