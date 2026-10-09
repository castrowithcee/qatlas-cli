package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// newApprovalFixture builds, under dir, a configuration with one service ("wiki"), one vault credential
// ("reader", holding one secret) and one connection ("personal") reading it, in a vault already encrypted
// with passphrase. Every piece is built through the packages directly, never through the editor: an
// approval test needs full control over what is approved and what is not before the editor ever opens,
// which the editor's own auto-approve (the very thing under test) would otherwise get in the way of.
func newApprovalFixture(t *testing.T, dir, passphrase string) (*config.Store, *config.Config, *vault.Vault) {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)

	cfg := newTestConfig(t)
	mustNoError(t, cfg.SetService("wiki", config.Service{
		Provider: "bookstack", BaseURL: "https://wiki.example.invalid",
	}))
	mustNoError(t, cfg.SetCredential("reader", config.Credential{
		Provider: "bookstack", Type: config.CredentialTypeVault,
	}))
	mustNoError(t, cfg.SetConnection("personal", config.Connection{
		Service: "wiki", Credential: "reader", Permissions: []config.Permission{config.PermissionRead},
	}))
	mustNoError(t, store.Save(cfg))

	setup, _ := newVaultResolver(t, dir)
	mustNoError(t, setup.SetVault("reader", "token-id", "canary-token-id-71ac",
		func(string) (string, error) { return passphrase, nil }))
	return store, cfg, setup.Vault()
}

// openApprovalModel builds an editor of its own over store's configuration and a vault resolver of its own
// over dir, cold: neither unlocked nor holding an admin session, the way a freshly started 'qatlas tui'
// would. requireAdmin's own dialog unlocks it and starts the session together in one typed passphrase (see
// unlockWithAdminPassphrase), so a test never has to unlock it first through some other path.
func openApprovalModel(t *testing.T, store *config.Store, dir string) *Model {
	t.Helper()
	secrets, _ := newResolver(t, dir, nil)
	secrets = secrets.WithVault(vault.New(dir), nil)
	m, err := buildModel(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	return m
}

// isApproved reports whether a fresh vault, reading only from disk and unlocked with passphrase, currently
// approves the named connection as cfg configures it now: the actual proof of the whole feature, never a
// guess from a resolver that may or may not itself be unlocked or share a running vault process.
func isApproved(t *testing.T, dir, passphrase string, cfg *config.Config, name string) bool {
	t.Helper()
	resolved, err := cfg.Resolve(name, "")
	if err != nil {
		t.Fatalf("Resolve(%q) = %v", name, err)
	}
	v := vault.New(dir)
	if _, err := v.Unlock(passphrase); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	err = v.CheckApproval(secret.ScopeOf(resolved))
	if err != nil && !errors.Is(err, vault.ErrApprovalRequired) {
		t.Fatalf("CheckApproval(%q) = %v", name, err)
	}
	return err == nil
}

// unlockWithAdminPassphrase answers the admin dialog requireAdmin opened for the first managing action of a
// run, the same way a person does; it works whether the vault started locked ("Unlock vault to continue")
// or merely session-less ("Admin passphrase needed to save"), since both ask for the same typed passphrase.
func unlockWithAdminPassphrase(t *testing.T, m *Model, passphrase string) {
	t.Helper()
	if m.screen != screenAdminAuth {
		t.Fatalf("screen = %v, want the admin passphrase dialog", m.screen)
	}
	typeText(t, m, passphrase)
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("the admin passphrase was refused: %q", m.fail)
	}
}

// Saving a connection's own form in an admin session approves it, whether or not anything about it changed:
// rule (a) of the binding, checked here through the resolver, which is what actually decides whether an
// invoke gets the secret.
func TestSavingAConnectionApprovesIt(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, _, _ := newApprovalFixture(t, dir, passphrase)
	m := openApprovalModel(t, store, dir)

	if isApproved(t, dir, passphrase, m.cfg, "personal") {
		t.Fatal("the fixture's connection is already approved; the test would prove nothing")
	}

	openEntryForm(t, m, sectionConnections, "personal")
	press(t, m, "f2")
	unlockWithAdminPassphrase(t, m, passphrase)

	if !strings.Contains(m.status, "approved") {
		t.Errorf("status = %q, want it to mention the approval", m.status)
	}
	if !isApproved(t, dir, passphrase, m.cfg, "personal") {
		t.Error("saving the connection's own form did not approve it")
	}
}

