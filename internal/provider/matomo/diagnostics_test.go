package matomo

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const diagConnections = `
  dg-admin: {service: matomo, credential: token, tools: [matomo.brute-force-ips.unblock], targets: [site/1, instance]}
  dg-instonly: {service: matomo, credential: token, tools: [matomo.brute-force-ips.unblock], targets: [instance]}
  dg-noinst: {service: matomo, credential: token, tools: [matomo.brute-force-ips.unblock], targets: [site/1]}
  dg-effects: {service: matomo, credential: token, effects: [read, update], targets: [site/1, instance]}
`

func newDiagEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(diagConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func diagAnswers(t *testing.T, extra answers) func(call) (*http.Response, error) {
	return with(versionOnly("5.14.2"), extra).handler(t)
}

const seoFixture = `[{"label":"Bing Indexed Pages","id":"indexed-pages-bing","rank":"1,234","logo":"plugins/SEO/images/bing.png",
"logo_link":"https://www.bing.com/","logo_tooltip":"Pages indexed","rank_suffix":""},
{"label":"Domain Age","id":"domain-age","rank":"3 years","logo":"plugins/SEO/images/whois.png","logo_link":"","logo_tooltip":"","rank_suffix":""}]`

func TestDiagnosticToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	for id, want := range map[string]struct {
		effect    capability.Effect
		confirm   capability.Confirmation
		allowList bool
	}{
		"matomo.seo.rank":                      {capability.EffectRead, capability.ConfirmationNone, false},
		"matomo.ip-locations.get":              {capability.EffectRead, capability.ConfirmationNone, false},
		"matomo.js-tracker-install.get-status": {capability.EffectRead, capability.ConfirmationNone, false},
		"matomo.brute-force-ips.unblock":       {capability.EffectUpdate, capability.ConfirmationRequired, true},
	} {
		d, _, found := reg.Lookup(id)
		if !found || d.Risk.Effect != want.effect || d.Risk.Confirmation != want.confirm || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity == "" || d.RequiresToolAllowList != want.allowList {
			t.Errorf("%s: found %t, %+v", id, found, d)
		}
	}
	location, _, _ := reg.Lookup("matomo.ip-locations.get")
	if location.Risk.DataSensitivity != liveSensitivity {
		t.Errorf("ip-locations sensitivity = %s", location.Risk.DataSensitivity)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		joined := " " + strings.Join(profile.Tools, " ") + " "
		if strings.Contains(joined, " matomo.brute-force-ips.unblock ") {
			t.Errorf("profile %s selects unblock", profile.ID)
		}
		if strings.Contains(joined, " matomo.ip-locations.get ") != (profile.ID == "live") {
			t.Errorf("profile %s: ip-locations placement", profile.ID)
		}
		if !strings.Contains(joined, " matomo.seo.rank ") || !strings.Contains(joined, " matomo.js-tracker-install.get-status ") {
			t.Errorf("profile %s lacks a diagnostic read", profile.ID)
		}
	}
}

func TestSEORankSendsOnlyTheHostAndReadsEveryMetric(t *testing.T) {
	var calls []call
	env := newDiagEnvironment(t, &calls, diagAnswers(t, answers{"SEO.getRank": fixed(seoFixture)}))
	for _, url := range []string{"https://WWW.Example.test:8443/a/b?q=1", "www.example.test", "http://www.example.test."} {
		calls = nil
		result, err := env.invoke("matomo.seo.rank", "open", `{"url":"`+url+`"}`)
		var out SEORankResult
		_ = json.Unmarshal([]byte(result), &out)
		if err != nil || out.Host != "www.example.test" || len(out.Metrics) != 2 || out.Metrics[0].Rank != "1,234" ||
			out.Metrics[0].ID != "indexed-pages-bing" || out.Metrics[0].LogoLink != "https://www.bing.com/" ||
			out.Metrics[1].Label != "Domain Age" {
			t.Fatalf("%s: result = %s, err = %v", url, result, err)
		}
		sent := calls[len(calls)-1]
		if sent.form.Get("url") != "www.example.test" || sent.auth != "Bearer "+tokenValue || sent.rawQuery != "" ||
			sent.form.Get("format") != "JSON" || sent.form.Has("token_auth") || countMethod(calls, "SEO.getRank") != 1 {
			t.Errorf("request = %+v", sent)
		}
	}
}

func TestSEORankRefusesBadTargetsBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newDiagEnvironment(t, &calls, diagAnswers(t, answers{}))
	for _, arguments := range []string{`{}`, `{"url":""}`, `{"url":"ftp://example.test"}`, `{"url":"https://user:pw@example.test/"}`,
		`{"url":"javascript:alert(1)"}`, `{"url":"exa mple.test"}`, `{"url":"example.test/path"}`, `{"url":"-bad.test"}`,
		`{"url":"https://"}`, `{"url":"` + strings.Repeat("a", 300) + `.test"}`, `{"url":"example.test","extra":1}`,
		`{"url":"https://example.test/` + strings.Repeat("a", 2100) + `"}`} {
		if _, err := env.invoke("matomo.seo.rank", "open", arguments); !isInvalidRequest(err) {
			t.Errorf("%.60s: err = %v", arguments, err)
		}
	}
	if _, err := env.invoke("matomo.seo.rank", "instance", `{"url":"example.test"}`); err == nil {
		t.Error("seo.rank ran on an instance-only binding")
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestIPLocationsGet(t *testing.T) {
	var calls []call
	env := newDiagEnvironment(t, &calls, diagAnswers(t, answers{"UserCountry.getLocationFromIP": fixed(
		`{"continent_code":"EU","continent_name":"Europe","country_code":"FR","country_name":"France","region_code":"IDF",` +
			`"region_name":"Ile-de-France","city_name":"Paris","area_code":"75","lat":48.85,"long":2.35,"postal_code":"75001",` +
			`"isp":"Example ISP","org":"Example Org","ip":"2001:db8::1"}`)}))
	result, err := env.invoke("matomo.ip-locations.get", "open", `{"ip":"2001:0DB8:0:0:0:0:0:1","provider":"geoip2php"}`)
	var out IPLocation
	_ = json.Unmarshal([]byte(result), &out)
	want := IPLocation{IP: "2001:db8::1", ContinentCode: "EU", ContinentName: "Europe", CountryCode: "FR", CountryName: "France",
		RegionCode: "IDF", RegionName: "Ile-de-France", CityName: "Paris", AreaCode: "75", Lat: "48.85", Long: "2.35",
		PostalCode: "75001", ISP: "Example ISP", Org: "Example Org"}
	sent := calls[len(calls)-1]
	if err != nil || out != want || sent.form.Get("ip") != "2001:db8::1" || sent.form.Get("provider") != "geoip2php" ||
		sent.auth != "Bearer "+tokenValue || sent.rawQuery != "" {
		t.Fatalf("result = %s, err = %v, request = %+v", result, err, sent)
	}
	if _, err := env.invoke("matomo.ip-locations.get", "open", `{"ip":"192.0.2.1"}`); err != nil ||
		calls[len(calls)-1].form.Has("provider") {
		t.Errorf("without provider: err = %v", err)
	}
	before := len(calls)
	for _, arguments := range []string{`{}`, `{"ip":""}`, `{"ip":"999.1.1.1"}`, `{"ip":"fe80::1%eth0"}`, `{"ip":"192.0.2.0/24"}`,
		`{"ip":"example.test"}`, `{"ip":"192.0.2.1","provider":"a b"}`, `{"ip":"192.0.2.1","provider":"1x"}`,
		`{"ip":"192.0.2.1","provider":"` + strings.Repeat("a", 65) + `"}`} {
		if _, err := env.invoke("matomo.ip-locations.get", "open", arguments); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", arguments, err)
		}
	}
	if _, err := env.invoke("matomo.ip-locations.get", "instance", `{"ip":"192.0.2.1"}`); err == nil {
		t.Error("ip-locations ran on an instance-only binding")
	}
	if len(calls) != before || *env.reads != 2 {
		t.Errorf("%d requests and %d secret reads, want none beyond the two calls", len(calls)-before, *env.reads)
	}
}

