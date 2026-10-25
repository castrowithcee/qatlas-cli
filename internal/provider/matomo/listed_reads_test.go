package matomo

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// The four listed-only reads are offered only to a connection whose tools list names them.
const listedReadTools = "[matomo.reports.evolution, matomo.distinct-counts.get, matomo.multi-sites.get, matomo.report-glossary.list]"

const listedConnections = `
  lr-open: {service: matomo, credential: token, tools: ` + listedReadTools + `}
  lr-general: {service: matomo, credential: token, tools: ` + listedReadTools + `, targets: [site]}
  lr-one: {service: matomo, credential: token, tools: ` + listedReadTools + `, targets: [site/2]}
  lr-two: {service: matomo, credential: token, tools: ` + listedReadTools + `, targets: [site/1, site/2]}
  lr-instance: {service: matomo, credential: token, tools: ` + listedReadTools + `, targets: [instance]}
`

func newListedEnvironment(t *testing.T, calls *[]call, a answers) *environment {
	t.Helper()
	return newListedEnvironmentFunc(t, calls, a.handler(t))
}

func newListedEnvironmentFunc(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	resetCatalogs()
	t.Cleanup(resetCatalogs)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(listedConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

// listedAnswers serves the version, the plugin state, the report catalog, and one method of the tool.
func listedAnswers(method string, handler func(call) *http.Response) answers {
	return with(catalogAnswers(nil), answers{
		"API.isPluginActivated": func(call) *http.Response { return ok(`{"value":true}`) },
		method:                  handler,
	})
}

// sameJSON compares two JSON documents without regard to member order and spacing.
func sameJSON(got, want string) bool {
	var a, b any
	return json.Unmarshal([]byte(got), &a) == nil && json.Unmarshal([]byte(want), &b) == nil && reflect.DeepEqual(a, b)
}

func fixed(body string) func(call) *http.Response {
	return func(call) *http.Response { return ok(body) }
}

func lastCall(calls []call, method string) call {
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].api() == method {
			return calls[i]
		}
	}
	return call{}
}

var listedCases = []struct{ tool, method, arguments string }{
	{"matomo.reports.evolution", "API.getRowEvolution",
		`{"idSite":1,"report":"Actions_getPageUrls","period":"day","date":"last3","label":["/a"]}`},
	{"matomo.distinct-counts.get", "Referrers.getNumberOfDistinctWebsites",
		`{"idSite":1,"kind":"websites","period":"day","date":"today"}`},
	{"matomo.multi-sites.get", "MultiSites.getAll", `{"view":"sites","period":"day","date":"today"}`},
	{"matomo.multi-sites.get", "MultiSites.getOne", `{"view":"site","idSite":1,"period":"day","date":"today"}`},
	{"matomo.multi-sites.get", "MultiSites.getAllWithGroups", `{"view":"groups","period":"day","date":"today"}`},
	{"matomo.report-glossary.list", "API.getGlossaryMetrics", `{"idSite":1,"section":"metrics"}`},
	{"matomo.report-glossary.list", "API.getGlossaryReports", `{"idSite":1,"section":"reports"}`},
}

