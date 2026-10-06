//go:build windows

package vaultproc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Supported reports whether a vault process runs on this platform.
const Supported = true

// pipePrefix is the namespace every local named pipe lives in. A vault path outside it names no pipe.
const pipePrefix = `\\.\pipe\`

// pipeBuffer is the size each direction of a pipe instance buffers: one message of either side fits.
const pipeBuffer = MaxMessage

// isPipePath reports whether path names a local named pipe.
func isPipePath(path string) bool {
	return len(path) > len(pipePrefix) && strings.EqualFold(path[:len(pipePrefix)], pipePrefix)
}

// pipePath returns the vault pipe of the vault at abs, an absolute path: named after this user's security
// identifier, so the names of two users never collide, and a hash of the path, compared without regard to
// case as the file system does, so two vaults of one user never share a pipe either.
func pipePath(abs string) (string, bool, error) {
	sid, err := currentUserSID()
	if err != nil {
		return "", true, err
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(abs))))
	return pipePrefix + "qatlas-vault-" + sid.String() + "-" + hex.EncodeToString(sum[:8]), true, nil
}

// pipeSecurity returns the security attributes every instance of a vault pipe is created with: a protected
// DACL that allows this user alone, and denies network logons outright on top of PIPE_REJECT_REMOTE_CLIENTS.
// Nobody else, other users, administrators, and the local groups included, can open the pipe or add an
// instance to it. Other processes of this user can do both; a client therefore checks the process that
// serves the instance it reached, and its key, before it sends anything (see Client).
func pipeSecurity() (*windows.SecurityAttributes, error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(D;;GA;;;NU)(A;;GA;;;" + sid.String() + ")")
	if err != nil {
		return nil, fmt.Errorf("cannot describe who may open the vault pipe: %w", err)
	}
	return &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}, nil
}

// createPipe creates one instance of the pipe at path. The first instance claims the name: with
// FILE_FLAG_FIRST_PIPE_INSTANCE it fails where any instance of that name exists already, whoever made it,
// so no other process can have put a pipe there first that this one would then serve beside.
func createPipe(path string, first bool, sa *windows.SecurityAttributes) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	flags := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
	if first {
		flags |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	mode := uint32(windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT |
		windows.PIPE_REJECT_REMOTE_CLIENTS)
	return windows.CreateNamedPipe(name, flags, mode, windows.PIPE_UNLIMITED_INSTANCES, pipeBuffer, pipeBuffer, 0, sa)
}

// Listen opens the vault pipe at path, a name from SocketPath, for a server to Serve on.
//
// The first instance of the pipe is created with FILE_FLAG_FIRST_PIPE_INSTANCE, which is what makes sure
// only one process serves the name: where an instance exists already, of a running vault process or of
// anything else, Listen fails with ErrRunning, and whoever answers there has to pass the client's check.
// Every instance allows this user alone and refuses remote clients. The listener always keeps one instance
// waiting for the next client before it hands out a connected one, so the name is never free for another
// process to take while the listener is open. Unlike a socket, a pipe leaves nothing behind: it is gone
// once the last of its handles is closed.
func Listen(path string) (net.Listener, error) {
	if err := checkPathLength(path); err != nil {
		return nil, err
	}
	if !isPipePath(path) {
		return nil, fmt.Errorf("%s is not a named pipe; the vault process does not listen there", path)
	}
	sa, err := pipeSecurity()
	if err != nil {
		return nil, err
	}
	first, err := createPipe(path, true, sa)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return nil, ErrRunning
		}
		return nil, fmt.Errorf("cannot create the vault pipe %s: %w", path, err)
	}
	closing, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(first)
		return nil, fmt.Errorf("cannot create the vault pipe %s: %w", path, err)
	}
	return &pipeListener{path: path, sa: sa, next: first, closing: closing}, nil
}

// pipeListener accepts the clients of one vault pipe, one instance per connection.
type pipeListener struct {
	path string
	sa   *windows.SecurityAttributes

	// acceptMu keeps Accept calls apart, and lets Close wait for the one in progress.
	acceptMu sync.Mutex
	mu       sync.Mutex
	closed   bool
	// next is the instance that waits for the next client; 0 while an Accept waits on it.
	next windows.Handle
	// closing is set by Close and wakes an Accept that waits for a client.
	closing windows.Handle
	// connecting holds the overlapped state of the connect in progress, so that it is allocated on the heap,
	// where it stays put for as long as the system may write to it.
	connecting *pipeOverlapped
}

// pipeOverlapped is the state of one overlapped operation. It is always reachable from a heap object until
// the operation completed, since the system writes to it after the call returned, and a goroutine's stack,
// unlike the heap, may move.
type pipeOverlapped struct {
	ov windows.Overlapped
}

func (l *pipeListener) Accept() (net.Conn, error) {
	l.acceptMu.Lock()
	defer l.acceptMu.Unlock()
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, net.ErrClosed
	}
	h := l.next
	l.next = 0
	l.mu.Unlock()

	err := l.connect(h)
	if errors.Is(err, net.ErrClosed) {
		_ = windows.CloseHandle(h)
		return nil, net.ErrClosed
	}
	// The next instance is created before the one just used is handed out or closed, so an instance of this
	// listener exists at every moment.
	next, createErr := createPipe(l.path, false, l.sa)
	if createErr != nil {
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("cannot open the next instance of the vault pipe: %w", createErr)
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		_ = windows.CloseHandle(next)
		_ = windows.CloseHandle(h)
		return nil, net.ErrClosed
	}
	l.next = next
	l.mu.Unlock()

	if err != nil && !errors.Is(err, windows.ERROR_NO_DATA) {
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("cannot accept a client of the vault pipe: %w", err)
	}
	// A client that closed its end before it was accepted is accepted all the same, as a socket's is: its
	// connection ends at its first read.
	return newPipeConn(h, l.path, true), nil
}

// connect waits until a client opens the instance h, or the listener closes. It returns only once the system
// no longer uses the overlapped state.
func (l *pipeListener) connect(h windows.Handle) error {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(event)
	op := &pipeOverlapped{}
	op.ov.HEvent = event
	l.connecting = op
	defer func() { l.connecting = nil }()

	err = windows.ConnectNamedPipe(h, &op.ov)
	switch {
	case err == nil, errors.Is(err, windows.ERROR_PIPE_CONNECTED):
		return nil
	case !errors.Is(err, windows.ERROR_IO_PENDING):
		return err
	}
	var n uint32
	which, err := windows.WaitForMultipleObjects([]windows.Handle{event, l.closing}, false, windows.INFINITE)
	if err == nil && which == windows.WAIT_OBJECT_0 {
		return windows.GetOverlappedResult(h, &op.ov, &n, false)
	}
	_ = windows.CancelIoEx(h, &op.ov)
	// Waits until the cancellation, or a client that came first, completed the connect.
	_ = windows.GetOverlappedResult(h, &op.ov, &n, true)
	switch {
	case err != nil:
		return err
	case which == windows.WAIT_OBJECT_0+1:
		return net.ErrClosed
	}
	return fmt.Errorf("waiting for a client ended with %#x", which)
}

// Close stops accepting: the waiting instance is closed, and an Accept in progress returns net.ErrClosed.
// Connections already accepted stay open until they are closed themselves.
func (l *pipeListener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	h := l.next
	l.next = 0
	l.mu.Unlock()

	_ = windows.SetEvent(l.closing)
	if h != 0 {
		_ = windows.CloseHandle(h)
	}
	// An Accept in progress gives up its instance first; only then is the event it waits on closed.
	l.acceptMu.Lock()
	_ = windows.CloseHandle(l.closing)
	l.acceptMu.Unlock()
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr(l.path) }

// pipeAddr is the address of either end of a vault pipe: its name.
type pipeAddr string

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return string(a) }

// AwaitHandover fails on Windows: a vault process here never hands the vault over to a successor, so no
// listener is ever taken over. A successor that is started all the same locks itself at once.
func AwaitHandover(context.Context, net.Listener, string, time.Duration) error {
	return fmt.Errorf("a vault pipe is never handed over: %w", errors.ErrUnsupported)
}

// terminate ends the process pid, the way a person ends a vault process with 'taskkill /F'. Windows has no
// signal a process could close itself on; the system takes its memory with it. It is a variable only so a
// test can see the call without ending itself.
var terminate = func(pid int) error {
	if pid <= 0 {
		return errors.New("no process to end")
	}
	process, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	return windows.TerminateProcess(process, 1)
}

// dialSocket opens the vault pipe at path as a client. A path that names no pipe, or a pipe nobody serves,
// is reported as not existing, which the client takes for no vault process at all. Where every instance is
// busy, it tries again until deadline or the end of ctx.
//
// The pipe is opened at the identification level: the process at the other end may learn who connected,
// but can never act as this user, whatever process it turns out to be.
func dialSocket(ctx context.Context, path string, deadline time.Time) (net.Conn, error) {
	if !isPipePath(path) {
		return nil, fmt.Errorf("%s is not a named pipe: %w", path, fs.ErrNotExist)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("%s is not a named pipe: %w", path, fs.ErrNotExist)
	}
	for {
		h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
		if err == nil {
			if kind, err := windows.GetFileType(h); err != nil || kind != windows.FILE_TYPE_PIPE {
				_ = windows.CloseHandle(h)
				return nil, fmt.Errorf("%s is not a named pipe: %w", path, fs.ErrNotExist)
			}
			return newPipeConn(h, path, false), nil
		}
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return nil, &os.PathError{Op: "open", Path: path, Err: err}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !time.Now().Before(deadline) {
			return nil, os.ErrDeadlineExceeded
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Directions of an operation on a pipeConn, which index its deadlines.
const (
	dirRead = iota
	dirWrite
)

// pipeConn is one end of a connected vault pipe instance, with overlapped reads and writes, so a deadline
// or Close ends an operation in progress.
type pipeConn struct {
	h    windows.Handle
	path string
	// server is true for the server's end, whose peer is the client.
	server bool

	mu       sync.Mutex
	closed   bool
	deadline [2]time.Time
	// ops holds every operation in progress, which keeps its overlapped state on the heap until it is done.
	ops      map[*pipeOp]struct{}
	inflight sync.WaitGroup
}

// pipeOp is one read or write in progress.
type pipeOp struct {
	pipeOverlapped
	dir     int
	timer   *time.Timer
	expired bool
}

func newPipeConn(h windows.Handle, path string, server bool) *pipeConn {
	return &pipeConn{h: h, path: path, server: server, ops: map[*pipeOp]struct{}{}}
}

func (c *pipeConn) Read(b []byte) (int, error) { return c.do(dirRead, b) }

// Write writes all of b, in as many operations as the pipe takes.
func (c *pipeConn) Write(b []byte) (int, error) {
	total := 0
	for total < len(b) {
		n, err := c.do(dirWrite, b[total:])
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

// do runs one overlapped read or write and waits for it. The operation is cancelled once its deadline
// passes or the connection closes; the wait then still lasts until the system confirmed the cancellation,
// so neither the buffer nor the overlapped state is ever written to after do returned.
func (c *pipeConn) do(dir int, b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(event)
	op := &pipeOp{dir: dir}
	op.ov.HEvent = event

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, net.ErrClosed
	}
	deadline := c.deadline[dir]
	if !deadline.IsZero() && !time.Now().Before(deadline) {
		c.mu.Unlock()
		return 0, os.ErrDeadlineExceeded
	}
	c.ops[op] = struct{}{}
	c.inflight.Add(1)
	c.arm(op)
	c.mu.Unlock()

	var n uint32
	if dir == dirWrite {
		err = windows.WriteFile(c.h, b, &n, &op.ov)
	} else {
		err = windows.ReadFile(c.h, b, &n, &op.ov)
	}
	if err == nil || errors.Is(err, windows.ERROR_IO_PENDING) {
		// A deadline or Close that came between the check above and the start of the operation found
		// nothing to cancel yet; it is cancelled here instead.
		c.mu.Lock()
		if c.closed || op.expired {
			_ = windows.CancelIoEx(c.h, &op.ov)
		}
		c.mu.Unlock()
		err = windows.GetOverlappedResult(c.h, &op.ov, &n, true)
	}

	c.mu.Lock()
	delete(c.ops, op)
	if op.timer != nil {
		op.timer.Stop()
	}
	expired, closed := op.expired, c.closed
	c.mu.Unlock()
	c.inflight.Done()

	switch {
	case err == nil:
		return int(n), nil
	case errors.Is(err, windows.ERROR_OPERATION_ABORTED) && closed:
		return int(n), net.ErrClosed
	case errors.Is(err, windows.ERROR_OPERATION_ABORTED) && expired:
		return int(n), os.ErrDeadlineExceeded
	case errors.Is(err, windows.ERROR_BROKEN_PIPE), errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED),
		errors.Is(err, windows.ERROR_NO_DATA):
		// The other end closed: an end of file for a read, a broken pipe for a write.
		if dir == dirRead {
			return int(n), io.EOF
		}
		return int(n), syscall.EPIPE
	}
	return int(n), err
}

// arm sets op's timer to its direction's deadline. The caller holds c.mu.
func (c *pipeConn) arm(op *pipeOp) {
	if op.timer != nil {
		op.timer.Stop()
		op.timer = nil
	}
	deadline := c.deadline[op.dir]
	if deadline.IsZero() {
		return
	}
	op.timer = time.AfterFunc(time.Until(deadline), func() { c.expire(op) })
}

// expire cancels op once its direction's deadline, as it is now, has passed.
func (c *pipeConn) expire(op *pipeOp) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, live := c.ops[op]; !live {
		return
	}
	deadline := c.deadline[op.dir]
	if deadline.IsZero() || time.Now().Before(deadline) {
		return
	}
	op.expired = true
	_ = windows.CancelIoEx(c.h, &op.ov)
}

func (c *pipeConn) setDeadline(t time.Time, dirs ...int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return net.ErrClosed
	}
	for _, dir := range dirs {
		c.deadline[dir] = t
	}
	for op := range c.ops {
		for _, dir := range dirs {
			if op.dir == dir {
				c.arm(op)
			}
		}
	}
	return nil
}

func (c *pipeConn) SetDeadline(t time.Time) error      { return c.setDeadline(t, dirRead, dirWrite) }
func (c *pipeConn) SetReadDeadline(t time.Time) error  { return c.setDeadline(t, dirRead) }
func (c *pipeConn) SetWriteDeadline(t time.Time) error { return c.setDeadline(t, dirWrite) }

// Close cancels whatever is in progress, waits for it to end, and closes the instance. What this end wrote
// before stays readable for the other end, which then reads an end of file: the instance is closed, never
// disconnected, which would discard it.
func (c *pipeConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	_ = windows.CancelIoEx(c.h, nil)
	c.mu.Unlock()
	c.inflight.Wait()
	return windows.CloseHandle(c.h)
}

func (c *pipeConn) LocalAddr() net.Addr  { return pipeAddr(c.path) }
func (c *pipeConn) RemoteAddr() net.Addr { return pipeAddr(c.path) }
