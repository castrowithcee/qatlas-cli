//go:build linux

package vaultproc

import (
	"fmt"
	"strings"
	"syscall"
)

// hardenHidesProgram reports whether Harden hides the program a hardened process runs from this user. On
// Linux it does: the cleared dumpable flag closes /proc/<pid>/exe as well.
const hardenHidesProgram = true

// wantHardened is what hardenedState reports in a process Harden protected.
const wantHardened = "dumpable 0 errno 0"

// hardenedState reports what Harden changed in this process: its dumpable flag.
func hardenedState() string {
	dumpable, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_GET_DUMPABLE, 0, 0)
	return strings.TrimSpace(fmt.Sprintln("dumpable", dumpable, errno))
}
