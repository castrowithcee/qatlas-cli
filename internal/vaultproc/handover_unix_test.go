//go:build linux || darwin

package vaultproc

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"golang.org/x/crypto/ssh"

	"github.com/castrowithcee/qatlas-cli/internal/filelock"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/release"
	"github.com/castrowithcee/qatlas-cli/internal/release/releasetest"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// These tests run the old vault process in the test process, with the program it was started from played by
// a copy of the test binary in a directory of the test's own: replacing that copy, as an update does, is what
// the server is told through its ownProgram seam. The successor is a real process started from that path, the
// test binary as a helper process in mode successor, which takes the vault over the way 'qatlas vault serve
// --successor' does. Both ends of every connection accept each other (allow): the peer check of a handover is
// the connection's own, already covered elsewhere.

const handoverArchive = "qatlas_9.9.9_test.tar.gz"

// handoverHelper is what a helper process started as a successor runs, for the modes TestHelperProcess hands
// on: successor takes the vault over like the real one; successor-no-socket reports ready without listening,
// as a successor whose socket went missing.
func handoverHelper(mode, socket string) {
	report := os.NewFile(4, "report")
	data, _ := io.ReadAll(io.LimitReader(os.NewFile(3, "handover"), 16<<20))
	var snap vault.Snapshot
	if err := json.Unmarshal(data, &snap); err != nil || snap.LocksAt == nil {
		fmt.Fprintln(report, "the vault handed over cannot be read")
		os.Exit(1)
	}
	if mode == "successor-no-socket" {
		_ = os.WriteFile(NextPath(socket)+".lock", nil, 0o600)
		fmt.Fprintln(report, "ready")
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	if err := Harden(); err != nil {
		fmt.Fprintln(report, err)
		os.Exit(1)
	}
	key, err := age.ParseX25519Identity(snap.Identity)
	if err != nil {
		fmt.Fprintln(report, "the vault key cannot be read")
		os.Exit(1)
	}
	l, err := Listen(NextPath(socket))
	if err != nil {
		fmt.Fprintln(report, err)
		os.Exit(1)
	}
	s := NewServer(key, snap.Secrets, snap.Bindings)
	s.Verify = allow
	s.LocksAt = *snap.LocksAt
	fmt.Fprintln(report, "ready")
	_ = report.Close()
	if err := AwaitHandover(context.Background(), l, socket, HandoverTimeout); err != nil {
		fmt.Println("not handed over")
		_ = s.Close()
	}
	if err := s.Serve(l); err != nil {
		fmt.Println("error", err)
		os.Exit(1)
	}
	fmt.Println("locked")
	os.Exit(0)
}

// handoverFixture is an unlocked vault on disk served by a server in this process, which keeps its log and
// may hand over, with a signed release whose archive holds the program the server was started from.
type handoverFixture struct {
	t        *testing.T
	vault    *vault.Vault
	server   *Server
	served   <-chan error
	client   *Client
	socket   string
	program  string
	replaced atomic.Bool
	release  ReleaseFiles
	scope    vault.Scope
	// mode is the helper mode a successor is started in; starts counts the successors started, and
	// successor and output are the last one and its standard output.
	mode      string
	starts    atomic.Int32
	mu        sync.Mutex
	successor *exec.Cmd
	output    *bufio.Reader
	ended     chan struct{}
}

func newHandoverFixture(t *testing.T) *handoverFixture {
	t.Helper()
	f := &handoverFixture{t: t, mode: "successor", scope: tokenVorbild()}
	f.vault = vault.New(t.TempDir())
	if err := f.vault.Set("gh-a", "user", "synthetic-value", func(string) (string, error) { return "fixture-phrase", nil }); err != nil {
		t.Fatalf("Set() = %v", err)
	}
	if err := f.vault.Approve([]vault.Scope{f.scope}); err != nil {
		t.Fatalf("Approve() = %v", err)
	}
	snap, err := f.vault.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	key, err := age.ParseX25519Identity(snap.Identity)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	f.program = filepath.Join(dir, "qatlas")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.program, program, 0o700); err != nil {
		t.Fatal(err)
	}
	signer := releasetest.NewSigner(t)
	f.release = writeRelease(t, dir, program, signer, nil)

	f.socket = socketIn(t)
	l, err := Listen(f.socket)
	if err != nil {
		t.Fatalf("Listen() = %v", err)
	}
	f.server = NewServer(key, snap.Secrets, snap.Bindings)
	f.server.Verify = allow
	f.server.KeepLog(f.vault.Dir(), 90)
	f.server.ownProgram = func() (string, bool) { return f.program, f.replaced.Load() }
	f.server.AllowHandover(f.vault.Dir(), []ssh.PublicKey{signer.PublicKey()}, f.start)
	f.served = serve(t, f.server, l)
	f.client = &Client{Path: f.socket, Recipient: key.Recipient().String(), Verify: allow}
	return f
}

