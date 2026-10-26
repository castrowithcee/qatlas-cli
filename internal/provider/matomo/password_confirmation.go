package matomo

import (
	"context"
	"net/url"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// rolePasswordConfirmation is the optional second secret role: the Matomo password of the token's user, which
// Matomo asks for as passwordConfirmation on some methods even when the caller uses a token.
const rolePasswordConfirmation = "password-confirmation"

// maxPasswordLength bounds the stored password; Matomo itself accepts far shorter ones.
const maxPasswordLength = 1024

// addPasswordConfirmation is the one place that reads the password role and sets passwordConfirmation. Only
// the methods that Matomo guards with a password confirmation call it; no other request carries the password.
// The value is registered with the redactor and travels in the POST body only. A role that is not set is
// refused before any request, and the refusal names the role, never a value.
func addPasswordConfirmation(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, params url.Values) error {
	const op = "confirm password"
	if secrets == nil {
		return provider.Fail(op, "no credential resolver was configured")
	}
	value, found, err := secrets.ResolveOptional(ctx, resolved.Credential, resolved.Secrets, rolePasswordConfirmation)
	if err != nil {
		return err
	}
	if !found {
		return provider.Fail(op, "this tool needs the secret role "+rolePasswordConfirmation+
			" (the Matomo password of the token's user) on the credential of this connection, and it is not set")
	}
	if red != nil {
		red.Add(value.Secret)
	}
	if len(value.Secret) > maxPasswordLength {
		return provider.Fail(op, "the secret role "+rolePasswordConfirmation+" is unusable")
	}
	params.Set("passwordConfirmation", value.Secret)
	return nil
}
