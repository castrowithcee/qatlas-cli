package vaultproc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"filippo.io/age"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// Client reaches the vault process at one socket. Every call opens a connection of its own, checks the
// process that listens, and only then sends its request.
//
// The check has two steps. The process that listens must run as this user, which the kernel reports
// through SO_PEERCRED on Linux and LOCAL_PEERCRED on macOS, and Windows through the token of the process
// that created the pipe instance. On Linux its program cannot be checked the way the server checks its
// clients, since the vault process is hardened and its /proc entries are closed to this user too; on macOS
// and Windows it is checked like that, see VerifyProgram. It must then prove that it
// holds the vault's key: the client sends a fresh random nonce encrypted to the vault's public recipient,
// and the process answers with a hash of what it decrypted. Nothing else is sent before both steps
// passed, not even the credential name a get asks for.
type Client struct {
	// Path is the socket, usually from SocketPath.
	Path string
	// Recipient is the vault's public recipient as age text, the content of its recipient file. The
	// challenge is encrypted to it; a client without one sends nothing at all.
	Recipient string
	// Timeout bounds one call; the context a call gets bounds it too, and the earlier limit wins. Zero
	// means DefaultRequestTimeout.
	Timeout time.Duration
	// Verify checks the process that listens before the challenge is sent. Nil means VerifyUser on Linux
	// and VerifyProgram on macOS and Windows; a test passes its own. The challenge is never skipped.
	Verify Verifier
}

// NewClient returns a client for the socket at path, checking the process there against the vault
// recipient.
func NewClient(path, recipient string) *Client { return &Client{Path: path, Recipient: recipient} }

// ErrNoRecipient reports a client that has no vault recipient to challenge the process with, which is a
// vault that is not encrypted: such a vault has no key, and therefore no vault process.
var ErrNoRecipient = errors.New("the vault has no recipient to check the vault process with; it is not encrypted")

// PeerError is a failure at the vault socket where a process did listen, together with the id of that
// process. The id is no secret, and it is what a person needs to find and end a process that cannot be
// used: one that holds another key, left from before the vault was encrypted anew, or one whose program an
// update replaced, which refuses every client from then on.
type PeerError struct {
	PID int
	Err error
}

func (e *PeerError) Error() string { return fmt.Sprintf("%v (process %d)", e.Err, e.PID) }

func (e *PeerError) Unwrap() error { return e.Err }

// Status is what a running vault process reports about itself. It holds no secret and no credential name.
type Status struct {
	// PID is the process id of the vault process.
	PID int
	// LocksAt is when the process locks itself unless a get comes first.
	LocksAt time.Time
	// Update is the vault's update setting as the process reads it from settings.age with its own key:
	// whether an update hands the vault over to the new program or locks it. SettingsUntrusted reports a
	// settings.age that could not be trusted, which reads as lock. Update is empty for a process that
	// does not say.
	Update            vault.UpdateBehaviour
	SettingsUntrusted bool
}

// Status reports on the running vault process. ErrNotRunning means there is none.
func (c *Client) Status(ctx context.Context) (Status, error) {
	resp, err := c.call(ctx, request{Op: opStatus})
	if err != nil {
		return Status{}, err
	}
	status := Status{PID: resp.PID, Update: vault.UpdateBehaviour(resp.Handover),
		SettingsUntrusted: resp.SettingsUntrusted}
	if resp.LocksAt != nil {
		status.LocksAt = *resp.LocksAt
	}
	return status, nil
}

// Get returns the secret of one credential role for the connection scope describes. found is false when the
// process holds nothing for the pair, which is not an error. vault.ErrApprovalRequired means the vault has not
// approved the connection as scope describes it; ErrNotRunning means the vault is not unlocked in a process.
func (c *Client) Get(ctx context.Context, credential, role string, scope vault.Scope) (value string, found bool,
	err error) {
	resp, err := c.call(ctx, request{Op: opGet, Credential: credential, Role: role, Scope: &scope})
	if err != nil {
		return "", false, err
	}
	return resp.Value, resp.Found, nil
}

