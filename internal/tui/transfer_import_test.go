package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/helptopics"
	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

const testImportWrongPass = "wrong-passphrase-canary"

// importFixture writes an export file through the core, from a source editor of its own, and returns its path.
// The directory name is long, so the path is wrapped in a view the way the long temp paths of macOS are.
func importFixture(t *testing.T, kind exportKind) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), strings.Repeat("long-directory-name-", 2))
	mustNoError(t, os.MkdirAll(dir, 0o700))
	path := filepath.Join(dir, "in.yaml")
	sel := manage.ExportSelection{All: true}
	if kind == exportWithout {
		v, _ := plainExportModel(t)
		_, err := v.m.svc.ExportConfig(sel, path)
		mustNoError(t, err)
		return path
	}
	v, _ := vaultExportModel(t)
	opts := manage.SecretExport{VaultPassphrase: func(string) (string, error) { return testExportVaultPass, nil }}
	if kind == exportUnencrypted {
		opts.Unencrypted = true
	} else {
		opts.ExportPassphrase = func(string) (string, error) { return testExportPass, nil }
	}
	_, err := v.m.svc.ExportConfigWithSecrets(sel, path, opts)
	mustNoError(t, err)
	return path
}

// plainImportTarget is an editor with no vault and an empty configuration.
func plainImportTarget(t *testing.T) *exportViews {
	t.Helper()
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.screen = screenList
	return &exportViews{t: t, m: m}
}

// seedTarget gives the target a service and a credential of the file's names, the service with another URL.
func seedTarget(t *testing.T, m *Model) {
	t.Helper()
	mustNoError(t, m.cfg.SetService("wiki", config.Service{Provider: "bookstack", BaseURL: "https://target.example.invalid"}))
	mustNoError(t, m.cfg.SetCredential("reader", config.Credential{Provider: "bookstack", Type: config.CredentialTypeKeyring}))
	mustNoError(t, m.store.Save(m.cfg))
	syncRevision(t, m)
	m.configExists = true
}

// startImport runs ':import' from the list, answering the admin dialog if there is one.
func (v *exportViews) startImport(adminPassphrase string) {
	v.t.Helper()
	v.pump(":")
	v.type_("import")
	v.pump("enter")
	if v.m.screen == screenAdminAuth {
		v.type_(adminPassphrase)
		v.pump("enter")
	}
	if v.m.screen != screenTransfer || v.m.imp == nil || v.m.imp.step != importFile {
		v.t.Fatalf(":import = screen %v fail %q, want the file question:\n%s", v.m.screen, v.m.fail, screenOf(v.m))
	}
}

// importTo walks the questions up to step. cursor is the mode row; encrypted says the file asks a passphrase.
func (v *exportViews) importTo(step importStep, path string, encrypted bool, cursor int) {
	v.t.Helper()
	steps := []importStep{importFile}
	if encrypted {
		steps = append(steps, importPassphrase)
	}
	steps = append(steps, importMode, importPreview)
	for _, s := range steps {
		if v.m.imp.step != s {
			v.t.Fatalf("at step %v fail %q, want %v:\n%s", v.m.imp.step, v.m.fail, s, screenOf(v.m))
		}
		if s == step {
			return
		}
		switch s {
		case importFile:
			v.typePath(path)
			v.pump("enter")
		case importPassphrase:
			v.type_(testExportPass)
			v.pump("enter")
		case importMode:
			for i := 0; i < cursor; i++ {
				v.pump("down")
			}
			v.pump("enter")
		}
	}
	v.t.Fatalf("step %v is not on the way", step)
}

func (v *exportViews) wantImportStep(step importStep) {
	v.t.Helper()
	if v.m.screen != screenTransfer || v.m.imp == nil || v.m.imp.step != step {
		v.t.Fatalf("screen %v step %+v fail %q, want step %v:\n%s", v.m.screen, v.m.imp, v.m.fail, step, screenOf(v.m))
	}
}

// squashed is a view with every run of blanks and line breaks as one blank, so a sentence the wrap broke reads whole.
func squashed(view string) string { return strings.Join(strings.Fields(view), " ") }