// writeRelease writes a release into dir whose archive holds program as bin/qatlas, its checksums.txt, and
// its signature by signer. change, if set, changes the archive after its checksum was listed.
func writeRelease(t *testing.T, dir string, program []byte, signer ssh.Signer, change func([]byte) []byte) ReleaseFiles {
	t.Helper()
	var archive bytes.Buffer
	gz, _ := gzip.NewWriterLevel(&archive, gzip.BestSpeed)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "bin/qatlas", Mode: 0o755, Size: int64(len(program)),
		Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(program); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive.Bytes())
	checksums := []byte(hex.EncodeToString(sum[:]) + "  " + handoverArchive + "\n")
	data := archive.Bytes()
	if change != nil {
		data = change(data)
	}
	files := ReleaseFiles{Checksums: filepath.Join(dir, "checksums.txt"), Signature: filepath.Join(dir, "checksums.txt.sig"),
		Archive: filepath.Join(dir, handoverArchive), ArchiveName: handoverArchive}
	for path, content := range map[string][]byte{files.Checksums: checksums, files.Archive: data,
		files.Signature: releasetest.Sign(t, signer, release.Namespace, "sha256", checksums)} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// replace puts content at the program's path the way an update does, by renaming a new file over it.
func (f *handoverFixture) replace(content []byte) {
	f.t.Helper()
	next := f.program + ".new"
	if err := os.WriteFile(next, content, 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(next, f.program); err != nil {
		f.t.Fatal(err)
	}
	f.replaced.Store(true)
}

// replaceWithRelease replaces the program with the one the release holds, a copy of the test binary.
func (f *handoverFixture) replaceWithRelease() {
	f.t.Helper()
	self, err := os.Executable()
	if err != nil {
		f.t.Fatal(err)
	}
	program, err := os.ReadFile(self)
	if err != nil {
		f.t.Fatal(err)
	}
	f.replace(program)
}

// start is the server's StartSuccessor: it starts the program it is given as a helper process, the way
// StartSuccessorProcess starts 'qatlas vault serve --successor', and ends it on any failure.
func (f *handoverFixture) start(ctx context.Context, program string, snap vault.Snapshot) (int, func(), error) {
	f.starts.Add(1)
	if program != f.program {
		return 0, nil, fmt.Errorf("the successor is started from %s, want %s", program, f.program)
	}
	handoverRead, handoverWrite, err := os.Pipe()
	if err != nil {
		return 0, nil, err
	}
	reportRead, reportWrite, err := os.Pipe()
	if err != nil {
		return 0, nil, err
	}
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		return 0, nil, err
	}
	cmd := exec.Command(program, "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), helperEnv+"="+f.mode, helperEnv+"_SOCKET="+f.socket)
	cmd.ExtraFiles = []*os.File{handoverRead, reportWrite}
	cmd.Stdout = outWrite
	err = cmd.Start()
	_ = handoverRead.Close()
	_ = reportWrite.Close()
	_ = outWrite.Close()
	if err != nil {
		return 0, nil, err
	}
	ended := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(ended)
	}()
	end := func() {
		_ = cmd.Process.Kill()
		<-ended
	}
	f.t.Cleanup(end)
	f.mu.Lock()
	f.successor, f.output, f.ended = cmd, bufio.NewReader(outRead), ended
	f.mu.Unlock()

	data, _ := json.Marshal(snap)
	_, err = handoverWrite.Write(data)
	_ = handoverWrite.Close()
	if err != nil {
		end()
		return 0, nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = reportRead.SetReadDeadline(deadline)
	}
	report, _ := bufio.NewReader(reportRead).ReadString('\n')
	_ = reportRead.Close()
	if strings.TrimSpace(report) != "ready" {
		end()
		return 0, nil, fmt.Errorf("the successor did not start: %q", report)
	}
	return cmd.Process.Pid, end, nil
}

