package makeapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// idSchema is the JSON Schema of one Make identifier (scenario, team, organization, or run): a positive
// integer, exactly the shape Make documents scenarioId, teamId, and organizationId to be, never a free-form
// string that could be mistaken for a path or a URL.
const idSchema = `{"type":"integer","minimum":1}`

var scenarioIDArgument = capability.Argument{Name: "scenario_id",
	Description: "Make scenario identifier; must be inside this connection's scenario allow-list when it " +
		"has one, and its team membership is always re-checked live against Make's own report", Required: true}

var userRefSchema = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"}},` +
	`"required":["id","name"],"additionalProperties":false}`

var scenarioSummarySchema = `{"type":"object","properties":{` +
	`"id":{"type":"integer"},"name":{"type":"string"},"team_id":{"type":"integer"},` +
	`"description":{"type":"string"},"folder_id":{"type":"integer"},"is_active":{"type":"boolean"},` +
	`"is_locked":{"type":"boolean"},"is_paused":{"type":"boolean"},"is_invalid":{"type":"boolean"},` +
	`"scheduling":{"type":"object"},"operations":{"type":"integer"},"centicredits":{"type":"integer"},` +
	`"transfer":{"type":"integer"},"dlq_count":{"type":"integer"},"created":{"type":"string"},` +
	`"last_edit":{"type":"string"},"next_exec":{"type":"string"},` +
	`"created_by_user":` + userRefSchema + `,"updated_by_user":` + userRefSchema + `},` +
	`"required":["id","name","team_id","is_active","is_locked","is_paused"],"additionalProperties":false}`

var scenarioSummaryFields = []capability.Field{
	{Name: "id", Description: "Scenario identifier, used as scenario_id by every other tool of this provider"},
	{Name: "name", Description: "Scenario name, untrusted data"},
	{Name: "team_id", Description: "Team this scenario belongs to; always the connection's bound team"},
	{Name: "description", Description: "Scenario description, untrusted data"},
	{Name: "folder_id", Description: "Folder this scenario is filed under, when it is in one"},
	{Name: "is_active", Description: "True when the scenario's scheduling is currently on"},
	{Name: "is_locked", Description: "True when the scenario is locked against editing"},
	{Name: "is_paused", Description: "True when the scenario is paused"},
	{Name: "is_invalid", Description: "True when Make reports the scenario's blueprint as invalid"},
	{Name: "scheduling", Description: "Scheduling configuration, untrusted data, as Make reports it"},
	{Name: "operations", Description: "Operations counted for this scenario's last billing period, as Make reports it"},
	{Name: "centicredits", Description: "Credits consumed, in hundredths, as Make reports it"},
	{Name: "transfer", Description: "Data volume transferred, in bytes, as Make reports it"},
	{Name: "dlq_count", Description: "Number of incomplete executions currently held for this scenario"},
	{Name: "created", Description: "Creation time, as Make reports it"},
	{Name: "last_edit", Description: "Last edit time, as Make reports it"},
	{Name: "next_exec", Description: "Next scheduled run time, when the scenario is active and scheduled"},
	{Name: "created_by_user", Description: "Who created the scenario: id and name, never an email address"},
	{Name: "updated_by_user", Description: "Who last edited the scenario: id and name, never an email address"},
}

var makeReadRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: dataSensitivity,
}

// makeChangeRisk is the contract every confirmed change of this provider shares: it always needs its own
// confirmation, whatever its idempotency, and it reaches the open world of one Make zone's scenarios.
func makeChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: dataSensitivity}
}

