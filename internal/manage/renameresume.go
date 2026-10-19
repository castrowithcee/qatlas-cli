package manage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// CredentialLeftover is what a credential's rename that stopped after the vault and before the configuration
// left: the vault holds the entry of the configured credential Credential under the name New, which no
// credential has, and Approvals names the connections whose approvals that run carried over to New. Until it
// is resolved, the credential's secrets are not read from the vault under its configured name. Renaming
// Credential to New again finishes the rename; PlanRevertRename puts it back.
type CredentialLeftover struct {
	Credential, New string
	Approvals       []string
}

// CredentialLeftovers finds every CredentialLeftover of cfg in v, which is unencrypted or unlocked in this
// process, sorted by credential. An entry counts only where it records the config id of a vault credential
// that is configured under another name, while the vault holds no entry of that name and no credential has
// the entry's.
func CredentialLeftovers(cfg *config.Config, v *vault.Vault) ([]CredentialLeftover, error) {
	if cfg == nil || v == nil {
		return nil, nil
	}
	ids, err := v.EntryCredentialIDs()
	if err != nil {
		return nil, err
	}
	approvals, err := v.Approvals()
	if errors.Is(err, vault.ErrNotEncrypted) {
		approvals, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	byID := map[string]string{}
	for name, cred := range cfg.Credentials {
		if cred.ID != "" && cred.Type == config.CredentialTypeVault {
			byID[cred.ID] = name
		}
	}
	var out []CredentialLeftover
	for name, owner := range ids {
		credential, ok := byID[owner]
		if owner == "" || !ok || credential == name {
			continue
		}
		_, configured := cfg.Credentials[name]
		_, held := ids[credential]
		renamed := cfg.Clone()
		if _, err := renamed.RenameCredential(credential, name); configured || held || err != nil {
			continue
		}
		left := CredentialLeftover{Credential: credential, New: name}
		if approvals != nil {
			rest, err := leftoverApprovals(cfg, renamed, credential, v, approvals)
			if err != nil {
				return nil, err
			}
			for _, r := range rest {
				left.Approvals = append(left.Approvals, r.After.Connection)
			}
		}
		out = append(out, left)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Credential < out[j].Credential })
	return out, nil
}

// leftoverApprovals are the approvals that a rename of the credential old of cfg, to its name in renamed,
// carried over before it stopped short of the configuration: of every connection that reads old or releases
// it, the approval that matches the connection's scope in renamed, for the entry it reads there, and records
// the connection's config id. Each comes as the way back, from the scope in renamed to the one in cfg.
func leftoverApprovals(cfg, renamed *config.Config, old string, v *vault.Vault,
	approvals map[string]vault.Approval) ([]vault.ScopeRename, error) {
	before, err := approval.VaultScopes(cfg)
	if err != nil {
		return nil, err
	}
	after, err := approval.VaultScopes(renamed)
	if err != nil {
		return nil, err
	}
	scopes := make(map[string]vault.Scope, len(before))
	for _, scope := range before {
		scopes[scope.Connection] = scope
	}
	readers := map[string]bool{}
	for _, reader := range readersOf(cfg, old) {
		readers[reader.Connection] = true
	}
	var out []vault.ScopeRename
	for _, scope := range after {
		back, ok := scopes[scope.Connection]
		approved, isApproved := approvals[scope.Connection]
		id := cfg.Connections[scope.Connection].ID
		if !ok || !isApproved || !readers[scope.Connection] || id == "" || approved.ConnectionID != id {
			continue
		}
		entry, held, err := v.CredentialID(scope.Credential)
		if err != nil {
			return nil, err
		}
		if held && approved.Fingerprint == vault.Fingerprint(scope, entry) {
			out = append(out, vault.ScopeRename{Before: scope, After: back, ConnectionID: id})
		}
	}
	return out, nil
}

// PlanRevertRename finds what putting back the CredentialLeftover of renaming the credential old to name
// changes, without changing anything: the vault entry the rename gave the name name, its pending writes, the
// approvals it carried over, and for a keyring credential the keys under name that hold the very secret of
// the key under old. Anything under name that is not such a rest is refused, as is a plan that finds none.
// approve says that the surface acts in an active admin session: only then do the approvals go back to old;
// without it they are left as they are. Rename carries the plan out (see revertCredential).
func (s *Service) PlanRevertRename(old, name string, approve bool) (*RenamePlan, error) {
	cfg, rev, err := s.Load()
	if err != nil {
		return nil, err
	}
	cand := cfg.Clone()
	cand.AssignIDs()
	cred, ok := cand.Credentials[old]
	if !ok {
		return nil, &RenameRefusedError{&config.NotThereError{Kind: "credential", Name: old}}
	}
	renamed := cand.Clone()
	if _, err := renamed.RenameCredential(old, name); err != nil {
		return nil, &RenameRefusedError{err}
	}
	plan := &RenamePlan{Kind: RenameCredential, Old: old, New: name, ID: cred.ID, cfg: cand, base: rev}
	plan.Revert, plan.before = true, renamed
	plan.Readers = readersOf(cand, old)
	if cred.Type == config.CredentialTypeKeyring {
		if s.secrets == nil {
			return nil, errNoSecretStore
		}
		for _, role := range credentialRoles(cand, cred) {
			copied, err := s.keyringCopy(old, name, role)
			if err != nil {
				return nil, err
			}
			if copied {
				plan.KeyringRoles = append(plan.KeyringRoles, role)
			}
		}
	}
	if err := s.planRevertVault(plan, cred, approve); err != nil {
		return nil, err
	}
	if !plan.VaultUnread && !plan.VaultEntry && len(plan.KeyringRoles) == 0 && len(plan.carry) == 0 {
		return nil, &RenameRefusedError{fmt.Errorf("nothing is left over from renaming credential %s to %s",
			old, name)}
	}
	return plan, nil
}