func (v *exportViews) wantContains(want ...string) {
	v.t.Helper()
	view := squashed(screenOf(v.m))
	for _, w := range want {
		if !strings.Contains(view, squashed(w)) {
			v.t.Errorf("the view does not show %q:\n%s", w, view)
		}
	}
}

// vaultImportTarget is an editor over an encrypted vault, unlocked in this process, with an empty configuration
// whose secret store is the vault: a connection that arrives with its secret is open until it is approved.
func vaultImportTarget(t *testing.T) *exportViews {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "qatlas")
	m, store := newEncryptedVaultModel(t, dir, testExportVaultPass, true)
	m.cfg.Defaults.SecretStore = config.CredentialTypeVault
	mustNoError(t, store.Save(m.cfg))
	syncRevision(t, m)
	m.configExists = true
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.screen = screenList
	return &exportViews{t: t, m: m}
}

func importedService(t *testing.T, m *Model, name string) (config.Service, bool) {
	t.Helper()
	cfg, _, err := m.svc.Load()
	if err != nil {
		return config.Service{}, false
	}
	svc, ok := cfg.Services[name]
	return svc, ok
}

func importedConnection(t *testing.T, m *Model, name string) bool {
	t.Helper()
	cfg, _, err := m.svc.Load()
	if err != nil {
		return false
	}
	_, ok := cfg.Connections[name]
	return ok
}

func TestImportCreateWritesOnlyAfterThePreview(t *testing.T) {
	path := importFixture(t, exportWithout)
	v := plainImportTarget(t)
	m := v.m
	v.startImport("")
	v.pump("enter")
	if m.imp.step != importFile || m.fail == "" {
		t.Fatalf("enter on no file = step %v fail %q, want the file question with a reason", m.imp.step, m.fail)
	}
	v.typePath(filepath.Join(filepath.Dir(path), "missing.yaml"))
	v.pump("enter")
	if m.imp.step != importFile || m.fail == "" {
		t.Fatalf("a missing file = step %v fail %q, want it refused at the file question", m.imp.step, m.fail)
	}
	v.typePath(path)
	v.pump("enter")
	v.wantImportStep(importMode)
	v.wantContains("create", "merge", "overwrite")
	v.pump("enter")
	v.wantImportStep(importPreview)
	view := screenOf(m)
	for _, want := range []string{"Import preview", "mode      create", "create    connections docs", "(id ", "services wiki",
		"credentials reader", "3 to create, 0 to replace, 0 to skip", "F2 write"} {
		if !strings.Contains(view, want) {
			t.Errorf("the preview does not show %q:\n%s", want, view)
		}
	}
	if !strings.Contains(unwrapped(view), path) {
		t.Errorf("the preview does not show the whole path %q:\n%s", path, view)
	}
	if importedConnection(t, m, "docs") {
		t.Fatal("the preview changed the configuration")
	}
	v.pump("enter")
	v.wantImportStep(importPreview)
	if importedConnection(t, m, "docs") {
		t.Fatal("enter on the preview wrote")
	}
	v.pump("f2")
	v.wantImportStep(importResult)
	v.wantContains("Import written", "created 3, replaced 0, skipped 0")
	if !importedConnection(t, m, "docs") {
		t.Fatal("the configuration lacks the connection after the write")
	}
	if _, ok := m.cfg.Connections["docs"]; !ok {
		t.Error("the editor's own state was not reloaded")
	}
	v.pump("enter")
	if m.screen != screenList || m.imp != nil || !strings.Contains(m.status, "Imported: created 3") {
		t.Fatalf("enter on the result = screen %v status %q, want the list", m.screen, m.status)
	}
}

