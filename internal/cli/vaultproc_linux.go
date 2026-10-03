//go:build linux

package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
)

// systemdRoot is the root the systemd files sessionWarnings reads are found below. It is a variable only so
// a test can point it at a directory of its own.
var systemdRoot = "/"

// sessionWarnings says what would end the vault process, or cut it off from its socket, when the session
// that started it ends. It reads the files systemd-logind reads and asks nothing; a system without systemd
// has no such warning.
func sessionWarnings(socket string) []string {
	if _, err := os.Stat(filepath.Join(systemdRoot, "run", "systemd", "system")); err != nil {
		return nil
	}
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	var warnings []string
	if killsUserProcesses(name) {
		warnings = append(warnings, "systemd-logind ends every process of a session when the session closes "+
			"(KillUserProcesses=yes), the vault process included, so the vault locks when you log out; it keeps "+
			"running only once KillUserProcesses=no is set, or your user is listed in KillExcludeUsers=, in "+
			"/etc/systemd/logind.conf")
	}
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	inRuntime := filepath.IsAbs(runtime) && strings.HasPrefix(socket, filepath.Clean(runtime)+string(filepath.Separator))
	if name != "" && inRuntime {
		if _, err := os.Stat(filepath.Join(systemdRoot, "var", "lib", "systemd", "linger", name)); err != nil {
			warnings = append(warnings, fmt.Sprintf("linger is off for %s, so the system removes %s when your "+
				"last session ends, and with it the vault socket, which locks the vault; run 'loginctl "+
				"enable-linger' to keep the vault unlocked after you log out", name, runtime))
		}
	}
	return warnings
}

// killsUserProcesses reports whether systemd-logind ends the processes of name's sessions when they close:
// KillUserProcesses=yes, unless KillExcludeUsers= names the user or KillOnlyUsers= names others only. The
// files are read in the order logind reads them, and a later setting wins.
func killsUserProcesses(name string) bool {
	settings := map[string]string{}
	for _, path := range logindFiles() {
		readLogindFile(path, settings)
	}
	kill, _ := parseSystemdBool(settings["KillUserProcesses"])
	if !kill {
		return false
	}
	if name == "" {
		return true
	}
	if contains(strings.Fields(settings["KillExcludeUsers"]), name) {
		return false
	}
	if only := strings.Fields(settings["KillOnlyUsers"]); len(only) > 0 && !contains(only, name) {
		return false
	}
	return true
}

// logindFiles lists logind.conf and its drop-ins in the order systemd reads them: the main file, then the
// drop-ins sorted by name, where a drop-in in a later directory replaces one of the same name.
func logindFiles() []string {
	var files []string
	for _, main := range []string{"usr/lib/systemd/logind.conf", "etc/systemd/logind.conf"} {
		files = append(files, filepath.Join(systemdRoot, main))
	}
	dropIns := map[string]string{}
	for _, dir := range []string{"usr/lib/systemd/logind.conf.d", "usr/local/lib/systemd/logind.conf.d",
		"run/systemd/logind.conf.d", "etc/systemd/logind.conf.d"} {
		entries, err := os.ReadDir(filepath.Join(systemdRoot, dir))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".conf") {
				dropIns[entry.Name()] = filepath.Join(systemdRoot, dir, entry.Name())
			}
		}
	}
	names := make([]string, 0, len(dropIns))
	for name := range dropIns {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		files = append(files, dropIns[name])
	}
	return files
}

// readLogindFile adds the [Login] settings of one file to settings. A file that cannot be read adds none.
func readLogindFile(path string, settings map[string]string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	section := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case line == "", strings.HasPrefix(line, "#"), strings.HasPrefix(line, ";"):
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			section = line
		case section == "[Login]":
			if key, value, ok := strings.Cut(line, "="); ok {
				settings[strings.TrimSpace(key)] = strings.TrimSpace(value)
			}
		}
	}
}

func parseSystemdBool(value string) (bool, bool) {
	switch strings.ToLower(value) {
	case "1", "yes", "y", "true", "t", "on":
		return true, true
	case "0", "no", "n", "false", "f", "off":
		return false, true
	}
	return false, false
}
