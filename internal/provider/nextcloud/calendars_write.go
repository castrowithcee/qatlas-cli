package nextcloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Calendar management: MKCALENDAR creates a calendar, PROPPATCH renames or recolours it, and DELETE removes
// it (Nextcloud moves it to its calendar trash bin). Update and delete send exactly one request after one
// read-only pre-check that refuses a missing, foreign, or shared calendar locally.

const (
	methodMkcalendar = "MKCALENDAR"

	maxCalendarTitle   = 128
	calendarNameSchema = `"name":{"type":"string","minLength":1,"maxLength":128}`
	calendarColorExpr  = `^#[0-9A-Fa-f]{6}([0-9A-Fa-f]{2})?$`

	calendarUncertain = "; the change may have been applied, check the calendar with calendar.calendars.list " +
		"before repeating it"
)

var calendarColorPattern = regexp.MustCompile(calendarColorExpr)

// calendarOwnProbeBody reads what decides whether a calendar is a calendar of the identity.
const calendarOwnProbeBody = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<d:propfind xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:prop>` +
	`<d:resourcetype/><oc:owner-principal/>` +
	`</d:prop></d:propfind>`

const calendarWriteOutProps = `"id":{"type":"string"}`

var calendarColorArgument = capability.Argument{
	Name: "color", Description: "Colour as #RRGGBB or #RRGGBBAA",
}

var calendarsCreate = capability.Descriptor{
	ID: Provider + ".calendar.calendars.create", Version: 1, Title: "Create a Nextcloud calendar",
	Description: "Create one calendar of the bound Nextcloud identity with a display name, an optional colour, and " +
		"the component types it takes (events, tasks, or both). Qatlas generates the calendar id; nothing raw " +
		"comes from an argument. It needs the general calendar target of the connection and binds the new " +
		"calendar. One MKCALENDAR follows without a pre-check; the request is never repeated after an unclear " +
		"result",
	Tags: []string{"nextcloud", "calendar", "create"}, Provider: Provider,
	Risk: eventWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarNameSchema + `,` +
		`"color":{"type":"string","pattern":"` + calendarColorExpr + `"},` +
		`"components":{"type":"array","minItems":1,"maxItems":2,"uniqueItems":true,` +
		`"items":{"type":"string","enum":["VEVENT","VTODO"]}}},` +
		`"required":["name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarWriteOutProps + `,` +
		`"created":{"type":"boolean"}},"required":["id","created"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "name", Description: "Display name, at most 128 characters, no control characters", Required: true},
		calendarColorArgument,
		{Name: "components", Description: "Component types as a subset of VEVENT and VTODO; both by default"},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Generated id of the calendar, the calendar argument of the other calendar tools"},
		{Name: "created", Description: "True when Nextcloud created the calendar"},
	},
	Examples: []capability.Example{{Description: "Create a calendar for events",
		Arguments: json.RawMessage(`{"name":"Projects","color":"#3366CC","components":["VEVENT"]}`)}},
}

var calendarsUpdate = capability.Descriptor{
	ID: Provider + ".calendar.calendars.update", Version: 1, Title: "Change a Nextcloud calendar",
	Description: "Change the display name and/or the colour of one bound calendar that belongs to the identity; a " +
		"calendar shared by someone else is refused. One read-only pre-check of the calendar follows, then one " +
		"PROPPATCH; a partly refused change is reported. The request is never repeated after an unclear result",
	Tags: []string{"nextcloud", "calendar", "update"}, Provider: Provider,
	Risk: eventWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `,` + calendarNameSchema + `,` +
		`"color":{"type":"string","pattern":"` + calendarColorExpr + `"}},` +
		`"required":["calendar"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarWriteOutProps + `,` +
		`"updated":{"type":"boolean"}},"required":["id","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{calendarArgument,
		{Name: "name", Description: "New display name, at most 128 characters; name or color is required"},
		{Name: "color", Description: "New colour as #RRGGBB or #RRGGBBAA; name or color is required"}},
	Fields: []capability.Field{
		{Name: "id", Description: "Id of the calendar"},
		{Name: "updated", Description: "True when Nextcloud stored the change"},
	},
	Examples: []capability.Example{{Description: "Rename a calendar",
		Arguments: json.RawMessage(`{"calendar":"projects","name":"Projects 2026"}`)}},
}

var calendarsDelete = capability.Descriptor{
	ID: Provider + ".calendar.calendars.delete", Version: 1, Title: "Delete a Nextcloud calendar",
	Description: "Delete one bound calendar that belongs to the identity, with all its events; a calendar shared " +
		"by someone else is refused. Nextcloud moves the calendar to its calendar trash bin; Qatlas offers no " +
		"restore. One read-only pre-check of the calendar follows, then one DELETE. The request is never " +
		"repeated after an unclear result",
	Tags: []string{"nextcloud", "calendar", "delete"}, Provider: Provider,
	Risk: eventWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarArgSchema + `},` +
		`"required":["calendar"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + calendarWriteOutProps + `,` +
		`"deleted":{"type":"boolean"}},"required":["id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{calendarArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Id of the calendar"},
		{Name: "deleted", Description: "True when Nextcloud deleted the calendar"},
	},
	Examples: []capability.Example{{Description: "Delete a calendar", Arguments: json.RawMessage(`{"calendar":"projects"}`)}},
	// Reachable only through a tools list, never through a profile.
	RequiresToolAllowList: true,
}

// CalendarWriteResult is the result of the calendar create, update, and delete tools.
type CalendarWriteResult struct {
	ID      string `json:"id"`
	Created bool   `json:"created,omitempty"`
	Updated bool   `json:"updated,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
}

