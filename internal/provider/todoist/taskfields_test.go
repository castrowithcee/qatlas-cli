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

// richTask is a task in the style of the Todoist documentation with every member of its answer set.
const richTask = `{"id":"6X7rM8997g3RQmvh","user_id":"2671355","project_id":"` + ownProject + `",` +
	`"section_id":"6Jf8VQXxpwv56VQ7","parent_id":"6X7rM8997g3RQmvg","added_by_uid":"2671362",` +
	`"assigned_by_uid":"2671363","responsible_uid":"2671364","completed_by_uid":"2671365",` +
	`"content":"Buy Milk","description":"2 liters","labels":["Food","Shopping"],"priority":2,` +
	`"due":{"date":"2026-09-30T09:00:00","string":"tomorrow 9am","lang":"en","is_recurring":false,` +
	`"timezone":"Europe/Berlin"},"deadline":{"date":"2026-10-05","lang":"en"},` +
	`"duration":{"amount":90,"unit":"minute"},"is_collapsed":false,"checked":true,"child_order":3,` +
	`"day_order":-1,"order_key":"a0b1","completed_count":0,"postponed_count":2,"note_count":4,` +
	`"is_deleted":false,"added_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-02T00:00:00Z",` +
	`"completed_at":"2026-09-03T00:00:00Z"}`

const richTaskOutput = `{"id":"6X7rM8997g3RQmvh","content":"Buy Milk","project_id":"` + ownProject + `",` +
	`"section_id":"6Jf8VQXxpwv56VQ7","parent_id":"6X7rM8997g3RQmvg","labels":["Food","Shopping"],"priority":2,` +
	`"due":{"date":"2026-09-30T09:00:00","string":"tomorrow 9am","is_recurring":false,` +
	`"timezone":"Europe/Berlin","lang":"en"},"deadline":"2026-10-05",` +
	`"deadline_details":{"date":"2026-10-05","lang":"en"},"duration":{"amount":90,"unit":"minute"},` +
	`"description":"2 liters","user_id":"2671355","added_by_uid":"2671362","assigned_by_uid":"2671363",` +
	`"responsible_uid":"2671364","completed_by_uid":"2671365","is_collapsed":false,"checked":true,` +
	`"child_order":3,"day_order":-1,"order_key":"a0b1","completed_count":0,"postponed_count":2,` +
	`"added_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-02T00:00:00Z","completed_at":"2026-09-03T00:00:00Z"}`

// taskTools are the seven tools that show a task, with the call that reaches each.
var taskTools = []struct{ tool, connection, arguments string }{
	{"todoist.tasks.list", "one", `{}`},
	{"todoist.tasks.filter", "one", `{"query":"today"}`},
	{"todoist.tasks.get", "one", `{"task_id":"taskRich"}`},
	{"todoist.completed-tasks.list", "one", `{"since":"2026-09-01T00:00:00Z","until":"2026-10-01T00:00:00Z"}`},
	{"todoist.tasks.create", "writer", `{"content":"Buy Milk"}`},
	{"todoist.tasks.update", "writer", `{"task_id":"taskRich","content":"Buy Milk"}`},
	{"todoist.tasks.move", "writer", `{"task_id":"taskRich","project_id":"` + ownProject + `"}`},
}

// richTodoist answers every task route with richTask.
func richTodoist(body string) *fakeTodoist {
	return &fakeTodoist{failure: func(w http.ResponseWriter, r *http.Request) bool {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case path == "/tasks" && r.Method == http.MethodGet, path == "/tasks/filter",
			path == "/tasks/completed/by_completion_date":
			key := "results"
			if strings.Contains(path, "completed") {
				key = "items"
			}
			fmt.Fprintf(w, `{"%s":[%s],"next_cursor":null}`, key, body)
		case strings.HasPrefix(path, "/tasks"):
			fmt.Fprint(w, body)
		default:
			return false
		}
		return true
	}}
}

// singleTask extracts the one task of a tool's result.
func singleTask(t *testing.T, tool, result string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	if list, ok := out["tasks"].([]any); ok {
		if len(list) != 1 {
			t.Fatalf("%s returned %d tasks", tool, len(list))
		}
		return list[0].(map[string]any)
	}
	return out
}

