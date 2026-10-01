//go:build !linux

package cli

// platformMCPRestart is no restart here: where a replaced program cannot be told reliably, or no process
// replaces itself in place, the server keeps running as before.
func platformMCPRestart() mcpRestart { return mcpRestart{} }
