package infomaniakdav

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

const (
	// writeUncertain is appended to a failure of a write whose request may have reached Infomaniak. The write
	// is never repeated.
	writeUncertain = "; the change may have taken effect, read it before repeating it"
	productID      = "-//Qatlas//Infomaniak DAV//EN"
)

const personArgSchema = `{"type":"object","properties":{"address":{"type":"string","minLength":3,"maxLength":254},` +
	`"name":{"type":"string","maxLength":256}},"required":["address"],"additionalProperties":false}`

const eventFieldSchemas = `"summary":{"type":"string","minLength":1,"maxLength":256},` +
	`"description":{"type":"string","maxLength":4096},"location":{"type":"string","maxLength":256},` +
	`"start":{"type":"string","minLength":10,"maxLength":20},"end":{"type":"string","minLength":10,"maxLength":20},` +
	`"all_day":{"type":"boolean"},"timezone":{"type":"string","maxLength":64},` +
	`"rrule":{"type":"string","maxLength":512},` +
	`"status":{"type":"string","enum":["TENTATIVE","CONFIRMED","CANCELLED"]},` +
	`"organizer":` + personArgSchema + `,"attendees":{"type":"array","maxItems":50,"items":` + personArgSchema + `}`

const (
	etagArgSchema = `"etag":{"type":"string","minLength":1,"maxLength":258}`
	idArgSchema   = `"id":{"type":"string","minLength":1,"maxLength":128}`
	writeOutProps = `"calendar":{"type":"string"},"id":{"type":"string"}`
)

func eventWriteRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: eventsSensitivity}
}

var eventFieldArguments = []capability.Argument{
	{Name: "summary", Description: "Title of the event", Required: true},
	{Name: "description", Description: "Description; line breaks are kept"},
	{Name: "location", Description: "Location"},
	{Name: "start", Description: "Start: a date YYYY-MM-DD with all_day, otherwise a local time YYYY-MM-DDTHH:MM:SS with timezone, or a UTC time ending in Z", Required: true},
	{Name: "end", Description: "End in the form of start, after start; with all_day the exclusive end date, one day after start by default; otherwise required"},
	{Name: "all_day", Description: "True for an event that lasts whole days"},
	{Name: "timezone", Description: "IANA time zone name such as Europe/Zurich for a local start and end; omit for UTC times or all-day events"},
	{Name: "rrule", Description: "Recurrence rule without the RRULE: prefix, for example FREQ=WEEKLY;COUNT=10; at most 512 bytes; validated, never expanded"},
	{Name: "status", Description: "TENTATIVE, CONFIRMED, or CANCELLED"},
	{Name: "organizer", Description: "Organizer as {address, name}; the name is optional"},
	{Name: "attendees", Description: "At most 50 attendees as {address, name}; Infomaniak may send each one an invitation"},
}

var calendarArgument = capability.Argument{Name: "calendar", Required: true,
	Description: "ID of an allow-listed calendar, the ID of its calendar/ID target"}

var etagArgument = capability.Argument{Name: "etag", Required: true,
	Description: "Entity tag of the event version to change, as events.get or events.list reports it"}

var eventsCreate = capability.Descriptor{
	ID: Provider + ".events.create", Version: 1, Title: "Create an Infomaniak calendar event",
	Description: "Create one new event in an allow-listed calendar from structured fields. Qatlas builds the " +
		"iCalendar object itself and generates the UID and the resource id; nothing raw comes from an argument. " +
		"One PUT with If-None-Match: * stores it, so no existing event can be replaced. A repeating event is a " +
		"single series, never expanded. Attendees may receive an invitation from Infomaniak. The request is " +
		"never repeated after an unclear result",
	Tags: []string{"infomaniak", "dav", "calendar", "event", "create"}, Provider: Provider,
	Risk: eventWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `,` + eventFieldSchemas +
		`},"required":["calendar","summary","start"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + writeOutProps + `,"uid":{"type":"string"},` +
		`"etag":{"type":"string"},"created":{"type":"boolean"}},"required":["calendar","id","uid","created"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{calendarArgument}, eventFieldArguments...),
	Fields: []capability.Field{
		{Name: "id", Description: "Generated resource id, the id argument of the other event tools"},
		{Name: "uid", Description: "Generated iCalendar UID"},
		{Name: "etag", Description: "Entity tag of the new event when Infomaniak reports one"},
		{Name: "created", Description: "True when Infomaniak accepted the event"},
	},
	Examples: []capability.Example{{Description: "Create a one-hour meeting with one attendee",
		Arguments: json.RawMessage(`{"calendar":"work","summary":"Review","start":"2026-03-10T09:00:00",` +
			`"end":"2026-03-10T10:00:00","timezone":"Europe/Zurich","attendees":[{"address":"anna@example.org"}]}`)}},
}

