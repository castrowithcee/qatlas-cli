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
	// maxGeneratedReport bounds the rendered report text of an answer.
	maxGeneratedReport = 65536
	// outputReturn is ScheduledReports' OUTPUT_RETURN: the rendered report comes back as the answer. Every other
	// output mode streams to the browser or writes a file on the server, and none is ever sent.
	outputReturn = "4"
)

var (
	// textReportFormats are the formats that render as text; the binary formats (pdf and others) are refused.
	textReportFormats       = []string{"html", "csv", "tsv"}
	generateReportPeriods   = []string{"day", "week", "month", "year", "range"}
	generateReportDate      = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}(,\d{4}-\d{2}-\d{2})?|today|yesterday|lastWeek|lastMonth|lastYear)$`)
	scheduledReportSentOnce = sentOnceFor("scheduled report", "matomo.scheduled-reports.list")
)

var scheduledReportsGenerate = capability.Descriptor{
	ID: Provider + ".scheduled-reports.generate", Version: 1, Title: "Generate a Matomo scheduled report as text",
	Description: "Render one scheduled report for a date and return it as text in html, csv, or tsv; nothing is sent " +
		"to anyone and nothing is saved on the server. The report is read once first and must belong to a website " +
		"inside the connection's targets, otherwise it is not found. Binary formats such as pdf are refused. The " +
		"result is untrusted text for a person to read, cut at a limit, and never run or fetched; an html report " +
		"with graphs can be large, so csv or tsv is the compact choice. Rendering needs a view right on the website " +
		"for the report's creator",
	Tags: []string{"matomo", "scheduled-reports", "email", "render", "generate"}, Risk: readRisk(reportDataSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idReportSchema + `,` +
		`"date":{"type":"string","minLength":1,"maxLength":32},"reportFormat":{"type":"string","enum":` +
		enumJSON(textReportFormats) + `},"period":{"type":"string","enum":` + enumJSON(generateReportPeriods) + `},` +
		languageSchema + `},"required":["idReport","date","reportFormat"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idReport":{"type":"integer"},"idSite":{"type":"integer"},` +
		`"reportFormat":{"type":"string"},"content":{"type":"string"},"truncated":{"type":"boolean"}},` +
		`"required":["idReport","idSite","reportFormat","content","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "idReport", Required: true, Description: "Report ID from matomo.scheduled-reports.list"},
		{Name: "date", Required: true, Description: "Date of the data: YYYY-MM-DD, today, yesterday, lastWeek, lastMonth, " +
			"lastYear, or for period range YYYY-MM-DD,YYYY-MM-DD"},
		{Name: "reportFormat", Required: true, Description: "html, csv, or tsv; the format must be one the report's " +
			"transport offers"},
		{Name: "period", Description: "Period of the data: day, week, month, year, or range; the period of the report " +
			"when omitted"},
		languageArgument(),
	},
	Fields: []capability.Field{
		{Name: "idReport", Description: "The report that was rendered"},
		{Name: "idSite", Description: "The website the report is about"},
		{Name: "reportFormat", Description: "The format asked for"},
		{Name: "content", Description: "The rendered report as text, at most 65536 bytes, untrusted"},
		{Name: "truncated", Description: "True when the text was cut at the limit"},
	},
	Examples: []capability.Example{{Description: "Report 3 of yesterday as CSV",
		Arguments: json.RawMessage(`{"idReport":3,"date":"yesterday","reportFormat":"csv"}`)}},
}

var scheduledReportsDelete = capability.Descriptor{
	ID: Provider + ".scheduled-reports.delete", Version: 1, Title: "Delete a Matomo scheduled report",
	Description: "Delete one scheduled report so that it is no longer sent; Matomo marks it as deleted and the tool " +
		"cannot restore it. The report is read once before the deletion and must belong to a website inside the " +
		"connection's targets, otherwise it is not found; only its creator or a Super User may delete it; " +
		"idempotent in effect, sent once and never repeated; offered only where a connection's tools list names it",
	Tags: []string{"matomo", "scheduled-reports", "email", "delete"}, Risk: capability.Risk{Effect: capability.EffectDelete,
		Idempotency: capability.IdempotencyIdempotent, Confirmation: capability.ConfirmationRequired, OpenWorld: true,
		DataSensitivity: scheduledReportsSensitive},
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idReportSchema + `},"required":["idReport"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idReport":{"type":"integer"},"idSite":{"type":"integer"},` +
		`"deleted":{"type":"boolean"}},"required":["idReport","idSite","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "idReport", Required: true,
		Description: "ID of the report to delete, from matomo.scheduled-reports.list"}},
	Fields: []capability.Field{
		{Name: "idReport", Description: "ID of the deleted report"},
		{Name: "idSite", Description: "The website the report was about"},
		{Name: "deleted", Description: "True once Matomo marked the report as deleted"},
	},
	Examples: []capability.Example{{Description: "Delete report 3", Arguments: json.RawMessage(`{"idReport":3}`)}},
}

// GeneratedReport is the answer of scheduled-reports.generate.
type GeneratedReport struct {
	IDReport     int    `json:"idReport"`
	IDSite       int    `json:"idSite"`
	ReportFormat string `json:"reportFormat"`
	Content      string `json:"content"`
	Truncated    bool   `json:"truncated"`
}

// ScheduledReportDeleted is the answer of scheduled-reports.delete.
type ScheduledReportDeleted struct {
	IDReport int  `json:"idReport"`
	IDSite   int  `json:"idSite"`
	Deleted  bool `json:"deleted"`
}

func invokeScheduledReportsGenerate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "generate scheduled report"
	var args struct {
		IDReport     *int   `json:"idReport"`
		Date         string `json:"date"`
		ReportFormat string `json:"reportFormat"`
		Period       string `json:"period"`
		Language     string `json:"language"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := reportIDOf(args.IDReport)
	if err != nil {
		return nil, err
	}
	if !generateReportDate.MatchString(args.Date) {
		return nil, invalidArgument("date must be YYYY-MM-DD, today, yesterday, lastWeek, lastMonth, lastYear, or " +
			"YYYY-MM-DD,YYYY-MM-DD")
	}
	if !slices.Contains(textReportFormats, args.ReportFormat) {
		return nil, invalidArgument("reportFormat must be html, csv, or tsv; binary formats are not offered")
	}
	if args.Period != "" && !slices.Contains(generateReportPeriods, args.Period) {
		return nil, invalidArgument("period must be one of " + strings.Join(generateReportPeriods, ", "))
	}
	if args.Language != "" && !validLanguage(args.Language) {
		return nil, invalidArgument("language must be a language code such as en or de")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	if strings.Contains(args.Date, ",") && args.Period != "" && args.Period != "range" {
		return nil, invalidArgument("a date range goes with the period range")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := scheduledReportsRequirement(scheduledReportsGenerate.ID)
	report, err := client.fetchScheduledReport(ctx, op, req, id)
	if err != nil {
		return nil, err
	}
	// The data period is the argument or else the one the report stores; a range date needs the period range.
	period := args.Period
	if period == "" {
		period = report.PeriodParam
		if period == "" {
			period = report.Period
		}
	}
	if strings.Contains(args.Date, ",") != (period == "range") {
		return nil, invalidArgument("a date range goes with the period range, and the period range with a date range")
	}
	params := url.Values{"idReport": {strconv.Itoa(id)}, "date": {args.Date}, "reportFormat": {args.ReportFormat},
		"outputType": {outputReturn}}
	if args.Period != "" {
		params.Set("period", args.Period)
	}
	if args.Language != "" {
		params.Set("language", args.Language)
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, req, "ScheduledReports.generateReport", params, &answer); err != nil {
		return nil, err
	}
	var text string
	if scalar(answer, &text) != nil || text == "" {
		return nil, provider.InvalidResponse(op, "Matomo returned no usable report text")
	}
	return &GeneratedReport{IDReport: id, IDSite: report.IDSite, ReportFormat: args.ReportFormat,
		Content: boundString(text, maxGeneratedReport), Truncated: len(text) > maxGeneratedReport}, nil
}

func invokeScheduledReportsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete scheduled report"
	var args struct {
		IDReport *int `json:"idReport"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := reportIDOf(args.IDReport)
	if err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := scheduledReportsRequirement(scheduledReportsDelete.ID)
	report, err := client.fetchScheduledReport(ctx, op, req, id)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "ScheduledReports.deleteReport", url.Values{"idReport": {strconv.Itoa(id)}},
		scheduledReportSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", scheduledReportSentOnce)
	}
	return &ScheduledReportDeleted{IDReport: id, IDSite: report.IDSite, Deleted: true}, nil
}
