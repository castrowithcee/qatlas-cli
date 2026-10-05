package vaultproc

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"filippo.io/age"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// The operations a request names.
const (
	opStatus = "status"
	opGet    = "get"
	opSet    = "set"
	opDelete = "delete"
	opLock   = "lock"
	opCheck  = "check"
	opBind   = "bind"
	// opLog appends one invocation log entry, signed by the process; opLogCheck checks the check values of
	// stored log lines. Neither ever answers with the log key or the vault's key.
	opLog      = "log"
	opLogCheck = "logcheck"
	// opApproveToken approves the open connection changes an agent token covers. The process checks the
	// token against the tokens the vault holds, writes the approvals into the vault itself, and logs each
	// decision; it never answers with a token's value.
	opApproveToken = "approve-token"
	// opHandover hands the unlocked vault over to a successor, in two phases on one connection: prepare,
	// which verifies a release, and commit, once the program was replaced; see Client.PrepareHandover.
	opHandover = "handover"
)

// The phases of a handover request.
const (
	phasePrepare = "prepare"
	phaseCommit  = "commit"
)

// The codes an answer's error carries. They are fixed words, never text built from a request, so an error
// can neither carry a secret nor echo what a peer sent.
const (
	codeRefused    = "refused"
	codeBadRequest = "bad-request"
	codeVersion    = "version"
	codeLocked     = "locked"
	codeChallenge  = "challenge"
	codeApproval   = "approval-required"
	codeNoLog      = "no-log"
	codeLogFailed  = "log-failed"
	// The codes of an approve-token request.
	codeNoTokens       = "no-tokens"
	codeTokenUnknown   = "token-unknown"
	codeTokenExpired   = "token-expired"
	codeTokensTampered = "tokens-tampered"
	codeTokenFailed    = "token-failed"
	// codeReplaced answers a request the process no longer serves because it was replaced: it hands, or
	// handed, the vault over to a successor, or an update replaced its own program. A client asks again.
	codeReplaced = "replaced"
	// The codes of a handover that did not happen. The process locked itself with each of them.
	codeReleaseUnverified = "release-unverified"
	codeProgramUnchanged  = "program-unchanged"
	codeProgramMismatch   = "program-mismatch"
	codeHandoverFailed    = "handover-failed"
)

// nonceSize is the number of random bytes a challenge holds.
const nonceSize = 32

// proofLabel separates the proof from any other use of the nonce's hash.
const proofLabel = "qatlas vault process proof v2\x00"

// hello is the first line a client sends on every connection: a fresh random nonce, encrypted to the
// vault's public recipient. Only a process holding the vault's key can decrypt it, which is how a client
// tells the vault process apart from anything else listening at the socket.
type hello struct {
	V         int    `json:"v"`
	Challenge []byte `json:"challenge,omitempty"`
}

// request is one line a client sends. Scope is the connection a get or a check is made for; a get without
// one is refused. Bindings replace the server's own on a bind. Entry is what a log appends, and Lines are
// the stored log lines a logcheck checks. Token, Scopes, and Names belong to approve-token: the agent token
// presented, the scope of every connection reading a vault credential as configured now, and the
// connections to limit the approval to, none for all. Phase and Release belong to handover.
type request struct {
	V          int             `json:"v"`
	Op         string          `json:"op"`
	Credential string          `json:"credential,omitempty"`
	Role       string          `json:"role,omitempty"`
	Value      string          `json:"value,omitempty"`
	Scope      *vault.Scope    `json:"scope,omitempty"`
	Bindings   *vault.Bindings `json:"bindings,omitempty"`
	Entry      *logEntry       `json:"entry,omitempty"`
	Lines      [][]byte        `json:"lines,omitempty"`
	Token      string          `json:"token,omitempty"`
	Scopes     []vault.Scope   `json:"scopes,omitempty"`
	Names      []string        `json:"names,omitempty"`
	Phase      string          `json:"phase,omitempty"`
	Release    *ReleaseFiles   `json:"release,omitempty"`
}

