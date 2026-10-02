package capability

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// ErrSecretRefNotReleased reports a reference that the connection does not release. The core refuses such a
// reference before the confirmation gate; a handler that is reached without that check still stops here.
var ErrSecretRefNotReleased = errors.New("the secret reference is not released by this connection")

// SecretRefAllowed reports whether ref names a forward credential that resolved releases: it is listed in
// the connection's forward_secrets, exists as a forward credential, and is not the connection's own
// credential. Nothing is read from a store.
func SecretRefAllowed(resolved *config.Resolved, ref string) bool {
	if resolved == nil || ref == "" || ref == resolved.Credential {
		return false
	}
	listed := false
	for _, name := range resolved.ForwardSecrets {
		if name == ref {
			listed = true
			break
		}
	}
	entry, ok := resolved.Forward[ref]
	return listed && ok && entry.Forward && len(entry.Fields) > 0
}

// ResolveSecretRef reads the field values of the forward credential ref names, for a handler that is about to
// send them to the provider. It reads only for a request that carries its confirmation and only for a
// reference the connection releases, and the resolver registers every value with the redactor. The error
// names the reference, never a value.
func ResolveSecretRef(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	ref string) (map[string]string, error) {
	if !Confirmed(ctx) {
		return nil, errors.New("a secret reference is resolved only for a confirmed request")
	}
	if !SecretRefAllowed(resolved, ref) {
		return nil, fmt.Errorf("%w: %s", ErrSecretRefNotReleased, ref)
	}
	return secrets.ResolveForwarded(ctx, secret.ForwardRef{Name: ref, Cred: resolved.Forward[ref]})
}

// MergeSecretFields sets every resolved field value into body, under the field's own name, and returns an
// error rather than replacing a member the body already has: a field the request already filled would hide
// which of the two reaches the provider. The error names the field, never a value.
func MergeSecretFields(body map[string]any, values map[string]string) error {
	if body == nil {
		return errors.New("the request body is missing")
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, exists := body[name]; exists {
			return fmt.Errorf("the request body already has a member named %q", name)
		}
	}
	for _, name := range names {
		body[name] = values[name]
	}
	return nil
}
