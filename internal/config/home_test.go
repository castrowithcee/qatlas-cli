package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// legacyHome builds home/.qatlas/cli with the kinds of entries an installation holds and returns home.
func legacyHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	legacy := filepath.Join(home, ".qatlas", "cli")
	files := map[string]string{
		"config.yaml":                 "version: 4\n",
		"config.yaml.lock":            "",
		"tui.yaml":                    "theme: dark\n",
		"backups/config.1.bak":        "version: 3\n",
		"vault/secrets.age":           "sealed",
		"vault/key.age":               "key",
		"vault/logs/.lock":            "",
		"vault/logs/2026-11-03.jsonl": `{"seq":1}` + "\n",
		"vault/pending/change.json":   "{}",
	}
	for rel, content := range files {
		path := filepath.Join(legacy, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink("config.yaml", filepath.Join(legacy, "linked.yaml")); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// snapshot records every entry below dir with its kind, permissions, and content or link target.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		desc := info.Mode().String()
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			link, _ := os.Readlink(path)
			desc += " -> " + link
		case info.Mode().IsRegular():
			data, _ := os.ReadFile(path)
			desc += " " + string(data)
		}
		out[filepath.ToSlash(rel)] = desc
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMigrateHomeCopiesTheLegacyDirectory(t *testing.T) {
	home := legacyHome(t)
	legacy := filepath.Join(home, ".qatlas", "cli")
	before := snapshot(t, legacy)

	m, err := migrateHome(home)

	if err != nil || m == nil {
		t.Fatalf("migrateHome() = %v, %v, want a migration", m, err)
	}
	target := filepath.Join(home, ".qatlas", "connect")
	if m.From != legacy || m.To != target {
		t.Errorf("migration = %+v, want %s to %s", m, legacy, target)
	}
	after := snapshot(t, legacy)
	if len(after) != len(before) {
		t.Fatalf("the legacy directory changed: %v, want %v", after, before)
	}
	for rel, desc := range before {
		if after[rel] != desc {
			t.Errorf("legacy %s = %q, want it unchanged %q", rel, after[rel], desc)
		}
	}
	copied := snapshot(t, target)
	for rel, desc := range before {
		if strings.HasSuffix(rel, ".lock") {
			if _, ok := copied[rel]; ok {
				t.Errorf("lock file %s was copied", rel)
			}
			continue
		}
		if runtime.GOOS == "windows" && strings.HasPrefix(desc, "d") {
			// Windows reports no directory permissions to compare.
			if _, ok := copied[rel]; !ok {
				t.Errorf("directory %s was not copied", rel)
			}
			continue
		}
		if copied[rel] != desc {
			t.Errorf("copy of %s = %q, want %q", rel, copied[rel], desc)
		}
	}
	if entries, _ := filepath.Glob(filepath.Join(home, ".qatlas", stagingPrefix+"*")); len(entries) > 0 {
		t.Errorf("staging left behind: %v", entries)
	}
}

func TestMigrateHomeRunsOnce(t *testing.T) {
	home := legacyHome(t)
	if m, err := migrateHome(home); err != nil || m == nil {
		t.Fatalf("first migrateHome() = %v, %v", m, err)
	}
	marker := filepath.Join(home, ".qatlas", "connect", "config.yaml")
	if err := os.WriteFile(marker, []byte("version: 4\n# changed after the migration\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m, err := migrateHome(home)

	if err != nil || m != nil {
		t.Fatalf("second migrateHome() = %v, %v, want nothing to do", m, err)
	}
	if data, _ := os.ReadFile(marker); !strings.Contains(string(data), "changed after") {
		t.Error("a second run replaced the migrated configuration")
	}
}

func TestMigrateHomeConcurrentProcessesMigrateOnce(t *testing.T) {
	home := legacyHome(t)
	const runs = 8
	var wg sync.WaitGroup
	results := make(chan *HomeMigration, runs)
	errs := make(chan error, runs)
	for range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := migrateHome(home)
			results <- m
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("migrateHome() error = %v", err)
		}
	}
	migrated := 0
	for m := range results {
		if m != nil {
			migrated++
		}
	}
	if migrated != 1 {
		t.Errorf("%d runs migrated, want exactly 1", migrated)
	}
	if _, err := os.Stat(filepath.Join(home, ".qatlas", "connect", "vault", "secrets.age")); err != nil {
		t.Errorf("the migrated vault is incomplete: %v", err)
	}
}

func TestMigrateHomeRepeatsAnInterruptedRun(t *testing.T) {
	home := legacyHome(t)
	stale := filepath.Join(home, ".qatlas", stagingPrefix+"123")
	if err := os.MkdirAll(filepath.Join(stale, "vault"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "config.yaml"), []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}

	m, err := migrateHome(home)

	if err != nil || m == nil {
		t.Fatalf("migrateHome() = %v, %v, want a migration", m, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("the interrupted staging directory is still there: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".qatlas", "connect", "config.yaml"))
	if err != nil || string(data) != "version: 4\n" {
		t.Errorf("migrated config.yaml = %q, %v, want the legacy content", data, err)
	}
}

func TestMigrateHomeWithoutLegacyDoesNothing(t *testing.T) {
	home := t.TempDir()

	m, err := migrateHome(home)

	if err != nil || m != nil {
		t.Fatalf("migrateHome() = %v, %v, want nothing to do", m, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".qatlas", "connect")); !os.IsNotExist(err) {
		t.Errorf("the target was created without a legacy directory: %v", err)
	}
}

func TestMigrateHomeLeavesAnExplicitPlaceAlone(t *testing.T) {
	home := legacyHome(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, tt := range []struct {
		name, explicit, config, cliHome string
	}{
		{name: "flag", explicit: filepath.Join(home, "elsewhere.yaml")},
		{name: "QATLAS_CONFIG", config: filepath.Join(home, "elsewhere.yaml")},
		{name: "QATLAS_CLI_HOME", cliHome: filepath.Join(home, "elsewhere")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("QATLAS_CONFIG", tt.config)
			t.Setenv("QATLAS_CLI_HOME", tt.cliHome)

			m, err := MigrateHome(tt.explicit)

			if err != nil || m != nil {
				t.Fatalf("MigrateHome() = %v, %v, want nothing to do", m, err)
			}
			if _, err := os.Stat(filepath.Join(home, ".qatlas", "connect")); !os.IsNotExist(err) {
				t.Errorf("an explicit place migrated the default directory: %v", err)
			}
		})
	}
}
