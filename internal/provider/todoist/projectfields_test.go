package todoist

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// richPersonalProject is a personal project in the style of the Todoist documentation with every member of
// its answer set, including the access value public_key and the deletion mark that are never shown.
const richPersonalProject = `{"id":"` + ownProject + `","can_assign_tasks":true,"can_comment":true,"child_order":1,` +
	`"order_key":"a0","is_collapsed":false,"color":"charcoal","icon":"lightbulb","creator_uid":"1234567",` +
	`"created_at":"2025-01-15T10:30:00Z","is_archived":true,"is_deleted":false,"is_favorite":false,` +
	`"is_frozen":false,"name":"Inbox","is_shared":false,"updated_at":"2025-01-16T10:30:00Z","view_style":"list",` +
	`"default_order":2,"default_order_key":"a1","description":"Plan","public_key":"canary-public-key-4f2a",` +
	`"access":{"visibility":"public","configuration":{"hide_collaborator_details":true,` +
	`"disable_duplication":false}},"role":"CREATOR","parent_id":"projOwn2","inbox_project":false}`

const richPersonalProjectOutput = `{"id":"` + ownProject + `","name":"Inbox","parent_id":"projOwn2",` +
	`"color":"charcoal","is_favorite":false,"is_shared":false,"is_inbox":false,"view_style":"list",` +
	`"description":"Plan","is_archived":true,"created_at":"2025-01-15T10:30:00Z",` +
	`"updated_at":"2025-01-16T10:30:00Z","can_assign_tasks":true,"can_comment":true,"child_order":1,` +
	`"order_key":"a0","is_collapsed":false,"icon":"lightbulb","creator_uid":"1234567","is_frozen":false,` +
	`"default_order":2,"default_order_key":"a1","access":{"visibility":"public","configuration":` +
	`{"hide_collaborator_details":true,"disable_duplication":false}},"role":"CREATOR"}`

const richWorkspaceProject = `{"id":"` + ownProject + `","can_assign_tasks":false,"can_comment":true,"child_order":0,` +
	`"order_key":null,"is_collapsed":true,"color":"blue","icon":null,"creator_uid":"7654321",` +
	`"created_at":"2025-02-01T08:00:00Z","is_archived":false,"is_deleted":false,"is_favorite":true,` +
	`"is_frozen":true,"name":"Roadmap","is_shared":true,"updated_at":null,"view_style":"board",` +
	`"default_order":0,"default_order_key":"b2","description":"","public_key":"","access":` +
	`{"visibility":"team","configuration":{}},"role":"MEMBER","collaborator_role_default":"READ_WRITE",` +
	`"folder_id":"9876","is_invite_only":false,"is_link_sharing_enabled":true,"status":"IN_PROGRESS",` +
	`"workspace_id":"4567","is_pending_default_collaborator_invites":true,"is_project_insights_enabled":false,` +
	`"parent_id":null}`

const richWorkspaceProjectOutput = `{"id":"` + ownProject + `","name":"Roadmap","color":"blue","is_favorite":true,` +
	`"is_shared":true,"is_inbox":false,"view_style":"board","created_at":"2025-02-01T08:00:00Z",` +
	`"can_assign_tasks":false,"can_comment":true,"child_order":0,"is_collapsed":true,"creator_uid":"7654321",` +
	`"is_frozen":true,"default_order":0,"default_order_key":"b2","access":{"visibility":"team"},` +
	`"role":"MEMBER","workspace_id":"4567","folder_id":"9876","is_invite_only":false,"status":"IN_PROGRESS",` +
	`"is_link_sharing_enabled":true,"collaborator_role_default":"READ_WRITE",` +
	`"is_pending_default_collaborator_invites":true,"is_project_insights_enabled":false}`

const richSection = `{"id":"sectOwn","user_id":"1234567","project_id":"` + ownProject + `",` +
	`"added_at":"2025-01-15T10:30:00Z","updated_at":"2025-01-16T10:30:00Z","archived_at":"2025-01-17T10:30:00Z",` +
	`"name":"Groceries","description":"Tasks for the launch plan","section_order":3,"order_key":"a1V",` +
	`"is_collapsed":true,"is_archived":true,"is_deleted":false}`

const richSectionOutput = `{"id":"sectOwn","project_id":"` + ownProject + `","name":"Groceries","order":3,` +
	`"description":"Tasks for the launch plan","user_id":"1234567","added_at":"2025-01-15T10:30:00Z",` +
	`"updated_at":"2025-01-16T10:30:00Z","archived_at":"2025-01-17T10:30:00Z","is_archived":true,` +
	`"is_collapsed":true,"order_key":"a1V"}`

// projectShowingTools and sectionShowingTools are the tools that show a project or a section, with the call that reaches
// each. The changing ones share the output schema of the reading ones.
var projectShowingTools = []struct{ tool, connection, arguments string }{
	{"todoist.projects.list", "one", `{}`},
	{"todoist.projects.get", "one", `{"project_id":"` + ownProject + `"}`},
	{"todoist.projects.create", "accountwriter", `{"name":"Inbox"}`},
	{"todoist.projects.update", "writer", `{"project_id":"` + ownProject + `","name":"Inbox"}`},
}

