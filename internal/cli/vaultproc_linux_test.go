//go:build linux

package cli

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// processArguments returns the command line of the process pid.
func processArguments(t *testing.T, pid int) string {
	t.Helper()
	cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		t.Fatalf("cannot read the command line of process %d: %v", pid, err)
	}
	return string(cmdline)
}

// sessionWarnings reads what systemd-logind reads: KillUserProcesses= with its exceptions, and whether the
// user lingers when the socket sits in the runtime directory.
func TestSessionWarnings(t *testing.T) {
	me, err := user.Current()
	if err != nil {
		t.Skipf("no current user: %v", err)
	}
	root := t.TempDir()
	original := systemdRoot
	systemdRoot = root
	t.Cleanup(func() { systemdRoot = original })
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	socket := filepath.Join(runtimeDir, "qatlas", "vault-0.sock")

	if got := sessionWarnings(socket); len(got) != 0 {
		t.Fatalf("sessionWarnings() without systemd = %v, want none", got)
	}
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "run", "systemd", "system"), 0o755); err != nil {
		t.Fatal(err)
	}

	write("etc/systemd/logind.conf", "[Login]\n#KillUserProcesses=yes\n")
	got := sessionWarnings(socket)
	if len(got) != 1 || !strings.Contains(got[0], "loginctl enable-linger") {
		t.Fatalf("sessionWarnings() without linger = %v, want the linger warning", got)
	}
	if got := sessionWarnings(filepath.Join(t.TempDir(), "vault-0.sock")); len(got) != 0 {
		t.Fatalf("sessionWarnings() for a socket outside the runtime directory = %v, want none", got)
	}

	write("var/lib/systemd/linger/"+me.Username, "")
	if got := sessionWarnings(socket); len(got) != 0 {
		t.Fatalf("sessionWarnings() with linger = %v, want none", got)
	}

	write("usr/lib/systemd/logind.conf.d/10-kill.conf", "[Login]\nKillUserProcesses=yes\n")
	got = sessionWarnings(socket)
	if len(got) != 1 || !strings.Contains(got[0], "KillUserProcesses=yes") {
		t.Fatalf("sessionWarnings() with KillUserProcesses=yes = %v, want the kill warning", got)
	}
	write("etc/systemd/logind.conf.d/20-exclude.conf", "[Login]\nKillExcludeUsers=root "+me.Username+"\n")
	if got := sessionWarnings(socket); len(got) != 0 {
		t.Fatalf("sessionWarnings() for an excluded user = %v, want none", got)
	}
	write("etc/systemd/logind.conf.d/20-exclude.conf", "[Other]\nKillExcludeUsers="+me.Username+"\n")
	write("etc/systemd/logind.conf.d/10-kill.conf", "[Login]\nKillUserProcesses=no\n")
	if got := sessionWarnings(socket); len(got) != 0 {
		t.Fatalf("sessionWarnings() with a drop-in in /etc switching it off = %v, want none", got)
	}
}
