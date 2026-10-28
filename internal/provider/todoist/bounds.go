package todoist

import (
	"context"
	"slices"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// projectTree is the parent of every project a call has read to resolve project trees; a top-level
// project maps to the empty string.
type projectTree struct {
	parents  map[string]string
	active   bool
	archived bool
}

// loadProjects reads the active projects, and the archived ones when asked, once per call. Every read
// pages through the whole list up to maxScanPages, so a tree larger than that is refused unresolved.
func (c *Client) loadProjects(ctx context.Context, op string, archived bool) error {
	if c.tree.parents == nil {
		c.tree.parents = map[string]string{}
	}
	for _, source := range []struct {
		path string
		done *bool
		want bool
	}{{"/projects", &c.tree.active, true}, {"/projects/archived", &c.tree.archived, archived}} {
		if !source.want || *source.done {
			continue
		}
		after := ""
		for pages := 0; ; pages++ {
			if pages == maxScanPages {
				return &provider.Error{Class: provider.ClassProviderError, Op: op,
					Message: "the subprojects of this connection could not all be resolved; nothing was changed"}
			}
			entries, next, err := c.page(ctx, op, source.path, nil, maxLimit, after)
			if err != nil {
				return err
			}
			raws, err := decodeEach(op, entries, func(p rawProject) string { return p.ID })
			if err != nil {
				return err
			}
			for _, raw := range raws {
				c.tree.parents[raw.ID] = text(raw.ParentID)
			}
			if next == "" {
				break
			}
			after = next
		}
		*source.done = true
	}
	return nil
}

// below reports whether the projects read so far put a project at or below a project tree of the
// connection.
func (c *Client) below(projectID string) bool {
	for steps := 0; projectID != "" && steps <= len(c.tree.parents); steps++ {
		if c.scope.root(projectID) {
			return true
		}
		projectID = c.tree.parents[projectID]
	}
	return false
}

// treeMember reports whether a project is at or below a project tree of the connection. Only a project
// that no read has seen can be archived, so the archived projects are read when the active ones do not
// know it.
func (c *Client) treeMember(ctx context.Context, op, projectID string) (bool, error) {
	if c.scope.root(projectID) {
		return true, nil
	}
	if len(c.scope.trees) == 0 {
		return false, nil
	}
	if err := c.loadProjects(ctx, op, false); err != nil {
		return false, err
	}
	if _, known := c.tree.parents[projectID]; !known {
		if err := c.loadProjects(ctx, op, true); err != nil {
			return false, err
		}
	}
	return c.below(projectID), nil
}

// belowExcluded reports whether the projects read so far put a project at or below an excluded tree. A
// project the read does not show, and a chain that is cut or loops, count as excluded: the exclusion could
// sit in the part that cannot be followed.
func (c *Client) belowExcluded(projectID string) bool {
	seen := map[string]bool{}
	for current := projectID; ; {
		if slices.Contains(c.scope.exTrees, current) {
			return true
		}
		parent, known := c.tree.parents[current]
		if !known || seen[current] {
			return true
		}
		seen[current] = true
		if parent == "" {
			return false
		}
		current = parent
	}
}

// excludedLoaded is projectExcluded for a project whose tree is already read.
func (c *Client) excludedLoaded(projectID string) bool {
	return slices.Contains(c.scope.exProjects, projectID) ||
		(len(c.scope.exTrees) > 0 && c.belowExcluded(projectID))
}

// projectExcluded reports whether a project is excluded: by its own ID, or at or below an excluded tree. The
// project tree is read only on a connection that excludes a tree; the archived projects are read when the
// active ones do not know the project, because only an archived project can be missing there.
func (c *Client) projectExcluded(ctx context.Context, op, projectID string) (bool, error) {
	if slices.Contains(c.scope.exProjects, projectID) || slices.Contains(c.scope.exTrees, projectID) {
		return true, nil
	}
	if len(c.scope.exTrees) == 0 {
		return false, nil
	}
	if err := c.loadProjects(ctx, op, false); err != nil {
		return false, err
	}
	if _, known := c.tree.parents[projectID]; !known {
		if err := c.loadProjects(ctx, op, true); err != nil {
			return false, err
		}
	}
	return c.belowExcluded(projectID), nil
}

// projectTargeted reports whether a target reaches a project: a bound project or one in a tree.
func (c *Client) projectTargeted(ctx context.Context, op, projectID string) (bool, error) {
	if c.scope.exact(projectID) {
		return true, nil
	}
	return c.treeMember(ctx, op, projectID)
}

// projectAllowed reports whether a project belongs to the connection: a target reaches it and no exclusion
// does.
func (c *Client) projectAllowed(ctx context.Context, op, projectID string) (bool, error) {
	excluded, err := c.projectExcluded(ctx, op, projectID)
	if err != nil || excluded {
		return false, err
	}
	return c.projectTargeted(ctx, op, projectID)
}

// requireProject refuses a named project that does not belong to the connection.
func (c *Client) requireProject(ctx context.Context, op, projectID string) error {
	if projectID == "" {
		return nil
	}
	ok, err := c.projectAllowed(ctx, op, projectID)
	if err != nil {
		return err
	}
	if !ok {
		return outsideScope("project")
	}
	return nil
}

// noteProject records the parent Todoist reports for a project a change answered with, so a project
// created or changed during this call is resolved without a second read.
func (c *Client) noteProject(raw rawProject) {
	if c.tree.parents == nil {
		c.tree.parents = map[string]string{}
	}
	c.tree.parents[raw.ID] = text(raw.ParentID)
}

// taskAllowed reports whether a task belongs to the connection: its section is bound or its project is, and
// neither its section nor its project is excluded.
func (c *Client) taskAllowed(ctx context.Context, op string, raw rawTask) (bool, error) {
	if raw.SectionID != nil && c.scope.excludesSection(*raw.SectionID) {
		return false, nil
	}
	excluded, err := c.projectExcluded(ctx, op, raw.ProjectID)
	if err != nil || excluded {
		return false, err
	}
	if raw.SectionID != nil && c.scope.hasSection(*raw.SectionID) {
		return true, nil
	}
	return c.projectTargeted(ctx, op, raw.ProjectID)
}

// sectionAllowed reports whether a section belongs to the connection: it is bound or its project is, and
// neither it nor its project is excluded.
func (c *Client) sectionAllowed(ctx context.Context, op string, raw rawSection) (bool, error) {
	if c.scope.excludesSection(raw.ID) {
		return false, nil
	}
	excluded, err := c.projectExcluded(ctx, op, raw.ProjectID)
	if err != nil || excluded {
		return false, err
	}
	if c.scope.hasSection(raw.ID) {
		return true, nil
	}
	return c.projectTargeted(ctx, op, raw.ProjectID)
}

// reaches is the Reaches answer of the Todoist target: which tools a binding to targets serves. A tool
// that acts on the whole account needs the wildcard; one that acts on projects needs a project or a project
// tree; creating a project needs the wildcard or a project tree to create it in.
func reaches(tool string, targets []string) (bool, []string) {
	bound, err := parseScope(targets)
	if err != nil || bound.all {
		return true, nil
	}
	switch tool {
	case filtersList.ID, labelsCreate.ID, labelsUpdate.ID, labelsReorder.ID, labelsDelete.ID:
		return false, []string{wildcard}
	case projectsCreate.ID:
		return len(bound.trees) > 0, []string{treeForm, wildcard}
	case projectsList.ID, projectsGet.ID, projectsUpdate.ID, projectsArchive.ID, projectsUnarchive.ID,
		projectsDelete.ID, sectionsList.ID, sectionsCreate.ID, sectionsReorder.ID, sectionsMove.ID,
		sectionsDelete.ID:
		return bound.projectLevel(), []string{projectForm, treeForm, wildcard}
	}
	return true, nil
}

const (
	projectForm = "PROJECT_ID"
	treeForm    = treePrefix + "PROJECT_ID"
	sectionForm = sectionPrefix + "SECTION_ID"
)

// targetKinds are the kinds of target a Todoist connection takes.
var targetKinds = []config.TargetKind{{
	Name: "project",
	Description: "a project: PROJECT_ID binds exactly that project, project/PROJECT_ID the project with all " +
		"its subprojects, also those created later; * binds the whole account and stands alone",
	Forms: []string{projectForm, treeForm, wildcard},
	// Subprojects are listed below their project.
	Nested:       true,
	ExcludeForms: []string{projectForm, treeForm},
}, {
	Name:         "section",
	Description:  "one section of a project, reaching only its tasks and their comments and reminders",
	Forms:        []string{sectionForm},
	Parents:      []string{"project"},
	ExcludeForms: []string{sectionForm},
}}
