package matomo

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
)

// The fixtures follow the JSON examples of the HeatmapSessionRecording reference (getHeatmap, getHeatmaps,
// getRecordedHeatmap, getRecordedHeatmapMetadata, getAvailableDeviceTypes, getAvailableHeatmapTypes) and the
// answers of the public demo (empty list, the not-found text, the write-right error text). Assumptions, as the
// reference gives none: the answers of the options that need a write right, of testUrlMatchPages, and the
// member page_treemirror of a heatmap; the array form of matchPageRules in a request; the shape of scroll data.
const homeHeatmap = `{"idsitehsr":11,"idsite":1,"name":"Homepage","sample_rate":"100.0","sample_limit":2000,` +
	`"match_page_rules":[{"attribute":"url","type":"equals_exactly","value":"https:\\/\\/divezone.net","inverted":"0"}],` +
	`"excluded_elements":"","screenshot_url":"https:\\/\\/divezone.net\\/","breakpoint_mobile":600,"breakpoint_tablet":960,` +
	`"created_date":"2018-04-08 08:24:27","updated_date":"2018-04-08 09:50:54","status":"active","capture_manually":0,` +
	`"created_date_pretty":"Apr 8, 2018","heatmapViewUrl":"index.php?module=Widgetize&action=iframe&moduleToWidgetize=` +
	`HeatmapSessionRecording&actionToWidgetize=showHeatmap&idSiteHsr=11&useDateUrl=0&idSite=1&period=day&date=yesterday&` +
	`token_auth=` + tokenValue + `"}`

const pricingHeatmap = `{"idsitehsr":"12","idsite":"1","name":"Pricing","sample_rate":"50.0","sample_limit":"500",` +
	`"match_page_rules":{"0":{"attribute":"path","type":"contains","value":"pricing","inverted":"1"}},` +
	`"status":"ended","capture_manually":"1","page_treemirror":"<html>mirror</html>",` +
	`"heatmapViewUrl":"index.php?module=Widgetize&idSiteHsr=12&Token%5FAuth=` + tokenValue + `&idSite=1"}`

const foreignHeatmap = `{"idsitehsr":55,"idsite":3,"name":"Foreign","heatmapViewUrl":"x?token_auth=` + tokenValue + `"}`

const heatmapMissing = "The requested heatmap does not exist"

func heatmapAnswers(extra answers) answers {
	return with(pluginsOn(), with(answers{
		"HeatmapSessionRecording.getHeatmaps": func(call) *http.Response {
			return ok("[" + homeHeatmap + "," + pricingHeatmap + "," + foreignHeatmap + "]")
		},
		"HeatmapSessionRecording.getHeatmap": func(c call) *http.Response {
			switch c.form.Get("idSiteHsr") {
			case "11":
				return ok(homeHeatmap)
			case "12":
				return ok(pricingHeatmap)
			case "55":
				return ok(foreignHeatmap)
			case "6":
				return ok(`{"idsitehsr":6,"name":"No site"}`)
			case "8":
				return ok(homeHeatmap)
			}
			return failure(heatmapMissing)
		},
		"HeatmapSessionRecording.getRecordedHeatmapMetadata": fixed(`{"nb_samples_device_all":5,"nb_samples_device_1":5,"avg_fold_device_1":9}`),
		"HeatmapSessionRecording.getRecordedHeatmap": fixed(`[{"selector":"li#menu-item-3240 > a","offset_x":838,"offset_y":966,"value":1},` +
			`{"selector":"li#menu-item-3240 > a","offset_x":1266,"offset_y":1104,"value":2},` +
			`{"selector":"aside#linkcat-799 > ul > li:nth-child(30) > a","offset_x":974,"offset_y":1066,"value":1}]`),
		"HeatmapSessionRecording.getAvailableDeviceTypes": fixed(`[{"name":"Desktop","key":1,"logo":"plugins\/Morpheus\/icons\/dist\/devices\/desktop.png"},` +
			`{"name":"Tablet","key":2,"logo":"plugins\/Morpheus\/icons\/dist\/devices\/tablet.png"}]`),
		"HeatmapSessionRecording.getAvailableHeatmapTypes":                 fixed(`[{"name":"Click","key":2},{"name":"Move","key":1},{"name":"Scroll","key":3}]`),
		"HeatmapSessionRecording.getAvailableStatuses":                     fixed(`[{"value":"active","name":"Active"},{"value":"ended","name":"Ended"}]`),
		"HeatmapSessionRecording.getAvailableTargetPageRules":              fixed(`{"targetAttributes":[{"value":"url","name":"URL"}],"targetTypes":[{"value":"equals_exactly","name":"equals exactly"}]}`),
		"HeatmapSessionRecording.getAvailableSessionRecordingSampleLimits": fixed(`[50,100,250]`),
		"HeatmapSessionRecording.testUrlMatchPages":                        fixed(`{"url":"https://example.org/pricing","matches":true}`),
	}, extra))
}

