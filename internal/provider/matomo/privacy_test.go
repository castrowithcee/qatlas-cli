package matomo

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

func pluginsOn() answers {
	return with(versionOnly("5.14.2"), answers{
		"API.isPluginActivated": func(call) *http.Response { return ok(`{"value":true}`) },
	})
}

func subjectRow(site, visit string) string {
	return `{"lastActionDateTime":"2026-10-24 10:02:05","idVisit":"` + visit + `","idSite":"` + site + `",` +
		`"siteName":"Shop","visitorId":"0123456789abcdef","visitIp":"192.0.2.7","userId":"alice@example.test",` +
		`"deviceType":"Smartphone","deviceModel":"A1","deviceTypeIcon":"plugins/Morpheus/icons/phone.png",` +
		`"operatingSystem":"Android 14","operatingSystemIcon":"os.png","browser":"Chrome 120",` +
		`"browserFamilyDescription":"Blink","browserIcon":"ch.png","country":"Germany","region":"Berlin",` +
		`"countryFlag":"flags/de.png","fingerprint":"must-not-appear","idSomething":"x"}`
}

func findAnswers(rows ...string) answers {
	return with(pluginsOn(), answers{
		"PrivacyManager.findDataSubjects": func(call) *http.Response { return ok("[" + strings.Join(rows, ",") + "]") },
	})
}

func TestDataSubjectsFindUsesTheFixedMethod(t *testing.T) {
	noLiveSpacing(t)
	var calls []call
	env := newEnvironment(t, &calls, findAnswers(subjectRow("2", "11"), subjectRow("3", "12"), subjectRow("2", "13")).handler(t))
	result, err := env.invoke("matomo.data-subjects.find", "one", `{"segment":"visitorId==0123456789abcdef"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id_visit":11`, `"id_visit":13`, `"id_site":2`, `"visitor_id":"0123456789abcdef"`,
		`"visit_ip":"192.0.2.7"`, `"user_id":"alice@example.test"`, `"country_flag":"flags/de.png"`,
		`"browser_icon":"ch.png"`, `"has_more":false`} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %s in %s", want, result)
		}
	}
	if strings.Contains(result, `"id_visit":12`) || strings.Contains(result, "must-not-appear") ||
		strings.Contains(result, "matomo_limit_reached") {
		t.Errorf("result = %s", result)
	}
	got := calls[len(calls)-1]
	if got.api() != "PrivacyManager.findDataSubjects" || got.form.Get("idSite") != "2" ||
		got.form.Get("segment") != "visitorId==0123456789abcdef" || got.auth != "Bearer "+tokenValue ||
		got.form.Has("token_auth") || strings.Contains(got.rawQuery, tokenValue) || got.form.Has("filter_limit") {
		t.Errorf("call = %+v", got)
	}
}

