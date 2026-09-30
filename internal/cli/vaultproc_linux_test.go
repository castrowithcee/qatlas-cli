//go:build linux

package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// withVaultProcess lets 'vault unlock' start a real vault process for the duration of a test: this test
// binary, run as qatlas, with its socket in a runtime directory of the test's own. Every process a test
// starts this way is ended on cleanup, whatever the test did with it.
func withVaultProcess(t *testing.T, dir string) *vaultproc.Client {
	t.Helper()
	original := vaultProcessSupported
	vaultProcessSupported = true
	t.Cleanup(func() { vaultProcessSupported = original })
	t.Setenv(runAsQatlasEnv, "1")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	v := vault.New(dir)
	recipient, err := v.Recipient()
	if err != nil {
		t.Fatalf("Recipient() = %v", err)
	}
	path, err := vaultproc.SocketPath(v.Dir())
	if err != nil {
		t.Fatalf("SocketPath() = %v", err)
	}
	client := vaultproc.NewClient(path, recipient)
	t.Cleanup(func() { endVaultProcess(t, client) })
	return client
}

// endVaultProcess locks a vault process a test left running, and kills it should it not end.
func endVaultProcess(t *testing.T, client *vaultproc.Client) {
	status, err := client.Status(context.Background())
	if err != nil {
		return
	}
	_ = client.Lock(context.Background())
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if syscall.Kill(status.PID, 0) != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(status.PID, syscall.SIGKILL)
	t.Errorf("the vault process %d did not end when it was locked", status.PID)
}

