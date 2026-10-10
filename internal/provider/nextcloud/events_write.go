package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Event writes in a bound calendar through CalDAV: a PUT with If-None-Match: * creates, a PUT with If-Match
// replaces, and a DELETE with If-Match removes one event. Each sends exactly one request after one read-only
// pre-check that refuses a read-only calendar locally.

const (
	eventProductID = "-//Qatlas//Nextcloud//EN"
	eventUncertain = "; the change may have been applied, check the calendar with events.list or events.get " +
		"before repeating it"
)

// eventCalendarProbeBody reads what decides whether a calendar takes a new or changed event.
const eventCalendarProbeBody = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop>` +
	`<d:resourcetype/><d:current-user-privilege-set/><c:supported-calendar-component-set/>` +
	`</d:prop></d:propfind>`

// eventProbeBody reads the stored event, its entity tag, and the privileges the identity holds on it.
const eventProbeBody = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop>` +
	`<d:getetag/><d:current-user-privilege-set/><c:calendar-data/>` +
	`</d:prop></d:propfind>`

const eventPersonArgSchema = `{"type":"object","properties":{"address":{"type":"string","minLength":3,"maxLength":254},` +
	`"name":{"type":"string","maxLength":256}},"required":["address"],"additionalProperties":false}`

const eventFieldSchemas = `"summary":{"type":"string","minLength":1,"maxLength":256},` +
	`"description":{"type":"string","maxLength":4096},"location":{"type":"string","maxLength":256},` +
	`"start":{"type":"string","minLength":10,"maxLength":20},"end":{"type":"string","minLength":10,"maxLength":20},` +
	`"all_day":{"type":"boolean"},"timezone":{"type":"string","maxLength":64},` +
	`"rrule":{"type":"string","maxLength":512},` +
	`"status":{"type":"string","enum":["TENTATIVE","CONFIRMED","CANCELLED"]},` +
	`"organizer":` + eventPersonArgSchema + `,"attendees":{"type":"array","maxItems":50,"items":` + eventPersonArgSchema + `}`

const (
	eventIDArgSchema   = `"id":{"type":"string","minLength":1,"maxLength":255}`
	eventETagArgSchema = `"etag":{"type":"string","minLength":1,"maxLength":258}`
	eventWriteOutProps = `"calendar":{"type":"string"},"id":{"type":"string"}`
)

// eventWriteRisk is open-world: attendees receive e-mail from Nextcloud.
func eventWriteRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: calendarSensitivity}
}

var eventFieldArguments = []capability.Argument{
	{Name: "summary", Description: "Title of the event", Required: true},
	{Name: "description", Description: "Description; line breaks are kept"},
	{Name: "location", Description: "Location"},
	{Name: "start", Description: "Start: a date YYYY-MM-DD with all_day, otherwise a local time YYYY-MM-DDTHH:MM:SS with timezone, or a UTC time ending in Z", Required: true},
	{Name: "end", Description: "End in the form of start, after start; with all_day the exclusive end date, one day after start by default; otherwise required"},
	{Name: "all_day", Description: "True for an event that lasts whole days"},
	{Name: "timezone", Description: "IANA time zone name such as Europe/Berlin for a local start and end; omit for UTC times or all-day events"},
	{Name: "rrule", Description: "Recurrence rule without the RRULE: prefix, for example FREQ=WEEKLY;COUNT=10; validated, never expanded"},
	{Name: "status", Description: "TENTATIVE, CONFIRMED, or CANCELLED"},
	{Name: "organizer", Description: "Organizer as {address, name}; the name is optional"},
	{Name: "attendees", Description: "At most 50 attendees as {address, name}; Nextcloud sends each one an invitation by e-mail"},
}

var (
	eventIDArgument   = capability.Argument{Name: "id", Description: "Event id as events.list or events.get reports it", Required: true}
	eventETagArgument = capability.Argument{Name: "etag", Required: true,
		Description: "Entity tag of the event version to change, as events.get or events.list reports it"}
)

