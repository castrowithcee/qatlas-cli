package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// catalogEnvironment configures one BookStack and one Telegram connection against local servers that fail
// the test if discovery ever calls them. Discovery is a local catalog view: it must answer from the
// configuration alone.
func catalogEnvironment(t *testing.T) (string, *atomic.Int32, string) {
	t.Helper()
	const providerCanary = "provider-body-canary-4b71"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"data":[{"id":1,"name":"` + providerCanary + `"}],"total":1}`))
	}))
	t.Cleanup(server.Close)

	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	cfg := writeConfig(t, fmt.Sprintf(`version: 1
services:
  wiki:
    provider: bookstack
    base_url: %s
  telegram:
    provider: telegram
    base_url: %s
credentials:
  reader:
    type: env
    values:
      token-id: TOOLS_TOKEN_ID
      token-secret: TOOLS_TOKEN_SECRET
  bot:
    type: env
    values:
      bot-token: TOOLS_BOT_TOKEN
connections:
  wiki:
    service: wiki
    credential: reader
    description: read-only account on the team wiki
  alerts:
    service: telegram
    credential: bot
    target: "-1009900112233"
provider_notes:
  bookstack: company handbook
defaults: {}
`, server.URL, server.URL))
	return cfg, &calls, providerCanary
}

// runTools drives the real command tree and the shipped provider registry with a credential resolver that
// counts every secret lookup, so a discovery run can prove it read none.
func runTools(t *testing.T, reads *atomic.Int32, args ...string) (int, string, string) {
	t.Helper()
	redactor := &redact.Redactor{}
	lookup := func(string) string {
		if reads != nil {
			reads.Add(1)
		}
		return ""
	}
	var stdout, stderr bytes.Buffer
	opts := &Options{
		Input: strings.NewReader(""), Redactor: redactor,
		Secrets: secret.NewWith(lookup, nil, nil, redactor),
	}
	code := run(newRootCommand(opts, defaultRegistry()), opts, args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// toonOfJSON renders a JSON document with the encoder that the TOON specification tests verify. A command
// whose TOON output equals this rendering carries exactly the data of its JSON output.
func toonOfJSON(t *testing.T, document string) string {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(document))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("output is not valid JSON: %v: %s", err, document)
	}
	encoded, err := output.MarshalTOON(value)
	if err != nil {
		t.Fatalf("MarshalTOON() = %v", err)
	}
	return string(encoded) + "\n"
}

func jsonEqual(a, b []byte) bool {
	var first, second any
	return json.Unmarshal(a, &first) == nil && json.Unmarshal(b, &second) == nil &&
		reflect.DeepEqual(first, second)
}

// toolSummaries decodes the compact index. Decoding into the core type is what proves that the command
// publishes that model and nothing else: a stray field would fail the strict decoder below.
func toolSummaries(t *testing.T, stdout string) []application.ToolSummary {
	t.Helper()
	var document struct {
		Tools []application.ToolSummary `json:"tools"`
	}
	decoder := json.NewDecoder(strings.NewReader(stdout))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("tools output is not the compact index: %v: %s", err, stdout)
	}
	return document.Tools
}

func toolIDs(t *testing.T, stdout string) []string {
	t.Helper()
	summaries := toolSummaries(t, stdout)
	ids := make([]string, len(summaries))
	for i, tool := range summaries {
		ids[i] = tool.ID
	}
	return ids
}

// providerSummaries decodes the namespace index. Decoding into the core type is what proves that the
// command publishes that model and nothing else: a stray field would fail the strict decoder.
func providerSummaries(t *testing.T, stdout string) []application.ProviderSummary {
	t.Helper()
	var document struct {
		Providers []application.ProviderSummary `json:"providers"`
	}
	decoder := json.NewDecoder(strings.NewReader(stdout))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("providers output is not the namespace index: %v: %s", err, stdout)
	}
	return document.Providers
}

// Acceptance 1: discovery starts with the namespaces. The list is a deterministic local TOON table of one
// row per provider, produced without any provider I/O, and it stays one row per provider however many
// tools that provider offers.
func TestProvidersListsTheNamespacesAsTOON(t *testing.T) {
	cfg, calls, _ := catalogEnvironment(t)

	code, stdout, stderr := runTools(t, nil, "providers", "--config", cfg)
	if code != exitOK || stderr != "" {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "providers[8]{connections,description,note,provider,tools}:\n") ||
		!strings.HasSuffix(stdout, "\n") || strings.Contains(stdout, "\r") {
		t.Errorf("stdout = %q, want an LF TOON table of eight namespace rows", stdout)
	}
	for _, want := range []string{
		// BookStack has exactly one configured connection, Telegram one, and every other compiled
		// provider none. An unconfigured namespace stays visible with zero. Every row carries the
		// provider's description, and the note the configuration keeps, which is empty where there is
		// none.
		"1,Self-hosted documentation platform for team knowledge,company handbook,bookstack,5",
		"1,Cloud-based instant messaging service,\"\",telegram,3",
		",\"\",github,57", ",\"\",lexware,3", ",\"\",nextcloud,6", ",\"\",seatable,7", ",\"\",todoist,39",
		",\"\",twentycrm,5", "0,Code hosting and software collaboration platform,",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not contain %q:\n%s", want, stdout)
		}
	}
	// The first step names namespaces, not tools: no ID, no title, and no connection name reaches it.
	for _, absent := range []string{".", "title", "effect", "wiki", "alerts"} {
		if strings.Contains(stdout, absent) {
			t.Errorf("the namespace index published %q:\n%s", absent, stdout)
		}
	}
	for i := 0; i < 3; i++ {
		if _, repeat, _ := runTools(t, nil, "providers", "--config", cfg); repeat != stdout {
			t.Fatalf("run %d = %q, want %q", i+2, repeat, stdout)
		}
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("provider calls = %d, want 0", got)
	}

	t.Run("the TOON default and --output json carry the same index", func(t *testing.T) {
		code, jsonOut, stderr := runTools(t, nil, "providers", "--config", cfg, "--output", "json")
		if code != exitOK || stderr != "" {
			t.Fatalf("exit=%d stderr=%q", code, stderr)
		}
		if got := toonOfJSON(t, jsonOut); got != stdout {
			t.Errorf("TOON output = %q, want the TOON rendering of the JSON data %q", stdout, got)
		}
		for _, provider := range providerSummaries(t, jsonOut) {
			want := 0
			switch provider.Provider {
			case "bookstack", "telegram":
				want = 1
			}
			if provider.Connections != want {
				t.Errorf("%s connections = %d, want %d", provider.Provider, provider.Connections, want)
			}
		}
	})

	t.Run("a connection that offers no tool is listed but not counted, as the help says", func(t *testing.T) {
		empty := writeConfig(t, `version: 1
