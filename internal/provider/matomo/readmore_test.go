package matomo

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// The fixtures follow the return shapes of the Insights, Transitions, BotTracking, and DBStats APIs of
// Matomo 5.14: tables as lists of row objects, an overview as an object of tables, and transitions as an
// object of lists.

func insightRows(n int) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = `{"label":"/page-` + strconv.Itoa(i) + `","nb_visits":` + strconv.Itoa(100+i) + `,"nb_visits_previous":50,` +
			`"difference":` + strconv.Itoa(50+i) + `,"growth_percent":"100%","growth_percent_numeric":100,"isNew":false,` +
			`"isMoverAndShaker":true}`
	}
	return "[" + strings.Join(rows, ",") + "]"
}

const transitionsAnswer = `{"date":"Oct 2026","previousPages":[{"label":"/home","referrals":40},{"label":"/shop","referrals":20},` +
	`{"label":"/blog","referrals":5}],"previousSiteSearches":[{"label":"shoes","referrals":3}],` +
	`"pageMetrics":{"pageviews":100,"loops":7,"entries":55,"exits":20},"followingPages":[{"label":"/checkout","referrals":60}],` +
	`"followingSiteSearches":[],"outlinks":[{"label":"https://partner.example.test/","referrals":2}],"downloads":[],` +
	`"referrers":[{"label":"Direct entries","shortName":"direct","visits":30,"details":[]},` +
	`{"label":"From search engines","shortName":"search","visits":25,"details":[{"label":"Example","referrals":25}]}]}`

const botChatbots = `[{"label":"ChatGPT","requests":"12","BotTracking_AIChatbotsUniquePageUrls":"5",` +
	`"BotTracking_AIChatbotsNotFoundRequests":"1","BotTracking_AIChatbotsServerErrorRequests":"0"},` +
	`{"label":"Claude","requests":"7","BotTracking_AIChatbotsUniquePageUrls":"3",` +
	`"BotTracking_AIChatbotsNotFoundRequests":"0","BotTracking_AIChatbotsServerErrorRequests":"2"}]`

const trackerTables = `[{"label":"matomo_log_visit","data_size":"16384","index_size":"32768","row_count":"120"},` +
	`{"label":"matomo_log_action","data_size":"8192","index_size":"0","row_count":"40"},` +
	`{"label":"matomo_log_link_visit_action","data_size":"4096","index_size":"1024","row_count":"9"}]`

const readMoreConnections = `
  rm-sites: {service: matomo, credential: token, tools: [matomo.insights.list, matomo.transitions.get, matomo.bot-realtime.get], targets: [site]}
  rm-one: {service: matomo, credential: token, tools: [matomo.insights.list, matomo.transitions.get, matomo.bot-realtime.get], targets: [site/2]}
  rm-instance-only: {service: matomo, credential: token, tools: [matomo.insights.list, matomo.transitions.get, matomo.bot-realtime.get], targets: [instance]}
  rm-db: {service: matomo, credential: token, tools: [matomo.database-statistics.get], targets: [instance]}
  rm-db-mixed: {service: matomo, credential: token, tools: [matomo.database-statistics.get], targets: [site/2, instance]}
  rm-db-sites: {service: matomo, credential: token, tools: [matomo.database-statistics.get], targets: [site]}
  rm-db-one: {service: matomo, credential: token, tools: [matomo.database-statistics.get], targets: [site/2]}
  rm-db-two: {service: matomo, credential: token, tools: [matomo.database-statistics.get], targets: [site/1, site/2]}
`

// newReadMoreEnvironment adds the connections that name the listed-only reads to the connections of the other tests.
func newReadMoreEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(readMoreConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func readMoreAnswers(extra answers) answers { return with(pluginsOn(), extra) }

func decodeObject(t *testing.T, text string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("result %q: %v", text, err)
	}
	return out
}

func lastCall(calls []call) call { return calls[len(calls)-1] }

