package forge

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
)

const flagsAPI = "/api/v1/repos/acme/widget/flags"

var flagTools = []*tool{repositoryFlagsList, repositoryFlagsCheck, repositoryFlagsAdd, repositoryFlagsRemove,
	repositoryFlagsReplace, repositoryFlagsClear}

// flagServer answers with the swagger shapes of Forgejo 15 through 17: a list of strings, 204 for a set flag and
// for every change, 404 for an unset flag.
func flagServer(status *int) func(call) *http.Response {
	return func(c call) *http.Response {
		switch {
		case c.method == http.MethodGet && c.path == flagsAPI:
			return jsonResponse(200, `["archived","sponsored"]`)
		case c.method == http.MethodGet && c.path == flagsAPI+"/archived":
			return jsonResponse(204, "")
		case c.method == http.MethodGet:
			return notFoundResponse()
		}
		return jsonResponse(*status, "")
	}
}

// flagConnection is the connection that names a tool; replace and clear need the listing one.
func flagConnection(tl *tool) string {
	if tl == repositoryFlagsReplace || tl == repositoryFlagsClear {
		return "flag-listed"
	}
	return "repo"
}

func TestRepositoryFlagsListAndCheck(t *testing.T) {
	status := 204
	e := newEnvironment(t, forgejo, flagServer(&status))
	result, err := e.invoke(t, repositoryFlagsList, "repo", `{}`)
	if err != nil || result["repository"] != "acme/widget" || result["truncated"] != nil ||
		!reflect.DeepEqual(result["flags"], []any{"archived", "sponsored"}) {
		t.Fatalf("%v, %v", result, err)
	}
	for _, c := range []struct {
		flag string
		want bool
	}{{"archived", true}, {"other", false}} {
		result, err := e.invoke(t, repositoryFlagsCheck, "repo", `{"flag":"`+c.flag+`"}`)
		if err != nil || result["has_flag"] != c.want || result["flag"] != c.flag {
			t.Errorf("%s: %v, %v", c.flag, result, err)
		}
	}
	if got := methods(e); !reflect.DeepEqual(got, []string{"GET " + flagsAPI, "GET " + flagsAPI + "/archived",
		"GET " + flagsAPI + "/other"}) {
		t.Errorf("calls = %v", got)
	}
	e = newEnvironment(t, forgejo, func(c call) *http.Response { return jsonResponse(200, `[]`) })
	// The result drops an empty member, so an empty list shows as no flags.
	if result, err := e.invoke(t, repositoryFlagsList, "repo", `{}`); err != nil || len(asList(result["flags"])) != 0 {
		t.Errorf("empty list: %v, %v", result, err)
	}
	many := `[` + strings.TrimSuffix(strings.Repeat(`"a",`, 101), ",") + `]`
	e = newEnvironment(t, forgejo, func(c call) *http.Response { return jsonResponse(200, many) })
	if result, err := e.invoke(t, repositoryFlagsList, "repo", `{}`); err != nil || result["truncated"] != true ||
		len(result["flags"].([]any)) != 100 {
		t.Errorf("capped list: %v", err)
	}
}

func TestRepositoryFlagsCheckKeepsOtherFailures(t *testing.T) {
	e := newEnvironment(t, forgejo, func(c call) *http.Response { return jsonResponse(403, bodyCanary) })
	if _, err := e.invoke(t, repositoryFlagsCheck, "repo", `{"flag":"archived"}`); err == nil {
		t.Error("a 403 must stay a failure")
	}
}

func TestRepositoryFlagsAreBoundToTheTargetsBeforeAnyIO(t *testing.T) {
	status := 204
	for _, tl := range flagTools {
		for _, connection := range []string{"repo", "exclude"} {
			repository := "other/tool"
			if connection == "exclude" {
				repository = "acme/gadget"
			}
			e := newEnvironment(t, forgejo, flagServer(&status))
			_, err := e.invoke(t, tl, flagConnection(tl),
				`{"repository":"`+repository+`","flag":"archived","flags":["archived"]}`)
			if connection == "exclude" && flagConnection(tl) == "repo" {
				_, err = e.invoke(t, tl, "exclude",
					`{"repository":"`+repository+`","flag":"archived","flags":["archived"]}`)
			}
			refused(t, e, tl.action+" "+connection, err)
			if err != nil && strings.Contains(err.Error(), repository) {
				t.Errorf("the refusal names the repository: %v", err)
			}
		}
	}
}

