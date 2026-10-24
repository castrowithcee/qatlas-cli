package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	goalsSensitive = "matomo-goals"
	// Matomo rejects longer values instead of truncating them.
	maxGoalNameLength = 50
	maxGoalTextLength = 255
	maxGoalRevenue    = 1e12
	maxGoalIDDigits   = 9
)

// Match attributes and pattern types of Goals.addGoal and Goals.updateGoal. visit_duration is the only numeric
// attribute and takes greater_than; the event attributes are the only ones that can use the event value as
// revenue; manually takes no pattern.
var (
	goalMatchAttributes = []string{"url", "title", "file", "external_website", "manually", "visit_duration",
		"event_action", "event_category", "event_name"}
	goalPatternTypes = []string{"exact", "contains", "regex", "greater_than"}
	goalEventMatches = []string{"event_action", "event_category", "event_name"}
)

func goalsRequirement(tool string) requirement { return requirement{tool: tool, plugin: "Goals"} }

// goalChangeRisk is the risk of every goal change: confirmed, open to the instance, and about the goal
// configuration of a website.
func goalChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: goalsSensitive}
}

// sentOnceFor is the note on a change whose outcome is unclear: it names the read that settles it.
func sentOnceFor(noun, tool string) string {
	return "; sent once and never repeated by Qatlas: after an unclear result, read the " + noun + " with " +
		tool + " before repeating it"
}

var goalSentOnce = sentOnceFor("goal", "matomo.goals.get")

const goalProperties = `"id":{"type":"integer"},"name":{"type":"string"},"description":{"type":"string"},` +
	`"match_attribute":{"type":"string"},"pattern":{"type":"string"},"pattern_type":{"type":"string"},` +
	`"case_sensitive":{"type":"boolean"},"allow_multiple":{"type":"boolean"},"revenue":{"type":"number"},` +
	`"event_value_as_revenue":{"type":"boolean"},"deleted":{"type":"boolean"}`

func goalFields(prefix string) []capability.Field {
	return []capability.Field{
		{Name: prefix + "id", Description: "Goal ID, used as idGoal by the other tools; it counts per website"},
		{Name: prefix + "name", Description: "Goal name, untrusted"},
		{Name: prefix + "description", Description: "Goal description, untrusted"},
		{Name: prefix + "match_attribute", Description: "What a conversion is matched on: url, title, file, " +
			"external_website, manually, visit_duration, event_action, event_category, or event_name"},
		{Name: prefix + "pattern", Description: "Match pattern, untrusted; absent for a goal matched manually"},
		{Name: prefix + "pattern_type", Description: "How the pattern matches: exact, contains, regex, or " +
			"greater_than; absent for a goal matched manually"},
		{Name: prefix + "case_sensitive", Description: "True when a string match regards case; absent for a goal " +
			"matched manually"},
		{Name: prefix + "allow_multiple", Description: "True when a visit may convert more than once"},
		{Name: prefix + "revenue", Description: "Default revenue of a conversion, in the currency of the website"},
		{Name: prefix + "event_value_as_revenue", Description: "True when the tracked event value is the revenue of " +
			"a conversion"},
		{Name: prefix + "deleted", Description: "Deletion flag; a goal that is read is never deleted"},
	}
}

func idGoalArgument(verb string) capability.Argument {
	return capability.Argument{Name: "idGoal", Required: true, Description: "ID of the goal to " + verb +
		", from matomo.goals.list; it counts per website, and the ecommerce values ecommerceOrder and " +
		"ecommerceAbandonedCart are no goals"}
}

const idGoalSchema = `"idGoal":{"type":"integer","minimum":1,"maximum":999999999}`

