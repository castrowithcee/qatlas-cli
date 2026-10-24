package matomo

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// dimensionsFixture follows CustomDimensions.getConfiguredCustomDimensions: IDs and flags arrive as strings or
// booleans. The last row names another website.
const dimensionsFixture = `[
{"idcustomdimension":"2","idsite":"1","index":"1","scope":"action","name":"Article","description":"d",
 "active":true,"extractions":[{"dimension":"url","pattern":"a_(.+).html"}],"case_sensitive":false},
{"idcustomdimension":"1","idsite":"1","index":"1","scope":"visit","name":"Customer","description":"",
 "active":false,"extractions":[],"case_sensitive":true},
{"idcustomdimension":"7","idsite":"2","index":"1","scope":"visit","name":"Foreign","active":true,"extractions":[]}
]`

func dimensionAnswers(extra answers) answers {
	return with(versionOnly("5.14.2"), with(answers{
		"API.isPluginActivated": func(call) *http.Response { return ok(`{"value":true}`) },
	}, extra))
}

func TestDimensionToolsAreRegisteredWithTheirRisk(t *testing.T) {
	reg := registry(t)
	want := map[string]struct {
		effect      capability.Effect
		idempotency capability.Idempotency
		confirm     capability.Confirmation
	}{
		"matomo.dimensions.list":        {capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone},
		"matomo.dimension-options.list": {capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone},
		"matomo.dimensions.add":         {capability.EffectCreate, capability.IdempotencyNonIdempotent, capability.ConfirmationRequired},
		"matomo.dimensions.update":      {capability.EffectUpdate, capability.IdempotencyIdempotent, capability.ConfirmationRequired},
	}
	for id, tc := range want {
		d, _, found := reg.Lookup(id)
		if !found {
			t.Fatalf("%s is not registered", id)
		}
		if d.Risk.Effect != tc.effect || d.Risk.Idempotency != tc.idempotency || d.Risk.Confirmation != tc.confirm ||
			!d.Risk.OpenWorld || d.Risk.DataSensitivity == "" || d.RequiresToolAllowList {
			t.Errorf("%s: risk = %+v", id, d.Risk)
		}
	}
	update, _, _ := reg.Lookup("matomo.dimensions.update")
	for _, part := range []string{"active=false pauses", "no way to delete", "scope cannot be changed"} {
		if !strings.Contains(update.Description, part) {
			t.Errorf("update description lacks %q", part)
		}
	}
	if _, _, found := reg.Lookup("matomo.dimensions.delete"); found {
		t.Error("a delete tool is registered")
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		joined := strings.Join(profile.Tools, " ")
		switch profile.ID {
		case "read":
			if !strings.Contains(joined, "matomo.dimensions.list") || strings.Contains(joined, "matomo.dimension-options.list") ||
				strings.Contains(joined, "matomo.dimensions.add") {
				t.Errorf("read profile = %v", profile.Tools)
			}
		case "dimensions":
			if profile.Recommended || !strings.Contains(joined, "matomo.dimensions.add") ||
				!strings.Contains(joined, "matomo.dimensions.update") || !strings.Contains(joined, "matomo.dimensions.list") ||
				!strings.Contains(joined, "matomo.dimension-options.list") {
				t.Errorf("dimensions profile = %+v", profile)
			}
		}
	}
}

