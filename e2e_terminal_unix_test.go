//go:build linux || darwin

package main

import (
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// processAlive reports whether the process pid still runs.
func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// killProcess ends the process pid at once.
func killProcess(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }

// runAtTerminal runs the binary with a pseudo-terminal as its controlling terminal and standard streams,
// types input once the passphrase prompt appeared, and returns everything it wrote there and its exit code.
func runAtTerminal(t *testing.T, c *runner, input string, args ...string) (string, int) {
	t.Helper()
	master, slave := openPTY(t)
	defer master.Close()
	cmd := exec.Command(c.bin, args...)
	cmd.Env = c.env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting %v: %v", args, err)
	}
	_ = slave.Close()

	var mu sync.Mutex
	var out strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := master.Read(buf)
			mu.Lock()
			out.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				// EIO once the last process holding the terminal ended.
				return
			}
		}
	}()
	text := func() string { mu.Lock(); defer mu.Unlock(); return out.String() }

	// The prompt is written before the echo is switched off, so typing waits for both: input typed in
	// between would be echoed back and look like a leaked passphrase.
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(text(), "passphrase: ") || !echoOff(master); {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("no passphrase prompt with echo off appeared: %q", text())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := io.WriteString(master, input); err != nil {
		t.Fatalf("typing at the terminal: %v", err)
	}
	err := cmd.Wait()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("running %v: %v", args, err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	c.seen.WriteString(text())
	return text(), code
}
