//go:build !linux

package selfupdate

// OtherProcesses is -1 off Linux: counting another process there needs more than this user's own rights.
func OtherProcesses() int { return -1 }
