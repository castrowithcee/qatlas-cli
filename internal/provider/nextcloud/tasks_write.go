package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Task writes in a bound calendar share the event write path: a PUT with If-None-Match: * creates, a PUT with
// If-Match replaces, and a DELETE with If-Match removes one task, each after one read-only pre-check.

const taskProductID = eventProductID

var taskKind = objectKind{"a", "task", "VTODO", "; the change may have been applied, check the calendar with " +
	"calendar.tasks.list or calendar.tasks.get before repeating it"}

const taskFieldSchemas = `"title":{"type":"string","minLength":1,"maxLength":256},` +
	`"description":{"type":"string","maxLength":4096},` +
	`"status":{"type":"string","enum":["NEEDS-ACTION","IN-PROCESS","COMPLETED","CANCELLED"]},` +
	`"priority":{"type":"integer","minimum":0,"maximum":9},"due":{"type":"string","minLength":10,"maxLength":40},` +
	`"percent":{"type":"integer","minimum":0,"maximum":100},` +
	`"categories":{"type":"array","maxItems":20,"items":{"type":"string","minLength":1,"maxLength":64}}`

const taskParentSchema = `"parent_uid":{"type":"string","minLength":1,"maxLength":256}`

var taskFieldArguments = []capability.Argument{
	{Name: "title", Description: "Title of the task", Required: true},
	{Name: "description", Description: "Description; line breaks are kept"},
	{Name: "status", Description: "NEEDS-ACTION (default), IN-PROCESS, COMPLETED, or CANCELLED; COMPLETED also sets percent 100 and the completion time"},
	{Name: "priority", Description: "Priority from 1 (highest) to 9; 0 or omitted for none"},
	{Name: "due", Description: "Due as a date YYYY-MM-DD or an RFC 3339 time, stored in UTC"},
	{Name: "percent", Description: "Percent complete from 1 to 100; omitted for none"},
	{Name: "categories", Description: "At most 20 categories"},
}

var (
	taskIDArgument   = capability.Argument{Name: "id", Description: "Task id as calendar.tasks.list or calendar.tasks.get reports it", Required: true}
	taskETagArgument = capability.Argument{Name: "etag", Required: true,
		Description: "Entity tag of the task version to change, as calendar.tasks.get or calendar.tasks.list reports it"}
)

