package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	logMarkerQuery  = "QUERYMARKER7f3a"
	logMarkerArg    = "ARGMARKER7f3a"
	logMarkerResult = "RESULTMARKER7f3a"
)

// mcpLogRegistry is a registry whose one tool answers with logMarkerResult, and the path of its configuration.
func mcpLogRegistry(t *testing.T) (*capability.Registry, string) {
	t.Helper()
	return mcpTestRegistry(t, func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
		json.RawMessage) (any, error) {
		return map[string]any{"note": logMarkerResult}, nil
	})
}

func mcpLogOptions(path string) *Options {
	return &Options{Config: path, Redactor: &redact.Redactor{}, Secrets: secret.NewWith(nil, nil, nil, nil)}
}

func mcpLogDir(path string) string { return filepath.Join(invokeLogDir(path), "logs") }

// readMCPLog returns the entries of every day file of the log of the configuration at path, in order.
func readMCPLog(t *testing.T, path string) ([]invokelog.Entry, string) {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(mcpLogDir(path), "*.jsonl"))
	sort.Strings(files)
	var entries []invokelog.Entry
	var raw strings.Builder
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		raw.Write(data)
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			var entry invokelog.Entry
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatalf("log line %q: %v", line, err)
			}
			entries = append(entries, entry)
		}
	}
	return entries, raw.String()
}

func mcpLogCall(id, tool, arguments string) string {
	return `{"jsonrpc":"2.0","id":"` + id + `","method":"tools/call","params":{` + mcpTestMeta +
		`,"name":"` + tool + `","arguments":` + arguments + `}}`
}

// runMCPLines serves the lines as one session and returns the raw stdout lines by response ID.
func runMCPLines(t *testing.T, registry *capability.Registry, options *Options, lines []string) (map[string]string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	server := newMCPServer(options, registry, &stdout, &stderr)
	if err := server.serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n")); err != nil {
		t.Fatalf("serve() = %v", err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var envelope struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			t.Fatalf("stdout line %q: %v", line, err)
		}
		out[strings.Trim(string(envelope.ID), `"`)] = line
	}
	return out, stderr.String()
}

