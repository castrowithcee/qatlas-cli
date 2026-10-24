package forge

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func topicsServer(c call) *http.Response {
	const base = "/api/v1/repos/acme/widget/topics"
	switch {
	case c.path == "/api/v1/topics/search":
		return jsonResponse(200, `{"topics":[{"id":1,"topic_name":"cli","repo_count":3,"created":"2030-01-01T00:00:00Z"}]}`,
			"Link", `<http://x/?page=2>; rel="next"`)
	case c.path == base && c.method == http.MethodGet:
		return jsonResponse(200, `{"topics":["cli","go"]}`, "X-Total-Count", "2")
	case strings.HasPrefix(c.path, base) && (c.method == http.MethodPut || c.method == http.MethodDelete):
		return jsonResponse(204, ``)
	case c.path == "/api/v1/gitignore/templates":
		return jsonResponse(200, `["Go","C++"]`)
	case c.path == "/api/v1/gitignore/templates/Go":
		return jsonResponse(200, `{"name":"Go","source":"`+strings.Repeat("a", 70<<10)+`"}`)
	case c.path == "/api/v1/label/templates":
		return jsonResponse(200, `["Default"]`)
	case c.path == "/api/v1/label/templates/Default":
		return jsonResponse(200, `[{"name":"bug","color":"ee0701","description":"d","exclusive":false}]`)
	case c.path == "/api/v1/licenses":
		return jsonResponse(200, `[{"key":"MIT","name":"MIT License","url":"https://example.org/mit"}]`)
	case c.path == "/api/v1/licenses/MIT":
		return jsonResponse(200, `{"key":"MIT","name":"MIT License","body":"text","implementation":"how"}`)
	case c.path == "/api/v1/repos/acme/widget/licenses":
		return jsonResponse(200, `["MIT"]`)
	}
	return notFoundResponse()
}

func TestTopicsSearchOnlyWithoutTargets(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, topicsServer)
		if _, err := e.invoke(t, topicsSearch, "repo", `{"q":"c"}`); !isInvalidRequest(err) {
			t.Errorf("%s: targets did not refuse: %v", p.id, err)
		}
		if len(*e.calls) != 0 || *e.reads != 0 {
			t.Errorf("%s: calls %v, reads %d", p.id, methods(e), *e.reads)
		}
		result, err := e.invoke(t, topicsSearch, "open", `{"q":"c","limit":5}`)
		rows, _ := result["topics"].([]any)
		if err != nil || len(rows) != 1 || result["has_more"] != true || result["next_cursor"] == nil {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		q, _ := url.ParseQuery((*e.calls)[0].query)
		if q.Get("q") != "c" || q.Get("limit") != "5" || q.Get("page") != "1" {
			t.Errorf("%s query = %v", p.id, q)
		}
	}
}

