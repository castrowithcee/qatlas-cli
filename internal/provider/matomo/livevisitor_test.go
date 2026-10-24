package matomo

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const profileAnswer = `{"visitorId":"0123456789abcdef","hasMoreVisits":false,"nextVisitorId":"aaaaaaaaaaaaaaaa","previousVisitorId":false,` +
	`"totalVisits":3,"totalVisitDuration":300,"totalVisitDurationPretty":"5 min","userId":"customer-7","visitsAggregated":3,` +
	`"firstVisit":{"date":1761000000,"prettyDate":"Oct 21","daysAgo":3,"referrerType":"search","referrerUrl":"https://search.example.test/?q=x","referralSummary":"Search (x)"},` +
	`"lastVisit":{"date":1761300000,"prettyDate":"Oct 24","daysAgo":0,"referrerType":"direct","referrerUrl":"","referralSummary":"Direct"},` +
	`"totalActions":9,"totalOutlinks":1,"totalDownloads":2,"totalSearches":1,"totalPageViews":5,"totalUniquePageViews":4,` +
	`"totalRevisitedPages":1,"totalPageViewsWithTiming":2,"totalPageViewsWithLoadTime":2,"averagePageGenerationTime":0.125,` +
	`"averagePageLoadTime":1.5,"totalEvents":2,"totalGoalConversions":1,"totalConversionsByGoal":{"idgoal=3":1},` +
	`"totalRevenueByGoal":{"idgoal=3":12.5},"totalEcommerceRevenue":"20","totalEcommerceConversions":1,"totalEcommerceItems":2,` +
	`"totalAbandonedCartsRevenue":"0","totalAbandonedCarts":0,"totalAbandonedCartsItems":0,` +
	`"searches":[{"keyword":"cup","searches":2}],"visitedPages":[{"url":"https://shop.example.test/a","count":3}],` +
	`"devices":[{"type":"Smartphone","count":3,"icon":"x.png","devices":[{"name":"Acme A1","count":3}]}],` +
	`"continents":[{"continent":"eur","nb_visits":3,"prettyName":"Europe"}],` +
	`"countries":[{"country":"de","nb_visits":3,"flag":"f.png","prettyName":"Germany","cities":["Berlin"]}],` +
	`"hasLatLong":true,"customDimensions":{"visit":[{"name":"Plan","values":[{"value":"gold","count":3}]}],` +
	`"action":[{"name":"Variant","values":[{"value":"b","count":1}]}]},` +
	`"lastVisits":[` + `VISITS` + `]}`

func profileServer(t *testing.T, enabled string, profile string) (*environment, *[]call) {
	t.Helper()
	noLiveSpacing(t)
	calls := &[]call{}
	env := newEnvironment(t, calls, with(versionOnly("5.14.2"), answers{
		"Live.isVisitorProfileEnabled": func(call) *http.Response { return ok(enabled) },
		"Live.getVisitorProfile":       func(call) *http.Response { return ok(profile) },
		"Live.getMostRecentVisitorId":  func(call) *http.Response { return ok(`{"value":"0123456789abcdef"}`) },
		"Live.getFirstVisitForVisitorId": func(call) *http.Response {
			return ok("[" + visitRow(1, "1", false) + "," + visitRow(2, "7", false) + "]")
		},
	}).handler(t))
	return env, calls
}

