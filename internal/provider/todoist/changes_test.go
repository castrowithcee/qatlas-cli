package todoist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// changePermissions are the effects the writing test connections hold.
var changePermissions = []config.Permission{config.PermissionRead, config.PermissionCreate,
	config.PermissionUpdate, config.PermissionDelete}

// contentCanary stands for the text of a change; it must never reach an error.
const contentCanary = "change-content-canary-51f0"

// projectOfTask is where the fake Todoist keeps each task a change may name.
var projectOfTask = map[string]string{"taskDue": ownProject, "taskOpen": ownProject, "taskDone": ownProject,
	"taskOther": otherProject, "taskForeign": foreignID}

// serveChange answers the change routes of the fake Todoist and the reads only the changes use. It reports
// whether it answered.
func serveChange(w http.ResponseWriter, r *http.Request, record recorded) bool {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	var body map[string]any
	_ = json.Unmarshal([]byte(record.body), &body)
	segments := strings.Split(strings.Trim(path, "/"), "/")
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && (path == "/tasks/taskOther" || path == "/tasks/taskDone"):
		fmt.Fprint(w, taskJSON(segments[1], projectOfTask[segments[1]], "null"))
	case r.Method == http.MethodPost && path == "/tasks":
		project, _ := body["project_id"].(string)
		if project == "" {
			project = "projInbox"
		}
		fmt.Fprint(w, taskJSON("taskNew", project, "null"))
	case r.Method == http.MethodPost && len(segments) == 3 && segments[0] == "tasks" && segments[2] == "move":
		// A move to a project answers with the task; any other move answers without it.
		if project, ok := body["project_id"].(string); ok {
			fmt.Fprint(w, taskJSON(segments[1], project, "null"))
		}
	case r.Method == http.MethodPost && len(segments) == 3 && segments[0] == "tasks":
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && len(segments) == 2 && segments[0] == "tasks":
		fmt.Fprint(w, taskJSON(segments[1], projectOfTask[segments[1]], "null"))
	case r.Method == http.MethodDelete && len(segments) == 2:
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && path == "/comments":
		content, _ := json.Marshal(body["content"])
		fmt.Fprintf(w, `{"id":"noteNew","content":%s,"posted_at":"2026-09-23T10:00:00Z","is_deleted":false}`, content)
	case r.Method == http.MethodPost && len(segments) == 2 && segments[0] == "comments":
		content, _ := json.Marshal(body["content"])
		fmt.Fprintf(w, `{"id":"%s","content":%s,"is_deleted":false}`, segments[1], content)
	case r.Method == http.MethodGet && len(segments) == 2 && segments[0] == "comments":
		parents := map[string]string{
			"noteOne": `"item_id":"taskDue"`, "noteProject": `"project_id":"` + ownProject + `"`,
			"noteStray": `"item_id":"taskForeign"`, "noteStrayProject": `"project_id":"` + foreignID + `"`,
		}
		parent, ok := parents[segments[1]]
		if !ok {
			notFound(w)
			return true
		}
		fmt.Fprintf(w, `{"id":"%s",%s,"content":"%s","is_deleted":false}`, segments[1], parent, foreignCanary)
	default:
		return false
	}
	return true
}

// change runs one confirmed request.
func (e *environment) change(operation, connection, arguments string) (string, error) {
	response, err := e.core.Invoke(context.Background(), application.InvokeRequest{
		Operation: operation, Connection: connection, Arguments: json.RawMessage(arguments), Confirmed: true})
	return string(response.Result), err
}

// writes returns the requests since before that are no reads.
func writes(requests []recorded) []recorded {
	var changes []recorded
	for _, request := range requests {
		if request.method != http.MethodGet {
			changes = append(changes, request)
		}
	}
	return changes
}

func bodyOf(t *testing.T, request recorded) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(request.body), &body); err != nil {
		t.Fatalf("body %q of %s %s is no JSON object: %v", request.body, request.method, request.path, err)
	}
	return body
}

