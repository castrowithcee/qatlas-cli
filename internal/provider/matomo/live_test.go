package matomo

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// noLiveSpacing removes the pause between Live requests, which tests do not need.
func noLiveSpacing(t *testing.T) {
	t.Helper()
	previous := liveSpacing
	liveSpacing = 0
	t.Cleanup(func() { liveSpacing = previous })
}

func visitRow(id int, site string, withActions bool) string {
	row := `{"idSite":"` + site + `","idVisit":"` + strconv.Itoa(id) + `","visitIp":"192.0.2.7",` +
		`"visitorId":"0123456789abcdef","fingerprint":"fedcba9876543210","userId":"customer-` + strconv.Itoa(id) + `",` +
		`"visitorType":"returning","visitCount":"3","visitDuration":"125","visitConverted":"1","actions":"2",` +
		`"searches":"0","interactions":"2","firstActionTimestamp":1761300000,"lastActionTimestamp":1761300125,` +
		`"lastActionDateTime":"2026-10-24 10:02:05","referrerType":"search","referrerName":"Search.example.test",` +
		`"referrerKeyword":"widgets","referrerUrl":"https://search.example.test/?q=widgets","country":"Germany",` +
		`"countryCode":"de","city":"Berlin","latitude":"52.52","longitude":"13.40","siteName":"shop",` +
		`"deviceType":"smartphone"`
	if withActions {
		row += `,"actionDetails":[{"type":"action","url":"https://shop.example.test/a","pageTitle":"Start",` +
			`"timestamp":1761300000,"timeSpent":"60","pageviewPosition":"1"},` +
			`{"type":"goal","goalName":"Order","revenue":"12.5","icon":"plugins/Morpheus/images/goal.png"}]`
	}
	return row + `}`
}

func visitRows(count int, site string, withActions bool) string {
	rows := make([]string, count)
	for i := range rows {
		rows[i] = visitRow(i+1, site, withActions)
	}
	return "[" + strings.Join(rows, ",") + "]"
}

type visitsAnswer struct {
	Visits []struct {
		ID        int    `json:"id_visit"`
		VisitorID string `json:"visitor_id"`
		UserID    string `json:"user_id"`
		IP        string `json:"visit_ip"`
		Country   string `json:"country"`
		Actions   []struct {
			Type     string `json:"type"`
			URL      string `json:"url"`
			Title    string `json:"page_title"`
			Goal     string `json:"goal_name"`
			Revenue  string `json:"revenue"`
			Position *int   `json:"pageview_position"`
		} `json:"actions"`
		Truncated bool `json:"actions_truncated"`
	} `json:"visits"`
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor"`
}

func liveServer(t *testing.T, total int, withActions bool) (*environment, *[]call) {
	t.Helper()
	noLiveSpacing(t)
	calls := &[]call{}
	env := newEnvironment(t, calls, with(versionOnly("5.14.2"), answers{
		"Live.getLastVisitsDetails": func(c call) *http.Response {
			offset, _ := strconv.Atoi(c.form.Get("filter_offset"))
			limit, _ := strconv.Atoi(c.form.Get("filter_limit"))
			n := max(0, min(limit, total-offset))
			rows := make([]string, n)
			for i := range rows {
				rows[i] = visitRow(offset+i+1, c.form.Get("idSite"), withActions && c.form.Get("doNotFetchActions") == "0")
			}
			return ok("[" + strings.Join(rows, ",") + "]")
		},
	}).handler(t))
	return env, calls
}

func visitsOf(t *testing.T, env *environment, connection, arguments string) visitsAnswer {
	t.Helper()
	result, err := env.invoke("matomo.visits.list", connection, arguments)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var out visitsAnswer
	if json.Unmarshal([]byte(result), &out) != nil {
		t.Fatalf("result = %s", result)
	}
	return out
}

func lastLive(t *testing.T, calls []call, method string) call {
	t.Helper()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].api() == method {
			return calls[i]
		}
	}
	t.Fatalf("no %s call in %d calls", method, len(calls))
	return call{}
}

