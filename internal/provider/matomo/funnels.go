package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"sort"
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
	// funnelsSensitive covers the names, steps, and URL patterns people wrote into funnels.
	funnelsSensitive = "matomo-funnels"
	funnelsPlugin    = "Funnels"
	maxFunnelSteps   = 100
	// maxTestSteps bounds the steps one test call sends.
	maxTestSteps       = 20
	maxFunnelID        = 999999999
	maxResultEntries   = 100
	maxResultDepth     = 5
	patternTypePattern = `^[a-z][a-z_]{0,63}$`
)

func funnelsRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: funnelsPlugin}
}

const (
	idFunnelSchema = `"idFunnel":{"type":"integer","minimum":1,"maximum":999999999}`
	idGoalFunnel   = `"idGoal":{"type":"integer","minimum":0,"maximum":999999999}`
)

const funnelStepProperties = `"position":{"type":"integer"},"name":{"type":"string"},"pattern_type":{"type":"string"},` +
	`"pattern":{"type":"string"},"required":{"type":"boolean"},"pattern_comparison":{"type":"string"}`

const funnelProperties = `"idFunnel":{"type":"integer"},"idSite":{"type":"integer"},"idGoal":{"type":"integer"},` +
	`"revision":{"type":"integer"},"name":{"type":"string"},"description":{"type":"string"},` +
	`"created_date":{"type":"string"},"activated":{"type":"boolean"},"steps":{"type":"array","items":` +
	`{"type":"object","properties":{` + funnelStepProperties + `},"additionalProperties":false}},` +
	`"is_sales_funnel":{"type":"boolean"},"final_step_position":{"type":"integer"}`

func funnelFields(prefix string) []capability.Field {
	return []capability.Field{
		{Name: prefix + "idFunnel", Description: "Funnel ID, used as idFunnel by matomo.funnels.get"},
		{Name: prefix + "idSite", Description: "The website the funnel belongs to"},
		{Name: prefix + "idGoal", Description: "ID of the goal the funnel belongs to; 0 for the sales funnel; absent " +
			"for a funnel without a goal"},
		{Name: prefix + "revision", Description: "Revision of the funnel definition as Matomo counts it"},
		{Name: prefix + "name", Description: "Funnel name, untrusted"},
		{Name: prefix + "description", Description: "Funnel description, untrusted"},
		{Name: prefix + "created_date", Description: "Time the funnel was created as Matomo stores it"},
		{Name: prefix + "activated", Description: "True when the funnel is activated and reports"},
		{Name: prefix + "steps", Description: "Steps of the funnel in the order of Matomo, at most 100"},
		{Name: prefix + "steps[].position", Description: "Position of the step in the funnel"},
		{Name: prefix + "steps[].name", Description: "Step name, untrusted"},
		{Name: prefix + "steps[].pattern_type", Description: "How the pattern matches, such as path_equals or " +
			"eventname_equals; the values come from matomo.funnel-options.list"},
		{Name: prefix + "steps[].pattern", Description: "Pattern a step matches, untrusted; text that is never fetched"},
		{Name: prefix + "steps[].required", Description: "True when a visit must pass the step to count"},
		{Name: prefix + "steps[].pattern_comparison", Description: "What the pattern is compared with, such as path " +
			"or eventname"},
		{Name: prefix + "is_sales_funnel", Description: "True for the sales funnel of the website"},
		{Name: prefix + "final_step_position", Description: "Position of the goal conversion after the last step"},
	}
}

func funnelSiteArgument() capability.Argument { return idSiteArgument() }

