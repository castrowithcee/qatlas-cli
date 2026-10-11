//go:build linux || darwin

package cli

import (
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// servingAsReplacedPeer listens at path as a vault process whose program was replaced does, answering every
// connection replaced. It stands in the test's own process, so the id the kernel names for it is this one;
// the SIGTERM that ends it is caught here, and ends the fake instead, the way it ends the real process.
// The returned channel receives once for every SIGTERM.
func servingAsReplacedPeer(t *testing.T, path string) <-chan os.Signal {
	t.Helper()
	l, err := vaultproc.Listen(path)
	if err != nil {
		t.Fatalf("Listen() = %v", err)
	}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 4096)
			_, _ = conn.Read(buf)
			_, _ = conn.Write([]byte(`{"v":` + strconv.Itoa(vaultproc.Version) + `,"error":"replaced"}` + "\n"))
			_ = conn.Close()
		}
	}()
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGTERM)
	ended := make(chan os.Signal, 4)
	go func() {
		for s := range sig {
			_ = l.Close()
			ended <- s
		}
	}()
	t.Cleanup(func() { signal.Stop(sig); close(sig); _ = l.Close() })
	return ended
}

// 'vault lock' and 'vault unlock' end a vault process whose program was replaced and that has no successor,
// the unlock telling so with the process id, and then asking the passphrase and starting a new one.
func TestVaultLockAndUnlockEndAReplacedVaultProcess(t *testing.T) {
	dir := encryptedVaultFixture(t, "")
	client := withVaultProcess(t, dir)
	withVaultPassphrase(t, offeringPassphrase("s3cret-phrase"))

	ended := servingAsReplacedPeer(t, client.Path)
	code, stdout, stderr := runWithInput(t, &Options{}, "", "vault", "lock", "--config", configIn(dir))
	if code != exitOK || !strings.Contains(stdout, "the vault is locked") {
		t.Fatalf("lock: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("lock did not end the replaced vault process")
	}

	ended = servingAsReplacedPeer(t, client.Path)
	code, stdout, stderr = runWithInput(t, &Options{}, "", "vault", "unlock", "--config", configIn(dir))
	if code != exitOK || !strings.Contains(stdout, "unlocked in a vault process (pid ") {
		t.Fatalf("unlock: exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("unlock did not end the replaced vault process")
	}
	if want := "ended the vault process (pid " + strconv.Itoa(os.Getpid()) + ")"; !strings.Contains(stderr, want) {
		t.Errorf("unlock: stderr = %q, want %q", stderr, want)
	}
}
