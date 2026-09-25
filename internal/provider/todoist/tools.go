package todoist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

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
	idSchema      = `{"type":"string","minLength":1,"maxLength":64,"pattern":"^[A-Za-z0-9]+$"}`
	limitSchema   = `{"type":"integer","minimum":1,"maximum":200}`
	cursorSchema  = `{"type":"string","minLength":1,"maxLength":1024,"pattern":"^[A-Za-z0-9_-]+$"}`
	searchSchema  = `{"type":"string","minLength":1,"maxLength":200,"pattern":"^[^\\x00-\\x1f\\x7f]+$"}`
	labelSchema   = `{"type":"string","minLength":1,"maxLength":100,"pattern":"^[^\\x00-\\x1f\\x7f]+$"}`
	querySchema   = `{"type":"string","minLength":1,"maxLength":1024,"pattern":"^[^\\x00-\\x1f\\x7f]+$"}`
	dateSchema    = `{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"}`
	instantSchema = `{"type":"string","minLength":20,"maxLength":35,` +
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
		`"is_recurring":` + booleanSchema + `,"timezone":` + stringSchema + `},` +
		`"required":["date","is_recurring"],"additionalProperties":false}`
	projectSchema = `{"type":"object","properties":{"id":` + stringSchema + `,"name":` + stringSchema + `,` +
		`"parent_id":` + stringSchema + `,"color":` + stringSchema + `,"is_favorite":` + booleanSchema + `,` +
		`"is_shared":` + booleanSchema + `,"is_inbox":` + booleanSchema + `,"view_style":` + stringSchema + `,` +
		`"description":` + stringSchema + `,"is_archived":` + booleanSchema + `,"created_at":` + stringSchema + `,` +
		`"updated_at":` + stringSchema + `},` +
		`"required":["id","name","is_favorite","is_shared","is_inbox"],"additionalProperties":false}`
	sectionSchema = `{"type":"object","properties":{"id":` + stringSchema + `,"project_id":` + stringSchema + `,` +
		`"name":` + stringSchema + `,"order":` + integerSchema + `,"description":` + stringSchema + `},` +
		`"required":["id","project_id","name","order"],"additionalProperties":false}`
	labelOutputSchema = `{"type":"object","properties":{"id":` + stringSchema + `,"name":` + stringSchema + `,` +
		`"color":` + stringSchema + `,"order":` + integerSchema + `,"is_favorite":` + booleanSchema + `},` +
		`"required":["id","name","order","is_favorite"],"additionalProperties":false}`
	taskSchema = `{"type":"object","properties":{"id":` + stringSchema + `,"content":` + stringSchema + `,` +
		`"project_id":` + stringSchema + `,"section_id":` + stringSchema + `,"parent_id":` + stringSchema + `,` +
		`"labels":{"type":"array","items":` + stringSchema + `},"priority":` + integerSchema + `,` +
		`"due":` + dueSchema + `,"deadline":` + stringSchema + `,"comment_count":` + integerSchema + `,` +
		`"description":` + stringSchema + `,"added_at":` + stringSchema + `,"updated_at":` + stringSchema + `,` +
		`"completed_at":` + stringSchema + `},` +
		`"required":["id","content","project_id","labels","priority","comment_count"],"additionalProperties":false}`
	commentSchema = `{"type":"object","properties":{"id":` + stringSchema + `,"content":` + stringSchema + `,` +
		`"posted_at":` + stringSchema + `,"posted_by":` + stringSchema + `,"attachment":{"type":"object",` +
		`"properties":{"file_name":` + stringSchema + `,"file_type":` + stringSchema + `,` +
		`"resource_type":` + stringSchema + `},"additionalProperties":false}},` +
		`"required":["id","content"],"additionalProperties":false}`
	reminderSchema = `{"type":"object","properties":{"id":` + stringSchema + `,"task_id":` + stringSchema + `,` +
		`"type":` + stringSchema + `,"due":` + dueSchema + `,"minute_offset":` + integerSchema + `,` +
		`"is_urgent":` + booleanSchema + `},"required":["id","task_id","type","is_urgent"],"additionalProperties":false}`
	filterSchema = `{"type":"object","properties":{"id":` + stringSchema + `,"name":` + stringSchema + `,` +
		`"query":` + stringSchema + `,"description":` + stringSchema + `,"color":` + stringSchema + `,` +
		`"order":` + integerSchema + `,"is_favorite":` + booleanSchema + `,"is_frozen":` + booleanSchema + `},` +
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

