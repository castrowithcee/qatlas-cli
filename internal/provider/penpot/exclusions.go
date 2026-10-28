package penpot

import (
	"context"
	"encoding/json"
)

// maxEvidenceProjects bounds the projects whose files a deletion reads; a team with more cannot be proven free
// of exclusions and is refused.
const maxEvidenceProjects = maxProjectsListed

// evidenceEntries reads a list for a proof that nothing excluded lies in it. Unlike the listing helpers it
// drops nothing: an element that is no object or carries no UUID could hide an excluded object, so the proof
// fails instead.
func (c *Client) evidenceEntries(ctx context.Context, op, command string, params map[string]any) ([]obj, error) {
	var raw json.RawMessage
	if err := c.do(ctx, op, command, params, &raw); err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, invalidResponse(op, "Penpot returned an invalid response")
	}
	entries := make([]obj, 0, len(items))
	for _, item := range items {
		entry, ok := asObj(item)
		if !ok || entry.id("id") == "" {
			return nil, invalidResponse(op, "Penpot returned an invalid response")
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// refuseExcludedInProject refuses deleting a project that holds an excluded file, live or deleted but still
// restorable. The refusal reads like any object outside the targets and names nothing.
func (c *Client) refuseExcludedInProject(ctx context.Context, op, teamID, projectID string) error {
	if len(c.scope.excludedFiles) == 0 {
		return nil
	}
	refusal := invalidRequest("project_id is outside the targets of this connection")
	files, err := c.evidenceEntries(ctx, op, cmdProjectFile, map[string]any{"project-id": projectID})
	if err != nil {
		return err
	}
	for _, file := range files {
		if c.scope.excludesFile(file.id("id")) {
			return refusal
		}
	}
	deleted, err := c.evidenceEntries(ctx, op, cmdDeletedFiles, map[string]any{"team-id": teamID})
	if err != nil {
		return err
	}
	for _, file := range deleted {
		// A deleted file that reports no project, or this one, may belong here.
		if reported := file.id("projectid"); c.scope.excludesFile(file.id("id")) && (reported == "" || reported == projectID) {
			return refusal
		}
	}
	return nil
}

// refuseExcludedInTeam refuses deleting a team that holds an excluded project or file, live or deleted but
// still restorable. A team of a connection without exclusions needs no proof. The refusal names nothing.
func (c *Client) refuseExcludedInTeam(ctx context.Context, op, teamID string) error {
	if !c.scope.excludes() {
		return nil
	}
	refusal := invalidRequest("team_id is outside the targets of this connection")
	projects, err := c.evidenceEntries(ctx, op, cmdProjects, map[string]any{"team-id": teamID})
	if err != nil {
		return err
	}
	for _, project := range projects {
		if c.scope.excludesProject(project.id("id")) {
			return refusal
		}
	}
	if len(c.scope.excludedFiles) == 0 {
		return nil
	}
	if len(projects) > maxEvidenceProjects {
		return invalidResponse(op, "the team has more projects than can be checked")
	}
	for _, project := range projects {
		files, err := c.evidenceEntries(ctx, op, cmdProjectFile, map[string]any{"project-id": project.id("id")})
		if err != nil {
			return err
		}
		for _, file := range files {
			if c.scope.excludesFile(file.id("id")) {
				return refusal
			}
		}
	}
	deleted, err := c.evidenceEntries(ctx, op, cmdDeletedFiles, map[string]any{"team-id": teamID})
	if err != nil {
		return err
	}
	for _, file := range deleted {
		if c.scope.excludesFile(file.id("id")) {
			return refusal
		}
	}
	return nil
}
