package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const mcpTestMeta = `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}`

func TestMCPCommandServesStdio(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + mcpTestMeta + `}}` + "\n"
	var stdout, stderr bytes.Buffer
	options := &Options{Input: strings.NewReader(input), Redactor: &redact.Redactor{}}
	code := run(newRootCommand(options, fakeRegistry(t)), options, []string{"mcp"}, &stdout, &stderr)
	if code != exitOK || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	responses := decodeMCPResponses(t, stdout.String())
	var result struct {
		Tools []mcpTool `json:"tools"`
	}
	decodeRaw(t, responses["1"].Result, &result)
	if len(result.Tools) != 3 {
		t.Fatalf("tools = %d, want 3", len(result.Tools))
	}
}

func TestMCPDiscoveryAndFixedTools(t *testing.T) {
	registry := fakeRegistry(t)
	addSyntheticOperations(t, registry, 100)

	input := `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{` + mcpTestMeta + `}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{` + mcpTestMeta + `}}` + "\n"
	responses, stderr := runMCP(t, registry, input)
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
	discover := responses["1"].Result
	if versions := stringFieldSlice(t, discover, "supportedVersions"); !reflect.DeepEqual(versions, []string{mcpProtocolVersion}) {
		t.Fatalf("supported versions = %v", versions)
	}
	capabilities := objectField(t, discover, "capabilities")
	if _, ok := capabilities["tools"]; !ok {
		t.Fatalf("capabilities = %s", capabilities)
	}
	// A client that only speaks MCP reads the same guide 'qatlas agents' prints, as the server instructions.
	var instructions struct {
		Instructions string `json:"instructions"`
	}
	decodeRaw(t, discover, &instructions)
	if instructions.Instructions != topicText(t, "agents") {
		t.Errorf("server/discover instructions = %q, want the agents guide", instructions.Instructions)
	}

	var listed struct {
		ResultType string    `json:"resultType"`
		Tools      []mcpTool `json:"tools"`
		TTL        int       `json:"ttlMs"`
		CacheScope string    `json:"cacheScope"`
	}
	decodeRaw(t, responses["2"].Result, &listed)
	if listed.ResultType != "complete" || len(listed.Tools) != 3 ||
		listed.TTL != mcpCacheTTLMillis || listed.CacheScope != "public" {
		t.Fatalf("tools/list = %+v", listed)
	}
	want := []string{"qatlas.search", "qatlas.describe", "qatlas.invoke"}
	for i, tool := range listed.Tools {
		if tool.Name != want[i] || len(tool.InputSchema) == 0 {
			t.Errorf("tool %d = %+v, want %q with schema", i, tool, want[i])
		}
	}
	var searchSchema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	decodeRaw(t, listed.Tools[0].InputSchema, &searchSchema)
	if _, ok := searchSchema.Properties["cursor"]; !ok {
		t.Errorf("qatlas.search schema = %s, want a cursor property", listed.Tools[0].InputSchema)
	}
}