func TestImportModesAgainstATargetThatHasTheNames(t *testing.T) {
	path := importFixture(t, exportWithout)
	t.Run("create stops", func(t *testing.T) {
		v := plainImportTarget(t)
		m := v.m
		seedTarget(t, m)
		v.startImport("")
		v.typePath(path)
		v.pump("enter", "enter")
		v.wantImportStep(importMode)
		if !strings.Contains(m.fail, "already has") {
			t.Fatalf("fail = %q, want the names the target has", m.fail)
		}
		if svc, _ := importedService(t, m, "wiki"); svc.BaseURL != "https://target.example.invalid" || importedConnection(t, m, "docs") {
			t.Fatal("the refused import changed the configuration")
		}
		v.pump("down", "enter")
		v.wantImportStep(importPreview)
		v.wantContains("mode      merge")
	})
	t.Run("merge keeps", func(t *testing.T) {
		v := plainImportTarget(t)
		m := v.m
		seedTarget(t, m)
		v.startImport("")
		v.typePath(path)
		v.pump("enter", "down", "enter")
		v.wantImportStep(importPreview)
		v.wantContains("mode      merge", "skip", "create    connections docs", "1 to create, 0 to replace, 2 to skip",
			"uses this installation's")
		if importedConnection(t, m, "docs") {
			t.Fatal("the preview wrote")
		}
		v.pump("f2")
		v.wantImportStep(importResult)
		if svc, _ := importedService(t, m, "wiki"); svc.BaseURL != "https://target.example.invalid" || !importedConnection(t, m, "docs") {
			t.Fatalf("merge result: service %+v, connection docs %v; want the target's service kept and the connection added",
				svc, importedConnection(t, m, "docs"))
		}
	})
	t.Run("overwrite replaces", func(t *testing.T) {
		v := plainImportTarget(t)
		m := v.m
		seedTarget(t, m)
		v.startImport("")
		v.typePath(path)
		v.pump("enter", "down", "down", "enter")
		v.wantImportStep(importPreview)
		v.wantContains("mode      overwrite", "replace", "kept from this installation")
		if svc, _ := importedService(t, m, "wiki"); svc.BaseURL != "https://target.example.invalid" {
			t.Fatal("the preview wrote")
		}
		v.pump("f2")
		v.wantImportStep(importResult)
		if svc, _ := importedService(t, m, "wiki"); svc.BaseURL != "https://wiki.example.invalid" {
			t.Fatalf("service = %+v, want the file's", svc)
		}
		v.wantContains("replaced 2")
	})
	t.Run("nothing to import", func(t *testing.T) {
		v := plainImportTarget(t)
		m := v.m
		v.startImport("")
		v.typePath(path)
		v.pump("enter", "enter", "f2", "enter")
		// The file is now in the target, so merge has nothing to add.
		if m.screen != screenList || !importedConnection(t, m, "docs") {
			t.Fatalf("the first import did not finish: screen %v fail %q", m.screen, m.fail)
		}
		v.startImport("")
		v.typePath(path)
		v.pump("enter", "down", "enter")
		v.wantImportStep(importPreview)
		v.wantContains("nothing to import")
		if strings.Contains(screenOf(m), "F2 write") {
			t.Error("the footer offers F2 although there is nothing to write")
		}
		before, _, _ := m.svc.Load()
		v.pump("f2")
		after, _, _ := m.svc.Load()
		if m.fail == "" || m.imp.step != importPreview || len(before.Connections) != len(after.Connections) {
			t.Fatalf("f2 with nothing to import = step %v fail %q", m.imp.step, m.fail)
		}
	})
}

