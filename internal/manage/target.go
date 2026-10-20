package manage

import (
	"context"
	"errors"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
	"github.com/castrowithcee/qatlas-cli/internal/vault"
)

// The bounds of one target choice. The time bound is that of a connection test.
const (
	TargetChoiceTimeout = 30 * time.Second
	maxPageEntries      = 200
	maxURLEntries       = 50
	maxTextBytes        = 256
	maxCursorBytes      = 1024
	maxURLBytes         = 2048
)

// TargetSource reads the targets a provider offers for a resolved connection. The caller supplies it, so
// this package knows no provider; ResolveURL receives the origin (scheme and host) of the connection's effective
// base URL, or "" where none is known. secrets is the resolver the read must take the connection's secrets
// from, or nil for the surface's own.
type TargetSource struct {
	// Browses reports whether the provider has a way to list its targets; nil means none has.
	Browses func(providerID string) bool
	List    func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		req config.TargetListRequest) (config.TargetPage, error)
	ResolveURL func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		origin, rawURL string) ([]config.TargetEntry, error)
}

// WithTargetSource returns a copy of s that reads targets through source.
func (s *Service) WithTargetSource(source TargetSource) *Service {
	c := *s
	c.targets = source
	return &c
}

// CanBrowseTargets reports whether a surface can offer to choose targets of the provider from its list.
func (s *Service) CanBrowseTargets(providerID string) bool {
	return s.targets.Browses != nil && s.targets.Browses(providerID)
}

// TargetChoiceError is the only error a target choice returns for a failure of the read itself. Code is the
// stable code of the failure (the provider class, or admin-required, invalid-request, unsupported-capability,
// vault-locked, missing-secret, approval-required, runtime, and for a draft unsaved-secret and
// vault-locked-here); the text is fixed per code and never carries anything a provider or a value said.
type TargetChoiceError struct{ Code string }

func (e *TargetChoiceError) Error() string {
	switch e.Code {
	case "admin-required":
		return "choosing targets needs the vault passphrase first"
	case "invalid-request":
		return "the request for targets is invalid"
	case "unsupported-capability":
		return "this provider cannot list its targets"
	case "vault-locked":
		return "the vault is locked"
	case "missing-secret":
		return "a credential of the connection is missing"
	case "approval-required":
		return "the connection is not approved"
	case "unsaved-secret":
		return "the credential has no saved secret yet; save the credential's secrets first"
	case "vault-locked-here":
		return "choosing targets before saving needs the vault unlocked here; enter the vault passphrase here first"
	case "runtime":
		return "the targets could not be read"
	}
	return provider.Explain(provider.Class(e.Code), "the provider")
}

// ListTargets reads one page of the targets of a saved connection from its provider. It needs proof when the
// vault is encrypted, as writing does; a refusal reads nothing and logs nothing. The page holds only entries
// that are valid targets of the requested kind. The warning is the redacted failure of the log, which never
// changes the result.
func (s *Service) ListTargets(ctx context.Context, proof AdminProof, connection string,
	req config.TargetListRequest) (config.TargetPage, string, error) {
	return s.listTargets(ctx, proof, savedSubject(connection), req)
}

// ListDraftTargets is ListTargets for a connection a surface is still setting up: draft names its provider,
// its service, its credential and its targets so far, and nothing of it is saved. Its secrets are read as
// draftSecrets says, and no approval is asked for or written.
func (s *Service) ListDraftTargets(ctx context.Context, proof AdminProof, draft ConnectionDraft,
	req config.TargetListRequest) (config.TargetPage, string, error) {
	return s.listTargets(ctx, proof, s.draftSubject(draft), req)
}

func (s *Service) listTargets(ctx context.Context, proof AdminProof, subject targetSubject,
	req config.TargetListRequest) (config.TargetPage, string, error) {
	operation := invokelog.TargetListOperation(req.Kind)
	if subject.draft {
		operation = invokelog.DraftTargetListOperation(req.Kind)
	}
	var page config.TargetPage
	warning, err := s.chooseTargets(ctx, proof, subject, operation,
		func(ctx context.Context, cfg *config.Config, resolved *config.Resolved, secrets secretsFunc) error {
			metadata, _ := cfg.ProviderMetadata(resolved.Provider)
			if err := checkListRequest(metadata.Target, req); err != nil {
				return err
			}
			if s.targets.List == nil {
				return config.ErrTargetBrowseUnsupported
			}
			r, err := secrets()
			if err != nil {
				return err
			}
			got, err := s.targets.List(ctx, resolved, r, req)
			if err != nil {
				return err
			}
			if len(got.Next) > maxCursorBytes {
				return provider.InvalidResponse("list targets", "the provider returned an unusable cursor")
			}
			page = config.TargetPage{
				Entries:      keepValid(metadata.Target, got.Entries, req.Kind, maxPageEntries),
				Next:         got.Next,
				ServerSearch: got.ServerSearch,
			}
			return nil
		})
	return page, warning, err
}