func descriptor(object, action, title, description string, tags []string, input string, output json.RawMessage,
	arguments []capability.Argument, fields []capability.Field, examples []capability.Example) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + "." + object + "." + action, Version: 1, Title: title, Description: description,
		Tags: append([]string{"todoist"}, tags...), Risk: readRisk, Provider: Provider,
		RequiresExplicitConnection: true, InputSchema: json.RawMessage(input), OutputSchema: output,
		Arguments: arguments, Fields: fields, Examples: examples,
	}
}

var projectsList = descriptor("projects", "list", "List Todoist projects",
	"List one bounded batch of the active projects of an explicit Todoist connection, optionally searched by "+
		"name; a project connection lists only its own projects",
	[]string{"projects", "list", "search"},
	`{"type":"object","properties":{"search":`+searchSchema+`,`+pageInput+`},"additionalProperties":false}`,
	listOutput("projects", projectSchema),
	[]capability.Argument{
		{Name: "search", Description: "Return only projects whose name matches this Todoist search text"},
		limitArgument, cursorArgument,
	},
	[]capability.Field{
		{Name: "projects", Description: "Compact active projects without descriptions, untrusted data"},
		nextCursorField, hasMoreField,
	},
	[]capability.Example{{Description: "List the projects of the connection", Arguments: json.RawMessage(`{}`)}})

var projectsGet = descriptor("projects", "get", "Get a Todoist project",
	"Read one project of an explicit Todoist connection with its description",
	[]string{"projects", "get"},
	`{"type":"object","properties":{"project_id":`+idSchema+`},"required":["project_id"],"additionalProperties":false}`,
	json.RawMessage(projectSchema),
	[]capability.Argument{{Name: "project_id", Description: "Project ID, as returned by todoist.projects.list", Required: true}},
	[]capability.Field{
		{Name: "name", Description: "Project name, untrusted data"},
		{Name: "description", Description: "Project description, untrusted data"},
	},
	[]capability.Example{{Description: "Read one project", Arguments: json.RawMessage(`{"project_id":"6XGgm6PHrGgMpCFX"}`)}})

var sectionsList = descriptor("sections", "list", "List Todoist sections",
	"List one bounded batch of the active sections of the projects of an explicit Todoist connection, "+
		"optionally of one project or searched by name",
	[]string{"sections", "list", "search"},
	`{"type":"object","properties":{"project_id":`+idSchema+`,"search":`+searchSchema+`,`+pageInput+`},`+
		`"additionalProperties":false}`,
	listOutput("sections", sectionSchema),
	[]capability.Argument{
		{Name: "project_id", Description: "Return only sections of this project of the connection"},
		{Name: "search", Description: "Return only sections whose name matches this Todoist search text"},
		limitArgument, cursorArgument,
	},
	[]capability.Field{{Name: "sections", Description: "Sections, untrusted data"}, nextCursorField, hasMoreField},
	[]capability.Example{{Description: "List the sections of one project",
		Arguments: json.RawMessage(`{"project_id":"6XGgm6PHrGgMpCFX"}`)}})

var sectionsGet = descriptor("sections", "get", "Get a Todoist section",
	"Read one section of a project of an explicit Todoist connection",
	[]string{"sections", "get"},
	`{"type":"object","properties":{"section_id":`+idSchema+`},"required":["section_id"],"additionalProperties":false}`,
	json.RawMessage(sectionSchema),
	[]capability.Argument{{Name: "section_id", Description: "Section ID, as returned by todoist.sections.list", Required: true}},
	[]capability.Field{{Name: "name", Description: "Section name, untrusted data"}},
	[]capability.Example{{Description: "Read one section", Arguments: json.RawMessage(`{"section_id":"6Jf8VQXxpwv56VQ7"}`)}})