func TestImportEncryptedFileAsksTheExportPassphrase(t *testing.T) {
	path := importFixture(t, exportEncrypted)
	v := plainImportTarget(t)
	m := v.m
	v.startImport("")
	v.typePath(path)
	v.pump("enter")
	v.wantImportStep(importPassphrase)
	v.type_(testImportWrongPass)
	if view := screenOf(m); strings.Contains(view, testImportWrongPass) {
		t.Fatalf("the passphrase is echoed:\n%s", view)
	}
	v.pump("enter")
	v.wantImportStep(importPassphrase)
	if !strings.Contains(m.fail, "does not decrypt") || m.imp.passphrase != "" || m.imp.input.Value() != "" {
		t.Fatalf("a wrong passphrase = fail %q, want it reported and asked again", m.fail)
	}
	v.pump("enter")
	if !strings.Contains(m.fail, "must not be empty") {
		t.Fatalf("an empty passphrase: fail %q", m.fail)
	}
	if importedConnection(t, m, "docs") {
		t.Fatal("a wrong passphrase changed the configuration")
	}
	v.type_(testExportPass)
	v.pump("enter")
	v.wantImportStep(importMode)
	v.pump("enter")
	v.wantImportStep(importPreview)
	v.wantContains("secret    credential reader brings roles: token-id", "the file was encrypted; its secrets go to the keyring")
	if strings.Contains(screenOf(m), "warning   the file holds secrets in plain text") {
		t.Error("an encrypted file is warned about as plain text")
	}
	v.pump("f2")
	v.wantImportStep(importResult)
	v.wantContains("secrets   written to the keyring for: reader")
	if !importedConnection(t, m, "docs") {
		t.Fatal("the import did not write")
	}
	v.pump("enter")
	if m.screen != screenList || m.imp != nil {
		t.Fatalf("enter on the result = screen %v: an encrypted file asks nothing more", m.screen)
	}
	v.assertHidden(testExportCanary, testExportPass, testExportVaultPass, testImportWrongPass)
}

func TestImportUnencryptedSecretsWarnAndOfferToDeleteTheFile(t *testing.T) {
	for _, tc := range []struct {
		name    string
		answer  string
		deleted bool
		want    string
	}{
		{"y deletes", "y", true, "deleted"},
		{"n keeps", "n", false, "was kept; delete it"},
		{"esc keeps", "esc", false, "was kept; delete it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := importFixture(t, exportUnencrypted)
			v := plainImportTarget(t)
			m := v.m
			v.startImport("")
			v.importTo(importPreview, path, false, 0)
			v.wantContains("warning   the file holds secrets in plain text; its secrets go to the keyring")
			v.pump("f2")
			v.wantImportStep(importResult)
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("the file is gone before the question: %v", err)
			}
			v.pump("enter")
			v.wantImportStep(importDelete)
			v.wantContains("which holds secrets in plain text?", "y delete the file")
			v.pump("enter", "f2", " ")
			v.wantImportStep(importDelete)
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("a key other than y deleted the file: %v", err)
			}
			v.pump(tc.answer)
			v.wantImportStep(importResult)
			v.wantContains(tc.want)
			if _, err := os.Stat(path); (err != nil) != tc.deleted {
				t.Fatalf("file stat error %v, deleted want %v", err, tc.deleted)
			}
			v.pump("enter")
			if m.screen != screenList || m.imp != nil {
				t.Fatalf("enter = screen %v, want the list", m.screen)
			}
			v.assertHidden(testExportCanary, testExportVaultPass)
		})
	}
}

func TestImportApprovalQuestion(t *testing.T) {
	path := importFixture(t, exportEncrypted)
	t.Run("y approves", func(t *testing.T) {
		v := vaultImportTarget(t)
		m := v.m
		v.startImport(testExportVaultPass)
		v.importTo(importPreview, path, true, 0)
		v.pump("f2")
		v.wantImportStep(importResult)
		v.pump("enter")
		v.wantImportStep(importApprove)
		v.wantContains("to approve: docs", "Approve the imported connections now?", "y approve")
		v.pump("enter", "f2")
		v.wantImportStep(importApprove)
		v.pump("y")
		v.wantImportStep(importResult)
		v.wantContains("approved  docs")
		if strings.Contains(screenOf(m), "open      ") {
			t.Errorf("a connection stays open after the approval:\n%s", screenOf(m))
		}
		v.pump("enter")
		if m.screen != screenList || m.imp != nil {
			t.Fatalf("enter = screen %v, want the list", m.screen)
		}
	})
	for _, answer := range []string{"n", "esc"} {
		t.Run(answer+" approves nothing", func(t *testing.T) {
			v := vaultImportTarget(t)
			m := v.m
			v.startImport(testExportVaultPass)
			v.importTo(importPreview, path, true, 0)
			v.pump("f2", "enter")
			v.wantImportStep(importApprove)
			v.pump(answer)
			v.wantImportStep(importResult)
			v.wantContains("open      imported connection docs", "nothing else was approved; approve with 'qatlas vault approve'")
			if strings.Contains(screenOf(m), "approved  ") {
				t.Errorf("a connection was approved:\n%s", screenOf(m))
			}
			v.pump("enter")
			if m.screen != screenList {
				t.Fatalf("enter = screen %v, want the list", m.screen)
			}
		})
	}
	t.Run("no vault", func(t *testing.T) {
		v := plainImportTarget(t)
		v.startImport("")
		v.importTo(importPreview, importFixture(t, exportWithout), false, 0)
		v.pump("f2")
		v.wantContains("new connections need the usual release")
		v.pump("enter")
		if v.m.screen != screenList {
			t.Fatalf("no question without a vault: screen %v", v.m.screen)
		}
	})
}

