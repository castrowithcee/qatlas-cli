package bookstack

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	auditLogInstanceWide = "audit log"

	auditLogRoles = "BookStack requires the role permissions settings-manage and users-manage for the token's user. " +
		"The audit log covers the whole instance and contains IP addresses and sign-in events of all users, so a " +
		"connection bound to books cannot use this tool, and a tool allow list entry is required"

	// auditDateTime is the form in which dates are sent to BookStack, in UTC.
	auditDateTime = "2006-01-02 15:04:05"
)

var (
	auditTypePattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	// auditLoggableTypes is the fixed list of object types the audit log can be filtered by.
	auditLoggableTypes = []string{"page", "chapter", "book", "bookshelf"}
)

var auditLogList = capability.Descriptor{
	ID: Provider + ".auditlog.list", Version: 1, Title: "List the BookStack audit log",
	Description: "List the activity entries of the instance, optionally filtered by type, user, object, and time. " +
		"Dates are UTC; a date without a time means the start of that day. " + auditLogRoles,
	Tags: []string{"administration", "auditlog", "bookstack"}, Risk: peopleReadRisk, Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0},` +
		`"type":{"type":"string","pattern":"^[a-z0-9_]{1,64}$"},"user_id":{"type":"integer","minimum":1},` +
		`"loggable_type":{"type":"string","enum":["page","chapter","book","bookshelf"]},"loggable_id":{"type":"integer","minimum":1},` +
		`"created_after":{"type":"string","maxLength":40},"created_before":{"type":"string","maxLength":40}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"type":{"type":"string"},"detail":{"type":"string"},"user_id":{"type":"integer"},"user_name":{"type":"string"},"loggable_type":{"type":"string"},"loggable_id":{"type":"integer"},"ip":{"type":"string"},"created_at":{"type":"string"}},"required":["id","type","created_at"]}}`),
	Arguments: []capability.Argument{
		{Name: "limit", Description: "Maximum number of entries to return; 0 returns all"},
		{Name: "offset", Description: "Number of matching entries to skip"},
		{Name: "type", Description: "Only entries of this activity type, lowercase letters, digits, and underscores, at most 64 characters"},
		{Name: "user_id", Description: "Only entries of this user"},
		{Name: "loggable_type", Description: "Only entries about this object type: page, chapter, book, or bookshelf"},
		{Name: "loggable_id", Description: "Only entries about this object"},
		{Name: "created_after", Description: "Only entries at or after this time, YYYY-MM-DD or RFC 3339"},
		{Name: "created_before", Description: "Only entries at or before this time, YYYY-MM-DD or RFC 3339"},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Entry identifier"},
		{Name: "type", Description: "Activity type"},
		{Name: "detail", Description: "Detail text of the entry, untrusted data"},
		{Name: "user_id", Description: "Identifier of the acting user"},
		{Name: "user_name", Description: "Name of the acting user, untrusted data"},
		{Name: "loggable_type", Description: "Type of the object the entry is about"},
		{Name: "loggable_id", Description: "Identifier of the object the entry is about"},
		{Name: "ip", Description: "IP address of the request, personal data"},
		{Name: "created_at", Description: "Time of the entry"},
	},
	Examples: []capability.Example{{Description: "List the sign-in entries of a day",
		Arguments: json.RawMessage(`{"type":"auth_login","created_after":"2026-10-01","created_before":"2026-10-02","limit":50}`)}},
}

type auditEntryJSON struct {
	ID           int64  `json:"id"`
	Type         string `json:"type"`
	Detail       string `json:"detail"`
	UserID       *int64 `json:"user_id"`
	LoggableID   *int64 `json:"loggable_id"`
	LoggableType string `json:"loggable_type"`
	IP           string `json:"ip"`
	CreatedAt    string `json:"created_at"`
	User         *struct {
		Name string `json:"name"`
	} `json:"user"`
}

// auditFilter is the validated filter of one audit log read.
type auditFilter struct {
	Type         string
	UserID       int64
	LoggableType string
	LoggableID   int64
	After        time.Time
	Before       time.Time
}

func (f auditFilter) active() bool {
	return f.Type != "" || f.UserID != 0 || f.LoggableType != "" || f.LoggableID != 0 || !f.After.IsZero() || !f.Before.IsZero()
}

// query maps the filter to the fixed BookStack filter syntax.
func (f auditFilter) query() url.Values {
	q := url.Values{}
	if f.Type != "" {
		q.Set("filter[type]", f.Type)
	}
	if f.UserID != 0 {
		q.Set("filter[user_id]", strconv.FormatInt(f.UserID, 10))
	}
	if f.LoggableType != "" {
		q.Set("filter[loggable_type]", f.LoggableType)
	}
	if f.LoggableID != 0 {
		q.Set("filter[loggable_id]", strconv.FormatInt(f.LoggableID, 10))
	}
	if !f.After.IsZero() {
		q.Set("filter[created_at:gte]", f.After.UTC().Format(auditDateTime))
	}
	if !f.Before.IsZero() {
		q.Set("filter[created_at:lte]", f.Before.UTC().Format(auditDateTime))
	}
	return q
}

// keep checks one row against every set filter, because BookStack ignores filters it does not know.
func (f auditFilter) keep(e auditEntryJSON) bool {
	if f.Type != "" && e.Type != f.Type {
		return false
	}
	if f.UserID != 0 && (e.UserID == nil || *e.UserID != f.UserID) {
		return false
	}
	if f.LoggableType != "" && e.LoggableType != f.LoggableType {
		return false
	}
	if f.LoggableID != 0 && (e.LoggableID == nil || *e.LoggableID != f.LoggableID) {
		return false
	}
	if f.After.IsZero() && f.Before.IsZero() {
		return true
	}
	created, err := time.Parse(time.RFC3339Nano, e.CreatedAt)
	if err != nil {
		return false
	}
	return (f.After.IsZero() || !created.Before(f.After)) && (f.Before.IsZero() || !created.After(f.Before))
}

func parseAuditTime(name, value string) (time.Time, error) {
	if t, err := time.Parse(time.DateOnly, value); err == nil {
		return t, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, invalidRequest(name + " must be a date YYYY-MM-DD or an RFC 3339 time")
	}
	return t, nil
}

func parseAuditFilter(raw json.RawMessage) (auditFilter, int, int, error) {
	var in struct {
		Limit        int     `json:"limit"`
		Offset       int     `json:"offset"`
		Type         *string `json:"type"`
		UserID       *int64  `json:"user_id"`
		LoggableType *string `json:"loggable_type"`
		LoggableID   *int64  `json:"loggable_id"`
		After        *string `json:"created_after"`
		Before       *string `json:"created_before"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return auditFilter{}, 0, 0, invalidRequest("the arguments could not be read")
	}
	var f auditFilter
	if in.Limit < 0 || in.Offset < 0 {
		return f, 0, 0, invalidRequest("limit and offset must not be negative")
	}
	if in.Type != nil {
		if !auditTypePattern.MatchString(*in.Type) {
			return f, 0, 0, invalidRequest("type must use lowercase letters, digits, and underscores, at most 64 characters")
		}
		f.Type = *in.Type
	}
	if in.UserID != nil {
		if *in.UserID <= 0 {
			return f, 0, 0, invalidRequest("user_id must be a positive integer")
		}
		f.UserID = *in.UserID
	}
	if in.LoggableType != nil {
		if !slices.Contains(auditLoggableTypes, *in.LoggableType) {
			return f, 0, 0, invalidRequest("loggable_type must be page, chapter, book, or bookshelf")
		}
		f.LoggableType = *in.LoggableType
	}
	if in.LoggableID != nil {
		if *in.LoggableID <= 0 {
			return f, 0, 0, invalidRequest("loggable_id must be a positive integer")
		}
		f.LoggableID = *in.LoggableID
	}
	var err error
	if in.After != nil {
		if f.After, err = parseAuditTime("created_after", *in.After); err != nil {
			return f, 0, 0, err
		}
	}
	if in.Before != nil {
		if f.Before, err = parseAuditTime("created_before", *in.Before); err != nil {
			return f, 0, 0, err
		}
	}
	return f, in.Limit, in.Offset, nil
}

