package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// twentyConfig configures two Twenty workspaces, a managed one and a self-hosted one, with separate API
// keys, next to a BookStack connection. No workspace is ever contacted here: every request in this file is
// refused by the core before any provider I/O, so the tests need no server and reach no CRM.
func twentyConfig(t *testing.T) string {
	t.Helper()
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	return writeConfig(t, `version: 1
services:
  crm-cloud:
    provider: twenty-crm
    base_url: https://api.twenty.com
  crm-selfhosted:
    provider: twenty-crm
    base_url: https://crm.example.invalid
  wiki:
    provider: bookstack
    base_url: https://wiki.example.invalid
credentials:
  crm-cloud-reader:
    type: env
    values:
      api-key: TWENTY_CLOUD_KEY
  crm-selfhosted-reader:
    type: env
    values:
      api-key: TWENTY_SELFHOSTED_KEY
  reader:
    type: env
    values:
      token-id: WIKI_TOKEN_ID
      token-secret: WIKI_TOKEN_SECRET
connections:
  crm:
    service: crm-cloud
    credential: crm-cloud-reader
  crm-internal:
    service: crm-selfhosted
    credential: crm-selfhosted-reader
  wiki:
    service: wiki
    credential: reader
defaults: {}
`)
}

// runTwentyCLI drives the real command tree and the shipped registry with a resolver that counts secret
// lookups, so a run can prove it resolved no API key.
func runTwentyCLI(t *testing.T, reads *atomic.Int32, input string, args ...string) (int, string, string) {
	t.Helper()
	redactor := &redact.Redactor{}
	lookup := func(string) string {
		if reads != nil {
			reads.Add(1)
		}
		return ""
	}
	var stdout, stderr bytes.Buffer
	options := &Options{
		Input: strings.NewReader(input), Redactor: redactor,
		Secrets: secret.NewWith(lookup, nil, nil, redactor),
	}
	code := run(newRootCommand(options, defaultRegistry()), options, args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// The Twenty namespace publishes every tool without contacting a workspace; above 50 tools a query lists
// them instead of their groups.
func TestTwentyToolsAreDiscoverable(t *testing.T) {
	path := twentyConfig(t)
	var reads atomic.Int32

	code, stdout, stderr := runTwentyCLI(t, &reads, "", "tools", "twenty-crm", "--all", "--query", "twentycrm", "--config", path)
	if code != exitOK || stderr != "" {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	for _, want := range []string{
		"tools[89]{id,title,effect,requires,confirm,reason}:", "twenty-crm.activity-targets.create,Link a Twenty CRM note or task to a record,create,", "twenty-crm.activity-targets.delete,Remove a Twenty CRM note or task link,delete,", "twenty-crm.activity-targets.list,List Twenty CRM note and task links,read,", "twenty-crm.agents.get,Get a Twenty CRM AI agent,read,", "twenty-crm.agents.list,List Twenty CRM AI agents,read,", "twenty-crm.attachments.get,Download a Twenty CRM attachment,read,", "twenty-crm.attachments.list,List Twenty CRM attachments of a record,read,", "twenty-crm.attachments.upload,Upload a file as a Twenty CRM attachment,create,", "twenty-crm.companies.create,Create a Twenty CRM company,create,", "twenty-crm.companies.delete,Delete a Twenty CRM company,delete,", "twenty-crm.companies.destroy,Destroy a Twenty CRM company,delete,", "twenty-crm.companies.get,Get a Twenty CRM company,read,", "twenty-crm.companies.list,List Twenty CRM companies,read,", "twenty-crm.companies.restore,Restore a Twenty CRM company,update,", "twenty-crm.companies.update,Update a Twenty CRM company,update,", "twenty-crm.logic-functions.get,Get a Twenty CRM logic function,read,", "twenty-crm.logic-functions.list,List Twenty CRM logic functions,read,", "twenty-crm.meta-fields.create,Create a Twenty CRM field,create,", "twenty-crm.meta-fields.delete,Delete a Twenty CRM custom field,delete,", "twenty-crm.meta-fields.get,Get Twenty CRM field metadata,read,", "twenty-crm.meta-fields.update,Update a Twenty CRM field,update,", "twenty-crm.meta-objects.create,Create a Twenty CRM custom object,create,", "twenty-crm.meta-objects.delete,Delete a Twenty CRM custom object,delete,", "twenty-crm.meta-objects.get,Get Twenty CRM object metadata,read,", "twenty-crm.meta-objects.list,List Twenty CRM object metadata,read,", "twenty-crm.meta-objects.update,Update a Twenty CRM object,update,", "twenty-crm.objects.get,Get a Twenty CRM object,read,", "twenty-crm.objects.list,List Twenty CRM objects,read,", "twenty-crm.page-layouts.get,Get a Twenty CRM dashboard layout,read,", "twenty-crm.page-layout-tabs.create,Add a tab to a Twenty CRM dashboard,create,", "twenty-crm.page-layout-tabs.delete,Delete a tab of a Twenty CRM dashboard,delete,", "twenty-crm.page-layout-widgets.create,Add a widget to a Twenty CRM dashboard tab,create,", "twenty-crm.page-layout-widgets.delete,Delete a widget of a Twenty CRM dashboard,delete,", "twenty-crm.page-layout-widgets.update,Update a widget of a Twenty CRM dashboard,update,", "twenty-crm.page-layouts.list,List Twenty CRM dashboard layouts,read,", "twenty-crm.records.batch-create,Create Twenty CRM records in a batch,update,", "twenty-crm.records.batch-delete,Delete Twenty CRM records in a batch,delete,", "twenty-crm.records.batch-update,Update Twenty CRM records in a batch,update,", "twenty-crm.records.create,Create a Twenty CRM record,create,", "twenty-crm.records.delete,Delete a Twenty CRM record,delete,", "twenty-crm.records.destroy,Destroy a Twenty CRM record,delete,", "twenty-crm.records.duplicates,Find duplicates of Twenty CRM records,read,", "twenty-crm.records.get,Get a Twenty CRM record,read,", "twenty-crm.records.group-by,Count Twenty CRM records by field,read,", "twenty-crm.records.list,List Twenty CRM records,read,", "twenty-crm.records.merge,Merge Twenty CRM records,delete,", "twenty-crm.records.merge-preview,Preview the merge of Twenty CRM records,read,", "twenty-crm.records.restore,Restore a Twenty CRM record,update,", "twenty-crm.records.search,Search Twenty CRM records,read,", "twenty-crm.records.search-all,Search all Twenty CRM objects,read,", "twenty-crm.records.update,Update a Twenty CRM record,update,", "twenty-crm.roles.create,Create a Twenty CRM role,create,", "twenty-crm.roles.delete,Delete a Twenty CRM role,delete,", "twenty-crm.roles.list,List Twenty CRM roles,read,", "twenty-crm.roles.set-field-permissions,Set the field rights of a Twenty CRM role,update,", "twenty-crm.roles.set-object-permissions,Set the object rights of a Twenty CRM role,update,", "twenty-crm.roles.set-permission-flags,Set the settings rights of a Twenty CRM role,update,", "twenty-crm.roles.update,Update a Twenty CRM role,update,", "twenty-crm.skills.get,Get a Twenty CRM skill,read,", "twenty-crm.skills.list,List Twenty CRM skills,read,", "twenty-crm.view-fields.create,Add a column to a Twenty CRM view,create,", "twenty-crm.view-fields.delete,Delete a column of a Twenty CRM view,delete,", "twenty-crm.view-fields.update,Update a column of a Twenty CRM view,update,", "twenty-crm.view-filters.create,Add a filter to a Twenty CRM view,create,", "twenty-crm.view-filters.delete,Delete a filter of a Twenty CRM view,delete,", "twenty-crm.view-filters.update,Update a filter of a Twenty CRM view,update,", "twenty-crm.view-groups.delete,Delete a group of a Twenty CRM view,delete,", "twenty-crm.view-groups.update,Update a group of a Twenty CRM view,update,", "twenty-crm.views.create,Create a Twenty CRM view,create,", "twenty-crm.views.delete,Delete a Twenty CRM view,delete,", "twenty-crm.views.get,Get a Twenty CRM view,read,", "twenty-crm.views.list,List Twenty CRM views,read,", "twenty-crm.views.update,Update a Twenty CRM view,update,", "twenty-crm.view-sorts.create,Add a sort to a Twenty CRM view,create,", "twenty-crm.view-sorts.delete,Delete a sort of a Twenty CRM view,delete,", "twenty-crm.view-sorts.update,Update a sort of a Twenty CRM view,update,", "twenty-crm.webhooks.delete,Delete a Twenty CRM webhook,delete,", "twenty-crm.webhooks.get,Get a Twenty CRM webhook,read,", "twenty-crm.webhooks.list,List Twenty CRM webhooks,read,", "twenty-crm.webhooks.update,Update a Twenty CRM webhook,update,", "twenty-crm.workflow-runs.list,List Twenty CRM workflow runs,read,", "twenty-crm.workflow-runs.retry,Retry a Twenty CRM workflow run,execute,", "twenty-crm.workflow-runs.stop,Stop a Twenty CRM workflow run,execute,", "twenty-crm.workflows.get,Get a Twenty CRM workflow,read,", "twenty-crm.workflows.list,List Twenty CRM workflows,read,", "twenty-crm.workflow-versions.activate,Activate a Twenty CRM workflow version,update,", "twenty-crm.workflow-versions.deactivate,Deactivate a Twenty CRM workflow version,update,", "twenty-crm.workspace-members.get,Get a Twenty CRM workspace member,read,", "twenty-crm.workspace-members.list,List Twenty CRM workspace members,read,",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("tools output does not contain %q:\n%s", want, stdout)
		}
	}
	if got := toolIDs(t, string(runTwentyJSON(t, "", "tools", "twenty-crm", "--all", "--query", "twentycrm", "--config", path))); len(got) != 89 {
		t.Errorf("twentycrm tools = %v, want all seven company, two object, sixteen record, three activity link, three attachment, three workflow read, four workflow control, four webhook, two member, seven role, two AI agent read, two skill read, two logic function read, twelve view, nine data model, and five dashboard tools", got)
	}
	if reads.Load() != 0 {
		t.Errorf("secret lookups = %d, want 0", reads.Load())
	}
}

// One tool document carries the complete contract. The workspace-specific data model is not part of it:
// an agent can neither name a Twenty field nor a URL through the contract.
func TestTwentyToolContractExposesOnlyTheStableCoreProjection(t *testing.T) {
	path := twentyConfig(t)

	code, stdout, stderr := runTwentyCLI(t, nil, "", "describe", "twenty-crm.companies.list", "--config", path)
	if code != exitOK || stderr != "" {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	for _, want := range []string{
		"id: twenty-crm.companies.list", "version: 1", "effect: read", "idempotency: safe",
		"confirmation: none", "name_contains", "domain_contains",
		"limit", "sort", "direction", "cursor",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("tool contract does not contain %q:\n%s", want, stdout)
		}
	}

	document := runTwentyJSON(t, "", "describe", "twenty-crm.companies.list", "--config", path)
	var described struct {
		Tool struct {
			InputSchema  json.RawMessage `json:"input_schema"`
			OutputSchema json.RawMessage `json:"output_schema"`
		} `json:"tool"`
		Connections []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(document, &described); err != nil {
		t.Fatalf("tool document = %s: %v", document, err)
	}
	for _, forbidden := range []string{"base_url", "origin", "workspace", "depth", "filter", "order_by"} {
		if strings.Contains(string(described.Tool.InputSchema), forbidden) {
			t.Errorf("the input schema offers %q: %s", forbidden, described.Tool.InputSchema)
		}
	}
	// The projection stays the stable core; a workspace-specific field is not part of the contract.
	for _, forbidden := range []string{"annual_revenue", "people", "opportunities", "custom"} {
		if strings.Contains(string(described.Tool.OutputSchema), forbidden) {
			t.Errorf("the output schema promises %q: %s", forbidden, described.Tool.OutputSchema)
		}
	}
	if len(described.Connections) != 2 {
		t.Errorf("connections = %v, want both configured Twenty connections", described.Connections)
	}
}

// Everything the contract does not allow is refused before a connection is opened and before any secret is
// resolved, so no request can reach a workspace. Two workspaces are never picked silently.
func TestTwentyInvokeRefusalsHappenBeforeSecretsAndProviderIO(t *testing.T) {
	path := twentyConfig(t)

	tests := []struct {
		name  string
		input string
		args  []string
		code  string
	}{
		{
			name: "two workspaces without a default", input: `{}`,
			args: []string{"invoke", "twenty-crm.companies.list", "--config", path},
			code: "connection-ambiguous",
		},
		{
			name: "an unknown argument", input: `{"depth":1}`,
			args: []string{"invoke", "twenty-crm.companies.list", "--connection", "crm", "--config", path},
			code: "invalid-request",
		},
		{
			name: "a page size beyond the bound", input: `{"limit":200}`,
			args: []string{"invoke", "twenty-crm.companies.list", "--connection", "crm", "--config", path},
			code: "invalid-request",
		},
		{
			name: "a filter separator in the search term", input: `{"name_contains":"Acme\",or(name[ilike]:\"%"}`,
			args: []string{"invoke", "twenty-crm.companies.list", "--connection", "crm", "--config", path},
			code: "invalid-request",
		},
		{
			name: "an identifier that is not a UUID", input: `{"id":"1"}`,
			args: []string{"invoke", "twenty-crm.companies.get", "--connection", "crm", "--config", path},
			code: "invalid-request",
		},
		{
			name: "a connection of another provider", input: `{"id":"8f14e45f-ceea-467a-9e60-3b4b5e0f0e21"}`,
			args: []string{"invoke", "twenty-crm.companies.get", "--connection", "wiki", "--config", path},
			code: "unsupported-capability",
		},
		{
			name: "an unknown connection", input: `{}`,
			args: []string{"invoke", "twenty-crm.companies.list", "--connection", "absent", "--config", path},
			code: "unknown-connection",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reads atomic.Int32
			code, stdout, stderr := runTwentyCLI(t, &reads, tt.input, tt.args...)
			if code != exitUsage || stdout != "" {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if !strings.Contains(stderr, "qatlas: "+tt.code+":") {
				t.Errorf("stderr = %q, want the %s code", stderr, tt.code)
			}
			if reads.Load() != 0 {
				t.Errorf("secret lookups = %d, want none before the request was refused", reads.Load())
			}
		})
	}
}

// The MCP broker and the tool CLI publish the same Twenty contracts and the same error codes, because both
// use the application core.
func TestTwentyMCPAndCLIShareTheCoreContracts(t *testing.T) {
	path := twentyConfig(t)
	options := &Options{Config: path, Redactor: &redact.Redactor{}}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"search","method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"qatlas.search","arguments":{"provider":"twenty-crm","all":true,"limit":50}}}`,
		`{"jsonrpc":"2.0","id":"describe","method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"qatlas.describe","arguments":{"operation":"twenty-crm.companies.list","version":1}}}`,
		`{"jsonrpc":"2.0","id":"invoke","method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"qatlas.invoke","arguments":{"operation":"twenty-crm.companies.get",` +
			`"connection":"crm","arguments":{"id":"not-a-uuid"}}}}`,
		`{"jsonrpc":"2.0","id":"ambiguous","method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"qatlas.invoke","arguments":{"operation":"twenty-crm.companies.list","arguments":{}}}}`,
	}, "\n") + "\n"

	responses, stderr := runMCPWithOptions(t, defaultRegistry(), input, options)
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}

	search := toolResultFrom(t, responses[`"search"`])
	var searched struct {
		Operations []application.SearchHit `json:"operations"`
		HasMore    bool                    `json:"has_more"`
	}
	decodeRaw(t, search.Structured, &searched)
	if len(searched.Operations) != 50 || !searched.HasMore || searched.Operations[0].ID != "twenty-crm.activity-targets.create" ||
		searched.Operations[3].ID != "twenty-crm.agents.get" ||
		searched.Operations[4].ID != "twenty-crm.agents.list" ||
		searched.Operations[5].ID != "twenty-crm.attachments.get" ||
		searched.Operations[6].ID != "twenty-crm.attachments.list" ||
		searched.Operations[7].ID != "twenty-crm.attachments.upload" ||
		searched.Operations[8].ID != "twenty-crm.companies.create" ||
		searched.Operations[14].ID != "twenty-crm.companies.update" ||
		searched.Operations[15].ID != "twenty-crm.logic-functions.get" ||
		searched.Operations[16].ID != "twenty-crm.logic-functions.list" ||
		searched.Operations[19].ID != "twenty-crm.meta-fields.get" ||
		searched.Operations[27].ID != "twenty-crm.objects.list" ||
		searched.Operations[33].ID != "twenty-crm.page-layouts.get" ||
		searched.Operations[34].ID != "twenty-crm.page-layouts.list" ||
		searched.Operations[35].ID != "twenty-crm.records.batch-create" ||
		searched.Operations[38].ID != "twenty-crm.records.create" ||
		searched.Operations[41].ID != "twenty-crm.records.duplicates" ||
		searched.Operations[44].ID != "twenty-crm.records.list" ||
		searched.Operations[45].ID != "twenty-crm.records.merge" ||
		searched.Operations[48].ID != "twenty-crm.records.search" ||
		searched.Operations[49].ID != "twenty-crm.records.search-all" {
		t.Fatalf("search operations = %+v", searched.Operations)
	}

	describe := toolResultFrom(t, responses[`"describe"`])
	describedByCLI := runTwentyJSON(t, "", "describe", "twenty-crm.companies.list", "--config", path, "--output", "json")
	assertMCPParity(t, describedByCLI, "tool", describe.Structured, "operation")
	assertMCPParity(t, describedByCLI, "connections", describe.Structured, "connections")

	invoke := toolResultFrom(t, responses[`"invoke"`])
	if !invoke.IsError {
		t.Fatalf("invoke = %+v, want the invalid identifier refused", invoke)
	}
	mcpCode, _, _ := strings.Cut(invoke.Content[0].Text, ":")
	code, _, cliStderr := runTwentyCLI(t, nil, `{"id":"not-a-uuid"}`,
		"invoke", "twenty-crm.companies.get", "--connection", "crm", "--config", path)
	cliCode, _, _ := strings.Cut(strings.TrimPrefix(strings.SplitN(cliStderr, "\n", 2)[0], "qatlas: "), ":")
	if code != exitUsage || mcpCode != cliCode || cliCode != "invalid-request" {
		t.Fatalf("MCP code=%q CLI code=%q exit=%d", mcpCode, cliCode, code)
	}

	// The broker does not pick one of the two workspaces either.
	ambiguous := toolResultFrom(t, responses[`"ambiguous"`])
	if !ambiguous.IsError || !strings.HasPrefix(ambiguous.Content[0].Text, "connection-ambiguous:") {
		t.Fatalf("ambiguous invoke = %+v, want several matching connections", ambiguous)
	}
}

func runTwentyJSON(t *testing.T, input string, args ...string) []byte {
	t.Helper()
	if len(args) > 0 && args[len(args)-1] != "json" {
		args = append(args, "--output", "json")
	}
	code, stdout, stderr := runTwentyCLI(t, nil, input, args...)
	if code != exitOK || stderr != "" {
		t.Fatalf("CLI %v exit=%d stderr=%q", args, code, stderr)
	}
	return []byte(stdout)
}
