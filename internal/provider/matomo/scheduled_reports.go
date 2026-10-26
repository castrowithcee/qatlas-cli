package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// scheduledReportsSensitive covers recipients (e-mail addresses, phone numbers) and logins, which are personal data.
	scheduledReportsSensitive = "matomo-scheduled-reports"
	scheduledReportsPlugin    = "ScheduledReports"
	maxReportParts            = 200
	maxRecipients             = 100
	maxRecipientText          = 320
)

var scheduledReportPeriods = []string{"day", "week", "month", "never"}

func scheduledReportsRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: scheduledReportsPlugin}
}

const scheduledReportProperties = `"idReport":{"type":"integer"},"idSite":{"type":"integer"},"login":{"type":"string"},` +
	`"description":{"type":"string"},"idSegment":{"type":"integer"},"period":{"type":"string"},` +
	`"periodParam":{"type":"string"},"hour":{"type":"integer"},"type":{"type":"string"},"format":{"type":"string"},` +
	`"reports":{"type":"array","items":{"type":"string"}},"emailMe":{"type":"boolean"},` +
	`"additionalEmails":{"type":"array","items":{"type":"string"}},"phoneNumbers":{"type":"array","items":{"type":"string"}},` +
	`"evolutionGraph":{"type":"boolean"},"displayFormat":{"type":"integer"},"enforceOrder":{"type":"boolean"},` +
	`"tsCreated":{"type":"string"},"tsLastSent":{"type":"string"},"evolutionGraphWithinPeriod":{"type":"boolean"},` +
	`"evolutionGraphPeriodN":{"type":"integer"}`

const idReportSchema = `"idReport":{"type":"integer","minimum":1,"maximum":999999999}`

