package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const findPassphrase = "correct horse battery staple"

// findScene is an encrypted vault whose connection "personal" is approved and whose credential "reader" has
// its vault entry recorded with the credential's config id, so that renames made behind the editor's back
// can be set up through the packages directly before the editor opens.
type findScene struct {
	store *config.Store
	dir   string
	cfg   *config.Config
	v     *vault.Vault
}

func newFindScene(t *testing.T) *findScene {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "qatlas")
	store, _, _ := newApprovalFixture(t, dir, findPassphrase)
	cfg := mustLoad(t, store)
	v := vault.New(dir)
	if _, err := v.Unlock(findPassphrase); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	id := cfg.Credentials["reader"].ID
	if _, err := v.SetUndoableWithID("reader", id, "token-id", "canary-token-id-71ac", nil); err != nil {
		t.Fatalf("SetUndoableWithID() = %v", err)
	}
	if _, _, err := approval.Approve(context.Background(), cfg, v, nil); err != nil {
		t.Fatalf("Approve() = %v", err)
	}
	return &findScene{store: store, dir: dir, cfg: cfg, v: v}
}

// renameByHand renames the connection or credential in the file the way a person editing it would, leaving
// the vault as it is.
func (s *findScene) renameByHand(t *testing.T, kind, old, name string) {
	t.Helper()
	renamed := s.cfg.Clone()
	var err error
	if kind == "connection" {
		_, err = renamed.RenameConnection(old, name)
	} else {
		_, err = renamed.RenameCredential(old, name)
	}
	mustNoError(t, err)
	mustNoError(t, s.store.Save(renamed))
}

// open builds an editor over the scene and unlocks it through Approvals, which also starts the admin session.
func (s *findScene) open(t *testing.T) *Model {
	t.Helper()
	m := openApprovalModel(t, s.store, s.dir)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	openSectionByName(t, m, sectionApprovals)
	press(t, m, "enter")
	unlockWithAdminPassphrase(t, m, findPassphrase)
	return m
}

// openWithoutAdmin builds an editor whose vault is unlocked in this process but has no admin session.
func (s *findScene) openWithoutAdmin(t *testing.T) *Model {
	t.Helper()
	secrets, _ := newVaultResolver(t, s.dir)
	if _, err := secrets.Vault().Unlock(findPassphrase); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	m, err := buildModel(s.store, nil, secrets, nil)
	mustNoError(t, err)
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	return m
}

func (s *findScene) approvals(t *testing.T) map[string]vault.Approval {
	t.Helper()
	fresh := vault.New(s.dir)
	if _, err := fresh.Unlock(findPassphrase); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	approvals, err := fresh.Approvals()
	mustNoError(t, err)
	return approvals
}

func (s *findScene) hasEntry(t *testing.T, name string) bool {
	t.Helper()
	fresh := vault.New(s.dir)
	if _, err := fresh.Unlock(findPassphrase); err != nil {
		t.Fatalf("Unlock() = %v", err)
	}
	held, err := fresh.HasEntry(name)
	mustNoError(t, err)
	return held
}

func wantInView(t *testing.T, m *Model, what string, wants ...string) {
	t.Helper()
	view := screenOf(m)
	for _, want := range wants {
		if !strings.Contains(view, want) {
			t.Errorf("%s does not show %q:\n%s", what, want, view)
		}
	}
}

