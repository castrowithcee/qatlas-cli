package forge

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const reviewBase = "/api/v1/repos/acme/widget/pulls/7/reviews"

// reviewFixture is a review of pull request 7 of acme/widget; pull is the web address of the pull request the
// review reports, empty for none.
func reviewFixture(id int, state string, dismissed bool, pull string) string {
	fields := `"id":` + strconv.Itoa(id) + `,"state":"` + state + `","body":"Fine","commit_id":"abc123",` +
		`"user":{"id":3,"login":"alice"},"team":{"id":6,"name":"core"},"comments_count":2,"official":true,` +
		`"stale":false,"dismissed":` + strconv.FormatBool(dismissed) + `,` +
		`"html_url":"https://forge.example.test/acme/widget/pulls/7#issuecomment-1",` +
		`"submitted_at":"2030-01-02T03:04:05Z","updated_at":"2030-01-02T03:04:06Z"`
	if pull != "" {
		fields += `,"pull_request_url":"https://forge.example.test/` + pull + `"`
	}
	return `{` + fields + `}`
}

// reviewCommentFixture is an inline comment; review is its review ID, 0 for none, pull the web address.
func reviewCommentFixture(id, review int, pull string) string {
	fields := `"id":` + strconv.Itoa(id) + `,"body":"Nil check","path":"main.go","position":12,"original_position":0,` +
		`"commit_id":"abc123","original_commit_id":"abc122","diff_hunk":"@@ -1 +1 @@",` +
		`"user":{"id":3,"login":"alice"},"resolver":{"id":4,"login":"bob"},` +
		`"html_url":"https://forge.example.test/acme/widget/pulls/7#issuecomment-` + strconv.Itoa(id) + `",` +
		`"created_at":"2030-01-02T03:04:05Z","updated_at":"2030-01-02T03:04:06Z","extra_lines_count":2`
	if review != 0 {
		fields += `,"pull_request_review_id":` + strconv.Itoa(review)
	}
	if pull != "" {
		fields += `,"pull_request_url":"https://forge.example.test/` + pull + `"`
	}
	return `{` + fields + `}`
}

// reviewServer answers pull request 7 of acme/widget (8 is an issue the instance does not know as a pull
// request). Review 42 is pending, 43 approved, 44 and 45 name another repository and another pull request, 46
// was submitted as a comment, 47 is dismissed, 48 answers with another ID. change answers everything else.
func reviewServer(p *product, change func(call) *http.Response) func(call) *http.Response {
	reviews := map[string]string{
		"42": reviewFixture(42, "PENDING", false, "acme/widget/pulls/7"),
		"43": reviewFixture(43, "APPROVED", false, "ACME/Widget/pulls/7"),
		"44": reviewFixture(44, "COMMENT", false, "other/place/pulls/7"),
		"45": reviewFixture(45, "COMMENT", false, "acme/widget/pulls/9"),
		"46": reviewFixture(46, "COMMENT", false, "acme/widget/pulls/7"),
		"47": reviewFixture(47, "APPROVED", true, "acme/widget/pulls/7"),
		"48": reviewFixture(99, "PENDING", false, "acme/widget/pulls/7"),
	}
	comments := map[string]string{
		"5": reviewCommentFixture(5, 42, "acme/widget/pulls/7"),
		"6": reviewCommentFixture(6, 42, "acme/widget/pulls/9"),
		"7": reviewCommentFixture(7, 43, "acme/widget/pulls/7"),
		"8": reviewCommentFixture(8, 0, ""),
	}
	return func(c call) *http.Response {
		if c.method == http.MethodGet {
			if c.path == "/api/v1/repos/acme/widget/pulls/7" {
				return jsonResponse(200, widgetPull(p, 7))
			}
			if c.path == reviewBase {
				return jsonResponse(200, `[`+reviews["42"]+`,`+reviews["43"]+`]`, "Link", `<`+baseURL+`/x?page=2>; rel="next"`)
			}
			if id, ok := strings.CutPrefix(c.path, reviewBase+"/"); ok {
				if body, found := reviews[id]; found {
					return jsonResponse(200, body)
				}
				if id == "42/comments" {
					return jsonResponse(200, `[`+comments["5"]+`,`+comments["6"]+`,`+comments["7"]+`,`+comments["8"]+`]`)
				}
				if id, ok := strings.CutPrefix(id, "42/comments/"); ok {
					if body, found := comments[id]; found {
						return jsonResponse(200, body)
					}
				}
			}
		}
		if change != nil {
			return change(c)
		}
		return notFoundResponse()
	}
}

