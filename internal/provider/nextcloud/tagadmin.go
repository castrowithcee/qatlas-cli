package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Administration of the system tag catalog (apps/dav SystemTagPlugin): a POST of a JSON document on the
// catalog creates a tag and names it in Content-Location, a PROPPATCH on the tag changes it, and a DELETE
// removes it together with every assignment. The rights of the identity stay the upper bound.

const (
	modeCreate = "create"
	modeUpdate = "update"
	modeDelete = "delete"

	// maxTagName is the length Nextcloud stores for a tag name.
	maxTagName  = 64
	tagColorLen = 6

	uncertainTagAdmin = "; the change may have been applied, check systemtags.list before repeating"

	messageTagAdminDenied = "this Nextcloud identity may not create, change, or delete this system tag"
	messageTagExists      = "a system tag with this name already exists"
	messageTagVisibility  = "Nextcloud refused the visibility or assignability; only an administrator may " +
		"create invisible or unassignable system tags"
	messageTagRefused = "Nextcloud refused to change this system tag; its name may exist already or this " +
		"identity may not change it"
	messageTagNoID = "; Nextcloud named no usable tag_id, find the tag with systemtags.list"
)

const (
	tagNameSchema  = `{"type":"string","minLength":1,"maxLength":64,"x-form":"a tag name without control characters"}`
	tagColorSchema = `{"type":"string","pattern":"^[0-9a-fA-F]{6}$","x-form":"six hex digits without #"}`
)

var tagAdminFields = []capability.Field{
	{Name: "tag_id", Description: "Identifier of the tag; pass it to systemtags.update, systemtags.delete, filetags.add"},
}

func tagAdminRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: dataSensitivity}
}

var (
	nameArgument    = capability.Argument{Name: "name", Description: "Name of the tag, at most 64 characters"}
	visibleArgument = capability.Argument{Name: "visible",
		Description: "Whether users see the tag; false needs administrator rights"}
	assignableArgument = capability.Argument{Name: "assignable",
		Description: "Whether users may assign the tag; false needs administrator rights"}
)

