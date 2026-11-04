package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	idSvc  = "00000000000000000000000000000001"
	idCred = "00000000000000000000000000000002"
	idConn = "00000000000000000000000000000003"
)

// A schema-2 file keeps the ids of every entry when the editor saves a form that rebuilds the entry.
func TestSavingKeepsStableIDs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := "version: 4\nservices:\n  wiki:\n    id: " + idSvc + "\n    provider: bookstack\n" +
		"    base_url: https://wiki.example.invalid\ncredentials:\n  reader:\n    id: " + idCred +
		"\n    type: env\n    values:\n      token-id: A\n      token-secret: B\n" +
		"connections:\n  wiki:\n    id: " + idConn + "\n    service: wiki\n    credential: reader\n"
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t, path)
	secrets, _ := newResolver(t, dir, nil)
	m, err := buildModel(store, nil, secrets, nil)
	if err != nil {
		t.Fatal(err)
	}

	openEntryForm(t, m, sectionConnections, "wiki")
	focusField(t, m, "description")
	typeText(t, m, "route")
	press(t, m, "f2")
	openEntryForm(t, m, sectionServices, "wiki")
	press(t, m, "f2")
	openEntryForm(t, m, sectionCredentials, "reader")
	press(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("editor reported %q", m.fail)
	}

	saved, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Connections["wiki"].Description != "route" {
		t.Fatalf("the edit was not saved: %+v", saved.Connections["wiki"])
	}
	if saved.Services["wiki"].ID != idSvc || saved.Credentials["reader"].ID != idCred || saved.Connections["wiki"].ID != idConn {
		t.Errorf("ids lost: %+v %+v %+v", saved.Services["wiki"], saved.Credentials["reader"], saved.Connections["wiki"])
	}
}

func idModel(t *testing.T, withIDs bool) (*Model, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ids := func(id string) string {
		if withIDs {
			return "    id: " + id + "\n"
		}
		return ""
	}
	file := "version: 4\nservices:\n  wiki:\n" + ids(idSvc) + "    provider: bookstack\n" +
		"    base_url: https://wiki.example.invalid\ncredentials:\n  reader:\n" + ids(idCred) +
		"    type: env\n    values:\n      token-id: A\n      token-secret: B\n" +
		"connections:\n  wiki:\n" + ids(idConn) + "    service: wiki\n    credential: reader\n"
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t, path)
	secrets, _ := newResolver(t, dir, nil)
	m, err := buildModel(store, nil, secrets, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m, path
}

// The form shows the id of each kind of entry as a read-only row in its first section, and the copy key
// hands it over.
func TestFormShowsAndCopiesID(t *testing.T) {
	m, _ := idModel(t, true)
	for _, c := range []struct {
		s    section
		name string
		id   string
	}{{sectionServices, "wiki", idSvc}, {sectionCredentials, "reader", idCred}, {sectionConnections, "wiki", idConn}} {
		openEntryForm(t, m, c.s, c.name)
		f := m.field("id")
		if f == nil || f.value() != c.id || !f.readOnly {
			t.Fatalf("%v: id row = %+v", c.s, f)
		}
		if f.section != "" && f.section != secGeneral {
			t.Errorf("%v: id row in section %q, want the first", c.s, f.section)
		}
		focusField(t, m, "id")
		if got, ok := pressCopy(m); !ok || got != c.id {
			t.Errorf("%v: copy handed over %q (%v)", c.s, got, ok)
		}
		before := f.value()
		typeText(t, m, "x")
		if m.field("id").value() != before {
			t.Errorf("%v: id row was edited", c.s)
		}
	}
}

// An entry without an id says it gets one on the next save; saving then keeps what the file assigns.
func TestFormWithoutIDSaysItGetsOne(t *testing.T) {
	m, _ := idModel(t, false)
	openEntryForm(t, m, sectionServices, "wiki")
	f := m.field("id")
	if f == nil || f.value() != "" || f.hint != tr(tFormIDPendingHint) {
		t.Fatalf("id row = %+v", f)
	}
	if !strings.Contains(m.View().Content, tr(tFormIDPendingHint)) {
		t.Errorf("the notice is not drawn")
	}
	if f.copyable {
		t.Errorf("the notice row takes focus, so there would be nothing to copy")
	}
	press(t, m, "esc")
	openSectionByName(t, m, sectionServices)
	pressNew(t, m)
	if f := m.field("id"); f == nil || f.value() != "" {
		t.Errorf("a new entry shows %+v", f)
	}
}
