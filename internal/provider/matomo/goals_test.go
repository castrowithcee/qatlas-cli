package matomo

import (
	"context"
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

// goalsFixture follows Goals.getGoals for one website: an object keyed by goal ID, with a goal matched
// manually (Matomo leaves out its pattern, pattern type, and case sensitivity).
const goalsFixture = `{
"3":{"idsite":"1","idgoal":"3","name":"Newsletter","description":"Signup","match_attribute":"url",
 "pattern":"https://shop.example.test/thanks","pattern_type":"exact","case_sensitive":"0","allow_multiple":"0",
 "revenue":"5.5","deleted":"0","event_value_as_revenue":"0"},
"1":{"idsite":"1","idgoal":"1","name":"manual goal","description":"","match_attribute":"manually",
 "allow_multiple":"1","revenue":"0","deleted":"0","event_value_as_revenue":"0"},
"2":{"idsite":"1","idgoal":"2","name":"Clicks","description":"","match_attribute":"event_action","pattern":"click",
 "pattern_type":"contains","case_sensitive":"1","allow_multiple":"0","revenue":0,"deleted":"0",
 "event_value_as_revenue":"1"}
}`

const goalOne = `{"idsite":"1","idgoal":"3","name":"Newsletter","description":"Signup","match_attribute":"url",
"pattern":"https://shop.example.test/thanks","pattern_type":"exact","case_sensitive":"0","allow_multiple":"0",
"revenue":"5.5","deleted":"0","event_value_as_revenue":"0"}`

// goalConnections adds the connections that change goals to the connections of the other tests.
const goalConnections = `
  writer: {service: matomo, credential: token, effects: [read, create, update, delete, execute]}
  writer-two: {service: matomo, credential: token, effects: [read, create, update, execute], targets: [site/2]}
  deleter: {service: matomo, credential: token, tools: [matomo.goals.get, matomo.goals.delete], targets: [site/1]}
  reader: {service: matomo, credential: token, effects: [read]}
`

func newGoalsEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(goalConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func (e *environment) change(operation, connection, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: json.RawMessage(arguments), Confirmed: true,
	})
	return string(response.Result), err
}

func goalAnswers(extra answers) answers {
	return with(versionOnly("5.14.2"), with(answers{
		"API.isPluginActivated": func(call) *http.Response { return ok(`{"value":true}`) },
	}, extra))
}

func countMethod(calls []call, method string) int {
	n := 0
	for _, c := range calls {
		if c.api() == method {
			n++
		}
	}
	return n
}

func TestGoalToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
		confirm     capability.Confirmation
		allowList   bool
	}{
		"matomo.goals.list":   {capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone, false},
		"matomo.goals.get":    {capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone, false},
		"matomo.goals.add":    {capability.EffectCreate, capability.IdempotencyNonIdempotent, capability.ConfirmationRequired, false},
		"matomo.goals.update": {capability.EffectUpdate, capability.IdempotencyIdempotent, capability.ConfirmationRequired, false},
		"matomo.goals.delete": {capability.EffectDelete, capability.IdempotencyIdempotent, capability.ConfirmationRequired, true},
	}
	for id, tc := range want {
		d, _, ok := reg.Lookup(id)
		if !ok {
			t.Fatalf("%s is not registered", id)
		}
		if d.Risk.Effect != tc.effect || d.Risk.Idempotency != tc.idempotency || d.Risk.Confirmation != tc.confirm ||
			!d.Risk.OpenWorld || d.Risk.DataSensitivity == "" || d.RequiresToolAllowList != tc.allowList {
			t.Errorf("%s: risk = %+v, allow list = %t", id, d.Risk, d.RequiresToolAllowList)
		}
	}
	delete_, _, _ := reg.Lookup("matomo.goals.delete")
	for _, part := range []string{"conversion", "deleted for good", "no longer shown", "cannot be undone", "new ID"} {
		if !strings.Contains(delete_.Description, part) {
			t.Errorf("delete description lacks %q", part)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		if strings.Contains(strings.Join(profile.Tools, " "), "matomo.goals.delete") {
			t.Errorf("profile %s selects goals.delete", profile.ID)
		}
		if profile.ID == "goals" {
			joined := strings.Join(profile.Tools, " ")
			if profile.Recommended || !strings.Contains(joined, "matomo.goals.add") || !strings.Contains(joined, "matomo.goals.update") {
				t.Errorf("goals profile = %+v", profile)
			}
		}
	}
}