func TestRepositoryFlagNamesAreChecked(t *testing.T) {
	status := 204
	bad := []string{`""`, `"."`, `".."`, `"a/b"`, `"a\\b"`, `"a\u0001"`, `"a\nb"`, `"a b"`, `"a?b"`, `"a%2Fb"`, `"ä"`,
		`"` + strings.Repeat("a", 101) + `"`, `5`, `null`, `true`, `["a"]`}
	for _, tl := range []*tool{repositoryFlagsCheck, repositoryFlagsAdd, repositoryFlagsRemove} {
		for _, flag := range bad {
			e := newEnvironment(t, forgejo, flagServer(&status))
			_, err := e.invoke(t, tl, "repo", `{"flag":`+flag+`}`)
			refused(t, e, tl.action+" "+flag, err)
		}
		e := newEnvironment(t, forgejo, flagServer(&status))
		_, err := e.invoke(t, tl, "repo", `{}`)
		refused(t, e, tl.action+" without a flag", err)
	}
	for _, flag := range bad {
		e := newEnvironment(t, forgejo, flagServer(&status))
		_, err := e.invoke(t, repositoryFlagsReplace, "flag-listed", `{"flags":[`+flag+`]}`)
		refused(t, e, "replace "+flag, err)
	}
	for _, args := range []string{`{}`, `{"flags":null}`, `{"flags":"a"}`, `{"flags":{"a":1}}`,
		`{"flags":[` + strings.TrimSuffix(strings.Repeat(`"a",`, 101), ",") + `]}`} {
		e := newEnvironment(t, forgejo, flagServer(&status))
		_, err := e.invoke(t, repositoryFlagsReplace, "flag-listed", args)
		refused(t, e, "replace "+args, err)
	}
	for _, tl := range []*tool{repositoryFlagsList, repositoryFlagsClear} {
		e := newEnvironment(t, forgejo, flagServer(&status))
		_, err := e.invoke(t, tl, flagConnection(tl), `{"flag":"archived"}`)
		refused(t, e, tl.action+" with a flag", err)
	}
	e := newEnvironment(t, forgejo, flagServer(&status))
	_, err := e.invoke(t, repositoryFlagsClear, "flag-listed", `{"flags":[]}`)
	refused(t, e, "clear with flags", err)
	for _, name := range []string{"a", "is-archived", "a.b_C-9", strings.Repeat("a", 100)} {
		if !validFlag(name) {
			t.Errorf("%q is refused", name)
		}
	}
}

func TestRepositoryFlagsAddAndRemoveSendOneRequestWithoutBody(t *testing.T) {
	status := 204
	for _, tl := range []*tool{repositoryFlagsAdd, repositoryFlagsRemove} {
		e := newEnvironment(t, forgejo, flagServer(&status))
		if _, err := e.invokeAs(t, tl, "repo", `{"flag":"archived"}`, false); err == nil || len(*e.calls) != 0 ||
			*e.reads != 0 {
			t.Fatalf("%s: unconfirmed: %v", tl.action, err)
		}
		result, err := e.invoke(t, tl, "repo", `{"repository":"acme/widget","flag":"archived"}`)
		key := map[*tool]string{repositoryFlagsAdd: "added", repositoryFlagsRemove: "removed"}[tl]
		if err != nil || result[key] != true || result["flag"] != "archived" || result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", tl.action, result, err)
		}
		method := map[*tool]string{repositoryFlagsAdd: "PUT ", repositoryFlagsRemove: "DELETE "}[tl]
		if got := methods(e); !reflect.DeepEqual(got, []string{method + flagsAPI + "/archived"}) ||
			(*e.calls)[0].body != "" {
			t.Errorf("%s calls = %v body %q", tl.action, got, (*e.calls)[0].body)
		}
	}
}

