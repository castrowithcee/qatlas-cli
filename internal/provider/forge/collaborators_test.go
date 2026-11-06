package forge

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const collaboratorsAPI = "/api/v1/repos/acme/widget/collaborators"

// collaboratorServer answers the collaborator routes with the shapes of the Forgejo and Gitea swagger: a list of
// users, 204 or 404 for the check, and the permission object; status is the answer of every change.
func collaboratorServer(status *int) func(call) *http.Response {
	return func(c call) *http.Response {
		switch {
		case c.method != http.MethodGet:
			return jsonResponse(*status, "")
		case c.path == collaboratorsAPI:
			return jsonResponse(200, `[`+userFixture+`,null]`, "Link", `<https://forge.example.test/next>; rel="next"`)
		case c.path == collaboratorsAPI+"/alice":
			return jsonResponse(204, "")
		case c.path == collaboratorsAPI+"/alice/permission":
			return jsonResponse(200, `{"permission":"write","role_name":"write","user":`+userFixture+`}`)
		}
		return notFoundResponse()
	}
}

func TestCollaboratorsListPagesAndShowsEveryUserMember(t *testing.T) {
	for _, p := range products {
		status := 204
		e := newEnvironment(t, p, collaboratorServer(&status))
		result, err := e.invoke(t, collaboratorsList, "repo", `{"limit":2}`)
		users := rowsOf(t, result, "collaborators")
		if err != nil || len(users) != 1 || users[0]["login"] != "octo" || users[0]["email"] == nil ||
			result["has_more"] != true || result["next_cursor"] == nil || result["repository"] != "acme/widget" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET " + collaboratorsAPI}) ||
			(*e.calls)[0].query != "limit=2&page=1" {
			t.Errorf("%s calls = %v %q", p.id, got, (*e.calls)[0].query)
		}
		_, err = e.invoke(t, collaboratorsList, "repo", `{"repository":"other/tool"}`)
		refused(t, newEnvironment(t, p, collaboratorServer(&status)), "foreign repository", err)
	}
}

func TestCollaboratorsAreBoundToTheTargetsBeforeAnyIO(t *testing.T) {
	for _, p := range products {
		status := 204
		for _, tl := range []*tool{collaboratorsList, collaboratorsCheck, collaboratorsGetPermission, collaboratorsAdd,
			collaboratorsRemove} {
			e := newEnvironment(t, p, collaboratorServer(&status))
			_, err := e.invoke(t, tl, "repo", `{"repository":"other/tool","collaborator":"alice","permission":"read"}`)
			refused(t, e, tl.action+" foreign repository", err)
			if err != nil && strings.Contains(err.Error(), "other/tool") {
				t.Errorf("%s: the refusal names the repository: %v", p.id, err)
			}
			_, err = e.invoke(t, tl, "exclude", `{"repository":"acme/gadget","collaborator":"alice","permission":"read"}`)
			refused(t, e, tl.action+" excluded repository", err)
		}
		for _, tl := range []*tool{collaboratorsCheck, collaboratorsGetPermission, collaboratorsAdd, collaboratorsRemove} {
			for _, name := range []string{"", "a/b", "..", "-x", "a b", strings.Repeat("a", 41), "a?sudo=1", "a@b"} {
				e := newEnvironment(t, p, collaboratorServer(&status))
				_, err := e.invoke(t, tl, "repo", `{"collaborator":"`+name+`","permission":"read"}`)
				refused(t, e, tl.action+" collaborator "+name, err)
			}
		}
	}
}

func TestCollaboratorsCheckAnswersABoolean(t *testing.T) {
	for _, p := range products {
		status := 204
		e := newEnvironment(t, p, collaboratorServer(&status))
		yes, err := e.invoke(t, collaboratorsCheck, "repo", `{"collaborator":"alice"}`)
		if err != nil || yes["is_collaborator"] != true || yes["collaborator"] != "alice" {
			t.Fatalf("%s: %v, %v", p.id, yes, err)
		}
		no, err := e.invoke(t, collaboratorsCheck, "repo", `{"collaborator":"bob"}`)
		if err != nil || no["is_collaborator"] != false {
			t.Fatalf("%s: %v, %v", p.id, no, err)
		}
		e = newEnvironment(t, p, func(call) *http.Response { return jsonResponse(403, bodyCanary) })
		if _, err := e.invoke(t, collaboratorsCheck, "repo", `{"collaborator":"alice"}`); err == nil {
			t.Errorf("%s: a 403 is not a plain no", p.id)
		}
	}
}