var scenariosList = capability.Descriptor{
	ID:      Provider + ".scenarios.list",
	Version: 1,
	Title:   "List Make scenarios",
	Description: "List the scenarios of the connection's bound team, restricted to its scenario allow-list " +
		"when it has one; page by page with a numeric offset",
	Tags:     []string{"make", "scenarios", "list", "automation"},
	Risk:     makeReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"offset":{"type":"integer","minimum":0},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxListLimit) + `},` +
		`"is_active":{"type":"boolean"},"name":{"type":"string","minLength":1,"maxLength":256}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"scenarios":{"type":"array","items":` + scenarioSummarySchema + `},` +
		`"offset":{"type":"integer"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["scenarios","offset","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "offset", Description: "Scenarios to skip before this page; 0 when omitted"},
		{Name: "limit", Description: "Scenarios per page, 1 to " + strconv.Itoa(maxListLimit) + "; " +
			strconv.Itoa(defaultListLimit) + " when omitted"},
		{Name: "is_active", Description: "When set, list only active or only inactive scenarios"},
		{Name: "name", Description: "When set, list only scenarios Make matches by this case-insensitive substring"},
	},
	Fields: append(append([]capability.Field{}, scenarioSummaryFields...),
		capability.Field{Name: "offset", Description: "Offset of this page, for computing the next call's offset"},
		capability.Field{Name: "has_more", Description: "True when a further page likely remains; Make " +
			"reports no total count, so this is true whenever this page was full"},
		capability.Field{Name: "count", Description: "Number of scenarios reported on this page after this " +
			"connection's scenario allow-list was applied"},
	),
	Examples: []capability.Example{{Description: "List the first page of reachable scenarios", Arguments: json.RawMessage(`{}`)}},
}

var scenariosGet = capability.Descriptor{
	ID:          Provider + ".scenarios.get",
	Version:     1,
	Title:       "Get a Make scenario",
	Description: "Read one scenario of the bound team's own report",
	Tags:        []string{"make", "scenarios", "get", "automation"},
	Risk:        makeReadRisk,
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"scenario_id":` + idSchema + `},` +
		`"required":["scenario_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(scenarioSummarySchema),
	Arguments:    []capability.Argument{scenarioIDArgument},
	Fields:       scenarioSummaryFields,
	Examples:     []capability.Example{{Description: "Read one scenario", Arguments: json.RawMessage(`{"scenario_id":1}`)}},
}

// userRefJSON mirrors the id/name/email object Make attaches to createdByUser and updatedByUser. The email
// is read but never surfaced: it is the acting person's own contact address, not scenario or run content,
// and this provider follows the same minimisation the n8n provider's credential references apply to what it
// passes through by choice, not by necessity.
type userRefJSON struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// UserRef is the stable Qatlas view of one user reference: never the email address Make also reports.
type UserRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func userRefOf(u *userRefJSON) *UserRef {
	if u == nil || (u.ID == 0 && u.Name == "") {
		return nil
	}
	return &UserRef{ID: u.ID, Name: bounded(u.Name)}
}

// scenarioJSON mirrors the subset of Make's scenario object this provider reads
// (developers.make.com/api-documentation/api-reference/scenarios).
type scenarioJSON struct {
	ID            int64           `json:"id"`
	Name          string          `json:"name"`
	TeamID        int64           `json:"teamId"`
	Description   string          `json:"description"`
	FolderID      int64           `json:"folderId"`
	IsActive      bool            `json:"isActive"`
	IsLocked      bool            `json:"islocked"`
	IsPaused      bool            `json:"isPaused"`
	IsInvalid     bool            `json:"isinvalid"`
	Scheduling    json.RawMessage `json:"scheduling"`
	Operations    int64           `json:"operations"`
	Centicredits  int64           `json:"centicredits"`
	Transfer      int64           `json:"transfer"`
	DlqCount      int64           `json:"dlqCount"`
	Created       string          `json:"created"`
	LastEdit      string          `json:"lastEdit"`
	NextExec      string          `json:"nextExec"`
	CreatedByUser *userRefJSON    `json:"createdByUser"`
	UpdatedByUser *userRefJSON    `json:"updatedByUser"`
}

// ScenarioSummary is the stable, scope-checked view of one scenario, shared by scenarios.list and
// scenarios.get: Make's own detail endpoint answers with the same object shape a list entry already has.
type ScenarioSummary struct {
	ID            int64           `json:"id"`
	Name          string          `json:"name"`
	TeamID        int64           `json:"team_id"`
	Description   string          `json:"description,omitempty"`
	FolderID      int64           `json:"folder_id,omitempty"`
	IsActive      bool            `json:"is_active"`
	IsLocked      bool            `json:"is_locked"`
	IsPaused      bool            `json:"is_paused"`
	IsInvalid     bool            `json:"is_invalid,omitempty"`
	Scheduling    json.RawMessage `json:"scheduling,omitempty"`
	Operations    int64           `json:"operations,omitempty"`
	Centicredits  int64           `json:"centicredits,omitempty"`
	Transfer      int64           `json:"transfer,omitempty"`
	DlqCount      int64           `json:"dlq_count,omitempty"`
	Created       string          `json:"created,omitempty"`
	LastEdit      string          `json:"last_edit,omitempty"`
	NextExec      string          `json:"next_exec,omitempty"`
	CreatedByUser *UserRef        `json:"created_by_user,omitempty"`
	UpdatedByUser *UserRef        `json:"updated_by_user,omitempty"`
}

func summaryOf(s scenarioJSON) ScenarioSummary {
	scheduling := s.Scheduling
	if len(scheduling) == 0 {
		scheduling = nil
	}
	return ScenarioSummary{
		ID: s.ID, Name: bounded(s.Name), TeamID: s.TeamID, Description: bounded(s.Description),
		FolderID: s.FolderID, IsActive: s.IsActive, IsLocked: s.IsLocked, IsPaused: s.IsPaused,
		IsInvalid: s.IsInvalid, Scheduling: scheduling, Operations: s.Operations, Centicredits: s.Centicredits,
		Transfer: s.Transfer, DlqCount: s.DlqCount, Created: bounded(s.Created), LastEdit: bounded(s.LastEdit),
		NextExec: bounded(s.NextExec), CreatedByUser: userRefOf(s.CreatedByUser), UpdatedByUser: userRefOf(s.UpdatedByUser),
	}
}

type scenariosPageJSON struct {
	Scenarios []scenarioJSON `json:"scenarios"`
}

// ScenariosPage is one offset-paginated, allow-list-filtered listing of scenarios.
type ScenariosPage struct {
	Scenarios []ScenarioSummary `json:"scenarios"`
	Offset    int               `json:"offset"`
	HasMore   bool              `json:"has_more"`
	Count     int               `json:"count"`
}

type scenariosListArguments struct {
	Offset   int    `json:"offset"`
	Limit    int    `json:"limit"`
	IsActive *bool  `json:"is_active"`
	Name     string `json:"name"`
}

func invokeScenariosList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input scenariosListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list scenarios", "the validated arguments could not be read")
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListScenarios(ctx, input.Offset, limit, input.IsActive, input.Name)
}

// ListScenarios reads one page of scenarios of the bound team, filtering server-side by teamId and by every
// argument Make's own "GET /scenarios" accepts, and defensively re-applying this connection's scenario
// allow-list to the page it answers with. has_more is a heuristic, not a value Make reports: Make's own
// pagination object (pg) echoes the request's offset and limit but carries no total count, so a full page is
// read as "more may follow", the same convention the seatable provider's own offset pagination uses.
func (c *Client) ListScenarios(ctx context.Context, offset, limit int, isActive *bool, name string) (*ScenariosPage, error) {
	const op = "list scenarios"
	query := url.Values{
		"teamId":     {strconv.FormatInt(c.scope.teamID, 10)},
		"pg[offset]": {strconv.Itoa(offset)},
		"pg[limit]":  {strconv.Itoa(limit)},
	}
	if isActive != nil {
		query.Set("isActive", strconv.FormatBool(*isActive))
	}
	if name != "" {
		query.Set("name", name)
	}
	var page scenariosPageJSON
	if err := c.get(ctx, op, "/scenarios", query, &page, needRead); err != nil {
		return nil, err
	}
	summaries := make([]ScenarioSummary, 0, len(page.Scenarios))
	for _, s := range page.Scenarios {
		if !c.scope.allowsTeam(s.TeamID) || !c.scope.allowsScenario(s.ID) {
			continue
		}
		summaries = append(summaries, summaryOf(s))
	}
	return &ScenariosPage{
		Scenarios: summaries, Offset: offset, HasMore: len(page.Scenarios) == limit, Count: len(summaries),
	}, nil
}

type scenarioArguments struct {
	ScenarioID int64 `json:"scenario_id"`
}

func invokeScenariosGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input scenarioArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("get scenario", "the validated arguments could not be read")
	}
	if err := selectScenario(resolved, input.ScenarioID); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	scenario, err := client.fetchScenario(ctx, "get scenario", input.ScenarioID)
	if err != nil {
		return nil, err
	}
	if err := client.verifyScenarioScope(scenario); err != nil {
		return nil, err
	}
	summary := summaryOf(*scenario)
	return &summary, nil
}

// fetchScenario reads exactly one scenario by ID, the same read scenarios.get, scenarios.blueprint, and
// runs.list/runs.get all use to confirm live that a scenario_id argument belongs to the bound team before
// any of that scenario's content, blueprint, or run history is returned.
func (c *Client) fetchScenario(ctx context.Context, op string, scenarioID int64) (*scenarioJSON, error) {
	var wrapper struct {
		Scenario scenarioJSON `json:"scenario"`
	}
	if err := c.get(ctx, op, "/scenarios/"+strconv.FormatInt(scenarioID, 10), nil, &wrapper, needRead); err != nil {
		return nil, err
	}
	if wrapper.Scenario.ID != scenarioID {
		return nil, invalidResponse(op, "Make answered with a scenario other than the one requested")
	}
	return &wrapper.Scenario, nil
}

// verifyScenarioScope confirms, against the scenario Make itself just answered with, that it belongs to the
// bound team, and, defensively, that it is still inside this connection's scenario allow-list. Local
// configuration alone proves nothing about what a scenario_id really resolves to; this is the live check the
// package doc describes. A scenario of another team is refused the same way as one outside the allow-list:
// an invalid request, never a provider error, and the refusal never names the scenario's real team.
func (c *Client) verifyScenarioScope(scenario *scenarioJSON) error {
	if !c.scope.allowsTeam(scenario.TeamID) {
		return invalidRequest("scenario_id belongs to a team outside the targets of this connection")
	}
	if !c.scope.allowsScenario(scenario.ID) {
		return invalidRequest("scenario_id is outside the targets of this connection")
	}
	return nil
}

// verifyScopeAfterChange re-applies the bound team and, defensively, this connection's scenario allow-list
// to a scenario after one changing request (create, update, start, or stop) has already been sent. Unlike
// verifyScenarioScope's pre-request refusal, a mismatch here can no longer mean "never sent": it is reported
// as a provider error, not an invalid request, because the change has already taken effect and this
// milestone offers no delete tool to undo it, the same distinction n8n's own verifyPlacement draws for its
// own create and update.
func (c *Client) verifyScopeAfterChange(op string, scenario *scenarioJSON) error {
	if !c.scope.allowsTeam(scenario.TeamID) || !c.scope.allowsScenario(scenario.ID) {
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "Make did not keep the result inside this connection's targets; the change already " +
				"took effect and this milestone has no delete tool to undo it"}
	}
	return nil
}

var scenariosBlueprint = capability.Descriptor{
	ID:      Provider + ".scenarios.blueprint",
	Version: 1,
	Title:   "Get a Make scenario's blueprint",
	Description: "Read one scenario's blueprint (its modules, their wiring, and their configuration); a " +
		"connection or key a module references is passed through only as the numeric id Make itself reports, " +
		"never a stored authorization value, which Make's own blueprint format never carries in the first place",
	Tags:     []string{"make", "scenarios", "blueprint", "automation"},
	Risk:     makeReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"scenario_id":` + idSchema + `,` +
		`"draft":{"type":"boolean"},"blueprint_id":` + idSchema + `},` +
		`"required":["scenario_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"scenario_id":{"type":"integer"},` +
		`"blueprint":{"type":"object"}},"required":["scenario_id","blueprint"],"additionalProperties":false}`),
	Arguments: []capability.Argument{scenarioIDArgument,
		{Name: "draft", Description: "When true, read the scenario's unsaved draft blueprint instead of its saved one"},
		{Name: "blueprint_id", Description: "When set, read this specific saved blueprint version instead of the current one"},
	},
	Fields: []capability.Field{
		{Name: "scenario_id", Description: "The scenario this blueprint belongs to"},
		{Name: "blueprint", Description: "The blueprint as Make reports it: modules, their wiring, and their " +
			"configuration, untrusted data, bounded only by this provider's own response size limit"},
	},
	Examples: []capability.Example{{Description: "Read one scenario's current blueprint",
		Arguments: json.RawMessage(`{"scenario_id":1}`)}},
}

