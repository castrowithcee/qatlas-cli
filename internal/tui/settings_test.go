package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// lockedVaultModel is an editor over an encrypted vault that is locked, with preferences stored at the
// returned path.
func lockedVaultModel(t *testing.T) (*Model, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "qatlas")
	mustNoError(t, newTestStore(t, filepath.Join(dir, "config.yaml")).Save(newTestConfig(t)))
	m, _ := newEncryptedVaultModel(t, dir, "hunter2", false)
	path := uiprefs.Path(dir)
	m.prefs = uiprefs.Default()
	m.savePrefs = func(p uiprefs.Prefs) error { return uiprefs.Save(path, p) }
	return m, path
}

func TestGeneralSavesWithoutAdminSession(t *testing.T) {
	restoreRenderer(t)
	t.Setenv("NO_COLOR", "")
	m, path := lockedVaultModel(t)
	openSectionByName(t, m, sectionGeneral)
	if m.screen != screenForm || m.section != sectionGeneral {
		t.Fatalf("General = screen %v section %v, want its form", m.screen, m.section)
	}
	labels := []string{m.fields[0].label, m.fields[1].label, m.fields[2].label, m.fields[3].label}
	if strings.Join(labels, ",") != "color scheme,background,start view,icons" {
		t.Fatalf("rows = %v", labels)
	}
	press(t, m, "right")        // color scheme: light
	press(t, m, "tab", "right") // background: qatlas
	press(t, m, "tab", "right")
	press(t, m, "tab", "right") // icons: nerdfont
	pump(t, m, "f2")
	if m.screen == screenAdminAuth || m.adminAuth != nil {
		t.Fatalf("saving the preferences asked for the admin session (screen %v)", m.screen)
	}
	if m.fail != "" || !strings.HasPrefix(m.status, "Saved") {
		t.Fatalf("status %q fail %q, want it saved", m.status, m.fail)
	}
	got, warnings := uiprefs.Load(path)
	want := uiprefs.Prefs{Theme: uiprefs.ThemeLight, Background: uiprefs.BackgroundQatlas, Start: "connections", Icons: uiprefs.IconsNerdFont,
		Language: uiprefs.LanguageEnglish, Keys: uiprefs.DefaultKeys()}
	if got != want || len(warnings) != 0 {
		t.Fatalf("stored = %+v %v, want %+v", got, warnings, want)
	}
	if m.prefs != want {
		t.Errorf("model prefs = %+v, want %+v", m.prefs, want)
	}
	if styles.active.GetForeground() != buildTheme(false, false, false).active.GetForeground() {
		t.Error("the colour scheme did not change at once")
	}
	if !styles.filled {
		t.Error("the background did not change at once")
	}
	if m.formDirty() {
		t.Error("the saved form still counts as changed")
	}
}

func TestGeneralWithBackgroundFieldFitsSmallTerminal(t *testing.T) {
	restoreRenderer(t)
	m, _ := lockedVaultModel(t)
	openSectionByName(t, m, sectionGeneral)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 14})
	press(t, m, "tab") // background
	view := m.render()
	lines := strings.Split(view, "\n")
	if len(lines) > 14 || m.tooSmall {
		t.Fatalf("%d lines at height 14, too small %v\n%s", len(lines), m.tooSmall, view)
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w > 40 {
			t.Errorf("line %d is %d cells wide at width 40", i, w)
		}
	}
	if !strings.Contains(view, "background") {
		t.Errorf("the background field is not on screen:\n%s", view)
	}
}

func TestGeneralSaveFailureIsReportedNotFatal(t *testing.T) {
	m, _ := lockedVaultModel(t)
	m.savePrefs = func(uiprefs.Prefs) error { return errors.New("disk full") }
	openSectionByName(t, m, sectionGeneral)
	press(t, m, "right")
	pump(t, m, "f2")
	if !strings.Contains(m.fail, "disk full") || m.screen != screenForm {
		t.Errorf("fail %q screen %v, want the error shown on the form", m.fail, m.screen)
	}
	if m.prefs != uiprefs.Default() {
		t.Errorf("prefs = %+v after a failed save", m.prefs)
	}
}

