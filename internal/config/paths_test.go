package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

func withPaths(lines string) string {
	return strings.Replace(minimal, "    credential: reader\n", "    credential: reader\n"+lines, 1)
}

func TestPathsAreValidated(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Decode(strings.NewReader(withPaths("    paths: ["+dir+", \"~/repos/kunde-a\", \"~\"]\n")), testProviders)
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	if got := cfg.Connections["wiki"].Paths; !reflect.DeepEqual(got, []string{dir, "~/repos/kunde-a", "~"}) {
		t.Errorf("paths = %v", got)
	}

	const canary = "paths-canary-71c3"
	for _, tt := range []struct {
		name, lines, want string
	}{
		{"an empty list", "    paths: []\n", "connections.wiki.paths: must name at least one directory"},
		{"an empty entry", "    paths: [\"\"]\n", "connections.wiki.paths[0]: must not be empty"},
		{"a relative entry", "    paths: [" + canary + "/repos]\n", "connections.wiki.paths[0]: must name a directory"},
		{"another user's home", "    paths: [\"~" + canary + "/repos\"]\n", "connections.wiki.paths[0]: must name a directory"},
		{"a glob", "    paths: [\"/repos/" + canary + "-*\"]\n", "connections.wiki.paths[0]: glob patterns are not supported"},
		{"a character class", "    paths: [\"/repos/" + canary + "[ab]\"]\n", "glob patterns are not supported"},
		{"a duplicate", "    paths: [/repos/" + canary + ", /repos/" + canary + "/]\n",
			"connections.wiki.paths[1]: a directory is listed more than once"},
		{"blanks at the edges", "    paths: [\" /repos/" + canary + "\"]\n", "must not start or end with blanks"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(withPaths(tt.lines)), testProviders)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Decode() = %v, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), canary) {
				t.Errorf("error = %q quotes the entry", err)
			}
		})
	}
}

func TestPathWarningsNameMissingDirectoriesByPosition(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing")
	cfg, err := Decode(strings.NewReader(withPaths("    paths: ["+dir+", "+missing+", "+file+"]\n")), testProviders)
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	want := []string{
		`connection "wiki": paths[1] names no existing directory, so no project lies inside it`,
		`connection "wiki": paths[2] names no existing directory, so no project lies inside it`,
	}
	if got := cfg.PathWarnings(); !reflect.DeepEqual(got, want) {
		t.Errorf("PathWarnings() = %q, want %q", got, want)
	}
}

func TestPathsSurviveMarshalCloneAndResolve(t *testing.T) {
	cfg, err := Decode(strings.NewReader(withPaths("    paths: [/repos/kunde-a, \"~/repos/kunde-b\"]\n")), testProviders)
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	want := []string{"/repos/kunde-a", "~/repos/kunde-b"}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Decode(strings.NewReader(string(data)), testProviders)
	if err != nil {
		t.Fatalf("Decode(Marshal()) = %v\n%s", err, data)
	}
	if got := again.Connections["wiki"].Paths; !reflect.DeepEqual(got, want) {
		t.Errorf("paths after a round trip = %v, want %v", got, want)
	}
	clone := cfg.Clone()
	clone.Connections["wiki"].Paths[0] = "/elsewhere"
	if got := cfg.Connections["wiki"].Paths; !reflect.DeepEqual(got, want) {
		t.Errorf("paths after editing a clone = %v, want %v", got, want)
	}
	resolved, err := cfg.Resolve("wiki", "")
	if err != nil || !reflect.DeepEqual(resolved.Paths, want) {
		t.Errorf("Resolve() paths = %v, %v", resolved, err)
	}

	unbound, err := Decode(strings.NewReader(minimal), testProviders)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := yaml.Marshal(unbound); strings.Contains(string(data), "paths") {
		t.Errorf("a connection without paths is saved with them:\n%s", data)
	}
}

func TestForProjectsKeepsOnlyApplyingConnectionsAndDefaults(t *testing.T) {
	base := t.TempDir()
	kundeA := filepath.Join(base, "repos", "kunde-a")
	cfg, err := Decode(strings.NewReader(withPaths("    paths: ["+kundeA+"]\n")), testProviders)
	if err != nil {
		t.Fatalf("Decode() = %v", err)
	}
	if !cfg.UsesPaths() {
		t.Fatal("UsesPaths() = false")
	}
	for _, tt := range []struct {
		projects []string
		want     bool
	}{
		{nil, false},
		{[]string{kundeA}, true},
		{[]string{filepath.Join(kundeA, "src")}, true},
		{[]string{filepath.Join(base, "repos", "kunde-ab")}, false},
		{[]string{filepath.Join(base, "repos")}, false},
		{[]string{filepath.Join(base, "other"), kundeA}, true},
	} {
		view := cfg.ForProjects(tt.projects)
		_, listed := view.Connections["wiki"]
		_, defaulted := view.Defaults.Connections["knowledge"]
		if listed != tt.want || defaulted != tt.want || cfg.ConnectionApplies("wiki", tt.projects) != tt.want {
			t.Errorf("ForProjects(%v): listed=%v default=%v, want %v", tt.projects, listed, defaulted, tt.want)
		}
	}
	if _, ok := cfg.Connections["wiki"]; !ok {
		t.Error("ForProjects changed the configuration it was taken from")
	}
}