// One session of every kind of exchange logs one entry each, under one session ID, with the byte lengths of
// the lines that were read and written, and nothing the agent asked for or got.
func TestMCPSessionLogRecordsBaselineNavigationAndInvokes(t *testing.T) {
	registry, path := mcpLogRegistry(t)
	lines := []string{
		`{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"probe","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":"ping","method":"ping"}`,
		`{"jsonrpc":"2.0","id":"list","method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":"cursor","method":"tools/list","params":{"cursor":"x"}}`,
		`{"jsonrpc":"2.0","id":"discover","method":"server/discover","params":{"bogus":1}}`,
		mcpLogCall("search", "qatlas.search", `{"query":"`+logMarkerQuery+`","provider":"fake","connection":"primary"}`),
		mcpLogCall("overview", "qatlas.search", `{"list":"connections","provider":"fake"}`),
		mcpLogCall("describe", "qatlas.describe", `{"operation":"fake.pages.get","connection":"primary"}`),
		mcpLogCall("describe-bad", "qatlas.describe", `{"operation":"fake.nothing.`+logMarkerArg+`"}`),
		mcpLogCall("invoke", "qatlas.invoke", `{"operation":"fake.pages.get","connection":"primary","arguments":{}}`),
		mcpLogCall("invoke-bad", "qatlas.invoke", `{"operation":"fake.pages.get","connection":"primary","arguments":{"`+logMarkerArg+`":1}}`),
		mcpLogCall("invoke-schema", "qatlas.invoke", `{"operation":"fake.pages.get","arguments":"`+logMarkerArg+`"}`),
		mcpLogCall("forged", "qatlas.invoke", `{"operation":"qatlas.search","connection":"primary"}`),
		mcpLogCall("forged-base", "qatlas.invoke", `{"operation":"mcp/initialize"}`),
		`{"jsonrpc":"2.0","id":"unknown","method":"tools/call","params":{"name":"qatlas.other","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":"nomethod","method":"resources/list"}`,
	}
	byID, stderr := runMCPLines(t, registry, mcpLogOptions(path), lines)
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}

	byLine := map[string]string{}
	for _, line := range lines {
		var envelope struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal([]byte(line), &envelope)
		byLine[envelope.ID] = line
	}
	// want is what each logged request ID must have produced: its operation and result, and the sizes of its
	// request line and of its response line, measured here and not by the server.
	want := map[string][2]string{
		"init":          {invokelog.OperationMCPInitialize, "success"},
		"list":          {invokelog.OperationMCPToolsList, "success"},
		"cursor":        {invokelog.OperationMCPToolsList, "invalid-params"},
		"discover":      {invokelog.OperationMCPInitialize, "invalid-params"},
		"search":        {invokelog.OperationMCPSearch, "success"},
		"overview":      {invokelog.OperationMCPSearch, "success"},
		"describe":      {invokelog.OperationMCPDescribe, "success"},
		"describe-bad":  {invokelog.OperationMCPDescribe, "unknown-operation"},
		"invoke":        {"fake.pages.get", "success"},
		"invoke-bad":    {"fake.pages.get", "invalid-request"},
		"invoke-schema": {"", "invalid-request"},
		"forged":        {"", "unknown-operation"},
		"forged-base":   {"", "unknown-operation"},
	}
	type sizes struct{ req, resp int64 }
	expected := map[string]sizes{}
	for id, w := range want {
		response, ok := byID[id]
		if !ok {
			t.Fatalf("no response for %s", id)
		}
		expected[w[0]+"|"+w[1]+"|"+id] = sizes{int64(len(byLine[id])), int64(len(response))}
	}

	entries, _ := readMCPLog(t, path)
	if len(entries) != len(want) {
		t.Fatalf("log holds %d entries, want %d: %+v", len(entries), len(want), entries)
	}
	session := entries[0].Session
	if !invokelog.ValidSession(session) {
		t.Fatalf("session = %q", session)
	}
	got := map[string]sizes{}
	for _, e := range entries {
		if e.Session != session || e.Path != "mcp" {
			t.Fatalf("entry = %+v, want session %s on the mcp path", e, session)
		}
		if e.ReqBytes == nil || e.RespBytes == nil {
			t.Fatalf("entry %+v lacks a size counter", e)
		}
		if reserved := invokelog.IsReservedOperation(e.Operation); reserved && (e.Connection != "" || e.Effect != "" || e.Version != 0) {
			t.Fatalf("reserved entry %+v carries more than it may", e)
		}
		// The sizes identify the exchange; the IDs are not logged.
		matched := false
		for key, s := range expected {
			parts := strings.SplitN(key, "|", 3)
			if parts[0] == e.Operation && parts[1] == e.Result && s.req == *e.ReqBytes && s.resp == *e.RespBytes {
				got[key], matched = s, true
			}
		}
		if !matched {
			t.Errorf("entry %+v matches no exchange of the session; expected %v", e, expected)
		}
	}
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("logged exchanges = %v, want %v", got, expected)
	}

	// ping, the notification, and the unknown tool and method are not logged, and the forged operations
	// of a client are only calls, never baseline or navigation.
	summary := invokelog.SummarizeSessions(entries)
	if len(summary) != 1 || summary[0].Session != session {
		t.Fatalf("sessions = %+v", summary)
	}
	calls := map[invokelog.Area]int{}
	for _, area := range summary[0].Areas {
		calls[area.Area] = area.Calls
	}
	if calls[invokelog.AreaBaseline] != 4 || calls[invokelog.AreaNavigation] != 4 || calls[invokelog.AreaInvoke] != 5 {
		t.Errorf("calls per area = %v, want baseline 4, navigation 4, invoke 5", calls)
	}
}

// Nothing the agent asked for or got is in the log, and a navigation call leaves no connection even where a
// filter named one.
func TestMCPSessionLogHoldsNoContent(t *testing.T) {
	registry, path := mcpLogRegistry(t)
	_, stderr := runMCPLines(t, registry, mcpLogOptions(path), []string{
		mcpLogCall("search", "qatlas.search", `{"query":"`+logMarkerQuery+`","provider":"fake","connection":"primary"}`),
		mcpLogCall("describe", "qatlas.describe", `{"operation":"fake.pages.get","connection":"primary"}`),
		mcpLogCall("invoke", "qatlas.invoke", `{"operation":"fake.pages.get","connection":"primary","arguments":{}}`),
		mcpLogCall("invoke-bad", "qatlas.invoke", `{"operation":"fake.pages.get","connection":"primary","arguments":{"`+logMarkerArg+`":1}}`),
	})
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
	entries, raw := readMCPLog(t, path)
	if len(entries) != 4 {
		t.Fatalf("log holds %d entries, want 4: %s", len(entries), raw)
	}
	for _, marker := range []string{logMarkerQuery, logMarkerArg, logMarkerResult} {
		if strings.Contains(raw, marker) {
			t.Errorf("the log contains %q: %s", marker, raw)
		}
	}
	for _, e := range entries {
		if invokelog.IsReservedOperation(e.Operation) && e.Connection != "" {
			t.Errorf("navigation entry %+v names a connection", e)
		}
	}
}

