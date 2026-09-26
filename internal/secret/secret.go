// Package secret resolves the secrets a credential stands for. Resolution is provider independent and
// follows one fixed cascade, where the first stage that delivers wins:
//
//  1. the environment variable
//  2. the system credential store
//  3. the plaintext fallback file, and only when it was switched on explicitly
//
// The order follows the rule that the more explicit and the more short-lived source wins, the same order
// gh, the AWS CLI and kubectl use. Because overriding is allowed, every caller is told which stage
// delivered: otherwise a forgotten environment variable would shadow the credential store silently.
//
// The cascade describes how a credential is resolved that does not say where its secrets are. A credential
// of type env does say it: it names its variable, and that description is exhaustive. Resolution for such a
// credential therefore ends after stage one. Anything else would let a developer's local credential store
// or plaintext file stand in for a variable a CI run forgot to set, which is the opposite of what naming
// the variable was for.
//
// A resolved value is handed to the redactor and to the caller and to nobody else. It is never written to
// the configuration, never logged, and never part of an error message.
package secret

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// Source names the stage of the cascade that delivered a secret. The value is what a user sees, so it is
// prose rather than an identifier.
type Source string

// The stages of the cascade, plus the outcome that no stage delivered.
const (
	SourceEnv       Source = "environment variable"
	SourceStore     Source = "credential store"
	SourcePlaintext Source = "plaintext file"
	SourceVault     Source = "vault"
	SourceMissing   Source = "missing"
)

// Value is a resolved secret together with the stage that produced it and the stages that were tried
// before. Only the resolver and the provider that authenticates with it ever see Secret.
type Value struct {
	Secret  string
	Source  Source
	Checked []string
}

// MissingSecretError reports that a credential does not yield a secret for a role. It is a usage problem.
//
// The message points at the configuration key that needs fixing and never repeats what the user wrote
// there. The configured text is treated as a possible secret, because a user who pastes a token into the
// field instead of a variable name writes something that a name rule cannot tell apart from a real name: a
// BookStack token is letters and digits, and so is a legal variable name. Naming the key and listing the
// stages that were checked keeps the message actionable without echoing the input.
//
// Err carries what a stage had to say beyond not delivering, for example that the plaintext fallback was
// refused and how to make it usable again. It is written by the resolver, holds no configured text and no
// stored value, and is unwrapped, so the same underlying state is classified the same way whether it was
// hit while reading, writing, or deleting.
type MissingSecretError struct {
	Credential string
	Role       string
	Type       string
	Checked    []string
	Err        error
}

func (e *MissingSecretError) Error() string {
	// A keyring or vault credential has no values section, so naming one would send the user to a key that
	// does not exist in their file.
	key := fmt.Sprintf("credentials.%s.values.%s", e.Credential, e.Role)
	if e.Type == config.CredentialTypeKeyring || e.Type == config.CredentialTypeVault {
		key = fmt.Sprintf("credentials.%s, secret role %s", e.Credential, e.Role)
	}

	parts := []string{
		key + " does not yield a secret",
		"checked: " + strings.Join(e.Checked, ", "),
	}
	if e.Err != nil {
		parts = append(parts, e.Err.Error())
	}
	return strings.Join(append(parts, e.remedy()), "; ")
}

func (e *MissingSecretError) Unwrap() error { return e.Err }

