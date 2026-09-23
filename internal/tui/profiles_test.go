package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// toggleTool ticks or unticks one tool of the form through the tool picker, the way a person does.
func toggleTool(t *testing.T, m *Model, id string) {
	t.Helper()
	tick(t, m, toolListLabel, id)
}

// tick ticks or unticks one value of a multiselect row in its picker, found by typing it.
func tick(t *testing.T, m *Model, label, value string) {
	t.Helper()
	focusField(t, m, label)
	press(t, m, "/")
	typeText(t, m, value)
	press(t, m, " ", "enter")
	if m.screen != screenForm {
		t.Fatalf("the %s picker did not close: screen %v", label, m.screen)
	}
}

// openConnection opens the form of one saved connection from the Connections list.
func openConnection(t *testing.T, m *Model, name string) {
	t.Helper()
	openSectionByName(t, m, sectionConnections)
	m.list.selectName(name)
	pump(t, m, "enter")
	if m.screen != screenForm || m.editing != name {
		t.Fatalf("screen = %v, editing = %q, want the form of %q", m.screen, m.editing, name)
	}
}

func savedConnection(t *testing.T, path string, reg *capability.Registry, name string) config.Connection {
	t.Helper()
	cfg, err := config.Load(path, reg)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Connections[name]
}

// A new connection starts on the recommended profile, fully expanded into visible ticks. Every preselected
// tool can be unticked and every registered tool ticked, and what is saved are the explicit ticks alone.
func TestANewConnectionStartsOnTheRecommendedProfile(t *testing.T) {
	reg := wikiRegistry(t)
	m, path := toolsModel(t, reg, nil)
	openSectionByName(t, m, sectionConnections)
	press(t, m, "n")
	for label, want := range map[string]string{
		profileLabel: "read", "permissions": "read", toolsLabel: toolsSelected,
		toolListLabel: "bookstack.pages.get, bookstack.pages.list",
	} {
		if got := m.fieldValue(label); got != want {
			t.Errorf("%s = %q, want %q", label, got, want)
		}
	}
	if hint := m.fieldHint(*m.field(toolListLabel)); !strings.HasSuffix(hint,
		"ticked: bookstack.pages.get, bookstack.pages.list") {
		t.Errorf("the tool list does not name its ticks: %q", hint)
	}
	for _, want := range []string{"not a role", "do not narrow what the credential itself may do",
		"ticks permissions read and tools bookstack.pages.list, bookstack.pages.get"} {
		if hint := m.field(profileLabel).hint; !strings.Contains(hint, want) {
			t.Errorf("the profile row does not say %q: %q", want, hint)
		}
	}
	if view := screenOf(m); !strings.Contains(view, "profile      < read >") {
		t.Errorf("the profile row is not shown:\n%s", view)
	}

	typeText(t, m, "fresh")
	toggleTool(t, m, "bookstack.pages.get")
	toggleTool(t, m, "bookstack.pages.list")
	if got := m.fieldValue(toolListLabel); got != "" {
		t.Fatalf("the preselected tools could not be unticked: %q", got)
	}
	if got := m.fieldValue(profileLabel); got != profileCustom {
		t.Fatalf("profile = %q after a change by hand, want %q", got, profileCustom)
	}
	for _, permission := range []string{"create", "update", "delete"} {
		tick(t, m, "permissions", permission)
	}
	var every []string
	for _, descriptor := range reg.Provider("bookstack") {
		toggleTool(t, m, descriptor.ID)
		every = append(every, descriptor.ID)
	}
	pump(t, m, "ctrl+s")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	saved := savedConnection(t, path, reg, "fresh")
	if !reflect.DeepEqual(saved.Tools, every) {
		t.Fatalf("saved tools = %#v, want every registered tool %v", saved.Tools, every)
	}
	if got := config.FormatPermissions(saved.Permissions); got != "read, create, update, delete" {
		t.Fatalf("saved permissions = %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "profile") || strings.Contains(string(data), "custom") {
		t.Fatalf("the configuration names a profile:\n%s", data)
	}
}

// Switching profiles replaces the ticks at once while they are a profile's, and only after asking once
// they were changed by hand.
func TestSwitchingProfilesAsksOnceTheTicksWereChanged(t *testing.T) {
	m, _ := toolsModel(t, wikiRegistry(t), nil)
	mustNoError(t, m.cfg.SetService("chat", config.Service{Provider: "telegram", BaseURL: "https://api.telegram.org"}))
	openSectionByName(t, m, sectionConnections)
	press(t, m, "n")
	focusField(t, m, providerLabel)
	selectChoice(t, m, "telegram")
	check := func(profile, permissions, tools string) {
		t.Helper()
		for label, want := range map[string]string{profileLabel: profile, "permissions": permissions,
			toolListLabel: tools} {
			if got := m.fieldValue(label); got != want {
				t.Fatalf("%s = %q, want %q", label, got, want)
			}
		}
	}
	check("send", "create", "telegram.messages.send")
	if hint := m.field(profileLabel).hint; !strings.Contains(hint, "preselects a change because") {
		t.Errorf("the recommended change is not explained: %q", hint)
	}

	focusField(t, m, profileLabel)
	selectChoice(t, m, "messaging")
	if m.screen != screenForm {
		t.Fatalf("an untouched profile asked before switching: screen %v", m.screen)
	}
	all := "telegram.messages.delete, telegram.messages.edit, telegram.messages.send"
	check("messaging", "create, update, delete", all)

	toggleTool(t, m, "telegram.messages.delete")
	check(profileCustom, "create, update, delete", "telegram.messages.edit, telegram.messages.send")
	focusField(t, m, profileLabel)
	press(t, m, "right")
	if m.screen != screenConfirm {
		t.Fatalf("a change by hand was replaced without asking: screen %v", m.screen)
	}
	view := strings.Join(strings.Fields(screenOf(m)), " ")
	for _, want := range []string{"Replace the permission and tool ticks with profile send?",
		"ticks permissions create and tools telegram.messages.send"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the question lacks %q:\n%s", want, view)
		}
	}
	press(t, m, "n")
	check(profileCustom, "create, update, delete", "telegram.messages.edit, telegram.messages.send")
	press(t, m, "right", "y")
	if m.screen != screenForm {
		t.Fatalf("screen = %v after confirming, want the form", m.screen)
	}
	check("send", "create", "telegram.messages.send")
}

