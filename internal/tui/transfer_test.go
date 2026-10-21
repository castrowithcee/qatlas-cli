package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/helptopics"
)

const (
	testExportCanary    = "export-canary-secret-value"
	testExportPass      = "export-canary-passphrase"
	testExportVaultPass = "export-vault-passphrase"
)

// exportViews collects every view an export test reads, so one check at the end can look for a value
// that must never be drawn.
type exportViews struct {
	t     *testing.T
	m     *Model
	views []string
}

func (v *exportViews) pump(keys ...string) {
	v.t.Helper()
	for _, k := range keys {
		pump(v.t, v.m, k)
		v.views = append(v.views, v.m.View(), screenOf(v.m), v.m.status, v.m.fail)
	}
}

func (v *exportViews) type_(text string) {
	typeText(v.t, v.m, text)
	v.views = append(v.views, v.m.View(), screenOf(v.m))
}

func (v *exportViews) assertHidden(secrets ...string) {
	v.t.Helper()
	for _, view := range v.views {
		for _, s := range secrets {
			if strings.Contains(view, s) {
				v.t.Fatalf("a view shows %q:\n%s", s, view)
			}
		}
	}
}

// plainExportModel is an editor with one connection and no vault.
func plainExportModel(t *testing.T) (*exportViews, string) {
	t.Helper()
	m, _, _ := newModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	seedConnections(t, m, "docs")
	m.startDir = t.TempDir()
	m.screen = screenList
	return &exportViews{t: t, m: m}, filepath.Join(t.TempDir(), "out.yaml")
}

// vaultExportModel is an editor over an encrypted vault, unlocked in this process, that holds a secret of
// the vault credential "reader". It has no admin session yet.
func vaultExportModel(t *testing.T) (*exportViews, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "qatlas")
	m, _ := newEncryptedVaultModel(t, dir, testExportVaultPass, true)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	seedConnections(t, m, "docs")
	mustNoError(t, m.cfg.SetCredential("reader", config.Credential{
		Provider: "bookstack", Type: config.CredentialTypeVault}))
	mustNoError(t, m.store.Save(m.cfg))
	syncRevision(t, m)
	mustNoError(t, m.secrets.SetVault("reader", "token-id", testExportCanary, nil))
	m.startDir = t.TempDir()
	m.screen = screenList
	return &exportViews{t: t, m: m}, filepath.Join(t.TempDir(), "out.yaml")
}

// startExport runs ':export' from the list, answering the admin dialog if there is one.
func (v *exportViews) startExport(adminPassphrase string) {
	v.t.Helper()
	v.pump(":")
	v.type_("export")
	v.pump("enter")
	if v.m.screen == screenAdminAuth {
		v.type_(adminPassphrase)
		v.pump("enter")
	}
	if v.m.screen != screenTransfer || v.m.export == nil || v.m.export.step != exportSelect {
		v.t.Fatalf(":export = screen %v fail %q, want the selection:\n%s", v.m.screen, v.m.fail, screenOf(v.m))
	}
}

func (v *exportViews) typePath(path string) {
	clearField(v.t, v.m)
	v.type_(path)
}

// advance walks the questions up to step. kind picks the mode row; path is the file.
func (v *exportViews) advance(step exportStep, kind exportKind, path string) {
	v.t.Helper()
	steps := []exportStep{exportSelect, exportMode}
	switch kind {
	case exportEncrypted:
		steps = append(steps, exportPassphrase)
	case exportUnencrypted:
		steps = append(steps, exportPlainConfirm)
	}
	steps = append(steps, exportTarget, exportPreview)
	for _, s := range steps {
		if v.m.export.step != s {
			v.t.Fatalf("at step %v, want %v:\n%s", v.m.export.step, s, screenOf(v.m))
		}
		if s == step {
			return
		}
		switch s {
		case exportSelect:
			v.pump(" ", "enter")
		case exportMode:
			for i := 0; i < int(kind); i++ {
				v.pump("down")
			}
			v.pump("enter")
		case exportPassphrase:
			v.type_(testExportPass)
			v.pump("enter")
			v.type_(testExportPass)
			v.pump("enter")
		case exportPlainConfirm:
			v.pump("y")
		case exportTarget:
			v.typePath(path)
			v.pump("enter")
		}
	}
	v.t.Fatalf("step %v is not on the way", step)
}

// unwrapped is a view with its line breaks removed. A long path is wrapped into the width, never cut:
// the wrap only breaks the line, it adds no indent and no filler, so the path reads whole again without them.
func unwrapped(view string) string { return strings.ReplaceAll(view, "\n", "") }

