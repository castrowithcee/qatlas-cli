package todoist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The structure changes: personal projects, sections, and personal labels. Every change requires
// confirmation in its own invoke request and is sent exactly once, like the task changes.
//
// A project connection changes only its own projects and the sections inside them. It creates no project,
// because a new project would lie outside the projects it names, and it changes no label, because a label
// belongs to the whole account. Archiving and deleting a project act on all its subprojects as well, so on
// a project connection every subproject must belong to the connection too. Only personal projects are
// changed: a workspace project belongs to a team, and its administration stays outside this provider.

// Bounds of the structure arguments. The input schemas mirror them.
const (
	maxProjectName = 120
	maxSectionName = 2048
	maxLabelName   = 128
	maxSectionPos  = 2147483647
	maxLabelPos    = 32767
	// maxScanPages bounds the project pages read to find the subprojects of a project. A tree that does not
	// fit is refused instead of being changed unchecked.
	maxScanPages = 25
)

// colors are the color names Todoist accepts for projects and labels.
var colors = []string{"berry_red", "red", "orange", "yellow", "olive_green", "lime_green", "green", "mint_green",
	"teal", "sky_blue", "light_blue", "blue", "grape", "violet", "lavender", "magenta", "salmon", "charcoal", "grey",
	"taupe"}

const (
	projectNameSchema = `{"type":"string","minLength":1,"maxLength":120,"pattern":"^[^\\x00-\\x1f\\x7f]+$"}`
	sectionNameSchema = `{"type":"string","minLength":1,"maxLength":2048,"pattern":"^[^\\x00-\\x1f\\x7f]+$"}`
	labelNameSchema   = `{"type":"string","minLength":1,"maxLength":128,"pattern":"^[^\\x00-\\x1f\\x7f]+$"}`
	colorSchema       = `{"type":"string","enum":["berry_red","red","orange","yellow","olive_green","lime_green",` +
		`"green","mint_green","teal","sky_blue","light_blue","blue","grape","violet","lavender","magenta","salmon",` +
		`"charcoal","grey","taupe"]}`
	viewStyleSchema    = `{"type":"string","enum":["list","board","calendar"]}`
	sectionOrderSchema = `{"type":"integer","minimum":0,"maximum":2147483647}`
	labelOrderSchema   = `{"type":"integer","minimum":0,"maximum":32767}`
	projectFieldInput  = `"description":` + descriptionSchema + `,"color":` + colorSchema + `,` +
		`"is_favorite":` + flagSchema + `,"view_style":` + viewStyleSchema
	labelFieldInput = `"color":` + colorSchema + `,"is_favorite":` + flagSchema
)

var projectFieldArguments = []capability.Argument{
	{Name: "description", Description: "Project description in Markdown, at most 16383 characters"},
	{Name: "color", Description: "Todoist color name such as blue or berry_red"},
	{Name: "is_favorite", Description: "True marks the project as a favorite"},
	{Name: "view_style", Description: "list, board, or calendar"},
}

var projectFields = projectOutputFields

var structureFields = []capability.Field{
	{Name: "id", Description: "ID of the project, section, label, or reminder the change concerned"},
	{Name: "result", Description: "What happened to it"},
}

var projectIDArgument = []capability.Argument{{Name: "project_id",
	Description: "Personal project of the connection, as returned by todoist.projects.list", Required: true}}

var projectIDInput = `{"type":"object","properties":{"project_id":` + idSchema + `},"required":["project_id"],` +
	`"additionalProperties":false}`

var projectsCreate = changing(capability.EffectCreate, capability.IdempotencyNonIdempotent, descriptor("projects",
	"create", "Create a Todoist project",
	"Create one personal project, optionally below a personal parent project, on an explicit Todoist * account "+
		"connection; a project connection does not offer it, because the new project would lie outside its "+
		"projects. A repeated call creates a second project"+retryNote,
	[]string{"projects", "create"},
	`{"type":"object","properties":{"name":`+projectNameSchema+`,"parent_id":`+idSchema+`,`+projectFieldInput+`},`+
		`"required":["name"],"additionalProperties":false}`,
	json.RawMessage(projectSchema),
	append([]capability.Argument{
		{Name: "name", Description: "Project name, at most 120 characters on one line", Required: true},
		{Name: "parent_id", Description: "Personal project the new project becomes a subproject of"},
	}, projectFieldArguments...),
	projectFields,
	[]capability.Example{{Description: "Create a project",
		Arguments: json.RawMessage(`{"name":"Garden","color":"green","view_style":"board"}`)}}))