var systemtagsCreate = capability.Descriptor{
	ID: Provider + ".systemtags.create", Version: 1, Title: "Create a Nextcloud system tag",
	Description: "Create exactly one confirmed system tag in the catalog of the Nextcloud instance, which is shared by " +
		"the whole instance; visible and assignable default to true, a duplicate name is refused",
	Tags: []string{"nextcloud", "systemtags", "tags", "webdav", "create"}, Provider: Provider,
	Risk: tagAdminRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + tagNameSchema +
		`,"visible":{"type":"boolean"},"assignable":{"type":"boolean"}},"required":["name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"created":{"type":"boolean"},"tag_id":{"type":"string"},` +
		`"name":{"type":"string"},"note":{"type":"string"}},"required":["created","name"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: nameArgument.Name, Required: true, Description: nameArgument.Description}, visibleArgument, assignableArgument},
	Fields: append([]capability.Field{{Name: "created", Description: "True when Nextcloud created the tag"},
		{Name: "name", Description: "Name that was sent"}}, tagAdminFields...),
	Examples: []capability.Example{{Description: "Create a visible, assignable tag", Arguments: json.RawMessage(`{"name":"Invoice"}`)}},
}

var systemtagsUpdate = capability.Descriptor{
	ID: Provider + ".systemtags.update", Version: 1, Title: "Change a Nextcloud system tag",
	Description: "Change the name, visibility, assignability, or color of exactly one confirmed visible system tag of " +
		"the Nextcloud instance, at least one field; one read-only pre-check precedes exactly one change request",
	Tags: []string{"nextcloud", "systemtags", "tags", "webdav", "update"}, Provider: Provider,
	Risk: tagAdminRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"tag_id":` + tagIDSchema + `,"name":` + tagNameSchema +
		`,"visible":{"type":"boolean"},"assignable":{"type":"boolean"},"color":` + tagColorSchema +
		`},"required":["tag_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"},"tag_id":{"type":"string"}},` +
		`"required":["updated","tag_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{tagIDArgument, nameArgument, visibleArgument, assignableArgument,
		{Name: "color", Description: "Color of the tag as six hex digits without #"}},
	Fields:   append([]capability.Field{{Name: "updated", Description: "True when Nextcloud applied the change"}}, tagAdminFields...),
	Examples: []capability.Example{{Description: "Rename a tag", Arguments: json.RawMessage(`{"tag_id":"7","name":"Invoices"}`)}},
}

var systemtagsDelete = capability.Descriptor{
	ID: Provider + ".systemtags.delete", Version: 1, Title: "Delete a Nextcloud system tag",
	Description: "Delete exactly one confirmed visible system tag from the Nextcloud instance; every assignment of the " +
		"tag to any file of the instance is dropped with it. One read-only pre-check precedes exactly one request",
	Tags: []string{"nextcloud", "systemtags", "tags", "webdav", "delete"}, Provider: Provider,
	Risk: tagAdminRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"tag_id":` + tagIDSchema +
		`},"required":["tag_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"},"tag_id":{"type":"string"}},` +
		`"required":["deleted","tag_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{tagIDArgument},
	Fields:    append([]capability.Field{{Name: "deleted", Description: "True when Nextcloud deleted the tag"}}, tagAdminFields...),
	Examples:  []capability.Example{{Description: "Use a tag_id from systemtags.list", Arguments: json.RawMessage(`{"tag_id":"7"}`)}},
	// Reachable only through a tools list, never through a profile.
	RequiresToolAllowList: true,
}

type tagAdminArguments struct {
	TagID          string  `json:"tag_id"`
	Name           *string `json:"name"`
	UserVisible    *bool   `json:"visible"`
	UserAssignable *bool   `json:"assignable"`
	Color          *string `json:"color"`
}

// readTagAdminArguments decodes strictly and validates every value before any credential access. The
// fields that do not belong to the operation are refused by the schema and again here.
func readTagAdminArguments(op string, raw json.RawMessage, mode string) (tagAdminArguments, error) {
	var input tagAdminArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	create, update := mode == modeCreate, mode == modeUpdate
	if !create && !validTagID(input.TagID) {
		return input, providerError(op, "the tag ID must be a tag_id reported by "+systemtagsList.ID)
	}
	if create && input.TagID != "" {
		return input, providerError(op, "a new tag has no tag_id")
	}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if name == "" || utf8.RuneCountInString(name) > maxTagName || hasControl(name) || !utf8.ValidString(name) {
			return input, providerError(op, "the name must be 1 to 64 characters without control characters")
		}
		input.Name = &name
	}
	if input.Color != nil && !validTagColor(*input.Color) {
		return input, providerError(op, "the color must be six hex digits without #")
	}
	changes := input.Name != nil || input.UserVisible != nil || input.UserAssignable != nil || input.Color != nil
	switch {
	case mode == modeDelete && changes:
		return input, providerError(op, "only a tag_id is accepted")
	case create && input.Name == nil:
		return input, providerError(op, "a name is required")
	case create && input.Color != nil:
		return input, providerError(op, "a new tag takes no color; change it afterwards")
	case update && !changes:
		return input, providerError(op, "at least one field to change is required")
	}
	return input, nil
}

func validTagColor(value string) bool {
	if len(value) != tagColorLen {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func boolOr(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func xmlBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// tagCreateBody is the fixed JSON document of a creation; json.Marshal does the escaping.
func tagCreateBody(name string, visible, assignable bool) (string, error) {
	raw, err := json.Marshal(struct {
		Name           string `json:"name"`
		UserVisible    bool   `json:"userVisible"`
		UserAssignable bool   `json:"userAssignable"`
	}{name, visible, assignable})
	return string(raw), err
}

// tagUpdateBody is the fixed PROPPATCH document for the fields that were given.
func tagUpdateBody(input tagAdminArguments) string {
	var props strings.Builder
	if input.Name != nil {
		props.WriteString(`<oc:display-name>` + xmlText(*input.Name) + `</oc:display-name>`)
	}
	if input.UserVisible != nil {
		props.WriteString(`<oc:user-visible>` + xmlBool(*input.UserVisible) + `</oc:user-visible>`)
	}
	if input.UserAssignable != nil {
		props.WriteString(`<oc:user-assignable>` + xmlBool(*input.UserAssignable) + `</oc:user-assignable>`)
	}
	if input.Color != nil {
		props.WriteString(`<nc:color>` + xmlText(*input.Color) + `</nc:color>`)
	}
	return xmlHeader + `<d:propertyupdate xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns" xmlns:nc="http://nextcloud.org/ns">` +
		`<d:set><d:prop>` + props.String() + `</d:prop></d:set></d:propertyupdate>`
}

