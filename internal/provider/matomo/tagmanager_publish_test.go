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

// The fixtures follow the return shapes of TagManager.exportContainerVersion (API/Export.php: the container with
// revision, version, tags, triggers, and variables) and of publishContainerVersion (the new release ID as a scalar).

const (
	publishTool      = "matomo.tag-manager-versions.publish"
	publishMethod    = "TagManager.publishContainerVersion"
	exportMethod     = "TagManager.exportContainerVersion"
	publishArguments = `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7,"environment":"live"}`
	foreignHost      = "collector.foreign.example"
)

const publishConnections = `
  tmp: {service: matomo, credential: token, tools: [matomo.tag-manager-versions.publish], targets: [site]}
  tmp-two: {service: matomo, credential: token, tools: [matomo.tag-manager-versions.publish], targets: [site/2]}
  tmp-effects: {service: matomo, credential: token, effects: [read, create, update, delete], targets: [site]}
`

func newPublishEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(publishConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

// publishableExport is a saved version with a Matomo tag, a theme color tag, a page view trigger, and its
// Matomo configuration; change edits it before it is encoded.
func publishableExport(change func(export map[string]any)) string {
	export := map[string]any{
		"idcontainer": "6OMh6taM", "idsite": 1, "context": "web", "name": "Main", "revision": 3,
		"version": map[string]any{"name": "v3", "description": "", "revision": 3},
		"tags": []any{
			map[string]any{"idtag": 1, "type": "Matomo", "name": "Matomo Analytics", "status": "active",
				"parameters":       map[string]any{"matomoConfig": "{{Matomo Configuration}}", "trackingType": "pageview", "customUrl": "{{Page}}"},
				"fire_trigger_ids": []any{2}, "block_trigger_ids": []any{}},
			map[string]any{"idtag": 2, "type": "ThemeColor", "name": "Theme", "status": "paused",
				"parameters": map[string]any{"themeColor": "{{Brand}}"}, "fire_trigger_ids": []any{2}},
		},
		"triggers": []any{map[string]any{"idtrigger": 2, "type": "PageView", "name": "Pageview", "parameters": []any{},
			"conditions": []any{map[string]any{"actual": "PageUrl", "comparison": "contains", "expected": "/"}}}},
		"variables": []any{
			map[string]any{"idvariable": 3, "type": "MatomoConfiguration", "name": "Matomo Configuration",
				"parameters": map[string]any{"matomoUrl": baseURL + "/", "idSite": "1", "bundleTracker": true,
					"jsEndpoint": "matomo.js", "trackingEndpoint": "matomo.php", "jsEndpointCustom": "custom.js",
					"trackingEndpointCustom": "custom.php", "customRequestProcessing": "", "appendToTrackingUrl": "a=1"}},
			map[string]any{"idvariable": 4, "type": "Constant", "name": "Brand", "parameters": map[string]any{"constantValue": "#123456"}},
			map[string]any{"idvariable": 5, "type": "Url", "name": "Page", "parameters": map[string]any{"urlPart": "href"}},
		},
	}
	if change != nil {
		change(export)
	}
	encoded, _ := json.Marshal(export)
	return string(encoded)
}

func entryOf(export map[string]any, list string, index int) map[string]any {
	return export[list].([]any)[index].(map[string]any)
}

func paramsOf(export map[string]any, list string, index int) map[string]any {
	return entryOf(export, list, index)["parameters"].(map[string]any)
}

func publishAnswers(export string, extra answers) answers {
	return tagManagerAnswers(with(answers{
		exportMethod:  func(call) *http.Response { return ok(export) },
		publishMethod: func(call) *http.Response { return ok(`{"value":21}`) },
	}, extra))
}

func TestTagManagerVersionsPublishIsListedOnlyConfirmedAndInNoProfile(t *testing.T) {
	reg := registry(t)
	d, _, found := reg.Lookup(publishTool)
	if !found || d.Risk.Effect != capability.EffectUpdate || d.Risk.Idempotency != capability.IdempotencyIdempotent ||
		d.Risk.Confirmation != capability.ConfirmationRequired || !d.Risk.OpenWorld ||
		d.Risk.DataSensitivity != tagManagerSensitive || !d.RequiresToolAllowList {
		t.Fatalf("descriptor = %+v", d)
	}
	if !strings.Contains(d.Description, "visitors of every website that embeds it run the new script") {
		t.Errorf("the description does not name the effect on visitors: %s", d.Description)
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == publishTool {
				t.Errorf("profile %s holds %s", profile.ID, id)
			}
		}
	}
	var calls []call
	env := newPublishEnvironment(t, &calls, publishAnswers(publishableExport(nil), nil).handler(t))
	if _, err := env.change(publishTool, "tmp-effects", publishArguments); err == nil {
		t.Error("an effects-only connection offered the tool")
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Errorf("calls = %d, secret reads = %d", len(calls), *env.reads)
	}
}

