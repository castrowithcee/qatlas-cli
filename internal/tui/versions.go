package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/castrowithcee/qatlas-cli/internal/selfupdate"
)

// The version list (command version, and the Update part of Settings) lets a person install a newer
// release, a prerelease, or an older version. It shows the choice 'qatlas update --list' shows, installs a
// newer one or a prerelease like 'qatlas update --to', and treats an older one as the checked rollback of
// that command.

// versionUpdater is implemented by an Updater that can install a chosen version. Without it the editor
// offers no version list.
type versionUpdater interface {
	// Versions lists the releases that can be chosen, newest first.
	Versions(ctx context.Context) ([]selfupdate.Entry, error)
	// InstallVersion installs a newer release or a prerelease named by tag.
	InstallVersion(ctx context.Context, tag string, progress func(done, total int64)) (selfupdate.Result, error)
	// PrepareRollback downloads and verifies an older version named by tag and checks it against the
	// configuration. Nothing is changed.
	PrepareRollback(ctx context.Context, tag string, progress func(done, total int64)) (Rollback, error)
}

// Rollback is an older version that is downloaded, verified, and checked against the configuration.
type Rollback interface {
	// Reads reports that the older version reads the current configuration, so nothing needs restoring.
	Reads() bool
	// Backup names the newest backup the older version reads, and when it was made; ok is false where there
	// is none or where Reads is true.
	Backup() (name string, at time.Time, ok bool)
	// Install installs the older version, restoring the offered backup first when restore is true.
	Install(ctx context.Context, restore bool) (selfupdate.Result, error)
}

// versionsTimeout bounds the lookup of the list, which runs on the network while the editor waits.
const versionsTimeout = 20 * time.Second

type (
	// versionsLoadedMsg carries the list asked for from screen from.
	versionsLoadedMsg struct {
		entries []selfupdate.Entry
		err     error
		from    screen
	}
	// rollbackPreparedMsg carries the older version after its download and its check against the
	// configuration.
	rollbackPreparedMsg struct {
		entry selfupdate.Entry
		plan  Rollback
		err   error
	}
)

// versionChoice is the open version list.
type versionChoice struct {
	// from is the screen the list returns to.
	from    screen
	entries map[string]selfupdate.Entry
}

// plain is the style of the running text of a question.
var plain = lipgloss.NewStyle()

// versionPending is the question about a chosen version: the installation itself, or, once the older
// version was checked, whether to restore a backup first (plan is then set).
type versionPending struct {
	entry selfupdate.Entry
	plan  Rollback
	// from is the screen the editor returns to once the list is closed.
	from screen
}

// versionsUnavailable is why the version list cannot open now, or empty where it can.
func (m *Model) versionsUnavailable() string {
	if _, ok := m.updater.(versionUpdater); !ok {
		return tr(tUpdateBuildCannot)
	}
	switch {
	case m.updating:
		return tr(tUpdateRunning)
	case m.updated != "":
		return tr(tUpdateAlreadyDone)
	case m.versionsLoading:
		return tr(tVersionsLoading)
	}
	return ""
}

// openVersions looks the list up in the background and opens it. It meets the blockers of u, because
// choosing a version ends in the same replacement.
func (m *Model) openVersions() tea.Cmd {
	m.clearMessages()
	if reason := m.versionsUnavailable(); reason != "" {
		m.status = reason
		return nil
	}
	if work := m.updateBlocker(); work != "" {
		m.status = tr(tVersionWaitAsk, work)
		return nil
	}
	updater := m.updater.(versionUpdater)
	from := m.screen
	m.versionsLoading = true
	m.status = tr(tVersionsLoading)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), versionsTimeout)
		defer cancel()
		entries, err := updater.Versions(ctx)
		return versionsLoadedMsg{entries: entries, err: err, from: from}
	}
}

// versionsLoaded opens the list. A list that arrives after the person moved on is dropped.
func (m *Model) versionsLoaded(msg versionsLoadedMsg) {
	m.versionsLoading = false
	if m.screen != msg.from {
		return
	}
	m.status = ""
	if msg.err != nil {
		m.fail = tr(tVersionsFailed, m.redactor.Error(msg.err))
		return
	}
	if len(msg.entries) == 0 {
		m.status = tr(tVersionsNone)
		return
	}
	v := &versionChoice{from: msg.from, entries: map[string]selfupdate.Entry{}}
	names := make([]string, 0, len(msg.entries))
	for _, e := range msg.entries {
		v.entries[e.Version] = e
		names = append(names, e.Version)
	}
	m.versions = v
	m.versionList.reset(names)
	m.versionList.startFilter()
	// The cursor starts on the running version, so newer and older ones are a step away either way.
	for _, e := range msg.entries {
		if e.Current {
			m.versionList.selectName(e.Version)
			break
		}
	}
	m.screen = screenVersions
}

// closeVersions leaves the list.
func (m *Model) closeVersions() {
	if m.versions != nil {
		m.screen = m.versions.from
	}
	m.versions = nil
	m.versionList.reset(nil)
}

