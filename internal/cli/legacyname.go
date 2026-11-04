package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// calledByLegacyName reports whether program, the name the process was started under (os.Args[0]), is the
// former program name. The base name counts without its Windows extension.
// Transition: the former name keeps working until the next major release.
func calledByLegacyName(program string) bool {
	base := strings.ToLower(filepath.Base(program))
	return strings.TrimSuffix(base, ".exe") == "qatlas"
}

// noteLegacyName tells on stderr that the program was started under its former name. It runs after the
// command, so a failure's diagnostic stays the first line on stderr, and never on stdout, which carries
// the answer and, for 'qconnect mcp', the JSON-RPC stream. Hidden commands are processes the program
// starts itself, such as the vault process; they stay silent.
func noteLegacyName(program string, root *cobra.Command, args []string, stderr io.Writer) {
	if !calledByLegacyName(program) {
		return
	}
	if c, _, err := root.Find(args); err == nil && c.Hidden {
		return
	}
	fmt.Fprintln(stderr, "qconnect: note: this program is now called qconnect; the name qatlas still works until the next major release")
}
