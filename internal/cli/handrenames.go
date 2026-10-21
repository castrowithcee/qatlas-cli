package cli

import (
	"context"
	"fmt"

	"github.com/castrowithcee/qatlas-cli/internal/manage"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// handRenameDocument is one rename made by hand in the configuration, as 'config validate' and 'vault
// approve' report it in JSON. Command is "" for an ambiguous one, for which nothing is suggested.
type handRenameDocument struct {
	Kind      string `json:"kind"`
	Old       string `json:"old"`
	New       string `json:"new,omitempty"`
	Ambiguous bool   `json:"ambiguous"`
	Command   string `json:"command,omitempty"`
}

func handRenameDocumentOf(opts *Options, r manage.HandRename) handRenameDocument {
	apply := opts.Redactor.Apply
	doc := handRenameDocument{Kind: r.Kind, Old: apply(r.Old), Ambiguous: r.Ambiguous}
	if !r.Ambiguous {
		doc.New = apply(r.New)
		doc.Command = fmt.Sprintf("qatlas config rename %s %s %s --adopt", r.Kind, apply(r.Old), apply(r.New))
	}
	return doc
}

// handRenameText tells one hand rename in a sentence, with the command that adopts it.
func handRenameText(opts *Options, r manage.HandRename) string {
	doc := handRenameDocumentOf(opts, r)
	what := "approval"
	if r.Kind == manage.RenameCredential {
		what = "entry"
	}
	if r.Ambiguous {
		return fmt.Sprintf("the vault holds a %s %s under the name %s that could belong to more than one "+
			"configured %s, or the other way round; nothing is suggested", r.Kind, what, doc.Old, r.Kind)
	}
	return fmt.Sprintf("%s %s was renamed to %s by hand in the configuration, while the vault holds its %s under "+
		"the old name: '%s' moves it after the configuration", r.Kind, doc.Old, doc.New, what, doc.Command)
}

// readableVaultNames reads the names of the vault without asking for a passphrase or starting anything: an
// unencrypted vault directly, an encrypted one only through a vault process that holds it unlocked. present
// is false where there is no vault to check. checked is false where there is one that could not be read this
// way, because it is locked, no vault process holds it, or the process cannot tell its names.
func readableVaultNames(ctx context.Context, opts *Options) (names vault.Names, present, checked bool) {
	v, err := vaultOf(opts)
	if err != nil {
		return names, false, false
	}
	state, err := v.State()
	if err != nil {
		return names, true, false
	}
	switch state {
	case vault.StateAbsent:
		return names, false, false
	case vault.StateUnencrypted, vault.StateUnlocked:
		names, err = v.KnownNames()
		return names, true, err == nil
	}
	client, err := vaultproc.ProcessClientOf(v)
	if err != nil {
		return names, true, false
	}
	names, err = client.Names(ctx)
	// No process (ErrNotRunning), one that cannot tell (ErrNoNames, ErrVersion) or any other failure is no
	// reason to fail a validation: the vault is reported as not checked.
	if err != nil {
		return vault.Names{}, true, false
	}
	return names, true, true
}