func TestMCPToolsUseApplicationCoreContracts(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	t.Setenv("QATLAS_CREDENTIAL_STORE", "none")
	path := writeConfig(t, validConfig)
	registry := fakeRegistry(t)
	searchRequest := `{"query":"page"}`
	describeRequest := `{"operation":"bookstack.pages.get","version":1}`
	invokeRequest := `{"operation":"bookstack.pages.get","connection":"wiki","arguments":{}}`
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"search","method":"tools/call","params":{` + mcpTestMeta + `,"name":"qatlas.search","arguments":` + searchRequest + `}}`,
		`{"jsonrpc":"2.0","id":"describe","method":"tools/call","params":{` + mcpTestMeta + `,"name":"qatlas.describe","arguments":` + describeRequest + `}}`,
		`{"jsonrpc":"2.0","id":"invoke","method":"tools/call","params":{` + mcpTestMeta + `,"name":"qatlas.invoke","arguments":` + invokeRequest + `}}`,
	}, "\n") + "\n"

	responses, stderr := runMCPWithOptions(t, registry, input, &Options{Config: path, Redactor: &redact.Redactor{}})
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}

	search := toolResultFrom(t, responses[`"search"`])
	if search.IsError {
		t.Fatalf("search returned an error: %s", search.Content[0].Text)
	}
	var searchResult struct {
		Operations []application.SearchHit `json:"operations"`
	}
	decodeRaw(t, search.Structured, &searchResult)
	if len(searchResult.Operations) != 2 {
		t.Fatalf("search operations = %d, want 2", len(searchResult.Operations))
	}
	// qatlas.search pages the entries of the CLI index and lists their connections instead of joining them.
	// What must agree is which tools they name, in which order, and what each entry says.
	indexed := toolSummaries(t, string(runFakeCLIJSON(t, "", "tools", "--query", "page", "--config", path,
		"--output", "json")))
	if len(indexed) != len(searchResult.Operations) {
		t.Fatalf("index = %+v, want the %d searched operations", indexed, len(searchResult.Operations))
	}
	for i, tool := range indexed {
		hit := searchResult.Operations[i]
		if tool.ID != hit.ID || tool.Title != hit.Title || tool.Effect != hit.Effect ||
			tool.Connections != strings.Join(hit.Connections, " ") {
			t.Errorf("index[%d] = %+v, want %+v", i, tool, hit)
		}
	}

	describe := toolResultFrom(t, responses[`"describe"`])
	var described application.DescribeResponse
	decodeRaw(t, describe.Structured, &described)
	wantConnections := []application.ConnectionRef{
		{Name: "wiki", Description: "read-only account on the team wiki"},
	}
	if describe.IsError || described.Operation.ID != "bookstack.pages.get" ||
		!reflect.DeepEqual(described.Connections, wantConnections) {
		t.Fatalf("describe = %+v, structured = %+v", describe, described)
	}
	describedByCLI := runFakeCLIJSON(t, "", "describe", "bookstack.pages.get", "--config", path, "--output", "json")
	assertMCPParity(t, describedByCLI, "tool", describe.Structured, "operation")
	assertMCPParity(t, describedByCLI, "connections", describe.Structured, "connections")

	invoke := toolResultFrom(t, responses[`"invoke"`])
	var invoked application.InvokeResponse
	decodeRaw(t, invoke.Structured, &invoked)
	var providerResult map[string]any
	if err := json.Unmarshal(invoked.Result, &providerResult); err != nil {
		t.Fatal(err)
	}
	if invoke.IsError || invoked.Operation != "bookstack.pages.get" || providerResult["html"] != "<p>Page</p>" {
		t.Fatalf("invoke = %+v, structured = %+v", invoke, invoked)
	}
	assertMCPParity(t, runFakeCLIJSON(t, "{}", "invoke", "bookstack.pages.get", "--connection", "wiki",
		"--config", path), "data", invoke.Structured, "")

	for _, result := range []mcpToolResult{search, describe, invoke} {
		var textValue any
		if err := json.Unmarshal([]byte(result.Content[0].Text), &textValue); err != nil {
			t.Fatalf("text content is not JSON: %q: %v", result.Content[0].Text, err)
		}
		var structuredValue any
		if err := json.Unmarshal(result.Structured, &structuredValue); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(textValue, structuredValue) {
			t.Errorf("text and structured content differ: %#v != %#v", textValue, structuredValue)
		}
	}
}

// A client of a handshake-based protocol version opens with initialize and then speaks without per-request
// metadata. It gets the requested version when the server speaks it and the newest one otherwise, the same
// guide as instructions, and the same fixed tools; a request that declares a version is still served per
// request.
func TestMCPInitializeNegotiatesLegacyVersions(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	t.Setenv("QATLAS_CREDENTIAL_STORE", "none")
	path := writeConfig(t, validConfig)
	registry := fakeRegistry(t)
	initialize := func(id int, params string) string {
		return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"initialize","params":%s}`, id, params)
	}

	for _, tt := range []struct{ requested, want string }{
		{"2025-06-18", "2025-06-18"},
		{"2025-11-25", "2025-11-25"},
		{"1900-01-01", "2025-11-25"},
		{mcpProtocolVersion, "2025-11-25"},
	} {
		input := strings.Join([]string{
			`{"jsonrpc":"2.0","id":0,"method":"tools/list","params":{}}`,
			initialize(1, `{"protocolVersion":"`+tt.requested+`","capabilities":{},"clientInfo":{"name":"c","version":"1"}}`),
			`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
			`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
			`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
			`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"_meta":{"progressToken":1},"name":"qatlas.search","arguments":{"query":"page"}}}`,
			`{"jsonrpc":"2.0","id":5,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"1900-01-01","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		}, "\n") + "\n"
		responses, stderr := runMCPWithOptions(t, registry, input, &Options{Config: path, Redactor: &redact.Redactor{}})
		if stderr != "" || len(responses) != 6 {
			t.Fatalf("%s: responses = %v, stderr = %q", tt.requested, responses, stderr)
		}
		if refused := responses["0"].Error; refused == nil || refused.Code != mcpInvalidParams ||
			!strings.Contains(refused.Message, "initialize") {
			t.Errorf("%s: request before initialize = %+v", tt.requested, responses["0"])
		}
		var result struct {
			ProtocolVersion string                     `json:"protocolVersion"`
			Capabilities    map[string]json.RawMessage `json:"capabilities"`
			ServerInfo      struct{ Name string }      `json:"serverInfo"`
			Instructions    string                     `json:"instructions"`
		}
		decodeRaw(t, responses["1"].Result, &result)
		if _, ok := result.Capabilities["tools"]; result.ProtocolVersion != tt.want || !ok ||
			result.ServerInfo.Name != "qatlas" || result.Instructions != topicText(t, "agents") {
			t.Errorf("%s: initialize = %s, want version %s", tt.requested, responses["1"].Result, tt.want)
		}
		var listed struct{ Tools []mcpTool }
		decodeRaw(t, responses["2"].Result, &listed)
		if len(listed.Tools) != 3 {
			t.Errorf("%s: tools/list = %s", tt.requested, responses["2"].Result)
		}
		if responses["3"].Error != nil || string(responses["3"].Result) != "{}" {
			t.Errorf("%s: ping = %+v", tt.requested, responses["3"])
		}
		if search := toolResultFrom(t, responses["4"]); search.IsError {
			t.Errorf("%s: search = %+v", tt.requested, search)
		}
		if refused := responses["5"].Error; refused == nil || refused.Code != mcpUnsupportedVersion {
			t.Errorf("%s: declared version after initialize = %+v", tt.requested, responses["5"])
		}
	}

	responses, _ := runMCP(t, registry, initialize(1, `{"capabilities":{}}`)+"\n")
	if refused := responses["1"].Error; refused == nil || refused.Code != mcpInvalidParams ||
		!strings.Contains(refused.Message, "params.protocolVersion") ||
		!strings.Contains(refused.Message, mcpProtocolVersion) || !strings.Contains(refused.Message, "2025-06-18") {
		t.Fatalf("initialize without a version = %+v", responses["1"])
	}
}

// A request without the per-request metadata names the exact _meta key it lacks and every protocol version
// the server speaks.
func TestMCPMetadataErrorsNameTheKey(t *testing.T) {
	for _, tt := range []struct{ meta, want string }{
		{``, `params._meta must be an object declaring "io.modelcontextprotocol/protocolVersion" and ` +
			`"io.modelcontextprotocol/clientCapabilities"; this server speaks MCP 2026-07-28 with per-request ` +
			`params._meta, or MCP 2025-11-25 or 2025-06-18 after an initialize request`},
		{`"_meta":{"io.modelcontextprotocol/clientCapabilities":{}}`,
			`params._meta["io.modelcontextprotocol/protocolVersion"] must be a non-empty string; this server ` +
				`speaks MCP 2026-07-28 with per-request params._meta, or MCP 2025-11-25 or 2025-06-18 after an ` +
				`initialize request`},
		{`"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}`,
			`params._meta["io.modelcontextprotocol/clientCapabilities"] must be an object`},
		{`"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{}}`,
			`params._meta["io.modelcontextprotocol/clientInfo"] must be an object with a non-empty name and version`},
	} {
		responses, _ := runMCP(t, fakeRegistry(t),
			`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{`+tt.meta+`}}`+"\n")
		if refused := responses["1"].Error; refused == nil || refused.Code != mcpInvalidParams || refused.Message != tt.want {
			t.Errorf("meta %s: response = %+v, want %q", tt.meta, responses["1"], tt.want)
		}
	}
}

// A broker argument outside the published input schema names the field, so a caller that sends tool
// instead of operation learns which name is wrong.
func TestMCPBrokerArgumentErrorsNameTheField(t *testing.T) {
	for _, tt := range []struct{ tool, arguments, want string }{
		{"qatlas.describe", `{"tool":"bookstack.pages.get"}`, "invalid-request: $.tool is not allowed"},
		{"qatlas.invoke", `{"operation":"bookstack.pages.get","version":"1"}`, "invalid-request: $.version must be integer"},
		{"qatlas.invoke", `{"connection":"wiki"}`, "invalid-request: $.operation is required"},
		{"qatlas.search", `{"effect":"write"}`, "invalid-request: $.effect is not an allowed value"},
	} {
		input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta + `,"name":"` + tt.tool +
			`","arguments":` + tt.arguments + `}}` + "\n"
		responses, _ := runMCP(t, fakeRegistry(t), input)
		if result := toolResultFrom(t, responses["1"]); !result.IsError || result.Content[0].Text != tt.want {
			t.Errorf("%s %s = %+v, want %q", tt.tool, tt.arguments, result, tt.want)
		}
	}
}

