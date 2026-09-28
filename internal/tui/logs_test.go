package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const logPassphrase = "correct horse battery staple"

// logFixture is an encrypted vault under dir with a log key to sign entries with, the way the vault process
// or an unlocking run signs them.
type logFixture struct {
	t      *testing.T
	dir    string
	logger *invokelog.Logger
}

func newLogFixture(t *testing.T) *logFixture {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "qatlas")
	setup, _ := newVaultResolver(t, dir)
	mustNoError(t, setup.SetVault("reader", "token-id", "canary-token-id-5c1e",
		func(string) (string, error) { return logPassphrase, nil }))
	v := vault.New(dir)
	if _, err := v.Unlock(logPassphrase); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	key, err := v.LogKey()
	mustNoError(t, err)
	t.Cleanup(key.Clear)
	return &logFixture{t: t, dir: dir, logger: invokelog.New(v.Dir(), 36500).WithKey(key)}
}

func (f *logFixture) logsDir() string { return filepath.Join(f.dir, vault.DirName, "logs") }

func (f *logFixture) append(path, client, operation, connection, result string) {
	f.t.Helper()
	fields := invokelog.Fields{Path: path, Operation: operation, Connection: connection, Effect: "read",
		Result: result, Duration: 12 * time.Millisecond}
	if client != "" {
		fields.Client = &invokelog.ClientInfo{Name: client, Version: "1.0"}
	}
	mustNoError(f.t, f.logger.Append(fields))
}

// today is the day file every append of this run went to.
func (f *logFixture) today() string {
	f.t.Helper()
	entries, err := os.ReadDir(f.logsDir())
	mustNoError(f.t, err)
	latest := ""
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			latest = max(latest, strings.TrimSuffix(e.Name(), ".jsonl"))
		}
	}
	return latest
}

// edit rewrites the day file of day through change, which gets its lines.
func (f *logFixture) edit(day string, change func([][]byte) [][]byte) {
	f.t.Helper()
	path := filepath.Join(f.logsDir(), day+".jsonl")
	data, err := os.ReadFile(path)
	mustNoError(f.t, err)
	lines := change(bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")))
	mustNoError(f.t, os.WriteFile(path, append(bytes.Join(lines, []byte("\n")), '\n'), 0o600))
}

// model opens an editor over the fixture's vault, unlocked in the editor's own process or locked, at
// 120x30, and shows the Logs section on day.
func (f *logFixture) model(unlocked bool, day string) *Model {
	f.t.Helper()
	store := newTestStore(f.t, filepath.Join(f.dir, "config.yaml"))
	secrets, _ := newResolver(f.t, f.dir, nil)
	v := vault.New(f.dir)
	if unlocked {
		if _, err := v.Unlock(logPassphrase); err != nil {
			f.t.Fatalf("Unlock() = %v", err)
		}
	}
	m, err := New(store, nil, secrets.WithVault(v, nil), nil)
	mustNoError(f.t, err)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.logs.day = day
	m.screen = screenNav
	pump(f.t, m, "8")
	if m.screen != screenLogs || m.section != sectionLogs || !m.logs.loaded {
		f.t.Fatalf("8 = screen %v section %v loaded %v, want the loaded Logs screen", m.screen, m.section,
			m.logs.loaded)
	}
	return m
}

// barLine is the line of the view that holds the bar's Mode field.
func barLine(view string) string {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Mode:") {
			return line
		}
	}
	return ""
}

// rowLine is the line of the view that shows operation.
func rowLine(t *testing.T, view, operation string) string {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, " "+operation+" ") {
			return line
		}
	}
	t.Fatalf("no row shows %s:\n%s", operation, view)
	return ""
}

func TestLogsSectionShowsTheDayVerified(t *testing.T) {
	f := newLogFixture(t)
	f.append("mcp", "codex", "wiki.pages.get", "wiki", "success")
	f.append("cli", "", "wiki.pages.list", "wiki", "success")
	m := f.model(true, f.today())

	view := m.View()
	for _, want := range []string{"8 Logs", "7 Tokens", "Mode: [Day] Range All", "Date: [" + f.today() + "]",
		"status", "time", "way", "client", "tool", "connection", "result", "dur.", "codex", "MCP", "CLI",
		"12ms", "filter: way=all client=all tool=all conn=all effect=all result=all"} {
		if !strings.Contains(view, want) {
			t.Errorf("view does not show %q:\n%s", want, view)
		}
	}
	if !strings.Contains(barLine(view), "✓ verified") {
		t.Errorf("the bar does not show the day verified:\n%s", view)
	}
	for _, op := range []string{"wiki.pages.get", "wiki.pages.list"} {
		if line := rowLine(t, view, op); !strings.Contains(line, "✓ verified") {
			t.Errorf("row %s is not verified: %q", op, line)
		}
	}
	if !strings.Contains(rowLine(t, view, "wiki.pages.get"), "> ") {
		t.Errorf("the first row is not marked:\n%s", view)
	}
	assertViewFits(t, view, 120, 30)
}

