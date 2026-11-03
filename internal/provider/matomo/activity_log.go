package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	activityLogPlugin   = "ActivityLog"
	activitySensitive   = usersSensitive
	maxActivityType     = 100
	maxActivityText     = 512
	activityEntriesNote = "Logins and descriptions are personal data and untrusted text, shortened. The answer " +
		"of Matomo is not verified against a reference. Needs the premium plugin ActivityLog and a connection " +
		"whose targets name the instance; Matomo shows Super Users all entries and other users only their own"
)

func activityLogRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: activityLogPlugin}
}

const activityFilterKeys = `"userLogin":{"type":"string","minLength":1,"maxLength":100},` +
	`"activityType":{"type":"string","minLength":1,"maxLength":100},` + periodGroup

var activityFilterArguments = []capability.Argument{
	{Name: "userLogin", Description: "Only entries of this login; 1 to 100 characters without control characters"},
	{Name: "activityType", Description: "Only entries of this activity type as matomo.activity-types.list names it"},
	periodArguments[0], periodArguments[1],
}

var activityLogList = capability.Descriptor{
	ID: Provider + ".activity-log.list", Version: 1, Title: "List Matomo activity log entries",
	Description: "Read the activity log of the instance (ActivityLog.getEntries) in batches of 25 by default and at " +
		"most 100, optionally filtered by login, activity type, and period with date. " + activityEntriesNote,
	Tags: []string{"matomo", "activity-log", "list", "instance", "personal-data"}, Risk: readRisk(activitySensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + activityFilterKeys + `,` + pagingKeys +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"entries":{"type":"array","items":{"type":"object",` +
		`"properties":{"id":{"type":"string"},"dateTime":{"type":"string"},"userLogin":{"type":"string"},` +
		`"activityType":{"type":"string"},"description":{"type":"string"}},"additionalProperties":false}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},"required":["has_more"],"additionalProperties":false}`),
	Arguments: append(append([]capability.Argument{}, activityFilterArguments...), pagingArguments...),
	Fields: append([]capability.Field{{Name: "entries", Description: "Entries in the order Matomo lists them: id, " +
		"dateTime, userLogin, activityType, and description (untrusted); other members of an entry are not returned"}},
		pagingFields...),
	Examples: []capability.Example{{Description: "The latest entries of one login",
		Arguments: json.RawMessage(`{"userLogin":"alice","limit":10}`)}},
}

var activityLogCount = capability.Descriptor{
	ID: Provider + ".activity-log.count", Version: 1, Title: "Count Matomo activity log entries",
	Description: "Count the activity log entries of the instance (ActivityLog.getEntryCount), optionally filtered by " +
		"login, activity type, and period with date. " + activityEntriesNote,
	Tags: []string{"matomo", "activity-log", "count", "instance", "personal-data"}, Risk: readRisk(activitySensitive),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + activityFilterKeys +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer","minimum":0}},` +
		`"required":["count"],"additionalProperties":false}`),
	Arguments: activityFilterArguments,
	Fields:    []capability.Field{{Name: "count", Description: "Number of matching entries"}},
	Examples:  []capability.Example{{Description: "Entries of today", Arguments: json.RawMessage(`{"period":"day","date":"today"}`)}},
}

var activityTypesList = capability.Descriptor{
	ID: Provider + ".activity-types.list", Version: 1, Title: "List Matomo activity types",
	Description: "Read the activity types the log knows (ActivityLog.getAllActivityTypes), in batches of 25 by " +
		"default and at most 100. The types are not fixed: they come from the installed plugins. Type names are " +
		"untrusted. The answer of Matomo is not verified against a reference. Needs the premium plugin " +
		"ActivityLog and a connection whose targets name the instance",
	Tags: []string{"matomo", "activity-log", "activity-types", "list", "instance"}, Risk: readRisk(instanceSensitive),
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + pagingKeys + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"types":{"type":"array","items":{"type":"object",` +
		`"properties":{"id":{"type":"string"},"name":{"type":"string"}},"additionalProperties":false}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},"required":["has_more"],"additionalProperties":false}`),
	Arguments: pagingArguments,
	Fields: append([]capability.Field{{Name: "types", Description: "Activity types: id (the value for activityType) " +
		"and name (untrusted); a type that Matomo gives as a bare text has it as id"}}, pagingFields...),
	Examples: []capability.Example{{Description: "The activity types", Arguments: json.RawMessage(`{}`)}},
}

// ActivityEntry is one entry of the log. All members are untrusted provider data.
type ActivityEntry struct {
	ID           string `json:"id,omitempty"`
	DateTime     string `json:"dateTime,omitempty"`
	UserLogin    string `json:"userLogin,omitempty"`
	ActivityType string `json:"activityType,omitempty"`
	Description  string `json:"description,omitempty"`
}

// ActivityLogResult is the answer of activity-log.list.
type ActivityLogResult struct {
	Entries    []ActivityEntry `json:"entries,omitempty"`
	HasMore    bool            `json:"has_more"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

// ActivityLogCount is the answer of activity-log.count.
type ActivityLogCount struct {
	Count int `json:"count"`
}

