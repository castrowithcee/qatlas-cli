package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ErrEntryTaken reports a credential rename onto a name the vault already holds an entry, or a pending write,
// for.
var ErrEntryTaken = errors.New("the vault holds secrets under the new credential name")

// ScopeRename is the scope of one connection before and after a credential it reads, or releases, is
// renamed; the connection keeps its name. ConnectionID is the config id of the connection, which the approval
// carried over records (see Approval).
type ScopeRename struct {
	Before, After Scope
	ConnectionID  string
}

// CredentialRename is what renaming the credential From to To changes in the vault. Entry renames the vault
// entry of From, a vault credential's, and records CredentialID, its config id, where the entry has none yet.
// Carry are the connections whose approvals follow the rename.
//
// Resume finishes a rename that stopped after the vault: the entry has the name To already and records
// CredentialID, and the vault holds no entry From. Pending writes for From are merged into it under To.
type CredentialRename struct {
	From, To, CredentialID string
	Entry, Resume          bool
	Carry                  []ScopeRename
}

// HasEntry reports whether the vault holds an entry for the credential name. It reads an unencrypted vault and
// one unlocked in this process; a locked one fails with ErrNotUnlocked.
func (v *Vault) HasEntry(name string) (bool, error) {
	state, err := v.State()
	if err != nil {
		return false, err
	}
	switch state {
	case StateUnencrypted:
		doc, err := loadPlainDocument(v.plainPath())
		if err != nil {
			return false, err
		}
		_, ok := doc.byName(name)
		return ok, nil
	case StateLocked:
		return false, ErrNotUnlocked
	case StateUnlocked:
		_, ok := v.doc.byName(name)
		return ok, nil
	}
	return false, nil
}

// EntryCredentialIDs returns the config id each entry of the vault records, by entry name; "" for an entry
// that records none. It reads an unencrypted vault and one unlocked in this process; a locked one fails with
// ErrNotUnlocked, and an absent one holds no entry.
func (v *Vault) EntryCredentialIDs() (map[string]string, error) {
	state, err := v.State()
	if err != nil {
		return nil, err
	}
	doc := newDocument()
	switch state {
	case StateUnencrypted:
		if doc, err = loadPlainDocument(v.plainPath()); err != nil {
			return nil, err
		}
	case StateLocked:
		return nil, ErrNotUnlocked
	case StateUnlocked:
		doc = v.doc
	}
	out := make(map[string]string, len(doc.Entries))
	for _, entry := range doc.Entries {
		out[entry.Name] = entry.CredentialID
	}
	return out, nil
}

// resumable reports whether doc holds what r.Resume expects: the entry To with r.CredentialID and no entry
// From.
func (d *document) resumable(r CredentialRename) bool {
	entry, ok := d.byName(r.To)
	_, from := d.byName(r.From)
	return ok && !from && r.CredentialID != "" && entry.CredentialID == r.CredentialID
}

// PendingWrites counts the pending writes of an encrypted vault unlocked in this process by credential name:
// the writes another process queued while it found the vault locked, which this one merges on its next unlock.
// A locked vault fails with ErrNotUnlocked; any other has none.
func (v *Vault) PendingWrites() (map[string]int, error) {
	state, err := v.State()
	if err != nil {
		return nil, err
	}
	switch state {
	case StateLocked:
		return nil, ErrNotUnlocked
	case StateUnlocked:
	default:
		return map[string]int{}, nil
	}
	names, err := v.pendingFiles()
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, name := range names {
		payload, _, err := v.readPending(v.identity, name)
		if err != nil {
			return nil, err
		}
		counts[payload.Name]++
	}
	return counts, nil
}

// RenameCredential applies r to an unencrypted vault or one unlocked in this process, in one write. The entry
// keeps its id and its secrets; nothing is copied. In an encrypted vault the pending writes for r.From are
// merged into the entry first and removed, and the approval of each connection in r.Carry is replaced by one
// for its After scope, but only where it is valid for its Before scope: an open approval stays as it is. It
// keeps the time it was given. It reports the connections whose approvals it carried, sorted.
//
// An entry or a pending write under r.To fails with ErrEntryTaken, a locked vault with ErrNotUnlocked, and
// nothing is written then. undo puts the vault's files back byte for byte as this call found them, without a
// passphrase; it is nil when err is not.
func (v *Vault) RenameCredential(r CredentialRename) (carried []string, undo func() error, err error) {
	unchanged := func() error { return nil }
	state, err := v.State()
	if err != nil {
		return nil, nil, err
	}
	switch state {
	case StateLocked:
		return nil, nil, ErrNotUnlocked
	case StateUnencrypted:
		original, err := readFile(v.plainPath())
		if err != nil {
			return nil, nil, fmt.Errorf("cannot read %s: %w", v.plainPath(), err)
		}
		doc, err := loadPlainDocument(v.plainPath())
		if err != nil {
			return nil, nil, err
		}
		if r.Resume && !doc.resumable(r) {
			return nil, nil, ErrEntryTaken
		}
		if _, taken := doc.byName(r.To); taken && !r.Resume {
			return nil, nil, ErrEntryTaken
		}
		// An unencrypted vault binds no connection, so there is no approval to carry.
		if r.Resume || !r.Entry || !doc.renameEntry(r.From, r.To, r.CredentialID) {
			return nil, unchanged, nil
		}
		if err := v.writePlain(doc); err != nil {
			return nil, nil, err
		}
		return nil, func() error { return writeFile(v.plainPath(), original) }, nil
	case StateUnlocked:
		return v.renameUnlocked(r)
	}
	return nil, unchanged, nil
}