var projectsUpdate = changing(capability.EffectUpdate, capability.IdempotencyIdempotent, descriptor("projects",
	"update", "Update a Todoist project",
	"Change the name, description, color, favorite mark, or view style of one personal project of an explicit "+
		"Todoist connection; fields left out stay unchanged",
	[]string{"projects", "update"},
	`{"type":"object","properties":{"project_id":`+idSchema+`,"name":`+projectNameSchema+`,`+projectFieldInput+`},`+
		`"required":["project_id"],"additionalProperties":false}`,
	json.RawMessage(projectSchema),
	append(append([]capability.Argument{}, projectIDArgument...), append([]capability.Argument{
		{Name: "name", Description: "New project name, at most 120 characters on one line"},
	}, projectFieldArguments...)...),
	projectFields,
	[]capability.Example{{Description: "Rename a project",
		Arguments: json.RawMessage(`{"project_id":"6XGgm6PHrGgMpCFX","name":"Garden 2027"}`)}}))

var projectsArchive = changing(capability.EffectUpdate, capability.IdempotencyIdempotent, descriptor("projects",
	"archive", "Archive a Todoist project",
	"Archive one personal project of an explicit Todoist connection together with all its subprojects; on a "+
		"project connection every subproject must belong to the connection as well; a project with an excluded "+
		"subproject or section below it is refused"+retryNote,
	[]string{"projects", "archive", "update"},
	projectIDInput, changeOutput("archived"), projectIDArgument, structureFields,
	[]capability.Example{{Description: "Archive a project", Arguments: json.RawMessage(`{"project_id":"6XGgm6PHrGgMpCFX"}`)}}))

var projectsUnarchive = changing(capability.EffectUpdate, capability.IdempotencyIdempotent, descriptor("projects",
	"unarchive", "Unarchive a Todoist project",
	"Make one archived personal project of an explicit Todoist connection active again; Todoist restores it "+
		"alone, as a top-level project, without its parent or subprojects",
	[]string{"projects", "unarchive", "update"},
	projectIDInput, changeOutput("unarchived"), projectIDArgument, structureFields,
	[]capability.Example{{Description: "Unarchive a project", Arguments: json.RawMessage(`{"project_id":"6XGgm6PHrGgMpCFX"}`)}}))

var projectsDelete = listedOnly(changing(capability.EffectDelete, capability.IdempotencyIdempotent, descriptor("projects",
	"delete", "Delete a Todoist project",
	"Delete one personal project of an explicit Todoist connection permanently, with all its subprojects, "+
		"sections, tasks, and comments; on a project connection every subproject must belong to the connection "+
		"as well; a project with an excluded subproject or section below it is refused. Offered only by a "+
		"connection whose tools list names it"+retryNote,
	[]string{"projects", "delete"},
	projectIDInput, changeOutput("deleted"), projectIDArgument, structureFields,
	[]capability.Example{{Description: "Delete a project", Arguments: json.RawMessage(`{"project_id":"6XGgm6PHrGgMpCFX"}`)}})))

var sectionIDArgument = capability.Argument{Name: "section_id",
	Description: "Section of a project of the connection, as returned by todoist.sections.list", Required: true}

var sectionIDInput = `{"type":"object","properties":{"section_id":` + idSchema + `},"required":["section_id"],` +
	`"additionalProperties":false}`

var sectionFields = sectionOutputFields

var sectionsCreate = changing(capability.EffectCreate, capability.IdempotencyNonIdempotent, descriptor("sections",
	"create", "Create a Todoist section",
	"Create one section in a project of an explicit Todoist connection; a repeated call creates a second "+
		"section"+retryNote,
	[]string{"sections", "create"},
	`{"type":"object","properties":{"name":`+sectionNameSchema+`,"project_id":`+idSchema+`,`+
		`"description":`+descriptionSchema+`},"required":["name"],"additionalProperties":false}`,
	json.RawMessage(sectionSchema),
	[]capability.Argument{
		{Name: "name", Description: "Section name, at most 2048 characters on one line", Required: true},
		{Name: "project_id", Description: "Project of the connection; the connection's only project when omitted", Target: "project"},
		{Name: "description", Description: "Section description, at most 16383 characters"},
	},
	sectionFields,
	[]capability.Example{{Description: "Create a section",
		Arguments: json.RawMessage(`{"name":"Waiting","project_id":"6XGgm6PHrGgMpCFX"}`)}}))

var sectionsUpdate = changing(capability.EffectUpdate, capability.IdempotencyIdempotent, descriptor("sections",
	"update", "Update a Todoist section",
	"Change the name or description of one section of a project of an explicit Todoist connection; moving and "+
		"reordering have tools of their own",
	[]string{"sections", "update"},
	`{"type":"object","properties":{"section_id":`+idSchema+`,"name":`+sectionNameSchema+`,`+
		`"description":`+descriptionSchema+`},"required":["section_id"],"additionalProperties":false}`,
	json.RawMessage(sectionSchema),
	[]capability.Argument{sectionIDArgument,
		{Name: "name", Description: "New section name, at most 2048 characters on one line"},
		{Name: "description", Description: "New section description; an empty text removes it"},
	},
	sectionFields,
	[]capability.Example{{Description: "Rename a section",
		Arguments: json.RawMessage(`{"section_id":"6Jf8VQXxpwv56VQ7","name":"Blocked"}`)}}))

