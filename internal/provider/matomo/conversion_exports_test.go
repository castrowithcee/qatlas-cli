package matomo

import (
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/application"
	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
)

// Assumed, as the reference gives no example and the plugin source is not public: getConversionExports answers a
// list of export objects and getConversionExport one such object, in the Matomo core convention (lower case
// member names, numbers possibly as text), each carrying an access token and an export address that holds the
// token; deleteConversionExport answers the generic success form {"result":"success","message":"ok"}.
const (
	conversionTokenA = "canary-export-access-token-aaaa1111"
	conversionTokenB = "canary-export-plain-token-bbbb2222"
	conversionTokenC = "canary-export-camel-token-cccc3333"
	conversionTokenD = "canary-export-url-token-dddd4444"
	conversionTokenE = "canary-export-link-token-eeee5555"
)

func convExportRow(id, site string) string {
	return `{"idexport":"` + id + `","idsite":` + site + `,"name":"Ads ` + id + `","type":"google","enabled":1,` +
		`"access_token":"` + conversionTokenA + `","token":"` + conversionTokenB + `","accessToken":"` + conversionTokenC + `",` +
		`"export_url":"https://matomo.example.test/index.php?module=API&idExport=` + id + `&token_auth=` + conversionTokenD + `",` +
		`"link":"https://matomo.example.test/export/` + id + `?key=` + conversionTokenE + `"}`
}

var conversionTokens = []string{conversionTokenA, conversionTokenB, conversionTokenC, conversionTokenD, conversionTokenE}

const conversionConnections = `
  ce-two: {service: matomo, credential: token, effects: [read], targets: [site/1, site/2]}
  ce-one: {service: matomo, credential: token, effects: [read], targets: [site/1]}
  ce-all: {service: matomo, credential: token, effects: [read]}
  ce-instance: {service: matomo, credential: token, effects: [read], targets: [instance]}
  ce-deleter: {service: matomo, credential: token, tools: [matomo.conversion-exports.delete], targets: [site/1, site/2]}
  ce-deleter-one: {service: matomo, credential: token, tools: [matomo.conversion-exports.delete], targets: [site/1]}
  ce-effects-delete: {service: matomo, credential: token, effects: [read, delete], targets: [site/1]}
`

func newConversionEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(conversionConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func conversionAnswers(extra answers) answers {
	return with(with(pluginsOn(), answers{
		"AdvertisingConversionExport.getConversionExports": fixed("[" + convExportRow("5", "1") + "," + convExportRow("6", "2") + "," +
			convExportRow("7", "3") + `,{"name":"No ID","idsite":1},{"idexport":8,"name":"No site"}]`),
		"AdvertisingConversionExport.getConversionExport": func(c call) *http.Response {
			switch c.form.Get("idExport") {
			case "5":
				return ok(convExportRow("5", "1"))
			case "6":
				return ok(convExportRow("6", "2"))
			case "7":
				return ok(convExportRow("7", "3"))
			case "9":
				return ok(convExportRow("5", "1"))
			}
			return failure("The export was not found.")
		},
		"AdvertisingConversionExport.deleteConversionExport": fixed(`{"result":"success","message":"ok"}`),
	}), extra)
}

func conversionIDs(result string) string {
	var out []string
	for _, part := range strings.Split(result, `"idExport":`)[1:] {
		out = append(out, part[:1])
	}
	return strings.Join(out, ",")
}

func TestConversionExportToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	for tool, want := range map[string]struct {
		effect  capability.Effect
		listed  bool
		confirm capability.Confirmation
	}{
		"matomo.conversion-exports.list":   {capability.EffectRead, false, capability.ConfirmationNone},
		"matomo.conversion-exports.delete": {capability.EffectDelete, true, capability.ConfirmationRequired},
	} {
		d, _, found := reg.Lookup(tool)
		if !found {
			t.Fatalf("%s is not registered", tool)
		}
		if d.Risk.Effect != want.effect || d.Risk.Confirmation != want.confirm || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity == "" || d.Risk.Idempotency == "" || d.RequiresToolAllowList != want.listed ||
			instanceTools[tool] || !strings.Contains(string(d.InputSchema), `"idSite"`) {
			t.Errorf("%s: risk = %+v, listed-only = %v", tool, d.Risk, d.RequiresToolAllowList)
		}
		for _, banned := range []string{"callback", "token", "module", "method", "format", "filter_limit", "token_auth", "url"} {
			if strings.Contains(string(d.InputSchema), `"`+banned+`"`) {
				t.Errorf("%s schema offers %s", tool, banned)
			}
		}
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if id == tool {
					t.Errorf("profile %s selects %s", profile.ID, tool)
				}
			}
		}
	}
	// The excluded methods have no tool.
	for _, tool := range metadata.Tools {
		if strings.HasPrefix(tool.ID, "matomo.conversion-exports.") && tool.ID != "matomo.conversion-exports.list" &&
			tool.ID != "matomo.conversion-exports.delete" {
			t.Errorf("unexpected tool %s", tool.ID)
		}
	}
}

