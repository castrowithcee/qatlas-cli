package config

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

var idKey = regexp.MustCompile(`(?m)^\s+id: `)

const (
	idA = "0123456789abcdef0123456789abcdef"
	idB = "fedcba9876543210fedcba9876543210"
)

func withWriteVersion(t *testing.T, v int) {
	t.Helper()
	old := writeVersion
	writeVersion = v
	t.Cleanup(func() { writeVersion = old })
}

func TestDuplicateIDsNameBothKeys(t *testing.T) {
	cfg := sample(t)
	must(t, cfg.SetService("a", Service{ID: idA, Provider: "bookstack"}))
	must(t, cfg.SetService("b", Service{ID: idA, Provider: "bookstack"}))
	// The same id in another type is a separate space.
	must(t, cfg.SetCredential("reader", Credential{ID: idA, Type: CredentialTypeKeyring}))

	err := cfg.Validate()

	if err == nil || !strings.Contains(err.Error(), "services.b.id") || !strings.Contains(err.Error(), "services.a.id") {
		t.Fatalf("Validate() = %v, want both services.a.id and services.b.id", err)
	}
	if strings.Contains(err.Error(), "credentials") {
		t.Errorf("an id shared across types is reported: %v", err)
	}
}

func TestMalformedIDIsInvalid(t *testing.T) {
	cfg := sample(t)
	must(t, cfg.SetConnection("wiki", Connection{ID: "short", Service: "wiki", Credential: "reader"}))
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "connections.wiki.id") {
		t.Fatalf("Validate() = %v, want connections.wiki.id", err)
	}
}

func TestEntryWithoutIDIsValidAndGetsOneWhenIDsAreWritten(t *testing.T) {
	withWriteVersion(t, versionIDs)
	store, path := newTarget(t)
	cfg := sample(t)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}

	must(t, store.Save(cfg))

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "version: 2") || len(idKey.FindAll(data, -1)) != 3 {
		t.Fatalf("file lacks version 2 and three ids:\n%s", data)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Services["wiki"].ID != cfg.Services["wiki"].ID || cfg.Services["wiki"].ID == "" ||
		loaded.Credentials["reader"].ID == "" || loaded.Connections["wiki"].ID == "" {
		t.Errorf("ids not assigned and kept: %+v", loaded)
	}
}

func TestSchemaOneWritesNoIDs(t *testing.T) {
	store, path := newTarget(t)
	cfg := sample(t)
	must(t, cfg.SetService("wiki", Service{ID: idA, Provider: "bookstack", BaseURL: "https://wiki.example.invalid"}))
	must(t, cfg.SetConnection("wiki", Connection{ID: idB, Service: "wiki", Credential: "reader"}))

	must(t, store.Save(cfg))

	data, _ := os.ReadFile(path)
	if idKey.Match(data) || !strings.Contains(string(data), "version: 1") {
		t.Fatalf("schema 1 file must have no id:\n%s", data)
	}
}

func TestSetKeepsExistingID(t *testing.T) {
	cfg := New(testProviders)
	must(t, cfg.SetService("s", Service{ID: idA, Provider: "bookstack"}))
	must(t, cfg.SetCredential("c", Credential{ID: idA, Type: CredentialTypeKeyring}))
	must(t, cfg.SetConnection("n", Connection{ID: idA, Service: "s", Credential: "c"}))

	must(t, cfg.SetService("s", Service{Provider: "bookstack", BaseURL: "https://x.example.invalid"}))
	must(t, cfg.SetCredential("c", Credential{Type: CredentialTypeKeyring}))
	must(t, cfg.SetConnection("n", Connection{Service: "s", Credential: "c", Description: "d"}))

	if cfg.Services["s"].ID != idA || cfg.Credentials["c"].ID != idA || cfg.Connections["n"].ID != idA {
		t.Errorf("an editor that rebuilds an entry lost its id: %+v", cfg)
	}
}

func TestIDAssignmentIsNoConnectionChange(t *testing.T) {
	withWriteVersion(t, versionIDs)
	store, _ := newTarget(t)
	cfg := sample(t)
	before := cfg.Clone()

	must(t, store.Save(cfg))

	if cfg.Connections["wiki"].ID == "" {
		t.Fatal("no id assigned")
	}
	if got := ChangedConnections(before, cfg); len(got) != 0 {
		t.Errorf("ChangedConnections() = %v, want none", got)
	}
}

func TestVersionNinetyNineReportsVersion(t *testing.T) {
	in := strings.Replace(minimal, "version: 1", "version: 99\nsurprise: true", 1)
	_, err := Decode(strings.NewReader(in), testProviders)
	if err == nil || !strings.Contains(err.Error(), "version: got 99") || strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("Decode() = %v, want a version message only", err)
	}
}

func TestVersionTwoWithIDsLoadsAndStaysVersionTwo(t *testing.T) {
	in := strings.NewReplacer("version: 1", "version: 2",
		"  wiki:\n    provider", "  wiki:\n    id: "+idA+"\n    provider",
		"  reader:\n", "  reader:\n    id: "+idB+"\n",
		"  wiki:\n    service", "  wiki:\n    id: "+idA+"\n    service").Replace(minimal)
	store, path := newTarget(t)
	must(t, os.MkdirAll(strings.TrimSuffix(path, "config.yaml"), 0o700))
	must(t, os.WriteFile(path, []byte(in), 0o600))

	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if cfg.Services["wiki"].ID != idA || cfg.Credentials["reader"].ID != idB || cfg.Connections["wiki"].ID != idA {
		t.Fatalf("ids not loaded: %+v", cfg)
	}
	// Saving with the schema-1 default must not drop the ids of a schema-2 file.
	must(t, store.Save(cfg))
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "version: 2") || strings.Count(string(data), idA) != 2 {
		t.Errorf("ids or version lost:\n%s", data)
	}
}
