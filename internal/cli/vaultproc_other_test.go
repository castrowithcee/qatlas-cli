//go:build !linux && !darwin && !windows

package cli

// The next steps of vault-locked where unlocking only lasts for the process that unlocked.
const (
	vaultLockedStep    = "run 'qconnect vault unlock' in a terminal, or press ctrl+l in 'qconnect tui'"
	vaultLockedMCPStep = "agents cannot unlock the vault; ask the user to unlock it"
)
