package makeapi

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The five tools of this file read the webhooks and mailhooks of the bound team. A hook's trigger URL (or a
// mailhook's address) is a trigger secret: whoever knows it can start the hook's scenario. Only hooks.url
// ever returns it; list, get, ping, and logs omit it and mask every part of it in the strings they do
// return. Hook logs are reduced to metadata, never to the request headers or bodies they may hold.
// API: GET /hooks (teamId required; typeName, assigned, pg[...]), GET /hooks/{hookId}, GET
// /hooks/{hookId}/ping, and GET /hooks/{hookId}/logs (from, to, pg[...]), all hooks:read, checked 2026-10-04
// against developers.make.com's published API reference, not a live account.

const (
	// maxHookLogSizes bounds the entries of one log's sizes object.
	maxHookLogSizes = 8
	// maxHookSizeKeyLength bounds one key of a log's sizes object.
	maxHookSizeKeyLength = 32
	// maxTriggerURLLength bounds the trigger URL hooks.url returns.
	maxTriggerURLLength = 1024
	// minMaskLength is the shortest trigger-secret part that is masked inside other strings.
	minMaskLength  = 6
	redactedMarker = "[redacted]"
)

var hookIDSchema = idSchema

var hookIDArgument = capability.Argument{Name: "hook_id", Required: true,
	Description: "Make webhook or mailhook identifier; its team membership is always re-checked live " +
		"against Make's own report, and a connection with a scenario allow-list reaches only hooks " +
		"assigned to a scenario on that list"}

var hookSummarySchema = `{"type":"object","properties":{` +
	`"id":{"type":"integer"},"name":{"type":"string"},"team_id":{"type":"integer"},` +
	`"type":{"type":"string"},"type_name":{"type":"string"},"package_name":{"type":"string"},` +
	`"enabled":{"type":"boolean"},"gone":{"type":"boolean"},"queue_count":{"type":"integer"},` +
	`"queue_limit":{"type":"integer"},"scenario_id":{"type":"integer"}},` +
	`"required":["id","name","team_id","enabled","gone"],"additionalProperties":false}`

var hookSummaryFields = []capability.Field{
	{Name: "id", Description: "Hook identifier, used as hook_id by the other hook tools"},
	{Name: "name", Description: "Hook name, untrusted data; any part of the trigger URL is masked"},
	{Name: "team_id", Description: "Team this hook belongs to; always the connection's bound team"},
	{Name: "type", Description: "Hook type as Make reports it, for example web or email"},
	{Name: "type_name", Description: "Hook type name as Make reports it"},
	{Name: "package_name", Description: "App package the hook belongs to, when Make reports one"},
	{Name: "enabled", Description: "True when the hook accepts incoming data"},
	{Name: "gone", Description: "True when Make reports the hook as gone"},
	{Name: "queue_count", Description: "Items waiting in the hook's queue"},
	{Name: "queue_limit", Description: "Capacity of the hook's queue"},
	{Name: "scenario_id", Description: "Scenario the hook is assigned to, when Make reports one"},
}