func TestImportEscGoesOneStepBackAndCancelsWithoutEffect(t *testing.T) {
	for _, tc := range []struct {
		name      string
		kind      exportKind
		encrypted bool
		steps     []importStep // what esc shows, from the preview back to the file
	}{
		{"encrypted", exportEncrypted, true, []importStep{importMode, importPassphrase, importFile}},
		{"plain", exportWithout, false, []importStep{importMode, importFile}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := importFixture(t, tc.kind)
			v := plainImportTarget(t)
			m := v.m
			v.startImport("")
			v.importTo(importPreview, path, tc.encrypted, 0)
			for _, want := range tc.steps {
				v.pump("esc")
				v.wantImportStep(want)
				if want == importPassphrase && (m.imp.input.Value() != "" || m.imp.passphrase != "") {
					t.Fatal("going back to the passphrase kept what was typed")
				}
				if importedConnection(t, m, "docs") {
					t.Fatal("going back changed the configuration")
				}
			}
			if got := m.imp.input.Value(); got != path {
				t.Fatalf("back at the file = %q, want what was typed", got)
			}
			// A file name is typed: leaving the first step asks.
			v.pump("esc")
			if m.screen != screenLeave || !strings.Contains(screenOf(m), "the import is not finished") {
				t.Fatalf("esc on the typed file = screen %v, want the leave question:\n%s", m.screen, screenOf(m))
			}
			v.pump("esc")
			v.wantImportStep(importFile)
			v.pump("esc", "d")
			if m.screen != screenList || m.imp != nil || !strings.Contains(m.status, "nothing was imported") {
				t.Fatalf("d = screen %v status %q, want the list", m.screen, m.status)
			}
			if importedConnection(t, m, "docs") {
				t.Fatal("the cancelled import changed the configuration")
			}
		})
	}
}

func TestImportEscOnAnEmptyFileQuestionCancelsAtOnce(t *testing.T) {
	v := plainImportTarget(t)
	v.startImport("")
	v.pump("esc")
	if v.m.screen != screenList || v.m.imp != nil || !strings.Contains(v.m.status, "nothing was imported") {
		t.Fatalf("esc = screen %v status %q, want the list", v.m.screen, v.m.status)
	}
}

func TestImportLeaveQuestionF2GoesOnAndCtrlCHoldsWhileWriting(t *testing.T) {
	path := importFixture(t, exportWithout)
	v := plainImportTarget(t)
	m := v.m
	v.startImport("")
	v.typePath(path)
	v.pump("esc", "f2")
	v.wantImportStep(importMode)
	v.pump("enter", "ctrl+c")
	if m.screen != screenLeave || !m.leaveQuit || !strings.Contains(screenOf(m), "d discard and quit") ||
		!strings.Contains(screenOf(m), "F2 write") {
		t.Fatalf("ctrl+c on a preview = screen %v, want the question that quits:\n%s", m.screen, screenOf(m))
	}
	v.pump("esc")
	v.wantImportStep(importPreview)
	m.imp.working = "writing"
	_, cmd := m.Update(keyMsg("ctrl+c"))
	if cmd != nil || m.screen != screenTransfer || !strings.Contains(m.status, "Wait until the import is written") {
		t.Fatalf("ctrl+c while writing = screen %v status %q, want it held", m.screen, m.status)
	}
	m.imp.working = "reading"
	_, cmd = m.Update(keyMsg("ctrl+c"))
	if cmd != nil || m.screen != screenTransfer {
		t.Fatalf("ctrl+c while reading = screen %v, want it held", m.screen)
	}
	m.imp.working = ""
	v.pump("f2")
	v.wantImportStep(importResult)
	if m.unsaved() {
		t.Error("a written import counts as unsaved")
	}
}

