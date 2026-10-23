package forge

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// relationServer answers issues 7 and 8 of acme/widget, 9 of widget that lives elsewhere, and issue 3 of
// acme/gadget, plus the relation routes: a list of one inside issue, one outside, and one that names no
// repository; a change goes to the handler.
func relationServer(p *product, change func(call) *http.Response) func(call) *http.Response {
	issues := issueServer(p, "open", nil)
	return func(c call) *http.Response {
		switch {
		case strings.HasSuffix(c.path, "/dependencies") || strings.HasSuffix(c.path, "/blocks"):
			if c.method == http.MethodGet {
				return jsonResponse(200, `[`+widgetIssue(p, 8, "open")+`,`+issueFixture(p, 9, "open", false, "other/place")+
					`,{"number":12,"title":"no repository"}]`, "X-Total-Count", "5")
			}
			return change(c)
		case c.path == "/api/v1/repos/acme/gadget/issues/3" && c.method == http.MethodGet:
			return jsonResponse(200, strings.ReplaceAll(issueFixture(p, 3, "open", false, "acme/gadget"), "widget", "gadget"))
		}
		return issues(c)
	}
}

var relations = []struct {
	relation relation
	list     *tool
	add      *tool
	remove   *tool
}{{dependencyRelation, issueDependenciesList, issueDependenciesAdd, issueDependenciesRemove},
	{blockRelation, issueBlocksList, issueBlocksAdd, issueBlocksRemove}}

func TestRelationListShowsForeignIssuesByIdentifierOnly(t *testing.T) {
	for _, p := range products {
		for _, r := range relations {
			e := newEnvironment(t, p, relationServer(p, nil))
			result, err := e.invoke(t, r.list, "repo", `{"number":7,"limit":3}`)
			issues, _ := result["issues"].([]any)
			if err != nil || len(issues) != 3 || result["has_more"] != true || result["next_cursor"] == nil {
				t.Fatalf("%s %s: %v, %v", p.id, r.relation.group, result, err)
			}
			path := "GET /api/v1/repos/acme/widget/issues/7/" + strings.TrimPrefix(r.relation.group, "issue-")
			if got := methods(e); !reflect.DeepEqual(got, []string{path}) || (*e.calls)[0].query != "limit=3&page=1" {
				t.Errorf("%s calls = %v %q", p.id, got, (*e.calls)[0].query)
			}
			inside, _ := issues[0].(map[string]any)
			if inside["title"] != "Crash on start" || inside["withheld"] != nil || inside["body"] == nil {
				t.Errorf("%s inside = %v", p.id, inside)
			}
			outside, _ := issues[1].(map[string]any)
			repository, _ := outside["repository"].(map[string]any)
			if !reflect.DeepEqual(outside, map[string]any{"number": float64(9), "withheld": true,
				"repository": map[string]any{"full_name": "other/place"}}) || repository["id"] != nil {
				t.Errorf("%s outside = %v", p.id, outside)
			}
			unknown, _ := issues[2].(map[string]any)
			if !reflect.DeepEqual(unknown, map[string]any{"number": float64(12), "withheld": true}) {
				t.Errorf("%s unknown = %v", p.id, unknown)
			}
			cursor, _ := result["next_cursor"].(string)
			if _, err := e.invoke(t, r.list, "repo", `{"number":7,"cursor":"`+cursor+`"}`); err != nil ||
				(*e.calls)[1].query != "limit=3&page=2" {
				t.Errorf("%s: continuation: %v %q", p.id, err, (*e.calls)[1].query)
			}
			if _, err := e.invoke(t, r.list, "repo", `{"number":8,"cursor":"`+cursor+`"}`); !isInvalidRequest(err) {
				t.Errorf("%s: cursor of another issue: %v", p.id, err)
			}
			// Without targets everything is shown in full.
			open := newEnvironment(t, p, relationServer(p, nil))
			result, err = open.invoke(t, r.list, "open", `{"repository":"acme/widget","number":7}`)
			if all, _ := result["issues"].([]any); err != nil || len(all) != 3 {
				t.Fatalf("%s: %v, %v", p.id, result, err)
			} else if row, _ := all[1].(map[string]any); row["withheld"] != nil || row["title"] == nil {
				t.Errorf("%s: open connection withheld %v", p.id, row)
			}
		}
	}
}

