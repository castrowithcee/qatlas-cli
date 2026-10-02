//go:build linux || darwin

package vaultproc

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// socketIn returns a socket path below a fresh test directory, in a run directory that does not exist yet.
func socketIn(t *testing.T) string {
	t.Helper()
	return filepath.Join(shortTempDir(t), "run", "v.sock")
}

// rawListen listens at path the way a server without the vault's key would, with nothing but a socket.
func rawListen(path string) (net.Listener, error) { return net.Listen("unix", path) }

// rawDial connects to path the way a client that checks nothing would.
func rawDial(path string) (net.Conn, error) { return net.Dial("unix", path) }

// prepareSocketDir creates the private directory a socket at path lives in, for a listener that does not.
func prepareSocketDir(t *testing.T, path string) {
	t.Helper()
	if err := privateDir(filepath.Dir(path)); err != nil {
		t.Fatalf("privateDir() error = %v", err)
	}
}

// checkPeerProcess checks the process pid as the server checks a client of this user.
func checkPeerProcess(pid int) error { return checkProcess(pid, uint32(os.Getuid())) }

// checkSocketPrivate checks that the socket at path and its directory are closed to others.
func checkSocketPrivate(t *testing.T, path string) {
	t.Helper()
	for file, want := range map[string]fs.FileMode{filepath.Dir(path): dirMode, path: socketMode} {
		info, err := os.Lstat(file)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("mode of %s = %v, %v, want %04o", file, info.Mode().Perm(), err, want)
		}
	}
}

// checkSocketPresent checks that the socket at path is still in place.
func checkSocketPresent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("the second Listen() removed the first socket: %v", err)
	}
}

// checkSocketGone checks that the socket at path was removed.
func checkSocketGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the socket is still there after Lock(): %v", err)
	}
}

func TestListenReplacesADeadSocket(t *testing.T) {
	path := socketIn(t)
	if err := privateDir(filepath.Dir(path)); err != nil {
		t.Fatalf("privateDir() error = %v", err)
	}
	dead, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix() error = %v", err)
	}
	dead.SetUnlinkOnClose(false)
	_ = dead.Close()

	if _, err := NewClient(path, testRecipient).Status(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Status() at a dead socket error = %v, want ErrNotRunning", err)
	}
	l, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen() over a dead socket error = %v", err)
	}
	_ = l.Close()
}

func TestListenLeavesAForeignFileAlone(t *testing.T) {
	path := socketIn(t)
	if err := privateDir(filepath.Dir(path)); err != nil {
		t.Fatalf("privateDir() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(path); err == nil {
		t.Fatalf("Listen() replaced a regular file")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "not a socket" {
		t.Fatalf("the file at the socket path changed: %q, %v", data, err)
	}
	if _, err := NewClient(path, testRecipient).Status(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Status() at a regular file error = %v, want ErrNotRunning", err)
	}
}

func TestListenRefusesAnOpenDirectory(t *testing.T) {
	path := socketIn(t)
	if err := os.Mkdir(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(path); err == nil {
		t.Fatalf("Listen() used a directory open to others")
	}

	link := filepath.Join(shortTempDir(t), "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(filepath.Join(link, "v.sock")); err == nil {
		t.Fatalf("Listen() followed a symbolic link")
	}
}

func TestListenRefusesAnOverlongPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), strings.Repeat("d", maxSocketPath), "v.sock")
	var tooLong *PathTooLongError
	if _, err := Listen(path); !errors.As(err, &tooLong) {
		t.Fatalf("Listen() error = %v, want PathTooLongError", err)
	}
	if _, err := os.Lstat(filepath.Dir(path)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Listen() created the directory of a path it refused")
	}
}

func TestCheckProcess(t *testing.T) {
	if err := checkProcess(os.Getpid(), uint32(os.Getuid())); err != nil {
		t.Fatalf("checkProcess() of this process error = %v", err)
	}
	if err := checkProcess(os.Getpid(), uint32(os.Getuid())+1); !errors.Is(err, ErrRefused) {
		t.Fatalf("checkProcess() of another user error = %v, want ErrRefused", err)
	}
	if err := checkProcess(0, uint32(os.Getuid())); !errors.Is(err, ErrRefused) {
		t.Fatalf("checkProcess() of pid 0 error = %v, want ErrRefused", err)
	}

	sleep := exec.Command("sleep", "60")
	if err := sleep.Start(); err != nil {
		t.Skipf("cannot start sleep: %v", err)
	}
	defer func() {
		_ = sleep.Process.Kill()
		_ = sleep.Wait()
	}()
	if err := checkProcess(sleep.Process.Pid, uint32(os.Getuid())); !errors.Is(err, ErrRefused) {
		t.Fatalf("checkProcess() of another program error = %v, want ErrRefused", err)
	}

	// A process whose program file was removed after it started, as an update leaves one behind.
	program := otherProgram(t)
	cmd, _ := helper(t, program, "sleep", "")
	if err := os.Remove(program); err != nil {
		t.Fatal(err)
	}
	err := checkProcess(cmd.Process.Pid, uint32(os.Getuid()))
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "removed or replaced") {
		t.Fatalf("checkProcess() of a removed program error = %v, want ErrRefused for a removed program", err)
	}
}
