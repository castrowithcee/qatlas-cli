//go:build windows

package vaultproc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// socketIn returns a fresh vault pipe name that no other test uses.
func socketIn(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return pipePrefix + "qatlas-vault-test-" + hex.EncodeToString(b[:])
}

// rawListen listens at path the way a server without the vault's key would. A pipe carries no key either
// way, so it is Listen itself.
func rawListen(path string) (net.Listener, error) { return Listen(path) }

// rawDial connects to path the way a client that checks nothing would.
func rawDial(path string) (net.Conn, error) {
	return dial(context.Background(), path, time.Now().Add(5*time.Second))
}

// prepareSocketDir has nothing to prepare: a pipe lives in no directory.
func prepareSocketDir(*testing.T, string) {}

// checkPeerProcess checks the process pid as the server checks a client.
func checkPeerProcess(pid int) error { return checkProcess(uint32(pid)) }

// checkSocketPrivate checks that the pipe at path allows this user alone and denies network logons.
func checkSocketPrivate(t *testing.T, path string) {
	t.Helper()
	conn, err := rawDial(path)
	if err != nil {
		t.Fatalf("dial() error = %v", err)
	}
	defer conn.Close()
	sd, err := windows.GetSecurityInfo(conn.(*pipeConn).h, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetSecurityInfo() error = %v", err)
	}
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	// The entries are compared by security identifier: SDDL text renders the built-in administrator as an
	// alias, which this user may be.
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 2 || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("the pipe's DACL is %s, want a protected one with a denial for network logons and an entry for this user", sd)
	}
	network, err := windows.CreateWellKnownSid(windows.WinNetworkSid)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []struct {
		kind byte
		sid  *windows.SID
	}{{windows.ACCESS_DENIED_ACE_TYPE, network}, {windows.ACCESS_ALLOWED_ACE_TYPE, sid}} {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != want.kind || !want.sid.Equals((*windows.SID)(unsafe.Pointer(&ace.SidStart))) {
			t.Fatalf("the pipe's DACL is %s, want this user alone and network logons denied", sd)
		}
	}
}

// checkSocketPresent has nothing to look at: a pipe exists as long as its listener, which the test checks
// by listening again once it closed.
func checkSocketPresent(*testing.T, string) {}

// checkSocketGone checks that nothing answers at path any more.
func checkSocketGone(t *testing.T, path string) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); ; {
		conn, err := rawDial(path)
		if err != nil {
			if !notRunning(err) {
				t.Fatalf("dial() after Lock() error = %v, want the pipe gone", err)
			}
			return
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			t.Fatalf("the pipe is still there after Lock()")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPipePath(t *testing.T) {
	dir := shortTempDir(t)
	vaultDir := filepath.Join(dir, ".qatlas", "cli", "vault")
	path, err := SocketPath(vaultDir)
	if err != nil {
		t.Fatalf("SocketPath() error = %v", err)
	}
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, pipePrefix+"qatlas-vault-"+sid.String()+"-") || checkPathLength(path) != nil {
		t.Fatalf("SocketPath() = %s, want a pipe named after this user", path)
	}
	if upper, err := SocketPath(strings.ToUpper(vaultDir)); err != nil || upper != path {
		t.Fatalf("SocketPath() of the same vault in other case = %s, %v, want %s", upper, err, path)
	}
	if other, err := SocketPath(filepath.Join(dir, "vault")); err != nil || other == path {
		t.Fatalf("SocketPath() of another vault = %s, %v, want a pipe of its own", other, err)
	}
	if _, err := NewClient(path, testRecipient).Status(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Status() without a process error = %v, want ErrNotRunning", err)
	}
}

// A pipe that exists already, whoever created it, keeps a vault process from serving the name beside it.
func TestListenRefusesAPipeThatExists(t *testing.T) {
	path := socketIn(t)
	squatter, err := createPipe(path, true, nil)
	if err != nil {
		t.Fatalf("createPipe() error = %v", err)
	}
	if _, err := Listen(path); !errors.Is(err, ErrRunning) {
		t.Fatalf("Listen() beside an existing pipe error = %v, want ErrRunning", err)
	}
	_ = windows.CloseHandle(squatter)
	l, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen() once the pipe is gone error = %v", err)
	}
	_ = l.Close()

	if _, err := Listen(filepath.Join(t.TempDir(), "v.sock")); err == nil {
		t.Fatalf("Listen() accepted a path that is no pipe")
	}
}

