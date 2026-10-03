//go:build linux

package vaultproc

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// deletedSuffix is what the kernel appends to /proc/<pid>/exe once the program file was removed or
// replaced, as an update does.
const deletedSuffix = " (deleted)"

// VerifyProgram checks the process at the other end of conn, a Unix socket connection, as the server
// checks every client: it must run under the same user id as this process, and it must run this very
// program, the file /proc/self/exe points to. The kernel reports the process that connected through
// SO_PEERCRED.
//
// The program is compared by the path the kernel reports and by the file itself, so neither another file
// at the same path nor the same file under another name passes. A program whose file was removed or
// replaced since it started is refused: it is a process an update left behind, and nothing says what it
// runs any more.
//
// The check keeps secrets away from other programs of the same user, such as a script an agent runs. It
// cannot stop a process of the same user that deliberately impersonates qatlas, and does not claim to.
func VerifyProgram(conn net.Conn) error {
	cred, err := peerCred(conn)
	if err != nil {
		return err
	}
	return checkProcess(int(cred.Pid), cred.Uid)
}

// VerifyUser checks that the process at the other end of conn runs under the same user id as this one, as
// the client checks the server before it sends its challenge. The program of the vault process cannot be
// checked from outside: Harden closes its /proc entries to this user as well.
func VerifyUser(conn net.Conn) error {
	cred, err := peerCred(conn)
	if err != nil {
		return err
	}
	if cred.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("%w: it runs as another user", ErrRefused)
	}
	return nil
}

// verifyServer is the check a client makes of the process that listens before it sends its challenge:
// VerifyUser, since Harden hides the vault process's program from this user.
func verifyServer(conn net.Conn) error { return VerifyUser(conn) }

// peerPID returns the process id of the peer of conn, or 0 when it cannot be told.
func peerPID(conn net.Conn) int {
	cred, err := peerCred(conn)
	if err != nil {
		return 0
	}
	return int(cred.Pid)
}

// peerCred asks the kernel who is at the other end of conn: the process that connected, seen from the
// server, and the process that listens, seen from the client.
func peerCred(conn net.Conn) (*syscall.Ucred, error) {
	unix, ok := conn.(*net.UnixConn)
	if !ok {
		return nil, fmt.Errorf("%w: not a Unix socket connection", ErrRefused)
	}
	raw, err := unix.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	if credErr != nil {
		return nil, fmt.Errorf("%w: the peer's credentials are unavailable: %w", ErrRefused, credErr)
	}
	return cred, nil
}

// checkProcess compares the process pid, running as uid, with this one. It is VerifyProgram without the
// socket, so a test can reach every refusal with a real process.
func checkProcess(pid int, uid uint32) error {
	if uid != uint32(os.Getuid()) {
		return fmt.Errorf("%w: it runs as another user", ErrRefused)
	}
	// A peer in another PID namespace is reported as pid 0: nothing can be checked about it.
	if pid <= 0 {
		return fmt.Errorf("%w: its process cannot be identified", ErrRefused)
	}
	own, ownInfo, err := programOf("/proc/self/exe")
	if err != nil {
		return fmt.Errorf("%w: this program %w", ErrRefused, err)
	}
	peer, peerInfo, err := programOf(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		return fmt.Errorf("%w: its program %w", ErrRefused, err)
	}
	if peer != own || !os.SameFile(peerInfo, ownInfo) {
		return fmt.Errorf("%w: it runs another program", ErrRefused)
	}
	return nil
}

// programOf reads the program a /proc/<pid>/exe link points to, both as the path the kernel reports and as
// the file.
func programOf(link string) (string, fs.FileInfo, error) {
	path, replaced, err := readProgramLink(link)
	if err != nil {
		return "", nil, err
	}
	if replaced {
		return "", nil, errors.New("was removed or replaced since it started")
	}
	info, err := os.Stat(link)
	if err != nil {
		return "", nil, errors.New("cannot be read")
	}
	return path, info, nil
}

// readProgramLink reads the path a /proc/<pid>/exe link points to, without the suffix the kernel appends
// once the file was removed or replaced, and whether it did. It is the one place that decides what a
// replaced program is.
func readProgramLink(link string) (path string, replaced bool, err error) {
	path, err = os.Readlink(link)
	if err != nil {
		return "", false, errors.New("cannot be read")
	}
	path, replaced = strings.CutSuffix(path, deletedSuffix)
	return path, replaced, nil
}

// ReplacedProgram returns the path this process was started from and whether its file was removed or
// replaced since, as an update does. The path is the one the program had at its start. It reads the same
// /proc/self/exe link VerifyProgram refuses a replaced program by, and tells nothing about the vault.
func ReplacedProgram() (string, bool) {
	path, replaced, err := readProgramLink("/proc/self/exe")
	if err != nil {
		return "", false
	}
	return path, replaced
}

// Harden keeps other processes of the same user from reading this process's memory and the system from
// writing it to a core dump: it clears the dumpable flag, which puts /proc/<pid>/mem and ptrace out of
// their reach. The vault process calls it before it takes any secret.
//
// The flag hides /proc/<pid>/exe from the same user as well, so a client checks the vault process with
// VerifyUser and the challenge instead of VerifyProgram. The process itself still reads its own entries,
// which is all VerifyProgram needs on the server's side.
func Harden() error {
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_SET_DUMPABLE, 0, 0); errno != 0 {
		return fmt.Errorf("cannot protect the vault process's memory: %w", errno)
	}
	return nil
}
