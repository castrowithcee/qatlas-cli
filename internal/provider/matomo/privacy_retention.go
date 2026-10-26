package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// maxRetentionDays bounds every retention and interval argument; Matomo has no upper limit.
	maxRetentionDays = 3650
	minLogDays       = 1
	minReportPeriods = 2
	retentionNumber  = `"type":"integer","maximum":3650`
)

const retentionSentOnce = "; sent once and never repeated by Qatlas: after an unclear result the settings may have been " +
	"saved, so read them in Matomo before repeating it"

const retentionNote = " Every argument is required and the call sets the whole state, because Matomo resets an omitted " +
	"argument to its default; matomo.privacy-settings.get does not return these settings, so check them in Matomo " +
	"first. It needs Super User rights, a connection whose targets name the instance, and a password confirmation: the " +
	"credential needs the secret role " + rolePasswordConfirmation + ", which no argument carries. Sent once and " +
	"never repeated"

var privacySettingsSetDeleteLogs = capability.Descriptor{
	ID: Provider + ".privacy-settings.set-delete-logs", Version: 1, Title: "Set the Matomo raw data deletion",
	Description: "Switch the scheduled deletion of old raw logs on or off and set the age in days " +
		"(PrivacyManager.setDeleteLogsSettings). With deletion on, the scheduled purge deletes the visit logs older " +
		"than that age for good from then on; it cannot be undone. Matomo ignores the age when a compliance policy " +
		"forbids it." + retentionNote + "; offered only where a connection's tools list names it",
	Tags: []string{"matomo", "gdpr", "privacy", "retention", "raw-data", "logs", "instance", "set"},
	Risk: eraseRisk(capability.EffectUpdate, rawDataSensitive), Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"enableDeleteLogs":{"type":"boolean"},` +
		`"deleteLogsOlderThan":{` + retentionNumber + `,"minimum":1}},` +
		`"required":["enableDeleteLogs","deleteLogsOlderThan"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"enableDeleteLogs":{"type":"boolean"},` +
		`"deleteLogsOlderThan":{"type":"integer"},"saved":{"type":"boolean"}},` +
		`"required":["enableDeleteLogs","deleteLogsOlderThan","saved"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "enableDeleteLogs", Required: true, Description: "True to delete old logs on schedule, false to keep them"},
		{Name: "deleteLogsOlderThan", Required: true, Description: "Age in days, 1 to 3650, beyond which logs are deleted"},
	},
	Fields: []capability.Field{
		{Name: "enableDeleteLogs", Description: "The deletion switch that was sent"},
		{Name: "deleteLogsOlderThan", Description: "The age in days that was sent"},
		{Name: "saved", Description: "True once Matomo confirmed it saved the settings"},
	},
	Examples: []capability.Example{{Description: "Delete logs older than 180 days",
		Arguments: json.RawMessage(`{"enableDeleteLogs":true,"deleteLogsOlderThan":180}`)}},
}

// DeleteLogsSet is the answer of privacy-settings.set-delete-logs.
type DeleteLogsSet struct {
	EnableDeleteLogs    bool `json:"enableDeleteLogs"`
	DeleteLogsOlderThan int  `json:"deleteLogsOlderThan"`
	Saved               bool `json:"saved"`
}

var reportKeeps = []string{"keepBasic", "keepDay", "keepWeek", "keepMonth", "keepYear", "keepRange", "keepSegments"}