// ActivityType is one activity type.
type ActivityType struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// ActivityTypesResult is the answer of activity-types.list.
type ActivityTypesResult struct {
	Types      []ActivityType `json:"types,omitempty"`
	HasMore    bool           `json:"has_more"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

type activityFilter struct {
	UserLogin    string `json:"userLogin"`
	ActivityType string `json:"activityType"`
	Period       string `json:"period"`
	Date         string `json:"date"`
}

// params validates the filters and returns the Matomo parameters for them.
func (f activityFilter) params() (url.Values, error) {
	params, err := periodParams(f.Period, f.Date)
	if err != nil {
		return nil, err
	}
	if f.UserLogin != "" {
		if !validText(f.UserLogin, maxLoginLength) {
			return nil, invalidArgument("userLogin must hold 1 to 100 characters without control characters")
		}
		params.Set("filterByUserLogin", f.UserLogin)
	}
	if f.ActivityType != "" {
		if !validText(f.ActivityType, maxActivityType) {
			return nil, invalidArgument("activityType must hold 1 to 100 characters without control characters")
		}
		params.Set("filterByActivityType", f.ActivityType)
	}
	return params, nil
}

// lowerMembers matches the members of a row without regard to case: the reference gives no example.
func lowerMembers(row map[string]json.RawMessage) map[string]json.RawMessage {
	members := make(map[string]json.RawMessage, len(row))
	for key, value := range row {
		members[strings.ToLower(key)] = value
	}
	return members
}

// firstText reads the first of the named members that holds a scalar.
func firstText(members map[string]json.RawMessage, names ...string) string {
	for _, name := range names {
		var text *loose
		if raw, found := members[name]; found && json.Unmarshal(raw, &text) == nil && text != nil {
			if value := text.str(maxActivityText); value != "" {
				return value
			}
		}
	}
	return ""
}

func invokeActivityLogList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list activity log entries"
	var args struct {
		activityFilter
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	params, err := args.params()
	if err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if err := requireInstance(bound); err != nil {
		return nil, err
	}
	binding := provider.CursorBinding(activityLogList.ID, scopeBinding(resolved), args.UserLogin, args.ActivityType,
		args.Period, args.Date)
	paging, err := startPage(binding, args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	// Matomo reads limit -1 as unlimited, so the window is always positive: the batch plus one row.
	params.Set("offset", strconv.Itoa(paging.offset))
	params.Set("limit", strconv.Itoa(paging.limit+1))
	var answer json.RawMessage
	if err := client.call(ctx, op, activityLogRequirement(activityLogList.ID), "ActivityLog.getEntries", params, &answer); err != nil {
		return nil, err
	}
	rows, valid := readRows[map[string]json.RawMessage](answer)
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid activity log")
	}
	entries := make([]ActivityEntry, 0, len(rows))
	for _, row := range rows {
		members := lowerMembers(row)
		entries = append(entries, ActivityEntry{
			ID:           firstText(members, "id", "idactivity", "idlogactivity"),
			DateTime:     firstText(members, "datetime", "date", "timestamp", "time"),
			UserLogin:    firstText(members, "userlogin", "user", "login"),
			ActivityType: firstText(members, "activitytype", "type"),
			Description:  firstText(members, "description", "message", "title"),
		})
	}
	batch, next := sliceWindow(paging, entries)
	return &ActivityLogResult{Entries: batch, HasMore: next != "", NextCursor: next}, nil
}

func invokeActivityLogCount(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "count activity log entries"
	var args activityFilter
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	params, err := args.params()
	if err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if err := requireInstance(bound); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, activityLogRequirement(activityLogCount.ID), "ActivityLog.getEntryCount", params, &answer); err != nil {
		return nil, err
	}
	var count loose
	n := (*int)(nil)
	if scalar(answer, &count) == nil {
		n = count.number()
	}
	if n == nil || *n < 0 {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid count")
	}
	return &ActivityLogCount{Count: *n}, nil
}

func invokeActivityTypesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list activity types"
	var args struct {
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if err := requireInstance(bound); err != nil {
		return nil, err
	}
	paging, err := startPage(provider.CursorBinding(activityTypesList.ID, scopeBinding(resolved)), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	// filterLimit counts from the start of the list and is never -1 (unlimited): ask for the rows up to the end
	// of this batch plus one.
	params := url.Values{"filterLimit": {strconv.Itoa(paging.want())}}
	var answer json.RawMessage
	if err := client.call(ctx, op, activityLogRequirement(activityTypesList.ID), "ActivityLog.getAllActivityTypes", params, &answer); err != nil {
		return nil, err
	}
	rows, valid := readRows[json.RawMessage](answer)
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid activity type list")
	}
	types := make([]ActivityType, 0, len(rows))
	for _, row := range rows {
		var members map[string]json.RawMessage
		var text *loose
		switch {
		case json.Unmarshal(row, &members) == nil && members != nil:
			lower := lowerMembers(members)
			types = append(types, ActivityType{ID: firstText(lower, "id", "key", "type", "name"),
				Name: firstText(lower, "name", "description", "label")})
		case json.Unmarshal(row, &text) == nil && text != nil:
			types = append(types, ActivityType{ID: text.str(maxActivityText)})
		default:
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid activity type list")
		}
	}
	batch, next := slice(paging, types)
	return &ActivityTypesResult{Types: batch, HasMore: next != "", NextCursor: next}, nil
}

func activityLogOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: activityLogList, Handler: capability.Handler(invokeActivityLogList)},
		{Descriptor: activityLogCount, Handler: capability.Handler(invokeActivityLogCount)},
		{Descriptor: activityTypesList, Handler: capability.Handler(invokeActivityTypesList)},
	}
}
