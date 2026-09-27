package makeapi

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
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

var scenariosList = capability.Descriptor{
	ID:      Provider + ".scenarios.list",
	Version: 1,
	Title:   "List Make scenarios",
	Description: "List the scenarios of the connection's bound team, restricted to its scenario allow-list " +
		"when it has one; page by page with a numeric offset",
	Tags:                       []string{"make", "scenarios", "list", "automation"},
	Risk:                       makeReadRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
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
	ID:                         Provider + ".scenarios.get",
	Version:                    1,
	Title:                      "Get a Make scenario",
	Description:                "Read one scenario of the bound team's own report",
	Tags:                       []string{"make", "scenarios", "get", "automation"},
	Risk:                       makeReadRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
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
	if err := c.get(ctx, op, "/scenarios", query, &page); err != nil {
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
	if err := c.get(ctx, op, "/scenarios/"+strconv.FormatInt(scenarioID, 10), nil, &wrapper); err != nil {
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

var scenariosBlueprint = capability.Descriptor{
	ID:      Provider + ".scenarios.blueprint",
	Version: 1,
	Title:   "Get a Make scenario's blueprint",
	Description: "Read one scenario's blueprint (its modules, their wiring, and their configuration); a " +
		"connection or key a module references is passed through only as the numeric id Make itself reports, " +
		"never a stored authorization value, which Make's own blueprint format never carries in the first place",
	Tags:                       []string{"make", "scenarios", "blueprint", "automation"},
	Risk:                       makeReadRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
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
	if err := c.get(ctx, op, "/scenarios/"+strconv.FormatInt(scenarioID, 10)+"/blueprint", query, &wrapper); err != nil {
		return nil, err
	}
	if len(wrapper.Blueprint) == 0 {
		return nil, invalidResponse(op, "Make did not report a blueprint")
	}
	return &Blueprint{ScenarioID: scenarioID, Blueprint: wrapper.Blueprint}, nil
}
