package infomaniakdav

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxNameLength        = 256
	maxDescriptionLength = 1024
	maxTokenLength       = 512
	maxLabelLength       = 64
	maxNameListed        = 32
	maxCollationsListed  = 16
)

var readRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: dataSensitivity,
}

const (
	stringList = `{"type":"array","items":{"type":"string"}}`
	formatList = `{"type":"array","items":{"type":"object","properties":{"content_type":{"type":"string"},` +
		`"version":{"type":"string"}},"additionalProperties":false}}`
)

const collectionSchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"name":{"type":"string"},"description":{"type":"string"},"color":{"type":"string"},` +
	`"components":` + stringList + `,"calendar_data":` + formatList + `,"address_data":` + formatList + `,` +
	`"max_resource_size":{"type":"integer","minimum":0},"min_date_time":{"type":"string"},` +
	`"max_date_time":{"type":"string"},"max_instances":{"type":"integer","minimum":0},` +
	`"max_attendees_per_instance":{"type":"integer","minimum":0},"timezone":{"type":"string"},` +
	`"collations":` + stringList + `,"privileges":` + stringList + `,"sync_token":{"type":"string"}},` +
	`"required":["id","name"],"additionalProperties":false}`

func listDescriptor(kind, object, title, description string) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + "." + object + ".list", Version: 1, Title: title, Description: description,
		Tags:        []string{"infomaniak", "dav", kind, "list"},
		Risk:        readRisk,
		Provider:    Provider,
		InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
			`"collections":{"type":"array","items":` + collectionSchema + `},"count":{"type":"integer"}},` +
			`"required":["collections","count"],"additionalProperties":false}`),
		Arguments: []capability.Argument{},
		Fields: []capability.Field{
			{Name: "id", Description: "Last path segment of the collection, the form of the " + kind + "/ID target"},
			{Name: "name", Description: "Display name, untrusted data"},
			{Name: "description", Description: "Description, untrusted data"},
			{Name: "color", Description: "Color as a hex value, only when the provider reports a valid one"},
			{Name: "components", Description: "Calendar: component names the calendar accepts, such as VEVENT, at most 32"},
			{Name: "calendar_data", Description: "Calendar: supported data formats as content_type and version, at most 8"},
			{Name: "address_data", Description: "Address book: supported data formats as content_type and version, at most 8"},
			{Name: "max_resource_size", Description: "Largest accepted object in bytes"},
			{Name: "min_date_time", Description: "Calendar: earliest accepted date as RFC 3339 in UTC"},
			{Name: "max_date_time", Description: "Calendar: latest accepted date as RFC 3339 in UTC"},
			{Name: "max_instances", Description: "Calendar: most instances of one recurring event"},
			{Name: "max_attendees_per_instance", Description: "Calendar: most attendees of one event instance"},
			{Name: "timezone", Description: "Calendar: TZID of the default time zone, untrusted data"},
			{Name: "collations", Description: "Supported text collations, at most 16"},
			{Name: "privileges", Description: "Privileges of the bound identity in the DAV: namespace, at most 32"},
			{Name: "sync_token", Description: "Sync token of the collection, untrusted data"},
			{Name: "count", Description: "Number of reported collections"},
		},
		Examples: []capability.Example{{Description: "List the allow-listed collections", Arguments: json.RawMessage(`{}`)}},
	}
}

var calendarsList = listDescriptor("calendar", "calendars", "List Infomaniak calendars",
	"List the calendars of the bound Infomaniak identity that the connection's calendar/ID targets allow; "+
		"calendars only, no events")
var addressbooksList = listDescriptor("addressbook", "address-books", "List Infomaniak address books",
	"List the address books of the bound Infomaniak identity that the connection's addressbook/ID targets "+
		"allow; address books only, no contacts")

// Collection is one allow-listed calendar or address book.
type Collection struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color,omitempty"`

	Components              []string     `json:"components,omitempty"`
	CalendarData            []DataFormat `json:"calendar_data,omitempty"`
	AddressData             []DataFormat `json:"address_data,omitempty"`
	MaxResourceSize         *uint64      `json:"max_resource_size,omitempty"`
	MinDateTime             string       `json:"min_date_time,omitempty"`
	MaxDateTime             string       `json:"max_date_time,omitempty"`
	MaxInstances            *uint64      `json:"max_instances,omitempty"`
	MaxAttendeesPerInstance *uint64      `json:"max_attendees_per_instance,omitempty"`
	Timezone                string       `json:"timezone,omitempty"`
	Collations              []string     `json:"collations,omitempty"`
	Privileges              []string     `json:"privileges,omitempty"`
	SyncToken               string       `json:"sync_token,omitempty"`
}

