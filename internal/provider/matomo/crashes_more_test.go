package matomo

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// Fixtures of the deeper crash reads. getLastCrashesOverview and getLastTopCrashes follow reads of the public demo.
// Assumptions, as neither the reference nor the demo shows them: the rows of getIgnoredCrashes and
// searchCrashMessagesForMerge (crash rows that carry idsite like getAllCrashes; the demo holds no ignored crash and
// refuses the search without a write right), the answer of getCrashVisitContext (a list of visits in the form of the
// visit log, with the actions in actionDetails; the demo refuses it for the segment right on visitId), and the rows
// of the new, reappeared, and disappeared reports (the form of the top report, which the demo holds no rows of).
const crashIgnoredList = `[{"idlogcrash":105,"idsite":1,"message":"ignored one","datetime_ignored_error":"2026-11-01 10:00:00"},` +
	`{"idlogcrash":119,"idsite":"1","message":"ignored two"},` + crashForeignList + `,` + crashNoSiteList + `]`

const crashMergeList = `[{"idlogcrash":44,"idsite":1,"message":"delta is null","resource_uri":"http:\/\/anothersite.com\/dist\/bundle.min.js"},` +
	`{"idlogcrash":119,"idsite":1,"message":"excluded one"},{"idlogcrash":105,"idsite":1,"message":"excluded two"},` +
	crashForeignList + `,` + crashNoSiteList + `]`

const crashVisitContext = `[{"idSite":1,"idVisit":7,"visitorId":"0123456789abcdef","visitIp":"192.0.2.1","country":"Germany",` +
	`"visitorType":"new","unknownMember":"must-not-pass","icon":"plugins/x.png","actionDetails":[` +
	`{"type":"action","url":"https:\/\/dive-shop.net\/products\/","pageTitle":"Products","timestamp":1793600000,"unknownAction":"nope"}]},` +
	`{"idSite":"1","idVisit":8,"visitorId":"fedcba9876543210","actionDetails":[]},` +
	`{"idSite":3,"idVisit":9,"visitorId":"aaaaaaaaaaaaaaaa","visitIp":"198.51.100.9"},` +
	`{"idVisit":10,"visitorId":"bbbbbbbbbbbbbbbb","visitIp":"198.51.100.10"}]`

const crashRecentOverview = `[{"nb_crash_occurrences":500,"nb_visits_with_crash":440,"nb_new_crashes":0,"nb_reappeared_crashes":0,` +
	`"nb_disappeared_crashes":0,"visits_crash_rate":0.0713}]`

const crashRecentTop = `[{"crash_first_seen":"2023-08-30 07:50:21","crash_last_seen":"2026-11-03 05:06:54","crash_last_reappeared":null,` +
	`"nb_crash_occurrences":11,"nb_visits_with_crash":11,"label":"NetworkError when attempting to fetch resource.",` +
	`"crash_source":"Unknown","idlogcrash":81,"segment":"crashId==81"},` +
	`{"crash_first_seen":"2023-08-30 09:00:17","crash_last_seen":"2026-11-03 04:43:25","crash_last_reappeared":"2026-11-02 01:00:00",` +
	`"nb_crash_occurrences":"11","nb_visits_with_crash":"11","label":"precision -2 out of range","crash_source":"https:\/\/dive-shop.net\/a.js",` +
	`"idlogcrash":"99","segment":"crashId==99","unknownMember":"x"},` +
	`{"label":"foreign","idlogcrash":300,"idsite":3},{"label":"no id"}]`

