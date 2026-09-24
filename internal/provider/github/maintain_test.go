package github

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	fileCanary    = "workflow-content-canary-91b4"
	messageCanary = "commit-message-canary-3f0a"
	commitSHA     = "c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00"
)

// guardedTools are every tool that requires an allow-list.
var guardedTools = append(append([]string{}, maintainerTools...), adminTools...)

func blobOf(content string) string {
	sum := sha1.Sum([]byte(content))
	return hex.EncodeToString(sum[:])
}

// fakeMaintain answers the Contents routes below .github/workflows/, the workflow enable and disable
// routes, and the Actions settings of the bound repository, and keeps their state. A change reads its body
// from the request fakeGitHub recorded.
type fakeMaintain struct {
	mu          sync.Mutex
	f           *fakeGitHub
	files       map[string]string
	enabled     bool
	allowed     string
	defaults    string
	canApprove  bool
	policyFixed bool
}

func (m *fakeMaintain) content(name string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	content, ok := m.files[name]
	return content, ok
}

func (m *fakeMaintain) route(w http.ResponseWriter, r *http.Request) bool {
	rest, ok := strings.CutPrefix(r.URL.Path, actionsPrefix)
	if !ok {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var body map[string]any
	if requests := m.f.recorded(); r.Method != http.MethodGet && len(requests) > 0 {
		body = requests[len(requests)-1].body
	}
	w.Header().Set("Content-Type", "application/json")
	name, isFile := strings.CutPrefix(rest, "contents/.github/workflows/")
	switch {
	case r.Method == http.MethodGet && rest == "contents/.github/workflows":
		names := make([]string, 0, len(m.files))
		for name := range m.files {
			names = append(names, name)
		}
		sort.Strings(names)
		entries := []string{`{"type":"dir","name":"sub","path":".github/workflows/sub","sha":"` + blobOf("sub") +
			`","size":0}`, `{"type":"file","name":"README.md","path":".github/workflows/README.md","sha":"` +
			blobOf("readme") + `","size":6}`}
		for _, name := range names {
			entries = append(entries, fmt.Sprintf(`{"type":"file","name":%q,"path":".github/workflows/%s",`+
				`"sha":%q,"size":%d,"download_url":"https://raw.example.invalid/%s"}`, name, name,
				blobOf(m.files[name]), len(m.files[name]), name))
		}
		fmt.Fprint(w, "["+strings.Join(entries, ",")+"]")
	case r.Method == http.MethodGet && isFile:
		content, ok := m.files[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return true
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(content))
		var wrapped []string
		for len(encoded) > 60 {
			wrapped, encoded = append(wrapped, encoded[:60]), encoded[60:]
		}
		wrapped = append(wrapped, encoded)
		fmt.Fprintf(w, `{"type":"file","encoding":"base64","path":".github/workflows/%s","sha":%q,"size":%d,`+
			`"content":%q}`, name, blobOf(content), len(content), strings.Join(wrapped, "\n")+"\n")
	case r.Method == http.MethodPut && isFile:
		sha, _ := body["sha"].(string)
		current, exists := m.files[name]
		switch {
		case !exists && sha != "", exists && sha == "":
			w.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(w, `{"message":"Invalid request. \"sha\" wasn't supplied."}`)
			return true
		case exists && sha != blobOf(current):
			w.WriteHeader(http.StatusConflict)
			fmt.Fprintf(w, `{"message":".github/workflows/%s does not match %s"}`, name, sha)
			return true
		}
		encoded, _ := body["content"].(string)
		data, _ := base64.StdEncoding.DecodeString(encoded)
		m.files[name] = string(data)
		status := http.StatusOK
		if !exists {
			status = http.StatusCreated
		}
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"content":{"name":%q,"path":".github/workflows/%s","sha":%q,"size":%d},`+
			`"commit":{"sha":%q,"html_url":"https://github.com/octo-org/example/commit/%s","message":%q}}`,
			name, name, blobOf(string(data)), len(data), commitSHA, commitSHA, body["message"])
	case r.Method == http.MethodGet && rest == "actions/permissions":
		if !m.enabled {
			fmt.Fprint(w, `{"enabled":false}`)
			return true
		}
		fmt.Fprintf(w, `{"enabled":true,"allowed_actions":%q,"selected_actions_url":"https://api.github.com/x"}`,
			m.allowed)
	case r.Method == http.MethodPut && strings.HasPrefix(rest, "actions/permissions") && m.policyFixed:
		w.WriteHeader(http.StatusConflict)
		fmt.Fprint(w, `{"message":"Conflict"}`)
	case r.Method == http.MethodPut && rest == "actions/permissions":
		m.enabled, _ = body["enabled"].(bool)
		if allowed, ok := body["allowed_actions"].(string); ok {
			m.allowed = allowed
		}
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && rest == "actions/permissions/workflow":
		fmt.Fprintf(w, `{"default_workflow_permissions":%q,"can_approve_pull_request_reviews":%t}`, m.defaults,
			m.canApprove)
	case r.Method == http.MethodPut && rest == "actions/permissions/workflow":
		m.defaults, _ = body["default_workflow_permissions"].(string)
		m.canApprove, _ = body["can_approve_pull_request_reviews"].(bool)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPut && strings.HasPrefix(rest, "actions/workflows/") &&
		(strings.HasSuffix(rest, "/enable") || strings.HasSuffix(rest, "/disable")):
		w.WriteHeader(http.StatusNoContent)
	default:
		return false
	}
	return true
}

// serveMaintain starts the fake GitHub with the maintainer and administrator routes in front of the Actions
// routes.
func serveMaintain(t *testing.T) (*fakeGitHub, *fakeMaintain, string) {
	t.Helper()
	actions := &fakeActions{runs: actionsRuns()}
	f := &fakeGitHub{}
	m := &fakeMaintain{f: f, files: map[string]string{"ci.yml": ciWorkflow + "# " + fileCanary + "\n",
		"release.yml": releaseWorkflow}, enabled: true, allowed: "all", defaults: "write"}
	f.failure = func(w http.ResponseWriter, r *http.Request) bool { return m.route(w, r) || actions.route(w, r) }
	return f, m, serve(t, f)
}

// maintainConfig adds to the Actions connections one without a tools list that holds every permission, one
// that lists the maintainer tools, one that lists the administrator tools, and a project connection that
// lists both.
func maintainConfig(base string) *config.Config {
	cfg := actionsConfig(base)
	repo := func(permissions []config.Permission, tools []string) config.Connection {
		return config.Connection{Service: "gh", Credential: "gh-reader", Target: repoTarget, Permissions: permissions,
			Tools: tools}
	}
	cfg.Connections["open"] = repo(config.Permissions(), nil)
	cfg.Connections["maintainer"] = repo([]config.Permission{config.PermissionRead, config.PermissionCreate,
		config.PermissionUpdate}, append([]string{workflowsList.ID, workflowsGet.ID}, maintainerTools...))
	cfg.Connections["admin"] = repo([]config.Permission{config.PermissionRead, config.PermissionUpdate}, adminTools)
	cfg.Connections["project"] = config.Connection{Service: "gh", Credential: "gh-reader", Target: projectTarget,
		Permissions: config.Permissions(), Tools: guardedTools}
	return cfg
}

// validArguments are arguments each guarded tool accepts.
func validArguments(id string) string {
	return map[string]string{
		workflowFilesList.ID:         `{}`,
		workflowFilesGet.ID:          `{"path":".github/workflows/ci.yml"}`,
		workflowFilesCreate.ID:       `{"path":".github/workflows/lint.yml","content":"on: push\n","message":"add lint"}`,
		workflowFilesUpdate.ID:       `{"path":".github/workflows/ci.yml","sha":"` + blobOf("x") + `","content":"on: push\n","message":"m"}`,
		workflowsEnable.ID:           `{"workflow":"old.yml"}`,
		workflowsDisable.ID:          `{"workflow":"ci.yml"}`,
		actionsPermissionsGet.ID:     `{}`,
		actionsPermissionsUpdate.ID:  `{"allowed_actions":"local_only"}`,
		workflowPermissionsGet.ID:    `{}`,
		workflowPermissionsUpdate.ID: `{"default_workflow_permissions":"read"}`,
	}[id]
}

// Exactly the maintainer and administrator tools and the project delete require an allow-list, no profile a
// new connection starts with selects them, their own profiles are chosen on purpose only, and no profile
// selects the project delete.
func TestGuardedToolsStayOutOfEveryStandardProfile(t *testing.T) {
	reg := registry(t)
	if err := reg.ValidateProfiles(); err != nil {
		t.Fatal(err)
	}
	guarded := map[string]bool{}
	for _, id := range guardedTools {
		guarded[id] = true
	}
	for _, descriptor := range reg.Provider(Provider) {
		if descriptor.RequiresToolAllowList != (guarded[descriptor.ID] || descriptor.ID == projectsDelete.ID) {
			t.Errorf("%s requires an allow-list = %t", descriptor.ID, descriptor.RequiresToolAllowList)
		}
	}
	metadata, _ := reg.ProviderMetadata(Provider)
	marked := 0
	for _, tool := range metadata.Tools {
		if tool.RequiresToolAllowList {
			marked++
		}
	}
	if marked != len(guardedTools)+1 || len(guardedTools) != 10 {
		t.Errorf("marked tools = %d, want the ten maintainer and administrator tools and the project delete", marked)
	}
	profiles := map[string]config.ToolProfile{}
	for _, profile := range metadata.Profiles {
		profiles[profile.ID] = profile
		for _, id := range profile.Tools {
			standard := profile.Recommended || profile.ID == "read" || profile.ID == "planning" ||
				profile.ID == "actions-observer" || profile.ID == "actions-operator"
			if standard && guarded[id] {
				t.Errorf("standard profile %s selects %s", profile.ID, id)
			}
			if id == projectsDelete.ID {
				t.Errorf("profile %s selects %s", profile.ID, id)
			}
		}
	}
	maintainer, admin := profiles["workflow-maintainer"], profiles["actions-admin"]
	if maintainer.Recommended || admin.Recommended {
		t.Error("a high-risk profile is recommended")
	}
	if want := append([]string{workflowsList.ID, workflowsGet.ID}, maintainerTools...); !reflect.DeepEqual(maintainer.Tools, want) {
		t.Errorf("maintainer profile = %v, want %v", maintainer.Tools, want)
	}
	if !reflect.DeepEqual(admin.Tools, adminTools) {
		t.Errorf("admin profile = %v, want %v", admin.Tools, adminTools)
	}
	if got := metadata.ProfilePermissions(maintainer); !reflect.DeepEqual(got, []config.Permission{
		config.PermissionRead, config.PermissionCreate, config.PermissionUpdate}) {
		t.Errorf("maintainer permissions = %v", got)
	}
	if got := metadata.ProfilePermissions(admin); !reflect.DeepEqual(got, []config.Permission{
		config.PermissionRead, config.PermissionUpdate}) {
		t.Errorf("admin permissions = %v", got)
	}
}

// Connections without a tools list, whatever their permissions, and connections whose list does not name a
// guarded tool neither discover nor run it; the refusal ends before a secret is read and before GitHub is
// contacted. A connection that lists the tools offers exactly those.
func TestGuardedToolsNeedTheirNameInTheToolsList(t *testing.T) {
	f, _, base := serveMaintain(t)
	reads := 0
	red := &redact.Redactor{}
	core := application.New(registry(t), maintainConfig(base), resolver(red, &reads), red)
	guarded := map[string]bool{}
	for _, id := range guardedTools {
		guarded[id] = true
	}

	for _, connection := range []string{"reader", "planner", "observer", "listed", "operator", "open"} {
		searched, err := core.Search(application.SearchRequest{Provider: Provider, Connection: connection})
		if err != nil {
			t.Fatal(err)
		}
		tools, err := core.Tools(application.SearchRequest{Provider: Provider, Connection: connection})
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range searched.Operations {
			if guarded[hit.ID] {
				t.Errorf("%s discovers %s through search", connection, hit.ID)
			}
		}
		for _, tool := range tools.Tools {
			if guarded[tool.ID] {
				t.Errorf("%s discovers %s through the tool index", connection, tool.ID)
			}
		}
		for _, id := range guardedTools {
			var unsupported *capability.UnsupportedError
			if _, err := core.Describe(application.DescribeRequest{Operation: id, Connection: connection}); !errors.As(err, &unsupported) {
				t.Errorf("describe %s on %s = %v, want unsupported", id, connection, err)
			}
			reads = 0
			before := len(f.recorded())
			if _, err := invoke(t, core, id, connection, validArguments(id), true); !errors.As(err, &unsupported) {
				t.Errorf("invoke %s on %s = %T %v, want unsupported", id, connection, err, err)
			}
			if reads != 0 || len(f.recorded()) != before {
				t.Errorf("%s on %s: secret reads = %d, requests = %d; want none", id, connection, reads,
					len(f.recorded())-before)
			}
		}
	}

	for _, id := range guardedTools {
		described, err := core.Describe(application.DescribeRequest{Operation: id})
		if err != nil {
			t.Fatal(err)
		}
		for _, route := range described.Connections {
			if route.Name != "maintainer" && route.Name != "admin" && route.Name != "project" {
				t.Errorf("%s is routed over %s", id, route.Name)
			}
		}
	}
	for connection, want := range map[string]int{"maintainer": 8, "admin": 4} {
		searched, _ := core.Search(application.SearchRequest{Provider: Provider, Connection: connection})
		if len(searched.Operations) != want {
			t.Errorf("%s discovers %d tools, want %d", connection, len(searched.Operations), want)
		}
	}
	// Each group stays with its own list, and a connection whose targets name no repository runs neither.
	for _, tt := range []struct {
		id, connection string
		unsupported    bool
	}{
		{actionsPermissionsUpdate.ID, "maintainer", true}, {workflowFilesUpdate.ID, "admin", true},
		{workflowFilesGet.ID, "project", false}, {workflowPermissionsGet.ID, "project", false},
	} {
		reads = 0
		before := len(f.recorded())
		var unsupported *capability.UnsupportedError
		_, err := invoke(t, core, tt.id, tt.connection, validArguments(tt.id), true)
		if tt.unsupported && !errors.As(err, &unsupported) || !tt.unsupported && !isInvalidRequest(err) {
			t.Errorf("%s on %s = %T %v, want a refusal", tt.id, tt.connection, err, err)
		}
		if reads != 0 || len(f.recorded()) != before {
			t.Errorf("%s on %s reached the credential or GitHub", tt.id, tt.connection)
		}
	}
}

// pathCanaries try to leave .github/workflows/ or to name something other than one workflow file there.
var pathCanaries = []string{
	"../ci.yml", ".github/workflows/../../ci.yml", ".github/workflows/../ci.yml", ".github/workflows/%2e%2e/ci.yml",
	".github/workflows/..%2fci.yml", ".github/workflows/ci%2eyml", "/.github/workflows/ci.yml",
	".github/workflows//ci.yml", ".github/workflows/sub/ci.yml", ".github/workflows/ci.txt",
	".github/workflows/ci.yml.bak", ".github/workflows/ci.yml ", " .github/workflows/ci.yml",
	".github/workflows/ｃi.yml", ".github/workflows/ci∕x.yml", ".github/workflows/ci.y​ml",
	".github/workflows/.hidden.yml", ".github/workflows/..yml", ".github/workflows/a..yml",
	".GitHub/workflows/ci.yml", ".github/workflows\\ci.yml", ".github/workflows/ci.yml\x00",
	".github/actions/setup/action.yml", "README.md", ".github/workflows/", ".github/workflows/.yml",
	"./.github/workflows/ci.yml", ".github/workflows/ci.yml/", ".github/workflows/" + strings.Repeat("a", 101) + ".yml",
}

// A path outside .github/workflows/, or not one .yml or .yaml file directly in it, is refused before a
// secret is resolved and before GitHub is contacted, by the core and by the tool itself.
func TestWorkflowFilePathsStayInsideTheWorkflowDirectory(t *testing.T) {
	for _, valid := range []string{".github/workflows/ci.yml", ".github/workflows/Release_2.yaml",
		".github/workflows/a.b-c.yml", ".github/workflows/" + strings.Repeat("a", 100) + ".yaml"} {
		if !validWorkflowFile(valid) {
			t.Errorf("validWorkflowFile(%q) = false", valid)
		}
	}
	f, _, base := serveMaintain(t)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	core := application.New(reg, maintainConfig(base), resolver(red, &reads), red)
	resolved := resolvedConnection("maintainer", base, repoTarget)
	for _, path := range pathCanaries {
		if validWorkflowFile(path) {
			t.Errorf("validWorkflowFile(%q) = true", path)
		}
		for _, id := range []string{workflowFilesGet.ID, workflowFilesCreate.ID, workflowFilesUpdate.ID} {
			arguments := map[string]any{"path": path}
			if id != workflowFilesGet.ID {
				arguments["content"], arguments["message"] = "on: push\n", "m"
			}
			if id == workflowFilesUpdate.ID {
				arguments["sha"] = blobOf("x")
			}
			raw, _ := json.Marshal(arguments)
			reads = 0
			before := len(f.recorded())
			if _, err := invoke(t, core, id, "maintainer", string(raw), true); !isInvalidRequest(err) {
				t.Errorf("%s %q through the core = %v, want an invalid request", id, path, err)
			}
			_, handler, _ := reg.Lookup(id)
			if _, err := handler(context.Background(), resolved, resolver(red, &reads), red, raw); !isInvalidRequest(err) {
				t.Errorf("%s %q through the tool = %v, want an invalid request", id, path, err)
			}
			if reads != 0 || len(f.recorded()) != before {
				t.Errorf("%s %q: secret reads = %d, requests = %d; want none", id, path, reads,
					len(f.recorded())-before)
			}
		}
	}
}

// The other bounds of a file change, and a missing confirmation, end before I/O as well.
func TestWorkflowFileChangesAreBoundedBeforeIO(t *testing.T) {
	f, _, base := serveMaintain(t)
	reads := 0
	red := &redact.Redactor{}
	reg := registry(t)
	core := application.New(reg, maintainConfig(base), resolver(red, &reads), red)
	sha := blobOf("x")
	large := strings.Repeat("a", maxWorkflowFile+1)
	tests := []struct {
		name, id, arguments string
		confirmed           bool
		want                any
	}{
		{"unconfirmed create", workflowFilesCreate.ID, validArguments(workflowFilesCreate.ID), false,
			&application.ConfirmationRequiredError{}},
		{"unconfirmed update", workflowFilesUpdate.ID, validArguments(workflowFilesUpdate.ID), false,
			&application.ConfirmationRequiredError{}},
		{"unconfirmed enable", workflowsEnable.ID, validArguments(workflowsEnable.ID), false,
			&application.ConfirmationRequiredError{}},
		{"unconfirmed settings", actionsPermissionsUpdate.ID, validArguments(actionsPermissionsUpdate.ID), false,
			&application.ConfirmationRequiredError{}},
		{"unconfirmed token defaults", workflowPermissionsUpdate.ID, validArguments(workflowPermissionsUpdate.ID),
			false, &application.ConfirmationRequiredError{}},
		{"an update without sha", workflowFilesUpdate.ID,
			`{"path":".github/workflows/ci.yml","content":"x","message":"m"}`, true, &application.InvalidRequestError{}},
		{"a short sha", workflowFilesUpdate.ID,
			`{"path":".github/workflows/ci.yml","sha":"abc123","content":"x","message":"m"}`, true,
			&application.InvalidRequestError{}},
		{"an uppercase sha", workflowFilesUpdate.ID, `{"path":".github/workflows/ci.yml","sha":"` +
			strings.ToUpper(sha) + `","content":"x","message":"m"}`, true, &application.InvalidRequestError{}},
		{"a sha on create", workflowFilesCreate.ID, `{"path":".github/workflows/x.yml","sha":"` + sha +
			`","content":"x","message":"m"}`, true, &application.InvalidRequestError{}},
		{"too large a file", workflowFilesCreate.ID, `{"path":".github/workflows/x.yml","content":"` + large +
			`","message":"m"}`, true, &application.InvalidRequestError{}},
		{"binary content", workflowFilesCreate.ID,
			`{"path":".github/workflows/x.yml","content":"on: push\u0000","message":"m"}`, true,
			&application.InvalidRequestError{}},
		{"an empty file", workflowFilesCreate.ID, `{"path":".github/workflows/x.yml","content":"","message":"m"}`,
			true, &application.InvalidRequestError{}},
		{"a blank message", workflowFilesCreate.ID, `{"path":".github/workflows/x.yml","content":"x","message":"  "}`,
			true, &application.InvalidRequestError{}},
		{"too long a message", workflowFilesCreate.ID, `{"path":".github/workflows/x.yml","content":"x","message":"` +
			strings.Repeat("m", maxCommitMessage+1) + `"}`, true, &application.InvalidRequestError{}},
		{"a message with an escape", workflowFilesCreate.ID,
			`{"path":".github/workflows/x.yml","content":"x","message":"m\u001b[31m"}`, true,
			&application.InvalidRequestError{}},
		{"a branch with two dots", workflowFilesCreate.ID,
			`{"path":".github/workflows/x.yml","content":"x","message":"m","branch":"a..b"}`, true,
			&application.InvalidRequestError{}},
		{"an owner argument", workflowFilesGet.ID, `{"path":".github/workflows/ci.yml","owner":"other"}`, false,
			&application.InvalidRequestError{}},
		{"a delete as an update", workflowFilesUpdate.ID, `{"path":".github/workflows/ci.yml","sha":"` + sha +
			`","message":"m","content":"x","delete":true}`, true, &application.InvalidRequestError{}},
		{"a dynamic path as workflow", workflowsDisable.ID, `{"workflow":"dynamic/codeql"}`, true,
			&application.InvalidRequestError{}},
		{"no setting", actionsPermissionsUpdate.ID, `{}`, true, &application.InvalidRequestError{}},
		{"allowed actions while switching off", actionsPermissionsUpdate.ID,
			`{"enabled":false,"allowed_actions":"all"}`, true, &application.InvalidRequestError{}},
		{"an unknown allowed value", actionsPermissionsUpdate.ID, `{"allowed_actions":"everything"}`, true,
			&application.InvalidRequestError{}},
		{"no token default", workflowPermissionsUpdate.ID, `{}`, true, &application.InvalidRequestError{}},
		{"an unknown token default", workflowPermissionsUpdate.ID, `{"default_workflow_permissions":"admin"}`, true,
			&application.InvalidRequestError{}},
		{"selected actions", actionsPermissionsUpdate.ID, `{"patterns_allowed":["x/*"]}`, true,
			&application.InvalidRequestError{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connection := "maintainer"
			if strings.Contains(tt.id, "permissions") {
				connection = "admin"
			}
			reads = 0
			before := len(f.recorded())
			_, err := invoke(t, core, tt.id, connection, tt.arguments, tt.confirmed)
			if want := fmt.Sprintf("%T", tt.want); fmt.Sprintf("%T", err) != want {
				t.Errorf("err = %T %v, want %s", err, err, want)
			}
			// The core applies the schema; the tool applies every bound again, except that it ignores an
			// argument the schema does not know.
			if tt.confirmed && tt.name != "a delete as an update" {
				_, handler, _ := reg.Lookup(tt.id)
				if _, err := handler(context.Background(), resolvedConnection(connection, base, repoTarget),
					resolver(red, &reads), red, json.RawMessage(tt.arguments)); err == nil {
					t.Error("the tool itself accepted the arguments")
				}
			}
			if reads != 0 || len(f.recorded()) != before {
				t.Errorf("secret reads = %d, requests = %d; want none", reads, len(f.recorded())-before)
			}
		})
	}
}

