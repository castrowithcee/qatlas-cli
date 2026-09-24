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

// The item tools delete, restore, and order the items of a project, and change or convert its draft issues.
// Every item they name, the one they change and the one an item is moved after, is resolved together with the
// project in one query and must belong to it; a draft change also resolves the draft issue of the item and the
// users it assigns, and a conversion the repository the issue is created in, before the one change is sent.

var itemIDArgument = capability.Argument{Name: "item_id", Description: "Project item identifier, as returned " +
	"by github.projectitems.list", Required: true}

const itemIDInput = `{"type":"object","properties":{"item_id":` + itemIDSchema + `},"required":["item_id"],` +
	`"additionalProperties":false}`

var itemsDelete = capability.Descriptor{
	ID:      Provider + ".projectitems.delete",
	Version: 1,
	Title:   "Delete a GitHub project item",
	Description: "Remove one item from a GitHub project an explicit connection allows, with its field values; " +
		"an issue or pull request stays in its repository, while a draft issue is deleted with its item. " +
		"Offered only by a connection whose tools list names it",
	Tags:                       []string{"github", "projects", "items", "delete"},
	Risk:                       changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema:                json.RawMessage(itemIDInput),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"item_id":{"type":"string"},` +
		`"deleted":{"type":"boolean"}},"required":["item_id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{itemIDArgument},
	Fields:    []capability.Field{{Name: "deleted", Description: "True once GitHub removed the item"}},
	Examples: []capability.Example{{
		Description: "Remove one item from the project",
		Arguments:   json.RawMessage(`{"item_id":"PVTI_lADOAAAAAAAAAAAAzgAAAAA"}`),
	}},
}

