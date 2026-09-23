package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider/bookstack"
	"github.com/castrowithcee/qatlas-cli/internal/provider/telegram"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// toolsModel builds an editor over the given registry with one service and one credential of its first
// provider saved, and the named connections over them.
func toolsModel(t *testing.T, reg *capability.Registry, connections map[string]config.Connection) (*Model, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	store := config.NewStore(path, reg)
	cfg := store.New()
	provider := reg.ProviderMetadataAll()[0].ID
	mustNoError(t, cfg.SetService("wiki", config.Service{Provider: provider, BaseURL: "https://wiki.example.invalid"}))
	mustNoError(t, cfg.SetCredential("reader", config.Credential{Provider: provider, Type: config.CredentialTypeKeyring}))
	for name, connection := range connections {
		mustNoError(t, cfg.SetConnection(name, connection))
	}
	mustNoError(t, store.Save(cfg))
	m, err := New(store, nil, nil, &redact.Redactor{})
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
	return m, path
}

func wikiRegistry(t *testing.T) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	for _, register := range []func(*capability.Registry) error{bookstack.Register, telegram.Register} {
		if err := register(reg); err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

func savedTools(t *testing.T, path string, reg *capability.Registry, name string) []string {
	t.Helper()
	cfg, err := config.Load(path, reg)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Connections[name].Tools
}

// The tool list offers exactly the tools the chosen provider registered, and follows a change of provider.
func TestTheToolListOffersTheRegisteredToolsOfTheChosenProvider(t *testing.T) {
	reg := wikiRegistry(t)
	m, _ := toolsModel(t, reg, nil)
	mustNoError(t, m.cfg.SetService("chat", config.Service{Provider: "telegram", BaseURL: "https://api.telegram.org"}))
	openSectionByName(t, m, sectionConnections)
	press(t, m, "n")

	var want []string
	for _, descriptor := range reg.Provider("bookstack") {
		want = append(want, descriptor.ID)
	}
	if got := m.field(toolListLabel).choices; !reflect.DeepEqual(got, want) {
		t.Fatalf("bookstack tools = %v, want the registered %v", got, want)
	}
	focusField(t, m, providerLabel)
	selectChoice(t, m, "telegram")
	want = nil
	for _, descriptor := range reg.Provider("telegram") {
		want = append(want, descriptor.ID)
	}
	if got := m.field(toolListLabel).choices; !reflect.DeepEqual(got, want) {
		t.Fatalf("telegram tools = %v, want the registered %v", got, want)
	}
}

// A connection without a tools list is saved without one as long as the tools rows are left alone, and a
// new connection starts the same way.
func TestAConnectionWithoutToolsIsSavedWithoutTools(t *testing.T) {
	reg := wikiRegistry(t)
	m, path := toolsModel(t, reg, map[string]config.Connection{"wiki": {Service: "wiki", Credential: "reader"}})
	openEntryForm(t, m, sectionConnections, "wiki")
	if got := m.fieldValue(toolsLabel); got != toolsAll {
		t.Fatalf("tools mode = %q, want %q", got, toolsAll)
	}
	if !m.field(toolListLabel).readOnly {
		t.Fatal("the tool list takes focus although every permitted tool is offered")
	}
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "tools:") {
		t.Fatalf("an untouched connection gained a tools list:\n%s", data)
	}

	press(t, m, "n")
	typeText(t, m, "fresh")
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	if got := savedTools(t, path, reg, "fresh"); got != nil {
		t.Fatalf("a new connection was saved with tools %#v, want none", got)
	}
}