var privacySettingsSetDeleteReports = capability.Descriptor{
	ID: Provider + ".privacy-settings.set-delete-reports", Version: 1, Title: "Set the Matomo report deletion",
	Description: "Switch the scheduled deletion of old archived reports on or off, set the age in periods, and choose " +
		"which report kinds are kept (PrivacyManager.setDeleteReportsSettings). With deletion on, the scheduled purge " +
		"deletes the older reports that are not kept for good from then on; the reports can only be rebuilt from logs " +
		"that still exist." + retentionNote + "; offered only where a connection's tools list names it",
	Tags: []string{"matomo", "gdpr", "privacy", "retention", "reports", "instance", "set"},
	Risk: eraseRisk(capability.EffectUpdate, rawDataSensitive), Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"enableDeleteReports":{"type":"boolean"},` +
		`"deleteReportsOlderThan":{` + retentionNumber + `,"minimum":2},"keepBasic":{"type":"boolean"},` +
		`"keepDay":{"type":"boolean"},"keepWeek":{"type":"boolean"},"keepMonth":{"type":"boolean"},` +
		`"keepYear":{"type":"boolean"},"keepRange":{"type":"boolean"},"keepSegments":{"type":"boolean"}},` +
		`"required":["enableDeleteReports","deleteReportsOlderThan","keepBasic","keepDay","keepWeek","keepMonth",` +
		`"keepYear","keepRange","keepSegments"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"enableDeleteReports":{"type":"boolean"},` +
		`"deleteReportsOlderThan":{"type":"integer"},"keepBasic":{"type":"boolean"},"keepDay":{"type":"boolean"},` +
		`"keepWeek":{"type":"boolean"},"keepMonth":{"type":"boolean"},"keepYear":{"type":"boolean"},` +
		`"keepRange":{"type":"boolean"},"keepSegments":{"type":"boolean"},"saved":{"type":"boolean"}},` +
		`"required":["enableDeleteReports","deleteReportsOlderThan","keepBasic","keepDay","keepWeek","keepMonth",` +
		`"keepYear","keepRange","keepSegments","saved"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "enableDeleteReports", Required: true, Description: "True to delete old reports on schedule, false to keep them"},
		{Name: "deleteReportsOlderThan", Required: true, Description: "Age in periods (months), 2 to 3650, beyond which reports are deleted"},
		{Name: "keepBasic", Required: true, Description: "True to keep the basic metrics (visits, actions, and similar) when deleting"},
		{Name: "keepDay", Required: true, Description: "True to keep day reports"},
		{Name: "keepWeek", Required: true, Description: "True to keep week reports"},
		{Name: "keepMonth", Required: true, Description: "True to keep month reports"},
		{Name: "keepYear", Required: true, Description: "True to keep year reports"},
		{Name: "keepRange", Required: true, Description: "True to keep range reports"},
		{Name: "keepSegments", Required: true, Description: "True to keep reports of segments"},
	},
	Fields: []capability.Field{
		{Name: "enableDeleteReports", Description: "The deletion switch that was sent"},
		{Name: "deleteReportsOlderThan", Description: "The age in periods that was sent"},
		{Name: "keepBasic", Description: "The keep flag for basic metrics that was sent"},
		{Name: "keepDay", Description: "The keep flag for day reports that was sent"},
		{Name: "keepWeek", Description: "The keep flag for week reports that was sent"},
		{Name: "keepMonth", Description: "The keep flag for month reports that was sent"},
		{Name: "keepYear", Description: "The keep flag for year reports that was sent"},
		{Name: "keepRange", Description: "The keep flag for range reports that was sent"},
		{Name: "keepSegments", Description: "The keep flag for segment reports that was sent"},
		{Name: "saved", Description: "True once Matomo confirmed it saved the settings"},
	},
	Examples: []capability.Example{{Description: "Delete reports older than 24 months but keep year reports",
		Arguments: json.RawMessage(`{"enableDeleteReports":true,"deleteReportsOlderThan":24,"keepBasic":true,` +
			`"keepDay":false,"keepWeek":false,"keepMonth":false,"keepYear":true,"keepRange":false,"keepSegments":false}`)}},
}

// DeleteReportsSet is the answer of privacy-settings.set-delete-reports.
type DeleteReportsSet struct {
	EnableDeleteReports    bool `json:"enableDeleteReports"`
	DeleteReportsOlderThan int  `json:"deleteReportsOlderThan"`
	KeepBasic              bool `json:"keepBasic"`
	KeepDay                bool `json:"keepDay"`
	KeepWeek               bool `json:"keepWeek"`
	KeepMonth              bool `json:"keepMonth"`
	KeepYear               bool `json:"keepYear"`
	KeepRange              bool `json:"keepRange"`
	KeepSegments           bool `json:"keepSegments"`
	Saved                  bool `json:"saved"`
}

