//go:build windows

package cli

import (
	"context"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// stopSignals are the signals that lock a vault process, as a lock request would. A vault process has no
// console, so Windows sends it none of them in practice; it ends with the system at logout or restart.
var stopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

// serveStreams returns the handover and the report a vault process inherits from StartProcess, its
// standard input and output, or false when either is not a pipe: 'qatlas vault serve' run by hand at a
// console.
func serveStreams() (handover, report *os.File, ok bool) {
	if !isPipe(os.Stdin) || !isPipe(os.Stdout) {
		return nil, nil, false
	}
	return os.Stdin, os.Stdout, true
}

// isPipe reports whether f is open and a pipe.
func isPipe(f *os.File) bool {
	if f == nil {
		return false
	}
	kind, err := windows.GetFileType(windows.Handle(f.Fd()))
	return err == nil && kind == windows.FILE_TYPE_PIPE
}

// watchSocket has nothing to watch on Windows: nobody can remove the vault pipe while this process holds
// it, and the system removes it with the process at logout or restart.
func watchSocket(context.Context, string, func()) {}

var procIsProcessInJob = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")

// sessionWarnings says when the vault process pid will end with the session that started it: where it
// could not leave the job object this session runs in, as an SSH server's job may not let it, it ends
// whenever that job is ended.
func sessionWarnings(_ string, pid int) []string {
	if pid <= 0 {
		return nil
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(process)
	var inJob int32
	if r, _, _ := procIsProcessInJob.Call(uintptr(process), 0, uintptr(unsafe.Pointer(&inJob))); r == 0 || inJob == 0 {
		return nil
	}
	return []string{"the vault process could not leave the job object this session runs in, so it ends, and the " +
		"vault locks, when that job is ended, as an SSH server does when the connection closes; unlock the vault " +
		"from a session that lets processes leave its job, such as a local terminal, to keep it unlocked after that"}
}
