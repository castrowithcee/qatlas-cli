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
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// teamProject is a workspace project; no structure change may touch it.
const teamProject = "projTeam"

// reminderTasks is the task each reminder of the fake Todoist belongs to.
var reminderTasks = map[string]string{"remOne": "taskDue", "remForeign": "taskForeign"}

// allTools lists every registered Todoist tool, for the connections whose tools list names them all.
func allTools() []string {
	reg := capability.NewRegistry()
	_ = Register(reg)
	var ids []string
	for _, descriptor := range reg.Provider(Provider) {
		ids = append(ids, descriptor.ID)
	}
	return ids
}

func labelJSON(id, name string) string {
	return `{"id":"` + id + `","name":"` + name + `","color":"grey","order":1,"is_favorite":false}`
}

func reminderJSON(id, task string) string {
	if task == "taskForeign" {
		return `{"id":"` + id + `","item_id":"` + task + `","notify_uid":"1","type":"absolute","minute_offset":null,` +
			`"is_urgent":false,"is_deleted":false,"due":{"date":"2026-09-22T08:00:00","string":"` + foreignCanary + `"}}`
	}
	return `{"id":"` + id + `","item_id":"` + task + `","notify_uid":"1","type":"relative","minute_offset":30,` +
		`"is_urgent":false,"is_deleted":false,"due":null}`
}

// serveStructure answers the structure and reminder routes of the fake Todoist. It reports whether it
// answered.
func serveStructure(w http.ResponseWriter, r *http.Request, record recorded) bool {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	segments := strings.Split(strings.Trim(path, "/"), "/")
	var body map[string]any
	_ = json.Unmarshal([]byte(record.body), &body)
	value := func(key string) string {
		text, _ := body[key].(string)
		return text
	}
	get, post := r.Method == http.MethodGet, r.Method == http.MethodPost
	w.Header().Set("Content-Type", "application/json")
	switch {
	case get && path == "/projects/archived":
		fmt.Fprint(w, `{"results":[],"next_cursor":null}`)
	case get && path == "/projects/"+teamProject:
		fmt.Fprint(w, `{"id":"`+teamProject+`","name":"Team","color":"blue","parent_id":null,"is_favorite":false,`+
			`"is_shared":true,"is_archived":false,"view_style":"list","workspace_id":"4567"}`)
	case post && path == "/projects":
		fmt.Fprint(w, projectJSON("projNew", value("name")))
	case post && len(segments) == 2 && segments[0] == "projects":
		fmt.Fprint(w, projectJSON(segments[1], "Renamed"))
	case post && len(segments) == 3 && segments[0] == "projects":
		fmt.Fprint(w, projectJSON(segments[1], "Project "+segments[1]))
	case post && path == "/sections":
		fmt.Fprint(w, sectionJSON("sectNew", value("project_id")))
	case post && len(segments) == 2 && segments[0] == "sections":
		project := ownProject
		if segments[1] == "sectForeign" {
			project = foreignID
		}
		fmt.Fprint(w, sectionJSON(segments[1], project))
	case post && path == "/sync" && record.form.Get("commands") != "":
		var commands []struct {
			UUID string `json:"uuid"`
		}
		_ = json.Unmarshal([]byte(record.form.Get("commands")), &commands)
		status := map[string]string{}
		for _, command := range commands {
			status[command.UUID] = "ok"
		}
		encoded, _ := json.Marshal(map[string]any{"sync_token": "abc", "sync_status": status})
		_, _ = w.Write(encoded)
	case post && path == "/labels":
		fmt.Fprint(w, labelJSON("labelNew", value("name")))
	case post && len(segments) == 2 && segments[0] == "labels":
		fmt.Fprint(w, labelJSON(segments[1], "renamed"))
	case get && len(segments) == 2 && segments[0] == "reminders":
		task, ok := reminderTasks[segments[1]]
		if !ok {
			notFound(w)
			return true
		}
		fmt.Fprint(w, reminderJSON(segments[1], task))
	case post && path == "/reminders":
		fmt.Fprint(w, reminderJSON("remNew", value("task_id")))
	case post && len(segments) == 2 && segments[0] == "reminders":
		fmt.Fprint(w, reminderJSON(segments[1], reminderTasks[segments[1]]))
	default:
		return false
	}
	return true
}

