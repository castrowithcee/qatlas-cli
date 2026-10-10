package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// groupCalendar is the tool group of the calendar tools.
const groupCalendar = "calendar"

// calendarSensitivity classifies calendar results: calendar names and event contents are personal data.
const calendarSensitivity = "nextcloud-calendar"

// calendarsRoot are the fixed path segments of the calendar home; the user ID follows them.
var calendarsRoot = []string{"remote.php", "dav", "calendars"}

// Bounds of the calendar reads.
const (
	maxCalendars        = 200
	maxCalendarName     = 256
	defaultEventLimit   = 50
	maxEventLimit       = 200
	maxEventIDLength    = 255
	messageCalendarNone = "this connection is not bound to this calendar"
)

// calendarsBody asks for exactly the properties the calendar listing is built from.
const calendarsBody = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop>` +
	`<d:displayname/><d:resourcetype/><d:current-user-privilege-set/><c:supported-calendar-component-set/>` +
	`</d:prop></d:propfind>`

// calendarProbeBody is the connection test: only the resource type, to tell a calendar from another node.
const calendarProbeBody = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/></d:prop></d:propfind>`

// reservedCalendar reports the scheduling and trash collections that sit beside the calendars in the
// calendar home. They are never calendars of these tools, however they are bound or addressed.
func reservedCalendar(uri string) bool {
	for _, name := range []string{"inbox", "outbox", "trashbin"} {
		if strings.EqualFold(uri, name) {
			return true
		}
	}
	return false
}

// holdsCalendar reports whether the selection covers a calendar URI.
func (s selection) holdsCalendar(uri string) bool {
	return !reservedCalendar(uri) && s.holds(uri)
}

var calendarRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: calendarSensitivity,
}

const calendarSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
	`"read_only":{"type":"boolean"},"components":{"type":"array","items":{"type":"string"}}},` +
	`"required":["id","name","read_only","components"],"additionalProperties":false}`

const personSchema = `{"type":"object","properties":{"address":{"type":"string"},"name":{"type":"string"},` +
	`"status":{"type":"string"}},"additionalProperties":false}`

const eventSchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"uid":{"type":"string"},"summary":{"type":"string"},"description":{"type":"string"},` +
	`"location":{"type":"string"},"start":{"type":"string"},"end":{"type":"string"},"all_day":{"type":"boolean"},` +
	`"timezone":{"type":"string"},"status":{"type":"string"},"rrule":{"type":"string"},` +
	`"organizer":` + personSchema + `,"attendees":{"type":"array","items":` + personSchema + `},` +
	`"attendees_truncated":{"type":"boolean"},"etag":{"type":"string"}},` +
	`"required":["id"],"additionalProperties":false}`

const calendarArgSchema = `"calendar":{"type":"string","minLength":1,"maxLength":255}`

var calendarArgument = capability.Argument{
	Name: "calendar", Required: true,
	Description: "URI of a bound calendar, the id the calendars list reports",
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

var calendarsList = capability.Descriptor{
	ID: Provider + ".calendars.list", Version: 1, Title: "List Nextcloud calendars",
	Description: "List the calendars of the bound Nextcloud identity that the connection's calendar targets " +
		"bind, shared calendars included; subscriptions, the scheduling inbox and outbox, and the trash bin " +
		"are never listed; calendars only, no events",
	Tags:        []string{"nextcloud", "calendar", "list"},
	Risk:        calendarRisk,
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"calendars":{"type":"array","items":` +
		calendarSchema + `},"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["calendars","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{},
	Fields: []capability.Field{
		{Name: "id", Description: "URI of the calendar, the form of the calendar/URI target"},
		{Name: "name", Description: "Display name, untrusted data"},
		{Name: "read_only", Description: "True unless the identity may create and change events in the calendar"},
		{Name: "components", Description: "Supported component types, VEVENT and VTODO"},
		{Name: "count", Description: "Number of reported calendars"},
		{Name: "truncated", Description: "True when more calendars match than one listing reports"},
	},
	Examples: []capability.Example{{Description: "List the bound calendars", Arguments: json.RawMessage(`{}`)}},
}

var eventsList = capability.Descriptor{
	ID: Provider + ".events.list", Version: 1, Title: "List Nextcloud calendar events",
	Description: "List the events of one bound calendar that overlap a time range, ordered by start; a " +
		"repeating event is reported once with its recurrence rule, never expanded; the result is limited " +
		"and compact",
	Tags: []string{"nextcloud", "calendar", "event", "list"}, Risk: calendarRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `,` +
		`"start":{"type":"string","minLength":20,"maxLength":40},"end":{"type":"string","minLength":20,"maxLength":40},` +
		`"limit":{"type":"integer","minimum":1,"maximum":200}},"required":["calendar","start","end"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"calendar":{"type":"string"},` +
		`"events":{"type":"array","items":` + eventSchema + `},"count":{"type":"integer"},` +
		`"truncated":{"type":"boolean"}},"required":["calendar","events","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		calendarArgument,
		{Name: "start", Description: "Start of the range as an RFC 3339 time, for example 2026-03-01T00:00:00Z", Required: true},
		{Name: "end", Description: "End of the range as an RFC 3339 time after start, at most 366 days later", Required: true},
		{Name: "limit", Description: "Maximum number of events, 50 by default and at most 200"},
	},
	Fields: eventFields,
	Examples: []capability.Example{{
		Description: "List the events of one calendar in March 2026",
		Arguments:   json.RawMessage(`{"calendar":"personal","start":"2026-03-01T00:00:00Z","end":"2026-04-01T00:00:00Z"}`),
	}},
}

