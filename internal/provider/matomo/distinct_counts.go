package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// distinctKind is one counter: the method, the plugin that provides it, and the first release that has it.
type distinctKind struct {
	method string
	plugin string
	since  version
}

// distinctKinds are the counters of the tool. AI assistants became a referrer type with Matomo 5.5.0, so no
// earlier release counts them.
var distinctKinds = map[string]distinctKind{
	"search-engines":  {"Referrers.getNumberOfDistinctSearchEngines", "Referrers", version{}},
	"social-networks": {"Referrers.getNumberOfDistinctSocialNetworks", "Referrers", version{}},
	"keywords":        {"Referrers.getNumberOfDistinctKeywords", "Referrers", version{}},
	"campaigns":       {"Referrers.getNumberOfDistinctCampaigns", "Referrers", version{}},
	"websites":        {"Referrers.getNumberOfDistinctWebsites", "Referrers", version{}},
	"ai-assistants":   {"Referrers.getNumberOfDistinctAIAssistants", "Referrers", version{5, 5, 0}},
	"website-urls":    {"Referrers.getNumberOfDistinctWebsitesUrls", "Referrers", version{}},
	"countries":       {"UserCountry.getNumberOfDistinctCountries", "UserCountry", version{}},
}

const distinctKindsSchema = `{"type":"string","enum":["search-engines","social-networks","keywords","campaigns",` +
	`"websites","ai-assistants","website-urls","countries"]}`

var distinctCountPattern = regexp.MustCompile(`^-?[0-9]{1,18}(\.[0-9]{1,6})?$`)

var distinctCountsGet = capability.Descriptor{
	ID: Provider + ".distinct-counts.get", Version: 1, Title: "Get a Matomo distinct count",
	Description: "Read how many different search engines, social networks, search keywords, campaigns, referring " +
		"websites, referring website addresses, AI assistants, or countries brought visits in a period, one number " +
		"per period. A date range or lastN with period day, week, month, or year answers one number per period, " +
		"at most 1000 periods; period range sums the dates. The counters of search engines, keywords, and " +
		"campaigns need the Referrers plugin, countries the UserCountry plugin, and AI assistants Matomo 5.5.0. " +
		"Archiving on demand can take long, so range periods and large segments may time out",
	Tags: []string{"matomo", "referrers", "countries", "distinct", "count"}, Risk: readRisk(reportDataSensitive),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,"kind":` + distinctKindsSchema + `,` +
		`"period":{"type":"string","enum":["day","week","month","year","range"]},` +
		`"date":{"type":"string","minLength":1,"maxLength":32},` +
		`"segment":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxSegmentLength) + `}},` +
		`"required":["kind","period","date"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"kind":{"type":"string"},` +
		`"period":{"type":"string"},"date":{"type":"string"},"value":{"type":"number"},` +
		`"values":{"type":"array","items":{"type":"object","properties":{"period":{"type":"string"},` +
		`"value":{"type":"number"}},"required":["period","value"],"additionalProperties":false}}},` +
		`"required":["idSite","kind","period","date"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "kind", Required: true, Description: "What is counted: search-engines, social-networks, keywords, " +
			"campaigns, websites, ai-assistants, website-urls, or countries"},
		{Name: "period", Required: true, Description: "day, week, month, year, or range"},
		{Name: "date", Required: true, Description: "YYYY-MM-DD, today, yesterday, or a range " +
			"YYYY-MM-DD,YYYY-MM-DD or lastN or previousN. With period day, week, month, or year, a range or " +
			"lastN answers one number per period; period range sums the dates"},
		{Name: "segment", Description: "Segment expression such as browserName==Firefox; fields from " +
			"matomo.segments.fields, values URL-encoded, ; for and, , for or"},
	},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the number belongs to"},
		{Name: "kind", Description: "What was counted"},
		{Name: "period", Description: "The period asked for"},
		{Name: "date", Description: "The date asked for"},
		{Name: "value", Description: "The number of different entries in the period; absent over several periods"},
		{Name: "values", Description: "Over several periods one entry per period in time order"},
		{Name: "values[].period", Description: "The period as Matomo labels it, such as 2026-09-01"},
		{Name: "values[].value", Description: "The number of different entries in that period"},
	},
	Examples: []capability.Example{
		{Description: "Referring websites of last month", Arguments: json.RawMessage(
			`{"idSite":1,"kind":"websites","period":"month","date":"previous1"}`)},
		{Description: "Countries per day of the last week", Arguments: json.RawMessage(
			`{"idSite":1,"kind":"countries","period":"day","date":"last7"}`)},
	},
}

// DistinctValue is the count of one period.
type DistinctValue struct {
	Period string      `json:"period"`
	Value  json.Number `json:"value"`
}

// DistinctResult is the answer of distinct-counts.get.
type DistinctResult struct {
	IDSite int             `json:"idSite"`
	Kind   string          `json:"kind"`
	Period string          `json:"period"`
	Date   string          `json:"date"`
	Value  *json.Number    `json:"value,omitempty"`
	Values []DistinctValue `json:"values,omitempty"`
}

func invokeDistinctCountsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get distinct count"
	var args struct {
		IDSite  *int   `json:"idSite"`
		Kind    string `json:"kind"`
		Period  string `json:"period"`
		Date    string `json:"date"`
		Segment string `json:"segment"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	kind, known := distinctKinds[args.Kind]
	if !known {
		return nil, invalidArgument("kind must be search-engines, social-networks, keywords, campaigns, websites, " +
			"ai-assistants, website-urls, or countries")
	}
	plan, err := planPeriod(args.Period, args.Date)
	if err != nil {
		return nil, err
	}
	if args.Segment != "" && !validSegmentExpression(args.Segment) {
		return nil, invalidArgument("segment must be terms of field, operator, and URL-encoded value " +
			"joined by ; or , (fields: matomo.segments.fields)")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := requirement{tool: distinctCountsGet.ID, minVersion: kind.since, plugin: kind.plugin}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "period": {args.Period}, "date": {args.Date}}
	if args.Segment != "" {
		params.Set("segment", args.Segment)
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, req, kind.method, params, &answer); err != nil {
		return nil, err
	}
	result := &DistinctResult{IDSite: id, Kind: args.Kind, Period: args.Period, Date: args.Date}
	invalid := provider.InvalidResponse(op, "Matomo returned a count in a form this tool does not read")
	if !plan.multiple {
		value, ok := countOf(answer)
		if !ok {
			return nil, invalid
		}
		result.Value = &value
		return result, nil
	}
	tables, ok := members(answer)
	if !ok || len(tables) > maxReportRows {
		return nil, invalid
	}
	for _, table := range tables {
		value, ok := countOf(table.value)
		if !ok {
			return nil, invalid
		}
		result.Values = append(result.Values, DistinctValue{Period: boundString(table.key, maxStringLength), Value: value})
	}
	return result, nil
}

// countOf reads one count: a number, a numeric string, or the single value Matomo wraps as {"value":n}.
func countOf(raw json.RawMessage) (json.Number, bool) {
	value, ok := decodeValue(raw)
	if object, isObject := value.(map[string]any); ok && isObject && len(object) == 1 {
		value, ok = object["value"], object["value"] != nil
	}
	switch v := value.(type) {
	case json.Number:
		return v, ok
	case string:
		text := strings.TrimSpace(v)
		return json.Number(text), ok && distinctCountPattern.MatchString(text)
	}
	return "", false
}
