package manage

import (
	"errors"
	"fmt"
	"sort"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// HandRename is a connection or a credential renamed by hand in the configuration, as the vault tells it: the
// configuration has the entry under New, while the vault keeps what belongs to it under Old, a name nothing
// of that kind is configured under. Adopting the rename (see PlanAdopt) brings the vault after the
// configuration; nothing does so on its own, since the configuration is no trust anchor.
//
// A connection's is an approval (see approval.HandRename); a credential's is the entry of a vault credential
// that records the credential's config id, carries no mark of a rename and is the only such entry, while the
// vault holds no entry under New. A keyring credential's keys record no id and are never found. Ambiguous
// says the vault keeps something under Old that could belong to more than one entry, or the other way round;
// New and ID are "" then, and nothing is suggested.
type HandRename struct {
	// Kind is RenameConnection or RenameCredential.
	Kind     string
	Old, New string
	// ID is the config id of the entry.
	ID        string
	Ambiguous bool
}

// HandRenames finds every HandRename of cfg in names, sorted by kind and old name: the names of an
// unencrypted vault or of one unlocked in this process (vault.Vault.KnownNames), or what a vault process that
// holds it unlocked tells (vaultproc.Client.Names). It reads no secret and writes nothing.
func HandRenames(cfg *config.Config, names vault.Names) []HandRename {
	if cfg == nil {
		return nil
	}
	var out []HandRename
	for _, r := range approval.HandRenames(cfg, names) {
		found := HandRename{Kind: RenameConnection, Old: r.Approval, Ambiguous: r.Ambiguous}
		if !r.Ambiguous {
			found.New, found.ID = r.Connection, cfg.Connections[r.Connection].ID
		}
		out = append(out, found)
	}
	byID := vaultCredentialsByID(cfg)
	entries := map[string][]string{}
	for name, id := range names.Entries {
		credential, ok := byID[id]
		if id == "" || !ok || credential == name || names.Renamed[name] != "" {
			continue
		}
		_, configured := cfg.Credentials[name]
		_, held := names.Entries[credential]
		if !configured && !held {
			entries[credential] = append(entries[credential], name)
		}
	}
	for credential, held := range entries {
		for _, name := range held {
			found := HandRename{Kind: RenameCredential, Old: name, Ambiguous: len(held) > 1}
			if !found.Ambiguous {
				found.New, found.ID = credential, cfg.Credentials[credential].ID
			}
			out = append(out, found)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Old < out[j].Old
	})
	return out
}

// vaultCredentialsByID names the vault credential of cfg each config id belongs to.
func vaultCredentialsByID(cfg *config.Config) map[string]string {
	byID := map[string]string{}
	for name, cred := range cfg.Credentials {
		if cred.ID != "" && cred.Type == config.CredentialTypeVault {
			byID[cred.ID] = name
		}
	}
	return byID
}

// PlanAdopt finds what adopting a rename of the entry old of kind to name changes, a rename made by hand in
// the configuration, which has name already and no longer old. It turns the rename back in memory, on a copy
// of the configuration as it is now, and plans renaming old to name from there exactly as PlanRename does:
// the vault entry, its pending writes and the system keyring's keys follow the new name, and with approve the
// approvals do as they are, valid or open; nothing is approved anew. Rename carries the plan out, writes the
// configuration as it is now and refuses the plan once the file changed since.
//
// A service binds nothing to its name outside the configuration and is refused, as is a name that is not
// configured, an old name that still is, and, where the vault could be read, a plan that finds nothing the
// vault or the system keyring keeps under old.
func (s *Service) PlanAdopt(kind, old, name string, approve bool) (*RenamePlan, error) {
	cfg, rev, err := s.Load()
	if err != nil {
		return nil, err
	}
	before := cfg.Clone()
	switch kind {
	case RenameService:
		return nil, &RenameRefusedError{errors.New("a service binds nothing to its name outside the " +
			"configuration, so a hand rename of one needs nothing adopted")}
	case RenameConnection:
		if err = adoptable(kind, cfg.Connections, old, name); err == nil {
			_, err = before.RenameConnection(name, old)
		}
	case RenameCredential:
		if err = adoptable(kind, cfg.Credentials, old, name); err == nil {
			_, err = before.RenameCredential(name, old)
		}
	default:
		return nil, &RenameRefusedError{fmt.Errorf("%s: %w", kind, ErrRenameUnsupported)}
	}
	if err != nil {
		return nil, &RenameRefusedError{err}
	}
	plan, err := s.planRename(before, rev, kind, old, name, approve)
	if err != nil {
		return nil, err
	}
	plan.Adopt = true
	if plan.VaultUnread {
		return plan, nil
	}
	switch {
	case kind == RenameConnection && plan.Approval == "":
		return nil, &RenameRefusedError{fmt.Errorf("the vault holds no approval under the connection name %s, "+
			"so there is nothing to adopt", old)}
	case kind == RenameCredential && !plan.VaultEntry && plan.PendingWrites == 0 && len(plan.KeyringRoles) == 0 &&
		!plan.Resumes:
		return nil, &RenameRefusedError{fmt.Errorf("neither the vault nor the system keyring holds anything "+
			"under the credential name %s, so there is nothing to adopt", old)}
	}
	return plan, nil
}

// adoptable reports why the rename of old to name of kind among entries is no hand rename to adopt: name is
// not configured, or old still is.
func adoptable[V any](kind string, entries map[string]V, old, name string) error {
	if _, ok := entries[name]; !ok {
		return &config.NotThereError{Kind: kind, Name: name}
	}
	if _, ok := entries[old]; ok {
		return fmt.Errorf("%s %q is still configured; adopting takes a rename the configuration has made "+
			"already", kind, old)
	}
	return nil
}