// Check reports whether the connection scope describes may read its credential, without reading a secret:
// nil when it may or when the process holds nothing for the credential, vault.ErrApprovalRequired when the
// vault has not approved it as it is now.
func (c *Client) Check(ctx context.Context, scope vault.Scope) error {
	_, err := c.call(ctx, request{Op: opCheck, Scope: &scope})
	return err
}

// CheckCredential is Check for one forward credential of the scope: nil when the connection may read it or the
// process holds nothing for it, vault.ErrApprovalRequired when the vault has not approved the connection, with
// that credential released, as it is now.
func (c *Client) CheckCredential(ctx context.Context, scope vault.Scope, credential string) error {
	_, err := c.call(ctx, request{Op: opCheck, Credential: credential, Scope: &scope})
	return err
}

// Bind replaces the bindings the running process checks every get with, after the vault's approvals
// changed. It does not write the vault.
func (c *Client) Bind(ctx context.Context, bindings vault.Bindings) error {
	_, err := c.call(ctx, request{Op: opBind, Bindings: &bindings})
	return err
}

// Set stores or replaces the secret of one credential role in the running process, so it keeps answering
// with what the vault on disk now holds. It does not write the vault.
func (c *Client) Set(ctx context.Context, credential, role, value string) error {
	_, err := c.call(ctx, request{Op: opSet, Credential: credential, Role: role, Value: value})
	return err
}

// Delete removes the secret of one credential role from the running process. Removing a pair it does not
// hold is not an error.
func (c *Client) Delete(ctx context.Context, credential, role string) error {
	_, err := c.call(ctx, request{Op: opDelete, Credential: credential, Role: role})
	return err
}

// Lock asks the vault process to overwrite its secrets and end. ErrNotRunning means there was none. A lock
// that reaches a process in the middle of a handover is answered replaced and asked again, which locks the
// successor; a lock is never lost to a handover.
//
// A vault process of another protocol version, one left from before an update, cannot be asked; it is ended
// with SIGTERM instead, on which it closes and overwrites its secrets just as on a lock, and on Windows,
// which has no such signal, with TerminateProcess, which takes its memory with it. That process was checked
// before its version was read, as this user's own (Linux) or this program (macOS, Windows), and its id comes
// from the kernel's credentials of the socket, or the pipe's record of the process that serves it.
func (c *Client) Lock(ctx context.Context) error {
	_, err := c.call(ctx, request{Op: opLock})
	var peer *PeerError
	if errors.Is(err, ErrVersion) && errors.As(err, &peer) && terminate(peer.PID) == nil {
		return nil
	}
	return err
}

// Log hands the fields of one completed invoke to the vault process, which checks them, signs them with
// the log key, and appends them to the invocation log it was started for, as the one writer that signs.
// ErrNotRunning means there is no vault process; ErrVersion one of another build, which cannot sign; and
// ErrNoLog one that keeps no log. In each case, and on any other error, nothing was written as far as
// the client can tell, and the caller writes the entry itself.
func (c *Client) Log(ctx context.Context, f invokelog.Fields) error {
	_, err := c.call(ctx, request{Op: opLog, Entry: logEntryOf(f)})
	return err
}

// ApproveWithToken asks the vault process to approve every open connection change the agent token covers,
// or only the changes of the connections names lists. scopes is the scope of every connection that reads a
// vault credential, as configured now. The process checks the token and decides, writes the approvals into
// the vault, and logs each decision; unlogged is true when it could not log one. vault.ErrTokenUnknown and
// vault.ErrTokenExpired refuse the token; ErrNotRunning means no vault process holds the vault unlocked.
func (c *Client) ApproveWithToken(ctx context.Context, token string, scopes []vault.Scope, names []string) (
	approval vault.TokenApproval, unlogged bool, err error) {
	resp, err := c.call(ctx, request{Op: opApproveToken, Token: token, Scopes: scopes, Names: names})
	if err != nil {
		return vault.TokenApproval{}, false, err
	}
	if resp.Approval != nil {
		approval = *resp.Approval
	}
	return approval, resp.Unlogged, nil
}

