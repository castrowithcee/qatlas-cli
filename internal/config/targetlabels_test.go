package config

import (
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

func labelledConfig(t *testing.T) *Config {
	t.Helper()
	cfg := problemsBase(t)
	cfg.Connections["seat"] = Connection{Service: "seat", Credential: "seatkey", Targets: []string{"t1", "t2"},
		TargetLabels: map[string]string{"t1": "Projects", "t2": "Archive"}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestTargetLabelsRoundTripByteStable(t *testing.T) {
	first, err := yaml.Marshal(labelledConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "target_labels:") {
		t.Fatalf("the labels were not written:\n%s", first)
	}
	back, err := Decode(strings.NewReader(string(first)), filesProviders{testProviders})
	if err != nil {
		t.Fatal(err)
	}
	if got := back.Connections["seat"].TargetLabels["t1"]; got != "Projects" {
		t.Fatalf("label after load = %q", got)
	}
	second, err := yaml.Marshal(back)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("rewrite is not byte-stable:\n%s\n---\n%s", first, second)
	}
	plain, _ := yaml.Marshal(problemsBase(t))
	if strings.Contains(string(plain), "target_labels") {
		t.Errorf("a connection without labels wrote the field")
	}
}

func TestCloneDoesNotShareTargetLabels(t *testing.T) {
	cfg := labelledConfig(t)
	clone := cfg.Clone()
	clone.Connections["seat"].TargetLabels["t1"] = "changed"
	if cfg.Connections["seat"].TargetLabels["t1"] != "Projects" {
		t.Error("Clone shares the label map")
	}
}

func TestSetConnectionDropsLabelsOfRemovedTargets(t *testing.T) {
	cfg := labelledConfig(t)
	conn := cfg.Connections["seat"]
	conn.Targets = []string{"t1"}
	labels := conn.TargetLabels
	if err := cfg.SetConnection("seat", conn); err != nil {
		t.Fatal(err)
	}
	got := cfg.Connections["seat"].TargetLabels
	if len(got) != 1 || got["t1"] != "Projects" {
		t.Errorf("labels after removing t2 = %v", got)
	}
	if len(labels) != 2 {
		t.Errorf("the caller's map was changed: %v", labels)
	}
	conn.Targets = []string{"t3"}
	if err := cfg.SetConnection("seat", conn); err != nil {
		t.Fatal(err)
	}
	if cfg.Connections["seat"].TargetLabels != nil {
		t.Errorf("labels left without a target: %v", cfg.Connections["seat"].TargetLabels)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v", err)
	}
}

func TestSetConnectionNormalizesLabels(t *testing.T) {
	cfg := labelledConfig(t)
	conn := cfg.Connections["seat"]
	conn.TargetLabels = map[string]string{"t1": "  big \t  projects "}
	if err := cfg.SetConnection("seat", conn); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Connections["seat"].TargetLabels["t1"]; got != "big projects" {
		t.Errorf("label = %q", got)
	}
}

func TestLabelOnlyChangeIsAConnectionChange(t *testing.T) {
	before := labelledConfig(t)
	after := before.Clone()
	after.Connections["seat"].TargetLabels["t1"] = "Other"
	changes := ChangedConnections(before, after)
	if len(changes) != 1 || changes[0].Name != "seat" || changes[0].Kind != ConnectionChanged {
		t.Errorf("changes = %v", changes)
	}
}

func TestHandWrittenOrphanLabelIsReportedNotRepaired(t *testing.T) {
	cfg := labelledConfig(t)
	conn := cfg.Connections["seat"]
	conn.TargetLabels["gone"] = "Old"
	cfg.Connections["seat"] = conn
	problems := Problems(cfg.Validate())
	if len(problems) != 1 || problems[0].Code != CodeUnknownTargetLabel {
		t.Fatalf("problems = %v", problems)
	}
	if _, ok := cfg.Connections["seat"].TargetLabels["gone"]; !ok {
		t.Error("validation repaired the label")
	}
}
