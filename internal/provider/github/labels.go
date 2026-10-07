package github

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Labels of a repository. github.labels.list and github.labels.get read them; github.labels.create and
// github.labels.update change them and need their own confirmation; github.labels.delete removes one
// permanently and is offered only where a connection's tools list names it, since it strips the label from
// every issue and pull request that carries it. A label is addressed by its name, matched exactly and
// URL-encoded as one opaque path segment, the way github.tags.get escapes a tag name; a name never opens
// another route. Every call takes the repository the same way every other repository tool does.

// labelSensitivity classifies github.labels.delete as its own guarded change, the way rulesetSensitivity and
// customPropertySensitivity classify theirs: removing a label reaches every issue and pull request that
// carries it, not just the label itself.
const labelSensitivity = "github-label"

// Bounds of a label. GitHub documents a label name and a description at up to 50 and 100 characters.
const (
	maxLabelNameLength        = 50
	maxLabelDescriptionLength = 100
)

const (
	labelNameSchema        = `{"type":"string","minLength":1,"maxLength":50}`
	labelColorSchema       = `{"type":"string","pattern":"^[0-9A-Fa-f]{6}$","x-form":"6-digit hex color without #"}`
	labelDescriptionSchema = `{"type":"string","maxLength":100}`
)

const labelProperties = `"name":{"type":"string"},"color":{"type":"string"},` +
	`"description":{"type":"string"},"default":{"type":"boolean"}`

const labelRequired = `"required":["name","color","default"],"additionalProperties":false`

const labelOutput = `{"type":"object","properties":{` + labelProperties + `},` + labelRequired + `}`

