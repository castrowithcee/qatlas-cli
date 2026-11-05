//go:build linux

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/secret"
	"github.com/castrowithcee/qatlas-connect/internal/vault"
)

// TestEarlierVaultProcessIsEnded installs this release beside a vault process the earlier release left
// unlocked, without a handover: a program built with the tag legacy_vault_names stands in for that release
// and unlocks the vault first. This release's status names the process, with its pid and how to end it; its
// lock ends it, after which nothing is reported; and its unlock ends such a process before it starts its
// own. It runs once with the configuration named in QCONNECT_CONFIG, and once at the earlier release's
// default location, which this release migrates from.
func TestEarlierVaultProcessIsEnded(t *testing.T) {
	if testing.Short() {
		t.Skip("the acceptance run builds the binary twice")
	}
	dir := t.TempDir()
	current := buildBinary(t, dir)
	earlier := filepath.Join(dir, "earlier", binaryName())
	build := exec.Command("go", "build", "-tags", "e2e,legacy_vault_names", "-buildvcs=false", "-o", earlier, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("building the earlier binary: %v", err)
	}
	for name, atDefault := range map[string]bool{"explicit configuration": false, "default location": true} {
		t.Run(name, func(t *testing.T) { earlierVaultProcessCase(t, current, earlier, atDefault) })
	}
}

func earlierVaultProcessCase(t *testing.T, current, earlier string, atDefault bool) {
	const (
		storedSecret = "canary-earlier-secret-4be1d9"
		passphrase   = "canary-passphrase-earlier-0c7a35"
	)
	home := t.TempDir()
	configDir := home
	if atDefault {
		configDir = filepath.Join(home, ".qatlas", "cli")
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("version: 4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	offer := func(string) (string, error) { return passphrase, nil }
	if err := vault.New(configDir).Set("vault-reader", "token", storedSecret, offer); err != nil {
		t.Fatalf("seeding the vault: %v", err)
	}

	runtimeDir := shortRuntimeDir(t)
	var seen strings.Builder
	env := []string{
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
		"XDG_RUNTIME_DIR=" + runtimeDir,
		secret.StoreSelector + "=none",
	}
	if !atDefault {
		env = append(env, "QCONNECT_CONFIG="+configPath)
	}
	now := &runner{bin: current, seen: &seen, env: env}
	before := &runner{bin: earlier, seen: &seen, env: env}
	// Whatever the test did, both programs lock what they reach; removing the runtime directory afterwards
	// makes any vault process left lock itself too.
	t.Cleanup(func() {
		_, _, _ = before.run(t, "vault", "lock")
		_, _, _ = now.run(t, "vault", "lock")
	})

	unlockEarlier := func() string {
		t.Helper()
		out, code := runAtTerminal(t, before, passphrase+"\n", "vault", "unlock")
		match := regexp.MustCompile(`unlocked in a vault process \(pid (\d+)\)`).FindStringSubmatch(out)
		if code != 0 || match == nil {
			t.Fatalf("unlock of the earlier program: exit %d, output %q", code, out)
		}
		return match[1]
	}
	type statusDoc struct {
		Process string `json:"process"`
		PID     int64  `json:"pid"`
		Warning string `json:"warning"`
	}
	status := func(c *runner) statusDoc {
		t.Helper()
		code, stdout, stderr := c.run(t, "vault", "status", "--output", "json")
		var doc statusDoc
		if code != 0 || json.Unmarshal([]byte(stdout), &doc) != nil {
			t.Fatalf("vault status: exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
		return doc
	}
	const earlierNote = "release before qatlas-connect"

	pid := unlockEarlier()
	got := status(now)
	if got.Process != "none" || !strings.Contains(got.Warning, "(pid "+pid+")") ||
		!strings.Contains(got.Warning, earlierNote) || !strings.Contains(got.Warning, "'qconnect vault lock'") {
		t.Fatalf("status = %+v, want no process of this release and a warning naming process %s and the lock", got, pid)
	}

	code, stdout, stderr := now.run(t, "vault", "lock")
	if code != 0 || !strings.Contains(stdout, "ended the vault process of the "+earlierNote+" (pid "+pid+")") {
		t.Fatalf("lock: exit %d, stdout %q, stderr %q, want process %s ended", code, stdout, stderr, pid)
	}
	if got := status(now); strings.Contains(got.Warning, earlierNote) {
		t.Fatalf("status after the lock = %+v, want no earlier process", got)
	}
	if got := status(before); got.Process != "none" {
		t.Fatalf("status of the earlier program after the lock = %+v, want its process gone", got)
	}

	pid = unlockEarlier()
	out, code := runAtTerminal(t, now, passphrase+"\n", "vault", "unlock")
	ended := strings.Index(out, "ended the vault process of the "+earlierNote+" (pid "+pid+")")
	match := regexp.MustCompile(`unlocked in a vault process \(pid (\d+)\)`).FindStringSubmatchIndex(out)
	if code != 0 || ended < 0 || match == nil || match[0] < ended {
		t.Fatalf("unlock: exit %d, output %q, want process %s ended before a process of this release starts", code,
			out, pid)
	}
	started, _ := strconv.Atoi(out[match[2]:match[3]])
	if got := status(now); got.Process != "running" || int(got.PID) != started || strings.Contains(got.Warning, earlierNote) {
		t.Fatalf("status after the unlock = %+v, want process %d of this release alone", got, started)
	}
	if got := status(before); got.Process != "none" {
		t.Fatalf("status of the earlier program after the unlock = %+v, want its process gone", got)
	}
	if strings.Contains(seen.String(), storedSecret) || strings.Contains(seen.String(), passphrase) {
		t.Error("a secret reached the output")
	}
}
