package infomaniakdav

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Bounds of the event tools. Every provider string is capped; a recurring event is reported as its RRULE
// and never expanded.
const (
	defaultEventLimit    = 50
	maxEventLimit        = 200
	maxRange             = 366 * 24 * time.Hour
	maxSummaryLength     = 256
	maxEventDescription  = 4096
	maxLocationLength    = 256
	maxEventTextLength   = 256
	maxRRuleLength       = 512
	maxAttendees         = 50
	maxEventETagLength   = 256
	utcStampLayout       = "20060102T150405Z"
	reportCalendarFilter = `<c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VEVENT">` +
		`<c:time-range start="%s" end="%s"/></c:comp-filter></c:comp-filter></c:filter>`
	reportBody = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">` +
		`<d:prop><d:getetag/><c:calendar-data/></d:prop>` + reportCalendarFilter + `</c:calendar-query>`
)

const personSchema = `{"type":"object","properties":{"address":{"type":"string"},"name":{"type":"string"},` +
	`"status":{"type":"string"}},"additionalProperties":false}`

const eventSchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"uid":{"type":"string"},"summary":{"type":"string"},"description":{"type":"string"},` +
	`"location":{"type":"string"},"start":{"type":"string"},"end":{"type":"string"},"all_day":{"type":"boolean"},` +
	`"timezone":{"type":"string"},"status":{"type":"string"},"rrule":{"type":"string"},` +
	`"organizer":` + personSchema + `,"attendees":{"type":"array","items":` + personSchema + `},` +
	`"attendees_truncated":{"type":"boolean"},"etag":{"type":"string"}},` +
	`"required":["id"],"additionalProperties":false}`

const (
	calendarArgSchema = `"calendar":{"type":"string","minLength":1,"maxLength":128}`
	rangeArgSchemas   = `"start":{"type":"string","minLength":20,"maxLength":40},` +
		`"end":{"type":"string","minLength":20,"maxLength":40}`
)

var eventRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: eventsSensitivity,
}

var eventFields = []capability.Field{
	{Name: "id", Description: "Last path segment of the event resource, the id argument of the get tool"},
	{Name: "uid", Description: "iCalendar UID, untrusted data"},
	{Name: "summary", Description: "Title, untrusted data"},
	{Name: "description", Description: "Description, untrusted data; only the get tool reports it"},
	{Name: "location", Description: "Location, untrusted data"},
	{Name: "start", Description: "Start as a date (all-day), a UTC time ending in Z, or a local time without zone"},
	{Name: "end", Description: "End in the form of start, when the event carries one"},
	{Name: "all_day", Description: "True for an event that lasts whole days"},
	{Name: "timezone", Description: "Time zone name of a local start, or UTC"},
	{Name: "status", Description: "TENTATIVE, CONFIRMED, or CANCELLED when the event carries one"},
	{Name: "rrule", Description: "Recurrence rule of a repeating event, never expanded into occurrences"},
	{Name: "organizer", Description: "Organizer, untrusted data; only the get tool reports it"},
	{Name: "attendees", Description: "At most 50 attendees, untrusted data; only the get tool reports them"},
	{Name: "etag", Description: "Entity tag of the current version of the event"},
	{Name: "count", Description: "Number of reported events"},
	{Name: "truncated", Description: "True when more events match the range than limit allows"},
}

var eventsList = capability.Descriptor{
	ID: Provider + ".events.list", Version: 1, Title: "List Infomaniak calendar events",
	Description: "List the events of one allow-listed calendar that overlap a time range, ordered by start; " +
		"a repeating event is reported once with its recurrence rule, never expanded; the result is " +
		"limited and compact",
	Tags: []string{"infomaniak", "dav", "calendar", "event", "list"}, Risk: eventRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `,` + rangeArgSchemas + `,` +
		`"limit":{"type":"integer","minimum":1,"maximum":200}},"required":["calendar","start","end"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"calendar":{"type":"string"},` +
		`"events":{"type":"array","items":` + eventSchema + `},"count":{"type":"integer"},` +
		`"truncated":{"type":"boolean"}},"required":["calendar","events","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "calendar", Description: "ID of an allow-listed calendar, the ID of its calendar/ID target", Required: true},
		{Name: "start", Description: "Start of the range as an RFC 3339 time, for example 2026-03-01T00:00:00Z", Required: true},
		{Name: "end", Description: "End of the range as an RFC 3339 time after start, at most 366 days later", Required: true},
		{Name: "limit", Description: "Maximum number of events, 50 by default and at most 200"},
	},
	Fields: eventFields,
	Examples: []capability.Example{{
		Description: "List the events of one calendar in March 2026",
		Arguments:   json.RawMessage(`{"calendar":"work","start":"2026-03-01T00:00:00Z","end":"2026-04-01T00:00:00Z"}`),
	}},
}

