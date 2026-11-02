package matomo

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
)

// The fixtures follow the JSON examples of the FormAnalytics reference (getForms, getFormsByStatuses,
// getCounters, getCurrentMostPopularForms). Assumptions, as the reference gives none: the answer of getForm
// (taken as one element of the getForms example), the answers of the five option methods, the map form of
// getAllGoals, the array form statuses[] of a request, and the member idsite of a popular-forms row.
const commentForm = `{"idsiteform":2,"idsite":1,"idgoal":null,"name":"Comment Form",` +
	`"description":"Standard WordPress comment form on posts","status":"running","in_overview":0,` +
	`"match_form_rules":[{"attribute":"form_id","pattern":"equals_exactly","value":"commentform"}],` +
	`"match_page_rules":[],"conversion_rule_option":"page_visit","conversion_rules":[],` +
	`"fields":[{"name":"comment","type":"textarea","displayName":"Comment"},` +
	`{"name":"author","type":"text","displayName":"Name"},{"name":"subscribe-reloaded","type":"select","displayName":""}],` +
	`"auto_created":true,"created_date":"2017-03-25 08:15:00","updated_date":"2019-08-04 04:35:51"}`

const jobForm = `{"idsiteform":"7","idsite":"1","idgoal":"3","name":"Apply to Job Form","description":"","status":"finished",` +
	`"in_overview":"1","match_form_rules":{"0":{"attribute":"form_id","pattern":"equals","value":"wpjb-apply-form"}},` +
	`"match_page_rules":[{"attribute":"url","pattern":"contains","value":"jobs"}],"conversion_rule_option":"page_visit",` +
	`"conversion_rules":[{"attribute":"url","pattern":"contains","value":"thanks"}],` +
	`"fields":[{"name":"cv_file","type":"file","displayName":"CV file"}],"auto_created":"0"}`

const foreignForm = `{"idsiteform":55,"idsite":3,"name":"Foreign"}`

const formMissing = "The requested form does not exist"

func formAnswers(extra answers) answers {
	return with(pluginsOn(), with(answers{
		"FormAnalytics.getForms": func(call) *http.Response {
			return ok("[" + commentForm + "," + jobForm + "," + foreignForm + `,{"idsiteform":9,"name":"No site"}]`)
		},
		"FormAnalytics.getFormsByStatuses": func(call) *http.Response { return ok("[" + jobForm + "," + foreignForm + "]") },
		"FormAnalytics.getForm": func(c call) *http.Response {
			switch c.form.Get("idForm") {
			case "2":
				return ok(commentForm)
			case "7":
				return ok(jobForm)
			case "55":
				return ok(foreignForm)
			case "6":
				return ok(`{"idsiteform":6,"name":"No site"}`)
			case "8":
				return ok(commentForm)
			case "5":
				return ok(`false`)
			}
			return failure(formMissing)
		},
		"FormAnalytics.getCounters": fixed(`{"nb_form_viewers":117,"nb_form_starters":"3","nb_form_submitters":"1",` +
			`"nb_form_resubmitters":"0","nb_form_conversions":"0","avg_form_time_spent":109.44,"form_starters_rate":"2.6%",` +
			`"form_submitter_rate":"33.3%","form_resubmitters_rate":"0%","form_conversion_rate":"0%"}`),
		"FormAnalytics.getCurrentMostPopularForms": fixed(`[{"label":"Checkout Form","idsiteform":42,"nb_form_viewers":11,` +
			`"nb_form_starters":"3","nb_form_submitters":"1","nb_form_resubmitters":"0","nb_form_conversions":"0",` +
			`"form_starters_rate":"27.3%","form_submitter_rate":"33.3%","form_conversion_rate":"0%","form_resubmitters_rate":"0%"},` +
			`{"label":"Comment Form","idsiteform":2,"nb_form_viewers":106,"nb_form_starters":"0","nb_form_submitters":"0",` +
			`"nb_form_resubmitters":"0","nb_form_conversions":"0","form_starters_rate":"0%","form_submitter_rate":"0%",` +
			`"form_conversion_rate":"0%","form_resubmitters_rate":"0%"},` +
			`{"label":"Foreign","idsiteform":55,"idsite":3,"nb_form_viewers":1},{"label":"No ID","nb_form_viewers":1}]`),
		"FormAnalytics.getAvailableStatuses":              fixed(`[{"value":"running","name":"Running"},{"value":"finished","name":"Finished"}]`),
		"FormAnalytics.getAvailableFormRules":             fixed(`[{"value":"form_id","name":"Form ID","patterns":["equals","contains"],"example":"login"}]`),
		"FormAnalytics.getAvailablePageRules":             fixed(`[{"value":"url","name":"URL","patterns":["equals_exactly"],"example":"https://example.org"}]`),
		"FormAnalytics.getAvailableConversionRuleOptions": fixed(`{"page_visit":"Page visit","form_submit":"Form submission"}`),
		"FormAnalytics.getAutoCreationSettings":           fixed(`"Forms are created automatically."`),
		"FormAnalytics.getAllGoals":                       fixed(`{"":"Default","3":"Newsletter","1":"Purchase","x":"Odd"}`),
	}, extra))
}