// remedy names the way out. It differs by credential type, and it stays within the same rule as the rest
// of the message: it names commands, keys, and the derived variable, never a configured text, a stored
// value, or anything read from a file.
//
// A keyring that is locked, unreachable or switched off is named first, with what to do on this platform:
// storing the secret again would run into the same keyring. The variable is the way that works in a session
// without a desktop, such as SSH, where nobody can unlock the keyring.
func (e *MissingSecretError) remedy() string {
	if e.Type == config.CredentialTypeKeyring {
		env := DerivedEnvName(e.Credential, e.Role)
		switch state := StoreStage(e.Checked); state {
		case StoreLocked, StoreUnavailable, StoreTimedOut:
			return fmt.Sprintf("%s; or export %s for this session (a built-in encrypted store for sessions "+
				"without a desktop is not available yet)", StoreAdvice(state, runtime.GOOS), env)
		case StoreOff:
			return fmt.Sprintf("%s; or export %s", StoreAdvice(state, runtime.GOOS), env)
		}
		return fmt.Sprintf("store it in the system keyring with 'qatlas credential set %s %s', or export %s",
			e.Credential, e.Role, env)
	}
	if e.Type == config.CredentialTypeVault {
		env := DerivedEnvName(e.Credential, e.Role)
		return fmt.Sprintf("store it in the vault with 'qatlas credential set %s %s', or export %s",
			e.Credential, e.Role, env)
	}
	return fmt.Sprintf("set the environment variable that credentials.%s.values.%s names, or change that "+
		"credential to type keyring and use 'qatlas credential set'", e.Credential, e.Role)
}

// VaultLockedError reports that a vault is encrypted and locked, and no terminal was attached to ask for
// its passphrase. It is its own type, kept apart from MissingSecretError, so it keeps the runtime exit code
// the process already leaves for a locked credential store, rather than the usage exit code a missing
// secret gets: a locked vault is not a configuration problem, and a person who unlocks it and retries needs
// no configuration change.
//
// Credential and Role name the secret an access was resolving when the vault turned out to be locked; both
// are empty when the locked vault itself, rather than one secret in it, was the target, for example
// 'qatlas vault unlock' run without a terminal.
type VaultLockedError struct {
	Credential string
	Role       string
}

func (e *VaultLockedError) Error() string {
	if e.Credential == "" {
		return "the vault is locked, and no terminal is attached to ask for its passphrase"
	}
	return fmt.Sprintf("the vault holding the secret of %s.%s is locked, and no terminal is attached to ask "+
		"for its passphrase", e.Credential, e.Role)
}

// Errors a Store reports. Anything else from a store is treated like ErrUnavailable, because a store that
// fails in an unexpected way must not be a dead end either.
var (
	// ErrNoEntry reports that the store answered and holds nothing under the key.
	ErrNoEntry = errors.New("the credential store holds no entry")
	// ErrUnavailable reports that no credential store could be reached, for example because no secret
	// service is running.
	ErrUnavailable = errors.New("the credential store is unavailable")
	// ErrLocked reports a reachable system credential store whose collection still needs the user's local
	// login password. It also wraps ErrUnavailable because the current operation still cannot use it.
	ErrLocked = errors.New("the credential store is locked")
	// ErrDisabled reports that the system credential store was switched off deliberately.
	ErrDisabled = errors.New("the credential store is switched off")
	// ErrTimedOut reports a store that did not answer inside the deadline. It always accompanies
	// ErrUnavailable: to the cascade a store that never answers is a store that cannot be reached.
	ErrTimedOut = errors.New("the credential store did not answer in time")
)

// StoreState is what the system credential store said about one role. It is typed so a user interface can
// explain the store and name the way out without reading prose, and like every other answer here it never
// carries a value. The values are also the words the store stage uses in Checked.
type StoreState string

// The states a store can be in for one role. StoreNotAsked is the empty value: the environment delivered
// first, or the credential names its variables and no store is consulted at all.
const (
	StoreNotAsked    StoreState = ""
	StoreHolds       StoreState = "stored"
	StoreEmpty       StoreState = "no entry"
	StoreLocked      StoreState = "locked"
	StoreUnavailable StoreState = "unavailable"
	StoreTimedOut    StoreState = "timed out"
	StoreOff         StoreState = "switched off"
)

