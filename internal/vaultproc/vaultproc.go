// Package vaultproc keeps an unlocked vault's secrets in a process of their own, so later invocations of
// qatlas, the CLI and the MCP broker alike, read them without asking for the passphrase again.
//
// The process listens on a Unix socket in a private runtime directory and answers one request per
// connection: status, get, check, set, delete, bind, lock, for the invocation log, log and logcheck, and
// approve-token, which approves open connection changes an agent token covers. Every message is one line of JSON, versioned
// and bounded in size, and every connection has a deadline, so a peer that hangs cannot hold either side. The
// one exception is handover, which hands the unlocked vault to a successor started from the program an update
// installs, in two requests on one connection; see Client.PrepareHandover.
//
// A secret leaves the process only for a connection the vault approved (see vault.Bindings), and only for
// this very program run by this very user. Both ends check each other. The server checks the process that
// connected, its user and its program, before it answers anything; see VerifyProgram. The client checks the
// user of the process that listens, on macOS its program as well, and then challenges it to prove that it
// holds the vault's key, before it sends anything else, not even a credential name; see Client. A
// connection therefore carries two exchanges: the challenge and its proof, then the request and its answer.
//
// While it runs, the process is the one writer of the invocation log that signs its entries: a client hands
// it the fields of an invoke, and the process checks them, chains them, and signs them with the log key it
// derives from the vault's key, which never leaves it; see invokelog. It checks a stored line's check value
// the same way, answering only whether it matches.
//
// Nothing here writes a diagnostic log, and no error or answer other than a successful get carries a secret
// value. The
// process locks itself after a period without a get, when asked to, or when it ends; locking overwrites the
// values it holds as far as Go allows, which is the copies this package owns and no others.
package vaultproc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Version is the protocol version a request carries and an answer repeats. A server refuses a request of
// another version rather than guessing what it meant. Version 7 adds handover, which hands the vault over to
// a successor started from an updated program, and the answer replaced, which sends a client on to it.
const Version = 7

// MaxMessage bounds one request or answer, newline included. It leaves room for any token or key a
// credential holds, and keeps a peer from making the other side buffer without end.
const MaxMessage = 64 << 10

// DefaultIdleTimeout is how long the process holds the secrets without a get before it locks itself.
const DefaultIdleTimeout = 12 * time.Hour

// DefaultRequestTimeout bounds one request on either side of a connection. A caller's own, shorter limit
// wins on the client.
const DefaultRequestTimeout = 5 * time.Second

// HandoverTimeout bounds each phase of a handover on either side of its connection: the prepare, the wait for
// the commit while the program is replaced, and the commit, which starts the successor.
const HandoverTimeout = 30 * time.Second

// maxSocketPath is the longest socket path accepted, in bytes. The kernel's sun_path holds 108 bytes on
// Linux and 104 on macOS and the BSDs, terminator included; a longer path would be cut off silently, and
// the socket would appear somewhere else. The stricter bound holds on every platform.
const maxSocketPath = 103

var (
	// ErrNotRunning reports that no vault process answers at the socket: it is missing, nobody listens on
	// it any more, or the process is locking itself right now. Every one of those means the vault is not
	// unlocked in a process, which is a state rather than a failure.
	ErrNotRunning = errors.New("no vault process is running")

	// ErrRunning reports that another vault process already serves the socket.
	ErrRunning = errors.New("a vault process is already running")

	// ErrRefused reports a peer that is not this program run by this user. It wraps the reason, which
	// never contains a secret.
	ErrRefused = errors.New("the process at the other end of the vault socket is not this qatlas of this user")

	// ErrTooLarge reports a message above MaxMessage.
	ErrTooLarge = fmt.Errorf("a vault process message exceeds %d bytes", MaxMessage)

	// ErrVersion reports a vault process that speaks another protocol version, which is a process started
	// by another build of qatlas.
	ErrVersion = errors.New("the vault process speaks another protocol version; lock it and unlock the vault again")

	// ErrNoLog reports a vault process that does not keep the invocation log, or cannot sign it.
	ErrNoLog = errors.New("the vault process does not keep the invocation log")

	// ErrNoTokens reports a vault process that does not approve with agent tokens.
	ErrNoTokens = errors.New("the vault process does not approve with agent tokens")

	// ErrUnsupported reports a platform the vault process does not run on yet.
	ErrUnsupported = fmt.Errorf("the vault process is not supported on this platform: %w", errors.ErrUnsupported)
)

// PathTooLongError reports a socket path the kernel would cut off.
type PathTooLongError struct{ Path string }

func (e *PathTooLongError) Error() string {
	return fmt.Sprintf("the vault socket path %s is %d bytes long, more than the %d a socket path can hold; "+
		"use a shorter configuration directory or set XDG_RUNTIME_DIR", e.Path, len(e.Path), maxSocketPath)
}

func checkPathLength(path string) error {
	if len(path) > maxSocketPath {
		return &PathTooLongError{Path: path}
	}
	return nil
}

// userRuntimeRoot holds the per-user runtime directories systemd-logind creates on Linux; a variable so
// tests can point it at a directory of their own.
var userRuntimeRoot = "/run/user"

// SocketPath returns the socket of the vault process that serves the vault in vaultDir, as vault.Vault.Dir
// reports it. The socket lives in $XDG_RUNTIME_DIR/qatlas when the session has a runtime directory, which
// the system empties at logout or restart. Without the variable, as under agent hosts that start qatlas
// with a filtered environment, the user's runtime directory is found by its conventional place on
// platforms that have one, so such a process still reaches the vault process an unlock in a terminal
// started. Only when there is none the socket lives in run beside the vault, ~/.qatlas/cli/run by
// default. Its name is derived from the vault's absolute path, so a vault under another configuration
// directory, a test's included, never reaches the process of another vault through the shared runtime
// directory.
//
// On macOS, where a socket path holds fewer bytes, a path that would be too long there moves to qatlas in
// the per-user temporary directory ($TMPDIR) instead; every other platform, and a path too long even
// there, fails with a PathTooLongError.
func SocketPath(vaultDir string) (string, error) {
	abs, err := filepath.Abs(vaultDir)
	if err != nil {
		return "", fmt.Errorf("cannot determine the vault directory: %w", err)
	}
	sum := sha256.Sum256([]byte(abs))
	name := "vault-" + hex.EncodeToString(sum[:8]) + ".sock"

	dir := filepath.Join(filepath.Dir(abs), "run")
	// A relative XDG_RUNTIME_DIR is invalid by the specification and ignored like an unset one.
	if runtime := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(runtime) {
		dir = filepath.Join(runtime, "qatlas")
	} else if runtime := userRuntimeDir(); runtime != "" {
		dir = filepath.Join(runtime, "qatlas")
	}
	path := filepath.Join(dir, name)
	if err := checkPathLength(path); err != nil {
		if fallback, ok := fallbackSocketDir(); ok && checkPathLength(filepath.Join(fallback, name)) == nil {
			return filepath.Join(fallback, name), nil
		}
		return "", err
	}
	return path, nil
}

// Verifier checks the process at the other end of a connection and returns an error wrapping ErrRefused
// when it is not to be answered or told anything. VerifyProgram is the check every real run uses.
type Verifier func(conn net.Conn) error