func TestVisitorsGetProfileReturnsEveryProfileField(t *testing.T) {
	visits := visitRow(1, "1", true) + "," + visitRow(2, "7", true)
	env, calls := profileServer(t, `{"value":true}`, strings.Replace(profileAnswer, "VISITS", visits, 1))
	result, err := env.invoke("matomo.visitors.get-profile", "open", `{"idSite":1,"visitor_id":"0123456789abcdef","segment":"country==de","limit_visits":5}`)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Found   bool           `json:"found"`
		Profile map[string]any `json:"profile"`
	}
	if json.Unmarshal([]byte(result), &out) != nil || !out.Found {
		t.Fatalf("result = %s", result)
	}
	p := out.Profile
	for _, key := range []string{"visitor_id", "next_visitor_id", "total_visits", "total_visit_duration", "user_id", "visits_aggregated",
		"first_visit", "last_visit", "total_actions", "total_outlinks", "total_downloads", "total_searches", "total_page_views",
		"total_unique_page_views", "total_revisited_pages", "total_page_views_with_timing", "total_page_views_with_load_time",
		"average_page_generation_time", "average_page_load_time", "total_events", "total_goal_conversions", "conversions_by_goal",
		"revenue_by_goal", "total_ecommerce_revenue", "total_ecommerce_conversions", "total_ecommerce_items", "total_abandoned_carts",
		"searches", "visited_pages", "devices", "continents", "countries", "has_lat_long", "custom_dimensions", "last_visits", "has_more_visits"} {
		if _, found := p[key]; !found {
			t.Errorf("profile lacks %s: %s", key, result)
		}
	}
	for _, key := range []string{"previous_visitor_id", "total_visit_duration_pretty"} {
		if _, found := p[key]; found {
			t.Errorf("profile holds %s", key)
		}
	}
	if strings.Contains(result, "x.png") || strings.Contains(result, "f.png") || strings.Contains(result, "prettyDate") {
		t.Errorf("presentation fields leak: %s", result)
	}
	visitsOut := p["last_visits"].([]any)
	if len(visitsOut) != 1 || visitsOut[0].(map[string]any)["actions"] == nil {
		t.Errorf("last_visits = %v, want the visit of website 1 with actions only", visitsOut)
	}
	if !strings.Contains(result, `"conversions_by_goal":[{"conversions":1,"goal_id":3}]`) ||
		!strings.Contains(result, `"visits":3,"value":"gold"`) && !strings.Contains(result, `"value":"gold"`) {
		t.Errorf("result = %s", result)
	}
	form := lastLive(t, *calls, "Live.getVisitorProfile").form
	if form.Get("idSite") != "1" || form.Get("visitorId") != "0123456789abcdef" || form.Get("segment") != "country==de" || form.Get("limitVisits") != "5" {
		t.Errorf("form = %v", form)
	}
}

func TestVisitorsGetProfileDisabledIsUnsupportedWithoutFurtherRequest(t *testing.T) {
	env, calls := profileServer(t, `{"value":false}`, `[]`)
	for _, tc := range []struct{ tool, arguments string }{
		{"matomo.visitors.get-profile", `{"idSite":1}`},
		{"matomo.visits.get-first", `{"idSite":1,"visitor_id":"0123456789abcdef"}`},
	} {
		if _, err := env.invoke(tc.tool, "open", tc.arguments); !isUnsupported(err) {
			t.Errorf("%s err = %v, want unsupported", tc.tool, err)
		}
	}
	for _, got := range *calls {
		if m := got.api(); m != "API.getMatomoVersion" && m != "Live.isVisitorProfileEnabled" {
			t.Errorf("request %s after a disabled profile", m)
		}
	}
}

