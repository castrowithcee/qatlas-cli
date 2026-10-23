package forge

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func metaRepo(fullName string) string {
	return `{"id":1,"name":"` + fullName[strings.Index(fullName, "/")+1:] + `","full_name":"` + fullName + `"}`
}

func metaServer(search string) func(call) *http.Response {
	return func(c call) *http.Response {
		const base = "/api/v1/repos/acme/widget"
		switch c.path {
		case "/api/v1/repos/search":
			return jsonResponse(200, search, "X-Total-Count", "9")
		case base + "/languages":
			return jsonResponse(200, `{"Go":1200,"Shell":"bad"}`)
		case base + "/activities/feeds":
			return jsonResponse(200, `[{"id":1,"op_type":"commit_repo","act_user":`+commitUser+`,"content":"x","repo":`+
				metaRepo("acme/widget")+`},{"id":2,"op_type":"commit_repo","repo":`+metaRepo("other/place")+`},`+
				`{"id":3,"op_type":"star_repo"}]`, "X-Total-Count", "3")
		case base + "/signing-key.gpg", base + "/signing-key.pub":
			return textResponse(200, "-----BEGIN PGP PUBLIC KEY BLOCK-----\nabc\n")
		}
		return notFoundResponse()
	}
}

func TestRepositoriesSearchWrapperRowsAndFilters(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, metaServer(`{"ok":true,"data":[`+metaRepo("acme/widget")+`,`+metaRepo("other/place")+`]}`))
		result, err := e.invoke(t, repositoriesSearch, "repo",
			`{"q":"w","topic":true,"include_description":true,"mode":"source","sort":"stars","order":"desc","limit":5}`)
		rows, _ := result["repositories"].([]any)
		if err != nil || len(rows) != 1 || rows[0].(map[string]any)["full_name"] != "acme/widget" ||
			result["has_more"] != true {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		q, _ := url.ParseQuery((*e.calls)[0].query)
		if q.Get("q") != "w" || q.Get("topic") != "true" || q.Get("includeDesc") != "true" || q.Get("mode") != "source" ||
			q.Get("sort") != "stars" || q.Get("order") != "desc" || q.Get("limit") != "5" || q.Get("uid") != "" {
			t.Errorf("%s query = %v", p.id, q)
		}
		failed := newEnvironment(t, p, metaServer(`{"ok":false,"data":[]}`))
		if _, err := failed.invoke(t, repositoriesSearch, "repo", `{}`); err == nil || isInvalidRequest(err) ||
			strings.Contains(err.Error(), "false") {
			t.Errorf("%s: ok false gave %v", p.id, err)
		}
		for _, bad := range []string{`{"uid":1}`, `{"team_id":1}`, `{"starredBy":1}`, `{"priority_owner_id":1}`,
			`{"mode":"x"}`, `{"sort":"x"}`} {
			if _, err := e.invoke(t, repositoriesSearch, "repo", bad); !isInvalidRequest(err) {
				t.Errorf("%s accepted %s: %v", p.id, bad, err)
			}
		}
		if len(*e.calls) != 1 {
			t.Errorf("%s: calls %v", p.id, methods(e))
		}
	}
}

func TestRepositoryMetaReads(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, metaServer(`{"ok":true,"data":[]}`))
		languages, err := e.invoke(t, repositoryLanguagesList, "repo", `{}`)
		if got, _ := languages["languages"].(map[string]any); err != nil || len(got) != 1 || got["Go"] != float64(1200) {
			t.Errorf("%s languages: %v, %v", p.id, languages, err)
		}
		activities, err := e.invoke(t, repositoryActivitiesList, "repo", `{"date":"2030-01-02"}`)
		rows, _ := activities["activities"].([]any)
		if err != nil || len(rows) != 1 || rows[0].(map[string]any)["act_user"].(map[string]any)["login"] != "alice" {
			t.Errorf("%s activities: %v, %v", p.id, activities, err)
		}
		if q, _ := url.ParseQuery((*e.calls)[1].query); q.Get("date") != "2030-01-02" {
			t.Errorf("%s activity query = %v", p.id, q)
		}
		key, err := e.invoke(t, repositorySigningKeysGet, "repo", `{}`)
		if err != nil || key["format"] != "gpg" || !strings.Contains(key["key"].(string), "PGP") {
			t.Errorf("%s key: %v, %v", p.id, key, err)
		}
		if p == gitea {
			if _, err := e.invoke(t, repositorySigningKeysGet, "repo", `{"format":"pub"}`); err != nil {
				t.Errorf("gitea pub: %v", err)
			}
		}
	}
}

func TestRepositoryMetaRefusalsBeforeAnyRequest(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, metaServer(`{"ok":true,"data":[]}`))
		cases := []struct {
			tool *tool
			args string
		}{
			{repositoryLanguagesList, `{"repository":"other/place"}`},
			{repositoryActivitiesList, `{"repository":"other/place"}`},
			{repositoryActivitiesList, `{"date":"2030-13-45"}`},
			{repositoryActivitiesList, `{"date":"yesterday"}`},
			{repositorySigningKeysGet, `{"repository":"other/place"}`},
			{repositorySigningKeysGet, `{"format":"ssh"}`},
			{repositorySigningKeysGet, `{"format":"x"}`},
		}
		if p == forgejo {
			cases = append(cases, struct {
				tool *tool
				args string
			}{repositorySigningKeysGet, `{"format":"pub"}`})
		}
		for _, c := range cases {
			if _, err := e.invoke(t, c.tool, "repo", c.args); !isInvalidRequest(err) {
				t.Errorf("%s %s %s: %v", p.id, c.tool.group, c.args, err)
			}
		}
		if len(*e.calls) != 0 || *e.reads != 0 {
			t.Errorf("%s: calls %v, token reads %d", p.id, methods(e), *e.reads)
		}
	}
}
