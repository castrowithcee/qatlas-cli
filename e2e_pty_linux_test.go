//go:build linux

package main

import (
	"os"
	"strconv"
	"syscall"
	"testing"
	"unsafe"
)

// openPTY opens a new pseudo-terminal pair with nothing but the system calls the standard library has.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK,
		uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		_ = master.Close()
		t.Skipf("cannot unlock the pseudo-terminal: %v", errno)
	}
	var number uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN,
		uintptr(unsafe.Pointer(&number))); errno != 0 {
		_ = master.Close()
		t.Skipf("cannot name the pseudo-terminal: %v", errno)
	}
	slave, err = os.OpenFile("/dev/pts/"+strconv.FormatUint(uint64(number), 10), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		_ = master.Close()
		t.Skipf("cannot open the pseudo-terminal: %v", err)
	}
	return master, slave
}
