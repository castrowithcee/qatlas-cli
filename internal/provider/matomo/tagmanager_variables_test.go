package matomo

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The fixtures follow the return shapes of TagManager.getContainerVariables, getContainerVariable,
// getAvailableContainerVariables, and the reference methods: database rows with decoded parameters and lookup
// table, grouped template metadata, and the arrays of the reference classes.

const variableRow = `{"idvariable":3,"idcontainerversion":7,"idsite":1,"type":"CustomJsFunction","name":"myVar",
"description":"d","status":"active","parameters":{"jsFunction":"function () { return 'SECRETCODE'; }"},
"default_value":"fallback","lookup_table":[{"comparison":"equals","match_value":"a","out_value":"<b>b</b>"}],
"created_date":"2026-01-02 03:04:05","updated_date":"2026-01-03 03:04:05","created_date_pretty":"Jan 2, 2026",
"updated_date_pretty":"Jan 3, 2026","typeMetadata":{"id":"CustomJsFunction","parameters":[{"name":"x","value":"SECRETMETA"}]}}`

const availableVariablesAnswer = `[{"name":"Custom","types":[{"id":"myVar","idvariable":3,"type":"CustomJsFunction","name":"myVar",
"category":"Custom","description":"","order":0,"is_pre_configured":false}]},
{"name":"Page","types":[{"id":"PageUrl","idvariable":"","type":"PageUrl","name":"Page URL","category":"Page",
"description":"The URL <b>x</b>","order":10,"is_pre_configured":true}]}]`

const base781 = `{"idSite":1,"idContainer":"6OMh6taM","idContainerVersion":7`

func TestTagManagerVariablesListAndGetReadEveryField(t *testing.T) {
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getContainerVariables": func(call) *http.Response {
			return ok(`[` + variableRow + `,` + entryRowOf("idvariable", 4, 7, 9) + `,` + entryRowOf("idvariable", 5, 8, 1) + `]`)
		},
		"TagManager.getContainerVariable": func(c call) *http.Response {
			switch c.form.Get("idVariable") {
			case "4":
				return ok(entryRowOf("idvariable", 4, 7, 9))
			case "5":
				return ok(variableRow) // another variable than asked for
			case "6":
				return ok(`false`)
			}
			return ok(variableRow)
		},
	}).handler(t))
	result, err := env.invoke("matomo.tag-manager-variables.list", "tm-all", base781+`}`)
	if err != nil {
		t.Fatal(err)
	}
	var out VariablesResult
	_ = json.Unmarshal([]byte(result), &out)
	if len(out.Variables) != 1 || out.IDSite != 1 || out.IDContainerVersion != 7 {
		t.Fatalf("out = %s", result)
	}
	v := out.Variables[0]
	params, _ := v.Parameters.(map[string]any)
	table, _ := v.LookupTable.([]any)
	if v.ID != 3 || v.Type != "CustomJsFunction" || v.Name != "myVar" || v.Description != "d" || v.Status != "active" ||
		v.DefaultValue != "fallback" || params["jsFunction"] != "function () { return 'SECRETCODE'; }" || len(table) != 1 ||
		v.Created != "2026-01-02 03:04:05" || v.UpdatedShown != "Jan 3, 2026" {
		t.Fatalf("variable = %+v", v)
	}
	if strings.Contains(result, "SECRETMETA") || strings.Contains(result, "typeMetadata") {
		t.Errorf("template metadata leaked: %s", result)
	}
	last := calls[len(calls)-1]
	if last.api() != "TagManager.getContainerVariables" || last.form.Get("idContainerVersion") != "7" {
		t.Fatalf("call = %+v", last)
	}
	result, err = env.invoke("matomo.tag-manager-variables.get", "tm-all", base781+`,"idVariable":3}`)
	var one EntryResult
	_ = json.Unmarshal([]byte(result), &one)
	if err != nil || one.ID != 3 || one.DefaultValue != "fallback" || one.LookupTable == nil ||
		calls[len(calls)-1].api() != "TagManager.getContainerVariable" || calls[len(calls)-1].form.Get("idVariable") != "3" {
		t.Fatalf("get = %s, %v", result, err)
	}
	for _, id := range []string{"4", "5"} {
		if _, err := env.invoke("matomo.tag-manager-variables.get", "tm-all", base781+`,"idVariable":`+id+`}`); classOf(err) != "invalid-provider-response" {
			t.Errorf("variable %s: %v", id, err)
		}
	}
	if _, err := env.invoke("matomo.tag-manager-variables.get", "tm-all", base781+`,"idVariable":6}`); classOf(err) != "not-found" {
		t.Errorf("missing variable: %v", err)
	}
}