// goalDefinitionSchema holds the members that define a goal; Goals.addGoal and Goals.updateGoal share them.
var goalDefinitionSchema = `"name":{"type":"string","minLength":1,"maxLength":50},` +
	`"matchAttribute":{"type":"string","enum":` + enumJSON(goalMatchAttributes) + `},` +
	`"pattern":{"type":"string","maxLength":255},` +
	`"patternType":{"type":"string","enum":` + enumJSON(goalPatternTypes) + `},` +
	`"caseSensitive":{"type":"boolean"},"revenue":{"type":"number","minimum":0,"maximum":1000000000000},` +
	`"allowMultipleConversionsPerVisit":{"type":"boolean"},"description":{"type":"string","maxLength":255},` +
	`"useEventValueAsRevenue":{"type":"boolean"}`

func enumJSON(values []string) string {
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

func goalDefinitionArguments(replaces bool) []capability.Argument {
	omitted, reset, clear := "false when omitted", "", ""
	if replaces {
		omitted = "false when omitted, which resets a set value"
		reset, clear = ", which resets a set value", ", which clears a set value"
	}
	return []capability.Argument{
		{Name: "name", Required: true, Description: "Goal name, 1 to 50 characters"},
		{Name: "matchAttribute", Required: true, Description: "What a conversion is matched on: url, title, file, " +
			"external_website, manually, visit_duration (minutes), event_action, event_category, or event_name"},
		{Name: "pattern", Description: "Match pattern, up to 255 characters: required except for manually, where " +
			"it must stay empty; a number of minutes for visit_duration; for url and file with patternType exact it " +
			"must start with http"},
		{Name: "patternType", Description: "How the pattern matches: exact, contains, or regex for the string " +
			"attributes, greater_than for visit_duration; required except for manually, where it must stay empty"},
		{Name: "caseSensitive", Description: "True for a case-sensitive string match; " + omitted},
		{Name: "revenue", Description: "Default revenue of a conversion, 0 to 1000000000000; 0 when omitted" + reset},
		{Name: "allowMultipleConversionsPerVisit", Description: "True when a visit may convert more than once; " + omitted},
		{Name: "description", Description: "Goal description, up to 255 characters; empty when omitted" + clear},
		{Name: "useEventValueAsRevenue", Description: "True to use the tracked event value as revenue; only for " +
			"event_action, event_category, and event_name; " + omitted},
	}
}

var goalsList = capability.Descriptor{
	ID: Provider + ".goals.list", Version: 1, Title: "List Matomo goals",
	Description: "List the goals of a website in batches ordered by ID, or by name with orderByName; names, " +
		"descriptions, and patterns are untrusted",
	Tags: []string{"matomo", "goals", "conversions", "list"}, Risk: readRisk(goalsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,"orderByName":{"type":"boolean"},` +
		pagingKeys + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"goals":{"type":"array",` +
		`"items":{"type":"object","properties":{` + goalProperties + `},"required":["id","name"],` +
		`"additionalProperties":false}},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["idSite","goals","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(), {Name: "orderByName", Description: "True to order " +
		"the goals by name without regard to case instead of by ID"}}, pagingArguments...),
	Fields: append(append([]capability.Field{{Name: "idSite", Description: "The website the goals belong to"},
		{Name: "goals", Description: "Goals of the website"}}, goalFields("goals[].")...), pagingFields...),
	Examples: []capability.Example{{Description: "List the goals of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var goalsGet = capability.Descriptor{
	ID: Provider + ".goals.get", Version: 1, Title: "Get a Matomo goal",
	Description: "Read one goal of a website; the goal ID counts per website, and an ID the website does not have " +
		"is not found; names, descriptions, and patterns are untrusted",
	Tags: []string{"matomo", "goals", "conversions", "get"}, Risk: readRisk(goalsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idGoalSchema + `},` +
		`"required":["idGoal"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` + goalProperties +
		`},"required":["idSite","id","name"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(), idGoalArgument("read")},
	Fields:    append([]capability.Field{{Name: "idSite", Description: "The website the goal belongs to"}}, goalFields("")...),
	Examples:  []capability.Example{{Description: "Read goal 3 of website 1", Arguments: json.RawMessage(`{"idSite":1,"idGoal":3}`)}},
}

var goalsAdd = capability.Descriptor{
	ID: Provider + ".goals.add", Version: 1, Title: "Add a Matomo goal",
	Description: "Create one goal for a website; it needs a write right on it; not idempotent, since a repeated " +
		"call creates a second goal; the goal counts conversions from now on and changes no recorded data; " +
		"sent once and never repeated",
	Tags: []string{"matomo", "goals", "conversions", "create"}, Risk: goalChangeRisk(capability.EffectCreate,
		capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + goalDefinitionSchema + `},` +
		`"required":["name","matchAttribute"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(goalAddedOutput),
	Arguments:    append([]capability.Argument{idSiteArgument()}, goalDefinitionArguments(false)...),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the goal was created for"},
		{Name: "idGoal", Description: "ID of the new goal"},
		{Name: "created", Description: "True once Matomo created the goal"},
	},
	Examples: []capability.Example{{Description: "Count visits to a thank-you page",
		Arguments: json.RawMessage(`{"idSite":1,"name":"Newsletter","matchAttribute":"url","pattern":` +
			`"https://shop.example.test/thanks","patternType":"exact","revenue":5}`)}},
}

const goalAddedOutput = `{"type":"object","properties":{"idSite":{"type":"integer"},"idGoal":{"type":"integer"},` +
	`"created":{"type":"boolean"}},"required":["idSite","idGoal","created"],"additionalProperties":false}`

const goalChangedOutput = `{"type":"object","properties":{"idSite":{"type":"integer"},"idGoal":{"type":"integer"},` +
	`"updated":{"type":"boolean"}},"required":["idSite","idGoal","updated"],"additionalProperties":false}`

const goalDeletedOutput = `{"type":"object","properties":{"idSite":{"type":"integer"},"idGoal":{"type":"integer"},` +
	`"name":{"type":"string"},"deleted":{"type":"boolean"}},"required":["idSite","idGoal","deleted"],` +
	`"additionalProperties":false}`

var goalsUpdate = capability.Descriptor{
	ID: Provider + ".goals.update", Version: 1, Title: "Update a Matomo goal",
	Description: "Replace the definition of one goal of a website as a whole: every value not given falls back to " +
		"its default (no case sensitivity, no revenue, one conversion per visit, no description), so read the goal " +
		"with matomo.goals.get first; it needs a write right; idempotent, since the same definition leaves the " +
		"same state; already recorded conversions are not reprocessed; sent once and never repeated",
	Tags: []string{"matomo", "goals", "conversions", "update"}, Risk: goalChangeRisk(capability.EffectUpdate,
		capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idGoalSchema + `,` +
		goalDefinitionSchema + `},"required":["idGoal","name","matchAttribute"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(goalChangedOutput),
	Arguments: append([]capability.Argument{idSiteArgument(), idGoalArgument("replace")},
		goalDefinitionArguments(true)...),
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the goal belongs to"},
		{Name: "idGoal", Description: "ID of the updated goal"},
		{Name: "updated", Description: "True once Matomo replaced the definition"},
	},
	Examples: []capability.Example{{Description: "Rename a goal and keep its URL match",
		Arguments: json.RawMessage(`{"idSite":1,"idGoal":3,"name":"Newsletter signup","matchAttribute":"url",` +
			`"pattern":"https://shop.example.test/thanks","patternType":"exact","revenue":5}`)}},
}

var goalsDelete = capability.Descriptor{
	ID: Provider + ".goals.delete", Version: 1, Title: "Delete a Matomo goal",
	Description: "Delete one goal of a website. The goal disappears from the website and from its reports, and " +
		"every conversion Matomo recorded for it is deleted for good; reports already archived stay stored but are " +
		"no longer shown, and a goal created again gets a new ID and no conversions. It cannot be undone. The goal " +
		"is read first, so an ID the website does not have is not found; it needs a write right; sent once and " +
		"never repeated; offered only where a connection's tools list names it",
	Tags: []string{"matomo", "goals", "conversions", "delete"}, Risk: goalChangeRisk(capability.EffectDelete,
		capability.IdempotencyIdempotent), Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idGoalSchema + `},` +
		`"required":["idGoal"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(goalDeletedOutput),
	Arguments:    []capability.Argument{idSiteArgument(), idGoalArgument("delete")},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the goal belonged to"},
		{Name: "idGoal", Description: "ID of the deleted goal"},
		{Name: "name", Description: "Name the goal had, untrusted"},
		{Name: "deleted", Description: "True once Matomo deleted the goal and its conversions"},
	},
	Examples: []capability.Example{{Description: "Delete goal 3 of website 1", Arguments: json.RawMessage(`{"idSite":1,"idGoal":3}`)}},
}

// goalJSON is a goal as Goals.getGoal reports it. Every member is read loosely and none is trusted.
type goalJSON struct {
	ID                  *loose `json:"idgoal"`
	IDSite              *loose `json:"idsite"`
	Name                *loose `json:"name"`
	Description         *loose `json:"description"`
	MatchAttribute      *loose `json:"match_attribute"`
	Pattern             *loose `json:"pattern"`
	PatternType         *loose `json:"pattern_type"`
	CaseSensitive       *loose `json:"case_sensitive"`
	AllowMultiple       *loose `json:"allow_multiple"`
	Revenue             *loose `json:"revenue"`
	EventValueAsRevenue *loose `json:"event_value_as_revenue"`
	Deleted             *loose `json:"deleted"`
}

// Goal is a goal of a website. All text is untrusted provider data.
type Goal struct {
	ID                  int      `json:"id"`
	Name                string   `json:"name"`
	Description         string   `json:"description,omitempty"`
	MatchAttribute      string   `json:"match_attribute,omitempty"`
	Pattern             string   `json:"pattern,omitempty"`
	PatternType         string   `json:"pattern_type,omitempty"`
	CaseSensitive       *bool    `json:"case_sensitive,omitempty"`
	AllowMultiple       *bool    `json:"allow_multiple,omitempty"`
	Revenue             *float64 `json:"revenue,omitempty"`
	EventValueAsRevenue *bool    `json:"event_value_as_revenue,omitempty"`
	Deleted             *bool    `json:"deleted,omitempty"`
}

// numeric reads a Matomo number that arrives as a string or a number; a value that is not finite is absent.
func (l *loose) numeric() *float64 {
	if l == nil {
		return nil
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(l.text), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

// goal converts a row; ok is false for a row without a usable goal ID or from another website than id.
func (g goalJSON) goal(site int) (Goal, bool) {
	id := g.ID.number()
	if id == nil || *id < 1 {
		return Goal{}, false
	}
	if row := g.IDSite.number(); row != nil && *row != site {
		return Goal{}, false
	}
	return Goal{
		ID: *id, Name: g.Name.str(maxStringLength), Description: g.Description.str(maxStringLength),
		MatchAttribute: g.MatchAttribute.str(maxStringLength), Pattern: g.Pattern.str(maxStringLength),
		PatternType: g.PatternType.str(maxStringLength), CaseSensitive: g.CaseSensitive.flag(),
		AllowMultiple: g.AllowMultiple.flag(), Revenue: g.Revenue.numeric(),
		EventValueAsRevenue: g.EventValueAsRevenue.flag(), Deleted: g.Deleted.flag(),
	}, true
}

// GoalsResult is the answer of goals.list.
type GoalsResult struct {
	IDSite     int    `json:"idSite"`
	Goals      []Goal `json:"goals"`
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// GoalResult is the answer of goals.get.
type GoalResult struct {
	IDSite int `json:"idSite"`
	Goal
}

// GoalAdded is the answer of goals.add.
type GoalAdded struct {
	IDSite  int  `json:"idSite"`
	IDGoal  int  `json:"idGoal"`
	Created bool `json:"created"`
}

// GoalUpdated is the answer of goals.update.
type GoalUpdated struct {
	IDSite  int  `json:"idSite"`
	IDGoal  int  `json:"idGoal"`
	Updated bool `json:"updated"`
}

// GoalDeleted is the answer of goals.delete.
type GoalDeleted struct {
	IDSite  int    `json:"idSite"`
	IDGoal  int    `json:"idGoal"`
	Name    string `json:"name,omitempty"`
	Deleted bool   `json:"deleted"`
}

// readGoals reads the answer of Goals.getGoals for one website: a list, or an object keyed by goal ID.
func readGoals(raw json.RawMessage, site int) ([]Goal, bool) {
	var rows []goalJSON
	raw = bytes.TrimSpace(raw)
	switch {
	case bytes.HasPrefix(raw, []byte("[")):
		if json.Unmarshal(raw, &rows) != nil {
			return nil, false
		}
	case bytes.HasPrefix(raw, []byte("{")):
		var keyed map[string]goalJSON
		if json.Unmarshal(raw, &keyed) != nil {
			return nil, false
		}
		for _, row := range keyed {
			rows = append(rows, row)
		}
	default:
		return nil, false
	}
	goals := []Goal{}
	for _, row := range rows {
		if goal, ok := row.goal(site); ok {
			goals = append(goals, goal)
		}
	}
	return goals, true
}

func invokeGoalsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list goals"
	var args struct {
		IDSite      *int   `json:"idSite"`
		OrderByName bool   `json:"orderByName"`
		Limit       int    `json:"limit"`
		Cursor      string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	binding := provider.CursorBinding(goalsList.ID, scopeBinding(resolved), id, args.OrderByName)
	paging, err := startPage(binding, args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}}
	if args.OrderByName {
		params.Set("orderByName", "1")
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, goalsRequirement(goalsList.ID), "Goals.getGoals", params, &answer); err != nil {
		return nil, err
	}
	goals, ok := readGoals(answer, id)
	if !ok {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid goal list")
	}
	slices.SortStableFunc(goals, func(a, b Goal) int {
		if args.OrderByName {
			if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
				return c
			}
			return b.ID - a.ID
		}
		return a.ID - b.ID
	})
	batch, next := slice(paging, goals)
	if batch == nil {
		batch = []Goal{}
	}
	return &GoalsResult{IDSite: id, Goals: batch, HasMore: next != "", NextCursor: next}, nil
}

// fetchGoal reads one goal of the website. Matomo answers a goal the website does not have with a success
// answer without a goal, which is not found here.
func (c *Client) fetchGoal(ctx context.Context, op string, req requirement, site, goal int) (Goal, error) {
	var answer json.RawMessage
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idGoal": {strconv.Itoa(goal)}}
	if err := c.call(ctx, op, req, "Goals.getGoal", params, &answer); err != nil {
		return Goal{}, err
	}
	var row goalJSON
	answer = bytes.TrimSpace(answer)
	if !bytes.HasPrefix(answer, []byte("{")) || json.Unmarshal(answer, &row) != nil {
		return Goal{}, provider.InvalidResponse(op, "Matomo returned an invalid goal")
	}
	if row.ID == nil {
		if isSuccessAnswer(answer) {
			return Goal{}, &provider.Error{Class: provider.ClassNotFound, Op: op,
				Message: "Matomo has no goal with this ID for this website"}
		}
		return Goal{}, provider.InvalidResponse(op, "Matomo returned an invalid goal")
	}
	found, ok := row.goal(site)
	if !ok || found.ID != goal {
		return Goal{}, provider.InvalidResponse(op, "Matomo returned another goal than asked for")
	}
	return found, nil
}

// isSuccessAnswer recognises {"result":"success"}, the form Matomo uses for a method that returns nothing.
func isSuccessAnswer(data []byte) bool {
	var answer struct {
		Result string `json:"result"`
	}
	return json.Unmarshal(data, &answer) == nil && answer.Result == "success"
}

func invokeGoalsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get goal"
	var args struct {
		IDSite *int `json:"idSite"`
		IDGoal *int `json:"idGoal"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	goalID, err := goalIDOf(args.IDGoal)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	goal, err := client.fetchGoal(ctx, op, goalsRequirement(goalsGet.ID), id, goalID)
	if err != nil {
		return nil, err
	}
	return &GoalResult{IDSite: id, Goal: goal}, nil
}

func goalIDOf(given *int) (int, error) {
	if given == nil || *given < 1 || len(strconv.Itoa(*given)) > maxGoalIDDigits {
		return 0, invalidArgument("idGoal must be a positive integer goal ID; ecommerce values are no goals")
	}
	return *given, nil
}

type goalDefinition struct {
	Name                   *string  `json:"name"`
	MatchAttribute         string   `json:"matchAttribute"`
	Pattern                string   `json:"pattern"`
	PatternType            string   `json:"patternType"`
	CaseSensitive          *bool    `json:"caseSensitive"`
	Revenue                *float64 `json:"revenue"`
	AllowMultiple          *bool    `json:"allowMultipleConversionsPerVisit"`
	Description            string   `json:"description"`
	UseEventValueAsRevenue *bool    `json:"useEventValueAsRevenue"`
}

func flagValue(value *bool) string {
	if value != nil && *value {
		return "1"
	}
	return "0"
}

// params checks the definition the way Matomo does, so a request that Matomo would refuse is not sent, and
// builds the form values. Matomo stays the authority: it checks again, and its answer decides.
func (d goalDefinition) params() (url.Values, error) {
	if d.Name == nil || strings.TrimSpace(*d.Name) == "" || utf8.RuneCountInString(*d.Name) > maxGoalNameLength {
		return nil, invalidArgument("name must hold 1 to 50 characters")
	}
	if !slices.Contains(goalMatchAttributes, d.MatchAttribute) {
		return nil, invalidArgument("matchAttribute must be one of " + strings.Join(goalMatchAttributes, ", "))
	}
	if utf8.RuneCountInString(d.Pattern) > maxGoalTextLength || utf8.RuneCountInString(d.Description) > maxGoalTextLength {
		return nil, invalidArgument("pattern and description must hold at most 255 characters")
	}
	switch {
	case d.MatchAttribute == "manually":
		if d.Pattern != "" || d.PatternType != "" {
			return nil, invalidArgument("a goal matched manually takes no pattern and no patternType")
		}
	case d.Pattern == "" || d.PatternType == "":
		return nil, invalidArgument("pattern and patternType are required unless matchAttribute is manually")
	case d.MatchAttribute == "visit_duration":
		if d.PatternType != "greater_than" {
			return nil, invalidArgument("visit_duration takes the patternType greater_than")
		}
		if value, err := strconv.ParseFloat(d.Pattern, 64); err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, invalidArgument("pattern must be a number of minutes for visit_duration")
		}
	case !slices.Contains(goalPatternTypes[:3], d.PatternType):
		return nil, invalidArgument("patternType must be exact, contains, or regex for this matchAttribute")
	}
	if d.Revenue != nil && (math.IsNaN(*d.Revenue) || *d.Revenue < 0 || *d.Revenue > maxGoalRevenue) {
		return nil, invalidArgument("revenue must be between 0 and 1000000000000")
	}
	if d.UseEventValueAsRevenue != nil && *d.UseEventValueAsRevenue && !slices.Contains(goalEventMatches, d.MatchAttribute) {
		return nil, invalidArgument("useEventValueAsRevenue is only valid for event_action, event_category, and event_name")
	}
	revenue := "0"
	if d.Revenue != nil {
		revenue = strconv.FormatFloat(*d.Revenue, 'f', -1, 64)
	}
	return url.Values{
		"name": {*d.Name}, "matchAttribute": {d.MatchAttribute}, "pattern": {d.Pattern}, "patternType": {d.PatternType},
		"caseSensitive": {flagValue(d.CaseSensitive)}, "revenue": {revenue},
		"allowMultipleConversionsPerVisit": {flagValue(d.AllowMultiple)}, "description": {d.Description},
		"useEventValueAsRevenue": {flagValue(d.UseEventValueAsRevenue)},
	}, nil
}

