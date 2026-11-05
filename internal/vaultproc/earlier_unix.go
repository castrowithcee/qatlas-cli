//go:build linux || darwin

package vaultproc

import (
	"net"
	"os"
)

// earlierPath returns the socket the earlier release keeps for the vault in vaultDir.
func earlierPath(vaultDir string) (string, error) { return socketPath(vaultDir, legacyRuntimeDirName) }

// earlierCred asks the kernel for the user and the process id of the process that listens at the other end
// of conn. It is a variable only so a test can play a process of another user.
var earlierCred = socketPeer

// probeEarlier reports whether a process serves the socket at path, as serves tells, and who it is. A socket
// nobody listens on is served by no process. It connects, sends nothing, and closes at once; an error means
// the socket is served, but its process cannot be told.
func probeEarlier(path string) (earlierPeer, bool, error) {
	if !serves(path) {
		return earlierPeer{}, false, nil
	}
	conn, err := net.DialTimeout("unix", path, earlierProbeTimeout)
	if err != nil {
		if notRunning(err) {
			return earlierPeer{}, false, nil
		}
		return earlierPeer{}, true, err
	}
	defer conn.Close()
	uid, pid, err := earlierCred(conn)
	if err != nil {
		return earlierPeer{}, true, err
	}
	return earlierPeer{ownUser: uid == uint32(os.Getuid()), pid: pid}, true, nil
}