var eventsGet = capability.Descriptor{
	ID: Provider + ".events.get", Version: 1, Title: "Read an Infomaniak calendar event",
	Description: "Read one event of an allow-listed calendar with its structured fields and entity tag; a " +
		"repeating event carries its recurrence rule, never expanded",
	Tags: []string{"infomaniak", "dav", "calendar", "event", "get"}, Risk: eventRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `,` +
		`"id":{"type":"string","minLength":1,"maxLength":128}},"required":["calendar","id"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"calendar":{"type":"string"},` +
		`"event":` + eventSchema + `},"required":["calendar","event"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "calendar", Description: "ID of an allow-listed calendar, the ID of its calendar/ID target", Required: true},
		{Name: "id", Description: "Event id as the list tool reports it: one path segment of the event resource", Required: true},
	},
	Fields: eventFields,
	Examples: []capability.Example{{
		Description: "Read one event the list tool reported",
		Arguments:   json.RawMessage(`{"calendar":"work","id":"standup.ics"}`),
	}},
}

// Person is an organizer or attendee.
type Person struct {
	Address string `json:"address,omitempty"`
	Name    string `json:"name,omitempty"`
	Status  string `json:"status,omitempty"`
}

// Event is one calendar event. The list tool leaves the long and personal members out.
type Event struct {
	ID                 string   `json:"id"`
	UID                string   `json:"uid,omitempty"`
	Summary            string   `json:"summary,omitempty"`
	Description        string   `json:"description,omitempty"`
	Location           string   `json:"location,omitempty"`
	Start              string   `json:"start,omitempty"`
	End                string   `json:"end,omitempty"`
	AllDay             bool     `json:"all_day,omitempty"`
	Timezone           string   `json:"timezone,omitempty"`
	Status             string   `json:"status,omitempty"`
	RRule              string   `json:"rrule,omitempty"`
	Organizer          *Person  `json:"organizer,omitempty"`
	Attendees          []Person `json:"attendees,omitempty"`
	AttendeesTruncated bool     `json:"attendees_truncated,omitempty"`
	ETag               string   `json:"etag,omitempty"`
}

// EventsPage is the result of the list tool.
type EventsPage struct {
	Calendar  string  `json:"calendar"`
	Events    []Event `json:"events"`
	Count     int     `json:"count"`
	Truncated bool    `json:"truncated,omitempty"`
}

// EventResult is the result of the get tool.
type EventResult struct {
	Calendar string `json:"calendar"`
	Event    Event  `json:"event"`
}

type eventArguments struct {
	Calendar string `json:"calendar"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Limit    int    `json:"limit"`
	ID       string `json:"id"`
}

// allowedCalendar checks the calendar argument against the allow-list before any secret or request.
func allowedCalendar(op string, resolved *config.Resolved, calendar string) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if !validCollectionID(calendar) || !contains(bound.calendars, calendar) {
		return &provider.Error{Class: provider.ClassPermission, Op: op,
			Message: "this connection does not allow this calendar"}
	}
	return nil
}

func decodeEventArguments(op string, raw json.RawMessage) (eventArguments, error) {
	var input eventArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

func invokeEventsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list events"
	input, err := decodeEventArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if err := allowedCalendar(op, resolved, input.Calendar); err != nil {
		return nil, err
	}
	from, to, err := parseRange(op, input.Start, input.End)
	if err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultEventLimit
	}
	if limit < 0 || limit > maxEventLimit {
		return nil, providerError(op, "limit must be between 1 and 200")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.listEvents(ctx, input.Calendar, from, to, limit)
}

func invokeEventsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read event"
	input, err := decodeEventArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if err := allowedCalendar(op, resolved, input.Calendar); err != nil {
		return nil, err
	}
	if !validCollectionID(input.ID) {
		return nil, providerError(op, "the event id must be one literal path segment as the list tool reports it")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.getEvent(ctx, input.Calendar, input.ID)
}

// parseRange validates the RFC 3339 range and returns it in the UTC form of a CalDAV time-range.
func parseRange(op, start, end string) (string, string, error) {
	from, err := time.Parse(time.RFC3339, start)
	if err != nil {
		return "", "", providerError(op, "start must be an RFC 3339 time")
	}
	to, err := time.Parse(time.RFC3339, end)
	if err != nil {
		return "", "", providerError(op, "end must be an RFC 3339 time")
	}
	if !to.After(from) || to.Sub(from) > maxRange {
		return "", "", providerError(op, "the range must end after it starts and span at most 366 days")
	}
	return from.UTC().Format(utcStampLayout), to.UTC().Format(utcStampLayout), nil
}

// calendarHome runs the discovery chain up to the calendar home set.
func (c *Client) calendarHome(ctx context.Context, op string) ([]string, error) {
	principal, err := c.discoverPrincipal(ctx, op)
	if err != nil {
		return nil, err
	}
	resources, err := c.propfind(ctx, op, principal, "0", calendarHomeBody)
	if err != nil {
		return nil, err
	}
	return singleLink(op, resources, propCalHome)
}

func (c *Client) listEvents(ctx context.Context, calendar, from, to string, limit int) (*EventsPage, error) {
	const op = "list events"
	home, err := c.calendarHome(ctx, op)
	if err != nil {
		return nil, err
	}
	base := append(append([]string{}, home...), calendar)
	body := fmt.Sprintf(reportBody, from, to)
	data, _, err := c.request(ctx, op, methodReport, base, true, "1", body, 207, maxResponseBytes)
	if err != nil {
		return nil, err
	}
	resources, err := parseMultiStatus(op, data)
	if err != nil {
		return nil, err
	}
	page := &EventsPage{Calendar: calendar, Events: []Event{}}
	seen := map[string]bool{}
	for i := range resources {
		id, err := eventID(op, resources[i].href, base)
		if err != nil {
			return nil, err
		}
		if resources[i].failure(op) != nil || seen[id] {
			continue
		}
		seen[id] = true
		event, err := parseEvent(resources[i].text[keyCalendar], false)
		if err != nil {
			continue
		}
		event.ID = id
		event.ETag = etagOf(resources[i].text[keyETag])
		page.Events = append(page.Events, *event)
	}
	sort.Slice(page.Events, func(i, j int) bool {
		a, b := page.Events[i], page.Events[j]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		return a.ID < b.ID
	})
	if len(page.Events) > limit {
		page.Events, page.Truncated = page.Events[:limit], true
	}
	page.Count = len(page.Events)
	return page, nil
}

// eventID binds an href of the answer to the calendar: it must be a direct child with a valid id.
func eventID(op, href string, base []string) (string, error) {
	segments, err := segmentsOf(op, href)
	if err != nil {
		return "", err
	}
	if len(segments) != len(base)+1 || !equalSegments(segments[:len(base)], base) ||
		!validCollectionID(segments[len(base)]) {
		return "", invalidResponse(op, "Infomaniak answered with a node outside the requested calendar")
	}
	return segments[len(base)], nil
}

func (c *Client) getEvent(ctx context.Context, calendar, id string) (*EventResult, error) {
	const op = "read event"
	home, err := c.calendarHome(ctx, op)
	if err != nil {
		return nil, err
	}
	path := append(append([]string{}, home...), calendar, id)
	data, header, err := c.request(ctx, op, methodGet, path, false, "", "", 200, maxEventBytes)
	if err != nil {
		return nil, err
	}
	event, err := parseEvent(string(data), true)
	if err != nil {
		return nil, invalidResponse(op, "Infomaniak returned an event that is not a valid iCalendar object")
	}
	event.ID = id
	event.ETag = etagOf(header.Get("ETag"))
	return &EventResult{Calendar: calendar, Event: *event}, nil
}