func TestExportWithoutSecretsWritesTheFileAfterThePreview(t *testing.T) {
	v, path := plainExportModel(t)
	// Longer than the room of a line, so the wrap happens here as it does for the long temp paths of macOS
	// and Windows, yet short enough to stay below MAX_PATH on Windows.
	dir := filepath.Join(filepath.Dir(path), strings.Repeat("long-directory-name-", 3))
	mustNoError(t, os.MkdirAll(dir, 0o700))
	path = filepath.Join(dir, "out.yaml")
	m := v.m
	v.startExport("")
	if view := screenOf(m); !strings.Contains(view, "everything") || !strings.Contains(view, "connection  docs") {
		t.Fatalf("the selection does not list what can be taken:\n%s", view)
	}
	v.pump("enter")
	if m.export.step != exportSelect || m.fail == "" {
		t.Fatalf("enter on nothing ticked = step %v fail %q, want the selection with a reason", m.export.step, m.fail)
	}
	v.pump(" ", "enter")
	if view := screenOf(m); m.export.step != exportMode || !strings.Contains(view, "without secrets") {
		t.Fatalf("the mode step is missing:\n%s", view)
	}
	v.pump("enter")
	if m.export.step != exportTarget || !strings.Contains(m.export.input.Value(), exportFileName) {
		t.Fatalf("target step = %v value %q, want the suggestion in the start directory", m.export.step,
			m.export.input.Value())
	}
	v.typePath(path)
	v.pump("enter")
	view := screenOf(m)
	for _, want := range []string{"Export preview", "connections docs", "services", "credentials", "F2 write"} {
		if !strings.Contains(view, want) {
			t.Errorf("the preview does not show %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, path) {
		t.Fatalf("the path fits a line, so this test would not see the wrap:\n%s", view)
	}
	if !strings.Contains(unwrapped(view), path) {
		t.Errorf("the preview does not show the whole path %q:\n%s", path, view)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the preview wrote the file")
	}
	v.pump("f2")
	if m.fail != "" || m.export == nil || m.export.step != exportResult {
		t.Fatalf("f2 = fail %q export %+v, want the result", m.fail, m.export)
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		t.Fatalf("the file is missing: %v", err)
	}
	if view := screenOf(m); !strings.Contains(view, "Export written") || !strings.Contains(view, "without   reader") {
		t.Errorf("the result is wrong:\n%s", view)
	}
	v.pump("enter")
	if m.screen != screenList || m.export != nil || !strings.Contains(m.status, "Exported to") {
		t.Fatalf("enter on the result = screen %v status %q, want the list", m.screen, m.status)
	}
}

func TestExportWithSecretsIsEncryptedAndAsksTheExportPassphraseTwice(t *testing.T) {
	v, path := vaultExportModel(t)
	m := v.m
	v.startExport(testExportVaultPass)
	v.pump(" ", "enter", "down", "enter")
	if m.export.step != exportPassphrase {
		t.Fatalf("step = %v fail %q, want the export passphrase", m.export.step, m.fail)
	}
	v.type_(testExportPass)
	if view := screenOf(m); strings.Contains(view, testExportPass) {
		t.Fatalf("the passphrase is echoed:\n%s", view)
	}
	v.pump("enter")
	if !m.export.confirming {
		t.Fatal("the passphrase was not asked again")
	}
	v.type_("something else")
	v.pump("enter")
	if m.export.step != exportPassphrase || m.export.confirming || !strings.Contains(m.fail, "did not match") {
		t.Fatalf("a different second entry = step %v confirming %v fail %q, want it refused and asked again",
			m.export.step, m.export.confirming, m.fail)
	}
	v.pump("enter")
	if m.export.confirming || !strings.Contains(m.fail, "must not be empty") {
		t.Fatalf("an empty passphrase = confirming %v fail %q, want it refused", m.export.confirming, m.fail)
	}
	v.type_(testExportPass)
	v.pump("enter")
	v.type_(testExportPass)
	v.pump("enter")
	if m.export.step != exportTarget {
		t.Fatalf("step = %v, want the file", m.export.step)
	}
	v.typePath(path)
	v.pump("enter")
	if view := screenOf(m); !strings.Contains(view, "encrypted with the export passphrase") {
		t.Errorf("the preview does not say the file is encrypted:\n%s", view)
	}
	v.pump("f2")
	if m.fail != "" || m.export == nil || m.export.step != exportResult {
		t.Fatalf("f2 = fail %q, want the result", m.fail)
	}
	view := screenOf(m)
	for _, want := range []string{"secret    reader: token-id", "encrypted with the export passphrase"} {
		if !strings.Contains(view, want) {
			t.Errorf("the result does not show %q:\n%s", want, view)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(data), testExportCanary) || !strings.HasPrefix(string(data), "age-encryption.org") {
		t.Fatalf("the file is not an age file without the secret: %v, %.40q", err, data)
	}
	v.pump("enter")
	v.assertHidden(testExportCanary, testExportPass, testExportVaultPass, "something else")
}

func TestExportUnencryptedNeedsItsOwnConfirmationAndWarns(t *testing.T) {
	v, path := vaultExportModel(t)
	m := v.m
	v.startExport(testExportVaultPass)
	v.pump(" ", "enter", "down", "down", "enter")
	if m.export.step != exportPlainConfirm {
		t.Fatalf("step = %v fail %q, want the plain text question", m.export.step, m.fail)
	}
	if view := screenOf(m); !strings.Contains(view, "plain text") || !strings.Contains(view, "warning") {
		t.Fatalf("the question does not warn:\n%s", view)
	}
	v.pump("enter", "f2", " ")
	if m.export.step != exportPlainConfirm {
		t.Fatalf("a key other than y moved to step %v", m.export.step)
	}
	v.pump("n")
	if m.export.step != exportMode {
		t.Fatalf("n = step %v, want the mode again", m.export.step)
	}
	v.pump("enter", "y")
	v.typePath(path)
	v.pump("enter")
	if view := screenOf(m); !strings.Contains(view, "warning   secrets are included in plain text") {
		t.Errorf("the preview does not warn:\n%s", view)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the file exists before the write was confirmed")
	}
	v.pump("f2")
	if m.fail != "" || m.export.step != exportResult {
		t.Fatalf("f2 = fail %q step %v, want the result", m.fail, m.export.step)
	}
	if view := screenOf(m); !strings.Contains(view, "warning   the file holds secrets in plain text") {
		t.Errorf("the result does not warn:\n%s", view)
	}
	if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), testExportCanary) {
		t.Fatalf("the plain file does not hold the secret: %v", err)
	}
	v.assertHidden(testExportCanary, testExportVaultPass)
}

func TestEscGoesOneStepBackAndCancelsOnTheFirstWithoutAFile(t *testing.T) {
	for _, tc := range []struct {
		name  string
		kind  exportKind
		steps []exportStep // what esc shows, from the preview back to the selection
	}{
		{"encrypted", exportEncrypted, []exportStep{exportTarget, exportPassphrase, exportMode, exportSelect}},
		{"unencrypted", exportUnencrypted, []exportStep{exportTarget, exportPlainConfirm, exportMode, exportSelect}},
		{"without", exportWithout, []exportStep{exportTarget, exportMode, exportSelect}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, path := vaultExportModel(t)
			m := v.m
			v.startExport(testExportVaultPass)
			v.advance(exportPreview, tc.kind, path)
			for _, want := range tc.steps {
				v.pump("esc")
				if m.screen != screenTransfer || m.export == nil || m.export.step != want {
					t.Fatalf("esc = screen %v step %v, want step %v", m.screen, m.export, want)
				}
				if want == exportPassphrase && (m.export.input.Value() != "" || m.export.passphrase != "") {
					t.Fatal("going back to the passphrase kept what was typed")
				}
			}
			// Ticks are set: leaving the first step asks, the way setup does.
			v.pump("esc")
			if m.screen != screenLeave || !strings.Contains(screenOf(m), "the export is not finished") {
				t.Fatalf("esc on the ticked selection = screen %v, want the leave question:\n%s", m.screen, screenOf(m))
			}
			v.pump("esc")
			if m.screen != screenTransfer || m.export == nil || m.export.step != exportSelect || m.export.ticked() == 0 {
				t.Fatalf("esc on the question = screen %v, want the selection with its ticks", m.screen)
			}
			v.pump("esc", "d")
			if m.screen != screenList || m.export != nil || !strings.Contains(m.status, "nothing was exported") {
				t.Fatalf("d = screen %v status %q, want the list", m.screen, m.status)
			}
			if _, err := os.Stat(path); err == nil {
				t.Fatal("a file was written")
			}
		})
	}
}