// heldPending is one pending file a rename merged: its path and its very bytes, for an undo.
type heldPending struct {
	path string
	data []byte
}

func (v *Vault) renameUnlocked(r CredentialRename) ([]string, func() error, error) {
	doc := v.doc
	if r.Resume && !doc.resumable(r) {
		return nil, nil, ErrEntryTaken
	}
	if _, taken := doc.byName(r.To); taken && !r.Resume {
		return nil, nil, ErrEntryTaken
	}
	names, err := v.pendingFiles()
	if err != nil {
		return nil, nil, err
	}
	next := doc.clone()
	var merged []heldPending
	var idsOnly []pendingPayload
	for _, name := range names {
		payload, data, err := v.readPending(v.identity, name)
		if err != nil {
			return nil, nil, err
		}
		if payload.Name == r.To {
			return nil, nil, ErrEntryTaken
		}
		if !r.Entry || payload.Name != r.From {
			continue
		}
		merged = append(merged, heldPending{path: filepath.Join(v.pendingPath(), name), data: data})
		if r.Resume {
			payload.Name = r.To
		}
		// The order an unlock merges in: a payload with only a credential id last.
		if len(payload.Roles) == 0 {
			idsOnly = append(idsOnly, payload)
			continue
		}
		next.mergePending(payload)
	}
	for _, payload := range idsOnly {
		next.mergePending(payload)
	}

	before := next.bindings().IDs
	renamed := r.Entry && next.renameEntry(r.From, r.To, r.CredentialID)
	after := next.bindings().IDs
	var carried []string
	for _, c := range r.Carry {
		approval, ok := next.Approvals[c.Before.Connection]
		own := before[c.Before.Credential]
		if r.Resume && c.Before.Credential == r.From {
			own = before[r.To]
		}
		if !ok || own == "" || approval.Fingerprint != Fingerprint(c.Before, own) || after[c.After.Credential] == "" {
			continue
		}
		id := after[c.After.Credential]
		next.Approvals[c.After.Connection] = Approval{
			Fingerprint: Fingerprint(c.After, id), Scope: c.After.Normalized(), CredentialID: id,
			Approved: approval.Approved, ConnectionID: c.ConnectionID,
		}
		carried = append(carried, c.After.Connection)
	}
	sort.Strings(carried)
	if !renamed && len(merged) == 0 && len(carried) == 0 {
		return nil, func() error { return nil }, nil
	}

	original, err := readFile(v.secretsPath())
	existed := err == nil
	if err != nil && !isNotExist(err) {
		return nil, nil, fmt.Errorf("cannot read %s: %w", v.secretsPath(), err)
	}
	undo := func() error {
		var failed error
		if existed {
			failed = writeFile(v.secretsPath(), original)
		} else if err := os.Remove(v.secretsPath()); err != nil && !isNotExist(err) {
			failed = err
		}
		for _, p := range merged {
			if err := writeFile(p.path, p.data); err != nil && failed == nil {
				failed = err
			}
		}
		v.doc = doc
		return failed
	}
	if err := v.writeEncrypted(next); err != nil {
		return nil, nil, err
	}
	v.doc = next
	// A pending write left for the old name would bring its secrets back under that name on the next unlock.
	for _, p := range merged {
		if err := os.Remove(p.path); err != nil && !isNotExist(err) {
			if undoErr := undo(); undoErr != nil {
				return nil, nil, fmt.Errorf("cannot remove %s: %w; the vault could not be put back as it was: %v",
					p.path, err, undoErr)
			}
			return nil, nil, fmt.Errorf("cannot remove %s: %w", p.path, err)
		}
	}
	return carried, undo, nil
}

// clone returns a copy of d that shares no map with it.
func (d *document) clone() *document {
	out := &document{Schema: d.Schema, Entries: make(map[string]Entry, len(d.Entries))}
	for id, entry := range d.Entries {
		roles := make(map[string]string, len(entry.Roles))
		for role, value := range entry.Roles {
			roles[role] = value
		}
		entry.Roles = roles
		out.Entries[id] = entry
	}
	if d.Approvals != nil {
		out.Approvals = make(map[string]Approval, len(d.Approvals))
		for name, approval := range d.Approvals {
			out.Approvals[name] = approval
		}
	}
	return out
}

// renameEntry gives the entry named from the name to, keeping its id, its secrets and its times, and records
// credentialID where it has none. It reports whether there was such an entry.
func (d *document) renameEntry(from, to, credentialID string) bool {
	entry, ok := d.byName(from)
	if !ok {
		return false
	}
	entry.Name = to
	if entry.CredentialID == "" {
		entry.CredentialID = credentialID
	}
	d.Entries[entry.ID] = entry
	return true
}