// ResolveTargetURL turns a URL of the web interface of a saved connection's service into targets. The URL
// must be a plain web URL; the provider decides which hosts belong to the service. It needs proof as
// ListTargets does.
func (s *Service) ResolveTargetURL(ctx context.Context, proof AdminProof, connection,
	rawURL string) ([]config.TargetEntry, string, error) {
	return s.resolveTargetURL(ctx, proof, savedSubject(connection), rawURL)
}

// ResolveDraftTargetURL is ResolveTargetURL for a draft, read the way ListDraftTargets reads it.
func (s *Service) ResolveDraftTargetURL(ctx context.Context, proof AdminProof, draft ConnectionDraft,
	rawURL string) ([]config.TargetEntry, string, error) {
	return s.resolveTargetURL(ctx, proof, s.draftSubject(draft), rawURL)
}

func (s *Service) resolveTargetURL(ctx context.Context, proof AdminProof, subject targetSubject,
	rawURL string) ([]config.TargetEntry, string, error) {
	operation := invokelog.OperationTargetResolve
	if subject.draft {
		operation = invokelog.OperationDraftTargetResolve
	}
	var entries []config.TargetEntry
	warning, err := s.chooseTargets(ctx, proof, subject, operation,
		func(ctx context.Context, cfg *config.Config, resolved *config.Resolved, secrets secretsFunc) error {
			metadata, _ := cfg.ProviderMetadata(resolved.Provider)
			origin, err := serviceOrigin(resolved.BaseURL, rawURL)
			if err != nil {
				return err
			}
			if s.targets.ResolveURL == nil {
				return config.ErrTargetBrowseUnsupported
			}
			r, err := secrets()
			if err != nil {
				return err
			}
			got, err := s.targets.ResolveURL(ctx, resolved, r, origin, rawURL)
			if err != nil {
				return err
			}
			entries = keepValid(metadata.Target, got, "", maxURLEntries)
			return nil
		})
	return entries, warning, err
}

// secretsFunc returns the resolver a read hands its source, nil for the surface's own. A read calls it only
// once its request passed every check, so a refused request reads no secret.
type secretsFunc func() (*secret.Resolver, error)

// targetSubject is what a target choice reads for: the connection open builds from the configuration and,
// for a draft, the resolver secrets returns for it (nil for the surface's own).
type targetSubject struct {
	draft   bool
	open    func(*config.Config) (*config.Resolved, error)
	secrets func(*config.Config, *config.Resolved) (*secret.Resolver, error)
}

func savedSubject(connection string) targetSubject {
	return targetSubject{open: func(cfg *config.Config) (*config.Resolved, error) { return cfg.Resolve(connection, "") }}
}

func (s *Service) draftSubject(draft ConnectionDraft) targetSubject {
	return targetSubject{
		draft: true,
		open:  func(cfg *config.Config) (*config.Resolved, error) { return draftResolved(cfg, draft) },
		secrets: func(cfg *config.Config, resolved *config.Resolved) (*secret.Resolver, error) {
			return s.draftSecrets(cfg, draft.NewCredential, resolved)
		},
	}
}

