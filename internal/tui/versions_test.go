package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/i18n"
	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
)

// fakeRollback is an older version that was checked against the configuration.
type fakeRollback struct {
	reads   bool
	backup  string
	at      time.Time
	err     error
	version string
	// installs records the restore flag of each installation.
	installs []bool
}

func (r *fakeRollback) Reads() bool { return r.reads }

func (r *fakeRollback) Backup() (string, time.Time, bool) {
	return r.backup, r.at, !r.reads && r.backup != ""
}

func (r *fakeRollback) Install(_ context.Context, restore bool) (selfupdate.Result, error) {
	r.installs = append(r.installs, restore)
	if r.err != nil {
		return selfupdate.Result{}, r.err
	}
	return selfupdate.Result{Current: "v0.4.0", Latest: r.version, Updated: true}, nil
}

// versionFake answers the version list and the installation of a chosen version.
type versionFake struct {
	*fakeUpdater
	entries    []selfupdate.Entry
	listErr    error
	installed  []string
	prepared   []string
	rollback   *fakeRollback
	prepareErr error
	progress   bool
}

func (f *versionFake) Versions(ctx context.Context) ([]selfupdate.Entry, error) {
	if _, ok := ctx.Deadline(); !ok {
		return nil, errors.New("the lookup runs without a deadline")
	}
	return f.entries, f.listErr
}

func (f *versionFake) InstallVersion(_ context.Context, tag string, progress func(done, total int64)) (selfupdate.Result, error) {
	if f.progress {
		progress(50, 100)
	}
	f.installed = append(f.installed, tag)
	return selfupdate.Result{Current: "v0.4.0", Latest: tag, Updated: true}, nil
}

func (f *versionFake) PrepareRollback(_ context.Context, tag string, _ func(done, total int64)) (Rollback, error) {
	f.prepared = append(f.prepared, tag)
	if f.prepareErr != nil {
		return nil, f.prepareErr
	}
	f.rollback.version = tag
	return f.rollback, nil
}

func choosable() *versionFake {
	return &versionFake{
		fakeUpdater: newerRelease(),
		entries: []selfupdate.Entry{
			{Version: "v0.6.0-rc.1", Prerelease: true},
			{Version: "v0.5.0"},
			{Version: "v0.4.0", Current: true},
			{Version: "v0.3.1", Older: true},
			{Version: "v0.3.0", Older: true},
		},
		rollback: &fakeRollback{reads: true},
	}
}

// boxText is the text of a screen without the frame of a box and the line breaks of its wrapping.
func boxText(view string) string { return flatText(strings.ReplaceAll(view, "│", " ")) }

// startedVersions builds an editor on v0.4.0 that can choose versions.
func startedVersions(t *testing.T, fake *versionFake) *Model {
	t.Helper()
	m := startedWith(t, fake)
	m.version = "v0.4.0"
	return m
}

// openVersionList runs the command version from the tab line.
func openVersionList(t *testing.T, m *Model) {
	t.Helper()
	pump(t, m, ":")
	typeText(t, m, "version")
	pump(t, m, "enter")
	if m.screen != screenVersions {
		t.Fatalf("the command version did not open the list: screen %v status %q fail %q", m.screen, m.status, m.fail)
	}
}

func TestTheVersionCommandListsTheChoosableVersions(t *testing.T) {
	m := startedVersions(t, choosable())
	openVersionList(t, m)
	view := boxText(screenOf(m))
	for _, want := range []string{"Choose a version", "v0.6.0-rc.1 newer · prerelease", "v0.5.0 newer", "v0.4.0 running",
		"v0.3.1 older", "v0.3.0 older"} {
		if !strings.Contains(view, want) {
			t.Errorf("the list does not say %q:\n%s", want, screenOf(m))
		}
	}
	if tag, _ := m.versionList.selected(); tag != "v0.4.0" {
		t.Errorf("the cursor stands on %q, want the running version", tag)
	}

	// The list searches like the others, by tag and by mark.
	typeText(t, m, "older")
	if got := strings.Join(m.versionList.matches, ","); got != "v0.3.1,v0.3.0" {
		t.Errorf("searching older matches %q", got)
	}
	pump(t, m, "esc")
	if m.screen != screenNav || m.versions != nil {
		t.Errorf("esc did not close the list: screen %v", m.screen)
	}
}

