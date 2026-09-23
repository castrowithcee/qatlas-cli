package tui

import (
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
	"github.com/castrowithcee/qatlas-cli/internal/provider/github"
	"github.com/castrowithcee/qatlas-cli/internal/provider/lexware"
	"github.com/castrowithcee/qatlas-cli/internal/provider/nextcloud"
	"github.com/castrowithcee/qatlas-cli/internal/provider/seatable"
	"github.com/castrowithcee/qatlas-cli/internal/provider/telegram"
	"github.com/castrowithcee/qatlas-cli/internal/provider/todoist"
	"github.com/castrowithcee/qatlas-cli/internal/provider/twentycrm"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// providerRegistry registers the eight providers this build ships for n == 8, and otherwise n providers
// whose names and IDs differ, so a search can tell the two columns apart.
func providerRegistry(t *testing.T, n int) *capability.Registry {
	t.Helper()
	reg := capability.NewRegistry()
	if n == 8 {
		for _, register := range []func(*capability.Registry) error{bookstack.Register, github.Register,
			lexware.Register, nextcloud.Register, seatable.Register, telegram.Register, todoist.Register,
			twentycrm.Register} {
			mustNoError(t, register(reg))
		}
		return reg
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("p%03d", i)
		mustNoError(t, reg.RegisterProvider(config.ProviderMetadata{
			ID: id, Name: fmt.Sprintf("Provider %03d", i),
			SecretRoles: []config.SecretRole{{Name: id + "-token", Description: "token"}},
		}, nil))
	}
	return reg
}

// providerModel is an editor over reg with one service and one credential for every provider, so the
// provider row of a new connection offers all of them.
func providerModel(t *testing.T, reg *capability.Registry) (*Model, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	m, err := New(config.NewStore(path, reg), nil, nil, &redact.Redactor{})
	mustNoError(t, err)
	for _, id := range m.cfg.Providers() {
		m.cfg.Services["svc-"+id] = config.Service{Provider: id, BaseURL: "https://" + id + ".example.invalid"}
		m.cfg.Credentials["cred-"+id] = config.Credential{Provider: id, Type: config.CredentialTypeKeyring}
	}
	return m, path
}

// openProviderRow opens the form of a new entry in section s and focuses its provider row.
func openProviderRow(t *testing.T, m *Model, s section) {
	t.Helper()
	openSectionByName(t, m, s)
	press(t, m, "n")
	focusField(t, m, providerLabel)
}

// formState is every row of the form with what it holds, offers and says, so a test can tell that nothing
// changed at all.
func formState(m *Model) []string {
	var state []string
	for _, f := range m.fields {
		state = append(state, fmt.Sprintf("%s=%q %v %v %q %v %v", f.label, f.value(), f.choices, f.selected,
			f.hint, f.hidden, f.readOnly))
	}
	return state
}

// Every provider row, in each form and in the guided setup, opens the same table for one, eight and many
// providers: the search line, the select marks, name and ID of every provider, and the full count.
func TestEveryProviderRowOpensTheSameTable(t *testing.T) {
	for _, n := range []int{1, 8, 55} {
		for _, form := range []string{"service", "credential", "connection", "setup"} {
			t.Run(fmt.Sprintf("%d providers/%s", n, form), func(t *testing.T) {
				m, _ := providerModel(t, providerRegistry(t, n))
				// The credential form of a new credential shows the roles of every provider.
				m.Update(tea.WindowSizeMsg{Width: 80, Height: 200})
				total := n
				switch form {
				case "setup":
					m.screen = screenMenu
					press(t, m, "c")
				default:
					openProviderRow(t, m, map[string]section{
						"service": sectionServices, "credential": sectionCredentials,
						"connection": sectionConnections,
					}[form])
					if form == "credential" {
						// A new credential names no provider yet, and that is a value of the row too.
						total++
					}
					before := m.fieldValue(providerLabel)
					view := m.View()
					if !strings.Contains(view, "provider     [ ") || strings.Contains(view, "provider     < ") ||
						!strings.Contains(view, fmt.Sprintf("enter opens the table of all %d providers", n)) {
						t.Fatalf("the provider row does not show that it opens a table:\n%s", view)
					}
					press(t, m, "right", "left", "right")
					if got := m.fieldValue(providerLabel); got != before || m.screen != screenForm {
						t.Fatalf("left/right changed the provider row to %q on screen %v", got, m.screen)
					}
					press(t, m, "enter")
				}
				if m.screen != screenProviders {
					t.Fatalf("screen = %v, want the provider table", m.screen)
				}
				if got := len(m.providers.list.all); got != total {
					t.Fatalf("the table offers %d providers, want %d", got, total)
				}
				view := m.View()
				for _, want := range []string{"hoose provider", fmt.Sprintf("1/%d (%d total)", total, total),
					"search: ", "NAME", "ID", "current: ", "> (*) "} {
					if !strings.Contains(view, want) {
						t.Errorf("the table does not show %q:\n%s", want, view)
					}
				}
				for _, id := range m.cfg.Providers() {
					metadata, _ := m.cfg.ProviderMetadata(id)
					if !strings.Contains(view, metadata.Name) || !strings.Contains(view, " "+id) {
						t.Errorf("the table does not show %s (%s):\n%s", metadata.Name, id, view)
					}
				}
			})
		}
	}
}

// The search looks at the name and the ID of every provider, ignores case, keeps the order of the table,
// and gives the same answer every time.
func TestTheProviderTableSearchesNameAndIDIgnoringCase(t *testing.T) {
	var byID []string
	for i := 40; i < 50; i++ {
		byID = append(byID, fmt.Sprintf("p%03d", i))
	}
	cases := []struct {
		n     int
		query string
		want  []string
	}{
		// "p04" is in no name, which read "Provider 040", only in the IDs.
		{55, "P04", byID},
		{55, "p04", byID},
		// "provider 042" is in no ID, only in a name.
		{55, "PROVIDER 042", []string{"p042"}},
		{55, "pRoViDeR 042", []string{"p042"}},
		{8, "HUB", []string{"github"}},
		{8, "crm", []string{"twentycrm"}},
		{8, "Lexware Office", []string{"lexware"}},
		{8, "TODO", []string{"todoist"}},
	}
	for _, c := range cases {
		m, _ := providerModel(t, providerRegistry(t, c.n))
		m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
		openProviderRow(t, m, sectionServices)
		for round := 0; round < 2; round++ {
			press(t, m, "enter")
			typeText(t, m, c.query)
			if got := m.providers.list.matches; !reflect.DeepEqual(got, c.want) {
				t.Errorf("search %q shows %v, want %v", c.query, got, c.want)
			}
			if view := m.View(); !strings.Contains(view, fmt.Sprintf("1/%d (%d total)", len(c.want), c.n)) {
				t.Errorf("search %q does not count its matches:\n%s", c.query, view)
			}
			press(t, m, "esc")
		}
	}
}

// Escape leaves the provider row and every row that depends on it exactly as they were; enter takes the
// marked provider, and only a shown one, and brings along what that provider defines and nothing of the one
// before.
func TestTheProviderTableTakesOnlyTheMarkedProvider(t *testing.T) {
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
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 60})
	openProviderRow(t, m, sectionConnections)
	if m.fieldValue("service") != "wiki" || m.fieldValue("credential") != "wiki-reader" {
		t.Fatalf("the form did not start on BookStack: %q + %q", m.fieldValue("service"), m.fieldValue("credential"))
	}
	before := formState(m)

	press(t, m, "enter", "down")
	typeText(t, m, "tele")
	press(t, m, "esc")
	if m.screen != screenForm {
		t.Fatalf("esc left screen %v, want the form", m.screen)
	}
	if got := formState(m); !reflect.DeepEqual(got, before) {
		t.Fatalf("esc changed the form:\n%v\nwant\n%v", got, before)
	}

	press(t, m, "enter")
	typeText(t, m, "nothing-like-this")
	if view := m.View(); !strings.Contains(view, "No provider matches") || !strings.Contains(view, "0/0 (2 total)") {
		t.Errorf("the empty table does not say so:\n%s", view)
	}
	press(t, m, "enter")
	if m.screen != screenProviders {
		t.Fatalf("enter without a match left the table for screen %v", m.screen)
	}
	press(t, m, "esc")
	if got := formState(m); !reflect.DeepEqual(got, before) {
		t.Fatalf("an empty enter changed the form:\n%v\nwant\n%v", got, before)
	}

	press(t, m, "enter", "home", "down")
	if view := m.View(); !strings.Contains(view, "> ( ) Telegram") || !strings.Contains(view, "(*) BookStack") {
		t.Fatalf("the table does not mark the selection and the current provider apart:\n%s", view)
	}
	press(t, m, "enter")
	if m.screen != screenForm || m.fieldValue(providerLabel) != "telegram" {
		t.Fatalf("enter left screen %v with provider %q, want the form on telegram", m.screen,
			m.fieldValue(providerLabel))
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
	if got := m.field("permissions").choices; !reflect.DeepEqual(got, m.permissionChoices("telegram")) {
		t.Errorf("permissions = %v, want those of Telegram", got)
	}
	list := m.field(toolListLabel)
	for _, tool := range append(append([]string(nil), list.choices...), list.marked()...) {
		if !strings.HasPrefix(tool, "telegram.") {
			t.Errorf("the tool list still holds %q of another provider", tool)
		}
	}
	if view := m.View(); !strings.Contains(view, "[ Telegram (telegram) ▾ ]") {
		t.Errorf("the provider row does not show the taken provider:\n%s", view)
	}
}

