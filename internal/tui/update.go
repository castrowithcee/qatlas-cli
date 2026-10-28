package tui

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
)

// Updater checks for and installs a newer stable release of qatlas itself. The caller supplies it, and
// leaves it nil where no check may run: in a dev build, or when the person opted out.
type Updater interface {
	Check(ctx context.Context) (selfupdate.Result, error)
	Update(ctx context.Context) (selfupdate.Result, error)
}

// updateNoter is implemented by an Updater that has more to say after an installation, such as what became
// of the vault process it locked.
type updateNoter interface{ Note() string }

// progressUpdater is implemented by an Updater that can report the download of the release archive.
type progressUpdater interface {
	UpdateProgress(ctx context.Context, progress func(done, total int64)) (selfupdate.Result, error)
}

// updateCheckTimeout bounds the check at start. It runs in the background, and a slow network must not
// leave a request open for the whole session.
const updateCheckTimeout = 5 * time.Second

// updateCheckedMsg carries the answer of the check at start, updateDoneMsg the outcome of an installation.
type (
	updateCheckedMsg struct {
		result selfupdate.Result
		err    error
	}
	updateDoneMsg struct {
		result selfupdate.Result
		err    error
	}
	// updateProgressMsg carries the percentage of the archive downloaded so far; next waits for what the
	// installation reports after it.
	updateProgressMsg struct {
		percent int
		next    tea.Cmd
	}
)

// checkUpdate asks once, in the background, whether a newer stable release exists.
func (m *Model) checkUpdate() tea.Cmd {
	if m.updater == nil {
		return nil
	}
	updater := m.updater
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
		defer cancel()
		result, err := updater.Check(ctx)
		return updateCheckedMsg{result: result, err: err}
	}
}

// updateChecked keeps a newer release for the banner. Everything else, a failed check included, stays
// silent: no network is not something the editor has to report.
func (m *Model) updateChecked(msg updateCheckedMsg) {
	if msg.err != nil || !msg.result.UpdateAvailable || msg.result.Latest == "" {
		return
	}
	m.release = msg.result
}

// updateOffered reports whether u opens the update question now.
func (m *Model) updateOffered() bool {
	return m.updater != nil && m.release.UpdateAvailable && !m.updating && m.updated == ""
}

// askUpdate opens the update question over the tab line or the list it was asked from.
func (m *Model) askUpdate() {
	if !m.updateOffered() {
		return
	}
	if work := m.updateBlocker(); work != "" {
		m.clearMessages()
		m.status = tr(tUpdateWaitAsk, work)
		return
	}
	m.updateFrom = m.screen
	m.screen = screenUpdate
	// Counted once here: the view is drawn on every key.
	m.updateText = selfupdate.Consequences()
	m.clearMessages()
}

// updateBlocker names the store or vault work in flight, if any. An installation locks the vault process
// and replaces the program, which such work must not meet halfway: a vault process still starting would be
// refused by the new program, and a restart would not happen.
func (m *Model) updateBlocker() string {
	switch {
	case m.busy != "":
		return m.busy
	case m.vaultBusy:
		return tr(tUpdateBlockerVault)
	}
	return ""
}

func (m *Model) updateUpdateConfirm(key tea.KeyPressMsg) tea.Cmd {
	if m.versionPending != nil {
		return m.updateVersionConfirm(key)
	}
	switch {
	case keyIs(key, actYes):
		m.screen = m.updateFrom
		if work := m.updateBlocker(); work != "" {
			m.status = tr(tUpdateWaitConfirm, work)
			return nil
		}
		return m.startUpdate()
	case keyIs(key, actNo) || keyIs(key, actBack):
		m.screen = m.updateFrom
		m.status = tr(tUpdateNotInstalled)
	}
	return nil
}

// startUpdate installs the release in the background. The editor keeps working on the version it runs.
func (m *Model) startUpdate() tea.Cmd {
	updater := m.updater
	progressing, ok := updater.(progressUpdater)
	return m.runInstall("", func(progress func(done, total int64)) tea.Msg {
		var result selfupdate.Result
		var err error
		if ok {
			result, err = progressing.UpdateProgress(context.Background(), progress)
		} else {
			result, err = updater.Update(context.Background())
		}
		return updateDoneMsg{result: result, err: err}
	})
}