func TestJSTrackerInstallStatus(t *testing.T) {
	var calls []call
	env := newDiagEnvironment(t, &calls, diagAnswers(t, answers{"JsTrackerInstallCheck.wasJsTrackerInstallTestSuccessful": func(c call) *http.Response {
		return ok(`{"isSuccess":` + map[bool]string{true: "true", false: "false"}[c.form.Get("nonce") != ""] +
			`,"mainUrl":"https://one.example.test"}`)
	}}))
	nonce := strings.Repeat("ab12", 8)
	result, err := env.invoke("matomo.js-tracker-install.get-status", "two", `{"idSite":2,"nonce":"`+nonce+`"}`)
	var out JSTrackerInstallStatus
	_ = json.Unmarshal([]byte(result), &out)
	sent := calls[len(calls)-1]
	if err != nil || out != (JSTrackerInstallStatus{IDSite: 2, IsSuccess: true, MainURL: "https://one.example.test"}) ||
		sent.form.Get("idSite") != "2" || sent.form.Get("nonce") != nonce || sent.auth != "Bearer "+tokenValue || sent.rawQuery != "" {
		t.Fatalf("result = %s, err = %v, request = %+v", result, err, sent)
	}
	// The only bound website is the default; without a nonce the latest result is read.
	result, err = env.invoke("matomo.js-tracker-install.get-status", "one", `{}`)
	if err != nil || !strings.Contains(result, `"idSite":2`) || !strings.Contains(result, `"isSuccess":false`) ||
		calls[len(calls)-1].form.Has("nonce") {
		t.Errorf("default website: %s, err = %v", result, err)
	}
	before, reads := len(calls), *env.reads
	for _, c := range [][2]string{{"one", `{"idSite":1}`}, {"two", `{"idSite":3}`},
		{"open", `{"idSite":1,"nonce":"` + strings.ToUpper(nonce) + `"}`}, {"open", `{"idSite":1,"nonce":"abc"}`},
		{"open", `{"idSite":1,"nonce":"` + nonce + `0"}`}, {"open", `{"idSite":0}`}, {"open", `{}`}} {
		if _, err := env.invoke("matomo.js-tracker-install.get-status", c[0], c[1]); !isInvalidRequest(err) {
			t.Errorf("%s %s: err = %v", c[0], c[1], err)
		}
	}
	if len(calls) != before || *env.reads != reads {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls)-before, *env.reads-reads)
	}
	// An answer without the success flag is invalid.
	env = newDiagEnvironment(t, &calls, diagAnswers(t, answers{"JsTrackerInstallCheck.wasJsTrackerInstallTestSuccessful": fixed(`{"mainUrl":"x"}`)}))
	if _, err := env.invoke("matomo.js-tracker-install.get-status", "open", `{"idSite":1}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("missing flag: err = %v", err)
	}
}

func TestBruteForceIPsUnblockNeedsToolsListInstanceAndConfirm(t *testing.T) {
	var calls []call
	env := newDiagEnvironment(t, &calls, diagAnswers(t, answers{"Login.unblockBruteForceIPs": fixed(`{"result":"success","message":"ok"}`)}))
	for _, connection := range []string{"dg-admin", "dg-instonly"} {
		result, err := env.change("matomo.brute-force-ips.unblock", connection, `{}`)
		if err != nil || result != `{"unblocked":true}` {
			t.Errorf("%s: result = %s, err = %v", connection, result, err)
		}
		sent := calls[len(calls)-1]
		if sent.form.Has("passwordConfirmation") || sent.form.Has("idSite") || sent.auth != "Bearer "+tokenValue || sent.rawQuery != "" ||
			sent.form.Get("format") != "JSON" || sent.form.Has("token_auth") {
			t.Errorf("request = %+v", sent)
		}
	}
	before, reads := len(calls), *env.reads
	for _, connection := range []string{"dg-noinst", "dg-effects", "open", "instance", "mixed"} {
		if _, err := env.change("matomo.brute-force-ips.unblock", connection, `{}`); err == nil {
			t.Errorf("unblock ran on %s", connection)
		}
	}
	if _, err := env.change("matomo.brute-force-ips.unblock", "dg-admin", `{"ip":"192.0.2.1"}`); err == nil {
		t.Error("unblock accepted an argument")
	}
	if _, err := env.invoke("matomo.brute-force-ips.unblock", "dg-admin", `{}`); err == nil {
		t.Error("unblock ran without confirm")
	}
	if len(calls) != before || *env.reads != reads {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls)-before, *env.reads-reads)
	}
	for targets, want := range map[string]bool{"site/1": false, "site": false, "instance": true, "site/1,instance": true} {
		if got, _ := reaches("matomo.brute-force-ips.unblock", strings.Split(targets, ",")); got != want {
			t.Errorf("reaches with %s = %t, want %t", targets, got, want)
		}
	}
}

func TestBruteForceIPsUnblockIsSentOnce(t *testing.T) {
	for name, answer := range map[string]func(call) (*http.Response, error){
		"timeout":         func(call) (*http.Response, error) { return nil, errTimeout },
		"502":             func(call) (*http.Response, error) { return respond(http.StatusBadGateway, ""), nil },
		"unreadable":      func(call) (*http.Response, error) { return ok("<html>" + bodyCanary + "</html>"), nil },
		"no confirmation": func(call) (*http.Response, error) { return ok(`[]`), nil },
	} {
		var calls []call
		env := newDiagEnvironment(t, &calls, func(c call) (*http.Response, error) {
			if c.api() != "Login.unblockBruteForceIPs" {
				return versionOnly("5.14.2").handler(t)(c)
			}
			return answer(c)
		})
		_, err := env.change("matomo.brute-force-ips.unblock", "dg-admin", `{}`)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || countMethod(calls, "Login.unblockBruteForceIPs") != 1 ||
			!strings.Contains(err.Error(), "never repeated") {
			t.Errorf("%s: err = %v, sent %d times", name, err, countMethod(calls, "Login.unblockBruteForceIPs"))
		}
	}
}

func TestDiagnosticToolsMapProviderFailuresWithoutRetry(t *testing.T) {
	tools := []struct{ id, connection, arguments, method string }{
		{"matomo.seo.rank", "open", `{"url":"example.test"}`, "SEO.getRank"},
		{"matomo.ip-locations.get", "open", `{"ip":"192.0.2.1"}`, "UserCountry.getLocationFromIP"},
		{"matomo.js-tracker-install.get-status", "open", `{"idSite":1}`, "JsTrackerInstallCheck.wasJsTrackerInstallTestSuccessful"},
		{"matomo.brute-force-ips.unblock", "dg-admin", `{}`, "Login.unblockBruteForceIPs"},
	}
	for name, tc := range map[string]struct {
		answer      func() *http.Response
		class       provider.Class
		unsupported bool
	}{
		"permission": {func() *http.Response { return failure(permissionMsg + bodyCanary) }, provider.ClassPermission, false},
		"not found":  {func() *http.Response { return failure("An unexpected website was found " + bodyCanary) }, provider.ClassNotFound, false},
		"method missing": {func() *http.Response {
			return failure("The method 'X.y' does not exist or is not available in the module 'X'.")
		}, "", true},
		"plugin inactive": {func() *http.Response { return failure("The plugin SEO is not activated.") }, "", true},
		"429":             {func() *http.Response { return respond(http.StatusTooManyRequests, bodyCanary) }, provider.ClassRateLimited, false},
	} {
		for _, tool := range tools {
			var calls []call
			env := newDiagEnvironment(t, &calls, func(c call) (*http.Response, error) {
				if c.api() != tool.method {
					return versionOnly("5.14.2").handler(t)(c)
				}
				return tc.answer(), nil
			})
			_, err := env.change(tool.id, tool.connection, tool.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || tc.unsupported != isUnsupported(err) ||
				tc.class != "" && classOf(err) != tc.class || countMethod(calls, tool.method) != 1 {
				t.Errorf("%s %s: err = %v, sent %d times", name, tool.id, err, countMethod(calls, tool.method))
			}
		}
	}
}