func invokeAuditLogList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	filter, limit, offset, err := parseAuditFilter(raw)
	if err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, auditLogInstanceWide); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListAuditLog(ctx, filter, limit, offset)
}

// ListAuditLog returns the reduced entries that match the filter.
func (c *Client) ListAuditLog(ctx context.Context, filter auditFilter, limit, offset int) (output.Collection, error) {
	rows, err := scanList(ctx, c, scanSpec[auditEntryJSON]{
		op: "list audit log", path: "/api/audit-log", query: filter.query(), filtered: filter.active(),
		narrow: "type, user_id, or created_after and created_before", limit: limit, offset: offset,
		arguments: argNames(auditLogList),
		id:        func(e auditEntryJSON) int64 { return e.ID },
		keep:      filter.keep,
		row: func(e auditEntryJSON) output.Row {
			row := output.Row{
				"id": e.ID, "type": clip(e.Type, maxResultString), "detail": clip(e.Detail, maxResultString),
				"loggable_type": clip(e.LoggableType, maxResultString), "ip": clip(e.IP, maxResultString),
				"created_at": clip(e.CreatedAt, maxResultString),
			}
			if e.UserID != nil {
				row["user_id"] = *e.UserID
			}
			if e.User != nil {
				row["user_name"] = clip(e.User.Name, maxResultString)
			}
			if e.LoggableID != nil {
				row["loggable_id"] = *e.LoggableID
			}
			return row
		},
	})
	if err != nil {
		return output.Collection{}, err
	}
	return output.Collection{Columns: fieldNames(auditLogList), Rows: rows}, nil
}
