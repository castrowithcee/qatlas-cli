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

// The fixtures follow the JSON examples of the HeatmapSessionRecording reference (getSessionRecording,
// getSessionRecordings, getRecordedSessions, getEventTypes). The demo has no recordings, so its answers only show
// the not-found text. Assumptions, as the reference gives no example: the whole shape of getRecordedSession (an
// object with a list of events, and the same events as a plain list), the member names of its events, and the
// answers that name a website or recording of a row.
const replayAddress = "index.php?module=Widgetize&action=iframe&moduleToWidgetize=HeatmapSessionRecording&" +
	"actionToWidgetize=replayRecording&idSiteHsr=13&idLogHsr=180779&idSite=1&period=day&date=today&token_auth=" + tokenValue

const homeRecording = `{"idsitehsr":13,"idsite":1,"name":"Homepage","sample_rate":"100.0","sample_limit":250,` +
	`"match_page_rules":[{"attribute":"url","type":"equals_exactly","value":"https:\\/\\/divezone.net","inverted":"0"}],` +
	`"min_session_time":10,"requires_activity":true,"capture_keystrokes":true,"created_date":"2018-05-10 16:34:54",` +
	`"updated_date":"2018-06-01 07:37:01","status":"active","capture_manually":0,"created_date_pretty":"May 10, 2018"}`

const checkoutRecording = `{"idsitehsr":"14","idsite":"1","name":"Checkout","sample_rate":"50.0","sample_limit":"100",` +
	`"match_page_rules":{"0":{"attribute":"path","type":"contains","value":"checkout","inverted":"1"}},` +
	`"min_session_time":"0","requires_activity":"0","capture_keystrokes":"0","status":"ended","capture_manually":"1"}`

const foreignRecording = `{"idsitehsr":55,"idsite":3,"name":"Foreign"}`

const recordingMissing = "The requested session recording does not exist"

const sessionRows = `[{"label":"19744450","nb_pageviews":3,"idvisit":19744450,"first_url":"divezone.net\/",` +
	`"last_url":"divezone.net\/jobs?token_auth=` + tokenValue + `","time_on_site":"79898","total_events":61,` +
	`"idloghsr":180779,"idvisitor":"c0ffee1234567890","location_country":"br","location_region":"RJ",` +
	`"location_city":"Rio de Janeiro","config_os":"AND","config_device_type":1,"config_device_model":"Galaxy A5 (2016)",` +
	`"config_browser_name":"SB","server_time":"2026-05-13 23:22:44","sessionReplayUrl":"` + replayAddress + `"},` +
	`{"label":"19744469","nb_pageviews":2,"idvisit":19744469,"first_url":"divezone.net\/jobs","last_url":"divezone.net\/",` +
	`"time_on_site":"23721","total_events":0,"idloghsr":180788,"idvisitor":false,"location_country":null,` +
	`"config_device_type":null,"server_time":"2026-05-13 23:25:03","sessionReplayUrl":"index.php?module=Widgetize&` +
	`idSiteHsr=13&idLogHsr=180788&idSite=1&Token%5FAuth=` + tokenValue + `"},` +
	`{"label":"900","idvisit":900,"idloghsr":900,"sessionReplayUrl":"index.php?idSiteHsr=13&idLogHsr=900&idSite=3&token_auth=` +
	tokenValue + `"},` +
	`{"label":"901","idvisit":901,"idloghsr":901,"sessionReplayUrl":"index.php?idSiteHsr=99&idLogHsr=901&idSite=1&token_auth=` +
	tokenValue + `"},` +
	`{"label":"902","idvisit":902,"idloghsr":902,"idvisitor":"0"}]`

const sessionObject = `{"idloghsr":180779,"idsitehsr":13,"idsite":1,"events":[` +
	`{"event_type":2,"time_since_load":120,"selector":"a#buy","x":10,"y":20},` +
	`{"event_type":"9","time_since_load":900,"selector":"input#email","text":"secret@example.org","value":"hunter2"},` +
	`{"event_type":10,"time_since_load":950,"selector":"select#plan","value":"enterprise","nested":{"text":"typed"}},` +
	`{"event_type":3,"time_since_load":1200,"y":400}]}`

const sessionEvents = `[{"event_type":2,"time_since_load":120,"selector":"a#buy"},` +
	`{"event_type":9,"time_since_load":900,"selector":"input#email","text":"secret@example.org"},` +
	`{"event_type":3,"time_since_load":1200,"y":400}]`

const eventTypes = `[{"name":"Move","key":1},{"name":"Click","key":2},{"name":"Scroll","key":3},` +
	`{"name":"Form Text Change","key":9},{"name":"Form Value Change","key":10}]`

