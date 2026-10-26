package matomo

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const operationsConnections = `
  op-full: {service: matomo, credential: token, effects: [read, update, execute], targets: [site/2, instance]}
  op-hosts: {service: matomo, credential: token, tools: [matomo.trusted-hosts.set], targets: [instance]}
  op-site: {service: matomo, credential: token, effects: [read, update, execute], targets: [site/2]}
  op-instance: {service: matomo, credential: token, effects: [read, update, execute], targets: [instance]}
`

const (
	archiveSettingsOp = "matomo.archive-settings.set"
	trustedHostsOp    = "matomo.trusted-hosts.set"
	brandingOp        = "matomo.branding.set"
	archiveOp         = "matomo.reports.archive"
)

var operationsCases = []struct{ op, connection, method, args, want string }{
	{archiveSettingsOp, "op-full", "CoreAdminHome.setArchiveSettings",
		`{"enableBrowserTriggerArchiving":false,"todayArchiveTimeToLive":600}`,
		`{"enableBrowserTriggerArchiving":false,"todayArchiveTimeToLive":600,"updated":true}`},
	{trustedHostsOp, "op-hosts", "CoreAdminHome.setTrustedHosts",
		`{"trustedHosts":["Analytics.example.test","m.example.test:8443","analytics.example.test"]}`,
		`{"trustedHosts":["analytics.example.test","m.example.test:8443"],"updated":true}`},
	{brandingOp, "op-full", "CoreAdminHome.setBrandingSettings",
		`{"useCustomLogo":true,"hasCustomLogo":true}`,
		`{"customLogoPath":"misc/user/logo.png","useCustomLogo":true}`},
	{archiveOp, "op-full", "CoreAdminHome.archiveReports",
		`{"idSite":2,"period":"day","date":"2026-10-01","segment":"browserName==Firefox","plugin":"Actions","report":"Actions_getPageUrls"}`,
		`{"archived":true,"date":"2026-10-01","idSite":2,"idarchives":[7,8],"nb_visits":12,"period":"day"}`},
}

func operationsAnswers(extra answers) answers {
	return with(versionOnly("5.14.2"), with(answers{
		"CoreAdminHome.setArchiveSettings": func(call) *http.Response { return ok(`{"value":true}`) },
		"CoreAdminHome.setTrustedHosts":    func(call) *http.Response { return ok(`{"value":true}`) },
		"CoreAdminHome.setBrandingSettings": func(call) *http.Response {
			return ok(`{"useCustomLogo":true,"customLogoPath":"misc/user/logo.png"}`)
		},
		"CoreAdminHome.archiveReports": func(call) *http.Response {
			return ok(`{"idarchives":[7,8],"nb_visits":12}`)
		},
	}, extra))
}

func newOperationsEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(operationsConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func TestOperationsToolsSendOneBearerRequest(t *testing.T) {
	for _, c := range operationsCases {
		var calls []call
		env := newOperationsEnvironment(t, &calls, operationsAnswers(nil).handler(t))
		result, err := env.change(c.op, c.connection, c.args)
		if err != nil || result != c.want {
			t.Fatalf("%s: %s, %v", c.op, result, err)
		}
		sent := calls[len(calls)-1]
		if sent.api() != c.method || countMethod(calls, c.method) != 1 || !strings.Contains(sent.auth, "Bearer") ||
			sent.form.Has("token_auth") || sent.form.Has("passwordConfirmation") || sent.rawQuery != "" ||
			sent.form.Get("format") != "JSON" || sent.form.Has("callback") {
			t.Errorf("%s: request = %v auth=%q query=%q", c.op, sent.form, sent.auth, sent.rawQuery)
		}
	}
}

func TestOperationsToolsSendTheFixedParameters(t *testing.T) {
	var calls []call
	env := newOperationsEnvironment(t, &calls, operationsAnswers(nil).handler(t))
	for _, c := range operationsCases {
		if _, err := env.change(c.op, c.connection, c.args); err != nil {
			t.Fatalf("%s: %v", c.op, err)
		}
	}
	byMethod := map[string]call{}
	for _, sent := range calls {
		byMethod[sent.api()] = sent
	}
	if f := byMethod["CoreAdminHome.setArchiveSettings"].form; f.Get("enableBrowserTriggerArchiving") != "0" ||
		f.Get("todayArchiveTimeToLive") != "600" {
		t.Errorf("archive settings form = %v", f)
	}
	if f := byMethod["CoreAdminHome.setTrustedHosts"].form; strings.Join(f["trustedHosts[]"], ",") != "analytics.example.test,m.example.test:8443" {
		t.Errorf("trusted hosts form = %v", f)
	}
	if f := byMethod["CoreAdminHome.setBrandingSettings"].form; f.Get("useCustomLogo") != "1" ||
		f.Get("hasCustomLogo") != "1" || f.Get("hasCustomFavicon") != "0" {
		t.Errorf("branding form = %v", f)
	}
	if f := byMethod["CoreAdminHome.archiveReports"].form; f.Get("idSite") != "2" || f.Get("period") != "day" ||
		f.Get("date") != "2026-10-01" || f.Get("segment") != "browserName==Firefox" || f.Get("plugin") != "Actions" ||
		f.Get("report") != "Actions_getPageUrls" {
		t.Errorf("archive form = %v", f)
	}
}

func TestBrandingAndArchiveAnswerForms(t *testing.T) {
	var calls []call
	env := newOperationsEnvironment(t, &calls, operationsAnswers(answers{
		"CoreAdminHome.setBrandingSettings": func(call) *http.Response { return ok(`{"useCustomLogo":false}`) },
		"CoreAdminHome.archiveReports":      func(call) *http.Response { return ok(`[]`) },
	}).handler(t))
	if result, err := env.change(brandingOp, "op-full", `{"useCustomLogo":false}`); err != nil ||
		result != `{"useCustomLogo":false}` {
		t.Errorf("branding off: %s, %v", result, err)
	}
	if result, err := env.change(archiveOp, "op-full", `{"idSite":2,"period":"range","date":"2026-09-01,2026-09-30"}`); err != nil ||
		result != `{"archived":false,"date":"2026-09-01,2026-09-30","idSite":2,"period":"range"}` {
		t.Errorf("empty archive: %s, %v", result, err)
	}
	env = newOperationsEnvironment(t, &calls, operationsAnswers(answers{
		"CoreAdminHome.archiveReports": func(call) *http.Response { return ok(`{"idarchives":5,"nb_visits":0}`) },
	}).handler(t))
	if result, err := env.change(archiveOp, "op-full", `{"idSite":2,"period":"month","date":"today"}`); err != nil ||
		!strings.Contains(result, `"idarchives":[5]`) || !strings.Contains(result, `"nb_visits":0`) {
		t.Errorf("single archive: %s, %v", result, err)
	}
}

func TestOperationsToolsRefuseBeforeSecretsAndIO(t *testing.T) {
	var calls []call
	env := newOperationsEnvironment(t, &calls, operationsAnswers(nil).handler(t))
	long := strings.Repeat("a", 64)
	bad := map[string][]string{
		archiveSettingsOp: {`{"enableBrowserTriggerArchiving":true}`, `{"todayArchiveTimeToLive":600}`,
			`{"enableBrowserTriggerArchiving":true,"todayArchiveTimeToLive":59}`,
			`{"enableBrowserTriggerArchiving":true,"todayArchiveTimeToLive":86401}`,
			`{"enableBrowserTriggerArchiving":"false","todayArchiveTimeToLive":600}`,
			`{"enableBrowserTriggerArchiving":true,"todayArchiveTimeToLive":600,"x":1}`},
		trustedHostsOp: {`{}`, `{"trustedHosts":[]}`, `{"trustedHosts":null}`,
			`{"trustedHosts":["other.example.test"]}`, `{"trustedHosts":["example.test"]}`,
			`{"trustedHosts":["analytics.example.test:8443"]}`, `{"trustedHosts":["analytics.example.test","a b"]}`,
			`{"trustedHosts":["analytics.example.test","https://x.example.test"]}`,
			`{"trustedHosts":["analytics.example.test","a.example.test:99999"]}`,
			`{"trustedHosts":["analytics.example.test","a.example.test:"]}`,
			`{"trustedHosts":["analytics.example.test","-a.example.test"]}`,
			`{"trustedHosts":["analytics.example.test",""]}`,
			`{"trustedHosts":["analytics.example.test"],"x":1}`},
		brandingOp: {`{}`, `{"hasCustomLogo":true}`, `{"useCustomLogo":"yes"}`, `{"useCustomLogo":true,"file":"x"}`},
		archiveOp: {`{"period":"day","date":"today"}`, `{"idSite":2,"date":"today"}`,
			`{"idSite":3,"period":"day","date":"today"}`, `{"idSite":0,"period":"day","date":"today"}`,
			`{"idSite":2,"period":"day","date":"last7"}`, `{"idSite":2,"period":"day","date":"2026-02-30"}`,
			`{"idSite":2,"period":"range","date":"2026-10-01"}`, `{"idSite":2,"period":"range","date":"2026-10-02,2026-10-01"}`,
			`{"idSite":2,"period":"range","date":"2025-01-01,2026-10-01"}`, `{"idSite":2,"period":"year","date":"2026-01-01,2026-10-01"}`,
			`{"idSite":2,"period":"hour","date":"today"}`, `{"idSite":2,"period":"day","date":"today","segment":"a;;b"}`,
			`{"idSite":2,"period":"day","date":"today","plugin":"1x"}`,
			`{"idSite":2,"period":"day","date":"today","report":"` + long + `a"}`,
			`{"idSite":2,"period":"day","date":"today","callback":"x"}`},
	}
	for op, list := range bad {
		for _, args := range list {
			connection := "op-full"
			if op == trustedHostsOp {
				connection = "op-hosts"
			}
			_, err := env.change(op, connection, args)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), "other.example") || strings.Contains(err.Error(), "last7") {
				t.Errorf("%s %.80s: err = %v", op, args, err)
			}
		}
	}
	valid := map[string]string{archiveSettingsOp: `{"enableBrowserTriggerArchiving":true,"todayArchiveTimeToLive":600}`,
		brandingOp: `{"useCustomLogo":false}`, archiveOp: `{"idSite":2,"period":"day","date":"today"}`}
	for op, args := range valid {
		for _, connection := range []string{"op-site"} {
			if _, err := env.change(op, connection, args); err == nil {
				t.Errorf("%s: accepted on %s without the instance target", op, connection)
			}
		}
	}
	if _, err := env.change(archiveOp, "op-instance", valid[archiveOp]); err == nil {
		t.Error("reports.archive accepted on a connection without website targets")
	}
	for op, args := range valid {
		if _, err := env.invoke(op, "op-full", args); err == nil || !strings.Contains(err.Error(), "confirm") {
			t.Errorf("%s: err = %v, want a confirmation request", op, err)
		}
	}
	if _, err := env.invoke(trustedHostsOp, "op-hosts", `{"trustedHosts":["analytics.example.test"]}`); err == nil ||
		!strings.Contains(err.Error(), "confirm") {
		t.Errorf("trusted hosts without confirm: %v", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("refusals sent %d requests and read %d secrets", len(calls), *env.reads)
	}
}

