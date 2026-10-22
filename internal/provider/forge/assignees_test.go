package forge

import (
	"net/http"
	"strings"
	"testing"
)

const assigneeBase = "/api/v1/repos/acme/widget"

func assigneeServer(p *product) func(call) *http.Response {
	return issueServer(p, "open", func(c call) *http.Response {
		switch {
		case c.method == http.MethodGet && c.path == assigneeBase+"/assignees":
			return jsonResponse(200, `[{"id":4,"login":"bob","full_name":"Bob"},null,{"id":5,"login":"carol"}]`)
		case c.method == http.MethodGet && c.path == assigneeBase+"/assignees/bob",
			c.method == http.MethodGet && c.path == assigneeBase+"/issues/7/assignees/bob":
			return jsonResponse(204, ``)
		case c.method == http.MethodGet && strings.HasPrefix(c.path, assigneeBase+"/issues/7/assignees/"),
			c.method == http.MethodGet && strings.HasPrefix(c.path, assigneeBase+"/assignees/"):
			return notFoundResponse()
		case c.method == http.MethodPost && c.path == assigneeBase+"/issues/7/assignees":
			return jsonResponse(201, widgetIssue(p, 7, "open"))
		case c.method == http.MethodDelete && c.path == assigneeBase+"/issues/7/assignees":
			return jsonResponse(200, widgetIssue(p, 7, "open"))
		}
		return notFoundResponse()
	})
}

func TestAssigneesListReadsTheUsersOfOneRepository(t *testing.T) {
	for _, p := range products {
		e := newEnvironment(t, p, assigneeServer(p))
		result, err := e.invoke(t, assigneesList, "repo", `{}`)
		users, _ := result["assignees"].([]any)
		if err != nil || len(users) != 2 || users[0].(map[string]any)["login"] != "bob" {
			t.Fatalf("%s: %v, %v", p.id, result, err)
		}
		if got := e.paths(); len(got) != 1 || got[0] != assigneeBase+"/assignees" {
			t.Errorf("%s paths = %v", p.id, got)
		}
		before := len(*e.calls)
		if _, err := e.invoke(t, assigneesList, "repo", `{"repository":"secret-owner/hidden"}`); !isInvalidRequest(err) ||
			strings.Contains(err.Error(), "secret-owner") || len(*e.calls) != before {
			t.Errorf("%s: %v", p.id, err)
		}
	}
}

func TestAssigneeChecksReportABooleanOnGitea(t *testing.T) {
	e := newEnvironment(t, gitea, assigneeServer(gitea))
	for login, want := range map[string]bool{"bob": true, "mallory": false} {
		result, err := e.invoke(t, assigneesCheck, "repo", `{"assignee":"`+login+`"}`)
		if err != nil || result["assignable"] != want || result["assignee"] != login {
			t.Errorf("assignees.check %s: %v, %v", login, result, err)
		}
		result, err = e.invoke(t, issueAssigneesCheck, "repo", `{"number":7,"assignee":"`+login+`"}`)
		if err != nil || result["assigned"] != want || result["number"] != float64(7) {
			t.Errorf("issue-assignees.check %s: %v, %v", login, result, err)
		}
	}
	// A pull request index and an issue outside the targets are refused after the evidence read.
	for _, number := range []string{"8", "9"} {
		if _, err := e.invoke(t, issueAssigneesCheck, "repo", `{"number":`+number+`,"assignee":"bob"}`); !isInvalidRequest(err) {
			t.Errorf("number %s: %v", number, err)
		}
	}
	before := len(*e.calls)
	for _, bad := range []string{`{"assignee":"a/b"}`, `{"assignee":"..%2f"}`, `{"repository":"secret-owner/hidden","assignee":"bob"}`} {
		if _, err := e.invoke(t, assigneesCheck, "repo", bad); !isInvalidRequest(err) || len(*e.calls) != before {
			t.Errorf("%s: %v", bad, err)
		}
	}
}

func TestIssueAssigneesAddAndRemoveSendOneValidatedRequest(t *testing.T) {
	e := newEnvironment(t, gitea, assigneeServer(gitea))
	result, err := e.invoke(t, issueAssigneesAdd, "repo", `{"number":7,"assignees":["bob","carol"]}`)
	if err != nil || result["number"] != float64(7) {
		t.Fatalf("add: %v, %v", result, err)
	}
	if _, err := e.invoke(t, issueAssigneesRemove, "repo", `{"number":7,"assignees":["bob"]}`); err != nil {
		t.Fatalf("remove: %v", err)
	}
	calls := *e.calls
	if len(calls) != 4 || calls[1].method != http.MethodPost || calls[1].path != assigneeBase+"/issues/7/assignees" ||
		calls[1].body != `{"assignees":["bob","carol"]}` || calls[3].method != http.MethodDelete ||
		calls[3].body != `{"assignees":["bob"]}` || calls[1].contentType != "application/json" {
		t.Errorf("calls = %v %+v", methods(e), calls)
	}
	before := len(*e.calls)
	for _, bad := range []string{`{"number":7,"assignees":[]}`, `{"number":7,"assignees":["a/b"]}`,
		`{"number":7,"assignees":["bob","BOB"]}`, `{"number":7}`, `{"number":8,"assignees":["bob"]}`} {
		_, err := e.invoke(t, issueAssigneesAdd, "repo", bad)
		if !isInvalidRequest(err) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	// Only the pull request index reads the issue before it is refused.
	if len(*e.calls) != before+1 {
		t.Errorf("calls after refusals = %v", methods(e))
	}
	before = len(*e.calls)
	if _, err := e.invokeAs(t, issueAssigneesAdd, "repo", `{"number":7,"assignees":["bob"]}`, false); err == nil ||
		len(*e.calls) != before {
		t.Errorf("an unconfirmed change was sent: %v", err)
	}
}
