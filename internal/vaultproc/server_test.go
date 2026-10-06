package vaultproc

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// allow and refuse stand in for VerifyProgram wherever the test is about the protocol rather than about
// the processes at either end.
func allow(net.Conn) error  { return nil }
func refuse(net.Conn) error { return ErrRefused }

// testKey is the vault key of every test server, and testRecipient its public half, which every test
// client challenges with. otherKey stands for the key of another vault.
var (
	testKey       = mustKey()
	testRecipient = testKey.Recipient().String()
	otherKey      = mustKey()
)

func mustKey() *age.X25519Identity {
	key, err := age.GenerateX25519Identity()
	if err != nil {
		panic(err)
	}
	return key
}

func testSecrets() map[string]map[string]string {
	return map[string]map[string]string{"wiki-reader": {"token": "synthetic-token"}}
}

// testScope is the connection every test get is made for, and testBindings approve it for wiki-reader.
func testScope() *vault.Scope {
	return &vault.Scope{Connection: "wiki", Credential: "wiki-reader", Provider: "bookstack",
		Origin: "https://wiki.example.test", Permissions: []string{"read", "create"}, Targets: []string{"a", "b"}}
}

const testCredentialID = "0123456789abcdef0123456789abcdef"

func testBindings() vault.Bindings {
	return vault.Bindings{
		IDs:       map[string]string{"wiki-reader": testCredentialID},
		Approvals: map[string]string{"wiki": vault.Fingerprint(*testScope(), testCredentialID)},
	}
}

// shortTempDir returns a fresh directory that is removed after the test, short enough for a socket path
// below it. t.TempDir nests the full test name, and on macOS the per-user temporary directory is long
// already, so there it is created in /tmp instead.
func shortTempDir(t *testing.T) string {
	t.Helper()
	base := ""
	if runtime.GOOS == "darwin" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "qv")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func testServer() *Server {
	s := NewServer(testKey, testSecrets(), testBindings())
	s.Verify = allow
	return s
}

func testClient() *Client {
	return &Client{Recipient: testRecipient, Verify: allow}
}

// exchangeOverPipe runs one client request against s over net.Pipe.
func exchangeOverPipe(t *testing.T, s *Server, c *Client, req request) (response, error) {
	t.Helper()
	clientEnd, serverEnd := net.Pipe()
	served := make(chan struct{})
	go func() {
		defer close(served)
		s.serveConn(serverEnd)
	}()
	defer func() {
		_ = clientEnd.Close()
		<-served
	}()
	ctx := context.Background()
	return c.exchange(ctx, clientEnd, c.deadline(ctx), req)
}

func TestServerAnswersEveryOperation(t *testing.T) {
	s := testServer()
	c := testClient()

	resp, err := exchangeOverPipe(t, s, c, request{Op: opGet, Credential: "wiki-reader", Role: "token", Scope: testScope()})
	if err != nil || !resp.Found || resp.Value != "synthetic-token" {
		t.Fatalf("get = %+v, %v, want the stored value", resp, err)
	}
	resp, err = exchangeOverPipe(t, s, c, request{Op: opGet, Credential: "wiki-reader", Role: "other", Scope: testScope()})
	if err != nil || resp.Found || resp.Value != "" {
		t.Fatalf("get of an unknown role = %+v, %v, want not found and no error", resp, err)
	}

	if _, err := exchangeOverPipe(t, s, c, request{Op: opSet, Credential: "crm", Role: "token",
		Value: "synthetic-other"}); err != nil {
		t.Fatalf("set error = %v", err)
	}
	crm := &vault.Scope{Connection: "crm", Credential: "crm", Provider: "twentycrm", Origin: "https://crm.example.test"}
	// A credential set in the process has no entry id the vault bound to a connection yet.
	resp, err = exchangeOverPipe(t, s, c, request{Op: opGet, Credential: "crm", Role: "token", Scope: crm})
	if !errors.Is(err, vault.ErrApprovalRequired) || resp.Value != "" {
		t.Fatalf("get of an unbound credential = %+v, %v, want vault.ErrApprovalRequired", resp, err)
	}
	bindings := testBindings()
	bindings.IDs["crm"] = "fedcba9876543210fedcba9876543210"
	bindings.Approvals["crm"] = vault.Fingerprint(*crm, bindings.IDs["crm"])
	if _, err := exchangeOverPipe(t, s, c, request{Op: opBind, Bindings: &bindings}); err != nil {
		t.Fatalf("bind error = %v", err)
	}
	resp, err = exchangeOverPipe(t, s, c, request{Op: opGet, Credential: "crm", Role: "token", Scope: crm})
	if err != nil || resp.Value != "synthetic-other" {
		t.Fatalf("get after set and bind = %+v, %v", resp, err)
	}

	if _, err := exchangeOverPipe(t, s, c, request{Op: opDelete, Credential: "crm", Role: "token"}); err != nil {
		t.Fatalf("delete error = %v", err)
	}
	if _, ok := s.secrets["crm"]; ok {
		t.Fatalf("delete left an empty credential behind")
	}
	if _, ok := s.bindings.IDs["crm"]; ok {
		t.Fatalf("delete left the id of a removed credential behind")
	}

	resp, err = exchangeOverPipe(t, s, c, request{Op: opStatus})
	if err != nil || resp.PID != os.Getpid() || resp.LocksAt == nil {
		t.Fatalf("status = %+v, %v, want this pid and a lock time", resp, err)
	}

	if _, err := exchangeOverPipe(t, s, c, request{Op: opGet}); err == nil {
		t.Fatalf("get without a credential succeeded")
	}
	if _, err := exchangeOverPipe(t, s, c, request{Op: "dump"}); err == nil {
		t.Fatalf("an unknown operation succeeded")
	}
}

