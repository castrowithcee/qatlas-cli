package matomo

import (
	"bytes"
	"context"
	"encoding/json"
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
	dimensionsSensitive = "matomo-custom-dimensions"
	// Matomo refuses a longer value instead of cutting it. The pattern and extraction limits are Qatlas caps.
	maxDimensionNameLength = 255
	maxDimensionDescLength = 1000
	maxExtractionPattern   = 255
	maxExtractions         = 10
	maxDimensionRows       = 200
)

var (
	// The extraction sources of Matomo 5; getAvailableExtractionDimensions reads the live list.
	extractionSources = []string{"url", "urlparam", "action_name"}
	// Scopes that can be configured; the conversion scope is internal to Matomo.
	dimensionScopes = []string{"visit", "action"}
	// Matomo refuses these characters (and any markup) in a name.
	blockedNameCharacters = "/\\&.<>"
)

var dimensionSentOnce = sentOnceFor("dimension", "matomo.dimensions.list")

func dimensionsRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: "CustomDimensions"}
}

func dimensionChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: dimensionsSensitive}
}

const (
	idDimensionSchema = `"idDimension":{"type":"integer","minimum":1,"maximum":999999999}`
	dimNameSchema     = `"name":{"type":"string","minLength":1,"maxLength":255}`
	dimDescSchema     = `"description":{"type":"string","maxLength":1000}`
	extractionsSchema = `"extractions":{"type":"array","maxItems":10,"items":{"type":"object","properties":` +
		`{"dimension":{"type":"string","enum":["url","urlparam","action_name"]},` +
		`"pattern":{"type":"string","maxLength":255}},"required":["dimension","pattern"],"additionalProperties":false}}`
	dimensionProperties = `"idDimension":{"type":"integer"},"name":{"type":"string"},"description":{"type":"string"},` +
		`"scope":{"type":"string"},"index":{"type":"integer"},"active":{"type":"boolean"},` +
		`"extractions":{"type":"array","items":{"type":"object","properties":{"dimension":{"type":"string"},` +
		`"pattern":{"type":"string"}},"additionalProperties":false}},"caseSensitive":{"type":"boolean"}`
)

func dimensionFields(prefix string) []capability.Field {
	return []capability.Field{
		{Name: prefix + "idDimension", Description: "Dimension ID, used as idDimension by the other tools; it counts per website"},
		{Name: prefix + "name", Description: "Dimension name, untrusted"},
		{Name: prefix + "description", Description: "Description of the dimension, untrusted"},
		{Name: prefix + "scope", Description: "visit or action"},
		{Name: prefix + "index", Description: "Slot of the dimension in the tracking tables"},
		{Name: prefix + "active", Description: "True while the dimension collects data; false means paused"},
		{Name: prefix + "extractions", Description: "Rules that fill an action dimension from the address or title, untrusted"},
		{Name: prefix + "extractions[].dimension", Description: "Source of the rule: url, urlparam, or action_name"},
		{Name: prefix + "extractions[].pattern", Description: "Pattern of the rule, untrusted"},
		{Name: prefix + "caseSensitive", Description: "True when extraction patterns match case-sensitively"},
	}
}

func idDimensionArgument(verb string) capability.Argument {
	return capability.Argument{Name: "idDimension", Required: true, Description: "ID of the dimension to " + verb +
		", from matomo.dimensions.list; it counts per website"}
}

var extractionsArgument = capability.Argument{Name: "extractions", Description: "Extraction rules as a list of " +
	"{dimension, pattern}, at most 10; dimension is url, urlparam, or action_name; the pattern of url and " +
	"action_name groups exactly one part in round brackets, such as index_(.+).html; the pattern of urlparam is " +
	"the parameter name; at most 255 characters; only for the scope action"}

