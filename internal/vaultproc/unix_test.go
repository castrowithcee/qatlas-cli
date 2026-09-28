//go:build linux || darwin

package vaultproc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
)

// These tests run the real peer check. Client and server share the test binary's process, so the program
// the server finds for its peer, through /proc/<peer>/exe on Linux and kern.procargs2 on macOS, is the test
// binary, exactly the program this process runs: the test binary checks itself. Another program is a copy
// of the test binary at another path, which is a different file, started as a helper process through
// TestHelperProcess.

const helperEnv = "QATLAS_VAULTPROC_HELPER"

// TestHelperProcess is not a test: it is what a helper process runs, selected by helperEnv.
func TestHelperProcess(t *testing.T) {
	mode := os.Getenv(helperEnv)
	if mode == "" {
		t.Skip("runs only as a helper process")
	}
	path := os.Getenv(helperEnv + "_SOCKET")
	switch mode {
	case "sleep":
		time.Sleep(time.Minute)
	case "listen":
		// Listens like a server without the vault's key: answers the challenge with a forged proof and
		// reports how many bytes the client sent after it, or that no challenge came at all.
		l, err := net.Listen("unix", path)
		if err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		fmt.Println("listening")
		conn, err := l.Accept()
		if err != nil {
			os.Exit(1)
		}
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		var h hello
		if err := readMessage(conn, &h); err != nil || len(h.Challenge) == 0 {
			fmt.Println("no challenge")
			os.Exit(0)
		}
		_ = writeMessage(conn, response{V: Version, Proof: proofOf(make([]byte, nonceSize))})
		data, _ := io.ReadAll(conn)
		fmt.Println("received", len(data))
	case "get":
		// Connects like a client with a valid challenge, without checking the server, and reports the
		// answer's error code and whether it carried a proof.
		conn, err := net.Dial("unix", path)
		if err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		recipient, err := age.ParseX25519Recipient(os.Getenv(helperEnv + "_RECIPIENT"))
		if err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		_, challenge, err := newChallenge(recipient)
		if err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		_ = writeMessage(conn, hello{V: Version, Challenge: challenge})
		var resp response
		if err := readMessage(conn, &resp); err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		fmt.Printf("answer %q %d\n", resp.Error, len(resp.Proof))
	case "serve-hardened":
		// Serves like the real vault process: hardened first, the key read from standard input.
		if err := Harden(); err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		text, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		key, err := age.ParseX25519Identity(strings.TrimSpace(text))
		if err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		l, err := Listen(path)
		if err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		fmt.Println("listening")
		if err := NewServer(key, testSecrets(), testBindings()).Serve(l); err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		fmt.Println("locked")
	case "harden":
		if err := Harden(); err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		fmt.Println(hardenedState())
	}
	os.Exit(0)
}

// otherProgram copies the test binary to another path, so a process started from it runs a program that
// is not this one.
func otherProgram(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("cannot read the test binary: %v", err)
	}
	path := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(path, data, 0o700); err != nil {
		t.Fatalf("cannot copy the test binary: %v", err)
	}
	return path
}

// helper starts program as a helper process in mode and returns it with its standard output.
func helper(t *testing.T, program, mode, socket string) (*exec.Cmd, *bufio.Reader) {
	t.Helper()
	cmd := exec.Command(program, "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), helperEnv+"="+mode, helperEnv+"_SOCKET="+socket,
		helperEnv+"_RECIPIENT="+testRecipient)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start a helper process: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd, bufio.NewReader(out)
}

func line(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	text, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("the helper process said %q, then %v", text, err)
	}
	return strings.TrimSpace(text)
}

// socketIn returns a socket path below a fresh test directory, in a run directory that does not exist yet.
func socketIn(t *testing.T) string {
	t.Helper()
	return filepath.Join(shortTempDir(t), "run", "v.sock")
}

