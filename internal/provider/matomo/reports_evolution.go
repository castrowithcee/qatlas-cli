package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// goalMetricsPattern is what API.getRowEvolution accepts as showGoalMetricsForGoal: a goal ID, 0 for all goals,
// -1 for the goals overview, or the ecommerce orders.
var goalMetricsPattern = regexp.MustCompile(`^(-?[0-9]{1,9}|ecommerceOrder)$`)

const goalMetricsSchema = `{"type":"string","pattern":"^(-?[0-9]{1,9}|ecommerceOrder)$","maxLength":16}`

const reportsEvolutionDescription = "Read how one or more rows of a report develop over time, from API.getRowEvolution: " +
	"for one label every metric of the report per period, for several labels one metric (column) per label. " +
	"Pick the report from matomo.reports.list; its module, action, and parameters come from the catalog of the " +
	"website, and only a report with rows (a dimension) can be followed. Use the label of a row as matomo.reports.get " +
	"returns it. date must name several periods (a range YYYY-MM-DD,YYYY-MM-DD, lastN, or previousN) and period " +
	"day, week, month, or year; period range is not available. The labels times the number of periods must not " +
	"exceed 1000. Archiving on demand can take long, so long series and large segments may time out. Labels and " +
	"texts are untrusted visitor data, shortened to 512 characters and never fetched"