var eventsGet = capability.Descriptor{
	ID: Provider + ".events.get", Version: 1, Title: "Get a Nextcloud calendar event",
	Description: "Read one event of a bound calendar with its structured fields and entity tag; a repeating " +
		"event carries its recurrence rule, never expanded",
	Tags: []string{"nextcloud", "calendar", "event", "get"}, Risk: calendarRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `,` +
		`"id":{"type":"string","minLength":1,"maxLength":255}},"required":["calendar","id"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"calendar":{"type":"string"},` +
		`"event":` + eventSchema + `},"required":["calendar","event"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		calendarArgument,
		{Name: "id", Description: "Event id as the events list reports it: one path segment of the event resource", Required: true},
	},
	Fields: eventFields,
	Examples: []capability.Example{{
		Description: "Read one event the list tool reported",
		Arguments:   json.RawMessage(`{"calendar":"personal","id":"standup.ics"}`),
	}},
}

// Calendar is one bound calendar.
type Calendar struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	ReadOnly   bool     `json:"read_only"`
	Components []string `json:"components"`
}

// CalendarsResult is the result of the calendars list.
type CalendarsResult struct {
	Calendars []Calendar `json:"calendars"`
	Count     int        `json:"count"`
	Truncated bool       `json:"truncated,omitempty"`
}

// EventsPage is the result of the events list.
type EventsPage struct {
	Calendar  string      `json:"calendar"`
	Events    []dav.Event `json:"events"`
	Count     int         `json:"count"`
	Truncated bool        `json:"truncated,omitempty"`
}

// EventResult is the result of the events get.
type EventResult struct {
	Calendar string    `json:"calendar"`
	Event    dav.Event `json:"event"`
}

type eventArguments struct {
	Calendar string `json:"calendar"`
	Start    string `json:"start"`
	End      string `json:"end"`
	Limit    int    `json:"limit"`
	ID       string `json:"id"`
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

// checkEventID accepts one literal path segment, the form the events list reports.
func checkEventID(id string) bool {
	return len(id) <= maxEventIDLength && checkSegment(id) == nil
}

// openCalendar refuses a calendar the connection does not bind before any credential access or request,
// then opens the client. The refusal does not name the calendar.
func openCalendar(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, uri string) (*Client, error) {
	bound, err := requireCalendar(resolved)
	if err != nil {
		return nil, err
	}
	if checkSegment(uri) != nil || !bound.holdsCalendar(uri) {
		return nil, &provider.Error{Class: provider.ClassPermission, Op: op, Message: messageCalendarNone}
	}
	return open(ctx, resolved, secrets, red, false)
}

func invokeCalendarsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	bound, err := requireCalendar(resolved)
	if err != nil {
		return nil, err
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.listCalendars(ctx, bound)
}

func invokeEventsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list events"
	input, err := decodeEventArguments(op, raw)
	if err != nil {
		return nil, err
	}
	from, to, err := dav.ParseRange(op, input.Start, input.End)
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
	client, err := openCalendar(ctx, op, resolved, secrets, red, input.Calendar)
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
	if !checkEventID(input.ID) {
		return nil, providerError(op, "the event id must be one literal path segment as the events list reports it")
	}
	client, err := openCalendar(ctx, op, resolved, secrets, red, input.Calendar)
	if err != nil {
		return nil, err
	}
	return client.getEvent(ctx, input.Calendar, input.ID)
}

// davServer names the one origin a DAV answer may point to.
func (c *Client) davServer() dav.Server { return dav.Server{Name: "Nextcloud", Origin: c.origin} }

// calendarHome are the segments of the calendar home of the identity, followed by rel.
func (c *Client) calendarHome(rel ...string) []string {
	segments := append(append([]string{}, c.install...), calendarsRoot...)
	return append(append(segments, c.user), rel...)
}

// calendarRequest sends one of the fixed calendar requests to a path built from checked segments and
// returns the answer when it has the expected status. A collection is addressed with a trailing slash.
func (c *Client) calendarRequest(ctx context.Context, op, method string, segments []string, collection bool,
	depth, body string, want int, limit int64) ([]byte, http.Header, error) {
	target := c.origin + escapePath(segments)
	if collection {
		target += "/"
	}
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	if body != "" {
		req.ContentLength = int64(len(body))
		req.Header.Set("Content-Type", "application/xml; charset=utf-8")
		req.Header.Set("Accept", "application/xml")
		req.Header.Set("Depth", depth)
	} else {
		req.Header.Set("Accept", "text/calendar")
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, nil, provider.Transport(op, "Nextcloud", err)
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return nil, nil, invalidResponse(op, "Nextcloud answered with an unexpected success status")
		}
		return nil, nil, statusError(op, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, nil, invalidResponse(op, "the Nextcloud response could not be read within the size limit")
	}
	return data, response.Header, nil
}

// childOf returns the last segment of an href that names a direct child of base. Every other location,
// whatever the reason, yields false: a foreign host, a deeper or shallower path, or an unusable form.
func (c *Client) childOf(op, href string, base []string) (string, bool) {
	segments, err := c.davServer().Segments(op, href)
	if err != nil || len(segments) != len(base)+1 || !equalSegments(segments[:len(base)], base) {
		return "", false
	}
	return segments[len(base)], true
}

// writable applies the conservative rule: the identity may change a calendar only with the aggregate
// privilege all or write, or with both bind and write-content, which is what creating and editing an event
// takes. A missing or unreadable privilege set counts as read-only.
func writable(privileges []string) bool {
	has := map[string]bool{}
	for _, name := range privileges {
		has[name] = true
	}
	return has["all"] || has["write"] || has["bind"] && has["write-content"]
}

// componentsOf keeps the two component types the Calendar app creates, in the order the server names them.
func componentsOf(names []string) []string {
	kept := []string{}
	for _, name := range names {
		if (name == "VEVENT" || name == "VTODO") && !containsString(kept, name) {
			kept = append(kept, name)
		}
	}
	return kept
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// listCalendars reads the children of the calendar home with one PROPFIND of depth 1 and keeps the bound
// calendars that are direct children of it.
func (c *Client) listCalendars(ctx context.Context, bound selection) (*CalendarsResult, error) {
	const op = "list calendars"
	home := c.calendarHome()
	data, _, err := c.calendarRequest(ctx, op, methodPropfind, home, true, depthChildren, calendarsBody,
		http.StatusMultiStatus, dav.MaxResponseBytes)
	if err != nil {
		return nil, err
	}
	resources, err := c.davServer().ParseMultiStatus(op, data)
	if err != nil {
		return nil, err
	}
	result := &CalendarsResult{Calendars: []Calendar{}}
	seen := map[string]bool{}
	for i := range resources {
		uri, ok := c.childOf(op, resources[i].Href, home)
		if !ok || !resources[i].Read || !resources[i].Calendar || !bound.holdsCalendar(uri) || seen[uri] {
			continue
		}
		seen[uri] = true
		result.Calendars = append(result.Calendars, Calendar{
			ID:         uri,
			Name:       dav.Clean(resources[i].Text[dav.KeyName], maxCalendarName),
			ReadOnly:   !writable(resources[i].Privileges),
			Components: componentsOf(resources[i].Components),
		})
	}
	sort.Slice(result.Calendars, func(i, j int) bool { return result.Calendars[i].ID < result.Calendars[j].ID })
	if len(result.Calendars) > maxCalendars {
		result.Calendars, result.Truncated = result.Calendars[:maxCalendars], true
	}
	result.Count = len(result.Calendars)
	return result, nil
}

func (c *Client) listEvents(ctx context.Context, calendar, from, to string, limit int) (*EventsPage, error) {
	const op = "list events"
	base := c.calendarHome(calendar)
	data, _, err := c.calendarRequest(ctx, op, methodReport, base, true, depthChildren,
		dav.CalendarQueryBody(from, to), http.StatusMultiStatus, dav.MaxResponseBytes)
	if err != nil {
		return nil, err
	}
	resources, err := c.davServer().ParseMultiStatus(op, data)
	if err != nil {
		return nil, err
	}
	page := &EventsPage{Calendar: calendar, Events: []dav.Event{}}
	seen := map[string]bool{}
	for i := range resources {
		id, ok := c.childOf(op, resources[i].Href, base)
		if !ok || !checkEventID(id) || !resources[i].Read || seen[id] {
			continue
		}
		seen[id] = true
		event, err := dav.ParseEvent(resources[i].Text[dav.KeyCalendar], false)
		if err != nil {
			continue
		}
		event.ID = id
		event.ETag = dav.ETagOf(resources[i].Text[dav.KeyETag])
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

func (c *Client) getEvent(ctx context.Context, calendar, id string) (*EventResult, error) {
	const op = "read event"
	data, header, err := c.calendarRequest(ctx, op, http.MethodGet, c.calendarHome(calendar, id), false, "", "",
		http.StatusOK, dav.MaxEventBytes)
	if err != nil {
		return nil, err
	}
	event, err := dav.ParseEvent(string(data), true)
	if err != nil {
		return nil, invalidResponse(op, "Nextcloud returned an event that is not a valid iCalendar object")
	}
	event.ID = id
	event.ETag = dav.ETagOf(header.Get("ETag"))
	return &EventResult{Calendar: calendar, Event: *event}, nil
}

// testCalendars is the calendar part of the connection test: one PROPFIND of depth 0 on the calendar home
// for a calendar target, or on every bound calendar for calendar/URI targets. It proves that the identity
// reaches what the connection binds. Nothing is written and no metadata is reported.
func (c *Client) testCalendars(ctx context.Context, bound selection) (provider.Class, error) {
	const op = "test connection"
	probe := func(rel []string, calendar bool) (provider.Class, error) {
		data, _, err := c.calendarRequest(ctx, op, methodPropfind, c.calendarHome(rel...), true, depthSelf,
			calendarProbeBody, http.StatusMultiStatus, dav.MaxResponseBytes)
		if err == nil {
			var resources []dav.Resource
			if resources, err = c.davServer().ParseMultiStatus(op, data); err == nil {
				if len(resources) != 1 || !resources[0].Read || calendar && !resources[0].Calendar {
					err = invalidResponse(op, "Nextcloud did not answer with the requested calendar node")
				}
			}
		}
		if err == nil {
			return provider.ClassOK, nil
		}
		var providerErr *provider.Error
		if !errors.As(err, &providerErr) {
			return provider.ClassProviderError, nil
		}
		if providerErr.Class == provider.ClassProviderError && providerErr.Message == messageNotFound {
			if !calendar {
				return "", errors.New("this Nextcloud identity has no calendar home at this instance")
			}
			return "", errors.New("this Nextcloud identity does not hold a calendar this connection is bound to")
		}
		return providerErr.Class, nil
	}
	if bound.all {
		return probe(nil, false)
	}
	for _, uri := range bound.ids {
		if reservedCalendar(uri) {
			return "", errors.New("this connection binds a calendar target that is not a calendar")
		}
		if class, err := probe([]string{uri}, true); class != provider.ClassOK || err != nil {
			return class, err
		}
	}
	return provider.ClassOK, nil
}
