//go:build linux

package vaultproc

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// userRuntimeDir returns the user's runtime directory when XDG_RUNTIME_DIR is not set: /run/user/<uid>,
// taken only when it is a real directory of this user that nobody else may enter, as the specification
// requires of the runtime directory. Otherwise it returns "".
func userRuntimeDir() string {
	uid := os.Getuid()
	dir := filepath.Join(userRuntimeRoot, strconv.Itoa(uid))
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return ""
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != uid {
		return ""
	}
	return dir
}