var privacySettingsSetReportDeletionSchedule = capability.Descriptor{
	ID: Provider + ".privacy-settings.set-report-deletion-schedule", Version: 1,
	Title: "Set the Matomo deletion schedule",
	Description: "Set the minimum number of days between two scheduled data deletion runs " +
		"(PrivacyManager.setScheduleReportDeletionSettings). It changes when the scheduled deletion may run, not " +
		"what it deletes." + retentionNote,
	Tags: []string{"matomo", "gdpr", "privacy", "retention", "schedule", "instance", "set"},
	Risk: eraseRisk(capability.EffectUpdate, rawDataSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"deleteLowestInterval":{` + retentionNumber +
		`,"minimum":1}},"required":["deleteLowestInterval"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleteLowestInterval":{"type":"integer"},` +
		`"saved":{"type":"boolean"}},"required":["deleteLowestInterval","saved"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "deleteLowestInterval", Required: true, Description: "Minimum days between scheduled deletion runs, 1 to 3650"},
	},
	Fields: []capability.Field{
		{Name: "deleteLowestInterval", Description: "The interval in days that was sent"},
		{Name: "saved", Description: "True once Matomo confirmed it saved the setting"},
	},
	Examples: []capability.Example{{Description: "Run the scheduled deletion at most every 7 days",
		Arguments: json.RawMessage(`{"deleteLowestInterval":7}`)}},
}

// ReportDeletionScheduleSet is the answer of privacy-settings.set-report-deletion-schedule.
type ReportDeletionScheduleSet struct {
	DeleteLowestInterval int  `json:"deleteLowestInterval"`
	Saved                bool `json:"saved"`
}

// retentionDays checks a required whole number of days or periods.
func retentionDays(value *int, minimum int, name string) (int, error) {
	if value == nil || *value < minimum || *value > maxRetentionDays {
		return 0, invalidArgument(name + " must be a whole number from " + strconv.Itoa(minimum) + " to " +
			strconv.Itoa(maxRetentionDays))
	}
	return *value, nil
}

// retentionFlag checks a required boolean; a missing one is refused instead of defaulting.
func retentionFlag(value *bool, name string) (bool, error) {
	if value == nil {
		return false, invalidArgument(name + " is required")
	}
	return *value, nil
}

// sendRetention opens the token, adds the password confirmation, and sends the one request.
func sendRetention(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	id, op, method string, params url.Values) error {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return err
	}
	if err := addPasswordConfirmation(ctx, resolved, secrets, red, params); err != nil {
		return err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, requirement{tool: id, plugin: "PrivacyManager"}, method, params,
		retentionSentOnce, &answer); err != nil {
		return err
	}
	if !confirmedByMatomo(answer) {
		return unclearChange(op, "Matomo returned no confirmation of the settings", retentionSentOnce)
	}
	return nil
}

func invokePrivacySettingsSetDeleteLogs(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set raw data deletion"
	var args struct {
		Enable *bool `json:"enableDeleteLogs"`
		Days   *int  `json:"deleteLogsOlderThan"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	enable, err := retentionFlag(args.Enable, "enableDeleteLogs")
	if err != nil {
		return nil, err
	}
	days, err := retentionDays(args.Days, minLogDays, "deleteLogsOlderThan")
	if err != nil {
		return nil, err
	}
	params := url.Values{"enableDeleteLogs": {flagValue(&enable)}, "deleteLogsOlderThan": {strconv.Itoa(days)}}
	if err := sendRetention(ctx, resolved, secrets, red, privacySettingsSetDeleteLogs.ID, op,
		"PrivacyManager.setDeleteLogsSettings", params); err != nil {
		return nil, err
	}
	return &DeleteLogsSet{EnableDeleteLogs: enable, DeleteLogsOlderThan: days, Saved: true}, nil
}

func invokePrivacySettingsSetDeleteReports(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set report deletion"
	var args struct {
		Enable       *bool `json:"enableDeleteReports"`
		Periods      *int  `json:"deleteReportsOlderThan"`
		KeepBasic    *bool `json:"keepBasic"`
		KeepDay      *bool `json:"keepDay"`
		KeepWeek     *bool `json:"keepWeek"`
		KeepMonth    *bool `json:"keepMonth"`
		KeepYear     *bool `json:"keepYear"`
		KeepRange    *bool `json:"keepRange"`
		KeepSegments *bool `json:"keepSegments"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	enable, err := retentionFlag(args.Enable, "enableDeleteReports")
	if err != nil {
		return nil, err
	}
	periods, err := retentionDays(args.Periods, minReportPeriods, "deleteReportsOlderThan")
	if err != nil {
		return nil, err
	}
	keeps := []*bool{args.KeepBasic, args.KeepDay, args.KeepWeek, args.KeepMonth, args.KeepYear, args.KeepRange,
		args.KeepSegments}
	params := url.Values{"enableDeleteReports": {flagValue(&enable)}, "deleteReportsOlderThan": {strconv.Itoa(periods)}}
	values := make([]bool, len(keeps))
	for i, keep := range keeps {
		if values[i], err = retentionFlag(keep, reportKeeps[i]); err != nil {
			return nil, err
		}
		params.Set(reportKeeps[i], flagValue(&values[i]))
	}
	if err := sendRetention(ctx, resolved, secrets, red, privacySettingsSetDeleteReports.ID, op,
		"PrivacyManager.setDeleteReportsSettings", params); err != nil {
		return nil, err
	}
	return &DeleteReportsSet{EnableDeleteReports: enable, DeleteReportsOlderThan: periods, KeepBasic: values[0],
		KeepDay: values[1], KeepWeek: values[2], KeepMonth: values[3], KeepYear: values[4], KeepRange: values[5],
		KeepSegments: values[6], Saved: true}, nil
}

func invokePrivacySettingsSetReportDeletionSchedule(ctx context.Context, resolved *config.Resolved,
	secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set deletion schedule"
	var args struct {
		Days *int `json:"deleteLowestInterval"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := needsInstance(resolved); err != nil {
		return nil, err
	}
	days, err := retentionDays(args.Days, 1, "deleteLowestInterval")
	if err != nil {
		return nil, err
	}
	params := url.Values{"deleteLowestInterval": {strconv.Itoa(days)}}
	if err := sendRetention(ctx, resolved, secrets, red, privacySettingsSetReportDeletionSchedule.ID, op,
		"PrivacyManager.setScheduleReportDeletionSettings", params); err != nil {
		return nil, err
	}
	return &ReportDeletionScheduleSet{DeleteLowestInterval: days, Saved: true}, nil
}