// A new connection saved in its own form, through the real key sequence of the guided form (not the
// guided setup, covered separately), is approved the same way TestSavingAConnectionApprovesIt already
// checks for the fixture's pre-existing, never-approved one.
func TestNewConnectionSavedInTheFormIsApproved(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, _, _ := newApprovalFixture(t, dir, passphrase)
	m := openApprovalModel(t, store, dir)

	addConnection(t, m, "second", "wiki", "reader")
	unlockWithAdminPassphrase(t, m, passphrase)

	if m.fail != "" {
		t.Fatalf("saving the new connection reported %q", m.fail)
	}
	if !isApproved(t, dir, passphrase, m.cfg, "second") {
		t.Error("a new connection saved in its own form was not approved")
	}
}

// Saving a connection's own form must not approve it in passing when it was already open for a reason the
// form never shows: here its service's base_url changed outside this run, so its origin no longer matches
// what was approved, and nothing on the connection form itself would have let a person notice.
func TestSavingAConnectionDoesNotApproveAHiddenServiceChange(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, cfg, v := newApprovalFixture(t, dir, passphrase)
	if _, _, err := approval.Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatalf("Approve() = %v", err)
	}

	changed := cfg.Clone()
	mustNoError(t, changed.SetService("wiki", config.Service{
		Provider: "bookstack", BaseURL: "https://wiki.example.invalid/moved",
	}))
	mustNoError(t, store.Save(changed))

	m := openApprovalModel(t, store, dir)
	if isApproved(t, dir, passphrase, m.cfg, "personal") {
		t.Fatal("the externally moved connection is still approved; the test would prove nothing")
	}

	openEntryForm(t, m, sectionConnections, "personal")
	press(t, m, "f2")
	unlockWithAdminPassphrase(t, m, passphrase)

	if m.fail != "" {
		t.Fatalf("saving the unchanged connection form reported %q", m.fail)
	}
	if isApproved(t, dir, passphrase, m.cfg, "personal") {
		t.Error("saving the connection's own form approved it despite its service having moved outside the form")
	}
	if !strings.Contains(m.status, "Approvals") {
		t.Errorf("status = %q, want it to point at Approvals for the hidden change", m.status)
	}
}

// Saving a connection's own form does approve a change that was already open, when every open field is one
// the form itself shows and a person saving it could see and review: here its permissions widened outside
// this run.
func TestSavingAConnectionApprovesAVisiblePermissionsChange(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, cfg, v := newApprovalFixture(t, dir, passphrase)
	if _, _, err := approval.Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatalf("Approve() = %v", err)
	}

	changed := cfg.Clone()
	mustNoError(t, changed.SetConnection("personal", config.Connection{
		Service: "wiki", Credential: "reader",
		Permissions: []config.Permission{config.PermissionRead, config.PermissionDelete},
	}))
	mustNoError(t, store.Save(changed))

	m := openApprovalModel(t, store, dir)
	if isApproved(t, dir, passphrase, m.cfg, "personal") {
		t.Fatal("the externally widened connection is still approved; the test would prove nothing")
	}

	openEntryForm(t, m, sectionConnections, "personal")
	press(t, m, "f2")
	unlockWithAdminPassphrase(t, m, passphrase)

	if m.fail != "" {
		t.Fatalf("saving the connection form reported %q", m.fail)
	}
	if !isApproved(t, dir, passphrase, m.cfg, "personal") {
		t.Error("saving the connection's own form did not approve a permissions change the form itself shows")
	}
}

