package config_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/config"
)

// Agents often start several MCP servers at once, each its own process; exactly one of them migrates.
func TestMigrateHomeAcrossProcesses(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, ".qatlas", "cli")
	if err := os.MkdirAll(filepath.Join(legacy, "vault", "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	for rel, content := range map[string]string{
		"config.yaml":                 "version: 4\n",
		"vault/secrets.age":           "sealed",
		"vault/logs/2026-11-03.jsonl": "{}\n",
	} {
		if err := os.WriteFile(filepath.Join(legacy, filepath.FromSlash(rel)), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	const processes = 4
	outputs := make([]string, processes)
	var wg sync.WaitGroup
	for i := range processes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(exe)
			cmd.Env = append(os.Environ(), config.WorkerModeEnv+"=migratehome", "HOME="+home, "USERPROFILE="+home,
				"QATLAS_CONFIG=", "QATLAS_CLI_HOME=")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("process %d: %v: %s", i, err, out)
			}
			outputs[i] = string(out)
		}()
	}
	wg.Wait()

	migrated := 0
	for _, out := range outputs {
		migrated += strings.Count(out, "migrated")
	}
	if migrated != 1 {
		t.Errorf("%d processes migrated, want exactly 1: %q", migrated, outputs)
	}
	data, err := os.ReadFile(filepath.Join(home, ".qatlas", "connect", "vault", "logs", "2026-11-03.jsonl"))
	if err != nil || string(data) != "{}\n" {
		t.Errorf("migrated log = %q, %v", data, err)
	}
}