func TestTagManagerVersionsPublishChecksTheExportThenSendsOneRequest(t *testing.T) {
	var calls []call
	env := newPublishEnvironment(t, &calls, publishAnswers(publishableExport(nil), nil).handler(t))
	if _, err := env.invoke(publishTool, "tmp", publishArguments); err == nil || !strings.Contains(err.Error(), "confirm") {
		t.Fatalf("without confirm: err = %v", err)
	}
	if len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("an unconfirmed call caused %d requests and %d secret reads", len(calls), *env.reads)
	}
	result, err := env.change(publishTool, "tmp", publishArguments)
	if err != nil {
		t.Fatal(err)
	}
	var methods []string
	for _, c := range calls {
		if strings.HasPrefix(c.api(), "TagManager.") {
			methods = append(methods, c.api())
		}
	}
	if strings.Join(methods, ",") != exportMethod+","+publishMethod+","+exportMethod {
		t.Fatalf("methods = %v", methods)
	}
	read, sent, reread := calls[len(calls)-3], calls[len(calls)-2], calls[len(calls)-1]
	if reread.form.Get("idSite") != "1" || reread.form.Get("idContainer") != "6OMh6taM" ||
		reread.form.Get("idContainerVersion") != "7" || reread.auth != "Bearer "+tokenValue {
		t.Errorf("re-read request = %+v", reread)
	}
	if read.form.Get("idSite") != "1" || read.form.Get("idContainer") != "6OMh6taM" || read.form.Get("idContainerVersion") != "7" {
		t.Errorf("export request = %v", read.form)
	}
	if sent.form.Get("idSite") != "1" || sent.form.Get("idContainer") != "6OMh6taM" ||
		sent.form.Get("idContainerVersion") != "7" || sent.form.Get("environment") != "live" ||
		sent.form.Get("format") != "JSON" || sent.auth != "Bearer "+tokenValue || sent.form.Has("token_auth") ||
		strings.Contains(sent.rawQuery, tokenValue) || strings.Contains(sent.path, tokenValue) || sent.rawQuery != "" {
		t.Errorf("request = %+v", sent)
	}
	for _, name := range []string{"callback", "jsoncallback", "disable_generic_filters", "disable_queued_filters",
		"disable_root_datatable_post_processor", "filter_limit"} {
		if sent.form.Has(name) || read.form.Has(name) || reread.form.Has(name) {
			t.Errorf("a request carries %s", name)
		}
	}
	var out VersionPublished
	_ = json.Unmarshal([]byte(result), &out)
	if out != (VersionPublished{IDSite: 1, IDContainer: "6OMh6taM", IDContainerVersion: 7, Environment: "live",
		IDContainerRelease: 21, Published: true, Verified: true}) {
		t.Errorf("result = %s", result)
	}
	if got := env.red.Apply("x " + tokenValue + " y"); strings.Contains(got, tokenValue) {
		t.Errorf("redactor leaves the token: %q", got)
	}
}