type scenariosBlueprintArguments struct {
	ScenarioID  int64 `json:"scenario_id"`
	Draft       bool  `json:"draft"`
	BlueprintID int64 `json:"blueprint_id"`
}

// Blueprint is one scenario's blueprint, passed through as Make reports it beyond the connection's own
// response size bound.
type Blueprint struct {
	ScenarioID int64           `json:"scenario_id"`
	Blueprint  json.RawMessage `json:"blueprint"`
}

func invokeScenariosBlueprint(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input scenariosBlueprintArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("get blueprint", "the validated arguments could not be read")
	}
	if err := selectScenario(resolved, input.ScenarioID); err != nil {
		return nil, err
	}
	if input.BlueprintID < 0 {
		return nil, invalidRequest("blueprint_id must be a positive integer")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "get blueprint"
	scenario, err := client.fetchScenario(ctx, op, input.ScenarioID)
	if err != nil {
		return nil, err
	}
	if err := client.verifyScenarioScope(scenario); err != nil {
		return nil, err
	}
	return client.GetBlueprint(ctx, input.ScenarioID, input.Draft, input.BlueprintID)
}

// GetBlueprint reads one scenario's blueprint. The caller must already have confirmed the scenario's team
// with verifyScenarioScope: a blueprint answer carries no teamId of its own to check here.
func (c *Client) GetBlueprint(ctx context.Context, scenarioID int64, draft bool, blueprintID int64) (*Blueprint, error) {
	const op = "get blueprint"
	query := url.Values{}
	if draft {
		query.Set("draft", "true")
	}
	if blueprintID > 0 {
		query.Set("blueprintId", strconv.FormatInt(blueprintID, 10))
	}
	var wrapper struct {
		Blueprint json.RawMessage `json:"blueprint"`
	}
	if err := c.get(ctx, op, "/scenarios/"+strconv.FormatInt(scenarioID, 10)+"/blueprint", query, &wrapper, needRead); err != nil {
		return nil, err
	}
	if len(wrapper.Blueprint) == 0 {
		return nil, invalidResponse(op, "Make did not report a blueprint")
	}
	return &Blueprint{ScenarioID: scenarioID, Blueprint: wrapper.Blueprint}, nil
}

