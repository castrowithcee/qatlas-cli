package makeapi

import (
	"context"
	"encoding/json"
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

// maxExecutionIDLength bounds a run's own identifier (Make's executionId), a plain path segment this
// provider never interprets further.
const maxExecutionIDLength = 128

// executionIDSchema is the JSON Schema of one run identifier: an opaque path value, never a free-form string
// that could be mistaken for a path or a URL.
var executionIDSchema = `{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxExecutionIDLength) +
	`,"pattern":"^[A-Za-z0-9_-]{1,` + strconv.Itoa(maxExecutionIDLength) + `}$"}`

// statusValues mirrors Make's own numeric run status (developers.make.com's Logs reference): 1 success, 2
// warning, 3 error. Make reports it as an integer, not a string, and this provider follows that.
var statusValues = []int{1, 2, 3}

func statusEnum() string {
	encoded, _ := json.Marshal(statusValues)
	return string(encoded)
}

var runSummarySchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"scenario_id":{"type":"integer"},"team_id":{"type":"integer"},` +
	`"organization_id":{"type":"integer"},"status":{"type":"integer","enum":` + statusEnum() + `},` +
	`"type":{"type":"string"},"event_type":{"type":"string"},"timestamp":{},` +
	`"duration_ms":{"type":"integer"},"operations":{"type":"integer"},"transfer":{"type":"integer"},` +
	`"centicredits":{"type":"integer"},"interrupt_reason":{"type":"string"},` +
	`"interrupt_module_id":{"type":"string"},"author_id":{"type":"integer"},"author_name":{"type":"string"}},` +
	`"required":["id","scenario_id","team_id","status"],"additionalProperties":false}`

var runSummaryFields = []capability.Field{
	{Name: "id", Description: "Run identifier, used as execution_id by runs.get"},
	{Name: "scenario_id", Description: "Scenario this run belongs to, the scenario_id argument this run was read under"},
	{Name: "team_id", Description: "Team this run belongs to; always the connection's bound team"},
	{Name: "organization_id", Description: "Organization this run belongs to, present only when Make reports one"},
	{Name: "status", Description: "1 success, 2 warning, 3 error, as Make reports it"},
	{Name: "type", Description: "Kind of log entry Make recorded, for example an execution or a scenario edit"},
	{Name: "event_type", Description: "How the run was triggered, as Make reports it"},
	{Name: "timestamp", Description: "When this run happened, exactly as Make reports it, untyped since its " +
		"unit is not documented consistently"},
	{Name: "duration_ms", Description: "Run duration in milliseconds, as Make reports it"},
	{Name: "operations", Description: "Operations this run consumed"},
	{Name: "transfer", Description: "Data volume this run transferred, in bytes"},
	{Name: "centicredits", Description: "Credits this run consumed, in hundredths"},
	{Name: "interrupt_reason", Description: "Present only when the run is paused: sleep or hitl (human in the loop)"},
	{Name: "interrupt_module_id", Description: "The module the run is paused at, when interrupt_reason is set"},
	{Name: "author_id", Description: "Who triggered the run, when Make reports one"},
	{Name: "author_name", Description: "Name of who triggered the run, when Make reports one"},
}

