//go:build windows

package config

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// The mode of os.Chmod only reflects the read-only attribute on Windows, so a private file or directory
// needs an ACL: this user alone, nothing inherited. A directory passes its entry on to what it holds.
const (
	privateFileSDDL = "D:P(A;;FA;;;%s)"
	privateDirSDDL  = "D:P(A;OICI;FA;;;%s)"
)

func restrictFile(path string) error {
	if err := os.Chmod(path, fileMode); err != nil {
		return err
	}
	return setPrivateACL(path, privateFileSDDL)
}

func restrictDir(path string) error { return setPrivateACL(path, privateDirSDDL) }

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
