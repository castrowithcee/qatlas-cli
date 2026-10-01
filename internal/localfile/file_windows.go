//go:build windows

package localfile

import (
	"io/fs"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// openFlags adds nothing on Windows: its file system knows no FIFO a name could be swapped for.
const openFlags = 0

// singleLink reports whether the opened file has exactly one hard link, read from its handle. A file whose
// link count cannot be read counts as having several.
func singleLink(file *os.File, _ fs.FileInfo) bool {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return false
	}
	return info.NumberOfLinks == 1
}

// validName reports whether a path below a released directory is one this package accepts. A colon, which
// on Windows names an alternate data stream of a file, is refused.
func validName(rel string) bool { return !strings.Contains(rel, ":") }
