//go:build !windows

package selfupdate

// isTransientLock is false off Windows: only Windows file locks are cured by waiting.
func isTransientLock(error) bool { return false }
