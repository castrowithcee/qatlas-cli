//go:build linux

package vaultproc

import (
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

// socketPeer returns the user and the process id the kernel reports for the peer of conn. Seen from a
// client, they are those of the process that listens.
func socketPeer(conn net.Conn) (uint32, int, error) {
	cred, err := peerCred(conn)
	if err != nil {
		return 0, 0, err
	}
	return cred.Uid, int(cred.Pid), nil
}

// signalEarlier sends SIGTERM to the vault process pid of the earlier release that serves path. The process
// is held by a pidfd before path is checked again, so the signal reaches the process that still served path
// after the pidfd was taken, never one that took its id since. Without pidfds, before Linux 5.3, it is sent
// by id right after the check.
func signalEarlier(path string, pid int) error {
	pidfd, err := unix.PidfdOpen(pid, 0)
	if errors.Is(err, unix.ENOSYS) {
		if err := recheckEarlier(path, pid); err != nil {
			return err
		}
		return unix.Kill(pid, unix.SIGTERM)
	}
	if err != nil {
		return err
	}
	defer unix.Close(pidfd)
	if err := recheckEarlier(path, pid); err != nil {
		return err
	}
	return unix.PidfdSendSignal(pidfd, unix.SIGTERM, nil, 0)
}
