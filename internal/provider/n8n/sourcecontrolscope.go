package n8n

import (
	"context"
	"net/url"
)

const sourceControlOutside = "source control would touch objects outside the targets of this connection"

// excludesSourceFile reports whether a file of the source control status belongs to the excluded part of
// the instance. Tags and variables stay: the variables file holds the global variables only (project
// variables are exported as stubs inside the project file). A workflow, credential, data table, or folder
// is placed by the owner project the status reports; one without it cannot be placed and counts as
// excluded where a project is excluded, as does a file of any other type.
func (s scope) excludesSourceFile(f sourceControlFileJSON) bool {
	if !s.hasExclusions() {
		return false
	}
	switch f.Type {
	case "tags", "variables":
		return false
	case "project":
		return s.excludesProject(f.ID)
	case "workflow":
		if s.excludesWorkflow(f.ID) {
			return true
		}
		return s.ownerUnplacedOrExcluded(f)
	case "credential", "datatable", "folders":
		return s.ownerUnplacedOrExcluded(f)
	}
	return true
}

func (s scope) ownerUnplacedOrExcluded(f sourceControlFileJSON) bool {
	if len(s.excludedProjects) == 0 {
		return false
	}
	return f.Owner == nil || f.Owner.ProjectID == "" || s.excludesProject(f.Owner.ProjectID)
}

// excludesPushFile decides a named object of a push. A folder cannot be placed from its name, and the folder
// file covers the folders of every project, so folders are refused where a project is excluded.
func (s scope) excludesPushFile(f pushFile, status map[pushFile]sourceControlFileJSON) bool {
	switch f.Type {
	case "tags", "variables":
		return false
	case "folders":
		return len(s.excludedProjects) > 0
	case "project":
		return s.excludesProject(f.ID)
	}
	if f.Type == "workflow" && s.excludesWorkflow(f.ID) {
		return true
	}
	if len(s.excludedProjects) == 0 {
		return false
	}
	file, known := status[f]
	return !known || s.excludesSourceFile(file)
}

// sourceControlFiles reads GET /source-control/status for one direction.
func (c *Client) sourceControlFiles(ctx context.Context, op, direction string) ([]sourceControlFileJSON, error) {
	var page struct {
		Data []sourceControlFileJSON `json:"data"`
	}
	if err := c.get(ctx, op, "/source-control/status", url.Values{"direction": {direction}}, &page,
		maxResponseBytes); err != nil {
		return nil, permissionNeutral(err)
	}
	return page.Data, nil
}

// refuseExcludedPull stops a pull whose preview touches an excluded object. The preview and the pull are
// two requests, so objects that change in between are not covered: that window is the limit of this check.
func (c *Client) refuseExcludedPull(ctx context.Context, op string) error {
	if !c.scope.hasExclusions() {
		return nil
	}
	files, err := c.sourceControlFiles(ctx, op, "pull")
	if err != nil {
		return err
	}
	for _, f := range files {
		if c.scope.excludesSourceFile(f) {
			return invalidRequest(sourceControlOutside)
		}
	}
	return nil
}

// refuseExcludedPush stops a push that names an excluded object. The owner of a named object comes from the
// push preview, read only where a project is excluded and an owner is needed; an object it does not list
// cannot be placed and is refused.
func (c *Client) refuseExcludedPush(ctx context.Context, op string, files []pushFile) error {
	if !c.scope.hasExclusions() {
		return nil
	}
	var status map[pushFile]sourceControlFileJSON
	for _, f := range files {
		if status == nil && len(c.scope.excludedProjects) > 0 &&
			(f.Type == "workflow" || f.Type == "credential" || f.Type == "datatable") {
			listed, err := c.sourceControlFiles(ctx, op, "push")
			if err != nil {
				return err
			}
			status = make(map[pushFile]sourceControlFileJSON, len(listed))
			for _, l := range listed {
				status[pushFile{ID: l.ID, Type: l.Type}] = l
			}
		}
		if c.scope.excludesPushFile(f, status) {
			return invalidRequest(sourceControlOutside)
		}
	}
	return nil
}
