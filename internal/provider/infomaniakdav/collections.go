package infomaniakdav

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"regexp"
	"sort"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxNameLength        = 256
	maxDescriptionLength = 1024
)

var readRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: dataSensitivity,
}

const collectionSchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"name":{"type":"string"},"description":{"type":"string"},"color":{"type":"string"}},` +
	`"required":["id","name"],"additionalProperties":false}`

func listDescriptor(kind, title, description string) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + "." + kind + "s.list", Version: 1, Title: title, Description: description,
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
			{Name: "count", Description: "Number of reported collections"},
		},
		Examples: []capability.Example{{Description: "List the allow-listed collections", Arguments: json.RawMessage(`{}`)}},
	}
}

var calendarsList = listDescriptor("calendar", "List Infomaniak calendars",
	"List the calendars of the bound Infomaniak identity that the connection's calendar/ID targets allow; "+
		"calendars only, no events")
var addressbooksList = listDescriptor("addressbook", "List Infomaniak address books",
	"List the address books of the bound Infomaniak identity that the connection's addressbook/ID targets "+
		"allow; address books only, no contacts")

// Collection is one allow-listed calendar or address book.
type Collection struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color,omitempty"`
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
	op, allowed, homeProp, homeBody, body := "list address books", c.scope.addressbooks, dav.PropBookHome,
		dav.AddressbookHomeBody, dav.AddressbooksBody
	if calendars {
		op, allowed, homeProp, homeBody, body = "list calendars", c.scope.calendars, dav.PropCalHome,
			dav.CalendarHomeBody, dav.CalendarsBody
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
		if !kindOK || !contains(allowed, id) || seen[id] {
			continue
		}
		seen[id] = true
		page.Collections = append(page.Collections, Collection{
			ID:          id,
			Name:        dav.Clean(resources[i].Text[dav.KeyName], maxNameLength),
			Description: dav.Clean(resources[i].Text[dav.KeyDescription], maxDescriptionLength),
			Color:       colorOf(resources[i].Text[dav.KeyColor], calendars),
		})
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
