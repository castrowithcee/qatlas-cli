//go:build windows

package vaultproc

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// errProgramGone is what idOf reports for a program whose file is gone from its path.
var errProgramGone = errors.New("was removed or replaced since it started")

// programFile is a program, both as the full path the system reports for a process started from it and
// as the file itself: its volume and file index, which stay the same under another name and differ for
// another file at the same path.
type programFile struct {
	path string
	id   fileID
}

// fileID identifies a file on this machine.
type fileID struct {
	volume, high, low uint32
}

// ownProgram is the program this process runs, taken when the process started. A later check compares with
// it rather than with whatever file is at that path by then, so a process whose program an update replaced
// knows that it runs something else.
var ownProgram, ownProgramErr = selfProgram()

// VerifyProgram checks the process at the other end of conn, a vault pipe connection, as the server checks
// every client and the client checks the server: it must run as this user, and it must run this very
// program. The system reports the peer's process through GetNamedPipeClientProcessId on the server's end
// and GetNamedPipeServerProcessId on the client's; its user is the user of its token, and its program the
// path QueryFullProcessImageName reports.
//
// The paths are compared without regard to case, as the file system does, and the files by their volume
// and file index, so neither another file at the same path nor the same file under another name passes. A
// process whose own program was removed, renamed, or replaced since it started refuses every peer: it is a
// process an update left behind.
//
// The peer's program is judged by the path it was started from, as on macOS: one started from a file
// that has since been replaced at that path is judged by the new file. The process id is the one the pipe
// recorded when it connected; should that process end and its id be reused before the check, the check
// sees the new process. A process that runs elevated while this one does not, or the other way round, may
// not let its token be read and is refused then.
//
// The check keeps secrets away from other programs of the same user, such as a script an agent runs. It
// cannot stop a process of the same user that deliberately impersonates qatlas, and does not claim to.
func VerifyProgram(conn net.Conn) error {
	pid, err := peerProcessID(conn)
	if err != nil {
		return err
	}
	return checkProcess(pid)
}

// VerifyUser checks that the process at the other end of conn runs as this user.
func VerifyUser(conn net.Conn) error {
	pid, err := peerProcessID(conn)
	if err != nil {
		return err
	}
	process, err := openPeer(pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	return checkUser(process)
}

// verifyServer is the check a client makes of the process that listens before it sends its challenge.
// Harden leaves the program of the vault process readable to this user, so the client checks it the same
// way the server checks its clients.
func verifyServer(conn net.Conn) error { return VerifyProgram(conn) }

// peerPID returns the process id of the peer of conn, or 0 when it cannot be told.
func peerPID(conn net.Conn) int {
	pid, err := peerProcessID(conn)
	if err != nil {
		return 0
	}
	return int(pid)
}

// peerProcessID asks the system which process is at the other end of conn: the client, seen from the
// server, and the process that created the instance, seen from the client.
func peerProcessID(conn net.Conn) (uint32, error) {
	pipe, ok := conn.(*pipeConn)
	if !ok {
		return 0, fmt.Errorf("%w: not a named pipe connection", ErrRefused)
	}
	var pid uint32
	var err error
	if pipe.server {
		err = windows.GetNamedPipeClientProcessId(pipe.h, &pid)
	} else {
		err = windows.GetNamedPipeServerProcessId(pipe.h, &pid)
	}
	if err != nil {
		return 0, fmt.Errorf("%w: the peer's process is unavailable: %w", ErrRefused, err)
	}
	return pid, nil
}

// checkProcess compares the process pid with this one. It is VerifyProgram without the pipe, so a test can
// reach every refusal with a real process.
func checkProcess(pid uint32) error {
	process, err := openPeer(pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	if err := checkUser(process); err != nil {
		return err
	}
	if ownProgramErr != nil {
		return fmt.Errorf("%w: this program %w", ErrRefused, ownProgramErr)
	}
	if replaced, err := ownProgramReplaced(); err != nil || replaced {
		return fmt.Errorf("%w: this program was removed or replaced since it started", ErrRefused)
	}
	peer, err := programOf(process)
	if err != nil {
		return fmt.Errorf("%w: its program %w", ErrRefused, err)
	}
	if !samePath(peer.path, ownProgram.path) || peer.id != ownProgram.id {
		return fmt.Errorf("%w: it runs another program", ErrRefused)
	}
	return nil
}

// ownProgramReplaced reports whether the file this process started from was removed or replaced since:
// the path it had at its start no longer names that file. An error means it cannot be told. It is the one
// place that decides what a replaced program is, for VerifyProgram and ReplacedProgram alike.
func ownProgramReplaced() (bool, error) {
	if ownProgramErr != nil {
		return false, ownProgramErr
	}
	now, err := idOf(ownProgram.path)
	if errors.Is(err, errProgramGone) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return now != ownProgram.id, nil
}

// ReplacedProgram returns the path this process was started from and whether its file was removed or
// replaced since, as an update does by renaming it and putting the new program at its path. The path is the
// full one the system reported at the start. It uses the same check VerifyProgram refuses a replaced program
// by, and tells nothing about the vault. Where the state cannot be read, the program counts as not replaced.
func ReplacedProgram() (string, bool) {
	replaced, err := ownProgramReplaced()
	if err != nil || !replaced {
		return "", false
	}
	return ownProgram.path, true
}

// userRuntimeDir finds no per-user runtime directory on Windows, which listens on a named pipe and leaves
// nothing on disk.
func userRuntimeDir() string { return "" }

// openPeer opens the process pid with no more than the right to ask who runs it and from which file.
func openPeer(pid uint32) (windows.Handle, error) {
	if pid == 0 {
		return 0, fmt.Errorf("%w: its process cannot be identified", ErrRefused)
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, fmt.Errorf("%w: its process cannot be read", ErrRefused)
	}
	return process, nil
}

// checkUser compares the user of process with the user of this one.
func checkUser(process windows.Handle) error {
	own, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("%w: this process's user cannot be read", ErrRefused)
	}
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return fmt.Errorf("%w: its user cannot be read", ErrRefused)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return fmt.Errorf("%w: its user cannot be read", ErrRefused)
	}
	if !user.User.Sid.Equals(own) {
		return fmt.Errorf("%w: it runs as another user", ErrRefused)
	}
	return nil
}

