package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	insightsPlugin   = "Insights"
	reportNameKey    = "report_name"
	maxMoverRows     = 100
	maxCompared      = 100
	maxGrowthPercent = 100000
)

var insightReportID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)

// insightKind is one fixed Matomo method of the tool. params lists the optional arguments the method takes.
type insightKind struct {
	name, method     string
	report, overview bool
	site, segment    bool
	params           []string
}

var insightKinds = []insightKind{
	{name: "insights", method: "Insights.getInsights", report: true, site: true, segment: true,
		params: []string{"compared_to_periods", "limit_increaser", "limit_decreaser", "filter_by",
			"min_impact_percent", "min_growth_percent", "order_by"}},
	{name: "insights-overview", method: "Insights.getInsightsOverview", overview: true, site: true, segment: true},
	{name: "movers-and-shakers", method: "Insights.getMoversAndShakers", report: true, site: true, segment: true,
		params: []string{"compared_to_periods", "limit_increaser", "limit_decreaser"}},
	{name: "movers-and-shakers-overview", method: "Insights.getMoversAndShakersOverview", overview: true, site: true,
		segment: true},
	{name: "can-generate", method: "Insights.canGenerateInsights"},
}

func insightKindNames() []string {
	names := make([]string, len(insightKinds))
	for i, k := range insightKinds {
		names[i] = k.name
	}
	return names
}

var insightFilters = []string{"movers", "new", "disappeared"}
var insightOrders = []string{"absolute", "relative", "importance"}