func TestTheUpdatePageOffersTheVersionList(t *testing.T) {
	m := startedVersions(t, choosable())
	openSectionByName(t, m, sectionUpdate)
	if view := m.render(); !strings.Contains(view, "choose version") {
		t.Fatalf("the Update page has no version row:\n%s", view)
	}
	pump(t, m, "down", "enter")
	if m.screen != screenVersions {
		t.Fatalf("the row did not open the list: screen %v status %q", m.screen, m.status)
	}
	pump(t, m, "esc")
	if m.screen != screenForm {
		t.Errorf("esc returned to screen %v, want the Update page", m.screen)
	}
}

func TestChoosingAPrereleaseAsksWithTheDirectionAndInstalls(t *testing.T) {
	fake := choosable()
	fake.progress = true
	m := startedVersions(t, fake)
	openVersionList(t, m)
	typeText(t, m, "rc")
	pump(t, m, "enter")
	if m.screen != screenUpdate {
		t.Fatalf("enter did not ask: screen %v", m.screen)
	}
	view := boxText(screenOf(m))
	for _, want := range []string{"Install qatlas v0.6.0-rc.1?", "prerelease, newer than the running version v0.4.0",
		"y install n/esc cancel"} {
		if !strings.Contains(view, want) {
			t.Errorf("the question does not say %q:\n%s", want, screenOf(m))
		}
	}
	if strings.Contains(view, "fixes made since") {
		t.Errorf("a newer version is said to miss fixes:\n%s", view)
	}

	// n returns to the list and installs nothing.
	pump(t, m, "n")
	if m.screen != screenVersions || len(fake.installed) != 0 {
		t.Fatalf("n left screen %v with installations %v", m.screen, fake.installed)
	}

	pump(t, m, "enter")
	_, cmd := m.Update(keyMsg("y"))
	if !m.updating || m.screen != screenNav {
		t.Fatalf("y did not start the installation in the background: screen %v updating %v", m.screen, m.updating)
	}
	if !strings.Contains(strings.Split(m.render(), "\n")[0], "Updating to v0.6.0-rc.1 …") {
		t.Errorf("the banner does not name the chosen version:\n%s", m.render())
	}
	deliver(m, cmd)
	if got := strings.Join(fake.installed, ","); got != "v0.6.0-rc.1" || m.updated != "v0.6.0-rc.1" || fake.updates != 0 {
		t.Errorf("installed %q updated %q (u installed %d)", got, m.updated, fake.updates)
	}
	if !strings.Contains(m.render(), "Updated to v0.6.0-rc.1") {
		t.Errorf("the finished installation is not shown:\n%s", m.render())
	}
}

func TestChoosingAnOlderVersionNamesTheLossAndTheVault(t *testing.T) {
	fake := choosable()
	m := startedVersions(t, fake)
	openVersionList(t, m)
	typeText(t, m, "v0.3.1")
	pump(t, m, "enter")
	view := boxText(screenOf(m))
	for _, want := range []string{"Install qatlas v0.3.1?", "Older than the running version v0.4.0",
		"the fixes made since are missing", "The vault is locked first", "reads your configuration"} {
		if !strings.Contains(view, want) {
			t.Errorf("the question does not say %q:\n%s", want, screenOf(m))
		}
	}
	pump(t, m, "y")
	if m.screen != screenNav {
		t.Fatalf("y left screen %v", m.screen)
	}
	deliver(m, nil)
}

// A rollback to a version that reads the configuration installs after the one question.
func TestARollbackThatReadsTheConfigurationInstallsAfterOneQuestion(t *testing.T) {
	fake := choosable()
	m := startedVersions(t, fake)
	openVersionList(t, m)
	typeText(t, m, "v0.3.1")
	pump(t, m, "enter")
	_, cmd := m.Update(keyMsg("y"))
	deliver(m, cmd)
	if got := strings.Join(fake.prepared, ","); got != "v0.3.1" {
		t.Fatalf("prepared %q", got)
	}
	if len(fake.rollback.installs) != 1 || fake.rollback.installs[0] || m.updated != "v0.3.1" {
		t.Errorf("installs %v updated %q, want one installation without a restore", fake.rollback.installs, m.updated)
	}
	if m.screen != screenNav || m.updating {
		t.Errorf("screen %v updating %v", m.screen, m.updating)
	}
}

