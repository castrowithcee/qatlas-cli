package forge

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const teamAPI = "/api/v1/teams/"

func teamJSON(id int, org string) string {
	orgObject := ""
	if org != "" {
		orgObject = `,"organization":` + orgJSON(org)
	}
	return `{"id":` + itoa(id) + `,"name":"developers","description":"Developers"` + orgObject +
		`,"includes_all_repositories":false,"permission":"write","units":["repo.code"],` +
		`"units_map":{"repo.code":"write"},"can_create_org_repo":true,"visibility":"private"}`
}

// teamServer knows team 7 of acme and team 8 of another organization; extra may answer first.
func teamServer(extra func(call) *http.Response) func(call) *http.Response {
	return func(c call) *http.Response {
		if extra != nil {
			if response := extra(c); response != nil {
				return response
			}
		}
		q, _ := url.ParseQuery(c.query)
		switch {
		case c.method == http.MethodGet && c.path == teamAPI+"7":
			return jsonResponse(200, teamJSON(7, "acme"))
		case c.method == http.MethodGet && c.path == teamAPI+"8":
			return jsonResponse(200, teamJSON(8, foreignOrg))
		case c.method == http.MethodGet && c.path == teamAPI+"9":
			return jsonResponse(200, teamJSON(9, ""))
		case c.method == http.MethodPatch && c.path == teamAPI+"7":
			return jsonResponse(200, teamJSON(7, ""))
		case c.method == http.MethodPost && c.path == "/api/v1/orgs/acme/teams":
			return jsonResponse(201, teamJSON(11, "acme"))
		case c.method == http.MethodGet && c.path == "/api/v1/orgs/acme/teams":
			if q.Get("page") == "1" {
				return jsonResponse(200, `[`+teamJSON(7, "")+`]`, "Link", `<https://forge.example.test/x?page=2>; rel="next"`)
			}
			return jsonResponse(200, `[null,`+teamJSON(12, "")+`]`)
		case c.method == http.MethodGet && c.path == "/api/v1/orgs/acme/teams/search":
			return jsonResponse(200, `{"ok":true,"data":[`+teamJSON(7, "")+`]}`, "X-Total-Count", "1")
		case c.method == http.MethodGet && c.path == "/api/v1/user/teams":
			return jsonResponse(200, `[`+teamJSON(7, "acme")+`,`+teamJSON(8, foreignOrg)+`,`+teamJSON(9, "")+`]`)
		case c.method == http.MethodGet && c.path == teamAPI+"7/members":
			if q.Get("page") == "1" {
				return jsonResponse(200, `[{"id":4,"login":"octo","email":"octo@example.invalid"}]`,
					"Link", `<https://forge.example.test/x?page=2>; rel="next"`)
			}
			return jsonResponse(200, `[null,{"id":5,"login":"hub"}]`)
		case c.method == http.MethodGet && c.path == teamAPI+"7/members/octo":
			return jsonResponse(200, commitUser)
		case c.method == http.MethodGet && c.path == teamAPI+"7/activities/feeds":
			return jsonResponse(200, `[{"id":1,"op_type":"commit_repo","repo":`+metaRepo("acme/widget")+`},`+
				`{"id":2,"op_type":"commit_repo","repo":`+metaRepo("stranger/place")+`},{"id":3,"op_type":"star_repo"}]`)
		case c.method == http.MethodPut || c.method == http.MethodDelete:
			return jsonResponse(204, "")
		}
		return notFoundResponse()
	}
}

// newTeamEnvironment adds the connections these tests need: team-listed names teams.delete for one organization,
// open-listed does so without targets, orgs-users names an organization and a user.
func newTeamEnvironment(t *testing.T, p *product, handler func(call) *http.Response) *environment {
	t.Helper()
	calls := []call{}
	serve(t, &calls, handler)
	reads := 0
	red := &redact.Redactor{}
	cfg := testConfig(p)
	cfg.Connections["team-listed"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"orgs/acme"}, Tools: []string{p.toolID(teamsDelete), p.toolID(teamsGet)}}
	cfg.Connections["repo-listed"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"repos/acme/widget"}, Tools: []string{p.toolID(teamsDelete)}}
	cfg.Connections["org-foreign-repo"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"orgs/acme", "repos/stranger/place"}}
	cfg.Connections["two-orgs"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Targets: []string{"orgs/acme", "orgs/" + foreignOrg}}
	cfg.Connections["open-listed"] = config.Connection{Service: "forge", Credential: "token", Effects: allEffects,
		Tools: []string{p.toolID(teamsDelete), p.toolID(teamsGet)}}
	return &environment{core: application.New(registry(t), cfg, resolver(red, &reads), red), red: red,
		reads: &reads, calls: &calls, p: p}
}

