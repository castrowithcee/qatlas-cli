package matomo

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// catalogFixture follows the form of API.getReportMetadata: standard reports, a goal report with its
// parameters, a custom dimension report, and empty maps that Matomo encodes as [].
const catalogFixture = `[
{"category":"Behaviour","subcategory":"Pages","name":"Page URLs","module":"Actions","action":"getPageUrls",
 "dimension":"Page URL","metrics":{"nb_hits":"Pageviews","nb_visits":"Unique Pageviews"},
 "metricsDocumentation":{"nb_hits":"The number of times this page was visited."},
 "processedMetrics":{"bounce_rate":"Bounce Rate"},"parameters":[],"documentation":"This report lists pages.",
"isSubtableReport":false,"actionToLoadSubTables":"getPageUrls","order":1,
 "uniqueId":"Actions_getPageUrls"},
{"category":"Behaviour","subcategory":"Pages","name":"Page titles","module":"Actions","action":"getPageTitles",
 "dimension":"Page title","metrics":{"nb_hits":"Pageviews"},"processedMetrics":false,"parameters":[],
 "uniqueId":"Actions_getPageTitles"},
{"category":"Acquisition","name":"Channel Type","module":"Referrers","action":"getReferrerType","dimension":"Channel Type",
 "metrics":{"nb_visits":"Visits"},"parameters":[],"uniqueId":"Referrers_getReferrerType"},
{"category":"Goals","name":"Goal Newsletter","module":"Goals","action":"get","parameters":{"idGoal":"3"},
 "metrics":{"nb_conversions":"Conversions"},"processedMetrics":{"conversion_rate":"Conversion Rate"},
 "uniqueId":"Goals_get_idGoal--3"},
{"category":"Custom Dimension","name":"Plan","module":"CustomDimensions","action":"getCustomDimension",
 "parameters":{"idDimension":2},"dimension":"Plan","metrics":{"nb_visits":"Visits"},
 "uniqueId":"CustomDimensions_getCustomDimension_idDimension--2"}
]`

func catalogAnswers(reads *int) answers {
	return with(versionOnly("5.14.2"), answers{"API.getReportMetadata": func(c call) *http.Response {
		if reads != nil {
			*reads++
		}
		if c.form.Get("idSite") == "99" {
			return failure(permissionMsg)
		}
		return ok(catalogFixture)
	}})
}

func newCatalogEnvironment(t *testing.T, calls *[]call, a answers) *environment {
	t.Helper()
	env := newEnvironment(t, calls, a.handler(t))
	resetCatalogs()
	t.Cleanup(resetCatalogs)
	return env
}

func fakeClock(t *testing.T) *time.Time {
	t.Helper()
	clock := time.Now()
	previous := now
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = previous })
	return &clock
}

type reportsListed struct {
	IDSite  int `json:"idSite"`
	Reports []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Category string `json:"category"`
	} `json:"reports"`
	Categories []CategoryCount `json:"categories"`
	HasMore    bool            `json:"has_more"`
	NextCursor string          `json:"next_cursor"`
	Raw        map[string]any  `json:"-"`
}

func (r reportsListed) ids() []string {
	ids := []string{}
	for _, report := range r.Reports {
		ids = append(ids, report.ID)
	}
	return ids
}

func listReports(t *testing.T, env *environment, connection, arguments string) reportsListed {
	t.Helper()
	result, err := env.invoke("matomo.reports.list", connection, arguments)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var out reportsListed
	if json.Unmarshal([]byte(result), &out) != nil {
		t.Fatalf("result = %s", result)
	}
	return out
}