// A change is refused before a credential is resolved or Todoist is contacted when it lacks its
// confirmation, when the connection's permissions or tools list do not offer it, when it names a project
// outside the connection, and when its arguments are malformed.
func TestChangesAreRefusedBeforeAnyIO(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	for _, tt := range []struct {
		name, operation, connection, arguments string
		unconfirmed                            bool
		code                                   string
	}{
		{"an unconfirmed create", "todoist.tasks.create", "writer", `{"content":"x"}`, true, "confirmation"},
		{"an unconfirmed delete", "todoist.tasks.delete", "writer", `{"task_id":"taskDue"}`, true, "confirmation"},
		{"an unconfirmed close", "todoist.tasks.close", "writer", `{"task_id":"taskDue"}`, true, "confirmation"},
		{"a create on a read connection", "todoist.tasks.create", "one", `{"content":"x"}`, false, "unsupported"},
		{"a comment on a read connection", "todoist.comments.create", "account",
			`{"task_id":"taskDue","content":"x"}`, false, "unsupported"},
		{"a delete the tools list leaves out", "todoist.tasks.delete", "limited", `{"task_id":"taskDue"}`, false,
			"unsupported"},
		{"an update the tools list leaves out", "todoist.tasks.update", "limited",
			`{"task_id":"taskDue","priority":2}`, false, "unsupported"},
		{"a create in a foreign project", "todoist.tasks.create", "writer",
			`{"content":"x","project_id":"` + foreignID + `"}`, false, "invalid"},
		{"a move to a foreign project", "todoist.tasks.move", "multiwriter",
			`{"task_id":"taskDue","project_id":"` + foreignID + `"}`, false, "invalid"},
		{"a comment on a foreign project", "todoist.comments.create", "writer",
			`{"project_id":"` + foreignID + `","content":"x"}`, false, "invalid"},
		{"a create without a destination on several projects", "todoist.tasks.create", "multiwriter",
			`{"content":"x"}`, false, "invalid"},
		{"a create in a section below a parent", "todoist.tasks.create", "writer",
			`{"content":"x","section_id":"sectOwn","parent_id":"taskDue"}`, false, "invalid"},
		{"a content of two lines", "todoist.tasks.create", "writer", `{"content":"x\ny"}`, false, "invalid"},
		{"a padded content", "todoist.tasks.create", "writer", `{"content":" x"}`, false, "invalid"},
		{"two due dates", "todoist.tasks.create", "writer",
			`{"content":"x","due_string":"tomorrow","due_date":"2026-09-30"}`, false, "invalid"},
		{"a due language without text", "todoist.tasks.create", "writer",
			`{"content":"x","due_date":"2026-09-30","due_lang":"de"}`, false, "invalid"},
		{"a malformed due time", "todoist.tasks.create", "writer",
			`{"content":"x","due_datetime":"2026-09-30T25:00:00Z"}`, false, "invalid"},
		{"a duration without unit", "todoist.tasks.create", "writer", `{"content":"x","duration":30}`, false, "invalid"},
		{"a priority beyond the bound", "todoist.tasks.create", "writer", `{"content":"x","priority":5}`, false, "invalid"},
		{"a label twice", "todoist.tasks.create", "writer", `{"content":"x","labels":["a","a"]}`, false, "invalid"},
		{"an update without values", "todoist.tasks.update", "writer", `{"task_id":"taskDue"}`, false, "invalid"},
		{"a cleared and a new due date", "todoist.tasks.update", "writer",
			`{"task_id":"taskDue","clear_due":true,"due_date":"2026-09-30"}`, false, "invalid"},
		{"a project in an update", "todoist.tasks.update", "writer",
			`{"task_id":"taskDue","project_id":"` + ownProject + `"}`, false, "invalid"},
		{"a move to two destinations", "todoist.tasks.move", "writer",
			`{"task_id":"taskDue","project_id":"` + ownProject + `","section_id":"sectOwn"}`, false, "invalid"},
		{"a move to nowhere", "todoist.tasks.move", "writer", `{"task_id":"taskDue"}`, false, "invalid"},
		{"a move below itself", "todoist.tasks.move", "writer", `{"task_id":"taskDue","parent_id":"taskDue"}`,
			false, "invalid"},
		{"a comment on task and project", "todoist.comments.create", "accountwriter",
			`{"task_id":"taskDue","project_id":"` + ownProject + `","content":"x"}`, false, "invalid"},
		{"a comment on nothing", "todoist.comments.create", "accountwriter", `{"content":"x"}`, false, "invalid"},
		{"a blank comment", "todoist.comments.create", "accountwriter", `{"task_id":"taskDue","content":" \n"}`,
			false, "invalid"},
		{"a comment attachment", "todoist.comments.create", "accountwriter",
			`{"task_id":"taskDue","content":"x","attachment":{"file_url":"https://files.example.invalid/x"}}`,
			false, "invalid"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			*env.reads = 0
			before := len(env.fake.recorded())
			response, err := env.core.Invoke(context.Background(), application.InvokeRequest{
				Operation: tt.operation, Connection: tt.connection, Arguments: json.RawMessage(tt.arguments),
				Confirmed: !tt.unconfirmed})
			var (
				confirmation *application.ConfirmationRequiredError
				unsupported  *capability.UnsupportedError
			)
			switch tt.code {
			case "confirmation":
				if !errors.As(err, &confirmation) {
					t.Errorf("err = %v, want confirmation to be required", err)
				}
			case "unsupported":
				if !errors.As(err, &unsupported) {
					t.Errorf("err = %v, want an unsupported capability", err)
				}
			case "invalid":
				if !isInvalidRequest(err) {
					t.Errorf("err = %v (%s), want an invalid request", err, response.Result)
				}
			}
			if *env.reads != 0 || len(env.fake.recorded()) != before {
				t.Errorf("secret reads = %d, requests = %d; want none", *env.reads, len(env.fake.recorded())-before)
			}
		})
	}
}