var heatmapCalls = []struct{ tool, arguments string }{
	{"matomo.heatmaps.list", `{"idSite":1}`},
	{"matomo.heatmaps.get", `{"idSite":1,"idSiteHsr":11}`},
	{"matomo.recorded-heatmaps.get", `{"idSite":1,"idSiteHsr":11,"heatmapType":2,"deviceType":1,"period":"day","date":"yesterday"}`},
	{"matomo.heatmap-options.list", `{"url":"https://example.org/a","matchPageRules":[{"attribute":"url","type":"contains","value":"a"}]}`},
}

func TestHeatmapToolsAreRegisteredAsOpenReadsInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, c := range heatmapCalls {
		d, _, found := reg.Lookup(c.tool)
		want := readRisk(heatmapsSensitive)
		if c.tool == "matomo.recorded-heatmaps.get" {
			want = readRisk(heatmapBehaviorSensitive)
		}
		if !found || d.Risk != want || d.Risk.Idempotency != capability.IdempotencySafe ||
			d.Risk.Confirmation != capability.ConfirmationNone || !d.Risk.OpenWorld || d.RequiresToolAllowList {
			t.Errorf("%s: found %t, %+v", c.tool, found, d)
		}
		for _, banned := range []string{"callback", "token", "module", "method", "format", "filter_limit"} {
			if strings.Contains(string(d.InputSchema), `"`+banned+`"`) {
				t.Errorf("%s schema offers %s", c.tool, banned)
			}
		}
		if strings.Contains(string(d.InputSchema), `"idSite"`) != (c.tool != "matomo.heatmap-options.list") {
			t.Errorf("%s idSite in schema = %s", c.tool, d.InputSchema)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		if strings.Contains(strings.Join(profile.Tools, " "), "heatmap") {
			t.Errorf("profile %s selects a heatmap tool", profile.ID)
		}
	}
}