type calendarWriteArguments struct {
	Calendar   string   `json:"calendar"`
	Name       string   `json:"name"`
	Color      string   `json:"color"`
	Components []string `json:"components"`
}

// checkCalendarName returns the trimmed display name, or refuses an empty, long, or control-character name.
func checkCalendarName(op, name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || utf8.RuneCountInString(trimmed) > maxCalendarTitle || !utf8.ValidString(trimmed) {
		return "", providerError(op, "name must be 1 to 128 characters")
	}
	for _, r := range trimmed {
		if unicode.IsControl(r) {
			return "", providerError(op, "name must not contain control characters")
		}
	}
	return trimmed, nil
}

func checkCalendarColor(op, color string) (string, error) {
	if !calendarColorPattern.MatchString(color) {
		return "", providerError(op, "color must be #RRGGBB or #RRGGBBAA")
	}
	return strings.ToUpper(color), nil
}

// mkcalendarBody builds the MKCALENDAR body from checked values; every value is escaped.
func mkcalendarBody(name, color string, components []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<c:mkcalendar xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:d="DAV:" xmlns:a="http://apple.com/ns/ical/">` +
		`<d:set><d:prop><d:displayname>` + xmlText(name) + `</d:displayname>`)
	if color != "" {
		b.WriteString(`<a:calendar-color>` + xmlText(color) + `</a:calendar-color>`)
	}
	b.WriteString(`<c:supported-calendar-component-set>`)
	for _, component := range components {
		b.WriteString(`<c:comp name="` + xmlText(component) + `"/>`)
	}
	b.WriteString(`</c:supported-calendar-component-set></d:prop></d:set></c:mkcalendar>`)
	return b.String()
}

// proppatchBody sets the display name and/or the colour; an empty value is left out.
func proppatchBody(name, color string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<d:propertyupdate xmlns:d="DAV:" xmlns:a="http://apple.com/ns/ical/"><d:set><d:prop>`)
	if name != "" {
		b.WriteString(`<d:displayname>` + xmlText(name) + `</d:displayname>`)
	}
	if color != "" {
		b.WriteString(`<a:calendar-color>` + xmlText(color) + `</a:calendar-color>`)
	}
	b.WriteString(`</d:prop></d:set></d:propertyupdate>`)
	return b.String()
}

// normalComponents keeps VEVENT and VTODO in a fixed order; none given means both.
func normalComponents(op string, given []string) ([]string, error) {
	if len(given) == 0 {
		return []string{"VEVENT", "VTODO"}, nil
	}
	kept := []string{}
	for _, want := range []string{"VEVENT", "VTODO"} {
		if containsString(given, want) {
			kept = append(kept, want)
		}
	}
	for _, name := range given {
		if !containsString(kept, name) {
			return nil, providerError(op, "components may only name VEVENT and VTODO")
		}
	}
	return kept, nil
}

func invokeCalendarsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create calendar"
	var input calendarWriteArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	if input.Calendar != "" {
		return nil, providerError(op, "the id of a new calendar is generated")
	}
	bound, err := requireCalendar(resolved)
	if err != nil {
		return nil, err
	}
	// A calendar of a single-calendar binding could not be reached afterwards, so it is not created.
	if !bound.all {
		return nil, &provider.Error{Class: provider.ClassPermission, Op: op,
			Message: "creating a calendar needs the general calendar target of the connection"}
	}
	name, err := checkCalendarName(op, input.Name)
	if err != nil {
		return nil, err
	}
	color := ""
	if input.Color != "" {
		if color, err = checkCalendarColor(op, input.Color); err != nil {
			return nil, err
		}
	}
	components, err := normalComponents(op, input.Components)
	if err != nil {
		return nil, err
	}
	id, err := dav.RandomID()
	if err != nil || checkSegment(id) != nil {
		return nil, providerError(op, "a calendar id could not be generated")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	status, _, err := client.calendarSend(ctx, op, methodMkcalendar, client.calendarHome(id),
		mkcalendarBody(name, color, components), map[int]string{
			http.StatusMethodNotAllowed: "a calendar with this id already exists, nothing was created",
			http.StatusConflict:         "Nextcloud cannot create a calendar here, nothing was created",
		})
	if err != nil {
		return nil, err
	}
	if status != http.StatusCreated {
		return nil, invalidResponse(op, "Nextcloud answered the change with an unexpected status"+calendarUncertain)
	}
	return &CalendarWriteResult{ID: id, Created: true}, nil
}

func invokeCalendarsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "change calendar"
	var input calendarWriteArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	if input.Components != nil {
		return nil, providerError(op, "the components of a calendar cannot be changed")
	}
	name, color := "", ""
	var err error
	if input.Name == "" && input.Color == "" {
		return nil, providerError(op, "name or color is required")
	}
	if input.Name != "" {
		if name, err = checkCalendarName(op, input.Name); err != nil {
			return nil, err
		}
	}
	if input.Color != "" {
		if color, err = checkCalendarColor(op, input.Color); err != nil {
			return nil, err
		}
	}
	client, err := openCalendar(ctx, op, resolved, secrets, red, input.Calendar)
	if err != nil {
		return nil, err
	}
	if err := client.ownCalendar(ctx, op, input.Calendar); err != nil {
		return nil, err
	}
	path := client.calendarHome(input.Calendar)
	status, data, err := client.calendarSend(ctx, op, methodProppatch, path, proppatchBody(name, color), nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusMultiStatus {
		return nil, invalidResponse(op, "Nextcloud answered the change with an unexpected status"+calendarUncertain)
	}
	if err := client.proppatchResult(op, data, path, name != "", color != ""); err != nil {
		return nil, err
	}
	return &CalendarWriteResult{ID: input.Calendar, Updated: true}, nil
}

func invokeCalendarsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete calendar"
	var input calendarWriteArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	if input.Name != "" || input.Color != "" || input.Components != nil {
		return nil, providerError(op, "deleting a calendar takes only the calendar argument")
	}
	client, err := openCalendar(ctx, op, resolved, secrets, red, input.Calendar)
	if err != nil {
		return nil, err
	}
	if err := client.ownCalendar(ctx, op, input.Calendar); err != nil {
		return nil, err
	}
	status, _, err := client.calendarSend(ctx, op, http.MethodDelete, client.calendarHome(input.Calendar), "", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusNoContent && status != http.StatusOK {
		return nil, invalidResponse(op, "Nextcloud answered the change with an unexpected status"+calendarUncertain)
	}
	return &CalendarWriteResult{ID: input.Calendar, Deleted: true}, nil
}

