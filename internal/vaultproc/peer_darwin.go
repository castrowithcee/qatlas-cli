//go:build darwin

package vaultproc

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// programFile is a program, both as its absolute path with every symbolic link resolved and as the file.
type programFile struct {
	path string
	info fs.FileInfo
}

// ownProgram is the program this process runs, taken when the process started. A later check compares with
// it rather than with whatever file is at that path by then, so a process whose program an update replaced
// knows that it runs something else.
var ownProgram, ownProgramErr = selfProgram()

// VerifyProgram checks the process at the other end of conn, a Unix socket connection, as the server
// checks every client: it must run under the same user id as this process, and it must run this very
// program. The kernel reports the peer's user through LOCAL_PEERCRED and its process through
// LOCAL_PEERPID; the program is the path the kernel kept from that process's exec (kern.procargs2).
//
// The path must be absolute; a process started by a relative path is refused, since which file that path
// meant depends on a working directory that cannot be read without cgo. The path is resolved to its real
// file and compared by that resolved path and by the file itself, so neither another file at the same path
// nor the same file under another name passes. A process whose own program was removed or replaced since
// it started refuses every peer: it is a process an update left behind.
//
// Unlike on Linux, the kernel does not tell which file a peer actually runs, only the path it was started
// by: a peer started from a file that has since been replaced at that path is judged by the new file. The
// process id is the one of the last process that used the peer's socket; should that process end and its
// id be reused before the check, the check sees the new process.
//
// The check keeps secrets away from other programs of the same user, such as a script an agent runs. It
// cannot stop a process of the same user that deliberately impersonates qatlas, and does not claim to.
func VerifyProgram(conn net.Conn) error {
	uid, pid, err := peerCred(conn)
	if err != nil {
		return err
	}
	return checkProcess(pid, uid)
}

// VerifyUser checks that the process at the other end of conn runs under the same user id as this one.
func VerifyUser(conn net.Conn) error {
	uid, _, err := peerCred(conn)
	if err != nil {
		return err
	}
	if uid != uint32(os.Getuid()) {
		return fmt.Errorf("%w: it runs as another user", ErrRefused)
	}
	return nil
}

// verifyServer is the check a client makes of the process that listens before it sends its challenge.
// Harden does not hide the vault process's program here, so the client checks it the same way the server
// checks its clients.
func verifyServer(conn net.Conn) error { return VerifyProgram(conn) }

// peerPID returns the process id of the peer of conn, or 0 when it cannot be told.
func peerPID(conn net.Conn) int {
	_, pid, err := peerCred(conn)
	if err != nil {
		return 0
	}
	return pid
}

// peerCred asks the kernel who is at the other end of conn: its user id, and the id of its process.
func peerCred(conn net.Conn) (uint32, int, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, 0, fmt.Errorf("%w: not a Unix socket connection", ErrRefused)
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return 0, 0, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	var cred *unix.Xucred
	var pid int
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if credErr == nil {
			pid, credErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		}
	}); err != nil {
		return 0, 0, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	if credErr != nil {
		return 0, 0, fmt.Errorf("%w: the peer's credentials are unavailable: %w", ErrRefused, credErr)
	}
	// XUCRED_VERSION is 0; a structure of another version is not read.
	if cred.Version != 0 {
		return 0, 0, fmt.Errorf("%w: the peer's credentials are unavailable", ErrRefused)
	}
	return cred.Uid, pid, nil
}

// checkProcess compares the process pid, running as uid, with this one. It is VerifyProgram without the
// socket, so a test can reach every refusal with a real process.
func checkProcess(pid int, uid uint32) error {
	if uid != uint32(os.Getuid()) {
		return fmt.Errorf("%w: it runs as another user", ErrRefused)
	}
	if pid <= 0 {
		return fmt.Errorf("%w: its process cannot be identified", ErrRefused)
	}
	if ownProgramErr != nil {
		return fmt.Errorf("%w: this program %w", ErrRefused, ownProgramErr)
	}
	if _, now, err := resolveProgram(ownProgram.path); err != nil || !os.SameFile(now, ownProgram.info) {
		return fmt.Errorf("%w: this program was removed or replaced since it started", ErrRefused)
	}
	peer, err := programOf(pid)
	if err != nil {
		return fmt.Errorf("%w: its program %w", ErrRefused, err)
	}
	if peer.path != ownProgram.path || !os.SameFile(peer.info, ownProgram.info) {
		return fmt.Errorf("%w: it runs another program", ErrRefused)
	}
	return nil
}

// selfProgram finds the program this process runs. os.Executable makes a relative start path absolute
// with the working directory this process started in, which is known for this process alone.
func selfProgram() (programFile, error) {
	path, err := os.Executable()
	if err != nil {
		return programFile{}, errors.New("cannot be read")
	}
	resolved, info, err := resolveProgram(path)
	if err != nil {
		return programFile{}, err
	}
	return programFile{path: resolved, info: info}, nil
}

// programOf finds the program the process pid was started from, by the path the kernel kept from its exec.
func programOf(pid int) (programFile, error) {
	args, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return programFile{}, errors.New("cannot be read")
	}
	path, err := execPath(args)
	if err != nil {
		return programFile{}, err
	}
	resolved, info, err := resolveProgram(path)
	if err != nil {
		return programFile{}, err
	}
	return programFile{path: resolved, info: info}, nil
}

// execPath reads the exec path from what kern.procargs2 returns: the argument count as a 32-bit integer,
// then the path the process was started by, terminated by a zero byte, then its arguments and environment.
// Only an absolute path names one file for certain.
func execPath(args []byte) (string, error) {
	if len(args) < 4 {
		return "", errors.New("cannot be read")
	}
	end := bytes.IndexByte(args[4:], 0)
	if end <= 0 {
		return "", errors.New("cannot be read")
	}
	path := string(args[4 : 4+end])
	if !filepath.IsAbs(path) {
		return "", errors.New("was started by a relative path, which does not name one file for certain")
	}
	return path, nil
}

// resolveProgram resolves path to the real file it names, as a path without symbolic links and as the file.
func resolveProgram(path string) (string, fs.FileInfo, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil, errors.New("was removed or replaced since it started")
	}
	if err != nil {
		return "", nil, errors.New("cannot be read")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", nil, errors.New("cannot be read")
	}
	return resolved, info, nil
}

// Harden keeps other processes from attaching to this one with a debugger, and the system from writing its
// memory to a core dump: it denies ptrace attachment (PT_DENY_ATTACH) and sets the core file size limit to
// zero. The vault process calls it before it takes any secret.
//
// Unlike on Linux, this does not hide the program the process runs from the same user, so a client checks
// the vault process with VerifyProgram as well as with the challenge.
func Harden() error {
	if err := unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{}); err != nil {
		return fmt.Errorf("cannot keep the vault process out of core dumps: %w", err)
	}
	if err := unix.PtraceDenyAttach(); err != nil {
		return fmt.Errorf("cannot protect the vault process's memory: %w", err)
	}
	return nil
}