var dimensionsList = capability.Descriptor{
	ID: Provider + ".dimensions.list", Version: 1, Title: "List Matomo custom dimensions",
	Description: "List the custom dimensions configured on one website with scope, state, and extraction rules; " +
		"the ID counts per website; names and patterns are untrusted",
	Tags: []string{"matomo", "dimensions", "custom", "list"}, Risk: readRisk(dimensionsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"dimensions":` +
		`{"type":"array","items":{"type":"object","properties":{` + dimensionProperties + `},` +
		`"required":["idDimension"],"additionalProperties":false}}},"required":["idSite","dimensions"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument()},
	Fields: append([]capability.Field{{Name: "idSite", Description: "The website the dimensions belong to"},
		{Name: "dimensions", Description: "Dimensions ordered by idDimension, at most 200"}},
		dimensionFields("dimensions[].")...),
	Examples: []capability.Example{{Description: "List the dimensions of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var dimensionOptionsList = capability.Descriptor{
	ID: Provider + ".dimension-options.list", Version: 1, Title: "List Matomo custom dimension options",
	Description: "Read the scopes a website can use with their free slots, and the sources that extraction rules can " +
		"read; the sources need a write right on some website",
	Tags: []string{"matomo", "dimensions", "scopes", "extractions", "list"}, Risk: readRisk(dimensionsSensitive),
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"scopes":` +
		`{"type":"array","items":{"type":"object","properties":{"value":{"type":"string"},"name":{"type":"string"},` +
		`"numSlotsAvailable":{"type":"integer"},"numSlotsUsed":{"type":"integer"},"numSlotsLeft":{"type":"integer"},` +
		`"supportsExtractions":{"type":"boolean"}},"required":["value"],"additionalProperties":false}},` +
		`"extractionDimensions":{"type":"array","items":{"type":"object","properties":{"value":{"type":"string"},` +
		`"name":{"type":"string"}},"required":["value"],"additionalProperties":false}}},` +
		`"required":["idSite","scopes","extractionDimensions"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument()},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the slots were counted for"},
		{Name: "scopes", Description: "Scopes a dimension can have"},
		{Name: "scopes[].value", Description: "Scope value for dimensions.add: visit or action"},
		{Name: "scopes[].name", Description: "Display name of the scope, untrusted"},
		{Name: "scopes[].numSlotsAvailable", Description: "Slots the instance has for the scope"},
		{Name: "scopes[].numSlotsUsed", Description: "Slots the website already uses; dimensions cannot be deleted"},
		{Name: "scopes[].numSlotsLeft", Description: "Slots the website can still use"},
		{Name: "scopes[].supportsExtractions", Description: "True when dimensions of the scope may have extraction rules"},
		{Name: "extractionDimensions", Description: "Sources that extraction rules can read"},
		{Name: "extractionDimensions[].value", Description: "Source value for extractions[].dimension"},
		{Name: "extractionDimensions[].name", Description: "Display name of the source, untrusted"},
	},
	Examples: []capability.Example{{Description: "Check the free slots of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var dimensionsAdd = capability.Descriptor{
	ID: Provider + ".dimensions.add", Version: 1, Title: "Add a Matomo custom dimension",
	Description: "Configure a new custom dimension on a website; it needs a write right and uses one slot of its " +
		"scope for good, since Matomo cannot delete dimensions; the name has no / \\ & . < > characters; not " +
		"idempotent, since a repeated call uses a second slot; sent once and never repeated",
	Tags: []string{"matomo", "dimensions", "custom", "create"}, Risk: dimensionChangeRisk(capability.EffectCreate,
		capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + dimNameSchema + `,"scope":` +
		`{"type":"string","enum":["visit","action"]},"active":{"type":"boolean"},` + extractionsSchema + `,` +
		`"caseSensitive":{"type":"boolean"},` + dimDescSchema + `},"required":["name","scope","active"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` + idDimensionSchema +
		`,"created":{"type":"boolean"}},"required":["idSite","idDimension","created"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "name", Required: true, Description: "Dimension name, 1 to 255 characters without / \\ & . < >"},
		{Name: "scope", Required: true, Description: "visit or action; it cannot be changed afterwards; see " +
			"matomo.dimension-options.list for the free slots"},
		{Name: "active", Required: true, Description: "True to collect data at once, false to create it paused"},
		extractionsArgument,
		{Name: "caseSensitive", Description: "True to match extraction patterns case-sensitively; true when omitted"},
		{Name: "description", Description: "Description, at most 1000 characters without markup"}},
	Fields: []capability.Field{{Name: "idSite", Description: "The website the dimension was created for"},
		{Name: "idDimension", Description: "ID of the new dimension; it counts per website"},
		{Name: "created", Description: "True once Matomo created the dimension"}},
	Examples: []capability.Example{{Description: "Add a visit dimension for the customer type",
		Arguments: json.RawMessage(`{"idSite":1,"name":"Customer type","scope":"visit","active":true}`)}},
}

var dimensionsUpdate = capability.Descriptor{
	ID: Provider + ".dimensions.update", Version: 1, Title: "Update a Matomo custom dimension",
	Description: "Change name, state, extraction rules, case sensitivity, or description of one dimension of a " +
		"website; a value not given stays as it is; active=false pauses the dimension, since Matomo has no way to " +
		"delete one; the scope cannot be changed after creation; it needs a write right; reads the dimension " +
		"first; idempotent, since the same values leave the same state; sent once and never repeated",
	Tags: []string{"matomo", "dimensions", "custom", "update", "pause"}, Risk: dimensionChangeRisk(capability.EffectUpdate,
		capability.IdempotencyIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idDimensionSchema + `,` +
		dimNameSchema + `,"active":{"type":"boolean"},` + extractionsSchema + `,"caseSensitive":{"type":"boolean"},` +
		dimDescSchema + `},"required":["idDimension"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` + dimensionProperties +
		`,"updated":{"type":"boolean"}},"required":["idSite","idDimension","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(), idDimensionArgument("change"),
		{Name: "name", Description: "New name, 1 to 255 characters without / \\ & . < >; unchanged when omitted"},
		{Name: "active", Description: "False pauses the dimension, true resumes it; unchanged when omitted"},
		{Name: "extractions", Description: "The complete new list of extraction rules ({dimension, pattern}, at most " +
			"10), which replaces the old one; an empty list clears it; unchanged when omitted"},
		{Name: "caseSensitive", Description: "New case sensitivity; unchanged when omitted"},
		{Name: "description", Description: "New description, at most 1000 characters; unchanged when omitted"}},
	Fields: append([]capability.Field{{Name: "idSite", Description: "The website the dimension belongs to"},
		{Name: "updated", Description: "True once Matomo accepted the change"}}, dimensionFields("")...),
	Examples: []capability.Example{{Description: "Pause dimension 2 of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idDimension":2,"active":false}`)}},
}

// dimensionJSON is a dimension as getConfiguredCustomDimensions reports it. Every member is read loosely.
type dimensionJSON struct {
	ID            *loose            `json:"idcustomdimension"`
	IDSite        *loose            `json:"idsite"`
	Name          *loose            `json:"name"`
	Description   *loose            `json:"description"`
	Scope         *loose            `json:"scope"`
	Index         *loose            `json:"index"`
	Active        *loose            `json:"active"`
	Extractions   []json.RawMessage `json:"extractions"`
	CaseSensitive *loose            `json:"case_sensitive"`
}

// Extraction is one extraction rule. The pattern is untrusted provider data.
type Extraction struct {
	Dimension string `json:"dimension"`
	Pattern   string `json:"pattern"`
}

// Dimension is a custom dimension of a website. Name, description, and patterns are untrusted provider data.
type Dimension struct {
	IDDimension   int          `json:"idDimension"`
	Name          string       `json:"name,omitempty"`
	Description   string       `json:"description,omitempty"`
	Scope         string       `json:"scope,omitempty"`
	Index         *int         `json:"index,omitempty"`
	Active        *bool        `json:"active,omitempty"`
	Extractions   []Extraction `json:"extractions,omitempty"`
	CaseSensitive *bool        `json:"caseSensitive,omitempty"`
}

// dimension converts a row; ok is false for a row without a usable ID or from another website than site.
func (d dimensionJSON) dimension(site int) (Dimension, bool) {
	id := d.ID.number()
	if id == nil || *id < 1 {
		return Dimension{}, false
	}
	if row := d.IDSite.number(); row != nil && *row != site {
		return Dimension{}, false
	}
	var extractions []Extraction
	for _, entry := range d.Extractions {
		var rule struct {
			Dimension *loose `json:"dimension"`
			Pattern   *loose `json:"pattern"`
		}
		if json.Unmarshal(entry, &rule) != nil || len(extractions) >= maxExtractions*2 {
			continue
		}
		extractions = append(extractions, Extraction{Dimension: rule.Dimension.str(64),
			Pattern: rule.Pattern.str(maxStringLength)})
	}
	return Dimension{IDDimension: *id, Name: d.Name.str(maxStringLength), Description: d.Description.str(maxDimensionDescLength),
		Scope: d.Scope.str(32), Index: d.Index.number(), Active: d.Active.flag(), Extractions: extractions,
		CaseSensitive: d.CaseSensitive.flag()}, true
}

// DimensionsResult is the answer of dimensions.list.
type DimensionsResult struct {
	IDSite     int         `json:"idSite"`
	Dimensions []Dimension `json:"dimensions"`
}

// ScopeOption is a scope with its slots.
type ScopeOption struct {
	Value               string `json:"value"`
	Name                string `json:"name,omitempty"`
	NumSlotsAvailable   *int   `json:"numSlotsAvailable,omitempty"`
	NumSlotsUsed        *int   `json:"numSlotsUsed,omitempty"`
	NumSlotsLeft        *int   `json:"numSlotsLeft,omitempty"`
	SupportsExtractions *bool  `json:"supportsExtractions,omitempty"`
}

// ExtractionSource is a source that extraction rules can read.
type ExtractionSource struct {
	Value string `json:"value"`
	Name  string `json:"name,omitempty"`
}

// DimensionOptionsResult is the answer of dimension-options.list.
type DimensionOptionsResult struct {
	IDSite               int                `json:"idSite"`
	Scopes               []ScopeOption      `json:"scopes"`
	ExtractionDimensions []ExtractionSource `json:"extractionDimensions"`
}

// DimensionAdded is the answer of dimensions.add.
type DimensionAdded struct {
	IDSite      int  `json:"idSite"`
	IDDimension int  `json:"idDimension"`
	Created     bool `json:"created"`
}

// DimensionUpdated is the answer of dimensions.update.
type DimensionUpdated struct {
	IDSite int `json:"idSite"`
	Dimension
	Updated bool `json:"updated"`
}

// fetchDimensions reads the configured dimensions of the website and keeps only rows of that website.
func (c *Client) fetchDimensions(ctx context.Context, op, tool string, site int) ([]Dimension, error) {
	var rows []dimensionJSON
	params := url.Values{"idSite": {strconv.Itoa(site)}}
	if err := c.call(ctx, op, dimensionsRequirement(tool), "CustomDimensions.getConfiguredCustomDimensions", params, &rows); err != nil {
		return nil, err
	}
	dimensions := []Dimension{}
	for _, row := range rows {
		if d, ok := row.dimension(site); ok && len(dimensions) < maxDimensionRows {
			dimensions = append(dimensions, d)
		}
	}
	slices.SortStableFunc(dimensions, func(a, b Dimension) int { return a.IDDimension - b.IDDimension })
	return dimensions, nil
}

func invokeDimensionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list custom dimensions"
	var args struct {
		IDSite *int `json:"idSite"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	dimensions, err := client.fetchDimensions(ctx, op, dimensionsList.ID, id)
	if err != nil {
		return nil, err
	}
	return &DimensionsResult{IDSite: id, Dimensions: dimensions}, nil
}

func invokeDimensionOptionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list custom dimension options"
	var args struct {
		IDSite *int `json:"idSite"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := dimensionsRequirement(dimensionOptionsList.ID)
	var scopeRows []struct {
		Value               *loose `json:"value"`
		Name                *loose `json:"name"`
		NumSlotsAvailable   *loose `json:"numSlotsAvailable"`
		NumSlotsUsed        *loose `json:"numSlotsUsed"`
		NumSlotsLeft        *loose `json:"numSlotsLeft"`
		SupportsExtractions *loose `json:"supportsExtractions"`
	}
	if err := client.call(ctx, op, req, "CustomDimensions.getAvailableScopes",
		url.Values{"idSite": {strconv.Itoa(id)}}, &scopeRows); err != nil {
		return nil, err
	}
	var sourceRows []struct {
		Value *loose `json:"value"`
		Name  *loose `json:"name"`
	}
	if err := client.call(ctx, op, req, "CustomDimensions.getAvailableExtractionDimensions", url.Values{}, &sourceRows); err != nil {
		return nil, err
	}
	result := &DimensionOptionsResult{IDSite: id, Scopes: []ScopeOption{}, ExtractionDimensions: []ExtractionSource{}}
	for _, row := range scopeRows {
		if value := row.Value.str(32); value != "" && len(result.Scopes) < 20 {
			result.Scopes = append(result.Scopes, ScopeOption{Value: value, Name: row.Name.str(maxStringLength),
				NumSlotsAvailable: row.NumSlotsAvailable.number(), NumSlotsUsed: row.NumSlotsUsed.number(),
				NumSlotsLeft: row.NumSlotsLeft.number(), SupportsExtractions: row.SupportsExtractions.flag()})
		}
	}
	for _, row := range sourceRows {
		if value := row.Value.str(64); value != "" && len(result.ExtractionDimensions) < 20 {
			result.ExtractionDimensions = append(result.ExtractionDimensions,
				ExtractionSource{Value: value, Name: row.Name.str(maxStringLength)})
		}
	}
	return result, nil
}

// checkDimensionName refuses what Matomo refuses: an empty or long name, markup, and its blocked characters.
func checkDimensionName(name string) error {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > maxDimensionNameLength ||
		strings.ContainsAny(name, blockedNameCharacters) {
		return invalidArgument("name must hold 1 to 255 characters without / \\ & . < >")
	}
	return nil
}

func checkDimensionDescription(text string) error {
	if utf8.RuneCountInString(text) > maxDimensionDescLength || strings.ContainsAny(text, "<>") {
		return invalidArgument("description must hold at most 1000 characters without markup")
	}
	return nil
}

func checkExtractions(rules []Extraction) error {
	if len(rules) > maxExtractions {
		return invalidArgument("extractions must hold at most 10 rules")
	}
	for _, rule := range rules {
		if !slices.Contains(extractionSources, rule.Dimension) {
			return invalidArgument("extractions[].dimension must be one of " + strings.Join(extractionSources, ", "))
		}
		if utf8.RuneCountInString(rule.Pattern) > maxExtractionPattern {
			return invalidArgument("extractions[].pattern must hold at most 255 characters")
		}
	}
	return nil
}

func setExtractions(params url.Values, rules []Extraction) {
	for i, rule := range rules {
		prefix := "extractions[" + strconv.Itoa(i) + "]"
		params.Set(prefix+"[dimension]", rule.Dimension)
		params.Set(prefix+"[pattern]", rule.Pattern)
	}
}

func invokeDimensionsAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add custom dimension"
	var args struct {
		IDSite        *int         `json:"idSite"`
		Name          string       `json:"name"`
		Scope         string       `json:"scope"`
		Active        *bool        `json:"active"`
		Extractions   []Extraction `json:"extractions"`
		CaseSensitive *bool        `json:"caseSensitive"`
		Description   string       `json:"description"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if err := checkDimensionName(args.Name); err != nil {
		return nil, err
	}
	if !slices.Contains(dimensionScopes, args.Scope) {
		return nil, invalidArgument("scope must be one of " + strings.Join(dimensionScopes, ", "))
	}
	if args.Active == nil {
		return nil, invalidArgument("active is required")
	}
	if err := checkExtractions(args.Extractions); err != nil {
		return nil, err
	}
	if len(args.Extractions) > 0 && args.Scope != "action" {
		return nil, invalidArgument("extractions can be used only in the scope action")
	}
	if err := checkDimensionDescription(args.Description); err != nil {
		return nil, err
	}
	caseSensitive := true
	if args.CaseSensitive != nil {
		caseSensitive = *args.CaseSensitive
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "name": {args.Name}, "scope": {args.Scope},
		"active": {flagValue(args.Active)}, "caseSensitive": {flagValue(&caseSensitive)}}
	if args.Description != "" {
		params.Set("description", args.Description)
	}
	setExtractions(params, args.Extractions)
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, dimensionsRequirement(dimensionsAdd.ID), "CustomDimensions.configureNewCustomDimension",
		params, dimensionSentOnce, &answer); err != nil {
		return nil, err
	}
	var created loose
	if err := json.Unmarshal(bytes.TrimSpace(answer), &created); err != nil || created.number() == nil || *created.number() < 1 {
		return nil, unclearChange(op, "Matomo returned no usable dimension ID", dimensionSentOnce)
	}
	return &DimensionAdded{IDSite: id, IDDimension: *created.number(), Created: true}, nil
}

func invokeDimensionsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update custom dimension"
	var args struct {
		IDSite        *int          `json:"idSite"`
		IDDimension   *int          `json:"idDimension"`
		Name          *string       `json:"name"`
		Active        *bool         `json:"active"`
		Extractions   *[]Extraction `json:"extractions"`
		CaseSensitive *bool         `json:"caseSensitive"`
		Description   *string       `json:"description"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if args.IDDimension == nil || *args.IDDimension < 1 || len(strconv.Itoa(*args.IDDimension)) > maxGoalIDDigits {
		return nil, invalidArgument("idDimension must be a positive integer dimension ID")
	}
	if args.Name == nil && args.Active == nil && args.Extractions == nil && args.CaseSensitive == nil && args.Description == nil {
		return nil, invalidArgument("give at least one of name, active, extractions, caseSensitive, and description")
	}
	if args.Name != nil {
		if err := checkDimensionName(*args.Name); err != nil {
			return nil, err
		}
	}
	if args.Extractions != nil {
		if err := checkExtractions(*args.Extractions); err != nil {
			return nil, err
		}
	}
	if args.Description != nil {
		if err := checkDimensionDescription(*args.Description); err != nil {
			return nil, err
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	// Matomo replaces name, state, and extractions as a whole, so the current values fill what is not given.
	// The read also proves that the ID belongs to the website, because Matomo looks it up per website.
	current, err := client.fetchDimensions(ctx, op, dimensionsUpdate.ID, id)
	if err != nil {
		return nil, err
	}
	index := slices.IndexFunc(current, func(d Dimension) bool { return d.IDDimension == *args.IDDimension })
	if index < 0 {
		return nil, &provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "Matomo has no custom dimension with this ID for this website"}
	}
	merged := current[index]
	if args.Name != nil {
		merged.Name = *args.Name
	}
	if args.Active != nil {
		merged.Active = args.Active
	}
	if args.Extractions != nil {
		merged.Extractions = *args.Extractions
	}
	if args.CaseSensitive != nil {
		merged.CaseSensitive = args.CaseSensitive
	}
	if args.Description != nil {
		merged.Description = *args.Description
	}
	if len(merged.Extractions) > 0 && merged.Scope != "action" {
		return nil, invalidArgument("extractions can be used only in the scope action")
	}
	if err := checkDimensionName(merged.Name); err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "idDimension": {strconv.Itoa(merged.IDDimension)},
		"name": {merged.Name}, "active": {flagValue(merged.Active)}, "caseSensitive": {flagValue(merged.CaseSensitive)},
		"description": {merged.Description}}
	setExtractions(params, merged.Extractions)
	var answer json.RawMessage
	if err := client.change(ctx, op, dimensionsRequirement(dimensionsUpdate.ID), "CustomDimensions.configureExistingCustomDimension",
		params, dimensionSentOnce, &answer); err != nil {
		return nil, err
	}
	return &DimensionUpdated{IDSite: id, Dimension: merged, Updated: true}, nil
}