// Ticking tools in the picker writes exactly those tools; nothing ticked writes the explicit empty list;
// going back to every permitted tool removes the list again.
func TestToolsArePickedTickedAndSaved(t *testing.T) {
	reg := wikiRegistry(t)
	m, path := toolsModel(t, reg, map[string]config.Connection{"wiki": {Service: "wiki", Credential: "reader"}})
	openEntryForm(t, m, sectionConnections, "wiki")
	focusField(t, m, toolsLabel)
	selectChoice(t, m, toolsSelected)
	focusField(t, m, toolListLabel)
	// The row takes no text: typing a tool ID changes nothing.
	typeText(t, m, "bookstack.pages.delete")
	if got := m.fieldValue(toolListLabel); got != "" || m.screen != screenForm {
		t.Fatalf("typing on the tool list gave %q on screen %v", got, m.screen)
	}

	press(t, m, " ")
	if m.screen != screenPicker {
		t.Fatalf("space on the tool list opened screen %v, want the picker", m.screen)
	}
	view := m.View()
	for _, want := range []string{"ticked: 0 of 5", "[ ] bookstack.pages.list  read",
		"[ ] bookstack.pages.delete  delete  (not permitted)"} {
		if !strings.Contains(view, want) {
			t.Fatalf("picker lacks %q:\n%s", want, view)
		}
	}
	typeText(t, m, "list")
	press(t, m, " ")
	if !strings.Contains(m.View(), "[x] bookstack.pages.list") {
		t.Fatalf("space did not tick the shown tool:\n%s", m.View())
	}
	press(t, m, "esc")
	if got := m.fieldValue(toolListLabel); got != "" {
		t.Fatalf("esc kept the ticks: %q", got)
	}
	press(t, m, "/")
	typeText(t, m, "get")
	press(t, m, " ", "enter")
	if got := m.fieldValue(toolListLabel); got != "bookstack.pages.get" {
		t.Fatalf("tool list = %q, want bookstack.pages.get", got)
	}
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	if got := savedTools(t, path, reg, "wiki"); !reflect.DeepEqual(got, []string{"bookstack.pages.get"}) {
		t.Fatalf("saved tools = %#v", got)
	}

	openEntryForm(t, m, sectionConnections, "wiki")
	if got := m.fieldValue(toolsLabel); got != toolsSelected {
		t.Fatalf("a listed connection opens in mode %q", got)
	}
	focusField(t, m, toolListLabel)
	press(t, m, " ", " ", "enter")
	if !strings.Contains(m.View(), "none of 5 ticked: no tool is offered") {
		t.Fatalf("an empty selection does not say that it closes the route:\n%s", m.View())
	}
	pump(t, m, "enter")
	if got := savedTools(t, path, reg, "wiki"); got == nil || len(got) != 0 {
		t.Fatalf("saved tools = %#v, want the explicit empty list", got)
	}

	openEntryForm(t, m, sectionConnections, "wiki")
	focusField(t, m, toolsLabel)
	selectChoice(t, m, toolsAll)
	pump(t, m, "enter")
	if got := savedTools(t, path, reg, "wiki"); got != nil {
		t.Fatalf("saved tools = %#v, want no tools list", got)
	}
}

// A tool whose effect the permissions exclude is refused by the core, and the editor shows why.
func TestAToolTheFormPermissionsExcludeIsRefused(t *testing.T) {
	reg := wikiRegistry(t)
	m, path := toolsModel(t, reg, map[string]config.Connection{"wiki": {Service: "wiki", Credential: "reader"}})
	openEntryForm(t, m, sectionConnections, "wiki")
	focusField(t, m, toolsLabel)
	selectChoice(t, m, toolsSelected)
	focusField(t, m, toolListLabel)
	press(t, m, "/")
	typeText(t, m, "delete")
	press(t, m, " ", "enter")
	pump(t, m, "enter")
	if !strings.Contains(m.fail, `tool "bookstack.pages.delete" has effect delete`) {
		t.Fatalf("fail = %q, want the refused effect", m.fail)
	}
	if got := savedTools(t, path, reg, "wiki"); got != nil {
		t.Fatalf("a refused list reached the file: %#v", got)
	}
}

// Sixty tools stay reachable in the smallest supported terminal: the picker scrolls, filters and ticks.
func TestManyToolsStayPickableInASmallTerminal(t *testing.T) {
	reg := capability.NewRegistry()
	if err := reg.RegisterProvider(config.ProviderMetadata{ID: "fake", Name: "Fake"}, nil); err != nil {
		t.Fatal(err)
	}
	var operations []capability.Operation
	for i := 0; i < 60; i++ {
		operations = append(operations, capability.Operation{
			Descriptor: capability.Descriptor{
				ID: fmt.Sprintf("fake.object%02d.list", i), Version: 1, Provider: "fake",
				Description: "List objects",
				Risk: capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
					Confirmation: capability.ConfirmationNone, DataSensitivity: "test"},
				InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"array"}`),
			},
			Handler: func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
				json.RawMessage) (any, error) {
				return []any{}, nil
			},
		})
	}
	if err := reg.Register("fake", operations...); err != nil {
		t.Fatal(err)
	}
	m, path := toolsModel(t, reg, map[string]config.Connection{
		"route": {Service: "wiki", Credential: "reader", Tools: []string{}},
	})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	openEntryForm(t, m, sectionConnections, "route")
	focusField(t, m, toolListLabel)
	// The connection form itself needs more than twelve lines; the picker it opens does not.
	press(t, m, " ")
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	check := func(want string) {
		t.Helper()
		view := m.View()
		if strings.Contains(view, "Resize terminal") {
			t.Fatalf("the picker was replaced by the resize notice:\n%s", view)
		}
		assertViewFits(t, view, 40, 12)
		if !strings.Contains(view, want) {
			t.Fatalf("%q is not on screen:\n%s", want, view)
		}
	}
	check("fake.object00.list")
	press(t, m, "end")
	check("fake.object59.list")
	press(t, m, " ")
	check("[x] fake.object59.list")
	typeText(t, m, "object42")
	check("1/1 (60 total)")
	press(t, m, " ")
	check("[x] fake.object42.list")
	press(t, m, "enter")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if got := m.fieldValue(toolListLabel); got != "fake.object42.list, fake.object59.list" {
		t.Fatalf("tool list = %q", got)
	}
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	if got := savedTools(t, path, reg, "route"); !reflect.DeepEqual(got,
		[]string{"fake.object42.list", "fake.object59.list"}) {
		t.Fatalf("saved tools = %#v", got)
	}
}
