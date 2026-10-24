package forge

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

const orgAPI = "/api/v1/orgs/acme"

func orgPeopleServer(p *product, extra func(call) *http.Response) func(call) *http.Response {
	return orgServer(p, func(c call) *http.Response {
		if extra != nil {
			if response := extra(c); response != nil {
				return response
			}
		}
		q, _ := url.ParseQuery(c.query)
		switch {
		case c.method == http.MethodGet && (c.path == orgAPI+"/members" || c.path == orgAPI+"/public_members" ||
			c.path == orgAPI+"/blocks" || c.path == orgAPI+"/list_blocked"):
			if c.path == orgAPI+"/list_blocked" {
				return jsonResponse(200, `[{"block_id":9,"created_at":"2030-01-02T03:04:05Z"}]`)
			}
			if q.Get("page") == "1" {
				return jsonResponse(200, `[{"id":4,"login":"octo","email":"octo@example.invalid"}]`,
					"Link", `<https://forge.example.test/x?page=2>; rel="next"`)
			}
			return jsonResponse(200, `[null,{"id":5,"login":"hub"}]`)
		case c.method == http.MethodGet && strings.HasSuffix(c.path, "/octo") && strings.Contains(c.path, "/members/"),
			c.method == http.MethodGet && strings.HasSuffix(c.path, "/octo") && strings.Contains(c.path, "/blocks/"):
			return jsonResponse(204, "")
		case c.method == http.MethodGet && c.path == orgAPI+"/members/redir":
			return jsonResponse(303, "", "Location", "https://forge.example.test/api/v1/orgs/acme/public_members/octo")
		case c.method == http.MethodGet && c.path == orgAPI+"/public_members/redir":
			return jsonResponse(204, "")
		case c.method != http.MethodGet:
			return jsonResponse(204, "")
		}
		return notFoundResponse()
	})
}

func TestOrgPeopleListsPageAndProjectPersons(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, orgPeopleServer(p, nil))
		for _, c := range []struct {
			tool *tool
			key  string
			path string
		}{{orgMembersList, "members", "/members"}, {orgPublicMembersList, "members", "/public_members"},
			{orgBlocksList, "blocked", map[bool]string{true: "/list_blocked", false: "/blocks"}[p == forgejo]}} {
			*e.calls = nil
			first, err := e.invoke(t, c.tool, "org", `{"organization":"acme","limit":1}`)
			rows, _ := first[c.key].([]any)
			cursor, _ := first["next_cursor"].(string)
			if err != nil || len(rows) != 1 || first["organization"] != "acme" {
				t.Fatalf("%s %s: %v, %v", p.id, c.path, first, err)
			}
			if c.path == "/list_blocked" {
				if first["has_more"] != false || rows[0].(map[string]any)["block_id"] != float64(9) {
					t.Errorf("%s forgejo blocks: %v", p.id, first)
				}
				continue
			}
			if first["has_more"] != true || cursor == "" || rows[0].(map[string]any)["email"] != "octo@example.invalid" {
				t.Fatalf("%s %s first: %v", p.id, c.path, first)
			}
			second, err := e.invoke(t, c.tool, "org", `{"organization":"acme","cursor":"`+cursor+`"}`)
			if err != nil || len(second[c.key].([]any)) != 1 || second["has_more"] != false {
				t.Errorf("%s %s second: %v, %v", p.id, c.path, second, err)
			}
			if got := e.paths(); got[0] != orgAPI+c.path {
				t.Errorf("%s paths = %v", p.id, got)
			}
		}
	}
}

func TestOrgPeopleToolsRefuseOtherOrganizationsBeforeAnyIO(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, orgPeopleServer(p, nil))
		tools := []*tool{orgMembersList, orgMembersCheck, orgMembersRemove, orgPublicMembersList, orgPublicMembersCheck,
			orgPublicMembersPublicize, orgPublicMembersConceal, orgBlocksList, orgBlocksAdd, orgBlocksRemove}
		if p == gitea {
			tools = append(tools, orgBlocksCheck)
		}
		for _, tl := range tools {
			_, err := e.invoke(t, tl, "org", `{"organization":"`+foreignOrg+`","username":"octo"}`)
			if tl.action == "list" {
				_, err = e.invoke(t, tl, "org", `{"organization":"`+foreignOrg+`"}`)
			}
			refused(t, e, tl.group+"."+tl.action, err)
			if err != nil && strings.Contains(err.Error(), foreignOrg) {
				t.Errorf("%s: quotes the name: %v", p.id, err)
			}
			if tl.action != "list" {
				_, err = e.invoke(t, tl, "org", `{"organization":"acme","username":"a/b"}`)
				refused(t, e, tl.group+"."+tl.action+" bad user", err)
			}
		}
	}
}

