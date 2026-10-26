package nextcloud

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Calendar trash bin of Nextcloud (apps/dav, CalDAV Trashbin): PROPFIND of calendars/<user>/trashbin/objects/
// lists the deleted events and tasks, a MOVE of one object to calendars/<user>/trashbin/restore/<name>
// restores it, and a DELETE of the object removes it for good. Only objects whose calendar is bound by the
// connection are ever listed or touched.

const (
	maxCalendarTrash     = 200
	maxCalendarTrashScan = dav.MaxResponseBytes

	messageTrashObjectNone = "this calendar trash holds no such entry of a bound calendar"

	calendarTrashUncertain = "; the change may have been applied, check the trash with calendar.trash.list " +
		"before repeating it"
)

// calendarTrashBody asks for exactly the properties a trash entry is built from.
const calendarTrashBody = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<d:propfind xmlns:d="DAV:" xmlns:nc="http://nextcloud.com/ns"><d:prop>` +
	`<d:resourcetype/><d:getcontenttype/><nc:deleted-at/><nc:calendar-uri/>` +
	`</d:prop></d:propfind>`

// calendarTrashRetentionBody asks the trash bin collection for its retention period.
const calendarTrashRetentionBody = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<d:propfind xmlns:d="DAV:" xmlns:nc="http://nextcloud.com/ns"><d:prop>` +
	`<nc:trash-bin-retention-duration/>` +
	`</d:prop></d:propfind>`

const calendarTrashEntrySchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"calendar":{"type":"string"},"kind":{"type":"string","enum":["VEVENT","VTODO"]},` +
	`"deleted_at":{"type":"string"}},"required":["id","calendar"],"additionalProperties":false}`

const calendarTrashIDSchema = `"id":{"type":"string","minLength":1,"maxLength":255,"pattern":"` +
	`^(?:\\.[^./\\\\%][^/\\\\%]*|\\.\\.[^/\\\\%]+|[^./\\\\%][^/\\\\%]*)$"}`

var calendarTrashList = capability.Descriptor{
	ID: Provider + ".calendar.trash.list", Version: 1, Title: "List the Nextcloud calendar trash bin",
	Description: "List the deleted events and tasks of the bound calendars, at most 200 per call, with the " +
		"retention period of the trash bin; entries of other calendars are not listed and no content is read",
	Tags: []string{"nextcloud", "calendar", "trash", "list"}, Provider: Provider,
	Risk:        calendarRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"entries":{"type":"array","items":` +
		calendarTrashEntrySchema + `},"count":{"type":"integer"},"truncated":{"type":"boolean"},` +
		`"retention_seconds":{"type":"integer"}},"required":["entries","count"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "entries", Description: "Deleted entries, untrusted data; pass id to calendar.trash.restore or delete"},
		{Name: "id", Description: "Name of the entry in the trash bin"},
		{Name: "calendar", Description: "URI of the calendar the entry was deleted from"},
		{Name: "kind", Description: "VEVENT or VTODO, when the server reports it"},
		{Name: "deleted_at", Description: "Deletion time, RFC 3339 in UTC, when the server reports it"},
		{Name: "count", Description: "Number of reported entries"},
		{Name: "truncated", Description: "True when more matching entries exist than reported"},
		{Name: "retention_seconds", Description: "Seconds Nextcloud keeps deleted entries, when it reports them"},
	},
	Examples: []capability.Example{{Description: "List the deleted calendar entries", Arguments: json.RawMessage(`{}`)}},
}

func calendarTrashChange(action, done, title, description string, effect capability.Effect,
	idempotency capability.Idempotency) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + ".calendar.trash." + action, Version: 1, Title: title, Description: description,
		Tags: []string{"nextcloud", "calendar", "trash", action}, Provider: Provider,
		Risk: eventWriteRisk(effect, idempotency),
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarTrashIDSchema +
			`},"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},` +
			`"calendar":{"type":"string"},"` + done + `":{"type":"boolean"}},"required":["id","calendar","` + done +
			`"],"additionalProperties":false}`),
		Arguments: []capability.Argument{{Name: "id", Required: true,
			Description: "Name of the trash entry, as calendar.trash.list reports it"}},
		Fields: []capability.Field{
			{Name: "id", Description: "The trash entry"},
			{Name: "calendar", Description: "URI of the calendar the entry was deleted from"},
			{Name: done, Description: "True when Nextcloud applied the change"},
		},
		Examples: []capability.Example{{Description: "Use an id from calendar.trash.list",
			Arguments: json.RawMessage(`{"id":"meeting.ics-deleted-1700000000"}`)}},
	}
}

var calendarTrashRestore = calendarTrashChange("restore", "restored", "Restore a Nextcloud calendar trash entry",
	"Restore exactly one deleted event or task of a bound calendar to its calendar; there is no other "+
		"destination. One read-only pre-check of the entry follows, then one MOVE. The request is never "+
		"repeated after an unclear result",
	capability.EffectUpdate, capability.IdempotencyNonIdempotent)

