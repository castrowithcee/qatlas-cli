//go:build legacy_vault_names

package config

// A build with the tag legacy_vault_names keeps its configuration in the folder of the release before
// qatlas-connect and so never migrates: it stands in for that release where a test hands the vault over
// from it.
const homeDirName = legacyHomeDirName