func TestDataSubjectsFindPagesAndFlagsMatomoLimit(t *testing.T) {
	noLiveSpacing(t)
	rows := make([]string, 0, maxFindRows)
	for i := 1; i <= maxFindRows; i++ {
		rows = append(rows, subjectRow("1", strconv.Itoa(i)))
	}
	var calls []call
	env := newEnvironment(t, &calls, findAnswers(rows...).handler(t))
	first, err := env.invoke("matomo.data-subjects.find", "open", `{"idSite":1,"segment":"a==b","limit":100}`)
	if err != nil || !strings.Contains(first, `"matomo_limit_reached":true`) || !strings.Contains(first, `"has_more":true`) {
		t.Fatalf("first = %.200s, err = %v", first, err)
	}
	cursor := between(first, `"next_cursor":"`, `"`)
	if _, err := env.invoke("matomo.data-subjects.find", "open", `{"idSite":1,"segment":"a==b","cursor":"`+cursor+`"}`); err != nil {
		t.Fatal(err)
	}
	for name, arguments := range map[string]string{
		"other segment": `{"idSite":1,"segment":"a==c","cursor":"` + cursor + `"}`,
		"other website": `{"idSite":2,"segment":"a==b","cursor":"` + cursor + `"}`,
		"other limit":   `{"idSite":1,"segment":"a==b","limit":50,"cursor":"` + cursor + `"}`,
	} {
		if _, err := env.invoke("matomo.data-subjects.find", "open", arguments); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestDataSubjectsFindBoundsStrings(t *testing.T) {
	noLiveSpacing(t)
	long := strings.Repeat("x", 3000)
	var calls []call
	env := newEnvironment(t, &calls, findAnswers(`{"idSite":"1","idVisit":"5","siteName":"`+long+`","browserIcon":"`+long+`"}`).handler(t))
	result, err := env.invoke("matomo.data-subjects.find", "open", `{"idSite":1,"segment":"a==b"}`)
	if err != nil || strings.Contains(result, strings.Repeat("x", maxStringLength+1)) {
		t.Fatalf("result = %.100s, err = %v", result, err)
	}
}

func TestDataSubjectsToolsRefuseBeforeSecretAndRequest(t *testing.T) {
	noLiveSpacing(t)
	var calls []call
	env := newEnvironment(t, &calls, pluginsOn().handler(t))
	long := strings.Repeat("s", maxSegment+1)
	for _, tc := range []struct{ tool, connection, arguments string }{
		{"find", "two", `{"segment":"a==b"}`}, {"find", "two", `{"idSite":3,"segment":"a==b"}`},
		{"find", "one", `{"idSite":1,"segment":"a==b"}`}, {"find", "instance", `{"idSite":1,"segment":"a==b"}`},
		{"find", "one", `{}`}, {"find", "one", `{"segment":""}`}, {"find", "one", "{\"segment\":\"a\\nb\"}"},
		{"find", "one", `{"segment":"` + long + `"}`}, {"find", "one", `{"segment":"a==b","limit":101}`},
		{"export", "two", `{"visits":[{"idSite":1,"idVisit":4},{"idSite":3,"idVisit":5}]}`},
		{"export", "one", `{"visits":[{"idSite":1,"idVisit":4}]}`}, {"export", "instance", `{"visits":[{"idSite":1,"idVisit":4}]}`},
		{"export", "open", `{"visits":[]}`}, {"export", "open", `{}`}, {"export", "open", `{"visits":[{"idSite":1}]}`},
		{"export", "open", `{"visits":[{"idSite":0,"idVisit":4}]}`}, {"export", "open", `{"visits":[{"idSite":1,"idVisit":-1}]}`},
		{"export", "open", `{"visits":[{"idSite":1,"idVisit":4},{"idSite":1,"idVisit":4}]}`},
		{"export", "open", `{"visits":[{"idSite":1,"idVisit":4}],"limit":26}`},
		{"export", "open", `{"visits":[{"idSite":1,"idVisit":4}],"cursor":"AAAA"}`},
	} {
		_, err := env.invoke("matomo.data-subjects."+tc.tool, tc.connection, tc.arguments)
		if err == nil {
			t.Errorf("%s %s %s: accepted", tc.tool, tc.connection, tc.arguments)
		} else if strings.Contains(err.Error(), "site/") || strings.Contains(err.Error(), "idSite 3") {
			t.Errorf("%s %s: refusal names a target: %v", tc.tool, tc.arguments, err)
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("secret reads = %d, requests = %d, want none", *env.reads, len(calls))
	}
}

func TestDataSubjectsFindNeedsTheLivePlugin(t *testing.T) {
	noLiveSpacing(t)
	var calls []call
	env := newEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"API.isPluginActivated": func(call) *http.Response { return ok(`{"value":false}`) },
	}).handler(t))
	_, err := env.invoke("matomo.data-subjects.find", "open", `{"idSite":1,"segment":"a==b"}`)
	if !isUnsupported(err) || !strings.Contains(err.Error(), "Live") || len(calls) != 2 {
		t.Fatalf("err = %v, calls = %d", err, len(calls))
	}
}