func TestEscOnTheFirstStepWithoutTicksCancelsAtOnce(t *testing.T) {
	v, path := plainExportModel(t)
	m := v.m
	v.startExport("")
	v.pump("esc")
	if m.screen != screenList || m.export != nil || !strings.Contains(m.status, "nothing was exported") {
		t.Fatalf("esc = screen %v status %q, want the list", m.screen, m.status)
	}
	v.startExport("")
	v.pump(" ", " ", "esc")
	if m.screen != screenList || m.export != nil {
		t.Fatalf("esc with every tick removed = screen %v, want the list", m.screen)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a file was written")
	}
}

func TestExportLeaveQuestionF2GoesOnAndCtrlCAsksOnADirtyExport(t *testing.T) {
	v, path := plainExportModel(t)
	m := v.m
	v.startExport("")
	v.pump(" ", "esc", "f2")
	if m.screen != screenTransfer || m.export.step != exportMode {
		t.Fatalf("F2 on the question = screen %v, want the next step", m.screen)
	}
	v.pump("enter")
	v.typePath(path)
	v.pump("enter", "ctrl+c")
	if m.screen != screenLeave || !m.leaveQuit || !strings.Contains(screenOf(m), "d discard and quit") {
		t.Fatalf("ctrl+c on a preview = screen %v, want the question that quits", m.screen)
	}
	v.pump("esc")
	if m.screen != screenTransfer || m.export.step != exportPreview {
		t.Fatalf("esc on the question = screen %v, want the preview", m.screen)
	}
	m.export.writing = true
	_, cmd := m.Update(keyMsg("ctrl+c"))
	if cmd != nil || m.screen != screenTransfer || !strings.Contains(m.status, "Wait until the export is written") {
		t.Fatalf("ctrl+c while writing = screen %v status %q, want it ignored", m.screen, m.status)
	}
	m.export.writing = false
	v.pump("f2")
	if m.export.step != exportResult {
		t.Fatalf("f2 = step %v, want the result", m.export.step)
	}
	if m.unsaved() {
		t.Error("a written export counts as unsaved")
	}
}