// accepted listens at a fresh pipe and returns both ends of one connection.
func accepted(t *testing.T) (server, client net.Conn) {
	t.Helper()
	path := socketIn(t)
	l, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	conns := make(chan net.Conn, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			conns <- nil
			return
		}
		conns <- conn
	}()
	client, err = rawDial(path)
	if err != nil {
		t.Fatalf("dial() error = %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	server = <-conns
	if server == nil {
		t.Fatalf("Accept() failed")
	}
	t.Cleanup(func() { _ = server.Close() })
	return server, client
}

// A deadline ends a read that waits, whether it passes on its own or is moved into the past, and Close ends
// one as well.
func TestPipeDeadlines(t *testing.T) {
	server, client := accepted(t)

	_ = client.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	start := time.Now()
	if _, err := client.Read(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatalf("Read() past its deadline error = %v after %v, want os.ErrDeadlineExceeded", err, time.Since(start))
	}

	_ = client.SetReadDeadline(time.Time{})
	done := make(chan error, 1)
	go func() {
		_, err := client.Read(make([]byte, 1))
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	_ = client.SetReadDeadline(time.Unix(1, 0))
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("Read() whose deadline moved into the past error = %v, want os.ErrDeadlineExceeded", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("Read() did not end when its deadline moved into the past")
	}

	go func() {
		_, err := server.Read(make([]byte, 1))
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	_ = server.Close()
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("Read() of a closed end error = %v, want net.ErrClosed", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("Read() did not end when its end closed")
	}
}

// What one end wrote before it closed stays readable at the other, which then reads an end of file; a
// write to an end whose peer is gone fails as a broken pipe.
func TestPipeKeepsWhatWasWrittenBeforeClose(t *testing.T) {
	server, client := accepted(t)
	if err := writeMessage(server, response{V: Version, Error: codeLocked}); err != nil {
		t.Fatalf("writeMessage() error = %v", err)
	}
	_ = server.Close()
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	var resp response
	if err := readMessage(client, &resp); err != nil || resp.Error != codeLocked {
		t.Fatalf("readMessage() after the server closed = %+v, %v, want its answer", resp, err)
	}
	if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("Read() after the answer error = %v, want io.EOF", err)
	}
	if _, err := client.Write([]byte("x\n")); err == nil {
		t.Fatalf("Write() to a closed pipe succeeded")
	}
}

func TestCheckProcess(t *testing.T) {
	if err := checkProcess(uint32(os.Getpid())); err != nil {
		t.Fatalf("checkProcess() of this process error = %v", err)
	}
	if err := checkProcess(0); !errors.Is(err, ErrRefused) {
		t.Fatalf("checkProcess() of pid 0 error = %v, want ErrRefused", err)
	}

	program := otherProgram(t)
	cmd, _ := helper(t, program, "sleep", "")
	err := checkProcess(uint32(cmd.Process.Pid))
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "another program") {
		t.Fatalf("checkProcess() of another program error = %v, want ErrRefused for another program", err)
	}

	// A running program cannot be removed on Windows, but an update can rename it and put another file at
	// its path; the process left behind is refused all the same.
	if err := os.Rename(program, program+".old"); err != nil {
		t.Fatal(err)
	}
	if err := checkProcess(uint32(cmd.Process.Pid)); !errors.Is(err, ErrRefused) {
		t.Fatalf("checkProcess() of a renamed program error = %v, want ErrRefused", err)
	}
}

// A process whose own program was renamed and replaced since it started refuses every peer, itself
// included.
func TestCheckProcessRefusesWhenItsOwnProgramWasReplaced(t *testing.T) {
	program := otherProgram(t)
	_, out := helper(t, program, "check-self", "")
	if got := line(t, out); got != "ready" {
		t.Fatalf("the helper process said %q", got)
	}
	if err := os.Rename(program, program+".old"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(program + ".old")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program, data, 0o700); err != nil {
		t.Fatal(err)
	}
	got := line(t, out)
	if !strings.Contains(got, "removed or replaced") {
		t.Fatalf("the helper whose program was replaced said %q, want a refusal", got)
	}
}

// init runs the helper process of TestCheckProcessRefusesWhenItsOwnProgramWasReplaced before any test: it
// reports that it started, waits until the test replaced its program, and then checks itself.
func init() {
	if os.Getenv(helperEnv) != "check-self" {
		return
	}
	fmt.Println("ready")
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if id, err := idOf(ownProgram.path); err != nil || id != ownProgram.id {
			break
		}
	}
	fmt.Println(checkProcess(uint32(os.Getpid())))
	os.Exit(0)
}
