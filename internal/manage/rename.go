package manage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// The kinds of entry a rename applies to.
const (
	RenameService    = "service"
	RenameConnection = "connection"
	RenameCredential = "credential"
)

// ErrRenameUnsupported reports a kind of entry that cannot be renamed.
var ErrRenameUnsupported = errors.New("this kind of entry cannot be renamed")

// RenameRefusedError reports a rename that the names themselves rule out, before anything was written: the
// kind or the entry is unknown, the new name is invalid or taken, or the vault or, for a credential, the
// system keyring or the unencrypted fallback file already gives that name something.
type RenameRefusedError struct{ Err error }

func (e *RenameRefusedError) Error() string { return e.Err.Error() }

func (e *RenameRefusedError) Unwrap() error { return e.Err }

// The approval states of a connection a RenamePlan names.
const (
	// ApprovalValid: the vault approved the connection as it is configured now.
	ApprovalValid = "approved"
	// ApprovalOpen: the vault holds an approval of the connection that no longer matches it.
	ApprovalOpen = "open"
)

// RenamePlan is what renaming one entry changes, found without changing anything: the dry run every surface
// shows, and what Rename carries out once it was confirmed.
type RenamePlan struct {
	// Kind is RenameService, RenameConnection or RenameCredential; Old and New are the names.
	Kind, Old, New string
	// ID is the config id of the entry, which the rename keeps.
	ID string
	// References are the configuration paths besides the entry itself that name it and change with it.
	References []string
	// Approval is the approval state of a renamed connection, ApprovalValid or ApprovalOpen, or "" where the
	// vault holds none or binds no approvals. CarryApproval says the rename moves it to the new name as it
	// is, so a valid one stays valid and an open one stays open.
	Approval      string
	CarryApproval bool
	// LapsedTokens names the agent tokens that name the connection as a vorbild, or, for a credential, whose
	// vorbild has an approval naming the credential that the rename does not carry over; the rename ends that
	// cover.
	LapsedTokens []string
	// VaultUnread is true when the vault is encrypted but not unlocked in this process, so approvals and agent
	// tokens are neither shown nor checked; Rename refuses such a plan.
	VaultUnread bool
	// Outside names what may refer to the entry by its name and is out of qatlas's reach.
	Outside []string
	// Adopt says the configuration carries the new name already (see PlanAdopt): References are what the hand
	// rename changed there, and Rename writes the configuration as it is.
	Adopt bool

	// The rest describes a credential's rename.
	CredentialPlan

	cfg  *config.Config
	base config.Revision
}

// RenameResult is what Rename did beyond the configuration. VaultCopies are the backups of the vault's files
// taken before the vault was written; Warnings are the redacted failures after the commit, none of which
// undoes the rename.
type RenameResult struct {
	VaultCopies []string
	Warnings    []string
	// Carried names the connections whose approvals a credential's rename carried over; EnvVars are the
	// plan's.
	Carried []string
	EnvVars []EnvRename
}

// PlanRename finds what renaming the entry old of kind to name changes, reading the configuration and, for a
// connection or a credential, the approvals and agent tokens of a vault unlocked in this process; for a
// credential also what the vault and the system keyring hold for it (see planCredential). It writes nothing.
// approve says that the surface acts in an active admin session, where saving counts as approval: only then
// does the rename carry approvals over; without it the approvals are left as they are.
//
// A name that is no valid name, is taken, or would inherit what the vault holds for another connection - an
// approval under that name that is not the rest of renaming this one, or an agent token that names it as a
// vorbild - is refused.
func (s *Service) PlanRename(kind, old, name string, approve bool) (*RenamePlan, error) {
	cfg, rev, err := s.Load()
	if err != nil {
		return nil, err
	}
	return s.planRename(cfg, rev, kind, old, name, approve)
}

