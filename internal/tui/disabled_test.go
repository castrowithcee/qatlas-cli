package tui

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// Saving a disabled connection keeps its state with the rest of the selection, and a copy starts in the
// state of its original.
func TestSavingADisabledConnectionKeepsItsStateAndSelection(t *testing.T) {
	reg := wikiRegistry(t)
	m, path := toolsModel(t, reg, map[string]config.Connection{"wiki": {
		Service: "wiki", Credential: "reader", Disabled: true,
		Permissions: []config.Permission{config.PermissionRead},
		Tools:       []string{"bookstack.pages.list"}, Paths: []string{"/work/kunde"},
	}})
	openEntryForm(t, m, sectionConnections, "wiki")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	got := savedConfig(t, path).Connections["wiki"]
	if !got.Disabled || !reflect.DeepEqual(got.Tools, []string{"bookstack.pages.list"}) ||
		!reflect.DeepEqual(got.Paths, []string{"/work/kunde"}) ||
		!reflect.DeepEqual(got.Permissions, []config.Permission{config.PermissionRead}) {
		t.Fatalf("saved connection = %+v, want state and selection kept", got)
	}

	m.screen = screenList
	m.list.selectName("wiki")
	pump(t, m, "p")
	if m.copyFrom != "wiki" {
		t.Fatalf("p: copyFrom %q, want wiki", m.copyFrom)
	}
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("saving the copy failed: %s", m.fail)
	}
	if copied := savedConfig(t, path).Connections["wiki-copy"]; !copied.Disabled {
		t.Errorf("copy = %+v, want it disabled like its original", copied)
	}
}

// The state row switches between active and disabled with left/right, and saving writes what it shows.
func TestStateRowSwitchesAndSaves(t *testing.T) {
	m, path := toolsModel(t, wikiRegistry(t), map[string]config.Connection{"wiki": {
		Service: "wiki", Credential: "reader", Disabled: true, Tools: []string{"bookstack.pages.list"},
	}})
	openEntryForm(t, m, sectionConnections, "wiki")
	focusField(t, m, stateLabel)
	if got := m.fieldValue(stateLabel); got != stateDisabled {
		t.Fatalf("state row = %q, want %q", got, stateDisabled)
	}
	press(t, m, "left")
	if got := m.fieldValue(stateLabel); got != stateActive {
		t.Fatalf("state row after left = %q, want %q", got, stateActive)
	}
	press(t, m, "right")
	if got := m.fieldValue(stateLabel); got != stateDisabled {
		t.Fatalf("state row after right = %q, want %q", got, stateDisabled)
	}
	press(t, m, "left")
	pump(t, m, "f2")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	got := savedConfig(t, path).Connections["wiki"]
	if got.Disabled || !reflect.DeepEqual(got.Tools, []string{"bookstack.pages.list"}) {
		t.Fatalf("saved connection = %+v, want it active with its selection", got)
	}

	openEntryForm(t, m, sectionConnections, "wiki")
	focusField(t, m, stateLabel)
	press(t, m, "right")
	pump(t, m, "f2")
	if !savedConfig(t, path).Connections["wiki"].Disabled {
		t.Fatalf("switching the row to disabled and saving did not disable the connection")
	}
}

// The list names the state as text, and a disabled connection can still be tested.
func TestListShowsStateAndDisabledConnectionStaysTestable(t *testing.T) {
	m, _ := toolsModel(t, wikiRegistry(t), map[string]config.Connection{"wiki": {
		Service: "wiki", Credential: "reader", Disabled: true,
	}})
	var calls []string
	m.tester = func(_ context.Context, name string) (provider.Class, error) {
		calls = append(calls, name)
		return "", nil
	}
	openSectionByName(t, m, sectionConnections)
	view := m.View()
	if !strings.Contains(view, "STATE") || !strings.Contains(view, "disabled") {
		t.Errorf("the list has no state column with text:\n%s", view)
	}
	pump(t, m, "t")
	if len(calls) != 1 || calls[0] != "wiki" {
		t.Errorf("tester calls = %v, want the disabled connection tested", calls)
	}
}

// A new connection and the guided setup start active.
func TestNewConnectionAndSetupStartActive(t *testing.T) {
	m, _ := toolsModel(t, wikiRegistry(t), nil)
	openSectionByName(t, m, sectionConnections)
	pump(t, m, "n")
	if m.screen != screenForm {
		t.Fatalf("n did not open a form: screen %v", m.screen)
	}
	if got := m.fieldValue(stateLabel); got != stateActive {
		t.Errorf("new connection state row = %q, want %q", got, stateActive)
	}

	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	secrets, _ := newResolver(t, dir, nil)
	sm, err := buildModel(store, nil, secrets, nil)
	if err != nil {
		t.Fatal(err)
	}
	walkSetup(t, sm, stepSummary)
	pump(t, sm, "f2")
	if sm.fail != "" {
		t.Fatalf("setup save failed: %q", sm.fail)
	}
	if savedConfig(t, store.Path()).Connections["personal"].Disabled {
		t.Errorf("a connection from the guided setup starts disabled")
	}
}
