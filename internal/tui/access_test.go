package tui

import (
	"reflect"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

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