var labelsList = capability.Descriptor{
	ID:      Provider + ".labels.list",
	Version: 1,
	Title:   "List GitHub labels",
	Description: "List one bounded batch of the labels of a repository a connection allows, with " +
		"their color, description, and whether GitHub created them by default",
	Tags:         []string{"github", "labels", "list"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(pagingKeys),
	OutputSchema: listOutput("labels", labelProperties, labelRequired),
	Arguments:    pagingArguments,
	Fields: append([]capability.Field{
		{Name: "labels", Description: "Labels with name, color, description, and default (true for a label " +
			"GitHub created with the repository, such as bug or enhancement)"},
	}, pagingFields...),
	Examples: []capability.Example{{Description: "List the labels", Arguments: json.RawMessage(`{}`)}},
}

var labelsGet = capability.Descriptor{
	ID:           Provider + ".labels.get",
	Version:      1,
	Title:        "Get a GitHub label",
	Description:  "Read one label of a repository a connection allows, matched by name exactly",
	Tags:         []string{"github", "labels", "get"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(`"name":`+labelNameSchema, "name"),
	OutputSchema: json.RawMessage(labelOutput),
	Arguments: []capability.Argument{
		{Name: "name", Description: "Label name, matched exactly", Required: true},
	},
	Fields: []capability.Field{
		{Name: "color", Description: "6-digit hex color, without #"},
		{Name: "description", Description: "Short description, untrusted data; absent when the label has none"},
		{Name: "default", Description: "True for a label GitHub created with the repository"},
	},
	Examples: []capability.Example{{Description: "Read one label", Arguments: json.RawMessage(`{"name":"bug"}`)}},
}

var labelsCreate = capability.Descriptor{
	ID:      Provider + ".labels.create",
	Version: 1,
	Title:   "Create a GitHub label",
	Description: "Create one new label in a repository a connection allows; name must not already " +
		"name a label of the repository, refused with a clear message otherwise; not idempotent, since a " +
		"repeated call with a new name creates a second label",
	Tags:     []string{"github", "labels", "create"},
	Risk:     changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider,
	InputSchema: inputSchema(`"name":`+labelNameSchema+`,"color":`+labelColorSchema+`,"description":`+
		labelDescriptionSchema, "name", "color"),
	OutputSchema: json.RawMessage(labelOutput),
	Arguments: []capability.Argument{
		{Name: "name", Description: "Label name, 1 to 50 characters, unique in the repository", Required: true},
		{Name: "color", Description: "6-digit hex color, without #", Required: true},
		{Name: "description", Description: "Short description, at most 100 characters"},
	},
	Examples: []capability.Example{{
		Description: "Create a label",
		Arguments:   json.RawMessage(`{"name":"bug","color":"d73a4a","description":"Something isn't working"}`),
	}},
}

var labelsUpdate = capability.Descriptor{
	ID:      Provider + ".labels.update",
	Version: 1,
	Title:   "Update a GitHub label",
	Description: "Change the name, color, or description of one label of a repository a connection " +
		"allows, matched by its current name exactly; at least one of new_name, color, or description is " +
		"required; idempotent, since sending the same values again leaves GitHub in the same state",
	Tags:     []string{"github", "labels", "update"},
	Risk:     changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider,
	InputSchema: inputSchema(`"name":`+labelNameSchema+`,"new_name":`+labelNameSchema+`,"color":`+
		labelColorSchema+`,"description":`+labelDescriptionSchema, "name"),
	OutputSchema: json.RawMessage(labelOutput),
	Arguments: []capability.Argument{
		{Name: "name", Description: "Current label name, matched exactly", Required: true},
		{Name: "new_name", Description: "New name for the label, 1 to 50 characters"},
		{Name: "color", Description: "New 6-digit hex color, without #"},
		{Name: "description", Description: "New short description, at most 100 characters; \"\" clears it"},
	},
	Examples: []capability.Example{{
		Description: "Recolor a label",
		Arguments:   json.RawMessage(`{"name":"bug","color":"b60205"}`),
	}},
}

const labelDeletedOutput = `{"type":"object","properties":{"deleted":{"type":"boolean"},"name":{"type":"string"}},` +
	`"required":["deleted","name"],"additionalProperties":false}`

var labelsDelete = capability.Descriptor{
	ID:      Provider + ".labels.delete",
	Version: 1,
	Title:   "Delete a GitHub label",
	Description: "Delete one label of a repository a connection allows permanently, removing it " +
		"from every issue and pull request that carries it; offered only where a connection's tools list " +
		"names it",
	Tags:                  []string{"github", "labels", "delete"},
	Risk:                  guardedRisk(capability.EffectDelete, capability.IdempotencyUnknown, labelSensitivity),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema:           inputSchema(`"name":`+labelNameSchema, "name"),
	OutputSchema:          json.RawMessage(labelDeletedOutput),
	Arguments: []capability.Argument{
		{Name: "name", Description: "Label name to delete, matched exactly", Required: true},
	},
	Fields: []capability.Field{
		{Name: "deleted", Description: "True once GitHub deleted the label"},
		{Name: "name", Description: "Label name that was deleted"},
	},
	Examples: []capability.Example{{
		Description: "Delete a label",
		Arguments:   json.RawMessage(`{"name":"wontfix"}`),
	}},
}

// labelsOperations binds every label tool to its handler.
func labelsOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: labelsList, Handler: labelsHandler(labelsList.ID, checkLabelsPagingOnly,
			func(ctx context.Context, c *Client, a *labelsArguments) (any, error) { return c.listLabels(ctx, a) })},
		{Descriptor: labelsGet, Handler: labelsHandler(labelsGet.ID, checkLabelName,
			func(ctx context.Context, c *Client, a *labelsArguments) (any, error) { return c.getLabel(ctx, a.Name) })},
		{Descriptor: labelsCreate, Handler: labelsHandler(labelsCreate.ID, checkLabelCreate,
			func(ctx context.Context, c *Client, a *labelsArguments) (any, error) { return c.createLabel(ctx, a) })},
		{Descriptor: labelsUpdate, Handler: labelsHandler(labelsUpdate.ID, checkLabelUpdate,
			func(ctx context.Context, c *Client, a *labelsArguments) (any, error) { return c.updateLabel(ctx, a) })},
		{Descriptor: labelsDelete, Handler: labelsHandler(labelsDelete.ID, checkLabelName,
			func(ctx context.Context, c *Client, a *labelsArguments) (any, error) {
				return c.deleteLabel(ctx, a.Name)
			})},
	}
}

