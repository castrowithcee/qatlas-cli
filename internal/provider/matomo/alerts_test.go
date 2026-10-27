package matomo

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const alertConnections = `
  al-tools: {service: matomo, credential: token, tools: [matomo.alerts.list, matomo.alerts.get, matomo.alerts.get-past-values, matomo.alerts.list-triggered, matomo.alerts.delete], targets: [site/1, site/2]}
  al-one: {service: matomo, credential: token, tools: [matomo.alerts.list, matomo.alerts.get, matomo.alerts.get-past-values, matomo.alerts.list-triggered, matomo.alerts.delete], targets: [site/2]}
  al-general: {service: matomo, credential: token, tools: [matomo.alerts.list, matomo.alerts.list-triggered], targets: [site]}
  al-instance: {service: matomo, credential: token, tools: [matomo.alerts.list, matomo.alerts.get, matomo.alerts.delete], targets: [instance]}
  al-effects: {service: matomo, credential: token, effects: [read, delete], targets: [site/1, site/2]}
`

// teamsCanary is a webhook address that must never reach a result, an error, or a log.
const teamsCanary = "https://teams.example.invalid/canary-7f3a91"

func newAlertEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(alertConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

// alertRow follows CustomAlerts Model::completeAlerts: numbers as strings, lists decoded, id_sites added.
func alertRow(id, name, sites string) string {
	return `{"idalert":"` + id + `","name":"` + name + `","description":"d","login":"admin","period":"week",` +
		`"report":"Actions_getPageUrls","report_condition":"matches_exactly","report_matched":"/home",` +
		`"report_mediums":["email","teams"],"metric":"nb_hits","metric_condition":"less_than","metric_matched":"100",` +
		`"compared_to":1,"email_me":1,"additional_emails":["ops@example.org"],"phone_numbers":["phone-1"],` +
		`"slack_channel_id":"C0123","ms_teams_webhook_url":"` + teamsCanary + `","id_sites":` + sites + `}`
}

func triggeredRow(idTriggered, alert, site, sites string) string {
	extra := ""
	if sites != "" {
		extra = `,"id_sites":` + sites
	}
	return `{"idtriggered":"` + idTriggered + `","idalert":"` + alert + `","idsite":"` + site + `",` +
		`"ts_triggered":"2026-10-01 10:00:00","ts_last_sent":null,"value_old":"12.500","value_new":"3.000",` +
		`"name":"t` + idTriggered + `","description":"","login":"admin","period":"day","report":"Actions_get",` +
		`"report_condition":null,"report_matched":null,"report_mediums":["teams"],"metric":"nb_hits",` +
		`"metric_condition":"less_than","metric_matched":"5","compared_to":1,"email_me":"0","additional_emails":[],` +
		`"phone_numbers":[],"slack_channel_id":null,"ms_teams_webhook_url":"` + teamsCanary + `"` + extra + `}`
}

var storedAlerts = []string{
	alertRow("1", "Alert one", `[1]`),
	alertRow("2", "Alert two", `["1","2"]`),
	alertRow("3", "Alert three", `[1,3]`),
	alertRow("4", "Alert four", `[2]`),
	// Alert 8 watches only a website outside the targets of al-tools.
	alertRow("8", "Alert eight", `[3]`),
}

var storedTriggered = []string{
	triggeredRow("11", "1", "1", `[1]`),
	triggeredRow("12", "3", "2", `[1,3]`),
	triggeredRow("13", "3", "3", `[3]`),
	triggeredRow("14", "4", "2", ``),
}

func alertAnswer(c call) *http.Response {
	switch id := c.form.Get("idAlert"); id {
	case "1", "2", "3", "4":
		return ok(storedAlerts[int(id[0]-'1')])
	case "8":
		return ok(storedAlerts[4])
	case "5":
		return failure("You are not the owner of the Alert with ID 5 " + bodyCanary)
	case "6":
		return ok(alertRow("7", "Another", `[1]`))
	case "7":
		return ok(alertRow("7", "No sites", `[]`))
	}
	return failure("Alert with ID 9 does not exist! " + bodyCanary)
}

func alertAnswers(extra answers) answers {
	return with(pluginsOn(), with(answers{
		"CustomAlerts.getAlerts":          func(call) *http.Response { return ok("[" + strings.Join(storedAlerts, ",") + "]") },
		"CustomAlerts.getTriggeredAlerts": func(call) *http.Response { return ok("[" + strings.Join(storedTriggered, ",") + "]") },
		"CustomAlerts.getAlert":           alertAnswer,
		"CustomAlerts.getValuesForAlertInPast": func(call) *http.Response {
			return ok(`[{"idSite":1,"value":"12.5"},{"idSite":2,"value":null},{"idSite":3,"value":9}]`)
		},
		"CustomAlerts.deleteAlert": func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) },
	}, extra))
}