// A connection left open by a change outside this run (simulated here by editing the file directly, the
// way another window or 'qatlas config' would) stays open when a different connection is saved in the same
// admin session: only the Approvals section, or its own form, releases it.
func TestExternallyChangedConnectionStaysOpenAfterSavingAnother(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, cfg, v := newApprovalFixture(t, dir, passphrase)
	mustNoError(t, cfg.SetConnection("other", config.Connection{
		Service: "wiki", Credential: "reader", Permissions: []config.Permission{config.PermissionRead},
	}))
	mustNoError(t, store.Save(cfg))

	// Both connections start approved, as if a previous run had already released them.
	if _, _, err := approval.Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatalf("Approve() = %v", err)
	}

	// "personal" changes outside this run: its permissions now include delete, so its fingerprint no longer
	// matches what was approved.
	changed := cfg.Clone()
	mustNoError(t, changed.SetConnection("personal", config.Connection{
		Service: "wiki", Credential: "reader",
		Permissions: []config.Permission{config.PermissionRead, config.PermissionDelete},
	}))
	mustNoError(t, store.Save(changed))

	m := openApprovalModel(t, store, dir)
	if isApproved(t, dir, passphrase, m.cfg, "personal") {
		t.Fatal("the externally changed connection is still approved; the test would prove nothing")
	}
	if !isApproved(t, dir, passphrase, m.cfg, "other") {
		t.Fatal("the untouched connection is not approved yet; the test would prove nothing")
	}

	openEntryForm(t, m, sectionConnections, "other")
	press(t, m, "f2")
	unlockWithAdminPassphrase(t, m, passphrase)

	if isApproved(t, dir, passphrase, m.cfg, "personal") {
		t.Error("saving a different connection approved one changed outside this run")
	}
	if !isApproved(t, dir, passphrase, m.cfg, "other") {
		t.Error("saving the untouched connection revoked its own approval")
	}
}

// Changing a service's base_url changes the origin every connection of that service resolves, so it opens
// every one of them that was approved before; saving the service in the same admin session approves them
// again, since this very save is what opened them.
func TestServiceChangeApprovesTheConnectionsItOpens(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, cfg, v := newApprovalFixture(t, dir, passphrase)
	if _, _, err := approval.Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatalf("Approve() = %v", err)
	}

	m := openApprovalModel(t, store, dir)
	if !isApproved(t, dir, passphrase, m.cfg, "personal") {
		t.Fatal("the fixture's connection is not approved yet; the test would prove nothing")
	}

	openEntryForm(t, m, sectionServices, "wiki")
	focusField(t, m, "base url")
	clearField(t, m)
	typeText(t, m, "https://wiki.example.invalid/moved")
	press(t, m, "f2")
	unlockWithAdminPassphrase(t, m, passphrase)

	if !strings.Contains(m.status, "approved") {
		t.Errorf("status = %q, want it to mention the approval the base_url change caused", m.status)
	}
	if !isApproved(t, dir, passphrase, m.cfg, "personal") {
		t.Error("the connection the base_url change opened was not approved again")
	}
}

// Deleting a connection removes its approval, so a connection later added again under the same name starts
// open rather than inheriting an approval that no longer describes anything real.
func TestDeletingAConnectionRevokesItsApproval(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, cfg, v := newApprovalFixture(t, dir, passphrase)
	if _, _, err := approval.Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatalf("Approve() = %v", err)
	}

	m := openApprovalModel(t, store, dir)
	openSectionByName(t, m, sectionConnections)
	name, ok := m.selected()
	if !ok || name != "personal" {
		t.Fatalf("selected() = %q, %v, want personal", name, ok)
	}
	press(t, m, "d")
	press(t, m, "y")
	unlockWithAdminPassphrase(t, m, passphrase)

	if m.fail != "" {
		t.Fatalf("deleting the connection reported %q", m.fail)
	}

	// A fresh vault.Vault, reading only from disk, proves the approval is really gone and not merely
	// missing from a cached copy this run happens to hold.
	fresh := vault.New(dir)
	if _, err := fresh.Unlock(passphrase); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	approvals, err := fresh.Approvals()
	if err != nil {
		t.Fatalf("Approvals() = %v", err)
	}
	if _, ok := approvals["personal"]; ok {
		t.Error("the deleted connection's approval is still held")
	}
}

