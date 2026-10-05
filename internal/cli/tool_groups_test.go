package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// groupedRegistry has a bookstack provider with the groups alpha, beta, and gamma. alpha holds alphaTools
// read tools, beta holds betaRead read tools and betaCreate create tools, which the read-only connection
// does not offer, and gamma holds none.
func groupedRegistry(t *testing.T, alphaTools, betaRead, betaCreate int) *capability.Registry {
	t.Helper()
	registry := capability.NewRegistry()
	if err := registry.RegisterProvider(config.ProviderMetadata{
		ID: "bookstack", Name: "BookStack", DefaultPermissions: []config.Permission{config.PermissionRead},
		SecretRoles: []config.SecretRole{{Name: "token-id"}, {Name: "token-secret"}},
		Target:      config.TargetMetadata{Label: "target"},
		Groups: []config.ToolGroup{
			{ID: "beta", Title: "Beta", Description: "second area"},
			{ID: "alpha", Title: "Alpha", Description: "first area"},
			{ID: "gamma", Title: "Gamma", Description: "empty area"},
		},
	}, nil); err != nil {
		t.Fatal(err)
	}
	add := func(name, group string, effect capability.Effect) {
		confirmation := capability.ConfirmationNone
		if effect != capability.EffectRead {
			confirmation = capability.ConfirmationRequired
		}
		err := registry.Register("bookstack", capability.Operation{
			Descriptor: capability.Descriptor{
				ID: "bookstack." + name + ".run", Version: 1, Description: "Synthetic " + name,
				Provider: "bookstack", Group: group,
				Risk: capability.Risk{Effect: effect, Idempotency: capability.IdempotencySafe,
					Confirmation: confirmation, DataSensitivity: "test"},
				InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"object"}`),
			},
			Handler: func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
				json.RawMessage) (any, error) {
				return map[string]any{}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < alphaTools; i++ {
		add(fmt.Sprintf("a%02d", i), "alpha", capability.EffectRead)
	}
	for i := 0; i < betaRead; i++ {
		add(fmt.Sprintf("b%02d", i), "beta", capability.EffectRead)
	}
	for i := 0; i < betaCreate; i++ {
		add(fmt.Sprintf("c%02d", i), "beta", capability.EffectCreate)
	}
	return registry
}

func groupedTools(t *testing.T, registry *capability.Registry, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	t.Setenv("QATLAS_CREDENTIAL_STORE", "none")
	path := writeConfig(t, validConfig)
	var stdout, stderr bytes.Buffer
	options := &Options{Redactor: &redact.Redactor{}}
	code := run(newRootCommand(options, registry), options,
		append(append([]string{"tools"}, args...), "--config", path, "--output", "json"), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func groupedMCPSearch(t *testing.T, registry *capability.Registry, arguments string) mcpToolResult {
	t.Helper()
	path := writeConfig(t, validConfig)
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta +
		`,"name":"qatlas.search","arguments":` + arguments + `}}` + "\n"
	responses, stderr := runMCPWithOptions(t, registry, input, &Options{Config: path, Redactor: &redact.Redactor{}})
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
	return toolResultFrom(t, responses["1"])
}

func TestToolsGroupsAboveTheThresholdMatchBetweenCLIAndMCP(t *testing.T) {
	t.Setenv("QATLAS_CREDENTIAL_STORE", "none")
	registry := groupedRegistry(t, 40, 15, 5)

	code, stdout, stderr := groupedTools(t, registry, "bookstack")
	if code != exitOK {
		t.Fatalf("tools exit=%d stderr=%q", code, stderr)
	}
	var cli application.ToolsResponse
	if err := json.Unmarshal([]byte(stdout), &cli); err != nil {
		t.Fatal(err)
	}
	if len(cli.Tools) != 0 || len(cli.Groups) != 2 || cli.Groups[0].Group != "alpha" || cli.Groups[1].Group != "beta" {
		t.Fatalf("groups = %+v tools = %d, want alpha and beta, no tools", cli.Groups, len(cli.Tools))
	}
	if cli.Groups[0].Tools != 40 || cli.Groups[1].Tools != 15 || cli.Groups[0].Offered != nil {
		t.Fatalf("counts = %+v, want 40 and 15 without offered", cli.Groups)
	}
	if cli.Groups[0].Title != "Alpha" || cli.Groups[0].Description != "first area" {
		t.Fatalf("group text = %+v", cli.Groups[0])
	}
	if want := "qatlas: 55 tools in 2 groups; 'qatlas tools bookstack <group>' lists one group, --query searches all\n"; stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}

	result := groupedMCPSearch(t, registry, `{"provider":"bookstack"}`)
	if result.IsError {
		t.Fatalf("search error: %s", result.Content[0].Text)
	}
	if strings.Contains(string(result.Structured), `"operations"`) || strings.Contains(string(result.Structured), `"has_more"`) {
		t.Fatalf("grouped search carries operations or paging: %s", result.Structured)
	}
	var mcp application.SearchResponse
	decodeRaw(t, result.Structured, &mcp)
	if fmt.Sprint(mcp.Groups) != fmt.Sprint(cli.Groups) || fmt.Sprint(mcp.Connections) != fmt.Sprint(cli.Connections) {
		t.Fatalf("MCP %+v %v differs from CLI %+v %v", mcp.Groups, mcp.Connections, cli.Groups, cli.Connections)
	}
}

func TestToolsGroupsAllAddsOffered(t *testing.T) {
	registry := groupedRegistry(t, 40, 15, 5)
	code, stdout, stderr := groupedTools(t, registry, "bookstack", "--all")
	if code != exitOK {
		t.Fatalf("tools exit=%d stderr=%q", code, stderr)
	}
	var cli application.ToolsResponse
	if err := json.Unmarshal([]byte(stdout), &cli); err != nil {
		t.Fatal(err)
	}
	if len(cli.Groups) != 2 || cli.Groups[0].Tools != 40 || *cli.Groups[0].Offered != 40 ||
		cli.Groups[1].Tools != 20 || *cli.Groups[1].Offered != 15 {
		t.Fatalf("groups = %+v, want alpha 40/40 and beta 20/15", cli.Groups)
	}
	if !strings.HasPrefix(stderr, "qatlas: 60 tools in 2 groups;") {
		t.Fatalf("stderr = %q", stderr)
	}
	result := groupedMCPSearch(t, registry, `{"provider":"bookstack","all":true}`)
	var mcp application.SearchResponse
	decodeRaw(t, result.Structured, &mcp)
	if len(mcp.Groups) != 2 || mcp.Groups[1].Tools != 20 || *mcp.Groups[1].Offered != 15 {
		t.Fatalf("MCP groups = %+v", mcp.Groups)
	}
}

func TestToolsOneGroupListsItsToolsAndMatchesMCP(t *testing.T) {
	registry := groupedRegistry(t, 40, 15, 5)
	code, stdout, stderr := groupedTools(t, registry, "bookstack", "beta")
	if code != exitOK || stderr != "" {
		t.Fatalf("tools exit=%d stderr=%q", code, stderr)
	}
	cli := toolIndex(t, stdout)
	if len(cli.Tools) != 15 || len(cli.Groups) != 0 || !strings.HasPrefix(cli.Tools[0].ID, "bookstack.b") {
		t.Fatalf("beta tools = %d groups = %d", len(cli.Tools), len(cli.Groups))
	}
	result := groupedMCPSearch(t, registry, `{"provider":"bookstack","group":"beta"}`)
	var mcp application.SearchResponse
	decodeRaw(t, result.Structured, &mcp)
	if len(mcp.Groups) != 0 || mcp.HasMore || fmt.Sprint(mcp.Operations) != fmt.Sprint(cli.Tools) {
		t.Fatalf("MCP operations differ from CLI: %v vs %v", mcp.Operations, cli.Tools)
	}
}

func TestToolsQueryStaysFlatAndCombinesWithAGroup(t *testing.T) {
	registry := groupedRegistry(t, 40, 15, 5)
	_, stdout, _ := groupedTools(t, registry, "bookstack", "--query", "synthetic")
	flat := toolIndex(t, stdout)
	if len(flat.Tools) != 55 || len(flat.Groups) != 0 {
		t.Fatalf("query answered %d tools, %d groups, want 55 tools", len(flat.Tools), len(flat.Groups))
	}
	_, stdout, _ = groupedTools(t, registry, "bookstack", "alpha", "--query", "a07")
	if scoped := toolIndex(t, stdout); len(scoped.Tools) != 1 || scoped.Tools[0].ID != "bookstack.a07.run" {
		t.Fatalf("group with query = %+v", scoped.Tools)
	}
	result := groupedMCPSearch(t, registry, `{"provider":"bookstack","query":"synthetic"}`)
	var mcp application.SearchResponse
	decodeRaw(t, result.Structured, &mcp)
	if len(mcp.Groups) != 0 || len(mcp.Operations) != 50 || !mcp.HasMore {
		t.Fatalf("MCP query = %d operations, %d groups, has_more=%t", len(mcp.Operations), len(mcp.Groups), mcp.HasMore)
	}
}

func TestToolsAtTheThresholdStayFlat(t *testing.T) {
	registry := groupedRegistry(t, 40, 10, 0)
	_, stdout, stderr := groupedTools(t, registry, "bookstack")
	if flat := toolIndex(t, stdout); len(flat.Tools) != 50 || len(flat.Groups) != 0 || stderr != "" {
		t.Fatalf("tools = %d groups = %d stderr = %q, want a flat list of 50", len(flat.Tools), len(flat.Groups), stderr)
	}
	// The filters apply before the threshold: --all lists the same tools plus none here, the beta group
	// of a bigger catalog drops below it once --connection or the group narrows it.
	registry = groupedRegistry(t, 40, 15, 5)
	_, stdout, _ = groupedTools(t, registry, "bookstack", "alpha")
	if flat := toolIndex(t, stdout); len(flat.Tools) != 40 || len(flat.Groups) != 0 {
		t.Fatalf("alpha = %d tools, %d groups", len(flat.Tools), len(flat.Groups))
	}
	result := groupedMCPSearch(t, registry, `{"provider":"bookstack","limit":10}`)
	var paged application.SearchResponse
	decodeRaw(t, result.Structured, &paged)
	if len(paged.Groups) != 0 || len(paged.Operations) != 10 || !paged.HasMore {
		t.Fatalf("paged search = %d operations, %d groups", len(paged.Operations), len(paged.Groups))
	}
}

func TestToolsGroupErrors(t *testing.T) {
	registry := groupedRegistry(t, 40, 15, 5)
	code, _, stderr := groupedTools(t, registry, "bookstack", "alph")
	if code != exitUsage || !strings.Contains(stderr, `unknown group "alph"`) ||
		!strings.Contains(stderr, `did you mean "alpha"?`) {
		t.Fatalf("unknown group: exit=%d stderr=%q", code, stderr)
	}
	code, _, stderr = groupedTools(t, fakeRegistry(t), "bookstack", "alpha")
	if code != exitUsage || !strings.Contains(stderr, "has no groups") {
		t.Fatalf("provider without groups: exit=%d stderr=%q", code, stderr)
	}
	if code, _, _ = groupedTools(t, registry, "bookstack", "alpha", "extra"); code != exitUsage {
		t.Fatalf("three arguments: exit=%d, want usage", code)
	}

	for arguments, want := range map[string]string{
		`{"provider":"bookstack","group":"alph"}`: `did you mean "alpha"?`,
		`{"group":"alpha"}`:                       "group needs provider",
	} {
		result := groupedMCPSearch(t, registry, arguments)
		var failure struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		decodeRaw(t, result.Structured, &failure)
		if !result.IsError || failure.Code != "invalid-request" || !strings.Contains(failure.Message, want) {
			t.Fatalf("%s = %+v, want invalid-request containing %q", arguments, failure, want)
		}
	}
	result := groupedMCPSearch(t, fakeRegistry(t), `{"provider":"bookstack","group":"alpha"}`)
	if !result.IsError || !strings.Contains(result.Content[0].Text, "has no groups") {
		t.Fatalf("MCP provider without groups = %+v", result)
	}
}

func TestToolsProviderWithoutGroupsStaysFlatAboveTheThreshold(t *testing.T) {
	registry := fakeRegistry(t)
	addSyntheticOperations(t, registry, 60)
	_, stdout, stderr := groupedTools(t, registry, "bookstack")
	if flat := toolIndex(t, stdout); len(flat.Tools) != 62 || len(flat.Groups) != 0 || stderr != "" {
		t.Fatalf("tools = %d groups = %d stderr = %q", len(flat.Tools), len(flat.Groups), stderr)
	}
}

func TestToolsGroupsAreATOONTable(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	t.Setenv("QATLAS_CREDENTIAL_STORE", "none")
	path := writeConfig(t, validConfig)
	registry := groupedRegistry(t, 40, 15, 5)
	for args, header := range map[string]string{
		"":      "groups[2]{group,title,description,tools}:\n  alpha,Alpha,first area,40\n  beta,Beta,second area,15\n",
		"--all": "groups[2]{group,title,description,tools,offered}:\n  alpha,Alpha,first area,40,40\n  beta,Beta,second area,20,15\n",
	} {
		var stdout, stderr bytes.Buffer
		options := &Options{Redactor: &redact.Redactor{}}
		command := []string{"tools", "bookstack", "--config", path}
		if args != "" {
			command = append(command, args)
		}
		if code := run(newRootCommand(options, registry), options, command, &stdout, &stderr); code != exitOK {
			t.Fatalf("exit=%d stderr=%q", code, stderr.String())
		}
		if !strings.HasSuffix(stdout.String(), header) {
			t.Fatalf("output = %q, want suffix %q", stdout.String(), header)
		}
	}
}

func TestGitHubToolsListGroupsBeforeTools(t *testing.T) {
	path := githubConfig(t)
	var reads atomic.Int32
	code, stdout, stderr := runTwentyCLI(t, &reads, "", "tools", "github", "--config", path)
	if code != exitOK || !strings.Contains(stdout, "groups[12]{group,title,description,tools}:\n") ||
		strings.Contains(stdout, "tools[") || !strings.Contains(stderr, "tools in 12 groups; 'qatlas tools github <group>'") {
		t.Fatalf("exit=%d stderr=%q stdout:\n%s", code, stderr, stdout)
	}
	code, stdout, stderr = runTwentyCLI(t, &reads, "", "tools", "github", "gists", "--config", path)
	if code != exitOK || stderr != "" || !strings.Contains(stdout, "github.gists.list") ||
		strings.Contains(stdout, "github.issues") {
		t.Fatalf("gists: exit=%d stderr=%q stdout:\n%s", code, stderr, stdout)
	}
	if reads.Load() != 0 {
		t.Errorf("secret lookups = %d, want 0", reads.Load())
	}
}