var formCalls = []struct{ tool, arguments string }{
	{"matomo.forms.list", `{"idSite":1}`},
	{"matomo.forms.get", `{"idSite":1,"idForm":2}`},
	{"matomo.forms.realtime", `{"idSite":1,"kind":"popular-forms","lastMinutes":30,"segment":"referrerName==example.com"}`},
	{"matomo.form-options.list", `{"idSite":1}`},
}

func TestFormToolsAreRegisteredAsOpenReadsInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, c := range formCalls {
		d, _, found := reg.Lookup(c.tool)
		if !found || d.Risk != readRisk(formsSensitive) || d.Risk.Idempotency != capability.IdempotencySafe ||
			d.Risk.Confirmation != capability.ConfirmationNone || !d.Risk.OpenWorld || d.RequiresToolAllowList {
			t.Errorf("%s: found %t, %+v", c.tool, found, d)
		}
		for _, banned := range []string{"callback", "token", "module", "method", "format", "filter_limit"} {
			if strings.Contains(string(d.InputSchema), `"`+banned+`"`) {
				t.Errorf("%s schema offers %s", c.tool, banned)
			}
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		if strings.Contains(strings.Join(profile.Tools, " "), "form") {
			t.Errorf("profile %s selects a form tool", profile.ID)
		}
	}
}

