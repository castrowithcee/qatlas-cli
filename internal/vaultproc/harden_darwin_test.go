//go:build darwin

package vaultproc

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

// hardenHidesProgram reports whether Harden hides the program a hardened process runs from this user. On
// macOS it does not: PT_DENY_ATTACH keeps debuggers out, not kern.procargs2.
const hardenHidesProgram = false

// wantHardened is what hardenedState reports in a process Harden protected.
const wantHardened = "core 0 0 <nil>"

// hardenedState reports what Harden changed in this process that can be read back: its core file size
// limit. PT_DENY_ATTACH leaves nothing this process can read about itself.
func hardenedState() string {
	var limit unix.Rlimit
	err := unix.Getrlimit(unix.RLIMIT_CORE, &limit)
	return strings.TrimSpace(fmt.Sprintln("core", limit.Cur, limit.Max, err))
}
