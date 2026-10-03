//go:build !darwin

package vaultproc

// fallbackSocketDir reports that the socket has no other place to move to when its usual path is too long.
func fallbackSocketDir() (string, bool) { return "", false }
