package todoist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The reminder read and changes. A reminder belongs to a task: on a project connection the task, or for an
// existing reminder the reminder and then its task, is read first, and a task outside the connection ends
// the request before anything is sent. Only time-based reminders are offered, a relative one some minutes
// before the task is due or an absolute one at a date and time; location reminders are not. Whether the
// account's plan includes reminders is Todoist's decision, which reaches the caller as a permission failure
// with a message of its own.

// maxMinuteOffset bounds how long before the due time a relative reminder may fire: 30 days.
const maxMinuteOffset = 43200

const (
	reminderIDSchema = `{"type":"object","properties":{"reminder_id":` + idSchema + `},"required":["reminder_id"],` +
		`"additionalProperties":false}`
	minuteOffsetSchema  = `{"type":"integer","minimum":0,"maximum":43200}`
	reminderTimingInput = `"minute_offset":` + minuteOffsetSchema + `,"due_string":` + dueStringSchema + `,` +
		`"due_lang":` + langSchema + `,"due_datetime":` + instantSchema + `,"is_urgent":` + flagSchema
)

var reminderIDArgument = capability.Argument{Name: "reminder_id", Description: "Reminder ID, as returned by " +
	"todoist.reminders.list", Required: true}

var reminderTimingArguments = []capability.Argument{
	{Name: "minute_offset", Description: "Relative reminder: minutes before the task is due, 0 through 43200; " +
		"the task needs a due time"},
	{Name: "due_string", Description: "Absolute reminder as Todoist reads it, such as \"tomorrow 9am\""},
	{Name: "due_lang", Description: "Two-letter language of due_string; only together with due_string"},
	{Name: "due_datetime", Description: "Absolute reminder as RFC 3339, such as 2026-09-30T09:00:00Z"},
	{Name: "is_urgent", Description: "True makes the reminder an urgent one"},
}

var reminderFields = []capability.Field{
	{Name: "id", Description: "Reminder ID"},
	{Name: "task_id", Description: "Task the reminder belongs to"},
	{Name: "type", Description: "relative or absolute"},
}

var remindersGet = descriptor("reminders", "get", "Get a Todoist reminder",
	"Read one time-based reminder of a task of an explicit Todoist connection. Reminders depend on the "+
		"account's plan",
	[]string{"reminders", "get"},
	reminderIDSchema, json.RawMessage(reminderSchema), []capability.Argument{reminderIDArgument},
	reminderFields,
	[]capability.Example{{Description: "Read one reminder", Arguments: json.RawMessage(`{"reminder_id":"6X7VrXrqjX6642cv"}`)}})

var remindersCreate = changing(capability.EffectCreate, capability.IdempotencyNonIdempotent, descriptor("reminders",
	"create", "Create a Todoist reminder",
	"Add one time-based reminder to one task of an explicit Todoist connection, either minute_offset before "+
		"the task is due or at due_string or due_datetime; reminders depend on the account's plan, and a "+
		"repeated call adds a second reminder"+retryNote,
	[]string{"reminders", "create"},
	`{"type":"object","properties":{"task_id":`+idSchema+`,`+reminderTimingInput+`},"required":["task_id"],`+
		`"additionalProperties":false}`,
	json.RawMessage(reminderSchema),
	append([]capability.Argument{{Name: "task_id", Description: "Task of the connection the reminder belongs to",
		Required: true}}, reminderTimingArguments...),
	reminderFields,
	[]capability.Example{{Description: "Remind 30 minutes before a task is due",
		Arguments: json.RawMessage(`{"task_id":"6X7rM8997g3RQmvh","minute_offset":30}`)}}))

var remindersUpdate = changing(capability.EffectUpdate, capability.IdempotencyIdempotent, descriptor("reminders",
	"update", "Update a Todoist reminder",
	"Change the time or the urgency of one reminder of a task of an explicit Todoist connection; a relative "+
		"reminder takes minute_offset, an absolute one due_string or due_datetime",
	[]string{"reminders", "update"},
	`{"type":"object","properties":{"reminder_id":`+idSchema+`,`+reminderTimingInput+`},"required":["reminder_id"],`+
		`"additionalProperties":false}`,
	json.RawMessage(reminderSchema),
	append([]capability.Argument{reminderIDArgument}, reminderTimingArguments...),
	reminderFields,
	[]capability.Example{{Description: "Move a relative reminder to one hour before",
		Arguments: json.RawMessage(`{"reminder_id":"6X7VrXrqjX6642cv","minute_offset":60}`)}}))

