package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func commandNames(list []command) []string {
	var names []string
	for _, c := range list {
		names = append(names, c.name)
	}
	return names
}

func cmdModel(t *testing.T) *Model {
	t.Helper()
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func TestColonOpensTheCommandLineAndEscClosesIt(t *testing.T) {
	m := cmdModel(t)
	seedServices(t, m, "a-one")
	for name, screens := range map[string]func(){
		"tab line": func() { m.screen = screenNav },
		"list":     func() { m.screen = screenList },
		"logs":     func() { openSectionByName(t, m, sectionLogs) },
	} {
		screens()
		from := m.screen
		press(t, m, ":")
		if m.screen != screenCommand || !strings.Contains(screenOf(m), "Command Palette") {
			t.Fatalf("%s: : opened screen %v:\n%s", name, m.screen, screenOf(m))
		}
		press(t, m, "esc")
		if m.screen != from || m.cmdline != nil {
			t.Fatalf("%s: esc = screen %v, want %v", name, m.screen, from)
		}
	}
}

func TestColonStaysTextWhereTextIsTyped(t *testing.T) {
	m := cmdModel(t)
	seedServices(t, m, "a-one")
	m.screen = screenList
	press(t, m, "/", ":")
	if m.screen != screenList || m.list.query() != ":" {
		t.Fatalf(": in the filter: screen %v query %q, want it as text", m.screen, m.list.query())
	}
	press(t, m, "esc", "esc")

	addService(t, m, "wiki", "https://wiki.example.invalid")
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	typeText(t, m, "a:b")
	if m.screen != screenForm || m.fieldValue("name") != "a:b" {
		t.Fatalf(": in a text row: screen %v name %q", m.screen, m.fieldValue("name"))
	}
}

func TestSlashStillFiltersTheList(t *testing.T) {
	m := cmdModel(t)
	seedServices(t, m, "a-one", "b-two")
	m.screen = screenList
	press(t, m, "/")
	typeText(t, m, "two")
	if !m.list.editing || m.list.query() != "two" {
		t.Fatalf("/ = editing %v query %q, want the list filter", m.list.editing, m.list.query())
	}
}

func TestCommandSuggestionsMatchPrefixWordAliasAndCase(t *testing.T) {
	m := cmdModel(t)
	list := m.commands()
	cases := []struct {
		query string
		first string
		has   string
	}{
		{"", "go overview", "quit"},
		{"QU", "quit", ""},
		{"go lo", "go logs", ""},
		{"tokens", "go tokens", ""},
		{"exit", "quit", ""},
		{"vault", "go vault", "lock vault"},
		{"cred", "go credentials", ""},
		{"guided", "setup", ""},
	}
	for _, c := range cases {
		got := commandNames(matchCommands(list, c.query))
		if len(got) == 0 || got[0] != c.first {
			t.Errorf("%q matches %v, want %q first", c.query, got, c.first)
		}
		if c.has != "" && !strings.Contains(strings.Join(got, "|"), c.has) {
			t.Errorf("%q matches %v, want %q among them", c.query, got, c.has)
		}
	}
	if got := matchCommands(list, "zzz"); len(got) != 0 {
		t.Errorf("zzz matches %v, want none", commandNames(got))
	}
}

func TestEnterRunsAreaCommandsLikeTheDigits(t *testing.T) {
	for a := area(0); a < areaCount; a++ {
		x, y := cmdModel(t), cmdModel(t)
		x.screen, y.screen = screenNav, screenNav
		pump(t, x, string(rune('1'+a)))
		pump(t, y, ":")
		typeText(t, y, "go "+strings.ToLower(a.title()))
		pump(t, y, "enter")
		if y.section != x.section || y.screen != x.screen || y.cmdline != nil {
			t.Errorf("%v: command = section %v screen %v, key = section %v screen %v",
				a.title(), y.section, y.screen, x.section, x.screen)
		}
	}
}

// The names the sections had before the areas still open them, inside their area.
func TestEnterRunsSectionAliasesLikeTheOptionBar(t *testing.T) {
	for s := section(0); s < sectionCount; s++ {
		x, y := cmdModel(t), cmdModel(t)
		openSectionByName(t, x, s)
		y.screen = screenNav
		pump(t, y, ":")
		typeText(t, y, "go "+strings.ToLower(s.title()))
		pump(t, y, "enter")
		if y.section != s || y.screen != x.screen || y.cmdline != nil {
			t.Errorf("%v: command = section %v screen %v, option bar = section %v screen %v",
				s.title(), y.section, y.screen, x.section, x.screen)
		}
	}
}

// The empty line lists the areas, not every sub-tab.
func TestEmptyCommandLineListsOnlyTheAreas(t *testing.T) {
	var got []string
	for _, c := range matchCommands(cmdModel(t).commands(), "") {
		if strings.HasPrefix(c.name, "go ") {
			got = append(got, c.name)
		}
	}
	want := "go overview|go connections|go activity|go access|go library|go settings"
	if strings.Join(got, "|") != want {
		t.Errorf("go commands = %v, want %s", got, want)
	}
}

func TestEnterRunsSetupHelpAndQuit(t *testing.T) {
	a, b := cmdModel(t), cmdModel(t)
	pump(t, a, "c")
	pump(t, b, ":")
	typeText(t, b, "setup")
	pump(t, b, "enter")
	if b.screen != a.screen || b.wizard == nil != (a.wizard == nil) {
		t.Errorf("setup: screen %v wizard %v, key: screen %v wizard %v", b.screen, b.wizard != nil, a.screen, a.wizard != nil)
	}

	m := cmdModel(t)
	pump(t, m, ":")
	typeText(t, m, "help")
	pump(t, m, "enter")
	if m.screen != screenHelp || m.helpFrom != screenNav {
		t.Errorf("help: screen %v from %v", m.screen, m.helpFrom)
	}

	m = cmdModel(t)
	pump(t, m, ":")
	typeText(t, m, "quit")
	_, cmd := m.Update(keyMsg("enter"))
	if !m.quitting || cmd == nil {
		t.Errorf("quit: quitting %v", m.quitting)
	}
}

func TestEnterRunsUpdateLikeU(t *testing.T) {
	m := startedWith(t, newerRelease())
	pump(t, m, ":")
	typeText(t, m, "update")
	pump(t, m, "enter")
	if m.screen != screenUpdate || !strings.Contains(screenOf(m), "Update qatlas to v0.5.0?") {
		t.Fatalf("update = screen %v:\n%s", m.screen, screenOf(m))
	}
}

func TestEnterRunsLockVaultLikeCtrlL(t *testing.T) {
	m, _ := newEncryptedVaultModel(t, filepath.Join(t.TempDir(), "qatlas"), "hunter2", true)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	openSectionByName(t, m, sectionServices)
	pump(t, m, ":")
	typeText(t, m, "lock vault")
	pump(t, m, "enter")
	if m.screen != screenConfirm || !m.vaultLockConfirm {
		t.Fatalf("lock vault = screen %v confirm %v, want the lock question", m.screen, m.vaultLockConfirm)
	}
}

func TestEscClosesTheCommandLineWithoutEffect(t *testing.T) {
	m := cmdModel(t)
	m.screen = screenNav
	pump(t, m, ":")
	typeText(t, m, "go logs")
	pump(t, m, "esc")
	if m.screen != screenNav || m.section != sectionServices || m.quitting {
		t.Fatalf("esc = screen %v section %v", m.screen, m.section)
	}
}

func TestUnknownAndImpossibleCommandsSayWhy(t *testing.T) {
	m := cmdModel(t)
	pump(t, m, ":")
	typeText(t, m, "frobnicate")
	pump(t, m, "enter")
	if m.screen != screenCommand || !strings.Contains(screenOf(m), `unknown command "frobnicate"`) {
		t.Fatalf("unknown command:\n%s", screenOf(m))
	}
	press(t, m, "esc")

	// No vault in this model: the command is refused with its reason and the line stays open.
	pump(t, m, ":")
	typeText(t, m, "lock vault")
	pump(t, m, "enter")
	if m.screen != screenCommand || !strings.Contains(screenOf(m), "lock vault: ") {
		t.Fatalf("impossible command:\n%s", screenOf(m))
	}
	press(t, m, "esc")

	// No update known.
	pump(t, m, ":")
	typeText(t, m, "update")
	pump(t, m, "enter")
	if m.screen != screenCommand || !strings.Contains(screenOf(m), "update: ") {
		t.Fatalf("update without a release:\n%s", screenOf(m))
	}
}

func TestSectionCommandIsRefusedOnAConfirmationAndKeepsIt(t *testing.T) {
	m := cmdModel(t)
	addService(t, m, "wiki", "https://wiki.example.invalid")
	openSectionByName(t, m, sectionServices)
	press(t, m, "d")
	if m.screen != screenConfirm {
		t.Fatalf("d = screen %v, want the confirmation", m.screen)
	}
	press(t, m, ":")
	typeText(t, m, "go logs")
	pump(t, m, "enter")
	if m.screen != screenCommand || !strings.Contains(screenOf(m), "cannot switch areas") {
		t.Fatalf("refusal:\n%s", screenOf(m))
	}
	press(t, m, "esc")
	if m.screen != screenConfirm || m.section != sectionServices {
		t.Fatalf("esc = screen %v section %v, want the confirmation unchanged", m.screen, m.section)
	}
}

func TestEveryFooterWithoutTextEntryNamesTheCommandLine(t *testing.T) {
	m := cmdModel(t)
	seedServices(t, m, "a-one")
	for _, s := range []screen{screenNav, screenList} {
		m.screen = s
		if flat := strings.Join(strings.Fields(screenOf(m)), " "); !strings.Contains(flat, ": cmds") {
			t.Errorf("screen %v footer lacks ': cmds':\n%s", s, screenOf(m))
		}
	}
}

func paletteModel(t *testing.T, w, h int) *Model {
	t.Helper()
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m.screen = screenNav
	press(t, m, ":")
	return m
}

func TestPaletteIsAnOverlayWithPlaceholderAndShortcuts(t *testing.T) {
	m := paletteModel(t, 150, 42)
	view := ansi.Strip(m.View())
	for _, want := range []string{"Command Palette", "/ search or run a command...", "Open Connections (2)", "close"} {
		if !strings.Contains(view, want) {
			t.Fatalf("palette lacks %q:\n%s", want, view)
		}
	}
	lines := strings.Split(view, "\n")
	for _, l := range lines {
		if strings.Contains(l, "Open Connections") {
			if !strings.HasSuffix(strings.TrimRight(l, " │"), "2") && !strings.Contains(l, "2 │") {
				t.Errorf("shortcut not at the right: %q", l)
			}
			if !strings.Contains(l, "> ") && !strings.Contains(l, "  ") {
				t.Errorf("row %q", l)
			}
		}
		if strings.Contains(l, "╭") && ansi.StringWidth(l) < 150 {
			continue
		}
	}
	if !strings.Contains(view, "qatlas") && len(lines) != 42 {
		t.Errorf("the screen below is gone: %d lines", len(lines))
	}
	width := 0
	for _, l := range lines {
		if strings.Contains(l, "Command Palette") {
			trimmed := strings.TrimSpace(l)
			width = ansi.StringWidth(trimmed[strings.Index(trimmed, "╭") : strings.LastIndex(trimmed, "╮")+len("╮")])
		}
	}
	if width == 0 || width > 60 {
		t.Errorf("palette width %d, want 1..60", width)
	}
	press(t, m, "c", "o", "n")
	view = ansi.Strip(m.View())
	if strings.Contains(view, "search or run a command") || !strings.Contains(view, "/ con") {
		t.Errorf("typed text not in the field:\n%s", view)
	}
}

func TestPaletteScrollKeepsTheCursorVisible(t *testing.T) {
	m := paletteModel(t, 80, 24)
	n := len(matchCommands(m.commands(), ""))
	for i := 0; i < n-1; i++ {
		press(t, m, "down")
	}
	last := matchCommands(m.commands(), "")[n-1]
	view := ansi.Strip(m.View())
	found := false
	for _, l := range strings.Split(view, "\n") {
		if strings.Contains(l, "> ") && strings.Contains(l, last.name) {
			found = true
		}
	}
	if !found {
		t.Errorf("cursor row %q not visible:\n%s", last.name, view)
	}
}

func TestPaletteClickRunsTheEntry(t *testing.T) {
	m := paletteModel(t, 150, 42)
	clickOn(t, m, "Open Connections")
	if m.screen == screenCommand || m.cmdline != nil {
		t.Fatalf("click left the palette open on screen %v", m.screen)
	}
}

func TestPaletteReadsWithoutColour(t *testing.T) {
	before := lipgloss.ColorProfile()
	prior := styles
	t.Cleanup(func() { lipgloss.SetColorProfile(before); setTheme(prior) })
	t.Setenv("NO_COLOR", "1")
	applyColorScheme("", "")
	lipgloss.SetColorProfile(termenv.Ascii)
	m := paletteModel(t, 150, 42)
	view := m.View()
	if strings.Contains(view, "\x1b[") {
		t.Errorf("colour sequence in the palette: %q", view)
	}
	if !strings.Contains(view, "> ") || !strings.Contains(view, "Command Palette") {
		t.Errorf("selection lost:\n%s", view)
	}
}

func TestPaletteStaysUsableAtTheSmallestSize(t *testing.T) {
	m := paletteModel(t, 40, 13)
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "> ") {
		t.Fatalf("no cursor at 40x13:\n%s", view)
	}
	press(t, m, "h", "e", "l", "p", "enter")
	if m.screen != screenHelp {
		t.Errorf("enter did not run help, screen %v", m.screen)
	}
}