func TestGoalsListOrdersPagesAndReadsEveryField(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, goalAnswers(answers{
		"Goals.getGoals": func(call) *http.Response { return ok(goalsFixture) },
	}).handler(t))
	result, err := env.invoke("matomo.goals.list", "open", `{"idSite":1,"limit":2}`)
	if err != nil {
		t.Fatal(err)
	}
	var out GoalsResult
	_ = json.Unmarshal([]byte(result), &out)
	if out.IDSite != 1 || len(out.Goals) != 2 || out.Goals[0].ID != 1 || out.Goals[1].ID != 2 || !out.HasMore {
		t.Fatalf("out = %s", result)
	}
	first := out.Goals[0]
	if first.MatchAttribute != "manually" || first.Pattern != "" || first.CaseSensitive != nil ||
		first.AllowMultiple == nil || !*first.AllowMultiple || first.Revenue == nil || *first.Revenue != 0 {
		t.Errorf("manual goal = %+v", first)
	}
	if clicks := out.Goals[1]; clicks.EventValueAsRevenue == nil || !*clicks.EventValueAsRevenue ||
		clicks.CaseSensitive == nil || !*clicks.CaseSensitive || clicks.PatternType != "contains" {
		t.Errorf("event goal = %+v", clicks)
	}
	next, err := env.invoke("matomo.goals.list", "open", `{"idSite":1,"cursor":"`+out.NextCursor+`"}`)
	if err != nil || !strings.Contains(next, `"name":"Newsletter"`) || !strings.Contains(next, `"revenue":5.5`) ||
		strings.Contains(next, "next_cursor") || !strings.Contains(next, `"deleted":false`) {
		t.Fatalf("next = %s, err = %v", next, err)
	}
	if _, err := env.invoke("matomo.goals.list", "open", `{"idSite":1,"orderByName":true,"cursor":"`+out.NextCursor+`"}`); !isInvalidRequest(err) {
		t.Errorf("cursor of another order: err = %v", err)
	}
	byName, _ := env.invoke("matomo.goals.list", "open", `{"idSite":1,"orderByName":true}`)
	var named GoalsResult
	_ = json.Unmarshal([]byte(byName), &named)
	if len(named.Goals) != 3 || named.Goals[0].Name != "Clicks" || named.Goals[1].Name != "manual goal" || named.Goals[2].Name != "Newsletter" {
		t.Errorf("by name = %s", byName)
	}
	last := calls[len(calls)-1]
	if last.api() != "Goals.getGoals" || last.form.Get("idSite") != "1" || last.form.Get("orderByName") != "1" {
		t.Errorf("request = %v", last.form)
	}
	if n := countMethod(calls, "API.isPluginActivated"); n != 1 {
		t.Errorf("plugin was asked %d times, want once", n)
	}
	if empty, err := (func() (string, error) {
		var c []call
		e := newGoalsEnvironment(t, &c, goalAnswers(answers{"Goals.getGoals": func(call) *http.Response { return ok(`[]`) }}).handler(t))
		return e.invoke("matomo.goals.list", "open", `{"idSite":1}`)
	})(); err != nil || empty != `{"has_more":false,"idSite":1}` {
		t.Errorf("empty = %s, err = %v", empty, err)
	}
}

func TestGoalsListDropsRowsOfAnotherWebsiteAndBoundsText(t *testing.T) {
	var calls []call
	long := strings.Repeat("n", 900)
	env := newGoalsEnvironment(t, &calls, goalAnswers(answers{
		"Goals.getGoals": func(call) *http.Response {
			return ok(`[{"idsite":"9","idgoal":"4","name":"other"},{"idsite":"1","idgoal":"5","name":"` + long + `"},{"idgoal":"x"}]`)
		},
	}).handler(t))
	result, err := env.invoke("matomo.goals.list", "open", `{"idSite":1}`)
	var out GoalsResult
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || len(out.Goals) != 1 || out.Goals[0].ID != 5 || len(out.Goals[0].Name) != maxStringLength {
		t.Fatalf("result = %.200s, err = %v", result, err)
	}
}