// maxLogCheckBatch bounds the stored lines one logcheck carries, before their base64 encoding, so a request
// stays well within MaxMessage.
const maxLogCheckBatch = MaxMessage / 2

// CheckLog asks the vault process whether the check value of each of lines, stored invocation log lines,
// matches, and returns one answer per line in the same order. The process only compares; it never answers
// with the log key. Lines are sent in as many requests as MaxMessage calls for. A single line too long to
// send at all is not one the log ever wrote, and is answered as not matching without asking.
func (c *Client) CheckLog(ctx context.Context, lines [][]byte) ([]bool, error) {
	valid := make([]bool, len(lines))
	var batch [][]byte
	var indexes []int
	size := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		resp, err := c.call(ctx, request{Op: opLogCheck, Lines: batch})
		if err != nil {
			return err
		}
		if len(resp.Valid) != len(batch) {
			return errors.New("the vault process answered a log check with the wrong number of results")
		}
		for i, ok := range resp.Valid {
			valid[indexes[i]] = ok
		}
		batch, indexes, size = nil, nil, 0
		return nil
	}
	for i, line := range lines {
		if len(line) > maxLogCheckBatch {
			continue
		}
		if size+len(line) > maxLogCheckBatch {
			if err := flush(); err != nil {
				return nil, err
			}
		}
		batch, indexes, size = append(batch, line), append(indexes, i), size+len(line)
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return valid, nil
}

// LogChecker returns an invokelog.Checker that checks through CheckLog, bounded by ctx.
func (c *Client) LogChecker(ctx context.Context) invokelog.Checker {
	return logChecker{ctx: ctx, client: c}
}

type logChecker struct {
	ctx    context.Context
	client *Client
}

func (l logChecker) Check(lines [][]byte) ([]bool, error) { return l.client.CheckLog(l.ctx, lines) }

// Handover is a handover of the vault process to a successor, prepared on one connection, which its commit
// uses as well; see Client.PrepareHandover.
type Handover struct {
	client *Client
	conn   net.Conn
	pid    int
}

// PrepareHandover asks the vault process to hand the unlocked vault over to a successor started from the
// program an update is about to install, which files describes: the release's checksums.txt, its signature,
// and the archive downloaded, each by its absolute path. The process reads and verifies them itself, and
// reads itself whether the vault hands over on an update or locks.
//
// It answers vault.UpdateHandover with the Handover to commit once the program is replaced, on this same
// connection, or vault.UpdateLock once it locked itself as the vault asks. An ErrHandover means it could not
// verify the release and locked itself; ErrNotRunning that there was nothing to hand over. The caller closes
// a Handover it does not commit, which the process takes as the end of the handover: it locks itself then.
func (c *Client) PrepareHandover(ctx context.Context, files ReleaseFiles) (vault.UpdateBehaviour, *Handover, error) {
	deadline := c.deadlineWithin(ctx, HandoverTimeout)
	conn, pid, err := c.dial(ctx, deadline)
	if err != nil {
		return "", nil, err
	}
	resp, err := c.exchange(ctx, conn, deadline, request{Op: opHandover, Phase: phasePrepare, Release: &files})
	if err != nil {
		_ = conn.Close()
		return "", nil, c.named(ctx, pid, err)
	}
	switch vault.UpdateBehaviour(resp.Handover) {
	case vault.UpdateHandover:
		return vault.UpdateHandover, &Handover{client: c, conn: conn, pid: pid}, nil
	case vault.UpdateLock:
		_ = conn.Close()
		return vault.UpdateLock, nil, nil
	}
	_ = conn.Close()
	return "", nil, c.named(ctx, pid, errors.New("the vault process answered the handover with something unknown"))
}

