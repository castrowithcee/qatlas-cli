package tui

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
)

// Updater checks for and installs a newer stable release of qatlas itself. The caller supplies it, and
// leaves it nil where no check may run: in a dev build, or when the person opted out.
type Updater interface {
	Check(ctx context.Context) (selfupdate.Result, error)
	Update(ctx context.Context) (selfupdate.Result, error)
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

// askUpdate opens the update question over the sidebar or the list it was asked from.
func (m *Model) askUpdate() {
	if !m.updateOffered() {
		return
	}
	m.updateFrom = m.screen
	m.screen = screenUpdate
	m.clearMessages()
}

func (m *Model) updateUpdateConfirm(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "y":
		m.screen = m.updateFrom
		return m.startUpdate()
	case "n", "esc":
		m.screen = m.updateFrom
		m.status = "Update not installed"
	case "ctrl+c":
		return m.quit()
	}
	return nil
}

// startUpdate installs the release in the background. The editor keeps working on the version it runs.
func (m *Model) startUpdate() tea.Cmd {
	m.updating = true
	m.clearMessages()
	updater := m.updater
	return func() tea.Msg {
		result, err := updater.Update(context.Background())
		return updateDoneMsg{result: result, err: err}
	}
}

// updateDone reports the outcome. The running process is not replaced in memory, so a successful update
// asks for a restart instead of pretending to be the new version.
func (m *Model) updateDone(msg updateDoneMsg) {
	m.updating = false
	var unsupported *selfupdate.UnsupportedInstallationError
	switch {
	case errors.As(msg.err, &unsupported):
		m.status = ""
		m.fail = m.redactor.Error(msg.err) + "; 'qatlas update --help' says which installations it replaces"
	case errors.Is(msg.err, selfupdate.ErrDevelopmentBuild):
		m.status = ""
		m.fail = msg.err.Error()
	case msg.err != nil:
		m.status = ""
		m.fail = "update failed: " + m.redactor.Error(msg.err)
	case msg.result.Updated:
		m.updated = msg.result.Latest
		m.fail = ""
		m.status = "Updated to " + m.updated + ". Restart qatlas tui to use it."
	default:
		// Nothing newer was there by the time of the installation.
		m.release = selfupdate.Result{}
		m.fail = ""
		m.status = "qatlas is already up to date"
	}
}

// updateBanners are the forms of the banner in the top line, longest first. Each form reads without colour;
// the short ones drop the emoji and the current version for narrow terminals.
func (m *Model) updateBanners() []string {
	switch {
	case m.updated != "":
		return []string{"Updated to " + m.updated + " · restart qatlas tui", "Updated · restart"}
	case m.updating:
		return []string{"Updating to " + m.release.Latest + " …", "Updating …"}
	case m.updateOffered():
		current, latest := m.release.Current, m.release.Latest
		return []string{
			"Update available " + current + " → " + latest + " 🎉 · u update",
			"Update available " + current + " → " + latest + " · u update",
			"Update available " + latest + " · u update",
			"Update " + latest + " · u",
		}
	}
	return nil
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

// updateView asks before the installation. The keys come right under the question, so a small terminal
// still shows them.
func (m *Model) updateView() string {
	question := "Update qatlas to " + m.release.Latest + "?"
	return m.wrapped(titleStyle, question) + "\n" + m.wrapped(hintStyle, "y update · n/esc cancel") + "\n\n" +
		m.wrapped(lipgloss.NewStyle(), "Installs the release like 'qatlas update' and verifies its checksum. "+
			"Restart qatlas tui afterwards to use it.")
}