func TestFormsListReadsAllFormsKeepsOnlyTheWebsiteAndPages(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, formAnswers(nil).handler(t))
	result, err := env.invoke("matomo.forms.list", "two", `{"idSite":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated FormAnalytics.getForms" {
		t.Errorf("requests = %s", methods(calls))
	}
	if sent := lastCall(calls, "FormAnalytics.getForms").form; sent.Get("idSite") != "1" || sent.Has("statuses[]") {
		t.Errorf("form = %v", sent)
	}
	if strings.Contains(result, "Foreign") || strings.Contains(result, "No site") {
		t.Errorf("result = %s", result)
	}
	var out FormsResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	if out.IDSite != 1 || len(out.Forms) != 2 || out.HasMore {
		t.Fatalf("result = %s", result)
	}
	comment, job := out.Forms[0], out.Forms[1]
	if comment.IDForm != 2 || comment.IDSite != 1 || comment.IDGoal != nil || comment.Name != "Comment Form" ||
		comment.Description != "Standard WordPress comment form on posts" || comment.Status != "running" ||
		comment.AutoCreated == nil || !*comment.AutoCreated || comment.CreatedDate != "2017-03-25 08:15:00" ||
		comment.UpdatedDate != "2019-08-04 04:35:51" || comment.InOverview == nil || *comment.InOverview != 0 ||
		len(comment.MatchFormRules) != 1 || comment.MatchFormRules[0] != (FormRule{"form_id", "equals_exactly", "commentform"}) ||
		len(comment.MatchPageRules) != 0 || comment.ConversionRuleOption != "page_visit" || len(comment.Fields) != 3 ||
		comment.Fields[0] != (FormField{"comment", "textarea", "Comment"}) || comment.Fields[2].DisplayName != "" {
		t.Errorf("comment = %+v", comment)
	}
	// Matomo versions that answer numbers as strings and a rule list keyed by position give the same values.
	if job.IDForm != 7 || job.IDGoal == nil || *job.IDGoal != 3 || job.InOverview == nil || *job.InOverview != 1 ||
		job.AutoCreated == nil || *job.AutoCreated || len(job.MatchFormRules) != 1 || len(job.MatchPageRules) != 1 ||
		len(job.ConversionRules) != 1 || job.ConversionRules[0].Value != "thanks" {
		t.Errorf("job = %+v", job)
	}

	page, err := env.invoke("matomo.forms.list", "two", `{"idSite":1,"limit":1}`)
	var first FormsResult
	if err != nil || json.Unmarshal([]byte(page), &first) != nil || len(first.Forms) != 1 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("first page = %s, err = %v", page, err)
	}
	page, err = env.invoke("matomo.forms.list", "two", `{"idSite":1,"limit":1,"cursor":"`+first.NextCursor+`"}`)
	var second FormsResult
	if err != nil || json.Unmarshal([]byte(page), &second) != nil || len(second.Forms) != 1 || second.HasMore ||
		second.Forms[0].IDForm != 7 {
		t.Fatalf("second page = %s, err = %v", page, err)
	}
	for _, other := range []string{`{"idSite":2,"limit":1,"cursor":"%s"}`, `{"idSite":1,"limit":1,"statuses":["running"],"cursor":"%s"}`} {
		if _, err := env.invoke("matomo.forms.list", "two", strings.Replace(other, "%s", first.NextCursor, 1)); err == nil {
			t.Errorf("a cursor of another list was accepted: %s", other)
		}
	}
}

func TestFormsListWithStatusesAsksGetFormsByStatuses(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, formAnswers(nil).handler(t))
	result, err := env.invoke("matomo.forms.list", "two", `{"idSite":1,"statuses":["running","finished"]}`)
	var out FormsResult
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || len(out.Forms) != 1 || out.Forms[0].IDForm != 7 ||
		strings.Contains(result, "Foreign") {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated FormAnalytics.getFormsByStatuses" {
		t.Errorf("requests = %s", methods(calls))
	}
	sent := lastCall(calls, "FormAnalytics.getFormsByStatuses").form
	if got := strings.Join(sent["statuses[]"], ","); sent.Get("idSite") != "1" || got != "running,finished" {
		t.Errorf("form = %v", sent)
	}
}

func TestFormsListWithoutFormsIsEmpty(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, formAnswers(answers{"FormAnalytics.getForms": fixed(`[]`)}).handler(t))
	result, err := env.invoke("matomo.forms.list", "one", `{}`)
	if err != nil || result != `{"has_more":false,"idSite":2}` || lastCall(calls, "FormAnalytics.getForms").form.Get("idSite") != "2" {
		t.Errorf("result = %s, err = %v", result, err)
	}
}

func TestFormsGetProvesTheWebsite(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, formAnswers(nil).handler(t))
	result, err := env.invoke("matomo.forms.get", "two", `{"idSite":1,"idForm":2}`)
	var form Form
	if err != nil || json.Unmarshal([]byte(result), &form) != nil || form.IDForm != 2 || form.IDSite != 1 || form.Name != "Comment Form" ||
		methods(calls) != "API.getMatomoVersion API.isPluginActivated FormAnalytics.getForm" {
		t.Fatalf("result = %s, err = %v, requests = %s", result, err, methods(calls))
	}
	if sent := lastCall(calls, "FormAnalytics.getForm").form; sent.Get("idSite") != "1" || sent.Get("idForm") != "2" {
		t.Errorf("form = %v", sent)
	}
}

func TestFormsGetRefusesWhatItCannotProve(t *testing.T) {
	for name, tc := range map[string]struct {
		arguments string
		class     provider.Class
	}{
		"form of another website": {`{"idSite":1,"idForm":55}`, provider.ClassNotFound},
		"answer without website":  {`{"idSite":1,"idForm":6}`, provider.ClassNotFound},
		"answer false":            {`{"idSite":1,"idForm":5}`, provider.ClassNotFound},
		"no form":                 {`{"idSite":1,"idForm":3}`, provider.ClassNotFound},
		"another form":            {`{"idSite":1,"idForm":8}`, provider.ClassInvalidResponse},
	} {
		var calls []call
		env := newEnvironment(t, &calls, formAnswers(nil).handler(t))
		_, err := env.invoke("matomo.forms.get", "two", tc.arguments)
		if classOf(err) != tc.class || strings.Contains(err.Error(), "55") || strings.Contains(err.Error(), "Foreign") ||
			strings.Contains(err.Error(), formMissing) {
			t.Errorf("%s: err = %v", name, err)
		}
		if countMethod(calls, "FormAnalytics.getForm") != 1 {
			t.Errorf("%s: requests = %s", name, methods(calls))
		}
	}
}

func TestFormsRealtimeChoosesTheMethodAndReadsCounters(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, formAnswers(nil).handler(t))
	result, err := env.invoke("matomo.forms.realtime", "two", `{"idSite":1,"kind":"counters","lastMinutes":30,"segment":"referrerName==example.com"}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated FormAnalytics.getCounters" {
		t.Errorf("requests = %s", methods(calls))
	}
	sent := lastCall(calls, "FormAnalytics.getCounters").form
	if sent.Get("idSite") != "1" || sent.Get("lastMinutes") != "30" || sent.Get("segment") != "referrerName==example.com" ||
		sent.Has("filter_limit") {
		t.Errorf("form = %v", sent)
	}
	var out FormsRealtimeResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	c := out.Counters
	if out.IDSite != 1 || out.Kind != "counters" || out.LastMinutes != 30 || len(out.Forms) != 0 || c == nil ||
		c.Viewers == nil || *c.Viewers != 117 || c.Starters == nil || *c.Starters != 3 || *c.Submitters != 1 ||
		*c.Resubmitters != 0 || *c.Conversions != 0 || c.AvgTimeSpent == nil || *c.AvgTimeSpent != 109.44 ||
		c.StartersRate != "2.6%" || c.SubmitterRate != "33.3%" || c.ResubmittersRate != "0%" || c.ConversionRate != "0%" {
		t.Errorf("result = %s", result)
	}
}

