//go:build linux || darwin

package vaultmigrate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// successorScript writes a shell script that stands in for the program an update installed: it records its
// arguments, its process id, and what it reads on HandoverFD into dir, then runs body.
func successorScript(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "qatlas")
	script := "#!/bin/sh\n" +
		"echo \"$@\" > " + filepath.Join(dir, "args") + "\n" +
		"echo $$ > " + filepath.Join(dir, "pid") + "\n" +
		"cat <&3 > " + filepath.Join(dir, "snapshot") + "\n" +
		body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func scriptPID(t *testing.T, dir string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "pid"))
	if err != nil {
		t.Fatalf("the script did not run: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func awaitGone(t *testing.T, pid int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); syscall.Kill(pid, 0) == nil; {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the successor %d still runs", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The successor contract: what a vault process of one release starts the vault process of the next with.
// A release must keep understanding what its predecessor sends, so none of this changes without keeping the
// old form working, and this test changes with it only by adding to it.
func TestSuccessorContract(t *testing.T) {
	if HandoverFD != 3 || ReportFD != 4 || ReportReady != "ready" || ReportRunning != "running" ||
		SuccessorFlag != "--successor" {
		t.Fatalf("the descriptors, report words, or successor flag changed: %d %d %q %q %q", HandoverFD, ReportFD,
			ReportReady, ReportRunning, SuccessorFlag)
	}
	if got := strings.Join(serveArgs("/home/a/.qatlas/cli/config.yaml", true), " "); got !=
		"vault serve --config /home/a/.qatlas/cli/config.yaml --successor" {
		t.Fatalf("the successor's arguments = %q", got)
	}

	locksAt := time.Date(2026, 10, 5, 12, 30, 0, 0, time.UTC)
	snap := vault.Snapshot{
		Identity: "placeholder-identity",
		Secrets:  map[string]map[string]string{"wiki-reader": {"user": "synthetic-value"}},
		Bindings: vault.Bindings{IDs: map[string]string{"wiki-reader": "0123"}, Approvals: map[string]string{"wiki": "abcd"}},
		LocksAt:  &locksAt,
	}
	const want = `{"identity":"placeholder-identity","secrets":{"wiki-reader":{"user":"synthetic-value"}},` +
		`"bindings":{"ids":{"wiki-reader":"0123"},"approvals":{"wiki":"abcd"}},"locks_at":"2026-10-05T12:30:00Z"}`
	data, err := json.Marshal(snap)
	if err != nil || string(data) != want {
		t.Fatalf("the snapshot's JSON = %s, %v, want %s", data, err, want)
	}
	var read vault.Snapshot
	if err := json.Unmarshal([]byte(want), &read); err != nil || read.LocksAt == nil || !read.LocksAt.Equal(locksAt) ||
		read.Secrets["wiki-reader"]["user"] != "synthetic-value" || read.Bindings.Approvals["wiki"] != "abcd" {
		t.Fatalf("the snapshot read back = %+v, %v", read, err)
	}

	// A successor started for real gets exactly these arguments and this JSON, and is taken as started on
	// the word ready alone.
	dir := t.TempDir()
	program := successorScript(t, dir, "echo ready >&4\nexec sleep 60")
	pid, end, err := StartSuccessor(context.Background(), program, "/home/a/.qatlas/cli/config.yaml", snap)
	if err != nil {
		t.Fatalf("StartSuccessor() = %v", err)
	}
	t.Cleanup(end)
	if pid != scriptPID(t, dir) {
		t.Fatalf("StartSuccessor() = pid %d, want the script's", pid)
	}
	if args, _ := os.ReadFile(filepath.Join(dir, "args")); strings.TrimSpace(string(args)) !=
		"vault serve --config /home/a/.qatlas/cli/config.yaml --successor" {
		t.Fatalf("the successor was started with %q", args)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "snapshot")); string(got) != want {
		t.Fatalf("the successor read %s, want %s", got, want)
	}
	end()
	awaitGone(t, pid)
}

// A successor that reports anything but ready, or nothing in time, is ended before StartSuccessor returns.
func TestStartSuccessorEndsASuccessorThatDoesNotStart(t *testing.T) {
	for name, tc := range map[string]struct {
		body    string
		timeout time.Duration
		want    string
	}{
		"reports a failure": {"echo cannot listen >&4\nexec sleep 60", StartTimeout, "cannot listen"},
		"reports running":   {"echo running >&4\nexec sleep 60", StartTimeout, "did not start"},
		"reports nothing":   {"exec sleep 60", 300 * time.Millisecond, "did not report"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			program := successorScript(t, dir, tc.body)
			ctx, cancel := context.WithTimeout(context.Background(), tc.timeout)
			defer cancel()
			locksAt := time.Now().Add(time.Hour)
			_, end, err := StartSuccessor(ctx, program, "/nowhere/config.yaml", vault.Snapshot{Identity: "x", LocksAt: &locksAt})
			if err == nil || end != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("StartSuccessor() = %v, want an error containing %q and no successor", err, tc.want)
			}
			awaitGone(t, scriptPID(t, dir))
		})
	}

	if _, _, err := StartSuccessor(context.Background(), filepath.Join(t.TempDir(), "missing"), "/nowhere/config.yaml",
		vault.Snapshot{}); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StartSuccessor() of a missing program = %v, want a start error", err)
	}
}