var hooksList = capability.Descriptor{
	ID: Provider + ".hooks.list", Version: 1, Title: "List Make hooks",
	Description: "List the webhooks and mailhooks of the bound team, page by page with a numeric offset; the " +
		"trigger URL is never part of the list, see make.hooks.url",
	Tags: []string{"make", "hooks", "list", "automation"}, Risk: makeReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"offset":{"type":"integer","minimum":0},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxListLimit) + `},` +
		`"type_name":{"type":"string","pattern":"^[A-Za-z0-9_.-]{1,64}$"},` +
		`"assigned":{"type":"boolean"}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"hooks":{"type":"array","items":` + hookSummarySchema + `},` +
		`"offset":{"type":"integer"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["hooks","offset","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "offset", Description: "Hooks to skip before this page; 0 when omitted"},
		{Name: "limit", Description: "Hooks per page, 1 to " + strconv.Itoa(maxListLimit) + "; " +
			strconv.Itoa(defaultListLimit) + " when omitted"},
		{Name: "type_name", Description: "When set, list only hooks of this Make hook type name"},
		{Name: "assigned", Description: "When set, list only hooks that are (true) or are not (false) " +
			"assigned to a scenario"},
	},
	Fields: append(append([]capability.Field{}, hookSummaryFields...),
		capability.Field{Name: "offset", Description: "Offset of this page, for computing the next call's offset"},
		capability.Field{Name: "has_more", Description: "True when a further page likely remains; Make " +
			"reports no total count, so this is true whenever this page was full"},
		capability.Field{Name: "count", Description: "Number of hooks on this page after the team and " +
			"scenario boundary was re-applied"},
	),
	Examples: []capability.Example{{Description: "List the first page of hooks", Arguments: json.RawMessage(`{}`)}},
}

var hooksGet = capability.Descriptor{
	ID: Provider + ".hooks.get", Version: 1, Title: "Get a Make hook",
	Description: "Read one webhook or mailhook of the bound team: its state and queue, never its trigger URL",
	Tags:        []string{"make", "hooks", "get", "automation"}, Risk: makeReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"hook_id":` + hookIDSchema + `},` +
		`"required":["hook_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(hookSummarySchema),
	Arguments:    []capability.Argument{hookIDArgument},
	Fields:       hookSummaryFields,
	Examples:     []capability.Example{{Description: "Read one hook", Arguments: json.RawMessage(`{"hook_id":1}`)}},
}

var hooksPing = capability.Descriptor{
	ID: Provider + ".hooks.ping", Version: 1, Title: "Ping a Make hook",
	Description: "Read a hook's live status (attached to a scenario, learning, gone) through Make's ping " +
		"endpoint, which only reports state and sends no data to the hook; the address it reports is never returned",
	Tags: []string{"make", "hooks", "ping", "automation"}, Risk: makeReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"hook_id":` + hookIDSchema + `},` +
		`"required":["hook_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},` +
		`"team_id":{"type":"integer"},"name":{"type":"string"},"attached":{"type":"boolean"},` +
		`"learning":{"type":"boolean"},"gone":{"type":"boolean"}},` +
		`"required":["id","team_id","attached","learning","gone"],"additionalProperties":false}`),
	Arguments: []capability.Argument{hookIDArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Hook identifier"},
		{Name: "team_id", Description: "Team of the hook; always the connection's bound team"},
		{Name: "name", Description: "Hook name as the ping reports it, untrusted data, trigger URL masked"},
		{Name: "attached", Description: "True when a scenario is attached to the hook"},
		{Name: "learning", Description: "True when the hook is learning its data structure"},
		{Name: "gone", Description: "True when Make reports the hook as gone"},
	},
	Examples: []capability.Example{{Description: "Ping one hook", Arguments: json.RawMessage(`{"hook_id":1}`)}},
}

var hookLogSchema = `{"type":"object","properties":{"id":{"type":"integer"},` +
	`"status_id":{"type":"integer"},"logged_at":{"type":"string"},"replayable":{"type":"boolean"},` +
	`"type_id":{"type":"integer"},"sizes":{"type":"object","additionalProperties":{"type":"integer"}}},` +
	`"required":["id","status_id"],"additionalProperties":false}`