// schedulingWriteSchema is the JSON Schema of a scheduling argument scenarios.create and scenarios.update
// accept: an object with at least a "type" field, the only part of the shape Make documents at all
// (developers.make.com's own scheduling object shows "type" and "interval"). additionalProperties stays
// true deliberately: Make's own scheduling types each carry further, undocumented fields of their own (for
// example an interval's own unit, or a specific day and time for other types), and closing the schema to
// only "type" and "interval" would silently reject every one of those instead of letting Make's own PATCH or
// POST validate them.
var schedulingWriteSchema = `{"type":"object","properties":{"type":{"type":"string","minLength":1,` +
	`"maxLength":64}},"required":["type"],"additionalProperties":true}`

var scenariosCreate = capability.Descriptor{
	ID:      Provider + ".scenarios.create",
	Version: 1,
	Title:   "Create a Make scenario",
	Description: "Create one scenario in the bound team from a blueprint and a scheduling configuration; a " +
		"repeated call creates a second scenario, never replaces the first. Always creates in the " +
		"connection's own bound team, never a caller-named one, and is refused outright on a connection " +
		"restricted by a scenario allow-list, since a scenario that does not exist yet can never already be " +
		"on that list",
	Tags:     []string{"make", "scenarios", "create", "automation"},
	Risk:     makeChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"blueprint":{"type":"object"},"scheduling":` + schedulingWriteSchema + `,` +
		`"folder_id":` + idSchema + `,"description":{"type":"string","maxLength":` +
		strconv.Itoa(maxDescriptionLength) + `},"confirm_new_app":{"type":"boolean"}},` +
		`"required":["blueprint","scheduling"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(scenarioSummarySchema),
	Arguments: []capability.Argument{
		{Name: "blueprint", Description: "The scenario's modules, their wiring, and their configuration, as " +
			"a JSON object; Make validates every module and any connection, key, or webhook reference itself", Required: true},
		{Name: "scheduling", Description: "Scheduling configuration, a JSON object with at least a \"type\" " +
			"field; further, type-specific fields are undocumented here and passed through unvalidated beyond " +
			"a size and nesting limit", Required: true},
		{Name: "folder_id", Description: "Folder to file the new scenario under, when this zone uses folders"},
		{Name: "description", Description: "Scenario description, up to " + strconv.Itoa(maxDescriptionLength) + " characters"},
		{Name: "confirm_new_app", Description: "Set true to confirm creating this scenario when its blueprint " +
			"uses an app for the first time in this organization; Make otherwise refuses the create"},
	},
	Fields: scenarioSummaryFields,
	Examples: []capability.Example{{Description: "Create a minimal on-demand scenario",
		Arguments: json.RawMessage(`{"blueprint":{"name":"My scenario","flow":[]},"scheduling":{"type":"on-demand"}}`)}},
}