func TestOrgChecksAnswerYesNoAndFollowTheMemberRedirect(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, orgPeopleServer(p, nil))
		checks := []struct {
			tool *tool
			key  string
		}{{orgMembersCheck, "member"}, {orgPublicMembersCheck, "public"}}
		if p == gitea {
			checks = append(checks, struct {
				tool *tool
				key  string
			}{orgBlocksCheck, "blocked"})
		}
		for _, c := range checks {
			yes, err := e.invoke(t, c.tool, "org", `{"organization":"acme","username":"octo"}`)
			if c.tool == orgPublicMembersCheck {
				// The fake serves public_members/octo as 404; only members and blocks answer 204 for octo.
				if err != nil || yes[c.key] != false {
					t.Errorf("%s %s: %v, %v", p.id, c.key, yes, err)
				}
			} else if err != nil || yes[c.key] != true {
				t.Errorf("%s %s yes: %v, %v", p.id, c.key, yes, err)
			}
			no, err := e.invoke(t, c.tool, "org", `{"organization":"acme","username":"hub"}`)
			if err != nil || no[c.key] != false {
				t.Errorf("%s %s no: %v, %v", p.id, c.key, no, err)
			}
		}
		*e.calls = nil
		result, err := e.invoke(t, orgMembersCheck, "org", `{"organization":"acme","username":"redir"}`)
		if err != nil || result["member"] != true || result["public_only"] != true ||
			!reflect.DeepEqual(methods(e), []string{"GET " + orgAPI + "/members/redir", "GET " + orgAPI + "/public_members/redir"}) {
			t.Errorf("%s redirect: %v, %v, %v", p.id, result, err, methods(e))
		}
		// Any other refusal is an error, not a no.
		e = newEnvironment(t, p, func(call) *http.Response { return jsonResponse(403, `{"message":"`+bodyCanary+`"}`) })
		if _, err := e.invoke(t, orgMembersCheck, "org", `{"organization":"acme","username":"octo"}`); err == nil {
			t.Errorf("%s: 403 answered", p.id)
		}
	}
}

func TestOrgBlocksCheckIsOfferedOnGiteaOnly(t *testing.T) {
	if _, ok := orgBlocksCheck.since[forgejo.id]; ok {
		t.Error("org-blocks.check is registered for forgejo")
	}
	if _, ok := orgBlocksCheck.since[gitea.id]; !ok {
		t.Error("org-blocks.check is missing for gitea")
	}
}

func TestOrgPeopleChangesUseTheProductPathAndNeedConfirmation(t *testing.T) {
	for _, p := range products {
		blockAdd, blockRemove := "PUT "+orgAPI+"/blocks/octo", "DELETE "+orgAPI+"/blocks/octo"
		if p == forgejo {
			blockAdd, blockRemove = "PUT "+orgAPI+"/block/octo", "PUT "+orgAPI+"/unblock/octo"
		}
		for _, c := range []struct {
			tool *tool
			want string
			key  string
		}{
			{orgMembersRemove, "DELETE " + orgAPI + "/members/octo", "removed"},
			{orgPublicMembersPublicize, "PUT " + orgAPI + "/public_members/octo", "public"},
			{orgPublicMembersConceal, "DELETE " + orgAPI + "/public_members/octo", "concealed"},
			{orgBlocksAdd, blockAdd, "blocked"},
			{orgBlocksRemove, blockRemove, "unblocked"},
		} {
			e := newEnvironment(t, p, orgPeopleServer(p, nil))
			args := `{"organization":"acme","username":"octo"}`
			if _, err := e.invokeAs(t, c.tool, "org", args, false); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s %s unconfirmed: %v", p.id, c.tool.action, err)
			}
			result, err := e.invoke(t, c.tool, "org", args)
			if err != nil || result[c.key] != true || result["username"] != "octo" ||
				!reflect.DeepEqual(methods(e), []string{c.want}) || (*e.calls)[0].body != "" {
				t.Errorf("%s %s: %v, %v, %v", p.id, c.want, result, err, methods(e))
			}
			// One request only, never repeated after a server error.
			e = newEnvironment(t, p, func(call) *http.Response { return jsonResponse(500, `{"message":"`+bodyCanary+`"}`) })
			_, err = e.invoke(t, c.tool, "org", args)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls) != 1 {
				t.Errorf("%s %s 5xx: %v, %v", p.id, c.want, err, methods(e))
			}
		}
	}
}

func TestOrgBlockNoteIsAGiteaQuery(t *testing.T) {
	e := newEnvironment(t, gitea, orgPeopleServer(gitea, nil))
	if _, err := e.invoke(t, orgBlocksAdd, "org", `{"organization":"acme","username":"octo","note":"spam & more"}`); err != nil {
		t.Fatal(err)
	}
	if q, _ := url.ParseQuery((*e.calls)[0].query); q.Get("note") != "spam & more" || len(q) != 1 {
		t.Errorf("query = %q", (*e.calls)[0].query)
	}
	before := len(*e.calls)
	if _, err := e.invoke(t, orgBlocksAdd, "org", `{"organization":"acme","username":"octo","note":"a\nb"}`); !isInvalidRequest(err) ||
		len(*e.calls) != before {
		t.Errorf("control character: %v", err)
	}
	f := newEnvironment(t, forgejo, orgPeopleServer(forgejo, nil))
	if _, err := f.invoke(t, orgBlocksAdd, "org", `{"organization":"acme","username":"octo","note":"x"}`); !isInvalidRequest(err) ||
		len(*f.calls) != 0 {
		t.Errorf("forgejo note: %v", err)
	}
}