func TestServerRefusesAnUncheckedPeer(t *testing.T) {
	s := testServer()
	s.Verify = refuse
	c := testClient()

	for _, req := range []request{
		{Op: opGet, Credential: "wiki-reader", Role: "token", Scope: testScope()},
		{Op: opSet, Credential: "wiki-reader", Role: "token", Value: "planted"},
		{Op: opLock},
	} {
		resp, err := exchangeOverPipe(t, s, c, req)
		if !errors.Is(err, ErrRefused) || resp.Value != "" {
			t.Fatalf("%s from a refused peer = %+v, %v, want ErrRefused", req.Op, resp, err)
		}
	}
	if got := string(s.secrets["wiki-reader"]["token"]); got != "synthetic-token" {
		t.Fatalf("a refused set changed the secret")
	}
	if s.stopping() {
		t.Fatalf("a refused lock stopped the server")
	}
}

func TestClientSendsNothingToAnUncheckedServer(t *testing.T) {
	clientEnd, serverEnd := net.Pipe()
	received := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(serverEnd)
		received <- data
	}()

	c := &Client{Recipient: testRecipient, Verify: refuse}
	ctx := context.Background()
	_, err := c.exchange(ctx, clientEnd, c.deadline(ctx), request{Op: opGet, Credential: "wiki-reader", Role: "token", Scope: testScope()})
	_ = clientEnd.Close()
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("exchange with an unchecked server error = %v, want ErrRefused", err)
	}
	if data := <-received; len(data) != 0 {
		t.Fatalf("the client sent %q to an unchecked server", data)
	}
}

func TestServerRefusesAnotherVersion(t *testing.T) {
	s := testServer()
	clientEnd, serverEnd := net.Pipe()
	defer clientEnd.Close()
	go s.serveConn(serverEnd)

	go func() {
		_ = writeMessage(clientEnd, request{V: Version + 1, Op: opGet, Credential: "wiki-reader", Role: "token", Scope: testScope()})
	}()
	var resp response
	if err := readMessage(clientEnd, &resp); err != nil {
		t.Fatalf("readMessage() error = %v", err)
	}
	if resp.Error != codeVersion || resp.Value != "" {
		t.Fatalf("answer to another version = %+v, want %q and no value", resp, codeVersion)
	}
	if !errors.Is(answerError(resp.Error), ErrVersion) {
		t.Fatalf("answerError(%q) is not ErrVersion", resp.Error)
	}
}