services:
  telegram:
    provider: telegram
    base_url: https://telegram.example.invalid
credentials:
  bot:
    type: env
    values:
      bot-token: TOOLS_BOT_TOKEN
connections:
  alerts:
    service: telegram
    credential: bot
    target: "-1009900112233"
    tools: []
defaults: {}
`)
		code, stdout, stderr := runTools(t, nil, "providers", "--config", empty)
		if code != exitOK || stderr != "" || !strings.Contains(stdout, `0,Cloud-based instant messaging service,"",telegram,3`) {
			t.Errorf("providers: exit=%d stdout=%q stderr=%q, want telegram counted with zero", code, stdout, stderr)
		}
		code, stdout, stderr = runTools(t, nil, "connections", "telegram", "--config", empty)
		if code != exitOK || stderr != "" || !strings.Contains(stdout, "alerts") {
			t.Errorf("connections: exit=%d stdout=%q stderr=%q, want alerts listed", code, stdout, stderr)
		}
		_, help, _ := runTools(t, nil, "providers", "--help")
		if !strings.Contains(help, "A connection counts when it offers at least one tool of the provider") ||
			!strings.Contains(help, "('tools: []')") {
			t.Errorf("providers help does not explain the count:\n%s", help)
		}
	})

	t.Run("an argument is a usage error", func(t *testing.T) {
		code, _, stderr := runTools(t, nil, "providers", "bookstack", "--config", cfg)
		if code != exitUsage || stderr == "" {
			t.Errorf("exit=%d stderr=%q", code, stderr)
		}
	})
}

// Acceptance 1b: the second step lists the tools of one namespace that a connection offers, as a
// deterministic local TOON table. Each entry says what the tool is, whether it changes anything, and which
// connections offer it; schemas, tags and route descriptions stay in the document that answers for exactly
// one tool.
func TestToolsListsOneNamespaceAsTOON(t *testing.T) {
	cfg, calls, _ := catalogEnvironment(t)

	code, stdout, stderr := runTools(t, nil, "tools", "bookstack", "--config", cfg)
	if code != exitOK || stderr != "" {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	// The wiki connection keeps the read-only default permissions, so it offers the two read tools only.
	if want := "tools[2]{connections,effect,id,title}:\n" +
		"  wiki,read,bookstack.pages.get,Get a BookStack page\n" +
		"  wiki,read,bookstack.pages.list,List BookStack pages\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
	for _, absent := range []string{"description", "tags", "version", "reason", "alerts", "read-only account"} {
		if strings.Contains(stdout, absent) {
			t.Errorf("the namespace listing published %q:\n%s", absent, stdout)
		}
	}
	for i := 0; i < 3; i++ {
		if _, repeat, _ := runTools(t, nil, "tools", "bookstack", "--config", cfg); repeat != stdout {
			t.Fatalf("run %d = %q, want %q", i+2, repeat, stdout)
		}
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("provider calls = %d, want 0", got)
	}

	t.Run("the TOON default and --output json carry the same listing", func(t *testing.T) {
		code, jsonOut, stderr := runTools(t, nil, "tools", "bookstack", "--config", cfg, "--output", "json")
		if code != exitOK || stderr != "" {
			t.Fatalf("exit=%d stderr=%q", code, stderr)
		}
		if got := toonOfJSON(t, jsonOut); got != stdout {
			t.Errorf("TOON output = %q, want the TOON rendering of the JSON data %q", stdout, got)
		}
		for _, tool := range toolSummaries(t, jsonOut) {
			if tool.Title == "" {
				t.Errorf("%s = %+v, want a titled tool", tool.ID, tool)
			}
		}
	})

	t.Run("--all lists every tool with the reason", func(t *testing.T) {
		code, stdout, stderr := runTools(t, nil, "tools", "bookstack", "--all", "--config", cfg)
		if code != exitOK || stderr != "" {
			t.Fatalf("exit=%d stderr=%q", code, stderr)
		}
		for _, want := range []string{
			"tools[5]{connections,effect,id,reason,title}:\n",
			`  "",create,bookstack.pages.create,effect-not-permitted,`,
			`  "",delete,bookstack.pages.delete,effect-not-permitted,`,
			`  wiki,read,bookstack.pages.get,"",Get a BookStack page`,
		} {
			if !strings.Contains(stdout, want) {
				t.Errorf("stdout does not contain %q:\n%s", want, stdout)
			}
		}
		code, stdout, stderr = runTools(t, nil, "tools", "lexware", "--all", "--config", cfg)
		if code != exitOK || stderr != "" || !strings.Contains(stdout, `  "",read,lexware.invoices.list,no-connection,`) {
			t.Errorf("a namespace without a connection: exit=%d stderr=%q stdout:\n%s", code, stderr, stdout)
		}
	})

	// An empty answer that only the configuration causes keeps stdout the payload and says so on stderr.
	t.Run("an empty listing points to --all", func(t *testing.T) {
		code, stdout, stderr := runTools(t, nil, "tools", "lexware", "--config", cfg)
		if code != exitOK || stdout != "tools: []\n" ||
			stderr != "qatlas: no connection offers any of the 3 matching tools; --all lists them with the "+
				"reason, 'qatlas connections' lists the connections\n" {
			t.Errorf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		code, stdout, stderr = runTools(t, nil, "tools", "--query", "absent", "--config", cfg)
		if code != exitOK || stdout != "tools: []\n" || stderr != "" {
			t.Errorf("a query without any match: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})

	// Without a namespace and without a query the command would print the whole catalog, which is what
	// the cascade exists to avoid. It names the first step instead.
	t.Run("a bare tools call names the first step", func(t *testing.T) {
		code, stdout, stderr := runTools(t, nil, "tools", "--config", cfg)
		if code != exitUsage || stdout != "" || !strings.Contains(stderr, "qatlas providers") {
			t.Errorf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})
}

// Acceptance 2: the namespace argument and --query restrict the same catalog.
func TestToolsFiltersByNamespaceAndQuery(t *testing.T) {
	cfg, _, _ := catalogEnvironment(t)

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"namespace", []string{"bookstack"}, []string{"bookstack.pages.create", "bookstack.pages.delete", "bookstack.pages.get", "bookstack.pages.list", "bookstack.pages.update"}},
		{"namespace telegram", []string{"telegram"}, []string{"telegram.messages.delete", "telegram.messages.edit", "telegram.messages.send"}},
		{"namespace lexware", []string{"lexware"}, []string{"lexware.invoices.create", "lexware.invoices.get", "lexware.invoices.list"}},
		{"namespace twentycrm", []string{"twentycrm"}, []string{
			"twentycrm.companies.create", "twentycrm.companies.delete", "twentycrm.companies.get", "twentycrm.companies.list", "twentycrm.companies.update",
		}},
		{"namespace seatable", []string{"seatable"}, []string{"seatable.columns.list", "seatable.rows.create", "seatable.rows.delete", "seatable.rows.get", "seatable.rows.list", "seatable.rows.update", "seatable.tables.list"}},
		{"namespace github", []string{"github"}, []string{
			"github.actionspermissions.get", "github.actionspermissions.update",
			"github.comments.create", "github.comments.list", "github.issues.close", "github.issues.create",
			"github.issues.get", "github.issues.list", "github.issues.reopen", "github.issues.update",
			"github.projectdrafts.create", "github.projectfieldoptions.delete", "github.projectfields.create",
			"github.projectfields.delete", "github.projectfields.list", "github.projectfields.update",
			"github.projectissues.create", "github.projectitems.add",
			"github.projectitems.archive", "github.projectitems.get", "github.projectitems.list",
			"github.projectitems.update", "github.projectiterations.replace", "github.projects.copy", "github.projects.create",
			"github.projects.delete", "github.projects.link", "github.projects.list", "github.projects.unlink",
			"github.projects.update", "github.projecttemplates.mark", "github.projecttemplates.unmark",
			"github.projectviews.create", "github.projectviews.delete", "github.projectviews.list",
			"github.projectviews.update", "github.repositories.list",
			"github.workflowartifacts.list", "github.workflowfiles.create",
			"github.workflowfiles.get", "github.workflowfiles.list", "github.workflowfiles.update",
			"github.workflowjobs.get", "github.workflowjobs.list", "github.workflowjobs.log",
			"github.workflowpermissions.get", "github.workflowpermissions.update", "github.workflowruns.cancel",
			"github.workflowruns.get", "github.workflowruns.list", "github.workflowruns.rerun",
			"github.workflowruns.rerunfailed", "github.workflows.disable", "github.workflows.dispatch",
			"github.workflows.enable", "github.workflows.get", "github.workflows.list",
		}},
		{"namespace nextcloud", []string{"nextcloud"}, []string{
			"nextcloud.files.create", "nextcloud.files.delete", "nextcloud.files.get", "nextcloud.files.list", "nextcloud.files.stat", "nextcloud.files.update",
		}},
		{"query", []string{"--query", "pages"}, []string{"bookstack.pages.create", "bookstack.pages.delete", "bookstack.pages.get", "bookstack.pages.list", "bookstack.pages.update"}},
		{"namespace and query", []string{"bookstack", "--query", "list"}, []string{"bookstack.pages.list"}},
		{"query without a match", []string{"--query", "absent"}, []string{}},
	}
	// --all filters the complete catalog, so namespace and query select every tool they match.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"tools", "--all"}, tt.args...)
			code, stdout, stderr := runTools(t, nil, append(args, "--config", cfg, "--output", "json")...)
			if code != exitOK || stderr != "" {
				t.Fatalf("exit=%d stderr=%q", code, stderr)
			}
			if got := toolIDs(t, stdout); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("tools = %v, want %v", got, tt.want)
			}
		})
	}

	// Without --all the same filters apply to the tools a connection offers, and --connection narrows that
	// to the one connection.
	for _, tt := range []struct {
		name string
		args []string
		want []string
	}{
		{"offered by namespace", []string{"bookstack"}, []string{"bookstack.pages.get", "bookstack.pages.list"}},
		{"offered by query", []string{"--query", "pages"}, []string{"bookstack.pages.get", "bookstack.pages.list"}},
		{"offered by a connection", []string{"telegram", "--connection", "alerts"}, []string{"telegram.messages.send"}},
		{"offered by another provider's connection", []string{"--query", "pages", "--connection", "alerts"}, []string{}},
		// A query also finds a tool by the description and the note of its provider and by the description
		// of a connection that offers it.
		{"offered by the provider description", []string{"--query", "instant"}, []string{"telegram.messages.send"}},
		{"offered by the provider note", []string{"--query", "Handbook"}, []string{"bookstack.pages.get", "bookstack.pages.list"}},
		{"offered by a connection description", []string{"--query", "read-only account"}, []string{"bookstack.pages.get", "bookstack.pages.list"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"tools"}, tt.args...)
			code, stdout, _ := runTools(t, nil, append(args, "--config", cfg, "--output", "json")...)
			if code != exitOK {
				t.Fatalf("exit=%d", code)
			}
			if got := toolIDs(t, stdout); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("tools = %v, want %v", got, tt.want)
			}
			for _, tool := range toolSummaries(t, stdout) {
				if tool.Connections == "" || tool.Reason != nil {
					t.Errorf("%s = %+v, want its connections and no reason", tool.ID, tool)
				}
			}
		})
	}

	t.Run("an unknown namespace is a usage error", func(t *testing.T) {
		code, stdout, stderr := runTools(t, nil, "tools", "bookstck", "--config", cfg)
		if code != exitUsage || stdout != "" ||
			!strings.Contains(stderr, `unknown tool namespace "bookstck" (did you mean "bookstack"?)`) {
			t.Errorf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})

	t.Run("a second argument is a usage error", func(t *testing.T) {
		code, _, stderr := runTools(t, nil, "tools", "bookstack", "telegram", "--config", cfg)
		if code != exitUsage || !strings.Contains(stderr, "expected at most one tool namespace") {
			t.Errorf("exit=%d stderr=%q", code, stderr)
		}
	})
}

// Acceptance 3: one tool document carries the complete contract, and the TOON default and --output json
// are two renderings of the same data.
func TestToolDescribesOneCompleteContract(t *testing.T) {
	cfg, _, _ := catalogEnvironment(t)

	code, toon, stderr := runTools(t, nil, "describe", "bookstack.pages.list", "--config", cfg)
	if code != exitOK || stderr != "" {
		t.Fatalf("TOON exit=%d stderr=%q", code, stderr)
	}
	code, jsonOut, stderr := runTools(t, nil, "describe", "bookstack.pages.list", "--config", cfg, "--output", "json")
	if code != exitOK || stderr != "" {
		t.Fatalf("JSON exit=%d stderr=%q", code, stderr)
	}
	if got := toonOfJSON(t, jsonOut); got != toon {
		t.Errorf("TOON output = %q, want the TOON rendering of the JSON data %q", toon, got)
	}

	var document struct {
		Tool        capability.Descriptor       `json:"tool"`
		Connections []application.ConnectionRef `json:"connections"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &document); err != nil {
		t.Fatalf("tool output is not valid JSON: %v", err)
	}
	tool := document.Tool
	if tool.ID != "bookstack.pages.list" || tool.Version != 1 || tool.Description == "" ||
		len(tool.Tags) == 0 || len(tool.InputSchema) == 0 || len(tool.OutputSchema) == 0 ||
		len(tool.Examples) == 0 || len(tool.Fields) == 0 || tool.Risk.Effect != capability.EffectRead ||
		tool.Risk.Idempotency != capability.IdempotencySafe ||
		tool.Risk.Confirmation != capability.ConfirmationNone || tool.Risk.DataSensitivity == "" {
		t.Errorf("tool contract = %+v", tool)
	}
	// A connection is published as the name that selects it plus the line its owner maintains, so a
	// reader can tell two routes of one provider apart without opening the configuration.
	want := []application.ConnectionRef{{Name: "wiki", Description: "read-only account on the team wiki"}}
	if !reflect.DeepEqual(document.Connections, want) {
		t.Errorf("connections = %+v, want %+v", document.Connections, want)
	}

	t.Run("a connection without a description carries the empty string", func(t *testing.T) {
		code, jsonOut, stderr := runTools(t, nil, "describe", "telegram.messages.send", "--config", cfg,
			"--output", "json")
		if code != exitOK || stderr != "" {
			t.Fatalf("exit=%d stderr=%q", code, stderr)
		}
		var document struct {
			Connections []application.ConnectionRef `json:"connections"`
		}
		if err := json.Unmarshal([]byte(jsonOut), &document); err != nil {
			t.Fatalf("tool output is not valid JSON: %v", err)
		}
		if want := []application.ConnectionRef{{Name: "alerts"}}; !reflect.DeepEqual(document.Connections, want) {
			t.Errorf("connections = %+v, want %+v", document.Connections, want)
		}
	})

	t.Run("there is no verb below a tool", func(t *testing.T) {
		code, stdout, stderr := runTools(t, nil, "describe", "show", "bookstack.pages.list", "--config", cfg)
		if code != exitUsage || stdout != "" || !strings.Contains(stderr, "expected exactly one tool ID") {
			t.Errorf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})

	t.Run("an unknown tool is a stable error", func(t *testing.T) {
		code, stdout, stderr := runTools(t, nil, "describe", "absent.pages.list", "--config", cfg)
		if code != exitUsage || stdout != "" || !strings.HasPrefix(stderr, "qatlas: unknown-operation:") {
			t.Errorf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})

	t.Run("a scalar format is refused naming the command and its formats", func(t *testing.T) {
		for _, args := range [][]string{
			{"describe", "bookstack.pages.list"}, {"providers"}, {"connections"}, {"tools", "bookstack"},
		} {
			for _, format := range []string{"table", "compact"} {
				code, stdout, stderr := runTools(t, nil, append(args, "--config", cfg, "--output", format)...)
				want := "qatlas: usage: 'qatlas " + args[0] + "' writes toon or json, not " + format +
					"; omit --output for toon or pass --output json\n"
				if code != exitUsage || stdout != "" || stderr != want {
					t.Errorf("%v --output %s: exit=%d stdout=%q stderr=%q, want %q", args, format, code,
						stdout, stderr, want)
				}
			}
		}
	})
}

// Acceptance 4: discovery reads no secret and publishes no secret, provider body, or payload canary.
func TestDiscoveryReadsNoSecretsAndLeaksNoCanary(t *testing.T) {
	const (
		tokenCanary  = "tools-token-canary-6d13"
		secretCanary = "tools-secret-canary-9f04"
		botCanary    = "tools-bot-canary-2e88"
	)
	cfg, calls, providerCanary := catalogEnvironment(t)
	t.Setenv("TOOLS_TOKEN_ID", tokenCanary)
	t.Setenv("TOOLS_TOKEN_SECRET", secretCanary)
	t.Setenv("TOOLS_BOT_TOKEN", botCanary)

	var reads atomic.Int32
	for _, args := range [][]string{
		{"providers", "--config", cfg},
		{"tools", "bookstack", "--config", cfg, "--output", "json"},
		{"describe", "telegram.messages.send", "--config", cfg},
		{"describe", "bookstack.pages.list", "--config", cfg, "--output", "json"},
		{"tools", "bookstack", "--all", "--config", cfg},
		{"connections", "--config", cfg},
		{"connections", "telegram", "--config", cfg, "--output", "json"},
	} {
		code, stdout, stderr := runTools(t, &reads, args...)
		if code != exitOK || stderr != "" {
			t.Fatalf("%v exit=%d stderr=%q", args, code, stderr)
		}
		for _, canary := range []string{
			tokenCanary, secretCanary, botCanary, providerCanary, "-1009900112233",
		} {
			if strings.Contains(stdout, canary) || strings.Contains(stderr, canary) {
				t.Errorf("%v published the canary %q", args, canary)
			}
		}
		// Discovery names routes, never where they lead or what they authenticate with.
		for _, absent := range []string{"http", "127.0.0.1", "TOOLS_", "reader", "bot"} {
			if strings.Contains(stdout, absent) {
				t.Errorf("%v published %q:\n%s", args, absent, stdout)
			}
		}
	}
	if reads.Load() != 0 {
		t.Errorf("secret lookups = %d, want 0", reads.Load())
	}
	if calls.Load() != 0 {
		t.Errorf("provider calls = %d, want 0", calls.Load())
	}
}

// syntheticRegistry registers count operations of one synthetic provider, so a test can grow the catalog
// without growing anything else.
func syntheticRegistry(t *testing.T, count int) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	if err := reg.RegisterProvider(config.ProviderMetadata{ID: "synthetic", Name: "Synthetic"}, nil); err != nil {
		t.Fatalf("RegisterProvider() = %v", err)
	}
	operations := make([]capability.Operation, count)
	for i := range operations {
		operations[i] = capability.Operation{
			Descriptor: capability.Descriptor{
				ID: fmt.Sprintf("synthetic.object%d.list", i), Version: 1, Provider: "synthetic",
				Description: fmt.Sprintf("List synthetic object %d", i),
				Risk: capability.Risk{
					Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
					Confirmation: capability.ConfirmationNone, DataSensitivity: "synthetic",
				},
				InputSchema:  json.RawMessage(`{"type":"object"}`),
				OutputSchema: json.RawMessage(`{"type":"array"}`),
			},
			Handler: func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
				json.RawMessage) (any, error) {
				return []any{}, nil
			},
		}
	}
	if err := reg.Register("synthetic", operations...); err != nil {
		t.Fatalf("Register() = %v", err)
	}
	return reg
}