// Workflow files are listed and read with their blob SHA; a change is one request that names the blob it
// replaces, answers with metadata only, and never overwrites a file that changed or already exists.
func TestWorkflowFilesAreWrittenOnlyOverTheBlobTheyReplace(t *testing.T) {
	f, m, base := serveMaintain(t)
	c := client(t, base, repoTarget)
	ctx := context.Background()

	original, _ := m.content("ci.yml")
	listed, err := c.listWorkflowFiles(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Files) != 2 || listed.Files[0].Path != ".github/workflows/ci.yml" ||
		listed.Files[1].Name != "release.yml" || listed.Files[0].SHA != blobOf(original) {
		t.Errorf("files = %+v, want the two workflow files without the directory and the README", listed.Files)
	}
	if request := f.recorded()[0]; request.path != actionsPrefix+"contents/.github/workflows" ||
		request.query != "ref=main" || request.method != http.MethodGet {
		t.Errorf("list request = %+v", request)
	}
	file, err := c.workflowFile(ctx, ".github/workflows/ci.yml", "")
	if err != nil || file.Content != original || file.SHA != blobOf(original) || file.Size != len(original) {
		t.Fatalf("workflowFile() = %+v, %v", file, err)
	}

	puts := func() int {
		_, _, rest := split(f.recorded())
		return rest[http.MethodPut]
	}
	updated := "name: CI\non: [push, pull_request]\njobs: {}\n# " + fileCanary + " v2\n"
	sent := len(f.recorded())
	change, err := c.writeWorkflowFile(ctx, &actionsArguments{Path: file.Path, SHA: file.SHA, Content: updated,
		Message: messageCanary, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.recorded()) != sent+1 {
		t.Errorf("an update sent %d requests, want exactly one", len(f.recorded())-sent)
	}
	if change.PreviousSHA != file.SHA || change.SHA != blobOf(updated) || change.Size != len(updated) ||
		change.CommitSHA != commitSHA || change.Branch != "main" || change.Path != file.Path {
		t.Errorf("change = %+v", change)
	}
	if encoded, _ := json.Marshal(change); strings.Contains(string(encoded), fileCanary) ||
		strings.Contains(string(encoded), messageCanary) {
		t.Errorf("the answer echoes the content or the message: %s", encoded)
	}
	requests := f.recorded()
	put := requests[len(requests)-1]
	written, _ := base64.StdEncoding.DecodeString(fmt.Sprint(put.body["content"]))
	if puts() != 1 || put.path != actionsPrefix+"contents/.github/workflows/ci.yml" || put.body["sha"] != file.SHA ||
		put.body["branch"] != "main" || put.body["message"] != messageCanary || string(written) != updated {
		t.Errorf("update request = %+v", put)
	}

	// The same update again names a blob the file no longer has: nothing is written, and the refusal is clear.
	_, err = c.writeWorkflowFile(ctx, &actionsArguments{Path: file.Path, SHA: file.SHA, Content: "on: push\n",
		Message: "m"})
	if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "no longer has the given blob SHA") ||
		strings.Contains(err.Error(), "may have been applied") || puts() != 2 {
		t.Errorf("a stale update = %v after %d PUT requests, want one clear conflict", err, puts())
	}
	if current, _ := m.content("ci.yml"); current != updated {
		t.Error("a stale update overwrote the file")
	}
	// A create never replaces an existing file.
	_, err = c.writeWorkflowFile(ctx, &actionsArguments{Path: file.Path, Content: "on: push\n", Message: "m"})
	if classOf(err) != provider.ClassProviderError || !strings.Contains(err.Error(), "may already exist") || puts() != 3 {
		t.Errorf("a create over an existing file = %v, want a clear refusal", err)
	}
	if current, _ := m.content("ci.yml"); current != updated {
		t.Error("a create overwrote the file")
	}
	created, err := c.writeWorkflowFile(ctx, &actionsArguments{Path: ".github/workflows/lint.yaml",
		Content: "on: push\n", Message: "m"})
	if err != nil || created.PreviousSHA != "" || created.SHA != blobOf("on: push\n") || puts() != 4 {
		t.Errorf("create = %+v, %v", created, err)
	}
	if _, ok := f.recorded()[len(f.recorded())-1].body["sha"]; ok {
		t.Error("a create sent a sha")
	}

	// A read refuses what is no text workflow file of at most 512 KiB.
	m.mu.Lock()
	m.files["bin.yml"] = "on: push\x00\x01"
	m.files["big.yml"] = strings.Repeat("a", maxWorkflowFile+1)
	m.mu.Unlock()
	for path, detail := range map[string]string{
		".github/workflows/bin.yml": "not text", ".github/workflows/big.yml": "at most 512 KiB",
		".github/workflows/none.yml": "does not hold",
	} {
		if _, err := c.workflowFile(ctx, path, ""); err == nil || !strings.Contains(err.Error(), detail) {
			t.Errorf("workflowFile(%s) = %v, want %q", path, err, detail)
		}
	}
}

