package manage

import (
	"errors"
	"sort"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// RenameFinding is one rename the vault shows that the configuration does not: a connection or credential
// renamed by hand (see HandRename), or the rest of a rename that stopped early (see CredentialLeftover and
// approval.Report.Leftovers). Entry is the name in the configuration the finding belongs to. From and To are
// the names the action takes: the old and new name of a hand rename, and for a rest the configured name and
// the name it was being renamed to. Carried names the connections whose approvals a credential's stopped
// rename carried over. Ambiguous says nothing is suggested, so there is no action.
type RenameFinding struct {
	Kind      string
	Entry     string
	From, To  string
	Leftover  bool
	Ambiguous bool
	Carried   []string
}

var (
	errAmbiguousFinding = errors.New("ambiguous: nothing is suggested, so there is no action")
	errRevertFinding    = errors.New("only the unfinished rename of a credential can be put back")
)

// RenameFindings lists every RenameFinding of cfg in v, sorted by kind and name. It reads the vault only
// where that needs no passphrase, an unencrypted one or one unlocked in this process, and finds nothing
// otherwise.
func RenameFindings(cfg *config.Config, v *vault.Vault) []RenameFinding {
	if cfg == nil || v == nil {
		return nil
	}
	state, err := v.State()
	if err != nil || (state != vault.StateUnencrypted && state != vault.StateUnlocked) {
		return nil
	}
	names, err := v.KnownNames()
	if err != nil {
		return nil
	}
	var out []RenameFinding
	for _, r := range HandRenames(cfg, names) {
		out = append(out, RenameFinding{Kind: r.Kind, Entry: r.New, From: r.Old, To: r.New, Ambiguous: r.Ambiguous})
	}
	if state == vault.StateUnlocked {
		if report, err := approval.Pending(cfg, v); err == nil {
			for _, l := range report.Leftovers {
				out = append(out, RenameFinding{Kind: RenameConnection, Entry: l.Connection,
					From: l.Connection, To: l.Approval, Leftover: true})
			}
		}
	}
	if left, err := CredentialLeftovers(cfg, v); err == nil {
		for _, l := range left {
			out = append(out, RenameFinding{Kind: RenameCredential, Entry: l.Credential,
				From: l.Credential, To: l.New, Leftover: true, Carried: l.Approvals})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].Leftover && !out[j].Leftover
	})
	return out
}

// CanRevert reports whether PlanRevertRename can put the finding back: only a credential's stopped rename.
func (f RenameFinding) CanRevert() bool { return f.Leftover && f.Kind == RenameCredential }

// PlanFinding plans the action for f: putting it back when revert, finishing a rest, else adopting a hand
// rename. approve is as for PlanRename. An ambiguous finding, and a revert of one that cannot be, is refused.
func (s *Service) PlanFinding(f RenameFinding, revert, approve bool) (*RenamePlan, error) {
	switch {
	case f.Ambiguous:
		return nil, &RenameRefusedError{errAmbiguousFinding}
	case revert && !f.CanRevert():
		return nil, &RenameRefusedError{errRevertFinding}
	case revert:
		return s.PlanRevertRename(f.From, f.To, approve)
	case f.Leftover:
		return s.PlanRename(f.Kind, f.From, f.To, approve)
	}
	return s.PlanAdopt(f.Kind, f.From, f.To, approve)
}