func TestConversionExportListFiltersRowsByTheTargets(t *testing.T) {
	var calls []call
	env := newConversionEnvironment(t, &calls, conversionAnswers(nil).handler(t))
	result, err := env.invoke("matomo.conversion-exports.list", "ce-two", `{}`)
	if err != nil || conversionIDs(result) != "5,6" || !strings.Contains(result, `"has_more":false`) {
		t.Fatalf("two websites: %s, %v", result, err)
	}
	sent := lastCall(calls, "AdvertisingConversionExport.getConversionExports")
	if sent.form.Has("idSite") || sent.form.Get("format") != "JSON" || sent.auth != "Bearer "+tokenValue ||
		strings.Contains(sent.rawQuery+sent.body, tokenValue) {
		t.Errorf("request = %+v", sent)
	}
	if result, err = env.invoke("matomo.conversion-exports.list", "ce-one", `{}`); err != nil || conversionIDs(result) != "5" {
		t.Errorf("one website: %s, %v", result, err)
	}
	if result, err = env.invoke("matomo.conversion-exports.list", "ce-all", `{}`); err != nil || conversionIDs(result) != "5,6,7" {
		t.Errorf("all websites: %s, %v", result, err)
	}
	// With idSite the website is sent and rows of another website are not shown even if Matomo answers them.
	if result, err = env.invoke("matomo.conversion-exports.list", "ce-two", `{"idSite":2}`); err != nil || conversionIDs(result) != "6" {
		t.Errorf("idSite 2: %s, %v", result, err)
	}
	if lastCall(calls, "AdvertisingConversionExport.getConversionExports").form.Get("idSite") != "2" {
		t.Errorf("idSite not sent")
	}
	env.excluding("ce-all", "site/6")
	rows := conversionIDs(mustInvokeExport(t, env, "matomo.conversion-exports.list", "ce-all", `{}`))
	if rows != "5,6,7" {
		t.Errorf("exclusion of an unrelated website: %s", rows)
	}
	env.excluding("ce-all", "site/2")
	if rows = conversionIDs(mustInvokeExport(t, env, "matomo.conversion-exports.list", "ce-all", `{}`)); rows != "5,7" {
		t.Errorf("exclusion of website 2: %s", rows)
	}
}

func mustInvokeExport(t *testing.T, env *environment, tool, connection, arguments string) string {
	t.Helper()
	result, err := env.invoke(tool, connection, arguments)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return result
}