// prepare prepares a handover with the fixture's release and fails the test unless the server agrees.
func (f *handoverFixture) prepare() *Handover {
	f.t.Helper()
	behaviour, h, err := f.client.PrepareHandover(context.Background(), f.release)
	if err != nil || behaviour != vault.UpdateHandover || h == nil {
		f.t.Fatalf("PrepareHandover() = %q, %v, %v, want a handover", behaviour, h, err)
	}
	f.t.Cleanup(func() { _ = h.Close() })
	return h
}

// assertLocked checks that the server locked itself without a successor and left nothing beside its socket.
func (f *handoverFixture) assertLocked() {
	f.t.Helper()
	waitServed(f.t, f.served)
	if _, err := f.client.Status(context.Background()); !errors.Is(err, ErrNotRunning) {
		f.t.Fatalf("Status() after a failed handover error = %v, want ErrNotRunning", err)
	}
	for _, path := range []string{f.socket, NextPath(f.socket), NextPath(f.socket) + ".lock"} {
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			f.t.Errorf("%s is left after a failed handover: %v", filepath.Base(path), err)
		}
	}
	f.mu.Lock()
	ended := f.ended
	f.mu.Unlock()
	if ended != nil {
		select {
		case <-ended:
		case <-time.After(5 * time.Second):
			f.t.Errorf("the successor still runs after a failed handover")
		}
	}
}

// handoverLog returns the results the vault.handover entries of the log carry, and fails the test unless
// every entry is signed.
func (f *handoverFixture) handoverLog() []string {
	f.t.Helper()
	var results []string
	for _, line := range strings.Split(strings.TrimSpace(logText(f.t, f.vault.Dir())), "\n") {
		var entry invokelog.Entry
		if line == "" || json.Unmarshal([]byte(line), &entry) != nil || entry.Operation != handoverOperation {
			continue
		}
		if entry.MAC == "" {
			f.t.Errorf("the vault.handover entry %q is not signed", line)
		}
		results = append(results, entry.Result)
	}
	return results
}

// committed is the outcome of a Commit.
type committed struct {
	pid int
	err error
}

