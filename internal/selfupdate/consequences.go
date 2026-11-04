package selfupdate

import (
	"fmt"
	"runtime"
	"strings"
)

// Question is the question before an installation, the same for the command line and the editor: the
// version to install and what that does to running qconnect processes.
func Question(latest string) string {
	return "Update qconnect to " + latest + "? " + Consequences()
}

// TargetQuestion is Question for a release chosen by version: it says whether the version is a prerelease
// and whether it is newer or older than the running one. An older version lacks the fixes made since, and
// it cannot take over an unlocked vault, so the question says the vault process is locked first.
func TargetQuestion(version string, prerelease, older bool) string {
	kind := "version"
	if prerelease {
		kind = "prerelease"
	}
	if older {
		return "Install qconnect " + kind + " " + version + " (older than the running version: its fixes of later " +
			"versions are missing)? " + consequences(runtime.GOOS, OtherProcesses(), true)
	}
	return "Install qconnect " + kind + " " + version + " (newer than the running version)? " + Consequences()
}

// Consequences is ConsequencesText for this platform and the processes running now.
func Consequences() string { return ConsequencesText(runtime.GOOS, OtherProcesses()) }

// ConsequencesText is the part of the update question that names what an installation does to running
// qconnect processes, the same words for the command line and the editor. others is the number of other
// qconnect processes of this user, or negative where it is not known; goos selects the platform's next step.
func ConsequencesText(goos string, others int) string { return consequences(goos, others, false) }

// consequences is ConsequencesText; older selects the words for an installation of an older version, which
// never hands the vault over.
func consequences(goos string, others int, older bool) string {
	var b strings.Builder
	if goos == "windows" {
		b.WriteString("The running qatlas.exe is renamed to qatlas.exe.old and replaced; the old file is removed " +
			"at the next start or update.")
		if others > 0 {
			fmt.Fprintf(&b, " %d other qconnect %s of yours %s running.", others, plural(others, "process", "processes"),
				plural(others, "is", "are"))
		}
		b.WriteString(" A running vault process is locked first, because Windows has no handover; " +
			"'qconnect vault unlock' unlocks it again.")
		b.WriteString(" Running 'qconnect mcp', 'qconnect tui' and 'qconnect web' keep running the old version until " +
			"they are restarted or, for 'qconnect mcp', reconnected in their client.")
		return b.String()
	}
	if older {
		b.WriteString("A running vault process is locked first, whatever the update behaviour is, because an " +
			"older version cannot take over an unlocked vault; 'qconnect vault unlock' unlocks it again.")
	} else {
		b.WriteString("A running vault process is handed over to the new version if the " +
			"update behaviour is handover, and locked first otherwise.")
	}
	if others > 0 {
		fmt.Fprintf(&b, " %d other qconnect %s of yours %s running.", others, plural(others, "process", "processes"),
			plural(others, "is", "are"))
	}
	if goos == "linux" || goos == "darwin" {
		b.WriteString(" Running 'qconnect mcp' servers restart themselves; only after a lock do they need " +
			"'qconnect vault unlock'.")
	} else {
		b.WriteString(" Reconnect running 'qconnect mcp' servers in their client.")
	}
	b.WriteString(" Restart running 'qconnect tui' and 'qconnect web'.")
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
