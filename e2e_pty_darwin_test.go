//go:build darwin

package main

import (
	"bytes"
	"os"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPTY opens a new pseudo-terminal pair with the ioctls grantpt, unlockpt, and ptsname use on macOS.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	fd := int(master.Fd())
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
		_ = master.Close()
		t.Skipf("cannot grant the pseudo-terminal: %v", err)
	}
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
		_ = master.Close()
		t.Skipf("cannot unlock the pseudo-terminal: %v", err)
	}
	// TIOCPTYGNAME writes the slave's path, at most 128 bytes, into the buffer it is given.
	var name [128]byte
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), unix.TIOCPTYGNAME,
		uintptr(unsafe.Pointer(&name[0]))); errno != 0 {
		_ = master.Close()
		t.Skipf("cannot name the pseudo-terminal: %v", errno)
	}
	end := bytes.IndexByte(name[:], 0)
	if end <= 0 {
		_ = master.Close()
		t.Skipf("the pseudo-terminal has no name")
	}
	path := string(name[:end])
	slave, err = os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		t.Skipf("cannot open the pseudo-terminal: %v", err)
	}
	return master, slave
}