func TestHeatmapsListReadsKeepsOnlyTheWebsiteAndRemovesTheToken(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, heatmapAnswers(nil).handler(t))
	result, err := env.invoke("matomo.heatmaps.list", "two", `{"idSite":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated HeatmapSessionRecording.getHeatmaps" {
		t.Errorf("requests = %s", methods(calls))
	}
	sent := lastCall(calls, "HeatmapSessionRecording.getHeatmaps").form
	if sent.Get("idSite") != "1" || sent.Get("includePageTreeMirror") != "0" {
		t.Errorf("form = %v", sent)
	}
	if strings.Contains(result, "token_auth") || strings.Contains(strings.ToLower(result), "token%5fauth") ||
		strings.Contains(result, tokenValue) || strings.Contains(result, "Foreign") {
		t.Errorf("result = %s", result)
	}
	var out HeatmapsResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	if out.IDSite != 1 || len(out.Heatmaps) != 2 || out.HasMore {
		t.Fatalf("result = %s", result)
	}
	home, pricing := out.Heatmaps[0], out.Heatmaps[1]
	if home.IDSiteHsr != 11 || home.IDSite != 1 || home.Name != "Homepage" || home.SampleRate != "100.0" ||
		home.SampleLimit == nil || *home.SampleLimit != 2000 || len(home.MatchPageRules) != 1 ||
		home.MatchPageRules[0].Attribute != "url" || home.MatchPageRules[0].Type != "equals_exactly" ||
		home.MatchPageRules[0].Inverted == nil || *home.MatchPageRules[0].Inverted || home.BreakpointMobile == nil ||
		*home.BreakpointMobile != 600 || *home.BreakpointTablet != 960 || home.Status != "active" ||
		home.CaptureManually == nil || *home.CaptureManually || home.CreatedDatePretty != "Apr 8, 2018" ||
		home.UpdatedDate == "" || home.CreatedDate == "" || home.ScreenshotURL == "" || home.PageTreeMirror != "" ||
		!strings.HasPrefix(home.HeatmapViewURL, "index.php?module=Widgetize&action=iframe") ||
		!strings.Contains(home.HeatmapViewURL, "idSiteHsr=11") {
		t.Errorf("home = %+v", home)
	}
	// Matomo versions that answer numbers as strings and a rule list keyed by position give the same values.
	if pricing.IDSiteHsr != 12 || pricing.SampleLimit == nil || *pricing.SampleLimit != 500 || len(pricing.MatchPageRules) != 1 ||
		pricing.MatchPageRules[0].Inverted == nil || !*pricing.MatchPageRules[0].Inverted ||
		pricing.CaptureManually == nil || !*pricing.CaptureManually || pricing.PageTreeMirror != "<html>mirror</html>" ||
		!strings.Contains(pricing.HeatmapViewURL, "idSite=1") {
		t.Errorf("pricing = %+v", pricing)
	}

	if _, err := env.invoke("matomo.heatmaps.list", "two", `{"idSite":1,"includePageTreeMirror":true}`); err != nil ||
		lastCall(calls, "HeatmapSessionRecording.getHeatmaps").form.Get("includePageTreeMirror") != "1" {
		t.Errorf("err = %v", err)
	}
	page, err := env.invoke("matomo.heatmaps.list", "two", `{"idSite":1,"limit":1}`)
	var first HeatmapsResult
	if err != nil || json.Unmarshal([]byte(page), &first) != nil || len(first.Heatmaps) != 1 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page = %s, err = %v", page, err)
	}
	page, err = env.invoke("matomo.heatmaps.list", "two", `{"idSite":1,"limit":1,"cursor":"`+first.NextCursor+`"}`)
	var second HeatmapsResult
	if err != nil || json.Unmarshal([]byte(page), &second) != nil || len(second.Heatmaps) != 1 || second.HasMore ||
		second.Heatmaps[0].IDSiteHsr != 12 {
		t.Fatalf("second page = %s, err = %v", page, err)
	}
	for _, other := range []string{`{"idSite":2,"limit":1,"cursor":"%s"}`, `{"idSite":1,"limit":1,"includePageTreeMirror":true,"cursor":"%s"}`} {
		if _, err := env.invoke("matomo.heatmaps.list", "two", strings.Replace(other, "%s", first.NextCursor, 1)); err == nil {
			t.Errorf("a cursor of another list was accepted: %s", other)
		}
	}
}

func TestHeatmapsListWithoutHeatmapsIsEmpty(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, heatmapAnswers(answers{"HeatmapSessionRecording.getHeatmaps": fixed(`[]`)}).handler(t))
	result, err := env.invoke("matomo.heatmaps.list", "one", `{}`)
	if err != nil || result != `{"has_more":false,"idSite":2}` ||
		lastCall(calls, "HeatmapSessionRecording.getHeatmaps").form.Get("idSite") != "2" {
		t.Errorf("result = %s, err = %v", result, err)
	}
}

func TestHeatmapsGetProvesTheWebsiteAndRemovesTheToken(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, heatmapAnswers(nil).handler(t))
	result, err := env.invoke("matomo.heatmaps.get", "two", `{"idSite":1,"idSiteHsr":11}`)
	var heatmap Heatmap
	if err != nil || json.Unmarshal([]byte(result), &heatmap) != nil || heatmap.IDSiteHsr != 11 || heatmap.IDSite != 1 ||
		methods(calls) != "API.getMatomoVersion API.isPluginActivated HeatmapSessionRecording.getHeatmap" ||
		strings.Contains(result, "token_auth") || strings.Contains(result, tokenValue) {
		t.Fatalf("result = %s, err = %v, requests = %s", result, err, methods(calls))
	}
	sent := lastCall(calls, "HeatmapSessionRecording.getHeatmap").form
	if sent.Get("idSite") != "1" || sent.Get("idSiteHsr") != "11" {
		t.Errorf("form = %v", sent)
	}
}

func TestHeatmapsGetRefusesWhatItCannotProve(t *testing.T) {
	for name, tc := range map[string]struct {
		arguments string
		class     provider.Class
	}{
		"heatmap of another website": {`{"idSite":1,"idSiteHsr":55}`, provider.ClassNotFound},
		"no heatmap":                 {`{"idSite":1,"idSiteHsr":3}`, provider.ClassNotFound},
		"answer without a website":   {`{"idSite":1,"idSiteHsr":6}`, provider.ClassInvalidResponse},
		"another heatmap":            {`{"idSite":1,"idSiteHsr":8}`, provider.ClassInvalidResponse},
	} {
		var calls []call
		env := newEnvironment(t, &calls, heatmapAnswers(nil).handler(t))
		_, err := env.invoke("matomo.heatmaps.get", "two", tc.arguments)
		if classOf(err) != tc.class || strings.Contains(err.Error(), "55") || strings.Contains(err.Error(), heatmapMissing) {
			t.Errorf("%s: err = %v", name, err)
		}
		if countMethod(calls, "HeatmapSessionRecording.getHeatmap") != 1 {
			t.Errorf("%s: requests = %s", name, methods(calls))
		}
	}
}

func TestRecordedHeatmapsGetBindsTheHeatmapBeforeReadingBehavior(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, heatmapAnswers(nil).handler(t))
	result, err := env.invoke("matomo.recorded-heatmaps.get", "two", `{"idSite":1,"idSiteHsr":11,"heatmapType":2,"deviceType":1,`+
		`"period":"day","date":"2026-10-01","segment":"browserName==Firefox"}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated HeatmapSessionRecording.getHeatmap "+
		"HeatmapSessionRecording.getRecordedHeatmapMetadata HeatmapSessionRecording.getRecordedHeatmap" {
		t.Errorf("requests = %s", methods(calls))
	}
	for key, want := range map[string]string{"idSite": "1", "idSiteHsr": "11", "period": "day", "date": "2026-10-01",
		"segment": "browserName==Firefox"} {
		for _, method := range []string{"getRecordedHeatmapMetadata", "getRecordedHeatmap"} {
			if got := lastCall(calls, "HeatmapSessionRecording."+method).form.Get(key); got != want {
				t.Errorf("%s %s = %q, want %q", method, key, got, want)
			}
		}
	}
	data := lastCall(calls, "HeatmapSessionRecording.getRecordedHeatmap").form
	if data.Get("heatmapType") != "2" || data.Get("deviceType") != "1" {
		t.Errorf("form = %v", data)
	}
	var out RecordedHeatmapResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	if out.IDSite != 1 || out.IDSiteHsr != 11 || out.HeatmapType != 2 || out.DeviceType != 1 || out.Period != "day" ||
		len(out.Metadata) != 3 || out.Metadata["nb_samples_device_all"] != float64(5) || len(out.Activities) != 3 || out.HasMore {
		t.Fatalf("result = %s", result)
	}
	first, _ := out.Activities[0].(map[string]any)
	if first["selector"] != "li#menu-item-3240 > a" || first["offset_x"] != float64(838) || first["value"] != float64(1) {
		t.Errorf("first activity = %v", first)
	}

	page, err := env.invoke("matomo.recorded-heatmaps.get", "two", `{"idSite":1,"idSiteHsr":11,"heatmapType":2,"deviceType":1,`+
		`"period":"day","date":"2026-10-01","limit":2}`)
	var one RecordedHeatmapResult
	if err != nil || json.Unmarshal([]byte(page), &one) != nil || len(one.Activities) != 2 || !one.HasMore || one.NextCursor == "" {
		t.Fatalf("first page = %s, err = %v", page, err)
	}
	page, err = env.invoke("matomo.recorded-heatmaps.get", "two", `{"idSite":1,"idSiteHsr":11,"heatmapType":2,"deviceType":1,`+
		`"period":"day","date":"2026-10-01","limit":2,"cursor":"`+one.NextCursor+`"}`)
	var two RecordedHeatmapResult
	if err != nil || json.Unmarshal([]byte(page), &two) != nil || len(two.Activities) != 1 || two.HasMore {
		t.Fatalf("second page = %s, err = %v", page, err)
	}
	if _, err := env.invoke("matomo.recorded-heatmaps.get", "two", `{"idSite":1,"idSiteHsr":11,"heatmapType":3,"deviceType":1,`+
		`"period":"day","date":"2026-10-01","limit":2,"cursor":"`+one.NextCursor+`"}`); err == nil {
		t.Error("a cursor of another read was accepted")
	}
}

