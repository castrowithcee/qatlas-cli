package vault

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// pendingPayload is what a pending file decrypts to: the roles a locked Set added or changed for one
// credential name. It carries no entry id: the id a merge assigns, or reuses, is decided by matching Name
// against the vault's document, which is unreadable while the vault that wrote the pending file was locked.
// CredentialID is the config id of the credential and is merged like the roles.
type pendingPayload struct {
	Name         string            `json:"name"`
	CredentialID string            `json:"credential_id,omitempty"`
	Roles        map[string]string `json:"roles"`
	Modified     time.Time         `json:"modified"`
}

// pendingFiles returns the *.age file names under pending, sorted, so a merge is deterministic. A missing
// pending directory holds none.
func (v *Vault) pendingFiles() ([]string, error) {
	entries, err := os.ReadDir(v.pendingPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read %s: %w", v.pendingPath(), err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".age") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// addPending writes roles as a pending entry, encrypted to the vault's public recipient alone, so it needs no
// passphrase. It is how Set stores a new or changed secret while the vault is locked; without roles it
// carries only the credential id.
func (v *Vault) addPending(name, credentialID string, roles map[string]string) (path string, err error) {
	recipientPEM, err := readFile(v.recipientPath())
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", v.recipientPath(), err)
	}
	payload := pendingPayload{Name: name, CredentialID: credentialID, Roles: roles, Modified: time.Now().UTC()}
	plain, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("cannot encode the pending entry for %s: %w", name, err)
	}
	data, err := encryptToRecipient(string(plain), recipientPEM)
	if err != nil {
		return "", err
	}
	path = filepath.Join(v.pendingPath(), newID()+".age")
	return path, writeFile(path, data)
}

// mergePending decrypts and applies every pending file to doc, in file name order, and returns how many
// were applied. It never removes a pending file itself: the caller removes them only after the merged
// document was written successfully, so a failed write leaves nothing lost.
func (v *Vault) mergePending(id *identity, doc *document) (int, error) {
	names, err := v.pendingFiles()
	if err != nil {
		return 0, err
	}
	// A payload with only a credential id goes last, so it finds an entry a pending secret creates.
	var idsOnly []pendingPayload
	for _, name := range names {
		payload, _, err := v.readPending(id, name)
		if err != nil {
			return 0, err
		}
		if len(payload.Roles) == 0 {
			idsOnly = append(idsOnly, payload)
			continue
		}
		doc.mergePending(payload)
	}
	for _, payload := range idsOnly {
		doc.mergePending(payload)
	}
	return len(names), nil
}

// readPending decrypts the pending file name and returns its payload and its very bytes.
func (v *Vault) readPending(id *identity, name string) (pendingPayload, []byte, error) {
	path := filepath.Join(v.pendingPath(), name)
	data, err := readFile(path)
	if err != nil {
		return pendingPayload{}, nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	plain, err := decryptFrom(data, id.key)
	if err != nil {
		return pendingPayload{}, nil, fmt.Errorf("cannot decrypt %s: %w", path, err)
	}
	var payload pendingPayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		return pendingPayload{}, nil, fmt.Errorf("%s does not hold a valid pending entry", path)
	}
	return payload, data, nil
}

// removePending deletes the given pending files after they were merged and the document that absorbed them
// was written. A failure here is not returned as fatal: the merge itself already succeeded, and a leftover
// pending file merges again next time, which is a duplicate no-op, not data loss.
func (v *Vault) removePending(names []string) {
	for _, name := range names {
		_ = os.Remove(filepath.Join(v.pendingPath(), name))
	}
}

// mergePending applies one pending payload to the document: an existing entry of the same name gets the
// payload's roles merged in, a name not yet present becomes a new entry. A payload with only a credential id
// never creates an entry and leaves the modification time alone, since no secret changed.
func (d *document) mergePending(p pendingPayload) {
	entry, ok := d.byName(p.Name)
	if !ok {
		if len(p.Roles) == 0 {
			return
		}
		entry = Entry{ID: newID(), Name: p.Name, Roles: map[string]string{}, Created: p.Modified}
	}
	if entry.CredentialID == "" {
		entry.CredentialID = p.CredentialID
	}
	if entry.Roles == nil {
		entry.Roles = map[string]string{}
	}
	for role, value := range p.Roles {
		entry.Roles[role] = value
	}
	if len(p.Roles) > 0 {
		entry.Modified = p.Modified
	}
	d.Entries[entry.ID] = entry
}