var runsList = capability.Descriptor{
	ID:      Provider + ".runs.list",
	Version: 1,
	Title:   "List Make scenario runs",
	Description: "List the run history (Make's own \"logs\") of one scenario of the bound team; page by " +
		"page with a numeric offset. The scenario's team membership is confirmed live before any run is listed",
	Tags:     []string{"make", "runs", "list", "automation"},
	Risk:     makeReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"scenario_id":` + idSchema + `,` +
		`"offset":{"type":"integer","minimum":0},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxListLimit) + `},` +
		`"status":{"type":"integer","enum":` + statusEnum() + `},` +
		`"from_ms":{"type":"integer","minimum":0},"to_ms":{"type":"integer","minimum":0}},` +
		`"required":["scenario_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"scenario_id":{"type":"integer"},"runs":{"type":"array","items":` + runSummarySchema + `},` +
		`"offset":{"type":"integer"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["scenario_id","runs","offset","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{scenarioIDArgument,
		{Name: "offset", Description: "Runs to skip before this page; 0 when omitted"},
		{Name: "limit", Description: "Runs per page, 1 to " + strconv.Itoa(maxListLimit) + "; " +
			strconv.Itoa(defaultListLimit) + " when omitted"},
		{Name: "status", Description: "When set, list only runs of this status: 1 success, 2 warning, 3 error"},
		{Name: "from_ms", Description: "When set, list only runs at or after this time, Unix epoch milliseconds"},
		{Name: "to_ms", Description: "When set, list only runs at or before this time, Unix epoch milliseconds"},
	},
	Fields: append(append([]capability.Field{}, runSummaryFields...),
		capability.Field{Name: "offset", Description: "Offset of this page, for computing the next call's offset"},
		capability.Field{Name: "has_more", Description: "True when a further page likely remains; Make " +
			"reports no total count, so this is true whenever this page was full"},
		capability.Field{Name: "count", Description: "Number of runs reported on this page after this " +
			"connection's team and organization boundary was re-applied"},
	),
	Examples: []capability.Example{{Description: "List the first page of one scenario's runs",
		Arguments: json.RawMessage(`{"scenario_id":1}`)}},
}

var runsGet = capability.Descriptor{
	ID:      Provider + ".runs.get",
	Version: 1,
	Title:   "Get a Make scenario run",
	Description: "Read one run's status, timings, operations, and data volume, and, for a failed run, a " +
		"short best-effort error excerpt; never a bundle's actual input or output data",
	Tags:     []string{"make", "runs", "get", "automation"},
	Risk:     makeReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"scenario_id":` + idSchema + `,` +
		`"execution_id":` + executionIDSchema + `},"required":["scenario_id","execution_id"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(runDetailSchema),
	Arguments: []capability.Argument{scenarioIDArgument,
		{Name: "execution_id", Description: "Make run identifier, unique within the named scenario", Required: true},
	},
	Fields: append(append([]capability.Field{}, runSummaryFields...),
		capability.Field{Name: "error", Description: "Present only for a best-effort error excerpt Make's own " +
			"log entry carried for a failed run, bounded to " + strconv.Itoa(maxErrorExcerptLength) +
			" characters; never the run's bundle data"},
	),
	Examples: []capability.Example{{Description: "Read one run",
		Arguments: json.RawMessage(`{"scenario_id":1,"execution_id":"abc123"}`)}},
}

var runDetailSchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"scenario_id":{"type":"integer"},"team_id":{"type":"integer"},` +
	`"organization_id":{"type":"integer"},"status":{"type":"integer","enum":` + statusEnum() + `},` +
	`"type":{"type":"string"},"event_type":{"type":"string"},"timestamp":{},` +
	`"duration_ms":{"type":"integer"},"operations":{"type":"integer"},"transfer":{"type":"integer"},` +
	`"centicredits":{"type":"integer"},"interrupt_reason":{"type":"string"},` +
	`"interrupt_module_id":{"type":"string"},"author_id":{"type":"integer"},"author_name":{"type":"string"},` +
	`"error":{"type":"string"}},"required":["id","scenario_id","team_id","status"],"additionalProperties":false}`

// runJSON mirrors the subset of Make's scenario log entry this provider reads
// (developers.make.com/api-documentation/api-reference/scenarios/logs).
type runJSON struct {
	ID                string          `json:"id"`
	TeamID            int64           `json:"teamId"`
	OrganizationID    int64           `json:"organizationId"`
	Status            int             `json:"status"`
	Type              string          `json:"type"`
	EventType         string          `json:"eventType"`
	Timestamp         json.RawMessage `json:"timestamp"`
	Duration          int64           `json:"duration"`
	Operations        int64           `json:"operations"`
	Transfer          int64           `json:"transfer"`
	Centicredits      int64           `json:"centicredits"`
	InterruptReason   string          `json:"interruptReason"`
	InterruptModuleID string          `json:"interruptModuleId"`
	AuthorID          int64           `json:"authorId"`
	AuthorName        string          `json:"authorName"`
	Detail            json.RawMessage `json:"detail"`
}