func TestGeneralLeaveQuestionSavesWithoutAdminSession(t *testing.T) {
	m, path := lockedVaultModel(t)
	openSectionByName(t, m, sectionGeneral)
	press(t, m, "right", "right") // dark
	pump(t, m, "esc")
	if m.screen != screenLeave {
		t.Fatalf("esc on a changed form = screen %v, want the leave question", m.screen)
	}
	pump(t, m, "f2")
	if m.screen != screenNav || m.screen == screenAdminAuth {
		t.Fatalf("after saving from the leave question: screen %v, want the tab line", m.screen)
	}
	if got, _ := uiprefs.Load(path); got.Theme != uiprefs.ThemeDark {
		t.Errorf("stored theme = %q, want dark", got.Theme)
	}
}

func TestGeneralHasNoFreeText(t *testing.T) {
	m, _ := lockedVaultModel(t)
	openSectionByName(t, m, sectionGeneral)
	for _, f := range m.fields {
		if f.kind != fieldChoice && f.kind != fieldKey {
			t.Errorf("row %q is neither a choice nor a key", f.label)
		}
	}
}

func TestAdvancedActionsStillNeedTheAdminSession(t *testing.T) {
	for _, tc := range []struct {
		section section
		action  string
	}{
		{sectionTransfer, pageActionExport},
		{sectionTransfer, pageActionImport},
		{sectionVault, vaultActionPassphrase},
	} {
		m, _ := lockedVaultModel(t)
		openSectionByName(t, m, tc.section)
		found := false
		for i, f := range m.fields {
			if f.kind == fieldVaultAction && f.action == tc.action {
				m.focus, found = i, true
				m.applyFocus()
			}
		}
		if !found {
			t.Fatalf("%v has no %s row", tc.section.title(), tc.action)
		}
		pump(t, m, "enter")
		want := screenAdminAuth
		if tc.section == sectionVault {
			// A vault action asks for the vault's own passphrase in its masked prompt, unchanged.
			want = screenVaultOffer
		}
		if m.screen != want {
			t.Errorf("%s from %s = screen %v, want screen %v", tc.action, tc.section.title(), m.screen, want)
		}
	}
}

func TestUpdatePageSaysWhyItCannotUpdate(t *testing.T) {
	m, _ := lockedVaultModel(t)
	openSectionByName(t, m, sectionUpdate)
	pump(t, m, "enter")
	if m.screen != screenForm || !strings.Contains(m.status, "cannot update itself") {
		t.Errorf("screen %v status %q, want the reason on the page", m.screen, m.status)
	}
}

func TestSettingsSubTabsInOrderWithAdvancedBar(t *testing.T) {
	m, _ := lockedVaultModel(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	want := []section{sectionGeneral, sectionVault, sectionDefaults, sectionTransfer, sectionUpdate, sectionSelftest}
	if got := areaSections[areaSettings]; len(got) != len(want) {
		t.Fatalf("Settings sections = %v, want %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("Settings sections = %v, want %v", got, want)
			}
		}
	}
	m.screen = screenNav
	pump(t, m, "6")
	if m.section != sectionGeneral {
		t.Fatalf("digit 6 opened %v, want General first", m.section.title())
	}
	m.screen = screenNav
	if line := m.subNavLine(); !strings.Contains(strings.Join(strings.Fields(line), " "), "[General] | Vault Defaults Transfer Update Self-test") {
		t.Errorf("option bar = %q", line)
	}
	m.screen, m.navSub = screenNav, true
	for _, s := range want[1:] {
		pump(t, m, "right")
		if m.section != s {
			t.Fatalf("right moved to %v, want %v", m.section.title(), s.title())
		}
	}
	pump(t, m, "right")
	if m.section != sectionGeneral {
		t.Errorf("right after the last page = %v, want it to wrap to General", m.section.title())
	}
	// At 40 columns the bar falls back to the active page alone and stays within the width.
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 14})
	if line := m.subNavLine(); lipgloss.Width(line) > 40 || !strings.Contains(line, "[General]") {
		t.Errorf("narrow option bar = %q", line)
	}
}

