package invokelog

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const testSession = "0123456789abcdef"

func mcpFields(operation string) Fields {
	return Fields{Path: "mcp", Operation: operation, Result: "success", Session: testSession,
		ReqBytes: int64Ptr(100), RespBytes: int64Ptr(2000)}
}

func TestNewSession(t *testing.T) {
	a, err := NewSession()
	if err != nil || !sessionPattern.MatchString(a) {
		t.Fatalf("NewSession() = %q, %v, want 16 lowercase hex digits", a, err)
	}
	if b, _ := NewSession(); a == b {
		t.Errorf("two sessions are both %q", a)
	}
}

// A reserved operation name can never be a tool ID, whose segments are lowercase words joined by dots with
// at least three segments.
func TestReservedOperationsAreNoToolIDs(t *testing.T) {
	for operation := range mcpOperations {
		if segments := strings.Split(operation, "."); len(segments) >= 3 {
			t.Errorf("%q has %d segments, which a tool ID may have", operation, len(segments))
		}
	}
	for _, operation := range []string{OperationMCPInitialize, OperationMCPToolsList} {
		if !strings.Contains(operation, "/") {
			t.Errorf("%q has no slash, which a tool ID lacks", operation)
		}
	}
}

func TestValidateMCPOperationsAndSessions(t *testing.T) {
	for operation := range mcpOperations {
		if err := mcpFields(operation).Validate(); err != nil {
			t.Errorf("Validate(%s) = %v", operation, err)
		}
		f := mcpFields(operation)
		f.Result = "invalid-request" // an error response keeps its size
		if err := f.Validate(); err != nil {
			t.Errorf("Validate(%s error response) = %v", operation, err)
		}
	}
	// Every mcp entry needs a session, an invoke keeps its response size on failure, and one that failed
	// before its tool was known has no operation.
	invoke := Fields{Path: "mcp", Operation: "wiki.pages.list", Version: 1, Connection: "wiki", Effect: "read",
		Result: "not-found", ReqBytes: int64Ptr(300), RespBytes: int64Ptr(150)}
	if err := invoke.Validate(); err == nil {
		t.Error("Validate(mcp invoke, no session) = nil, want a refusal")
	}
	invoke.Session = testSession
	if err := invoke.Validate(); err != nil {
		t.Errorf("Validate(mcp invoke error with session) = %v", err)
	}
	invoke.Operation, invoke.Version, invoke.Effect, invoke.Connection = "", 0, "", ""
	if err := invoke.Validate(); err != nil {
		t.Errorf("Validate(mcp invoke without an operation) = %v", err)
	}

	bad := map[string]func(*Fields){
		"search with a connection": func(f *Fields) { f.Operation, f.Connection = OperationMCPSearch, "wiki" },
		"describe with an effect":  func(f *Fields) { f.Operation, f.Effect = OperationMCPDescribe, "read" },
		"baseline with a version":  func(f *Fields) { f.Operation, f.Version = OperationMCPInitialize, 1 },
		"baseline on cli":          func(f *Fields) { f.Operation, f.Path = OperationMCPToolsList, "cli" },
		"baseline without session": func(f *Fields) { f.Operation, f.Session = OperationMCPInitialize, "" },
		"baseline without request": func(f *Fields) { f.Operation, f.ReqBytes = OperationMCPInitialize, nil },
		"baseline without answer":  func(f *Fields) { f.Operation, f.RespBytes = OperationMCPToolsList, nil },
		"session on cli":           func(f *Fields) { f.Operation, f.Path = "a.b.c", "cli" },
		"session on tui":           func(f *Fields) { f.Operation, f.Path = OperationConnectionCreate, "tui" },
		"short session":            func(f *Fields) { f.Session = testSession[:8] },
		"upper-case session":       func(f *Fields) { f.Session = strings.ToUpper(testSession) },
		"session with text":        func(f *Fields) { f.Session = "client-name-here!" },
	}
	for name, change := range bad {
		f := mcpFields(OperationMCPSearch)
		f.Operation = "a.b.c"
		change(&f)
		if err := f.Validate(); err == nil {
			t.Errorf("Validate(%s) = nil, want an error", name)
		}
	}
	cli := Fields{Path: "cli", Operation: "a.b.c", Result: "not-found", RespBytes: int64Ptr(1)}
	if cli.Validate() == nil {
		t.Error("Validate(cli failure with resp_bytes) = nil, want an error")
	}
}