var hooksLogs = capability.Descriptor{
	ID: Provider + ".hooks.logs", Version: 1, Title: "List Make hook logs",
	Description: "List a hook's incoming-data log entries as metadata only (time, status, sizes); never the " +
		"request headers or bodies, the parser details, or anything that could hold the trigger URL",
	Tags: []string{"make", "hooks", "logs", "automation"}, Risk: makeReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"hook_id":` + hookIDSchema + `,` +
		`"offset":{"type":"integer","minimum":0},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxListLimit) + `},` +
		`"from_ms":{"type":"integer","minimum":0},"to_ms":{"type":"integer","minimum":0}},` +
		`"required":["hook_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"hook_id":{"type":"integer"},` +
		`"logs":{"type":"array","items":` + hookLogSchema + `},"offset":{"type":"integer"},` +
		`"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["hook_id","logs","offset","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{hookIDArgument,
		{Name: "offset", Description: "Entries to skip before this page; 0 when omitted"},
		{Name: "limit", Description: "Entries per page, 1 to " + strconv.Itoa(maxListLimit) + "; " +
			strconv.Itoa(defaultListLimit) + " when omitted"},
		{Name: "from_ms", Description: "When set, only entries at or after this time, Unix epoch milliseconds"},
		{Name: "to_ms", Description: "When set, only entries at or before this time, Unix epoch milliseconds"},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Log entry identifier"},
		{Name: "status_id", Description: "1 success, 3 failed, as Make reports it"},
		{Name: "logged_at", Description: "When the entry was logged, as Make reports it"},
		{Name: "replayable", Description: "True when Make could replay the execution; this provider never does"},
		{Name: "type_id", Description: "Entry type as Make reports it"},
		{Name: "sizes", Description: "Payload sizes in bytes as Make reports them, at most " +
			strconv.Itoa(maxHookLogSizes) + " entries"},
		{Name: "hook_id", Description: "Hook the entries belong to"},
		{Name: "offset", Description: "Offset of this page, for computing the next call's offset"},
		{Name: "has_more", Description: "True when a further page likely remains (this page was full)"},
		{Name: "count", Description: "Number of entries on this page"},
	},
	Examples: []capability.Example{{Description: "List one hook's recent logs", Arguments: json.RawMessage(`{"hook_id":1}`)}},
}

var hooksURL = capability.Descriptor{
	ID: Provider + ".hooks.url", Version: 1, Title: "Get a Make hook's trigger URL",
	Description: "Return the trigger URL (or mailhook address) of one hook of the bound team. It is a secret: " +
		"anyone holding it can start the hook's scenario, so treat it like a credential. No other hook tool " +
		"returns it; this tool is offered only when a connection's tools list names it",
	Tags: []string{"make", "hooks", "url", "automation"}, Risk: makeReadRisk, Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"hook_id":` + hookIDSchema + `},` +
		`"required":["hook_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},` +
		`"team_id":{"type":"integer"},"url":{"type":"string"}},` +
		`"required":["id","team_id","url"],"additionalProperties":false}`),
	Arguments: []capability.Argument{hookIDArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Hook identifier"},
		{Name: "team_id", Description: "Team of the hook; always the connection's bound team"},
		{Name: "url", Description: "Trigger URL or mailhook address, a secret"},
	},
	Examples: []capability.Example{{Description: "Read one hook's trigger URL", Arguments: json.RawMessage(`{"hook_id":1}`)}},
}

// hookJSON mirrors the subset of Make's hook object this provider reads
// (developers.make.com/api-documentation/api-reference/hooks). udid and url are read only to be masked, and
// url only to be returned by hooks.url.
type hookJSON struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	TeamID      int64  `json:"teamId"`
	UDID        string `json:"udid"`
	Type        string `json:"type"`
	TypeName    string `json:"typeName"`
	PackageName string `json:"packageName"`
	Enabled     bool   `json:"enabled"`
	Gone        bool   `json:"gone"`
	QueueCount  int64  `json:"queueCount"`
	QueueLimit  int64  `json:"queueLimit"`
	ScenarioID  int64  `json:"scenarioId"`
	URL         string `json:"url"`
}

// HookSummary is the stable, scope-checked view of one hook. It never carries the trigger URL or its udid.
type HookSummary struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	TeamID      int64  `json:"team_id"`
	Type        string `json:"type,omitempty"`
	TypeName    string `json:"type_name,omitempty"`
	PackageName string `json:"package_name,omitempty"`
	Enabled     bool   `json:"enabled"`
	Gone        bool   `json:"gone"`
	QueueCount  int64  `json:"queue_count,omitempty"`
	QueueLimit  int64  `json:"queue_limit,omitempty"`
	ScenarioID  int64  `json:"scenario_id,omitempty"`
}

