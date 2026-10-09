package infomaniakchat

import (
	"errors"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The two target kinds a connection may combine. A kChat identifier is the character class the instance's
// own generator produces: lowercase letters and digits, never a separator or anything that could change
// how it is read as one path segment.
const (
	teamPrefix    = "team/"
	channelPrefix = "channel/"
	// maxIDLength bounds a configured or argument identifier well above the 26 characters kChat's own
	// generator produces today, so an oversized value fails fast instead of becoming an oversized path
	// segment; the exact length is not re-enforced, only this generous upper bound.
	maxIDLength = 40
)

// scope is the team and channel boundary of one connection: one or more bound teams, and either every
// channel of them the token can reach (an empty channel allow-list) or only the channels explicitly
// listed.
type scope struct {
	teams    []string
	channels []string
}

// allowsTeam reports whether a team belongs to this connection's boundary.
func (s scope) allowsTeam(teamID string) bool {
	for _, allowed := range s.teams {
		if allowed == teamID {
			return true
		}
	}
	return false
}

// allowsChannel reports whether a channel belongs to this connection's local boundary. It says nothing
// about which team the channel actually belongs to; only the live check against the instance proves that,
// see (*Client).verifyChannelScope.
func (s scope) allowsChannel(channelID string) bool {
	if len(s.channels) == 0 {
		return true
	}
	for _, allowed := range s.channels {
		if allowed == channelID {
			return true
		}
	}
	return false
}

// scopeOf reads the bound teams and channel allow-list of one connection.
func scopeOf(resolved *config.Resolved) (scope, error) {
	values := provider.TargetsOf(resolved)
	return parseScope(values)
}

// parseScope reads the configured targets of one connection: one or more team/TEAM_ID entries, and zero or
// more channel/CHANNEL_ID entries, none named twice. No error ever quotes a configured value.
func parseScope(values []string) (scope, error) {
	var bound scope
	seenTeams, seenChannels := map[string]bool{}, map[string]bool{}
	for _, raw := range values {
		kind, id, err := parseTarget(strings.TrimSpace(raw))
		if err != nil {
			return scope{}, err
		}
		switch kind {
		case "team":
			if seenTeams[id] {
				return scope{}, errors.New("the kChat team target list names a team more than once")
			}
			seenTeams[id] = true
			bound.teams = append(bound.teams, id)
		case "channel":
			if seenChannels[id] {
				return scope{}, errors.New("the kChat channel target list names a channel more than once")
			}
			seenChannels[id] = true
			bound.channels = append(bound.channels, id)
		}
	}
	if len(bound.teams) == 0 {
		return scope{}, errors.New("a kChat connection needs one or more team/TEAM_ID targets")
	}
	return bound, nil
}

// validateTarget checks the form of one configured target in isolation, before configuration validation
// checks the whole set with parseScope.
func validateTarget(raw string) error {
	_, _, err := parseTarget(strings.TrimSpace(raw))
	return err
}

// parseTarget reads one configured target as team/TEAM_ID or channel/CHANNEL_ID.
func parseTarget(raw string) (kind, id string, err error) {
	switch {
	case strings.HasPrefix(raw, teamPrefix):
		kind, id = "team", strings.TrimPrefix(raw, teamPrefix)
	case strings.HasPrefix(raw, channelPrefix):
		kind, id = "channel", strings.TrimPrefix(raw, channelPrefix)
	default:
		return "", "", errors.New("a kChat target must be team/TEAM_ID or channel/CHANNEL_ID")
	}
	if !validMattermostID(id) {
		return "", "", errors.New("a kChat target ID must be a kChat-style identifier of lowercase letters and digits")
	}
	return kind, id, nil
}

// validMattermostID keeps an identifier, whether configured or given as an agent argument, to the
// character class kChat's own generator produces. It is never used to build a URL, only a single path
// segment of the fixed kChat REST paths this provider calls.
func validMattermostID(value string) bool {
	if value == "" || len(value) > maxIDLength {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

// selectChannel checks a channel_id argument against the connection's local scope before any secret is
// resolved and before any request is sent. A channel outside a configured channel allow-list is refused as
// an invalid request, never as a provider failure, because the connection's own configuration decided
// against it. Whether the channel is truly inside the bound teams is confirmed later, live, against the
// instance: local configuration alone proves nothing about what a channel_id really resolves to, see
// (*Client).verifyChannelScope.
func selectChannel(resolved *config.Resolved, channelID string) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if !validMattermostID(channelID) {
		return invalidRequest("channel_id must be a kChat-style identifier")
	}
	if !bound.allowsChannel(channelID) {
		return invalidRequest("channel_id is outside the targets of this connection")
	}
	return nil
}

// selectTeam checks a team_id argument against the connection's bound teams before any request is sent.
func selectTeam(resolved *config.Resolved, teamID string) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if !validMattermostID(teamID) {
		return invalidRequest("team_id must be a kChat-style identifier")
	}
	if !bound.allowsTeam(teamID) {
		return invalidRequest("team_id is outside the targets of this connection")
	}
	return nil
}

func boundScope(resolved *config.Resolved) (scope, error) {
	if resolved == nil {
		return scope{}, providerError("open", "no connection was selected")
	}
	bound, err := scopeOf(resolved)
	if err != nil {
		return scope{}, providerError("open", err.Error())
	}
	return bound, nil
}
