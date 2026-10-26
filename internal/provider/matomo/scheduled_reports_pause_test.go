package matomo

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const fullParameters = `{"displayFormat":"1","emailMe":true,"evolutionGraph":false,"enforceOrder":false,` +
	`"additionalEmails":["x@example.com","x@example.org"]}`

// pauseRow builds a getReports row; a member set to "-" is left out.
func pauseRow(overrides map[string]string) string {
	members := map[string]string{
		"idreport": `"1"`, "idsite": `"1"`, "login": `"admin"`, "description": `"Weekly one"`, "idsegment": `null`,
		"period": `"week"`, "period_param": `"week"`, "hour": `"6"`, "type": `"email"`, "format": `"html"`,
		"reports": `["VisitsSummary_get","Actions_getPageUrls"]`, "parameters": fullParameters,
		"ts_created": `"2026-01-02 03:04:05"`, "ts_last_sent": `null`, "deleted": `"0"`,
		"evolution_graph_within_period": `"0"`, "evolution_graph_period_n": `"30"`,
	}
	for k, v := range overrides {
		members[k] = v
	}
	var parts []string
	for k, v := range members {
		if v != "-" {
			parts = append(parts, `"`+k+`":`+v)
		}
	}
	sort.Strings(parts)
	return "{" + strings.Join(parts, ",") + "}"
}

func pauseAnswers(rows func(call) string, update func(call) (*http.Response, error)) func(call) (*http.Response, error) {
	return func(c call) (*http.Response, error) {
		if c.api() == "ScheduledReports.updateReport" {
			if update != nil {
				return update(c)
			}
			return ok(`{"result":"success","message":"ok"}`), nil
		}
		if c.api() == "ScheduledReports.getReports" {
			return ok(rows(c)), nil
		}
		return reportAnswers(nil)[c.api()](c), nil
	}
}

func oneRow(row string) func(call) string { return func(call) string { return "[" + row + "]" } }

func sorted(values []string) []string {
	out := append([]string{}, values...)
	sort.Strings(out)
	return out
}