var funnelsList = capability.Descriptor{
	ID: Provider + ".funnels.list", Version: 1, Title: "List Matomo funnels",
	Description: "List the activated funnels of a website with their steps, in batches, after a check that the " +
		"website has any activated funnel; deactivated funnels are not listed. Names and patterns are untrusted. " +
		"Reports of a funnel are read with matomo.reports.get. Needs the plugin Funnels and a view right on the website",
	Tags: []string{"matomo", "funnels", "conversions", "list"}, Risk: readRisk(funnelsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + pagingKeys + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"has_activated_funnel":{"type":"boolean"},"funnels":{"type":"array","items":{"type":"object",` +
		`"properties":{` + funnelProperties + `},"required":["idFunnel","idSite","steps"],` +
		`"additionalProperties":false}},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["idSite","has_activated_funnel","funnels","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{funnelSiteArgument()}, pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "idSite", Description: "The website the funnels belong to"},
		{Name: "has_activated_funnel", Description: "True when the website has at least one activated funnel"},
		{Name: "funnels", Description: "Activated funnels in the order Matomo lists them; empty without one"}},
		funnelFields("funnels[].")...), pagingFields...),
	Examples: []capability.Example{{Description: "Funnels of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var funnelsGet = capability.Descriptor{
	ID: Provider + ".funnels.get", Version: 1, Title: "Get a Matomo funnel",
	Description: "Read the definition of one funnel of a website, chosen by exactly one of idFunnel (a funnel by ID) " +
		"and idGoal (the funnel of a goal; 0 for the sales funnel). The funnel ID counts across websites, so the " +
		"answer is shown only when Matomo reports it for this website, otherwise it is not found. A website without " +
		"such a funnel is not found. Names and patterns are untrusted. Needs the plugin Funnels and a view right on " +
		"the website",
	Tags: []string{"matomo", "funnels", "conversions", "get"}, Risk: readRisk(funnelsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idFunnelSchema + `,` +
		idGoalFunnel + `},"oneOf":[{"required":["idFunnel"]},{"required":["idGoal"]}],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + funnelProperties + `},` +
		`"required":["idFunnel","idSite","steps"],"additionalProperties":false}`),
	Arguments: []capability.Argument{funnelSiteArgument(),
		{Name: "idFunnel", Description: "ID of the funnel to read, from matomo.funnels.list; give this or idGoal, " +
			"not both"},
		{Name: "idGoal", Description: "ID of the goal whose funnel to read, from matomo.goals.list; 0 reads the " +
			"sales funnel; give this or idFunnel, not both"}},
	Fields: funnelFields(""),
	Examples: []capability.Example{
		{Description: "Read funnel 7 of website 1", Arguments: json.RawMessage(`{"idSite":1,"idFunnel":7}`)},
		{Description: "Read the sales funnel of website 1", Arguments: json.RawMessage(`{"idSite":1,"idGoal":0}`)}},
}

var funnelsTestSteps = capability.Descriptor{
	ID: Provider + ".funnels.test-steps", Version: 1, Title: "Test a URL against Matomo funnel steps",
	Description: "Ask Matomo whether a URL or comparable value matches the patterns of given funnel steps. Nothing " +
		"is stored and the value is never fetched; it is sent to Matomo as text. The steps are given here, not read " +
		"from a funnel. The answer is returned as Matomo gives it, shortened. Needs the plugin Funnels and a " +
		"connection that binds a website",
	Tags: []string{"matomo", "funnels", "patterns", "test"}, Risk: readRisk(funnelsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","minLength":1,` +
		`"maxLength":2048},"steps":` + funnelStepsSchema(1, maxTestSteps) + `},` +
		`"required":["url","steps"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"result":{}},"required":["result"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "url", Required: true, Description: "The URL or comparable value to test, up to 2048 characters"},
		{Name: "steps", Required: true, Description: "1 to 20 step definitions to test against; each holds " +
			"pattern_type (a value from matomo.funnel-options.list) and pattern, and optionally name and required"}},
	Fields: []capability.Field{{Name: "result", Description: "Matomo's answer: the tested value and one result " +
		"entry per step pattern, untrusted; strings, lists, and nesting are shortened"}},
	Examples: []capability.Example{{Description: "Test a pricing URL against one step", Arguments: json.RawMessage(
		`{"url":"https://example.org/pricing","steps":[{"pattern_type":"path_equals","pattern":"/pricing"}]}`)}},
}

var funnelOptionsList = capability.Descriptor{
	ID: Provider + ".funnel-options.list", Version: 1, Title: "List Matomo funnel pattern types",
	Description: "List the pattern match types a funnel step can use, grouped by comparison category, as Matomo " +
		"gives them. Reads no website data. Needs the plugin Funnels and a connection that binds a website",
	Tags: []string{"matomo", "funnels", "patterns", "reference"}, Risk: readRisk(funnelsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"pattern_matches":{}},` +
		`"required":["pattern_matches"],"additionalProperties":false}`),
	Fields: []capability.Field{{Name: "pattern_matches", Description: "Pattern types grouped by comparison " +
		"category, as Matomo gives them; strings, lists, and nesting are shortened"}},
	Examples: []capability.Example{{Description: "List the pattern types", Arguments: json.RawMessage(`{}`)}},
}