func TestDimensionsListReadsTheWebsiteOnly(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, dimensionAnswers(answers{
		"CustomDimensions.getConfiguredCustomDimensions": func(call) *http.Response { return ok(dimensionsFixture) },
	}).handler(t))
	result, err := env.invoke("matomo.dimensions.list", "open", `{"idSite":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var out DimensionsResult
	_ = json.Unmarshal([]byte(result), &out)
	if out.IDSite != 1 || len(out.Dimensions) != 2 || out.Dimensions[0].IDDimension != 1 || out.Dimensions[1].IDDimension != 2 {
		t.Fatalf("out = %s", result)
	}
	second := out.Dimensions[1]
	if second.Scope != "action" || second.Active == nil || !*second.Active || second.CaseSensitive == nil || *second.CaseSensitive ||
		len(second.Extractions) != 1 || second.Extractions[0].Pattern != "a_(.+).html" || second.Index == nil || *second.Index != 1 ||
		out.Dimensions[0].Active == nil || *out.Dimensions[0].Active {
		t.Errorf("second = %+v", second)
	}
	if sent := calls[len(calls)-1]; sent.form.Get("idSite") != "1" || sent.auth != "Bearer "+tokenValue ||
		strings.Contains(sent.rawQuery, tokenValue) {
		t.Errorf("request = %+v", sent)
	}
}

func TestDimensionOptionsListReadsScopesAndSources(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, dimensionAnswers(answers{
		"CustomDimensions.getAvailableScopes": func(call) *http.Response {
			return ok(`[{"value":"visit","name":"Visit","numSlotsAvailable":5,"numSlotsUsed":1,"numSlotsLeft":4,"supportsExtractions":false},` +
				`{"value":"action","name":"Action","numSlotsAvailable":5,"numSlotsUsed":5,"numSlotsLeft":0,"supportsExtractions":true}]`)
		},
		"CustomDimensions.getAvailableExtractionDimensions": func(call) *http.Response {
			return ok(`[{"value":"url","name":"Page URL"},{"value":"urlparam","name":"Param"},{"value":"action_name","name":"Title"}]`)
		},
	}).handler(t))
	result, err := env.invoke("matomo.dimension-options.list", "open", `{"idSite":1}`)
	var out DimensionOptionsResult
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || len(out.Scopes) != 2 || out.Scopes[1].NumSlotsLeft == nil || *out.Scopes[1].NumSlotsLeft != 0 ||
		out.Scopes[1].SupportsExtractions == nil || !*out.Scopes[1].SupportsExtractions || len(out.ExtractionDimensions) != 3 {
		t.Fatalf("result = %s, err = %v", result, err)
	}
}

func TestDimensionsAddSendsOneConfirmedRequest(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, dimensionAnswers(answers{
		"CustomDimensions.configureNewCustomDimension": func(call) *http.Response { return ok(`3`) },
	}).handler(t))
	args := `{"idSite":1,"name":"Article","scope":"action","active":true,` +
		`"extractions":[{"dimension":"url","pattern":"a_(.+).html"}],"caseSensitive":false,"description":"d"}`
	if _, err := env.invoke("matomo.dimensions.add", "writer", args); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("without confirm: err = %v", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("an unconfirmed call caused %d requests and %d secret reads", len(calls), *env.reads)
	}
	result, err := env.change("matomo.dimensions.add", "writer", args)
	var out DimensionAdded
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || !out.Created || out.IDSite != 1 || out.IDDimension != 3 {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if countMethod(calls, "CustomDimensions.configureNewCustomDimension") != 1 {
		t.Fatalf("add was sent %d times", countMethod(calls, "CustomDimensions.configureNewCustomDimension"))
	}
	sent := calls[len(calls)-1]
	for name, want := range map[string]string{"idSite": "1", "name": "Article", "scope": "action", "active": "1",
		"caseSensitive": "0", "description": "d", "extractions[0][dimension]": "url", "extractions[0][pattern]": "a_(.+).html"} {
		if sent.form.Get(name) != want {
			t.Errorf("%s = %q, want %q", name, sent.form.Get(name), want)
		}
	}
	if sent.auth != "Bearer "+tokenValue || sent.form.Has("token_auth") || strings.Contains(sent.rawQuery, tokenValue) {
		t.Errorf("token handling: %+v", sent)
	}
	if _, err := env.change("matomo.dimensions.add", "reader", args); err == nil {
		t.Error("a read connection offered dimensions.add")
	}
	for _, bad := range []string{
		`{"idSite":1,"name":"a.b","scope":"visit","active":true}`,
		`{"idSite":1,"name":"a<b","scope":"visit","active":true}`,
		`{"idSite":1,"name":"","scope":"visit","active":true}`,
		`{"idSite":1,"name":"a","scope":"conversion","active":true}`,
		`{"idSite":1,"name":"a","scope":"visit"}`,
		`{"idSite":1,"name":"a","scope":"visit","active":true,"extractions":[{"dimension":"url","pattern":"(.+)"}]}`,
		`{"idSite":1,"name":"a","scope":"action","active":true,"extractions":[{"dimension":"referrer","pattern":"x"}]}`,
		`{"idSite":1,"name":"a","scope":"action","active":true,"description":"<b>"}`,
	} {
		before := len(calls)
		if _, err := env.change("matomo.dimensions.add", "writer", bad); err == nil || len(calls) != before {
			t.Errorf("%s: err = %v, %d new requests", bad, err, len(calls)-before)
		}
	}
}

func TestDimensionsUpdatePausesAndKeepsTheRest(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, dimensionAnswers(answers{
		"CustomDimensions.getConfiguredCustomDimensions":    func(call) *http.Response { return ok(dimensionsFixture) },
		"CustomDimensions.configureExistingCustomDimension": func(call) *http.Response { return ok(`{"result":"success"}`) },
	}).handler(t))
	result, err := env.change("matomo.dimensions.update", "writer", `{"idSite":1,"idDimension":2,"active":false}`)
	var out DimensionUpdated
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || !out.Updated || out.IDDimension != 2 || out.Active == nil || *out.Active {
		t.Fatalf("result = %s, err = %v", result, err)
	}
	if countMethod(calls, "CustomDimensions.configureExistingCustomDimension") != 1 {
		t.Fatalf("update was sent %d times", countMethod(calls, "CustomDimensions.configureExistingCustomDimension"))
	}
	sent := calls[len(calls)-1]
	for name, want := range map[string]string{"idSite": "1", "idDimension": "2", "name": "Article", "active": "0",
		"caseSensitive": "0", "description": "d", "extractions[0][dimension]": "url", "extractions[0][pattern]": "a_(.+).html"} {
		if sent.form.Get(name) != want {
			t.Errorf("%s = %q, want %q", name, sent.form.Get(name), want)
		}
	}
	if _, err := env.change("matomo.dimensions.update", "writer", `{"idSite":1,"idDimension":2,"extractions":[]}`); err != nil {
		t.Fatal(err)
	}
	if sent := calls[len(calls)-1]; sent.form.Has("extractions[0][dimension]") || sent.form.Get("active") != "1" {
		t.Errorf("request = %v", sent.form)
	}
	// An ID of another website is not found, and nothing is changed.
	before := countMethod(calls, "CustomDimensions.configureExistingCustomDimension")
	if _, err := env.change("matomo.dimensions.update", "writer", `{"idSite":1,"idDimension":7,"active":false}`); classOf(err) != provider.ClassNotFound ||
		countMethod(calls, "CustomDimensions.configureExistingCustomDimension") != before {
		t.Errorf("foreign ID: err = %v", err)
	}
	// Extractions are refused for a visit dimension.
	if _, err := env.change("matomo.dimensions.update", "writer",
		`{"idSite":1,"idDimension":1,"extractions":[{"dimension":"url","pattern":"(.+)"}]}`); err == nil {
		t.Error("extractions on a visit dimension accepted")
	}
	for _, bad := range []string{`{"idSite":1,"idDimension":2}`, `{"idSite":1,"idDimension":0,"active":true}`,
		`{"idSite":1,"active":true}`, `{"idSite":1,"idDimension":2,"name":"a/b"}`} {
		n := len(calls)
		if _, err := env.change("matomo.dimensions.update", "writer", bad); err == nil || len(calls) != n {
			t.Errorf("%s: err = %v", bad, err)
		}
	}
}

func TestDimensionsForeignWebsiteIsRefusedBeforeSecretAndRequest(t *testing.T) {
	for _, tool := range []struct{ name, args string }{
		{"matomo.dimensions.list", `{"idSite":1}`},
		{"matomo.dimension-options.list", `{"idSite":1}`},
		{"matomo.dimensions.add", `{"idSite":1,"name":"a","scope":"visit","active":true}`},
		{"matomo.dimensions.update", `{"idSite":1,"idDimension":2,"active":false}`},
	} {
		var calls []call
		env := newGoalsEnvironment(t, &calls, dimensionAnswers(answers{}).handler(t))
		_, err := env.change(tool.name, "writer-two", tool.args)
		if err == nil || !isInvalidRequest(err) || strings.Contains(err.Error(), "site/") {
			t.Errorf("%s: err = %v, want an invalid request that names no target", tool.name, err)
		}
		if len(calls) != 0 || *env.reads != 0 {
			t.Errorf("%s: %d requests and %d secret reads before the refusal", tool.name, len(calls), *env.reads)
		}
	}
}

func TestDimensionErrorsMapToClassesWithoutTheMatomoText(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		class         provider.Class
		unsupported   bool
	}{
		{"permission", "You can't access this resource as it requires 'write' access for the website id = 1.", provider.ClassPermission, false},
		{"method missing", "The method 'CustomDimensions.configureNewCustomDimension' does not exist or is not available in the module 'CustomDimensions'.", "", true},
		{"plugin inactive", "The plugin CustomDimensions is not activated.", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []call
			env := newGoalsEnvironment(t, &calls, dimensionAnswers(answers{
				"CustomDimensions.configureNewCustomDimension": func(call) *http.Response { return failure(tc.message + bodyCanary) },
			}).handler(t))
			_, err := env.change("matomo.dimensions.add", "writer", `{"idSite":1,"name":"a","scope":"visit","active":true}`)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || tc.unsupported != isUnsupported(err) ||
				classOf(err) != tc.class {
				t.Fatalf("err = %v (class %q), want class %q unsupported %t", err, classOf(err), tc.class, tc.unsupported)
			}
		})
	}
}

func TestDimensionUnclearChangeIsNotRepeated(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, dimensionAnswers(answers{
		"CustomDimensions.configureNewCustomDimension": func(call) *http.Response { return ok(`{"result":"success"}`) },
	}).handler(t))
	_, err := env.change("matomo.dimensions.add", "writer", `{"idSite":1,"name":"a","scope":"visit","active":true}`)
	if err == nil || !strings.Contains(err.Error(), "never repeated") ||
		countMethod(calls, "CustomDimensions.configureNewCustomDimension") != 1 {
		t.Fatalf("err = %v", err)
	}
	var calls2 []call
	env = newGoalsEnvironment(t, &calls2, dimensionAnswers(answers{
		"CustomDimensions.getConfiguredCustomDimensions":    func(call) *http.Response { return ok(dimensionsFixture) },
		"CustomDimensions.configureExistingCustomDimension": func(call) *http.Response { return respond(http.StatusBadGateway, "") },
	}).handler(t))
	_, err = env.change("matomo.dimensions.update", "writer", `{"idSite":1,"idDimension":2,"active":false}`)
	if err == nil || countMethod(calls2, "CustomDimensions.configureExistingCustomDimension") != 1 {
		t.Fatalf("err = %v", err)
	}
}

func TestInactiveDimensionsPluginRefusesToolsWithoutFurtherRequests(t *testing.T) {
	var calls []call
	env := newGoalsEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"API.isPluginActivated": func(call) *http.Response { return ok(`{"value":false}`) },
	}).handler(t))
	_, err := env.invoke("matomo.dimensions.list", "open", `{"idSite":1}`)
	if !isUnsupported(err) || countMethod(calls, "CustomDimensions.getConfiguredCustomDimensions") != 0 {
		t.Fatalf("err = %v", err)
	}
}
