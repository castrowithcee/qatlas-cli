package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var renameProviders ProviderCatalog = testProviderCatalog{
	"acme.chat": {
		ID: "acme.chat", Name: "Acme Chat",
		SecretRoles: []SecretRole{{Name: "token"}},
		Tools: []ToolMetadata{
			{ID: "acme.chat.messages.get", Effect: EffectRead},
			{ID: "acme.chat.messages.list", Effect: EffectRead},
		},
		RenamedTools: map[string]string{"acmechat.messages.fetch": "acme.chat.messages.get"},
		FormerIDs:    []string{"acmechat"},
		RemovedTools: []string{"acmechat.messages.purge"},
	},
}

const renamedConfig = `version: 1
services:
  svc:
    provider: acmechat
    base_url: https://example.invalid
credentials:
  cred:
    type: env
    provider: acmechat
    values:
      token: ACME_TOKEN
connections:
  conn:
    service: svc
    credential: cred
    permissions: [read]
    tools: [acmechat.messages.fetch, acme.chat.messages.list, acmechat.messages.purge]
provider_notes:
  acmechat: team chat
defaults:
  connections:
    acmechat: conn
    acmechat.messages.fetch: conn
`

func writeRenamed(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMapsFormerNamesWarnsAndLeavesFileUntouched(t *testing.T) {
	path := writeRenamed(t, renamedConfig)
	cfg, err := Load(path, renameProviders)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if got := cfg.Services["svc"].Provider; got != "acme.chat" {
		t.Errorf("service provider = %q", got)
	}
	if got := cfg.Credentials["cred"].Provider; got != "acme.chat" {
		t.Errorf("credential provider = %q", got)
	}
	wantTools := []string{"acme.chat.messages.get", "acme.chat.messages.list", "acmechat.messages.purge"}
	if got := cfg.Connections["conn"].Tools; !reflect.DeepEqual(got, wantTools) {
		t.Errorf("tools = %v, want %v", got, wantTools)
	}
	if got := cfg.ProviderNotes; !reflect.DeepEqual(got, map[string]string{"acme.chat": "team chat"}) {
		t.Errorf("provider notes = %v", got)
	}
	wantDefaults := map[string]string{"acme.chat": "conn", "acme.chat.messages.get": "conn"}
	if got := cfg.Defaults.Connections; !reflect.DeepEqual(got, wantDefaults) {
		t.Errorf("defaults = %v, want %v", got, wantDefaults)
	}

	// One warning per mapped entry, then the removed tool.
	warnings := strings.Join(cfg.ToolWarnings(), "\n")
	for _, want := range []string{
		`services.svc.provider: provider "acmechat" was renamed to "acme.chat"`,
		`credentials.cred.provider: provider "acmechat" was renamed to "acme.chat"`,
		`connections.conn.tools: tool "acmechat.messages.fetch" was renamed to "acme.chat.messages.get"`,
		`defaults.connections.acmechat: provider "acmechat" was renamed to "acme.chat"`,
		`defaults.connections.acmechat.messages.fetch: tool "acmechat.messages.fetch" was renamed`,
		`provider_notes.acmechat: provider "acmechat" was renamed to "acme.chat"`,
		`connections.conn.tools: tool "acmechat.messages.purge" was removed from qatlas and is ignored`,
	} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings lack %q:\n%s", want, warnings)
		}
	}
	if got := len(cfg.Renamings()); got != 6 {
		t.Errorf("Renamings() = %d entries, want 6", got)
	}
	if got := len(cfg.ConnectionRenamings("conn")); got != 2 {
		t.Errorf("ConnectionRenamings() = %d entries, want service provider and one tool", got)
	}

	if after, _ := os.ReadFile(path); string(after) != renamedConfig {
		t.Errorf("loading changed the file:\n%s", after)
	}
}

func TestSaveWritesCurrentNames(t *testing.T) {
	path := writeRenamed(t, renamedConfig)
	store := NewStore(path, renameProviders)
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	saved, _ := os.ReadFile(path)
	for _, gone := range []string{"acmechat.messages.fetch", "provider: acmechat", "acmechat: "} {
		if strings.Contains(string(saved), gone) {
			t.Errorf("saved file still holds %q:\n%s", gone, saved)
		}
	}
	for _, want := range []string{"provider: acme.chat", "acme.chat.messages.get", "acme.chat: team chat"} {
		if !strings.Contains(string(saved), want) {
			t.Errorf("saved file lacks %q:\n%s", want, saved)
		}
	}
	reloaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	// The removed tool is kept by its historical ID, and nothing is left to map.
	if len(reloaded.Renamings()) != 0 || !strings.Contains(string(saved), "acmechat.messages.purge") {
		t.Errorf("reloaded renamings = %v, file:\n%s", reloaded.Renamings(), saved)
	}
}

func TestOldAndNewToolNameInOneListIsADuplicate(t *testing.T) {
	text := strings.Replace(renamedConfig,
		"tools: [acmechat.messages.fetch, acme.chat.messages.list, acmechat.messages.purge]",
		"tools: [acmechat.messages.fetch, acme.chat.messages.get]", 1)
	_, err := Load(writeRenamed(t, text), renameProviders)
	if err == nil || !strings.Contains(err.Error(), `tool "acme.chat.messages.get" is listed more than once`) {
		t.Errorf("Load() = %v, want a duplicate tool error", err)
	}
}

func TestRenamedKeyCollisionIsAnError(t *testing.T) {
	for name, text := range map[string]string{
		"defaults": strings.Replace(renamedConfig, "defaults:\n  connections:\n",
			"defaults:\n  connections:\n    acme.chat: conn\n", 1),
		"notes": strings.Replace(renamedConfig, "provider_notes:\n", "provider_notes:\n  acme.chat: other\n", 1),
	} {
		_, err := Load(writeRenamed(t, text), renameProviders)
		if err == nil || !strings.Contains(err.Error(), "collides with an existing entry") {
			t.Errorf("%s: Load() = %v, want a collision error", name, err)
		}
	}
}

func TestRemovedToolUnderFormerProviderStillWarns(t *testing.T) {
	text := `version: 1
services:
  svc:
    provider: acmechat
    base_url: https://example.invalid
credentials:
  cred:
    type: env
    values:
      token: ACME_TOKEN
connections:
  conn:
    service: svc
    credential: cred
    permissions: [read]
    tools: [acmechat.messages.purge]
`
	cfg, err := Load(writeRenamed(t, text), renameProviders)
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}
	if !strings.Contains(strings.Join(cfg.ToolWarnings(), "\n"),
		`tool "acmechat.messages.purge" was removed from qatlas and is ignored`) {
		t.Errorf("warnings = %v", cfg.ToolWarnings())
	}
}