func TestInsightsListSendsTheFixedMethodPerKind(t *testing.T) {
	var calls []call
	env := newReadMoreEnvironment(t, &calls, readMoreAnswers(answers{
		"Insights.getInsights":         func(call) *http.Response { return ok(insightRows(2)) },
		"Insights.getMoversAndShakers": func(call) *http.Response { return ok(insightRows(1)) },
		"Insights.getInsightsOverview": func(call) *http.Response {
			return ok(`{"Pages":` + insightRows(1) + `,"Downloads":` + insightRows(1) + `}`)
		},
		"Insights.getMoversAndShakersOverview": func(call) *http.Response { return ok(`[]`) },
		"Insights.canGenerateInsights":         func(call) *http.Response { return ok(`{"value":true}`) },
	}).handler(t))
	base := `"period":"week","date":"yesterday"`
	for _, tc := range []struct {
		arguments, method string
		form              map[string]string
		rows              int
	}{
		{`{"kind":"insights","idSite":1,` + base + `,"report":"Actions_getPageUrls","segment":"browserName==Firefox",` +
			`"compared_to_periods":2,"limit_increaser":3,"limit_decreaser":0,"filter_by":"new","min_impact_percent":5,` +
			`"min_growth_percent":30,"order_by":"relative"}`, "Insights.getInsights",
			map[string]string{"idSite": "1", "period": "week", "date": "yesterday", "reportUniqueId": "Actions_getPageUrls",
				"segment": "browserName==Firefox", "comparedToXPeriods": "2", "limitIncreaser": "3", "limitDecreaser": "0",
				"filterBy": "new", "minImpactPercent": "5", "minGrowthPercent": "30", "orderBy": "relative"}, 2},
		{`{"kind":"movers-and-shakers","idSite":1,` + base + `,"report":"Actions_getPageUrls"}`, "Insights.getMoversAndShakers",
			map[string]string{"idSite": "1", "reportUniqueId": "Actions_getPageUrls"}, 1},
		{`{"kind":"insights-overview","idSite":1,` + base + `}`, "Insights.getInsightsOverview", map[string]string{"idSite": "1"}, 2},
		{`{"kind":"movers-and-shakers-overview","idSite":1,` + base + `}`, "Insights.getMoversAndShakersOverview", map[string]string{"idSite": "1"}, 0},
	} {
		calls = nil
		text, err := env.invoke("matomo.insights.list", "rm-sites", tc.arguments)
		if err != nil {
			t.Fatalf("%s: %v", tc.method, err)
		}
		got := lastCall(calls)
		if got.api() != tc.method || got.auth != "Bearer "+tokenValue || got.form.Get("format") != "JSON" || got.form.Has("token_auth") {
			t.Errorf("%s: request = %+v", tc.method, got)
		}
		for name, want := range tc.form {
			if got.form.Get(name) != want {
				t.Errorf("%s: %s = %q, want %q", tc.method, name, got.form.Get(name), want)
			}
		}
		result := decodeObject(t, text)
		rows, _ := result["rows"].([]any)
		if len(rows) != tc.rows || result["idSite"] != float64(1) {
			t.Errorf("%s: result = %s", tc.method, text)
		}
		if strings.HasSuffix(tc.method, "Overview") && tc.rows > 0 &&
			!strings.Contains(text, `"report_name":"Pages"`) || strings.Contains(text, `"has_more":true`) {
			t.Errorf("%s: result = %s", tc.method, text)
		}
	}
	calls = nil
	text, err := env.invoke("matomo.insights.list", "rm-instance-only", `{"kind":"can-generate",`+base+`}`)
	if err == nil {
		t.Fatalf("an instance-only connection read insights: %s", text)
	}
	text, err = env.invoke("matomo.insights.list", "rm-sites", `{"kind":"can-generate",`+base+`}`)
	if err != nil || !strings.Contains(text, `"can_generate":true`) || strings.Contains(text, `"idSite"`) {
		t.Fatalf("can-generate: %s, %v", text, err)
	}
	if got := lastCall(calls); got.form.Has("idSite") || got.api() != "Insights.canGenerateInsights" {
		t.Errorf("can-generate request = %+v", got)
	}
}