func TestVisitorsGetProfileUnknownVisitorAndArguments(t *testing.T) {
	env, calls := profileServer(t, `{"value":true}`, `[]`)
	result, err := env.invoke("matomo.visitors.get-profile", "open", `{"idSite":1,"visitor_id":"0123456789ABCDEF"}`)
	if err != nil || result != `{"found":false}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if form := lastLive(t, *calls, "Live.getVisitorProfile").form; form.Get("limitVisits") != "10" {
		t.Errorf("form = %v", form)
	}
	before := len(*calls)
	for _, arguments := range []string{`{"idSite":1,"visitor_id":"xyz"}`, `{"idSite":1,"visitor_id":"0123456789abcdeg"}`,
		`{"idSite":1,"visitor_id":"0123456789abcdef0"}`, `{"idSite":1,"limit_visits":0}`, `{"idSite":1,"limit_visits":21}`} {
		if _, err := env.invoke("matomo.visitors.get-profile", "open", arguments); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", arguments, err)
		}
	}
	if _, err := env.invoke("matomo.visits.get-first", "open", `{"idSite":1}`); !isInvalidRequest(err) {
		t.Errorf("missing visitor_id: err = %v", err)
	}
	if len(*calls) != before {
		t.Error("invalid arguments caused requests")
	}
}

func TestVisitorsGetProfileBoundsLists(t *testing.T) {
	pages := make([]string, 150)
	visits := make([]string, 30)
	for i := range pages {
		pages[i] = `{"url":"https://shop.example.test/p` + strings.Repeat("x", 3000) + `","count":1}`
	}
	for i := range visits {
		visits[i] = visitRow(i+1, "1", false)
	}
	answer := `{"visitorId":"0123456789abcdef","visitedPages":[` + strings.Join(pages, ",") + `],"lastVisits":[` + strings.Join(visits, ",") + `]}`
	env, _ := profileServer(t, `{"value":true}`, answer)
	result, err := env.invoke("matomo.visitors.get-profile", "open", `{"idSite":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Profile struct {
			Pages     []map[string]any `json:"visited_pages"`
			Truncated bool             `json:"visited_pages_truncated"`
			Visits    []any            `json:"last_visits"`
			VTrunc    bool             `json:"last_visits_truncated"`
		} `json:"profile"`
	}
	if json.Unmarshal([]byte(result), &out) != nil || len(out.Profile.Pages) != 100 || !out.Profile.Truncated ||
		len(out.Profile.Visits) != 20 || !out.Profile.VTrunc {
		t.Fatalf("pages = %d, visits = %d, result size %d", len(out.Profile.Pages), len(out.Profile.Visits), len(result))
	}
	if url, _ := out.Profile.Pages[0]["url"].(string); len(url) > maxUrlLength {
		t.Errorf("address of %d bytes", len(url))
	}
}

func TestVisitorsGetLatestID(t *testing.T) {
	env, calls := profileServer(t, `{"value":true}`, `[]`)
	result, err := env.invoke("matomo.visitors.get-latest-id", "open", `{"idSite":1,"segment":"country==de"}`)
	if err != nil || result != `{"found":true,"visitor_id":"0123456789abcdef"}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if form := lastLive(t, *calls, "Live.getMostRecentVisitorId").form; form.Get("segment") != "country==de" || form.Get("idSite") != "1" {
		t.Errorf("form = %v", form)
	}
	for _, answer := range []string{`{"value":false}`, `false`, `{"value":""}`} {
		env := newEnvironment(t, &[]call{}, with(versionOnly("5.14.2"), answers{
			"Live.getMostRecentVisitorId": func(call) *http.Response { return ok(answer) },
		}).handler(t))
		if result, err := env.invoke("matomo.visitors.get-latest-id", "open", `{"idSite":1}`); err != nil || result != `{"found":false}` {
			t.Errorf("%s: result = %s, err = %v", answer, result, err)
		}
	}
	env = newEnvironment(t, &[]call{}, with(versionOnly("5.14.2"), answers{
		"Live.getMostRecentVisitorId": func(call) *http.Response { return ok(`{"value":"not-an-id"}`) },
	}).handler(t))
	if _, err := env.invoke("matomo.visitors.get-latest-id", "open", `{"idSite":1}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
}

func TestVisitsGetFirstDropsOtherWebsites(t *testing.T) {
	env, calls := profileServer(t, `{"value":true}`, `[]`)
	result, err := env.invoke("matomo.visits.get-first", "open", `{"idSite":1,"visitor_id":"0123456789abcdef"}`)
	if err != nil || !strings.HasPrefix(result, `{"found":true,"visit":{`) || strings.Contains(result, `"id_visit":2`) ||
		strings.Contains(result, `"actions":[`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if form := lastLive(t, *calls, "Live.getFirstVisitForVisitorId").form; form.Get("visitorId") != "0123456789abcdef" {
		t.Errorf("form = %v", form)
	}
	env = newEnvironment(t, &[]call{}, with(versionOnly("5.14.2"), answers{
		"Live.isVisitorProfileEnabled":   func(call) *http.Response { return ok(`{"value":true}`) },
		"Live.getFirstVisitForVisitorId": func(call) *http.Response { return ok(`[` + visitRow(2, "7", false) + `]`) },
	}).handler(t))
	if result, err := env.invoke("matomo.visits.get-first", "open", `{"idSite":1,"visitor_id":"0123456789abcdef"}`); err != nil || result != `{"found":false}` {
		t.Errorf("result = %s, err = %v", result, err)
	}
}