// StoreStateOf classifies what a store operation returned. Anything unexpected counts as unavailable, the
// same rule the cascade follows.
func StoreStateOf(err error) StoreState {
	switch {
	case err == nil:
		return StoreHolds
	case errors.Is(err, ErrNoEntry):
		return StoreEmpty
	case errors.Is(err, ErrDisabled):
		return StoreOff
	case errors.Is(err, ErrTimedOut):
		return StoreTimedOut
	case errors.Is(err, ErrLocked):
		return StoreLocked
	default:
		return StoreUnavailable
	}
}

// StoreStage reads back the state the store stage reported in a list of checked stages, as Status and
// MissingSecretError hand it out. It is StoreNotAsked when the store was not consulted.
func StoreStage(checked []string) StoreState {
	prefix := string(SourceStore) + " ("
	for _, c := range checked {
		if strings.HasPrefix(c, prefix) && strings.HasSuffix(c, ")") {
			return StoreState(strings.TrimSuffix(strings.TrimPrefix(c, prefix), ")"))
		}
	}
	return StoreNotAsked
}

// Store is the system credential store. It is an interface so a test never touches the store of the
// machine it runs on, and so a platform without one can be represented instead of aborting.
//
// Get takes the context of the request that needs the secret, so a store call never outlives it.
type Store interface {
	Get(ctx context.Context, key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// StoreKey is the account name one (credential, role) pair uses inside the store. Credential and role are
// both configuration names, so the key is stable, readable in the platform's own key manager, and free of
// characters that would need quoting.
func StoreKey(credential, role string) string { return credential + "/" + role }

// EnvName returns the environment variable that can carry the secret of one (credential, role) pair.
//
// A credential of type env names its variable itself: that is the unchanged path for CI and headless use.
// Every other credential type derives the name, so stage one of the cascade exists for it too without
// writing anything into the configuration.
func EnvName(credential string, cred config.Credential, role string) string {
	if cred.Type == config.CredentialTypeEnv {
		return cred.Values[role]
	}
	return DerivedEnvName(credential, role)
}

// DerivedEnvName is the deterministic variable name of a credential that does not name one itself:
// QATLAS_<CREDENTIAL>_<ROLE>, upper-cased, with every character that is not a letter or a digit replaced
// by an underscore. The fixed prefix also guarantees a name that cannot start with a digit.
func DerivedEnvName(credential, role string) string {
	return "QATLAS_" + envPart(credential) + "_" + envPart(role)
}

func envPart(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - ('a' - 'A'))
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// Resolver resolves secrets through the cascade. It is built once per run and shared.
type Resolver struct {
	env       func(string) string
	store     Store
	plaintext *File
	vault     *vault.Vault
	// passphrase asks for the vault's passphrase interactively; see WithVault. It is never consulted while
	// Unattended.
	passphrase vault.PassphraseFunc
	redactor   *redact.Redactor

	mu         sync.Mutex
	unattended bool
	// failed is what the store said when it last failed, and failedAt when; see storeRetry.
	failed   StoreState
	failedAt time.Time
}

// storeRetry is how long the cascade skips a store that failed. A locked or unreachable store stays so for
// the next role of the same run, and asking it again would only wait a second time; one invoke ends within
// this span, so a run of the CLI asks a failing store once. A server or an interface that runs for longer
// asks again after it, so a keyring unlocked in the meantime is found.
const storeRetry = time.Minute

// New returns the resolver of a normal run: the process environment, the credential store of the
// platform, the plaintext fallback file, and the vault, all below dir, the directory holding config.yaml.
// It fails only when the store was selected with an unrecognised value.
func New(dir string, red *redact.Redactor) (*Resolver, error) {
	store, err := SystemStore()
	if err != nil {
		return nil, err
	}
	r := NewWith(os.Getenv, store, NewFile(filepath.Join(dir, FileName)), red)
	return r.WithVault(vault.New(dir), vault.ReadPassphrase), nil
}

// NewWith returns a resolver over an explicit environment, store, and fallback file. A nil file means the
// plaintext stage cannot deliver. It is how tests build a resolver that touches nothing of the machine.
func NewWith(env func(string) string, store Store, plaintext *File, red *redact.Redactor) *Resolver {
	if env == nil {
		env = func(string) string { return "" }
	}
	if store == nil {
		store = unavailableStore{err: ErrDisabled}
	}
	return &Resolver{env: env, store: store, plaintext: plaintext, redactor: red}
}

// WithVault attaches the vault v is resolved from, and ask, which prompts interactively for its
// passphrase. A resolver built with NewWith alone has no vault: a credential of type vault then reports its
// secret unavailable rather than panicking, which is what every test that does not exercise the vault
// wants. It returns r so it chains onto NewWith.
func (r *Resolver) WithVault(v *vault.Vault, ask vault.PassphraseFunc) *Resolver {
	r.vault = v
	r.passphrase = ask
	return r
}

// Unattended prepares the resolver for a server that answers requests nobody watches, such as the MCP
// broker: the store never waits for an unlock prompt, which nobody would answer, and the vault, if any, is
// never asked for its passphrase, which nobody would type.
func (r *Resolver) Unattended() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.unattended = true
}

// Resolve returns the secret of one (credential, role) pair. The credential name and its configuration
// entry are passed separately because the entry alone does not know what it is called. The store stage
// ends with ctx at the latest.
func (r *Resolver) Resolve(ctx context.Context, credential string, cred config.Credential, role string) (Value, error) {
	checked := make([]string, 0, 3)

	envName := EnvName(credential, cred, role)
	switch value := r.env(envName); {
	case envName == "":
		// Only a credential of type env can stay silent about its variable, and then the role is not
		// configured at all. The message names the stage, never the text behind the key.
		checked = append(checked, stage(SourceEnv, "not named"))
	case value != "":
		return r.deliver(value, SourceEnv, checked), nil
	default:
		checked = append(checked, stage(SourceEnv, "not set"))
	}

	// A credential of type env is fully described by the variable it names. Falling through to a store or
	// to a file would let something local answer for a variable that is simply not set, which is exactly
	// the CI and headless case this type exists for.
	if cred.Type == config.CredentialTypeEnv {
		return Value{}, missing(credential, cred, role, checked, nil)
	}

	// A vault credential is resolved from the vault alone: it never falls through to the system keyring or
	// to the plaintext fallback, the same way env stops after stage one.
	if cred.Type == config.CredentialTypeVault {
		value, found, err := r.fromVault(credential, role)
		switch {
		case err != nil && errors.Is(err, vault.ErrNoTerminal):
			return Value{}, &VaultLockedError{Credential: credential, Role: role}
		case err != nil:
			checked = append(checked, stage(SourceVault, "unavailable"))
			return Value{}, missing(credential, cred, role, checked, err)
		case found:
			return r.deliver(value, SourceVault, checked), nil
		default:
			checked = append(checked, stage(SourceVault, "no entry"))
			return Value{}, missing(credential, cred, role, checked, nil)
		}
	}

	// A machine without a running secret service must not be a dead end, so an unreachable store is one
	// more stage that did not deliver rather than a failure.
	switch value, state := r.fromStore(ctx, StoreKey(credential, role)); {
	case state == StoreHolds && value != "":
		return r.deliver(value, SourceStore, checked), nil
	case state == StoreHolds:
		checked = append(checked, stage(SourceStore, string(StoreEmpty)))
	default:
		checked = append(checked, stage(SourceStore, string(state)))
	}

	value, err := r.fallback(credential, role)
	var cause error
	var tooOpen *PermissionError
	switch {
	case err == nil && value != "":
		return r.deliver(value, SourcePlaintext, checked), nil
	case errors.Is(err, ErrNoEntry):
		checked = append(checked, stage(SourcePlaintext, "no entry"))
	case errors.Is(err, ErrDisabled):
		// The same wording for the absent file and for the file that was never switched on: to a
		// reader they are the same thing, and the difference would only hint at what is on disk.
		checked = append(checked, stage(SourcePlaintext, "not enabled"))
	case errors.As(err, &tooOpen):
		checked = append(checked, stage(SourcePlaintext, "readable by others"))
		// The refusal is actionable and names no content, so it is carried into the message and stays
		// reachable for whoever classifies the error.
		cause = tooOpen
	default:
		checked = append(checked, stage(SourcePlaintext, "unreadable"))
	}

	return Value{}, missing(credential, cred, role, checked, cause)
}

// fromStore asks the store for one key, unless it failed less than storeRetry ago; then the earlier answer
// stands without asking again.
func (r *Resolver) fromStore(ctx context.Context, key string) (string, StoreState) {
	r.mu.Lock()
	if r.failed != StoreNotAsked && time.Since(r.failedAt) < storeRetry {
		defer r.mu.Unlock()
		return "", r.failed
	}
	unattended := r.unattended
	r.mu.Unlock()

	if unattended {
		ctx = withoutPrompt(ctx)
	}
	value, err := r.store.Get(ctx, key)
	state := StoreStateOf(err)
	switch state {
	case StoreHolds, StoreEmpty:
		r.storeAnswered()
	default:
		r.mu.Lock()
		r.failed, r.failedAt = state, time.Now()
		r.mu.Unlock()
	}
	return value, state
}

// storeAnswered forgets an earlier failure once the store works again.
func (r *Resolver) storeAnswered() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed = StoreNotAsked
}

