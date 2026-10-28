package todoist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var readRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: dataSensitivity,
}

// Input schemas shared by the tools. The backslashes are doubled because the patterns are embedded in JSON
// schema strings.
const (
	idSchema          = `{"type":"string","minLength":1,"maxLength":64,"pattern":"^[A-Za-z0-9]+$"}`
	positiveSchema    = `{"type":"integer","minimum":1}`
	limitSchema       = `{"type":"integer","minimum":1,"maximum":200}`
	cursorSchema      = `{"type":"string","minLength":1,"maxLength":1024,"pattern":"^[A-Za-z0-9_-]+$"}`
	searchSchema      = `{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\x00-\\x1f\\x7f]+$"}`
	labelSchema       = `{"type":"string","minLength":1,"maxLength":100,"pattern":"^[^\\x00-\\x1f\\x7f]+$"}`
	querySchema       = `{"type":"string","minLength":1,"maxLength":1024,"pattern":"^[^\\x00-\\x1f\\x7f]+$"}`
	languageTagSchema = `{"type":"string","minLength":2,"maxLength":35,"pattern":"^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8}){0,3}$"}`
	idsSchema         = `{"type":"array","minItems":1,"maxItems":100,"uniqueItems":true,"items":` + idSchema + `}`
	dateSchema        = `{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"}`
	instantSchema     = `{"type":"string","minLength":20,"maxLength":35,` +
		`"pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?(Z|[+-][0-9]{2}:[0-9]{2})$"}`
	pageInput = `"limit":` + limitSchema + `,"cursor":` + cursorSchema
)

// Output schemas shared by the tools.
const (
	stringSchema  = `{"type":"string"}`
	booleanSchema = `{"type":"boolean"}`
	integerSchema = `{"type":"integer"}`
	pageOutput    = `"next_cursor":` + stringSchema + `,"has_more":` + booleanSchema
	dueSchema     = `{"type":"object","properties":{"date":` + stringSchema + `,"string":` + stringSchema + `,` +
		`"is_recurring":` + booleanSchema + `,"timezone":` + stringSchema + `,"lang":` + stringSchema + `},` +
		`"required":["date","is_recurring"],"additionalProperties":false}`
	projectAccessSchema = `{"type":"object","properties":{"visibility":` + stringSchema + `,` +
		`"configuration":{"type":"object","properties":{"hide_collaborator_details":` + booleanSchema + `,` +
		`"disable_duplication":` + booleanSchema + `},"additionalProperties":false}},"additionalProperties":false}`
	projectSchema = `{"type":"object","properties":{"id":` + stringSchema + `,"name":` + stringSchema + `,` +
		`"parent_id":` + stringSchema + `,"color":` + stringSchema + `,"is_favorite":` + booleanSchema + `,` +
		`"is_shared":` + booleanSchema + `,"is_inbox":` + booleanSchema + `,"view_style":` + stringSchema + `,` +
		`"description":` + stringSchema + `,"is_archived":` + booleanSchema + `,"created_at":` + stringSchema + `,` +
		`"updated_at":` + stringSchema + `,"can_assign_tasks":` + booleanSchema + `,"can_comment":` + booleanSchema + `,` +
		`"child_order":` + integerSchema + `,"order_key":` + stringSchema + `,"is_collapsed":` + booleanSchema + `,` +
		`"icon":` + stringSchema + `,"creator_uid":` + stringSchema + `,"is_frozen":` + booleanSchema + `,` +
		`"default_order":` + integerSchema + `,"default_order_key":` + stringSchema + `,` +
		`"access":` + projectAccessSchema + `,"role":` + stringSchema + `,"workspace_id":` + stringSchema + `,` +
		`"folder_id":` + stringSchema + `,"is_invite_only":` + booleanSchema + `,"status":` + stringSchema + `,` +
		`"is_link_sharing_enabled":` + booleanSchema + `,"collaborator_role_default":` + stringSchema + `,` +
		`"is_pending_default_collaborator_invites":` + booleanSchema + `,` +
		`"is_project_insights_enabled":` + booleanSchema + `},` +
		`"required":["id","name","is_favorite","is_shared","is_inbox"],"additionalProperties":false}`
	sectionSchema = `{"type":"object","properties":{"id":` + stringSchema + `,"project_id":` + stringSchema + `,` +
		`"name":` + stringSchema + `,"order":` + integerSchema + `,"description":` + stringSchema + `,` +
		`"user_id":` + stringSchema + `,"added_at":` + stringSchema + `,"updated_at":` + stringSchema + `,` +
		`"archived_at":` + stringSchema + `,"is_archived":` + booleanSchema + `,"is_collapsed":` + booleanSchema + `,` +
		`"order_key":` + stringSchema + `},` +
		`"required":["id","project_id","name","order"],"additionalProperties":false}`
	labelOutputSchema = `{"type":"object","properties":{"id":` + stringSchema + `,"name":` + stringSchema + `,` +
		`"color":` + stringSchema + `,"order":` + integerSchema + `,"is_favorite":` + booleanSchema + `},` +
		`"required":["id","name","order","is_favorite"],"additionalProperties":false}`
	taskSchema = `{"type":"object","properties":{"id":` + stringSchema + `,"content":` + stringSchema + `,` +
		`"project_id":` + stringSchema + `,"section_id":` + stringSchema + `,"parent_id":` + stringSchema + `,` +
		`"labels":{"type":"array","items":` + stringSchema + `},"priority":` + integerSchema + `,` +
		`"due":` + dueSchema + `,"deadline":` + stringSchema + `,"deadline_details":{"type":"object",` +
		`"properties":{"date":` + stringSchema + `,"lang":` + stringSchema + `},"required":["date"],` +
		`"additionalProperties":false},"duration":{"type":"object","properties":{"amount":` + integerSchema + `,` +
		`"unit":` + stringSchema + `},"required":["amount","unit"],"additionalProperties":false},` +
		`"description":` + stringSchema + `,"user_id":` + stringSchema + `,"added_by_uid":` + stringSchema + `,` +
		`"assigned_by_uid":` + stringSchema + `,"responsible_uid":` + stringSchema + `,` +
		`"completed_by_uid":` + stringSchema + `,"is_collapsed":` + booleanSchema + `,"checked":` + booleanSchema + `,` +
		`"child_order":` + integerSchema + `,"day_order":` + integerSchema + `,"order_key":` + stringSchema + `,` +
		`"completed_count":` + integerSchema + `,"postponed_count":` + integerSchema + `,` +
		`"added_at":` + stringSchema + `,"updated_at":` + stringSchema + `,"completed_at":` + stringSchema + `},` +
		`"required":["id","content","project_id","labels","priority"],"additionalProperties":false}`
	thumbnailSchema = `{"type":"object","properties":{"url":` + stringSchema + `,"width":` + integerSchema + `,` +
		`"height":` + integerSchema + `},"additionalProperties":false}`
	attachmentSchema = `{"type":"object","properties":{"file_name":` + stringSchema + `,"file_type":` + stringSchema + `,` +
		`"resource_type":` + stringSchema + `,"file_size":` + integerSchema + `,"upload_state":` + stringSchema + `,` +
		`"file_url":` + stringSchema + `,"image":` + stringSchema + `,"image_width":` + integerSchema + `,` +
		`"image_height":` + integerSchema + `,"file_duration":` + integerSchema + `,"tn_s":` + thumbnailSchema + `,` +
		`"tn_m":` + thumbnailSchema + `,"tn_l":` + thumbnailSchema + `},"additionalProperties":false}`
	commentSchema = `{"type":"object","properties":{"id":` + stringSchema + `,"content":` + stringSchema + `,` +
		`"posted_at":` + stringSchema + `,"posted_by":` + stringSchema + `,"attachment":` + attachmentSchema + `,` +
		`"uids_to_notify":{"type":"array","items":` + stringSchema + `},"uids_to_notify_truncated":` + booleanSchema + `,` +
		`"reactions":{"type":"object","additionalProperties":{"type":"array","items":` + stringSchema + `}},` +
		`"reactions_truncated":` + booleanSchema + `},` +
		`"required":["id","content"],"additionalProperties":false}`
	reminderSchema = `{"type":"object","properties":{"id":` + stringSchema + `,"task_id":` + stringSchema + `,` +
		`"type":` + stringSchema + `,"due":` + dueSchema + `,"minute_offset":` + integerSchema + `,` +
		`"is_urgent":` + booleanSchema + `,"notify_uid":` + stringSchema + `},` +
		`"required":["id","task_id","type","is_urgent"],"additionalProperties":false}`
	filterSchema = `{"type":"object","properties":{"id":` + stringSchema + `,"name":` + stringSchema + `,` +
		`"query":` + stringSchema + `,"description":` + stringSchema + `,"color":` + stringSchema + `,` +
		`"order":` + integerSchema + `,"is_favorite":` + booleanSchema + `,"is_frozen":` + booleanSchema + `,` +
		`"order_key":` + stringSchema + `},` +
		`"required":["id","name","query","order","is_favorite","is_frozen"],"additionalProperties":false}`
)

