package matomo

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const siteManageConnections = `
  sw: {service: matomo, credential: token, effects: [read, create, update], targets: [site/1, site/2]}
  sw-one: {service: matomo, credential: token, effects: [read, create, update], targets: [site/2]}
  sw-inst: {service: matomo, credential: token, effects: [read, update], targets: [site/1, instance]}
  sw-only-inst: {service: matomo, credential: token, effects: [read, update], targets: [site/1]}
`

func newSiteManageEnvironment(t *testing.T, calls *[]call, extra answers) *environment {
	t.Helper()
	serve(t, calls, with(versionOnly("5.14.2"), extra).handler(t))
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

func confirmed(env *environment, op, connection, args string) (string, error) {
	return env.change(op, connection, args)
}

func TestSiteURLToolsSendOneRequestAndReportTheCount(t *testing.T) {
	var calls []call
	env := newSiteManageEnvironment(t, &calls, answers{
		"SitesManager.addSiteAliasUrls": func(call) *http.Response { return ok(`{"value":2}`) },
		"SitesManager.setSiteAliasUrls": func(call) *http.Response { return ok(`{"value":0}`) },
	})
	result, err := confirmed(env, "matomo.site-urls.add", "sw", `{"idSite":1,"urls":["https://a.example.test","http://b.example.test/x"]}`)
	if err != nil || result != `{"idSite":1,"inserted":2,"updated":true}` {
		t.Fatalf("add: %s, %v", result, err)
	}
	sent := calls[len(calls)-1]
	if got := sent.form["urls[]"]; !slices.Equal(got, []string{"https://a.example.test", "http://b.example.test/x"}) ||
		sent.form.Get("idSite") != "1" || !strings.Contains(sent.auth, "Bearer") || strings.Contains(sent.rawQuery, "urls") {
		t.Errorf("request = %v auth=%q query=%q", sent.form, sent.auth, sent.rawQuery)
	}
	result, err = confirmed(env, "matomo.site-urls.set", "sw", `{"idSite":1,"urls":[]}`)
	if err != nil || result != `{"idSite":1,"inserted":0,"updated":true}` {
		t.Fatalf("set: %s, %v", result, err)
	}
	if _, has := calls[len(calls)-1].form["urls[]"]; has || countMethod(calls, "SitesManager.setSiteAliasUrls") != 1 {
		t.Errorf("empty set sent urls: %v", calls[len(calls)-1].form)
	}
}

func TestSiteURLsAreCheckedBeforeAnyRequest(t *testing.T) {
	userInfo := (&url.URL{Scheme: "https", User: url.UserPassword("u", "p"), Host: "a.example.test"}).String()
	many := make([]string, 21)
	for i := range many {
		many[i] = `"https://a.example.test"`
	}
	for name, args := range map[string]string{
		"ftp":      `{"idSite":1,"urls":["ftp://a.example.test"]}`,
		"no host":  `{"idSite":1,"urls":["https://"]}`,
		"userinfo": `{"idSite":1,"urls":["` + userInfo + `"]}`,
		"space":    `{"idSite":1,"urls":["https://a.example.test/a b"]}`,
		"too long": `{"idSite":1,"urls":["https://a.example.test/` + strings.Repeat("a", 2000) + `"]}`,
		"too many": `{"idSite":1,"urls":[` + strings.Join(many, ",") + `]}`,
		"empty":    `{"idSite":1,"urls":[]}`,
		"foreign":  `{"idSite":3,"urls":["https://a.example.test"]}`,
	} {
		var calls []call
		env := newSiteManageEnvironment(t, &calls, nil)
		if _, err := confirmed(env, "matomo.site-urls.add", "sw", args); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", name, err)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: %d requests, %d secret reads", name, len(calls), *env.reads)
		}
	}
}