func TestTeamsListPagesBothPathsAndFiltersAccountRows(t *testing.T) {
	for _, p := range products {
		e := newTeamEnvironment(t, p, teamServer(nil))
		first, err := e.invoke(t, teamsList, "org", `{"organization":"acme","limit":1}`)
		rows, _ := first["teams"].([]any)
		cursor, _ := first["next_cursor"].(string)
		if err != nil || len(rows) != 1 || first["has_more"] != true || cursor == "" || first["organization"] != "acme" {
			t.Fatalf("%s first: %v, %v", p.id, first, err)
		}
		second, err := e.invoke(t, teamsList, "org", `{"organization":"acme","cursor":"`+cursor+`"}`)
		if err != nil || len(second["teams"].([]any)) != 1 || second["has_more"] != false {
			t.Errorf("%s second: %v, %v", p.id, second, err)
		}
		if got := e.paths(); got[0] != "/api/v1/orgs/acme/teams" {
			t.Errorf("%s paths = %v", p.id, got)
		}
		// The account's teams: every row is checked against the targets; rows without an organization drop.
		*e.calls = nil
		mine, err := e.invoke(t, teamsList, "org", `{}`)
		got, _ := mine["teams"].([]any)
		if err != nil || len(got) != 1 || got[0].(map[string]any)["id"] != float64(7) || mine["organization"] != nil ||
			strings.Contains(jsonOf(mine), foreignOrg) || e.paths()[0] != "/api/v1/user/teams" {
			t.Errorf("%s account list: %v, %v, %v", p.id, mine, err, e.paths())
		}
		// Without targets every row stays.
		all, err := e.invoke(t, teamsList, "open", `{}`)
		if err != nil || len(all["teams"].([]any)) != 3 {
			t.Errorf("%s open account list: %v, %v", p.id, all, err)
		}
		team := rows[0].(map[string]any)
		if p == gitea && team["visibility"] != "private" || p == forgejo && team["visibility"] != nil {
			t.Errorf("%s visibility = %v", p.id, team["visibility"])
		}
	}
}

