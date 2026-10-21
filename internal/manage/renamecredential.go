package manage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// CredentialPlan is what renaming a credential changes besides the configuration.
type CredentialPlan struct {
	// Readers are the connections that read the credential or release it as a forward credential, sorted by
	// name.
	Readers []RenameReader
	// KeyringRoles are the roles of a keyring credential the system keyring holds under the old name. Each is
	// written under the new name and read back before the commit, and the old key is removed after it.
	KeyringRoles []string
	// VaultEntry says the vault holds the entry of a vault credential, which the rename renames, keeping its
	// id and its secrets. PendingWrites counts the writes queued for the old name, merged into it first.
	VaultEntry    bool
	PendingWrites int
	// EnvVars are the variables derived from the credential's name that can override its stored secrets; the
	// credential reads them under their new names from now on. qatlas changes no environment.
	EnvVars []EnvRename
	// Resumes says the rename finishes one that stopped before the configuration was saved: the vault holds
	// the credential's entry under the new name already, or the system keyring holds a copy of its secrets
	// there. What that run carried over is reported as carried.
	Resumes bool
	// Revert says the plan puts back what such a stopped rename left instead (see PlanRevertRename): the
	// vault entry gets the old name again, the approvals it carried go back to the old name, and KeyringRoles
	// are the copies under the new name that are removed. The configuration is not written.
	Revert bool

	before *config.Config
	carry  []vault.ScopeRename
	// resumeEntry is the part of Resumes that concerns the vault entry, which approvals depend on; resumed
	// are the connections whose approvals the run that stopped carried over.
	resumeEntry bool
	resumed     []string
}

// RenameReader is one connection that reads a renamed credential or releases it.
type RenameReader struct {
	Connection string
	// Approval is ApprovalValid, ApprovalOpen, or "" where the vault holds no approval of the connection or
	// binds none. Carried says the rename carries a valid one over; an open one stays open.
	Approval string
	Carried  bool
}

// EnvRename is the variable one role of a credential is read from, before and after a rename.
type EnvRename struct{ Role, Old, New string }

// planCredential fills in what renaming a credential changes beyond the configuration, and refuses a new name
// another store already gives something to: a vault entry or a pending write under it, and for a keyring
// credential a key of the system keyring or an entry of the unencrypted fallback file. The keyring is read
// only for a keyring credential; one that cannot be asked fails the plan, since what it holds cannot be told.
func (s *Service) planCredential(plan *RenamePlan, before *config.Config, approve bool) error {
	plan.before = before
	cred := plan.cfg.Credentials[plan.New]
	roles := credentialRoles(plan.cfg, cred)
	plan.Readers = readersOf(before, plan.Old)
	plan.Outside = []string{"commands, scripts, instructions and documents that name the credential " + plan.Old}
	if cred.Forward {
		plan.Outside = append(plan.Outside, "agent calls that pass the forward credential "+plan.Old+
			" as a reference argument")
	}
	switch {
	case cred.Type == config.CredentialTypeEnv:
		names := make([]string, 0, len(cred.Values))
		for _, name := range cred.Values {
			names = append(names, name)
		}
		sort.Strings(names)
		plan.Outside = append(plan.Outside, "environment variables "+strings.Join(names, ", ")+
			": the credential names them itself, and they stay as they are")
	case cred.Forward:
		plan.Outside = append(plan.Outside, "environment variables: a forward credential reads none")
	default:
		for _, role := range roles {
			plan.EnvVars = append(plan.EnvVars, EnvRename{Role: role, Old: secret.DerivedEnvName(plan.Old, role),
				New: secret.DerivedEnvName(plan.New, role)})
		}
		if len(roles) > 0 {
			plan.Outside = append(plan.Outside, "environment variables named after the credential, which override "+
				"its stored secrets: it reads them under its new name from now on, and qatlas changes "+
				"no environment")
		}
	}
	if cred.Type == config.CredentialTypeKeyring {
		if err := s.planKeyring(plan, roles); err != nil {
			return err
		}
	}
	return s.planCredentialVault(plan, cred, approve)
}

