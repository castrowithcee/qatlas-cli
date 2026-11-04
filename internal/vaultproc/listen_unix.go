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
	if err := lockSocket(lock, path); err != nil {
		_ = lock.Close()
		if errors.Is(err, ErrRunning) {
			return nil, err
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

// releaseWait bounds how long Listen waits for the lock of a vault process that no longer has its socket.
const releaseWait = time.Second

// lockSocket takes the lock beside the socket at path, or reports ErrRunning. A process removes its socket
// before it releases the lock, so a held lock without a socket belongs to a process that is ending, or one
// about to listen: whoever starts a vault process right after ending one waits for the lock rather than
// failing, while a socket that is there is reported at once.
func lockSocket(lock *os.File, path string) error {
	deadline := time.Now().Add(releaseWait)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		if _, statErr := os.Lstat(path); !errors.Is(statErr, fs.ErrNotExist) || time.Now().After(deadline) {
			return ErrRunning
		}
		time.Sleep(10 * time.Millisecond)
	}
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
	_, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, dirMode); err != nil {
			return fmt.Errorf("cannot create %s: %w", dir, err)
		}
		// The umask may have taken bits away; the mode is set as meant.
		if err := os.Chmod(dir, dirMode); err != nil {
			return fmt.Errorf("cannot set the permissions of %s: %w", dir, err)
		}
	} else if err != nil {
		return fmt.Errorf("cannot inspect %s: %w", dir, err)
	}
	return checkPrivateDir(dir)
}

// checkPrivateDir is privateDir for a directory that must exist already: it only looks.
func checkPrivateDir(dir string) error {
	info, err := os.Lstat(dir)
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
// socket first, stops serving it, and releases the lock last, so a successor never finds its own fresh socket
// removed.
//
// own is the socket as a file, and path where it is now: where it was created, or the vault socket once a
// handover moved a successor's socket there. Once this listener handed its path over, closing it leaves the
// socket at the path alone, since it is the successor's. legacy is the socket of a vault process of the
// earlier release that a successor waits to be handed over by, with what it placed beside it, legacyMark and
// legacyLock; see ListenSuccessor.
type lockedListener struct {
	*net.UnixListener
	lock *os.File
	own  fs.FileInfo

	mu         sync.Mutex
	path       string
	handedOver bool
	legacy     string
	legacyMark fs.FileInfo
	legacyLock *os.File
}

func (l *lockedListener) Close() error {
	l.mu.Lock()
	path, handedOver := l.path, l.handedOver
	l.leaveLegacy()
	l.mu.Unlock()
	// The socket goes before the listener stops serving: a client that finds no process here then finds no
	// socket either, which is what lockSocket waits on. The lock, still held, keeps any other socket off path.
	if now, statErr := os.Lstat(path); !handedOver && statErr == nil && os.SameFile(now, l.own) {
		_ = os.Remove(path)
	}
	err := l.UnixListener.Close()
	beforeUnlock()
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

// ListenSuccessor opens the listener of a successor that serves the vault in vaultDir, handed over by the
// vault process that serves the vault in fromDir: Listen at NextPath of its socket, which the vault process
// that started it moves onto the socket (see AwaitHandover).
//
// A vault process of the release before qatlas-connect serves the socket that release placed in its own
// runtime directory, and hands over only what it finds beside that one. Where such a process serves the vault
// in fromDir and none serves this release's socket for vaultDir, the successor listens at this release's
// socket at once, but accepts nothing before the handover, and leaves beside the earlier socket a lock it
// holds and a socket nothing listens on, which the earlier process moves onto its own. The earlier socket
// must pass the checks Listen makes of its own: a socket of this user in a directory nobody else may enter,
// with its lock held. Nothing already there is replaced. Only such a process hands over a vault in another
// directory than the one the successor serves; without one, a successor for another directory fails.
func ListenSuccessor(vaultDir, fromDir string) (net.Listener, error) {
	socket, err := SocketPath(vaultDir)
	if err != nil {
		return nil, err
	}
	legacy, err := socketPath(fromDir, legacyRuntimeDirName)
	if err != nil {
		legacy = ""
	}
	if filepath.Clean(fromDir) != filepath.Clean(vaultDir) && !takesOverEarlier(socket, legacy) {
		return nil, errors.New("no vault process of the earlier release serves the vault handed over")
	}
	return listenSuccessor(socket, legacy)
}

// EarlierServes reports whether a vault process of the release before qatlas-connect serves the vault in
// vaultDir and none of this release does, which is when a successor takes over from that process.
func EarlierServes(vaultDir string) bool {
	socket, err := SocketPath(vaultDir)
	if err != nil {
		return false
	}
	legacy, err := socketPath(vaultDir, legacyRuntimeDirName)
	return err == nil && takesOverEarlier(socket, legacy)
}

// listenSuccessor is ListenSuccessor for this release's socket and the earlier release's, legacy, which is
// empty where there is none.
func listenSuccessor(socket, legacy string) (net.Listener, error) {
	if !takesOverEarlier(socket, legacy) {
		return Listen(NextPath(socket))
	}
	l, err := Listen(socket)
	if err != nil {
		return nil, err
	}
	if err := l.(*lockedListener).markBeside(legacy); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("cannot prepare the handover from the vault process of the earlier release: %w", err)
	}
	return l, nil
}

// takesOverEarlier reports whether a successor of this release's socket takes over from a vault process of
// the earlier release at legacy: one serves legacy, and none serves socket.
func takesOverEarlier(socket, legacy string) bool {
	return legacy != "" && legacy != socket && !serves(socket) && serves(legacy)
}

// serves reports whether a vault process serves the socket at path: there is a socket of this user, in a
// directory only this user can enter, and the lock beside it is held. It only looks; the lock it tries is
// let go at once.
func serves(path string) bool {
	if checkPrivateDir(filepath.Dir(path)) != nil {
		return false
	}
	if info, err := os.Lstat(path); err != nil || info.Mode().Type() != fs.ModeSocket || !ownedByMe(info) {
		return false
	}
	lock, err := os.OpenFile(path+".lock", os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return false
	}
	defer lock.Close()
	if info, err := lock.Stat(); err != nil || !info.Mode().IsRegular() || !ownedByMe(info) {
		return false
	}
	return errors.Is(syscall.Flock(int(lock.Fd()), syscall.LOCK_SH|syscall.LOCK_NB), syscall.EWOULDBLOCK)
}

// markBeside places at NextPath of legacy, the socket of a vault process of the earlier release, what that
// process hands over: a lock this listener holds, and a socket that is bound but never listened on, so no
// connection reaches anything there. Once the earlier process moved both onto its own paths, the handover is
// done (see AwaitHandover).
func (l *lockedListener) markBeside(legacy string) error {
	next := NextPath(legacy)
	if _, err := os.Lstat(next); !errors.Is(err, fs.ErrNotExist) {
		return errors.New("another successor's socket is in the way")
	}
	lock, err := os.OpenFile(next+".lock", os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, socketMode)
	if err != nil {
		return err
	}
	mark, err := bindOnly(next)
	if err == nil {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	}
	if err != nil {
		if mark != nil {
			_ = os.Remove(next)
		}
		_ = lock.Close()
		_ = os.Remove(next + ".lock")
		return err
	}
	l.mu.Lock()
	l.legacy, l.legacyMark, l.legacyLock = legacy, mark, lock
	l.mu.Unlock()
	return nil
}

// bindOnly creates a socket at path that nothing ever listens on, private to this user, and returns it as a
// file.
func bindOnly(path string) (fs.FileInfo, error) {
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, err
	}
	err = syscall.Bind(fd, &syscall.SockaddrUnix{Name: path})
	_ = syscall.Close(fd)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, socketMode); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return os.Lstat(path)
}

