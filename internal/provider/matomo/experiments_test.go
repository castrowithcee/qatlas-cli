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

// The fixtures follow the getExperiment example of the AbTesting reference, in the JSON form a read of the
// public demo returned (targets, metrics, and variations as lists, booleans as 0 and 1). Assumptions, as the
// reference gives no example: the answers of the three list methods (taken as lists of the getExperiment
// answer), the answers of the three option methods, the answers of the two template methods (a string, also as
// {"value":...}), the array form statuses[] of a request, and an experiment without a website.
const sidebarExperiment = `{"idexperiment":"1","idsite":"1","confidence_threshold":"95.0","mde_relative":"15",` +
	`"name":"AdSidebar","description":"Displaying the ad either in the top, or sidebar","hypothesis":"If I display only one ad",` +
	`"included_targets":[{"attribute":"url","inverted":"0","type":"any","value":""}],"excluded_targets":[],` +
	`"success_metrics":[{"metric":"nb_conversions"},{"metric":"nb_pageviews"}],"percentage_participants":"100",` +
	`"original_redirect_url":null,"status":"archived","start_date":"2016-09-03 23:27:26","modified_date":"2016-10-19 00:57:26",` +
	`"end_date":"2016-10-19 00:53:06","forward_utm_params":0,"forward_all_query_params":1,` +
	`"variations":[{"idvariation":3,"name":"NoSidebarAd","percentage":null,"redirect_url":null},` +
	`{"idvariation":4,"name":"NoHeaderAd","percentage":50,"redirect_url":"https://example.org/b"}],` +
	`"start_date_site_timezone":"2016-09-04 01:27:26","end_date_site_timezone":"2016-10-19 02:53:06",` +
	`"date_range_string":"2016-09-04,2016-10-19","duration":"45 days and 1 hour"}`

const runningExperiment = `{"idexperiment":4,"idsite":1,"name":"Banner","status":"running","excluded_targets":` +
	`{"0":{"attribute":"url","inverted":1,"type":"equals_exactly","value":"https://example.org/x"}},"variations":{"0":{"idvariation":9,"name":"B"}}}`

const foreignExperiment = `{"idexperiment":55,"idsite":3,"name":"Foreign"}`

const experimentMissing = "The experiment does not exist"

const includeTag = `<script src="https://matomo.example.org/plugins/AbTesting/js/tracker.min.js"></script>`

func experimentAnswers(extra answers) answers {
	return with(pluginsOn(), with(answers{
		"AbTesting.getAllExperiments": func(call) *http.Response {
			return ok("[" + sidebarExperiment + "," + runningExperiment + "," + foreignExperiment + `,{"idexperiment":9,"name":"No site"}]`)
		},
		"AbTesting.getActiveExperiments":     func(call) *http.Response { return ok("[" + runningExperiment + "," + foreignExperiment + "]") },
		"AbTesting.getExperimentsByStatuses": func(call) *http.Response { return ok("[" + sidebarExperiment + "," + foreignExperiment + "]") },
		"AbTesting.getExperiment": func(c call) *http.Response {
			switch c.form.Get("idExperiment") {
			case "1":
				return ok(sidebarExperiment)
			case "4":
				return ok(runningExperiment)
			case "55":
				return ok(foreignExperiment)
			case "6":
				return ok(`{"idexperiment":6,"name":"No site"}`)
			case "8":
				return ok(sidebarExperiment)
			case "5":
				return ok(`{"value":false}`)
			}
			return failure(experimentMissing)
		},
		"AbTesting.getAvailableStatuses":         fixed(`[{"value":"running","name":"Running"},{"value":"archived","name":"Archived"}]`),
		"AbTesting.getAvailableSuccessMetrics":   fixed(`{"nb_conversions":"Conversions","nb_pageviews":"Pageviews"}`),
		"AbTesting.getAvailableTargetAttributes": fixed(`{"url":{"types":["any","equals_exactly"],"example":"https://example.org"}}`),
		"AbTesting.getJsExperimentTemplate":      fixed(`"<script>window._paq.push(['AbTesting::create',{name:'Banner'}]);</script>"`),
		"AbTesting.getJsIncludeTemplate":         fixed(`{"value":` + string(mustJSON(includeTag)) + `}`),
	}, extra))
}

