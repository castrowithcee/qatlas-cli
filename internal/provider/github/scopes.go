package github

import (
	"context"
	"net/http"
	"strings"
	"sync"
)

// Scope gates mark the parts of a selection GitHub accepts only from a classic token that holds one of the
// named scopes. GitHub checks those scopes against the text of a document before it runs it and then refuses
// the whole document, even for a fragment on a type the data never holds, so a gated part is sent only when
// the token holds one of its scopes. A gate is its opener, the part, and gateEnd; GraphQL uses neither < nor >.
const (
	gateUserEmail      = "<user:email read:user|"
	gateOrganization   = "<read:org|"
	gateTeam           = "<read:org read:discussion|"
	gateEnterpriseTeam = "<read:org read:enterprise|"
	gateEnterprise     = "<read:enterprise|"
	gateEnd            = ">"
)

// impliedScopes are the scopes GitHub grants with a broader one, as far as the gates need them.
var impliedScopes = map[string][]string{
	"user":             {"read:user", "user:email"},
	"admin:org":        {"write:org"},
	"write:org":        {"read:org"},
	"admin:enterprise": {"read:enterprise"},
	"write:discussion": {"read:discussion"},
}

// grantedScopes holds per token the scopes GitHub last reported in X-OAuth-Scopes, so a process asks for them
// at most once and every later answer keeps them current.
var grantedScopes = struct {
	sync.Mutex
	byAuth map[string]map[string]bool
}{byAuth: map[string]map[string]bool{}}

func parseScopes(header string) map[string]bool {
	granted := map[string]bool{}
	var grant func(string)
	grant = func(scope string) {
		if scope == "" || granted[scope] {
			return
		}
		granted[scope] = true
		for _, implied := range impliedScopes[scope] {
			grant(implied)
		}
	}
	for _, scope := range strings.Split(header, ",") {
		grant(strings.TrimSpace(scope))
	}
	return granted
}

// observeScopes remembers the scopes an answer reports. Only a classic token's answer carries the header.
func (c *Client) observeScopes(header http.Header) {
	values := header.Values("X-OAuth-Scopes")
	if len(values) == 0 {
		return
	}
	granted := parseScopes(strings.Join(values, ","))
	grantedScopes.Lock()
	grantedScopes.byAuth[c.auth] = granted
	grantedScopes.Unlock()
}

func (c *Client) knownScopes() (map[string]bool, bool) {
	grantedScopes.Lock()
	defer grantedScopes.Unlock()
	granted, ok := grantedScopes.byAuth[c.auth]
	return granted, ok
}

// scoped resolves the gates of a document for this token. Before the first answer it reads the scopes from
// the rate limit route, which costs no rate limit. A token whose answer names no scopes, such as a
// fine-grained token, or whose scopes cannot be read holds none here: a gated part is sent only for a
// reported scope.
func (c *Client) scoped(ctx context.Context, op, document string) string {
	if !strings.Contains(document, gateEnd) {
		return document
	}
	granted, ok := c.knownScopes()
	if !ok {
		_ = c.do(ctx, op, http.MethodGet, c.endpoints.rest+"/rate_limit", nil, nil, false, nil, nil)
		if granted, ok = c.knownScopes(); !ok {
			granted = map[string]bool{}
			grantedScopes.Lock()
			grantedScopes.byAuth[c.auth] = granted
			grantedScopes.Unlock()
		}
	}
	return withScopes(document, granted)
}

// withScopes keeps each gated part one of whose scopes is granted and drops the others.
func withScopes(document string, granted map[string]bool) string {
	var b strings.Builder
	for {
		start := strings.IndexByte(document, '<')
		if start < 0 {
			b.WriteString(document)
			return b.String()
		}
		b.WriteString(document[:start])
		gate := document[start+1:]
		bar, end := strings.IndexByte(gate, '|'), strings.IndexByte(gate, '>')
		if bar < 0 || end < bar {
			b.WriteString(document[start:])
			return b.String()
		}
		for _, scope := range strings.Fields(gate[:bar]) {
			if granted[scope] {
				b.WriteString(gate[bar+1 : end])
				break
			}
		}
		document = gate[end+1:]
	}
}
