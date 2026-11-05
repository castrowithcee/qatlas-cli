//go:build darwin

package vaultproc

import (
	"net"
	"syscall"
)

// socketPeer returns the user and the process id the kernel reports for the peer of conn. Seen from a
// client, they are those of the process that listens.
func socketPeer(conn net.Conn) (uint32, int, error) { return peerCred(conn) }

// signalEarlier sends SIGTERM to the vault process pid of the earlier release that serves path, right after
// checking again that pid still serves it: macOS has no handle that holds a process by more than its id.
func signalEarlier(path string, pid int) error {
	if err := recheckEarlier(path, pid); err != nil {
		return err
	}
	return syscall.Kill(pid, syscall.SIGTERM)
}
