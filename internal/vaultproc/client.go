package vaultproc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"syscall"
	"time"
)

// Client reaches the vault process at one socket. Every call opens a connection of its own, checks the
// process that listens, and only then sends its request.
type Client struct {
	// Path is the socket, usually from SocketPath.
	Path string
	// Timeout bounds one call; the context a call gets bounds it too, and the earlier limit wins. Zero
	// means DefaultRequestTimeout.
	Timeout time.Duration
	// Verify checks the server before anything is sent. Nil means VerifyProgram; a test passes its own.
	Verify Verifier
}

// NewClient returns a client for the socket at path.
func NewClient(path string) *Client { return &Client{Path: path} }

// Status is what a running vault process reports about itself. It holds no secret and no credential name.
type Status struct {
	// PID is the process id of the vault process.
	PID int
	// LocksAt is when the process locks itself unless a get comes first.
	LocksAt time.Time
}

// Status reports on the running vault process. ErrNotRunning means there is none.
func (c *Client) Status(ctx context.Context) (Status, error) {
	resp, err := c.call(ctx, request{Op: opStatus})
	if err != nil {
		return Status{}, err
	}
	status := Status{PID: resp.PID}
	if resp.LocksAt != nil {
		status.LocksAt = *resp.LocksAt
	}
	return status, nil
}

// Get returns the secret of one credential role. found is false when the process holds nothing for the
// pair, which is not an error. ErrNotRunning means the vault is not unlocked in a process.
func (c *Client) Get(ctx context.Context, credential, role string) (value string, found bool, err error) {
	resp, err := c.call(ctx, request{Op: opGet, Credential: credential, Role: role})
	if err != nil {
		return "", false, err
	}
	return resp.Value, resp.Found, nil
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

// Lock asks the vault process to overwrite its secrets and end. ErrNotRunning means there was none.
func (c *Client) Lock(ctx context.Context) error {
	_, err := c.call(ctx, request{Op: opLock})
	return err
}

// call connects, checks the server, and exchanges one request for its answer.
func (c *Client) call(ctx context.Context, req request) (response, error) {
	if err := checkPathLength(c.Path); err != nil {
		return response{}, err
	}
	deadline := c.deadline(ctx)
	dialer := net.Dialer{Deadline: deadline}
	conn, err := dialer.DialContext(ctx, "unix", c.Path)
	if err != nil {
		if notRunning(err) {
			return response{}, ErrNotRunning
		}
		return response{}, c.failed(ctx, err)
	}
	defer conn.Close()
	return c.exchange(ctx, conn, deadline, req)
}

// deadline is the earlier of the client's own limit and the end of ctx.
func (c *Client) deadline(ctx context.Context) time.Time {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}
	deadline := time.Now().Add(timeout)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	return deadline
}

// exchange runs one request on an open connection. The server is checked before the request is written,
// so an unchecked server learns nothing, not even which credential was asked for. A context that ends
// early ends the exchange at once rather than at the deadline.
func (c *Client) exchange(ctx context.Context, conn net.Conn, deadline time.Time, req request) (response, error) {
	if err := conn.SetDeadline(deadline); err != nil {
		return response{}, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	verify := c.Verify
	if verify == nil {
		verify = VerifyProgram
	}
	if err := verify(conn); err != nil {
		return response{}, err
	}

	req.V = Version
	if err := writeMessage(conn, req); err != nil {
		return response{}, c.failed(ctx, err)
	}
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
// over from a process that ended and nobody listens on it any more.
func notRunning(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
}