func TestServerAnswersTooLargeAsBadRequest(t *testing.T) {
	s := testServer()
	clientEnd, serverEnd := net.Pipe()
	defer clientEnd.Close()
	go s.serveConn(serverEnd)

	go func() { _, _ = clientEnd.Write([]byte(strings.Repeat("x", MaxMessage+1))) }()
	var resp response
	if err := readMessage(clientEnd, &resp); err != nil {
		t.Fatalf("readMessage() error = %v", err)
	}
	if resp.Error != codeBadRequest {
		t.Fatalf("answer to a too large request = %+v, want %q", resp, codeBadRequest)
	}
}

func TestServerDropsAHangingPeer(t *testing.T) {
	s := testServer()
	s.RequestTimeout = 50 * time.Millisecond
	clientEnd, serverEnd := net.Pipe()
	defer clientEnd.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.serveConn(serverEnd)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("the server still waits for a peer that sends nothing")
	}
}

func TestClientStopsWaitingAtItsDeadline(t *testing.T) {
	for name, tc := range map[string]struct {
		timeout time.Duration
		ctx     time.Duration
		want    error
	}{
		"own limit":    {timeout: 50 * time.Millisecond, ctx: time.Minute, want: os.ErrDeadlineExceeded},
		"caller limit": {timeout: time.Minute, ctx: 50 * time.Millisecond, want: context.DeadlineExceeded},
	} {
		t.Run(name, func(t *testing.T) {
			clientEnd, serverEnd := net.Pipe()
			defer clientEnd.Close()
			defer serverEnd.Close()
			// The server reads the challenge and never answers.
			go func() {
				var h hello
				_ = readMessage(serverEnd, &h)
			}()

			c := &Client{Recipient: testRecipient, Verify: allow, Timeout: tc.timeout}
			ctx, cancel := context.WithTimeout(context.Background(), tc.ctx)
			defer cancel()
			start := time.Now()
			_, err := c.exchange(ctx, clientEnd, c.deadline(ctx), request{Op: opStatus})
			if !errors.Is(err, tc.want) {
				t.Fatalf("exchange error = %v, want %v", err, tc.want)
			}
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Fatalf("exchange took %s", elapsed)
			}
		})
	}
}

func TestClientStopsWhenTheCallerCancels(t *testing.T) {
	clientEnd, serverEnd := net.Pipe()
	defer clientEnd.Close()
	defer serverEnd.Close()
	go func() {
		var h hello
		_ = readMessage(serverEnd, &h)
	}()

	c := &Client{Recipient: testRecipient, Verify: allow, Timeout: time.Minute}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	_, err := c.exchange(ctx, clientEnd, c.deadline(ctx), request{Op: opStatus})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("exchange error = %v, want context.Canceled", err)
	}
}

func TestClientReportsAMissingProcess(t *testing.T) {
	if !Supported {
		t.Skip("the vault process runs only on Linux, macOS, and Windows; elsewhere nothing dials or places its socket")
	}
	c := NewClient(filepath.Join(shortTempDir(t), "absent.sock"), testRecipient)
	if _, err := c.Status(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Status() without a process error = %v, want ErrNotRunning", err)
	}
	if _, _, err := c.Get(context.Background(), "wiki-reader", "token", *testScope()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Get() without a process error = %v, want ErrNotRunning", err)
	}
}

func TestLockOverwritesTheSecrets(t *testing.T) {
	s := testServer()
	held := s.secrets["wiki-reader"]["token"]
	s.stop()
	if resp := s.answer(request{Op: opGet, Credential: "wiki-reader", Role: "token", Scope: testScope()}); resp.Error != codeLocked {
		t.Fatalf("get while locking = %+v, want %q", resp, codeLocked)
	}
	s.wipe()
	if s.secrets != nil || s.key != nil {
		t.Fatalf("wipe() kept the secrets or the key")
	}
	if resp := s.solve(nil); resp.Error != codeLocked {
		t.Fatalf("a challenge after wipe() = %+v, want %q", resp, codeLocked)
	}
	for _, b := range held {
		if b != 0 {
			t.Fatalf("wipe() left the value in memory: %q", held)
		}
	}
	if !errors.Is(answerError(codeLocked), ErrNotRunning) {
		t.Fatalf("a locked answer is not ErrNotRunning")
	}
}