var itemsUnarchive = capability.Descriptor{
	ID:                         Provider + ".projectitems.unarchive",
	Version:                    1,
	Title:                      "Restore an archived GitHub project item",
	Description:                "Restore one archived item of a GitHub project an explicit connection allows",
	Tags:                       []string{"github", "projects", "items", "unarchive", "planning"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                json.RawMessage(itemIDInput),
	OutputSchema:               itemsArchive.OutputSchema,
	Arguments:                  []capability.Argument{itemIDArgument},
	Fields:                     []capability.Field{{Name: "archived", Description: "False once the item is restored"}},
	Examples: []capability.Example{{
		Description: "Restore one item",
		Arguments:   json.RawMessage(`{"item_id":"PVTI_lADOAAAAAAAAAAAAzgAAAAA"}`),
	}},
}

var itemsMove = capability.Descriptor{
	ID:      Provider + ".projectitems.move",
	Version: 1,
	Title:   "Move a GitHub project item",
	Description: "Place one item of a GitHub project an explicit connection allows directly after another item " +
		"of it, or first without after_id; this is the project order that views without a sort show",
	Tags:                       []string{"github", "projects", "items", "move", "order", "planning"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"item_id":` + itemIDSchema + `,` +
		`"after_id":` + itemIDSchema + `},"required":["item_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"item_id":{"type":"string"},` +
		`"after_id":{"type":"string"},"moved":{"type":"boolean"}},"required":["item_id","moved"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		itemIDArgument,
		{Name: "after_id", Description: "Identifier of the item of the same project to place it after; the item " +
			"moves to the top when omitted"},
	},
	Fields: []capability.Field{
		{Name: "after_id", Description: "The item it now follows, absent when it moved to the top"},
		{Name: "moved", Description: "True once GitHub placed the item"},
	},
	Examples: []capability.Example{{
		Description: "Place an item after another one",
		Arguments:   json.RawMessage(`{"item_id":"PVTI_lADOAAAAAAAAAAAAzgAAAAA","after_id":"PVTI_lADOAAAAAAAAAAAAzgAAAAB"}`),
	}},
}

var draftsUpdate = capability.Descriptor{
	ID:      Provider + ".projectdrafts.update",
	Version: 1,
	Title:   "Update a GitHub project draft",
	Description: "Replace the title, body, or assignees of one draft issue of a GitHub project an explicit " +
		"connection allows; fields left out stay unchanged",
	Tags:                       []string{"github", "projects", "drafts", "update", "planning"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"item_id":` + itemIDSchema + `,` +
		`"title":` + titleSchema + `,"body":` + bodySchema + `,"assignees":` + assigneesSchema + `},` +
		`"required":["item_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"item_id":{"type":"string"},` +
		`"title":{"type":"string"},"assignees":` + stringListSchema + `},"required":["item_id","title","assignees"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "item_id", Description: "Identifier of the project item of the draft, as returned by " +
			"github.projectitems.list", Required: true},
		{Name: "title", Description: "Draft title, 1 to 256 characters"},
		{Name: "body", Description: "Draft body in Markdown, at most 65536 characters; stored as given, \"\" " +
			"empties it"},
		{Name: "assignees", Description: "Assignee logins; replaces every assignee of the draft, [] removes them all"},
	},
	Fields: []capability.Field{
		{Name: "title", Description: "Title of the draft after the change, untrusted data"},
		{Name: "assignees", Description: "Logins assigned to the draft after the change"},
	},
	Examples: []capability.Example{{
		Description: "Retitle a draft and assign it",
		Arguments:   json.RawMessage(`{"item_id":"PVTI_lADOAAAAAAAAAAAAzgAAAAA","title":"Evaluate a read cache","assignees":["octocat"]}`),
	}},
}

var draftsConvert = capability.Descriptor{
	ID:      Provider + ".projectdrafts.convert",
	Version: 1,
	Title:   "Convert a GitHub project draft into an issue",
	Description: "Turn one draft issue of a GitHub project an explicit connection allows into an issue of a " +
		"repository it allows; the item keeps its place and field values. A converted item is no draft any " +
		"more, so a repeated call is refused and opens no second issue",
	Tags:                       []string{"github", "projects", "drafts", "issues", "convert", "planning"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"item_id":` + itemIDSchema + `,` +
		`"repository":` + repoSchema + `},"required":["item_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"item_id":{"type":"string"},` +
		`"issue":{"type":"object","properties":{"number":{"type":"integer"},"repository":{"type":"string"},` +
		`"url":{"type":"string"}},"required":["number","repository"],"additionalProperties":false}},` +
		`"required":["item_id","issue"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "item_id", Description: "Identifier of the project item of the draft, as returned by " +
			"github.projectitems.list", Required: true},
		repositoryArgument,
	},
	Fields: []capability.Field{
		{Name: "item_id", Description: "The project item, which now holds the issue"},
		{Name: "issue", Description: "Number, repository, and URL of the created issue"},
	},
	Examples: []capability.Example{{
		Description: "Turn a draft into an issue",
		Arguments:   json.RawMessage(`{"item_id":"PVTI_lADOAAAAAAAAAAAAzgAAAAA","repository":"octo-org/example"}`),
	}},
}

// itemOperations are the item and draft tools.
func itemOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: itemsDelete, Handler: capability.Handler(invokeItemsDelete)},
		{Descriptor: itemsUnarchive, Handler: capability.Handler(invokeItemsUnarchive)},
		{Descriptor: itemsMove, Handler: capability.Handler(invokeItemsMove)},
		{Descriptor: draftsUpdate, Handler: capability.Handler(invokeDraftsUpdate)},
		{Descriptor: draftsConvert, Handler: capability.Handler(invokeDraftsConvert)},
	}
}

// DeletedItem is the answer of a deleted item.
type DeletedItem struct {
	ItemID  string `json:"item_id"`
	Deleted bool   `json:"deleted"`
}

// MovedItem is the answer of a moved item.
type MovedItem struct {
	ItemID  string `json:"item_id"`
	AfterID string `json:"after_id,omitempty"`
	Moved   bool   `json:"moved"`
}

// Draft is a draft issue after a change.
type Draft struct {
	ItemID    string   `json:"item_id"`
	Title     string   `json:"title"`
	Assignees []string `json:"assignees"`
}

// ConvertedDraft is the answer of a draft turned into an issue.
type ConvertedDraft struct {
	ItemID string   `json:"item_id"`
	Issue  IssueRef `json:"issue"`
}

// itemRef is the argument of a delete or a restore.
type itemRef struct {
	ItemID string `json:"item_id"`
}

func (ref itemRef) check() error { return checkItemID(ref.ItemID) }

// ItemMove places an item after another item of the project, or first when AfterID is empty.
type ItemMove struct {
	ItemID  string `json:"item_id"`
	AfterID string `json:"after_id"`
}

func (move ItemMove) check() error {
	if err := checkItemID(move.ItemID); err != nil {
		return err
	}
	if move.AfterID == "" {
		return nil
	}
	if move.AfterID == move.ItemID {
		return invalidRequest("after_id must name another item than item_id")
	}
	return checkItemID(move.AfterID)
}

// DraftChanges are the settings a draft update writes; a nil setting stays unchanged.
type DraftChanges struct {
	ItemID    string    `json:"item_id"`
	Title     *string   `json:"title"`
	Body      *string   `json:"body"`
	Assignees *[]string `json:"assignees"`
}

func (changes DraftChanges) check() error {
	if err := checkItemID(changes.ItemID); err != nil {
		return err
	}
	if changes.Title == nil && changes.Body == nil && changes.Assignees == nil {
		return invalidRequest("name at least one of title, body, or assignees to change")
	}
	return IssueContent{Title: changes.Title, Body: changes.Body, Assignees: changes.Assignees}.check(false)
}

// The mutations of the items. Every identifier and value travels as a variable.
const (
	deleteItemMutation = `mutation($project:ID!,$item:ID!){item:deleteProjectV2Item(` +
		`input:{projectId:$project,itemId:$item}){deletedItemId}}`
	unarchiveMutation = `mutation($project:ID!,$item:ID!){item:unarchiveProjectV2Item(` +
		`input:{projectId:$project,itemId:$item}){item{id}}}`
	moveItemMutation = `mutation($project:ID!,$item:ID!,$after:ID){item:updateProjectV2ItemPosition(` +
		`input:{projectId:$project,itemId:$item,afterId:$after}){clientMutationId}}`
	convertDraftMutation = `mutation($item:ID!,$repository:ID!){item:convertProjectV2DraftIssueItemToIssue(` +
		`input:{itemId:$item,repositoryId:$repository}){item{id content{... on Issue{number url}}}}}`
)

// projectItem checks that a client is bound to a project and resolves the project with the item it changes.
func (c *Client) projectItem(ctx context.Context, op string, request planningRequest) (*projectInfo, planningNodes, error) {
	if c.target.kind != kindProject {
		return nil, planningNodes{}, providerError(op, "this connection is not bound to a project")
	}
	if err := checkItemID(request.item); err != nil {
		return nil, planningNodes{}, err
	}
	return c.resolve(ctx, op, request)
}

// DeleteItem removes one item from the bound project. An issue or pull request stays in its repository; a
// draft issue is deleted with its item.
func (c *Client) DeleteItem(ctx context.Context, itemID string) (*DeletedItem, error) {
	const op = "delete project item"
	info, _, err := c.projectItem(ctx, op, planningRequest{item: itemID})
	if err != nil {
		return nil, err
	}
	var answer struct {
		Item *struct {
			ID string `json:"deletedItemId"`
		} `json:"item"`
	}
	if err := c.mutate(ctx, op, deleteItemMutation, map[string]any{"project": info.id, "item": itemID},
		&answer); err != nil {
		return nil, err
	}
	if answer.Item == nil || answer.Item.ID != itemID {
		return nil, invalidResponse(op, true)
	}
	return &DeletedItem{ItemID: itemID, Deleted: true}, nil
}

// UnarchiveItem restores one archived item of the bound project.
func (c *Client) UnarchiveItem(ctx context.Context, itemID string) (*Archived, error) {
	const op = "unarchive project item"
	info, _, err := c.projectItem(ctx, op, planningRequest{item: itemID})
	if err != nil {
		return nil, err
	}
	var answer struct {
		Item *struct {
			Item *struct {
				ID string `json:"id"`
			} `json:"item"`
		} `json:"item"`
	}
	if err := c.mutate(ctx, op, unarchiveMutation, map[string]any{"project": info.id, "item": itemID},
		&answer); err != nil {
		return nil, err
	}
	if answer.Item == nil || answer.Item.Item == nil || answer.Item.Item.ID != itemID {
		return nil, invalidResponse(op, true)
	}
	return &Archived{ItemID: itemID, Archived: false}, nil
}

// MoveItem places one item of the bound project after another item of it, or first.
func (c *Client) MoveItem(ctx context.Context, move ItemMove) (*MovedItem, error) {
	const op = "move project item"
	if err := move.check(); err != nil {
		return nil, err
	}
	info, _, err := c.projectItem(ctx, op, planningRequest{item: move.ItemID, after: move.AfterID})
	if err != nil {
		return nil, err
	}
	variables := map[string]any{"project": info.id, "item": move.ItemID, "after": nil}
	if move.AfterID != "" {
		variables["after"] = move.AfterID
	}
	var answer struct {
		Item *json.RawMessage `json:"item"`
	}
	if err := c.mutate(ctx, op, moveItemMutation, variables, &answer); err != nil {
		return nil, err
	}
	if answer.Item == nil {
		return nil, invalidResponse(op, true)
	}
	return &MovedItem{ItemID: move.ItemID, AfterID: move.AfterID, Moved: true}, nil
}

// UpdateDraft changes the named settings of one draft issue of the bound project in one mutation. The draft
// and the users to assign are resolved with the project first.
func (c *Client) UpdateDraft(ctx context.Context, changes DraftChanges) (*Draft, error) {
	const op = "update draft issue"
	if err := changes.check(); err != nil {
		return nil, err
	}
	request := planningRequest{item: changes.ItemID, draft: true}
	if changes.Assignees != nil {
		request.assignees = *changes.Assignees
	}
	_, nodes, err := c.projectItem(ctx, op, request)
	if err != nil {
		return nil, err
	}
	declarations, inputs := []string{"$draft:ID!"}, []string{"draftIssueId:$draft"}
	variables := map[string]any{"draft": nodes.draft}
	for _, setting := range []struct {
		name, input, kind string
		value             any
		set               bool
	}{
		{"title", "title", "String!", changes.Title, changes.Title != nil},
		{"body", "body", "String!", changes.Body, changes.Body != nil},
		{"assignees", "assigneeIds", "[ID!]!", append([]string{}, nodes.assignees...), changes.Assignees != nil},
	} {
		if setting.set {
			declarations = append(declarations, "$"+setting.name+":"+setting.kind)
			inputs = append(inputs, setting.input+":$"+setting.name)
			variables[setting.name] = setting.value
		}
	}
	document := "mutation(" + strings.Join(declarations, ",") + "){draft:updateProjectV2DraftIssue(input:{" +
		strings.Join(inputs, ",") + "}){draftIssue{id title assignees(first:10){nodes{login}}}}}"
	var answer struct {
		Draft *struct {
			Issue *struct {
				ID        string `json:"id"`
				Title     string `json:"title"`
				Assignees struct {
					Nodes []struct {
						Login string `json:"login"`
					} `json:"nodes"`
				} `json:"assignees"`
			} `json:"draftIssue"`
		} `json:"draft"`
	}
	if err := c.mutate(ctx, op, document, variables, &answer); err != nil {
		return nil, err
	}
	if answer.Draft == nil || answer.Draft.Issue == nil || answer.Draft.Issue.ID != nodes.draft {
		return nil, invalidResponse(op, true)
	}
	draft := &Draft{ItemID: changes.ItemID, Title: answer.Draft.Issue.Title, Assignees: []string{}}
	for _, node := range answer.Draft.Issue.Assignees.Nodes {
		draft.Assignees = append(draft.Assignees, node.Login)
	}
	return draft, nil
}

// ConvertDraft turns one draft issue of the bound project into an issue of a repository the connection
// allows.
func (c *Client) ConvertDraft(ctx context.Context, repository, itemID string) (*ConvertedDraft, error) {
	repo, err := c.allowed.choose(kindRepository, repository)
	if err != nil {
		return nil, err
	}
	return c.convertDraft(ctx, repo, itemID)
}

// convertDraft is ConvertDraft for a repository selectTarget has already checked. The item keeps its
// identifier, so a repeated conversion finds no draft and is refused before any change.
func (c *Client) convertDraft(ctx context.Context, repo target, itemID string) (*ConvertedDraft, error) {
	const op = "convert draft issue"
	_, nodes, err := c.projectItem(ctx, op, planningRequest{item: itemID, draft: true, repository: repo})
	if err != nil {
		return nil, err
	}
	var answer struct {
		Item *struct {
			Item *struct {
				ID      string `json:"id"`
				Content *struct {
					Number int    `json:"number"`
					URL    string `json:"url"`
				} `json:"content"`
			} `json:"item"`
		} `json:"item"`
	}
	if err := c.mutate(ctx, op, convertDraftMutation, map[string]any{"item": itemID,
		"repository": nodes.repository}, &answer); err != nil {
		return nil, err
	}
	if answer.Item == nil || answer.Item.Item == nil || answer.Item.Item.ID == "" ||
		answer.Item.Item.Content == nil || answer.Item.Item.Content.Number < 1 {
		return nil, invalidResponse(op, true)
	}
	return &ConvertedDraft{ItemID: answer.Item.Item.ID, Issue: IssueRef{Number: answer.Item.Item.Content.Number,
		Repository: repo.owner + "/" + repo.repo, URL: answer.Item.Item.Content.URL}}, nil
}

var (
	invokeItemsDelete = projectHandler("delete project item",
		func(c *Client, ctx context.Context, ref itemRef) (any, error) { return c.DeleteItem(ctx, ref.ItemID) })
	invokeItemsUnarchive = projectHandler("unarchive project item",
		func(c *Client, ctx context.Context, ref itemRef) (any, error) {
			return c.UnarchiveItem(ctx, ref.ItemID)
		})
	invokeItemsMove = projectHandler("move project item",
		func(c *Client, ctx context.Context, move ItemMove) (any, error) { return c.MoveItem(ctx, move) })
	invokeDraftsUpdate = projectHandler("update draft issue",
		func(c *Client, ctx context.Context, changes DraftChanges) (any, error) {
			return c.UpdateDraft(ctx, changes)
		})
)

// invokeDraftsConvert checks both targets before a credential is resolved: the project that holds the draft
// and the repository the issue is created in.
func invokeDraftsConvert(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments itemRef
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable("convert draft issue")
	}
	bound, err := selectTarget(resolved, kindProject, raw)
	if err != nil {
		return nil, err
	}
	repo, err := selectTarget(resolved, kindRepository, raw)
	if err != nil {
		return nil, err
	}
	if err := arguments.check(); err != nil {
		return nil, err
	}
	client, err := openAt(resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(repo.locate(client.convertDraft(ctx, repo, arguments.ItemID)))
}