// commitHeld prepares a handover, replaces the program with the release's, and commits while a log request,
// waiting for the log's lock held here, is in flight. It returns once the commit began: release lets the
// request finish and returns its error, and the commit's outcome then arrives on the channel.
func (f *handoverFixture) commitHeld() (func() error, <-chan committed) {
	f.t.Helper()
	ctx := context.Background()
	h := f.prepare()
	f.replaceWithRelease()
	logs := filepath.Join(f.vault.Dir(), "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		f.t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(logs, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = lock.Close() })
	if err := filelock.Lock(lock); err != nil {
		f.t.Fatal(err)
	}
	logged := make(chan error, 1)
	go func() {
		logged <- f.client.Log(ctx, invokelog.Fields{Path: "cli", Operation: "bookstack.pages.list", Effect: "read",
			Result: "success"})
	}()
	waitUntil(f.t, "the log request is in flight", func() bool {
		f.server.mu.Lock()
		defer f.server.mu.Unlock()
		return f.server.inFlight == 1
	})
	commit := make(chan committed, 1)
	go func() {
		pid, err := h.Commit(ctx)
		commit <- committed{pid, err}
	}()
	waitUntil(f.t, "the handover is committed", func() bool {
		f.server.mu.Lock()
		defer f.server.mu.Unlock()
		return f.server.handingOver
	})
	release := func() error {
		if err := filelock.Unlock(lock); err != nil {
			return err
		}
		return <-logged
	}
	return release, commit
}

// countingClient returns a client of the fixture's socket and the number of connections it opened.
func (f *handoverFixture) countingClient() (*Client, *atomic.Int32) {
	var attempts atomic.Int32
	c := *f.client
	c.Verify = func(net.Conn) error {
		attempts.Add(1)
		return nil
	}
	return &c, &attempts
}

func waitUntil(t *testing.T, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !done(); {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A release the vault process cannot verify itself ends the handover before it begins: the process locks
// itself, starts nothing, and logs why.
func TestHandoverPrepareRefusesAnUnverifiedRelease(t *testing.T) {
	for name, files := range map[string]func(*handoverFixture) ReleaseFiles{
		"signed by another key": func(f *handoverFixture) ReleaseFiles {
			return writeRelease(t, t.TempDir(), []byte("program"), releasetest.NewSigner(t), nil)
		},
		"archive not the one listed": func(f *handoverFixture) ReleaseFiles {
			files := f.release
			data, err := os.ReadFile(files.Archive)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(files.Archive, append(data, 0), 0o600); err != nil {
				t.Fatal(err)
			}
			return files
		},
		"relative path": func(f *handoverFixture) ReleaseFiles {
			files := f.release
			files.Checksums = "checksums.txt"
			return files
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newHandoverFixture(t)
			behaviour, h, err := f.client.PrepareHandover(context.Background(), files(f))
			if !errors.Is(err, ErrHandover) || h != nil || behaviour != "" || !strings.Contains(err.Error(), "verify the release") {
				t.Fatalf("PrepareHandover() = %q, %v, %v, want ErrHandover for the release", behaviour, h, err)
			}
			f.assertLocked()
			if n := f.starts.Load(); n != 0 {
				t.Fatalf("%d successors were started", n)
			}
			if got := f.handoverLog(); len(got) != 1 || got[0] != codeReleaseUnverified {
				t.Fatalf("the log holds handovers %v, want one %s", got, codeReleaseUnverified)
			}
		})
	}
}

// A vault that asks an update to lock it is locked at the prepare, with the answer lock and no successor.
func TestHandoverPrepareLocksWhenTheVaultSaysSo(t *testing.T) {
	f := newHandoverFixture(t)
	if err := f.vault.SetUpdateBehaviour(vault.UpdateLock); err != nil {
		t.Fatal(err)
	}
	behaviour, h, err := f.client.PrepareHandover(context.Background(), f.release)
	if err != nil || behaviour != vault.UpdateLock || h != nil {
		t.Fatalf("PrepareHandover() = %q, %v, %v, want lock", behaviour, h, err)
	}
	f.assertLocked()
	if n := f.starts.Load(); n != 0 {
		t.Fatalf("%d successors were started", n)
	}
}

// A commit whose program was not replaced, or was replaced by another program than the release holds, starts
// no successor and locks.
func TestHandoverCommitRefusesAProgramThatIsNotTheRelease(t *testing.T) {
	for name, tc := range map[string]struct {
		replace func(*handoverFixture)
		code    string
	}{
		"not replaced": {func(*handoverFixture) {}, codeProgramUnchanged},
		"another program": {func(f *handoverFixture) { f.replace([]byte("#!/bin/sh\nexit 0\n")) },
			codeProgramMismatch},
	} {
		t.Run(name, func(t *testing.T) {
			f := newHandoverFixture(t)
			h := f.prepare()
			tc.replace(f)
			pid, err := h.Commit(context.Background())
			if !errors.Is(err, ErrHandover) || pid != 0 {
				t.Fatalf("Commit() = %d, %v, want ErrHandover", pid, err)
			}
			f.assertLocked()
			if n := f.starts.Load(); n != 0 {
				t.Fatalf("%d successors were started", n)
			}
			if got := f.handoverLog(); len(got) != 1 || got[0] != tc.code {
				t.Fatalf("the log holds handovers %v, want one %s", got, tc.code)
			}
		})
	}
}

// A successor that reports ready without its socket is ended, what it left beside the socket is removed, and
// the old process locks.
func TestHandoverFallsBackWhenTheSuccessorCannotTakeOver(t *testing.T) {
	f := newHandoverFixture(t)
	f.mode = "successor-no-socket"
	h := f.prepare()
	f.replaceWithRelease()
	pid, err := h.Commit(context.Background())
	if !errors.Is(err, ErrHandover) || pid != 0 {
		t.Fatalf("Commit() = %d, %v, want ErrHandover", pid, err)
	}
	if n := f.starts.Load(); n != 1 {
		t.Fatalf("%d successors were started, want 1", n)
	}
	f.assertLocked()
	if got := f.handoverLog(); len(got) != 1 || got[0] != codeHandoverFailed {
		t.Fatalf("the log holds handovers %v, want one %s", got, codeHandoverFailed)
	}
}

// A prepared handover whose client goes away before it commits locks the process.
func TestHandoverEndsWithoutACommit(t *testing.T) {
	f := newHandoverFixture(t)
	h := f.prepare()
	_ = h.Close()
	f.assertLocked()
}

// The successor takes the vault over: a request in flight at the commit is answered by the old process, one
// that arrives after it is answered replaced, and asked again, the successor answers it, with the time to lock
// at the old process had. The old process ends; the socket and its lock at the vault socket's path are the
// successor's, nothing is left beside them, and the successor removes the socket once it locks.
func TestHandoverToASuccessor(t *testing.T) {
	f := newHandoverFixture(t)
	ctx := context.Background()
	before, err := f.client.Status(ctx)
	if err != nil || before.PID != os.Getpid() {
		t.Fatalf("Status() = %+v, %v", before, err)
	}
	if before.Update != vault.UpdateHandover || before.SettingsUntrusted {
		t.Fatalf("Status() = %+v, want the update setting handover", before)
	}
	release, commit := f.commitHeld()

	// A request that arrives after the commit is answered replaced, and asked again until the successor
	// answers. It is a status: a get would restart the idle period, which is checked below.
	c, attempts := f.countingClient()
	type statusResult struct {
		status Status
		err    error
	}
	got := make(chan statusResult, 1)
	go func() {
		status, err := c.Status(ctx)
		got <- statusResult{status, err}
	}()
	waitUntil(t, "the status was answered replaced and asked again", func() bool { return attempts.Load() >= 2 })
	if n := f.starts.Load(); n != 0 {
		t.Fatalf("a successor was started while a request was in flight")
	}
	if err := release(); err != nil {
		t.Fatalf("Log() in flight at the commit = %v, want it answered", err)
	}
	result := <-commit
	if result.err != nil || result.pid <= 0 || result.pid == os.Getpid() {
		t.Fatalf("Commit() = %d, %v, want the successor's process id", result.pid, result.err)
	}
	if asked := <-got; asked.err != nil || asked.status.PID != result.pid {
		t.Fatalf("Status() asked again after the handover = %+v, %v, want the successor's answer", asked.status, asked.err)
	}
	waitServed(t, f.served)

	after, err := f.client.Status(ctx)
	if err != nil || after.PID != result.pid || !after.LocksAt.Equal(before.LocksAt) {
		t.Fatalf("Status() after the handover = %+v, %v, want process %d locking at %v", after, err, result.pid,
			before.LocksAt)
	}
	if value, found, err := f.client.Get(ctx, "gh-a", "user", f.scope); err != nil || !found || value != "synthetic-value" {
		t.Fatalf("Get() from the successor = %q, %v, %v", value, found, err)
	}
	for _, path := range []string{f.socket, f.socket + ".lock"} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("%s is missing after the handover: %v", filepath.Base(path), err)
		}
	}
	for _, path := range []string{NextPath(f.socket), NextPath(f.socket) + ".lock"} {
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s is left after the handover: %v", filepath.Base(path), err)
		}
	}
	if _, err := Listen(f.socket); !errors.Is(err, ErrRunning) {
		t.Fatalf("Listen() at the handed over socket error = %v, want ErrRunning", err)
	}
	if _, err := f.client.Status(ctx); err != nil {
		t.Fatalf("Status() after a refused Listen() = %v, want the successor still reachable", err)
	}

	// The handover is in the log, signed and chained after the request that was in flight; the successor,
	// which holds the vault's key, checks every check value.
	if got := f.handoverLog(); len(got) != 1 || got[0] != "success" {
		t.Fatalf("the log holds handovers %v, want one success", got)
	}
	report, err := invokelog.VerifyWith(f.vault.Dir(), f.client.LogChecker(ctx))
	if err != nil || report.Broken || len(report.Days) != 1 || report.Days[0].Unchecked != 0 || report.Days[0].Changed != 0 {
		t.Fatalf("VerifyWith() = %+v, %v, want an intact, checked log", report, err)
	}

	if err := f.client.Lock(ctx); err != nil {
		t.Fatalf("Lock() of the successor = %v", err)
	}
	f.mu.Lock()
	out := f.output
	f.mu.Unlock()
	if got := line(t, out); got != "locked" {
		t.Fatalf("the successor said %q after Lock()", got)
	}
	if _, err := os.Lstat(f.socket); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the socket is still there after the successor locked: %v", err)
	}
}