// Enabling or disabling a workflow and changing a setting report the state before and after, send the
// complete setting GitHub replaces in one request, and a workflow without a file is refused before it.
func TestWorkflowStatesAndSettingsReportBeforeAndAfter(t *testing.T) {
	f, m, base := serveMaintain(t)
	c := client(t, base, repoTarget)
	ctx := context.Background()
	last := func() recorded { requests := f.recorded(); return requests[len(requests)-1] }

	state, err := c.setWorkflowState(ctx, "old.yml", true)
	if err != nil || state.PreviousState != "disabled_manually" || state.State != "active" || state.WorkflowID != 4 ||
		last().method != http.MethodPut || last().path != actionsPrefix+"actions/workflows/4/enable" {
		t.Errorf("enable = %+v, %v, last request %+v", state, err, last())
	}
	state, err = c.setWorkflowState(ctx, "1", false)
	if err != nil || state.PreviousState != "active" || state.State != "disabled_manually" ||
		last().path != actionsPrefix+"actions/workflows/1/disable" {
		t.Errorf("disable = %+v, %v", state, err)
	}
	before := len(f.recorded())
	if _, err := c.setWorkflowState(ctx, "3", false); err == nil || !strings.Contains(err.Error(), "no workflow file") {
		t.Errorf("disable of a dynamic workflow = %v", err)
	}
	if _, _, rest := split(f.recorded()[before:]); rest[http.MethodPut] != 0 {
		t.Error("a dynamic workflow was changed")
	}

	actions, err := c.updateActionsPermissions(ctx, &actionsArguments{AllowedActions: "local_only"})
	if err != nil || actions.Before != (ActionsPermissions{Enabled: true, AllowedActions: "all"}) ||
		actions.After != (ActionsPermissions{Enabled: true, AllowedActions: "local_only"}) ||
		!reflect.DeepEqual(last().body, map[string]any{"enabled": true, "allowed_actions": "local_only"}) {
		t.Errorf("actions permissions = %+v, %v, body %v", actions, err, last().body)
	}
	off := false
	actions, err = c.updateActionsPermissions(ctx, &actionsArguments{Enabled: &off})
	if err != nil || actions.After != (ActionsPermissions{}) || !reflect.DeepEqual(last().body, map[string]any{"enabled": false}) {
		t.Errorf("switching off = %+v, %v, body %v", actions, err, last().body)
	}
	if read, err := c.actionsPermissions(ctx, "get"); err != nil || read.Enabled {
		t.Errorf("read after switching off = %+v, %v", read, err)
	}
	// Allowed actions of disabled Actions would be dropped by GitHub, so they are refused before the change.
	_, _, rest := split(f.recorded())
	putsBefore := rest[http.MethodPut]
	if _, err := c.updateActionsPermissions(ctx, &actionsArguments{AllowedActions: "all"}); !isInvalidRequest(err) {
		t.Errorf("allowed actions while disabled = %v, want an invalid request", err)
	}
	if _, _, rest = split(f.recorded()); rest[http.MethodPut] != putsBefore {
		t.Error("allowed actions while disabled were sent")
	}

	approve := true
	token, err := c.updateWorkflowPermissions(ctx, &actionsArguments{CanApprove: &approve})
	if err != nil || token.Before != (WorkflowPermissions{DefaultWorkflowPermissions: "write"}) ||
		token.After != (WorkflowPermissions{DefaultWorkflowPermissions: "write", CanApprove: true}) ||
		!reflect.DeepEqual(last().body, map[string]any{"default_workflow_permissions": "write",
			"can_approve_pull_request_reviews": true}) {
		t.Errorf("token defaults = %+v, %v, body %v", token, err, last().body)
	}

	// A setting an organization fixes is refused clearly and once.
	m.mu.Lock()
	m.policyFixed = true
	m.mu.Unlock()
	_, _, rest = split(f.recorded())
	putsBefore = rest[http.MethodPut]
	_, err = c.updateWorkflowPermissions(ctx, &actionsArguments{DefaultWorkflowPermissions: "read"})
	if _, _, rest = split(f.recorded()); err == nil || !strings.Contains(err.Error(), "organization or enterprise policy") ||
		strings.Contains(err.Error(), "may have been applied") || rest[http.MethodPut] != putsBefore+1 {
		t.Errorf("a fixed setting = %v", err)
	}
}