func TestVisitsListDefaultsAskForNoActionsAndOneExtraRow(t *testing.T) {
	env, calls := liveServer(t, 60, true)
	out := visitsOf(t, env, "open", `{"idSite":1}`)
	form := lastLive(t, *calls, "Live.getLastVisitsDetails").form
	if form.Get("filter_limit") != "26" || form.Get("filter_offset") != "0" || form.Get("doNotFetchActions") != "1" ||
		form.Get("idSite") != "1" {
		t.Fatalf("form = %v", form)
	}
	for _, name := range []string{"flat", "enhanced", "segment", "period", "date", "minTimestamp", "countVisitorsToFetch"} {
		if form.Has(name) {
			t.Errorf("request carries %s", name)
		}
	}
	if len(out.Visits) != 25 || !out.HasMore || out.NextCursor == "" || out.Visits[0].Actions != nil {
		t.Fatalf("visits = %d, has_more = %t, cursor = %q", len(out.Visits), out.HasMore, out.NextCursor)
	}
	first := out.Visits[0]
	if first.ID != 1 || first.VisitorID != "0123456789abcdef" || first.IP != "192.0.2.7" || first.Country != "Germany" ||
		first.UserID != "customer-1" {
		t.Fatalf("visit = %+v", first)
	}

	next := visitsOf(t, env, "open", `{"idSite":1,"cursor":"`+out.NextCursor+`"}`)
	form = lastLive(t, *calls, "Live.getLastVisitsDetails").form
	if form.Get("filter_offset") != "25" || form.Get("filter_limit") != "26" || len(next.Visits) != 25 ||
		next.Visits[0].ID != 26 || !next.HasMore {
		t.Fatalf("form = %v, next = %d starting at %d", form, len(next.Visits), next.Visits[0].ID)
	}
}

func TestVisitsListEndsOnAFullLastPage(t *testing.T) {
	env, _ := liveServer(t, 10, false)
	out := visitsOf(t, env, "open", `{"idSite":1,"limit":10}`)
	if len(out.Visits) != 10 || out.HasMore || out.NextCursor != "" {
		t.Fatalf("visits = %d, has_more = %t", len(out.Visits), out.HasMore)
	}
}

func TestVisitsListLimitBounds(t *testing.T) {
	env, calls := liveServer(t, 500, false)
	if out := visitsOf(t, env, "open", `{"idSite":1,"limit":100}`); len(out.Visits) != 100 || !out.HasMore {
		t.Fatalf("visits = %d", len(out.Visits))
	}
	if lastLive(t, *calls, "Live.getLastVisitsDetails").form.Get("filter_limit") != "101" {
		t.Error("filter_limit is not limit+1")
	}
	for _, arguments := range []string{`{"idSite":1,"limit":101}`, `{"idSite":1,"limit":0}`, `{"idSite":1,"cursor":"x y"}`} {
		before := len(*calls)
		if _, err := env.invoke("matomo.visits.list", "open", arguments); err == nil || len(*calls) != before {
			t.Errorf("%s: err = %v, new calls = %d", arguments, err, len(*calls)-before)
		}
	}
}

func TestVisitsListCursorIsBoundToTheArguments(t *testing.T) {
	env, _ := liveServer(t, 60, false)
	out := visitsOf(t, env, "open", `{"idSite":1}`)
	for _, arguments := range []string{`{"idSite":2,"cursor":"` + out.NextCursor + `"}`,
		`{"idSite":1,"include_actions":true,"cursor":"` + out.NextCursor + `"}`,
		`{"idSite":1,"limit":10,"cursor":"` + out.NextCursor + `"}`} {
		if _, err := env.invoke("matomo.visits.list", "open", arguments); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want an invalid request", arguments, err)
		}
	}
}