func TestF2GoesOnLikeEnterOnEveryStepAndEnterConfirmsTheOneLineDialogs(t *testing.T) {
	v, path := vaultExportModel(t)
	m := v.m
	v.startExport(testExportVaultPass)
	v.pump(" ", "f2", "down", "f2")
	if m.export.step != exportPassphrase {
		t.Fatalf("step = %v, want the passphrase", m.export.step)
	}
	v.type_(testExportPass)
	v.pump("f2")
	v.type_(testExportPass)
	v.pump("f2")
	if m.export.step != exportTarget {
		t.Fatalf("step = %v, want the file", m.export.step)
	}
	v.typePath(path)
	v.pump("f2")
	if m.export.step != exportPreview {
		t.Fatalf("step = %v, want the preview", m.export.step)
	}
	v.pump("enter")
	if m.export.step != exportPreview {
		t.Fatalf("enter on the preview = step %v, it must not write", m.export.step)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("enter wrote the file")
	}
	v.pump("f2")
	if m.export.step != exportResult {
		t.Fatalf("f2 = step %v, want the result", m.export.step)
	}
	v.pump("f2")
	if m.screen != screenList || m.export != nil {
		t.Fatalf("f2 on the result = screen %v, want the list", m.screen)
	}
}

// The footer names ? and : exactly where they work: not on the two lines that read text.
func TestExportFooterNamesHelpAndCommandsOnlyWhereTheyWork(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind exportKind
		step exportStep
	}{
		{"select", exportEncrypted, exportSelect},
		{"mode", exportEncrypted, exportMode},
		{"passphrase", exportEncrypted, exportPassphrase},
		{"plain question", exportUnencrypted, exportPlainConfirm},
		{"target", exportEncrypted, exportTarget},
		{"preview", exportEncrypted, exportPreview},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, path := vaultExportModel(t)
			m := v.m
			v.startExport(testExportVaultPass)
			v.advance(tc.step, tc.kind, path)
			named := strings.Contains(screenOf(m), "? help")
			if named != m.helpReachable() {
				t.Errorf("footer names ? help = %v, but it works = %v:\n%s", named, m.helpReachable(), screenOf(m))
			}
			before := m.export.input.Value()
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
			} else if m.screen != screenTransfer || m.export.input.Value() != before+"?" {
				t.Fatalf("? = screen %v, want it typed", m.screen)
			}
			if m.screen != screenTransfer || m.export == nil || m.export.step != tc.step {
				t.Fatalf("back on screen %v, want step %v", m.screen, tc.step)
			}
		})
	}
}

