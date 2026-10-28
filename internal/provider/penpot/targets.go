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
	filePrefix    = "file/"
	maxTeams      = 20
	maxProjects   = 200
	maxFiles      = 200
)

// scope is the boundary of one connection: its teams, and optionally the projects and single files it is
// narrowed to. Projects and files together form one allow-list within the teams. The excluded projects and
// files are no part of that allow-list: only allowsProject and allowsFile read them, so the restrictions
// derived from the targets (narrowed, fileOnly, reach) stay as they are.
type scope struct {
	teams            []string
	projects         []string
	files            []string
	excludedProjects []string
	excludedFiles    []string
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

// narrowed reports whether the connection has an allow-list of projects or files.
func (s scope) narrowed() bool { return len(s.projects) > 0 || len(s.files) > 0 }

// fileOnly reports an allow-list of files without any project.
func (s scope) fileOnly() bool { return len(s.projects) == 0 && len(s.files) > 0 }

// excludes reports whether the connection excludes a project or a file.
func (s scope) excludes() bool { return len(s.excludedProjects) > 0 || len(s.excludedFiles) > 0 }

func (s scope) excludesProject(id string) bool { return contains(s.excludedProjects, id) }

func (s scope) excludesFile(id string) bool { return contains(s.excludedFiles, id) }

// targetsProject is the allow-list check of a project alone, without the exclusions.
func (s scope) targetsProject(id string) bool { return !s.narrowed() || contains(s.projects, id) }

// allowsProject is the local project check: every project when there is no allow-list, otherwise only the
// listed projects; a listed file does not open its project. An excluded project is never allowed. Whether the
// project belongs to a bound team is established separately, with a request.
func (s scope) allowsProject(id string) bool { return !s.excludesProject(id) && s.targetsProject(id) }

// allowsFile is the local file check: the file of an allowed project, or a listed file. An excluded file, and
// every file of an excluded project, is refused even when a target names it. Whether the file lies in the
// project is established separately, with a request.
func (s scope) allowsFile(projectID, fileID string) bool {
	return !s.excludesProject(projectID) && !s.excludesFile(fileID) &&
		(s.targetsProject(projectID) || contains(s.files, fileID))
}

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

// parseTarget reads one team/TEAM_ID, project/PROJECT_ID, or file/FILE_ID entry.
func parseTarget(raw string) (kind, id string, err error) {
	const form = "a Penpot target must be team/TEAM_ID, project/PROJECT_ID, or file/FILE_ID with a UUID"
	switch {
	case strings.HasPrefix(raw, teamPrefix):
		kind, raw = "team", strings.TrimPrefix(raw, teamPrefix)
	case strings.HasPrefix(raw, projectPrefix):
		kind, raw = "project", strings.TrimPrefix(raw, projectPrefix)
	case strings.HasPrefix(raw, filePrefix):
		kind, raw = "file", strings.TrimPrefix(raw, filePrefix)
	default:
		return "", "", errors.New(form)
	}
	id, ok := parseUUID(raw)
	if !ok {
		return "", "", errors.New(form)
	}
	return kind, id, nil
}

// parseScope reads the configured targets: at least one team, any number of projects and files, none named twice.
// The exclusions name projects and files only, none twice and none equal to a target. No error quotes a
// configured value.
func parseScope(values []string, excludes ...string) (scope, error) {
	var bound scope
	for _, raw := range values {
		kind, id, err := parseTarget(strings.TrimSpace(raw))
		if err != nil {
			return scope{}, err
		}
		list := &bound.teams
		limit := maxTeams
		switch kind {
		case "project":
			list, limit = &bound.projects, maxProjects
		case "file":
			list, limit = &bound.files, maxFiles
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
	for _, raw := range excludes {
		kind, id, err := parseTarget(strings.TrimSpace(raw))
		if err != nil || kind == "team" {
			return scope{}, errors.New("a Penpot exclusion must be project/PROJECT_ID or file/FILE_ID with a UUID")
		}
		targets, list, limit := bound.projects, &bound.excludedProjects, maxProjects
		if kind == "file" {
			targets, list, limit = bound.files, &bound.excludedFiles, maxFiles
		}
		if contains(*list, id) {
			return scope{}, errors.New("the Penpot exclusions name a " + kind + " more than once")
		}
		if contains(targets, id) {
			return scope{}, errors.New("a Penpot exclusion must not equal a target")
		}
		if len(*list) >= limit {
			return scope{}, errors.New("the Penpot exclusions name too many " + kind + " entries")
		}
		*list = append(*list, id)
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
	bound, err := parseScope(provider.TargetsOf(resolved), provider.ExcludesOf(resolved)...)
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
// checked afterwards with a request (see (*Client).locateProject). A listed file does not open its project:
// tools that act on a project itself use this check.
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

// selectProjectForFile is selectProject for a tool that acts on one file of the project: besides an allowed
// project, a listed file passes, unless the file or its project is excluded. The file argument is only read here; its form is checked by the caller.
func selectProjectForFile(resolved *config.Resolved, projectField, projectRaw, fileRaw string) (string, error) {
	bound, err := boundScope(resolved)
	if err != nil {
		return "", err
	}
	projectID, ok := parseUUID(projectRaw)
	if !ok {
		return "", invalidRequest(projectField + " must be a UUID")
	}
	fileID, _ := parseUUID(fileRaw)
	if !bound.allowsFile(projectID, fileID) {
		return "", invalidRequest(projectField + " is outside the targets of this connection")
	}
	return projectID, nil
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
	if bound.narrowed() {
		return "", invalidRequest("a connection with a project or file allow-list cannot create projects")
	}
	return id, nil
}

// refuseFileOnly refuses a team-level read to a connection that is bound to single files only: such a
// binding names no project, so nothing at team level is part of it. The refusal comes before any secret.
func refuseFileOnly(resolved *config.Resolved, what string) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if bound.fileOnly() {
		return invalidRequest("a connection bound to files only cannot " + what + ", which is not part of a file")
	}
	return nil
}