func TestListedReadsAreInNoProfileAndNeedTheToolsList(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, c := range listedCases {
		descriptor, _, ok := reg.Lookup(c.tool)
		risk := descriptor.Risk
		if !ok || !descriptor.RequiresToolAllowList || risk.Effect != "read" || risk.Idempotency != "safe" ||
			risk.Confirmation != "none" || !risk.OpenWorld || risk.DataSensitivity == "" {
			t.Errorf("%s: descriptor = %+v", c.tool, descriptor)
		}
		for _, profile := range metadata.Profiles {
			if slices.Contains(profile.Tools, c.tool) {
				t.Errorf("%s is in profile %s", c.tool, profile.ID)
			}
		}
	}
	var calls []call
	env := newListedEnvironment(t, &calls, answers{})
	for _, c := range listedCases {
		if _, err := env.invoke(c.tool, "open", c.arguments); !isUnsupported(err) {
			t.Errorf("%s by effect: err = %v, want unsupported-capability", c.tool, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("calls = %d, secret reads = %d, want none", len(calls), *env.reads)
	}
}

func TestListedReadsMapProviderFailuresAndHideProviderText(t *testing.T) {
	for _, c := range listedCases {
		for name, tc := range map[string]struct {
			answer func(call) *http.Response
			class  provider.Class
		}{
			"permission": {func(call) *http.Response { return failure(permissionMsg + bodyCanary) }, provider.ClassPermission},
			"not found":  {func(call) *http.Response { return failure("Website not found " + bodyCanary) }, provider.ClassNotFound},
			"429":        {func(call) *http.Response { return respond(http.StatusTooManyRequests, bodyCanary) }, provider.ClassRateLimited},
			"html":       {fixed("<html>" + bodyCanary + "</html>"), provider.ClassInvalidResponse},
			"unknown":    {func(call) *http.Response { return failure("Something " + bodyCanary) }, provider.ClassInvalidResponse},
		} {
			var calls []call
			env := newListedEnvironment(t, &calls, listedAnswers(c.method, tc.answer))
			_, err := env.invoke(c.tool, "lr-open", c.arguments)
			if classOf(err) != tc.class || err != nil && (strings.Contains(err.Error(), bodyCanary) ||
				strings.Contains(err.Error(), tokenValue)) {
				t.Errorf("%s %s: err = %v, want %s", c.method, name, err, tc.class)
			}
			if got := countMethod(calls, c.method); got != 1 {
				t.Errorf("%s %s: sent %d times, want once (no retry)", c.method, name, got)
			}
		}
		var calls []call
		served := listedAnswers(c.method, fixed(`[]`)).handler(t)
		env := newListedEnvironmentFunc(t, &calls, func(x call) (*http.Response, error) {
			if x.api() == c.method {
				return nil, context.DeadlineExceeded
			}
			return served(x)
		})
		if _, err := env.invoke(c.tool, "lr-open", c.arguments); classOf(err) != provider.ClassTimeout ||
			countMethod(calls, c.method) != 1 {
			t.Errorf("%s timeout: err = %v", c.method, err)
		}
	}
}

func TestListedReadsTreatMissingMethodsAndInactivePluginsAsUnsupported(t *testing.T) {
	for _, c := range listedCases {
		var calls []call
		env := newListedEnvironment(t, &calls, listedAnswers(c.method, func(call) *http.Response {
			return failure("The method is not available in the module")
		}))
		if _, err := env.invoke(c.tool, "lr-open", c.arguments); !isUnsupported(err) {
			t.Errorf("%s missing method: err = %v", c.method, err)
		}
	}
	plugins := map[string]string{"matomo.distinct-counts.get": "Referrers", "matomo.multi-sites.get": "MultiSites"}
	for _, c := range listedCases {
		plugin := plugins[c.tool]
		if plugin == "" {
			continue
		}
		var calls []call
		env := newListedEnvironment(t, &calls, with(listedAnswers(c.method, fixed(`[]`)), answers{
			"API.isPluginActivated": func(x call) *http.Response {
				return ok(map[bool]string{true: `{"value":false}`, false: `{"value":true}`}[x.form.Get("pluginName") == plugin])
			}}))
		_, err := env.invoke(c.tool, "lr-open", c.arguments)
		if !isUnsupported(err) || !strings.Contains(err.Error(), plugin) || countMethod(calls, c.method) != 0 {
			t.Errorf("%s inactive plugin: err = %v", c.method, err)
		}
	}
	var calls []call
	env := newListedEnvironment(t, &calls, with(listedAnswers("Referrers.getNumberOfDistinctAIAssistants", fixed(`3`)),
		versionOnly("5.4.0")))
	_, err := env.invoke("matomo.distinct-counts.get", "lr-open", `{"idSite":1,"kind":"ai-assistants","period":"day","date":"today"}`)
	if !isUnsupported(err) || !strings.Contains(err.Error(), "5.5.0") || countMethod(calls, "Referrers.getNumberOfDistinctAIAssistants") != 0 {
		t.Errorf("AI assistants on 5.4.0: err = %v", err)
	}
}

func TestListedReadsRefuseForeignTargetsBeforeSecretAndIO(t *testing.T) {
	var calls []call
	env := newListedEnvironment(t, &calls, answers{})
	for _, tc := range []struct{ tool, connection, arguments string }{
		{"matomo.reports.evolution", "lr-one", `{"idSite":1,"report":"Actions_getPageUrls","period":"day","date":"last3","label":["/a"]}`},
		{"matomo.reports.evolution", "lr-two", `{"report":"Actions_getPageUrls","period":"day","date":"last3","label":["/a"]}`},
		{"matomo.distinct-counts.get", "lr-one", `{"idSite":1,"kind":"countries","period":"day","date":"today"}`},
		{"matomo.distinct-counts.get", "lr-general", `{"idSite":0,"kind":"countries","period":"day","date":"today"}`},
		{"matomo.multi-sites.get", "lr-one", `{"view":"site","idSite":1,"period":"day","date":"today"}`},
		{"matomo.multi-sites.get", "lr-two", `{"view":"site","period":"day","date":"today"}`},
		{"matomo.multi-sites.get", "lr-open", `{"view":"sites","idSite":1,"period":"day","date":"today"}`},
		{"matomo.report-glossary.list", "lr-one", `{"idSite":1,"section":"reports"}`},
		{"matomo.report-glossary.list", "lr-two", `{"section":"reports"}`},
		{"matomo.multi-sites.get", "lr-instance", `{"view":"sites","period":"day","date":"today"}`},
		{"matomo.reports.evolution", "lr-open", `{"idSite":1,"idSites":"all","report":"x","period":"day","date":"last3","label":["/a"]}`},
	} {
		_, err := env.invoke(tc.tool, tc.connection, tc.arguments)
		if err == nil {
			t.Errorf("%s %s %s: accepted", tc.tool, tc.connection, tc.arguments)
		} else if strings.Contains(err.Error(), "site/") || strings.Contains(err.Error(), "idSite 1") {
			t.Errorf("%s: error names a target: %v", tc.tool, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("requests = %d, secret reads = %d, want none", len(calls), *env.reads)
	}
}

func TestListedReadsSendBearerHeaderAndNoForbiddenParameters(t *testing.T) {
	var calls []call
	for _, c := range listedCases {
		env := newListedEnvironment(t, &calls, listedAnswers(c.method, fixed(`[]`)))
		_, _ = env.invoke(c.tool, "lr-open", c.arguments)
	}
	for _, got := range calls {
		if got.method != http.MethodPost || got.auth != "Bearer "+tokenValue || got.rawQuery != "" ||
			strings.Contains(got.form.Encode(), tokenValue) || got.form.Get("format") != "JSON" || got.form.Get("module") != "API" {
			t.Errorf("request = %+v", got)
		}
		for _, name := range []string{"callback", "jsoncallback", "disable_generic_filters", "disable_queued_filters",
			"disable_root_datatable_post_processor", "token_auth"} {
			if got.form.Has(name) {
				t.Errorf("%s carries %s", got.api(), name)
			}
		}
		if got.form.Get("filter_limit") == "-1" {
			t.Errorf("%s carries filter_limit=-1", got.api())
		}
	}
}

// --- distinct counts

func TestDistinctCountsGetEveryKind(t *testing.T) {
	methods := map[string]string{
		"search-engines": "Referrers.getNumberOfDistinctSearchEngines", "social-networks": "Referrers.getNumberOfDistinctSocialNetworks",
		"keywords": "Referrers.getNumberOfDistinctKeywords", "campaigns": "Referrers.getNumberOfDistinctCampaigns",
		"websites": "Referrers.getNumberOfDistinctWebsites", "ai-assistants": "Referrers.getNumberOfDistinctAIAssistants",
		"website-urls": "Referrers.getNumberOfDistinctWebsitesUrls", "countries": "UserCountry.getNumberOfDistinctCountries",
	}
	for kind, method := range methods {
		var calls []call
		env := newListedEnvironment(t, &calls, listedAnswers(method, fixed(`{"value":42}`)))
		result, err := env.invoke("matomo.distinct-counts.get", "lr-open",
			`{"idSite":1,"kind":"`+kind+`","period":"month","date":"2026-09-01","segment":"countryCode==de"}`)
		want := `{"idSite":1,"kind":"` + kind + `","period":"month","date":"2026-09-01","value":42}`
		if err != nil || !sameJSON(result, want) {
			t.Errorf("%s: result = %s, err = %v", kind, result, err)
		}
		sent := lastCall(calls, method).form
		if sent.Get("idSite") != "1" || sent.Get("period") != "month" || sent.Get("date") != "2026-09-01" ||
			sent.Get("segment") != "countryCode==de" {
			t.Errorf("%s: form = %v", kind, sent)
		}
	}
}

func TestDistinctCountsGetValueForms(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"bare number":    {`7`, `"value":7`},
		"numeric string": {`"12"`, `"value":12`},
		"wrapped":        {`{"value":0}`, `"value":0`},
	} {
		var calls []call
		env := newListedEnvironment(t, &calls, listedAnswers("Referrers.getNumberOfDistinctKeywords", fixed(tc.body)))
		result, err := env.invoke("matomo.distinct-counts.get", "lr-open", `{"idSite":1,"kind":"keywords","period":"day","date":"today"}`)
		if err != nil || !strings.Contains(result, tc.want) {
			t.Errorf("%s: result = %s, err = %v", name, result, err)
		}
	}
	for name, body := range map[string]string{"empty": `[]`, "text": `"many"`, "list": `[1,2]`, "null": `null`} {
		var calls []call
		env := newListedEnvironment(t, &calls, listedAnswers("Referrers.getNumberOfDistinctKeywords", fixed(body)))
		_, err := env.invoke("matomo.distinct-counts.get", "lr-open", `{"idSite":1,"kind":"keywords","period":"day","date":"today"}`)
		if classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s: err = %v, want invalid response", name, err)
		}
	}
}

func TestDistinctCountsGetOverSeveralPeriods(t *testing.T) {
	var calls []call
	env := newListedEnvironment(t, &calls, listedAnswers("UserCountry.getNumberOfDistinctCountries",
		fixed(`{"2026-09-01":5,"2026-09-02":{"value":"6"},"2026-09-03":0}`)))
	result, err := env.invoke("matomo.distinct-counts.get", "lr-open", `{"idSite":1,"kind":"countries","period":"day","date":"last3"}`)
	want := `{"idSite":1,"kind":"countries","period":"day","date":"last3","values":[{"period":"2026-09-01","value":5},` +
		`{"period":"2026-09-02","value":6},{"period":"2026-09-03","value":0}]}`
	if err != nil || !sameJSON(result, want) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	// Period range sums the dates into one number.
	env = newListedEnvironment(t, &calls, listedAnswers("UserCountry.getNumberOfDistinctCountries", fixed(`9`)))
	result, err = env.invoke("matomo.distinct-counts.get", "lr-open", `{"idSite":1,"kind":"countries","period":"range","date":"2026-09-01,2026-09-30"}`)
	if err != nil || !strings.Contains(result, `"value":9`) {
		t.Errorf("range: result = %s, err = %v", result, err)
	}
}

func TestDistinctCountsGetValidatesArguments(t *testing.T) {
	var calls []call
	env := newListedEnvironment(t, &calls, answers{})
	long := strings.Repeat("a", 33)
	for name, arguments := range map[string]string{
		"unknown kind":     `{"idSite":1,"kind":"visitors","period":"day","date":"today"}`,
		"no kind":          `{"idSite":1,"period":"day","date":"today"}`,
		"bad period":       `{"idSite":1,"kind":"keywords","period":"hour","date":"today"}`,
		"bad date":         `{"idSite":1,"kind":"keywords","period":"day","date":"` + long + `"}`,
		"range needs span": `{"idSite":1,"kind":"keywords","period":"range","date":"today"}`,
		"last0":            `{"idSite":1,"kind":"keywords","period":"day","date":"last0"}`,
		"too many periods": `{"idSite":1,"kind":"keywords","period":"day","date":"last1001"}`,
		"bad segment":      `{"idSite":1,"kind":"keywords","period":"day","date":"today","segment":"x"}`,
		"all sites":        `{"idSite":"all","kind":"keywords","period":"day","date":"today"}`,
	} {
		if _, err := env.invoke("matomo.distinct-counts.get", "lr-open", arguments); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("invalid arguments caused %d requests and %d secret reads", len(calls), *env.reads)
	}
}

// --- multi sites

const multiSitesRows = `[
{"label":"site1","nb_visits":10,"nb_actions":30,"nb_pageviews":25,"hits":28,"revenue":0,"visits_evolution":"+5%",
 "idsite":1,"group":"shops","main_url":"https://site1.example.test"},
{"label":"site2","nb_visits":4,"nb_actions":9,"idsite":2,"group":"","main_url":"https://site2.example.test"},
{"label":"site3","nb_visits":99,"nb_actions":99,"idsite":"3","group":"shops","main_url":"https://site3.example.test"}]`

func TestMultiSitesGetAllFiltersRowsOutsideTheTargets(t *testing.T) {
	for connection, want := range map[string][]string{"lr-open": {"site1", "site2", "site3"}, "lr-general": {"site1", "site2", "site3"},
		"lr-one": {"site2"}, "lr-two": {"site1", "site2"}} {
		var calls []call
		env := newListedEnvironment(t, &calls, listedAnswers("MultiSites.getAll", fixed(multiSitesRows)))
		result, err := env.invoke("matomo.multi-sites.get", connection,
			`{"view":"sites","period":"month","date":"2026-09-01","enhanced":true,"pattern":" site ","columns":["nb_visits","nb_actions"],`+
				`"sort_column":"nb_visits","sort_order":"desc","segment":"countryCode==de"}`)
		if err != nil {
			t.Fatalf("%s: %v", connection, err)
		}
		var out struct {
			Rows    []map[string]any `json:"rows"`
			HasMore bool             `json:"has_more"`
		}
		_ = json.Unmarshal([]byte(result), &out)
		var labels []string
		for _, row := range out.Rows {
			labels = append(labels, row["label"].(string))
		}
		if !slices.Equal(labels, want) || out.HasMore {
			t.Errorf("%s: labels = %v, has_more = %t", connection, labels, out.HasMore)
		}
		sent := lastCall(calls, "MultiSites.getAll").form
		if sent.Get("enhanced") != "1" || sent.Get("pattern") != "site" || sent.Get("showColumns") != "nb_visits,nb_actions" ||
			sent.Get("filter_sort_column") != "nb_visits" || sent.Get("filter_sort_order") != "desc" ||
			sent.Get("filter_limit") != "26" || sent.Get("filter_offset") != "0" || sent.Get("format_metrics") != "0" ||
			sent.Get("segment") != "countryCode==de" || sent.Has("idSite") {
			t.Errorf("%s: form = %v", connection, sent)
		}
		if strings.Contains(result, "site3") && connection != "lr-open" && connection != "lr-general" {
			t.Errorf("%s: row outside the targets in %s", connection, result)
		}
	}
}

func TestMultiSitesGetAllPagesOverTheRowsMatomoReturned(t *testing.T) {
	rows := func(c call) string {
		offset, count := windowOf(c, 5)
		parts := make([]string, count)
		for i := range parts {
			parts[i] = `{"label":"s` + strconv.Itoa(offset+i+1) + `","idsite":` + strconv.Itoa(offset+i+1) + `}`
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	var calls []call
	env := newListedEnvironment(t, &calls, listedAnswers("MultiSites.getAll", func(c call) *http.Response { return ok(rows(c)) }))
	// Only website 2 and 4 are inside the targets: a batch holds fewer rows than limit and still continues.
	scoped := `{"view":"sites","period":"day","date":"today","limit":2`
	first, err := env.invoke("matomo.multi-sites.get", "lr-two", scoped+`}`)
	var page struct {
		Rows       []map[string]any `json:"rows"`
		HasMore    bool             `json:"has_more"`
		NextCursor string           `json:"next_cursor"`
	}
	_ = json.Unmarshal([]byte(first), &page)
	if err != nil || len(page.Rows) != 2 || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("first = %s, err = %v", first, err)
	}
	second, err := env.invoke("matomo.multi-sites.get", "lr-two", scoped+`,"cursor":"`+page.NextCursor+`"}`)
	page.Rows = nil
	_ = json.Unmarshal([]byte(second), &page)
	if err != nil || len(page.Rows) != 0 || !page.HasMore {
		t.Fatalf("second = %s, err = %v", second, err)
	}
	if sent := lastCall(calls, "MultiSites.getAll").form; sent.Get("filter_offset") != "2" || sent.Get("filter_limit") != "3" {
		t.Errorf("window = %v", sent)
	}
	if _, err := env.invoke("matomo.multi-sites.get", "lr-two", `{"view":"sites","period":"day","date":"today","cursor":"`+page.NextCursor+`"}`); err != nil {
		t.Errorf("cursor with its own arguments: %v", err)
	}
	// A cursor is bound to the targets and the arguments of its list.
	if _, err := env.invoke("matomo.multi-sites.get", "lr-open", scoped+`,"cursor":"`+page.NextCursor+`"}`); !isInvalidRequest(err) {
		t.Errorf("cursor of another connection: err = %v", err)
	}
	if _, err := env.invoke("matomo.multi-sites.get", "lr-two", `{"view":"sites","period":"day","date":"yesterday","limit":2,"cursor":"`+page.NextCursor+`"}`); !isInvalidRequest(err) {
		t.Errorf("cursor of another list: err = %v", err)
	}
}

func TestMultiSitesGetOneAndGroups(t *testing.T) {
	var calls []call
	env := newListedEnvironment(t, &calls, listedAnswers("MultiSites.getOne", fixed(`{"nb_visits":10,"nb_actions":30,"visits_evolution":"+5%"}`)))
	result, err := env.invoke("matomo.multi-sites.get", "lr-one", `{"view":"site","period":"day","date":"yesterday","enhanced":true}`)
	if err != nil || !strings.Contains(result, `"site":{"nb_actions":30,"nb_visits":10,"visits_evolution":"+5%"}`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if sent := lastCall(calls, "MultiSites.getOne").form; sent.Get("idSite") != "2" || sent.Get("enhanced") != "1" {
		t.Errorf("form = %v", sent)
	}
	const groups = `{"numSites":3,"totals":{"nb_visits":113,"previous_nb_visits":100},"lastDate":"2026-09-01","sites":[` +
		`{"label":"shops","isGroup":1,"nb_visits":109},{"label":"site1","idsite":1,"group":"shops","nb_visits":10},` +
		`{"label":"site3","idsite":3,"group":"shops","nb_visits":99},{"label":"site2","idsite":2,"group":"","nb_visits":4}]}`
	for connection, want := range map[string]struct {
		labels   []string
		numSites bool
	}{"lr-open": {[]string{"shops", "site1", "site3", "site2"}, true}, "lr-general": {[]string{"shops", "site1", "site3", "site2"}, true},
		"lr-two": {[]string{"site1", "site2"}, false}, "lr-one": {[]string{"site2"}, false}} {
		env = newListedEnvironment(t, &calls, listedAnswers("MultiSites.getAllWithGroups", fixed(groups)))
		result, err = env.invoke("matomo.multi-sites.get", connection, `{"view":"groups","period":"month","date":"2026-09-01","pattern":"s","limit":10}`)
		var out struct {
			NumSites *int           `json:"num_sites"`
			Totals   map[string]any `json:"totals"`
			LastDate string         `json:"last_date"`
			Rows     []struct {
				Label string `json:"label"`
			} `json:"rows"`
		}
		_ = json.Unmarshal([]byte(result), &out)
		var labels []string
		for _, row := range out.Rows {
			labels = append(labels, row.Label)
		}
		if err != nil || !slices.Equal(labels, want.labels) || (out.NumSites != nil) != want.numSites ||
			(out.Totals != nil) != want.numSites || out.LastDate != "2026-09-01" {
			t.Errorf("%s: result = %s, err = %v", connection, result, err)
		}
		if sent := lastCall(calls, "MultiSites.getAllWithGroups").form; sent.Get("filter_limit") != "11" ||
			sent.Get("filter_offset") != "0" || sent.Get("pattern") != "s" {
			t.Errorf("%s: form = %v", connection, sent)
		}
	}
}

func TestMultiSitesGetValidatesArguments(t *testing.T) {
	var calls []call
	env := newListedEnvironment(t, &calls, answers{})
	for name, arguments := range map[string]string{
		"no view":           `{"period":"day","date":"today"}`,
		"unknown view":      `{"view":"all","period":"day","date":"today"}`,
		"several periods":   `{"view":"sites","period":"day","date":"last7"}`,
		"range of periods":  `{"view":"sites","period":"week","date":"2026-09-01,2026-09-30"}`,
		"idSite on sites":   `{"view":"sites","idSite":1,"period":"day","date":"today"}`,
		"enhanced groups":   `{"view":"groups","enhanced":true,"period":"day","date":"today"}`,
		"pattern on site":   `{"view":"site","idSite":1,"pattern":"a","period":"day","date":"today"}`,
		"columns on groups": `{"view":"groups","columns":["nb_visits"],"period":"day","date":"today"}`,
		"limit on site":     `{"view":"site","idSite":1,"limit":5,"period":"day","date":"today"}`,
		"bad column":        `{"view":"sites","columns":["a b"],"period":"day","date":"today"}`,
		"dup column":        `{"view":"sites","columns":["nb_visits","nb_visits"],"period":"day","date":"today"}`,
		"bad sort order":    `{"view":"sites","sort_order":"up","period":"day","date":"today"}`,
		"long pattern":      `{"view":"sites","pattern":"` + strings.Repeat("a", 101) + `","period":"day","date":"today"}`,
		"limit zero page":   `{"view":"sites","limit":101,"period":"day","date":"today"}`,
	} {
		if _, err := env.invoke("matomo.multi-sites.get", "lr-open", arguments); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("invalid arguments caused %d requests and %d secret reads", len(calls), *env.reads)
	}
}

func TestMultiSitesGetBoundsProviderText(t *testing.T) {
	long := strings.Repeat("x", 5000)
	var calls []call
	env := newListedEnvironment(t, &calls, listedAnswers("MultiSites.getAll", fixed(
		`[{"label":"`+long+`","idsite":1,"main_url":"`+long+`","nested":{"a":1},"list":[1]}]`)))
	result, err := env.invoke("matomo.multi-sites.get", "lr-open", `{"view":"sites","period":"day","date":"today"}`)
	if err != nil || len(result) > 2000 || strings.Contains(result, "nested") {
		t.Errorf("len = %d, err = %v", len(result), err)
	}
}

// --- glossary

func TestReportGlossaryListEachSection(t *testing.T) {
	var calls []call
	env := newListedEnvironment(t, &calls, listedAnswers("API.getGlossaryReports", fixed(
		`[{"name":"Page URLs (Behaviour)","documentation":"Pages visited.","onlineGuideUrl":"https://matomo.org/guide"},`+
			`{"name":"Channel Type (Acquisition)","documentation":"How visitors arrived."},{"name":"","documentation":"x"}]`)))
	result, err := env.invoke("matomo.report-glossary.list", "lr-open", `{"idSite":1,"section":"reports","language":"de"}`)
	want := `{"idSite":1,"section":"reports","reports":[{"name":"Page URLs (Behaviour)","documentation":"Pages visited.",` +
		`"online_guide_url":"https://matomo.org/guide"},{"name":"Channel Type (Acquisition)","documentation":"How visitors arrived."}],"has_more":false}`
	if err != nil || !sameJSON(result, want) {
		t.Fatalf("reports: result = %s, err = %v", result, err)
	}
	if sent := lastCall(calls, "API.getGlossaryReports").form; sent.Get("idSite") != "1" || sent.Get("language") != "de" {
		t.Errorf("form = %v", sent)
	}
	env = newListedEnvironment(t, &calls, listedAnswers("API.getGlossaryMetrics", fixed(
		`[{"name":"Bounce Rate","id":"bounce_rate","documentation":"Share of single page visits."},`+
			`{"name":"Visits","id":"nb_visits","documentation":"Number of visits."}]`)))
	result, err = env.invoke("matomo.report-glossary.list", "lr-open", `{"idSite":1,"section":"metrics"}`)
	want = `{"idSite":1,"section":"metrics","metrics":[{"id":"bounce_rate","name":"Bounce Rate","documentation":"Share of single page visits."},` +
		`{"id":"nb_visits","name":"Visits","documentation":"Number of visits."}],"has_more":false}`
	if err != nil || !sameJSON(result, want) {
		t.Fatalf("metrics: result = %s, err = %v", result, err)
	}
}

func TestReportGlossaryListPagesAndBoundsText(t *testing.T) {
	entries := make([]string, 7)
	for i := range entries {
		entries[i] = `{"name":"M` + strconv.Itoa(i) + `","id":"m` + strconv.Itoa(i) + `","documentation":"` + strings.Repeat("d", 5000) + `"}`
	}
	var calls []call
	env := newListedEnvironment(t, &calls, listedAnswers("API.getGlossaryMetrics", fixed("["+strings.Join(entries, ",")+"]")))
	var ids []string
	cursor := ""
	for range 5 {
		arguments := `{"idSite":1,"section":"metrics","limit":3`
		if cursor != "" {
			arguments += `,"cursor":"` + cursor + `"`
		}
		result, err := env.invoke("matomo.report-glossary.list", "lr-open", arguments+`}`)
		var page GlossaryResult
		if err != nil || json.Unmarshal([]byte(result), &page) != nil {
			t.Fatalf("result = %.200s, err = %v", result, err)
		}
		for _, metric := range page.Metrics {
			ids = append(ids, metric.ID)
			if len(metric.Documentation) > maxReportText {
				t.Errorf("documentation of %s is %d bytes", metric.ID, len(metric.Documentation))
			}
		}
		if cursor = page.NextCursor; !page.HasMore {
			break
		}
	}
	if len(ids) != 7 || ids[0] != "m0" || ids[6] != "m6" {
		t.Errorf("ids = %v", ids)
	}
}

func TestReportGlossaryListValidatesArguments(t *testing.T) {
	var calls []call
	env := newListedEnvironment(t, &calls, answers{})
	for name, arguments := range map[string]string{
		"no section":      `{"idSite":1}`,
		"unknown section": `{"idSite":1,"section":"goals"}`,
		"bad language":    `{"idSite":1,"section":"reports","language":"de; drop"}`,
		"bad cursor":      `{"idSite":1,"section":"reports","cursor":"abc"}`,
		"limit":           `{"idSite":1,"section":"reports","limit":101}`,
	} {
		if _, err := env.invoke("matomo.report-glossary.list", "lr-open", arguments); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("invalid arguments caused %d requests and %d secret reads", len(calls), *env.reads)
	}
}