func TestRecordedHeatmapsGetReadsNoBehaviorOfAForeignHeatmap(t *testing.T) {
	for name, id := range map[string]string{"another website": "55", "unknown": "3", "no website": "6"} {
		var calls []call
		env := newEnvironment(t, &calls, heatmapAnswers(nil).handler(t))
		_, err := env.invoke("matomo.recorded-heatmaps.get", "two", `{"idSite":1,"idSiteHsr":`+id+`,"heatmapType":2,"deviceType":1,`+
			`"period":"day","date":"yesterday"}`)
		if err == nil || strings.Contains(err.Error(), "55") ||
			countMethod(calls, "HeatmapSessionRecording.getRecordedHeatmapMetadata")+countMethod(calls, "HeatmapSessionRecording.getRecordedHeatmap") != 0 {
			t.Errorf("%s: err = %v, requests = %s", name, err, methods(calls))
		}
	}
}

func TestRecordedHeatmapsGetReadsEmptyAndScrollAnswers(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, heatmapAnswers(answers{
		"HeatmapSessionRecording.getRecordedHeatmapMetadata": fixed(`[]`),
		"HeatmapSessionRecording.getRecordedHeatmap":         fixed(`[]`),
	}).handler(t))
	result, err := env.invoke("matomo.recorded-heatmaps.get", "two", `{"idSite":1,"idSiteHsr":11,"heatmapType":3,"deviceType":2,`+
		`"period":"range","date":"last7"}`)
	if err != nil || strings.Contains(result, "metadata") || strings.Contains(result, "activities") {
		t.Errorf("result = %s, err = %v", result, err)
	}
}

