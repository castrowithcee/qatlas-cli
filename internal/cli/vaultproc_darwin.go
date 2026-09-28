//go:build darwin

package cli

// sessionWarnings has nothing to read on macOS: there is no systemd-logind whose settings would end the
// vault process at logout or remove its socket.
func sessionWarnings(string) []string { return nil }
