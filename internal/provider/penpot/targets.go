package penpot

import (
	"errors"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	teamPrefix    = "team/"
	projectPrefix = "project/"
	maxTeams      = 20
	maxProjects   = 200
)

// scope is the boundary of one connection: its teams, and optionally the projects it is narrowed to.
type scope struct {
	teams    []string
	projects []string
}

func contains(list []string, id string) bool {
	for _, item := range list {
		if item == id {
			return true
		}
	}
	return false
}

func (s scope) allowsTeam(id string) bool { return contains(s.teams, id) }

// allowsProject is the local project check: every project when the allow-list is empty. Whether the project
// belongs to a bound team is established separately, with a request.
func (s scope) allowsProject(id string) bool { return len(s.projects) == 0 || contains(s.projects, id) }

// parseUUID accepts the 36-character hexadecimal form and returns it in lower case.
func parseUUID(raw string) (string, bool) {
	if len(raw) != 36 {
		return "", false
	}
	raw = strings.ToLower(raw)
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return "", false
			}
			continue
		}
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return raw, true
}

// parseTarget reads one team/TEAM_ID or project/PROJECT_ID entry.
func parseTarget(raw string) (kind, id string, err error) {
	const form = "a Penpot target must be team/TEAM_ID or project/PROJECT_ID with a UUID"
	switch {
	case strings.HasPrefix(raw, teamPrefix):
		kind, raw = "team", strings.TrimPrefix(raw, teamPrefix)
	case strings.HasPrefix(raw, projectPrefix):
		kind, raw = "project", strings.TrimPrefix(raw, projectPrefix)
	default:
		return "", "", errors.New(form)
	}
	id, ok := parseUUID(raw)
	if !ok {
		return "", "", errors.New(form)
	}
	return kind, id, nil
}

// parseScope reads the configured targets: at least one team, any number of projects, none named twice.
// No error quotes a configured value.
func parseScope(values []string) (scope, error) {
	var bound scope
	for _, raw := range values {
		kind, id, err := parseTarget(strings.TrimSpace(raw))
		if err != nil {
			return scope{}, err
		}
		list := &bound.teams
		limit := maxTeams
		if kind == "project" {
			list, limit = &bound.projects, maxProjects
		}
		if contains(*list, id) {
			return scope{}, errors.New("the Penpot targets name a " + kind + " more than once")
		}
		if len(*list) >= limit {
			return scope{}, errors.New("the Penpot targets name too many " + kind + " entries")
		}
		*list = append(*list, id)
	}
	if len(bound.teams) == 0 {
		return scope{}, errors.New("a Penpot connection needs at least one team/TEAM_ID target")
	}
	return bound, nil
}

func validateTarget(raw string) error {
	_, _, err := parseTarget(strings.TrimSpace(raw))
	return err
}

func validateSet(values []string) error {
	_, err := parseScope(values)
	return err
}

// boundScope reads the connection's scope before any secret is resolved.
func boundScope(resolved *config.Resolved) (scope, error) {
	if resolved == nil {
		return scope{}, providerError("open", "no connection was selected")
	}
	values := provider.TargetsOf(resolved)
	bound, err := parseScope(values)
	if err != nil {
		return scope{}, providerError("open", err.Error())
	}
	return bound, nil
}

// selectTeam checks a team_id argument against the connection's teams before any secret is resolved and
// before any request is sent. The refusal never names the team.
func selectTeam(resolved *config.Resolved, raw string) (string, error) {
	bound, err := boundScope(resolved)
	if err != nil {
		return "", err
	}
	id, ok := parseUUID(raw)
	if !ok {
		return "", invalidRequest("team_id must be a UUID")
	}
	if !bound.allowsTeam(id) {
		return "", invalidRequest("team_id is outside the targets of this connection")
	}
	return id, nil
}

// selectProject checks a project_id argument against the connection's project allow-list, when it has one,
// before any secret is resolved and before any request is sent. Whether the project lies in a bound team is
// checked afterwards with a request (see (*Client).locateProject).
func selectProject(resolved *config.Resolved, raw string) (string, error) {
	return selectProjectAs(resolved, "project_id", raw)
}

// selectProjectAs is selectProject for an argument with another name, such as the target of a move.
func selectProjectAs(resolved *config.Resolved, field, raw string) (string, error) {
	bound, err := boundScope(resolved)
	if err != nil {
		return "", err
	}
	id, ok := parseUUID(raw)
	if !ok {
		return "", invalidRequest(field + " must be a UUID")
	}
	if !bound.allowsProject(id) {
		return "", invalidRequest(field + " is outside the targets of this connection")
	}
	return id, nil
}

// selectTeamForNewProject is selectTeam for a new project: a connection narrowed by a project allow-list
// refuses it, since the new project could not lie inside that list. The refusal comes before any secret.
func selectTeamForNewProject(resolved *config.Resolved, raw string) (string, error) {
	id, err := selectTeam(resolved, raw)
	if err != nil {
		return "", err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return "", err
	}
	if len(bound.projects) > 0 {
		return "", invalidRequest("a connection with a project allow-list cannot create projects")
	}
	return id, nil
}