// The bulk approval ('a') also removes a stale approval, left over from a connection removed outside a
// delete this editor itself ran (which already revokes its own), the same way 'qatlas vault approve' with
// no --connection does; it is offered even when nothing at all is open, since there is still the stale
// approval to clean up.
func TestBulkApproveRemovesStaleApprovals(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, cfg, v := newApprovalFixture(t, dir, passphrase)
	mustNoError(t, cfg.SetConnection("other", config.Connection{
		Service: "wiki", Credential: "reader", Permissions: []config.Permission{config.PermissionRead},
	}))
	mustNoError(t, store.Save(cfg))
	if _, _, err := approval.Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatalf("Approve() = %v", err)
	}

	// "other" disappears outside this run (renamed or deleted directly in the file), leaving its approval
	// stale: nothing here revoked it.
	withoutOther := cfg.Clone()
	mustNoError(t, withoutOther.DeleteConnection("other"))
	mustNoError(t, store.Save(withoutOther))

	m := openApprovalModel(t, store, dir)
	// Unlocks the vault and starts the admin session; "personal" stays approved, so nothing is open.
	openEntryForm(t, m, sectionServices, "wiki")
	press(t, m, "f2")
	unlockWithAdminPassphrase(t, m, passphrase)

	openSectionByName(t, m, sectionApprovals)
	if len(m.list.all) != 0 {
		t.Fatalf("Approvals lists %v, want none open", m.list.all)
	}
	press(t, m, "a")
	if !m.approveAllConfirm || m.screen != screenConfirm {
		t.Fatalf("a with nothing open but a stale approval did not open the bulk confirmation: screen %v", m.screen)
	}
	if !strings.Contains(screenOf(m), "stale approval") {
		t.Errorf("the confirmation does not mention the stale approval:\n%s", screenOf(m))
	}
	pump(t, m, "y")
	if m.fail != "" {
		t.Fatalf("bulk approval reported %q", m.fail)
	}
	if !strings.Contains(m.status, "stale") {
		t.Errorf("status = %q, want it to mention the stale approval removed", m.status)
	}

	fresh := vault.New(dir)
	if _, err := fresh.Unlock(passphrase); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	approvals, err := fresh.Approvals()
	if err != nil {
		t.Fatalf("Approvals() = %v", err)
	}
	if _, ok := approvals["other"]; ok {
		t.Error("the stale approval of the deleted connection is still held")
	}
}

// An unencrypted vault is unaffected by any of this: saving a connection never mentions an approval, and the
// resolver never refuses it either, exactly as before this task.
func TestUnencryptedVaultUnaffectedBySaving(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)
	secrets, _ := newVaultResolver(t, dir)

	m, err := buildModel(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	addService(t, m, "wiki", "https://wiki.example.invalid")
	addVaultCredential(t, m, "reader")
	focusRole(t, m, "token-id")
	press(t, m, "enter")
	typeText(t, m, "canary-token-id-9f31")
	pump(t, m, "enter")
	if m.screen == screenVaultOffer {
		// Leaving the offer empty keeps the vault unencrypted, the way declining it on 'qatlas credential
		// set' does.
		pump(t, m, "enter")
	}
	press(t, m, "esc")

	addConnection(t, m, "personal", "wiki", "reader")
	if strings.Contains(m.status, "approved") {
		t.Errorf("status = %q, an unencrypted vault must never mention an approval", m.status)
	}

	resolved, err := m.cfg.Resolve("personal", "")
	if err != nil {
		t.Fatalf("Resolve() = %v", err)
	}
	if err := secrets.Usable(context.Background(), resolved); err != nil {
		t.Errorf("Usable() = %v, want an unencrypted vault to refuse nothing", err)
	}
}