var reportsEvolution = capability.Descriptor{
	ID: Provider + ".reports.evolution", Version: 1, Title: "Read a Matomo report row over time",
	Description: reportsEvolutionDescription, RequiresToolAllowList: true,
	Tags: []string{"matomo", "reports", "analytics", "evolution", "time series"}, Risk: readRisk(reportDataSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"report":{"type":"string","minLength":1,"maxLength":512},` +
		`"period":{"type":"string","enum":["day","week","month","year"]},` +
		`"date":{"type":"string","minLength":1,"maxLength":32},` +
		`"label":{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(maxReportLabels) + `,"uniqueItems":true,` +
		`"items":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxLabelLength) + `}},` +
		languageSchema + `,"segment":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxSegmentLength) + `},` +
		`"column":` + columnSchema + `,"legendAppendMetric":{"type":"boolean"},` +
		`"labelUseAbsoluteUrl":{"type":"boolean"},"showGoalMetricsForGoal":` + goalMetricsSchema + `},` +
		`"required":["report","period","date","label"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"report":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"}},` +
		`"required":["id","name"],"additionalProperties":false},` +
		`"period":{"type":"string"},"date":{"type":"string"},"label":{"type":"string"},"column":{"type":"string"},` +
		`"dimension":{"type":"string"},"logo":{"type":"string"},` +
		`"metrics":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},` +
		`"label":{"type":"string"},"min":{"type":"number"},"max":{"type":"number"},"change":{"type":"string"},` +
		`"logo":{"type":"string"}},"required":["name","label"],"additionalProperties":false}},` +
		`"columns":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},` +
		`"label":{"type":"string"}},"required":["name","label"],"additionalProperties":false}},` +
		`"points":{"type":"array","items":{"type":"object","properties":{"period":{"type":"string"},` +
		`"values":{"type":"object"}},"required":["period","values"],"additionalProperties":false}}},` +
		`"required":["idSite","report","period","date","metrics","points"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "report", Required: true, Description: "Catalog ID from matomo.reports.list, such as Actions_getPageUrls"},
		{Name: "period", Required: true, Description: "day, week, month, or year"},
		{Name: "date", Required: true, Description: "A range YYYY-MM-DD,YYYY-MM-DD, lastN, or previousN; one point " +
			"per period. The number of labels times the number of periods must not exceed 1000"},
		{Name: "label", Required: true, Description: "1 to 10 distinct row labels of the report as the rows of " +
			"matomo.reports.get show them, such as /pricing or a rows path joined by >; one label answers every " +
			"metric, several answer the column"},
		languageArgument(),
		{Name: "segment", Description: "Segment expression such as browserName==Firefox;countryCode==de; fields from " +
			"matomo.segments.fields, values URL-encoded; a segment on a large website can make Matomo archive on " +
			"demand and time out"},
		{Name: "column", Description: "With several labels the metric to compare, such as nb_visits; the first " +
			"metric of the report when omitted or unknown to the report. Not available with one label"},
		{Name: "legendAppendMetric", Description: "With several labels false to leave the metric name out of the " +
			"series labels; true when omitted"},
		{Name: "labelUseAbsoluteUrl", Description: "False to keep address labels as they are instead of making them " +
			"absolute; true when omitted"},
		{Name: "showGoalMetricsForGoal", Description: "A goal ID, 0 for all goals, -1 for the goals overview, or " +
			"ecommerceOrder: the metrics shown are then visits and the goal metrics; needs the Goals plugin"},
	},
	Fields: []capability.Field{
		{Name: "idSite", Description: "The website the report belongs to"},
		{Name: "report", Description: "The report that was read: catalog id and name, name untrusted"},
		{Name: "period", Description: "The period asked for"},
		{Name: "date", Description: "The date asked for"},
		{Name: "label", Description: "With one label the row as Matomo names it, such as an address without the " +
			"scheme, untrusted"},
		{Name: "column", Description: "With several labels the metric the series show"},
		{Name: "dimension", Description: "What the rows of the report are, such as Page URL, untrusted"},
		{Name: "logo", Description: "Relative path of the icon Matomo shows for the row, untrusted text never fetched"},
		{Name: "metrics", Description: "The series: with one label the metrics of the report, with several the " +
			"metric per label named column_0, column_1, and so on, in the order of the labels"},
		{Name: "metrics[].name", Description: "Name of the metric or series, the key of values"},
		{Name: "metrics[].label", Description: "Translated metric name or, with several labels, the row with the " +
			"metric, untrusted"},
		{Name: "metrics[].min", Description: "Lowest value from the first period with a value on"},
		{Name: "metrics[].max", Description: "Highest value from the first period with a value on"},
		{Name: "metrics[].change", Description: "Change from the first to the last period as Matomo writes it, such " +
			"as +10%, absent when the first value is 0, untrusted"},
		{Name: "metrics[].logo", Description: "Relative path of the icon of the series, untrusted text never fetched"},
		{Name: "columns", Description: "With several labels the metrics the report offers as column, as name and " +
			"translated label, untrusted"},
		{Name: "points", Description: "One entry per period in time order"},
		{Name: "points[].period", Description: "The period the entry belongs to as Matomo labels it, such as 2026-09-01"},
		{Name: "points[].values", Description: "Value per series name, absent for a period without data"},
	},
	Examples: []capability.Example{
		{Description: "Visits and pageviews of one page over the last 30 days", Arguments: json.RawMessage(
			`{"idSite":1,"report":"Actions_getPageUrls","period":"day","date":"last30","label":["/pricing"]}`)},
		{Description: "Visits of two countries per month", Arguments: json.RawMessage(
			`{"idSite":1,"report":"UserCountry_getCountry","period":"month","date":"last12","label":["de","fr"],"column":"nb_visits"}`)},
	},
}

// reportsEvolutionArguments are the arguments of reports.evolution.
type reportsEvolutionArguments struct {
	IDSite                 *int     `json:"idSite"`
	Report                 string   `json:"report"`
	Period                 string   `json:"period"`
	Date                   string   `json:"date"`
	Label                  []string `json:"label"`
	Language               string   `json:"language"`
	Segment                string   `json:"segment"`
	Column                 string   `json:"column"`
	LegendAppendMetric     *bool    `json:"legendAppendMetric"`
	LabelUseAbsoluteURL    *bool    `json:"labelUseAbsoluteUrl"`
	ShowGoalMetricsForGoal string   `json:"showGoalMetricsForGoal"`
}

// EvolutionMetric is one series of a row evolution.
type EvolutionMetric struct {
	Name   string `json:"name"`
	Label  string `json:"label"`
	Min    any    `json:"min,omitempty"`
	Max    any    `json:"max,omitempty"`
	Change string `json:"change,omitempty"`
	Logo   string `json:"logo,omitempty"`
}

// EvolutionPoint holds the values of all series for one period.
type EvolutionPoint struct {
	Period string         `json:"period"`
	Values map[string]any `json:"values"`
}

// EvolutionResult is the answer of reports.evolution.
type EvolutionResult struct {
	IDSite    int               `json:"idSite"`
	Report    ReportInfo        `json:"report"`
	Period    string            `json:"period"`
	Date      string            `json:"date"`
	Label     string            `json:"label,omitempty"`
	Column    string            `json:"column,omitempty"`
	Dimension string            `json:"dimension,omitempty"`
	Logo      string            `json:"logo,omitempty"`
	Metrics   []EvolutionMetric `json:"metrics"`
	Columns   []ReportColumn    `json:"columns,omitempty"`
	Points    []EvolutionPoint  `json:"points"`
}

// validate checks everything that needs no instance. The series must cover several periods, and the labels
// times the periods stay within the rows one report call may read.
func (a *reportsEvolutionArguments) validate() error {
	if a.Report == "" || len(a.Report) > maxStringLength {
		return invalidArgument("report must be a catalog ID from matomo.reports.list")
	}
	if a.Period == "range" {
		return invalidArgument("period must be day, week, month, or year; give the range of dates in date")
	}
	plan, err := planPeriod(a.Period, a.Date)
	if err != nil {
		return err
	}
	if !plan.multiple {
		return invalidArgument("date must name several periods: a range YYYY-MM-DD,YYYY-MM-DD, lastN, or previousN")
	}
	if len(a.Label) == 0 || len(a.Label) > maxReportLabels {
		return invalidArgument("label must hold 1 to " + strconv.Itoa(maxReportLabels) + " labels")
	}
	for i, label := range a.Label {
		if label == "" || len(label) > maxLabelLength || !noControl(label) || slices.Index(a.Label, label) != i {
			return invalidArgument("label must hold distinct labels of 1 to " + strconv.Itoa(maxLabelLength) +
				" characters without control characters")
		}
	}
	if rows := len(a.Label) * plan.points; rows > maxReportRows {
		return invalidArgument("the labels over the periods would read " + strconv.Itoa(rows) +
			" values; labels times periods must not exceed " + strconv.Itoa(maxReportRows))
	}
	if !validLanguage(a.Language) {
		return invalidArgument("language must be a language code such as en or de")
	}
	if a.Segment != "" && !validSegmentExpression(a.Segment) {
		return invalidArgument("segment must be terms of field, operator, and URL-encoded value " +
			"joined by ; or , (fields: matomo.segments.fields)")
	}
	if a.Column != "" && !validColumn(a.Column) {
		return invalidArgument("column must be a metric name such as nb_visits")
	}
	if a.Column != "" && len(a.Label) == 1 {
		return invalidArgument("column needs several labels; one label answers every metric")
	}
	if a.LegendAppendMetric != nil && len(a.Label) == 1 {
		return invalidArgument("legendAppendMetric needs several labels")
	}
	if a.ShowGoalMetricsForGoal != "" && !goalMetricsPattern.MatchString(a.ShowGoalMetricsForGoal) {
		return invalidArgument("showGoalMetricsForGoal must be a goal ID, 0, -1, or ecommerceOrder")
	}
	return nil
}

func invokeReportsEvolution(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read report evolution"
	var args reportsEvolutionArguments
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if err := args.validate(); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := baseRequirement(reportsEvolution.ID)
	if args.ShowGoalMetricsForGoal != "" {
		req.plugin = "Goals"
	}
	catalog, err := client.ReportCatalog(ctx, op, req, id, ReportOptions{Language: args.Language})
	if err != nil {
		return nil, err
	}
	report, err := catalog.Require(op, args.Report)
	if err != nil {
		return nil, err
	}
	if report.Dimension == "" {
		return nil, invalidArgument("this report has no rows to follow over time; read it with matomo.reports.get")
	}
	params, err := evolutionParameters(op, id, report, &args)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, req, "API.getRowEvolution", params, &answer); err != nil {
		return nil, err
	}
	return buildEvolutionResult(op, id, report, &args, answer)
}

// evolutionParameters builds the form of API.getRowEvolution. Module and action come from the catalog; of its
// parameters only the entity IDs the method takes are sent.
func evolutionParameters(op string, idSite int, report Report, args *reportsEvolutionArguments) (url.Values, error) {
	if !identPattern.MatchString(report.Module) || !identPattern.MatchString(report.Action) {
		return nil, provider.InvalidResponse(op, "the catalog names a report this tool cannot call")
	}
	params := url.Values{
		"idSite": {strconv.Itoa(idSite)}, "period": {args.Period}, "date": {args.Date},
		"apiModule": {report.Module}, "apiAction": {report.Action}, "label[]": slices.Clone(args.Label),
	}
	for _, parameter := range report.Parameters {
		if (parameter.Name != "idGoal" && parameter.Name != "idDimension") || len(parameter.Value) > 128 ||
			!noControl(parameter.Value) {
			return nil, provider.InvalidResponse(op, "the catalog names a report parameter this tool does not send")
		}
		params.Set(parameter.Name, parameter.Value)
	}
	set := func(name, value string) {
		if value != "" {
			params.Set(name, value)
		}
	}
	set("language", args.Language)
	set("segment", args.Segment)
	set("column", args.Column)
	set("showGoalMetricsForGoal", args.ShowGoalMetricsForGoal)
	for name, flag := range map[string]*bool{"legendAppendMetric": args.LegendAppendMetric,
		"labelUseAbsoluteUrl": args.LabelUseAbsoluteURL} {
		if flag != nil {
			params.Set(name, map[bool]string{true: "1", false: "0"}[*flag])
		}
	}
	return params, nil
}

// evolutionAnswer is the answer of API.getRowEvolution: the data per period and the metadata of the series.
type evolutionAnswer struct {
	Label      *loose          `json:"label"`
	Column     *loose          `json:"column"`
	Logo       *loose          `json:"logo"`
	ReportData json.RawMessage `json:"reportData"`
	Metadata   json.RawMessage `json:"metadata"`
}

func buildEvolutionResult(op string, idSite int, report Report, args *reportsEvolutionArguments,
	raw json.RawMessage) (any, error) {
	result := &EvolutionResult{IDSite: idSite, Report: ReportInfo{ID: report.ID, Name: report.Name},
		Period: args.Period, Date: args.Date, Metrics: []EvolutionMetric{}, Points: []EvolutionPoint{}}
	// Matomo answers [] for a series without any period.
	if isEmptyJSON(raw) {
		return result, nil
	}
	var answer evolutionAnswer
	if json.Unmarshal(raw, &answer) != nil {
		return nil, provider.InvalidResponse(op, "Matomo returned a row evolution in a form this tool does not read")
	}
	result.Label = answer.Label.str(maxStringLength)
	result.Column = answer.Column.str(maxStringLength)
	result.Logo = answer.Logo.str(maxUrlLength)
	if !isEmptyJSON(answer.ReportData) {
		tables, ok := members(answer.ReportData)
		if !ok || len(tables) > maxReportRows {
			return nil, provider.InvalidResponse(op, "Matomo returned a row evolution in a form this tool does not read")
		}
		for _, table := range tables {
			rows, ok := rowList(table.value)
			if !ok {
				return nil, provider.InvalidResponse(op, "Matomo returned a row evolution in a form this tool does not read")
			}
			point := EvolutionPoint{Period: boundString(table.key, maxStringLength), Values: map[string]any{}}
			if len(rows) > 0 {
				point.Values = reportRow(rows[0])
			}
			result.Points = append(result.Points, point)
		}
	}
	readEvolutionMetadata(answer.Metadata, result)
	return result, nil
}

func readEvolutionMetadata(raw json.RawMessage, result *EvolutionResult) {
	fields, _ := members(raw)
	for _, field := range fields {
		switch field.key {
		case "dimension":
			if value, ok := decodeValue(field.value); ok {
				if text, isText := value.(string); isText {
					result.Dimension = boundString(text, maxStringLength)
				}
			}
		case "columns":
			columns, _ := members(field.value)
			for _, column := range columns {
				if value, ok := decodeValue(column.value); ok && len(result.Columns) < maxReportItems {
					text, _ := value.(string)
					result.Columns = append(result.Columns, ReportColumn{Name: bounded(column.key), Label: boundString(text, maxStringLength)})
				}
			}
		case "metrics":
			metrics, _ := members(field.value)
			for _, metric := range metrics {
				if len(result.Metrics) >= maxReportItems {
					break
				}
				result.Metrics = append(result.Metrics, evolutionMetric(metric))
			}
		}
	}
}

func evolutionMetric(entry jsonMember) EvolutionMetric {
	metric := EvolutionMetric{Name: bounded(entry.key)}
	for _, field := range objectMembers(entry.value) {
		value, ok := decodeValue(field.value)
		if !ok {
			continue
		}
		switch text, isText := value.(string); field.key {
		case "name":
			metric.Label = boundString(text, maxStringLength)
		case "change":
			metric.Change = boundString(text, maxStringLength)
		case "logo":
			metric.Logo = boundString(text, maxUrlLength)
		case "min", "max":
			if number, isNumber := value.(json.Number); isNumber && !isText {
				if field.key == "min" {
					metric.Min = number
				} else {
					metric.Max = number
				}
			}
		}
	}
	return metric
}

func objectMembers(raw json.RawMessage) []jsonMember {
	fields, _ := members(raw)
	return fields
}