// A process whose own program an update replaced refuses every client; it answers replaced, which still
// counts as the refusal of a program where nothing takes the vault over.
func TestServerAnswersReplacedOnceItsProgramIsReplaced(t *testing.T) {
	path := socketIn(t)
	l, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen() = %v", err)
	}
	s := NewServer(testKey, testSecrets(), testBindings())
	s.Verify = refuse
	s.ownProgram = func() (string, bool) { return "/opt/qatlas/bin/qatlas", true }
	serve(t, s, l)
	var attempts atomic.Int32
	c := NewClient(path, testRecipient)
	c.Verify = func(net.Conn) error {
		attempts.Add(1)
		return nil
	}
	began := time.Now()
	_, _, err = c.Get(context.Background(), "wiki-reader", "token", *testScope())
	if !errors.Is(err, ErrReplaced) || !errors.Is(err, ErrProgramRefused) {
		t.Fatalf("Get() from a replaced process error = %v, want ErrReplaced", err)
	}
	// Without a successor the client gives up after about two seconds of asking again.
	if took := time.Since(began); attempts.Load() < 2 || took > replacedLimit+time.Second {
		t.Fatalf("Get() asked %d times in %v, want it asked again within %v", attempts.Load(), took, replacedLimit)
	}

	// Any other refusal is final at once.
	s.ownProgram = func() (string, bool) { return "", false }
	attempts.Store(0)
	if _, _, err := c.Get(context.Background(), "wiki-reader", "token", *testScope()); !errors.Is(err, ErrProgramRefused) ||
		errors.Is(err, ErrReplaced) || attempts.Load() != 1 {
		t.Fatalf("Get() refused = %v after %d attempts, want one refusal", err, attempts.Load())
	}
}