func TestTagManagerVersionsPublishAcceptsTheConnectionAddressInItsForms(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"without closing slash": func(e map[string]any) { paramsOf(e, "variables", 0)["matomoUrl"] = baseURL },
		"upper-case host": func(e map[string]any) {
			paramsOf(e, "variables", 0)["matomoUrl"] = "https://ANALYTICS.example.test/matomo/"
		},
		"numeric idSite": func(e map[string]any) { paramsOf(e, "variables", 0)["idSite"] = 1 },
		"no endpoints": func(e map[string]any) {
			delete(paramsOf(e, "variables", 0), "jsEndpoint")
			delete(paramsOf(e, "variables", 0), "trackingEndpoint")
		},
		"js endpoint":           func(e map[string]any) { paramsOf(e, "variables", 0)["jsEndpoint"] = "js/tracker.php" },
		"reference with spaces": func(e map[string]any) { paramsOf(e, "tags", 0)["matomoConfig"] = "{{ matomo configuration }}" },
		"another environment":   nil,
	} {
		var calls []call
		env := newPublishEnvironment(t, &calls, publishAnswers(publishableExport(change), nil).handler(t))
		arguments := publishArguments
		if change == nil {
			arguments = strings.Replace(publishArguments, `"live"`, `"staging"`, 1)
		}
		if _, err := env.change(publishTool, "tmp", arguments); err != nil || countMethod(calls, publishMethod) != 1 {
			t.Errorf("%s: err = %v, sent %d times", name, err, countMethod(calls, publishMethod))
		}
	}
}