// The Approvals section (6): the list names what changed, its detail approves one connection, and 'a'
// approves every connection currently open after confirming how many.
func TestApprovalsSectionListsDetailsAndApproves(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, cfg, _ := newApprovalFixture(t, dir, passphrase)
	mustNoError(t, cfg.SetConnection("other", config.Connection{
		Service: "wiki", Credential: "reader", Permissions: []config.Permission{config.PermissionRead},
	}))
	mustNoError(t, store.Save(cfg))

	m := openApprovalModel(t, store, dir)
	// Approvals cannot list a locked vault (see TestApprovalsSectionWhileLocked); an unrelated managing
	// action unlocks it first here, exactly as well suited as ctrl+l would be for this fixture.
	openEntryForm(t, m, sectionServices, "wiki")
	press(t, m, "f2")
	unlockWithAdminPassphrase(t, m, passphrase)

	openSectionByName(t, m, sectionApprovals)
	if m.section != sectionApprovals || m.screen != screenList {
		t.Fatalf("opening Approvals = section %v screen %v", m.section, m.screen)
	}
	if len(m.list.all) != 2 {
		t.Fatalf("Approvals lists %v, want both new connections", m.list.all)
	}
	if !strings.Contains(screenOf(m), "new connection, not yet approved") {
		t.Errorf("the list does not describe a new connection:\n%s", screenOf(m))
	}

	// The detail of one, approved on its own.
	m.list.selectName("personal")
	press(t, m, "enter")
	if m.approvalDetail != "personal" || m.screen != screenConfirm {
		t.Fatalf("enter on personal = detail %q screen %v", m.approvalDetail, m.screen)
	}
	detail := screenOf(m)
	if !strings.Contains(detail, "Approve personal?") || !strings.Contains(detail, "credential") {
		t.Errorf("the detail screen does not show the expected fields:\n%s", detail)
	}
	// The admin session from unlocking above is still active, so this runs at once, without asking again;
	// approving itself is still asynchronous (see approveOne), hence pump rather than press.
	pump(t, m, "y")
	if m.fail != "" {
		t.Fatalf("approving personal reported %q", m.fail)
	}
	if !isApproved(t, dir, passphrase, m.cfg, "personal") {
		t.Error("y on the detail screen did not approve personal")
	}
	if m.screen != screenList || m.section != sectionApprovals {
		t.Fatalf("screen after approving = %v section %v, want back on the Approvals list", m.screen, m.section)
	}
	if len(m.list.all) != 1 || m.list.all[0] != "other" {
		t.Errorf("Approvals still lists %v after approving personal, want only other", m.list.all)
	}

	// Bulk approval of what remains, with a confirmation naming the count.
	press(t, m, "a")
	if !m.approveAllConfirm || m.screen != screenConfirm {
		t.Fatalf("a did not open the bulk confirmation: screen %v", m.screen)
	}
	if !strings.Contains(screenOf(m), "Approve 1 connection") {
		t.Errorf("the bulk confirmation does not name the count:\n%s", screenOf(m))
	}
	pump(t, m, "y")
	if m.fail != "" {
		t.Fatalf("approving all reported %q", m.fail)
	}
	if !isApproved(t, dir, passphrase, m.cfg, "other") {
		t.Error("a did not approve the remaining connection")
	}
	if len(m.list.all) != 0 {
		t.Errorf("Approvals still lists %v after approving everything", m.list.all)
	}
	if !strings.Contains(screenOf(m), "No open approvals.") {
		t.Errorf("an empty Approvals section does not say so:\n%s", screenOf(m))
	}
}

// An unencrypted vault has no approvals at all: the section says so, and its sidebar count is "-", never 0.
func TestApprovalsSectionWhileUnencrypted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)
	secrets, _ := newVaultResolver(t, dir) // a vault directory that holds nothing yet: state absent

	m, err := buildModel(store, nil, secrets, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	openSectionByName(t, m, sectionApprovals)
	if !strings.Contains(screenOf(m), "unencrypted") {
		t.Errorf("the section does not explain the unencrypted vault:\n%s", screenOf(m))
	}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	view := m.View()
	if !strings.Contains(view, "6 Approvals    -") && !strings.Contains(view, "6 Approvals  -") {
		t.Errorf("the sidebar does not show '-' for Approvals while unencrypted:\n%s", view)
	}
}

// A locked, encrypted vault cannot be listed merely by opening the section: no passphrase is asked to look,
// only to actually do something. enter on the empty list is that something: it writes nothing of its own,
// only rebuilds the list, but still goes through requireAdmin like any other managing action, so its
// "Unlock vault to continue" dialog unlocks the vault and starts the admin session; the list then shows
// what Pending finds, without anything having been approved along the way.
func TestApprovalsSectionWhileLocked(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, _, _ := newApprovalFixture(t, dir, passphrase)

	m := openApprovalModel(t, store, dir)
	openSectionByName(t, m, sectionApprovals)
	if m.screen == screenAdminAuth || m.adminAuth != nil {
		t.Fatal("merely opening the locked Approvals section asked for the passphrase")
	}
	view := screenOf(m)
	if !strings.Contains(view, "locked") || !strings.Contains(view, "press enter or ctrl+l to unlock") {
		t.Errorf("the section does not explain the locked vault with enter or ctrl+l as the way out:\n%s", view)
	}
	if !strings.Contains(view, "enter unlock") {
		t.Errorf("the footer does not name enter as the key while locked:\n%s", view)
	}

	press(t, m, "enter")
	unlockWithAdminPassphrase(t, m, passphrase)
	if m.screen != screenList || m.section != sectionApprovals {
		t.Fatalf("screen after unlocking = %v section %v, want back on the Approvals list", m.screen, m.section)
	}
	if len(m.list.all) != 1 || m.list.all[0] != "personal" {
		t.Errorf("Approvals lists %v after unlocking, want personal, freshly computed and not yet approved",
			m.list.all)
	}
	if isApproved(t, dir, passphrase, m.cfg, "personal") {
		t.Error("merely unlocking the vault to see the list approved a connection")
	}
}

