package forge

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const reactionFixture = `{"content":"+1","created_at":"2030-01-02T03:04:05Z","user":{"id":3,"login":"alice"}}`

// reactionServer answers the issues and comments of acme/widget (see issueServer and commentServer) and the
// reaction routes below them. The change handler answers a POST and a DELETE of the reaction routes.
func reactionServer(p *product, change func(call) *http.Response) func(call) *http.Response {
	comments := commentServer(nil)
	issues := issueServer(p, "open", nil)
	return func(c call) *http.Response {
		if strings.HasSuffix(c.path, "/reactions") {
			if c.method == http.MethodGet {
				return jsonResponse(200, `[`+reactionFixture+`,`+reactionFixture+`]`, "X-Total-Count", "5")
			}
			return change(c)
		}
		if strings.Contains(c.path, "/issues/comments/") {
			return comments(c)
		}
		return issues(c)
	}
}

func TestIssueReactionsListPagesAndShowsEveryMember(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, reactionServer(p, nil))
		result, err := e.invoke(t, issueReactionsList, "repo", `{"number":8,"limit":2}`)
		reactions, _ := result["reactions"].([]any)
		if err != nil || len(reactions) != 2 || result["has_more"] != true || result["next_cursor"] == nil {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		first, _ := reactions[0].(map[string]any)
		user, _ := first["user"].(map[string]any)
		if first["content"] != "+1" || first["created_at"] == nil || user["login"] != "alice" {
			t.Errorf("%s reaction = %v", p.id, first)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/issues/8/reactions"}) ||
			(*e.calls)[0].query != "limit=2&page=1" {
			t.Errorf("%s calls = %v %q", p.id, got, (*e.calls)[0].query)
		}
		cursor, _ := result["next_cursor"].(string)
		if _, err := e.invoke(t, issueReactionsList, "repo", `{"number":8,"cursor":"`+cursor+`"}`); err != nil ||
			(*e.calls)[1].query != "limit=2&page=2" {
			t.Errorf("%s: continuation: %v %q", p.id, err, (*e.calls)[1].query)
		}
		if _, err := e.invoke(t, issueReactionsList, "repo", `{"number":7,"cursor":"`+cursor+`"}`); !isInvalidRequest(err) {
			t.Errorf("%s: cursor of another issue: %v", p.id, err)
		}
	}
}

func TestIssueReactionAddReadsFirstAndReportsExisting(t *testing.T) {
	for _, p := range products {
		status := 201
		e := newEnvironment(t, p, reactionServer(p, func(c call) *http.Response {
			if c.method == http.MethodPost {
				return jsonResponse(status, reactionFixture)
			}
			return notFoundResponse()
		}))
		if _, err := e.invokeAs(t, issueReactionsAdd, "repo", `{"number":7,"content":"+1"}`, false); err == nil ||
			len(*e.calls) != 0 {
			t.Fatalf("%s: unconfirmed add: %v", p.id, err)
		}
		result, err := e.invoke(t, issueReactionsAdd, "repo", `{"number":7,"content":"+1"}`)
		if err != nil || result["added"] != true {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/issues/7",
			"POST /api/v1/repos/acme/widget/issues/7/reactions"}) ||
			!reflect.DeepEqual(bodyOf(t, (*e.calls)[1]), map[string]any{"content": "+1"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		status = 200
		if result, err = e.invoke(t, issueReactionsAdd, "repo", `{"number":8,"content":"+1"}`); err != nil ||
			result["added"] != false {
			t.Errorf("%s: existing reaction on a pull request: %v, %v", p.id, result, err)
		}
		// An issue whose canonical repository lies outside the targets is refused before the change.
		before := len(*e.calls)
		if _, err = e.invoke(t, issueReactionsAdd, "repo", `{"number":9,"content":"+1"}`); err == nil ||
			strings.Contains(err.Error(), "place") {
			t.Errorf("%s: foreign issue: %v", p.id, err)
		}
		for _, c := range (*e.calls)[before:] {
			if c.method != http.MethodGet {
				t.Errorf("%s: change request %s %s", p.id, c.method, c.path)
			}
		}
		before, reads := len(*e.calls), *e.reads
		for _, bad := range []string{`{"number":7}`, `{"number":7,"content":""}`, `{"content":"+1"}`,
			`{"number":0,"content":"+1"}`, `{"number":7,"content":"+1","user":"bob"}`,
			`{"repository":"secret-owner/hidden","number":7,"content":"+1"}`} {
			if _, err := e.invoke(t, issueReactionsAdd, "repo", bad); !isInvalidRequest(err) || len(*e.calls) != before ||
				*e.reads != reads || strings.Contains(err.Error(), "secret-owner") {
				t.Errorf("%s %s: %v", p.id, bad, err)
			}
		}
	}
}

func TestIssueReactionRemoveSendsOnlyTheContent(t *testing.T) {
	for _, p := range products {
		for _, status := range []int{200, 204} {
			e := newEnvironment(t, p, reactionServer(p, func(c call) *http.Response {
				if c.method == http.MethodDelete {
					return jsonResponse(status, "")
				}
				return notFoundResponse()
			}))
			result, err := e.invoke(t, issueReactionsRemove, "repo", `{"number":7,"content":"heart"}`)
			if err != nil || result["removed"] != true || result["number"] != float64(7) {
				t.Fatalf("%s: %v, %v", p.id, result, err)
			}
			if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/issues/7",
				"DELETE /api/v1/repos/acme/widget/issues/7/reactions"}) ||
				!reflect.DeepEqual(bodyOf(t, (*e.calls)[1]), map[string]any{"content": "heart"}) {
				t.Errorf("%s calls = %v body %q", p.id, got, (*e.calls)[1].body)
			}
			if _, err := e.invoke(t, issueReactionsRemove, "repo", `{"number":7,"content":"heart","user":"bob"}`); !isInvalidRequest(err) {
				t.Errorf("%s: a user argument was accepted: %v", p.id, err)
			}
		}
	}
}