func TestTeamsSearchUnwrapsAndSendsFilters(t *testing.T) {
	for _, p := range products {
		e := newTeamEnvironment(t, p, teamServer(nil))
		result, err := e.invoke(t, teamsSearch, "org", `{"organization":"acme","q":"dev","include_desc":false}`)
		if err != nil || len(result["teams"].([]any)) != 1 || result["has_more"] != false {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		q, _ := url.ParseQuery((*e.calls)[0].query)
		if (*e.calls)[0].path != "/api/v1/orgs/acme/teams/search" || q.Get("q") != "dev" || q.Get("include_desc") != "false" ||
			q.Get("page") != "1" {
			t.Errorf("%s call = %+v", p.id, (*e.calls)[0])
		}
		bad := newTeamEnvironment(t, p, func(call) *http.Response { return jsonResponse(200, `{"ok":false,"data":[]}`) })
		if _, err := bad.invoke(t, teamsSearch, "org", `{"organization":"acme"}`); err == nil {
			t.Errorf("%s: failed search answered", p.id)
		}
	}
}

func TestTeamToolsWithoutOrganizationTargetsAreRefusedBeforeAnySecret(t *testing.T) {
	cases := []struct {
		tool *tool
		args string
	}{
		{teamsGet, `{"id":7}`}, {teamsUpdate, `{"id":7,"description":"x"}`}, {teamsDelete, `{"id":7}`},
		{teamActivitiesList, `{"id":7}`}, {teamMembersList, `{"id":7}`}, {teamMembersCheck, `{"id":7,"username":"octo"}`},
		{teamMembersAdd, `{"id":7,"username":"octo"}`}, {teamMembersRemove, `{"id":7,"username":"octo"}`},
		{teamsList, `{}`},
	}
	for _, p := range products {
		e := newTeamEnvironment(t, p, teamServer(nil))
		for _, c := range cases {
			for _, connection := range []string{"repo", "owner", "mixed", "repo-listed"} {
				if connection == "repo-listed" && c.tool != teamsDelete {
					continue
				}
				if c.tool == teamsDelete && connection != "repo-listed" {
					continue
				}
				_, err := e.invoke(t, c.tool, connection, c.args)
				refused(t, e, c.tool.group+"."+c.tool.action+" "+connection, err)
			}
		}
		for _, c := range []struct {
			tool *tool
			args string
		}{{teamsList, `{"organization":"` + foreignOrg + `"}`}, {teamsSearch, `{"organization":"` + foreignOrg + `"}`},
			{teamsCreate, `{"organization":"` + foreignOrg + `","name":"x","permission":"admin"}`}} {
			_, err := e.invoke(t, c.tool, "org", c.args)
			refused(t, e, c.tool.group+"."+c.tool.action, err)
			if err != nil && strings.Contains(err.Error(), foreignOrg) {
				t.Errorf("%s: quotes the name: %v", p.id, err)
			}
		}
	}
}

func TestTeamGetProvesTheOrganizationAndNeverNamesAForeignOne(t *testing.T) {
	for _, p := range products {
		e := newTeamEnvironment(t, p, teamServer(nil))
		got, err := e.invoke(t, teamsGet, "org", `{"id":7}`)
		if err != nil || got["name"] != "developers" || got["units_map"] == nil || len(*e.calls) != 1 {
			t.Fatalf("%s: %v, %v", p.id, got, err)
		}
		*e.calls = nil
		_, err = e.invoke(t, teamsGet, "org", `{"id":8}`)
		if err == nil || classOfError(err) != "not-found" || strings.Contains(strings.ToLower(err.Error()), foreignOrg) ||
			len(*e.calls) != 1 {
			t.Errorf("%s foreign: %v, %v", p.id, err, methods(e))
		}
		// An answer without an organization proves nothing when targets exist.
		if _, err = e.invoke(t, teamsGet, "org", `{"id":9}`); err == nil {
			t.Errorf("%s: unproven team shown", p.id)
		}
		// Without targets the team is shown.
		open, err := e.invoke(t, teamsGet, "open", `{"id":8}`)
		if err != nil || open["id"] != float64(8) {
			t.Errorf("%s open: %v, %v", p.id, open, err)
		}
		// An answer for another team is invalid.
		wrong := newTeamEnvironment(t, p, func(c call) *http.Response { return jsonResponse(200, teamJSON(99, "acme")) })
		if _, err := wrong.invoke(t, teamsGet, "org", `{"id":7}`); err == nil {
			t.Errorf("%s: another team answered", p.id)
		}
	}
}

func TestTeamMutationsReadTheTeamOnceThenChangeOnce(t *testing.T) {
	for _, p := range products {
		for _, c := range []struct {
			tool   *tool
			args   string
			method string
			path   string
			body   string
		}{
			{teamsUpdate, `{"id":7,"description":"Devs","permission":"read","includes_all_repositories":true}`,
				"PATCH", teamAPI + "7", ""},
			{teamMembersAdd, `{"id":7,"username":"octo"}`, "PUT", teamAPI + "7/members/octo", ""},
			{teamMembersRemove, `{"id":7,"username":"octo"}`, "DELETE", teamAPI + "7/members/octo", ""},
		} {
			e := newTeamEnvironment(t, p, teamServer(nil))
			if _, err := e.invokeAs(t, c.tool, "org", c.args, false); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s %s unconfirmed: %v", p.id, c.tool.action, err)
			}
			if _, err := e.invoke(t, c.tool, "org", c.args); err != nil {
				t.Fatalf("%s %s: %v", p.id, c.tool.action, err)
			}
			if want := []string{"GET " + teamAPI + "7", c.method + " " + c.path}; !reflect.DeepEqual(methods(e), want) {
				t.Errorf("%s %s calls = %v", p.id, c.tool.action, methods(e))
			}
			// Without targets there is no proof read.
			open := newTeamEnvironment(t, p, teamServer(nil))
			if _, err := open.invoke(t, c.tool, "open", c.args); err != nil ||
				!reflect.DeepEqual(methods(open), []string{c.method + " " + c.path}) {
				t.Errorf("%s %s open: %v, %v", p.id, c.tool.action, err, methods(open))
			}
			// A team of another organization is refused after the proof read, with no change request.
			foreign := strings.Replace(c.args, `"id":7`, `"id":8`, 1)
			e = newTeamEnvironment(t, p, teamServer(nil))
			_, err := e.invoke(t, c.tool, "org", foreign)
			if err == nil || strings.Contains(strings.ToLower(err.Error()), foreignOrg) ||
				!reflect.DeepEqual(methods(e), []string{"GET " + teamAPI + "8"}) {
				t.Errorf("%s %s foreign: %v, %v", p.id, c.tool.action, err, methods(e))
			}
			// The change is never repeated after a server error.
			e = newTeamEnvironment(t, p, teamServer(func(cl call) *http.Response {
				if cl.method != http.MethodGet {
					return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
				}
				return nil
			}))
			_, err = e.invoke(t, c.tool, "org", c.args)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls) != 2 {
				t.Errorf("%s %s 5xx: %v, %v", p.id, c.tool.action, err, methods(e))
			}
		}
	}
}