func TestInsightsListPagesTheRowsAndBindsTheCursor(t *testing.T) {
	var calls []call
	env := newReadMoreEnvironment(t, &calls, readMoreAnswers(answers{
		"Insights.getInsights": func(call) *http.Response { return ok(insightRows(5)) },
	}).handler(t))
	args := `"kind":"insights","idSite":1,"period":"day","date":"today","report":"Actions_getPageUrls"`
	var seen []string
	cursor := ""
	for page := 0; page < 3; page++ {
		extra := `"limit":2`
		if cursor != "" {
			extra = `"cursor":"` + cursor + `"`
		}
		text, err := env.invoke("matomo.insights.list", "rm-sites", `{`+args+`,`+extra+`}`)
		if err != nil {
			t.Fatal(err)
		}
		result := decodeObject(t, text)
		for _, one := range result["rows"].([]any) {
			seen = append(seen, one.(map[string]any)["label"].(string))
		}
		cursor, _ = result["next_cursor"].(string)
		if (cursor != "") != (result["has_more"] == true) {
			t.Fatalf("page %d: %s", page, text)
		}
	}
	if len(seen) != 5 || seen[0] != "/page-0" || seen[4] != "/page-4" || cursor != "" {
		t.Fatalf("rows = %v, cursor = %q", seen, cursor)
	}
	text, _ := env.invoke("matomo.insights.list", "rm-sites", `{`+args+`,"limit":2}`)
	cursor, _ = decodeObject(t, text)["next_cursor"].(string)
	for name, arguments := range map[string]string{
		"other limit":      `{` + args + `,"cursor":"` + cursor + `","limit":3}`,
		"other report":     `{"kind":"insights","idSite":1,"period":"day","date":"today","report":"Actions_getEntryPageUrls","cursor":"` + cursor + `"}`,
		"other site":       `{"kind":"insights","idSite":2,"period":"day","date":"today","report":"Actions_getPageUrls","cursor":"` + cursor + `"}`,
		"manipulated":      `{` + args + `,"cursor":"` + cursor[:len(cursor)-2] + `AA"}`,
		"over the maximum": `{` + args + `,"limit":1001}`,
	} {
		if _, err := env.invoke("matomo.insights.list", "rm-sites", arguments); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestInsightsListRefusesBadArgumentsBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newReadMoreEnvironment(t, &calls, readMoreAnswers(nil).handler(t))
	ok := `"idSite":1,"period":"day","date":"today","report":"Actions_getPageUrls"`
	for connection, arguments := range map[string]string{
		"rm-one":           `{"kind":"insights","idSite":1,"period":"day","date":"today","report":"Actions_getPageUrls"}`,
		"rm-instance-only": `{"kind":"insights",` + ok + `}`,
	} {
		_, err := env.invoke("matomo.insights.list", connection, arguments)
		if err == nil || strings.Contains(err.Error(), "site/") {
			t.Errorf("%s: err = %v", connection, err)
		}
	}
	for _, arguments := range []string{
		`{"kind":"nope",` + ok + `}`, `{"kind":"insights","idSite":1,"period":"day","date":"today"}`,
		`{"kind":"insights-overview","idSite":1,"period":"day","date":"today","report":"Actions_getPageUrls"}`,
		`{"kind":"insights","idSite":1,"period":"day","date":"last7","report":"Actions_getPageUrls"}`,
		`{"kind":"insights","idSite":1,"period":"day","date":"2026-10-01,2026-10-07","report":"Actions_getPageUrls"}`,
		`{"kind":"insights","idSite":1,"period":"day","date":"today","report":"a b"}`,
		`{"kind":"movers-and-shakers",` + ok + `,"filter_by":"new"}`,
		`{"kind":"movers-and-shakers",` + ok + `,"order_by":"relative"}`,
		`{"kind":"insights-overview","idSite":1,"period":"day","date":"today","limit_increaser":2}`,
		`{"kind":"insights",` + ok + `,"filter_by":"other"}`, `{"kind":"insights",` + ok + `,"order_by":"other"}`,
		`{"kind":"insights",` + ok + `,"limit_increaser":101}`, `{"kind":"insights",` + ok + `,"compared_to_periods":0}`,
		`{"kind":"insights",` + ok + `,"segment":"nonsense"}`, `{"kind":"insights",` + ok + `,"module":"API"}`,
		`{"kind":"can-generate","idSite":1,"period":"day","date":"today"}`,
		`{"kind":"can-generate","period":"day","date":"today","segment":"browserName==Firefox"}`,
		`{"kind":"insights",` + ok + `,"idSite":0}`,
	} {
		if _, err := env.invoke("matomo.insights.list", "rm-sites", arguments); err == nil {
			t.Errorf("accepted %s", arguments)
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("secret reads = %d, requests = %d, want none", *env.reads, len(calls))
	}
}

func TestTransitionsGetSendsTheFixedMethodAndPagesEachList(t *testing.T) {
	var calls []call
	env := newReadMoreEnvironment(t, &calls, readMoreAnswers(answers{
		"Transitions.getTransitionsForPageUrl":   func(call) *http.Response { return ok(transitionsAnswer) },
		"Transitions.getTransitionsForPageTitle": func(call) *http.Response { return ok(transitionsAnswer) },
		"Transitions.getTransitionsForAction":    func(call) *http.Response { return ok(transitionsAnswer) },
	}).handler(t))
	base := `"idSite":1,"period":"month","date":"2026-09-01"`
	text, err := env.invoke("matomo.transitions.get", "rm-sites",
		`{`+base+`,"page_type":"url","page":"https://shop.example.test/cart","segment":"browserName==Firefox","limit_before_grouping":10,"limit":2}`)
	if err != nil {
		t.Fatal(err)
	}
	got := lastCall(calls)
	if got.api() != "Transitions.getTransitionsForPageUrl" || got.form.Get("pageUrl") != "https://shop.example.test/cart" ||
		got.form.Get("idSite") != "1" || got.form.Get("period") != "month" || got.form.Get("date") != "2026-09-01" ||
		got.form.Get("segment") != "browserName==Firefox" || got.form.Get("limitBeforeGrouping") != "10" ||
		got.auth != "Bearer "+tokenValue || got.form.Has("token_auth") || strings.Contains(got.rawQuery, "cart") {
		t.Fatalf("request = %+v", got)
	}
	result := decodeObject(t, text)
	previous := result["previous_pages"].([]any)
	if len(previous) != 2 || result["has_more"] != true || result["next_cursor"] == "" ||
		result["report_date"] != "Oct 2026" || len(result["referrers"].([]any)) != 2 {
		t.Fatalf("result = %s", text)
	}
	metrics := result["page_metrics"].(map[string]any)
	if metrics["pageviews"] != float64(100) || metrics["exits"] != float64(20) {
		t.Errorf("page_metrics = %v", metrics)
	}
	if !strings.Contains(text, `"short_name":"search"`) || !strings.Contains(text, `"details":[{"label":"Example","referrals":25}]`) {
		t.Errorf("referrers = %s", text)
	}
	text, err = env.invoke("matomo.transitions.get", "rm-sites",
		`{`+base+`,"page_type":"url","page":"https://shop.example.test/cart","segment":"browserName==Firefox","limit_before_grouping":10,"cursor":"`+
			result["next_cursor"].(string)+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	next := decodeObject(t, text)
	if rows := next["previous_pages"].([]any); len(rows) != 1 || rows[0].(map[string]any)["label"] != "/blog" || next["has_more"] == true {
		t.Errorf("second batch = %s", text)
	}
	if _, err := env.invoke("matomo.transitions.get", "rm-sites",
		`{`+base+`,"page_type":"title","page":"Cart","cursor":"`+result["next_cursor"].(string)+`"}`); !isInvalidRequest(err) {
		t.Errorf("a cursor of another page: err = %v", err)
	}
	for arguments, want := range map[string]struct {
		method string
		form   map[string]string
	}{
		`{` + base + `,"page_type":"title","page":"Cart"}`: {"Transitions.getTransitionsForPageTitle", map[string]string{"pageTitle": "Cart"}},
		`{` + base + `,"page_type":"title","page":"Cart","parts":["followingActions","externalReferrers"]}`: {"Transitions.getTransitionsForAction",
			map[string]string{"actionName": "Cart", "actionType": "title", "parts": "followingActions,externalReferrers"}},
	} {
		if _, err := env.invoke("matomo.transitions.get", "rm-sites", arguments); err != nil {
			t.Fatal(err)
		}
		got := lastCall(calls)
		if got.api() != want.method {
			t.Errorf("method = %s, want %s", got.api(), want.method)
		}
		for name, value := range want.form {
			if got.form.Get(name) != value {
				t.Errorf("%s: %s = %q, want %q", want.method, name, got.form.Get(name), value)
			}
		}
	}
}

func TestTransitionsGetRefusesBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newReadMoreEnvironment(t, &calls, readMoreAnswers(nil).handler(t))
	base := `"period":"day","date":"today","page_type":"url","page":"/cart"`
	_, err := env.invoke("matomo.transitions.get", "rm-one", `{"idSite":1,`+base+`}`)
	if err == nil || strings.Contains(err.Error(), "site/") {
		t.Errorf("a foreign site: err = %v", err)
	}
	for _, arguments := range []string{
		`{"idSite":1,"period":"day","date":"today","page_type":"other","page":"/cart"}`,
		`{"idSite":1,"period":"day","date":"today","page_type":"url","page":""}`,
		`{"idSite":1,"period":"day","date":"today","page_type":"url","page":"a` + "\n" + `b"}`,
		`{"idSite":1,"period":"day","date":"today","page_type":"url","page":"` + strings.Repeat("a", 2049) + `"}`,
		`{"idSite":1,"period":"day","date":"last7","page_type":"url","page":"/cart"}`,
		`{"idSite":1,` + base[:strings.Index(base, `,"page_type"`)] + `,"page_type":"url","page":"/c","parts":["other"]}`,
		`{"idSite":1,` + base + `,"parts":["followingActions","followingActions"]}`, `{"idSite":1,` + base + `,"parts":[]}`,
		`{"idSite":1,` + base + `,"limit_before_grouping":0}`, `{"idSite":1,` + base + `,"segment":"x"}`,
		`{"idSite":1,` + base + `,"method":"API"}`,
	} {
		if _, err := env.invoke("matomo.transitions.get", "rm-sites", arguments); err == nil {
			t.Errorf("accepted %s", arguments)
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("secret reads = %d, requests = %d, want none", *env.reads, len(calls))
	}
}

func TestTransitionsGetMapsTheCloudLimitWithoutRetry(t *testing.T) {
	var calls []call
	env := newReadMoreEnvironment(t, &calls, func(c call) (*http.Response, error) {
		switch c.api() {
		case "API.getMatomoVersion":
			return ok(versionAnswer), nil
		case "API.isPluginActivated":
			return ok(`{"value":true}`), nil
		}
		return respond(429, ""), nil
	})
	_, err := env.invoke("matomo.transitions.get", "rm-sites", `{"idSite":1,"period":"day","date":"today","page_type":"url","page":"/cart"}`)
	if classOf(err) != provider.ClassRateLimited || len(calls) != 3 {
		t.Fatalf("err = %v, calls = %d, want the version, the plugin, and one refused transitions request", err, len(calls))
	}
}

func TestBotRealtimeGetNeedsVersionAndPluginWithoutFurtherRequests(t *testing.T) {
	var calls []call
	env := newReadMoreEnvironment(t, &calls, readMoreAnswers(answers{
		"BotTracking.getAIChatbotsRealTime":  func(call) *http.Response { return ok(botChatbots) },
		"BotTracking.getTopPageUrlsRealTime": func(call) *http.Response { return ok(`[{"label":"shop.example.test/cart","requests":"4"}]`) },
	}).handler(t))
	text, err := env.invoke("matomo.bot-realtime.get", "rm-sites", `{"idSite":1,"kind":"chatbots","last_minutes":60,"limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	got := lastCall(calls)
	if got.api() != "BotTracking.getAIChatbotsRealTime" || got.form.Get("idSite") != "1" || got.form.Get("lastMinutes") != "60" {
		t.Fatalf("request = %+v", got)
	}
	result := decodeObject(t, text)
	if len(result["rows"].([]any)) != 1 || result["has_more"] != true || result["last_minutes"] != float64(60) ||
		!strings.Contains(text, `"BotTracking_AIChatbotsUniquePageUrls":"5"`) {
		t.Fatalf("result = %s", text)
	}
	text, err = env.invoke("matomo.bot-realtime.get", "rm-sites", `{"idSite":1,"kind":"top-pages"}`)
	if err != nil || !strings.Contains(text, `"label":"shop.example.test/cart"`) || strings.Contains(text, "last_minutes") {
		t.Fatalf("top-pages: %s, %v", text, err)
	}
	if lastCall(calls).form.Has("lastMinutes") {
		t.Error("lastMinutes was sent without being asked for")
	}
	for _, arguments := range []string{`{"idSite":1,"kind":"x"}`, `{"idSite":1,"kind":"chatbots","last_minutes":0}`,
		`{"idSite":1,"kind":"chatbots","last_minutes":721}`, `{"idSite":1,"kind":"chatbots","idSite2":1}`} {
		if _, err := env.invoke("matomo.bot-realtime.get", "rm-sites", arguments); err == nil {
			t.Errorf("accepted %s", arguments)
		}
	}

	for name, tc := range map[string]struct {
		version string
		plugin  bool
	}{"old release": {"5.11.2", true}, "plugin off": {"5.14.2", false}} {
		var seen []call
		env := newReadMoreEnvironment(t, &seen, with(versionOnly(tc.version), answers{
			"API.isPluginActivated": func(call) *http.Response {
				return ok(map[bool]string{true: `{"value":true}`, false: `{"value":false}`}[tc.plugin])
			},
		}).handler(t))
		_, err := env.invoke("matomo.bot-realtime.get", "rm-sites", `{"idSite":1,"kind":"chatbots"}`)
		if !isUnsupported(err) {
			t.Errorf("%s: err = %v", name, err)
		}
		for _, one := range seen {
			if strings.HasPrefix(one.api(), "BotTracking.") {
				t.Errorf("%s: sent %s", name, one.api())
			}
		}
	}
	var seen []call
	env = newReadMoreEnvironment(t, &seen, readMoreAnswers(nil).handler(t))
	if _, err := env.invoke("matomo.bot-realtime.get", "rm-one", `{"idSite":1,"kind":"chatbots"}`); err == nil ||
		strings.Contains(err.Error(), "site/") || *env.reads != 0 || len(seen) != 0 {
		t.Errorf("a foreign site: err = %v, reads = %d, requests = %d", err, *env.reads, len(seen))
	}
}

func TestDatabaseStatisticsGetUsesOneMethodPerSection(t *testing.T) {
	var calls []call
	env := newReadMoreEnvironment(t, &calls, readMoreAnswers(answers{
		"DBStats.getGeneralInformation": func(call) *http.Response { return ok(`[3,12,"104857600"]`) },
		"DBStats.getDBStatus": func(call) *http.Response {
			return ok(`{"Uptime":"86400","Threads":"2","Questions":"1000","Slow queries":"1","Opens":"unavailable"}`)
		},
		"DBStats.getDatabaseUsageSummary": func(call) *http.Response {
			return ok(`[{"label":"tracker_data","data_size":10,"index_size":2,"row_count":5}]`)
		},
		"DBStats.getTrackerDataSummary":      func(call) *http.Response { return ok(trackerTables) },
		"DBStats.getMetricDataSummary":       func(call) *http.Response { return ok(`[]`) },
		"DBStats.getMetricDataSummaryByYear": func(call) *http.Response { return ok(`[{"label":"2026","data_size":1,"index_size":1,"row_count":1}]`) },
		"DBStats.getReportDataSummary":       func(call) *http.Response { return ok(`[]`) },
		"DBStats.getReportDataSummaryByYear": func(call) *http.Response { return ok(`[]`) },
		"DBStats.getAdminDataSummary": func(call) *http.Response {
			return ok(`[{"label":"matomo_option","data_size":1,"index_size":1,"row_count":1}]`)
		},
		"DBStats.getIndividualReportsSummary": func(call) *http.Response {
			return ok(`[{"label":"Actions_actions","row_count":4,"blob_size":900,"name_size":40}]`)
		},
		"DBStats.getIndividualMetricsSummary": func(call) *http.Response { return ok(`[{"label":"nb_visits","row_count":4}]`) },
	}).handler(t))
	methods := map[string]string{"general-information": "getGeneralInformation", "status": "getDBStatus",
		"usage-summary": "getDatabaseUsageSummary", "tracker-tables": "getTrackerDataSummary",
		"metric-tables": "getMetricDataSummary", "metric-tables-by-year": "getMetricDataSummaryByYear",
		"report-tables": "getReportDataSummary", "report-tables-by-year": "getReportDataSummaryByYear",
		"admin-tables": "getAdminDataSummary", "individual-reports": "getIndividualReportsSummary",
		"individual-metrics": "getIndividualMetricsSummary"}
	if len(methods) != len(dbSections) {
		t.Fatalf("sections = %d, methods = %d", len(dbSections), len(methods))
	}
	for section, method := range methods {
		text, err := env.invoke("matomo.database-statistics.get", "rm-db", `{"section":"`+section+`"}`)
		if err != nil {
			t.Fatalf("%s: %v", section, err)
		}
		got := lastCall(calls)
		if got.api() != "DBStats."+method || got.auth != "Bearer "+tokenValue || got.form.Has("token_auth") ||
			got.form.Has("forceCache") || decodeObject(t, text)["section"] != section {
			t.Errorf("%s: request = %+v, result = %s", section, got, text)
		}
	}
	text, _ := env.invoke("matomo.database-statistics.get", "rm-db-mixed", `{"section":"general-information"}`)
	if !strings.Contains(text, `"website_count":3`) || !strings.Contains(text, `"total_size":"104857600"`) {
		t.Errorf("general-information = %s", text)
	}
	text, _ = env.invoke("matomo.database-statistics.get", "rm-db", `{"section":"status"}`)
	if !strings.Contains(text, `"Slow queries":"1"`) || !strings.Contains(text, `"Opens":"unavailable"`) {
		t.Errorf("status = %s", text)
	}
	text, _ = env.invoke("matomo.database-statistics.get", "rm-db", `{"section":"individual-reports","force_cache":true}`)
	if lastCall(calls).form.Get("forceCache") != "true" || !strings.Contains(text, `"blob_size":900`) {
		t.Errorf("individual-reports = %s, form = %v", text, lastCall(calls).form)
	}
	text, err := env.invoke("matomo.database-statistics.get", "rm-db", `{"section":"tracker-tables","limit":2}`)
	result := decodeObject(t, text)
	if err != nil || len(result["rows"].([]any)) != 2 || result["has_more"] != true {
		t.Fatalf("tracker-tables = %s, %v", text, err)
	}
	text, _ = env.invoke("matomo.database-statistics.get", "rm-db", `{"section":"tracker-tables","cursor":"`+result["next_cursor"].(string)+`"}`)
	if rows := decodeObject(t, text)["rows"].([]any); len(rows) != 1 || strings.Contains(text, "next_cursor") {
		t.Errorf("second batch = %s", text)
	}
}

func TestDatabaseStatisticsGetRefusesBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newReadMoreEnvironment(t, &calls, readMoreAnswers(nil).handler(t))
	for _, tc := range []struct{ connection, arguments string }{
		{"rm-db-sites", `{"section":"status"}`}, {"rm-db-one", `{"section":"status"}`},
		{"rm-db-two", `{"section":"status"}`}, {"rm-db", `{}`}, {"rm-db", `{"section":"nope"}`},
		{"rm-db", `{"section":"status","force_cache":true}`}, {"rm-db", `{"section":"tracker-tables","force_cache":true}`},
		{"rm-db", `{"section":"status","limit":5}`}, {"rm-db", `{"section":"general-information","cursor":"abc"}`},
		{"rm-db", `{"section":"tracker-tables","limit":1001}`}, {"rm-db", `{"section":"tracker-tables","cursor":"bogus"}`},
		{"rm-db", `{"section":"status","method":"DBStats.getDBStatus"}`},
	} {
		if _, err := env.invoke("matomo.database-statistics.get", tc.connection, tc.arguments); err == nil {
			t.Errorf("%s %s: accepted", tc.connection, tc.arguments)
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("secret reads = %d, requests = %d, want none", *env.reads, len(calls))
	}
	if ok, forms := reaches(databaseStatisticsGet.ID, []string{"site"}); ok || len(forms) != 1 || forms[0] != "instance" {
		t.Errorf("reaches = %v %v", ok, forms)
	}
	if ok, _ := reaches(databaseStatisticsGet.ID, []string{"instance"}); !ok {
		t.Error("an instance binding is not offered database-statistics.get")
	}
	for _, id := range []string{insightsList.ID, transitionsGet.ID, botRealtimeGet.ID} {
		if ok, _ := reaches(id, []string{"instance"}); ok {
			t.Errorf("an instance-only binding is offered %s", id)
		}
	}
}

// readMoreTools are the four tools with a connection and arguments that pass every check before a request.
var readMoreTools = []struct{ id, connection, arguments string }{
	{"matomo.insights.list", "rm-sites", `{"kind":"insights","idSite":1,"period":"day","date":"yesterday","report":"Actions_getPageUrls"}`},
	{"matomo.transitions.get", "rm-sites", `{"idSite":1,"page_type":"url","page":"https://shop.example.test/cart","period":"day","date":"yesterday"}`},
	{"matomo.bot-realtime.get", "rm-sites", `{"idSite":1,"kind":"chatbots"}`},
	{"matomo.database-statistics.get", "rm-db", `{"section":"tracker-tables"}`},
}

func TestReadMoreToolsMapErrorsAndNeverLeakTheMessage(t *testing.T) {
	passthrough := func(c call, body func() (*http.Response, error)) (*http.Response, error) {
		switch c.api() {
		case "API.getMatomoVersion":
			return ok(versionAnswer), nil
		case "API.isPluginActivated":
			return ok(`{"value":true}`), nil
		}
		return body()
	}
	for _, tool := range readMoreTools {
		for name, tc := range map[string]struct {
			message string
			class   provider.Class
			missing bool
		}{
			"permission":     {"You can't access this resource as it requires 'superuser' access.", provider.ClassPermission, false},
			"not found":      {"An unexpected website was found in the request.", provider.ClassNotFound, false},
			"method missing": {"The method 'X.y' does not exist or is not available in the module 'X'.", "", true},
			"plugin off":     {"The plugin X is not activated.", "", true},
			"other":          {"Something went wrong", provider.ClassInvalidResponse, false},
		} {
			var calls []call
			env := newReadMoreEnvironment(t, &calls, func(c call) (*http.Response, error) {
				return passthrough(c, func() (*http.Response, error) { return failure(tc.message + bodyCanary), nil })
			})
			_, err := env.invoke(tool.id, tool.connection, tool.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || isUnsupported(err) != tc.missing || classOf(err) != tc.class {
				t.Errorf("%s %s: err = %v", tool.id, name, err)
			}
		}
		for status, want := range map[int]provider.Class{429: provider.ClassRateLimited, 401: provider.ClassAuth} {
			var calls []call
			env := newReadMoreEnvironment(t, &calls, func(c call) (*http.Response, error) {
				return passthrough(c, func() (*http.Response, error) { return respond(status, bodyCanary), nil })
			})
			if _, err := env.invoke(tool.id, tool.connection, tool.arguments); classOf(err) != want || strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("%s status %d: err = %v", tool.id, status, err)
			}
		}
		for name, body := range map[string]string{"html": `<html>` + bodyCanary + `</html>`, "wrong shape": `"` + bodyCanary + `"`} {
			var calls []call
			env := newReadMoreEnvironment(t, &calls, func(c call) (*http.Response, error) {
				return passthrough(c, func() (*http.Response, error) { return ok(body), nil })
			})
			if _, err := env.invoke(tool.id, tool.connection, tool.arguments); classOf(err) != provider.ClassInvalidResponse ||
				strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("%s %s: err = %v", tool.id, name, err)
			}
		}
		var calls []call
		env := newReadMoreEnvironment(t, &calls, func(c call) (*http.Response, error) {
			return passthrough(c, func() (*http.Response, error) { return nil, context.DeadlineExceeded })
		})
		if _, err := env.invoke(tool.id, tool.connection, tool.arguments); classOf(err) != provider.ClassTimeout {
			t.Errorf("%s timeout: err = %v", tool.id, err)
		}
		var sent []call
		env = newReadMoreEnvironment(t, &sent, func(c call) (*http.Response, error) {
			return passthrough(c, func() (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: errReader{}, Header: http.Header{}}, nil
			})
		})
		if _, err := env.invoke(tool.id, tool.connection, tool.arguments); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s unreadable body: err = %v", tool.id, err)
		}
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, context.Canceled }
func (errReader) Close() error             { return nil }

func TestReadMoreToolsCarryNoForbiddenParameterAndAreListedOnly(t *testing.T) {
	var calls []call
	env := newReadMoreEnvironment(t, &calls, readMoreAnswers(answers{
		"Insights.getInsights":                 func(call) *http.Response { return ok(`[]`) },
		"Transitions.getTransitionsForPageUrl": func(call) *http.Response { return ok(`{}`) },
		"BotTracking.getAIChatbotsRealTime":    func(call) *http.Response { return ok(`[]`) },
		"DBStats.getTrackerDataSummary":        func(call) *http.Response { return ok(`[]`) },
	}).handler(t))
	for _, tool := range readMoreTools {
		if _, err := env.invoke(tool.id, tool.connection, tool.arguments); err != nil {
			t.Fatalf("%s: %v", tool.id, err)
		}
	}
	for _, got := range calls {
		if strings.Contains(got.rawQuery, tokenValue) || strings.Contains(got.path, tokenValue) || got.form.Has("token_auth") || got.auth != "Bearer "+tokenValue {
			t.Errorf("token reached the URL or the form: %+v", got)
		}
		for _, name := range []string{"callback", "jsoncallback", "disable_generic_filters", "disable_queued_filters",
			"disable_root_datatable_post_processor"} {
			if got.form.Has(name) {
				t.Errorf("request carries %s", name)
			}
		}
		if got.form.Get("filter_limit") == "-1" {
			t.Error("request carries filter_limit=-1")
		}
	}
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, tool := range readMoreTools {
		descriptor, _, found := reg.Lookup(tool.id)
		if !found {
			t.Fatalf("%s is not registered", tool.id)
		}
		risk := descriptor.Risk
		want := reportDataSensitive
		if tool.id == "matomo.database-statistics.get" {
			want = instanceSensitive
		}
		if string(risk.Effect) != "read" || string(risk.Idempotency) != "safe" || string(risk.Confirmation) != "none" ||
			!risk.OpenWorld || risk.DataSensitivity != want || !descriptor.RequiresToolAllowList {
			t.Errorf("%s: risk = %+v, listed only = %t", tool.id, risk, descriptor.RequiresToolAllowList)
		}
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if id == tool.id {
					t.Errorf("profile %s holds %s", profile.ID, id)
				}
			}
		}
	}
}