// Taking a provider in the service form puts its default URL in place of the default of the one before,
// and escape leaves the URL as it was.
func TestTheProviderTableUpdatesTheDefaultURLOfAService(t *testing.T) {
	m, _ := providerModel(t, providerRegistry(t, 8))
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	openProviderRow(t, m, sectionServices)
	telegramMetadata, _ := m.cfg.ProviderMetadata("telegram")
	if telegramMetadata.DefaultBaseURL == "" {
		t.Fatal("the test needs a provider with a default URL")
	}
	before := m.fieldValue("base url")

	press(t, m, "enter")
	typeText(t, m, "Telegram")
	press(t, m, "esc")
	if got := m.fieldValue("base url"); got != before || m.fieldValue(providerLabel) != "bookstack" {
		t.Fatalf("esc left provider %q and url %q, want bookstack and %q", m.fieldValue(providerLabel), got, before)
	}
	press(t, m, "enter")
	typeText(t, m, "Telegram")
	press(t, m, "enter")
	if got := m.fieldValue("base url"); got != telegramMetadata.DefaultBaseURL {
		t.Errorf("base url = %q, want the Telegram default %q", got, telegramMetadata.DefaultBaseURL)
	}
}

// The table scrolls inside the smallest terminal the editor supports, with the selection always on screen;
// the resize notice never replaces it, in a form or as the first step of the guided setup.
func TestTheProviderTableFitsASmallTerminal(t *testing.T) {
	m, _ := providerModel(t, providerRegistry(t, 55))
	// The form is opened in a usual terminal: above it stands the long path of the test directory.
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	openProviderRow(t, m, sectionServices)
	press(t, m, "enter")
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})

	check := func(want string) {
		t.Helper()
		view := m.View()
		if strings.Contains(view, "Resize terminal") {
			t.Fatalf("the table was replaced by the resize notice:\n%s", view)
		}
		assertViewFits(t, view, 40, 12)
		for _, part := range []string{"search: ", "NAME", "> ", want} {
			if !strings.Contains(view, part) {
				t.Fatalf("%q is not on screen:\n%s", part, view)
			}
		}
	}
	check("p000")
	for i := 1; i < 12; i++ {
		press(t, m, "down")
		check(fmt.Sprintf("p%03d", i))
	}
	press(t, m, "end")
	check("p054")
	press(t, m, "home")
	check("p000")
	press(t, m, "pgdown")
	check(m.providers.list.matches[m.providers.list.cursor])
	typeText(t, m, "p05")
	check("p050")
	press(t, m, "home", "down", "enter")
	if got := m.fieldValue(providerLabel); got != "p051" {
		t.Errorf("provider = %q, want the marked p051", got)
	}

	m, _ = providerModel(t, providerRegistry(t, 55))
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	press(t, m, "c")
	if m.screen != screenProviders {
		t.Fatalf("the guided setup opened screen %v, want the provider table", m.screen)
	}
	check("p000")
	press(t, m, "end")
	check("p054")
}

