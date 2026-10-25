package matomo

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const siteManageConnections = `
  sm-writer: {service: matomo, credential: token, effects: [read, create, update, delete, execute], targets: [site, instance]}
  sm-single: {service: matomo, credential: token, effects: [read, create, update], targets: [site/1]}
  sm-noinst: {service: matomo, credential: token, tools: [matomo.sites.delete, matomo.sites.get], targets: [site]}
  sm-deleter: {service: matomo, credential: token, tools: [matomo.sites.delete, matomo.sites.get], targets: [site/1, instance]}
  um-deleter: {service: matomo, credential: token, tools: [matomo.users.delete], targets: [instance]}
  sm-instance: {service: matomo, credential: token, effects: [read, create], targets: [instance]}
`

func newSiteManageEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(siteManageConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func siteManageAnswers(extra answers) answers {
	return with(versionOnly("5.14.2"), extra)
}

func TestSiteManageToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	for id, want := range map[string]struct {
		effect capability.Effect
		idem   capability.Idempotency
		listed bool
	}{
		"matomo.sites.add":    {capability.EffectCreate, capability.IdempotencyNonIdempotent, false},
		"matomo.sites.update": {capability.EffectUpdate, capability.IdempotencyIdempotent, false},
		"matomo.sites.delete": {capability.EffectDelete, capability.IdempotencyIdempotent, true},
	} {
		d, _, found := reg.Lookup(id)
		if !found || d.Risk.Effect != want.effect || d.Risk.Idempotency != want.idem ||
			d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" ||
			d.RequiresToolAllowList != want.listed {
			t.Errorf("%s: found %t, %+v", id, found, d)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		joined := " " + strings.Join(profile.Tools, " ") + " "
		if strings.Contains(joined, " matomo.sites.add ") || strings.Contains(joined, " matomo.sites.delete ") {
			t.Errorf("profile %s selects add or delete", profile.ID)
		}
		if strings.Contains(joined, " matomo.sites.update ") != (profile.ID == "sites") || profile.ID == "sites" && profile.Recommended {
			t.Errorf("profile %s: %v", profile.ID, profile.Tools)
		}
	}
}

func TestSitesAddNeedsTheInstanceAndReturnsTheNewID(t *testing.T) {
	var calls []call
	env := newSiteManageEnvironment(t, &calls, siteManageAnswers(answers{
		"SitesManager.addSite": func(call) *http.Response { return ok(`{"value":9}`) },
	}).handler(t))
	args := `{"siteName":"Shop","urls":["https://shop.example.test","https://alias.example.test"],"ecommerce":true,` +
		`"siteSearch":false,"excludedIps":"192.0.2.1","timezone":"Europe/Berlin","currency":"EUR","group":"Shops",` +
		`"startDate":"2026-01-02","keepURLFragments":2,"type":"website","excludeUnknownUrls":true,"description":"d",` +
		`"settingValues":{"Plugin":[{"name":"a","value":true},{"name":"b","value":["x","y"]},{"name":"c","value":3}]}}`
	if _, err := env.invoke("matomo.sites.add", "sm-writer", args); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("without confirm: err = %v", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("unconfirmed call: %d requests, %d reads", len(calls), *env.reads)
	}
	result, err := env.change("matomo.sites.add", "sm-writer", args)
	if err != nil || result != `{"created":true,"idSite":9}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	sent := calls[len(calls)-1]
	if countMethod(calls, "SitesManager.addSite") != 1 || sent.auth != "Bearer "+tokenValue || sent.form.Has("token_auth") {
		t.Errorf("request = %+v", sent)
	}
	for name, want := range map[string]string{"siteName": "Shop", "ecommerce": "1", "siteSearch": "0", "excludedIps": "192.0.2.1",
		"timezone": "Europe/Berlin", "currency": "EUR", "group": "Shops", "startDate": "2026-01-02", "keepURLFragments": "2",
		"type": "website", "excludeUnknownUrls": "1", "description": "d", "settingValues[Plugin][0][name]": "a",
		"settingValues[Plugin][0][value]": "1", "settingValues[Plugin][2][value]": "3"} {
		if sent.form.Get(name) != want {
			t.Errorf("%s = %q, want %q", name, sent.form.Get(name), want)
		}
	}
	if got := sent.form["urls[]"]; len(got) != 2 || got[1] != "https://alias.example.test" {
		t.Errorf("urls = %v", got)
	}
	if got := sent.form["settingValues[Plugin][1][value][]"]; len(got) != 2 {
		t.Errorf("list setting = %v", got)
	}
	before, readsBefore := len(calls), *env.reads
	for _, connection := range []string{"sm-single", "sm-noinst"} {
		if _, err := env.change("matomo.sites.add", connection, args); err == nil {
			t.Errorf("%s offered sites.add", connection)
		}
	}
	if _, err := env.change("matomo.sites.add", "sm-instance", `{"siteName":"x","urls":[]}`); !isInvalidRequest(err) {
		t.Errorf("empty urls: err = %v", err)
	}
	if _, err := env.change("matomo.sites.add", "sm-instance", `{"siteName":"x","startDate":"tomorrow"}`); !isInvalidRequest(err) {
		t.Errorf("bad date: err = %v", err)
	}
	if len(calls) != before || *env.reads != readsBefore {
		t.Errorf("%d requests and %d reads before a refusal", len(calls)-before, *env.reads-readsBefore)
	}
	if ok, _ := reaches("matomo.sites.add", []string{"site/1"}); ok {
		t.Error("sites.add reaches a connection without the instance")
	}
	if ok, _ := reaches("matomo.sites.add", []string{"instance"}); !ok {
		t.Error("sites.add does not reach an instance connection")
	}
}

func TestSitesAddErrorsAndUnclearResults(t *testing.T) {
	var calls []call
	env := newSiteManageEnvironment(t, &calls, siteManageAnswers(answers{
		"SitesManager.addSite": func(call) *http.Response {
			return failure("You can't access this resource as it requires 'superuser' access.")
		},
	}).handler(t))
	if _, err := env.change("matomo.sites.add", "sm-writer", `{"siteName":"x"}`); classOf(err) != provider.ClassPermission {
		t.Errorf("permission: err = %v", err)
	}
	for name, handler := range map[string]func(call) *http.Response{
		"no ID":  func(call) *http.Response { return ok(`{"value":"none"}`) },
		"server": func(call) *http.Response { return respond(500, bodyCanary) },
		"html":   func(call) *http.Response { return ok(`<html>` + bodyCanary + `</html>`) },
	} {
		var c []call
		e := newSiteManageEnvironment(t, &c, siteManageAnswers(answers{"SitesManager.addSite": handler}).handler(t))
		_, err := e.change("matomo.sites.add", "sm-writer", `{"siteName":"x"}`)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || !strings.Contains(err.Error(), "never repeated") ||
			countMethod(c, "SitesManager.addSite") != 1 {
			t.Errorf("%s: err = %v, sent %d times", name, err, countMethod(c, "SitesManager.addSite"))
		}
	}
	var c []call
	e := newSiteManageEnvironment(t, &c, func(x call) (*http.Response, error) {
		if x.api() == "SitesManager.addSite" {
			return nil, context.DeadlineExceeded
		}
		return siteManageAnswers(answers{}).handler(t)(x)
	})
	if _, err := e.change("matomo.sites.add", "sm-writer", `{"siteName":"x"}`); classOf(err) != provider.ClassTimeout ||
		!strings.Contains(err.Error(), "never repeated") || countMethod(c, "SitesManager.addSite") != 1 {
		t.Errorf("timeout: err = %v", err)
	}
}

func TestSitesUpdateKeepsUnsentListsAndChecksTheWebsiteFirst(t *testing.T) {
	var calls []call
	row := strings.Replace(siteRow("1", "shop"), `"excluded_ips":""`, `"excluded_ips":"192.0.2.1"`, 1)
	env := newSiteManageEnvironment(t, &calls, siteManageAnswers(answers{
		"SitesManager.getSiteFromId": func(call) *http.Response { return ok("[" + row + "]") },
		"SitesManager.updateSite":    func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) },
	}).handler(t))
	result, err := env.change("matomo.sites.update", "sm-single", `{"siteName":"Renamed","excludedUserAgents":"bot"}`)
	if err != nil || result != `{"idSite":1,"updated":true}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	sent := calls[len(calls)-1]
	if sent.api() != "SitesManager.updateSite" || countMethod(calls, "SitesManager.updateSite") != 1 {
		t.Fatalf("requests = %v", calls)
	}
	for name, want := range map[string]string{"idSite": "1", "siteName": "Renamed", "excludedUserAgents": "bot",
		"excludedIps": "192.0.2.1", "searchKeywordParameters": "q", "searchCategoryParameters": ""} {
		if !sent.form.Has(name) || sent.form.Get(name) != want {
			t.Errorf("%s = %q (set %t), want %q", name, sent.form.Get(name), sent.form.Has(name), want)
		}
	}
	for _, name := range []string{"timezone", "currency", "group", "urls[]", "ecommerce", "type"} {
		if sent.form.Has(name) {
			t.Errorf("%s was sent without being given", name)
		}
	}
	// All six lists given: no read first.
	calls = nil
	all := `{"idSite":1,"searchKeywordParameters":"","searchCategoryParameters":"","excludedIps":"","excludedQueryParameters":"",` +
		`"excludedUserAgents":"","excludedReferrers":"","timezone":"UTC"}`
	if _, err := env.change("matomo.sites.update", "sm-single", all); err != nil || countMethod(calls, "SitesManager.getSiteFromId") != 0 {
		t.Errorf("all lists given: err = %v", err)
	}
	before, readsBefore := len(calls), *env.reads
	for name, args := range map[string]string{"foreign": `{"idSite":2,"siteName":"x"}`, "empty": `{"idSite":1}`,
		"keep": `{"idSite":1,"keepURLFragments":3}`, "blank name": `{"idSite":1,"siteName":" "}`} {
		if _, err := env.change("matomo.sites.update", "sm-single", args); !isInvalidRequest(err) || strings.Contains(err.Error(), "site/") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if len(calls) != before || *env.reads != readsBefore {
		t.Errorf("%d requests and %d reads before a refusal", len(calls)-before, *env.reads-readsBefore)
	}
}

func TestSitesUpdateErrorsAndUnclearResults(t *testing.T) {
	var calls []call
	env := newSiteManageEnvironment(t, &calls, siteManageAnswers(answers{
		"SitesManager.getSiteFromId": func(call) *http.Response { return ok(siteRow("1", "shop")) },
		"SitesManager.updateSite":    func(call) *http.Response { return ok(`{"value":1}`) },
	}).handler(t))
	if _, err := env.change("matomo.sites.update", "sm-single", `{"siteName":"x"}`); err == nil ||
		!strings.Contains(err.Error(), "never repeated") {
		t.Errorf("unconfirmed update: err = %v", err)
	}
	var c []call
	e := newSiteManageEnvironment(t, &c, siteManageAnswers(answers{
		"SitesManager.getSiteFromId": func(call) *http.Response { return failure(permissionMsg) },
	}).handler(t))
	if _, err := e.change("matomo.sites.update", "sm-single", `{"siteName":"x"}`); classOf(err) != provider.ClassPermission ||
		countMethod(c, "SitesManager.updateSite") != 0 {
		t.Errorf("permission: err = %v", err)
	}
}

func TestSitesDeleteNeedsInstanceWebsiteAndToolsList(t *testing.T) {
	var calls []call
	env := newSiteManageEnvironment(t, &calls, siteManageAnswers(answers{
		"SitesManager.getSiteFromId": func(call) *http.Response { return ok(siteRow("1", "shop")) },
		"SitesManager.deleteSite":    func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) },
	}).handler(t))
	if _, err := env.change("matomo.sites.delete", "sm-writer", `{"idSite":1}`); err == nil {
		t.Fatal("an effects connection offered sites.delete")
	}
	if len(calls) != 0 {
		t.Fatalf("a tool that is not offered caused %d requests", len(calls))
	}
	if _, err := env.invoke("matomo.sites.delete", "sm-deleter", `{"idSite":1}`); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("without confirm: err = %v", err)
	}
	result, err := env.change("matomo.sites.delete", "sm-deleter", `{"idSite":1}`)
	if err != nil || result != `{"deleted":true,"idSite":1,"name":"shop"}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	var methods []string
	for _, c := range calls {
		methods = append(methods, c.api())
	}
	if got := strings.Join(methods, " "); got != "API.getMatomoVersion SitesManager.getSiteFromId SitesManager.deleteSite" {
		t.Errorf("requests = %s", got)
	}
	if calls[len(calls)-1].form.Has("passwordConfirmation") {
		t.Error("a password confirmation was sent")
	}
	before, readsBefore := len(calls), *env.reads
	if _, err := env.change("matomo.sites.delete", "sm-deleter", `{"idSite":2}`); !isInvalidRequest(err) || strings.Contains(err.Error(), "site/") {
		t.Errorf("foreign website: err = %v", err)
	}
	if _, err := env.change("matomo.sites.delete", "sm-deleter", `{}`); !isInvalidRequest(err) {
		t.Errorf("no idSite: err = %v", err)
	}
	if _, err := env.change("matomo.sites.delete", "sm-noinst", `{"idSite":1}`); err == nil {
		t.Error("sites.delete ran without the instance")
	}
	if len(calls) != before || *env.reads != readsBefore {
		t.Errorf("%d requests and %d reads before a refusal", len(calls)-before, *env.reads-readsBefore)
	}
	for targets, want := range map[string]bool{"site/1": false, "instance": false, "site,instance": true} {
		if ok, _ := reaches("matomo.sites.delete", strings.Split(targets, ",")); ok != want {
			t.Errorf("reaches(%s) = %t", targets, ok)
		}
	}
}

func TestSitesDeleteErrorsAndUnclearResults(t *testing.T) {
	read := func(call) *http.Response { return ok(siteRow("1", "shop")) }
	for name, tc := range map[string]struct {
		answer func(call) *http.Response
		class  provider.Class
	}{
		"permission": {func(call) *http.Response {
			return failure("You can't access this resource as it requires 'superuser' access.")
		}, provider.ClassPermission},
		"last site": {func(call) *http.Response { return failure("At least one website must remain") }, ""},
		"unclear":   {func(call) *http.Response { return ok(`{"value":1}`) }, ""},
	} {
		var calls []call
		env := newSiteManageEnvironment(t, &calls, siteManageAnswers(answers{
			"SitesManager.getSiteFromId": read, "SitesManager.deleteSite": tc.answer,
		}).handler(t))
		_, err := env.change("matomo.sites.delete", "sm-deleter", `{"idSite":1}`)
		if err == nil || countMethod(calls, "SitesManager.deleteSite") != 1 || tc.class != "" && classOf(err) != tc.class {
			t.Errorf("%s: err = %v", name, err)
		}
		if name == "unclear" && !strings.Contains(err.Error(), "never repeated") {
			t.Errorf("unclear: err = %v", err)
		}
	}
}