var sectionsReorder = changing(capability.EffectUpdate, capability.IdempotencyIdempotent, descriptor("sections",
	"reorder", "Reorder a Todoist section",
	"Set the position of one section inside its project of an explicit Todoist connection",
	[]string{"sections", "reorder", "update"},
	`{"type":"object","properties":{"section_id":`+idSchema+`,"order":`+sectionOrderSchema+`},`+
		`"required":["section_id","order"],"additionalProperties":false}`,
	json.RawMessage(sectionSchema),
	[]capability.Argument{sectionIDArgument,
		{Name: "order", Description: "New position of the section in its project, from 0", Required: true}},
	sectionFields,
	[]capability.Example{{Description: "Make a section the first one",
		Arguments: json.RawMessage(`{"section_id":"6Jf8VQXxpwv56VQ7","order":1}`)}}))

var sectionsMove = changing(capability.EffectUpdate, capability.IdempotencyIdempotent, descriptor("sections",
	"move", "Move a Todoist section",
	"Move one section of an explicit Todoist connection, with its tasks, to another project of the same "+
		"connection",
	[]string{"sections", "move", "update"},
	`{"type":"object","properties":{"section_id":`+idSchema+`,"project_id":`+idSchema+`},`+
		`"required":["section_id","project_id"],"additionalProperties":false}`,
	changeOutput("moved"),
	[]capability.Argument{sectionIDArgument,
		{Name: "project_id", Description: "Project of the connection to move the section to", Required: true}},
	structureFields,
	[]capability.Example{{Description: "Move a section to another project",
		Arguments: json.RawMessage(`{"section_id":"6Jf8VQXxpwv56VQ7","project_id":"6XGgm6PHrGgMpCFX"}`)}}))

var sectionsDelete = listedOnly(changing(capability.EffectDelete, capability.IdempotencyIdempotent, descriptor("sections",
	"delete", "Delete a Todoist section",
	"Delete one section of a project of an explicit Todoist connection permanently, with all its tasks. "+
		"Offered only by a connection whose tools list names it"+retryNote,
	[]string{"sections", "delete"},
	sectionIDInput, changeOutput("deleted"), []capability.Argument{sectionIDArgument}, structureFields,
	[]capability.Example{{Description: "Delete a section", Arguments: json.RawMessage(`{"section_id":"6Jf8VQXxpwv56VQ7"}`)}})))

var labelIDArgument = capability.Argument{Name: "label_id", Description: "Personal label ID, as returned by " +
	"todoist.labels.list", Required: true}

var labelFields = []capability.Field{
	{Name: "id", Description: "Label ID"},
	{Name: "name", Description: "Label name, untrusted data"},
}

var labelsCreate = changing(capability.EffectCreate, capability.IdempotencyNonIdempotent, descriptor("labels",
	"create", "Create a Todoist label",
	"Create one personal label of the account bound to an explicit Todoist * account connection; a project "+
		"connection does not offer it, because a label belongs to the whole account"+retryNote,
	[]string{"labels", "create"},
	`{"type":"object","properties":{"name":`+labelNameSchema+`,`+labelFieldInput+`},"required":["name"],`+
		`"additionalProperties":false}`,
	json.RawMessage(labelOutputSchema),
	[]capability.Argument{
		{Name: "name", Description: "Label name, at most 128 characters on one line", Required: true},
		{Name: "color", Description: "Todoist color name such as blue or berry_red"},
		{Name: "is_favorite", Description: "True marks the label as a favorite"},
	},
	labelFields,
	[]capability.Example{{Description: "Create a label", Arguments: json.RawMessage(`{"name":"waiting","color":"grey"}`)}}))

var labelsUpdate = changing(capability.EffectUpdate, capability.IdempotencyIdempotent, descriptor("labels",
	"update", "Update a Todoist label",
	"Change the name, color, or favorite mark of one personal label on an explicit Todoist * account "+
		"connection; a new name reaches every task that carries the label, so on a connection with exclusions "+
		"it is refused when an active task with the label lies outside the connection; completed tasks are "+
		"not checked",
	[]string{"labels", "update"},
	`{"type":"object","properties":{"label_id":`+idSchema+`,"name":`+labelNameSchema+`,`+labelFieldInput+`},`+
		`"required":["label_id"],"additionalProperties":false}`,
	json.RawMessage(labelOutputSchema),
	[]capability.Argument{labelIDArgument,
		{Name: "name", Description: "New label name, at most 128 characters on one line"},
		{Name: "color", Description: "Todoist color name such as blue or berry_red"},
		{Name: "is_favorite", Description: "True marks the label as a favorite, false removes the mark"},
	},
	labelFields,
	[]capability.Example{{Description: "Rename a label",
		Arguments: json.RawMessage(`{"label_id":"2156154810","name":"blocked"}`)}}))