// Commit tells the vault process that the program was replaced, and waits until it handed the vault over: it
// checks that the program at its own path is now the one the release holds, starts the successor from it,
// hands it the vault, and hands it the socket. It returns the successor's process id. Any error other than
// one of ctx means the process locked itself instead. The connection is closed either way.
func (h *Handover) Commit(ctx context.Context) (int, error) {
	defer h.conn.Close()
	deadline := h.client.deadlineWithin(ctx, HandoverTimeout)
	if err := h.conn.SetDeadline(deadline); err != nil {
		return 0, err
	}
	stop := context.AfterFunc(ctx, func() { _ = h.conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	if err := writeMessage(h.conn, request{V: Version, Op: opHandover, Phase: phaseCommit}); err != nil {
		return 0, h.client.named(ctx, h.pid, h.client.failed(ctx, err))
	}
	resp, err := h.client.answer(ctx, h.conn)
	if err != nil {
		return 0, h.client.named(ctx, h.pid, err)
	}
	return resp.PID, nil
}

// Close ends a handover that is not committed; the vault process locks itself then.
func (h *Handover) Close() error { return h.conn.Close() }

// The pauses and the overall limit within which a call asks again when a vault process answers replaced: a
// handover moves the socket to the successor within moments of the commit.
const (
	replacedPause = 50 * time.Millisecond
	replacedLimit = 2 * time.Second
)

// call connects, checks the server, and exchanges one request for its answer. A process that answers
// replaced is asked again, on a new connection, for up to replacedLimit: while a vault process hands the
// vault over, the next connection reaches its successor. Every other answer, a refusal included, is final.
// A process whose own program was replaced and that has no successor still answers replaced once the
// limit is reached.
func (c *Client) call(ctx context.Context, req request) (response, error) {
	end := time.Now().Add(replacedLimit)
	for {
		resp, err := c.callOnce(ctx, req)
		if !errors.Is(err, ErrReplaced) || !time.Now().Add(replacedPause).Before(end) {
			return resp, err
		}
		select {
		case <-ctx.Done():
			return resp, err
		case <-time.After(replacedPause):
		}
	}
}

// callOnce is one attempt of call.
func (c *Client) callOnce(ctx context.Context, req request) (response, error) {
	deadline := c.deadline(ctx)
	conn, pid, err := c.dial(ctx, deadline)
	if err != nil {
		return response{}, err
	}
	defer conn.Close()
	resp, err := c.exchange(ctx, conn, deadline, req)
	return resp, c.named(ctx, pid, err)
}

// dial connects to the socket. The peer's process id is taken from what the kernel said right after
// connecting: macOS no longer tells who was at the other end once a server that refused this client has
// closed the connection.
func (c *Client) dial(ctx context.Context, deadline time.Time) (net.Conn, int, error) {
	if err := checkPathLength(c.Path); err != nil {
		return nil, 0, err
	}
	if _, err := c.recipient(); err != nil {
		return nil, 0, err
	}
	conn, err := dialSocket(ctx, c.Path, deadline)
	if err != nil {
		if notRunning(err) {
			return nil, 0, ErrNotRunning
		}
		return nil, 0, c.failed(ctx, err)
	}
	return conn, peerPID(conn), nil
}

// named adds the process id of the peer to an error it caused, as a PeerError.
func (c *Client) named(ctx context.Context, pid int, err error) error {
	if err != nil && !errors.Is(err, ErrNotRunning) && ctx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) {
		if pid > 0 {
			err = &PeerError{PID: pid, Err: err}
		}
	}
	return err
}

// deadline is the earlier of the client's own limit and the end of ctx.
func (c *Client) deadline(ctx context.Context) time.Time {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}
	return c.deadlineWithin(ctx, timeout)
}

// deadlineWithin is the earlier of timeout from now and the end of ctx.
func (c *Client) deadlineWithin(ctx context.Context, timeout time.Duration) time.Time {
	deadline := time.Now().Add(timeout)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	return deadline
}