// The guided setup chooses its provider in the same table: enter takes the marked provider and goes on,
// going back shows the table on that provider, and escape cancels the setup without writing anything.
func TestGuidedSetupChoosesItsProviderInTheTable(t *testing.T) {
	m, path := providerModel(t, providerRegistry(t, 8))
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	press(t, m, "c")
	if m.screen != screenProviders {
		t.Fatalf("c opened screen %v, want the provider table", m.screen)
	}
	view := m.View()
	for _, want := range []string{"Setup step 1 of 6 · choose provider  1/8 (8 total)", "search: ", "NAME",
		"> (*) BookStack", "( ) Telegram", "telegram", "enter choose and continue"} {
		if !strings.Contains(view, want) {
			t.Errorf("the setup table does not show %q:\n%s", want, view)
		}
	}

	typeText(t, m, "TELE")
	press(t, m, "enter")
	if m.screen != screenForm || m.wizard == nil || m.wizard.step != stepService || m.wizard.provider != "telegram" {
		t.Fatalf("enter did not go on with telegram: screen %v, wizard %+v", m.screen, m.wizard)
	}
	if got := m.field("service").choices; !reflect.DeepEqual(got, []string{"svc-telegram", newService}) {
		t.Errorf("services = %v, want the Telegram service and a new one", got)
	}

	press(t, m, "ctrl+b")
	if m.screen != screenProviders || !strings.Contains(m.View(), "> (*) Telegram") {
		t.Fatalf("going back does not show the table on telegram: screen %v\n%s", m.screen, m.View())
	}
	press(t, m, "esc")
	if m.wizard != nil || m.screen != screenMenu {
		t.Fatalf("esc did not cancel the setup: screen %v", m.screen)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("cancelling the setup wrote the configuration")
	}
}