func TestHeatmapOptionsListReadsEveryOptionAndTestsAUrlOnRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, heatmapAnswers(nil).handler(t))
	result, err := env.invoke("matomo.heatmap-options.list", "two", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated HeatmapSessionRecording.getAvailableDeviceTypes "+
		"HeatmapSessionRecording.getAvailableHeatmapTypes HeatmapSessionRecording.getAvailableStatuses "+
		"HeatmapSessionRecording.getAvailableTargetPageRules HeatmapSessionRecording.getAvailableSessionRecordingSampleLimits" {
		t.Errorf("requests = %s", methods(calls))
	}
	var out HeatmapOptionsResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.DeviceTypes) != 2 || out.DeviceTypes[0].Name != "Desktop" || out.DeviceTypes[0].Key == nil || *out.DeviceTypes[0].Key != 1 ||
		!strings.HasSuffix(out.DeviceTypes[0].Logo, "desktop.png") || len(out.HeatmapTypes) != 3 || *out.HeatmapTypes[0].Key != 2 ||
		out.Statuses == nil || out.TargetPageRules == nil || out.SessionRecordingSampleLimits == nil || out.URLMatch != nil ||
		len(out.Unavailable) != 0 {
		t.Errorf("result = %s", result)
	}

	calls = nil
	result, err = env.invoke("matomo.heatmap-options.list", "two", `{"url":"https://example.org/pricing","matchPageRules":[`+
		`{"attribute":"url","type":"equals_exactly","value":"https://example.org/pricing"},`+
		`{"attribute":"path","type":"contains","value":"x","inverted":true}]}`)
	if err != nil || !strings.Contains(result, `"url_match":{"matches":true,"url":"https://example.org/pricing"}`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	form := lastCall(calls, "HeatmapSessionRecording.testUrlMatchPages").form
	for key, want := range map[string]string{"url": "https://example.org/pricing", "matchPageRules[0][attribute]": "url",
		"matchPageRules[0][type]": "equals_exactly", "matchPageRules[0][value]": "https://example.org/pricing",
		"matchPageRules[1][attribute]": "path", "matchPageRules[1][type]": "contains", "matchPageRules[1][value]": "x",
		"matchPageRules[1][inverted]": "1"} {
		if form.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, form.Get(key), want)
		}
	}
	if form.Has("matchPageRules[0][inverted]") || form.Has("idSite") {
		t.Errorf("form = %v", form)
	}

	calls = nil
	if _, err := env.invoke("matomo.heatmap-options.list", "two", `{"url":"https://example.org/a"}`); err != nil ||
		lastCall(calls, "HeatmapSessionRecording.testUrlMatchPages").form.Has("matchPageRules[0][attribute]") {
		t.Errorf("err = %v", err)
	}
}

func TestHeatmapOptionsListNamesWhatTheTokenMayNotRead(t *testing.T) {
	denied := func(call) *http.Response { return failure(permissionMsg + bodyCanary) }
	var calls []call
	env := newEnvironment(t, &calls, heatmapAnswers(answers{
		"HeatmapSessionRecording.getAvailableStatuses":                     denied,
		"HeatmapSessionRecording.getAvailableTargetPageRules":              denied,
		"HeatmapSessionRecording.getAvailableSessionRecordingSampleLimits": denied,
		"HeatmapSessionRecording.testUrlMatchPages":                        denied,
	}).handler(t))
	result, err := env.invoke("matomo.heatmap-options.list", "two", `{"url":"https://example.org/a"}`)
	if err != nil || strings.Contains(result, bodyCanary) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	var out HeatmapOptionsResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	gaps := []HeatmapOptionGap{}
	for _, gap := range out.Unavailable {
		gaps = append(gaps, gap)
	}
	want := []HeatmapOptionGap{{"statuses", "permission"}, {"target_page_rules", "permission"},
		{"session_recording_sample_limits", "permission"}, {"url_match", "permission"}}
	if len(gaps) != len(want) || len(out.DeviceTypes) != 2 || len(out.HeatmapTypes) != 3 || out.Statuses != nil || out.URLMatch != nil {
		t.Fatalf("result = %s", result)
	}
	for i := range want {
		if gaps[i] != want[i] {
			t.Errorf("gap %d = %+v, want %+v", i, gaps[i], want[i])
		}
	}

	// A token without any right fails as a whole.
	calls = nil
	env = newEnvironment(t, &calls, heatmapAnswers(answers{
		"HeatmapSessionRecording.getAvailableDeviceTypes":                  denied,
		"HeatmapSessionRecording.getAvailableHeatmapTypes":                 denied,
		"HeatmapSessionRecording.getAvailableStatuses":                     denied,
		"HeatmapSessionRecording.getAvailableTargetPageRules":              denied,
		"HeatmapSessionRecording.getAvailableSessionRecordingSampleLimits": denied,
	}).handler(t))
	if _, err := env.invoke("matomo.heatmap-options.list", "two", `{}`); classOf(err) != provider.ClassPermission ||
		strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("err = %v", err)
	}

	// Any failure other than the right ends the call, so that nothing is silently missing.
	calls = nil
	env = newEnvironment(t, &calls, heatmapAnswers(answers{
		"HeatmapSessionRecording.getAvailableStatuses": func(call) *http.Response { return failure("Something " + bodyCanary) },
	}).handler(t))
	if _, err := env.invoke("matomo.heatmap-options.list", "two", `{}`); classOf(err) != provider.ClassInvalidResponse ||
		countMethod(calls, "HeatmapSessionRecording.getAvailableTargetPageRules") != 0 {
		t.Errorf("err = %v, requests = %s", err, methods(calls))
	}
}

func TestHeatmapToolsRefuseBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, heatmapAnswers(nil).handler(t))
	recorded := func(extra string) string {
		return `{"idSite":1,"idSiteHsr":11,"heatmapType":2,"deviceType":1,"period":"day","date":"yesterday"` + extra + `}`
	}
	for _, c := range []struct{ tool, connection, arguments string }{
		{"matomo.heatmaps.list", "two", `{"idSite":3}`},
		{"matomo.heatmaps.list", "two", `{}`},
		{"matomo.heatmaps.list", "instance", `{}`},
		{"matomo.heatmaps.list", "two", `{"idSite":1,"callback":"x"}`},
		{"matomo.heatmaps.list", "two", `{"idSite":1,"includePageTreeMirror":"yes"}`},
		{"matomo.heatmaps.list", "two", `{"idSite":1,"limit":0}`},
		{"matomo.heatmaps.get", "two", `{"idSite":3,"idSiteHsr":11}`},
		{"matomo.heatmaps.get", "two", `{"idSiteHsr":11}`},
		{"matomo.heatmaps.get", "one", `{"idSite":1,"idSiteHsr":11}`},
		{"matomo.heatmaps.get", "two", `{"idSite":1}`},
		{"matomo.heatmaps.get", "two", `{"idSite":1,"idSiteHsr":0}`},
		{"matomo.heatmaps.get", "two", `{"idSite":1,"idSiteHsr":"11"}`},
		{"matomo.heatmaps.get", "instance", `{"idSite":1,"idSiteHsr":11}`},
		{"matomo.recorded-heatmaps.get", "two", strings.Replace(recorded(""), `"idSite":1`, `"idSite":3`, 1)},
		{"matomo.recorded-heatmaps.get", "one", recorded("")},
		{"matomo.recorded-heatmaps.get", "two", `{"idSite":1,"idSiteHsr":11,"deviceType":1,"period":"day","date":"yesterday"}`},
		{"matomo.recorded-heatmaps.get", "two", `{"idSite":1,"idSiteHsr":11,"heatmapType":2,"period":"day","date":"yesterday"}`},
		{"matomo.recorded-heatmaps.get", "two", strings.Replace(recorded(""), `"heatmapType":2`, `"heatmapType":0`, 1)},
		{"matomo.recorded-heatmaps.get", "two", strings.Replace(recorded(""), `"deviceType":1`, `"deviceType":100`, 1)},
		{"matomo.recorded-heatmaps.get", "two", strings.Replace(recorded(""), `"day"`, `"decade"`, 1)},
		{"matomo.recorded-heatmaps.get", "two", strings.Replace(recorded(""), `"yesterday"`, `"last7"`, 1)},
		{"matomo.recorded-heatmaps.get", "two", strings.Replace(recorded(""), `"yesterday"`, `"x y"`, 1)},
		{"matomo.recorded-heatmaps.get", "two", recorded(`,"segment":"not a segment"`)},
		{"matomo.recorded-heatmaps.get", "two", recorded(`,"token_auth":"x"`)},
		{"matomo.heatmap-options.list", "instance", `{}`},
		{"matomo.heatmap-options.list", "two", `{"idSite":1}`},
		{"matomo.heatmap-options.list", "two", `{"url":""}`},
		{"matomo.heatmap-options.list", "two", `{"url":"` + strings.Repeat("a", 2049) + `"}`},
		{"matomo.heatmap-options.list", "two", `{"matchPageRules":[{"attribute":"url","type":"contains","value":"a"}]}`},
		{"matomo.heatmap-options.list", "two", `{"url":"/a","matchPageRules":[]}`},
		{"matomo.heatmap-options.list", "two", `{"url":"/a","matchPageRules":[{"attribute":"URL","type":"contains","value":"a"}]}`},
		{"matomo.heatmap-options.list", "two", `{"url":"/a","matchPageRules":[{"attribute":"url","type":"contains","value":""}]}`},
		{"matomo.heatmap-options.list", "two", `{"url":"/a","matchPageRules":[{"attribute":"url","type":"contains","value":"a","x":1}]}`},
	} {
		_, err := env.invoke(c.tool, c.connection, c.arguments)
		if err == nil {
			t.Errorf("%s %s %s was accepted", c.tool, c.connection, c.arguments)
		} else if strings.Contains(err.Error(), "idSite=3") || strings.Contains(err.Error(), "site/") {
			t.Errorf("%s names a target: %v", c.tool, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestHeatmapRequestsCarryTheBearerTokenOnlyInTheHeader(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, heatmapAnswers(nil).handler(t))
	for _, c := range heatmapCalls {
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
			"disable_root_datatable_post_processor", "filter_limit"} {
			if c.form.Has(banned) {
				t.Errorf("%s sends %s", c.api(), banned)
			}
		}
	}
	if got := env.red.Apply("x " + tokenValue + " y"); strings.Contains(got, tokenValue) {
		t.Errorf("redacted text = %q", got)
	}
}

func TestWithoutTokenRemovesTheParameterInEverySpelling(t *testing.T) {
	const token = tokenValue
	for name, tc := range map[string]struct{ in, want string }{
		"last":       {"index.php?module=W&idSite=1&token_auth=" + token, "index.php?module=W&idSite=1"},
		"first":      {"index.php?token_auth=" + token + "&idSite=1", "index.php?idSite=1"},
		"only":       {"index.php?token_auth=" + token, "index.php"},
		"upper case": {"index.php?a=1&TOKEN_AUTH=" + token + "&b=2", "index.php?a=1&b=2"},
		"encoded":    {"index.php?a=1&token%5Fauth=" + token + "&b=2", "index.php?a=1&b=2"},
		"semicolon":  {"index.php?a=1;token_auth=" + token + ";b=2", "index.php?a=1&b=2"},
		"array":      {"index.php?a=1&token_auth[]=" + token + "&b=2", "index.php?a=1&b=2"},
		"twice":      {"index.php?token_auth=a&b=2&token_auth=" + token, "index.php?b=2"},
		"empty":      {"index.php?a=1&token_auth=", "index.php?a=1"},
		"absolute":   {"https://analytics.example.test/index.php?x=1&token_auth=" + token, "https://analytics.example.test/index.php?x=1"},
		"fragment":   {"index.php?x=1#token_auth=" + token, "index.php?x=1"},
		"anonymous":  {"index.php?x=1&token_auth=anonymous", "index.php?x=1"},
		"none":       {"index.php?x=1", "index.php?x=1"},
		// A replay address from the session recording tools has the same form.
		"replay": {"index.php?module=Widgetize&actionToWidgetize=showSessionRecording&idLogHsr=5&token_auth=" + token,
			"index.php?module=Widgetize&actionToWidgetize=showSessionRecording&idLogHsr=5"},
	} {
		if got := withoutToken(tc.in, token); got != tc.want || strings.Contains(got, token) {
			t.Errorf("%s: withoutToken(%q) = %q, want %q", name, tc.in, got, tc.want)
		}
	}
	for name, in := range map[string]string{
		"unparsable":      "index.php?x=%zz&token_auth=" + token,
		"bad name":        "index.php?%zz=1&token_auth=" + token,
		"control":         "http://[::1\x7f]/?token_auth=" + token,
		"token in path":   "index.php/" + token + "?x=1",
		"token in value":  "index.php?x=" + token,
		"double encoding": "index.php?x=1&token%255Fauth=" + token,
	} {
		if got := withoutToken(in, token); got != "" {
			t.Errorf("%s: withoutToken(%q) = %q, want the field left out", name, in, got)
		}
	}
}

func TestHeatmapViewAddressesLoseTheTokenBeforeTheResultLeaves(t *testing.T) {
	// The redactor would mask the token value, so a parameter name left behind shows a missing removal.
	view := func(address string) string {
		return `{"idsitehsr":11,"idsite":1,"heatmapViewUrl":"` + address + `"}`
	}
	for name, tc := range map[string]struct{ address, want string }{
		"plain":      {"index.php?idSite=1&token_auth=" + tokenValue, "index.php?idSite=1"},
		"upper":      {"index.php?Token_Auth=" + tokenValue + "&idSite=1", "index.php?idSite=1"},
		"unparsable": {"index.php?x=%zz&token_auth=" + tokenValue, ""},
	} {
		var calls []call
		env := newEnvironment(t, &calls, heatmapAnswers(answers{
			"HeatmapSessionRecording.getHeatmap": fixed(view(tc.address)),
		}).handler(t))
		result, err := env.invoke("matomo.heatmaps.get", "two", `{"idSite":1,"idSiteHsr":11}`)
		var heatmap Heatmap
		if err != nil || json.Unmarshal([]byte(result), &heatmap) != nil || heatmap.HeatmapViewURL != tc.want ||
			strings.Contains(strings.ToLower(result), "token") || strings.Contains(result, tokenValue) {
			t.Errorf("%s: result = %s, err = %v", name, result, err)
		}
	}
}

func TestHeatmapToolsMapMatomoErrorsAndTransportFailures(t *testing.T) {
	// The first Matomo method of a tool; the options tool asks every option, so all of them fail alike.
	firstOf := map[string]string{"matomo.heatmaps.list": "HeatmapSessionRecording.getHeatmaps",
		"matomo.heatmaps.get": "HeatmapSessionRecording.getHeatmap", "matomo.recorded-heatmaps.get": "HeatmapSessionRecording.getHeatmap",
		"matomo.heatmap-options.list": "HeatmapSessionRecording.getAvailableDeviceTypes"}
	for _, c := range heatmapCalls {
		method := firstOf[c.tool]
		hits := func(in call) bool {
			return in.api() == method || c.tool == "matomo.heatmap-options.list" && strings.HasPrefix(in.api(), "HeatmapSessionRecording.")
		}
		for name, tc := range map[string]struct {
			answer      func(call) (*http.Response, error)
			class       provider.Class
			unsupported bool
		}{
			"permission": {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false},
			"object not found": {func(call) (*http.Response, error) {
				return ok2(failure(heatmapMissing + bodyCanary))
			}, provider.ClassNotFound, false},
			"method missing": {func(call) (*http.Response, error) {
				return ok2(failure("The method 'HeatmapSessionRecording.x' does not exist or is not available in the module 'HeatmapSessionRecording'." + bodyCanary))
			}, "", true},
			"module missing": {func(call) (*http.Response, error) {
				return ok2(failure("The plugin HeatmapSessionRecording is not activated." + bodyCanary))
			}, "", true},
			"429":        {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, bodyCanary), nil }, provider.ClassRateLimited, false},
			"timeout":    {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false},
			"unreadable": {func(call) (*http.Response, error) { return ok2(ok("<html>" + bodyCanary + "</html>")) }, provider.ClassInvalidResponse, false},
			"unknown":    {func(call) (*http.Response, error) { return ok2(failure("Something " + bodyCanary)) }, provider.ClassInvalidResponse, false},
		} {
			if c.tool == "matomo.heatmap-options.list" && name == "object not found" {
				continue
			}
			var calls []call
			env := newEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if hits(in) {
					return tc.answer(in)
				}
				return heatmapAnswers(nil).handler(t)(in)
			})
			clock := time.Now()
			t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, func() time.Time { return clock },
				func(context.Context, time.Duration) error { return nil })))
			_, err := env.invoke(c.tool, "two", c.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), heatmapMissing) ||
				isUnsupported(err) != tc.unsupported || classOf(err) != tc.class || countMethod(calls, method) != 1 {
				t.Errorf("%s %s: err = %v (class %q)", c.tool, name, err, classOf(err))
			}
		}
	}
}

