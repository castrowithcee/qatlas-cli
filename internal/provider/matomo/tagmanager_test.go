package matomo

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// The fixtures follow the return shapes of TagManager.getContainers, getContainer, the embed code and install
// instruction methods, and the catalog methods of Matomo 5.14: database rows with strings for flags, enriched
// with versions, releases, and a draft; template metadata grouped by category.

const containerRow = `{"idcontainer":"6OMh6taM","idsite":1,"context":"web","name":"Main","description":"Shop",
"ignoreGtmDataLayer":"0","activelySyncGtmDataLayer":"1","isTagFireLimitAllowedInPreviewMode":"0","status":"active",
"created_date":"2026-01-02 03:04:05","updated_date":"2026-02-03 04:05:06","created_date_pretty":"Jan 2, 2026",
"updated_date_pretty":"Feb 3, 2026",
"versions":[{"idcontainerversion":7,"idcontainer":"6OMh6taM","idsite":1,"status":"active","revision":1,"name":"v1",
 "description":"first","created_date":"2026-01-02 03:04:05","updated_date":"2026-01-03 03:04:05",
 "created_date_pretty":"Jan 2, 2026","updated_date_pretty":"Jan 3, 2026","environments":["live","dev"]},
 {"idcontainerversion":8,"idcontainer":"6OMh6taM","idsite":1,"status":"active","revision":0,"name":"draft",
 "description":"","created_date":"2026-01-04 03:04:05","updated_date":"2026-01-04 03:04:05","environments":[]}],
"releases":[{"idcontainerrelease":3,"idcontainer":"6OMh6taM","idcontainerversion":7,"idsite":1,"status":"active",
 "environment":"live","release_login":"admin","release_date":"2026-01-05 03:04:05","release_date_pretty":"Jan 5, 2026",
 "version_name":"v1"}],
"draft":{"idcontainerversion":8,"idcontainer":"6OMh6taM","idsite":1,"status":"active","revision":0,"name":"draft",
 "description":"","created_date":"2026-01-04 03:04:05","updated_date":"2026-01-04 03:04:05"}}`

func otherContainerRow(id, name string, site int) string {
	return `{"idcontainer":"` + id + `","idsite":` + itoa(site) + `,"context":"web","name":"` + name +
		`","description":"","status":"active","versions":[],"releases":[],"draft":[]}`
}

const tagManagerConnections = `
  tm-all: {service: matomo, credential: token, tools: [matomo.tag-manager-containers.list, matomo.tag-manager-containers.get, matomo.tag-manager-containers.get-embed-code, matomo.tag-manager-catalog.list, matomo.tag-manager-versions.list, matomo.tag-manager-versions.get, matomo.tag-manager-versions.export], targets: [site]}
  tm-one: {service: matomo, credential: token, tools: [matomo.tag-manager-containers.list, matomo.tag-manager-containers.get, matomo.tag-manager-containers.get-embed-code, matomo.tag-manager-catalog.list, matomo.tag-manager-versions.list, matomo.tag-manager-versions.get, matomo.tag-manager-versions.export], targets: [site/2]}
  tm-instance: {service: matomo, credential: token, tools: [matomo.tag-manager-containers.list, matomo.tag-manager-catalog.list], targets: [instance]}
`

func newTagManagerEnvironment(t *testing.T, calls *[]call, handler func(call) (*http.Response, error)) *environment {
	t.Helper()
	serve(t, calls, handler)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	document := strings.Replace(testConfigDocument, "connections:\n", "connections:\n"+strings.TrimPrefix(tagManagerConnections, "\n"), 1)
	cfg, err := config.Decode(strings.NewReader(document), reg)
	if err != nil {
		t.Fatal(err)
	}
	return &environment{cfg: cfg, core: application.New(reg, cfg, resolver(red, &reads), red), red: red, reads: &reads}
}

func tagManagerAnswers(extra answers) answers { return with(pluginsOn(), extra) }