var calendarTrashDelete = func() capability.Descriptor {
	d := calendarTrashChange("delete", "deleted", "Delete a Nextcloud calendar trash entry for good",
		"Permanently delete exactly one deleted event or task of a bound calendar from the trash bin; it cannot "+
			"be restored afterwards. One read-only pre-check of the entry follows, then one DELETE. The request "+
			"is never repeated after an unclear result",
		capability.EffectDelete, capability.IdempotencyIdempotent)
	// Reachable only through a tools list, never through a profile.
	d.RequiresToolAllowList = true
	return d
}()

// CalendarTrashEntry is one deleted event or task of a bound calendar.
type CalendarTrashEntry struct {
	ID        string `json:"id"`
	Calendar  string `json:"calendar"`
	Kind      string `json:"kind,omitempty"`
	DeletedAt string `json:"deleted_at,omitempty"`
}

// CalendarTrashResult is the result of the calendar trash list.
type CalendarTrashResult struct {
	Entries          []CalendarTrashEntry `json:"entries"`
	Count            int                  `json:"count"`
	Truncated        bool                 `json:"truncated,omitempty"`
	RetentionSeconds int64                `json:"retention_seconds,omitempty"`
}

type calendarTrashArguments struct {
	ID string `json:"id"`
}

// trashSegments are the path segments of the trash bin collection of the identity, followed by rel.
func (c *Client) calendarTrashSegments(rel ...string) []string {
	return c.calendarHome(append([]string{"trashbin"}, rel...)...)
}

// calendarTrashEntryOf reads one answered object. ok is false for an object outside the bound calendars or
// without a usable calendar URI; such an object is never reported.
func calendarTrashEntryOf(res *dav.Resource, id string, bound selection) (CalendarTrashEntry, bool) {
	uri := res.Text[dav.KeyCalendarURI]
	if !res.Read || res.Calendar || checkSegment(uri) != nil || !bound.holdsCalendar(uri) {
		return CalendarTrashEntry{}, false
	}
	entry := CalendarTrashEntry{ID: id, Calendar: uri}
	switch contentComponent(res.Text[dav.KeyContentType]) {
	case "vevent":
		entry.Kind = "VEVENT"
	case "vtodo":
		entry.Kind = "VTODO"
	}
	entry.DeletedAt = deletedAtOf(res.Text[dav.KeyDeletedAt])
	return entry, true
}

// contentComponent returns the lower-case component parameter of a calendar content type, or "".
func contentComponent(contentType string) string {
	for _, part := range strings.Split(contentType, ";")[1:] {
		if name, value, ok := strings.Cut(strings.TrimSpace(part), "="); ok && strings.EqualFold(name, "component") {
			return strings.ToLower(strings.Trim(value, `"`))
		}
	}
	return ""
}

