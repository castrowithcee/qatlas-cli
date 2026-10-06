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
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// socketIn returns a socket path below a fresh test directory, in a run directory that does not exist yet.
func socketIn(t *testing.T) string {
	t.Helper()
	return filepath.Join(shortTempDir(t), "run", "v.sock")
}

// rawListen listens at path the way a server without the vault's key would: a plain socket, without the
// lock Listen takes. The directory is prepareSocketDir's.
func rawListen(path string) (net.Listener, error) { return net.Listen("unix", path) }

// rawDial connects to path the way a client that checks nothing would.
func rawDial(path string) (net.Conn, error) { return net.Dial("unix", path) }

// prepareSocketDir creates the private directory the socket at path lives in, for rawListen.
func prepareSocketDir(t *testing.T, path string) {
	t.Helper()
	if err := privateDir(filepath.Dir(path)); err != nil {
		t.Fatalf("privateDir() error = %v", err)
	}
}

// checkPeerProcess checks the process pid as the server checks a client of this user.
func checkPeerProcess(pid int) error { return checkProcess(pid, uint32(os.Getuid())) }

// checkSocketPrivate checks the modes of the socket at path and of its directory: their owner's alone.
func checkSocketPrivate(t *testing.T, path string) {
	t.Helper()
	for file, want := range map[string]fs.FileMode{filepath.Dir(path): dirMode, path: socketMode} {
		info, err := os.Lstat(file)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("mode of %s = %v, %v, want %04o", file, info.Mode().Perm(), err, want)
		}
	}
}

// checkSocketPresent checks that the socket at path is still there.
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

// platformHelper runs the helper process modes only Linux and macOS have: the successors of a handover.
func platformHelper(mode, path string) {
	switch mode {
	case "successor", "successor-no-socket":
		handoverHelper(mode, path)
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

func TestSocketPathFindsTheUserRuntimeDirectoryWithoutXDG(t *testing.T) {
	root := userRuntimeRoot
	t.Cleanup(func() { userRuntimeRoot = root })
	// A short root keeps the socket path within the kernel's limit; t.TempDir names it after the test.
	short := func() string {
		t.Helper()
		dir, err := os.MkdirTemp("", "qrt")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		return dir
	}
	userRuntimeRoot = short()
	t.Setenv("XDG_RUNTIME_DIR", "")
	// A short vault directory too: beside it, the socket path must stay within the limit of macOS (104
	// bytes), or the fallback directory takes over.
	vaultDir := filepath.Join(short(), "vault")
	runtimeDir := filepath.Join(userRuntimeRoot, strconv.Itoa(os.Getuid()))

	beside := func() string {
		t.Helper()
		path, err := SocketPath(vaultDir)
		if err != nil {
			t.Fatalf("SocketPath() error = %v", err)
		}
		return filepath.Dir(path)
	}
	if dir := beside(); dir != filepath.Join(filepath.Dir(vaultDir), "run") {
		t.Fatalf("SocketPath() without a runtime directory = %s, want run beside the vault", dir)
	}

	if err := os.Mkdir(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if dir := beside(); dir != filepath.Join(filepath.Dir(vaultDir), "run") {
		t.Fatalf("SocketPath() with a runtime directory open to others = %s, want it ignored", dir)
	}

	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Only Linux has a user runtime directory below userRuntimeRoot; macOS has none and keeps the socket
	// beside the vault.
	wantDir := filepath.Join(filepath.Dir(vaultDir), "run")
	if runtime.GOOS == "linux" {
		wantDir = filepath.Join(runtimeDir, "qatlas")
	}
	if dir := beside(); dir != wantDir {
		t.Fatalf("SocketPath() without XDG_RUNTIME_DIR = %s, want %s", dir, wantDir)
	}

	explicit := short()
	t.Setenv("XDG_RUNTIME_DIR", explicit)
	if dir := beside(); dir != filepath.Join(explicit, "qatlas") {
		t.Fatalf("SocketPath() with XDG_RUNTIME_DIR = %s, want the variable to win", dir)
	}
}