func TestTagManagerContainersListReadsEveryFieldAndPages(t *testing.T) {
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getContainers": func(call) *http.Response {
			return ok(`[` + otherContainerRow("zzzzzzzz", "Zeta", 1) + `,` + containerRow + `,` +
				otherContainerRow("foreign1", "Foreign", 9) + `,` + otherContainerRow("bbbbbbbb", "alpha", 1) + `]`)
		},
	}).handler(t))
	result, err := env.invoke("matomo.tag-manager-containers.list", "tm-all", `{"idSite":1,"limit":2}`)
	if err != nil {
		t.Fatal(err)
	}
	var out ContainersResult
	_ = json.Unmarshal([]byte(result), &out)
	if out.IDSite != 1 || len(out.Containers) != 2 || out.Containers[0].Name != "alpha" || out.Containers[1].ID != "6OMh6taM" ||
		!out.HasMore || out.NextCursor == "" {
		t.Fatalf("out = %s", result)
	}
	main := out.Containers[1]
	if main.Context != "web" || main.Description != "Shop" || main.IgnoreGTM == nil || *main.IgnoreGTM ||
		main.SyncGTM == nil || !*main.SyncGTM || main.FireLimit == nil || *main.FireLimit || main.Status != "active" ||
		main.Created != "2026-01-02 03:04:05" || main.UpdatedShown != "Feb 3, 2026" ||
		main.Draft == nil || main.Draft.ID != 8 || main.Draft.Revision == nil || *main.Draft.Revision != 0 ||
		len(main.Versions) != 2 || main.Versions[0].Name != "v1" || strings.Join(main.Versions[0].Environments, ",") != "live,dev" ||
		len(main.Releases) != 1 || main.Releases[0].Environment != "live" || main.Releases[0].Login != "admin" ||
		main.Releases[0].VersionID != 7 || main.Releases[0].VersionName != "v1" || main.Releases[0].ReleasedShow != "Jan 5, 2026" {
		t.Fatalf("container = %+v", main)
	}
	if got := calls[len(calls)-1]; got.api() != "TagManager.getContainers" || got.form.Get("idSite") != "1" {
		t.Fatalf("call = %+v", got)
	}
	next, err := env.invoke("matomo.tag-manager-containers.list", "tm-all", `{"idSite":1,"cursor":"`+out.NextCursor+`"}`)
	if err != nil || !strings.Contains(next, `"Zeta"`) || strings.Contains(next, `"has_more":true`) {
		t.Fatalf("next = %s, err = %v", next, err)
	}
	if _, err := env.invoke("matomo.tag-manager-containers.list", "tm-all", `{"idSite":2,"cursor":"`+out.NextCursor+`"}`); !isInvalidRequest(err) {
		t.Errorf("cursor of another website: %v", err)
	}
}

func TestTagManagerContainersListEmptyAndKeyedAnswers(t *testing.T) {
	for name, body := range map[string]string{"empty": `[]`, "keyed": `{"3":` + otherContainerRow("aaaaaaaa", "A", 1) + `}`} {
		var calls []call
		env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
			"TagManager.getContainers": func(call) *http.Response { return ok(body) },
		}).handler(t))
		result, err := env.invoke("matomo.tag-manager-containers.list", "tm-all", `{"idSite":1}`)
		if err != nil || !strings.Contains(result, `"has_more":false`) || strings.Contains(result, "null") {
			t.Fatalf("%s: %s, %v", name, result, err)
		}
	}
}

func TestTagManagerContainersListCapsVersionsAndReleases(t *testing.T) {
	versions := make([]string, 60)
	for i := range versions {
		versions[i] = `{"idcontainerversion":` + itoa(i+1) + `,"revision":` + itoa(i+1) + `,"name":"v"}`
	}
	row := `{"idcontainer":"aaaaaaaa","idsite":1,"name":"A","versions":[` + strings.Join(versions, ",") + `],"releases":[]}`
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getContainers": func(call) *http.Response { return ok(`[` + row + `]`) },
	}).handler(t))
	result, err := env.invoke("matomo.tag-manager-containers.list", "tm-all", `{"idSite":1}`)
	var out ContainersResult
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || len(out.Containers) != 1 || len(out.Containers[0].Versions) != 50 || !out.Containers[0].VersionsTruncated {
		t.Fatalf("out = %s, err = %v", result, err)
	}
}