func commandNames(cmd *cobra.Command) []string {
	names := make([]string, 0, len(cmd.Commands()))
	for _, sub := range cmd.Commands() {
		if sub.IsAdditionalHelpTopicCommand() {
			// A help topic is text, not a command; the help topic tests cover it.
			continue
		}
		if sub.Hidden {
			// A hidden command is a kept alias, not part of the public surface.
			continue
		}
		names = append(names, sub.Name())
		for _, child := range sub.Commands() {
			names = append(names, sub.Name()+" "+child.Name())
		}
	}
	sort.Strings(names)
	return names
}

// Acceptance 7: a large catalog grows the catalog data only. The command tree and the MCP tool list stay
// exactly as they are.
func TestLargeCatalogGrowsOnlyTheData(t *testing.T) {
	t.Setenv("QATLAS_CONFIG", "")
	t.Setenv("QATLAS_CLI_HOME", "")
	cfg := writeConfig(t, "version: 1\n")

	small := newRootCommand(&Options{}, syntheticRegistry(t, 1))
	large := newRootCommand(&Options{}, syntheticRegistry(t, 128))
	if !reflect.DeepEqual(commandNames(small), commandNames(large)) {
		t.Errorf("commands = %v, want %v", commandNames(large), commandNames(small))
	}
	if len(mcpTools()) != 3 {
		t.Errorf("MCP tools = %d, want the three fixed broker tools", len(mcpTools()))
	}

	var stdout, stderr bytes.Buffer
	opts := &Options{Input: strings.NewReader(""), Redactor: &redact.Redactor{}}
	code := run(newRootCommand(opts, syntheticRegistry(t, 128)), opts,
		[]string{"tools", "synthetic", "--all", "--config", cfg, "--output", "json"}, &stdout, &stderr)
	if code != exitOK || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if got := len(toolIDs(t, stdout.String())); got != 128 {
		t.Errorf("catalog entries = %d, want 128", got)
	}
}

