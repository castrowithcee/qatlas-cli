//go:build linux || darwin || windows

package vaultproc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// ErrEarlierUnknown reports that something serves the socket, or the pipe, of the release before
// qatlas-connect for a vault, but cannot be told to be a vault process of this user. It is not ended.
var ErrEarlierUnknown = errors.New("something serves the vault process socket of the release before " +
	"qatlas-connect, but its process cannot be identified, so it is not ended")

// earlierPeer is what the system reports about the process that serves the socket or pipe of the earlier
// release: whether it runs as this user, and its id.
type earlierPeer struct {
	ownUser bool
	pid     int
}

// earlierEndWait bounds how long EndEarlier waits for the process it ended to stop serving.
const earlierEndWait = 2 * time.Second

// earlierProbeTimeout bounds one connection a probe of the earlier socket or pipe opens.
const earlierProbeTimeout = time.Second

// FindEarlier returns the id of the vault process of the release before qatlas-connect that holds the vault
// in vaultDir unlocked, which this release cannot ask, since it reaches that process by other names; 0 where
// there is none.
//
// Only a process of this user counts that serves the earlier release's socket or pipe for vaultDir: on
// Linux and macOS a socket of this user in a directory nobody else may enter, with the lock beside it held,
// on Windows the pipe, served by a process of this user. Its id and user are the kernel's credentials of
// the socket, or the pipe's record of the process that serves it. It only connects, and sends nothing. A
// socket or pipe that is served but whose process cannot be told is reported as ErrEarlierUnknown.
func FindEarlier(vaultDir string) (int, error) {
	path := earlierPathOf(vaultDir)
	if path == "" {
		return 0, nil
	}
	return findEarlierAt(path)
}

// EndEarlier ends the vault process FindEarlier finds for vaultDir, the way Client.Lock ends one of another
// protocol version: with SIGTERM, on which it overwrites its secrets and ends, and on Windows with
// TerminateProcess. It asks nothing of it, and waits until it no longer serves. It returns the id of the
// process ended, 0 where there was none. A process that cannot be identified is not ended (see FindEarlier).
func EndEarlier(ctx context.Context, vaultDir string) (int, error) {
	path := earlierPathOf(vaultDir)
	if path == "" {
		return 0, nil
	}
	pid, err := findEarlierAt(path)
	if pid == 0 || err != nil {
		return 0, err
	}
	if err := signalEarlier(path, pid); err != nil {
		return pid, fmt.Errorf("cannot end the vault process of the release before qatlas-connect (pid %d): %w",
			pid, err)
	}
	for deadline := time.Now().Add(earlierEndWait); ; {
		if _, served, _ := probeEarlier(path); !served {
			return pid, nil
		}
		if time.Now().After(deadline) {
			return pid, fmt.Errorf("the vault process of the release before qatlas-connect (pid %d) did not end "+
				"in time", pid)
		}
		select {
		case <-ctx.Done():
			return pid, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// earlierPathOf returns the socket or pipe of the earlier release for vaultDir, or "" where it cannot be
// told or is this release's own, as it is in a build that stands in for the earlier release.
func earlierPathOf(vaultDir string) string {
	path, err := earlierPath(vaultDir)
	if err != nil {
		return ""
	}
	if current, err := SocketPath(vaultDir); err != nil || current == path {
		return ""
	}
	return path
}

// findEarlierAt is FindEarlier for the socket or pipe at path.
func findEarlierAt(path string) (int, error) {
	peer, served, err := probeEarlier(path)
	switch {
	case !served:
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("%w (%s): %w", ErrEarlierUnknown, path, err)
	}
	pid, err := earlierPID(peer)
	if err != nil {
		return 0, fmt.Errorf("%w (%s): %w", ErrEarlierUnknown, path, err)
	}
	return pid, nil
}

// earlierPID decides on the process peer serves the earlier socket or pipe with: its id where it is a
// process of this user that may be ended, 0 where it runs as another user, which is never one to end, and
// an error where its id names no process that can be ended safely.
func earlierPID(peer earlierPeer) (int, error) {
	switch {
	case !peer.ownUser:
		return 0, nil
	case peer.pid <= 1, peer.pid == os.Getpid():
		// 0 is a peer in another PID namespace, 1 the init process; this process ends no other way.
		return 0, errors.New("its process id cannot be used")
	}
	return peer.pid, nil
}

// recheckEarlier checks, right before the signal, that path is still served by the process pid of this
// user, so the signal reaches the process found and no other that took its id since.
func recheckEarlier(path string, pid int) error {
	peer, served, err := probeEarlier(path)
	if err != nil {
		return err
	}
	if !served || !peer.ownUser || peer.pid != pid {
		return errors.New("it no longer serves the socket it was found at")
	}
	return nil
}
