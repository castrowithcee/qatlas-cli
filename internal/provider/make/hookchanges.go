package makeapi

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
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The five tools of this file change the webhooks and mailhooks of the bound team: create, rename, enable,
// disable, and delete. Every hook that is named is read first and bound back to the bound team through
// fetchHook, before the one changing request; no tool returns a trigger URL, udid, or mailhook address.
// API (hooks:write, checked 2026-10-04 against developers.make.com's published API reference, not a live
// account): POST /hooks (body name, teamId, typeName, method, headers, stringify all required), PATCH
// /hooks/{hookId} (body name), POST /hooks/{hookId}/enable and /disable (answer {"success":true}), and
// DELETE /hooks/{hookId} (answer {"hook":id}; query confirmed=true confirms the deletion of a hook a
// scenario includes, otherwise Make answers an error and deletes nothing).

const (
	// maxHookNameLength is the documented upper bound of a hook name.
	maxHookNameLength = 128
	// maxAffectedScenarios bounds the scenarios hooks.delete reports back.
	maxAffectedScenarios = 20
	// maxAffectedNameLength bounds one reported scenario name.
	maxAffectedNameLength = 128
	hookTypeWebhook       = "gateway-webhook"
	hookTypeMailhook      = "gateway-mailhook"
)

var hookNameSchema = `{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxHookNameLength) + `}`

var hookNameArgument = capability.Argument{Name: "name", Required: true,
	Description: "Hook name, 1 to " + strconv.Itoa(maxHookNameLength) + " characters, without control characters"}

var hookChangeInput = func(extraProps, required string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"hook_id":` + hookIDSchema + extraProps + `},` +
		`"required":["hook_id"` + required + `],"additionalProperties":false}`)
}