func TestVisitsListWithActionsFlatAndEnhancedAreExplicit(t *testing.T) {
	env, calls := liveServer(t, 3, true)
	out := visitsOf(t, env, "open", `{"idSite":1,"include_actions":true,"enhanced":true,"limit":2}`)
	form := lastLive(t, *calls, "Live.getLastVisitsDetails").form
	if form.Get("doNotFetchActions") != "0" || form.Get("enhanced") != "1" || form.Has("flat") {
		t.Fatalf("form = %v", form)
	}
	actions := out.Visits[0].Actions
	if len(actions) != 2 || actions[0].URL != "https://shop.example.test/a" || actions[0].Title != "Start" ||
		actions[0].Position == nil || *actions[0].Position != 1 || actions[1].Goal != "Order" || actions[1].Revenue != "12.5" {
		t.Fatalf("actions = %+v", actions)
	}
	raw, err := env.invoke("matomo.visits.list", "open", `{"idSite":1,"include_actions":true}`)
	if err != nil || strings.Contains(raw, "Morpheus") || !strings.Contains(raw, `"device_type":"smartphone"`) {
		t.Errorf("a presentation field reached the result or the device is missing: %v", err)
	}
}

func TestVisitsListReadsFlatActionsAndCapsThem(t *testing.T) {
	noLiveSpacing(t)
	var calls []call
	flat := `{"idSite":"1","idVisit":"7","actionDetails_0_type":"action","actionDetails_0_url":"https://a.example.test/",` +
		`"actionDetails_1_type":"search","actionDetails_1_siteSearchKeyword":"q"`
	for i := 2; i < maxActions+5; i++ {
		flat += `,"actionDetails_` + strconv.Itoa(i) + `_type":"action"`
	}
	flat += `}`
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"Live.getLastVisitsDetails": func(call) *http.Response { return ok("[" + flat + "]") },
	}).handler(t))
	out := visitsOf(t, env, "open", `{"idSite":1,"include_actions":true,"flat":true}`)
	if lastLive(t, calls, "Live.getLastVisitsDetails").form.Get("flat") != "1" {
		t.Error("flat was not sent")
	}
	v := out.Visits[0]
	if len(v.Actions) != maxActions || !v.Truncated || v.Actions[0].URL != "https://a.example.test/" || v.Actions[1].Type != "search" {
		t.Fatalf("actions = %d, truncated = %t", len(v.Actions), v.Truncated)
	}
}