func TestHandRenamedConnectionIsNoticedAndAdopted(t *testing.T) {
	s := newFindScene(t)
	s.renameByHand(t, "connection", "personal", "main")
	m := s.open(t)

	wantInView(t, m, "Approvals", "renames found in the vault: 1 renamed by hand", "f renames")
	openSectionByName(t, m, sectionConnections)
	m.list.selectName("main")
	wantInView(t, m, "the entry", "connection main was renamed by hand from personal", "f renames")

	press(t, m, "f")
	if m.screen != screenFindings {
		t.Fatalf("f = screen %v, want the findings:\n%s", m.screen, screenOf(m))
	}
	wantInView(t, m, "the findings", "connection  personal -> main, renamed by hand", "enter adopt")

	press(t, m, "enter")
	if m.screen != screenRename || m.rename == nil || m.rename.plan == nil || !m.rename.plan.Adopt {
		t.Fatalf("enter = screen %v, want the dry run of an adoption:\n%s", m.screen, screenOf(m))
	}
	wantInView(t, m, "the dry run", "Adopt the rename of connection personal to main?", "Dry run", "y adopt",
		"approval")
	if _, ok := s.approvals(t)["personal"]; !ok {
		t.Fatal("the dry run moved the approval")
	}

	press(t, m, "esc")
	if m.screen != screenFindings || m.rename != nil || !strings.Contains(m.status, "nothing was adopted") {
		t.Fatalf("esc = screen %v status %q, want the findings again", m.screen, m.status)
	}
	approvals := s.approvals(t)
	if _, ok := approvals["personal"]; !ok || approvals["main"].ConnectionID != "" {
		t.Fatalf("cancelling changed the approvals: %v", approvals)
	}

	pump(t, m, "enter", "y")
	if m.fail != "" || m.screen != screenList {
		t.Fatalf("confirming = screen %v fail %q", m.screen, m.fail)
	}
	if !strings.Contains(m.status, "Adopted the rename of connection personal to main") {
		t.Errorf("status = %q", m.status)
	}
	approvals = s.approvals(t)
	if _, ok := approvals["personal"]; ok {
		t.Error("the approval is still held under the old name")
	}
	if _, ok := approvals["main"]; !ok || !isApproved(t, s.dir, findPassphrase, m.cfg, "main") {
		t.Errorf("the approval did not follow the rename: %v", approvals)
	}
	if got := m.renameFindings(); len(got) != 0 {
		t.Errorf("findings after adopting = %+v", got)
	}
}

func TestApproveAllKeepsTheApprovalOfAHandRename(t *testing.T) {
	s := newFindScene(t)
	s.renameByHand(t, "connection", "personal", "main")
	m := s.open(t)

	openSectionByName(t, m, sectionApprovals)
	press(t, m, "a")
	if !m.approveAllConfirm {
		t.Fatalf("a did not open the bulk confirmation:\n%s", screenOf(m))
	}
	view := screenOf(m)
	if strings.Contains(view, "stale approval") || !strings.Contains(view, "keeps 1 approval(s) of a rename made by hand") {
		t.Errorf("the confirmation does not tell what stays:\n%s", view)
	}
	pump(t, m, "y")
	if m.fail != "" || strings.Contains(m.status, "stale") {
		t.Fatalf("approving all: fail %q status %q", m.fail, m.status)
	}
	if _, ok := s.approvals(t)["personal"]; !ok {
		t.Error("approving all removed the approval of the hand rename")
	}
	// Main is approved under its own name now, so what stays under the old one is the rest of a rename.
	if got := m.renameFindings(); len(got) != 1 || got[0].to != "personal" || !got[0].leftover {
		t.Errorf("findings after approving all = %+v, want the old approval still found", got)
	}
}

func TestUnfinishedConnectionRenameIsNoticedAndFinished(t *testing.T) {
	s := newFindScene(t)
	id := s.cfg.Connections["personal"].ID
	if _, _, err := s.v.CarryApproval("personal", "main", id); err != nil {
		t.Fatalf("CarryApproval() = %v", err)
	}
	s.renameByHand(t, "connection", "personal", "main")
	m := s.open(t)

	wantInView(t, m, "Approvals", "1 unfinished")
	openSectionByName(t, m, sectionConnections)
	m.list.selectName("main")
	wantInView(t, m, "the entry", "connection main has an unfinished rename to personal")

	press(t, m, "f")
	wantInView(t, m, "the findings", "connection  main -> personal, unfinished", "enter finish")
	if strings.Contains(screenOf(m), "b put back") {
		t.Error("a connection's rename is offered to be put back")
	}
	press(t, m, "b")
	if m.screen != screenFindings || m.rename != nil || m.status == "" {
		t.Fatalf("b = screen %v status %q, want a refusal on the findings", m.screen, m.status)
	}

	pump(t, m, "enter")
	wantInView(t, m, "the dry run", "Rename connection main to personal?")
	pump(t, m, "y")
	if m.fail != "" {
		t.Fatalf("confirming reported %q", m.fail)
	}
	cfg := mustLoad(t, s.store)
	if _, ok := cfg.Connections["personal"]; !ok || len(cfg.Connections) != 1 {
		t.Fatalf("connections after finishing = %v", cfg.Connections)
	}
	if !isApproved(t, s.dir, findPassphrase, cfg, "personal") {
		t.Error("the connection is not approved under the name it was renamed to")
	}
}

