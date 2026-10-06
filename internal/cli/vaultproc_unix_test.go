//go:build linux || darwin

package cli

import (
	"os"
	"syscall"
	"testing"
)

// processAlive reports whether the process pid still runs.
func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// killProcess ends the process pid at once.
func killProcess(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }

// checkServeStreams checks what 'vault serve' takes for the handover and the report it inherits: a pipe on
// each of its two descriptors, and nothing else.
func checkServeStreams(t *testing.T, dir string) {
	t.Helper()
	if inheritedPipe(int(mustOpen(t, configIn(dir)).Fd())) {
		t.Errorf("inheritedPipe() accepted a regular file")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if !inheritedPipe(int(r.Fd())) {
		t.Errorf("inheritedPipe() refused a pipe")
	}
	if inheritedPipe(1 << 20) {
		t.Errorf("inheritedPipe() accepted a descriptor that is not open")
	}
}