var eventsCreate = capability.Descriptor{
	ID: Provider + ".events.create", Version: 1, Title: "Create a Nextcloud calendar event",
	Description: "Create one confirmed event in a bound, writable calendar from structured fields. Qatlas builds " +
		"the iCalendar object itself and generates its UID and resource id; nothing raw comes from an argument. " +
		"One read-only pre-check of the calendar refuses a read-only calendar, then one PUT that never replaces " +
		"an existing event stores it. A repeating event is one series, never expanded. With attendees the change " +
		"reaches outside: Nextcloud sends them invitations by e-mail. The request is never repeated after an " +
		"unclear result",
	Tags: []string{"nextcloud", "calendar", "event", "create"}, Provider: Provider,
	Risk: eventWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `,` + eventFieldSchemas +
		`},"required":["calendar","summary","start"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + eventWriteOutProps + `,"uid":{"type":"string"},` +
		`"etag":{"type":"string"},"created":{"type":"boolean"}},"required":["calendar","id","uid","created"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{calendarArgument}, eventFieldArguments...),
	Fields: []capability.Field{
		{Name: "id", Description: "Generated resource id, the id argument of the other event tools"},
		{Name: "uid", Description: "Generated iCalendar UID"},
		{Name: "etag", Description: "Entity tag of the new event when Nextcloud reports one"},
		{Name: "created", Description: "True when Nextcloud stored the event"},
	},
	Examples: []capability.Example{{Description: "Create a one-hour meeting",
		Arguments: json.RawMessage(`{"calendar":"personal","summary":"Review","start":"2026-03-10T09:00:00",` +
			`"end":"2026-03-10T10:00:00","timezone":"Europe/Berlin"}`)}},
}

var eventsUpdate = capability.Descriptor{
	ID: Provider + ".events.update", Version: 1, Title: "Replace a Nextcloud calendar event",
	Description: "Replace one confirmed event of a bound, writable calendar completely by structured fields, bound " +
		"to its etag. One read-only pre-check reads the event and refuses a read-only calendar, a changed etag, " +
		"and an event that holds overrides of single occurrences or other components; then one PUT with the etag " +
		"follows. The UID is kept and SEQUENCE raised; everything Qatlas does not model, such as alarms, is lost. " +
		"With attendees the change reaches outside: Nextcloud sends them updates by e-mail. The request is never " +
		"repeated after an unclear result",
	Tags: []string{"nextcloud", "calendar", "event", "update"}, Provider: Provider,
	Risk: eventWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `,` + eventIDArgSchema + `,` +
		eventETagArgSchema + `,` + eventFieldSchemas +
		`},"required":["calendar","id","etag","summary","start"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + eventWriteOutProps + `,"uid":{"type":"string"},` +
		`"etag":{"type":"string"},"updated":{"type":"boolean"}},"required":["calendar","id","uid","updated"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{calendarArgument, eventIDArgument, eventETagArgument}, eventFieldArguments...),
	Fields: []capability.Field{
		{Name: "id", Description: "Event id"},
		{Name: "uid", Description: "iCalendar UID of the event, kept"},
		{Name: "etag", Description: "Entity tag of the new version when Nextcloud reports one"},
		{Name: "updated", Description: "True when Nextcloud stored the new version"},
	},
	Examples: []capability.Example{{Description: "Move an event to another day",
		Arguments: json.RawMessage(`{"calendar":"personal","id":"standup.ics","etag":"e1","summary":"Standup",` +
			`"start":"2026-03-11T09:00:00","end":"2026-03-11T09:15:00","timezone":"Europe/Berlin"}`)}},
}

var eventsDelete = capability.Descriptor{
	ID: Provider + ".events.delete", Version: 1, Title: "Delete a Nextcloud calendar event",
	Description: "Delete one confirmed event of a bound, writable calendar, bound to its etag; a changed etag " +
		"deletes nothing. One read-only pre-check of the calendar refuses a read-only calendar, then one DELETE " +
		"follows. Nextcloud may keep the event in its calendar trash bin for a while; Qatlas offers no restore. " +
		"With attendees the change reaches outside: Nextcloud sends them a cancellation by e-mail. The request " +
		"is never repeated after an unclear result",
	Tags: []string{"nextcloud", "calendar", "event", "delete"}, Provider: Provider,
	Risk: eventWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `,` + eventIDArgSchema + `,` +
		eventETagArgSchema + `},"required":["calendar","id","etag"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + eventWriteOutProps + `,"deleted":{"type":"boolean"}},` +
		`"required":["calendar","id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{calendarArgument, eventIDArgument, eventETagArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Event id"},
		{Name: "deleted", Description: "True when Nextcloud deleted the event"},
	},
	Examples: []capability.Example{{Description: "Delete an event",
		Arguments: json.RawMessage(`{"calendar":"personal","id":"standup.ics","etag":"e1"}`)}},
	// Reachable only through a tools list, never through a profile.
	RequiresToolAllowList: true,
}

