//go:build !linux && !darwin

package vaultproc

import "net"

// Supported reports whether a vault process runs on this platform. It does not here yet.
const Supported = false

// VerifyProgram refuses every peer on this platform: nothing checks here yet what runs at the other end.
func VerifyProgram(net.Conn) error { return ErrUnsupported }

// VerifyUser refuses every peer on this platform, like VerifyProgram.
func VerifyUser(net.Conn) error { return ErrUnsupported }

func verifyServer(net.Conn) error { return ErrUnsupported }

func peerPID(net.Conn) int { return 0 }

// userRuntimeDir finds no runtime directory without XDG_RUNTIME_DIR on this platform.
func userRuntimeDir() string { return "" }

// Listen does not open a vault socket on this platform.
func Listen(string) (net.Listener, error) { return nil, ErrUnsupported }

// Harden has nothing to protect the vault process with on this platform.
func Harden() error { return ErrUnsupported }

// ReplacedProgram cannot tell on this platform whether the running program was replaced.
func ReplacedProgram() (string, bool) { return "", false }