func TestIconsKeepTheNameVisible(t *testing.T) {
	m, _ := lockedVaultModel(t)
	for _, width := range []int{40, 60, 100} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 28})
		m.prefs.Icons = uiprefs.IconsNone
		plain := m.navLine()
		if strings.Contains(plain, areaIcons[areaOverview]) {
			t.Errorf("width %d: icons shown although the preference is none", width)
		}
		m.prefs.Icons = uiprefs.IconsNerdFont
		for _, s := range []section{sectionOverview, sectionConnections, sectionSettings()} {
			m.section = s
			line := m.navLine()
			a := s.area()
			if !strings.Contains(line, a.title()) || !strings.Contains(line, areaIcons[a]) {
				t.Errorf("width %d, area %s: tab line lacks icon or name: %q", width, a.title(), line)
			}
			if lipgloss.Width(line) > width {
				t.Errorf("width %d: tab line is %d cells wide", width, lipgloss.Width(line))
			}
		}
	}
}

func sectionSettings() section { return areaSections[areaSettings][0] }

func TestStartViewFromPreferencesOpensThatArea(t *testing.T) {
	m, _ := lockedVaultModel(t)
	m.prefs.Start = "connections"
	a, ok := areaByName(m.prefs.Start)
	if !ok {
		t.Fatal("connections is not an area")
	}
	m.start = a
	m.Init()
	if m.section != sectionConnections {
		t.Errorf("section = %v, want connections", m.section.title())
	}
}

// The state row of the vault page is a status mark with its word; the word stays without colour, and the page
// fits every size.
func TestVaultStateOfTheSettingsIsAStatusMark(t *testing.T) {
	for _, noColor := range []bool{false, true} {
		restoreRenderer(t)
		t.Setenv("NO_COLOR", "")
		applyColorScheme("dark", "")
		if noColor {
			withNoColor(t)
		}
		m, _, _ := newModel(t)
		openVaultSection(t, m)
		want := "● " + m.vaultStateText()
		for _, size := range []struct {
			width, height int
			shown         bool
		}{{120, 40, true}, {80, 24, true}, {40, minimumHeight, false}} {
			m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			view := m.render()
			assertViewFits(t, view, size.width, size.height)
			plain := ansi.Strip(view)
			if size.shown && !strings.Contains(plain, want) {
				t.Errorf("%dx%d noColor=%v: the vault state is no status mark %q:\n%s", size.width, size.height, noColor, want, plain)
			}
			if noColor && plain != view {
				t.Errorf("%dx%d: NO_COLOR still emits escape sequences", size.width, size.height)
			}
		}
	}
}

// The long explanation of the icons preference and of its nerdfont value show the glyphs and where to get a font.
func TestIconsExplanationShowsTestCharactersAndFontHint(t *testing.T) {
	eachLanguage(t, func(t *testing.T) {
		m, _, _ := newModel(t)
		f := choiceField(iconsLabel, []string{uiprefs.IconsNone, uiprefs.IconsNerdFont}, uiprefs.IconsNone)
		for name, got := range map[string]string{
			"detail": f.withDetail(iconsDetail()).detail,
			"picker": m.pickerChoiceSubject(f, uiprefs.IconsNerdFont).long,
		} {
			for _, icon := range areaIcons {
				if !strings.Contains(got, icon) {
					t.Errorf("%s lacks the test character %q: %q", name, icon, got)
				}
			}
			if !strings.Contains(got, "https://www.nerdfonts.com") {
				t.Errorf("%s lacks the font URL: %q", name, got)
			}
		}
	})
}