// change sends the one request of a mutation after the instance is proven to serve the tool. It never
// repeats: a failure after which the request may have been applied says so.
func (c *Client) change(ctx context.Context, op string, req requirement, method string, params url.Values,
	hint string, out any) error {
	info, err := c.ensure(ctx, op, req)
	if err != nil {
		return err
	}
	return uncertainChange(c.refuseMissing(req, c.send(ctx, op, method, params, info.bodyToken, out)), hint)
}

// uncertainChange marks a failure of a sent change whose outcome is unknown: no answer in time, a reset
// connection, an HTTP status Qatlas does not classify, or an answer that cannot be read. An error Matomo
// reported itself in a recognised form is a clean refusal and stays as it is.
func uncertainChange(err error, hint string) error {
	var failure *provider.Error
	if !errors.As(err, &failure) {
		return err
	}
	switch {
	case failure.MayHaveArrived(), failure.Class == provider.ClassInvalidResponse,
		failure.Class == provider.ClassProviderError:
		marked := *failure
		marked.Message += hint
		return &marked
	}
	return err
}

func unclearChange(op, message, hint string) error {
	return provider.InvalidResponse(op, message+hint)
}

func invokeGoalsAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add goal"
	var args struct {
		IDSite *int `json:"idSite"`
		goalDefinition
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	params, err := args.params()
	if err != nil {
		return nil, err
	}
	params.Set("idSite", strconv.Itoa(id))
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, goalsRequirement(goalsAdd.ID), "Goals.addGoal", params, goalSentOnce, &answer); err != nil {
		return nil, err
	}
	var created int
	if scalar(answer, &created) != nil || created < 1 {
		return nil, unclearChange(op, "Matomo returned no usable ID for the new goal", goalSentOnce)
	}
	return &GoalAdded{IDSite: id, IDGoal: created, Created: true}, nil
}

func invokeGoalsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update goal"
	var args struct {
		IDSite *int `json:"idSite"`
		IDGoal *int `json:"idGoal"`
		goalDefinition
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	goalID, err := goalIDOf(args.IDGoal)
	if err != nil {
		return nil, err
	}
	params, err := args.params()
	if err != nil {
		return nil, err
	}
	params.Set("idSite", strconv.Itoa(id))
	params.Set("idGoal", strconv.Itoa(goalID))
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	// Matomo checks the pair itself and answers an ID the website does not have as an error, so no read
	// comes first.
	var answer json.RawMessage
	if err := client.change(ctx, op, goalsRequirement(goalsUpdate.ID), "Goals.updateGoal", params, goalSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the update", goalSentOnce)
	}
	return &GoalUpdated{IDSite: id, IDGoal: goalID, Updated: true}, nil
}

func invokeGoalsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete goal"
	var args struct {
		IDSite *int `json:"idSite"`
		IDGoal *int `json:"idGoal"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	goalID, err := goalIDOf(args.IDGoal)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	// Matomo pairs the website and the goal, but answers a delete of an ID the website does not have with
	// success and nothing else. One read proves the goal and names it before anything is deleted.
	req := goalsRequirement(goalsDelete.ID)
	goal, err := client.fetchGoal(ctx, op, req, id, goalID)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "idGoal": {strconv.Itoa(goalID)}}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "Goals.deleteGoal", params, goalSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", goalSentOnce)
	}
	return &GoalDeleted{IDSite: id, IDGoal: goalID, Name: goal.Name, Deleted: true}, nil
}