var remindersDelete = changing(capability.EffectDelete, capability.IdempotencyIdempotent, descriptor("reminders",
	"delete", "Delete a Todoist reminder",
	"Delete one reminder of a task of an explicit Todoist connection permanently"+retryNote,
	[]string{"reminders", "delete"},
	reminderIDSchema, changeOutput("deleted"), []capability.Argument{reminderIDArgument}, structureFields,
	[]capability.Example{{Description: "Delete a reminder", Arguments: json.RawMessage(`{"reminder_id":"6X7VrXrqjX6642cv"}`)}}))

// reminderTools are the reminder changes the tasks and organize profiles tick. The delete stays unticked.
var reminderTools = []string{remindersCreate.ID, remindersUpdate.ID}

// reminderTiming is when a reminder fires and whether it is urgent. At most one time is named.
type reminderTiming struct {
	MinuteOffset *int   `json:"minute_offset"`
	DueString    string `json:"due_string"`
	DueLang      string `json:"due_lang"`
	DueDatetime  string `json:"due_datetime"`
	IsUrgent     *bool  `json:"is_urgent"`
}

// times counts the times that are named.
func (t reminderTiming) times() int {
	count := 0
	if t.MinuteOffset != nil {
		count++
	}
	if t.DueString != "" {
		count++
	}
	if t.DueDatetime != "" {
		count++
	}
	return count
}

// body checks the timing and returns the Todoist request body of its set values. No error quotes a value.
func (t reminderTiming) body() (map[string]any, error) {
	switch {
	case t.times() > 1:
		return nil, invalidRequest("name at most one of minute_offset, due_string, and due_datetime")
	case t.MinuteOffset != nil && (*t.MinuteOffset < 0 || *t.MinuteOffset > maxMinuteOffset):
		return nil, invalidRequest("minute_offset must be 0 through 43200")
	case t.DueString != "" && !validText(t.DueString, maxDueString):
		return nil, invalidRequest("due_string must be 1 to 200 characters without control characters or padding")
	case t.DueLang != "" && (t.DueString == "" || !validLang(t.DueLang)):
		return nil, invalidRequest("due_lang must be a two-letter language code and comes only with due_string")
	}
	body := map[string]any{}
	switch {
	case t.MinuteOffset != nil:
		body["minute_offset"] = *t.MinuteOffset
	case t.DueString != "":
		due := map[string]any{"string": t.DueString}
		if t.DueLang != "" {
			due["lang"] = t.DueLang
		}
		body["due"] = due
	case t.DueDatetime != "":
		date, _, err := parseInstant("due_datetime", t.DueDatetime)
		if err != nil {
			return nil, err
		}
		body["due"] = map[string]any{"date": date}
	}
	if t.IsUrgent != nil {
		body["is_urgent"] = *t.IsUrgent
	}
	return body, nil
}

// parentTask returns the task a reminder names.
func parentTask(raw rawReminder) string {
	if raw.TaskID != "" {
		return raw.TaskID
	}
	return raw.ItemID
}

// readReminder reads one reminder and, on a project connection, its task, and refuses a reminder of a task
// outside the connection. It returns the reminder and its task.
func (c *Client) readReminder(ctx context.Context, op, id string) (rawReminder, string, error) {
	var raw rawReminder
	if err := c.get(ctx, op, "/reminders/"+url.PathEscape(id), nil, &raw); err != nil {
		return rawReminder{}, "", err
	}
	if raw.ID == "" {
		return rawReminder{}, "", invalidResponse(op)
	}
	if raw.IsDeleted {
		return rawReminder{}, "", &provider.Error{Class: provider.ClassProviderError, Op: op, Message: notFoundMessage}
	}
	task := parentTask(raw)
	if c.scope.all {
		return raw, task, nil
	}
	if !validID(task) {
		return rawReminder{}, "", invalidResponse(op)
	}
	if _, err := c.readTask(ctx, op, task); err != nil {
		return rawReminder{}, "", err
	}
	return raw, task, nil
}