type scenariosCreateArguments struct {
	Blueprint     json.RawMessage `json:"blueprint"`
	Scheduling    json.RawMessage `json:"scheduling"`
	FolderID      int64           `json:"folder_id"`
	Description   string          `json:"description"`
	ConfirmNewApp bool            `json:"confirm_new_app"`
}

func invokeScenariosCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create scenario"
	var input scenariosCreateArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if len(bound.scenarios) > 0 {
		return nil, invalidRequest("this connection restricts scenarios by an allow-list, so it cannot " +
			"create one: a newly created scenario can never already be on that list")
	}
	if err := validJSONObject(input.Blueprint, maxBlueprintWriteBytes, maxBlueprintDepth, "blueprint"); err != nil {
		return nil, err
	}
	if err := validJSONObject(input.Scheduling, maxSchedulingWriteBytes, maxSchedulingDepth, "scheduling"); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreateScenario(ctx, input.Blueprint, input.Scheduling, input.FolderID, input.Description,
		input.ConfirmNewApp)
}

// CreateScenario sends the one changing POST /scenarios request, teamId always the connection's own bound
// team, never a caller-supplied one, and blueprint and scheduling encoded to the JSON strings Make's own
// request body wants (see the package doc). Make's own create response already carries the full created
// scenario, unlike n8n's own createWorkflow, so this reads it directly instead of a second, separate GET, and
// re-applies the bound team and scenario allow-list to it before returning.
func (c *Client) CreateScenario(ctx context.Context, blueprint, scheduling json.RawMessage, folderID int64,
	description string, confirmNewApp bool) (*ScenarioSummary, error) {
	const op = "create scenario"
	body := map[string]any{
		"teamId":     c.scope.teamID,
		"blueprint":  string(blueprint),
		"scheduling": string(scheduling),
	}
	if folderID > 0 {
		body["folderId"] = folderID
	}
	if description != "" {
		body["description"] = description
	}
	var query url.Values
	if confirmNewApp {
		query = url.Values{"confirmed": {"true"}}
	}
	var wrapper struct {
		Scenario scenarioJSON `json:"scenario"`
	}
	if err := c.change(ctx, op, http.MethodPost, "/scenarios", query, body, &wrapper, needWrite, uncertain); err != nil {
		return nil, err
	}
	if wrapper.Scenario.ID == 0 {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "Make did not report the created scenario" + uncertain}
	}
	if err := c.verifyScopeAfterChange(op, &wrapper.Scenario); err != nil {
		return nil, err
	}
	summary := summaryOf(wrapper.Scenario)
	return &summary, nil
}

