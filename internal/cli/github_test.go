package cli

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// githubConfig binds one GitHub token to an organization project, to a repository, and to the same project
// with the repository it may plan in and the permission to change both. No GitHub API is contacted here:
// every request in this file is answered locally or refused before provider I/O. The command helpers of
// the Twenty tests are provider-neutral and reused.
func githubConfig(t *testing.T) string {
	t.Helper()
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	return writeConfig(t, `version: 1
services:
  gh:
    provider: github
    base_url: https://api.github.com
credentials:
  gh-reader:
    provider: github
    type: env
    values:
      token: GITHUB_READER_TOKEN
connections:
  planning:
    service: gh
    credential: gh-reader
    target: orgs/octo-org/projects/7
    permissions: [read]
    tools: [github.projectitems.list, github.projectitems.get]
  code:
    service: gh
    credential: gh-reader
    target: repos/octo-org/example
    permissions: [read]
  roadmap:
    service: gh
    credential: gh-reader
    targets: [orgs/octo-org/projects/7, repos/octo-org/example]
    permissions: [read, create, update]
    tools: [github.projectitems.list, github.projectitems.update, github.projectissues.create]
defaults: {}
`)
}

// The GitHub namespace publishes the read tools and the changes; a project connection limited by its tools
// list offers only the project tools, and only a connection whose permissions allow a change offers it.
func TestGitHubToolsAreDiscoverable(t *testing.T) {
	path := githubConfig(t)
	var reads atomic.Int32

	code, stdout, stderr := runTwentyCLI(t, &reads, "", "tools", "github", "--config", path)
	if code != exitOK || stderr != "" {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	for _, want := range []string{
		"tools[15]{effect,id,title}:", "read,github.issues.get,", "read,github.issues.list,",
		"read,github.projectitems.get,", "read,github.projectitems.list,", "read,github.comments.list,",
		"create,github.issues.create,", "update,github.issues.update,", "update,github.issues.close,",
		"update,github.issues.reopen,", "create,github.comments.create,", "update,github.projectitems.update,",
		"create,github.projectitems.add,", "update,github.projectitems.archive,",
		"create,github.projectdrafts.create,", "create,github.projectissues.create,",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("tools output does not contain %q:\n%s", want, stdout)
		}
	}
	if reads.Load() != 0 {
		t.Errorf("secret lookups = %d, want 0", reads.Load())
	}

	document := runTwentyJSON(t, "", "tool", "github.projectitems.list", "--config", path)
	var described struct {
		Tool struct {
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tool"`
		Connections []struct {
			Name string `json:"name"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(document, &described); err != nil {
		t.Fatalf("tool document = %s: %v", document, err)
	}
	for _, want := range []string{"status_not", "cursor", "limit", "labels"} {
		if !strings.Contains(string(described.Tool.InputSchema), want) {
			t.Errorf("input schema lacks %q: %s", want, described.Tool.InputSchema)
		}
	}
	if len(described.Connections) != 3 {
		t.Errorf("connections = %+v, want every connection that allows it", described.Connections)
	}
	issues := runTwentyJSON(t, "", "tool", "github.issues.list", "--config", path)
	if !strings.Contains(string(issues), `"name":"code"`) || strings.Contains(string(issues), `"name":"planning"`) {
		t.Errorf("issue tool connections = %s, want only the repository connection", issues)
	}
	planned := runTwentyJSON(t, "", "tool", "github.projectissues.create", "--config", path)
	if !strings.Contains(string(planned), `"name":"roadmap"`) || strings.Contains(string(planned), `"name":"code"`) ||
		!strings.Contains(string(planned), `"confirmation":"required"`) ||
		!strings.Contains(string(planned), `"idempotency":"non_idempotent"`) {
		t.Errorf("planned issue tool = %s, want only the connection that may change the project", planned)
	}
	if reads.Load() != 0 {
		t.Errorf("secret lookups = %d, want 0", reads.Load())
	}
}

// Requests outside the bound target are refused before a secret is read and before GitHub is contacted.
func TestGitHubInvokeRefusalsHappenBeforeSecretsAndProviderIO(t *testing.T) {
	path := githubConfig(t)
	tests := []struct {
		name, input, code string
		args              []string
	}{
		{"a project tool on a repository", `{}`, "unsupported-capability",
			[]string{"invoke", "github.projectitems.list", "--connection", "code", "--config", path}},
		{"an issue tool outside the tools list", `{}`, "unsupported-capability",
			[]string{"invoke", "github.issues.list", "--connection", "planning", "--config", path}},
		{"an owner argument", `{"owner":"other-org"}`, "invalid-request",
			[]string{"invoke", "github.projectitems.list", "--connection", "planning", "--config", path}},
		{"a free filter", `{"status":["Done\" OR x"]}`, "invalid-request",
			[]string{"invoke", "github.projectitems.list", "--connection", "planning", "--config", path}},
		{"a foreign cursor", `{"cursor":"AAAAAAAAAAAAAAAAY3VyLTM"}`, "invalid-request",
			[]string{"invoke", "github.projectitems.list", "--connection", "planning", "--config", path}},
		{"no explicit connection", `{"number":1}`, "connection-selection",
			[]string{"invoke", "github.issues.get", "--config", path}},
		{"an unconfirmed change", `{"item_id":"PVTI_x1","fields":{"Status":"Done"}}`, "confirmation-required",
			[]string{"invoke", "github.projectitems.update", "--connection", "roadmap", "--config", path}},
		{"a change the permissions exclude", `{"title":"x"}`, "unsupported-capability",
			[]string{"invoke", "github.issues.create", "--connection", "code", "--confirm", "--config", path}},
		{"a change outside the tools list", `{"item_id":"PVTI_x1"}`, "unsupported-capability",
			[]string{"invoke", "github.projectitems.archive", "--connection", "roadmap", "--confirm", "--config", path}},
		{"a repository outside the targets", `{"repository":"octo-org/other","title":"x"}`, "invalid-request",
			[]string{"invoke", "github.projectissues.create", "--connection", "roadmap", "--confirm", "--config", path}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reads atomic.Int32
			code, stdout, stderr := runTwentyCLI(t, &reads, tt.input, tt.args...)
			if code != exitUsage || stdout != "" || !strings.Contains(stderr, "qatlas: "+tt.code+":") {
				t.Fatalf("exit=%d stdout=%q stderr=%q, want %s", code, stdout, stderr, tt.code)
			}
			if reads.Load() != 0 {
				t.Errorf("secret lookups = %d, want none", reads.Load())
			}
		})
	}
}

// The MCP broker finds and describes the same GitHub tools as the CLI.
func TestGitHubMCPAndCLIShareTheCoreContracts(t *testing.T) {
	path := githubConfig(t)
	options := &Options{Config: path, Redactor: &redact.Redactor{}}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"search","method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"qatlas.search","arguments":{"provider":"github"}}}`,
		`{"jsonrpc":"2.0","id":"describe","method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"qatlas.describe","arguments":{"operation":"github.projectitems.list","version":1}}}`,
	}, "\n") + "\n"
	responses, stderr := runMCPWithOptions(t, defaultRegistry(), input, options)
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
	var searched struct {
		Operations []application.SearchHit `json:"operations"`
	}
	decodeRaw(t, toolResultFrom(t, responses[`"search"`]).Structured, &searched)
	if len(searched.Operations) != 15 || searched.Operations[0].ID != "github.comments.create" ||
		searched.Operations[14].ID != "github.projectitems.update" {
		t.Fatalf("search operations = %+v", searched.Operations)
	}
	describedByCLI := runTwentyJSON(t, "", "tool", "github.projectitems.list", "--config", path, "--output", "json")
	describe := toolResultFrom(t, responses[`"describe"`])
	assertMCPParity(t, describedByCLI, "tool", describe.Structured, "operation")
	assertMCPParity(t, describedByCLI, "connections", describe.Structured, "connections")
}