// secretParts lists the strings that identify a hook's trigger: the URL or address, the udid, the last path
// segment of a URL, and the local part of a mailhook address.
func secretParts(h hookJSON, extra ...string) []string {
	var parts []string
	for _, value := range append([]string{h.URL, h.UDID}, extra...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		parts = append(parts, value)
		if at := strings.Index(value, "@"); at > 0 {
			parts = append(parts, value[:at])
		}
		trimmed := strings.TrimRight(value, "/")
		if slash := strings.LastIndex(trimmed, "/"); slash >= 0 {
			parts = append(parts, trimmed[slash+1:])
		}
	}
	return parts
}

// maskSecrets replaces every trigger-secret part in value, longest first, then bounds it.
func maskSecrets(value string, parts []string) string {
	for pass := 0; pass < 2; pass++ {
		for _, part := range parts {
			if len(part) >= minMaskLength {
				value = strings.ReplaceAll(value, part, redactedMarker)
			}
		}
	}
	return bounded(value)
}

func hookSummaryOf(h hookJSON) HookSummary {
	parts := secretParts(h)
	return HookSummary{
		ID: h.ID, Name: maskSecrets(h.Name, parts), TeamID: h.TeamID, Type: maskSecrets(h.Type, parts),
		TypeName: maskSecrets(h.TypeName, parts), PackageName: maskSecrets(h.PackageName, parts),
		Enabled: h.Enabled, Gone: h.Gone, QueueCount: h.QueueCount, QueueLimit: h.QueueLimit,
		ScenarioID: h.ScenarioID,
	}
}

// allowsHook applies the bound team and, narrower than the scenario tools, the scenario allow-list: with a
// list configured, only a hook assigned to a listed scenario is reachable, never an unassigned one.
func (c *Client) allowsHook(h hookJSON) bool {
	if !c.scope.allowsTeam(h.TeamID) {
		return false
	}
	return len(c.scope.scenarios) == 0 || (h.ScenarioID != 0 && c.scope.allowsScenario(h.ScenarioID))
}

type hooksListArguments struct {
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
	TypeName string `json:"type_name"`
	Assigned *bool  `json:"assigned"`
}

// HooksPage is one offset-paginated, scope-filtered listing of hooks.
type HooksPage struct {
	Hooks   []HookSummary `json:"hooks"`
	Offset  int           `json:"offset"`
	HasMore bool          `json:"has_more"`
	Count   int           `json:"count"`
}

func invokeHooksList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input hooksListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list hooks", "the validated arguments could not be read")
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListHooks(ctx, input.Offset, limit, input.TypeName, input.Assigned)
}

// ListHooks reads one page of hooks of the bound team, always sending the bound team as teamId, and
// re-applies the team and scenario boundary to the answer.
func (c *Client) ListHooks(ctx context.Context, offset, limit int, typeName string, assigned *bool) (*HooksPage, error) {
	const op = "list hooks"
	query := url.Values{
		"teamId":     {strconv.FormatInt(c.scope.teamID, 10)},
		"pg[offset]": {strconv.Itoa(offset)},
		"pg[limit]":  {strconv.Itoa(limit)},
	}
	if typeName != "" {
		query.Set("typeName", typeName)
	}
	if assigned != nil {
		query.Set("assigned", strconv.FormatBool(*assigned))
	}
	var page struct {
		Hooks []hookJSON `json:"hooks"`
	}
	if err := c.get(ctx, op, "/hooks", query, &page, needHooksRead); err != nil {
		return nil, err
	}
	hooks := make([]HookSummary, 0, len(page.Hooks))
	for _, h := range page.Hooks {
		if c.allowsHook(h) {
			hooks = append(hooks, hookSummaryOf(h))
		}
	}
	return &HooksPage{Hooks: hooks, Offset: offset, HasMore: len(page.Hooks) == limit, Count: len(hooks)}, nil
}