// EventWriteResult is the result of the event create, update, and delete tools.
type EventWriteResult struct {
	Calendar string `json:"calendar"`
	ID       string `json:"id"`
	UID      string `json:"uid,omitempty"`
	ETag     string `json:"etag,omitempty"`
	Created  bool   `json:"created,omitempty"`
	Updated  bool   `json:"updated,omitempty"`
	Deleted  bool   `json:"deleted,omitempty"`
}

type eventWriteArguments struct {
	Calendar string `json:"calendar"`
	ID       string `json:"id"`
	ETag     string `json:"etag"`
	dav.EventInput
}

type eventDeleteArguments struct {
	Calendar string `json:"calendar"`
	ID       string `json:"id"`
	ETag     string `json:"etag"`
}

func decodeEventWrite(op string, raw json.RawMessage, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return providerError(op, "the validated arguments could not be read")
	}
	return nil
}

// eventTarget checks the event id and the entity tag of an existing event before any secret or request.
func eventTarget(op, id, etag string) (string, error) {
	if !checkEventID(id) {
		return "", providerError(op, "the event id must be one literal path segment as the events list reports it")
	}
	normal, ok := dav.NormalETag(etag)
	if !ok {
		return "", providerError(op, "etag must be the entity tag of the event as the read tools report it")
	}
	return normal, nil
}

func invokeEventsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create event"
	var input eventWriteArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	if input.ID != "" || input.ETag != "" {
		return nil, providerError(op, "the id of a new event is generated and takes no etag")
	}
	draft, err := dav.NewEventDraft(op, eventProductID, input.EventInput)
	if err != nil {
		return nil, err
	}
	uid, err := dav.RandomID()
	if err != nil {
		return nil, providerError(op, "an event id could not be generated")
	}
	body, err := draft.Encode(op, uid, 0, time.Now())
	if err != nil {
		return nil, err
	}
	client, err := openCalendar(ctx, op, resolved, secrets, red, input.Calendar)
	if err != nil {
		return nil, err
	}
	if err := client.eventCalendarWritable(ctx, op, input.Calendar); err != nil {
		return nil, err
	}
	id := uid + ".ics"
	header, err := client.eventSend(ctx, op, http.MethodPut, input.Calendar, id, "", body)
	if err != nil {
		return nil, err
	}
	return &EventWriteResult{Calendar: input.Calendar, ID: id, UID: uid, ETag: dav.ETagOf(header.Get("ETag")),
		Created: true}, nil
}

func invokeEventsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update event"
	var input eventWriteArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	etag, err := eventTarget(op, input.ID, input.ETag)
	if err != nil {
		return nil, err
	}
	draft, err := dav.NewEventDraft(op, eventProductID, input.EventInput)
	if err != nil {
		return nil, err
	}
	client, err := openCalendar(ctx, op, resolved, secrets, red, input.Calendar)
	if err != nil {
		return nil, err
	}
	current, err := client.eventCurrent(ctx, op, input.Calendar, input.ID, etag)
	if err != nil {
		return nil, err
	}
	body, err := draft.Encode(op, current.UID, current.Sequence+1, time.Now())
	if err != nil {
		return nil, err
	}
	header, err := client.eventSend(ctx, op, http.MethodPut, input.Calendar, input.ID, etag, body)
	if err != nil {
		return nil, err
	}
	return &EventWriteResult{Calendar: input.Calendar, ID: input.ID, UID: current.UID,
		ETag: dav.ETagOf(header.Get("ETag")), Updated: true}, nil
}

func invokeEventsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete event"
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
	if err := client.eventCalendarWritable(ctx, op, input.Calendar); err != nil {
		return nil, err
	}
	if _, err := client.eventSend(ctx, op, http.MethodDelete, input.Calendar, input.ID, etag, ""); err != nil {
		return nil, err
	}
	return &EventWriteResult{Calendar: input.Calendar, ID: input.ID, Deleted: true}, nil
}

func eventReadOnly(op string) error {
	return &provider.Error{Class: provider.ClassPermission, Op: op,
		Message: "this calendar is read-only for this Nextcloud identity, nothing was changed"}
}

func eventPrecondition(op string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op,
		Message: "the event no longer matches the etag (precondition failed), nothing was changed; read it again"}
}