func recordingAnswers(extra answers) answers {
	return with(pluginsOn(), with(answers{
		"HeatmapSessionRecording.getSessionRecordings": func(call) *http.Response {
			return ok("[" + homeRecording + "," + checkoutRecording + "," + foreignRecording + "]")
		},
		"HeatmapSessionRecording.getSessionRecording": func(c call) *http.Response {
			switch c.form.Get("idSiteHsr") {
			case "13":
				return ok(homeRecording)
			case "14":
				return ok(checkoutRecording)
			case "55":
				return ok(foreignRecording)
			case "6":
				return ok(`{"idsitehsr":6,"name":"No site"}`)
			case "8":
				return ok(homeRecording)
			}
			return failure(recordingMissing)
		},
		"HeatmapSessionRecording.getRecordedSessions": fixed(sessionRows),
		"HeatmapSessionRecording.getRecordedSession":  fixed(sessionObject),
		"HeatmapSessionRecording.getEventTypes":       fixed(eventTypes),
	}, extra))
}

var recordingCalls = []struct{ tool, arguments string }{
	{"matomo.session-recordings.list", `{"idSite":1}`},
	{"matomo.session-recordings.get", `{"idSite":1,"idSiteHsr":13}`},
	{"matomo.recorded-sessions.list", `{"idSite":1,"idSiteHsr":13,"period":"day","date":"yesterday"}`},
	{"matomo.recorded-sessions.get", `{"idSite":1,"idSiteHsr":13,"idLogHsr":180779}`},
}

func TestSessionRecordingToolsAreRegisteredAsOpenReadsInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, c := range recordingCalls {
		d, _, found := reg.Lookup(c.tool)
		want := readRisk(sessionRecordingsSensitive)
		if strings.HasPrefix(c.tool, "matomo.recorded-sessions.") {
			want = readRisk(recordedSessionsSensitive)
		}
		if !found || d.Risk != want || d.Risk.Effect != capability.EffectRead || d.Risk.Idempotency != capability.IdempotencySafe ||
			d.Risk.Confirmation != capability.ConfirmationNone || !d.Risk.OpenWorld || d.RequiresToolAllowList {
			t.Errorf("%s: found %t, %+v", c.tool, found, d)
		}
		for _, banned := range []string{"callback", "token", "module", "method", "format", "filter_limit", "idSubtable"} {
			if strings.Contains(string(d.InputSchema), `"`+banned+`"`) {
				t.Errorf("%s schema offers %s", c.tool, banned)
			}
		}
		if !strings.Contains(string(d.InputSchema), `"idSite"`) {
			t.Errorf("%s has no idSite", c.tool)
		}
	}
	if readRisk(recordedSessionsSensitive).DataSensitivity == readRisk(sessionRecordingsSensitive).DataSensitivity ||
		readRisk(recordedSessionsSensitive).DataSensitivity == readRisk(heatmapBehaviorSensitive).DataSensitivity {
		t.Error("the recorded sessions share a data sensitivity label")
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		if joined := strings.Join(profile.Tools, " "); strings.Contains(joined, "recording") || strings.Contains(joined, "recorded") {
			t.Errorf("profile %s selects a session recording tool", profile.ID)
		}
	}
}

