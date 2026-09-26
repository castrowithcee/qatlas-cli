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
)

// allow and refuse stand in for VerifyProgram wherever the test is about the protocol rather than about
// the processes at either end.
func allow(net.Conn) error  { return nil }
func refuse(net.Conn) error { return ErrRefused }

func testServer() *Server {
	s := NewServer(map[string]map[string]string{"wiki-reader": {"token": "synthetic-token"}})
	s.Verify = allow
	return s
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
	c := &Client{Verify: allow}

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
	c := &Client{Verify: allow}

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

	c := &Client{Verify: refuse}
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
			// The server reads the request and never answers.
			go func() {
				var req request
				_ = readMessage(serverEnd, &req)
			}()

			c := &Client{Verify: allow, Timeout: tc.timeout}
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
		var req request
		_ = readMessage(serverEnd, &req)
	}()

	c := &Client{Verify: allow, Timeout: time.Minute}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	_, err := c.exchange(ctx, clientEnd, c.deadline(ctx), request{Op: opStatus})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("exchange error = %v, want context.Canceled", err)
	}
}

func TestClientReportsAMissingProcess(t *testing.T) {
	c := NewClient(filepath.Join(t.TempDir(), "absent.sock"))
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
	if s.secrets != nil {
		t.Fatalf("wipe() kept the secrets")
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
	c := NewClient("/" + strings.Repeat("s", maxSocketPath))
	if _, err := c.Status(context.Background()); !errors.As(err, &tooLong) {
		t.Fatalf("Status() at an overlong path error = %v, want PathTooLongError", err)
	}
}
