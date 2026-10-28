package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// shelfMigrate stands for a provider whose box/NAME meant exactly that box and whose box/NAME/* meant the box
// with everything below it, while the current forms read box/NAME as the box with everything below it. A
// connection that names a box both ways has no current form of the same reach.
func shelfMigrate(targets, exclude []string) (TargetMigration, bool) {
	out := TargetMigration{Targets: []string{}, Exclude: slices.Clone(exclude)}
	for _, target := range targets {
		if base, tree := strings.CutSuffix(target, "/*"); tree {
			if slices.Contains(targets, base) {
				return TargetMigration{}, false
			}
			out.Targets = append(out.Targets, base)
			continue
		}
		out.Targets = append(out.Targets, target)
		out.Exclude = append(out.Exclude, target+"/*")
	}
	return out, true
}

func shelfCatalog(migrate func([]string, []string) (TargetMigration, bool)) ProviderCatalog {
	return testProviderCatalog{"shelf": {
		ID: "shelf", Name: "Shelf", DefaultBaseURL: "https://shelf.example.test",
		SecretRoles: []SecretRole{{Name: "token"}},
		Target: TargetMetadata{Label: "box", Multiple: true, MigrateTargets: migrate,
			Kinds: []TargetKind{{Name: "box", Forms: []string{"box/NAME", "box/NAME/*"}, Excludable: true}}},
		Tools: []ToolMetadata{{ID: "shelf.boxes.list", Effect: EffectRead}},
	}}
}

// shelfFile is a configuration of schema 3 whose targets are spelled in their former meaning.
const shelfFile = `version: 3
services:
  store:
    provider: shelf
credentials:
  key:
    type: keyring
connections:
  exact:
    service: store
    credential: key
    target: box/alpha
    target_labels:
      box/alpha: Alpha
  tree:
    service: store
    credential: key
    targets: [box/beta/*, box/gamma]
    exclude: [box/delta]
    target_labels:
      box/beta/*: Beta
      box/delta: Delta
  plain:
    service: store
    credential: key
`

// mixedConnection names one box both ways.
const mixedConnection = `  mixed:
    service: store
    credential: key
    targets: [box/eps, box/eps/*]
    target_labels:
      box/eps: Eps
`

func TestDecodeRewritesTheTargetsOfAnOlderSchema(t *testing.T) {
	cfg, err := Decode(strings.NewReader(shelfFile), shelfCatalog(shelfMigrate))
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}

	exact := cfg.Connections["exact"]
	if exact.Target != "box/alpha" || !slices.Equal(exact.Exclude, []string{"box/alpha/*"}) ||
		!reflect.DeepEqual(exact.TargetLabels, map[string]string{"box/alpha": "Alpha"}) {
		t.Errorf("exact = %+v, want its box with a new exclusion that has no label", exact)
	}
	tree := cfg.Connections["tree"]
	if !slices.Equal(tree.Targets, []string{"box/beta", "box/gamma"}) ||
		!slices.Equal(tree.Exclude, []string{"box/delta", "box/gamma/*"}) ||
		!reflect.DeepEqual(tree.TargetLabels, map[string]string{"box/beta": "Beta", "box/delta": "Delta"}) {
		t.Errorf("tree = %+v, want the labels on the entries they moved to", tree)
	}
	if plain := cfg.Connections["plain"]; plain.TargetValues() != nil || plain.Exclude != nil || plain.TargetsOutdated {
		t.Errorf("plain = %+v, want it unchanged", plain)
	}
	if got := cfg.RetargetedConnections(); !slices.Equal(got, []string{"exact", "tree"}) {
		t.Errorf("RetargetedConnections() = %v", got)
	}
	if got := cfg.OutdatedTargets(); got != nil {
		t.Errorf("OutdatedTargets() = %v, want none", got)
	}
}

// A file of the current schema is never rewritten, so a file written once keeps its targets on every later read.
func TestDecodeLeavesTheTargetsOfTheCurrentSchema(t *testing.T) {
	calls := 0
	counting := func(targets, exclude []string) (TargetMigration, bool) {
		calls++
		return shelfMigrate(targets, exclude)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(shelfFile), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(path, shelfCatalog(counting))
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() = %v", err)
	}
	calls = 0

	again, err := store.Load()

	if err != nil || again.Version != Version || calls != 0 {
		t.Fatalf("Load() = version %d, %v, %d hook calls; want the current schema without a call", again.Version, err, calls)
	}
	if !reflect.DeepEqual(again.Connections["tree"].Targets, cfg.Connections["tree"].Targets) ||
		!reflect.DeepEqual(again.Connections["exact"].Exclude, cfg.Connections["exact"].Exclude) {
		t.Errorf("reread = %+v, want what was written", again.Connections)
	}
	if got := again.RetargetedConnections(); got != nil {
		t.Errorf("RetargetedConnections() = %v after the migration", got)
	}
}

