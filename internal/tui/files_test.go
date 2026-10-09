package tui

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// filesCatalog gives the bookstack provider of a real registry the local file directions under test; no
// shipped provider has a tool that reads or writes local files yet.
type filesCatalog struct {
	*capability.Registry
	support config.LocalFilesSupport
}

func (c filesCatalog) ProviderMetadata(id string) (config.ProviderMetadata, bool) {
	metadata, ok := c.Registry.ProviderMetadata(id)
	if id == "bookstack" {
		metadata.LocalFiles = c.support
	}
	return metadata, ok
}

func (c filesCatalog) ProviderMetadataAll() []config.ProviderMetadata {
	all := c.Registry.ProviderMetadataAll()
	for i := range all {
		if all[i].ID == "bookstack" {
			all[i].LocalFiles = c.support
		}
	}
	return all
}

// filesModel builds an editor over the wiki registry whose bookstack provider has the given local file
// directions, with one service, one credential and the named connections saved.
func filesModel(t *testing.T, support config.LocalFilesSupport, connections map[string]config.Connection) (*Model, string, filesCatalog) {
	t.Helper()
	catalog := filesCatalog{Registry: wikiRegistry(t), support: support}
	path := filepath.Join(t.TempDir(), "config.yaml")
	store := config.NewStore(path, catalog)
	cfg := store.New()
	mustNoError(t, cfg.SetService("wiki", config.Service{Provider: "bookstack", BaseURL: "https://wiki.example.invalid"}))
	mustNoError(t, cfg.SetCredential("reader", config.Credential{Provider: "bookstack", Type: config.CredentialTypeKeyring}))
	for name, connection := range connections {
		mustNoError(t, cfg.SetConnection(name, connection))
	}
	mustNoError(t, store.Save(cfg))
	m, err := buildModel(store, nil, nil, &redact.Redactor{})
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
	return m, path, catalog
}

func openFilesList(t *testing.T, m *Model, label string) {
	t.Helper()
	focusField(t, m, label)
	press(t, m, "enter")
	if m.screen != screenPaths {
		t.Fatalf("enter on the %s row opened screen %v, want the directory list", label, m.screen)
	}
}

func rowHidden(m *Model, label string) bool {
	f := m.field(label)
	return f == nil || f.hidden
}

// A local file row stands only for a direction the provider has a tool for; a row that already holds
// entries stays, so they can be seen and removed.
func TestFilesRowsFollowTheDirectionsOfTheProvider(t *testing.T) {
	for _, tc := range []struct {
		name                string
		support             config.LocalFilesSupport
		wantRead, wantWrite bool
		connection          config.Connection
	}{
		{"no direction", config.LocalFilesSupport{}, false, false, config.Connection{Service: "wiki", Credential: "reader"}},
		{"read only", config.LocalFilesSupport{Read: true}, true, false, config.Connection{Service: "wiki", Credential: "reader"}},
		{"write only", config.LocalFilesSupport{Write: true}, false, true, config.Connection{Service: "wiki", Credential: "reader"}},
		{"both", config.LocalFilesSupport{Read: true, Write: true}, true, true, config.Connection{Service: "wiki", Credential: "reader"}},
		// Entries without a direction are refused by the core on load; the row is what a person sees then.
	} {
		m, _, _ := filesModel(t, tc.support, map[string]config.Connection{"wiki": tc.connection})
		openConnection(t, m, "wiki")
		if got := !rowHidden(m, filesReadLabel); got != tc.wantRead {
			t.Errorf("%s: uploads row shown = %v, want %v", tc.name, got, tc.wantRead)
		}
		if got := !rowHidden(m, filesWriteLabel); got != tc.wantWrite {
			t.Errorf("%s: downloads row shown = %v, want %v", tc.name, got, tc.wantWrite)
		}
	}

	// A row holding entries is kept visible even without a direction.
	m, _, _ := filesModel(t, config.LocalFilesSupport{}, nil)
	openSectionByName(t, m, sectionConnections)
	pressNew(t, m)
	m.field(filesReadLabel).entries = []string{"/srv/in"}
	m.syncFilesRows()
	if rowHidden(m, filesReadLabel) {
		t.Error("a row with entries was hidden")
	}
	if !rowHidden(m, filesWriteLabel) {
		t.Error("an empty row without a direction was shown")
	}
}