// A change whose outcome is unclear is reported as such and never repeated; a refusal names what the
// request needs without claiming what the token holds.
func TestUnclearMaintenanceChangesAreNeverRepeated(t *testing.T) {
	for name, answer := range map[string]func(http.ResponseWriter){
		"server error":    func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) },
		"gateway timeout": func(w http.ResponseWriter) { w.WriteHeader(http.StatusGatewayTimeout) },
		"dropped connection": func(w http.ResponseWriter) {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				connection.Close()
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f, m, base := serveMaintain(t)
			route := f.failure
			f.failure = func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method == http.MethodPut {
					answer(w)
					return true
				}
				return route(w, r)
			}
			c := client(t, base, repoTarget)
			ctx := context.Background()
			ci, _ := m.content("ci.yml")
			on := true
			calls := []func() error{
				func() error {
					_, err := c.writeWorkflowFile(ctx, &actionsArguments{Path: ".github/workflows/new.yml",
						Content: "on: push\n", Message: "m"})
					return err
				},
				func() error {
					_, err := c.writeWorkflowFile(ctx, &actionsArguments{Path: ".github/workflows/ci.yml",
						SHA: blobOf(ci), Content: "on: push\n", Message: "m"})
					return err
				},
				func() error { _, err := c.setWorkflowState(ctx, "old.yml", true); return err },
				func() error {
					_, err := c.updateActionsPermissions(ctx, &actionsArguments{Enabled: &on})
					return err
				},
				func() error {
					_, err := c.updateWorkflowPermissions(ctx, &actionsArguments{DefaultWorkflowPermissions: "read"})
					return err
				},
			}
			for i, call := range calls {
				err := call()
				if err == nil || !strings.Contains(err.Error(), "may have been applied") {
					t.Errorf("call %d = %v, want an unclear outcome", i, err)
				}
				if _, _, rest := split(f.recorded()); rest[http.MethodPut] != i+1 {
					t.Errorf("call %d: PUT requests = %d, want one per call", i, rest[http.MethodPut])
				}
			}
		})
	}

	f, _, base := serveMaintain(t)
	route := f.failure
	f.failure = func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPut || strings.Contains(r.URL.Path, "/contents/") ||
			strings.HasSuffix(r.URL.Path, "/actions/permissions") {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"Resource not accessible by personal access token"}`)
			return true
		}
		return route(w, r)
	}
	c := client(t, base, repoTarget)
	ctx := context.Background()
	for name, tt := range map[string]struct {
		call func() error
		want string
	}{
		"file write": {func() error {
			_, err := c.writeWorkflowFile(ctx, &actionsArguments{Path: ".github/workflows/new.yml", Content: "x", Message: "m"})
			return err
		}, "Workflows: read and write"},
		"file read": {func() error {
			_, err := c.workflowFile(ctx, ".github/workflows/ci.yml", "")
			return err
		}, "Contents: read on a fine-grained"},
		"workflow state": {func() error { _, err := c.setWorkflowState(ctx, "ci.yml", false); return err },
			"Actions: read and write"},
		"settings read": {func() error { _, err := c.actionsPermissions(ctx, "get"); return err },
			"Administration: read on a fine-grained"},
		"settings change": {func() error {
			_, err := c.updateWorkflowPermissions(ctx, &actionsArguments{DefaultWorkflowPermissions: "read"})
			return err
		}, "Administration: read and write"},
	} {
		err := tt.call()
		if classOf(err) != provider.ClassPermission || !strings.Contains(err.Error(), tt.want) ||
			strings.Contains(err.Error(), "may have been applied") || strings.Contains(err.Error(), "personal access token") {
			t.Errorf("%s = %v, want a clear permission refusal naming %q", name, err, tt.want)
		}
	}
}

// Every guarded tool satisfies its output contract through the application core over a connection that lists
// it; confirmed changes write one audit event each without file content or commit message, and no change
// answer echoes the content.
func TestGuardedToolsSatisfyTheirContractThroughTheApplicationCore(t *testing.T) {
	_, m, base := serveMaintain(t)
	red := &redact.Redactor{}
	var audit strings.Builder
	core := application.New(registry(t), maintainConfig(base), resolver(red, nil), red)
	core.SetAudit(&audit)
	ci, _ := m.content("ci.yml")
	content, _ := json.Marshal("on: push\n# " + fileCanary + "\n")

	for _, request := range []struct {
		operation, connection, arguments string
		confirmed, echoes                bool
	}{
		{workflowFilesList.ID, "maintainer", `{}`, false, false},
		{workflowFilesGet.ID, "maintainer", `{"path":".github/workflows/ci.yml","ref":"main"}`, false, true},
		{workflowFilesCreate.ID, "maintainer", `{"path":".github/workflows/lint.yml","content":` + string(content) +
			`,"message":"` + messageCanary + `"}`, true, false},
		{workflowFilesUpdate.ID, "maintainer", `{"path":".github/workflows/ci.yml","sha":"` + blobOf(ci) +
			`","content":` + string(content) + `,"message":"` + messageCanary + `","branch":"main"}`, true, false},
		{workflowsDisable.ID, "maintainer", `{"workflow":"ci.yml"}`, true, false},
		{workflowsEnable.ID, "maintainer", `{"workflow":"4"}`, true, false},
		{actionsPermissionsGet.ID, "admin", `{}`, false, false},
		{actionsPermissionsUpdate.ID, "admin", `{"enabled":true,"allowed_actions":"selected"}`, true, false},
		{workflowPermissionsGet.ID, "admin", `{}`, false, false},
		{workflowPermissionsUpdate.ID, "admin", `{"default_workflow_permissions":"read",` +
			`"can_approve_pull_request_reviews":false}`, true, false},
	} {
		result, err := invoke(t, core, request.operation, request.connection, request.arguments, request.confirmed)
		if err != nil {
			t.Errorf("%s = %v", request.operation, err)
			continue
		}
		if strings.Contains(string(result), tokenValue) || strings.Contains(string(result), messageCanary) ||
			strings.Contains(string(result), fileCanary) != request.echoes {
			t.Errorf("%s = %s", request.operation, result)
		}
	}
	events := audit.String()
	if strings.Count(events, `"result":"success"`) != 6 || strings.Contains(events, fileCanary) ||
		strings.Contains(events, messageCanary) || strings.Contains(events, "on: push") {
		t.Errorf("audit = %s, want one content-free event per change", events)
	}
}