func TestSessionRecordingsListReadsKeepsOnlyTheWebsiteAndPages(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, recordingAnswers(nil).handler(t))
	result, err := env.invoke("matomo.session-recordings.list", "two", `{"idSite":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated HeatmapSessionRecording.getSessionRecordings" ||
		lastCall(calls, "HeatmapSessionRecording.getSessionRecordings").form.Get("idSite") != "1" {
		t.Errorf("requests = %s", methods(calls))
	}
	if strings.Contains(result, "Foreign") {
		t.Errorf("result = %s", result)
	}
	var out SessionRecordingsResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	if out.IDSite != 1 || len(out.SessionRecordings) != 2 || out.HasMore {
		t.Fatalf("result = %s", result)
	}
	home, checkout := out.SessionRecordings[0], out.SessionRecordings[1]
	if home.IDSiteHsr != 13 || home.IDSite != 1 || home.Name != "Homepage" || home.SampleRate != "100.0" ||
		home.SampleLimit == nil || *home.SampleLimit != 250 || len(home.MatchPageRules) != 1 ||
		home.MatchPageRules[0].Attribute != "url" || home.MatchPageRules[0].Inverted == nil || *home.MatchPageRules[0].Inverted ||
		home.MinSessionTime == nil || *home.MinSessionTime != 10 || home.RequiresActivity == nil || !*home.RequiresActivity ||
		home.CaptureKeystrokes == nil || !*home.CaptureKeystrokes || home.CreatedDate == "" || home.UpdatedDate == "" ||
		home.Status != "active" || home.CaptureManually == nil || *home.CaptureManually || home.CreatedDatePretty != "May 10, 2018" {
		t.Errorf("home = %+v", home)
	}
	// Matomo versions that answer numbers and flags as strings and a rule list keyed by position give the same values.
	if checkout.IDSiteHsr != 14 || checkout.SampleLimit == nil || *checkout.SampleLimit != 100 || len(checkout.MatchPageRules) != 1 ||
		checkout.MatchPageRules[0].Inverted == nil || !*checkout.MatchPageRules[0].Inverted ||
		checkout.CaptureKeystrokes == nil || *checkout.CaptureKeystrokes || checkout.CaptureManually == nil || !*checkout.CaptureManually {
		t.Errorf("checkout = %+v", checkout)
	}

	page, err := env.invoke("matomo.session-recordings.list", "two", `{"idSite":1,"limit":1}`)
	var first SessionRecordingsResult
	if err != nil || json.Unmarshal([]byte(page), &first) != nil || len(first.SessionRecordings) != 1 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page = %s, err = %v", page, err)
	}
	page, err = env.invoke("matomo.session-recordings.list", "two", `{"idSite":1,"limit":1,"cursor":"`+first.NextCursor+`"}`)
	var second SessionRecordingsResult
	if err != nil || json.Unmarshal([]byte(page), &second) != nil || len(second.SessionRecordings) != 1 || second.HasMore ||
		second.SessionRecordings[0].IDSiteHsr != 14 {
		t.Fatalf("second page = %s, err = %v", page, err)
	}
	if _, err := env.invoke("matomo.session-recordings.list", "two", `{"idSite":2,"limit":1,"cursor":"`+first.NextCursor+`"}`); err == nil {
		t.Error("a cursor of another list was accepted")
	}
}

func TestSessionRecordingsListWithoutRecordingsIsEmpty(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, recordingAnswers(answers{"HeatmapSessionRecording.getSessionRecordings": fixed(`[]`)}).handler(t))
	result, err := env.invoke("matomo.session-recordings.list", "one", `{}`)
	if err != nil || result != `{"has_more":false,"idSite":2}` ||
		lastCall(calls, "HeatmapSessionRecording.getSessionRecordings").form.Get("idSite") != "2" {
		t.Errorf("result = %s, err = %v", result, err)
	}
}

func TestSessionRecordingsGetProvesTheWebsite(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, recordingAnswers(nil).handler(t))
	result, err := env.invoke("matomo.session-recordings.get", "two", `{"idSite":1,"idSiteHsr":13}`)
	var recording SessionRecording
	if err != nil || json.Unmarshal([]byte(result), &recording) != nil || recording.IDSiteHsr != 13 || recording.IDSite != 1 ||
		recording.Name != "Homepage" || methods(calls) != "API.getMatomoVersion API.isPluginActivated HeatmapSessionRecording.getSessionRecording" {
		t.Fatalf("result = %s, err = %v, requests = %s", result, err, methods(calls))
	}
	sent := lastCall(calls, "HeatmapSessionRecording.getSessionRecording").form
	if sent.Get("idSite") != "1" || sent.Get("idSiteHsr") != "13" {
		t.Errorf("form = %v", sent)
	}
}

func TestSessionRecordingsGetRefusesWhatItCannotProve(t *testing.T) {
	for name, tc := range map[string]struct {
		arguments string
		class     provider.Class
	}{
		"recording of another website": {`{"idSite":1,"idSiteHsr":55}`, provider.ClassNotFound},
		"no recording":                 {`{"idSite":1,"idSiteHsr":3}`, provider.ClassNotFound},
		"answer without a website":     {`{"idSite":1,"idSiteHsr":6}`, provider.ClassInvalidResponse},
		"another recording":            {`{"idSite":1,"idSiteHsr":8}`, provider.ClassInvalidResponse},
	} {
		var calls []call
		env := newEnvironment(t, &calls, recordingAnswers(nil).handler(t))
		_, err := env.invoke("matomo.session-recordings.get", "two", tc.arguments)
		if classOf(err) != tc.class || strings.Contains(err.Error(), "55") || strings.Contains(err.Error(), recordingMissing) {
			t.Errorf("%s: err = %v", name, err)
		}
		if countMethod(calls, "HeatmapSessionRecording.getSessionRecording") != 1 {
			t.Errorf("%s: requests = %s", name, methods(calls))
		}
	}
}

func TestRecordedSessionsListBindsTheRecordingFiltersRowsAndRemovesTheToken(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, recordingAnswers(nil).handler(t))
	result, err := env.invoke("matomo.recorded-sessions.list", "two", `{"idSite":1,"idSiteHsr":13,"period":"day",`+
		`"date":"2026-05-13","segment":"browserName==Firefox","idVisit":19744450}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated HeatmapSessionRecording.getSessionRecording "+
		"HeatmapSessionRecording.getRecordedSessions" {
		t.Errorf("requests = %s", methods(calls))
	}
	sent := lastCall(calls, "HeatmapSessionRecording.getRecordedSessions").form
	for key, want := range map[string]string{"idSite": "1", "idSiteHsr": "13", "period": "day", "date": "2026-05-13",
		"segment": "browserName==Firefox", "idSubtable": "19744450"} {
		if sent.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, sent.Get(key), want)
		}
	}
	if strings.Contains(strings.ToLower(result), "token") || strings.Contains(result, tokenValue) {
		t.Errorf("result holds a token: %s", result)
	}
	var out RecordedSessionsResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	// The rows of another website (idSite=3) and of another recording (idSiteHsr=99) are dropped.
	if out.IDSite != 1 || out.IDSiteHsr != 13 || out.Period != "day" || len(out.RecordedSessions) != 3 || out.HasMore {
		t.Fatalf("result = %s", result)
	}
	first, second, third := out.RecordedSessions[0], out.RecordedSessions[1], out.RecordedSessions[2]
	if first.Label != "19744450" || first.NbPageviews == nil || *first.NbPageviews != 3 || first.IDVisit == nil ||
		*first.IDVisit != 19744450 || first.FirstURL != "divezone.net/" || first.LastURL != "divezone.net/jobs" ||
		first.TimeOnSite != "79898" || first.TotalEvents == nil || *first.TotalEvents != 61 || first.IDLogHsr == nil ||
		*first.IDLogHsr != 180779 || first.IDVisitor != "c0ffee1234567890" || first.LocationCountry != "br" ||
		first.LocationRegion != "RJ" || first.LocationCity != "Rio de Janeiro" || first.ConfigOS != "AND" ||
		first.ConfigDeviceType == nil || *first.ConfigDeviceType != 1 || first.ConfigDeviceModel != "Galaxy A5 (2016)" ||
		first.ConfigBrowserName != "SB" || first.ServerTime != "2026-05-13 23:22:44" ||
		!strings.HasPrefix(first.SessionReplayURL, "index.php?module=Widgetize") ||
		!strings.Contains(first.SessionReplayURL, "idLogHsr=180779") || !strings.HasSuffix(first.SessionReplayURL, "date=today") {
		t.Errorf("first = %+v", first)
	}
	if second.IDVisitor != "" || second.LocationCountry != "" || second.ConfigDeviceType != nil ||
		second.SessionReplayURL != "index.php?module=Widgetize&idSiteHsr=13&idLogHsr=180788&idSite=1" || third.IDVisitor != "" {
		t.Errorf("second = %+v, third = %+v", second, third)
	}

	page, err := env.invoke("matomo.recorded-sessions.list", "two", `{"idSite":1,"idSiteHsr":13,"period":"day","date":"2026-05-13","limit":2}`)
	var one RecordedSessionsResult
	if err != nil || json.Unmarshal([]byte(page), &one) != nil || len(one.RecordedSessions) != 2 || !one.HasMore || one.NextCursor == "" {
		t.Fatalf("first page = %s, err = %v", page, err)
	}
	page, err = env.invoke("matomo.recorded-sessions.list", "two", `{"idSite":1,"idSiteHsr":13,"period":"day","date":"2026-05-13",`+
		`"limit":2,"cursor":"`+one.NextCursor+`"}`)
	var two RecordedSessionsResult
	if err != nil || json.Unmarshal([]byte(page), &two) != nil || len(two.RecordedSessions) != 1 || two.HasMore {
		t.Fatalf("second page = %s, err = %v", page, err)
	}
	if _, err := env.invoke("matomo.recorded-sessions.list", "two", `{"idSite":1,"idSiteHsr":13,"period":"day","date":"2026-05-14",`+
		`"limit":2,"cursor":"`+one.NextCursor+`"}`); err == nil {
		t.Error("a cursor of another read was accepted")
	}
}

