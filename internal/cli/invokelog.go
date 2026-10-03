package cli

import (
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// invokeLogWriter writes the invocation log entries of one run, signed wherever the vault's key is at hand
// and unsigned everywhere else, through the one chain every writer shares:
//
//   - A vault process that passes the client's check appends the entry itself, signed; it is then the only
//     writer, and this process writes nothing.
//   - Without one, a vault unlocked in this process signs the entry here, under the same lock.
//   - A vault that is locked without a vault process, unencrypted, or absent has no key to sign with: the
//     entry is written unsigned, exactly as before the log was signed at all.
//
// A vault process that is there but cannot take the entry, one of another build included, never costs the
// entry: it is written here instead, unsigned unless this process holds the vault unlocked, and the reason
// comes back as an *invokelog.UnsignedError for the caller to warn about.
type invokeLogWriter struct {
	logger *invokelog.Logger
	// vault is the vault this run resolves its credentials from, so one it unlocked for this very invoke
	// is found unlocked here too; nil where the run has none.
	vault *vault.Vault
}

func (w invokeLogWriter) Append(f invokelog.Fields) error {
	return connlog.SigningWriter{Logger: w.logger, Vault: w.vault, ProcessSupported: vaultProcessSupported}.Append(f)
}