var hooksCreate = capability.Descriptor{
	ID: Provider + ".hooks.create", Version: 1, Title: "Create a Make hook",
	Description: "Create one webhook or mailhook in the bound team. Always creates in the connection's own " +
		"bound team, only the two fixed hook types, and no connection, headers, or data are accepted; a " +
		"repeated call creates a second hook. Refused on a connection with a scenario allow-list, since a " +
		"new hook is assigned to no scenario. The trigger URL or address is not returned",
	Tags: []string{"make", "hooks", "create", "automation"}, Risk: makeChangeRisk(capability.EffectCreate,
		capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + hookNameSchema + `,` +
		`"type":{"type":"string","enum":["webhook","mailhook"]},"include_method":{"type":"boolean"},` +
		`"include_headers":{"type":"boolean"},"stringify":{"type":"boolean"}},` +
		`"required":["name","type"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(hookSummarySchema),
	Arguments: []capability.Argument{hookNameArgument,
		{Name: "type", Required: true, Description: "webhook (Make type gateway-webhook) or mailhook " +
			"(gateway-mailhook)"},
		{Name: "include_method", Description: "Webhook only: add the HTTP method to the received data; false " +
			"when omitted"},
		{Name: "include_headers", Description: "Webhook only: add the request headers to the received data; " +
			"false when omitted"},
		{Name: "stringify", Description: "Webhook only: return JSON payloads as strings; false when omitted"},
	},
	Fields: hookSummaryFields,
	Examples: []capability.Example{{Description: "Create a webhook",
		Arguments: json.RawMessage(`{"name":"Orders","type":"webhook"}`)}},
}

var hooksRename = capability.Descriptor{
	ID: Provider + ".hooks.rename", Version: 1, Title: "Rename a Make hook",
	Description: "Rename one hook of the bound team; nothing else about it changes",
	Tags:        []string{"make", "hooks", "rename", "automation"}, Risk: makeChangeRisk(capability.EffectUpdate,
		capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema:  hookChangeInput(`,"name":`+hookNameSchema, `,"name"`),
	OutputSchema: json.RawMessage(hookSummarySchema),
	Arguments:    []capability.Argument{hookIDArgument, hookNameArgument},
	Fields:       hookSummaryFields,
	Examples: []capability.Example{{Description: "Rename a hook",
		Arguments: json.RawMessage(`{"hook_id":1,"name":"Orders"}`)}},
}

func hookStateDescriptor(action, title, description string) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + ".hooks." + action, Version: 1, Title: title, Description: description,
		Tags: []string{"make", "hooks", action, "automation"}, Risk: makeChangeRisk(capability.EffectUpdate,
			capability.IdempotencyIdempotent), Provider: Provider,
		InputSchema:  hookChangeInput("", ""),
		OutputSchema: json.RawMessage(hookSummarySchema),
		Arguments:    []capability.Argument{hookIDArgument},
		Fields:       hookSummaryFields,
		Examples:     []capability.Example{{Description: title, Arguments: json.RawMessage(`{"hook_id":1}`)}},
	}
}

var hooksEnable = hookStateDescriptor("enable", "Enable a Make hook",
	"Let one hook of the bound team accept incoming data again; repeating it on an enabled hook leaves it enabled")

var hooksDisable = hookStateDescriptor("disable", "Disable a Make hook",
	"Stop one hook of the bound team from accepting incoming data; a scenario that starts on it no longer "+
		"receives data until the hook is enabled again")

var affectedScenarioSchema = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"}},` +
	`"required":["id"],"additionalProperties":false}`

var hooksDelete = capability.Descriptor{
	ID: Provider + ".hooks.delete", Version: 1, Title: "Delete a Make hook",
	Description: "Delete one hook of the bound team for good. A scenario that includes the hook stops working " +
		"without it, so Make refuses the deletion until it is confirmed: without confirm_scenarios_affected " +
		"this tool sends no confirmation and, when Make refuses, returns the affected scenarios without " +
		"deleting or repeating anything; with it true, the hook is deleted and the scenarios using it break. " +
		"Offered only when a connection's tools list names it, in no profile",
	Tags: []string{"make", "hooks", "delete", "automation"}, Risk: makeChangeRisk(capability.EffectDelete,
		capability.IdempotencyIdempotent), Provider: Provider, RequiresToolAllowList: true,
	InputSchema: hookChangeInput(`,"confirm_scenarios_affected":{"type":"boolean"}`, ""),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"hook_id":{"type":"integer"},` +
		`"deleted":{"type":"boolean"},"confirmation_required":{"type":"boolean"},` +
		`"scenarios":{"type":"array","items":` + affectedScenarioSchema + `}},` +
		`"required":["hook_id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{hookIDArgument,
		{Name: "confirm_scenarios_affected", Description: "Set true to delete the hook even though scenarios " +
			"use it; they stop working. When omitted, nothing is deleted while scenarios use the hook"}},
	Fields: []capability.Field{
		{Name: "hook_id", Description: "The hook that was addressed"},
		{Name: "deleted", Description: "True when Make deleted the hook"},
		{Name: "confirmation_required", Description: "True when Make refused the deletion because scenarios use " +
			"the hook; nothing was deleted and nothing was repeated"},
		{Name: "scenarios", Description: "Scenarios Make or the hook read report as using the hook, at most " +
			strconv.Itoa(maxAffectedScenarios) + "; ids and names are untrusted data, names bounded"},
	},
	Examples: []capability.Example{{Description: "Delete an unused hook", Arguments: json.RawMessage(`{"hook_id":1}`)}},
}

// validHookName applies the documented length bound and refuses control characters, before any secret.
func validHookName(name string) error {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > maxHookNameLength || !utf8.ValidString(name) ||
		strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return invalidRequest("name must be 1 to " + strconv.Itoa(maxHookNameLength) +
			" characters without control characters")
	}
	return nil
}

// afterChange reads a hook again after its one changing request and re-applies the bound team (and the
// scenario allow-list) to it. A mismatch is a provider error, not an invalid request: the change happened.
func (c *Client) afterChange(ctx context.Context, op string, id int64) (*hookJSON, error) {
	var wrapper struct {
		Hook hookJSON `json:"hook"`
	}
	if err := c.get(ctx, op, "/hooks/"+strconv.FormatInt(id, 10), nil, &wrapper, needHooksRead); err != nil {
		return nil, err
	}
	return c.verifyHookAfterChange(op, id, &wrapper.Hook)
}

