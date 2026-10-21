package tui

import (
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
		"sidebar": func() { m.screen = screenNav },
		"list":    func() { m.screen = screenList },
		"logs":    func() { openSectionByName(t, m, sectionLogs) },
	} {
		screens()
		from := m.screen
		press(t, m, ":")
		if m.screen != screenCommand || !strings.Contains(screenOf(m), "Commands") {
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
		{"", "go services", "quit"},
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

func TestEnterRunsSectionCommandsLikeTheDigits(t *testing.T) {
	for s := section(0); s < sectionCount; s++ {
		a, b := cmdModel(t), cmdModel(t)
		a.screen, b.screen = screenNav, screenNav
		pump(t, a, string(rune('1'+s)))
		pump(t, b, ":")
		typeText(t, b, "go "+strings.ToLower(s.title()))
		pump(t, b, "enter")
		if b.section != a.section || b.screen != a.screen || b.cmdline != nil {
			t.Errorf("%v: command = section %v screen %v, key = section %v screen %v",
				s, b.section, b.screen, a.section, a.screen)
		}
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
	if m.screen != screenCommand || !strings.Contains(screenOf(m), "cannot switch sections") {
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
