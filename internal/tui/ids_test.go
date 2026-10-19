package tui

import (
	"os"
	"path/filepath"
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
	file := "version: 2\nservices:\n  wiki:\n    id: " + idSvc + "\n    provider: bookstack\n" +
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