// A tool that requires an explicit connection is refused without one over the tool CLI and the MCP broker
// alike, and both name the routes that offer it as the same detail.
func TestConnectionSelectionNamesCandidatesOverCLIAndMCP(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	t.Setenv("QATLAS_CREDENTIAL_STORE", "none")
	registry := capability.NewRegistry()
	if err := registry.RegisterProvider(config.ProviderMetadata{ID: "fake", Name: "Fake"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("fake", capability.Operation{
		Descriptor: capability.Descriptor{
			ID: "fake.pages.get", Version: 1, Description: "Read a fake page", Provider: "fake",
			RequiresExplicitConnection: true,
			Risk: capability.Risk{
				Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
				Confirmation: capability.ConfirmationNone, DataSensitivity: "test",
			},
			InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`),
		},
		Handler: func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
			json.RawMessage) (any, error) {
			return map[string]any{}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, `version: 1
services:
  fake:
    provider: fake
    base_url: https://example.invalid
credentials:
  reader:
    type: keyring
connections:
  primary:
    service: fake
    credential: reader
    description: team pages
  secondary:
    service: fake
    credential: reader
defaults: {}
`)

	var stdout, stderr bytes.Buffer
	options := &Options{Input: strings.NewReader("{}"), Redactor: &redact.Redactor{}}
	code := run(newRootCommand(options, registry), options,
		[]string{"invoke", "fake.pages.get", "--config", path}, &stdout, &stderr)
	lines := strings.Split(stderr.String(), "\n")
	if code != exitUsage || len(lines) < 2 || !strings.HasPrefix(lines[0], "qatlas: connection-selection: ") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}

	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta +
		`,"name":"qatlas.invoke","arguments":{"operation":"fake.pages.get","arguments":{}}}}` + "\n"
	responses, _ := runMCPWithOptions(t, registry, input, &Options{Config: path, Redactor: &redact.Redactor{}})
	invoke := toolResultFrom(t, responses["1"])
	if !invoke.IsError || !strings.HasPrefix(invoke.Content[0].Text, "connection-selection: ") {
		t.Fatalf("invoke = %+v", invoke)
	}
	if !jsonEqual([]byte(lines[1]), invoke.Structured) {
		t.Fatalf("CLI detail = %s, MCP detail = %s", lines[1], invoke.Structured)
	}
	var detail struct {
		Code        string                      `json:"code"`
		Operation   string                      `json:"operation"`
		Connections []application.ConnectionRef `json:"connections"`
	}
	decodeRaw(t, invoke.Structured, &detail)
	want := []application.ConnectionRef{{Name: "primary", Description: "team pages"}, {Name: "secondary"}}
	if detail.Code != string(output.CodeConnectionSelection) || detail.Operation != "fake.pages.get" ||
		!reflect.DeepEqual(detail.Connections, want) {
		t.Fatalf("detail = %+v, want the candidates %+v", detail, want)
	}
}

// ambiguousConfig offers bookstack.pages.list over three routes with similar names: one described, one
// without a description, and one whose description contains a value the redactor knows. The service,
// credential, environment variables and target are canaries that no diagnostic may carry.
const ambiguousConfig = `
version: 1
services:
  service-canary:
    provider: bookstack
    base_url: https://endpoint-canary.example.invalid
credentials:
  credential-canary:
    type: env
    values:
      token-id: ENV_CANARY_ID
      token-secret: ENV_CANARY_SECRET
connections:
  wiki:
    service: service-canary
    credential: credential-canary
    target: target-canary
    description: read-only account on the team wiki
  wiki-2:
    service: service-canary
    credential: credential-canary
  wiki-staging:
    service: service-canary
    credential: credential-canary
    description: staging wiki secret-canary-value
`

// An ambiguous route is refused over the tool CLI and the MCP broker alike, and both name the same
// candidates with their descriptions as data: an empty description stays an empty string, a known secret is
// redacted, and nothing else of a route leaves the core.
func TestConnectionAmbiguityNamesCandidatesOverCLIAndMCP(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	t.Setenv("QATLAS_CREDENTIAL_STORE", "none")
	path := writeConfig(t, ambiguousConfig)
	registry := fakeRegistry(t)
	newRedactor := func() *redact.Redactor {
		redactor := &redact.Redactor{}
		redactor.Add("secret-canary-value")
		return redactor
	}

	var stdout, stderr bytes.Buffer
	redactor := newRedactor()
	options := &Options{Input: strings.NewReader("{}"), Redactor: redactor,
		Secrets: secret.NewWith(nil, nil, nil, redactor)}
	code := run(newRootCommand(options, registry), options,
		[]string{"invoke", "bookstack.pages.list", "--config", path}, &stdout, &stderr)
	lines := strings.Split(stderr.String(), "\n")
	if code != exitUsage || stdout.Len() != 0 || len(lines) < 2 ||
		!strings.HasPrefix(lines[0], "qatlas: connection-ambiguous: ") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	cliDetail := []byte(lines[1])

	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta +
		`,"name":"qatlas.invoke","arguments":{"operation":"bookstack.pages.list","arguments":{}}}}` + "\n"
	responses, mcpStderr := runMCPWithOptions(t, registry, input, &Options{Config: path, Redactor: newRedactor()})
	if mcpStderr != "" {
		t.Fatalf("MCP stderr = %q", mcpStderr)
	}
	invoke := toolResultFrom(t, responses["1"])
	if !invoke.IsError || !strings.HasPrefix(invoke.Content[0].Text, "connection-ambiguous: ") {
		t.Fatalf("invoke = %+v", invoke)
	}
	if !jsonEqual(cliDetail, invoke.Structured) {
		t.Fatalf("CLI detail = %s, MCP detail = %s", cliDetail, invoke.Structured)
	}

	var detail struct {
		Code        string                      `json:"code"`
		Message     string                      `json:"message"`
		Operation   string                      `json:"operation"`
		Connections []application.ConnectionRef `json:"connections"`
	}
	decoder := json.NewDecoder(bytes.NewReader(invoke.Structured))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&detail); err != nil {
		t.Fatalf("detail %s: %v", invoke.Structured, err)
	}
	want := []application.ConnectionRef{
		{Name: "wiki", Description: "read-only account on the team wiki"},
		{Name: "wiki-2", Description: ""},
		{Name: "wiki-staging", Description: "staging wiki " + redact.Marker},
	}
	if detail.Code != string(output.CodeConnectionAmbiguous) || detail.Operation != "bookstack.pages.list" ||
		detail.Message != strings.TrimPrefix(invoke.Content[0].Text, "connection-ambiguous: ") ||
		!reflect.DeepEqual(detail.Connections, want) {
		t.Fatalf("detail = %+v, want the candidates %+v", detail, want)
	}
	if !strings.Contains(string(invoke.Structured), `{"name":"wiki-2","description":""}`) {
		t.Errorf("detail %s must keep the missing description as an empty string", invoke.Structured)
	}
	for _, published := range []string{stderr.String(), string(responses["1"].Result)} {
		for _, canary := range []string{"service-canary", "endpoint-canary", "credential-canary", "ENV_CANARY",
			"target-canary", "secret-canary-value"} {
			if strings.Contains(published, canary) {
				t.Errorf("diagnostic %q carries %q", published, canary)
			}
		}
	}
}

// qatlas.search pages through the same catalog the tool CLI lists in one answer. Following next_cursor
// from the first page names every tool of the namespace once, in the order of the CLI index, and a cursor
// that does not continue this search fails as an invalid request.
func TestMCPSearchPagesMatchTheCLIIndex(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	t.Setenv("QATLAS_CREDENTIAL_STORE", "none")
	path := writeConfig(t, validConfig)
	registry := fakeRegistry(t)
	addSyntheticOperations(t, registry, 2*50+3)

	var stdout, stderr bytes.Buffer
	options := &Options{Redactor: &redact.Redactor{}}
	code := run(newRootCommand(options, registry), options,
		[]string{"tools", "bookstack", "--config", path, "--output", "json"}, &stdout, &stderr)
	if code != exitOK || stderr.Len() != 0 {
		t.Fatalf("tools exit=%d stderr=%q", code, stderr.String())
	}
	indexed := toolSummaries(t, stdout.String())

	search := func(arguments string) mcpToolResult {
		t.Helper()
		input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"qatlas.search","arguments":` + arguments + `}}` + "\n"
		responses, stderr := runMCPWithOptions(t, registry, input, &Options{Config: path, Redactor: &redact.Redactor{}})
		if stderr != "" {
			t.Fatalf("stderr = %q", stderr)
		}
		return toolResultFrom(t, responses["1"])
	}
	var searched []application.SearchHit
	var firstCursor string
	arguments := `{"provider":"bookstack"}`
	for pages := 1; ; pages++ {
		result := search(arguments)
		if result.IsError {
			t.Fatalf("search returned an error: %s", result.Content[0].Text)
		}
		var page application.SearchResponse
		decodeRaw(t, result.Structured, &page)
		searched = append(searched, page.Operations...)
		if (page.NextCursor != "") != page.HasMore {
			t.Fatalf("page %d: has_more=%t next_cursor=%q", pages, page.HasMore, page.NextCursor)
		}
		if !page.HasMore {
			if pages != 3 {
				t.Fatalf("read %d pages, want 3", pages)
			}
			break
		}
		if firstCursor == "" {
			firstCursor = page.NextCursor
		}
		arguments = `{"provider":"bookstack","cursor":"` + page.NextCursor + `"}`
	}
	if len(searched) != len(indexed) {
		t.Fatalf("search pages name %d tools, the CLI index %d", len(searched), len(indexed))
	}
	for i, tool := range indexed {
		hit := searched[i]
		if tool.ID != hit.ID || tool.Title != hit.Title || tool.Effect != hit.Effect {
			t.Errorf("index[%d] = %+v, search = %+v", i, tool, hit)
		}
	}

	for _, arguments := range []string{
		`{"provider":"bookstack","effect":"read","cursor":"` + firstCursor + `"}`,
		`{"cursor":"` + firstCursor + `"}`,
		`{"provider":"bookstack","cursor":"not a cursor"}`,
	} {
		result := search(arguments)
		if !result.IsError || !strings.HasPrefix(result.Content[0].Text, string(output.CodeInvalidRequest)+": ") {
			t.Errorf("search %s = %+v, want an invalid-request tool error", arguments, result)
		}
	}
}