func mustJSON(v any) []byte {
	out, _ := json.Marshal(v)
	return out
}

var experimentCalls = []struct{ tool, arguments string }{
	{"matomo.experiments.list", `{"idSite":1}`},
	{"matomo.experiments.get", `{"idSite":1,"idExperiment":1}`},
	{"matomo.experiment-options.list", `{"idSite":1}`},
	{"matomo.experiment-templates.get", `{"kind":"experiment","idSite":1,"idExperiment":4}`},
	{"matomo.experiment-templates.get", `{"kind":"include"}`},
}

func TestExperimentToolsAreRegisteredAsOpenReadsInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, c := range experimentCalls {
		d, _, found := reg.Lookup(c.tool)
		if !found || d.Risk != readRisk(experimentsSensitive) || d.Risk.Idempotency != capability.IdempotencySafe ||
			d.Risk.Confirmation != capability.ConfirmationNone || !d.Risk.OpenWorld || d.RequiresToolAllowList {
			t.Errorf("%s: found %t, %+v", c.tool, found, d)
		}
		for _, banned := range []string{"callback", "token", "module", "method", "format", "filter_limit"} {
			if strings.Contains(string(d.InputSchema), `"`+banned+`"`) {
				t.Errorf("%s schema offers %s", c.tool, banned)
			}
		}
	}
	if d, _, _ := reg.Lookup("matomo.experiment-templates.get"); !strings.Contains(d.Description, "untrusted") {
		t.Errorf("description = %s", d.Description)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		if joined := strings.Join(profile.Tools, " "); strings.Contains(joined, "experiment") {
			t.Errorf("profile %s selects an experiment tool", profile.ID)
		}
	}
}