// deletedAtOf normalises the deletion time, given as RFC 3339 or as Unix seconds, to RFC 3339 in UTC. An
// unusable value yields "".
func deletedAtOf(raw string) string {
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 && seconds <= maxTrashDeleted {
		return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil && t.Year() > 1970 && t.Year() < 10000 {
		return t.UTC().Format(time.RFC3339)
	}
	return ""
}

// listCalendarTrash lists the deleted objects of the bound calendars with one PROPFIND of depth 1 on the
// objects collection, plus one of depth 0 on the trash bin collection for its retention period.
func (c *Client) listCalendarTrash(ctx context.Context, bound selection) (*CalendarTrashResult, error) {
	const op = "list calendar trash"
	base := c.calendarTrashSegments("objects")
	data, _, err := c.calendarRequest(ctx, op, methodPropfind, base, true, depthChildren, calendarTrashBody,
		http.StatusMultiStatus, maxCalendarTrashScan)
	if err != nil {
		return nil, calendarTrashMissing(err)
	}
	resources, err := c.davServer().ParseMultiStatus(op, data)
	if err != nil {
		return nil, err
	}
	result := &CalendarTrashResult{Entries: []CalendarTrashEntry{}}
	seen := map[string]bool{}
	for i := range resources {
		id, ok := c.childOf(op, resources[i].Href, base)
		if !ok || !checkEventID(id) || seen[id] {
			continue
		}
		seen[id] = true
		if entry, ok := calendarTrashEntryOf(&resources[i], id, bound); ok {
			result.Entries = append(result.Entries, entry)
		}
	}
	sort.Slice(result.Entries, func(i, j int) bool {
		a, b := result.Entries[i], result.Entries[j]
		if a.Calendar != b.Calendar {
			return a.Calendar < b.Calendar
		}
		return a.ID < b.ID
	})
	if len(result.Entries) > maxCalendarTrash {
		result.Entries, result.Truncated = result.Entries[:maxCalendarTrash], true
	}
	result.Count = len(result.Entries)
	if result.RetentionSeconds, err = c.calendarTrashRetention(ctx, op); err != nil {
		return nil, err
	}
	return result, nil
}

// calendarTrashRetention reads the retention period; a server that does not report one yields 0.
func (c *Client) calendarTrashRetention(ctx context.Context, op string) (int64, error) {
	base := c.calendarTrashSegments()
	data, _, err := c.calendarRequest(ctx, op, methodPropfind, base, true, depthSelf, calendarTrashRetentionBody,
		http.StatusMultiStatus, dav.MaxResponseBytes)
	if err != nil {
		return 0, calendarTrashMissing(err)
	}
	resources, err := c.davServer().ParseMultiStatus(op, data)
	if err != nil {
		return 0, err
	}
	if len(resources) != 1 {
		return 0, invalidResponse(op, "Nextcloud did not answer with the requested node")
	}
	seconds, err := strconv.ParseInt(resources[0].Text[dav.KeyRetention], 10, 64)
	if err != nil || seconds < 0 {
		return 0, nil
	}
	return seconds, nil
}

// calendarTrashMissing explains a missing trash bin collection, which means the feature is not available.
func calendarTrashMissing(err error) error {
	if isNotFound(err) {
		return providerError("list calendar trash", "the Nextcloud calendar trash bin is not available")
	}
	return err
}

// readCalendarTrashEntry is the pre-check of restore and delete: one PROPFIND of depth 0 on the entry. An
// entry that is missing, or whose calendar is not bound, is answered alike and without naming the calendar.
func (c *Client) readCalendarTrashEntry(ctx context.Context, op, id string, bound selection) (CalendarTrashEntry, error) {
	target := c.calendarTrashSegments("objects", id)
	data, _, err := c.calendarRequest(ctx, op, methodPropfind, target, false, depthSelf, calendarTrashBody,
		http.StatusMultiStatus, dav.MaxResponseBytes)
	if err != nil {
		if isNotFound(err) {
			return CalendarTrashEntry{}, providerError(op, messageTrashObjectNone)
		}
		return CalendarTrashEntry{}, err
	}
	resources, err := c.davServer().ParseMultiStatus(op, data)
	if err != nil {
		return CalendarTrashEntry{}, err
	}
	if len(resources) != 1 {
		return CalendarTrashEntry{}, invalidResponse(op, "Nextcloud did not answer with the requested node")
	}
	found, err := c.davServer().Segments(op, resources[0].Href)
	if err != nil || !equalSegments(found, target) {
		return CalendarTrashEntry{}, invalidResponse(op, "Nextcloud did not answer with the requested node")
	}
	entry, ok := calendarTrashEntryOf(&resources[0], id, bound)
	if !ok {
		return CalendarTrashEntry{}, providerError(op, messageTrashObjectNone)
	}
	return entry, nil
}

func invokeCalendarTrashList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	bound, err := requireCalendar(resolved)
	if err != nil {
		return nil, err
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.listCalendarTrash(ctx, bound)
}

func invokeCalendarTrashRestore(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeCalendarTrashChange(ctx, resolved, secrets, red, raw, "restore calendar trash entry", "restored")
}

func invokeCalendarTrashDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeCalendarTrashChange(ctx, resolved, secrets, red, raw, "delete calendar trash entry", "deleted")
}

// invokeCalendarTrashChange reads the entry once and then sends exactly one MOVE (restore) or DELETE. The
// request is never repeated, and an unclear outcome is reported as such.
func invokeCalendarTrashChange(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, op, done string) (any, error) {
	var input calendarTrashArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !checkEventID(input.ID) {
		return nil, providerError(op, "id must be one literal path segment as calendar.trash.list reports it")
	}
	bound, err := requireCalendar(resolved)
	if err != nil {
		return nil, err
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	entry, err := client.readCalendarTrashEntry(ctx, op, input.ID, bound)
	if err != nil {
		return nil, err
	}
	method, extra := http.MethodDelete, http.Header(nil)
	if done == "restored" {
		method = methodMove
		extra = http.Header{"Destination": {client.origin + escapePath(client.calendarTrashSegments("restore", input.ID))}}
	}
	response, err := client.webdavTo(ctx, op, method,
		client.origin+escapePath(client.calendarTrashSegments("objects", input.ID)), nil, "", "",
		calendarTrashUncertain, extra)
	if err != nil {
		if method == methodMove && refusedAsConflict(err) {
			return nil, providerErrorFrom(op, "Nextcloud refused to restore the entry, an object with this name may "+
				"exist again, nothing was changed", err)
		}
		return nil, err
	}
	response.Body.Close()
	return map[string]any{"id": input.ID, "calendar": entry.Calendar, done: true}, nil
}

// refusedAsConflict reports whether err is the clear refusal of a 409 or 412 answer.
func refusedAsConflict(err error) bool {
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		return false
	}
	for _, status := range []int{http.StatusConflict, http.StatusPreconditionFailed} {
		if providerErr.Message == statusError("", status).(*provider.Error).Message {
			return true
		}
	}
	return false
}