// RunSummary is the stable, scope-checked listing view of one run. ScenarioID is not a field Make's own log
// entry is confirmed to carry; this provider sets it from the already scope-checked scenario_id the call was
// made under, never from an unconfirmed response field.
type RunSummary struct {
	ID                string          `json:"id"`
	ScenarioID        int64           `json:"scenario_id"`
	TeamID            int64           `json:"team_id"`
	OrganizationID    int64           `json:"organization_id,omitempty"`
	Status            int             `json:"status"`
	Type              string          `json:"type,omitempty"`
	EventType         string          `json:"event_type,omitempty"`
	Timestamp         json.RawMessage `json:"timestamp,omitempty"`
	DurationMS        int64           `json:"duration_ms,omitempty"`
	Operations        int64           `json:"operations,omitempty"`
	Transfer          int64           `json:"transfer,omitempty"`
	Centicredits      int64           `json:"centicredits,omitempty"`
	InterruptReason   string          `json:"interrupt_reason,omitempty"`
	InterruptModuleID string          `json:"interrupt_module_id,omitempty"`
	AuthorID          int64           `json:"author_id,omitempty"`
	AuthorName        string          `json:"author_name,omitempty"`
}

// RunDetail is the stable, scope-checked read view of one run.
type RunDetail struct {
	RunSummary
	Error string `json:"error,omitempty"`
}

func runSummaryOf(scenarioID int64, r runJSON) RunSummary {
	timestamp := r.Timestamp
	if len(timestamp) == 0 {
		timestamp = nil
	}
	return RunSummary{
		ID: bounded(r.ID), ScenarioID: scenarioID, TeamID: r.TeamID, OrganizationID: r.OrganizationID,
		Status: r.Status, Type: bounded(r.Type), EventType: bounded(r.EventType), Timestamp: timestamp,
		DurationMS: r.Duration, Operations: r.Operations, Transfer: r.Transfer, Centicredits: r.Centicredits,
		InterruptReason: bounded(r.InterruptReason), InterruptModuleID: bounded(r.InterruptModuleID),
		AuthorID: r.AuthorID, AuthorName: bounded(r.AuthorName),
	}
}

type runsPageJSON struct {
	ScenarioLogs []runJSON `json:"scenarioLogs"`
}

// RunsPage is one offset-paginated, scope-filtered listing of runs of one scenario.
type RunsPage struct {
	ScenarioID int64        `json:"scenario_id"`
	Runs       []RunSummary `json:"runs"`
	Offset     int          `json:"offset"`
	HasMore    bool         `json:"has_more"`
	Count      int          `json:"count"`
}

type runsListArguments struct {
	ScenarioID int64 `json:"scenario_id"`
	Offset     int   `json:"offset"`
	Limit      int   `json:"limit"`
	Status     int   `json:"status"`
	FromMS     int64 `json:"from_ms"`
	ToMS       int64 `json:"to_ms"`
}

func invokeRunsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input runsListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list runs", "the validated arguments could not be read")
	}
	if err := selectScenario(resolved, input.ScenarioID); err != nil {
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
	const op = "list runs"
	scenario, err := client.fetchScenario(ctx, op, input.ScenarioID)
	if err != nil {
		return nil, err
	}
	if err := client.verifyScenarioScope(scenario); err != nil {
		return nil, err
	}
	return client.ListRuns(ctx, input.ScenarioID, input.Offset, limit, input.Status, input.FromMS, input.ToMS)
}

// ListRuns reads one page of a scenario's runs. The caller must already have confirmed the scenario's team
// with verifyScenarioScope. Every returned entry is defensively re-checked against the bound team and,
// when configured, the bound organization, even though the scenario itself was already confirmed: a log
// entry answer carries its own teamId and organizationId, and this provider trusts its own scope check over
// Make's filtering. has_more is a heuristic the same way ListScenarios' own is: Make's pagination object
// carries no total count.
func (c *Client) ListRuns(ctx context.Context, scenarioID int64, offset, limit, status int, fromMS, toMS int64) (*RunsPage, error) {
	const op = "list runs"
	query := url.Values{"pg[offset]": {strconv.Itoa(offset)}, "pg[limit]": {strconv.Itoa(limit)}}
	if status != 0 {
		query.Set("status", strconv.Itoa(status))
	}
	if fromMS > 0 {
		query.Set("from", strconv.FormatInt(fromMS, 10))
	}
	if toMS > 0 {
		query.Set("to", strconv.FormatInt(toMS, 10))
	}
	var page runsPageJSON
	if err := c.get(ctx, op, "/scenarios/"+strconv.FormatInt(scenarioID, 10)+"/logs", query, &page, needRead); err != nil {
		return nil, err
	}
	summaries := make([]RunSummary, 0, len(page.ScenarioLogs))
	for _, r := range page.ScenarioLogs {
		if !c.scope.allowsTeam(r.TeamID) || !c.scope.allowsOrg(r.OrganizationID) {
			continue
		}
		summaries = append(summaries, runSummaryOf(scenarioID, r))
	}
	return &RunsPage{
		ScenarioID: scenarioID, Runs: summaries, Offset: offset, HasMore: len(page.ScenarioLogs) == limit,
		Count: len(summaries),
	}, nil
}

