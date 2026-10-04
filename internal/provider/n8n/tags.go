package n8n

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// maxTagNameLength mirrors the Public API's own limit for a tag name (1 to 24 characters).
const maxTagNameLength = 24

// tagDataSensitivity marks tags: instance-wide labels, plain text.
const tagDataSensitivity = "n8n-tags"

// tagsInstanceWide names the capability in a refusal by requireInstanceScope.
const tagsInstanceWide = "tags"

// tagPermissionMessage is the one message of a 403 on a tags endpoint: the API key's tag scope or its
// owner's role may be missing. The body is never read into a message.
const tagPermissionMessage = "n8n refused this tags operation: the API key may lack the tag scope or its " +
	"owner's role may not manage tags; Qatlas cannot tell which"

var tagReadRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: tagDataSensitivity}

func tagChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: tagDataSensitivity}
}

var tagNameSchema = `{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxTagNameLength) + `}`

var tagItemSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"}},` +
	`"required":["id","name"],"additionalProperties":false}`

var tagIDArgument = capability.Argument{Name: "tag_id", Description: "n8n tag identifier", Required: true}

var tagNameArgument = capability.Argument{Name: "name",
	Description: "Tag name, 1 to " + strconv.Itoa(maxTagNameLength) + " characters, no control characters",
	Required:    true}

var tagFields = []capability.Field{
	{Name: "id", Description: "Tag identifier"},
	{Name: "name", Description: "Tag name, untrusted data"},
	{Name: "created_at", Description: "Creation time, as n8n reports it"},
	{Name: "updated_at", Description: "Last update time, as n8n reports it"},
}

const tagInstanceNote = "Tags are instance-wide: refused on a connection with project or workflow targets"

var tagsList = capability.Descriptor{
	ID: Provider + ".tags.list", Version: 1, Title: "List n8n tags",
	Description: "List the tags of the bound n8n instance, page by page with an opaque cursor. " + tagInstanceNote,
	Tags:        []string{"n8n", "tags", "list", "automation"}, Risk: tagReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"cursor":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCursorLength) + `},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxListLimit) + `}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"tags":{"type":"array","items":` + tagItemSchema + `},` +
		`"cursor":{"type":"string"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["tags","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "cursor", Description: "Opaque cursor from a previous call's has_more page"},
		{Name: "limit", Description: "Tags per page, 1 to 250; 100 when omitted"},
	},
	Fields: []capability.Field{
		{Name: "tags", Description: "Tags on this page; id, name (untrusted data), created_at, updated_at"},
		{Name: "cursor", Description: "Cursor for the next page; absent when has_more is false"},
		{Name: "has_more", Description: "True when a further page remains"},
		{Name: "count", Description: "Number of tags on this page"},
	},
	Examples: []capability.Example{{Description: "List the first page of tags", Arguments: json.RawMessage(`{}`)}},
}

var tagsGet = capability.Descriptor{
	ID: Provider + ".tags.get", Version: 1, Title: "Read an n8n tag",
	Description: "Read one tag of the bound n8n instance by ID. " + tagInstanceNote,
	Tags:        []string{"n8n", "tags", "get", "automation"}, Risk: tagReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"tag_id":` + targetIDSchema + `},` +
		`"required":["tag_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(tagItemSchema),
	Arguments:    []capability.Argument{tagIDArgument},
	Fields:       tagFields,
	Examples: []capability.Example{{Description: "Read a tag",
		Arguments: json.RawMessage(`{"tag_id":"2tUt1wbLX592XDdX"}`)}},
}

