package manage

import (
	"context"
	"errors"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// ApprovalPolicy says which connections a management surface lets ApproveAfterChange approve after a change.
// Approving is a security decision, so every surface names its policy; the core never picks one for it.
type ApprovalPolicy int

const (
	// ApprovalNone approves nothing. A surface that leaves the decision to a person uses it explicitly.
	ApprovalNone ApprovalPolicy = iota
	// ApprovalDirectOnly approves only the connection the change saved directly by name, and only when the
	// connection's own form shows everything that opened it. Other connections the change opened stay open.
	ApprovalDirectOnly
	// ApprovalSweepNewlyOpened approves the direct connection by the same rule and also every connection the
	// change newly opened, judged against the ApprovalSnapshot taken before it.
	ApprovalSweepNewlyOpened
)

// ApprovalSnapshot is what approval.Pending finds about a configuration, in an encrypted and unlocked vault
// only, captured before a change. It is the starting point ApprovalSweepNewlyOpened compares with.
type ApprovalSnapshot struct {
	report approval.Report
	ok     bool
}

// Valid reports whether the snapshot has something to compare with. It does not for a missing vault
// handle, an unreadable vault state, a vault that exists but is not encrypted (it binds no connection,
// whatever the change does), a locked vault (nothing here unlocks it merely to compare), and a failed
// check. It does for a vault that does not exist yet: nothing is open before it exists either, so a change
// that creates and encrypts the vault in one step can still tell what it newly opened.
func (s ApprovalSnapshot) Valid() bool { return s.ok }

// SnapshotApprovals captures the approvals of cfg in v right now.
func SnapshotApprovals(v *vault.Vault, cfg *config.Config) ApprovalSnapshot {
	if v == nil {
		return ApprovalSnapshot{}
	}
	state, err := v.State()
	if err != nil {
		return ApprovalSnapshot{}
	}
	if state == vault.StateAbsent {
		return ApprovalSnapshot{ok: true}
	}
	if state != vault.StateUnlocked {
		return ApprovalSnapshot{}
	}
	report, err := approval.Pending(cfg, v)
	if err != nil {
		return ApprovalSnapshot{}
	}
	return ApprovalSnapshot{report: report, ok: true}
}

// ApprovalResult is the outcome of ApproveAfterChange. Failing to approve never undoes the change that
// already succeeded, so failures are reported here and never as an error of the change.
type ApprovalResult struct {
	// Approved lists the connections approved, sorted.
	Approved []string
	// StayedOpen names the direct connection that was left open because it is open for a reason its own
	// form never shows; "" when it was not left open. StayReason is the short reason, and ForwardChanged
	// says the reason is a changed release of payload secrets, which only an explicit approval releases.
	StayedOpen     string
	StayReason     string
	ForwardChanged bool
	// CheckErr is set when the open connections could not be determined; nothing was approved then.
	CheckErr error
	// ApproveErr is set when approving failed; nothing was approved then. Neither error carries a secret.
	ApproveErr error
	// Warning is a hint from handing the approvals to a running vault process, "" when there is none.
	Warning string
}

// ApproveAfterChange approves connections in v after a change that already succeeded, by policy. candidate
// is the configuration as saved; before is the snapshot taken before the change (used by
// ApprovalSweepNewlyOpened only); direct names the connection saved directly by name, "" for none.
//
// The direct connection is approved unless it is open for a reason the form never showed (see
// approval.DirectApprovable) or its release of payload secrets changed: such a connection stays open and the
// result says why. ApprovalSweepNewlyOpened additionally approves every connection open now that was not
// open in before, and approves nothing when before is not Valid. ApprovalDirectOnly approves only in an
// unlocked vault and needs no snapshot; it ignores before.
func ApproveAfterChange(ctx context.Context, v *vault.Vault, before ApprovalSnapshot,
	candidate *config.Config, direct string, policy ApprovalPolicy) ApprovalResult {
	var res ApprovalResult
	if v == nil {
		return res
	}
	switch policy {
	case ApprovalDirectOnly:
		if direct == "" {
			return res
		}
		state, err := v.State()
		if err != nil || state != vault.StateUnlocked {
			return res
		}
	case ApprovalSweepNewlyOpened:
		if !before.ok {
			return res
		}
	default:
		return res
	}

	report, err := approval.Pending(candidate, v)
	if err != nil {
		res.CheckErr = err
		return res
	}
	beforeOpen := map[string]bool{}
	for _, c := range before.report.Open {
		beforeOpen[c.Connection] = true
	}
	directChange, directWasOpen := OpenChange(before.report, direct)

	var names []string
	for _, c := range report.Open {
		switch {
		case direct != "" && c.Connection == direct:
			approvable := !releaseChanged(c)
			if policy == ApprovalDirectOnly {
				approvable = approvable && approval.DirectApprovable(c)
			} else {
				approvable = approvable && (!directWasOpen || approval.DirectApprovable(directChange))
			}
			if approvable {
				names = append(names, c.Connection)
				continue
			}
			// Open for a reason the connection's own form never showed: only an explicit approval may
			// release it.
			res.StayedOpen = c.Connection
			res.StayReason = StaysOpenReason(c)
			res.ForwardChanged = releaseChanged(c)
		case policy == ApprovalSweepNewlyOpened && !beforeOpen[c.Connection]:
			names = append(names, c.Connection)
		}
	}
	if len(names) == 0 {
		return res
	}
	approved, warning, err := approval.Approve(ctx, candidate, v, names)
	if err != nil {
		res.ApproveErr = err
		return res
	}
	res.Approved, res.Warning = approved, warning
	return res
}

// OpenChange is the change report holds for name, and whether it held one at all (name was open).
func OpenChange(report approval.Report, name string) (approval.Change, bool) {
	for _, c := range report.Open {
		if c.Connection == name {
			return c, true
		}
	}
	return approval.Change{}, false
}

// StaysOpenReason is the short reason change stays open.
func StaysOpenReason(c approval.Change) string {
	if c.New {
		return "new connection, not yet approved"
	}
	fields := make([]string, len(c.Fields))
	for i, f := range c.Fields {
		fields[i] = f.Field
	}
	return strings.Join(fields, ", ") + " changed"
}

// releaseChanged reports whether an approved connection's release of payload secrets differs from what was
// approved. Such a change is a decision of its own: no save approves it in passing, not even the connection's
// own form, which shows the list but not what each listed credential's fields mean for an approval. A
// connection that was never approved is new, and a person creating it has chosen its release.
func releaseChanged(c approval.Change) bool {
	if c.New {
		return false
	}
	for _, f := range c.Fields {
		if f.Field == approval.FieldForward {
			return true
		}
	}
	return false
}

// EncryptApproval is the outcome of ApproveOnEncrypt.
type EncryptApproval struct {
	// Approved lists the connections approved, sorted.
	Approved []string
	// ConfigErr is set when the configuration could not be read; nothing was approved then. A configuration
	// that does not exist yet is not an error: there is nothing to approve.
	ConfigErr error
	// ApproveErr is set when approving failed; nothing was approved then.
	ApproveErr error
	// Warning is a hint from handing the approvals to a running vault process, "" when there is none.
	Warning string
}

// ApproveOnEncrypt approves every connection that reads a secret the vault v just encrypted, since the
// passphrase was proven a moment ago: from now on the vault hands a secret only to a connection it approved
// as it is configured now. load reads the saved configuration. A configuration that cannot be read approves
// nothing and is reported, and the encryption stands.
func ApproveOnEncrypt(ctx context.Context, v *vault.Vault, load func() (*config.Config, error)) EncryptApproval {
	var res EncryptApproval
	if v == nil || load == nil {
		return res
	}
	cfg, err := load()
	var missing *config.NotFoundError
	switch {
	case errors.As(err, &missing):
		return res
	case err != nil:
		res.ConfigErr = err
		return res
	}
	approved, warning, err := approval.Approve(ctx, cfg, v, nil)
	if err != nil {
		res.ApproveErr = err
		return res
	}
	res.Approved, res.Warning = approved, warning
	return res
}
