package matomo

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/application"
	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
)

// Assumptions, as the CustomReports reference gives no example for the write methods: addCustomReport answers the
// ID of the new report (bare or as {"value":N}), updateCustomReport, pauseCustomReport, resumeCustomReport, and
// deleteCustomReport answer the generic success form {"result":"success","message":"ok"}, and duplicateCustomReport answers an
// object whose form is only described in words, here with a member success and the ID of the new report.
const customReportConnections = `
  cr-writer: {service: matomo, credential: token, effects: [read, create, update]}
  cr-writer-one: {service: matomo, credential: token, effects: [read, create, update], targets: [site/1]}
  cr-writer-two: {service: matomo, credential: token, effects: [read, create, update], targets: [site/2]}
  cr-writer-both: {service: matomo, credential: token, effects: [read, create, update], targets: [site/1, site/2]}
  cr-deleter: {service: matomo, credential: token, tools: [matomo.custom-reports.delete]}
  cr-deleter-one: {service: matomo, credential: token, tools: [matomo.custom-reports.delete], targets: [site/1]}
  cr-deleter-two: {service: matomo, credential: token, tools: [matomo.custom-reports.delete], targets: [site/2]}
  cr-deleter-both: {service: matomo, credential: token, tools: [matomo.custom-reports.delete], targets: [site/1, site/2]}
  cr-effects-delete: {service: matomo, credential: token, effects: [read, create, update, delete]}
`

func newCustomReportChangeEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(customReportConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

// crConn names the connection that offers the tool: the listed-only delete tool needs one with a tools list.
func crConn(tool, writer string) string {
	if tool == "matomo.custom-reports.delete" {
		return strings.Replace(writer, "cr-writer", "cr-deleter", 1)
	}
	return writer
}

func customReportChangeAnswers(extra answers) answers {
	return customReportAnswers(with(answers{
		"CustomReports.addCustomReport":       fixed(`{"value":15}`),
		"CustomReports.updateCustomReport":    fixed(`{"result":"success","message":"ok"}`),
		"CustomReports.duplicateCustomReport": fixed(`{"success":true,"idcustomreport":16}`),
		"CustomReports.pauseCustomReport":     fixed(`{"result":"success","message":"ok"}`),
		"CustomReports.deleteCustomReport":    fixed(`{"result":"success","message":"ok"}`),
		"CustomReports.resumeCustomReport":    fixed(`{"result":"success","message":"ok"}`),
	}, extra))
}

const customReportDefinitionArguments = `"name":"Pages","reportType":"table","metricIds":["nb_visits","pageviews"]`

var customReportChanges = []struct{ tool, arguments, method, reads string }{
	{"matomo.custom-reports.add", `{"idSite":1,` + customReportDefinitionArguments + `}`,
		"CustomReports.addCustomReport", ""},
	{"matomo.custom-reports.update", `{"idSite":1,"idCustomReport":1,` + customReportDefinitionArguments + `}`,
		"CustomReports.updateCustomReport", "CustomReports.getConfiguredReport"},
	{"matomo.custom-reports.duplicate", `{"idSite":1,"idCustomReport":1,"idDestinationSites":[1,2]}`,
		"CustomReports.duplicateCustomReport", "CustomReports.getConfiguredReport"},
	{"matomo.custom-reports.pause", `{"idSite":1,"idCustomReport":1}`,
		"CustomReports.pauseCustomReport", "CustomReports.getConfiguredReport"},
	{"matomo.custom-reports.pause", `{"idSite":1,"idCustomReport":1,"resume":true}`,
		"CustomReports.resumeCustomReport", "CustomReports.getConfiguredReport"},
	{"matomo.custom-reports.delete", `{"idSite":1,"idCustomReport":1}`,
		"CustomReports.deleteCustomReport", "CustomReports.getConfiguredReport"},
}

func TestCustomReportChangeToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
	}{
		"matomo.custom-reports.add":       {capability.EffectCreate, capability.IdempotencyNonIdempotent},
		"matomo.custom-reports.update":    {capability.EffectUpdate, capability.IdempotencyIdempotent},
		"matomo.custom-reports.duplicate": {capability.EffectCreate, capability.IdempotencyNonIdempotent},
		"matomo.custom-reports.pause":     {capability.EffectUpdate, capability.IdempotencyIdempotent},
		"matomo.custom-reports.delete":    {capability.EffectDelete, capability.IdempotencyIdempotent},
	}
	for tool, w := range want {
		d, _, found := reg.Lookup(tool)
		if !found {
			t.Fatalf("%s is not registered", tool)
		}
		if d.Risk.Effect != w.effect || d.Risk.Idempotency != w.idempotency ||
			d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld ||
			d.Risk.DataSensitivity != customReportsSensitive || d.RequiresToolAllowList != (tool == "matomo.custom-reports.delete") ||
			!strings.Contains(d.Description, "never repeated") {
			t.Errorf("%s: risk = %+v, description %q", tool, d.Risk, d.Description)
		}
		for _, banned := range []string{"callback", "token", "module", "method", "format", "filter_limit"} {
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
	for _, other := range []string{"matomo.custom-reports.resume", "matomo.custom-metrics.add"} {
		if _, _, found := reg.Lookup(other); found {
			t.Errorf("%s is offered", other)
		}
	}
}

func TestCustomReportChangesSendOneRequestWithTheGivenParametersOnly(t *testing.T) {
	var calls []call
	env := newCustomReportChangeEnvironment(t, &calls, customReportChangeAnswers(nil).handler(t))
	full := `"name":"Pages","reportType":"evolution","metricIds":["nb_visits","pageviews"],"categoryId":"General_Actions",` +
		`"dimensionIds":["Actions.PageTitle"],"subcategoryId":"General_Pages","description":"About pages",` +
		`"segmentFilter":"deviceType==smartphone","multipleIdSites":[1,2]`
	for _, tc := range []struct {
		tool, arguments, method string
		want                    map[string]string
		result                  string
	}{
		{"matomo.custom-reports.add", `{"idSite":1,` + full + `}`, "CustomReports.addCustomReport", map[string]string{
			"idSite": "1", "name": "Pages", "reportType": "evolution", "metricIds[0]": "nb_visits", "metricIds[1]": "pageviews",
			"categoryId": "General_Actions", "dimensionIds[0]": "Actions.PageTitle", "subcategoryId": "General_Pages",
			"description": "About pages", "segmentFilter": "deviceType==smartphone", "multipleIdSites[0]": "1", "multipleIdSites[1]": "2"},
			`{"created":true,"idCustomReport":15,"idSite":1}`},
		{"matomo.custom-reports.add", `{"idSite":1,` + customReportDefinitionArguments + `}`, "CustomReports.addCustomReport",
			map[string]string{"idSite": "1", "name": "Pages", "reportType": "table", "metricIds[0]": "nb_visits", "metricIds[1]": "pageviews"},
			`{"created":true,"idCustomReport":15,"idSite":1}`},
		{"matomo.custom-reports.update", `{"idSite":1,"idCustomReport":1,` + full + `,"subCategoryReportIds":[2,3]}`,
			"CustomReports.updateCustomReport", map[string]string{
				"idSite": "1", "idCustomReport": "1", "name": "Pages", "reportType": "evolution", "metricIds[0]": "nb_visits",
				"metricIds[1]": "pageviews", "categoryId": "General_Actions", "dimensionIds[0]": "Actions.PageTitle",
				"subcategoryId": "General_Pages", "description": "About pages", "segmentFilter": "deviceType==smartphone",
				"multipleIdSites[0]": "1", "multipleIdSites[1]": "2", "subCategoryReportIds[0]": "2", "subCategoryReportIds[1]": "3"},
			`{"idCustomReport":1,"idSite":1,"saved":true}`},
		{"matomo.custom-reports.update", `{"idSite":1,"idCustomReport":1,` + customReportDefinitionArguments + `}`,
			"CustomReports.updateCustomReport", map[string]string{"idSite": "1", "idCustomReport": "1", "name": "Pages",
				"reportType": "table", "metricIds[0]": "nb_visits", "metricIds[1]": "pageviews"},
			`{"idCustomReport":1,"idSite":1,"saved":true}`},
		{"matomo.custom-reports.duplicate", `{"idSite":1,"idCustomReport":1,"idDestinationSites":[2,1]}`,
			"CustomReports.duplicateCustomReport", map[string]string{"idSite": "1", "idCustomReport": "1",
				"idDestinationSites[0]": "2", "idDestinationSites[1]": "1"},
			`{"answer":{"idcustomreport":16,"success":true},"duplicated":true,"idCustomReport":1,"idDestinationSites":[2,1],"idSite":1}`},
		{"matomo.custom-reports.duplicate", `{"idSite":1,"idCustomReport":1}`, "CustomReports.duplicateCustomReport",
			map[string]string{"idSite": "1", "idCustomReport": "1", "idDestinationSites[0]": "1"},
			`{"answer":{"idcustomreport":16,"success":true},"duplicated":true,"idCustomReport":1,"idDestinationSites":[1],"idSite":1}`},
		{"matomo.custom-reports.pause", `{"idSite":1,"idCustomReport":1}`, "CustomReports.pauseCustomReport",
			map[string]string{"idSite": "1", "idCustomReport": "1"}, `{"idCustomReport":1,"idSite":1,"paused":true}`},
		{"matomo.custom-reports.pause", `{"idSite":1,"idCustomReport":1,"resume":false}`, "CustomReports.pauseCustomReport",
			map[string]string{"idSite": "1", "idCustomReport": "1"}, `{"idCustomReport":1,"idSite":1,"paused":true}`},
		{"matomo.custom-reports.pause", `{"idSite":1,"idCustomReport":1,"resume":true}`, "CustomReports.resumeCustomReport",
			map[string]string{"idSite": "1", "idCustomReport": "1"}, `{"idCustomReport":1,"idSite":1,"paused":false}`},
		{"matomo.custom-reports.delete", `{"idSite":1,"idCustomReport":1}`, "CustomReports.deleteCustomReport",
			map[string]string{"idSite": "1", "idCustomReport": "1"}, `{"deleted":true,"idCustomReport":1,"idSite":1}`},
	} {
		calls = nil
		if _, err := env.invoke(tc.tool, crConn(tc.tool, "cr-writer-both"), tc.arguments); err == nil || !strings.Contains(err.Error(), "confirm") || len(calls) != 0 {
			t.Fatalf("%s without confirm: err = %v, requests = %d", tc.tool, err, len(calls))
		}
		result, err := env.change(tc.tool, crConn(tc.tool, "cr-writer-both"), tc.arguments)
		if err != nil || result != tc.result {
			t.Fatalf("%s %s: result = %s, err = %v", tc.tool, tc.arguments, result, err)
		}
		reads := ""
		if !strings.HasSuffix(tc.method, "addCustomReport") {
			reads = " CustomReports.getConfiguredReport"
		}
		// The instance probe is cached across tests, so it may be missing from the requests.
		if got := strings.TrimPrefix(strings.TrimPrefix(methods(calls), "API.getMatomoVersion "), "API.isPluginActivated "); got != strings.TrimPrefix(reads+" "+tc.method, " ") {
			t.Errorf("%s: requests = %s", tc.tool, got)
		}
		if reads != "" && countMethod(calls, "CustomReports.getConfiguredReport") != 1 {
			t.Errorf("%s: proof reads = %d", tc.tool, countMethod(calls, "CustomReports.getConfiguredReport"))
		}
		form := lastCall(calls, tc.method).form
		for key, value := range tc.want {
			if form.Get(key) != value {
				t.Errorf("%s: %s = %q, want %q (form %v)", tc.tool, key, form.Get(key), value, form)
			}
		}
		if len(form) != len(tc.want)+3 || form.Get("format") != "JSON" || form.Get("module") != "API" || form.Get("method") != tc.method {
			t.Errorf("%s: form = %v", tc.tool, form)
		}
	}
}

func TestCustomReportChangeRefusesBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newCustomReportChangeEnvironment(t, &calls, customReportChangeAnswers(nil).handler(t))
	def := customReportDefinitionArguments
	var tooMany, tooManySites []string
	for i := 1; i <= 101; i++ {
		tooMany = append(tooMany, strconv.Quote("m"+strconv.Itoa(i)))
	}
	for i := 1; i <= 21; i++ {
		tooManySites = append(tooManySites, strconv.Itoa(i))
	}
	for _, c := range []struct{ tool, connection, arguments string }{
		{"matomo.custom-reports.add", "cr-writer-two", `{"idSite":1,` + def + `}`},
		{"matomo.custom-reports.add", "cr-writer-both", `{` + def + `}`},
		{"matomo.custom-reports.add", "cr-writer-both", `{"idSite":1,` + def + `,"multipleIdSites":[1,3]}`},
		{"matomo.custom-reports.add", "cr-writer-both", `{"idSite":1,` + def + `,"multipleIdSites":[3]}`},
		{"matomo.custom-reports.add", "cr-writer-both", `{"idSite":1,` + def + `,"multipleIdSites":[]}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,` + def + `,"multipleIdSites":[2,2]}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,` + def + `,"multipleIdSites":[` + strings.Join(tooManySites, ",") + `]}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,"reportType":"table","metricIds":["a"]}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,"name":"","reportType":"table","metricIds":["a"]}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,"name":"` + strings.Repeat("n", 256) + `","reportType":"table","metricIds":["a"]}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,"name":"a","metricIds":["a"]}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,"name":"a","reportType":"ta ble","metricIds":["a"]}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,"name":"a","reportType":"table"}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,"name":"a","reportType":"table","metricIds":[]}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,"name":"a","reportType":"table","metricIds":["a","a"]}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,"name":"a","reportType":"table","metricIds":["a b"]}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,"name":"a","reportType":"table","metricIds":[` + strings.Join(tooMany, ",") + `]}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,` + def + `,"dimensionIds":[]}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,` + def + `,"categoryId":""}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,` + def + `,"subcategoryId":"x/y"}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,` + def + `,"description":"` + strings.Repeat("d", 2049) + `"}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,` + def + `,"segmentFilter":"` + strings.Repeat("s", 2049) + `"}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,` + def + `,"segmentFilter":"not a segment"}`},
		{"matomo.custom-reports.add", "cr-writer", `{"idSite":1,` + def + `,"callback":"x"}`},
		{"matomo.custom-reports.update", "cr-writer-two", `{"idSite":1,"idCustomReport":1,` + def + `}`},
		{"matomo.custom-reports.update", "cr-writer-both", `{"idSite":1,"idCustomReport":1,` + def + `,"multipleIdSites":[3]}`},
		{"matomo.custom-reports.update", "cr-writer", `{"idSite":1,"idCustomReport":0,` + def + `}`},
		{"matomo.custom-reports.update", "cr-writer", `{"idSite":1,"idCustomReport":2147483648,` + def + `}`},
		{"matomo.custom-reports.update", "cr-writer", `{"idSite":1,` + def + `}`},
		{"matomo.custom-reports.update", "cr-writer", `{"idSite":1,"idCustomReport":1,"reportType":"table","metricIds":["a"]}`},
		{"matomo.custom-reports.update", "cr-writer", `{"idSite":1,"idCustomReport":1,` + def + `,"subCategoryReportIds":[]}`},
		{"matomo.custom-reports.update", "cr-writer", `{"idSite":1,"idCustomReport":1,` + def + `,"subCategoryReportIds":[0]}`},
		{"matomo.custom-reports.update", "cr-writer", `{"idSite":1,"idCustomReport":1,` + def + `,"subCategoryReportIds":[2,2]}`},
		{"matomo.custom-reports.duplicate", "cr-writer-two", `{"idSite":1,"idCustomReport":1,"idDestinationSites":[2]}`},
		{"matomo.custom-reports.duplicate", "cr-writer-two", `{"idSite":1,"idCustomReport":1}`},
		{"matomo.custom-reports.duplicate", "cr-writer-one", `{"idSite":1,"idCustomReport":1,"idDestinationSites":[1,2]}`},
		{"matomo.custom-reports.duplicate", "cr-writer-one", `{"idSite":1,"idCustomReport":1,"idDestinationSites":[2]}`},
		{"matomo.custom-reports.duplicate", "cr-writer-both", `{"idSite":1,"idCustomReport":1,"idDestinationSites":[2,3]}`},
		{"matomo.custom-reports.duplicate", "cr-writer-both", `{"idSite":1,"idCustomReport":1,"idDestinationSites":[2,2]}`},
		{"matomo.custom-reports.duplicate", "cr-writer-both", `{"idSite":1,"idCustomReport":1,"idDestinationSites":[]}`},
		{"matomo.custom-reports.duplicate", "cr-writer-both", `{"idSite":1,"idCustomReport":1,"idDestinationSites":[0]}`},
		{"matomo.custom-reports.duplicate", "cr-writer", `{"idSite":1,"idCustomReport":1,"idDestinationSites":[` + strings.Join(tooManySites, ",") + `]}`},
		{"matomo.custom-reports.duplicate", "cr-writer", `{"idSite":1,"idDestinationSites":[2]}`},
		{"matomo.custom-reports.pause", "cr-writer-two", `{"idSite":1,"idCustomReport":1}`},
		{"matomo.custom-reports.pause", "cr-writer-both", `{"idCustomReport":1}`},
		{"matomo.custom-reports.pause", "cr-writer", `{"idSite":1}`},
		{"matomo.custom-reports.pause", "cr-writer", `{"idSite":1,"idCustomReport":0}`},
		{"matomo.custom-reports.pause", "cr-writer", `{"idSite":1,"idCustomReport":1,"resume":"yes"}`},
		{"matomo.custom-reports.delete", "cr-writer-two", `{"idSite":1,"idCustomReport":1}`},
		{"matomo.custom-reports.delete", "cr-writer-both", `{"idCustomReport":1}`},
		{"matomo.custom-reports.delete", "cr-writer", `{"idSite":1}`},
		{"matomo.custom-reports.delete", "cr-writer", `{"idSite":1,"idCustomReport":0}`},
		{"matomo.custom-reports.delete", "cr-writer", `{"idSite":1,"idCustomReport":2147483648}`},
		{"matomo.custom-reports.delete", "cr-writer", `{"idSite":1,"idCustomReport":1,"idSite2":3}`},
		{"matomo.custom-reports.pause", "cr-writer", `{"idSite":1,"idCustomReport":1,"method":"CustomReports.deleteCustomReport"}`},
	} {
		_, err := env.change(c.tool, crConn(c.tool, c.connection), c.arguments)
		if err == nil {
			t.Errorf("%s %s %s was accepted", c.tool, c.connection, c.arguments)
		} else if strings.Contains(err.Error(), "site/") || strings.Contains(err.Error(), "idSite=1") ||
			strings.Contains(c.arguments, `[2,3]`) && strings.Contains(err.Error(), "3") ||
			strings.Contains(c.arguments, `[1,3]`) && strings.Contains(err.Error(), "3") {
			t.Errorf("%s names a target: %v", c.tool, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestCustomReportChangeRefusesAReportOfAnotherWebsiteAfterOneReadWithoutMutation(t *testing.T) {
	for _, c := range customReportChanges {
		if c.reads == "" {
			continue
		}
		var calls []call
		env := newCustomReportChangeEnvironment(t, &calls, customReportChangeAnswers(nil).handler(t))
		// 50 belongs to website 3, 51 has no website, 52 spans website 3 outside the targets, 53 and 99 are unknown.
		for _, id := range []string{"50", "51", "52", "53", "99"} {
			calls = nil
			arguments := strings.Replace(c.arguments, `"idCustomReport":1`, `"idCustomReport":`+id, 1)
			_, err := env.change(c.tool, crConn(c.tool, "cr-writer-both"), arguments)
			if err == nil || classOf(err) != provider.ClassNotFound && classOf(err) != provider.ClassInvalidResponse ||
				countMethod(calls, c.method) != 0 || countMethod(calls, c.reads) != 1 {
				t.Errorf("%s report %s: requests = %s, err = %v", c.tool, id, methods(calls), err)
			}
			if err != nil && strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("%s shows the provider message", c.tool)
			}
		}
		// A report that spans website 2 is outside the targets of a connection that binds only website 1.
		calls = nil
		arguments := strings.Replace(strings.Replace(c.arguments, `"idCustomReport":1`, `"idCustomReport":8`, 1),
			`"idDestinationSites":[1,2]`, `"idDestinationSites":[1]`, 1)
		if _, err := env.change(c.tool, crConn(c.tool, "cr-writer-one"), arguments); err == nil || classOf(err) != provider.ClassNotFound ||
			countMethod(calls, c.method) != 0 {
			t.Errorf("%s spanning report: requests = %s, err = %v", c.tool, methods(calls), err)
		}
	}
}

func TestCustomReportChangeRequestsCarryTheBearerTokenOnlyInTheHeader(t *testing.T) {
	var calls []call
	env := newCustomReportChangeEnvironment(t, &calls, customReportChangeAnswers(nil).handler(t))
	for _, c := range customReportChanges {
		if _, err := env.change(c.tool, crConn(c.tool, "cr-writer-both"), c.arguments); err != nil {
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

func TestCustomReportChangeMapsMatomoErrorsAndNeverRepeats(t *testing.T) {
	for _, c := range customReportChanges {
		for name, tc := range map[string]struct {
			answer      func(call) (*http.Response, error)
			class       provider.Class
			unsupported bool
			unclear     bool
		}{
			"permission": {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false, false},
			"object not found": {func(call) (*http.Response, error) {
				return ok2(failure("An unexpected website was found in the request: website id was set to '1' ." + bodyCanary))
			}, provider.ClassNotFound, false, false},
			"report not found": {func(call) (*http.Response, error) { return ok2(failure(reportMissing + bodyCanary)) }, provider.ClassNotFound, false, false},
			"method missing": {func(call) (*http.Response, error) {
				return ok2(failure("The method '" + c.method + "' does not exist or is not available in the module 'CustomReports'." + bodyCanary))
			}, "", true, false},
			"plugin inactive": {func(call) (*http.Response, error) {
				return ok2(failure("The plugin CustomReports is not activated." + bodyCanary))
			}, "", true, false},
			"429":        {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, bodyCanary), nil }, provider.ClassRateLimited, false, false},
			"timeout":    {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false, true},
			"server":     {func(call) (*http.Response, error) { return respond(500, bodyCanary), nil }, "", false, true},
			"unreadable": {func(call) (*http.Response, error) { return ok2(ok("<html>" + bodyCanary + "</html>")) }, provider.ClassInvalidResponse, false, true},
			"no confirmation": {func(call) (*http.Response, error) {
				if c.tool == "matomo.custom-reports.duplicate" {
					return ok2(ok(`[]`))
				}
				return ok2(ok(`{"value":"none"}`))
			}, provider.ClassInvalidResponse, false, true},
			"reported failure": {func(call) (*http.Response, error) {
				return ok2(ok(`{"success":false,"message":"` + bodyCanary + `"}`))
			}, provider.ClassInvalidResponse, false, true},
		} {
			if name == "reported failure" && c.tool != "matomo.custom-reports.duplicate" {
				continue
			}
			var calls []call
			env := newCustomReportChangeEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if in.api() == c.method {
					return tc.answer(in)
				}
				return customReportChangeAnswers(nil).handler(t)(in)
			})
			clock := time.Now()
			t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, func() time.Time { return clock },
				func(context.Context, time.Duration) error { return nil })))
			_, err := env.change(c.tool, crConn(c.tool, "cr-writer-both"), c.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || isUnsupported(err) != tc.unsupported ||
				countMethod(calls, c.method) != 1 || strings.Contains(err.Error(), "never repeated") != tc.unclear ||
				tc.class != "" && classOf(err) != tc.class {
				t.Errorf("%s %s: err = %v (class %q), %s sent %d times", c.tool, name, err, classOf(err), c.method, countMethod(calls, c.method))
			}
		}
	}
}