func TestGoalsGetReadsOneGoalOrIsNotFound(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, goalAnswers(answers{
		"Goals.getGoal": func(c call) *http.Response {
			switch c.form.Get("idGoal") {
			case "3":
				return ok(goalOne)
			case "4":
				return ok(`{"idsite":"1","idgoal":"7","name":"other"}`)
			case "5":
				return ok(`{"result":"success","message":"ok"}`)
			}
			return ok(`null`)
		},
	}).handler(t))
	result, err := env.invoke("matomo.goals.get", "open", `{"idSite":1,"idGoal":3}`)
	var out GoalResult
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || out.IDSite != 1 || out.ID != 3 || out.Pattern != "https://shop.example.test/thanks" ||
		out.PatternType != "exact" || out.Revenue == nil || *out.Revenue != 5.5 {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if last := calls[len(calls)-1]; last.form.Get("idSite") != "1" || last.form.Get("idGoal") != "3" {
		t.Errorf("request = %v", last.form)
	}
	if _, err := env.invoke("matomo.goals.get", "open", `{"idSite":1,"idGoal":5}`); classOf(err) != provider.ClassNotFound {
		t.Errorf("unknown goal: err = %v, want not-found", err)
	}
	if _, err := env.invoke("matomo.goals.get", "open", `{"idSite":1,"idGoal":4}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("another goal: err = %v, want invalid-provider-response", err)
	}
	if _, err := env.invoke("matomo.goals.get", "open", `{"idSite":1,"idGoal":6}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("null answer: err = %v", err)
	}
	for _, bad := range []string{`{"idSite":1}`, `{"idSite":1,"idGoal":0}`, `{"idSite":1,"idGoal":"ecommerceOrder"}`} {
		if _, err := env.invoke("matomo.goals.get", "open", bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestGoalsAddSendsOneConfirmedRequestAndReadsTheNewID(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, goalAnswers(answers{
		"Goals.addGoal": func(call) *http.Response { return ok(`{"value":7}`) },
	}).handler(t))
	args := `{"idSite":1,"name":"Newsletter","matchAttribute":"url","pattern":"https://shop.example.test/thanks",` +
		`"patternType":"exact","caseSensitive":true,"revenue":5.5,"allowMultipleConversionsPerVisit":true,"description":"Signup"}`
	if _, err := env.invoke("matomo.goals.add", "writer", args); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("without confirm: err = %v", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("an unconfirmed call caused %d requests and %d secret reads", len(calls), *env.reads)
	}
	result, err := env.change("matomo.goals.add", "writer", args)
	if err != nil || result != `{"created":true,"idGoal":7,"idSite":1}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if countMethod(calls, "Goals.addGoal") != 1 {
		t.Fatalf("addGoal was sent %d times", countMethod(calls, "Goals.addGoal"))
	}
	sent := calls[len(calls)-1]
	for name, want := range map[string]string{"idSite": "1", "name": "Newsletter", "matchAttribute": "url",
		"pattern": "https://shop.example.test/thanks", "patternType": "exact", "caseSensitive": "1", "revenue": "5.5",
		"allowMultipleConversionsPerVisit": "1", "description": "Signup", "useEventValueAsRevenue": "0"} {
		if sent.form.Get(name) != want {
			t.Errorf("%s = %q, want %q", name, sent.form.Get(name), want)
		}
	}
	if sent.auth != "Bearer "+tokenValue || sent.form.Has("token_auth") || strings.Contains(sent.rawQuery, tokenValue) {
		t.Errorf("token handling: %+v", sent)
	}
	manual, err := env.change("matomo.goals.add", "writer", `{"idSite":1,"name":"Manual","matchAttribute":"manually"}`)
	if err != nil || !strings.Contains(manual, `"idGoal":7`) {
		t.Fatalf("manual = %s, err = %v", manual, err)
	}
	if last := calls[len(calls)-1]; last.form.Get("pattern") != "" || last.form.Get("patternType") != "" || !last.form.Has("pattern") {
		t.Errorf("manual request = %v", last.form)
	}
	if _, err := env.change("matomo.goals.add", "reader", args); err == nil {
		t.Error("a read connection offered goals.add")
	}
}

func TestGoalsDefinitionIsCheckedBeforeAnyRequest(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, goalAnswers(answers{}).handler(t))
	for name, args := range map[string]string{
		"no name":                  `{"idSite":1,"matchAttribute":"manually"}`,
		"long name":                `{"idSite":1,"name":"` + strings.Repeat("n", 51) + `","matchAttribute":"manually"}`,
		"unknown attribute":        `{"idSite":1,"name":"a","matchAttribute":"referrer","pattern":"x","patternType":"exact"}`,
		"no pattern":               `{"idSite":1,"name":"a","matchAttribute":"title","patternType":"contains"}`,
		"no pattern type":          `{"idSite":1,"name":"a","matchAttribute":"title","pattern":"x"}`,
		"manual with pattern":      `{"idSite":1,"name":"a","matchAttribute":"manually","pattern":"x"}`,
		"duration type":            `{"idSite":1,"name":"a","matchAttribute":"visit_duration","pattern":"2","patternType":"exact"}`,
		"duration number":          `{"idSite":1,"name":"a","matchAttribute":"visit_duration","pattern":"long","patternType":"greater_than"}`,
		"greater than for strings": `{"idSite":1,"name":"a","matchAttribute":"title","pattern":"x","patternType":"greater_than"}`,
		"negative revenue":         `{"idSite":1,"name":"a","matchAttribute":"manually","revenue":-1}`,
		"event value on url":       `{"idSite":1,"name":"a","matchAttribute":"url","pattern":"http://x","patternType":"contains","useEventValueAsRevenue":true}`,
		"unknown argument":         `{"idSite":1,"name":"a","matchAttribute":"manually","idGoal":1}`,
	} {
		if _, err := env.change("matomo.goals.add", "writer", args); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := env.change("matomo.goals.update", "writer", `{"idSite":1,"name":"a","matchAttribute":"manually"}`); err == nil {
		t.Error("update without idGoal accepted")
	}
	if len(calls) != 0 {
		t.Errorf("refused arguments caused %d requests", len(calls))
	}
}

func TestGoalsUpdateReplacesTheDefinitionWithoutAPriorRead(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, goalAnswers(answers{
		"Goals.updateGoal": func(c call) *http.Response {
			if c.form.Get("idGoal") == "9" {
				return failure("There is no goal with id '9' for site with id '1'." + bodyCanary)
			}
			return ok(`{"result":"success","message":"ok"}`)
		},
	}).handler(t))
	args := `{"idSite":1,"idGoal":3,"name":"Renamed","matchAttribute":"event_action","pattern":"click",` +
		`"patternType":"contains","useEventValueAsRevenue":true}`
	result, err := env.change("matomo.goals.update", "writer", args)
	if err != nil || result != `{"idGoal":3,"idSite":1,"updated":true}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	sent := calls[len(calls)-1]
	if sent.form.Get("idGoal") != "3" || sent.form.Get("idSite") != "1" || sent.form.Get("useEventValueAsRevenue") != "1" ||
		sent.form.Get("revenue") != "0" || sent.form.Get("caseSensitive") != "0" {
		t.Errorf("request = %v", sent.form)
	}
	if countMethod(calls, "Goals.getGoal") != 0 {
		t.Error("update read the goal first")
	}
	_, err = env.change("matomo.goals.update", "writer", strings.Replace(args, `"idGoal":3`, `"idGoal":9`, 1))
	if classOf(err) != provider.ClassNotFound || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("unknown goal: err = %v, want not-found", err)
	}
}

func TestGoalsDeleteReadsFirstAndIsOnlyOfferedByTheToolsList(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, goalAnswers(answers{
		"Goals.getGoal": func(c call) *http.Response {
			if c.form.Get("idGoal") == "3" {
				return ok(goalOne)
			}
			return ok(`{"result":"success","message":"ok"}`)
		},
		"Goals.deleteGoal": func(call) *http.Response { return ok(`{"result":"success","message":"ok"}`) },
	}).handler(t))
	if _, err := env.change("matomo.goals.delete", "writer", `{"idSite":1,"idGoal":3}`); err == nil {
		t.Fatal("an effects connection offered goals.delete")
	}
	if len(calls) != 0 {
		t.Fatalf("a tool that is not offered caused %d requests", len(calls))
	}
	if _, err := env.invoke("matomo.goals.delete", "deleter", `{"idSite":1,"idGoal":3}`); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("without confirm: err = %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("an unconfirmed delete caused %d requests", len(calls))
	}
	result, err := env.change("matomo.goals.delete", "deleter", `{"idSite":1,"idGoal":3}`)
	if err != nil || result != `{"deleted":true,"idGoal":3,"idSite":1,"name":"Newsletter"}` {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	var methods []string
	for _, c := range calls {
		methods = append(methods, c.api())
	}
	if got := strings.Join(methods, " "); got != "API.getMatomoVersion API.isPluginActivated Goals.getGoal Goals.deleteGoal" {
		t.Errorf("requests = %s", got)
	}
	before := len(calls)
	if _, err := env.change("matomo.goals.delete", "deleter", `{"idSite":1,"idGoal":8}`); classOf(err) != provider.ClassNotFound {
		t.Errorf("unknown goal: err = %v, want not-found", err)
	}
	if countMethod(calls[before:], "Goals.deleteGoal") != 0 {
		t.Error("a goal the website does not have still reached deleteGoal")
	}
}

func TestGoalsForeignWebsiteIsRefusedBeforeSecretAndRequest(t *testing.T) {
	for _, tool := range []struct{ name, args string }{
		{"matomo.goals.list", `{"idSite":1}`},
		{"matomo.goals.get", `{"idSite":1,"idGoal":3}`},
		{"matomo.goals.add", `{"idSite":1,"name":"a","matchAttribute":"manually"}`},
		{"matomo.goals.update", `{"idSite":1,"idGoal":3,"name":"a","matchAttribute":"manually"}`},
		{"matomo.goals.delete", `{"idSite":1,"idGoal":3}`},
	} {
		var calls []call
		env := newGoalsEnvironment(t, &calls, goalAnswers(answers{}).handler(t))
		connection := "writer-two"
		if tool.name == "matomo.goals.delete" {
			connection = "deleter"
			tool.args = `{"idSite":2,"idGoal":3}`
		}
		_, err := env.change(tool.name, connection, tool.args)
		if err == nil || !isInvalidRequest(err) || strings.Contains(err.Error(), "site/") {
			t.Errorf("%s: err = %v, want an invalid request that names no target", tool.name, err)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: %d requests and %d secret reads before the refusal", tool.name, len(calls), *env.reads)
		}
	}
}

func TestGoalErrorsMapToClassesWithoutTheMatomoText(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		class         provider.Class
		unsupported   bool
	}{
		{"permission", "You can't access this resource as it requires 'write' access for the website id = 1.", provider.ClassPermission, false},
		{"unknown website", "An unexpected website was found in the request: website id was set to '1' .", provider.ClassNotFound, false},
		{"method missing", "The method 'Goals.addGoal' does not exist or is not available in the module 'Goals'.", "", true},
		{"plugin inactive", "The plugin Goals is not activated.", "", true},
		{"unknown text", "Something went wrong", provider.ClassInvalidResponse, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []call
			env := newGoalsEnvironment(t, &calls, goalAnswers(answers{
				"Goals.addGoal": func(call) *http.Response { return failure(tc.message + bodyCanary) },
			}).handler(t))
			_, err := env.change("matomo.goals.add", "writer", `{"idSite":1,"name":"a","matchAttribute":"manually"}`)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || tc.unsupported != isUnsupported(err) ||
				classOf(err) != tc.class {
				t.Fatalf("err = %v (class %q), want class %q unsupported %t", err, classOf(err), tc.class, tc.unsupported)
			}
			if countMethod(calls, "Goals.addGoal") != 1 {
				t.Errorf("addGoal was sent %d times, want once", countMethod(calls, "Goals.addGoal"))
			}
		})
	}
}

