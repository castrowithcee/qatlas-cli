package manage

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// shelfCatalog is setupCatalog with a provider whose box/NAME meant exactly that box and box/NAME/* the box
// with everything below it, while the current forms read box/NAME as the box with everything below it.
type shelfCatalog struct{ setupCatalog }

var shelfProvider = config.ProviderMetadata{
	ID: "shelf", DefaultBaseURL: "https://shelf.example.test", SecretRoles: []config.SecretRole{{Name: "token"}},
	Target: config.TargetMetadata{Label: "box", Multiple: true, MigrateTargets: shelfMigrate,
		Kinds: []config.TargetKind{{Name: "box", Forms: []string{"box/NAME", "box/NAME/*"}, Excludable: true}}},
	Tools: []config.ToolMetadata{{ID: "shelf.boxes.list", Effect: config.EffectRead}},
}

func (c shelfCatalog) ProviderMetadata(id string) (config.ProviderMetadata, bool) {
	if id == shelfProvider.ID {
		return shelfProvider, true
	}
	return c.setupCatalog.ProviderMetadata(id)
}

func (c shelfCatalog) ProviderMetadataAll() []config.ProviderMetadata {
	return append(c.setupCatalog.ProviderMetadataAll(), shelfProvider)
}

// shelfMigrate refuses a connection that names a box both ways: no current form has that reach.
func shelfMigrate(targets, exclude []string) (config.TargetMigration, bool) {
	out := config.TargetMigration{Targets: []string{}, Exclude: slices.Clone(exclude)}
	for _, target := range targets {
		if base, tree := strings.CutSuffix(target, "/*"); tree {
			if slices.Contains(targets, base) {
				return config.TargetMigration{}, false
			}
			out.Targets = append(out.Targets, base)
			continue
		}
		out.Targets = append(out.Targets, target)
		out.Exclude = append(out.Exclude, target+"/*")
	}
	return out, true
}

// shelfSchemaFile is a file of the given schema whose targets are spelled in their former meaning. EFFECTS is the
// name of the effects field of that schema.
const shelfSchemaFile = `version: VERSION
services:
    store:
        provider: shelf
credentials:
    vault-reader:
        type: vault
    keyring-reader:
        type: keyring
connections:
    exact:
        service: store
        credential: vault-reader
        targets: [box/alpha, box/beta/*]
        target_labels:
            box/alpha: Alpha
            box/beta/*: Beta
        EFFECTS: [read]
    plain:
        service: store
        credential: vault-reader
        EFFECTS: [read]
    keyring:
        service: store
        credential: keyring-reader
        target: box/alpha
`

const shelfMixed = `    mixed:
        service: store
        credential: vault-reader
        targets: [box/eps, box/eps/*]
`

// formerShelfTargets are the targets and exclusions each vault connection was approved with.
var formerShelfTargets = map[string][2][]string{
	"exact": {{"box/alpha", "box/beta/*"}, nil},
	"plain": {nil, nil},
	"mixed": {{"box/eps", "box/eps/*"}, nil},
}

func shelfFile(version int, mixed bool) string {
	effects := "effects"
	if version < 3 {
		effects = "permissions"
	}
	file := strings.NewReplacer("VERSION", fmt.Sprint(version), "EFFECTS", effects).Replace(shelfSchemaFile)
	if mixed {
		file += shelfMixed
	}
	return file
}

// formerScope is the scope a connection was approved with before the migration, its former targets included.
func formerScope(t *testing.T, cfg *config.Config, name string) vault.Scope {
	t.Helper()
	resolved, err := cfg.Resolve(name, "")
	if err != nil {
		t.Fatal(err)
	}
	scope := secret.ScopeOf(resolved)
	scope.Targets, scope.Exclude = formerShelfTargets[name][0], formerShelfTargets[name][1]
	return scope
}

