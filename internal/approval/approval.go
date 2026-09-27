// Package approval decides which connections an encrypted vault hands a vault credential's secret to. A
// connection is approved for its scope as it is configured at that moment (see vault.Scope); any later
// change of its endpoint, provider, permissions, targets, tools list, or credential entry leaves it open
// until a person approves it again. The approvals themselves live inside the encrypted vault.
//
// Every function here reads or writes the approvals of a vault unlocked in this process and never asks for
// a passphrase: making sure the person at hand may approve at all, and unlocking the vault for it, is up to
// the caller. Every change is handed to a running vault process, the way every other vault change is.
package approval

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultmigrate"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// The fields of a scope, as a FieldChange names them.
const (
	FieldCredential  = "credential"
	FieldProvider    = "provider"
	FieldOrigin      = "origin"
	FieldPermissions = "permissions"
	FieldTargets     = "targets"
	FieldTools       = "tools"
)

// FieldChange is one field of a connection's scope that differs from what was approved, each side written as
// a person reads it. It never holds a secret.
type FieldChange struct {
	Field  string
	Before string
	After  string
}

// Change is one connection that reads a vault credential and is not approved as it is configured now.
type Change struct {
	Connection string
	// New is true when the vault holds no approval for the connection at all; Before is nil then.
	New    bool
	Before *vault.Scope
	After  vault.Scope
	// Fields lists what differs from Before, in a fixed order; it is empty for a new connection.
	Fields []FieldChange
}

// Report is what Pending finds.
type Report struct {
	// Open lists the connections that are not approved as they are configured now, by name.
	Open []Change
	// Stale names the approvals of connections that no longer exist, were renamed, or no longer read a vault
	// credential, sorted. Revoke removes them.
	Stale []string
}

// candidate is one connection of cfg that reads a vault credential the vault holds an entry for.
type candidate struct {
	scope vault.Scope
	id    string
}

// candidates returns every connection of cfg that reads a vault credential the vault holds an entry for, by
// name. A connection whose credential has no entry yet has no secret to hand out, and nothing to approve.
func candidates(cfg *config.Config, v *vault.Vault) (map[string]candidate, error) {
	out := map[string]candidate{}
	if cfg == nil {
		return out, nil
	}
	for name, connection := range cfg.Connections {
		if cfg.Credentials[connection.Credential].Type != config.CredentialTypeVault {
			continue
		}
		resolved, err := cfg.Resolve(name, "")
		if err != nil {
			return nil, err
		}
		id, ok, err := v.CredentialID(connection.Credential)
		if err != nil {
			return nil, err
		}
		if ok {
			out[name] = candidate{scope: secret.ScopeOf(resolved), id: id}
		}
	}
	return out, nil
}

// Pending reports which connections of cfg the vault unlocked in this process has not approved as they are
// configured now, with what changed, and which approvals no longer belong to a connection. It fails with
// vault.ErrNotUnlocked or vault.ErrNotEncrypted where there are no approvals to compare with.
func Pending(cfg *config.Config, v *vault.Vault) (Report, error) {
	approvals, err := v.Approvals()
	if err != nil {
		return Report{}, err
	}
	current, err := candidates(cfg, v)
	if err != nil {
		return Report{}, err
	}

	var report Report
	for _, name := range sortedKeys(current) {
		c := current[name]
		approved, ok := approvals[name]
		switch {
		case !ok:
			report.Open = append(report.Open, Change{Connection: name, New: true, After: c.scope.Normalized()})
		case approved.Fingerprint != vault.Fingerprint(c.scope, c.id):
			before := approved.Scope
			report.Open = append(report.Open, Change{
				Connection: name, Before: &before, After: c.scope.Normalized(),
				Fields: diff(approved, c.scope, c.id),
			})
		}
	}
	for _, name := range sortedKeys(approvals) {
		if connection, ok := cfgConnection(cfg, name); !ok ||
			cfg.Credentials[connection.Credential].Type != config.CredentialTypeVault {
			report.Stale = append(report.Stale, name)
		}
	}
	return report, nil
}

func cfgConnection(cfg *config.Config, name string) (config.Connection, bool) {
	if cfg == nil {
		return config.Connection{}, false
	}
	connection, ok := cfg.Connections[name]
	return connection, ok
}

