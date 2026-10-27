// Package approval decides which connections an encrypted vault hands a vault credential's secret to. A
// connection is approved for its scope as it is configured at that moment (see vault.Scope); any later
// change of its endpoint, provider, effects, targets, tools list, paths, files, forward credentials with
// their fields, state, or credential entry leaves it
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
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// The fields of a scope, as a FieldChange names them.
const (
	FieldCredential  = "credential"
	FieldProvider    = "provider"
	FieldOrigin      = "origin"
	FieldEffects     = "effects"
	FieldTargets     = "targets"
	FieldExclude     = "exclude"
	FieldTools       = "tools"
	FieldPaths       = "paths"
	FieldFiles       = "files"
	FieldForward     = "forward"
	FieldState       = "state"
	FieldActiveUntil = "active until"
)

// The two states a FieldState change reads between.
const (
	stateActive   = "active"
	stateDisabled = "disabled"
)

// ActiveUntilText is how a person reads a connection's active_until limit: the instant in local time, or
// unlimited without one. A value that does not read stays as written.
func ActiveUntilText(limit string) string {
	if limit == "" {
		return "unlimited"
	}
	at, err := time.Parse(time.RFC3339, limit)
	if err != nil {
		return limit
	}
	return at.Local().Format("2006-01-02 15:04:05 MST")
}

// StateText is how a person reads whether a connection is switched off.
func StateText(disabled bool) string {
	if disabled {
		return stateDisabled
	}
	return stateActive
}

// OriginText is the origin (base URL) of a scope as a person may read it: credentials in it are masked.
func OriginText(origin string) string {
	return config.MaskURL(origin)
}

// FieldChange is one field of a connection's scope that differs from what was approved, each side written as
// a person reads it. It never holds a secret.
//
// A list field (effects, targets, tools, paths, files, forward) that stays a list on both sides also
// carries what the change means: Added is what is newly asked for, Removed what falls away, and Kept what
// stays, each sorted. A change between a mode and a list - tools "(every tool of its effects)" and a
// list, paths "(every project)" and a list, files "(no local files)" and a list - and every single-value
// field carries only Before and After; IsListChange tells the two apart. Before and After are set in both
// cases.
//
// Renamed marks what a provider's rename tables explain: entries of a list that the provider renamed
// (never counted in Added, Removed, or Kept), and, with IsRename, a single value that is the provider's
// former ID. It changes how a person reads the change, not whether the connection is open or what approving
// it releases.
type FieldChange struct {
	Field   string
	Before  string
	After   string
	Added   []string
	Removed []string
	Kept    []string
	Renamed []Rename
	// IsRename is true when a single-value field changed from a former ID to the current one.
	IsRename bool
	// CredentialsChanged is true when the origin differs only in the parts a display masks (user info, secret
	// query values): Before and After are then equal as shown, and a person is told that credentials changed.
	CredentialsChanged bool
}

// Rename is one list entry that a provider renamed: Old was approved, New is configured now.
type Rename struct {
	Old string
	New string
}

// IsListChange reports whether the change is told by Added, Removed, Renamed, and Kept rather than by
// Before and After alone.
func (f FieldChange) IsListChange() bool {
	return len(f.Added) > 0 || len(f.Removed) > 0 || len(f.Renamed) > 0
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
	// Leftovers are the stale approvals a rename that did not finish left behind, sorted by approval.
	Leftovers []Leftover
	// HandRenames are the stale approvals of connections renamed by hand in the configuration, sorted by
	// approval. Like the leftovers they are among Stale.
	HandRenames []HandRename
}

// RevocableStale are the stale approvals that approving everything may remove: all but the hand renames, which
// stay so that adopting the rename still finds them.
func (r Report) RevocableStale() []string {
	byHand := make(map[string]bool, len(r.HandRenames))
	for _, h := range r.HandRenames {
		byHand[h.Approval] = true
	}
	var out []string
	for _, name := range r.Stale {
		if !byHand[name] {
			out = append(out, name)
		}
	}
	return out
}