func TestCrashesListIgnoredKeepsOnlyTheWebsiteAndPages(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, crashAnswers(nil).handler(t))
	result, err := env.invoke("matomo.crashes.list-ignored", "two", `{"idSite":1,"limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated CrashAnalytics.getIgnoredCrashes" {
		t.Errorf("requests = %s", methods(calls))
	}
	if sent := lastCall(calls, "CrashAnalytics.getIgnoredCrashes").form; sent.Get("idSite") != "1" || sent.Has("filter_limit") ||
		sent.Has("filter_offset") {
		t.Errorf("form = %v", sent)
	}
	var first CrashesResult
	if err := json.Unmarshal([]byte(result), &first); err != nil || first.IDSite != 1 || len(first.Crashes) != 1 || !first.HasMore ||
		first.Crashes[0].IDLogCrash != 105 || first.Crashes[0].DatetimeIgnoredError != "2026-11-01 10:00:00" {
		t.Fatalf("first page = %s, err = %v", result, err)
	}
	result, err = env.invoke("matomo.crashes.list-ignored", "two", `{"idSite":1,"cursor":"`+first.NextCursor+`"}`)
	var second CrashesResult
	if err != nil || json.Unmarshal([]byte(result), &second) != nil || len(second.Crashes) != 1 || second.HasMore ||
		second.Crashes[0].IDLogCrash != 119 || second.Crashes[0].Message != "ignored two" {
		t.Fatalf("second page = %s, err = %v", result, err)
	}
	for _, leaked := range []string{"Foreign", "foreign-message", "NoSite", "nosite-message"} {
		if strings.Contains(result, leaked) {
			t.Errorf("result shows %s: %s", leaked, result)
		}
	}
	if _, err := env.invoke("matomo.crashes.list-ignored", "two", `{"idSite":2,"cursor":"`+first.NextCursor+`"}`); err == nil {
		t.Error("a cursor of another website was accepted")
	}

	env = newEnvironment(t, &calls, crashAnswers(answers{"CrashAnalytics.getIgnoredCrashes": fixed(`[]`)}).handler(t))
	result, err = env.invoke("matomo.crashes.list-ignored", "one", `{}`)
	if err != nil || !sameEmpty(result, `"crashes"`, 2) || lastCall(calls, "CrashAnalytics.getIgnoredCrashes").form.Get("idSite") != "2" {
		t.Errorf("result = %s, err = %v", result, err)
	}
	env = newEnvironment(t, &calls, crashAnswers(answers{"CrashAnalytics.getIgnoredCrashes": fixed(`"x"`)}).handler(t))
	if _, err = env.invoke("matomo.crashes.list-ignored", "two", `{"idSite":1}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
}