func TestImportF2GoesOnLikeEnterAndEnterDoesNotWrite(t *testing.T) {
	path := importFixture(t, exportEncrypted)
	v := plainImportTarget(t)
	m := v.m
	v.startImport("")
	v.typePath(path)
	v.pump("f2")
	v.wantImportStep(importPassphrase)
	v.type_(testExportPass)
	v.pump("f2")
	v.wantImportStep(importMode)
	v.pump("down", "f2")
	v.wantImportStep(importPreview)
	v.wantContains("mode      merge")
	v.pump("enter")
	v.wantImportStep(importPreview)
	if importedConnection(t, m, "docs") {
		t.Fatal("enter wrote")
	}
	v.pump("f2")
	v.wantImportStep(importResult)
	v.pump("f2")
	if m.screen != screenList || m.imp != nil {
		t.Fatalf("f2 on the result = screen %v, want the list", m.screen)
	}
}

func TestImportNeedsTheAdminSession(t *testing.T) {
	path := importFixture(t, exportWithout)
	t.Run("no session at the start", func(t *testing.T) {
		v := vaultImportTarget(t)
		m := v.m
		m.endAdminSession()
		v.pump(":")
		v.type_("import")
		v.pump("enter")
		if m.screen != screenAdminAuth || m.imp != nil {
			t.Fatalf(":import without a session = screen %v, want the admin dialog and no import", m.screen)
		}
		v.type_("typo-canary")
		v.pump("enter")
		if m.screen != screenAdminAuth || m.imp != nil {
			t.Fatalf("a wrong passphrase = screen %v", m.screen)
		}
		v.pump("esc")
		if m.screen != screenList || m.imp != nil {
			t.Fatalf("esc on the dialog = screen %v, want the list", m.screen)
		}
		v.assertHidden("typo-canary", testExportVaultPass)
	})
	t.Run("session ended before the write", func(t *testing.T) {
		v := vaultImportTarget(t)
		m := v.m
		v.startImport(testExportVaultPass)
		v.importTo(importPreview, path, false, 0)
		m.endAdminSession()
		v.pump("f2")
		if m.screen != screenAdminAuth {
			t.Fatalf("f2 without a session = screen %v, want the admin dialog", m.screen)
		}
		if importedConnection(t, m, "docs") {
			t.Fatal("the import wrote without the session")
		}
		v.pump("esc")
		v.wantImportStep(importPreview)
		v.pump("f2")
		v.type_(testExportVaultPass)
		v.pump("enter")
		v.wantImportStep(importResult)
		if !importedConnection(t, m, "docs") {
			t.Fatal("the import did not write after the session")
		}
	})
}

func TestImportConflictWritesNothingAndPlansAgain(t *testing.T) {
	path := importFixture(t, exportWithout)
	v := plainImportTarget(t)
	m := v.m
	v.startImport("")
	v.importTo(importPreview, path, false, 0)
	// Another change lands after the plan was made.
	other := newTestConfig(t)
	mustNoError(t, other.SetService("other", config.Service{Provider: "bookstack", BaseURL: "https://other.example.invalid"}))
	mustNoError(t, m.store.Save(other))
	v.pump("f2")
	v.wantImportStep(importMode)
	if !strings.Contains(m.fail, "nothing was written") || !strings.Contains(m.fail, "plan the import again") {
		t.Fatalf("fail = %q, want nothing written and plan again", m.fail)
	}
	if importedConnection(t, m, "docs") {
		t.Fatal("a conflicting import wrote")
	}
	v.pump("enter", "f2")
	v.wantImportStep(importResult)
	if !importedConnection(t, m, "docs") {
		t.Fatal("planning again did not write")
	}
	if _, ok := importedService(t, m, "other"); !ok {
		t.Fatal("the other change was lost")
	}
}

