package config

import (
	"reflect"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

func TestConnectionPermissionsAreIndependentFromCredentials(t *testing.T) {
	cfg, err := Decode(strings.NewReader(strings.Replace(minimal,
		"    credential: reader\n", "    credential: reader\n    permissions: [read, update]\n", 1)), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	want := []Permission{PermissionRead, PermissionUpdate}
	if got := cfg.ConnectionPermissions("wiki"); !reflect.DeepEqual(got, want) {
		t.Fatalf("permissions = %v, want %v", got, want)
	}
	if !cfg.ConnectionAllows("wiki", "bookstack.pages.list", "read") || !cfg.ConnectionAllows("wiki", "bookstack.pages.list", "update") ||
		cfg.ConnectionAllows("wiki", "bookstack.pages.list", "create") || cfg.ConnectionAllows("wiki", "bookstack.pages.list", "delete") {
		t.Fatal("connection permission check does not match the explicit local allow-list")
	}
}

func TestConnectionPermissionsRoundTripMissingAndDenyAll(t *testing.T) {
	missing, err := Decode(strings.NewReader(minimal), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := yaml.Marshal(missing)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "permissions:") {
		t.Fatalf("missing permissions were materialized:\n%s", encoded)
	}

	connection := missing.Connections["wiki"]
	connection.Permissions = []Permission{}
	missing.Connections["wiki"] = connection
	encoded, err = yaml.Marshal(missing)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "permissions: []") {
		t.Fatalf("deny-all was omitted:\n%s", encoded)
	}
	roundTrip, err := Decode(strings.NewReader(string(encoded)), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if got := roundTrip.ConnectionPermissions("wiki"); got == nil || len(got) != 0 {
		t.Fatalf("round-trip permissions = %#v", got)
	}
}

func TestMissingAndEmptyConnectionPermissionsStaySafe(t *testing.T) {
	cfg, err := Decode(strings.NewReader(minimal), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ConnectionPermissions("wiki"); !reflect.DeepEqual(got, []Permission{PermissionRead}) {
		t.Fatalf("missing permissions = %v, want the safe read compatibility default", got)
	}

	empty, err := Decode(strings.NewReader(strings.Replace(minimal,
		"    credential: reader\n", "    credential: reader\n    permissions: []\n", 1)), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if got := empty.ConnectionPermissions("wiki"); got == nil || len(got) != 0 {
		t.Fatalf("explicit empty permissions = %#v, want a non-nil deny-all list", got)
	}
}

func TestConnectionPermissionsRejectUnknownAndDuplicateValues(t *testing.T) {
	for _, permissions := range []string{"[read, publish]", "[read, read]"} {
		input := strings.Replace(minimal, "    credential: reader\n",
			"    credential: reader\n    permissions: "+permissions+"\n", 1)
		if _, err := Decode(strings.NewReader(input), testProviders); err == nil {
			t.Fatalf("permissions %s were accepted", permissions)
		}
	}
}

func TestPermissionTextDistinguishesDefaultAndDenyAll(t *testing.T) {
	if got, err := ParsePermissions(""); err != nil || got != nil {
		t.Fatalf("empty = %#v, %v", got, err)
	}
	got, err := ParsePermissions("none")
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("none = %#v, %v", got, err)
	}
	if FormatPermissions(nil) != "" || FormatPermissions([]Permission{}) != "none" {
		t.Fatal("permission formatting lost default/deny-all distinction")
	}
}

func TestSeaTableTargetAllowListRoundTripsWithoutChangingSingleTargets(t *testing.T) {
	input := `version: 1
services:
  main: {provider: seatable, base_url: https://cloud.seatable.io}
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

func TestConnectionToolsNarrowThePermissions(t *testing.T) {
	missing, err := Decode(strings.NewReader(minimal), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if !missing.ConnectionAllows("wiki", "bookstack.pages.list", "read") ||
		!missing.ConnectionAllows("wiki", "bookstack.pages.get", "read") {
		t.Fatal("a connection without tools no longer offers every tool its permissions admit")
	}

	listed, err := Decode(strings.NewReader(withTools(
		"    permissions: [read, create]\n    tools: [bookstack.pages.list]\n")), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if !listed.ConnectionAllows("wiki", "bookstack.pages.list", "read") {
		t.Fatal("a listed tool is refused")
	}
	for _, tool := range []struct{ id, effect string }{
		{"bookstack.pages.get", "read"}, {"bookstack.pages.create", "create"}, {"bookstack.pages.search", "read"},
	} {
		if listed.ConnectionAllows("wiki", tool.id, tool.effect) {
			t.Errorf("%s is offered although the tools list does not name it", tool.id)
		}
	}
	// The tools list never widens the permissions: a listed tool still needs its effect.
	if listed.ConnectionAllows("wiki", "bookstack.pages.list", "delete") {
		t.Error("a listed tool is offered for an effect the permissions exclude")
	}

	empty, err := Decode(strings.NewReader(withTools("    tools: []\n")), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if empty.ConnectionAllows("wiki", "bookstack.pages.list", "read") {
		t.Fatal("an explicit empty tools list does not deny every tool")
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

func TestConnectionToolsRejectUnknownForeignDuplicateAndExcludedTools(t *testing.T) {
	for _, tc := range []struct{ lines, want string }{
		{"    tools: [bookstack.pages.search]\n",
			`connections.wiki.tools: entry 1 is not a registered tool of provider "bookstack"`},
		{"    tools: [bookstack.pages.list, s3cr3t-pasted-by-mistake]\n",
			`connections.wiki.tools: entry 2 is not a registered tool of provider "bookstack"`},
		{"    tools: [telegram.messages.send]\n",
			`connections.wiki.tools: tool "telegram.messages.send" belongs to provider "telegram", not to "bookstack"`},
		{"    tools: [bookstack.pages.list, bookstack.pages.list]\n",
			`connections.wiki.tools: tool "bookstack.pages.list" is listed more than once`},
		{"    tools: [bookstack.pages.create]\n",
			`connections.wiki.tools: tool "bookstack.pages.create" has effect create, which the connection's ` +
				`permissions do not allow`},
		{"    permissions: []\n    tools: [bookstack.pages.list]\n",
			`connections.wiki.tools: tool "bookstack.pages.list" has effect read, which the connection's ` +
				`permissions do not allow`},
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
	if !cfg.ConnectionAllows("wiki", "bookstack.pages.list", "read") ||
		cfg.ConnectionAllows("wiki", "bookstack.pages.get", "read") {
		t.Error("wiki does not offer exactly its own tool")
	}
	if !cfg.ConnectionAllows("wiki-reader", "bookstack.pages.get", "read") ||
		cfg.ConnectionAllows("wiki-reader", "bookstack.pages.list", "read") {
		t.Error("wiki-reader does not offer exactly its own tool")
	}
}
