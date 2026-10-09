package tui

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// connectionEntries reads every entry the log under the vault directory next to configPath holds.
func connectionEntries(t *testing.T, configPath string) []invokelog.Entry {
	t.Helper()
	logs := filepath.Join(vault.New(filepath.Dir(configPath)).Dir(), "logs")
	files, _ := filepath.Glob(filepath.Join(logs, "*.jsonl"))
	var entries []invokelog.Entry
	for _, name := range files {
		f, err := os.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var e invokelog.Entry
			if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
				t.Fatal(err)
			}
			entries = append(entries, e)
		}
		f.Close()
	}
	return entries
}

func wantEntry(t *testing.T, e invokelog.Entry, operation, effect, connection string) {
	t.Helper()
	if e.Path != "tui" || e.Operation != operation || e.Effect != effect || e.Connection != connection ||
		e.Result != "success" || e.Token != "" || e.Time.IsZero() {
		t.Errorf("entry = %+v, want tui %s %s %s success", e, operation, effect, connection)
	}
}

func TestEditorLogsOneEntryPerChangedConnection(t *testing.T) {
	m, _, path := newModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "WIKI_ID", "WIKI_SECRET")
	if got := connectionEntries(t, path); len(got) != 0 {
		t.Fatalf("a service and a credential logged %d entries, want none", len(got))
	}

	addConnection(t, m, "wiki", "wiki", "reader")
	if m.fail != "" {
		t.Fatalf("editor reported %q", m.fail)
	}
	entries := connectionEntries(t, path)
	if len(entries) != 1 {
		t.Fatalf("creating logged %d entries, want 1", len(entries))
	}
	wantEntry(t, entries[0], invokelog.OperationConnectionCreate, "create", "wiki")

	// Saving without a change to the connection logs nothing.
	openEntryForm(t, m, sectionConnections, "wiki")
	press(t, m, "enter")
	if len(connectionEntries(t, path)) != 1 {
		t.Fatalf("an unchanged save logged an entry")
	}

	openEntryForm(t, m, sectionConnections, "wiki")
	focusField(t, m, "description")
	typeText(t, m, "audit")
	press(t, m, "f2")
	entries = connectionEntries(t, path)
	if len(entries) != 2 {
		t.Fatalf("changing logged %d entries in total, want 2", len(entries))
	}
	wantEntry(t, entries[1], invokelog.OperationConnectionChange, "update", "wiki")

	openSectionByName(t, m, sectionConnections)
	press(t, m, "d")
	press(t, m, "y")
	entries = connectionEntries(t, path)
	if len(entries) != 3 {
		t.Fatalf("deleting logged %d entries in total, want 3 (status %q, fail %q)", len(entries), m.status, m.fail)
	}
	wantEntry(t, entries[2], invokelog.OperationConnectionDelete, "delete", "wiki")

	raw, _ := os.ReadFile(filepath.Join(vault.New(filepath.Dir(path)).Dir(), "logs",
		entries[0].Time.UTC().Format("2006-01-02")+".jsonl"))
	for _, value := range []string{"example.invalid", "audit", "WIKI_ID"} {
		if strings.Contains(string(raw), value) {
			t.Errorf("the log carries the value %q", value)
		}
	}
}

func TestAConflictLogsNoConnectionChange(t *testing.T) {
	m, store, path := newModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "WIKI_ID", "WIKI_SECRET")
	openSectionByName(t, m, sectionConnections)
	pressNew(t, m)
	typeText(t, m, "wiki")
	press(t, m, "tab")
	selectChoice(t, m, m.cfg.Services["wiki"].Provider)
	press(t, m, "tab")
	selectChoice(t, m, "wiki")
	press(t, m, "tab")
	selectChoice(t, m, "reader")

	foreignWriters[0].write(t, store, "theirs")
	press(t, m, "f2")
	if !strings.Contains(m.fail, "changed outside this editor") {
		t.Fatalf("fail = %q, want a conflict", m.fail)
	}
	if got := connectionEntries(t, path); len(got) != 0 {
		t.Errorf("a conflict logged %d entries, want none", len(got))
	}
}