func TestTagManagerVariablesBoundUntrustedCode(t *testing.T) {
	big := strings.Repeat("A", 20000)
	row := `{"idvariable":1,"default_value":"` + big + `","parameters":{"jsFunction":"` + big + `"},
"lookup_table":[{"out_value":"` + big + `"}]}`
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getContainerVariables": func(call) *http.Response { return ok(`[` + row + `]`) },
		"TagManager.getContainerVariable":  func(call) *http.Response { return ok(row) },
	}).handler(t))
	for _, tool := range []struct{ id, args string }{{"matomo.tag-manager-variables.list", base781 + `}`},
		{"matomo.tag-manager-variables.get", base781 + `,"idVariable":1}`}} {
		result, err := env.invoke(tool.id, "tm-all", tool.args)
		if err != nil || !strings.Contains(result, `"truncated":true`) || len(result) > maxExportText {
			t.Errorf("%s: %d bytes, %v", tool.id, len(result), err)
		}
	}
}

func TestTagManagerAvailableVariablesFlattenAndPage(t *testing.T) {
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getAvailableContainerVariables": func(call) *http.Response { return ok(availableVariablesAnswer) },
	}).handler(t))
	first, err := env.invoke("matomo.tag-manager-variables.list-available", "tm-all", base781+`,"limit":1}`)
	var out AvailableVariablesResult
	_ = json.Unmarshal([]byte(first), &out)
	if err != nil || len(out.Variables) != 1 || !out.HasMore || out.NextCursor == "" {
		t.Fatalf("first = %s, %v", first, err)
	}
	v := out.Variables[0]
	if v.ID != "myVar" || v.IDVariable == nil || *v.IDVariable != 3 || v.Type != "CustomJsFunction" || v.Category != "Custom" ||
		v.Order == nil || *v.Order != 0 || v.IsPreConfigured == nil || *v.IsPreConfigured {
		t.Fatalf("variable = %+v", v)
	}
	rest, err := env.invoke("matomo.tag-manager-variables.list-available", "tm-all", base781+`,"cursor":"`+out.NextCursor+`"}`)
	var tail AvailableVariablesResult
	_ = json.Unmarshal([]byte(rest), &tail)
	if err != nil || len(tail.Variables) != 1 || tail.HasMore {
		t.Fatalf("rest = %s, %v", rest, err)
	}
	p := tail.Variables[0]
	if p.ID != "PageUrl" || p.IDVariable != nil || p.IsPreConfigured == nil || !*p.IsPreConfigured || p.Description != "The URL <b>x</b>" {
		t.Fatalf("preconfigured = %+v", p)
	}
	other := strings.Replace(base781, `"idContainerVersion":7`, `"idContainerVersion":8`, 1)
	before := len(calls)
	if _, err := env.invoke("matomo.tag-manager-variables.list-available", "tm-all", other+`,"cursor":"`+out.NextCursor+`"}`); !isInvalidRequest(err) || len(calls) != before {
		t.Errorf("foreign cursor: %v", err)
	}
	if _, err := env.invoke("matomo.tag-manager-variables.list", "tm-all", base781+`,"cursor":"`+out.NextCursor+`"}`); !isInvalidRequest(err) {
		t.Errorf("cursor of another tool: %v", err)
	}
}