func TestImportOffersAVaultPassphraseForTheFirstSecret(t *testing.T) {
	path := importFixture(t, exportEncrypted)
	dir := filepath.Join(t.TempDir(), "qatlas")
	store := newTestStore(t, filepath.Join(dir, "config.yaml"))
	cfg := newTestConfig(t)
	cfg.Defaults.SecretStore = config.CredentialTypeVault
	mustNoError(t, store.Save(cfg))
	secrets, _ := newVaultResolver(t, dir)
	m, err := buildModel(store, nil, secrets, nil)
	mustNoError(t, err)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.screen = screenList
	v := &exportViews{t: t, m: m}
	v.startImport("")
	v.importTo(importPreview, path, true, 0)
	v.wantContains("its secrets go to the vault")
	v.pump("f2")
	if m.screen != screenVaultOffer {
		t.Fatalf("f2 = screen %v fail %q, want the vault passphrase offer", m.screen, m.fail)
	}
	if importedConnection(t, m, "docs") {
		t.Fatal("the import wrote before the offer was answered")
	}
	v.pump("esc")
	v.wantImportStep(importPreview)
	v.pump("f2")
	v.type_("first-vault-pass")
	v.pump("enter")
	v.type_("first-vault-pass")
	v.pump("enter")
	v.wantImportStep(importResult)
	v.wantContains("secrets   written to the vault for: reader")
	if state, err := secrets.Vault().State(); err != nil || state != vault.StateUnlocked {
		t.Fatalf("vault state = %v, %v; want it encrypted and unlocked by the offer", state, err)
	}
	v.assertHidden(testExportCanary, testExportPass, "first-vault-pass")
}

// The footer names ? and : exactly where they work: not on the two lines that read text.
func TestImportFooterNamesHelpAndCommandsOnlyWhereTheyWork(t *testing.T) {
	path := importFixture(t, exportEncrypted)
	for _, tc := range []struct {
		name string
		step importStep
	}{
		{"file", importFile},
		{"passphrase", importPassphrase},
		{"mode", importMode},
		{"preview", importPreview},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := plainImportTarget(t)
			m := v.m
			v.startImport("")
			v.importTo(tc.step, path, true, 0)
			named := strings.Contains(screenOf(m), "? help")
			if named != m.helpReachable() {
				t.Errorf("footer names ? help = %v, but it works = %v:\n%s", named, m.helpReachable(), screenOf(m))
			}
			before := m.imp.input.Value()
			v.pump("?")
			if named {
				if m.screen != screenHelp {
					t.Fatalf("? = screen %v, want the help", m.screen)
				}
				v.pump("esc", ":")
				if m.screen != screenCommand {
					t.Fatalf(": = screen %v, want the command line", m.screen)
				}
				v.pump("esc")
			} else if m.screen != screenTransfer || m.imp.input.Value() != before+"?" {
				t.Fatalf("? = screen %v, want it typed", m.screen)
			}
			v.wantImportStep(tc.step)
		})
	}
	// After the write: the result and both questions, where nothing is typed.
	t.Run("questions", func(t *testing.T) {
		plain := importFixture(t, exportUnencrypted)
		v := vaultImportTarget(t)
		m := v.m
		v.startImport(testExportVaultPass)
		v.importTo(importPreview, plain, false, 0)
		v.pump("f2")
		for _, step := range []importStep{importResult, importApprove, importDelete} {
			v.wantImportStep(step)
			if view := screenOf(m); !strings.Contains(view, "? help") || !strings.Contains(view, ": cmds") || !m.helpReachable() {
				t.Errorf("step %v does not name ? and : although they work:\n%s", step, view)
			}
			v.pump("?", "esc")
			v.wantImportStep(step)
			if step == importResult {
				v.pump("enter")
			} else if step == importApprove {
				v.pump("n", "enter")
			}
		}
	})
}