func changes(e *environment) []call {
	var out []call
	for _, c := range *e.calls {
		if c.method != http.MethodGet {
			out = append(out, c)
		}
	}
	return out
}

func answerWith(status int, body string) func(call) *http.Response {
	return func(call) *http.Response { return jsonResponse(status, body) }
}

func TestReviewReadsShowEveryMemberAndPage(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, reviewServer(p, nil))
		list, err := e.invoke(t, reviewsList, "repo", `{"number":7}`)
		if err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		rows, _ := list["reviews"].([]any)
		if len(rows) != 2 || list["has_more"] != true || list["next_cursor"] == nil {
			t.Errorf("%s list = %v", p.id, list)
		}
		get, err := e.invoke(t, reviewsGet, "repo", `{"number":7,"id":43}`)
		if err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		for _, key := range []string{"id", "state", "body", "commit_id", "comments_count", "dismissed", "official",
			"stale", "user", "team", "html_url", "pull_request_url", "submitted_at", "updated_at"} {
			if get[key] == nil && key != "dismissed" && key != "stale" {
				t.Errorf("%s review lacks %s: %v", p.id, key, get)
			}
		}
		comments, err := e.invoke(t, reviewCommentsList, "repo", `{"number":7,"id":42}`)
		if err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		// Comment 6 names another pull request and comment 7 another review; neither is shown.
		rows, _ = comments["comments"].([]any)
		if len(rows) != 2 {
			t.Fatalf("%s comments = %v", p.id, comments)
		}
		first := rows[0].(map[string]any)
		if first["path"] != "main.go" || first["resolver"] == nil || first["diff_hunk"] == nil || first["position"] != float64(12) {
			t.Errorf("%s comment = %v", p.id, first)
		}
		if _, ok := first["extra_lines_count"]; ok != (p == forgejo) {
			t.Errorf("%s: extra_lines_count present = %v", p.id, ok)
		}
	}
}

func TestReviewToolsRefuseTargetsBeforeTheToken(t *testing.T) {
	for _, p := range products {
		cases := map[*tool]string{
			reviewsList: `{"number":7}`, reviewsGet: `{"number":7,"id":42}`, reviewsCreate: `{"number":7,"body":"x"}`,
			reviewsSubmit: `{"number":7,"id":42,"event":"COMMENT"}`, reviewsApprove: `{"number":7}`,
			reviewsDelete: `{"number":7,"id":42}`, reviewsDismiss: `{"number":7,"id":42,"message":"x"}`,
			reviewsUndismiss: `{"number":7,"id":42}`, reviewCommentsList: `{"number":7,"id":42}`,
		}
		if p == forgejo {
			cases[reviewCommentsGet] = `{"number":7,"id":42,"comment":5}`
			cases[reviewCommentsCreate] = `{"number":7,"id":42,"path":"a.go","body":"x","new_position":1}`
			cases[reviewCommentsDelete] = `{"number":7,"id":42,"comment":5}`
		} else {
			cases[reviewCommentsReply] = `{"number":7,"review_id":42,"id":5,"body":"x"}`
			cases[reviewCommentsResolve] = `{"number":7,"review_id":42,"id":5}`
			cases[reviewCommentsUnresolve] = `{"number":7,"review_id":42,"id":5}`
		}
		for tl, arguments := range cases {
			e := newEnvironment(t, p, reviewServer(p, nil))
			arguments = `{"repository":"other/place",` + strings.TrimPrefix(arguments, "{")
			_, err := e.invoke(t, tl, "reviews", arguments)
			if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != 0 || strings.Contains(err.Error(), "other") {
				t.Errorf("%s %s: err %v, calls %d, reads %d", p.id, tl.action, err, len(*e.calls), *e.reads)
			}
		}
	}
}