// eventOnlyNode returns the single node of a depth 0 answer when it is the addressed one.
func (c *Client) eventOnlyNode(op string, data []byte, segments []string) (*dav.Resource, error) {
	resources, err := c.davServer().ParseMultiStatus(op, data)
	if err != nil {
		return nil, err
	}
	if len(resources) != 1 || !resources[0].Read {
		return nil, invalidResponse(op, "Nextcloud did not answer with the requested node")
	}
	found, err := c.davServer().Segments(op, resources[0].Href)
	if err != nil || !equalSegments(found, segments) {
		return nil, invalidResponse(op, "Nextcloud did not answer with the requested node")
	}
	return &resources[0], nil
}

// eventCalendarWritable is the pre-check of create and delete: one PROPFIND of depth 0 on the bound calendar.
// A node that is no calendar, holds no events, or is read-only for the identity is refused without a change.
func (c *Client) eventCalendarWritable(ctx context.Context, op, calendar string) error {
	base := c.calendarHome(calendar)
	data, _, err := c.calendarRequest(ctx, op, methodPropfind, base, true, depthSelf, eventCalendarProbeBody,
		http.StatusMultiStatus, dav.MaxResponseBytes)
	if err != nil {
		return err
	}
	node, err := c.eventOnlyNode(op, data, base)
	if err != nil {
		return err
	}
	if !node.Calendar {
		return invalidResponse(op, "Nextcloud did not answer with a calendar")
	}
	if len(node.Components) > 0 && !containsString(node.Components, "VEVENT") {
		return providerError(op, "this calendar holds no events, nothing was changed")
	}
	if !writable(node.Privileges) {
		return eventReadOnly(op)
	}
	return nil
}

// eventCurrent is the pre-check of update: one PROPFIND of depth 0 on the event reads its data, its entity
// tag, and the privileges the identity holds on it, which Nextcloud derives from the calendar. It refuses a
// read-only calendar, a changed entity tag, and an object that is not exactly one plain event.
func (c *Client) eventCurrent(ctx context.Context, op, calendar, id, etag string) (dav.ExistingEvent, error) {
	path := c.calendarHome(calendar, id)
	data, _, err := c.calendarRequest(ctx, op, methodPropfind, path, false, depthSelf, eventProbeBody,
		http.StatusMultiStatus, dav.MaxResponseBytes)
	if err != nil {
		return dav.ExistingEvent{}, err
	}
	node, err := c.eventOnlyNode(op, data, path)
	if err != nil {
		return dav.ExistingEvent{}, err
	}
	if !writable(node.Privileges) {
		return dav.ExistingEvent{}, eventReadOnly(op)
	}
	if served := node.Text[dav.KeyETag]; served != "" && dav.ETagOf(served) != etag {
		return dav.ExistingEvent{}, eventPrecondition(op)
	}
	stored := node.Text[dav.KeyCalendar]
	if strings.TrimSpace(stored) == "" {
		return dav.ExistingEvent{}, invalidResponse(op, "Nextcloud did not report the stored event")
	}
	return c.davServer().ExistingEventOf(op, []byte(stored))
}

// eventSend is the single write path. It sends exactly one PUT or DELETE to the event in the bound calendar
// and never repeats it. Without a match the request carries If-None-Match: *, otherwise If-Match with the
// validated entity tag. A failure that leaves the outcome open carries eventUncertain; a refusal or a
// redirect is clear.
func (c *Client) eventSend(ctx context.Context, op, method, calendar, id, match, body string) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.origin+escapePath(c.calendarHome(calendar, id)),
		strings.NewReader(body))
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "text/calendar; charset=utf-8")
	}
	if match == "" {
		req.Header.Set("If-None-Match", "*")
	} else {
		req.Header.Set("If-Match", `"`+match+`"`)
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, sentTransportError(op, err, eventUncertain)
	}
	defer response.Body.Close()
	switch status := response.StatusCode; {
	case status == http.StatusPreconditionFailed && match == "":
		return nil, providerError(op, "an event with this id already exists, nothing was written")
	case status == http.StatusPreconditionFailed:
		return nil, eventPrecondition(op)
	case status == http.StatusForbidden:
		return nil, &provider.Error{Class: provider.ClassPermission, Op: op,
			Message: "this Nextcloud identity may not change this event, nothing was changed"}
	case status < 200 || status >= 300:
		return nil, sentStatusError(op, status, eventUncertain)
	case status == http.StatusNoContent, status == http.StatusOK, status == http.StatusCreated && method == http.MethodPut:
		return response.Header, nil
	}
	return nil, invalidResponse(op, "Nextcloud answered the change with an unexpected status"+eventUncertain)
}