type hookArguments struct {
	HookID int64 `json:"hook_id"`
}

// fetchHook reads one hook and binds it back to this connection: a hook of another team, or outside a
// scenario allow-list, is refused without naming whatever it belongs to.
func (c *Client) fetchHook(ctx context.Context, op string, hookID int64) (*hookJSON, error) {
	if hookID <= 0 {
		return nil, invalidRequest("hook_id must be a positive integer")
	}
	var wrapper struct {
		Hook hookJSON `json:"hook"`
	}
	path := "/hooks/" + strconv.FormatInt(hookID, 10)
	if err := c.get(ctx, op, path, nil, &wrapper, needHooksRead); err != nil {
		return nil, err
	}
	if wrapper.Hook.ID != hookID || !c.allowsHook(wrapper.Hook) {
		return nil, invalidRequest("hook_id is outside the targets of this connection")
	}
	return &wrapper.Hook, nil
}

// openHook parses the arguments, validates the id locally, opens the client, and binds the hook.
func openHook(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, into any) (*Client, int64, *hookJSON, error) {
	var base hookArguments
	if err := json.Unmarshal(raw, &base); err != nil {
		return nil, 0, nil, providerError(op, "the validated arguments could not be read")
	}
	if into != nil {
		if err := json.Unmarshal(raw, into); err != nil {
			return nil, 0, nil, providerError(op, "the validated arguments could not be read")
		}
	}
	if _, err := boundScope(resolved); err != nil {
		return nil, 0, nil, err
	}
	if base.HookID <= 0 {
		return nil, 0, nil, invalidRequest("hook_id must be a positive integer")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, 0, nil, err
	}
	hook, err := client.fetchHook(ctx, op, base.HookID)
	if err != nil {
		return nil, 0, nil, err
	}
	return client, base.HookID, hook, nil
}

func invokeHooksGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	_, _, hook, err := openHook(ctx, "get hook", resolved, secrets, red, raw, nil)
	if err != nil {
		return nil, err
	}
	return hookSummaryOf(*hook), nil
}

// HookPing is the masked live status of one hook; the address Make reports is never part of it.
type HookPing struct {
	ID       int64  `json:"id"`
	TeamID   int64  `json:"team_id"`
	Name     string `json:"name,omitempty"`
	Attached bool   `json:"attached"`
	Learning bool   `json:"learning"`
	Gone     bool   `json:"gone"`
}

func invokeHooksPing(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "ping hook"
	client, id, hook, err := openHook(ctx, op, resolved, secrets, red, raw, nil)
	if err != nil {
		return nil, err
	}
	var answer struct {
		URL      string `json:"url"`
		Name     string `json:"name"`
		Address  string `json:"address"`
		TeamID   int64  `json:"teamId"`
		Attached bool   `json:"attached"`
		Learning bool   `json:"learning"`
		Gone     bool   `json:"gone"`
	}
	if err := client.get(ctx, op, "/hooks/"+strconv.FormatInt(id, 10)+"/ping", nil, &answer, needHooksRead); err != nil {
		return nil, err
	}
	if answer.TeamID != 0 && !client.scope.allowsTeam(answer.TeamID) {
		return nil, invalidRequest("hook_id is outside the targets of this connection")
	}
	parts := secretParts(*hook, answer.URL, answer.Address)
	return &HookPing{ID: id, TeamID: hook.TeamID, Name: maskSecrets(answer.Name, parts),
		Attached: answer.Attached, Learning: answer.Learning, Gone: answer.Gone || hook.Gone}, nil
}

type hookLogsArguments struct {
	HookID int64 `json:"hook_id"`
	Offset int   `json:"offset"`
	Limit  int   `json:"limit"`
	FromMS int64 `json:"from_ms"`
	ToMS   int64 `json:"to_ms"`
}