type runArguments struct {
	ScenarioID  int64  `json:"scenario_id"`
	ExecutionID string `json:"execution_id"`
}

func invokeRunsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input runArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("get run", "the validated arguments could not be read")
	}
	if err := selectScenario(resolved, input.ScenarioID); err != nil {
		return nil, err
	}
	if !validExecutionID(input.ExecutionID) {
		return nil, invalidRequest("execution_id must be a usable Make run identifier")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "get run"
	scenario, err := client.fetchScenario(ctx, op, input.ScenarioID)
	if err != nil {
		return nil, err
	}
	if err := client.verifyScenarioScope(scenario); err != nil {
		return nil, err
	}
	return client.GetRun(ctx, input.ScenarioID, input.ExecutionID)
}

// GetRun reads one run of one scenario. The caller must already have confirmed the scenario's team with
// verifyScenarioScope. The returned run is defensively re-checked against the bound team and, when
// configured, the bound organization, the same defence ListRuns applies to every entry of a page: Make
// itself scopes the logs path by scenario_id, but this provider verifies its own boundary instead of
// trusting that scoping alone.
func (c *Client) GetRun(ctx context.Context, scenarioID int64, executionID string) (*RunDetail, error) {
	const op = "get run"
	var wrapper struct {
		ScenarioLog runJSON `json:"scenarioLog"`
	}
	path := "/scenarios/" + strconv.FormatInt(scenarioID, 10) + "/logs/" + url.PathEscape(executionID)
	if err := c.get(ctx, op, path, nil, &wrapper, needRead); err != nil {
		return nil, err
	}
	run := wrapper.ScenarioLog
	if run.ID != executionID {
		return nil, invalidResponse(op, "Make answered with a run other than the one requested")
	}
	if !c.scope.allowsTeam(run.TeamID) || !c.scope.allowsOrg(run.OrganizationID) {
		return nil, invalidRequest("execution_id belongs to a team or organization outside the targets of this connection")
	}
	detail := RunDetail{RunSummary: runSummaryOf(scenarioID, run)}
	detail.Error = bestEffortError(run.Detail)
	return &detail, nil
}

// bestEffortError extracts a short error message from a run's own "detail" object, when its shape happens
// to carry one under detail.error.message, the common shape Make's own error payloads elsewhere use. The
// exact schema of a scenario log's "detail" object is not documented field by field, so this is
// deliberately conservative: anything this cannot recognise, including the versioned scenario-history
// changes Make attaches to a "modify" log entry, is never surfaced at all rather than guessed at or passed
// through raw.
// qatlas-dev: extend this once a real failed run's "detail" shape is confirmed against a live account.
func bestEffortError(detail json.RawMessage) string {
	if len(detail) == 0 {
		return ""
	}
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(detail, &parsed); err != nil {
		return ""
	}
	message := strings.TrimSpace(parsed.Error.Message)
	if message == "" {
		return ""
	}
	if len(message) > maxErrorExcerptLength {
		message = message[:maxErrorExcerptLength]
	}
	return message
}

func validExecutionID(value string) bool {
	if value == "" || len(value) > maxExecutionIDLength {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			continue
		default:
			return false
		}
	}
	return true
}

var scenarioRunSchema = `{"type":"object","properties":{` +
	`"scenario_id":{"type":"integer"},"execution_id":{"type":"string"},"status":{}},` +
	`"required":["scenario_id","execution_id"],"additionalProperties":false}`