// newShelfWorld writes the file beside an encrypted vault that approved its vault connections as they were.
func newShelfWorld(t *testing.T, file string) migrateWorld {
	t.Helper()
	dir := t.TempDir()
	w := migrateWorld{dir: dir, path: filepath.Join(dir, "config.yaml"), v: vault.New(dir)}
	if err := os.WriteFile(w.path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	offer := func(string) (string, error) { return migratePassphrase, nil }
	if err := w.v.Set("vault-reader", "token", "synthetic-token", offer); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.NewStore(w.path, shelfCatalog{}).Load()
	if err != nil {
		t.Fatal(err)
	}
	var scopes []vault.Scope
	for name := range formerShelfTargets {
		if _, ok := cfg.Connections[name]; ok {
			scopes = append(scopes, formerScope(t, cfg, name))
		}
	}
	if err := w.v.Approve(scopes); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w migrateWorld) shelfService() *Service {
	return ForVault(config.NewStore(w.path, shelfCatalog{}), w.v, connlog.SurfaceCLI, nil).WithVaultProcessSupport(false)
}

// A file of every former schema reaches the current one through every step: ids, effects and targets.
func TestMigrationRewritesTargetsFromEverySchema(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		t.Run(fmt.Sprint("schema ", version), func(t *testing.T) {
			w := newShelfWorld(t, shelfFile(version, false))

			m, err := w.shelfService().MigrateSchema()
			if err != nil || m == nil {
				t.Fatalf("MigrateSchema() = %+v, %v", m, err)
			}

			if m.From != version || !slices.Equal(m.Retargeted, []string{"exact"}) || m.OutdatedTargets != nil {
				t.Errorf("migration = %+v, want exact to be approved again", m)
			}
			data, _ := os.ReadFile(w.path)
			if !strings.Contains(string(data), "version: 4\n") || strings.Contains(string(data), "permissions") {
				t.Errorf("migrated file:\n%s", data)
			}
			cfg, err := config.Load(w.path, shelfCatalog{})
			if err != nil {
				t.Fatalf("Load() = %v", err)
			}
			exact := cfg.Connections["exact"]
			if exact.ID == "" || !slices.Equal(exact.Effects, []config.Effect{config.EffectRead}) ||
				!slices.Equal(exact.Targets, []string{"box/alpha", "box/beta"}) ||
				!slices.Equal(exact.Exclude, []string{"box/alpha/*"}) ||
				!reflect.DeepEqual(exact.TargetLabels, map[string]string{"box/alpha": "Alpha", "box/beta": "Beta"}) {
				t.Errorf("exact = %+v", exact)
			}
			if keyring := cfg.Connections["keyring"]; keyring.Target != "box/alpha" ||
				!slices.Equal(keyring.Exclude, []string{"box/alpha/*"}) {
				t.Errorf("keyring = %+v", keyring)
			}
		})
	}
}