func TestExportNeverReplacesAnExistingFile(t *testing.T) {
	v, path := plainExportModel(t)
	m := v.m
	mustNoError(t, os.WriteFile(path, []byte("keep"), 0o600))
	v.startExport("")
	v.advance(exportPreview, exportWithout, path)
	v.pump("f2")
	if m.export == nil || m.export.step != exportTarget || !strings.Contains(m.fail, "already exists") {
		t.Fatalf("f2 on an existing file = fail %q, want it refused at the file question", m.fail)
	}
	if data, _ := os.ReadFile(path); string(data) != "keep" {
		t.Fatalf("the existing file now holds %q", data)
	}
	other := filepath.Join(filepath.Dir(path), "other.yaml")
	v.typePath(other)
	v.pump("enter", "f2")
	if m.export.step != exportResult {
		t.Fatalf("another name = step %v fail %q, want the result", m.export.step, m.fail)
	}
}

func TestExportWithSecretsNeedsAVaultPassphraseAndTheAdminSession(t *testing.T) {
	t.Run("no passphrase on the vault", func(t *testing.T) {
		v, path := plainExportModel(t)
		m := v.m
		v.startExport("")
		v.pump(" ", "enter", "down", "enter")
		if m.export.step != exportMode || !strings.Contains(m.fail, "no passphrase") {
			t.Fatalf("with secrets = step %v fail %q, want it refused with the reason", m.export.step, m.fail)
		}
		if view := screenOf(m); !strings.Contains(view, "(not possible)") {
			t.Errorf("the mode step does not mark the rows:\n%s", view)
		}
		v.pump("down", "enter")
		if m.export.step != exportMode {
			t.Fatalf("unencrypted without a passphrase = step %v", m.export.step)
		}
		if _, err := os.Stat(path); err == nil {
			t.Fatal("a file was written")
		}
	})
	t.Run("session ended", func(t *testing.T) {
		v, path := vaultExportModel(t)
		m := v.m
		v.startExport(testExportVaultPass)
		v.pump(" ", "enter")
		m.endAdminSession()
		v.pump("down", "enter")
		if m.screen != screenAdminAuth || m.export.step != exportMode {
			t.Fatalf("with secrets without a session = screen %v step %v, want the admin dialog", m.screen, m.export.step)
		}
		v.type_("typo-canary")
		v.pump("enter")
		if m.screen != screenAdminAuth || m.export.step != exportMode {
			t.Fatalf("a wrong passphrase = screen %v step %v", m.screen, m.export.step)
		}
		v.pump("esc")
		if m.screen != screenTransfer || m.export == nil || m.export.step != exportMode {
			t.Fatalf("esc on the dialog = screen %v, want the mode step", m.screen)
		}
		v.pump("enter")
		v.type_(testExportVaultPass)
		v.pump("enter")
		if m.export.step != exportPassphrase {
			t.Fatalf("the right passphrase = step %v fail %q", m.export.step, m.fail)
		}
		if _, err := os.Stat(path); err == nil {
			t.Fatal("a file was written")
		}
		v.assertHidden(testExportVaultPass, "typo-canary")
	})
	t.Run("session ended before the write", func(t *testing.T) {
		v, path := vaultExportModel(t)
		m := v.m
		v.startExport(testExportVaultPass)
		v.advance(exportPreview, exportEncrypted, path)
		m.endAdminSession()
		v.pump("f2")
		if m.screen != screenAdminAuth {
			t.Fatalf("f2 without a session = screen %v, want the admin dialog", m.screen)
		}
		if _, err := os.Stat(path); err == nil {
			t.Fatal("a file was written without the session")
		}
		v.pump("esc")
		if m.screen != screenTransfer || m.export.step != exportPreview {
			t.Fatalf("esc = screen %v, want the preview", m.screen)
		}
	})
}

func TestExportIsInTheRegisterAndTheHelp(t *testing.T) {
	m := cmdModel(t)
	var export *command
	list := m.commands()
	for i := range list {
		if list[i].name == "export" {
			export = &list[i]
		}
	}
	if export == nil {
		t.Fatalf("the register %v has no export", commandNames(list))
	}
	if got := commandNames(matchCommands(list, "backup")); len(got) != 1 || got[0] != "export" {
		t.Errorf("backup matches %v, want export", got)
	}
	for _, from := range []screen{screenNav, screenList} {
		if reason := m.unavailableFrom(*export, from); reason != "" {
			t.Errorf("export from screen %v is unavailable: %s", from, reason)
		}
	}
	for _, from := range []screen{screenForm, screenTransfer, screenLogs} {
		if reason := m.unavailableFrom(*export, from); reason == "" {
			t.Errorf("export from screen %v is available", from)
		}
	}
	found := false
	for _, topic := range helptopics.All() {
		found = found || strings.Contains(topic.Text, "The command export")
	}
	if !found {
		t.Error("no help topic describes the export command")
	}
}
