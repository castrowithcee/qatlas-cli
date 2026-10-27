package matomo

import (
	"context"
	"encoding/json"
	"net/url"
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
	// alertsSensitive covers logins, e-mail addresses, phone numbers, and Slack channels, which are personal
	// data, and the names and conditions people wrote.
	alertsSensitive = "matomo-alerts"
	alertsPlugin    = "CustomAlerts"
	maxAlertMedium  = 20
	maxAlertSites   = 1000
	maxPastPeriods  = 1000
	// maxAlertSecret bounds a stored webhook address only so that it can be registered for redaction.
	maxAlertSecret = 2048
)

func alertsRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: alertsPlugin}
}

var alertSentOnce = sentOnceFor("alert", "matomo.alerts.get")

const alertProperties = `"idAlert":{"type":"integer"},"name":{"type":"string"},"description":{"type":"string"},` +
	`"login":{"type":"string"},"period":{"type":"string"},"report":{"type":"string"},` +
	`"report_condition":{"type":"string"},"report_matched":{"type":"string"},` +
	`"report_mediums":{"type":"array","items":{"type":"string"}},"metric":{"type":"string"},` +
	`"metric_condition":{"type":"string"},"metric_matched":{"type":"number"},"compared_to":{"type":"integer"},` +
	`"email_me":{"type":"boolean"},"additional_emails":{"type":"array","items":{"type":"string"}},` +
	`"phone_numbers":{"type":"array","items":{"type":"string"}},"slack_channel_id":{"type":"string"},` +
	`"ms_teams_webhook_set":{"type":"boolean"},"id_sites":{"type":"array","items":{"type":"integer"}}`

const triggeredProperties = alertProperties + `,"idTriggered":{"type":"integer"},"idSite":{"type":"integer"},` +
	`"ts_triggered":{"type":"string"},"ts_last_sent":{"type":"string"},"value_old":{"type":"number"},` +
	`"value_new":{"type":"number"}`

const (
	idAlertSchema     = `"idAlert":{"type":"integer","minimum":1,"maximum":999999999}`
	subPeriodNSchema  = `"subPeriodN":{"type":"integer","minimum":0,"maximum":1000}`
	allAlertsProperty = `"ifSuperUserReturnAllAlerts":{"type":"boolean"}`
)

func alertFields(prefix string) []capability.Field {
	return []capability.Field{
		{Name: prefix + "idAlert", Description: "Alert ID, used as idAlert by the other alert tools"},
		{Name: prefix + "name", Description: "Name of the alert, untrusted"},
		{Name: prefix + "description", Description: "Description of the alert, untrusted"},
		{Name: prefix + "login", Description: "Login of the user who owns the alert, untrusted, personal data"},
		{Name: prefix + "period", Description: "Period the alert is evaluated for: day, week, or month"},
		{Name: prefix + "report", Description: "Unique ID of the report the alert watches, untrusted"},
		{Name: prefix + "report_condition", Description: "How report_matched is compared with the report rows, untrusted; " +
			"absent without a row filter"},
		{Name: prefix + "report_matched", Description: "Value a report row has to match, untrusted"},
		{Name: prefix + "report_mediums", Description: "Ways the alert notifies (email, mobile, slack, teams), at most 20, untrusted"},
		{Name: prefix + "metric", Description: "Metric the alert watches, untrusted"},
		{Name: prefix + "metric_condition", Description: "Condition on the metric, such as less_than or greater_than, untrusted"},
		{Name: prefix + "metric_matched", Description: "Threshold the condition compares with"},
		{Name: prefix + "compared_to", Description: "How many periods back the value is compared with, as Matomo stores it"},
		{Name: prefix + "email_me", Description: "True when the owner is notified by e-mail"},
		{Name: prefix + "additional_emails", Description: "Further e-mail recipients, at most 100, untrusted, personal data"},
		{Name: prefix + "phone_numbers", Description: "Phone number recipients, at most 100, untrusted, personal data"},
		{Name: prefix + "slack_channel_id", Description: "Slack channel the alert notifies, untrusted, personal data"},
		{Name: prefix + "ms_teams_webhook_set", Description: "True when a Microsoft Teams address is stored; the address " +
			"itself is a credential and is never returned"},
		{Name: prefix + "id_sites", Description: "Every website the alert watches; the tool shows only alerts whose " +
			"websites all lie inside the connection's targets"},
	}
}

