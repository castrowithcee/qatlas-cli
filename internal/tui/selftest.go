package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/selftest"
)

// The selftest scenarios run from the command line (':'), one command per scenario of the selftest register.
// The editor only starts selftest.Run and shows what it reports; it measures nothing itself.

// SelfTest is what the editor needs to run the selftest. The caller supplies the version and the way to
// start a writer process, since only it knows the program's own command line. The zero value cannot run.
type SelfTest struct {
	Version string
	Worker  selftest.WorkerCommand

	// scenarios and run replace the register and selftest.Run in tests.
	scenarios func() []selftest.Scenario
	run       func(context.Context, selftest.Options) (*selftest.Report, error)
}

func (s SelfTest) list() []selftest.Scenario {
	if s.scenarios != nil {
		return s.scenarios()
	}
	return selftest.Scenarios()
}

func (s SelfTest) runner() func(context.Context, selftest.Options) (*selftest.Report, error) {
	if s.run != nil {
		return s.run
	}
	return selftest.Run
}

// selftestMsg is one event of a run: a progress line, or, with done set, the end of selftest.Run.
type selftestMsg struct {
	id     int
	line   string
	done   bool
	report *selftest.Report
	err    error
}

// selftestState is the run on screen, from its start until the person leaves its result.
type selftestState struct {
	id       int
	scenario string
	profile  string
	from     screen
	cancel   context.CancelFunc
	events   chan selftestMsg
	lines    []string
	// cancelling is true from esc until selftest.Run has returned, which is when the workers are ended and
	// the scratch directory is removed. quitWhenDone is ctrl+c during that time.
	cancelling   bool
	quitWhenDone bool
	done         bool
	report       *selftest.Report
	// failure is the redacted error of a failed run; cancelled is set when the person ended it.
	failure   string
	cancelled bool
	// saved is the file the report was written to, saveErr why that failed.
	saved, saveErr string
	offset         int
}

// selftestCommands is one register entry per scenario of the selftest register.
func (m *Model) selftestCommands() []command {
	var list []command
	for _, sc := range m.selftest.list() {
		name := sc.Name
		list = append(list, command{
			name:        "selftest " + name,
			desc:        tr(tCmdSelftest, sc.Summary),
			unavailable: func(m *Model) string { return m.selftestUnavailable() },
			run:         func(m *Model) tea.Cmd { return m.startSelftest(name) },
		})
	}
	return list
}

// selftestUnavailable is why a selftest cannot start now.
func (m *Model) selftestUnavailable() string {
	switch {
	case m.st != nil && !m.st.done:
		return tr(tSelftestRunning)
	case m.selftest.run == nil && m.selftest.Worker.Program == "":
		return tr(tSelftestNone)
	}
	if m.section == sectionSelftest && m.screen == screenForm {
		return ""
	}
	return onScreens("start a selftest", screenNav, screenList, screenSelftest)(m)
}

// startSelftest runs one scenario with the same default profile as 'qatlas selftest <name>'. The run
// reports on a channel, from which one command at a time reads the next event.
func (m *Model) startSelftest(name string) tea.Cmd {
	if reason := m.selftestUnavailable(); reason != "" {
		m.status = reason
		return nil
	}
	from := m.screen
	if m.st != nil {
		from = m.st.from
	}
	profile := selftest.Standard()
	ctx, cancel := context.WithCancel(context.Background())
	m.selftestID++
	st := &selftestState{id: m.selftestID, scenario: name, profile: profile.Name, from: from, cancel: cancel,
		events: make(chan selftestMsg, 64)}
	m.st = st
	m.screen = screenSelftest
	m.clearMessages()
	run, opts := m.selftest.runner(), selftest.Options{
		Scenario: name, Profile: profile, Version: m.selftest.Version, Worker: m.selftest.Worker,
		// A line is dropped when the editor is behind; the end of the run is never dropped.
		Progress: func(line string) {
			select {
			case st.events <- selftestMsg{id: st.id, line: line}:
			default:
			}
		},
	}
	return func() tea.Msg {
		go func() {
			report, err := run(ctx, opts)
			st.events <- selftestMsg{id: st.id, done: true, report: report, err: err}
		}()
		return <-st.events
	}
}