func TestConversionExportListCapsTheRows(t *testing.T) {
	rows := make([]string, maxConversionExports+3)
	for i := range rows {
		rows[i] = `{"idexport":` + strings.Repeat("1", 1) + `,"idsite":1,"name":"` + strings.Repeat("x", maxStringLength+10) + `"}`
	}
	var calls []call
	env := newConversionEnvironment(t, &calls, conversionAnswers(answers{
		"AdvertisingConversionExport.getConversionExports": fixed("[" + strings.Join(rows, ",") + "]")}).handler(t))
	result := mustInvokeExport(t, env, "matomo.conversion-exports.list", "ce-two", `{}`)
	if strings.Count(result, `"idExport"`) != maxConversionExports || !strings.Contains(result, `"has_more":true`) ||
		strings.Contains(result, strings.Repeat("x", maxStringLength+1)) {
		t.Errorf("rows are not capped: %d bytes", len(result))
	}
}

func TestConversionExportNeverReturnsTokensAndRegistersThem(t *testing.T) {
	var calls []call
	env := newConversionEnvironment(t, &calls, conversionAnswers(nil).handler(t))
	for _, c := range []struct{ name, connection, arguments string }{
		{"list", "ce-two", `{}`}, {"list of a website", "ce-two", `{"idSite":1}`}, {"detail", "ce-two", `{"idExport":5}`},
		{"detail with website", "ce-two", `{"idSite":2,"idExport":6}`},
	} {
		result := mustInvokeExport(t, env, "matomo.conversion-exports.list", c.connection, c.arguments)
		for _, secretValue := range conversionTokens {
			if strings.Contains(result, secretValue) {
				t.Errorf("%s returns a token: %s", c.name, result)
			}
		}
		for _, name := range []string{"token", "access_token", "accessToken", "export_url", "link", "token_auth", "://"} {
			if strings.Contains(result, name) {
				t.Errorf("%s returns %q: %s", c.name, name, result)
			}
		}
		if !strings.Contains(result, `"name":"Ads `) || !strings.Contains(result, `"type":"google"`) ||
			!strings.Contains(result, `"enabled":true`) {
			t.Errorf("%s lacks the allowlisted fields: %s", c.name, result)
		}
	}
	for _, secretValue := range conversionTokens {
		if got := env.red.Apply("x " + secretValue + " y"); strings.Contains(got, secretValue) {
			t.Errorf("the redactor does not know %s", secretValue)
		}
	}
	// A token in an allowlisted field is dropped rather than shown.
	env = newConversionEnvironment(t, &calls, conversionAnswers(answers{
		"AdvertisingConversionExport.getConversionExport": fixed(`{"idexport":5,"idsite":1,"name":"` + conversionTokenA +
			`","type":"x","access_token":"` + conversionTokenA + `"}`)}).handler(t))
	if result := mustInvokeExport(t, env, "matomo.conversion-exports.list", "ce-two", `{"idExport":5}`); strings.Contains(result, conversionTokenA) {
		t.Errorf("name carries a token: %s", result)
	}
}

func TestConversionExportErrorsCarryNoTokens(t *testing.T) {
	var calls []call
	env := newConversionEnvironment(t, &calls, conversionAnswers(nil).handler(t))
	for name, c := range map[string]struct{ tool, connection, arguments string }{
		"foreign detail":   {"matomo.conversion-exports.list", "ce-two", `{"idExport":7}`},
		"wrong website":    {"matomo.conversion-exports.list", "ce-two", `{"idSite":1,"idExport":6}`},
		"foreign delete":   {"matomo.conversion-exports.delete", "ce-deleter", `{"idSite":1,"idExport":7}`},
		"mismatch delete":  {"matomo.conversion-exports.delete", "ce-deleter", `{"idSite":1,"idExport":6}`},
		"returned another": {"matomo.conversion-exports.list", "ce-two", `{"idExport":9}`},
	} {
		_, err := env.change(c.tool, c.connection, c.arguments)
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		for _, secretValue := range conversionTokens {
			if strings.Contains(err.Error(), secretValue) || strings.Contains(env.red.Error(err), secretValue) {
				t.Errorf("%s: error carries a token: %v", name, err)
			}
		}
		if strings.Contains(err.Error(), "site/") || strings.Contains(err.Error(), "987") {
			t.Errorf("%s: error names a target: %v", name, err)
		}
	}
	if countMethod(calls, "AdvertisingConversionExport.deleteConversionExport") != 0 {
		t.Errorf("a foreign export was deleted")
	}
	// A failing Matomo answer that repeats the token is not passed on.
	env = newConversionEnvironment(t, &calls, conversionAnswers(answers{
		"AdvertisingConversionExport.getConversionExports": func(call) *http.Response {
			return failure("bad " + conversionTokenA + bodyCanary)
		}}).handler(t))
	if _, err := env.invoke("matomo.conversion-exports.list", "ce-two", `{}`); err == nil ||
		strings.Contains(err.Error(), conversionTokenA) || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("err = %v", err)
	}
}

