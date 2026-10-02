package vaultproc

import (
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"filippo.io/age"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
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
	logKey  *invokelog.Key               // derived from key; signs and checks the invocation log
	log     *invokelog.Logger            // the invocation log this server writes; see KeepLog
	secrets map[string]map[string][]byte // credential name, role, value
	// bindings decide which connection a get may hand a secret to; see vault.Bindings.
	bindings vault.Bindings
	locked   bool
	locksAt  time.Time
	timer    *time.Timer
	// vaultDir is the vault directory an approve-token request opens with key; see ApproveWithTokens.
	vaultDir string
	// tokenMu keeps approve-token requests apart, each of which reads and writes the vault.
	tokenMu sync.Mutex

	listener net.Listener
	stopOnce sync.Once
	stopped  chan struct{}
	handlers sync.WaitGroup
}

// NewServer returns a server holding key, the vault's own key, secrets, keyed by credential name and then by
// role, the shape of a decrypted vault document, and bindings, which say which connection may read which
// credential. The key proves to every client that this process holds the vault; a server without one, or
// with another vault's, fails every client's challenge and is told nothing. A get is answered only for a
// connection bindings approve. The server keeps copies of the secrets; the caller drops its map once the
// server holds them, since only the copies can be overwritten when the server locks.
func NewServer(key age.Identity, secrets map[string]map[string]string, bindings vault.Bindings) *Server {
	held := make(map[string]map[string][]byte, len(secrets))
	for credential, roles := range secrets {
		held[credential] = make(map[string][]byte, len(roles))
		for role, value := range roles {
			held[credential][role] = []byte(value)
		}
	}
	s := &Server{key: key, secrets: held, bindings: copyBindings(bindings), stopped: make(chan struct{})}
	if id, ok := key.(*age.X25519Identity); ok {
		// A key the log key cannot be derived from leaves the server without one: it then refuses log and
		// logcheck, and a client falls back to what it would do without a vault process.
		s.logKey, _ = invokelog.DeriveKey(id)
	}
	return s
}

// KeepLog makes the server the writer of the invocation log below vaultDir, the vault directory it holds
// unlocked, which keeps the entries of retentionDays days: every log request is signed with the log key
// and appended under the log's own lock, through the same invokelog.Logger any other writer uses. Both
// values come from the configuration the process was started with, never from a request. A server
// without KeepLog refuses every log request. Call it before Serve.
func (s *Server) KeepLog(vaultDir string, retentionDays int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logKey != nil {
		s.log = invokelog.New(vaultDir, retentionDays).WithKey(s.logKey)
	}
}

// ApproveWithTokens lets the server approve open connection changes with an agent token for the vault in
// vaultDir, the vault directory it holds unlocked, which comes from the configuration the process was
// started with, never from a request. For each such request the server opens the vault with its key as the
// vault is on disk then, so a token created or revoked since counts at once; checks the token, its expiry,
// and what its vorbild connections cover (see vault.Vault.ApproveWithToken); writes the approvals into the
// vault; checks every later get against them; and logs each decision with the token's name, never its
// value. A server without it refuses every approve-token request. Call it before Serve.
func (s *Server) ApproveWithTokens(vaultDir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vaultDir = vaultDir
}

// copyBindings returns bindings with maps of their own, never nil.
func copyBindings(b vault.Bindings) vault.Bindings {
	out := vault.Bindings{IDs: make(map[string]string, len(b.IDs)), Approvals: make(map[string]string, len(b.Approvals))}
	for name, id := range b.IDs {
		out.IDs[name] = id
	}
	for name, fingerprint := range b.Approvals {
		out.Approvals[name] = fingerprint
	}
	return out
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
	s.logKey.Clear()
	s.logKey, s.log = nil, nil
	for _, roles := range s.secrets {
		for _, value := range roles {
			clear(value)
		}
	}
	s.secrets = nil
	s.bindings = vault.Bindings{}
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

	var resp response
	switch req.Op {
	case opLog, opLogCheck:
		resp = s.answerLog(req)
	case opApproveToken:
		resp = s.answerToken(req)
	default:
		resp = s.answer(req)
	}
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
	case opBind:
		if req.Bindings == nil {
			return response{V: Version, Error: codeBadRequest}
		}
		s.bindings = copyBindings(*req.Bindings)
		return response{V: Version}
	case opCheck:
		if req.Scope == nil {
			return response{V: Version, Error: codeBadRequest}
		}
		// A check names the credential it asks about, a forward credential of the scope, or none for the
		// scope's own.
		credential := req.Credential
		if credential == "" {
			credential = req.Scope.Credential
		}
		if _, held := s.secrets[credential]; !held {
			return response{V: Version}
		}
		if !s.bindings.Allows(*req.Scope, credential) {
			return response{V: Version, Error: codeApproval}
		}
		return response{V: Version, Found: true}
	}

	if req.Credential == "" || req.Role == "" {
		return response{V: Version, Error: codeBadRequest}
	}
	switch req.Op {
	case opGet:
		s.touch()
		roles, held := s.secrets[req.Credential]
		if !held {
			return response{V: Version}
		}
		// A credential the process holds is handed out only to a connection the vault approved as it is
		// now; a get that names no connection, or another credential than its connection reads, is refused
		// the same way.
		if req.Scope == nil || !s.bindings.AllowsRole(*req.Scope, req.Credential, req.Role) {
			return response{V: Version, Error: codeApproval}
		}
		value, found := roles[req.Role]
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
			// The vault removed the entry with its last role; one stored again under the same name is a
			// new entry with a new id, which no approval covers until the vault's bindings say so.
			delete(s.secrets, req.Credential)
			delete(s.bindings.IDs, req.Credential)
		}
		return response{V: Version}
	}
	return response{V: Version, Error: codeBadRequest}
}

