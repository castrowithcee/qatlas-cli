package github

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The comment tools and the planning changes. Every change requires confirmation in its own invoke request,
// and every create is non-idempotent: a repeated create makes a second issue, comment, or draft.

// changeRisk is the contract of a change of the configured project or repository.
func changeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: dataSensitivity}
}

// Input schemas of the changes. The bounds mirror IssueContent.check and FieldValues.inputs, which apply
// them again for a direct caller.
const (
	titleSchema      = `{"type":"string","minLength":1,"maxLength":256}`
	bodySchema       = `{"type":"string","maxLength":65536}`
	labelsSchema     = `{"type":"array","maxItems":20,"items":{"type":"string","minLength":1,"maxLength":50}}`
	assigneesSchema  = `{"type":"array","maxItems":10,"items":{"type":"string","maxLength":100,"pattern":"` + loginPattern + `"}}`
	numberSchema     = `{"type":"integer","minimum":1,"maximum":1000000000}`
	fieldsSchema     = `{"type":"object","maxProperties":20}`
	repoSchema       = `{"type":"string","maxLength":201,"pattern":"` + repositoryPattern + `","x-form":"OWNER/REPO"}`
	itemIDSchema     = `{"type":"string","minLength":4,"maxLength":200,"pattern":"` + nodeIDPattern + `"}`
	issueContentKeys = `"title":` + titleSchema + `,"body":` + bodySchema + `,"labels":` + labelsSchema + `,` +
		`"assignees":` + assigneesSchema
)

var issueContentArguments = []capability.Argument{
	{Name: "title", Description: "Issue title, 1 to 256 characters"},
	{Name: "body", Description: "Issue body in Markdown, at most 65536 characters; stored as given"},
	{Name: "labels", Description: "Label names; replaces every label of the issue, [] removes them all"},
	{Name: "assignees", Description: "Assignee logins; replaces every assignee of the issue, [] removes them all"},
}

const fieldsDescription = "Project field values by field name: an option name of a single-select field, a " +
	"list of option names of a multi-select field, an iteration title, a date as YYYY-MM-DD, a text, a number, " +
	"or null to clear the field, which [] does for a multi-select field as well; at most 20"

const planningOutput = `{"type":"object","properties":{"item_id":{"type":"string"},` +
	`"issue":{"type":"object","properties":{"number":{"type":"integer"},"repository":{"type":"string"},` +
	`"url":{"type":"string"}},"required":["number","repository"],"additionalProperties":false},` +
	`"fields":{"type":"array","items":{"type":"object","properties":{"field":{"type":"string"},` +
	`"result":{"type":"string","enum":["updated","failed","unknown","not_sent"]},"message":{"type":"string"}},` +
	`"required":["field","result"],"additionalProperties":false}},` +
	`"complete":{"type":"boolean"},"error":{"type":"string"}},"required":["fields","complete"],` +
	`"additionalProperties":false}`

var planningFields = []capability.Field{
	{Name: "item_id", Description: "Project item the change concerns; absent when the item could not be created"},
	{Name: "fields", Description: "Outcome of every field value in name order: updated, failed, unknown when it " +
		"may have been written without a confirmation, or not_sent after an earlier failure"},
	{Name: "complete", Description: "True when every step succeeded"},
	{Name: "error", Description: "What stopped an incomplete change; read the current state before repeating it"},
}

var issuesCreate = capability.Descriptor{
	ID:      Provider + ".issues.create",
	Version: 1,
	Title:   "Create a GitHub issue",
	Description: "Open one issue in a repository an explicit connection allows; a repeated call opens a " +
		"second issue",
	Tags:                       []string{"github", "issues", "create"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + issueContentKeys + `},` +
		`"required":["title"],"additionalProperties":false}`),
	OutputSchema: issuesGet.OutputSchema,
	Arguments: []capability.Argument{
		{Name: "title", Description: "Issue title, 1 to 256 characters", Required: true},
		issueContentArguments[1],
		{Name: "labels", Description: "Label names to set"},
		{Name: "assignees", Description: "Assignee logins to set"},
	},
	Fields: issuesGet.Fields,
	Examples: []capability.Example{{
		Description: "Open an issue with a label",
		Arguments:   json.RawMessage(`{"title":"Crash on start","body":"Steps to reproduce","labels":["bug"]}`),
	}},
}