func TestHandRenamedCredentialIsNoticedAndAdopted(t *testing.T) {
	s := newFindScene(t)
	s.renameByHand(t, "credential", "reader", "reader2")
	m := s.open(t)

	openSectionByName(t, m, sectionCredentials)
	m.list.selectName("reader2")
	wantInView(t, m, "the entry", "credential reader2 was renamed by hand from reader", "f renames")
	press(t, m, "f")
	wantInView(t, m, "the findings", "credential  reader -> reader2, renamed by hand")

	pump(t, m, "enter")
	wantInView(t, m, "the dry run", "Adopt the rename of credential reader to reader2?", "vault     entry reader -> reader2")
	press(t, m, "esc")
	if !s.hasEntry(t, "reader") || s.hasEntry(t, "reader2") {
		t.Fatal("cancelling moved the vault entry")
	}

	pump(t, m, "enter", "y")
	if m.fail != "" {
		t.Fatalf("confirming reported %q", m.fail)
	}
	if s.hasEntry(t, "reader") || !s.hasEntry(t, "reader2") {
		t.Error("the vault entry did not follow the rename")
	}
	if got := m.renameFindings(); len(got) != 0 {
		t.Errorf("findings after adopting = %+v", got)
	}
}

func TestUnfinishedCredentialRenameIsFinishedOrPutBack(t *testing.T) {
	stop := func(t *testing.T) *findScene {
		s := newFindScene(t)
		_, _, err := s.v.RenameCredential(vault.CredentialRename{From: "reader", To: "reader2",
			CredentialID: s.cfg.Credentials["reader"].ID, Entry: true, Mark: true})
		mustNoError(t, err)
		return s
	}

	t.Run("finish", func(t *testing.T) {
		s := stop(t)
		m := s.open(t)
		openSectionByName(t, m, sectionCredentials)
		m.list.selectName("reader")
		wantInView(t, m, "the entry", "credential reader has an unfinished rename to reader2")
		press(t, m, "f")
		wantInView(t, m, "the findings", "credential  reader -> reader2, unfinished", "enter finish", "b put back")

		pump(t, m, "enter")
		wantInView(t, m, "the dry run", "Rename credential reader to reader2?", "resumes")
		pump(t, m, "y")
		if m.fail != "" {
			t.Fatalf("confirming reported %q", m.fail)
		}
		if _, ok := mustLoad(t, s.store).Credentials["reader2"]; !ok {
			t.Error("the configuration was not renamed")
		}
		if got := m.renameFindings(); len(got) != 0 {
			t.Errorf("findings after finishing = %+v", got)
		}
	})

	t.Run("put back", func(t *testing.T) {
		s := stop(t)
		m := s.open(t)
		openSectionByName(t, m, sectionCredentials)
		m.list.selectName("reader")
		press(t, m, "f")
		pump(t, m, "b")
		if m.screen != screenRename || m.rename == nil || !m.rename.plan.Revert {
			t.Fatalf("b = screen %v, want the dry run of putting back:\n%s", m.screen, screenOf(m))
		}
		wantInView(t, m, "the dry run", "Put credential reader back", "vault     entry reader2 -> reader",
			"config    not changed", "y put back")
		press(t, m, "n")
		if m.screen != screenFindings || !s.hasEntry(t, "reader2") || s.hasEntry(t, "reader") {
			t.Fatalf("n = screen %v, want the findings with the vault untouched", m.screen)
		}

		pump(t, m, "b", "y")
		if m.fail != "" {
			t.Fatalf("confirming reported %q", m.fail)
		}
		if !s.hasEntry(t, "reader") || s.hasEntry(t, "reader2") {
			t.Error("the vault entry was not put back")
		}
		if _, ok := mustLoad(t, s.store).Credentials["reader"]; !ok {
			t.Error("the configuration changed")
		}
		if !strings.Contains(m.status, "Put credential reader back") {
			t.Errorf("status = %q", m.status)
		}
	})
}

