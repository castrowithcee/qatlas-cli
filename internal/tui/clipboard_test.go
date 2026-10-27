package tui

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// clipboardOf is the text a command hands to the terminal, and whether it is a clipboard command at all.
func clipboardOf(cmd tea.Cmd) (string, bool) {
	if cmd == nil {
		return "", false
	}
	msg := cmd()
	if v := reflect.ValueOf(msg); v.Kind() == reflect.String && reflect.TypeOf(msg).Name() == "setClipboardMsg" {
		return v.String(), true
	}
	return "", false
}

// pressCopy presses the copy key and returns what it handed to the terminal.
func pressCopy(m *Model) (string, bool) {
	_, cmd := m.Update(textKey("c"))
	return clipboardOf(cmd)
}

func noControl(t *testing.T, where, text string) {
	t.Helper()
	for _, r := range text {
		if r != '\n' && r < ' ' || r == 0x7f || r == 0x1b {
			t.Errorf("%s holds the control character %q", where, r)
			return
		}
	}
}

// Without an open area c copies the displayed name of the marked row, and says it handed it over.
func TestCopyNameOfMarkedRow(t *testing.T) {
	m := newTableModel(t, 140)
	openSectionByName(t, m, sectionConnections)
	m.list.selectName("tracker-issues")
	got, ok := pressCopy(m)
	if !ok || got != "tracker-issues" {
		t.Fatalf("c copied %q (%v), want the name of the marked connection", got, ok)
	}
	if !strings.Contains(m.status, "terminal") {
		t.Errorf("status = %q, want the hand-over to the terminal", m.status)
	}
	if !strings.Contains(withFooter(m, screenOf(m)), "c copy") {
		t.Errorf("the footer does not name c:\n%s", withFooter(m, screenOf(m)))
	}
}

// The status never claims the text was copied, in either language.
func TestCopyStatusIsAHandOverInBothLanguages(t *testing.T) {
	t.Cleanup(func() { setLanguage(uiprefs.LanguageEnglish) })
	for lang, want := range map[string]string{uiprefs.LanguageEnglish: "Handed to the terminal", uiprefs.LanguageGerman: "An das Terminal"} {
		setLanguage(lang)
		m := newTableModel(t, 140)
		m.Update(tea.WindowSizeMsg{Width: 200, Height: 40})
		openSectionByName(t, m, sectionServices)
		m.list.selectName("wiki-main")
		if _, ok := pressCopy(m); !ok {
			t.Fatalf("%s: c handed nothing over", lang)
		}
		if !strings.Contains(m.status, want) {
			t.Errorf("%s: status = %q, want %q", lang, m.status, want)
		}
		low := strings.ToLower(m.status)
		if strings.Contains(low, "copied") || strings.Contains(low, "kopiert") {
			t.Errorf("%s: status %q claims the text was copied", lang, m.status)
		}
		if foot := withFooter(m, screenOf(m)); !strings.Contains(foot, map[string]string{uiprefs.LanguageEnglish: "c copy", uiprefs.LanguageGerman: "c kopieren"}[lang]) {
			t.Errorf("%s: the footer does not name c:\n%s", lang, foot)
		}
	}
}

// On a text line c is a letter: it types and copies nothing.
func TestCopyKeyTypesOnTextLines(t *testing.T) {
	m := newTableModel(t, 140)
	openSectionByName(t, m, sectionConnections)
	press(t, m, "/")
	_, cmd := m.Update(textKey("c"))
	if text, ok := clipboardOf(cmd); ok {
		t.Fatalf("c on the filter line copied %q", text)
	}
	if m.list.query() != "c" {
		t.Errorf("the filter = %q, want c typed", m.list.query())
	}
}

