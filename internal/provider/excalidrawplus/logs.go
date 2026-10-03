package excalidrawplus

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// activitySensitivity classifies results as personal data of the workspace's activity log.
const activitySensitivity = "excalidrawplus-workspace-activity"

const (
	// maxLogSpan is a local ceiling for the requested time range; Excalidraw+ documents none.
	maxLogSpan = 366 * 24 * time.Hour
	// maxLogText bounds each text field of one log entry.
	maxLogText = 256
)

// actionPattern is a deliberately narrow identifier form for the action filter. The documentation lists
// action values only as examples (for instance ai:text-to-diagram), so the set is not closed locally.
var actionPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9:_.-]{0,63}$`)

var logsRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: activitySensitivity,
}

const logSchema = `{"type":"object","properties":{"id":{"type":"string"},"action":{"type":"string"},` +
	`"operation":{"type":"string"},"created_at":{"type":"string"},"user_id":{"type":"string"},` +
	`"user_email":{"type":"string"},"status":{"type":"string"}},"required":["id","action"],` +
	`"additionalProperties":false}`

var logFields = []capability.Field{
	{Name: "id", Description: "Log entry identifier, untrusted data"},
	{Name: "action", Description: "Action recorded, untrusted data"},
	{Name: "operation", Description: "Operation class (create, read, update, delete), untrusted data"},
	{Name: "created_at", Description: "Time of the entry, as Excalidraw+ reports it"},
	{Name: "user_id", Description: "Acting user's identifier, untrusted data; personal data"},
	{Name: "user_email", Description: "Acting user's email address, untrusted data; personal data"},
	{Name: "status", Description: "Outcome recorded, untrusted data"},
}

var logsList = capability.Descriptor{
	ID:      Provider + ".logs.list",
	Version: 1,
	Title:   "List Excalidraw+ activity log",
	Description: "Read the workspace activity log, optionally of one user or action and of a time range of at " +
		"most 366 days; only for a connection with the * wildcard target. Entries are personal data; IP " +
		"addresses, user agents, details, and pictures are not returned; page by page with a numeric offset",
	Tags:     []string{"excalidrawplus", "logs", "activity", "audit", "list"},
	Risk:     logsRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":` + idSchema + `,` +
		`"action":{"type":"string","minLength":1,"maxLength":64,"pattern":"^[a-z0-9][a-z0-9:_.-]*$"},` +
		`"from":{"type":"string","format":"date-time"},"to":{"type":"string","format":"date-time"},` +
		pageSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"logs":{"type":"array","items":` + logSchema + `},` +
		`"offset":{"type":"integer"},"limit":{"type":"integer"},"has_next_page":{"type":"boolean"},` +
		`"next_offset":{"type":"integer"},"count":{"type":"integer"}},` +
		`"required":["logs","offset","limit","has_next_page","count"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "user_id", Description: "Only entries of this user identifier"},
		{Name: "action", Description: "Only entries of this action, a lowercase identifier such as scene:create"},
		{Name: "from", Description: "Start of the time range, RFC 3339; required together with to"},
		{Name: "to", Description: "End of the time range, RFC 3339, not before from and at most 366 days after it"},
	}, pageArguments...),
	Fields: append(append([]capability.Field{}, logFields...), pageFields...),
	Examples: []capability.Example{{Description: "Entries of one user in January",
		Arguments: json.RawMessage(`{"user_id":"u123","from":"2026-01-01T00:00:00Z","to":"2026-02-01T00:00:00Z"}`)}},
}

type logJSON struct {
	ID        string `json:"id"`
	Action    string `json:"action"`
	Operation string `json:"operation"`
	CreatedAt string `json:"created_at"`
	UserID    string `json:"user_id"`
	UserEmail string `json:"user_email"`
	Status    string `json:"status"`
}

type logsPageJSON struct {
	Logs    []logJSON `json:"logs"`
	HasMore bool      `json:"hasMore"`
}