func TestReviewOnAnIssueIndexIsRefusedWithoutAChange(t *testing.T) {
	for _, p := range products {
		cases := map[*tool]string{
			reviewsCreate: `{"number":8,"body":"x"}`, reviewsApprove: `{"number":8}`,
			reviewsSubmit: `{"number":8,"id":42,"event":"COMMENT"}`, reviewsDelete: `{"number":8,"id":42}`,
			reviewsDismiss: `{"number":8,"id":42,"message":"x"}`, reviewsGet: `{"number":8,"id":42}`,
			reviewsList: `{"number":8}`, reviewCommentsList: `{"number":8,"id":42}`,
		}
		for tl, arguments := range cases {
			e := newEnvironment(t, p, reviewServer(p, nil))
			_, err := e.invoke(t, tl, "reviews", arguments)
			if classOfError(err) != "not-found" || len(changes(e)) != 0 || len(*e.calls) != 1 {
				t.Errorf("%s %s: err %v, calls %v", p.id, tl.action, err, methods(e))
			}
		}
	}
}

func TestReviewOfAnotherPullRequestIsRefusedWithoutAChange(t *testing.T) {
	for _, p := range products {
		for _, id := range []string{"44", "45", "48"} {
			cases := map[*tool]string{
				reviewsSubmit: `{"number":7,"id":` + id + `,"event":"COMMENT"}`, reviewsDelete: `{"number":7,"id":` + id + `}`,
				reviewsDismiss: `{"number":7,"id":` + id + `,"message":"x"}`, reviewsUndismiss: `{"number":7,"id":` + id + `}`,
				reviewsApprove: `{"number":7,"review_id":` + id + `}`, reviewsGet: `{"number":7,"id":` + id + `}`,
			}
			if p == forgejo {
				cases[reviewCommentsCreate] = `{"number":7,"id":` + id + `,"path":"a.go","body":"x","new_position":1}`
			} else {
				cases[reviewCommentsReply] = `{"number":7,"review_id":` + id + `,"id":5,"body":"x"}`
				cases[reviewCommentsResolve] = `{"number":7,"review_id":` + id + `,"id":5}`
			}
			for tl, arguments := range cases {
				e := newEnvironment(t, p, reviewServer(p, answerWith(200, `{}`)))
				_, err := e.invoke(t, tl, "reviews", arguments)
				if err == nil || len(changes(e)) != 0 {
					t.Errorf("%s %s review %s: err %v, calls %v", p.id, tl.action, id, err, methods(e))
				}
			}
		}
	}
}

func TestReviewCommentOfAnotherPullRequestOrReviewIsRefusedWithoutAChange(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, reviewServer(p, answerWith(200, `{}`)))
		if p == forgejo {
			for _, comment := range []string{"6", "7"} {
				for tl, arguments := range map[*tool]string{reviewCommentsGet: `{"number":7,"id":42,"comment":` + comment + `}`,
					reviewCommentsDelete: `{"number":7,"id":42,"comment":` + comment + `}`} {
					if _, err := e.invoke(t, tl, "reviews", arguments); !isInvalidRequest(err) {
						t.Errorf("forgejo %s comment %s: %v", tl.action, comment, err)
					}
				}
			}
		} else {
			for _, comment := range []string{"6", "7", "9"} {
				for _, tl := range []*tool{reviewCommentsReply, reviewCommentsResolve, reviewCommentsUnresolve} {
					arguments := `{"number":7,"review_id":42,"id":` + comment + `,"body":"x"}`
					if tl != reviewCommentsReply {
						arguments = `{"number":7,"review_id":42,"id":` + comment + `}`
					}
					if _, err := e.invoke(t, tl, "reviews", arguments); !isInvalidRequest(err) {
						t.Errorf("gitea %s comment %s: %v", tl.action, comment, err)
					}
				}
			}
		}
		if len(changes(e)) != 0 {
			t.Errorf("%s: a change was sent: %v", p.id, methods(e))
		}
	}
}