// ReleaseFiles names the files of a downloaded release that a handover is prepared with, each by its absolute
// path, and the name of the archive as checksums.txt lists it. The vault process reads and verifies them
// itself; nothing in them is taken on trust from the client.
type ReleaseFiles struct {
	// Checksums is checksums.txt, Signature checksums.txt.sig, and Archive the archive of this platform.
	Checksums string `json:"checksums"`
	Signature string `json:"signature"`
	Archive   string `json:"archive"`
	// ArchiveName is the archive's file name in the release, which checksums.txt lists it by.
	ArchiveName string `json:"archive_name"`
}

// logEntry is what a client sends for one invoke to log: invokelog.Fields, without anything the server
// computes itself, which is the time, the sequence number, the previous entry's hash, and the check value.
type logEntry struct {
	Path       string                `json:"path"`
	Client     *invokelog.ClientInfo `json:"client,omitempty"`
	Operation  string                `json:"operation,omitempty"`
	Version    int                   `json:"version,omitempty"`
	Connection string                `json:"connection,omitempty"`
	Effect     string                `json:"effect,omitempty"`
	Result     string                `json:"result"`
	DurationMS int64                 `json:"duration_ms,omitempty"`
}

func (e logEntry) fields() invokelog.Fields {
	return invokelog.Fields{
		Path: e.Path, Client: e.Client, Operation: e.Operation, Version: e.Version, Connection: e.Connection,
		Effect: e.Effect, Result: e.Result, Duration: time.Duration(e.DurationMS) * time.Millisecond,
	}
}

func logEntryOf(f invokelog.Fields) *logEntry {
	return &logEntry{
		Path: f.Path, Client: f.Client, Operation: f.Operation, Version: f.Version, Connection: f.Connection,
		Effect: f.Effect, Result: f.Result, DurationMS: f.Duration.Milliseconds(),
	}
}

// response is a line a server answers with. Error is empty on success; Proof answers the hello, Found and
// Value belong to get, PID and LocksAt to status, Valid, one per line asked, to logcheck, Approval and
// Unlogged to approve-token, Handover to the prepare of a handover, and PID to its commit as well. A status
// carries Handover too, the vault's update setting as the process reads it, with SettingsUntrusted where
// settings.age could not be trusted and reads as lock.
type response struct {
	V        int                  `json:"v"`
	Error    string               `json:"error,omitempty"`
	Proof    []byte               `json:"proof,omitempty"`
	Found    bool                 `json:"found,omitempty"`
	Value    string               `json:"value,omitempty"`
	PID      int                  `json:"pid,omitempty"`
	LocksAt  *time.Time           `json:"locks_at,omitempty"`
	Valid    []bool               `json:"valid,omitempty"`
	Approval *vault.TokenApproval `json:"approval,omitempty"`
	Unlogged bool                 `json:"unlogged,omitempty"`
	Handover string               `json:"handover,omitempty"`
	// SettingsUntrusted belongs to status.
	SettingsUntrusted bool `json:"settings_untrusted,omitempty"`
}

// writeMessage sends v as one line of JSON. A message above MaxMessage is not sent at all. The encoded
// bytes are overwritten once written, since they may hold a secret.
func writeMessage(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	defer clear(data)
	if len(data)+1 > MaxMessage {
		return ErrTooLarge
	}
	line := append(data, '\n')
	defer clear(line)
	_, err = w.Write(line)
	return err
}

// readMessage reads one line of JSON into v. It never reads more than MaxMessage bytes, and a line that
// does not end within them is refused as too large instead of being buffered further. The bytes read are
// overwritten once decoded.
func readMessage(r io.Reader, v any) error {
	reader := bufio.NewReaderSize(io.LimitReader(r, MaxMessage+1), 4096)
	line, err := reader.ReadBytes('\n')
	defer clear(line)
	switch {
	case len(line) > MaxMessage:
		return ErrTooLarge
	case errors.Is(err, io.EOF):
		if len(line) == 0 {
			return io.EOF
		}
		return io.ErrUnexpectedEOF
	case err != nil:
		return err
	}
	if err := json.Unmarshal(line, v); err != nil {
		// The decoder's message may quote the input, which can be a secret; it is not passed on.
		return errors.New("a vault process message is not valid JSON")
	}
	return nil
}

