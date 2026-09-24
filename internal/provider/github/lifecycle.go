package github

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The project lifecycle tools create, change, close, reopen, copy, and delete a project, link it to a
// repository or unlink it, and mark it as a template or unmark it. A new project has no number yet, so
// creating one, and the destination of a copy, need the owner as an owner target, or a connection without
// targets; a project pattern of the owner is not enough. Every other lifecycle tool checks the project like
// the other project tools, and a link or an unlink checks the repository as well. GitHub marks only projects
// of an organization as templates.

// The state of a project after a change. The update names its project through its target field; a create
// and a copy name the new project inside created.
const (
	projectStateProperties = `"number":{"type":"integer"},"title":{"type":"string"},"url":{"type":"string"},` +
		`"closed":{"type":"boolean"},"public":{"type":"boolean"}`
	projectStateRequired = `"number","title","closed","public"`
	updatedOutput        = `{"type":"object","properties":{` + projectStateProperties + `},` +
		`"required":[` + projectStateRequired + `],"additionalProperties":false}`
	createdOutput = `{"type":"object","properties":{"created":{"type":"object","properties":{` +
		`"project":{"type":"string"},` + projectStateProperties + `},"required":["project",` + projectStateRequired +
		`],"additionalProperties":false}},"required":["created"],"additionalProperties":false}`
)

var createdField = capability.Field{Name: "created", Description: "The new project: project as " +
	"users/LOGIN/projects/NUMBER or orgs/LOGIN/projects/NUMBER, number, title (untrusted data), url, closed, " +
	"public"}

var newOwnerArgument = capability.Argument{Name: "owner", Description: "Owner of the new project as " +
	"users/LOGIN for a user or orgs/LOGIN for an organization; optional when the connection's targets name " +
	"exactly one owner; must lie inside the targets when the connection lists any, as an owner target itself, " +
	"since a project pattern of the owner is not enough"}

var newOwnerField = capability.Field{Name: "owner", Description: "Owner of the new project, as users/LOGIN " +
	"or orgs/LOGIN; the one a default chose when the argument was left out"}

