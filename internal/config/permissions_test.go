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
	if !cfg.ConnectionAllows("wiki", "read") || !cfg.ConnectionAllows("wiki", "update") ||
		cfg.ConnectionAllows("wiki", "create") || cfg.ConnectionAllows("wiki", "delete") {
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
