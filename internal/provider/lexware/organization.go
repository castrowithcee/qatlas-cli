package lexware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const organizationPrefix = "organization/"

const organizationForm = "a Lexware target must be organization/ORGANIZATION_ID with a UUID"

// parseOrganizationTarget reads one organization/UUID target and returns the UUID in lower case. The error
// never quotes the value.
func parseOrganizationTarget(raw string) (string, error) {
	id, ok := strings.CutPrefix(strings.TrimSpace(raw), organizationPrefix)
	if !ok || !validUUID(id) {
		return "", errors.New(organizationForm)
	}
	return strings.ToLower(id), nil
}

func validateOrganizationTarget(raw string) error {
	_, err := parseOrganizationTarget(raw)
	return err
}

// boundOrganization returns the organization a connection is bound to, or "" for an unbound connection.
func boundOrganization(resolved *config.Resolved) (string, error) {
	var values []string
	if strings.TrimSpace(resolved.Target) != "" {
		values = append(values, resolved.Target)
	}
	values = append(values, resolved.Targets...)
	switch len(values) {
	case 0:
		return "", nil
	case 1:
		id, err := parseOrganizationTarget(values[0])
		if err != nil {
			return "", providerError("open", organizationForm)
		}
		return id, nil
	}
	return "", providerError("open", "a Lexware connection can be bound to one organization only")
}

// binding is the organization a client must verify before its first business request.
type binding struct {
	want  string
	entry *organizationEntry
}

// organizationEntry holds the organization one key reported, once. The mutex is held while the profile is
// read, so concurrent callers share a single request. Only a successful read is kept: a failure is retried
// by the next call and never counts as a match.
type organizationEntry struct {
	mu  sync.Mutex
	got string
}

var (
	organizationsMu sync.Mutex
	organizations   = map[string]*organizationEntry{}
)

// organizationEntryFor returns the process-local entry of one connection and key. The key enters only as a
// SHA-256 digest, so the cache never holds it.
func organizationEntryFor(connection, apiKey string) *organizationEntry {
	digest := sha256.Sum256([]byte(apiKey))
	cacheKey := connection + "\x00" + hex.EncodeToString(digest[:])
	organizationsMu.Lock()
	defer organizationsMu.Unlock()
	entry := organizations[cacheKey]
	if entry == nil {
		entry = &organizationEntry{}
		organizations[cacheKey] = entry
	}
	return entry
}

// verifyOrganization makes sure the key belongs to the bound organization. The organization is read once per
// process, connection and key, and a foreign organization stays rejected for the life of the process. The
// error names neither organization.
func (c *Client) verifyOrganization(ctx context.Context, op string) error {
	b := c.binding
	if b == nil {
		return nil
	}
	b.entry.mu.Lock()
	defer b.entry.mu.Unlock()
	if b.entry.got == "" {
		var profile struct {
			OrganizationID string `json:"organizationId"`
		}
		if err := c.sendRaw(ctx, "check organization", http.MethodGet, "", "/v1/profile", nil, nil, &profile, ""); err != nil {
			return err
		}
		id := strings.ToLower(profile.OrganizationID)
		if !validUUID(id) {
			return providerError("check organization", "Lexware did not report the organization of the API key")
		}
		b.entry.got = id
	}
	if b.entry.got != b.want {
		return &provider.Error{
			Class: provider.ClassPermission, Op: op,
			Message: "the API key belongs to a different organization than the one this connection is bound to",
		}
	}
	return nil
}

// SuggestTarget offers the organization of the API key as the target of a connection that has none, from
// one profile read. A bound connection, or a profile without a valid organization, yields "".
func SuggestTarget(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (string, error) {
	if resolved == nil || strings.TrimSpace(resolved.Target) != "" || len(resolved.Targets) > 0 {
		return "", nil
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return "", err
	}
	profile, err := client.GetProfile(ctx)
	if err != nil {
		return "", err
	}
	id := strings.ToLower(profile.OrganizationID)
	if !validUUID(id) {
		return "", nil
	}
	return organizationPrefix + id, nil
}