func TestReviewEventMatrixSendsApprovedOnlyFromApprove(t *testing.T) {
	for _, p := range products {
		answer := answerWith(200, reviewFixture(42, "COMMENT", false, "acme/widget/pulls/7"))
		e := newEnvironment(t, p, reviewServer(p, answer))
		for _, arguments := range []string{`{"number":7,"event":"APPROVED"}`, `{"number":7,"event":"REQUEST_REVIEW"}`,
			`{"number":7,"event":"approved"}`} {
			if _, err := e.invoke(t, reviewsCreate, "reviews", arguments); !isInvalidRequest(err) {
				t.Errorf("%s create %s: %v", p.id, arguments, err)
			}
		}
		for _, arguments := range []string{`{"number":7,"id":42,"event":"APPROVED"}`, `{"number":7,"id":42,"event":"PENDING"}`,
			`{"number":7,"id":42}`} {
			if _, err := e.invoke(t, reviewsSubmit, "reviews", arguments); !isInvalidRequest(err) {
				t.Errorf("%s submit %s: %v", p.id, arguments, err)
			}
		}
		if len(*e.calls) != 0 || *e.reads != 0 {
			t.Fatalf("%s: calls before any request %v, reads %d", p.id, methods(e), *e.reads)
		}
		// create sends the events it knows; a pending review sends none or PENDING as given.
		for event, want := range map[string]string{`,"event":"COMMENT"`: `"event":"COMMENT"`,
			`,"event":"REQUEST_CHANGES"`: `"event":"REQUEST_CHANGES"`, `,"event":"PENDING"`: `"event":"PENDING"`, ``: ``} {
			*e.calls = nil
			if _, err := e.invoke(t, reviewsCreate, "reviews", `{"number":7,"body":"x"`+event+`}`); err != nil {
				t.Fatalf("%s create %s: %v", p.id, event, err)
			}
			sent := changes(e)
			if len(sent) != 1 || sent[0].path != reviewBase || sent[0].method != http.MethodPost ||
				strings.Contains(sent[0].body, "APPROVED") || (want != "" && !strings.Contains(sent[0].body, want)) ||
				(want == "" && strings.Contains(sent[0].body, "event")) {
				t.Errorf("%s create %q: %v", p.id, event, sent)
			}
		}
		// submit sends its event to the pending review.
		*e.calls = nil
		if _, err := e.invoke(t, reviewsSubmit, "reviews", `{"number":7,"id":42,"event":"REQUEST_CHANGES","body":"no"}`); err != nil {
			t.Fatalf("%s submit: %v", p.id, err)
		}
		sent := changes(e)
		if len(sent) != 1 || sent[0].path != reviewBase+"/42" || sent[0].body != `{"body":"no","event":"REQUEST_CHANGES"}` {
			t.Errorf("%s submit: %v", p.id, sent)
		}
		// A review that is not pending is not submitted.
		*e.calls = nil
		if _, err := e.invoke(t, reviewsSubmit, "reviews", `{"number":7,"id":46,"event":"COMMENT"}`); !isInvalidRequest(err) ||
			len(changes(e)) != 0 {
			t.Errorf("%s submit of a final review: %v", p.id, err)
		}
		// approve sends APPROVED as a new review or for a pending one.
		*e.calls = nil
		if _, err := e.invoke(t, reviewsApprove, "reviews", `{"number":7,"body":"ok","commit_id":"abc1234"}`); err != nil {
			t.Fatalf("%s approve: %v", p.id, err)
		}
		sent = changes(e)
		if len(sent) != 1 || sent[0].path != reviewBase || sent[0].body != `{"body":"ok","commit_id":"abc1234","event":"APPROVED"}` {
			t.Errorf("%s approve new: %v", p.id, sent)
		}
		*e.calls = nil
		if _, err := e.invoke(t, reviewsApprove, "reviews", `{"number":7,"review_id":42}`); err != nil {
			t.Fatalf("%s approve pending: %v", p.id, err)
		}
		sent = changes(e)
		if len(sent) != 1 || sent[0].path != reviewBase+"/42" || sent[0].body != `{"event":"APPROVED"}` {
			t.Errorf("%s approve pending: %v", p.id, sent)
		}
		if _, err := e.invoke(t, reviewsApprove, "reviews", `{"number":7,"review_id":42,"commit_id":"abc1234"}`); !isInvalidRequest(err) {
			t.Errorf("%s approve with review and commit: %v", p.id, err)
		}
	}
}