// changedReminder checks the reminder a change answered with: it must name the task it was made for.
func changedReminder(op string, raw rawReminder, want, task string) (*Reminder, error) {
	if raw.ID == "" || (want != "" && raw.ID != want) {
		return nil, invalidChange(op, true)
	}
	parent := parentTask(raw)
	if parent == "" {
		parent = task
	}
	if task != "" && parent != task {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "Todoist answered with a reminder of another task" + uncertain}
	}
	reminder := reminderOf(raw, parent)
	return &reminder, nil
}

func prepareRemindersGet(_ scope, arguments struct {
	ReminderID string `json:"reminder_id"`
}) (run, error) {
	if err := checkRequiredID("reminder_id", arguments.ReminderID); err != nil {
		return nil, err
	}
	return func(ctx context.Context, c *Client) (any, error) {
		raw, task, err := c.readReminder(ctx, "get reminder", arguments.ReminderID)
		if err != nil {
			return nil, err
		}
		reminder := reminderOf(raw, task)
		return &reminder, nil
	}, nil
}

func prepareRemindersCreate(_ scope, arguments struct {
	TaskID string `json:"task_id"`
	reminderTiming
}) (run, error) {
	const op = "create reminder"
	if err := checkRequiredID("task_id", arguments.TaskID); err != nil {
		return nil, err
	}
	if arguments.times() != 1 {
		return nil, invalidRequest("name exactly one of minute_offset, due_string, and due_datetime")
	}
	body, err := arguments.body()
	if err != nil {
		return nil, err
	}
	body["task_id"] = arguments.TaskID
	body["reminder_type"] = "absolute"
	if arguments.MinuteOffset != nil {
		body["reminder_type"] = "relative"
	}
	return func(ctx context.Context, c *Client) (any, error) {
		if err := c.taskInScope(ctx, op, arguments.TaskID); err != nil {
			return nil, err
		}
		var raw rawReminder
		if err := c.change(ctx, op, http.MethodPost, "/reminders", body, &raw); err != nil {
			return nil, err
		}
		return changedReminder(op, raw, "", arguments.TaskID)
	}, nil
}

func prepareRemindersUpdate(_ scope, arguments struct {
	ReminderID string `json:"reminder_id"`
	reminderTiming
}) (run, error) {
	const op = "update reminder"
	if err := checkRequiredID("reminder_id", arguments.ReminderID); err != nil {
		return nil, err
	}
	body, err := arguments.body()
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, invalidRequest("name at least one value to change")
	}
	return func(ctx context.Context, c *Client) (any, error) {
		task := ""
		if !c.scope.all {
			var err error
			if _, task, err = c.readReminder(ctx, op, arguments.ReminderID); err != nil {
				return nil, err
			}
		}
		var raw rawReminder
		if err := c.change(ctx, op, http.MethodPost, "/reminders/"+url.PathEscape(arguments.ReminderID), body,
			&raw); err != nil {
			return nil, err
		}
		return changedReminder(op, raw, arguments.ReminderID, task)
	}, nil
}

func prepareRemindersDelete(_ scope, arguments struct {
	ReminderID string `json:"reminder_id"`
}) (run, error) {
	const op = "delete reminder"
	if err := checkRequiredID("reminder_id", arguments.ReminderID); err != nil {
		return nil, err
	}
	return func(ctx context.Context, c *Client) (any, error) {
		if !c.scope.all {
			if _, _, err := c.readReminder(ctx, op, arguments.ReminderID); err != nil {
				return nil, err
			}
		}
		if err := c.change(ctx, op, http.MethodDelete, "/reminders/"+url.PathEscape(arguments.ReminderID), nil,
			nil); err != nil {
			return nil, err
		}
		return &Change{ID: arguments.ReminderID, Result: "deleted"}, nil
	}, nil
}
