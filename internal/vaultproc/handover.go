package vaultproc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"filippo.io/age"
	"golang.org/x/crypto/ssh"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/release"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// handoverOperation is the operation the outcome of a committed or failed handover is logged under.
const handoverOperation = "vault.handover"

// The largest release files a prepare reads, and the largest program a commit hashes.
const (
	maxChecksumsFile = 1 << 20
	maxSignatureFile = 1 << 20
	maxArchiveFile   = 128 << 20
	maxProgramFile   = 128 << 20
)

// StartSuccessor starts the successor of a vault process from program and hands it snap. program is the file
// that replaced the vault process's own program at the path it was started from, already checked against the
// release. It returns once the successor listens at NextPath of the vault socket, with its process id and a
// function that ends it; on an error the successor is ended already. ctx bounds the start.
type StartSuccessor func(ctx context.Context, program string, snap vault.Snapshot) (pid int, end func(), err error)

// handoverConfig is what AllowHandover sets.
type handoverConfig struct {
	vaultDir string
	keys     []ssh.PublicKey
	start    StartSuccessor
}

// handoverListener is a listener a successor can take over, which is what Listen returns on platforms that
// run a vault process.
type handoverListener interface {
	// handOver moves the successor's lock and then its socket onto the listener's own paths; from then on,
	// closing the listener leaves both alone.
	handOver() error
	// removeNext removes what a successor that did not take over left beside the socket.
	removeNext()
}

// AllowHandover lets the server hand the vault over to a successor started from the program an update
// installs, as a handover request asks (see Client.PrepareHandover). vaultDir is the vault directory whose
// settings.age says whether an update hands over or locks, read with the server's own key at every prepare;
// keys are the public keys a release must be signed with; start starts the successor. All three come from the
// program and the configuration the process was started with, never from a request. A server without it
// answers a handover with an error and locks itself. Call it before Serve.
func (s *Server) AllowHandover(vaultDir string, keys []ssh.PublicKey, start StartSuccessor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handover = handoverConfig{vaultDir: vaultDir, keys: keys, start: start}
}

// serveHandover carries out a checked handover request, which takes over its connection: the prepare, then,
// on the same connection, the commit. Every way it ends other than a completed handover or the setting lock
// locks the server, with the reason as the answer's code; a client that goes away between the phases ends it
// the same way. A second handover while one is under way is only refused: it does not disturb the first.
func (s *Server) serveHandover(conn net.Conn, req request) {
	if code := s.claimHandover(conn); code != "" {
		_ = writeMessage(conn, response{V: Version, Error: code})
		return
	}
	defer s.releaseHandover()
	if req.Phase != phasePrepare || req.Release == nil {
		s.failHandover(conn, codeBadRequest)
		return
	}
	_ = conn.SetDeadline(time.Now().Add(HandoverTimeout))
	hash, behaviour, code := s.prepare(*req.Release)
	switch {
	case code != "":
		s.failHandover(conn, code)
		return
	case behaviour != vault.UpdateHandover:
		// The vault asks an update to lock it: the process does so at once, as on a lock request.
		s.stop()
		_ = writeMessage(conn, response{V: Version, Handover: string(vault.UpdateLock)})
		return
	}
	if writeMessage(conn, response{V: Version, Handover: string(vault.UpdateHandover)}) != nil {
		s.failHandover(conn, codeHandoverFailed)
		return
	}

	// The client replaces the program now, and commits on this connection, which was checked while it still
	// ran the program this process runs.
	_ = conn.SetDeadline(time.Now().Add(HandoverTimeout))
	var commit request
	if err := readMessage(conn, &commit); err != nil {
		s.failHandover(conn, codeHandoverFailed)
		return
	}
	if commit.V != Version || commit.Op != opHandover || commit.Phase != phaseCommit {
		s.failHandover(conn, codeBadRequest)
		return
	}
	_ = conn.SetDeadline(time.Now().Add(HandoverTimeout))
	pid, code := s.commit(hash)
	if code != "" {
		s.failHandover(conn, code)
		return
	}
	// The successor serves the socket now. What is left here is the record, the answer, and the lock, which
	// overwrites the secrets once the last connection is done.
	s.logHandover("success")
	_ = writeMessage(conn, response{V: Version, PID: pid})
	s.stop()
}