// planKeyring finds the keys of the system keyring the rename moves and refuses a new name the keyring or the
// unencrypted fallback file already holds a secret under. A secret of the old name in the fallback file is
// refused as well: the file is read-only, so the rename could not carry it over.
func (s *Service) planKeyring(plan *RenamePlan, roles []string) error {
	if s.secrets == nil {
		return errNoSecretStore
	}
	for _, role := range roles {
		held, err := s.previousKeyringSecret(plan.Old, role)
		if err != nil {
			return fmt.Errorf("reading %s from the system keyring: %w", secret.StoreKey(plan.Old, role), err)
		}
		if held.had {
			plan.KeyringRoles = append(plan.KeyringRoles, role)
		}
		taken, err := s.previousKeyringSecret(plan.New, role)
		if err != nil {
			return fmt.Errorf("reading %s from the system keyring: %w", secret.StoreKey(plan.New, role), err)
		}
		switch {
		case taken.had && held.had && taken.value == held.value:
			// A copy a rename that stopped wrote: finishing it writes nothing there.
			plan.Resumes = true
		case taken.had:
			return &RenameRefusedError{fmt.Errorf("the system keyring holds a secret under %s already; remove "+
				"it with the system's key manager before a credential takes the name %s",
				secret.StoreKey(plan.New, role), plan.New)}
		}
	}
	holder, ok := s.secrets.(interface{ Plaintext() *secret.File })
	if !ok || holder.Plaintext() == nil {
		return nil
	}
	file := holder.Plaintext()
	for _, role := range roles {
		for _, name := range []string{plan.Old, plan.New} {
			holds, err := file.Holds(name, role)
			if err != nil {
				return err
			}
			switch {
			case holds && name == plan.Old:
				return &RenameRefusedError{fmt.Errorf("the unencrypted fallback file %s holds the %s secret of %s, "+
					"which a rename cannot carry over; remove it with 'qatlas credential delete %s %s' and store it "+
					"again with 'qatlas credential set %s %s' first", file.Path(), role, plan.Old, plan.Old, role,
					plan.Old, role)}
			case holds:
				return &RenameRefusedError{fmt.Errorf("the unencrypted fallback file %s holds a secret under the "+
					"name %s already; remove it before a credential takes that name", file.Path(), plan.New)}
			}
		}
	}
	return nil
}

// planCredentialVault fills in what the vault holds for the credential and the approvals of its readers, and
// refuses a new name the vault holds secrets or pending writes under. A vault that is encrypted but not
// unlocked in this process is only reported unread.
func (s *Service) planCredentialVault(plan *RenamePlan, cred config.Credential, approve bool) error {
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
	isVault := cred.Type == config.CredentialTypeVault
	if owner, taken := ids[plan.New]; taken {
		// Only the entry a rename of this very credential left is taken over; any other keeps the name.
		if _, old := ids[plan.Old]; !isVault || old || owner == "" || owner != plan.ID {
			return &RenameRefusedError{fmt.Errorf("the vault holds secrets under the name %s already; a "+
				"credential cannot take that name while they are there", plan.New)}
		}
		plan.Resumes, plan.resumeEntry = true, true
	}
	if isVault {
		_, plan.VaultEntry = ids[plan.Old]
	}
	if state == vault.StateUnencrypted {
		return nil
	}
	pending, err := v.PendingWrites()
	if err != nil {
		return err
	}
	if pending[plan.New] > 0 {
		return &RenameRefusedError{fmt.Errorf("the vault holds pending writes for the name %s; a credential "+
			"cannot take that name while they are there", plan.New)}
	}
	if isVault {
		plan.PendingWrites = pending[plan.Old]
	}
	return s.planApprovals(plan, v, approve)
}

