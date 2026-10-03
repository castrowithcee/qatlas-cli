//go:build windows

package filelock

import (
	"os"

	"golang.org/x/sys/windows"
)

// Lock takes an exclusive, blocking lock on the whole of f, released by Unlock or when
// the process exits. Locking a byte range that reaches past any real file size is the documented way to
// lock a whole file with LockFileEx.
func Lock(f *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0,
		^uint32(0), ^uint32(0), &overlapped)
}

func Unlock(f *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, ^uint32(0), ^uint32(0), &overlapped)
}
