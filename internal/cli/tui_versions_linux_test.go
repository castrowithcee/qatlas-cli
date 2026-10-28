package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/release/releasetest"
	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// versionUpdaterFor is the editor's updater on a client against chosenReleases, with the management core of
// the configuration in dir, the way the editor wires it.
func versionUpdaterFor(t *testing.T, dir string, client *selfupdate.Client) *lockingUpdater {
	t.Helper()
	opts := &Options{Updater: client, Config: configIn(dir)}
	u, ok := tuiUpdater(opts, "v1.0.0").(*lockingUpdater)
	if !ok {
		t.Fatal("the editor has no updater")
	}
	u.svc = manage.New(config.NewStore(configIn(dir), sharedRegistry()), nil, connlog.SurfaceTUI, opts.Redactor)
	return u
}

// The editor lists the same choice as 'qatlas update --list'.
func TestTUIUpdaterListsTheChoosableVersions(t *testing.T) {
	u := versionUpdaterFor(t, t.TempDir(), chosenUpdater(t, installedProgram(t), chosenReleases, new(int)))
	entries, err := u.Versions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, fmt.Sprintf("%s/%t/%t/%t", e.Version, e.Prerelease, e.Current, e.Older))
	}
	want := "v1.3.0-rc.1/true/false/false v1.2.0/false/false/false v1.0.0/false/true/false v0.31.0/false/false/true"
	if strings.Join(got, " ") != want {
		t.Errorf("list = %v, want %s", got, want)
	}
}

func TestTUIUpdaterInstallsAChosenPrerelease(t *testing.T) {
	withChosenProgram(t, "chosen-program")
	executable := installedProgram(t)
	var replaced int
	u := versionUpdaterFor(t, t.TempDir(), chosenUpdater(t, executable, chosenReleases, &replaced))
	var downloaded int
	result, err := u.InstallVersion(context.Background(), "v1.3.0-rc.1", func(done, total int64) { downloaded++ })
	if err != nil || !result.Updated || result.Latest != "v1.3.0-rc.1" || replaced != 1 {
		t.Fatalf("InstallVersion() = %+v, %v, replaced %d", result, err, replaced)
	}
	if data, _ := os.ReadFile(executable); string(data) != "chosen-program" || downloaded == 0 {
		t.Errorf("program %q, progress reports %d", data, downloaded)
	}
	// An older version is not installed this way.
	if _, err := u.InstallVersion(context.Background(), "v0.31.0", nil); err == nil || replaced != 1 {
		t.Errorf("an older version was installed as an update: %v", err)
	}
}

// A rollback locks an unlocked vault process before the replacement, though the setting is handover, and
// only then; preparing it changes nothing.
func TestTUIUpdaterRollbackLocksTheVaultProcessWhateverTheSettingSays(t *testing.T) {
	withChosenProgram(t, rollbackProgram)
	dir := encryptedVaultFixture(t, "")
	if _, err := vault.New(dir).Unlock("s3cret-phrase"); err != nil {
		t.Fatal(err)
	}
	signer := releasetest.NewSigner(t)
	vaultClient := handoverServer(t, dir, signer.PublicKey())
	if err := os.WriteFile(configIn(dir), []byte(readableConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := installedProgram(t)
	var runningBefore bool
	client := chosenUpdater(t, executable, chosenReleases, new(int))
	client.BeforeReplace = func(context.Context, selfupdate.Release) error {
		runningBefore = running(vaultClient)
		return nil
	}
	u := versionUpdaterFor(t, dir, client)

	plan, err := u.PrepareRollback(context.Background(), "v0.31.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Reads() {
		t.Fatal("the older program does not read a readable configuration")
	}
	if _, _, ok := plan.Backup(); ok {
		t.Error("a backup is offered though the configuration is read")
	}
	if data, _ := os.ReadFile(executable); string(data) != "old-program" || !running(vaultClient) {
		t.Fatalf("preparing changed the program (%q) or locked the vault process", data)
	}

	result, err := plan.Install(context.Background(), false)
	if err != nil || !result.Updated || result.Latest != "v0.31.0" {
		t.Fatalf("Install() = %+v, %v", result, err)
	}
	if runningBefore || running(vaultClient) {
		t.Error("the vault process was not locked before the replacement")
	}
	if data, _ := os.ReadFile(executable); string(data) != rollbackProgram {
		t.Errorf("program = %q", data)
	}
	if note := u.Note(); !strings.Contains(note, "older version cannot take over") || !strings.Contains(note, "'qatlas vault unlock'") {
		t.Errorf("note = %q", note)
	}
}

// A configuration the older program does not read offers the newest backup it reads; only a confirmed
// restore changes the files, and a rollback without any readable backup offers none.
func TestTUIUpdaterRollbackRestoresABackupOnlyWhenConfirmed(t *testing.T) {
	withChosenProgram(t, rollbackProgram)
	f := newRollbackFixture(t, unreadableConfig, rollbackBackups())
	u := versionUpdaterFor(t, f.dir, chosenUpdater(t, f.executable, chosenReleases, &f.replaced))
	before := f.snapshot(t)

	plan, err := u.PrepareRollback(context.Background(), "v0.31.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	name, at, ok := plan.Backup()
	if plan.Reads() || !ok || name != newestReadable || at.UTC().Format("2006-01-02 15:04") != "2026-03-01 10:00" {
		t.Fatalf("Reads %v, backup %q at %v (%v)", plan.Reads(), name, at, ok)
	}
	f.assertUnchanged(t, before)

	if _, err := plan.Install(context.Background(), false); !errors.Is(err, manage.ErrRestoreNotConfirmed) {
		t.Errorf("Install(false) = %v, want ErrRestoreNotConfirmed", err)
	}
	f.assertUnchanged(t, before)
	if data, _ := os.ReadFile(f.executable); string(data) != "old-program" || f.replaced != 0 {
		t.Fatalf("the program changed without a confirmed restore: %q", data)
	}

	if _, err := plan.Install(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(f.config); string(got) != rollbackBackups()[newestReadable] {
		t.Errorf("configuration = %q", got)
	}
	if data, _ := os.ReadFile(f.executable); string(data) != rollbackProgram {
		t.Errorf("program = %q", data)
	}

	// No backup it reads: the check says so and offers nothing.
	g := newRollbackFixture(t, unreadableConfig, map[string]string{unreadableOne: "version: 99\n"})
	plan, err = versionUpdaterFor(t, g.dir, chosenUpdater(t, g.executable, chosenReleases, &g.replaced)).
		PrepareRollback(context.Background(), "v0.31.0", nil)
	if _, _, ok := plan.Backup(); err != nil || plan.Reads() || ok {
		t.Errorf("Reads %v, backup offered %v, err %v", plan.Reads(), ok, err)
	}
}