var scheduledReportsList = capability.Descriptor{
	ID: Provider + ".scheduled-reports.list", Version: 1, Title: "List Matomo scheduled reports",
	Description: "List the scheduled e-mail reports the token's user may see, ordered by description as Matomo gives " +
		"them (a Super User sees all reports, other users their own). Reports of a website outside the connection's " +
		"targets are left out. The recipients (additional e-mail addresses, phone numbers) and the creator's login " +
		"are personal data; descriptions are untrusted text. The tool only reads the definition and sends nothing",
	Tags: []string{"matomo", "scheduled-reports", "email", "recipients", "list"}, Risk: readRisk(scheduledReportsSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,"period":{"type":"string","enum":` +
		enumJSON(scheduledReportPeriods) + `},` + idReportSchema + `,"ifSuperUserReturnOnlySuperUserReports":` +
		`{"type":"boolean"},` + idSegmentSchema + `,` + pagingKeys + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"reports":{"type":"array","items":{"type":"object",` +
		`"properties":{` + scheduledReportProperties + `},"required":["idReport","idSite"],"additionalProperties":false}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},"required":["reports","has_more"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "idSite", Target: "site", Description: "Only the reports of this website; the reports of every website of " +
			"the connection when omitted"},
		{Name: "period", Description: "Only the reports sent at this schedule: day, week, month, or never"},
		{Name: "idReport", Description: "Only this report; Matomo answers an unknown ID as not found"},
		{Name: "ifSuperUserReturnOnlySuperUserReports", Description: "True to return only the reports of the token's " +
			"own user even for a Super User; false when omitted"},
		{Name: "idSegment", Description: "Only the reports that use this saved segment"},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "reports", Description: "Scheduled reports in the order Matomo lists them"},
		{Name: "reports[].idReport", Description: "Report ID, used as idReport by the other scheduled-report tools"},
		{Name: "reports[].idSite", Description: "The website the report is about"},
		{Name: "reports[].login", Description: "Login of the user who created the report, untrusted, personal data"},
		{Name: "reports[].description", Description: "Description of the report, untrusted"},
		{Name: "reports[].idSegment", Description: "Saved segment the report uses; absent for none"},
		{Name: "reports[].period", Description: "Schedule: day, week, month, or never"},
		{Name: "reports[].periodParam", Description: "Period of the data in the report"},
		{Name: "reports[].hour", Description: "Hour of sending as Matomo stores it"},
		{Name: "reports[].type", Description: "Transport of the report, such as email, untrusted"},
		{Name: "reports[].format", Description: "Output format of the report, such as html, pdf, csv, or tsv, untrusted"},
		{Name: "reports[].reports", Description: "Unique IDs of the reports inside, at most 200, untrusted"},
		{Name: "reports[].emailMe", Description: "True when the creator receives the report too"},
		{Name: "reports[].additionalEmails", Description: "Further e-mail recipients, at most 100, untrusted, personal data"},
		{Name: "reports[].phoneNumbers", Description: "Phone number recipients of a mobile report, at most 100, untrusted, " +
			"personal data"},
		{Name: "reports[].evolutionGraph", Description: "True when evolution graphs are included"},
		{Name: "reports[].displayFormat", Description: "Matomo's display format number of the tables and graphs"},
		{Name: "reports[].enforceOrder", Description: "True when the stored order of the inner reports is kept"},
		{Name: "reports[].tsCreated", Description: "Creation time as Matomo stores it (UTC)"},
		{Name: "reports[].tsLastSent", Description: "Time of the last sending as Matomo stores it (UTC); absent if never sent"},
		{Name: "reports[].evolutionGraphWithinPeriod", Description: "True when evolution graphs show each day of the period"},
		{Name: "reports[].evolutionGraphPeriodN", Description: "Number of periods an evolution graph shows"},
	}, pagingFields...),
	Examples: []capability.Example{{Description: "Scheduled reports of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

// ScheduledReport is one scheduled report. All text is untrusted provider data; the recipients and the login are
// personal data.
type ScheduledReport struct {
	IDReport                   int      `json:"idReport"`
	IDSite                     int      `json:"idSite"`
	Login                      string   `json:"login,omitempty"`
	Description                string   `json:"description,omitempty"`
	IDSegment                  int      `json:"idSegment,omitempty"`
	Period                     string   `json:"period,omitempty"`
	PeriodParam                string   `json:"periodParam,omitempty"`
	Hour                       *int     `json:"hour,omitempty"`
	Type                       string   `json:"type,omitempty"`
	Format                     string   `json:"format,omitempty"`
	Reports                    []string `json:"reports,omitempty"`
	EmailMe                    *bool    `json:"emailMe,omitempty"`
	AdditionalEmails           []string `json:"additionalEmails,omitempty"`
	PhoneNumbers               []string `json:"phoneNumbers,omitempty"`
	EvolutionGraph             *bool    `json:"evolutionGraph,omitempty"`
	DisplayFormat              *int     `json:"displayFormat,omitempty"`
	EnforceOrder               *bool    `json:"enforceOrder,omitempty"`
	TsCreated                  string   `json:"tsCreated,omitempty"`
	TsLastSent                 string   `json:"tsLastSent,omitempty"`
	EvolutionGraphWithinPeriod *bool    `json:"evolutionGraphWithinPeriod,omitempty"`
	EvolutionGraphPeriodN      *int     `json:"evolutionGraphPeriodN,omitempty"`
}

// ScheduledReportsResult is the answer of scheduled-reports.list.
type ScheduledReportsResult struct {
	Reports    []ScheduledReport `json:"reports"`
	HasMore    bool              `json:"has_more"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// storedReportJSON is a report as ScheduledReports.getReports gives it. Every member is read loosely.
type storedReportJSON struct {
	IDReport     *loose          `json:"idreport"`
	IDSite       *loose          `json:"idsite"`
	Login        *loose          `json:"login"`
	Description  *loose          `json:"description"`
	IDSegment    *loose          `json:"idsegment"`
	Period       *loose          `json:"period"`
	PeriodParam  *loose          `json:"period_param"`
	Hour         *loose          `json:"hour"`
	Type         *loose          `json:"type"`
	Format       *loose          `json:"format"`
	Reports      json.RawMessage `json:"reports"`
	Parameters   json.RawMessage `json:"parameters"`
	Created      *loose          `json:"ts_created"`
	LastSent     *loose          `json:"ts_last_sent"`
	GraphWithin  *loose          `json:"evolution_graph_within_period"`
	GraphPeriodN *loose          `json:"evolution_graph_period_n"`
}

// reportParametersJSON holds the transport parameters Qatlas reads; any other key is dropped.
type reportParametersJSON struct {
	EmailMe          *loose          `json:"emailMe"`
	EvolutionGraph   *loose          `json:"evolutionGraph"`
	DisplayFormat    *loose          `json:"displayFormat"`
	EnforceOrder     *loose          `json:"enforceOrder"`
	AdditionalEmails json.RawMessage `json:"additionalEmails"`
	PhoneNumbers     json.RawMessage `json:"phoneNumbers"`
}

// textList reads a JSON array of scalars as bounded text; anything else is an empty list.
func textList(raw json.RawMessage, entries, limit int) []string {
	var items []loose
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var out []string
	for i := range items {
		if text := items[i].str(limit); text != "" && len(out) < entries {
			out = append(out, text)
		}
	}
	return out
}

// report converts a row; ok is false for a row without a usable report ID or website, since the website is the
// proof that the report lies inside the targets.
func (s storedReportJSON) report() (ScheduledReport, bool) {
	id, site := s.IDReport.number(), s.IDSite.number()
	if id == nil || *id < 1 || site == nil || *site < 1 {
		return ScheduledReport{}, false
	}
	out := ScheduledReport{
		IDReport: *id, IDSite: *site, Login: s.Login.str(maxStringLength), Description: s.Description.str(maxStringLength),
		Period: s.Period.str(maxStringLength), PeriodParam: s.PeriodParam.str(maxStringLength), Hour: s.Hour.number(),
		Type: s.Type.str(maxStringLength), Format: s.Format.str(maxStringLength),
		Reports:   textList(s.Reports, maxReportParts, maxStringLength),
		TsCreated: s.Created.str(maxStringLength), TsLastSent: s.LastSent.str(maxStringLength),
		EvolutionGraphWithinPeriod: s.GraphWithin.flag(), EvolutionGraphPeriodN: s.GraphPeriodN.number(),
	}
	if segment := s.IDSegment.number(); segment != nil && *segment > 0 {
		out.IDSegment = *segment
	}
	var parameters reportParametersJSON
	if json.Unmarshal(s.Parameters, &parameters) == nil {
		out.EmailMe, out.EvolutionGraph, out.EnforceOrder = parameters.EmailMe.flag(), parameters.EvolutionGraph.flag(),
			parameters.EnforceOrder.flag()
		out.DisplayFormat = parameters.DisplayFormat.number()
		out.AdditionalEmails = textList(parameters.AdditionalEmails, maxRecipients, maxRecipientText)
		out.PhoneNumbers = textList(parameters.PhoneNumbers, maxRecipients, maxRecipientText)
	}
	return out, true
}

func invokeScheduledReportsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list scheduled reports"
	var args struct {
		IDSite    *int   `json:"idSite"`
		Period    string `json:"period"`
		IDReport  *int   `json:"idReport"`
		OnlyOwn   bool   `json:"ifSuperUserReturnOnlySuperUserReports"`
		IDSegment *int   `json:"idSegment"`
		Limit     int    `json:"limit"`
		Cursor    string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	site := 0
	if args.IDSite != nil {
		if site, err = selectSite(resolved, args.IDSite); err != nil {
			return nil, err
		}
	} else if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	if args.Period != "" && !slices.Contains(scheduledReportPeriods, args.Period) {
		return nil, invalidArgument("period must be one of day, week, month, never")
	}
	report, segment := 0, 0
	if args.IDReport != nil {
		if report, err = reportIDOf(args.IDReport); err != nil {
			return nil, err
		}
	}
	if args.IDSegment != nil {
		if *args.IDSegment < 1 || len(strconv.Itoa(*args.IDSegment)) > maxIDDigits {
			return nil, invalidArgument("idSegment must be a positive integer segment ID")
		}
		segment = *args.IDSegment
	}
	binding := provider.CursorBinding(scheduledReportsList.ID, scopeBinding(resolved), site, args.Period, report,
		args.OnlyOwn, segment)
	paging, err := startPage(binding, args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	if site > 0 {
		params.Set("idSite", strconv.Itoa(site))
	}
	if args.Period != "" {
		params.Set("period", args.Period)
	}
	if report > 0 {
		params.Set("idReport", strconv.Itoa(report))
	}
	if args.OnlyOwn {
		params.Set("ifSuperUserReturnOnlySuperUserReports", "1")
	}
	if segment > 0 {
		params.Set("idSegment", strconv.Itoa(segment))
	}
	var rows []json.RawMessage
	if err := client.call(ctx, op, scheduledReportsRequirement(scheduledReportsList.ID), "ScheduledReports.getReports",
		params, &rows); err != nil {
		return nil, err
	}
	// Matomo answers for every website the token views, so each row is proven against the targets before paging.
	reports := []ScheduledReport{}
	for _, row := range rows {
		var stored storedReportJSON
		if json.Unmarshal(row, &stored) != nil {
			continue
		}
		parsed, ok := stored.report()
		if !ok || !bound.allows(parsed.IDSite) || site > 0 && parsed.IDSite != site {
			continue
		}
		reports = append(reports, parsed)
	}
	batch, next := slice(paging, reports)
	return &ScheduledReportsResult{Reports: batch, HasMore: next != "", NextCursor: next}, nil
}

func reportIDOf(given *int) (int, error) {
	if given == nil || *given < 1 || len(strconv.Itoa(*given)) > maxIDDigits {
		return 0, invalidArgument("idReport must be a positive integer report ID from matomo.scheduled-reports.list")
	}
	return *given, nil
}

// scheduledReportNotFound is the answer for a report that is absent or lies outside the targets; it does not tell which.
func scheduledReportNotFound(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op,
		Message: "Matomo has no scheduled report with this ID inside the targets of this connection"}
}

// fetchScheduledReport is the proving read of a report: one getReports request with its ID. A report of a
// website outside the targets is not found, and the answer does not tell whether it exists.
func (c *Client) fetchScheduledReport(ctx context.Context, op string, req requirement, id int) (ScheduledReport, error) {
	report, _, err := c.fetchScheduledReportRow(ctx, op, req, id)
	return report, err
}

// fetchScheduledReportRow is fetchScheduledReport that also hands back the proven row as Matomo gave it.
func (c *Client) fetchScheduledReportRow(ctx context.Context, op string, req requirement, id int) (ScheduledReport,
	json.RawMessage, error) {
	var rows []json.RawMessage
	params := url.Values{"idReport": {strconv.Itoa(id)}}
	if err := c.call(ctx, op, req, "ScheduledReports.getReports", params, &rows); err != nil {
		return ScheduledReport{}, nil, err
	}
	if len(rows) == 0 {
		return ScheduledReport{}, nil, scheduledReportNotFound(op)
	}
	if len(rows) != 1 || !bytes.HasPrefix(bytes.TrimSpace(rows[0]), []byte("{")) {
		return ScheduledReport{}, nil, provider.InvalidResponse(op, "Matomo returned an invalid scheduled report")
	}
	var stored storedReportJSON
	if json.Unmarshal(rows[0], &stored) != nil {
		return ScheduledReport{}, nil, provider.InvalidResponse(op, "Matomo returned an invalid scheduled report")
	}
	report, ok := stored.report()
	if !ok || report.IDReport != id {
		return ScheduledReport{}, nil, provider.InvalidResponse(op, "Matomo returned another scheduled report than asked for")
	}
	if !c.scope.allows(report.IDSite) {
		return ScheduledReport{}, nil, scheduledReportNotFound(op)
	}
	return report, rows[0], nil
}