func TestUpdateCreatedTimeChecksEveryIDAndTheDate(t *testing.T) {
	var calls []call
	env := newSiteManageEnvironment(t, &calls, answers{
		"SitesManager.updateSiteCreatedTime": func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) },
	})
	result, err := confirmed(env, "matomo.sites.update-created-time", "sw", `{"idSites":[1,2],"minDate":"2024-01-15"}`)
	if err != nil || result != `{"idSites":[1,2],"minDate":"2024-01-15","updated":true}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if sent := calls[len(calls)-1]; sent.form.Get("idSites") != "1,2" || sent.form.Get("minDate") != "2024-01-15" {
		t.Errorf("request = %v", sent.form)
	}
	before := len(calls)
	reads := *env.reads
	for name, args := range map[string]string{
		"foreign":   `{"idSites":[2,3],"minDate":"2024-01-15"}`,
		"repeat":    `{"idSites":[1,1],"minDate":"2024-01-15"}`,
		"none":      `{"idSites":[],"minDate":"2024-01-15"}`,
		"all":       `{"idSites":"all","minDate":"2024-01-15"}`,
		"today":     `{"idSites":[1],"minDate":"today"}`,
		"bad month": `{"idSites":[1],"minDate":"2024-13-01"}`,
		"time":      `{"idSites":[1],"minDate":"2024-01-15 10:00:00"}`,
		"short":     `{"idSites":[1],"minDate":"2024-1-5"}`,
	} {
		if _, err := confirmed(env, "matomo.sites.update-created-time", "sw", args); !isInvalidRequest(err) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = "1"
	}
	if _, err := confirmed(env, "matomo.sites.update-created-time", "sw", `{"idSites":[`+strings.Join(tooMany, ",")+`],"minDate":"2024-01-15"}`); err == nil {
		t.Error("101 IDs accepted")
	}
	if len(calls) != before || *env.reads != reads {
		t.Errorf("refusals sent %d requests and read %d secrets", len(calls)-before, *env.reads-reads)
	}
}

func TestSiteGroupsRenameNeedsTheInstanceTarget(t *testing.T) {
	var calls []call
	env := newSiteManageEnvironment(t, &calls, answers{
		"SitesManager.renameGroup": func(call) *http.Response { return ok(`{"value":true}`) },
	})
	const args = `{"oldGroupName":"Shops","newGroupName":"Stores"}`
	if _, err := confirmed(env, "matomo.site-groups.rename", "sw-only-inst", args); err == nil {
		t.Error("renamed without the instance target")
	}
	if _, err := confirmed(env, "matomo.site-groups.rename", "sw-inst", `{"oldGroupName":"","newGroupName":"x"}`); !isInvalidRequest(err) {
		t.Errorf("empty old name: err = %v", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("%d requests, %d secret reads before the refusals", len(calls), *env.reads)
	}
	result, err := confirmed(env, "matomo.site-groups.rename", "sw-inst", args)
	if err != nil || result != `{"newGroupName":"Stores","oldGroupName":"Shops","renamed":true}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if sent := calls[len(calls)-1]; sent.form.Get("oldGroupName") != "Shops" || sent.form.Get("newGroupName") != "Stores" {
		t.Errorf("request = %v", sent.form)
	}
	if ok, _ := reaches(siteGroupsRename.ID, []string{"site"}); ok {
		t.Error("rename reaches without instance")
	}
}

func TestSiteManageErrorsAndUnclearChanges(t *testing.T) {
	tools := []struct{ op, connection, args, method string }{
		{"matomo.site-urls.add", "sw", `{"idSite":1,"urls":["https://a.example.test"]}`, "SitesManager.addSiteAliasUrls"},
		{"matomo.site-urls.set", "sw", `{"idSite":1,"urls":["https://a.example.test"]}`, "SitesManager.setSiteAliasUrls"},
		{"matomo.sites.update-created-time", "sw", `{"idSites":[1],"minDate":"2024-01-15"}`, "SitesManager.updateSiteCreatedTime"},
		{"matomo.site-groups.rename", "sw-inst", `{"oldGroupName":"a","newGroupName":"b"}`, "SitesManager.renameGroup"},
	}
	for _, tool := range tools {
		var calls []call
		env := newSiteManageEnvironment(t, &calls, answers{
			tool.method: func(call) *http.Response {
				return failure("You can't access this resource as it requires 'admin' access." + bodyCanary)
			},
		})
		_, err := confirmed(env, tool.op, tool.connection, tool.args)
		if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), bodyCanary) {
			t.Errorf("%s: err = %v", tool.op, err)
		}
		for name, handler := range map[string]func(call) *http.Response{
			"html":   func(call) *http.Response { return ok(`<html>` + bodyCanary + `</html>`) },
			"server": func(call) *http.Response { return respond(500, bodyCanary) },
		} {
			calls = nil
			env = newSiteManageEnvironment(t, &calls, answers{tool.method: handler})
			_, err = confirmed(env, tool.op, tool.connection, tool.args)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || !strings.Contains(err.Error(), "never repeated") ||
				countMethod(calls, tool.method) != 1 {
				t.Errorf("%s %s: err = %v, sent %d", tool.op, name, err, countMethod(calls, tool.method))
			}
		}
	}
}

func TestSiteManageToolsAreRegisteredAndProfiled(t *testing.T) {
	reg := registry(t)
	for _, id := range []string{siteURLsAdd.ID, siteURLsSet.ID, sitesUpdateCreatedTime.ID, siteGroupsRename.ID} {
		d, _, ok := reg.Lookup(id)
		if !ok || d.Risk.Confirmation != "required" || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" {
			t.Fatalf("%s: ok=%t risk=%+v", id, ok, d.Risk)
		}
	}
	if !strings.Contains(siteURLsSet.Description, "Replace the whole list") {
		t.Error("set description does not say it replaces the list")
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		joined := strings.Join(profile.Tools, " ")
		if strings.Contains(joined, siteGroupsRename.ID) {
			t.Errorf("profile %s holds the Super User rename", profile.ID)
		}
		if profile.ID == "sites" && (profile.Recommended || !strings.Contains(joined, siteURLsSet.ID)) {
			t.Errorf("sites profile = %+v", profile)
		}
	}
}