// fromVault asks the vault for one credential role, unlocking it interactively when it is encrypted and
// locked. A resolver built without WithVault reports the vault unavailable rather than reaching into a nil
// pointer, which is every test that never sets up a vault.
func (r *Resolver) fromVault(credential, role string) (string, bool, error) {
	if r.vault == nil {
		return "", false, errors.New("no vault is configured for this resolver")
	}
	ask := r.passphrase
	r.mu.Lock()
	unattended := r.unattended
	r.mu.Unlock()
	if unattended {
		ask = nil
	}
	value, found, _, err := r.vault.Get(credential, role, ask)
	if err != nil {
		return "", false, err
	}
	return value, found, nil
}

func missing(credential string, cred config.Credential, role string, checked []string, cause error) error {
	return &MissingSecretError{
		Credential: credential, Role: role, Type: cred.Type, Checked: checked, Err: cause,
	}
}

// Status reports which stage would deliver, without handing the value to the caller. A user interface asks
// here, so it can show the source of a secret it must never see.
func (r *Resolver) Status(credential string, cred config.Credential, role string) (Source, []string) {
	value, err := r.Resolve(context.Background(), credential, cred, role)
	var missing *MissingSecretError
	if errors.As(err, &missing) {
		return SourceMissing, missing.Checked
	}
	if err != nil {
		return SourceMissing, []string{err.Error()}
	}
	return value.Source, value.Checked
}

