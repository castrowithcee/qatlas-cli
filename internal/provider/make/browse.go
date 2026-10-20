package makeapi

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// maxFolderDepth bounds how far a nested folder answer is followed.
const maxFolderDepth = 16

func targetBrowser() capability.TargetBrowser {
	return capability.TargetBrowser{List: listTargets, ResolveURL: resolveTargetURL}
}

// listTargets reads one page of organizations, the teams of an organization, the folders of a team, or the
// scenarios of a team. None of them filters by the connection's current targets. A cursor is the numeric
// offset of the next page, as in Make's pg[offset]/pg[limit]. Folders come in one answer, since Make's
// folder list documents no paging.
func listTargets(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	offset := 0
	if req.Cursor != "" {
		value, err := strconv.Atoi(req.Cursor)
		if err != nil || value < 0 || strconv.Itoa(value) != req.Cursor {
			return config.TargetPage{}, invalidRequest("the target cursor is not a page of this provider")
		}
		offset = value
	}
	var parentID int64
	switch req.Kind {
	case "organization":
	case "team", "folder", "scenario":
		wantKind := "organization"
		if req.Kind != "team" {
			wantKind = "team"
		}
		kind, id, err := parseTarget(strings.TrimSpace(req.Parent))
		if err != nil || kind != wantKind {
			return config.TargetPage{}, invalidRequest("the parent of a " + req.Kind + " must be " + wantKind +
				"/" + strings.ToUpper(wantKind) + "_ID")
		}
		parentID = id
	default:
		return config.TargetPage{}, invalidRequest("the kind is not one this provider offers")
	}
	if resolved == nil {
		return config.TargetPage{}, providerError("open", "no connection was selected")
	}
	client, err := build(ctx, resolved, secrets, red, nil, scope{})
	if err != nil {
		return config.TargetPage{}, err
	}
	switch req.Kind {
	case "organization":
		return client.listOrganizationTargets(ctx, offset)
	case "team":
		return client.listTeamTargets(ctx, parentID, offset)
	case "folder":
		return client.listFolderTargets(ctx, parentID)
	}
	return client.listScenarioTargets(ctx, parentID, offset, req.Query)
}

func pageQuery(offset int) url.Values {
	return url.Values{"pg[offset]": {strconv.Itoa(offset)}, "pg[limit]": {strconv.Itoa(defaultListLimit)}}
}

// nextOffset is the cursor of the following page; Make reports no total, so a full page may be followed by more.
func nextOffset(rows, offset int) string {
	if rows >= defaultListLimit {
		return strconv.Itoa(offset + rows)
	}
	return ""
}

