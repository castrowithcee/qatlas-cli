package selfupdate

import (
	"fmt"
	"runtime"
	"strings"
)

// Question is the question before an installation, the same for the command line and the editor: the
// version to install and what that does to running qatlas processes.
func Question(latest string) string {
	return "Update qatlas to " + latest + "? " + Consequences()
}

// Consequences is ConsequencesText for this platform and the processes running now.
func Consequences() string { return ConsequencesText(runtime.GOOS, OtherProcesses()) }

// ConsequencesText is the part of the update question that names what an installation does to running
// qatlas processes, the same words for the command line and the editor. others is the number of other
// qatlas processes of this user, or negative where it is not known; goos selects the platform's next step.
func ConsequencesText(goos string, others int) string {
	var b strings.Builder
	b.WriteString("A running vault process is locked first.")
	if others > 0 {
		fmt.Fprintf(&b, " %d other qatlas %s of yours %s running.", others, plural(others, "process", "processes"),
			plural(others, "is", "are"))
	}
	if goos == "linux" {
		b.WriteString(" Running 'qatlas mcp' servers restart themselves and only need 'qatlas vault unlock'.")
	} else {
		b.WriteString(" Reconnect running 'qatlas mcp' servers in their client.")
	}
	b.WriteString(" Restart running 'qatlas tui' and 'qatlas web'.")
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