func TestTagManagerVersionsPublishRefusesUncheckedContentBeforeTheWrite(t *testing.T) {
	set := func(list string, index int, key string, value any) func(map[string]any) {
		return func(e map[string]any) { paramsOf(e, list, index)[key] = value }
	}
	addEntry := func(list string, entry map[string]any) func(map[string]any) {
		return func(e map[string]any) { e[list] = append(e[list].([]any), entry) }
	}
	foreignURL := "https://" + foreignHost + "/matomo/"
	for name, tc := range map[string]struct {
		change func(map[string]any)
		names  string
	}{
		"custom html tag": {addEntry("tags", map[string]any{"idtag": 9, "type": "CustomHtml", "name": bodyCanary,
			"parameters": map[string]any{"customHtml": "<script src=https://" + foreignHost + "/x.js></script>"}}), "CustomHtml"},
		"custom image tag": {addEntry("tags", map[string]any{"idtag": 9, "type": "CustomImage", "name": "Pixel",
			"parameters": map[string]any{"customImageSrc": foreignURL}}), "CustomImage"},
		"unknown tag type":   {addEntry("tags", map[string]any{"idtag": 9, "type": bodyCanary, "name": "x"}), ""},
		"google tag":         {func(e map[string]any) { entryOf(e, "tags", 1)["type"] = "GoogleAnalytics4" }, ""},
		"lower-case type":    {func(e map[string]any) { entryOf(e, "tags", 0)["type"] = "matomo" }, ""},
		"type with space":    {func(e map[string]any) { entryOf(e, "tags", 0)["type"] = "Matomo " }, ""},
		"type missing":       {func(e map[string]any) { delete(entryOf(e, "tags", 1), "type") }, ""},
		"type number":        {func(e map[string]any) { entryOf(e, "tags", 1)["type"] = 5 }, ""},
		"custom js variable": {addEntry("variables", map[string]any{"idvariable": 9, "type": "CustomJsFunction", "name": "Js", "parameters": map[string]any{"jsFunction": "function () { return " + bodyCanary + "; }"}}), "CustomJsFunction"},
		"request processing": {addEntry("variables", map[string]any{"idvariable": 9, "type": "CustomRequestProcessing", "name": "Rp", "parameters": map[string]any{"jsFunction": "function (r) { return r; }"}}), "CustomRequestProcessing"},
		"unknown variable":   {addEntry("variables", map[string]any{"idvariable": 9, "type": "PluginScript", "name": "P"}), ""},
		"unknown trigger":    {addEntry("triggers", map[string]any{"idtrigger": 9, "type": "PluginTrigger", "name": "T"}), ""},
		"foreign address":    {set("variables", 0, "matomoUrl", foreignURL), ""},
		"http address":       {set("variables", 0, "matomoUrl", "http://"+apiHost+"/matomo/"), ""},
		"protocol-relative":  {set("variables", 0, "matomoUrl", "//"+apiHost+"/matomo/"), ""},
		"other path":         {set("variables", 0, "matomoUrl", "https://"+apiHost+"/other/"), ""},
		"sub path":           {set("variables", 0, "matomoUrl", "https://"+apiHost+"/matomo/sub/"), ""},
		"other port":         {set("variables", 0, "matomoUrl", "https://"+apiHost+":8443/matomo/"), ""},
		"user info":          {set("variables", 0, "matomoUrl", "https://"+foreignHost+"@"+apiHost+"/matomo/"), ""},
		"query":              {set("variables", 0, "matomoUrl", baseURL+"/?x=1"), ""},
		"address variable":   {set("variables", 0, "matomoUrl", "{{Brand}}"), ""},
		"joined address":     {set("variables", 0, "matomoUrl", baseURL+"{{Brand}}"), ""},
		"address missing":    {func(e map[string]any) { delete(paramsOf(e, "variables", 0), "matomoUrl") }, ""},
		"address not text":   {set("variables", 0, "matomoUrl", []any{baseURL}), ""},
		"no parameters":      {func(e map[string]any) { entryOf(e, "variables", 0)["parameters"] = []any{} }, ""},
		"other website":      {set("variables", 0, "idSite", "2"), ""},
		"website variable":   {set("variables", 0, "idSite", "{{Brand}}"), ""},
		"website missing":    {func(e map[string]any) { delete(paramsOf(e, "variables", 0), "idSite") }, ""},
		"custom js endpoint": {func(e map[string]any) {
			paramsOf(e, "variables", 0)["bundleTracker"] = false
			paramsOf(e, "variables", 0)["jsEndpoint"] = "custom"
		}, ""},
		"custom tracking endpoint": {set("variables", 0, "trackingEndpoint", "custom"), ""},
		"endpoint not text":        {set("variables", 0, "trackingEndpoint", 5), ""},
		"request processing set":   {set("variables", 0, "customRequestProcessing", "{{Rp}}"), ""},
		"api url set":              {set("variables", 0, "setApiUrl", "https://"+foreignHost+"/"), ""},
		"absolute js endpoint":     {set("variables", 0, "jsEndpointCustom", "https://"+foreignHost+"/x.js"), ""},
		"absolute path endpoint":   {set("variables", 0, "trackingEndpointCustom", "/custom.php"), ""},
		"foreign endpoint":         {set("variables", 0, "trackingEndpointCustom", "//"+foreignHost+"/x.php"), ""},
		"unused foreign config": {addEntry("variables", map[string]any{"idvariable": 9, "type": "MatomoConfiguration",
			"name": "Spare", "parameters": map[string]any{"matomoUrl": foreignURL, "idSite": "1"}}), ""},
		"tag without config":     {func(e map[string]any) { delete(paramsOf(e, "tags", 0), "matomoConfig") }, ""},
		"tag with literal":       {set("tags", 0, "matomoConfig", "Matomo Configuration"), ""},
		"tag without opening":    {set("tags", 0, "matomoConfig", "Matomo Configuration}}"), ""},
		"tag with missing":       {set("tags", 0, "matomoConfig", "{{Nothing}}"), ""},
		"tag with constant":      {set("tags", 0, "matomoConfig", "{{Brand}}"), ""},
		"tag with joined":        {set("tags", 0, "matomoConfig", "{{Matomo Configuration}}x"), ""},
		"tag with nested braces": {set("tags", 0, "matomoConfig", "{{{Matomo Configuration}}}"), ""},
		"tag with array":         {set("tags", 0, "matomoConfig", []any{"{{Matomo Configuration}}"}), ""},
		"ambiguous name": {addEntry("variables", map[string]any{"idvariable": 9, "type": "Constant",
			"name": "MATOMO CONFIGURATION", "parameters": map[string]any{"constantValue": "x"}}), ""},
		"draft": {func(e map[string]any) {
			e["revision"] = 0
			e["version"].(map[string]any)["revision"] = 0
		}, ""},
		"revision missing": {func(e map[string]any) { delete(e, "revision") }, ""},
		"revision differs": {func(e map[string]any) { e["version"].(map[string]any)["revision"] = 4 }, ""},
		"version missing":  {func(e map[string]any) { delete(e, "version") }, ""},
		"long text cut":    {set("variables", 1, "constantValue", strings.Repeat("x", maxCodeLength+1)), ""},
		"entries capped": {func(e map[string]any) {
			for i := 0; i < maxExportEntries; i++ {
				e["variables"] = append(e["variables"].([]any), map[string]any{"idvariable": 100 + i, "type": "Constant", "name": "c"})
			}
		}, ""},
	} {
		var calls []call
		env := newPublishEnvironment(t, &calls, publishAnswers(publishableExport(tc.change), nil).handler(t))
		_, err := env.change(publishTool, "tmp", publishArguments)
		if err == nil || !isInvalidRequest(err) || countMethod(calls, publishMethod) != 0 || countMethod(calls, exportMethod) != 1 {
			t.Errorf("%s: err = %v, published %d times", name, err, countMethod(calls, publishMethod))
			continue
		}
		text := err.Error()
		if strings.Contains(text, bodyCanary) || strings.Contains(text, foreignHost) || strings.Contains(text, "Matomo Configuration") ||
			!strings.Contains(text, "not published") || (tc.names != "" && !strings.Contains(text, tc.names)) {
			t.Errorf("%s: refusal = %v", name, err)
		}
	}
}

