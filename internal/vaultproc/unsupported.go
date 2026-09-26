//go:build !linux

package vaultproc

import "net"

// VerifyProgram refuses every peer on this platform: nothing checks here yet what runs at the other end.
func VerifyProgram(net.Conn) error { return ErrUnsupported }

// VerifyUser refuses every peer on this platform, like VerifyProgram.
func VerifyUser(net.Conn) error { return ErrUnsupported }

func peerPID(net.Conn) int { return 0 }

// Listen does not open a vault socket on this platform.
func Listen(string) (net.Listener, error) { return nil, ErrUnsupported }

// Harden has nothing to protect the vault process with on this platform.
func Harden() error { return ErrUnsupported }
