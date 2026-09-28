//go:build windows

package vaultproc

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// hardenHidesProgram reports whether Harden hides the program a hardened process runs from this user. On
// Windows it does not: the DACL leaves PROCESS_QUERY_LIMITED_INFORMATION, which QueryFullProcessImageName
// needs, to this user.
const hardenHidesProgram = false

// wantHardened is what hardenedState reports in a process Harden protected.
const wantHardened = "read denied true, write denied true, query allowed true"

// hardenedState reports what Harden changed in this process: whether a process of this user, this one
// asking by its id rather than through its own pseudo handle, may still open it to read or write its
// memory, and to ask which program it runs.
func hardenedState() string {
	pid := uint32(os.Getpid())
	openable := func(access uint32) bool {
		h, err := windows.OpenProcess(access, false, pid)
		if err != nil {
			return false
		}
		_ = windows.CloseHandle(h)
		return true
	}
	return fmt.Sprintf("read denied %v, write denied %v, query allowed %v",
		!openable(windows.PROCESS_VM_READ), !openable(windows.PROCESS_VM_WRITE|windows.PROCESS_VM_OPERATION),
		openable(windows.PROCESS_QUERY_LIMITED_INFORMATION))
}