// The approval of a rewritten connection no longer matches and the one of an unchanged connection still does:
// the fingerprint follows the targets and exclusions.
func TestMigrationChangesTheFingerprintOfRewrittenTargetsOnly(t *testing.T) {
	w := newShelfWorld(t, shelfFile(3, false))
	before, err := config.Load(w.path, shelfCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	formerExact, formerPlain := formerScope(t, before, "exact"), formerScope(t, before, "plain")

	if _, err := w.shelfService().MigrateSchema(); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(w.path, shelfCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	scope := func(name string) vault.Scope {
		resolved, err := cfg.Resolve(name, "")
		if err != nil {
			t.Fatal(err)
		}
		return secret.ScopeOf(resolved)
	}
	if vault.Fingerprint(scope("exact"), "id-1") == vault.Fingerprint(formerExact, "id-1") {
		t.Error("the fingerprint of the rewritten connection did not change")
	}
	if vault.Fingerprint(scope("plain"), "id-1") != vault.Fingerprint(formerPlain, "id-1") {
		t.Error("the fingerprint of the unchanged connection changed")
	}
	if open := openConnections(t, cfg, w.dir); !slices.Equal(open, []string{"exact"}) {
		t.Errorf("open approvals = %v, want exact alone", open)
	}
}

// A refused connection is written as it was, with its mark, and the rest of the file is migrated. The file then
// fails to load with the problem, also on every later load, and a second start migrates nothing. The report
// names connections only.
func TestMigrationWritesARefusedConnectionAsItWas(t *testing.T) {
	original := shelfFile(3, true)
	w := newShelfWorld(t, original)
	svc := w.shelfService()

	m, err := svc.MigrateSchema()

	if err != nil || m == nil {
		t.Fatalf("MigrateSchema() = %+v, %v", m, err)
	}
	if !slices.Equal(m.OutdatedTargets, []string{"mixed"}) || !slices.Equal(m.Retargeted, []string{"exact"}) {
		t.Errorf("migration = %+v", m)
	}
	if report := fmt.Sprintf("%+v", *m); strings.Contains(report, "box/") || strings.Contains(report, "Alpha") {
		t.Errorf("the report holds a target or a label: %s", report)
	}
	if backup, _ := os.ReadFile(m.Backup); string(backup) != original {
		t.Error("the backup is not the file as it was")
	}

	cfg, err := config.NewStore(w.path, shelfCatalog{}).Load()
	if err != nil {
		t.Fatal(err)
	}
	if mixed := cfg.Connections["mixed"]; !mixed.TargetsOutdated || !slices.Equal(mixed.Targets, []string{"box/eps", "box/eps/*"}) ||
		mixed.Exclude != nil {
		t.Errorf("mixed = %+v, want it as it was", mixed)
	}
	if exact := cfg.Connections["exact"]; !slices.Equal(exact.Targets, []string{"box/alpha", "box/beta"}) {
		t.Errorf("exact = %+v, want it migrated", exact)
	}
	for range 2 {
		loaded, err := config.Load(w.path, shelfCatalog{})
		if err != nil || !loaded.Connections["mixed"].TargetsOutdated {
			t.Fatalf("Load() = %v, want the file to load with mixed marked", err)
		}
		err = loaded.Validate()
		problems := config.Problems(err)
		if len(problems) != 1 || problems[0].Code != config.CodeTargetsOutdated ||
			problems[0].Location() != "connections.mixed.targets" || strings.Contains(err.Error(), "box/") {
			t.Fatalf("Validate() = %v, want the outdated targets of mixed", err)
		}
	}
	if again, err := svc.MigrateSchema(); again != nil || err != nil {
		t.Errorf("second MigrateSchema() = %+v, %v", again, err)
	}
}

// A locked vault cannot say which connection was approved, so the report names every rewritten vault connection.
func TestMigrationWithALockedVaultNamesEveryRewrittenVaultConnection(t *testing.T) {
	w := newShelfWorld(t, shelfFile(3, false))
	w.v = vault.New(w.dir)

	m, err := w.shelfService().MigrateSchema()

	if err != nil || m == nil || !m.Pending || !slices.Equal(m.Retargeted, []string{"exact"}) {
		t.Fatalf("MigrateSchema() = %+v, %v", m, err)
	}
}

// Saving new targets for a marked connection removes the mark and needs its approval again like any target
// change; a save that leaves its targets as they are keeps the mark and the approval it has.
func TestSavingNewTargetsLiftsTheMarkAndNeedsApproval(t *testing.T) {
	w := newShelfWorld(t, shelfFile(3, true))
	svc := w.shelfService()
	if m, err := svc.MigrateSchema(); err != nil || m == nil {
		t.Fatalf("MigrateSchema() = %+v, %v", m, err)
	}
	cfg, rev, err := svc.Load()
	if err != nil {
		t.Fatal(err)
	}
	pending := func(cfg *config.Config) bool {
		t.Helper()
		report, err := approval.Pending(cfg, w.v)
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(report.Open, func(c approval.Change) bool { return c.Connection == "mixed" })
	}

	same := cfg.Clone()
	conn := same.Connections["mixed"]
	conn.TargetsOutdated, conn.Description = false, "renamed"
	if err := same.SetConnection("mixed", conn); err != nil {
		t.Fatal(err)
	}
	if !same.Connections["mixed"].TargetsOutdated || pending(same) {
		t.Fatalf("a save with the same targets: marked %v, approval open %v", same.Connections["mixed"].TargetsOutdated, pending(same))
	}

	chosen := cfg.Clone()
	conn = chosen.Connections["mixed"]
	conn.TargetsOutdated, conn.Targets = false, []string{"box/eps"}
	if err := chosen.SetConnection("mixed", conn); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveConfig(cfg, chosen, rev); err != nil {
		t.Fatalf("SaveConfig() = %v", err)
	}
	loaded, err := config.Load(w.path, shelfCatalog{})
	if err != nil || loaded.Validate() != nil || loaded.Connections["mixed"].TargetsOutdated {
		t.Fatalf("reloaded mixed = %+v, %v; want it valid and unmarked", loaded.Connections["mixed"], err)
	}
	if !pending(loaded) {
		t.Error("the new targets need no approval")
	}
}