var labelsReorder = changing(capability.EffectUpdate, capability.IdempotencyIdempotent, descriptor("labels",
	"reorder", "Reorder a Todoist label",
	"Set the position of one personal label in the label list on an explicit Todoist * account connection",
	[]string{"labels", "reorder", "update"},
	`{"type":"object","properties":{"label_id":`+idSchema+`,"order":`+labelOrderSchema+`},`+
		`"required":["label_id","order"],"additionalProperties":false}`,
	json.RawMessage(labelOutputSchema),
	[]capability.Argument{labelIDArgument,
		{Name: "order", Description: "New position of the label in the label list, from 0", Required: true}},
	labelFields,
	[]capability.Example{{Description: "Make a label the first one", Arguments: json.RawMessage(`{"label_id":"2156154810","order":1}`)}}))

var labelsDelete = listedOnly(changing(capability.EffectDelete, capability.IdempotencyIdempotent, descriptor("labels",
	"delete", "Delete a Todoist label",
	"Delete one personal label on an explicit Todoist * account connection permanently and remove it from "+
		"every task; on a connection with exclusions it is refused when an active task with the label lies "+
		"outside the connection, and completed tasks are not checked. Offered only by a connection whose "+
		"tools list names it"+retryNote,
	[]string{"labels", "delete"},
	`{"type":"object","properties":{"label_id":`+idSchema+`},"required":["label_id"],"additionalProperties":false}`,
	changeOutput("deleted"), []capability.Argument{labelIDArgument}, structureFields,
	[]capability.Example{{Description: "Delete a label", Arguments: json.RawMessage(`{"label_id":"2156154810"}`)}})))

// structureTools are the structure changes the organize profile ticks. Archiving, unarchiving, and the
// deletes stay unticked.
var structureTools = []string{projectsCreate.ID, projectsUpdate.ID, sectionsCreate.ID, sectionsUpdate.ID,
	sectionsReorder.ID, sectionsMove.ID, labelsCreate.ID, labelsUpdate.ID, labelsReorder.ID}

// listedOnly marks a change that deletes more than itself: no connection offers it for its permissions
// alone, only one whose tools list names it.
func listedOnly(d capability.Descriptor) capability.Descriptor {
	d.RequiresToolAllowList = true
	return d
}

// accountOnly refuses a tool on a project connection as an unsupported capability before any credential is
// resolved: the tool acts on the whole account, beyond any list of projects.
func accountOnly(id string, next capability.Handler) capability.Handler {
	return reachedBy(id, func(bound scope) bool { return bound.all }, next)
}

// reachedBy refuses a tool as an unsupported capability before any credential is resolved when the
// connection's scope does not pass the check.
func reachedBy(id string, reached func(scope) bool, next capability.Handler) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		if resolved == nil {
			return nil, providerError("open", "no connection was selected")
		}
		bound, err := scopeOf(resolved)
		if err != nil {
			return nil, providerError("open", err.Error())
		}
		if !reached(bound) {
			return nil, &capability.UnsupportedError{Connection: resolved.Name, Capability: id}
		}
		return next(ctx, resolved, secrets, red, raw)
	}
}

func validColor(value string) bool {
	for _, color := range colors {
		if value == color {
			return true
		}
	}
	return false
}

// projectValues are the project values a create or an update sets. A nil pointer or an empty text leaves a
// value unchanged.
type projectValues struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Color       string  `json:"color"`
	IsFavorite  *bool   `json:"is_favorite"`
	ViewStyle   string  `json:"view_style"`
}

func (v projectValues) check() error {
	switch {
	case v.Name != nil && !validText(*v.Name, maxProjectName):
		return invalidRequest("name must be 1 to 120 characters on one line without padding")
	case v.Description != nil && !validMultiline(*v.Description, maxDescription, true):
		return invalidRequest("description must be at most 16383 characters without control characters")
	case v.Color != "" && !validColor(v.Color):
		return invalidRequest("color must be a Todoist color name")
	case v.ViewStyle != "" && v.ViewStyle != "list" && v.ViewStyle != "board" && v.ViewStyle != "calendar":
		return invalidRequest("view_style must be list, board, or calendar")
	}
	return nil
}

func (v projectValues) body() map[string]any {
	body := map[string]any{}
	if v.Name != nil {
		body["name"] = *v.Name
	}
	if v.Description != nil {
		body["description"] = *v.Description
	}
	if v.Color != "" {
		body["color"] = v.Color
	}
	if v.IsFavorite != nil {
		body["is_favorite"] = *v.IsFavorite
	}
	if v.ViewStyle != "" {
		body["view_style"] = v.ViewStyle
	}
	return body
}

