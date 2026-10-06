//go:build windows

package vault

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// onlyThisUser reports whether the DACL of path is protected, so it inherits nothing from the directory
// above, and holds one allowing entry for sid and no other. The entry is compared by security identifier,
// not by its SDDL text, which renders well-known accounts such as the built-in administrator as an alias.
func onlyThisUser(t *testing.T, path string, sid *windows.SID) (string, bool) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(%s) error = %v", path, err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return sd.String(), false
	}
	if control&windows.SE_DACL_PROTECTED == 0 || dacl.AceCount != 1 {
		return sd.String(), false
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	return sd.String(), ace.Header.AceType == aceAllowed && sid.Equals((*windows.SID)(unsafe.Pointer(&ace.SidStart)))
}

func currentSID(t *testing.T) *windows.SID {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return user.User.Sid
}

// Every file the vault writes, and the directory it creates for them, lets this user alone reach it and
// inherits nothing from the directory above.
func TestVaultFilesArePrivate(t *testing.T) {
	lowWorkFactor(t)
	dir := t.TempDir()
	v := New(dir)
	if err := v.Set("wiki-reader", "token-id", "id-1", offering("s3cret-phrase")); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	sid := currentSID(t)
	for _, path := range []string{v.Dir(), filepath.Join(v.Dir(), keyFile), filepath.Join(v.Dir(), recipientFile),
		filepath.Join(v.Dir(), secretsFile)} {
		if dacl, ok := onlyThisUser(t, path, sid); !ok {
			t.Errorf("the DACL of %s is %s, want this user alone", path, dacl)
		}
	}
	if _, found, _, err := v.Get("wiki-reader", "token-id", offering("s3cret-phrase")); err != nil || !found {
		t.Fatalf("Get() of a private vault = %v, %v", found, err)
	}
}

// A vault file whose ACL lets anyone else reach it is refused, the way a widened mode is elsewhere; the
// system and the Administrators group, like root, are not anyone else.
func TestForeignAccessIsRefused(t *testing.T) {
	dir := t.TempDir()
	v := New(dir)
	if err := v.Set("wiki-reader", "token-id", "id-1", nil); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	path := filepath.Join(v.Dir(), plainFile)
	sid := currentSID(t)

	for _, tc := range []struct {
		sddl    string
		refused bool
	}{
		{fmt.Sprintf("D:P(A;;FA;;;%s)(A;;FA;;;SY)(A;;FA;;;BA)", sid.String()), false},
		{fmt.Sprintf("D:P(A;;FA;;;%s)(D;;FA;;;WD)", sid.String()), false},
		{fmt.Sprintf("D:P(A;;FA;;;%s)(A;OICIIO;FA;;;WD)", sid.String()), false},
		{fmt.Sprintf("D:P(A;;FA;;;%s)(A;;0x100080;;;WD)", sid.String()), false},
		{fmt.Sprintf("D:P(A;;FA;;;%s)(A;;FR;;;WD)", sid.String()), true},
		{fmt.Sprintf("D:P(A;;FA;;;%s)(A;;FW;;;BU)", sid.String()), true},
		{fmt.Sprintf("D:P(A;;FA;;;%s)(A;;WD;;;AU)", sid.String()), true},
	} {
		sd, err := windows.SecurityDescriptorFromString(tc.sddl)
		if err != nil {
			t.Fatal(err)
		}
		dacl, _, err := sd.DACL()
		if err != nil {
			t.Fatal(err)
		}
		if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
			windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
			t.Fatalf("SetNamedSecurityInfo(%s) error = %v", tc.sddl, err)
		}
		_, _, _, err = v.Get("wiki-reader", "token-id", nil)
		var perm *PermissionError
		switch {
		case tc.refused && (!errors.As(err, &perm) || perm.Grantee == "" || !strings.Contains(err.Error(), "icacls")):
			t.Errorf("Get() with the DACL %s error = %v, want a PermissionError naming who and the fix", tc.sddl, err)
		case !tc.refused && err != nil:
			t.Errorf("Get() with the DACL %s error = %v, want it read", tc.sddl, err)
		}
	}
}