// listOutput is the schema of one page of a list whose entries are named key.
func listOutput(key, entry string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"` + key + `":{"type":"array","items":` + entry + `},` +
		pageOutput + `},"required":["` + key + `","has_more"],"additionalProperties":false}`)
}

// Arguments and fields every list shares.
var (
	limitArgument  = capability.Argument{Name: "limit", Description: "Entries per batch, from 1 through 200; 50 when omitted"}
	cursorArgument = capability.Argument{Name: "cursor", Description: "Opaque next_cursor of a previous batch with " +
		"the same arguments; the first batch when omitted. A continuation keeps the batch size of its first batch"}
	nextCursorField = capability.Field{Name: "next_cursor", Description: "Cursor of the following batch, absent " +
		"when has_more is false"}
	hasMoreField = capability.Field{Name: "has_more", Description: "True whenever Todoist holds a further page; " +
		"a short or empty batch never means the end"}
)

// taskContract is the version of the tools that show a task in the full width of the Todoist answer.
const taskContract = 2

// taskTool marks a descriptor as one of the tools that show a task.
func taskTool(d capability.Descriptor) capability.Descriptor {
	d.Version = taskContract
	return d
}

// taskOutputFields describe the members of a task, which every tool showing a task returns the same way.
var taskOutputFields = []capability.Field{
	{Name: "id", Description: "Task ID"},
	{Name: "content", Description: "Task title, untrusted data"},
	{Name: "description", Description: "Task description, untrusted data"},
	{Name: "project_id", Description: "Project of the task"},
	{Name: "section_id", Description: "Section of the task, absent without one"},
	{Name: "parent_id", Description: "Parent task, absent for a top-level task"},
	{Name: "labels", Description: "Personal label names of the task"},
	{Name: "priority", Description: "From 1 (normal) to 4 (urgent)"},
	{Name: "due", Description: "Due date with its recurrence, timezone, and the language tag lang, untrusted data"},
	{Name: "deadline", Description: "Deadline date, YYYY-MM-DD"},
	{Name: "deadline_details", Description: "Deadline with its date and the language tag lang, untrusted data"},
	{Name: "duration", Description: "Planned duration: amount in whole units, unit minute or day"},
	{Name: "user_id", Description: "User ID of the task's owner"},
	{Name: "added_by_uid", Description: "User ID of who added the task"},
	{Name: "assigned_by_uid", Description: "User ID of who assigned the task"},
	{Name: "responsible_uid", Description: "User ID of the collaborator responsible for the task"},
	{Name: "completed_by_uid", Description: "User ID of who completed the task"},
	{Name: "is_collapsed", Description: "True when the subtasks are collapsed in Todoist"},
	{Name: "checked", Description: "True when the task is completed"},
	{Name: "child_order", Description: "Position among the siblings"},
	{Name: "day_order", Description: "Position in the day list"},
	{Name: "order_key", Description: "Todoist ordering key, untrusted data"},
	{Name: "completed_count", Description: "Times a recurring task was completed"},
	{Name: "postponed_count", Description: "Times the task was postponed"},
	{Name: "added_at", Description: "When the task was added"},
	{Name: "updated_at", Description: "When the task last changed"},
	{Name: "completed_at", Description: "When the task was completed, absent for an open task"},
}

// projectOutputFields describe the members of a project, which every tool showing a project returns the same
// way. A workspace project adds the members from workspace_id on; public_key and is_deleted are never shown.
var projectOutputFields = []capability.Field{
	{Name: "id", Description: "Project ID"},
	{Name: "name", Description: "Project name, untrusted data"},
	{Name: "parent_id", Description: "Parent project, absent for a top-level or workspace project"},
	{Name: "color", Description: "Color name"},
	{Name: "is_favorite", Description: "True when the project is a favorite"},
	{Name: "is_shared", Description: "True when the project is shared with other users"},
	{Name: "is_inbox", Description: "True for the Inbox project"},
	{Name: "view_style", Description: "View style such as list or board"},
	{Name: "description", Description: "Project description, untrusted data"},
	{Name: "is_archived", Description: "True when the project is archived"},
	{Name: "created_at", Description: "When the project was created"},
	{Name: "updated_at", Description: "When the project last changed"},
	{Name: "can_assign_tasks", Description: "True when the user can assign tasks in the project"},
	{Name: "can_comment", Description: "True when the user can comment in the project"},
	{Name: "child_order", Description: "Position among the sibling projects"},
	{Name: "order_key", Description: "Todoist ordering key among the siblings, untrusted data"},
	{Name: "is_collapsed", Description: "True when the project is collapsed in the user's view"},
	{Name: "icon", Description: "Icon name, absent for the default icon, untrusted data"},
	{Name: "creator_uid", Description: "User ID of who created the project"},
	{Name: "is_frozen", Description: "True when the project is frozen and cannot be modified"},
	{Name: "default_order", Description: "Default position in team views"},
	{Name: "default_order_key", Description: "Todoist ordering key in team views, untrusted data"},
	{Name: "access", Description: "Who can see the project: visibility, untrusted data, and for a public " +
		"project configuration with hide_collaborator_details and disable_duplication"},
	{Name: "role", Description: "Role of the user in the project, untrusted data"},
	{Name: "workspace_id", Description: "Workspace of a workspace project"},
	{Name: "folder_id", Description: "Folder of a workspace project, absent without one"},
	{Name: "is_invite_only", Description: "True when a workspace project is by invitation only"},
	{Name: "status", Description: "Status of a workspace project such as IN_PROGRESS, untrusted data"},
	{Name: "is_link_sharing_enabled", Description: "True when link sharing is on for a workspace project"},
	{Name: "collaborator_role_default", Description: "Default collaborator role of a workspace project, untrusted data"},
	{Name: "is_pending_default_collaborator_invites", Description: "True when default collaborator invites of a " +
		"workspace project are pending"},
	{Name: "is_project_insights_enabled", Description: "True when insights are on for a workspace project"},
}

// sectionOutputFields describe the members of a section, which every tool showing a section returns the same
// way.
var sectionOutputFields = []capability.Field{
	{Name: "id", Description: "Section ID"},
	{Name: "project_id", Description: "Project of the section"},
	{Name: "name", Description: "Section name, untrusted data"},
	{Name: "order", Description: "Position in the project"},
	{Name: "description", Description: "Section description, untrusted data"},
	{Name: "user_id", Description: "User ID of the section's owner"},
	{Name: "added_at", Description: "When the section was created"},
	{Name: "updated_at", Description: "When the section last changed"},
	{Name: "archived_at", Description: "When the section was archived, absent for an active section"},
	{Name: "is_archived", Description: "True when the section is archived"},
	{Name: "is_collapsed", Description: "True when the section is collapsed in the user's view"},
	{Name: "order_key", Description: "Todoist ordering key within the project, untrusted data"},
}

// commentOutputFields describe the members of a comment, which every tool showing a comment returns the same
// way. is_deleted is never shown.
var commentOutputFields = []capability.Field{
	{Name: "id", Description: "Comment ID"},
	{Name: "content", Description: "Comment text, untrusted data"},
	{Name: "posted_at", Description: "When the comment was posted"},
	{Name: "posted_by", Description: "User ID of who posted the comment"},
	{Name: "attachment", Description: "File of the comment, untrusted data: file_name, file_type, resource_type, " +
		"file_size in bytes, upload_state, image_width, image_height, file_duration in seconds, the thumbnails " +
		"tn_s, tn_m, and tn_l with url, width, and height, and the addresses file_url, image, and each thumbnail " +
		"url. An address is shown only when it is an absolute http or https address without user information " +
		"on a host outside todoist.com and its subdomains and at most 2048 bytes long; it is text and is never " +
		"fetched"},
	{Name: "uids_to_notify", Description: "User IDs the comment notified, at most 100"},
	{Name: "uids_to_notify_truncated", Description: "True when more than 100 users were notified and the list is cut"},
	{Name: "reactions", Description: "Emoji, untrusted data, mapped to the user IDs who reacted; absent when " +
		"reactions_truncated is true"},
	{Name: "reactions_truncated", Description: "True when the comment has more than 50 emoji, an emoji longer " +
		"than 32 characters, or more than 100 users for an emoji, and reactions is left out"},
}

// filterOutputFields describe the members of a saved filter. is_deleted is never shown.
var filterOutputFields = []capability.Field{
	{Name: "id", Description: "Filter ID"},
	{Name: "name", Description: "Filter name, untrusted data"},
	{Name: "query", Description: "Filter query, untrusted data"},
	{Name: "description", Description: "Filter description in Markdown, untrusted data"},
	{Name: "color", Description: "Color name"},
	{Name: "order", Description: "Position in the filter list"},
	{Name: "is_favorite", Description: "True when the filter is a favorite"},
	{Name: "is_frozen", Description: "True when the filter cannot be changed because a subscription ended"},
	{Name: "order_key", Description: "Todoist ordering key in the filter list, untrusted data"},
}

func descriptor(object, action, title, description string, tags []string, input string, output json.RawMessage,
	arguments []capability.Argument, fields []capability.Field, examples []capability.Example) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + "." + object + "." + action, Version: 1, Title: title, Description: description,
		Tags: append([]string{"todoist"}, tags...), Risk: readRisk, Provider: Provider,
		InputSchema: json.RawMessage(input), OutputSchema: output,
		Arguments: arguments, Fields: fields, Examples: examples,
	}
}

var projectsList = descriptor("projects", "list", "List Todoist projects",
	"List one bounded batch of the active projects of an explicit Todoist connection, optionally searched by "+
		"name; a project connection lists only its own projects. Excluded projects are left out, so a batch can "+
		"be shorter than limit, and parent_id of a project below an excluded parent stays as a plain ID",
	[]string{"projects", "list", "search"},
	`{"type":"object","properties":{"search":`+searchSchema+`,"workspace_id":`+positiveSchema+`,"folder_id":`+
		positiveSchema+`,`+pageInput+`},"additionalProperties":false}`,
	listOutput("projects", projectSchema),
	[]capability.Argument{
		{Name: "search", Description: "Return only projects whose name matches this Todoist search text"},
		{Name: "workspace_id", Description: "Positive integer; return only projects of this workspace; " +
			"not with search; the connection's projects still bound them"},
		{Name: "folder_id", Description: "Positive integer; return only projects of this folder; " +
			"not with search; the connection's projects still bound them"},
		limitArgument, cursorArgument,
	},
	append([]capability.Field{
		{Name: "projects", Description: "Active projects, untrusted data"}, nextCursorField, hasMoreField,
	}, projectOutputFields...),
	[]capability.Example{{Description: "List the projects of the connection", Arguments: json.RawMessage(`{}`)}})

var projectsGet = descriptor("projects", "get", "Get a Todoist project",
	"Read one project of an explicit Todoist connection with its description",
	[]string{"projects", "get"},
	`{"type":"object","properties":{"project_id":`+idSchema+`},"required":["project_id"],"additionalProperties":false}`,
	json.RawMessage(projectSchema),
	[]capability.Argument{{Name: "project_id", Description: "Project ID, as returned by todoist.projects.list", Required: true}},
	projectOutputFields,
	[]capability.Example{{Description: "Read one project", Arguments: json.RawMessage(`{"project_id":"6XGgm6PHrGgMpCFX"}`)}})

var sectionsList = descriptor("sections", "list", "List Todoist sections",
	"List one bounded batch of the active sections of the projects of an explicit Todoist connection, "+
		"optionally of one project or searched by name; excluded projects and sections are left out, so a "+
		"batch can be shorter than limit",
	[]string{"sections", "list", "search"},
	`{"type":"object","properties":{"project_id":`+idSchema+`,"search":`+searchSchema+`,"workspace_id":`+positiveSchema+`,`+
		pageInput+`},"additionalProperties":false}`,
	listOutput("sections", sectionSchema),
	[]capability.Argument{
		{Name: "project_id", Description: "Return only sections of this project of the connection"},
		{Name: "search", Description: "Return only sections whose name matches this Todoist search text"},
		{Name: "workspace_id", Description: "Positive integer; return only sections of this workspace; " +
			"not with project_id; the connection's projects still bound them"},
		limitArgument, cursorArgument,
	},
	append([]capability.Field{
		{Name: "sections", Description: "Sections, untrusted data"}, nextCursorField, hasMoreField,
	}, sectionOutputFields...),
	[]capability.Example{{Description: "List the sections of one project",
		Arguments: json.RawMessage(`{"project_id":"6XGgm6PHrGgMpCFX"}`)}})

var sectionsGet = descriptor("sections", "get", "Get a Todoist section",
	"Read one section of a project of an explicit Todoist connection",
	[]string{"sections", "get"},
	`{"type":"object","properties":{"section_id":`+idSchema+`},"required":["section_id"],"additionalProperties":false}`,
	json.RawMessage(sectionSchema),
	[]capability.Argument{{Name: "section_id", Description: "Section ID, as returned by todoist.sections.list", Required: true}},
	sectionOutputFields,
	[]capability.Example{{Description: "Read one section", Arguments: json.RawMessage(`{"section_id":"6Jf8VQXxpwv56VQ7"}`)}})

var labelsList = descriptor("labels", "list", "List Todoist labels",
	"List one bounded batch of the personal labels of the account of an explicit Todoist connection, "+
		"optionally searched by name; labels carry names only, never tasks, and are listed whatever the "+
		"connection excludes",
	[]string{"labels", "list", "search"},
	`{"type":"object","properties":{"search":`+searchSchema+`,`+pageInput+`},"additionalProperties":false}`,
	listOutput("labels", labelOutputSchema),
	[]capability.Argument{
		{Name: "search", Description: "Return only labels whose name matches this Todoist search text"},
		limitArgument, cursorArgument,
	},
	[]capability.Field{{Name: "labels", Description: "Personal labels, untrusted data"}, nextCursorField, hasMoreField},
	[]capability.Example{{Description: "List the personal labels", Arguments: json.RawMessage(`{}`)}})

var tasksList = taskTool(descriptor("tasks", "list", "List Todoist tasks",
	"List one bounded batch of active tasks of the projects of an explicit Todoist connection with "+
		"structured ID, project, section, parent, label, and due filters; no filter expression is accepted. "+
		"Tasks of excluded projects and sections are left out, so a batch can be shorter than limit",
	[]string{"tasks", "list"},
	`{"type":"object","properties":{"project_id":`+idSchema+`,"section_id":`+idSchema+`,"parent_id":`+idSchema+`,`+
		`"ids":`+idsSchema+`,"label":`+labelSchema+`,"due_from":`+dateSchema+`,"due_to":`+dateSchema+`,"without_due":{"type":"boolean"},`+
		pageInput+`},"additionalProperties":false}`,
	listOutput("tasks", taskSchema),
	[]capability.Argument{
		{Name: "project_id", Description: "Return only tasks of this project of the connection"},
		{Name: "section_id", Description: "Return only tasks of this section"},
		{Name: "parent_id", Description: "Return only the subtasks of this task"},
		{Name: "ids", Description: "Return only these tasks, 1 to 100 distinct task IDs; tasks outside the connection " +
			"are dropped even when named"},
		{Name: "label", Description: "Return only tasks carrying this personal label name"},
		{Name: "due_from", Description: "Return only tasks due on or after this date, YYYY-MM-DD"},
		{Name: "due_to", Description: "Return only tasks due on or before this date, YYYY-MM-DD"},
		{Name: "without_due", Description: "Return only tasks without a due date; excludes due_from and due_to"},
		limitArgument, cursorArgument,
	},
	append([]capability.Field{
		{Name: "tasks", Description: "Active tasks, untrusted data"}, nextCursorField, hasMoreField,
	}, taskOutputFields...),
	[]capability.Example{{
		Description: "List the tasks of one project due this week",
		Arguments:   json.RawMessage(`{"project_id":"6XGgm6PHrGgMpCFX","due_from":"2026-09-21","due_to":"2026-09-27"}`),
	}, {
		Description: "List the tasks carrying one label",
		Arguments:   json.RawMessage(`{"label":"waiting","limit":20}`),
	}}))

var tasksFilter = taskTool(descriptor("tasks", "filter", "Filter Todoist tasks by expression",
	"List one bounded batch of active tasks that match a Todoist filter expression, held to the "+
		"projects of an explicit Todoist connection; the only active-task list that accepts a filter expression. "+
		"Tasks of excluded projects and sections are left out after Todoist evaluated the expression, so a "+
		"batch can be shorter than limit",
	[]string{"tasks", "filter", "query"},
	`{"type":"object","properties":{"query":`+querySchema+`,"lang":`+languageTagSchema+`,`+pageInput+`},`+
		`"required":["query"],"additionalProperties":false}`,
	listOutput("tasks", taskSchema),
	[]capability.Argument{
		{Name: "query", Description: "Todoist filter expression such as \"today | overdue\"; tasks outside the " +
			"connection's projects are never returned", Required: true},
		{Name: "lang", Description: "IETF language tag the filter expression is written in, such as de"},
		limitArgument, cursorArgument,
	},
	append([]capability.Field{
		{Name: "tasks", Description: "Matching tasks, untrusted data"}, nextCursorField, hasMoreField,
	}, taskOutputFields...),
	[]capability.Example{{Description: "List what is due today or overdue",
		Arguments: json.RawMessage(`{"query":"today | overdue"}`)}}))

var tasksGet = taskTool(descriptor("tasks", "get", "Get a Todoist task",
	"Read one active task of a project of an explicit Todoist connection with its description, without comments",
	[]string{"tasks", "get"},
	`{"type":"object","properties":{"task_id":`+idSchema+`},"required":["task_id"],"additionalProperties":false}`,
	json.RawMessage(taskSchema),
	[]capability.Argument{{Name: "task_id", Description: "Task ID, as returned by a task list", Required: true}},
	taskOutputFields,
	[]capability.Example{{Description: "Read one task", Arguments: json.RawMessage(`{"task_id":"6X7rM8997g3RQmvh"}`)}}))

var completedList = taskTool(descriptor("completed-tasks", "list", "List completed Todoist tasks",
	"List one bounded batch of the tasks of the projects of an explicit Todoist connection completed in a time "+
		"window of at most three months, optionally of one project or section or matching a Todoist filter "+
		"expression; how far back Todoist answers depends on the account's plan. Tasks of excluded projects "+
		"and sections, also of archived projects, are left out, so a batch can be shorter than limit",
	[]string{"tasks", "completed", "list", "history", "filter"},
	`{"type":"object","properties":{"since":`+instantSchema+`,"until":`+instantSchema+`,"project_id":`+idSchema+`,`+
		`"section_id":`+idSchema+`,"parent_id":`+idSchema+`,"workspace_id":`+positiveSchema+`,`+
		`"query":`+querySchema+`,"filter_lang":`+languageTagSchema+`,`+pageInput+`},"required":["since","until"],`+
		`"additionalProperties":false}`,
	listOutput("tasks", taskSchema),
	[]capability.Argument{
		{Name: "since", Description: "Start of the completion window, inclusive, RFC 3339 such as 2026-09-01T00:00:00Z",
			Required: true},
		{Name: "until", Description: "End of the completion window, exclusive, RFC 3339; at most three months after since",
			Required: true},
		{Name: "project_id", Description: "Return only tasks of this project of the connection"},
		{Name: "section_id", Description: "Return only tasks of this section"},
		{Name: "parent_id", Description: "Return only the subtasks of this task"},
		{Name: "workspace_id", Description: "Return only tasks of this workspace; the connection's projects still bound them"},
		{Name: "query", Description: "Todoist filter expression such as \"@waiting\"; tasks outside the " +
			"connection's projects are never returned"},
		{Name: "filter_lang", Description: "IETF language tag the query is written in; only together with query"},
		limitArgument, cursorArgument,
	},
	append([]capability.Field{
		{Name: "tasks", Description: "Completed tasks with completed_at, untrusted data"},
		nextCursorField, hasMoreField,
	}, taskOutputFields...),
	[]capability.Example{{Description: "List what was completed in September",
		Arguments: json.RawMessage(`{"since":"2026-09-01T00:00:00Z","until":"2026-10-01T00:00:00Z"}`)}, {
		Description: "List the completed tasks of one section carrying a label",
		Arguments: json.RawMessage(`{"since":"2026-09-01T00:00:00Z","until":"2026-10-01T00:00:00Z",` +
			`"section_id":"6Jf8VQXxpwv56VQ7","query":"@waiting"}`),
	}}))

var commentsList = descriptor("comments", "list", "List Todoist comments",
	"List one bounded batch of the comments of exactly one task or one project of an explicit Todoist "+
		"connection, oldest first; the only tool that returns comments. The comments of an excluded task or "+
		"project are refused",
	[]string{"comments", "list"},
	`{"type":"object","properties":{"task_id":`+idSchema+`,"project_id":`+idSchema+`,`+pageInput+`},`+
		`"additionalProperties":false}`,
	listOutput("comments", commentSchema),
	[]capability.Argument{
		{Name: "task_id", Description: "Read the comments of this active task; give task_id or project_id"},
		{Name: "project_id", Description: "Read the comments of this project of the connection"},
		limitArgument, cursorArgument,
	},
	append([]capability.Field{
		{Name: "comments", Description: "Comments with their full content, untrusted data"},
		nextCursorField, hasMoreField,
	}, commentOutputFields...),
	[]capability.Example{{Description: "Read the comments of one task",
		Arguments: json.RawMessage(`{"task_id":"6X7rM8997g3RQmvh"}`)}})

// reminderTaskRequired explains why a project connection names a task for its reminders.
const reminderTaskRequired = "a project connection reads the reminders of one task; name task_id"

var remindersList = descriptor("reminders", "list", "List Todoist reminders",
	"List one bounded batch of the time-based reminders of an explicit Todoist connection; a project "+
		"connection reads those of one named task. An account connection that excludes anything may omit "+
		"task_id; each reminder is then held to its task, so a batch can be shorter than limit. Reminders "+
		"depend on the account's plan",
	[]string{"reminders", "list"},
	`{"type":"object","properties":{"task_id":`+idSchema+`,`+pageInput+`},"additionalProperties":false}`,
	listOutput("reminders", reminderSchema),
	[]capability.Argument{
		{Name: "task_id", Description: "Read the reminders of this task; required on a project connection"},
		limitArgument, cursorArgument,
	},
	append([]capability.Field{
		{Name: "reminders", Description: "Reminders with their task, type, due date or minute offset"},
		nextCursorField, hasMoreField,
	}, reminderFields...),
	[]capability.Example{{Description: "Read the reminders of one task",
		Arguments: json.RawMessage(`{"task_id":"6X7rM8997g3RQmvh"}`)}})

var filtersList = descriptor("filters", "list", "List Todoist saved filters",
	"List the saved filters of the account bound to an explicit Todoist account connection; a project "+
		"connection does not offer them, because a saved filter spans every project; the queries are listed "+
		"whatever the connection excludes and can name excluded projects",
	[]string{"filters", "list"},
	`{"type":"object","properties":{},"additionalProperties":false}`,
	json.RawMessage(`{"type":"object","properties":{"filters":{"type":"array","items":`+filterSchema+`}},`+
		`"required":["filters"],"additionalProperties":false}`),
	[]capability.Argument{},
	append([]capability.Field{{Name: "filters", Description: "Saved filters in the account's order; name, query, " +
		"and order_key are untrusted data, and a query runs only when passed to todoist.tasks.filter"}},
		filterOutputFields...),
	[]capability.Example{{Description: "List the saved filters", Arguments: json.RawMessage(`{}`)}})

// projectTools are the reads a project connection offers. The saved filters join them only on an account
// connection.
var projectTools = []string{projectsList.ID, projectsGet.ID, sectionsList.ID, sectionsGet.ID, labelsList.ID,
	tasksList.ID, tasksFilter.ID, tasksGet.ID, completedList.ID, commentsList.ID, remindersList.ID, remindersGet.ID}

// Register adds Todoist metadata, its read-only connection test, the read operations, and the confirmed
// task, comment, reminder, and structure changes. A new connection starts with reads only.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Todoist", DefaultBaseURL: apiRoot,
		Description:    "Personal task manager and to-do list service",
		RenamedTools:   renamedTools,
		DefaultEffects: []config.Effect{config.EffectRead},
		SecretRoles: []config.SecretRole{{
			Name: roleToken,
			Description: "Todoist personal API token from Settings, Integrations, Developer; it reaches the whole " +
				"account, so the connection target decides which projects Qatlas exposes",
		}},
		Target: config.TargetMetadata{
			Label:    "project",
			Required: true,
			Multiple: true,
			Wildcard: wildcard,
			WildcardWarning: "Every project, saved filter, label, and reminder of this Todoist account is exposed " +
				"to the agent, and a change permission reaches account-wide projects and labels. Use project IDs " +
				"when the whole account is not required.",
			Description: "one or more entries as an allow-list, each a project ID for exactly that project, " +
				"project/PROJECT_ID for a project with all its subprojects, or section/SECTION_ID for one " +
				"section; or * for the whole account",
			Kinds:    targetKinds,
			Reaches:  reaches,
			Validate: validateTarget,
			ValidateSet: func(values []string) error {
				_, err := parseScope(values)
				return err
			},
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read projects", Recommended: true,
			Description: "reads projects, sections, labels, tasks, completed tasks, comments, and reminders " +
				"of the connection's projects and changes nothing",
			Tools: projectTools,
		}, {
			ID: "account-read", Title: "Read the account",
			Description: "reads everything the project profile reads plus the saved filters; for a * account " +
				"connection, and changes nothing",
			Tools: append(append([]string{}, projectTools...), filtersList.ID),
		}, {
			ID: "tasks", Title: "Manage tasks",
			Description: "reads what the read profile reads and creates, updates, moves, closes, and reopens " +
				"tasks, creates and updates comments and reminders of the connection's projects; every change " +
				"needs its own confirmation, and the deletes stay unticked",
			Tools: append(append(append([]string{}, projectTools...), changeTools...), reminderTools...),
		}, {
			ID: "organize", Title: "Organize the account",
			Description: "reads what the account-read profile reads, makes the changes of the tasks profile, and " +
				"creates, updates, reorders, and moves projects, sections, and labels; for a * account " +
				"connection. Every change needs its own confirmation; archiving, unarchiving, and the deletes " +
				"stay unticked",
			Tools: append(append(append(append(append([]string{}, projectTools...), filtersList.ID), changeTools...),
				reminderTools...), structureTools...),
		}},
	}, TestConnection); err != nil {
		return err
	}
	if err := reg.RegisterTargetBrowser(Provider, capability.TargetBrowser{List: browseList,
		ResolveURL: browseResolveURL}); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: projectsList, Handler: handle("list projects", prepareProjectsList)},
		capability.Operation{Descriptor: projectsGet, Handler: handle("get project", prepareProjectsGet)},
		capability.Operation{Descriptor: sectionsList, Handler: handle("list sections", prepareSectionsList)},
		capability.Operation{Descriptor: sectionsGet, Handler: handle("get section", prepareSectionsGet)},
		capability.Operation{Descriptor: labelsList, Handler: handle("list labels", prepareLabelsList)},
		capability.Operation{Descriptor: tasksList, Handler: handle("list tasks", prepareTasksList)},
		capability.Operation{Descriptor: tasksFilter, Handler: handle("filter tasks", prepareTasksFilter)},
		capability.Operation{Descriptor: tasksGet, Handler: handle("get task", prepareTasksGet)},
		capability.Operation{Descriptor: completedList, Handler: handle("list completed tasks", prepareCompletedList)},
		capability.Operation{Descriptor: commentsList, Handler: handle("list comments", prepareCommentsList)},
		capability.Operation{Descriptor: remindersList, Handler: handle("list reminders", prepareRemindersList)},
		capability.Operation{Descriptor: filtersList, Handler: accountOnly(filtersList.ID, invokeFiltersList)},
		capability.Operation{Descriptor: remindersGet, Handler: handle("get reminder", prepareRemindersGet)},
		capability.Operation{Descriptor: tasksCreate, Handler: handle("create task", prepareTasksCreate)},
		capability.Operation{Descriptor: tasksUpdate, Handler: handle("update task", prepareTasksUpdate)},
		capability.Operation{Descriptor: tasksMove, Handler: handle("move task", prepareTasksMove)},
		capability.Operation{Descriptor: tasksClose,
			Handler: handle("close task", prepareTaskState("close task", http.MethodPost, "/close", "closed"))},
		capability.Operation{Descriptor: tasksReopen,
			Handler: handle("reopen task", prepareTaskState("reopen task", http.MethodPost, "/reopen", "reopened"))},
		capability.Operation{Descriptor: tasksDelete,
			Handler: handle("delete task", prepareTaskState("delete task", http.MethodDelete, "", "deleted"))},
		capability.Operation{Descriptor: commentsCreate, Handler: handle("create comment", prepareCommentsCreate)},
		capability.Operation{Descriptor: commentsUpdate, Handler: handle("update comment", prepareCommentsUpdate)},
		capability.Operation{Descriptor: commentsDelete, Handler: handle("delete comment", prepareCommentsDelete)},
		capability.Operation{Descriptor: remindersCreate, Handler: handle("create reminder", prepareRemindersCreate)},
		capability.Operation{Descriptor: remindersUpdate, Handler: handle("update reminder", prepareRemindersUpdate)},
		capability.Operation{Descriptor: remindersDelete, Handler: handle("delete reminder", prepareRemindersDelete)},
		capability.Operation{Descriptor: projectsCreate,
			Handler: reachedBy(projectsCreate.ID, func(bound scope) bool { return bound.all || len(bound.trees) > 0 },
				handle("create project", prepareProjectsCreate))},
		capability.Operation{Descriptor: projectsUpdate, Handler: handle("update project", prepareProjectsUpdate)},
		capability.Operation{Descriptor: projectsArchive, Handler: handle("archive project",
			prepareProjectState("archive project", http.MethodPost, "/archive", "archived", true, false))},
		capability.Operation{Descriptor: projectsUnarchive, Handler: handle("unarchive project",
			prepareProjectState("unarchive project", http.MethodPost, "/unarchive", "unarchived", false, false))},
		capability.Operation{Descriptor: projectsDelete, Handler: handle("delete project",
			prepareProjectState("delete project", http.MethodDelete, "", "deleted", true, true))},
		capability.Operation{Descriptor: sectionsCreate, Handler: handle("create section", prepareSectionsCreate)},
		capability.Operation{Descriptor: sectionsUpdate, Handler: handle("update section", prepareSectionsUpdate)},
		capability.Operation{Descriptor: sectionsReorder, Handler: handle("reorder section", prepareSectionsReorder)},
		capability.Operation{Descriptor: sectionsMove, Handler: handle("move section", prepareSectionsMove)},
		capability.Operation{Descriptor: sectionsDelete, Handler: handle("delete section", prepareSectionsDelete)},
		capability.Operation{Descriptor: labelsCreate,
			Handler: accountOnly(labelsCreate.ID, handle("create label", prepareLabelsCreate))},
		capability.Operation{Descriptor: labelsUpdate,
			Handler: accountOnly(labelsUpdate.ID, handle("update label", prepareLabelsUpdate))},
		capability.Operation{Descriptor: labelsReorder,
			Handler: accountOnly(labelsReorder.ID, handle("reorder label", prepareLabelsReorder))},
		capability.Operation{Descriptor: labelsDelete,
			Handler: accountOnly(labelsDelete.ID, handle("delete label", prepareLabelsDelete))},
	)
}

// run is the provider part of one request, after every argument passed the checks of its prepare step.
type run func(context.Context, *Client) (any, error)

// handle turns a prepare step into a handler. The arguments and the scope are checked before a credential
// is resolved, so a refused request never becomes a provider call.
func handle[T any](op string, prepare func(scope, T) (run, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments T
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, providerError(op, "the validated arguments could not be read")
		}
		if resolved == nil {
			return nil, providerError("open", "no connection was selected")
		}
		bound, err := scopeOf(resolved)
		if err != nil {
			return nil, providerError("open", err.Error())
		}
		execute, err := prepare(bound, arguments)
		if err != nil {
			return nil, err
		}
		client, err := Open(ctx, resolved, secrets, red)
		if err != nil {
			return nil, err
		}
		return execute(ctx, client)
	}
}

// page is the paging part every list argument set shares.
type page struct {
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`
}

