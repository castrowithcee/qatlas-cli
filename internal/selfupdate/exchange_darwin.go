package selfupdate

import "golang.org/x/sys/unix"

// exchange swaps the directory entries a and b in one step.
func exchange(a, b string) error { return unix.RenamexNp(a, b, unix.RENAME_SWAP) }