// A qatlas.search hit carries what picking a tool needs, in the order of the CLI index: id, title, effect,
// and the offering connections as a list, plus the reason only in a search with all. Description, version,
// tags, and provider stay one describe away.
func TestMCPSearchHitsAreCompact(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	t.Setenv("QATLAS_CREDENTIAL_STORE", "none")
	path := writeConfig(t, strings.Replace(validConfig, "    description: read-only account on the team wiki\n",
		"    description: read-only account on the team wiki\n    tools: [bookstack.pages.get]\n", 1))
	registry := fakeRegistry(t)
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"offered","method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"qatlas.search","arguments":{"provider":"bookstack"}}}`,
		`{"jsonrpc":"2.0","id":"all","method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"qatlas.search","arguments":{"provider":"bookstack","all":true}}}`,
	}, "\n") + "\n"
	responses, stderr := runMCPWithOptions(t, registry, input, &Options{Config: path, Redactor: &redact.Redactor{}})
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}

	for id, want := range map[string]string{
		`"offered"`: `{"operations":[{"id":"bookstack.pages.get","title":"Read one page","effect":"read",` +
			`"connections":["wiki"]}],"has_more":false}`,
		`"all"`: `{"operations":[{"id":"bookstack.pages.get","title":"Read one page","effect":"read",` +
			`"connections":["wiki"]},{"id":"bookstack.pages.list","title":"List pages","effect":"read",` +
			`"connections":[],"reason":"not-in-tools-list"}],"has_more":false}`,
	} {
		result := toolResultFrom(t, responses[id])
		if result.IsError || string(result.Structured) != want || result.Content[0].Text != want {
			t.Errorf("search %s = %s, text %q, want %s", id, result.Structured, result.Content[0].Text, want)
		}
	}
}