func TestReviewOwnApproveIsAProviderErrorWithoutProviderText(t *testing.T) {
	for _, p := range products {
		for _, arguments := range []string{`{"number":7}`, `{"number":7,"review_id":42}`} {
			e := newEnvironment(t, p, reviewServer(p, answerWith(422, `{"message":"`+bodyCanary+`"}`)))
			_, err := e.invoke(t, reviewsApprove, "reviews", arguments)
			if classOfError(err) != "provider-error" || err == nil || strings.Contains(err.Error(), bodyCanary) ||
				strings.Contains(err.Error(), "may have been applied") || len(changes(e)) != 1 {
				t.Errorf("%s %s: err %v, calls %v", p.id, arguments, err, methods(e))
			}
		}
	}
}

func TestReviewChangesAreNotRepeatedAfterAnOpenOutcome(t *testing.T) {
	for _, p := range products {
		cases := map[*tool]string{reviewsCreate: `{"number":7,"body":"x"}`, reviewsApprove: `{"number":7}`,
			reviewsSubmit: `{"number":7,"id":42,"event":"COMMENT"}`, reviewsDelete: `{"number":7,"id":42}`,
			reviewsDismiss: `{"number":7,"id":42,"message":"x"}`}
		if p == forgejo {
			cases[reviewCommentsCreate] = `{"number":7,"id":42,"path":"a.go","body":"x","new_position":1}`
			cases[reviewCommentsDelete] = `{"number":7,"id":42,"comment":5}`
		} else {
			cases[reviewCommentsReply] = `{"number":7,"review_id":42,"id":5,"body":"x"}`
			cases[reviewCommentsResolve] = `{"number":7,"review_id":42,"id":5}`
		}
		for tl, arguments := range cases {
			e := newEnvironment(t, p, reviewServer(p, answerWith(502, `{"message":"`+bodyCanary+`"}`)))
			_, err := e.invoke(t, tl, "reviews", arguments)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(changes(e)) != 1 {
				t.Errorf("%s %s: err %v, calls %v", p.id, tl.action, err, methods(e))
			}
		}
	}
}

