package dav

import (
	"errors"
	"strings"
	"time"

	"github.com/emersion/go-ical"
)

// Bounds of the event model. Every string read from a server is capped; a recurring event is reported as its
// RRULE and never expanded.
const (
	MaxSummaryLength    = 256
	MaxEventDescription = 4096
	MaxLocationLength   = 256
	MaxAttendees        = 50
	maxEventTextLength  = 256
	maxRRuleLength      = 512
)

// Person is an organizer or attendee. An organizer carries only the parameters RFC 5545 documents for it.
type Person struct {
	Address          string   `json:"address,omitempty"`
	Name             string   `json:"name,omitempty"`
	Status           string   `json:"status,omitempty"`
	Role             string   `json:"role,omitempty"`
	CUType           string   `json:"cutype,omitempty"`
	RSVP             *bool    `json:"rsvp,omitempty"`
	Member           []string `json:"member,omitempty"`
	MemberTruncated  bool     `json:"member_truncated,omitempty"`
	DelegatedTo      []string `json:"delegated_to,omitempty"`
	DelegatedToCut   bool     `json:"delegated_to_truncated,omitempty"`
	DelegatedFrom    []string `json:"delegated_from,omitempty"`
	DelegatedFromCut bool     `json:"delegated_from_truncated,omitempty"`
	SentBy           string   `json:"sent_by,omitempty"`
	Dir              string   `json:"dir,omitempty"`
	Language         string   `json:"language,omitempty"`
}

// Event is one calendar event. A compact read leaves the long, personal, and later members out.
type Event struct {
	ID string `json:"id"`
	EventBody
	ETag               string      `json:"etag,omitempty"`
	Overrides          []EventBody `json:"overrides,omitempty"`
	OverridesTruncated bool        `json:"overrides_truncated,omitempty"`
}

var errNoEvent = errors.New("no event")

// ParseOccurrences reads every VEVENT of an iCalendar object as one compact event with its recurrence_id.
// A server fills such an object when it expanded a recurring event for a range.
func ParseOccurrences(data string) (events []*Event, err error) {
	defer func() {
		if recover() != nil {
			events, err = nil, errNoEvent
		}
	}()
	calendar, err := ical.NewDecoder(strings.NewReader(data)).Decode()
	if err != nil {
		return nil, err
	}
	for _, child := range calendar.Children {
		if child.Name != ical.CompEvent {
			continue
		}
		event := &Event{EventBody: bodyOf(child, false)}
		if prop := child.Props.Get(ical.PropRecurrenceID); prop != nil {
			event.RecurrenceID, _, _ = timeOf(prop)
		}
		events = append(events, event)
	}
	if len(events) == 0 {
		return nil, errNoEvent
	}
	return events, nil
}

// ParseEvent reads the first event of an iCalendar object, preferring the main one of a recurring series
// over its overrides. With full unset, only the compact members are filled: the description, the people,
// and every later member stay out, and overrides are not read.
func ParseEvent(data string, full bool) (event *Event, err error) {
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
	event = &Event{EventBody: bodyOf(chosen, full)}
	if !full {
		return event, nil
	}
	for _, child := range calendar.Children {
		if child.Name != ical.CompEvent || child == chosen || child.Props.Get(ical.PropRecurrenceID) == nil {
			continue
		}
		event.Overrides = appendCapped(event.Overrides, &event.OverridesTruncated, MaxOverrides, bodyOf(child, true))
	}
	return event, nil
}

// bodyOf reads one VEVENT component.
func bodyOf(chosen *ical.Component, full bool) EventBody {
	body := EventBody{
		UID:      Clean(textOf(chosen, ical.PropUID), maxEventTextLength),
		Summary:  Clean(textOf(chosen, ical.PropSummary), MaxSummaryLength),
		Location: Clean(textOf(chosen, ical.PropLocation), MaxLocationLength),
		Status:   Clean(textOf(chosen, ical.PropStatus), 32),
	}
	body.Start, body.AllDay, body.Timezone = timeOf(chosen.Props.Get(ical.PropDateTimeStart))
	var endZone string
	body.End, _, endZone = timeOf(chosen.Props.Get(ical.PropDateTimeEnd))
	if prop := chosen.Props.Get(ical.PropRecurrenceRule); prop != nil {
		body.RRule = Clean(prop.Value, maxRRuleLength)
	}
	if !full {
		return body
	}
	body.Description = CleanText(textOf(chosen, ical.PropDescription), MaxEventDescription, true)
	if prop := chosen.Props.Get(ical.PropOrganizer); prop != nil {
		person := personOf(prop, true)
		body.Organizer = &person
	}
	for _, prop := range chosen.Props.Values(ical.PropAttendee) {
		if len(body.Attendees) >= MaxAttendees {
			body.AttendeesTruncated = true
			break
		}
		body.Attendees = append(body.Attendees, personOf(&prop, false))
	}
	fullFields(&body, chosen, endZone)
	return body
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

// timeOf renders a DTSTART or DTEND value without resolving zones: a date, a UTC time, or a local time with
// the zone name of the property. A value that is none of these yields nothing.
func timeOf(prop *ical.Prop) (value string, allDay bool, zone string) {
	if prop == nil {
		return "", false, ""
	}
	return timeValue(prop.Value, prop.Params.Get(ical.ParamTimezoneID))
}

func timeValue(value, zoneName string) (string, bool, string) {
	raw := strings.TrimSpace(value)
	if t, err := time.Parse("20060102", raw); err == nil {
		return t.Format("2006-01-02"), true, ""
	}
	if t, err := time.Parse("20060102T150405Z", raw); err == nil {
		return t.Format("2006-01-02T15:04:05Z"), false, "UTC"
	}
	if t, err := time.Parse("20060102T150405", raw); err == nil {
		return t.Format("2006-01-02T15:04:05"), false, Clean(zoneName, 64)
	}
	return "", false, ""
}
