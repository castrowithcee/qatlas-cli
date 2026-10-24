package forge

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const userFixture = `{"id":5,"login":"octo","full_name":"Octo Cat","email":"octo@example.invalid","is_admin":true,` +
	`"restricted":false,"active":true,"last_login":"2030-01-01T00:00:00Z","login_name":"sso-octo","source_id":4,` +
	`"prohibit_login":true,"pronouns":"they/them","type":"individual","website":"https://octo.example.invalid",` +
	`"followers_count":2}`

func userServer(extra func(call) *http.Response) func(call) *http.Response {
	return func(c call) *http.Response {
		if extra != nil {
			if response := extra(c); response != nil {
				return response
			}
		}
		q, _ := url.ParseQuery(c.query)
		switch {
		case c.path == "/api/v1/user":
			return jsonResponse(200, `{"id":1,"login":"alice"}`)
		case c.path == "/api/v1/users/search":
			return jsonResponse(200, `{"ok":true,"data":[`+userFixture+`]}`, "X-Total-Count", "1")
		case c.path == "/api/v1/users/octo":
			return jsonResponse(200, userFixture)
		case c.path == "/api/v1/users/octo/heatmap":
			return jsonResponse(200, `[{"timestamp":1893456000,"contributions":3}]`)
		case c.path == "/api/v1/users/octo/activities/feeds":
			return jsonResponse(200, `[{"id":1,"op_type":"commit_repo","act_user":`+commitUser+`}]`, "X-Total-Count", "1")
		case c.method == http.MethodGet && (c.path == "/api/v1/users/octo/followers" || c.path == "/api/v1/user/followers" ||
			c.path == "/api/v1/users/octo/following" || c.path == "/api/v1/user/following"):
			if q.Get("page") == "1" {
				return jsonResponse(200, `[`+userFixture+`]`, "Link", `<https://forge.example.test/x?page=2>; rel="next"`)
			}
			return jsonResponse(200, `[`+userFixture+`]`)
		case strings.HasSuffix(c.path, "/following/hub"):
			return jsonResponse(204, "")
		case strings.Contains(c.path, "/following/"):
			return notFoundResponse()
		}
		return notFoundResponse()
	}
}

func TestUserViewsOfOthersAreRefusedWithTargetsBeforeAnyIO(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, userServer(nil))
		cases := []struct {
			tool *tool
			args string
		}{
			{usersSearch, `{"q":"octo"}`}, {usersGet, `{"username":"octo"}`}, {userActivitiesList, `{"username":"octo"}`},
			{userHeatmapGet, `{"username":"octo"}`}, {userFollowersList, `{"username":"octo"}`},
			{userFollowingList, `{"username":"octo"}`}, {userFollowingCheck, `{"username":"octo","target":"hub"}`},
		}
		for _, connection := range []string{"owner", "org", "repo", "mixed", "two"} {
			for _, c := range cases {
				_, err := e.invoke(t, c.tool, connection, c.args)
				refused(t, e, connection+" "+c.tool.action, err)
				if err != nil && (strings.Contains(err.Error(), "octo") || strings.Contains(err.Error(), "hub")) {
					t.Errorf("%s: the refusal quotes a name: %v", p.id, err)
				}
			}
		}
		for _, bad := range []string{`{"username":"a/b"}`, `{"username":""}`} {
			_, err := e.invoke(t, usersGet, "open", bad)
			refused(t, e, bad, err)
		}
	}
}

