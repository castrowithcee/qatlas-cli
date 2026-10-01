//go:build unix

package localfile

import (
	"io/fs"
	"os"
	"syscall"
)

// openFlags opens a file for reading without blocking on a FIFO and without making a terminal the
// controlling one, in case the name was swapped for either between the check and the open.
const openFlags = syscall.O_NONBLOCK | syscall.O_NOCTTY

// singleLink reports whether the opened file has exactly one hard link. A file whose link count cannot be
// read counts as having several.
func singleLink(_ *os.File, info fs.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint64(stat.Nlink) == 1
}

// validName reports whether a path below a released directory is one this package accepts. On Unix every
// local path is.
func validName(string) bool { return true }