var sectionShowingTools = []struct{ tool, connection, arguments string }{
	{"todoist.sections.list", "one", `{}`},
	{"todoist.sections.get", "one", `{"section_id":"sectOwn"}`},
	{"todoist.sections.create", "writer", `{"name":"Groceries","project_id":"` + ownProject + `"}`},
	{"todoist.sections.update", "writer", `{"section_id":"sectOwn","name":"Groceries"}`},
	{"todoist.sections.reorder", "writer", `{"section_id":"sectOwn","order":3}`},
}

// structureTodoist answers every project and section route with the given bodies.
func structureTodoist(project, section string) *fakeTodoist {
	return &fakeTodoist{failure: func(w http.ResponseWriter, r *http.Request) bool {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		w.Header().Set("Content-Type", "application/json")
		listing := r.Method == http.MethodGet && (path == "/projects" || path == "/sections")
		switch {
		case listing && path == "/projects":
			fmt.Fprintf(w, `{"results":[%s],"next_cursor":null}`, project)
		case listing:
			fmt.Fprintf(w, `{"results":[%s],"next_cursor":null}`, section)
		case strings.HasPrefix(path, "/projects/") && !strings.HasSuffix(path, "/archived"), path == "/projects":
			fmt.Fprint(w, project)
		case strings.HasPrefix(path, "/sections/"), path == "/sections":
			fmt.Fprint(w, section)
		default:
			return false
		}
		return true
	}}
}

func shown(t *testing.T, env *environment, tool, connection, arguments, key string) (string, map[string]any) {
	t.Helper()
	run := env.invoke
	if strings.HasSuffix(connection, "writer") {
		run = env.change
	}
	result, err := run(tool, connection, arguments)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	if list, ok := out[key].([]any); ok {
		if len(list) != 1 {
			t.Fatalf("%s returned %d entries", tool, len(list))
		}
		return result, list[0].(map[string]any)
	}
	return result, out
}

func TestProjectToolsShowThePersonalAndTheWorkspaceAnswerExactly(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"personal":  {richPersonalProject, richPersonalProjectOutput},
		"workspace": {richWorkspaceProject, richWorkspaceProjectOutput},
	} {
		var want map[string]any
		if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
			t.Fatal(err)
		}
		for _, tt := range projectShowingTools {
			if name == "workspace" && strings.HasSuffix(tt.connection, "writer") {
				continue // a change of a workspace project is refused
			}
			t.Run(name+"/"+tt.tool, func(t *testing.T) {
				env := newEnvironment(t, structureTodoist(tc.body, richSection))
				result, got := shown(t, env, tt.tool, tt.connection, tt.arguments, "projects")
				for _, hidden := range []string{"public_key", "canary-public-key", "is_deleted"} {
					if strings.Contains(result, hidden) {
						t.Errorf("result carries %s: %s", hidden, result)
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("project = %v\nwant     %v", got, want)
				}
			})
		}
	}
}

func TestSectionToolsShowTheDocumentedAnswerExactly(t *testing.T) {
	var want map[string]any
	if err := json.Unmarshal([]byte(richSectionOutput), &want); err != nil {
		t.Fatal(err)
	}
	for _, tt := range sectionShowingTools {
		t.Run(tt.tool, func(t *testing.T) {
			env := newEnvironment(t, structureTodoist(richPersonalProject, richSection))
			result, got := shown(t, env, tt.tool, tt.connection, tt.arguments, "sections")
			if strings.Contains(result, "is_deleted") {
				t.Errorf("result carries is_deleted: %s", result)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("section = %v\nwant     %v", got, want)
			}
		})
	}
}

func sameJSON(a, b string) bool {
	var x, y any
	return json.Unmarshal([]byte(a), &x) == nil && json.Unmarshal([]byte(b), &y) == nil && reflect.DeepEqual(x, y)
}

