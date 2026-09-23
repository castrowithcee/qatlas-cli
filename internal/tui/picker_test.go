package tui

import (
	"fmt"
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
)

// seedConnections stores one service, one credential and the named connections over them, so a test can
// start from a choice row longer than any terminal.
func seedConnections(t *testing.T, m *Model, names ...string) {
	t.Helper()
	mustNoError(t, m.cfg.SetService("wiki", config.Service{
		Provider: "bookstack", BaseURL: "https://wiki.example.invalid",
	}))
	mustNoError(t, m.cfg.SetCredential("reader", config.Credential{
		Provider: "bookstack", Type: config.CredentialTypeKeyring,
	}))
	for _, name := range names {
		mustNoError(t, m.cfg.SetConnection(name, config.Connection{Service: "wiki", Credential: "reader"}))
	}
	mustNoError(t, m.store.Save(m.cfg))
	m.configExists = true
}

func numberedConnections(n int) []string {
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("conn-%03d", i)
	}
	return names
}

// openDefaultForm opens the form of a new default and focuses its connection row.
func openDefaultForm(t *testing.T, m *Model) {
	t.Helper()
	openSectionByName(t, m, sectionDefaults)
	press(t, m, "n")
	typeText(t, m, "docs")
	focusField(t, m, "connection")
}

// countingPress presses keys and returns how many it pressed, so a test can bound the effort of a choice.
func countingPress(t *testing.T, m *Model, keys ...string) int {
	t.Helper()
	press(t, m, keys...)
	return len(keys)
}

// Any one of many values is reached by typing a part of it, not by stepping through all values before it,
// and the choice is saved like one made with left/right.
func TestThePickerReachesOneOfManyConnectionsDirectly(t *testing.T) {
	m, _, path := newModel(t)
	names := numberedConnections(60)
	seedConnections(t, m, names...)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	openDefaultForm(t, m)
	if got := m.fieldValue("connection"); got != names[0] {
		t.Fatalf("connection = %q, want the first value %q", got, names[0])
	}
	if view := m.View(); !strings.Contains(view, "/ search") {
		t.Errorf("a row of %d values does not point out the picker:\n%s", len(names), view)
	}

	keys := countingPress(t, m, "/")
	if m.screen != screenPicker {
		t.Fatalf("/ on a choice row opened screen %v, want the picker", m.screen)
	}
	view := m.View()
	for _, want := range []string{"Choose connection", "1/60 (60 total)", "current: conn-000", "conn-000  (current)"} {
		if !strings.Contains(view, want) {
			t.Errorf("the picker does not show %q:\n%s", want, view)
		}
	}
	typeText(t, m, "047")
	keys += 3
	view = m.View()
	if !strings.Contains(view, "1/1 (60 total)") || !strings.Contains(view, "> conn-047") {
		t.Errorf("the filtered picker does not select conn-047:\n%s", view)
	}
	keys += countingPress(t, m, "enter")
	if keys >= 10 {
		t.Errorf("choosing took %d keys", keys)
	}
	if m.screen != screenForm {
		t.Fatalf("enter left screen %v, want the form", m.screen)
	}
	if got := m.fieldValue("connection"); got != "conn-047" {
		t.Fatalf("connection = %q, want conn-047", got)
	}

	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("saving reported %q", m.fail)
	}
	saved, err := loadTestConfig(t, path)
	mustNoError(t, err)
	if got := saved.Defaults.Connections["docs"]; got != "conn-047" {
		t.Errorf("stored default = %q, want conn-047", got)
	}
}

// The provider row of a credential lists what the core compiled, however many that is, and a provider taken
// from the picker brings its own secret roles along exactly like one stepped to with left/right.
func TestThePickerReachesOneOfManyProviders(t *testing.T) {
	reg := capability.NewRegistry()
	for i := 0; i < 55; i++ {
		id := fmt.Sprintf("p%03d", i)
		mustNoError(t, reg.RegisterProvider(config.ProviderMetadata{
			ID: id, Name: fmt.Sprintf("Provider %03d", i),
			SecretRoles: []config.SecretRole{{Name: id + "-token", Description: "token"}},
		}, nil))
	}
	store := config.NewStore(filepath.Join(t.TempDir(), "config.yaml"), reg)
	m, err := New(store, nil, nil, &redact.Redactor{})
	mustNoError(t, err)
	// A credential without a provider asks for the roles of all 55, so the form needs a tall terminal.
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 200})
	openSectionByName(t, m, sectionCredentials)
	press(t, m, "n")
	focusField(t, m, providerLabel)
	if got := len(m.fields[m.focus].choices); got != 56 {
		t.Fatalf("provider choices = %d, want the 55 compiled ones and the empty one", got)
	}

	press(t, m, "/")
	typeText(t, m, "P042")
	press(t, m, "enter")
	if got := m.fieldValue(providerLabel); got != "p042" {
		t.Fatalf("provider = %q, want p042", got)
	}
	var roles []string
	for _, f := range m.fields {
		if f.kind == fieldEnvName || f.kind == fieldSecret {
			roles = append(roles, f.label)
		}
	}
	if !reflect.DeepEqual(roles, []string{"p042-token"}) {
		t.Errorf("roles = %v, want only the role of p042", roles)
	}
}