func TestAmbiguousFindingHasNoAction(t *testing.T) {
	s := newFindScene(t)
	id := s.cfg.Connections["personal"].ID
	if _, _, err := s.v.CarryApproval("personal", "other", id); err != nil {
		t.Fatalf("CarryApproval() = %v", err)
	}
	s.renameByHand(t, "connection", "personal", "main")
	m := s.open(t)

	openSectionByName(t, m, sectionApprovals)
	wantInView(t, m, "Approvals", "2 ambiguous")
	press(t, m, "f")
	wantInView(t, m, "the findings", "connection  other, ambiguous", "no action here")
	if strings.Contains(screenOf(m), "enter adopt") || strings.Contains(screenOf(m), "enter finish") {
		t.Errorf("an ambiguous finding offers an action:\n%s", screenOf(m))
	}
	press(t, m, "enter", "b")
	if m.screen != screenFindings || m.rename != nil || m.adminAuth != nil {
		t.Fatalf("an ambiguous finding started something: screen %v", m.screen)
	}
	if _, ok := s.approvals(t)["personal"]; !ok {
		t.Error("the approval changed")
	}
}

func TestFindingsNeedTheAdminSession(t *testing.T) {
	s := newFindScene(t)
	s.renameByHand(t, "connection", "personal", "main")
	m := s.openWithoutAdmin(t)

	openSectionByName(t, m, sectionConnections)
	m.list.selectName("main")
	press(t, m, "f")
	if m.screen != screenFindings {
		t.Fatalf("f = screen %v, want the findings (reading needs no admin session)", m.screen)
	}
	press(t, m, "enter")
	if m.screen != screenAdminAuth || m.rename != nil {
		t.Fatalf("enter without an admin session = screen %v, want the admin passphrase", m.screen)
	}
	press(t, m, "esc")
	if m.screen != screenFindings || m.rename != nil {
		t.Fatalf("esc = screen %v, want the findings with nothing planned", m.screen)
	}
	if _, ok := s.approvals(t)["personal"]; !ok {
		t.Error("the approval moved without an admin session")
	}

	press(t, m, "enter")
	unlockWithAdminPassphrase(t, m, findPassphrase)
	if m.screen != screenRename || m.rename == nil || m.rename.plan == nil {
		t.Fatalf("after the passphrase = screen %v, want the dry run", m.screen)
	}
}

func TestFindingsWithNothingFoundOrLocked(t *testing.T) {
	s := newFindScene(t)
	m := s.open(t)
	openSectionByName(t, m, sectionConnections)
	if strings.Contains(screenOf(m), "f renames") {
		t.Errorf("the keys offer f with nothing found:\n%s", screenOf(m))
	}
	press(t, m, "f")
	if m.screen != screenList || !strings.Contains(m.status, "No renames found") {
		t.Errorf("f with nothing found = screen %v status %q", m.screen, m.status)
	}

	locked := openApprovalModel(t, s.store, s.dir)
	openSectionByName(t, locked, sectionConnections)
	press(t, locked, "f")
	if locked.screen != screenList || !strings.Contains(locked.status, "locked") {
		t.Errorf("f with a locked vault = screen %v status %q", locked.screen, locked.status)
	}
}

func TestRenamesCommandOpensTheFindings(t *testing.T) {
	s := newFindScene(t)
	s.renameByHand(t, "connection", "personal", "main")
	m := s.open(t)
	openSectionByName(t, m, sectionConnections)
	press(t, m, ":")
	typeText(t, m, "renames")
	press(t, m, "enter")
	if m.screen != screenFindings {
		t.Fatalf("the renames command = screen %v, want the findings", m.screen)
	}
}
