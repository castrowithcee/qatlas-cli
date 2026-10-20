package manage

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/connlog"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
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
// base URL, or "" where none is known.
type TargetSource struct {
	List       func(context.Context, *config.Resolved, config.TargetListRequest) (config.TargetPage, error)
	ResolveURL func(ctx context.Context, resolved *config.Resolved, origin, rawURL string) ([]config.TargetEntry, error)
}

// WithTargetSource returns a copy of s that reads targets through source.
func (s *Service) WithTargetSource(source TargetSource) *Service {
	c := *s
	c.targets = source
	return &c
}

// TargetChoiceError is the only error a target choice returns for a failure of the read itself. Code is the
// stable code of the failure (the provider class, or admin-required, invalid-request, unsupported-capability,
// vault-locked, missing-secret, approval-required, runtime); the text is fixed per code and never carries
// anything a provider or a value said.
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
	var page config.TargetPage
	warning, err := s.chooseTargets(ctx, proof, connection, invokelog.TargetListOperation(req.Kind),
		func(ctx context.Context, cfg *config.Config, resolved *config.Resolved) error {
			metadata, _ := cfg.ProviderMetadata(resolved.Provider)
			if err := checkListRequest(metadata.Target, req); err != nil {
				return err
			}
			if s.targets.List == nil {
				return config.ErrTargetBrowseUnsupported
			}
			got, err := s.targets.List(ctx, resolved, req)
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
	var entries []config.TargetEntry
	warning, err := s.chooseTargets(ctx, proof, connection, invokelog.OperationTargetResolve,
		func(ctx context.Context, cfg *config.Config, resolved *config.Resolved) error {
			metadata, _ := cfg.ProviderMetadata(resolved.Provider)
			origin, err := serviceOrigin(resolved.BaseURL, rawURL)
			if err != nil {
				return err
			}
			if s.targets.ResolveURL == nil {
				return config.ErrTargetBrowseUnsupported
			}
			got, err := s.targets.ResolveURL(ctx, resolved, origin, rawURL)
			if err != nil {
				return err
			}
			entries = keepValid(metadata.Target, got, "", maxURLEntries)
			return nil
		})
	return entries, warning, err
}

// chooseTargets is the one path of every target choice: admin proof, connection, bounded read, classified
// error, log entry.
func (s *Service) chooseTargets(ctx context.Context, proof AdminProof, connection, operation string,
	read func(context.Context, *config.Config, *config.Resolved) error) (string, error) {
	if s.targets.List == nil || s.targets.ResolveURL == nil {
		return "", &TargetChoiceError{Code: "unsupported-capability"}
	}
	required, _, err := AdminRequired(s.vault())
	if err != nil || required && !proof.granted {
		return "", &TargetChoiceError{Code: "admin-required"}
	}
	cfg, err := s.store.Load()
	if err != nil {
		return "", err
	}
	resolved, err := cfg.Resolve(connection, "")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, TargetChoiceTimeout)
	defer cancel()
	choiceErr := classifyTargetError(read(ctx, cfg, resolved))

	result := "success"
	if choiceErr != nil {
		result = choiceErr.Code
	}
	var warning string
	recorder := connlog.NewRecorder(s.surface, s.store.Path(), cfg.LogRetentionDays(), s.vault(), s.processOK)
	if err := recorder.RecordTargetChoice(operation, resolved.Name, resolved.ID, result); err != nil {
		warning = s.redact("warning: " + err.Error())
	}
	if choiceErr != nil {
		return warning, choiceErr
	}
	return warning, nil
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
	if kind.Parent == "" {
		if req.Parent != "" {
			return invalid
		}
		return nil
	}
	if !t.ValidTarget(kind.Parent, req.Parent) {
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