func etagOf(value string) string {
	return clean(strings.Trim(strings.TrimSpace(value), `"`), maxEventETagLength)
}

var errNoEvent = errors.New("no event")

// parseEvent reads the first event of an iCalendar object, preferring the main one of a recurring series
// over its overrides. With full unset, description, organizer, and attendees are left out.
func parseEvent(data string, full bool) (event *Event, err error) {
	defer func() {
		if recover() != nil {
			event, err = nil, errNoEvent
		}
	}()
	calendar, err := ical.NewDecoder(strings.NewReader(data)).Decode()
	if err != nil {
		return nil, err
	}
	var chosen *ical.Component
	for _, child := range calendar.Children {
		if child.Name != ical.CompEvent {
			continue
		}
		if child.Props.Get(ical.PropRecurrenceID) == nil {
			chosen = child
			break
		}
		if chosen == nil {
			chosen = child
		}
	}
	if chosen == nil {
		return nil, errNoEvent
	}
	event = &Event{
		UID:      clean(textOf(chosen, ical.PropUID), maxEventTextLength),
		Summary:  clean(textOf(chosen, ical.PropSummary), maxSummaryLength),
		Location: clean(textOf(chosen, ical.PropLocation), maxLocationLength),
		Status:   clean(textOf(chosen, ical.PropStatus), 32),
	}
	event.Start, event.AllDay, event.Timezone = timeOf(chosen.Props.Get(ical.PropDateTimeStart))
	event.End, _, _ = timeOf(chosen.Props.Get(ical.PropDateTimeEnd))
	if prop := chosen.Props.Get(ical.PropRecurrenceRule); prop != nil {
		event.RRule = clean(prop.Value, maxRRuleLength)
	}
	if !full {
		return event, nil
	}
	event.Description = cleanText(textOf(chosen, ical.PropDescription), maxEventDescription, true)
	if prop := chosen.Props.Get(ical.PropOrganizer); prop != nil {
		person := personOf(prop)
		event.Organizer = &person
	}
	for _, prop := range chosen.Props.Values(ical.PropAttendee) {
		if len(event.Attendees) >= maxAttendees {
			event.AttendeesTruncated = true
			break
		}
		event.Attendees = append(event.Attendees, personOf(&prop))
	}
	return event, nil
}

func textOf(component *ical.Component, name string) string {
	prop := component.Props.Get(name)
	if prop == nil {
		return ""
	}
	if text, err := prop.Text(); err == nil {
		return text
	}
	return prop.Value
}

func personOf(prop *ical.Prop) Person {
	address := strings.TrimSpace(prop.Value)
	if len(address) >= 7 && strings.EqualFold(address[:7], "mailto:") {
		address = address[7:]
	}
	return Person{
		Address: clean(address, maxEventTextLength),
		Name:    clean(prop.Params.Get(ical.ParamCommonName), maxEventTextLength),
		Status:  clean(prop.Params.Get(ical.ParamParticipationStatus), 32),
	}
}

// timeOf renders a DTSTART or DTEND value without resolving zones: a date, a UTC time, or a local time with
// the zone name of the property. A value that is none of these yields nothing.
func timeOf(prop *ical.Prop) (value string, allDay bool, zone string) {
	if prop == nil {
		return "", false, ""
	}
	raw := strings.TrimSpace(prop.Value)
	if t, err := time.Parse("20060102", raw); err == nil {
		return t.Format("2006-01-02"), true, ""
	}
	if t, err := time.Parse("20060102T150405Z", raw); err == nil {
		return t.Format("2006-01-02T15:04:05Z"), false, "UTC"
	}
	if t, err := time.Parse("20060102T150405", raw); err == nil {
		return t.Format("2006-01-02T15:04:05"), false, clean(prop.Params.Get(ical.ParamTimezoneID), 64)
	}
	return "", false, ""
}