// answerLog carries out one checked log or logcheck request. It holds s.mu only to see whether the server
// is still unlocked and to take the log and its key, never while it reads or writes a file, so a slow disk
// or a writer holding the log's lock never holds up a get. Neither request restarts the idle period: an
// invoke that reads a secret does that with its get already. A log request's fields are validated before
// anything is written, and a logcheck's lines are only compared, never written; the answer names neither
// the key nor anything the request sent.
func (s *Server) answerLog(req request) response {
	s.mu.Lock()
	locked := s.locked || s.stopping()
	logger, key := s.log, s.logKey
	s.mu.Unlock()
	switch {
	case locked:
		return response{V: Version, Error: codeLocked}
	case key == nil || (req.Op == opLog && logger == nil):
		return response{V: Version, Error: codeNoLog}
	}

	if req.Op == opLogCheck {
		if len(req.Lines) == 0 {
			return response{V: Version, Error: codeBadRequest}
		}
		valid, err := key.Check(req.Lines)
		if err != nil {
			return response{V: Version, Error: codeNoLog}
		}
		return response{V: Version, Valid: valid}
	}

	// A duration beyond what Validate accepts is refused before it is converted, where it could overflow.
	if req.Entry == nil || req.Entry.DurationMS < 0 || req.Entry.DurationMS > invokelog.MaxDuration.Milliseconds() {
		return response{V: Version, Error: codeBadRequest}
	}
	fields := req.Entry.fields()
	if fields.Validate() != nil {
		return response{V: Version, Error: codeBadRequest}
	}
	if logger.Append(fields) != nil {
		return response{V: Version, Error: codeLogFailed}
	}
	return response{V: Version}
}

// approveOperation is the operation a token approval is logged under.
const approveOperation = "vault.approve"

// answerToken carries out one checked approve-token request. Like answerLog it holds s.mu only to take what
// it needs and, at the end, to adopt the vault's new bindings, never while it reads or writes a file; tokenMu
// keeps two such requests from writing the vault over each other. The answer names the token and the
// connections it decided on, never a value.
func (s *Server) answerToken(req request) response {
	s.mu.Lock()
	locked := s.locked || s.stopping()
	key, dir, logger := s.key, s.vaultDir, s.log
	s.mu.Unlock()
	if locked {
		return response{V: Version, Error: codeLocked}
	}
	id, ok := key.(*age.X25519Identity)
	if dir == "" || !ok {
		return response{V: Version, Error: codeNoTokens}
	}

	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	v, err := vault.OpenWithKey(dir, id)
	if err != nil {
		return response{V: Version, Error: codeTokenFailed}
	}
	result, err := v.ApproveWithToken(req.Token, req.Scopes, req.Names, time.Now())
	refused := invokelog.Fields{Path: "cli", Operation: approveOperation, Result: resultAdminRequired,
		Token: result.Token}
	switch {
	case errors.Is(err, vault.ErrTokenUnknown):
		logDecision(logger, refused)
		return response{V: Version, Error: codeTokenUnknown}
	case errors.Is(err, vault.ErrTokenExpired):
		logDecision(logger, refused)
		return response{V: Version, Error: codeTokenExpired}
	case errors.Is(err, vault.ErrTokensTampered):
		return response{V: Version, Error: codeTokensTampered}
	case err != nil:
		return response{V: Version, Error: codeTokenFailed}
	}

	if bindings, err := v.Bindings(); err == nil {
		s.mu.Lock()
		if !s.locked && !s.stopping() {
			s.bindings = copyBindings(bindings)
		}
		s.mu.Unlock()
	}
	logged := true
	for _, change := range result.Changes {
		fields := invokelog.Fields{Path: "cli", Operation: approveOperation, Connection: change.Connection,
			Effect: change.Kind, Result: resultAdminRequired, Token: result.Token}
		if change.Approved {
			fields.Result = "success"
		}
		if !logDecision(logger, fields) {
			logged = false
		}
	}
	return response{V: Version, Approval: &result, Unlogged: !logged}
}

// resultAdminRequired is the result a refused token approval is logged with, the code the command fails with.
const resultAdminRequired = "admin-required"

// logDecision appends one token decision to the invocation log and reports whether it was written.
func logDecision(logger *invokelog.Logger, fields invokelog.Fields) bool {
	return logger != nil && fields.Validate() == nil && logger.Append(fields) == nil
}

// touch restarts the idle period on a get. The caller holds s.mu.
func (s *Server) touch() {
	idle := s.idle()
	s.locksAt = time.Now().Add(idle)
	if s.timer != nil {
		s.timer.Reset(idle)
	}
}