var scenariosUpdate = capability.Descriptor{
	ID:      Provider + ".scenarios.update",
	Version: 1,
	Title:   "Replace parts of a Make scenario",
	Description: "Replace one scenario's name, blueprint, scheduling, or folder in the bound team; only the " +
		"fields given are changed, exactly as Make's own partial PATCH is. Never changes the active state; " +
		"scenarios.start and scenarios.stop own that",
	Tags:     []string{"make", "scenarios", "update", "automation"},
	Risk:     makeChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"scenario_id":` + idSchema + `,` +
		`"name":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxScenarioNameLength) + `},` +
		`"blueprint":{"type":"object"},"scheduling":` + schedulingWriteSchema + `,` +
		`"folder_id":` + idSchema + `,"clear_folder":{"type":"boolean"},` +
		`"confirm_new_app":{"type":"boolean"}},"required":["scenario_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(scenarioSummarySchema),
	Arguments: []capability.Argument{scenarioIDArgument,
		{Name: "name", Description: "New scenario name, 1 to " + strconv.Itoa(maxScenarioNameLength) + " characters"},
		{Name: "blueprint", Description: "Replacement blueprint, as a JSON object; Make validates every " +
			"module and any connection, key, or webhook reference itself"},
		{Name: "scheduling", Description: "Replacement scheduling configuration, a JSON object with at least " +
			"a \"type\" field"},
		{Name: "folder_id", Description: "Folder to move the scenario into; mutually exclusive with clear_folder"},
		{Name: "clear_folder", Description: "Set true to remove the scenario's folder assignment; mutually " +
			"exclusive with folder_id"},
		{Name: "confirm_new_app", Description: "Set true to confirm this update when the replacement " +
			"blueprint uses an app for the first time in this organization"},
	},
	Fields: scenarioSummaryFields,
	Examples: []capability.Example{{Description: "Rename a scenario",
		Arguments: json.RawMessage(`{"scenario_id":1,"name":"Renamed"}`)}},
}

type scenariosUpdateArguments struct {
	ScenarioID    int64           `json:"scenario_id"`
	Name          string          `json:"name"`
	Blueprint     json.RawMessage `json:"blueprint"`
	Scheduling    json.RawMessage `json:"scheduling"`
	FolderID      *int64          `json:"folder_id"`
	ClearFolder   bool            `json:"clear_folder"`
	ConfirmNewApp bool            `json:"confirm_new_app"`
}

func invokeScenariosUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update scenario"
	var input scenariosUpdateArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectScenario(resolved, input.ScenarioID); err != nil {
		return nil, err
	}
	if input.FolderID != nil && input.ClearFolder {
		return nil, invalidRequest("folder_id and clear_folder cannot both be set")
	}
	if input.Name == "" && len(input.Blueprint) == 0 && len(input.Scheduling) == 0 && input.FolderID == nil &&
		!input.ClearFolder {
		return nil, invalidRequest("scenarios.update needs at least one of name, blueprint, scheduling, " +
			"folder_id, or clear_folder to change")
	}
	if len(input.Blueprint) > 0 {
		if err := validJSONObject(input.Blueprint, maxBlueprintWriteBytes, maxBlueprintDepth, "blueprint"); err != nil {
			return nil, err
		}
	}
	if len(input.Scheduling) > 0 {
		if err := validJSONObject(input.Scheduling, maxSchedulingWriteBytes, maxSchedulingDepth, "scheduling"); err != nil {
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
	return client.UpdateScenario(ctx, input.ScenarioID, input.Name, input.Blueprint, input.Scheduling,
		input.FolderID, input.ClearFolder, input.ConfirmNewApp)
}

// UpdateScenario sends the one changing PATCH /scenarios/{id} request with only the fields the caller gave,
// blueprint and scheduling encoded to the JSON strings Make's own request body wants (see the package doc),
// then re-reads the scenario, exactly as every other change of this provider does, to answer with the same
// scope-checked summary scenarios.get would and to defensively re-apply this connection's targets to it.
func (c *Client) UpdateScenario(ctx context.Context, scenarioID int64, name string, blueprint, scheduling json.RawMessage,
	folderID *int64, clearFolder, confirmNewApp bool) (*ScenarioSummary, error) {
	const op = "update scenario"
	body := map[string]any{}
	if name != "" {
		body["name"] = name
	}
	if len(blueprint) > 0 {
		body["blueprint"] = string(blueprint)
	}
	if len(scheduling) > 0 {
		body["scheduling"] = string(scheduling)
	}
	switch {
	case clearFolder:
		body["folderId"] = nil
	case folderID != nil:
		body["folderId"] = *folderID
	}
	var query url.Values
	if confirmNewApp {
		query = url.Values{"confirmed": {"true"}}
	}
	if err := c.change(ctx, op, http.MethodPatch, "/scenarios/"+strconv.FormatInt(scenarioID, 10), query, body,
		nil, needWrite, uncertain); err != nil {
		return nil, err
	}
	scenario, err := c.fetchScenario(ctx, op, scenarioID)
	if err != nil {
		return nil, err
	}
	if err := c.verifyScopeAfterChange(op, scenario); err != nil {
		return nil, err
	}
	summary := summaryOf(*scenario)
	return &summary, nil
}

// scenarioActivationDescriptor builds the shared shape of scenarios.start and scenarios.stop: both take only
// scenario_id and answer the same re-read ScenarioSummary scenarios.get would.
func scenarioActivationDescriptor(action, title, description string) capability.Descriptor {
	return capability.Descriptor{
		ID:          Provider + ".scenarios." + action,
		Version:     1,
		Title:       title,
		Description: description,
		Tags:        []string{"make", "scenarios", action, "automation"},
		Risk:        makeChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		Provider:    Provider,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"scenario_id":` + idSchema + `},` +
			`"required":["scenario_id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(scenarioSummarySchema),
		Arguments:    []capability.Argument{scenarioIDArgument},
		Fields:       scenarioSummaryFields,
		Examples:     []capability.Example{{Description: title, Arguments: json.RawMessage(`{"scenario_id":1}`)}},
	}
}

var scenariosStart = scenarioActivationDescriptor("start", "Start a Make scenario",
	"Turn on one scenario's scheduling in the bound team, so its triggers run automatically again; "+
		"repeating it on an already active scenario leaves it active. Does not itself run the scenario; "+
		"scenarios.run does that on demand, independent of the scheduling this tool controls")

var scenariosStop = scenarioActivationDescriptor("stop", "Stop a Make scenario",
	"Turn off one scenario's scheduling in the bound team; repeating it on an already inactive scenario "+
		"leaves it inactive")

func invokeScenariosStart(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeScenarioActivation(ctx, resolved, secrets, red, raw, true)
}

func invokeScenariosStop(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeScenarioActivation(ctx, resolved, secrets, red, raw, false)
}

func invokeScenarioActivation(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, active bool) (any, error) {
	op := "stop scenario"
	if active {
		op = "start scenario"
	}
	var input scenarioArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectScenario(resolved, input.ScenarioID); err != nil {
		return nil, err
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
	return client.SetScenarioActive(ctx, input.ScenarioID, active)
}

// SetScenarioActive sends the one changing POST to Make's start or stop endpoint, then re-reads the scenario
// to confirm the active state actually changed, to answer with the same scope-checked summary scenarios.get
// would, and to defensively re-apply this connection's targets to it.
func (c *Client) SetScenarioActive(ctx context.Context, scenarioID int64, active bool) (*ScenarioSummary, error) {
	op, suffix := "stop scenario", "/stop"
	if active {
		op, suffix = "start scenario", "/start"
	}
	if err := c.change(ctx, op, http.MethodPost, "/scenarios/"+strconv.FormatInt(scenarioID, 10)+suffix, nil,
		nil, nil, needWrite, uncertain); err != nil {
		return nil, err
	}
	scenario, err := c.fetchScenario(ctx, op, scenarioID)
	if err != nil {
		return nil, err
	}
	if err := c.verifyScopeAfterChange(op, scenario); err != nil {
		return nil, err
	}
	if scenario.IsActive != active {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "Make did not report the requested active state after the change" + uncertain}
	}
	summary := summaryOf(*scenario)
	return &summary, nil
}