// A provider taken for a credential brings its own secret roles and drops those of every other provider.
func TestTheProviderTableSetsTheRolesOfACredential(t *testing.T) {
	m, _ := providerModel(t, providerRegistry(t, 55))
	// A credential without a provider asks for the roles of all 55, so the form needs a tall terminal.
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 200})
	openProviderRow(t, m, sectionCredentials)
	press(t, m, "enter")
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

// Todoist joins the table from its registry metadata alone: its row shows the name and the ID, and its
// account wildcard carries the warning its metadata declares, while a project list carries none.
func TestTheProviderTableShowsTodoistAndItsWildcardWarning(t *testing.T) {
	m, _ := providerModel(t, providerRegistry(t, 8))
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
	openProviderRow(t, m, sectionServices)
	press(t, m, "enter")
	typeText(t, m, "todoist")
	if view := m.View(); !strings.Contains(view, "Todoist") || !strings.Contains(view, " todoist") ||
		!strings.Contains(view, "1/1 (8 total)") {
		t.Fatalf("the table does not show Todoist with its ID:\n%s", view)
	}
	press(t, m, "enter")
	if got := m.fieldValue(providerLabel); got != "todoist" {
		t.Fatalf("provider = %q, want todoist", got)
	}

	metadata, _ := m.cfg.ProviderMetadata("todoist")
	m.cfg.Connections["tasks"] = config.Connection{Service: "svc-todoist", Credential: "cred-todoist", Target: "*"}
	m.section, m.screen, m.editing = sectionConnections, screenForm, "tasks"
	m.fields = m.buildFields("tasks")
	if view := m.View(); !strings.Contains(view, "warning: "+metadata.Target.WildcardWarning[:40]) {
		t.Fatalf("the account wildcard shows no warning:\n%s", view)
	}
	m.field("target").input.SetValue("6XGgm6PHrGgMpCFX, 6Jf8VQXxpwv56VQ7")
	if view := m.View(); strings.Contains(view, "warning: ") {
		t.Fatalf("a project list shows a warning:\n%s", view)
	}
	if got := m.permissionChoices("todoist"); !reflect.DeepEqual(got, []string{"default", "read", "create", "update",
		"delete"}) {
		t.Errorf("todoist permissions = %v, want reads and the task and comment changes", got)
	}
}