func TestRecordedSessionsReadNoDataOfAForeignRecording(t *testing.T) {
	for _, tool := range []struct{ name, arguments string }{
		{"matomo.recorded-sessions.list", `"period":"day","date":"yesterday"`},
		{"matomo.recorded-sessions.get", `"idLogHsr":180779`},
	} {
		for name, id := range map[string]string{"another website": "55", "unknown": "3", "no website": "6", "other id": "8"} {
			var calls []call
			env := newEnvironment(t, &calls, recordingAnswers(nil).handler(t))
			_, err := env.invoke(tool.name, "two", `{"idSite":1,"idSiteHsr":`+id+`,`+tool.arguments+`}`)
			if err == nil || strings.Contains(err.Error(), "55") || strings.Contains(err.Error(), recordingMissing) ||
				countMethod(calls, "HeatmapSessionRecording.getRecordedSessions")+countMethod(calls, "HeatmapSessionRecording.getRecordedSession")+
					countMethod(calls, "HeatmapSessionRecording.getEventTypes") != 0 {
				t.Errorf("%s %s: err = %v, requests = %s", tool.name, name, err, methods(calls))
			}
		}
	}
}

func TestRecordedSessionsGetWithholdsTypedTextAndNamesTheEventTypes(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, recordingAnswers(nil).handler(t))
	result, err := env.invoke("matomo.recorded-sessions.get", "two", `{"idSite":1,"idSiteHsr":13,"idLogHsr":180779}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated HeatmapSessionRecording.getSessionRecording "+
		"HeatmapSessionRecording.getRecordedSession HeatmapSessionRecording.getEventTypes" {
		t.Errorf("requests = %s", methods(calls))
	}
	sent := lastCall(calls, "HeatmapSessionRecording.getRecordedSession").form
	if sent.Get("idSite") != "1" || sent.Get("idSiteHsr") != "13" || sent.Get("idLogHsr") != "180779" {
		t.Errorf("form = %v", sent)
	}
	for _, typed := range []string{"secret@example.org", "hunter2", "enterprise", `"typed"`} {
		if strings.Contains(result, typed) {
			t.Errorf("result holds typed text %q: %s", typed, result)
		}
	}
	var out struct {
		IDSite, IDSiteHsr, IDLogHsr int
		Session                     map[string]any
		Events                      []any
		TypedTextWithheld           int `json:"typed_text_withheld"`
		EventTypes                  []EventType
		HasMore                     bool `json:"has_more"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	events, _ := out.Session["events"].([]any)
	if out.IDSite != 1 || out.IDSiteHsr != 13 || out.IDLogHsr != 180779 || out.TypedTextWithheld != 2 || len(events) != 4 ||
		len(out.EventTypes) != 5 || out.EventTypes[3].Name != "Form Text Change" || out.EventTypes[3].Key == nil ||
		*out.EventTypes[3].Key != 9 || out.HasMore || len(out.Events) != 0 {
		t.Fatalf("result = %s", result)
	}
	click, _ := events[0].(map[string]any)
	form, _ := events[1].(map[string]any)
	if click["selector"] != "a#buy" || click["x"] != float64(10) || len(form) != 3 || form["selector"] != "input#email" ||
		form["time_since_load"] != float64(900) {
		t.Errorf("click = %v, form = %v", click, form)
	}
}