// labelsArguments holds the arguments of every label tool; the input schema of each tool admits only its
// own. new_name, color, and description are pointers so github.labels.update tells a field left out from one
// sent as "" to clear it. page, perPage, and binding are derived by the checks.
type labelsArguments struct {
	Name        string  `json:"name"`
	NewName     *string `json:"new_name"`
	Color       *string `json:"color"`
	Description *string `json:"description"`
	Limit       int     `json:"limit"`
	Cursor      string  `json:"cursor"`

	page, perPage int
	binding       []byte
}

// labelsHandler decodes and checks the arguments and the repository before a credential is resolved, so a
// refused request never becomes a provider call, the way refsHandler does for the branch and tag tools.
func labelsHandler(id string, check func(*labelsArguments, target) error,
	call func(context.Context, *Client, *labelsArguments) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments labelsArguments
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, unreadable(id)
		}
		bound, err := selectTarget(resolved, kindRepository, raw)
		if err != nil {
			return nil, err
		}
		if err := check(&arguments, bound); err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(call(ctx, client, &arguments))
	}
}

func checkLabelsPagingOnly(a *labelsArguments, bound target) error {
	limit, err := normalizeLimit(a.Limit)
	if err != nil {
		return err
	}
	a.binding = fingerprint("labels", "list", bound.String())
	a.page, a.perPage, err = pageOf(a.binding, a.Cursor, limit)
	return err
}

func (a *labelsArguments) query() url.Values {
	return url.Values{"per_page": {strconv.Itoa(a.perPage)}, "page": {strconv.Itoa(a.page)}}
}

// stringOr reads an optional label argument as a plain string, "" when it was left out.
func stringOr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func checkLabelName(a *labelsArguments, _ target) error {
	return checkText("name", a.Name)
}

func checkLabelCreate(a *labelsArguments, _ target) error {
	if err := checkText("name", a.Name); err != nil {
		return err
	}
	return checkText("description", stringOr(a.Description))
}

func checkLabelUpdate(a *labelsArguments, _ target) error {
	if err := checkText("name", a.Name); err != nil {
		return err
	}
	if a.NewName == nil && a.Color == nil && a.Description == nil {
		return invalidRequest("give at least one of new_name, color, or description to change")
	}
	if a.NewName != nil {
		if err := checkText("new_name", *a.NewName); err != nil {
			return err
		}
	}
	if a.Description != nil {
		if err := checkText("description", *a.Description); err != nil {
			return err
		}
	}
	return nil
}

// Permission messages of the label tools. GitHub decides on every request; a message names what such a
// request needs without claiming what the configured token holds. GitHub documents a label as belonging to
// an issue or a pull request rather than to a scope of its own, so the fine-grained permission named here is
// stated as cautiously as the rest of this package states one it cannot verify against GitHub live.
const (
	labelsReadPermission = "GitHub refused this token the labels of this repository; reading them needs no " +
		"scope for a public repository, or repo on a classic token, or Issues: read on a fine-grained token, " +
		"for a private one"
	labelsChangePermission = "GitHub refused this change of a label; it needs repo on a classic token, or " +
		"Issues: read and write on a fine-grained token"
)

// labelPath is the REST route of one label of the bound repository, or of the label collection without a
// name. The name is escaped as one opaque path segment, the way tagRefPath escapes a tag name, so it can
// never address another route.
func (c *Client) labelPath(name string) string {
	if name == "" {
		return c.repoPath("labels")
	}
	return c.repoPath("labels/" + url.PathEscape(name))
}

// Label is the compact view of one label; its JSON tags match GitHub's own field names, so a label decodes
// directly without an intermediate shape.
type Label struct {
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description,omitempty"`
	Default     bool   `json:"default"`
}

// LabelList is one batch of labels.
type LabelList struct {
	Labels     []Label `json:"labels"`
	NextCursor string  `json:"next_cursor,omitempty"`
	HasMore    bool    `json:"has_more"`
}