// Acceptance 6: the public surface is the tool taxonomy: lists are nouns, single actions are verbs. The
// replaced trees are gone from the command tree, from the help, and from the generated manpages, and the
// former name of describe runs as a hidden alias that no help names.
func TestPublicSurfaceIsTheToolTaxonomy(t *testing.T) {
	root := DocumentationCommand("test")
	want := []string{
		"config", "config validate", "connections", "credential", "credential delete", "credential set",
		"describe", "invoke", "mcp", "providers", "tools", "tui", "update",
	}
	if got := commandNames(root); !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %v, want %v", got, want)
	}

	manuals := t.TempDir()
	header := &doc.GenManHeader{Section: "1", Source: "Qatlas CLI test", Manual: "Qatlas CLI Manual"}
	if err := doc.GenManTree(root, header, manuals); err != nil {
		t.Fatalf("GenManTree() = %v", err)
	}
	pages := map[string]bool{}
	entries, err := os.ReadDir(manuals)
	if err != nil {
		t.Fatalf("ReadDir() = %v", err)
	}
	for _, entry := range entries {
		pages[entry.Name()] = true
	}
	for _, page := range []string{"qatlas-connections.1", "qatlas-tools.1", "qatlas-describe.1", "qatlas-invoke.1"} {
		if !pages[page] {
			t.Errorf("manpage %s is missing: %v", page, pages)
		}
	}
	for _, page := range []string{
		"qatlas-capabilities.1", "qatlas-search.1", "qatlas-tool.1", "qatlas-knowledge.1", "qatlas-tool-show.1",
	} {
		if pages[page] {
			t.Errorf("manpage %s still exists", page)
		}
	}
	singular := regexp.MustCompile(`qatlas tool\b`)
	for _, entry := range entries {
		page, err := os.ReadFile(filepath.Join(manuals, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if singular.Match(page) {
			t.Errorf("manpage %s names the hidden command 'qatlas tool'", entry.Name())
		}
	}

	var help bytes.Buffer
	root.SetOut(&help)
	if err := root.Help(); err != nil {
		t.Fatalf("Help() = %v", err)
	}
	for _, want := range []string{
		"qatlas providers", "qatlas connections <provider>", "qatlas tools <provider>",
		"qatlas describe <tool-id>", "qatlas invoke <tool-id>", "TOON 4.1",
	} {
		if !strings.Contains(help.String(), want) {
			t.Errorf("help does not mention %q:\n%s", want, help.String())
		}
	}
	// The help starts with the way in: the agents guide for an agent, the editor for a person. Discovery
	// then begins with the providers, before their connections.
	intro, _, _ := strings.Cut(help.String(), "Usage:")
	agents, tui := strings.Index(intro, "qatlas agents"), strings.Index(intro, "qatlas tui")
	providers, connections := strings.Index(intro, "qatlas providers"), strings.Index(intro, "qatlas connections")
	if agents < 0 || tui < 0 || !(agents < providers && tui < providers) {
		t.Errorf("the help does not start with 'qatlas agents' and 'qatlas tui':\n%s", intro)
	}
	if providers < 0 || !(providers < connections) {
		t.Errorf("the help does not name 'qatlas providers' before 'qatlas connections':\n%s", intro)
	}
	_, listed, found := strings.Cut(help.String(), "Available Commands:")
	if !found {
		t.Fatalf("help has no command list:\n%s", help.String())
	}
	listed, _, _ = strings.Cut(listed, "\nFlags:")
	for _, removed := range []string{"capabilities", "knowledge", "search", "tool"} {
		if strings.Contains(listed, "\n  "+removed+" ") {
			t.Errorf("help still offers the removed command %q:%s", removed, listed)
		}
	}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Hidden {
			return
		}
		if singular.MatchString(c.Long) || singular.MatchString(c.Short) {
			t.Errorf("the help of %q names the hidden command 'qatlas tool'", c.CommandPath())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

// describe replaces the former tool command; the old name keeps working as a hidden alias with exactly the
// same answer.
func TestToolRunsAsAHiddenAliasOfDescribe(t *testing.T) {
	cfg, _, _ := catalogEnvironment(t)
	for _, format := range [][]string{nil, {"--output", "json"}} {
		code, described, stderr := runTools(t, nil, append([]string{"describe", "bookstack.pages.list", "--config", cfg}, format...)...)
		if code != exitOK || stderr != "" || described == "" {
			t.Fatalf("describe %v: exit=%d stderr=%q", format, code, stderr)
		}
		code, aliased, stderr := runTools(t, nil, append([]string{"tool", "bookstack.pages.list", "--config", cfg}, format...)...)
		if code != exitOK || stderr != "" || aliased != described {
			t.Errorf("tool %v: exit=%d stderr=%q stdout=%q, want %q", format, code, stderr, aliased, described)
		}
	}
	code, _, stderr := runTools(t, nil, "tool", "bookstack.pages.list", "--connection", "alerts", "--config", cfg)
	if code != exitUsage || !strings.HasPrefix(stderr, "qatlas: unsupported-capability: ") {
		t.Errorf("tool with a foreign connection: exit=%d stderr=%q", code, stderr)
	}
}

// The tool commands answer with the data the removed capabilities, describe, and search commands used to
// publish, which is the migration parity their removal rests on.
func TestToolCommandsKeepTheDataOfTheRemovedCommands(t *testing.T) {
	cfg, _, _ := catalogEnvironment(t)
	core, err := applicationCore(&Options{Config: cfg}, defaultRegistry(), false)
	if err != nil {
		t.Fatalf("applicationCore() = %v", err)
	}

	// capabilities listed name, risk, and description of every operation a connection offers.
	code, stdout, stderr := runTools(t, nil, "tools", "bookstack", "--config", cfg, "--output", "json")
	if code != exitOK || stderr != "" {
		t.Fatalf("tools exit=%d stderr=%q", code, stderr)
	}
	catalog, err := core.Tools(application.SearchRequest{Provider: "bookstack"})
	if err != nil {
		t.Fatalf("Tools() = %v", err)
	}
	if got := toolSummaries(t, stdout); !reflect.DeepEqual(got, catalog.Tools) {
		t.Errorf("tools = %+v, want %+v", got, catalog.Tools)
	}
	// The cascade must still reach every compiled operation: walking the namespaces and listing each one
	// names exactly the catalog the removed commands published in one answer.
	reachable := map[string]bool{}
	for _, provider := range core.Providers().Providers {
		listed, err := core.Tools(application.SearchRequest{Provider: provider.Provider, All: true})
		if err != nil {
			t.Fatalf("Tools(%s) = %v", provider.Provider, err)
		}
		if len(listed.Tools) != provider.Tools {
			t.Errorf("%s lists %d tools, want the %d it counts", provider.Provider, len(listed.Tools),
				provider.Tools)
		}
		for _, tool := range listed.Tools {
			reachable[tool.ID] = true
		}
	}
	for _, descriptor := range defaultRegistry().All() {
		if !reachable[descriptor.ID] {
			t.Errorf("the cascade never reaches %q", descriptor.ID)
		}
	}

	// search filtered the same catalog by query. The index keeps that filter and drops only the payload,
	// so the two views name the same tools in the same order.
	code, stdout, stderr = runTools(t, nil, "tools", "--config", cfg, "--query", "pages", "--output", "json")
	if code != exitOK || stderr != "" {
		t.Fatalf("query exit=%d stderr=%q", code, stderr)
	}
	searched, err := core.Search(application.SearchRequest{Query: "pages"})
	if err != nil {
		t.Fatalf("Search() = %v", err)
	}
	filtered := toolSummaries(t, stdout)
	if len(filtered) != len(searched.Operations) {
		t.Fatalf("query result = %+v, want the %d searched operations", filtered, len(searched.Operations))
	}
	for i, tool := range filtered {
		if tool.ID != searched.Operations[i].ID {
			t.Errorf("query result[%d] = %q, want %q", i, tool.ID, searched.Operations[i].ID)
		}
		if tool.Title != searched.Operations[i].Title || tool.Effect != searched.Operations[i].Effect {
			t.Errorf("query result[%d] = %+v, want %+v", i, tool, searched.Operations[i])
		}
	}

	// describe returned one complete descriptor and its connections.
	for _, id := range []string{"bookstack.pages.get", "bookstack.pages.list", "telegram.messages.send"} {
		code, stdout, stderr = runTools(t, nil, "describe", id, "--config", cfg, "--output", "json")
		if code != exitOK || stderr != "" {
			t.Fatalf("tool %s exit=%d stderr=%q", id, code, stderr)
		}
		var document struct {
			Tool        json.RawMessage             `json:"tool"`
			Connections []application.ConnectionRef `json:"connections"`
		}
		if err := json.Unmarshal([]byte(stdout), &document); err != nil {
			t.Fatalf("tool output = %q: %v", stdout, err)
		}
		described, err := core.Describe(application.DescribeRequest{Operation: id})
		if err != nil {
			t.Fatalf("Describe(%s) = %v", id, err)
		}
		wantOperation, err := json.Marshal(described.Operation)
		if err != nil {
			t.Fatal(err)
		}
		if !jsonEqual(document.Tool, wantOperation) {
			t.Errorf("tool %s = %s, want %s", id, document.Tool, wantOperation)
		}
		if !reflect.DeepEqual(document.Connections, described.Connections) {
			t.Errorf("tool %s connections = %v, want %v", id, document.Connections, described.Connections)
		}
	}
}

// The connections step lists every configured route with what it may do, sorted by provider and name, and
// answers for one provider when asked. It never publishes a service, a URL, a credential, or a target.
func TestConnectionsListTheConfiguredRoutes(t *testing.T) {
	cfg, calls, _ := catalogEnvironment(t)
	var reads atomic.Int32

	code, stdout, stderr := runTools(t, &reads, "connections", "--config", cfg)
	if code != exitOK || stderr != "" {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	want := "connections[2]{description,name,permissions,provider,tools}:\n" +
		"  read-only account on the team wiki,wiki,read,bookstack,all-permitted\n" +
		"  \"\",alerts,create,telegram,all-permitted\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}

	code, jsonOut, stderr := runTools(t, &reads, "connections", "--config", cfg, "--output", "json")
	if code != exitOK || stderr != "" {
		t.Fatalf("json exit=%d stderr=%q", code, stderr)
	}
	if got := toonOfJSON(t, jsonOut); got != stdout {
		t.Errorf("TOON output = %q, want the TOON rendering of the JSON data %q", stdout, got)
	}
	var document struct {
		Connections []application.ConnectionSummary `json:"connections"`
	}
	decoder := json.NewDecoder(strings.NewReader(jsonOut))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("connections output is not the connection index: %v: %s", err, jsonOut)
	}

	code, stdout, stderr = runTools(t, &reads, "connections", "telegram", "--config", cfg)
	if code != exitOK || stderr != "" ||
		stdout != "connections[1]{description,name,permissions,provider,tools}:\n  \"\",alerts,create,telegram,all-permitted\n" {
		t.Errorf("connections telegram: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runTools(t, &reads, "connections", "lexware", "--config", cfg)
	if code != exitOK || stderr != "" || stdout != "connections: []\n" {
		t.Errorf("connections lexware: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runTools(t, &reads, "connections", "absent", "--config", cfg)
	if code != exitUsage || stdout != "" || strings.Contains(stderr, "Usage:") ||
		!strings.HasPrefix(stderr, `qatlas: usage: unknown provider "absent"; run 'qatlas providers'`) {
		t.Errorf("connections absent: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if reads.Load() != 0 || calls.Load() != 0 {
		t.Errorf("secret lookups = %d, provider calls = %d, want none", reads.Load(), calls.Load())
	}
}

// A connection that does not offer a tool is refused with the same reason the catalog names, followed by a
// JSON detail, on the command line and over MCP alike, and without the usage block.
func TestUnsupportedCapabilityNamesTheReason(t *testing.T) {
	cfg, _, _ := catalogEnvironment(t)
	var reads atomic.Int32

	for _, tt := range []struct {
		name string
		args []string
	}{
		{"invoke", []string{"invoke", "bookstack.pages.delete", "--connection", "wiki", "--confirm", "--arg", "id=1"}},
		{"describe", []string{"describe", "bookstack.pages.delete", "--connection", "wiki"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runTools(t, &reads, append(tt.args, "--config", cfg)...)
			lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
			wantMessage := `qatlas: unsupported-capability: connection "wiki" does not offer tool ` +
				`"bookstack.pages.delete" (effect-not-permitted); 'qatlas describe bookstack.pages.delete' names ` +
				`the connections that offer it, or change the connection in 'qatlas tui'`
			if code != exitUsage || stdout != "" || len(lines) != 2 || lines[0] != wantMessage {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			var detail map[string]any
			if err := json.Unmarshal([]byte(lines[1]), &detail); err != nil {
				t.Fatalf("detail = %q: %v", lines[1], err)
			}
			if detail["code"] != "unsupported-capability" || detail["operation"] != "bookstack.pages.delete" ||
				detail["connection"] != "wiki" || detail["reason"] != "effect-not-permitted" ||
				!strings.Contains(fmt.Sprint(detail["message"]), "(effect-not-permitted)") {
				t.Errorf("detail = %v", detail)
			}
		})
	}

	code, _, stderr := runTools(t, &reads, "invoke", "telegram.messages.send", "--connection", "wiki",
		"--confirm", "--arg", "text=x", "--config", cfg)
	if code != exitUsage || !strings.Contains(stderr, `"reason":"other-provider"`) {
		t.Errorf("a foreign connection: exit=%d stderr=%q", code, stderr)
	}
	if reads.Load() != 0 {
		t.Errorf("secret lookups = %d, want none", reads.Load())
	}

	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta +
		`,"name":"qatlas.invoke","arguments":{"operation":"bookstack.pages.delete","connection":"wiki",` +
		`"arguments":{"id":1},"confirm":true}}}` + "\n"
	responses, mcpStderr := runMCPWithOptions(t, defaultRegistry(), input,
		&Options{Config: cfg, Redactor: &redact.Redactor{}})
	result := toolResultFrom(t, responses["1"])
	var detail map[string]any
	decodeRaw(t, result.Structured, &detail)
	if mcpStderr != "" || !result.IsError ||
		!strings.HasPrefix(result.Content[0].Text, "unsupported-capability: connection \"wiki\"") ||
		detail["reason"] != "effect-not-permitted" || detail["connection"] != "wiki" {
		t.Errorf("MCP invoke = %+v detail=%v stderr=%q", result, detail, mcpStderr)
	}
}

// qatlas.search follows the catalog rule of 'qatlas tools': the offered tools by default, and with all the
// others as well, each with the reason the CLI names.
func TestMCPSearchFollowsTheCatalogRule(t *testing.T) {
	cfg, _, _ := catalogEnvironment(t)
	for _, all := range []bool{false, true} {
		args := []string{"tools", "bookstack", "--config", cfg, "--output", "json"}
		arguments := `{"provider":"bookstack"}`
		if all {
			args = append(args, "--all")
			arguments = `{"provider":"bookstack","all":true}`
		}
		code, stdout, stderr := runTools(t, nil, args...)
		if code != exitOK || stderr != "" {
			t.Fatalf("CLI %v: exit=%d stderr=%q", args, code, stderr)
		}
		indexed := toolSummaries(t, stdout)

		input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + mcpTestMeta +
			`,"name":"qatlas.search","arguments":` + arguments + `}}` + "\n"
		responses, mcpStderr := runMCPWithOptions(t, defaultRegistry(), input,
			&Options{Config: cfg, Redactor: &redact.Redactor{}})
		var page application.SearchResponse
		decodeRaw(t, toolResultFrom(t, responses["1"]).Structured, &page)
		if mcpStderr != "" || len(page.Operations) != len(indexed) {
			t.Fatalf("all=%v: search = %+v, index = %+v", all, page.Operations, indexed)
		}
		for i, hit := range page.Operations {
			reason := ""
			if indexed[i].Reason != nil {
				reason = string(*indexed[i].Reason)
			}
			if hit.ID != indexed[i].ID || strings.Join(hit.Connections, " ") != indexed[i].Connections ||
				string(hit.Reason) != reason || (indexed[i].Reason != nil) != all {
				t.Errorf("all=%v: search[%d] = %+v, index = %+v", all, i, hit, indexed[i])
			}
		}
	}
}
