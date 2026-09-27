package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// ErrApprovalRequired reports a connection whose current scope the encrypted vault has not approved for the
// credential it reads: it was never approved, or something security relevant about it changed since, its
// endpoint, its permissions, its targets, its tools list, or the credential entry itself. The secret is not
// handed out until a person approves the connection as it is now.
var ErrApprovalRequired = errors.New("the connection is not approved to read its vault credential")

// Scope is the security relevant cut of one connection, the part an approval is given for. It is built from
// the configuration, which the vault does not trust: an approval only holds as long as the scope a caller
// presents is exactly the one a person approved, together with the credential entry that existed then. It
// never holds a secret.
type Scope struct {
	// Connection is the name of the connection; approvals are kept per connection.
	Connection string `json:"connection"`
	// Credential is the name of the vault credential the connection reads.
	Credential string `json:"credential"`
	// Provider is the provider the connection's service belongs to.
	Provider string `json:"provider"`
	// Origin is the effective endpoint of the connection's service, its base URL or its provider's default.
	Origin string `json:"origin"`
	// Permissions are the effects the connection permits, the provider's defaults applied.
	Permissions []string `json:"permissions"`
	// Targets are the connection's target or targets.
	Targets []string `json:"targets"`
	// Tools is the connection's tools list. Nil means it has none, so every tool its permissions admit is
	// offered; an empty list offers none. The two are different scopes.
	Tools []string `json:"tools"`
}

// Normalized returns s with every list whose order carries no meaning sorted, and an absent permissions or
// targets list made empty, since both mean the same. Tools keeps nil apart from empty.
func (s Scope) Normalized() Scope {
	sorted := func(values []string) []string {
		out := append(make([]string, 0, len(values)), values...)
		slices.Sort(out)
		return out
	}
	s.Permissions = sorted(s.Permissions)
	s.Targets = sorted(s.Targets)
	if s.Tools != nil {
		s.Tools = sorted(s.Tools)
	}
	return s
}

// fingerprintLabel separates a connection fingerprint from any other use of the same hash.
const fingerprintLabel = "qatlas connection approval v1\x00"

// Fingerprint returns the fingerprint of scope for the credential entry credentialID, the random id the vault
// keeps for that entry: a credential deleted and stored again under the same name gets a new id, and with it
// a new fingerprint. It is deterministic: lists whose order does not matter are sorted first. The connection
// name is not part of it; approvals are looked up by that name instead.
//
// The canonical form is a JSON object with a fixed field order. A field added later is added with omitempty,
// so every connection that leaves it empty keeps the fingerprint it was approved with.
func Fingerprint(scope Scope, credentialID string) string {
	n := scope.Normalized()
	canonical := struct {
		CredentialID string   `json:"credential_id"`
		Credential   string   `json:"credential"`
		Provider     string   `json:"provider"`
		Origin       string   `json:"origin"`
		Permissions  []string `json:"permissions"`
		Targets      []string `json:"targets"`
		Tools        []string `json:"tools"`
	}{credentialID, n.Credential, n.Provider, n.Origin, n.Permissions, n.Targets, n.Tools}
	data, err := json.Marshal(canonical)
	if err != nil {
		// Marshalling strings and string slices cannot fail.
		panic("vault: cannot encode a connection scope: " + err.Error())
	}
	sum := sha256.Sum256(append([]byte(fingerprintLabel), data...))
	return hex.EncodeToString(sum[:])
}

// Approval is what the vault keeps for one approved connection: the fingerprint an access must match, and the
// scope and the credential entry it was given for, so a change can be shown field by field. It never holds a
// secret.
type Approval struct {
	Fingerprint  string    `json:"fingerprint"`
	Scope        Scope     `json:"scope"`
	CredentialID string    `json:"credential_id"`
	Approved     time.Time `json:"approved"`
}

// Bindings is what a vault process needs to check an access without the document: the entry id of every
// credential, by name, and the approved fingerprint of every connection, by name. It holds no secret.
type Bindings struct {
	IDs       map[string]string `json:"ids"`
	Approvals map[string]string `json:"approvals"`
}

// Allows reports whether scope may read credential: the credential is the one scope names, the vault holds
// an entry for it, and the connection's approval matches the scope and that entry. It is the one check the
// vault process and a vault unlocked in this process both make.
func (b Bindings) Allows(scope Scope, credential string) bool {
	id := b.IDs[credential]
	approved := b.Approvals[scope.Connection]
	return scope.Credential == credential && id != "" && approved != "" && approved == Fingerprint(scope, id)
}

