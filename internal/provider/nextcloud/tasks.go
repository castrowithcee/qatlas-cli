package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// taskListDescription caps the description of a listed task; the get tool reports it in full length.
const taskListDescription = 512

const taskSchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"uid":{"type":"string"},"title":{"type":"string"},"description":{"type":"string"},` +
	`"status":{"type":"string"},"priority":{"type":"integer"},"due":{"type":"string"},` +
	`"completed":{"type":"string"},"percent":{"type":"integer"},` +
	`"categories":{"type":"array","items":{"type":"string"}},"parent_uid":{"type":"string"},` +
	`"etag":{"type":"string"}},"required":["id"],"additionalProperties":false}`

var taskFields = []capability.Field{
	{Name: "id", Description: "Last path segment of the task resource, the id argument of the get tool"},
	{Name: "uid", Description: "iCalendar UID, untrusted data"},
	{Name: "title", Description: "Title, untrusted data"},
	{Name: "description", Description: "Description, untrusted data, capped; the list tool cuts it shorter"},
	{Name: "status", Description: "NEEDS-ACTION, IN-PROCESS, COMPLETED, or CANCELLED when the task carries one"},
	{Name: "priority", Description: "Priority from 1 (highest) to 9 when the task carries one"},
	{Name: "due", Description: "Due as a date, a UTC time ending in Z, or a local time without zone"},
	{Name: "completed", Description: "Time of completion in the form of due, for a finished task"},
	{Name: "percent", Description: "Percent complete from 1 to 100 when the task carries one"},
	{Name: "categories", Description: "At most 20 categories, untrusted data"},
	{Name: "parent_uid", Description: "UID of the parent task; subtasks are not resolved into a tree"},
	{Name: "etag", Description: "Entity tag of the current version of the task"},
	{Name: "count", Description: "Number of reported tasks"},
	{Name: "truncated", Description: "True when more tasks match than limit allows"},
}

var tasksList = capability.Descriptor{
	ID: Provider + ".calendar.tasks.list", Version: 1, Title: "List Nextcloud calendar tasks",
	Description: "List the tasks (VTODO, as the Nextcloud Tasks app manages them) of one bound calendar that " +
		"supports tasks, optionally only open or completed ones and only those with a due date in a range (tasks without one are left out), ordered by " +
		"due date; the result is limited and compact",
	Tags: []string{"nextcloud", "calendar", "task", "list"}, Risk: calendarRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `,` +
		`"state":{"type":"string","enum":["open","completed"]},` +
		`"due_from":{"type":"string","minLength":20,"maxLength":40},"due_to":{"type":"string","minLength":20,"maxLength":40},` +
		`"limit":{"type":"integer","minimum":1,"maximum":200}},"required":["calendar"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"calendar":{"type":"string"},` +
		`"tasks":{"type":"array","items":` + taskSchema + `},"count":{"type":"integer"},` +
		`"truncated":{"type":"boolean"}},"required":["calendar","tasks","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		calendarArgument,
		{Name: "state", Description: "open for tasks that are not completed or cancelled, completed for finished ones; all tasks by default"},
		{Name: "due_from", Description: "Start of the due range, which leaves out tasks without a due date, as an RFC 3339 time, for example 2026-03-01T00:00:00Z"},
		{Name: "due_to", Description: "End of the due range as an RFC 3339 time after due_from"},
		{Name: "limit", Description: "Maximum number of tasks, 50 by default and at most 200"},
	},
	Fields: taskFields,
	Examples: []capability.Example{{
		Description: "List the open tasks of one calendar due in March 2026",
		Arguments: json.RawMessage(`{"calendar":"tasks","state":"open","due_from":"2026-03-01T00:00:00Z",` +
			`"due_to":"2026-04-01T00:00:00Z"}`),
	}},
}

