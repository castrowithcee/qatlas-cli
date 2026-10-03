// Package approval decides which connections an encrypted vault hands a vault credential's secret to. A
// connection is approved for its scope as it is configured at that moment (see vault.Scope); any later
// change of its endpoint, provider, permissions, targets, tools list, paths, files, forward credentials with
// their fields, or credential entry leaves it
// open until a person approves it again. The approvals themselves live inside the encrypted vault.
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
	"time"

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
	FieldPaths       = "paths"
	FieldFiles       = "files"
	FieldForward     = "forward"
)

// FieldChange is one field of a connection's scope that differs from what was approved, each side written as
// a person reads it. It never holds a secret.
//
// A list field (permissions, targets, tools, paths, files, forward) that stays a list on both sides also
// carries what the change means: Added is what is newly asked for, Removed what falls away, and Kept what
// stays, each sorted. A change between a mode and a list - tools "(every tool the permissions allow)" and a
// list, paths "(every project)" and a list, files "(no local files)" and a list - and every single-value
// field carries only Before and After; IsListChange tells the two apart. Before and After are set in both
// cases.
type FieldChange struct {
	Field   string
	Before  string
	After   string
	Added   []string
	Removed []string
	Kept    []string
}

// IsListChange reports whether the change is told by Added, Removed, and Kept rather than by Before and
// After alone.
func (f FieldChange) IsListChange() bool {
	return len(f.Added) > 0 || len(f.Removed) > 0
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
	// Approved is when the connection was last approved; the zero time for a new connection.
	Approved time.Time
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
				Fields: diff(approved, c.scope, c.id), Approved: approved.Approved,
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
	// addList records a change of a list field. mode is true when one side is a mode rather than a list, which
	// stays readable only as before and after, as does a list whose text changed without a different entry.
	addList := func(field string, mode bool, b, a []string, bText, aText string) {
		if bText == aText {
			return
		}
		change := FieldChange{Field: field, Before: bText, After: aText}
		if !mode {
			change.Added, change.Removed, change.Kept = setDiff(b, a)
			if !change.IsListChange() {
				change.Kept = nil
			}
		}
		fields = append(fields, change)
	}
	addList(FieldPermissions, false, before.Permissions, after.Permissions,
		list(before.Permissions), list(after.Permissions))
	addList(FieldTargets, false, before.Targets, after.Targets, list(before.Targets), list(after.Targets))
	addList(FieldTools, (before.Tools == nil) != (after.Tools == nil), before.Tools, after.Tools,
		tools(before.Tools), tools(after.Tools))
	addList(FieldPaths, (len(before.Paths) == 0) != (len(after.Paths) == 0), before.Paths, after.Paths,
		PathsText(before.Paths), PathsText(after.Paths))
	beforeFiles, afterFiles := FileItems(before.FilesRead, before.FilesWrite), FileItems(after.FilesRead, after.FilesWrite)
	addList(FieldFiles, (len(beforeFiles) == 0) != (len(afterFiles) == 0), beforeFiles, afterFiles,
		FilesText(before.FilesRead, before.FilesWrite), FilesText(after.FilesRead, after.FilesWrite))
	addList(FieldForward, false, ForwardItems(before.Forward), ForwardItems(after.Forward),
		ForwardText(before.Forward), ForwardText(after.Forward))
	return fields
}