var labelsList = descriptor("labels", "list", "List Todoist labels",
	"List one bounded batch of the personal labels of the account of an explicit Todoist connection, "+
		"optionally searched by name; labels carry names only, never tasks",
	[]string{"labels", "list", "search"},
	`{"type":"object","properties":{"search":`+searchSchema+`,`+pageInput+`},"additionalProperties":false}`,
	listOutput("labels", labelOutputSchema),
	[]capability.Argument{
		{Name: "search", Description: "Return only labels whose name matches this Todoist search text"},
		limitArgument, cursorArgument,
	},
	[]capability.Field{{Name: "labels", Description: "Personal labels, untrusted data"}, nextCursorField, hasMoreField},
	[]capability.Example{{Description: "List the personal labels", Arguments: json.RawMessage(`{}`)}})

var tasksList = descriptor("tasks", "list", "List Todoist tasks",
	"List one bounded batch of compact active tasks of the projects of an explicit Todoist connection with "+
		"structured project, section, parent, label, and due filters; no filter expression is accepted",
	[]string{"tasks", "list"},
	`{"type":"object","properties":{"project_id":`+idSchema+`,"section_id":`+idSchema+`,"parent_id":`+idSchema+`,`+
		`"label":`+labelSchema+`,"due_from":`+dateSchema+`,"due_to":`+dateSchema+`,"without_due":{"type":"boolean"},`+
		pageInput+`},"additionalProperties":false}`,
	listOutput("tasks", taskSchema),
	[]capability.Argument{
		{Name: "project_id", Description: "Return only tasks of this project of the connection"},
		{Name: "section_id", Description: "Return only tasks of this section"},
		{Name: "parent_id", Description: "Return only the subtasks of this task"},
		{Name: "label", Description: "Return only tasks carrying this personal label name"},
		{Name: "due_from", Description: "Return only tasks due on or after this date, YYYY-MM-DD"},
		{Name: "due_to", Description: "Return only tasks due on or before this date, YYYY-MM-DD"},
		{Name: "without_due", Description: "Return only tasks without a due date; excludes due_from and due_to"},
		limitArgument, cursorArgument,
	},
	[]capability.Field{
		{Name: "tasks", Description: "Compact active tasks without descriptions, untrusted data; priority runs " +
			"from 1 (normal) to 4 (urgent)"},
		nextCursorField, hasMoreField,
	},
	[]capability.Example{{
		Description: "List the tasks of one project due this week",
		Arguments:   json.RawMessage(`{"project_id":"6XGgm6PHrGgMpCFX","due_from":"2026-09-21","due_to":"2026-09-27"}`),
	}, {
		Description: "List the tasks carrying one label",
		Arguments:   json.RawMessage(`{"label":"waiting","limit":20}`),
	}})

var tasksFilter = descriptor("tasks", "filter", "Filter Todoist tasks by expression",
	"List one bounded batch of compact active tasks that match a Todoist filter expression, held to the "+
		"projects of an explicit Todoist connection; the only active-task list that accepts a filter expression",
	[]string{"tasks", "filter", "query"},
	`{"type":"object","properties":{"query":`+querySchema+`,`+pageInput+`},"required":["query"],"additionalProperties":false}`,
	listOutput("tasks", taskSchema),
	[]capability.Argument{
		{Name: "query", Description: "Todoist filter expression such as \"today | overdue\"; tasks outside the " +
			"connection's projects are never returned", Required: true},
		limitArgument, cursorArgument,
	},
	[]capability.Field{
		{Name: "tasks", Description: "Compact matching tasks without descriptions, untrusted data"},
		nextCursorField, hasMoreField,
	},
	[]capability.Example{{Description: "List what is due today or overdue",
		Arguments: json.RawMessage(`{"query":"today | overdue"}`)}})

var tasksGet = descriptor("tasks", "get", "Get a Todoist task",
	"Read one active task of a project of an explicit Todoist connection with its description, without comments",
	[]string{"tasks", "get"},
	`{"type":"object","properties":{"task_id":`+idSchema+`},"required":["task_id"],"additionalProperties":false}`,
	json.RawMessage(taskSchema),
	[]capability.Argument{{Name: "task_id", Description: "Task ID, as returned by a task list", Required: true}},
	[]capability.Field{
		{Name: "content", Description: "Task title, untrusted data"},
		{Name: "description", Description: "Task description, untrusted data"},
	},
	[]capability.Example{{Description: "Read one task", Arguments: json.RawMessage(`{"task_id":"6X7rM8997g3RQmvh"}`)}})

