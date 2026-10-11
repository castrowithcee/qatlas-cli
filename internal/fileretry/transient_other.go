//go:build !windows

package fileretry

// Transient is false off Windows: only Windows file locks are cured by waiting.
func Transient(error) bool { return false }
