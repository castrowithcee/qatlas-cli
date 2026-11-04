package vaultproc

// legacyProofLabel is the proof label of the release before qatlas-connect. Only a build with the tag
// legacy_vault_names proves with it, to stand in for that release; this release accepts it nowhere. Kept until
// the next major release.
const legacyProofLabel = "qatlas vault process proof v2\x00"

// legacyRuntimeDirName is the directory below the runtime directory that the release before qatlas-connect
// keeps its socket in; a successor looks there only for a vault process of that release that hands the vault
// over to it (see ListenSuccessor). Kept until the next major release.
const legacyRuntimeDirName = "qatlas"

// legacyPipeName starts the pipe name of the release before qatlas-connect on Windows. Windows never hands
// over, so only a build with the tag legacy_vault_names uses it. Kept until the next major release.
const legacyPipeName = "qatlas-vault-"