// planApprovals fills in the approval of every reader and the agent tokens whose cover the rename ends. An
// open approval that would match the reader once renamed is refused: renaming would approve it silently.
func (s *Service) planApprovals(plan *RenamePlan, v *vault.Vault, approve bool) error {
	approvals, err := v.Approvals()
	if err != nil {
		return err
	}
	oldScopes, err := approval.VaultScopes(plan.before)
	if err != nil {
		return err
	}
	newScopes, err := approval.VaultScopes(plan.cfg)
	if err != nil {
		return err
	}
	scopes := make(map[string][2]vault.Scope, len(oldScopes))
	for _, scope := range oldScopes {
		scopes[scope.Connection] = [2]vault.Scope{scope}
	}
	for _, scope := range newScopes {
		pair := scopes[scope.Connection]
		pair[1] = scope
		scopes[scope.Connection] = pair
	}
	carried := map[string]bool{}
	resumed := map[string]bool{}
	if plan.resumeEntry {
		rest, err := leftoverApprovals(plan.before, plan.cfg, plan.Old, v, approvals)
		if err != nil {
			return err
		}
		for _, r := range rest {
			resumed[r.After.Connection] = true
		}
	}
	for i := range plan.Readers {
		reader := &plan.Readers[i]
		pair, ok := scopes[reader.Connection]
		approved, isApproved := approvals[reader.Connection]
		if !ok || !isApproved {
			continue
		}
		if resumed[reader.Connection] {
			// The run that stopped carried it over already.
			reader.Approval, reader.Carried = ApprovalValid, true
			carried[reader.Connection] = true
			plan.resumed = append(plan.resumed, reader.Connection)
			continue
		}
		// The entry a connection reads keeps its id when it is the one renamed.
		own, held, err := v.CredentialID(pair[0].Credential)
		if err != nil {
			return err
		}
		if !held && plan.resumeEntry {
			own, held, err = v.CredentialID(pair[1].Credential)
			if err != nil {
				return err
			}
		}
		if !held {
			continue
		}
		if approved.Fingerprint != vault.Fingerprint(pair[0], own) {
			reader.Approval = ApprovalOpen
			if approved.Fingerprint == vault.Fingerprint(pair[1], own) {
				return &RenameRefusedError{fmt.Errorf("the vault holds an approval of %s for the credential name "+
					"%s that the rename would make valid again unseen; approve the connection as it is with "+
					"'qatlas vault approve' first", reader.Connection, plan.New)}
			}
			continue
		}
		reader.Approval, reader.Carried = ApprovalValid, approve
		if approve {
			carried[reader.Connection] = true
			plan.carry = append(plan.carry, vault.ScopeRename{Before: pair[0], After: pair[1],
				ConnectionID: plan.cfg.Connections[reader.Connection].ID})
		}
	}
	tokens, err := v.Tokens()
	if err != nil {
		return err
	}
	for _, token := range tokens {
		lapsed, err := approval.LapsedModels(plan.before, v, token, time.Now())
		if err != nil {
			return err
		}
		for _, model := range token.Models {
			approved, ok := approvals[model]
			if ok && !carried[model] && !slices.Contains(lapsed, model) && namesCredential(approved.Scope, plan.Old) {
				plan.LapsedTokens = append(plan.LapsedTokens, token.Name)
				break
			}
		}
	}
	return nil
}