func TestScheduledReportsPauseSendsEveryValueUnchangedWithOnlyTheScheduleSetToNever(t *testing.T) {
	for name, tc := range map[string]struct {
		overrides map[string]string
		check     func(*testing.T, call)
	}{
		"plain": {nil, func(t *testing.T, c call) {
			if c.form.Get("description") != "Weekly one" || c.form.Get("hour") != "6" || c.form.Get("reportType") != "email" ||
				c.form.Get("reportFormat") != "html" || c.form.Get("periodParam") != "week" || c.form.Has("idSegment") ||
				c.form.Get("evolutionPeriodFor") != "prev" || c.form.Get("evolutionPeriodN") != "30" ||
				strings.Join(c.form["reports[]"], ",") != "VisitsSummary_get,Actions_getPageUrls" ||
				c.form.Get("parameters[displayFormat]") != "1" || c.form.Get("parameters[emailMe]") != "1" ||
				c.form.Get("parameters[evolutionGraph]") != "0" || c.form.Get("parameters[enforceOrder]") != "0" ||
				strings.Join(c.form["parameters[additionalEmails][]"], ",") != "x@example.com,x@example.org" {
				t.Errorf("form = %v", c.form)
			}
		}},
		"numbers, segment, month": {map[string]string{"idreport": `1`, "idsite": `1`, "idsegment": `"5"`, "hour": `0`,
			"period": `"month"`, "period_param": `"year"`, "format": `"csv"`, "evolution_graph_period_n": `12`,
			"evolution_graph_within_period": `0`}, func(t *testing.T, c call) {
			if c.form.Get("idSegment") != "5" || c.form.Get("hour") != "0" || c.form.Get("periodParam") != "year" ||
				c.form.Get("reportFormat") != "csv" || c.form.Get("evolutionPeriodN") != "12" {
				t.Errorf("form = %v", c.form)
			}
		}},
		"each day of the period": {map[string]string{"evolution_graph_within_period": `"1"`}, func(t *testing.T, c call) {
			if c.form.Get("evolutionPeriodFor") != "each" || c.form.Has("evolutionPeriodN") {
				t.Errorf("form = %v", c.form)
			}
		}},
		"no additional emails, flags set, description": {map[string]string{"parameters": `{"displayFormat":"3",` +
			`"emailMe":false,"evolutionGraph":true,"enforceOrder":true,"reportDescription":"Weekly numbers"}`},
			func(t *testing.T, c call) {
				if c.form.Has("parameters[additionalEmails][]") || c.form.Get("parameters[emailMe]") != "0" ||
					c.form.Get("parameters[evolutionGraph]") != "1" || c.form.Get("parameters[enforceOrder]") != "1" ||
					c.form.Get("parameters[reportDescription]") != "Weekly numbers" || c.form.Get("parameters[displayFormat]") != "3" {
					t.Errorf("form = %v", c.form)
				}
			}},
		"description with quotes and umlauts": {map[string]string{"description": `"Bericht \"Q1\" für Büro & Co"`},
			func(t *testing.T, c call) {
				if c.form.Get("description") != `Bericht "Q1" für Büro & Co` {
					t.Errorf("description = %q", c.form.Get("description"))
				}
			}},
	} {
		var calls []call
		env := newReportEnvironment(t, &calls, pauseAnswers(oneRow(pauseRow(tc.overrides)), nil))
		result, err := env.change("matomo.scheduled-reports.pause", "sr-tools", `{"idSite":1,"idReport":1}`)
		if err != nil || result != `{"alreadyPaused":false,"idReport":1,"idSite":1,"paused":true}` {
			t.Errorf("%s: result = %s, err = %v", name, result, err)
			continue
		}
		if got := methods(calls); got != "API.getMatomoVersion API.isPluginActivated ScheduledReports.getReports ScheduledReports.updateReport" {
			t.Errorf("%s: requests = %s", name, got)
		}
		sent := calls[len(calls)-1]
		if sent.form.Get("period") != "never" || sent.form.Get("idReport") != "1" || sent.form.Get("idSite") != "1" ||
			sent.auth != "Bearer "+tokenValue || sent.rawQuery != "" || sent.form.Has("token_auth") ||
			sent.form.Has("passwordConfirmation") || sent.form.Has("callback") || sent.form.Get("format") != "JSON" {
			t.Errorf("%s: request = %+v", name, sent)
		}
		allowed := []string{"idReport", "idSite", "description", "period", "hour", "reportType", "reportFormat", "reports[]",
			"periodParam", "evolutionPeriodFor", "evolutionPeriodN", "idSegment", "module", "method", "format",
			"parameters[displayFormat]", "parameters[emailMe]", "parameters[evolutionGraph]", "parameters[enforceOrder]",
			"parameters[additionalEmails][]", "parameters[reportDescription]"}
		for key := range sent.form {
			if !contains(allowed, key) {
				t.Errorf("%s: unexpected parameter %s", name, key)
			}
		}
		tc.check(t, sent)
	}
}

func contains(list []string, item string) bool {
	for _, entry := range list {
		if entry == item {
			return true
		}
	}
	return false
}