// setDiff splits two lists into what only after holds, what only before holds, and what both hold, each
// sorted and without duplicates.
func setDiff(before, after []string) (added, removed, kept []string) {
	inBefore := make(map[string]bool, len(before))
	for _, value := range before {
		inBefore[value] = true
	}
	inAfter := make(map[string]bool, len(after))
	for _, value := range after {
		inAfter[value] = true
	}
	for value := range inAfter {
		if inBefore[value] {
			kept = append(kept, value)
		} else {
			added = append(added, value)
		}
	}
	for value := range inBefore {
		if !inAfter[value] {
			removed = append(removed, value)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(kept)
	return added, removed, kept
}

// FileItems is the local directories a connection releases as one item each, "read: dir" or "write: dir".
func FileItems(read, write []string) []string {
	items := make([]string, 0, len(read)+len(write))
	for _, dir := range read {
		items = append(items, "read: "+dir)
	}
	for _, dir := range write {
		items = append(items, "write: "+dir)
	}
	return items
}

// ForwardItems is the forward credentials a connection releases as one item each: the name with the field
// names it releases, never a value.
func ForwardItems(forward []vault.ForwardSecret) []string {
	items := make([]string, len(forward))
	for i, entry := range forward {
		items[i] = entry.Name + " (fields: " + strings.Join(entry.Fields, " ") + ")"
	}
	return items
}

func list(values []string) string {
	if len(values) == 0 {
		return "(none)"
	}
	return strings.Join(values, " ")
}

// PathsText is a paths list as a person reads it.
func PathsText(values []string) string {
	if len(values) == 0 {
		return "(every project)"
	}
	return list(values)
}

// FilesText is the local directories a connection releases as a person reads them, per direction.
func FilesText(read, write []string) string {
	if len(read) == 0 && len(write) == 0 {
		return "(no local files)"
	}
	var parts []string
	if len(read) > 0 {
		parts = append(parts, "read: "+list(read))
	}
	if len(write) > 0 {
		parts = append(parts, "write: "+list(write))
	}
	return strings.Join(parts, "; ")
}

// ForwardText is the forward credentials a connection releases as a person reads them: each name with the
// field names it releases, never a value.
func ForwardText(forward []vault.ForwardSecret) string {
	if len(forward) == 0 {
		return "(none)"
	}
	return strings.Join(ForwardItems(forward), "; ")
}

func tools(values []string) string {
	if values == nil {
		return "(every tool the permissions allow)"
	}
	return list(values)
}

// DirectApprovable reports whether change - a connection's own open change, found right after some action
// that saved it directly by name (internal/tui's own Connections section, or a guided setup, terminal or
// browser, that just created it) - is made entirely of fields that action's own form shows: permissions,
// targets, tools, or a plain rename of its credential, never one "stored anew" (the vault holds a different
// entry under the same credential name), which a form cannot tell apart from an ordinary rename. A
// connection that was never approved at all (change.New) counts the same way: nothing in the form hid that
// either. Anything else - a changed provider, a changed origin (a service's base_url), or a credential
// replaced under the same name - left it open outside the fields the form shows, so saving unrelated fields
// must never approve it in passing.
//
// This is the one rule every caller that saves a connection directly by name uses to decide whether that
// save may approve it on its own; nothing else decides that on its own.
func DirectApprovable(change Change) bool {
	if change.New {
		return true
	}
	for _, f := range change.Fields {
		switch f.Field {
		case FieldPermissions, FieldTargets, FieldTools:
		case FieldCredential:
			if strings.HasSuffix(f.After, " (stored anew)") {
				return false
			}
		default:
			return false
		}
	}
	return true
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

// VaultScopes returns the scope of every connection of cfg that reads a credential of type vault, as it is
// configured now and sorted by name: what an approval with an agent token is decided on (see
// vault.Vault.ApproveWithToken), whether or not the vault holds an entry for its credential yet.
func VaultScopes(cfg *config.Config) ([]vault.Scope, error) {
	if cfg == nil {
		return nil, nil
	}
	var scopes []vault.Scope
	for _, name := range sortedKeys(cfg.Connections) {
		if cfg.Credentials[cfg.Connections[name].Credential].Type != config.CredentialTypeVault {
			continue
		}
		resolved, err := cfg.Resolve(name, "")
		if err != nil {
			return nil, err
		}
		scopes = append(scopes, secret.ScopeOf(resolved))
	}
	return scopes, nil
}

// LapsedModels names the vorbild connections of token that cover nothing now, sorted: one that is no
// connection of cfg reading a vault credential any more, because it was renamed, deleted, or switched away
// from the vault; one the vault unlocked in this process holds no approval for; and one whose approval was
// given for a credential entry since removed or stored anew. It is the same rule the vault applies when a
// token approves (see vault.Vault.ApproveWithToken), so 'qatlas vault token list' and 'show' warn about
// exactly the vorbilder an approval would skip.
func LapsedModels(cfg *config.Config, v *vault.Vault, token vault.Token) ([]string, error) {
	approvals, err := v.Approvals()
	if err != nil {
		return nil, err
	}
	var lapsed []string
	for _, name := range token.Models {
		connection, ok := cfgConnection(cfg, name)
		approved, isApproved := approvals[name]
		if !ok || cfg.Credentials[connection.Credential].Type != config.CredentialTypeVault || !isApproved {
			lapsed = append(lapsed, name)
			continue
		}
		id, held, err := v.CredentialID(approved.Scope.Credential)
		if err != nil {
			return nil, err
		}
		if !held || id != approved.CredentialID {
			lapsed = append(lapsed, name)
		}
	}
	sort.Strings(lapsed)
	return lapsed, nil
}
