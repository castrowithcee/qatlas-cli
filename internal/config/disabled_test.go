package config

import (
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

func TestConnectionDisabledRoundTripsAndDefaultsToActive(t *testing.T) {
	cfg, err := Decode(strings.NewReader(minimal), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Connections["wiki"].Disabled {
		t.Fatal("a connection without the field must be active")
	}
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "disabled") {
		t.Fatalf("an active connection wrote the field:\n%s", encoded)
	}

	connection := cfg.Connections["wiki"]
	connection.Disabled = true
	cfg.Connections["wiki"] = connection
	if !cfg.Clone().Connections["wiki"].Disabled {
		t.Fatal("Clone dropped the state")
	}
	encoded, err = yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "disabled: true") {
		t.Fatalf("the state was not written:\n%s", encoded)
	}
	back, err := Decode(strings.NewReader(string(encoded)), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Connections["wiki"].Disabled {
		t.Fatal("the state was lost on the way back")
	}
}

func TestDisabledRefusalNamesOnlyWhatWouldBeOffered(t *testing.T) {
	cfg, err := Decode(strings.NewReader(strings.Replace(minimal,
		"    credential: reader\n", "    credential: reader\n    permissions: [read]\n    disabled: true\n", 1)),
		testProviders)
	if err != nil {
		t.Fatal(err)
	}
	read := ToolMetadata{ID: "bookstack.pages.list", Effect: EffectRead}
	update := ToolMetadata{ID: "bookstack.pages.update", Effect: EffectUpdate}
	if got := cfg.ConnectionRefusal("wiki", read); got != RefusalDisabled {
		t.Errorf("refusal of an offered tool = %q, want %q", got, RefusalDisabled)
	}
	if got := cfg.ConnectionRefusal("wiki", update); got != RefusalEffect {
		t.Errorf("refusal of a tool the selection refuses = %q, want %q", got, RefusalEffect)
	}
	if got := cfg.IdleWarning("wiki"); got != "" {
		t.Errorf("a disabled connection warns: %s", got)
	}
}
