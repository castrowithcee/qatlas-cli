package penpot

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Choosing targets reads what the token reaches: its teams, the projects of one team, and the files of one
// project. It only reads, with the token alone and never through the connection's targets, so a person finds
// an entry the targets do not name yet. Penpot's commands are not paged, so every list is one page, bounded.

const maxBrowseText = 200

// browseText bounds a name Penpot sent: control characters become spaces and the text is cut on a rune boundary.
func browseText(value string) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, value))
	if len(value) > maxBrowseText {
		cut := maxBrowseText
		for !utf8.RuneStart(value[cut]) {
			cut--
		}
		value = strings.TrimSpace(value[:cut])
	}
	return value
}

func browseList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	const op = "choose targets"
	var parentID string
	switch req.Kind {
	case "team":
	case "project", "file":
		kind, wantKind := "team", teamPrefix
		if req.Kind == "file" {
			kind, wantKind = "project", projectPrefix
		}
		id, ok := strings.CutPrefix(req.Parent, wantKind)
		if parentID, ok = parseUUID(id); !ok {
			return config.TargetPage{}, invalidRequest("the parent must be " + kind + "/" + strings.ToUpper(kind) + "_ID")
		}
	default:
		return config.TargetPage{}, invalidRequest("the kind is not one of this provider")
	}
	client, err := openScoped(ctx, resolved, secrets, red, nil, scope{})
	if err != nil {
		return config.TargetPage{}, err
	}
	var page config.TargetPage
	switch req.Kind {
	case "team":
		var raw json.RawMessage
		if err := client.do(ctx, op, cmdTeams, nil, &raw); err != nil {
			return config.TargetPage{}, err
		}
		entries, ok := objects(raw)
		if !ok {
			return config.TargetPage{}, invalidResponse(op, "Penpot returned an invalid response")
		}
		for _, entry := range entries {
			if id := entry.id("id"); id != "" && len(page.Entries) < maxTeamsListed {
				page.Entries = append(page.Entries, config.TargetEntry{Target: teamPrefix + id,
					Name: browseText(entry.str("name")), Kind: "team", HasChildren: true})
			}
		}
	case "project":
		projects, err := client.projectsOf(ctx, op, parentID)
		if err != nil {
			return config.TargetPage{}, err
		}
		for _, project := range projects {
			if len(page.Entries) < maxProjectsListed {
				page.Entries = append(page.Entries, config.TargetEntry{Target: projectPrefix + project.ID,
					Name: browseText(project.Name), Kind: "project", HasChildren: true})
			}
		}
	default:
		files, err := client.filesOf(ctx, op, parentID)
		if err != nil {
			return config.TargetPage{}, err
		}
		for _, file := range files {
			if len(page.Entries) < maxFilesListed {
				page.Entries = append(page.Entries, config.TargetEntry{Target: filePrefix + file.ID,
					Name: browseText(file.Name), Kind: "file"})
			}
		}
	}
	return page, nil
}

// browseResolveURL reads team, project, and file from a URL of the web interface without a request. The
// frontend routes (penpot 2.18, frontend/src/app/main/ui/routes.cljs and app.main.data.common) carry the IDs
// as query parameters of the hash route: #/dashboard/files?team-id=..&project-id=.. and
// #/workspace?team-id=..&file-id=..; the legacy dashboard route keeps them in the path:
// #/dashboard/team/TEAM_ID/projects/PROJECT_ID. Only the host of the connection's origin counts.
func browseResolveURL(_ context.Context, _ *config.Resolved, _ *secret.Resolver, _ *redact.Redactor,
	origin, rawURL string) ([]config.TargetEntry, error) {
	if origin == "" {
		origin = cloudOrigin
	}
	base, err := originOf(origin)
	if err != nil {
		return nil, nil
	}
	service, err := url.Parse(base)
	if err != nil {
		return nil, nil
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.EqualFold(parsed.Host, service.Host) || parsed.Scheme != "https" ||
		!strings.HasPrefix(parsed.EscapedPath(), service.EscapedPath()) {
		return nil, nil
	}
	route, rawQuery, _ := strings.Cut(parsed.Fragment, "?")
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, nil
	}
	segments := strings.Split(strings.Trim(route, "/"), "/")
	var team, project, file string
	switch {
	case len(segments) >= 1 && segments[0] == "workspace":
		file = idOf(query.Get("file-id"))
		team = idOf(query.Get("team-id"))
	case len(segments) >= 2 && segments[0] == "dashboard" && segments[1] == "team":
		if len(segments) >= 3 {
			team = idOf(segments[2])
		}
		if len(segments) >= 5 && segments[3] == "projects" {
			project = idOf(segments[4])
		}
	case len(segments) >= 1 && segments[0] == "dashboard":
		team = idOf(query.Get("team-id"))
		project = idOf(query.Get("project-id"))
	}
	var entries []config.TargetEntry
	if team != "" {
		entries = append(entries, config.TargetEntry{Target: teamPrefix + team, Name: team, Kind: "team", HasChildren: true})
	}
	switch {
	case file != "":
		entries = append(entries, config.TargetEntry{Target: filePrefix + file, Name: file, Kind: "file"})
	case project != "":
		entries = append(entries, config.TargetEntry{Target: projectPrefix + project, Name: project, Kind: "project", HasChildren: true})
	}
	return entries, nil
}

func idOf(raw string) string {
	id, _ := parseUUID(raw)
	return id
}

// reaches tells the tools a binding to single files only cannot serve: those that act on a team or a project
// as a whole. Every other binding reaches every tool; the checks at the call stay in force.
func reaches(tool string, targets []string) (bool, []string) {
	var bound scope
	for _, raw := range targets {
		switch kind, id, err := parseTarget(strings.TrimSpace(raw)); {
		case err != nil:
		case kind == "project":
			bound.projects = append(bound.projects, id)
		case kind == "file":
			bound.files = append(bound.files, id)
		}
	}
	if !bound.fileOnly() {
		return true, nil
	}
	switch tool {
	case Provider + ".files.list", Provider + ".projects.list", Provider + ".projects.rename",
		Provider + ".projects.delete", Provider + ".files.create", Provider + ".files.import",
		Provider + ".files.move":
		return false, []string{"project/PROJECT_ID"}
	case Provider + ".projects.create", Provider + ".members.list", Provider + ".members.set-role",
		Provider + ".members.remove", Provider + ".webhooks.list", Provider + ".webhooks.update",
		Provider + ".webhooks.delete", Provider + ".teams.create", Provider + ".teams.delete":
		return false, []string{"team/TEAM_ID"}
	}
	return true, nil
}
