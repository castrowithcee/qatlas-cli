package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The guided setup shows the base URL of a reused service, in the hint and in the summary, without the
// credentials in it.
func TestSetupShowsAReusedServiceBaseURLWithoutItsCredentials(t *testing.T) {
	for _, service := range []string{"wiki-main", "token-host"} {
		m := rawModel(t)
		// Their values would fail the validation of the setup; they are no part of this check.
		delete(m.cfg.Credentials, "vault-cred")
		delete(m.cfg.Credentials, "env-cred")
		m.screen = screenNav
		m.startSetup()
		if m.screen == screenTemplate {
			press(t, m, "enter")
		}
		press(t, m, "enter")
		if m.wizard == nil || m.wizard.step != stepService {
			t.Fatalf("the setup is at screen %v, want the service step:\n%s", m.screen, screenOf(m))
		}
		f := m.field("service")
		for i, c := range f.choices {
			if c == service {
				f.index = i
			}
		}
		m.setupRefresh()
		hint := f.hint + "\n" + f.detail
		for m.screen != screenSummary {
			before := m.wizard.step
			pump(t, m, "f2")
			if m.wizard.step == before && m.screen != screenSummary {
				t.Fatalf("the setup stays on step %d:\n%s", before, screenOf(m))
			}
		}
		shown := hint + "\n" + ansi.Strip(screenOf(m))
		for _, canary := range []string{canaryURLPass, canaryQuery, canaryUser} {
			if strings.Contains(shown, canary) {
				t.Errorf("%s: the setup shows %q:\n%s", service, canary, shown)
			}
		}
		if !strings.Contains(hint, "***") || !strings.Contains(ansi.Strip(screenOf(m)), "***") {
			t.Errorf("%s: the setup hint or summary lacks the mask:\n%s", service, shown)
		}
	}
}