func TestSocketPath(t *testing.T) {
	if !Supported || runtime.GOOS == "windows" {
		t.Skip("the vault socket is placed on Linux and macOS only; Windows has a pipe of its own (see TestPipePath)")
	}
	home := shortTempDir(t)
	vaultDir := filepath.Join(home, ".qatlas", "cli", "vault")

	root := userRuntimeRoot
	t.Cleanup(func() { userRuntimeRoot = root })
	userRuntimeRoot = filepath.Join(t.TempDir(), "missing")
	t.Setenv("XDG_RUNTIME_DIR", "")
	path, err := SocketPath(vaultDir)
	if err != nil {
		t.Fatalf("SocketPath() error = %v", err)
	}
	if dir := filepath.Dir(path); dir != filepath.Join(home, ".qatlas", "cli", "run") {
		t.Fatalf("SocketPath() without a runtime directory = %s, want it in run beside the vault", path)
	}
	name := filepath.Base(path)
	if !strings.HasPrefix(name, "vault-") || !strings.HasSuffix(name, ".sock") {
		t.Fatalf("SocketPath() name = %s", name)
	}

	t.Setenv("XDG_RUNTIME_DIR", "relative/run")
	if again, err := SocketPath(vaultDir); err != nil || again != path {
		t.Fatalf("SocketPath() with a relative runtime directory = %s, %v, want it ignored", again, err)
	}

	runtimeDir := shortTempDir(t)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	inRuntime, err := SocketPath(vaultDir)
	if err != nil || inRuntime != filepath.Join(runtimeDir, "qatlas", name) {
		t.Fatalf("SocketPath() with a runtime directory = %s, %v, want %s", inRuntime, err,
			filepath.Join(runtimeDir, "qatlas", name))
	}
	other, err := SocketPath(filepath.Join(shortTempDir(t), "vault"))
	if err != nil || other == inRuntime {
		t.Fatalf("SocketPath() of another vault = %s, %v, want a socket of its own", other, err)
	}

	t.Setenv("XDG_RUNTIME_DIR", "/"+strings.Repeat("r", maxSocketPath))
	// Too long for the per-user temporary directory macOS moves an overlong path to, too.
	t.Setenv("TMPDIR", "/"+strings.Repeat("t", maxSocketPath))
	var tooLong *PathTooLongError
	if _, err := SocketPath(vaultDir); !errors.As(err, &tooLong) {
		t.Fatalf("SocketPath() of an overlong path error = %v, want PathTooLongError", err)
	}
	c := NewClient("/"+strings.Repeat("s", maxSocketPath), testRecipient)
	if _, err := c.Status(context.Background()); !errors.As(err, &tooLong) {
		t.Fatalf("Status() at an overlong path error = %v, want PathTooLongError", err)
	}
}

// fakeServer answers a client's challenge over net.Pipe with whatever answer builds from it, and reports
// everything the client sent after the challenge: nothing, when the answer is not a valid proof.
func fakeServer(t *testing.T, answer func(challenge []byte) response) (clientEnd net.Conn, challenge <-chan []byte, rest <-chan []byte) {
	t.Helper()
	clientEnd, serverEnd := net.Pipe()
	challenges := make(chan []byte, 1)
	after := make(chan []byte, 1)
	go func() {
		defer serverEnd.Close()
		var h hello
		if err := readMessage(serverEnd, &h); err != nil {
			challenges <- nil
			after <- nil
			return
		}
		challenges <- h.Challenge
		_ = writeMessage(serverEnd, answer(h.Challenge))
		_ = serverEnd.SetReadDeadline(time.Now().Add(time.Second))
		data, _ := io.ReadAll(serverEnd)
		after <- data
	}()
	t.Cleanup(func() { _ = clientEnd.Close() })
	return clientEnd, challenges, after
}

func solvedWith(key age.Identity) func([]byte) response {
	return func(challenge []byte) response {
		proof, ok := solveChallenge(key, challenge)
		if !ok {
			return response{V: Version, Error: codeChallenge}
		}
		return response{V: Version, Proof: proof}
	}
}