func (c *Client) listLabels(ctx context.Context, a *labelsArguments) (*LabelList, error) {
	const op = "list labels"
	var raw []Label
	hasNext, err := c.restPage(ctx, op, c.labelPath(""), a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, labelsReadPermission)
	}
	result := &LabelList{Labels: make([]Label, 0, len(raw))}
	for _, label := range raw {
		if label.Name == "" {
			return nil, invalidEntry(op, "a label")
		}
		result.Labels = append(result.Labels, label)
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

func (c *Client) getLabel(ctx context.Context, name string) (*Label, error) {
	const op = "get label"
	var label Label
	if err := c.rest(ctx, op, c.labelPath(name), &label); err != nil {
		return nil, actionsFailure(err, labelsReadPermission)
	}
	if label.Name == "" {
		return nil, invalidEntry(op, "a label")
	}
	return &label, nil
}

// createLabel sends the create request directly, instead of through restChange, so a 422 answer can be read
// once and, when it names an existing label, turned into a clear refusal instead of the generic "rejected as
// invalid" message, the way createRepository and createRelease do for their own 422 answers. The request is
// still sent exactly once, and every other status is classified exactly like restChange would.
func (c *Client) createLabel(ctx context.Context, a *labelsArguments) (*Label, error) {
	const op = "create label"
	body := map[string]any{"name": a.Name, "color": stringOr(a.Color)}
	if a.Description != nil {
		body["description"] = *a.Description
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, provider.Waited(op, "GitHub", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoints.rest+c.labelPath(""),
		bytes.NewReader(payload))
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	c.authorize(req)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	defer c.limiter.HoldFor(mutationInterval)
	if err != nil {
		failure := provider.Transport(op, "GitHub", err)
		if failure.MayHaveArrived() {
			failure.Message += uncertain
		}
		return nil, failure
	}
	defer response.Body.Close()
	c.observeRateLimit(response.Header)
	if response.StatusCode < 200 || response.StatusCode > 299 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		if response.StatusCode == http.StatusUnprocessableEntity && bytes.Contains(data, []byte(`"already_exists"`)) {
			return nil, invalidRequest("GitHub already holds a label named " + a.Name +
				" in this repository; choose another name")
		}
		response.Body = io.NopCloser(bytes.NewReader(data))
		return nil, actionsFailure(c.statusError(op, response, true), labelsChangePermission)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "the GitHub response could not be read within the size limit" + uncertain}
	}
	var label Label
	if err := json.Unmarshal(data, &label); err != nil || label.Name == "" {
		return nil, invalidResponse(op, true)
	}
	return &label, nil
}

func (c *Client) updateLabel(ctx context.Context, a *labelsArguments) (*Label, error) {
	const op = "update label"
	body := map[string]any{}
	if a.NewName != nil {
		body["new_name"] = *a.NewName
	}
	if a.Color != nil {
		body["color"] = *a.Color
	}
	if a.Description != nil {
		body["description"] = *a.Description
	}
	var label Label
	if err := c.restChange(ctx, op, http.MethodPatch, c.labelPath(a.Name), body, &label); err != nil {
		return nil, actionsFailure(err, labelsChangePermission)
	}
	if label.Name == "" {
		return nil, invalidEntry(op, "a label")
	}
	return &label, nil
}

func (c *Client) deleteLabel(ctx context.Context, name string) (any, error) {
	const op = "delete label"
	if err := c.restChange(ctx, op, http.MethodDelete, c.labelPath(name), nil, nil); err != nil {
		return nil, actionsFailure(err, labelsChangePermission)
	}
	return map[string]any{"deleted": true, "name": name}, nil
}

// labelsSubject names the label a repository path below /labels addresses: labels for the collection, or
// labels/NAME for one, unescaped and reported only when it still names a usable label; it is empty for any
// other path, such as the repository route restSubject already names.
func labelsSubject(path string) string {
	if path == "labels" {
		return "the labels of this repository"
	}
	rest, ok := strings.CutPrefix(path, "labels/")
	if !ok {
		return ""
	}
	name, err := url.PathUnescape(rest)
	if err != nil || checkText("name", name) != nil || name == "" {
		return "a label"
	}
	return "label " + name
}