func alertIDsOf(t *testing.T, result string) []int {
	t.Helper()
	var out AlertsResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	ids := []int{}
	for _, a := range out.Alerts {
		ids = append(ids, a.IDAlert)
	}
	return ids
}

func triggeredIDsOf(t *testing.T, result string) []int {
	t.Helper()
	var out TriggeredAlertsResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	ids := []int{}
	for _, a := range out.Triggered {
		ids = append(ids, a.IDTriggered)
	}
	return ids
}

func noAlertLeak(t *testing.T, what, text string) {
	t.Helper()
	for _, leak := range []string{teamsCanary, "teams.example.invalid", "canary-7f3a91", bodyCanary} {
		if strings.Contains(text, leak) {
			t.Errorf("%s contains %q: %s", what, leak, text)
		}
	}
}

func TestAlertToolsAreRegisteredWithTheirRiskAndProfile(t *testing.T) {
	reg := registry(t)
	for id, want := range map[string]struct {
		effect    capability.Effect
		confirm   capability.Confirmation
		allowList bool
	}{
		"matomo.alerts.list":            {capability.EffectRead, capability.ConfirmationNone, false},
		"matomo.alerts.get":             {capability.EffectRead, capability.ConfirmationNone, false},
		"matomo.alerts.get-past-values": {capability.EffectRead, capability.ConfirmationNone, false},
		"matomo.alerts.list-triggered":  {capability.EffectRead, capability.ConfirmationNone, false},
		"matomo.alerts.delete":          {capability.EffectDelete, capability.ConfirmationRequired, true},
	} {
		d, _, found := reg.Lookup(id)
		if !found || d.Risk.Effect != want.effect || d.Risk.Confirmation != want.confirm || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity != alertsSensitive || d.RequiresToolAllowList != want.allowList ||
			d.Risk.Idempotency == "" {
			t.Errorf("%s: found %t, %+v", id, found, d)
		}
		if want.effect == capability.EffectRead && d.Risk.Idempotency != capability.IdempotencySafe {
			t.Errorf("%s idempotency = %s", id, d.Risk.Idempotency)
		}
	}
	for _, id := range []string{"add", "edit", "update", "pause", "create"} {
		if _, _, found := reg.Lookup("matomo.alerts." + id); found {
			t.Errorf("matomo.alerts.%s exists", id)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		joined := " " + strings.Join(profile.Tools, " ") + " "
		inLive := profile.ID == "live"
		for _, id := range []string{"list", "get", "get-past-values", "list-triggered"} {
			if strings.Contains(joined, " matomo.alerts."+id+" ") != inLive {
				t.Errorf("profile %s and matomo.alerts.%s: %v", profile.ID, id, profile.Tools)
			}
		}
		if strings.Contains(joined, "matomo.alerts.delete") {
			t.Errorf("profile %s selects alerts.delete", profile.ID)
		}
	}
	for _, id := range []string{"matomo.alerts.get", "matomo.alerts.get-past-values", "matomo.alerts.delete"} {
		d, _, _ := reg.Lookup(id)
		if strings.Contains(string(d.InputSchema), "ms_teams") || strings.Contains(string(d.InputSchema), "callback") {
			t.Errorf("%s schema = %s", id, d.InputSchema)
		}
	}
}

func TestAlertsListKeepsAlertsWithAReachableWebsiteAndPages(t *testing.T) {
	var calls []call
	env := newAlertEnvironment(t, &calls, alertAnswers(nil).handler(t))
	result, err := env.invoke("matomo.alerts.list", "al-tools", `{}`)
	if err != nil || !slices.Equal(alertIDsOf(t, result), []int{1, 2, 3, 4}) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	noAlertLeak(t, "result", result)
	var out AlertsResult
	_ = json.Unmarshal([]byte(result), &out)
	second := out.Alerts[1]
	// An alert that also watches a website outside the targets keeps its full list of websites.
	if !slices.Equal(out.Alerts[2].IDSites, []int{1, 3}) {
		t.Errorf("third = %+v", out.Alerts[2])
	}
	if !out.Alerts[0].MsTeamsWebhookSet || second.Name != "Alert two" || second.Login != "admin" || second.Period != "week" ||
		second.Report != "Actions_getPageUrls" || second.ReportMatched != "/home" || second.MetricMatched == nil ||
		*second.MetricMatched != 100 || second.ComparedTo == nil || *second.ComparedTo != 1 || second.EmailMe == nil ||
		!*second.EmailMe || !slices.Equal(second.AdditionalEmails, []string{"ops@example.org"}) ||
		!slices.Equal(second.IDSites, []int{1, 2}) || !slices.Equal(second.ReportMediums, []string{"email", "teams"}) ||
		second.SlackChannelID != "C0123" {
		t.Errorf("second = %+v", second)
	}
	sent := lastCall(calls, "CustomAlerts.getAlerts")
	if sent.form.Get("idSites") != "1,2" || sent.form.Has("ifSuperUserReturnAllAlerts") || sent.auth != "Bearer "+tokenValue ||
		sent.rawQuery != "" || sent.form.Has("token_auth") || sent.form.Get("format") != "JSON" {
		t.Errorf("request = %+v", sent)
	}
	result, err = env.invoke("matomo.alerts.list", "al-tools", `{"limit":2}`)
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || len(out.Alerts) != 2 || !out.HasMore || out.NextCursor == "" {
		t.Fatalf("page one: %s, err = %v", result, err)
	}
	next := out.NextCursor
	result, err = env.invoke("matomo.alerts.list", "al-tools", `{"cursor":"`+next+`"}`)
	if err != nil || !slices.Equal(alertIDsOf(t, result), []int{3, 4}) {
		t.Errorf("page two: %s, err = %v", result, err)
	}
	if _, err := env.invoke("matomo.alerts.list", "al-tools", `{"idSite":1,"cursor":"`+next+`"}`); !isInvalidRequest(err) {
		t.Errorf("a cursor of other arguments: err = %v", err)
	}
	result, err = env.invoke("matomo.alerts.list", "al-tools", `{"idSite":2,"ifSuperUserReturnAllAlerts":true}`)
	sent = lastCall(calls, "CustomAlerts.getAlerts")
	if err != nil || !slices.Equal(alertIDsOf(t, result), []int{2, 4}) || sent.form.Get("idSite") != "" ||
		sent.form.Get("idSites") != "2" || sent.form.Get("ifSuperUserReturnAllAlerts") != "1" {
		t.Errorf("filters: %s, err = %v, request = %v", result, err, sent.form)
	}
	// A connection bound to one website lists the alerts that watch that website, also with other websites.
	result, err = env.invoke("matomo.alerts.list", "al-one", `{}`)
	if err != nil || !slices.Equal(alertIDsOf(t, result), []int{2, 4}) {
		t.Errorf("one website: %s, err = %v", result, err)
	}
	if _, err = env.invoke("matomo.alerts.list", "al-general", `{}`); err != nil ||
		lastCall(calls, "CustomAlerts.getAlerts").form.Get("idSites") != "all" {
		t.Errorf("general binding: err = %v", err)
	}
}

func TestAlertsListDropsRowsItCannotProve(t *testing.T) {
	var calls []call
	rows := `[` + alertRow("1", "ok", `[1]`) + `,` + alertRow("2", "no sites", `[]`) + `,` +
		alertRow("3", "bad id", `[1,"x"]`) + `,` + strings.Replace(alertRow("4", "absent", `[1]`), `,"id_sites":[1]`, "", 1) + `]`
	env := newAlertEnvironment(t, &calls, alertAnswers(answers{"CustomAlerts.getAlerts": fixed(rows)}).handler(t))
	result, err := env.invoke("matomo.alerts.list", "al-tools", `{}`)
	if err != nil || !slices.Equal(alertIDsOf(t, result), []int{1}) {
		t.Errorf("result = %s, err = %v", result, err)
	}
	noAlertLeak(t, "result", result)
}

func TestAlertsListRefusesBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newAlertEnvironment(t, &calls, alertAnswers(nil).handler(t))
	for _, c := range [][2]string{{"al-tools", `{"idSite":3}`}, {"al-tools", `{"idSite":0}`}, {"al-tools", `{"callback":"x"}`},
		{"al-tools", `{"idSites":"all"}`}, {"al-tools", `{"limit":0}`}, {"al-instance", `{}`}} {
		if _, err := env.invoke("matomo.alerts.list", c[0], c[1]); err == nil {
			t.Errorf("%s %s was accepted", c[0], c[1])
		}
	}
	for _, c := range [][2]string{{"al-tools", `{"idSite":3}`}, {"al-tools", `{"idSite":99}`}} {
		_, err := env.invoke("matomo.alerts.list-triggered", c[0], c[1])
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "99") {
			t.Errorf("list-triggered %s: err = %v", c[1], err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestAlertsListTriggeredKeepsEntriesInsideTheTargets(t *testing.T) {
	var calls []call
	env := newAlertEnvironment(t, &calls, alertAnswers(nil).handler(t))
	result, err := env.invoke("matomo.alerts.list-triggered", "al-tools", `{}`)
	// Only the website of the entry counts: entry 12 is for website 2 though its alert also watches website 3;
	// entry 13 is for website 3.
	if err != nil || !slices.Equal(triggeredIDsOf(t, result), []int{11, 12, 14}) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	noAlertLeak(t, "result", result)
	var out TriggeredAlertsResult
	_ = json.Unmarshal([]byte(result), &out)
	first := out.Triggered[0]
	if first.IDAlert != 1 || first.IDSite != 1 || first.TsTriggered != "2026-10-01 10:00:00" || first.TsLastSent != "" ||
		first.ValueOld == nil || *first.ValueOld != 12.5 || first.ValueNew == nil || *first.ValueNew != 3 ||
		!first.MsTeamsWebhookSet || first.Name != "t11" {
		t.Errorf("first = %+v", first)
	}
	if sent := lastCall(calls, "CustomAlerts.getTriggeredAlerts"); sent.form.Get("idSites") != "1,2" || sent.auth != "Bearer "+tokenValue ||
		sent.rawQuery != "" || sent.form.Has("token_auth") {
		t.Errorf("request = %+v", sent)
	}
	result, err = env.invoke("matomo.alerts.list-triggered", "al-tools", `{"idSite":2,"limit":2}`)
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || !slices.Equal(triggeredIDsOf(t, result), []int{12, 14}) || out.HasMore ||
		lastCall(calls, "CustomAlerts.getTriggeredAlerts").form.Get("idSites") != "2" {
		t.Errorf("website 2: %s, err = %v", result, err)
	}
	result, err = env.invoke("matomo.alerts.list-triggered", "al-tools", `{"limit":1}`)
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || len(out.Triggered) != 1 || !out.HasMore || out.NextCursor == "" {
		t.Errorf("paged: %s, err = %v", result, err)
	}
}

func TestAlertsGetReadsOnceAndHidesTheTeamsAddress(t *testing.T) {
	var calls []call
	env := newAlertEnvironment(t, &calls, alertAnswers(nil).handler(t))
	result, err := env.invoke("matomo.alerts.get", "al-tools", `{"idSite":2,"idAlert":2}`)
	if err != nil {
		t.Fatal(err)
	}
	noAlertLeak(t, "result", result)
	var out AlertDefinition
	_ = json.Unmarshal([]byte(result), &out)
	if out.IDAlert != 2 || out.Name != "Alert two" || !out.MsTeamsWebhookSet || !slices.Equal(out.IDSites, []int{1, 2}) ||
		strings.Contains(result, "ms_teams_webhook_url") {
		t.Errorf("result = %s", result)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated CustomAlerts.getAlert" {
		t.Errorf("requests = %s", got)
	}
	if sent := lastCall(calls, "CustomAlerts.getAlert"); sent.form.Get("idAlert") != "2" || sent.auth != "Bearer "+tokenValue ||
		sent.rawQuery != "" || sent.form.Has("token_auth") {
		t.Errorf("request = %+v", sent)
	}
	// The website defaults to the only one the connection binds.
	if _, err := env.invoke("matomo.alerts.get", "al-one", `{"idAlert":4}`); err != nil {
		t.Errorf("default website: %v", err)
	}
	// Without a teams address the flag is false.
	plain := strings.Replace(storedAlerts[0], teamsCanary, "", 1)
	env = newAlertEnvironment(t, &calls, alertAnswers(answers{"CustomAlerts.getAlert": fixed(plain)}).handler(t))
	if result, err = env.invoke("matomo.alerts.get", "al-tools", `{"idSite":1,"idAlert":1}`); err != nil ||
		!strings.Contains(result, `"ms_teams_webhook_set":false`) {
		t.Errorf("no address: %s, err = %v", result, err)
	}
}

func TestAlertsRefuseBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newAlertEnvironment(t, &calls, alertAnswers(nil).handler(t))
	for tool, arguments := range map[string][]string{
		"matomo.alerts.get": {`{}`, `{"idAlert":0}`, `{"idAlert":"2"}`, `{"idSite":3,"idAlert":2}`, `{"idSite":3,"idAlert":3}`, `{"idSite":2,"idAlert":2,"login":"x"}`},
		"matomo.alerts.get-past-values": {`{"idAlert":2}`, `{"idAlert":2,"subPeriodN":-1}`, `{"idAlert":2,"subPeriodN":1001}`,
			`{"idSite":3,"idAlert":2,"subPeriodN":1}`, `{"idSite":3,"idAlert":3,"subPeriodN":1}`, `{"idAlert":2,"subPeriodN":1,"period":"day"}`},
		"matomo.alerts.delete": {`{}`, `{"idAlert":0}`, `{"idSite":3,"idAlert":2}`, `{"idSite":3,"idAlert":3}`, `{"idAlert":2,"msTeamsWebhookUrl":"x"}`},
	} {
		for _, arguments := range arguments {
			if _, err := env.change(tool, "al-tools", arguments); err == nil {
				t.Errorf("%s %s was accepted", tool, arguments)
			}
		}
	}
	// A connection with several websites needs the website named; an instance binding reaches none.
	for _, c := range [][3]string{{"matomo.alerts.get", "al-tools", `{"idAlert":2}`},
		{"matomo.alerts.delete", "al-tools", `{"idAlert":2}`}, {"matomo.alerts.get", "al-instance", `{"idAlert":2}`},
		{"matomo.alerts.delete", "al-instance", `{"idSite":1,"idAlert":2}`}} {
		if _, err := env.change(c[0], c[1], c[2]); err == nil {
			t.Errorf("%v was accepted", c)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestAlertOfAnotherWebsiteIsRefusedAfterOneProvingReadWithoutFurtherRequest(t *testing.T) {
	for _, c := range []struct{ tool, connection, arguments string }{
		// Alert 8 watches only website 3, outside the targets.
		{"get", "al-tools", `{"idSite":1,"idAlert":8}`},
		{"get-past-values", "al-tools", `{"idSite":1,"idAlert":8,"subPeriodN":0}`},
		{"delete", "al-tools", `{"idSite":1,"idAlert":8}`},
		// Alert 3 does not watch website 2.
		{"get", "al-one", `{"idAlert":3}`},
		{"get-past-values", "al-one", `{"idAlert":3,"subPeriodN":0}`},
		{"delete", "al-one", `{"idAlert":3}`},
		// Alert 1 does not watch website 2.
		{"get", "al-tools", `{"idSite":2,"idAlert":1}`},
		{"get-past-values", "al-tools", `{"idSite":2,"idAlert":1,"subPeriodN":0}`},
		{"delete", "al-tools", `{"idSite":2,"idAlert":1}`},
		// An alert without any website proves nothing.
		{"delete", "al-tools", `{"idSite":1,"idAlert":7}`},
		{"get", "al-tools", `{"idSite":1,"idAlert":7}`},
	} {
		var calls []call
		env := newAlertEnvironment(t, &calls, alertAnswers(nil).handler(t))
		result, err := env.change("matomo.alerts."+c.tool, c.connection, c.arguments)
		if err == nil || result != "" || classOf(err) != provider.ClassNotFound || strings.Contains(err.Error(), "3") ||
			countMethod(calls, "CustomAlerts.getAlert") != 1 || countMethod(calls, "CustomAlerts.getValuesForAlertInPast") != 0 ||
			countMethod(calls, "CustomAlerts.deleteAlert") != 0 {
			t.Errorf("%s %s %s: err = %v, requests = %s", c.connection, c.tool, c.arguments, err, methods(calls))
		}
		noAlertLeak(t, "error", err.Error())
	}
	// Another alert than the one asked for is not trusted.
	var calls []call
	env := newAlertEnvironment(t, &calls, alertAnswers(nil).handler(t))
	if _, err := env.change("matomo.alerts.delete", "al-tools", `{"idSite":1,"idAlert":6}`); classOf(err) != provider.ClassInvalidResponse ||
		countMethod(calls, "CustomAlerts.deleteAlert") != 0 {
		t.Errorf("mismatched row: err = %v", err)
	}
}

func TestAlertWithAWebsiteOutsideTheTargetsStillServesTheReachableOne(t *testing.T) {
	var calls []call
	env := newAlertEnvironment(t, &calls, alertAnswers(nil).handler(t))
	result, err := env.invoke("matomo.alerts.get", "al-tools", `{"idSite":1,"idAlert":3}`)
	var got AlertDefinition
	_ = json.Unmarshal([]byte(result), &got)
	if err != nil || !slices.Equal(got.IDSites, []int{1, 3}) {
		t.Errorf("get: %s, err = %v", result, err)
	}
	// The values of website 3 are left out.
	result, err = env.invoke("matomo.alerts.get-past-values", "al-tools", `{"idSite":1,"idAlert":3,"subPeriodN":0}`)
	if err != nil || result != `{"idAlert":3,"values":[{"idSite":1,"value":12.5}]}` {
		t.Errorf("past values: %s, err = %v", result, err)
	}
	// The deletion follows the proving read; Matomo checks the ownership.
	result, err = env.change("matomo.alerts.delete", "al-tools", `{"idSite":1,"idAlert":3}`)
	if err != nil || result != `{"deleted":true,"idAlert":3,"id_sites":[1,3]}` ||
		countMethod(calls, "CustomAlerts.deleteAlert") != 1 {
		t.Errorf("delete: %s, err = %v", result, err)
	}
}

func TestAlertsGetPastValuesReadsOnceThenEvaluates(t *testing.T) {
	var calls []call
	env := newAlertEnvironment(t, &calls, alertAnswers(nil).handler(t))
	result, err := env.invoke("matomo.alerts.get-past-values", "al-tools", `{"idSite":1,"idAlert":2,"subPeriodN":3}`)
	if err != nil || result != `{"idAlert":2,"values":[{"idSite":1,"value":12.5},{"idSite":2}]}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated CustomAlerts.getAlert CustomAlerts.getValuesForAlertInPast" {
		t.Errorf("requests = %s", got)
	}
	sent := lastCall(calls, "CustomAlerts.getValuesForAlertInPast")
	if sent.form.Get("idAlert") != "2" || sent.form.Get("subPeriodN") != "3" || sent.auth != "Bearer "+tokenValue ||
		sent.rawQuery != "" || sent.form.Has("token_auth") {
		t.Errorf("request = %+v", sent)
	}
	if result, err = env.invoke("matomo.alerts.get-past-values", "al-tools", `{"idSite":1,"idAlert":2,"subPeriodN":0}`); err != nil ||
		lastCall(calls, "CustomAlerts.getValuesForAlertInPast").form.Get("subPeriodN") != "0" {
		t.Errorf("subPeriodN 0: %s, err = %v", result, err)
	}
}

func TestAlertsDeleteReadsOnceThenDeletesOnce(t *testing.T) {
	var calls []call
	env := newAlertEnvironment(t, &calls, alertAnswers(nil).handler(t))
	if _, err := env.invoke("matomo.alerts.delete", "al-tools", `{"idSite":1,"idAlert":2}`); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("without confirm: err = %v", err)
	}
	if _, err := env.change("matomo.alerts.delete", "al-effects", `{"idSite":1,"idAlert":2}`); err == nil {
		t.Fatal("a connection with an effects list offered alerts.delete")
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("a refusal caused %d requests and %d secret reads", len(calls), *env.reads)
	}
	result, err := env.change("matomo.alerts.delete", "al-tools", `{"idSite":1,"idAlert":2}`)
	if err != nil || result != `{"deleted":true,"idAlert":2,"id_sites":[1,2]}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated CustomAlerts.getAlert CustomAlerts.deleteAlert" {
		t.Errorf("requests = %s", got)
	}
	if sent := calls[len(calls)-1]; sent.form.Get("idAlert") != "2" || sent.auth != "Bearer "+tokenValue || sent.rawQuery != "" ||
		sent.form.Has("token_auth") || sent.form.Has("idSite") {
		t.Errorf("request = %+v", sent)
	}
}

func TestAlertsDeleteIsSentOnceAndMapsErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		answer  func(call) (*http.Response, error)
		class   provider.Class
		unclear bool
	}{
		"timeout":         {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, true},
		"502":             {func(call) (*http.Response, error) { return respond(http.StatusBadGateway, ""), nil }, "", true},
		"500":             {func(call) (*http.Response, error) { return respond(http.StatusInternalServerError, bodyCanary), nil }, "", true},
		"unreadable":      {func(call) (*http.Response, error) { return ok("<html>" + bodyCanary + "</html>"), nil }, provider.ClassInvalidResponse, true},
		"no confirmation": {func(call) (*http.Response, error) { return ok(`[]`), nil }, provider.ClassInvalidResponse, true},
		"permission":      {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false},
		"not owner": {func(call) (*http.Response, error) {
			return ok2(failure("You are not the owner of the Alert with ID 2 " + bodyCanary))
		}, provider.ClassPermission, false},
		"not found": {func(call) (*http.Response, error) {
			return ok2(failure("Alert with ID 2 does not exist! " + bodyCanary))
		}, provider.ClassNotFound, false},
		"429": {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, ""), nil }, provider.ClassRateLimited, false},
	} {
		var calls []call
		env := newAlertEnvironment(t, &calls, func(c call) (*http.Response, error) {
			if c.api() == "CustomAlerts.deleteAlert" {
				return tc.answer(c)
			}
			return alertAnswers(nil).handler(t)(c)
		})
		_, err := env.change("matomo.alerts.delete", "al-tools", `{"idSite":1,"idAlert":2}`)
		if err == nil || classOf(err) != tc.class && tc.class != "" || countMethod(calls, "CustomAlerts.deleteAlert") != 1 {
			t.Errorf("%s: err = %v, sent %d times", name, err, countMethod(calls, "CustomAlerts.deleteAlert"))
			continue
		}
		noAlertLeak(t, name, err.Error())
		if tc.unclear && !strings.Contains(err.Error(), "never repeated") {
			t.Errorf("%s: err = %v does not say that the outcome is unclear", name, err)
		}
	}
}

