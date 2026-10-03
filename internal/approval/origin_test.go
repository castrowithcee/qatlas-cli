package approval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
)

func logChange(t *testing.T, logger *invokelog.Logger, surface, operation, connection string) {
	t.Helper()
	effect := "update"
	if operation == invokelog.OperationConnectionCreate {
		effect = "create"
	}
	err := logger.Append(invokelog.Fields{Path: surface, Operation: operation, Connection: connection,
		Effect: effect, Result: "success"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOriginsFindsTheYoungestEntryAfterTheLastApproval(t *testing.T) {
	_, v := fixture(t)
	dir := v.Dir()
	key, err := v.LogKey()
	if err != nil {
		t.Fatal(err)
	}
	defer key.Clear()
	logger := invokelog.New(dir, 90).WithKey(key)
	logChange(t, logger, "web", invokelog.OperationConnectionChange, "alpha")
	logChange(t, logger, "tui", invokelog.OperationConnectionChange, "alpha")
	logChange(t, logger, "cli", invokelog.OperationConnectionChange, "other")

	now := time.Now()
	change := Change{Connection: "alpha", Approved: now.Add(-time.Hour)}
	got := Origins(dir, key, []Change{change}, now, now, 90)["alpha"]
	if got.Source != OriginTUI || !got.Verified || got.Seq != 2 || got.Time.IsZero() {
		t.Fatalf("origin = %+v, want a verified tui entry #2", got)
	}
	if text := got.Text(); !strings.HasPrefix(text, "changed in qatlas tui, ") || strings.Contains(text, "unverified") {
		t.Fatalf("Text() = %q", text)
	}

	// Without a checker the same entry is shown, marked unverified.
	got = Origins(dir, nil, []Change{change}, now, now, 90)["alpha"]
	if got.Source != OriginTUI || got.Verified || !strings.HasSuffix(got.Text(), "(unverified)") {
		t.Fatalf("unchecked origin = %+v / %q", got, got.Text())
	}
}

func TestOriginsIgnoresEntriesBeforeTheLastApproval(t *testing.T) {
	dir := t.TempDir()
	logChange(t, invokelog.New(dir, 90), "tui", invokelog.OperationConnectionChange, "alpha")
	now := time.Now()
	modified := now.Add(-3 * time.Minute)
	change := Change{Connection: "alpha", Approved: now.Add(time.Hour)}
	got := Origins(dir, nil, []Change{change}, modified, now, 90)["alpha"]
	if got.Source != OriginOutside || !got.Time.Equal(modified) || got.Seq != 0 {
		t.Fatalf("origin = %+v, want outside with the file time", got)
	}
	if text := got.Text(); !strings.Contains(text, "outside qatlas") || !strings.Contains(text, "last modified") {
		t.Fatalf("Text() = %q", text)
	}
}

func TestOriginsIgnoresDeletesAndForeignPaths(t *testing.T) {
	dir := t.TempDir()
	logger := invokelog.New(dir, 90)
	logChange(t, logger, "tui", invokelog.OperationConnectionDelete, "alpha")
	logChange(t, logger, "mcp", invokelog.OperationConnectionChange, "alpha")
	now := time.Now()
	got := Origins(dir, nil, []Change{{Connection: "alpha", New: true}}, now, now, 90)["alpha"]
	if got.Source != OriginOutside {
		t.Fatalf("origin = %+v, want outside", got)
	}
}

func TestOriginsOfANewConnectionUsesTheRetentionWindow(t *testing.T) {
	dir := t.TempDir()
	logChange(t, invokelog.New(dir, 90), "cli", invokelog.OperationConnectionCreate, "alpha")
	now := time.Now()
	got := Origins(dir, nil, []Change{{Connection: "alpha", New: true}}, now, now, 90)["alpha"]
	if got.Source != OriginCLI {
		t.Fatalf("origin = %+v, want cli", got)
	}
}

func TestOriginsCredentialStoredAnew(t *testing.T) {
	change := Change{Connection: "alpha", After: Change{}.After, Fields: []FieldChange{
		{Field: FieldCredential, Before: "cred", After: "cred (stored anew)"}}}
	change.After.Credential = "cred"
	now := time.Now()
	// The log is never read: a directory that is no vault proves it.
	got := Origins(filepath.Join(t.TempDir(), "missing"), nil, []Change{change}, now, now, 90)["alpha"]
	if got.Source != OriginVault || got.Seq != 0 {
		t.Fatalf("origin = %+v, want vault", got)
	}
	if text := got.Text(); !strings.Contains(text, "vault entry of cred was stored anew") || strings.Contains(text, "outside") {
		t.Fatalf("Text() = %q", text)
	}
	// Another field changed too: the log decides.
	change.Fields = append(change.Fields, FieldChange{Field: FieldOrigin, Before: "a", After: "b"})
	if got := Origins(t.TempDir(), nil, []Change{change}, now, now, 90)["alpha"]; got.Source != OriginOutside {
		t.Fatalf("origin = %+v, want outside", got)
	}
}

func TestOriginsUnreadableLogIsUnknown(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "logs"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	got := Origins(dir, nil, []Change{{Connection: "alpha", New: true}}, now, now, 90)["alpha"]
	if got.Source != OriginUnknown || got.Text() != "origin unknown: the log could not be read" {
		t.Fatalf("origin = %+v / %q", got, got.Text())
	}
}