// leaveLegacy removes what markBeside placed, wherever the earlier process moved it: the socket first, then
// the lock, which it lets go last, as Close does. The caller holds l.mu.
func (l *lockedListener) leaveLegacy() {
	if l.legacy == "" {
		return
	}
	for _, path := range []string{l.legacy, NextPath(l.legacy)} {
		if now, err := os.Lstat(path); err == nil && os.SameFile(now, l.legacyMark) {
			_ = os.Remove(path)
		}
	}
	if held, err := l.legacyLock.Stat(); err == nil {
		for _, path := range []string{l.legacy + ".lock", NextPath(l.legacy) + ".lock"} {
			if now, err := os.Lstat(path); err == nil && os.SameFile(now, held) {
				_ = os.Remove(path)
			}
		}
	}
	_ = l.legacyLock.Close()
	l.legacy, l.legacyMark, l.legacyLock = "", nil, nil
}

// AwaitHandover waits, in a successor, until the vault process that started it moved l, the listener
// ListenSuccessor opened, onto socket, and from then on treats socket as l's own, which closing l removes. A
// vault process of the earlier release moves what ListenSuccessor left beside its own socket instead, which
// AwaitHandover then removes. It fails once timeout passes or ctx ends first; the successor then locks itself,
// so a successor that never took over does not keep the secrets.
func AwaitHandover(ctx context.Context, l net.Listener, socket string, timeout time.Duration) error {
	locked, ok := l.(*lockedListener)
	if !ok {
		return errors.New("the listener cannot be handed over")
	}
	locked.mu.Lock()
	at, own := socket, locked.own
	if locked.legacy != "" {
		at, own = locked.legacy, locked.legacyMark
	}
	locked.mu.Unlock()
	deadline := time.Now().Add(timeout)
	for {
		if now, err := os.Lstat(at); err == nil && os.SameFile(now, own) {
			locked.mu.Lock()
			locked.leaveLegacy()
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

// beforeUnlock runs in Close once the socket no longer serves, right before the lock is released. It is a
// variable only so a test can hold that moment.
var beforeUnlock = func() {}

// terminate asks the process pid to end, the way a person ends a vault process with 'kill'. It is a variable
// only so a test can see the call without ending itself.
var terminate = func(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
