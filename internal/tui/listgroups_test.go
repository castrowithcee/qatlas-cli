package tui

import (
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// groupModel is the table fixture plus one credential no connection uses and that names no provider.
func groupModel(t *testing.T) *Model {
	t.Helper()
	m := newTableModel(t, 120)
	m.cfg.Credentials["loose"] = config.Credential{Type: config.CredentialTypeEnv, Values: map[string]string{"token": "X"}}
	return m
}

func shownNames(m *Model) []string { return append([]string(nil), m.list.matches...) }

func TestListsAreGroupedByProvider(t *testing.T) {
	m := groupModel(t)
	for _, c := range []struct {
		section section
		want    []string
	}{
		{sectionServices, []string{groupRow("bookstack"), "wiki-main", groupRow("tracker"), "tracker-main"}},
		{sectionConnections, []string{groupRow("bookstack"), "wiki-docs", groupRow("tracker"), "tracker-issues", "tracker-triage"}},
		{sectionCredentials, []string{groupRow("bookstack"), "wiki-reader", groupRow("tracker"), "tracker-bot", groupRow(""), "loose"}},
	} {
		openSectionByName(t, m, c.section)
		if got := shownNames(m); strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s shows %q, want %q", c.section.title(), got, c.want)
		}
		if name, ok := m.selected(); !ok || isGroupRow(name) {
			t.Errorf("%s opens on %q, want the first entry", c.section.title(), name)
		}
	}
	view := screenOf(m)
	for _, want := range []string{"▾ ■ bookstack (1)", "▾ ■ tracker (1)", "▾ ■ no provider (1)"} {
		if !strings.Contains(view, want) {
			t.Errorf("the credential list lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "PROVIDER") {
		t.Errorf("the provider column is still shown:\n%s", view)
	}
}

func TestGroupsFoldAndUnfold(t *testing.T) {
	m := groupModel(t)
	openSectionByName(t, m, sectionConnections)
	press(t, m, "up") // the group row of bookstack
	if name, _ := m.list.selected(); name != groupRow("bookstack") {
		t.Fatalf("cursor on %q, want the bookstack group row", name)
	}
	press(t, m, "left")
	if got := shownNames(m); strings.Join(got, ",") != groupRow("bookstack")+","+groupRow("tracker")+",tracker-issues,tracker-triage" {
		t.Fatalf("after left: %q", got)
	}
	if view := screenOf(m); !strings.Contains(view, "▸ ■ bookstack (1)") || strings.Contains(view, "wiki-docs  ") {
		t.Errorf("a folded group must read as folded and hide its entries:\n%s", view)
	}
	press(t, m, "right")
	if len(m.list.matches) != 5 {
		t.Errorf("after right: %q", m.list.matches)
	}
	press(t, m, "enter")
	if len(m.list.matches) != 4 || m.screen != screenList {
		t.Errorf("enter on a group row folds it and opens no form: %q, screen %v", m.list.matches, m.screen)
	}
	press(t, m, "enter")
	if len(m.list.matches) != 5 {
		t.Errorf("enter on a folded group row unfolds it: %q", m.list.matches)
	}
	// Left on an entry folds its group; the cursor follows to the group row.
	press(t, m, "down", "left")
	if name, _ := m.list.selected(); name != groupRow("bookstack") || len(m.list.matches) != 4 {
		t.Errorf("left on an entry: cursor %q, shown %q", name, m.list.matches)
	}
	// The fold survives leaving and entering the section.
	openSectionByName(t, m, sectionServices)
	openSectionByName(t, m, sectionConnections)
	if len(m.list.matches) != 4 {
		t.Errorf("the fold is lost: %q", m.list.matches)
	}
}

func TestActionsIgnoreGroupRows(t *testing.T) {
	m := groupModel(t)
	openSectionByName(t, m, sectionConnections)
	press(t, m, "up")
	if _, ok := m.selected(); ok {
		t.Fatal("a group row is selected as an entry")
	}
	for _, key := range []string{"d", "r", "p", "t"} {
		_, cmd := m.Update(keyMsg(key))
		if m.screen != screenList || cmd != nil || m.editing != "" || m.rename != nil || m.testing {
			t.Errorf("%s on a group row acted: screen %v, editing %q", key, m.screen, m.editing)
		}
	}
	// Enter on a group row folds it and opens no form.
	press(t, m, "enter")
	if m.screen != screenList || m.editing != "" {
		t.Errorf("enter on a group row opened screen %v", m.screen)
	}
}

func TestFilterOpensGroupsWithHits(t *testing.T) {
	m := groupModel(t)
	openSectionByName(t, m, sectionConnections)
	press(t, m, "up", "left") // fold bookstack
	press(t, m, "/")
	typeText(t, m, "wiki")
	if got := strings.Join(m.list.matches, ","); got != groupRow("bookstack")+",wiki-docs" {
		t.Errorf("filter shows %q, want the folded group open with its hit", got)
	}
	if view := screenOf(m); !strings.Contains(view, "▾ ■ bookstack (1)") || !strings.Contains(view, "1/1 (3 total)") {
		t.Errorf("filtered view:\n%s", view)
	}
	// The provider name is searchable although it is no column.
	press(t, m, "esc")
	press(t, m, "/")
	typeText(t, m, "tracker")
	if len(m.list.matches) != 3 {
		t.Errorf("filter on the provider shows %q", m.list.matches)
	}
	// Clearing the filter brings the fold state back.
	press(t, m, "esc")
	if got := strings.Join(m.list.matches, ","); got != groupRow("bookstack")+","+groupRow("tracker")+",tracker-issues,tracker-triage" {
		t.Errorf("after clearing: %q", got)
	}
}

func TestPositionCountsEntriesOnly(t *testing.T) {
	m := groupModel(t)
	openSectionByName(t, m, sectionConnections)
	if view := screenOf(m); !strings.Contains(view, " 1/3 ") {
		t.Errorf("first entry:\n%s", view)
	}
	press(t, m, "down", "down") // group row of tracker, then tracker-issues
	if view := screenOf(m); !strings.Contains(view, " 2/3 ") {
		t.Errorf("second entry:\n%s", view)
	}
	press(t, m, "up")
	if view := screenOf(m); !strings.Contains(view, " 1/3 ") {
		t.Errorf("on a group row the position is that of the entry above:\n%s", view)
	}
	press(t, m, "left") // fold tracker: total stays 3
	if view := screenOf(m); !strings.Contains(view, " 1/3 ") {
		t.Errorf("folded:\n%s", view)
	}
}

func TestSelectionFollowsTheNameAfterASave(t *testing.T) {
	m := groupModel(t)
	openSectionByName(t, m, sectionConnections)
	press(t, m, "down", "down", "down") // tracker-triage
	if name, _ := m.selected(); name != "tracker-triage" {
		t.Fatalf("selected %q", name)
	}
	// A rename moves the entry to another place of its group; the selection follows the name.
	m.cfg.Connections["a-triage"] = m.cfg.Connections["tracker-triage"]
	delete(m.cfg.Connections, "tracker-triage")
	m.returnToList("a-triage")
	if name, _ := m.selected(); name != "a-triage" {
		t.Errorf("selected %q after the rename", name)
	}
	// A saved entry in a folded group unfolds it.
	press(t, m, "left")
	m.returnToList("a-triage")
	if name, ok := m.selected(); !ok || name != "a-triage" {
		t.Errorf("selected %q (%v) after saving into a folded group", name, ok)
	}
}

func TestACredentialWithoutProviderHasItsOwnGroup(t *testing.T) {
	m := groupModel(t)
	openSectionByName(t, m, sectionCredentials)
	if m.groups.of["loose"] != "" || m.groups.of["wiki-reader"] != "bookstack" {
		t.Errorf("groups = %v", m.groups.of)
	}
	if got := m.groups.rowText(groupRow(""), true); got != "▾ ■ no provider (1)" {
		t.Errorf("row = %q", got)
	}
	if m.list.matches[len(m.list.matches)-2] != groupRow("") {
		t.Errorf("the group without provider is not last: %q", m.list.matches)
	}
}
