package vaultproc

import (
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"filippo.io/age"
)

// Server holds an unlocked vault's secrets in memory and answers the clients on one listener. The zero
// value is not usable; use NewServer.
type Server struct {
	// IdleTimeout is how long the server holds the secrets without a get before it locks itself. Zero means
	// DefaultIdleTimeout.
	IdleTimeout time.Duration
	// RequestTimeout bounds each connection from accept to answer, so a peer that stops sending or reading
	// cannot hold a handler. Zero means DefaultRequestTimeout.
	RequestTimeout time.Duration
	// Verify checks the peer of every connection before it is answered. Nil means VerifyProgram; a test
	// passes its own.
	Verify Verifier

	mu      sync.Mutex
	key     age.Identity                 // the vault's key, which answers a client's challenge
	secrets map[string]map[string][]byte // credential name, role, value
	locked  bool
	locksAt time.Time
	timer   *time.Timer

	listener net.Listener
	stopOnce sync.Once
	stopped  chan struct{}
	handlers sync.WaitGroup
}

// NewServer returns a server holding key, the vault's own key, and secrets, keyed by credential name and
// then by role, the shape of a decrypted vault document. The key proves to every client that this process
// holds the vault; a server without one, or with another vault's, fails every client's challenge and is
// told nothing. The server keeps copies of the secrets; the caller drops its map once the server holds
// them, since only the copies can be overwritten when the server locks.
func NewServer(key age.Identity, secrets map[string]map[string]string) *Server {
	held := make(map[string]map[string][]byte, len(secrets))
	for credential, roles := range secrets {
		held[credential] = make(map[string][]byte, len(roles))
		for role, value := range roles {
			held[credential][role] = []byte(value)
		}
	}
	return &Server{key: key, secrets: held, stopped: make(chan struct{})}
}

// Serve answers connections on l until the server locks: on a lock request, after IdleTimeout without a
// get, or through Close. It then closes l, waits for the requests in flight, overwrites the secrets, and
// returns nil. It returns an error only when l fails otherwise; the secrets are overwritten then too.
func (s *Server) Serve(l net.Listener) error {
	idle := s.idle()
	s.mu.Lock()
	s.listener = l
	s.locksAt = time.Now().Add(idle)
	s.timer = time.AfterFunc(idle, s.stop)
	s.mu.Unlock()
	if s.stopping() {
		// Close came first; stop found no listener to close yet.
		_ = l.Close()
	}

	defer func() {
		s.stop()
		s.handlers.Wait()
		s.wipe()
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			if s.stopping() {
				return nil
			}
			return err
		}
		s.handlers.Add(1)
		go func() {
			defer s.handlers.Done()
			s.serveConn(conn)
		}()
	}
}

func (s *Server) idle() time.Duration {
	if s.IdleTimeout <= 0 {
		return DefaultIdleTimeout
	}
	return s.IdleTimeout
}

// stopping reports whether the server is locking itself.
func (s *Server) stopping() bool {
	select {
	case <-s.stopped:
		return true
	default:
		return false
	}
}

// Close locks the server as a lock request would, for a process that is asked to end.
func (s *Server) Close() error {
	s.stop()
	return nil
}

// stop ends Serve once: no further connection is accepted, and the idle timer is dropped.
func (s *Server) stop() {
	s.stopOnce.Do(func() {
		close(s.stopped)
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.timer != nil {
			s.timer.Stop()
		}
		if s.listener != nil {
			_ = s.listener.Close()
		}
	})
}

// wipe overwrites every value the server holds and forgets them and the key. The key's own bytes belong to
// the age library and cannot be overwritten from here; they are only dropped. Any request still arriving
// is answered as locked.
func (s *Server) wipe() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.key = nil
	for _, roles := range s.secrets {
		for _, value := range roles {
			clear(value)
		}
	}
	s.secrets = nil
	s.locked = true
}

// serveConn answers the challenge and then the one request of a connection. The peer is checked after its
// first line was read and before anything is done with it, so an unchecked peer neither gets the vault key
// to answer a challenge nor changes the server, and no answer reaches it but the refusal. The deadline
// covers the whole connection.
func (s *Server) serveConn(conn net.Conn) {
	defer conn.Close()
	timeout := s.RequestTimeout
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return
	}

	var h hello
	if err := readMessage(conn, &h); err != nil {
		if errors.Is(err, ErrTooLarge) {
			_ = writeMessage(conn, response{V: Version, Error: codeBadRequest})
		}
		return
	}

	verify := s.Verify
	if verify == nil {
		verify = VerifyProgram
	}
	if verify(conn) != nil {
		_ = writeMessage(conn, response{V: Version, Error: codeRefused})
		return
	}
	// A client of another build sends its own first line; whatever it is, its version tells.
	if h.V != Version {
		_ = writeMessage(conn, response{V: Version, Error: codeVersion})
		return
	}
	if resp := s.solve(h.Challenge); writeMessage(conn, resp) != nil || resp.Error != "" {
		return
	}

	var req request
	if err := readMessage(conn, &req); err != nil {
		if errors.Is(err, ErrTooLarge) {
			_ = writeMessage(conn, response{V: Version, Error: codeBadRequest})
		}
		return
	}
	if req.V != Version {
		_ = writeMessage(conn, response{V: Version, Error: codeVersion})
		return
	}

	resp := s.answer(req)
	_ = writeMessage(conn, resp)
	if req.Op == opLock && resp.Error == "" {
		s.stop()
	}
}

// solve answers a checked client's challenge with the proof that this server holds the vault's key.
func (s *Server) solve(challenge []byte) response {
	s.mu.Lock()
	key := s.key
	locked := s.locked || s.stopping()
	s.mu.Unlock()
	if locked {
		return response{V: Version, Error: codeLocked}
	}
	proof, ok := solveChallenge(key, challenge)
	if !ok {
		return response{V: Version, Error: codeChallenge}
	}
	return response{V: Version, Proof: proof}
}

// answer carries out one checked request.
func (s *Server) answer(req request) response {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A request that was accepted before the server began to lock is not answered from the secrets either.
	if s.locked || s.stopping() {
		return response{V: Version, Error: codeLocked}
	}

	switch req.Op {
	case opStatus:
		locksAt := s.locksAt
		return response{V: Version, PID: os.Getpid(), LocksAt: &locksAt}
	case opLock:
		return response{V: Version}
	}

	if req.Credential == "" || req.Role == "" {
		return response{V: Version, Error: codeBadRequest}
	}
	switch req.Op {
	case opGet:
		s.touch()
		value, found := s.secrets[req.Credential][req.Role]
		if !found {
			return response{V: Version}
		}
		return response{V: Version, Found: true, Value: string(value)}
	case opSet:
		roles := s.secrets[req.Credential]
		if roles == nil {
			roles = map[string][]byte{}
			s.secrets[req.Credential] = roles
		}
		clear(roles[req.Role])
		roles[req.Role] = []byte(req.Value)
		return response{V: Version}
	case opDelete:
		roles := s.secrets[req.Credential]
		clear(roles[req.Role])
		delete(roles, req.Role)
		if len(roles) == 0 {
			delete(s.secrets, req.Credential)
		}
		return response{V: Version}
	}
	return response{V: Version, Error: codeBadRequest}
}

// touch restarts the idle period on a get. The caller holds s.mu.
func (s *Server) touch() {
	idle := s.idle()
	s.locksAt = time.Now().Add(idle)
	if s.timer != nil {
		s.timer.Reset(idle)
	}
}
