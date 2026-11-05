//go:build windows

package vaultproc

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// earlierPath returns the pipe the earlier release serves the vault in vaultDir on.
func earlierPath(vaultDir string) (string, error) {
	abs, err := filepath.Abs(vaultDir)
	if err != nil {
		return "", err
	}
	path, _, err := pipePathNamed(abs, legacyPipeName)
	return path, err
}

// probeEarlier reports whether a process serves the pipe at path, and who it is: the process the pipe
// records as the server of the instance reached, with the user of its token. Serving the pipe is what
// holding the lock is on Linux and macOS: its first instance claimed the name for that process alone (see
// Listen). A pipe this user may not open is not this user's. It connects, sends nothing, and closes at once;
// an error means the pipe is served, but its process cannot be told.
func probeEarlier(path string) (earlierPeer, bool, error) {
	conn, err := dialSocket(context.Background(), path, time.Now().Add(earlierProbeTimeout))
	if err != nil {
		if notRunning(err) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return earlierPeer{}, false, nil
		}
		return earlierPeer{}, true, err
	}
	defer conn.Close()
	pid, err := peerProcessID(conn)
	if err != nil {
		return earlierPeer{}, true, err
	}
	process, err := openPeer(pid)
	if err != nil {
		return earlierPeer{}, true, err
	}
	defer windows.CloseHandle(process)
	own, err := runsAsMe(process)
	if err != nil {
		return earlierPeer{}, true, err
	}
	return earlierPeer{ownUser: own, pid: int(pid)}, true, nil
}

// runsAsMe reports whether process runs as this user, by the user of its token.
func runsAsMe(process windows.Handle) (bool, error) {
	own, err := currentUserSID()
	if err != nil {
		return false, err
	}
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return false, errors.New("its user cannot be read")
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return false, errors.New("its user cannot be read")
	}
	return user.User.Sid.Equals(own), nil
}

// signalEarlier ends the vault process pid of the earlier release that serves path with TerminateProcess.
// The process is held by a handle before path is checked again, so it ends the process that still served
// path after the handle was opened, never one that took its id since.
func signalEarlier(path string, pid int) error {
	process, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false,
		uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	if own, err := runsAsMe(process); err != nil || !own {
		return errors.New("it does not run as this user")
	}
	if err := recheckEarlier(path, pid); err != nil {
		return err
	}
	return windows.TerminateProcess(process, 1)
}
