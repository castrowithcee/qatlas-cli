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

const reportConnections = `
  sr-tools: {service: matomo, credential: token, tools: [matomo.scheduled-reports.list, matomo.scheduled-reports.generate, matomo.scheduled-reports.delete, matomo.scheduled-reports.pause], targets: [site/1, site/2]}
  sr-one: {service: matomo, credential: token, tools: [matomo.scheduled-reports.generate, matomo.scheduled-reports.delete, matomo.scheduled-reports.pause], targets: [site/2]}
  sr-instance: {service: matomo, credential: token, tools: [matomo.scheduled-reports.generate, matomo.scheduled-reports.delete, matomo.scheduled-reports.pause], targets: [instance]}
  sr-effects: {service: matomo, credential: token, effects: [read, delete], targets: [site/1, site/2]}
  sr-update: {service: matomo, credential: token, effects: [read, update], targets: [site/1, site/2]}
`

func newReportEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(reportConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

// scheduledRow follows ScheduledReports.getReports: numbers as strings, parameters and reports decoded.
func scheduledRow(id, site, description, period, format, parameters string) string {
	return `{"idreport":"` + id + `","idsite":"` + site + `","login":"admin","description":"` + description +
		`","idsegment":null,"period":"` + period + `","period_param":"` + period + `","hour":"6","type":"email","format":"` +
		format + `","reports":["VisitsSummary_get","Actions_getPageUrls"],"parameters":` + parameters +
		`,"ts_created":"2026-01-02 03:04:05","ts_last_sent":null,"deleted":"0","evolution_graph_within_period":"0",` +
		`"evolution_graph_period_n":"30"}`
}

const emailParameters = `{"displayFormat":"1","emailMe":true,"evolutionGraph":false,"additionalEmails":["a@example.com","b@example.com"]}`

var storedReports = []string{
	scheduledRow("1", "1", "Weekly one", "week", "html", emailParameters),
	scheduledRow("2", "2", "Daily two", "day", "csv", `{"emailMe":false,"additionalEmails":[]}`),
	scheduledRow("3", "3", "Monthly three", "month", "pdf", `[]`),
}

func reportAnswer(c call) *http.Response {
	switch id := c.form.Get("idReport"); id {
	case "":
		return ok("[" + strings.Join(storedReports, ",") + "]")
	case "1", "2", "3":
		n := int(id[0] - '0')
		return ok("[" + storedReports[n-1] + "]")
	case "8":
		return ok("[" + storedReports[1] + "]")
	case "7":
		return ok(`[]`)
	}
	return failure("Requested report couldn't be found.")
}

func reportAnswers(extra answers) answers {
	return with(versionOnly("5.14.2"), with(answers{
		"API.isPluginActivated":           func(call) *http.Response { return ok(`{"value":true}`) },
		"ScheduledReports.getReports":     reportAnswer,
		"ScheduledReports.generateReport": func(call) *http.Response { return ok(`{"value":"label;visits\nhome;4\n"}`) },
		"ScheduledReports.deleteReport":   func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) },
	}, extra))
}

func reportIDsOf(t *testing.T, result string) []int {
	t.Helper()
	var out ScheduledReportsResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	ids := []int{}
	for _, r := range out.Reports {
		ids = append(ids, r.IDReport)
	}
	return ids
}

func TestScheduledReportToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	for id, want := range map[string]struct {
		effect    capability.Effect
		confirm   capability.Confirmation
		allowList bool
	}{
		"matomo.scheduled-reports.list":     {capability.EffectRead, capability.ConfirmationNone, false},
		"matomo.scheduled-reports.generate": {capability.EffectRead, capability.ConfirmationNone, false},
		"matomo.scheduled-reports.delete":   {capability.EffectDelete, capability.ConfirmationRequired, true},
		"matomo.scheduled-reports.pause":    {capability.EffectUpdate, capability.ConfirmationRequired, false},
	} {
		d, _, found := reg.Lookup(id)
		if !found || d.Risk.Effect != want.effect || d.Risk.Confirmation != want.confirm || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity == "" || d.RequiresToolAllowList != want.allowList {
			t.Errorf("%s: found %t, %+v", id, found, d)
		}
	}
	list, _, _ := reg.Lookup("matomo.scheduled-reports.list")
	if list.Risk.DataSensitivity != scheduledReportsSensitive {
		t.Errorf("list sensitivity = %q", list.Risk.DataSensitivity)
	}
	// The tools that create, change, or send a report do not exist.
	for _, id := range []string{"add", "update", "send", "resume"} {
		if _, _, found := reg.Lookup("matomo.scheduled-reports." + id); found {
			t.Errorf("matomo.scheduled-reports.%s exists", id)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		joined := strings.Join(profile.Tools, " ")
		inLive := profile.ID == "live"
		if strings.Contains(joined, "matomo.scheduled-reports.list") != inLive ||
			strings.Contains(joined, "matomo.scheduled-reports.generate") != inLive ||
			strings.Contains(joined, "matomo.scheduled-reports.delete") || strings.Contains(joined, "matomo.scheduled-reports.pause") {
			t.Errorf("profile %s selects scheduled reports wrongly: %v", profile.ID, profile.Tools)
		}
	}
	generate, _, _ := reg.Lookup("matomo.scheduled-reports.generate")
	for _, name := range []string{"outputType", "parameters", "pdf"} {
		if strings.Contains(string(generate.InputSchema), name) {
			t.Errorf("generate schema mentions %s", name)
		}
	}
}

func TestScheduledReportsListFiltersRowsByTargetsAndPages(t *testing.T) {
	var calls []call
	env := newReportEnvironment(t, &calls, reportAnswers(nil).handler(t))
	result, err := env.invoke("matomo.scheduled-reports.list", "sr-tools", `{}`)
	if err != nil || !slices.Equal(reportIDsOf(t, result), []int{1, 2}) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	var out ScheduledReportsResult
	_ = json.Unmarshal([]byte(result), &out)
	first := out.Reports[0]
	if first.IDSite != 1 || first.Description != "Weekly one" || first.Login != "admin" || first.Period != "week" ||
		first.Format != "html" || first.Hour == nil || *first.Hour != 6 || first.EmailMe == nil || !*first.EmailMe ||
		!slices.Equal(first.AdditionalEmails, []string{"a@example.com", "b@example.com"}) ||
		!slices.Equal(first.Reports, []string{"VisitsSummary_get", "Actions_getPageUrls"}) || first.IDSegment != 0 ||
		first.DisplayFormat == nil || *first.DisplayFormat != 1 || first.TsLastSent != "" {
		t.Errorf("first = %+v", first)
	}
	sent := lastCall(calls, "ScheduledReports.getReports")
	for _, name := range []string{"idSite", "period", "idReport", "ifSuperUserReturnOnlySuperUserReports", "idSegment"} {
		if sent.form.Has(name) {
			t.Errorf("an argument not given still sent %s", name)
		}
	}
	if sent.auth != "Bearer "+tokenValue || sent.rawQuery != "" || sent.form.Has("token_auth") || sent.form.Get("format") != "JSON" {
		t.Errorf("request = %+v", sent)
	}
	// A limit pages over the rows inside the targets.
	result, err = env.invoke("matomo.scheduled-reports.list", "sr-tools", `{"limit":1}`)
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || len(out.Reports) != 1 || !out.HasMore || out.NextCursor == "" {
		t.Fatalf("page one: %s, err = %v", result, err)
	}
	next := out.NextCursor
	result, err = env.invoke("matomo.scheduled-reports.list", "sr-tools", `{"cursor":"`+next+`"}`)
	if err != nil || !slices.Equal(reportIDsOf(t, result), []int{2}) {
		t.Errorf("page two: %s, err = %v", result, err)
	}
	if _, err := env.invoke("matomo.scheduled-reports.list", "sr-tools", `{"idSite":1,"cursor":"`+next+`"}`); !isInvalidRequest(err) {
		t.Errorf("a cursor of other arguments: err = %v", err)
	}
	result, err = env.invoke("matomo.scheduled-reports.list", "sr-tools",
		`{"idSite":2,"period":"day","idReport":2,"ifSuperUserReturnOnlySuperUserReports":true,"idSegment":5}`)
	sent = lastCall(calls, "ScheduledReports.getReports")
	if err != nil || !slices.Equal(reportIDsOf(t, result), []int{2}) || sent.form.Get("idSite") != "2" ||
		sent.form.Get("period") != "day" || sent.form.Get("idReport") != "2" || sent.form.Get("idSegment") != "5" ||
		sent.form.Get("ifSuperUserReturnOnlySuperUserReports") != "1" {
		t.Errorf("filters: %s, err = %v, request = %v", result, err, sent.form)
	}
}

func TestScheduledReportsListRefusesBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newReportEnvironment(t, &calls, reportAnswers(nil).handler(t))
	for _, c := range [][2]string{{"sr-tools", `{"idSite":3}`}, {"sr-tools", `{"period":"year"}`}, {"sr-tools", `{"idReport":0}`},
		{"sr-tools", `{"idSegment":0}`}, {"sr-tools", `{"callback":"x"}`}, {"sr-instance", `{}`}} {
		if _, err := env.invoke("matomo.scheduled-reports.list", c[0], c[1]); err == nil {
			t.Errorf("%s %s was accepted", c[0], c[1])
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestScheduledReportsListMapsFailuresWithoutRetry(t *testing.T) {
	for name, tc := range map[string]struct {
		answer func(call) *http.Response
		class  provider.Class
	}{
		"permission": {func(call) *http.Response { return failure(permissionMsg + bodyCanary) }, provider.ClassPermission},
		"not found":  {func(call) *http.Response { return failure("Requested report couldn't be found. " + bodyCanary) }, provider.ClassNotFound},
		"429":        {func(call) *http.Response { return respond(http.StatusTooManyRequests, bodyCanary) }, provider.ClassRateLimited},
		"html":       {fixed("<html>" + bodyCanary + "</html>"), provider.ClassInvalidResponse},
	} {
		var calls []call
		env := newReportEnvironment(t, &calls, reportAnswers(answers{"ScheduledReports.getReports": tc.answer}).handler(t))
		_, err := env.invoke("matomo.scheduled-reports.list", "sr-tools", `{}`)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || classOf(err) != tc.class ||
			countMethod(calls, "ScheduledReports.getReports") != 1 {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestScheduledReportsGenerateReadsOnceThenRendersWithOutputTypeReturn(t *testing.T) {
	var calls []call
	env := newReportEnvironment(t, &calls, reportAnswers(nil).handler(t))
	result, err := env.invoke("matomo.scheduled-reports.generate", "sr-tools",
		`{"idReport":2,"date":"yesterday","reportFormat":"csv","language":"de"}`)
	var out GeneratedReport
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || out.IDReport != 2 || out.IDSite != 2 || out.ReportFormat != "csv" ||
		out.Content != "label;visits\nhome;4\n" || out.Truncated {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated ScheduledReports.getReports ScheduledReports.generateReport" {
		t.Errorf("requests = %s", got)
	}
	proof := lastCall(calls, "ScheduledReports.getReports")
	if proof.form.Get("idReport") != "2" || proof.form.Has("idSite") {
		t.Errorf("proving read = %v", proof.form)
	}
	sent := lastCall(calls, "ScheduledReports.generateReport")
	if sent.form.Get("outputType") != "4" || sent.form.Get("idReport") != "2" || sent.form.Get("date") != "yesterday" ||
		sent.form.Get("reportFormat") != "csv" || sent.form.Get("language") != "de" || sent.form.Has("period") ||
		sent.form.Has("parameters") || sent.form.Has("passwordConfirmation") || sent.form.Has("token_auth") ||
		sent.auth != "Bearer "+tokenValue || sent.rawQuery != "" || sent.form.Get("format") != "JSON" {
		t.Errorf("request = %+v", sent)
	}
	// A report whose stored period is a range-less one takes a single date; a range needs the period range.
	for _, format := range []string{"html", "tsv"} {
		if _, err := env.invoke("matomo.scheduled-reports.generate", "sr-tools",
			`{"idReport":1,"date":"2026-01-01,2026-01-31","period":"range","reportFormat":"`+format+`"}`); err != nil {
			t.Errorf("%s range: %v", format, err)
		}
		if sent = lastCall(calls, "ScheduledReports.generateReport"); sent.form.Get("outputType") != "4" ||
			sent.form.Get("period") != "range" || sent.form.Get("reportFormat") != format {
			t.Errorf("%s request = %v", format, sent.form)
		}
	}
}

func TestScheduledReportsGenerateCutsLongText(t *testing.T) {
	long, _ := json.Marshal(strings.Repeat("a", maxGeneratedReport+10))
	var calls []call
	env := newReportEnvironment(t, &calls, reportAnswers(answers{
		"ScheduledReports.generateReport": func(call) *http.Response { return ok(`{"value":` + string(long) + `}`) },
	}).handler(t))
	result, err := env.invoke("matomo.scheduled-reports.generate", "sr-tools", `{"idReport":1,"date":"today","reportFormat":"html"}`)
	var out GeneratedReport
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || !out.Truncated || len(out.Content) != maxGeneratedReport {
		t.Errorf("truncated = %t, length = %d, err = %v", out.Truncated, len(out.Content), err)
	}
}

func TestScheduledReportsGenerateRefusesBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newReportEnvironment(t, &calls, reportAnswers(nil).handler(t))
	for name, c := range map[string][2]string{
		"pdf":             {"sr-tools", `{"idReport":1,"date":"today","reportFormat":"pdf"}`},
		"rtf":             {"sr-tools", `{"idReport":1,"date":"today","reportFormat":"rtf"}`},
		"no format":       {"sr-tools", `{"idReport":1,"date":"today"}`},
		"outputType":      {"sr-tools", `{"idReport":1,"date":"today","reportFormat":"csv","outputType":2}`},
		"parameters":      {"sr-tools", `{"idReport":1,"date":"today","reportFormat":"csv","parameters":{}}`},
		"no ID":           {"sr-tools", `{"date":"today","reportFormat":"csv"}`},
		"bad date":        {"sr-tools", `{"idReport":1,"date":"last7","reportFormat":"csv"}`},
		"bad period":      {"sr-tools", `{"idReport":1,"date":"today","reportFormat":"csv","period":"never"}`},
		"bad language":    {"sr-tools", `{"idReport":1,"date":"today","reportFormat":"csv","language":"DE"}`},
		"range no period": {"sr-tools", `{"idReport":1,"date":"2026-01-01,2026-01-31","reportFormat":"csv","period":"day"}`},
		"instance only":   {"sr-instance", `{"idReport":1,"date":"today","reportFormat":"csv"}`},
		"idSite":          {"sr-tools", `{"idReport":1,"idSite":3,"date":"today","reportFormat":"csv"}`},
	} {
		if _, err := env.invoke("matomo.scheduled-reports.generate", c[0], c[1]); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestScheduledReportsForeignReportIsRefusedAfterTheProvingReadWithoutRendering(t *testing.T) {
	for _, tool := range []struct{ id, arguments string }{
		{"generate", `{"idReport":3,"date":"today","reportFormat":"csv"}`},
		{"delete", `{"idReport":3}`},
		{"generate", `{"idReport":99,"date":"today","reportFormat":"csv"}`},
		{"delete", `{"idReport":99}`},
		{"generate", `{"idReport":7,"date":"today","reportFormat":"csv"}`},
		{"delete", `{"idReport":7}`},
	} {
		for _, connection := range []string{"sr-tools", "sr-one"} {
			var calls []call
			env := newReportEnvironment(t, &calls, reportAnswers(nil).handler(t))
			result, err := env.change("matomo.scheduled-reports."+tool.id, connection, tool.arguments)
			if err == nil || result != "" || classOf(err) != provider.ClassNotFound || strings.Contains(err.Error(), "3") && strings.Contains(err.Error(), "site") ||
				countMethod(calls, "ScheduledReports.generateReport") != 0 || countMethod(calls, "ScheduledReports.deleteReport") != 0 ||
				countMethod(calls, "ScheduledReports.getReports") != 1 {
				t.Errorf("%s %s %s: err = %v, requests = %s", connection, tool.id, tool.arguments, err, methods(calls))
			}
		}
	}
	// A report of a bound website that only another connection reaches is refused as well.
	var calls []call
	env := newReportEnvironment(t, &calls, reportAnswers(nil).handler(t))
	if _, err := env.change("matomo.scheduled-reports.delete", "sr-one", `{"idReport":1}`); classOf(err) != provider.ClassNotFound ||
		countMethod(calls, "ScheduledReports.deleteReport") != 0 {
		t.Errorf("report of website 1 on a connection bound to website 2: err = %v", err)
	}
	// A different report than the one asked for is not trusted.
	env = newReportEnvironment(t, &calls, reportAnswers(nil).handler(t))
	if _, err := env.change("matomo.scheduled-reports.delete", "sr-tools", `{"idReport":8}`); classOf(err) != provider.ClassInvalidResponse ||
		countMethod(calls, "ScheduledReports.deleteReport") != 0 {
		t.Errorf("mismatched row: err = %v", err)
	}
}

func TestScheduledReportsGenerateRefusesPeriodMismatchAfterTheProvingReadWithoutRendering(t *testing.T) {
	var calls []call
	env := newReportEnvironment(t, &calls, reportAnswers(nil).handler(t))
	// The stored period of report 1 is week, so a date range needs period range.
	if _, err := env.invoke("matomo.scheduled-reports.generate", "sr-tools",
		`{"idReport":1,"date":"2026-01-01,2026-01-31","reportFormat":"csv"}`); !isInvalidRequest(err) ||
		countMethod(calls, "ScheduledReports.generateReport") != 0 {
		t.Errorf("err = %v, requests = %s", err, methods(calls))
	}
}

func TestScheduledReportsGenerateSendsOnceAndMapsFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		answer      func(call) (*http.Response, error)
		class       provider.Class
		unsupported bool
	}{
		"timeout":      {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false},
		"502":          {func(call) (*http.Response, error) { return respond(http.StatusBadGateway, ""), nil }, "", false},
		"unreadable":   {func(call) (*http.Response, error) { return ok("<html>" + bodyCanary + "</html>"), nil }, provider.ClassInvalidResponse, false},
		"no text":      {func(call) (*http.Response, error) { return ok(`{"value":""}`), nil }, provider.ClassInvalidResponse, false},
		"result error": {func(call) (*http.Response, error) { return ok2(failure("Invalid format " + bodyCanary)) }, provider.ClassInvalidResponse, false},
		"permission":   {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false},
		"not found":    {func(call) (*http.Response, error) { return ok2(failure("Website not found " + bodyCanary)) }, provider.ClassNotFound, false},
		"plugin inactive": {func(call) (*http.Response, error) {
			return ok2(failure("The plugin ScheduledReports is not activated."))
		}, "", true},
		"429": {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, ""), nil }, provider.ClassRateLimited, false},
	} {
		var calls []call
		env := newReportEnvironment(t, &calls, func(c call) (*http.Response, error) {
			if c.api() == "ScheduledReports.generateReport" {
				return tc.answer(c)
			}
			return reportAnswers(nil).handler(t)(c)
		})
		_, err := env.invoke("matomo.scheduled-reports.generate", "sr-tools", `{"idReport":1,"date":"today","reportFormat":"csv"}`)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || tc.unsupported != isUnsupported(err) ||
			tc.class != "" && classOf(err) != tc.class || countMethod(calls, "ScheduledReports.generateReport") != 1 {
			t.Errorf("%s: err = %v, sent %d times", name, err, countMethod(calls, "ScheduledReports.generateReport"))
		}
	}
}

func TestScheduledReportsDeleteReadsOnceThenDeletesOnce(t *testing.T) {
	var calls []call
	env := newReportEnvironment(t, &calls, reportAnswers(nil).handler(t))
	if _, err := env.invoke("matomo.scheduled-reports.delete", "sr-tools", `{"idReport":2}`); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("without confirm: err = %v", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("a refusal caused %d requests and %d secret reads", len(calls), *env.reads)
	}
	if _, err := env.change("matomo.scheduled-reports.delete", "sr-effects", `{"idReport":2}`); err == nil {
		t.Fatal("a connection with an effects list offered scheduled-reports.delete")
	}
	if len(calls) != 0 {
		t.Fatalf("an unlisted connection caused %d requests", len(calls))
	}
	result, err := env.change("matomo.scheduled-reports.delete", "sr-tools", `{"idReport":2}`)
	if err != nil || result != `{"deleted":true,"idReport":2,"idSite":2}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated ScheduledReports.getReports ScheduledReports.deleteReport" {
		t.Errorf("requests = %s", got)
	}
	sent := calls[len(calls)-1]
	if sent.form.Get("idReport") != "2" || sent.form.Has("passwordConfirmation") || sent.auth != "Bearer "+tokenValue ||
		sent.rawQuery != "" || sent.form.Has("token_auth") {
		t.Errorf("request = %+v", sent)
	}
	before := len(calls)
	for _, arguments := range []string{`{}`, `{"idReport":0}`, `{"idReport":"2"}`, `{"idReport":2,"idSite":2}`} {
		if _, err := env.change("matomo.scheduled-reports.delete", "sr-tools", arguments); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
	if _, err := env.change("matomo.scheduled-reports.delete", "sr-instance", `{"idReport":2}`); err == nil {
		t.Error("an instance-only connection deleted a report")
	}
	if len(calls) != before {
		t.Errorf("%d requests before the refusal", len(calls)-before)
	}
}

func TestScheduledReportsDeleteIsSentOnceAndMapsErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		answer      func(call) (*http.Response, error)
		class       provider.Class
		unsupported bool
		unclear     bool
	}{
		"timeout":         {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false, true},
		"502":             {func(call) (*http.Response, error) { return respond(http.StatusBadGateway, ""), nil }, "", false, true},
		"unreadable":      {func(call) (*http.Response, error) { return ok("<html>" + bodyCanary + "</html>"), nil }, provider.ClassInvalidResponse, false, true},
		"no confirmation": {func(call) (*http.Response, error) { return ok(`[]`), nil }, provider.ClassInvalidResponse, false, true},
		"permission":      {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false, false},
		"not found":       {func(call) (*http.Response, error) { return ok2(failure("Requested report couldn't be found.")) }, provider.ClassNotFound, false, false},
		"plugin inactive": {func(call) (*http.Response, error) {
			return ok2(failure("The plugin ScheduledReports is not activated."))
		}, "", true, false},
		"429": {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, ""), nil }, provider.ClassRateLimited, false, false},
	} {
		var calls []call
		env := newReportEnvironment(t, &calls, func(c call) (*http.Response, error) {
			if c.api() == "ScheduledReports.deleteReport" {
				return tc.answer(c)
			}
			return reportAnswers(nil).handler(t)(c)
		})
		_, err := env.change("matomo.scheduled-reports.delete", "sr-tools", `{"idReport":1}`)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || tc.unsupported != isUnsupported(err) ||
			tc.class != "" && classOf(err) != tc.class || countMethod(calls, "ScheduledReports.deleteReport") != 1 {
			t.Errorf("%s: err = %v, sent %d times", name, err, countMethod(calls, "ScheduledReports.deleteReport"))
			continue
		}
		if tc.unclear && !strings.Contains(err.Error(), "never repeated") {
			t.Errorf("%s: err = %v does not say that the outcome is unclear", name, err)
		}
	}
}
