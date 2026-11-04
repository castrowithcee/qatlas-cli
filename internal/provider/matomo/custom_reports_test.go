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

// The report fixtures are answers of the public demo (getConfiguredReports, getConfiguredReport,
// getAvailableCategories). Assumptions, as no verified answer exists: the members of getCustomMetrics and of
// getWebsiteMetricsForCustomMetric (an idsite per row), the answers of getAvailableReportTypes,
// getAvailableDimensions, getAvailableMetrics and getAvailableMetricsForCustomMetric, and the shapes of
// multiple_idsites that the demo only shows as null.
const demoReport = `{"idcustomreport":1,"idsite":1,"revision":0,"report_type":"table","name":"Pages by New\/Returning visitor",` +
	`"description":"","category":{"id":"CustomReports_CustomReports","name":"Custom Reports","order":65,"icon":"icon-business"},` +
	`"subcategory":null,"subcategory_order":9999999,"dimensions":["CoreHome.VisitorReturning","Actions.PageTitle"],` +
	`"metrics":["nb_uniq_visitors","nb_visits","pageviews"],"segment_filter":"","created_date":"2017-10-20 02:31:50",` +
	`"created_by":"alice","updated_date":"2017-10-20 02:31:50","updated_by":null,"status":"active","multiple_idsites":null,` +
	`"site":{"id":1,"name":"Demo Site"},"created_date_pretty":"Oct 20, 2017","updated_date_pretty":"Oct 20, 2017",` +
	`"allowedToEdit":false}`

const demoEvolutionReport = `{"idcustomreport":8,"idsite":1,"revision":2,"report_type":"evolution","name":"Evolution",` +
	`"category":"CustomReports_CustomReports","subcategory":"Goals_Goals","dimensions":[],"metrics":["nb_visits"],` +
	`"segment_filter":"deviceType==smartphone","status":"paused","multiple_idsites":"1,2","site":{"id":1,"name":"Demo Site"},"allowedToEdit":true}`

const foreignReport = `{"idcustomreport":50,"idsite":3,"name":"Foreign"}`

const reportMissing = "The requested custom report does not exist for this website. Please ensure the site is correct."

