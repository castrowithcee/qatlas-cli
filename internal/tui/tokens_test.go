package tui

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const tokenPassphrase = "correct horse battery staple"

// newTokenFixture is newApprovalFixture with its one connection ("personal") approved, so it covers
// something as a vorbild, and a vault of the test's own, unlocked with the passphrase, to create tokens in
// before the editor opens, or to check what the editor wrote. The editor itself opens cold over dir, see
// openApprovalModel.
func newTokenFixture(t *testing.T) (*config.Store, string, *vault.Vault) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, cfg, _ := newApprovalFixture(t, dir, tokenPassphrase)
	v := vault.New(dir)
	if _, err := v.Unlock(tokenPassphrase); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	if _, _, err := approval.Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatalf("Approve() = %v", err)
	}
	return store, dir, v
}

// fixtureToken creates a token in the fixture's vault, outside the editor.
func fixtureToken(t *testing.T, v *vault.Vault, name string, expires *time.Time, models ...string) vault.Token {
	t.Helper()
	token, err := v.CreateToken(name, models, expires)
	if err != nil {
		t.Fatalf("CreateToken(%q) = %v", name, err)
	}
	return token
}

// openTokensUnlocked opens the Tokens section of a cold editor and unlocks it the way a person does: enter
// on the locked list, then the passphrase, which starts the admin session too.
func openTokensUnlocked(t *testing.T, m *Model) {
	t.Helper()
	openSectionByName(t, m, sectionTokens)
	pump(t, m, "enter")
	unlockWithAdminPassphrase(t, m, tokenPassphrase)
	if m.screen != screenList || m.section != sectionTokens {
		t.Fatalf("screen after unlocking = %v section %v, want the Tokens list", m.screen, m.section)
	}
}

// assertNoTokenValue fails when a message of the editor carries a token value.
func assertNoTokenValue(t *testing.T, m *Model) {
	t.Helper()
	for _, text := range []string{m.status, m.fail, m.busy} {
		if strings.Contains(text, vault.TokenPrefix) {
			t.Errorf("a message carries a token value: %q", text)
		}
	}
}