// A server without the vault's key, with another vault's key, or with an answer that proves nothing is
// told nothing after the challenge: the request, and the credential name in it, is never sent.
func TestClientSendsNothingAfterAFailedChallenge(t *testing.T) {
	for name, answer := range map[string]func([]byte) response{
		"no key":      solvedWith(nil),
		"another key": solvedWith(otherKey),
		"another vault's proof": func(challenge []byte) response {
			// Solving with the other key fails; a server of another vault can at best hash a nonce of its own.
			return response{V: Version, Proof: proofOf(make([]byte, nonceSize))}
		},
		"no proof":         func([]byte) response { return response{V: Version} },
		"echoed challenge": func(c []byte) response { return response{V: Version, Proof: c} },
	} {
		t.Run(name, func(t *testing.T) {
			conn, _, rest := fakeServer(t, answer)
			c := testClient()
			ctx := context.Background()
			_, err := c.exchange(ctx, conn, c.deadline(ctx), request{Op: opGet, Credential: "wiki-reader", Role: "token", Scope: testScope()})
			_ = conn.Close()
			if !errors.Is(err, ErrRefused) {
				t.Fatalf("exchange error = %v, want ErrRefused", err)
			}
			if data := <-rest; len(data) != 0 {
				t.Fatalf("the client sent %q after a failed challenge", data)
			}
		})
	}
}

// Every connection carries a nonce of its own, so a proof recorded on one connection proves nothing on the
// next, even to the very client that saw it.
func TestChallengeIsFreshPerConnection(t *testing.T) {
	c := testClient()
	ctx := context.Background()

	var recorded []byte
	conn, first, _ := fakeServer(t, func(challenge []byte) response {
		resp := solvedWith(testKey)(challenge)
		recorded = resp.Proof
		return resp
	})
	done := make(chan error, 1)
	go func() {
		_, err := c.exchange(ctx, conn, c.deadline(ctx), request{Op: opStatus})
		done <- err
	}()
	firstChallenge := <-first
	// The fake server stops after the proof; the client's request then finds nobody to answer it.
	<-done
	if recorded == nil {
		t.Fatalf("the first connection was not answered with a proof")
	}

	conn, second, rest := fakeServer(t, func([]byte) response { return response{V: Version, Proof: recorded} })
	_, err := c.exchange(ctx, conn, c.deadline(ctx), request{Op: opGet, Credential: "wiki-reader", Role: "token", Scope: testScope()})
	_ = conn.Close()
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("exchange with a replayed proof error = %v, want ErrRefused", err)
	}
	if data := <-rest; len(data) != 0 {
		t.Fatalf("the client sent %q after a replayed proof", data)
	}
	if secondChallenge := <-second; string(secondChallenge) == string(firstChallenge) {
		t.Fatalf("two connections carried the same challenge")
	}
}

// The real server fails the challenge without the vault's key, and does nothing a request asks for.
func TestServerWithoutTheKeyFailsTheChallenge(t *testing.T) {
	for name, key := range map[string]age.Identity{"no key": nil, "another key": otherKey} {
		t.Run(name, func(t *testing.T) {
			s := NewServer(key, testSecrets(), testBindings())
			s.Verify = allow
			c := testClient()
			for _, req := range []request{
				{Op: opGet, Credential: "wiki-reader", Role: "token", Scope: testScope()},
				{Op: opSet, Credential: "wiki-reader", Role: "token", Value: "planted"},
				{Op: opLock},
			} {
				resp, err := exchangeOverPipe(t, s, c, req)
				if !errors.Is(err, ErrRefused) || resp.Value != "" {
					t.Fatalf("%s = %+v, %v, want ErrRefused", req.Op, resp, err)
				}
			}
			if got := string(s.secrets["wiki-reader"]["token"]); got != "synthetic-token" || s.stopping() {
				t.Fatalf("a request after a failed challenge changed the server")
			}
		})
	}
}

// The server only ever answers with a hash of a nonce, never with what it decrypted, and only for a
// message that holds exactly one nonce: it is no way to decrypt anything else encrypted to the vault.
func TestSolveChallengeIsNoDecryptionOracle(t *testing.T) {
	recipient, err := age.ParseX25519Recipient(testRecipient)
	if err != nil {
		t.Fatal(err)
	}
	nonce, challenge, err := newChallenge(recipient)
	if err != nil {
		t.Fatalf("newChallenge() error = %v", err)
	}
	proof, ok := solveChallenge(testKey, challenge)
	if !ok || !checkProof(nonce, proof) || strings.Contains(string(proof), string(nonce)) {
		t.Fatalf("solveChallenge() = %x, %v, want the proof of the nonce and not the nonce", proof, ok)
	}

	for _, plain := range []string{"", "synthetic-secret", strings.Repeat("x", nonceSize+1)} {
		var buf strings.Builder
		w, err := age.Encrypt(&buf, recipient)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, plain)
		_ = w.Close()
		if _, ok := solveChallenge(testKey, []byte(buf.String())); ok {
			t.Fatalf("solveChallenge() answered a message of %d bytes", len(plain))
		}
	}
	if _, ok := solveChallenge(testKey, []byte("not an age message")); ok {
		t.Fatalf("solveChallenge() answered garbage")
	}
}

