package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	reportDataSensitive = "matomo-report-data"
	// A call reads at most maxReportRows rows over all periods: limit times the number of periods.
	defaultReportLimit = 100
	maxReportRows      = 1000
	maxReportOffset    = 100000
	maxReportColumns   = 50
	maxReportLabels    = 10
	maxLabelLength     = 256
	maxFilterPattern   = 100
	maxSegmentLength   = 1000
	maxSegmentTerms    = 20
	maxValueDepth      = 4
	periodLabelKey     = "period_label"
)

// firstColumnFlatten is the first release that keeps flattened dimension columns when showColumns is set.
var firstColumnFlatten = version{5, 14, 0}

var (
	namePattern    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)
	identPattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)
	segmentTerm    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.]{0,99}(==|!=|=@|!@|=\^|=\$|<=|>=|<|>)[^\x00-\x1f\x7f]{0,200}$`)
	countedDate    = regexp.MustCompile(`^(last|previous)(\d{1,4})$`)
	singleDate     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	forbiddenNames = map[string]bool{
		"idSite": true, "period": true, "date": true, "method": true, "module": true, "format": true,
		"format_metrics": true, "language": true, "idSubtable": true, "segment": true, "flat": true, "label": true,
		"expanded": true, "serialize": true, "token_auth": true, "showColumns": true, "hideColumns": true,
		"hideColumnsRecursively": true, "showRawMetrics": true, "showTimer": true, "hideMetricsDoc": true,
		"apiModule": true, "apiAction": true, "apiParameters": true, "keep_summary_row": true,
		"keep_flattened_dimension_columns": true, "include_aggregate_rows": true, "show_dimensions": true,
		"callback": true, "jsoncallback": true, "compare": true, "compareDates": true, "comparePeriods": true,
		"compareSegments": true, "labelFilterAddLabelIndex": true,
	}
)

const reportsGetDescription = "Read one report of a website for a period: the rows of the report with the chosen " +
	"columns, at most 100 rows per call unless limit says otherwise (1 to 1000), continued with next_cursor. " +
	"Pick the report from matomo.reports.list; its module, method, and parameters come from the catalog of the " +
	"website. Rows over several periods (period day, week, month, or year with a date range or lastN) are one " +
	"call, and limit times the number of periods must not exceed 1000. Matomo reports no total row count: " +
	"has_more is true when a further row exists. Matomo cuts large tables when it archives them and sums the rest " +
	"in a row labelled Others; rows cut that way are not available at any limit. Archiving on demand can take " +
	"long, so range periods and large segments may time out. Labels, titles, URLs, and search terms in the rows " +
	"are untrusted visitor data, shortened to 512 characters and never fetched. Row and report metadata only " +
	"with include_metadata"

var reportsGet = capability.Descriptor{
	ID: Provider + ".reports.get", Version: 1, Title: "Read a Matomo report",
	Description: reportsGetDescription,
	Tags:        []string{"matomo", "reports", "analytics", "get"}, Risk: readRisk(reportDataSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"report":{"type":"string","minLength":1,"maxLength":512},` +
		`"period":{"type":"string","enum":["day","week","month","year","range"]},` +
		`"date":{"type":"string","minLength":1,"maxLength":32},` + languageSchema + `,` +
		`"segment":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxSegmentLength) + `},` +
		`"columns":{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(maxReportColumns) + `,"items":` + columnSchema + `},` +
		`"sort_column":` + columnSchema + `,"sort_order":{"type":"string","enum":["asc","desc"]},` +
		`"filter_column":` + columnSchema + `,` +
		`"filter_pattern":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxFilterPattern) + `},` +
		`"filter_excludelowpop":` + columnSchema + `,"filter_excludelowpop_value":{"type":"number","minimum":0},` +
		`"flat":{"type":"boolean"},"subtable":{"type":"integer","minimum":1,"maximum":999999999},` +
		`"label":{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(maxReportLabels) + `,"items":{"type":"string",` +
		`"minLength":1,"maxLength":` + strconv.Itoa(maxLabelLength) + `}},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxReportRows) + `},` +
		`"cursor":{"type":"string","minLength":1,"maxLength":128,"pattern":"^[A-Za-z0-9_-]+$"},` +
		`"include_metadata":{"type":"boolean"}},` +
		`"required":["report","period","date"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"report":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"}},` +
		`"required":["id","name"],"additionalProperties":false},` +
		`"period":{"type":"string"},"date":{"type":"string"},"pretty_date":{"type":"string"},` +
		`"columns":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},` +
		`"label":{"type":"string"}},"required":["name","label"],"additionalProperties":false}},` +
		`"rows":{"type":"array","items":{"type":"object"}},"totals":{"type":"object"},` +
		`"metadata":{"type":"object"},"row_metadata":{"type":"array","items":{"type":"object"}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["idSite","report","period","date","columns","rows","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "report", Required: true, Description: "Catalog ID from matomo.reports.list, such as Actions_getPageUrls"},
		{Name: "period", Required: true, Description: "day, week, month, year, or range"},
		{Name: "date", Required: true, Description: "YYYY-MM-DD, today, yesterday, or a range YYYY-MM-DD,YYYY-MM-DD " +
			"or lastN or previousN. With period day, week, month, or year, a range or lastN answers one table per " +
			"period; period range sums the dates into one table"},
		languageArgument(),
		{Name: "segment", Description: "Segment expression such as browserName==Firefox;countryCode==de; fields from " +
			"matomo.segments.fields, values URL-encoded, ; for and, , for or; a segment on a large website can " +
			"make Matomo archive on demand and time out"},
		{Name: "columns", Description: "Columns to return, such as nb_visits or bounce_rate; the label column is " +
			"always kept; all columns of the report when omitted. With flat on a release before Matomo 5.14.0 " +
			"columns is not available"},
		{Name: "sort_column", Description: "Column to sort the rows by before paging; the default of the report when " +
			"omitted"},
		{Name: "sort_order", Description: "asc or desc; desc when omitted"},
		{Name: "filter_column", Description: "Column that filter_pattern is matched against; label when omitted"},
		{Name: "filter_pattern", Description: "Regular expression of the Matomo instance that keeps matching rows, up " +
			"to 100 characters"},
		{Name: "filter_excludelowpop", Description: "Column whose value must be at least " +
			"filter_excludelowpop_value for a row to stay"},
		{Name: "filter_excludelowpop_value", Description: "Minimum value for filter_excludelowpop; 0 when omitted"},
		{Name: "flat", Description: "True to flatten subtables into one list with combined labels; false when omitted"},
		{Name: "subtable", Description: "idsubdatatable of a row (from row_metadata) to read that row's subtable of " +
			"the same report"},
		{Name: "label", Description: "At most 10 row labels; only those rows are returned. Matomo then ignores its " +
			"row limit, so Qatlas pages these rows itself"},
		{Name: "limit", Description: "Rows per period and call, 1 through 1000; 100 when omitted, or 1000 divided by " +
			"the number of periods when that is smaller. limit times the number of periods must not exceed 1000. " +
			"A continuation keeps the limit of its first call"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous call with the same arguments; the first rows " +
			"when omitted"},
		{Name: "include_metadata", Description: "True to add the metadata of the report and of each row, such as " +
			"idsubdatatable and URLs; false when omitted"},
	},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the report belongs to"},
		{Name: "report", Description: "The report that was read: catalog id and name, name untrusted"},
		{Name: "period", Description: "The period asked for"},
		{Name: "date", Description: "The date asked for"},
		{Name: "pretty_date", Description: "Matomo's description of the period, in the language of the request, untrusted"},
		{Name: "columns", Description: "Returned columns in order, as name and translated label, untrusted"},
		{Name: "rows", Description: "Rows as objects of the returned columns with raw numbers, in the order Matomo " +
			"sorted them; labels are untrusted visitor data. Over several periods every row also holds " +
			periodLabelKey + ", the period it belongs to"},
		{Name: "totals", Description: "Totals Matomo reports for the report, absent when it reports none"},
		{Name: "metadata", Description: "Matomo's metadata of the report, only with include_metadata, untrusted"},
		{Name: "row_metadata", Description: "Metadata of each row in the order of rows, such as idsubdatatable, only " +
			"with include_metadata, untrusted"},
		{Name: "has_more", Description: "True when Matomo returned further rows beyond this call; Matomo reports no total"},
		{Name: "next_cursor", Description: "Cursor of the following rows, absent when has_more is false"},
	},
	Examples: []capability.Example{
		{Description: "Top ten pages of last month", Arguments: json.RawMessage(
			`{"idSite":1,"report":"Actions_getPageUrls","period":"month","date":"previous1","limit":10,` +
				`"columns":["nb_hits","nb_visits"],"sort_column":"nb_hits"}`)},
		{Description: "Visits per day of the last 30 days", Arguments: json.RawMessage(
			`{"idSite":1,"report":"VisitsSummary_get","period":"day","date":"last30","columns":["nb_visits"]}`)},
	},
}

const columnSchema = `{"type":"string","pattern":"^[A-Za-z][A-Za-z0-9_.-]{0,63}$","maxLength":64}`

// reportsGetArguments are the arguments of reports.get. The cursor binding holds all of them but the page.
type reportsGetArguments struct {
	IDSite             *int     `json:"idSite"`
	Report             string   `json:"report"`
	Period             string   `json:"period"`
	Date               string   `json:"date"`
	Language           string   `json:"language"`
	Segment            string   `json:"segment"`
	Columns            []string `json:"columns"`
	SortColumn         string   `json:"sort_column"`
	SortOrder          string   `json:"sort_order"`
	FilterColumn       string   `json:"filter_column"`
	FilterPattern      string   `json:"filter_pattern"`
	ExcludeLowPop      string   `json:"filter_excludelowpop"`
	ExcludeLowPopValue *float64 `json:"filter_excludelowpop_value"`
	Flat               bool     `json:"flat"`
	Subtable           *int     `json:"subtable"`
	Label              []string `json:"label"`
	Limit              int      `json:"limit"`
	Cursor             string   `json:"cursor"`
	IncludeMetadata    bool     `json:"include_metadata"`
}

// ReportInfo names the report that was read.
type ReportInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ReportColumn is a returned column with its translated label.
type ReportColumn struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

// ReportResult is the answer of reports.get.
type ReportResult struct {
	IDSite      int              `json:"idSite"`
	Report      ReportInfo       `json:"report"`
	Period      string           `json:"period"`
	Date        string           `json:"date"`
	PrettyDate  string           `json:"pretty_date,omitempty"`
	Columns     []ReportColumn   `json:"columns"`
	Rows        []map[string]any `json:"rows"`
	Totals      map[string]any   `json:"totals,omitempty"`
	Metadata    map[string]any   `json:"metadata,omitempty"`
	RowMetadata []map[string]any `json:"row_metadata,omitempty"`
	HasMore     bool             `json:"has_more"`
	NextCursor  string           `json:"next_cursor,omitempty"`
}

// periodPlan is what the period and date arguments mean: how many tables Matomo answers and whether they come
// as one table per period.
type periodPlan struct {
	points   int
	multiple bool
}

// planPeriod validates period and date and counts the periods a call reads. Period range sums its dates into
// one table; the other periods answer one table per period when the date is a range or lastN or previousN.
func planPeriod(period, date string) (periodPlan, error) {
	const message = "date must be YYYY-MM-DD, today, yesterday, a range YYYY-MM-DD,YYYY-MM-DD, lastN, or previousN"
	if !slices.Contains(periods, period) {
		return periodPlan{}, invalidArgument("period must be day, week, month, year, or range")
	}
	if len(date) > 32 || !datePattern.MatchString(date) {
		return periodPlan{}, invalidArgument(message)
	}
	if first, last, isRange := strings.Cut(date, ","); isRange {
		from, errFrom := time.Parse("2006-01-02", first)
		to, errTo := time.Parse("2006-01-02", last)
		if errFrom != nil || errTo != nil || from.After(to) {
			return periodPlan{}, invalidArgument("the range must hold two valid dates, the first not after the second")
		}
		if period == "range" {
			return periodPlan{points: 1}, nil
		}
		return limitPoints(period, countPeriods(period, from, to), true)
	}
	if match := countedDate.FindStringSubmatch(date); match != nil {
		n, _ := strconv.Atoi(match[2])
		if n < 1 {
			return periodPlan{}, invalidArgument("lastN and previousN need N of at least 1")
		}
		if period == "range" {
			return periodPlan{points: 1}, nil
		}
		return limitPoints(period, n, true)
	}
	if period == "range" {
		return periodPlan{}, invalidArgument("period range needs a range YYYY-MM-DD,YYYY-MM-DD, lastN, or previousN")
	}
	if singleDate.MatchString(date) {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return periodPlan{}, invalidArgument(message)
		}
	}
	return periodPlan{points: 1}, nil
}

func limitPoints(period string, points int, multiple bool) (periodPlan, error) {
	if points > maxReportRows {
		return periodPlan{}, invalidArgument("the date covers " + strconv.Itoa(points) + " " + period + " periods; " +
			"at most " + strconv.Itoa(maxReportRows) + " periods can be read in one call")
	}
	return periodPlan{points: points, multiple: multiple}, nil
}

func countPeriods(period string, from, to time.Time) int {
	switch period {
	case "week":
		return int(mondayOf(to).Sub(mondayOf(from))/(7*24*time.Hour)) + 1
	case "month":
		return (to.Year()-from.Year())*12 + int(to.Month()) - int(from.Month()) + 1
	case "year":
		return to.Year() - from.Year() + 1
	}
	return int(to.Sub(from)/(24*time.Hour)) + 1
}

func mondayOf(day time.Time) time.Time {
	offset := (int(day.Weekday()) + 6) % 7
	return day.AddDate(0, 0, -offset)
}

// reportWindow is the part of the rows one call reads.
type reportWindow struct {
	offset  int
	limit   int
	binding []byte
}

// startReportWindow reads limit and cursor. A cursor carries offset and limit and is bound to the arguments,
// so a changed limit, another report, or a manipulated cursor is refused. limit times the number of periods
// stays within maxReportRows.
func startReportWindow(binding []byte, limit, points int, cursor string) (reportWindow, error) {
	allowed := maxReportRows / points
	if limit != 0 && (limit < 1 || limit > maxReportRows) {
		return reportWindow{}, invalidArgument("limit must be between 1 and " + strconv.Itoa(maxReportRows))
	}
	if limit > allowed {
		return reportWindow{}, invalidArgument("limit " + strconv.Itoa(limit) + " over " + strconv.Itoa(points) +
			" periods would read " + strconv.Itoa(limit*points) + " rows; limit times periods must not exceed " +
			strconv.Itoa(maxReportRows) + ", so use limit " + strconv.Itoa(allowed) + " or less, or fewer periods")
	}
	if cursor == "" {
		if limit == 0 {
			limit = min(defaultReportLimit, allowed)
		}
		return reportWindow{limit: limit, binding: binding}, nil
	}
	const refused = "cursor is not a next_cursor of this report; start the report again without cursor"
	inner, ok := provider.DecodeCursor(binding, cursor, maxCursorLength)
	offsetText, limitText, cut := strings.Cut(inner, ":")
	offset, offsetErr := strconv.Atoi(offsetText)
	size, sizeErr := strconv.Atoi(limitText)
	if !ok || !cut || offsetErr != nil || sizeErr != nil || offset < 1 || offset > maxReportOffset ||
		size < 1 || size > allowed {
		return reportWindow{}, invalidArgument(refused)
	}
	if limit != 0 && limit != size {
		return reportWindow{}, invalidArgument("limit must stay " + strconv.Itoa(size) + " for this cursor, or be left out")
	}
	return reportWindow{offset: offset, limit: size, binding: binding}, nil
}

func (w reportWindow) cursor(offset int) string {
	if offset > maxReportOffset {
		return ""
	}
	return provider.EncodeCursor(w.binding, strconv.Itoa(offset)+":"+strconv.Itoa(w.limit))
}

func noControl(text string) bool {
	return !strings.ContainsFunc(text, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

func validColumn(name string) bool { return namePattern.MatchString(name) }

// validSegmentExpression checks the shape of a segment: terms of field, operator, and value joined by ; (and)
// or , (or). Which fields exist is up to the instance (matomo.segments.fields).
func validSegmentExpression(expression string) bool {
	if expression == "" || len(expression) > maxSegmentLength {
		return false
	}
	terms := strings.FieldsFunc(expression, func(r rune) bool { return r == ';' || r == ',' })
	if len(terms) == 0 || len(terms) > maxSegmentTerms || strings.HasPrefix(expression, ";") ||
		strings.HasPrefix(expression, ",") || strings.HasSuffix(expression, ";") || strings.HasSuffix(expression, ",") ||
		strings.Contains(expression, ";;") || strings.Contains(expression, ",,") ||
		strings.Contains(expression, ";,") || strings.Contains(expression, ",;") {
		return false
	}
	for _, term := range terms {
		if !segmentTerm.MatchString(term) {
			return false
		}
	}
	return true
}

// validate checks everything that needs no instance and returns the period plan.
func (a *reportsGetArguments) validate() (periodPlan, error) {
	if a.Report == "" || len(a.Report) > maxStringLength {
		return periodPlan{}, invalidArgument("report must be a catalog ID from matomo.reports.list")
	}
	plan, err := planPeriod(a.Period, a.Date)
	if err != nil {
		return periodPlan{}, err
	}
	if !validLanguage(a.Language) {
		return periodPlan{}, invalidArgument("language must be a language code such as en or de")
	}
	if a.Segment != "" && !validSegmentExpression(a.Segment) {
		return periodPlan{}, invalidArgument("segment must be terms of field, operator, and URL-encoded value " +
			"joined by ; or , (fields: matomo.segments.fields)")
	}
	if a.Columns != nil && (len(a.Columns) == 0 || len(a.Columns) > maxReportColumns) {
		return periodPlan{}, invalidArgument("columns must hold 1 to " + strconv.Itoa(maxReportColumns) + " names")
	}
	for i, name := range a.Columns {
		if !validColumn(name) || slices.Index(a.Columns, name) != i {
			return periodPlan{}, invalidArgument("columns must be distinct column names such as nb_visits")
		}
	}
	for _, name := range []string{a.SortColumn, a.FilterColumn, a.ExcludeLowPop} {
		if name != "" && !validColumn(name) {
			return periodPlan{}, invalidArgument("column names must be names such as nb_visits")
		}
	}
	if a.SortOrder != "" && a.SortOrder != "asc" && a.SortOrder != "desc" {
		return periodPlan{}, invalidArgument("sort_order must be asc or desc")
	}
	if a.FilterColumn != "" && a.FilterPattern == "" {
		return periodPlan{}, invalidArgument("filter_column needs filter_pattern")
	}
	if len(a.FilterPattern) > maxFilterPattern || !noControl(a.FilterPattern) {
		return periodPlan{}, invalidArgument("filter_pattern must hold at most 100 characters without control characters")
	}
	if a.ExcludeLowPopValue != nil && (a.ExcludeLowPop == "" || *a.ExcludeLowPopValue < 0 || *a.ExcludeLowPopValue > 1e15) {
		return periodPlan{}, invalidArgument("filter_excludelowpop_value needs filter_excludelowpop and a value of 0 or more")
	}
	if a.Subtable != nil && (*a.Subtable < 1 || *a.Subtable > 999999999) {
		return periodPlan{}, invalidArgument("subtable must be the idsubdatatable of a row")
	}
	if a.Label != nil && (len(a.Label) == 0 || len(a.Label) > maxReportLabels) {
		return periodPlan{}, invalidArgument("label must hold 1 to " + strconv.Itoa(maxReportLabels) + " labels")
	}
	for _, label := range a.Label {
		if label == "" || len(label) > maxLabelLength || !noControl(label) {
			return periodPlan{}, invalidArgument("a label must hold 1 to " + strconv.Itoa(maxLabelLength) +
				" characters without control characters")
		}
	}
	return plan, nil
}

func invokeReportsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get report"
	var args reportsGetArguments
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	plan, err := args.validate()
	if err != nil {
		return nil, err
	}
	shape := args
	shape.Limit, shape.Cursor, shape.IncludeMetadata = 0, "", false
	binding := provider.CursorBinding(reportsGet.ID, scopeBinding(resolved), id, shape)
	window, err := startReportWindow(binding, args.Limit, plan.points, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := baseRequirement(reportsGet.ID)
	if args.Flat && len(args.Columns) > 0 {
		req.minVersion = firstColumnFlatten
	}
	// The catalog decides which report exists and with which parameters Matomo is called. It is read without
	// period and date, which Matomo checks again for the report itself.
	catalog, err := client.ReportCatalog(ctx, op, req, id, ReportOptions{Language: args.Language})
	if err != nil {
		return nil, err
	}
	report, err := catalog.Require(op, args.Report)
	if err != nil {
		return nil, err
	}
	params, err := reportParameters(op, id, report, &args, window)
	if err != nil {
		return nil, err
	}
	var answer processedReport
	if err := client.call(ctx, op, req, "API.getProcessedReport", params, &answer); err != nil {
		return nil, err
	}
	return buildReportResult(op, id, report, &args, plan, window, &answer)
}

// reportParameters builds the form of API.getProcessedReport. The module, method, and parameters of the report
// come from the catalog; filter_limit is always the rows of this window plus one.
func reportParameters(op string, idSite int, report Report, args *reportsGetArguments, window reportWindow) (url.Values, error) {
	if !identPattern.MatchString(report.Module) || !identPattern.MatchString(report.Action) {
		return nil, provider.InvalidResponse(op, "the catalog names a report this tool cannot call")
	}
	params := url.Values{
		"idSite": {strconv.Itoa(idSite)}, "period": {args.Period}, "date": {args.Date},
		"apiModule": {report.Module}, "apiAction": {report.Action},
		"hideMetricsDoc": {"1"}, "showTimer": {"0"}, "format_metrics": {"0"},
		"filter_offset": {strconv.Itoa(window.offset)}, "filter_limit": {strconv.Itoa(window.limit + 1)},
	}
	for _, parameter := range report.Parameters {
		if !identPattern.MatchString(parameter.Name) || forbiddenNames[parameter.Name] ||
			strings.HasPrefix(parameter.Name, "filter_") || strings.HasPrefix(parameter.Name, "disable_") ||
			len(parameter.Value) > 128 || !noControl(parameter.Value) {
			return nil, provider.InvalidResponse(op, "the catalog names a report parameter this tool does not send")
		}
		params.Set("apiParameters["+parameter.Name+"]", parameter.Value)
	}
	set := func(name, value string) {
		if value != "" {
			params.Set(name, value)
		}
	}
	set("language", args.Language)
	set("segment", args.Segment)
	set("showColumns", strings.Join(args.Columns, ","))
	set("filter_sort_column", args.SortColumn)
	set("filter_sort_order", args.SortOrder)
	set("filter_column", args.FilterColumn)
	set("filter_pattern", args.FilterPattern)
	set("filter_excludelowpop", args.ExcludeLowPop)
	if args.ExcludeLowPopValue != nil {
		params.Set("filter_excludelowpop_value", strconv.FormatFloat(*args.ExcludeLowPopValue, 'f', -1, 64))
	}
	if args.Flat {
		params.Set("flat", "1")
		if len(args.Columns) > 0 {
			params.Set("keep_flattened_dimension_columns", "1")
		}
	}
	if args.Subtable != nil {
		params.Set("idSubtable", strconv.Itoa(*args.Subtable))
	}
	if len(args.Label) > 0 {
		params["label[]"] = slices.Clone(args.Label)
	}
	return params, nil
}

// processedReport is the answer of API.getProcessedReport. The members stay raw: their order and the shape of
// the tables are read on the Qatlas side.
type processedReport struct {
	PrettyDate     *loose          `json:"prettyDate"`
	Metadata       json.RawMessage `json:"metadata"`
	Columns        json.RawMessage `json:"columns"`
	ReportData     json.RawMessage `json:"reportData"`
	ReportMetadata json.RawMessage `json:"reportMetadata"`
	ReportTotal    json.RawMessage `json:"reportTotal"`
}

type jsonMember struct {
	key   string
	value json.RawMessage
}

// members reads a JSON object in document order; ok is false for anything but an object.
func members(raw json.RawMessage) (out []jsonMember, ok bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, false
	}
	for decoder.More() {
		token, err := decoder.Token()
		key, isKey := token.(string)
		if err != nil || !isKey {
			return nil, false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, false
		}
		out = append(out, jsonMember{key: key, value: value})
	}
	return out, true
}

func isEmptyJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || string(trimmed) == "null" || string(trimmed) == "false" || string(trimmed) == "[]" ||
		string(trimmed) == "{}"
}

// rowList reads one table: a list of row objects, or the single row object of a report without dimension.
func rowList(raw json.RawMessage) ([]json.RawMessage, bool) {
	if isEmptyJSON(raw) {
		return nil, true
	}
	trimmed := bytes.TrimSpace(raw)
	switch trimmed[0] {
	case '[':
		var rows []json.RawMessage
		return rows, json.Unmarshal(trimmed, &rows) == nil
	case '{':
		return []json.RawMessage{trimmed}, true
	}
	return nil, false
}

// decodeValue reads one JSON value keeping numbers as written.
func decodeValue(raw json.RawMessage) (any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	return value, decoder.Decode(&value) == nil
}

// cleanValue bounds a provider value: strings are cut, lists and objects are limited in size and depth.
func cleanValue(value any, depth int) (any, bool) {
	switch v := value.(type) {
	case string:
		return boundString(v, maxReportText), true
	case json.Number, bool, nil:
		return v, true
	case []any:
		if depth >= maxValueDepth {
			return nil, false
		}
		out := make([]any, 0, min(len(v), maxReportItems))
		for _, item := range v {
			if cleaned, ok := cleanValue(item, depth+1); ok && len(out) < maxReportItems {
				out = append(out, cleaned)
			}
		}
		return out, true
	case map[string]any:
		if depth >= maxValueDepth {
			return nil, false
		}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		out := map[string]any{}
		for _, key := range keys {
			if cleaned, ok := cleanValue(v[key], depth+1); ok && len(out) < maxReportItems {
				out[bounded(key)] = cleaned
			}
		}
		return out, true
	}
	return nil, false
}

func cleanObject(raw json.RawMessage) map[string]any {
	value, ok := decodeValue(raw)
	object, isObject := value.(map[string]any)
	if !ok || !isObject {
		return nil
	}
	cleaned, _ := cleanValue(object, 0)
	out, _ := cleaned.(map[string]any)
	return out
}

// reportRow keeps the scalar members of a row: labels shortened, numbers as Matomo wrote them. Nested members
// such as comparisons are dropped.
func reportRow(raw json.RawMessage) map[string]any {
	row := map[string]any{}
	fields, ok := members(raw)
	if !ok {
		return row
	}
	for _, field := range fields {
		value, ok := decodeValue(field.value)
		if !ok {
			continue
		}
		switch v := value.(type) {
		case string:
			row[bounded(field.key)] = boundString(v, maxStringLength)
		case json.Number, bool, nil:
			row[bounded(field.key)] = v
		}
	}
	return row
}

// reportTable is the table of one period.
type reportTable struct {
	label string
	rows  []json.RawMessage
	meta  []json.RawMessage
}

func readTables(op string, answer *processedReport, plan periodPlan) ([]reportTable, error) {
	invalid := provider.InvalidResponse(op, "Matomo returned a report in a form this tool does not read")
	if !plan.multiple {
		rows, ok := rowList(answer.ReportData)
		if !ok {
			return nil, invalid
		}
		meta, _ := rowList(answer.ReportMetadata)
		return []reportTable{{rows: rows, meta: meta}}, nil
	}
	if isEmptyJSON(answer.ReportData) {
		return nil, nil
	}
	tables, ok := members(answer.ReportData)
	if !ok {
		return nil, invalid
	}
	metaTables, _ := members(answer.ReportMetadata)
	out := make([]reportTable, 0, len(tables))
	for _, entry := range tables {
		rows, ok := rowList(entry.value)
		if !ok || len(out) >= maxReportRows {
			return nil, invalid
		}
		table := reportTable{label: boundString(entry.key, maxStringLength), rows: rows}
		for _, candidate := range metaTables {
			if candidate.key == entry.key {
				table.meta, _ = rowList(candidate.value)
				break
			}
		}
		out = append(out, table)
	}
	return out, nil
}

func buildReportResult(op string, idSite int, report Report, args *reportsGetArguments, plan periodPlan,
	window reportWindow, answer *processedReport) (any, error) {
	tables, err := readTables(op, answer, plan)
	if err != nil {
		return nil, err
	}
	result := &ReportResult{IDSite: idSite, Report: ReportInfo{ID: report.ID, Name: report.Name}, Period: args.Period,
		Date: args.Date, PrettyDate: answer.PrettyDate.str(maxStringLength), Columns: []ReportColumn{},
		Rows: []map[string]any{}}
	if columns, ok := members(answer.Columns); ok {
		for _, column := range columns {
			if value, ok := decodeValue(column.value); ok && len(result.Columns) < maxReportItems {
				label, _ := value.(string)
				result.Columns = append(result.Columns, ReportColumn{Name: bounded(column.key), Label: boundString(label, maxStringLength)})
			}
		}
	}
	// Matomo skips the offset and limit when a label filter is set, and a report may disable its generic
	// filters; then the window is cut here.
	clientWindow := len(args.Label) > 0
	hasMore := false
	for _, table := range tables {
		rows, meta := table.rows, table.meta
		if len(meta) != len(rows) {
			meta = nil
		}
		if clientWindow || len(rows) > window.limit+1 {
			skip := min(window.offset, len(rows))
			rows = rows[skip:]
			if meta != nil {
				meta = meta[skip:]
			}
		}
		if len(rows) > window.limit {
			hasMore = true
			rows = rows[:window.limit]
		}
		for i, rawRow := range rows {
			row := reportRow(rawRow)
			if plan.multiple {
				row[periodLabelKey] = table.label
			}
			result.Rows = append(result.Rows, row)
			if args.IncludeMetadata {
				entry := map[string]any{}
				if meta != nil {
					if cleaned := cleanObject(meta[i]); cleaned != nil {
						entry = cleaned
					}
				}
				result.RowMetadata = append(result.RowMetadata, entry)
			}
		}
	}
	if hasMore {
		result.HasMore = true
		result.NextCursor = window.cursor(window.offset + window.limit)
	}
	if totals := cleanObject(answer.ReportTotal); len(totals) > 0 {
		result.Totals = totals
	}
	if args.IncludeMetadata {
		result.Metadata = cleanObject(answer.Metadata)
	}
	return result, nil
}
