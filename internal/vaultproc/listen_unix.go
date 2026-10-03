//go:build linux || darwin

package vaultproc

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"syscall"
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
	return &lockedListener{UnixListener: listener, lock: lock}, nil
}

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
type lockedListener struct {
	*net.UnixListener
	lock *os.File
}

func (l *lockedListener) Close() error {
	err := l.UnixListener.Close()
	_ = l.lock.Close()
	return err
}