// A project connection changes nothing outside its projects: the task or comment a change concerns and
// every section, parent, or task it names are read first, and a foreign one ends the request before
// anything is sent. No refusal carries foreign content.
func TestChangesStayInsideTheConnection(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	for _, tt := range []struct{ name, operation, connection, arguments string }{
		{"a move below a foreign parent", "todoist.tasks.move", "writer", `{"task_id":"taskDue","parent_id":"taskForeign"}`},
		{"a move into a foreign section", "todoist.tasks.move", "writer", `{"task_id":"taskDue","section_id":"sectForeign"}`},
		{"a move of a foreign task", "todoist.tasks.move", "multiwriter",
			`{"task_id":"taskForeign","project_id":"` + ownProject + `"}`},
		{"a subtask of a foreign parent", "todoist.tasks.create", "writer", `{"content":"x","parent_id":"taskForeign"}`},
		{"a task in a foreign section", "todoist.tasks.create", "writer", `{"content":"x","section_id":"sectForeign"}`},
		{"a parent of another project", "todoist.tasks.create", "multiwriter",
			`{"content":"x","project_id":"` + ownProject + `","parent_id":"taskOther"}`},
		{"a section of another project", "todoist.tasks.create", "multiwriter",
			`{"content":"x","project_id":"` + otherProject + `","section_id":"sectOwn"}`},
		{"an update of a foreign task", "todoist.tasks.update", "writer", `{"task_id":"taskForeign","priority":1}`},
		{"a close of a foreign task", "todoist.tasks.close", "writer", `{"task_id":"taskForeign"}`},
		{"a reopen of a foreign task", "todoist.tasks.reopen", "writer", `{"task_id":"taskForeign"}`},
		{"a delete of a foreign task", "todoist.tasks.delete", "writer", `{"task_id":"taskForeign"}`},
		{"a comment on a foreign task", "todoist.comments.create", "writer", `{"task_id":"taskForeign","content":"x"}`},
		{"an update of a foreign task comment", "todoist.comments.update", "writer",
			`{"comment_id":"noteStray","content":"x"}`},
		{"a delete of a foreign project comment", "todoist.comments.delete", "writer", `{"comment_id":"noteStrayProject"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := len(env.fake.recorded())
			_, err := env.change(tt.operation, tt.connection, tt.arguments)
			if !isInvalidRequest(err) || strings.Contains(err.Error(), foreignCanary) {
				t.Errorf("err = %v, want a scope refusal without foreign content", err)
			}
			if sent := writes(env.fake.recorded()[before:]); len(sent) != 0 {
				t.Errorf("a refused change reached Todoist: %+v", sent)
			}
		})
	}
}

// Each change is exactly one request of its own with a fresh request ID and the values it names, after
// the reads its scope needs. Close and reopen are separate requests, and no task change touches comments.
func TestTaskChangesSendOneRequestEach(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	type step struct {
		name, operation, connection, arguments string
		reads                                  []string
		method, path                           string
		body                                   map[string]any
		result                                 string
	}
	steps := []step{
		{"a create in the only project", "todoist.tasks.create", "writer",
			`{"content":"Send the invoice","description":"line one\nline two","labels":["waiting","money"],` +
				`"priority":4,"due_string":"morgen 9 Uhr","due_lang":"de","duration":30,"duration_unit":"minute",` +
				`"assignee_id":"1234567"}`,
			nil, http.MethodPost, "/api/v1/tasks",
			map[string]any{"content": "Send the invoice", "description": "line one\nline two",
				"labels": []any{"waiting", "money"}, "priority": float64(4), "due_string": "morgen 9 Uhr",
				"due_lang": "de", "duration": float64(30), "duration_unit": "minute", "assignee_id": "1234567",
				"project_id": ownProject},
			`"id":"taskNew"`},
		{"a create in a section", "todoist.tasks.create", "multiwriter",
			`{"content":"x","section_id":"sectOwn","due_datetime":"2026-09-30T11:00:00+02:00"}`,
			[]string{"/api/v1/sections/sectOwn"}, http.MethodPost, "/api/v1/tasks",
			map[string]any{"content": "x", "section_id": "sectOwn", "project_id": ownProject,
				"due_datetime": "2026-09-30T09:00:00Z"},
			`"project_id":"` + ownProject + `"`},
		{"a subtask", "todoist.tasks.create", "multiwriter", `{"content":"x","parent_id":"taskOther"}`,
			[]string{"/api/v1/tasks/taskOther"}, http.MethodPost, "/api/v1/tasks",
			map[string]any{"content": "x", "parent_id": "taskOther", "project_id": otherProject},
			`"project_id":"` + otherProject + `"`},
		{"a create in the Inbox", "todoist.tasks.create", "accountwriter", `{"content":"x","due_date":"2026-09-30"}`,
			nil, http.MethodPost, "/api/v1/tasks", map[string]any{"content": "x", "due_date": "2026-09-30"},
			`"project_id":"projInbox"`},
		{"an update", "todoist.tasks.update", "writer",
			`{"task_id":"taskDue","content":"Renamed","labels":[],"clear_due":true,"clear_duration":true,"unassign":true}`,
			[]string{"/api/v1/tasks/taskDue"}, http.MethodPost, "/api/v1/tasks/taskDue",
			map[string]any{"content": "Renamed", "labels": []any{}, "due_string": "no date", "duration": nil,
				"duration_unit": nil, "assignee_id": nil},
			`"id":"taskDue"`},
		{"a move to another project", "todoist.tasks.move", "multiwriter",
			`{"task_id":"taskDue","project_id":"` + otherProject + `"}`,
			[]string{"/api/v1/tasks/taskDue"}, http.MethodPost, "/api/v1/tasks/taskDue/move",
			map[string]any{"project_id": otherProject}, `"project_id":"` + otherProject + `"`},
		{"a move below a parent, read back", "todoist.tasks.move", "multiwriter",
			`{"task_id":"taskDue","parent_id":"taskOther"}`,
			[]string{"/api/v1/tasks/taskDue", "/api/v1/tasks/taskOther", "/api/v1/tasks/taskDue"},
			http.MethodPost, "/api/v1/tasks/taskDue/move", map[string]any{"parent_id": "taskOther"}, `"id":"taskDue"`},
		{"a close", "todoist.tasks.close", "writer", `{"task_id":"taskDue"}`, []string{"/api/v1/tasks/taskDue"},
			http.MethodPost, "/api/v1/tasks/taskDue/close", nil, `{"id":"taskDue","result":"closed"}`},
		{"a reopen", "todoist.tasks.reopen", "writer", `{"task_id":"taskDone"}`, []string{"/api/v1/tasks/taskDone"},
			http.MethodPost, "/api/v1/tasks/taskDone/reopen", nil, `{"id":"taskDone","result":"reopened"}`},
		{"a delete", "todoist.tasks.delete", "writer", `{"task_id":"taskDue"}`, []string{"/api/v1/tasks/taskDue"},
			http.MethodDelete, "/api/v1/tasks/taskDue", nil, `{"id":"taskDue","result":"deleted"}`},
		{"a close on the account", "todoist.tasks.close", "accountwriter", `{"task_id":"taskDue"}`, nil,
			http.MethodPost, "/api/v1/tasks/taskDue/close", nil, `{"id":"taskDue","result":"closed"}`},
	}
	requestIDs := map[string]bool{}
	for _, tt := range steps {
		t.Run(tt.name, func(t *testing.T) {
			before := len(env.fake.recorded())
			result, err := env.change(tt.operation, tt.connection, tt.arguments)
			if err != nil || !strings.Contains(result, tt.result) {
				t.Fatalf("result = %s, %v; want %s", result, err, tt.result)
			}
			requests := env.fake.recorded()[before:]
			var reads []string
			for _, request := range requests {
				if request.method == http.MethodGet {
					reads = append(reads, request.path)
				}
				if strings.Contains(request.path, "/comments") {
					t.Errorf("a task change touched comments: %+v", request)
				}
			}
			if strings.Join(reads, " ") != strings.Join(tt.reads, " ") {
				t.Errorf("reads = %v, want %v", reads, tt.reads)
			}
			sent := writes(requests)
			if len(sent) != 1 || sent[0].method != tt.method || sent[0].path != tt.path {
				t.Fatalf("changes = %+v, want exactly one %s %s", sent, tt.method, tt.path)
			}
			if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(sent[0].requestID) || requestIDs[sent[0].requestID] {
				t.Errorf("request ID = %q, want a fresh one", sent[0].requestID)
			}
			requestIDs[sent[0].requestID] = true
			if sent[0].auth != "Bearer "+tokenValue {
				t.Errorf("authorization = %q", sent[0].auth)
			}
			if tt.body == nil {
				if sent[0].body != "" {
					t.Errorf("body = %q, want none", sent[0].body)
				}
				return
			}
			if sent[0].contentType != "application/json" {
				t.Errorf("content type = %q", sent[0].contentType)
			}
			if body := bodyOf(t, sent[0]); fmt.Sprint(body) != fmt.Sprint(tt.body) {
				t.Errorf("body = %v, want %v", body, tt.body)
			}
		})
	}
}

// Comments change only through the comment tools, for one named task, project, or comment of the
// connection.
func TestCommentChanges(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	for _, tt := range []struct {
		name, operation, connection, arguments string
		reads                                  []string
		method, path                           string
		body                                   map[string]any
		result                                 string
	}{
		{"a task comment", "todoist.comments.create", "writer", `{"task_id":"taskDue","content":"Done?\nPlease check"}`,
			[]string{"/api/v1/tasks/taskDue"}, http.MethodPost, "/api/v1/comments",
			map[string]any{"task_id": "taskDue", "content": "Done?\nPlease check"},
			`"content":"Done?\nPlease check"`},
		{"a project comment", "todoist.comments.create", "writer", `{"project_id":"` + ownProject + `","content":"Kick-off"}`,
			nil, http.MethodPost, "/api/v1/comments", map[string]any{"project_id": ownProject, "content": "Kick-off"},
			`"id":"noteNew"`},
		{"an update of a task comment", "todoist.comments.update", "writer", `{"comment_id":"noteOne","content":"Fixed"}`,
			[]string{"/api/v1/comments/noteOne", "/api/v1/tasks/taskDue"}, http.MethodPost, "/api/v1/comments/noteOne",
			map[string]any{"content": "Fixed"}, `"content":"Fixed"`},
		{"a delete of a project comment", "todoist.comments.delete", "writer", `{"comment_id":"noteProject"}`,
			[]string{"/api/v1/comments/noteProject"}, http.MethodDelete, "/api/v1/comments/noteProject", nil,
			`{"id":"noteProject","result":"deleted"}`},
		{"a delete on the account", "todoist.comments.delete", "accountwriter", `{"comment_id":"noteStray"}`,
			nil, http.MethodDelete, "/api/v1/comments/noteStray", nil, `{"id":"noteStray","result":"deleted"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := len(env.fake.recorded())
			result, err := env.change(tt.operation, tt.connection, tt.arguments)
			if err != nil || !strings.Contains(result, tt.result) {
				t.Fatalf("result = %s, %v; want %s", result, err, tt.result)
			}
			requests := env.fake.recorded()[before:]
			var reads []string
			for _, request := range requests {
				if request.method == http.MethodGet {
					reads = append(reads, request.path)
				}
			}
			if strings.Join(reads, " ") != strings.Join(tt.reads, " ") {
				t.Errorf("reads = %v, want %v", reads, tt.reads)
			}
			sent := writes(requests)
			if len(sent) != 1 || sent[0].method != tt.method || sent[0].path != tt.path || sent[0].requestID == "" {
				t.Fatalf("changes = %+v, want exactly one %s %s", sent, tt.method, tt.path)
			}
			if tt.body != nil && fmt.Sprint(bodyOf(t, sent[0])) != fmt.Sprint(tt.body) {
				t.Errorf("body = %s, want %v", sent[0].body, tt.body)
			}
		})
	}
}