func TestTagManagerContainersGetBindsTheContainerToTheWebsite(t *testing.T) {
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getContainer": func(c call) *http.Response {
			if c.form.Get("idContainer") == "other123" {
				return ok(otherContainerRow("6OMh6taM", "X", 1))
			}
			if c.form.Get("idContainer") == "elsewher" {
				return ok(otherContainerRow("elsewher", "X", 9))
			}
			return ok(containerRow)
		},
	}).handler(t))
	result, err := env.invoke("matomo.tag-manager-containers.get", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM"}`)
	var out ContainerResult
	_ = json.Unmarshal([]byte(result), &out)
	if err != nil || out.IDSite != 1 || out.ID != "6OMh6taM" || out.Draft == nil || len(out.Versions) != 2 || len(out.Releases) != 1 {
		t.Fatalf("out = %s, err = %v", result, err)
	}
	if got := calls[len(calls)-1]; got.api() != "TagManager.getContainer" || got.form.Get("idContainer") != "6OMh6taM" {
		t.Fatalf("call = %+v", got)
	}
	for _, id := range []string{"other123", "elsewher"} {
		if _, err := env.invoke("matomo.tag-manager-containers.get", "tm-all", `{"idSite":1,"idContainer":"`+id+`"}`); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("container %s of another website: %v", id, err)
		}
	}
}

func TestTagManagerEmbedCodeAndInstructions(t *testing.T) {
	const snippet = "<!-- Matomo Tag Manager -->\n<script>g.src='https://analytics.example.test/js/container_6OMh6taM.js'</script>"
	snippetJSON, _ := json.Marshal(snippet)
	steps := `[{"description":"Add it to the <head>","embedCode":` + string(snippetJSON) + `,"helpUrl":"https://developer.example.test/embedding"},` +
		`{"description":"Done","embedCode":"","helpUrl":"","pageViewTriggerEditUrl":"https://analytics.example.test/index.php?module=TagManager"}]`
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getContainerEmbedCode":           func(call) *http.Response { return ok(`{"value":` + string(snippetJSON) + `}`) },
		"TagManager.getContainerInstallInstructions": func(call) *http.Response { return ok(steps) },
	}).handler(t))
	result, err := env.invoke("matomo.tag-manager-containers.get-embed-code", "tm-all",
		`{"idSite":1,"idContainer":"6OMh6taM","environment":"live"}`)
	var code EmbedCodeResult
	_ = json.Unmarshal([]byte(result), &code)
	if err != nil || code.Form != "embed-code" || code.Code != snippet || code.Truncated || len(code.Steps) != 0 {
		t.Fatalf("code = %s, err = %v", result, err)
	}
	if got := calls[len(calls)-1]; got.api() != "TagManager.getContainerEmbedCode" || got.form.Get("environment") != "live" ||
		got.form.Has("jsFramework") {
		t.Fatalf("call = %+v", got)
	}
	result, err = env.invoke("matomo.tag-manager-containers.get-embed-code", "tm-all",
		`{"idSite":1,"idContainer":"6OMh6taM","environment":"live","form":"install-instructions","jsFramework":"react"}`)
	var instructions EmbedCodeResult
	_ = json.Unmarshal([]byte(result), &instructions)
	if err != nil || instructions.Form != "install-instructions" || len(instructions.Steps) != 2 ||
		instructions.Steps[0].Code != snippet || instructions.Steps[0].HelpURL != "https://developer.example.test/embedding" ||
		instructions.Steps[1].PageViewTriggerEditURL == "" || instructions.Code != "" {
		t.Fatalf("instructions = %s, err = %v", result, err)
	}
	if got := calls[len(calls)-1]; got.api() != "TagManager.getContainerInstallInstructions" || got.form.Get("jsFramework") != "react" {
		t.Fatalf("call = %+v", got)
	}
}

func TestTagManagerEmbedCodeBoundsTheText(t *testing.T) {
	long := strings.Repeat("x", maxCodeLength+100)
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getContainerEmbedCode":           func(call) *http.Response { return ok(`{"value":"` + long + `"}`) },
		"TagManager.getContainerInstallInstructions": func(call) *http.Response { return ok(`[{"description":"d","embedCode":"` + long + `"}]`) },
	}).handler(t))
	result, err := env.invoke("matomo.tag-manager-containers.get-embed-code", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","environment":"dev"}`)
	var code EmbedCodeResult
	_ = json.Unmarshal([]byte(result), &code)
	if err != nil || len(code.Code) != maxCodeLength || !code.Truncated {
		t.Fatalf("code = %d bytes, truncated = %t, err = %v", len(code.Code), code.Truncated, err)
	}
	result, err = env.invoke("matomo.tag-manager-containers.get-embed-code", "tm-all",
		`{"idSite":1,"idContainer":"6OMh6taM","environment":"dev","form":"install-instructions"}`)
	var steps EmbedCodeResult
	_ = json.Unmarshal([]byte(result), &steps)
	if err != nil || len(steps.Steps) != 1 || len(steps.Steps[0].Code) != maxCodeLength || !steps.Steps[0].Truncated {
		t.Fatalf("steps = %s, err = %v", result[:80], err)
	}
}