func idAlertArgument(verb string) capability.Argument {
	return capability.Argument{Name: "idAlert", Required: true, Description: "ID of the alert to " + verb +
		", from matomo.alerts.list; only the token's own alerts qualify"}
}

func alertSiteArgument() capability.Argument {
	return capability.Argument{Name: "idSite", Target: "site", Description: "A website the alert watches, from " +
		"matomo.sites.list; optional when the connection binds exactly one website; it must be one of the alert's " +
		"websites"}
}

var alertsList = capability.Descriptor{
	ID: Provider + ".alerts.list", Version: 1, Title: "List Matomo alerts",
	Description: "List the custom alerts of the token's user for the websites inside the connection's targets, in " +
		"batches; with the Super User switch the alerts of all users. An alert watches several websites and " +
		"appears when at least one of them lies inside the targets, with all its websites listed. Logins, " +
		"recipients, names, and conditions are personal or untrusted data; a Microsoft Teams address never " +
		"appears, only whether one is stored. Needs the plugin CustomAlerts and a view right on the websites",
	Tags: []string{"matomo", "alerts", "notifications", "recipients", "list"}, Risk: readRisk(alertsSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + allAlertsProperty + `,` +
		pagingKeys + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"alerts":{"type":"array","items":{"type":"object",` +
		`"properties":{` + alertProperties + `},"required":["idAlert","id_sites"],"additionalProperties":false}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},"required":["alerts","has_more"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "idSite", Target: "site", Description: "Only the alerts that watch this website; the alerts of every " +
			"website of the connection when omitted"},
		{Name: "ifSuperUserReturnAllAlerts", Description: "True for a Super User to get the alerts of all users; " +
			"false when omitted"},
	}, pagingArguments...),
	Fields: append(append([]capability.Field{{Name: "alerts", Description: "Alerts in the order Matomo lists them"}},
		alertFields("alerts[].")...), pagingFields...),
	Examples: []capability.Example{{Description: "Alerts of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var alertsGet = capability.Descriptor{
	ID: Provider + ".alerts.get", Version: 1, Title: "Get a Matomo alert",
	Description: "Read one alert of the token's own user, also for a Super User; one read first proves that the " +
		"website is one of the alert's and that all its websites lie inside the targets, otherwise the alert is not " +
		"found. Logins, recipients, names, and conditions are personal or untrusted data; a Microsoft Teams address " +
		"never appears. Needs the plugin CustomAlerts",
	Tags: []string{"matomo", "alerts", "notifications", "get"}, Risk: readRisk(alertsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idAlertSchema + `},` +
		`"required":["idAlert"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + alertProperties + `},` +
		`"required":["idAlert","id_sites"],"additionalProperties":false}`),
	Arguments: []capability.Argument{alertSiteArgument(), idAlertArgument("read")},
	Fields:    alertFields(""),
	Examples:  []capability.Example{{Description: "Read alert 4 of website 1", Arguments: json.RawMessage(`{"idSite":1,"idAlert":4}`)}},
}

var alertsGetPastValues = capability.Descriptor{
	ID: Provider + ".alerts.get-past-values", Version: 1, Title: "Get the past values of a Matomo alert",
	Description: "Compute the value an own alert watches for each of its websites, for the current period or " +
		"periods back, only for the websites inside the connection's targets. One read first proves that the website " +
		"is one of the alert's, otherwise the alert is not found. Matomo evaluates the report of every website of " +
		"the alert, which can take time; nothing is sent to anyone. Needs the plugin CustomAlerts",
	Tags: []string{"matomo", "alerts", "notifications", "values"}, Risk: readRisk(alertsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idAlertSchema + `,` +
		subPeriodNSchema + `},"required":["idAlert","subPeriodN"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idAlert":{"type":"integer"},"values":{"type":"array",` +
		`"items":{"type":"object","properties":{"idSite":{"type":"integer"},"value":{"type":"number"}},` +
		`"required":["idSite"],"additionalProperties":false}}},"required":["idAlert","values"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{alertSiteArgument(), idAlertArgument("evaluate"),
		{Name: "subPeriodN", Required: true, Description: "How many periods of the alert's period back to evaluate, " +
			"0 through 1000; 0 is the current period"}},
	Fields: []capability.Field{
		{Name: "idAlert", Description: "The alert that was evaluated"},
		{Name: "values", Description: "One entry per website of the alert"},
		{Name: "values[].idSite", Description: "The website the value belongs to"},
		{Name: "values[].value", Description: "The value of the alert's metric; absent when Matomo has none"},
	},
	Examples: []capability.Example{{Description: "Value of alert 4 two periods back",
		Arguments: json.RawMessage(`{"idSite":1,"idAlert":4,"subPeriodN":2}`)}},
}

var alertsListTriggered = capability.Descriptor{
	ID: Provider + ".alerts.list-triggered", Version: 1, Title: "List triggered Matomo alerts",
	Description: "List the entries of alerts that fired for the token's own login, in batches, with the alert " +
		"definition as it was when it fired. An entry appears only when its website lies inside the connection's " +
		"targets. Logins, recipients, names, and conditions are personal or untrusted data; a Microsoft Teams " +
		"address never appears. Needs the plugin CustomAlerts and a view right on the websites",
	Tags: []string{"matomo", "alerts", "notifications", "triggered", "list"}, Risk: readRisk(alertsSensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + pagingKeys + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"triggered":{"type":"array","items":{"type":"object",` +
		`"properties":{` + triggeredProperties + `},"required":["idTriggered","idAlert","idSite"],` +
		`"additionalProperties":false}},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["triggered","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{{Name: "idSite", Target: "site", Description: "Only the entries of " +
		"this website; the entries of every website of the connection when omitted"}}, pagingArguments...),
	Fields: append(append([]capability.Field{{Name: "triggered", Description: "Entries in the order Matomo lists them"},
		{Name: "triggered[].idTriggered", Description: "ID of the entry"},
		{Name: "triggered[].idSite", Description: "The website the alert fired for"},
		{Name: "triggered[].ts_triggered", Description: "Time the alert fired as Matomo stores it (UTC)"},
		{Name: "triggered[].ts_last_sent", Description: "Time the notification was sent as Matomo stores it (UTC); absent if not sent"},
		{Name: "triggered[].value_old", Description: "Value of the earlier period; absent if none"},
		{Name: "triggered[].value_new", Description: "Value of the current period; absent if none"}},
		alertFields("triggered[].")...), pagingFields...),
	Examples: []capability.Example{{Description: "Triggered alerts of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var alertsDelete = capability.Descriptor{
	ID: Provider + ".alerts.delete", Version: 1, Title: "Delete a Matomo alert",
	Description: "Delete one alert of the token's own user for good; the tool cannot restore it, and its triggered " +
		"entries stay. One read first proves that the website is one of the alert's and that all its websites lie " +
		"inside the targets, otherwise the alert is not found; another user's alert is refused as not owned. " +
		"Idempotent in effect" + alertSentOnce + "; offered only where a connection's tools list names it",
	Tags: []string{"matomo", "alerts", "notifications", "delete"}, Risk: capability.Risk{Effect: capability.EffectDelete,
		Idempotency: capability.IdempotencyIdempotent, Confirmation: capability.ConfirmationRequired, OpenWorld: true,
		DataSensitivity: alertsSensitive},
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idAlertSchema + `},` +
		`"required":["idAlert"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idAlert":{"type":"integer"},` +
		`"id_sites":{"type":"array","items":{"type":"integer"}},"deleted":{"type":"boolean"}},` +
		`"required":["idAlert","id_sites","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{alertSiteArgument(), idAlertArgument("delete")},
	Fields: []capability.Field{
		{Name: "idAlert", Description: "ID of the deleted alert"},
		{Name: "id_sites", Description: "The websites the alert watched"},
		{Name: "deleted", Description: "True once Matomo accepted the deletion"},
	},
	Examples: []capability.Example{{Description: "Delete alert 4", Arguments: json.RawMessage(`{"idSite":1,"idAlert":4}`)}},
}

// AlertDefinition is the definition of an alert. All text is untrusted provider data; the login, the
// recipients, and the Slack channel are personal data. The Teams webhook address is a credential and is only
// reported as set or not.
type AlertDefinition struct {
	IDAlert           int      `json:"idAlert"`
	Name              string   `json:"name,omitempty"`
	Description       string   `json:"description,omitempty"`
	Login             string   `json:"login,omitempty"`
	Period            string   `json:"period,omitempty"`
	Report            string   `json:"report,omitempty"`
	ReportCondition   string   `json:"report_condition,omitempty"`
	ReportMatched     string   `json:"report_matched,omitempty"`
	ReportMediums     []string `json:"report_mediums,omitempty"`
	Metric            string   `json:"metric,omitempty"`
	MetricCondition   string   `json:"metric_condition,omitempty"`
	MetricMatched     *float64 `json:"metric_matched,omitempty"`
	ComparedTo        *int     `json:"compared_to,omitempty"`
	EmailMe           *bool    `json:"email_me,omitempty"`
	AdditionalEmails  []string `json:"additional_emails,omitempty"`
	PhoneNumbers      []string `json:"phone_numbers,omitempty"`
	SlackChannelID    string   `json:"slack_channel_id,omitempty"`
	MsTeamsWebhookSet bool     `json:"ms_teams_webhook_set"`
	IDSites           []int    `json:"id_sites,omitempty"`
}

// TriggeredAlert is one entry of alerts.list-triggered.
type TriggeredAlert struct {
	IDTriggered int      `json:"idTriggered"`
	IDSite      int      `json:"idSite"`
	TsTriggered string   `json:"ts_triggered,omitempty"`
	TsLastSent  string   `json:"ts_last_sent,omitempty"`
	ValueOld    *float64 `json:"value_old,omitempty"`
	ValueNew    *float64 `json:"value_new,omitempty"`
	AlertDefinition
}

// AlertsResult is the answer of alerts.list.
type AlertsResult struct {
	Alerts     []AlertDefinition `json:"alerts"`
	HasMore    bool              `json:"has_more"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// TriggeredAlertsResult is the answer of alerts.list-triggered.
type TriggeredAlertsResult struct {
	Triggered  []TriggeredAlert `json:"triggered"`
	HasMore    bool             `json:"has_more"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// AlertValue is the value of an alert for one website.
type AlertValue struct {
	IDSite int      `json:"idSite"`
	Value  *float64 `json:"value,omitempty"`
}

// AlertPastValues is the answer of alerts.get-past-values.
type AlertPastValues struct {
	IDAlert int          `json:"idAlert"`
	Values  []AlertValue `json:"values"`
}

// AlertDeleted is the answer of alerts.delete.
type AlertDeleted struct {
	IDAlert int   `json:"idAlert"`
	IDSites []int `json:"id_sites"`
	Deleted bool  `json:"deleted"`
}

// alertRowJSON is an alert row of CustomAlerts as Model::completeAlerts leaves it, also the alert part of a
// triggered entry. Every member is read loosely; the Teams address is only read to be registered for redaction.
type alertRowJSON struct {
	IDAlert          *loose          `json:"idalert"`
	Name             *loose          `json:"name"`
	Description      *loose          `json:"description"`
	Login            *loose          `json:"login"`
	Period           *loose          `json:"period"`
	Report           *loose          `json:"report"`
	ReportCondition  *loose          `json:"report_condition"`
	ReportMatched    *loose          `json:"report_matched"`
	ReportMediums    json.RawMessage `json:"report_mediums"`
	Metric           *loose          `json:"metric"`
	MetricCondition  *loose          `json:"metric_condition"`
	MetricMatched    *loose          `json:"metric_matched"`
	ComparedTo       *loose          `json:"compared_to"`
	EmailMe          *loose          `json:"email_me"`
	AdditionalEmails json.RawMessage `json:"additional_emails"`
	PhoneNumbers     json.RawMessage `json:"phone_numbers"`
	SlackChannelID   *loose          `json:"slack_channel_id"`
	MsTeamsWebhook   *loose          `json:"ms_teams_webhook_url"`
	IDSites          json.RawMessage `json:"id_sites"`
	// Members of a triggered entry only.
	IDTriggered *loose `json:"idtriggered"`
	IDSite      *loose `json:"idsite"`
	TsTriggered *loose `json:"ts_triggered"`
	TsLastSent  *loose `json:"ts_last_sent"`
	ValueOld    *loose `json:"value_old"`
	ValueNew    *loose `json:"value_new"`
}

// siteIDs reads id_sites. present is false when the answer holds none; ok is false for a list with anything
// but positive integer IDs, which cannot prove a target.
func (r alertRowJSON) siteIDs() (ids []int, present, ok bool) {
	trimmed := strings.TrimSpace(string(r.IDSites))
	if trimmed == "" || trimmed == "null" {
		return nil, false, true
	}
	var items []loose
	if json.Unmarshal(r.IDSites, &items) != nil || len(items) > maxAlertSites {
		return nil, true, false
	}
	for i := range items {
		id := items[i].number()
		if id == nil || *id < 1 || len(strconv.Itoa(*id)) > maxIDDigits {
			return nil, true, false
		}
		ids = append(ids, *id)
	}
	return ids, true, true
}

// definition converts a row; ok is false for a row without a usable alert ID. A stored Teams address is
// registered for redaction and never copied.
func (r alertRowJSON) definition(red *redact.Redactor, sites []int) (AlertDefinition, bool) {
	id := r.IDAlert.number()
	if id == nil || *id < 1 {
		return AlertDefinition{}, false
	}
	teams := false
	if r.MsTeamsWebhook != nil {
		if address := r.MsTeamsWebhook.str(maxAlertSecret); address != "" {
			teams = true
			if red != nil {
				red.Add(address)
			}
		}
	}
	return AlertDefinition{
		IDAlert: *id, Name: r.Name.str(maxStringLength), Description: r.Description.str(maxStringLength),
		Login: r.Login.str(maxStringLength), Period: r.Period.str(maxStringLength), Report: r.Report.str(maxStringLength),
		ReportCondition: r.ReportCondition.str(maxStringLength), ReportMatched: r.ReportMatched.str(maxStringLength),
		ReportMediums: textList(r.ReportMediums, maxAlertMedium, maxStringLength), Metric: r.Metric.str(maxStringLength),
		MetricCondition: r.MetricCondition.str(maxStringLength), MetricMatched: r.MetricMatched.numeric(),
		ComparedTo: r.ComparedTo.number(), EmailMe: r.EmailMe.flag(),
		AdditionalEmails: textList(r.AdditionalEmails, maxRecipients, maxRecipientText),
		PhoneNumbers:     textList(r.PhoneNumbers, maxRecipients, maxRecipientText),
		SlackChannelID:   r.SlackChannelID.str(maxStringLength), MsTeamsWebhookSet: teams, IDSites: sites,
	}, true
}

// allowsAny reports whether at least one website is inside the scope.
func (s scope) allowsAny(ids []int) bool {
	for _, id := range ids {
		if s.allows(id) {
			return true
		}
	}
	return false
}

func alertIDOf(given *int) (int, error) {
	if given == nil || *given < 1 || len(strconv.Itoa(*given)) > maxIDDigits {
		return 0, invalidArgument("idAlert must be a positive integer alert ID from matomo.alerts.list")
	}
	return *given, nil
}

// alertNotFound is the answer for an alert that is absent or does not watch the website; it does not tell
// which, and names no website.
func alertNotFound(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op,
		Message: "Matomo has no alert with this ID for this website inside the targets of this connection"}
}

// fetchAlert is the one proving read of an alert: getAlert with its ID. The alert must watch the website, which
// selectSite has already proven to lie inside the targets; its other websites do not matter.
func (c *Client) fetchAlert(ctx context.Context, op string, req requirement, red *redact.Redactor, id, site int) (AlertDefinition, error) {
	var row alertRowJSON
	if err := c.call(ctx, op, req, "CustomAlerts.getAlert", url.Values{"idAlert": {strconv.Itoa(id)}}, &row); err != nil {
		return AlertDefinition{}, err
	}
	sites, present, ok := row.siteIDs()
	if !ok {
		return AlertDefinition{}, provider.InvalidResponse(op, "Matomo returned an invalid alert")
	}
	alert, valid := row.definition(red, sites)
	if !valid || alert.IDAlert != id {
		return AlertDefinition{}, provider.InvalidResponse(op, "Matomo returned another alert than asked for")
	}
	if !present || !slices.Contains(sites, site) {
		return AlertDefinition{}, alertNotFound(op)
	}
	return alert, nil
}

// alertSites is the idSites parameter of a list call: the website asked for, or every website of the
// connection, which Matomo reads as all the token views when the connection binds no single websites.
func alertSites(bound scope, site int) string {
	switch {
	case site > 0:
		return strconv.Itoa(site)
	case bound.allSites:
		return "all"
	}
	ids := slices.Clone(bound.sites)
	slices.Sort(ids)
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}

func alertListSite(resolved *config.Resolved, given *int) (scope, int, error) {
	bound, err := boundScope(resolved)
	if err != nil {
		return scope{}, 0, err
	}
	site := 0
	if given != nil {
		if site, err = selectSite(resolved, given); err != nil {
			return scope{}, 0, err
		}
	} else if !bound.reachesSites() {
		return scope{}, 0, invalidArgument("this connection is bound to no website")
	}
	return bound, site, nil
}

func invokeAlertsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list alerts"
	var args struct {
		IDSite    *int   `json:"idSite"`
		AllAlerts bool   `json:"ifSuperUserReturnAllAlerts"`
		Limit     int    `json:"limit"`
		Cursor    string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	bound, site, err := alertListSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	paging, err := startPage(provider.CursorBinding(alertsList.ID, scopeBinding(resolved), site, args.AllAlerts),
		args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSites": {alertSites(bound, site)}}
	if args.AllAlerts {
		params.Set("ifSuperUserReturnAllAlerts", "1")
	}
	var rows []json.RawMessage
	if err := client.call(ctx, op, alertsRequirement(alertsList.ID), "CustomAlerts.getAlerts", params, &rows); err != nil {
		return nil, err
	}
	// Matomo answers with each alert's full website list; an alert counts when it watches a reachable website.
	alerts := []AlertDefinition{}
	for _, data := range rows {
		var row alertRowJSON
		if json.Unmarshal(data, &row) != nil {
			continue
		}
		sites, present, ok := row.siteIDs()
		if !ok || !present || len(sites) == 0 || !bound.allowsAny(sites) || site > 0 && !slices.Contains(sites, site) {
			continue
		}
		if alert, valid := row.definition(red, sites); valid {
			alerts = append(alerts, alert)
		}
	}
	batch, next := slice(paging, alerts)
	return &AlertsResult{Alerts: batch, HasMore: next != "", NextCursor: next}, nil
}

func invokeAlertsListTriggered(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list triggered alerts"
	var args struct {
		IDSite *int   `json:"idSite"`
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	bound, site, err := alertListSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	paging, err := startPage(provider.CursorBinding(alertsListTriggered.ID, scopeBinding(resolved), site),
		args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var rows []json.RawMessage
	if err := client.call(ctx, op, alertsRequirement(alertsListTriggered.ID), "CustomAlerts.getTriggeredAlerts",
		url.Values{"idSites": {alertSites(bound, site)}}, &rows); err != nil {
		return nil, err
	}
	triggered := []TriggeredAlert{}
	for _, data := range rows {
		var row alertRowJSON
		if json.Unmarshal(data, &row) != nil {
			continue
		}
		// Only the website of the entry counts.
		sites, _, ok := row.siteIDs()
		entry, trigger := row.IDSite.number(), row.IDTriggered.number()
		if !ok || entry == nil || trigger == nil || *entry < 1 || *trigger < 1 || !bound.allows(*entry) ||
			site > 0 && *entry != site {
			continue
		}
		definition, valid := row.definition(red, sites)
		if !valid {
			continue
		}
		triggered = append(triggered, TriggeredAlert{IDTriggered: *trigger, IDSite: *entry,
			TsTriggered: row.TsTriggered.str(maxStringLength), TsLastSent: row.TsLastSent.str(maxStringLength),
			ValueOld: row.ValueOld.numeric(), ValueNew: row.ValueNew.numeric(), AlertDefinition: definition})
	}
	batch, next := slice(paging, triggered)
	return &TriggeredAlertsResult{Triggered: batch, HasMore: next != "", NextCursor: next}, nil
}

// alertCall settles the arguments every single-alert tool shares before any secret is read or request is made.
func alertCall(resolved *config.Resolved, idSite, idAlert *int) (site, id int, err error) {
	if id, err = alertIDOf(idAlert); err != nil {
		return 0, 0, err
	}
	if site, err = selectSite(resolved, idSite); err != nil {
		return 0, 0, err
	}
	return site, id, nil
}

func invokeAlertsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get alert"
	var args struct {
		IDSite  *int `json:"idSite"`
		IDAlert *int `json:"idAlert"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, id, err := alertCall(resolved, args.IDSite, args.IDAlert)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	alert, err := client.fetchAlert(ctx, op, alertsRequirement(alertsGet.ID), red, id, site)
	if err != nil {
		return nil, err
	}
	return &alert, nil
}

func invokeAlertsGetPastValues(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get alert values"
	var args struct {
		IDSite     *int `json:"idSite"`
		IDAlert    *int `json:"idAlert"`
		SubPeriodN *int `json:"subPeriodN"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if args.SubPeriodN == nil || *args.SubPeriodN < 0 || *args.SubPeriodN > maxPastPeriods {
		return nil, invalidArgument("subPeriodN must be an integer from 0 through 1000")
	}
	site, id, err := alertCall(resolved, args.IDSite, args.IDAlert)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := alertsRequirement(alertsGetPastValues.ID)
	alert, err := client.fetchAlert(ctx, op, req, red, id, site)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		IDSite *loose          `json:"idSite"`
		Value  json.RawMessage `json:"value"`
	}
	params := url.Values{"idAlert": {strconv.Itoa(id)}, "subPeriodN": {strconv.Itoa(*args.SubPeriodN)}}
	if err := client.call(ctx, op, req, "CustomAlerts.getValuesForAlertInPast", params, &rows); err != nil {
		return nil, err
	}
	out := &AlertPastValues{IDAlert: id, Values: []AlertValue{}}
	for _, row := range rows {
		entry := row.IDSite.number()
		if entry == nil || !slices.Contains(alert.IDSites, *entry) || !client.scope.allows(*entry) {
			continue
		}
		var value loose
		var number *float64
		if json.Unmarshal(row.Value, &value) == nil {
			number = value.numeric()
		}
		out.Values = append(out.Values, AlertValue{IDSite: *entry, Value: number})
	}
	return out, nil
}

func invokeAlertsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete alert"
	var args struct {
		IDSite  *int `json:"idSite"`
		IDAlert *int `json:"idAlert"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, id, err := alertCall(resolved, args.IDSite, args.IDAlert)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := alertsRequirement(alertsDelete.ID)
	alert, err := client.fetchAlert(ctx, op, req, red, id, site)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.change(ctx, op, req, "CustomAlerts.deleteAlert", url.Values{"idAlert": {strconv.Itoa(id)}},
		alertSentOnce, &answer); err != nil {
		return nil, err
	}
	if !isSuccessAnswer(answer) {
		return nil, unclearChange(op, "Matomo returned no confirmation of the deletion", alertSentOnce)
	}
	return &AlertDeleted{IDAlert: id, IDSites: alert.IDSites, Deleted: true}, nil
}

func alertsOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: alertsList, Handler: capability.Handler(invokeAlertsList)},
		{Descriptor: alertsGet, Handler: capability.Handler(invokeAlertsGet)},
		{Descriptor: alertsGetPastValues, Handler: capability.Handler(invokeAlertsGetPastValues)},
		{Descriptor: alertsListTriggered, Handler: capability.Handler(invokeAlertsListTriggered)},
		{Descriptor: alertsDelete, Handler: capability.Handler(invokeAlertsDelete)},
	}
}