// runInstall runs work in the background while the banner shows an installation. target names the version
// being installed, or is empty for the newest stable release. work reports the download of the archive to
// progress and returns the message that ends it.
func (m *Model) runInstall(target string, work func(progress func(done, total int64)) tea.Msg) tea.Cmd {
	m.updating = true
	m.updateTarget = target
	m.updateUnlocked = m.vaultOpen()
	m.clearMessages()
	m.updatePercent = -1
	// The work runs on its own and reports through events; each message the editor gets waits for the next
	// one, so the banner follows the download without a timer.
	events := make(chan tea.Msg, 1)
	go func() {
		last := -1
		events <- work(func(done, total int64) {
			if total <= 0 {
				return
			}
			if percent := int(done * 100 / total); percent != last {
				last = percent
				select {
				case events <- updateProgressMsg{percent: percent}:
				default:
					// The editor has not taken the previous value yet; the next one supersedes it.
				}
			}
		})
	}()
	var wait tea.Cmd
	wait = func() tea.Msg {
		msg := <-events
		if progress, ok := msg.(updateProgressMsg); ok {
			progress.next = wait
			return progress
		}
		return msg
	}
	return wait
}

// vaultOpen reports whether the vault is encrypted and unlocked, in this process or in a vault process.
func (m *Model) vaultOpen() bool {
	v := m.secrets.Vault()
	if v == nil {
		return false
	}
	state, err := v.State()
	return err == nil && m.vaultDisplayUnlocked(state)
}

// updateDone reports the outcome. The running process is not replaced in memory: where the editor can
// restart, it replaces itself with the new program, asking first when that would drop unsaved input;
// elsewhere a successful update asks for a restart instead of pretending to be the new version.
func (m *Model) updateDone(msg updateDoneMsg) tea.Cmd {
	m.updating = false
	restored := m.versionRestoredNote()
	var unsupported *selfupdate.UnsupportedInstallationError
	switch {
	case errors.As(msg.err, &unsupported):
		m.status = ""
		m.fail = tr(tUpdateUnsupported, m.redactor.Error(msg.err))
	case errors.Is(msg.err, selfupdate.ErrDevelopmentBuild):
		m.status = ""
		m.fail = msg.err.Error()
	case msg.err != nil:
		m.status = ""
		m.fail = tr(tUpdateFailed, m.redactor.Error(msg.err))
	case msg.result.Updated:
		m.updated = msg.result.Latest
		m.fail = ""
		m.status = m.restartHint()
		if restored != "" {
			m.status += " " + restored
		}
		return m.restartAfterUpdate(msg.result.Current, msg.result.Latest)
	default:
		// Nothing newer was there by the time of the installation.
		m.release = selfupdate.Result{}
		m.fail = ""
		m.status = tr(tUpdateUpToDate)
	}
	return nil
}

// updateBanners are the forms of the banner in the top line, longest first. Each form reads without colour;
// the short ones drop the emoji and the current version for narrow terminals.
func (m *Model) updateBanners() []string {
	switch {
	case m.updated != "":
		return []string{tr(tBannerUpdated, m.updated), tr(tBannerUpdatedShort)}
	case m.updating && m.updatePercent >= 0:
		return []string{tr(tBannerUpdatingPercent, m.installing(), m.updatePercent),
			tr(tBannerUpdatingPercentShort, m.updatePercent)}
	case m.updating:
		return []string{tr(tBannerUpdating, m.installing()), tr(tBannerUpdatingShort)}
	case m.updateOffered():
		current, latest := m.release.Current, m.release.Latest
		return []string{
			tr(tBannerAvailableFull, current, latest),
			tr(tBannerAvailable, current, latest),
			tr(tBannerAvailableLatest, latest),
			tr(tBannerAvailableShort, latest),
		}
	}
	return nil
}

// installing is the version the running installation puts in place.
func (m *Model) installing() string {
	if m.updateTarget != "" {
		return m.updateTarget
	}
	return m.release.Latest
}

// updateBanner is the longest banner that fits room cells, or nothing.
func (m *Model) updateBanner(room int) string {
	for _, banner := range m.updateBanners() {
		if lipgloss.Width(banner) <= room {
			return banner
		}
	}
	return ""
}

// updateView asks before the installation.
func (m *Model) updateView() string {
	if m.versionPending != nil {
		return m.versionQuestionView()
	}
	after := tr(tUpdateAfterManual)
	if m.restart != nil {
		after = tr(tUpdateAfterRestart)
	}
	// The closing advice stands on a line of its own: a sentence about other running processes, present on one
	// machine and missing on another, would otherwise move where it wraps.
	text, advice := strings.TrimSpace(m.updateText), ""
	if i := strings.LastIndex(text, ". "); i >= 0 {
		text, advice = text[:i+1], strings.TrimSpace(text[i+2:])
	}
	details := []string{m.wrapped(lipgloss.NewStyle(), text)}
	if advice != "" {
		details = append(details, m.wrapped(lipgloss.NewStyle(), advice))
	}
	details = append(details, "", m.wrapped(lipgloss.NewStyle(), after))
	return m.questionText(question{
		title:       tr(tUpdateQuestion, m.release.Latest),
		consequence: tr(tUpdateConsequence),
		details:     details,
		keys:        "y update · n/{back} cancel · {command} cmds · {help} help",
	})
}