// workspaceProject reports whether a project belongs to a Todoist workspace.
func workspaceProject(raw rawProject) bool {
	value := bytes.TrimSpace(raw.WorkspaceID)
	return len(value) > 0 && !bytes.Equal(value, []byte("null")) && !bytes.Equal(value, []byte(`""`))
}

// personalProject reads the project a change concerns and refuses one outside the connection or of a
// workspace. It runs on every connection, because only this read tells a personal project from a workspace
// one.
func (c *Client) personalProject(ctx context.Context, op, id string) (rawProject, error) {
	raw, err := c.readProject(ctx, op, id)
	if err != nil {
		return rawProject{}, err
	}
	if workspaceProject(raw) {
		return rawProject{}, invalidRequest("the project belongs to a Todoist workspace; only personal projects " +
			"are changed")
	}
	return raw, nil
}

// subprojectsInScope refuses to archive or delete a project when one of its subprojects at any depth lies
// outside the connection, or when it, or a subproject, holds an excluded section, because Todoist archives
// and deletes a project with all its descendants. It reads the active projects and, for a delete, the
// archived ones too; a tree larger than the bound is refused unchecked. Only an account connection without
// exclusions holds every project.
func (c *Client) subprojectsInScope(ctx context.Context, op, projectID string, archived bool) error {
	if c.scope.unrestricted() {
		return nil
	}
	if err := c.loadProjects(ctx, op, archived); err != nil {
		return err
	}
	for id, ancestor := range c.tree.parents {
		for steps := 0; ancestor != "" && steps < len(c.tree.parents); steps++ {
			if ancestor == projectID {
				if c.excludedLoaded(id) || (!c.scope.exact(id) && !c.below(id)) {
					return errSubprojectOutside
				}
				break
			}
			ancestor = c.tree.parents[ancestor]
		}
	}
	return c.sectionsBelow(ctx, op, projectID)
}

var errSubprojectOutside = invalidRequest("the project has a subproject outside this connection, which Todoist " +
	"would change with it")

// sectionsBelow refuses a project that holds an excluded section itself or in a subproject at any depth. The
// section is read to learn its project; a section Todoist does not hold lies in no project.
func (c *Client) sectionsBelow(ctx context.Context, op, projectID string) error {
	for _, sectionID := range c.scope.exSections {
		var raw rawSection
		if err := c.get(ctx, op, "/sections/"+url.PathEscape(sectionID), nil, &raw); err != nil {
			var failure *provider.Error
			if errors.As(err, &failure) && failure.Class == provider.ClassNotFound {
				continue
			}
			return err
		}
		if raw.ID != sectionID || !validID(raw.ProjectID) {
			return invalidResponse(op)
		}
		current := raw.ProjectID
		for steps := 0; current != "" && steps <= len(c.tree.parents); steps++ {
			if current == projectID {
				return errSubprojectOutside
			}
			current = c.tree.parents[current]
		}
	}
	return nil
}

// changedProject checks the project a change answered with. Its request may have changed something, so every
// failure says so.
func (c *Client) changedProject(ctx context.Context, op string, raw rawProject, want string) (*Project, error) {
	if raw.ID == "" || (want != "" && raw.ID != want) {
		return nil, invalidChange(op, true)
	}
	c.noteProject(raw)
	ok, err := c.projectAllowed(ctx, op, raw.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "Todoist answered with a project outside the connection" + uncertain}
	}
	project := projectOf(raw)
	return &project, nil
}

func prepareProjectsCreate(bound scope, arguments struct {
	ParentID string `json:"parent_id"`
	projectValues
}) (run, error) {
	const op = "create project"
	if arguments.Name == nil {
		return nil, invalidRequest("name is required")
	}
	if err := checkID("parent_id", arguments.ParentID); err != nil {
		return nil, err
	}
	if err := arguments.check(); err != nil {
		return nil, err
	}
	if !bound.all && arguments.ParentID == "" {
		return nil, invalidRequest("name parent_id; a connection bound to a project tree creates subprojects only")
	}
	body := arguments.body()
	return func(ctx context.Context, c *Client) (any, error) {
		if arguments.ParentID != "" {
			// A new project joins a bound project only below a project tree: a bare project ID binds that
			// project alone, so a project created below it would be outside the connection.
			if !c.scope.all {
				member, err := c.treeMember(ctx, op, arguments.ParentID)
				if err != nil {
					return nil, err
				}
				if !member {
					return nil, outsideScope("project")
				}
			}
			if _, err := c.personalProject(ctx, op, arguments.ParentID); err != nil {
				return nil, err
			}
			body["parent_id"] = arguments.ParentID
		}
		var raw rawProject
		if err := c.change(ctx, op, http.MethodPost, "/projects", body, &raw); err != nil {
			return nil, err
		}
		return c.changedProject(ctx, op, raw, "")
	}, nil
}

