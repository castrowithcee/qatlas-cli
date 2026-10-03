//go:build !windows

package filelock

import (
	"os"

	"golang.org/x/sys/unix"
)

// Lock takes an exclusive, blocking advisory lock on f, released by Unlock or when the
// process exits. It is scoped to this file descriptor, so several goroutines of the same process still
// serialize on the OS-level lock like separate processes would.
func Lock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX)
}

func Unlock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
