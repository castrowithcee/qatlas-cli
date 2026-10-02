package n8n

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const newProject = "PROJECT_NEW_0003CCCC"

func projectJSONOf(id, name string) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"type":"team","icon":null,"description":null,`+
		`"customTelemetryTags":[],"creatorId":null,"createdAt":"2026-01-01T00:00:00.000Z",`+
		`"updatedAt":"2026-01-02T00:00:00.000Z"}`, id, name)
}

// connectionFor picks the connection a project tool can run on: update and delete need a project allow-list
// that holds the target, create needs none.
func connectionFor(operation string) string {
	if operation == projectsUpdate.ID || operation == projectsDelete.ID {
		return "pdelete"
	}
	return "pdelete-open"
}

func noRequest(t *testing.T) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		return nil, nil
	}
}

func TestProjectsListIsFilteredToTheProjectAllowList(t *testing.T) {
	body := fmt.Sprintf(`{"data":[%s,%s],"nextCursor":"abc"}`,
		projectJSONOf(ownProject, "Own"), projectJSONOf(foreignProject, foreignCanary))
	for _, tt := range []struct {
		connection string
		want       int
	}{{"project", 1}, {"open", 2}} {
		var calls []call
		env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet || r.URL.Path != apiPath+"/projects" {
				t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
			}
			return jsonResponse(200, body), nil
		})
		result, err := env.invoke(projectsList.ID, tt.connection, `{}`)
		if err != nil {
			t.Fatalf("%s: invoke() = %v", tt.connection, err)
		}
		var page ProjectsPage
		if err := json.Unmarshal([]byte(result), &page); err != nil || page.Count != tt.want ||
			len(page.Projects) != tt.want || !page.HasMore || page.Cursor != "abc" {
			t.Fatalf("%s: page = %s, %v, want %d projects", tt.connection, result, err, tt.want)
		}
		if tt.connection == "project" && strings.Contains(result, foreignProject) {
			t.Fatalf("restricted list leaked a foreign project: %s", result)
		}
		if calls[0].query.Get("limit") != "100" {
			t.Fatalf("limit = %q, want default 100", calls[0].query.Get("limit"))
		}
	}
}

func TestProjectChangesRequireConfirmationAndSendNoRequestWithoutIt(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{projectsCreate.ID, `{"name":"X"}`},
		{projectsUpdate.ID, fmt.Sprintf(`{"project_id":%q,"name":"X"}`, ownProject)},
		{projectsDelete.ID, fmt.Sprintf(`{"project_id":%q}`, ownProject)},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, noRequest(t))
			_, err := env.invoke(tt.operation, "pdelete-open", tt.arguments)
			if !isConfirmationRequired(err) {
				t.Fatalf("err = %v, want confirmation-required", err)
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
			}
		})
	}
}

// A project outside the allow-list is refused before any secret is read and any request is sent, and the
// refusal never names it.
func TestProjectUpdateAndDeleteOutsideTheAllowListAreRefusedLocally(t *testing.T) {
	for _, tt := range []struct{ name, operation, connection, arguments string }{
		{"update foreign", projectsUpdate.ID, "pdelete", fmt.Sprintf(`{"project_id":%q,"name":"X"}`, foreignProject)},
		{"delete foreign", projectsDelete.ID, "pdelete", fmt.Sprintf(`{"project_id":%q}`, foreignProject)},
		{"update without allow-list", projectsUpdate.ID, "pdelete-open",
			fmt.Sprintf(`{"project_id":%q,"name":"X"}`, ownProject)},
		{"delete without allow-list", projectsDelete.ID, "pdelete-open", fmt.Sprintf(`{"project_id":%q}`, ownProject)},
		{"update under workflow list", projectsUpdate.ID, "pdelete-workflow",
			fmt.Sprintf(`{"project_id":%q,"name":"X"}`, ownProject)},
		{"delete under workflow list", projectsDelete.ID, "pdelete-workflow",
			fmt.Sprintf(`{"project_id":%q}`, ownProject)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, noRequest(t))
			_, err := env.confirmed(tt.operation, tt.connection, tt.arguments)
			if !isInvalidRequest(err) {
				t.Fatalf("err = %v, want an invalid request", err)
			}
			if len(calls) != 0 || *env.reads != 0 {
				t.Fatalf("calls = %+v, reads = %d, want none", calls, *env.reads)
			}
			if strings.Contains(err.Error(), foreignProject) {
				t.Fatalf("error %q names the foreign project", err)
			}
		})
	}
}

func TestProjectCreateIsRefusedUnderAnAllowList(t *testing.T) {
	for _, connection := range []string{"project", "workflow", "both"} {
		var calls []call
		env := newEnvironment(t, &calls, noRequest(t))
		_, err := env.confirmed(projectsCreate.ID, connection, `{"name":"X"}`)
		if !isInvalidRequest(err) || len(calls) != 0 || *env.reads != 0 {
			t.Fatalf("%s: err = %v, calls = %+v, reads = %d, want a local refusal", connection, err, calls, *env.reads)
		}
	}
}

func TestProjectNamesAreValidated(t *testing.T) {
	var calls []call
	env := newEnvironment(t, &calls, noRequest(t))
	for _, name := range []string{"   ", "a\\u0000b", "a\\nb"} {
		_, err := env.confirmed(projectsCreate.ID, "open", `{"name":"`+name+`"}`)
		if !isInvalidRequest(err) {
			t.Fatalf("name %q: err = %v, want an invalid request", name, err)
		}
	}
	_, err := env.confirmed(projectsCreate.ID, "open", `{"name":"`+strings.Repeat("a", 256)+`"}`)
	if err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want an oversized name refused", err, calls)
	}
	_, err = env.confirmed(projectsCreate.ID, "open", `{"name":"X","id":"abc"}`)
	if err == nil || len(calls) != 0 {
		t.Fatalf("err = %v, calls = %+v, want an extra field refused", err, calls)
	}
}

func TestProjectCreateUpdateDeleteSendOneRequestEach(t *testing.T) {
	var calls []call
	var bodies []string
	env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
		var raw []byte
		if r.Body != nil {
			raw, _ = io.ReadAll(r.Body)
		}
		bodies = append(bodies, string(raw))
		switch {
		case r.Method == http.MethodPost && r.URL.Path == apiPath+"/projects":
			return jsonResponse(201, projectJSONOf(newProject, "Marketing")), nil
		case r.Method == http.MethodPut && r.URL.Path == apiPath+"/projects/"+ownProject:
			return jsonResponse(204, ``), nil
		case r.Method == http.MethodDelete && r.URL.Path == apiPath+"/projects/"+ownProject:
			return jsonResponse(204, ``), nil
		}
		t.Fatalf("unexpected request to %s %s", r.Method, r.URL.Path)
		return nil, nil
	})
	if result, err := env.confirmed(projectsCreate.ID, "pdelete-open", `{"name":"Marketing"}`); err != nil ||
		!strings.Contains(result, newProject) {
		t.Fatalf("create = %s, %v", result, err)
	}
	if result, err := env.confirmed(projectsUpdate.ID, "pdelete", fmt.Sprintf(`{"project_id":%q,"name":"New"}`, ownProject)); err != nil ||
		!strings.Contains(result, `"updated":true`) {
		t.Fatalf("update = %s, %v", result, err)
	}
	if result, err := env.confirmed(projectsDelete.ID, "pdelete", fmt.Sprintf(`{"project_id":%q}`, ownProject)); err != nil ||
		!strings.Contains(result, `"deleted":true`) {
		t.Fatalf("delete = %s, %v", result, err)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %+v, want exactly one request per change", calls)
	}
	if bodies[0] != `{"name":"Marketing"}` || bodies[1] != `{"name":"New"}` || bodies[2] != "" {
		t.Fatalf("bodies = %q, want only the name, and none for delete", bodies)
	}
	if calls[2].query.Encode() != "" {
		t.Fatalf("delete query = %q, want none (no transfer target)", calls[2].query.Encode())
	}
}

func TestProjectDeleteIsOnlyOfferedByAToolsList(t *testing.T) {
	if !projectsDelete.RequiresToolAllowList || projectsDelete.Risk.Effect != "delete" ||
		projectsDelete.Risk.Confirmation != "required" {
		t.Fatalf("descriptor = %+v", projectsDelete)
	}
	metadata, _ := registry(t).ProviderMetadata(Provider)
	for _, profile := range metadata.Profiles {
		for _, id := range profile.Tools {
			if id == projectsDelete.ID {
				t.Fatalf("profile %q selects %s", profile.ID, id)
			}
		}
	}
	var calls []call
	env := newEnvironment(t, &calls, noRequest(t))
	_, err := env.confirmed(projectsDelete.ID, "open", fmt.Sprintf(`{"project_id":%q}`, ownProject))
	if err == nil || len(calls) != 0 || *env.reads != 0 {
		t.Fatalf("err = %v, calls = %+v, reads = %d, want delete unavailable without a tools list", err, calls, *env.reads)
	}
}

func TestProjectProfiles(t *testing.T) {
	metadata, _ := registry(t).ProviderMetadata(Provider)
	got := map[string][]string{}
	for _, profile := range metadata.Profiles {
		got[profile.ID] = profile.Tools
	}
	if strings.Join(got["projects-read"], ",") != projectsList.ID {
		t.Fatalf("projects-read = %v", got["projects-read"])
	}
	if strings.Join(got["projects-manage"], ",") != strings.Join([]string{projectsList.ID, projectsCreate.ID, projectsUpdate.ID}, ",") {
		t.Fatalf("projects-manage = %v", got["projects-manage"])
	}
	for _, id := range []string{"read", "manage"} {
		for _, tool := range got[id] {
			if strings.HasPrefix(tool, "n8n.projects.") {
				t.Fatalf("profile %q contains project tool %s", id, tool)
			}
		}
	}
}

func TestProjectForbiddenIsReportedAsLicenseOrRoleErrorWithoutLeaking(t *testing.T) {
	for _, tt := range []struct{ operation, arguments string }{
		{projectsList.ID, `{}`},
		{projectsCreate.ID, `{"name":"X"}`},
		{projectsUpdate.ID, fmt.Sprintf(`{"project_id":%q,"name":"X"}`, ownProject)},
		{projectsDelete.ID, fmt.Sprintf(`{"project_id":%q}`, ownProject)},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) {
				return jsonResponse(403, `{"message":"`+foreignCanary+`"}`), nil
			})
			_, err := env.confirmed(tt.operation, connectionFor(tt.operation), tt.arguments)
			if classOf(err) != provider.ClassPermission {
				t.Fatalf("class = %q, want permission", classOf(err))
			}
			if !strings.Contains(err.Error(), "license") || !strings.Contains(err.Error(), "role") ||
				strings.Contains(err.Error(), foreignCanary) {
				t.Fatalf("err = %v, want a license and role hint without the provider body", err)
			}
			if len(calls) != 1 {
				t.Fatalf("calls = %+v, want one request", calls)
			}
		})
	}
}

func TestProjectChangesAreNeverRetriedOnAnUnclearOutcome(t *testing.T) {
	for _, tt := range []struct {
		name, operation, arguments string
		respond                    func() (*http.Response, error)
	}{
		{"create 5xx", projectsCreate.ID, `{"name":"X"}`,
			func() (*http.Response, error) { return jsonResponse(500, `{}`), nil }},
		{"create unreadable", projectsCreate.ID, `{"name":"X"}`,
			func() (*http.Response, error) { return jsonResponse(201, `not json`), nil }},
		{"create transport", projectsCreate.ID, `{"name":"X"}`,
			func() (*http.Response, error) { return nil, errUnclearTransport{} }},
		{"update 5xx", projectsUpdate.ID, fmt.Sprintf(`{"project_id":%q,"name":"X"}`, ownProject),
			func() (*http.Response, error) { return jsonResponse(502, `{}`), nil }},
		{"delete transport", projectsDelete.ID, fmt.Sprintf(`{"project_id":%q}`, ownProject),
			func() (*http.Response, error) { return nil, errUnclearTransport{} }},
		{"delete conflict part-way", projectsDelete.ID, fmt.Sprintf(`{"project_id":%q}`, ownProject),
			func() (*http.Response, error) { return jsonResponse(409, `{"message":"`+foreignCanary+`"}`), nil }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls []call
			env := newEnvironment(t, &calls, func(r *http.Request) (*http.Response, error) { return tt.respond() })
			_, err := env.confirmed(tt.operation, connectionFor(tt.operation), tt.arguments)
			if err == nil || !strings.Contains(err.Error(), "may have taken effect") {
				t.Fatalf("err = %v, want uncertain", err)
			}
			if strings.Contains(err.Error(), foreignCanary) {
				t.Fatalf("error leaked the provider body: %v", err)
			}
			if len(calls) != 1 {
				t.Fatalf("calls = %+v, want exactly one attempt", calls)
			}
		})
	}
}