const templateGroups = `[{"name":"Analytics","types":[
{"id":"CustomHtml","name":"Custom HTML","description":"Adds HTML","category":"Developer","icon":"plugins/TagManager/images/icon.svg",
 "help":"Use with care","order":3,"contexts":["web"],"hasAdvancedSettings":true,"isCustomTemplate":false,
 "parameters":[{"name":"customHtml","title":"HTML","value":"<b>x</b>","defaultValue":"","type":"string","uiControl":"textarea",
  "uiControlAttributes":{"rows":4},"availableValues":null,"description":"Markup","inlineHelp":"help","introduction":"Intro",
  "condition":"foo"},
 {"name":"htmlPosition","title":"Position","defaultValue":"bodyEnd","type":"string","uiControl":"select",
  "availableValues":{"headStart":"Head","bodyEnd":"Body end"}}]}]},
{"name":"General_Others","types":[{"id":"Other","name":"Other","category":"General_Others","order":9,"contexts":["web","ios"],"parameters":[]}]}]`

func TestTagManagerCatalogListSendsTheFixedMethodPerKind(t *testing.T) {
	list := `[{"id":"web","name":"Web"},{"id":"ios","name":"iOS"}]`
	var calls []call
	answer := func(body string) func(call) *http.Response { return func(call) *http.Response { return ok(body) } }
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getAvailableContexts":                          answer(list),
		"TagManager.getAvailableEnvironments":                      answer(`[{"id":"live","name":"Live"},{"id":"dev","name":"Dev"}]`),
		"TagManager.getAvailableEnvironmentsWithPublishCapability": answer(`{"1":{"id":"dev","name":"Dev"}}`),
		"TagManager.getAvailableTagFireLimits":                     answer(`[{"id":"unlimited","name":"Unlimited"}]`),
		"TagManager.getAvailableComparisons":                       answer(`[{"id":"equals","name":"equals"},{"id":"not_equals","name":"not equals"}]`),
		"TagManager.getAvailableTagTypesInContext":                 answer(templateGroups),
		"TagManager.getAvailableTriggerTypesInContext":             answer(`[]`),
		"TagManager.getAvailableVariableTypesInContext":            answer(`[]`),
	}).handler(t))
	cases := []struct {
		arguments, method string
		site, context     bool
		entries           int
	}{
		{`{"kind":"contexts"}`, "TagManager.getAvailableContexts", false, false, 2},
		{`{"kind":"environments"}`, "TagManager.getAvailableEnvironments", false, false, 2},
		{`{"kind":"publishable-environments","idSite":1}`, "TagManager.getAvailableEnvironmentsWithPublishCapability", true, false, 1},
		{`{"kind":"fire-limits"}`, "TagManager.getAvailableTagFireLimits", false, false, 1},
		{`{"kind":"comparisons"}`, "TagManager.getAvailableComparisons", false, false, 2},
		{`{"kind":"tag-types","idContext":"web"}`, "TagManager.getAvailableTagTypesInContext", false, true, 2},
		{`{"kind":"trigger-types","idContext":"web"}`, "TagManager.getAvailableTriggerTypesInContext", false, true, 0},
		{`{"kind":"variable-types","idContext":"ios"}`, "TagManager.getAvailableVariableTypesInContext", false, true, 0},
	}
	for _, tc := range cases {
		result, err := env.invoke("matomo.tag-manager-catalog.list", "tm-all", tc.arguments)
		var out CatalogResult
		_ = json.Unmarshal([]byte(result), &out)
		got := finalCall(calls)
		if err != nil || len(out.Entries) != tc.entries || out.HasMore || got.api() != tc.method ||
			got.form.Has("idSite") != tc.site || got.form.Has("idContext") != tc.context {
			t.Errorf("%s: result = %s, err = %v, call = %+v", tc.arguments, result, err, got)
		}
	}
	result, _ := env.invoke("matomo.tag-manager-catalog.list", "tm-all", `{"kind":"tag-types","idContext":"web"}`)
	var out CatalogResult
	_ = json.Unmarshal([]byte(result), &out)
	html, other := out.Entries[0], out.Entries[1]
	if html.ID != "CustomHtml" || html.Name != "Custom HTML" || html.Description != "Adds HTML" || html.Category != "Developer" ||
		html.Icon == "" || html.Help != "Use with care" || html.Order == nil || *html.Order != 3 ||
		len(html.Contexts) != 1 || html.HasAdvancedSettings == nil || !*html.HasAdvancedSettings ||
		html.IsCustomTemplate == nil || *html.IsCustomTemplate || len(html.Parameters) != 2 {
		t.Fatalf("html = %+v", html)
	}
	first, second := html.Parameters[0], html.Parameters[1]
	if first.Name != "customHtml" || first.Title != "HTML" || first.Type != "string" || first.UIControl != "textarea" ||
		first.Description != "Markup" || first.InlineHelp != "help" || first.Introduction != "Intro" ||
		first.DefaultValue != "" || first.Condition != "foo" || first.UIAttributes != `{"rows":4}` || len(first.Available) != 0 ||
		second.DefaultValue != "bodyEnd" || len(second.Available) != 2 || second.Available[0] != (CatalogChoice{"headStart", "Head"}) ||
		second.Available[1].Value != "bodyEnd" {
		t.Fatalf("parameters = %+v", html.Parameters)
	}
	if other.Category != "General_Others" || len(other.Contexts) != 2 {
		t.Fatalf("other = %+v", other)
	}
}

