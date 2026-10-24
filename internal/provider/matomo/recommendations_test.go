package matomo

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// scanFixture follows Goals.runGoalRecommendationScan, including the development-only debug member.
const scanFixture = `{"mode":"deterministic","goals":[{"id":"url:thank-you","name":"Thank you page","category":"purchase",
"matchAttribute":"url","pattern":"/thank-you","patternType":"contains","caseSensitive":false,
"allowMultipleConversionsPerVisit":false,"revenue":0,"useEventValueAsRevenue":false,"reason":"Orders end here",
"description":"Orders end here","source":"link","needsSetup":false,"implementationNote":"note",
"evidence":["a","b"],"sourcePages":["https://shop.example.test/"],"priority":1}],
"manualGoals":[{"name":"Form","howTo":"Track it","category":"event"}],
"warnings":[{"type":"fewConversions","severity":"info","message":"Few"}],"aiError":null,"generatedAt":1790000000,
"remainingAiScans":null,"providerName":"AI","aiAvailability":"notConfigured","privacyNote":"note",
"debug":{"url":"https://shop.example.test/"}}`

const savedFixture = `{"mode":"deterministic","goals":[],"manualGoals":[],"useAi":false,"generatedAt":1790000000,
"remainingAiScans":3,"providerName":"AI","aiAvailability":"available","privacyNote":"note"}`

func recommendationToolsAnswers(extra answers) answers { return goalAnswers(extra) }

func TestGoalRecommendationsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
		confirm     capability.Confirmation
	}{
		"matomo.goal-recommendations.list":    {capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone},
		"matomo.goal-recommendations.scan":    {capability.EffectExecute, capability.IdempotencyNonIdempotent, capability.ConfirmationRequired},
		"matomo.goal-recommendations.dismiss": {capability.EffectUpdate, capability.IdempotencyIdempotent, capability.ConfirmationRequired},
	}
	for id, tc := range want {
		d, _, ok := reg.Lookup(id)
		if !ok {
			t.Fatalf("%s is not registered", id)
		}
		if d.Risk.Effect != tc.effect || d.Risk.Idempotency != tc.idempotency || d.Risk.Confirmation != tc.confirm ||
			!d.Risk.OpenWorld || d.Risk.DataSensitivity == "" || d.RequiresToolAllowList {
			t.Errorf("%s: risk = %+v", id, d.Risk)
		}
		if strings.Contains(strings.ToLower(string(d.InputSchema)), "useai") ||
			!strings.Contains(string(d.InputSchema), `"additionalProperties":false`) {
			t.Errorf("%s: input schema %s", id, d.InputSchema)
		}
		for _, a := range d.Arguments {
			if strings.EqualFold(a.Name, "useAi") || strings.Contains(strings.ToLower(a.Name), "ai") && a.Name != "all" {
				t.Errorf("%s offers the argument %s", id, a.Name)
			}
		}
	}
	scan, _, _ := reg.Lookup("matomo.goal-recommendations.scan")
	for _, part := range []string{"fetches", "50", "replaces the saved", "no AI provider", "Not idempotent", "never repeated"} {
		if !strings.Contains(scan.Description, part) {
			t.Errorf("scan description lacks %q", part)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		joined := strings.Join(profile.Tools, " ")
		inGoals := strings.Contains(joined, "goal-recommendations.scan") && strings.Contains(joined, "goal-recommendations.dismiss") &&
			strings.Contains(joined, "goal-recommendations.list")
		if inGoals != (profile.ID == "goals") || (profile.ID != "goals" && strings.Contains(joined, "goal-recommendations")) {
			t.Errorf("profile %s = %s", profile.ID, joined)
		}
	}
}

