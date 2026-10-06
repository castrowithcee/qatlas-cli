package makeapi

import (
	"errors"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// The three target kinds a connection may combine. Team mode: exactly one team, at most one organization,
// and zero or more scenarios. Organization mode: no team, exactly one organization, and no scenario (the
// narrower reading, since a scenario belongs to a team). Exactly one of the two modes applies. Every value
// is a plain positive integer, the form every Make identifier this provider reads takes (scenarioId, teamId,
// organizationId are all documented as integers), so a target is never a free-form string that could be
// mistaken for a path or a URL.
const (
	teamPrefix         = "team/"
	organizationPrefix = "organization/"
	scenarioPrefix     = "scenario/"
	// maxTargetIDDigits bounds a configured or argument identifier well above any realistic Make id, so a
	// malformed value fails fast instead of overflowing.
	maxTargetIDDigits = 18
)

// scope is the team, organization, and scenario boundary of one connection: exactly one bound team, an
// optional bound organization, and either every scenario of that team (an empty allow-list) or only the
// scenarios explicitly listed.
type scope struct {
	teamID    int64
	orgID     int64 // 0 means no organization target was configured
	scenarios []int64
}

// organizationMode reports whether the connection is bound to an organization instead of a team.
func (s scope) organizationMode() bool { return s.teamID == 0 }

// allowsScenario reports whether a scenario belongs to this connection's local scenario boundary. It says
// nothing about the scenario's team; that is always checked separately, live, against Make's own report,
// see (*Client).verifyScenarioScope.
func (s scope) allowsScenario(scenarioID int64) bool {
	if len(s.scenarios) == 0 {
		return true
	}
	for _, allowed := range s.scenarios {
		if allowed == scenarioID {
			return true
		}
	}
	return false
}

// allowsTeam reports whether a reported team ID matches the one team this connection is bound to.
func (s scope) allowsTeam(teamID int64) bool { return teamID == s.teamID }

// allowsOrg reports whether a reported organization ID is admitted: every organization when none was
// configured, otherwise only the one bound.
func (s scope) allowsOrg(orgID int64) bool { return s.orgID == 0 || orgID == s.orgID }

// scopeOf reads the bound team, organization, and scenario targets of one connection.
func scopeOf(resolved *config.Resolved) (scope, error) {
	values := resolved.Targets
	if len(values) == 0 && strings.TrimSpace(resolved.Target) != "" {
		values = []string{resolved.Target}
	}
	return parseScope(values)
}

// parseScope reads the configured targets of one connection: either exactly one team/TEAM_ID, at most one
// organization/ORG_ID, and zero or more scenario/SCENARIO_ID entries (team mode), or no team and exactly one
// organization/ORG_ID (organization mode), none named twice within its own kind.
// No error ever quotes a configured value.
func parseScope(values []string) (scope, error) {
	var bound scope
	teamSet, orgSet := false, false
	seenScenarios := map[int64]bool{}
	for _, raw := range values {
		kind, id, err := parseTarget(strings.TrimSpace(raw))
		if err != nil {
			return scope{}, err
		}
		switch kind {
		case "team":
			if teamSet {
				return scope{}, errors.New("a Make connection may bind exactly one team/TEAM_ID target")
			}
			bound.teamID, teamSet = id, true
		case "organization":
			if orgSet {
				return scope{}, errors.New("a Make connection may bind at most one organization/ORG_ID target")
			}
			bound.orgID, orgSet = id, true
		case "scenario":
			if seenScenarios[id] {
				return scope{}, errors.New("the Make scenario target list names a scenario more than once")
			}
			seenScenarios[id] = true
			bound.scenarios = append(bound.scenarios, id)
		}
	}
	if !teamSet {
		if !orgSet {
			return scope{}, errors.New("a Make connection needs exactly one team/TEAM_ID target, or, in " +
				"organization mode, exactly one organization/ORG_ID target and no team")
		}
		if len(bound.scenarios) > 0 {
			return scope{}, errors.New("a Make connection bound to an organization cannot target scenarios")
		}
	}
	return bound, nil
}

// validateTarget checks the form of one configured target in isolation, before configuration validation
// checks the whole set with parseScope.
func validateTarget(raw string) error {
	_, _, err := parseTarget(strings.TrimSpace(raw))
	return err
}

// parseTarget reads one configured target as team/TEAM_ID, organization/ORG_ID, or scenario/SCENARIO_ID.
func parseTarget(raw string) (kind string, id int64, err error) {
	switch {
	case strings.HasPrefix(raw, teamPrefix):
		kind, raw = "team", strings.TrimPrefix(raw, teamPrefix)
	case strings.HasPrefix(raw, organizationPrefix):
		kind, raw = "organization", strings.TrimPrefix(raw, organizationPrefix)
	case strings.HasPrefix(raw, scenarioPrefix):
		kind, raw = "scenario", strings.TrimPrefix(raw, scenarioPrefix)
	default:
		return "", 0, errors.New("a Make target must be team/TEAM_ID, organization/ORG_ID, or scenario/SCENARIO_ID")
	}
	if !validPositiveID(raw) {
		return "", 0, errors.New("a Make target ID must be a positive integer")
	}
	value, convErr := strconv.ParseInt(raw, 10, 64)
	if convErr != nil {
		return "", 0, errors.New("a Make target ID must be a positive integer")
	}
	return kind, value, nil
}

// validPositiveID keeps a target or argument identifier to plain, non-padded decimal digits: never a sign,
// a leading zero, a separator, or anything that could change how it is read or turn it into something other
// than one opaque path or query value of the fixed Make API paths this provider calls.
func validPositiveID(raw string) bool {
	if raw == "" || len(raw) > maxTargetIDDigits || raw[0] == '0' {
		return false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// errNeedsTeamConnection and errNeedsOrganizationConnection refuse a tool in the wrong connection mode,
// before any secret is resolved or request is sent.
func errNeedsTeamConnection() error {
	return invalidRequest("this tool needs a team connection; this connection is bound to an organization")
}

func errNeedsOrganizationConnection() error {
	return invalidRequest("this tool needs an organization connection; this connection is bound to a team")
}

// boundScope reads the connection's scope for a team tool, wrapping a parse failure as a provider error the
// same way a missing connection is: both are configuration problems that exist before any secret is
// resolved. A connection in organization mode is refused here, the one gate every team tool passes.
func boundScope(resolved *config.Resolved) (scope, error) {
	bound, err := anyScope(resolved)
	if err != nil {
		return scope{}, err
	}
	if bound.organizationMode() {
		return scope{}, errNeedsTeamConnection()
	}
	return bound, nil
}

// anyScope reads the connection's scope in either mode.
func anyScope(resolved *config.Resolved) (scope, error) {
	if resolved == nil {
		return scope{}, providerError("open", "no connection was selected")
	}
	bound, err := scopeOf(resolved)
	if err != nil {
		return scope{}, providerError("open", err.Error())
	}
	return bound, nil
}

// selectScenario checks a scenario_id argument against the connection's local scenario allow-list before
// any secret is resolved and before any request is sent. A scenario outside a configured allow-list is
// refused as an invalid request, never as a provider failure, because the connection's own configuration
// decided against it. Whether the scenario also belongs to the bound team is checked separately, live, see
// (*Client).verifyScenarioScope.
func selectScenario(resolved *config.Resolved, scenarioID int64) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if scenarioID <= 0 {
		return invalidRequest("scenario_id must be a positive integer")
	}
	if !bound.allowsScenario(scenarioID) {
		return invalidRequest("scenario_id is outside the targets of this connection")
	}
	return nil
}