// HookLog is the metadata of one hook log entry; headers, bodies, parsers, and udids are never read.
type HookLog struct {
	ID         int64            `json:"id"`
	StatusID   int              `json:"status_id"`
	LoggedAt   string           `json:"logged_at,omitempty"`
	Replayable bool             `json:"replayable,omitempty"`
	TypeID     int64            `json:"type_id,omitempty"`
	Sizes      map[string]int64 `json:"sizes,omitempty"`
}

// HookLogsPage is one offset-paginated listing of a hook's log metadata.
type HookLogsPage struct {
	HookID  int64     `json:"hook_id"`
	Logs    []HookLog `json:"logs"`
	Offset  int       `json:"offset"`
	HasMore bool      `json:"has_more"`
	Count   int       `json:"count"`
}

func invokeHooksLogs(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list hook logs"
	var input hookLogsArguments
	client, id, _, err := openHook(ctx, op, resolved, secrets, red, raw, &input)
	if err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	query := url.Values{"pg[offset]": {strconv.Itoa(input.Offset)}, "pg[limit]": {strconv.Itoa(limit)}}
	if input.FromMS > 0 {
		query.Set("from", strconv.FormatInt(input.FromMS, 10))
	}
	if input.ToMS > 0 {
		query.Set("to", strconv.FormatInt(input.ToMS, 10))
	}
	var page struct {
		HookLogs []struct {
			ID         int64                      `json:"id"`
			StatusID   int                        `json:"statusId"`
			LoggedAt   string                     `json:"loggedAt"`
			Replayable bool                       `json:"replayable"`
			TypeID     int64                      `json:"typeId"`
			Sizes      map[string]json.RawMessage `json:"sizes"`
		} `json:"hookLogs"`
	}
	if err := client.get(ctx, op, "/hooks/"+strconv.FormatInt(id, 10)+"/logs", query, &page, needHooksRead); err != nil {
		return nil, err
	}
	logs := make([]HookLog, 0, len(page.HookLogs))
	for _, entry := range page.HookLogs {
		logs = append(logs, HookLog{ID: entry.ID, StatusID: entry.StatusID, LoggedAt: bounded(entry.LoggedAt),
			Replayable: entry.Replayable, TypeID: entry.TypeID, Sizes: hookSizes(entry.Sizes)})
	}
	return &HookLogsPage{HookID: id, Logs: logs, Offset: input.Offset, HasMore: len(page.HookLogs) == limit,
		Count: len(logs)}, nil
}

// hookSizes keeps only short-keyed integer entries of a log's sizes object, at most maxHookLogSizes.
func hookSizes(raw map[string]json.RawMessage) map[string]int64 {
	out := map[string]int64{}
	for key, value := range raw {
		var size int64
		if len(out) >= maxHookLogSizes || len(key) > maxHookSizeKeyLength || json.Unmarshal(value, &size) != nil {
			continue
		}
		out[key] = size
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// HookURL is the one result that carries a trigger secret.
type HookURL struct {
	ID     int64  `json:"id"`
	TeamID int64  `json:"team_id"`
	URL    string `json:"url"`
}

// invokeHooksURL returns the trigger URL. The value is deliberately not registered with the redactor: that
// would blank this tool's own answer, and nothing else of this provider ever holds it.
func invokeHooksURL(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	_, id, hook, err := openHook(ctx, "get hook url", resolved, secrets, red, raw, nil)
	if err != nil {
		return nil, err
	}
	value := strings.TrimSpace(hook.URL)
	if value == "" || len(value) > maxTriggerURLLength || strings.ContainsAny(value, " \t\r\n\x00") {
		return nil, providerError("get hook url", "Make reports no usable trigger URL for this hook")
	}
	return &HookURL{ID: id, TeamID: hook.TeamID, URL: value}, nil
}
