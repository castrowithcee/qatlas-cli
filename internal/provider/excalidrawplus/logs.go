package excalidrawplus

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
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
	`"user_email":{"type":"string"},"status":{"type":"integer"},"details":{},"details_omitted":{"type":"boolean"},` +
	`"ip_address":{"type":"string"},"workspace_id":{"type":"string"},"app_source":{"type":"string"},` +
	`"source_type":{"type":"string"},"source_id":{"type":"string"},"user_full_name":{"type":"string"}},` +
	`"required":["id","action"],` +
	`"additionalProperties":false}`

var logFields = []capability.Field{
	{Name: "id", Description: "Log entry identifier, untrusted data"},
	{Name: "action", Description: "Action recorded, untrusted data"},
	{Name: "operation", Description: "Operation class (create, read, update, delete), untrusted data"},
	{Name: "created_at", Description: "Time of the entry, as Excalidraw+ reports it"},
	{Name: "user_id", Description: "Acting user's identifier, untrusted data; personal data"},
	{Name: "user_email", Description: "Acting user's email address, untrusted data; personal data"},
	{Name: "status", Description: "Outcome code recorded as an integer, untrusted data"},
	{Name: "details", Description: "Free text or object of the entry, untrusted data; an object beyond the " +
		"depth, size, or length limits is left out and marked by details_omitted"},
	{Name: "details_omitted", Description: "True when the details were left out because they exceed the limits"},
	{Name: "ip_address", Description: "Address the action came from, untrusted data; personal data"},
	{Name: "workspace_id", Description: "Workspace identifier, untrusted data"},
	{Name: "app_source", Description: "Application the action came from, untrusted data"},
	{Name: "source_type", Description: "Kind of the acting source, untrusted data"},
	{Name: "source_id", Description: "Identifier of the acting source, untrusted data"},
	{Name: "user_full_name", Description: "Acting user's full name, untrusted data; personal data"},
	{Name: "total_count", Description: "Number of entries matching the filters, as Excalidraw+ reports it"},
	{Name: "total_pages", Description: "Number of pages as Excalidraw+ counts them"},
	{Name: "current_page", Description: "Page number as Excalidraw+ counts it"},
	{Name: "available_actions", Description: "Action values Excalidraw+ reports as available, capped, untrusted data"},
}

var logsList = capability.Descriptor{
	ID:      Provider + ".logs.list",
	Version: 2,
	Title:   "List Excalidraw+ activity log",
	Description: "Read the workspace activity log, optionally of one user or action and of a time range of at " +
		"most 366 days; only for a connection with the * wildcard target. Entries are personal data, including IP " +
		"addresses; pictures are not returned; page by page with a numeric offset",
	Tags:     []string{"excalidrawplus", "logs", "activity", "audit", "list"},
	Risk:     logsRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":` + idSchema + `,` +
		`"action":{"type":"string","minLength":1,"maxLength":64,"pattern":"^[a-z0-9][a-z0-9:_.-]*$"},` +
		`"operation":{"type":"string","enum":["create","read","update","delete"]},` +
		`"from":{"type":"string","format":"date-time"},"to":{"type":"string","format":"date-time"},` +
		pageSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"logs":{"type":"array","items":` + logSchema + `},` +
		`"offset":{"type":"integer"},"limit":{"type":"integer"},"has_next_page":{"type":"boolean"},` +
		`"next_offset":{"type":"integer"},"count":{"type":"integer"},"total_count":{"type":"integer"},` +
		`"total_pages":{"type":"integer"},"current_page":{"type":"integer"},` +
		`"available_actions":{"type":"array","items":{"type":"string"}}},` +
		`"required":["logs","offset","limit","has_next_page","count"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "user_id", Description: "Only entries of this user identifier"},
		{Name: "action", Description: "Only entries of this action, a lowercase identifier such as scene:create"},
		{Name: "operation", Description: "Only entries of this operation: create, read, update, or delete"},
		{Name: "from", Description: "Start of the time range, RFC 3339; usable alone"},
		{Name: "to", Description: "End of the time range, RFC 3339; usable alone. With both, from is not after " +
			"to and the range is at most 366 days"},
	}, pageArguments...),
	Fields: append(append([]capability.Field{}, logFields...), pageFields...),
	Examples: []capability.Example{{Description: "Entries of one user in January",
		Arguments: json.RawMessage(`{"user_id":"u123","from":"2026-01-01T00:00:00Z","to":"2026-02-01T00:00:00Z"}`)}},
}