func TestExperimentsListReadsAllAndKeepsOnlyTheWebsiteAndPages(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, experimentAnswers(nil).handler(t))
	result, err := env.invoke("matomo.experiments.list", "two", `{"idSite":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated AbTesting.getAllExperiments" {
		t.Errorf("requests = %s", methods(calls))
	}
	if sent := lastCall(calls, "AbTesting.getAllExperiments").form; sent.Get("idSite") != "1" || sent.Has("statuses[]") {
		t.Errorf("form = %v", sent)
	}
	if strings.Contains(result, "Foreign") || strings.Contains(result, "No site") {
		t.Errorf("result = %s", result)
	}
	var out ExperimentsResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	if out.IDSite != 1 || len(out.Experiments) != 2 || out.HasMore {
		t.Fatalf("result = %s", result)
	}
	side, running := out.Experiments[0], out.Experiments[1]
	if side.IDExperiment != 1 || side.IDSite != 1 || side.Name != "AdSidebar" || side.Status != "archived" ||
		side.ConfidenceThreshold == nil || *side.ConfidenceThreshold != 95 || side.MdeRelative == nil || *side.MdeRelative != 15 ||
		side.Hypothesis != "If I display only one ad" || len(side.IncludedTargets) != 1 ||
		side.IncludedTargets[0].Attribute != "url" || side.IncludedTargets[0].Inverted == nil || *side.IncludedTargets[0].Inverted ||
		side.IncludedTargets[0].Type != "any" || len(side.ExcludedTargets) != 0 ||
		strings.Join(side.SuccessMetrics, ",") != "nb_conversions,nb_pageviews" ||
		side.PercentageParticipant == nil || *side.PercentageParticipant != 100 || side.OriginalRedirectURL != "" ||
		side.StartDate != "2016-09-03 23:27:26" || side.ModifiedDate != "2016-10-19 00:57:26" ||
		side.EndDate != "2016-10-19 00:53:06" || side.ForwardUTMParams == nil || *side.ForwardUTMParams ||
		side.ForwardAllQueryParams == nil || !*side.ForwardAllQueryParams || len(side.Variations) != 2 ||
		side.Variations[0].IDVariation == nil || *side.Variations[0].IDVariation != 3 || side.Variations[0].Percentage != nil ||
		side.Variations[1].Percentage == nil || *side.Variations[1].Percentage != 50 ||
		side.Variations[1].RedirectURL != "https://example.org/b" ||
		side.StartDateSiteTimezone != "2016-09-04 01:27:26" || side.EndDateSiteTimezone != "2016-10-19 02:53:06" ||
		side.DateRangeString != "2016-09-04,2016-10-19" || side.Duration != "45 days and 1 hour" {
		t.Errorf("experiment = %+v", side)
	}
	// Numbers as numbers and lists keyed by position give the same values.
	if running.IDExperiment != 4 || len(running.ExcludedTargets) != 1 || running.ExcludedTargets[0].Inverted == nil ||
		!*running.ExcludedTargets[0].Inverted || running.ExcludedTargets[0].Value != "https://example.org/x" ||
		len(running.Variations) != 1 || running.Variations[0].Name != "B" {
		t.Errorf("running = %+v", running)
	}

	page, err := env.invoke("matomo.experiments.list", "two", `{"idSite":1,"limit":1}`)
	var first ExperimentsResult
	if err != nil || json.Unmarshal([]byte(page), &first) != nil || len(first.Experiments) != 1 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page = %s, err = %v", page, err)
	}
	page, err = env.invoke("matomo.experiments.list", "two", `{"idSite":1,"limit":1,"cursor":"`+first.NextCursor+`"}`)
	var second ExperimentsResult
	if err != nil || json.Unmarshal([]byte(page), &second) != nil || len(second.Experiments) != 1 || second.HasMore ||
		second.Experiments[0].IDExperiment != 4 {
		t.Fatalf("second page = %s, err = %v", page, err)
	}
	for _, other := range []string{`{"idSite":2,"limit":1,"cursor":"%s"}`, `{"idSite":1,"limit":1,"active":true,"cursor":"%s"}`,
		`{"idSite":1,"limit":1,"statuses":["running"],"cursor":"%s"}`} {
		if _, err := env.invoke("matomo.experiments.list", "two", strings.Replace(other, "%s", first.NextCursor, 1)); err == nil {
			t.Errorf("a cursor of another list was accepted: %s", other)
		}
	}
}

func TestExperimentsListChoosesOneMethod(t *testing.T) {
	for name, tc := range map[string]struct{ arguments, method, ids string }{
		"active":   {`{"idSite":1,"active":true}`, "AbTesting.getActiveExperiments", "4"},
		"statuses": {`{"idSite":1,"statuses":["running","archived"]}`, "AbTesting.getExperimentsByStatuses", "1"},
	} {
		var calls []call
		env := newEnvironment(t, &calls, experimentAnswers(nil).handler(t))
		result, err := env.invoke("matomo.experiments.list", "two", tc.arguments)
		var out ExperimentsResult
		if err != nil || json.Unmarshal([]byte(result), &out) != nil || len(out.Experiments) != 1 ||
			strings.Contains(result, "Foreign") {
			t.Fatalf("%s: result = %s, err = %v", name, result, err)
		}
		if methods(calls) != "API.getMatomoVersion API.isPluginActivated "+tc.method {
			t.Errorf("%s: requests = %s", name, methods(calls))
		}
		sent := lastCall(calls, tc.method).form
		if name == "statuses" && strings.Join(sent["statuses[]"], ",") != "running,archived" || sent.Get("idSite") != "1" {
			t.Errorf("%s: form = %v", name, sent)
		}
	}
}

func TestExperimentsListWithoutExperimentsIsEmpty(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, experimentAnswers(answers{"AbTesting.getAllExperiments": fixed(`[]`)}).handler(t))
	result, err := env.invoke("matomo.experiments.list", "one", `{}`)
	if err != nil || result != `{"experiments":[],"has_more":false,"idSite":2}` &&
		result != `{"has_more":false,"idSite":2}` || lastCall(calls, "AbTesting.getAllExperiments").form.Get("idSite") != "2" {
		t.Errorf("result = %s, err = %v", result, err)
	}
}

func TestExperimentsGetProvesTheWebsite(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, experimentAnswers(nil).handler(t))
	result, err := env.invoke("matomo.experiments.get", "two", `{"idSite":1,"idExperiment":1}`)
	var experiment Experiment
	if err != nil || json.Unmarshal([]byte(result), &experiment) != nil || experiment.IDExperiment != 1 || experiment.IDSite != 1 ||
		experiment.Name != "AdSidebar" || methods(calls) != "API.getMatomoVersion API.isPluginActivated AbTesting.getExperiment" {
		t.Fatalf("result = %s, err = %v, requests = %s", result, err, methods(calls))
	}
	if sent := lastCall(calls, "AbTesting.getExperiment").form; sent.Get("idSite") != "1" || sent.Get("idExperiment") != "1" {
		t.Errorf("form = %v", sent)
	}
}

func TestExperimentsGetRefusesWhatItCannotProve(t *testing.T) {
	for name, tc := range map[string]struct {
		arguments string
		class     provider.Class
	}{
		"experiment of another website": {`{"idSite":1,"idExperiment":55}`, provider.ClassNotFound},
		"answer without website":        {`{"idSite":1,"idExperiment":6}`, provider.ClassNotFound},
		"answer false":                  {`{"idSite":1,"idExperiment":5}`, provider.ClassNotFound},
		"no experiment":                 {`{"idSite":1,"idExperiment":3}`, provider.ClassNotFound},
		"another experiment":            {`{"idSite":1,"idExperiment":8}`, provider.ClassInvalidResponse},
	} {
		var calls []call
		env := newEnvironment(t, &calls, experimentAnswers(nil).handler(t))
		_, err := env.invoke("matomo.experiments.get", "two", tc.arguments)
		if classOf(err) != tc.class || strings.Contains(err.Error(), "55") || strings.Contains(err.Error(), "Foreign") ||
			strings.Contains(err.Error(), experimentMissing) {
			t.Errorf("%s: err = %v", name, err)
		}
		if countMethod(calls, "AbTesting.getExperiment") != 1 {
			t.Errorf("%s: requests = %s", name, methods(calls))
		}
	}
}

func TestExperimentOptionsListReadsEveryOption(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, experimentAnswers(nil).handler(t))
	result, err := env.invoke("matomo.experiment-options.list", "two", `{"idSite":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated AbTesting.getAvailableStatuses "+
		"AbTesting.getAvailableSuccessMetrics AbTesting.getAvailableTargetAttributes" {
		t.Errorf("requests = %s", methods(calls))
	}
	if lastCall(calls, "AbTesting.getAvailableStatuses").form.Get("idSite") != "1" ||
		lastCall(calls, "AbTesting.getAvailableSuccessMetrics").form.Get("idSite") != "1" ||
		lastCall(calls, "AbTesting.getAvailableTargetAttributes").form.Has("idSite") {
		t.Errorf("website parameters are wrong: %v", calls)
	}
	var out ExperimentOptionsResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	statuses, _ := out.Statuses.([]any)
	metrics, _ := out.SuccessMetrics.(map[string]any)
	attributes, _ := out.TargetAttributes.(map[string]any)
	if out.IDSite != 1 || len(statuses) != 2 || metrics["nb_conversions"] != "Conversions" || attributes["url"] == nil ||
		len(out.Unavailable) != 0 {
		t.Errorf("result = %s", result)
	}
}

func TestExperimentOptionsListNamesWhatTheTokenMayNotRead(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, experimentAnswers(answers{
		"AbTesting.getAvailableSuccessMetrics": fixed(`{"result":"error","message":"` + permissionMsg + bodyCanary + `"}`),
	}).handler(t))
	result, err := env.invoke("matomo.experiment-options.list", "two", `{"idSite":1}`)
	var out ExperimentOptionsResult
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || len(out.Unavailable) != 1 ||
		out.Unavailable[0] != (ExperimentOptionGap{"success_metrics", "permission"}) || out.SuccessMetrics != nil ||
		out.Statuses == nil || out.TargetAttributes == nil || strings.Contains(result, bodyCanary) {
		t.Errorf("result = %s, err = %v", result, err)
	}

	calls = nil
	denied := func(call) *http.Response { return failure(permissionMsg + bodyCanary) }
	env = newEnvironment(t, &calls, experimentAnswers(answers{"AbTesting.getAvailableStatuses": denied,
		"AbTesting.getAvailableSuccessMetrics": denied, "AbTesting.getAvailableTargetAttributes": denied}).handler(t))
	if _, err := env.invoke("matomo.experiment-options.list", "two", `{"idSite":1}`); classOf(err) != provider.ClassPermission ||
		err == nil || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("err = %v", err)
	}
}