// FunnelStep is one step of a funnel. All text is untrusted provider data.
type FunnelStep struct {
	Position          *int   `json:"position,omitempty"`
	Name              string `json:"name,omitempty"`
	PatternType       string `json:"pattern_type,omitempty"`
	Pattern           string `json:"pattern,omitempty"`
	Required          *bool  `json:"required,omitempty"`
	PatternComparison string `json:"pattern_comparison,omitempty"`
}

// Funnel is a funnel of a website. All text is untrusted provider data.
type Funnel struct {
	IDFunnel          int          `json:"idFunnel"`
	IDSite            int          `json:"idSite"`
	IDGoal            *int         `json:"idGoal,omitempty"`
	Revision          *int         `json:"revision,omitempty"`
	Name              string       `json:"name,omitempty"`
	Description       string       `json:"description,omitempty"`
	CreatedDate       string       `json:"created_date,omitempty"`
	Activated         *bool        `json:"activated,omitempty"`
	Steps             []FunnelStep `json:"steps"`
	IsSalesFunnel     *bool        `json:"is_sales_funnel,omitempty"`
	FinalStepPosition *int         `json:"final_step_position,omitempty"`
}

// FunnelsResult is the answer of funnels.list.
type FunnelsResult struct {
	IDSite             int      `json:"idSite"`
	HasActivatedFunnel bool     `json:"has_activated_funnel"`
	Funnels            []Funnel `json:"funnels"`
	HasMore            bool     `json:"has_more"`
	NextCursor         string   `json:"next_cursor,omitempty"`
}

// FunnelTestResult is the answer of funnels.test-steps.
type FunnelTestResult struct {
	Result any `json:"result"`
}

// FunnelOptionsResult is the answer of funnel-options.list.
type FunnelOptionsResult struct {
	PatternMatches any `json:"pattern_matches"`
}

// funnelStepJSON is a step as Funnels reports it. Every member is read loosely and none is trusted.
type funnelStepJSON struct {
	Position          *loose `json:"position"`
	Name              *loose `json:"name"`
	PatternType       *loose `json:"pattern_type"`
	Pattern           *loose `json:"pattern"`
	Required          *loose `json:"required"`
	PatternComparison *loose `json:"patternComparison"`
}

// funnelJSON is a funnel as Funnels.getFunnel and its siblings report it. The description may be of any JSON
// type, so it is read apart.
type funnelJSON struct {
	ID                *loose          `json:"idfunnel"`
	IDSite            *loose          `json:"idsite"`
	IDGoal            *loose          `json:"idgoal"`
	Revision          *loose          `json:"revision"`
	Name              *loose          `json:"name"`
	Description       json.RawMessage `json:"description"`
	CreatedDate       *loose          `json:"created_date"`
	Activated         *loose          `json:"activated"`
	Steps             json.RawMessage `json:"steps"`
	IsSalesFunnel     *loose          `json:"isSalesFunnel"`
	FinalStepPosition *loose          `json:"final_step_position"`
}

// funnelText reads a JSON scalar as bounded text; anything else is empty.
func funnelText(raw json.RawMessage, limit int) string {
	var value loose
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value.str(limit)
}

