package vaultproc

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
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

func testServer() *Server {
	s := NewServer(testKey, testSecrets())
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

	resp, err := exchangeOverPipe(t, s, c, request{Op: opGet, Credential: "wiki-reader", Role: "token"})
	if err != nil || !resp.Found || resp.Value != "synthetic-token" {
		t.Fatalf("get = %+v, %v, want the stored value", resp, err)
	}
	resp, err = exchangeOverPipe(t, s, c, request{Op: opGet, Credential: "wiki-reader", Role: "other"})
	if err != nil || resp.Found || resp.Value != "" {
		t.Fatalf("get of an unknown role = %+v, %v, want not found and no error", resp, err)
	}

	if _, err := exchangeOverPipe(t, s, c, request{Op: opSet, Credential: "crm", Role: "token",
		Value: "synthetic-other"}); err != nil {
		t.Fatalf("set error = %v", err)
	}
	resp, err = exchangeOverPipe(t, s, c, request{Op: opGet, Credential: "crm", Role: "token"})
	if err != nil || resp.Value != "synthetic-other" {
		t.Fatalf("get after set = %+v, %v", resp, err)
	}

	if _, err := exchangeOverPipe(t, s, c, request{Op: opDelete, Credential: "crm", Role: "token"}); err != nil {
		t.Fatalf("delete error = %v", err)
	}
	if _, ok := s.secrets["crm"]; ok {
		t.Fatalf("delete left an empty credential behind")
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
		{Op: opGet, Credential: "wiki-reader", Role: "token"},
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
	_, err := c.exchange(ctx, clientEnd, c.deadline(ctx), request{Op: opGet, Credential: "wiki-reader", Role: "token"})
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
		_ = writeMessage(clientEnd, request{V: Version + 1, Op: opGet, Credential: "wiki-reader", Role: "token"})
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
	c := NewClient(filepath.Join(t.TempDir(), "absent.sock"), testRecipient)
	if _, err := c.Status(context.Background()); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Status() without a process error = %v, want ErrNotRunning", err)
	}
	if _, _, err := c.Get(context.Background(), "wiki-reader", "token"); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Get() without a process error = %v, want ErrNotRunning", err)
	}
}

func TestLockOverwritesTheSecrets(t *testing.T) {
	s := testServer()
	held := s.secrets["wiki-reader"]["token"]
	s.stop()
	if resp := s.answer(request{Op: opGet, Credential: "wiki-reader", Role: "token"}); resp.Error != codeLocked {
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
	home := t.TempDir()
	vaultDir := filepath.Join(home, ".qatlas", "cli", "vault")

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

	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	inRuntime, err := SocketPath(vaultDir)
	if err != nil || inRuntime != filepath.Join(runtimeDir, "qatlas", name) {
		t.Fatalf("SocketPath() with a runtime directory = %s, %v, want %s", inRuntime, err,
			filepath.Join(runtimeDir, "qatlas", name))
	}
	other, err := SocketPath(filepath.Join(t.TempDir(), "vault"))
	if err != nil || other == inRuntime {
		t.Fatalf("SocketPath() of another vault = %s, %v, want a socket of its own", other, err)
	}

	t.Setenv("XDG_RUNTIME_DIR", "/"+strings.Repeat("r", maxSocketPath))
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
			_, err := c.exchange(ctx, conn, c.deadline(ctx), request{Op: opGet, Credential: "wiki-reader", Role: "token"})
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
	_, err := c.exchange(ctx, conn, c.deadline(ctx), request{Op: opGet, Credential: "wiki-reader", Role: "token"})
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
			s := NewServer(key, testSecrets())
			s.Verify = allow
			c := testClient()
			for _, req := range []request{
				{Op: opGet, Credential: "wiki-reader", Role: "token"},
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
