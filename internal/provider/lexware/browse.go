package lexware

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const maxBrowseName = 200

// browseName bounds the company name Lexware sent: control characters become spaces and the text is cut on a
// rune boundary.
func browseName(value string) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, value))
	if len(value) > maxBrowseName {
		cut := maxBrowseName
		for !utf8.RuneStart(value[cut]) {
			cut--
		}
		value = strings.TrimSpace(value[:cut])
	}
	return value
}

// browseList offers the one organization the API key belongs to, from one profile read. The connection may
// still be unbound, so the read uses the key alone and no other organization is ever listed.
func browseList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	if req.Kind != "organization" || req.Parent != "" || req.Cursor != "" {
		return config.TargetPage{}, providerError("choose targets", "Lexware offers only the organization of the API key")
	}
	if resolved == nil {
		return config.TargetPage{}, providerError("choose targets", "no connection was selected")
	}
	unbound := *resolved
	unbound.Target, unbound.Targets = "", nil
	client, err := Open(ctx, &unbound, secrets, red)
	if err != nil {
		return config.TargetPage{}, err
	}
	profile, err := client.GetProfile(ctx)
	if err != nil {
		return config.TargetPage{}, err
	}
	id := strings.ToLower(profile.OrganizationID)
	if !validUUID(id) {
		return config.TargetPage{}, providerError("choose targets", "Lexware did not report the organization of the API key")
	}
	return config.TargetPage{Entries: []config.TargetEntry{{
		Target: organizationPrefix + id, Name: browseName(profile.CompanyName), Kind: "organization",
	}}}, nil
}

// browseResolveURL resolves nothing: Lexware has no web interface URL that names the organization.
func browseResolveURL(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, string, string) ([]config.TargetEntry, error) {
	return nil, nil
}