// The two lists are edited like the path list, saved through the core's validation, and an emptied list is
// written as no files at all.
func TestFilesListsAreEditedAndSaved(t *testing.T) {
	m, path, catalog := filesModel(t, config.LocalFilesSupport{Read: true, Write: true},
		map[string]config.Connection{"wiki": {Service: "wiki", Credential: "reader"}})
	root := t.TempDir()
	wideEnoughFor(m, root)
	in, out, more := filepath.Join(root, "in"), filepath.Join(root, "out"), filepath.Join(root, "more")

	openConnection(t, m, "wiki")
	if view := screenOf(m); !strings.Contains(view, "uploads") || !strings.Contains(view, "downloads") ||
		!strings.Contains(view, "(none: no local files released)") {
		t.Fatalf("the form does not show the two rows and what empty means:\n%s", view)
	}
	openFilesList(t, m, filesReadLabel)
	if view := screenOf(m); !strings.Contains(view, "Directories for uploads of wiki") ||
		!strings.Contains(view, "read files from") {
		t.Fatalf("the list does not say what it is for:\n%s", view)
	}
	addPath(t, m, in)
	addPath(t, m, more)
	// The core's rules refuse a too broad entry as it is taken, without naming more than the rule.
	for _, broad := range []string{"/", "~", "relative/dir", root + "/../x"} {
		addPath(t, m, broad)
		if !strings.Contains(m.fail, "directory:") || m.pathEdit < 0 {
			t.Fatalf("%q was taken: error %q", broad, m.fail)
		}
		press(t, m, "esc", "d")
	}
	pump(t, m, "f2")
	openConnection(t, m, "wiki")
	openFilesList(t, m, filesWriteLabel)
	addPath(t, m, out)
	pump(t, m, "f2")
	openConnection(t, m, "wiki")
	// Edit the second upload directory and remove the first.
	openFilesList(t, m, filesReadLabel)
	press(t, m, "down", "e")
	clearField(t, m)
	typeText(t, m, more+"2")
	press(t, m, "enter", "up", "d", "y")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	saved := savedFilesConnection(t, path, catalog, "wiki")
	if want := (config.Files{Read: []string{more + "2"}, Write: []string{out}}); !reflect.DeepEqual(saved.Files, want) {
		t.Fatalf("saved files = %+v, want %+v", saved.Files, want)
	}

	// Emptied, both lists leave no key behind.
	openConnection(t, m, "wiki")
	openFilesList(t, m, filesReadLabel)
	press(t, m, "d", "y")
	pump(t, m, "f2")
	openConnection(t, m, "wiki")
	openFilesList(t, m, filesWriteLabel)
	press(t, m, "d", "y")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("saving no files failed: %s", m.fail)
	}
	if got := savedFilesConnection(t, path, catalog, "wiki").Files; !got.Empty() || got.Read != nil || got.Write != nil {
		t.Fatalf("saved files = %+v, want none", got)
	}
	if file := savedFile(t, path); strings.Contains(file, "files") {
		t.Fatalf("a connection without files still names them:\n%s", file)
	}
}

// Saving a connection whose files were not touched keeps them.
func TestSavingAConnectionKeepsItsFiles(t *testing.T) {
	dir := t.TempDir()
	m, path, catalog := filesModel(t, config.LocalFilesSupport{Read: true},
		map[string]config.Connection{"wiki": {Service: "wiki", Credential: "reader", Files: config.Files{Read: []string{dir}}}})
	openConnection(t, m, "wiki")
	if got := m.field(filesReadLabel).entries; !reflect.DeepEqual(got, []string{dir}) {
		t.Fatalf("the uploads row holds %v, want %v", got, []string{dir})
	}
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	if got := savedFilesConnection(t, path, catalog, "wiki").Files; !reflect.DeepEqual(got, config.Files{Read: []string{dir}}) {
		t.Fatalf("files after saving = %+v", got)
	}
}

// A direction the provider has no tool for is refused on save by the core; the message names the position,
// never the path.
func TestSavingFilesWithoutADirectionIsRefused(t *testing.T) {
	m, path, catalog := filesModel(t, config.LocalFilesSupport{Read: true},
		map[string]config.Connection{"wiki": {Service: "wiki", Credential: "reader"}})
	openConnection(t, m, "wiki")
	f := m.field(filesWriteLabel)
	f.hidden, f.entries = false, []string{"/srv/private-out"}
	pump(t, m, "f2")
	if !strings.Contains(m.fail, "files.write") {
		t.Fatalf("save was not refused for the write direction: %q", m.fail)
	}
	if strings.Contains(m.fail, "private-out") {
		t.Fatalf("the refusal quotes the path: %q", m.fail)
	}
	if got := savedFilesConnection(t, path, catalog, "wiki").Files; !got.Empty() {
		t.Fatalf("files were saved: %+v", got)
	}
}

// The approval detail shows files like every other field of the scope.
func TestApprovalDetailShowsFiles(t *testing.T) {
	rows := approvalDetailRows(approval.Change{
		New:   true,
		After: vault.Scope{FilesRead: []string{"/srv/in"}, FilesWrite: []string{"/srv/out"}},
	})
	if !containsRow(rows, "files", "read: /srv/in; write: /srv/out") {
		t.Errorf("a new connection's detail lacks its files:\n%s", strings.Join(rows, "\n"))
	}
	rows = approvalDetailRows(approval.Change{
		Fields: []approval.FieldChange{{Field: approval.FieldFiles, Before: "(no local files)", After: "read: /srv/in"}},
	})
	if !containsRow(rows, "files", "(no local files) -> read: /srv/in") {
		t.Errorf("a changed files release is not shown as a change:\n%s", strings.Join(rows, "\n"))
	}
	rows = approvalDetailRows(approval.Change{})
	if !containsRow(rows, "files", "unchanged") {
		t.Errorf("an unchanged files release has no row:\n%s", strings.Join(rows, "\n"))
	}
}

func containsRow(rows []string, label, value string) bool {
	for _, row := range rows {
		if strings.HasPrefix(row, label) && strings.HasSuffix(row, value) {
			return true
		}
	}
	return false
}

func savedFilesConnection(t *testing.T, path string, catalog filesCatalog, name string) config.Connection {
	t.Helper()
	cfg, err := config.Load(path, catalog)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Connections[name]
}