func prepareProjectsUpdate(bound scope, arguments struct {
	ProjectID string `json:"project_id"`
	projectValues
}) (run, error) {
	const op = "update project"
	if err := bound.requireProjectLevel(); err != nil {
		return nil, err
	}
	if err := checkRequiredID("project_id", arguments.ProjectID); err != nil {
		return nil, err
	}
	if err := arguments.check(); err != nil {
		return nil, err
	}
	body := arguments.body()
	if len(body) == 0 {
		return nil, invalidRequest("name at least one value to change")
	}
	if err := bound.selectProject(arguments.ProjectID); err != nil {
		return nil, err
	}
	return func(ctx context.Context, c *Client) (any, error) {
		if _, err := c.personalProject(ctx, op, arguments.ProjectID); err != nil {
			return nil, err
		}
		var raw rawProject
		if err := c.change(ctx, op, http.MethodPost, "/projects/"+url.PathEscape(arguments.ProjectID), body,
			&raw); err != nil {
			return nil, err
		}
		return c.changedProject(ctx, op, raw, arguments.ProjectID)
	}, nil
}

// prepareProjectState builds the archive, unarchive, and delete of one project. Each is a request of its
// own. An archive and a delete act on every subproject as well, which a project connection checks first.
func prepareProjectState(op, method, suffix, result string, subprojects, archived bool) func(scope, struct {
	ProjectID string `json:"project_id"`
}) (run, error) {
	return func(bound scope, arguments struct {
		ProjectID string `json:"project_id"`
	}) (run, error) {
		if err := checkRequiredID("project_id", arguments.ProjectID); err != nil {
			return nil, err
		}
		if err := bound.requireProjectLevel(); err != nil {
			return nil, err
		}
		if err := bound.selectProject(arguments.ProjectID); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if _, err := c.personalProject(ctx, op, arguments.ProjectID); err != nil {
				return nil, err
			}
			if subprojects {
				if err := c.subprojectsInScope(ctx, op, arguments.ProjectID, archived); err != nil {
					return nil, err
				}
			}
			if err := c.change(ctx, op, method, "/projects/"+url.PathEscape(arguments.ProjectID)+suffix, nil,
				nil); err != nil {
				return nil, err
			}
			return &Change{ID: arguments.ProjectID, Result: result}, nil
		}, nil
	}
}

// sectionInScope reads the section a change concerns and refuses one outside the connection. Only an
// account connection without exclusions reads nothing first.
func (c *Client) sectionInScope(ctx context.Context, op, id string) error {
	if c.scope.unrestricted() {
		return nil
	}
	_, err := c.readSection(ctx, op, id)
	return err
}

// changedSection checks the section a change answered with.
func (c *Client) changedSection(ctx context.Context, op string, raw rawSection, want string) (*Section, error) {
	if raw.ID == "" || (want != "" && raw.ID != want) {
		return nil, invalidChange(op, true)
	}
	ok, err := c.sectionAllowed(ctx, op, raw)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "Todoist answered with a section outside the connection's projects" + uncertain}
	}
	section := sectionOf(raw)
	return &section, nil
}

func checkSectionName(name *string) error {
	if name != nil && !validText(*name, maxSectionName) {
		return invalidRequest("name must be 1 to 2048 characters on one line without padding")
	}
	return nil
}

func checkSectionDescription(description *string) error {
	if description != nil && !validMultiline(*description, maxDescription, true) {
		return invalidRequest("description must be at most 16383 characters without control characters")
	}
	return nil
}

func prepareSectionsCreate(bound scope, arguments struct {
	Name        *string `json:"name"`
	ProjectID   string  `json:"project_id"`
	Description *string `json:"description"`
}) (run, error) {
	const op = "create section"
	if arguments.Name == nil {
		return nil, invalidRequest("name is required")
	}
	if err := checkSectionName(arguments.Name); err != nil {
		return nil, err
	}
	if err := checkSectionDescription(arguments.Description); err != nil {
		return nil, err
	}
	if err := checkID("project_id", arguments.ProjectID); err != nil {
		return nil, err
	}
	if err := bound.requireProjectLevel(); err != nil {
		return nil, err
	}
	if err := bound.selectProject(arguments.ProjectID); err != nil {
		return nil, err
	}
	project := arguments.ProjectID
	if project == "" {
		single, ok := bound.defaultProject()
		if !ok {
			return nil, invalidRequest("name project_id; only a connection with one project has a default project")
		}
		project = single
	}
	body := map[string]any{"name": *arguments.Name, "project_id": project}
	if arguments.Description != nil {
		body["description"] = *arguments.Description
	}
	return func(ctx context.Context, c *Client) (any, error) {
		if err := c.requireProject(ctx, op, project); err != nil {
			return nil, err
		}
		var raw rawSection
		if err := c.change(ctx, op, http.MethodPost, "/sections", body, &raw); err != nil {
			return nil, err
		}
		return c.changedSection(ctx, op, raw, "")
	}, nil
}