func TestInactivePluginRefusesGoalToolsWithoutFurtherRequests(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"API.isPluginActivated": func(call) *http.Response { return ok(`{"value":false}`) },
	}).handler(t))
	_, err := env.change("matomo.goals.add", "writer", `{"idSite":1,"name":"a","matchAttribute":"manually"}`)
	if !isUnsupported(err) || !strings.Contains(err.Error(), "Goals") || len(calls) != 2 {
		t.Fatalf("err = %v, calls = %d", err, len(calls))
	}
}

func TestUnclearChangeIsNotRepeatedAndSaysSo(t *testing.T) {
	const add = `{"idSite":1,"name":"a","matchAttribute":"manually"}`
	for name, handler := range map[string]func(call) *http.Response{
		"html":       func(call) *http.Response { return ok(`<html>` + bodyCanary + `</html>`) },
		"no ID":      func(call) *http.Response { return ok(`{"value":"none"}`) },
		"server":     func(call) *http.Response { return respond(500, bodyCanary) },
		"redirected": func(call) *http.Response { return respond(302, "") },
	} {
		var calls []call
		env := newGoalsEnvironment(t, &calls, goalAnswers(answers{"Goals.addGoal": handler}).handler(t))
		_, err := env.change("matomo.goals.add", "writer", add)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || !strings.Contains(err.Error(), "never repeated") ||
			countMethod(calls, "Goals.addGoal") != 1 {
			t.Errorf("%s: err = %v, addGoal sent %d times", name, err, countMethod(calls, "Goals.addGoal"))
		}
	}
	var calls []call
	env := newGoalsEnvironment(t, &calls, func(c call) (*http.Response, error) {
		if c.api() == "Goals.updateGoal" {
			return nil, context.DeadlineExceeded
		}
		return goalAnswers(answers{}).handler(t)(c)
	})
	_, err := env.change("matomo.goals.update", "writer", `{"idSite":1,"idGoal":3,"name":"a","matchAttribute":"manually"}`)
	if classOf(err) != provider.ClassTimeout || !strings.Contains(err.Error(), "never repeated") ||
		countMethod(calls, "Goals.updateGoal") != 1 {
		t.Errorf("timeout: err = %v, updateGoal sent %d times", err, countMethod(calls, "Goals.updateGoal"))
	}
	// A confirmation Qatlas cannot read after an update is unclear too.
	var calls2 []call
	env2 := newGoalsEnvironment(t, &calls2, goalAnswers(answers{
		"Goals.updateGoal": func(call) *http.Response { return ok(`{"value":3}`) },
	}).handler(t))
	if _, err := env2.change("matomo.goals.update", "writer", `{"idSite":1,"idGoal":3,"name":"a","matchAttribute":"manually"}`); err == nil ||
		!strings.Contains(err.Error(), "never repeated") {
		t.Errorf("unconfirmed update: err = %v", err)
	}
}

func TestRateLimitedChangeIsACleanRefusal(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, func(c call) (*http.Response, error) {
		if c.api() == "Goals.addGoal" {
			return respond(429, ""), nil
		}
		return goalAnswers(answers{}).handler(t)(c)
	})
	_, err := env.change("matomo.goals.add", "writer", `{"idSite":1,"name":"a","matchAttribute":"manually"}`)
	if classOf(err) != provider.ClassRateLimited || strings.Contains(err.Error(), "never repeated") ||
		countMethod(calls, "Goals.addGoal") != 1 {
		t.Errorf("err = %v, addGoal sent %d times", err, countMethod(calls, "Goals.addGoal"))
	}
}
