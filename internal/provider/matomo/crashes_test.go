package matomo

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/ratelimit"
)

// The fixtures of getAllCrashes, getCrashSummary, and getCrashTypes follow reads of the public demo
// (stack traces shortened). Assumptions, as neither the reference nor the demo shows them: the answer of
// getCrashGroups (an object keyed by the parent crash ID whose values are lists of crashes that carry idsite), a
// crash row without a website, and the answers of rows that are not on the demo (foreign websites, unreadable
// members).
const crashDemoList = `{"idlogcrash":119,"idsite":1,"crash_type":"ApiError","message":"Error returned from API: invalid value specified for user ID: user not found",` +
	`"resource_uri":"https:\/\/dive-shop.net\/zenMode.ts","stack_trace":"    at eval (zenMode.ts:41:13)\n    at c (mousetrap.min.js?cb=759:5:35)",` +
	`"resource_line":41,"resource_column":13,"datetime_first_seen":"2023-08-30 11:15:18","datetime_ignored_error":null,` +
	`"datetime_last_seen":"2026-11-03 03:03:26","datetime_last_reappeared":null,"group_idlogcrash":null,` +
	`"datetime_first_seen_pretty":"Wednesday, August 30, 2023 11:15:18","datetime_last_seen_pretty":"Tuesday, November 3, 2026 03:03:26",` +
	`"datetime_last_reappeared_pretty":null,"datetime_ignored_error_pretty":null,"date_first_seen":"2023-08-30",` +
	`"date_first_seen_pretty":"Aug 30, 2023","date_last_seen":"2026-11-03","date_last_seen_pretty":"Nov 3, 2026"}`

// The reference example of getAllCrashes gives every number as a string and an empty string for no value.
const crashReferenceList = `{"idlogcrash":"44","idsite":"1","crash_type":"TypeError","message":"delta is null",` +
	`"resource_uri":"http:\/\/anothersite.com\/dist\/bundle.min.js","stack_trace":"TypeError: this.performDecode is not a function",` +
	`"resource_line":"250","resource_column":"485","datetime_first_seen":"2023-08-30 05:51:21","datetime_ignored_error":"",` +
	`"datetime_last_seen":"2026-05-13 00:02:05","datetime_last_reappeared":"2026-06-01 10:00:00","group_idlogcrash":"112",` +
	`"datetime_last_reappeared_pretty":"Monday, June 1, 2026 10:00:00"}`

const crashForeignList = `{"idlogcrash":300,"idsite":3,"crash_type":"Foreign","message":"foreign-message"}`
const crashNoSiteList = `{"idlogcrash":301,"crash_type":"NoSite","message":"nosite-message"}`

const crashDemoSummary = `{"idlogcrash":105,"group_idlogcrash":null,"message":"dashboard is not defined","crash_type":"ReferenceError",` +
	`"resource_uri":"https:\/\/dive-shop.net\/index.php?module=Proxy&action=getUmdJs&chunk=0","resource_line":178,"resource_column":5272,` +
	`"stack_trace":"    at Proxy.mounted (index.php:178:5272)\n    at Rr (index.php:41:56622)","datetime_first_seen":"2023-08-30 09:51:36",` +
	`"datetime_first_seen_pretty":"Wednesday, August 30, 2023 09:51:36","datetime_last_seen":"2026-11-03 03:00:40",` +
	`"datetime_last_seen_pretty":"Tuesday, November 3, 2026 03:00:40","datetime_last_reappeared":null,"datetime_last_reappeared_pretty":null,` +
	`"datetime_ignored_error":null,"datetime_ignored_error_pretty":null,"category":"","crash_page_url":"https:\/\/dive-shop.net\/products\/basic-wetsuit\/"}`

const crashDemoTypes = `["TypeError","SyntaxError","unknown","Error","RangeError","ApiError","ReferenceError","js"]`

const crashMissing = "The requested crash does not exist."

// sameEmpty accepts an empty list, written out or left out, as the answer of a list without entries.
func sameEmpty(result, member string, site int) bool {
	tail := `"has_more":false,"idSite":` + itoa(site) + `}`
	return sameJSON(result, `{`+member+`:[],`+tail) || sameJSON(result, `{`+tail)
}

func crashRow(id int, site string) string {
	return `{"idlogcrash":` + itoa(id) + `,"idsite":` + site + `,"crash_type":"Error","message":"m` + itoa(id) + `"}`
}

