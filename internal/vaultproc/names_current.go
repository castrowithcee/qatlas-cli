//go:build !legacy_vault_names

package vaultproc

// The names this release reaches its vault process by.
const (
	// proofLabel separates the proof from any other use of the nonce's hash.
	proofLabel = "qatlas-connect vault process proof v2\x00"
	// runtimeDirName is the directory below the runtime directory the socket lives in.
	runtimeDirName = "qatlas-connect"
	// pipeName starts the name of the vault pipe on Windows.
	pipeName = "qatlas-connect-vault-"
)
