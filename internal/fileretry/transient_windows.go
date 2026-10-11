//go:build windows

package fileretry

import (
	"errors"
	"syscall"
)

const (
	errorAccessDenied     syscall.Errno = 5
	errorSharingViolation syscall.Errno = 32
	errorLockViolation    syscall.Errno = 33
)

// Transient reports the errors a file shows while another process holds it for a moment.
func Transient(err error) bool {
	return errors.Is(err, errorSharingViolation) || errors.Is(err, errorLockViolation) ||
		errors.Is(err, errorAccessDenied)
}