func TestReportsListProjectsCompactEntriesAndCategories(t *testing.T) {
	var calls []call
	env := newCatalogEnvironment(t, &calls, catalogAnswers(nil))
	result, err := env.invoke("matomo.reports.list", "open", `{"idSite":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var generic struct {
		Reports []map[string]any `json:"reports"`
	}
	_ = json.Unmarshal([]byte(result), &generic)
	for _, report := range generic.Reports {
		if len(report) != 3 {
			t.Fatalf("report %v holds more than id, name, category", report)
		}
	}
	got := listReports(t, env, "open", `{"idSite":1}`)
	if got.IDSite != 1 || len(got.Reports) != 5 || got.Reports[0].ID != "Actions_getPageUrls" ||
		got.Reports[0].Name != "Page URLs" || got.Reports[0].Category != "Behaviour" {
		t.Fatalf("got = %+v", got)
	}
	want := []CategoryCount{{"Behaviour", 2}, {"Acquisition", 1}, {"Goals", 1}, {"Custom Dimension", 1}}
	if !slices.Equal(got.Categories, want) {
		t.Errorf("categories = %v, want %v", got.Categories, want)
	}
	// The request carries exactly the fixed method and the website.
	last := calls[len(calls)-1]
	if last.api() != "API.getReportMetadata" || last.form.Get("idSite") != "1" || last.form.Has("idSites") || last.form.Has("language") || last.form.Has("period") {
		t.Errorf("request = %v", last.form)
	}
}

func TestReportsListFiltersByCategoryAndQuery(t *testing.T) {
	var calls []call
	env := newCatalogEnvironment(t, &calls, catalogAnswers(nil))
	byCategory := listReports(t, env, "open", `{"idSite":1,"category":"behaviour"}`)
	if !slices.Equal(byCategory.ids(), []string{"Actions_getPageUrls", "Actions_getPageTitles"}) || byCategory.Categories != nil {
		t.Errorf("category filter = %+v", byCategory)
	}
	byQuery := listReports(t, env, "open", `{"idSite":1,"query":"PAGE"}`)
	if !slices.Equal(byQuery.ids(), []string{"Actions_getPageUrls", "Actions_getPageTitles"}) ||
		!slices.Equal(byQuery.Categories, []CategoryCount{{"Behaviour", 2}}) {
		t.Errorf("query filter = %+v", byQuery)
	}
	byID := listReports(t, env, "open", `{"idSite":1,"query":"idgoal--3"}`)
	if !slices.Equal(byID.ids(), []string{"Goals_get_idGoal--3"}) {
		t.Errorf("id search = %+v", byID)
	}
	none := listReports(t, env, "open", `{"idSite":1,"category":"Nothing"}`)
	if len(none.Reports) != 0 || none.HasMore {
		t.Errorf("unknown category = %+v", none)
	}
}

func TestReportsListPagesWithCursor(t *testing.T) {
	var calls []call
	env := newCatalogEnvironment(t, &calls, catalogAnswers(nil))
	first := listReports(t, env, "open", `{"idSite":1,"limit":2}`)
	if len(first.Reports) != 2 || !first.HasMore || first.NextCursor == "" || len(first.Categories) == 0 {
		t.Fatalf("first = %+v", first)
	}
	second := listReports(t, env, "open", `{"idSite":1,"cursor":"`+first.NextCursor+`"}`)
	if !slices.Equal(second.ids(), []string{"Referrers_getReferrerType", "Goals_get_idGoal--3"}) || !second.HasMore ||
		second.Categories != nil {
		t.Fatalf("second = %+v", second)
	}
	last := listReports(t, env, "open", `{"idSite":1,"cursor":"`+second.NextCursor+`"}`)
	if len(last.Reports) != 1 || last.HasMore || last.NextCursor != "" {
		t.Fatalf("last = %+v", last)
	}
	for name, arguments := range map[string]string{
		"altered":        `{"idSite":1,"cursor":"` + first.NextCursor[:len(first.NextCursor)-2] + `AA"}`,
		"other category": `{"idSite":1,"category":"Goals","cursor":"` + first.NextCursor + `"}`,
		"other site":     `{"idSite":2,"cursor":"` + first.NextCursor + `"}`,
		"other limit":    `{"idSite":1,"limit":3,"cursor":"` + first.NextCursor + `"}`,
	} {
		if _, err := env.invoke("matomo.reports.list", "open", arguments); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want invalid-request", name, err)
		}
	}
}

func TestReportsDescribeReturnsDetailsAndDocumentationOnRequest(t *testing.T) {
	var calls []call
	env := newCatalogEnvironment(t, &calls, catalogAnswers(nil))
	result, err := env.invoke("matomo.reports.describe", "open", `{"idSite":1,"id":"Actions_getPageUrls"}`)
	if err != nil {
		t.Fatal(err)
	}
	var report Report
	_ = json.Unmarshal([]byte(result), &report)
	if report.Module != "Actions" || report.Action != "getPageUrls" || report.Dimension != "Page URL" ||
		len(report.Metrics) != 2 || report.Metrics[0] != (Pair{"nb_hits", "Pageviews"}) ||
		len(report.ProcessedMetrics) != 1 || report.SubtableAction != "getPageUrls" ||
		len(report.Parameters) != 0 || report.MetricsDocumentation != nil || strings.Contains(result, "was visited") {
		t.Fatalf("report = %s", result)
	}
	if calls[len(calls)-1].form.Get("hideMetricsDoc") != "1" {
		t.Error("the catalog read asked for the documentation")
	}
	withDocs, err := env.invoke("matomo.reports.describe", "open", `{"idSite":1,"id":"Actions_getPageUrls","includeMetricsDocumentation":true}`)
	if err != nil || !strings.Contains(withDocs, "The number of times this page was visited.") {
		t.Fatalf("with docs = %s, err = %v", withDocs, err)
	}
	if calls[len(calls)-1].form.Get("hideMetricsDoc") != "0" {
		t.Error("the documentation read did not ask for the documentation")
	}
	goal, err := env.invoke("matomo.reports.describe", "open", `{"idSite":1,"id":"Goals_get_idGoal--3"}`)
	var goalReport Report
	_ = json.Unmarshal([]byte(goal), &goalReport)
	if err != nil || len(goalReport.Parameters) != 1 || goalReport.Parameters[0] != (Pair{"idGoal", "3"}) {
		t.Fatalf("goal = %s, err = %v", goal, err)
	}
	dimension, _ := env.invoke("matomo.reports.describe", "open", `{"idSite":1,"id":"CustomDimensions_getCustomDimension_idDimension--2"}`)
	if !strings.Contains(dimension, `{"name":"idDimension","value":"2"}`) {
		t.Errorf("custom dimension = %s", dimension)
	}
}

func TestReportsDescribeUnknownIDIsNotFound(t *testing.T) {
	var calls []call
	env := newCatalogEnvironment(t, &calls, catalogAnswers(nil))
	for _, id := range []string{"Actions_getNothing", "Actions.getPageUrls", "actions_getpageurls"} {
		_, err := env.invoke("matomo.reports.describe", "open", `{"idSite":1,"id":"`+id+`"}`)
		if classOf(err) != provider.ClassNotFound {
			t.Errorf("%s: err = %v, want not-found", id, err)
		}
	}
	if _, err := env.invoke("matomo.reports.describe", "open", `{"idSite":1}`); err == nil {
		t.Error("a missing id was accepted")
	}
}

func TestCatalogIsCachedPerSiteAndLanguageAndExpires(t *testing.T) {
	var calls []call
	reads := 0
	env := newCatalogEnvironment(t, &calls, catalogAnswers(&reads))
	clock := fakeClock(t)
	listReports(t, env, "open", `{"idSite":1}`)
	listReports(t, env, "open", `{"idSite":1,"category":"Goals"}`)
	if _, err := env.invoke("matomo.reports.describe", "open", `{"idSite":1,"id":"Actions_getPageUrls"}`); err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Fatalf("reads = %d, want 1 for list, filter, and describe", reads)
	}
	listReports(t, env, "open", `{"idSite":2}`)
	listReports(t, env, "open", `{"idSite":1,"language":"de"}`)
	if reads != 3 {
		t.Fatalf("reads = %d, want 3 after another site and another language", reads)
	}
	if last := calls[len(calls)-1]; last.form.Get("language") != "de" {
		t.Errorf("language not sent: %v", last.form)
	}
	*clock = clock.Add(catalogTTL - time.Second)
	listReports(t, env, "open", `{"idSite":1}`)
	if reads != 3 {
		t.Fatalf("reads = %d, want a hit just before expiry", reads)
	}
	*clock = clock.Add(2 * time.Second)
	listReports(t, env, "open", `{"idSite":1}`)
	if reads != 4 {
		t.Fatalf("reads = %d, want a new read after expiry", reads)
	}
}

func TestCatalogCacheKeyHoldsNoTokenAndIsBounded(t *testing.T) {
	var calls []call
	env := newCatalogEnvironment(t, &calls, catalogAnswers(nil))
	listReports(t, env, "open", `{"idSite":1}`)
	catalogs.mu.Lock()
	defer catalogs.mu.Unlock()
	for key := range catalogs.known {
		if strings.Contains(string(key.instance[:]), tokenValue) || key.site != 1 {
			t.Errorf("key = %+v", key)
		}
	}
	for i := 0; i < 3*maxCatalogEntries; i++ {
		catalogs.mu.Unlock()
		holdCatalog(catalogKey{site: i + 10}, &Catalog{})
		catalogs.mu.Lock()
	}
	if len(catalogs.known) > maxCatalogEntries {
		t.Errorf("entries = %d, want at most %d", len(catalogs.known), maxCatalogEntries)
	}
}

func TestReportCatalogIsReusableForReportValidation(t *testing.T) {
	var calls []call
	reads := 0
	env := newCatalogEnvironment(t, &calls, catalogAnswers(&reads))
	client, err := Open(t.Context(), mustResolve(t, env, "open"), resolver(env.red, nil), env.red)
	if err != nil {
		t.Fatal(err)
	}
	req := baseRequirement("matomo.reports.get")
	catalog, err := client.ReportCatalog(t.Context(), "get report", req, 1, ReportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	report, err := catalog.Require("get report", "Goals_get_idGoal--3")
	if err != nil || report.Module != "Goals" || report.Action != "get" || report.Parameters[0].Name != "idGoal" {
		t.Fatalf("report = %+v, err = %v", report, err)
	}
	if _, err := catalog.Require("get report", "Goals_get_idGoal--4"); classOf(err) != provider.ClassNotFound {
		t.Errorf("err = %v, want not-found", err)
	}
	if _, err := client.ReportCatalog(t.Context(), "get report", req, 1, ReportOptions{}); err != nil || reads != 1 {
		t.Errorf("second access: reads = %d, err = %v", reads, err)
	}
}

func TestReportsToolsRefuseSitesOutsideTargetsBeforeSecretAndIO(t *testing.T) {
	var calls []call
	env := newCatalogEnvironment(t, &calls, catalogAnswers(nil))
	for _, tc := range []struct{ tool, connection, arguments string }{
		{"matomo.reports.list", "one", `{"idSite":1}`},
		{"matomo.reports.list", "two", `{}`},
		{"matomo.reports.list", "open", `{}`},
		{"matomo.reports.list", "open", `{"idSite":0}`},
		{"matomo.reports.describe", "one", `{"idSite":9,"id":"x"}`},
		{"matomo.segments.fields", "two", `{"idSite":3}`},
		{"matomo.segments.suggest-values", "one", `{"idSite":1,"segment":"pageUrl"}`},
		{"matomo.reports.list", "open", `{"idSite":1,"language":"de; drop"}`},
		{"matomo.reports.list", "open", `{"idSite":1,"idSites":"all"}`},
	} {
		if _, err := env.invoke(tc.tool, tc.connection, tc.arguments); err == nil {
			t.Errorf("%s %s %s: accepted", tc.tool, tc.connection, tc.arguments)
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("secret reads = %d, requests = %d, want none", *env.reads, len(calls))
	}
	// A single bound website needs no argument.
	if got := listReports(t, env, "one", `{}`); got.IDSite != 2 {
		t.Errorf("idSite = %d, want 2", got.IDSite)
	}
}

func TestReportsToolsMapErrorsAndHideTheToken(t *testing.T) {
	for name, tc := range map[string]struct {
		answer func(call) *http.Response
		class  provider.Class
	}{
		"permission": {func(call) *http.Response { return failure(permissionMsg) }, provider.ClassPermission},
		"not found":  {func(call) *http.Response { return failure("Website not found") }, provider.ClassNotFound},
		"429":        {func(call) *http.Response { return respond(429, "slow") }, provider.ClassRateLimited},
		"html":       {func(call) *http.Response { return ok("<html>login</html>") }, provider.ClassInvalidResponse},
		"not a list": {func(call) *http.Response { return ok(`{"a":1}`) }, provider.ClassInvalidResponse},
	} {
		var calls []call
		env := newCatalogEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{"API.getReportMetadata": tc.answer}))
		_, err := env.invoke("matomo.reports.list", "open", `{"idSite":1}`)
		if classOf(err) != tc.class {
			t.Errorf("%s: err = %v, want %s", name, err, tc.class)
		}
		if err != nil && strings.Contains(err.Error(), tokenValue) {
			t.Errorf("%s: error holds the token", name)
		}
	}
	var calls []call
	missing := with(versionOnly("5.14.2"), answers{"API.getReportMetadata": func(call) *http.Response {
		return failure("The plugin is not activated")
	}})
	env := newCatalogEnvironment(t, &calls, missing)
	if _, err := env.invoke("matomo.reports.list", "open", `{"idSite":1}`); !isUnsupported(err) {
		t.Errorf("err = %v, want unsupported-capability", err)
	}
	// A failure is never held.
	var retry []call
	reads := 0
	env2 := newCatalogEnvironment(t, &retry, with(versionOnly("5.14.2"), answers{"API.getReportMetadata": func(call) *http.Response {
		reads++
		if reads == 1 {
			return failure(permissionMsg)
		}
		return ok(catalogFixture)
	}}))
	_, _ = env2.invoke("matomo.reports.list", "open", `{"idSite":1}`)
	if got := listReports(t, env2, "open", `{"idSite":1}`); len(got.Reports) != 5 {
		t.Errorf("after a failure: %+v", got)
	}
	for _, c := range append(calls, retry...) {
		if c.auth != "Bearer "+tokenValue || c.rawQuery != "" || strings.Contains(c.form.Encode(), tokenValue) {
			t.Errorf("token handling: auth %q query %q", c.auth, c.rawQuery)
		}
	}
}

func TestReportsListBoundsProviderTextAndSkipsIncompleteRows(t *testing.T) {
	long := strings.Repeat("x", 5000)
	rows := []string{`{"uniqueId":"A","name":"` + long + `","category":"` + long + `","metrics":[],"parameters":[]}`,
		`{"uniqueId":"","name":"no id","category":"c"}`, `{"uniqueId":"A","name":"duplicate","category":"c"}`,
		`{"uniqueId":"B","name":"n","category":"c","metrics":{"m":["not","scalar"],"k":5}}`}
	var calls []call
	env := newCatalogEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{"API.getReportMetadata": func(call) *http.Response {
		return ok("[" + strings.Join(rows, ",") + "]")
	}}))
	got := listReports(t, env, "open", `{"idSite":1}`)
	if !slices.Equal(got.ids(), []string{"A", "B"}) || len(got.Reports[0].Name) > maxStringLength ||
		len(got.Reports[0].Category) > maxStringLength {
		t.Fatalf("got = %+v", got)
	}
	described, _ := env.invoke("matomo.reports.describe", "open", `{"idSite":1,"id":"B"}`)
	if !strings.Contains(described, `{"name":"k","value":"5"}`) || strings.Contains(described, "scalar") {
		t.Errorf("described = %s", described)
	}
	many := make([]string, maxCatalogReports+50)
	for i := range many {
		many[i] = `{"uniqueId":"R` + strconv.Itoa(i) + `","name":"n","category":"c"}`
	}
	var calls2 []call
	env2 := newCatalogEnvironment(t, &calls2, with(versionOnly("5.14.2"), answers{"API.getReportMetadata": func(call) *http.Response {
		return ok("[" + strings.Join(many, ",") + "]")
	}}))
	if all := listReports(t, env2, "open", `{"idSite":1,"limit":100}`); len(all.Categories) != 1 || all.Categories[0].Count != maxCatalogReports {
		t.Errorf("categories = %v, want the catalog cut at %d", all.Categories, maxCatalogReports)
	}
}

func TestCatalogOptionsAreValidatedSentAndKeyed(t *testing.T) {
	var calls []call
	reads := 0
	env := newCatalogEnvironment(t, &calls, catalogAnswers(&reads))
	for _, bad := range []string{`"period":"decade"`, `"date":"2024-1-1"`, `"date":"last5; x"`, `"date":"2024-01-01,2024-01-31,x"`,
		`"date":"` + strings.Repeat("1", 40) + `"`} {
		if _, err := env.invoke("matomo.reports.list", "open", `{"idSite":1,`+bad+`}`); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("a refused option caused %d requests", len(calls))
	}
	listReports(t, env, "open", `{"idSite":1,"period":"range","date":"2024-01-01,2024-01-31","showSubtableReports":true}`)
	form := calls[len(calls)-1].form
	if form.Get("period") != "range" || form.Get("date") != "2024-01-01,2024-01-31" || form.Get("showSubtableReports") != "1" {
		t.Errorf("request = %v", form)
	}
	for _, date := range []string{"today", "yesterday", "last30", "previous7", "2024-05-01"} {
		listReports(t, env, "open", `{"idSite":1,"period":"day","date":"`+date+`"}`)
	}
	if reads != 6 {
		t.Fatalf("reads = %d, want one per option set", reads)
	}
	listReports(t, env, "open", `{"idSite":1,"period":"day","date":"today"}`)
	if reads != 6 {
		t.Errorf("reads = %d, want a hit for equal options", reads)
	}
	first := listReports(t, env, "open", `{"idSite":1,"period":"day","limit":1}`)
	if _, err := env.invoke("matomo.reports.list", "open", `{"idSite":1,"period":"week","cursor":"`+first.NextCursor+`"}`); !isInvalidRequest(err) {
		t.Errorf("cursor of other options: err = %v", err)
	}
	if _, err := env.invoke("matomo.reports.list", "open", `{"idSite":1,"period":"day","showSubtableReports":true,"cursor":"`+first.NextCursor+`"}`); !isInvalidRequest(err) {
		t.Errorf("cursor of other subtable flag: err = %v", err)
	}
	if _, err := env.invoke("matomo.reports.describe", "open", `{"idSite":1,"id":"Actions_getPageUrls","period":"day","date":"today"}`); err != nil {
		t.Error(err)
	}
}