func TestTrustedHostsAreOfferedOnlyThroughTheToolsList(t *testing.T) {
	var calls []call
	env := newOperationsEnvironment(t, &calls, operationsAnswers(nil).handler(t))
	if _, err := env.change(trustedHostsOp, "op-full", `{"trustedHosts":["analytics.example.test"]}`); err == nil {
		t.Error("trusted hosts offered without a tools entry")
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests, %d secret reads", len(calls), *env.reads)
	}
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, tool := range metadata.Tools {
		switch tool.ID {
		case trustedHostsOp:
			if !tool.RequiresToolAllowList || tool.Effect != config.EffectUpdate {
				t.Errorf("%s = %+v", tool.ID, tool)
			}
		case archiveSettingsOp, brandingOp:
			if tool.RequiresToolAllowList || tool.Effect != config.EffectUpdate {
				t.Errorf("%s = %+v", tool.ID, tool)
			}
		case archiveOp:
			if tool.RequiresToolAllowList || tool.Effect != config.EffectExecute {
				t.Errorf("%s = %+v", tool.ID, tool)
			}
		}
	}
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			for _, op := range []string{archiveSettingsOp, trustedHostsOp, brandingOp, archiveOp} {
				if id == op {
					t.Errorf("profile %s holds %s", profile.ID, id)
				}
			}
		}
	}
	for _, op := range []string{archiveSettingsOp, trustedHostsOp, brandingOp, archiveOp} {
		d, _, found := reg.Lookup(op)
		if !found || d.Risk.Confirmation != "required" || !d.Risk.OpenWorld || d.Risk.DataSensitivity == "" ||
			d.Risk.Idempotency == "" {
			t.Errorf("%s: found %t, %+v", op, found, d.Risk)
		}
	}
	if ok, _ := reaches(archiveOp, []string{"site"}); ok {
		t.Error("reports.archive reaches a connection without instance")
	}
	if ok, _ := reaches(archiveOp, []string{"instance"}); ok {
		t.Error("reports.archive reaches a connection without websites")
	}
	if ok, _ := reaches(archiveOp, []string{"site/2", "instance"}); !ok {
		t.Error("reports.archive does not reach instance and website")
	}
	for _, op := range []string{archiveSettingsOp, trustedHostsOp, brandingOp} {
		if ok, _ := reaches(op, []string{"site"}); ok {
			t.Errorf("%s reaches a connection without instance", op)
		}
		if ok, _ := reaches(op, []string{"instance"}); !ok {
			t.Errorf("%s does not reach an instance connection", op)
		}
	}
}

