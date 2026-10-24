package forge

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

const repoTeamsAPI = "/api/v1/repos/acme/widget/teams"

func teamRepoServer(extra func(call) *http.Response) func(call) *http.Response {
	return teamServer(func(c call) *http.Response {
		if extra != nil {
			if response := extra(c); response != nil {
				return response
			}
		}
		q, _ := url.ParseQuery(c.query)
		switch {
		case c.method == http.MethodGet && c.path == teamAPI+"7/repos":
			if q.Get("page") == "1" {
				return jsonResponse(200, `[`+metaRepo("acme/widget")+`]`, "Link",
					`<https://forge.example.test/x?page=2>; rel="next"`)
			}
			return jsonResponse(200, `[null,`+metaRepo("stranger/place")+`,`+metaRepo("acme/gadget")+`]`)
		case c.method == http.MethodGet && c.path == teamAPI+"7/repos/acme/widget":
			return jsonResponse(200, metaRepo("acme/widget"))
		case c.method == http.MethodGet && c.path == repoTeamsAPI:
			return jsonResponse(200, `[null,`+teamJSON(7, "")+`]`)
		case c.method == http.MethodGet && c.path == repoTeamsAPI+"/developers":
			return jsonResponse(200, teamJSON(7, ""))
		}
		return nil
	})
}

func TestTeamRepositoriesListPagesAndFiltersRows(t *testing.T) {
	for _, p := range products {
		e := newTeamEnvironment(t, p, teamRepoServer(nil))
		first, err := e.invoke(t, teamRepositoriesList, "org", `{"id":7,"limit":1}`)
		rows, _ := first["repositories"].([]any)
		cursor, _ := first["next_cursor"].(string)
		if err != nil || len(rows) != 1 || first["has_more"] != true || cursor == "" {
			t.Fatalf("%s first: %v, %v", p.id, first, err)
		}
		second, err := e.invoke(t, teamRepositoriesList, "org", `{"id":7,"cursor":"`+cursor+`"}`)
		got, _ := second["repositories"].([]any)
		if err != nil || len(got) != 1 || second["has_more"] != false || strings.Contains(jsonOf(second), "stranger") {
			t.Errorf("%s second: %v, %v", p.id, second, err)
		}
		// A team of another organization is refused after the proof read.
		*e.calls = nil
		if _, err := e.invoke(t, teamRepositoriesList, "org", `{"id":8}`); err == nil ||
			!reflect.DeepEqual(methods(e), []string{"GET " + teamAPI + "8"}) {
			t.Errorf("%s foreign: %v, %v", p.id, err, methods(e))
		}
		// Targets without an organization refuse before any secret.
		r := newTeamEnvironment(t, p, teamRepoServer(nil))
		_, err = r.invoke(t, teamRepositoriesList, "repo", `{"id":7}`)
		refused(t, r, "list repo targets", err)
	}
}

func TestTeamRepositoriesAreRefusedOutsideTheTargetsBeforeAnyIO(t *testing.T) {
	for _, p := range products {
		for _, tl := range []*tool{teamRepositoriesCheck, teamRepositoriesAdd, teamRepositoriesRemove} {
			e := newTeamEnvironment(t, p, teamRepoServer(nil))
			for _, c := range []struct{ connection, args string }{
				{"org", `{"id":7,"repository":"stranger/place"}`},              // owner outside the targets
				{"org-foreign-repo", `{"id":7,"repository":"stranger/place"}`}, // repository named, owner not as orgs/
				{"repo", `{"id":7,"repository":"acme/widget"}`},                // no organization target
				{"owner", `{"id":7,"repository":"acme/widget"}`},               // no organization target
				{"org", `{"id":7,"repository":"bad"}`},                         // not OWNER/REPO
				{"org", `{"id":7}`},                                            // no repository, no default
				{"org", `{"id":0,"repository":"acme/widget"}`},                 // bad id
			} {
				_, err := e.invoke(t, tl, c.connection, c.args)
				refused(t, e, tl.action+" "+c.connection+" "+c.args, err)
			}
		}
	}
}

func TestTeamRepositoryChangesNeedBothTargetsAndTheTeamsOwnOrganization(t *testing.T) {
	for _, p := range products {
		for _, c := range []struct {
			tool   *tool
			method string
		}{{teamRepositoriesAdd, "PUT"}, {teamRepositoriesRemove, "DELETE"}} {
			args := `{"id":7,"repository":"acme/widget"}`
			e := newTeamEnvironment(t, p, teamRepoServer(nil))
			if _, err := e.invokeAs(t, c.tool, "org", args, false); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s %s unconfirmed: %v", p.id, c.tool.action, err)
			}
			got, err := e.invoke(t, c.tool, "org", args)
			if err != nil || got["repository"] != "acme/widget" {
				t.Fatalf("%s %s: %v, %v", p.id, c.tool.action, got, err)
			}
			if want := []string{"GET " + teamAPI + "7", c.method + " " + teamAPI + "7/repos/acme/widget"}; !reflect.DeepEqual(methods(e), want) {
				t.Errorf("%s %s calls = %v", p.id, c.tool.action, methods(e))
			}
			// Team of another organization: refused after the proof read, with no change request.
			e = newTeamEnvironment(t, p, teamRepoServer(nil))
			_, err = e.invoke(t, c.tool, "org", `{"id":8,"repository":"acme/widget"}`)
			if err == nil || strings.Contains(strings.ToLower(err.Error()), foreignOrg) ||
				!reflect.DeepEqual(methods(e), []string{"GET " + teamAPI + "8"}) {
				t.Errorf("%s %s foreign team: %v, %v", p.id, c.tool.action, err, methods(e))
			}
			// Repository of another organization than the team's, both inside the targets.
			e = newTeamEnvironment(t, p, teamRepoServer(nil))
			_, err = e.invoke(t, c.tool, "two-orgs", `{"id":7,"repository":"`+foreignOrg+`/place"}`)
			if err == nil || strings.Contains(strings.ToLower(err.Error()), foreignOrg) ||
				!reflect.DeepEqual(methods(e), []string{"GET " + teamAPI + "7"}) {
				t.Errorf("%s %s foreign repository: %v, %v", p.id, c.tool.action, err, methods(e))
			}
			// The change is never repeated after a server error.
			e = newTeamEnvironment(t, p, teamRepoServer(func(cl call) *http.Response {
				if cl.method != http.MethodGet {
					return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
				}
				return nil
			}))
			_, err = e.invoke(t, c.tool, "org", args)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls) != 2 {
				t.Errorf("%s %s 5xx: %v, %v", p.id, c.tool.action, err, methods(e))
			}
		}
	}
}

