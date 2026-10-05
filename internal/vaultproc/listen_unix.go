//go:build linux || darwin

package vaultproc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Supported reports whether a vault process runs on this platform.
const Supported = true

// Modes of the socket and of the directory it lives in: both are reachable by their owner alone.
const (
	socketMode = 0o600
	dirMode    = 0o700
)

// Listen opens the vault socket at path for a server to Serve on.
//
// The directory is created at 0700 when it does not exist, and refused when it exists but is a symbolic
// link, belongs to another user, or is open to others. The socket is 0600.
//
// A lock file beside the socket, held for as long as the listener is open, makes sure only one process
// serves the path. Holding it also proves that a socket left at the path is dead: its server would still
// hold the lock. Such a socket is replaced. Anything else at the path, a file or a socket of another user,
// is left alone and refused, since it is not this program's to remove.
func Listen(path string) (net.Listener, error) {
	if err := checkPathLength(path); err != nil {
		return nil, err
	}
	if err := privateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}

	lock, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, socketMode)
	if err != nil {
		return nil, fmt.Errorf("cannot open the lock beside the vault socket: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrRunning
		}
		return nil, fmt.Errorf("cannot lock the vault socket: %w", err)
	}

	listener, err := listenLocked(path)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	own, err := os.Lstat(path)
	if err != nil {
		_ = listener.Close()
		_ = lock.Close()
		return nil, fmt.Errorf("cannot inspect %s: %w", path, err)
	}
	// Close removes the socket itself, at the path it has by then and only while it is still this one: a
	// handover moves it, or hands its path to a successor.
	listener.SetUnlinkOnClose(false)
	return &lockedListener{UnixListener: listener, lock: lock, own: own, path: path}, nil
}

// nextSuffix names the socket a successor listens on beside the vault socket until the handover moves it
// onto the vault socket; with ".lock" appended, it names the successor's lock.
const nextSuffix = ".next"

// NextPath returns the path the successor of the vault process at socket listens on until the handover
// moves its socket, and its lock beside it, onto socket.
func NextPath(socket string) string { return socket + nextSuffix }

// listenLocked replaces a dead socket of this user and listens at path. The caller holds the lock.
func listenLocked(path string) (*net.UnixListener, error) {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("cannot inspect %s: %w", path, err)
	case info.Mode().Type() != fs.ModeSocket || !ownedByMe(info):
		return nil, fmt.Errorf("%s exists and is not a socket of this user; it is left alone, remove it "+
			"yourself if it is not needed", path)
	default:
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("cannot replace the dead vault socket %s: %w", path, err)
		}
	}

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("cannot listen on %s: %w", path, err)
	}
	// The directory already keeps others out between bind and chmod; the mode says the same on the
	// socket itself.
	if err := os.Chmod(path, socketMode); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("cannot set the permissions of %s: %w", path, err)
	}
	return listener, nil
}

// privateDir makes sure dir exists, is a real directory of this user, and is closed to everyone else.
func privateDir(dir string) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, dirMode); err != nil {
			return fmt.Errorf("cannot create %s: %w", dir, err)
		}
		// The umask may have taken bits away; the mode is set as meant.
		if err := os.Chmod(dir, dirMode); err != nil {
			return fmt.Errorf("cannot set the permissions of %s: %w", dir, err)
		}
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return fmt.Errorf("cannot inspect %s: %w", dir, err)
	}
	switch {
	case !info.IsDir():
		return fmt.Errorf("%s is not a directory; the vault socket is not placed there", dir)
	case !ownedByMe(info):
		return fmt.Errorf("%s belongs to another user; the vault socket is not placed there", dir)
	case info.Mode().Perm()&0o077 != 0:
		return fmt.Errorf("%s is open to other users (mode %04o); the vault socket is not placed there "+
			"until it is private again: chmod 700 %s", dir, info.Mode().Perm(), dir)
	}
	return nil
}

func ownedByMe(info fs.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}

// lockedListener is the socket together with the lock that makes it this process's. Closing removes the
// socket first and releases the lock after, so a successor never finds its own fresh socket removed.
//
// own is the socket as a file, and path where it is now: where it was created, or the vault socket once a
// handover moved a successor's socket there. Once this listener handed its path over, closing it leaves the
// socket at the path alone, since it is the successor's.
type lockedListener struct {
	*net.UnixListener
	lock *os.File
	own  fs.FileInfo

	mu         sync.Mutex
	path       string
	handedOver bool
}

func (l *lockedListener) Close() error {
	l.mu.Lock()
	path, handedOver := l.path, l.handedOver
	l.mu.Unlock()
	err := l.UnixListener.Close()
	if now, statErr := os.Lstat(path); !handedOver && statErr == nil && os.SameFile(now, l.own) {
		_ = os.Remove(path)
	}
	_ = l.lock.Close()
	return err
}

// handOver moves the successor's lock and then its socket, both beside this listener's at NextPath, onto this
// listener's own paths. Each rename is atomic, and the lock goes first: from the first rename on, no other
// process can take the vault socket, and from the second, every new connection reaches the successor. Only
// one process ever serves the vault socket: this listener's socket no longer has a path once the second
// rename is done, and closing the listener leaves the successor's alone.
func (l *lockedListener) handOver() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	next := NextPath(l.path)
	info, err := os.Lstat(next)
	if err != nil || info.Mode().Type() != fs.ModeSocket || !ownedByMe(info) {
		return errors.New("the successor's socket is missing")
	}
	if err := os.Rename(next+".lock", l.path+".lock"); err != nil {
		return err
	}
	if err := os.Rename(next, l.path); err != nil {
		return err
	}
	l.handedOver = true
	return nil
}

// removeNext removes the socket and the lock a successor that did not take over left at NextPath.
func (l *lockedListener) removeNext() {
	l.mu.Lock()
	next := NextPath(l.path)
	l.mu.Unlock()
	_ = os.Remove(next)
	_ = os.Remove(next + ".lock")
}

// AwaitHandover waits, in a successor, until the vault process that started it moved l, the listener Listen
// opened at NextPath(socket), onto socket, and from then on treats socket as l's own, which closing l removes.
// It fails once timeout passes or ctx ends first; the successor then locks itself, so a successor that never
// took over does not keep the secrets.
func AwaitHandover(ctx context.Context, l net.Listener, socket string, timeout time.Duration) error {
	locked, ok := l.(*lockedListener)
	if !ok {
		return errors.New("the listener cannot be handed over")
	}
	deadline := time.Now().Add(timeout)
	for {
		if now, err := os.Lstat(socket); err == nil && os.SameFile(now, locked.own) {
			locked.mu.Lock()
			locked.path = socket
			locked.mu.Unlock()
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("the vault process did not hand the vault socket over in time")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// terminate asks the process pid to end, the way a person ends a vault process with 'kill'. It is a variable
// only so a test can see the call without ending itself.
var terminate = func(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