func TestTagManagerCatalogListPages(t *testing.T) {
	var rows []string
	for i := 0; i < 5; i++ {
		rows = append(rows, `{"id":"op`+itoa(i)+`","name":"Op"}`)
	}
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getAvailableComparisons": func(call) *http.Response { return ok(`[` + strings.Join(rows, ",") + `]`) },
	}).handler(t))
	first, err := env.invoke("matomo.tag-manager-catalog.list", "tm-all", `{"kind":"comparisons","limit":3}`)
	var out CatalogResult
	_ = json.Unmarshal([]byte(first), &out)
	if err != nil || len(out.Entries) != 3 || !out.HasMore || out.NextCursor == "" {
		t.Fatalf("first = %s, err = %v", first, err)
	}
	next, err := env.invoke("matomo.tag-manager-catalog.list", "tm-all", `{"kind":"comparisons","cursor":"`+out.NextCursor+`"}`)
	var rest CatalogResult
	_ = json.Unmarshal([]byte(next), &rest)
	if err != nil || len(rest.Entries) != 2 || rest.HasMore || rest.Entries[0].ID != "op3" {
		t.Fatalf("next = %s, err = %v", next, err)
	}
	if _, err := env.invoke("matomo.tag-manager-catalog.list", "tm-all", `{"kind":"fire-limits","cursor":"`+out.NextCursor+`"}`); !isInvalidRequest(err) {
		t.Errorf("cursor of another kind: %v", err)
	}
}