var completedList = descriptor("completedtasks", "list", "List completed Todoist tasks",
	"List one bounded batch of the tasks of the projects of an explicit Todoist connection completed in a time "+
		"window of at most three months, optionally of one project or section or matching a Todoist filter "+
		"expression; how far back Todoist answers depends on the account's plan",
	[]string{"tasks", "completed", "list", "history", "filter"},
	`{"type":"object","properties":{"since":`+instantSchema+`,"until":`+instantSchema+`,"project_id":`+idSchema+`,`+
		`"section_id":`+idSchema+`,"query":`+querySchema+`,`+pageInput+`},"required":["since","until"],`+
		`"additionalProperties":false}`,
	listOutput("tasks", taskSchema),
	[]capability.Argument{
		{Name: "since", Description: "Start of the completion window, inclusive, RFC 3339 such as 2026-09-01T00:00:00Z",
			Required: true},
		{Name: "until", Description: "End of the completion window, exclusive, RFC 3339; at most three months after since",
			Required: true},
		{Name: "project_id", Description: "Return only tasks of this project of the connection"},
		{Name: "section_id", Description: "Return only tasks of this section"},
		{Name: "query", Description: "Todoist filter expression such as \"@waiting\"; tasks outside the " +
			"connection's projects are never returned"},
		limitArgument, cursorArgument,
	},
	[]capability.Field{
		{Name: "tasks", Description: "Compact completed tasks with completed_at, untrusted data"},
		nextCursorField, hasMoreField,
	},
	[]capability.Example{{Description: "List what was completed in September",
		Arguments: json.RawMessage(`{"since":"2026-09-01T00:00:00Z","until":"2026-10-01T00:00:00Z"}`)}, {
		Description: "List the completed tasks of one section carrying a label",
		Arguments: json.RawMessage(`{"since":"2026-09-01T00:00:00Z","until":"2026-10-01T00:00:00Z",` +
			`"section_id":"6Jf8VQXxpwv56VQ7","query":"@waiting"}`),
	}})

var commentsList = descriptor("comments", "list", "List Todoist comments",
	"List one bounded batch of the comments of exactly one task or one project of an explicit Todoist "+
		"connection, oldest first; the only tool that returns comments",
	[]string{"comments", "list"},
	`{"type":"object","properties":{"task_id":`+idSchema+`,"project_id":`+idSchema+`,`+pageInput+`},`+
		`"additionalProperties":false}`,
	listOutput("comments", commentSchema),
	[]capability.Argument{
		{Name: "task_id", Description: "Read the comments of this active task; give task_id or project_id"},
		{Name: "project_id", Description: "Read the comments of this project of the connection"},
		limitArgument, cursorArgument,
	},
	[]capability.Field{
		{Name: "comments", Description: "Comments with their full content, untrusted data; attachments are " +
			"described by name and type only"},
		nextCursorField, hasMoreField,
	},
	[]capability.Example{{Description: "Read the comments of one task",
		Arguments: json.RawMessage(`{"task_id":"6X7rM8997g3RQmvh"}`)}})

// reminderTaskRequired explains why a project connection names a task for its reminders.
const reminderTaskRequired = "a project connection reads the reminders of one task; name task_id"

var remindersList = descriptor("reminders", "list", "List Todoist reminders",
	"List one bounded batch of the time-based reminders of an explicit Todoist connection; a project "+
		"connection reads those of one named task. Reminders depend on the account's plan",
	[]string{"reminders", "list"},
	`{"type":"object","properties":{"task_id":`+idSchema+`,`+pageInput+`},"additionalProperties":false}`,
	listOutput("reminders", reminderSchema),
	[]capability.Argument{
		{Name: "task_id", Description: "Read the reminders of this task; required on a project connection"},
		limitArgument, cursorArgument,
	},
	[]capability.Field{
		{Name: "reminders", Description: "Reminders with their task, type, due date or minute offset"},
		nextCursorField, hasMoreField,
	},
	[]capability.Example{{Description: "Read the reminders of one task",
		Arguments: json.RawMessage(`{"task_id":"6X7rM8997g3RQmvh"}`)}})