func (c *Client) listOrganizationTargets(ctx context.Context, offset int) (config.TargetPage, error) {
	var answer struct {
		Organizations []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"organizations"`
	}
	if err := c.get(ctx, "list organization targets", "/organizations", pageQuery(offset), &answer, needOrgRead); err != nil {
		return config.TargetPage{}, err
	}
	entries := make([]config.TargetEntry, 0, len(answer.Organizations))
	for _, o := range answer.Organizations {
		if o.ID <= 0 {
			continue
		}
		entries = append(entries, config.TargetEntry{
			Target: organizationPrefix + strconv.FormatInt(o.ID, 10), Name: bounded(o.Name), Kind: "organization",
			HasChildren: true,
		})
	}
	return config.TargetPage{Entries: entries, Next: nextOffset(len(answer.Organizations), offset)}, nil
}

func (c *Client) listTeamTargets(ctx context.Context, orgID int64, offset int) (config.TargetPage, error) {
	query := pageQuery(offset)
	query.Set("organizationId", strconv.FormatInt(orgID, 10))
	var answer struct {
		Teams []struct {
			ID             int64  `json:"id"`
			Name           string `json:"name"`
			OrganizationID int64  `json:"organizationId"`
		} `json:"teams"`
	}
	if err := c.get(ctx, "list team targets", "/teams", query, &answer, needTeamsRead); err != nil {
		return config.TargetPage{}, err
	}
	entries := make([]config.TargetEntry, 0, len(answer.Teams))
	for _, team := range answer.Teams {
		if team.ID <= 0 || (team.OrganizationID != 0 && team.OrganizationID != orgID) {
			continue
		}
		entries = append(entries, config.TargetEntry{
			Target: teamPrefix + strconv.FormatInt(team.ID, 10), Name: bounded(team.Name), Kind: "team",
			HasChildren: true,
		})
	}
	return config.TargetPage{Entries: entries, Next: nextOffset(len(answer.Teams), offset)}, nil
}

type folderJSON struct {
	ID       int64        `json:"id"`
	Name     string       `json:"name"`
	Path     string       `json:"path"`
	TeamID   int64        `json:"teamId"`
	Children []folderJSON `json:"children"`
}

// listFolderTargets reads every folder of the team at once, subfolders included (childrenDepth=all), and
// lists them flat with their path as the readable place.
func (c *Client) listFolderTargets(ctx context.Context, teamID int64) (config.TargetPage, error) {
	query := url.Values{"teamId": {strconv.FormatInt(teamID, 10)}, "childrenDepth": {"all"}}
	var answer struct {
		Folders []folderJSON `json:"scenariosFolders"`
	}
	if err := c.get(ctx, "list folder targets", "/scenarios-folders", query, &answer, needRead); err != nil {
		return config.TargetPage{}, err
	}
	var entries []config.TargetEntry
	var walk func(folders []folderJSON, depth int)
	walk = func(folders []folderJSON, depth int) {
		for _, f := range folders {
			if f.ID > 0 && (f.TeamID == 0 || f.TeamID == teamID) {
				entries = append(entries, config.TargetEntry{
					Target: folderPrefix + strconv.FormatInt(f.ID, 10), Name: bounded(f.Name), Kind: "folder",
					Path: bounded(f.Path),
				})
			}
			if depth < maxFolderDepth {
				walk(f.Children, depth+1)
			}
		}
	}
	walk(answer.Folders, 0)
	return config.TargetPage{Entries: entries}, nil
}

// listScenarioTargets lists the scenarios of the team, whatever their folder, so a scenario outside any
// folder stays selectable; the folder id is the readable place. Make filters by name on the server.
func (c *Client) listScenarioTargets(ctx context.Context, teamID int64, offset int, search string) (config.TargetPage, error) {
	query := pageQuery(offset)
	query.Set("teamId", strconv.FormatInt(teamID, 10))
	if search != "" {
		query.Set("name", search)
	}
	var page scenariosPageJSON
	if err := c.get(ctx, "list scenario targets", "/scenarios", query, &page, needRead); err != nil {
		return config.TargetPage{}, err
	}
	entries := make([]config.TargetEntry, 0, len(page.Scenarios))
	for _, s := range page.Scenarios {
		if s.ID <= 0 || s.TeamID != teamID {
			continue
		}
		path := ""
		if s.FolderID > 0 {
			path = "folder/" + strconv.FormatInt(s.FolderID, 10)
		}
		entries = append(entries, config.TargetEntry{
			Target: scenarioPrefix + strconv.FormatInt(s.ID, 10), Name: bounded(s.Name), Kind: "scenario", Path: path,
		})
	}
	return config.TargetPage{Entries: entries, Next: nextOffset(len(page.Scenarios), offset), ServerSearch: search != ""}, nil
}

// resolveTargetURL resolves no URL. Neither Make's API documentation nor its help pages document the route
// of the web interface for a team, scenario or folder, so a URL names no target and no request is sent.
func resolveTargetURL(_ context.Context, _ *config.Resolved, _ *secret.Resolver, _ *redact.Redactor,
	_, _ string) ([]config.TargetEntry, error) {
	return nil, nil
}