func TestUserReadsPageAndHideAccountSecurity(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, userServer(nil))
		search, err := e.invoke(t, usersSearch, "open", `{"q":"octo","uid":5}`)
		users, _ := search["users"].([]any)
		if err != nil || len(users) != 1 || search["has_more"] != false ||
			!strings.Contains((*e.calls)[0].query, "q=octo") || !strings.Contains((*e.calls)[0].query, "uid=5") {
			t.Fatalf("%s search: %v, %v", p.id, search, err)
		}
		get, err := e.invoke(t, usersGet, "open", `{"username":"octo"}`)
		if err != nil || get["login"] != "octo" {
			t.Fatalf("%s get: %v, %v", p.id, get, err)
		}
		for _, result := range []string{jsonOf(search), jsonOf(get)} {
			for _, name := range []string{"last_login", "login_name", "source_id", "prohibit_login", "sso-octo"} {
				if strings.Contains(result, name) {
					t.Errorf("%s: the account detail %s is shown", p.id, name)
				}
			}
			if p == forgejo && (!strings.Contains(result, "pronouns") || strings.Contains(result, `"type"`)) {
				t.Errorf("forgejo: pronouns only: %s", result)
			}
			if p == gitea && (strings.Contains(result, "pronouns") || !strings.Contains(result, `"type":"individual"`)) {
				t.Errorf("gitea: type only: %s", result)
			}
		}
		heat, err := e.invoke(t, userHeatmapGet, "open", `{"username":"octo"}`)
		if err != nil || len(heat["heatmap"].([]any)) != 1 || heat["truncated"] != false {
			t.Errorf("%s heatmap: %v, %v", p.id, heat, err)
		}
		feed, err := e.invoke(t, userActivitiesList, "open", `{"username":"octo","only_performed_by":true,"date":"2030-01-02"}`)
		last := (*e.calls)[len(*e.calls)-1].query
		if err != nil || len(feed["activities"].([]any)) != 1 || !strings.Contains(last, "only-performed-by=true") ||
			!strings.Contains(last, "date=2030-01-02") {
			t.Errorf("%s activities: %v, %v, %s", p.id, feed, err, last)
		}
		for _, list := range []*tool{userFollowersList, userFollowingList} {
			first, err := e.invoke(t, list, "open", `{"username":"octo","limit":1}`)
			cursor, _ := first["next_cursor"].(string)
			if err != nil || first["has_more"] != true || cursor == "" {
				t.Fatalf("%s %s first: %v, %v", p.id, list.group, first, err)
			}
			second, err := e.invoke(t, list, "open", `{"username":"octo","cursor":"`+cursor+`"}`)
			if err != nil || second["has_more"] != false {
				t.Errorf("%s %s second: %v, %v", p.id, list.group, second, err)
			}
			if _, err := e.invoke(t, list, "open", `{"cursor":"`+cursor+`"}`); !isInvalidRequest(err) {
				t.Errorf("%s: foreign cursor: %v", p.id, err)
			}
		}
	}
}

func TestUserSearchSortIsForgejoOnly(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, userServer(nil))
		result, err := e.invoke(t, usersSearch, "open", `{"sort":"newest"}`)
		if p == forgejo {
			if err != nil || result == nil || !strings.Contains((*e.calls)[0].query, "sort=newest") {
				t.Errorf("forgejo sort: %v, %v", result, err)
			}
			continue
		}
		refused(t, e, "gitea sort", err)
		_, err = e.invoke(t, usersSearch, "open", `{"sort":"bogus"}`)
		refused(t, e, "bogus sort", err)
	}
}