// addSyntheticOperations registers count read operations in the BookStack namespace.
func addSyntheticOperations(t *testing.T, registry *capability.Registry, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		descriptor := capability.Descriptor{
			ID: fmt.Sprintf("bookstack.synthetic%d.get", i), Version: 1,
			Description: "Synthetic operation", Provider: "bookstack",
			Risk: capability.Risk{
				Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
				Confirmation: capability.ConfirmationNone, DataSensitivity: "test",
			},
			InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
		}
		if err := registry.Register("bookstack", capability.Operation{
			Descriptor: descriptor,
			Handler: func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
				json.RawMessage) (any, error) {
				return map[string]any{}, nil
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMCPAndJSONCLIErrorCodeParity(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	path := writeConfig(t, validConfig)
	request := `{"operation":"absent.operation.get"}`
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta +
		`,"name":"qatlas.describe","arguments":` + request + `}}` + "\n"
	responses, _ := runMCPWithOptions(t, fakeRegistry(t), input, &Options{Config: path})
	mcpResult := toolResultFrom(t, responses["1"])
	if !mcpResult.IsError {
		t.Fatalf("MCP result = %+v, want tool error", mcpResult)
	}
	mcpCode, _, _ := strings.Cut(mcpResult.Content[0].Text, ":")

	code, stdout, stderr := runFakeCLI(t, "describe", "absent.operation.get", "--config", path)
	if code != exitUsage || stdout != "" {
		t.Fatalf("JSON CLI exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	firstLine, _, _ := strings.Cut(stderr, "\n")
	cliDiagnostic := strings.TrimPrefix(firstLine, "qatlas: ")
	cliCode, _, _ := strings.Cut(cliDiagnostic, ":")
	if mcpCode != cliCode || cliCode != "unknown-operation" {
		t.Fatalf("MCP code=%q JSON CLI code=%q", mcpCode, cliCode)
	}
}

func TestMCPToolErrorsAreSafeAndProtocolErrorsStaySeparate(t *testing.T) {
	const canary = "secret-provider-detail-7294"
	t.Setenv("QATLAS_CREDENTIAL_STORE", "none")
	registry, path := mcpTestRegistry(t, func(context.Context, *config.Resolved, *secret.Resolver,
		*redact.Redactor, json.RawMessage) (any, error) {
		return nil, errors.New("provider failed with " + canary)
	})
	redactor := &redact.Redactor{}
	redactor.Add(canary)
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta + `,"name":"qatlas.invoke","arguments":{"operation":"fake.pages.get","connection":"primary","arguments":{}}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{` + mcpTestMeta + `,"name":"qatlas.absent","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"1900-01-01","io.modelcontextprotocol/clientCapabilities":{}}}}`,
	}, "\n") + "\n"
	responses, stderr := runMCPWithOptions(t, registry, input, &Options{Config: path, Redactor: redactor})

	execution := toolResultFrom(t, responses["1"])
	if !execution.IsError || !strings.HasPrefix(execution.Content[0].Text, "runtime:") ||
		!strings.Contains(execution.Content[0].Text, redact.Marker) {
		t.Fatalf("tool execution error = %+v", execution)
	}
	if responses["1"].Error != nil {
		t.Fatalf("execution failure became a protocol error: %+v", responses["1"].Error)
	}
	if responses["2"].Error == nil || responses["2"].Error.Code != mcpInvalidParams {
		t.Fatalf("unknown tool response = %+v", responses["2"])
	}
	if responses["3"].Error == nil || responses["3"].Error.Code != mcpUnsupportedVersion {
		t.Fatalf("unsupported version response = %+v", responses["3"])
	}
	if strings.Contains(encodeResponses(t, responses)+stderr, canary) {
		t.Fatal("MCP output leaked the registered secret canary")
	}
}

// A resource the provider does not hold or does not show to the credential is the runtime failure not-found,
// with the same code and message over the CLI, exit code 1, and MCP.
func TestNotFoundIsTheSameRuntimeFailureInCLIAndMCP(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	t.Setenv("QATLAS_CREDENTIAL_STORE", "none")
	const message = "GitHub does not hold repository octo-org/absent or does not show it to this token"
	registry, path := mcpTestRegistry(t, func(context.Context, *config.Resolved, *secret.Resolver,
		*redact.Redactor, json.RawMessage) (any, error) {
		return nil, &provider.Error{Class: provider.ClassNotFound, Op: "get page", Message: message}
	})

	var stdout, stderr bytes.Buffer
	opts := &Options{Redactor: &redact.Redactor{}}
	code := run(newRootCommand(opts, registry), opts,
		[]string{"invoke", "fake.pages.get", "--connection", "primary", "--config", path}, &stdout, &stderr)
	want := "not-found: get page: " + message
	if code != exitRuntime || stdout.Len() != 0 || strings.TrimSpace(stderr.String()) != "qatlas: "+want {
		t.Fatalf("CLI exit=%d stdout=%q stderr=%q, want exit %d and %q", code, stdout.String(), stderr.String(),
			exitRuntime, want)
	}

	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta + `,"name":"qatlas.invoke",` +
		`"arguments":{"operation":"fake.pages.get","connection":"primary","arguments":{}}}}` + "\n"
	responses, _ := runMCPWithOptions(t, registry, input, &Options{Config: path, Redactor: &redact.Redactor{}})
	result := toolResultFrom(t, responses["1"])
	if !result.IsError || result.Content[0].Text != want {
		t.Fatalf("MCP result = %+v, want the error %q", result, want)
	}
}

func TestMCPConfirmedMutationKeepsAuditOffProtocol(t *testing.T) {
	const message = "private-message-canary-1842"
	registry, path := mcpSendRegistry(t)
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta +
		`,"name":"qatlas.invoke","arguments":{"operation":"fake.messages.send","connection":"target","arguments":{"text":"` +
		message + `"},"confirm":true}}}` + "\n"
	responses, stderr := runMCPWithOptions(t, registry, input, &Options{
		Config: path, Redactor: &redact.Redactor{}, Secrets: secret.NewWith(nil, nil, nil, nil),
	})
	if result := toolResultFrom(t, responses["1"]); result.IsError {
		t.Fatalf("mutation result = %+v", result)
	}
	var audit map[string]any
	if err := json.Unmarshal([]byte(stderr), &audit); err != nil {
		t.Fatalf("stderr is not one audit event: %q: %v", stderr, err)
	}
	if len(audit) != 6 || audit["operation"] != "fake.messages.send" || audit["connection"] != "target" ||
		audit["confirmed"] != true || audit["result"] != "success" {
		t.Fatalf("audit = %#v", audit)
	}
	if strings.Contains(stderr, message) || strings.Contains(encodeResponses(t, responses), `"request_id"`) {
		t.Fatalf("message or audit crossed streams: stdout=%s stderr=%s", encodeResponses(t, responses), stderr)
	}
}

// mcpSendRegistry offers fake.messages.send, a create tool that requires confirmation and an explicit
// connection, through the connection target.
func mcpSendRegistry(t *testing.T) (*capability.Registry, string) {
	t.Helper()
	registry := capability.NewRegistry()
	if err := registry.RegisterProvider(config.ProviderMetadata{
		ID: "fake", Name: "Fake", DefaultPermissions: []config.Permission{config.PermissionCreate},
	}, nil); err != nil {
		t.Fatal(err)
	}
	descriptor := capability.Descriptor{
		ID: "fake.messages.send", Version: 1, Description: "Send a fake message", Provider: "fake",
		RequiresExplicitConnection: true,
		Risk: capability.Risk{
			Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
			Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: "message",
		},
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`),
	}
	if err := registry.Register("fake", capability.Operation{
		Descriptor: descriptor,
		Handler: func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
			json.RawMessage) (any, error) {
			return map[string]any{"ok": true}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	return registry, writeConfig(t, `version: 1
services:
  fake:
    provider: fake
    base_url: https://example.invalid
credentials:
  sender:
    type: keyring
connections:
  target:
    service: fake
    credential: sender
defaults: {}
`)
}

func TestMCPCancellationStopsCoreAndSuppressesResponse(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	registry, path := mcpTestRegistry(t, func(ctx context.Context, _ *config.Resolved, _ *secret.Resolver,
		_ *redact.Redactor, _ json.RawMessage) (any, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	})
	options := &Options{Config: path, Redactor: &redact.Redactor{}, Secrets: secret.NewWith(nil, nil, nil, nil)}
	var stdout, stderr bytes.Buffer
	server := newMCPServer(options, registry, &stdout, &stderr)
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- server.serve(context.Background(), reader) }()

	call := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{` + mcpTestMeta + `,"name":"qatlas.invoke","arguments":{"operation":"fake.pages.get","connection":"primary","arguments":{}}}}` + "\n"
	if _, err := io.WriteString(writer, call); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	if _, err := io.WriteString(writer, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":7,"reason":"test"}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not reach the application handler")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("cancelled request wrote stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestMCPDeadlineReturnsTimeoutToolError(t *testing.T) {
	registry, path := mcpTestRegistry(t, func(ctx context.Context, _ *config.Resolved, _ *secret.Resolver,
		_ *redact.Redactor, _ json.RawMessage) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	var stdout, stderr bytes.Buffer
	server := newMCPServer(&Options{
		Config: path, Redactor: &redact.Redactor{}, Secrets: secret.NewWith(nil, nil, nil, nil),
	}, registry, &stdout, &stderr)
	server.timeout = 20 * time.Millisecond
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta + `,"name":"qatlas.invoke","arguments":{"operation":"fake.pages.get","connection":"primary","arguments":{}}}}` + "\n"
	if err := server.serve(context.Background(), strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	responses := decodeMCPResponses(t, stdout.String())
	result := toolResultFrom(t, responses["1"])
	if !result.IsError || result.Content[0].Text != "timeout: the request did not finish within 20ms; "+
		"check that the service answers, then try again" {
		t.Fatalf("deadline result = %+v", result)
	}
}

func TestMCPMessageLimitDoesNotDesynchronizeFollowingMessages(t *testing.T) {
	oversized := strings.Repeat("x", maxMCPMessageBytes+1)
	valid := `{"jsonrpc":"2.0","id":2,"method":"server/discover","params":{` + mcpTestMeta + `}}`
	responses, _ := runMCP(t, fakeRegistry(t), oversized+"\n"+valid+"\n")
	if responses["null"].Error == nil || responses["null"].Error.Code != mcpParseError {
		t.Fatalf("oversized response = %+v", responses["null"])
	}
	if len(responses["2"].Result) == 0 {
		t.Fatal("valid message after oversized input was not served")
	}
}

func TestMCPInFlightLimitStopsBeforeCore(t *testing.T) {
	var stdout, stderr bytes.Buffer
	server := newMCPServer(&Options{}, fakeRegistry(t), &stdout, &stderr)
	for i := 0; i < maxMCPInFlight; i++ {
		server.pending[fmt.Sprintf("%d", i)] = &mcpPending{}
	}
	line := `{"jsonrpc":"2.0","id":"overflow","method":"tools/call","params":{` + mcpTestMeta +
		`,"name":"qatlas.search","arguments":{}}}`
	server.handle(context.Background(), []byte(line))
	responses := decodeMCPResponses(t, stdout.String())
	response := responses[`"overflow"`]
	if response.Error == nil || response.Error.Code != mcpInternalError {
		t.Fatalf("overflow response = %+v", response)
	}
	if stderr.Len() != 0 || len(server.pending) != maxMCPInFlight {
		t.Fatalf("stderr=%q pending=%d", stderr.String(), len(server.pending))
	}
}

func TestMCPServeReturnsStdoutWriteError(t *testing.T) {
	want := errors.New("synthetic stdout failure")
	server := newMCPServer(&Options{}, fakeRegistry(t), mcpFailWriter{err: want}, io.Discard)
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{` + mcpTestMeta + `}}` + "\n"
	if err := server.serve(context.Background(), strings.NewReader(input)); !errors.Is(err, want) {
		t.Fatalf("serve() error = %v, want wrapped %v", err, want)
	}
}

type decodedMCPResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *mcpRPCError    `json:"error"`
}

type mcpToolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Structured json.RawMessage `json:"structuredContent"`
	IsError    bool            `json:"isError"`
}

// Every failed tool call carries structuredContent with at least the code and the message of its text, so a
// caller branches on the object alone, including where no code adds a detail and on the deadline.
func TestMCPToolErrorsCarryCodeAndMessage(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	t.Setenv("QATLAS_CREDENTIAL_STORE", "none")
	failing := func(err error) func(t *testing.T) (*capability.Registry, string) {
		return func(t *testing.T) (*capability.Registry, string) {
			return mcpTestRegistry(t, func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
				json.RawMessage) (any, error) {
				return nil, err
			})
		}
	}
	waiting := func(t *testing.T) (*capability.Registry, string) {
		return mcpTestRegistry(t, func(ctx context.Context, _ *config.Resolved, _ *secret.Resolver,
			_ *redact.Redactor, _ json.RawMessage) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})
	}
	unused := failing(errors.New("handler must not run"))
	for _, tt := range []struct {
		code      output.Code
		registry  func(t *testing.T) (*capability.Registry, string)
		tool      string
		arguments string
		timeout   bool
	}{
		{output.CodeInvalidRequest, unused, "qatlas.search", `{"cursor":"bogus"}`, false},
		{output.CodeUnknownOperation, unused, "qatlas.describe", `{"operation":"absent.operation.get"}`, false},
		{output.CodeUnknownConnection, unused, "qatlas.describe", `{"operation":"fake.pages.get","connection":"absent"}`, false},
		{output.CodeConfirmationRequired, mcpSendRegistry, "qatlas.invoke",
			`{"operation":"fake.messages.send","connection":"target","arguments":{"text":"hello"}}`, false},
		{output.CodeNotFound, failing(&provider.Error{Class: provider.ClassNotFound, Op: "get page", Message: "absent"}),
			"qatlas.invoke", `{"operation":"fake.pages.get","connection":"primary","arguments":{}}`, false},
		{output.CodeProviderError, failing(&provider.Error{Class: provider.ClassProviderError, Op: "get page", Message: "failed"}),
			"qatlas.invoke", `{"operation":"fake.pages.get","connection":"primary","arguments":{}}`, false},
		{output.CodeTimeout, waiting, "qatlas.invoke",
			`{"operation":"fake.pages.get","connection":"primary","arguments":{}}`, true},
	} {
		t.Run(string(tt.code), func(t *testing.T) {
			registry, path := tt.registry(t)
			var stdout, stderr bytes.Buffer
			server := newMCPServer(&Options{
				Config: path, Redactor: &redact.Redactor{}, Secrets: secret.NewWith(nil, nil, nil, nil),
			}, registry, &stdout, &stderr)
			if tt.timeout {
				server.timeout = 20 * time.Millisecond
			}
			input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta + `,"name":"` + tt.tool +
				`","arguments":` + tt.arguments + `}}` + "\n"
			if err := server.serve(context.Background(), strings.NewReader(input)); err != nil {
				t.Fatal(err)
			}
			result := toolResultFrom(t, decodeMCPResponses(t, stdout.String())["1"])
			var detail struct {
				Code    output.Code `json:"code"`
				Message string      `json:"message"`
			}
			if !result.IsError || result.Structured == nil {
				t.Fatalf("result = %+v, want a tool error with structuredContent", result)
			}
			decodeRaw(t, result.Structured, &detail)
			if detail.Code != tt.code || detail.Message == "" ||
				result.Content[0].Text != string(detail.Code)+": "+detail.Message {
				t.Fatalf("structuredContent = %s with text %q, want code %s and the message of the text",
					result.Structured, result.Content[0].Text, tt.code)
			}
		})
	}
}

func runMCP(t *testing.T, registry *capability.Registry, input string) (map[string]decodedMCPResponse, string) {
	t.Helper()
	return runMCPWithOptions(t, registry, input, &Options{Redactor: &redact.Redactor{}})
}

func runMCPWithOptions(t *testing.T, registry *capability.Registry, input string,
	options *Options) (map[string]decodedMCPResponse, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	server := newMCPServer(options, registry, &stdout, &stderr)
	if err := server.serve(context.Background(), strings.NewReader(input)); err != nil {
		t.Fatalf("serve() = %v", err)
	}
	return decodeMCPResponses(t, stdout.String()), stderr.String()
}

func decodeMCPResponses(t *testing.T, text string) map[string]decodedMCPResponse {
	t.Helper()
	responses := map[string]decodedMCPResponse{}
	decoder := json.NewDecoder(strings.NewReader(text))
	for {
		var response decodedMCPResponse
		if err := decoder.Decode(&response); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decode MCP response from %q: %v", text, err)
		}
		responses[string(response.ID)] = response
	}
	return responses
}

func toolResultFrom(t *testing.T, response decodedMCPResponse) mcpToolResult {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("unexpected protocol error: %+v", response.Error)
	}
	var result mcpToolResult
	decodeRaw(t, response.Result, &result)
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("tool content = %+v", result.Content)
	}
	return result
}