var projectsCreate = capability.Descriptor{
	ID:      Provider + ".projects.create",
	Version: 1,
	Title:   "Create a GitHub project",
	Description: "Create one empty project of a user or an organization an explicit connection names as an " +
		"owner target; a repeated call creates a second project",
	Tags:                       []string{"github", "projects", "create", "planning"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"title":` + titleSchema + `},` +
		`"required":["title"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(createdOutput),
	Arguments: []capability.Argument{
		{Name: "title", Description: "Project title, 1 to 256 characters", Required: true},
	},
	Fields: []capability.Field{createdField},
	Examples: []capability.Example{{
		Description: "Create a project of an organization",
		Arguments:   json.RawMessage(`{"owner":"orgs/octo-org","title":"Roadmap 2027"}`),
	}},
}

var projectsUpdate = capability.Descriptor{
	ID:      Provider + ".projects.update",
	Version: 1,
	Title:   "Update a GitHub project",
	Description: "Change the title, short description, readme, or visibility of one GitHub project an " +
		"explicit connection allows, or close or reopen it; settings left out stay unchanged",
	Tags:                       []string{"github", "projects", "update", "close", "reopen", "planning"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"title":` + titleSchema + `,` +
		`"short_description":{"type":"string","maxLength":1024},"readme":` + bodySchema + `,` +
		`"public":{"type":"boolean"},"closed":{"type":"boolean"}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(updatedOutput),
	Arguments: []capability.Argument{
		{Name: "title", Description: "New project title, 1 to 256 characters"},
		{Name: "short_description", Description: "New short description, 1 to 1024 characters; GitHub cannot " +
			"clear it through its API"},
		{Name: "readme", Description: "New readme in Markdown, 1 to 65536 characters; GitHub cannot clear it " +
			"through its API"},
		{Name: "public", Description: "true makes the project public, false private"},
		{Name: "closed", Description: "true closes the project, false reopens it"},
	},
	Fields: []capability.Field{
		{Name: "title", Description: "Project title after the change, untrusted data"},
		{Name: "url", Description: "Web address of the project"},
		{Name: "closed", Description: "True while the project is closed"},
		{Name: "public", Description: "True while the project is public"},
	},
	Examples: []capability.Example{{
		Description: "Close a finished project",
		Arguments:   json.RawMessage(`{"project":"orgs/octo-org/projects/7","closed":true}`),
	}},
}

var projectsDelete = capability.Descriptor{
	ID:      Provider + ".projects.delete",
	Version: 1,
	Title:   "Delete a GitHub project",
	Description: "Delete one GitHub project an explicit connection allows permanently, with its items, " +
		"fields, and views; the issues and pull requests of its items stay. Offered only by a connection whose " +
		"tools list names it",
	Tags:                       []string{"github", "projects", "delete"},
	Risk:                       changeRisk(capability.EffectDelete, capability.IdempotencyUnknown),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema:                json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
		`"required":["deleted"],"additionalProperties":false}`),
	Fields: []capability.Field{{Name: "deleted", Description: "True once GitHub deleted the project"}},
	Examples: []capability.Example{{
		Description: "Delete a project",
		Arguments:   json.RawMessage(`{"project":"orgs/octo-org/projects/7"}`),
	}},
}

var projectsCopy = capability.Descriptor{
	ID:      Provider + ".projects.copy",
	Version: 1,
	Title:   "Copy a GitHub project",
	Description: "Copy one GitHub project an explicit connection allows, with its fields and views and, on " +
		"request, its draft issues, into a new project of an owner the connection names as an owner target; a " +
		"repeated call creates a second copy",
	Tags:                       []string{"github", "projects", "copy", "create", "planning"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"owner":` + ownerSchema + `,` +
		`"title":` + titleSchema + `,"include_drafts":{"type":"boolean"}},"required":["title"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(createdOutput),
	Arguments: []capability.Argument{
		{Name: "title", Description: "Title of the copy, 1 to 256 characters", Required: true},
		newOwnerArgument,
		{Name: "include_drafts", Description: "true copies the draft issues as well; false when omitted"},
	},
	Fields: []capability.Field{createdField},
	Examples: []capability.Example{{
		Description: "Copy a project template into an organization",
		Arguments: json.RawMessage(`{"project":"orgs/octo-org/projects/7","owner":"orgs/octo-org",` +
			`"title":"Roadmap 2028"}`),
	}},
}

const linkInput = `{"type":"object","properties":{"repository":` + repoSchema + `},` +
	`"additionalProperties":false}`

var projectsLink = capability.Descriptor{
	ID:      Provider + ".projects.link",
	Version: 1,
	Title:   "Link a GitHub project to a repository",
	Description: "Link one GitHub project an explicit connection allows to one repository it allows, so the " +
		"project appears in the repository; a linked project stays linked once",
	Tags:                       []string{"github", "projects", "repositories", "link", "planning"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                json.RawMessage(linkInput),
	OutputSchema:               json.RawMessage(linkedOutput),
	Arguments:                  []capability.Argument{repositoryArgument},
	Fields:                     []capability.Field{linkedField},
	Examples: []capability.Example{{
		Description: "Link a project to a repository",
		Arguments:   json.RawMessage(`{"project":"orgs/octo-org/projects/7","repository":"octo-org/example"}`),
	}},
}

var projectsUnlink = capability.Descriptor{
	ID:      Provider + ".projects.unlink",
	Version: 1,
	Title:   "Unlink a GitHub project from a repository",
	Description: "Remove the link between one GitHub project an explicit connection allows and one repository " +
		"it allows; the project and its items stay",
	Tags:                       []string{"github", "projects", "repositories", "unlink", "planning"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                json.RawMessage(linkInput),
	OutputSchema:               json.RawMessage(linkedOutput),
	Arguments:                  []capability.Argument{repositoryArgument},
	Fields:                     []capability.Field{linkedField},
	Examples: []capability.Example{{
		Description: "Unlink a project from a repository",
		Arguments:   json.RawMessage(`{"project":"orgs/octo-org/projects/7","repository":"octo-org/example"}`),
	}},
}

const linkedOutput = `{"type":"object","properties":{"linked":{"type":"boolean"}},"required":["linked"],` +
	`"additionalProperties":false}`

const templateOutput = `{"type":"object","properties":{` + projectStateProperties + `,"template":{"type":"boolean"}},` +
	`"required":[` + projectStateRequired + `,"template"],"additionalProperties":false}`

var templateFields = []capability.Field{
	{Name: "title", Description: "Project title, untrusted data"},
	{Name: "url", Description: "Web address of the project"},
	{Name: "template", Description: "True while the project is a template of its organization"},
}

var templatesMark = capability.Descriptor{
	ID:      Provider + ".projecttemplates.mark",
	Version: 1,
	Title:   "Mark a GitHub project as a template",
	Description: "Mark one project of an organization an explicit connection allows as a template, which the " +
		"organization offers when a project is created; GitHub marks no project of a user",
	Tags:                       []string{"github", "projects", "templates", "update", "planning"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema:               json.RawMessage(templateOutput),
	Fields:                     templateFields,
	Examples: []capability.Example{{
		Description: "Offer a project as a template",
		Arguments:   json.RawMessage(`{"project":"orgs/octo-org/projects/7"}`),
	}},
}

var templatesUnmark = capability.Descriptor{
	ID:      Provider + ".projecttemplates.unmark",
	Version: 1,
	Title:   "Unmark a GitHub project as a template",
	Description: "Stop offering one project an explicit connection allows as a template; the project itself " +
		"stays unchanged",
	Tags:                       []string{"github", "projects", "templates", "update", "planning"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema:               json.RawMessage(templateOutput),
	Fields:                     templateFields,
	Examples: []capability.Example{{
		Description: "Withdraw a template",
		Arguments:   json.RawMessage(`{"project":"orgs/octo-org/projects/7"}`),
	}},
}

var linkedField = capability.Field{Name: "linked", Description: "True after a link, false after an unlink"}

// lifecycleOperations are the project lifecycle tools.
func lifecycleOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: projectsCreate, Handler: capability.Handler(invokeProjectsCreate)},
		{Descriptor: projectsUpdate, Handler: capability.Handler(invokeProjectsUpdate)},
		{Descriptor: projectsDelete, Handler: capability.Handler(invokeProjectsDelete)},
		{Descriptor: projectsCopy, Handler: capability.Handler(invokeProjectsCopy)},
		{Descriptor: projectsLink, Handler: capability.Handler(invokeProjectsLink(true))},
		{Descriptor: projectsUnlink, Handler: capability.Handler(invokeProjectsLink(false))},
		{Descriptor: templatesMark, Handler: capability.Handler(invokeProjectsTemplate(true))},
		{Descriptor: templatesUnmark, Handler: capability.Handler(invokeProjectsTemplate(false))},
	}
}

// ProjectState is a project after a lifecycle change: Project is the value a project argument takes.
type ProjectState struct {
	Project string `json:"project"`
	Number  int    `json:"number"`
	Title   string `json:"title"`
	URL     string `json:"url,omitempty"`
	Closed  bool   `json:"closed"`
	Public  bool   `json:"public"`
}

// Created names the project a create or a copy made.
type Created struct {
	Created ProjectState `json:"created"`
}

// Deleted is the answer of a deleted project.
type Deleted struct {
	Deleted bool `json:"deleted"`
}

// Templated is a project after it was marked or unmarked as a template.
type Templated struct {
	ProjectState
	Template bool `json:"template"`
}

// Linked is the answer of a link or an unlink.
type Linked struct {
	Linked bool `json:"linked"`
}

// ProjectChanges are the settings a project update writes; a nil setting stays unchanged.
type ProjectChanges struct {
	Title            *string `json:"title"`
	ShortDescription *string `json:"short_description"`
	Readme           *string `json:"readme"`
	Public           *bool   `json:"public"`
	Closed           *bool   `json:"closed"`
}

func (p ProjectChanges) check() error {
	if p.Title == nil && p.ShortDescription == nil && p.Readme == nil && p.Public == nil && p.Closed == nil {
		return invalidRequest("name at least one of title, short_description, readme, public, or closed to change")
	}
	// GitHub ignores an empty short description or readme and answers with success, so an empty value would
	// report a change that never happened.
	if (p.ShortDescription != nil && *p.ShortDescription == "") || (p.Readme != nil && *p.Readme == "") {
		return invalidRequest("short_description and readme need at least 1 character; GitHub ignores an empty " +
			"value and cannot clear either through its API")
	}
	if p.Title != nil {
		return checkProjectTitle(*p.Title)
	}
	return nil
}

func checkProjectTitle(title string) error {
	return (IssueContent{Title: &title}).check(true)
}

// The mutations of the lifecycle. Every identifier and value travels as a variable; an update names only the
// settings it changes.
const (
	projectStateSelection = `projectV2{number title url closed public}`
	createProjectMutation = `mutation($owner:ID!,$title:String!){create:createProjectV2(` +
		`input:{ownerId:$owner,title:$title}){` + projectStateSelection + `}}`
	copyProjectMutation = `mutation($project:ID!,$owner:ID!,$title:String!,$drafts:Boolean!){` +
		`create:copyProjectV2(input:{projectId:$project,ownerId:$owner,title:$title,includeDraftIssues:$drafts}){` +
		projectStateSelection + `}}`
	deleteProjectMutation = `mutation($project:ID!){delete:deleteProjectV2(input:{projectId:$project}){` +
		`clientMutationId}}`
	linkProjectMutation = `mutation($project:ID!,$repository:ID!){link:linkProjectV2ToRepository(` +
		`input:{projectId:$project,repositoryId:$repository}){clientMutationId}}`
	unlinkProjectMutation = `mutation($project:ID!,$repository:ID!){link:unlinkProjectV2FromRepository(` +
		`input:{projectId:$project,repositoryId:$repository}){clientMutationId}}`
	templateSelection    = `projectV2{number title url closed public template}`
	markTemplateMutation = `mutation($project:ID!){template:markProjectV2AsTemplate(input:{projectId:$project}){` +
		templateSelection + `}}`
	unmarkTemplateMutation = `mutation($project:ID!){template:unmarkProjectV2AsTemplate(input:{projectId:$project}){` +
		templateSelection + `}}`
)

type projectStateJSON struct {
	Number   int    `json:"number"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Closed   bool   `json:"closed"`
	Public   bool   `json:"public"`
	Template bool   `json:"template"`
}

type projectAnswerJSON struct {
	Project *projectStateJSON `json:"projectV2"`
}

// stateOf reads the project a change answered with. The change already happened, so an answer without it
// leaves the outcome open.
func stateOf(op string, owner target, answer *projectAnswerJSON) (*ProjectState, error) {
	if answer == nil || answer.Project == nil || answer.Project.Number < 1 {
		return nil, invalidResponse(op, true)
	}
	node := answer.Project
	project := target{kind: kindProject, scope: owner.scope, owner: owner.owner, number: node.Number}
	return &ProjectState{Project: project.String(), Number: node.Number, Title: node.Title, URL: node.URL,
		Closed: node.Closed, Public: node.Public}, nil
}

// ownerID resolves the node of an owner a project is created in. A failure names that owner.
func (c *Client) ownerID(ctx context.Context, op string, owner target) (string, error) {
	at := *c
	at.target = owner
	var answer struct {
		Owner *struct {
			ID string `json:"id"`
		} `json:"owner"`
	}
	if err := at.graphql(ctx, op, `query($owner:String!){owner:`+owner.ownerField()+`(login:$owner){id}}`,
		map[string]any{"owner": owner.owner}, &answer); err != nil {
		return "", err
	}
	if answer.Owner == nil || answer.Owner.ID == "" {
		return "", notFound(op, subject{in: owner})
	}
	return answer.Owner.ID, nil
}

// createProject creates one empty project of the bound owner.
func (c *Client) createProject(ctx context.Context, title string) (*Created, error) {
	const op = "create project"
	if c.target.kind != kindOwner {
		return nil, providerError(op, "this client is not bound to an owner")
	}
	ownerID, err := c.ownerID(ctx, op, c.target)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Create *projectAnswerJSON `json:"create"`
	}
	if err := c.mutate(ctx, op, createProjectMutation, map[string]any{"owner": ownerID, "title": title},
		&answer); err != nil {
		return nil, err
	}
	state, err := stateOf(op, c.target, answer.Create)
	if err != nil {
		return nil, err
	}
	return &Created{Created: *state}, nil
}

// copyProject copies the bound project into a new project of owner.
func (c *Client) copyProject(ctx context.Context, owner target, title string, drafts bool) (*Created, error) {
	const op = "copy project"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	info, _, err := c.resolve(ctx, op, planningRequest{})
	if err != nil {
		return nil, err
	}
	ownerID, err := c.ownerID(ctx, op, owner)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Create *projectAnswerJSON `json:"create"`
	}
	if err := c.mutate(ctx, op, copyProjectMutation, map[string]any{"project": info.id, "owner": ownerID,
		"title": title, "drafts": drafts}, &answer); err != nil {
		return nil, err
	}
	state, err := stateOf(op, owner, answer.Create)
	if err != nil {
		return nil, err
	}
	return &Created{Created: *state}, nil
}

// updateProject writes the named settings of the bound project in one mutation.
func (c *Client) updateProject(ctx context.Context, changes ProjectChanges) (*ProjectState, error) {
	const op = "update project"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	if err := changes.check(); err != nil {
		return nil, err
	}
	info, _, err := c.resolve(ctx, op, planningRequest{})
	if err != nil {
		return nil, err
	}
	declarations, inputs := []string{"$project:ID!"}, []string{"projectId:$project"}
	variables := map[string]any{"project": info.id}
	set := func(name, kind string, value any) {
		declarations = append(declarations, "$"+name+":"+kind)
		inputs = append(inputs, name+":$"+name)
		variables[name] = value
	}
	if changes.Title != nil {
		set("title", "String!", *changes.Title)
	}
	if changes.ShortDescription != nil {
		set("shortDescription", "String!", *changes.ShortDescription)
	}
	if changes.Readme != nil {
		set("readme", "String!", *changes.Readme)
	}
	if changes.Public != nil {
		set("public", "Boolean!", *changes.Public)
	}
	if changes.Closed != nil {
		set("closed", "Boolean!", *changes.Closed)
	}
	document := "mutation(" + strings.Join(declarations, ",") + "){update:updateProjectV2(input:{" +
		strings.Join(inputs, ",") + "}){" + projectStateSelection + "}}"
	var answer struct {
		Update *projectAnswerJSON `json:"update"`
	}
	if err := c.mutate(ctx, op, document, variables, &answer); err != nil {
		return nil, err
	}
	state, err := stateOf(op, c.target, answer.Update)
	if err != nil {
		return nil, err
	}
	if state.Number != c.target.number {
		return nil, invalidResponse(op, true)
	}
	return state, nil
}

// deleteProject deletes the bound project.
func (c *Client) deleteProject(ctx context.Context) (*Deleted, error) {
	const op = "delete project"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	info, _, err := c.resolve(ctx, op, planningRequest{})
	if err != nil {
		return nil, err
	}
	var answer struct {
		Delete *json.RawMessage `json:"delete"`
	}
	if err := c.mutate(ctx, op, deleteProjectMutation, map[string]any{"project": info.id}, &answer); err != nil {
		return nil, err
	}
	if answer.Delete == nil {
		return nil, invalidResponse(op, true)
	}
	return &Deleted{Deleted: true}, nil
}

// linkProject links the bound project to a repository, or unlinks it. The project and the repository are
// resolved in one query.
func (c *Client) linkProject(ctx context.Context, repo target, link bool) (*Linked, error) {
	op, mutation := "link project", linkProjectMutation
	if !link {
		op, mutation = "unlink project", unlinkProjectMutation
	}
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	info, nodes, err := c.resolve(ctx, op, planningRequest{repository: repo})
	if err != nil {
		return nil, err
	}
	var answer struct {
		Link *json.RawMessage `json:"link"`
	}
	if err := c.mutate(ctx, op, mutation, map[string]any{"project": info.id, "repository": nodes.repository},
		&answer); err != nil {
		return nil, err
	}
	if answer.Link == nil {
		return nil, invalidResponse(op, true)
	}
	return &Linked{Linked: link}, nil
}

// checkTemplateOwner refuses to mark a project of a user as a template: GitHub marks only projects of an
// organization and answers any other with a bare refusal.
func checkTemplateOwner(project target) error {
	if project.scope != "orgs" {
		return invalidRequest("GitHub marks only a project of an organization as a template, and " +
			project.argument() + " belongs to a user")
	}
	return nil
}

// markTemplate marks the bound project as a template, or unmarks it. Unmarking a project that is no template
// succeeds and changes nothing.
func (c *Client) markTemplate(ctx context.Context, template bool) (*Templated, error) {
	op, mutation := "mark project as template", markTemplateMutation
	if !template {
		op, mutation = "unmark project as template", unmarkTemplateMutation
	}
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	if template {
		if err := checkTemplateOwner(c.target); err != nil {
			return nil, err
		}
	}
	info, _, err := c.resolve(ctx, op, planningRequest{})
	if err != nil {
		return nil, err
	}
	var answer struct {
		Template *projectAnswerJSON `json:"template"`
	}
	if err := c.mutate(ctx, op, mutation, map[string]any{"project": info.id}, &answer); err != nil {
		return nil, err
	}
	state, err := stateOf(op, c.target, answer.Template)
	if err != nil {
		return nil, err
	}
	if state.Number != c.target.number {
		return nil, invalidResponse(op, true)
	}
	return &Templated{ProjectState: *state, Template: answer.Template.Project.Template}, nil
}

// The handlers check every target and argument before a credential is resolved, so a refused request never
// becomes a secret read or a provider call.

func invokeProjectsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable("create project")
	}
	owner, err := selectOwner(resolved, kindOwner, raw)
	if err != nil {
		return nil, err
	}
	if err := checkProjectTitle(arguments.Title); err != nil {
		return nil, err
	}
	client, err := openAt(resolved, secrets, red, owner)
	if err != nil {
		return nil, err
	}
	return owner.locate(client.createProject(ctx, arguments.Title))
}

func invokeProjectsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var changes ProjectChanges
	if err := json.Unmarshal(raw, &changes); err != nil {
		return nil, unreadable("update project")
	}
	bound, err := selectTarget(resolved, kindProject, raw)
	if err != nil {
		return nil, err
	}
	if err := changes.check(); err != nil {
		return nil, err
	}
	client, err := openAt(resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.updateProject(ctx, changes))
}

func invokeProjectsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	bound, err := selectTarget(resolved, kindProject, raw)
	if err != nil {
		return nil, err
	}
	client, err := openAt(resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.deleteProject(ctx))
}

func invokeProjectsCopy(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Title         string `json:"title"`
		IncludeDrafts bool   `json:"include_drafts"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable("copy project")
	}
	// Both targets are checked: the project that is copied and the owner that receives the copy.
	bound, err := selectTarget(resolved, kindProject, raw)
	if err != nil {
		return nil, err
	}
	owner, err := selectOwner(resolved, kindOwner, raw)
	if err != nil {
		return nil, err
	}
	if err := checkProjectTitle(arguments.Title); err != nil {
		return nil, err
	}
	client, err := openAt(resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(owner.locate(client.copyProject(ctx, owner, arguments.Title, arguments.IncludeDrafts)))
}

func invokeProjectsLink(link bool) func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
	json.RawMessage) (any, error) {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		// Both targets are checked: the project and the repository it is linked to or unlinked from.
		bound, err := selectTarget(resolved, kindProject, raw)
		if err != nil {
			return nil, err
		}
		repo, err := selectTarget(resolved, kindRepository, raw)
		if err != nil {
			return nil, err
		}
		client, err := openAt(resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(repo.locate(client.linkProject(ctx, repo, link)))
	}
}

func invokeProjectsTemplate(template bool) func(context.Context, *config.Resolved, *secret.Resolver,
	*redact.Redactor, json.RawMessage) (any, error) {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		bound, err := selectTarget(resolved, kindProject, raw)
		if err != nil {
			return nil, err
		}
		if template {
			if err := checkTemplateOwner(bound); err != nil {
				return nil, err
			}
		}
		client, err := openAt(resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(client.markTemplate(ctx, template))
	}
}