func TestTagManagerVersionsPublishRefusesArgumentsBeforeSecretAndRequest(t *testing.T) {
	for name, tc := range map[string]struct{ connection, arguments string }{
		"foreign website":   {"tmp-two", publishArguments},
		"no website":        {"tmp", `{"idContainer":"6OMh6taM","idContainerVersion":7,"environment":"live"}`},
		"preview":           {"tmp", strings.Replace(publishArguments, `"live"`, `"preview"`, 1)},
		"preview upper":     {"tmp", strings.Replace(publishArguments, `"live"`, `"Preview"`, 1)},
		"no environment":    {"tmp", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7}`},
		"bad environment":   {"tmp", strings.Replace(publishArguments, `"live"`, `"li ve"`, 1)},
		"short environment": {"tmp", strings.Replace(publishArguments, `"live"`, `"l"`, 1)},
		"no version":        {"tmp", `{"idSite":1,"idContainer":"6OMh6taM","environment":"live"}`},
		"version zero":      {"tmp", strings.Replace(publishArguments, `:7`, `:0`, 1)},
		"bad container":     {"tmp", strings.Replace(publishArguments, `6OMh6taM`, `a b`, 1)},
		"extra argument":    {"tmp", strings.Replace(publishArguments, `}`, `,"method":"TagManager.importContainerVersion"}`, 1)},
	} {
		var calls []call
		env := newPublishEnvironment(t, &calls, publishAnswers(publishableExport(nil), nil).handler(t))
		_, err := env.change(publishTool, tc.connection, tc.arguments)
		if err == nil || *env.reads != 0 || len(calls) != 0 {
			t.Errorf("%s: err = %v, secret reads = %d, calls = %d", name, err, *env.reads, len(calls))
		}
		if err != nil && (strings.Contains(err.Error(), "site/") || strings.Contains(err.Error(), "importContainerVersion")) {
			t.Errorf("%s: the refusal quotes the value: %v", name, err)
		}
	}
}

func TestTagManagerVersionsPublishIsUnsupportedWithoutThePlugin(t *testing.T) {
	var calls []call
	env := newPublishEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
		"API.isPluginActivated": func(call) *http.Response { return ok(`{"value":false}`) },
	}).handler(t))
	if _, err := env.change(publishTool, "tmp", publishArguments); !isUnsupported(err) {
		t.Errorf("err = %v", err)
	}
	for _, c := range calls {
		if strings.HasPrefix(c.api(), "TagManager.") {
			t.Errorf("sent %s", c.api())
		}
	}
}

func TestTagManagerVersionsPublishCheckReadFailuresPublishNothing(t *testing.T) {
	for name, outcome := range map[string]func(call) (*http.Response, error){
		"timeout": func(call) (*http.Response, error) { return nil, context.DeadlineExceeded },
		"html":    func(call) (*http.Response, error) { return ok(`<html>` + bodyCanary + `</html>`), nil },
		"not found": func(call) (*http.Response, error) {
			return failure("The requested container version does not exist." + bodyCanary), nil
		},
		"permission": func(call) (*http.Response, error) { return failure(permissionMsg + bodyCanary), nil },
		"array":      func(call) (*http.Response, error) { return ok(`[]`), nil },
		"type not a text": func(call) (*http.Response, error) {
			return ok(publishableExport(func(e map[string]any) { entryOf(e, "tags", 0)["type"] = map[string]any{"a": 1} })), nil
		},
		"other container": func(call) (*http.Response, error) {
			return ok(strings.Replace(publishableExport(nil), "6OMh6taM", "zzzzzzzz", 1)), nil
		},
		"other website": func(call) (*http.Response, error) {
			return ok(strings.Replace(publishableExport(nil), `"idsite":1`, `"idsite":2`, 1)), nil
		},
	} {
		var calls []call
		env := newPublishEnvironment(t, &calls, func(c call) (*http.Response, error) {
			switch c.api() {
			case "API.getMatomoVersion":
				return ok(versionAnswer), nil
			case "API.isPluginActivated":
				return ok(`{"value":true}`), nil
			case exportMethod:
				return outcome(c)
			}
			return ok(`{"value":21}`), nil
		})
		_, err := env.change(publishTool, "tmp", publishArguments)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || countMethod(calls, publishMethod) != 0 ||
			strings.Contains(err.Error(), "never repeated") {
			t.Errorf("%s: err = %v, published %d times", name, err, countMethod(calls, publishMethod))
		}
	}
}

func TestTagManagerVersionsPublishMapsRefusalsWithoutTheMessage(t *testing.T) {
	for name, tc := range map[string]struct {
		message string
		class   provider.Class
		missing bool
		unclear bool
	}{
		"publish right":       {`You can't access this resource as it requires a capability "tagmanager_publish_live_container" for the website id = 1.`, provider.ClassPermission, false, false},
		"write right":         {"You can't access this resource as it requires 'write' access for the website id = 1.", provider.ClassPermission, false, false},
		"environment unknown": {`The environment "live" does not exist.`, provider.ClassNotFound, false, false},
		"version missing":     {"The requested container version does not exist.", provider.ClassNotFound, false, false},
		"container missing":   {`The requested container "token123" does not exist.`, provider.ClassNotFound, false, false},
		"method missing":      {"The method 'TagManager.publishContainerVersion' does not exist or is not available in the module 'TagManager'.", "", true, false},
		"recursion":           {`It seems an entity references itself or a recursion is caused in some other way.`, provider.ClassInvalidResponse, false, true},
	} {
		var calls []call
		env := newPublishEnvironment(t, &calls, publishAnswers(publishableExport(nil), answers{
			publishMethod: func(call) *http.Response { return failure(tc.message + bodyCanary) },
		}).handler(t))
		_, err := env.change(publishTool, "tmp", publishArguments)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), "token123") ||
			isUnsupported(err) != tc.missing || classOf(err) != tc.class || countMethod(calls, publishMethod) != 1 ||
			strings.Contains(err.Error(), "never repeated") != tc.unclear {
			t.Errorf("%s: err = %v (class %q), sent %d times", name, err, classOf(err), countMethod(calls, publishMethod))
		}
	}
}