func TestRelationChangeReadsBothEndsAndSendsOneRequest(t *testing.T) {
	for _, p := range products {
		for _, r := range relations {
			e := newEnvironment(t, p, relationServer(p, func(c call) *http.Response {
				if c.method == http.MethodPost {
					return jsonResponse(201, widgetIssue(p, 7, "open"))
				}
				return jsonResponse(200, widgetIssue(p, 7, "open"))
			}))
			route := "/api/v1/repos/acme/widget/issues/7/" + strings.TrimPrefix(r.relation.group, "issue-")
			args := `{"number":7,"` + r.relation.number + `":8}`
			if _, err := e.invokeAs(t, r.add, "repo", args, false); err == nil || len(*e.calls) != 0 {
				t.Fatalf("%s: unconfirmed add: %v", p.id, err)
			}
			result, err := e.invoke(t, r.add, "repo", args)
			if err != nil || result["added"] != true {
				t.Fatalf("%s %s: %v, %v", p.id, r.relation.group, result, err)
			}
			if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/issues/7",
				"GET /api/v1/repos/acme/widget/issues/8", "POST " + route}) ||
				!reflect.DeepEqual(bodyOf(t, (*e.calls)[2]), map[string]any{"owner": "acme", "repo": "widget", "index": float64(8)}) {
				t.Errorf("%s calls = %v body %q", p.id, got, (*e.calls)[2].body)
			}
			// A counterpart in another repository of the targets.
			two := newEnvironment(t, p, relationServer(p, func(call) *http.Response { return jsonResponse(200, "{}") }))
			result, err = two.invoke(t, r.remove, "two", `{"repository":"acme/widget","number":7,"`+r.relation.number+
				`":3,"`+r.relation.repo+`":"acme/gadget"}`)
			if err != nil || result["removed"] != true {
				t.Fatalf("%s: %v, %v", p.id, result, err)
			}
			if got := methods(two); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/issues/7",
				"GET /api/v1/repos/acme/gadget/issues/3", "DELETE " + route}) ||
				!reflect.DeepEqual(bodyOf(t, (*two.calls)[2]), map[string]any{"owner": "acme", "repo": "gadget", "index": float64(3)}) {
				t.Errorf("%s calls = %v body %q", p.id, got, (*two.calls)[2].body)
			}
		}
	}
}

func TestRelationChangeRefusesForeignEndsBeforeAnyChange(t *testing.T) {
	for _, p := range products {
		for _, r := range relations {
			e := newEnvironment(t, p, relationServer(p, func(call) *http.Response { return jsonResponse(200, "{}") }))
			// A counterpart repository outside the targets is refused before the token and any I/O.
			reads := *e.reads
			for _, tool := range []*tool{r.add, r.remove} {
				for _, bad := range []string{
					`{"number":7,"` + r.relation.number + `":3,"` + r.relation.repo + `":"secret-owner/hidden"}`,
					`{"number":7,"` + r.relation.number + `":3,"` + r.relation.repo + `":"acme/gadget"}`,
					`{"number":7,"` + r.relation.number + `":3,"` + r.relation.repo + `":"nonsense"}`,
					`{"number":7}`, `{"number":7,"` + r.relation.number + `":0}`, `{"number":7,"` + r.relation.number + `":7}`,
					`{"repository":"secret-owner/hidden","number":7,"` + r.relation.number + `":3}`,
				} {
					if _, err := e.invoke(t, tool, "repo", bad); !isInvalidRequest(err) || len(*e.calls) != 0 ||
						*e.reads != reads || strings.Contains(err.Error(), "secret-owner") ||
						strings.Contains(err.Error(), "gadget") {
						t.Errorf("%s %s: %v", p.id, bad, err)
					}
				}
			}
			// An issue or counterpart whose canonical repository lies outside, or that is unknown, gets no change.
			for _, args := range []string{`{"number":9,"` + r.relation.number + `":8}`, `{"number":7,"` + r.relation.number + `":9}`,
				`{"number":7,"` + r.relation.number + `":404}`} {
				for _, tool := range []*tool{r.add, r.remove} {
					if _, err := e.invoke(t, tool, "repo", args); err == nil || strings.Contains(err.Error(), "place") {
						t.Errorf("%s %s: %v", p.id, args, err)
					}
				}
			}
			for _, c := range *e.calls {
				if c.method != http.MethodGet {
					t.Errorf("%s: change request %s %s", p.id, c.method, c.path)
				}
			}
		}
	}
}

func TestRelationChangeIsNotRepeatedAfterAnUnclearResult(t *testing.T) {
	for _, p := range products {
		for _, r := range relations {
			e := newEnvironment(t, p, relationServer(p, func(call) *http.Response { return jsonResponse(502, "bad gateway") }))
			for _, tool := range []*tool{r.add, r.remove} {
				before := len(*e.calls)
				_, err := e.invoke(t, tool, "repo", `{"number":7,"`+r.relation.number+`":8}`)
				if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls)-before != 3 {
					t.Errorf("%s %s: %v, %d calls", p.id, tool.action, err, len(*e.calls)-before)
				}
			}
		}
	}
}
