package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

const (
	longTokenHelp = "Alpha personal access token: classic with read:project plus repo, or fine-grained with " +
		"read access to issues and projects; project changes need project instead of read:project; " +
		"actions reads need Actions: read on a fine-grained token, and dispatches need repo on a classic " +
		"token; deleting the logs of a run needs repo on a classic token, or Actions: write on a " +
		"fine-grained one; PERMISSION-LIST-TAIL"
	shortTokenHelp = "Beta personal API token from Settings, Integrations, Developer; it reaches the whole " +
		"account, so the connection target decides which projects are exposed"
)

// roleCollisionModel builds an editor over two providers that both define a role called "token", with a
// credential for each and one that names no provider.
func roleCollisionModel(t *testing.T) *Model {
	t.Helper()
	reg := capability.NewRegistry()
	// alpha sorts before beta: a lookup by role name alone would answer with alpha's text for both.
	for _, p := range []config.ProviderMetadata{
		{ID: "alpha", Name: "Alpha", SecretRoles: []config.SecretRole{{Name: "token", Description: longTokenHelp}}},
		{ID: "beta", Name: "Beta", SecretRoles: []config.SecretRole{{Name: "token", Description: shortTokenHelp}}},
	} {
		if err := reg.RegisterProvider(p, nil); err != nil {
			t.Fatal(err)
		}
	}
	store := config.NewStore(filepath.Join(t.TempDir(), "config.yaml"), reg)
	cfg := store.New()
	mustNoError(t, cfg.SetCredential("a-cred", config.Credential{Provider: "alpha", Type: config.CredentialTypeEnv,
		Values: map[string]string{"token": "ALPHA_TOKEN"}}))
	mustNoError(t, cfg.SetCredential("b-cred", config.Credential{Provider: "beta", Type: config.CredentialTypeEnv,
		Values: map[string]string{"token": "BETA_TOKEN"}}))
	mustNoError(t, cfg.SetCredential("c-cred", config.Credential{Type: config.CredentialTypeEnv,
		Values: map[string]string{"token": "ANY_TOKEN"}}))
	mustNoError(t, store.Save(cfg))
	m, err := buildModel(store, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return m
}

func focusedHint(t *testing.T, m *Model, credential string) (hint, detail string) {
	t.Helper()
	editEntry(t, m, credential)
	focusField(t, m, "token")
	f := m.fields[m.focus]
	return m.fieldHint(f), m.fieldDetail(f)
}

func TestRoleHelpComesFromTheProviderOfTheCredential(t *testing.T) {
	m := roleCollisionModel(t)

	hint, detail := focusedHint(t, m, "b-cred")
	if !strings.Contains(hint, "Beta personal API token") || !strings.Contains(detail, "decides which projects") ||
		strings.Contains(hint+detail, "Alpha") || strings.Contains(hint+detail, "PERMISSION-LIST-TAIL") {
		t.Errorf("beta credential: hint %q detail %q, want beta's help only", hint, detail)
	}
	press(t, m, "esc")

	hint, detail = focusedHint(t, m, "a-cred")
	if !strings.HasPrefix(hint, "Alpha personal access token") || strings.Contains(hint, "PERMISSION-LIST-TAIL") {
		t.Errorf("alpha credential inline hint = %q, want the short form of alpha's help", hint)
	}
	if !strings.Contains(detail, "PERMISSION-LIST-TAIL") || !strings.Contains(detail, "Actions: write") {
		t.Errorf("alpha credential detail = %q, want the whole help", detail)
	}
	press(t, m, "esc")

	// An ambiguous role of a credential without a provider gets no provider's text.
	hint, detail = focusedHint(t, m, "c-cred")
	if strings.Contains(hint+detail, "Alpha") || strings.Contains(hint+detail, "Beta") {
		t.Errorf("credential without provider: hint %q detail %q, want no provider's help", hint, detail)
	}
}

func TestFocusedFieldHintStandsWholeAndWrapped(t *testing.T) {
	sizes := []struct{ w, h int }{{80, 40}, {75, 40}, {208, 60}, {120, 30}}
	for _, size := range sizes {
		for _, credential := range []string{"a-cred", "b-cred", "c-cred"} {
			m := roleCollisionModel(t)
			m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
			editEntry(t, m, credential)
			focusField(t, m, "token")
			view := screenOf(m)
			if strings.Contains(view, "Resize terminal") {
				t.Fatalf("%dx%d %s: the form was replaced by the resize notice:\n%s", size.w, size.h, credential, view)
			}
			for _, want := range []string{"Edit " + credential, "token", "esc leave"} {
				if !strings.Contains(view, want) {
					t.Errorf("%dx%d %s: view lacks %q:\n%s", size.w, size.h, credential, want, view)
				}
			}
			if strings.Contains(view, "…") || strings.Contains(view, "F1") {
				t.Errorf("%dx%d %s: the hint is cut or names F1:\n%s", size.w, size.h, credential, view)
			}
			if credential == "a-cred" {
				flat := strings.Join(strings.Fields(view), " ")
				if !strings.Contains(flat, "PERMISSION-LIST-TAIL") || !strings.Contains(flat, "Actions: write") {
					t.Errorf("%dx%d: the hint is not whole:\n%s", size.w, size.h, view)
				}
			}
		}
	}
}

func TestF1HasNoEffect(t *testing.T) {
	m := roleCollisionModel(t)
	editEntry(t, m, "a-cred")
	focusField(t, m, "token")
	before := m.focus
	press(t, m, "f1")
	if m.screen != screenForm || m.focus != before {
		t.Errorf("F1: screen %v focus %d, want the form on the same row", m.screen, m.focus)
	}
	if strings.Contains(screenOf(m), "F1") {
		t.Errorf("the form names F1:\n%s", screenOf(m))
	}
	// f1 closes nothing in the help either.
	m.openHelp()
	press(t, m, "f1")
	if m.screen != screenHelp {
		t.Errorf("f1 in the help left it for screen %v", m.screen)
	}
}

// Every form row of every shipped provider keeps the form on screen at the usual terminal sizes: no row,
// whatever its hint says, turns the form into the resize notice, and the keys stay visible.
func TestNoFormRowOfAnyProviderBlocksTheFormAtUsualSizes(t *testing.T) {
	sizes := []struct{ w, h int }{{80, 24}, {75, 40}, {120, 30}, {208, 60}}
	check := func(t *testing.T, m *Model, where string, w, h int) {
		t.Helper()
		for i := range m.fields {
			if m.fields[i].hidden || m.fields[i].readOnly {
				continue
			}
			m.focus = i
			m.applyFocus()
			view := screenOf(m)
			if strings.Contains(view, "Resize terminal") {
				t.Errorf("%s %dx%d row %q: the form was replaced by the resize notice:\n%s",
					where, w, h, m.fields[i].label, view)
				continue
			}
			if !strings.Contains(view, "esc") || !strings.Contains(view, m.fields[i].label) {
				t.Errorf("%s %dx%d row %q: the focused row or the keys are missing:\n%s",
					where, w, h, m.fields[i].label, view)
			}
		}
	}
	for _, size := range sizes {
		reg := providerRegistry(t, 8)
		for _, id := range application.ProviderIDs(reg) {
			m, _ := providerModel(t, reg)
			m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
			m.screen = screenNav
			press(t, m, "c")
			m.wizard.provider = id
			for step := stepService; step <= stepPermissions; step++ {
				m.setupShow(step)
				check(t, m, id+" setup "+setupTitles[step], size.w, size.h)
			}
			for _, s := range []section{sectionServices, sectionCredentials, sectionConnections} {
				m.wizard = nil
				m.screen = screenNav
				openSectionByName(t, m, s)
				pressNew(t, m)
				focusField(t, m, providerLabel)
				previous := m.fields[m.focus].value()
				at := choiceIndex(m.fields[m.focus].choices, id)
				if at < 0 {
					continue
				}
				m.fields[m.focus].index = at
				m.choiceChanged(previous)
				check(t, m, id+" new "+m.section.entry(), size.w, size.h)
			}
		}
	}
}

func choiceIndex(values []string, want string) int {
	for i, v := range values {
		if v == want {
			return i
		}
	}
	return -1
}
