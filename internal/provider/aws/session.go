package aws

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/awsquery"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/endpoints"
	"github.com/castrowithcee/qatlas-connect/internal/provider/aws/sigv4"
)

const (
	stsVersion = "2011-06-15"

	// sessionSeconds is the duration requested from AssumeRole. 3600 s is the default maximum session
	// duration of every role and the cap of role chaining, so it is never above what the role allows; a
	// role's own higher maximum is not used.
	sessionSeconds = 3600

	// renewBefore is how long before expiry a session is replaced, so a call never starts with a session
	// that ends while it runs.
	renewBefore = 5 * time.Minute

	// cacheLimit bounds both in-memory caches; a full cache is dropped and refilled, which only costs
	// requests.
	cacheLimit = 256
)

// assumedSession is the credential AssumeRole issued. It lives only in memory.
type assumedSession struct {
	creds   sigv4.Credentials
	expires time.Time
}

// sessionEntry serializes the fetch of one session, so concurrent calls share one AssumeRole.
type sessionEntry struct {
	mu      sync.Mutex
	session *assumedSession
}

var sessions = struct {
	mu      sync.Mutex
	entries map[string]*sessionEntry
}{entries: map[string]*sessionEntry{}}

// resetCaches empties the session and account caches. Tests use it; production never needs to.
func resetCaches() {
	sessions.mu.Lock()
	sessions.entries = map[string]*sessionEntry{}
	sessions.mu.Unlock()
	accounts.mu.Lock()
	accounts.seen = map[string]bool{}
	accounts.mu.Unlock()
}

// sessionEntryFor returns the entry of one credential set and region.
func sessionEntryFor(key string) *sessionEntry {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	if entry, ok := sessions.entries[key]; ok {
		return entry
	}
	if len(sessions.entries) >= cacheLimit {
		sessions.entries = map[string]*sessionEntry{}
	}
	entry := &sessionEntry{}
	sessions.entries[key] = entry
	return entry
}

// effective returns the credentials that sign calls in region. Without a role these are the connection's
// access key. With a role the broker holds one session per credential set and region: it assumes the role
// through the regional STS endpoint of that region, reuses the session until renewBefore its expiry, and
// then replaces it. The session credentials are registered with the redactor on every use, since the
// redactor of one invoke has not seen the session another invoke fetched.
func (c *Client) effective(ctx context.Context, region string) (sigv4.Credentials, error) {
	if c.creds.roleARN == "" {
		return c.creds.key, nil
	}
	key := fingerprint(c.creds.key.AccessKeyID, c.creds.key.SecretAccessKey, c.creds.key.SessionToken,
		c.creds.roleARN, c.creds.externalID, region)
	entry := sessionEntryFor(key)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if s := entry.session; s != nil && clock.Now().Add(renewBefore).Before(s.expires) {
		c.hide(s.creds)
		return s.creds, nil
	}
	entry.session = nil
	fresh, failure := c.assume(ctx, region)
	if failure != nil {
		return sigv4.Credentials{}, failure
	}
	entry.session = fresh
	return fresh.creds, nil
}

// hide registers session credentials with the redactor.
func (c *Client) hide(creds sigv4.Credentials) {
	if c.red != nil {
		c.red.Add(creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken)
	}
}

// assumeRoleResult is the part of the AssumeRole answer the broker uses.
type assumeRoleResult struct {
	Credentials struct {
		AccessKeyID     string `xml:"AccessKeyId"`
		SecretAccessKey string `xml:"SecretAccessKey"`
		SessionToken    string `xml:"SessionToken"`
		Expiration      string `xml:"Expiration"`
	} `xml:"Credentials"`
}

// denied is the message of a refused role assumption. AWS answers a role that requires MFA with the same
// AccessDenied as a wrong trust policy, so the message names both causes instead of guessing.
const denied = "AWS refused to assume the configured role; check the role's trust policy, the sts:AssumeRole " +
	"permission of the access key, and the external ID. A role that requires multi-factor authentication " +
	"cannot be assumed by a connection"

// assume fetches a new session with AssumeRole. The call is signed with the connection's access key.
func (c *Client) assume(ctx context.Context, region string) (*assumedSession, *provider.Error) {
	const op = "assume role"
	params := map[string]string{
		"RoleArn":         c.creds.roleARN,
		"RoleSessionName": sessionName(c.name),
		"DurationSeconds": strconv.Itoa(sessionSeconds),
	}
	if c.creds.externalID != "" {
		params["ExternalId"] = c.creds.externalID
	}
	var out assumeRoleResult
	if failure := c.send(ctx, op, endpoints.STS, region, c.creds.key, capability.IdempotencyIdempotent,
		awsquery.Call{Action: "AssumeRole", Version: stsVersion, Params: params}, &out); failure != nil {
		if failure.Class == provider.ClassPermission {
			failure.Message = denied
		}
		return nil, failure
	}
	got := out.Credentials
	expires, err := time.Parse(time.RFC3339, got.Expiration)
	if err != nil || got.AccessKeyID == "" || got.SecretAccessKey == "" || got.SessionToken == "" {
		return nil, provider.InvalidResponse(op, "AWS returned an invalid response")
	}
	session := &assumedSession{
		creds:   sigv4.Credentials{AccessKeyID: got.AccessKeyID, SecretAccessKey: got.SecretAccessKey, SessionToken: got.SessionToken},
		expires: expires,
	}
	// Registered before anything else can print it, and before the lifetime check can fail.
	c.hide(session.creds)
	// A session that is due for renewal at once would be fetched again by every call.
	if !clock.Now().Add(renewBefore).Before(expires) {
		return nil, provider.InvalidResponse(op, "AWS returned a session that is about to expire")
	}
	return session, nil
}

// accounts remembers which credential sets were proved to belong to which bound account. The proof holds
// for the life of the process: an access key ID belongs to one account for good, the key is part of the
// cache key together with a digest of its secret and token, and a session of a role is a new credential
// set whenever it is replaced. Only a match is stored, so a refusal is checked again on the next call. The
// cache saves the second request of every call.
var accounts = struct {
	mu   sync.Mutex
	seen map[string]bool
}{seen: map[string]bool{}}

func accountKey(creds sigv4.Credentials, account string) string {
	return fingerprint(creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken, account)
}

func accountSeen(creds sigv4.Credentials, account string) bool {
	accounts.mu.Lock()
	defer accounts.mu.Unlock()
	return accounts.seen[accountKey(creds, account)]
}

func rememberAccount(creds sigv4.Credentials, account string) {
	accounts.mu.Lock()
	defer accounts.mu.Unlock()
	if len(accounts.seen) >= cacheLimit {
		accounts.seen = map[string]bool{}
	}
	accounts.seen[accountKey(creds, account)] = true
}