func TestRepositoryTopics(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, topicsServer)
		list, err := e.invoke(t, repositoryTopicsList, "repo", `{}`)
		if got, _ := list["topics"].([]any); err != nil || len(got) != 2 || list["has_more"] != false {
			t.Fatalf("%s list: %v, %v", p.id, list, err)
		}
		if _, err := e.invoke(t, repositoryTopicsSet, "repo", `{"topics":["cli","go"]}`); err != nil {
			t.Fatalf("%s set: %v", p.id, err)
		}
		set := (*e.calls)[1]
		if set.method != http.MethodPut || set.path != "/api/v1/repos/acme/widget/topics" || set.body != `{"topics":["cli","go"]}` {
			t.Errorf("%s set sent %+v", p.id, set)
		}
		if _, err := e.invoke(t, repositoryTopicsSet, "repo", `{"topics":[]}`); err != nil ||
			(*e.calls)[2].body != `{"topics":[]}` {
			t.Errorf("%s empty set: %v %+v", p.id, err, (*e.calls)[2])
		}
		if _, err := e.invoke(t, repositoryTopicsAdd, "repo", `{"topic":"cli"}`); err != nil {
			t.Fatalf("%s add: %v", p.id, err)
		}
		if _, err := e.invoke(t, repositoryTopicsRemove, "repo", `{"topic":"cli"}`); err != nil {
			t.Fatalf("%s remove: %v", p.id, err)
		}
		add, remove := (*e.calls)[3], (*e.calls)[4]
		if add.method != http.MethodPut || add.path != "/api/v1/repos/acme/widget/topics/cli" || add.body != "" ||
			remove.method != http.MethodDelete || remove.path != add.path {
			t.Errorf("%s add/remove sent %+v %+v", p.id, add, remove)
		}
		before := len(*e.calls)
		for _, c := range []struct {
			tool *tool
			args string
		}{
			{repositoryTopicsList, `{"repository":"other/place"}`},
			{repositoryTopicsSet, `{"repository":"other/place","topics":["a"]}`},
			{repositoryTopicsAdd, `{"repository":"other/place","topic":"a"}`},
			{repositoryTopicsRemove, `{"repository":"other/place","topic":"a"}`},
			{repositoryTopicsAdd, `{"topic":"a/b"}`},
			{repositoryTopicsAdd, `{"topic":"../x"}`},
			{repositoryTopicsRemove, `{"topic":"a%2Fb"}`},
			{repositoryTopicsRemove, `{"topic":"` + strings.Repeat("a", 36) + `"}`},
			{repositoryTopicsSet, `{"topics":["` + strings.Repeat("a", 36) + `"]}`},
			{repositoryTopicsSet, `{"topics":["a/b"]}`},
			{repositoryTopicsSet, `{}`},
		} {
			if _, err := e.invoke(t, c.tool, "repo", c.args); !isInvalidRequest(err) {
				t.Errorf("%s accepted %s %s: %v", p.id, c.tool.group, c.args, err)
			}
		}
		if len(*e.calls) != before {
			t.Errorf("%s: refusals sent requests", p.id)
		}
		if _, err := e.invokeAs(t, repositoryTopicsSet, "repo", `{"topics":["a"]}`, false); err == nil ||
			len(*e.calls) != before {
			t.Errorf("%s: set without confirm: %v", p.id, err)
		}
		// Both instances trim and lowercase a topic before they validate it, and set drops empty names and
		// repeats, so mixed case is normalized and sent instead of refused.
		if _, err := e.invoke(t, repositoryTopicsSet, "repo", `{"topics":[" CLI ","cli","","Go-Lang"]}`); err != nil ||
			(*e.calls)[before].body != `{"topics":["cli","go-lang"]}` {
			t.Errorf("%s mixed-case set: %v %+v", p.id, err, (*e.calls)[before])
		}
		added, err := e.invoke(t, repositoryTopicsAdd, "repo", `{"topic":"Upper"}`)
		if err != nil || added["topic"] != "upper" || (*e.calls)[before+1].path != "/api/v1/repos/acme/widget/topics/upper" {
			t.Errorf("%s mixed-case add: %v %v %+v", p.id, added, err, (*e.calls)[before+1])
		}
	}
}

func TestTemplatesNeedNoTargetsAndCapText(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, topicsServer)
		names, err := e.invoke(t, gitignoreTemplatesList, "repo", `{}`)
		if got, _ := names["templates"].([]any); err != nil || len(got) != 2 {
			t.Fatalf("%s gitignore list: %v, %v", p.id, names, err)
		}
		got, err := e.invoke(t, gitignoreTemplatesGet, "repo", `{"name":"Go"}`)
		if err != nil || got["truncated"] != true || len(got["source"].(string)) != maxTemplateText {
			t.Errorf("%s gitignore get: %v", p.id, err)
		}
		if l, err := e.invoke(t, labelTemplatesList, "repo", `{}`); err != nil || len(l["templates"].([]any)) != 1 {
			t.Errorf("%s label list: %v", p.id, err)
		}
		if l, err := e.invoke(t, labelTemplatesGet, "repo", `{"name":"Default"}`); err != nil ||
			l["labels"].([]any)[0].(map[string]any)["color"] != "ee0701" {
			t.Errorf("%s label get: %v", p.id, err)
		}
		if l, err := e.invoke(t, licenseTemplatesList, "repo", `{}`); err != nil ||
			l["templates"].([]any)[0].(map[string]any)["key"] != "MIT" {
			t.Errorf("%s license list: %v", p.id, err)
		}
		if l, err := e.invoke(t, licenseTemplatesGet, "repo", `{"name":"MIT"}`); err != nil || l["body"] != "text" ||
			l["truncated"] != nil {
			t.Errorf("%s license get: %v %v", p.id, l, err)
		}
		before := len(*e.calls)
		for _, name := range []string{"a/b", "..", ".x", "a b", "a%2Fb", ""} {
			if _, err := e.invoke(t, licenseTemplatesGet, "repo", `{"name":"`+name+`"}`); !isInvalidRequest(err) {
				t.Errorf("%s accepted name %q: %v", p.id, name, err)
			}
		}
		if len(*e.calls) != before {
			t.Errorf("%s: refusals sent requests", p.id)
		}
	}
	e := newEnvironment(t, gitea, topicsServer)
	if l, err := e.invoke(t, repositoryLicensesList, "repo", `{}`); err != nil || l["licenses"].([]any)[0] != "MIT" {
		t.Errorf("repository licenses: %v %v", l, err)
	}
	if _, err := e.invoke(t, repositoryLicensesList, "repo", `{"repository":"other/place"}`); !isInvalidRequest(err) {
		t.Errorf("repository licenses outside targets: %v", err)
	}
}