// A structure or reminder change is refused before a credential is resolved or Todoist is contacted when
// it lacks its confirmation, when the connection does not offer it, when it names a project outside the
// connection, and when its arguments are malformed. An account-wide change is not offered by a project
// connection, and a delete that takes more than itself only by a connection whose tools list names it.
func TestStructureChangesAreRefusedBeforeAnyIO(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	for _, tt := range []struct {
		name, operation, connection, arguments string
		unconfirmed                            bool
		code                                   string
	}{
		{"an unconfirmed archive", "todoist.projects.archive", "organizer", `{"project_id":"` + ownProject + `"}`,
			true, "confirmation"},
		{"an unconfirmed project delete", "todoist.projects.delete", "organizer",
			`{"project_id":"` + ownProject + `"}`, true, "confirmation"},
		{"an unconfirmed section move", "todoist.sections.move", "multiorganizer",
			`{"section_id":"sectOwn","project_id":"` + otherProject + `"}`, true, "confirmation"},
		{"an unconfirmed label delete", "todoist.labels.delete", "organizer", `{"label_id":"2156154810"}`, true,
			"confirmation"},
		{"an unconfirmed reminder", "todoist.reminders.create", "writer", `{"task_id":"taskDue","minute_offset":5}`,
			true, "confirmation"},
		{"a project delete without a tools list", "todoist.projects.delete", "accountwriter",
			`{"project_id":"` + ownProject + `"}`, false, "unsupported"},
		{"a section delete without a tools list", "todoist.sections.delete", "multiwriter",
			`{"section_id":"sectOwn"}`, false, "unsupported"},
		{"a label delete without a tools list", "todoist.labels.delete", "accountwriter",
			`{"label_id":"2156154810"}`, false, "unsupported"},
		{"a project create on a project connection", "todoist.projects.create", "multiorganizer", `{"name":"x"}`,
			false, "unsupported"},
		{"a label create on a project connection", "todoist.labels.create", "multiorganizer", `{"name":"x"}`,
			false, "unsupported"},
		{"a label update on a project connection", "todoist.labels.update", "writer",
			`{"label_id":"2156154810","name":"x"}`, false, "unsupported"},
		{"a label reorder on a project connection", "todoist.labels.reorder", "multiorganizer",
			`{"label_id":"2156154810","order":1}`, false, "unsupported"},
		{"a reminder on a read connection", "todoist.reminders.create", "one",
			`{"task_id":"taskDue","minute_offset":5}`, false, "unsupported"},
		{"an update of a foreign project", "todoist.projects.update", "multiorganizer",
			`{"project_id":"` + foreignID + `","name":"x"}`, false, "invalid"},
		{"an archive of a foreign project", "todoist.projects.archive", "multiorganizer",
			`{"project_id":"` + foreignID + `"}`, false, "invalid"},
		{"an unarchive of a foreign project", "todoist.projects.unarchive", "multiorganizer",
			`{"project_id":"` + foreignID + `"}`, false, "invalid"},
		{"a delete of a foreign project", "todoist.projects.delete", "multiorganizer",
			`{"project_id":"` + foreignID + `"}`, false, "invalid"},
		{"a section in a foreign project", "todoist.sections.create", "multiorganizer",
			`{"name":"x","project_id":"` + foreignID + `"}`, false, "invalid"},
		{"a section move to a foreign project", "todoist.sections.move", "multiorganizer",
			`{"section_id":"sectOwn","project_id":"` + foreignID + `"}`, false, "invalid"},
		{"a section without a project on several", "todoist.sections.create", "multiorganizer", `{"name":"x"}`,
			false, "invalid"},
		{"a section without a project on the account", "todoist.sections.create", "organizer", `{"name":"x"}`,
			false, "invalid"},
		{"a workspace project create", "todoist.projects.create", "organizer", `{"name":"x","workspace_id":"4567"}`,
			false, "invalid"},
		{"a project update without values", "todoist.projects.update", "organizer",
			`{"project_id":"` + ownProject + `"}`, false, "invalid"},
		{"an unknown color", "todoist.projects.update", "organizer",
			`{"project_id":"` + ownProject + `","color":"#ff0000"}`, false, "invalid"},
		{"a project name of two lines", "todoist.projects.create", "organizer", `{"name":"a\nb"}`, false, "invalid"},
		{"a padded label name", "todoist.labels.create", "organizer", `{"name":" x"}`, false, "invalid"},
		{"a section update without values", "todoist.sections.update", "organizer", `{"section_id":"sectOwn"}`,
			false, "invalid"},
		{"a negative section order", "todoist.sections.reorder", "organizer", `{"section_id":"sectOwn","order":-1}`,
			false, "invalid"},
		{"a label order beyond the bound", "todoist.labels.reorder", "organizer",
			`{"label_id":"2156154810","order":40000}`, false, "invalid"},
		{"a reminder without a time", "todoist.reminders.create", "writer", `{"task_id":"taskDue"}`, false, "invalid"},
		{"a reminder with two times", "todoist.reminders.create", "writer",
			`{"task_id":"taskDue","minute_offset":5,"due_string":"tomorrow"}`, false, "invalid"},
		{"a reminder language without text", "todoist.reminders.create", "writer",
			`{"task_id":"taskDue","due_datetime":"2026-09-30T09:00:00Z","due_lang":"de"}`, false, "invalid"},
		{"a location reminder", "todoist.reminders.create", "writer",
			`{"task_id":"taskDue","type":"location","loc_lat":"41.1","loc_long":"-8.6"}`, false, "invalid"},
		{"a reminder update without values", "todoist.reminders.update", "writer", `{"reminder_id":"remOne"}`,
			false, "invalid"},
		{"a reminder offset beyond the bound", "todoist.reminders.update", "writer",
			`{"reminder_id":"remOne","minute_offset":50000}`, false, "invalid"},
		{"a completed-task query with a line break", "todoist.completedtasks.list", "one",
			`{"since":"2026-09-01T00:00:00Z","until":"2026-09-02T00:00:00Z","query":"today\n"}`, false, "invalid"},
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

// A structure or reminder change never leaves the connection: the section, reminder, task, or project it
// concerns is read first, and one outside the connection or of a workspace ends the request before anything
// is sent. No refusal carries foreign content.
func TestStructureChangesStayInsideTheConnection(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	for _, tt := range []struct{ name, operation, connection, arguments string }{
		{"an update of a foreign section", "todoist.sections.update", "multiorganizer",
			`{"section_id":"sectForeign","name":"x"}`},
		{"a reorder of a foreign section", "todoist.sections.reorder", "multiorganizer",
			`{"section_id":"sectForeign","order":1}`},
		{"a move of a foreign section", "todoist.sections.move", "multiorganizer",
			`{"section_id":"sectForeign","project_id":"` + ownProject + `"}`},
		{"a delete of a foreign section", "todoist.sections.delete", "multiorganizer", `{"section_id":"sectForeign"}`},
		{"a reminder on a foreign task", "todoist.reminders.create", "writer",
			`{"task_id":"taskForeign","minute_offset":5}`},
		{"an update of a foreign reminder", "todoist.reminders.update", "writer",
			`{"reminder_id":"remForeign","minute_offset":5}`},
		{"a delete of a foreign reminder", "todoist.reminders.delete", "writer", `{"reminder_id":"remForeign"}`},
		{"a read of a foreign reminder", "todoist.reminders.get", "one", `{"reminder_id":"remForeign"}`},
		{"an update of a workspace project", "todoist.projects.update", "organizer",
			`{"project_id":"` + teamProject + `","name":"x"}`},
		{"an archive of a workspace project", "todoist.projects.archive", "organizer",
			`{"project_id":"` + teamProject + `"}`},
		{"a delete of a workspace project", "todoist.projects.delete", "organizer",
			`{"project_id":"` + teamProject + `"}`},
		{"a subproject of a workspace project", "todoist.projects.create", "organizer",
			`{"name":"x","parent_id":"` + teamProject + `"}`},
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

// allowedRoute is every route a Todoist tool may reach. Workspaces, users, settings, notifications,
// folders, shared labels, collaborators, backups, and uploads are none of them.
var allowedRoute = regexp.MustCompile(`^/api/v1/(projects(/archived|/search|/[A-Za-z0-9]+(/archive|/unarchive)?)?|` +
	`sections(/search|/[A-Za-z0-9]+)?|labels(/search|/[A-Za-z0-9]+)?|reminders(/[A-Za-z0-9]+)?|` +
	`tasks(/filter|/completed/by_completion_date|/[A-Za-z0-9]+(/move|/close|/reopen)?)?|comments(/[A-Za-z0-9]+)?|sync)$`)

// Each structure and reminder change is exactly one request of its own with a fresh request ID and the
// values it names, after the reads its scope needs. Every request stays on the personal routes.
func TestStructureChangesSendOneRequestEach(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	type step struct {
		name, operation, connection, arguments string
		reads                                  []string
		method, path                           string
		body                                   map[string]any
		result                                 string
	}
	project := func(id string) string { return "/api/v1/projects/" + id }
	steps := []step{
		{"a project", "todoist.projects.create", "organizer",
			`{"name":"Garden","description":"beds\nand trees","color":"green","is_favorite":true,"view_style":"board"}`,
			nil, http.MethodPost, "/api/v1/projects",
			map[string]any{"name": "Garden", "description": "beds\nand trees", "color": "green", "is_favorite": true,
				"view_style": "board"}, `"id":"projNew"`},
		{"a subproject", "todoist.projects.create", "organizer", `{"name":"Beds","parent_id":"` + ownProject + `"}`,
			[]string{project(ownProject)}, http.MethodPost, "/api/v1/projects",
			map[string]any{"name": "Beds", "parent_id": ownProject}, `"id":"projNew"`},
		{"a project update", "todoist.projects.update", "multiorganizer",
			`{"project_id":"` + otherProject + `","name":"Renamed","is_favorite":false}`,
			[]string{project(otherProject)}, http.MethodPost, project(otherProject),
			map[string]any{"name": "Renamed", "is_favorite": false}, `"name":"Renamed"`},
		{"a project archive", "todoist.projects.archive", "multiorganizer", `{"project_id":"` + ownProject + `"}`,
			[]string{project(ownProject), "/api/v1/projects"}, http.MethodPost, project(ownProject) + "/archive", nil,
			`{"id":"` + ownProject + `","result":"archived"}`},
		{"a project unarchive", "todoist.projects.unarchive", "multiorganizer", `{"project_id":"` + ownProject + `"}`,
			[]string{project(ownProject)}, http.MethodPost, project(ownProject) + "/unarchive", nil,
			`{"id":"` + ownProject + `","result":"unarchived"}`},
		{"a project delete", "todoist.projects.delete", "multiorganizer", `{"project_id":"` + ownProject + `"}`,
			[]string{project(ownProject), "/api/v1/projects", "/api/v1/projects/archived"}, http.MethodDelete,
			project(ownProject), nil, `{"id":"` + ownProject + `","result":"deleted"}`},
		{"a project archive on the account", "todoist.projects.archive", "organizer",
			`{"project_id":"` + ownProject + `"}`, []string{project(ownProject)}, http.MethodPost,
			project(ownProject) + "/archive", nil, `"result":"archived"`},
		{"a section in the only project", "todoist.sections.create", "writer",
			`{"name":"Waiting","description":"on hold"}`, nil, http.MethodPost, "/api/v1/sections",
			map[string]any{"name": "Waiting", "project_id": ownProject, "description": "on hold"}, `"id":"sectNew"`},
		{"a section update", "todoist.sections.update", "writer", `{"section_id":"sectOwn","name":"Blocked"}`,
			[]string{"/api/v1/sections/sectOwn"}, http.MethodPost, "/api/v1/sections/sectOwn",
			map[string]any{"name": "Blocked"}, `"id":"sectOwn"`},
		{"a section reorder", "todoist.sections.reorder", "writer", `{"section_id":"sectOwn","order":0}`,
			[]string{"/api/v1/sections/sectOwn"}, http.MethodPost, "/api/v1/sections/sectOwn",
			map[string]any{"section_order": float64(0)}, `"id":"sectOwn"`},
		{"a section delete", "todoist.sections.delete", "multiorganizer", `{"section_id":"sectOwn"}`,
			[]string{"/api/v1/sections/sectOwn"}, http.MethodDelete, "/api/v1/sections/sectOwn", nil,
			`{"id":"sectOwn","result":"deleted"}`},
		{"a label", "todoist.labels.create", "organizer", `{"name":"waiting","color":"grey"}`, nil,
			http.MethodPost, "/api/v1/labels", map[string]any{"name": "waiting", "color": "grey"}, `"id":"labelNew"`},
		{"a label update", "todoist.labels.update", "organizer",
			`{"label_id":"2156154810","name":"blocked","is_favorite":true}`, nil, http.MethodPost,
			"/api/v1/labels/2156154810", map[string]any{"name": "blocked", "is_favorite": true}, `"id":"2156154810"`},
		{"a label reorder", "todoist.labels.reorder", "organizer", `{"label_id":"2156154810","order":3}`, nil,
			http.MethodPost, "/api/v1/labels/2156154810", map[string]any{"order": float64(3)}, `"id":"2156154810"`},
		{"a label delete", "todoist.labels.delete", "organizer", `{"label_id":"2156154810"}`, nil,
			http.MethodDelete, "/api/v1/labels/2156154810", nil, `{"id":"2156154810","result":"deleted"}`},
		{"a relative reminder", "todoist.reminders.create", "writer",
			`{"task_id":"taskDue","minute_offset":30,"is_urgent":true}`, []string{"/api/v1/tasks/taskDue"},
			http.MethodPost, "/api/v1/reminders",
			map[string]any{"task_id": "taskDue", "reminder_type": "relative", "minute_offset": float64(30),
				"is_urgent": true}, `"task_id":"taskDue"`},
		{"an absolute reminder by text", "todoist.reminders.create", "accountwriter",
			`{"task_id":"taskDue","due_string":"morgen 9 Uhr","due_lang":"de"}`, nil, http.MethodPost,
			"/api/v1/reminders", map[string]any{"task_id": "taskDue", "reminder_type": "absolute",
				"due": map[string]any{"string": "morgen 9 Uhr", "lang": "de"}}, `"id":"remNew"`},
		{"an absolute reminder by time", "todoist.reminders.create", "writer",
			`{"task_id":"taskDue","due_datetime":"2026-09-30T11:00:00+02:00"}`, []string{"/api/v1/tasks/taskDue"},
			http.MethodPost, "/api/v1/reminders", map[string]any{"task_id": "taskDue", "reminder_type": "absolute",
				"due": map[string]any{"date": "2026-09-30T09:00:00Z"}}, `"id":"remNew"`},
		{"a reminder update", "todoist.reminders.update", "writer", `{"reminder_id":"remOne","minute_offset":60}`,
			[]string{"/api/v1/reminders/remOne", "/api/v1/tasks/taskDue"}, http.MethodPost, "/api/v1/reminders/remOne",
			map[string]any{"minute_offset": float64(60)}, `"id":"remOne"`},
		{"a reminder delete", "todoist.reminders.delete", "writer", `{"reminder_id":"remOne"}`,
			[]string{"/api/v1/reminders/remOne", "/api/v1/tasks/taskDue"}, http.MethodDelete, "/api/v1/reminders/remOne",
			nil, `{"id":"remOne","result":"deleted"}`},
		{"a reminder delete on the account", "todoist.reminders.delete", "accountwriter", `{"reminder_id":"remOne"}`,
			nil, http.MethodDelete, "/api/v1/reminders/remOne", nil, `"result":"deleted"`},
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
			if tt.body == nil {
				if sent[0].body != "" {
					t.Errorf("body = %q, want none", sent[0].body)
				}
				return
			}
			if body := bodyOf(t, sent[0]); fmt.Sprint(body) != fmt.Sprint(tt.body) {
				t.Errorf("body = %v, want %v", body, tt.body)
			}
		})
	}

	// A section moves through exactly one typed Sync command with its two arguments and nothing else.
	before := len(env.fake.recorded())
	result, err := env.change("todoist.sections.move", "multiwriter",
		`{"section_id":"sectOwn","project_id":"`+otherProject+`"}`)
	if err != nil || result != `{"id":"sectOwn","result":"moved"}` {
		t.Fatalf("move = %s, %v", result, err)
	}
	requests := env.fake.recorded()[before:]
	sent := writes(requests)
	if len(requests) != 2 || requests[0].path != "/api/v1/sections/sectOwn" || len(sent) != 1 ||
		sent[0].path != "/api/v1/sync" || len(sent[0].form) != 1 || sent[0].requestID == "" ||
		sent[0].contentType != "application/x-www-form-urlencoded" {
		t.Fatalf("requests = %+v, want the section read and one Sync command", requests)
	}
	var commands []map[string]any
	if err := json.Unmarshal([]byte(sent[0].form.Get("commands")), &commands); err != nil || len(commands) != 1 ||
		len(commands[0]) != 3 || commands[0]["type"] != "section_move" ||
		fmt.Sprint(commands[0]["args"]) != fmt.Sprint(map[string]any{"id": "sectOwn", "project_id": otherProject}) ||
		!regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).
			MatchString(fmt.Sprint(commands[0]["uuid"])) {
		t.Errorf("commands = %s, want one section_move with a fresh UUID", sent[0].form.Get("commands"))
	}

	// No request left the personal routes, and no project was created in a workspace.
	for _, request := range env.fake.recorded() {
		if !allowedRoute.MatchString(request.path) {
			t.Errorf("a request reached %s %s", request.method, request.path)
		}
		if strings.Contains(request.body, "workspace") {
			t.Errorf("a request names a workspace: %+v", request)
		}
	}
}

// No tool reaches workspaces, users, account or notification settings, collaborators, or a generic Sync
// or API route: every tool belongs to one of the personal resources.
func TestNoToolReachesAccountAdministration(t *testing.T) {
	personal := map[string]bool{"projects": true, "sections": true, "labels": true, "tasks": true,
		"completedtasks": true, "comments": true, "reminders": true, "filters": true}
	for _, descriptor := range registry(t).Provider(Provider) {
		parts := strings.Split(descriptor.ID, ".")
		if len(parts) != 3 || !personal[parts[1]] {
			t.Errorf("tool %s reaches beyond the personal resources", descriptor.ID)
		}
		schema := string(descriptor.InputSchema)
		for _, forbidden := range []string{"workspace", "commands", "notification", "collaborator", "folder",
			"\"path\"", "\"method\"", "\"url\""} {
			if strings.Contains(schema, forbidden) {
				t.Errorf("tool %s takes %s", descriptor.ID, forbidden)
			}
		}
	}
}

// A project with subprojects is archived or deleted on a project connection only when every subproject at
// any depth belongs to the connection: Todoist changes them with it. The check reads every page of the
// project tree, archived projects for a delete as well, and refuses a tree it cannot read to the end.
func TestSubprojectsBoundArchiveAndDelete(t *testing.T) {
	tree := func(active [][]string, archived []string, endless bool) *fakeTodoist {
		return &fakeTodoist{failure: func(w http.ResponseWriter, r *http.Request) bool {
			if r.Method != http.MethodGet {
				return false
			}
			entries := func(pairs []string) string {
				var projects []string
				for i := 0; i+1 < len(pairs); i += 2 {
					parent := "null"
					if pairs[i+1] != "" {
						parent = `"` + pairs[i+1] + `"`
					}
					projects = append(projects, `{"id":"`+pairs[i]+`","name":"`+foreignCanary+`","parent_id":`+parent+`}`)
				}
				return strings.Join(projects, ",")
			}
			switch r.URL.Path {
			case "/api/v1/projects":
				page := 0
				if cursor := r.URL.Query().Get("cursor"); cursor != "" {
					fmt.Sscanf(cursor, "page.%d", &page)
				}
				next := "null"
				if endless || page+1 < len(active) {
					next = fmt.Sprintf(`"page.%d"`, page+1)
				}
				current := []string{}
				if page < len(active) {
					current = active[page]
				}
				fmt.Fprintf(w, `{"results":[%s],"next_cursor":%s}`, entries(current), next)
				return true
			case "/api/v1/projects/archived":
				fmt.Fprintf(w, `{"results":[%s],"next_cursor":null}`, entries(archived))
				return true
			}
			return false
		}}
	}
	active := [][]string{{ownProject, "", otherProject, ownProject}, {foreignID, ""}}

	// The subproject belongs to the connection, and the foreign project is no descendant.
	f := tree(active, []string{"projGone", otherProject}, false)
	env := newEnvironment(t, f)
	if result, err := env.change("todoist.projects.archive", "multiorganizer", `{"project_id":"`+ownProject+`"}`); err != nil ||
		!strings.Contains(result, "archived") {
		t.Fatalf("archive = %s, %v", result, err)
	}
	var cursors []string
	for _, request := range f.recorded() {
		if request.path == "/api/v1/projects" {
			cursors = append(cursors, request.query.Get("cursor")+"/"+request.query.Get("limit"))
		}
		if request.path == "/api/v1/projects/archived" {
			t.Error("an archive read the archived projects")
		}
	}
	if strings.Join(cursors, " ") != "/200 page.1/200" {
		t.Errorf("tree pages = %v, want both pages of the active projects", cursors)
	}

	// A delete takes an archived descendant outside the connection with it and is refused.
	before := len(f.recorded())
	_, err := env.change("todoist.projects.delete", "multiorganizer", `{"project_id":"`+ownProject+`"}`)
	if !isInvalidRequest(err) || strings.Contains(err.Error(), foreignCanary) ||
		len(writes(f.recorded()[before:])) != 0 {
		t.Errorf("delete = %v, want a refusal before anything is sent", err)
	}
	// So does an archive with an active descendant outside it, on a later page.
	f = tree([][]string{{ownProject, "", otherProject, ownProject}, {"projKid", otherProject}}, nil, false)
	env = newEnvironment(t, f)
	if _, err := env.change("todoist.projects.archive", "multiorganizer", `{"project_id":"`+ownProject+`"}`); !isInvalidRequest(err) ||
		len(writes(f.recorded())) != 0 {
		t.Errorf("archive = %v, want a refusal before anything is sent", err)
	}
	// An account connection holds every subproject and reads no tree.
	if _, err := env.change("todoist.projects.archive", "organizer", `{"project_id":"`+ownProject+`"}`); err != nil {
		t.Errorf("account archive = %v", err)
	}
	// A tree that never ends is refused unchecked.
	f = tree(nil, nil, true)
	env = newEnvironment(t, f)
	_, err = env.change("todoist.projects.delete", "multiorganizer", `{"project_id":"`+ownProject+`"}`)
	if classOf(err) != provider.ClassProviderError || len(writes(f.recorded())) != 0 ||
		len(f.recorded()) != 1+maxScanPages {
		t.Errorf("endless tree = %v after %d requests, want a refusal after %d pages", err, len(f.recorded()),
			maxScanPages)
	}
}

// A destructive or non-idempotent structure change whose outcome is unclear is reported as possibly
// applied and is never sent again; one Todoist refused is reported as refused. No error carries the change's
// content or the token.
func TestUnclearStructureChangesAreNeverRepeated(t *testing.T) {
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
		{"a refusal", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"Invalid argument `+contentCanary+`"}`)
		}, provider.ClassProviderError, false},
		{"a forbidden change", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error_tag":"FORBIDDEN"}`)
		}, provider.ClassPermission, false},
	}
	changes := []struct{ operation, arguments string }{
		{"todoist.projects.delete", `{"project_id":"` + ownProject + `"}`},
		{"todoist.projects.archive", `{"project_id":"` + ownProject + `"}`},
		{"todoist.sections.delete", `{"section_id":"sectOwn"}`},
		{"todoist.labels.delete", `{"label_id":"2156154810"}`},
		{"todoist.reminders.delete", `{"reminder_id":"remOne"}`},
		{"todoist.sections.move", `{"section_id":"sectOwn","project_id":"` + otherProject + `"}`},
		{"todoist.projects.create", `{"name":"` + contentCanary + `"}`},
		{"todoist.labels.create", `{"name":"` + contentCanary + `"}`},
		{"todoist.reminders.create", `{"task_id":"taskDue","due_string":"` + contentCanary + `"}`},
	}
	for _, answer := range answers {
		for _, change := range changes {
			t.Run(answer.name+" of "+change.operation, func(t *testing.T) {
				f := &fakeTodoist{failure: func(w http.ResponseWriter, r *http.Request) bool {
					if r.Method == http.MethodGet {
						return false
					}
					answer.answer(w)
					return true
				}}
				env := newEnvironment(t, f)
				_, err := env.change(change.operation, "organizer", change.arguments)
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
}

// A section move reports the result Todoist gives its one command: a refusal keeps its class, and an
// answer without that command's result may have been applied.
func TestTheSectionMoveReadsItsCommandResult(t *testing.T) {
	for _, tt := range []struct {
		name, status string
		class        provider.Class
		detail       string
		uncertain    bool
	}{
		{"a plan limit", `{"error_tag":"PREMIUM_ONLY","error_code":32,"error":"Premium only","http_code":403}`,
			provider.ClassPermission, planMessage, false},
		{"a forbidden move", `{"error_tag":"FORBIDDEN","error":"` + contentCanary + `","http_code":403}`,
			provider.ClassPermission, changeDenied, false},
		{"a missing section", `{"error_tag":"NOT_FOUND","http_code":404}`, provider.ClassProviderError,
			notFoundMessage, false},
		{"an invalid move", `{"error_tag":"INVALID_ARGUMENT_VALUE","error":"` + contentCanary + `","http_code":400}`,
			provider.ClassProviderError, "invalid", false},
		{"an unknown result", `"pending"`, provider.ClassInvalidResponse, "", true},
		{"no result", "", provider.ClassInvalidResponse, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var f *fakeTodoist
			f = &fakeTodoist{failure: func(w http.ResponseWriter, r *http.Request) bool {
				if r.URL.Path != "/api/v1/sync" {
					return false
				}
				requests := f.recorded()
				var commands []struct {
					UUID string `json:"uuid"`
				}
				_ = json.Unmarshal([]byte(requests[len(requests)-1].form.Get("commands")), &commands)
				status := `{}`
				if tt.status != "" && len(commands) == 1 {
					status = `{"` + commands[0].UUID + `":` + tt.status + `}`
				}
				fmt.Fprint(w, `{"sync_token":"abc","sync_status":`+status+`}`)
				return true
			}}
			env := newEnvironment(t, f)
			_, err := env.change("todoist.sections.move", "multiorganizer",
				`{"section_id":"sectOwn","project_id":"`+otherProject+`"}`)
			if classOf(err) != tt.class || !strings.Contains(err.Error(), tt.detail) ||
				strings.Contains(err.Error(), "may have been applied") != tt.uncertain ||
				strings.Contains(err.Error(), contentCanary) {
				t.Errorf("err = %v (class %q), want class %q with %q, possibly applied = %t", err, classOf(err),
					tt.class, tt.detail, tt.uncertain)
			}
			if sent := writes(f.recorded()); len(sent) != 1 {
				t.Errorf("changes sent = %d, want exactly one", len(sent))
			}
		})
	}
}

// When the account's plan lacks reminders or more labels, the change ends as a permission failure with the
// plan message, was not applied, and is not repeated.
func TestPlanLimitsArePermissionFailures(t *testing.T) {
	for _, tt := range []struct{ operation, connection, arguments string }{
		{"todoist.reminders.create", "writer", `{"task_id":"taskDue","minute_offset":30}`},
		{"todoist.reminders.update", "writer", `{"reminder_id":"remOne","due_datetime":"2026-09-30T09:00:00Z"}`},
		{"todoist.labels.create", "organizer", `{"name":"` + contentCanary + `"}`},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			f := &fakeTodoist{failure: func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method == http.MethodGet {
					return false
				}
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"error_tag":"PREMIUM_ONLY","error_code":32,"error":"Premium only feature","http_code":403}`)
				return true
			}}
			env := newEnvironment(t, f)
			_, err := env.change(tt.operation, tt.connection, tt.arguments)
			if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), planMessage) ||
				strings.Contains(err.Error(), "may have been applied") || strings.Contains(err.Error(), "PREMIUM") ||
				strings.Contains(err.Error(), contentCanary) {
				t.Errorf("err = %v (class %q), want the plan permission failure", err, classOf(err))
			}
			if sent := writes(f.recorded()); len(sent) != 1 {
				t.Errorf("changes sent = %d, want exactly one", len(sent))
			}
		})
	}
}

// A reminder is read by its ID only when its task belongs to the connection.
func TestRemindersGetStaysOnTheConnectionsTasks(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	result, err := env.invoke("todoist.reminders.get", "one", `{"reminder_id":"remOne"}`)
	if err != nil || result != `{"id":"remOne","is_urgent":false,"minute_offset":30,"task_id":"taskDue","type":"relative"}` {
		t.Errorf("reminder = %s, %v", result, err)
	}
	requests := env.fake.recorded()
	if len(requests) != 2 || requests[0].path != "/api/v1/reminders/remOne" || requests[1].path != "/api/v1/tasks/taskDue" {
		t.Errorf("requests = %+v, want the reminder and its task", requests)
	}
	if _, err := env.invoke("todoist.reminders.get", "one", `{"reminder_id":"remMissing"}`); classOf(err) != provider.ClassProviderError {
		t.Errorf("missing reminder = %v, want a provider error", err)
	}
}

// Completed tasks are reached over every page: each page announces the next while Todoist has one, a full
// page never looks like the end, and the project, section, and filter narrow every page alike.
func TestCompletedTasksReachEveryPage(t *testing.T) {
	pages := [][]string{{"done1", ownProject, "done2", ownProject}, {"done3", foreignID, "done4", ownProject},
		{"done5", ownProject}}
	f := &fakeTodoist{failure: func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v1/tasks/completed/by_completion_date" {
			return false
		}
		page := 0
		if cursor := r.URL.Query().Get("cursor"); cursor != "" {
			fmt.Sscanf(cursor, "page.%d", &page)
		}
		var items []string
		for i := 0; i+1 < len(pages[page]); i += 2 {
			items = append(items, taskJSON(pages[page][i], pages[page][i+1], "null"))
		}
		next := "null"
		if page+1 < len(pages) {
			next = fmt.Sprintf(`"page.%d"`, page+1)
		}
		fmt.Fprintf(w, `{"items":[%s],"next_cursor":%s}`, strings.Join(items, ","), next)
		return true
	}}
	env := newEnvironment(t, f)
	arguments := `"since":"2026-09-01T00:00:00Z","until":"2026-10-01T00:00:00Z","project_id":"` + ownProject + `",` +
		`"section_id":"sectOwn","query":"@waiting","limit":2`
	var got []string
	cursor := ""
	for round := 0; ; round++ {
		call := `{` + arguments + `}`
		if cursor != "" {
			call = `{` + arguments + `,"cursor":"` + cursor + `"}`
		}
		result, err := env.invoke("todoist.completedtasks.list", "multi", call)
		if err != nil || strings.Contains(result, foreignCanary) {
			t.Fatalf("page %d = %s, %v", round, result, err)
		}
		var listed TaskList
		if err := json.Unmarshal([]byte(result), &listed); err != nil {
			t.Fatal(err)
		}
		for _, task := range listed.Tasks {
			got = append(got, task.ID)
		}
		if round < len(pages)-1 && (!listed.HasMore || listed.NextCursor == "") {
			t.Fatalf("page %d = %s, want it to announce the next page", round, result)
		}
		if !listed.HasMore {
			if round != len(pages)-1 || listed.NextCursor != "" {
				t.Fatalf("page %d = %s ended the list early", round, result)
			}
			break
		}
		cursor = listed.NextCursor
	}
	if strings.Join(got, " ") != "done1 done2 done4 done5" {
		t.Errorf("tasks = %v, want every task of the connection over all pages", got)
	}
	requests := f.recorded()
	if len(requests) != len(pages) {
		t.Fatalf("requests = %d, want one per page", len(requests))
	}
	for i, request := range requests {
		query := request.query
		if query.Get("project_id") != ownProject || query.Get("section_id") != "sectOwn" ||
			query.Get("filter_query") != "@waiting" || query.Get("limit") != "2" ||
			query.Get("since") != "2026-09-01T00:00:00Z" || query.Get("until") != "2026-10-01T00:00:00Z" {
			t.Errorf("page %d query = %v, want the same narrowing on every page", i, query)
		}
		if want := map[bool]string{true: "", false: fmt.Sprintf("page.%d", i)}[i == 0]; query.Get("cursor") != want {
			t.Errorf("page %d cursor = %q, want %q", i, query.Get("cursor"), want)
		}
	}
	// A cursor belongs to its filter.
	if _, err := env.invoke("todoist.completedtasks.list", "multi",
		`{"since":"2026-09-01T00:00:00Z","until":"2026-10-01T00:00:00Z","project_id":"`+ownProject+
			`","section_id":"sectOwn","query":"@other","limit":2,"cursor":"`+cursor+`"}`); !isInvalidRequest(err) {
		t.Errorf("a cursor of another filter = %v, want an invalid request", err)
	}
}