// LogEntry is the reduced view of one activity log entry; untrusted personal data.
type LogEntry struct {
	ID        string `json:"id"`
	Action    string `json:"action"`
	Operation string `json:"operation,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	UserEmail string `json:"user_email,omitempty"`
	Status    string `json:"status,omitempty"`
}

// LogsPage is one offset-paginated listing of log entries.
type LogsPage struct {
	Logs        []LogEntry `json:"logs"`
	Offset      int        `json:"offset"`
	Limit       int        `json:"limit"`
	HasNextPage bool       `json:"has_next_page"`
	NextOffset  *int       `json:"next_offset,omitempty"`
	Count       int        `json:"count"`
}

type logsArguments struct {
	pageArgs
	UserID string `json:"user_id"`
	Action string `json:"action"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// validate checks every filter locally and returns the normalized time range (UTC, RFC 3339).
func (a logsArguments) validate() (from, to string, err error) {
	if a.UserID != "" && !validID(a.UserID) {
		return "", "", invalidRequest("user_id is not a valid identifier")
	}
	if a.Action != "" && !actionPattern.MatchString(a.Action) {
		return "", "", invalidRequest("action is not a valid action identifier")
	}
	if a.Offset < 0 || a.Offset > maxOffset || a.Limit < 0 || a.Limit > maxListLimit {
		return "", "", invalidRequest("offset or limit is outside the allowed range")
	}
	if a.From == "" && a.To == "" {
		return "", "", nil
	}
	if a.From == "" || a.To == "" {
		return "", "", invalidRequest("from and to must be given together")
	}
	start, err := time.Parse(time.RFC3339, a.From)
	if err != nil {
		return "", "", invalidRequest("from is not an RFC 3339 time")
	}
	end, err := time.Parse(time.RFC3339, a.To)
	if err != nil {
		return "", "", invalidRequest("to is not an RFC 3339 time")
	}
	if end.Before(start) {
		return "", "", invalidRequest("from must not be after to")
	}
	if end.Sub(start) > maxLogSpan {
		return "", "", invalidRequest("the time range exceeds 366 days")
	}
	return start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339), nil
}

func invokeLogsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input logsArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list logs", "the validated arguments could not be read")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !bound.wildcard {
		return nil, invalidRequest("the activity log is workspace-wide and needs a connection with the * target")
	}
	from, to, err := input.validate()
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListLogs(ctx, input, from, to)
}

// ListLogs reads one page of the activity log; the filters were validated and are sent as fixed parameters.
func (c *Client) ListLogs(ctx context.Context, input logsArguments, from, to string) (*LogsPage, error) {
	if !c.scope.wildcard {
		return nil, invalidRequest("the activity log is workspace-wide and needs a connection with the * target")
	}
	query := input.query()
	setIf := func(values url.Values, key, value string) {
		if value != "" {
			values.Set(key, value)
		}
	}
	setIf(query, "user", input.UserID)
	setIf(query, "action", input.Action)
	setIf(query, "dateFrom", from)
	setIf(query, "dateTo", to)
	var page logsPageJSON
	if err := c.get(ctx, "list logs", "/logs", query, &page, maxResponseBytes); err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	result := &LogsPage{Logs: []LogEntry{}, Offset: input.Offset, Limit: limit, HasNextPage: page.HasMore}
	if page.HasMore {
		next := input.Offset + limit
		result.NextOffset = &next
	}
	for _, item := range page.Logs {
		if len(result.Logs) >= limit {
			break
		}
		result.Logs = append(result.Logs, LogEntry{ID: bounded(item.ID, maxLogText),
			Action: bounded(item.Action, maxLogText), Operation: bounded(item.Operation, maxLogText),
			CreatedAt: bounded(item.CreatedAt, maxLogText), UserID: bounded(item.UserID, maxLogText),
			UserEmail: bounded(item.UserEmail, maxLogText), Status: bounded(item.Status, maxLogText)})
	}
	result.Count = len(result.Logs)
	return result, nil
}