// currentUserSID returns the user this process runs as.
func currentUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("cannot determine this process's user: %w", err)
	}
	return user.User.Sid, nil
}

// selfProgram finds the program this process runs, the same way programOf finds a peer's.
func selfProgram() (programFile, error) {
	return programOf(windows.CurrentProcess())
}

// programOf finds the program process runs, by the full path the system reports for it.
func programOf(process windows.Handle) (programFile, error) {
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(process, 0, &buf[0], &size); err != nil {
		return programFile{}, errors.New("cannot be read")
	}
	path := windows.UTF16ToString(buf[:size])
	id, err := idOf(path)
	if errors.Is(err, errProgramGone) {
		return programFile{}, err
	}
	if err != nil {
		return programFile{}, errors.New("cannot be read")
	}
	return programFile{path: path, id: id}, nil
}

// idOf returns the identity of the file at path, which must be a file, not a directory. It opens the file
// for its attributes alone and shares it for everything, deleting and renaming included, so it never stands
// in the way of an update that renames the running program at that very moment.
func idOf(path string) (fileID, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fileID{}, err
	}
	h, err := windows.CreateFile(name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return fileID{}, errProgramGone
	}
	if err != nil {
		return fileID{}, err
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return fileID{}, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return fileID{}, errors.New("is a directory")
	}
	return fileID{volume: info.VolumeSerialNumber, high: info.FileIndexHigh, low: info.FileIndexLow}, nil
}

// samePath compares two program paths the way the file system does, without regard to case, once both are
// in their plain form.
func samePath(a, b string) bool {
	return strings.EqualFold(plainPath(a), plainPath(b))
}

// plainPath drops the \\?\ prefix a long path may carry and cleans the rest.
func plainPath(path string) string {
	if strings.HasPrefix(path, `\\?\`) && !strings.HasPrefix(path, `\\?\UNC\`) {
		path = path[len(`\\?\`):]
	}
	return filepath.Clean(path)
}

// processAccess is all a hardened vault process lets anyone open it for: ask who runs it and from which
// file, wait for it to end, and end it, as 'taskkill' does.
const processAccess = windows.PROCESS_QUERY_LIMITED_INFORMATION | windows.SYNCHRONIZE | windows.PROCESS_TERMINATE

// Harden keeps other processes of the same user from reading or writing this process's memory, from
// injecting a thread, and from duplicating its handles: it replaces the process's DACL with a protected one
// that allows this user, the owner, and the system no more than processAccess, PROCESS_VM_READ never among
// it. The owner's own implicit rights, to read and change the DACL, go with it (OWNER RIGHTS), so a process
// of this user cannot simply grant itself more. The vault process calls it before it takes any secret.
//
// The protection is weaker than on Linux, and the narrowest Windows allows a process of the same user to
// set on itself. A process of this user that runs elevated with the debug privilege, or an administrator,
// still reads the memory. So does anything that takes over one of the process's threads, whose own DACLs
// this does not change, and anything that opened the process before Harden ran: a handle keeps the access
// it was opened with. It keeps the program the process runs readable, so a client checks the vault process
// with VerifyProgram as well as with the challenge.
func Harden() error {
	sid, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("cannot protect the vault process's memory: %w", err)
	}
	sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf("D:P(A;;%#x;;;%s)(A;;%#x;;;OW)(A;;%#x;;;SY)",
		processAccess, sid.String(), processAccess, processAccess))
	if err != nil {
		return fmt.Errorf("cannot protect the vault process's memory: %w", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("cannot protect the vault process's memory: %w", err)
	}
	if err := windows.SetSecurityInfo(windows.CurrentProcess(), windows.SE_KERNEL_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("cannot protect the vault process's memory: %w", err)
	}
	return nil
}
