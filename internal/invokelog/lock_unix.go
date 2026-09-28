//go:build !windows

package invokelog

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockExclusive takes an exclusive, blocking advisory lock on f, released by unlockExclusive or when the
// process exits. It is scoped to this file descriptor, so several goroutines of the same process still
// serialize on the OS-level lock like separate processes would.
func lockExclusive(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX)
}

func unlockExclusive(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