// nextSelftestEvent reads the next event of the run.
func nextSelftestEvent(st *selftestState) tea.Cmd {
	return func() tea.Msg { return <-st.events }
}

func (m *Model) handleSelftest(msg selftestMsg) tea.Cmd {
	st := m.st
	if st == nil || st.id != msg.id {
		return nil
	}
	if !msg.done {
		st.lines = append(st.lines, msg.line)
		if len(st.lines) > 200 {
			st.lines = st.lines[len(st.lines)-200:]
		}
		return nextSelftestEvent(st)
	}
	st.cancel()
	st.done = true
	st.report = msg.report
	switch {
	case st.cancelling && msg.err != nil:
		st.cancelled = true
		// The context error is the expected end; anything else, a scratch directory that could not be removed
		// included, is shown.
		if msg.err != nil && msg.err.Error() != context.Canceled.Error() {
			st.failure = m.redactor.Apply(msg.err.Error())
		}
		st.report = nil
	case msg.err != nil:
		st.failure = m.redactor.Apply(msg.err.Error())
		st.report = nil
	}
	if st.quitWhenDone {
		return m.quit()
	}
	return nil
}

// endSelftest is esc while a run is on: it cancels the run and waits for selftest.Run to return.
func (m *Model) endSelftest() {
	if st := m.st; st != nil && !st.done && !st.cancelling {
		st.cancelling = true
		st.cancel()
	}
}

func (m *Model) updateSelftest(key tea.KeyMsg) tea.Cmd {
	st := m.st
	if !st.done {
		if key.String() == keyOf(actBack) {
			m.endSelftest()
		}
		return nil
	}
	switch key.String() {
	case keyOf(actBack):
		m.screen = st.from
		m.st = nil
	case "down":
		st.offset++
	case "up":
		st.offset--
	case "pgdown":
		st.offset += m.selftestRoom()
	case "pgup":
		st.offset -= m.selftestRoom()
	case "home":
		st.offset = 0
	case "end":
		st.offset = 1 << 20
	case "w":
		m.saveSelftest()
	}
	return nil
}

// saveSelftest writes the report as the JSON 'qatlas selftest <name> --json' prints, into the system's
// temporary directory (not the scratch directory, which is gone), so it can be handed on as it is.
func (m *Model) saveSelftest() {
	st := m.st
	switch {
	case st.report == nil:
		return
	case st.saved != "":
		return
	}
	data, err := json.MarshalIndent(st.report, "", "  ")
	if err == nil {
		var f *os.File
		if f, err = os.CreateTemp("", "qatlas-selftest-*.json"); err == nil {
			_, err = f.Write(append(data, '\n'))
			err = errors.Join(err, f.Close())
			if err == nil {
				st.saved, st.saveErr = f.Name(), ""
			} else {
				_ = os.Remove(f.Name())
			}
		}
	}
	if err != nil {
		st.saveErr = m.redactor.Apply(err.Error())
	}
}

// selftestText is the report as the 'qatlas selftest' table shows it.
func selftestText(r *selftest.Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, tr(tSelftestRepHead)+"\n", r.Scenario, r.Profile)
	if r.Version != "" {
		fmt.Fprintf(&b, tr(tSelftestRepVersion)+"\n", r.Version)
	}
	fmt.Fprintf(&b, tr(tSelftestRepSystem)+"\n", r.GOOS, r.GOARCH, r.CPUs)
	fmt.Fprintf(&b, tr(tSelftestRepDir)+"\n", r.TempDir)
	fmt.Fprintf(&b, tr(tSelftestRepElapsed)+"\n", r.ElapsedMs)
	for i := 0; i < len(r.Measurements); {
		group := r.Measurements[i].Group
		fmt.Fprintf(&b, "\n%s\n", group)
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		for ; i < len(r.Measurements) && r.Measurements[i].Group == group; i++ {
			ms := r.Measurements[i]
			cells := make([]string, 0, len(ms.Values))
			for _, v := range ms.Values {
				cells = append(cells, fmt.Sprintf("%s=%s", v.Name, selftestNumber(v.Value)))
			}
			fmt.Fprintf(tw, "  %s\t%s\n", ms.Case, strings.Join(cells, "  "))
		}
		_ = tw.Flush()
	}
	return strings.TrimRight(b.String(), "\n")
}

func selftestNumber(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%.2f", v)
}