type logJSON struct {
	ID        string          `json:"id"`
	Action    string          `json:"action"`
	Operation string          `json:"operation"`
	CreatedAt string          `json:"created_at"`
	UserID    string          `json:"user_id"`
	UserEmail string          `json:"user_email"`
	Status    json.RawMessage `json:"status"`

	// Later members are read member by member so one unexpected type drops only that member.
	Details     json.RawMessage `json:"details"`
	IPAddress   json.RawMessage `json:"ip_address"`
	WorkspaceID json.RawMessage `json:"workspace_id"`
	AppSource   json.RawMessage `json:"app_source"`
	SourceType  json.RawMessage `json:"source_type"`
	SourceID    json.RawMessage `json:"source_id"`
	UserName    json.RawMessage `json:"user_full_name"`
}

type logsPageJSON struct {
	Logs             []logJSON       `json:"logs"`
	HasMore          bool            `json:"hasMore"`
	TotalCount       json.RawMessage `json:"totalCount"`
	TotalPages       json.RawMessage `json:"totalPages"`
	CurrentPage      json.RawMessage `json:"currentPage"`
	AvailableActions json.RawMessage `json:"availableActions"`
}

// LogEntry is the reduced view of one activity log entry; untrusted personal data.
type LogEntry struct {
	ID        string      `json:"id"`
	Action    string      `json:"action"`
	Operation string      `json:"operation,omitempty"`
	CreatedAt string      `json:"created_at,omitempty"`
	UserID    string      `json:"user_id,omitempty"`
	UserEmail string      `json:"user_email,omitempty"`
	Status    json.Number `json:"status,omitempty"`

	Details        any    `json:"details,omitempty"`
	DetailsOmitted bool   `json:"details_omitted,omitempty"`
	IPAddress      string `json:"ip_address,omitempty"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	AppSource      string `json:"app_source,omitempty"`
	SourceType     string `json:"source_type,omitempty"`
	SourceID       string `json:"source_id,omitempty"`
	UserFullName   string `json:"user_full_name,omitempty"`
}

// LogsPage is one offset-paginated listing of log entries.
type LogsPage struct {
	Logs        []LogEntry `json:"logs"`
	Offset      int        `json:"offset"`
	Limit       int        `json:"limit"`
	HasNextPage bool       `json:"has_next_page"`
	NextOffset  *int       `json:"next_offset,omitempty"`
	Count       int        `json:"count"`

	TotalCount       json.Number `json:"total_count,omitempty"`
	TotalPages       json.Number `json:"total_pages,omitempty"`
	CurrentPage      json.Number `json:"current_page,omitempty"`
	AvailableActions []string    `json:"available_actions,omitempty"`
}

type logsArguments struct {
	pageArgs
	UserID    string `json:"user_id"`
	Action    string `json:"action"`
	Operation string `json:"operation"`
	From      string `json:"from"`
	To        string `json:"to"`
}

// validate checks every filter locally and returns the normalized time range (UTC, RFC 3339).
func (a logsArguments) validate() (from, to string, err error) {
	if a.UserID != "" && !validID(a.UserID) {
		return "", "", invalidRequest("user_id is not a valid identifier")
	}
	if a.Action != "" && !actionPattern.MatchString(a.Action) {
		return "", "", invalidRequest("action is not a valid action identifier")
	}
	if a.Operation != "" && !validOperation(a.Operation) {
		return "", "", invalidRequest("operation must be create, read, update, or delete")
	}
	if a.Offset < 0 || a.Offset > maxOffset || a.Limit < 0 || a.Limit > maxListLimit {
		return "", "", invalidRequest("offset or limit is outside the allowed range")
	}
	var start, end time.Time
	if a.From != "" {
		if start, err = time.Parse(time.RFC3339, a.From); err != nil {
			return "", "", invalidRequest("from is not an RFC 3339 time")
		}
		from = start.UTC().Format(time.RFC3339)
	}
	if a.To != "" {
		if end, err = time.Parse(time.RFC3339, a.To); err != nil {
			return "", "", invalidRequest("to is not an RFC 3339 time")
		}
		to = end.UTC().Format(time.RFC3339)
	}
	if a.From != "" && a.To != "" {
		if end.Before(start) {
			return "", "", invalidRequest("from must not be after to")
		}
		if end.Sub(start) > maxLogSpan {
			return "", "", invalidRequest("the time range exceeds 366 days")
		}
	}
	return from, to, nil
}

func validOperation(operation string) bool {
	switch operation {
	case "create", "read", "update", "delete":
		return true
	}
	return false
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
	if !validOperation(input.Operation) && input.Operation != "" {
		return nil, invalidRequest("operation must be create, read, update, or delete")
	}
	setIf(query, "operation", input.Operation)
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
			UserEmail: bounded(item.UserEmail, maxLogText), Status: logStatus(item.Status)})
		entry := &result.Logs[len(result.Logs)-1]
		entry.Details, entry.DetailsOmitted = detailsOf(item.Details)
		entry.IPAddress, entry.WorkspaceID = logText(item.IPAddress), logText(item.WorkspaceID)
		entry.AppSource, entry.SourceType = logText(item.AppSource), logText(item.SourceType)
		entry.SourceID, entry.UserFullName = logText(item.SourceID), logText(item.UserName)
	}
	result.Count = len(result.Logs)
	result.TotalCount, result.TotalPages = logStatus(page.TotalCount), logStatus(page.TotalPages)
	result.CurrentPage = logStatus(page.CurrentPage)
	var actions []string
	if json.Unmarshal(page.AvailableActions, &actions) == nil {
		for _, action := range actions {
			if value := bounded(cleanValue(action), maxLogText); value != "" && len(result.AvailableActions) < maxAvailableActions {
				result.AvailableActions = append(result.AvailableActions, value)
			}
		}
	}
	return result, nil
}

// logStatus accepts an integer given as a JSON number or as a string of digits. Anything else
// (fractions, exponents, text, null, absence) yields no status instead of an invented one.
func logStatus(raw json.RawMessage) json.Number {
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, `"`) {
		var str string
		if json.Unmarshal(raw, &str) != nil {
			return ""
		}
		text = strings.TrimSpace(str)
	}
	sign := ""
	if strings.HasPrefix(text, "-") {
		sign, text = "-", text[1:]
	}
	if text == "" || len(text) > 18 || (len(text) > 1 && text[0] == '0') || (sign != "" && text == "0") {
		return ""
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return json.Number(sign + text)
}