// errUnproven reports a server that did not prove that it holds the vault's key.
var errUnproven = fmt.Errorf("%w: it cannot prove that it holds this vault's key", ErrRefused)

// ErrProgramRefused reports that the vault process refused this program, as it does one that was
// removed or replaced since it started. It wraps ErrRefused.
var ErrProgramRefused = fmt.Errorf("%w: the vault process refused this program", ErrRefused)

// ErrReplaced reports a vault process that no longer answers because it was replaced: it is handing the
// vault over, or handed it over, to a successor, which a new connection reaches in a moment, or an update
// replaced its own program. It wraps ErrProgramRefused, which a process whose program was replaced answered
// before.
var ErrReplaced = fmt.Errorf("%w; it was replaced by a newer one", ErrProgramRefused)

// ErrHandover reports a handover that did not happen. The vault process locked itself instead; the error
// wraps the reason, which never carries a secret or a path.
var ErrHandover = errors.New("the vault process did not hand the vault over and locked itself")

// answerError turns a response's error code into the error the client returns.
func answerError(code string) error {
	switch code {
	case codeRefused:
		return ErrProgramRefused
	case codeVersion:
		return ErrVersion
	case codeLocked:
		return ErrNotRunning
	case codeChallenge:
		return errUnproven
	case codeApproval:
		return vault.ErrApprovalRequired
	case codeNoLog:
		return ErrNoLog
	case codeLogFailed:
		return errors.New("the vault process could not write the invocation log")
	case codeNoTokens:
		return ErrNoTokens
	case codeTokenUnknown:
		return vault.ErrTokenUnknown
	case codeTokenExpired:
		return vault.ErrTokenExpired
	case codeTokensTampered:
		return vault.ErrTokensTampered
	case codeTokenFailed:
		return errors.New("the vault process could not approve with the agent token")
	case codeReplaced:
		return ErrReplaced
	case codeReleaseUnverified:
		return fmt.Errorf("%w: it could not verify the release", ErrHandover)
	case codeProgramUnchanged:
		return fmt.Errorf("%w: its program was not replaced", ErrHandover)
	case codeProgramMismatch:
		return fmt.Errorf("%w: the program installed is not the one the release holds", ErrHandover)
	case codeHandoverFailed:
		return fmt.Errorf("%w: no successor took the vault over", ErrHandover)
	default:
		return errors.New("the vault process could not read the request")
	}
}

// newChallenge returns a fresh nonce and the challenge that carries it, encrypted to recipient. The caller
// overwrites the nonce once the proof was checked.
func newChallenge(recipient age.Recipient) (nonce, challenge []byte, err error) {
	nonce = make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("cannot read random bytes for the vault process challenge: %w", err)
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipient)
	if err == nil {
		_, err = w.Write(nonce)
	}
	if err == nil {
		err = w.Close()
	}
	if err != nil {
		clear(nonce)
		return nil, nil, fmt.Errorf("cannot encrypt the vault process challenge: %w", err)
	}
	return nonce, buf.Bytes(), nil
}

// solveChallenge decrypts a challenge with key and returns its proof. It refuses anything that does not
// decrypt to exactly one nonce, and it never returns what it decrypted, only a hash of it: a server that
// answered with the plaintext would decrypt any message encrypted to the vault, a pending entry included,
// for whoever asked.
func solveChallenge(key age.Identity, challenge []byte) ([]byte, bool) {
	if key == nil || len(challenge) == 0 {
		return nil, false
	}
	r, err := age.Decrypt(bytes.NewReader(challenge), key)
	if err != nil {
		return nil, false
	}
	nonce, err := io.ReadAll(io.LimitReader(r, nonceSize+1))
	defer clear(nonce)
	if err != nil || len(nonce) != nonceSize {
		return nil, false
	}
	return proofOf(nonce), true
}

// proofOf derives the proof of a nonce.
func proofOf(nonce []byte) []byte {
	h := sha256.New()
	h.Write([]byte(proofLabel))
	h.Write(nonce)
	return h.Sum(nil)
}

// checkProof compares a server's proof with the one nonce calls for, in constant time.
func checkProof(nonce, proof []byte) bool {
	want := proofOf(nonce)
	return subtle.ConstantTimeCompare(want, proof) == 1
}