func TestConversionExportDetailIsBoundToItsWebsite(t *testing.T) {
	var calls []call
	env := newConversionEnvironment(t, &calls, conversionAnswers(nil).handler(t))
	result, err := env.invoke("matomo.conversion-exports.list", "ce-two", `{"idExport":5}`)
	if err != nil || conversionIDs(result) != "5" || !strings.Contains(result, `"idSite":1`) {
		t.Fatalf("detail: %s, %v", result, err)
	}
	form := lastCall(calls, "AdvertisingConversionExport.getConversionExport").form
	if form.Get("idExport") != "5" || form.Has("idSite") {
		t.Errorf("form = %v", form)
	}
	for name, c := range map[string]struct{ connection, arguments string }{
		"outside the targets": {"ce-two", `{"idExport":7}`},
		"website of the one":  {"ce-one", `{"idExport":6}`},
		"another idSite":      {"ce-two", `{"idSite":2,"idExport":5}`},
	} {
		result, err := env.invoke("matomo.conversion-exports.list", c.connection, c.arguments)
		if err == nil || result != "" || classOf(err) != provider.ClassNotFound {
			t.Errorf("%s: %q, %v", name, result, err)
		}
	}
	for name, arguments := range map[string]string{"zero": `{"idExport":0}`, "negative": `{"idExport":-1}`,
		"text": `{"idExport":"x"}`, "huge": `{"idExport":1000000000}`, "unknown": `{"idExport":1,"token":"x"}`} {
		calls = nil
		if _, err := env.invoke("matomo.conversion-exports.list", "ce-two", arguments); err == nil || len(calls) != 0 {
			t.Errorf("%s: err = %v, %d requests", name, err, len(calls))
		}
	}
}