func TestVisitsListDropsRowsOfOtherWebsitesAndBoundsStrings(t *testing.T) {
	noLiveSpacing(t)
	var calls []call
	long := strings.Repeat("x", 5000)
	body := `[` + visitRow(1, "2", false) + `,{"idSite":"1","idVisit":"2","city":"` + long + `","referrerUrl":"` + long + `"}]`
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"Live.getLastVisitsDetails": func(call) *http.Response { return ok(body) },
	}).handler(t))
	result, err := env.invoke("matomo.visits.list", "open", `{"idSite":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Visits []struct {
			ID   int    `json:"id_visit"`
			City string `json:"city"`
			URL  string `json:"referrer_url"`
		} `json:"visits"`
	}
	if json.Unmarshal([]byte(result), &out) != nil || len(out.Visits) != 1 || out.Visits[0].ID != 2 ||
		len(out.Visits[0].City) != maxStringLength || len(out.Visits[0].URL) != maxUrlLength {
		t.Fatalf("result = %.200s", result)
	}
}

func TestVisitsListSendsPeriodSegmentAndMinTimestamp(t *testing.T) {
	env, calls := liveServer(t, 1, false)
	visitsOf(t, env, "open", `{"idSite":1,"period":"range","date":"2026-10-01,2026-10-24",`+
		`"segment":"referrerName==example.test","min_timestamp":1761300000}`)
	form := lastLive(t, *calls, "Live.getLastVisitsDetails").form
	if form.Get("period") != "range" || form.Get("date") != "2026-10-01,2026-10-24" ||
		form.Get("segment") != "referrerName==example.test" || form.Get("minTimestamp") != "1761300000" {
		t.Fatalf("form = %v", form)
	}
	before := len(*calls)
	for _, arguments := range []string{`{"idSite":1,"period":"day"}`, `{"idSite":1,"date":"today"}`,
		`{"idSite":1,"period":"decade","date":"today"}`, `{"idSite":1,"period":"day","date":"to day"}`,
		`{"idSite":1,"segment":"` + strings.Repeat("a", maxSegment+1) + `"}`, `{"idSite":1,"min_timestamp":-1}`} {
		if _, err := env.invoke("matomo.visits.list", "open", arguments); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want an invalid request", arguments, err)
		}
	}
	if len(*calls) != before {
		t.Error("an invalid argument reached Matomo")
	}
}

func TestLiveToolsRefuseForeignWebsitesBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, versionOnly("5.14.2").handler(t))
	for _, tc := range []struct{ tool, connection, arguments string }{
		{"matomo.visits.list", "one", `{"idSite":1}`},
		{"matomo.visits.list", "two", `{"idSite":3}`},
		{"matomo.visits.list", "two", `{}`},
		{"matomo.visits.list", "instance", `{"idSite":1}`},
		{"matomo.live-counters.get", "one", `{"idSite":1,"last_minutes":5}`},
		{"matomo.live-counters.get", "mixed", `{"idSite":9,"last_minutes":5}`},
		{"matomo.visits.get-latest-time", "one", `{"idSites":[1]}`},
		{"matomo.visits.get-latest-time", "two", `{"idSites":[1,3]}`},
		{"matomo.visits.get-latest-time", "open", `{}`},
		{"matomo.visits.get-latest-time", "general", `{}`},
		{"matomo.visitors.get-profile", "one", `{"idSite":1}`},
		{"matomo.visitors.get-profile", "two", `{"idSite":3,"visitor_id":"0123456789abcdef"}`},
		{"matomo.visitors.get-latest-id", "two", `{"idSite":3}`},
		{"matomo.visitors.get-latest-id", "instance", `{"idSite":1}`},
		{"matomo.visits.get-first", "two", `{"idSite":3,"visitor_id":"0123456789abcdef"}`},
		{"matomo.visits.get-first", "mixed", `{"idSite":9,"visitor_id":"0123456789abcdef"}`},
	} {
		_, err := env.invoke(tc.tool, tc.connection, tc.arguments)
		if err == nil || !isInvalidRequest(err) && !isUnsupported(err) {
			t.Errorf("%s %s %s: err = %v, want a refusal", tc.tool, tc.connection, tc.arguments, err)
			continue
		}
		for _, id := range []string{"1", "3", "9"} {
			if strings.Contains(err.Error(), "site "+id) || strings.Contains(err.Error(), "site/"+id) {
				t.Errorf("refusal names a target: %v", err)
			}
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("secret reads = %d, requests = %d, want none", *env.reads, len(calls))
	}
}

func TestLiveCountersGet(t *testing.T) {
	noLiveSpacing(t)
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"Live.getCounters": func(call) *http.Response {
			return ok(`[{"visits":12,"actions":40,"visitors":9,"visitsConverted":2}]`)
		},
	}).handler(t))
	result, err := env.invoke("matomo.live-counters.get", "open",
		`{"idSite":3,"last_minutes":30,"segment":"countryCode==de","hide_columns":["actions"]}`)
	if err != nil || result != `{"actions":40,"visitors":9,"visits":12,"visits_converted":2}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	form := calls[len(calls)-1].form
	if form.Get("idSite") != "3" || form.Get("lastMinutes") != "30" || form.Get("segment") != "countryCode==de" ||
		form.Get("hideColumns") != "actions" || form.Has("showColumns") {
		t.Fatalf("form = %v", form)
	}
	for _, arguments := range []string{`{"idSite":3}`, `{"idSite":3,"last_minutes":0}`, `{"idSite":3,"last_minutes":2881}`,
		`{"idSite":3,"last_minutes":5,"show_columns":["ip"]}`} {
		if _, err := env.invoke("matomo.live-counters.get", "open", arguments); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v, want an invalid request", arguments, err)
		}
	}
}