func TestRecordedSessionsGetPagesAListOfEvents(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, recordingAnswers(answers{"HeatmapSessionRecording.getRecordedSession": fixed(sessionEvents)}).handler(t))
	const arguments = `{"idSite":1,"idSiteHsr":13,"idLogHsr":180779,"limit":2`
	page, err := env.invoke("matomo.recorded-sessions.get", "two", arguments+`}`)
	var one RecordedSessionResult
	if err != nil || json.Unmarshal([]byte(page), &one) != nil || len(one.Events) != 2 || !one.HasMore || one.NextCursor == "" ||
		one.TypedTextWithheld != 1 || strings.Contains(page, "secret@example.org") || one.Session != nil {
		t.Fatalf("first page = %s, err = %v", page, err)
	}
	page, err = env.invoke("matomo.recorded-sessions.get", "two", arguments+`,"cursor":"`+one.NextCursor+`"}`)
	var two RecordedSessionResult
	if err != nil || json.Unmarshal([]byte(page), &two) != nil || len(two.Events) != 1 || two.HasMore {
		t.Fatalf("second page = %s, err = %v", page, err)
	}
}

func TestRecordedSessionsGetRefusesAnAnswerOfAnotherSessionOrShape(t *testing.T) {
	for name, body := range map[string]string{
		"another website":   `{"idsite":3,"events":[]}`,
		"another recording": `{"idsitehsr":99,"events":[]}`,
		"another session":   `{"idloghsr":5,"events":[]}`,
		"scalar":            `"text"`,
		"false":             `false`,
	} {
		var calls []call
		env := newEnvironment(t, &calls, recordingAnswers(answers{"HeatmapSessionRecording.getRecordedSession": fixed(body)}).handler(t))
		result, err := env.invoke("matomo.recorded-sessions.get", "two", recordingCalls[3].arguments)
		if classOf(err) != provider.ClassInvalidResponse || result != "" || countMethod(calls, "HeatmapSessionRecording.getEventTypes") != 0 {
			t.Errorf("%s: result = %s, err = %v", name, result, err)
		}
	}
}

func TestRecordingToolsRefuseBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, recordingAnswers(nil).handler(t))
	listed := func(extra string) string {
		return `{"idSite":1,"idSiteHsr":13,"period":"day","date":"yesterday"` + extra + `}`
	}
	for _, c := range []struct{ tool, connection, arguments string }{
		{"matomo.session-recordings.list", "two", `{"idSite":3}`},
		{"matomo.session-recordings.list", "two", `{}`},
		{"matomo.session-recordings.list", "instance", `{}`},
		{"matomo.session-recordings.list", "two", `{"idSite":1,"callback":"x"}`},
		{"matomo.session-recordings.list", "two", `{"idSite":1,"limit":0}`},
		{"matomo.session-recordings.get", "two", `{"idSite":3,"idSiteHsr":13}`},
		{"matomo.session-recordings.get", "two", `{"idSiteHsr":13}`},
		{"matomo.session-recordings.get", "one", `{"idSite":1,"idSiteHsr":13}`},
		{"matomo.session-recordings.get", "two", `{"idSite":1}`},
		{"matomo.session-recordings.get", "two", `{"idSite":1,"idSiteHsr":0}`},
		{"matomo.session-recordings.get", "two", `{"idSite":1,"idSiteHsr":"13"}`},
		{"matomo.session-recordings.get", "instance", `{"idSite":1,"idSiteHsr":13}`},
		{"matomo.recorded-sessions.list", "two", strings.Replace(listed(""), `"idSite":1`, `"idSite":3`, 1)},
		{"matomo.recorded-sessions.list", "one", listed("")},
		{"matomo.recorded-sessions.list", "two", `{"idSite":1,"period":"day","date":"yesterday"}`},
		{"matomo.recorded-sessions.list", "two", `{"idSite":1,"idSiteHsr":13,"date":"yesterday"}`},
		{"matomo.recorded-sessions.list", "two", `{"idSite":1,"idSiteHsr":13,"period":"day"}`},
		{"matomo.recorded-sessions.list", "two", strings.Replace(listed(""), `"day"`, `"decade"`, 1)},
		{"matomo.recorded-sessions.list", "two", strings.Replace(listed(""), `"yesterday"`, `"last7"`, 1)},
		{"matomo.recorded-sessions.list", "two", listed(`,"segment":"not a segment"`)},
		{"matomo.recorded-sessions.list", "two", listed(`,"idVisit":0`)},
		{"matomo.recorded-sessions.list", "two", listed(`,"idSubtable":5`)},
		{"matomo.recorded-sessions.list", "two", listed(`,"token_auth":"x"`)},
		{"matomo.recorded-sessions.get", "two", `{"idSite":3,"idSiteHsr":13,"idLogHsr":1}`},
		{"matomo.recorded-sessions.get", "one", `{"idSite":1,"idSiteHsr":13,"idLogHsr":1}`},
		{"matomo.recorded-sessions.get", "two", `{"idSite":1,"idSiteHsr":13}`},
		{"matomo.recorded-sessions.get", "two", `{"idSite":1,"idLogHsr":1}`},
		{"matomo.recorded-sessions.get", "two", `{"idSite":1,"idSiteHsr":13,"idLogHsr":0}`},
		{"matomo.recorded-sessions.get", "two", `{"idSite":1,"idSiteHsr":13,"idLogHsr":1,"period":"day"}`},
		{"matomo.recorded-sessions.get", "instance", `{"idSite":1,"idSiteHsr":13,"idLogHsr":1}`},
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

func TestRecordingRequestsCarryTheBearerTokenOnlyInTheHeaderAndResultsHoldNoToken(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, recordingAnswers(nil).handler(t))
	for _, c := range recordingCalls {
		result, err := env.invoke(c.tool, "two", c.arguments)
		if err != nil {
			t.Fatalf("%s: %v", c.tool, err)
		}
		if strings.Contains(strings.ToLower(result), "token") || strings.Contains(result, tokenValue) {
			t.Errorf("%s result holds a token: %s", c.tool, result)
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

func TestRecordedSessionAddressesLoseTheTokenBeforeTheResultLeaves(t *testing.T) {
	row := func(address string) string {
		return `[{"label":"1","idloghsr":5,"first_url":"` + address + `","sessionReplayUrl":"` + address + `"}]`
	}
	for name, tc := range map[string]struct{ address, want string }{
		"plain":      {"index.php?idSite=1&token_auth=" + tokenValue, "index.php?idSite=1"},
		"upper":      {"index.php?Token_Auth=" + tokenValue + "&idSite=1", "index.php?idSite=1"},
		"semicolon":  {"index.php?idSite=1;token_auth=" + tokenValue, "index.php?idSite=1"},
		"unparsable": {"index.php?x=%zz&token_auth=" + tokenValue, ""},
	} {
		var calls []call
		env := newEnvironment(t, &calls, recordingAnswers(answers{"HeatmapSessionRecording.getRecordedSessions": fixed(row(tc.address))}).handler(t))
		result, err := env.invoke("matomo.recorded-sessions.list", "two", recordingCalls[2].arguments)
		var out RecordedSessionsResult
		if err != nil || json.Unmarshal([]byte(result), &out) != nil || len(out.RecordedSessions) != 1 ||
			out.RecordedSessions[0].SessionReplayURL != tc.want || out.RecordedSessions[0].FirstURL != tc.want ||
			strings.Contains(strings.ToLower(result), "token") || strings.Contains(result, tokenValue) {
			t.Errorf("%s: result = %s, err = %v", name, result, err)
		}
	}
}

func TestRecordingToolsMapMatomoErrorsAndTransportFailures(t *testing.T) {
	// The first Matomo method of a tool; the session tools bind the recording first.
	firstOf := map[string]string{"matomo.session-recordings.list": "HeatmapSessionRecording.getSessionRecordings",
		"matomo.session-recordings.get": "HeatmapSessionRecording.getSessionRecording",
		"matomo.recorded-sessions.list": "HeatmapSessionRecording.getSessionRecording",
		"matomo.recorded-sessions.get":  "HeatmapSessionRecording.getSessionRecording"}
	for _, c := range recordingCalls {
		method := firstOf[c.tool]
		for name, tc := range map[string]struct {
			answer      func(call) (*http.Response, error)
			class       provider.Class
			unsupported bool
		}{
			"permission": {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false},
			"object not found": {func(call) (*http.Response, error) {
				return ok2(failure(recordingMissing + bodyCanary))
			}, provider.ClassNotFound, false},
			"method missing": {func(call) (*http.Response, error) {
				return ok2(failure("The method 'HeatmapSessionRecording.x' does not exist or is not available in the module 'HeatmapSessionRecording'." + bodyCanary))
			}, "", true},
			"plugin inactive": {func(call) (*http.Response, error) {
				return ok2(failure("The plugin HeatmapSessionRecording is not activated." + bodyCanary))
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
				return recordingAnswers(nil).handler(t)(in)
			})
			clock := time.Now()
			t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, func() time.Time { return clock },
				func(context.Context, time.Duration) error { return nil })))
			_, err := env.invoke(c.tool, "two", c.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), recordingMissing) ||
				isUnsupported(err) != tc.unsupported || classOf(err) != tc.class || countMethod(calls, method) != 1 {
				t.Errorf("%s %s: err = %v (class %q)", c.tool, name, err, classOf(err))
			}
			if c.tool != "matomo.session-recordings.list" && c.tool != "matomo.session-recordings.get" &&
				countMethod(calls, "HeatmapSessionRecording.getRecordedSessions")+countMethod(calls, "HeatmapSessionRecording.getRecordedSession") != 0 {
				t.Errorf("%s %s: data read after a failed proof: %s", c.tool, name, methods(calls))
			}
		}
	}
}

