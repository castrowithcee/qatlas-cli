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
)

// The operations a request names.
const (
	opStatus = "status"
	opGet    = "get"
	opSet    = "set"
	opDelete = "delete"
	opLock   = "lock"
)

// The codes an answer's error carries. They are fixed words, never text built from a request, so an error
// can neither carry a secret nor echo what a peer sent.
const (
	codeRefused    = "refused"
	codeBadRequest = "bad-request"
	codeVersion    = "version"
	codeLocked     = "locked"
	codeChallenge  = "challenge"
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

// request is one line a client sends.
type request struct {
	V          int    `json:"v"`
	Op         string `json:"op"`
	Credential string `json:"credential,omitempty"`
	Role       string `json:"role,omitempty"`
	Value      string `json:"value,omitempty"`
}

// response is a line a server answers with. Error is empty on success; Proof answers the hello, Found and
// Value belong to get, PID and LocksAt to status.
type response struct {
	V       int        `json:"v"`
	Error   string     `json:"error,omitempty"`
	Proof   []byte     `json:"proof,omitempty"`
	Found   bool       `json:"found,omitempty"`
	Value   string     `json:"value,omitempty"`
	PID     int        `json:"pid,omitempty"`
	LocksAt *time.Time `json:"locks_at,omitempty"`
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

// answerError turns a response's error code into the error the client returns.
func answerError(code string) error {
	switch code {
	case codeRefused:
		return fmt.Errorf("%w: the vault process refused this program", ErrRefused)
	case codeVersion:
		return ErrVersion
	case codeLocked:
		return ErrNotRunning
	case codeChallenge:
		return errUnproven
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
