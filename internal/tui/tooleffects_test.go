package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// effectSplit returns, for one effect of the GitHub picker, its tools that a collective tick reaches and its
// listed only ones.
func effectSplit(t *testing.T, m *Model, effect config.Effect) (open, listed []string) {
	t.Helper()
	metadata, _ := m.cfg.ProviderMetadata("github")
	for _, tool := range metadata.Tools {
		switch {
		case tool.Effect != effect:
		case tool.RequiresToolAllowList:
			listed = append(listed, tool.ID)
		default:
			open = append(open, tool.ID)
		}
	}
	return open, listed
}

// effectWithListedTool finds an effect that has both kinds of tools.
func effectWithListedTool(t *testing.T, m *Model) config.Effect {
	t.Helper()
	for _, effect := range config.Effects() {
		if open, listed := effectSplit(t, m, effect); len(open) > 0 && len(listed) > 0 {
			return effect
		}
	}
	t.Fatal("no effect with a listed only tool")
	return ""
}

func effectRow(effect config.Effect) string { return effectRowPrefix + string(effect) }

func TestEffectRowsStandAboveTheGroupsInTheOrderOfTheEffects(t *testing.T) {
	m, _, _ := groupedPicker(t, 100)
	rows := m.picker.matches[:len(m.pickerEffects.effects)]
	for i, effect := range m.pickerEffects.effects {
		if rows[i] != effectRow(effect) {
			t.Fatalf("row %d = %q, want the %s row", i, rows[i], effect)
		}
	}
	if m.picker.matches[len(rows)] != allGroupsRow {
		t.Fatalf("the all-groups row does not follow the effect rows: %q", m.picker.matches[len(rows)])
	}
	if view := screenOf(m); !strings.Contains(view, "[ ] read 0/") {
		t.Fatalf("no effect row with its count:\n%s", view)
	}
}

func TestSpaceOnAnEffectRowTicksAndUnticksItsTools(t *testing.T) {
	m, _, _ := groupedPicker(t, 100)
	effect := config.EffectCreate
	open, listed := effectSplit(t, m, effect)
	cursorTo(t, m, effectRow(effect))
	press(t, m, " ")
	for _, tool := range open {
		if !m.pickerMarks[tool] {
			t.Fatalf("%s is not ticked", tool)
		}
	}
	for _, tool := range listed {
		if m.pickerMarks[tool] {
			t.Fatalf("listed only %s was ticked collectively", tool)
		}
	}
	want := fmt.Sprintf("%d/%d", len(open), len(open)+len(listed))
	if got := m.pickerEffects.rowText(effectRow(effect), m.pickerMarks); got != fmt.Sprintf("[%s] create %s", map[bool]string{true: "-", false: "x"}[len(listed) > 0], want) {
		t.Fatalf("row = %q", got)
	}
	press(t, m, " ")
	if len(m.pickerMarks) != 0 {
		t.Fatalf("a second space left %v", m.pickerMarks)
	}
}

func TestAListedOnlyToolIsTickedOneByOneAndUntickedCollectively(t *testing.T) {
	m, _, _ := groupedPicker(t, 100)
	effect := effectWithListedTool(t, m)
	open, listed := effectSplit(t, m, effect)
	row := effectRow(effect)
	cursorTo(t, m, row)
	press(t, m, " ")
	if got := m.pickerEffects.rowText(row, m.pickerMarks); !strings.HasPrefix(got, "[-] "+string(effect)) {
		t.Fatalf("row = %q, want a partial tick", got)
	}
	// Ticking one by one completes the effect; the listed only tool alone does not, but space then unticks all.
	for _, tool := range listed {
		m.pickerMarks[tool] = true
	}
	if got := m.pickerEffects.rowText(row, m.pickerMarks); !strings.HasPrefix(got, "[x] ") {
		t.Fatalf("row = %q, want all ticked", got)
	}
	delete(m.pickerMarks, open[0])
	press(t, m, " ") // an open tool is missing: ticks it, the listed ones stay
	if !m.pickerMarks[open[0]] || !m.pickerMarks[listed[0]] {
		t.Fatalf("ticks = %v", m.pickerMarks)
	}
	press(t, m, " ") // every open tool ticked: unticks all, listed ones included
	if len(m.pickerMarks) != 0 {
		t.Fatalf("ticks = %v, want none", m.pickerMarks)
	}
}

func TestOneTickMakesAnEffectRowPartial(t *testing.T) {
	m, _, _ := groupedPicker(t, 100)
	open, _ := effectSplit(t, m, config.EffectRead)
	m.pickerMarks[open[0]] = true
	if got := m.pickerEffects.rowText(effectRow(config.EffectRead), m.pickerMarks); !strings.HasPrefix(got, "[-] read 1/") {
		t.Fatalf("row = %q", got)
	}
	if view := screenOf(m); !strings.Contains(view, "[-] read 1/") {
		t.Fatalf("the screen lacks the partial row:\n%s", view)
	}
}

func TestASearchNarrowsAnEffectRowToTheMatches(t *testing.T) {
	m, _, _ := groupedPicker(t, 100)
	open, listed := effectSplit(t, m, config.EffectRead)
	typeText(t, m, "github.issues.get")
	cursorTo(t, m, effectRow(config.EffectRead))
	press(t, m, " ")
	if len(m.pickerMarks) != 1 || !m.pickerMarks["github.issues.get"] {
		t.Fatalf("ticks = %v, want only the match", m.pickerMarks)
	}
	if got := m.pickerEffects.rowText(effectRow(config.EffectRead), m.pickerMarks); !strings.HasPrefix(got, "[-] read 1/"+fmt.Sprint(len(open)+len(listed))) {
		t.Fatalf("the count must stay over all tools of the effect: %q", got)
	}
}

func TestEscDropsEffectTicks(t *testing.T) {
	m, _, _ := groupedPicker(t, 100)
	cursorTo(t, m, effectRow(config.EffectRead))
	press(t, m, " ", "esc")
	if m.screen != screenLeave {
		t.Fatalf("esc did not ask: screen %v", m.screen)
	}
	press(t, m, "d")
	if got := m.fieldValue(toolListLabel); got != "" || m.screen != screenForm {
		t.Fatalf("the ticks survived: %q on screen %v", got, m.screen)
	}
}

func TestTheToolsRowSummarisesCountAndEffects(t *testing.T) {
	m, path, reg := groupedPicker(t, 100)
	open, _ := effectSplit(t, m, config.EffectRead)
	cursorTo(t, m, effectRow(config.EffectRead))
	press(t, m, " ")
	cursorTo(t, m, effectRow(config.EffectCreate))
	press(t, m, " ", "enter")
	_, total := m.pickerGroups.count(nil)
	openCreate, _ := effectSplit(t, m, config.EffectCreate)
	want := fmt.Sprintf("%d of %d · read, create", len(open)+len(openCreate), total)
	if view := screenOf(m); !strings.Contains(view, want) {
		t.Fatalf("form lacks %q:\n%s", want, view)
	}
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	if got := len(savedTools(t, path, reg, "repo")); got != len(open)+len(openCreate) {
		t.Fatalf("saved %d tools", got)
	}
	openEntryForm(t, m, sectionConnections, "repo")
	if view := screenOf(m); !strings.Contains(view, want) {
		t.Fatalf("reopened form lacks %q:\n%s", want, view)
	}
}