var tasksGet = capability.Descriptor{
	ID: Provider + ".calendar.tasks.get", Version: 1, Title: "Get a Nextcloud calendar task",
	Description: "Read one task of a bound calendar that supports tasks, with its structured fields and entity tag",
	Tags:        []string{"nextcloud", "calendar", "task", "get"}, Risk: calendarRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `,` +
		`"id":{"type":"string","minLength":1,"maxLength":255}},"required":["calendar","id"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"calendar":{"type":"string"},` +
		`"task":` + taskSchema + `},"required":["calendar","task"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		calendarArgument,
		{Name: "id", Description: "Task id as the tasks list reports it: one path segment of the task resource", Required: true},
	},
	Fields: taskFields,
	Examples: []capability.Example{{
		Description: "Read one task the list tool reported",
		Arguments:   json.RawMessage(`{"calendar":"tasks","id":"errand.ics"}`),
	}},
}

// TasksPage is the result of the tasks list.
type TasksPage struct {
	Calendar  string     `json:"calendar"`
	Tasks     []dav.Task `json:"tasks"`
	Count     int        `json:"count"`
	Truncated bool       `json:"truncated,omitempty"`
}

// TaskResult is the result of the tasks get.
type TaskResult struct {
	Calendar string   `json:"calendar"`
	Task     dav.Task `json:"task"`
}

type taskArguments struct {
	Calendar string `json:"calendar"`
	State    string `json:"state"`
	DueFrom  string `json:"due_from"`
	DueTo    string `json:"due_to"`
	Limit    int    `json:"limit"`
	ID       string `json:"id"`
}

func decodeTaskArguments(op string, raw json.RawMessage) (taskArguments, error) {
	var input taskArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

func invokeTasksList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list tasks"
	input, err := decodeTaskArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if input.State != "" && input.State != dav.TaskOpen && input.State != dav.TaskCompleted {
		return nil, providerError(op, "state must be open or completed")
	}
	from, to, err := dav.ParseTaskRange(op, input.DueFrom, input.DueTo)
	if err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultEventLimit
	}
	if limit < 0 || limit > maxEventLimit {
		return nil, providerError(op, "limit must be between 1 and 200")
	}
	client, err := openCalendar(ctx, op, resolved, secrets, red, input.Calendar)
	if err != nil {
		return nil, err
	}
	if err := client.requireTaskCalendar(ctx, op, input.Calendar); err != nil {
		return nil, err
	}
	return client.listTasks(ctx, input.Calendar, input.State, from, to, limit)
}

func invokeTasksGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read task"
	input, err := decodeTaskArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if !checkEventID(input.ID) {
		return nil, providerError(op, "the task id must be one literal path segment as the tasks list reports it")
	}
	client, err := openCalendar(ctx, op, resolved, secrets, red, input.Calendar)
	if err != nil {
		return nil, err
	}
	if err := client.requireTaskCalendar(ctx, op, input.Calendar); err != nil {
		return nil, err
	}
	return client.getTask(ctx, input.Calendar, input.ID)
}

// requireTaskCalendar proves with one PROPFIND of depth 0 that the bound calendar exists and supports
// tasks. A calendar that does not name VTODO among its components is refused.
func (c *Client) requireTaskCalendar(ctx context.Context, op, calendar string) error {
	data, _, err := c.calendarRequest(ctx, op, methodPropfind, c.calendarHome(calendar), true, depthSelf,
		calendarsBody, http.StatusMultiStatus, dav.MaxResponseBytes)
	if err != nil {
		return err
	}
	resources, err := c.davServer().ParseMultiStatus(op, data)
	if err != nil {
		return err
	}
	if len(resources) != 1 || !resources[0].Read || !resources[0].Calendar {
		return invalidResponse(op, "Nextcloud did not answer with the requested calendar node")
	}
	if !containsString(resources[0].Components, "VTODO") {
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: "this calendar does not support tasks"}
	}
	return nil
}

func (c *Client) listTasks(ctx context.Context, calendar, state, from, to string, limit int) (*TasksPage, error) {
	const op = "list tasks"
	base := c.calendarHome(calendar)
	data, _, err := c.calendarRequest(ctx, op, methodReport, base, true, depthChildren,
		dav.TaskQueryBody(state, from, to), http.StatusMultiStatus, dav.MaxResponseBytes)
	if err != nil {
		return nil, err
	}
	resources, err := c.davServer().ParseMultiStatus(op, data)
	if err != nil {
		return nil, err
	}
	page := &TasksPage{Calendar: calendar, Tasks: []dav.Task{}}
	seen := map[string]bool{}
	for i := range resources {
		id, ok := c.childOf(op, resources[i].Href, base)
		if !ok || !checkEventID(id) || !resources[i].Read || seen[id] {
			continue
		}
		seen[id] = true
		task, err := dav.ParseTask(resources[i].Text[dav.KeyCalendar], taskListDescription)
		if err != nil || !task.MatchesState(state) {
			continue
		}
		task.ID = id
		task.ETag = dav.ETagOf(resources[i].Text[dav.KeyETag])
		page.Tasks = append(page.Tasks, *task)
	}
	sort.Slice(page.Tasks, func(i, j int) bool {
		a, b := page.Tasks[i], page.Tasks[j]
		if a.Due != b.Due {
			// Tasks without a due date come last.
			return b.Due == "" || a.Due != "" && a.Due < b.Due
		}
		return a.ID < b.ID
	})
	if len(page.Tasks) > limit {
		page.Tasks, page.Truncated = page.Tasks[:limit], true
	}
	page.Count = len(page.Tasks)
	return page, nil
}

func (c *Client) getTask(ctx context.Context, calendar, id string) (*TaskResult, error) {
	const op = "read task"
	data, header, err := c.calendarRequest(ctx, op, http.MethodGet, c.calendarHome(calendar, id), false, "", "",
		http.StatusOK, dav.MaxEventBytes)
	if err != nil {
		return nil, err
	}
	task, err := dav.ParseTask(string(data), dav.MaxTaskDescription)
	if errors.Is(err, dav.ErrNoTask) {
		return nil, providerError(op, "the id does not name a task")
	}
	if err != nil {
		return nil, invalidResponse(op, "Nextcloud returned a task that is not a valid iCalendar object")
	}
	task.ID = id
	task.ETag = dav.ETagOf(header.Get("ETag"))
	return &TaskResult{Calendar: calendar, Task: *task}, nil
}