const (
	// maxAvailableActions caps the action values reported in the page head.
	maxAvailableActions = 100
	// Limits for the free details of one entry. A structure beyond them is left out, not shortened.
	maxDetailsText  = 2048
	maxDetailsDepth = 4
	maxDetailsNodes = 100
)

func logText(raw json.RawMessage) string { return bounded(textOf(raw), maxLogText) }

// detailsOf passes text on bounded and a structure only whole: when it exceeds the depth, node, or length
// limits it is left out and reported as omitted.
func detailsOf(raw json.RawMessage) (value any, omitted bool) {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return nil, false
	}
	if text[0] == '"' {
		if details := bounded(textOf(raw), maxDetailsText); details != "" {
			return details, false
		}
		return nil, false
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var decoded any
	if decoder.Decode(&decoded) != nil {
		return nil, false
	}
	nodes := 0
	cleaned, ok := cleanDetails(decoded, 1, &nodes)
	if !ok {
		return nil, true
	}
	return cleaned, false
}

func cleanDetails(value any, depth int, nodes *int) (any, bool) {
	*nodes++
	if depth > maxDetailsDepth || *nodes > maxDetailsNodes {
		return nil, false
	}
	switch v := value.(type) {
	case string:
		if len(v) > maxLogText {
			return nil, false
		}
		return cleanValue(v), true
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			cleaned, ok := cleanDetails(item, depth+1, nodes)
			if !ok {
				return nil, false
			}
			out = append(out, cleaned)
		}
		return out, true
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if len(key) > maxLogText {
				return nil, false
			}
			cleaned, ok := cleanDetails(item, depth+1, nodes)
			if !ok {
				return nil, false
			}
			out[cleanValue(key)] = cleaned
		}
		return out, true
	default:
		return v, true
	}
}