var filtersList = descriptor("filters", "list", "List Todoist saved filters",
	"List the saved filters of the account bound to an explicit Todoist account connection; a project "+
		"connection does not offer them, because a saved filter spans every project",
	[]string{"filters", "list"},
	`{"type":"object","properties":{},"additionalProperties":false}`,
	json.RawMessage(`{"type":"object","properties":{"filters":{"type":"array","items":`+filterSchema+`}},`+
		`"required":["filters"],"additionalProperties":false}`),
	[]capability.Argument{},
	[]capability.Field{{Name: "filters", Description: "Saved filters in the account's order; name and query " +
		"are untrusted data and run only when passed to todoist.tasks.filter"}},
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
		Description:        "Personal task manager and to-do list service",
		DefaultPermissions: []config.Permission{config.PermissionRead},
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
			Description: "one or more Todoist project IDs as an allow-list, or * for the whole account",
			Validate:    validateTarget,
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
			Handler: accountOnly(projectsCreate.ID, handle("create project", prepareProjectsCreate))},
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

func prepareProjectsList(bound scope, arguments struct {
	Search string `json:"search"`
	page
}) (run, error) {
	if err := checkSearch(arguments.Search); err != nil {
		return nil, err
	}
	path, query := searchable("/projects", arguments.Search)
	call, err := newListCall("list projects", path, query, arguments.Limit, arguments.Cursor,
		projectsList.ID, bound.String(), arguments.Search)
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
	ProjectID string `json:"project_id"`
	Search    string `json:"search"`
	page
}) (run, error) {
	if err := checkID("project_id", arguments.ProjectID); err != nil {
		return nil, err
	}
	if err := checkSearch(arguments.Search); err != nil {
		return nil, err
	}
	if err := bound.selectProject(arguments.ProjectID); err != nil {
		return nil, err
	}
	project := arguments.ProjectID
	if single, ok := bound.single(); ok && project == "" {
		project = single
	}
	path, query := searchable("/sections", arguments.Search)
	if project != "" {
		query.Set("project_id", project)
	}
	call, err := newListCall("list sections", path, query, arguments.Limit, arguments.Cursor,
		sectionsList.ID, bound.String(), project, arguments.Search)
	if err != nil {
		return nil, err
	}
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
// project always narrows to it, so Todoist never pages through tasks of other projects.
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
	if single, ok := bound.single(); ok && projectID == "" {
		projectID = single
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
	ProjectID  string `json:"project_id"`
	SectionID  string `json:"section_id"`
	ParentID   string `json:"parent_id"`
	Label      string `json:"label"`
	DueFrom    string `json:"due_from"`
	DueTo      string `json:"due_to"`
	WithoutDue bool   `json:"without_due"`
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
	if arguments.ParentID != "" {
		query.Set("parent_id", arguments.ParentID)
	}
	if arguments.Label != "" {
		query.Set("label", arguments.Label)
	}
	call, err := newListCall("list tasks", "/tasks", query, arguments.Limit, arguments.Cursor,
		tasksList.ID, bound.String(), project, arguments.SectionID, arguments.ParentID, arguments.Label,
		window.from, window.to, window.without)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, c *Client) (any, error) { return c.listTasks(ctx, call, window, false) }, nil
}

func prepareTasksFilter(bound scope, arguments struct {
	Query string `json:"query"`
	page
}) (run, error) {
	if !validText(arguments.Query, 1024) {
		return nil, invalidRequest("query must be 1 to 1024 characters without control characters or padding")
	}
	call, err := newListCall("filter tasks", "/tasks/filter", url.Values{"query": {arguments.Query}},
		arguments.Limit, arguments.Cursor, tasksFilter.ID, bound.String(), arguments.Query)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, c *Client) (any, error) { return c.listTasks(ctx, call, dueWindow{}, false) }, nil
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
	Since     string `json:"since"`
	Until     string `json:"until"`
	ProjectID string `json:"project_id"`
	SectionID string `json:"section_id"`
	Query     string `json:"query"`
	page
}) (run, error) {
	query, project, err := taskSelection(bound, arguments.ProjectID, arguments.SectionID)
	if err != nil {
		return nil, err
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
		arguments.Limit, arguments.Cursor, completedList.ID, bound.String(), project, arguments.SectionID, since, until,
		arguments.Query)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, c *Client) (any, error) { return c.listTasks(ctx, call, dueWindow{}, false) }, nil
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
