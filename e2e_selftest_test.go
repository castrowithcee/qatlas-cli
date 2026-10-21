package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// selftestInstallation lays out a schema-1 configuration, a vault with a file, logs, and a backups folder the
// way a real installation looks, below a temporary home.
func selftestInstallation(t *testing.T) (home string, env []string) {
	t.Helper()
	home = t.TempDir()
	cliHome := filepath.Join(home, "cli-home")
	files := map[string]string{
		"config.yaml":                      "version: 1\nservices: {}\n",
		"vault/secrets.yaml":               "canary: value\n",
		"vault/logs/2026-01-01.jsonl":      "{}\n",
		"backups/config.yaml.20260101.bak": "old\n",
	}
	for name, content := range files {
		path := filepath.Join(cliHome, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-48 * time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+filepath.Join(home, "xdg"),
		"APPDATA="+filepath.Join(home, "appdata"), "QATLAS_CLI_HOME="+cliHome,
		"QATLAS_CONFIG="+filepath.Join(cliHome, "config.yaml"))
	return home, env
}

type fileState struct {
	Content string
	Mtime   time.Time
}

// snapshotTree records every file below root with its bytes and modification time.
func snapshotTree(t *testing.T, root string) map[string]fileState {
	t.Helper()
	out := map[string]fileState{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			out[rel+"/"] = fileState{}
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[rel] = fileState{string(data), info.ModTime()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func scratchDirs(t *testing.T, base string) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(base, "qatlas-selftest-*"))
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// The selftest of the built binary measures in the smoke profile, as text and as JSON, and leaves the
// installation byte- and mtime-identical, a configuration of an older schema included, which no other command
// would leave alone. The scratch directory below --dir is gone afterwards.
func TestSelftestLeavesInstallationUntouched(t *testing.T) {
	if testing.Short() {
		t.Skip("the selftest builds the binary")
	}
	bin := buildBinary(t, t.TempDir())
	home, env := selftestInstallation(t)
	before := snapshotTree(t, home)
	base := t.TempDir()

	run := func(args ...string) (string, string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%v: %v\nstderr: %s", args, err, stderr.String())
		}
		return stdout.String(), stderr.String()
	}

	text, stderr := run("selftest", "storage", "--profile", "smoke", "--dir", base)
	for _, want := range []string{"selftest storage (profile smoke)", "system:", "CPUs", "directory:",
		"read-under-write", "lock-wait", "append", "read-p95-ms="} {
		if !strings.Contains(text, want) {
			t.Errorf("text output lacks %q:\n%s", want, text)
		}
	}
	if !strings.Contains(stderr, "lock-wait") || strings.Contains(stderr, "migrated") {
		t.Errorf("stderr = %q, want progress and no migration", stderr)
	}

	jsonOut, _ := run("selftest", "storage", "--profile", "smoke", "--json", "--dir", base)
	var report struct {
		Scenario     string `json:"scenario"`
		Profile      string `json:"profile"`
		TempDir      string `json:"temp_dir"`
		Measurements []struct {
			Group  string `json:"group"`
			Values []struct {
				Name  string  `json:"name"`
				Value float64 `json:"value"`
			} `json:"values"`
		} `json:"measurements"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &report); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, jsonOut)
	}
	if report.Scenario != "storage" || report.Profile != "smoke" || len(report.Measurements) == 0 ||
		filepath.Dir(report.TempDir) != base {
		t.Errorf("report = %+v", report)
	}
	viaOutput, _ := run("selftest", "storage", "--profile", "smoke", "--output", "json", "--dir", base)
	if !json.Valid([]byte(viaOutput)) {
		t.Errorf("--output json is not JSON:\n%s", viaOutput)
	}

	if after := snapshotTree(t, home); !reflect.DeepEqual(before, after) {
		t.Errorf("the installation changed:\nbefore %v\nafter  %v", before, after)
	}
	if left := scratchDirs(t, base); len(left) != 0 {
		t.Errorf("scratch directories left behind: %v", left)
	}
}

// A worker that speaks another protocol version stops with a clear message instead of measuring.
func TestSelftestWorkerRefusesOtherProtocol(t *testing.T) {
	if testing.Short() {
		t.Skip("the selftest builds the binary")
	}
	bin := buildBinary(t, t.TempDir())
	cmd := exec.Command(bin, "selftest", "worker")
	cmd.Stdin = strings.NewReader(`{"protocol":999,"path":"x","name":"y"}` + "\n")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() == 0 {
		t.Fatalf("err = %v, want a non-zero exit", err)
	}
	if !strings.Contains(stderr.String(), "protocol version 999 is not supported") {
		t.Errorf("stderr = %q", stderr.String())
	}
}