// claimHandover makes conn the one handover of the server, or returns the code that refuses it: a server
// that handed over or is locking, or one with another handover under way.
func (s *Server) claimHandover(conn net.Conn) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.handingOver:
		return codeReplaced
	case s.locked || s.stopping():
		return codeLocked
	case s.pending != nil:
		return codeHandoverFailed
	}
	s.pending = conn
	return ""
}

func (s *Server) releaseHandover() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = nil
}

// failHandover records a handover that did not happen, locks the server, and answers with code, so that no
// client is told of the failure while the server still accepts connections. Requests that arrive from here
// on are answered as locked rather than replaced: no successor holds the vault.
func (s *Server) failHandover(conn net.Conn, code string) {
	s.logHandover(code)
	s.mu.Lock()
	s.handingOver = false
	s.mu.Unlock()
	s.stop()
	_ = conn.SetWriteDeadline(time.Now().Add(s.requestTimeout()))
	_ = writeMessage(conn, response{V: Version, Error: code})
}

// logHandover appends the outcome of a handover to the invocation log the server keeps, if it keeps one.
func (s *Server) logHandover(result string) {
	s.mu.Lock()
	logger := s.log
	s.mu.Unlock()
	logDecision(logger, invokelog.Fields{Path: "cli", Operation: handoverOperation, Result: result})
}

// prepare verifies the release files and reads the vault's update setting, both by the server itself. It
// returns the SHA-256 of the program in the release archive and the setting, or the code of the failure.
func (s *Server) prepare(files ReleaseFiles) (hash string, behaviour vault.UpdateBehaviour, code string) {
	s.mu.Lock()
	config, key := s.handover, s.key
	locked := s.locked || s.stopping()
	s.mu.Unlock()
	id, ok := key.(*age.X25519Identity)
	switch {
	case locked:
		return "", "", codeLocked
	case config.start == nil || !ok:
		return "", "", codeHandoverFailed
	}
	hash, err := verifyRelease(files, config.keys)
	if err != nil {
		return "", "", codeReleaseUnverified
	}
	// A setting the vault cannot trust reads as lock, which is what is done then.
	behaviour, _ = vault.ReadUpdateBehaviour(config.vaultDir, id)
	return hash, behaviour, ""
}

// commit hands the vault over to a successor started from the program that replaced this one, which must be
// the program of the verified release, hash. From the moment it begins, new requests are answered replaced;
// those already read are answered first, so the snapshot the successor gets holds every change they made. Any
// failure before the successor took the socket over ends the successor and leaves nothing of it behind; the
// caller then locks.
func (s *Server) commit(hash string) (int, string) {
	s.mu.Lock()
	if s.locked || s.stopping() {
		s.mu.Unlock()
		return 0, codeLocked
	}
	s.handingOver = true
	// The successor takes over the time this process locks at; until then, no timer here is needed.
	if s.timer != nil {
		s.timer.Stop()
	}
	config, l := s.handover, s.listener
	s.mu.Unlock()
	if !s.drain() {
		return 0, codeHandoverFailed
	}

	program, replaced := s.ownProgram()
	if !replaced || !filepath.IsAbs(program) {
		return 0, codeProgramUnchanged
	}
	if got, err := programHash(program); err != nil || got != hash {
		return 0, codeProgramMismatch
	}
	swap, ok := l.(handoverListener)
	if !ok {
		return 0, codeHandoverFailed
	}
	snap, ok := s.snapshot()
	if !ok {
		return 0, codeHandoverFailed
	}

	ctx, cancel := context.WithTimeout(context.Background(), HandoverTimeout)
	defer cancel()
	go func() {
		select {
		case <-s.stopped:
			cancel()
		case <-ctx.Done():
		}
	}()
	pid, end, err := config.start(ctx, program, snap)
	if err != nil {
		swap.removeNext()
		return 0, codeHandoverFailed
	}
	// The socket changes hands under s.mu, which stop takes as well: a lock that comes first ends the
	// successor instead, and one that comes after finds the socket handed over already.
	s.mu.Lock()
	if s.stopping() {
		err = errors.New("the server is locking")
	} else {
		err = swap.handOver()
	}
	s.mu.Unlock()
	if err != nil {
		end()
		swap.removeNext()
		return 0, codeHandoverFailed
	}
	return pid, ""
}

