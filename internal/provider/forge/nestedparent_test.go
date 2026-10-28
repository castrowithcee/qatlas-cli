package forge

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// nestedParentSteps lists each tool that shows a repository object, the argument of the call, the member of the
// answer that holds the rows (empty when the answer is the row), and the member of a row that holds the repository.
var nestedParentSteps = []struct {
	name      string
	tool      *tool
	arguments string
	rows      string
	field     string
}{
	{"combined status", commitStatusesGetCombined, `{"repository":"acme/widget","ref":"main"}`, "", "repository"},
	{"notifications", notificationsList, `{}`, "notifications", "repository"},
	{"repository notifications", repositoryNotificationsList, `{"repository":"acme/widget"}`, "notifications", "repository"},
	{"thread", notificationsGet, `{"id":11}`, "", "repository"},
	{"repository activities", repositoryActivitiesList, `{"repository":"acme/widget"}`, "activities", "repo"},
	{"user activities", userActivitiesList, `{"username":"acme"}`, "activities", "repo"},
	{"team activities", teamActivitiesList, `{"id":7}`, "activities", "repo"},
	{"org activities", orgActivitiesList, `{"organization":"acme"}`, "activities", "repo"},
}

func TestExclusionsHideAnExcludedParentInNestedRepositories(t *testing.T) {
	parent := "upstream/project"
	repo := func() map[string]any {
		var r map[string]any
		_ = json.Unmarshal([]byte(strings.Replace(repositoryJSON("acme/widget", 7), "upstream/project", parent, 1)), &r)
		return r
	}
	marshal := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	thread := func() map[string]any {
		return map[string]any{"id": 11, "unread": true, "repository": repo(), "subject": map[string]any{"title": "t"}}
	}
	activity := func() map[string]any { return map[string]any{"id": 1, "op_type": "x", "repo": repo()} }
	for _, p := range products {
		e := newEnvironment(t, p, func(c call) *http.Response {
			switch {
			case strings.HasSuffix(c.path, "/status"):
				return jsonResponse(http.StatusOK, marshal(map[string]any{"sha": "abc", "state": "success",
					"total_count": 0, "statuses": []any{}, "repository": repo()}))
			case strings.Contains(c.path, "/notifications/threads/"):
				return jsonResponse(http.StatusOK, marshal(thread()))
			case strings.HasSuffix(c.path, "/notifications"):
				return jsonResponse(http.StatusOK, marshal([]any{thread()}))
			case strings.HasSuffix(c.path, "/activities/feeds"):
				return jsonResponse(http.StatusOK, marshal([]any{activity()}))
			}
			return jsonResponse(http.StatusOK, marshal(repo()))
		})
		for _, tc := range []struct {
			parent string
			shown  bool
		}{{"upstream/project", true}, {"acme/gadget", false}, {"ACME/Gadget", false}} {
			parent = tc.parent
			for _, step := range nestedParentSteps {
				result, err := e.invoke(t, step.tool, "exclude", step.arguments)
				if err != nil {
					t.Fatalf("%s %s: %v", p.id, step.name, err)
				}
				row := result
				if step.rows != "" {
					rows := rowsOf(t, result, step.rows)
					if len(rows) != 1 {
						t.Fatalf("%s %s: %v", p.id, step.name, result)
					}
					row = rows[0]
				}
				inner, _ := row[step.field].(map[string]any)
				shown, _ := inner["parent"].(map[string]any)
				if tc.shown && (shown["full_name"] != tc.parent || shown["id"] == nil) {
					t.Errorf("%s %s: parent %s = %v, want identifiers", p.id, step.name, tc.parent, inner["parent"])
				}
				if !tc.shown && (inner["parent"] != nil || inner["fork"] != true) {
					t.Errorf("%s %s: parent %s = %v, want none", p.id, step.name, tc.parent, inner)
				}
			}
		}
	}
}