func TestFormsRealtimeListsPopularFormsWithABoundedLimit(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, formAnswers(nil).handler(t))
	result, err := env.invoke("matomo.forms.realtime", "two", `{"idSite":1,"kind":"popular-forms","lastMinutes":60}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated FormAnalytics.getCurrentMostPopularForms" {
		t.Errorf("requests = %s", methods(calls))
	}
	sent := lastCall(calls, "FormAnalytics.getCurrentMostPopularForms").form
	if sent.Get("lastMinutes") != "60" || sent.Get("filter_limit") != "5" || sent.Has("segment") {
		t.Errorf("form = %v", sent)
	}
	var out FormsRealtimeResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	// The row of another website and the row without a form ID fall away.
	if out.Kind != "popular-forms" || out.Counters != nil || len(out.Forms) != 2 || strings.Contains(result, "Foreign") ||
		strings.Contains(result, "No ID") {
		t.Fatalf("result = %s", result)
	}
	checkout := out.Forms[0]
	if checkout.Label != "Checkout Form" || checkout.IDForm != 42 || checkout.Viewers == nil || *checkout.Viewers != 11 ||
		*checkout.Starters != 3 || checkout.StartersRate != "27.3%" || checkout.SubmitterRate != "33.3%" ||
		out.Forms[1].IDForm != 2 || *out.Forms[1].Viewers != 106 {
		t.Errorf("checkout = %+v", checkout)
	}
	if _, err := env.invoke("matomo.forms.realtime", "two", `{"idSite":1,"kind":"popular-forms","lastMinutes":60,"limit":1}`); err != nil {
		t.Fatal(err)
	}
	if lastCall(calls, "FormAnalytics.getCurrentMostPopularForms").form.Get("filter_limit") != "1" {
		t.Errorf("form = %v", lastCall(calls, "FormAnalytics.getCurrentMostPopularForms").form)
	}
	result, _ = env.invoke("matomo.forms.realtime", "two", `{"idSite":1,"kind":"popular-forms","lastMinutes":60,"limit":1}`)
	if json.Unmarshal([]byte(result), &out) != nil || len(out.Forms) != 1 {
		t.Errorf("a longer answer than limit was not cut: %s", result)
	}
}

func TestFormsRealtimeKeepsTheCountersOfAnEmptyAnswer(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, formAnswers(answers{"FormAnalytics.getCounters": fixed(`[]`),
		"FormAnalytics.getCurrentMostPopularForms": fixed(`[]`)}).handler(t))
	for _, kind := range []string{"counters", "popular-forms"} {
		result, err := env.invoke("matomo.forms.realtime", "two", `{"idSite":1,"kind":"`+kind+`","lastMinutes":5}`)
		if err != nil || !strings.Contains(result, `"kind":"`+kind+`"`) {
			t.Errorf("%s: result = %s, err = %v", kind, result, err)
		}
	}
}

func TestFormOptionsListReadsEveryOptionOfTheWebsite(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, formAnswers(nil).handler(t))
	result, err := env.invoke("matomo.form-options.list", "two", `{"idSite":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if methods(calls) != "API.getMatomoVersion API.isPluginActivated FormAnalytics.getAvailableStatuses "+
		"FormAnalytics.getAvailableFormRules FormAnalytics.getAvailablePageRules "+
		"FormAnalytics.getAvailableConversionRuleOptions FormAnalytics.getAutoCreationSettings FormAnalytics.getAllGoals" {
		t.Errorf("requests = %s", methods(calls))
	}
	for _, method := range []string{"getAvailableStatuses", "getAvailableFormRules", "getAvailablePageRules",
		"getAvailableConversionRuleOptions"} {
		if sent := lastCall(calls, "FormAnalytics."+method).form; sent.Has("idSite") {
			t.Errorf("%s sends %v", method, sent)
		}
	}
	for _, method := range []string{"getAutoCreationSettings", "getAllGoals"} {
		if lastCall(calls, "FormAnalytics."+method).form.Get("idSite") != "1" {
			t.Errorf("%s sends no idSite", method)
		}
	}
	var out FormOptionsResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	statuses, _ := out.Statuses.([]any)
	conversion, _ := out.ConversionRuleOptions.(map[string]any)
	if out.IDSite != 1 || len(statuses) != 2 || out.FormRules == nil || out.PageRules == nil ||
		conversion["page_visit"] != "Page visit" || out.AutoCreationSettings != "Forms are created automatically." ||
		len(out.Unavailable) != 0 || len(out.Goals) != 2 || out.Goals[0] != (FormGoal{1, "Purchase"}) ||
		out.Goals[1] != (FormGoal{3, "Newsletter"}) {
		t.Errorf("result = %s", result)
	}
}

func TestFormOptionsListNamesWhatTheTokenMayNotRead(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, formAnswers(answers{
		"FormAnalytics.getAvailablePageRules": fixed(`{"result":"error","message":"` + permissionMsg + bodyCanary + `"}`),
		"FormAnalytics.getAllGoals":           fixed(`[]`),
	}).handler(t))
	result, err := env.invoke("matomo.form-options.list", "two", `{"idSite":1}`)
	var out FormOptionsResult
	if err != nil || json.Unmarshal([]byte(result), &out) != nil || len(out.Unavailable) != 1 ||
		out.Unavailable[0] != (FormOptionGap{"page_rules", "permission"}) || out.PageRules != nil || out.Statuses == nil ||
		len(out.Goals) != 0 || strings.Contains(result, bodyCanary) {
		t.Errorf("result = %s, err = %v", result, err)
	}

	calls = nil
	denied := func(call) *http.Response { return failure(permissionMsg + bodyCanary) }
	all := answers{}
	for _, m := range []string{"getAvailableStatuses", "getAvailableFormRules", "getAvailablePageRules",
		"getAvailableConversionRuleOptions", "getAutoCreationSettings", "getAllGoals"} {
		all["FormAnalytics."+m] = denied
	}
	env = newEnvironment(t, &calls, formAnswers(all).handler(t))
	if _, err := env.invoke("matomo.form-options.list", "two", `{"idSite":1}`); classOf(err) != provider.ClassPermission ||
		err == nil || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("err = %v", err)
	}

	for name, answer := range map[string]func(call) *http.Response{
		"timeout-like invalid goals": fixed(`"text"`),
	} {
		env = newEnvironment(t, &calls, formAnswers(answers{"FormAnalytics.getAllGoals": answer}).handler(t))
		if _, err := env.invoke("matomo.form-options.list", "two", `{"idSite":1}`); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestFormToolsRefuseBeforeSecretAndRequest(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, formAnswers(nil).handler(t))
	realtime := func(extra string) string {
		return `{"idSite":1,"kind":"popular-forms","lastMinutes":30` + extra + `}`
	}
	for _, c := range []struct{ tool, connection, arguments string }{
		{"matomo.forms.list", "two", `{"idSite":3}`},
		{"matomo.forms.list", "two", `{}`},
		{"matomo.forms.list", "instance", `{}`},
		{"matomo.forms.list", "two", `{"idSite":1,"callback":"x"}`},
		{"matomo.forms.list", "two", `{"idSite":1,"statuses":[]}`},
		{"matomo.forms.list", "two", `{"idSite":1,"statuses":["running","running"]}`},
		{"matomo.forms.list", "two", `{"idSite":1,"statuses":["a b"]}`},
		{"matomo.forms.list", "two", `{"idSite":1,"statuses":"running"}`},
		{"matomo.forms.list", "two", `{"idSite":1,"limit":0}`},
		{"matomo.forms.get", "two", `{"idSite":3,"idForm":2}`},
		{"matomo.forms.get", "two", `{"idForm":2}`},
		{"matomo.forms.get", "one", `{"idSite":1,"idForm":2}`},
		{"matomo.forms.get", "two", `{"idSite":1}`},
		{"matomo.forms.get", "two", `{"idSite":1,"idForm":0}`},
		{"matomo.forms.get", "two", `{"idSite":1,"idForm":"2"}`},
		{"matomo.forms.get", "instance", `{"idSite":1,"idForm":2}`},
		{"matomo.forms.realtime", "two", strings.Replace(realtime(""), `"idSite":1`, `"idSite":3`, 1)},
		{"matomo.forms.realtime", "one", realtime("")},
		{"matomo.forms.realtime", "two", `{"idSite":1,"kind":"popular-forms"}`},
		{"matomo.forms.realtime", "two", `{"idSite":1,"lastMinutes":30}`},
		{"matomo.forms.realtime", "two", strings.Replace(realtime(""), `"popular-forms"`, `"report"`, 1)},
		{"matomo.forms.realtime", "two", strings.Replace(realtime(""), `30`, `0`, 1)},
		{"matomo.forms.realtime", "two", strings.Replace(realtime(""), `30`, `2881`, 1)},
		{"matomo.forms.realtime", "two", realtime(`,"limit":0`)},
		{"matomo.forms.realtime", "two", realtime(`,"limit":101`)},
		{"matomo.forms.realtime", "two", realtime(`,"limit":-1`)},
		{"matomo.forms.realtime", "two", strings.Replace(realtime(`,"limit":5`), `"popular-forms"`, `"counters"`, 1)},
		{"matomo.forms.realtime", "two", realtime(`,"segment":"not a segment"`)},
		{"matomo.forms.realtime", "two", realtime(`,"filter_limit":-1`)},
		{"matomo.forms.realtime", "two", realtime(`,"token_auth":"x"`)},
		{"matomo.form-options.list", "two", `{}`},
		{"matomo.form-options.list", "two", `{"idSite":3}`},
		{"matomo.form-options.list", "instance", `{"idSite":1}`},
		{"matomo.form-options.list", "two", `{"idSite":1,"method":"x"}`},
	} {
		_, err := env.invoke(c.tool, c.connection, c.arguments)
		if err == nil {
			t.Errorf("%s %s %s was accepted", c.tool, c.connection, c.arguments)
		} else if strings.Contains(err.Error(), "idSite=3") || strings.Contains(err.Error(), "site/") ||
			strings.Contains(err.Error(), "-1") {
			t.Errorf("%s names a value: %v", c.tool, err)
		}
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("%d requests and %d secret reads before the refusal", len(calls), *env.reads)
	}
}