// The filter ignores case, keeps the order of the row and gives the same answer every time.
func TestThePickerFilterIgnoresCaseAndIsDeterministic(t *testing.T) {
	m, _, _ := newModel(t)
	seedConnections(t, m, append(numberedConnections(50), "Wiki-Main", "wiki-archive", "chat")...)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	openDefaultForm(t, m)
	// The row lists its values sorted, so the capitalised name comes first.
	want := []string{"Wiki-Main", "wiki-archive"}

	for _, query := range []string{"WIKI", "wiki", "WiKi"} {
		press(t, m, "/")
		typeText(t, m, query)
		if got := m.picker.matches; !reflect.DeepEqual(got, want) {
			t.Errorf("filter %q shows %v, want %v", query, got, want)
		}
		view := m.View()
		if !strings.Contains(view, "Wiki-Main") || !strings.Contains(view, "wiki-archive") ||
			strings.Contains(view, "chat") || strings.Contains(view, "conn-000") {
			t.Errorf("filter %q draws the wrong values:\n%s", query, view)
		}
		press(t, m, "esc")
	}
}

// Escape leaves the row and every row that depends on it exactly as they were. Enter takes the one value
// that is selected and shown, and a filter that matches nothing takes nothing.
func TestThePickerCancelsCompletelyAndTakesOnlyAShownValue(t *testing.T) {
	m, _, _ := newModel(t)
	seedConnections(t, m, numberedConnections(60)...)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	openDefaultForm(t, m)
	press(t, m, "right", "right")
	before := m.fieldValue("connection")
	if before != "conn-002" {
		t.Fatalf("connection = %q, want conn-002", before)
	}

	press(t, m, "/", "down", "down", "down")
	typeText(t, m, "conn-01")
	press(t, m, "down", "esc")
	if m.screen != screenForm {
		t.Fatalf("esc left screen %v, want the form", m.screen)
	}
	if got := m.fieldValue("connection"); got != before {
		t.Errorf("connection after esc = %q, want %q unchanged", got, before)
	}
	if got := m.fieldValue("domain"); got != "docs" {
		t.Errorf("domain after esc = %q, want the typed docs", got)
	}

	// No match: enter keeps the picker open and changes nothing.
	press(t, m, "/")
	typeText(t, m, "nothing-like-this")
	if view := m.View(); !strings.Contains(view, "No value matches") || !strings.Contains(view, "0/0 (60 total)") {
		t.Errorf("the empty picker does not say so:\n%s", view)
	}
	press(t, m, "enter")
	if m.screen != screenPicker {
		t.Errorf("enter without a match left the picker for screen %v", m.screen)
	}
	press(t, m, "esc")
	if got := m.fieldValue("connection"); got != before {
		t.Errorf("connection after an empty enter = %q, want %q unchanged", got, before)
	}

	// Enter takes the selected value among those shown, and only that one.
	press(t, m, "/")
	typeText(t, m, "conn-04")
	press(t, m, "home", "down", "down")
	view := m.View()
	if !strings.Contains(view, "> conn-042") || strings.Contains(view, "conn-050") {
		t.Fatalf("the picker does not show the selection among the matches:\n%s", view)
	}
	press(t, m, "enter")
	if got := m.fieldValue("connection"); got != "conn-042" {
		t.Errorf("connection = %q, want the selected conn-042", got)
	}
}