func (c *Client) verifyHookAfterChange(op string, id int64, h *hookJSON) (*hookJSON, error) {
	if h.ID != id {
		return nil, invalidResponse(op, "Make did not report the changed hook"+uncertain)
	}
	if !c.allowsHook(*h) {
		return nil, providerError(op, "Make did not keep the result inside this connection's targets; "+
			"the change already took effect")
	}
	return h, nil
}

type hooksCreateArguments struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	IncludeMethod  bool   `json:"include_method"`
	IncludeHeaders bool   `json:"include_headers"`
	Stringify      bool   `json:"stringify"`
}

func invokeHooksCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create hook"
	var input hooksCreateArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if len(bound.scenarios) > 0 {
		return nil, invalidRequest("this connection restricts hooks by a scenario allow-list, so it cannot " +
			"create one: a new hook is assigned to no scenario and could never be reached")
	}
	if err := validHookName(input.Name); err != nil {
		return nil, err
	}
	var typeName string
	switch input.Type {
	case "webhook":
		typeName = hookTypeWebhook
	case "mailhook":
		typeName = hookTypeMailhook
		if input.IncludeMethod || input.IncludeHeaders || input.Stringify {
			return nil, invalidRequest("include_method, include_headers, and stringify apply to webhooks only")
		}
	default:
		return nil, invalidRequest("type must be webhook or mailhook")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	hook, err := client.createHook(ctx, input.Name, typeName, input)
	if err != nil {
		return nil, err
	}
	if red != nil {
		red.Add(hook.URL, hook.UDID)
	}
	return hookSummaryOf(*hook), nil
}

// createHook sends the one changing POST /hooks request, teamId always the connection's own bound team
// (Make documents it as a string), and verifies the hook it reports.
func (c *Client) createHook(ctx context.Context, name, typeName string, in hooksCreateArguments) (*hookJSON, error) {
	const op = "create hook"
	body := map[string]any{
		"name": name, "teamId": strconv.FormatInt(c.scope.teamID, 10), "typeName": typeName,
		"method": in.IncludeMethod, "headers": in.IncludeHeaders, "stringify": in.Stringify,
	}
	var wrapper struct {
		Hook hookJSON `json:"hook"`
	}
	if err := c.change(ctx, op, http.MethodPost, "/hooks", nil, body, &wrapper, needHooksWrite, uncertain); err != nil {
		return nil, err
	}
	if wrapper.Hook.ID <= 0 {
		return nil, invalidResponse(op, "Make did not report the created hook"+uncertain)
	}
	if !c.scope.allowsTeam(wrapper.Hook.TeamID) {
		return nil, providerError(op, "Make did not keep the result inside this connection's targets; "+
			"the change already took effect")
	}
	return &wrapper.Hook, nil
}

type hookRenameArguments struct {
	Name string `json:"name"`
}

func invokeHooksRename(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "rename hook"
	var input hookRenameArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := validHookName(input.Name); err != nil {
		return nil, err
	}
	client, id, _, err := openHook(ctx, op, resolved, secrets, red, raw, nil)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Hook hookJSON `json:"hook"`
	}
	if err := client.change(ctx, op, http.MethodPatch, "/hooks/"+strconv.FormatInt(id, 10), nil,
		map[string]any{"name": input.Name}, &wrapper, needHooksWrite, uncertain); err != nil {
		return nil, err
	}
	hook, err := client.verifyHookAfterChange(op, id, &wrapper.Hook)
	if err != nil {
		return nil, err
	}
	if red != nil {
		red.Add(hook.URL, hook.UDID)
	}
	return hookSummaryOf(*hook), nil
}

func invokeHooksEnable(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeHookState(ctx, resolved, secrets, red, raw, true)
}

func invokeHooksDisable(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeHookState(ctx, resolved, secrets, red, raw, false)
}