func TestCollaboratorsGetPermissionShowsEveryMember(t *testing.T) {
	for _, p := range products {
		status := 204
		e := newEnvironment(t, p, collaboratorServer(&status))
		result, err := e.invoke(t, collaboratorsGetPermission, "repo", `{"collaborator":"alice"}`)
		user, _ := result["user"].(map[string]any)
		if err != nil || result["permission"] != "write" || result["role_name"] != "write" || user["login"] != "octo" ||
			user["email"] == nil || result["repository"] != "acme/widget" || result["collaborator"] != "alice" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET " + collaboratorsAPI + "/alice/permission"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		if _, err := e.invoke(t, collaboratorsGetPermission, "repo", `{"collaborator":"bob"}`); classOfError(err) == "" {
			t.Errorf("%s: 404 must be an error", p.id)
		}
	}
}

func TestCollaboratorsAddSendsEachPermissionOnceAndRefusesOthers(t *testing.T) {
	for _, p := range products {
		for _, permission := range []string{"read", "write", "admin"} {
			status := 204
			e := newEnvironment(t, p, collaboratorServer(&status))
			if _, err := e.invokeAs(t, collaboratorsAdd, "repo", `{"collaborator":"carol","permission":"`+permission+`"}`,
				false); err == nil || len(*e.calls) != 0 || *e.reads != 0 {
				t.Fatalf("%s: unconfirmed add: %v", p.id, err)
			}
			result, err := e.invoke(t, collaboratorsAdd, "repo", `{"collaborator":"carol","permission":"`+permission+`"}`)
			if err != nil || result["added"] != true || result["permission"] != permission ||
				result["collaborator"] != "carol" || result["repository"] != "acme/widget" {
				t.Fatalf("%s %s: %v, %v", p.id, permission, result, err)
			}
			if got := methods(e); !reflect.DeepEqual(got, []string{"PUT " + collaboratorsAPI + "/carol"}) ||
				(*e.calls)[0].body != `{"permission":"`+permission+`"}` {
				t.Errorf("%s %s calls = %v body %q", p.id, permission, got, (*e.calls)[0].body)
			}
		}
		status := 204
		for _, args := range []string{`{"collaborator":"carol","permission":"owner"}`,
			`{"collaborator":"carol","permission":"none"}`, `{"collaborator":"carol","permission":"WRITE"}`,
			`{"collaborator":"carol"}`, `{"collaborator":"carol","permission":"read","email":"x"}`} {
			e := newEnvironment(t, p, collaboratorServer(&status))
			_, err := e.invoke(t, collaboratorsAdd, "repo", args)
			refused(t, e, "add "+args, err)
		}
	}
}

func TestCollaboratorsRemoveSendsOneDelete(t *testing.T) {
	for _, p := range products {
		status := 204
		e := newEnvironment(t, p, collaboratorServer(&status))
		if _, err := e.invokeAs(t, collaboratorsRemove, "repo", `{"collaborator":"alice"}`, false); err == nil ||
			len(*e.calls) != 0 || *e.reads != 0 {
			t.Fatalf("%s: unconfirmed remove: %v", p.id, err)
		}
		result, err := e.invoke(t, collaboratorsRemove, "repo", `{"collaborator":"alice"}`)
		if err != nil || result["removed"] != true || result["collaborator"] != "alice" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"DELETE " + collaboratorsAPI + "/alice"}) ||
			(*e.calls)[0].body != "" {
			t.Errorf("%s calls = %v body %q", p.id, got, (*e.calls)[0].body)
		}
	}
}

func TestCollaboratorChangesAreNeverRepeatedAfterAnUnclearResult(t *testing.T) {
	for _, p := range products {
		for _, tl := range []*tool{collaboratorsAdd, collaboratorsRemove} {
			e := newEnvironment(t, p, func(c call) *http.Response { return jsonResponse(502, bodyCanary) })
			args := `{"collaborator":"alice","permission":"read"}`
			if tl == collaboratorsRemove {
				args = `{"collaborator":"alice"}`
			}
			_, err := e.invoke(t, tl, "repo", args)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls) != 1 {
				t.Errorf("%s %s: %v after %d calls", p.id, tl.action, err, len(*e.calls))
			}
		}
	}
}

func TestCollaboratorToolsRisk(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		for _, tl := range []*tool{collaboratorsList, collaboratorsCheck, collaboratorsGetPermission, collaboratorsAdd,
			collaboratorsRemove} {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok {
				t.Fatalf("%s is missing", p.toolID(tl))
			}
			change := tl == collaboratorsAdd || tl == collaboratorsRemove
			if d.RequiresToolAllowList || d.Risk.Idempotency == "" || !d.Risk.OpenWorld ||
				!strings.HasSuffix(d.Risk.DataSensitivity, "-personal-data") ||
				(change != (d.Risk.Confirmation == "required")) || (change != (d.Risk.Effect == "update")) {
				t.Errorf("%s risk = %+v", d.ID, d.Risk)
			}
		}
	}
}