// Choosing another provider through the picker narrows the service and credential rows the same way
// left/right does: no service or credential of the previous provider stays selected.
func TestAProviderFromThePickerLeavesNoForeignServiceOrCredential(t *testing.T) {
	reg := capability.NewRegistry()
	for _, register := range []func(*capability.Registry) error{bookstack.Register, telegram.Register} {
		mustNoError(t, register(reg))
	}
	store := config.NewStore(filepath.Join(t.TempDir(), "config.yaml"), reg)
	m, err := New(store, nil, nil, &redact.Redactor{})
	mustNoError(t, err)
	m.cfg.Services["wiki"] = config.Service{Provider: "bookstack", BaseURL: "https://wiki.example.invalid"}
	m.cfg.Services["notifications"] = config.Service{Provider: "telegram", BaseURL: "https://api.telegram.org"}
	m.cfg.Credentials["wiki-reader"] = config.Credential{Provider: "bookstack", Type: config.CredentialTypeKeyring}
	m.cfg.Credentials["bot"] = config.Credential{Provider: "telegram", Type: config.CredentialTypeKeyring}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	openSectionByName(t, m, sectionConnections)
	press(t, m, "n")
	focusField(t, m, providerLabel)
	if m.fieldValue("service") != "wiki" || m.fieldValue("credential") != "wiki-reader" {
		t.Fatalf("the form did not start on BookStack: %q + %q", m.fieldValue("service"), m.fieldValue("credential"))
	}

	press(t, m, "/")
	typeText(t, m, "TELE")
	press(t, m, "enter")
	if got := m.fieldValue(providerLabel); got != "telegram" {
		t.Fatalf("provider = %q, want telegram", got)
	}
	if got := m.field("service").choices; !reflect.DeepEqual(got, []string{"notifications"}) {
		t.Errorf("services = %v, want only the Telegram service", got)
	}
	if got := m.field("credential").choices; !reflect.DeepEqual(got, []string{"bot"}) {
		t.Errorf("credentials = %v, want only the Telegram credential", got)
	}
	if m.fieldValue("service") != "notifications" || m.fieldValue("credential") != "bot" {
		t.Errorf("selected %q + %q, want notifications + bot", m.fieldValue("service"), m.fieldValue("credential"))
	}
	if hint := m.field("target").hint; !strings.Contains(hint, "required for Telegram") {
		t.Errorf("target hint = %q, want the Telegram rule", hint)
	}
}

// A small row keeps its direct way: left/right changes it in place, no dialog opens, and the form does not
// point to a picker it does not need.
func TestASmallChoiceKeepsLeftAndRight(t *testing.T) {
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	openSectionByName(t, m, sectionCredentials)
	press(t, m, "n")
	focusField(t, m, typeLabel)
	before := m.credentialType()
	press(t, m, "right")
	if m.screen != screenForm {
		t.Fatalf("right opened screen %v, want the form", m.screen)
	}
	after := m.credentialType()
	if after == before {
		t.Fatalf("right left the type at %q", after)
	}
	press(t, m, "left")
	if got := m.credentialType(); got != before {
		t.Errorf("left moved to %q, want back to %q", got, before)
	}
	if view := m.View(); strings.Contains(view, "/ search") {
		t.Errorf("a row of %d values points to the picker:\n%s", len(m.fields[m.focus].choices), view)
	}
}

// The picker scrolls inside a small terminal like every list: the selection stays on screen and the resize
// notice never replaces it. A terminal too small even for that leads back to the form, not out of it.
func TestThePickerFitsASmallTerminal(t *testing.T) {
	m, _, _ := newModel(t)
	names := numberedConnections(60)
	seedConnections(t, m, names...)
	// The form is opened in a usual terminal: the empty list before it names the long path of the test
	// directory, which needs more than twelve lines.
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	openDefaultForm(t, m)
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	if view := m.View(); strings.Contains(view, "Resize terminal") {
		t.Fatalf("the form does not fit 40x12:\n%s", view)
	}
	press(t, m, "/")

	check := func(want string) {
		t.Helper()
		view := m.View()
		if strings.Contains(view, "Resize terminal") {
			t.Fatalf("the picker was replaced by the resize notice:\n%s", view)
		}
		assertViewFits(t, view, 40, 12)
		if !strings.Contains(view, "> "+want) {
			t.Fatalf("the selection %q is not on screen:\n%s", want, view)
		}
	}
	check(names[0])
	for i := 1; i < 15; i++ {
		press(t, m, "down")
		check(names[i])
	}
	press(t, m, "end")
	check(names[59])
	press(t, m, "home")
	check(names[0])
	press(t, m, "pgdown")
	check(m.picker.matches[m.picker.cursor])
	press(t, m, "up", "up")
	check(m.picker.matches[m.picker.cursor])

	m.Update(tea.WindowSizeMsg{Width: 20, Height: 6})
	press(t, m, "esc")
	if m.screen != screenForm {
		t.Errorf("esc on the resize notice left screen %v, want the form", m.screen)
	}
	if got := m.fieldValue("connection"); got != names[0] {
		t.Errorf("connection = %q, want %q unchanged", got, names[0])
	}
}