// A log that cannot be written is a warning; the connection stays saved.
func TestALogFailureIsAWarningAndKeepsTheSave(t *testing.T) {
	m, _, path := newModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "WIKI_ID", "WIKI_SECRET")
	// A file where the logs directory belongs.
	vaultDir := vault.New(filepath.Dir(path)).Dir()
	if err := os.MkdirAll(vaultDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vaultDir, "logs"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	addConnection(t, m, "wiki", "wiki", "reader")
	if m.fail != "" {
		t.Fatalf("a log failure was reported as an error: %q", m.fail)
	}
	if !strings.HasPrefix(m.status, "Saved wiki") || !strings.Contains(m.status, "warning: the change was saved") {
		t.Errorf("status = %q, want Saved with a warning", m.status)
	}
	if _, ok := savedConfig(t, path).Connections["wiki"]; !ok {
		t.Error("the connection was not saved")
	}
}

// The warning of a failed log reaches the status line redacted.
func TestALogFailureWarningIsRedactedInTheStatusLine(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	secrets, _ := newResolver(t, dir, nil)
	const marked = "the change was saved"
	red := &redact.Redactor{}
	red.Add(marked)
	m, err := buildModel(newTestStore(t, path), nil, secrets, red)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addCredential(t, m, "reader", "WIKI_ID", "WIKI_SECRET")
	vaultDir := vault.New(filepath.Dir(path)).Dir()
	if err := os.MkdirAll(vaultDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vaultDir, "logs"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	addConnection(t, m, "wiki", "wiki", "reader")
	if m.fail != "" {
		t.Fatalf("a log failure was reported as an error: %q", m.fail)
	}
	if strings.Contains(m.status, marked) || !strings.Contains(m.status, redact.Marker) {
		t.Errorf("status = %q, want the log warning redacted", m.status)
	}
}

func TestGuidedSetupLogsTheNewConnection(t *testing.T) {
	m, _, path, _, _ := newStoreModel(t)
	walkSetup(t, m, stepSummary)
	pump(t, m, "f2")
	if m.fail != "" || m.wizard.saved == "" {
		t.Fatalf("save failed: %q", m.fail)
	}
	entries := connectionEntries(t, path)
	if len(entries) != 1 {
		t.Fatalf("the setup logged %d entries, want 1", len(entries))
	}
	wantEntry(t, entries[0], invokelog.OperationConnectionCreate, "create", m.wizard.saved)
}

func TestAFailedSetupSaveLogsNothing(t *testing.T) {
	m, _, path, _, _ := newStoreModel(t)
	walkSetup(t, m, stepSummary)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	pump(t, m, "f2")
	if m.wizard.saved != "" {
		t.Fatalf("the save did not fail")
	}
	if got := connectionEntries(t, filepath.Join(filepath.Dir(path), "config.yaml")); len(got) != 0 {
		t.Errorf("a failed save logged %d entries", len(got))
	}
}

// A change of a service that shifts what an approval of a connection covers is a change of that connection,
// logged for every vault connection that reads the service and for no other.
func TestServiceChangeLogsTheConnectionsItReaches(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	secrets, _ := newVaultResolver(t, dir)
	m, err := buildModel(newTestStore(t, path), nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addService(t, m, "zother", "https://other.example.invalid")
	addCredential(t, m, "envcred", "WIKI_ID", "WIKI_SECRET")
	addVaultCredential(t, m, "reader")
	for _, c := range []struct{ name, service, credential string }{
		{"c1", "wiki", "reader"}, {"c2", "wiki", "reader"}, {"c3", "zother", "reader"}, {"c4", "wiki", "envcred"},
	} {
		addConnection(t, m, c.name, c.service, c.credential)
		if m.fail != "" {
			t.Fatalf("addConnection(%s) reported %q", c.name, m.fail)
		}
	}
	// A service and the credentials logged nothing; only the four connections were created.
	before := len(connectionEntries(t, path))
	if before != 4 {
		t.Fatalf("setup logged %d entries, want the 4 creations", before)
	}

	openEntryForm(t, m, sectionServices, "wiki")
	focusField(t, m, "base url")
	clearField(t, m)
	typeText(t, m, "https://moved.example.invalid")
	press(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("editor reported %q", m.fail)
	}
	entries := connectionEntries(t, path)[before:]
	got := map[string]bool{}
	for _, e := range entries {
		wantEntry(t, e, invokelog.OperationConnectionChange, "update", e.Connection)
		got[e.Connection] = true
	}
	if len(entries) != 2 || !got["c1"] || !got["c2"] {
		t.Errorf("entries = %+v, want one change each for c1 and c2 only", entries)
	}

	// A description of the service reaches no scope: nothing is logged.
	before = len(connectionEntries(t, path))
	openEntryForm(t, m, sectionServices, "wiki")
	press(t, m, "f2")
	if got := len(connectionEntries(t, path)); got != before {
		t.Errorf("an unchanged service logged %d entries", got-before)
	}
}