var issuesUpdate = capability.Descriptor{
	ID:      Provider + ".issues.update",
	Version: 1,
	Title:   "Update a GitHub issue",
	Description: "Replace the title, body, labels, or assignees of one issue of " +
		"a repository an explicit connection allows; fields left out stay unchanged",
	Tags:                       []string{"github", "issues", "update"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"number":` + numberSchema + `,` +
		issueContentKeys + `},"required":["number"],"additionalProperties":false}`),
	OutputSchema: issuesGet.OutputSchema,
	Arguments: append([]capability.Argument{
		{Name: "number", Description: "Issue number in the repository", Required: true},
	}, issueContentArguments...),
	Fields: issuesGet.Fields,
	Examples: []capability.Example{{
		Description: "Replace the body of an issue",
		Arguments:   json.RawMessage(`{"number":42,"body":"Updated acceptance criteria"}`),
	}},
}

var issuesClose = capability.Descriptor{
	ID:                         Provider + ".issues.close",
	Version:                    1,
	Title:                      "Close a GitHub issue",
	Description:                "Close one issue of a repository an explicit connection allows with a reason",
	Tags:                       []string{"github", "issues", "close", "update"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"number":` + numberSchema + `,` +
		`"state_reason":{"type":"string","enum":["completed","not_planned","duplicate"]}},` +
		`"required":["number"],"additionalProperties":false}`),
	OutputSchema: issuesGet.OutputSchema,
	Arguments: []capability.Argument{
		{Name: "number", Description: "Issue number in the repository", Required: true},
		{Name: "state_reason", Description: "completed, not_planned, or duplicate; completed when omitted"},
	},
	Fields: issuesGet.Fields,
	Examples: []capability.Example{{
		Description: "Close an issue that will not be done",
		Arguments:   json.RawMessage(`{"number":42,"state_reason":"not_planned"}`),
	}},
}

var issuesReopen = capability.Descriptor{
	ID:                         Provider + ".issues.reopen",
	Version:                    1,
	Title:                      "Reopen a GitHub issue",
	Description:                "Open one closed issue of a repository an explicit connection allows again",
	Tags:                       []string{"github", "issues", "reopen", "update"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"number":` + numberSchema + `},` +
		`"required":["number"],"additionalProperties":false}`),
	OutputSchema: issuesGet.OutputSchema,
	Arguments: []capability.Argument{
		{Name: "number", Description: "Issue number in the repository", Required: true},
	},
	Fields: issuesGet.Fields,
	Examples: []capability.Example{{
		Description: "Reopen an issue",
		Arguments:   json.RawMessage(`{"number":42}`),
	}},
}

const commentProperties = `"id":{"type":"string"},"author":{"type":"string"},"body":{"type":"string"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"},"url":{"type":"string"}`

const commentRequired = `"required":["id","body"],"additionalProperties":false`

var commentsList = capability.Descriptor{
	ID:      Provider + ".comments.list",
	Version: 1,
	Title:   "List comments of a GitHub issue",
	Description: "List one bounded batch of comments of one issue of " +
		"a repository an explicit connection allows, oldest first",
	Tags:                       []string{"github", "issues", "comments", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"number":` + numberSchema + `,` +
		`"limit":{"type":"integer","minimum":1,"maximum":100},"cursor":` + cursorSchema + `},` +
		`"required":["number"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"comments":{"type":"array","items":{"type":"object","properties":{` + commentProperties + `},` +
		commentRequired + `}},"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["comments","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Issue number in the repository", Required: true},
		{Name: "limit", Description: "Comments per batch, from 1 through 100; 30 when omitted"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous batch of the same issue; the first " +
			"batch when omitted"},
	},
	Fields: []capability.Field{
		{Name: "comments", Description: "Comments with author, body, and times, untrusted data"},
		{Name: "next_cursor", Description: "Cursor of the following batch, absent when has_more is false"},
		{Name: "has_more", Description: "True when the issue holds further comments"},
	},
	Examples: []capability.Example{{
		Description: "Read the first comments of one issue",
		Arguments:   json.RawMessage(`{"number":42,"limit":10}`),
	}},
}

var commentsCreate = capability.Descriptor{
	ID:      Provider + ".comments.create",
	Version: 1,
	Title:   "Comment on a GitHub issue",
	Description: "Write exactly one comment on one issue of a repository an explicit connection allows; a " +
		"repeated call writes a second comment",
	Tags:                       []string{"github", "issues", "comments", "create"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"number":` + numberSchema + `,` +
		`"body":{"type":"string","minLength":1,"maxLength":65536}},"required":["number","body"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + commentProperties + `},` +
		commentRequired + `}`),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Issue number in the repository", Required: true},
		{Name: "body", Description: "Comment in Markdown, 1 to 65536 characters; stored as given", Required: true},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Comment identifier"},
		{Name: "url", Description: "Web address of the comment"},
	},
	Examples: []capability.Example{{
		Description: "Comment on an issue",
		Arguments:   json.RawMessage(`{"number":42,"body":"Fixed in the latest build."}`),
	}},
}

