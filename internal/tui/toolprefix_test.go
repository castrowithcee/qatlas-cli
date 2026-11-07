package tui

import (
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/approval"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/vault"
)

const testPrefix = "bookstack.pages.*"

// A prefix in a connection's tools stands as the prefix, with the number of tools it covers today, in the
// table, the detail screen, the picker and the approval; the selected count is the covered tools, not the
// entries.
func TestAToolPrefixIsShownAsPrefixWithItsMatchCount(t *testing.T) {
	reg := wikiRegistry(t)
	m, _ := toolsModel(t, reg, map[string]config.Connection{
		"wiki": {Service: "wiki", Credential: "reader", Tools: []string{testPrefix, "bookstack.books.list"}},
	})
	metadata, _ := m.cfg.ProviderMetadata("bookstack")
	matches := config.ToolPrefixMatches(metadata, testPrefix)
	if matches < 2 {
		t.Fatalf("the test prefix covers %d tools, want at least 2 to tell entries from tools", matches)
	}
	want := testPrefix + " " + tr(tToolPrefixCovers, matches)

	openSectionByName(t, m, sectionConnections)
	if got, wantCell := m.cells("wiki")[4], tr(tFormListedPrefix, matches+1); got != wantCell {
		t.Errorf("tools cell = %q, want %q (covered tools, not the 2 entries)", got, wantCell)
	}
	var shown []string
	for _, fact := range m.detailFacts("wiki", tabDetails) {
		shown = append(shown, fact.value)
	}
	if !contains(shown, want) || !contains(shown, "bookstack.books.list") {
		t.Errorf("detail facts = %q, want the prefix as %q and the exact ID unchanged", shown, want)
	}

	openEntryForm(t, m, sectionConnections, "wiki")
	if got := m.toolText(testPrefix); got != want {
		t.Errorf("picker text = %q, want %q", got, want)
	}

	change := approval.Change{Connection: "wiki", After: vault.Scope{Provider: "bookstack", Tools: []string{testPrefix}}, New: true}
	var rows []string
	for _, fact := range m.approvalFacts(change) {
		rows = append(rows, fact.value)
	}
	if !strings.Contains(strings.Join(rows, "\n"), want) {
		t.Errorf("approval rows = %q, want %q", rows, want)
	}
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