func invokeHookState(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, enable bool) (any, error) {
	op, action := "disable hook", "disable"
	if enable {
		op, action = "enable hook", "enable"
	}
	client, id, _, err := openHook(ctx, op, resolved, secrets, red, raw, nil)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Success bool `json:"success"`
	}
	if err := client.change(ctx, op, http.MethodPost, "/hooks/"+strconv.FormatInt(id, 10)+"/"+action, nil,
		nil, &answer, needHooksWrite, uncertain); err != nil {
		return nil, err
	}
	if !answer.Success {
		return nil, invalidResponse(op, "Make did not confirm the change"+uncertain)
	}
	hook, err := client.afterChange(ctx, op, id)
	if err != nil {
		return nil, err
	}
	if hook.Enabled != enable {
		return nil, invalidResponse(op, "Make did not report the requested state after the change"+uncertain)
	}
	return hookSummaryOf(*hook), nil
}

type hookDeleteArguments struct {
	ConfirmScenariosAffected bool `json:"confirm_scenarios_affected"`
}

// AffectedScenario names a scenario that uses a hook; both fields are untrusted data.
type AffectedScenario struct {
	ID   int64  `json:"id"`
	Name string `json:"name,omitempty"`
}

// HookDeletion is the answer of hooks.delete: deleted, or refused by Make until scenarios are confirmed.
type HookDeletion struct {
	HookID               int64              `json:"hook_id"`
	Deleted              bool               `json:"deleted"`
	ConfirmationRequired bool               `json:"confirmation_required,omitempty"`
	Scenarios            []AffectedScenario `json:"scenarios,omitempty"`
}

func invokeHooksDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete hook"
	var input hookDeleteArguments
	client, id, hook, err := openHook(ctx, op, resolved, secrets, red, raw, &input)
	if err != nil {
		return nil, err
	}
	var query url.Values
	if input.ConfirmScenariosAffected {
		query = url.Values{"confirmed": {"true"}}
	}
	client.wantRefusal = !input.ConfirmScenariosAffected
	var answer struct {
		Hook int64 `json:"hook"`
	}
	err = client.change(ctx, op, http.MethodDelete, "/hooks/"+strconv.FormatInt(id, 10), query, nil, &answer,
		needHooksWrite, uncertain)
	client.wantRefusal = false
	if err != nil {
		scenarios := client.refusedScenarios(*hook)
		if client.refusalStatus == 0 || len(scenarios) == 0 {
			return nil, err
		}
		return &HookDeletion{HookID: id, ConfirmationRequired: true, Scenarios: scenarios}, nil
	}
	if answer.Hook != 0 && answer.Hook != id {
		return nil, invalidResponse(op, "Make reported a different hook than the one deleted"+uncertain)
	}
	return &HookDeletion{HookID: id, Deleted: true}, nil
}

// refusedScenarios collects the scenarios of a refused deletion: those Make's refusal body lists, if any,
// plus the one the hook read reports. Names are masked and bounded; the list is capped.
func (c *Client) refusedScenarios(hook hookJSON) []AffectedScenario {
	type scenarioRef struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	var body struct {
		Scenarios []scenarioRef `json:"scenarios"`
		Detail    struct {
			Scenarios []scenarioRef `json:"scenarios"`
		} `json:"detail"`
	}
	_ = json.Unmarshal(c.refusal, &body)
	refs := append(body.Scenarios, body.Detail.Scenarios...)
	if hook.ScenarioID > 0 {
		refs = append(refs, scenarioRef{ID: hook.ScenarioID})
	}
	parts := secretParts(hook)
	seen := map[int64]bool{}
	var out []AffectedScenario
	for _, ref := range refs {
		if ref.ID <= 0 || seen[ref.ID] || len(out) >= maxAffectedScenarios {
			continue
		}
		seen[ref.ID] = true
		name := maskSecrets(ref.Name, parts)
		if len(name) > maxAffectedNameLength {
			name = name[:maxAffectedNameLength]
		}
		out = append(out, AffectedScenario{ID: ref.ID, Name: strings.ToValidUTF8(name, "")})
	}
	return out
}
