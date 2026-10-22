package forge

import (
	"context"
	"errors"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// testConnection proves the product and its version first, without the token where the instance allows that
// (see probe), and only then reads with the token: the first target the connection names, or without targets
// the account behind the token. A wrong product or a version below the minimum fails closed as a provider
// error. Success proves that the token sees that target; every tool is still decided per request.
func (p *product) testConnection(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (provider.Class, error) {
	if resolved == nil {
		return provider.ClassProviderError, nil
	}
	origin, err := originOf(resolved.BaseURL)
	if err != nil {
		return provider.ClassProviderError, nil
	}
	allowed, err := allowlistOf(resolved)
	if err != nil {
		return provider.ClassProviderError, nil
	}
	if _, err := probe(ctx, p, origin, func() (string, error) {
		return resolveToken(ctx, p, resolved, secrets, red)
	}); err != nil {
		return classOf(err)
	}
	client, err := open(ctx, p, resolved, secrets, red)
	if err != nil {
		return classOf(err)
	}
	if err := client.proveFirstTarget(ctx, allowed); err != nil {
		return classOf(err)
	}
	return provider.ClassOK, nil
}

func classOf(err error) (provider.Class, error) {
	var failure *provider.Error
	if errors.As(err, &failure) {
		return failure.Class, nil
	}
	var invalid *provider.InvalidRequestError
	if errors.As(err, &invalid) {
		return provider.ClassProviderError, nil
	}
	return "", err
}

// proveFirstTarget reads the first target by its canonical name: a repository must answer with a full name the
// targets allow, an owner with its own name. Without targets it reads the account behind the token.
func (c *Client) proveFirstTarget(ctx context.Context, allowed allowlist) error {
	const op = "test connection"
	notReached := &provider.Error{Class: provider.ClassNotFound, Op: op,
		Message: c.product.name + " does not show the first target of this connection to this token"}
	if len(allowed) == 0 {
		var account object
		_, err := c.get(ctx, op, "/user", nil, &account)
		return err
	}
	first := allowed[0]
	var answer object
	if first.kind == kindRepository {
		if _, err := c.get(ctx, op, "/repos/"+escape(first.owner)+"/"+escape(first.repo), nil, &answer); err != nil {
			return err
		}
		if fullName, ok := answer.text("full_name"); !ok || !allowed.allowsFullName(fullName) {
			return notReached
		}
		return nil
	}
	if _, err := c.get(ctx, op, "/users/"+escape(first.owner), nil, &answer); err != nil {
		return err
	}
	if login, ok := answer.text("login"); !ok || !strings.EqualFold(login, first.owner) {
		return notReached
	}
	return nil
}