func customReportAnswers(extra answers) answers {
	return with(pluginsOn(), with(answers{
		"CustomReports.getConfiguredReports": func(c call) *http.Response {
			rows := demoReport + "," + demoEvolutionReport + "," + foreignReport + `,{"idcustomreport":51,"name":"No site"},` +
				`{"idcustomreport":52,"idsite":1,"name":"Spans three","multipleIdSites":[1,2,3]},` +
				`{"idcustomreport":53,"idsite":1,"name":"All sites","multiple_idsites":"all"},` +
				`{"idcustomreport":54,"idsite":1,"name":"Spans two","multipleIdSites":["1","2"]}`
			if c.form.Get("skipCategoryMetadata") == "1" {
				rows = strings.Replace(rows, `"category":{"id":"CustomReports_CustomReports","name":"Custom Reports","order":65,"icon":"icon-business"}`,
					`"category":"CustomReports_CustomReports"`, 1)
			}
			return ok("[" + rows + "]")
		},
		"CustomReports.getConfiguredReport": func(c call) *http.Response {
			switch c.form.Get("idCustomReport") {
			case "1":
				return ok(strings.Replace(demoReport, `"allowedToEdit":false`, `"child_reports":[],"multipleIdSites":[],"allowedToEdit":false`, 1))
			case "8":
				return ok(demoEvolutionReport)
			case "50":
				return ok(foreignReport)
			case "51":
				return ok(`{"idcustomreport":51,"name":"No site"}`)
			case "52":
				return ok(`{"idcustomreport":52,"idsite":1,"multipleIdSites":[1,2,3]}`)
			case "53":
				return ok(`false`)
			case "54":
				return ok(demoReport)
			}
			return failure(reportMissing)
		},
		"CustomReports.getAvailableCategories": fixed(`[{"uniqueId":"General_AIAssistants","name":"AI Assistants","subcategories":` +
			`[{"uniqueId":"AIAgents_AIAgentsOverview","name":"AI Agents Overview"}]},{"uniqueId":"General_Actions","name":"Behaviour",` +
			`"subcategories":[{"uniqueId":"customdimension2","name":"Page Author"},{"uniqueId":"General_Pages","name":"Pages"}]}]`),
		"CustomReports.getAvailableReportTypes":            fixed(`{"table":"Table","evolution":"Evolution"}`),
		"CustomReports.getAvailableDimensions":             fixed(`[{"category":"Visitor","dimensions":[{"uniqueId":"CoreHome.VisitorReturning","name":"Returning"}]}]`),
		"CustomReports.getAvailableMetrics":                fixed(`[{"category":"General","metrics":[{"uniqueId":"nb_visits","name":"Visits"}]}]`),
		"CustomReports.getAvailableMetricsForCustomMetric": fixed(`[{"category":"General","metrics":[{"uniqueId":"nb_visits","name":"Visits"}]}]`),
		"CustomReports.getCustomMetrics": fixed(`[{"idcustommetric":1,"name":"Pages per visit","formula":"pageviews \/ nb_visits",` +
			`"formatType":"number","decimals":2,"description":"Ratio"},{"idcustommetric":2,"name":"Second","formula":"nb_visits"}]`),
		"CustomReports.getWebsiteMetricsForCustomMetric": func(c call) *http.Response {
			rows := []string{`{"idsite":1,"name":"One"}`, `{"idsite":2,"name":"Two"}`, `{"idsite":3,"name":"Three"}`,
				`{"idsite":4,"name":"Four"}`, `{"idsite":5,"name":"Five"}`}
			var out []string
			for _, r := range rows {
				var id int
				for _, ch := range r[len(`{"idsite":`):] {
					if ch < '0' || ch > '9' {
						break
					}
					id = id*10 + int(ch-'0')
				}
				after := 0
				for _, ch := range c.form.Get("afterIdSite") {
					after = after*10 + int(ch-'0')
				}
				if id > after {
					out = append(out, r)
				}
			}
			limit := 0
			for _, ch := range c.form.Get("websiteLimit") {
				limit = limit*10 + int(ch-'0')
			}
			if len(out) > limit {
				out = out[:limit]
			}
			return ok("[" + strings.Join(out, ",") + "]")
		},
	}, extra))
}

var customReportCalls = []struct{ tool, connection, arguments string }{
	{"matomo.custom-reports.list", "two", `{"idSite":1}`},
	{"matomo.custom-reports.get", "two", `{"idSite":1,"idCustomReport":1}`},
	{"matomo.custom-metrics.list", "mixed", `{"kind":"metrics"}`},
	{"matomo.custom-report-options.list", "mixed", `{"idSite":2}`},
}

func TestCustomReportToolsAreRegisteredAsOpenReadsInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, c := range customReportCalls {
		d, _, found := reg.Lookup(c.tool)
		if !found || d.Risk != readRisk(customReportsSensitive) || d.Risk.Idempotency != capability.IdempotencySafe ||
			d.Risk.Confirmation != capability.ConfirmationNone || !d.Risk.OpenWorld || d.RequiresToolAllowList {
			t.Errorf("%s: found %t, %+v", c.tool, found, d)
		}
		for _, banned := range []string{"callback", "token", "module", "method", "format", "filter_limit", "afterIdSite", "websiteLimit"} {
			if strings.Contains(string(d.InputSchema), `"`+banned+`"`) {
				t.Errorf("%s schema offers %s", c.tool, banned)
			}
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		if strings.Contains(strings.Join(profile.Tools, " "), "custom-report") || strings.Contains(strings.Join(profile.Tools, " "), "custom-metric") {
			t.Errorf("profile %s selects a custom report tool", profile.ID)
		}
	}
	for _, other := range []string{"matomo.custom-reports.preview", "matomo.custom-reports.report"} {
		if _, _, found := reg.Lookup(other); found {
			t.Errorf("%s is offered", other)
		}
	}
}