// A saved connection opens as it was saved and is saved unchanged: the profile row only says which profile
// its ticks match. A profile replaces its ticks only after asking.
func TestASavedConnectionIsNotChangedByProfiles(t *testing.T) {
	reg := wikiRegistry(t)
	m, path := toolsModel(t, reg, map[string]config.Connection{
		"all": {Service: "wiki", Credential: "reader"},
		"listed": {Service: "wiki", Credential: "reader",
			Permissions: []config.Permission{config.PermissionRead, config.PermissionCreate},
			Tools:       []string{"bookstack.pages.list", "bookstack.pages.create"}},
		"matching": {Service: "wiki", Credential: "reader", Permissions: []config.Permission{config.PermissionRead},
			Tools: []string{"bookstack.pages.list", "bookstack.pages.get"}},
	})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, profile := range map[string]string{"all": profileCustom, "listed": profileCustom, "matching": "read"} {
		openConnection(t, m, name)
		if got := m.fieldValue(profileLabel); got != profile {
			t.Errorf("%s opens on profile %q, want %q", name, got, profile)
		}
		pump(t, m, "enter")
		if m.fail != "" {
			t.Fatalf("saving %s failed: %s", name, m.fail)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("opening and saving changed the configuration:\n%s\nwant\n%s", after, before)
	}

	openConnection(t, m, "all")
	focusField(t, m, profileLabel)
	press(t, m, "right")
	if m.screen != screenConfirm {
		t.Fatalf("a profile replaced the ticks of a saved connection without asking: screen %v", m.screen)
	}
	press(t, m, "n")
	if m.fieldValue(toolsLabel) != toolsAll || m.fieldValue("permissions") != "" {
		t.Fatalf("declining changed the form: tools %q, permissions %q", m.fieldValue(toolsLabel),
			m.fieldValue("permissions"))
	}
	press(t, m, "right", "y")
	pump(t, m, "ctrl+s")
	saved := savedConnection(t, path, reg, "all")
	if !reflect.DeepEqual(saved.Tools, []string{"bookstack.pages.get", "bookstack.pages.list"}) ||
		config.FormatPermissions(saved.Permissions) != "read" {
		t.Fatalf("a confirmed profile saved %+v", saved)
	}
}

// A tool registered later is offered for ticking but joins neither a saved connection nor a profile.
func TestALaterToolJoinsNoSavedConnection(t *testing.T) {
	reg := wikiRegistry(t)
	m, path := toolsModel(t, reg, map[string]config.Connection{
		"wiki": {Service: "wiki", Credential: "reader", Permissions: []config.Permission{config.PermissionRead},
			Tools: []string{"bookstack.pages.list"}},
	})
	if err := reg.Register("bookstack", capability.Operation{
		Descriptor: capability.Descriptor{
			ID: "bookstack.books.list", Version: 1, Provider: "bookstack", Description: "List books",
			Risk: capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
				Confirmation: capability.ConfirmationNone, DataSensitivity: "test"},
			InputSchema: json.RawMessage(`{"type":"object"}`), OutputSchema: json.RawMessage(`{"type":"array"}`),
		},
		Handler: func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor,
			json.RawMessage) (any, error) {
			return []any{}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	openConnection(t, m, "wiki")
	if list := m.field(toolListLabel); !strings.Contains(strings.Join(list.choices, ","), "bookstack.books.list") ||
		list.selected["bookstack.books.list"] {
		t.Fatalf("the later tool is not offered unticked: %v %v", list.choices, list.selected)
	}
	pump(t, m, "ctrl+s")
	if got := savedTools(t, path, reg, "wiki"); !reflect.DeepEqual(got, []string{"bookstack.pages.list"}) {
		t.Fatalf("saved tools = %#v, want the listed tool only", got)
	}
	press(t, m, "n")
	if got := m.fieldValue(toolListLabel); got != "bookstack.pages.get, bookstack.pages.list" {
		t.Fatalf("the recommended profile of a new connection ticks %q", got)
	}
}

// The guided setup starts its permissions step on the recommended profile, shows exactly what it ticks, and
// saves the explicit ticks as changed there.
func TestGuidedSetupStartsOnTheRecommendedProfile(t *testing.T) {
	reg := wikiRegistry(t)
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	secrets, _ := newResolver(t, dir, nil)
	m, err := New(config.NewStore(path, reg), nil, secrets, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
	walkSetup(t, m, stepPermissions)
	if m.fieldValue(profileLabel) != "read" || m.fieldValue("permissions") != "read" ||
		m.fieldValue(toolListLabel) != "bookstack.pages.get, bookstack.pages.list" {
		t.Fatalf("permissions step = profile %q, permissions %q, tools %q", m.fieldValue(profileLabel),
			m.fieldValue("permissions"), m.fieldValue(toolListLabel))
	}
	toggleTool(t, m, "bookstack.pages.list")
	if m.fieldValue(profileLabel) != profileCustom {
		t.Fatalf("profile = %q after a change by hand", m.fieldValue(profileLabel))
	}
	press(t, m, "ctrl+s")
	if m.screen != screenSummary {
		t.Fatalf("screen = %v, error %q, want the summary", m.screen, m.fail)
	}
	for _, want := range []string{"permissions  read", "tools        bookstack.pages.get"} {
		if !strings.Contains(screenOf(m), want) {
			t.Fatalf("summary lacks %q:\n%s", want, screenOf(m))
		}
	}
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("save failed: %s", m.fail)
	}
	saved := savedConnection(t, path, reg, "personal")
	if !reflect.DeepEqual(saved.Tools, []string{"bookstack.pages.get"}) ||
		config.FormatPermissions(saved.Permissions) != "read" {
		t.Fatalf("saved connection = %+v", saved)
	}
}
