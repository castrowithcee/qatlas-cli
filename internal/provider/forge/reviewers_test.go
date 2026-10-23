package forge

import (
	"net/http"
	"strings"
	"testing"
)

const reviewersBase = "/api/v1/repos/acme/widget/pulls/7/requested_reviewers"

func reviewersServer(p *product, change func(call) *http.Response) func(call) *http.Response {
	return reviewServer(p, func(c call) *http.Response {
		if c.method == http.MethodGet && c.path == "/api/v1/repos/acme/widget/reviewers" {
			return jsonResponse(200, `[{"id":3,"login":"alice","email":"a@example.test"},{"id":4,"login":"bob"}]`)
		}
		if change == nil {
			return notFoundResponse()
		}
		return change(c)
	})
}

func TestReviewersListShowsPeople(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, reviewersServer(p, nil))
		got, err := e.invoke(t, reviewersList, "repo", `{}`)
		rows, _ := got["reviewers"].([]any)
		if err != nil || len(rows) != 2 || len(changes(e)) != 0 {
			t.Errorf("%s: %v %v", p.id, got, err)
		}
	}
}

func TestReviewersRequestAndRemoveSendOneRequest(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, reviewersServer(p, func(c call) *http.Response {
			if c.method == http.MethodPost {
				return jsonResponse(201, `[`+reviewFixture(50, "REQUEST_REVIEW", false, "acme/widget/pulls/7")+`]`)
			}
			return jsonResponse(204, ``)
		}))
		got, err := e.invoke(t, reviewersRequest, "org", `{"repository":"acme/widget","number":7,"reviewers":["alice"],"team_reviewers":["core"]}`)
		sent := changes(e)
		reviews, _ := got["reviews"].([]any)
		if err != nil || len(reviews) != 1 || len(sent) != 1 || sent[0].method != http.MethodPost ||
			sent[0].path != reviewersBase || sent[0].body != `{"reviewers":["alice"],"team_reviewers":["core"]}` {
			t.Errorf("%s request: %v %v %v", p.id, got, err, sent)
		}
		*e.calls = nil
		got, err = e.invoke(t, reviewersRemove, "repo", `{"number":7,"reviewers":["alice"]}`)
		sent = changes(e)
		if err != nil || got["done"] != true || len(sent) != 1 || sent[0].method != http.MethodDelete ||
			sent[0].path != reviewersBase || sent[0].body != `{"reviewers":["alice"]}` {
			t.Errorf("%s remove: %v %v %v", p.id, got, err, sent)
		}
	}
}

func TestReviewerToolsRefuseTargetsBeforeTheToken(t *testing.T) {
	for _, p := range products {
		cases := map[*tool]string{reviewersList: `{}`, reviewersRequest: `{"number":7,"reviewers":["a"]}`,
			reviewersRemove: `{"number":7,"reviewers":["a"]}`}
		for tl, arguments := range cases {
			e := newEnvironment(t, p, reviewersServer(p, nil))
			arguments = `{"repository":"other/place",` + strings.TrimPrefix(arguments, "{")
			arguments = strings.Replace(arguments, ",}", "}", 1)
			_, err := e.invoke(t, tl, "repo", arguments)
			if !isInvalidRequest(err) || len(*e.calls) != 0 || *e.reads != 0 || strings.Contains(err.Error(), "other") {
				t.Errorf("%s %s: err %v, calls %d, reads %d", p.id, tl.action, err, len(*e.calls), *e.reads)
			}
		}
	}
}

func TestReviewerChangesOnAnIssueIndexAreRefused(t *testing.T) {
	for _, p := range products {
		for _, tl := range []*tool{reviewersRequest, reviewersRemove} {
			e := newEnvironment(t, p, reviewersServer(p, nil))
			_, err := e.invoke(t, tl, "repo", `{"number":8,"reviewers":["alice"]}`)
			if classOfError(err) != "not-found" || len(changes(e)) != 0 {
				t.Errorf("%s %s: %v %v", p.id, tl.action, err, methods(e))
			}
		}
	}
}

func TestReviewerNamesAndTeamsAreCheckedBeforeAnyRequest(t *testing.T) {
	cases := []struct{ connection, arguments string }{
		{"repo", `{"number":7,"team_reviewers":["core"]}`},
		{"mixed", `{"repository":"acme/widget","number":7,"team_reviewers":["core"]}`},
		{"org", `{"repository":"acme/widget","number":7}`},
		{"org", `{"repository":"acme/widget","number":7,"reviewers":["bad name"]}`},
		{"org", `{"repository":"acme/widget","number":7,"reviewers":["a","A"]}`},
		{"org", `{"repository":"acme/widget","number":7,"team_reviewers":["../x"]}`},
		{"org", `{"repository":"acme/widget","number":7,"reviewers":["` + strings.Repeat("a", 41) + `"]}`},
	}
	for _, p := range products {
		for _, tl := range []*tool{reviewersRequest, reviewersRemove} {
			for _, c := range cases {
				e := newEnvironment(t, p, reviewersServer(p, nil))
				_, err := e.invoke(t, tl, c.connection, c.arguments)
				if err == nil || len(*e.calls) != 0 || *e.reads != 0 {
					t.Errorf("%s %s %s: %v, calls %d", p.id, tl.action, c.arguments, err, len(*e.calls))
				}
			}
		}
	}
}

func TestReviewerTeamsAreAllowedWithoutTargets(t *testing.T) {
	e := newEnvironment(t, gitea, reviewersServer(gitea, answerWith(204, ``)))
	if _, err := e.invoke(t, reviewersRemove, "open", `{"repository":"acme/widget","number":7,"team_reviewers":["core"]}`); err != nil {
		t.Error(err)
	}
}

func TestReviewerChangesAreNotRepeatedAfterAnOpenOutcome(t *testing.T) {
	for _, p := range products {
		for _, tl := range []*tool{reviewersRequest, reviewersRemove} {
			e := newEnvironment(t, p, reviewersServer(p, answerWith(502, `{"message":"`+bodyCanary+`"}`)))
			_, err := e.invoke(t, tl, "repo", `{"number":7,"reviewers":["alice"]}`)
			if err == nil || !strings.Contains(err.Error(), "may have been applied") || len(changes(e)) != 1 {
				t.Errorf("%s %s: %v %v", p.id, tl.action, err, methods(e))
			}
		}
	}
}

func TestReviewerChangesNeedConfirmation(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, reviewersServer(p, nil))
		if _, err := e.invokeAs(t, reviewersRequest, "repo", `{"number":7,"reviewers":["alice"]}`, false); err == nil ||
			len(*e.calls) != 0 {
			t.Errorf("%s: %v", p.id, err)
		}
	}
}
