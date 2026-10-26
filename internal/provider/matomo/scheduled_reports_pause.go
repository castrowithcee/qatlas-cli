package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"reflect"
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
	// maxReportDescription is the length (in bytes) at which updateReport truncates a description.
	maxReportDescription = 250
	periodNever          = "never"
)

var (
	// storedReportKeys are the members of a getReports row; a row with another member is not written back.
	storedReportKeys = []string{"idreport", "idsite", "login", "description", "idsegment", "period", "period_param",
		"hour", "type", "format", "reports", "parameters", "ts_created", "ts_last_sent", "deleted",
		"evolution_graph_within_period", "evolution_graph_period_n"}
	// emailParameterKeys are the transport parameters of an e-mail report that updateReport keeps.
	emailParameterKeys = []string{"emailMe", "evolutionGraph", "additionalEmails", "displayFormat", "enforceOrder",
		"reportDescription"}
	dataPeriods = []string{"day", "week", "month", "year"}
)

var scheduledReportsPause = capability.Descriptor{
	ID: Provider + ".scheduled-reports.pause", Version: 1, Title: "Pause a Matomo scheduled report",
	Description: "Pause one scheduled report by setting its schedule to never, so that Matomo stops sending it; " +
		"nothing else changes and the tool never sends a report. The tool reads the report once, hands every other " +
		"setting (recipients included) back to Matomo exactly as read, and refuses before writing when a value " +
		"cannot be handed back unchanged (only e-mail reports qualify). A report that is already paused is not " +
		"written. The report must belong to the website and lie inside the connection's targets, otherwise it is " +
		"not found; only its creator or a Super User may change it. Matomo may also store a language for the " +
		"token's user when it has none. Resuming is possible only in Matomo; idempotent; sent once and never " +
		"repeated; after an unclear result read the report with matomo.scheduled-reports.list before repeating it",
	Tags: []string{"matomo", "scheduled-reports", "email", "pause", "update"}, Risk: capability.Risk{
		Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: scheduledReportsSensitive},
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idReportSchema +
		`},"required":["idReport"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idReport":{"type":"integer"},"idSite":{"type":"integer"},` +
		`"paused":{"type":"boolean"},"alreadyPaused":{"type":"boolean"}},"required":["idReport","idSite","paused",` +
		`"alreadyPaused"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(), {Name: "idReport", Required: true,
		Description: "ID of the report to pause, from matomo.scheduled-reports.list; it must belong to the website"}},
	Fields: []capability.Field{
		{Name: "idReport", Description: "ID of the paused report"},
		{Name: "idSite", Description: "The website the report is about"},
		{Name: "paused", Description: "True when the report's schedule is never"},
		{Name: "alreadyPaused", Description: "True when the report was paused before and nothing was written"},
	},
	Examples: []capability.Example{{Description: "Pause report 3 of website 1", Arguments: json.RawMessage(`{"idSite":1,"idReport":3}`)}},
}

// ScheduledReportPaused is the answer of scheduled-reports.pause.
type ScheduledReportPaused struct {
	IDReport      int  `json:"idReport"`
	IDSite        int  `json:"idSite"`
	Paused        bool `json:"paused"`
	AlreadyPaused bool `json:"alreadyPaused"`
}

// pauseView is a report without its schedule, in the form Matomo stores and getReports gives back. The row Qatlas
// read and the state a write of the same values would leave are both reduced to it and compared.
type pauseView struct {
	Description string
	IDSegment   int
	Hour        int
	Type        string
	Format      string
	PeriodParam string
	Reports     []string
	Parameters  map[string]any
	Each        bool
	PeriodN     int
}

// unwritable is the refusal before the write; nothing has been sent.
func unwritable(op, what string) error {
	return provider.InvalidResponse(op, "the scheduled report has "+what+" that Qatlas cannot hand back to Matomo "+
		"unchanged; nothing was changed, pause it in Matomo")
}

func exactString(raw json.RawMessage) (string, bool) {
	var text string
	if len(raw) == 0 || json.Unmarshal(raw, &text) != nil {
		return "", false
	}
	return text, true
}

// exactInt reads a database integer that arrives as a string or a number, in its plain decimal form only.
func exactInt(raw json.RawMessage) (int, bool) {
	var value loose
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return 0, false
	}
	n := value.number()
	if n == nil || strconv.Itoa(*n) != value.text {
		return 0, false
	}
	return *n, true
}

func pauseViewOf(op string, row json.RawMessage) (pauseView, error) {
	var members map[string]json.RawMessage
	if json.Unmarshal(row, &members) != nil {
		return pauseView{}, unwritable(op, "an unreadable form")
	}
	for name := range members {
		if !slices.Contains(storedReportKeys, name) {
			return pauseView{}, unwritable(op, "a member unknown to Qatlas")
		}
	}
	var view pauseView
	var ok bool
	if view.Description, ok = exactString(members["description"]); !ok || view.Description == "" ||
		len(view.Description) > maxReportDescription {
		return pauseView{}, unwritable(op, "a description of another form")
	}
	if view.Type, ok = exactString(members["type"]); !ok || view.Type != "email" {
		return pauseView{}, unwritable(op, "a transport other than e-mail")
	}
	if view.Format, ok = exactString(members["format"]); !ok || view.Format == "" {
		return pauseView{}, unwritable(op, "an output format of another form")
	}
	if view.PeriodParam, ok = exactString(members["period_param"]); !ok || !slices.Contains(dataPeriods, view.PeriodParam) {
		return pauseView{}, unwritable(op, "a data period of another form")
	}
	if view.Hour, ok = exactInt(members["hour"]); !ok || view.Hour < 0 || view.Hour > 23 {
		return pauseView{}, unwritable(op, "an hour of another form")
	}
	if segment := bytes.TrimSpace(members["idsegment"]); len(segment) > 0 && string(segment) != "null" {
		if view.IDSegment, ok = exactInt(segment); !ok || view.IDSegment < 1 {
			return pauseView{}, unwritable(op, "a segment of another form")
		}
	}
	var each int
	if each, ok = exactInt(members["evolution_graph_within_period"]); !ok || each < 0 || each > 1 {
		return pauseView{}, unwritable(op, "an evolution setting of another form")
	}
	view.Each = each == 1
	if n, ok := exactInt(members["evolution_graph_period_n"]); !ok || n < 1 {
		return pauseView{}, unwritable(op, "an evolution setting of another form")
	} else if !view.Each {
		view.PeriodN = n
	}
	if json.Unmarshal(members["reports"], &view.Reports) != nil || len(view.Reports) == 0 ||
		len(view.Reports) > maxReportParts {
		return pauseView{}, unwritable(op, "a report list of another form")
	}
	if json.Unmarshal(members["parameters"], &view.Parameters) != nil || view.Parameters == nil {
		return pauseView{}, unwritable(op, "parameters of another form")
	}
	for name, value := range view.Parameters {
		if !slices.Contains(emailParameterKeys, name) {
			return pauseView{}, unwritable(op, "a parameter unknown to Qatlas")
		}
		if list, isList := value.([]any); isList {
			texts := make([]string, 0, len(list))
			for _, entry := range list {
				text, isText := entry.(string)
				if !isText {
					return pauseView{}, unwritable(op, "a list of another form")
				}
				texts = append(texts, text)
			}
			view.Parameters[name] = texts
		}
	}
	return view, nil
}

// params is the updateReport request for the view; only the schedule differs from what was read.
func (v pauseView) params(op string, id, site int) (url.Values, error) {
	params := url.Values{"idReport": {strconv.Itoa(id)}, "idSite": {strconv.Itoa(site)}, "description": {v.Description},
		"period": {periodNever}, "hour": {strconv.Itoa(v.Hour)}, "reportType": {v.Type}, "reportFormat": {v.Format},
		"reports[]": slices.Clone(v.Reports), "periodParam": {v.PeriodParam}, "evolutionPeriodFor": {"prev"}}
	if v.IDSegment > 0 {
		params.Set("idSegment", strconv.Itoa(v.IDSegment))
	}
	if v.Each {
		params.Set("evolutionPeriodFor", "each")
	} else {
		params.Set("evolutionPeriodN", strconv.Itoa(v.PeriodN))
	}
	for name, value := range v.Parameters {
		key := "parameters[" + name + "]"
		switch typed := value.(type) {
		case bool:
			params.Set(key, map[bool]string{true: "1", false: "0"}[typed])
		case string:
			params.Set(key, typed)
		case []string:
			// An empty list has no form encoding; it would be dropped and the stored value would change.
			if len(typed) == 0 {
				return nil, unwritable(op, "an empty list")
			}
			params[key+"[]"] = slices.Clone(typed)
		default:
			return nil, unwritable(op, "a parameter of another form")
		}
	}
	if _, found := v.Parameters["displayFormat"]; !found {
		return nil, unwritable(op, "no display format")
	}
	return params, nil
}

const phpWhitespace = " \t\n\r\x00\x0b"

// afterWrite is the state Matomo's updateReport leaves for a request, following its validation and defaults.
func afterWrite(params url.Values) pauseView {
	view := pauseView{Description: params.Get("description"), Type: params.Get("reportType"),
		Format: params.Get("reportFormat"), PeriodParam: params.Get("periodParam"), Reports: params["reports[]"],
		Parameters: map[string]any{}, Each: params.Get("evolutionPeriodFor") == "each"}
	view.Description = view.Description[:min(len(view.Description), maxReportDescription)]
	view.IDSegment, _ = strconv.Atoi(params.Get("idSegment"))
	view.Hour, _ = strconv.Atoi(params.Get("hour"))
	if !view.Each {
		view.PeriodN, _ = strconv.Atoi(params.Get("evolutionPeriodN"))
	}
	for key, values := range params {
		name, found := strings.CutPrefix(key, "parameters[")
		if !found {
			continue
		}
		if name, found = strings.CutSuffix(name, "][]"); found {
			var emails []string
			for _, email := range values {
				email = strings.Trim(email, phpWhitespace)
				if email != "" && email != "0" && !slices.Contains(emails, email) {
					emails = append(emails, email)
				}
			}
			view.Parameters[name] = emails
		} else if name, found = strings.CutSuffix(name, "]"); found {
			view.Parameters[name] = values[0]
		}
	}
	// The e-mail transport turns these three into flags and gives them their defaults when they are absent.
	for name, fallback := range map[string]bool{"emailMe": true, "evolutionGraph": false, "enforceOrder": false} {
		value, given := view.Parameters[name].(string)
		view.Parameters[name] = fallback
		if given {
			view.Parameters[name] = value == "1" || value == "true"
		}
	}
	if text, found := view.Parameters["reportDescription"].(string); found {
		view.Parameters["reportDescription"] = strings.Trim(text, phpWhitespace)
	}
	return view
}

func invokeScheduledReportsPause(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "pause scheduled report"
	var args struct {
		IDSite   *int `json:"idSite"`
		IDReport *int `json:"idReport"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	id, err := reportIDOf(args.IDReport)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := scheduledReportsRequirement(scheduledReportsPause.ID)
	report, row, err := client.fetchScheduledReportRow(ctx, op, req, id)
	if err != nil {
		return nil, err
	}
	if report.IDSite != site {
		return nil, scheduledReportNotFound(op)
	}
	if report.Period == periodNever {
		return &ScheduledReportPaused{IDReport: id, IDSite: site, Paused: true, AlreadyPaused: true}, nil
	}
	if !slices.Contains(scheduledReportPeriods, report.Period) {
		return nil, unwritable(op, "a schedule of another form")
	}
	view, err := pauseViewOf(op, row)
	if err != nil {
		return nil, err
	}
	params, err := view.params(op, id, site)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(view, afterWrite(params)) {
		return nil, unwritable(op, "a value")
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "ScheduledReports.updateReport", params, scheduledReportSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the change", scheduledReportSentOnce)
	}
	return &ScheduledReportPaused{IDReport: id, IDSite: site, Paused: true}, nil
}