// chooseTargets is the one path of every target choice: admin proof, connection, bounded read, classified
// error, log entry.
func (s *Service) chooseTargets(ctx context.Context, proof AdminProof, subject targetSubject, operation string,
	read func(context.Context, *config.Config, *config.Resolved, secretsFunc) error) (string, error) {
	if s.targets.List == nil || s.targets.ResolveURL == nil {
		return "", &TargetChoiceError{Code: "unsupported-capability"}
	}
	required, _, err := AdminRequired(s.vault())
	if err != nil || required && !proof.granted {
		return "", &TargetChoiceError{Code: "admin-required"}
	}
	cfg, err := s.store.Load()
	var notFound *config.NotFoundError
	if subject.draft && errors.As(err, &notFound) {
		// A draft may be the first connection of a configuration that does not exist yet.
		cfg, err = s.store.New(), nil
	}
	if err != nil {
		return "", err
	}
	resolved, err := subject.open(cfg)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, TargetChoiceTimeout)
	defer cancel()
	err = read(ctx, cfg, resolved, func() (*secret.Resolver, error) {
		if subject.secrets == nil {
			return nil, nil
		}
		return subject.secrets(cfg, resolved)
	})
	if subject.draft && unsavedSecret(err) {
		err = &TargetChoiceError{Code: "unsaved-secret"}
	}
	choiceErr := classifyTargetError(err)

	result := "success"
	if choiceErr != nil {
		result = choiceErr.Code
	}
	var warning string
	recorder := connlog.NewRecorder(s.surface, s.store.Path(), cfg.LogRetentionDays(), s.vault(), s.processOK)
	// A draft has no name or id of its own yet; its entry names the draft and nothing the person typed.
	name, id := resolved.Name, resolved.ID
	if subject.draft {
		name, id = invokelog.DraftConnection, ""
	}
	if err := recorder.RecordTargetChoice(operation, name, id, result); err != nil {
		warning = s.redact("warning: " + err.Error())
	}
	if choiceErr != nil {
		return warning, choiceErr
	}
	return warning, nil
}

// draftResolved builds the connection draft would become from its service, its credential and its targets so
// far, the way the configuration would resolve it, without a name, rights, or anything it releases besides
// its own credential. Nothing is written. The errors are those of BuildConnectionCandidate, and
// invalid-request for a target beyond the bounds of a choice.
func draftResolved(cfg *config.Config, draft ConnectionDraft) (*config.Resolved, error) {
	cand, err := BuildConnectionCandidate(cfg, draft, StageCredential)
	if err != nil {
		return nil, err
	}
	if len(draft.Targets) > maxPageEntries || slices.ContainsFunc(draft.Targets, func(t string) bool {
		return t == "" || len(t) > maxTextBytes || !plainText(t)
	}) {
		return nil, &TargetChoiceError{Code: "invalid-request"}
	}
	service := cand.Config.Services[draft.Service]
	target, targets := SplitTargets(draft.Targets)
	return &config.Resolved{
		Provider: draft.Provider, BaseURL: cand.Config.ServiceBaseURL(service), Options: maps.Clone(service.Options),
		Target: target, Targets: targets, Service: draft.Service, Credential: draft.Credential,
		Secrets: cand.Config.Credentials[draft.Credential],
	}, nil
}

// draftSecrets returns the resolver a draft's read takes its credential's secrets from. A new credential
// that keeps them in a store has none saved yet. A credential of an encrypted vault is read only from the
// vault this process holds unlocked, which the surface's admin passphrase did (see VerifyAdmin), and only
// for the roles of its provider: no approval is checked or written, a vault process is never asked, and a
// pending entry is never read. Every other credential is read by the surface's own resolver (nil), the way a
// connection test reads it.
func (s *Service) draftSecrets(cfg *config.Config, newCredential bool,
	resolved *config.Resolved) (*secret.Resolver, error) {
	cred := resolved.Secrets
	if newCredential && cred.Type != config.CredentialTypeEnv {
		return nil, &TargetChoiceError{Code: "unsaved-secret"}
	}
	v := s.vault()
	if cred.Type != config.CredentialTypeVault || v == nil {
		return nil, nil
	}
	state, err := v.State()
	switch {
	case err != nil:
		return nil, err
	case state == vault.StateLocked:
		return nil, &TargetChoiceError{Code: "vault-locked-here"}
	case state != vault.StateUnlocked:
		return nil, nil
	}
	values := map[string]string{}
	for _, role := range cfg.SecretRolesOf(resolved.Provider) {
		// No passphrase is offered: a vault this process no longer holds unlocked stays locked.
		value, found, state, err := v.Get(resolved.Credential, role, nil)
		switch {
		case err != nil || state != vault.StateUnlocked:
			return nil, &TargetChoiceError{Code: "vault-locked-here"}
		case !found || value == "":
			return nil, &TargetChoiceError{Code: "unsaved-secret"}
		}
		if s.redactor != nil {
			s.redactor.Add(value)
		}
		values[secret.EnvName(resolved.Credential, cred, role)] = value
	}
	// The values stand in for the first stage of the cascade; the resolver has no store, file or vault, so
	// it answers nothing else, a forward credential included.
	return secret.NewWith(func(name string) string { return values[name] }, nil, nil, s.redactor), nil
}

