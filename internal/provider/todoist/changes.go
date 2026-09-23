package todoist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The task and comment changes. Every change requires confirmation in its own invoke request and is sent
// exactly once. A create is non-idempotent: repeating it makes a second task or comment. Closing is
// non-idempotent as well, because closing a recurring task moves it to its next occurrence instead of
// completing it. Every other change leaves the same state when it is repeated.
//
// Comment attachments are not offered: Todoist takes them only as an uploaded file or as a link to a file,
// and neither an upload route nor an arbitrary URL belongs to this provider's boundary.

// changeRisk is the contract of a change of a task or comment of the connection's projects.
func changeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: dataSensitivity}
}

// changing gives a descriptor the risk of a change.
func changing(effect capability.Effect, idempotency capability.Idempotency, d capability.Descriptor) capability.Descriptor {
	d.Risk = changeRisk(effect, idempotency)
	return d
}

// Bounds of the change arguments. The input schemas mirror them, and the prepare steps apply them again.
const (
	maxContent     = 500
	maxDescription = 16383
	maxComment     = 15000
	maxLabels      = 100
	maxDueString   = 200
	maxDuration    = 10000
)

// Input schemas of the changes. A description and a comment may span lines; no other control character is
// accepted.
const (
	contentSchema     = `{"type":"string","minLength":1,"maxLength":500,"pattern":"^[^\\x00-\\x1f\\x7f]+$"}`
	descriptionSchema = `{"type":"string","maxLength":16383,"pattern":"^[^\\x00-\\x08\\x0b\\x0c\\x0e-\\x1f\\x7f]*$"}`
	commentTextSchema = `{"type":"string","minLength":1,"maxLength":15000,` +
		`"pattern":"^[^\\x00-\\x08\\x0b\\x0c\\x0e-\\x1f\\x7f]+$"}`
	labelsSchema    = `{"type":"array","maxItems":100,"items":` + labelSchema + `}`
	prioritySchema  = `{"type":"integer","minimum":1,"maximum":4}`
	dueStringSchema = `{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\x00-\\x1f\\x7f]+$"}`
	langSchema      = `{"type":"string","pattern":"^[a-z]{2}$"}`
	durationSchema  = `{"type":"integer","minimum":1,"maximum":10000}`
	unitSchema      = `{"type":"string","enum":["minute","day"]}`
	flagSchema      = `{"type":"boolean"}`
	taskFieldInput  = `"description":` + descriptionSchema + `,"labels":` + labelsSchema + `,` +
		`"priority":` + prioritySchema + `,"assignee_id":` + idSchema + `,"due_string":` + dueStringSchema + `,` +
		`"due_date":` + dateSchema + `,"due_datetime":` + instantSchema + `,"due_lang":` + langSchema + `,` +
		`"duration":` + durationSchema + `,"duration_unit":` + unitSchema
)