// bindings derives the Bindings of a document.
func (d *document) bindings() Bindings {
	b := Bindings{IDs: map[string]string{}, Approvals: map[string]string{}}
	for _, entry := range d.Entries {
		b.IDs[entry.Name] = entry.ID
	}
	for name, approval := range d.Approvals {
		b.Approvals[name] = approval.Fingerprint
	}
	return b
}

// unlockedDocument returns the document of an encrypted vault unlocked in this process. Approvals exist only
// there: an unencrypted vault binds no connection, and a locked one cannot be read.
func (v *Vault) unlockedDocument() (*document, error) {
	if v.unlocked && v.doc != nil && v.identity != nil {
		return v.doc, nil
	}
	state, err := v.State()
	if err != nil {
		return nil, err
	}
	if state == StateLocked {
		return nil, ErrNotUnlocked
	}
	return nil, ErrNotEncrypted
}

// Bindings returns the bindings of the vault unlocked in this process, for a vault process to check accesses
// with.
func (v *Vault) Bindings() (Bindings, error) {
	doc, err := v.unlockedDocument()
	if err != nil {
		return Bindings{}, err
	}
	return doc.bindings(), nil
}

// Approvals returns a copy of every approval the vault unlocked in this process holds, by connection name.
func (v *Vault) Approvals() (map[string]Approval, error) {
	doc, err := v.unlockedDocument()
	if err != nil {
		return nil, err
	}
	out := make(map[string]Approval, len(doc.Approvals))
	for name, approval := range doc.Approvals {
		out[name] = approval
	}
	return out, nil
}

// CredentialID returns the entry id of credential in the vault unlocked in this process, and whether the
// vault holds an entry for it at all.
func (v *Vault) CredentialID(credential string) (string, bool, error) {
	doc, err := v.unlockedDocument()
	if err != nil {
		return "", false, err
	}
	entry, ok := doc.byName(credential)
	return entry.ID, ok && entry.ID != "", nil
}

// CheckApproval reports whether scope may read its credential from the vault unlocked in this process: nil
// when it may, or when the vault holds no entry for the credential at all, so there is nothing to hand out;
// ErrApprovalRequired otherwise. It fails with ErrNotUnlocked or ErrNotEncrypted where there are no approvals
// to check against.
func (v *Vault) CheckApproval(scope Scope) error {
	doc, err := v.unlockedDocument()
	if err != nil {
		return err
	}
	if _, ok := doc.byName(scope.Credential); !ok {
		return nil
	}
	if !doc.bindings().Allows(scope, scope.Credential) {
		return ErrApprovalRequired
	}
	return nil
}

// Approve records scopes as approved, each for the credential entry its credential names now, and writes the
// vault. It needs the vault unlocked in this process. A scope whose credential the vault holds no entry for is
// refused, and nothing is written then: there is no secret to approve access to yet.
func (v *Vault) Approve(scopes []Scope) error {
	doc, err := v.unlockedDocument()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	approvals := make(map[string]Approval, len(scopes))
	for _, scope := range scopes {
		entry, ok := doc.byName(scope.Credential)
		if !ok || entry.ID == "" {
			return fmt.Errorf("connection %s cannot be approved: the vault holds no secret of credential %s",
				scope.Connection, scope.Credential)
		}
		approvals[scope.Connection] = Approval{
			Fingerprint: Fingerprint(scope, entry.ID), Scope: scope.Normalized(), CredentialID: entry.ID,
			Approved: now,
		}
	}
	if len(approvals) == 0 {
		return nil
	}
	next := make(map[string]Approval, len(doc.Approvals)+len(approvals))
	for name, approval := range doc.Approvals {
		next[name] = approval
	}
	for name, approval := range approvals {
		next[name] = approval
	}
	return v.writeApprovals(doc, next)
}

// Revoke removes the approvals of the named connections, for a connection that was deleted or renamed, and
// writes the vault when one was there. It reports how many it removed and needs the vault unlocked in this
// process.
func (v *Vault) Revoke(connections []string) (int, error) {
	doc, err := v.unlockedDocument()
	if err != nil {
		return 0, err
	}
	next := make(map[string]Approval, len(doc.Approvals))
	for name, approval := range doc.Approvals {
		next[name] = approval
	}
	removed := 0
	for _, name := range connections {
		if _, ok := next[name]; ok {
			delete(next, name)
			removed++
		}
	}
	if removed == 0 {
		return 0, nil
	}
	return removed, v.writeApprovals(doc, next)
}

// writeApprovals writes doc with approvals in place of its own and adopts them only once the write
// succeeded, so a failed write leaves this process with what the vault on disk still holds.
func (v *Vault) writeApprovals(doc *document, approvals map[string]Approval) error {
	next := *doc
	next.Approvals = approvals
	if err := v.writeEncrypted(&next); err != nil {
		return err
	}
	doc.Approvals = approvals
	return nil
}