func TestRecordedSessionReadsMapErrorsOfTheLaterMethods(t *testing.T) {
	for _, c := range []struct{ tool, arguments, method string }{
		{recordingCalls[2].tool, recordingCalls[2].arguments, "getRecordedSessions"},
		{recordingCalls[3].tool, recordingCalls[3].arguments, "getRecordedSession"},
		{recordingCalls[3].tool, recordingCalls[3].arguments, "getEventTypes"},
	} {
		for name, tc := range map[string]struct {
			answer      func(call) *http.Response
			class       provider.Class
			unsupported bool
		}{
			"permission": {func(call) *http.Response { return failure(permissionMsg + bodyCanary) }, provider.ClassPermission, false},
			"not found":  {func(call) *http.Response { return failure(recordingMissing + bodyCanary) }, provider.ClassNotFound, false},
			"missing": {func(call) *http.Response {
				return failure("The method 'HeatmapSessionRecording.x' does not exist or is not available in the module 'HeatmapSessionRecording'.")
			}, "", true},
			"invalid": {fixed(`"text"`), provider.ClassInvalidResponse, false},
		} {
			var calls []call
			env := newEnvironment(t, &calls, recordingAnswers(answers{"HeatmapSessionRecording." + c.method: tc.answer}).handler(t))
			_, err := env.invoke(c.tool, "two", c.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), recordingMissing) ||
				isUnsupported(err) != tc.unsupported || classOf(err) != tc.class {
				t.Errorf("%s %s: err = %v", c.method, name, err)
			}
		}
	}
}

func TestRecordingToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range recordingCalls {
		var calls []call
		env := newEnvironment(t, &calls, with(recordingAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.invoke(c.tool, "two", c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" ||
			lastCall(calls, "API.isPluginActivated").form.Get("pluginName") != heatmapsPlugin {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}

func TestRecordingTextAndSessionsAreShortened(t *testing.T) {
	long := strings.Repeat("n", 6000)
	body := `{"idsitehsr":13,"idsite":1,"name":"` + long + `","match_page_rules":[{"value":"` + long + `"}]}`
	row := `[{"label":"` + long + `","first_url":"` + long + `","location_city":"` + long + `","idloghsr":5}]`
	var calls []call
	env := newEnvironment(t, &calls, recordingAnswers(answers{
		"HeatmapSessionRecording.getSessionRecording": fixed(body),
		"HeatmapSessionRecording.getRecordedSessions": fixed(row),
		"HeatmapSessionRecording.getRecordedSession": fixed(`{"events":[{"event_type":2,"selector":"` + long + `","extra":[` +
			strings.Repeat("1,", 200) + `1]}]}`),
	}).handler(t))
	result, err := env.invoke("matomo.session-recordings.get", "two", recordingCalls[1].arguments)
	var recording SessionRecording
	if err != nil || json.Unmarshal([]byte(result), &recording) != nil || len(recording.Name) != maxStringLength ||
		len(recording.MatchPageRules) != 1 || len(recording.MatchPageRules[0].Value) != maxUrlLength {
		t.Errorf("result length %d, err = %v", len(result), err)
	}
	result, err = env.invoke(recordingCalls[2].tool, "two", recordingCalls[2].arguments)
	var sessions RecordedSessionsResult
	if err != nil || json.Unmarshal([]byte(result), &sessions) != nil || len(sessions.RecordedSessions) != 1 ||
		len(sessions.RecordedSessions[0].Label) != maxStringLength || len(sessions.RecordedSessions[0].FirstURL) != maxUrlLength ||
		len(sessions.RecordedSessions[0].LocationCity) != maxStringLength {
		t.Errorf("result length %d, err = %v", len(result), err)
	}
	result, err = env.invoke(recordingCalls[3].tool, "two", recordingCalls[3].arguments)
	var one struct{ Session map[string]any }
	if err != nil || json.Unmarshal([]byte(result), &one) != nil {
		t.Fatalf("result length %d, err = %v", len(result), err)
	}
	events, _ := one.Session["events"].([]any)
	event, _ := events[0].(map[string]any)
	extra, _ := event["extra"].([]any)
	if len(event["selector"].(string)) != maxUrlLength || len(extra) != maxResultEntries {
		t.Errorf("event = %.200v", event)
	}
}

func TestRecordedSessionsGetCutsTextOfEventsWithoutAReadableType(t *testing.T) {
	body := `{"events":[` +
		`{"time_since_load":10,"selector":"input#a","text":"no-type-secret","Value":"no-type-value","x":1},` +
		`{"event_type":"form","selector":"input#b","TEXT":"word-type-secret"},` +
		`{"event_type":2,"time_since_load":20,"selector":"a#c","text":"click-label"},` +
		`{"title":"not an event","text":"plain-text"}]}`
	var calls []call
	env := newEnvironment(t, &calls, recordingAnswers(answers{"HeatmapSessionRecording.getRecordedSession": fixed(body)}).handler(t))
	result, err := env.invoke("matomo.recorded-sessions.get", "two", recordingCalls[3].arguments)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"no-type-secret", "no-type-value", "word-type-secret"} {
		if strings.Contains(result, secret) {
			t.Errorf("result holds %q: %s", secret, result)
		}
	}
	var out struct {
		Session           struct{ Events []map[string]any }
		TypedTextWithheld int `json:"typed_text_withheld"`
	}
	if err := json.Unmarshal([]byte(result), &out); err != nil || len(out.Session.Events) != 4 || out.TypedTextWithheld != 2 {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	events := out.Session.Events
	if events[0]["x"] != float64(1) || events[0]["selector"] != "input#a" || events[1]["selector"] != "input#b" ||
		events[2]["text"] != "click-label" || events[3]["text"] != "plain-text" {
		t.Errorf("events = %v", events)
	}
}