func TestCommentReactionsBindTheIDBeforeEveryCall(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, reactionServer(p, func(c call) *http.Response {
			if c.method == http.MethodPost {
				return jsonResponse(201, reactionFixture)
			}
			return jsonResponse(204, "")
		}))
		result, err := e.invoke(t, commentReactionsList, "repo", `{"id":42}`)
		if reactions, _ := result["reactions"].([]any); err != nil || len(reactions) != 2 {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if result, err = e.invoke(t, commentReactionsAdd, "repo", `{"id":43,"content":"eyes"}`); err != nil ||
			result["added"] != true {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if result, err = e.invoke(t, commentReactionsRemove, "repo", `{"id":42,"content":"eyes"}`); err != nil ||
			result["removed"] != true || result["id"] != float64(42) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		want := []string{"GET /api/v1/repos/acme/widget/issues/comments/42",
			"GET /api/v1/repos/acme/widget/issues/comments/42/reactions",
			"GET /api/v1/repos/acme/widget/issues/comments/43",
			"POST /api/v1/repos/acme/widget/issues/comments/43/reactions",
			"GET /api/v1/repos/acme/widget/issues/comments/42",
			"DELETE /api/v1/repos/acme/widget/issues/comments/42/reactions"}
		if got := methods(e); !reflect.DeepEqual(got, want) ||
			!reflect.DeepEqual(bodyOf(t, (*e.calls)[3]), map[string]any{"content": "eyes"}) ||
			!reflect.DeepEqual(bodyOf(t, (*e.calls)[5]), map[string]any{"content": "eyes"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		// A comment of another repository, one without proof, one with another ID, and one unknown are refused;
		// only the evidence read goes out.
		before := len(*e.calls)
		for _, id := range []string{"44", "45", "46", "47"} {
			for _, t2 := range []struct {
				tool *tool
				args string
			}{{commentReactionsList, `{"id":` + id + `}`}, {commentReactionsAdd, `{"id":` + id + `,"content":"+1"}`},
				{commentReactionsRemove, `{"id":` + id + `,"content":"+1"}`}} {
				if _, err := e.invoke(t, t2.tool, "repo", t2.args); err == nil || strings.Contains(err.Error(), "place") {
					t.Errorf("%s %s: %v", p.id, t2.args, err)
				}
			}
		}
		for _, c := range (*e.calls)[before:] {
			if c.method != http.MethodGet || strings.HasSuffix(c.path, "/reactions") {
				t.Errorf("%s: request %s %s for a foreign comment", p.id, c.method, c.path)
			}
		}
		before, reads := len(*e.calls), *e.reads
		if _, err := e.invoke(t, commentReactionsAdd, "repo", `{"repository":"secret-owner/hidden","id":42,"content":"+1"}`); !isInvalidRequest(err) ||
			len(*e.calls) != before || *e.reads != reads || strings.Contains(err.Error(), "secret-owner") {
			t.Errorf("%s: %v", p.id, err)
		}
	}
}

func TestReactionChangeIsNotRepeatedAfterAnUnclearResult(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, reactionServer(p, func(call) *http.Response {
			return jsonResponse(502, "bad gateway")
		}))
		for _, tool := range []*tool{issueReactionsAdd, issueReactionsRemove} {
			before := len(*e.calls)
			_, err := e.invoke(t, tool, "repo", `{"number":7,"content":"+1"}`)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(*e.calls)-before != 2 {
				t.Errorf("%s %s: %v, %d calls", p.id, tool.action, err, len(*e.calls)-before)
			}
		}
	}
}

func TestReactionToolsAreNotListedOnly(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		for _, tool := range []*tool{issueReactionsRemove, commentReactionsRemove, issueReactionsAdd, commentReactionsAdd} {
			d, _, ok := reg.Lookup(p.toolID(tool))
			if !ok || d.RequiresToolAllowList || d.Risk.Confirmation != "required" {
				t.Errorf("%s descriptor = %+v", p.toolID(tool), d)
			}
		}
	}
}
