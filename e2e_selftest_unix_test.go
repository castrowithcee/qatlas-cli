//go:build linux || darwin

package main

import (
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// An interrupt or termination ends the run with a non-zero code and removes the scratch directory, also
// while writer processes run.
func TestSelftestRemovesScratchDirectoryOnSignal(t *testing.T) {
	if testing.Short() {
		t.Skip("the selftest builds the binary")
	}
	bin := buildBinary(t, t.TempDir())
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			_, env := selftestInstallation(t)
			base := t.TempDir()
			// The standard profile runs for minutes, so the signal always arrives mid-run.
			cmd := exec.Command(bin, "selftest", "storage", "--dir", base)
			cmd.Env = env
			var stderr strings.Builder
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			deadline := time.Now().Add(30 * time.Second)
			for len(scratchDirs(t, base)) == 0 {
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					t.Fatal("the scratch directory never appeared")
				}
				time.Sleep(20 * time.Millisecond)
			}
			time.Sleep(300 * time.Millisecond)
			_ = cmd.Process.Signal(sig)
			select {
			case err := <-done:
				exit, ok := err.(*exec.ExitError)
				if !ok || exit.ExitCode() == 0 {
					t.Errorf("err = %v, want a non-zero exit", err)
				}
			case <-time.After(30 * time.Second):
				_ = cmd.Process.Kill()
				t.Fatal("the run did not end after the signal")
			}
			if left := scratchDirs(t, base); len(left) != 0 {
				t.Errorf("scratch directories left behind: %v", left)
			}
			if !strings.Contains(stderr.String(), "interrupted") {
				t.Errorf("stderr = %q", stderr.String())
			}
		})
	}
}