// The raw tab is copied whole, also what lies below the pane, and holds no secret and no control character.
func TestCopyRawTabIsWholeAndSafe(t *testing.T) {
	m := longRawModel(t)
	view := showRaw(t, m)
	if strings.Contains(view, "octo/repo59") {
		t.Fatalf("the pane already shows the last line:\n%s", view)
	}
	got, ok := pressCopy(m)
	if !ok || !strings.Contains(got, "octo/repo59") || !strings.Contains(got, "octo/repo00") {
		t.Fatalf("the raw copy lacks lines beyond the pane: %q", got)
	}

	before := currentProfile()
	setProfile(profileNone)
	t.Cleanup(func() { setProfile(before) })
	m = rawModel(t)
	m.redactor.Add("canary-redacted-registered")
	canaries := []string{canaryUser, canaryURLPass, canaryQuery, canaryOption, canaryVault, "canary env", "canary-redacted-registered"}
	for _, s := range []section{sectionServices, sectionCredentials, sectionConnections} {
		openSectionByName(t, m, s)
		for _, name := range m.list.all {
			if isGroupRow(name) {
				continue
			}
			m.leaveDetail()
			m.screen = screenList
			m.list.selectName(name)
			if s == sectionConnections {
				conn := m.cfg.Connections[name]
				conn.Description += " canary-redacted-registered"
				m.cfg.Connections[name] = conn
			}
			showRaw(t, m)
			for tab, label := range map[detailTab]string{tabRaw: "raw", tabDetails: "details", tabOverview: "overview"} {
				m.detailTab = tab
				text, ok := pressCopy(m)
				if !ok {
					t.Fatalf("%v %q %s: c handed nothing over", s, name, label)
				}
				for _, c := range canaries {
					if strings.Contains(text, c) {
						t.Errorf("%v %q %s: the copy holds %q:\n%s", s, name, label, c, text)
					}
				}
				noControl(t, name+" "+label, text)
			}
		}
	}
}

// The explanation is copied from its overlay.
func TestCopyInfoOverlay(t *testing.T) {
	m := newTableModel(t, 100)
	openSectionByName(t, m, sectionServices)
	press(t, m, "i")
	if !m.infoOpen {
		t.Fatal("i did not open the explanation")
	}
	got, ok := pressCopy(m)
	if !ok || !strings.Contains(got, m.infoShown.title) || len(got) < len(m.infoShown.title)+10 {
		t.Fatalf("the overlay copy = %q (%v)", got, ok)
	}
	if !strings.Contains(shown(m.render()), "c copy") {
		t.Errorf("the overlay does not name c:\n%s", m.render())
	}
}

// A log entry is copied with its message, whole and without the registered secret or any control character.
func TestCopyLogDetail(t *testing.T) {
	f := newLogFixture(t)
	f.append("/a", "client", "op.one", "wiki", "boom canary-log-secret \x1b[31mred\x07 end")
	m := f.model(true, f.localToday())
	m.redactor.Add("canary-log-secret")
	m.Update(tea.WindowSizeMsg{Width: minimumWidth, Height: minimumHeight})
	press(t, m, "down", "enter")
	if m.screen != screenLogDetail {
		t.Fatalf("enter = screen %v, want the detail", m.screen)
	}
	if strings.Contains(screenOf(m), logLabel("response")+":") {
		t.Fatalf("the window already shows the last field:\n%s", screenOf(m))
	}
	got, ok := pressCopy(m)
	if !ok {
		t.Fatal("c handed nothing over")
	}
	for _, want := range []string{"op.one", "boom", logLabel("response"), "wiki"} {
		if !strings.Contains(got, want) {
			t.Errorf("the copy lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "canary-log-secret") {
		t.Errorf("the copy holds the registered secret:\n%s", got)
	}
	noControl(t, "log copy", got)
}

// The approval detail is copied with its facts.
func TestCopyApprovalDetail(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, _, _ := newApprovalFixture(t, dir, passphrase)
	m := openApprovalModel(t, store, dir)
	openEntryForm(t, m, sectionServices, "wiki")
	press(t, m, "f2")
	unlockWithAdminPassphrase(t, m, passphrase)
	openSectionByName(t, m, sectionApprovals)
	if len(m.list.all) == 0 {
		t.Fatal("no open approval")
	}
	m.list.selectName(m.list.all[0])
	press(t, m, "enter")
	if m.approvalDetail == "" {
		t.Fatalf("enter opened no detail: screen %v", m.screen)
	}
	got, ok := pressCopy(m)
	if !ok || !strings.Contains(got, m.approvalDetail) || !strings.Contains(got, "credential") {
		t.Fatalf("the approval copy = %q (%v)", got, ok)
	}
	noControl(t, "approval copy", got)
	if !strings.Contains(withFooter(m, screenOf(m)), "c copy") {
		t.Errorf("the footer does not name c:\n%s", withFooter(m, screenOf(m)))
	}
}
