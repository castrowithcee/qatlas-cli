package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// newTableModel is an editor on two providers with entries in every section, sized to a terminal of the
// given width. One keyring secret is stored and one is not, and the variable of the env credential is set.
func newTableModel(t *testing.T, width int) *Model {
	t.Helper()
	reg := testCatalog(t)
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: "tracker", Name: "Tracker",
		SecretRoles: []config.SecretRole{{Name: "token", Description: "tracker API token"}},
		Target:      config.TargetMetadata{Label: "repository", Multiple: true},
	}, nil); err != nil {
		t.Fatalf("register tracker: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "qatlas")
	secrets, keyring := newResolver(t, dir, map[string]string{"TRACKER_TOKEN": "canary-tracker-token"})
	if err := keyring.Set(secret.StoreKey("wiki-reader", "token-id"), "canary-wiki-id"); err != nil {
		t.Fatalf("store secret: %v", err)
	}
	m, err := New(config.NewStore(filepath.Join(dir, "config.yaml"), reg), nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	m.cfg.Services = map[string]config.Service{
		"wiki-main":    {Provider: "bookstack", BaseURL: "https://wiki.example.invalid"},
		"tracker-main": {Provider: "tracker", BaseURL: "https://tracker.internal.example.invalid/api/v4"},
	}
	m.cfg.Credentials = map[string]config.Credential{
		"wiki-reader": {Provider: "bookstack", Type: config.CredentialTypeKeyring},
		"tracker-bot": {Provider: "tracker", Type: config.CredentialTypeEnv,
			Values: map[string]string{"token": "TRACKER_TOKEN"}},
	}
	m.cfg.Connections = map[string]config.Connection{
		"wiki-docs": {Service: "wiki-main", Credential: "wiki-reader",
			Description: "handbook pages for onboarding, read only"},
		"tracker-issues": {Service: "tracker-main", Credential: "tracker-bot", Targets: []string{"octo/test-repo"},
			Permissions: []config.Permission{config.PermissionRead, config.PermissionCreate},
			Tools:       []string{"tracker.issues.list", "tracker.issues.create"},
			Description: "issues in the test repository, read and create"},
		"tracker-triage": {Service: "tracker-main", Credential: "tracker-bot",
			Targets:     []string{"octo/a", "octo/b", "octo/c"},
			Permissions: []config.Permission{config.PermissionRead, config.PermissionUpdate}, Tools: []string{}},
	}
	m.cfg.Defaults.Connections = map[string]string{
		"bookstack": "wiki-docs", "tracker": "tracker-issues", "tracker.issues.create": "tracker-issues",
	}
	m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
	return m
}

// tableLines are the column names and the rows of the list screen, in the order they are drawn.
func tableLines(t *testing.T, m *Model) (string, []string) {
	t.Helper()
	lines := strings.Split(screenOf(m), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "  "+sectionColumns[m.section][0].title) {
			end := min(i+1+len(m.list.matches), len(lines))
			return line, lines[i+1 : end]
		}
	}
	t.Fatalf("the list has no column names:\n%s", screenOf(m))
	return "", nil
}

// cellAt is the cell of line that starts at the column of the title in header.
func cellAt(header, line, title string) string {
	offset := lipgloss.Width(header[:strings.Index(header, title)])
	runes, used := []rune(line), 0
	for i, r := range runes {
		if used == offset {
			rest := string(runes[i:])
			if cut := strings.Index(rest, columnGap); cut >= 0 {
				rest = rest[:cut]
			}
			return strings.TrimSpace(rest)
		}
		used += lipgloss.Width(string(r))
	}
	return ""
}

// Every section lists its entries as a table: a line of column names, and under it one row per entry
// whose cells start exactly where their column name does.
func TestSectionListsAreAlignedTables(t *testing.T) {
	m := newTableModel(t, 200)
	want := map[section]map[string][]string{
		sectionServices: {
			"tracker-main": {"tracker-main", "tracker", "https://tracker.internal.example.invalid/api/v4"},
			"wiki-main":    {"wiki-main", "bookstack", "https://wiki.example.invalid"},
		},
		sectionCredentials: {
			"tracker-bot": {"tracker-bot", "tracker", placeEnv, "token ✓"},
			"wiki-reader": {"wiki-reader", "bookstack", placeKeyring, "token-id ✓, token-secret missing"},
		},
		sectionConnections: {
			"tracker-issues": {"tracker-issues", "tracker-main", "read,create", "listed 2",
				"octo/test-repo", "issues in the test repository, read and create"},
			"tracker-triage": {"tracker-triage", "tracker-main", "read,update", "none", "3 targets",
				undescribedMarker},
			"wiki-docs": {"wiki-docs", "wiki-main", "read", "all", "-",
				"handbook pages for onboarding, read only"},
		},
		sectionDefaults: {
			"bookstack":             {"bookstack", "wiki-docs"},
			"tracker":               {"tracker", "tracker-issues"},
			"tracker.issues.create": {"tracker.issues.create", "tracker-issues"},
		},
	}
	for s, entries := range want {
		openSectionByName(t, m, s)
		header, rows := tableLines(t, m)
		if len(rows) != len(entries) {
			t.Fatalf("%s: %d rows, want %d:\n%s", s.title(), len(rows), len(entries), screenOf(m))
		}
		for i, row := range rows {
			cells := entries[m.list.matches[i]]
			for c, col := range sectionColumns[s] {
				if got := cellAt(header, row, col.title); got != cells[c] {
					t.Errorf("%s: column %s of %q = %q, want %q\n%s", s.title(), col.title,
						m.list.matches[i], got, cells[c], screenOf(m))
				}
			}
		}
		if strings.Contains(screenOf(m), "canary") {
			t.Fatalf("%s: the list shows a secret value:\n%s", s.title(), screenOf(m))
		}
	}
}

// A narrow terminal cuts the long texts and marks the cut; the name of every entry stays whole, and no line
// is wider than the workspace. The sidebar leaves an 80 column terminal the narrowest workspace it has.
func TestNarrowTablesCutTheLongTextsButNeverTheName(t *testing.T) {
	for _, width := range []int{80, 60, 40} {
		m := newTableModel(t, width)
		for s := sectionServices; s <= sectionDefaults; s++ {
			openSectionByName(t, m, s)
			assertViewFits(t, screenOf(m), m.width, m.height)
			header, rows := tableLines(t, m)
			for i, row := range rows {
				name := m.list.matches[i]
				if got := cellAt(header, row, sectionColumns[s][0].title); got != name {
					t.Errorf("width %d, %s: name cell = %q, want %q whole\n%s", width, s.title(), got, name,
						screenOf(m))
				}
			}
		}
		openSectionByName(t, m, sectionConnections)
		if view := screenOf(m); !strings.Contains(view, "…") || !strings.Contains(view, "DESCRIPTION") {
			t.Errorf("width %d: the description is not cut to fit:\n%s", width, view)
		}
	}
}

// The description gives way before any value that names something: at 120 columns every other cell of a
// connection stays whole beside a long description, and at 80 columns the optional columns are left out,
// from the right, before a name or a service is cut. The storage column falls back to its short labels.
func TestTheFreeTextGivesWayFirst(t *testing.T) {
	long := "handbook pages for onboarding new colleagues, read only, never anything written back"
	for _, c := range []struct {
		width   int
		columns []string
		gone    []string
	}{
		{120, []string{"NAME", "SERVICE", "EFFECTS", "TOOLS", "TARGETS"}, nil},
		{80, []string{"NAME", "SERVICE"}, []string{"EFFECTS", "TOOLS", "TARGETS"}},
	} {
		m := newTableModel(t, c.width)
		conn := m.cfg.Connections["wiki-docs"]
		conn.Description = long
		m.cfg.Connections["wiki-docs"] = conn
		openSectionByName(t, m, sectionConnections)
		header, rows := tableLines(t, m)
		for _, title := range c.gone {
			if strings.Contains(header, title) {
				t.Errorf("width %d: column %s is still shown:\n%s", c.width, title, screenOf(m))
			}
		}
		for i, row := range rows {
			cells := m.cells(m.list.matches[i])
			for index, title := range c.columns {
				if got := cellAt(header, row, title); got != cells[index] {
					t.Errorf("width %d: %s = %q, want %q whole\n%s", c.width, title, got, cells[index], screenOf(m))
				}
			}
			if got := cellAt(header, row, "DESCRIPTION"); got != cells[5] &&
				(!strings.HasSuffix(got, "…") || lipgloss.Width(got) < flexFloor) {
				t.Errorf("width %d: description = %q, want it cut to the room left\n%s", c.width, got, screenOf(m))
			}
		}
	}

	m := newTableModel(t, 80)
	openSectionByName(t, m, sectionCredentials)
	header, rows := tableLines(t, m)
	if len(rows) != 2 || cellAt(header, rows[0], "STORAGE") != "env" ||
		cellAt(header, rows[1], "STORAGE") != "keyring" {
		t.Errorf("the storage column does not fall back to its short labels:\n%s", screenOf(m))
	}
}

// The filter still looks at every cell of an entry, including the part a narrow terminal cuts off, and the
// column names stay above what it leaves.
func TestFilterFindsTableEntriesByAnyCell(t *testing.T) {
	m := newTableModel(t, 80)
	openSectionByName(t, m, sectionConnections)
	press(t, m, "/")
	typeText(t, m, "onboarding")
	if len(m.list.matches) != 1 || m.list.matches[0] != "wiki-docs" {
		t.Fatalf("matches = %v, want the connection whose description holds the text", m.list.matches)
	}
	if header, rows := tableLines(t, m); !strings.Contains(header, "SERVICE") || len(rows) != 1 ||
		!strings.Contains(rows[0], "wiki-docs") {
		t.Errorf("the filtered table is not shown under its column names:\n%s", screenOf(m))
	}
	press(t, m, "esc")
	openSectionByName(t, m, sectionCredentials)
	press(t, m, "/")
	typeText(t, m, "missing")
	if len(m.list.matches) != 1 || m.list.matches[0] != "wiki-reader" {
		t.Errorf("matches = %v, want the credential with a missing secret", m.list.matches)
	}
}

// The column names are part of the frame: a list longer than the terminal scrolls under them.
func TestTheColumnNamesDoNotScrollAway(t *testing.T) {
	m := newTableModel(t, 120)
	for i := range 40 {
		m.cfg.Services[fmt.Sprintf("service-%02d", i)] = config.Service{Provider: "bookstack",
			BaseURL: "https://wiki.example.invalid"}
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 16})
	openSectionByName(t, m, sectionServices)
	press(t, m, "end")
	header, rows := tableLines(t, m)
	if !strings.Contains(header, "BASE URL") || len(rows) == 0 {
		t.Fatalf("the column names are gone after scrolling:\n%s", screenOf(m))
	}
	if !strings.Contains(screenOf(m), "> wiki-main") {
		t.Errorf("the last entry is not selected and shown:\n%s", screenOf(m))
	}
	assertViewFits(t, screenOf(m), m.width, m.height)
}