// versionMarks are the words that tell a version apart in the list.
func versionMarks(e selfupdate.Entry) []string {
	var marks []string
	if e.Current {
		marks = append(marks, tr(tVersionMarkRunning))
	}
	if !e.Current && !e.Older {
		marks = append(marks, tr(tVersionMarkNewer))
	}
	if e.Prerelease {
		marks = append(marks, tr(tVersionMarkPrerelease))
	}
	if e.Older {
		marks = append(marks, tr(tVersionMarkOlder))
	}
	return marks
}

// versionText is what the list shows and searches of one version.
func (m *Model) versionText(tag string) string {
	if m.versions == nil {
		return tag
	}
	e, ok := m.versions.entries[tag]
	if !ok {
		return tag
	}
	return tag + "  " + strings.Join(versionMarks(e), " · ")
}

func (m *Model) updateVersions(key tea.KeyPressMsg) tea.Cmd {
	if m.versions == nil {
		m.screen = screenNav
		return nil
	}
	switch {
	case keyIs(key, actBack):
		m.closeVersions()
	case keyIs(key, actEnter):
		tag, ok := m.versionList.selected()
		if !ok {
			return nil
		}
		m.askVersion(m.versions.entries[tag])
	case keyIs(key, actUp):
		m.versionList.move(-1)
	case keyIs(key, actDown):
		m.versionList.move(1)
	case keyIs(key, actPageUp) || keyIs(key, actPageDown) || keyIs(key, actHome) || keyIs(key, actEnd):
		start, end := m.versionWindow()
		m.versionList.page(key, start, end)
	default:
		return m.versionList.updateFilter(key)
	}
	return nil
}

// askVersion opens the question about installing e over the list.
func (m *Model) askVersion(e selfupdate.Entry) {
	m.clearMessages()
	if e.Current {
		m.status = tr(tVersionIsRunning, e.Version)
		return
	}
	if work := m.updateBlocker(); work != "" {
		m.status = tr(tVersionWaitAsk, work)
		return
	}
	m.versionPending = &versionPending{entry: e, from: m.versions.from}
	m.updateFrom = screenVersions
	m.screen = screenUpdate
	// The consequences are counted once here: the view is drawn on every key.
	m.updateText = consequencesOf(selfupdate.TargetQuestion(e.Version, e.Prerelease, e.Older))
}

// consequencesOf is the part of the core's question after the version, which says what the installation does
// to running qatlas processes; the question is built for a terminal and holds the version in front of it.
func consequencesOf(question string) string {
	if _, after, ok := strings.Cut(question, "? "); ok {
		return after
	}
	return question
}

// versionQuestionView asks before the installation, or, for an older version whose configuration check
// needs a backup, before the restore.
func (m *Model) versionQuestionView() string {
	p := m.versionPending
	if p.plan != nil {
		return m.restoreQuestionView()
	}
	e := p.entry
	note := tr(tVersionNewerNote, m.runningVersion())
	switch {
	case e.Older && e.Prerelease:
		note = tr(tVersionOlderPrereleaseNote, m.runningVersion())
	case e.Older:
		note = tr(tVersionOlderNote, m.runningVersion())
	case e.Prerelease:
		note = tr(tVersionPrereleaseNote, m.runningVersion())
	}
	after := tr(tUpdateAfterManual)
	if m.restart != nil {
		after = tr(tUpdateAfterRestart)
	}
	return m.questionText(question{
		title:       tr(tVersionQuestion, e.Version),
		consequence: note,
		warn:        e.Older,
		details: []string{m.wrapped(plain, strings.TrimSpace(m.updateText)), "",
			m.wrapped(plain, after)},
		keys: "{yes} install · {no}/{back} cancel · {command} cmds · {help} help",
	})
}

// restoreQuestionView asks whether to restore the backup the older version reads.
func (m *Model) restoreQuestionView() string {
	p := m.versionPending
	name, at, _ := p.plan.Backup()
	version := p.entry.Version
	return m.questionText(question{
		title:       tr(tVersionRestoreQuestion, version),
		consequence: tr(tVersionRestoreConsequence, version),
		warn:        true,
		details: []string{m.wrapped(plain, tr(tVersionRestoreOffer, name,
			at.UTC().Format("2006-01-02 15:04 UTC"), version))},
		keys: "{yes} restore and install · {no}/{back} cancel · {command} cmds · {help} help",
	})
}

// updateVersionConfirm answers the question about a chosen version.
func (m *Model) updateVersionConfirm(key tea.KeyPressMsg) tea.Cmd {
	p := m.versionPending
	switch {
	case keyIs(key, actYes):
		if work := m.updateBlocker(); work != "" {
			m.versionPending = nil
			m.screen = p.from
			m.versions = nil
			m.versionList.reset(nil)
			m.status = tr(tVersionWaitConfirm, work)
			return nil
		}
		m.versionPending = nil
		m.screen = p.from
		if p.plan != nil {
			return m.installRollback(p.entry, p.plan, true)
		}
		m.versions = nil
		m.versionList.reset(nil)
		return m.installVersion(p.entry)
	case keyIs(key, actNo) || keyIs(key, actBack):
		m.cancelVersionAsk()
	}
	return nil
}