// recipient parses the vault recipient the challenge is encrypted to.
func (c *Client) recipient() (age.Recipient, error) {
	text := strings.TrimSpace(c.Recipient)
	if text == "" {
		return nil, ErrNoRecipient
	}
	recipient, err := age.ParseX25519Recipient(text)
	if err != nil {
		return nil, errors.New("the vault recipient cannot be read; the vault process is not asked")
	}
	return recipient, nil
}

// exchange runs one request on an open connection. The server is checked, its user and then its key,
// before the request is written, so an unchecked server learns nothing, not even which credential was
// asked for. A context that ends early ends the exchange at once rather than at the deadline.
func (c *Client) exchange(ctx context.Context, conn net.Conn, deadline time.Time, req request) (response, error) {
	if err := conn.SetDeadline(deadline); err != nil {
		return response{}, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	verify := c.Verify
	if verify == nil {
		verify = verifyServer
	}
	if err := verify(conn); err != nil {
		return response{}, err
	}
	if err := c.challenge(ctx, conn); err != nil {
		return response{}, err
	}

	req.V = Version
	if err := writeMessage(conn, req); err != nil {
		return response{}, c.failed(ctx, err)
	}
	return c.answer(ctx, conn)
}

// challenge asks the server to prove that it holds the vault's key, with a nonce of its own for this one
// connection, so no earlier answer can be replayed.
func (c *Client) challenge(ctx context.Context, conn net.Conn) error {
	recipient, err := c.recipient()
	if err != nil {
		return err
	}
	nonce, challenge, err := newChallenge(recipient)
	if err != nil {
		return err
	}
	defer clear(nonce)
	if err := writeMessage(conn, hello{V: Version, Challenge: challenge}); err != nil {
		return c.failed(ctx, err)
	}
	resp, err := c.answer(ctx, conn)
	switch {
	case err == nil && checkProof(nonce, resp.Proof):
		return nil
	case err == nil, errors.Is(err, errUnproven):
		return errUnproven
	default:
		return err
	}
}

// answer reads one answer and turns its error code into the client's error.
func (c *Client) answer(ctx context.Context, conn net.Conn) (response, error) {
	var resp response
	if err := readMessage(conn, &resp); err != nil {
		return response{}, c.failed(ctx, err)
	}
	if resp.V != Version {
		return response{}, ErrVersion
	}
	if resp.Error != "" {
		return response{}, answerError(resp.Error)
	}
	return resp, nil
}

// failed says why an exchange broke off. A context that ended wins, so a caller's overall limit is
// reported as that limit; a connection closed without an answer means the process is locking itself.
func (c *Client) failed(ctx context.Context, err error) error {
	callerEnded := false
	if end, ok := ctx.Deadline(); ok && !time.Now().Before(end) {
		// The connection's deadline can pass a moment before ctx reports that it ended.
		callerEnded = true
	}
	switch {
	case ctx.Err() != nil:
		return ctx.Err()
	case errors.Is(err, os.ErrDeadlineExceeded) && callerEnded:
		return context.DeadlineExceeded
	case errors.Is(err, os.ErrDeadlineExceeded):
		return fmt.Errorf("the vault process did not answer in time: %w", err)
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, syscall.EPIPE),
		errors.Is(err, syscall.ECONNRESET):
		return ErrNotRunning
	case errors.Is(err, ErrTooLarge):
		return err
	}
	return fmt.Errorf("cannot talk to the vault process: %w", err)
}

// notRunning reports a dial error that means nobody serves the socket: it does not exist, or it is left
// over from a process that ended and nobody listens on it any more. Something at the path that is not a
// socket at all means the same; Linux reports it as a refused connection, macOS as ENOTSOCK. On Windows a
// pipe nobody serves does not exist at all, and a path that names no pipe is reported as not existing.
func notRunning(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ENOTSOCK)
}