func crashAnswers(extra answers) answers {
	return with(pluginsOn(), with(answers{
		"CrashAnalytics.getAllCrashes": func(call) *http.Response {
			return ok("[" + crashDemoList + "," + crashReferenceList + "," + crashForeignList + "," + crashNoSiteList + "," +
				`{"idlogcrash":105,"idsite":1},{"idlogcrash":106,"idsite":3}]`)
		},
		"CrashAnalytics.getCrashSummary": func(c call) *http.Response {
			if c.form.Get("idLogCrash") == "105" {
				return ok(crashDemoSummary)
			}
			return failure(crashMissing)
		},
		"CrashAnalytics.getCrashGroups": fixed(`{"112":[` + crashReferenceList + `,{"idlogcrash":119,"idsite":1,"message":"child"},` +
			crashForeignList + `,` + crashNoSiteList + `],"30":[` + crashForeignList + `],"7":{"0":{"idlogcrash":7,"idsite":2,"message":"keyed"}}}`),
		"CrashAnalytics.getCrashTypes": fixed(crashDemoTypes),
		// Assumed forms, see crashes_more_test.go.
		"CrashAnalytics.getIgnoredCrashes":           fixed(crashIgnoredList),
		"CrashAnalytics.searchCrashMessagesForMerge": fixed(crashMergeList),
		"CrashAnalytics.getCrashVisitContext":        fixed(crashVisitContext),
		"CrashAnalytics.getLastCrashesOverview":      fixed(crashRecentOverview),
		"CrashAnalytics.getLastTopCrashes":           fixed(crashRecentTop),
		"CrashAnalytics.getLastNewCrashes":           fixed(crashRecentTop),
		"CrashAnalytics.getLastReappearedCrashes":    fixed(crashRecentTop),
		"CrashAnalytics.getLastDisappearedCrashes":   fixed(crashRecentTop),
	}, extra))
}

var crashCalls = []struct{ tool, arguments string }{
	{"matomo.crashes.list", `{"idSite":1}`},
	{"matomo.crashes.get", `{"idSite":1,"idLogCrash":105}`},
	{"matomo.crash-groups.list", `{"idSite":1}`},
	{"matomo.crash-types.list", `{"idSite":1}`},
	{"matomo.crashes.list-ignored", `{"idSite":1}`},
	{"matomo.crashes.merge-candidates", `{"idSite":1,"searchTerm":"undefined"}`},
	{"matomo.crashes.visit-context", `{"idSite":1,"idLogCrash":105,"period":"day","date":"yesterday"}`},
	{"matomo.crashes.recent", `{"idSite":1,"kind":"top"}`},
}

// crashFirst is the method a tool asks first once the instance is proven.
var crashFirst = map[string]string{"matomo.crashes.list": "CrashAnalytics.getAllCrashes", "matomo.crashes.get": "CrashAnalytics.getAllCrashes",
	"matomo.crash-groups.list": "CrashAnalytics.getCrashGroups", "matomo.crash-types.list": "CrashAnalytics.getCrashTypes",
	"matomo.crashes.list-ignored": "CrashAnalytics.getIgnoredCrashes", "matomo.crashes.merge-candidates": "CrashAnalytics.searchCrashMessagesForMerge",
	"matomo.crashes.visit-context": "CrashAnalytics.getAllCrashes", "matomo.crashes.recent": "CrashAnalytics.getLastTopCrashes"}

func TestCrashToolsAreRegisteredAsOpenReadsInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, c := range crashCalls {
		d, _, found := reg.Lookup(c.tool)
		sensitivity := crashesSensitive
		if c.tool == "matomo.crashes.visit-context" {
			// Visits carry the personal data the live tools carry, and are classified like them.
			sensitivity = liveSensitivity
		}
		if !found || d.Risk != readRisk(sensitivity) || d.Risk.Idempotency != capability.IdempotencySafe ||
			d.Risk.Confirmation != capability.ConfirmationNone || !d.Risk.OpenWorld || d.RequiresToolAllowList ||
			d.Risk.Effect != capability.EffectRead || d.Risk.DataSensitivity == "" {
			t.Errorf("%s: found %t, %+v", c.tool, found, d)
		}
		for _, banned := range []string{"callback", "token", "module", "method", "format", "filter_limit", "filter_offset",
			"filter_sort_column", "disable_generic_filters"} {
			if strings.Contains(string(d.InputSchema), `"`+banned+`"`) {
				t.Errorf("%s schema offers %s", c.tool, banned)
			}
		}
		if !strings.Contains(d.Description, "CrashAnalytics") {
			t.Errorf("%s description = %s", c.tool, d.Description)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		if joined := strings.Join(profile.Tools, " "); strings.Contains(joined, "crash") {
			t.Errorf("profile %s selects a crash tool", profile.ID)
		}
	}
}