var tasksCreate = capability.Descriptor{
	ID: Provider + ".calendar.tasks.create", Version: 1, Title: "Create a Nextcloud calendar task",
	Description: "Create one task (VTODO) in a bound calendar from structured fields. Qatlas builds the iCalendar " +
		"object itself and generates its UID and resource id; nothing raw comes from an argument. Without " +
		"parent_uid one read-only pre-check of the calendar refuses a read-only calendar or one without tasks; " +
		"with it one query of the calendar must find exactly one task with that UID, otherwise nothing is " +
		"written. Then one PUT that never replaces an existing task stores it. Recurrence and alarms are not " +
		"supported. The request is never repeated after an unclear result",
	Tags: []string{"nextcloud", "calendar", "task", "create"}, Provider: Provider,
	Risk: eventWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `,` + taskFieldSchemas + `,` +
		taskParentSchema + `},"required":["calendar","title"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + eventWriteOutProps + `,"uid":{"type":"string"},` +
		`"etag":{"type":"string"},"created":{"type":"boolean"}},"required":["calendar","id","uid","created"],` +
		`"additionalProperties":false}`),
	Arguments: append(append([]capability.Argument{calendarArgument}, taskFieldArguments...),
		capability.Argument{Name: "parent_uid", Description: "UID of a task of the same calendar that becomes the parent"}),
	Fields: []capability.Field{
		{Name: "id", Description: "Generated resource id, the id argument of the other task tools"},
		{Name: "uid", Description: "Generated iCalendar UID"},
		{Name: "etag", Description: "Entity tag of the new task when Nextcloud reports one"},
		{Name: "created", Description: "True when Nextcloud stored the task"},
	},
	Examples: []capability.Example{{Description: "Create a task due on a day",
		Arguments: json.RawMessage(`{"calendar":"tasks","title":"Send invoice","due":"2026-03-10","priority":3}`)}},
}

var tasksUpdate = capability.Descriptor{
	ID: Provider + ".calendar.tasks.update", Version: 1, Title: "Replace a Nextcloud calendar task",
	Description: "Replace one task of a bound calendar completely by structured fields, bound to its etag; this is " +
		"also how a task is completed. One read-only pre-check reads the task and refuses a changed etag and " +
		"a task with content Qatlas does not write, such as recurrence, alarms, attachments, or extensions; then " +
		"one PUT with the etag follows. UID and parent relation are kept and SEQUENCE raised. A read-only " +
		"calendar is refused by Nextcloud. The request is never repeated after an unclear result",
	Tags: []string{"nextcloud", "calendar", "task", "update"}, Provider: Provider,
	Risk: eventWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `,` + eventIDArgSchema + `,` +
		eventETagArgSchema + `,` + taskFieldSchemas + `},"required":["calendar","id","etag","title"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + eventWriteOutProps + `,"uid":{"type":"string"},` +
		`"etag":{"type":"string"},"updated":{"type":"boolean"}},"required":["calendar","id","uid","updated"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{calendarArgument, taskIDArgument, taskETagArgument}, taskFieldArguments...),
	Fields: []capability.Field{
		{Name: "id", Description: "Task id"},
		{Name: "uid", Description: "iCalendar UID of the task, kept"},
		{Name: "etag", Description: "Entity tag of the new version when Nextcloud reports one"},
		{Name: "updated", Description: "True when Nextcloud stored the new version"},
	},
	Examples: []capability.Example{{Description: "Complete a task",
		Arguments: json.RawMessage(`{"calendar":"tasks","id":"errand.ics","etag":"e1","title":"Send invoice","status":"COMPLETED"}`)}},
}

var tasksDelete = capability.Descriptor{
	ID: Provider + ".calendar.tasks.delete", Version: 1, Title: "Delete a Nextcloud calendar task",
	Description: "Delete one task of a bound calendar, bound to its etag; a changed etag deletes nothing, and an " +
		"object that is no task is refused. One read-only pre-check reads the task, then one DELETE follows. " +
		"A read-only calendar is refused by Nextcloud. Nextcloud may keep the task in its calendar trash bin " +
		"for a while; Qatlas offers no restore. The request is never repeated after an unclear result",
	Tags: []string{"nextcloud", "calendar", "task", "delete"}, Provider: Provider,
	Risk: eventWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `,` + eventIDArgSchema + `,` +
		eventETagArgSchema + `},"required":["calendar","id","etag"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + eventWriteOutProps + `,"deleted":{"type":"boolean"}},` +
		`"required":["calendar","id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{calendarArgument, taskIDArgument, taskETagArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Task id"},
		{Name: "deleted", Description: "True when Nextcloud deleted the task"},
	},
	Examples: []capability.Example{{Description: "Delete a task",
		Arguments: json.RawMessage(`{"calendar":"tasks","id":"errand.ics","etag":"e1"}`)}},
	// Reachable only through a tools list, never through a profile.
	RequiresToolAllowList: true,
}

// TaskWriteResult is the result of the task create, update, and delete tools.
type TaskWriteResult = EventWriteResult

type taskWriteArguments struct {
	Calendar string `json:"calendar"`
	ID       string `json:"id"`
	ETag     string `json:"etag"`
	dav.TaskInput
}

func invokeTasksCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create task"
	var input taskWriteArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	if input.ID != "" || input.ETag != "" {
		return nil, providerError(op, "the id of a new task is generated and takes no etag")
	}
	draft, err := dav.NewTaskDraft(op, taskProductID, input.TaskInput)
	if err != nil {
		return nil, err
	}
	uid, err := dav.RandomID()
	if err != nil {
		return nil, providerError(op, "a task id could not be generated")
	}
	body, err := draft.Encode(op, uid, dav.TaskCarry{}, time.Now())
	if err != nil {
		return nil, err
	}
	client, err := openCalendar(ctx, op, resolved, secrets, red, input.Calendar)
	if err != nil {
		return nil, err
	}
	if draft.ParentUID() != "" {
		err = client.requireParentTask(ctx, op, input.Calendar, draft.ParentUID())
	} else {
		err = client.calendarWritable(ctx, op, input.Calendar, taskKind)
	}
	if err != nil {
		return nil, err
	}
	id := uid + ".ics"
	header, err := client.objectSend(ctx, op, taskKind, http.MethodPut, input.Calendar, id, "", body)
	if err != nil {
		return nil, err
	}
	return &TaskWriteResult{Calendar: input.Calendar, ID: id, UID: uid, ETag: dav.ETagOf(header.Get("ETag")),
		Created: true}, nil
}

func invokeTasksUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update task"
	var input taskWriteArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	etag, err := eventTarget(op, input.ID, input.ETag)
	if err != nil {
		return nil, err
	}
	if input.ParentUID != "" {
		return nil, providerError(op, "the parent of a task is set when it is created and kept by an update")
	}
	draft, err := dav.NewTaskDraft(op, taskProductID, input.TaskInput)
	if err != nil {
		return nil, err
	}
	client, err := openCalendar(ctx, op, resolved, secrets, red, input.Calendar)
	if err != nil {
		return nil, err
	}
	stored, err := client.taskStored(ctx, op, input.Calendar, input.ID, etag)
	if err != nil {
		return nil, err
	}
	current, err := client.davServer().ExistingTaskOf(op, stored)
	if err != nil {
		return nil, err
	}
	body, err := draft.Encode(op, current.UID, current.Carry, time.Now())
	if err != nil {
		return nil, err
	}
	header, err := client.objectSend(ctx, op, taskKind, http.MethodPut, input.Calendar, input.ID, etag, body)
	if err != nil {
		return nil, err
	}
	return &TaskWriteResult{Calendar: input.Calendar, ID: input.ID, UID: current.UID,
		ETag: dav.ETagOf(header.Get("ETag")), Updated: true}, nil
}

func invokeTasksDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete task"
	var input eventDeleteArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	etag, err := eventTarget(op, input.ID, input.ETag)
	if err != nil {
		return nil, err
	}
	client, err := openCalendar(ctx, op, resolved, secrets, red, input.Calendar)
	if err != nil {
		return nil, err
	}
	stored, err := client.taskStored(ctx, op, input.Calendar, input.ID, etag)
	if err != nil {
		return nil, err
	}
	if _, err := dav.ParseTask(string(stored), 0); err != nil {
		if errors.Is(err, dav.ErrNoTask) {
			return nil, providerError(op, "the id does not name a task, nothing was changed")
		}
		return nil, invalidResponse(op, "Nextcloud returned a task that is not a valid iCalendar object")
	}
	if _, err := client.objectSend(ctx, op, taskKind, http.MethodDelete, input.Calendar, input.ID, etag, ""); err != nil {
		return nil, err
	}
	return &TaskWriteResult{Calendar: input.Calendar, ID: input.ID, Deleted: true}, nil
}

// taskStored is the pre-check of update and delete: one GET of the task, which must still carry the etag the
// caller names. Nextcloud refuses a change in a read-only calendar with 403 on the write itself.
func (c *Client) taskStored(ctx context.Context, op, calendar, id, etag string) ([]byte, error) {
	data, header, err := c.calendarRequest(ctx, op, http.MethodGet, c.calendarHome(calendar, id), false, "", "",
		http.StatusOK, dav.MaxEventBytes)
	if err != nil {
		return nil, err
	}
	if dav.ETagOf(header.Get("ETag")) != etag {
		return nil, objectPrecondition(op, taskKind)
	}
	return data, nil
}

// requireParentTask is the pre-check of a create with a parent: one REPORT on the bound calendar that must
// find exactly one task with the UID. The answer is checked again, so a server that ignores the filter
// cannot make a foreign task a parent.
func (c *Client) requireParentTask(ctx context.Context, op, calendar, uid string) error {
	base := c.calendarHome(calendar)
	data, _, err := c.calendarRequest(ctx, op, methodReport, base, true, depthChildren,
		dav.TaskParentQueryBody(uid), http.StatusMultiStatus, dav.MaxResponseBytes)
	if err != nil {
		return err
	}
	resources, err := c.davServer().ParseMultiStatus(op, data)
	if err != nil {
		return err
	}
	found := 0
	for i := range resources {
		if id, ok := c.childOf(op, resources[i].Href, base); !ok || !checkEventID(id) || !resources[i].Read {
			continue
		}
		if task, err := dav.ParseTask(resources[i].Text[dav.KeyCalendar], 0); err == nil && task.UID == uid {
			found++
		}
	}
	if found != 1 {
		return providerError(op, "parent_uid must name exactly one task of this calendar, nothing was changed")
	}
	return nil
}