// diff lists the fields of now that differ from what approved was given for, in a fixed order.
func diff(approved vault.Approval, now vault.Scope, id string) []FieldChange {
	before, after := approved.Scope.Normalized(), now.Normalized()
	var fields []FieldChange
	add := func(field, b, a string) {
		if b != a {
			fields = append(fields, FieldChange{Field: field, Before: b, After: a})
		}
	}
	switch {
	case before.Credential != after.Credential:
		add(FieldCredential, before.Credential, after.Credential)
	case approved.CredentialID != id:
		// Same name, another entry: the credential was removed from the vault and stored anew.
		fields = append(fields, FieldChange{Field: FieldCredential, Before: before.Credential,
			After: after.Credential + " (stored anew)"})
	}
	add(FieldProvider, before.Provider, after.Provider)
	add(FieldOrigin, before.Origin, after.Origin)
	add(FieldPermissions, list(before.Permissions), list(after.Permissions))
	add(FieldTargets, list(before.Targets), list(after.Targets))
	add(FieldTools, tools(before.Tools), tools(after.Tools))
	return fields
}

func list(values []string) string {
	if len(values) == 0 {
		return "(none)"
	}
	return strings.Join(values, " ")
}

func tools(values []string) string {
	if values == nil {
		return "(every tool the permissions allow)"
	}
	return list(values)
}

// ErrUnknownConnection reports a connection Approve was asked for that does not read a vault credential the
// vault holds an entry for.
var ErrUnknownConnection = errors.New("the connection does not read a secret stored in the vault")

// Approve approves the named connections of cfg as they are configured now, or every open one when names is
// empty, in the vault unlocked in this process, and hands the new approvals to a running vault process. It
// returns the names it approved, sorted, and a warning for a vault process that could not take them, "" when
// there is none to tell. A name that does not read a vault credential the vault holds an entry for fails
// with ErrUnknownConnection, before anything is written.
func Approve(ctx context.Context, cfg *config.Config, v *vault.Vault, names []string) (approved []string,
	warning string, err error) {
	current, err := candidates(cfg, v)
	if err != nil {
		return nil, "", err
	}
	if len(names) == 0 {
		report, err := Pending(cfg, v)
		if err != nil {
			return nil, "", err
		}
		for _, change := range report.Open {
			names = append(names, change.Connection)
		}
	}
	scopes := make([]vault.Scope, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		c, ok := current[name]
		if !ok {
			return nil, "", fmt.Errorf("connection %s: %w", name, ErrUnknownConnection)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		scopes = append(scopes, c.scope)
		approved = append(approved, name)
	}
	if len(scopes) == 0 {
		return nil, "", nil
	}
	if err := v.Approve(scopes); err != nil {
		return nil, "", err
	}
	sort.Strings(approved)
	return approved, Sync(ctx, v), nil
}

// Revoke removes the approvals of the named connections, most often the stale ones Pending reports, from the
// vault unlocked in this process, and hands the change to a running vault process. It returns how many it
// removed and a warning as Approve does.
func Revoke(ctx context.Context, v *vault.Vault, names []string) (removed int, warning string, err error) {
	removed, err = v.Revoke(names)
	if err != nil || removed == 0 {
		return removed, "", err
	}
	return removed, Sync(ctx, v), nil
}

// Sync hands the approvals of the vault unlocked in this process to the vault process that holds it
// unlocked, so it checks every later access against them. A platform without a vault process, or none
// running, needs nothing said and returns "". Any other failure returns a warning that names the way out and
// never a secret: the vault itself already holds the change.
func Sync(ctx context.Context, v *vault.Vault) string {
	if !vaultproc.Supported || v == nil {
		return ""
	}
	bindings, err := v.Bindings()
	if err != nil {
		return ""
	}
	err = vaultmigrate.SyncChange(ctx, v, func(ctx context.Context, client *vaultproc.Client) error {
		return client.Bind(ctx, bindings)
	})
	if err == nil {
		return ""
	}
	return fmt.Sprintf("the vault holds the approvals, but the vault process that holds it unlocked could not "+
		"take them and still checks with the approvals it held before: %s; %s", err, secret.VaultProcessRemedy(err))
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