func prepareSectionsUpdate(_ scope, arguments struct {
	SectionID   string  `json:"section_id"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
}) (run, error) {
	const op = "update section"
	if err := checkRequiredID("section_id", arguments.SectionID); err != nil {
		return nil, err
	}
	if err := checkSectionName(arguments.Name); err != nil {
		return nil, err
	}
	if err := checkSectionDescription(arguments.Description); err != nil {
		return nil, err
	}
	body := map[string]any{}
	if arguments.Name != nil {
		body["name"] = *arguments.Name
	}
	if arguments.Description != nil {
		body["description"] = *arguments.Description
	}
	if len(body) == 0 {
		return nil, invalidRequest("name at least one value to change")
	}
	return sectionChange(op, arguments.SectionID, body), nil
}

func prepareSectionsReorder(bound scope, arguments struct {
	SectionID string `json:"section_id"`
	Order     *int   `json:"order"`
}) (run, error) {
	if err := bound.requireProjectLevel(); err != nil {
		return nil, err
	}
	if err := checkRequiredID("section_id", arguments.SectionID); err != nil {
		return nil, err
	}
	if arguments.Order == nil || *arguments.Order < 0 || *arguments.Order > maxSectionPos {
		return nil, invalidRequest("order must be a position from 0")
	}
	return sectionChange("reorder section", arguments.SectionID, map[string]any{"section_order": *arguments.Order}), nil
}

// sectionChange sends one update of a section of the connection.
func sectionChange(op, id string, body map[string]any) run {
	return func(ctx context.Context, c *Client) (any, error) {
		if err := c.sectionInScope(ctx, op, id); err != nil {
			return nil, err
		}
		var raw rawSection
		if err := c.change(ctx, op, http.MethodPost, "/sections/"+url.PathEscape(id), body, &raw); err != nil {
			return nil, err
		}
		return c.changedSection(ctx, op, raw, id)
	}
}

// prepareSectionsMove moves a section through the one Sync command API v1 offers for it. The command and
// its two arguments are fixed here; the section and the destination are both checked against the
// connection first.
func prepareSectionsMove(bound scope, arguments struct {
	SectionID string `json:"section_id"`
	ProjectID string `json:"project_id"`
}) (run, error) {
	const op = "move section"
	if err := checkRequiredID("section_id", arguments.SectionID); err != nil {
		return nil, err
	}
	if err := checkRequiredID("project_id", arguments.ProjectID); err != nil {
		return nil, err
	}
	if err := bound.requireProjectLevel(); err != nil {
		return nil, err
	}
	if err := bound.selectProject(arguments.ProjectID); err != nil {
		return nil, err
	}
	return func(ctx context.Context, c *Client) (any, error) {
		if err := c.sectionInScope(ctx, op, arguments.SectionID); err != nil {
			return nil, err
		}
		if err := c.requireProject(ctx, op, arguments.ProjectID); err != nil {
			return nil, err
		}
		if err := c.command(ctx, op, "section_move", map[string]any{"id": arguments.SectionID,
			"project_id": arguments.ProjectID}); err != nil {
			return nil, err
		}
		return &Change{ID: arguments.SectionID, Result: "moved"}, nil
	}, nil
}

func prepareSectionsDelete(bound scope, arguments struct {
	SectionID string `json:"section_id"`
}) (run, error) {
	const op = "delete section"
	if err := bound.requireProjectLevel(); err != nil {
		return nil, err
	}
	if err := checkRequiredID("section_id", arguments.SectionID); err != nil {
		return nil, err
	}
	return func(ctx context.Context, c *Client) (any, error) {
		if err := c.sectionInScope(ctx, op, arguments.SectionID); err != nil {
			return nil, err
		}
		if err := c.change(ctx, op, http.MethodDelete, "/sections/"+url.PathEscape(arguments.SectionID), nil,
			nil); err != nil {
			return nil, err
		}
		return &Change{ID: arguments.SectionID, Result: "deleted"}, nil
	}, nil
}

// labelValues are the label values a create or an update sets.
type labelValues struct {
	Name       *string `json:"name"`
	Color      string  `json:"color"`
	IsFavorite *bool   `json:"is_favorite"`
}

func (v labelValues) body() (map[string]any, error) {
	switch {
	case v.Name != nil && !validText(*v.Name, maxLabelName):
		return nil, invalidRequest("name must be 1 to 128 characters on one line without padding")
	case v.Color != "" && !validColor(v.Color):
		return nil, invalidRequest("color must be a Todoist color name")
	}
	body := map[string]any{}
	if v.Name != nil {
		body["name"] = *v.Name
	}
	if v.Color != "" {
		body["color"] = v.Color
	}
	if v.IsFavorite != nil {
		body["is_favorite"] = *v.IsFavorite
	}
	return body, nil
}

// changedLabel checks the label a change answered with.
func changedLabel(op string, raw rawLabel, want string) (*Label, error) {
	if raw.ID == "" || (want != "" && raw.ID != want) {
		return nil, invalidChange(op, true)
	}
	label := labelOf(raw)
	return &label, nil
}

// labelChange sends one create or update of a personal label.
func labelChange(op, path string, body map[string]any, want string) run {
	return func(ctx context.Context, c *Client) (any, error) {
		var raw rawLabel
		if err := c.change(ctx, op, http.MethodPost, path, body, &raw); err != nil {
			return nil, err
		}
		return changedLabel(op, raw, want)
	}
}

func prepareLabelsCreate(_ scope, arguments labelValues) (run, error) {
	if arguments.Name == nil {
		return nil, invalidRequest("name is required")
	}
	body, err := arguments.body()
	if err != nil {
		return nil, err
	}
	return labelChange("create label", "/labels", body, ""), nil
}

// labelTasksInScope refuses a label change that reaches tasks outside the connection. A new name or a
// delete reaches every task with the label, so on a connection with exclusions the active tasks that carry
// it are read first, at most maxScanPages pages, and the change is refused when one lies outside or the
// list cannot be read to its end. Completed tasks are not checked.
func (c *Client) labelTasksInScope(ctx context.Context, op, labelID string) error {
	if !c.scope.hasExcludes() {
		return nil
	}
	var label rawLabel
	if err := c.get(ctx, op, "/labels/"+url.PathEscape(labelID), nil, &label); err != nil {
		return err
	}
	if label.ID != labelID || label.Name == "" {
		return invalidResponse(op)
	}
	after := ""
	for pages := 0; ; pages++ {
		if pages == maxScanPages {
			return &provider.Error{Class: provider.ClassProviderError, Op: op,
				Message: "the tasks with this label could not all be checked; nothing was changed"}
		}
		entries, next, err := c.page(ctx, op, "/tasks", url.Values{"label": {label.Name}}, maxLimit, after)
		if err != nil {
			return err
		}
		tasks, err := decodeEach(op, entries, func(t rawTask) string { return t.ID })
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if task.IsDeleted {
				continue
			}
			ok, err := c.taskAllowed(ctx, op, task)
			if err != nil {
				return err
			}
			if !ok {
				return invalidRequest("a task with this label lies outside this connection, which the change " +
					"would reach")
			}
		}
		if next == "" {
			return nil
		}
		after = next
	}
}

func prepareLabelsUpdate(_ scope, arguments struct {
	LabelID string `json:"label_id"`
	labelValues
}) (run, error) {
	if err := checkRequiredID("label_id", arguments.LabelID); err != nil {
		return nil, err
	}
	body, err := arguments.body()
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, invalidRequest("name at least one value to change")
	}
	change := labelChange("update label", "/labels/"+url.PathEscape(arguments.LabelID), body, arguments.LabelID)
	if arguments.Name == nil {
		return change, nil
	}
	// Only a new name reaches the tasks that carry the label.
	return func(ctx context.Context, c *Client) (any, error) {
		if err := c.labelTasksInScope(ctx, "update label", arguments.LabelID); err != nil {
			return nil, err
		}
		return change(ctx, c)
	}, nil
}

func prepareLabelsReorder(_ scope, arguments struct {
	LabelID string `json:"label_id"`
	Order   *int   `json:"order"`
}) (run, error) {
	if err := checkRequiredID("label_id", arguments.LabelID); err != nil {
		return nil, err
	}
	if arguments.Order == nil || *arguments.Order < 0 || *arguments.Order > maxLabelPos {
		return nil, invalidRequest("order must be a position from 0 through 32767")
	}
	return labelChange("reorder label", "/labels/"+url.PathEscape(arguments.LabelID),
		map[string]any{"order": *arguments.Order}, arguments.LabelID), nil
}

func prepareLabelsDelete(_ scope, arguments struct {
	LabelID string `json:"label_id"`
}) (run, error) {
	const op = "delete label"
	if err := checkRequiredID("label_id", arguments.LabelID); err != nil {
		return nil, err
	}
	return func(ctx context.Context, c *Client) (any, error) {
		if err := c.labelTasksInScope(ctx, op, arguments.LabelID); err != nil {
			return nil, err
		}
		if err := c.change(ctx, op, http.MethodDelete, "/labels/"+url.PathEscape(arguments.LabelID), nil,
			nil); err != nil {
			return nil, err
		}
		return &Change{ID: arguments.LabelID, Result: "deleted"}, nil
	}, nil
}