func TestScheduledReportsPauseRefusesAnyValueItCannotHandBackUnchangedBeforeWriting(t *testing.T) {
	long := `"` + strings.Repeat("x", 251) + `"`
	for name, overrides := range map[string]map[string]string{
		"enforceOrder absent":     {"parameters": `{"displayFormat":"1","emailMe":true,"evolutionGraph":false}`},
		"emailMe absent":          {"parameters": `{"displayFormat":"1","evolutionGraph":false,"enforceOrder":false}`},
		"emailMe as text":         {"parameters": `{"displayFormat":"1","emailMe":"1","evolutionGraph":false,"enforceOrder":false}`},
		"displayFormat number":    {"parameters": `{"displayFormat":1,"emailMe":true,"evolutionGraph":false,"enforceOrder":false}`},
		"displayFormat absent":    {"parameters": `{"emailMe":true,"evolutionGraph":false,"enforceOrder":false}`},
		"empty email list":        {"parameters": `{"displayFormat":"1","emailMe":true,"evolutionGraph":false,"enforceOrder":false,"additionalEmails":[]}`},
		"untrimmed email":         {"parameters": `{"displayFormat":"1","emailMe":true,"evolutionGraph":false,"enforceOrder":false,"additionalEmails":[" x@example.com"]}`},
		"duplicate email":         {"parameters": `{"displayFormat":"1","emailMe":true,"evolutionGraph":false,"enforceOrder":false,"additionalEmails":["x@example.com","x@example.com"]}`},
		"email not text":          {"parameters": `{"displayFormat":"1","emailMe":true,"evolutionGraph":false,"enforceOrder":false,"additionalEmails":[7]}`},
		"unknown parameter":       {"parameters": `{"displayFormat":"1","emailMe":true,"evolutionGraph":false,"enforceOrder":false,"extra":"x"}`},
		"untrimmed description 2": {"parameters": `{"displayFormat":"1","emailMe":true,"evolutionGraph":false,"enforceOrder":false,"reportDescription":" x "}`},
		"parameters as list":      {"parameters": `[]`},
		"parameters null":         {"parameters": `null`},
		"mobile":                  {"type": `"mobile"`, "format": `"sms"`, "parameters": `{"phoneNumbers":["` + "+" + `"]}`},
		"unknown row member":      {"future_column": `"x"`},
		"long description":        {"description": long},
		"empty description":       {"description": `""`},
		"description not text":    {"description": `5`},
		"no reports":              {"reports": `[]`},
		"reports not texts":       {"reports": `[1]`},
		"reports absent":          {"reports": `-`},
		"hour 24":                 {"hour": `"24"`},
		"hour text":               {"hour": `"six"`},
		"hour padded":             {"hour": `"06"`},
		"period param range":      {"period_param": `"range"`},
		"period param null":       {"period_param": `null`},
		"segment text":            {"idsegment": `"x"`},
		"segment zero":            {"idsegment": `"0"`},
		"graph n zero":            {"evolution_graph_period_n": `"0"`},
		"graph within other":      {"evolution_graph_within_period": `"2"`},
		"period other":            {"period": `"year"`},
		"format absent":           {"format": `-`},
		"format number":           {"format": `1`},
	} {
		var calls []call
		env := newReportEnvironment(t, &calls, pauseAnswers(oneRow(pauseRow(overrides)), nil))
		result, err := env.change("matomo.scheduled-reports.pause", "sr-tools", `{"idSite":1,"idReport":1}`)
		if err == nil || result != "" || classOf(err) != provider.ClassInvalidResponse ||
			countMethod(calls, "ScheduledReports.updateReport") != 0 || countMethod(calls, "ScheduledReports.getReports") != 1 {
			t.Errorf("%s: result = %q, err = %v, requests = %s", name, result, err, methods(calls))
		}
	}
}

func TestScheduledReportsPauseDoesNotWriteAPausedReport(t *testing.T) {
	// An odd shape does not matter: nothing is written.
	for _, row := range []string{pauseRow(map[string]string{"period": `"never"`, "period_param": `"day"`}),
		pauseRow(map[string]string{"period": `"never"`, "parameters": `[]`})} {
		var calls []call
		env := newReportEnvironment(t, &calls, pauseAnswers(oneRow(row), nil))
		result, err := env.change("matomo.scheduled-reports.pause", "sr-tools", `{"idSite":1,"idReport":1}`)
		if err != nil || result != `{"alreadyPaused":true,"idReport":1,"idSite":1,"paused":true}` ||
			countMethod(calls, "ScheduledReports.updateReport") != 0 || countMethod(calls, "ScheduledReports.getReports") != 1 {
			t.Errorf("result = %s, err = %v, requests = %s", result, err, methods(calls))
		}
	}
}

func TestScheduledReportsPauseBindsTheReportToTheWebsite(t *testing.T) {
	rows := func(c call) string {
		switch c.form.Get("idReport") {
		case "1":
			return "[" + pauseRow(nil) + "]"
		case "2":
			return "[" + pauseRow(map[string]string{"idreport": `"2"`, "idsite": `"2"`}) + "]"
		case "3":
			return "[" + pauseRow(map[string]string{"idreport": `"3"`, "idsite": `"3"`}) + "]"
		case "8":
			return "[" + pauseRow(map[string]string{"idreport": `"2"`}) + "]"
		}
		return "[]"
	}
	for name, tc := range map[string]struct{ connection, arguments string }{
		"foreign report":            {"sr-tools", `{"idSite":1,"idReport":3}`},
		"foreign report on site 3":  {"sr-tools", `{"idSite":2,"idReport":3}`},
		"report of another website": {"sr-tools", `{"idSite":1,"idReport":2}`},
		"report of site 1 on one":   {"sr-one", `{"idReport":1}`},
		"unknown report":            {"sr-tools", `{"idSite":1,"idReport":7}`},
	} {
		var calls []call
		env := newReportEnvironment(t, &calls, pauseAnswers(rows, nil))
		result, err := env.change("matomo.scheduled-reports.pause", tc.connection, tc.arguments)
		if err == nil || result != "" || classOf(err) != provider.ClassNotFound || strings.Contains(err.Error(), "site 3") ||
			countMethod(calls, "ScheduledReports.updateReport") != 0 || countMethod(calls, "ScheduledReports.getReports") != 1 {
			t.Errorf("%s: err = %v, requests = %s", name, err, methods(calls))
		}
	}
	var calls []call
	env := newReportEnvironment(t, &calls, pauseAnswers(rows, nil))
	if _, err := env.change("matomo.scheduled-reports.pause", "sr-tools", `{"idSite":1,"idReport":8}`); classOf(err) != provider.ClassInvalidResponse ||
		countMethod(calls, "ScheduledReports.updateReport") != 0 {
		t.Errorf("mismatched row: err = %v", err)
	}
	// The binding of the website comes from the connection when it binds exactly one.
	calls = nil
	env = newReportEnvironment(t, &calls, pauseAnswers(rows, nil))
	if result, err := env.change("matomo.scheduled-reports.pause", "sr-one", `{"idReport":2}`); err != nil ||
		result != `{"alreadyPaused":false,"idReport":2,"idSite":2,"paused":true}` {
		t.Errorf("result = %s, err = %v", result, err)
	}
}