var scenariosRun = capability.Descriptor{
	ID:      Provider + ".scenarios.run",
	Version: 1,
	Title:   "Run a Make scenario",
	Description: "Run one scenario of the bound team on demand, starting exactly one new execution; a " +
		"repeated call starts another execution every time, never the same one again. Needs the " +
		"scenarios:read, scenarios:write, and scenarios:run scopes. Offers no callback_url argument: this " +
		"provider never places a caller-chosen URL into an outbound call it does not control. responsive " +
		"defaults to false, so this one request returns as soon as the run starts instead of blocking until " +
		"it finishes; use make.runs.get or make.runs.list afterward to learn the run's outcome",
	Tags:     []string{"make", "scenarios", "run", "automation", "execute"},
	Risk:     makeChangeRisk(capability.EffectExecute, capability.IdempotencyNonIdempotent),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"scenario_id":` + idSchema + `,` +
		`"data":{"type":"object"},"responsive":{"type":"boolean"}},` +
		`"required":["scenario_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(scenarioRunSchema),
	Arguments: []capability.Argument{scenarioIDArgument,
		{Name: "data", Description: "Input parameters for a scenario that expects them, as a JSON object, " +
			"up to " + strconv.Itoa(maxRunDataBytes) + " bytes"},
		{Name: "responsive", Description: "When true, this one request waits until the run finishes instead " +
			"of returning as soon as it starts; false when omitted, since a long-running scenario could " +
			"easily outlast this request's own timeout and then be reported as merely uncertain"},
	},
	Fields: []capability.Field{
		{Name: "scenario_id", Description: "The scenario this run belongs to"},
		{Name: "execution_id", Description: "Identifier of the new run; Make documents this as the same " +
			"identifier make.runs.get and make.runs.list read under their own id"},
		{Name: "status", Description: "Present only when Make reports one immediately; its exact shape is " +
			"not documented consistently, so it is passed through opaquely, untyped, and never interpreted here"},
	},
	Examples: []capability.Example{{Description: "Run a scenario with no input",
		Arguments: json.RawMessage(`{"scenario_id":1}`)}},
}

type scenariosRunArguments struct {
	ScenarioID int64           `json:"scenario_id"`
	Data       json.RawMessage `json:"data"`
	Responsive bool            `json:"responsive"`
}

func invokeScenariosRun(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "run scenario"
	var input scenariosRunArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectScenario(resolved, input.ScenarioID); err != nil {
		return nil, err
	}
	if len(input.Data) > 0 {
		if err := validJSONObject(input.Data, maxRunDataBytes, maxRunDataDepth, "data"); err != nil {
			return nil, err
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	scenario, err := client.fetchScenario(ctx, op, input.ScenarioID)
	if err != nil {
		return nil, err
	}
	if err := client.verifyScenarioScope(scenario); err != nil {
		return nil, err
	}
	return client.RunScenario(ctx, input.ScenarioID, input.Data, input.Responsive)
}

// ScenarioRun is the answer of one confirmed scenarios.run: the new run's own identifier, and, when Make
// reports one right away, its status. See the package doc for why this is never followed by a re-read the
// way every other change of this provider is.
type ScenarioRun struct {
	ScenarioID  int64           `json:"scenario_id"`
	ExecutionID string          `json:"execution_id"`
	Status      json.RawMessage `json:"status,omitempty"`
}

// RunScenario sends the one changing POST /scenarios/{id}/run request. data, when given, is sent as an
// ordinary nested JSON object, unlike blueprint and scheduling elsewhere in this package, which Make's own
// schema documents as strings instead (see the package doc). callback_url is never offered as an argument at
// all, so this method never builds one into the request; responsive is sent only when the caller asked for
// it, since Make itself already defaults it to false. A timeout, a connection reset, or a 5xx after this
// request is marked with runUncertain, not the generic uncertain every other change of this provider uses,
// since a run is never followed by a re-read this provider could point a caller back to instead.
func (c *Client) RunScenario(ctx context.Context, scenarioID int64, data json.RawMessage, responsive bool) (*ScenarioRun, error) {
	const op = "run scenario"
	body := map[string]any{}
	if len(data) > 0 {
		body["data"] = data
	}
	if responsive {
		body["responsive"] = true
	}
	var result struct {
		ExecutionID string          `json:"executionId"`
		Status      json.RawMessage `json:"status"`
	}
	if err := c.change(ctx, op, http.MethodPost, "/scenarios/"+strconv.FormatInt(scenarioID, 10)+"/run", nil,
		body, &result, needRun, runUncertain); err != nil {
		return nil, err
	}
	if result.ExecutionID == "" {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "Make did not report an execution id for the run" + runUncertain}
	}
	run := &ScenarioRun{ScenarioID: scenarioID, ExecutionID: bounded(result.ExecutionID)}
	if len(result.Status) > 0 {
		run.Status = result.Status
	}
	return run, nil
}