func TestCrashesListSendsTheWindowAndKeepsOnlyTheWebsite(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, crashAnswers(nil).handler(t))
	result, err := env.invoke("matomo.crashes.list", "two", `{"idSite":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated CrashAnalytics.getAllCrashes" {
		t.Errorf("requests = %s", methods(calls))
	}
	sent := lastCall(calls, "CrashAnalytics.getAllCrashes").form
	if sent.Get("idSite") != "1" || sent.Get("filter_sort_column") != "datetime_last_seen" || sent.Get("filter_sort_order") != "desc" ||
		sent.Get("filter_offset") != "0" || sent.Get("filter_limit") != "26" {
		t.Errorf("form = %v", sent)
	}
	for _, leaked := range []string{"Foreign", "foreign-message", "NoSite", "nosite-message"} {
		if strings.Contains(result, leaked) {
			t.Errorf("result shows %s: %s", leaked, result)
		}
	}
	var out CrashesResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	if out.IDSite != 1 || len(out.Crashes) != 3 || out.HasMore {
		t.Fatalf("result = %s", result)
	}
	demo, reference := out.Crashes[0], out.Crashes[1]
	if demo.IDLogCrash != 119 || demo.IDSite != 1 || demo.CrashType != "ApiError" || demo.GroupIDLogCrash != nil ||
		!strings.HasPrefix(demo.Message, "Error returned from API") || demo.ResourceURI != "https://dive-shop.net/zenMode.ts" ||
		demo.ResourceLine == nil || *demo.ResourceLine != 41 || demo.ResourceColumn == nil || *demo.ResourceColumn != 13 ||
		!strings.Contains(demo.StackTrace, "mousetrap") || demo.DatetimeFirstSeen != "2023-08-30 11:15:18" ||
		demo.DatetimeFirstSeenPretty != "Wednesday, August 30, 2023 11:15:18" || demo.DatetimeLastSeen != "2026-11-03 03:03:26" ||
		demo.DatetimeLastSeenPretty != "Tuesday, November 3, 2026 03:03:26" || demo.DatetimeLastReappeared != "" ||
		demo.DatetimeIgnoredError != "" || demo.DateFirstSeen != "2023-08-30" || demo.DateFirstSeenPretty != "Aug 30, 2023" ||
		demo.DateLastSeen != "2026-11-03" || demo.DateLastSeenPretty != "Nov 3, 2026" {
		t.Errorf("demo crash = %+v", demo)
	}
	if reference.IDLogCrash != 44 || reference.GroupIDLogCrash == nil || *reference.GroupIDLogCrash != 112 ||
		reference.ResourceLine == nil || *reference.ResourceLine != 250 || reference.DatetimeLastReappeared != "2026-06-01 10:00:00" ||
		reference.DatetimeLastReappearedPretty != "Monday, June 1, 2026 10:00:00" || reference.DatetimeIgnoredError != "" {
		t.Errorf("reference crash = %+v", reference)
	}
}

func TestCrashesListSortsAndPagesFromTheStart(t *testing.T) {
	var calls []call
	rows := make([]string, 0, 5)
	for i := 1; i <= 5; i++ {
		rows = append(rows, crashRow(i, "1"))
	}
	env := newEnvironment(t, &calls, crashAnswers(answers{"CrashAnalytics.getAllCrashes": func(c call) *http.Response {
		return ok("[" + strings.Join(rows, ",") + "]")
	}}).handler(t))
	result, err := env.invoke("matomo.crashes.list", "two",
		`{"idSite":1,"sort_column":"datetime_first_seen","sort_order":"asc","limit":2}`)
	var first CrashesResult
	if err != nil || json.Unmarshal([]byte(result), &first) != nil || len(first.Crashes) != 2 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page = %s, err = %v", result, err)
	}
	sent := lastCall(calls, "CrashAnalytics.getAllCrashes").form
	if sent.Get("filter_sort_column") != "datetime_first_seen" || sent.Get("filter_sort_order") != "asc" ||
		sent.Get("filter_limit") != "3" || sent.Get("filter_offset") != "0" {
		t.Errorf("form = %v", sent)
	}
	result, err = env.invoke("matomo.crashes.list", "two",
		`{"idSite":1,"sort_column":"datetime_first_seen","sort_order":"asc","cursor":"`+first.NextCursor+`"}`)
	var second CrashesResult
	if err != nil || json.Unmarshal([]byte(result), &second) != nil {
		t.Fatalf("second page = %s, err = %v", result, err)
	}
	// The window always starts at the first row; the server offset stays 0.
	sent = lastCall(calls, "CrashAnalytics.getAllCrashes").form
	if sent.Get("filter_offset") != "0" || sent.Get("filter_limit") != "5" || len(second.Crashes) != 2 ||
		second.Crashes[0].IDLogCrash != 3 || !second.HasMore {
		t.Errorf("second page = %s, form = %v", result, sent)
	}
	for _, other := range []string{`{"idSite":2,"cursor":"%s"}`, `{"idSite":1,"sort_order":"asc","cursor":"%s"}`,
		`{"idSite":1,"sort_column":"datetime_first_seen","cursor":"%s"}`} {
		if _, err := env.invoke("matomo.crashes.list", "two", strings.Replace(other, "%s", first.NextCursor, 1)); err == nil {
			t.Errorf("a cursor of another list was accepted: %s", other)
		}
	}
}

func TestCrashesListWithoutCrashesIsEmpty(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, crashAnswers(answers{"CrashAnalytics.getAllCrashes": fixed(`[]`)}).handler(t))
	result, err := env.invoke("matomo.crashes.list", "one", `{}`)
	if err != nil || !sameEmpty(result, `"crashes"`, 2) ||
		lastCall(calls, "CrashAnalytics.getAllCrashes").form.Get("idSite") != "2" {
		t.Errorf("result = %s, err = %v", result, err)
	}
}

func TestCrashesGetProvesTheCrashForTheWebsiteFirst(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, crashAnswers(nil).handler(t))
	result, err := env.invoke("matomo.crashes.get", "two", `{"idSite":1,"idLogCrash":105}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated CrashAnalytics.getAllCrashes CrashAnalytics.getCrashSummary" {
		t.Errorf("requests = %s", methods(calls))
	}
	proof := lastCall(calls, "CrashAnalytics.getAllCrashes").form
	if proof.Get("idSite") != "1" || proof.Get("filter_limit") != "10000" || proof.Get("filter_offset") != "0" {
		t.Errorf("proof form = %v", proof)
	}
	if sent := lastCall(calls, "CrashAnalytics.getCrashSummary").form; sent.Get("idSite") != "1" || sent.Get("idLogCrash") != "105" {
		t.Errorf("summary form = %v", sent)
	}
	var crash Crash
	if err := json.Unmarshal([]byte(result), &crash); err != nil {
		t.Fatal(err)
	}
	if crash.IDLogCrash != 105 || crash.IDSite != 1 || crash.CrashType != "ReferenceError" || crash.Message != "dashboard is not defined" ||
		crash.ResourceLine == nil || *crash.ResourceLine != 178 || crash.ResourceColumn == nil || *crash.ResourceColumn != 5272 ||
		crash.CrashPageURL != "https://dive-shop.net/products/basic-wetsuit/" || crash.Category != "" || crash.GroupIDLogCrash != nil ||
		!strings.Contains(crash.StackTrace, "Proxy.mounted") || crash.DatetimeLastSeen != "2026-11-03 03:00:40" ||
		crash.DatetimeLastSeenPretty != "Tuesday, November 3, 2026 03:00:40" || crash.DatetimeFirstSeen != "2023-08-30 09:51:36" {
		t.Errorf("crash = %+v", crash)
	}
}