// unsavedSecret reports whether err says that a credential has no secret stored for a role yet: a vault
// without the entry, or a system keyring that answered without one. An env credential stores nothing.
func unsavedSecret(err error) bool {
	var missing *secret.MissingSecretError
	if !errors.As(err, &missing) {
		return false
	}
	switch missing.Type {
	case config.CredentialTypeVault:
		return missing.Err == nil
	case config.CredentialTypeKeyring:
		return secret.StoreStage(missing.Checked) == secret.StoreEmpty
	}
	return false
}

// classifyTargetError maps what a read returned to a TargetChoiceError and nil to nil. The error text of the
// provider is dropped: only its class survives.
func classifyTargetError(err error) *TargetChoiceError {
	var (
		providerErr *provider.Error
		choice      *TargetChoiceError
		locked      *secret.VaultLockedError
		missing     *secret.MissingSecretError
		approval    *secret.ApprovalRequiredError
	)
	switch {
	case err == nil:
		return nil
	case errors.As(err, &choice):
		return choice
	case errors.As(err, &providerErr):
		return &TargetChoiceError{Code: string(providerErr.Class)}
	case errors.Is(err, context.DeadlineExceeded):
		return &TargetChoiceError{Code: string(provider.ClassTimeout)}
	case errors.Is(err, config.ErrTargetBrowseUnsupported):
		return &TargetChoiceError{Code: "unsupported-capability"}
	case errors.As(err, &locked):
		return &TargetChoiceError{Code: "vault-locked"}
	case errors.As(err, &missing):
		return &TargetChoiceError{Code: "missing-secret"}
	case errors.As(err, &approval):
		return &TargetChoiceError{Code: "approval-required"}
	}
	return &TargetChoiceError{Code: "runtime"}
}

// checkListRequest refuses a request that names no kind, a parent that is not a target of the kind's parent
// kind, or text beyond the bounds.
func checkListRequest(t config.TargetMetadata, req config.TargetListRequest) error {
	invalid := &TargetChoiceError{Code: "invalid-request"}
	kind, ok := t.KindByName(req.Kind)
	if !ok || len(req.Query) > maxTextBytes || len(req.Cursor) > maxCursorBytes || !plainText(req.Query) {
		return invalid
	}
	if req.Parent == "" {
		if kind.Parent != "" {
			return invalid
		}
		return nil
	}
	// A parent names the one entry the listing sits under, never a wildcard. A nested kind also lists under
	// an entry of its own kind.
	if strings.Contains(req.Parent, "*") ||
		!(kind.Parent != "" && t.ValidTarget(kind.Parent, req.Parent) || kind.Nested && t.ValidTarget(kind.Name, req.Parent)) {
		return invalid
	}
	return nil
}

// serviceOrigin checks that rawURL is a web URL without credentials and returns the origin of the
// connection's effective base URL (which already holds the provider's default), or "" where none is known.
// Which web hosts belong to the service is the provider's decision: a web interface often lives on another
// host than the API.
func serviceOrigin(baseURL, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || len(rawURL) > maxURLBytes || !plainText(rawURL) || u.Host == "" || u.User != nil ||
		u.Scheme != "http" && u.Scheme != "https" {
		return "", &TargetChoiceError{Code: "invalid-request"}
	}
	base, err := url.Parse(baseURL)
	if baseURL == "" || err != nil || base.Host == "" {
		return "", nil
	}
	return strings.ToLower(base.Scheme) + "://" + strings.ToLower(base.Host), nil
}

// keepValid drops every entry that is not a valid target of its kind, or of kind when that is set, or whose
// texts are empty, overlong, or hold control characters, and keeps at most limit entries.
func keepValid(t config.TargetMetadata, entries []config.TargetEntry, kind string, limit int) []config.TargetEntry {
	kept := make([]config.TargetEntry, 0, len(entries))
	for _, e := range entries {
		switch {
		case len(kept) == limit:
			return kept
		case kind != "" && e.Kind != kind, !t.ValidTarget(e.Kind, e.Target):
		case e.Name == "" || len(e.Name) > maxTextBytes || len(e.Path) > maxTextBytes ||
			!plainText(e.Name) || !plainText(e.Path):
		default:
			e.HasChildren = e.HasChildren && t.HasChildKind(e.Kind)
			kept = append(kept, e)
		}
	}
	return kept
}

func plainText(s string) bool {
	return utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0
}
