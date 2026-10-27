package forge

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// version is the numeric part of a product version. Forgejo counts 15.0.10; Gitea counted 1.27.3 and then
// switched to 28.2.0, which compares as later without a special case.
type version struct{ major, minor, patch int }

func (v version) String() string { return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch) }

func (v version) less(other version) bool {
	if v.major != other.major {
		return v.major < other.major
	}
	if v.minor != other.minor {
		return v.minor < other.minor
	}
	return v.patch < other.patch
}

// parseVersion reads MAJOR.MINOR.PATCH and ignores what follows a '-' or '+': a development build such as
// 16.0.0-dev-785-465b60ef+gitea-1.22.0 or 29.0.0+dev-160-g55186ca39b counts as its number.
func parseVersion(raw string) (version, bool) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "v")
	if len(raw) > 128 {
		return version{}, false
	}
	if cut := strings.IndexAny(raw, "-+"); cut >= 0 {
		raw = raw[:cut]
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var numbers [3]int
	for i, part := range parts {
		if part == "" || len(part) > 6 || strings.Trim(part, "0123456789") != "" {
			return version{}, false
		}
		numbers[i], _ = strconv.Atoi(part)
	}
	return version{numbers[0], numbers[1], numbers[2]}, true
}

// forgejoMarker is the compatibility suffix Forgejo appends to the version it reports below /api/v1. It is no
// sign of Gitea; a Gitea build never carries it, so an instance reporting it is not Gitea.
const forgejoMarker = "+gitea-"

// mismatch reports an instance of the other product. The message names neither the instance nor
// what it reported.
func mismatch(op string, p *product) *provider.Error {
	return provider.Fail(op, "the instance does not identify as "+p.name+"; check the base URL and the provider of "+
		"the service")
}

func tooOld(op string, p *product) *provider.Error {
	return provider.Fail(op, "the instance runs a "+p.name+" version older than "+p.minimum.String()+
		", which Qatlas does not support")
}

// versionTTL is how long a version read stays valid in this process. A long-running broker sees an upgrade of
// the instance after at most this long.
const versionTTL = 10 * time.Minute

type cachedVersion struct {
	value version
	until time.Time
}

// versions holds the version of every instance this process has read, keyed by product and origin, so a tool
// that depends on a version does not send an extra request on every call.
var versions = struct {
	sync.Mutex
	entries map[string]cachedVersion
}{entries: map[string]cachedVersion{}}

var now = time.Now

func cacheKey(p *product, origin string) string { return p.id + " " + origin }

func cachedVersionOf(p *product, origin string) (version, bool) {
	versions.Lock()
	defer versions.Unlock()
	entry, ok := versions.entries[cacheKey(p, origin)]
	if !ok || now().After(entry.until) {
		return version{}, false
	}
	return entry.value, true
}

func remember(p *product, origin string, v version) {
	versions.Lock()
	defer versions.Unlock()
	versions.entries[cacheKey(p, origin)] = cachedVersion{value: v, until: now().Add(versionTTL)}
}

// probe proves the product of the instance at origin and reads its version, in this order:
//
//  1. GET /api/forgejo/v1/version without a credential: 200 with a version means Forgejo. Any other client
//     error but 429 means it is not Forgejo, which also covers a proxy that blocks the path.
//  2. Only for Gitea: GET /api/v1/version without a credential, and once more with the token only when the
//     instance refuses anonymous access. A version with the Forgejo marker is not Gitea.
//
// A mismatch or a version below the minimum fails closed. token is called only for that second attempt.
func probe(ctx context.Context, p *product, origin string, token func() (string, error)) (version, error) {
	const op = "read version"
	client := provider.NoRedirectClient(defaultTimeout, transport)
	limiter := limiters.For("origin " + origin)
	var answer struct {
		Version string `json:"version"`
	}
	status, _, err := send(ctx, client, limiter, p, origin+"/api/forgejo/v1", "", op, "/version", nil, &answer)
	isForgejo := err == nil && answer.Version != ""
	if err != nil && (status < 400 || status > 499 || status == http.StatusTooManyRequests) {
		return version{}, err
	}
	if err == nil && answer.Version == "" {
		return version{}, provider.InvalidResponse(op, p.name+" returned an invalid version")
	}
	if isForgejo != (p == forgejo) {
		return version{}, mismatch(op, p)
	}
	if p == gitea {
		answer.Version = ""
		status, _, err = send(ctx, client, limiter, p, origin+apiPrefix, "", op, "/version", nil, &answer)
		if (status == http.StatusUnauthorized || status == http.StatusForbidden) && token != nil {
			value, tokenErr := token()
			if tokenErr != nil {
				return version{}, tokenErr
			}
			_, _, err = send(ctx, client, limiters.For(value), p, origin+apiPrefix, "token "+value, op, "/version", nil,
				&answer)
		}
		if err != nil {
			return version{}, err
		}
		if strings.Contains(strings.ToLower(answer.Version), forgejoMarker) {
			return version{}, mismatch(op, p)
		}
	}
	parsed, ok := parseVersion(answer.Version)
	if !ok {
		return version{}, provider.InvalidResponse(op, p.name+" returned an invalid version")
	}
	if parsed.less(p.minimum) {
		return version{}, tooOld(op, p)
	}
	remember(p, origin, parsed)
	return parsed, nil
}

// knownVersion is the version of the instance, from this process's cache or one read of /version.
func (c *Client) knownVersion(ctx context.Context) (version, error) {
	if v, ok := cachedVersionOf(c.product, c.origin); ok {
		return v, nil
	}
	return probe(ctx, c.product, c.origin, func() (string, error) {
		return strings.TrimPrefix(c.auth, "token "), nil
	})
}

// requireVersion refuses an operation the instance's version does not have, before the operation's own
// request. It reads the version only when this process holds none for the instance, without a credential
// unless the instance refuses that. The message names the version the operation needs, never the instance's.
func requireVersion(ctx context.Context, p *product, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, op string, needed version) error {
	origin, err := originOf(resolved.BaseURL)
	if err != nil {
		return provider.Fail("open", err.Error())
	}
	current, ok := cachedVersionOf(p, origin)
	if !ok {
		current, err = probe(ctx, p, origin, func() (string, error) {
			return resolveToken(ctx, p, resolved, secrets, red)
		})
		if err != nil {
			return err
		}
	}
	return atLeast(op, p, current, needed)
}

// atLeast refuses a version below the one an operation or argument needs.
func atLeast(op string, p *product, current, needed version) error {
	if current.less(needed) {
		return provider.Fail(op, "this operation is not available on this instance; it needs "+p.name+" "+
			needed.String()+" or later")
	}
	return nil
}
