//go:build !linux && !darwin

package cli

// The next steps of vault-locked where unlocking only lasts for the process that unlocked.
const (
	vaultLockedStep    = "run 'qatlas vault unlock' in a terminal, or press ctrl+l in 'qatlas tui'"
	vaultLockedMCPStep = "agents cannot unlock the vault; ask the user to unlock it"
)