func mcpTestRegistry(t *testing.T, handler capability.Handler) (*capability.Registry, string) {
	t.Helper()
	registry := capability.NewRegistry()
	if err := registry.RegisterProvider(config.ProviderMetadata{ID: "fake", Name: "Fake"}, nil); err != nil {
		t.Fatal(err)
	}
	descriptor := capability.Descriptor{
		ID: "fake.pages.get", Version: 1, Description: "Read a fake page", Provider: "fake",
		Risk: capability.Risk{
			Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
			Confirmation: capability.ConfirmationNone, DataSensitivity: "test",
		},
		InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	}
	if err := registry.Register("fake", capability.Operation{Descriptor: descriptor, Handler: handler}); err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, `version: 1
services:
  fake:
    provider: fake
    base_url: https://example.invalid
credentials:
  reader:
    type: keyring
connections:
  primary:
    service: fake
    credential: reader
defaults: {}
`)
	return registry, path
}

func decodeRaw(t *testing.T, raw json.RawMessage, target any) {
	t.Helper()
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
}

func objectField(t *testing.T, raw json.RawMessage, name string) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	decodeRaw(t, raw, &object)
	var field map[string]json.RawMessage
	decodeRaw(t, object[name], &field)
	return field
}

func stringFieldSlice(t *testing.T, raw json.RawMessage, name string) []string {
	t.Helper()
	var object map[string]json.RawMessage
	decodeRaw(t, raw, &object)
	var field []string
	decodeRaw(t, object[name], &field)
	return field
}