func checkID(name, value string) error {
	if value != "" && !validID(value) {
		return invalidRequest(name + " is not a Todoist ID")
	}
	return nil
}

// checkTaskIDs bounds the ids filter: 1 to 100 distinct Todoist IDs, or none.
func checkTaskIDs(ids []string) error {
	if ids == nil {
		return nil
	}
	if len(ids) < 1 || len(ids) > 100 {
		return invalidRequest("ids must name 1 to 100 task IDs")
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || !validID(id) {
			return invalidRequest("ids contains a value that is not a Todoist ID")
		}
		if seen[id] {
			return invalidRequest("ids must not repeat a task ID")
		}
		seen[id] = true
	}
	return nil
}

var langPattern = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8}){0,3}$`)

// checkLang accepts an IETF language tag of at most 35 characters, or none.
func checkLang(name, value string) error {
	if value != "" && (len(value) > 35 || !langPattern.MatchString(value)) {
		return invalidRequest(name + " must be an IETF language tag such as de or en-US")
	}
	return nil
}

func checkSearch(value string) error {
	if value != "" && !validText(value, 200) {
		return invalidRequest("search must be 1 to 200 characters without control characters or padding")
	}
	return nil
}

// searchable returns the list route, or its search route with the search text as query.
func searchable(path, search string) (string, url.Values) {
	if search == "" {
		return path, url.Values{}
	}
	return path + "/search", url.Values{"query": {search}}
}

// positiveFilter validates an optional positive integer filter and sets it on the query.
func positiveFilter(name string, value *int64, query url.Values) (string, error) {
	if value == nil {
		return "", nil
	}
	if *value < 1 {
		return "", invalidRequest(name + " must be a positive integer")
	}
	text := strconv.FormatInt(*value, 10)
	query.Set(name, text)
	return text, nil
}

func prepareProjectsList(bound scope, arguments struct {
	Search      string `json:"search"`
	WorkspaceID *int64 `json:"workspace_id"`
	FolderID    *int64 `json:"folder_id"`
	page
}) (run, error) {
	if err := checkSearch(arguments.Search); err != nil {
		return nil, err
	}
	if arguments.Search != "" && (arguments.WorkspaceID != nil || arguments.FolderID != nil) {
		return nil, invalidRequest("workspace_id and folder_id cannot be combined with search")
	}
	if err := bound.requireProjectLevel(); err != nil {
		return nil, err
	}
	path, query := searchable("/projects", arguments.Search)
	workspace, err := positiveFilter("workspace_id", arguments.WorkspaceID, query)
	if err != nil {
		return nil, err
	}
	folder, err := positiveFilter("folder_id", arguments.FolderID, query)
	if err != nil {
		return nil, err
	}
	call, err := newListCall("list projects", path, query, arguments.Limit, arguments.Cursor,
		projectsList.ID, bound.String(), arguments.Search, workspace, folder)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, c *Client) (any, error) { return c.listProjects(ctx, call) }, nil
}

func prepareProjectsGet(bound scope, arguments struct {
	ProjectID string `json:"project_id"`
}) (run, error) {
	if err := checkID("project_id", arguments.ProjectID); err != nil {
		return nil, err
	}
	if arguments.ProjectID == "" {
		return nil, invalidRequest("project_id is required")
	}
	if err := bound.selectProject(arguments.ProjectID); err != nil {
		return nil, err
	}
	return func(ctx context.Context, c *Client) (any, error) { return c.getProject(ctx, arguments.ProjectID) }, nil
}

func prepareSectionsList(bound scope, arguments struct {
	ProjectID   string `json:"project_id"`
	Search      string `json:"search"`
	WorkspaceID *int64 `json:"workspace_id"`
	page
}) (run, error) {
	if err := checkID("project_id", arguments.ProjectID); err != nil {
		return nil, err
	}
	if err := checkSearch(arguments.Search); err != nil {
		return nil, err
	}
	if err := bound.requireProjectLevel(); err != nil {
		return nil, err
	}
	if err := bound.selectProject(arguments.ProjectID); err != nil {
		return nil, err
	}
	project := arguments.ProjectID
	if single, ok := bound.single(); ok && project == "" {
		project = single
	}
	if project != "" && arguments.WorkspaceID != nil {
		return nil, invalidRequest("workspace_id cannot be combined with project_id or a project connection")
	}
	path, query := searchable("/sections", arguments.Search)
	if project != "" {
		query.Set("project_id", project)
	}
	workspace, err := positiveFilter("workspace_id", arguments.WorkspaceID, query)
	if err != nil {
		return nil, err
	}
	call, err := newListCall("list sections", path, query, arguments.Limit, arguments.Cursor,
		sectionsList.ID, bound.String(), project, arguments.Search, workspace)
	if err != nil {
		return nil, err
	}
	call.project = project
	return func(ctx context.Context, c *Client) (any, error) { return c.listSections(ctx, call, project) }, nil
}

func prepareSectionsGet(_ scope, arguments struct {
	SectionID string `json:"section_id"`
}) (run, error) {
	if arguments.SectionID == "" || !validID(arguments.SectionID) {
		return nil, invalidRequest("section_id is not a Todoist ID")
	}
	return func(ctx context.Context, c *Client) (any, error) { return c.getSection(ctx, arguments.SectionID) }, nil
}

func prepareLabelsList(bound scope, arguments struct {
	Search string `json:"search"`
	page
}) (run, error) {
	if err := checkSearch(arguments.Search); err != nil {
		return nil, err
	}
	path, query := searchable("/labels", arguments.Search)
	call, err := newListCall("list labels", path, query, arguments.Limit, arguments.Cursor,
		labelsList.ID, bound.String(), arguments.Search)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, c *Client) (any, error) { return c.listLabels(ctx, call) }, nil
}

// taskSelection is the project and section a task list narrows to. A connection bound to exactly one
// project, or to exactly one section, always narrows to it, so Todoist never pages through tasks of other
// places.
func taskSelection(bound scope, projectID, sectionID string) (url.Values, string, error) {
	if err := checkID("project_id", projectID); err != nil {
		return nil, "", err
	}
	if err := checkID("section_id", sectionID); err != nil {
		return nil, "", err
	}
	if err := bound.selectProject(projectID); err != nil {
		return nil, "", err
	}
	if bound.excludesSection(sectionID) {
		return nil, "", outsideScope("section")
	}
	if sectionID != "" && !bound.projectLevel() && !bound.hasSection(sectionID) {
		return nil, "", outsideScope("section")
	}
	if single, ok := bound.single(); ok && projectID == "" {
		projectID = single
	}
	// Todoist narrows to one section only; with several bound sections or a project the rows are held to
	// the connection one by one.
	if sectionID == "" && projectID == "" && !bound.projectLevel() && len(bound.sections) == 1 {
		sectionID = bound.sections[0]
	}
	query := url.Values{}
	if projectID != "" {
		query.Set("project_id", projectID)
	}
	if sectionID != "" {
		query.Set("section_id", sectionID)
	}
	return query, projectID, nil
}

func prepareTasksList(bound scope, arguments struct {
	ProjectID  string   `json:"project_id"`
	SectionID  string   `json:"section_id"`
	ParentID   string   `json:"parent_id"`
	IDs        []string `json:"ids"`
	Label      string   `json:"label"`
	DueFrom    string   `json:"due_from"`
	DueTo      string   `json:"due_to"`
	WithoutDue bool     `json:"without_due"`
	page
}) (run, error) {
	query, project, err := taskSelection(bound, arguments.ProjectID, arguments.SectionID)
	if err != nil {
		return nil, err
	}
	if err := checkID("parent_id", arguments.ParentID); err != nil {
		return nil, err
	}
	if arguments.Label != "" && !validText(arguments.Label, 100) {
		return nil, invalidRequest("label must be 1 to 100 characters without control characters or padding")
	}
	window := dueWindow{from: arguments.DueFrom, to: arguments.DueTo, without: arguments.WithoutDue}
	for name, value := range map[string]string{"due_from": window.from, "due_to": window.to} {
		if value != "" && !validDate(value) {
			return nil, invalidRequest(name + " must be a date as YYYY-MM-DD")
		}
	}
	if window.without && (window.from != "" || window.to != "") {
		return nil, invalidRequest("without_due cannot be combined with due_from or due_to")
	}
	if window.from != "" && window.to != "" && window.from > window.to {
		return nil, invalidRequest("due_from must not be after due_to")
	}
	if err := checkTaskIDs(arguments.IDs); err != nil {
		return nil, err
	}
	if len(arguments.IDs) > 0 {
		query.Set("ids", strings.Join(arguments.IDs, ","))
	}
	if arguments.ParentID != "" {
		query.Set("parent_id", arguments.ParentID)
	}
	if arguments.Label != "" {
		query.Set("label", arguments.Label)
	}
	call, err := newListCall("list tasks", "/tasks", query, arguments.Limit, arguments.Cursor,
		tasksList.ID, bound.String(), project, arguments.SectionID, arguments.ParentID, arguments.IDs, arguments.Label,
		window.from, window.to, window.without)
	if err != nil {
		return nil, err
	}
	call.project = project
	return func(ctx context.Context, c *Client) (any, error) { return c.listTasks(ctx, call, window) }, nil
}

func prepareTasksFilter(bound scope, arguments struct {
	Query string `json:"query"`
	Lang  string `json:"lang"`
	page
}) (run, error) {
	if !validText(arguments.Query, 1024) {
		return nil, invalidRequest("query must be 1 to 1024 characters without control characters or padding")
	}
	if err := checkLang("lang", arguments.Lang); err != nil {
		return nil, err
	}
	values := url.Values{"query": {arguments.Query}}
	if arguments.Lang != "" {
		values.Set("lang", arguments.Lang)
	}
	call, err := newListCall("filter tasks", "/tasks/filter", values,
		arguments.Limit, arguments.Cursor, tasksFilter.ID, bound.String(), arguments.Query, arguments.Lang)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, c *Client) (any, error) { return c.listTasks(ctx, call, dueWindow{}) }, nil
}

func prepareTasksGet(_ scope, arguments struct {
	TaskID string `json:"task_id"`
}) (run, error) {
	if arguments.TaskID == "" || !validID(arguments.TaskID) {
		return nil, invalidRequest("task_id is not a Todoist ID")
	}
	return func(ctx context.Context, c *Client) (any, error) { return c.getTask(ctx, arguments.TaskID) }, nil
}

func prepareCompletedList(bound scope, arguments struct {
	Since       string `json:"since"`
	Until       string `json:"until"`
	ProjectID   string `json:"project_id"`
	SectionID   string `json:"section_id"`
	ParentID    string `json:"parent_id"`
	WorkspaceID *int64 `json:"workspace_id"`
	Query       string `json:"query"`
	FilterLang  string `json:"filter_lang"`
	page
}) (run, error) {
	query, project, err := taskSelection(bound, arguments.ProjectID, arguments.SectionID)
	if err != nil {
		return nil, err
	}
	if err := checkID("parent_id", arguments.ParentID); err != nil {
		return nil, err
	}
	if arguments.ParentID != "" {
		query.Set("parent_id", arguments.ParentID)
	}
	workspace, err := positiveFilter("workspace_id", arguments.WorkspaceID, query)
	if err != nil {
		return nil, err
	}
	if err := checkLang("filter_lang", arguments.FilterLang); err != nil {
		return nil, err
	}
	if arguments.FilterLang != "" {
		if arguments.Query == "" {
			return nil, invalidRequest("filter_lang requires query")
		}
		query.Set("filter_lang", arguments.FilterLang)
	}
	if arguments.Query != "" {
		if !validText(arguments.Query, 1024) {
			return nil, invalidRequest("query must be 1 to 1024 characters without control characters or padding")
		}
		query.Set("filter_query", arguments.Query)
	}
	since, from, err := parseInstant("since", arguments.Since)
	if err != nil {
		return nil, err
	}
	until, to, err := parseInstant("until", arguments.Until)
	if err != nil {
		return nil, err
	}
	if !to.After(from) {
		return nil, invalidRequest("until must be after since")
	}
	query.Set("since", since)
	query.Set("until", until)
	call, err := newListCall("list completed tasks", "/tasks/completed/by_completion_date", query,
		arguments.Limit, arguments.Cursor, completedList.ID, bound.String(), project, arguments.SectionID, arguments.ParentID, workspace, since, until,
		arguments.Query, arguments.FilterLang)
	if err != nil {
		return nil, err
	}
	call.project = project
	return func(ctx context.Context, c *Client) (any, error) { return c.listTasks(ctx, call, dueWindow{}) }, nil
}

func prepareCommentsList(bound scope, arguments struct {
	TaskID    string `json:"task_id"`
	ProjectID string `json:"project_id"`
	page
}) (run, error) {
	if (arguments.TaskID == "") == (arguments.ProjectID == "") {
		return nil, invalidRequest("name exactly one of task_id and project_id")
	}
	if err := checkID("task_id", arguments.TaskID); err != nil {
		return nil, err
	}
	if err := checkID("project_id", arguments.ProjectID); err != nil {
		return nil, err
	}
	if err := bound.selectProject(arguments.ProjectID); err != nil {
		return nil, err
	}
	query := url.Values{}
	if arguments.TaskID != "" {
		query.Set("task_id", arguments.TaskID)
	} else {
		query.Set("project_id", arguments.ProjectID)
	}
	call, err := newListCall("list comments", "/comments", query, arguments.Limit, arguments.Cursor,
		commentsList.ID, bound.String(), arguments.TaskID, arguments.ProjectID)
	if err != nil {
		return nil, err
	}
	call.project = arguments.ProjectID
	return func(ctx context.Context, c *Client) (any, error) {
		return c.listComments(ctx, call, arguments.TaskID, arguments.ProjectID)
	}, nil
}

func prepareRemindersList(bound scope, arguments struct {
	TaskID string `json:"task_id"`
	page
}) (run, error) {
	if err := checkID("task_id", arguments.TaskID); err != nil {
		return nil, err
	}
	if arguments.TaskID == "" && !bound.all {
		return nil, invalidRequest(reminderTaskRequired)
	}
	query := url.Values{}
	if arguments.TaskID != "" {
		query.Set("task_id", arguments.TaskID)
	}
	call, err := newListCall("list reminders", "/reminders", query, arguments.Limit, arguments.Cursor,
		remindersList.ID, bound.String(), arguments.TaskID)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, c *Client) (any, error) { return c.listReminders(ctx, call, arguments.TaskID) }, nil
}

// invokeFiltersList reads the saved filters. A project connection does not offer them: a saved filter
// spans every project of the account, so accountOnly refuses it as an unsupported capability before any
// credential is resolved.
func invokeFiltersList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.listFilters(ctx)
}