func TestTeamUpdateAndCreateBodies(t *testing.T) {
	for _, p := range products {
		e := newTeamEnvironment(t, p, teamServer(nil))
		result, err := e.invoke(t, teamsUpdate, "org", `{"id":7,"name":"devs","units_map":{"repo.code":"read"},`+
			`"units":["repo.issues"],"can_create_org_repo":false}`)
		if err != nil || result["id"] != float64(7) {
			t.Fatalf("%s update: %v, %v", p.id, result, err)
		}
		body := bodyOf(t, (*e.calls)[1])
		if body["name"] != "devs" || body["can_create_org_repo"] != false || len(body) != 4 ||
			!reflect.DeepEqual(body["units_map"], map[string]any{"repo.code": "read"}) {
			t.Errorf("%s update body = %v", p.id, body)
		}
		*e.calls = nil
		created, err := e.invoke(t, teamsCreate, "org", `{"organization":"acme","name":"developers",`+
			`"permission":"write","units":["repo.code"]}`)
		if err != nil || created["id"] != float64(11) || len(*e.calls) != 1 || (*e.calls)[0].path != "/api/v1/orgs/acme/teams" {
			t.Fatalf("%s create: %v, %v, %v", p.id, created, err, methods(e))
		}
		if body := bodyOf(t, (*e.calls)[0]); body["name"] != "developers" || body["permission"] != "write" ||
			len(body) != 3 {
			t.Errorf("%s create body = %v", p.id, body)
		}
		for _, bad := range []string{
			`{"organization":"acme","name":"x"}`,                                     // no units, not admin
			`{"organization":"acme","name":"a b","permission":"admin"}`,              // bad name
			`{"organization":"acme","permission":"admin"}`,                           // no name
			`{"organization":"acme","name":"x","permission":"owner"}`,                // bad permission
			`{"organization":"acme","name":"x","units_map":{"code":"read"}}`,         // bad unit key
			`{"organization":"acme","name":"x","units_map":{"repo.code":"all"}}`,     // bad level
			`{"organization":"acme","name":"x","units":["repo.code"],"other":true}`,  // unknown argument
			`{"organization":"acme","name":"x","description":"a\u0000b","units":[]}`, // control character
		} {
			n := len(*e.calls)
			if _, err := e.invoke(t, teamsCreate, "org", bad); !isInvalidRequest(err) || len(*e.calls) != n {
				t.Errorf("%s create %s: %v", p.id, bad, err)
			}
		}
		if _, err := e.invoke(t, teamsUpdate, "org", `{"id":7}`); !isInvalidRequest(err) {
			t.Errorf("%s: update without a setting: %v", p.id, err)
		}
	}
}

func TestTeamVisibilityIsGiteaOnly(t *testing.T) {
	g := newTeamEnvironment(t, gitea, teamServer(nil))
	if _, err := g.invoke(t, teamsUpdate, "org", `{"id":7,"visibility":"limited"}`); err != nil ||
		bodyOf(t, (*g.calls)[1])["visibility"] != "limited" {
		t.Errorf("gitea visibility: %v, %v", err, methods(g))
	}
	f := newTeamEnvironment(t, forgejo, teamServer(nil))
	for _, c := range []struct {
		tool *tool
		args string
	}{{teamsUpdate, `{"id":7,"visibility":"limited"}`},
		{teamsCreate, `{"organization":"acme","name":"x","permission":"admin","visibility":"private"}`}} {
		_, err := f.invoke(t, c.tool, "org", c.args)
		refused(t, f, c.tool.action, err)
	}
}