// Leftover is a stale approval that records the config id of a connection that exists under another name,
// while the vault holds an approval under that name too: what remains of renaming that connection, from or
// to the name of the approval, when the rename stopped between the vault and the configuration. Renaming the
// connection to Approval, or revoking the approval, clears it; neither releases anything the vault had not
// approved.
type Leftover struct {
	Approval   string
	Connection string
}

// HandRename is a stale approval under a name no connection has, that belongs to the connection Connection,
// renamed by hand in the configuration: the vault holds no approval under Connection, and the approval either
// records Connection's config id or, recording none, was given for the very credential Connection reads, and
// no other connection could be it (see HandRenames). Adopting the rename carries it over as it is, valid or
// open; nothing carries it on its own, since the configuration is no trust anchor.
//
// Ambiguous says the approval could be that of more than one connection, or its connection that of more than
// one approval; Connection is "" then, and nothing is suggested.
type HandRename struct {
	Approval   string
	Connection string
	Ambiguous  bool
}

// approvalRef is what telling a connection's hand rename needs of one approval: the config id of the
// connection and the name of the credential it was given for.
type approvalRef struct{ connectionID, credential string }

// HandRenames finds the HandRename of every approval names lists, sorted by approval: the names of an
// unencrypted vault or of one unlocked in this process (vault.Vault.KnownNames), or what a vault process that
// holds it unlocked tells. It reads no secret and writes nothing.
func HandRenames(cfg *config.Config, names vault.Names) []HandRename {
	refs := make(map[string]approvalRef, len(names.Approvals))
	for name, id := range names.Approvals {
		refs[name] = approvalRef{connectionID: id, credential: names.ApprovalCredentials[name]}
	}
	return handRenames(cfg, refs)
}

// handRenames finds the hand renames among approvals, by connection name. An approval that records a config
// id belongs to the connection with that id, if one other than itself has it and has no approval of its
// own; one id claimed by more than one such approval is ambiguous. An approval without an id is matched by
// its credential only against the connections that read a vault credential, have no approval and are not
// claimed by an id: exactly one candidate, matched by no other such approval, is a hand rename; any other
// count but none is ambiguous.
func handRenames(cfg *config.Config, approvals map[string]approvalRef) []HandRename {
	if cfg == nil {
		return nil
	}
	claimed := map[string]bool{}
	byOwner := map[string][]string{}
	var unrecorded []string
	for name, ref := range approvals {
		if ref.connectionID != "" {
			claimed[ref.connectionID] = true
		}
		if _, configured := cfg.Connections[name]; configured {
			continue
		}
		if ref.connectionID == "" {
			unrecorded = append(unrecorded, name)
			continue
		}
		owner := connectionWithID(cfg, ref.connectionID)
		if _, held := approvals[owner]; owner != "" && !held {
			byOwner[owner] = append(byOwner[owner], name)
		}
	}
	var out []HandRename
	for owner, names := range byOwner {
		for _, name := range names {
			if len(names) == 1 {
				out = append(out, HandRename{Approval: name, Connection: owner})
			} else {
				out = append(out, HandRename{Approval: name, Ambiguous: true})
			}
		}
	}
	candidates := map[string][]string{}
	matches := map[string]int{}
	for _, name := range unrecorded {
		credential := approvals[name].credential
		if credential == "" || cfg.Credentials[credential].Type != config.CredentialTypeVault {
			continue
		}
		for connection, entry := range cfg.Connections {
			if _, held := approvals[connection]; held || entry.Credential != credential || claimed[entry.ID] {
				continue
			}
			candidates[name] = append(candidates[name], connection)
			matches[connection]++
		}
	}
	for name, found := range candidates {
		if len(found) == 1 && matches[found[0]] == 1 {
			out = append(out, HandRename{Approval: name, Connection: found[0]})
		} else {
			out = append(out, HandRename{Approval: name, Ambiguous: true})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Approval < out[j].Approval })
	return out
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
				Fields: diff(approved, c.scope, c.id, cfg.ProviderCatalog()), Approved: approved.Approved,
			})
		}
	}
	refs := make(map[string]approvalRef, len(approvals))
	for name, approved := range approvals {
		refs[name] = approvalRef{connectionID: approved.ConnectionID, credential: approved.Scope.Credential}
	}
	report.HandRenames = handRenames(cfg, refs)
	byHand := map[string]bool{}
	for _, r := range report.HandRenames {
		byHand[r.Approval] = true
	}
	for _, name := range sortedKeys(approvals) {
		if connection, ok := cfgConnection(cfg, name); !ok ||
			cfg.Credentials[connection.Credential].Type != config.CredentialTypeVault {
			report.Stale = append(report.Stale, name)
			owner := connectionWithID(cfg, approvals[name].ConnectionID)
			if owner != "" && owner != name && !byHand[name] {
				report.Leftovers = append(report.Leftovers, Leftover{Approval: name, Connection: owner})
			}
		}
	}
	return report, nil
}

