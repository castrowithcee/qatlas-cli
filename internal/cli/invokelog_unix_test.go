//go:build linux || darwin

package cli

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// While a vault process holds the vault unlocked, it writes and signs every entry itself, and verify has it
// check them: a check value changed by hand, on the last entry too, breaks the chain. Once it is locked,
// the entries it signed stay signed, verify reports them unchecked, and the next entries are unverified.
func TestInvokeLogSignedByTheVaultProcess(t *testing.T) {
	dir := encryptedVaultFixture(t, "")
	withVaultProcess(t, dir)
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))
	if code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "unlock", "--config", configIn(dir)); code != exitOK {
		t.Fatalf("unlock: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}

	for i := 0; i < 3; i++ {
		code, _, stderr := runWithInput(t, &Options{}, "", "invoke", "bookstack.pages.nope", "--config", configIn(dir))
		if code == exitOK || strings.Contains(stderr, "warning") {
			t.Fatalf("invoke %d: exit %d, stderr %q, want the unknown operation and no warning", i, code, stderr)
		}
	}
	entries, lines := logEntries(t, dir)
	if len(entries) != 3 {
		t.Fatalf("the log holds %d entries, want 3", len(entries))
	}
	for i, entry := range entries {
		if entry.MAC == "" || entry.Seq != uint64(i+1) || entry.Operation != "bookstack.pages.nope" {
			t.Fatalf("entry %d = %+v, want a signed entry of the invoke", i, entry)
		}
	}

	code, doc, stderr := verifyJSON(t, dir)
	if code != exitOK || doc.Broken || doc.MACCheck != macChecked || doc.Days[0].Unverified != 0 ||
		doc.Days[0].Unchecked != 0 || stderr != "" {
		t.Fatalf("verify: exit %d, report %+v, stderr %q", code, doc, stderr)
	}

	// The last line keeps a valid chain, nothing follows it, but its check value no longer matches.
	files, _ := filepath.Glob(filepath.Join(dir, vault.DirName, "logs", "*.jsonl"))
	original, _ := os.ReadFile(files[0])
	last := string(lines[len(lines)-1])
	changedLast := strings.Replace(last, `"path":"cli"`, `"path":"mcp"`, 1)
	if err := os.WriteFile(files[0], []byte(strings.Replace(string(original), last, changedLast, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	code, doc, _ = verifyJSON(t, dir)
	if code == exitOK || !doc.Broken || doc.Days[0].Changed != 1 ||
		!strings.Contains(doc.Days[0].Problem, "sequence 3 does not match its check value") {
		t.Fatalf("verify of a changed last line: exit %d, report %+v", code, doc)
	}
	if err := os.WriteFile(files[0], original, 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _, stderr := runWithInput(t, &Options{}, "", "vault", "lock", "--config", configIn(dir)); code != exitOK {
		t.Fatalf("lock: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := runWithInput(t, &Options{}, "", "invoke", "bookstack.pages.nope", "--config", configIn(dir)); code == exitOK ||
		strings.Contains(stderr, "warning") {
		t.Fatalf("invoke while locked: exit %d, stderr %q", code, stderr)
	}
	code, doc, stderr = verifyJSON(t, dir)
	if code != exitOK || doc.Broken || doc.MACCheck != macVaultLocked || doc.Days[0].Unchecked != 3 ||
		doc.Days[0].Unverified != 1 || stderr != "" {
		t.Fatalf("verify while locked: exit %d, report %+v, stderr %q", code, doc, stderr)
	}

	// The log key a vault unlocked here derives is the one the vault process signed with.
	here := vault.New(dir)
	if _, err := here.Unlock("s3cret-phrase"); err != nil {
		t.Fatal(err)
	}
	checker, macCheck, done := logChecker(context.Background(), here, func(w string) { t.Errorf("warning %q", w) })
	defer done()
	report, err := invokelog.VerifyWith(here.Dir(), checker)
	if err != nil || macCheck != macChecked || report.Broken || report.Days[0].Changed != 0 || report.Days[0].Unchecked != 0 {
		t.Fatalf("VerifyWith(unlocked here) = %+v, %q, %v, want the process's check values valid", report, macCheck, err)
	}
}

// oldVaultProcess listens at the socket of the vault in dir like a vault process of another build: it
// answers every connection with a version error.
func oldVaultProcess(t *testing.T, dir string) {
	t.Helper()
	original := vaultProcessSupported
	vaultProcessSupported = true
	t.Cleanup(func() { vaultProcessSupported = original })
	runtimeDir, err := os.MkdirTemp("", "qv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	path, err := vaultproc.SocketPath(vault.New(dir).Dir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			_, _ = bufio.NewReader(conn).ReadBytes('\n')
			_, _ = conn.Write([]byte(`{"v":3,"error":"version"}` + "\n"))
			_ = conn.Close()
		}
	}()
}

// A vault process of another build cannot sign: the entry is written unsigned all the same, the invoke is
// not changed, and the reason is a warning. verify says it cannot have the check values checked.
func TestInvokeLogFallsBackOnAnOldVaultProcess(t *testing.T) {
	dir := encryptedVaultFixture(t, "")
	oldVaultProcess(t, dir)

	writer := invokeLogWriter{logger: invokelog.New(filepath.Join(dir, vault.DirName), 90), vault: vault.New(dir)}
	err := writer.Append(logFields("op.direct"))
	var unsigned *invokelog.UnsignedError
	if !errors.As(err, &unsigned) || !errors.Is(err, vaultproc.ErrVersion) {
		t.Fatalf("Append() = %v, want an UnsignedError wrapping ErrVersion", err)
	}

	code, _, stderr := runWithInput(t, &Options{}, "", "invoke", "bookstack.pages.nope", "--config", configIn(dir))
	if code == exitOK || !strings.HasPrefix(stderr, "qatlas: unknown-operation") ||
		!strings.Contains(stderr, "qatlas: warning: the invocation log entry was written without a check value") {
		t.Fatalf("invoke: exit %d, stderr %q, want the invoke's own error and the warning", code, stderr)
	}
	entries, _ := logEntries(t, dir)
	if len(entries) != 2 || entries[0].MAC != "" || entries[1].MAC != "" {
		t.Fatalf("entries = %+v, want two unsigned entries", entries)
	}

	code, doc, stderr := verifyJSON(t, dir)
	if code != exitOK || doc.Broken || doc.MACCheck != macVaultLocked || doc.Days[0].Unverified != 2 ||
		!strings.Contains(stderr, "the vault process cannot check") {
		t.Fatalf("verify: exit %d, report %+v, stderr %q", code, doc, stderr)
	}
}