// ownCalendar is the pre-check of update and delete: one PROPFIND of depth 0 on the bound calendar. A node
// that is no calendar, or whose owner is not reported as the identity itself, is refused without a change.
// A shared calendar reports its sharer as owner; an unreported owner counts as foreign.
func (c *Client) ownCalendar(ctx context.Context, op, calendar string) error {
	base := c.calendarHome(calendar)
	data, _, err := c.calendarRequest(ctx, op, methodPropfind, base, true, depthSelf, calendarOwnProbeBody,
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
	return c.ownedByIdentity(op, node, "calendar")
}

// ownedByIdentity refuses a node whose owner is not reported as the identity itself. A shared node reports
// its sharer as owner; an unreported owner counts as foreign.
func (c *Client) ownedByIdentity(op string, node *dav.Resource, noun string) error {
	owners := node.Links[dav.PropOwner]
	if len(owners) != 1 {
		return &provider.Error{Class: provider.ClassPermission, Op: op,
			Message: "the owner of this " + noun + " is not confirmed to be this identity, nothing was changed"}
	}
	segments, err := c.davServer().Segments(op, owners[0])
	if err != nil || len(segments) < 3 || segments[len(segments)-3] != "principals" ||
		segments[len(segments)-2] != "users" || segments[len(segments)-1] != c.user {
		return &provider.Error{Class: provider.ClassPermission, Op: op,
			Message: "this " + noun + " belongs to someone else, nothing was changed"}
	}
	return nil
}

func (c *Client) calendarSend(ctx context.Context, op, method string, segments []string, body string,
	refusals map[int]string) (int, []byte, error) {
	return c.collectionSend(ctx, op, method, segments, body, refusals, calendarUncertain)
}

// collectionSend is the single write path of the calendar and address book management tools. It sends
// exactly one request and never repeats it. A failure that leaves the outcome open carries hint; a refusal
// or a redirect is clear. refusals maps client error statuses to their own clear message. The body of a
// success is read only up to the response limit.
func (c *Client) collectionSend(ctx context.Context, op, method string, segments []string, body string,
	refusals map[int]string, hint string) (int, []byte, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+escapePath(segments)+"/", reader)
	if err != nil {
		return 0, nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	if body != "" {
		req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	}
	response, err := c.http.Do(req)
	if err != nil {
		return 0, nil, sentTransportError(op, err, hint)
	}
	defer response.Body.Close()
	status := response.StatusCode
	if message, ok := refusals[status]; ok {
		return 0, nil, providerError(op, message)
	}
	if status < 200 || status >= 300 {
		return 0, nil, sentDAVStatusError(op, response, hint)
	}
	if status != http.StatusMultiStatus {
		return status, nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, dav.MaxResponseBytes+1))
	if err != nil || int64(len(data)) > dav.MaxResponseBytes {
		return 0, nil, invalidResponse(op, "the Nextcloud response could not be read within the size limit"+hint)
	}
	return status, data, nil
}

// proppatchResult checks the per-property answer of a calendar PROPPATCH.
func (c *Client) proppatchResult(op string, data []byte, path []string, name, color bool) error {
	return c.patchResult(op, calendarUncertain, data, path, []patchProp{
		{name, dav.KeyName, "name"}, {color, dav.KeyColor, "color"}})
}

// patchProp is one property a PROPPATCH asked to set.
type patchProp struct {
	asked bool
	key   string
	label string
}

// patchResult checks the per-property answer of a PROPPATCH. The parser keeps only properties of a
// successful propstat, so a requested property that is missing was refused.
func (c *Client) patchResult(op, hint string, data []byte, path []string, props []patchProp) error {
	resources, err := c.davServer().ParseMultiStatus(op, data)
	if err != nil {
		return withUncertainty(err, hint)
	}
	if len(resources) != 1 {
		return invalidResponse(op, "Nextcloud did not answer with the requested node"+hint)
	}
	found, err := c.davServer().Segments(op, resources[0].Href)
	if err != nil || !equalSegments(found, path) {
		return invalidResponse(op, "Nextcloud did not answer with the requested node"+hint)
	}
	var failed, applied []string
	for _, p := range props {
		_, done := resources[0].Text[p.key]
		switch {
		case p.asked && done:
			applied = append(applied, p.label)
		case p.asked:
			failed = append(failed, p.label)
		}
	}
	switch {
	case len(failed) == 0:
		return nil
	case len(applied) == 0:
		return providerError(op, "Nextcloud refused the change of "+strings.Join(failed, " and ")+", nothing was changed")
	}
	return providerError(op, "Nextcloud changed the "+strings.Join(applied, " and ")+" but refused the change of "+
		strings.Join(failed, " and "))
}