// A log that cannot be written changes no response and is reported as a warning for each entry.
func TestMCPSessionLogFailureLeavesResponsesAsTheyAre(t *testing.T) {
	lines := []string{
		`{"jsonrpc":"2.0","id":"list","method":"tools/list"}`,
		mcpLogCall("search", "qatlas.search", `{"provider":"fake"}`),
		mcpLogCall("invoke", "qatlas.invoke", `{"operation":"fake.pages.get","connection":"primary","arguments":{}}`),
	}
	registry, path := mcpLogRegistry(t)
	healthy, stderr := runMCPLines(t, registry, mcpLogOptions(path), lines)
	if stderr != "" {
		t.Fatalf("healthy stderr = %q", stderr)
	}

	registry, path = mcpLogRegistry(t)
	// A file where the logs directory belongs.
	if err := os.MkdirAll(invokeLogDir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mcpLogDir(path), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	broken, stderr := runMCPLines(t, registry, mcpLogOptions(path), lines)
	if !reflect.DeepEqual(healthy, broken) {
		t.Errorf("responses changed with an unwritable log:\n%v\n%v", healthy, broken)
	}
	if n := strings.Count(stderr, "qatlas: warning: could not write the invocation log"); n != 3 {
		t.Errorf("%d warnings, want 3: %q", n, stderr)
	}
}

// Without a configuration there is no retention to apply: nothing is logged and nothing is said.
func TestMCPSessionLogNeedsAConfiguration(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "config.yaml")
	_, stderr := runMCPLines(t, fakeRegistry(t), &Options{Config: missing, Redactor: &redact.Redactor{}},
		[]string{`{"jsonrpc":"2.0","id":"list","method":"tools/list"}`})
	if stderr != "" {
		t.Errorf("stderr = %q", stderr)
	}
	if _, err := os.Stat(invokeLogDir(missing)); !os.IsNotExist(err) {
		t.Errorf("a log directory exists without a configuration: %v", err)
	}
}

// A request too large to read is answered but is not an entry, and neither is the next one affected.
func TestMCPSessionLogSkipsOversizedMessages(t *testing.T) {
	registry, path := mcpLogRegistry(t)
	_, _ = runMCPLines(t, registry, mcpLogOptions(path), []string{
		strings.Repeat("x", maxRequestBytes+1),
		`{"jsonrpc":"2.0","id":"list","method":"tools/list"}`,
	})
	entries, _ := readMCPLog(t, path)
	if len(entries) != 1 || entries[0].Operation != invokelog.OperationMCPToolsList {
		t.Fatalf("entries = %+v, want the tools/list alone", entries)
	}
}

// A restart keeps the session ID, so the log does not split one client session into two.
func TestMCPHandoffKeepsTheSession(t *testing.T) {
	registry, path := mcpLogRegistry(t)
	server := newMCPServer(mcpLogOptions(path), registry, &bytes.Buffer{}, &bytes.Buffer{})
	other := newMCPServer(mcpLogOptions(path), registry, &bytes.Buffer{}, &bytes.Buffer{})
	if server.session == "" || server.session == other.session {
		t.Fatalf("sessions %q and %q must be set and differ", server.session, other.session)
	}
	encoded, _ := json.Marshal(mcpHandoff{Version: mcpHandoffVersion, From: "1", Session: server.session})
	handoff := decodeMCPHandoff(string(encoded))
	if handoff == nil {
		t.Fatal("handoff was refused")
	}
	other.adopt(handoff)
	if other.session != server.session {
		t.Errorf("adopted session = %q, want %q", other.session, server.session)
	}
	bad, _ := json.Marshal(mcpHandoff{Version: mcpHandoffVersion, From: "1", Session: "not a session"})
	if decodeMCPHandoff(string(bad)) != nil {
		t.Error("a handoff with a malformed session was accepted")
	}
}