func TestExperimentTemplatesReadTextAndNeverRunIt(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, experimentAnswers(nil).handler(t))
	result, err := env.invoke("matomo.experiment-templates.get", "two", `{"kind":"experiment","idSite":1,"idExperiment":4}`)
	var out ExperimentTemplateResult
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.Kind != "experiment" || out.IDSite != 1 ||
		out.IDExperiment != 4 || !strings.HasPrefix(out.Template, "<script>window._paq") || out.Truncated {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	// The experiment is proven for the website before the template is asked for.
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated AbTesting.getExperiment AbTesting.getJsExperimentTemplate" {
		t.Errorf("requests = %s", methods(calls))
	}
	if sent := lastCall(calls, "AbTesting.getJsExperimentTemplate").form; sent.Get("idSite") != "1" || sent.Get("idExperiment") != "4" {
		t.Errorf("form = %v", sent)
	}

	calls = nil
	result, err = env.invoke("matomo.experiment-templates.get", "one", `{"kind":"include"}`)
	out = ExperimentTemplateResult{}
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || out.Kind != "include" || out.Template != includeTag ||
		out.IDSite != 0 || countMethod(calls, "AbTesting.getJsIncludeTemplate") != 1 || countMethod(calls, "AbTesting.getExperiment") != 0 {
		t.Fatalf("result = %s, err = %v, requests = %s", result, err, methods(calls))
	}
	if sent := lastCall(calls, "AbTesting.getJsIncludeTemplate").form; sent.Has("idSite") || sent.Has("idExperiment") {
		t.Errorf("form = %v", sent)
	}

	// Matomo answers an empty string, or false, when there is nothing to embed.
	for _, empty := range []string{`""`, `{"value":""}`, `false`} {
		env = newEnvironment(t, &calls, experimentAnswers(answers{"AbTesting.getJsIncludeTemplate": fixed(empty)}).handler(t))
		result, err = env.invoke("matomo.experiment-templates.get", "two", `{"kind":"include"}`)
		if err != nil || result != `{"kind":"include","template":""}` {
			t.Errorf("%s: result = %s, err = %v", empty, result, err)
		}
	}
	env = newEnvironment(t, &calls, experimentAnswers(answers{"AbTesting.getJsIncludeTemplate": fixed(`[1]`)}).handler(t))
	if _, err = env.invoke("matomo.experiment-templates.get", "two", `{"kind":"include"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
}

func TestExperimentTemplatesAskNothingForAnExperimentOfAnotherWebsite(t *testing.T) {
	for _, id := range []string{"55", "6", "5", "3"} {
		var calls []call
		env := newEnvironment(t, &calls, experimentAnswers(nil).handler(t))
		_, err := env.invoke("matomo.experiment-templates.get", "two", `{"kind":"experiment","idSite":1,"idExperiment":`+id+`}`)
		if classOf(err) != provider.ClassNotFound || strings.Contains(err.Error(), "55") ||
			countMethod(calls, "AbTesting.getJsExperimentTemplate") != 0 {
			t.Errorf("experiment %s: err = %v, requests = %s", id, err, methods(calls))
		}
	}
}

func TestExperimentToolsRefuseBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, experimentAnswers(nil).handler(t))
	for _, c := range []struct{ tool, connection, arguments string }{
		{"matomo.experiments.list", "two", `{"idSite":3}`},
		{"matomo.experiments.list", "two", `{}`},
		{"matomo.experiments.list", "instance", `{}`},
		{"matomo.experiments.list", "two", `{"idSite":1,"callback":"x"}`},
		{"matomo.experiments.list", "two", `{"idSite":1,"statuses":[]}`},
		{"matomo.experiments.list", "two", `{"idSite":1,"statuses":["running","running"]}`},
		{"matomo.experiments.list", "two", `{"idSite":1,"statuses":["a b"]}`},
		{"matomo.experiments.list", "two", `{"idSite":1,"statuses":"running"}`},
		{"matomo.experiments.list", "two", `{"idSite":1,"active":false}`},
		{"matomo.experiments.list", "two", `{"idSite":1,"active":true,"statuses":["running"]}`},
		{"matomo.experiments.list", "two", `{"idSite":1,"limit":0}`},
		{"matomo.experiments.get", "two", `{"idSite":3,"idExperiment":1}`},
		{"matomo.experiments.get", "two", `{"idExperiment":1}`},
		{"matomo.experiments.get", "one", `{"idSite":1,"idExperiment":1}`},
		{"matomo.experiments.get", "two", `{"idSite":1}`},
		{"matomo.experiments.get", "two", `{"idSite":1,"idExperiment":0}`},
		{"matomo.experiments.get", "two", `{"idSite":1,"idExperiment":"1"}`},
		{"matomo.experiments.get", "instance", `{"idSite":1,"idExperiment":1}`},
		{"matomo.experiment-options.list", "two", `{}`},
		{"matomo.experiment-options.list", "two", `{"idSite":3}`},
		{"matomo.experiment-options.list", "instance", `{"idSite":1}`},
		{"matomo.experiment-options.list", "two", `{"idSite":1,"method":"x"}`},
		{"matomo.experiment-templates.get", "two", `{"idSite":1,"idExperiment":1}`},
		{"matomo.experiment-templates.get", "two", `{"kind":"other"}`},
		{"matomo.experiment-templates.get", "two", `{"kind":"experiment","idSite":3,"idExperiment":1}`},
		{"matomo.experiment-templates.get", "two", `{"kind":"experiment","idExperiment":1}`},
		{"matomo.experiment-templates.get", "one", `{"kind":"experiment","idSite":1,"idExperiment":1}`},
		{"matomo.experiment-templates.get", "two", `{"kind":"experiment","idSite":1}`},
		{"matomo.experiment-templates.get", "two", `{"kind":"experiment","idSite":1,"idExperiment":0}`},
		{"matomo.experiment-templates.get", "instance", `{"kind":"experiment","idSite":1,"idExperiment":1}`},
		{"matomo.experiment-templates.get", "instance", `{"kind":"include"}`},
		{"matomo.experiment-templates.get", "two", `{"kind":"include","idSite":1}`},
		{"matomo.experiment-templates.get", "two", `{"kind":"include","idExperiment":1}`},
		{"matomo.experiment-templates.get", "two", `{"kind":"include","token_auth":"x"}`},
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

func TestExperimentRequestsCarryTheBearerTokenOnlyInTheHeader(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, experimentAnswers(nil).handler(t))
	all := append([]struct{ tool, arguments string }{}, experimentCalls...)
	all = append(all, struct{ tool, arguments string }{"matomo.experiments.list", `{"idSite":1,"statuses":["running"]}`},
		struct{ tool, arguments string }{"matomo.experiments.list", `{"idSite":1,"active":true}`})
	for _, c := range all {
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

func TestExperimentToolsMapMatomoErrorsAndTransportFailures(t *testing.T) {
	firstOf := map[string]string{"matomo.experiments.list": "AbTesting.getAllExperiments", "matomo.experiments.get": "AbTesting.getExperiment",
		"matomo.experiment-options.list": "AbTesting.getAvailableStatuses", "matomo.experiment-templates.get": "AbTesting.getExperiment"}
	for i, c := range experimentCalls {
		method := firstOf[c.tool]
		if i == len(experimentCalls)-1 {
			method = "AbTesting.getJsIncludeTemplate"
		}
		hits := func(in call) bool {
			return in.api() == method || c.tool == "matomo.experiment-options.list" && strings.HasPrefix(in.api(), "AbTesting.")
		}
		for name, tc := range map[string]struct {
			answer      func(call) (*http.Response, error)
			class       provider.Class
			unsupported bool
		}{
			"permission": {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false},
			"object not found": {func(call) (*http.Response, error) {
				return ok2(failure(experimentMissing + bodyCanary))
			}, provider.ClassNotFound, false},
			"method missing": {func(call) (*http.Response, error) {
				return ok2(failure("The method 'AbTesting.x' does not exist or is not available in the module 'AbTesting'." + bodyCanary))
			}, "", true},
			"module missing": {func(call) (*http.Response, error) {
				return ok2(failure("The plugin AbTesting is not activated." + bodyCanary))
			}, "", true},
			"429":        {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, bodyCanary), nil }, provider.ClassRateLimited, false},
			"timeout":    {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false},
			"unreadable": {func(call) (*http.Response, error) { return ok2(ok("<html>" + bodyCanary + "</html>")) }, provider.ClassInvalidResponse, false},
			"unknown":    {func(call) (*http.Response, error) { return ok2(failure("Something " + bodyCanary)) }, provider.ClassInvalidResponse, false},
		} {
			if name == "object not found" && (c.tool == "matomo.experiment-options.list" || c.tool == "matomo.experiment-templates.get" && i == len(experimentCalls)-1) {
				continue
			}
			var calls []call
			env := newEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if hits(in) {
					return tc.answer(in)
				}
				return experimentAnswers(nil).handler(t)(in)
			})
			clock := time.Now()
			t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, func() time.Time { return clock },
				func(context.Context, time.Duration) error { return nil })))
			_, err := env.invoke(c.tool, "two", c.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), experimentMissing) ||
				isUnsupported(err) != tc.unsupported || classOf(err) != tc.class || countMethod(calls, method) != 1 {
				t.Errorf("%s %s: err = %v (class %q)", c.arguments, name, err, classOf(err))
			}
		}
	}
}

func TestExperimentTemplateMethodErrorsAfterTheProofMapToo(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, experimentAnswers(answers{
		"AbTesting.getJsExperimentTemplate": func(call) *http.Response {
			return failure("The method 'AbTesting.getJsExperimentTemplate' does not exist or is not available in the module 'AbTesting'." + bodyCanary)
		},
	}).handler(t))
	_, err := env.invoke("matomo.experiment-templates.get", "two", experimentCalls[3].arguments)
	if !isUnsupported(err) || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("err = %v", err)
	}
}

func TestExperimentToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range experimentCalls {
		var calls []call
		env := newEnvironment(t, &calls, with(experimentAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.invoke(c.tool, "two", c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" ||
			lastCall(calls, "API.isPluginActivated").form.Get("pluginName") != experimentsPlugin {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}

func TestExperimentTextAndListsAreShortened(t *testing.T) {
	long := strings.Repeat("n", 6000)
	targets := strings.TrimSuffix(strings.Repeat(`{"attribute":"a","type":"t","value":"`+long+`"},`, 150), ",")
	variations := strings.TrimSuffix(strings.Repeat(`{"idvariation":1,"name":"`+long+`","redirect_url":"`+long+`"},`, 150), ",")
	body := `{"idexperiment":2,"idsite":1,"name":"` + long + `","hypothesis":"` + long + `","included_targets":[` + targets +
		`],"variations":[` + variations + `]}`
	var calls []call
	env := newEnvironment(t, &calls, experimentAnswers(answers{
		"AbTesting.getExperiment":           fixed(body),
		"AbTesting.getJsExperimentTemplate": fixed(`"` + strings.Repeat("s", maxSnippetText+500) + `"`),
	}).handler(t))
	result, err := env.invoke("matomo.experiments.get", "two", `{"idSite":1,"idExperiment":2}`)
	var experiment Experiment
	if err != nil || json.Unmarshal([]byte(result), &experiment) != nil || len(experiment.Name) != maxStringLength ||
		len(experiment.Hypothesis) != maxUrlLength || len(experiment.IncludedTargets) != maxExperimentRows ||
		len(experiment.IncludedTargets[0].Value) != maxUrlLength || len(experiment.Variations) != maxExperimentRows ||
		len(experiment.Variations[0].Name) != maxStringLength || len(experiment.Variations[0].RedirectURL) != maxUrlLength {
		t.Errorf("result length %d, err = %v", len(result), err)
	}
	result, err = env.invoke("matomo.experiment-templates.get", "two", `{"kind":"experiment","idSite":1,"idExperiment":2}`)
	var template ExperimentTemplateResult
	if err != nil || json.Unmarshal([]byte(result), &template) != nil || len(template.Template) != maxSnippetText || !template.Truncated {
		t.Errorf("template length %d, err = %v", len(template.Template), err)
	}
}