// Digit 6 opens Approvals from the sidebar or the list, and the narrow navigation line offers it too.
func TestApprovalsNavigation(t *testing.T) {
	m, _, _ := newModel(t)
	m.screen = screenNav
	press(t, m, "6")
	if m.screen != screenList || m.section != sectionApprovals {
		t.Fatalf("6 on the sidebar = screen %v section %v", m.screen, m.section)
	}

	m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	view := m.View()
	if !strings.Contains(view, "6") {
		t.Errorf("the narrow navigation line does not offer digit 6:\n%s", view)
	}
}

// toolsChange is an open change of a connection with 60 tools, 2 of them new, 1 gone, 57 unchanged.
func toolsChange() approval.Change {
	var kept []string
	for i := 0; i < 57; i++ {
		kept = append(kept, "github.issues.operation"+strings.Repeat("x", i%3)+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	added := []string{"github.pullrequestchecks.list", "github.pullrequests.get"}
	removed := []string{"github.repositories.archive"}
	return approval.Change{
		Connection: "gh",
		Fields: []approval.FieldChange{{
			Field: approval.FieldTools, Before: "old", After: "new",
			Added: added, Removed: removed, Kept: kept,
		}},
	}
}

// A list change shows what is new and what falls away first and the rest as a count, which e spells out; a
// view too long for the window scrolls instead of cutting anything, at 80 columns too.
func TestApprovalDetailShowsNewAndRemovedBeforeUnchanged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, _, _ := newApprovalFixture(t, dir, "correct horse battery staple")
	m := openApprovalModel(t, store, dir)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	change := toolsChange()
	title := m.wrapped(titleStyle, "Approve gh?") + "\n\n"

	view := m.approvalChangeView(title, change)
	assertViewFits(t, view, m.width, m.height)
	for _, want := range []string{
		"tools       2 new, 1 removed",
		"+ github.pullrequestchecks.list", "+ github.pullrequests.get", "- github.repositories.archive",
		"unchanged: 57", "e unchanged",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("collapsed view lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "github.issues.operation") {
		t.Errorf("collapsed view lists the unchanged tools:\n%s", view)
	}
	if strings.Index(view, "+ github.pullrequests.get") > strings.Index(view, "unchanged: 57") ||
		strings.Index(view, "- github.repositories.archive") > strings.Index(view, "unchanged: 57") {
		t.Errorf("unchanged comes before new or removed:\n%s", view)
	}
	if !strings.Contains(view, "credential  unchanged") {
		t.Errorf("an unchanged field is not one compact line:\n%s", view)
	}

	m.approvalDetail, m.screen = "gh", screenConfirm
	press(t, m, "e")
	if !m.approvalExpanded {
		t.Fatal("e did not expand the unchanged entries")
	}
	view = m.approvalChangeView(title, change)
	assertViewFits(t, view, m.width, m.height)
	if !strings.Contains(view, "lines 1-") || !strings.Contains(view, "e fold unchanged") {
		t.Errorf("an expanded view that is longer than the window does not scroll:\n%s", view)
	}
	seen := map[string]bool{}
	for step := 0; step < 80 && len(seen) < 57; step++ {
		for _, line := range strings.Split(m.approvalChangeView(title, change), "\n") {
			if name := strings.TrimSpace(line); strings.HasPrefix(name, "github.issues.operation") {
				seen[name] = true
			}
		}
		press(t, m, "pgdown")
	}
	if len(seen) != 57 {
		t.Errorf("scrolling reached %d of 57 unchanged tools", len(seen))
	}
	press(t, m, "e")
	if m.approvalExpanded {
		t.Error("e did not fold the unchanged entries again")
	}
}

// A mode switch stays a before-and-after line, and a new connection shows its whole scope as before.
func TestApprovalDetailKeepsModeSwitchesReadable(t *testing.T) {
	rows := approvalDetailRows(approval.Change{Fields: []approval.FieldChange{
		{Field: approval.FieldTools, Before: "(every tool the permissions allow)", After: "a.b c.d"},
	}})
	if !containsRow(rows, "tools", "(every tool the permissions allow) -> a.b c.d") {
		t.Errorf("mode switch rows:\n%s", strings.Join(rows, "\n"))
	}
	rows = approvalDetailRows(approval.Change{New: true, After: vault.Scope{Tools: []string{"a.b", "c.d"}}})
	if !containsRow(rows, "tools", "a.b c.d") {
		t.Errorf("new connection rows:\n%s", strings.Join(rows, "\n"))
	}
}

// openApprovalDetail opens the Approvals detail of the fixture's connection "personal" on a model whose
// admin session is already running, delivering the origin lookup the way the event loop does.
func openApprovalDetail(t *testing.T, dir, passphrase string, store *config.Store) *Model {
	t.Helper()
	m := openApprovalModel(t, store, dir)
	openEntryForm(t, m, sectionServices, "wiki")
	press(t, m, "f2")
	unlockWithAdminPassphrase(t, m, passphrase)
	openSectionByName(t, m, sectionApprovals)
	m.list.selectName("personal")
	pump(t, m, "enter")
	if m.approvalDetail != "personal" {
		t.Fatalf("detail = %q, want personal", m.approvalDetail)
	}
	return m
}

func TestApprovalDetailNamesTheOrigin(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, _, _ := newApprovalFixture(t, dir, passphrase)

	// Nothing logged: the configuration file was changed outside qatlas.
	detail := screenOf(openApprovalDetail(t, dir, passphrase, store))
	for _, want := range []string{"source", "changed outside qatlas: config.yaml edited directly (last modified ",
		"an agent or another program may have edited the file", "l logs"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail lacks %q:\n%s", want, detail)
		}
	}

	// A change qatlas logged: unsigned here, so shown unverified.
	err := invokelog.New(vault.New(dir).Dir(), 90).Append(invokelog.Fields{Path: "tui", Operation: invokelog.OperationConnectionCreate,
		Connection: "personal", Effect: "create", Result: "success"})
	mustNoError(t, err)
	detail = screenOf(openApprovalDetail(t, dir, passphrase, store))
	if !strings.Contains(detail, "changed in qatlas tui, "+time.Now().Format("2006-01-02")) ||
		!strings.Contains(detail, "(unverified)") || strings.Contains(detail, "outside qatlas") {
		t.Errorf("detail does not name the logged change:\n%s", detail)
	}
}

func TestApprovalDetailLogsKeyOpensTheFilteredLogs(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, _, _ := newApprovalFixture(t, dir, passphrase)
	logger := invokelog.New(vault.New(dir).Dir(), 90)
	for _, connection := range []string{"personal", "elsewhere"} {
		mustNoError(t, logger.Append(invokelog.Fields{Path: "tui", Operation: invokelog.OperationConnectionChange,
			Connection: connection, Effect: "update", Result: "success"}))
	}
	m := openApprovalDetail(t, dir, passphrase, store)
	pump(t, m, "l")
	if m.section != sectionLogs || m.screen != screenLogs || m.approvalDetail != "" {
		t.Fatalf("after l: section %v screen %v detail %q, want the Logs section", m.section, m.screen, m.approvalDetail)
	}
	lv := m.logs
	today := time.Now().Format(logDateLayout)
	if lv.mode != logModeRange || lv.to != today || lv.filters[logFilterConnection] != "personal" {
		t.Fatalf("logs = mode %v to %q filters %v, want a range ending today filtered on personal",
			lv.mode, lv.to, lv.filters)
	}
	view := screenOf(m)
	if !strings.Contains(view, "config.connectio") || !strings.Contains(view, "1/1") || strings.Contains(view, "elsewhere") ||
		!strings.Contains(view, "conn=personal") {
		t.Errorf("the Logs view is not filtered on personal:\n%s", view)
	}
}

func TestApprovalDetailUnreadableLogIsUnknownAndStillApproves(t *testing.T) {
	const passphrase = "correct horse battery staple"
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, _, _ := newApprovalFixture(t, dir, passphrase)
	mustNoError(t, os.WriteFile(filepath.Join(vault.New(dir).Dir(), "logs"), []byte("not a directory"), 0o600))
	m := openApprovalDetail(t, dir, passphrase, store)
	if detail := screenOf(m); !strings.Contains(detail, "origin unknown: the log could not be read") {
		t.Errorf("detail does not say the origin is unknown:\n%s", detail)
	}
	pump(t, m, "y")
	if m.fail != "" || !isApproved(t, dir, passphrase, m.cfg, "personal") {
		t.Errorf("approving with an unreadable log: fail %q", m.fail)
	}
}