// changeOutput is the schema of the answer of a change that returns no resource.
func changeOutput(result string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"id":` + stringSchema + `,` +
		`"result":{"type":"string","enum":["` + result + `"]}},"required":["id","result"],"additionalProperties":false}`)
}

// Change is the answer of a change that returns no resource: the ID it concerned and what happened to it.
type Change struct {
	ID     string `json:"id"`
	Result string `json:"result"`
}

// retryNote closes the description of every change that is not safe to repeat blindly.
const retryNote = "; sent once and never repeated by Qatlas: after an unclear result, read the current state " +
	"before repeating it"

var taskFieldArguments = []capability.Argument{
	{Name: "description", Description: "Task description in Markdown, at most 16383 characters; stored as given"},
	{Name: "labels", Description: "Personal label names; replaces every label of the task, [] removes them all"},
	{Name: "priority", Description: "1 (normal) through 4 (urgent)"},
	{Name: "assignee_id", Description: "User ID of the responsible collaborator of a shared project"},
	{Name: "due_string", Description: "Due date as Todoist reads it, such as \"tomorrow 9am\" or \"every monday\"; " +
		"excludes due_date and due_datetime"},
	{Name: "due_date", Description: "Due date as YYYY-MM-DD"},
	{Name: "due_datetime", Description: "Due time as RFC 3339, such as 2026-09-30T09:00:00Z"},
	{Name: "due_lang", Description: "Two-letter language of due_string; only together with due_string"},
	{Name: "duration", Description: "Planned duration, from 1 through 10000 units; together with duration_unit"},
	{Name: "duration_unit", Description: "minute or day; together with duration"},
}

var changedTaskFields = []capability.Field{
	{Name: "id", Description: "Task ID"},
	{Name: "content", Description: "Task title, untrusted data"},
	{Name: "description", Description: "Task description, untrusted data"},
}

var tasksCreate = changing(capability.EffectCreate, capability.IdempotencyNonIdempotent, descriptor("tasks",
	"create", "Create a Todoist task",
	"Create one task in a project of an explicit Todoist connection, optionally in a section or below a parent "+
		"task of that connection; a repeated call creates a second task"+retryNote,
	[]string{"tasks", "create"},
	`{"type":"object","properties":{"content":`+contentSchema+`,"project_id":`+idSchema+`,"section_id":`+idSchema+`,`+
		`"parent_id":`+idSchema+`,`+taskFieldInput+`},"required":["content"],"additionalProperties":false}`,
	json.RawMessage(taskSchema),
	append([]capability.Argument{
		{Name: "content", Description: "Task title, at most 500 characters on one line", Required: true},
		{Name: "project_id", Description: "Project of the connection; the connection's only project when omitted, " +
			"the Inbox on a * connection"},
		{Name: "section_id", Description: "Section of a project of the connection; excludes parent_id"},
		{Name: "parent_id", Description: "Task of the connection the new task becomes a subtask of; excludes section_id"},
	}, taskFieldArguments...),
	changedTaskFields,
	[]capability.Example{{
		Description: "Create a task due tomorrow",
		Arguments:   json.RawMessage(`{"content":"Send the invoice","project_id":"6XGgm6PHrGgMpCFX","due_string":"tomorrow"}`),
	}}))

var tasksUpdate = changing(capability.EffectUpdate, capability.IdempotencyIdempotent, descriptor("tasks", "update",
	"Update a Todoist task",
	"Change the content, description, labels, priority, assignee, due date, or duration of one task of an "+
		"explicit Todoist connection; fields left out stay unchanged. Moving, closing, and reopening have tools "+
		"of their own",
	[]string{"tasks", "update"},
	`{"type":"object","properties":{"task_id":`+idSchema+`,"content":`+contentSchema+`,`+taskFieldInput+`,`+
		`"clear_due":`+flagSchema+`,"clear_duration":`+flagSchema+`,"unassign":`+flagSchema+`},`+
		`"required":["task_id"],"additionalProperties":false}`,
	json.RawMessage(taskSchema),
	append(append([]capability.Argument{
		{Name: "task_id", Description: "Task ID, as returned by a task list", Required: true},
		{Name: "content", Description: "New task title, at most 500 characters on one line"},
	}, taskFieldArguments...),
		capability.Argument{Name: "clear_due", Description: "True removes the due date"},
		capability.Argument{Name: "clear_duration", Description: "True removes the duration"},
		capability.Argument{Name: "unassign", Description: "True removes the responsible collaborator"},
	),
	changedTaskFields,
	[]capability.Example{{
		Description: "Raise the priority and replace the labels of a task",
		Arguments:   json.RawMessage(`{"task_id":"6X7rM8997g3RQmvh","priority":4,"labels":["waiting"]}`),
	}}))

var tasksMove = changing(capability.EffectUpdate, capability.IdempotencyIdempotent, descriptor("tasks", "move",
	"Move a Todoist task",
	"Move one task of an explicit Todoist connection, with its subtasks, to another project, section, or parent "+
		"task of the same connection",
	[]string{"tasks", "move", "update"},
	`{"type":"object","properties":{"task_id":`+idSchema+`,"project_id":`+idSchema+`,"section_id":`+idSchema+`,`+
		`"parent_id":`+idSchema+`},"required":["task_id"],"additionalProperties":false}`,
	json.RawMessage(taskSchema),
	[]capability.Argument{
		{Name: "task_id", Description: "Task ID, as returned by a task list", Required: true},
		{Name: "project_id", Description: "Project of the connection to move to; name exactly one destination"},
		{Name: "section_id", Description: "Section of a project of the connection to move to"},
		{Name: "parent_id", Description: "Task of the connection to move below"},
	},
	changedTaskFields,
	[]capability.Example{{
		Description: "Move a task into a section",
		Arguments:   json.RawMessage(`{"task_id":"6X7rM8997g3RQmvh","section_id":"6Jf8VQXxpwv56VQ7"}`),
	}}))

var taskIDInput = `{"type":"object","properties":{"task_id":` + idSchema + `},"required":["task_id"],` +
	`"additionalProperties":false}`

var taskIDArgument = []capability.Argument{{Name: "task_id", Description: "Task ID, as returned by a task list",
	Required: true}}

var changeFields = []capability.Field{
	{Name: "id", Description: "ID of the task or comment the change concerned"},
	{Name: "result", Description: "What happened to it"},
}

var tasksClose = changing(capability.EffectUpdate, capability.IdempotencyNonIdempotent, descriptor("tasks", "close",
	"Close a Todoist task",
	"Complete one task of an explicit Todoist connection with its subtasks; a recurring task moves to its next "+
		"occurrence instead, so a repeated call skips one"+retryNote,
	[]string{"tasks", "close", "complete", "update"},
	taskIDInput, changeOutput("closed"), taskIDArgument, changeFields,
	[]capability.Example{{Description: "Complete a task", Arguments: json.RawMessage(`{"task_id":"6X7rM8997g3RQmvh"}`)}}))

var tasksReopen = changing(capability.EffectUpdate, capability.IdempotencyIdempotent, descriptor("tasks", "reopen",
	"Reopen a Todoist task",
	"Make one completed task of an explicit Todoist connection active again",
	[]string{"tasks", "reopen", "uncomplete", "update"},
	taskIDInput, changeOutput("reopened"), taskIDArgument, changeFields,
	[]capability.Example{{Description: "Reopen a completed task", Arguments: json.RawMessage(`{"task_id":"6X7rM8997g3RQmvh"}`)}}))

var tasksDelete = changing(capability.EffectDelete, capability.IdempotencyIdempotent, descriptor("tasks", "delete",
	"Delete a Todoist task",
	"Delete one task of an explicit Todoist connection permanently, with its subtasks and comments",
	[]string{"tasks", "delete"},
	taskIDInput, changeOutput("deleted"), taskIDArgument, changeFields,
	[]capability.Example{{Description: "Delete a task", Arguments: json.RawMessage(`{"task_id":"6X7rM8997g3RQmvh"}`)}}))

var commentsCreate = changing(capability.EffectCreate, capability.IdempotencyNonIdempotent, descriptor("comments",
	"create", "Create a Todoist comment",
	"Add one text comment to exactly one task or one project of an explicit Todoist connection, without an "+
		"attachment; a repeated call adds a second comment"+retryNote,
	[]string{"comments", "create"},
	`{"type":"object","properties":{"task_id":`+idSchema+`,"project_id":`+idSchema+`,"content":`+commentTextSchema+`},`+
		`"required":["content"],"additionalProperties":false}`,
	json.RawMessage(commentSchema),
	[]capability.Argument{
		{Name: "task_id", Description: "Task to comment on; give task_id or project_id"},
		{Name: "project_id", Description: "Project of the connection to comment on"},
		{Name: "content", Description: "Comment text in Markdown, at most 15000 characters; stored as given", Required: true},
	},
	[]capability.Field{{Name: "id", Description: "Comment ID"}, {Name: "content", Description: "Comment text, untrusted data"}},
	[]capability.Example{{Description: "Comment on a task",
		Arguments: json.RawMessage(`{"task_id":"6X7rM8997g3RQmvh","content":"Waiting for the signed offer"}`)}}))

var commentIDSchema = `"comment_id":` + idSchema

var commentsUpdate = changing(capability.EffectUpdate, capability.IdempotencyIdempotent, descriptor("comments",
	"update", "Update a Todoist comment",
	"Replace the text of one comment of a task or project of an explicit Todoist connection",
	[]string{"comments", "update"},
	`{"type":"object","properties":{`+commentIDSchema+`,"content":`+commentTextSchema+`},`+
		`"required":["comment_id","content"],"additionalProperties":false}`,
	json.RawMessage(commentSchema),
	[]capability.Argument{
		{Name: "comment_id", Description: "Comment ID, as returned by todoist.comments.list", Required: true},
		{Name: "content", Description: "New comment text, at most 15000 characters", Required: true},
	},
	[]capability.Field{{Name: "id", Description: "Comment ID"}, {Name: "content", Description: "Comment text, untrusted data"}},
	[]capability.Example{{Description: "Correct a comment",
		Arguments: json.RawMessage(`{"comment_id":"6X7rfFVPjhvv84XG","content":"Offer signed on Monday"}`)}}))

var commentsDelete = changing(capability.EffectDelete, capability.IdempotencyIdempotent, descriptor("comments",
	"delete", "Delete a Todoist comment",
	"Delete one comment of a task or project of an explicit Todoist connection permanently",
	[]string{"comments", "delete"},
	`{"type":"object","properties":{`+commentIDSchema+`},"required":["comment_id"],"additionalProperties":false}`,
	changeOutput("deleted"),
	[]capability.Argument{{Name: "comment_id", Description: "Comment ID, as returned by todoist.comments.list",
		Required: true}},
	changeFields,
	[]capability.Example{{Description: "Delete a comment", Arguments: json.RawMessage(`{"comment_id":"6X7rfFVPjhvv84XG"}`)}}))

// changeTools are the changes the tasks profile ticks. The deletes stay unticked.
var changeTools = []string{tasksCreate.ID, tasksUpdate.ID, tasksMove.ID, tasksClose.ID, tasksReopen.ID,
	commentsCreate.ID, commentsUpdate.ID}

// taskFields are the task values a create or an update sets. A nil pointer leaves a value unchanged.
type taskFields struct {
	Content      *string   `json:"content"`
	Description  *string   `json:"description"`
	Labels       *[]string `json:"labels"`
	Priority     int       `json:"priority"`
	AssigneeID   string    `json:"assignee_id"`
	DueString    string    `json:"due_string"`
	DueDate      string    `json:"due_date"`
	DueDatetime  string    `json:"due_datetime"`
	DueLang      string    `json:"due_lang"`
	Duration     int       `json:"duration"`
	DurationUnit string    `json:"duration_unit"`
}

// check applies the bounds of every value that is set. No error quotes a value.
func (f taskFields) check() error {
	if f.Content != nil && !validText(*f.Content, maxContent) {
		return invalidRequest("content must be 1 to 500 characters on one line without padding")
	}
	if f.Description != nil && !validMultiline(*f.Description, maxDescription, true) {
		return invalidRequest("description must be at most 16383 characters without control characters")
	}
	if f.Labels != nil {
		if len(*f.Labels) > maxLabels {
			return invalidRequest("labels holds at most 100 names")
		}
		seen := map[string]bool{}
		for _, label := range *f.Labels {
			if !validText(label, 100) || seen[label] {
				return invalidRequest("labels must be distinct names of 1 to 100 characters without control characters")
			}
			seen[label] = true
		}
	}
	if f.Priority != 0 && (f.Priority < 1 || f.Priority > 4) {
		return invalidRequest("priority must be 1 through 4")
	}
	if err := checkID("assignee_id", f.AssigneeID); err != nil {
		return err
	}
	dues := 0
	for _, value := range []string{f.DueString, f.DueDate, f.DueDatetime} {
		if value != "" {
			dues++
		}
	}
	switch {
	case dues > 1:
		return invalidRequest("name at most one of due_string, due_date, and due_datetime")
	case f.DueString != "" && !validText(f.DueString, maxDueString):
		return invalidRequest("due_string must be 1 to 200 characters without control characters or padding")
	case f.DueDate != "" && !validDate(f.DueDate):
		return invalidRequest("due_date must be a date as YYYY-MM-DD")
	case f.DueLang != "" && (f.DueString == "" || !validLang(f.DueLang)):
		return invalidRequest("due_lang must be a two-letter language code and comes only with due_string")
	case (f.Duration == 0) != (f.DurationUnit == ""):
		return invalidRequest("duration and duration_unit come together")
	case f.Duration != 0 && (f.Duration < 1 || f.Duration > maxDuration):
		return invalidRequest("duration must be 1 through 10000")
	case f.DurationUnit != "" && f.DurationUnit != "minute" && f.DurationUnit != "day":
		return invalidRequest("duration_unit must be minute or day")
	}
	if f.DueDatetime != "" {
		if _, _, err := parseInstant("due_datetime", f.DueDatetime); err != nil {
			return err
		}
	}
	return nil
}

// set reports whether any value is set.
func (f taskFields) set() bool {
	return f.Content != nil || f.Description != nil || f.Labels != nil || f.Priority != 0 || f.AssigneeID != "" ||
		f.DueString != "" || f.DueDate != "" || f.DueDatetime != "" || f.Duration != 0
}

// body is the Todoist request body of the set values. It runs after check.
func (f taskFields) body() map[string]any {
	body := map[string]any{}
	if f.Content != nil {
		body["content"] = *f.Content
	}
	if f.Description != nil {
		body["description"] = *f.Description
	}
	if f.Labels != nil {
		body["labels"] = append([]string{}, *f.Labels...)
	}
	if f.Priority != 0 {
		body["priority"] = f.Priority
	}
	if f.AssigneeID != "" {
		body["assignee_id"] = f.AssigneeID
	}
	switch {
	case f.DueString != "":
		body["due_string"] = f.DueString
		if f.DueLang != "" {
			body["due_lang"] = f.DueLang
		}
	case f.DueDate != "":
		body["due_date"] = f.DueDate
	case f.DueDatetime != "":
		body["due_datetime"], _, _ = parseInstant("due_datetime", f.DueDatetime)
	}
	if f.Duration != 0 {
		body["duration"], body["duration_unit"] = f.Duration, f.DurationUnit
	}
	return body
}

// validMultiline refuses control characters other than line breaks and tabs, and bounds the length.
func validMultiline(value string, maxRunes int, empty bool) bool {
	if (!empty && strings.TrimSpace(value) == "") || utf8.RuneCountInString(value) > maxRunes {
		return false
	}
	for _, r := range value {
		if (r < 0x20 && r != '\n' && r != '\r' && r != '\t') || r == 0x7f {
			return false
		}
	}
	return true
}

func validLang(value string) bool {
	return len(value) == 2 && value[0] >= 'a' && value[0] <= 'z' && value[1] >= 'a' && value[1] <= 'z'
}

func checkRequiredID(name, value string) error {
	if value == "" || !validID(value) {
		return invalidRequest(name + " is not a Todoist ID")
	}
	return nil
}

func checkIDs(values map[string]string) error {
	for name, value := range values {
		if err := checkID(name, value); err != nil {
			return err
		}
	}
	return nil
}

// changedTask checks the task a change answered with. Its request may have changed something, so every
// failure says so.
func (c *Client) changedTask(op string, raw rawTask) (*Task, error) {
	if raw.ID == "" {
		return nil, invalidChange(op, true)
	}
	if !c.scope.allows(raw.ProjectID) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "Todoist answered with a task outside the connection's projects" + uncertain}
	}
	task := taskOf(raw, true)
	return &task, nil
}

// placement returns the project a task created in or moved to a section or below a parent belongs to. On
// a project connection the section or the parent is read first and must lie in the connection; a project
// that is named as well must be the same. An account connection holds every project, so Todoist alone
// checks the combination.
func (c *Client) placement(ctx context.Context, op, projectID, sectionID, parentID string) (string, error) {
	if c.scope.all {
		return projectID, nil
	}
	switch {
	case parentID != "":
		parent, err := c.readTask(ctx, op, parentID)
		if err != nil {
			return "", err
		}
		if projectID != "" && parent.ProjectID != projectID {
			return "", invalidRequest("parent_id is not a task of project_id")
		}
		return parent.ProjectID, nil
	case sectionID != "":
		section, err := c.readSection(ctx, op, sectionID)
		if err != nil {
			return "", err
		}
		if projectID != "" && section.ProjectID != projectID {
			return "", invalidRequest("section_id is not a section of project_id")
		}
		return section.ProjectID, nil
	}
	return projectID, nil
}

// taskInScope reads the task a change concerns on a project connection and refuses one outside it.
func (c *Client) taskInScope(ctx context.Context, op, id string) error {
	if c.scope.all {
		return nil
	}
	_, err := c.readTask(ctx, op, id)
	return err
}

func prepareTasksCreate(bound scope, arguments struct {
	ProjectID string `json:"project_id"`
	SectionID string `json:"section_id"`
	ParentID  string `json:"parent_id"`
	taskFields
}) (run, error) {
	const op = "create task"
	if arguments.Content == nil {
		return nil, invalidRequest("content is required")
	}
	if err := checkIDs(map[string]string{"project_id": arguments.ProjectID, "section_id": arguments.SectionID,
		"parent_id": arguments.ParentID}); err != nil {
		return nil, err
	}
	if err := arguments.check(); err != nil {
		return nil, err
	}
	if err := bound.selectProject(arguments.ProjectID); err != nil {
		return nil, err
	}
	if arguments.SectionID != "" && arguments.ParentID != "" {
		return nil, invalidRequest("name section_id or parent_id, not both; a subtask stays in the section of its parent")
	}
	project := arguments.ProjectID
	if project == "" && arguments.SectionID == "" && arguments.ParentID == "" && !bound.all {
		single, ok := bound.single()
		if !ok {
			return nil, invalidRequest("name project_id, section_id, or parent_id; a connection with several " +
				"projects has no default project")
		}
		project = single
	}
	body := arguments.body()
	return func(ctx context.Context, c *Client) (any, error) {
		resolved, err := c.placement(ctx, op, project, arguments.SectionID, arguments.ParentID)
		if err != nil {
			return nil, err
		}
		if resolved != "" {
			body["project_id"] = resolved
		}
		if arguments.SectionID != "" {
			body["section_id"] = arguments.SectionID
		}
		if arguments.ParentID != "" {
			body["parent_id"] = arguments.ParentID
		}
		var raw rawTask
		if err := c.change(ctx, op, http.MethodPost, "/tasks", body, &raw); err != nil {
			return nil, err
		}
		return c.changedTask(op, raw)
	}, nil
}

func prepareTasksUpdate(_ scope, arguments struct {
	TaskID        string `json:"task_id"`
	ClearDue      bool   `json:"clear_due"`
	ClearDuration bool   `json:"clear_duration"`
	Unassign      bool   `json:"unassign"`
	taskFields
}) (run, error) {
	const op = "update task"
	if err := checkRequiredID("task_id", arguments.TaskID); err != nil {
		return nil, err
	}
	if err := arguments.check(); err != nil {
		return nil, err
	}
	switch {
	case !arguments.set() && !arguments.ClearDue && !arguments.ClearDuration && !arguments.Unassign:
		return nil, invalidRequest("name at least one value to change")
	case arguments.ClearDue && (arguments.DueString != "" || arguments.DueDate != "" || arguments.DueDatetime != ""):
		return nil, invalidRequest("clear_due cannot be combined with a new due date")
	case arguments.ClearDuration && arguments.Duration != 0:
		return nil, invalidRequest("clear_duration cannot be combined with a new duration")
	case arguments.Unassign && arguments.AssigneeID != "":
		return nil, invalidRequest("unassign cannot be combined with assignee_id")
	}
	body := arguments.body()
	if arguments.ClearDue {
		// Todoist removes a due date when it is set to the text "no date".
		body["due_string"] = "no date"
	}
	if arguments.ClearDuration {
		body["duration"], body["duration_unit"] = nil, nil
	}
	if arguments.Unassign {
		body["assignee_id"] = nil
	}
	return func(ctx context.Context, c *Client) (any, error) {
		if err := c.taskInScope(ctx, op, arguments.TaskID); err != nil {
			return nil, err
		}
		var raw rawTask
		if err := c.change(ctx, op, http.MethodPost, "/tasks/"+url.PathEscape(arguments.TaskID), body, &raw); err != nil {
			return nil, err
		}
		return c.changedTask(op, raw)
	}, nil
}

func prepareTasksMove(bound scope, arguments struct {
	TaskID    string `json:"task_id"`
	ProjectID string `json:"project_id"`
	SectionID string `json:"section_id"`
	ParentID  string `json:"parent_id"`
}) (run, error) {
	const op = "move task"
	if err := checkRequiredID("task_id", arguments.TaskID); err != nil {
		return nil, err
	}
	destinations := map[string]string{"project_id": arguments.ProjectID, "section_id": arguments.SectionID,
		"parent_id": arguments.ParentID}
	if err := checkIDs(destinations); err != nil {
		return nil, err
	}
	body := map[string]any{}
	for name, value := range destinations {
		if value != "" {
			body[name] = value
		}
	}
	if len(body) != 1 {
		return nil, invalidRequest("name exactly one of project_id, section_id, and parent_id")
	}
	if arguments.ParentID == arguments.TaskID {
		return nil, invalidRequest("a task cannot be moved below itself")
	}
	if err := bound.selectProject(arguments.ProjectID); err != nil {
		return nil, err
	}
	return func(ctx context.Context, c *Client) (any, error) {
		if err := c.taskInScope(ctx, op, arguments.TaskID); err != nil {
			return nil, err
		}
		if _, err := c.placement(ctx, op, "", arguments.SectionID, arguments.ParentID); err != nil {
			return nil, err
		}
		var raw rawTask
		if err := c.change(ctx, op, http.MethodPost, "/tasks/"+url.PathEscape(arguments.TaskID)+"/move", body,
			&raw); err != nil {
			return nil, err
		}
		if raw.ID == "" {
			// Todoist confirmed the move without the task; the task is read back to report where it is now.
			var err error
			if raw, err = c.readTask(ctx, op, arguments.TaskID); err != nil {
				return nil, err
			}
		}
		return c.changedTask(op, raw)
	}, nil
}

// prepareTaskState builds the close, reopen, and delete of one task. Each is a request of its own, so a
// close can never reopen a task and a reopen never close one.
func prepareTaskState(op, method, suffix, result string) func(scope, struct {
	TaskID string `json:"task_id"`
}) (run, error) {
	return func(_ scope, arguments struct {
		TaskID string `json:"task_id"`
	}) (run, error) {
		if err := checkRequiredID("task_id", arguments.TaskID); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.taskInScope(ctx, op, arguments.TaskID); err != nil {
				return nil, err
			}
			if err := c.change(ctx, op, method, "/tasks/"+url.PathEscape(arguments.TaskID)+suffix, nil, nil); err != nil {
				return nil, err
			}
			return &Change{ID: arguments.TaskID, Result: result}, nil
		}, nil
	}
}

func prepareCommentsCreate(bound scope, arguments struct {
	TaskID    string `json:"task_id"`
	ProjectID string `json:"project_id"`
	Content   string `json:"content"`
}) (run, error) {
	const op = "create comment"
	if (arguments.TaskID == "") == (arguments.ProjectID == "") {
		return nil, invalidRequest("name exactly one of task_id and project_id")
	}
	if err := checkIDs(map[string]string{"task_id": arguments.TaskID, "project_id": arguments.ProjectID}); err != nil {
		return nil, err
	}
	if err := checkComment(arguments.Content); err != nil {
		return nil, err
	}
	if err := bound.selectProject(arguments.ProjectID); err != nil {
		return nil, err
	}
	body := map[string]any{"content": arguments.Content}
	if arguments.TaskID != "" {
		body["task_id"] = arguments.TaskID
	} else {
		body["project_id"] = arguments.ProjectID
	}
	return func(ctx context.Context, c *Client) (any, error) {
		if arguments.TaskID != "" {
			if err := c.taskInScope(ctx, op, arguments.TaskID); err != nil {
				return nil, err
			}
		}
		var raw rawComment
		if err := c.change(ctx, op, http.MethodPost, "/comments", body, &raw); err != nil {
			return nil, err
		}
		if raw.ID == "" {
			return nil, invalidChange(op, true)
		}
		comment := commentOf(raw)
		return &comment, nil
	}, nil
}

func prepareCommentsUpdate(_ scope, arguments struct {
	CommentID string `json:"comment_id"`
	Content   string `json:"content"`
}) (run, error) {
	const op = "update comment"
	if err := checkRequiredID("comment_id", arguments.CommentID); err != nil {
		return nil, err
	}
	if err := checkComment(arguments.Content); err != nil {
		return nil, err
	}
	return func(ctx context.Context, c *Client) (any, error) {
		if err := c.commentInScope(ctx, op, arguments.CommentID); err != nil {
			return nil, err
		}
		var raw rawComment
		if err := c.change(ctx, op, http.MethodPost, "/comments/"+url.PathEscape(arguments.CommentID),
			map[string]any{"content": arguments.Content}, &raw); err != nil {
			return nil, err
		}
		if raw.ID == "" {
			return nil, invalidChange(op, true)
		}
		comment := commentOf(raw)
		return &comment, nil
	}, nil
}

func prepareCommentsDelete(_ scope, arguments struct {
	CommentID string `json:"comment_id"`
}) (run, error) {
	const op = "delete comment"
	if err := checkRequiredID("comment_id", arguments.CommentID); err != nil {
		return nil, err
	}
	return func(ctx context.Context, c *Client) (any, error) {
		if err := c.commentInScope(ctx, op, arguments.CommentID); err != nil {
			return nil, err
		}
		if err := c.change(ctx, op, http.MethodDelete, "/comments/"+url.PathEscape(arguments.CommentID), nil,
			nil); err != nil {
			return nil, err
		}
		return &Change{ID: arguments.CommentID, Result: "deleted"}, nil
	}, nil
}

func checkComment(content string) error {
	if !validMultiline(content, maxComment, false) {
		return invalidRequest("content must be 1 to 15000 characters without control characters")
	}
	return nil
}

// commentInScope reads the comment a change concerns on a project connection and refuses one of a task or
// project outside it. A task comment is held to the project of its task, which is read as well.
func (c *Client) commentInScope(ctx context.Context, op, id string) error {
	if c.scope.all {
		return nil
	}
	var raw rawComment
	if err := c.get(ctx, op, "/comments/"+url.PathEscape(id), nil, &raw); err != nil {
		return err
	}
	if raw.ID == "" {
		return invalidResponse(op)
	}
	task := raw.TaskID
	if task == "" {
		task = raw.ItemID
	}
	switch {
	case raw.IsDeleted:
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: notFoundMessage}
	case task != "":
		if !validID(task) {
			return invalidResponse(op)
		}
		if _, err := c.readTask(ctx, op, task); err != nil {
			return err
		}
		return nil
	case raw.ProjectID != "" && c.scope.allows(raw.ProjectID):
		return nil
	}
	return outsideScope("comment")
}