var eventsUpdate = capability.Descriptor{
	ID: Provider + ".events.update", Version: 1, Title: "Replace an Infomaniak calendar event",
	Description: "Replace one event of an allow-listed calendar completely by structured fields, bound to its " +
		"etag. The UID is kept and SEQUENCE is raised, which needs one GET before the single PUT with If-Match. " +
		"Everything Qatlas does not model, such as alarms and other properties, is lost. An event that holds " +
		"overrides of single occurrences or more than one event component is refused. A changed etag or a " +
		"failed precondition changes nothing. Attendees may receive an update from Infomaniak. The request is " +
		"never repeated after an unclear result",
	Tags: []string{"infomaniak", "dav", "calendar", "event", "update"}, Provider: Provider,
	Risk: eventWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `,` + idArgSchema + `,` +
		etagArgSchema + `,` + eventFieldSchemas +
		`},"required":["calendar","id","etag","summary","start"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + writeOutProps + `,"uid":{"type":"string"},` +
		`"etag":{"type":"string"},"updated":{"type":"boolean"}},"required":["calendar","id","uid","updated"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{calendarArgument,
		{Name: "id", Description: "Event id as the list tool reports it", Required: true}, etagArgument},
		eventFieldArguments...),
	Fields: []capability.Field{
		{Name: "id", Description: "Event id"},
		{Name: "uid", Description: "iCalendar UID of the event, kept"},
		{Name: "etag", Description: "Entity tag of the new version when Infomaniak reports one"},
		{Name: "updated", Description: "True when Infomaniak accepted the event"},
	},
	Examples: []capability.Example{{Description: "Move an event to another day",
		Arguments: json.RawMessage(`{"calendar":"work","id":"standup.ics","etag":"e1","summary":"Standup",` +
			`"start":"2026-03-11T09:00:00","end":"2026-03-11T09:15:00","timezone":"Europe/Zurich"}`)}},
}

var eventsDelete = capability.Descriptor{
	ID: Provider + ".events.delete", Version: 1, Title: "Delete an Infomaniak calendar event",
	Description: "Delete one event of an allow-listed calendar for good, bound to its etag, with one DELETE with " +
		"If-Match. A changed etag deletes nothing. Attendees may be notified by Infomaniak. The request is never " +
		"repeated after an unclear result",
	Tags: []string{"infomaniak", "dav", "calendar", "event", "delete", "permanent"}, Provider: Provider,
	RequiresToolAllowList: true,
	Risk:                  eventWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `,` + idArgSchema + `,` +
		etagArgSchema + `},"required":["calendar","id","etag"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + writeOutProps + `,"deleted":{"type":"boolean"}},` +
		`"required":["calendar","id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{calendarArgument,
		{Name: "id", Description: "Event id as the list tool reports it", Required: true}, etagArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Event id"},
		{Name: "deleted", Description: "True when Infomaniak accepted the deletion"},
	},
	Examples: []capability.Example{{Description: "Delete an event",
		Arguments: json.RawMessage(`{"calendar":"work","id":"standup.ics","etag":"e1"}`)}},
}

