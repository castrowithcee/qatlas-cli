package vaultproc

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
)

func testLogKey(t *testing.T) *invokelog.Key {
	t.Helper()
	key, err := invokelog.DeriveKey(testKey)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func goodEntry() *logEntry {
	return &logEntry{Path: "mcp", Client: &invokelog.ClientInfo{Name: "client", Version: "1"},
		Operation: "bookstack.pages.list", Version: 1, Connection: "wiki", Effect: "read", Result: "success",
		DurationMS: 12, Session: "fedcba9876543210"}
}

// logLines returns every line the log below vaultDir holds.
func logLines(t *testing.T, vaultDir string) [][]byte {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(vaultDir, "logs", "*.jsonl"))
	var lines [][]byte
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")) {
			if len(line) > 0 {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

func TestServerSignsTheLogItKeeps(t *testing.T) {
	dir := t.TempDir()
	s := testServer()
	s.KeepLog(dir, 90)
	c := testClient()

	for i := 0; i < 3; i++ {
		if _, err := exchangeOverPipe(t, s, c, request{Op: opLog, Entry: goodEntry()}); err != nil {
			t.Fatalf("log %d error = %v", i, err)
		}
	}
	lines := logLines(t, dir)
	if len(lines) != 3 {
		t.Fatalf("the log holds %d lines, want 3", len(lines))
	}
	for _, line := range lines {
		if bytes.HasSuffix(line, []byte(`"mac":""}`)) {
			t.Fatalf("line %s is unsigned", line)
		}
	}
	report, err := invokelog.VerifyWith(dir, testLogKey(t))
	if err != nil || report.Broken || report.Days[0].Entries != 3 || report.Days[0].Unverified != 0 {
		t.Fatalf("VerifyWith() = %+v, %v, want 3 signed entries in an intact chain", report, err)
	}

	// logcheck answers whether each line matches, and nothing else.
	changed := bytes.Replace(lines[2], []byte(`"wiki"`), []byte(`"crm"`), 1)
	resp, err := exchangeOverPipe(t, s, c, request{Op: opLogCheck, Lines: [][]byte{lines[0], changed, []byte("{}")}})
	if err != nil || len(resp.Valid) != 3 || !resp.Valid[0] || resp.Valid[1] || resp.Valid[2] {
		t.Fatalf("logcheck = %+v, %v, want valid, changed, malformed", resp, err)
	}
	if resp.Value != "" || resp.Proof != nil {
		t.Fatalf("logcheck answered with more than its results: %+v", resp)
	}
	if _, err := exchangeOverPipe(t, s, c, request{Op: opLogCheck}); err == nil {
		t.Fatal("logcheck without lines succeeded")
	}
}

func int64Ptr(v int64) *int64 { return &v }

func TestServerLogsSizeCounters(t *testing.T) {
	dir := t.TempDir()
	s := testServer()
	s.KeepLog(dir, 90)
	c := testClient()

	ok := goodEntry()
	ok.ReqBytes, ok.RespBytes = int64Ptr(10), int64Ptr(0)
	failed := goodEntry()
	failed.Result, failed.ReqBytes = "unknown-operation", int64Ptr(2)
	for _, e := range []*logEntry{goodEntry(), ok, failed} {
		if _, err := exchangeOverPipe(t, s, c, request{Op: opLog, Entry: e}); err != nil {
			t.Fatalf("log error = %v", err)
		}
	}
	lines := logLines(t, dir)
	if len(lines) != 3 {
		t.Fatalf("the log holds %d lines, want 3", len(lines))
	}
	if bytes.Contains(lines[0], []byte("_bytes")) {
		t.Errorf("line without counters carries one: %s", lines[0])
	}
	if !bytes.Contains(lines[1], []byte(`"req_bytes":10,"resp_bytes":0,`)) {
		t.Errorf("line = %s, want both counters", lines[1])
	}
	if !bytes.Contains(lines[2], []byte(`"req_bytes":2,`)) || bytes.Contains(lines[2], []byte("resp_bytes")) {
		t.Errorf("failure line = %s, want req_bytes only", lines[2])
	}
	report, err := invokelog.VerifyWith(dir, testLogKey(t))
	if err != nil || report.Broken || report.Days[0].Entries != 3 || report.Days[0].Unverified != 0 {
		t.Fatalf("VerifyWith() = %+v, %v, want an intact signed chain", report, err)
	}
}

// The session ID and the mcp baseline and navigation operations cross the protocol, are signed with the rest
// of the line, and an error response keeps its size on the mcp path.
func TestServerSignsSessionEntries(t *testing.T) {
	dir := t.TempDir()
	s := testServer()
	s.KeepLog(dir, 90)
	c := testClient()
	const session = "0123456789abcdef"

	baseline := &logEntry{Path: "mcp", Operation: invokelog.OperationMCPToolsList, Result: "success",
		Session: session, ReqBytes: int64Ptr(60), RespBytes: int64Ptr(9000)}
	failedSearch := &logEntry{Path: "mcp", Operation: invokelog.OperationMCPSearch, Result: "invalid-request",
		Session: session, ReqBytes: int64Ptr(80), RespBytes: int64Ptr(200)}
	cliEntry := goodEntry()
	cliEntry.Path, cliEntry.Session = "cli", ""
	for _, e := range []*logEntry{baseline, failedSearch, cliEntry} {
		if _, err := exchangeOverPipe(t, s, c, request{Op: opLog, Entry: e}); err != nil {
			t.Fatalf("log error = %v", err)
		}
	}
	lines := logLines(t, dir)
	if len(lines) != 3 || !bytes.Contains(lines[0], []byte(`"session":"`+session+`","prev_hash"`)) ||
		bytes.Contains(lines[2], []byte("session")) {
		t.Fatalf("lines = %q, want the session before prev_hash on the mcp entries only", lines)
	}
	report, err := invokelog.VerifyWith(dir, testLogKey(t))
	if err != nil || report.Broken || report.Days[0].Unverified != 0 {
		t.Fatalf("VerifyWith() = %+v, %v, want an intact signed chain", report, err)
	}
	changed := bytes.Replace(lines[0], []byte(session), []byte("fedcba9876543210"), 1)
	resp, err := exchangeOverPipe(t, s, c, request{Op: opLogCheck, Lines: [][]byte{lines[0], changed}})
	if err != nil || len(resp.Valid) != 2 || !resp.Valid[0] || resp.Valid[1] {
		t.Fatalf("logcheck = %+v, %v, want the changed session detected", resp, err)
	}
}

// TestLogEntryDecodesAcrossVersions covers a client of the same protocol version that predates the
// counters, and one that adds fields this server does not know.
func TestLogEntryDecodesAcrossVersions(t *testing.T) {
	var old logEntry
	if err := json.Unmarshal([]byte(`{"path":"cli","operation":"a.b","result":"success","duration_ms":3}`), &old); err != nil {
		t.Fatal(err)
	}
	if f := old.fields(); f.Validate() != nil || f.ReqBytes != nil || f.RespBytes != nil {
		t.Fatalf("old entry fields = %+v, want a valid entry without counters", f)
	}
	var newer logEntry
	if err := json.Unmarshal([]byte(`{"path":"cli","result":"success","req_bytes":4,"resp_bytes":5,"later_field":[1]}`), &newer); err != nil {
		t.Fatal(err)
	}
	if f := newer.fields(); f.ReqBytes == nil || *f.ReqBytes != 4 || *f.RespBytes != 5 {
		t.Fatalf("newer entry fields = %+v", f)
	}
}

func TestServerRefusesInvalidLogFields(t *testing.T) {
	dir := t.TempDir()
	s := testServer()
	s.KeepLog(dir, 90)
	c := testClient()

	bad := map[string]func(*logEntry){
		"unknown path":         func(e *logEntry) { e.Path = "web" },
		"free-text result":     func(e *logEntry) { e.Result = "it worked" },
		"unknown effect":       func(e *logEntry) { e.Effect = "wipe" },
		"injected line":        func(e *logEntry) { e.Operation = "x\n{\"seq\":1,\"prev_hash\":\"\"}" },
		"terminal escape":      func(e *logEntry) { e.Connection = "\x1b]0;owned\x07" },
		"overlong operation":   func(e *logEntry) { e.Operation = strings.Repeat("o", 300) },
		"overlong client":      func(e *logEntry) { e.Client = &invokelog.ClientInfo{Name: strings.Repeat("n", 200)} },
		"negative duration":    func(e *logEntry) { e.DurationMS = -1 },
		"overflowing duration": func(e *logEntry) { e.DurationMS = 1 << 62 },
		"negative version":     func(e *logEntry) { e.Version = -3 },
		"negative req_bytes":   func(e *logEntry) { e.ReqBytes = int64Ptr(-1) },
		"huge req_bytes":       func(e *logEntry) { e.ReqBytes = int64Ptr(invokelog.MaxReqBytes + 1) },
		"negative resp_bytes":  func(e *logEntry) { e.RespBytes = int64Ptr(-1) },
		"huge resp_bytes":      func(e *logEntry) { e.RespBytes = int64Ptr(invokelog.MaxRespBytes + 1) },
		"resp_bytes on error":  func(e *logEntry) { e.Path = "cli"; e.Result = "not-found"; e.RespBytes = int64Ptr(1) },
		"session on cli":       func(e *logEntry) { e.Path = "cli"; e.Session = "0123456789abcdef" },
		"malformed session":    func(e *logEntry) { e.Session = "ABC" },
		"mcp without session":  func(e *logEntry) { e.Session = "" },
		"search with a connection": func(e *logEntry) {
			e.Operation, e.Session, e.Effect, e.Version = invokelog.OperationMCPSearch, "0123456789abcdef", "", 0
			e.ReqBytes, e.RespBytes = int64Ptr(1), int64Ptr(1)
		},
	}
	for name, change := range bad {
		entry := goodEntry()
		change(entry)
		if _, err := exchangeOverPipe(t, s, c, request{Op: opLog, Entry: entry}); err == nil {
			t.Errorf("log with %s succeeded", name)
		}
	}
	if _, err := exchangeOverPipe(t, s, c, request{Op: opLog}); err == nil {
		t.Error("log without an entry succeeded")
	}
	if lines := logLines(t, dir); len(lines) != 0 {
		t.Fatalf("refused requests wrote %d lines: %s", len(lines), lines)
	}
}

func TestServerWithoutALogRefusesLogRequests(t *testing.T) {
	s := testServer()
	c := testClient()
	if _, err := exchangeOverPipe(t, s, c, request{Op: opLog, Entry: goodEntry()}); !errors.Is(err, ErrNoLog) {
		t.Fatalf("log without KeepLog error = %v, want ErrNoLog", err)
	}
}

func TestLockedServerRefusesLogRequests(t *testing.T) {
	dir := t.TempDir()
	s := testServer()
	s.KeepLog(dir, 90)
	s.wipe()
	if resp := s.answerLog(request{Op: opLog, Entry: goodEntry()}); resp.Error != codeLocked {
		t.Fatalf("log after wipe = %+v, want locked", resp)
	}
	if s.logKey != nil || s.log != nil {
		t.Fatal("wipe kept the log key")
	}
	if lines := logLines(t, dir); len(lines) != 0 {
		t.Fatalf("a locked server wrote %d lines", len(lines))
	}
}

func TestLogRequestNeverCarriesTheKey(t *testing.T) {
	dir := t.TempDir()
	s := testServer()
	s.KeepLog(dir, 90)
	c := testClient()
	resp, err := exchangeOverPipe(t, s, c, request{Op: opLog, Entry: goodEntry()})
	if err != nil {
		t.Fatal(err)
	}
	encoded := strings.ToLower(resp.Value + string(resp.Proof))
	if strings.Contains(encoded, strings.ToLower(testKey.String())) {
		t.Fatal("the answer holds the vault key")
	}
}

func TestLogEntryCarriesTheRenameFields(t *testing.T) {
	f := invokelog.Fields{Path: "cli", Operation: invokelog.OperationConnectionRename, Connection: "new",
		ConnectionID: "0123456789abcdef0123456789abcdef", FormerConnection: "old", Effect: "update", Result: "success"}
	data, err := json.Marshal(logEntryOf(f))
	if err != nil {
		t.Fatal(err)
	}
	var decoded logEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.fields(); !reflect.DeepEqual(got, f) {
		t.Fatalf("fields = %+v, want %+v", got, f)
	}
}

// A server kept with a retention of zero never removes a day file.
func TestServerWithoutRetentionKeepsOldDays(t *testing.T) {
	dir := t.TempDir()
	logs := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(logs, "2020-01-01.jsonl")
	if err := os.WriteFile(old, []byte(`{"seq":1,"time":"2020-01-01T00:00:00Z","path":"cli","operation":"old.entry"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := testServer()
	s.KeepLog(dir, 0)
	if _, err := exchangeOverPipe(t, s, testClient(), request{Op: opLog, Entry: goodEntry()}); err != nil {
		t.Fatalf("log error = %v", err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatalf("the server removed an old day file: %v", err)
	}
}
