package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Checkpoint records the files a write of secrets or approvals changes - the document, encrypted or not, the
// key, the recipient and the pending entries - together with what this process holds of the vault, and
// returns restore, which puts those very bytes back, removes such a file or pending entry that was not there,
// and returns this Vault to the state it had. A vault directory the checkpoint did not find is removed again
// once it is empty. restore needs no passphrase. It is for a change of several writes that, when it fails,
// must leave the vault byte for byte as it was. Agent tokens and settings are not covered.
func (v *Vault) Checkpoint() (restore func() error, err error) {
	files := []string{v.plainPath(), v.secretsPath(), v.keyPath(), v.recipientPath()}
	saved := map[string][]byte{}
	for _, path := range files {
		data, err := readFile(path)
		switch {
		case err == nil:
			saved[path] = data
		case !isNotExist(err):
			return nil, fmt.Errorf("cannot read %s: %w", path, err)
		}
	}
	pending, err := v.pendingFiles()
	if err != nil {
		return nil, err
	}
	dirExisted, err := fileExists(v.dir)
	if err != nil {
		return nil, err
	}
	pendingExisted, err := fileExists(v.pendingPath())
	if err != nil {
		return nil, err
	}
	unlocked, id, doc := v.unlocked, v.identity, cloneDocument(v.doc)

	return func() error {
		var failed []error
		for _, path := range files {
			if data, ok := saved[path]; ok {
				if err := writeFile(path, data); err != nil {
					failed = append(failed, err)
				}
			} else if err := os.Remove(path); err != nil && !isNotExist(err) {
				failed = append(failed, err)
			}
		}
		now, err := v.pendingFiles()
		if err != nil {
			failed = append(failed, err)
		}
		for _, name := range now {
			if !slices.Contains(pending, name) {
				if err := os.Remove(filepath.Join(v.pendingPath(), name)); err != nil && !isNotExist(err) {
					failed = append(failed, err)
				}
			}
		}
		if !pendingExisted {
			if err := os.Remove(v.pendingPath()); err != nil && !isNotExist(err) {
				failed = append(failed, err)
			}
		}
		if !dirExisted {
			if err := os.Remove(v.dir); err != nil && !isNotExist(err) {
				failed = append(failed, err)
			}
		}
		v.unlocked, v.identity, v.doc = unlocked, id, cloneDocument(doc)
		return errors.Join(failed...)
	}, nil
}

// cloneDocument is document.clone that also takes nil, the document of a vault not unlocked here.
func cloneDocument(d *document) *document {
	if d == nil {
		return nil
	}
	return d.clone()
}