func TestTagManagerReferencesChooseTheMethodByKindAndBindTheCursor(t *testing.T) {
	rows := make([]string, 0, 30)
	for i := 1; i <= 30; i++ {
		rows = append(rows, `{"referenceId":`+itoa(i)+`,"referenceType":"tag","referenceTypeName":"Tag","referenceName":"n`+itoa(i)+`"}`)
	}
	var calls []call
	env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
		"TagManager.getContainerTriggerReferences":  func(call) *http.Response { return ok(`[` + strings.Join(rows, ",") + `]`) },
		"TagManager.getContainerVariableReferences": func(call) *http.Response { return ok(`{"0":` + rows[0] + `}`) },
	}).handler(t))
	first, err := env.invoke("matomo.tag-manager-references.list", "tm-all", base781+`,"kind":"trigger","idTrigger":7,"limit":20}`)
	var out ReferencesResult
	_ = json.Unmarshal([]byte(first), &out)
	last := calls[len(calls)-1]
	if err != nil || len(out.References) != 20 || !out.HasMore || out.Kind != "trigger" || out.IDTrigger != 7 ||
		last.api() != "TagManager.getContainerTriggerReferences" || last.form.Get("idTrigger") != "7" || last.form.Has("idVariable") {
		t.Fatalf("first = %s, %v, call %+v", first, err, last)
	}
	if r := out.References[0]; r.ID != 1 || r.Type != "tag" || r.TypeName != "Tag" || r.Name != "n1" {
		t.Fatalf("reference = %+v", r)
	}
	rest, err := env.invoke("matomo.tag-manager-references.list", "tm-all", base781+`,"kind":"trigger","idTrigger":7,"cursor":"`+out.NextCursor+`"}`)
	var tail ReferencesResult
	_ = json.Unmarshal([]byte(rest), &tail)
	if err != nil || len(tail.References) != 10 || tail.HasMore || tail.References[0].ID != 21 {
		t.Fatalf("rest = %s, %v", rest, err)
	}
	for _, other := range []string{`"kind":"trigger","idTrigger":8`, `"kind":"variable","idVariable":7`} {
		before := len(calls)
		if _, err := env.invoke("matomo.tag-manager-references.list", "tm-all", base781+`,`+other+`,"cursor":"`+out.NextCursor+`"}`); !isInvalidRequest(err) || len(calls) != before {
			t.Errorf("cursor for %s: %v", other, err)
		}
	}
	result, err := env.invoke("matomo.tag-manager-references.list", "tm-all", base781+`,"kind":"variable","idVariable":3}`)
	last = calls[len(calls)-1]
	if err != nil || last.api() != "TagManager.getContainerVariableReferences" || last.form.Get("idVariable") != "3" ||
		!strings.Contains(result, `"idVariable":3`) || !strings.Contains(result, `"kind":"variable"`) {
		t.Fatalf("variable kind: %s, %v, call %+v", result, err, last)
	}
}

func TestTagManagerReferencesEmptyAndMalformed(t *testing.T) {
	for body, valid := range map[string]bool{`[]`: true, `{}`: true, `[{"referenceName":"x"}]`: false, `"x"`: false} {
		var calls []call
		env := newTagManagerEnvironment(t, &calls, tagManagerAnswers(answers{
			"TagManager.getContainerTriggerReferences": func(call) *http.Response { return ok(body) },
		}).handler(t))
		result, err := env.invoke("matomo.tag-manager-references.list", "tm-all", base781+`,"kind":"trigger","idTrigger":7}`)
		if valid && (err != nil || strings.Contains(result, `"referenceId"`) || !strings.Contains(result, `"kind":"trigger"`)) {
			t.Errorf("%s: %s, %v", body, result, err)
		}
		if !valid && classOf(err) != "invalid-provider-response" {
			t.Errorf("%s: %v", body, err)
		}
	}
}
