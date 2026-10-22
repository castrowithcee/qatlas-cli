package forge

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// commentFixture is the answer of one product for a comment. conversation is the API path the comment names:
// repos/OWNER/REPO/issues/N for an issue, repos/OWNER/REPO/pulls/N for a pull request, empty for none.
func commentFixture(id int, conversation string) string {
	fields := `"id":` + strconv.Itoa(id) + `,"body":"Looks good","user":{"id":3,"login":"alice"},` +
		`"original_author":"","assets":[{"id":1,"name":"log.txt","size":10}],` +
		`"html_url":"https://` + apiHost + `/acme/widget/issues/7#issuecomment-` + strconv.Itoa(id) + `",` +
		`"created_at":"2030-01-02T03:04:05Z","updated_at":"2030-01-02T03:04:06Z"`
	switch {
	case strings.Contains(conversation, "/pulls/"):
		fields += `,"pull_request_url":"https://` + apiHost + `/api/v1/` + conversation + `"`
	case conversation != "":
		fields += `,"issue_url":"https://` + apiHost + `/api/v1/` + conversation + `"`
	}
	return `{` + fields + `}`
}

// commentServer answers the comments of acme/widget: 42 on an issue, 43 on a pull request, 44 in another
// repository, 45 without any address, 46 with addresses that disagree.
func commentServer(change func(call) *http.Response) func(call) *http.Response {
	const base = "/api/v1/repos/acme/widget/issues/comments/"
	return func(c call) *http.Response {
		if c.method == http.MethodGet {
			switch c.path {
			case base + "42":
				return jsonResponse(200, commentFixture(42, "repos/acme/widget/issues/7"))
			case base + "43":
				return jsonResponse(200, commentFixture(43, "repos/ACME/Widget/pulls/8"))
			case base + "44":
				return jsonResponse(200, commentFixture(44, "repos/other/place/issues/1"))
			case base + "45":
				return jsonResponse(200, commentFixture(45, ""))
			case base + "46":
				body := commentFixture(46, "repos/acme/widget/issues/7")
				return jsonResponse(200, body[:len(body)-1]+`,"pull_request_url":"https://`+apiHost+
					`/api/v1/repos/other/place/pulls/2"}`)
			case base + "47":
				return jsonResponse(200, commentFixture(99, "repos/acme/widget/issues/7"))
			}
		}
		if change != nil {
			return change(c)
		}
		return notFoundResponse()
	}
}