func TestOperationsToolsMapErrorsAndNeverRetry(t *testing.T) {
	for _, c := range operationsCases {
		for name, message := range map[string]string{
			"permission": "You can't access this resource as it requires 'superuser' access." + bodyCanary,
		} {
			var calls []call
			env := newOperationsEnvironment(t, &calls, operationsAnswers(answers{c.method: func(call) *http.Response {
				return failure(message)
			}}).handler(t))
			_, err := env.change(c.op, c.connection, c.args)
			if classOf(err) != provider.ClassPermission || strings.Contains(err.Error(), bodyCanary) ||
				countMethod(calls, c.method) != 1 {
				t.Errorf("%s %s: err = %v", c.op, name, err)
			}
		}
		for name, message := range map[string]string{
			"not found": "The website id = 2 does not exist " + bodyCanary,
			"missing":   "The method 'x' does not exist or is not available in the module 'CoreAdminHome'." + bodyCanary,
			"inactive":  "The plugin 'CoreAdminHome' is not activated " + bodyCanary,
			"unknown":   "Something odd " + bodyCanary,
		} {
			var calls []call
			env := newOperationsEnvironment(t, &calls, operationsAnswers(answers{c.method: func(call) *http.Response {
				return failure(message)
			}}).handler(t))
			_, err := env.change(c.op, c.connection, c.args)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || countMethod(calls, c.method) != 1 {
				t.Errorf("%s %s: err = %v, sent %d", c.op, name, err, countMethod(calls, c.method))
			}
		}
		for name, handler := range map[string]func(call) *http.Response{
			"html":   func(call) *http.Response { return ok(`<html>` + bodyCanary + `</html>`) },
			"server": func(call) *http.Response { return respond(500, bodyCanary) },
			"rate":   func(call) *http.Response { return respond(429, bodyCanary) },
			"empty":  func(call) *http.Response { return ok(`"nothing"`) },
		} {
			var calls []call
			env := newOperationsEnvironment(t, &calls, operationsAnswers(answers{c.method: handler}).handler(t))
			_, err := env.change(c.op, c.connection, c.args)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || countMethod(calls, c.method) != 1 {
				t.Errorf("%s %s: err = %v, sent %d", c.op, name, err, countMethod(calls, c.method))
			}
			if name == "rate" && classOf(err) != provider.ClassRateLimited {
				t.Errorf("%s: 429 class = %v", c.op, classOf(err))
			}
			if name != "rate" && (err == nil || !strings.Contains(err.Error(), "never repeated")) {
				t.Errorf("%s %s: no uncertainty reported: %v", c.op, name, err)
			}
		}
		var calls []call
		env := newOperationsEnvironment(t, &calls, func(x call) (*http.Response, error) {
			if x.api() == c.method {
				return nil, context.DeadlineExceeded
			}
			return operationsAnswers(nil).handler(t)(x)
		})
		if _, err := env.change(c.op, c.connection, c.args); classOf(err) != provider.ClassTimeout ||
			!strings.Contains(err.Error(), "never repeated") || countMethod(calls, c.method) != 1 {
			t.Errorf("%s timeout: err = %v", c.op, err)
		}
	}
}
