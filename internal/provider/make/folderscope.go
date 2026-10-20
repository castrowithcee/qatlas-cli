package makeapi

import (
	"context"
	"net/url"
	"slices"
	"strconv"
)

// folderParents reads the folder tree of the bound team once per client and keeps each folder's parent
// (0 for a top-level folder). It is the only extra read a folder target ever costs a call.
func (c *Client) folderParents(ctx context.Context, op string) (map[int64]int64, error) {
	if c.parents != nil {
		return c.parents, nil
	}
	query := url.Values{"teamId": {strconv.FormatInt(c.scope.teamID, 10)}, "childrenDepth": {"all"}}
	var answer struct {
		Folders []folderJSON `json:"scenariosFolders"`
	}
	if err := c.get(ctx, op, "/scenarios-folders", query, &answer, needRead); err != nil {
		return nil, err
	}
	parents := map[int64]int64{}
	var walk func(folders []folderJSON, parent int64, depth int)
	walk = func(folders []folderJSON, parent int64, depth int) {
		for _, f := range folders {
			if f.ID > 0 && (f.TeamID == 0 || f.TeamID == c.scope.teamID) {
				parents[f.ID] = parent
				if depth < maxFolderDepth {
					walk(f.Children, f.ID, depth+1)
				}
			}
		}
	}
	walk(answer.Folders, 0, 0)
	c.parents = parents
	return parents, nil
}

// inListedFolder reports whether the folder is a listed folder or lies below one, judged by the bound
// team's own folder tree; a folder that tree does not hold, such as one of another team, never counts.
func (c *Client) inListedFolder(ctx context.Context, op string, folderID int64) (bool, error) {
	if slices.Contains(c.scope.folders, folderID) {
		return true, nil
	}
	if folderID == 0 || len(c.scope.folders) == 0 {
		return false, nil
	}
	parents, err := c.folderParents(ctx, op)
	if err != nil {
		return false, err
	}
	seen := map[int64]bool{}
	for id := folderID; id != 0 && !seen[id]; id = parents[id] {
		if _, known := parents[id]; !known {
			return false, nil
		}
		if slices.Contains(c.scope.folders, id) {
			return true, nil
		}
		seen[id] = true
	}
	return false, nil
}

// scenarioInScope applies the scenario and folder allow-list to a scenario Make reported. The folder tree
// is read only when the scenario has a folder, a folder target exists, and the folder is not listed itself.
func (c *Client) scenarioInScope(ctx context.Context, op string, s scenarioJSON) (bool, error) {
	if c.scope.allowsScenario(s) {
		return true, nil
	}
	return c.inListedFolder(ctx, op, s.FolderID)
}

// checkFolderMove keeps a scenario inside a folder allow-list: a scenario that is in scope only through its
// folders may move only into a listed folder or one below it, never out of folders, before the change is sent.
func (c *Client) checkFolderMove(ctx context.Context, op string, scenario *scenarioJSON, folderID *int64,
	clearFolder bool) error {
	if len(c.scope.folders) == 0 || c.scope.listsScenario(scenario.ID) {
		return nil
	}
	if clearFolder {
		return invalidRequest("the new folder is outside the targets of this connection")
	}
	if folderID == nil {
		return nil
	}
	ok, err := c.inListedFolder(ctx, op, *folderID)
	if err != nil {
		return err
	}
	if !ok {
		return invalidRequest("the new folder is outside the targets of this connection")
	}
	return nil
}