// Placement reports where a copy of one secret lies, and which places could not be asked at all. It is the
// answer to a different question than the cascade gives: not "what would be delivered now", but "what is
// stored somewhere and would be left behind".
//
// Holding and Unknown are kept apart because they call for opposite reactions. A place that answered and
// holds nothing is settled. A place that could not be asked may hold anything, so a caller that is about to
// make a stored secret unreachable has to stop rather than assume the best.
type Placement struct {
	Holding []Source
	Unknown []Source
	// Err carries what each unreachable place said, in the order of Unknown. It names files, modes and
	// switches, never a stored value.
	Err error
}

// Settled reports whether every place could be asked, so Holding is the whole truth.
func (p Placement) Settled() bool { return len(p.Unknown) == 0 }

// Stored reports which places keep an entry for one (credential, role) pair.
//
// The environment is deliberately not consulted. A variable lives in the user's shell, it is not something
// this program stored, and it orphans nothing when a credential stops reading it; counting it would let a
// set variable hide a secret that really does sit in the store. That is exactly why this question cannot be
// answered with the cascade: the cascade stops at the first stage that delivers, and stage one is the
// environment.
//
// The value that comes back from a store is discarded. It is read because asking whether an entry exists is
// the only question a credential store answers.
func (r *Resolver) Stored(credential, role string) Placement {
	var p Placement
	var causes []error

	switch value, err := r.store.Get(context.Background(), StoreKey(credential, role)); {
	case err == nil && value != "":
		p.Holding = append(p.Holding, SourceStore)
	case err == nil, errors.Is(err, ErrNoEntry):
		// The store answered and holds nothing.
	default:
		// Switched off, unreachable, timed out: all of them leave the question open. A store that was
		// skipped is not a store that is empty.
		p.Unknown = append(p.Unknown, SourceStore)
		causes = append(causes, err)
	}

	// A resolver without a fallback file has no such file to leave anything behind in.
	if r.plaintext != nil {
		switch holds, err := r.plaintext.Holds(credential, role); {
		case err != nil:
			p.Unknown = append(p.Unknown, SourcePlaintext)
			causes = append(causes, err)
		case holds:
			p.Holding = append(p.Holding, SourcePlaintext)
		}
	}

	p.Err = errors.Join(causes...)
	return p
}

