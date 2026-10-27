package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/uiprefs"
)

// withNoColor makes the rest of the test draw as a terminal with NO_COLOR does.
func withNoColor(t *testing.T) {
	t.Helper()
	restoreRenderer(t)
	t.Setenv("NO_COLOR", "1")
	setProfile(profileNone)
	applyTheme(uiprefs.ThemeTerminal)
}

// The detail screens of a token and of an open approval keep their state as a mark with its word, and every
// line in the terminal, at 40x16, the smallest size the screens were served at before, too; without colour nothing carries a colour sequence.
func TestAccessDetailsKeepTheirStatusWordsAtEverySize(t *testing.T) {
	for _, noColor := range []bool{false, true} {
		restoreRenderer(t)
		setProfile(profileTrueColor)
		t.Setenv("NO_COLOR", "")
		useDarkColours()
		if noColor {
			withNoColor(t)
		}
		store, dir, v := newTokenFixture(t)
		past := time.Now().Add(-time.Hour)
		fixtureToken(t, v, "ci-valid", nil, "personal")
		fixtureToken(t, v, "ci-expired", &past, "personal")
		m := openApprovalModel(t, store, dir)
		m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		openTokensUnlocked(t, m)
		for _, size := range []struct{ width, height int }{{120, 40}, {80, 24}, {40, 16}} {
			m.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
			for name, word := range map[string]string{"ci-valid": "valid", "ci-expired": "expired"} {
				m.tokenDetail, m.screen = name, screenConfirm
				view := m.render()
				assertViewFits(t, view, size.width, size.height)
				plain := ansi.Strip(view)
				if !strings.Contains(plain, "Token "+name) || !strings.Contains(plain, "● "+word) {
					t.Errorf("%dx%d %s noColor=%v: the detail lacks its name or its status word:\n%s", size.width, size.height, name, noColor, plain)
				}
				if noColor && plain != view {
					t.Errorf("%dx%d %s: NO_COLOR still emits escape sequences", size.width, size.height, name)
				}
			}
		}
	}
}

// Changing the access mode swaps the effects row for the tools row and back; the row of the other mode is
// not shown, so it cannot be focused.
func TestAccessModeShowsOnlyItsOwnRow(t *testing.T) {
	m, _ := toolsModel(t, wikiRegistry(t), map[string]config.Connection{"wiki": {Service: "wiki", Credential: "reader"}})
	openEntryForm(t, m, sectionConnections, "wiki")
	if m.fieldValue(accessLabel) != accessEffects || m.visibleField(effectsLabel) == nil || m.visibleField(toolListLabel) != nil {
		t.Fatalf("by effect shows effects %v and tools %v", m.visibleField(effectsLabel), m.visibleField(toolListLabel))
	}
	selectAccess(t, m, accessTools)
	if m.visibleField(effectsLabel) != nil || m.visibleField(toolListLabel) == nil {
		t.Fatal("selected tools does not swap the effects row for the tools row")
	}
	selectAccess(t, m, accessEffects)
	if m.visibleField(effectsLabel) == nil || m.visibleField(toolListLabel) != nil {
		t.Fatal("by effect does not bring the effects row back")
	}
}

// Each mode stores its own field alone, whatever the row of the other mode holds.
func TestAccessModeSavesOnlyItsOwnField(t *testing.T) {
	reg := wikiRegistry(t)
	m, path := toolsModel(t, reg, map[string]config.Connection{"wiki": {Service: "wiki", Credential: "reader"}})
	openEntryForm(t, m, sectionConnections, "wiki")
	m.field(effectsLabel).selected = map[string]bool{"read": true, "create": true}
	m.field(toolListLabel).selected = map[string]bool{"bookstack.pages.list": true}

	selectAccess(t, m, accessEffects)
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	got := savedConnection(t, path, reg, "wiki")
	if got.Tools != nil || !reflect.DeepEqual(got.Effects, []config.Effect{config.EffectRead, config.EffectCreate}) {
		t.Fatalf("by effect saved %+v, want the effects alone", got)
	}

	openEntryForm(t, m, sectionConnections, "wiki")
	m.field(toolListLabel).selected = map[string]bool{"bookstack.pages.list": true}
	selectAccess(t, m, accessTools)
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	got = savedConnection(t, path, reg, "wiki")
	if got.Effects != nil || !reflect.DeepEqual(got.Tools, []string{"bookstack.pages.list"}) {
		t.Fatalf("selected tools saved %+v, want the tools alone", got)
	}
}

// A problem about both fields, or about the hidden row of the other mode, stands under the access row.
func TestAccessProblemsStandUnderTheAccessRow(t *testing.T) {
	m, _ := toolsModel(t, wikiRegistry(t), map[string]config.Connection{"wiki": {Service: "wiki", Credential: "reader"}})
	openEntryForm(t, m, sectionConnections, "wiki")
	for _, p := range []config.Problem{
		{Section: config.SectionConnections, Name: "wiki", Field: "effects", Code: config.CodeEffectsAndTools},
		{Section: config.SectionConnections, Name: "wiki", Field: "effects", Code: config.CodePermissionsAndEffects},
		{Section: config.SectionConnections, Name: "wiki", Field: "effects", Code: config.CodePermissionsRenamed},
	} {
		if got := m.problemRow(p, "wiki"); got != accessLabel {
			t.Errorf("%s stands under %q, want %q", p.Code, got, accessLabel)
		}
	}
	unknown := config.Problem{Section: config.SectionConnections, Name: "wiki", Field: "effects", Code: config.CodeUnknownEffect}
	if got := m.problemRow(unknown, "wiki"); got != effectsLabel {
		t.Errorf("an effect problem stands under %q, want the effects row", got)
	}
	selectAccess(t, m, accessTools)
	if got := m.problemRow(unknown, "wiki"); got != accessLabel {
		t.Errorf("an effect problem with the effects row hidden stands under %q, want the access row", got)
	}
}