func TestTagManagerVersionsPublishUnclearOutcomeIsReportedAndNotRepeated(t *testing.T) {
	for name, outcome := range map[string]func(call) (*http.Response, error){
		"429 without Retry-After": func(call) (*http.Response, error) { return respond(429, bodyCanary), nil },
		"bad gateway":             func(call) (*http.Response, error) { return respond(http.StatusBadGateway, bodyCanary), nil },
		"timeout":                 func(call) (*http.Response, error) { return nil, context.DeadlineExceeded },
		"reset":                   func(call) (*http.Response, error) { return nil, context.Canceled },
		"html":                    func(call) (*http.Response, error) { return ok(`<html>` + bodyCanary + `</html>`), nil },
		"unreadable": func(call) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: errReader{}, Header: http.Header{}}, nil
		},
		"no release ID": func(call) (*http.Response, error) { return ok(`{"result":"success"}`), nil },
		"zero":          func(call) (*http.Response, error) { return ok(`{"value":0}`), nil },
	} {
		var calls []call
		export := publishableExport(nil)
		env := newPublishEnvironment(t, &calls, func(c call) (*http.Response, error) {
			switch c.api() {
			case "API.getMatomoVersion":
				return ok(versionAnswer), nil
			case "API.isPluginActivated":
				return ok(`{"value":true}`), nil
			case exportMethod:
				return ok(export), nil
			}
			return outcome(c)
		})
		_, err := env.change(publishTool, "tmp", publishArguments)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || countMethod(calls, publishMethod) != 1 ||
			countMethod(calls, exportMethod) != 1 {
			t.Errorf("%s: err = %v, sent %d times", name, err, countMethod(calls, publishMethod))
			continue
		}
		if name != "429 without Retry-After" && (!strings.Contains(err.Error(), "never repeated") ||
			!strings.Contains(err.Error(), "matomo.tag-manager-versions.get")) {
			t.Errorf("%s: no uncertainty message: %v", name, err)
		}
		if name == "429 without Retry-After" && classOf(err) != provider.ClassRateLimited {
			t.Errorf("429: class %q", classOf(err))
		}
		for _, c := range calls {
			if strings.Contains(c.rawQuery+c.path, tokenValue) || (strings.HasPrefix(c.api(), "TagManager.") && c.auth != "Bearer "+tokenValue) {
				t.Errorf("%s: token handling: %+v", name, c)
			}
		}
	}
}