// readRows reads a list of objects that Matomo answers as an array, or as an object keyed by position.
func readRows[T any](raw json.RawMessage) ([]T, bool) {
	raw = bytes.TrimSpace(raw)
	var rows []T
	switch {
	case bytes.HasPrefix(raw, []byte("[")):
		if json.Unmarshal(raw, &rows) != nil {
			return nil, false
		}
	case bytes.HasPrefix(raw, []byte("{")):
		var keyed map[string]T
		if json.Unmarshal(raw, &keyed) != nil {
			return nil, false
		}
		keys := make([]string, 0, len(keyed))
		for key := range keyed {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, errA := strconv.Atoi(keys[i])
			b, errB := strconv.Atoi(keys[j])
			if errA == nil && errB == nil {
				return a < b
			}
			return keys[i] < keys[j]
		})
		for _, key := range keys {
			rows = append(rows, keyed[key])
		}
	case bytes.Equal(raw, []byte("null")), len(raw) == 0:
		return nil, true
	default:
		return nil, false
	}
	return rows, true
}

func (s funnelStepJSON) step() FunnelStep {
	return FunnelStep{Position: s.Position.number(), Name: s.Name.str(maxStringLength),
		PatternType: s.PatternType.str(maxStringLength), Pattern: s.Pattern.str(maxUrlLength),
		Required: s.Required.flag(), PatternComparison: s.PatternComparison.str(maxStringLength)}
}

// funnel converts a row. found is false for a row that does not belong to the website; ok is false for a row
// without a usable funnel ID or without the website that proves where it belongs.
func (f funnelJSON) funnel(site int) (funnel Funnel, found, ok bool) {
	id, row := f.ID.number(), f.IDSite.number()
	if id == nil || *id < 1 || row == nil {
		return Funnel{}, false, false
	}
	if *row != site {
		return Funnel{}, false, true
	}
	steps := []FunnelStep{}
	if rows, valid := readRows[funnelStepJSON](f.Steps); valid {
		for i := range rows {
			if len(steps) < maxFunnelSteps {
				steps = append(steps, rows[i].step())
			}
		}
	}
	return Funnel{IDFunnel: *id, IDSite: site, IDGoal: f.IDGoal.number(), Revision: f.Revision.number(),
		Name: f.Name.str(maxStringLength), Description: funnelText(f.Description, maxStringLength),
		CreatedDate: f.CreatedDate.str(maxStringLength), Activated: f.Activated.flag(), Steps: steps,
		IsSalesFunnel: f.IsSalesFunnel.flag(), FinalStepPosition: f.FinalStepPosition.number()}, true, true
}

func funnelNotFound(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op,
		Message: "Matomo has no such funnel for this website inside the targets of this connection"}
}

