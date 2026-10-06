//go:build windows

package vaultproc

import (
	"fmt"
	"os"
	"unsafe"

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
//
// A token that has SeDebugPrivilege enabled opens any process whatever its DACL says, which Harden documents
// as a limit of the protection. For such a token, as the elevated administrator of a CI runner, opening says
// nothing about the DACL, so the process's own DACL is read and judged instead: what its entries allow
// decides each answer, and a DACL that is not protected, or has an entry for anyone but this user, the
// owner or the system, answers as open.
func hardenedState() string {
	if debugPrivilegeEnabled() {
		return daclState()
	}
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

// debugPrivilegeEnabled reports whether the token of this process has SeDebugPrivilege enabled.
func debugPrivilegeEnabled() bool {
	name, err := windows.UTF16PtrFromString("SeDebugPrivilege")
	if err != nil {
		return false
	}
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, name, &luid); err != nil {
		return false
	}
	token := windows.GetCurrentProcessToken()
	var size uint32
	_ = windows.GetTokenInformation(token, windows.TokenPrivileges, nil, 0, &size)
	if size == 0 {
		return false
	}
	buf := make([]byte, size)
	if err := windows.GetTokenInformation(token, windows.TokenPrivileges, &buf[0], size, &size); err != nil {
		return false
	}
	privileges := (*windows.Tokenprivileges)(unsafe.Pointer(&buf[0]))
	for _, p := range privileges.AllPrivileges() {
		if p.Luid == luid && p.Attributes&windows.SE_PRIVILEGE_ENABLED != 0 {
			return true
		}
	}
	return false
}

// daclState answers as hardenedState does from the DACL of this process alone.
func daclState() string {
	sd, err := windows.GetSecurityInfo(windows.CurrentProcess(), windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "cannot read the DACL: " + err.Error()
	}
	control, _, err := sd.Control()
	dacl, _, derr := sd.DACL()
	if err != nil || derr != nil || dacl == nil || control&windows.SE_DACL_PROTECTED == 0 {
		return "the DACL is missing or not protected"
	}
	user, err := currentUserSID()
	if err != nil {
		return err.Error()
	}
	owner, _ := windows.StringToSid("S-1-3-4") // OWNER RIGHTS
	system, _ := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	var granted uint32
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return "cannot read the DACL: " + err.Error()
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
			(!sid.Equals(user) && !sid.Equals(owner) && !sid.Equals(system)) {
			return "the DACL holds an entry of another kind or for someone else"
		}
		granted |= uint32(ace.Mask)
	}
	return fmt.Sprintf("read denied %v, write denied %v, query allowed %v",
		granted&windows.PROCESS_VM_READ == 0, granted&(windows.PROCESS_VM_WRITE|windows.PROCESS_VM_OPERATION) == 0,
		granted&windows.PROCESS_QUERY_LIMITED_INFORMATION == windows.PROCESS_QUERY_LIMITED_INFORMATION)
}