// Without the log key, a line that carries a check value is unverified, never verified: the editor never
// asks for the passphrase to check it.
func TestLogsWithALockedVaultAreUnverified(t *testing.T) {
	f := newLogFixture(t)
	f.append("mcp", "codex", "wiki.pages.get", "wiki", "success")
	m := f.model(false, f.today())

	view := m.View()
	if strings.Contains(view, "✓ verified") {
		t.Errorf("a locked vault shows an entry verified:\n%s", view)
	}
	if line := rowLine(t, view, "wiki.pages.get"); !strings.Contains(line, "? unverified") {
		t.Errorf("row = %q, want it unverified", line)
	}
	if !strings.Contains(view, "the vault is locked") {
		t.Errorf("the view does not say why the check values are not checked:\n%s", view)
	}
	if m.screen != screenLogs {
		t.Errorf("screen = %v, want no passphrase prompt", m.screen)
	}
}

// A changed line, one missing from the middle, and one that no longer parses each show on their own row,
// and the bar shows the worst of them.
func TestLogsMarkChangedMissingAndUnreadableLines(t *testing.T) {
	f := newLogFixture(t)
	for _, op := range []string{"op.one", "op.two", "op.three", "op.four", "op.five"} {
		f.append("cli", "", op, "wiki", "success")
	}
	day := f.today()
	f.edit(day, func(lines [][]byte) [][]byte {
		lines[1] = bytes.Replace(lines[1], []byte(`"op.two"`), []byte(`"op.hid"`), 1)
		return append(append(lines[:3], lines[4]), []byte(`{"seq": not json`))
	})
	m := f.model(true, day)

	view := m.View()
	if !strings.Contains(barLine(view), "✗ altered") {
		t.Errorf("the bar does not show the day altered:\n%s", view)
	}
	for op, want := range map[string]string{
		"op.one": "✓ verified", "op.hid": "✗ altered", "op.three": "✓ verified", "op.five": "… gap",
		"(unreadable line)": "✗ altered",
	} {
		if line := rowLine(t, view, op); !strings.Contains(line, want) {
			t.Errorf("row %s = %q, want %s", op, line, want)
		}
	}

	press(t, m, "down", "enter")
	if m.screen != screenLogDetail {
		t.Fatalf("enter = screen %v, want the detail", m.screen)
	}
	detail := m.View()
	for _, want := range []string{"Log entry " + day, "✗ altered", "hash chain mismatch after this entry",
		"does not match its check", "op.hid", "sequence", "2", "wiki", "read", "12ms"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail does not show %q:\n%s", want, detail)
		}
	}
	press(t, m, "esc")
	press(t, m, "end", "up", "enter")
	if detail := m.View(); !strings.Contains(detail, "1 entries missing right before this one") {
		t.Errorf("the gap's detail does not explain it:\n%s", detail)
	}
	press(t, m, "esc")
	if m.screen != screenLogs {
		t.Errorf("esc from the detail = screen %v, want the Logs screen", m.screen)
	}
}