// A refused connection stays as it was, and its mark keeps the file invalid on every later load until a person
// chooses its targets again. No message quotes a target.
func TestARefusedConnectionStaysAndKeepsItsProblem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(shelfFile+mixedConnection), 0o600); err != nil {
		t.Fatal(err)
	}
	catalog := shelfCatalog(shelfMigrate)
	checkProblem := func(err error) {
		t.Helper()
		problems := Problems(err)
		if len(problems) != 1 || problems[0].Code != CodeTargetsOutdated || problems[0].Location() != "connections.mixed.targets" ||
			problems[0].Hint() == "" {
			t.Fatalf("problems = %+v, want the outdated targets of mixed alone", problems)
		}
		if text := err.Error() + problems[0].Hint(); strings.Contains(text, "box/") {
			t.Errorf("the problem quotes a target: %s", text)
		}
	}

	_, err := NewStore(path, catalog).Load()
	checkProblem(err)

	migrating := NewStore(path, catalog).ForMigration()
	cfg, err := migrating.Load()
	if err != nil {
		t.Fatalf("ForMigration().Load() = %v", err)
	}
	mixed := cfg.Connections["mixed"]
	if !mixed.TargetsOutdated || !slices.Equal(mixed.Targets, []string{"box/eps", "box/eps/*"}) || mixed.Exclude != nil ||
		!reflect.DeepEqual(mixed.TargetLabels, map[string]string{"box/eps": "Eps"}) {
		t.Errorf("mixed = %+v, want it as it was, marked", mixed)
	}
	if got := cfg.OutdatedTargets(); !slices.Equal(got, []string{"mixed"}) {
		t.Errorf("OutdatedTargets() = %v", got)
	}
	if err := NewStore(path, catalog).Save(cfg); err == nil {
		t.Fatal("a plain store saved a configuration with outdated targets")
	}
	if err := migrating.Save(cfg); err != nil {
		t.Fatalf("ForMigration().Save() = %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "version: 4") || !strings.Contains(string(data), "targets_outdated: true") {
		t.Errorf("written file:\n%s", data)
	}

	_, err = NewStore(path, catalog).Load()
	checkProblem(err)

	// Another problem next to it is not accepted even for the migration.
	broken := strings.Replace(string(data), "credential: key", "credential: nobody", 1)
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	var invalid *InvalidError
	if _, err := migrating.Load(); !errors.As(err, &invalid) {
		t.Errorf("ForMigration().Load() = %v, want the other problem", err)
	}
}

// A provider answer that does not line up with the former entries cannot carry the labels over, and is taken as a
// refusal rather than guessed at.
func TestAMisshapenMigrationIsARefusal(t *testing.T) {
	for name, migrate := range map[string]func([]string, []string) (TargetMigration, bool){
		"targets dropped": func(targets, exclude []string) (TargetMigration, bool) {
			return TargetMigration{Exclude: exclude}, true
		},
		"exclusions dropped": func(targets, exclude []string) (TargetMigration, bool) {
			return TargetMigration{Targets: targets}, true
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(shelfFile), shelfCatalog(migrate))
			problems := Problems(err)
			if !onlyOutdatedTargets(err) || !slices.ContainsFunc(problems, func(p Problem) bool {
				return p.Location() == "connections.tree.targets"
			}) {
				t.Errorf("problems = %+v, want tree refused", problems)
			}
		})
	}
}

// The mark sits at the target line of a connection with a single target too.
func TestTheOutdatedProblemSitsAtTheSingleTarget(t *testing.T) {
	file := strings.Replace(shelfFile, "version: 3", "version: 4", 1)
	file = strings.Replace(file, "    target: box/alpha\n", "    target: box/alpha\n    targets_outdated: true\n", 1)

	_, err := Decode(strings.NewReader(file), shelfCatalog(shelfMigrate))

	problems := Problems(err)
	if len(problems) != 1 || problems[0].Location() != "connections.exact.target" {
		t.Errorf("problems = %+v", problems)
	}
}