// A client without the vault's recipient sends nothing and does not even connect.
func TestClientWithoutARecipientSendsNothing(t *testing.T) {
	for _, recipient := range []string{"", "not a recipient"} {
		c := NewClient(filepath.Join(t.TempDir(), "absent.sock"), recipient)
		if _, err := c.Status(context.Background()); err == nil || errors.Is(err, ErrNotRunning) {
			t.Fatalf("Status() with recipient %q error = %v, want a refusal before connecting", recipient, err)
		}
	}
}

// A get is answered only for the connection the bindings approve, exactly as it was approved: any change of
// its scope, another credential than the one it reads, or no connection at all is refused without a value.
// A check answers the same question without one.
func TestServerHandsSecretsOnlyToApprovedConnections(t *testing.T) {
	s := testServer()
	c := testClient()

	changed := map[string]func(*vault.Scope){
		"origin":      func(sc *vault.Scope) { sc.Origin = "http://127.0.0.1:9" },
		"permissions": func(sc *vault.Scope) { sc.Permissions = []string{"read", "delete"} },
		"targets":     func(sc *vault.Scope) { sc.Targets = []string{"other"} },
		"tools":       func(sc *vault.Scope) { sc.Tools = []string{} },
		"provider":    func(sc *vault.Scope) { sc.Provider = "github" },
		"connection":  func(sc *vault.Scope) { sc.Connection = "wiki-copy" },
	}
	for field, change := range changed {
		scope := testScope()
		change(scope)
		resp, err := exchangeOverPipe(t, s, c, request{Op: opGet, Credential: "wiki-reader", Role: "token",
			Scope: scope})
		if !errors.Is(err, vault.ErrApprovalRequired) || resp.Value != "" {
			t.Errorf("get with a changed %s = %+v, %v, want vault.ErrApprovalRequired", field, resp, err)
		}
		if _, err := exchangeOverPipe(t, s, c, request{Op: opCheck, Scope: scope}); !errors.Is(err,
			vault.ErrApprovalRequired) {
			t.Errorf("check with a changed %s = %v, want vault.ErrApprovalRequired", field, err)
		}
	}

	for name, req := range map[string]request{
		"no connection":      {Op: opGet, Credential: "wiki-reader", Role: "token"},
		"another credential": {Op: opGet, Credential: "wiki-reader", Role: "token", Scope: &vault.Scope{Connection: "wiki", Credential: "crm"}},
	} {
		resp, err := exchangeOverPipe(t, s, c, req)
		if !errors.Is(err, vault.ErrApprovalRequired) || resp.Value != "" {
			t.Errorf("get with %s = %+v, %v, want vault.ErrApprovalRequired", name, resp, err)
		}
	}

	// The same scope in another order is the same scope.
	reordered := testScope()
	reordered.Permissions = []string{"create", "read"}
	reordered.Targets = []string{"b", "a"}
	if _, err := exchangeOverPipe(t, s, c, request{Op: opCheck, Scope: reordered}); err != nil {
		t.Errorf("check of the approved scope = %v", err)
	}

	// Bindings that drop the approval take effect at once.
	if _, err := exchangeOverPipe(t, s, c, request{Op: opBind, Bindings: &vault.Bindings{
		IDs: testBindings().IDs}}); err != nil {
		t.Fatalf("bind error = %v", err)
	}
	if _, err := exchangeOverPipe(t, s, c, request{Op: opGet, Credential: "wiki-reader", Role: "token",
		Scope: testScope()}); !errors.Is(err, vault.ErrApprovalRequired) {
		t.Errorf("get after the approval was revoked = %v, want vault.ErrApprovalRequired", err)
	}
}