// tagAdminError replaces the generic message of a status that has a precise meaning for tag administration.
// It keeps the class and never carries provider text.
func tagAdminError(op string, err error, messages map[int]string) error {
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		return err
	}
	for code, message := range messages {
		if providerErr.Message == statusError(op, code).(*provider.Error).Message {
			changed := *providerErr
			changed.Message = message
			return &changed
		}
	}
	return err
}

func tagAdminOpen(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return open(ctx, resolved, secrets, red, false)
}

func invokeSystemTagsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create system tag"
	input, err := readTagAdminArguments(op, raw, modeCreate)
	if err != nil {
		return nil, err
	}
	client, err := tagAdminOpen(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.createTag(ctx, op, input)
}

func invokeSystemTagsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update system tag"
	input, err := readTagAdminArguments(op, raw, modeUpdate)
	if err != nil {
		return nil, err
	}
	client, err := tagAdminOpen(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.updateTag(ctx, op, input)
}

func invokeSystemTagsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete system tag"
	input, err := readTagAdminArguments(op, raw, modeDelete)
	if err != nil {
		return nil, err
	}
	client, err := tagAdminOpen(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.deleteTag(ctx, op, input)
}

// createTag sends exactly one POST. The new tag_id is taken from Content-Location only when it names a
// direct child of the catalog; otherwise the tag is reported as created without an ID, because the server
// did act.
func (c *Client) createTag(ctx context.Context, op string, input tagAdminArguments) (any, error) {
	body, err := tagCreateBody(*input.Name, boolOr(input.UserVisible, true), boolOr(input.UserAssignable, true))
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	extra := http.Header{"Content-Type": {"application/json"}, "Accept": {"application/json"}}
	response, err := c.webdavTo(ctx, op, http.MethodPost, c.tagURL()+"/", strings.NewReader(body), "", "", uncertainTagAdmin, extra)
	if err != nil {
		return nil, tagAdminError(op, err, map[int]string{
			http.StatusForbidden: messageTagAdminDenied, http.StatusConflict: messageTagExists,
			http.StatusBadRequest: messageTagVisibility})
	}
	response.Body.Close()
	result := map[string]any{"created": true, "name": *input.Name}
	below, err := c.segmentsBelow(op, response.Header.Get("Content-Location"), c.tagsPrefix())
	if err != nil || len(below) != 1 || !validTagID(below[0]) {
		result["note"] = "created" + messageTagNoID
		return result, nil
	}
	result["tag_id"] = below[0]
	return result, nil
}

// updateTag reads the tag once and sends exactly one PROPPATCH.
func (c *Client) updateTag(ctx context.Context, op string, input tagAdminArguments) (any, error) {
	if _, err := c.fetchTag(ctx, op, input.TagID); err != nil {
		return nil, err
	}
	resources, err := c.multistatusAt(ctx, op, methodProppatch, c.tagURL(input.TagID), tagUpdateBody(input),
		"application/xml; charset=utf-8", uncertainTagAdmin, 1, false)
	if err != nil {
		return nil, tagAdminError(op, err, map[int]string{
			http.StatusForbidden: messageTagAdminDenied, http.StatusConflict: messageTagExists})
	}
	below, err := c.segmentsBelow(op, resources[0].href, c.tagsPrefix())
	if err != nil || len(below) != 1 || below[0] != input.TagID {
		return nil, withUncertainty(invalidResponse(op, messageForeignEntry), uncertainTagAdmin)
	}
	if !resources[0].read {
		// The refused property carries no status this parser keeps, so a taken name and a missing right
		// cannot be told apart here.
		return nil, providerError(op, messageTagRefused)
	}
	return map[string]any{"updated": true, "tag_id": input.TagID}, nil
}

// deleteTag reads the tag once and sends exactly one DELETE.
func (c *Client) deleteTag(ctx context.Context, op string, input tagAdminArguments) (any, error) {
	if _, err := c.fetchTag(ctx, op, input.TagID); err != nil {
		return nil, err
	}
	response, err := c.webdavTo(ctx, op, http.MethodDelete, c.tagURL(input.TagID), nil, "", "", uncertainTagAdmin, nil)
	if err != nil {
		return nil, tagAdminError(op, err, map[int]string{
			http.StatusForbidden: messageTagAdminDenied, http.StatusNotFound: messageTagMissing})
	}
	response.Body.Close()
	return map[string]any{"deleted": true, "tag_id": input.TagID}, nil
}