// connectionWithID is the name of the connection of cfg whose config id is id, "" for none or an empty id.
func connectionWithID(cfg *config.Config, id string) string {
	if cfg == nil || id == "" {
		return ""
	}
	for name, connection := range cfg.Connections {
		if connection.ID == id {
			return name
		}
	}
	return ""
}

func cfgConnection(cfg *config.Config, name string) (config.Connection, bool) {
	if cfg == nil {
		return config.Connection{}, false
	}
	connection, ok := cfg.Connections[name]
	return connection, ok
}

// diff lists the fields of now that differ from what approved was given for, in a fixed order. catalog
// supplies the rename tables that let a renamed provider or tool read as a rename; nil knows none.
func diff(approved vault.Approval, now vault.Scope, id string, catalog config.ProviderCatalog) []FieldChange {
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
	providerRenamed := false
	if before.Provider != after.Provider {
		providerRenamed = formerProvider(catalog, after.Provider, before.Provider)
		fields = append(fields, FieldChange{Field: FieldProvider, Before: before.Provider, After: after.Provider,
			IsRename: providerRenamed})
	}
	if before.Origin != after.Origin {
		// The comparison runs on the real values, since a changed password must be approved; only what is shown
		// is masked.
		b, a := OriginText(before.Origin), OriginText(after.Origin)
		fields = append(fields, FieldChange{Field: FieldOrigin, Before: b, After: a, CredentialsChanged: b == a})
	}
	// addList records a change of a list field. mode is true when one side is a mode rather than a list, which
	// stays readable only as before and after, as does a list whose text changed without a different entry.
	addList := func(field string, mode bool, b, a []string, bText, aText string, renamed ...Rename) {
		if bText == aText {
			return
		}
		change := FieldChange{Field: field, Before: bText, After: aText}
		if !mode {
			if len(renamed) > 0 {
				change.Renamed = renamed
				b, a = withoutRenamed(b, a, renamed)
			}
			change.Added, change.Removed, change.Kept = setDiff(b, a)
			if !change.IsListChange() {
				change.Kept = nil
			}
		}
		fields = append(fields, change)
	}
	addList(FieldEffects, false, before.Effects, after.Effects,
		list(before.Effects), list(after.Effects))
	addList(FieldTargets, false, before.Targets, after.Targets, list(before.Targets), list(after.Targets))
	addList(FieldExclude, false, before.Exclude, after.Exclude, list(before.Exclude), list(after.Exclude))
	var renamedTools []Rename
	if before.Tools != nil && after.Tools != nil && (before.Provider == after.Provider || providerRenamed) {
		renamedTools = renamedEntries(catalog, after.Provider, before.Tools, after.Tools)
	}
	addList(FieldTools, (before.Tools == nil) != (after.Tools == nil), before.Tools, after.Tools,
		tools(before.Tools), tools(after.Tools), renamedTools...)
	addList(FieldPaths, (len(before.Paths) == 0) != (len(after.Paths) == 0), before.Paths, after.Paths,
		PathsText(before.Paths), PathsText(after.Paths))
	beforeFiles, afterFiles := FileItems(before.FilesRead, before.FilesWrite), FileItems(after.FilesRead, after.FilesWrite)
	addList(FieldFiles, (len(beforeFiles) == 0) != (len(afterFiles) == 0), beforeFiles, afterFiles,
		FilesText(before.FilesRead, before.FilesWrite), FilesText(after.FilesRead, after.FilesWrite))
	addList(FieldForward, false, ForwardItems(before.Forward), ForwardItems(after.Forward),
		ForwardText(before.Forward), ForwardText(after.Forward))
	add(FieldState, StateText(before.Disabled), StateText(after.Disabled))
	add(FieldActiveUntil, ActiveUntilText(before.ActiveUntil), ActiveUntilText(after.ActiveUntil))
	return fields
}