// Old lines have no session field; new lines carry it before prev_hash. The check value covers the
// serialized line either way, so signed and unsigned, old and new entries verify together.
func TestSessionEntriesMixWithOldOnesInOneChain(t *testing.T) {
	dir := t.TempDir()
	key, _ := testKey(t)
	signed, _ := signedLogger(t, dir, key, time.Date(2026, 4, 1, 8, 0, 0, 0, time.UTC))
	unsigned, _ := clockAt(time.Date(2026, 4, 1, 8, 0, 0, 0, time.UTC))
	unsigned.vaultDir, unsigned.retentionDays = dir, 90

	mustAppend(t, signed, "old.style.signed")
	mustAppend(t, unsigned, "old.style.unsigned")
	for _, logger := range []*Logger{signed, unsigned, signed} {
		if err := logger.Append(mcpFields(OperationMCPInitialize)); err != nil {
			t.Fatal(err)
		}
	}
	mustAppend(t, signed, "old.style.after")

	lines := readLines(t, filepath.Join(signed.logsDir(), "2026-04-01.jsonl"))
	if len(lines) != 6 {
		t.Fatalf("log holds %d lines, want 6", len(lines))
	}
	if strings.Contains(string(lines[0]), "session") || !strings.Contains(string(lines[2]), `"session":"`+testSession+`","prev_hash"`) {
		t.Fatalf("lines = %q, want session only on the new lines, before prev_hash", lines)
	}
	if !strings.HasSuffix(string(lines[2]), `"}`) || strings.Contains(string(lines[2]), `"mac":""`) || !strings.Contains(string(lines[3]), `"mac":""}`) {
		t.Fatalf("signing = %q / %q, want line 2 signed and line 3 not", lines[2], lines[3])
	}
	report := verifyWith(t, dir, key)
	if report.Broken || report.Days[0].Entries != 6 || report.Days[0].Unverified != 2 || report.Days[0].Changed != 0 {
		t.Fatalf("report = %+v, want an intact chain with 2 unsigned of 6", report)
	}

	// Changing the session of a signed line is detected.
	lines[2] = []byte(strings.Replace(string(lines[2]), testSession, "fedcba9876543210", 1))
	writeLines(t, filepath.Join(signed.logsDir(), "2026-04-01.jsonl"), lines)
	if report := verifyWith(t, dir, key); !report.Broken || report.Days[0].Changed != 1 {
		t.Fatalf("report = %+v, want the changed session found", report)
	}
}

func TestAppendKeepsSessionOutOfCutMarkersAndDecodes(t *testing.T) {
	dir := t.TempDir()
	logger, _ := clockAt(time.Date(2026, 4, 1, 8, 0, 0, 0, time.UTC))
	logger.vaultDir, logger.retentionDays = dir, 90
	if err := logger.Append(mcpFields(OperationMCPSearch)); err != nil {
		t.Fatal(err)
	}
	var e Entry
	if err := json.Unmarshal(readLines(t, filepath.Join(logger.logsDir(), "2026-04-01.jsonl"))[0], &e); err != nil ||
		e.Session != testSession || e.Operation != OperationMCPSearch || *e.RespBytes != 2000 {
		t.Fatalf("entry = %+v, %v", e, err)
	}
}

