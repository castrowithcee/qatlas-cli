package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-connect/internal/selfupdate"
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

// adoptProgramName gives an installation that an updater of a release before the rename left as
// <prefix>/bin/qatlas its current name, with qatlas as an alias (see selfupdate.AdoptCurrentName), and says so
// on stderr in the one process that did it. Hidden commands, which the program starts itself, and
// self-contained ones leave the installation alone. Transition until the next major release.
func adoptProgramName(c *cobra.Command) {
	if c.Hidden || selfContained(c) {
		return
	}
	if path, ok := selfupdate.AdoptCurrentName(); ok {
		fmt.Fprintf(c.ErrOrStderr(), "qconnect: this program is now installed as %s; qatlas stays an alias of it "+
			"until the next major release\n", path)
	}
}