func TestRepositoryFlagsReplaceSendsTheListAlsoWhenEmpty(t *testing.T) {
	status := 204
	for args, body := range map[string]string{`{"flags":["archived","a.b"]}`: `{"flags":["archived","a.b"]}`,
		`{"flags":[]}`: `{"flags":[]}`} {
		e := newEnvironment(t, forgejo, flagServer(&status))
		if _, err := e.invokeAs(t, repositoryFlagsReplace, "flag-listed", args, false); err == nil ||
			len(*e.calls) != 0 || *e.reads != 0 {
			t.Fatalf("unconfirmed: %v", err)
		}
		result, err := e.invoke(t, repositoryFlagsReplace, "flag-listed", args)
		if err != nil || result["replaced"] != true || result["repository"] != "acme/widget" {
			t.Fatalf("%v, %v", result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"PUT " + flagsAPI}) || (*e.calls)[0].body != body {
			t.Errorf("calls = %v body %q", got, (*e.calls)[0].body)
		}
	}
}

func TestRepositoryFlagsClear(t *testing.T) {
	status := 204
	e := newEnvironment(t, forgejo, flagServer(&status))
	if _, err := e.invokeAs(t, repositoryFlagsClear, "flag-listed", `{}`, false); err == nil || len(*e.calls) != 0 ||
		*e.reads != 0 {
		t.Fatalf("unconfirmed: %v", err)
	}
	result, err := e.invoke(t, repositoryFlagsClear, "flag-listed", `{}`)
	if err != nil || result["cleared"] != true || result["repository"] != "acme/widget" {
		t.Fatalf("%v, %v", result, err)
	}
	if got := methods(e); !reflect.DeepEqual(got, []string{"DELETE " + flagsAPI}) || (*e.calls)[0].body != "" {
		t.Errorf("calls = %v body %q", got, (*e.calls)[0].body)
	}
}

func TestRepositoryFlagsReplaceAndClearAreListedOnly(t *testing.T) {
	status := 204
	for tl, args := range map[*tool]string{repositoryFlagsReplace: `{"flags":["a"]}`, repositoryFlagsClear: `{}`} {
		e := newEnvironment(t, forgejo, flagServer(&status))
		if _, err := e.invoke(t, tl, "repo", args); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s without a tools list: %v", tl.action, err)
		}
	}
}

func TestRepositoryFlagChangesAreNeverRepeatedAfterAnUnclearResult(t *testing.T) {
	args := map[*tool]string{repositoryFlagsAdd: `{"flag":"archived"}`, repositoryFlagsRemove: `{"flag":"archived"}`,
		repositoryFlagsReplace: `{"flags":["a"]}`, repositoryFlagsClear: `{}`}
	for _, tl := range flagTools[2:] {
		for _, status := range []int{502, 500} {
			e := newEnvironment(t, forgejo, func(c call) *http.Response { return jsonResponse(status, bodyCanary) })
			_, err := e.invoke(t, tl, flagConnection(tl), args[tl])
			if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls) != 1 {
				t.Errorf("%s %d: %v after %d calls", tl.action, status, err, len(*e.calls))
			}
		}
	}
}

func TestRepositoryFlagToolsRisk(t *testing.T) {
	reg := registry(t)
	effects := map[*tool]capability.Effect{repositoryFlagsList: capability.EffectRead,
		repositoryFlagsCheck: capability.EffectRead, repositoryFlagsAdd: capability.EffectUpdate,
		repositoryFlagsRemove: capability.EffectUpdate, repositoryFlagsReplace: capability.EffectUpdate,
		repositoryFlagsClear: capability.EffectDelete}
	for _, tl := range flagTools {
		d, _, ok := reg.Lookup(forgejo.toolID(tl))
		if !ok {
			t.Fatalf("%s is not registered", forgejo.toolID(tl))
		}
		change := effects[tl] != capability.EffectRead
		if d.Risk.Effect != effects[tl] || d.Risk.Idempotency == "" || !d.Risk.OpenWorld ||
			change != (d.Risk.Confirmation == capability.ConfirmationRequired) ||
			d.RequiresToolAllowList != (tl == repositoryFlagsReplace || tl == repositoryFlagsClear) {
			t.Errorf("%s risk = %+v listed-only %t", d.ID, d.Risk, d.RequiresToolAllowList)
		}
		if change && !strings.Contains(d.Description, "instance administrator") {
			t.Errorf("%s does not name the administrator requirement", d.ID)
		}
	}
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}