func TestTeamRepositoriesCheckAnswersYesAndNo(t *testing.T) {
	for _, p := range products {
		e := newTeamEnvironment(t, p, teamRepoServer(nil))
		yes, err := e.invoke(t, teamRepositoriesCheck, "org", `{"id":7,"repository":"acme/widget"}`)
		no, err2 := e.invoke(t, teamRepositoriesCheck, "org", `{"id":7,"repository":"acme/gadget"}`)
		if err != nil || err2 != nil || yes["assigned"] != true || no["assigned"] != false {
			t.Errorf("%s check: %v %v, %v %v", p.id, yes, err, no, err2)
		}
		deny := newTeamEnvironment(t, p, teamRepoServer(func(c call) *http.Response {
			if strings.HasSuffix(c.path, "/repos/acme/widget") {
				return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
			}
			return nil
		}))
		if _, err := deny.invoke(t, teamRepositoriesCheck, "org", `{"id":7,"repository":"acme/widget"}`); err == nil {
			t.Errorf("%s: 500 answered", p.id)
		}
	}
}

func TestRepositoryTeamsNeedTheOwnerAsOrganizationTargetBeforeAnyIO(t *testing.T) {
	for _, p := range products {
		for _, tl := range []*tool{repositoryTeamsList, repositoryTeamsCheck, repositoryTeamsAdd, repositoryTeamsRemove} {
			e := newTeamEnvironment(t, p, teamRepoServer(nil))
			for _, c := range []struct{ connection, repository, team string }{
				{"repo", "acme/widget", "developers"},                // repository only
				{"org-foreign-repo", "stranger/place", "developers"}, // repository named, owner not as orgs/
				{"org", "stranger/place", "developers"},
				{"org", "acme/widget", "a/b"},
				{"org", "acme/widget", ".."},
			} {
				if tl == repositoryTeamsList && c.team != "developers" {
					continue // the list takes no team
				}
				args := `{"repository":"` + c.repository + `","team":"` + c.team + `"}`
				if tl == repositoryTeamsList {
					args = `{"repository":"` + c.repository + `"}`
				}
				_, err := e.invoke(t, tl, c.connection, args)
				refused(t, e, tl.action+" "+c.connection+" "+args, err)
			}
		}
	}
}

func TestRepositoryTeamsListCheckAndChange(t *testing.T) {
	for _, p := range products {
		e := newTeamEnvironment(t, p, teamRepoServer(nil))
		list, err := e.invoke(t, repositoryTeamsList, "org", `{"repository":"acme/widget"}`)
		if err != nil || len(list["teams"].([]any)) != 1 || list["repository"] != "acme/widget" {
			t.Fatalf("%s list: %v, %v", p.id, list, err)
		}
		yes, err := e.invoke(t, repositoryTeamsCheck, "org", `{"repository":"acme/widget","team":"developers"}`)
		no, err2 := e.invoke(t, repositoryTeamsCheck, "org", `{"repository":"acme/widget","team":"ops"}`)
		if err != nil || err2 != nil || yes["assigned"] != true || no["assigned"] != false {
			t.Errorf("%s check: %v %v, %v %v", p.id, yes, err, no, err2)
		}
		for _, c := range []struct {
			tool   *tool
			method string
		}{{repositoryTeamsAdd, "PUT"}, {repositoryTeamsRemove, "DELETE"}} {
			args := `{"repository":"acme/widget","team":"developers"}`
			e := newTeamEnvironment(t, p, teamRepoServer(nil))
			if _, err := e.invokeAs(t, c.tool, "org", args, false); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s %s unconfirmed: %v", p.id, c.tool.action, err)
			}
			if _, err := e.invoke(t, c.tool, "org", args); err != nil ||
				!reflect.DeepEqual(methods(e), []string{c.method + " " + repoTeamsAPI + "/developers"}) {
				t.Errorf("%s %s: %v, %v", p.id, c.tool.action, err, methods(e))
			}
			e = newTeamEnvironment(t, p, teamRepoServer(func(cl call) *http.Response {
				return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
			}))
			if _, err := e.invoke(t, c.tool, "org", args); err == nil || !strings.Contains(err.Error(), "may have been applied") ||
				len(*e.calls) != 1 {
				t.Errorf("%s %s 5xx: %v, %v", p.id, c.tool.action, err, methods(e))
			}
		}
	}
}
