// Package connlog writes the invocation log entries of qatlas's own changes to connections in the
// configuration file, one per connection that changed, and the writer that signs an entry wherever the
// vault's key is at hand. An entry names the operation, the surface the change was made on, the connection,
// and the result, never a value of the connection.
package connlog

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultmigrate"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// The surfaces a change is made on; they are the Path of the entry.
const (
	SurfaceTUI = "tui"
	SurfaceWeb = "web"
	SurfaceCLI = "cli"
)

// SigningWriter writes invocation log entries signed wherever the vault's key is at hand and unsigned
// everywhere else, through the one chain every writer shares:
//
//   - A vault process that passes the client's check appends the entry itself, signed; it is then the only
//     writer, and this process writes nothing.
//   - Without one, a vault unlocked in this process signs the entry here, under the same lock.
//   - A vault that is locked without a vault process, unencrypted, or absent has no key to sign with: the
//     entry is written unsigned.
//
// A vault process that is there but cannot take the entry never costs the entry: it is written here
// instead, unsigned unless this process holds the vault unlocked, and the reason comes back as an
// *invokelog.UnsignedError for the caller to warn about.
type SigningWriter struct {
	Logger *invokelog.Logger
	// Vault is the vault the log belongs to; nil where there is none.
	Vault *vault.Vault
	// ProcessSupported says whether this run may ask a vault process at all.
	ProcessSupported bool
}

// Append implements invokelog.Writer.
func (w SigningWriter) Append(f invokelog.Fields) error {
	state := vault.StateAbsent
	if w.Vault != nil {
		if current, err := w.Vault.State(); err == nil {
			state = current
		}
	}
	if state != vault.StateLocked && state != vault.StateUnlocked {
		return w.Logger.Append(f)
	}

	var processErr error
	if w.ProcessSupported {
		// A vault whose process cannot even be addressed has none to ask, the same rule a credential read
		// follows (see secret.Resolver).
		if client, err := vaultmigrate.ProcessClientOf(w.Vault); err == nil {
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
		if key, err := w.Vault.LogKey(); err == nil {
			defer key.Clear()
			return w.Logger.WithKey(key).Append(f)
		}
	}
	if err := w.Logger.Append(f); err != nil {
		return err
	}
	if processErr != nil {
		return &invokelog.UnsignedError{Err: processErr}
	}
	return nil
}

// Recorder logs the connection changes one surface makes. A nil Recorder logs nothing.
type Recorder struct {
	Writer  invokelog.Writer
	Surface string
}

// NewRecorder returns the recorder for surface that writes under the vault directory next to the
// configuration file at configPath, with retention days of the configuration, signed through v when v is the
// vault of that directory. processSupported says whether a vault process may be asked.
func NewRecorder(surface, configPath string, retentionDays int, v *vault.Vault, processSupported bool) *Recorder {
	dir := vault.New(filepath.Dir(configPath)).Dir()
	writer := SigningWriter{Logger: invokelog.New(dir, retentionDays), ProcessSupported: processSupported}
	if v != nil && v.Dir() == dir {
		writer.Vault = v
	}
	return &Recorder{Writer: writer, Surface: surface}
}

// Record appends one entry per connection that differs between before and after, in the order of
// config.ChangedConnections. Call it only after the change was committed to the configuration file. A
// failure never undoes the change: it is returned for the caller to show as a warning, and an unwritable
// entry or an invalid connection name does not keep the remaining entries from being written. The error
// never carries a connection name.
func (r *Recorder) Record(before, after *config.Config) error {
	if r == nil || r.Writer == nil {
		return nil
	}
	var invalid, failed, unsigned error
	for _, change := range Changes(before, after) {
		fields := invokelog.Fields{
			Path: r.Surface, Operation: operationOf(change.Kind), Connection: change.Name,
			Effect: change.Kind, Result: "success",
		}
		if err := fields.Validate(); err != nil {
			invalid = errors.New("a connection name could not be logged: " + err.Error())
			continue
		}
		err := r.Writer.Append(fields)
		var unsignedErr *invokelog.UnsignedError
		switch {
		case err == nil:
		case errors.As(err, &unsignedErr):
			unsigned = err
		default:
			failed = err
		}
	}
	switch {
	case failed != nil:
		return fmt.Errorf("the change was saved, but the log entry could not be written: %w", failed)
	case invalid != nil:
		return fmt.Errorf("the change was saved, but %w", invalid)
	case unsigned != nil:
		return fmt.Errorf("the change was saved and logged, but unsigned: %w", unsigned)
	}
	return nil
}

func operationOf(kind string) string {
	switch kind {
	case config.ConnectionCreated:
		return invokelog.OperationConnectionCreate
	case config.ConnectionDeleted:
		return invokelog.OperationConnectionDelete
	}
	return invokelog.OperationConnectionChange
}

// Changes lists the connections that changed between before and after: those whose entry differs (see
// config.ChangedConnections) and, besides, every remaining connection of after that reads a vault credential
// and whose approval scope, as approval.VaultScopes derives it, differs from the one in before or is new
// there. The second kind is a connection whose own entry stayed as it was while a service, a credential, or
// a payload credential it uses changed what an approval of it covers; it is listed as changed. A connection
// whose scope merely drops out of the vault scopes (its credential leaves the vault) opens nothing and is
// not listed. Where a scope cannot be derived, only the entry comparison counts. The result is sorted by
// name.
func Changes(before, after *config.Config) []config.ConnectionChange {
	changes := config.ChangedConnections(before, after)
	newScopes, err := approval.VaultScopes(after)
	if err != nil {
		return changes
	}
	oldScopes, err := approval.VaultScopes(before)
	if err != nil {
		oldScopes = nil
	}
	listed := make(map[string]bool, len(changes))
	for _, change := range changes {
		listed[change.Name] = true
	}
	old := make(map[string]string, len(oldScopes))
	for _, scope := range oldScopes {
		old[scope.Connection] = vault.Fingerprint(scope, "")
	}
	for _, scope := range newScopes {
		if listed[scope.Connection] {
			continue
		}
		if fingerprint, ok := old[scope.Connection]; !ok || fingerprint != vault.Fingerprint(scope, "") {
			changes = append(changes, config.ConnectionChange{Name: scope.Connection, Kind: config.ConnectionChanged})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Name < changes[j].Name })
	return changes
}