func exportTablesAnswer() string {
	return `{"log_visit":[{"idvisit":"11","idsite":"1","idvisitor":"abcdef","location_ip":"c0000207","user_id":"alice",` +
		`"referer_url":"https://r.example.test/?token_auth=` + "secret-canary" + `","config_id":"cfg","x_nothing":null},` +
		`{"idvisit":"99","idsite":"1","config_id":"other visit"},{"idvisit":"11","idsite":"3","config_id":"other site"}],` +
		`"log_link_visit_action":[{"idlink_va":"1","idvisit":"11","idsite":"1","api_token":"column-canary",` +
		`"custom_var_v1":"page","nested":{"a":1,"b":"two"},"session_secret":"s","count":7,"flag":true}],` +
		`"log_action_log_link_visit_action_idaction_url":[{"idaction":"5","name":"https://shop.example.test/p"}],` +
		`"myplugin":["plain text export",{"password":"x"}]}`
}

func exportAnswers(body string) answers {
	return with(pluginsOn(), answers{
		"PrivacyManager.exportDataSubjects": func(call) *http.Response { return ok(body) },
	})
}

func TestDataSubjectsExportEncodesTheListAndFiltersRows(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, exportAnswers(exportTablesAnswer()).handler(t))
	result, err := env.invoke("matomo.data-subjects.export", "two", `{"visits":[{"idSite":1,"idVisit":11},{"idSite":2,"idVisit":12}]}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"name":"log_visit"`, `"row_count":1`, `"idvisitor":"abcdef"`, `"location_ip":"c0000207"`,
		`"user_id":"alice"`, `"custom_var_v1":"page"`, `"count":7`, `"flag":true`, `"name":"https://shop.example.test/p"`,
		`"value":"plain text export"`, `"has_more":false`, `"id_site":2,"id_visit":12`} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %s in %s", want, result)
		}
	}
	for _, unwanted := range []string{"other visit", "other site", "secret-canary", "column-canary", "api_token", "session_secret",
		"password", "x_nothing", "truncated", "next_cursor"} {
		if strings.Contains(result, unwanted) {
			t.Errorf("result carries %q: %s", unwanted, result)
		}
	}
	got := calls[len(calls)-1]
	if got.api() != "PrivacyManager.exportDataSubjects" || got.form.Get("visits[0][idsite]") != "1" ||
		got.form.Get("visits[0][idvisit]") != "11" || got.form.Get("visits[1][idsite]") != "2" ||
		got.form.Get("visits[1][idvisit]") != "12" || got.auth != "Bearer "+tokenValue || got.form.Has("token_auth") ||
		strings.Contains(got.rawQuery, tokenValue) || len(got.form) != 7 {
		t.Errorf("call = %+v", got)
	}
}

func TestDataSubjectsExportEmptyAnswer(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, exportAnswers(`[]`).handler(t))
	result, err := env.invoke("matomo.data-subjects.export", "open", `{"visits":[{"idSite":1,"idVisit":11}]}`)
	if err != nil || strings.Contains(result, "truncated") || !strings.Contains(result, `"id_visit":11`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}

