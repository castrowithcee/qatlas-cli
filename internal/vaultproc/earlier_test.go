//go:build linux || darwin || windows

package vaultproc

import (
	"os"
	"testing"
)

// Only a process of this user whose id names a process that can be ended is one to end; another user's is
// never one, and an id that cannot be used is reported.
func TestEarlierPID(t *testing.T) {
	for _, tc := range []struct {
		name    string
		peer    earlierPeer
		want    int
		unknown bool
	}{
		{"this user's process", earlierPeer{ownUser: true, pid: 4242}, 4242, false},
		{"another user's process", earlierPeer{ownUser: false, pid: 4242}, 0, false},
		{"another user's without an id", earlierPeer{ownUser: false, pid: 0}, 0, false},
		{"another PID namespace", earlierPeer{ownUser: true, pid: 0}, 0, true},
		{"a negative id", earlierPeer{ownUser: true, pid: -1}, 0, true},
		{"the init process", earlierPeer{ownUser: true, pid: 1}, 0, true},
		{"this process", earlierPeer{ownUser: true, pid: os.Getpid()}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := earlierPID(tc.peer)
			if got != tc.want || (err != nil) != tc.unknown {
				t.Fatalf("earlierPID(%+v) = %d, %v, want %d, error %v", tc.peer, got, err, tc.want, tc.unknown)
			}
		})
	}
}