func serve(t *testing.T, s *Server, l net.Listener) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- s.Serve(l) }()
	t.Cleanup(func() { _ = s.Close() })
	return done
}

func waitServed(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Serve() did not return")
	}
}

func TestSocketRoundTrip(t *testing.T) {
	path := socketIn(t)
	l, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	for file, want := range map[string]fs.FileMode{filepath.Dir(path): dirMode, path: socketMode} {
		info, err := os.Lstat(file)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("mode of %s = %v, %v, want %04o", file, info.Mode().Perm(), err, want)
		}
	}

	s := NewServer(testKey, testSecrets(), testBindings())
	done := serve(t, s, l)
	c := NewClient(path, testRecipient)
	ctx := context.Background()

	value, found, err := c.Get(ctx, "wiki-reader", "token", *testScope())
	if err != nil || !found || value != "synthetic-token" {
		t.Fatalf("Get() = %q, %v, %v, want the stored value", value, found, err)
	}
	if err := c.Set(ctx, "wiki-reader", "token", "synthetic-new"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if value, _, _ := c.Get(ctx, "wiki-reader", "token", *testScope()); value != "synthetic-new" {
		t.Fatalf("Get() after Set() = %q", value)
	}
	if err := c.Delete(ctx, "wiki-reader", "token"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, found, err := c.Get(ctx, "wiki-reader", "token", *testScope()); err != nil || found {
		t.Fatalf("Get() after Delete() = found %v, %v", found, err)
	}
	status, err := c.Status(ctx)
	if err != nil || status.PID != os.Getpid() || time.Until(status.LocksAt) < time.Hour {
		t.Fatalf("Status() = %+v, %v, want this process and the default idle time", status, err)
	}

	if err := c.Lock(ctx); err != nil {
		t.Fatalf("Lock() error = %v", err)
	}
	waitServed(t, done)
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the socket is still there after Lock(): %v", err)
	}
	if _, err := c.Status(ctx); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Status() after Lock() error = %v, want ErrNotRunning", err)
	}
	if err := c.Lock(ctx); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Lock() without a process error = %v, want ErrNotRunning", err)
	}
}