func TestDataSubjectsExportCapsTheAnswer(t *testing.T) {
	rows := make([]string, 0, 400)
	for i := 0; i < 400; i++ {
		rows = append(rows, `{"idvisit":"11","idsite":"1","idlink_va":"`+strconv.Itoa(i)+`","text":"`+strings.Repeat("y", 3000)+`"}`)
	}
	var calls []call
	env := newEnvironment(t, &calls, exportAnswers(`{"log_link_visit_action":[`+strings.Join(rows, ",")+`],`+
		`"log_visit":[{"idvisit":"11","idsite":"1","config_id":"c"}]}`).handler(t))
	result, err := env.invoke("matomo.data-subjects.export", "open", `{"visits":[{"idSite":1,"idVisit":11}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) > maxExportBytes+4096 || !strings.Contains(result, `"truncated":true`) ||
		!strings.Contains(result, `"row_count":400`) {
		t.Errorf("len = %d, result = %.300s", len(result), result)
	}
	if strings.Contains(result, strings.Repeat("y", maxExportValue+1)) {
		t.Error("a value is not shortened")
	}
}

func TestDataSubjectsExportCursorOverTheInputList(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, exportAnswers(`[]`).handler(t))
	var list []string
	for i := 1; i <= 25; i++ {
		list = append(list, `{"idSite":1,"idVisit":`+strconv.Itoa(i)+`}`)
	}
	input := `[` + strings.Join(list, ",") + `]`
	first, err := env.invoke("matomo.data-subjects.export", "open", `{"visits":`+input+`}`)
	if err != nil || !strings.Contains(first, `"has_more":true`) || strings.Contains(first, `"id_visit":11,`) {
		t.Fatalf("first = %s, err = %v", first, err)
	}
	if got := calls[len(calls)-1]; got.form.Get("visits[9][idvisit]") != "10" || got.form.Has("visits[10][idvisit]") {
		t.Errorf("call = %+v", got)
	}
	cursor := between(first, `"next_cursor":"`, `"`)
	second, err := env.invoke("matomo.data-subjects.export", "open", `{"visits":`+input+`,"cursor":"`+cursor+`"}`)
	if err != nil || !strings.Contains(second, `"id_visit":11`) || !strings.Contains(second, `"id_visit":20`) {
		t.Fatalf("second = %s, err = %v", second, err)
	}
	if got := calls[len(calls)-1]; got.form.Get("visits[0][idvisit]") != "11" {
		t.Errorf("call = %+v", got)
	}
	third := between(second, `"next_cursor":"`, `"`)
	last, err := env.invoke("matomo.data-subjects.export", "open", `{"visits":`+input+`,"cursor":"`+third+`"}`)
	if err != nil || strings.Contains(last, "next_cursor") || strings.Contains(last, `"has_more":true`) {
		t.Fatalf("last = %s, err = %v", last, err)
	}
	other := `[` + strings.Join(append(slices.Clone(list[:24]), `{"idSite":1,"idVisit":99}`), ",") + `]`
	before := len(calls)
	for name, arguments := range map[string]string{
		"other list":  `{"visits":` + other + `,"cursor":"` + cursor + `"}`,
		"other limit": `{"visits":` + input + `,"limit":5,"cursor":"` + cursor + `"}`,
		"altered":     `{"visits":` + input + `,"cursor":"` + cursor[:len(cursor)-2] + `AA"}`,
	} {
		if _, err := env.invoke("matomo.data-subjects.export", "open", arguments); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if len(calls) != before {
		t.Errorf("a refused cursor caused %d requests", len(calls)-before)
	}
	// The cursor is also bound to the targets of the connection.
	if _, err := env.invoke("matomo.data-subjects.export", "two", `{"visits":`+input+`,"cursor":"`+cursor+`"}`); !isInvalidRequest(err) {
		t.Errorf("other targets: err = %v", err)
	}
}

func siteOrNull(id string) string {
	if id == "" {
		return "null"
	}
	return id
}

func settingsAnswers() answers {
	return with(pluginsOn(), answers{
		"PrivacyManager.getAnonymisationSettings": func(c call) *http.Response {
			return ok(`{"useAnonymizedIpForVisitEnrichment":false,"ipAddressMaskLength":2,"doNotTrackEnabled":"1",` +
				`"ipAnonymizerEnabled":true,"forceCookielessTracking":false,"anonymizeUserId":true,"anonymizeOrderId":false,` +
				`"anonymizeReferrer":"exclude_query","randomizeConfigId":false,"useSiteSpecificSettings":true,` +
				`"maskLengthOptions":[{"key":"1","value":"1 byte","description":""},{"key":"2","value":"2 bytes","description":"Recommended"}],` +
				`"useAnonymizedIpForVisitEnrichmentOptions":[{"key":"1","value":"Yes","description":"d"}],` +
				`"referrerAnonymizationOptions":{"exclude_query":"Remove query","exclude_all":"Remove all"},` +
				`"trackerFileName":"matomo.js","trackerWritable":true,"brand_new":"must-not-appear",` +
				`"extraMetadata":{"anonymizeUserId":{"compliancePolicyControlled":{"cnil_v1":{"requiredValue":true,` +
				`"effectiveValue":true,"constraintType":"min","scope":"global","policyTitle":"CNIL"}},"idSite":` + siteOrNull(c.form.Get("idSiteSpecific")) + `}}}`)
		},
		"PrivacyManager.getAvailableVisitColumnsToAnonymize": func(call) *http.Response {
			return ok(`[{"column_name":"visit_ip","default_value":"0.0.0.0"},{"column_name":"user_id","default_value":null},` +
				`{"column_name":"campaign","default_value":0},{"default_value":"x"}]`)
		},
		"PrivacyManager.getAvailableLinkVisitActionColumnsToAnonymize": func(call) *http.Response {
			return ok(`[{"column_name":"search_cat","default_value":""}]`)
		},
	})
}

func TestPrivacySettingsGetModes(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, settingsAnswers().handler(t))
	check := func(connection, arguments, method string, wants ...string) {
		t.Helper()
		result, err := env.invoke("matomo.privacy-settings.get", connection, arguments)
		if err != nil {
			t.Fatalf("%s %s: %v", connection, arguments, err)
		}
		for _, want := range wants {
			if !strings.Contains(result, want) {
				t.Errorf("%s: missing %s in %s", arguments, want, result)
			}
		}
		if strings.Contains(result, "must-not-appear") {
			t.Errorf("%s: %s", arguments, result)
		}
		got := calls[len(calls)-1]
		if got.api() != method || got.auth != "Bearer "+tokenValue || got.form.Has("token_auth") || strings.Contains(got.rawQuery, tokenValue) {
			t.Errorf("%s: call = %+v", arguments, got)
		}
	}
	check("one", `{"idSite":2}`, "PrivacyManager.getAnonymisationSettings", `"id_site":2`, `"ip_address_mask_length":2`,
		`"do_not_track_enabled":true`, `"anonymize_referrer":"exclude_query"`, `"use_site_specific_settings":true`,
		`"mask_length_options":[{"key":"1","value":"1 byte"}`, `{"key":"exclude_all","value":"Remove all"}`,
		`"tracker_file_name":"matomo.js"`, `"setting":"anonymizeUserId"`, `"policy":"cnil_v1"`, `"required_value":true`,
		`"policy_title":"CNIL"`)
	if got := calls[len(calls)-1]; got.form.Get("idSiteSpecific") != "2" {
		t.Errorf("call = %+v", got)
	}
	check("mixed", `{}`, "PrivacyManager.getAnonymisationSettings", `"section":"settings"`, `"ip_anonymizer_enabled":true`)
	if got := calls[len(calls)-1]; got.form.Has("idSiteSpecific") {
		t.Errorf("global call = %+v", got)
	}
	check("mixed", `{"section":"visit-columns"}`, "PrivacyManager.getAvailableVisitColumnsToAnonymize",
		`{"column_name":"campaign","default_value":0}`, `{"column_name":"user_id"}`,
		`{"column_name":"visit_ip","default_value":"0.0.0.0"}`)
	check("mixed", `{"section":"action-columns"}`, "PrivacyManager.getAvailableLinkVisitActionColumnsToAnonymize",
		`"action_columns":[{"column_name":"search_cat","default_value":""}]`)
}

func TestPrivacySettingsGetRefusesBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, settingsAnswers().handler(t))
	for _, tc := range []struct{ connection, arguments string }{
		{"open", `{}`}, {"general", `{"section":"visit-columns"}`}, {"one", `{"section":"action-columns"}`},
		{"two", `{}`}, {"two", `{"idSite":3}`}, {"one", `{"idSite":1}`}, {"mixed", `{"idSite":1}`},
		{"mixed", `{"section":"visit-columns","idSite":2}`}, {"open", `{"section":"nope","idSite":1}`},
		{"instance", `{"idSite":1}`}, {"two", `{"section":"settings","idSite":0}`},
	} {
		_, err := env.invoke("matomo.privacy-settings.get", tc.connection, tc.arguments)
		if err == nil {
			t.Errorf("%s %s: accepted", tc.connection, tc.arguments)
		} else if strings.Contains(err.Error(), "site/") && strings.Contains(tc.arguments, "3") {
			t.Errorf("%s %s: refusal names a target: %v", tc.connection, tc.arguments, err)
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("secret reads = %d, requests = %d, want none", *env.reads, len(calls))
	}
	if ok, _ := reaches(privacySettingsGet.ID, []string{"instance"}); ok {
		t.Error("an instance-only binding is offered the privacy settings")
	}
}

func complianceAnswers() answers {
	return with(pluginsOn(), answers{
		"PrivacyManager.getCompliancePolicies": func(call) *http.Response {
			return ok(`[{"id":"gdpr_v1","title":"GDPR","description":"EU","hidden":"x"},{"id":"cnil_v1","title":"CNIL","description":"FR"}]`)
		},
		"PrivacyManager.getComplianceStatus": func(c call) *http.Response {
			return ok(`{"complianceModeEnforced":true,"complianceConfigControlled":false,"complianceRequirements":[` +
				`{"name":"IP anonymization","value":"compliant","notes":"on"},{"name":"Cookies","value":"unknown","notes":"check"}]}`)
		},
		"PrivacyManager.getCompliancePolicySettings": func(c call) *http.Response {
			return ok(`{"policy":"cnil_v1","title":"CNIL","description":"FR","configControlled":false,"policyEnforced":false,` +
				`"settings":[{"id":"ip","name":"IP","whatItDoes":"masks","impact":"low","status":"enforced","enforced":true,` +
				`"toggleable":true,"section":"settings"},{"id":"cookies","name":"Cookies","whatItDoes":"n","impact":"",` +
				`"status":"manual","enforced":null,"toggleable":false,"section":"external"}]}`)
		},
	})
}

func TestComplianceGetModes(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, complianceAnswers().handler(t))
	for _, tc := range []struct {
		arguments, method string
		form              map[string]string
		wants             []string
	}{
		{`{}`, "PrivacyManager.getCompliancePolicies", nil, []string{`"id":"cnil_v1"`, `"id":"gdpr_v1"`}},
		{`{"kind":"status","policy":"cnil_v1","idSite":2}`, "PrivacyManager.getComplianceStatus",
			map[string]string{"idSite": "2", "complianceType": "cnil_v1"},
			[]string{`"enforced":true`, `"config_controlled":false`, `"name":"Cookies","notes":"check","value":"unknown"`}},
		{`{"kind":"status","policy":"cnil_v1","all_sites":true}`, "PrivacyManager.getComplianceStatus",
			map[string]string{"idSite": "all", "complianceType": "cnil_v1"}, []string{`"requirements"`}},
		{`{"kind":"settings","policy":"cnil_v1","all_sites":true}`, "PrivacyManager.getCompliancePolicySettings",
			map[string]string{"idSite": "all", "compliancePolicy": "cnil_v1"},
			[]string{`"policy_enforced":false`, `"what_it_does":"masks"`, `"enforced":true`, `"toggleable":false`}},
		{`{"kind":"settings","policy":"cnil_v1","idSite":2}`, "PrivacyManager.getCompliancePolicySettings",
			map[string]string{"idSite": "2", "compliancePolicy": "cnil_v1"}, nil},
	} {
		result, err := env.invoke("matomo.compliance.get", "mixed", tc.arguments)
		if err != nil {
			t.Fatalf("%s: %v", tc.arguments, err)
		}
		for _, want := range tc.wants {
			if !strings.Contains(result, want) {
				t.Errorf("%s: missing %s in %s", tc.arguments, want, result)
			}
		}
		if strings.Contains(result, "hidden") {
			t.Errorf("%s: %s", tc.arguments, result)
		}
		got := calls[len(calls)-1]
		if got.api() != tc.method || got.auth != "Bearer "+tokenValue || got.form.Has("token_auth") || strings.Contains(got.rawQuery, tokenValue) {
			t.Errorf("%s: call = %+v", tc.arguments, got)
		}
		for name, value := range tc.form {
			if got.form.Get(name) != value {
				t.Errorf("%s: %s = %q, want %q", tc.arguments, name, got.form.Get(name), value)
			}
		}
	}
	if result, _ := env.invoke("matomo.compliance.get", "instance", `{"kind":"settings","policy":"cnil_v1","all_sites":true}`); strings.Contains(result, `"enforced":false`) {
		t.Errorf("a null flag reads as false: %s", result)
	}
}

func TestComplianceGetRefusesBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, complianceAnswers().handler(t))
	for _, tc := range []struct{ connection, arguments string }{
		{"open", `{}`}, {"general", `{}`}, {"one", `{}`}, {"two", `{"kind":"status","policy":"p","idSite":1}`},
		{"instance", `{"kind":"status","policy":"p","idSite":1}`}, {"mixed", `{"kind":"status","policy":"p","idSite":1}`},
		{"mixed", `{"kind":"nope"}`}, {"mixed", `{"policy":"p"}`}, {"mixed", `{"all_sites":true}`},
		{"mixed", `{"kind":"status"}`}, {"mixed", `{"kind":"status","policy":"p"}`},
		{"mixed", `{"kind":"status","policy":"p","idSite":2,"all_sites":true}`}, {"mixed", `{"kind":"settings","policy":"a b","all_sites":true}`},
		{"mixed", `{"kind":"settings","policy":"","all_sites":true}`}, {"mixed", `{"kind":"settings","policy":"p","idSite":0}`},
	} {
		_, err := env.invoke("matomo.compliance.get", tc.connection, tc.arguments)
		if err == nil {
			t.Errorf("%s %s: accepted", tc.connection, tc.arguments)
		} else if strings.Contains(err.Error(), "site/") && strings.Contains(tc.arguments, `"idSite":1`) {
			t.Errorf("%s %s: refusal names a target: %v", tc.connection, tc.arguments, err)
		}
	}
	if *env.reads != 0 || len(calls) != 0 {
		t.Fatalf("secret reads = %d, requests = %d, want none", *env.reads, len(calls))
	}
	if ok, forms := reaches(complianceGet.ID, []string{"site"}); ok || !slices.Equal(forms, []string{"instance"}) {
		t.Errorf("reaches = %v %v", ok, forms)
	}
	if ok, _ := reaches(complianceGet.ID, []string{"site/1", "instance"}); !ok {
		t.Error("an instance binding is not offered compliance.get")
	}
}

func TestComplianceGetMapsAMissingGranularFeatureAndUnknownPolicy(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, with(pluginsOn(), answers{
		"PrivacyManager.getCompliancePolicySettings": func(call) *http.Response {
			return failure("Granular compliance configuration is not enabled" + bodyCanary)
		},
		"PrivacyManager.getComplianceStatus": func(call) *http.Response { return failure("Invalid compliance type" + bodyCanary) },
	}).handler(t))
	_, err := env.invoke("matomo.compliance.get", "instance", `{"kind":"settings","policy":"p","all_sites":true}`)
	if !isUnsupported(err) || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("settings: err = %v", err)
	}
	_, err = env.invoke("matomo.compliance.get", "instance", `{"kind":"status","policy":"p","all_sites":true}`)
	if classOf(err) != provider.ClassNotFound || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("status: err = %v", err)
	}
}

// privacyTools are the four tools with a connection and arguments that pass every check before a request.
var privacyTools = []struct{ id, connection, arguments string }{
	{"matomo.data-subjects.find", "open", `{"idSite":1,"segment":"visitorId==0123456789abcdef"}`},
	{"matomo.data-subjects.export", "open", `{"visits":[{"idSite":1,"idVisit":4}]}`},
	{"matomo.privacy-settings.get", "open", `{"idSite":1}`},
	{"matomo.compliance.get", "instance", `{}`},
}

func TestPrivacyToolsMapErrorsAndNeverLeakTheMessage(t *testing.T) {
	noLiveSpacing(t)
	passthrough := func(c call, body func() (*http.Response, error)) (*http.Response, error) {
		switch c.api() {
		case "API.getMatomoVersion":
			return ok(versionAnswer), nil
		case "API.isPluginActivated":
			return ok(`{"value":true}`), nil
		}
		return body()
	}
	for _, tool := range privacyTools {
		for name, tc := range map[string]struct {
			message string
			class   provider.Class
			missing bool
		}{
			"permission":     {"You can't access this resource as it requires 'superuser' access.", provider.ClassPermission, false},
			"not found":      {"An unexpected website was found in the request.", provider.ClassNotFound, false},
			"method missing": {"The method 'PrivacyManager.x' does not exist or is not available in the module 'PrivacyManager'.", "", true},
			"plugin off":     {"The plugin PrivacyManager is not activated.", "", true},
			"other":          {"Something went wrong", provider.ClassInvalidResponse, false},
		} {
			var calls []call
			env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
				return passthrough(c, func() (*http.Response, error) { return failure(tc.message + bodyCanary), nil })
			})
			_, err := env.invoke(tool.id, tool.connection, tool.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || isUnsupported(err) != tc.missing || classOf(err) != tc.class {
				t.Errorf("%s %s: err = %v", tool.id, name, err)
			}
		}
		for status, want := range map[int]provider.Class{429: provider.ClassRateLimited, 401: provider.ClassAuth} {
			var calls []call
			env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
				return passthrough(c, func() (*http.Response, error) { return respond(status, bodyCanary), nil })
			})
			if _, err := env.invoke(tool.id, tool.connection, tool.arguments); classOf(err) != want || strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("%s status %d: err = %v", tool.id, status, err)
			}
		}
		for name, body := range map[string]string{"html": `<html>` + bodyCanary + `</html>`, "wrong shape": `"` + bodyCanary + `"`} {
			var calls []call
			env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
				return passthrough(c, func() (*http.Response, error) { return ok(body), nil })
			})
			if _, err := env.invoke(tool.id, tool.connection, tool.arguments); classOf(err) != provider.ClassInvalidResponse ||
				strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("%s %s: err = %v", tool.id, name, err)
			}
		}
		var calls []call
		env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
			return passthrough(c, func() (*http.Response, error) { return nil, context.DeadlineExceeded })
		})
		if _, err := env.invoke(tool.id, tool.connection, tool.arguments); classOf(err) != provider.ClassTimeout {
			t.Errorf("%s timeout: err = %v", tool.id, err)
		}
	}
}

func TestPrivacyRateLimitIsNotRetried(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, func(c call) (*http.Response, error) {
		if c.api() == "API.getMatomoVersion" {
			return ok(versionAnswer), nil
		}
		return respond(429, ""), nil
	})
	_, err := env.invoke("matomo.compliance.get", "instance", `{}`)
	if classOf(err) != provider.ClassRateLimited || len(calls) != 2 {
		t.Fatalf("err = %v, calls = %d, want the version and one refused plugin read", err, len(calls))
	}
}

func TestPrivacyToolsAreClassifiedAndInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, tool := range privacyTools {
		descriptor, _, found := reg.Lookup(tool.id)
		if !found {
			t.Fatalf("%s is not registered", tool.id)
		}
		risk := descriptor.Risk
		want := dataSubjectsSensitive
		if strings.Contains(tool.id, "settings") || strings.Contains(tool.id, "compliance") {
			want = instanceSensitive
		}
		if risk.DataSensitivity != want || !risk.OpenWorld || string(risk.Effect) != "read" ||
			string(risk.Idempotency) != "safe" || string(risk.Confirmation) != "none" {
			t.Errorf("%s risk = %+v", tool.id, risk)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if strings.Contains(id, "data-subjects") || strings.Contains(id, "privacy-settings") || strings.Contains(id, "compliance") {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
}