func TestConversionExportToolsRefuseForeignWebsitesBeforeSecretAndIO(t *testing.T) {
	var calls []call
	env := newConversionEnvironment(t, &calls, conversionAnswers(nil).handler(t))
	for _, c := range []struct{ tool, connection, arguments string }{
		{"matomo.conversion-exports.list", "ce-two", `{"idSite":987}`},
		{"matomo.conversion-exports.list", "ce-two", `{"idSite":3,"idExport":5}`},
		{"matomo.conversion-exports.list", "ce-instance", `{}`},
		{"matomo.conversion-exports.list", "ce-instance", `{"idSite":1}`},
		{"matomo.conversion-exports.delete", "ce-deleter", `{"idSite":987,"idExport":5}`},
		{"matomo.conversion-exports.delete", "ce-deleter", `{"idExport":5}`},
		{"matomo.conversion-exports.delete", "ce-deleter-one", `{"idSite":2,"idExport":6}`},
	} {
		_, err := env.change(c.tool, c.connection, c.arguments)
		if err == nil || strings.Contains(err.Error(), "987") || strings.Contains(err.Error(), "site/") {
			t.Errorf("%s %s %s: err = %v", c.tool, c.connection, c.arguments, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusals", len(calls), *env.reads)
	}
}

func TestConversionExportDeleteIsListedOnlyAndSendsExactlyOneMutation(t *testing.T) {
	const tool = "matomo.conversion-exports.delete"
	var calls []call
	env := newConversionEnvironment(t, &calls, conversionAnswers(nil).handler(t))
	if _, err := env.change(tool, "ce-effects-delete", `{"idSite":1,"idExport":5}`); err == nil || len(calls) != 0 || *env.reads != 0 {
		t.Errorf("through effects only: err = %v, requests = %d", err, len(calls))
	}
	// Without confirmation nothing happens.
	if _, err := env.invoke(tool, "ce-deleter", `{"idSite":1,"idExport":5}`); err == nil || len(calls) != 0 {
		t.Errorf("unconfirmed: err = %v, requests = %d", err, len(calls))
	}
	result, err := env.change(tool, "ce-deleter", `{"idSite":1,"idExport":5}`)
	if err != nil || !sameJSON(result, `{"idSite":1,"idExport":5,"deleted":true}`) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated AdvertisingConversionExport.getConversionExport "+
		"AdvertisingConversionExport.deleteConversionExport" {
		t.Errorf("requests = %s", methods(calls))
	}
	sent := lastCall(calls, "AdvertisingConversionExport.deleteConversionExport")
	if sent.form.Get("idExport") != "5" || sent.form.Get("idSite") != "1" || sent.auth != "Bearer "+tokenValue ||
		strings.Contains(sent.rawQuery+sent.body, tokenValue) || sent.form.Has("token_auth") {
		t.Errorf("request = %+v", sent)
	}
	// One website connections need no idSite.
	if _, err := env.change(tool, "ce-deleter-one", `{"idExport":5}`); err != nil {
		t.Errorf("one website: %v", err)
	}
	// A success form without result success is accepted when it is a confirmed flag, and nothing else is.
	for body, accepted := range map[string]bool{`{"value":true}`: true, `true`: true, `{"value":false}`: false,
		`{"result":"success"}`: true, `[]`: false, `"x"`: false} {
		calls = nil
		env = newConversionEnvironment(t, &calls, conversionAnswers(answers{
			"AdvertisingConversionExport.deleteConversionExport": fixed(body)}).handler(t))
		_, err := env.change(tool, "ce-deleter", `{"idSite":1,"idExport":5}`)
		if (err == nil) != accepted || countMethod(calls, "AdvertisingConversionExport.deleteConversionExport") != 1 {
			t.Errorf("%s: err = %v", body, err)
		}
	}
}

func TestConversionExportErrorsMapToClassesWithoutProviderText(t *testing.T) {
	for _, c := range []struct{ tool, connection, arguments, method string }{
		{"matomo.conversion-exports.list", "ce-two", `{}`, "AdvertisingConversionExport.getConversionExports"},
		{"matomo.conversion-exports.list", "ce-two", `{"idExport":5}`, "AdvertisingConversionExport.getConversionExport"},
		{"matomo.conversion-exports.delete", "ce-deleter", `{"idSite":1,"idExport":5}`, "AdvertisingConversionExport.deleteConversionExport"},
		{"matomo.conversion-exports.delete", "ce-deleter", `{"idSite":1,"idExport":5}`, "AdvertisingConversionExport.getConversionExport"},
	} {
		for name, tc := range map[string]struct {
			answer      func(call) (*http.Response, error)
			class       provider.Class
			unsupported bool
			unclear     bool
		}{
			"permission": {func(call) (*http.Response, error) {
				return ok2(failure("You need to have write access to at least one website. " + permissionMsg + bodyCanary))
			}, provider.ClassPermission, false, false},
			"not found": {func(call) (*http.Response, error) {
				return ok2(failure("An unexpected website was found in the request: website id was set to '1' ." + bodyCanary))
			}, provider.ClassNotFound, false, false},
			"method missing": {func(call) (*http.Response, error) {
				return ok2(failure("The method '" + c.method + "' does not exist or is not available in the module 'AdvertisingConversionExport'." + bodyCanary))
			}, "", true, false},
			"plugin inactive": {func(call) (*http.Response, error) {
				return ok2(failure("The plugin AdvertisingConversionExport is not activated." + bodyCanary))
			}, "", true, false},
			"429":        {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, bodyCanary), nil }, provider.ClassRateLimited, false, false},
			"timeout":    {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false, true},
			"unreadable": {func(call) (*http.Response, error) { return ok2(ok("<html>" + bodyCanary + "</html>")) }, provider.ClassInvalidResponse, false, true},
		} {
			mutation := c.method == "AdvertisingConversionExport.deleteConversionExport"
			var calls []call
			env := newConversionEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if in.api() == c.method {
					return tc.answer(in)
				}
				return conversionAnswers(nil).handler(t)(in)
			})
			_, err := env.change(c.tool, c.connection, c.arguments)
			unclear := tc.unclear && mutation
			if err == nil || strings.Contains(err.Error(), bodyCanary) || isUnsupported(err) != tc.unsupported ||
				countMethod(calls, c.method) != 1 || strings.Contains(err.Error(), "never repeated") != unclear ||
				tc.class != "" && classOf(err) != tc.class {
				t.Errorf("%s %s %s: err = %v (class %q), sent %d times", c.tool, c.method, name, err, classOf(err), countMethod(calls, c.method))
			}
			if !mutation && c.tool == "matomo.conversion-exports.delete" && countMethod(calls, "AdvertisingConversionExport.deleteConversionExport") != 0 {
				t.Errorf("%s %s: deleted after a failed proof read", c.method, name)
			}
		}
	}
}