func TestAllSevenTaskToolsShowTheDocumentedAnswerExactly(t *testing.T) {
	var want map[string]any
	if err := json.Unmarshal([]byte(richTaskOutput), &want); err != nil {
		t.Fatal(err)
	}
	for _, tt := range taskTools {
		t.Run(tt.tool, func(t *testing.T) {
			env := newEnvironment(t, richTodoist(richTask))
			result, err := env.invoke(tt.tool, tt.connection, tt.arguments)
			if tt.connection == "writer" {
				result, err = env.change(tt.tool, tt.connection, tt.arguments)
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(result, "comment_count") {
				t.Errorf("result carries comment_count: %s", result)
			}
			if got := singleTask(t, tt.tool, result); !reflect.DeepEqual(got, want) {
				t.Errorf("task = %v\nwant   %v", got, want)
			}
		})
	}
}

func TestAbsentTaskMembersStayOutAndZeroesStay(t *testing.T) {
	const sparse = `{"id":"taskRich","project_id":"` + ownProject + `","content":"x","labels":[],"priority":1,` +
		`"checked":false,"child_order":0,"deadline":null,"duration":null,"user_id":"7\u0007","order_key":"a\nb"}`
	env := newEnvironment(t, richTodoist(sparse))
	result, err := env.invoke("todoist.tasks.get", "one", `{"task_id":"taskRich"}`)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":"taskRich","content":"x","project_id":"` + ownProject + `","priority":1,"user_id":"7",` +
		`"checked":false,"child_order":0,"order_key":"a b"}`
	var got, expected map[string]any
	_ = json.Unmarshal([]byte(result), &got)
	_ = json.Unmarshal([]byte(want), &expected)
	delete(got, "labels")
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("task = %s, want %s", result, want)
	}
}

func TestForeignTaskTextsAreBounded(t *testing.T) {
	long := strings.Repeat("ä", 400)
	body := `{"id":"taskRich","project_id":"` + ownProject + `","content":"x","labels":[],"priority":1,` +
		`"order_key":"` + long + `","duration":{"amount":1,"unit":"` + long + `"},` +
		`"due":{"date":"2026-09-30","lang":"` + long + `","is_recurring":false},` +
		`"deadline":{"date":"2026-10-05","lang":"` + long + `"}}`
	env := newEnvironment(t, richTodoist(body))
	result, err := env.invoke("todoist.tasks.get", "one", `{"task_id":"taskRich"}`)
	if err != nil {
		t.Fatal(err)
	}
	var task Task
	if err := json.Unmarshal([]byte(result), &task); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"order_key": task.OrderKey, "unit": task.Duration.Unit,
		"due.lang": task.Due.Lang, "deadline_details.lang": task.DeadlineDetails.Lang} {
		if len(value) == 0 || len(value) > maxOrderKey {
			t.Errorf("%s has %d bytes, want 1 through %d", name, len(value), maxOrderKey)
		}
	}
}

func TestOnlyTheSevenTaskToolsAreVersion2(t *testing.T) {
	two := map[string]bool{}
	for _, tt := range taskTools {
		two[tt.tool] = true
	}
	for _, d := range registry(t).Provider(Provider) {
		want := 1
		if two[d.ID] {
			want = 2
		}
		if d.Version != want {
			t.Errorf("%s version = %d, want %d", d.ID, d.Version, want)
		}
	}
}

func TestEveryTaskMemberIsInSchemaAndFields(t *testing.T) {
	reg := registry(t)
	for _, tt := range taskTools {
		t.Run(tt.tool, func(t *testing.T) {
			env := newEnvironment(t, richTodoist(richTask))
			result, err := env.invoke(tt.tool, tt.connection, tt.arguments)
			if tt.connection == "writer" {
				result, err = env.change(tt.tool, tt.connection, tt.arguments)
			}
			if err != nil {
				t.Fatal(err)
			}
			task := singleTask(t, tt.tool, result)
			var fields map[string]bool
			var properties map[string]any
			for _, d := range reg.Provider(Provider) {
				if d.ID != tt.tool {
					continue
				}
				var schema map[string]any
				if err := json.Unmarshal(d.OutputSchema, &schema); err != nil {
					t.Fatal(err)
				}
				properties, _ = schema["properties"].(map[string]any)
				if list, ok := properties["tasks"].(map[string]any); ok {
					properties = list["items"].(map[string]any)["properties"].(map[string]any)
				}
				fields = map[string]bool{}
				for _, f := range d.Fields {
					fields[f.Name] = true
				}
			}
			var problems []string
			for key := range task {
				if _, ok := properties[key]; !ok || !fields[key] {
					problems = append(problems, key+" (output)")
				}
			}
			for key := range properties {
				if !fields[key] {
					problems = append(problems, key+" (schema)")
				}
			}
			sort.Strings(problems)
			if len(problems) > 0 {
				t.Errorf("not in schema or fields: %v", problems)
			}
			if _, ok := properties["comment_count"]; ok || fields["comment_count"] {
				t.Error("comment_count is still published")
			}
		})
	}
}