func TestScheduledReportsPauseNeedsConfirmationAndRefusesBadArgumentsBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newReportEnvironment(t, &calls, pauseAnswers(oneRow(pauseRow(nil)), nil))
	if _, err := env.invoke("matomo.scheduled-reports.pause", "sr-tools", `{"idSite":1,"idReport":1}`); err == nil ||
		!strings.Contains(err.Error(), "confirm") {
		t.Fatalf("without confirm: err = %v", err)
	}
	if _, err := env.change("matomo.scheduled-reports.pause", "sr-effects", `{"idSite":1,"idReport":1}`); err == nil {
		t.Fatal("a connection without the effect update offered scheduled-reports.pause")
	}
	for _, arguments := range []string{`{}`, `{"idSite":1}`, `{"idSite":1,"idReport":0}`, `{"idSite":1,"idReport":"1"}`,
		`{"idSite":3,"idReport":1}`, `{"idSite":0,"idReport":1}`, `{"idReport":1}`, `{"idSite":1,"idReport":1,"period":"day"}`,
		`{"idSite":1,"idReport":1,"parameters":{}}`, `{"idSite":1,"idReport":1,"additionalEmails":["x@example.com"]}`,
		`{"idSite":1,"idReport":1,"callback":"x"}`} {
		if _, err := env.change("matomo.scheduled-reports.pause", "sr-tools", arguments); err == nil {
			t.Errorf("%s was accepted", arguments)
		}
	}
	if _, err := env.change("matomo.scheduled-reports.pause", "sr-instance", `{"idSite":1,"idReport":1}`); err == nil {
		t.Error("an instance-only connection paused a report")
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("refusals caused %d requests and %d secret reads", len(calls), *env.reads)
	}
	// The effect update offers the tool without a tools list.
	if _, err := env.change("matomo.scheduled-reports.pause", "sr-update", `{"idSite":1,"idReport":1}`); err != nil {
		t.Errorf("effects update: %v", err)
	}
}

func TestScheduledReportsPauseIsSentOnceAndMapsErrors(t *testing.T) {
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
		"result error":    {func(call) (*http.Response, error) { return ok2(failure("Invalid segment " + bodyCanary)) }, provider.ClassInvalidResponse, false, false},
		"permission":      {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false, false},
		"not found":       {func(call) (*http.Response, error) { return ok2(failure("Requested report couldn't be found.")) }, provider.ClassNotFound, false, false},
		"plugin inactive": {func(call) (*http.Response, error) {
			return ok2(failure("The plugin ScheduledReports is not activated."))
		}, "", true, false},
		"429": {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, ""), nil }, provider.ClassRateLimited, false, false},
	} {
		var calls []call
		env := newReportEnvironment(t, &calls, pauseAnswers(oneRow(pauseRow(nil)), tc.answer))
		_, err := env.change("matomo.scheduled-reports.pause", "sr-tools", `{"idSite":1,"idReport":1}`)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || tc.unsupported != isUnsupported(err) ||
			tc.class != "" && classOf(err) != tc.class || countMethod(calls, "ScheduledReports.updateReport") != 1 {
			t.Errorf("%s: err = %v, sent %d times", name, err, countMethod(calls, "ScheduledReports.updateReport"))
			continue
		}
		if tc.unclear && !strings.Contains(err.Error(), "never repeated") {
			t.Errorf("%s: err = %v does not say that the outcome is unclear", name, err)
		}
	}
}