// cancelVersionAsk answers the question with no. Declining the installation returns to the list; declining
// the restore ends the rollback, which changed nothing.
func (m *Model) cancelVersionAsk() {
	p := m.versionPending
	m.versionPending = nil
	if p.plan != nil {
		m.versions = nil
		m.versionList.reset(nil)
		m.screen = p.from
		m.status = tr(tVersionRestoreDeclined)
		return
	}
	m.screen = screenVersions
	m.status = tr(tUpdateNotInstalled)
}

// installVersion installs e in the background: a newer release or a prerelease at once, an older version
// after it was checked against the configuration.
func (m *Model) installVersion(e selfupdate.Entry) tea.Cmd {
	updater := m.updater.(versionUpdater)
	if e.Older {
		return m.runInstall(e.Version, func(progress func(done, total int64)) tea.Msg {
			plan, err := updater.PrepareRollback(context.Background(), e.Version, progress)
			return rollbackPreparedMsg{entry: e, plan: plan, err: err}
		})
	}
	return m.runInstall(e.Version, func(progress func(done, total int64)) tea.Msg {
		result, err := updater.InstallVersion(context.Background(), e.Version, progress)
		return updateDoneMsg{result: result, err: err}
	})
}

// installRollback installs the checked older version, restoring its backup first when restore is true.
func (m *Model) installRollback(e selfupdate.Entry, plan Rollback, restore bool) tea.Cmd {
	if restore {
		name, _, _ := plan.Backup()
		m.versionRestored = name
	}
	return m.runInstall(e.Version, func(func(done, total int64)) tea.Msg {
		result, err := plan.Install(context.Background(), restore)
		return updateDoneMsg{result: result, err: err}
	})
}

// rollbackPrepared goes on once the older version was downloaded and checked: it installs at once where the
// version reads the configuration, asks before restoring a backup where it does not, and stops where it
// reads neither.
func (m *Model) rollbackPrepared(msg rollbackPreparedMsg) tea.Cmd {
	m.updating = false
	if msg.err != nil {
		return m.updateDone(updateDoneMsg{err: msg.err})
	}
	e, plan := msg.entry, msg.plan
	if plan.Reads() {
		return m.installRollback(e, plan, false)
	}
	if _, _, ok := plan.Backup(); !ok {
		m.status = ""
		m.fail = tr(tVersionNoBackup, e.Version)
		return nil
	}
	m.versionPending = &versionPending{entry: e, plan: plan, from: m.screen}
	m.updateFrom = m.screen
	m.screen = screenUpdate
	m.clearMessages()
	return nil
}

// versionRestoredNote is what a successful rollback says about the backup it restored, once.
func (m *Model) versionRestoredNote() string {
	name := m.versionRestored
	m.versionRestored = ""
	if name == "" {
		return ""
	}
	return tr(tVersionRestored, name)
}

func (m *Model) versionFrame() (string, string) {
	l := &m.versionList
	shown, total := len(l.matches), len(l.all)
	var head strings.Builder
	head.WriteString(m.boxTop(tr(tVersionsTitle, min(l.cursor+1, shown), total), ""))
	head.WriteString(m.boxSearch(l, tr(tLogPickSearch)))
	if shown == 0 {
		head.WriteString(m.boxText(styles.hint, tr(tVersionsNoMatch, l.query())))
	}
	keys := "type to search · {up}/{down} move · {enter} choose · {back} close"
	if l.query() == "" {
		keys += " · {command} cmds · {help} help"
	}
	return head.String(), m.boxBottom() + m.keyHint(keys) + m.notes()
}

// versionRow is one version of the list: its tag, and the words that mark it dimmed.
func (m *Model) versionRow(i int) string {
	_, width := m.fitIn(m.boxRoom(), "  ")
	text := truncateCells(m.versionList.text(m.versionList.matches[i]), width)
	if head, rest, ok := strings.Cut(text, "  "); ok && i != m.versionList.cursor {
		text = styles.cell.Render(head) + "  " + styles.hint.Render(rest)
	}
	return m.boxRow(m.listRowIn(m.boxRoom(), i == m.versionList.cursor, text))
}

func (m *Model) versionWindow() (int, int) {
	header, footer := m.versionFrame()
	return m.windowIn(&m.versionList, header, footer, m.versionRow)
}

func (m *Model) versionsView() string {
	if m.versions == nil {
		return ""
	}
	header, footer := m.versionFrame()
	var b strings.Builder
	b.WriteString(header)
	start, end := m.versionWindow()
	for i := start; i < end; i++ {
		b.WriteString(m.versionRow(i) + "\n")
	}
	m.hitTable(&m.versionList, screenVersions, header, start, end, m.versionRow)
	b.WriteString(footer)
	return b.String()
}