func TestHeatmapRecordedReadsMapErrorsOfTheLaterMethods(t *testing.T) {
	for _, method := range []string{"getRecordedHeatmapMetadata", "getRecordedHeatmap"} {
		for name, tc := range map[string]struct {
			answer      func(call) *http.Response
			class       provider.Class
			unsupported bool
		}{
			"permission": {func(call) *http.Response { return failure(permissionMsg + bodyCanary) }, provider.ClassPermission, false},
			"missing": {func(call) *http.Response {
				return failure("The method 'HeatmapSessionRecording.x' does not exist or is not available in the module 'HeatmapSessionRecording'.")
			}, "", true},
			"invalid": {fixed(`"text"`), provider.ClassInvalidResponse, false},
		} {
			var calls []call
			env := newEnvironment(t, &calls, heatmapAnswers(answers{"HeatmapSessionRecording." + method: tc.answer}).handler(t))
			_, err := env.invoke(heatmapCalls[2].tool, "two", heatmapCalls[2].arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || isUnsupported(err) != tc.unsupported || classOf(err) != tc.class {
				t.Errorf("%s %s: err = %v", method, name, err)
			}
		}
	}
}

func TestHeatmapToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range heatmapCalls {
		var calls []call
		env := newEnvironment(t, &calls, with(heatmapAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.invoke(c.tool, "two", c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" ||
			lastCall(calls, "API.isPluginActivated").form.Get("pluginName") != heatmapsPlugin {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}

func TestHeatmapTextAndActivitiesAreShortened(t *testing.T) {
	long := strings.Repeat("n", 6000)
	body := `{"idsitehsr":11,"idsite":1,"name":"` + long + `","excluded_elements":"` + long + `","screenshot_url":"` + long +
		`","page_treemirror":"` + strings.Repeat("m", 20000) + `","match_page_rules":[{"value":"` + long + `"}]}`
	var calls []call
	env := newEnvironment(t, &calls, heatmapAnswers(answers{
		"HeatmapSessionRecording.getHeatmap": fixed(body),
		"HeatmapSessionRecording.getRecordedHeatmap": fixed(`[{"selector":"` + long + `","value":1,"extra":[` +
			strings.Repeat("1,", 200) + `1]}]`),
	}).handler(t))
	result, err := env.invoke("matomo.heatmaps.get", "two", `{"idSite":1,"idSiteHsr":11}`)
	var heatmap Heatmap
	if err != nil || json.Unmarshal([]byte(result), &heatmap) != nil || len(heatmap.Name) != maxStringLength ||
		len(heatmap.ExcludedElements) != maxUrlLength || len(heatmap.ScreenshotURL) != maxUrlLength ||
		len(heatmap.MatchPageRules) != 1 || len(heatmap.MatchPageRules[0].Value) != maxUrlLength {
		t.Errorf("result length %d, err = %v", len(result), err)
	}
	result, err = env.invoke(heatmapCalls[2].tool, "two", heatmapCalls[2].arguments)
	var recorded RecordedHeatmapResult
	if err != nil || json.Unmarshal([]byte(result), &recorded) != nil || len(recorded.Activities) != 1 {
		t.Fatalf("result length %d, err = %v", len(result), err)
	}
	row, _ := recorded.Activities[0].(map[string]any)
	extra, _ := row["extra"].([]any)
	if len(row["selector"].(string)) != maxUrlLength || len(extra) != maxResultEntries {
		t.Errorf("row = %.200v", row)
	}
}
