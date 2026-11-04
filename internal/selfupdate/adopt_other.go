//go:build !linux && !darwin && !windows

package selfupdate

// adoptName does nothing on a platform without a release.
func adoptName(string) (string, bool, error) { return "", false, nil }