var itemsUpdate = capability.Descriptor{
	ID:      Provider + ".projectitems.update",
	Version: 1,
	Title:   "Update GitHub project item fields",
	Description: "Set or clear single-select, multi-select, text, number, date, and iteration fields of one item of " +
		"a GitHub project an explicit connection allows, by field and option name",
	Tags:                       []string{"github", "projects", "items", "fields", "update", "planning"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"item_id":` + itemIDSchema + `,` +
		`"fields":` + fieldsSchema + `},"required":["item_id","fields"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(planningOutput),
	Arguments: []capability.Argument{
		{Name: "item_id", Description: "Project item identifier, as returned by github.projectitems.list", Required: true},
		{Name: "fields", Description: fieldsDescription, Required: true},
	},
	Fields: planningFields,
	Examples: []capability.Example{{
		Description: "Move an item to In progress and set its estimate",
		Arguments:   json.RawMessage(`{"item_id":"PVTI_lADOAAAAAAAAAAAAzgAAAAA","fields":{"Status":"In progress","Estimate":3}}`),
	}},
}

var itemsAdd = capability.Descriptor{
	ID:      Provider + ".projectitems.add",
	Version: 1,
	Title:   "Add a GitHub issue to the project",
	Description: "Add one existing issue of a repository an explicit connection allows to a GitHub project it " +
		"allows, then set field values; an issue already in the project keeps its item",
	Tags:                       []string{"github", "projects", "items", "add", "planning"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"repository":` + repoSchema + `,` +
		`"number":` + numberSchema + `,"fields":` + fieldsSchema + `},"required":["number"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(planningOutput),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Issue number in the repository", Required: true},
		repositoryArgument,
		{Name: "fields", Description: fieldsDescription},
	},
	Fields: planningFields,
	Examples: []capability.Example{{
		Description: "Add an issue to the project as Todo",
		Arguments:   json.RawMessage(`{"repository":"octo-org/example","number":42,"fields":{"Status":"Todo"}}`),
	}},
}

var itemsArchive = capability.Descriptor{
	ID:      Provider + ".projectitems.archive",
	Version: 1,
	Title:   "Archive a GitHub project item",
	Description: "Archive one item of a GitHub project an explicit connection allows; GitHub keeps it " +
		"restorable",
	Tags:                       []string{"github", "projects", "items", "archive", "planning"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"item_id":` + itemIDSchema + `},` +
		`"required":["item_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"item_id":{"type":"string"},` +
		`"archived":{"type":"boolean"}},"required":["item_id","archived"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "item_id", Description: "Project item identifier, as returned by github.projectitems.list", Required: true},
	},
	Fields: []capability.Field{{Name: "archived", Description: "True once the item is archived"}},
	Examples: []capability.Example{{
		Description: "Archive one item",
		Arguments:   json.RawMessage(`{"item_id":"PVTI_lADOAAAAAAAAAAAAzgAAAAA"}`),
	}},
}

var draftsCreate = capability.Descriptor{
	ID:      Provider + ".projectdrafts.create",
	Version: 1,
	Title:   "Create a GitHub project draft",
	Description: "Add one draft issue to a GitHub project an explicit connection allows, then set field " +
		"values; a repeated call adds a second draft",
	Tags:                       []string{"github", "projects", "drafts", "create", "planning"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"title":` + titleSchema + `,` +
		`"body":` + bodySchema + `,"fields":` + fieldsSchema + `},"required":["title"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(planningOutput),
	Arguments: []capability.Argument{
		{Name: "title", Description: "Draft title, 1 to 256 characters", Required: true},
		{Name: "body", Description: "Draft body in Markdown, at most 65536 characters"},
		{Name: "fields", Description: fieldsDescription},
	},
	Fields: planningFields,
	Examples: []capability.Example{{
		Description: "Note an idea as a draft",
		Arguments:   json.RawMessage(`{"title":"Evaluate a cache for reads","fields":{"Status":"Todo"}}`),
	}},
}

var projectIssuesCreate = capability.Descriptor{
	ID:      Provider + ".projectissues.create",
	Version: 1,
	Title:   "Create a planned GitHub issue",
	Description: "Open one issue in a repository an explicit connection allows, add it to a GitHub project it " +
		"allows, then set field values; a repeated call opens a second issue",
	Tags:                       []string{"github", "projects", "issues", "create", "planning"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"repository":` + repoSchema + `,` +
		issueContentKeys + `,"fields":` + fieldsSchema + `},"required":["title"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(planningOutput),
	Arguments: []capability.Argument{
		{Name: "title", Description: "Issue title, 1 to 256 characters", Required: true},
		issueContentArguments[1],
		{Name: "labels", Description: "Label names to set"},
		{Name: "assignees", Description: "Assignee logins to set"},
		{Name: "fields", Description: fieldsDescription},
		repositoryArgument,
	},
	Fields: append([]capability.Field{
		{Name: "issue", Description: "Number, repository, and URL of the created issue"},
	}, planningFields...),
	Examples: []capability.Example{{
		Description: "Open a planned issue in the project's Todo column",
		Arguments: json.RawMessage(`{"repository":"octo-org/example","title":"Crash on start",` +
			`"body":"Steps to reproduce","fields":{"Status":"Todo","Priority":"P1"}}`),
	}},
}

// unreadable is the answer to arguments the application core validated but this handler cannot decode.
func unreadable(op string) error {
	return providerError(op, "the validated arguments could not be read")
}

func invokeIssuesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var content IssueContent
	if err := json.Unmarshal(raw, &content); err != nil {
		return nil, unreadable("create issue")
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	// The arguments are checked before a credential is resolved, so a refused change never becomes a
	// provider call.
	if err := content.check(true); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.CreateIssue(ctx, content))
}

func invokeIssuesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Number int `json:"number"`
		IssueContent
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable("update issue")
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := checkNumber(arguments.Number); err != nil {
		return nil, err
	}
	if err := arguments.IssueContent.check(false); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.UpdateIssue(ctx, arguments.Number, arguments.IssueContent))
}

func invokeIssuesClose(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Number      int    `json:"number"`
		StateReason string `json:"state_reason"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable("close issue")
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := checkNumber(arguments.Number); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.CloseIssue(ctx, arguments.Number, arguments.StateReason))
}

func invokeIssuesReopen(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable("reopen issue")
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := checkNumber(arguments.Number); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.ReopenIssue(ctx, arguments.Number))
}

func invokeCommentsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options CommentListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, unreadable("list comments")
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	after, err := options.normalize(bound)
	if err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.listComments(ctx, options, after))
}

func invokeCommentsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Number int    `json:"number"`
		Body   string `json:"body"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable("create comment")
	}
	bound, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := checkNumber(arguments.Number); err != nil {
		return nil, err
	}
	if err := checkCommentBody(arguments.Body); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.CreateComment(ctx, arguments.Number, arguments.Body))
}

func invokeItemsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		ItemID string      `json:"item_id"`
		Fields FieldValues `json:"fields"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable("update project item")
	}
	bound, err := selectTarget(resolved, kindProject, raw)
	if err != nil {
		return nil, err
	}
	if err := checkItemID(arguments.ItemID); err != nil {
		return nil, err
	}
	inputs, err := arguments.Fields.inputs()
	if err != nil {
		return nil, err
	}
	if len(inputs) == 0 {
		return nil, invalidRequest("fields must name at least one field")
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.UpdateItemFields(ctx, arguments.ItemID, arguments.Fields))
}

func invokeItemsAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Number int         `json:"number"`
		Fields FieldValues `json:"fields"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable("add project item")
	}
	// Both targets are checked before a credential is resolved: the project the issue joins and the
	// repository it lives in.
	bound, err := selectTarget(resolved, kindProject, raw)
	if err != nil {
		return nil, err
	}
	repo, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := checkNumber(arguments.Number); err != nil {
		return nil, err
	}
	if _, err := arguments.Fields.inputs(); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(repo.locate(client.addIssue(ctx, repo, arguments.Number, arguments.Fields)))
}

func invokeItemsArchive(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		ItemID string `json:"item_id"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable("archive project item")
	}
	bound, err := selectTarget(resolved, kindProject, raw)
	if err != nil {
		return nil, err
	}
	if err := checkItemID(arguments.ItemID); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.ArchiveItem(ctx, arguments.ItemID))
}

func invokeDraftsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Title  string      `json:"title"`
		Body   *string     `json:"body"`
		Fields FieldValues `json:"fields"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable("create draft issue")
	}
	bound, err := selectTarget(resolved, kindProject, raw)
	if err != nil {
		return nil, err
	}
	if err := (IssueContent{Title: &arguments.Title, Body: arguments.Body}).check(true); err != nil {
		return nil, err
	}
	if _, err := arguments.Fields.inputs(); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.CreateDraft(ctx, arguments.Title, arguments.Body, arguments.Fields))
}

func invokeProjectIssuesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Fields FieldValues `json:"fields"`
		IssueContent
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable("create planned issue")
	}
	bound, err := selectTarget(resolved, kindProject, raw)
	if err != nil {
		return nil, err
	}
	repo, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := arguments.IssueContent.check(true); err != nil {
		return nil, err
	}
	if _, err := arguments.Fields.inputs(); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(repo.locate(client.createPlannedIssue(ctx, repo, arguments.IssueContent, arguments.Fields)))
}