// Set stores a secret for one (credential, role) pair in the system credential store. It never writes the
// plaintext fallback: that needs SetPlaintext and therefore an explicit decision.
func (r *Resolver) Set(credential, role, value string) error {
	r.register(value)
	if err := r.store.Set(StoreKey(credential, role), value); err != nil {
		return err
	}
	r.storeAnswered()
	return nil
}

// SetPlaintext writes a secret into the plaintext fallback file and switches that fallback on. It is the
// named way out for a machine without a credential store, and the only way the file ever comes into
// existence.
func (r *Resolver) SetPlaintext(credential, role, value string) error {
	if r.plaintext == nil {
		return ErrUnavailable
	}
	r.register(value)
	return r.plaintext.Set(credential, role, value)
}

// SetVault stores a secret for one (credential, role) pair in the vault. offer is asked for a passphrase
// only when this is the vault's first secret, and only then decides whether the vault becomes encrypted; a
// nil offer, or one that returns an empty passphrase, creates it unencrypted. Every later Set of any
// credential, including while the vault is encrypted and locked, needs no passphrase: it either writes the
// entry directly or queues it as a pending entry merged in on the next unlock.
func (r *Resolver) SetVault(credential, role, value string, offer vault.PassphraseFunc) error {
	if r.vault == nil {
		return ErrUnavailable
	}
	r.register(value)
	return r.vault.Set(credential, role, value, offer)
}

// DeleteVault removes one credential role from the vault. Like a vault read, it needs the passphrase when
// the vault is encrypted and locked, asked the same interactive way and never while Unattended.
func (r *Resolver) DeleteVault(credential, role string) error {
	if r.vault == nil {
		return ErrUnavailable
	}
	ask := r.passphrase
	r.mu.Lock()
	unattended := r.unattended
	r.mu.Unlock()
	if unattended {
		ask = nil
	}
	removed, err := r.vault.Delete(credential, role, ask)
	switch {
	case errors.Is(err, vault.ErrNoTerminal):
		// Deleting from an encrypted vault needs the entry to remove, which sits inside secrets.age: like a
		// read, it cannot proceed without the passphrase.
		return &VaultLockedError{Credential: credential, Role: role}
	case err != nil:
		return err
	case !removed:
		return ErrNoEntry
	}
	return nil
}

// Vault returns the vault this resolver reads and writes, so a caller can show its status or unlock it. It
// is nil when no vault is configured, which happens only in a test built directly with NewWith.
func (r *Resolver) Vault() *vault.Vault { return r.vault }

