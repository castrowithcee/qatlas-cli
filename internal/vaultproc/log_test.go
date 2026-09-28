package vaultproc

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
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
		DurationMS: 12}
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