func TestIdleLock(t *testing.T) {
	path := socketIn(t)
	l, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	s := NewServer(testKey, testSecrets(), testBindings())
	s.IdleTimeout = 500 * time.Millisecond
	done := serve(t, s, l)
	c := NewClient(path, testRecipient)

	// Each get restarts the idle period, so the server outlives several of them.
	for range 5 {
		if _, _, err := c.Get(context.Background(), "wiki-reader", "token", *testScope()); err != nil {
			t.Fatalf("Get() while in use error = %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	waitServed(t, done)
	if _, err := c.Status(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Status() after the idle time error = %v, want ErrNotRunning", err)
	}
}

func TestListenServesOnlyOnce(t *testing.T) {
	path := socketIn(t)
	first, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	if _, err := Listen(path); !errors.Is(err, ErrRunning) {
		t.Fatalf("second Listen() error = %v, want ErrRunning", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("the second Listen() removed the first socket: %v", err)
	}
	_ = first.Close()
	again, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen() after Close() error = %v", err)
	}
	_ = again.Close()
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

func TestClientRefusesAnotherProgramAsServer(t *testing.T) {
	path := socketIn(t)
	if err := privateDir(filepath.Dir(path)); err != nil {
		t.Fatalf("privateDir() error = %v", err)
	}
	cmd, out := helper(t, otherProgram(t), "listen", path)
	if got := line(t, out); got != "listening" {
		t.Fatalf("the helper process said %q", got)
	}

	_, _, err := NewClient(path, testRecipient).Get(context.Background(), "wiki-reader", "token", *testScope())
	var peer *PeerError
	if !errors.Is(err, ErrRefused) || !errors.As(err, &peer) || peer.PID != cmd.Process.Pid ||
		!strings.Contains(err.Error(), fmt.Sprintf("(process %d)", cmd.Process.Pid)) {
		t.Fatalf("Get() from another program error = %v, want ErrRefused naming its process", err)
	}
	// Where the client can check the program of the process that listens, it does not even send the
	// challenge; elsewhere the challenge is all it sends.
	want := "received 0"
	if !hardenHidesProgram {
		want = "no challenge"
	}
	if got := line(t, out); got != want {
		t.Fatalf("the client sent something to another program: %s, want %s", got, want)
	}
}

func TestServerRefusesAnotherProgramAsClient(t *testing.T) {
	path := socketIn(t)
	l, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	s := NewServer(testKey, testSecrets(), testBindings())
	serve(t, s, l)

	// Another program does not even get the challenge answered: the key does not work for it.
	_, out := helper(t, otherProgram(t), "get", path)
	if got := line(t, out); got != fmt.Sprintf("answer %q 0", codeRefused) {
		t.Fatalf("another program got %s", got)
	}
}

// A server that refuses this program, as the process an update left behind does, is named by its process
// id, so a person can end what cannot be asked to lock.
func TestClientNamesAServerThatRefusesIt(t *testing.T) {
	path := socketIn(t)
	l, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	s := NewServer(testKey, testSecrets(), testBindings())
	s.Verify = func(net.Conn) error { return fmt.Errorf("%w: it runs another program", ErrRefused) }
	serve(t, s, l)

	err = NewClient(path, testRecipient).Lock(context.Background())
	var peer *PeerError
	if !errors.Is(err, ErrRefused) || !errors.As(err, &peer) || peer.PID != os.Getpid() {
		t.Fatalf("Lock() refused by the server error = %v, want ErrRefused naming process %d", err, os.Getpid())
	}
	if s.stopping() {
		t.Fatalf("a refused lock stopped the server")
	}
}

func TestHarden(t *testing.T) {
	_, out := helper(t, os.Args[0], "harden", "")
	if got := line(t, out); got != wantHardened {
		t.Fatalf("the hardened helper process said %q", got)
	}
}

// A hardened vault process cannot be checked by its program from outside on Linux, where it passes the
// client's check of its user and its key; on macOS its program is checked as well. Either way it still
// checks its own clients by their program.
func TestClientReachesAHardenedServer(t *testing.T) {
	path := socketIn(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), helperEnv+"=serve-hardened", helperEnv+"_SOCKET="+path)
	cmd.Stdin = strings.NewReader(testKey.String() + "\n")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start a helper process: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	out := bufio.NewReader(stdout)
	if got := line(t, out); got != "listening" {
		t.Fatalf("the helper process said %q", got)
	}

	err = checkProcess(cmd.Process.Pid, uint32(os.Getuid()))
	if hardenHidesProgram && !errors.Is(err, ErrRefused) {
		t.Fatalf("checkProcess() of a hardened process error = %v, want ErrRefused", err)
	}
	if !hardenHidesProgram && err != nil {
		t.Fatalf("checkProcess() of a hardened process error = %v, want it to pass", err)
	}
	c := NewClient(path, testRecipient)
	ctx := context.Background()
	status, err := c.Status(ctx)
	if err != nil || status.PID != cmd.Process.Pid {
		t.Fatalf("Status() = %+v, %v, want the helper's pid", status, err)
	}
	if value, found, err := c.Get(ctx, "wiki-reader", "token", *testScope()); err != nil || !found || value != "synthetic-token" {
		t.Fatalf("Get() = %q, %v, %v", value, found, err)
	}
	if _, err := NewClient(path, otherKey.Recipient().String()).Status(ctx); !errors.Is(err, ErrRefused) {
		t.Fatalf("Status() challenged for another vault error = %v, want ErrRefused", err)
	}
	if err := c.Lock(ctx); err != nil {
		t.Fatalf("Lock() error = %v", err)
	}
	if got := line(t, out); got != "locked" {
		t.Fatalf("the helper process said %q after Lock()", got)
	}
}