func TestCrashesGetRefusesWhatItCannotProve(t *testing.T) {
	for name, tc := range map[string]struct {
		arguments string
		extra     answers
		class     provider.Class
		summaries int
	}{
		"crash of another website":             {`{"idSite":1,"idLogCrash":300}`, nil, provider.ClassNotFound, 0},
		"crash in the list of another website": {`{"idSite":1,"idLogCrash":106}`, nil, provider.ClassNotFound, 0},
		"crash without a website":              {`{"idSite":1,"idLogCrash":301}`, nil, provider.ClassNotFound, 0},
		"crash not in the list":                {`{"idSite":1,"idLogCrash":999}`, nil, provider.ClassNotFound, 0},
		"summary says it is missing": {`{"idSite":1,"idLogCrash":105}`, answers{
			"CrashAnalytics.getCrashSummary": func(call) *http.Response { return failure(crashMissing + bodyCanary) }}, provider.ClassNotFound, 1},
		"summary of another crash": {`{"idSite":1,"idLogCrash":105}`, answers{
			"CrashAnalytics.getCrashSummary": fixed(`{"idlogcrash":104,"message":"x"}`)}, provider.ClassInvalidResponse, 1},
		"summary names another website": {`{"idSite":1,"idLogCrash":105}`, answers{
			"CrashAnalytics.getCrashSummary": fixed(`{"idlogcrash":105,"idsite":3,"message":"x"}`)}, provider.ClassNotFound, 1},
		"summary false": {`{"idSite":1,"idLogCrash":105}`, answers{
			"CrashAnalytics.getCrashSummary": fixed(`false`)}, provider.ClassNotFound, 1},
		"summary is a list": {`{"idSite":1,"idLogCrash":105}`, answers{
			"CrashAnalytics.getCrashSummary": fixed(`[1,2]`)}, provider.ClassInvalidResponse, 1},
		"list is no list": {`{"idSite":1,"idLogCrash":105}`, answers{
			"CrashAnalytics.getAllCrashes": fixed(`"x"`)}, provider.ClassInvalidResponse, 0},
	} {
		var calls []call
		env := newEnvironment(t, &calls, crashAnswers(tc.extra).handler(t))
		_, err := env.invoke("matomo.crashes.get", "two", tc.arguments)
		if classOf(err) != tc.class || strings.Contains(err.Error(), "300") || strings.Contains(err.Error(), "Foreign") ||
			strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), crashMissing) {
			t.Errorf("%s: err = %v", name, err)
		}
		if countMethod(calls, "CrashAnalytics.getAllCrashes") != 1 || countMethod(calls, "CrashAnalytics.getCrashSummary") != tc.summaries {
			t.Errorf("%s: requests = %s", name, methods(calls))
		}
	}
}