func TestLogsModeRangeAllAndAnEmptyDay(t *testing.T) {
	f := newLogFixture(t)
	f.append("cli", "", "op.old", "wiki", "success")
	today := f.today()
	mustNoError(t, os.Rename(filepath.Join(f.logsDir(), today+".jsonl"), filepath.Join(f.logsDir(), "2020-01-02.jsonl")))
	f.append("cli", "", "op.new", "wiki", "success")
	m := f.model(true, today)

	if view := m.View(); !strings.Contains(view, "op.new") || strings.Contains(view, "op.old") {
		t.Errorf("Day shows other days:\n%s", view)
	}
	press(t, m, "tab")
	if m.logs.focus != logFocusMode || !strings.Contains(m.View(), "> Mode:") {
		t.Fatalf("tab = focus %v, want the Mode field marked:\n%s", m.logs.focus, m.View())
	}
	pump(t, m, "right")
	if m.logs.mode != logModeRange || !strings.Contains(m.View(), "Mode: Day [Range] All") ||
		!strings.Contains(m.View(), "Range: [") {
		t.Fatalf("right = mode %v, want Range:\n%s", m.logs.mode, m.View())
	}
	press(t, m, "tab", "enter")
	if m.logs.dialog == nil || len(m.logs.dialog.inputs) != 2 {
		t.Fatalf("enter on the range did not open the from/to dialog")
	}
	clearField(t, m)
	typeText(t, m, "2020-01-01")
	press(t, m, "tab")
	clearField(t, m)
	typeText(t, m, "2019-12-31")
	press(t, m, "enter")
	if m.logs.dialog == nil || m.logs.dialog.err == "" {
		t.Fatalf("a range ending before it starts was taken")
	}
	clearField(t, m)
	typeText(t, m, today)
	pump(t, m, "enter")
	view := m.View()
	if m.logs.dialog != nil || !strings.Contains(view, "Range: [2020-01-01.."+today+"]") ||
		!strings.Contains(view, today+" ") || !strings.Contains(view, "op.old") || !strings.Contains(view, "op.new") {
		t.Fatalf("the range does not show both days with their dates:\n%s", view)
	}
	press(t, m, "shift+tab")
	pump(t, m, "right")
	if view := m.View(); !strings.Contains(view, "Mode: Day Range [All]") || !strings.Contains(view, "(all recorded days)") ||
		!strings.Contains(view, "op.old") {
		t.Fatalf("All does not show every day:\n%s", view)
	}

	// Back to Day, on a day without a file: the bar and the reason, nothing else.
	pump(t, m, "right")
	press(t, m, "tab", "enter")
	clearField(t, m)
	typeText(t, m, "20-1-1")
	press(t, m, "enter")
	if m.logs.dialog == nil || !strings.Contains(m.View(), "YYYY-MM-DD") {
		t.Fatalf("a malformed date was taken:\n%s", m.View())
	}
	clearField(t, m)
	typeText(t, m, "2021-05-05")
	pump(t, m, "enter")
	view = m.View()
	for _, want := range []string{"Date: [2021-05-05]", "No entries for 2021-05-05.", "✓ verified"} {
		if !strings.Contains(view, want) {
			t.Errorf("the empty day does not show %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "op.") {
		t.Errorf("the empty day shows entries:\n%s", view)
	}
}

func TestLogsFilterAndSearch(t *testing.T) {
	f := newLogFixture(t)
	f.append("mcp", "codex", "wiki.pages.get", "wiki", "success")
	f.append("mcp", "claude-code", "wiki.pages.create", "wiki", "policy-denied")
	f.append("cli", "", "chat.send", "alerts", "success")
	m := f.model(true, f.today())

	press(t, m, "f")
	if m.logs.pick == nil || !strings.Contains(m.View(), "Choose a filter") {
		t.Fatalf("f did not open the filter dialog:\n%s", m.View())
	}
	typeText(t, m, "client")
	press(t, m, "enter")
	if view := m.View(); !strings.Contains(view, "Filter by client") || !strings.Contains(view, "claude-code") {
		t.Fatalf("the client filter does not offer the clients:\n%s", view)
	}
	typeText(t, m, "codex")
	press(t, m, "enter")
	view := m.View()
	if m.logs.pick != nil || !strings.Contains(view, "client=codex") || !strings.Contains(view, "wiki.pages.get") ||
		strings.Contains(view, "wiki.pages.create") || strings.Contains(view, "chat.send") {
		t.Fatalf("the client filter does not keep codex alone:\n%s", view)
	}

	// Back to all clients, then search the text of the rows.
	press(t, m, "f")
	typeText(t, m, "client")
	press(t, m, "enter", "home", "enter")
	if m.logs.filters[logFilterClient] != "" {
		t.Fatalf("choosing all left the filter at %q", m.logs.filters[logFilterClient])
	}
	press(t, m, "/")
	typeText(t, m, "denied")
	press(t, m, "enter")
	view = m.View()
	if !strings.Contains(view, "search: denied") || !strings.Contains(view, "wiki.pages.create") ||
		strings.Contains(view, "wiki.pages.get") {
		t.Fatalf("the search does not keep the denied entry alone:\n%s", view)
	}
	press(t, m, "esc")
	if view := m.View(); !strings.Contains(view, "chat.send") || m.screen != screenLogs {
		t.Fatalf("esc did not clear the search first:\n%s", view)
	}
	press(t, m, "esc")
	if m.screen != screenNav {
		t.Errorf("esc without a search = screen %v, want the sidebar", m.screen)
	}
}

// Digits stay the section keys, v and g do nothing, and ? opens the help over the Logs screen.
func TestLogsKeys(t *testing.T) {
	f := newLogFixture(t)
	f.append("cli", "", "op.one", "wiki", "success")
	m := f.model(true, f.today())

	press(t, m, "v", "g")
	if m.screen != screenLogs || m.logs.mode != logModeDay || m.logs.pick != nil || m.logs.dialog != nil {
		t.Errorf("v/g changed the Logs screen: screen %v mode %v", m.screen, m.logs.mode)
	}
	press(t, m, "?")
	if m.screen != screenHelp {
		t.Fatalf("? = screen %v, want the help", m.screen)
	}
	press(t, m, "esc")
	if m.screen != screenLogs {
		t.Fatalf("esc from the help = screen %v, want the Logs screen", m.screen)
	}
	press(t, m, "3")
	if m.screen != screenList || m.section != sectionConnections {
		t.Errorf("3 from Logs = screen %v section %v, want Connections", m.screen, m.section)
	}
	pump(t, m, "8")
	press(t, m, "enter")
	press(t, m, "1")
	if m.screen != screenList || m.section != sectionServices {
		t.Errorf("1 from the detail = screen %v section %v, want Services", m.screen, m.section)
	}
	m.screen = screenNav
	pump(t, m, "8")
	press(t, m, "esc")
	if m.screen != screenNav || m.section != sectionLogs {
		t.Fatalf("esc = screen %v section %v", m.screen, m.section)
	}
	pump(t, m, "enter")
	if m.screen != screenLogs {
		t.Errorf("enter on the sidebar = screen %v, want the Logs screen", m.screen)
	}
}

// A log file is not trusted: terminal sequences and control characters never reach the screen, and a
// registered secret is redacted.
func TestLogsNeverPrintControlCharactersOrSecrets(t *testing.T) {
	f := newLogFixture(t)
	f.append("cli", "", "op.one", "wiki", "success")
	day := f.today()
	f.edit(day, func(lines [][]byte) [][]byte {
		lines[0] = bytes.Replace(lines[0], []byte(`"op.one"`), []byte(`"\u001b[31mred\u202e"`), 1)
		lines[0] = bytes.Replace(lines[0], []byte(`"wiki"`), []byte(`"canary-secret-value-77"`), 1)
		return lines
	})
	store := newTestStore(t, filepath.Join(f.dir, "config.yaml"))
	secrets, _ := newResolver(t, f.dir, nil)
	m, err := New(store, nil, secrets.WithVault(vault.New(f.dir), nil), nil)
	mustNoError(t, err)
	m.redactor.Add("canary-secret-value-77")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.logs.day = day
	pump(t, m, "8")
	press(t, m, "enter")
	for _, view := range []string{m.logDetailView(), func() string { m.screen = screenLogs; return m.View() }()} {
		if strings.Contains(view, "\x1b[31m") || strings.Contains(view, "\u202e") {
			t.Errorf("a control sequence from the log reached the screen: %q", view)
		}
		if strings.Contains(view, "canary-secret-value-77") || !strings.Contains(view, "[redacted]") {
			t.Errorf("a registered secret was not redacted:\n%s", view)
		}
		if !strings.Contains(view, "�[31mred") {
			t.Errorf("the changed operation is not shown made safe:\n%s", view)
		}
	}
}

// Below 80 columns the bar stands field by field and the duration is the first column to go.
func TestLogsNarrowLayout(t *testing.T) {
	f := newLogFixture(t)
	f.append("mcp", "claude-code", "github.issues.create", "github-ops", "success")
	m := f.model(true, f.today())
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})

	view := m.View()
	assertViewFits(t, view, 60, 20)
	for _, want := range []string{"Mode: [Day] Range All", "Date: [" + f.today() + "]", "✓ verified", "github.issues"} {
		if !strings.Contains(view, want) {
			t.Errorf("narrow view does not show %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "dur.") || strings.Contains(view, "12ms") {
		t.Errorf("the narrow table keeps the duration:\n%s", view)
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	if view := m.View(); strings.Contains(view, "Resize terminal") {
		t.Errorf("the smallest terminal cannot show the Logs screen:\n%s", view)
	}
}

// Without a vault, Tokens says so instead of a list and offers nothing to do.
func TestTokensSectionWithoutAVault(t *testing.T) {
	const noVault = "There is no vault configured for this run."
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	pump(t, m, "7")
	if m.screen != screenList || m.section != sectionTokens {
		t.Fatalf("7 = screen %v section %v", m.screen, m.section)
	}
	view := m.View()
	if !strings.Contains(view, noVault) || !strings.Contains(view, "7 Tokens       -") {
		t.Errorf("Tokens does not say that there is no vault:\n%s", view)
	}
	for _, action := range []string{"n new", "enter show", "x revoke", "d delete", "/ filter"} {
		if strings.Contains(view, action) {
			t.Errorf("Tokens offers %q:\n%s", action, view)
		}
	}
	press(t, m, "n")
	if m.screen != screenList || m.fail != noVault {
		t.Errorf("n on Tokens = screen %v error %q", m.screen, m.fail)
	}
}
