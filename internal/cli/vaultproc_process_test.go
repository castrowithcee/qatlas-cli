//go:build linux || darwin || windows

package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// withVaultProcess lets 'vault unlock' start a real vault process for the duration of a test: this test
// binary, run as qatlas, with its socket in a runtime directory of the test's own, or on Windows on a pipe
// named after the test's own vault. Every process a test
// starts this way is ended on cleanup, whatever the test did with it.
func withVaultProcess(t *testing.T, dir string) *vaultproc.Client {
	t.Helper()
	original := vaultProcessSupported
	vaultProcessSupported = true
	t.Cleanup(func() { vaultProcessSupported = original })
	t.Setenv(runAsQatlasEnv, "1")
	t.Setenv("XDG_RUNTIME_DIR", shortRuntimeDir(t))

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

// shortRuntimeDir returns a fresh runtime directory that is removed after the test, short enough for the
// vault socket below it; Windows, whose vault pipe lives in no directory, ignores it. t.TempDir nests the full test name, and on macOS the per-user temporary directory
// is long already, so there it is created in /tmp instead.
func shortRuntimeDir(t *testing.T) string {
	t.Helper()
	base := ""
	if runtime.GOOS == "darwin" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "qv")
	if err != nil {
		t.Fatalf("MkdirTemp() = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// endVaultProcess locks a vault process a test left running, and kills it should it not end.
func endVaultProcess(t *testing.T, client *vaultproc.Client) {
	status, err := client.Status(context.Background())
	if err != nil {
		return
	}
	_ = client.Lock(context.Background())
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if !processAlive(status.PID) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	killProcess(status.PID)
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
	if args := processArguments(t, status.PID); strings.Contains(args, canaryVault) || strings.Contains(args, "s3cret") {
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
	if !vaultHandoverPlatform {
		// A vault process that never hands the vault over reports no update behaviour: none applies.
		if strings.Contains(stdout, `"update_behaviour"`) {
			t.Fatalf("status of a vault process that never hands over: stdout = %q, want no update behaviour", stdout)
		}
	} else {
		if !strings.Contains(stdout, `"update_behaviour":"handover"`) {
			t.Fatalf("status: stdout = %q, want the update behaviour handover", stdout)
		}
		// The process reads the update behaviour itself, fresh for every status: a settings.age it cannot
		// trust reads as lock, with a warning.
		if err := os.WriteFile(filepath.Join(dir, vault.DirName, "settings.age"), []byte("not written by this vault"), 0o600); err != nil {
			t.Fatal(err)
		}
		code, stdout, _ = runWithInput(t, &Options{}, "", "vault", "status", "--config", configIn(dir), "--output", "json")
		if code != exitOK || !strings.Contains(stdout, `"update_behaviour":"lock"`) || !strings.Contains(stdout, "settings.age") {
			t.Fatalf("status with an untrusted settings.age: exit code = %d, stdout = %q", code, stdout)
		}
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
	for deadline := time.Now().Add(5 * time.Second); processAlive(status.PID); {
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
	checkServeStreams(t, dir)
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

// The next steps of vault-locked where a vault process keeps the vault unlocked for later calls.
const (
	vaultLockedStep    = "run 'qatlas vault unlock' in a terminal, which keeps it unlocked for later commands, then try again"
	vaultLockedMCPStep = "agents cannot unlock the vault; ask the user to run 'qatlas vault unlock' in a terminal, " +
		"which keeps it unlocked for later calls, then try again"
)