func rollbackWithBackup(fake *versionFake) {
	fake.rollback = &fakeRollback{backup: "config-20260102T030405Z.yaml", at: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
}

func chooseOlder(t *testing.T, m *Model) {
	t.Helper()
	openVersionList(t, m)
	typeText(t, m, "v0.3.1")
	pump(t, m, "enter")
	_, cmd := m.Update(keyMsg("y"))
	deliver(m, cmd)
}

func TestARollbackThatNeedsABackupAsksBeforeRestoring(t *testing.T) {
	fake := choosable()
	rollbackWithBackup(fake)
	m := startedVersions(t, fake)
	chooseOlder(t, m)
	if m.screen != screenUpdate || m.versionPending == nil || m.versionPending.plan == nil {
		t.Fatalf("the check did not ask about the backup: screen %v", m.screen)
	}
	view := boxText(screenOf(m))
	for _, want := range []string{"Restore a backup of the configuration for v0.3.1?",
		"v0.3.1 does not read your current configuration", "config-20260102T030405Z.yaml from 2026-01-02 03:04 UTC",
		"saves the current configuration as a new backup first",
		"Changes made since then are missing in v0.3.1 but stay in that backup", "y restore and install n/esc cancel"} {
		if !strings.Contains(view, want) {
			t.Errorf("the question does not say %q:\n%s", want, screenOf(m))
		}
	}
	if len(fake.rollback.installs) != 0 {
		t.Fatalf("the program was installed before the answer: %v", fake.rollback.installs)
	}

	// n and esc end the rollback without a change.
	for _, no := range []string{"n", "esc"} {
		m := startedVersions(t, fake)
		chooseOlder(t, m)
		pump(t, m, no)
		if m.screen != screenNav || m.updating || m.updated != "" || len(fake.rollback.installs) != 0 ||
			!strings.Contains(m.status, "nothing was changed") {
			t.Errorf("%s: screen %v updating %v installs %v status %q", no, m.screen, m.updating,
				fake.rollback.installs, m.status)
		}
	}

	// y restores and installs.
	m = startedVersions(t, fake)
	chooseOlder(t, m)
	_, cmd := m.Update(keyMsg("y"))
	deliver(m, cmd)
	if len(fake.rollback.installs) != 1 || !fake.rollback.installs[0] || m.updated != "v0.3.1" {
		t.Fatalf("installs %v updated %q, want one installation with the restore", fake.rollback.installs, m.updated)
	}
	if !strings.Contains(m.status, "restored from the backup config-20260102T030405Z.yaml") {
		t.Errorf("the status does not name the restored backup: %q", m.status)
	}
}

func TestARollbackWithoutAReadableBackupChangesNothing(t *testing.T) {
	fake := choosable()
	fake.rollback = &fakeRollback{}
	m := startedVersions(t, fake)
	chooseOlder(t, m)
	if m.screen == screenUpdate || m.updating || m.updated != "" || len(fake.rollback.installs) != 0 {
		t.Fatalf("screen %v updating %v installs %v", m.screen, m.updating, fake.rollback.installs)
	}
	if want := "v0.3.1 reads neither your configuration nor any of its backups; nothing was changed."; !strings.Contains(boxText(screenOf(m)), want) {
		t.Errorf("the failure does not say %q:\n%s", want, screenOf(m))
	}
}

func TestRollbackFailuresAreReportedWithoutSecrets(t *testing.T) {
	fake := choosable()
	fake.prepareErr = errors.New("could not check v0.3.1: token s3cr3t-value rejected")
	m := startedVersions(t, fake)
	m.redactor.Add("s3cr3t-value")
	chooseOlder(t, m)
	view := screenOf(m)
	if !strings.Contains(view, "update failed: could not check v0.3.1") || strings.Contains(view, "s3cr3t-value") ||
		m.updating || m.updated != "" {
		t.Errorf("updating %v updated %q\n%s", m.updating, m.updated, view)
	}
}

func TestChoosingAVersionMeetsTheBlockersOfU(t *testing.T) {
	for name, busy := range map[string]func(*Model){
		"vault work": func(m *Model) { m.vaultBusy = true },
		"store work": func(m *Model) { m.busy = "saving the configuration" },
	} {
		t.Run(name, func(t *testing.T) {
			fake := choosable()
			m := startedVersions(t, fake)
			busy(m)
			pump(t, m, ":")
			typeText(t, m, "version")
			pump(t, m, "enter")
			if m.screen == screenVersions || !strings.Contains(m.status, "choose a version again") {
				t.Fatalf("the list opened during work or gave no reason: screen %v status %q", m.screen, m.status)
			}

			// Work that starts while the question is open stops the installation at y.
			m.vaultBusy, m.busy = false, ""
			openVersionList(t, m)
			pump(t, m, "up", "enter")
			busy(m)
			_, cmd := m.Update(keyMsg("y"))
			if cmd != nil || m.updating || len(fake.installed) != 0 || !strings.Contains(m.status, "choose it again") {
				t.Errorf("y installed during work: updating %v installed %v status %q", m.updating, fake.installed, m.status)
			}
		})
	}
}

func TestTheRunningVersionIsNotInstalledAgain(t *testing.T) {
	fake := choosable()
	m := startedVersions(t, fake)
	openVersionList(t, m)
	pump(t, m, "enter")
	if m.screen != screenVersions || !strings.Contains(m.status, "v0.4.0 is the running version") {
		t.Errorf("screen %v status %q", m.screen, m.status)
	}
}

func TestTheVersionListReportsWhatItCannotDo(t *testing.T) {
	// A failed lookup says so without a list.
	fake := choosable()
	fake.listErr = errors.New("dial tcp: no route to host")
	m := startedVersions(t, fake)
	pump(t, m, ":")
	typeText(t, m, "version")
	pump(t, m, "enter")
	if m.screen == screenVersions || !strings.Contains(boxText(screenOf(m)), "could not list the versions: dial tcp") {
		t.Errorf("screen %v:\n%s", m.screen, screenOf(m))
	}

	// An updater that cannot choose versions, and none at all, leave the command unavailable.
	for name, updater := range map[string]Updater{"plain updater": newerRelease(), "no updater": nil} {
		m := startedWith(t, updater)
		pump(t, m, ":")
		typeText(t, m, "version")
		pump(t, m, "enter")
		if m.screen != screenCommand || !strings.Contains(m.cmdline.msg, "cannot update itself") {
			t.Errorf("%s: screen %v message %q", name, m.screen, m.cmdline.msg)
		}
	}
}

func TestUStillInstallsTheNewestStableRelease(t *testing.T) {
	fake := choosable()
	m := startedVersions(t, fake)
	pump(t, m, "U")
	if m.screen != screenUpdate || !strings.Contains(screenOf(m), "Update qatlas to v0.5.0?") {
		t.Fatalf("u did not ask the update question: screen %v", m.screen)
	}
	pump(t, m, "y")
	if fake.updates != 1 || len(fake.installed) != 0 {
		t.Errorf("u installed %d releases and %v versions", fake.updates, fake.installed)
	}
}

func TestTheVersionQuestionIsGerman(t *testing.T) {
	useLanguage(t, i18n.German)
	fake := choosable()
	m := startedVersions(t, fake)
	pump(t, m, ":")
	typeText(t, m, "version")
	pump(t, m, "enter")
	if m.screen != screenVersions {
		t.Fatalf("screen %v status %q", m.screen, m.status)
	}
	view := boxText(screenOf(m))
	for _, want := range []string{"Version wählen", "v0.3.1 älter", "v0.6.0-rc.1 neuer · Vorabversion", "v0.4.0 läuft gerade"} {
		if !strings.Contains(view, want) {
			t.Errorf("the list does not say %q:\n%s", want, screenOf(m))
		}
	}
	typeText(t, m, "v0.3.1")
	pump(t, m, "enter")
	view = boxText(screenOf(m))
	for _, want := range []string{"qatlas v0.3.1 installieren?", "Fehlerbehebungen fehlen", "Tresor wird zuerst gesperrt"} {
		if !strings.Contains(view, want) {
			t.Errorf("the question does not say %q:\n%s", want, screenOf(m))
		}
	}
}

func TestTheVersionListFitsASmallTerminal(t *testing.T) {
	m := startedVersions(t, choosable())
	for _, size := range [][2]int{{100, 28}, {60, 20}, {40, minimumHeight}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.screen = screenNav
		openVersionList(t, m)
		assertViewFits(t, m.render(), size[0], size[1])
		pump(t, m, "enter", "n")
		assertViewFits(t, m.render(), size[0], size[1])
		pump(t, m, "esc")
	}
}
