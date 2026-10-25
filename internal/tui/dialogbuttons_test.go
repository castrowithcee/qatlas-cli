package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// openDeleteQuestion opens the delete question of the connection "docs" in a modal-sized terminal.
func openDeleteQuestion(t *testing.T) *Model {
	t.Helper()
	m, _, _ := newModel(t)
	seedConnections(t, m, "docs")
	sizedModel(m, 100, 30)
	openSectionByName(t, m, sectionConnections)
	press(t, m, "d")
	if m.screen != screenConfirm {
		t.Fatalf("screen = %v, want the delete question", m.screen)
	}
	return m
}

func TestClickOnDeleteConfirmsTheQuestion(t *testing.T) {
	m := openDeleteQuestion(t)
	if view := ansi.Strip(m.View()); !strings.Contains(view, "[ Delete ]") || !strings.Contains(view, "[ Cancel ]") {
		t.Fatalf("the question shows no buttons:\n%s", view)
	}
	clickOn(t, m, "Delete ]")
	if _, ok := m.cfg.Connections["docs"]; ok || m.screen == screenConfirm {
		t.Fatalf("the click on Delete left the connection (screen %v)", m.screen)
	}
}

func TestClickOnCancelCancelsTheQuestion(t *testing.T) {
	m := openDeleteQuestion(t)
	clickOn(t, m, "Cancel ]")
	if m.screen != screenList || m.status != tr(tCancelled) {
		t.Fatalf("screen %v status %q after the click on Cancel", m.screen, m.status)
	}
	if _, ok := m.cfg.Connections["docs"]; !ok {
		t.Fatal("the click on Cancel deleted the connection")
	}
}

// Only a question that destroys something draws its answer as the danger button.
func TestOnlyDestructiveQuestionsHaveADangerButton(t *testing.T) {
	restoreRenderer(t)
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Setenv("NO_COLOR", "")
	applyColorScheme("dark", "")
	danger := button(buttonDanger, "Delete", false)
	fill := danger[:strings.Index(danger, "[ Delete")]

	if view := openDeleteQuestion(t).View(); !strings.Contains(view, fill) {
		t.Errorf("the delete question has no danger button:\n%s", view)
	}
	m := openDeleteQuestion(t)
	m.screen, m.vaultLockConfirm, m.vaultLockBack = screenConfirm, true, screenList
	view := m.View()
	if !strings.Contains(ansi.Strip(view), "[ Lock ]") || strings.Contains(view, fill) {
		t.Errorf("the lock question is missing or has a danger button:\n%s", view)
	}
}

func TestDialogsReadWithoutColour(t *testing.T) {
	restoreRenderer(t)
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Setenv("NO_COLOR", "1")
	applyColorScheme("dark", "")

	question := openDeleteQuestion(t)
	input, _ := startServiceForm(t)
	sizedModel(input, 100, 30)
	input.screen, input.secretInput = screenSecret, maskedInput()
	input.secretInput.SetValue("canary-secret")
	for name, m := range map[string]*Model{"question": question, "input": input} {
		view := m.View()
		for _, seq := range []string{"\x1b[3", "\x1b[4", "38;", "48;"} {
			if strings.Contains(view, seq) {
				t.Errorf("%s: NO_COLOR view holds %q", name, seq)
			}
		}
		plain := ansi.Strip(view)
		if name == "question" && !strings.Contains(plain, "[ Delete ]") {
			t.Errorf("the question lacks its buttons:\n%s", plain)
		}
		if name == "input" && (!strings.Contains(plain, "> Store <") || strings.Contains(plain, "canary-secret")) {
			t.Errorf("the input dialog lacks its focused button or shows the secret:\n%s", plain)
		}
	}
}

// The secret field is a surface and shows the mask, never the typed value.
func TestSecretFieldIsMaskedOnItsSurface(t *testing.T) {
	restoreRenderer(t)
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Setenv("NO_COLOR", "")
	applyColorScheme("dark", "")
	m, _ := startServiceForm(t)
	sizedModel(m, 100, 30)
	m.screen, m.secretInput = screenSecret, maskedInput()
	m.secretInput.SetValue("canary-secret")
	view := m.View()
	surface := lipgloss.NewStyle().Background(styles.palette.surface).Render("x")
	if !strings.Contains(view, surface[:strings.Index(surface, "x")]) {
		t.Errorf("the field has no surface:\n%s", view)
	}
	plain := ansi.Strip(view)
	if strings.Contains(plain, "canary-secret") || !strings.Contains(plain, strings.Repeat("*", len("canary-secret"))) {
		t.Errorf("the field does not show the mask only:\n%s", plain)
	}
}

// The passphrase dialog keeps fitting the smallest terminal, where it takes the whole workspace.
func TestPassphraseDialogFitsTheSmallestTerminalWithButtonsInModals(t *testing.T) {
	m, _ := startServiceForm(t)
	m.screen, m.secretInput = screenSecret, maskedInput()
	sizedModel(m, minimumWidth, minimumHeight)
	view := m.View()
	assertViewFits(t, view, minimumWidth, minimumHeight)
	if strings.Contains(view, "Resize") || strings.Contains(view, "[ Cancel ]") {
		t.Errorf("the small terminal shows a modal or a resize hint:\n%s", view)
	}
	sizedModel(m, minimumWidth, minimumHeight-1)
	if !strings.Contains(m.View(), "Resize") {
		t.Error("a terminal below the minimum does not ask for more room")
	}
}