func TestCrashGroupsListReadsGroupsAndDropsForeignCrashes(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, crashAnswers(nil).handler(t))
	result, err := env.invoke("matomo.crash-groups.list", "two", `{"idSite":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated CrashAnalytics.getCrashGroups" ||
		lastCall(calls, "CrashAnalytics.getCrashGroups").form.Get("idSite") != "1" {
		t.Errorf("requests = %s", methods(calls))
	}
	if strings.Contains(result, "Foreign") || strings.Contains(result, "NoSite") || strings.Contains(result, "keyed") {
		t.Errorf("result = %s", result)
	}
	var out CrashGroupsResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	// Group 7 belongs to website 2 and group 30 holds only a foreign crash: neither is shown.
	if out.IDSite != 1 || len(out.Groups) != 1 || out.HasMore || out.Groups[0].ParentIDLogCrash != 112 ||
		len(out.Groups[0].Crashes) != 2 || out.Groups[0].Crashes[0].IDLogCrash != 44 || out.Groups[0].Crashes[1].Message != "child" {
		t.Errorf("result = %s", result)
	}

	for name, body := range map[string]string{"empty list": `[]`, "empty object": `{}`} {
		env = newEnvironment(t, &calls, crashAnswers(answers{"CrashAnalytics.getCrashGroups": fixed(body)}).handler(t))
		result, err = env.invoke("matomo.crash-groups.list", "two", `{"idSite":1}`)
		if err != nil || !sameEmpty(result, `"groups"`, 1) {
			t.Errorf("%s: result = %s, err = %v", name, result, err)
		}
	}
	env = newEnvironment(t, &calls, crashAnswers(answers{"CrashAnalytics.getCrashGroups": fixed(`{"5":"x"}`)}).handler(t))
	if _, err = env.invoke("matomo.crash-groups.list", "two", `{"idSite":1}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
	env = newEnvironment(t, &calls, crashAnswers(answers{"CrashAnalytics.getCrashGroups": fixed(`"x"`)}).handler(t))
	if _, err = env.invoke("matomo.crash-groups.list", "two", `{"idSite":1}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
}

func TestCrashGroupsListPages(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, crashAnswers(answers{"CrashAnalytics.getCrashGroups": fixed(
		`{"9":[` + crashRow(9, "1") + `],"2":[` + crashRow(2, "1") + `],"5":{"idlogcrash":5,"idsite":1}}`)}).handler(t))
	result, err := env.invoke("matomo.crash-groups.list", "two", `{"idSite":1,"limit":2}`)
	var first CrashGroupsResult
	if err != nil || json.Unmarshal([]byte(result), &first) != nil || len(first.Groups) != 2 || !first.HasMore ||
		first.Groups[0].ParentIDLogCrash != 2 || first.Groups[1].ParentIDLogCrash != 5 {
		t.Fatalf("first page = %s, err = %v", result, err)
	}
	result, err = env.invoke("matomo.crash-groups.list", "two", `{"idSite":1,"cursor":"`+first.NextCursor+`"}`)
	var second CrashGroupsResult
	if err != nil || json.Unmarshal([]byte(result), &second) != nil || len(second.Groups) != 1 || second.HasMore ||
		second.Groups[0].ParentIDLogCrash != 9 {
		t.Errorf("second page = %s, err = %v", result, err)
	}
}

func TestCrashTypesListReadsTypesAndPages(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, crashAnswers(nil).handler(t))
	result, err := env.invoke("matomo.crash-types.list", "two", `{"idSite":1,"limit":3}`)
	var first CrashTypesResult
	if err != nil || json.Unmarshal([]byte(result), &first) != nil || strings.Join(first.CrashTypes, ",") != "TypeError,SyntaxError,unknown" ||
		!first.HasMore || first.IDSite != 1 {
		t.Fatalf("first page = %s, err = %v", result, err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated CrashAnalytics.getCrashTypes" {
		t.Errorf("requests = %s", methods(calls))
	}
	if sent := lastCall(calls, "CrashAnalytics.getCrashTypes").form; sent.Get("idSite") != "1" || sent.Get("filter_limit") != "4" ||
		sent.Has("filter_offset") {
		t.Errorf("form = %v", sent)
	}
	result, err = env.invoke("matomo.crash-types.list", "two", `{"idSite":1,"cursor":"`+first.NextCursor+`"}`)
	var second CrashTypesResult
	if err != nil || json.Unmarshal([]byte(result), &second) != nil || strings.Join(second.CrashTypes, ",") != "Error,RangeError,ApiError" ||
		!second.HasMore {
		t.Fatalf("second page = %s, err = %v", result, err)
	}
	if sent := lastCall(calls, "CrashAnalytics.getCrashTypes").form; sent.Get("filter_limit") != "7" {
		t.Errorf("form = %v", sent)
	}

	// A list keyed by position, empty names, and an empty list.
	env = newEnvironment(t, &calls, crashAnswers(answers{"CrashAnalytics.getCrashTypes": fixed(`{"0":"A","2":"B","3":""}`)}).handler(t))
	result, err = env.invoke("matomo.crash-types.list", "one", `{}`)
	if err != nil || !sameJSON(result, `{"idSite":2,"crash_types":["A","B"],"has_more":false}`) {
		t.Errorf("result = %s, err = %v", result, err)
	}
	env = newEnvironment(t, &calls, crashAnswers(answers{"CrashAnalytics.getCrashTypes": fixed(`[]`)}).handler(t))
	result, err = env.invoke("matomo.crash-types.list", "one", `{}`)
	if err != nil || !sameEmpty(result, `"crash_types"`, 2) {
		t.Errorf("result = %s, err = %v", result, err)
	}
}

func TestCrashToolsRefuseBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, crashAnswers(nil).handler(t))
	for _, c := range []struct{ tool, connection, arguments string }{
		{"matomo.crashes.list", "two", `{"idSite":3}`},
		{"matomo.crashes.list", "two", `{}`},
		{"matomo.crashes.list", "instance", `{}`},
		{"matomo.crashes.list", "two", `{"idSite":1,"callback":"x"}`},
		{"matomo.crashes.list", "two", `{"idSite":1,"filter_limit":-1}`},
		{"matomo.crashes.list", "two", `{"idSite":1,"sort_column":"stack_trace"}`},
		{"matomo.crashes.list", "two", `{"idSite":1,"sort_order":"up"}`},
		{"matomo.crashes.list", "two", `{"idSite":1,"limit":0}`},
		{"matomo.crashes.list", "two", `{"idSite":1,"cursor":"x"}`},
		{"matomo.crashes.get", "two", `{"idSite":3,"idLogCrash":105}`},
		{"matomo.crashes.get", "two", `{"idLogCrash":105}`},
		{"matomo.crashes.get", "one", `{"idSite":1,"idLogCrash":105}`},
		{"matomo.crashes.get", "two", `{"idSite":1}`},
		{"matomo.crashes.get", "two", `{"idSite":1,"idLogCrash":0}`},
		{"matomo.crashes.get", "two", `{"idSite":1,"idLogCrash":2147483648}`},
		{"matomo.crashes.get", "two", `{"idSite":1,"idLogCrash":"105"}`},
		{"matomo.crashes.get", "instance", `{"idSite":1,"idLogCrash":105}`},
		{"matomo.crashes.get", "two", `{"idSite":1,"idLogCrash":105,"method":"x"}`},
		{"matomo.crash-groups.list", "two", `{"idSite":3}`},
		{"matomo.crash-groups.list", "two", `{}`},
		{"matomo.crash-groups.list", "instance", `{"idSite":1}`},
		{"matomo.crash-groups.list", "two", `{"idSite":1,"token_auth":"x"}`},
		{"matomo.crash-types.list", "two", `{"idSite":3}`},
		{"matomo.crash-types.list", "two", `{}`},
		{"matomo.crash-types.list", "instance", `{"idSite":1}`},
		{"matomo.crash-types.list", "two", `{"idSite":1,"filter_limit":5}`},
	} {
		_, err := env.invoke(c.tool, c.connection, c.arguments)
		if err == nil {
			t.Errorf("%s %s %s was accepted", c.tool, c.connection, c.arguments)
		} else if strings.Contains(err.Error(), "idSite=3") || strings.Contains(err.Error(), "site/") {
			t.Errorf("%s names a value: %v", c.tool, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestCrashRequestsCarryTheBearerTokenOnlyInTheHeader(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, crashAnswers(nil).handler(t))
	for _, c := range crashCalls {
		if _, err := env.invoke(c.tool, "two", c.arguments); err != nil {
			t.Fatalf("%s: %v", c.tool, err)
		}
	}
	for _, c := range calls {
		if c.auth != "Bearer "+tokenValue || c.rawQuery != "" || c.method != http.MethodPost ||
			strings.Contains(c.body, tokenValue) || c.form.Has("token_auth") || c.form.Get("format") != "JSON" {
			t.Errorf("%s: auth %q, query %q, body %q", c.api(), c.auth, c.rawQuery, c.body)
		}
		for _, banned := range []string{"callback", "jsoncallback", "disable_generic_filters", "disable_queued_filters",
			"disable_root_datatable_post_processor"} {
			if c.form.Has(banned) {
				t.Errorf("%s sends %s", c.api(), banned)
			}
		}
		if c.form.Get("filter_limit") == "-1" || c.form.Get("filter_limit") == "false" {
			t.Errorf("%s asks for every row", c.api())
		}
	}
	if got := env.red.Apply("x " + tokenValue + " y"); strings.Contains(got, tokenValue) {
		t.Errorf("redacted text = %q", got)
	}
}

func TestCrashToolsMapMatomoErrorsAndTransportFailures(t *testing.T) {
	for _, c := range crashCalls {
		method := crashFirst[c.tool]
		for name, tc := range map[string]struct {
			answer      func(call) (*http.Response, error)
			class       provider.Class
			unsupported bool
		}{
			"permission": {func(call) (*http.Response, error) {
				return ok2(failure("You can't access this resource as it requires 'write' access for the website id = 1." + bodyCanary))
			}, provider.ClassPermission, false},
			"object not found": {func(call) (*http.Response, error) { return ok2(failure(crashMissing + bodyCanary)) }, provider.ClassNotFound, false},
			"method missing": {func(call) (*http.Response, error) {
				return ok2(failure("The method 'CrashAnalytics.x' does not exist or is not available in the module 'CrashAnalytics'." + bodyCanary))
			}, "", true},
			"module missing": {func(call) (*http.Response, error) {
				return ok2(failure("The plugin CrashAnalytics is not activated." + bodyCanary))
			}, "", true},
			"429":        {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, bodyCanary), nil }, provider.ClassRateLimited, false},
			"timeout":    {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false},
			"unreadable": {func(call) (*http.Response, error) { return ok2(ok("<html>" + bodyCanary + "</html>")) }, provider.ClassInvalidResponse, false},
			"unknown":    {func(call) (*http.Response, error) { return ok2(failure("Something " + bodyCanary)) }, provider.ClassInvalidResponse, false},
		} {
			var calls []call
			env := newEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if in.api() == method {
					return tc.answer(in)
				}
				return crashAnswers(nil).handler(t)(in)
			})
			clock := time.Now()
			t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, func() time.Time { return clock },
				func(context.Context, time.Duration) error { return nil })))
			_, err := env.invoke(c.tool, "two", c.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), crashMissing) ||
				isUnsupported(err) != tc.unsupported || classOf(err) != tc.class || countMethod(calls, method) != 1 {
				t.Errorf("%s %s: err = %v (class %q)", c.tool, name, err, classOf(err))
			}
			if name == "429" && countMethod(calls, method) != 1 {
				t.Errorf("%s: the request was repeated: %s", c.tool, methods(calls))
			}
		}
	}
}

func TestCrashSummaryErrorsAfterTheProofMapToo(t *testing.T) {
	for name, tc := range map[string]struct {
		message     string
		class       provider.Class
		unsupported bool
	}{
		"permission": {permissionMsg + bodyCanary, provider.ClassPermission, false},
		"method missing": {"The method 'CrashAnalytics.getCrashSummary' does not exist or is not available in the module 'CrashAnalytics'." +
			bodyCanary, "", true},
	} {
		var calls []call
		env := newEnvironment(t, &calls, crashAnswers(answers{
			"CrashAnalytics.getCrashSummary": func(call) *http.Response { return failure(tc.message) },
		}).handler(t))
		_, err := env.invoke("matomo.crashes.get", "two", crashCalls[1].arguments)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || isUnsupported(err) != tc.unsupported || classOf(err) != tc.class {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestCrashToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range crashCalls {
		var calls []call
		env := newEnvironment(t, &calls, with(crashAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.invoke(c.tool, "two", c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" ||
			lastCall(calls, "API.isPluginActivated").form.Get("pluginName") != crashesPlugin {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}

func TestCrashTextAndListsAreShortened(t *testing.T) {
	long := strings.Repeat("n", 6000)
	body := `{"idlogcrash":2,"idsite":1,"crash_type":"` + long + `","message":"` + long + `","resource_uri":"` + long +
		`","stack_trace":"` + long + `","crash_page_url":"` + long + `","datetime_last_seen":"` + long + `"}`
	var calls []call
	env := newEnvironment(t, &calls, crashAnswers(answers{
		"CrashAnalytics.getAllCrashes":   fixed("[" + body + "]"),
		"CrashAnalytics.getCrashSummary": fixed(body),
	}).handler(t))
	check := func(c Crash) {
		if len(c.CrashType) != maxStringLength || len(c.Message) != maxUrlLength || len(c.ResourceURI) != maxUrlLength ||
			len(c.StackTrace) != maxCrashStack || len(c.CrashPageURL) != 0 && len(c.CrashPageURL) != maxUrlLength ||
			len(c.DatetimeLastSeen) != maxStringLength {
			t.Errorf("lengths = %d %d %d %d %d", len(c.CrashType), len(c.Message), len(c.ResourceURI), len(c.StackTrace), len(c.CrashPageURL))
		}
	}
	result, err := env.invoke("matomo.crashes.list", "two", `{"idSite":1}`)
	var list CrashesResult
	if err != nil || json.Unmarshal([]byte(result), &list) != nil || len(list.Crashes) != 1 {
		t.Fatalf("result length %d, err = %v", len(result), err)
	}
	check(list.Crashes[0])

	result, err = env.invoke("matomo.crashes.get", "two", `{"idSite":1,"idLogCrash":2}`)
	var crash Crash
	if err != nil || json.Unmarshal([]byte(result), &crash) != nil || crash.IDLogCrash != 2 {
		t.Fatalf("result length %d, err = %v", len(result), err)
	}
	check(crash)

	member := `{"idlogcrash":2,"idsite":1,"message":"short"}`
	members := strings.TrimSuffix(strings.Repeat(member+",", maxCrashGroupMembers+20), ",")
	types := strings.TrimSuffix(strings.Repeat(`"`+long+`",`, 3), ",")
	env = newEnvironment(t, &calls, crashAnswers(answers{
		"CrashAnalytics.getCrashGroups": fixed(`{"2":[` + members + `]}`),
		"CrashAnalytics.getCrashTypes":  fixed(`[` + types + `]`),
	}).handler(t))
	result, err = env.invoke("matomo.crash-groups.list", "two", `{"idSite":1}`)
	var groups CrashGroupsResult
	if err != nil || json.Unmarshal([]byte(result), &groups) != nil || len(groups.Groups) != 1 ||
		len(groups.Groups[0].Crashes) != maxCrashGroupMembers {
		t.Errorf("groups = %d, err = %v", len(groups.Groups), err)
	}
	result, err = env.invoke("matomo.crash-types.list", "two", `{"idSite":1}`)
	var typed CrashTypesResult
	if err != nil || json.Unmarshal([]byte(result), &typed) != nil || len(typed.CrashTypes) != 3 || len(typed.CrashTypes[0]) != maxStringLength {
		t.Errorf("types = %v, err = %v", typed.CrashTypes, err)
	}
}