// Delete removes a secret from the credential store and from the plaintext fallback and reports where it
// was actually removed. ErrNoEntry means nothing was stored anywhere.
//
// A delete succeeds only when no stage kept an entry back. If one place could not be cleared, the result
// is a *RemainingError naming that place, even when another place was cleared: a half-done delete that
// reports success would leave a copy of the secret behind under an all-clear.
func (r *Resolver) Delete(credential, role string) ([]Source, error) {
	var cleared []Source
	var remaining []Source
	var causes []error

	// A store that was switched off deliberately is not consulted and therefore holds nothing back that
	// this run was ever going to touch. Every other failure leaves an entry that may still be there.
	switch err := r.store.Delete(StoreKey(credential, role)); {
	case err == nil:
		cleared = append(cleared, SourceStore)
	case errors.Is(err, ErrNoEntry), errors.Is(err, ErrDisabled):
	default:
		remaining = append(remaining, SourceStore)
		causes = append(causes, err)
	}

	if r.plaintext != nil {
		switch err := r.plaintext.Delete(credential, role); {
		case err == nil:
			cleared = append(cleared, SourcePlaintext)
		case errors.Is(err, ErrNoEntry), errors.Is(err, ErrDisabled):
		default:
			remaining = append(remaining, SourcePlaintext)
			causes = append(causes, err)
		}
	}

	// Every place must be clear before a delete may call itself done. Reporting the part that worked and
	// staying silent about the copy that is still on disk would be the worst of both.
	if len(remaining) > 0 {
		return cleared, &RemainingError{
			Credential: credential, Role: role,
			Cleared: cleared, Remaining: remaining, Err: errors.Join(causes...),
		}
	}
	if len(cleared) == 0 {
		return nil, ErrNoEntry
	}
	return cleared, nil
}

// RemainingError reports a delete that could not clear every place a secret may sit. It names what is
// still there, what was removed, and what each stage said, so the blocker rather than the first failure
// decides the message. Its causes are unwrapped, so a caller classifies it by what actually blocked.
type RemainingError struct {
	Credential string
	Role       string
	Cleared    []Source
	Remaining  []Source
	Err        error
}

func (e *RemainingError) Error() string {
	msg := fmt.Sprintf("the secret for %s.%s may still be stored in the %s",
		e.Credential, e.Role, joinSources(e.Remaining))
	if len(e.Cleared) > 0 {
		msg += fmt.Sprintf(" (it was removed from the %s)", joinSources(e.Cleared))
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *RemainingError) Unwrap() error { return e.Err }

func joinSources(sources []Source) string {
	names := make([]string, len(sources))
	for i, s := range sources {
		names[i] = string(s)
	}
	return strings.Join(names, " and the ")
}

// Plaintext returns the fallback file this resolver reads, so a caller can name its path in a message. It
// is nil when no fallback is configured.
func (r *Resolver) Plaintext() *File { return r.plaintext }

// StoreSkipped reports whether the system credential store was switched off for this run and was therefore
// never consulted. A command that removes secrets has to be able to say so: it cleared what it could
// reach, and a silent success would otherwise read as "the secret is gone everywhere".
func (r *Resolver) StoreSkipped() bool {
	s, ok := r.store.(unavailableStore)
	return ok && errors.Is(s.err, ErrDisabled)
}

// Lookup reports whether an environment variable carries a value, without returning it. It answers the one
// question a user interface has about stage one: does something shadow the store?
func (r *Resolver) Lookup(name string) bool { return name != "" && r.env(name) != "" }

func (r *Resolver) fallback(credential, role string) (string, error) {
	if r.plaintext == nil {
		return "", ErrDisabled
	}
	return r.plaintext.Get(credential, role)
}

// deliver registers the value with the redactor before it leaves the resolver, so everything printed after
// this point is covered even if a provider echoes the credential back.
func (r *Resolver) deliver(value string, source Source, checked []string) Value {
	r.register(value)
	return Value{Secret: value, Source: source, Checked: checked}
}

func (r *Resolver) register(value string) {
	if r.redactor != nil {
		r.redactor.Add(value)
	}
}

func stage(source Source, reason string) string {
	return fmt.Sprintf("%s (%s)", source, reason)
}
