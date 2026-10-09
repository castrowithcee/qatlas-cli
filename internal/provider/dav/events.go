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

// Person is an organizer or attendee.
type Person struct {
	Address string `json:"address,omitempty"`
	Name    string `json:"name,omitempty"`
	Status  string `json:"status,omitempty"`
}

// Event is one calendar event. A compact read leaves the long and personal members out.
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

var errNoEvent = errors.New("no event")

// ParseEvent reads the first event of an iCalendar object, preferring the main one of a recurring series
// over its overrides. With full unset, description, organizer, and attendees are left out.
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
	event = &Event{
		UID:      Clean(textOf(chosen, ical.PropUID), maxEventTextLength),
		Summary:  Clean(textOf(chosen, ical.PropSummary), MaxSummaryLength),
		Location: Clean(textOf(chosen, ical.PropLocation), MaxLocationLength),
		Status:   Clean(textOf(chosen, ical.PropStatus), 32),
	}
	event.Start, event.AllDay, event.Timezone = timeOf(chosen.Props.Get(ical.PropDateTimeStart))
	event.End, _, _ = timeOf(chosen.Props.Get(ical.PropDateTimeEnd))
	if prop := chosen.Props.Get(ical.PropRecurrenceRule); prop != nil {
		event.RRule = Clean(prop.Value, maxRRuleLength)
	}
	if !full {
		return event, nil
	}
	event.Description = CleanText(textOf(chosen, ical.PropDescription), MaxEventDescription, true)
	if prop := chosen.Props.Get(ical.PropOrganizer); prop != nil {
		person := personOf(prop)
		event.Organizer = &person
	}
	for _, prop := range chosen.Props.Values(ical.PropAttendee) {
		if len(event.Attendees) >= MaxAttendees {
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
		Address: Clean(address, maxEventTextLength),
		Name:    Clean(prop.Params.Get(ical.ParamCommonName), maxEventTextLength),
		Status:  Clean(prop.Params.Get(ical.ParamParticipationStatus), 32),
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
		return t.Format("2006-01-02T15:04:05"), false, Clean(prop.Params.Get(ical.ParamTimezoneID), 64)
	}
	return "", false, ""
}
