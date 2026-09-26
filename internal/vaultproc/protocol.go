package vaultproc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
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
)

// request is one line a client sends.
type request struct {
	V          int    `json:"v"`
	Op         string `json:"op"`
	Credential string `json:"credential,omitempty"`
	Role       string `json:"role,omitempty"`
	Value      string `json:"value,omitempty"`
}

// response is the one line a server answers with. Error is empty on success; Found and Value belong to get,
// PID and LocksAt to status.
type response struct {
	V       int        `json:"v"`
	Error   string     `json:"error,omitempty"`
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

// answerError turns a response's error code into the error the client returns.
func answerError(code string) error {
	switch code {
	case codeRefused:
		return fmt.Errorf("%w: the vault process refused this program", ErrRefused)
	case codeVersion:
		return ErrVersion
	case codeLocked:
		return ErrNotRunning
	default:
		return errors.New("the vault process could not read the request")
	}
}