func TestReviewCreateBoundsAndChecksInlineComments(t *testing.T) {
	line := func(path string, extra string) string {
		return `{"number":7,"comments":[{"path":"` + path + `","body":"x"` + extra + `}]}`
	}
	many := strings.Repeat(`{"path":"a.go","body":"x","new_position":1},`, 51)
	for _, p := range products {
		e := newEnvironment(t, p, reviewServer(p, answerWith(200, reviewFixture(50, "PENDING", false, "acme/widget/pulls/7"))))
		for name, arguments := range map[string]string{
			"parent segment": line("../etc/passwd", `,"new_position":1`), "inner parent": line("a/../b", `,"new_position":1`),
			"absolute": line("/etc/passwd", `,"new_position":1`), "backslash": line(`a\\b`, `,"new_position":1`),
			"dot segment": line("./a", `,"new_position":1`), "empty segment": line("a//b", `,"new_position":1`),
			"no line": line("a.go", ``), "both lines": line("a.go", `,"new_position":1,"old_position":2`),
			"zero line": line("a.go", `,"new_position":0`), "too many": `{"number":7,"comments":[` + strings.TrimSuffix(many, ",") + `]}`,
			"bad commit": `{"number":7,"commit_id":"zz"}`, "long text": `{"number":7,"body":"` + strings.Repeat("x", 65537) + `"}`,
		} {
			if _, err := e.invoke(t, reviewsCreate, "reviews", arguments); !isInvalidRequest(err) {
				t.Errorf("%s %s: %v", p.id, name, err)
			}
		}
		if len(*e.calls) != 0 {
			t.Fatalf("%s: requests before validation: %v", p.id, methods(e))
		}
		if _, err := e.invoke(t, reviewsCreate, "reviews", `{"number":7,"commit_id":"abc1234","event":"COMMENT",`+
			`"comments":[{"path":"dir/main.go","body":"Nil","new_position":12},{"path":"b.go","body":"Old","old_position":3}]}`); err != nil {
			t.Fatalf("%s: %v", p.id, err)
		}
		sent := changes(e)
		want := `{"comments":[{"body":"Nil","new_position":12,"old_position":0,"path":"dir/main.go"},` +
			`{"body":"Old","new_position":0,"old_position":3,"path":"b.go"}],"commit_id":"abc1234","event":"COMMENT"}`
		if len(sent) != 1 || sent[0].body != want {
			t.Errorf("%s body = %v, want %s", p.id, sent, want)
		}
	}
}

func TestReviewExtraLinesCountIsForgejoSixteenAndLater(t *testing.T) {
	create := `{"number":7,"comments":[{"path":"a.go","body":"x","new_position":3,"extra_lines_count":2}]}`
	comment := `{"number":7,"id":42,"path":"a.go","body":"x","new_position":3,"extra_lines_count":2}`
	e := newEnvironment(t, gitea, reviewServer(gitea, nil))
	if _, err := e.invoke(t, reviewsCreate, "reviews", create); !isInvalidRequest(err) || len(*e.calls) != 0 {
		t.Errorf("gitea create: %v", err)
	}
	old := newEnvironment(t, forgejo, forgejoAt("15.0.10", reviewServer(forgejo, answerWith(200, reviewFixture(50, "PENDING", false, "acme/widget/pulls/7")))))
	for tl, arguments := range map[*tool]string{reviewsCreate: create, reviewCommentsCreate: comment} {
		_, err := old.invoke(t, tl, "reviews", arguments)
		if err == nil || !strings.Contains(err.Error(), "not available") || !strings.Contains(err.Error(), "16.0.0") ||
			len(changes(old)) != 0 {
			t.Errorf("forgejo 15 %s: %v, calls %v", tl.action, err, methods(old))
		}
	}
	if _, err := old.invoke(t, reviewsCreate, "reviews", `{"number":7,"body":"x"}`); err != nil {
		t.Errorf("forgejo 15 create without the member: %v", err)
	}
	current := newEnvironment(t, forgejo, forgejoAt("16.0.6", reviewServer(forgejo, answerWith(200, reviewFixture(50, "PENDING", false, "acme/widget/pulls/7")))))
	if _, err := current.invoke(t, reviewsCreate, "reviews", create); err != nil {
		t.Errorf("forgejo 16 create: %v", err)
	}
	if sent := changes(current); len(sent) != 1 || !strings.Contains(sent[0].body, `"extra_lines_count":2`) {
		t.Errorf("forgejo 16 body: %v", sent)
	}
}