func TestCustomReportAddAcceptsABareIDAndDuplicateABareID(t *testing.T) {
	var calls []call
	env := newCustomReportChangeEnvironment(t, &calls, customReportChangeAnswers(answers{
		"CustomReports.addCustomReport":       fixed(`21`),
		"CustomReports.duplicateCustomReport": fixed(`22`),
	}).handler(t))
	result, err := env.change("matomo.custom-reports.add", "cr-writer", customReportChanges[0].arguments)
	if err != nil || result != `{"created":true,"idCustomReport":21,"idSite":1}` {
		t.Errorf("add: result = %s, err = %v", result, err)
	}
	result, err = env.change("matomo.custom-reports.duplicate", "cr-writer", customReportChanges[2].arguments)
	var out CustomReportDuplicated
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.Answer != float64(22) || !out.Duplicated {
		t.Errorf("duplicate: result = %s, err = %v", result, err)
	}
}

func TestCustomReportChangeToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range customReportChanges {
		var calls []call
		env := newCustomReportChangeEnvironment(t, &calls, with(customReportChangeAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.change(c.tool, crConn(c.tool, "cr-writer-both"), c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}

func TestCustomReportDeleteIsReachableOnlyThroughTheToolsList(t *testing.T) {
	const tool = "matomo.custom-reports.delete"
	var calls []call
	env := newCustomReportChangeEnvironment(t, &calls, customReportChangeAnswers(nil).handler(t))
	if _, err := env.change(tool, "cr-effects-delete", `{"idSite":1,"idCustomReport":1}`); err == nil || len(calls) != 0 || *env.reads != 0 {
		t.Errorf("through effects only: err = %v, requests = %d", err, len(calls))
	}
	if _, err := env.change(tool, "cr-deleter", `{"idSite":1,"idCustomReport":1}`); err != nil {
		t.Errorf("through the tools list: %v", err)
	}
	if countMethod(calls, "CustomReports.deleteCustomReport") != 1 || countMethod(calls, "CustomReports.getConfiguredReport") != 1 {
		t.Errorf("requests = %s", methods(calls))
	}
}