// DataFormat is one supported data format of a collection.
type DataFormat struct {
	ContentType string `json:"content_type,omitempty"`
	Version     string `json:"version,omitempty"`
}

// CollectionsPage is the result of one list operation.
type CollectionsPage struct {
	Collections []Collection `json:"collections"`
	Count       int          `json:"count"`
}

func invokeCalendarsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	return invokeList(ctx, resolved, secrets, red, true)
}

func invokeAddressbooksList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	return invokeList(ctx, resolved, secrets, red, false)
}

func invokeList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, calendars bool) (any, error) {
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	allowed := bound.addressbooks
	if calendars {
		allowed = bound.calendars
	}
	if len(allowed) == 0 {
		// Nothing of this kind is allowed, so nothing is asked of Infomaniak and no secret is read.
		return &CollectionsPage{Collections: []Collection{}}, nil
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.list(ctx, calendars)
}

// discoverPrincipal runs the first discovery step and returns the segments of the principal.
func (c *Client) discoverPrincipal(ctx context.Context, op string) ([]string, error) {
	resources, err := c.propfind(ctx, op, nil, "0", dav.PrincipalBody)
	if err != nil {
		return nil, err
	}
	return singleLink(op, resources, dav.PropPrincipal)
}

// singleLink reads the one location a discovery property must carry.
func singleLink(op string, resources []dav.Resource, property xml.Name) ([]string, error) {
	var hrefs []string
	for i := range resources {
		if failure(&resources[i], op) != nil {
			continue
		}
		hrefs = append(hrefs, resources[i].Links[property]...)
	}
	if len(hrefs) != 1 {
		return nil, invalidResponse(op, "Infomaniak did not name exactly one location during discovery")
	}
	segments, err := server.Segments(op, hrefs[0])
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 {
		return nil, invalidResponse(op, "Infomaniak named the root as a discovery location")
	}
	return segments, nil
}

// list runs the discovery chain and reports the allow-listed collections of one kind.
func (c *Client) list(ctx context.Context, calendars bool) (*CollectionsPage, error) {
	allowed := c.scope.addressbooks
	if calendars {
		allowed = c.scope.calendars
	}
	return c.collect(ctx, calendars, func(id string) bool { return contains(allowed, id) })
}

// collect runs the discovery chain and reports the collections of one kind that keep accepts.
func (c *Client) collect(ctx context.Context, calendars bool, keep func(id string) bool) (*CollectionsPage, error) {
	op, homeProp, homeBody, body := "list address books", dav.PropBookHome, dav.AddressbookHomeBody, dav.AddressbooksBody
	if calendars {
		op, homeProp, homeBody, body = "list calendars", dav.PropCalHome, dav.CalendarHomeBody, dav.CalendarsBody
	}
	principal, err := c.discoverPrincipal(ctx, op)
	if err != nil {
		return nil, err
	}
	homeResources, err := c.propfind(ctx, op, principal, "0", homeBody)
	if err != nil {
		return nil, err
	}
	home, err := singleLink(op, homeResources, homeProp)
	if err != nil {
		return nil, err
	}
	resources, err := c.propfind(ctx, op, home, "1", body)
	if err != nil {
		return nil, err
	}

	page := &CollectionsPage{Collections: []Collection{}}
	seen := map[string]bool{}
	for i := range resources {
		segments, err := server.Segments(op, resources[i].Href)
		if err != nil {
			return nil, err
		}
		if len(segments) < len(home) || !equalSegments(segments[:len(home)], home) || len(segments) > len(home)+1 {
			return nil, invalidResponse(op, "Infomaniak answered with a node outside the discovered home set")
		}
		if len(segments) == len(home) || failure(&resources[i], op) != nil {
			continue
		}
		id := segments[len(segments)-1]
		kindOK := resources[i].Addressbook
		if calendars {
			kindOK = resources[i].Calendar
		}
		if !kindOK || !keep(id) || seen[id] {
			continue
		}
		seen[id] = true
		page.Collections = append(page.Collections, collectionOf(id, &resources[i], calendars))
	}
	sort.Slice(page.Collections, func(i, j int) bool { return page.Collections[i].ID < page.Collections[j].ID })
	page.Count = len(page.Collections)
	return page, nil
}

func equalSegments(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var colorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}(?:[0-9A-Fa-f]{2})?$`)

// colorOf keeps only a hex color; every other value is dropped.
func colorOf(value string, calendars bool) string {
	if calendars && colorPattern.MatchString(value) {
		return value
	}
	return ""
}

// collectionOf builds one collection from the properties the server reported. Every property is optional:
// one that is absent or unreadable is left out, never guessed.
func collectionOf(id string, r *dav.Resource, calendars bool) Collection {
	c := Collection{
		ID:              id,
		Name:            dav.Clean(r.Text[dav.KeyName], maxNameLength),
		Description:     dav.Clean(r.Text[dav.KeyDescription], maxDescriptionLength),
		Color:           colorOf(r.Text[dav.KeyColor], calendars),
		Collations:      names(r.Collations, maxCollationsListed),
		Privileges:      names(r.Privileges, maxNameListed),
		MaxResourceSize: numberOf(r.Text[dav.KeyMaxSize]),
		SyncToken:       dav.Clean(r.Text[dav.KeySyncToken], maxTokenLength),
	}
	if !calendars {
		c.AddressData = formats(r.AddressData)
		return c
	}
	c.Components = names(r.Components, maxNameListed)
	c.CalendarData = formats(r.CalendarData)
	c.MinDateTime = stampOf(r.Text[dav.KeyMinDate])
	c.MaxDateTime = stampOf(r.Text[dav.KeyMaxDate])
	c.MaxInstances = numberOf(r.Text[dav.KeyMaxInstances])
	c.MaxAttendeesPerInstance = numberOf(r.Text[dav.KeyMaxAttendees])
	c.Timezone = tzidOf(r.Text[dav.KeyTimezone])
	return c
}

func names(values []string, limit int) []string {
	var out []string
	for _, value := range values {
		if value = dav.Clean(value, maxLabelLength); value != "" && len(out) < limit {
			out = append(out, value)
		}
	}
	return out
}

func formats(values []dav.DataFormat) []DataFormat {
	var out []DataFormat
	for _, value := range values {
		format := DataFormat{ContentType: dav.Clean(value.ContentType, maxLabelLength),
			Version: dav.Clean(value.Version, maxLabelLength)}
		if format != (DataFormat{}) && len(out) < 8 {
			out = append(out, format)
		}
	}
	return out
}

// numberOf accepts only plain decimal digits that fit an unsigned 64-bit integer.
func numberOf(value string) *uint64 {
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return nil
	}
	return &n
}

// stampOf rewrites the CalDAV UTC form (20060102T150405Z) to RFC 3339; anything else is dropped.
func stampOf(value string) string {
	t, err := time.Parse("20060102T150405Z", value)
	if err != nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// tzidOf returns the TZID of the first VTIMEZONE in a calendar-timezone value. The value may be cut at the
// parser's text limit, so a TZID line counts only when a complete line follows it: a cut line never yields a
// partial name.
func tzidOf(value string) string {
	value = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(value)
	var lines []string
	for _, line := range strings.Split(value, "\n") {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if len(lines) > 0 {
				lines[len(lines)-1] += line[1:]
			}
			continue
		}
		lines = append(lines, line)
	}
	inZone := false
	for i, line := range lines {
		upper := strings.ToUpper(line)
		switch {
		case upper == "BEGIN:VTIMEZONE":
			inZone = true
		case upper == "END:VTIMEZONE":
			return ""
		case inZone && (strings.HasPrefix(upper, "TZID:") || strings.HasPrefix(upper, "TZID;")):
			colon := strings.Index(line, ":")
			if i == len(lines)-1 || colon < 0 {
				return ""
			}
			return dav.Clean(line[colon+1:], maxLabelLength)
		}
	}
	return ""
}