func TestVisitsGetLatestTimeReadsEachWebsiteInsideTheTargets(t *testing.T) {
	noLiveSpacing(t)
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"Live.getMostRecentVisitsDateTime": func(c call) *http.Response {
			if c.form.Get("idSite") == "2" {
				return ok(`{"value":""}`)
			}
			return ok(`{"value":"2026-10-24 10:02:05"}`)
		},
	}).handler(t))
	result, err := env.invoke("matomo.visits.get-latest-time", "two", `{}`)
	if err != nil || result != `{"sites":[{"id":1,"latest_visit_time":"2026-10-24 10:02:05"},{"id":2}]}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if result, err = env.invoke("matomo.visits.get-latest-time", "general", `{"idSites":[5],"period":"day","date":"today"}`); err != nil ||
		!strings.HasPrefix(result, `{"sites":[{"id":5,`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	form := calls[len(calls)-1].form
	if form.Get("period") != "day" || form.Get("date") != "today" || form.Get("idSite") != "5" {
		t.Fatalf("form = %v", form)
	}
}

func TestLiveToolsMapErrorsAndNeverLeakTheMessage(t *testing.T) {
	noLiveSpacing(t)
	for _, tool := range []struct{ id, arguments string }{
		{"matomo.visits.list", `{"idSite":1}`}, {"matomo.live-counters.get", `{"idSite":1,"last_minutes":5}`},
		{"matomo.visits.get-latest-time", `{"idSites":[1]}`},
		{"matomo.visitors.get-profile", `{"idSite":1,"visitor_id":"0123456789abcdef"}`},
		{"matomo.visitors.get-latest-id", `{"idSite":1}`},
		{"matomo.visits.get-first", `{"idSite":1,"visitor_id":"0123456789abcdef"}`},
	} {
		for name, tc := range map[string]struct {
			message string
			class   provider.Class
		}{
			"permission": {permissionMsg, provider.ClassPermission},
			"not found":  {"An unexpected website was found in the request.", provider.ClassNotFound},
			"other":      {"Something went wrong", provider.ClassInvalidResponse},
		} {
			var calls []call
			env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
				if c.api() == "API.getMatomoVersion" {
					return ok(versionAnswer), nil
				}
				return failure(tc.message + bodyCanary), nil
			})
			_, err := env.invoke(tool.id, "open", tool.arguments)
			if err == nil || classOf(err) != tc.class || strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("%s %s: err = %v, want class %s", tool.id, name, err, tc.class)
			}
		}
		for name, status := range map[string]int{"rate limit": 429, "unauthorized": 401} {
			var calls []call
			env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
				if c.api() == "API.getMatomoVersion" {
					return ok(versionAnswer), nil
				}
				return respond(status, ""), nil
			})
			want := map[int]provider.Class{429: provider.ClassRateLimited, 401: provider.ClassAuth}[status]
			if _, err := env.invoke(tool.id, "open", tool.arguments); classOf(err) != want {
				t.Errorf("%s %s: err = %v", tool.id, name, err)
			}
		}
		var calls []call
		env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
			if c.api() == "API.getMatomoVersion" {
				return ok(versionAnswer), nil
			}
			return ok(`<html>` + bodyCanary + `</html>`), nil
		})
		if _, err := env.invoke(tool.id, "open", tool.arguments); classOf(err) != provider.ClassInvalidResponse ||
			strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s html: err = %v", tool.id, err)
		}
		env = newEnvironment(t, &calls, func(c call) (*http.Response, error) {
			if c.api() == "API.getMatomoVersion" {
				return ok(versionAnswer), nil
			}
			return nil, context.DeadlineExceeded
		})
		if _, err := env.invoke(tool.id, "open", tool.arguments); classOf(err) != provider.ClassTimeout {
			t.Errorf("%s timeout: err = %v", tool.id, err)
		}
	}
}

func TestLiveToolsSendTheBearerTokenOnly(t *testing.T) {
	env, calls := liveServer(t, 1, false)
	visitsOf(t, env, "open", `{"idSite":1}`)
	for _, got := range *calls {
		if got.auth != "Bearer "+tokenValue || strings.Contains(got.rawQuery, tokenValue) || got.form.Has("token_auth") ||
			got.form.Has("callback") || got.form.Has("jsoncallback") || got.form.Get("filter_limit") == "-1" {
			t.Errorf("request = %+v", got)
		}
	}
	if got := env.red.Apply("Bearer " + tokenValue); strings.Contains(got, tokenValue) {
		t.Error("the redactor leaves the bearer header")
	}
}

func TestLiveRequestsOfOneConnectionRunOneAtATime(t *testing.T) {
	noLiveSpacing(t)
	var running, overlaps, seen atomic.Int32
	var calls []call
	var mu sync.Mutex
	env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
		if c.api() == "API.getMatomoVersion" {
			return ok(versionAnswer), nil
		}
		if running.Add(1) > 1 {
			overlaps.Add(1)
		}
		seen.Add(1)
		time.Sleep(15 * time.Millisecond)
		running.Add(-1)
		mu.Lock()
		defer mu.Unlock()
		return ok(`[]`), nil
	})
	// The test environment is not safe for concurrent calls through the core, so the handlers run directly.
	resolved := mustResolve(t, env, "open")
	secrets := resolver(env.red, nil)
	var wg sync.WaitGroup
	for i := range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%2 == 1 {
				_, _ = invokeLiveCountersGet(context.Background(), resolved, secrets, env.red,
					json.RawMessage(`{"idSite":1,"last_minutes":5}`))
				return
			}
			_, _ = invokeVisitsList(context.Background(), resolved, secrets, env.red, json.RawMessage(`{"idSite":1}`))
		}()
	}
	wg.Wait()
	// The counters answer [] and fail as an invalid response, which is fine here: only overlap counts.
	if seen.Load() != 6 || overlaps.Load() != 0 {
		t.Fatalf("live requests = %d, overlapping = %d", seen.Load(), overlaps.Load())
	}
}

func TestLiveRequestsKeepASpacingBelowTheCloudLimit(t *testing.T) {
	previous := liveSpacing
	liveSpacing = 40 * time.Millisecond
	t.Cleanup(func() { liveSpacing = previous })
	var stamps []time.Time
	var calls []call
	env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
		if c.api() == "API.getMatomoVersion" {
			return ok(versionAnswer), nil
		}
		stamps = append(stamps, time.Now())
		return ok(`[]`), nil
	})
	for range 3 {
		if _, err := env.invoke("matomo.visits.list", "open", `{"idSite":1}`); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i < len(stamps); i++ {
		if gap := stamps[i].Sub(stamps[i-1]); gap < 35*time.Millisecond {
			t.Errorf("gap %d = %s", i, gap)
		}
	}
}

func TestLiveToolsAreClassifiedAsPersonalData(t *testing.T) {
	reg := registry(t)
	for _, id := range []string{"matomo.visits.list", "matomo.live-counters.get", "matomo.visits.get-latest-time",
		"matomo.visitors.get-profile", "matomo.visitors.get-latest-id", "matomo.visits.get-first"} {
		descriptor, _, found := reg.Lookup(id)
		if !found {
			t.Fatalf("%s is not registered", id)
		}
		risk := descriptor.Risk
		if risk.DataSensitivity != liveSensitivity || !risk.OpenWorld || string(risk.Effect) != "read" {
			t.Errorf("%s risk = %+v", id, risk)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	recommended, _ := metadata.RecommendedProfile()
	if strings.Contains(strings.Join(recommended.Tools, " "), "live") || strings.Contains(strings.Join(recommended.Tools, " "), "visits") {
		t.Errorf("the recommended profile holds live tools: %v", recommended.Tools)
	}
}

const pluginVisit = `{"idSite":"1","idVisit":"9","visitIp":"192.0.2.9","visitorId":"abcdef0123456789","fingerprint":false,` +
	`"deviceType":"Smartphone","deviceBrand":"Acme","deviceModel":"A1","operatingSystem":"Android 14",` +
	`"operatingSystemName":"Android","operatingSystemCode":"AND","operatingSystemVersion":"14",` +
	`"operatingSystemIcon":"plugins/Morpheus/icons/os.png","browserFamily":"Blink","browserFamilyDescription":"Blink engine",` +
	`"browser":"Chrome 120","browserName":"Chrome","browserCode":"CH","browserVersion":"120","browserIcon":"x.png",` +
	`"plugins":"pdf, flash","pluginsIcons":[{"pluginIcon":"p.png","pluginName":"pdf"}],"resolution":"390x844",` +
	`"languageCode":"de","language":"German","visitLocalTime":"12:30:00","visitLocalHour":"12","events":"2",` +
	`"daysSinceLastVisit":4,"secondsSinceLastVisit":345600,"goalConversions":1,"serverDate":"2026-10-24",` +
	`"visitServerHour":"10","siteCurrency":"EUR","siteCurrencySymbol":"EUR","totalEcommerceRevenue":"99.5",` +
	`"totalEcommerceConversions":2,"totalEcommerceItems":5,"totalAbandonedCarts":1,"referrerKeywordPosition":false,` +
	`"referrerSocialNetworkUrl":"https://social.example.test","continentCode":"eur","regionCode":"BE",` +
	`"location":"Berlin, Germany","dimension1":"gold","dimension2":"de-DE","visitDurationPretty":"2 min",` +
	`"actionDetails":[` +
	`{"type":"event","eventCategory":"video","eventAction":"play","eventName":"intro","eventValue":3.5,"timestamp":1761300001},` +
	`{"type":"content","contentName":"banner","contentPiece":"img.png","contentTarget":"https://shop.example.test/x","contentInteraction":"click"},` +
	`{"type":"ecommerceOrder","orderId":"A-1","revenue":"20","revenueSubTotal":"15","revenueTax":"3","revenueShipping":"2",` +
	`"revenueDiscount":"0","items":2,"referrerType":"direct","itemDetails":[{"itemSKU":"S1","itemName":"Cup",` +
	`"itemCategory":"Home","itemCategory2":"Kitchen","price":7.5,"quantity":2}]},` +
	`{"type":"goal","goalId":"3","goalName":"Signup","goalPageId":"11","revenue":"0","referrerName":"Mail.example.test"},` +
	`{"type":"action","url":"https://shop.example.test/p","productViewName":"Cup","productViewSku":"S1",` +
	`"productViewPrice":"7.5","productViewCategories":["Home","Kitchen"],"dimension3":"variant-b",` +
	`"pageIdAction":"5","idpageview":"abc123","pageId":"77","timeSpentRef":"12"}]}`

func TestVisitsListReturnsThePluginFieldsOfVisitsAndActions(t *testing.T) {
	noLiveSpacing(t)
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"Live.getLastVisitsDetails": func(call) *http.Response { return ok("[" + pluginVisit + "]") },
	}).handler(t))
	result, err := env.invoke("matomo.visits.list", "open", `{"idSite":1,"include_actions":true}`)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Visits []map[string]any `json:"visits"`
	}
	if json.Unmarshal([]byte(result), &out) != nil || len(out.Visits) != 1 {
		t.Fatalf("result = %.300s", result)
	}
	visit := out.Visits[0]
	for key, want := range map[string]any{"device_type": "Smartphone", "device_brand": "Acme", "operating_system_name": "Android",
		"browser_name": "Chrome", "browser_version": "120", "browser_plugins": "pdf, flash", "resolution": "390x844",
		"language_code": "de", "visit_local_time": "12:30:00", "visit_local_hour": float64(12), "event_count": float64(2),
		"days_since_last_visit": float64(4), "goal_conversions": float64(1), "visit_server_hour": float64(10),
		"total_ecommerce_revenue": "99.5", "total_ecommerce_conversions": float64(2), "continent_code": "eur",
		"region_code": "BE", "location": "Berlin, Germany", "referrer_social_network_url": "https://social.example.test"} {
		if visit[key] != want {
			t.Errorf("visit[%s] = %v, want %v", key, visit[key], want)
		}
	}
	for _, absent := range []string{"fingerprint", "referrer_keyword_position", "visit_duration_pretty", "operatingSystemIcon"} {
		if _, found := visit[absent]; found {
			t.Errorf("visit holds %s", absent)
		}
	}
	dims, _ := visit["custom_dimensions"].(map[string]any)
	if dims["dimension1"] != "gold" || dims["dimension2"] != "de-DE" {
		t.Errorf("custom dimensions = %v", visit["custom_dimensions"])
	}
	actions, _ := visit["actions"].([]any)
	if len(actions) != 5 {
		t.Fatalf("actions = %d", len(actions))
	}
	pick := func(i int) map[string]any { m, _ := actions[i].(map[string]any); return m }
	if e := pick(0); e["event_category"] != "video" || e["event_action"] != "play" || e["event_name"] != "intro" || e["event_value"] != "3.5" {
		t.Errorf("event = %v", e)
	}
	if c := pick(1); c["content_name"] != "banner" || c["content_piece"] != "img.png" || c["content_interaction"] != "click" ||
		c["content_target"] != "https://shop.example.test/x" {
		t.Errorf("content = %v", c)
	}
	order := pick(2)
	items, _ := order["item_details"].([]any)
	item, _ := items[0].(map[string]any)
	if order["order_id"] != "A-1" || order["revenue_tax"] != "3" || order["items"] != float64(2) || len(items) != 1 ||
		item["item_sku"] != "S1" || item["item_category2"] != "Kitchen" || item["price"] != "7.5" || item["quantity"] != float64(2) {
		t.Errorf("order = %v", order)
	}
	if g := pick(3); g["goal_id"] != float64(3) || g["goal_name"] != "Signup" || g["goal_page_id"] != float64(11) ||
		g["referrer_name"] != "Mail.example.test" {
		t.Errorf("goal = %v", g)
	}
	page := pick(4)
	cats, _ := page["product_view_categories"].([]any)
	pageDims, _ := page["custom_dimensions"].(map[string]any)
	if page["product_view_name"] != "Cup" || len(cats) != 2 || pageDims["dimension3"] != "variant-b" ||
		page["page_view_id"] != "abc123" || page["page_id"] != float64(77) || page["time_spent_ref"] != float64(12) {
		t.Errorf("page = %v", page)
	}
}

func TestVisitsListReadsFlatItemDetails(t *testing.T) {
	noLiveSpacing(t)
	var calls []call
	flat := `{"idSite":"1","idVisit":"4","actionDetails_0_type":"ecommerceOrder","actionDetails_0_orderId":"B-2",` +
		`"actionDetails_0_itemDetails_0_itemSKU":"S9","actionDetails_0_itemDetails_0_quantity":3,` +
		`"actionDetails_0_itemDetails_1_itemSKU":"S8","actionDetails_0_productViewCategories_0":"Home"}`
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"Live.getLastVisitsDetails": func(call) *http.Response { return ok("[" + flat + "]") },
	}).handler(t))
	result, err := env.invoke("matomo.visits.list", "open", `{"idSite":1,"include_actions":true,"flat":true}`)
	if err != nil || !strings.Contains(result, `"item_sku":"S9"`) || !strings.Contains(result, `"item_sku":"S8"`) ||
		!strings.Contains(result, `"quantity":3`) || !strings.Contains(result, `"product_view_categories":["Home"]`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}

func TestVisitsListBoundsListsAndDimensions(t *testing.T) {
	noLiveSpacing(t)
	var calls []call
	items := make([]string, maxItemDetails+7)
	for i := range items {
		items[i] = `{"itemSKU":"S` + strconv.Itoa(i) + `"}`
	}
	dims := ""
	for i := 1; i <= maxCustomDimensions+10; i++ {
		dims += `,"dimension` + strconv.Itoa(i) + `":"` + strings.Repeat("v", 2000) + `"`
	}
	body := `[{"idSite":"1","idVisit":"1"` + dims + `,"actionDetails":[{"type":"ecommerceOrder","itemDetails":[` +
		strings.Join(items, ",") + `]}]}]`
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"Live.getLastVisitsDetails": func(call) *http.Response { return ok(body) },
	}).handler(t))
	result, err := env.invoke("matomo.visits.list", "open", `{"idSite":1,"include_actions":true}`)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Visits []struct {
			Dimensions map[string]string `json:"custom_dimensions"`
			Actions    []struct {
				Items []map[string]any `json:"item_details"`
			} `json:"actions"`
		} `json:"visits"`
	}
	if json.Unmarshal([]byte(result), &out) != nil || len(out.Visits[0].Dimensions) != maxCustomDimensions ||
		len(out.Visits[0].Actions[0].Items) != maxItemDetails {
		t.Fatalf("dimensions = %d, items = %d", len(out.Visits[0].Dimensions), len(out.Visits[0].Actions[0].Items))
	}
	for _, v := range out.Visits[0].Dimensions {
		if len(v) != maxStringLength {
			t.Fatalf("dimension value length = %d", len(v))
		}
	}
}
