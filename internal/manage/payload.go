package manage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/approval"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
	"github.com/castrowithcee/qatlas-cli/internal/vaultproc"
)

// PayloadFieldProblem asks the configuration core whether name is a valid field name of a payload credential,
// so the rule stays the core's. It returns "" for a valid one and otherwise the reason to show.
func PayloadFieldProblem(name string) string {
	trial := config.New()
	_ = trial.SetCredential("trial", config.Credential{
		Type: config.CredentialTypeVault, Forward: true, Fields: []string{name}})
	err := trial.Validate()
	if err == nil {
		return ""
	}
	for _, line := range strings.Split(err.Error(), "\n") {
		if _, rule, ok := strings.Cut(line, "fields[0]: "); ok {
			return "the field name is refused: " + rule
		}
	}
	return "the field name is refused"
}

// PayloadSave describes the save of one payload credential. The surface builds Candidate from its form: the
// credential entry set in a clone of Previous and passed through Validate. No value is ever part of a result.
type PayloadSave struct {
	// Previous is the configuration as it was loaded, Base its revision.
	Previous *config.Config
	Base     config.Revision
	// Candidate is the configuration to save, with the entry Name set and validated.
	Candidate *config.Config
	// Name is the saved credential. Editing is the name of the stored credential being edited, "" for a new one.
	Name, Editing string
	// Roles are the fields whose value in Values is stored; a field of the entry missing from both keeps
	// the value it has.
	Roles  []string
	Values map[string]string
	// Offer is asked for a passphrase only when a vault value here would be the vault's very first.
	Offer vault.PassphraseFunc
}

// PayloadSaved is the outcome of SavePayload.
type PayloadSaved struct {
	// Warning chains, in order and joined by "; ", the redacted warnings of the vault process hand-over, the
	// connection log and the vault process taking the removals; "" for none.
	Warning string
	// Leftover names the removed fields whose stored value could not be removed again.
	Leftover []string
}

// LeftoverNote is the sentence for a removed field of credential whose stored value could not be removed.
func LeftoverNote(credential, field string) string {
	return fmt.Sprintf("warning: the stored value of %s could not be removed, remove it with "+
		"'qatlas credential delete %s %s'", field, credential, field)
}

// SavePayload commits the values and the configuration entry together (see CommitSecrets), logs the changed
// connections, and then removes the stored value of every field the edit dropped, handing a vault removal on
// to a vault process that holds the vault unlocked. An error from the commit means nothing was changed. The
// removals run only after a successful commit; one that fails is reported in Leftover, not as an error.
func (s *Service) SavePayload(p PayloadSave) (PayloadSaved, error) {
	var res PayloadSaved
	cred := p.Candidate.Credentials[p.Name]
	toVault := cred.Type == config.CredentialTypeVault
	var removed []string
	if p.Editing != "" {
		for _, old := range p.Previous.Credentials[p.Editing].Fields {
			if !contains(cred.Fields, old) {
				removed = append(removed, old)
			}
		}
	}
	warning, err := s.CommitSecrets(p.Candidate, p.Base, p.Name, toVault, p.Roles, p.Values, p.Offer)
	if err != nil {
		return res, err
	}
	warnings := []string{warning, s.RecordConnections(p.Previous, p.Candidate)}
	for _, field := range removed {
		var rmErr error
		if toVault {
			rmErr = s.secrets.DeleteVault(p.Name, field)
		} else {
			_, rmErr = s.secrets.Delete(p.Name, field)
		}
		if rmErr != nil && !errors.Is(rmErr, secret.ErrNoEntry) {
			res.Leftover = append(res.Leftover, field)
			continue
		}
		if toVault {
			warnings = append(warnings, s.SyncVaultProcess(context.Background(),
				func(ctx context.Context, c *vaultproc.Client) error { return c.Delete(ctx, p.Name, field) }))
		}
	}
	res.Warning = chainWarnings(warnings...)
	return res, nil
}

// chainWarnings joins the non-empty warnings with "; ".
func chainWarnings(warnings ...string) string {
	var kept []string
	for _, w := range warnings {
		if w != "" {
			kept = append(kept, w)
		}
	}
	return strings.Join(kept, "; ")
}