// planRename is PlanRename from cfg, the configuration the plan starts from, whose file has the revision rev
// that Rename checks.
func (s *Service) planRename(cfg *config.Config, rev config.Revision, kind, old, name string,
	approve bool) (*RenamePlan, error) {
	cand := cfg.Clone()
	cand.AssignIDs()
	before := cand.Clone()
	plan := &RenamePlan{Kind: kind, Old: old, New: name, cfg: cand, base: rev}
	var err error
	switch kind {
	case RenameService:
		plan.References, err = cand.RenameService(old, name)
		plan.ID = cand.Services[name].ID
		plan.Outside = []string{"instructions, scripts and documents that name the service " + old}
	case RenameConnection:
		plan.References, err = cand.RenameConnection(old, name)
		plan.ID = cand.Connections[name].ID
		plan.Outside = []string{
			"agent calls and scripts that select the connection " + old + " by name, such as --connection " + old,
			"instructions and documents that name the connection " + old,
			"invocation log entries written before keep the name " + old,
			"environment variables: none is named after a connection",
		}
	case RenameCredential:
		plan.References, err = cand.RenameCredential(old, name)
		plan.ID = cand.Credentials[name].ID
	default:
		return nil, &RenameRefusedError{fmt.Errorf("%s: %w", kind, ErrRenameUnsupported)}
	}
	if err != nil {
		return nil, &RenameRefusedError{err}
	}
	if err := cand.Validate(); err != nil {
		return nil, &config.InvalidError{Path: s.store.Path(), Err: err}
	}
	switch kind {
	case RenameConnection:
		if err := s.planVault(plan, cfg, approve); err != nil {
			return nil, err
		}
	case RenameCredential:
		if err := s.planCredential(plan, before, approve); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

// planVault fills in what the vault holds for the connection plan renames and refuses a new name the vault
// already gives something to.
func (s *Service) planVault(plan *RenamePlan, cfg *config.Config, approve bool) error {
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
	case vault.StateUnlocked:
	default:
		return nil
	}
	tokens, err := v.Tokens()
	if err != nil {
		return err
	}
	for _, token := range tokens {
		if slices.Contains(token.Models, plan.New) {
			return &RenameRefusedError{fmt.Errorf("agent token %s names %s as a vorbild connection; renaming a "+
				"connection to that name would extend the token, so revoke the token first", token.Name, plan.New)}
		}
		if slices.Contains(token.Models, plan.Old) {
			plan.LapsedTokens = append(plan.LapsedTokens, token.Name)
		}
	}
	approvals, err := v.Approvals()
	if err != nil {
		return err
	}
	if taken, ok := approvals[plan.New]; ok && (!approve || taken.ConnectionID == "" || taken.ConnectionID != plan.ID) {
		return &RenameRefusedError{fmt.Errorf("the vault holds a stale approval under the name %s; remove it "+
			"with 'qatlas vault approve' before a connection takes that name", plan.New)}
	}
	if _, ok := approvals[plan.Old]; !ok {
		return nil
	}
	report, err := approval.Pending(cfg, v)
	if err != nil {
		return err
	}
	plan.Approval = ApprovalValid
	if _, open := OpenChange(report, plan.Old); open {
		plan.Approval = ApprovalOpen
	}
	plan.CarryApproval = approve
	return nil
}

// saveRename is a seam so a test can fail the commit after the vault was written.
var saveRename = func(save func(*config.Config) error, cfg *config.Config) error { return save(cfg) }

// Rename carries out plan, which PlanRename made, only if the configuration file still has the revision the
// plan was based on, under the configuration's lock: it backs up the vault's files, carries the approval of a
// renamed connection over to the new name, and saves the configuration, which backs up the file and is the
// commit point. A failed save puts the vault's approvals back as they were. A vault that is encrypted but not
// unlocked in this process is refused before anything is written.
//
// Once committed it removes the approval under the old name, hands the approvals to a vault process that
// holds the vault unlocked, and logs a connection's rename as one config.connection.rename entry. A failure
// there leaves at most a stale approval that names its connection (see approval.Leftover) and is returned as
// a warning. A credential's rename is carried out by renameCredential, and a plan of PlanRevertRename by
// revertCredential.
func (s *Service) Rename(ctx context.Context, plan *RenamePlan) (RenameResult, error) {
	var res RenameResult
	if plan == nil || plan.cfg == nil {
		return res, errors.New("no rename was planned")
	}
	if plan.VaultUnread {
		return res, vault.ErrNotUnlocked
	}
	v := s.vault()
	if v != nil {
		state, err := v.State()
		if err != nil {
			return res, err
		}
		if state == vault.StateLocked {
			return res, vault.ErrNotUnlocked
		}
	}
	if plan.Revert {
		return s.revertCredential(ctx, plan, v)
	}
	if plan.Kind == RenameCredential {
		return s.renameCredential(ctx, plan, v)
	}
	carried := false
	err := s.store.Transact(plan.base, func(save func(*config.Config) error) error {
		undo := func() error { return nil }
		var copies []string
		discard := func() {
			for _, path := range copies {
				_ = os.Remove(path)
			}
		}
		if plan.CarryApproval {
			var err error
			copies, err = s.backupVaultFiles(v)
			if err != nil {
				discard()
				return err
			}
			carried, undo, err = v.CarryApproval(plan.Old, plan.New, plan.ID)
			if err != nil || !carried {
				discard()
				copies = nil
			}
			if err != nil {
				return fmt.Errorf("carrying the approval of %s over: %w", plan.Old, err)
			}
		}
		if err := saveRename(save, plan.cfg); err != nil {
			if carried {
				if undoErr := undo(); undoErr != nil {
					// The copies are the only way back for the vault now, so they stay.
					return fmt.Errorf("%w; the approvals in the vault could not be put back as they were (%v), "+
						"its copies are %s", err, undoErr, strings.Join(copies, ", "))
				}
				carried = false
			}
			discard()
			return err
		}
		res.VaultCopies = copies
		return nil
	})
	if err != nil {
		return res, err
	}
	if carried {
		if _, err := v.Revoke([]string{plan.Old}); err != nil {
			res.Warnings = append(res.Warnings, s.redact(fmt.Sprintf("warning: the approval under the old name "+
				"%s could not be removed: %v; it approves nothing while no connection has that name, and "+
				"'qatlas vault approve' removes it", plan.Old, err)))
		}
		if warning := s.SyncVaultProcess(ctx, func(ctx context.Context, client *vaultproc.Client) error {
			bindings, err := v.Bindings()
			if err != nil {
				return err
			}
			return client.Bind(ctx, bindings)
		}); warning != "" {
			res.Warnings = append(res.Warnings, warning)
		}
	}
	if plan.Kind == RenameConnection {
		recorder := connlog.NewRecorder(s.surface, s.store.Path(), plan.cfg.LogRetentionDays(), v, s.processOK)
		if err := recorder.RecordRename(plan.Old, plan.New, plan.ID); err != nil {
			res.Warnings = append(res.Warnings, s.redact("warning: "+err.Error()))
		}
	}
	return res, nil
}