func TestReviewDismissAndUndismiss(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, reviewServer(p, answerWith(200, reviewFixture(43, "APPROVED", true, "acme/widget/pulls/7"))))
		result, err := e.invoke(t, reviewsDismiss, "reviews", `{"number":7,"id":43,"message":"Outdated","priors":true}`)
		sent := changes(e)
		if err != nil || result["changed"] != true || len(sent) != 1 || sent[0].path != reviewBase+"/43/dismissals" ||
			sent[0].body != `{"message":"Outdated","priors":true}` {
			t.Errorf("%s dismiss: %v %v %v", p.id, result, err, sent)
		}
		*e.calls = nil
		// A dismissed review is returned unchanged without a request.
		result, err = e.invoke(t, reviewsDismiss, "reviews", `{"number":7,"id":47,"message":"again"}`)
		if err != nil || result["changed"] != false || len(changes(e)) != 0 {
			t.Errorf("%s dismiss of a dismissed review: %v %v", p.id, result, err)
		}
		if _, err := e.invoke(t, reviewsDismiss, "reviews", `{"number":7,"id":43}`); !isInvalidRequest(err) {
			t.Errorf("%s dismiss without a message: %v", p.id, err)
		}
		*e.calls = nil
		result, err = e.invoke(t, reviewsUndismiss, "reviews", `{"number":7,"id":47}`)
		sent = changes(e)
		if err != nil || result["changed"] != true || len(sent) != 1 || sent[0].path != reviewBase+"/47/undismissals" ||
			sent[0].body != "" {
			t.Errorf("%s undismiss: %v %v %v", p.id, result, err, sent)
		}
		*e.calls = nil
		if result, err = e.invoke(t, reviewsUndismiss, "reviews", `{"number":7,"id":43}`); err != nil ||
			result["changed"] != false || len(changes(e)) != 0 {
			t.Errorf("%s undismiss of a review that is not dismissed: %v %v", p.id, result, err)
		}
	}
}

func TestReviewDeleteSendsOneDelete(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, reviewServer(p, answerWith(204, ``)))
		result, err := e.invoke(t, reviewsDelete, "reviews", `{"number":7,"id":43}`)
		sent := changes(e)
		if err != nil || result["deleted"] != true || len(sent) != 1 || sent[0].method != http.MethodDelete ||
			sent[0].path != reviewBase+"/43" {
			t.Errorf("%s: %v %v %v", p.id, result, err, sent)
		}
	}
}

func TestForgejoReviewCommentToolsBindTheCommentToItsReview(t *testing.T) {
	e := newEnvironment(t, forgejo, reviewServer(forgejo, func(c call) *http.Response {
		if c.method == http.MethodPost {
			return jsonResponse(200, reviewCommentFixture(9, 42, "acme/widget/pulls/7"))
		}
		return jsonResponse(204, ``)
	}))
	got, err := e.invoke(t, reviewCommentsGet, "reviews", `{"number":7,"id":42,"comment":5}`)
	if err != nil || got["id"] != float64(5) || got["resolver"] == nil {
		t.Errorf("get: %v %v", got, err)
	}
	created, err := e.invoke(t, reviewCommentsCreate, "reviews", `{"number":7,"id":42,"path":"main.go","body":"Nil","old_position":4}`)
	sent := changes(e)
	if err != nil || created["id"] != float64(9) || len(sent) != 1 || sent[0].path != reviewBase+"/42/comments" ||
		sent[0].body != `{"body":"Nil","new_position":0,"old_position":4,"path":"main.go"}` {
		t.Errorf("create: %v %v %v", created, err, sent)
	}
	*e.calls = nil
	deleted, err := e.invoke(t, reviewCommentsDelete, "reviews", `{"number":7,"id":42,"comment":5}`)
	sent = changes(e)
	if err != nil || deleted["deleted"] != true || len(sent) != 1 || sent[0].method != http.MethodDelete ||
		sent[0].path != reviewBase+"/42/comments/5" {
		t.Errorf("delete: %v %v %v", deleted, err, sent)
	}
}