// ForwardChoices are the payload credentials a connection may release, in name order.
func ForwardChoices(cfg *config.Config) []string {
	var names []string
	for name, cred := range cfg.Credentials {
		if cred.Forward {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// ForwardBinding says whether an approval binds the release of payload credentials.
type ForwardBinding int

const (
	// ForwardBound: an encrypted vault is in use and the connection's own credential is in it, so an approval
	// binds the release.
	ForwardBound ForwardBinding = iota
	// ForwardUnbound: no encrypted vault is in use, so only the forward_secrets list limits the release.
	ForwardUnbound
	// ForwardCredentialOutside: the connection's own credential is not in the vault, so no approval binds the
	// release although a vault exists.
	ForwardCredentialOutside
)

// ForwardBinding reports how the release of payload credentials is bound. connCredential is the credential
// of the connection in question, "" when that is not known yet. An unreadable vault state counts as unbound.
func (s *Service) ForwardBinding(cfg *config.Config, connCredential string) ForwardBinding {
	v := s.vault()
	if v == nil {
		return ForwardUnbound
	}
	state, err := v.State()
	if err != nil || (state != vault.StateLocked && state != vault.StateUnlocked) {
		return ForwardUnbound
	}
	if connCredential != "" && cfg.Credentials[connCredential].Type != config.CredentialTypeVault {
		return ForwardCredentialOutside
	}
	return ForwardBound
}

// ForwardOpen names the connections of cfg that release the payload credential and now wait for a person's
// approval, in report order. It answers only from an unlocked, encrypted vault and is empty otherwise, also
// when the check fails: nothing can be said then.
func (s *Service) ForwardOpen(cfg *config.Config, credential string) []string {
	v := s.vault()
	if v == nil {
		return nil
	}
	if state, err := v.State(); err != nil || state != vault.StateUnlocked {
		return nil
	}
	report, err := approval.Pending(cfg, v)
	if err != nil {
		return nil
	}
	var open []string
	for _, c := range report.Open {
		if contains(cfg.Connections[c.Connection].ForwardSecrets, credential) {
			open = append(open, c.Connection)
		}
	}
	return open
}

// RefusedReleaseError reports a release list the configuration refuses. Its text is the configuration's.
type RefusedReleaseError struct{ Err error }

func (e *RefusedReleaseError) Error() string { return e.Err.Error() }

func (e *RefusedReleaseError) Unwrap() error { return e.Err }

// ReleaseCheck says what is known about the approval of a connection after its release list was saved.
type ReleaseCheck int

const (
	// ReleaseUnchecked: nothing can be said, for lack of an unlocked vault.
	ReleaseUnchecked ReleaseCheck = iota
	// ReleaseLocked: the vault is locked, so the approval was not checked.
	ReleaseLocked
	// ReleaseCheckFailed: the open approvals could not be determined; ReleaseSaved.CheckErr says why.
	ReleaseCheckFailed
	// ReleaseOpen: the connection waits for approval; ReleaseSaved.Change says why.
	ReleaseOpen
	// ReleaseSettled: the connection is not open.
	ReleaseSettled
)

// ReleaseSaved is the outcome of SaveForwardRelease. It never approves anything.
type ReleaseSaved struct {
	// Config is the configuration as saved.
	Config *config.Config
	// Warning is the redacted warning of the connection log, "" for none.
	Warning string
	// Check, CheckErr and Change describe the approval of the connection (see ReleaseCheck). CheckErr carries
	// no secret but is the caller's to redact like any error text.
	Check    ReleaseCheck
	CheckErr error
	Change   approval.Change
}

// SaveForwardRelease saves the forward_secrets list of the existing connection name in cfg, loaded at rev, and
// writes the configuration only: a changed release of an approved connection stays open until a person
// approves it. A list the configuration refuses is a *RefusedReleaseError; a file that changed since rev is a
// *config.ConflictError. cfg is not changed.
func (s *Service) SaveForwardRelease(cfg *config.Config, rev config.Revision, name string,
	forward []string) (ReleaseSaved, error) {
	var res ReleaseSaved
	conn, ok := cfg.Connections[name]
	if !ok {
		return res, &config.NotThereError{Kind: "connection", Name: name}
	}
	conn.ForwardSecrets = append([]string(nil), forward...)
	cand := cfg.Clone()
	if err := cand.SetConnection(name, conn); err != nil {
		return res, &RefusedReleaseError{Err: err}
	}
	if err := cand.Validate(); err != nil {
		return res, &RefusedReleaseError{Err: err}
	}
	logged, err := s.SaveConfig(cfg, cand, rev)
	if err != nil {
		return res, err
	}
	res.Config, res.Warning = cand, logged
	res.Check, res.Change, res.CheckErr = s.releaseCheck(cand, name)
	return res, nil
}

func (s *Service) releaseCheck(cand *config.Config, name string) (ReleaseCheck, approval.Change, error) {
	v := s.vault()
	if v == nil {
		return ReleaseUnchecked, approval.Change{}, nil
	}
	state, err := v.State()
	switch {
	case err != nil:
		return ReleaseUnchecked, approval.Change{}, nil
	case state == vault.StateLocked:
		return ReleaseLocked, approval.Change{}, nil
	case state != vault.StateUnlocked:
		return ReleaseUnchecked, approval.Change{}, nil
	}
	report, err := approval.Pending(cand, v)
	if err != nil {
		return ReleaseCheckFailed, approval.Change{}, err
	}
	if change, open := OpenChange(report, name); open {
		return ReleaseOpen, change, nil
	}
	return ReleaseSettled, approval.Change{}, nil
}