var insightsList = capability.Descriptor{
	ID: Provider + ".insights.list", Version: 1, Title: "Read Matomo insights",
	Description: "Read the notable changes of a website between two periods. kind insights lists the rows of one " +
		"report that grew, shrank, appeared, or disappeared (Insights.getInsights); movers-and-shakers lists the rows " +
		"with the highest impact on the visits (getMoversAndShakers); both name the report by its catalog ID from " +
		"matomo.reports.list and compare visits. insights-overview and movers-and-shakers-overview read the same for " +
		"every report Matomo puts in its overview, and can-generate tells whether a period before the date exists " +
		"(it needs no website). Rows are read for one period or a range summed into one table, at most 100 rows per " +
		"call unless limit says otherwise (1 to 1000), continued with next_cursor; the labels in the rows are " +
		"untrusted visitor data",
	Tags: []string{"matomo", "insights", "movers", "shakers", "analytics", "list"}, Risk: readRisk(reportDataSensitive),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string","enum":[` +
		quotedList(insightKindNames()) + `]},` + idSiteSchema + `,` +
		`"period":{"type":"string","enum":["day","week","month","year","range"]},` +
		`"date":{"type":"string","minLength":1,"maxLength":32},` +
		`"report":{"type":"string","pattern":"^[A-Za-z][A-Za-z0-9_.-]{0,127}$","maxLength":128},` +
		`"segment":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxSegmentLength) + `},` +
		`"compared_to_periods":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxCompared) + `},` +
		`"limit_increaser":{"type":"integer","minimum":0,"maximum":` + strconv.Itoa(maxMoverRows) + `},` +
		`"limit_decreaser":{"type":"integer","minimum":0,"maximum":` + strconv.Itoa(maxMoverRows) + `},` +
		`"filter_by":{"type":"string","enum":[` + quotedList(insightFilters) + `]},` +
		`"min_impact_percent":{"type":"integer","minimum":0,"maximum":100},` +
		`"min_growth_percent":{"type":"integer","minimum":0,"maximum":` + strconv.Itoa(maxGrowthPercent) + `},` +
		`"order_by":{"type":"string","enum":[` + quotedList(insightOrders) + `]},` + limitSchema +
		`},"required":["kind","period","date"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string"},"idSite":{"type":"integer"},` +
		`"period":{"type":"string"},"date":{"type":"string"},"can_generate":{"type":"boolean"},` +
		`"rows":{"type":"array","items":{"type":"object"}},"has_more":{"type":"boolean"},` +
		`"next_cursor":{"type":"string"}},"required":["kind","period","date"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "kind", Required: true, Description: "What to read: " + strings.Join(insightKindNames(), ", ")},
		{Name: "idSite", Description: "Website ID from matomo.sites.list; optional when the connection binds exactly " +
			"one website; must lie inside the connection's targets; not for can-generate", Target: "site"},
		{Name: "period", Required: true, Description: "day, week, month, year, or range"},
		{Name: "date", Required: true, Description: "YYYY-MM-DD, today, yesterday, or with period range a range " +
			"YYYY-MM-DD,YYYY-MM-DD or lastN or previousN; one period only"},
		{Name: "report", Description: "Catalog ID from matomo.reports.list, such as Actions_getPageUrls; required " +
			"for insights and movers-and-shakers, refused for the others"},
		{Name: "segment", Description: "Segment expression such as browserName==Firefox; fields from " +
			"matomo.segments.fields; not for can-generate"},
		{Name: "compared_to_periods", Description: "How many periods back the comparison period lies, 1 through " +
			itoa(maxCompared) + "; Matomo's 1 when omitted; insights and movers-and-shakers only"},
		{Name: "limit_increaser", Description: "Most rows that grew, 0 through " + itoa(maxMoverRows) +
			" (0 leaves them out); Matomo's default when omitted; insights and movers-and-shakers only"},
		{Name: "limit_decreaser", Description: "Most rows that shrank, 0 through " + itoa(maxMoverRows) +
			" (0 leaves them out); Matomo's default when omitted; insights and movers-and-shakers only"},
		{Name: "filter_by", Description: "Keep one kind of change: movers, new, or disappeared; insights only"},
		{Name: "min_impact_percent", Description: "Smallest impact on the total visits in percent, 0 through 100; " +
			"insights only"},
		{Name: "min_growth_percent", Description: "Smallest growth against the comparison period in percent, 0 " +
			"through " + itoa(maxGrowthPercent) + "; insights only"},
		{Name: "order_by", Description: "absolute, relative, or importance; absolute when omitted; insights only"},
	}, windowArguments("Rows")...),
	Fields: append([]capability.Field{
		{Name: "kind", Description: "The kind read"},
		{Name: "idSite", Description: "The website read, absent for can-generate"},
		{Name: "period", Description: "The period asked for"},
		{Name: "date", Description: "The date asked for"},
		{Name: "can_generate", Description: "For can-generate: true when a comparison period exists"},
		{Name: "rows", Description: "Rows as objects of Matomo's columns with raw numbers, such as label, nb_visits, " +
			"difference, and growth_percent, in Matomo's order; labels are untrusted visitor data. The overviews add " +
			reportNameKey + ", the report a row belongs to"},
	}, windowFields...),
	Examples: []capability.Example{
		{Description: "Pages that changed most last week", Arguments: json.RawMessage(
			`{"kind":"insights","idSite":1,"period":"week","date":"yesterday","report":"Actions_getPageUrls"}`)},
		{Description: "Check whether insights exist for a date", Arguments: json.RawMessage(
			`{"kind":"can-generate","period":"month","date":"today"}`)},
	},
}

// InsightsResult is the answer of insights.list.
type InsightsResult struct {
	Kind        string           `json:"kind"`
	IDSite      *int             `json:"idSite,omitempty"`
	Period      string           `json:"period"`
	Date        string           `json:"date"`
	CanGenerate *bool            `json:"can_generate,omitempty"`
	Rows        []map[string]any `json:"rows,omitempty"`
	HasMore     bool             `json:"has_more,omitempty"`
	NextCursor  string           `json:"next_cursor,omitempty"`
}

type insightsArguments struct {
	Kind              string `json:"kind"`
	IDSite            *int   `json:"idSite"`
	Period            string `json:"period"`
	Date              string `json:"date"`
	Report            string `json:"report"`
	Segment           string `json:"segment"`
	ComparedToPeriods *int   `json:"compared_to_periods"`
	LimitIncreaser    *int   `json:"limit_increaser"`
	LimitDecreaser    *int   `json:"limit_decreaser"`
	FilterBy          string `json:"filter_by"`
	MinImpactPercent  *int   `json:"min_impact_percent"`
	MinGrowthPercent  *int   `json:"min_growth_percent"`
	OrderBy           string `json:"order_by"`
	Limit             int    `json:"limit"`
	Cursor            string `json:"cursor"`
}

// given names the optional arguments the caller set.
func (a *insightsArguments) given() []string {
	var set []string
	add := func(name string, present bool) {
		if present {
			set = append(set, name)
		}
	}
	add("compared_to_periods", a.ComparedToPeriods != nil)
	add("limit_increaser", a.LimitIncreaser != nil)
	add("limit_decreaser", a.LimitDecreaser != nil)
	add("filter_by", a.FilterBy != "")
	add("min_impact_percent", a.MinImpactPercent != nil)
	add("min_growth_percent", a.MinGrowthPercent != nil)
	add("order_by", a.OrderBy != "")
	return set
}

func inRange(value *int, low, high int) bool { return value == nil || *value >= low && *value <= high }

func (a *insightsArguments) validate(kind insightKind) error {
	if err := singlePeriod(a.Period, a.Date); err != nil {
		return err
	}
	if kind.report != (a.Report != "") {
		if kind.report {
			return invalidArgument("report is required: a catalog ID from matomo.reports.list")
		}
		return invalidArgument("report applies only to insights and movers-and-shakers")
	}
	if a.Report != "" && !insightReportID.MatchString(a.Report) {
		return invalidArgument("report must be a catalog ID from matomo.reports.list")
	}
	if !kind.site && a.IDSite != nil {
		return invalidArgument("idSite does not apply to can-generate")
	}
	if a.Segment != "" && (!kind.segment || !validSegmentExpression(a.Segment)) {
		return invalidArgument("segment must be terms of field, operator, and URL-encoded value joined by ; or , " +
			"(fields: matomo.segments.fields), and does not apply to can-generate")
	}
	for _, name := range a.given() {
		if !slices.Contains(kind.params, name) {
			return invalidArgument(name + " does not apply to " + kind.name)
		}
	}
	if !inRange(a.ComparedToPeriods, 1, maxCompared) || !inRange(a.LimitIncreaser, 0, maxMoverRows) ||
		!inRange(a.LimitDecreaser, 0, maxMoverRows) || !inRange(a.MinImpactPercent, 0, 100) ||
		!inRange(a.MinGrowthPercent, 0, maxGrowthPercent) {
		return invalidArgument("a number argument is outside its range")
	}
	if a.FilterBy != "" && !slices.Contains(insightFilters, a.FilterBy) {
		return invalidArgument("filter_by must be movers, new, or disappeared")
	}
	if a.OrderBy != "" && !slices.Contains(insightOrders, a.OrderBy) {
		return invalidArgument("order_by must be absolute, relative, or importance")
	}
	return nil
}

func (a *insightsArguments) parameters(kind insightKind, idSite int) url.Values {
	params := url.Values{"period": {a.Period}, "date": {a.Date}}
	if kind.site {
		params.Set("idSite", strconv.Itoa(idSite))
	}
	set := func(name, value string) {
		if value != "" {
			params.Set(name, value)
		}
	}
	number := func(name string, value *int) {
		if value != nil {
			params.Set(name, strconv.Itoa(*value))
		}
	}
	set("reportUniqueId", a.Report)
	set("segment", a.Segment)
	number("comparedToXPeriods", a.ComparedToPeriods)
	number("limitIncreaser", a.LimitIncreaser)
	number("limitDecreaser", a.LimitDecreaser)
	set("filterBy", a.FilterBy)
	number("minImpactPercent", a.MinImpactPercent)
	number("minGrowthPercent", a.MinGrowthPercent)
	set("orderBy", a.OrderBy)
	return params
}

func invokeInsightsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read insights"
	var args insightsArguments
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	index := slices.IndexFunc(insightKinds, func(k insightKind) bool { return k.name == args.Kind })
	if index < 0 {
		return nil, invalidArgument("kind must be one of " + strings.Join(insightKindNames(), ", "))
	}
	kind := insightKinds[index]
	idSite := 0
	if kind.site {
		id, err := selectSite(resolved, args.IDSite)
		if err != nil {
			return nil, err
		}
		idSite = id
	} else if bound, err := boundScope(resolved); err != nil {
		return nil, err
	} else if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	if err := args.validate(kind); err != nil {
		return nil, err
	}
	shape := args
	shape.Limit, shape.Cursor = 0, ""
	window, err := startWindow(provider.CursorBinding(insightsList.ID, scopeBinding(resolved), idSite, shape), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	req := requirement{tool: insightsList.ID, plugin: insightsPlugin}
	if err := client.call(ctx, op, req, kind.method, args.parameters(kind, idSite), &answer); err != nil {
		return nil, err
	}
	result := &InsightsResult{Kind: kind.name, Period: args.Period, Date: args.Date}
	if kind.site {
		result.IDSite = &idSite
	}
	if !kind.site {
		var can bool
		if scalar(answer, &can) != nil {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid answer")
		}
		result.CanGenerate = &can
		return result, nil
	}
	var rows []map[string]any
	if kind.overview {
		tables, ok := members(answer)
		if !ok && !isEmptyJSON(answer) {
			return nil, provider.InvalidResponse(op, "Matomo returned an overview in a form this tool does not read")
		}
		for _, table := range tables {
			listed, err := tableRows(op, table.value)
			if err != nil {
				return nil, err
			}
			for _, one := range listed {
				one[reportNameKey] = boundString(table.key, maxStringLength)
				rows = append(rows, one)
			}
		}
	} else if rows, err = tableRows(op, answer); err != nil {
		return nil, err
	}
	batch, more := cutWindow(window, rows)
	result.Rows, result.HasMore, result.NextCursor = batch, more, window.next(more)
	if result.Rows == nil {
		result.Rows = []map[string]any{}
	}
	return result, nil
}