// renameCredential carries out a credential's plan under the configuration's lock, in the order that leaves
// nothing behind a failed commit: it writes the keyring secrets under the new name and reads each back, backs
// up the vault's files and renames the vault entry with the approvals the plan carries (see
// vault.Vault.RenameCredential), and saves the configuration, the commit point. A failure up to the commit
// removes the new keyring keys again and puts the vault back byte for byte.
//
// The vault entry carries a mark of the rename (vault.Entry.RenamedFrom) from its write until the commit; once
// committed the run removes it in a further write, and a failure there is a warning. A run that stops between
// the two leaves the mark, and a resumed run removes it.
//
// Once committed it removes the old keyring keys, hands the renamed entry and the approvals to a vault process
// that holds the vault unlocked, and logs every connection whose entry or approval scope changed, the way any
// credential change is logged. A failure there is returned as a warning: an old key is read by no credential
// while none has the old name.
func (s *Service) renameCredential(ctx context.Context, plan *RenamePlan, v *vault.Vault) (RenameResult, error) {
	res := RenameResult{EnvVars: plan.EnvVars}
	cred := plan.cfg.Credentials[plan.New]
	toVault := cred.Type == config.CredentialTypeVault
	var moved []string
	vaultWritten := false
	err := s.store.Transact(plan.base, func(save func(*config.Config) error) error {
		var undos []undoStep
		if cred.Type == config.CredentialTypeKeyring {
			var err error
			if moved, undos, err = s.moveKeyring(plan, credentialRoles(plan.cfg, cred)); err != nil {
				return err
			}
		}
		undoVault := func() error { return nil }
		var copies []string
		discard := func() {
			for _, path := range copies {
				_ = os.Remove(path)
			}
		}
		if v != nil && (plan.VaultEntry || plan.PendingWrites > 0 || len(plan.carry) > 0) {
			var err error
			if copies, err = s.backupVaultFiles(v); err != nil {
				discard()
				return undoKeyring(plan.New, undos, err)
			}
			res.Carried, undoVault, err = v.RenameCredential(vault.CredentialRename{
				From: plan.Old, To: plan.New, CredentialID: plan.ID, Entry: toVault, Resume: plan.resumeEntry,
				Mark: true, Carry: plan.carry,
			})
			if err != nil {
				discard()
				if errors.Is(err, vault.ErrEntryTaken) {
					err = &RenameRefusedError{err}
				}
				return undoKeyring(plan.New, undos, fmt.Errorf("renaming %s in the vault: %w", plan.Old, err))
			}
			vaultWritten = true
		}
		if err := saveRename(save, plan.cfg); err != nil {
			if undoErr := undoVault(); undoErr != nil {
				// The copies are the only way back for the vault now, so they stay.
				err = fmt.Errorf("%w; the vault could not be put back as it was (%v), its copies are %s", err,
					undoErr, strings.Join(copies, ", "))
			} else {
				discard()
			}
			return undoKeyring(plan.New, undos, err)
		}
		res.VaultCopies = copies
		return nil
	})
	if err != nil {
		return RenameResult{EnvVars: plan.EnvVars}, err
	}
	for _, role := range moved {
		if _, err := s.secrets.Delete(plan.Old, role); err != nil && !errors.Is(err, secret.ErrNoEntry) {
			res.Warnings = append(res.Warnings, s.redact(fmt.Sprintf("warning: the system keyring still holds the "+
				"old key %s: %v; no credential reads it while none is named %s, remove it with the system's key "+
				"manager", secret.StoreKey(plan.Old, role), err, plan.Old)))
		}
	}
	// The configuration is saved, so the entry no longer stands for a rename in progress. A resumed run clears
	// the mark a stopped one left.
	if toVault && v != nil && (vaultWritten || plan.resumeEntry) {
		if err := v.ClearRenameMarker(plan.New); err != nil {
			res.Warnings = append(res.Warnings, s.redact(fmt.Sprintf("warning: the vault entry %s still carries "+
				"the mark of the rename from %s: %v; it is harmless", plan.New, plan.Old, err)))
		}
	}
	// A vault process still holds what it held before a run that stopped, so a resumed one is handed over too.
	if vaultWritten || plan.resumeEntry {
		if warning := s.syncRenamedEntry(ctx, v, plan.cfg, cred, plan.Old, plan.New); warning != "" {
			res.Warnings = append(res.Warnings, warning)
		}
	}
	if len(plan.resumed) > 0 {
		res.Carried = append(res.Carried, plan.resumed...)
		sort.Strings(res.Carried)
	}
	if warning := s.RecordConnections(plan.before, plan.cfg); warning != "" {
		res.Warnings = append(res.Warnings, warning)
	}
	return res, nil
}

// syncRenamedEntry hands the entry of cred, renamed from one name to another in the vault, and the approvals
// to a vault process that holds the vault unlocked, and returns its warning. The entry's secrets are handed
// only for a vault credential; for any other the approvals alone are.
func (s *Service) syncRenamedEntry(ctx context.Context, v *vault.Vault, cfg *config.Config, cred config.Credential,
	from, to string) string {
	return s.SyncVaultProcess(ctx, func(ctx context.Context, client *vaultproc.Client) error {
		if cred.Type != config.CredentialTypeVault {
			return nil
		}
		for _, role := range vaultRoles(cfg, cred) {
			value, found, _, err := v.Get(to, role, nil)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			if err := client.Set(ctx, to, role, value); err != nil {
				return err
			}
			if err := client.Delete(ctx, from, role); err != nil {
				return err
			}
		}
		return nil
	})
}