func TestTeamMembersListCheckAndActivities(t *testing.T) {
	for _, p := range products {
		e := newTeamEnvironment(t, p, teamServer(nil))
		first, err := e.invoke(t, teamMembersList, "org", `{"id":7,"limit":1}`)
		rows, _ := first["members"].([]any)
		cursor, _ := first["next_cursor"].(string)
		if err != nil || len(rows) != 1 || first["has_more"] != true || cursor == "" ||
			rows[0].(map[string]any)["email"] != "octo@example.invalid" {
			t.Fatalf("%s first: %v, %v", p.id, first, err)
		}
		second, err := e.invoke(t, teamMembersList, "org", `{"id":7,"cursor":"`+cursor+`"}`)
		if err != nil || len(second["members"].([]any)) != 1 || second["has_more"] != false {
			t.Errorf("%s second: %v, %v", p.id, second, err)
		}
		// A cursor of another team is refused.
		if _, err := e.invoke(t, teamMembersList, "open", `{"id":8,"cursor":"`+cursor+`"}`); !isInvalidRequest(err) {
			t.Errorf("%s: foreign cursor: %v", p.id, err)
		}
		yes, err := e.invoke(t, teamMembersCheck, "org", `{"id":7,"username":"octo"}`)
		no, err2 := e.invoke(t, teamMembersCheck, "org", `{"id":7,"username":"hub"}`)
		if err != nil || err2 != nil || yes["member"] != true || no["member"] != false {
			t.Errorf("%s check: %v %v, %v %v", p.id, yes, err, no, err2)
		}
		for _, bad := range []string{`{"id":7,"username":"a/b"}`, `{"id":7,"username":""}`, `{"id":0,"username":"octo"}`} {
			n := len(*e.calls)
			if _, err := e.invoke(t, teamMembersCheck, "org", bad); !isInvalidRequest(err) || len(*e.calls) != n {
				t.Errorf("%s check %s: %v", p.id, bad, err)
			}
		}
		// Any other refusal of the check is an error, not a no.
		deny := newTeamEnvironment(t, p, teamServer(func(c call) *http.Response {
			if strings.HasSuffix(c.path, "/members/octo") {
				return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
			}
			return nil
		}))
		if _, err := deny.invoke(t, teamMembersCheck, "org", `{"id":7,"username":"octo"}`); err == nil {
			t.Errorf("%s: 500 answered", p.id)
		}
		// Activities keep only rows of the team's organization inside the targets.
		e = newTeamEnvironment(t, p, teamServer(nil))
		feed, err := e.invoke(t, teamActivitiesList, "org", `{"id":7,"date":"2030-01-02"}`)
		acts, _ := feed["activities"].([]any)
		if err != nil || len(acts) != 1 || feed["id"] != float64(7) || strings.Contains(jsonOf(feed), "stranger") ||
			!strings.Contains((*e.calls)[1].query, "date=2030-01-02") {
			t.Errorf("%s activities: %v, %v, %+v", p.id, feed, err, *e.calls)
		}
		if _, err := e.invoke(t, teamActivitiesList, "org", `{"id":7,"date":"2030-13-45"}`); !isInvalidRequest(err) {
			t.Errorf("%s: bad date: %v", p.id, err)
		}
		all, err := e.invoke(t, teamActivitiesList, "open", `{"id":7}`)
		if err != nil || len(all["activities"].([]any)) != 3 {
			t.Errorf("%s open activities: %v, %v", p.id, all, err)
		}
	}
}

func TestTeamsDeleteIsListedOnlyAndInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		d, _, ok := reg.Lookup(p.toolID(teamsDelete))
		if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" {
			t.Fatalf("%s descriptor = %+v", p.id, d)
		}
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if strings.Contains(id, ".teams.") || strings.Contains(id, ".team-") {
					t.Errorf("%s: profile %s holds %s", p.id, profile.ID, id)
				}
			}
		}
		e := newTeamEnvironment(t, p, teamServer(nil))
		if _, err := e.invoke(t, teamsDelete, "org", `{"id":7}`); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: delete without a tools list: %v", p.id, err)
		}
		if _, err := e.invokeAs(t, teamsDelete, "team-listed", `{"id":7}`, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed delete: %v", p.id, err)
		}
		result, err := e.invoke(t, teamsDelete, "team-listed", `{"id":7}`)
		if err != nil || result["deleted"] != true || result["id"] != float64(7) ||
			!reflect.DeepEqual(methods(e), []string{"GET " + teamAPI + "7", "DELETE " + teamAPI + "7"}) {
			t.Errorf("%s delete: %v, %v, %v", p.id, result, err, methods(e))
		}
		*e.calls = nil
		if _, err := e.invoke(t, teamsDelete, "team-listed", `{"id":8}`); err == nil ||
			!reflect.DeepEqual(methods(e), []string{"GET " + teamAPI + "8"}) {
			t.Errorf("%s foreign delete: %v, %v", p.id, err, methods(e))
		}
		*e.calls = nil
		if _, err := e.invoke(t, teamsDelete, "open-listed", `{"id":8}`); err != nil ||
			!reflect.DeepEqual(methods(e), []string{"DELETE " + teamAPI + "8"}) {
			t.Errorf("%s open delete: %v, %v", p.id, err, methods(e))
		}
	}
}
