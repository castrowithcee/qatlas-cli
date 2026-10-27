package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The cron tools list the scheduled tasks of the instance and start one now. Like the other instance
// administration they reach past any target, so only a connection without targets offers them.

const adminCronSensitivity = "-admin-cron"

const maxCronName = 64

var cronMembers = []member{
	{name: "name", kind: kindText, note: "Name of the cron task, untrusted data"},
	{name: "schedule", kind: kindText, note: "Schedule expression, untrusted data"},
	{name: "next", kind: kindText, note: "Next scheduled execution time"},
	{name: "prev", kind: kindText, note: "Previous execution time"},
	{name: "exec_times", kind: kindInteger, note: "Number of executions so far"},
}

// cronName reads the narrow form of a task name: lower-case letters, digits, _ and -, starting with a letter or digit.
func cronName(value string) error {
	ok := value != "" && len(value) <= maxCronName
	for i := 0; ok && i < len(value); i++ {
		c := value[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		ok = alnum || (i > 0 && (c == '_' || c == '-'))
	}
	if !ok {
		return invalidArgument("task must be 1 to " + itoa(maxCronName) +
			" characters: lower-case letters, digits, _ and -, starting with a letter or digit")
	}
	return nil
}

var adminCronList = &tool{
	group: "admin-cron", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the cron tasks of " + p.name,
			Description: "List one batch of the scheduled cron tasks of the whole instance as an administrator, with " +
				"every member of the cron object; names and schedules are untrusted data. " + adminUserNote,
			Tags:         []string{p.id, "admin", "cron", "list"},
			Risk:         readRisk(p.id + adminCronSensitivity),
			InputSchema:  inputSchema(pagingKeys),
			OutputSchema: instanceListOutput("tasks", schemaOf(p, cronMembers)),
			Arguments:    append([]capability.Argument(nil), pagingArguments...),
			Fields:       instanceListFields("tasks", "Cron tasks with every member of the cron object"),
			Examples:     []capability.Example{{Description: "List a batch", Arguments: json.RawMessage(`{"limit":20}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.instanceWide(); err != nil {
			return nil, err
		}
		var a struct {
			Limit  int    `json:"limit"`
			Cursor string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		const path = "/admin/cron"
		page, err := startPaging(provider.CursorBinding(p.id, "admin-cron.list", "", path, ""), a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			return instancePage(ctx, c, "list cron tasks", path, url.Values{}, "tasks", "", page,
				func(row object) map[string]any { return project(p, cronMembers, row) })
		}, nil
	},
}

var adminCronRun = &tool{
	group: "admin-cron", action: "run", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Run a " + p.name + " cron task now",
			Description: "Start one cron task of the instance now by its name, as an administrator. Not idempotent; the " +
				"request is sent once and never repeated after an unclear outcome. " + adminUserNote,
			Tags:        []string{p.id, "admin", "cron", "run", "execute"},
			Risk:        changeRisk(capability.EffectExecute, capability.IdempotencyNonIdempotent, p.id+adminCronSensitivity),
			InputSchema: inputSchema(`"task":{"type":"string","minLength":1,"maxLength":`+itoa(maxCronName)+`}`, "task"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"task":{"type":"string"},` +
				`"started":{"type":"boolean"}},"required":["task","started"],"additionalProperties":false}`),
			Arguments: []capability.Argument{{Name: "task", Required: true,
				Description: "Name of the cron task, as admin-cron.list reports it as name"}},
			Fields: []capability.Field{{Name: "task", Description: "Name of the cron task"},
				{Name: "started", Description: "True once the instance accepted the request"}},
			Examples: []capability.Example{{Description: "Run a task",
				Arguments: json.RawMessage(`{"task":"archive_cleanup"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		if err := allowed.instanceWide(); err != nil {
			return nil, err
		}
		var a struct {
			Task string `json:"task"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := cronName(a.Task); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "run cron task", http.MethodPost, "/admin/cron/"+escape(a.Task), nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"task": a.Task, "started": true}, nil
		}, nil
	},
}
