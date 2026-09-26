package vault

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Entry is one credential's secrets in the vault. Id is random and fixed for the life of the entry, so a
// pending write can be merged into the entry it belongs to by name even though the id was chosen without
// seeing the vault's content. Name is the credential name it belongs to; Roles holds one secret per role.
// A vault entry carries no configuration value beyond the name: everything else a credential needs, such
// as its provider or its type, stays in config.yaml.
type Entry struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Roles    map[string]string `json:"roles"`
	Created  time.Time         `json:"created"`
	Modified time.Time         `json:"modified"`
}

// document is the JSON shape held by secrets.json and, encrypted, by secrets.age.
type document struct {
	Schema  int              `json:"schema"`
	Entries map[string]Entry `json:"entries,omitempty"`
}

func newDocument() *document {
	return &document{Schema: schemaVersion, Entries: map[string]Entry{}}
}

// byName returns the entry of one credential and whether it exists.
func (d *document) byName(name string) (Entry, bool) {
	for _, entry := range d.Entries {
		if entry.Name == name {
			return entry, true
		}
	}
	return Entry{}, false
}

// setRole stores one role's secret under the entry named name, creating the entry when it does not exist
// yet. now is passed in so a test can make the metadata deterministic.
func (d *document) setRole(name, role, value string, now time.Time) {
	entry, ok := d.byName(name)
	if !ok {
		entry = Entry{ID: newID(), Name: name, Roles: map[string]string{}, Created: now}
	}
	if entry.Roles == nil {
		entry.Roles = map[string]string{}
	}
	entry.Roles[role] = value
	entry.Modified = now
	d.Entries[entry.ID] = entry
}

// deleteRole removes one role from the entry named name and reports whether it was there. An entry left
// without any role is removed entirely, so an empty entry never lingers in the document.
func (d *document) deleteRole(name, role string) bool {
	entry, ok := d.byName(name)
	if !ok {
		return false
	}
	if _, ok := entry.Roles[role]; !ok {
		return false
	}
	delete(entry.Roles, role)
	if len(entry.Roles) == 0 {
		delete(d.Entries, entry.ID)
		return true
	}
	entry.Modified = time.Now().UTC()
	d.Entries[entry.ID] = entry
	return true
}

// newID returns a random, URL-safe entry id. It is 16 bytes of randomness, the same size a UUID spends on
// entropy, encoded as hex so it needs no further escaping anywhere it is used, such as a pending file name.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read only fails when the OS random source is unusable, which leaves nothing this
		// process could do safely; the same assumption every other user of crypto/rand in this module makes.
		panic("vault: cannot read random bytes: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// PermissionError reports a vault file or directory that other users can read. Like the plaintext
// credential fallback, a vault is the one place secrets sit outside the system keyring, so a widened mode
// is refused rather than ignored.
type PermissionError struct {
	Path string
	Mode fs.FileMode
}

func (e *PermissionError) Error() string {
	return fmt.Sprintf("%s holds vault data but its mode is %04o; it is not read until it is private again: "+
		"chmod 600 %s", e.Path, e.Mode.Perm(), e.Path)
}

// checkMode refuses a vault file that others can read or write. Windows is exempt for the same reason the
// plaintext credential fallback exempts it: os.Stat synthesises a mode from the read-only attribute alone,
// and access there is governed by ACLs instead.
func checkMode(path string, info fs.FileInfo) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return &PermissionError{Path: path, Mode: perm}
	}
	return nil
}

// readFile reads path and checks its permissions first, the same order the plaintext fallback uses.
func readFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if err := checkMode(path, info); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// writeFile replaces path atomically with mode 0600, creating its directory at 0700 if needed. It is used
// for every file a vault writes: the document, the key, the recipient, and a pending entry.
func writeFile(path string, data []byte) error {
	target := path
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("cannot create %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".qatlas-vault-*.tmp")
	if err != nil {
		return fmt.Errorf("cannot write next to %s: %w", target, err)
	}
	name := tmp.Name()
	moved := false
	defer func() {
		if !moved {
			_ = os.Remove(name)
		}
	}()

	if err := os.Chmod(name, fileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cannot set the permissions of %s: %w", name, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cannot write %s: %w", name, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cannot write %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cannot write %s: %w", name, err)
	}
	if err := os.Rename(name, target); err != nil {
		return fmt.Errorf("cannot replace %s: %w", target, err)
	}
	moved = true
	return nil
}

// loadPlain reads and decodes secrets.json. A missing file is an empty document, not an error: the vault
// directory is created lazily, on the first Set.
func loadPlainDocument(path string) (*document, error) {
	data, err := readFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return newDocument(), nil
		}
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	return decodeDocument(path, data)
}

func decodeDocument(path string, data []byte) (*document, error) {
	if len(data) == 0 {
		return newDocument(), nil
	}
	var d document
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("cannot read %s: not a valid vault document", path)
	}
	// A document of a schema version this build does not know is refused rather than read or, worse,
	// silently overwritten with the current schema on the next write.
	if d.Schema != schemaVersion {
		return nil, fmt.Errorf("cannot read %s: unsupported vault schema version %d, want %d",
			path, d.Schema, schemaVersion)
	}
	if d.Entries == nil {
		d.Entries = map[string]Entry{}
	}
	return &d, nil
}

func encodeDocument(d *document) ([]byte, error) {
	return json.MarshalIndent(d, "", "  ")
}
