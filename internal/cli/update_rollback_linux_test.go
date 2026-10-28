package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/release/releasetest"
	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const (
	// newConfig is a configuration the stand-in older program does not read: it rejects a version line 99.
	unreadableConfig = "version: 99\nmarker: current-config\n"
	readableConfig   = "version: 4\nmarker: readable\n"
	// rollbackProgram stands in for an older release's program: it reads a configuration unless its file
	// has the version line 99, and records the file it was given.
	rollbackProgram = "#!/bin/sh\nif grep -q '^version: 99' \"$2\"; then exit 1; fi\nexit 0\n"
)

// rollbackFixture is an installed program and a configuration directory with backups.
type rollbackFixture struct {
	dir        string
	config     string
	executable string
	replaced   int
}

func newRollbackFixture(t *testing.T, current string, backups map[string]string) *rollbackFixture {
	t.Helper()
	f := &rollbackFixture{dir: t.TempDir(), executable: installedProgram(t)}
	f.config = configIn(f.dir)
	if err := os.WriteFile(f.config, []byte(current), 0o600); err != nil {
		t.Fatal(err)
	}
	if len(backups) > 0 {
		if err := os.MkdirAll(filepath.Join(f.dir, "backups"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range backups {
		if err := os.WriteFile(filepath.Join(f.dir, "backups", name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func withChosenProgram(t *testing.T, program string) {
	t.Helper()
	original := chosenProgram
	chosenProgram = program
	t.Cleanup(func() { chosenProgram = original })
}

// run runs 'qatlas update --to v0.31.0' with args against the fixture and returns the exit code, stderr, and the
// installed program.
func (f *rollbackFixture) run(t *testing.T, tweak func(*selfupdate.Client), args ...string) (int, string, string) {
	t.Helper()
	updater := chosenUpdater(t, f.executable, chosenReleases, &f.replaced)
	if tweak != nil {
		tweak(updater)
	}
	code, _, stderr := runWithInput(t, &Options{Updater: updater}, "",
		append([]string{"update", "--config", f.config, "--to", "v0.31.0"}, args...)...)
	data, _ := os.ReadFile(f.executable)
	return code, stderr, string(data)
}

// snapshot is the content and modification time of every file in the configuration directory.
func (f *rollbackFixture) snapshot(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(f.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		out[path] = info.ModTime().String() + "|" + string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *rollbackFixture) assertUnchanged(t *testing.T, before map[string]string) {
	t.Helper()
	after := f.snapshot(t)
	if len(after) != len(before) {
		t.Errorf("the configuration directory changed: %d files before, %d after", len(before), len(after))
	}
	for path, want := range before {
		if after[path] != want {
			t.Errorf("%s changed", path)
		}
	}
}

func (f *rollbackFixture) backupBodies(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, _ := os.ReadDir(filepath.Join(f.dir, "backups"))
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(f.dir, "backups", e.Name()))
		out[e.Name()] = string(data)
	}
	return out
}

const (
	newestReadable = "config-20260301T100000Z-000.yaml"
	unreadableOne  = "config-20260401T100000Z-000.yaml"
	olderReadable  = "config-20260101T100000Z-000.yaml"
)

func rollbackBackups() map[string]string {
	return map[string]string{
		unreadableOne:  "version: 99\nmarker: unreadable-backup\n",
		newestReadable: "version: 4\nmarker: newest-readable\n",
		olderReadable:  "version: 4\nmarker: older-readable\n",
	}
}

func TestRollbackLeavesAReadableConfigurationAlone(t *testing.T) {
	withChosenProgram(t, rollbackProgram)
	withInteractive(t, false)
	f := newRollbackFixture(t, readableConfig, rollbackBackups())
	before := f.snapshot(t)
	code, stderr, program := f.run(t, nil, "--yes")
	if code != exitOK || program != rollbackProgram || f.replaced != 1 {
		t.Fatalf("exit code = %d, stderr = %q, replaced %d", code, stderr, f.replaced)
	}
	f.assertUnchanged(t, before)
}

func TestRollbackWithoutAConfigurationFileInstalls(t *testing.T) {
	withChosenProgram(t, rollbackProgram)
	withInteractive(t, false)
	f := newRollbackFixture(t, readableConfig, nil)
	must := os.Remove(f.config)
	if must != nil {
		t.Fatal(must)
	}
	code, stderr, program := f.run(t, nil, "--yes")
	if code != exitOK || program != rollbackProgram || f.replaced != 1 {
		t.Fatalf("exit code = %d, stderr = %q, replaced %d", code, stderr, f.replaced)
	}
}

func TestRollbackRestoresTheNewestReadableBackupWithTheFlag(t *testing.T) {
	withChosenProgram(t, rollbackProgram)
	withInteractive(t, false)
	f := newRollbackFixture(t, unreadableConfig, rollbackBackups())
	code, stderr, program := f.run(t, nil, "--yes", "--restore-backup")
	if code != exitOK || program != rollbackProgram || f.replaced != 1 {
		t.Fatalf("exit code = %d, stderr = %q, replaced %d", code, stderr, f.replaced)
	}
	if got, _ := os.ReadFile(f.config); string(got) != rollbackBackups()[newestReadable] {
		t.Errorf("configuration = %q, want the newest readable backup", got)
	}
	found := false
	for name, body := range f.backupBodies(t) {
		if body == unreadableConfig && name != unreadableOne {
			found = true
		}
	}
	if !found {
		t.Errorf("the replaced configuration is not among the backups: %v", f.backupBodies(t))
	}
	if !strings.Contains(stderr, newestReadable) {
		t.Errorf("stderr does not name the restored backup: %q", stderr)
	}
}

func TestRollbackWithoutTheFlagNamesTheBackupAndChangesNothing(t *testing.T) {
	withChosenProgram(t, rollbackProgram)
	withInteractive(t, false)
	f := newRollbackFixture(t, unreadableConfig, rollbackBackups())
	before := f.snapshot(t)
	code, stderr, program := f.run(t, nil, "--yes")
	if code != exitUsage || program != "old-program" || f.replaced != 0 {
		t.Fatalf("exit code = %d, stderr = %q, program %q, replaced %d", code, stderr, program, f.replaced)
	}
	for _, want := range []string{"v0.31.0", "does not read the current configuration", newestReadable,
		"2026-03-01 10:00:00 UTC", "--restore-backup", "nothing was changed"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %q", want, stderr)
		}
	}
	if strings.Contains(stderr, "newest-readable") || strings.Contains(stderr, "current-config") {
		t.Errorf("stderr quotes configuration content: %q", stderr)
	}
	f.assertUnchanged(t, before)
}

func TestRollbackWithoutAReadableBackupChangesNothing(t *testing.T) {
	withChosenProgram(t, rollbackProgram)
	withInteractive(t, false)
	f := newRollbackFixture(t, unreadableConfig, map[string]string{unreadableOne: "version: 99\n"})
	before := f.snapshot(t)
	code, stderr, program := f.run(t, nil, "--yes", "--restore-backup")
	if code == exitOK || program != "old-program" || f.replaced != 0 || !strings.Contains(stderr, "none of its backups") {
		t.Fatalf("exit code = %d, stderr = %q, program %q, replaced %d", code, stderr, program, f.replaced)
	}
	f.assertUnchanged(t, before)
}

func TestRollbackAsksForTheProgramAndSeparatelyForTheRestore(t *testing.T) {
	withChosenProgram(t, rollbackProgram)
	withInteractive(t, true)
	for name, tt := range map[string]struct {
		answers  []bool
		restored bool
	}{"restore": {[]bool{true, true}, true}, "no restore": {[]bool{true, false}, false}, "no program": {[]bool{false}, false}} {
		t.Run(name, func(t *testing.T) {
			var asked []string
			withVaultConfirm(t, func(prompt string) (bool, error) {
				asked = append(asked, prompt)
				return tt.answers[len(asked)-1], nil
			})
			f := newRollbackFixture(t, unreadableConfig, rollbackBackups())
			before := f.snapshot(t)
			code, stderr, program := f.run(t, nil)
			if len(asked) != len(tt.answers) {
				t.Fatalf("%d questions, want %d: %q", len(asked), len(tt.answers), asked)
			}
			for _, want := range []string{"older than the running version", "vault process is locked first", "whatever the update behaviour"} {
				if !strings.Contains(asked[0], want) {
					t.Errorf("program question lacks %q: %q", want, asked[0])
				}
			}
			if len(asked) == 2 {
				for _, want := range []string{"v0.31.0 does not read the current configuration", newestReadable,
					"2026-03-01 10:00:00 UTC", "missing in v0.31.0 but kept in that backup"} {
					if !strings.Contains(asked[1], want) {
						t.Errorf("restore question lacks %q: %q", want, asked[1])
					}
				}
			}
			if tt.restored {
				if code != exitOK || program != rollbackProgram {
					t.Fatalf("exit code = %d, stderr = %q, program %q", code, stderr, program)
				}
				if got, _ := os.ReadFile(f.config); string(got) != rollbackBackups()[newestReadable] {
					t.Errorf("configuration = %q", got)
				}
				return
			}
			if code != exitOK || program != "old-program" || f.replaced != 0 || !strings.Contains(stderr, "cancelled") {
				t.Fatalf("exit code = %d, stderr = %q, program %q", code, stderr, program)
			}
			f.assertUnchanged(t, before)
		})
	}
}

// The check runs on copies only: the program is given a path that is none of the real files, and the real
// configuration and backups keep content and modification time.
func TestRollbackChecksCopiesOnly(t *testing.T) {
	withInteractive(t, false)
	f := newRollbackFixture(t, unreadableConfig, rollbackBackups())
	record := filepath.Join(t.TempDir(), "paths")
	withChosenProgram(t, "#!/bin/sh\necho \"$2\" >> "+record+"\nif grep -q '^version: 99' \"$2\"; then exit 1; fi\nexit 0\n")
	before := f.snapshot(t)
	code, stderr, _ := f.run(t, nil, "--yes")
	if code != exitUsage {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	f.assertUnchanged(t, before)
	paths, _ := os.ReadFile(record)
	lines := strings.Fields(string(paths))
	if len(lines) != 3 {
		t.Fatalf("the program was run %d times: %q", len(lines), paths)
	}
	for _, p := range lines {
		if strings.HasPrefix(p, f.dir) {
			t.Errorf("the program was given %s inside the configuration directory", p)
		}
	}
}

func TestRollbackEndsAnUnfinishedCheckWithoutChanges(t *testing.T) {
	withInteractive(t, false)
	withChosenProgram(t, "#!/bin/sh\nexec sleep 30\n")
	f := newRollbackFixture(t, unreadableConfig, rollbackBackups())
	before := f.snapshot(t)
	start := time.Now()
	code, stderr, program := f.run(t, func(c *selfupdate.Client) { c.CheckTimeout = 200 * time.Millisecond }, "--yes", "--restore-backup")
	if code == exitOK || program != "old-program" || f.replaced != 0 || !strings.Contains(stderr, "did not finish") {
		t.Fatalf("exit code = %d, stderr = %q, program %q", code, stderr, program)
	}
	if time.Since(start) > 15*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
	f.assertUnchanged(t, before)
}

func TestRollbackPutsThePreviousConfigurationBackWhenTheReplacementFails(t *testing.T) {
	withInteractive(t, false)
	withChosenProgram(t, rollbackProgram)
	f := newRollbackFixture(t, unreadableConfig, rollbackBackups())
	code, stderr, program := f.run(t, func(c *selfupdate.Client) {
		c.BeforeReplace = func(context.Context, selfupdate.Release) error { return errors.New("replacement refused") }
	}, "--yes", "--restore-backup")
	if code == exitOK || program != "old-program" || !strings.Contains(stderr, "replacement refused") {
		t.Fatalf("exit code = %d, stderr = %q, program %q", code, stderr, program)
	}
	if got, _ := os.ReadFile(f.config); string(got) != unreadableConfig {
		t.Errorf("configuration = %q, want the previous one back", got)
	}
}

func TestRestoreBackupNeedsTo(t *testing.T) {
	withInteractive(t, false)
	for _, args := range [][]string{{"--restore-backup"}, {"--restore-backup", "--check"}, {"--restore-backup", "--list"}} {
		f := newRollbackFixture(t, readableConfig, nil)
		updater := chosenUpdater(t, f.executable, chosenReleases, &f.replaced)
		code, _, stderr := runWithInput(t, &Options{Updater: updater}, "", append([]string{"update", "--config", f.config}, args...)...)
		if code != exitUsage || f.replaced != 0 {
			t.Errorf("%v: exit code = %d, stderr = %q", args, code, stderr)
		}
	}
}

// An older version locks an unlocked vault process before the replacement, though the setting is handover.
func TestRollbackLocksTheVaultProcessWhateverTheSettingSays(t *testing.T) {
	withChosenProgram(t, rollbackProgram)
	withInteractive(t, false)
	dir := encryptedVaultFixture(t, "")
	unlocked := vault.New(dir)
	if _, err := unlocked.Unlock("s3cret-phrase"); err != nil {
		t.Fatal(err)
	}
	signer := releasetest.NewSigner(t)
	signerKeyClient := handoverServer(t, dir, signer.PublicKey())
	f := &rollbackFixture{dir: dir, config: configIn(dir), executable: installedProgram(t)}
	if err := os.WriteFile(f.config, []byte(readableConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	var runningBefore bool
	code, stderr, program := f.run(t, func(c *selfupdate.Client) {
		c.BeforeReplace = func(context.Context, selfupdate.Release) error {
			runningBefore = running(signerKeyClient)
			return nil
		}
	}, "--yes")
	if code != exitOK || program != rollbackProgram {
		t.Fatalf("exit code = %d, stderr = %q, program %q", code, stderr, program)
	}
	if runningBefore || running(signerKeyClient) {
		t.Error("the vault process was not locked before the replacement")
	}
	for _, want := range []string{"older version cannot take over", "'qatlas vault unlock'"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q: %q", want, stderr)
		}
	}
}
