//go:build legacy_vault_names

package vaultproc

// A build with the tag legacy_vault_names reaches its vault process by the names of the release before
// qatlas-connect, and nothing else differs: it stands in for that release where a test hands the vault over
// from it.
const (
	proofLabel     = legacyProofLabel
	runtimeDirName = legacyRuntimeDirName
	pipeName       = legacyPipeName
)
