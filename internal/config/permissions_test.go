package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

func TestConnectionEffectsAreIndependentFromCredentials(t *testing.T) {
	cfg, err := Decode(strings.NewReader(strings.Replace(minimal,
		"    credential: reader\n", "    credential: reader\n    effects: [read, update]\n", 1)), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	want := []Effect{EffectRead, EffectUpdate}
	if got := cfg.ConnectionEffects("wiki"); !reflect.DeepEqual(got, want) {
		t.Fatalf("permissions = %v, want %v", got, want)
	}
	allows := func(effect Effect) bool {
		return cfg.ConnectionAllows("wiki", ToolMetadata{ID: "bookstack.pages.list", Effect: effect})
	}
	if !allows(EffectRead) || !allows(EffectUpdate) || allows(EffectCreate) || allows(EffectDelete) {
		t.Fatal("connection permission check does not match the explicit local allow-list")
	}
}

func TestConnectionEffectsRoundTripMissingAndDenyAll(t *testing.T) {
	missing, err := Decode(strings.NewReader(minimal), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := yaml.Marshal(missing)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "effects:") {
		t.Fatalf("missing permissions were materialized:\n%s", encoded)
	}

	connection := missing.Connections["wiki"]
	connection.Effects = []Effect{}
	missing.Connections["wiki"] = connection
	encoded, err = yaml.Marshal(missing)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "effects: []") {
		t.Fatalf("deny-all was omitted:\n%s", encoded)
	}
	roundTrip, err := Decode(strings.NewReader(string(encoded)), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if got := roundTrip.ConnectionEffects("wiki"); got == nil || len(got) != 0 {
		t.Fatalf("round-trip permissions = %#v", got)
	}
}

func TestMissingAndEmptyConnectionPermissionsStaySafe(t *testing.T) {
	cfg, err := Decode(strings.NewReader(minimal), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ConnectionEffects("wiki"); !reflect.DeepEqual(got, []Effect{EffectRead}) {
		t.Fatalf("missing permissions = %v, want the safe read compatibility default", got)
	}

	empty, err := Decode(strings.NewReader(strings.Replace(minimal,
		"    credential: reader\n", "    credential: reader\n    effects: []\n", 1)), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if got := empty.ConnectionEffects("wiki"); got == nil || len(got) != 0 {
		t.Fatalf("explicit empty permissions = %#v, want a non-nil deny-all list", got)
	}
}

func TestConnectionEffectsRejectUnknownAndDuplicateValues(t *testing.T) {
	for _, permissions := range []string{"[read, publish]", "[read, read]"} {
		input := strings.Replace(minimal, "    credential: reader\n",
			"    credential: reader\n    effects: "+permissions+"\n", 1)
		if _, err := Decode(strings.NewReader(input), testProviders); err == nil {
			t.Fatalf("permissions %s were accepted", permissions)
		}
	}
}

func TestPermissionTextDistinguishesDefaultAndDenyAll(t *testing.T) {
	if got, err := ParseEffects(""); err != nil || got != nil {
		t.Fatalf("empty = %#v, %v", got, err)
	}
	got, err := ParseEffects("none")
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("none = %#v, %v", got, err)
	}
	if FormatEffects(nil) != "" || FormatEffects([]Effect{}) != "none" {
		t.Fatal("permission formatting lost default/deny-all distinction")
	}
}

func TestSeaTableTargetAllowListRoundTripsWithoutChangingSingleTargets(t *testing.T) {
	input := `version: 1
services:
  main: {provider: seatable.base, base_url: https://cloud.seatable.io}
credentials:
  reader: {type: keyring}
connections:
  selected:
    service: main
    credential: reader
    targets: [id:0000, id:0001]
  all:
    service: main
    credential: reader
    target: "*"
defaults: {}
`
	cfg, err := Decode(strings.NewReader(input), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := Decode(strings.NewReader(string(encoded)), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if got := roundTrip.Connections["selected"].TargetValues(); !reflect.DeepEqual(got, []string{"id:0000", "id:0001"}) {
		t.Fatalf("allow-list = %v", got)
	}
	if got := roundTrip.Connections["all"]; got.Target != "*" || len(got.Targets) != 0 {
		t.Fatalf("wildcard = %+v", got)
	}
}

// withTools returns the minimal configuration with the given YAML lines added to its one connection.
func withTools(lines string) string {
	return strings.Replace(minimal, "    credential: reader\n", "    credential: reader\n"+lines, 1)
}

func TestConnectionToolsOfferExactlyTheListedAndDeriveTheEffects(t *testing.T) {
	missing, err := Decode(strings.NewReader(minimal), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if !missing.ConnectionAllows("wiki", ToolMetadata{ID: "bookstack.pages.list", Effect: "read"}) ||
		!missing.ConnectionAllows("wiki", ToolMetadata{ID: "bookstack.pages.get", Effect: "read"}) {
		t.Fatal("a connection by effect without a field no longer offers every tool of the provider's defaults")
	}
	if got := missing.ConnectionEffects("wiki"); !reflect.DeepEqual(got, []Effect{EffectRead}) {
		t.Fatalf("default effects = %v", got)
	}

	listed, err := Decode(strings.NewReader(withTools(
		"    tools: [bookstack.pages.create, bookstack.pages.list]\n")), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"bookstack.pages.list", "bookstack.pages.create"} {
		if !listed.ConnectionAllows("wiki", ToolMetadata{ID: id, Effect: EffectRead}) {
			t.Errorf("the listed tool %s is refused", id)
		}
	}
	for _, id := range []string{"bookstack.pages.get", "bookstack.pages.search"} {
		if refusal := listed.ConnectionRefusal("wiki", ToolMetadata{ID: id, Effect: EffectRead}); refusal != RefusalToolsList {
			t.Errorf("%s: refusal = %q, want %q", id, refusal, RefusalToolsList)
		}
	}
	if got := listed.ConnectionEffects("wiki"); !reflect.DeepEqual(got, []Effect{EffectRead, EffectCreate}) {
		t.Errorf("derived effects = %v, want read and create", got)
	}

	empty, err := Decode(strings.NewReader(withTools("    tools: []\n")), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if empty.ConnectionAllows("wiki", ToolMetadata{ID: "bookstack.pages.list", Effect: "read"}) {
		t.Fatal("an explicit empty tools list does not deny every tool")
	}
	if got := empty.ConnectionEffects("wiki"); got == nil || len(got) != 0 {
		t.Fatalf("effects of an empty tools list = %#v, want a non-nil empty list", got)
	}
}

func TestEffectsAndToolsTogetherAreAStructuredProblem(t *testing.T) {
	for _, lines := range []string{
		"    effects: [read]\n    tools: [bookstack.pages.list]\n",
		"    effects: []\n    tools: []\n",
	} {
		_, err := Decode(strings.NewReader(withTools(lines)), testProviders)
		var p Problem
		if !errors.As(err, &p) || p.Code != CodeEffectsAndTools || p.Field != "effects" || p.Hint() == "" {
			t.Errorf("%q: error = %v, want the problem %s with a hint", lines, err, CodeEffectsAndTools)
		}
	}
	_, err := Decode(strings.NewReader(withTools("    permissions: [read]\n    effects: [read]\n")), testProviders)
	var p Problem
	if !errors.As(err, &p) || p.Code != CodePermissionsAndEffects || p.Hint() == "" {
		t.Errorf("both field names: error = %v, want the problem %s", err, CodePermissionsAndEffects)
	}
}

func TestFormerPermissionsAreReadAsEffectsOrDroppedNextToTools(t *testing.T) {
	alone, err := Decode(strings.NewReader(withTools("    permissions: [read, create]\n")), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if got := alone.ConnectionEffects("wiki"); !reflect.DeepEqual(got, []Effect{EffectRead, EffectCreate}) {
		t.Errorf("former permissions = %v, want them as effects", got)
	}
	encoded, _ := yaml.Marshal(alone)
	if !strings.Contains(string(encoded), "effects:") || strings.Contains(string(encoded), "permissions:") {
		t.Errorf("the former name was written again:\n%s", encoded)
	}

	withList, err := Decode(strings.NewReader(withTools(
		"    permissions: [read, create]\n    tools: [bookstack.pages.list]\n")), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	conn := withList.Connections["wiki"]
	if conn.Effects != nil || !reflect.DeepEqual(conn.Tools, []string{"bookstack.pages.list"}) {
		t.Errorf("connection = %+v, want only the tools", conn)
	}
	if got := withList.ConnectionEffects("wiki"); !reflect.DeepEqual(got, []Effect{EffectRead}) {
		t.Errorf("effects = %v, want those of the tool", got)
	}
}

func TestConnectionToolsRoundTripMissingEmptyAndListed(t *testing.T) {
	for _, tc := range []struct {
		name, lines, encoded string
		want                 []string
	}{
		{"missing", "", "", nil},
		{"empty", "    tools: []\n", "tools: []", []string{}},
		{"listed", "    tools: [bookstack.pages.list, bookstack.pages.get]\n", "tools:", []string{
			"bookstack.pages.list", "bookstack.pages.get",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Decode(strings.NewReader(withTools(tc.lines)), testProviders)
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range []*Config{cfg, cfg.Clone()} {
				encoded, err := yaml.Marshal(candidate)
				if err != nil {
					t.Fatal(err)
				}
				if tc.encoded == "" && strings.Contains(string(encoded), "tools:") {
					t.Fatalf("a missing tools list was materialized:\n%s", encoded)
				}
				if !strings.Contains(string(encoded), tc.encoded) {
					t.Fatalf("encoded form lost %q:\n%s", tc.encoded, encoded)
				}
				roundTrip, err := Decode(strings.NewReader(string(encoded)), testProviders)
				if err != nil {
					t.Fatal(err)
				}
				got := roundTrip.Connections["wiki"].Tools
				if (got == nil) != (tc.want == nil) || !reflect.DeepEqual(append([]string{}, got...),
					append([]string{}, tc.want...)) {
					t.Fatalf("round-trip tools = %#v, want %#v", got, tc.want)
				}
			}
		})
	}
}

func TestConnectionToolsRejectUnknownForeignDuplicateAndFormerlyExcludedTools(t *testing.T) {
	for _, tc := range []struct{ lines, want string }{
		{"    tools: [bookstack.pages.search]\n",
			`connections.wiki.tools: entry 1 is not a registered tool of provider "bookstack"`},
		{"    tools: [bookstack.pages.list, s3cr3t-pasted-by-mistake]\n",
			`connections.wiki.tools: entry 2 is not a registered tool of provider "bookstack"`},
		{"    tools: [telegram.messages.send]\n",
			`connections.wiki.tools: tool "telegram.messages.send" belongs to provider "telegram", not to "bookstack"`},
		{"    tools: [bookstack.pages.list, bookstack.pages.list]\n",
			`connections.wiki.tools: tool "bookstack.pages.list" is listed more than once`},
		{"    permissions: []\n    tools: [bookstack.pages.list]\n",
			`connections.wiki.tools: tool "bookstack.pages.list" has effect read, which the former permissions ` +
				`of the connection do not allow`},
	} {
		_, err := Decode(strings.NewReader(withTools(tc.lines)), testProviders)
		if err == nil || err.Error() != tc.want {
			t.Errorf("tools %q: error = %v, want %q", tc.lines, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), "s3cr3t") {
			t.Errorf("the error quotes an unknown entry: %v", err)
		}
	}
	if _, err := Decode(strings.NewReader(withTools(
		"    permissions: [read, create]\n    tools: [bookstack.pages.create, bookstack.pages.list]\n")),
		testProviders); err != nil {
		t.Fatalf("a valid tools list was refused: %v", err)
	}
}

// A tool the provider removed stays valid in an older file: it is skipped by every check, named by one warning
// per entry, and never offered, while the other entries keep working and any other unknown entry stays an error.
func TestRemovedToolsAreIgnoredWithAWarning(t *testing.T) {
	cfg, err := Decode(strings.NewReader(withTools(
		"    tools: [bookstack.pages.publish, bookstack.pages.list, bookstack.pages.publish]\n")), testProviders)
	if err != nil {
		t.Fatalf("a removed tool made the configuration invalid: %v", err)
	}
	want := []string{`connections.wiki.tools: tool "bookstack.pages.publish" was removed from qatlas and is ignored`,
		`connections.wiki.tools: tool "bookstack.pages.publish" was removed from qatlas and is ignored`}
	if got := cfg.ToolWarnings(); !reflect.DeepEqual(got, want) {
		t.Errorf("ToolWarnings() = %q, want %q", got, want)
	}
	if !cfg.ConnectionAllows("wiki", ToolMetadata{ID: "bookstack.pages.list", Effect: "read"}) ||
		cfg.ConnectionAllows("wiki", ToolMetadata{ID: "bookstack.pages.get", Effect: "read"}) {
		t.Error("the remaining listed tool is not offered exactly")
	}
	// The removed entry is skipped even where the permissions exclude its former effect.
	if _, err := Decode(strings.NewReader(withTools("    permissions: []\n    tools: [bookstack.pages.publish]\n")),
		testProviders); err != nil {
		t.Errorf("a removed tool failed the former permission check: %v", err)
	}
	plain, err := Decode(strings.NewReader(minimal), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if got := plain.ToolWarnings(); len(got) != 0 {
		t.Errorf("ToolWarnings() = %q on a file without a removed tool", got)
	}
	_, err = Decode(strings.NewReader(withTools("    tools: [bookstack.pages.publish, bookstack.pages.search]\n")),
		testProviders)
	if err == nil || err.Error() != `connections.wiki.tools: entry 2 is not a registered tool of provider "bookstack"` {
		t.Errorf("another unknown entry: error = %v", err)
	}
}

// Two connections of one service and one credential are two policies: neither list leaks into the other.
func TestConnectionToolsAreEvaluatedPerConnection(t *testing.T) {
	input := strings.Replace(minimal, "defaults:", `  wiki-reader:
    service: wiki
    credential: reader
    tools: [bookstack.pages.get]
defaults:`, 1)
	input = strings.Replace(input, "    credential: reader\n",
		"    credential: reader\n    tools: [bookstack.pages.list]\n", 1)
	cfg, err := Decode(strings.NewReader(input), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ConnectionAllows("wiki", ToolMetadata{ID: "bookstack.pages.list", Effect: "read"}) ||
		cfg.ConnectionAllows("wiki", ToolMetadata{ID: "bookstack.pages.get", Effect: "read"}) {
		t.Error("wiki does not offer exactly its own tool")
	}
	if !cfg.ConnectionAllows("wiki-reader", ToolMetadata{ID: "bookstack.pages.get", Effect: "read"}) ||
		cfg.ConnectionAllows("wiki-reader", ToolMetadata{ID: "bookstack.pages.list", Effect: "read"}) {
		t.Error("wiki-reader does not offer exactly its own tool")
	}
}

// A tool that requires an allow-list is offered only by a connection whose tools list names it: no
// selection by effect exposes it, whatever the effects, and an ordinary tool keeps its behaviour on the same
// connection.
func TestToolsRequiringAnAllowListNeedTheirName(t *testing.T) {
	guarded := ToolMetadata{ID: "bookstack.pages.create", Effect: EffectCreate, RequiresToolAllowList: true}
	ordinary := ToolMetadata{ID: "bookstack.pages.list", Effect: EffectRead}
	for _, tc := range []struct {
		name, lines           string
		guarded, ordinaryWant bool
	}{
		{"by effect", "    effects: [read, create, update, delete, execute]\n", false, true},
		{"a list without it", "    tools: [bookstack.pages.list]\n", false, true},
		{"a list naming it", "    tools: [bookstack.pages.create]\n", true, false},
		{"an empty list", "    tools: []\n", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Decode(strings.NewReader(withTools(tc.lines)), testProviders)
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.ConnectionAllows("wiki", guarded); got != tc.guarded {
				t.Errorf("guarded tool offered = %t, want %t", got, tc.guarded)
			}
			if got := cfg.ConnectionAllows("wiki", ordinary); got != tc.ordinaryWant {
				t.Errorf("ordinary tool offered = %t, want %t", got, tc.ordinaryWant)
			}
		})
	}
}

func TestFormerPermissionsInACurrentFileAreAStructuredProblem(t *testing.T) {
	for _, lines := range []string{"    permissions: [read]\n", "    permissions: [read]\n    effects: [read]\n"} {
		current := strings.Replace(withTools(lines), "version: 1", "version: 3", 1)
		_, err := Decode(strings.NewReader(current), testProviders)
		var p Problem
		if !errors.As(err, &p) || p.Code != CodePermissionsRenamed || p.Field != "effects" || !strings.Contains(p.Hint(), "effects") {
			t.Errorf("%q: error = %v, want the problem %s with a hint", lines, err, CodePermissionsRenamed)
		}
	}
	older := strings.Replace(withTools("    permissions: [read]\n"), "version: 1", "version: 2", 1)
	if _, err := Decode(strings.NewReader(older), testProviders); err != nil {
		t.Errorf("a version 2 file with permissions is still read: %v", err)
	}
}