func TestGiteaReviewCommentToolsActOnACommentOfTheReview(t *testing.T) {
	e := newEnvironment(t, gitea, reviewServer(gitea, func(c call) *http.Response {
		if strings.HasSuffix(c.path, "/replies") {
			return jsonResponse(201, reviewCommentFixture(10, 42, "acme/widget/pulls/7"))
		}
		return jsonResponse(204, ``)
	}))
	reply, err := e.invoke(t, reviewCommentsReply, "reviews", `{"number":7,"review_id":42,"id":5,"body":"Done"}`)
	sent := changes(e)
	if err != nil || reply["id"] != float64(10) || len(sent) != 1 ||
		sent[0].path != "/api/v1/repos/acme/widget/pulls/7/comments/5/replies" || sent[0].body != `{"body":"Done"}` {
		t.Errorf("reply: %v %v %v", reply, err, sent)
	}
	for _, action := range []string{"resolve", "unresolve"} {
		*e.calls = nil
		tl := reviewCommentsResolve
		if action == "unresolve" {
			tl = reviewCommentsUnresolve
		}
		result, err := e.invoke(t, tl, "reviews", `{"number":7,"review_id":42,"id":5}`)
		sent := changes(e)
		if err != nil || result["resolved"] != (action == "resolve") || len(sent) != 1 || sent[0].method != http.MethodPost ||
			sent[0].path != "/api/v1/repos/acme/widget/pulls/comments/5/"+action || sent[0].body != "" {
			t.Errorf("%s: %v %v %v", action, result, err, sent)
		}
		// pull request, review, and comments are read before the change.
		if got := e.paths(); !slices.Equal(got[:3], []string{"/api/v1/repos/acme/widget/pulls/7", reviewBase + "/42", reviewBase + "/42/comments"}) {
			t.Errorf("%s reads %v", action, got)
		}
	}
}

func TestReviewListedOnlyToolsAreInNoProfileAndNeedTheToolsList(t *testing.T) {
	reg := registry(t)
	for _, p := range products {
		listed := []*tool{reviewsApprove, reviewsDelete}
		if p == forgejo {
			listed = append(listed, reviewCommentsDelete)
		}
		for _, tl := range listed {
			d, _, ok := reg.Lookup(p.toolID(tl))
			if !ok || !d.RequiresToolAllowList || d.Risk.Confirmation != "required" {
				t.Fatalf("%s descriptor = %+v", p.toolID(tl), d)
			}
			metadata, _ := reg.ProviderMetadata(p.id)
			for _, profile := range metadata.Profiles {
				if slices.Contains(profile.Tools, d.ID) {
					t.Errorf("profile %s holds %s", profile.ID, d.ID)
				}
			}
		}
		for _, tl := range []*tool{reviewsCreate, reviewsSubmit, reviewsDismiss, reviewsUndismiss, reviewsList, reviewsGet} {
			if d, _, ok := reg.Lookup(p.toolID(tl)); !ok || d.RequiresToolAllowList {
				t.Errorf("%s descriptor = %+v", p.toolID(tl), d)
			}
		}
		e := newEnvironment(t, p, reviewServer(p, answerWith(204, ``)))
		for tl, arguments := range map[*tool]string{reviewsApprove: `{"number":7}`, reviewsDelete: `{"number":7,"id":43}`} {
			if _, err := e.invoke(t, tl, "repo", arguments); err == nil || len(*e.calls) != 0 {
				t.Errorf("%s offered without a tools list: %v", p.toolID(tl), err)
			}
		}
	}
}

func TestReviewChangesNeedConfirmation(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, reviewServer(p, nil))
		if _, err := e.invokeAs(t, reviewsCreate, "reviews", `{"number":7,"body":"x"}`, false); err == nil || len(*e.calls) != 0 {
			t.Errorf("%s: %v", p.id, err)
		}
	}
}