func TestAnswerWithoutTheNewMembersStaysAsItWas(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	result, err := env.invoke("todoist.projects.get", "one", `{"project_id":"`+ownProject+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"projOwn1","name":"Project projOwn1","color":"blue","is_favorite":false,"is_shared":false,` +
		`"is_inbox":false,"view_style":"list","description":"about Project projOwn1",` +
		`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}`
	if !sameJSON(result, want) {
		t.Errorf("project = %s\nwant     %s", result, want)
	}
	result, err = env.invoke("todoist.sections.get", "one", `{"section_id":"sectOwn"}`)
	if err != nil {
		t.Fatal(err)
	}
	want = `{"id":"sectOwn","project_id":"projOwn1","name":"Doing","order":1,"is_archived":false}`
	if !sameJSON(result, want) {
		t.Errorf("section = %s\nwant     %s", result, want)
	}
}

func TestRowsOutsideTheConnectionAreStillDropped(t *testing.T) {
	env := newEnvironment(t, &fakeTodoist{})
	for tool, key := range map[string]string{"todoist.projects.list": "projects", "todoist.sections.list": "sections"} {
		result, err := env.invoke(tool, "one", `{}`)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(result), &out); err != nil {
			t.Fatal(err)
		}
		if list, _ := out[key].([]any); len(list) != 1 || strings.Contains(result, foreignCanary) || strings.Contains(result, foreignID) {
			t.Errorf("%s = %s, want only the project of the connection", tool, result)
		}
	}
}

func TestAbsentProjectMembersStayOutAndZeroesStay(t *testing.T) {
	long := strings.Repeat("ä", 400)
	body := `{"id":"` + ownProject + `","name":"x","can_comment":false,"child_order":0,"order_key":"a\nb",` +
		`"icon":"i\u0007c","creator_uid":null,"default_order":0,"default_order_key":"` + long + `",` +
		`"access":{"visibility":"` + long + `","configuration":null},"role":"` + long + `","workspace_id":4567,` +
		`"folder_id":null,"status":"S\u0007","collaborator_role_default":"` + long + `",` +
		`"inbox_project":false,"is_favorite":false,"is_shared":false}`
	env := newEnvironment(t, structureTodoist(body, richSection))
	result, err := env.invoke("todoist.projects.get", "one", `{"project_id":"`+ownProject+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	var got Project
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"creator_uid", "folder_id", "is_frozen", "can_assign_tasks", "is_collapsed",
		"is_invite_only"} {
		if strings.Contains(result, `"`+key+`"`) {
			t.Errorf("%s appears although absent: %s", key, result)
		}
	}
	if got.CanComment == nil || *got.CanComment || got.ChildOrder == nil || *got.ChildOrder != 0 ||
		got.DefaultOrder == nil || *got.DefaultOrder != 0 {
		t.Errorf("zero values lost: %s", result)
	}
	if got.OrderKey != "a b" || got.Icon != "i c" || got.Status != "S" || got.WorkspaceID != "4567" {
		t.Errorf("cleaning or ID wrong: %s", result)
	}
	for name, value := range map[string]string{"default_order_key": got.DefaultOrderKey,
		"visibility": got.Access.Visibility, "role": got.Role, "collaborator_role_default": got.CollaboratorRoleDefault} {
		limit := maxTagText
		if name == "default_order_key" {
			limit = maxOrderKey
		}
		if len(value) == 0 || len(value) > limit {
			t.Errorf("%s has %d bytes, want 1 through %d", name, len(value), limit)
		}
	}
	if got.Access.Configuration != nil {
		t.Errorf("empty configuration shown: %s", result)
	}
}

func TestEveryProjectAndSectionMemberIsInSchemaAndFields(t *testing.T) {
	reg := registry(t)
	check := func(t *testing.T, tool, connection, arguments, key, project string) {
		env := newEnvironment(t, structureTodoist(project, richSection))
		_, member := shown(t, env, tool, connection, arguments, key)
		var fields map[string]bool
		var properties map[string]any
		for _, d := range reg.Provider(Provider) {
			if d.ID != tool {
				continue
			}
			var schema map[string]any
			if err := json.Unmarshal(d.OutputSchema, &schema); err != nil {
				t.Fatal(err)
			}
			properties, _ = schema["properties"].(map[string]any)
			if list, ok := properties[key].(map[string]any); ok {
				properties = list["items"].(map[string]any)["properties"].(map[string]any)
			}
			fields = map[string]bool{}
			for _, f := range d.Fields {
				fields[f.Name] = true
			}
		}
		var problems []string
		for name := range member {
			if _, ok := properties[name]; !ok || !fields[name] {
				problems = append(problems, name+" (output)")
			}
		}
		for name := range properties {
			if !fields[name] {
				problems = append(problems, name+" (schema)")
			}
		}
		for _, hidden := range []string{"public_key", "is_deleted"} {
			if _, ok := properties[hidden]; ok || fields[hidden] {
				problems = append(problems, hidden+" (published)")
			}
		}
		sort.Strings(problems)
		if len(problems) > 0 {
			t.Errorf("not in schema or fields: %v", problems)
		}
	}
	for _, tt := range projectShowingTools {
		t.Run(tt.tool, func(t *testing.T) { check(t, tt.tool, tt.connection, tt.arguments, "projects", richPersonalProject) })
		if tt.connection != "writer" {
			t.Run(tt.tool+"/workspace", func(t *testing.T) {
				check(t, tt.tool, tt.connection, tt.arguments, "projects", richWorkspaceProject)
			})
		}
	}
	for _, tt := range sectionShowingTools {
		t.Run(tt.tool, func(t *testing.T) { check(t, tt.tool, tt.connection, tt.arguments, "sections", richPersonalProject) })
	}
}