func TestCommentGetBindsTheIDToTheRepository(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, commentServer(nil))
		result, err := e.invoke(t, commentsGet, "repo", `{"id":42}`)
		if err != nil || result["id"] != float64(42) || result["body"] != "Looks good" ||
			result["issue_url"] == nil || result["assets"] == nil {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/issues/comments/42"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		// A comment of a pull request is accepted, and names compare without case.
		if result, err = e.invoke(t, commentsGet, "repo", `{"id":43}`); err != nil || result["pull_request_url"] == nil {
			t.Errorf("%s: pull request comment: %v, %v", p.id, result, err)
		}
		// A foreign comment, one without proof, one with disagreeing addresses, and one with another ID are refused.
		for _, id := range []string{"44", "45", "46", "47"} {
			result, err = e.invoke(t, commentsGet, "repo", `{"id":`+id+`}`)
			if result != nil || err == nil || strings.Contains(err.Error(), "other") || strings.Contains(err.Error(), "place") {
				t.Errorf("%s id %s: %v, %v", p.id, id, result, err)
			}
		}
		// A repository outside the targets is refused before the secret and any request.
		before, reads := len(*e.calls), *e.reads
		_, err = e.invoke(t, commentsGet, "repo", `{"repository":"secret-owner/hidden","id":42}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "secret-owner") || len(*e.calls) != before ||
			*e.reads != reads {
			t.Errorf("%s: %v, calls %d, reads %d", p.id, err, len(*e.calls), *e.reads)
		}
		if _, err = e.invoke(t, commentsGet, "open", `{"id":42}`); !isInvalidRequest(err) {
			t.Errorf("%s: a repository is required without a single target: %v", p.id, err)
		}
	}
}

func TestCommentCreateSendsOneRequestOnIssuesAndPullRequests(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, commentServer(func(c call) *http.Response {
			if c.method == http.MethodPost {
				return jsonResponse(201, commentFixture(50, "repos/acme/widget/issues/7"))
			}
			return notFoundResponse()
		}))
		if _, err := e.invokeAs(t, commentsCreate, "repo", `{"number":7,"body":"Hi"}`, false); err == nil ||
			len(*e.calls) != 0 {
			t.Fatalf("%s: unconfirmed create: %v", p.id, err)
		}
		for _, number := range []string{"7", "8"} {
			if _, err := e.invoke(t, commentsCreate, "repo", `{"number":`+number+`,"body":"Hi\nthere"}`); err != nil {
				t.Fatalf("%s: %v", p.id, err)
			}
		}
		calls := *e.calls
		if len(calls) != 2 || calls[0].method != http.MethodPost || calls[0].path != "/api/v1/repos/acme/widget/issues/7/comments" ||
			calls[1].path != "/api/v1/repos/acme/widget/issues/8/comments" ||
			!reflect.DeepEqual(bodyOf(t, calls[0]), map[string]any{"body": "Hi\nthere"}) {
			t.Errorf("%s calls = %+v", p.id, calls)
		}
		before := len(*e.calls)
		for _, bad := range []string{`{"number":7}`, `{"number":7,"body":""}`, `{"body":"x"}`, `{"number":0,"body":"x"}`,
			`{"number":7,"body":"x","updated_at":"2030-01-02T03:04:05Z"}`,
			`{"repository":"secret-owner/hidden","number":7,"body":"x"}`} {
			if _, err := e.invoke(t, commentsCreate, "repo", bad); !isInvalidRequest(err) || len(*e.calls) != before ||
				(strings.Contains(bad, "secret-owner") && strings.Contains(err.Error(), "secret-owner")) {
				t.Errorf("%s %s: %v", p.id, bad, err)
			}
		}
	}
}

func TestCommentUpdateAndDeleteReadFirstAndRefuseForeignComments(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, commentServer(func(c call) *http.Response {
			switch c.method {
			case http.MethodPatch:
				return jsonResponse(200, commentFixture(42, "repos/acme/widget/issues/7"))
			case http.MethodDelete:
				return jsonResponse(204, "")
			}
			return notFoundResponse()
		}))
		if _, err := e.invokeAs(t, commentsUpdate, "repo", `{"id":42,"body":"New"}`, false); err == nil || len(*e.calls) != 0 {
			t.Fatalf("%s: unconfirmed update: %v", p.id, err)
		}
		if _, err := e.invoke(t, commentsUpdate, "repo", `{"id":42,"body":"New"}`); err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		if got := methods(e); !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/issues/comments/42",
			"PATCH /api/v1/repos/acme/widget/issues/comments/42"}) ||
			!reflect.DeepEqual(bodyOf(t, (*e.calls)[1]), map[string]any{"body": "New"}) {
			t.Errorf("%s calls = %v", p.id, got)
		}
		before := len(*e.calls)
		for _, id := range []string{"44", "45", "46"} {
			if _, err := e.invoke(t, commentsUpdate, "repo", `{"id":`+id+`,"body":"x"}`); err == nil {
				t.Errorf("%s: update of %s accepted", p.id, id)
			}
		}
		for _, bad := range []string{`{"id":42}`, `{"id":42,"body":""}`, `{"body":"x"}`,
			`{"id":42,"body":"x","updated_at":"2030-01-02T03:04:05Z"}`} {
			if _, err := e.invoke(t, commentsUpdate, "repo", bad); !isInvalidRequest(err) {
				t.Errorf("%s %s: %v", p.id, bad, err)
			}
		}
		for _, c := range (*e.calls)[before:] {
			if c.method != http.MethodGet {
				t.Errorf("%s: change request %s %s for a foreign comment", p.id, c.method, c.path)
			}
		}
	}
}

func TestCommentDeleteIsListedOnlyAndInNoProfile(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		d, _, ok := reg.Lookup(p.toolID(commentsDelete))
		if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" {
			t.Fatalf("%s descriptor = %+v", p.id, d)
		}
		for _, other := range reg.Provider(p.id) {
			if strings.HasPrefix(other.ID, p.id+".comments.") && other.ID != d.ID && other.RequiresToolAllowList {
				t.Errorf("%s is listed-only too", other.ID)
			}
		}
		metadata, _ := reg.ProviderMetadata(p.id)
		for _, profile := range metadata.Profiles {
			for _, id := range profile.Tools {
				if id == d.ID {
					t.Errorf("%s: profile %s holds comments.delete", p.id, profile.ID)
				}
			}
		}
		e := newEnvironment(t, p, commentServer(func(c call) *http.Response {
			if c.method == http.MethodDelete {
				return jsonResponse(204, "")
			}
			return notFoundResponse()
		}))
		if _, err := e.invoke(t, commentsDelete, "repo", `{"id":42}`); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: delete without a tools list: %v", p.id, err)
		}
		if _, err := e.invokeAs(t, commentsDelete, "delete", `{"id":42}`, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: unconfirmed delete: %v", p.id, err)
		}
		if _, err := e.invoke(t, commentsDelete, "delete", `{"id":44}`); err == nil || len(*e.calls) != 1 {
			t.Errorf("%s: foreign delete: %v, %v", p.id, err, methods(e))
		}
		result, err := e.invoke(t, commentsDelete, "delete", `{"id":42}`)
		if err != nil || result["deleted"] != true || result["id"] != float64(42) {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := methods(e)[1:]; !reflect.DeepEqual(got, []string{"GET /api/v1/repos/acme/widget/issues/comments/42",
			"DELETE /api/v1/repos/acme/widget/issues/comments/42"}) || (*e.calls)[2].body != "" {
			t.Errorf("%s calls = %v", p.id, got)
		}
	}
}

func TestCommentListsAskForOneConversationOrRepository(t *testing.T) {
	for _, p := range products {
		rows := `[` + commentFixture(1, "repos/acme/widget/issues/7") + `,` + commentFixture(2, "repos/other/place/issues/7") +
			`,` + commentFixture(3, "") + `,` + commentFixture(4, "repos/acme/widget/pulls/7") + `]`
		e := newEnvironment(t, p, func(c call) *http.Response {
			switch c.path {
			case "/api/v1/repos/acme/widget/issues/7/comments":
				return jsonResponse(200, rows)
			case "/api/v1/repos/acme/widget/issues/comments":
				return jsonResponse(200, rows, "Link", `<https://`+apiHost+`/x?page=2>; rel="next"`)
			}
			return notFoundResponse()
		})
		result, err := e.invoke(t, commentsList, "repo",
			`{"number":7,"since":"2030-01-01T00:00:00Z","before":"2031-01-01T00:00:00Z"}`)
		list, _ := result["comments"].([]any)
		if err != nil || len(list) != 2 || result["truncated"] != nil {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if c := (*e.calls)[0]; c.method != http.MethodGet || c.query != "before=2031-01-01T00%3A00%3A00Z&since=2030-01-01T00%3A00%3A00Z" {
			t.Errorf("%s call = %+v", p.id, c)
		}
		result, err = e.invoke(t, commentsListRepository, "repo", `{"since":"2030-01-01T00:00:00Z","limit":10}`)
		list, _ = result["comments"].([]any)
		if err != nil || len(list) != 2 || result["has_more"] != true || result["next_cursor"] == nil {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if c := (*e.calls)[1]; c.method != http.MethodGet || c.path != "/api/v1/repos/acme/widget/issues/comments" ||
			c.query != "limit=10&page=1&since=2030-01-01T00%3A00%3A00Z" {
			t.Errorf("%s call = %+v", p.id, c)
		}
		for _, bad := range []string{`{"number":7,"since":"yesterday"}`, `{"number":0}`, `{"since":"x"}`} {
			tl := commentsList
			if strings.HasPrefix(bad, `{"since"`) {
				tl = commentsListRepository
			}
			if _, err := e.invoke(t, tl, "repo", bad); !isInvalidRequest(err) {
				t.Errorf("%s %s: %v", p.id, bad, err)
			}
		}
		before, reads := len(*e.calls), *e.reads
		_, err = e.invoke(t, commentsListRepository, "repo", `{"repository":"secret-owner/hidden"}`)
		if !isInvalidRequest(err) || strings.Contains(err.Error(), "secret-owner") || len(*e.calls) != before ||
			*e.reads != reads {
			t.Errorf("%s: %v", p.id, err)
		}
	}
}

func TestCommentListIsCappedAndSaysSo(t *testing.T) {
	var rows []string
	for i := 1; i <= maxCommentRows+5; i++ {
		rows = append(rows, commentFixture(i, "repos/acme/widget/issues/7"))
	}
	for _, p := range products {
		e := newEnvironment(t, p, func(c call) *http.Response { return jsonResponse(200, "["+strings.Join(rows, ",")+"]") })
		result, err := e.invoke(t, commentsList, "repo", `{"number":7}`)
		list, _ := result["comments"].([]any)
		if err != nil || len(list) != maxCommentRows || result["truncated"] != true {
			t.Errorf("%s: %d comments, %v, %v", p.id, len(list), result["truncated"], err)
		}
	}
}