// keyringCopy reports whether the system keyring holds role under name with the very secret it holds under
// old: the copy a rename of old to name wrote before it stopped.
func (s *Service) keyringCopy(old, name, role string) (bool, error) {
	held, err := s.previousKeyringSecret(old, role)
	if err != nil {
		return false, fmt.Errorf("reading %s from the system keyring: %w", secret.StoreKey(old, role), err)
	}
	copied, err := s.previousKeyringSecret(name, role)
	if err != nil {
		return false, fmt.Errorf("reading %s from the system keyring: %w", secret.StoreKey(name, role), err)
	}
	return held.had && copied.had && held.value == copied.value, nil
}

// planRevertVault fills in what the vault holds of the rest a revert puts back. A vault that is encrypted but
// not unlocked in this process is only reported unread.
func (s *Service) planRevertVault(plan *RenamePlan, cred config.Credential, approve bool) error {
	v := s.vault()
	if v == nil {
		return nil
	}
	state, err := v.State()
	if err != nil {
		return err
	}
	switch state {
	case vault.StateLocked:
		plan.VaultUnread = true
		return nil
	case vault.StateUnencrypted, vault.StateUnlocked:
	default:
		return nil
	}
	ids, err := v.EntryCredentialIDs()
	if err != nil {
		return err
	}
	if owner, taken := ids[plan.New]; taken {
		if _, old := ids[plan.Old]; cred.Type != config.CredentialTypeVault || old || owner == "" || owner != plan.ID {
			return &RenameRefusedError{fmt.Errorf("the vault holds secrets under the name %s that are not left "+
				"over from renaming credential %s", plan.New, plan.Old)}
		}
		plan.VaultEntry = true
	}
	if state == vault.StateUnencrypted {
		return nil
	}
	pending, err := v.PendingWrites()
	if err != nil {
		return err
	}
	if plan.VaultEntry {
		plan.PendingWrites = pending[plan.New]
	}
	approvals, err := v.Approvals()
	if err != nil {
		return err
	}
	rest, err := leftoverApprovals(plan.cfg, plan.before, plan.Old, v, approvals)
	if err != nil {
		return err
	}
	back := map[string]bool{}
	for _, r := range rest {
		back[r.After.Connection] = true
	}
	scopes, err := approval.VaultScopes(plan.cfg)
	if err != nil {
		return err
	}
	for i := range plan.Readers {
		reader := &plan.Readers[i]
		approved, isApproved := approvals[reader.Connection]
		switch {
		case back[reader.Connection]:
			reader.Approval, reader.Carried = ApprovalValid, approve
		case isApproved:
			reader.Approval = ApprovalOpen
			for _, scope := range scopes {
				id, held, err := v.CredentialID(scope.Credential)
				if err != nil {
					return err
				}
				if scope.Connection == reader.Connection && held && approved.Fingerprint == vault.Fingerprint(scope, id) {
					reader.Approval = ApprovalValid
				}
			}
		}
	}
	if approve {
		plan.carry = rest
	}
	return nil
}

// revertCredential carries out a plan of PlanRevertRename under the configuration's lock, which it does not
// write: if the configuration still has the revision the plan was based on, it backs up the vault's files and
// gives the entry its old name again with the approvals the plan carries back (see
// vault.Vault.RenameCredential), the commit point. Then it removes each keyring copy that still holds the
// very secret of the old key and hands the entry and the approvals to a vault process that holds the vault
// unlocked; a failure there is returned as a warning.
func (s *Service) revertCredential(ctx context.Context, plan *RenamePlan, v *vault.Vault) (RenameResult, error) {
	var res RenameResult
	cred := plan.cfg.Credentials[plan.Old]
	written := false
	err := s.store.Transact(plan.base, func(func(*config.Config) error) error {
		if v == nil || !plan.VaultEntry && plan.PendingWrites == 0 && len(plan.carry) == 0 {
			return nil
		}
		copies, err := s.backupVaultFiles(v)
		discard := func() {
			for _, path := range copies {
				_ = os.Remove(path)
			}
		}
		if err != nil {
			discard()
			return err
		}
		res.Carried, _, err = v.RenameCredential(vault.CredentialRename{
			From: plan.New, To: plan.Old, CredentialID: plan.ID, Entry: plan.VaultEntry, Carry: plan.carry,
		})
		if err != nil {
			discard()
			if errors.Is(err, vault.ErrEntryTaken) {
				err = &RenameRefusedError{err}
			}
			return fmt.Errorf("putting %s back in the vault: %w", plan.Old, err)
		}
		res.VaultCopies, written = copies, true
		return nil
	})
	if err != nil {
		return RenameResult{}, err
	}
	for _, role := range plan.KeyringRoles {
		key := secret.StoreKey(plan.New, role)
		// Checked again, so only a copy of the old key is ever removed.
		copied, err := s.keyringCopy(plan.Old, plan.New, role)
		if err == nil && copied {
			if _, err = s.secrets.Delete(plan.New, role); errors.Is(err, secret.ErrNoEntry) {
				err = nil
			}
		} else if err == nil {
			err = errors.New("it no longer holds the secret of " + secret.StoreKey(plan.Old, role))
		}
		if err != nil {
			res.Warnings = append(res.Warnings, s.redact(fmt.Sprintf("warning: the system keyring key %s was left "+
				"in place: %v; no credential reads it while none is named %s", key, err, plan.New)))
		}
	}
	if written {
		if warning := s.syncRenamedEntry(ctx, v, plan.cfg, cred, plan.New, plan.Old); warning != "" {
			res.Warnings = append(res.Warnings, warning)
		}
	}
	return res, nil
}