var tagsCreate = capability.Descriptor{
	ID: Provider + ".tags.create", Version: 1, Title: "Create an n8n tag",
	Description: "Create one tag from a name. A repeated call may be refused as a name conflict. " + tagInstanceNote,
	Tags:        []string{"n8n", "tags", "create", "automation"},
	Risk:        tagChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + tagNameSchema + `},` +
		`"required":["name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(tagItemSchema),
	Arguments:    []capability.Argument{tagNameArgument},
	Fields:       tagFields,
	Examples:     []capability.Example{{Description: "Create a tag", Arguments: json.RawMessage(`{"name":"production"}`)}},
}

var tagsUpdate = capability.Descriptor{
	ID: Provider + ".tags.update", Version: 1, Title: "Rename an n8n tag",
	Description: "Replace the name of one tag. " + tagInstanceNote,
	Tags:        []string{"n8n", "tags", "update", "automation"},
	Risk:        tagChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"tag_id":` + targetIDSchema + `,` +
		`"name":` + tagNameSchema + `},"required":["tag_id","name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(tagItemSchema),
	Arguments:    []capability.Argument{tagIDArgument, tagNameArgument},
	Fields:       tagFields,
	Examples: []capability.Example{{Description: "Rename a tag",
		Arguments: json.RawMessage(`{"tag_id":"2tUt1wbLX592XDdX","name":"staging"}`)}},
}

var tagsDelete = capability.Descriptor{
	ID: Provider + ".tags.delete", Version: 1, Title: "Delete an n8n tag",
	Description: "Delete one tag of the bound n8n instance; it is removed from every workflow that carries it. " +
		tagInstanceNote,
	Tags: []string{"n8n", "tags", "delete", "automation"},
	Risk: tagChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"tag_id":` + targetIDSchema + `},` +
		`"required":["tag_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"id":{"type":"string"},"deleted":{"type":"boolean"}},` +
		`"required":["id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{tagIDArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the deleted tag"},
		{Name: "deleted", Description: "True when n8n accepted the deletion"},
	},
	Examples: []capability.Example{{Description: "Delete a tag", Arguments: json.RawMessage(`{"tag_id":"2tUt1wbLX592XDdX"}`)}},
}

type instanceTagJSON struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// InstanceTag is the stable Qatlas view of one tag. Name is untrusted data.
type InstanceTag struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// InstanceTagsPage is one paginated listing of tags.
type InstanceTagsPage struct {
	Tags    []InstanceTag `json:"tags"`
	Cursor  string        `json:"cursor,omitempty"`
	HasMore bool          `json:"has_more"`
	Count   int           `json:"count"`
}

// TagDeleted is what tags.delete reports.
type TagDeleted struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

func summarizeInstanceTag(t instanceTagJSON) InstanceTag {
	return InstanceTag{ID: bounded(t.ID), Name: bounded(t.Name), CreatedAt: bounded(t.CreatedAt), UpdatedAt: bounded(t.UpdatedAt)}
}

// tagError replaces the generic 403 message with the neutral scope or role message.
func tagError(err error) error {
	if failure, ok := err.(*provider.Error); ok && failure.Class == provider.ClassPermission {
		failure.Message = tagPermissionMessage
	}
	return err
}

// validTagName keeps a tag name inside n8n's own limits and free of control characters.
func validTagName(name string) error {
	if strings.TrimSpace(name) == "" {
		return invalidRequest("name must not be empty")
	}
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxTagNameLength {
		return invalidRequest("name must be valid text of at most " + strconv.Itoa(maxTagNameLength) + " characters")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return invalidRequest("name must not contain control characters")
		}
	}
	return nil
}

type tagArguments struct {
	TagID  string `json:"tag_id"`
	Name   string `json:"name"`
	Cursor string `json:"cursor"`
	Limit  int    `json:"limit"`
}

// prepareTag reads the arguments and applies the instance-wide gate, before any secret or request.
func prepareTag(op string, resolved *config.Resolved, raw json.RawMessage) (tagArguments, error) {
	var input tagArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	if err := requireInstanceScope(resolved, tagsInstanceWide); err != nil {
		return input, err
	}
	return input, nil
}

func validTagID(id string) error {
	if !validTargetID(id) {
		return invalidRequest("tag_id must be a usable n8n identifier")
	}
	return nil
}

func invokeTagsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := prepareTag("list tags", resolved, raw)
	if err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	page, err := client.ListTags(ctx, input.Cursor, limit)
	return page, tagError(err)
}

// ListTags reads one page of GET /tags.
func (c *Client) ListTags(ctx context.Context, cursor string, limit int) (*InstanceTagsPage, error) {
	const op = "list tags"
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var page struct {
		Data       []instanceTagJSON `json:"data"`
		NextCursor *string           `json:"nextCursor"`
	}
	if err := c.get(ctx, op, "/tags", query, &page, maxResponseBytes); err != nil {
		return nil, err
	}
	next := ""
	if page.NextCursor != nil {
		if len(*page.NextCursor) > maxCursorLength {
			return nil, invalidResponse(op, "n8n reported an oversized page cursor")
		}
		next = *page.NextCursor
	}
	tags := make([]InstanceTag, 0, len(page.Data))
	for _, t := range page.Data {
		tags = append(tags, summarizeInstanceTag(t))
	}
	return &InstanceTagsPage{Tags: tags, Cursor: next, HasMore: next != "", Count: len(tags)}, nil
}

func invokeTagsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read tag"
	input, err := prepareTag(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	if err := validTagID(input.TagID); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var tag instanceTagJSON
	if err := client.get(ctx, op, "/tags/"+url.PathEscape(input.TagID), nil, &tag, maxResponseBytes); err != nil {
		return nil, tagError(err)
	}
	return summarizeInstanceTag(tag), nil
}

func invokeTagsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create tag"
	input, err := prepareTag(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	if err := validTagName(input.Name); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var tag instanceTagJSON
	if err := client.change(ctx, op, http.MethodPost, "/tags", nil, map[string]any{"name": input.Name}, &tag,
		maxResponseBytes); err != nil {
		return nil, tagError(err)
	}
	return summarizeInstanceTag(tag), nil
}

func invokeTagsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update tag"
	input, err := prepareTag(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	if err := validTagID(input.TagID); err != nil {
		return nil, err
	}
	if err := validTagName(input.Name); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var tag instanceTagJSON
	if err := client.change(ctx, op, http.MethodPut, "/tags/"+url.PathEscape(input.TagID), nil,
		map[string]any{"name": input.Name}, &tag, maxResponseBytes); err != nil {
		return nil, tagError(err)
	}
	return summarizeInstanceTag(tag), nil
}

func invokeTagsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete tag"
	input, err := prepareTag(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	if err := validTagID(input.TagID); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.change(ctx, op, http.MethodDelete, "/tags/"+url.PathEscape(input.TagID), nil, nil, nil,
		maxResponseBytes); err != nil {
		return nil, tagError(err)
	}
	return &TagDeleted{ID: input.TagID, Deleted: true}, nil
}