// encryptedVaultFixture writes the vault fixture's configuration, with extra appended, and an encrypted
// vault holding one secret and one pending entry.
func encryptedVaultFixture(t *testing.T, extra string) string {
	t.Helper()
	dir := vaultCredentialFixture(t)
	v := vault.New(dir)
	if err := v.Set("wiki-vault", "token-id", canaryVault, offeringPassphrase("s3cret-phrase")); err != nil {
		t.Fatalf("vault Set() = %v", err)
	}
	// The fixture's connection is approved as the base configuration has it, before extra is appended.
	approveConnections(t, dir, v)
	if err := vault.New(dir).Set("wiki-vault", "token-secret", "second-"+canaryVault, nil); err != nil {
		t.Fatalf("pending Set() = %v", err)
	}
	if extra != "" {
		f, err := os.OpenFile(configIn(dir), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString(extra)
		_ = f.Close()
	}
	return dir
}

// 'vault unlock' hands the unlocked vault, pending entries merged, to a vault process of its own; 'vault
// status' shows it, a second unlock finds it instead of starting another, and 'vault lock' ends it, all
// without a terminal but for the passphrase itself.
func TestVaultUnlockStartsAVaultProcess(t *testing.T) {
	dir := encryptedVaultFixture(t, "")
	client := withVaultProcess(t, dir)
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))

	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "unlock", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("unlock: exit code = %d (stdout: %s, stderr: %s)", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "unlocked in a vault process (pid ") || !strings.Contains(stdout, "merged 1 pending entry") {
		t.Errorf("unlock: stdout = %q, want the process and the merge reported", stdout)
	}
	for _, out := range []string{stdout, stderr} {
		if strings.Contains(out, canaryVault) || strings.Contains(out, "s3cret-phrase") {
			t.Fatalf("unlock printed a secret: %q", out)
		}
	}

	ctx := context.Background()
	status, err := client.Status(ctx)
	if err != nil || status.PID == os.Getpid() || time.Until(status.LocksAt) < 11*time.Hour {
		t.Fatalf("Status() = %+v, %v, want another process locking itself in 12h", status, err)
	}
	if value, found, err := client.Get(ctx, "wiki-vault", "token-secret", connectionScope(t, dir, "wiki")); err != nil || !found || value != "second-"+canaryVault {
		t.Fatalf("Get() of the merged entry = %q, %v, %v", value, found, err)
	}
	cmdline, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(status.PID), "cmdline"))
	if strings.Contains(string(cmdline), canaryVault) || strings.Contains(string(cmdline), "s3cret") {
		t.Fatalf("the vault process carries a secret in its arguments")
	}

	// Nothing is asked the second time, and the same process keeps serving.
	withVaultPassphrase(t, func(string) (string, error) {
		t.Errorf("a second unlock asked for the passphrase")
		return "", vault.ErrNoTerminal
	})
	code, stdout, stderr = runWithInput(t, &Options{}, "", "vault", "unlock", "--config", configIn(dir))
	if code != exitOK || !strings.Contains(stdout, "already unlocked") || !strings.Contains(stdout, strconv.Itoa(status.PID)) {
		t.Fatalf("second unlock: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}

	withInteractive(t, false)
	code, stdout, stderr = runWithInput(t, &Options{}, "", "vault", "status", "--config", configIn(dir), "--output", "json")
	if code != exitOK || !strings.Contains(stdout, `"process":"running"`) || !strings.Contains(stdout, `"pid":`+strconv.Itoa(status.PID)) ||
		!strings.Contains(stdout, `"locks_at":`) || !strings.Contains(stdout, `"state":"unlocked"`) ||
		!strings.Contains(stdout, `"entries":"unknown"`) {
		// This run never unlocked the vault itself, yet the process holds it open: the effective state.
		t.Fatalf("status: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}

	code, stdout, stderr = runWithInput(t, &Options{}, "", "vault", "lock", "--config", configIn(dir))
	if code != exitOK || !strings.Contains(stdout, "the vault is locked") {
		t.Fatalf("lock: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if _, err := client.Status(ctx); !errors.Is(err, vaultproc.ErrNotRunning) {
		t.Fatalf("Status() after lock error = %v, want ErrNotRunning", err)
	}
	code, stdout, _ = runWithInput(t, &Options{}, "", "vault", "lock", "--config", configIn(dir))
	if code != exitOK || !strings.Contains(stdout, "already locked") {
		t.Fatalf("second lock: exit code = %d, stdout = %q", code, stdout)
	}
	code, stdout, _ = runWithInput(t, &Options{}, "", "vault", "status", "--config", configIn(dir), "--output", "json")
	if code != exitOK || !strings.Contains(stdout, `"process":"none"`) || strings.Contains(stdout, `"pid"`) {
		t.Fatalf("status after lock: exit code = %d, stdout = %q", code, stdout)
	}
}

// vault.idle_timeout reaches the vault process, which locks itself once it passed without a read.
func TestVaultProcessLocksItselfWhenIdle(t *testing.T) {
	dir := encryptedVaultFixture(t, "vault:\n  idle_timeout: 700ms\n")
	client := withVaultProcess(t, dir)
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))

	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "unlock", "--config", configIn(dir))
	if code != exitOK {
		t.Fatalf("unlock: exit code = %d (stdout: %s, stderr: %s)", code, stdout, stderr)
	}
	status, err := client.Status(context.Background())
	if err != nil || time.Until(status.LocksAt) > time.Second {
		t.Fatalf("Status() = %+v, %v, want the configured idle timeout", status, err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		if _, err := client.Status(context.Background()); errors.Is(err, vaultproc.ErrNotRunning) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the vault process is still running after its idle timeout")
		}
		time.Sleep(100 * time.Millisecond)
	}
	for deadline := time.Now().Add(5 * time.Second); syscall.Kill(status.PID, 0) == nil; {
		if time.Now().After(deadline) {
			t.Fatalf("the vault process %d locked but did not end", status.PID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A configuration whose idle timeout is unusable stops the unlock before the passphrase is asked.
func TestVaultUnlockChecksTheIdleTimeoutFirst(t *testing.T) {
	dir := encryptedVaultFixture(t, "vault:\n  idle_timeout: \"0\"\n")
	withVaultProcess(t, dir)
	withVaultPassphrase(t, func(string) (string, error) {
		t.Errorf("the passphrase was asked for despite an unusable configuration")
		return "", vault.ErrNoTerminal
	})
	code, _, stderr := runWithInput(t, &Options{}, "", "vault", "unlock", "--config", configIn(dir))
	if code != exitUsage || !strings.Contains(stderr, "vault.idle_timeout") {
		t.Fatalf("exit code = %d, stderr = %q, want the setting refused", code, stderr)
	}
}

// Without a vault process, locking is already done, and neither 'lock' nor 'status' needs a terminal.
func TestVaultLockWithoutAProcess(t *testing.T) {
	dir := encryptedVaultFixture(t, "")
	withVaultProcess(t, dir)
	withInteractive(t, false)

	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "lock", "--config", configIn(dir))
	if code != exitOK || !strings.Contains(stdout, "already locked") {
		t.Fatalf("lock: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	code, stdout, _ = runWithInput(t, &Options{}, "", "vault", "status", "--config", configIn(dir), "--output", "json")
	if code != exitOK || !strings.Contains(stdout, `"process":"none"`) {
		t.Fatalf("status: exit code = %d, stdout = %q", code, stdout)
	}

	plain := vaultCredentialFixture(t)
	code, stdout, _ = runWithInput(t, &Options{}, "", "vault", "lock", "--config", configIn(plain))
	if code != exitOK || !strings.Contains(stdout, "not encrypted") {
		t.Fatalf("lock of an absent vault: exit code = %d, stdout = %q", code, stdout)
	}
	code, stdout, _ = runWithInput(t, &Options{}, "", "vault", "status", "--config", configIn(plain), "--output", "json")
	if code != exitOK || strings.Contains(stdout, `"process"`) {
		t.Fatalf("status of an absent vault: exit code = %d, stdout = %q, want no process field", code, stdout)
	}
}

// 'vault serve' run by hand, without the pipes 'vault unlock' hands it, refuses before it does anything.
func TestVaultServeRefusesToRunByHand(t *testing.T) {
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := vaultCredentialFixture(t)
	cmd := exec.Command(program, "vault", "serve", "--config", configIn(dir))
	cmd.Env = append(os.Environ(), runAsQatlasEnv+"=1", "XDG_RUNTIME_DIR="+t.TempDir())
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != exitUsage || !strings.Contains(string(out), "'qatlas vault unlock'") {
		t.Fatalf("vault serve by hand = %v, %q, want a usage error naming vault unlock", err, out)
	}

	if inheritedPipe(int(mustOpen(t, configIn(dir)).Fd())) {
		t.Errorf("inheritedPipe() accepted a regular file")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if !inheritedPipe(int(r.Fd())) {
		t.Errorf("inheritedPipe() refused a pipe")
	}
	if inheritedPipe(1 << 20) {
		t.Errorf("inheritedPipe() accepted a descriptor that is not open")
	}
}

func mustOpen(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// sessionWarnings reads what systemd-logind reads: KillUserProcesses= with its exceptions, and whether the
// user lingers when the socket sits in the runtime directory.
func TestSessionWarnings(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skipf("no current user: %v", err)
	}
	root := t.TempDir()
	original := systemdRoot
	systemdRoot = root
	t.Cleanup(func() { systemdRoot = original })
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	socket := filepath.Join(runtimeDir, "qatlas", "vault-0.sock")

	if got := sessionWarnings(socket); len(got) != 0 {
		t.Fatalf("sessionWarnings() without systemd = %v, want none", got)
	}
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "run", "systemd", "system"), 0o755); err != nil {
		t.Fatal(err)
	}

	write("etc/systemd/logind.conf", "[Login]\n#KillUserProcesses=yes\n")
	got := sessionWarnings(socket)
	if len(got) != 1 || !strings.Contains(got[0], "loginctl enable-linger") {
		t.Fatalf("sessionWarnings() without linger = %v, want the linger warning", got)
	}
	if got := sessionWarnings(filepath.Join(t.TempDir(), "vault-0.sock")); len(got) != 0 {
		t.Fatalf("sessionWarnings() for a socket outside the runtime directory = %v, want none", got)
	}

	write("var/lib/systemd/linger/"+me.Username, "")
	if got := sessionWarnings(socket); len(got) != 0 {
		t.Fatalf("sessionWarnings() with linger = %v, want none", got)
	}

	write("usr/lib/systemd/logind.conf.d/10-kill.conf", "[Login]\nKillUserProcesses=yes\n")
	got = sessionWarnings(socket)
	if len(got) != 1 || !strings.Contains(got[0], "KillUserProcesses=yes") {
		t.Fatalf("sessionWarnings() with KillUserProcesses=yes = %v, want the kill warning", got)
	}
	write("etc/systemd/logind.conf.d/20-exclude.conf", "[Login]\nKillExcludeUsers=root "+me.Username+"\n")
	if got := sessionWarnings(socket); len(got) != 0 {
		t.Fatalf("sessionWarnings() for an excluded user = %v, want none", got)
	}
	write("etc/systemd/logind.conf.d/20-exclude.conf", "[Other]\nKillExcludeUsers="+me.Username+"\n")
	write("etc/systemd/logind.conf.d/10-kill.conf", "[Login]\nKillUserProcesses=no\n")
	if got := sessionWarnings(socket); len(got) != 0 {
		t.Fatalf("sessionWarnings() with a drop-in in /etc switching it off = %v, want none", got)
	}
}

// The next steps of vault-locked where a vault process keeps the vault unlocked for later calls.
const (
	vaultLockedStep    = "run 'qatlas vault unlock' in a terminal, which keeps it unlocked for later commands, then try again"
	vaultLockedMCPStep = "agents cannot unlock the vault; ask the user to run 'qatlas vault unlock' in a terminal, " +
		"which keeps it unlocked for later calls, then try again"
)