// boolAnswer reads a Matomo flag that arrives bare or as {"value": ...}.
func boolAnswer(raw json.RawMessage) (value, ok bool) {
	var direct loose
	if json.Unmarshal(raw, &direct) == nil && direct.flag() != nil && strings.TrimSpace(direct.text) != "" {
		return *direct.flag(), true
	}
	var wrapped struct {
		Value *loose `json:"value"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && wrapped.Value != nil && strings.TrimSpace(wrapped.Value.text) != "" {
		return *wrapped.Value.flag(), true
	}
	return false, false
}

// fetchFunnel is the one read that both proves a funnel belongs to the website and delivers it: the answer
// carries idsite, and a funnel of another website, or one without it, is never shown. A funnel ID is global in
// Matomo, so the ID alone proves nothing.
func (c *Client) fetchFunnel(ctx context.Context, op string, req requirement, method string, params url.Values,
	site int, wantFunnel, wantGoal *int) (Funnel, error) {
	var answer json.RawMessage
	if err := c.call(ctx, op, req, method, params, &answer); err != nil {
		return Funnel{}, err
	}
	trimmed := bytes.TrimSpace(answer)
	switch string(trimmed) {
	case "null", "false", "[]", "{}", "":
		return Funnel{}, funnelNotFound(op)
	}
	var row funnelJSON
	if !bytes.HasPrefix(trimmed, []byte("{")) || json.Unmarshal(trimmed, &row) != nil {
		return Funnel{}, provider.InvalidResponse(op, "Matomo returned an invalid funnel")
	}
	funnel, belongs, ok := row.funnel(site)
	if !ok {
		return Funnel{}, provider.InvalidResponse(op, "Matomo returned an invalid funnel")
	}
	if !belongs {
		return Funnel{}, funnelNotFound(op)
	}
	if wantFunnel != nil && funnel.IDFunnel != *wantFunnel || wantGoal != nil && funnel.IDGoal != nil && *funnel.IDGoal != *wantGoal {
		return Funnel{}, provider.InvalidResponse(op, "Matomo returned another funnel than asked for")
	}
	return funnel, nil
}

func invokeFunnelsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list funnels"
	var args struct {
		IDSite *int   `json:"idSite"`
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	paging, err := startPage(provider.CursorBinding(funnelsList.ID, scopeBinding(resolved), site), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := funnelsRequirement(funnelsList.ID)
	params := url.Values{"idSite": {strconv.Itoa(site)}}
	var flag json.RawMessage
	if err := client.call(ctx, op, req, "Funnels.hasAnyActivatedFunnelForSite", params, &flag); err != nil {
		return nil, err
	}
	has, ok := boolAnswer(flag)
	if !ok {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid answer")
	}
	out := &FunnelsResult{IDSite: site, HasActivatedFunnel: has, Funnels: []Funnel{}}
	if !has {
		return out, nil
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, req, "Funnels.getAllActivatedFunnelsForSite", params, &answer); err != nil {
		return nil, err
	}
	rows, valid := readRows[funnelJSON](answer)
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid funnel list")
	}
	funnels := []Funnel{}
	for i := range rows {
		if funnel, belongs, usable := rows[i].funnel(site); usable && belongs {
			funnels = append(funnels, funnel)
		}
	}
	batch, next := slice(paging, funnels)
	out.Funnels, out.HasMore, out.NextCursor = batch, next != "", next
	return out, nil
}

func invokeFunnelsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get funnel"
	var args struct {
		IDSite   *int `json:"idSite"`
		IDFunnel *int `json:"idFunnel"`
		IDGoal   *int `json:"idGoal"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if (args.IDFunnel == nil) == (args.IDGoal == nil) {
		return nil, invalidArgument("give exactly one of idFunnel and idGoal")
	}
	if args.IDFunnel != nil && (*args.IDFunnel < 1 || *args.IDFunnel > maxFunnelID) {
		return nil, invalidArgument("idFunnel must be a positive integer funnel ID from matomo.funnels.list")
	}
	if args.IDGoal != nil && (*args.IDGoal < 0 || *args.IDGoal > maxFunnelID) {
		return nil, invalidArgument("idGoal must be a goal ID, or 0 for the sales funnel")
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := funnelsRequirement(funnelsGet.ID)
	params := url.Values{"idSite": {strconv.Itoa(site)}}
	method := "Funnels.getFunnel"
	switch {
	case args.IDFunnel != nil:
		params.Set("idFunnel", strconv.Itoa(*args.IDFunnel))
	case *args.IDGoal == 0:
		method = "Funnels.getSalesFunnelForSite"
	default:
		method = "Funnels.getGoalFunnel"
		params.Set("idGoal", strconv.Itoa(*args.IDGoal))
	}
	funnel, err := client.fetchFunnel(ctx, op, req, method, params, site, args.IDFunnel, args.IDGoal)
	if err != nil {
		return nil, err
	}
	return &funnel, nil
}

// boundedValue keeps an oversized provider value out of a result: strings, lists, objects, and nesting are cut.
func boundedValue(value any, depth int) any {
	switch typed := value.(type) {
	case string:
		return boundString(typed, maxUrlLength)
	case []any:
		if depth >= maxResultDepth {
			return nil
		}
		out := make([]any, 0, min(len(typed), maxResultEntries))
		for _, item := range typed {
			if len(out) == maxResultEntries {
				break
			}
			out = append(out, boundedValue(item, depth+1))
		}
		return out
	case map[string]any:
		if depth >= maxResultDepth {
			return nil
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		out := map[string]any{}
		for _, key := range keys {
			if len(out) == maxResultEntries {
				break
			}
			out[boundString(key, maxStringLength)] = boundedValue(typed[key], depth+1)
		}
		return out
	}
	return value
}

// boundedAnswer decodes an answer whose members Matomo's reference does not describe.
func boundedAnswer(raw json.RawMessage) any {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return boundedValue(value, 0)
}

// websiteBound refuses a call of a tool that reads no website unless the connection binds one.
func websiteBound(resolved *config.Resolved) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if !bound.reachesSites() {
		return invalidArgument("this connection is bound to no website")
	}
	return nil
}

func invokeFunnelsTestSteps(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "test funnel steps"
	var args struct {
		URL   string            `json:"url"`
		Steps []funnelStepInput `json:"steps"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if args.URL == "" || utf8.RuneCountInString(args.URL) > maxUrlLength {
		return nil, invalidArgument("url must hold 1 to 2048 characters")
	}
	params := url.Values{"url": {args.URL}}
	if err := funnelStepParams(params, args.Steps, 1, maxTestSteps); err != nil {
		return nil, err
	}
	if err := websiteBound(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, funnelsRequirement(funnelsTestSteps.ID), "Funnels.testUrlMatchesSteps", params, &answer); err != nil {
		return nil, err
	}
	return &FunnelTestResult{Result: boundedAnswer(answer)}, nil
}

// funnelStepsSchema describes the steps of a funnel as an input: the members a step needs to match.
func funnelStepsSchema(minItems, maxItems int) string {
	return `{"type":"array","minItems":` + strconv.Itoa(minItems) + `,"maxItems":` + strconv.Itoa(maxItems) +
		`,"items":{"type":"object","properties":{"pattern_type":{"type":"string","pattern":"` + patternTypePattern +
		`"},"pattern":{"type":"string","minLength":1,"maxLength":2048},"name":{"type":"string","maxLength":512},` +
		`"required":{"type":"boolean"}},"required":["pattern_type","pattern"],"additionalProperties":false}}`
}

// funnelStepInput is a step as a caller defines it. The position of a step is its place in the list.
type funnelStepInput struct {
	PatternType string  `json:"pattern_type"`
	Pattern     string  `json:"pattern"`
	Name        *string `json:"name"`
	Required    *bool   `json:"required"`
}

// funnelStepParams checks the steps and adds them to the request as steps[N][member].
func funnelStepParams(params url.Values, steps []funnelStepInput, minSteps, maxSteps int) error {
	if len(steps) < minSteps || len(steps) > maxSteps {
		return invalidArgument("steps must hold " + strconv.Itoa(minSteps) + " to " + strconv.Itoa(maxSteps) +
			" step definitions")
	}
	for i, step := range steps {
		if !patternTypeOK(step.PatternType) || step.Pattern == "" || utf8.RuneCountInString(step.Pattern) > maxUrlLength ||
			step.Name != nil && utf8.RuneCountInString(*step.Name) > maxStringLength {
			return invalidArgument("each step needs a pattern_type of lowercase letters and underscores and a " +
				"pattern of 1 to 2048 characters; a name holds at most 512")
		}
		key := "steps[" + strconv.Itoa(i) + "]"
		params.Set(key+"[pattern_type]", step.PatternType)
		params.Set(key+"[pattern]", step.Pattern)
		if step.Name != nil {
			params.Set(key+"[name]", *step.Name)
		}
		if step.Required != nil {
			params.Set(key+"[required]", flagValue(step.Required))
		}
	}
	return nil
}

func patternTypeOK(value string) bool {
	if value == "" || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, r := range value {
		if r != '_' && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
}

func invokeFunnelOptionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list funnel pattern types"
	if err := websiteBound(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, funnelsRequirement(funnelOptionsList.ID), "Funnels.getAvailablePatternMatches", nil, &answer); err != nil {
		return nil, err
	}
	return &FunnelOptionsResult{PatternMatches: boundedAnswer(answer)}, nil
}

func funnelsOperations() []capability.Operation {
	return append([]capability.Operation{
		{Descriptor: funnelsList, Handler: capability.Handler(invokeFunnelsList)},
		{Descriptor: funnelsGet, Handler: capability.Handler(invokeFunnelsGet)},
		{Descriptor: funnelsTestSteps, Handler: capability.Handler(invokeFunnelsTestSteps)},
		{Descriptor: funnelOptionsList, Handler: capability.Handler(invokeFunnelOptionsList)},
	}, funnelsChangeOperations()...)
}