// selftestBody is the text under the title: the progress while the run goes on, the outcome after it. Each
// line is drawn for the panel p.
func (m *Model) selftestBody(p dialogPanel) []string {
	st := m.st
	wrap := func(style lipgloss.Style, text string) []string { return p.textLines(style, text) }
	switch {
	case !st.done && st.cancelling:
		return wrap(styles.warning, tr(tSelftestCancelling))
	case !st.done:
		body := wrap(lipgloss.NewStyle(), tr(tSelftestRunningBody, st.profile))
		body = append(body, p.side(""))
		for _, l := range st.lines {
			body = append(body, wrap(styles.hint, l)...)
		}
		return body
	}
	var body []string
	switch {
	case st.cancelled:
		body = wrap(styles.warning, tr(tSelftestCancelled))
		if st.failure != "" {
			body = append(body, wrap(styles.fail, tr(tNoteError, st.failure))...)
		}
	case st.failure != "":
		body = wrap(styles.fail, tr(tNoteError, st.failure))
	default:
		body = m.selftestReport(p, selftestText(st.report))
	}
	switch {
	case st.saved != "":
		body = append(body, p.side(""))
		body = append(body, wrap(styles.ok, tr(tSelftestSaved, st.saved))...)
	case st.saveErr != "":
		body = append(body, p.side(""))
		body = append(body, wrap(styles.fail, tr(tSelftestSaveError, st.saveErr))...)
	}
	return body
}

// selftestReport draws the text of a report: its headings in bold, the facts above the first group dimmed,
// and the measurements as they are.
func (m *Model) selftestReport(p dialogPanel, text string) []string {
	var body []string
	grouped := false
	for i, line := range strings.Split(text, "\n") {
		style := styles.cell
		switch {
		case line == "":
			grouped = true
		case strings.HasPrefix(line, " "):
		case i == 0 || grouped:
			style = styles.title
		default:
			style = styles.hint
		}
		body = append(body, p.textLines(style, line)...)
	}
	return body
}

// selftestState is the state of the run as a mark and its word.
func (m *Model) selftestState() (statusKind, string) {
	st := m.st
	switch {
	case !st.done:
		return statusDim, tr(tStatusRunning)
	case st.cancelled:
		return statusWarn, tr(tStatusCancelled)
	case st.failure != "":
		return statusFail, tr(tStatusFailed)
	}
	return statusOK, tr(tStatusDone)
}

func (m *Model) selftestKeys() string {
	st := m.st
	switch {
	case !st.done && st.cancelling:
		return "ctrl+c quit when done · {command} cmds · {help} help"
	case !st.done:
		return "{back} cancel · {command} cmds · {help} help"
	case st.report != nil:
		return "up/down scroll · w save as file · {back} close · {command} cmds · {help} help"
	}
	return "{back} close · {command} cmds · {help} help"
}

// selftestFrame is the title above the body and the keys below it, and the lines of body that fit between.
func (m *Model) selftestFrame(note string) (head, foot string, room int) {
	p := m.dialogPanelOf()
	title := tr(tSelftestTitle, m.st.scenario)
	kind, word := m.selftestState()
	if p.framed {
		head = p.top(title, note) + p.result(kind, word) + p.blank()
	} else {
		head = m.wrapped(styles.title, title) + "  " + statusMark(kind, word) + "\n\n"
	}
	foot = p.bottom() + m.keyHint(m.selftestKeys())
	return head, foot, max(m.height-strings.Count(head, "\n")-strings.Count(foot, "\n")-1, 1)
}

func (m *Model) selftestRoom() int {
	_, _, room := m.selftestFrame("")
	return room
}

func (m *Model) selftestView() string {
	st := m.st
	p := m.dialogPanelOf()
	head, foot, room := m.selftestFrame("")
	body := m.selftestBody(p)
	start := 0
	if !st.done {
		// Progress shows its newest lines.
		start = max(len(body)-room, 0)
	} else {
		st.offset = max(min(st.offset, len(body)-room), 0)
		start = st.offset
	}
	shown := body[start:min(start+room, len(body))]
	if p.framed && len(body) > room {
		head, _, _ = m.selftestFrame(fmt.Sprintf("%d-%d/%d", start+1, start+len(shown), len(body)))
	}
	return head + strings.Join(shown, "\n") + "\n" + foot
}