// formerProvider reports whether the provider current lists former among its earlier IDs.
func formerProvider(catalog config.ProviderCatalog, current, former string) bool {
	if catalog == nil || current == former {
		return false
	}
	metadata, ok := catalog.ProviderMetadata(current)
	return ok && slices.Contains(metadata.FormerIDs, former)
}

// renamedEntries pairs each approved tool that the current provider's rename table maps to a tool configured
// now, and that the approval did not already hold, sorted by the old ID.
func renamedEntries(catalog config.ProviderCatalog, provider string, before, after []string) []Rename {
	if catalog == nil {
		return nil
	}
	metadata, ok := catalog.ProviderMetadata(provider)
	if !ok {
		return nil
	}
	var out []Rename
	for _, old := range before {
		current, mapped := metadata.RenamedTools[old]
		if mapped && slices.Contains(after, current) && !slices.Contains(before, current) &&
			!slices.ContainsFunc(out, func(r Rename) bool { return r.Old == old || r.New == current }) {
			out = append(out, Rename{Old: old, New: current})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Old < out[j].Old })
	return out
}

// withoutRenamed drops the renamed entries from both lists, so they are neither added, removed, nor kept.
func withoutRenamed(before, after []string, renamed []Rename) (b, a []string) {
	for _, value := range before {
		if !slices.ContainsFunc(renamed, func(r Rename) bool { return r.Old == value }) {
			b = append(b, value)
		}
	}
	for _, value := range after {
		if !slices.ContainsFunc(renamed, func(r Rename) bool { return r.New == value }) {
			a = append(a, value)
		}
	}
	return b, a
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
		return "(every tool of its effects)"
	}
	return list(values)
}

// DirectApprovable reports whether change - a connection's own open change, found right after some action
// that saved it directly by name (internal/tui's own Connections section, or a guided setup, terminal or
// browser, that just created it) - is made entirely of fields that action's own form shows: effects,
// targets, tools, its state, its active-until limit, or a plain rename of its credential, never one "stored
// anew" (the vault holds a different entry under the same credential name), which a form cannot tell apart
// from an ordinary rename. A connection that was never approved at all (change.New) counts the same way: nothing in the form hid that
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
		case FieldEffects, FieldTargets, FieldTools, FieldState, FieldActiveUntil:
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
	if err := v.ApproveConnections(scopes, ConnectionIDs(cfg)); err != nil {
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
	err = vaultproc.SyncChange(ctx, v, func(ctx context.Context, client *vaultproc.Client) error {
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

// ConnectionIDs returns the config id of every connection of cfg that has one, by name.
func ConnectionIDs(cfg *config.Config) map[string]string {
	ids := map[string]string{}
	if cfg == nil {
		return ids
	}
	for name, connection := range cfg.Connections {
		if connection.ID != "" {
			ids[name] = connection.ID
		}
	}
	return ids
}

// LapsedModels names the vorbild connections of token that cover nothing now, sorted: one that is no
// connection of cfg reading a vault credential any more, because it was renamed, deleted, or switched away
// from the vault; one the vault unlocked in this process holds no approval for; and one whose approval was
// given for a credential entry since removed or stored anew; and one that is disabled or past its active_until
// at now, as approved or as configured now. It is the same rule the vault applies when a token approves (see
// vault.Vault.ApproveWithToken), so 'qatlas vault token list' and 'show' warn about exactly the vorbilder
// an approval would skip.
func LapsedModels(cfg *config.Config, v *vault.Vault, token vault.Token, now time.Time) ([]string, error) {
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
		if !held || id != approved.CredentialID || approved.Scope.Disabled || approved.Scope.PastLimit(now) ||
			connection.Off(now) {
			lapsed = append(lapsed, name)
		}
	}
	sort.Strings(lapsed)
	return lapsed, nil
}