// A change whose outcome is unclear is reported as possibly applied and is never sent again. A change
// Todoist refused is reported as refused. No error carries the change's content or the token.
func TestUnclearChangesAreNeverRepeated(t *testing.T) {
	answers := []struct {
		name      string
		answer    func(http.ResponseWriter)
		class     provider.Class
		uncertain bool
	}{
		{"a dropped connection", func(w http.ResponseWriter) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
		}, provider.ClassUnreachable, true},
		{"a server error", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"error":"`+contentCanary+`"}`)
		}, provider.ClassProviderError, true},
		{"a gateway timeout", func(w http.ResponseWriter) { w.WriteHeader(http.StatusGatewayTimeout) },
			provider.ClassTimeout, true},
		// A close or a delete reads no answer, so only the changes that answer with a resource meet this one.
		{"an unreadable answer", func(w http.ResponseWriter) { fmt.Fprint(w, `{"id":`) },
			provider.ClassInvalidResponse, true},
		{"a refusal", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"Invalid argument `+contentCanary+`"}`)
		}, provider.ClassProviderError, false},
		{"a forbidden change", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error_tag":"FORBIDDEN"}`)
		}, provider.ClassPermission, false},
		{"a rate limit", func(w http.ResponseWriter) {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusTooManyRequests)
		}, provider.ClassRateLimited, false},
	}
	changes := []struct {
		operation, arguments string
		answered             bool
	}{
		{"todoist.tasks.create", `{"content":"` + contentCanary + `"}`, true},
		{"todoist.comments.create", `{"task_id":"taskDue","content":"` + contentCanary + `"}`, true},
		{"todoist.tasks.close", `{"task_id":"taskDue"}`, false},
		{"todoist.tasks.delete", `{"task_id":"taskDue"}`, false},
		{"todoist.comments.update", `{"comment_id":"noteOne","content":"` + contentCanary + `"}`, true},
	}
	for _, answer := range answers {
		for _, change := range changes {
			if answer.class == provider.ClassInvalidResponse && !change.answered {
				continue
			}
			t.Run(answer.name+" of "+change.operation, func(t *testing.T) {
				f := &fakeTodoist{failure: func(w http.ResponseWriter, r *http.Request) bool {
					if r.Method == http.MethodGet {
						return false
					}
					answer.answer(w)
					return true
				}}
				env := newEnvironment(t, f)
				_, err := env.change(change.operation, "accountwriter", change.arguments)
				if classOf(err) != answer.class {
					t.Fatalf("err = %v (class %q), want class %q", err, classOf(err), answer.class)
				}
				if got := strings.Contains(err.Error(), "may have been applied"); got != answer.uncertain {
					t.Errorf("err = %v, want possibly applied = %t", err, answer.uncertain)
				}
				message := env.red.Error(err) + err.Error()
				for _, leak := range []string{contentCanary, tokenValue, "api.todoist.com"} {
					if strings.Contains(message, leak) {
						t.Errorf("the error carries %q: %v", leak, err)
					}
				}
				if sent := writes(f.recorded()); len(sent) != 1 {
					t.Errorf("changes sent = %d, want exactly one: %+v", len(sent), sent)
				}
			})
		}
	}

	// A created task or comment without an identifier may still exist.
	for operation, arguments := range map[string]string{"todoist.tasks.create": `{"content":"x"}`,
		"todoist.comments.create": `{"task_id":"taskDue","content":"x"}`} {
		f := &fakeTodoist{failure: func(w http.ResponseWriter, r *http.Request) bool {
			if r.Method == http.MethodGet {
				return false
			}
			fmt.Fprint(w, `{"content":"x"}`)
			return true
		}}
		env := newEnvironment(t, f)
		_, err := env.change(operation, "accountwriter", arguments)
		if classOf(err) != provider.ClassInvalidResponse || !strings.Contains(err.Error(), "may have been applied") ||
			len(writes(f.recorded())) != 1 {
			t.Errorf("%s without an ID = %v after %d changes, want one possibly applied change", operation, err,
				len(writes(f.recorded())))
		}
	}
}
