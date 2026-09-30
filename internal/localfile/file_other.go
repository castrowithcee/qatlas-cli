//go:build !unix && !windows

package localfile

import (
	"io/fs"
	"os"
)

// openFlags adds nothing where neither Unix nor Windows rules apply.
const openFlags = 0

// singleLink cannot tell the link count here, so it refuses every file.
func singleLink(*os.File, fs.FileInfo) bool { return false }

// validName refuses every path here: without Unix or Windows file semantics, os.Root cannot be relied on to
// keep an operation inside the released directory.
func validName(string) bool { return false }