// drain waits until every request read before the handover was committed is answered, within the time one
// request may take.
func (s *Server) drain() bool {
	deadline := time.Now().Add(s.requestTimeout())
	for {
		s.mu.Lock()
		n := s.inFlight
		s.mu.Unlock()
		switch {
		case n == 0:
			return true
		case s.stopping() || time.Now().After(deadline):
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// snapshot returns what the server holds, in the shape a vault process is handed it, with the time it locks
// at. The values are copied into strings for the successor's pipe; only the server's own copies can be
// overwritten when it locks.
func (s *Server) snapshot() (vault.Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.key.(*age.X25519Identity)
	if !ok || s.locked || s.stopping() {
		return vault.Snapshot{}, false
	}
	secrets := make(map[string]map[string]string, len(s.secrets))
	for credential, roles := range s.secrets {
		secrets[credential] = make(map[string]string, len(roles))
		for role, value := range roles {
			secrets[credential][role] = string(value)
		}
	}
	locksAt := s.locksAt
	return vault.Snapshot{Identity: id.String(), Secrets: secrets, Bindings: copyBindings(s.bindings),
		LocksAt: &locksAt}, true
}

// verifyRelease checks the release files the way 'qatlas update' does, by itself: the signature over
// checksums.txt by one of keys, the archive's checksum listed there, and then the hash of the program in the
// archive, which it returns. The error never reaches a client.
func verifyRelease(files ReleaseFiles, keys []ssh.PublicKey) (string, error) {
	name := files.ArchiveName
	if name == "" || len(name) > 255 || name != filepath.Base(name) {
		return "", errors.New("the archive name is not a file name")
	}
	checksums, err := readReleaseFile(files.Checksums, maxChecksumsFile)
	if err != nil {
		return "", err
	}
	signature, err := readReleaseFile(files.Signature, maxSignatureFile)
	if err != nil {
		return "", err
	}
	if err := release.VerifyChecksums(checksums, signature, keys); err != nil {
		return "", err
	}
	want, err := release.ChecksumFor(checksums, name)
	if err != nil {
		return "", err
	}
	archive, err := readReleaseFile(files.Archive, maxArchiveFile)
	if err != nil {
		return "", err
	}
	if sum := sha256.Sum256(archive); hex.EncodeToString(sum[:]) != want {
		return "", errors.New("the archive does not match its checksum")
	}
	return release.ProgramSHA256(name, archive)
}

// readReleaseFile reads a regular file at an absolute path, of at most limit bytes. It is only ever read: a
// path from a client names data to verify, never a program to run.
func readReleaseFile(path string, limit int64) ([]byte, error) {
	f, err := openRegular(path, limit)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("the file is too large")
	}
	return data, nil
}

// programHash returns the hex SHA-256 of the program file at path.
func programHash(path string) (string, error) {
	f, err := openRegular(path, maxProgramFile)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxProgramFile+1))
	if err != nil {
		return "", err
	}
	if n > maxProgramFile {
		return "", errors.New("the program is too large")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// openRegular opens path, which must be absolute and name a regular file of at most limit bytes itself, not
// through a symbolic link, and be the same file once open.
func openRegular(path string, limit int64) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("the path is not absolute")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, errors.New("the file cannot be read")
	}
	if !before.Mode().IsRegular() || before.Size() > limit {
		return nil, fmt.Errorf("the file is not a regular file of at most %d bytes", limit)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("the file cannot be read")
	}
	if after, err := f.Stat(); err != nil || !os.SameFile(before, after) {
		_ = f.Close()
		return nil, errors.New("the file changed while it was opened")
	}
	return f, nil
}