// moveKeyring writes every role of roles the system keyring holds under the old name of plan under the new
// one and reads it back. It returns the roles it moved and the undo of each write; on a failure it has undone
// them itself. A key the new name already holds is refused, never overwritten, unless it holds the very same
// secret.
func (s *Service) moveKeyring(plan *RenamePlan, roles []string) ([]string, []undoStep, error) {
	var moved []string
	var undos []undoStep
	for _, role := range roles {
		key := secret.StoreKey(plan.New, role)
		held, err := s.previousKeyringSecret(plan.Old, role)
		if err != nil {
			return nil, nil, undoKeyring(plan.New, undos, fmt.Errorf("reading %s from the system keyring: %w",
				secret.StoreKey(plan.Old, role), err))
		}
		if !held.had {
			continue
		}
		taken, err := s.previousKeyringSecret(plan.New, role)
		if err != nil {
			return nil, nil, undoKeyring(plan.New, undos, fmt.Errorf("reading %s from the system keyring: %w", key, err))
		}
		if taken.had && taken.value == held.value {
			// The copy a rename that stopped wrote; it was there before, so no undo removes it.
			moved = append(moved, role)
			continue
		}
		if taken.had {
			return nil, nil, undoKeyring(plan.New, undos, &RenameRefusedError{fmt.Errorf("the system keyring holds "+
				"a secret under %s already", key)})
		}
		// The undo comes first: a write that fails may still have stored the key.
		undos = append(undos, undoStep{role: role, undo: s.keyringUndo(plan.New, role, previousSecret{}), removal: true})
		if err := s.secrets.Set(plan.New, role, held.value); err != nil {
			return nil, nil, undoKeyring(plan.New, undos, fmt.Errorf("storing the secret under %s: %w", key, err))
		}
		back, err := s.previousKeyringSecret(plan.New, role)
		if err != nil {
			return nil, nil, undoKeyring(plan.New, undos, fmt.Errorf("reading %s back: %w", key, err))
		}
		if !back.had || back.value != held.value {
			return nil, nil, undoKeyring(plan.New, undos, fmt.Errorf("the system keyring does not return the "+
				"secret just stored under %s", key))
		}
		moved = append(moved, role)
	}
	return moved, undos, nil
}

// undoKeyring removes the keys a failed rename wrote under the new name and says which, if any, are left.
func undoKeyring(name string, steps []undoStep, cause error) error {
	var left []string
	for _, step := range steps {
		if err := step.undo(); err != nil {
			left = append(left, step.role)
		}
	}
	if len(left) > 0 {
		cause = fmt.Errorf("%w; the system keyring still holds the secrets written under the new name %s (%s), "+
			"remove them with the system's key manager", cause, name, strings.Join(left, ", "))
	}
	return cause
}

// credentialRoles are the roles a credential reads: a forward credential's fields, otherwise the roles of its
// provider (see AcceptedRoles).
func credentialRoles(cfg *config.Config, cred config.Credential) []string {
	if cred.Forward {
		return cred.Fields
	}
	return AcceptedRoles(cfg, cred)
}

// vaultRoles are the roles a vault entry of cred can hold: every compiled role and its fields, so no secret
// of the entry is overlooked.
func vaultRoles(cfg *config.Config, cred config.Credential) []string {
	roles := cfg.SecretRoles()
	for _, field := range cred.Fields {
		if !slices.Contains(roles, field) {
			roles = append(roles, field)
		}
	}
	return roles
}

// readersOf lists the connections of cfg that read credential or release it as a forward credential, sorted.
func readersOf(cfg *config.Config, credential string) []RenameReader {
	var readers []RenameReader
	for name, connection := range cfg.Connections {
		if connection.Credential == credential || slices.Contains(connection.ForwardSecrets, credential) {
			readers = append(readers, RenameReader{Connection: name})
		}
	}
	sort.Slice(readers, func(i, j int) bool { return readers[i].Connection < readers[j].Connection })
	return readers
}

// namesCredential reports whether scope reads credential or releases it as a forward credential.
func namesCredential(scope vault.Scope, credential string) bool {
	return scope.Credential == credential || slices.ContainsFunc(scope.Forward, func(f vault.ForwardSecret) bool {
		return f.Name == credential
	})
}