func TestGoalRecommendationsScanSendsOneRequestWithoutAIAndReadsEveryField(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, recommendationToolsAnswers(answers{
		"Goals.runGoalRecommendationScan": func(call) *http.Response { return ok(scanFixture) },
	}).handler(t))
	if _, err := env.invoke("matomo.goal-recommendations.scan", "writer", `{"idSite":1}`); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("without confirm: err = %v", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("an unconfirmed scan caused %d requests", len(calls))
	}
	for _, arg := range []string{`{"idSite":1,"useAi":true}`, `{"idSite":1,"useAi":false}`, `{"idSite":1,"use_ai":true}`} {
		if _, err := env.change("matomo.goal-recommendations.scan", "writer", arg); err == nil {
			t.Errorf("%s accepted", arg)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("an AI argument caused %d requests", len(calls))
	}
	result, err := env.change("matomo.goal-recommendations.scan", "writer", `{"idSite":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var out Recommendations
	_ = json.Unmarshal([]byte(result), &out)
	if out.IDSite != 1 || out.Mode != "deterministic" || len(out.Goals) != 1 || out.Goals[0]["id"] != "url:thank-you" ||
		out.Goals[0]["needsSetup"] != false || len(out.ManualGoals) != 1 || len(out.Warnings) != 1 ||
		out.GeneratedAt == nil || *out.GeneratedAt != 1790000000 || out.RemainingAIScans != nil ||
		out.AIAvailability != "notConfigured" || out.ProviderName != "AI" || out.PrivacyNote != "note" ||
		strings.Contains(result, "debug") || strings.Contains(result, "aiError") {
		t.Fatalf("result = %s", result)
	}
	if countMethod(calls, "Goals.runGoalRecommendationScan") != 1 {
		t.Fatalf("scan sent %d times", countMethod(calls, "Goals.runGoalRecommendationScan"))
	}
	sent := calls[len(calls)-1]
	for name := range sent.form {
		if strings.Contains(strings.ToLower(name), "ai") && name != "format" {
			t.Errorf("request carries %s", name)
		}
	}
	if sent.form.Has("useAi") || sent.form.Get("idSite") != "1" || sent.auth != "Bearer "+tokenValue ||
		sent.form.Has("token_auth") || strings.Contains(sent.rawQuery, tokenValue) {
		t.Errorf("request = %+v", sent)
	}
}

func TestGoalRecommendationsScanDoesNotRepeatAfterAnUnclearResult(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, recommendationToolsAnswers(answers{
		"Goals.runGoalRecommendationScan": func(call) *http.Response { return ok(`<html>` + bodyCanary + `</html>`) },
	}).handler(t))
	_, err := env.change("matomo.goal-recommendations.scan", "writer", `{"idSite":1}`)
	if err == nil || classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "never repeated") ||
		strings.Contains(err.Error(), bodyCanary) {
		t.Fatalf("err = %v", err)
	}
	if countMethod(calls, "Goals.runGoalRecommendationScan") != 1 {
		t.Errorf("scan sent %d times", countMethod(calls, "Goals.runGoalRecommendationScan"))
	}
}

func TestGoalRecommendationsListIsAReadOfSavedResults(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, recommendationToolsAnswers(answers{
		"Goals.getSavedRecommendedGoals": func(call) *http.Response { return ok(savedFixture) },
	}).handler(t))
	result, err := env.invoke("matomo.goal-recommendations.list", "reader", `{"idSite":1}`)
	var out Recommendations
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || out.UseAI == nil || *out.UseAI || out.RemainingAIScans == nil || *out.RemainingAIScans != 3 ||
		out.GeneratedAt == nil || out.AIAvailability != "available" {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if countMethod(calls, "Goals.runGoalRecommendationScan") != 0 {
		t.Error("list started a scan")
	}
	empty := newGoalsEnvironment(t, &calls, recommendationToolsAnswers(answers{
		"Goals.getSavedRecommendedGoals": func(call) *http.Response {
			return ok(`{"mode":null,"goals":[],"manualGoals":[],"useAi":false,"generatedAt":null,"remainingAiScans":null}`)
		},
	}).handler(t))
	if result, err := empty.invoke("matomo.goal-recommendations.list", "reader", `{"idSite":1}`); err != nil ||
		strings.Contains(result, "generatedAt") {
		t.Errorf("empty = %s, err = %v", result, err)
	}
}

func TestGoalRecommendationsDismissOneOrAll(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, recommendationToolsAnswers(answers{
		"Goals.dismissRecommendedGoal": func(c call) *http.Response {
			return ok(map[bool]string{true: `{"success":true}`, false: `{"success":false}`}[c.form.Get("recommendationId") == "url:thank-you"])
		},
		"Goals.dismissRecommendedGoals": func(call) *http.Response { return ok(`{"success":true}`) },
	}).handler(t))
	if _, err := env.invoke("matomo.goal-recommendations.dismiss", "writer", `{"idSite":1,"all":true}`); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("without confirm: err = %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("unconfirmed call caused %d requests", len(calls))
	}
	result, err := env.change("matomo.goal-recommendations.dismiss", "writer", `{"idSite":1,"recommendation_id":"url:thank-you"}`)
	if err != nil || result != `{"dismissed":true,"idSite":1,"recommendation_id":"url:thank-you"}` {
		t.Fatalf("one = %s, err = %v", result, err)
	}
	if last := calls[len(calls)-1]; last.api() != "Goals.dismissRecommendedGoal" || last.form.Get("recommendationId") != "url:thank-you" {
		t.Errorf("request = %v", last.form)
	}
	result, err = env.change("matomo.goal-recommendations.dismiss", "writer", `{"idSite":1,"recommendation_id":"url:other"}`)
	if err != nil || !strings.Contains(result, `"dismissed":false`) {
		t.Fatalf("unknown = %s, err = %v", result, err)
	}
	result, err = env.change("matomo.goal-recommendations.dismiss", "writer", `{"idSite":1,"all":true}`)
	if err != nil || result != `{"all":true,"dismissed":true,"idSite":1}` {
		t.Fatalf("all = %s, err = %v", result, err)
	}
	if last := calls[len(calls)-1]; last.api() != "Goals.dismissRecommendedGoals" || last.form.Has("recommendationId") {
		t.Errorf("request = %v", last.form)
	}
	before := len(calls)
	for name, args := range map[string]string{
		"neither":        `{"idSite":1}`,
		"all false":      `{"idSite":1,"all":false}`,
		"both":           `{"idSite":1,"all":true,"recommendation_id":"url:x"}`,
		"no attribute":   `{"idSite":1,"recommendation_id":"thank-you"}`,
		"unknown attr":   `{"idSite":1,"recommendation_id":"referrer:x"}`,
		"empty pattern":  `{"idSite":1,"recommendation_id":"url:"}`,
		"control":        `{"idSite":1,"recommendation_id":"url:a\nb"}`,
		"padded":         `{"idSite":1,"recommendation_id":" url:a"}`,
		"too long":       `{"idSite":1,"recommendation_id":"url:` + strings.Repeat("a", 300) + `"}`,
		"unknown member": `{"idSite":1,"all":true,"useAi":true}`,
	} {
		if _, err := env.change("matomo.goal-recommendations.dismiss", "writer", args); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if len(calls) != before {
		t.Errorf("refused arguments caused %d requests", len(calls)-before)
	}
}

func TestGoalRecommendationsForeignWebsiteAndFeatureErrors(t *testing.T) {
	for _, tool := range []string{"matomo.goal-recommendations.list", "matomo.goal-recommendations.scan",
		"matomo.goal-recommendations.dismiss"} {
		var calls []call
		env := newGoalsEnvironment(t, &calls, recommendationToolsAnswers(answers{}).handler(t))
		_, err := env.change(tool, "writer-two", `{"idSite":1,"all":true}`)
		if tool != "matomo.goal-recommendations.dismiss" {
			_, err = env.change(tool, "writer-two", `{"idSite":1}`)
		}
		if err == nil || !isInvalidRequest(err) || strings.Contains(err.Error(), "site/") || len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: err = %v, %d requests, %d reads", tool, err, len(calls), *env.reads)
		}
	}
	var calls []call
	env := newGoalsEnvironment(t, &calls, recommendationToolsAnswers(answers{
		"Goals.getSavedRecommendedGoals": func(call) *http.Response {
			return failure("Goal recommendations are not enabled on this Matomo instance." + bodyCanary)
		},
		"Goals.runGoalRecommendationScan": func(call) *http.Response {
			return failure("You can't access this resource as it requires 'write' access for the website id = 1.")
		},
	}).handler(t))
	_, err := env.invoke("matomo.goal-recommendations.list", "reader", `{"idSite":1}`)
	if !isUnsupported(err) || strings.Contains(err.Error(), bodyCanary) {
		t.Errorf("flag off: err = %v", err)
	}
	if _, err := env.change("matomo.goal-recommendations.scan", "writer", `{"idSite":1}`); classOf(err) != provider.ClassPermission {
		t.Errorf("view token: err = %v", err)
	}
}