// tagManagerArguments are valid arguments of every tool, for checks that hold for all of them.
var tagManagerTools = []struct{ id, arguments string }{
	{"matomo.tag-manager-containers.list", `{"idSite":1}`},
	{"matomo.tag-manager-containers.get", `{"idSite":1,"idContainer":"6OMh6taM"}`},
	{"matomo.tag-manager-containers.get-embed-code", `{"idSite":1,"idContainer":"6OMh6taM","environment":"live"}`},
	{"matomo.tag-manager-catalog.list", `{"kind":"publishable-environments","idSite":1}`},
	{"matomo.tag-manager-versions.list", `{"idSite":1,"idContainer":"6OMh6taM"}`},
	{"matomo.tag-manager-versions.get", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7}`},
	{"matomo.tag-manager-versions.export", `{"idSite":1,"idContainer":"6OMh6taM"}`},
}

func TestTagManagerToolsRefuseBadArgumentsBeforeSecretAndRequest(t *testing.T) {
	for name, tc := range map[string]struct{ tool, connection, arguments string }{
		"foreign website":            {"matomo.tag-manager-containers.list", "tm-one", `{"idSite":1}`},
		"foreign website get":        {"matomo.tag-manager-containers.get", "tm-one", `{"idSite":1,"idContainer":"6OMh6taM"}`},
		"foreign website embed":      {"matomo.tag-manager-containers.get-embed-code", "tm-one", `{"idSite":3,"idContainer":"6OMh6taM","environment":"live"}`},
		"foreign website catalog":    {"matomo.tag-manager-catalog.list", "tm-one", `{"kind":"publishable-environments","idSite":1}`},
		"no website":                 {"matomo.tag-manager-containers.list", "tm-all", `{}`},
		"bad site":                   {"matomo.tag-manager-containers.list", "tm-all", `{"idSite":0}`},
		"bad limit":                  {"matomo.tag-manager-containers.list", "tm-all", `{"idSite":1,"limit":101}`},
		"missing container":          {"matomo.tag-manager-containers.get", "tm-all", `{"idSite":1}`},
		"long container":             {"matomo.tag-manager-containers.get", "tm-all", `{"idSite":1,"idContainer":"123456789"}`},
		"container with markup":      {"matomo.tag-manager-containers.get", "tm-all", `{"idSite":1,"idContainer":"a\"b"}`},
		"missing environment":        {"matomo.tag-manager-containers.get-embed-code", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM"}`},
		"short environment":          {"matomo.tag-manager-containers.get-embed-code", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","environment":"a"}`},
		"environment with dash":      {"matomo.tag-manager-containers.get-embed-code", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","environment":"a-b"}`},
		"unknown form":               {"matomo.tag-manager-containers.get-embed-code", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","environment":"live","form":"x"}`},
		"framework without steps":    {"matomo.tag-manager-containers.get-embed-code", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","environment":"live","jsFramework":"react"}`},
		"unknown framework":          {"matomo.tag-manager-containers.get-embed-code", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","environment":"live","form":"install-instructions","jsFramework":"vue"}`},
		"unknown kind":               {"matomo.tag-manager-catalog.list", "tm-all", `{"kind":"tags"}`},
		"missing kind":               {"matomo.tag-manager-catalog.list", "tm-all", `{}`},
		"types without context":      {"matomo.tag-manager-catalog.list", "tm-all", `{"kind":"tag-types"}`},
		"bad context":                {"matomo.tag-manager-catalog.list", "tm-all", `{"kind":"tag-types","idContext":"Web;"}`},
		"context for contexts":       {"matomo.tag-manager-catalog.list", "tm-all", `{"kind":"contexts","idContext":"web"}`},
		"site for contexts":          {"matomo.tag-manager-catalog.list", "tm-all", `{"kind":"contexts","idSite":1}`},
		"publishable without site":   {"matomo.tag-manager-catalog.list", "tm-all", `{"kind":"publishable-environments"}`},
		"foreign website versions":   {"matomo.tag-manager-versions.list", "tm-one", `{"idSite":1,"idContainer":"6OMh6taM"}`},
		"foreign website version":    {"matomo.tag-manager-versions.get", "tm-one", `{"idSite":3,"idContainer":"6OMh6taM","idContainerVersion":7}`},
		"foreign website export":     {"matomo.tag-manager-versions.export", "tm-one", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7}`},
		"no website export":          {"matomo.tag-manager-versions.export", "tm-all", `{"idContainer":"6OMh6taM"}`},
		"versions without container": {"matomo.tag-manager-versions.list", "tm-all", `{"idSite":1}`},
		"export bad container":       {"matomo.tag-manager-versions.export", "tm-all", `{"idSite":1,"idContainer":"a b"}`},
		"get without version":        {"matomo.tag-manager-versions.get", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM"}`},
		"get version zero":           {"matomo.tag-manager-versions.get", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":0}`},
		"get version text":           {"matomo.tag-manager-versions.get", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":"7"}`},
		"export version negative":    {"matomo.tag-manager-versions.export", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":-1}`},
		"export version too large":   {"matomo.tag-manager-versions.export", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":2147483648}`},
		"versions bad limit":         {"matomo.tag-manager-versions.list", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","limit":0,"cursor":"!"}`},
		"export extra argument":      {"matomo.tag-manager-versions.export", "tm-all", `{"idSite":1,"idContainer":"6OMh6taM","method":"TagManager.deleteContainerVersion"}`},
		"extra argument":             {"matomo.tag-manager-containers.list", "tm-all", `{"idSite":1,"method":"TagManager.deleteContainer"}`},
	} {
		var calls []call
		env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{}).handler(t))
		_, err := env.invoke(tc.tool, tc.connection, tc.arguments)
		if err == nil || *env.reads != 0 || len(calls) != 0 {
			t.Errorf("%s: err = %v, secret reads = %d, calls = %d", name, err, *env.reads, len(calls))
		}
		if err != nil && strings.Contains(err.Error(), "TagManager.deleteContainer") {
			t.Errorf("%s: the refusal quotes the value: %v", name, err)
		}
	}
}

func TestTagManagerToolsAreUnsupportedWithoutThePluginAndSendNoMethod(t *testing.T) {
	for _, tool := range tagManagerTools {
		var calls []call
		env := newTagManagerEnvironment(t, &calls, with(versionOnly("5.14.2"), answers{
			"API.isPluginActivated": func(c call) *http.Response {
				if c.form.Get("pluginName") != "TagManager" {
					t.Errorf("plugin asked for = %q", c.form.Get("pluginName"))
				}
				return ok(`{"value":false}`)
			},
		}).handler(t))
		_, err := env.invoke(tool.id, "tm-all", tool.arguments)
		if !isUnsupported(err) {
			t.Errorf("%s: err = %v", tool.id, err)
		}
		for _, c := range calls {
			if strings.HasPrefix(c.api(), "TagManager.") {
				t.Errorf("%s sent %s", tool.id, c.api())
			}
		}
	}
}

func TestTagManagerToolsMapErrorsAndNeverLeakTheMessage(t *testing.T) {
	passthrough := func(c call, body func() (*http.Response, error)) (*http.Response, error) {
		switch c.api() {
		case "API.getMatomoVersion":
			return ok(versionAnswer), nil
		case "API.isPluginActivated":
			return ok(`{"value":true}`), nil
		}
		return body()
	}
	for _, tool := range tagManagerTools {
		for name, tc := range map[string]struct {
			message string
			class   provider.Class
			missing bool
		}{
			"permission":        {"You can't access this resource as it requires 'view' access for the website id = 1.", provider.ClassPermission, false},
			"tag manager role":  {"You can't access this resource as it requires 'write' access for the website id = 1.", provider.ClassPermission, false},
			"site not found":    {"An unexpected website was found in the request: website id = 1 not found.", provider.ClassNotFound, false},
			"container missing": {`The requested container "token123" does not exist.`, provider.ClassNotFound, false},
			"environment":       {`The environment "login_a" does not exist.`, provider.ClassNotFound, false},
			"context":           {`The context "password" is not supported`, provider.ClassNotFound, false},
			"method missing":    {"The method 'TagManager.x' does not exist or is not available in the module 'TagManager'.", "", true},
			"plugin off":        {"The plugin TagManager is not activated.", "", true},
			"other":             {"Something went wrong", provider.ClassInvalidResponse, false},
		} {
			var calls []call
			env := newTagManagerEnvironment(t, &calls, func(c call) (*http.Response, error) {
				return passthrough(c, func() (*http.Response, error) { return failure(tc.message + bodyCanary), nil })
			})
			_, err := env.invoke(tool.id, "tm-all", tool.arguments)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || strings.Contains(err.Error(), "token123") ||
				isUnsupported(err) != tc.missing || classOf(err) != tc.class {
				t.Errorf("%s %s: err = %v", tool.id, name, err)
			}
		}
		for status, want := range map[int]provider.Class{429: provider.ClassRateLimited, 401: provider.ClassAuth} {
			var calls []call
			env := newTagManagerEnvironment(t, &calls, func(c call) (*http.Response, error) {
				return passthrough(c, func() (*http.Response, error) { return respond(status, bodyCanary), nil })
			})
			if _, err := env.invoke(tool.id, "tm-all", tool.arguments); classOf(err) != want || strings.Contains(err.Error(), bodyCanary) {
				t.Errorf("%s status %d: err = %v", tool.id, status, err)
			}
		}
		for name, body := range map[string]string{"html": `<html>` + bodyCanary + `</html>`, "wrong shape": `"` + bodyCanary + `"`} {
			var calls []call
			env := newTagManagerEnvironment(t, &calls, func(c call) (*http.Response, error) {
				return passthrough(c, func() (*http.Response, error) { return ok(body), nil })
			})
			_, err := env.invoke(tool.id, "tm-all", tool.arguments)
			// The embed code form accepts a bare text; every other answer of the wrong shape is refused.
			if tool.id != "matomo.tag-manager-containers.get-embed-code" &&
				(classOf(err) != provider.ClassInvalidResponse || strings.Contains(err.Error(), bodyCanary)) {
				t.Errorf("%s %s: err = %v", tool.id, name, err)
			}
		}
		var calls []call
		env := newTagManagerEnvironment(t, &calls, func(c call) (*http.Response, error) {
			return passthrough(c, func() (*http.Response, error) { return nil, context.DeadlineExceeded })
		})
		if _, err := env.invoke(tool.id, "tm-all", tool.arguments); classOf(err) != provider.ClassTimeout {
			t.Errorf("%s timeout: err = %v", tool.id, err)
		}
		var sent []call
		env = newTagManagerEnvironment(t, &sent, func(c call) (*http.Response, error) {
			return passthrough(c, func() (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: errReader{}, Header: http.Header{}}, nil
			})
		})
		if _, err := env.invoke(tool.id, "tm-all", tool.arguments); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s unreadable body: err = %v", tool.id, err)
		}
	}
}

func TestTagManagerToolsSendBearerAndNoForbiddenParameterAndAreListedOnly(t *testing.T) {
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getContainers":                                 func(call) *http.Response { return ok(`[]`) },
		"TagManager.getContainer":                                  func(call) *http.Response { return ok(containerRow) },
		"TagManager.getContainerEmbedCode":                         func(call) *http.Response { return ok(`{"value":"x"}`) },
		"TagManager.getAvailableEnvironmentsWithPublishCapability": func(call) *http.Response { return ok(`[]`) },
		"TagManager.getContainerVersions":                          func(call) *http.Response { return ok(`[]`) },
		"TagManager.getContainerVersion":                           func(call) *http.Response { return ok(versionRow) },
		"TagManager.exportContainerVersion":                        func(call) *http.Response { return ok(versionExportRow) },
	}).handler(t))
	for _, tool := range tagManagerTools {
		if _, err := env.invoke(tool.id, "tm-all", tool.arguments); err != nil {
			t.Fatalf("%s: %v", tool.id, err)
		}
	}
	for _, got := range calls {
		if strings.Contains(got.rawQuery, tokenValue) || strings.Contains(got.path, tokenValue) || got.form.Has("token_auth") ||
			got.auth != "Bearer "+tokenValue || got.method != http.MethodPost {
			t.Errorf("token reached the URL or the form: %+v", got)
		}
		for _, name := range []string{"callback", "jsoncallback", "disable_generic_filters", "disable_queued_filters",
			"disable_root_datatable_post_processor", "filter_limit"} {
			if got.form.Has(name) {
				t.Errorf("request carries %s", name)
			}
		}
	}
	reg := registry(t)
	metadata, _ := reg.ProviderMetadata(Provider)
	for _, tool := range tagManagerTools {
		descriptor, _, found := reg.Lookup(tool.id)
		if !found {
			t.Fatalf("%s is not registered", tool.id)
		}
		risk := descriptor.Risk
		if string(risk.Effect) != "read" || string(risk.Idempotency) != "safe" || string(risk.Confirmation) != "none" ||
			!risk.OpenWorld || risk.DataSensitivity != tagManagerSensitive || !descriptor.RequiresToolAllowList {
			t.Errorf("%s: risk = %+v, listed only = %t", tool.id, risk, descriptor.RequiresToolAllowList)
		}
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if id == tool.id {
					t.Errorf("profile %s holds %s", profile.ID, id)
				}
			}
		}
	}
}

func TestTagManagerToolsNeedWebsiteBinding(t *testing.T) {
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{}).handler(t))
	for _, tool := range []string{"matomo.tag-manager-containers.list", "matomo.tag-manager-catalog.list"} {
		if _, err := env.invoke(tool, "tm-instance", `{"kind":"contexts","idSite":1}`); err == nil {
			t.Errorf("%s: instance-only connection reached a website tool", tool)
		}
	}
	if len(calls) != 0 {
		t.Errorf("calls = %d", len(calls))
	}
}