func TestSummarizeSessionsGroupsAreasAndIncludesErrors(t *testing.T) {
	entry := func(session, operation, result string, req, resp *int64) Entry {
		return Entry{Path: "mcp", Session: session, Operation: operation, Result: result, ReqBytes: req, RespBytes: resp}
	}
	entries := []Entry{
		entry("aaaa", OperationMCPInitialize, "success", ptr(100), ptr(1000)),
		entry("aaaa", OperationMCPToolsList, "success", ptr(50), ptr(9000)),
		entry("aaaa", OperationMCPSearch, "success", ptr(80), ptr(500)),
		entry("aaaa", OperationMCPSearch, "invalid-request", ptr(60), ptr(200)),
		entry("aaaa", OperationMCPDescribe, "success", ptr(70), ptr(300)),
		entry("aaaa", "wiki.pages.list", "success", ptr(300), ptr(4000)),
		entry("aaaa", "wiki.pages.list", "not-found", ptr(300), ptr(150)),
		entry("aaaa", "chat.msgs.send", "success", ptr(10), nil),
		// A call that failed before its tool was known has no operation and still is an invoke.
		entry("aaaa", "", "invalid-request", ptr(40), ptr(100)),
		entry("bbbb", OperationMCPInitialize, "success", ptr(100), ptr(1000)),
		// Left out: no session, CLI, other kinds.
		entry("", "wiki.pages.list", "success", ptr(1), ptr(1)),
		entry("", "", "invalid-request", ptr(1), ptr(1)),
		{Path: "cli", Session: "cccc", Operation: "wiki.pages.list", ReqBytes: ptr(1), RespBytes: ptr(1)},
		{Kind: KindCut, Path: "mcp", Session: "aaaa", Operation: OperationMCPSearch},
		{Path: "mcp", Session: "aaaa", Operation: OperationApprove},
	}
	got := SummarizeSessions(entries)
	want := []SessionSummary{
		{Session: "aaaa", Areas: []AreaSummary{
			{Area: AreaBaseline, ToolBytes: ToolBytes{Calls: 2, ReqBytes: 150, RespBytes: 10000}, Tools: []ToolBytes{
				{Operation: OperationMCPToolsList, Calls: 1, ReqBytes: 50, RespBytes: 9000},
				{Operation: OperationMCPInitialize, Calls: 1, ReqBytes: 100, RespBytes: 1000}}},
			{Area: AreaNavigation, ToolBytes: ToolBytes{Calls: 3, ReqBytes: 210, RespBytes: 1000}, Tools: []ToolBytes{
				{Operation: OperationMCPSearch, Calls: 2, ReqBytes: 140, RespBytes: 700},
				{Operation: OperationMCPDescribe, Calls: 1, ReqBytes: 70, RespBytes: 300}}},
			{Area: AreaInvoke, ToolBytes: ToolBytes{Calls: 4, ReqBytes: 650, RespBytes: 4250}, Tools: []ToolBytes{
				{Operation: "wiki.pages.list", Calls: 2, ReqBytes: 600, RespBytes: 4150},
				{Operation: "", Calls: 1, ReqBytes: 40, RespBytes: 100},
				{Operation: "chat.msgs.send", Calls: 1, ReqBytes: 10}}},
		}},
		{Session: "bbbb", Areas: []AreaSummary{
			{Area: AreaBaseline, ToolBytes: ToolBytes{Calls: 1, ReqBytes: 100, RespBytes: 1000}, Tools: []ToolBytes{
				{Operation: OperationMCPInitialize, Calls: 1, ReqBytes: 100, RespBytes: 1000}}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SummarizeSessions() = %+v\nwant %+v", got, want)
	}
	if SummarizeSessions(nil) == nil {
		t.Error("SummarizeSessions(nil) = nil, want an empty slice")
	}
}

func TestIsReservedOperation(t *testing.T) {
	for operation := range mcpOperations {
		if !IsReservedOperation(operation) {
			t.Errorf("IsReservedOperation(%s) = false", operation)
		}
	}
	if IsReservedOperation("wiki.pages.list") || IsReservedOperation("") {
		t.Error("IsReservedOperation(tool or empty) = true")
	}
}

func TestIsInvokeExcludesBaselineAndNavigation(t *testing.T) {
	for operation := range mcpOperations {
		if IsInvoke(Entry{Path: "mcp", Operation: operation}) {
			t.Errorf("IsInvoke(%s) = true", operation)
		}
	}
	if !IsInvoke(Entry{Path: "mcp", Operation: "wiki.pages.list"}) {
		t.Error("IsInvoke(tool) = false")
	}
	got := Summarize([]Entry{
		{Path: "mcp", Operation: OperationMCPSearch, RespBytes: ptr(5)},
		{Path: "mcp", Operation: "a.b.c", RespBytes: ptr(7)},
	})
	if len(got) != 1 || got[0].Operation != "a.b.c" {
		t.Fatalf("Summarize() = %+v, want only the tool", got)
	}
}
