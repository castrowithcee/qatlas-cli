package cli

import (
	"context"
	"errors"

	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultmigrate"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
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
	state := vault.StateAbsent
	if w.vault != nil {
		if current, err := w.vault.State(); err == nil {
			state = current
		}
	}
	if state != vault.StateLocked && state != vault.StateUnlocked {
		return w.logger.Append(f)
	}

	var processErr error
	if vaultProcessSupported {
		// A vault whose process cannot even be addressed has none to ask, the same rule a credential read
		// follows (see secret.Resolver).
		if client, err := vaultmigrate.ProcessClientOf(w.vault); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), vaultproc.DefaultRequestTimeout)
			err = client.Log(ctx, f)
			cancel()
			if err == nil {
				return nil
			}
			if !errors.Is(err, vaultproc.ErrNotRunning) {
				processErr = err
			}
		}
	}

	if state == vault.StateUnlocked {
		if key, err := w.vault.LogKey(); err == nil {
			defer key.Clear()
			return w.logger.WithKey(key).Append(f)
		}
	}
	if err := w.logger.Append(f); err != nil {
		return err
	}
	if processErr != nil {
		return &invokelog.UnsignedError{Err: processErr}
	}
	return nil
}
