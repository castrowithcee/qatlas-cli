//go:build windows

package vault

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// daclOf returns the DACL of path in SDDL, with whether it inherits from the directory above.
func daclOf(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(%s) error = %v", path, err)
	}
	return sd.String()
}

func currentSID(t *testing.T) string {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return user.User.Sid.String()
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
		dacl := daclOf(t, path)
		if !strings.HasPrefix(dacl, "D:P") || strings.Count(dacl, "(") != 1 || !strings.Contains(dacl, ";;;"+sid+")") {
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
		{fmt.Sprintf("D:P(A;;FA;;;%s)(A;;FA;;;SY)(A;;FA;;;BA)", sid), false},
		{fmt.Sprintf("D:P(A;;FA;;;%s)(D;;FA;;;WD)", sid), false},
		{fmt.Sprintf("D:P(A;;FA;;;%s)(A;OICIIO;FA;;;WD)", sid), false},
		{fmt.Sprintf("D:P(A;;FA;;;%s)(A;;0x100080;;;WD)", sid), false},
		{fmt.Sprintf("D:P(A;;FA;;;%s)(A;;FR;;;WD)", sid), true},
		{fmt.Sprintf("D:P(A;;FA;;;%s)(A;;FW;;;BU)", sid), true},
		{fmt.Sprintf("D:P(A;;FA;;;%s)(A;;WD;;;AU)", sid), true},
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
