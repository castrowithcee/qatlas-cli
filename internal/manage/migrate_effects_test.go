package manage

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// schemaTwo is a schema-2 file whose connections combine the former permissions and tools in every way.
// The vault ones are approved with their former effective effects before the migration.
const schemaTwo = `version: 2
services:
    wiki:
        provider: docs
        base_url: https://wiki.example.test
credentials:
    keyring-reader:
        type: keyring
    vault-reader:
        type: vault
connections:
    broad:
        service: wiki
        credential: vault-reader
        permissions: [read, update]
        tools: [docs.read]
    same:
        service: wiki
        credential: vault-reader
        permissions: [read]
        tools: [docs.read]
    defaulted:
        service: wiki
        credential: vault-reader
        tools: [docs.read]
    emptied:
        service: wiki
        credential: vault-reader
        permissions: [read]
        tools: []
    plain:
        service: wiki
        credential: vault-reader
        permissions: [read, update]
    unapproved:
        service: wiki
        credential: vault-reader
        permissions: [read, update]
        tools: [docs.read]
    keyring:
        service: wiki
        credential: keyring-reader
        permissions: [read, update]
        tools: [docs.read]
`

// formerEffects are the effects each connection's approval was given for before the migration.
var formerEffects = map[string][]string{
	"broad": {"read", "update"}, "same": {"read"}, "defaulted": {"read"}, "emptied": {"read"},
	"plain": {"read", "update"},
}