func TestFormRequestsCarryTheBearerTokenOnlyInTheHeader(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, formAnswers(nil).handler(t))
	all := append([]struct{ tool, arguments string }{}, formCalls...)
	all = append(all, struct{ tool, arguments string }{"matomo.forms.realtime", `{"idSite":1,"kind":"counters","lastMinutes":30}`},
		struct{ tool, arguments string }{"matomo.forms.list", `{"idSite":1,"statuses":["running"]}`})
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
			"disable_root_datatable_post_processor"} {
			if c.form.Has(banned) {
				t.Errorf("%s sends %s", c.api(), banned)
			}
		}
		if c.form.Has("filter_limit") && (c.api() != "FormAnalytics.getCurrentMostPopularForms" || c.form.Get("filter_limit") == "-1") {
			t.Errorf("%s sends filter_limit %q", c.api(), c.form.Get("filter_limit"))
		}
	}
	if got := env.red.Apply("x " + tokenValue + " y"); strings.Contains(got, tokenValue) {
		t.Errorf("redacted text = %q", got)
	}
}

func TestFormToolsMapMatomoErrorsAndTransportFailures(t *testing.T) {
	firstOf := map[string]string{"matomo.forms.list": "FormAnalytics.getForms", "matomo.forms.get": "FormAnalytics.getForm",
		"matomo.forms.realtime": "FormAnalytics.getCurrentMostPopularForms", "matomo.form-options.list": "FormAnalytics.getAvailableStatuses"}
	for _, c := range formCalls {
		method := firstOf[c.tool]
		hits := func(in call) bool {
			return in.api() == method || c.tool == "matomo.form-options.list" && strings.HasPrefix(in.api(), "FormAnalytics.")
		}
		for name, tc := range map[string]struct {
			answer      func(call) (*http.Response, error)
			class       provider.Class
			unsupported bool
		}{
			"permission": {func(call) (*http.Response, error) { return ok2(failure(permissionMsg + bodyCanary)) }, provider.ClassPermission, false},
			"object not found": {func(call) (*http.Response, error) {
				return ok2(failure(formMissing + bodyCanary))
			}, provider.ClassNotFound, false},
			"method missing": {func(call) (*http.Response, error) {
				return ok2(failure("The method 'FormAnalytics.x' does not exist or is not available in the module 'FormAnalytics'." + bodyCanary))
			}, "", true},
			"module missing": {func(call) (*http.Response, error) {
				return ok2(failure("The plugin FormAnalytics is not activated." + bodyCanary))
			}, "", true},
			"429":        {func(call) (*http.Response, error) { return respond(http.StatusTooManyRequests, bodyCanary), nil }, provider.ClassRateLimited, false},
			"timeout":    {func(call) (*http.Response, error) { return nil, errTimeout }, provider.ClassTimeout, false},
			"unreadable": {func(call) (*http.Response, error) { return ok2(ok("<html>" + bodyCanary + "</html>")) }, provider.ClassInvalidResponse, false},
			"unknown":    {func(call) (*http.Response, error) { return ok2(failure("Something " + bodyCanary)) }, provider.ClassInvalidResponse, false},
		} {
			if c.tool == "matomo.form-options.list" && name == "object not found" {
				continue
			}
			var calls []call
			env := newEnvironment(t, &calls, func(in call) (*http.Response, error) {
				if hits(in) {
					return tc.answer(in)
				}
				return formAnswers(nil).handler(t)(in)
			})
			clock := time.Now()
			t.Cleanup(limiters.Replace(tokenValue, ratelimit.New(0, func() time.Time { return clock },
				func(context.Context, time.Duration) error { return nil })))
			_, err := env.invoke(c.tool, "two", c.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), formMissing) ||
				isUnsupported(err) != tc.unsupported || classOf(err) != tc.class || countMethod(calls, method) != 1 {
				t.Errorf("%s %s: err = %v (class %q)", c.tool, name, err, classOf(err))
			}
		}
	}
}