func TestConversionExportToolsRequireTheActivePlugin(t *testing.T) {
	var calls []call
	env := newConversionEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"API.isPluginActivated": fixed(`{"value":false}`),
	}).handler(t))
	for _, c := range []struct{ tool, connection, arguments string }{
		{"matomo.conversion-exports.list", "ce-two", `{}`},
		{"matomo.conversion-exports.list", "ce-two", `{"idExport":5}`},
		{"matomo.conversion-exports.delete", "ce-deleter", `{"idSite":1,"idExport":5}`},
	} {
		_, err := env.change(c.tool, c.connection, c.arguments)
		if !isUnsupported(err) || !strings.Contains(err.Error(), conversionExportsPlugin) {
			t.Errorf("%s: err = %v", c.tool, err)
		}
	}
	if n := countMethod(calls, "AdvertisingConversionExport.getConversionExports") +
		countMethod(calls, "AdvertisingConversionExport.getConversionExport") +
		countMethod(calls, "AdvertisingConversionExport.deleteConversionExport"); n != 0 {
		t.Errorf("requests = %s", methods(calls))
	}
}

func TestConversionExportListRefusesUnreadableAnswers(t *testing.T) {
	for _, body := range []string{`"` + bodyCanary + `"`, `<html>` + bodyCanary + `</html>`, ``, `7`} {
		var calls []call
		env := newConversionEnvironment(t, &calls, conversionAnswers(answers{
			"AdvertisingConversionExport.getConversionExports": fixed(body),
			"AdvertisingConversionExport.getConversionExport":  fixed(body)}).handler(t))
		for _, arguments := range []string{`{}`, `{"idExport":5}`} {
			_, err := env.invoke("matomo.conversion-exports.list", "ce-two", arguments)
			if classOf(err) != provider.ClassInvalidResponse || strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("%q %s: err = %v", body, arguments, err)
			}
		}
	}
}

func TestConversionExportExamplesRunThroughTheirHandlers(t *testing.T) {
	var calls []call
	env := newConversionEnvironment(t, &calls, conversionAnswers(nil).handler(t))
	for _, d := range []capability.Descriptor{conversionExportsList, conversionExportsDelete} {
		connection := "ce-two"
		if d.RequiresToolAllowList {
			connection = "ce-deleter"
		}
		for _, example := range d.Examples {
			if _, err := env.change(d.ID, connection, string(example.Arguments)); err != nil {
				t.Errorf("%s example %q: %v", d.ID, example.Description, err)
			}
		}
	}
}