// WriteResult is the result of the create, update, and delete tools.
type WriteResult struct {
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

func decodeWriteArguments(op string, raw json.RawMessage) (eventWriteArguments, error) {
	var input eventWriteArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

// writeTarget checks the calendar, the event id, and the etag before any secret or request.
func writeTarget(op string, resolved *config.Resolved, input eventWriteArguments, withID bool) (etag string, err error) {
	if err := allowedCalendar(op, resolved, input.Calendar); err != nil {
		return "", err
	}
	if !withID {
		return "", nil
	}
	if !validCollectionID(input.ID) {
		return "", providerError(op, "the event id must be one literal path segment as the list tool reports it")
	}
	etag, ok := dav.NormalETag(input.ETag)
	if !ok {
		return "", providerError(op, "etag must be the entity tag of the event as the read tools report it")
	}
	return etag, nil
}

func invokeEventsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create event"
	input, err := decodeWriteArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if input.ID != "" || input.ETag != "" {
		return nil, providerError(op, "the id of a new event is generated and takes no etag")
	}
	if _, err := writeTarget(op, resolved, input, false); err != nil {
		return nil, err
	}
	draft, err := dav.NewEventDraft(op, productID, input.EventInput)
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
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	home, err := client.calendarHome(ctx, op)
	if err != nil {
		return nil, err
	}
	id := uid + ".ics"
	header, err := client.mutate(ctx, op, eventKind, methodPut, append(append([]string{}, home...), input.Calendar, id), "", body)
	if err != nil {
		return nil, err
	}
	return &WriteResult{Calendar: input.Calendar, ID: id, UID: uid, ETag: dav.ETagOf(header.Get("ETag")), Created: true}, nil
}

func invokeEventsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update event"
	input, err := decodeWriteArguments(op, raw)
	if err != nil {
		return nil, err
	}
	etag, err := writeTarget(op, resolved, input, true)
	if err != nil {
		return nil, err
	}
	draft, err := dav.NewEventDraft(op, productID, input.EventInput)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	home, err := client.calendarHome(ctx, op)
	if err != nil {
		return nil, err
	}
	path := append(append([]string{}, home...), input.Calendar, input.ID)
	data, header, err := client.request(ctx, op, methodGet, path, false, "", "", 200, maxEventBytes)
	if err != nil {
		return nil, err
	}
	current, err := server.ExistingEventOf(op, data)
	if err != nil {
		return nil, err
	}
	if served := header.Get("ETag"); served != "" && dav.ETagOf(served) != etag {
		return nil, errPrecondition(op, "event")
	}
	body, err := draft.Encode(op, current.UID, current.Sequence+1, time.Now())
	if err != nil {
		return nil, err
	}
	out, err := client.mutate(ctx, op, eventKind, methodPut, path, etag, body)
	if err != nil {
		return nil, err
	}
	return &WriteResult{Calendar: input.Calendar, ID: input.ID, UID: current.UID, ETag: dav.ETagOf(out.Get("ETag")),
		Updated: true}, nil
}

func invokeEventsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete event"
	input, err := decodeWriteArguments(op, raw)
	if err != nil {
		return nil, err
	}
	etag, err := writeTarget(op, resolved, input, true)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	home, err := client.calendarHome(ctx, op)
	if err != nil {
		return nil, err
	}
	if _, err := client.mutate(ctx, op, eventKind, methodDelete, append(append([]string{}, home...), input.Calendar, input.ID),
		etag, ""); err != nil {
		return nil, err
	}
	return &WriteResult{Calendar: input.Calendar, ID: input.ID, Deleted: true}, nil
}

func errPrecondition(op, noun string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op,
		Message: "the " + noun + " no longer matches the etag (precondition failed), nothing was changed; read it again"}
}

// writeKind names what a write changes, for its Content-Type and its messages.
type writeKind struct{ noun, collection, contentType string }

var eventKind = writeKind{"event", "calendar", "text/calendar; charset=utf-8"}

// mutate is the single write path. It sends exactly one PUT or DELETE and never repeats it. With an empty
// match the request carries If-None-Match: *, otherwise If-Match with the validated entity tag. A failure
// that leaves the outcome open carries writeUncertain.
func (c *Client) mutate(ctx context.Context, op string, kind writeKind, method string, segments []string, match, body string) (http.Header, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, provider.Waited(op, "Infomaniak", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, origin+pathOf(segments, false), strings.NewReader(body))
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/xml")
	if method == methodPut {
		req.Header.Set("Content-Type", kind.contentType)
	}
	if match == "" {
		req.Header.Set("If-None-Match", "*")
	} else {
		req.Header.Set("If-Match", `"`+match+`"`)
	}
	response, err := c.http.Do(req)
	if err != nil {
		failure := provider.Transport(op, "Infomaniak", err)
		var providerErr *provider.Error
		if errors.As(failure, &providerErr) && providerErr.MayHaveArrived() {
			providerErr.Message += writeUncertain
		}
		return nil, failure
	}
	defer response.Body.Close()
	switch status := response.StatusCode; {
	case status == http.StatusPreconditionFailed:
		if match == "" {
			return nil, &provider.Error{Class: provider.ClassProviderError, Op: op,
				Message: "a " + kind.noun + " with this id already exists, nothing was written"}
		}
		return nil, errPrecondition(op, kind.noun)
	case status == http.StatusForbidden:
		return nil, &provider.Error{Class: provider.ClassPermission, Op: op,
			Message: "this Infomaniak identity may not change this " + kind.collection}
	case status >= 500:
		failure := statusError(op, status)
		var providerErr *provider.Error
		if errors.As(failure, &providerErr) {
			providerErr.Message += writeUncertain
		}
		return nil, failure
	case status >= 300:
		return nil, statusError(op, status)
	case status == http.StatusOK || status == http.StatusNoContent || (status == http.StatusCreated && method == methodPut):
		return response.Header, nil
	}
	return nil, invalidResponse(op, "Infomaniak answered the change with an unexpected status"+writeUncertain)
}