func TestCrashesMergeCandidatesSendsTheSearchAndExcludesWithoutAProof(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, crashAnswers(nil).handler(t))
	// 300 belongs to another website: it only excludes, so it needs no proof and costs no request.
	result, err := env.invoke("matomo.crashes.merge-candidates", "two",
		`{"idSite":1,"resourceUri":"http://anothersite.com/dist/bundle.min.js","searchTerm":"delta","excludeIdLogCrashes":[119,105,300]}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated CrashAnalytics.searchCrashMessagesForMerge" {
		t.Errorf("requests = %s", methods(calls))
	}
	sent := lastCall(calls, "CrashAnalytics.searchCrashMessagesForMerge").form
	if sent.Get("idSite") != "1" || sent.Get("resourceUri") != "http://anothersite.com/dist/bundle.min.js" ||
		sent.Get("searchTerm") != "delta" || sent.Get("excludeIdLogCrashes") != "105,119,300" || sent.Get("offset") != "0" ||
		sent.Get("limit") != "26" || sent.Has("filter_limit") || sent.Has("filter_offset") {
		t.Errorf("form = %v", sent)
	}
	var out CrashMergeCandidatesResult
	if err := json.Unmarshal([]byte(result), &out); err != nil || out.IDSite != 1 || out.HasMore || len(out.Candidates) != 1 ||
		out.Candidates[0].IDLogCrash != 44 || out.Candidates[0].ResourceURI != "http://anothersite.com/dist/bundle.min.js" {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	for _, leaked := range []string{"Foreign", "NoSite", "excluded"} {
		if strings.Contains(result, leaked) {
			t.Errorf("result shows %s: %s", leaked, result)
		}
	}

	// Without arguments the optional parameters stay out of the request.
	if _, err := env.invoke("matomo.crashes.merge-candidates", "two", `{"idSite":1}`); err != nil {
		t.Fatal(err)
	}
	sent = lastCall(calls, "CrashAnalytics.searchCrashMessagesForMerge").form
	if sent.Has("resourceUri") || sent.Has("searchTerm") || sent.Has("excludeIdLogCrashes") {
		t.Errorf("form = %v", sent)
	}
}

func TestCrashesMergeCandidatesPagesFromTheStart(t *testing.T) {
	var calls []call
	rows := make([]string, 0, 5)
	for i := 1; i <= 5; i++ {
		rows = append(rows, crashRow(i, "1"))
	}
	env := newEnvironment(t, &calls, crashAnswers(answers{"CrashAnalytics.searchCrashMessagesForMerge": fixed("[" + strings.Join(rows, ",") + "]")}).handler(t))
	result, err := env.invoke("matomo.crashes.merge-candidates", "two", `{"idSite":1,"searchTerm":"m","limit":2}`)
	var first CrashMergeCandidatesResult
	if err != nil || json.Unmarshal([]byte(result), &first) != nil || len(first.Candidates) != 2 || !first.HasMore {
		t.Fatalf("first page = %s, err = %v", result, err)
	}
	if sent := lastCall(calls, "CrashAnalytics.searchCrashMessagesForMerge").form; sent.Get("limit") != "3" || sent.Get("offset") != "0" {
		t.Errorf("form = %v", sent)
	}
	result, err = env.invoke("matomo.crashes.merge-candidates", "two", `{"idSite":1,"searchTerm":"m","cursor":"`+first.NextCursor+`"}`)
	var second CrashMergeCandidatesResult
	if err != nil || json.Unmarshal([]byte(result), &second) != nil || len(second.Candidates) != 2 || second.Candidates[0].IDLogCrash != 3 ||
		!second.HasMore {
		t.Fatalf("second page = %s, err = %v", result, err)
	}
	if sent := lastCall(calls, "CrashAnalytics.searchCrashMessagesForMerge").form; sent.Get("limit") != "5" || sent.Get("offset") != "0" {
		t.Errorf("form = %v", sent)
	}
	// The cursor is bound to the search and the exclusions.
	for _, other := range []string{`{"idSite":1,"searchTerm":"x","cursor":"%s"}`, `{"idSite":1,"searchTerm":"m","excludeIdLogCrashes":[1],"cursor":"%s"}`,
		`{"idSite":1,"cursor":"%s"}`} {
		if _, err := env.invoke("matomo.crashes.merge-candidates", "two", strings.Replace(other, "%s", first.NextCursor, 1)); err == nil {
			t.Errorf("a cursor of another search was accepted: %s", other)
		}
	}
}

func TestCrashesVisitContextProvesTheCrashThenReadsOnlyThePersonalDataItKnows(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, crashAnswers(nil).handler(t))
	result, err := env.invoke("matomo.crashes.visit-context", "two",
		`{"idSite":1,"idLogCrash":105,"period":"range","date":"2026-10-01,2026-10-24","segment":"referrerName==example.com"}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated CrashAnalytics.getAllCrashes CrashAnalytics.getCrashVisitContext" {
		t.Errorf("requests = %s", methods(calls))
	}
	if proof := lastCall(calls, "CrashAnalytics.getAllCrashes").form; proof.Get("idSite") != "1" || proof.Get("filter_limit") != "10000" {
		t.Errorf("proof form = %v", proof)
	}
	sent := lastCall(calls, "CrashAnalytics.getCrashVisitContext").form
	if sent.Get("idSite") != "1" || sent.Get("idLogCrash") != "105" || sent.Get("period") != "range" ||
		sent.Get("date") != "2026-10-01,2026-10-24" || sent.Get("segment") != "referrerName==example.com" ||
		sent.Get("fetchRecentActions") != "1" || sent.Get("filter_offset") != "0" || sent.Get("filter_limit") != "26" {
		t.Errorf("form = %v", sent)
	}
	var out struct {
		IDSite     int              `json:"idSite"`
		IDLogCrash int              `json:"idLogCrash"`
		Events     []map[string]any `json:"events"`
		HasMore    bool             `json:"has_more"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil || out.IDSite != 1 || out.IDLogCrash != 105 || out.HasMore ||
		len(out.Events) != 2 {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	visit := out.Events[0]
	actions, _ := visit["actions"].([]any)
	if visit["id_visit"] != float64(7) || visit["visitor_id"] != "0123456789abcdef" || visit["visit_ip"] != "192.0.2.1" ||
		visit["country"] != "Germany" || visit["visitor_type"] != "new" || len(actions) != 1 {
		t.Errorf("visit = %v", visit)
	}
	// A string website ID counts; a visit of another website, one without a website, and members the model does not
	// know do not appear.
	for _, leaked := range []string{"198.51.100", "aaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbb", "must-not-pass", "plugins/x.png", "nope", "unknown"} {
		if strings.Contains(result, leaked) {
			t.Errorf("result shows %s: %s", leaked, result)
		}
	}
	if out.Events[1]["id_visit"] != float64(8) {
		t.Errorf("second event = %v", out.Events[1])
	}

	// No actions: Matomo is told so and the actions of the answer are not shown.
	result, err = env.invoke("matomo.crashes.visit-context", "two",
		`{"idSite":1,"idLogCrash":105,"period":"day","date":"yesterday","include_recent_actions":false}`)
	if err != nil || strings.Contains(result, "actions") || lastCall(calls, "CrashAnalytics.getCrashVisitContext").form.Get("fetchRecentActions") != "0" ||
		lastCall(calls, "CrashAnalytics.getCrashVisitContext").form.Has("segment") {
		t.Errorf("result = %s, err = %v", result, err)
	}
}

func TestCrashesVisitContextPages(t *testing.T) {
	var calls []call
	rows := make([]string, 0, 5)
	for i := 1; i <= 5; i++ {
		rows = append(rows, `{"idSite":1,"idVisit":`+itoa(i)+`}`)
	}
	env := newEnvironment(t, &calls, crashAnswers(answers{"CrashAnalytics.getCrashVisitContext": fixed("[" + strings.Join(rows, ",") + "]")}).handler(t))
	const arguments = `{"idSite":1,"idLogCrash":105,"period":"day","date":"yesterday","limit":2`
	result, err := env.invoke("matomo.crashes.visit-context", "two", arguments+`}`)
	var first CrashVisitContextResult
	if err != nil || json.Unmarshal([]byte(result), &first) != nil || len(first.Events) != 2 || !first.HasMore {
		t.Fatalf("first page = %s, err = %v", result, err)
	}
	if sent := lastCall(calls, "CrashAnalytics.getCrashVisitContext").form; sent.Get("filter_limit") != "3" || sent.Get("filter_offset") != "0" {
		t.Errorf("form = %v", sent)
	}
	result, err = env.invoke("matomo.crashes.visit-context", "two", arguments+`,"cursor":"`+first.NextCursor+`"}`)
	var second CrashVisitContextResult
	if err != nil || json.Unmarshal([]byte(result), &second) != nil || len(second.Events) != 2 || second.Events[0]["id_visit"] != float64(3) {
		t.Fatalf("second page = %s, err = %v", result, err)
	}
	if sent := lastCall(calls, "CrashAnalytics.getCrashVisitContext").form; sent.Get("filter_limit") != "5" || sent.Get("filter_offset") != "0" {
		t.Errorf("form = %v", sent)
	}
	for _, other := range []string{`"date":"today"`, `"idLogCrash":119`, `"include_recent_actions":false`, `"segment":"browserCode==FF"`} {
		changed := strings.Replace(arguments, `"idLogCrash":105`, `"idLogCrash":105,`+other, 1)
		if strings.HasPrefix(other, `"idLogCrash"`) {
			changed = strings.Replace(arguments, `"idLogCrash":105`, other, 1)
		} else if strings.HasPrefix(other, `"date"`) {
			changed = strings.Replace(arguments, `"date":"yesterday"`, other, 1)
		}
		if _, err := env.invoke("matomo.crashes.visit-context", "two", changed+`,"cursor":"`+first.NextCursor+`"}`); err == nil {
			t.Errorf("a cursor of another context was accepted: %s", other)
		}
	}
}

func TestCrashesVisitContextOfAnUnprovenCrashSendsNoContextRequest(t *testing.T) {
	for name, id := range map[string]string{"crash of another website": "300", "crash in the list of another website": "106",
		"crash without a website": "301", "crash not in the list": "999"} {
		var calls []call
		env := newEnvironment(t, &calls, crashAnswers(nil).handler(t))
		_, err := env.invoke("matomo.crashes.visit-context", "two",
			`{"idSite":1,"idLogCrash":`+id+`,"period":"day","date":"yesterday"}`)
		if classOf(err) != provider.ClassNotFound || strings.Contains(err.Error(), id) ||
			countMethod(calls, "CrashAnalytics.getAllCrashes") != 1 || countMethod(calls, "CrashAnalytics.getCrashVisitContext") != 0 {
			t.Errorf("%s: err = %v, requests = %s", name, err, methods(calls))
		}
	}
}

func TestCrashesVisitContextMapsErrorsAfterTheProof(t *testing.T) {
	for name, tc := range map[string]struct {
		answer      func(call) *http.Response
		class       provider.Class
		unsupported bool
	}{
		"segment right": {func(call) *http.Response {
			return failure("You do not have enough permission to access the segment visitId" + bodyCanary)
		}, provider.ClassPermission, false},
		"method missing": {func(call) *http.Response {
			return failure("The method 'CrashAnalytics.getCrashVisitContext' does not exist or is not available in the module 'CrashAnalytics'." + bodyCanary)
		}, "", true},
		"no list":   {fixed(`"x"`), provider.ClassInvalidResponse, false},
		"not a row": {fixed(`[1,2]`), provider.ClassInvalidResponse, false},
	} {
		var calls []call
		env := newEnvironment(t, &calls, crashAnswers(answers{"CrashAnalytics.getCrashVisitContext": tc.answer}).handler(t))
		_, err := env.invoke("matomo.crashes.visit-context", "two", crashCalls[6].arguments)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || isUnsupported(err) != tc.unsupported || classOf(err) != tc.class ||
			countMethod(calls, "CrashAnalytics.getCrashVisitContext") != 1 {
			t.Errorf("%s: err = %v (class %q)", name, err, classOf(err))
		}
	}
}

func TestCrashesRecentAsksTheMethodOfItsKind(t *testing.T) {
	for kind, method := range recentMethods {
		var calls []call
		env := newEnvironment(t, &calls, crashAnswers(nil).handler(t))
		result, err := env.invoke("matomo.crashes.recent", "two", `{"idSite":1,"kind":"`+kind+`","lastMinutes":120,"segment":"browserCode==FF"}`)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if methods(calls) != "API.getMatomoVersion API.isPluginActivated "+method {
			t.Errorf("%s: requests = %s", kind, methods(calls))
		}
		sent := lastCall(calls, method).form
		if sent.Get("idSite") != "1" || sent.Get("lastMinutes") != "120" || sent.Get("segment") != "browserCode==FF" ||
			sent.Has("filter_offset") || sent.Has("callback") || (kind == "overview") == sent.Has("filter_limit") ||
			(kind != "overview" && sent.Get("filter_limit") != "26") {
			t.Errorf("%s: form = %v", kind, sent)
		}
		var out RecentCrashesResult
		if err := json.Unmarshal([]byte(result), &out); err != nil || out.IDSite != 1 || out.Kind != kind || out.HasMore {
			t.Fatalf("%s: result = %s, err = %v", kind, result, err)
		}
		if kind == "overview" {
			o := out.Overview
			if o == nil || len(out.Crashes) != 0 || o.Occurrences == nil || *o.Occurrences != 500 || *o.VisitsWithCrash != 440 ||
				*o.NewCrashes != 0 || *o.ReappearedCrash != 0 || *o.DisappearedCrash != 0 || o.CrashRate == nil || *o.CrashRate != 0.0713 {
				t.Errorf("overview = %s", result)
			}
			continue
		}
		if out.Overview != nil || len(out.Crashes) != 2 {
			t.Fatalf("%s: result = %s", kind, result)
		}
		top, second := out.Crashes[0], out.Crashes[1]
		if top.IDLogCrash != 81 || top.Label != "NetworkError when attempting to fetch resource." || top.CrashSource != "Unknown" ||
			top.CrashFirstSeen != "2023-08-30 07:50:21" || top.CrashLastSeen != "2026-11-03 05:06:54" || top.CrashLastReappears != "" ||
			top.Occurrences == nil || *top.Occurrences != 11 || *top.VisitsWithCrash != 11 || top.Segment != "crashId==81" {
			t.Errorf("%s: top = %+v", kind, top)
		}
		if second.IDLogCrash != 99 || second.CrashLastReappears != "2026-11-02 01:00:00" || second.CrashSource != "https://dive-shop.net/a.js" ||
			second.Occurrences == nil || *second.Occurrences != 11 || strings.Contains(result, "unknownMember") || strings.Contains(result, "foreign") {
			t.Errorf("%s: second = %+v", kind, second)
		}
	}
}

func TestCrashesRecentOmitsLastMinutesAndPages(t *testing.T) {
	var calls []call
	rows := make([]string, 0, 5)
	for i := 1; i <= 5; i++ {
		rows = append(rows, `{"idlogcrash":`+itoa(i)+`,"label":"l`+itoa(i)+`"}`)
	}
	env := newEnvironment(t, &calls, crashAnswers(answers{"CrashAnalytics.getLastNewCrashes": fixed("[" + strings.Join(rows, ",") + "]")}).handler(t))
	result, err := env.invoke("matomo.crashes.recent", "two", `{"idSite":1,"kind":"new","limit":2}`)
	var first RecentCrashesResult
	if err != nil || json.Unmarshal([]byte(result), &first) != nil || len(first.Crashes) != 2 || !first.HasMore {
		t.Fatalf("first page = %s, err = %v", result, err)
	}
	if sent := lastCall(calls, "CrashAnalytics.getLastNewCrashes").form; sent.Has("lastMinutes") || sent.Has("segment") ||
		sent.Get("filter_limit") != "3" {
		t.Errorf("form = %v", sent)
	}
	result, err = env.invoke("matomo.crashes.recent", "two", `{"idSite":1,"kind":"new","cursor":"`+first.NextCursor+`"}`)
	var second RecentCrashesResult
	if err != nil || json.Unmarshal([]byte(result), &second) != nil || len(second.Crashes) != 2 || second.Crashes[0].IDLogCrash != 3 ||
		!second.HasMore || lastCall(calls, "CrashAnalytics.getLastNewCrashes").form.Get("filter_limit") != "5" {
		t.Fatalf("second page = %s, err = %v", result, err)
	}
	for _, other := range []string{`{"idSite":1,"kind":"top","cursor":"%s"}`, `{"idSite":1,"kind":"new","lastMinutes":5,"cursor":"%s"}`,
		`{"idSite":1,"kind":"new","segment":"browserCode==FF","cursor":"%s"}`, `{"idSite":2,"kind":"new","cursor":"%s"}`} {
		if _, err := env.invoke("matomo.crashes.recent", "two", strings.Replace(other, "%s", first.NextCursor, 1)); err == nil {
			t.Errorf("a cursor of another report was accepted: %s", other)
		}
	}

	// An empty report and an unusable overview.
	env = newEnvironment(t, &calls, crashAnswers(answers{"CrashAnalytics.getLastNewCrashes": fixed(`[]`)}).handler(t))
	result, err = env.invoke("matomo.crashes.recent", "one", `{"kind":"new"}`)
	if err != nil || !sameJSON(result, `{"idSite":2,"kind":"new","has_more":false}`) {
		t.Errorf("result = %s, err = %v", result, err)
	}
	env = newEnvironment(t, &calls, crashAnswers(answers{"CrashAnalytics.getLastCrashesOverview": fixed(`[]`)}).handler(t))
	if _, err = env.invoke("matomo.crashes.recent", "one", `{"kind":"overview"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
}

func TestDeeperCrashReadsRefuseBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, crashAnswers(nil).handler(t))
	const context = `"idLogCrash":105,"period":"day","date":"yesterday"`
	for _, c := range []struct{ tool, connection, arguments string }{
		{"matomo.crashes.list-ignored", "two", `{"idSite":3}`},
		{"matomo.crashes.list-ignored", "two", `{}`},
		{"matomo.crashes.list-ignored", "instance", `{"idSite":1}`},
		{"matomo.crashes.list-ignored", "two", `{"idSite":1,"filter_limit":-1}`},
		{"matomo.crashes.list-ignored", "two", `{"idSite":1,"limit":101}`},
		{"matomo.crashes.merge-candidates", "two", `{"idSite":3}`},
		{"matomo.crashes.merge-candidates", "two", `{}`},
		{"matomo.crashes.merge-candidates", "instance", `{"idSite":1}`},
		{"matomo.crashes.merge-candidates", "two", `{"idSite":1,"searchTerm":"` + strings.Repeat("a", 257) + `"}`},
		{"matomo.crashes.merge-candidates", "two", `{"idSite":1,"resourceUri":"` + strings.Repeat("a", 2049) + `"}`},
		{"matomo.crashes.merge-candidates", "two", `{"idSite":1,"excludeIdLogCrashes":[0]}`},
		{"matomo.crashes.merge-candidates", "two", `{"idSite":1,"excludeIdLogCrashes":[2147483648]}`},
		{"matomo.crashes.merge-candidates", "two", `{"idSite":1,"excludeIdLogCrashes":[5,5]}`},
		{"matomo.crashes.merge-candidates", "two", `{"idSite":1,"excludeIdLogCrashes":"5"}`},
		{"matomo.crashes.merge-candidates", "two", `{"idSite":1,"excludeIdLogCrashes":[` + strings.TrimSuffix(strings.Repeat("1,", 101), ",") + `]}`},
		{"matomo.crashes.merge-candidates", "two", `{"idSite":1,"module":"API"}`},
		{"matomo.crashes.visit-context", "two", `{"idSite":3,` + context + `}`},
		{"matomo.crashes.visit-context", "two", `{` + context + `}`},
		{"matomo.crashes.visit-context", "instance", `{"idSite":1,` + context + `}`},
		{"matomo.crashes.visit-context", "two", `{"idSite":1,"period":"day","date":"yesterday"}`},
		{"matomo.crashes.visit-context", "two", `{"idSite":1,"idLogCrash":0,"period":"day","date":"yesterday"}`},
		{"matomo.crashes.visit-context", "two", `{"idSite":1,"idLogCrash":2147483648,"period":"day","date":"yesterday"}`},
		{"matomo.crashes.visit-context", "two", `{"idSite":1,"idLogCrash":105,"date":"yesterday"}`},
		{"matomo.crashes.visit-context", "two", `{"idSite":1,"idLogCrash":105,"period":"day"}`},
		{"matomo.crashes.visit-context", "two", `{"idSite":1,"idLogCrash":105,"period":"hour","date":"yesterday"}`},
		{"matomo.crashes.visit-context", "two", `{"idSite":1,"idLogCrash":105,"period":"day","date":"x;y"}`},
		{"matomo.crashes.visit-context", "two", `{"idSite":1,` + context + `,"segment":"visitId==1;;x"}`},
		{"matomo.crashes.visit-context", "two", `{"idSite":1,` + context + `,"segment":"` + strings.Repeat("a", 1001) + `"}`},
		{"matomo.crashes.visit-context", "two", `{"idSite":1,` + context + `,"include_recent_actions":"1"}`},
		{"matomo.crashes.visit-context", "two", `{"idSite":1,` + context + `,"filter_limit":5}`},
		{"matomo.crashes.visit-context", "two", `{"idSite":1,` + context + `,"fetchRecentActions":1}`},
		{"matomo.crashes.recent", "two", `{"idSite":3,"kind":"top"}`},
		{"matomo.crashes.recent", "two", `{"kind":"top"}`},
		{"matomo.crashes.recent", "instance", `{"idSite":1,"kind":"top"}`},
		{"matomo.crashes.recent", "two", `{"idSite":1}`},
		{"matomo.crashes.recent", "two", `{"idSite":1,"kind":"getLastTopCrashes"}`},
		{"matomo.crashes.recent", "two", `{"idSite":1,"kind":"top","lastMinutes":0}`},
		{"matomo.crashes.recent", "two", `{"idSite":1,"kind":"top","lastMinutes":721}`},
		{"matomo.crashes.recent", "two", `{"idSite":1,"kind":"top","lastMinutes":-1}`},
		{"matomo.crashes.recent", "two", `{"idSite":1,"kind":"top","lastMinutes":"30"}`},
		{"matomo.crashes.recent", "two", `{"idSite":1,"kind":"top","segment":"a;;b"}`},
		{"matomo.crashes.recent", "two", `{"idSite":1,"kind":"overview","limit":5}`},
		{"matomo.crashes.recent", "two", `{"idSite":1,"kind":"overview","cursor":"x"}`},
		{"matomo.crashes.recent", "two", `{"idSite":1,"kind":"top","cursor":"x"}`},
		{"matomo.crashes.recent", "two", `{"idSite":1,"kind":"top","filter_limit":-1}`},
		{"matomo.crashes.recent", "two", `{"idSite":1,"kind":"top","callback":"x"}`},
	} {
		_, err := env.invoke(c.tool, c.connection, c.arguments)
		if err == nil {
			t.Errorf("%s %s %s was accepted", c.tool, c.connection, c.arguments)
		} else if strings.Contains(err.Error(), "idSite=3") || strings.Contains(err.Error(), "site/") ||
			strings.Contains(err.Error(), "aaaaaaaa") {
			t.Errorf("%s names a value: %v", c.tool, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}

	// The highest lastMinutes the reference allows is accepted.
	if _, err := env.invoke("matomo.crashes.recent", "two", `{"idSite":1,"kind":"top","lastMinutes":720}`); err != nil ||
		lastCall(calls, "CrashAnalytics.getLastTopCrashes").form.Get("lastMinutes") != "720" {
		t.Errorf("lastMinutes 720: err = %v", err)
	}
}

func TestDeeperCrashReadsShortenTheTextOfAnswers(t *testing.T) {
	long := strings.Repeat("n", 6000)
	crash := `{"idlogcrash":2,"idsite":1,"crash_type":"` + long + `","message":"` + long + `","resource_uri":"` + long +
		`","stack_trace":"` + long + `"}`
	var calls []call
	env := newEnvironment(t, &calls, crashAnswers(answers{
		"CrashAnalytics.getIgnoredCrashes":           fixed("[" + crash + "]"),
		"CrashAnalytics.searchCrashMessagesForMerge": fixed("[" + crash + "]"),
		"CrashAnalytics.getLastTopCrashes": fixed(`[{"idlogcrash":2,"label":"` + long + `","crash_source":"` + long + `","segment":"` + long +
			`","crash_first_seen":"` + long + `"}]`),
		"CrashAnalytics.getCrashVisitContext": fixed(`[{"idSite":1,"visitorId":"` + long + `","referrerUrl":"` + long + `","actionDetails":[` +
			`{"url":"` + long + `","pageTitle":"` + long + `"}]}]`),
	}).handler(t))
	check := func(c Crash) {
		if len(c.CrashType) != maxStringLength || len(c.Message) != maxUrlLength || len(c.ResourceURI) != maxUrlLength ||
			len(c.StackTrace) != maxCrashStack {
			t.Errorf("lengths = %d %d %d %d", len(c.CrashType), len(c.Message), len(c.ResourceURI), len(c.StackTrace))
		}
	}
	result, err := env.invoke("matomo.crashes.list-ignored", "two", `{"idSite":1}`)
	var ignored CrashesResult
	if err != nil || json.Unmarshal([]byte(result), &ignored) != nil || len(ignored.Crashes) != 1 {
		t.Fatalf("ignored: err = %v", err)
	}
	check(ignored.Crashes[0])
	result, err = env.invoke("matomo.crashes.merge-candidates", "two", `{"idSite":1}`)
	var merge CrashMergeCandidatesResult
	if err != nil || json.Unmarshal([]byte(result), &merge) != nil || len(merge.Candidates) != 1 {
		t.Fatalf("candidates: err = %v", err)
	}
	check(merge.Candidates[0])
	result, err = env.invoke("matomo.crashes.recent", "two", `{"idSite":1,"kind":"top"}`)
	var recent RecentCrashesResult
	if err != nil || json.Unmarshal([]byte(result), &recent) != nil || len(recent.Crashes) != 1 {
		t.Fatalf("recent: err = %v", err)
	}
	if c := recent.Crashes[0]; len(c.Label) != maxUrlLength || len(c.CrashSource) != maxUrlLength || len(c.Segment) != maxStringLength ||
		len(c.CrashFirstSeen) != maxStringLength {
		t.Errorf("recent lengths = %d %d %d %d", len(c.Label), len(c.CrashSource), len(c.Segment), len(c.CrashFirstSeen))
	}
	result, err = env.invoke("matomo.crashes.visit-context", "two", crashCalls[6].arguments)
	var context CrashVisitContextResult
	if err != nil || json.Unmarshal([]byte(result), &context) != nil || len(context.Events) != 1 {
		t.Fatalf("context: err = %v", err)
	}
	event := context.Events[0]
	actions, _ := event["actions"].([]any)
	action, _ := actions[0].(map[string]any)
	if len(event["visitor_id"].(string)) != maxStringLength || len(event["referrer_url"].(string)) != maxUrlLength ||
		len(action["url"].(string)) != maxUrlLength || len(action["page_title"].(string)) != maxStringLength {
		t.Errorf("event = %d bytes", len(result))
	}
}