func encodeResponses(t *testing.T, responses map[string]decodedMCPResponse) string {
	t.Helper()
	encoded, err := json.Marshal(responses)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// runFakeCLIJSON runs one public CLI command with the fake registry and returns its JSON document.
func runFakeCLIJSON(t *testing.T, input string, args ...string) []byte {
	t.Helper()
	code, stdout, stderr := runFakeCLIInput(t, input, args...)
	if code != exitOK || stderr != "" {
		t.Fatalf("CLI %v exit=%d stderr=%q", args, code, stderr)
	}
	return []byte(stdout)
}

// assertMCPParity proves that the public tool CLI and the fixed MCP broker tool publish the same data. The
// CLI document names it in the tool taxonomy, the broker in the core contract; the payload is identical.
func assertMCPParity(t *testing.T, cliDocument []byte, cliKey string, mcpDocument json.RawMessage, mcpKey string) {
	t.Helper()
	cliValue, mcpValue := jsonMember(t, cliDocument, cliKey), jsonMember(t, mcpDocument, mcpKey)
	if !jsonEqual(cliValue, mcpValue) {
		t.Fatalf("CLI %q = %s, MCP %q = %s", cliKey, cliValue, mcpKey, mcpValue)
	}
}

func jsonMember(t *testing.T, document json.RawMessage, key string) json.RawMessage {
	t.Helper()
	if key == "" {
		return document
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(document, &members); err != nil {
		t.Fatalf("document %s is not a JSON object: %v", document, err)
	}
	member, ok := members[key]
	if !ok {
		t.Fatalf("document %s has no member %q", document, key)
	}
	return member
}

type mcpFailWriter struct{ err error }

func (w mcpFailWriter) Write([]byte) (int, error) { return 0, w.err }

// A transport failure reaches an agent through two doors, and both must publish the same thing: the same
// code, the same message, and the same stable cause. Nothing of the request may travel with it.
func TestMCPAndCLITransportDiagnosisParity(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	t.Setenv("QATLAS_CREDENTIAL_STORE", "none")
	const (
		tokenCanary = "bot-token-canary-5517"
		hostCanary  = "wiki.internal.example"
		osCanary    = "raw-os-canary-9134"
	)
	// The chain is what net/http hands back: the request URL with its credential, the socket operation,
	// and the resolver failure underneath.
	transport := &url.Error{
		Op: "Get", URL: "https://" + tokenCanary + "@" + hostCanary + "/api/pages",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{
			Err: osCanary, Name: hostCanary, IsNotFound: true,
		}},
	}
	registry, path := mcpTestRegistry(t, func(context.Context, *config.Resolved, *secret.Resolver,
		*redact.Redactor, json.RawMessage) (any, error) {
		return nil, provider.Transport("read page", "Fake", transport)
	})

	var stdout, stderr bytes.Buffer
	redactor := &redact.Redactor{}
	redactor.Add(tokenCanary)
	opts := &Options{Input: strings.NewReader(""), Redactor: redactor, Config: path}
	code := run(newRootCommand(opts, registry), opts,
		[]string{"invoke", "fake.pages.get", "--connection", "primary", "--config", path}, &stdout, &stderr)
	if code != exitRuntime || stdout.Len() != 0 {
		t.Fatalf("CLI exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	cliDiagnostic := strings.TrimPrefix(strings.TrimSpace(stderr.String()), "qatlas: ")

	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta +
		`,"name":"qatlas.invoke","arguments":{"operation":"fake.pages.get","connection":"primary"}}}` + "\n"
	responses, mcpStderr := runMCPWithOptions(t, registry, input,
		&Options{Config: path, Redactor: redactor})
	result := toolResultFrom(t, responses["1"])
	if !result.IsError {
		t.Fatalf("MCP result = %+v, want a tool error", result)
	}

	if result.Content[0].Text != cliDiagnostic {
		t.Fatalf("MCP diagnosis = %q, CLI diagnosis = %q", result.Content[0].Text, cliDiagnostic)
	}
	// The class stays the compatible unreachable, and the cause is what a DNS failure now adds to it.
	if !strings.HasPrefix(cliDiagnostic, string(output.CodeUnreachable)+": ") ||
		!strings.HasSuffix(cliDiagnostic, "(cause: "+string(provider.CauseDNS)+")") {
		t.Fatalf("diagnosis = %q, want an unreachable class with the dns cause", cliDiagnostic)
	}
	for _, canary := range []string{tokenCanary, hostCanary, osCanary, "/api/pages"} {
		if strings.Contains(cliDiagnostic+stderr.String()+encodeResponses(t, responses)+mcpStderr, canary) {
			t.Errorf("the diagnosis or the audit leaks the canary %q", canary)
		}
	}
}

// A misspelled tool, connection, or provider is refused with the closest configured or registered name, in
// the same words on the command line and over MCP. A name that resembles nothing gets no suggestion.
func TestMisspelledNamesSuggestTheClosestOneOverCLIAndMCP(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	registry, path := mcpTestRegistry(t, func(context.Context, *config.Resolved, *secret.Resolver,
		*redact.Redactor, json.RawMessage) (any, error) {
		return map[string]any{}, nil
	})
	cli := func(args ...string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		redactor := &redact.Redactor{}
		opts := &Options{Input: strings.NewReader(""), Redactor: redactor,
			Secrets: secret.NewWith(nil, nil, nil, redactor)}
		code := run(newRootCommand(opts, registry), opts, append(args, "--config", path), &stdout, &stderr)
		if code != exitUsage || stdout.Len() != 0 {
			t.Fatalf("%v: exit=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
		first, _, _ := strings.Cut(stderr.String(), "\n")
		return first
	}
	mcp := func(tool, arguments string) string {
		t.Helper()
		input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"` + tool + `","arguments":` + arguments + `}}` + "\n"
		responses, _ := runMCPWithOptions(t, registry, input, &Options{Config: path, Redactor: &redact.Redactor{}})
		result := toolResultFrom(t, responses["1"])
		if !result.IsError {
			t.Fatalf("%s %s = %+v, want a tool error", tool, arguments, result)
		}
		return result.Content[0].Text
	}

	for _, tt := range []struct{ cli, mcp, want string }{
		{cli("describe", "fake.page.get"), mcp("qatlas.describe", `{"operation":"fake.page.get"}`),
			`unknown-operation: unknown tool "fake.page.get" (did you mean "fake.pages.get"?)`},
		{cli("invoke", "fake.pages.get", "--connection", "primry"),
			mcp("qatlas.invoke", `{"operation":"fake.pages.get","connection":"primry"}`),
			`unknown-connection: unknown connection "primry" (did you mean "primary"?)`},
		{cli("describe", "zzz.unrelated.tool"), mcp("qatlas.describe", `{"operation":"zzz.unrelated.tool"}`),
			`unknown-operation: unknown tool "zzz.unrelated.tool"`},
	} {
		if tt.cli != "qatlas: "+tt.want || tt.mcp != tt.want {
			t.Errorf("CLI = %q, MCP = %q, want %q", tt.cli, tt.mcp, tt.want)
		}
	}

	if got, want := cli("tools", "fak"), `qatlas: usage: unknown tool namespace "fak" (did you mean "fake"?); `+
		`run 'qatlas providers' to list the namespaces`; got != want {
		t.Errorf("CLI tools = %q, want %q", got, want)
	}
	if got, want := cli("connections", "fak"), `qatlas: usage: unknown provider "fak" (did you mean "fake"?); `+
		`run 'qatlas providers' to list the providers`; got != want {
		t.Errorf("CLI connections = %q, want %q", got, want)
	}
	if got, want := mcp("qatlas.search", `{"provider":"fak"}`), `invalid-request: unknown provider "fak" `+
		`(did you mean "fake"?); leave the provider out to search every provider`; got != want {
		t.Errorf("MCP search = %q, want %q", got, want)
	}
}