func TestCustomReportsListKeepsOnlyReportsInsideTheTargetsAndPages(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, customReportAnswers(nil).handler(t))
	result, err := env.invoke("matomo.custom-reports.list", "two", `{"idSite":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated CustomReports.getConfiguredReports" {
		t.Errorf("requests = %s", methods(calls))
	}
	if sent := lastCall(calls, "CustomReports.getConfiguredReports").form; sent.Get("idSite") != "1" || sent.Has("skipCategoryMetadata") {
		t.Errorf("form = %v", sent)
	}
	// Foreign rows, a row without a website, a report spanning a website outside the targets, and an
	// unreadable span fall away; the creator logins are never copied.
	for _, hidden := range []string{"Foreign", "No site", "Spans three", "All sites", "alice", "pretty", "created_by"} {
		if strings.Contains(result, hidden) {
			t.Errorf("result shows %q: %s", hidden, result)
		}
	}
	var out CustomReportsResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	if out.IDSite != 1 || len(out.Reports) != 3 || out.HasMore {
		t.Fatalf("result = %s", result)
	}
	demo, evolution := out.Reports[0], out.Reports[1]
	if demo.ID != 1 || demo.IDSite != 1 || demo.Revision == nil || *demo.Revision != 0 || demo.ReportType != "table" ||
		demo.Name != "Pages by New/Returning visitor" || demo.Category == nil || demo.Category.ID != "CustomReports_CustomReports" ||
		demo.Category.Name != "Custom Reports" || demo.Category.Order == nil || *demo.Category.Order != 65 ||
		demo.Category.Icon != "icon-business" || demo.Subcategory != "" || demo.SubcategoryOrder == nil ||
		*demo.SubcategoryOrder != 9999999 || len(demo.Dimensions) != 2 || demo.Dimensions[1] != "Actions.PageTitle" ||
		len(demo.Metrics) != 3 || demo.CreatedDate != "2017-10-20 02:31:50" || demo.Status != "active" ||
		demo.Site == nil || *demo.Site != (CustomReportSite{1, "Demo Site"}) || demo.AllowedToEdit == nil || *demo.AllowedToEdit {
		t.Errorf("demo = %+v", demo)
	}
	if evolution.ID != 8 || evolution.Category == nil || evolution.Category.ID != "CustomReports_CustomReports" ||
		evolution.Subcategory != "Goals_Goals" || evolution.SegmentFilter != "deviceType==smartphone" ||
		evolution.Status != "paused" || len(evolution.MultipleIDSites) != 2 || evolution.AllowedToEdit == nil || !*evolution.AllowedToEdit {
		t.Errorf("evolution = %+v", evolution)
	}
	if out.Reports[2].ID != 54 || len(out.Reports[2].MultipleIDSites) != 2 {
		t.Errorf("spanning report = %+v", out.Reports[2])
	}

	// A connection that binds only website 2 shows none of the reports of website 1, also not the one that spans both.
	result, err = env.invoke("matomo.custom-reports.list", "mixed", `{"idSite":2}`)
	if err != nil || result != `{"has_more":false,"idSite":2}` {
		t.Errorf("result = %s, err = %v", result, err)
	}

	page, err := env.invoke("matomo.custom-reports.list", "two", `{"idSite":1,"limit":2}`)
	var first CustomReportsResult
	if err != nil || json.Unmarshal([]byte(page), &first) != nil || len(first.Reports) != 2 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page = %s, err = %v", page, err)
	}
	page, err = env.invoke("matomo.custom-reports.list", "two", `{"idSite":1,"cursor":"`+first.NextCursor+`"}`)
	var second CustomReportsResult
	if err != nil || json.Unmarshal([]byte(page), &second) != nil || len(second.Reports) != 1 || second.HasMore || second.Reports[0].ID != 54 {
		t.Fatalf("second page = %s, err = %v", page, err)
	}
	for _, other := range []string{`{"idSite":2,"cursor":"%s"}`, `{"idSite":1,"skipCategoryMetadata":true,"cursor":"%s"}`, `{"idSite":1,"limit":1,"cursor":"%s"}`} {
		if _, err := env.invoke("matomo.custom-reports.list", "two", strings.Replace(other, "%s", first.NextCursor, 1)); err == nil {
			t.Errorf("a cursor of another list was accepted: %s", other)
		}
	}
}

func TestCustomReportsListSkipCategoryMetadataKeepsTheCategoryID(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, customReportAnswers(nil).handler(t))
	result, err := env.invoke("matomo.custom-reports.list", "two", `{"idSite":1,"skipCategoryMetadata":true}`)
	var out CustomReportsResult
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || len(out.Reports) == 0 ||
		out.Reports[0].Category == nil || *out.Reports[0].Category != (CustomReportCategory{ID: "CustomReports_CustomReports"}) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if lastCall(calls, "CustomReports.getConfiguredReports").form.Get("skipCategoryMetadata") != "1" {
		t.Error("skipCategoryMetadata was not sent")
	}
}

func TestCustomReportsListWithoutReportsIsEmpty(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, customReportAnswers(answers{"CustomReports.getConfiguredReports": fixed(`[]`)}).handler(t))
	result, err := env.invoke("matomo.custom-reports.list", "one", `{}`)
	if err != nil || result != `{"has_more":false,"idSite":2}` {
		t.Errorf("result = %s, err = %v", result, err)
	}
}

func TestCustomReportsGetProvesTheWebsite(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, customReportAnswers(nil).handler(t))
	result, err := env.invoke("matomo.custom-reports.get", "two", `{"idSite":1,"idCustomReport":1}`)
	var report CustomReport
	if err != nil || json.Unmarshal([]byte(result), &report) != nil || report.ID != 1 || report.IDSite != 1 ||
		report.Name != "Pages by New/Returning visitor" || report.ChildReports != nil ||
		methods(calls) != "API.getMatomoVersion API.isPluginActivated CustomReports.getConfiguredReport" {
		t.Fatalf("result = %s, err = %v, requests = %s", result, err, methods(calls))
	}
	if sent := lastCall(calls, "CustomReports.getConfiguredReport").form; sent.Get("idSite") != "1" || sent.Get("idCustomReport") != "1" {
		t.Errorf("form = %v", sent)
	}
}

func TestCustomReportsGetRefusesWhatItCannotProve(t *testing.T) {
	for name, tc := range map[string]struct {
		arguments string
		class     provider.Class
	}{
		"report of another website":     {`{"idSite":1,"idCustomReport":50}`, provider.ClassNotFound},
		"answer without website":        {`{"idSite":1,"idCustomReport":51}`, provider.ClassNotFound},
		"spans a website outside":       {`{"idSite":1,"idCustomReport":52}`, provider.ClassNotFound},
		"answer false":                  {`{"idSite":1,"idCustomReport":53}`, provider.ClassNotFound},
		"no report":                     {`{"idSite":1,"idCustomReport":3}`, provider.ClassNotFound},
		"another report than asked for": {`{"idSite":1,"idCustomReport":54}`, provider.ClassInvalidResponse},
	} {
		var calls []call
		env := newEnvironment(t, &calls, customReportAnswers(nil).handler(t))
		_, err := env.invoke("matomo.custom-reports.get", "two", tc.arguments)
		if classOf(err) != tc.class || strings.Contains(err.Error(), "50") || strings.Contains(err.Error(), "Foreign") ||
			strings.Contains(err.Error(), reportMissing) {
			t.Errorf("%s: err = %v", name, err)
		}
		if countMethod(calls, "CustomReports.getConfiguredReport") != 1 {
			t.Errorf("%s: requests = %s", name, methods(calls))
		}
	}
}

func TestCustomMetricsListReadsTheMetricsWithOneFixedMethod(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, customReportAnswers(nil).handler(t))
	result, err := env.invoke("matomo.custom-metrics.list", "mixed", `{"kind":"metrics","limit":1}`)
	var first CustomMetricsResult
	if err != nil || json.Unmarshal([]byte(result), &first) != nil || first.Kind != "metrics" || len(first.Metrics) != 1 ||
		!first.HasMore || first.NextCursor == "" || len(first.Websites) != 0 {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated CustomReports.getCustomMetrics" {
		t.Errorf("requests = %s", methods(calls))
	}
	if row, _ := first.Metrics[0].(map[string]any); row["name"] != "Pages per visit" || row["formula"] != "pageviews / nb_visits" {
		t.Errorf("metric = %v", first.Metrics[0])
	}
	result, err = env.invoke("matomo.custom-metrics.list", "mixed", `{"kind":"metrics","cursor":"`+first.NextCursor+`"}`)
	var second CustomMetricsResult
	if err != nil || json.Unmarshal([]byte(result), &second) != nil || len(second.Metrics) != 1 || second.HasMore {
		t.Fatalf("second page = %s, err = %v", result, err)
	}
	if _, err := env.invoke("matomo.custom-metrics.list", "mixed", `{"kind":"websites","cursor":"`+first.NextCursor+`"}`); err == nil {
		t.Error("a cursor of the metrics was accepted for the websites")
	}
}

func TestCustomMetricsListPagesWebsitesByCursorAndFiltersForeignRows(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, customReportAnswers(nil).handler(t))
	// "mixed" binds website 2 and the instance. Rows 1, 3, 4, and 5 belong to other websites.
	result, err := env.invoke("matomo.custom-metrics.list", "mixed", `{"kind":"websites","limit":1}`)
	var first CustomMetricsResult
	if err != nil || json.Unmarshal([]byte(result), &first) != nil || len(first.Websites) != 0 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first batch = %s, err = %v", result, err)
	}
	sent := lastCall(calls, "CustomReports.getWebsiteMetricsForCustomMetric").form
	if sent.Get("afterIdSite") != "0" || sent.Get("websiteLimit") != "2" {
		t.Errorf("form = %v", sent)
	}
	result, err = env.invoke("matomo.custom-metrics.list", "mixed", `{"kind":"websites","cursor":"`+first.NextCursor+`"}`)
	var second CustomMetricsResult
	if err != nil || json.Unmarshal([]byte(result), &second) != nil || len(second.Websites) != 1 || second.Websites[0].IDSite != 2 ||
		!second.HasMore {
		t.Fatalf("second batch = %s, err = %v", result, err)
	}
	if sent := lastCall(calls, "CustomReports.getWebsiteMetricsForCustomMetric").form; sent.Get("afterIdSite") != "1" || sent.Get("websiteLimit") != "2" {
		t.Errorf("form = %v", sent)
	}
	result, err = env.invoke("matomo.custom-metrics.list", "mixed", `{"kind":"websites","limit":10,"cursor":"`+second.NextCursor+`"}`)
	if err == nil {
		t.Errorf("a changed limit was accepted: %s", result)
	}
	result, err = env.invoke("matomo.custom-metrics.list", "mixed", `{"kind":"websites","limit":99}`)
	var all CustomMetricsResult
	if err != nil || json.Unmarshal([]byte(result), &all) != nil || len(all.Websites) != 1 || all.HasMore {
		t.Fatalf("all = %s, err = %v", result, err)
	}
	for _, foreign := range []string{`"Three"`, `"One"`, `"Five"`} {
		if strings.Contains(result, foreign) {
			t.Errorf("result names a website outside the targets: %s", result)
		}
	}
}

func TestCustomMetricsListRefusesAnOutOfOrderWebsiteRow(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, customReportAnswers(answers{
		"CustomReports.getWebsiteMetricsForCustomMetric": fixed(`[{"idsite":2},{"idsite":2}]`)}).handler(t))
	if _, err := env.invoke("matomo.custom-metrics.list", "mixed", `{"kind":"websites"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
}

func TestCustomReportOptionsListReadsEveryOptionOfTheWebsite(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, customReportAnswers(nil).handler(t))
	result, err := env.invoke("matomo.custom-report-options.list", "mixed", `{"idSite":2}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated CustomReports.getAvailableCategories "+
		"CustomReports.getAvailableReportTypes CustomReports.getAvailableDimensions CustomReports.getAvailableMetrics "+
		"CustomReports.getAvailableMetricsForCustomMetric" {
		t.Errorf("requests = %s", methods(calls))
	}
	if lastCall(calls, "CustomReports.getAvailableReportTypes").form.Has("idSite") {
		t.Error("getAvailableReportTypes sends idSite")
	}
	for _, method := range []string{"getAvailableCategories", "getAvailableDimensions", "getAvailableMetrics",
		"getAvailableMetricsForCustomMetric"} {
		if lastCall(calls, "CustomReports."+method).form.Get("idSite") != "2" {
			t.Errorf("%s sends no idSite", method)
		}
	}
	var out CustomReportOptionsResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	types, _ := out.ReportTypes.(map[string]any)
	if out.IDSite != 2 || len(out.Categories) != 2 || out.Categories[1].UniqueID != "General_Actions" ||
		len(out.Categories[1].Subcategories) != 2 || out.Categories[1].Subcategories[1] != (CustomReportSubcategory{"General_Pages", "Pages"}) ||
		types["evolution"] != "Evolution" || out.Dimensions == nil || out.Metrics == nil || out.CustomMetricMetrics == nil ||
		len(out.Unavailable) != 0 {
		t.Errorf("result = %s", result)
	}
}

func TestCustomReportOptionsListSkipsTheSuperUserOptionWithoutTheInstanceTarget(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, customReportAnswers(nil).handler(t))
	result, err := env.invoke("matomo.custom-report-options.list", "two", `{"idSite":1}`)
	var out CustomReportOptionsResult
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.CustomMetricMetrics != nil ||
		len(out.Unavailable) != 1 || out.Unavailable[0] != (CustomReportOptionGap{"custom_metric_metrics", "instance-target"}) {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if countMethod(calls, "CustomReports.getAvailableMetricsForCustomMetric") != 0 {
		t.Error("the Super User method was requested without the instance target")
	}
}

func TestCustomReportOptionsListNamesWhatTheTokenMayNotRead(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, customReportAnswers(answers{
		"CustomReports.getAvailableReportTypes":            fixed(`{"result":"error","message":"` + permissionMsg + bodyCanary + `"}`),
		"CustomReports.getAvailableMetricsForCustomMetric": fixed(`{"result":"error","message":"You can't access this resource as it requires a 'superuser' access."}`),
	}).handler(t))
	result, err := env.invoke("matomo.custom-report-options.list", "mixed", `{"idSite":2}`)
	var out CustomReportOptionsResult
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || len(out.Unavailable) != 2 ||
		out.Unavailable[0] != (CustomReportOptionGap{"report_types", "permission"}) ||
		out.Unavailable[1] != (CustomReportOptionGap{"custom_metric_metrics", "permission"}) ||
		out.ReportTypes != nil || out.Categories == nil || strings.Contains(result, bodyCanary) {
		t.Errorf("result = %s, err = %v", result, err)
	}

	// Every option the connection may ask for is refused: the call fails, also when the Super User option was
	// never asked for.
	all := answers{}
	for _, m := range []string{"getAvailableCategories", "getAvailableReportTypes", "getAvailableDimensions", "getAvailableMetrics"} {
		all["CustomReports."+m] = func(call) *http.Response { return failure(permissionMsg + bodyCanary) }
	}
	env = newEnvironment(t, &calls, customReportAnswers(all).handler(t))
	if _, err := env.invoke("matomo.custom-report-options.list", "two", `{"idSite":1}`); classOf(err) != provider.ClassPermission ||
		err == nil || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("err = %v", err)
	}

	env = newEnvironment(t, &calls, customReportAnswers(answers{"CustomReports.getAvailableCategories": fixed(`"text"`)}).handler(t))
	if _, err := env.invoke("matomo.custom-report-options.list", "mixed", `{"idSite":2}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
}

func TestCustomReportToolsRefuseBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, customReportAnswers(nil).handler(t))
	for _, c := range []struct{ tool, connection, arguments string }{
		{"matomo.custom-reports.list", "two", `{"idSite":3}`},
		{"matomo.custom-reports.list", "two", `{}`},
		{"matomo.custom-reports.list", "instance", `{}`},
		{"matomo.custom-reports.list", "two", `{"idSite":1,"callback":"x"}`},
		{"matomo.custom-reports.list", "two", `{"idSite":1,"limit":0}`},
		{"matomo.custom-reports.list", "two", `{"idSite":1,"skipCategoryMetadata":"yes"}`},
		{"matomo.custom-reports.get", "two", `{"idSite":3,"idCustomReport":1}`},
		{"matomo.custom-reports.get", "two", `{"idCustomReport":1}`},
		{"matomo.custom-reports.get", "one", `{"idSite":1,"idCustomReport":1}`},
		{"matomo.custom-reports.get", "two", `{"idSite":1}`},
		{"matomo.custom-reports.get", "two", `{"idSite":1,"idCustomReport":0}`},
		{"matomo.custom-reports.get", "two", `{"idSite":1,"idCustomReport":"1"}`},
		{"matomo.custom-reports.get", "instance", `{"idSite":1,"idCustomReport":1}`},
		{"matomo.custom-report-options.list", "two", `{}`},
		{"matomo.custom-report-options.list", "two", `{"idSite":3}`},
		{"matomo.custom-report-options.list", "instance", `{"idSite":1}`},
		{"matomo.custom-report-options.list", "two", `{"idSite":1,"method":"x"}`},
		{"matomo.custom-metrics.list", "two", `{"kind":"metrics"}`},
		{"matomo.custom-metrics.list", "open", `{"kind":"websites"}`},
		{"matomo.custom-metrics.list", "general", `{"kind":"metrics"}`},
		{"matomo.custom-metrics.list", "instance", `{"kind":"websites"}`},
		{"matomo.custom-metrics.list", "mixed", `{}`},
		{"matomo.custom-metrics.list", "mixed", `{"kind":"report"}`},
		{"matomo.custom-metrics.list", "mixed", `{"kind":"metrics","limit":0}`},
		{"matomo.custom-metrics.list", "mixed", `{"kind":"websites","limit":100}`},
		{"matomo.custom-metrics.list", "mixed", `{"kind":"websites","cursor":"abc"}`},
		{"matomo.custom-metrics.list", "mixed", `{"kind":"websites","afterIdSite":3}`},
		{"matomo.custom-metrics.list", "mixed", `{"kind":"websites","websiteLimit":3}`},
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

func TestCustomReportRequestsCarryTheBearerTokenOnlyInTheHeader(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, customReportAnswers(nil).handler(t))
	all := append([]struct{ tool, connection, arguments string }{}, customReportCalls...)
	all = append(all, struct{ tool, connection, arguments string }{"matomo.custom-metrics.list", "mixed", `{"kind":"websites"}`})
	for _, c := range all {
		if _, err := env.invoke(c.tool, c.connection, c.arguments); err != nil {
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

func TestCustomReportToolsMapMatomoErrorsAndTransportFailures(t *testing.T) {
	calls0 := append(append([]struct{ tool, connection, arguments string }{}, customReportCalls...),
		struct{ tool, connection, arguments string }{"matomo.custom-metrics.list", "mixed", `{"kind":"websites"}`})
	firstOf := map[string]string{"matomo.custom-reports.list": "CustomReports.getConfiguredReports",
		"matomo.custom-reports.get": "CustomReports.getConfiguredReport", "matomo.custom-metrics.list": "CustomReports.getCustomMetrics",
		"matomo.custom-report-options.list": "CustomReports.getAvailableCategories"}
	for _, c := range calls0 {
		method := firstOf[c.tool]
		websites := c.tool == "matomo.custom-metrics.list" && strings.Contains(c.arguments, "websites")
		if websites {
			method = "CustomReports.getWebsiteMetricsForCustomMetric"
		}
		hits := func(in call) bool {
			return in.api() == method || c.tool == "matomo.custom-report-options.list" && strings.HasPrefix(in.api(), "CustomReports.")
		}
		for name, tc := range map[string]struct {
			answer      func(call) (*http.Response, error)
			class       provider.Class
			unsupported bool
		}{
			"permission": {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false},
			"object not found": {func(call) (*http.Response, error) {
				return ok2(failure(reportMissing + bodyCanary))
			}, provider.ClassNotFound, false},
			"method missing": {func(call) (*http.Response, error) {
				return ok2(failure("The method 'CustomReports.x' does not exist or is not available in the module 'CustomReports'." + bodyCanary))
			}, "", true},
			"module missing": {func(call) (*http.Response, error) {
				return ok2(failure("The plugin CustomReports is not activated." + bodyCanary))
			}, "", true},
			"429":        {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, bodyCanary), nil }, provider.ClassRateLimited, false},
			"timeout":    {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false},
			"unreadable": {func(call) (*http.Response, error) { return ok2(ok("<html>" + bodyCanary + "</html>")) }, provider.ClassInvalidResponse, false},
			"unknown":    {func(call) (*http.Response, error) { return ok2(failure("Something " + bodyCanary)) }, provider.ClassInvalidResponse, false},
		} {
			if c.tool == "matomo.custom-report-options.list" && name == "object not found" ||
				c.tool != "matomo.custom-reports.get" && name == "object not found" {
				continue
			}
			var calls []call
			env := newEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if hits(in) {
					return tc.answer(in)
				}
				return customReportAnswers(nil).handler(t)(in)
			})
			clock := time.Now()
			t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, func() time.Time { return clock },
				func(context.Context, time.Duration) error { return nil })))
			_, err := env.invoke(c.tool, c.connection, c.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), reportMissing) ||
				isUnsupported(err) != tc.unsupported || classOf(err) != tc.class || countMethod(calls, method) != 1 {
				t.Errorf("%s %s: err = %v (class %q)", c.tool, name, err, classOf(err))
			}
		}
	}
}

func TestCustomReportToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	calls0 := append(append([]struct{ tool, connection, arguments string }{}, customReportCalls...),
		struct{ tool, connection, arguments string }{"matomo.custom-metrics.list", "mixed", `{"kind":"websites"}`})
	for _, c := range calls0 {
		var calls []call
		env := newEnvironment(t, &calls, with(customReportAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.invoke(c.tool, c.connection, c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" ||
			lastCall(calls, "API.isPluginActivated").form.Get("pluginName") != customReportsPlugin {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}

func TestCustomReportTextAndListsAreShortened(t *testing.T) {
	long := strings.Repeat("n", 6000)
	ids := strings.TrimSuffix(strings.Repeat(`"`+long+`",`, 150), ",")
	body := `{"idcustomreport":2,"idsite":1,"name":"` + long + `","description":"` + long + `","segment_filter":"` + long +
		`","dimensions":[` + ids + `],"metrics":[` + ids + `],"category":{"id":"` + long + `"},"subcategory":"` + long + `"}`
	var calls []call
	env := newEnvironment(t, &calls, customReportAnswers(answers{
		"CustomReports.getConfiguredReport": fixed(body),
		"CustomReports.getCustomMetrics":    fixed(`[{"name":"` + long + `"}]`),
	}).handler(t))
	result, err := env.invoke("matomo.custom-reports.get", "two", `{"idSite":1,"idCustomReport":2}`)
	var report CustomReport
	if err != nil || json.Unmarshal([]byte(result), &report) != nil || len(report.Name) != maxStringLength ||
		len(report.Description) != maxUrlLength || len(report.SegmentFilter) != maxUrlLength ||
		len(report.Dimensions) != maxResultEntries || len(report.Metrics) != maxResultEntries ||
		len(report.Dimensions[0]) != maxStringLength || len(report.Category.ID) != maxStringLength ||
		len(report.Subcategory) != maxStringLength {
		t.Errorf("result length %d, err = %v", len(result), err)
	}
	result, err = env.invoke("matomo.custom-metrics.list", "mixed", `{"kind":"metrics"}`)
	var metrics CustomMetricsResult
	if err != nil || json.Unmarshal([]byte(result), &metrics) != nil || len(metrics.Metrics) != 1 {
		t.Fatalf("result length %d, err = %v", len(result), err)
	}
	if row, _ := metrics.Metrics[0].(map[string]any); len(row["name"].(string)) != maxUrlLength {
		t.Errorf("name length %d", len(row["name"].(string)))
	}
}
