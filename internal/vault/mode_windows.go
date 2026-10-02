//go:build windows

package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileAccess are the rights to a vault file that count as reaching it: reading or writing its content, or
// changing who may. Rights such as reading its attributes, or deleting it, do not.
const fileAccess = windows.FILE_READ_DATA | windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA |
	windows.WRITE_DAC | windows.WRITE_OWNER | windows.GENERIC_ALL | windows.GENERIC_READ | windows.GENERIC_WRITE

// The ACE types an ACL may hold that allow rights. The plain and the conditional (callback) kind carry
// their mask and SID in the same place; the object and compound kinds do not, and are not read.
const (
	aceAllowed               = windows.ACCESS_ALLOWED_ACE_TYPE
	aceAllowedCompound       = 4
	aceAllowedObject         = 5
	aceAllowedCallback       = 9
	aceAllowedCallbackObject = 11
)

// checkMode refuses a vault file that others can read or write, the way file modes do elsewhere: on
// Windows the mode os.Stat reports only reflects the read-only attribute, so the file's ACL is read
// instead. Besides this user, only the system and the Administrators group may reach the file, as root may
// on other systems, and only they may own it. An allowing entry for anyone else, a group this user belongs
// to included, and an ACL that allows everyone, refuse the file. Entries that only deny, or only pass on
// to new files, are never a reason to refuse.
func checkMode(path string, _ fs.FileInfo) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("cannot read the permissions of %s: %w", path, err)
	}
	trusted, err := trustedSIDs()
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return &PermissionError{Path: path, Grantee: "an owner that cannot be read"}
	}
	if !isTrusted(owner, trusted) {
		return &PermissionError{Path: path, Grantee: accountName(owner) + " as its owner"}
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("cannot read the permissions of %s: %w", path, err)
	}
	if dacl == nil {
		return &PermissionError{Path: path, Grantee: "everyone"}
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("cannot read the permissions of %s: %w", path, err)
		}
		if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		switch ace.Header.AceType {
		case aceAllowed, aceAllowedCallback:
			if ace.Mask&fileAccess == 0 {
				continue
			}
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !isTrusted(sid, trusted) {
				return &PermissionError{Path: path, Grantee: accountName(sid)}
			}
		case aceAllowedCompound, aceAllowedObject, aceAllowedCallbackObject:
			return &PermissionError{Path: path, Grantee: "an entry of a kind this check does not read"}
		}
	}
	return nil
}

// trustedSIDs are who may reach a vault file besides nobody: this user, the system, the Administrators
// group, and the owner through the OWNER RIGHTS and CREATOR OWNER entries, which never name anyone else.
func trustedSIDs() ([]*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("cannot determine this process's user: %w", err)
	}
	trusted := []*windows.SID{user.User.Sid}
	for _, known := range []windows.WELL_KNOWN_SID_TYPE{windows.WinLocalSystemSid, windows.WinBuiltinAdministratorsSid,
		windows.WinCreatorOwnerRightsSid, windows.WinCreatorOwnerSid} {
		sid, err := windows.CreateWellKnownSid(known)
		if err != nil {
			return nil, fmt.Errorf("cannot determine the system accounts: %w", err)
		}
		trusted = append(trusted, sid)
	}
	return trusted, nil
}

func isTrusted(sid *windows.SID, trusted []*windows.SID) bool {
	for _, t := range trusted {
		if sid.Equals(t) {
			return true
		}
	}
	return false
}

// accountName names sid as DOMAIN\name where it can be looked up, and by the identifier itself otherwise.
func accountName(sid *windows.SID) string {
	if account, domain, _, err := sid.LookupAccount(""); err == nil && account != "" {
		if domain != "" {
			return domain + `\` + account
		}
		return account
	}
	return sid.String()
}

// privateFileSDDL and privateDirSDDL allow this user full access and nobody else any, and inherit nothing
// from the directory above. A directory passes the same entry on to what is created in it.
const (
	privateFileSDDL = "D:P(A;;FA;;;%s)"
	privateDirSDDL  = "D:P(A;OICI;FA;;;%s)"
)

// restrictFile gives a file the vault is about to fill an ACL that lets this user alone reach it. The mode
// is set too, which on Windows only clears the read-only attribute.
func restrictFile(path string) error {
	if err := os.Chmod(path, fileMode); err != nil {
		return err
	}
	return setPrivateACL(path, privateFileSDDL)
}

// mkdirPrivate creates dir and every parent it is missing, each with an ACL that lets this user alone reach
// it and what is created in it. A directory that exists already keeps its ACL.
func mkdirPrivate(dir string) error {
	var missing []string
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if _, err := os.Lstat(d); err == nil || !errors.Is(err, fs.ErrNotExist) {
			break
		}
		missing = append(missing, d)
		if filepath.Dir(d) == d {
			break
		}
	}
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := setPrivateACL(missing[i], privateDirSDDL); err != nil {
			return fmt.Errorf("cannot set the permissions of %s: %w", missing[i], err)
		}
	}
	return nil
}

// setPrivateACL replaces the DACL of path with sddl, filled in with this user.
func setPrivateACL(path, sddl string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("cannot determine this process's user: %w", err)
	}
	sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf(sddl, user.User.Sid.String()))
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
