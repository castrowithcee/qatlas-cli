//go:build darwin

package cli

import (
	"testing"

	"golang.org/x/sys/unix"
)

// processArguments returns what the kernel keeps of how the process pid was started: its exec path, its
// arguments, and its environment.
func processArguments(t *testing.T, pid int) string {
	t.Helper()
	args, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		t.Fatalf("cannot read the arguments of process %d: %v", pid, err)
	}
	return string(args)
}