func TestImportViewsNeverShowASecretOrAPassphrase(t *testing.T) {
	for _, kind := range []exportKind{exportEncrypted, exportUnencrypted} {
		path := importFixture(t, kind)
		v := vaultImportTarget(t)
		m := v.m
		v.startImport(testExportVaultPass)
		v.typePath(path)
		v.pump("enter")
		if kind == exportEncrypted {
			v.type_(testImportWrongPass)
			v.pump("enter")
			v.type_(testExportPass)
			v.pump("enter")
		}
		v.pump("enter", "down", "up", "f2")
		for i := 0; i < 4 && m.imp != nil; i++ {
			v.pump("y")
			v.pump("enter")
		}
		if m.fail != "" {
			t.Fatalf("fail = %q", m.fail)
		}
		v.assertHidden(testExportCanary, testExportPass, testExportVaultPass, testImportWrongPass)
	}
}

func TestImportIsInTheRegisterAndTheHelp(t *testing.T) {
	m := cmdModel(t)
	var imp *command
	list := m.commands()
	for i := range list {
		if list[i].name == "import" {
			imp = &list[i]
		}
	}
	if imp == nil {
		t.Fatalf("the register %v has no import", commandNames(list))
	}
	for _, from := range []screen{screenNav, screenList} {
		if reason := m.unavailableFrom(*imp, from); reason != "" {
			t.Errorf("import from screen %v is unavailable: %s", from, reason)
		}
	}
	for _, from := range []screen{screenForm, screenTransfer, screenLogs} {
		if reason := m.unavailableFrom(*imp, from); reason == "" {
			t.Errorf("import from screen %v is available", from)
		}
	}
	found := false
	for _, topic := range helptopics.All() {
		found = found || strings.Contains(topic.Text, "The command import")
	}
	if !found {
		t.Error("no help topic describes the import command")
	}
}

func TestImportFromAHomePathReadsTheFileInTheHomeDirectory(t *testing.T) {
	home := tempHome(t)
	fixture := importFixture(t, exportWithout)
	data, err := os.ReadFile(fixture)
	mustNoError(t, err)
	mustNoError(t, os.WriteFile(filepath.Join(home, "in.yaml"), data, 0o600))
	v := plainImportTarget(t)
	v.startImport("")
	v.importTo(importMode, "~"+string(filepath.Separator)+"in.yaml", false, 0)
	if want := filepath.Join(home, "in.yaml"); v.m.imp.path != want {
		t.Fatalf("path = %q, want %q", v.m.imp.path, want)
	}
}

func TestImportFileQuestionSuggestsFilesAndRefusesAMissingOne(t *testing.T) {
	home := tempHome(t)
	treeOf(t, home, "inbox/", "in-a.yaml")
	v := plainImportTarget(t)
	m := v.m
	v.startImport("")
	s := string(filepath.Separator)
	v.typePath("~" + s + "in")
	view := screenOf(m)
	if !strings.Contains(view, "~"+s+"inbox"+s) || !strings.Contains(view, "~"+s+"in-a.yaml") {
		t.Fatalf("directories and files are not suggested:\n%s", view)
	}
	// A marked directory is entered, not taken as the file.
	v.pump("down", "down", "enter")
	if m.imp.step != importFile || m.fail != "" || m.imp.input.Value() != "~"+s+"inbox"+s {
		t.Fatalf("value %q step %v fail %q, want the directory entered", m.imp.input.Value(), m.imp.step, m.fail)
	}
	v.typePath("~" + s + "in")
	v.pump("down", "enter")
	if want := filepath.Join(home, "in-a.yaml"); m.imp.path != want {
		t.Fatalf("path = %q, want the marked file %q", m.imp.path, want)
	}
	v.typePath("~" + s + "missing.yaml")
	v.pump("enter")
	if m.imp.step != importFile || m.fail == "" {
		t.Fatalf("a missing file = step %v fail %q, want it refused", m.imp.step, m.fail)
	}
}