// A lock that reaches the old process after the commit is answered replaced and asked again, which locks the
// successor: the vault is locked in the end, never left unlocked by a lock that went nowhere.
func TestHandoverLockDuringTheHandoverLocksTheSuccessor(t *testing.T) {
	f := newHandoverFixture(t)
	release, commit := f.commitHeld()
	c, attempts := f.countingClient()
	locked := make(chan error, 1)
	go func() { locked <- c.Lock(context.Background()) }()
	waitUntil(t, "the lock was answered replaced and asked again", func() bool { return attempts.Load() >= 2 })
	if err := release(); err != nil {
		t.Fatalf("Log() in flight at the commit = %v", err)
	}
	if result := <-commit; result.err != nil {
		t.Fatalf("Commit() = %d, %v", result.pid, result.err)
	}
	if err := <-locked; err != nil {
		t.Fatalf("Lock() during the handover = %v, want the successor locked", err)
	}
	waitServed(t, f.served)
	f.mu.Lock()
	out := f.output
	f.mu.Unlock()
	if got := line(t, out); got != "locked" {
		t.Fatalf("the successor said %q after the lock", got)
	}
	if _, err := f.client.Status(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Status() after the lock error = %v, want ErrNotRunning", err)
	}
}

// Status reports the update setting as the vault process reads it with its own key: lock where the vault
// says so, and lock with a warning where settings.age cannot be trusted.
func TestStatusReportsTheUpdateSetting(t *testing.T) {
	f := newHandoverFixture(t)
	ctx := context.Background()
	if err := f.vault.SetUpdateBehaviour(vault.UpdateLock); err != nil {
		t.Fatal(err)
	}
	if status, err := f.client.Status(ctx); err != nil || status.Update != vault.UpdateLock || status.SettingsUntrusted {
		t.Fatalf("Status() = %+v, %v, want lock", status, err)
	}
	if err := os.WriteFile(filepath.Join(f.vault.Dir(), "settings.age"), []byte("not written by this vault"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, err := f.client.Status(ctx); err != nil || status.Update != vault.UpdateLock || !status.SettingsUntrusted {
		t.Fatalf("Status() = %+v, %v, want lock, untrusted", status, err)
	}
}