func TestOwnAccountViewsFollowTheAccountBinding(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, userServer(nil))
		// Targets without the account, or with a repository, are refused before any I/O.
		for _, connection := range []string{"org", "repo", "mixed", "two"} {
			for _, c := range []struct {
				tool *tool
				args string
			}{{userFollowersList, `{}`}, {userFollowingList, `{}`}, {userFollowingCheck, `{"target":"hub"}`},
				{userFollowsAdd, `{"username":"hub"}`}, {userFollowsRemove, `{"username":"hub"}`}} {
				_, err := e.invoke(t, c.tool, connection, c.args)
				refused(t, e, connection+" "+c.tool.group+"."+c.tool.action, err)
			}
		}
		// A users/NAME target is proved by a read of the account.
		list, err := e.invoke(t, userFollowersList, "owner", `{}`)
		if err != nil || len(list["users"].([]any)) != 1 || strings.Join(e.paths(), " ") !=
			"/api/v1/user /api/v1/user/followers" {
			t.Errorf("%s own followers: %v, %v, %v", p.id, list, err, e.paths())
		}
		// Another account behind the token is refused.
		other := newEnvironment(t, p, accountServer("bob", userServer(nil)))
		_, err = other.invoke(t, userFollowingList, "owner", `{}`)
		if !isInvalidRequest(err) || len(*other.calls) != 1 {
			t.Errorf("%s: foreign account: %v, %v", p.id, err, other.paths())
		}
		// Without targets there is no proof read.
		open := newEnvironment(t, p, userServer(nil))
		yes, err := open.invoke(t, userFollowingCheck, "open", `{"target":"hub"}`)
		no, err2 := open.invoke(t, userFollowingCheck, "open", `{"target":"ghost"}`)
		if err != nil || err2 != nil || yes["following"] != true || no["following"] != false ||
			strings.Join(open.paths(), " ") != "/api/v1/user/following/hub /api/v1/user/following/ghost" {
			t.Errorf("%s own check: %v %v, %v %v, %v", p.id, yes, err, no, err2, open.paths())
		}
		named, err := open.invoke(t, userFollowingCheck, "open", `{"username":"octo","target":"hub"}`)
		if err != nil || named["following"] != true || named["username"] != "octo" ||
			(*open.calls)[2].path != "/api/v1/users/octo/following/hub" {
			t.Errorf("%s named check: %v, %v", p.id, named, err)
		}
		deny := newEnvironment(t, p, userServer(func(c call) *http.Response {
			if strings.Contains(c.path, "/following/") {
				return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
			}
			return nil
		}))
		if _, err := deny.invoke(t, userFollowingCheck, "open", `{"target":"hub"}`); err == nil {
			t.Errorf("%s: 500 answered", p.id)
		}
	}
}

func TestFollowChangesNeedConfirmAndSendOneRequest(t *testing.T) {
	for _, p := range products {
		for _, c := range []struct {
			tool   *tool
			method string
		}{{userFollowsAdd, http.MethodPut}, {userFollowsRemove, http.MethodDelete}} {
			e := newEnvironment(t, p, userServer(nil))
			if _, err := e.invokeAs(t, c.tool, "open", `{"username":"hub"}`, false); err == nil || len(*e.calls) != 0 ||
				*e.reads != 0 {
				t.Errorf("%s %s: unconfirmed: %v", p.id, c.tool.action, err)
			}
			for _, bad := range []string{`{"username":"a/b"}`, `{}`} {
				_, err := e.invoke(t, c.tool, "open", bad)
				refused(t, e, bad, err)
			}
			result, err := e.invoke(t, c.tool, "open", `{"username":"hub"}`)
			if err != nil || result["username"] != "hub" || len(*e.calls) != 1 || (*e.calls)[0].method != c.method ||
				(*e.calls)[0].path != "/api/v1/user/following/hub" {
				t.Errorf("%s %s: %v, %v, %v", p.id, c.tool.action, result, err, *e.calls)
			}
			// With the account named, one read and one change.
			bound := newEnvironment(t, p, userServer(nil))
			if _, err := bound.invoke(t, c.tool, "owner", `{"username":"hub"}`); err != nil || len(*bound.calls) != 2 {
				t.Errorf("%s %s bound: %v, %v", p.id, c.tool.action, err, bound.paths())
			}
			// A 5xx is not repeated and says the outcome is open.
			failing := newEnvironment(t, p, func(c call) *http.Response {
				if strings.HasPrefix(c.path, "/api/v1/user/following/") {
					return jsonResponse(500, `{"message":"`+bodyCanary+`"}`)
				}
				return notFoundResponse()
			})
			if _, err := failing.invoke(t, c.tool, "open", `{"username":"hub"}`); err == nil || len(*failing.calls) != 1 {
				t.Errorf("%s %s: 5xx: %v, %d calls", p.id, c.tool.action, err, len(*failing.calls))
			}
		}
	}
}
