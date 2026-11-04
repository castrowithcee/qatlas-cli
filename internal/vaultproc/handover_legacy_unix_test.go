//go:build (linux || darwin) && !legacy_vault_names

package vaultproc

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// legacySockets returns the socket of this release and the one the earlier release keeps for the same vault,
// in the runtime directory names of either below one fresh directory.
func legacySockets(t *testing.T) (current, legacy string) {
	t.Helper()
	root := shortTempDir(t)
	return filepath.Join(root, runtimeDirName, "v.sock"), filepath.Join(root, legacyRuntimeDirName, "v.sock")
}

// ownSocket reports whether the socket at path is the one l listens on.
func ownSocket(l net.Listener, path string) bool {
	info, err := os.Lstat(path)
	return err == nil && os.SameFile(info, l.(*lockedListener).own)
}

func assertAbsent(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s is there: %v", path, err)
		}
	}
}

// A successor prepares a handover from the earlier release's socket only where a vault process serves that
// socket, with the checks Listen makes of its own, and none serves this release's socket. Anything in its way
// there is left alone.
func TestListenSuccessorPreparesOnlyForAServingEarlierProcess(t *testing.T) {
	for name, tc := range map[string]struct {
		setup  func(t *testing.T, current, legacy string)
		legacy bool
		fails  bool
	}{
		"earlier process serves":        {setup: func(t *testing.T, _, legacy string) { listenFor(t, legacy) }, legacy: true},
		"nothing at the earlier socket": {setup: func(*testing.T, string, string) {}},
		"earlier socket without its lock": {setup: func(t *testing.T, _, legacy string) {
			prepareSocketDir(t, legacy)
			l, err := rawListen(legacy)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = l.Close() })
		}},
		"earlier directory open to others": {setup: func(t *testing.T, _, legacy string) {
			listenFor(t, legacy)
			if err := os.Chmod(filepath.Dir(legacy), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		"this release's socket served too": {setup: func(t *testing.T, current, legacy string) {
			listenFor(t, legacy)
			listenFor(t, current)
		}},
		"something beside the earlier socket": {setup: func(t *testing.T, _, legacy string) {
			listenFor(t, legacy)
			if err := os.WriteFile(NextPath(legacy)+".lock", []byte("kept"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, fails: true},
	} {
		t.Run(name, func(t *testing.T) {
			current, legacy := legacySockets(t)
			tc.setup(t, current, legacy)
			l, err := listenSuccessor(current, legacy)
			if tc.fails {
				if err == nil {
					_ = l.Close()
					t.Fatal("listenSuccessor() succeeded with something in the way")
				}
				if data, _ := os.ReadFile(NextPath(legacy) + ".lock"); string(data) != "kept" {
					t.Fatalf("what was in the way was replaced: %q", data)
				}
				assertAbsent(t, NextPath(legacy))
				return
			}
			if err != nil {
				t.Fatalf("listenSuccessor() error = %v", err)
			}
			t.Cleanup(func() { _ = l.Close() })
			if !tc.legacy {
				if !ownSocket(l, NextPath(current)) {
					t.Fatal("the successor does not listen beside this release's socket")
				}
				assertAbsent(t, NextPath(legacy), NextPath(legacy)+".lock")
				return
			}
			if !ownSocket(l, current) {
				t.Fatal("the successor does not listen at this release's socket")
			}
			assertAbsent(t, NextPath(current), NextPath(current)+".lock")
			// Beside the earlier socket is a socket nobody listens on, and a lock that is held.
			info, err := os.Lstat(NextPath(legacy))
			if err != nil || info.Mode().Type() != fs.ModeSocket || ownSocket(l, NextPath(legacy)) {
				t.Fatalf("beside the earlier socket is %v, %v, want a socket of its own", info, err)
			}
			if conn, err := rawDial(NextPath(legacy)); err == nil {
				_ = conn.Close()
				t.Fatal("a connection beside the earlier socket reached something")
			}
			if !serves(NextPath(legacy)) {
				t.Fatal("the lock beside the earlier socket is not held")
			}
		})
	}
}

// listenFor listens at path as a vault process does, until the test ends.
func listenFor(t *testing.T, path string) net.Listener {
	t.Helper()
	l, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen(%s) error = %v", path, err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// Once a vault process of the earlier release moved what the successor left beside its socket onto its own
// paths, the handover is done: nothing is left at the earlier socket, and the successor alone serves this
// release's socket.
func TestAwaitHandoverFromTheEarlierSocket(t *testing.T) {
	current, legacy := legacySockets(t)
	earlier := listenFor(t, legacy)
	l, err := listenSuccessor(current, legacy)
	if err != nil {
		t.Fatalf("listenSuccessor() error = %v", err)
	}
	if err := earlier.(handoverListener).handOver(); err != nil {
		t.Fatalf("handOver() of the earlier process error = %v", err)
	}
	if err := AwaitHandover(context.Background(), l, current, time.Second); err != nil {
		t.Fatalf("AwaitHandover() error = %v", err)
	}
	if !ownSocket(l, current) || !serves(current) {
		t.Fatal("the successor does not serve this release's socket")
	}
	if entries, err := os.ReadDir(filepath.Dir(legacy)); err != nil || len(entries) != 0 {
		t.Fatalf("the earlier runtime directory holds %v, %v, want nothing", entries, err)
	}
	if _, err := Listen(current); !errors.Is(err, ErrRunning) {
		t.Fatalf("Listen() at the successor's socket error = %v, want ErrRunning", err)
	}
	// The earlier process ends after its handover and leaves the successor alone.
	_ = earlier.Close()
	if !ownSocket(l, current) {
		t.Fatal("closing the earlier process removed the successor's socket")
	}
	_ = l.Close()
	assertAbsent(t, current)
}

// A successor the earlier process never hands over to removes everything of its own, at both sockets, once it
// locks, and leaves the earlier process its socket.
func TestSuccessorOfTheEarlierProcessCleansUpWithoutAHandover(t *testing.T) {
	current, legacy := legacySockets(t)
	earlier := listenFor(t, legacy)
	l, err := listenSuccessor(current, legacy)
	if err != nil {
		t.Fatalf("listenSuccessor() error = %v", err)
	}
	if err := AwaitHandover(context.Background(), l, current, 50*time.Millisecond); err == nil {
		t.Fatal("AwaitHandover() without a handover succeeded")
	}
	_ = l.Close()
	assertAbsent(t, current, NextPath(legacy), NextPath(legacy)+".lock")
	if !ownSocket(earlier, legacy) || !serves(legacy) {
		t.Fatal("the earlier process lost its socket")
	}
}

// A vault process of the earlier release hands the vault over to this release's successor, which then serves
// this release's socket alone: a client finds it there, with the secrets and the time to lock at, and nothing
// is left at the earlier socket.
func TestHandoverFromTheEarlierRelease(t *testing.T) {
	current, legacy := legacySockets(t)
	f := newHandoverFixtureAt(t, legacy)
	f.mode, f.env = "successor-renamed", []string{helperEnv + "_CURRENT=" + current}
	ctx := context.Background()
	before, err := f.client.Status(ctx)
	if err != nil {
		t.Fatalf("Status() = %v", err)
	}
	h := f.prepare()
	f.replaceWithRelease()
	pid, err := h.Commit(ctx)
	if err != nil || pid <= 0 || pid == os.Getpid() {
		t.Fatalf("Commit() = %d, %v, want the successor's process id", pid, err)
	}
	waitServed(t, f.served)

	c := *f.client
	c.Path = current
	after, err := c.Status(ctx)
	if err != nil || after.PID != pid || !after.LocksAt.Equal(before.LocksAt) {
		t.Fatalf("Status() at this release's socket = %+v, %v, want process %d locking at %v", after, err, pid,
			before.LocksAt)
	}
	if value, found, err := c.Get(ctx, "gh-a", "user", f.scope); err != nil || !found || value != "synthetic-value" {
		t.Fatalf("Get() from the successor = %v, %v, want the value", found, err)
	}
	if _, err := f.client.Status(ctx); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Status() at the earlier socket = %v, want ErrNotRunning", err)
	}
	entries, err := os.ReadDir(filepath.Dir(legacy))
	if err != nil || len(entries) != 0 {
		t.Fatalf("the earlier runtime directory holds %v, %v, want nothing", entries, err)
	}
	assertAbsent(t, NextPath(current), NextPath(current)+".lock")
	if got := f.handoverLog(); len(got) != 1 || got[0] != "success" {
		t.Fatalf("the log holds handovers %v, want one success", got)
	}

	if err := c.Lock(ctx); err != nil {
		t.Fatalf("Lock() of the successor = %v", err)
	}
	f.mu.Lock()
	out := f.output
	f.mu.Unlock()
	if got := line(t, out); got != "locked" {
		t.Fatalf("the successor said %q after Lock()", got)
	}
	assertAbsent(t, current)
}

// No downgrade: a vault process of this release never hands over to a successor that listens only beside the
// earlier release's socket, as a successor of that release would. It locks itself instead.
func TestHandoverNeverGoesToASuccessorOfTheEarlierRelease(t *testing.T) {
	current, legacy := legacySockets(t)
	f := newHandoverFixtureAt(t, current)
	f.mode, f.env = "successor-earlier", []string{helperEnv + "_LEGACY=" + legacy}
	h := f.prepare()
	f.replaceWithRelease()
	if pid, err := h.Commit(context.Background()); !errors.Is(err, ErrHandover) || pid != 0 {
		t.Fatalf("Commit() = %d, %v, want ErrHandover", pid, err)
	}
	f.assertLocked()
	if got := f.handoverLog(); len(got) != 1 || got[0] != codeHandoverFailed {
		t.Fatalf("the log holds handovers %v, want one %s", got, codeHandoverFailed)
	}
	c := *f.client
	c.Path = legacy
	if _, err := c.Status(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Status() at the earlier socket = %v, want ErrNotRunning", err)
	}
}

// A successor that serves another vault directory than the one handed over takes over only from a vault
// process of the earlier release serving that directory: it listens at this release's socket for its own
// directory and marks beside the earlier socket of the directory handed over. Without such a process it fails
// and leaves nothing behind.
func TestListenSuccessorForAnotherVaultDirectory(t *testing.T) {
	runtimeDir := shortTempDir(t)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	home := t.TempDir()
	from, to := filepath.Join(home, "cli", "vault"), filepath.Join(home, "connect", "vault")
	socket, err := SocketPath(to)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := socketPath(from, legacyRuntimeDirName)
	if err != nil {
		t.Fatal(err)
	}

	if EarlierServes(from) {
		t.Fatal("EarlierServes() without a process = true")
	}
	if l, err := ListenSuccessor(to, from); err == nil {
		_ = l.Close()
		t.Fatal("ListenSuccessor() for another directory succeeded without an earlier process")
	}
	assertAbsent(t, socket, NextPath(socket), NextPath(legacy))

	// A process of this release that serves the directory handed over hands over beside its own socket only.
	own, err := SocketPath(from)
	if err != nil {
		t.Fatal(err)
	}
	current := listenFor(t, own)
	if EarlierServes(from) {
		t.Fatal("EarlierServes() with a process of this release = true")
	}
	if l, err := ListenSuccessor(to, from); err == nil {
		_ = l.Close()
		t.Fatal("ListenSuccessor() for another directory succeeded from a process of this release")
	}
	_ = current.Close()

	listenFor(t, legacy)
	if !EarlierServes(from) {
		t.Fatal("EarlierServes() with an earlier process = false")
	}
	l, err := ListenSuccessor(to, from)
	if err != nil {
		t.Fatalf("ListenSuccessor() error = %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	if !ownSocket(l, socket) {
		t.Fatal("the successor does not listen at this release's socket for the directory it serves")
	}
	if !serves(NextPath(legacy)) {
		t.Fatal("nothing held beside the earlier socket of the directory handed over")
	}
}