// An empty section keeps its help text and draws no column names over nothing.
func TestAnEmptySectionShowsNoColumnNames(t *testing.T) {
	m, _, _ := newModel(t)
	openSectionByName(t, m, sectionServices)
	if view := screenOf(m); strings.Contains(view, "BASE URL") || !strings.Contains(view, "No services yet") {
		t.Errorf("the empty list:\n%s", view)
	}
}

// The order in which a row gives way: the free text first, down to its floor; then the short labels; then
// the optional columns, from the right; then the other values, the widest first and never below their
// heading; and the name never. The free text gets whatever room is left.
func TestTableLayoutPriority(t *testing.T) {
	tab := table{
		columns: []column{{title: "NAME"}, {title: "KIND", optional: true},
			{title: "NOTE", optional: true, short: map[string]string{"a long label": "short"}},
			{title: "ID"}, {title: "TEXT", flex: true}},
		rows: map[string][]string{
			"a-rather-long-name": {"a-rather-long-name", "kind-value", "a long label", "identifier-x",
				"free text that gives way before the rest"},
		},
	}
	for _, c := range []struct {
		width int
		want  []int
		short bool
		line  string
	}{
		{200, []int{18, 10, 12, 12, 40}, false,
			"a-rather-long-name  kind-value  a long label  identifier-x  free text that gives way before the rest"},
		{80, []int{18, 10, 12, 12, 20}, false,
			"a-rather-long-name  kind-value  a long label  identifier-x  free text that give…"},
		{70, []int{18, 10, 5, 12, 17}, true, "a-rather-long-name  kind-value  short  identifier-x  free text that g…"},
		{55, []int{18, 0, 0, 12, 21}, true, "a-rather-long-name  identifier-x  free text that gives…"},
		{40, []int{18, 0, 0, 6, 12}, true, "a-rather-long-name  ident…  free text t…"},
		{30, []int{18, 0, 0, 2, 6}, true, "a-rather-long-name  i…  free …"},
		{10, []int{18, 0, 0, 2, 4}, true, "a-rather-long-name  i…  fre…"},
	} {
		got := tab.layout(c.width)
		if fmt.Sprint(got.widths) != fmt.Sprint(c.want) || got.short[2] != c.short {
			t.Errorf("layout(%d) = %v, short %v; want %v, short %v", c.width, got.widths, got.short, c.want,
				c.short)
		}
		if line := tab.line(tab.rows["a-rather-long-name"], got); line != c.line {
			t.Errorf("line at %d = %q, want %q", c.width, line, c.line)
		}
	}
}