func TestAlertReadsMapMatomoErrorsAndHTTPFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		id     string
		answer func(call) *http.Response
		class  provider.Class
	}{
		"not owner":  {"5", nil, provider.ClassPermission},
		"not found":  {"9", nil, provider.ClassNotFound},
		"view right": {"1", func(call) *http.Response { return failure(permissionMsg + bodyCanary) }, provider.ClassPermission},
		"429":        {"1", func(call) *http.Response { return respond(http.StatusTooManyRequests, bodyCanary) }, provider.ClassRateLimited},
		"html":       {"1", fixed("<html>" + bodyCanary + "</html>"), provider.ClassInvalidResponse},
		"not object": {"1", fixed(`[]`), provider.ClassInvalidResponse},
	} {
		var calls []call
		extra := answers{}
		if tc.answer != nil {
			extra["CustomAlerts.getAlert"] = tc.answer
		}
		env := newAlertEnvironment(t, &calls, alertAnswers(extra).handler(t))
		_, err := env.invoke("matomo.alerts.get", "al-tools", `{"idSite":1,"idAlert":`+tc.id+`}`)
		if err == nil || classOf(err) != tc.class || countMethod(calls, "CustomAlerts.getAlert") != 1 {
			t.Errorf("%s: err = %v", name, err)
			continue
		}
		noAlertLeak(t, name, err.Error())
	}
	for name, answer := range map[string]func(call) (*http.Response, error){
		"timeout":    func(call) (*http.Response, error) { return nil, errTimeout },
		"unreadable": func(call) (*http.Response, error) { return ok("<html>" + bodyCanary + "</html>"), nil },
		"error":      func(call) (*http.Response, error) { return ok2(failure("Something " + teamsCanary + bodyCanary)) },
	} {
		for _, method := range []string{"CustomAlerts.getAlerts", "CustomAlerts.getTriggeredAlerts", "CustomAlerts.getValuesForAlertInPast"} {
			var calls []call
			env := newAlertEnvironment(t, &calls, func(c call) (*http.Response, error) {
				if c.api() == method {
					return answer(c)
				}
				return alertAnswers(nil).handler(t)(c)
			})
			tool, arguments := "matomo.alerts.list", `{}`
			switch method {
			case "CustomAlerts.getTriggeredAlerts":
				tool = "matomo.alerts.list-triggered"
			case "CustomAlerts.getValuesForAlertInPast":
				tool, arguments = "matomo.alerts.get-past-values", `{"idSite":1,"idAlert":2,"subPeriodN":1}`
			}
			_, err := env.invoke(tool, "al-tools", arguments)
			if err == nil || countMethod(calls, method) != 1 {
				t.Errorf("%s %s: err = %v", name, method, err)
				continue
			}
			noAlertLeak(t, name, err.Error())
			if name == "timeout" && classOf(err) != provider.ClassTimeout {
				t.Errorf("%s %s: class = %s", name, method, classOf(err))
			}
		}
	}
}

func TestAlertToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range []struct{ tool, arguments string }{
		{"matomo.alerts.list", `{}`}, {"matomo.alerts.list-triggered", `{}`}, {"matomo.alerts.get", `{"idSite":1,"idAlert":1}`},
		{"matomo.alerts.get-past-values", `{"idSite":1,"idAlert":1,"subPeriodN":0}`}, {"matomo.alerts.delete", `{"idSite":1,"idAlert":1}`},
	} {
		var calls []call
		env := newAlertEnvironment(t, &calls, with(alertAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.change(c.tool, "al-tools", c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}

func TestAlertMethodMissingIsUnsupported(t *testing.T) {
	var calls []call
	env := newAlertEnvironment(t, &calls, alertAnswers(answers{
		"CustomAlerts.getAlerts": func(call) *http.Response {
			return failure("The method CustomAlerts.getAlerts is not available in the module CustomAlerts.")
		},
	}).handler(t))
	if _, err := env.invoke("matomo.alerts.list", "al-tools", `{}`); !isUnsupported(err) {
		t.Errorf("err = %v", err)
	}
}

func TestTeamsAddressIsRegisteredForRedaction(t *testing.T) {
	var calls []call
	env := newAlertEnvironment(t, &calls, alertAnswers(nil).handler(t))
	if _, err := env.invoke("matomo.alerts.get", "al-tools", `{"idSite":1,"idAlert":1}`); err != nil {
		t.Fatal(err)
	}
	if got := env.red.Apply("address " + teamsCanary + " end"); strings.Contains(got, teamsCanary) {
		t.Errorf("redacted text = %q", got)
	}
}