// rereadEnvironment serves the export checked before the release first and the one read afterwards second.
func rereadEnvironment(t *testing.T, calls *[]call, first string, second func(call) (*http.Response, error)) *environment {
	exports := 0
	return newPublishEnvironment(t, calls, func(c call) (*http.Response, error) {
		switch c.api() {
		case "API.getMatomoVersion":
			return ok(versionAnswer), nil
		case "API.isPluginActivated":
			return ok(`{"value":true}`), nil
		case exportMethod:
			exports++
			if exports == 1 {
				return ok(first), nil
			}
			return second(c)
		}
		return ok(`{"value":21}`), nil
	})
}

func TestTagManagerVersionsPublishReportsAChangeSinceTheCheckAfterThePublish(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"tag parameter": func(e map[string]any) { paramsOf(e, "tags", 0)["trackingType"] = "event" },
		"added variable": func(e map[string]any) {
			e["variables"] = append(e["variables"].([]any), map[string]any{"idvariable": 9, "type": "Constant", "name": "New"})
		},
		"variable type":  func(e map[string]any) { entryOf(e, "variables", 2)["type"] = "CustomJsFunction" },
		"trigger id":     func(e map[string]any) { entryOf(e, "tags", 0)["fire_trigger_ids"] = []any{9} },
		"condition":      func(e map[string]any) { entryOf(e, "triggers", 0)["conditions"] = []any{} },
		"status":         func(e map[string]any) { entryOf(e, "tags", 1)["status"] = "active" },
		"revision":       func(e map[string]any) { e["revision"] = 4; e["version"].(map[string]any)["revision"] = 4 },
		"removed tag":    func(e map[string]any) { e["tags"] = e["tags"].([]any)[:1] },
		"configuration":  func(e map[string]any) { paramsOf(e, "variables", 0)["idSite"] = "2" },
		"version name":   func(e map[string]any) { e["version"].(map[string]any)["name"] = "other" },
		"container name": func(e map[string]any) { e["name"] = bodyCanary },
		"cut afterwards": func(e map[string]any) {
			paramsOf(e, "variables", 1)["constantValue"] = strings.Repeat("x", maxCodeLength+1)
		},
		"entry type kind": func(e map[string]any) { entryOf(e, "tags", 0)["type"] = map[string]any{"a": 1} },
	} {
		var calls []call
		env := rereadEnvironment(t, &calls, publishableExport(nil), func(call) (*http.Response, error) {
			return ok(publishableExport(change)), nil
		})
		_, err := env.change(publishTool, "tmp", publishArguments)
		if err == nil || countMethod(calls, publishMethod) != 1 || countMethod(calls, exportMethod) != 2 ||
			strings.Contains(err.Error(), bodyCanary) || !strings.Contains(err.Error(), "was published") {
			t.Errorf("%s: err = %v, published %d times, read %d times", name, err, countMethod(calls, publishMethod),
				countMethod(calls, exportMethod))
		}
	}
}