// An unencrypted or absent vault has no passphrase and so no agent tokens: the section says so, points at
// Vault, and offers no action; n is refused with the same words.
func TestTokensNeedAnEncryptedVault(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	secrets, _ := newVaultResolver(t, dir)
	m, err := buildModel(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	openSectionByName(t, m, sectionTokens)

	view := m.View()
	for _, want := range []string{"Agent tokens need an encrypted vault.", "Open Vault (5)", "7 Tokens       -"} {
		if !strings.Contains(view, want) {
			t.Errorf("the section does not show %q:\n%s", want, view)
		}
	}
	for _, action := range []string{"n new", "enter show", "x revoke", "/ filter"} {
		if strings.Contains(view, action) {
			t.Errorf("an unencrypted vault's Tokens section offers %q:\n%s", action, view)
		}
	}
	press(t, m, "n")
	if m.screen != screenList || !strings.Contains(m.fail, "Agent tokens need an encrypted vault.") {
		t.Errorf("n = screen %v error %q, want it refused on the list", m.screen, m.fail)
	}
}

// A locked vault's tokens are not listed merely by opening the section; enter unlocks it through the admin
// dialog and the list then shows every token with its vorbilder and expiry, never a value.
func TestTokensWhileLocked(t *testing.T) {
	store, dir, v := newTokenFixture(t)
	token := fixtureToken(t, v, "kunde-a-ci", nil, "personal")
	m := openApprovalModel(t, store, dir)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})

	openSectionByName(t, m, sectionTokens)
	if m.screen == screenAdminAuth {
		t.Fatal("merely opening the locked Tokens section asked for the passphrase")
	}
	view := screenOf(m)
	if !strings.Contains(view, "The vault is locked") || !strings.Contains(view, "enter unlock") {
		t.Errorf("the section does not explain the locked vault with enter as the way out:\n%s", view)
	}
	if strings.Contains(view, "kunde-a-ci") || strings.Contains(view, "x revoke") {
		t.Errorf("the locked section lists a token or offers to revoke one:\n%s", view)
	}

	pump(t, m, "enter")
	unlockWithAdminPassphrase(t, m, tokenPassphrase)
	if !slices.Equal(m.list.all, []string{"kunde-a-ci"}) {
		t.Fatalf("Tokens lists %v after unlocking, want kunde-a-ci", m.list.all)
	}
	view = m.View()
	for _, want := range []string{"kunde-a-ci", "personal", "no expiry", "7 Tokens       1", "n new", "x revoke"} {
		if !strings.Contains(view, want) {
			t.Errorf("the unlocked list does not show %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, token.Value) || strings.Contains(view, "⚠") {
		t.Errorf("the list shows the token's value or a warning for a healthy token:\n%s", view)
	}
}

// n creates a token in an admin session: a name, vorbilder ticked in the picker, and an expiry. The created
// token opens its detail with the value still hidden, and the vault on disk holds exactly what was chosen.
func TestCreatingATokenInTheTUI(t *testing.T) {
	store, dir, _ := newTokenFixture(t)
	m := openApprovalModel(t, store, dir)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	openSectionByName(t, m, sectionTokens)

	pump(t, m, "n")
	unlockWithAdminPassphrase(t, m, tokenPassphrase)
	if m.screen != screenForm {
		t.Fatalf("screen after n and the passphrase = %v, want the token form", m.screen)
	}
	view := screenOf(m)
	for _, want := range []string{"New agent token", "vorbilder", "expires", "each vorbild sets the ceiling",
		"can never change or delete a vorbild connection itself", "F2 create"} {
		if !strings.Contains(view, want) {
			t.Errorf("the token form does not show %q:\n%s", want, view)
		}
	}

	typeText(t, m, "kunde-a-ci")
	press(t, m, "tab")
	if view := screenOf(m); !strings.Contains(view, "enter open") || !strings.Contains(view, "F2 create") {
		t.Errorf("the vorbilder row does not name its keys:\n%s", view)
	}
	press(t, m, "enter")
	if m.screen != screenPicker || !slices.Equal(m.picker.all, []string{"personal"}) {
		t.Fatalf("enter on vorbilder = screen %v choices %v, want the picker of vault connections",
			m.screen, m.picker.all)
	}
	press(t, m, " ", "enter")
	if got := m.fieldValue(vorbilderLabel); got != "personal" {
		t.Fatalf("vorbilder = %q after ticking, want personal", got)
	}
	press(t, m, "tab")
	typeText(t, m, "2099-12-01")
	pump(t, m, "f2")

	if m.fail != "" {
		t.Fatalf("creating the token reported %q", m.fail)
	}
	if m.screen != screenConfirm || m.tokenDetail != "kunde-a-ci" || m.tokenValueShown() {
		t.Errorf("after creating: screen %v detail %q shown %v, want the detail with the value hidden",
			m.screen, m.tokenDetail, m.tokenValueShown())
	}
	if !strings.Contains(m.status, "Created agent token kunde-a-ci") {
		t.Errorf("status = %q, want it to name the created token", m.status)
	}
	assertNoTokenValue(t, m)

	check := vault.New(dir)
	if _, err := check.Unlock(tokenPassphrase); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	tokens, err := check.Tokens()
	if err != nil || len(tokens) != 1 {
		t.Fatalf("Tokens() = %v, %v, want the one created token", tokens, err)
	}
	got := tokens[0]
	want := time.Date(2099, 12, 1, 0, 0, 0, 0, time.Local)
	if got.Name != "kunde-a-ci" || !slices.Equal(got.Models, []string{"personal"}) || got.Expires == nil ||
		!got.Expires.Equal(want) {
		t.Errorf("created token = %s %v %v, want kunde-a-ci [personal] %v", got.Name, got.Models, got.Expires, want)
	}
	if strings.Contains(m.View(), got.Value) {
		t.Error("the detail shows the value of a token nobody asked to reveal")
	}
}

// The form refuses, on itself and with every input kept, what the vault would refuse or what could never
// work: a bad name, no vorbild, an expiry in the past or unreadable, and a name that is taken.
func TestTokenFormRefusesWhatCannotWork(t *testing.T) {
	store, dir, v := newTokenFixture(t)
	fixtureToken(t, v, "taken", nil, "personal")
	m := openApprovalModel(t, store, dir)
	openSectionByName(t, m, sectionTokens)
	pump(t, m, "n")
	unlockWithAdminPassphrase(t, m, tokenPassphrase)

	refused := func(want string) {
		t.Helper()
		pump(t, m, "f2")
		if m.screen != screenForm || !strings.Contains(m.fail, want) {
			t.Errorf("F2 = screen %v error %q, want the form kept and %q", m.screen, m.fail, want)
		}
	}
	refused("agent token name must")
	typeText(t, m, "bad name")
	refused("agent token name must")
	clearField(t, m)
	typeText(t, m, "kunde-b")
	refused("tick at least one vorbild")
	press(t, m, "tab", "enter", " ", "enter", "tab")
	typeText(t, m, "2000-01-01")
	refused("expires lies in the past")
	clearField(t, m)
	typeText(t, m, "soon")
	refused("YYYY-MM-DD")
	clearField(t, m)
	press(t, m, "shift+tab", "shift+tab")
	clearField(t, m)
	typeText(t, m, "taken")
	refused("agent token taken exists already")
	if got := m.fieldValue("name"); got != "taken" || m.fieldValue(vorbilderLabel) != "personal" {
		t.Errorf("the refused form holds name %q vorbilder %q, want every input kept", got,
			m.fieldValue(vorbilderLabel))
	}

	tokens, err := m.secrets.Vault().Tokens()
	if err != nil || len(tokens) != 1 {
		t.Errorf("Tokens() = %d tokens, %v, want only the fixture's one", len(tokens), err)
	}
	assertNoTokenValue(t, m)
}

// The detail shows a token's value only after s, and only while the admin session lasts; once it has lapsed,
// s asks for the passphrase again before showing it.
func TestTokenValueIsShownOnlyOnRequest(t *testing.T) {
	store, dir, v := newTokenFixture(t)
	token := fixtureToken(t, v, "kunde-a-ci", nil, "personal")
	m := openApprovalModel(t, store, dir)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	openTokensUnlocked(t, m)

	pump(t, m, "enter")
	if m.screen != screenConfirm || m.tokenDetail != "kunde-a-ci" {
		t.Fatalf("enter = screen %v detail %q, want the token's detail", m.screen, m.tokenDetail)
	}
	view := screenOf(m)
	if strings.Contains(view, token.Value) || !strings.Contains(view, "●●●●") ||
		!strings.Contains(view, "s reveal/hide") {
		t.Errorf("the detail does not mask the value behind s:\n%s", view)
	}

	pump(t, m, "s")
	if !strings.Contains(screenOf(m), token.Value) {
		t.Errorf("s in an admin session does not show the value:\n%s", screenOf(m))
	}
	assertNoTokenValue(t, m)
	pump(t, m, "s")
	if strings.Contains(screenOf(m), token.Value) {
		t.Error("a second s does not hide the value again")
	}

	pump(t, m, "s")
	m.admin.End()
	if strings.Contains(screenOf(m), token.Value) {
		t.Error("the value stays shown after the admin session lapsed")
	}
	pump(t, m, "s")
	if m.screen != screenAdminAuth || m.adminAuth.locked {
		t.Fatalf("s after the session lapsed = screen %v, want the admin passphrase dialog", m.screen)
	}
	unlockWithAdminPassphrase(t, m, tokenPassphrase)
	if m.screen != screenConfirm || !strings.Contains(screenOf(m), token.Value) {
		t.Errorf("the right passphrase does not show the value on the detail:\n%s", screenOf(m))
	}
	assertNoTokenValue(t, m)

	pump(t, m, "esc")
	if m.screen != screenList || m.tokenReveal {
		t.Errorf("esc = screen %v reveal %v, want the list and the value hidden again", m.screen, m.tokenReveal)
	}
}

// x revokes a token only after asking; n keeps it and returns where the question was asked, y revokes it in
// the vault and returns to the list.
func TestRevokingATokenAsksFirst(t *testing.T) {
	store, dir, v := newTokenFixture(t)
	fixtureToken(t, v, "ci", nil, "personal")
	fixtureToken(t, v, "other", nil, "personal")
	m := openApprovalModel(t, store, dir)
	openTokensUnlocked(t, m)

	press(t, m, "x")
	if m.screen != screenConfirm || !strings.Contains(screenOf(m), "Revoke agent token ci?") {
		t.Fatalf("x on the list = screen %v:\n%s", m.screen, screenOf(m))
	}
	press(t, m, "n")
	if m.screen != screenList || len(m.list.all) != 2 {
		t.Fatalf("n = screen %v list %v, want the list with both tokens", m.screen, m.list.all)
	}

	pump(t, m, "enter", "x")
	if m.tokenRevoke != "ci" {
		t.Fatalf("x on the detail asks about %q, want ci", m.tokenRevoke)
	}
	press(t, m, "esc")
	if m.screen != screenConfirm || m.tokenDetail != "ci" || m.tokenRevoke != "" {
		t.Fatalf("esc on the question = screen %v detail %q, want back on the detail", m.screen, m.tokenDetail)
	}
	pump(t, m, "x", "y")
	if m.fail != "" {
		t.Fatalf("revoking reported %q", m.fail)
	}
	if m.screen != screenList || !slices.Equal(m.list.all, []string{"other"}) ||
		!strings.Contains(m.status, "Revoked agent token ci") {
		t.Errorf("after y: screen %v list %v status %q, want the list without ci", m.screen, m.list.all, m.status)
	}
	tokens, err := m.secrets.Vault().Tokens()
	if err != nil || len(tokens) != 1 || tokens[0].Name != "other" {
		t.Errorf("Tokens() after revoking = %v, %v, want other alone", tokens, err)
	}
}

// A vorbild that covers nothing now, and an expiry that has passed, are named on the list behind the token's
// name and on its detail, symbol and words together, never colour alone; both fit a narrow terminal.
func TestTokenWarningsForLapsedVorbildAndExpiry(t *testing.T) {
	store, dir, v := newTokenFixture(t)
	fixtureToken(t, v, "ci", nil, "gone", "personal")
	past := time.Now().Add(-time.Hour)
	fixtureToken(t, v, "old", &past, "personal")
	m := openApprovalModel(t, store, dir)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	openTokensUnlocked(t, m)

	view := screenOf(m)
	for _, want := range []string{"ci  ⚠ 1 vorbild covers nothing", "old  ⚠ expired"} {
		if !strings.Contains(view, want) {
			t.Errorf("the list does not show %q:\n%s", want, view)
		}
	}

	pump(t, m, "enter")
	view = screenOf(m)
	if !strings.Contains(view, "⚠ gone (renamed, deleted") || !strings.Contains(view, "covers nothing") {
		t.Errorf("the detail does not warn in words about the lapsed vorbild:\n%s", view)
	}
	pump(t, m, "esc", "down", "enter")
	if m.tokenDetail != "old" || !strings.Contains(screenOf(m), "⚠ expired, approves nothing") {
		t.Errorf("the detail of %q does not warn about the expiry:\n%s", m.tokenDetail, screenOf(m))
	}

	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	assertViewFits(t, m.View(), 60, 20)
	pump(t, m, "esc")
	assertViewFits(t, m.View(), 60, 20)
}