func TestFormToolsAreUnsupportedWithoutThePluginAndSendNothingFurther(t *testing.T) {
	for _, c := range formCalls {
		var calls []call
		env := newEnvironment(t, &calls, with(formAnswers(nil), answers{
			"API.isPluginActivated": fixed(`{"value":false}`),
		}).handler(t))
		_, err := env.invoke(c.tool, "two", c.arguments)
		if !isUnsupported(err) || methods(calls) != "API.getMatomoVersion API.isPluginActivated" ||
			lastCall(calls, "API.isPluginActivated").form.Get("pluginName") != formsPlugin {
			t.Errorf("%s: err = %v, requests = %s", c.tool, err, methods(calls))
		}
	}
}

func TestFormTextAndListsAreShortened(t *testing.T) {
	long := strings.Repeat("n", 6000)
	rules := strings.TrimSuffix(strings.Repeat(`{"attribute":"a","pattern":"p","value":"`+long+`"},`, 150), ",")
	fields := strings.TrimSuffix(strings.Repeat(`{"name":"f","type":"text","displayName":"`+long+`"},`, 250), ",")
	body := `{"idsiteform":2,"idsite":1,"name":"` + long + `","description":"` + long + `","match_form_rules":[` + rules +
		`],"fields":[` + fields + `]}`
	var calls []call
	env := newEnvironment(t, &calls, formAnswers(answers{
		"FormAnalytics.getForm":                    fixed(body),
		"FormAnalytics.getCurrentMostPopularForms": fixed(`[{"label":"` + long + `","idsiteform":2}]`),
		"FormAnalytics.getAllGoals":                fixed(`{"1":"` + long + `"}`),
		"FormAnalytics.getAutoCreationSettings":    fixed(`"` + long + `"`),
	}).handler(t))
	result, err := env.invoke("matomo.forms.get", "two", `{"idSite":1,"idForm":2}`)
	var form Form
	if err != nil || json.Unmarshal([]byte(result), &form) != nil || len(form.Name) != maxStringLength ||
		len(form.Description) != maxUrlLength || len(form.MatchFormRules) != maxFormRules ||
		len(form.MatchFormRules[0].Value) != maxUrlLength || len(form.Fields) != maxFormFields ||
		len(form.Fields[0].DisplayName) != maxStringLength {
		t.Errorf("result length %d, err = %v", len(result), err)
	}
	result, err = env.invoke("matomo.forms.realtime", "two", formCalls[2].arguments)
	var realtime FormsRealtimeResult
	if err != nil || json.Unmarshal([]byte(result), &realtime) != nil || len(realtime.Forms) != 1 ||
		len(realtime.Forms[0].Label) != maxStringLength {
		t.Errorf("result length %d, err = %v", len(result), err)
	}
	result, err = env.invoke("matomo.form-options.list", "two", formCalls[3].arguments)
	var options FormOptionsResult
	if err != nil || json.Unmarshal([]byte(result), &options) != nil || len(options.Goals) != 1 ||
		len(options.Goals[0].Name) != maxStringLength {
		t.Errorf("result length %d, err = %v", len(result), err)
	}
	if text, _ := options.AutoCreationSettings.(string); len(text) != maxUrlLength {
		t.Errorf("auto-creation text length %d", len(text))
	}

	var manyGoals strings.Builder
	manyGoals.WriteString("{")
	for i := 1; i <= 150; i++ {
		if i > 1 {
			manyGoals.WriteString(",")
		}
		manyGoals.WriteString(`"` + strconv.Itoa(i) + `":"g"`)
	}
	manyGoals.WriteString("}")
	env = newEnvironment(t, &calls, formAnswers(answers{"FormAnalytics.getAllGoals": fixed(manyGoals.String())}).handler(t))
	result, err = env.invoke("matomo.form-options.list", "two", formCalls[3].arguments)
	if err != nil || json.Unmarshal([]byte(result), &options) != nil || len(options.Goals) != maxResultEntries {
		t.Errorf("goals = %d, err = %v", len(options.Goals), err)
	}
}
