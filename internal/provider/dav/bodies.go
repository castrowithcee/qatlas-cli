package dav

import (
	"fmt"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The fixed request bodies. Each asks for exactly the properties the result is built from.
const (
	PrincipalBody = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<d:propfind xmlns:d="DAV:"><d:prop><d:current-user-principal/></d:prop></d:propfind>`
	CalendarHomeBody = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav"><d:prop>` +
		`<c:calendar-home-set/></d:prop></d:propfind>`
	AddressbookHomeBody = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:carddav"><d:prop>` +
		`<c:addressbook-home-set/></d:prop></d:propfind>`
	CalendarsBody = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" xmlns:a="http://apple.com/ns/ical/">` +
		`<d:prop><d:displayname/><d:resourcetype/><c:calendar-description/><a:calendar-color/></d:prop></d:propfind>`
	AddressbooksBody = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<d:propfind xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:carddav"><d:prop>` +
		`<d:displayname/><d:resourcetype/><c:addressbook-description/></d:prop></d:propfind>`
	// AddressbookQueryBody asks for every card of an address book with its entity tag.
	AddressbookQueryBody = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<card:addressbook-query xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">` +
		`<d:prop><d:getetag/><card:address-data/></d:prop></card:addressbook-query>`
)

const (
	utcStampLayout = "20060102T150405Z"
	maxRange       = 366 * 24 * time.Hour

	calendarFilter = `<c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VEVENT">` +
		`<c:time-range start="%s" end="%s"/></c:comp-filter></c:comp-filter></c:filter>`
	calendarQueryBody = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">` +
		`<d:prop><d:getetag/><c:calendar-data/></d:prop>` + calendarFilter + `</c:calendar-query>`
)

// ParseRange validates the RFC 3339 range and returns it in the UTC form of a CalDAV time-range.
func ParseRange(op, start, end string) (string, string, error) {
	from, err := time.Parse(time.RFC3339, start)
	if err != nil {
		return "", "", provider.Fail(op, "start must be an RFC 3339 time")
	}
	to, err := time.Parse(time.RFC3339, end)
	if err != nil {
		return "", "", provider.Fail(op, "end must be an RFC 3339 time")
	}
	if !to.After(from) || to.Sub(from) > maxRange {
		return "", "", provider.Fail(op, "the range must end after it starts and span at most 366 days")
	}
	return from.UTC().Format(utcStampLayout), to.UTC().Format(utcStampLayout), nil
}

// CalendarQueryBody asks for the events that overlap a range given in the form ParseRange returns.
func CalendarQueryBody(from, to string) string {
	return fmt.Sprintf(calendarQueryBody, from, to)
}