// newSchemaTwoWorld writes schemaTwo beside an encrypted vault and approves the connections as they were.
func newSchemaTwoWorld(t *testing.T, locked bool) migrateWorld {
	t.Helper()
	dir := t.TempDir()
	w := migrateWorld{dir: dir, path: filepath.Join(dir, "config.yaml"), v: vault.New(dir)}
	if err := os.WriteFile(w.path, []byte(schemaTwo), 0o600); err != nil {
		t.Fatal(err)
	}
	offer := func(string) (string, error) { return migratePassphrase, nil }
	if err := w.v.Set("vault-reader", "key-id", "synthetic-id", offer); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(w.path, setupCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	var scopes []vault.Scope
	for name, effects := range formerEffects {
		resolved, err := cfg.Resolve(name, "")
		if err != nil {
			t.Fatal(err)
		}
		scope := secret.ScopeOf(resolved)
		scope.Effects = effects
		scopes = append(scopes, scope)
	}
	if err := w.v.Approve(scopes); err != nil {
		t.Fatal(err)
	}
	if locked {
		w.v = vault.New(dir)
	}
	return w
}

func openConnections(t *testing.T, cfg *config.Config, dir string) []string {
	t.Helper()
	probe := vault.New(dir)
	if _, err := probe.Unlock(migratePassphrase); err != nil {
		t.Fatal(err)
	}
	report, err := approval.Pending(cfg, probe)
	if err != nil {
		t.Fatal(err)
	}
	var open []string
	for _, change := range report.Open {
		open = append(open, change.Connection)
	}
	return open
}

func TestMigrationNarrowsToolConnectionsAndReportsTheirApprovals(t *testing.T) {
	for _, locked := range []bool{false, true} {
		name := "unlocked"
		if locked {
			name = "locked"
		}
		t.Run(name, func(t *testing.T) {
			w := newSchemaTwoWorld(t, locked)
			svc := w.service()

			m, err := svc.MigrateSchema()
			if err != nil || m == nil {
				t.Fatalf("MigrateSchema() = %+v, %v", m, err)
			}

			// A locked vault cannot say which connection was approved, so it lists every narrowed vault one.
			want := []string{"broad", "emptied"}
			if locked {
				want = []string{"broad", "emptied", "unapproved"}
			}
			if !reflect.DeepEqual(m.Reapproval, want) {
				t.Errorf("Reapproval = %v, want %v", m.Reapproval, want)
			}

			data, err := os.ReadFile(w.path)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if !strings.Contains(text, "version: 3") || strings.Contains(text, "permissions") {
				t.Errorf("migrated file:\n%s", text)
			}
			cfg, err := config.Load(w.path, setupCatalog{})
			if err != nil {
				t.Fatal(err)
			}
			if c := cfg.Connections["broad"]; c.Effects != nil || !reflect.DeepEqual(c.Tools, []string{"docs.read"}) {
				t.Errorf("broad = %+v, want the tools alone", c)
			}
			if c := cfg.Connections["emptied"]; c.Effects != nil || c.Tools == nil || len(c.Tools) != 0 {
				t.Errorf("emptied = %+v, want an empty tools list alone", c)
			}
			if c := cfg.Connections["plain"]; !reflect.DeepEqual(c.Effects, []config.Effect{config.EffectRead, config.EffectUpdate}) {
				t.Errorf("plain = %+v, want its permissions as effects", c)
			}

			// Unchanged effective effects keep the approval; narrower ones are open until approved once.
			// "unapproved" was never approved, so it is open too.
			open := openConnections(t, cfg, w.dir)
			if wantOpen := []string{"broad", "emptied", "unapproved"}; !reflect.DeepEqual(sortedCopy(open), wantOpen) {
				t.Errorf("open approvals = %v, want %v", open, wantOpen)
			}

			// A second run migrates nothing.
			again, err := svc.MigrateSchema()
			if again != nil || err != nil {
				t.Errorf("second MigrateSchema() = %+v, %v", again, err)
			}
		})
	}
}

func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// The migration never widens an approval: what it leaves open is exactly the narrowed connections, and
// approving them once covers the narrower scope.
func TestMigratedNarrowedConnectionsCanBeApprovedOnce(t *testing.T) {
	w := newSchemaTwoWorld(t, false)
	if _, err := w.service().MigrateSchema(); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(w.path, setupCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := approval.Approve(context.Background(), cfg, w.v, []string{"broad", "emptied"}); err != nil {
		t.Fatal(err)
	}
	probe, err := w.v.Approvals()
	if err != nil {
		t.Fatal(err)
	}
	if got := probe["broad"].Scope.Effects; !reflect.DeepEqual(got, []string{"read"}) {
		t.Errorf("approved effects of broad = %v, want only those of its tool", got)
	}
}

func TestMigrationOfAFileWithBothFieldNamesLeavesItUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	in := strings.Replace(schemaTwo, "        permissions: [read]\n        tools: [docs.read]\n",
		"        permissions: [read]\n        effects: [read]\n", 1)
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	w := migrateWorld{dir: dir, path: path, v: vault.New(dir)}

	m, err := w.service().MigrateSchema()

	if m != nil || err == nil {
		t.Fatalf("MigrateSchema() = %+v, %v, want an error", m, err)
	}
	if after, _ := os.ReadFile(path); string(after) != in {
		t.Error("the invalid file was changed")
	}
}

// The fingerprint of a connection whose effective effects do not change is byte for byte the one it had
// before the migration, so its approval stays valid. The reference was computed from the schema-2 reading.
func TestMigrationKeepsTheFingerprintOfUnchangedEffects(t *testing.T) {
	const reference = "ebb79e0615045748c89deb00e83ccfb0fe0b38ca93e4ab90b80019c9d91b2533"
	w := newSchemaTwoWorld(t, false)
	if _, err := w.service().MigrateSchema(); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(w.path, setupCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := cfg.Resolve("same", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := vault.Fingerprint(secret.ScopeOf(resolved), "id-1"); got != reference {
		t.Errorf("Fingerprint() = %s, want %s", got, reference)
	}
	// The scope schema 2 approved, written out: permissions and tools as the former validation required.
	former := vault.Scope{Connection: "same", Credential: "vault-reader", Provider: "docs",
		Origin: "https://wiki.example.test", Effects: []string{"read"}, Targets: []string{}, Tools: []string{"docs.read"}}
	if got := vault.Fingerprint(former, "id-1"); got != reference {
		t.Errorf("former Fingerprint() = %s, want %s", got, reference)
	}
}