func TestTagManagerVersionsPublishUnclearRereadIsReportedAndNotRepeated(t *testing.T) {
	for name, outcome := range map[string]func(call) (*http.Response, error){
		"timeout":     func(call) (*http.Response, error) { return nil, context.DeadlineExceeded },
		"bad gateway": func(call) (*http.Response, error) { return respond(http.StatusBadGateway, bodyCanary), nil },
		"html":        func(call) (*http.Response, error) { return ok(`<html>` + bodyCanary + `</html>`), nil },
		"unreadable": func(call) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: errReader{}, Header: http.Header{}}, nil
		},
		"error result": func(call) (*http.Response, error) { return failure("boom" + bodyCanary), nil },
		"truncated": func(call) (*http.Response, error) {
			return ok(publishableExport(func(e map[string]any) {
				for i := 0; i < maxExportEntries; i++ {
					e["variables"] = append(e["variables"].([]any), map[string]any{"idvariable": 100 + i, "type": "Constant", "name": "c"})
				}
			})), nil
		},
	} {
		var calls []call
		env := rereadEnvironment(t, &calls, publishableExport(nil), outcome)
		_, err := env.change(publishTool, "tmp", publishArguments)
		if err == nil || strings.Contains(err.Error(), bodyCanary) || countMethod(calls, publishMethod) != 1 ||
			countMethod(calls, exportMethod) != 2 || !strings.Contains(err.Error(), "was published") ||
			!strings.Contains(err.Error(), "check afterwards failed") {
			t.Errorf("%s: err = %v, published %d times, read %d times", name, err, countMethod(calls, publishMethod),
				countMethod(calls, exportMethod))
		}
	}
}
