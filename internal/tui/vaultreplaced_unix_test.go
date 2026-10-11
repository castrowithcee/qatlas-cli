//go:build linux || darwin

package tui

import (
	"os"
	"os/signal"
	"path/filepath"
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

// 'ctrl+l' unlocking while a vault process with a replaced program answers ends that process, tells so with
// its id, and starts a new one, without asking anything but the passphrase.
func TestCtrlLEndsAReplacedVaultProcessBeforeStartingOne(t *testing.T) {
	const passphrase = "hunter2"
	dir := filepath.Join(t.TempDir(), "qatlas")
	path := filepath.Join(dir, "config.yaml")
	store := newTestStore(t, path)
	setup, _ := newVaultResolver(t, dir)
	mustNoError(t, setup.SetVault("other", "role", "canary-seed", func(string) (string, error) { return passphrase, nil }))
	locked, _ := newVaultResolver(t, dir)
	runtimeDir, err := os.MkdirTemp("", "qv")
	if err != nil {
		t.Fatalf("MkdirTemp() = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	withStartableVaultProcess(t)

	m, err := buildModel(store, nil, locked, nil)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	socket, err := vaultproc.SocketPath(m.secrets.Vault().Dir())
	if err != nil {
		t.Fatalf("SocketPath() = %v", err)
	}
	ended := servingAsReplacedPeer(t, socket)

	openSectionByName(t, m, sectionServices)
	pump(t, m, "ctrl+l")
	typeText(t, m, passphrase)
	pump(t, m, "enter")
	if m.fail != "" {
		t.Fatalf("unlocking reported %q", m.fail)
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the replaced vault process was not ended")
	}
	if want := "Ended the vault process (pid " + strconv.Itoa(os.Getpid()) + ")"; !strings.Contains(m.status, want) {
		t.Errorf("status = %q, want %q", m.status, want)
	}
	client, err := vaultproc.ProcessClientOf(m.secrets.Vault())
	if err != nil {
		t.Fatal(err)
	}
	if status, err := client.Status(t.Context()); err != nil || status.PID != os.Getpid() {
		t.Errorf("Status() = %+v, %v, want the new vault process answering", status, err)
	}
}
