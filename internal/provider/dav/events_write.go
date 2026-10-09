package dav

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"time"

	// The zone database is embedded so that a time zone is checked the same way on every host.
	_ "time/tzdata"

	"github.com/emersion/go-ical"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	maxTimezoneLen = 64
	maxSequence    = 1000000
	localLayout    = "2006-01-02T15:04:05"
	utcLayout      = "2006-01-02T15:04:05Z"
	dateLayout     = "2006-01-02"
	icalLocal      = "20060102T150405"
	icalUTC        = "20060102T150405Z"
	icalDate       = "20060102"
)

var (
	timezonePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z0-9_+-]+){0,2}$`)
	rrulePattern    = regexp.MustCompile(`^[A-Za-z0-9=;,+-]+$`)
)

// PersonInput is an organizer or attendee to write.
type PersonInput struct {
	Address string `json:"address"`
	Name    string `json:"name"`
}

// EventInput is the structured content of an event to write.
type EventInput struct {
	Summary     string        `json:"summary"`
	Description string        `json:"description"`
	Location    string        `json:"location"`
	Start       string        `json:"start"`
	End         string        `json:"end"`
	AllDay      bool          `json:"all_day"`
	Timezone    string        `json:"timezone"`
	RRule       string        `json:"rrule"`
	Status      string        `json:"status"`
	Organizer   *PersonInput  `json:"organizer"`
	Attendees   []PersonInput `json:"attendees"`
}

// EventDraft is a validated event, ready to be encoded with a UID.
type EventDraft struct {
	summary, description, location, status, rrule, productID string
	start, end                                               *ical.Prop
	organizer                                                *PersonInput
	attendees                                                []PersonInput
}

func checkPerson(op string, person PersonInput) (PersonInput, error) {
	name, ok := ValidText(person.Name, maxEventTextLength, false)
	if !ok || strings.Contains(name, `"`) || !ValidAddress(person.Address) {
		return person, provider.Fail(op, "an organizer or attendee needs a plain e-mail address and a short name without control characters or quotes")
	}
	return PersonInput{Address: person.Address, Name: name}, nil
}

func validTimezone(name string) bool {
	if len(name) > maxTimezoneLen || name == "Local" || !timezonePattern.MatchString(name) {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

// validRRule accepts a bounded RRULE value that the recurrence parser understands. It is never expanded.
func validRRule(value string) bool {
	if value == "" || len(value) > maxRRuleLength || !rrulePattern.MatchString(value) {
		return false
	}
	prop := ical.NewProp(ical.PropRecurrenceRule)
	prop.Value = value
	props := ical.Props{}
	props.Set(prop)
	rule, err := props.RecurrenceRule()
	return err == nil && rule != nil
}

// timeProps validates the time arguments and builds DTSTART and DTEND.
func timeProps(op string, input EventInput) (start, end *ical.Prop, err error) {
	bad := func(message string) (*ical.Prop, *ical.Prop, error) { return nil, nil, provider.Fail(op, message) }
	if input.AllDay {
		if input.Timezone != "" {
			return bad("an all-day event takes no timezone")
		}
		from, err := time.Parse(dateLayout, input.Start)
		if err != nil {
			return bad("start must be a date YYYY-MM-DD for an all-day event")
		}
		to := from.AddDate(0, 0, 1)
		if input.End != "" {
			if to, err = time.Parse(dateLayout, input.End); err != nil || !to.After(from) {
				return bad("end must be a date YYYY-MM-DD after start for an all-day event")
			}
		}
		return dateProp(ical.PropDateTimeStart, from), dateProp(ical.PropDateTimeEnd, to), nil
	}
	if input.End == "" {
		return bad("end is required for an event with a time")
	}
	utc := strings.HasSuffix(input.Start, "Z")
	if utc != strings.HasSuffix(input.End, "Z") {
		return bad("start and end must both be UTC times or both be local times")
	}
	zone := input.Timezone
	if utc {
		if zone != "" && zone != "UTC" {
			return bad("a UTC time takes no other timezone")
		}
		from, err1 := time.Parse(utcLayout, input.Start)
		to, err2 := time.Parse(utcLayout, input.End)
		if err1 != nil || err2 != nil || !to.After(from) {
			return bad("start and end must be times YYYY-MM-DDTHH:MM:SSZ with end after start")
		}
		return valueProp(ical.PropDateTimeStart, from.Format(icalUTC), ""), valueProp(ical.PropDateTimeEnd, to.Format(icalUTC), ""), nil
	}
	if zone == "" {
		return bad("a local time needs a timezone; a UTC time ends in Z instead")
	}
	if !validTimezone(zone) {
		return bad("timezone must be an IANA time zone name such as Europe/Zurich")
	}
	loc, _ := time.LoadLocation(zone)
	from, err1 := time.ParseInLocation(localLayout, input.Start, loc)
	to, err2 := time.ParseInLocation(localLayout, input.End, loc)
	if err1 != nil || err2 != nil || !to.After(from) {
		return bad("start and end must be times YYYY-MM-DDTHH:MM:SS with end after start")
	}
	return valueProp(ical.PropDateTimeStart, from.Format(icalLocal), zone), valueProp(ical.PropDateTimeEnd, to.Format(icalLocal), zone), nil
}

func dateProp(name string, t time.Time) *ical.Prop {
	prop := valueProp(name, t.Format(icalDate), "")
	prop.SetValueType(ical.ValueDate)
	return prop
}

func valueProp(name, value, zone string) *ical.Prop {
	prop := ical.NewProp(name)
	prop.Value = value
	if zone != "" {
		prop.Params.Set(ical.ParamTimezoneID, zone)
	}
	return prop
}

// NewEventDraft validates every argument without any I/O. No message quotes a value. The product ID names
// the writer in the PRODID of the encoded object.
func NewEventDraft(op, productID string, input EventInput) (*EventDraft, error) {
	draft := &EventDraft{productID: productID}
	var ok bool
	if draft.summary, ok = ValidText(input.Summary, MaxSummaryLength, true); !ok || strings.TrimSpace(draft.summary) == "" {
		return nil, provider.Fail(op, "summary is required, at most 256 bytes, without control characters")
	}
	if draft.description, ok = ValidText(input.Description, MaxEventDescription, true); !ok {
		return nil, provider.Fail(op, "description must be at most 4096 bytes without control characters")
	}
	if draft.location, ok = ValidText(input.Location, MaxLocationLength, true); !ok {
		return nil, provider.Fail(op, "location must be at most 256 bytes without control characters")
	}
	switch status := strings.ToUpper(input.Status); status {
	case "", "TENTATIVE", "CONFIRMED", "CANCELLED":
		draft.status = status
	default:
		return nil, provider.Fail(op, "status must be TENTATIVE, CONFIRMED, or CANCELLED")
	}
	if input.RRule != "" {
		if !validRRule(input.RRule) {
			return nil, provider.Fail(op, "rrule must be a valid recurrence rule of at most 512 bytes without the RRULE: prefix")
		}
		draft.rrule = input.RRule
	}
	var err error
	if draft.start, draft.end, err = timeProps(op, input); err != nil {
		return nil, err
	}
	if input.Organizer != nil {
		person, err := checkPerson(op, *input.Organizer)
		if err != nil {
			return nil, err
		}
		draft.organizer = &person
	}
	if len(input.Attendees) > MaxAttendees {
		return nil, provider.Fail(op, "an event takes at most 50 attendees")
	}
	seen := map[string]bool{}
	for _, entry := range input.Attendees {
		person, err := checkPerson(op, entry)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(person.Address)
		if seen[key] {
			return nil, provider.Fail(op, "an attendee may be named only once")
		}
		seen[key] = true
		draft.attendees = append(draft.attendees, person)
	}
	if _, err := draft.Encode(op, "placeholder", 0, time.Now()); err != nil {
		return nil, err
	}
	return draft, nil
}

func personProp(name string, person PersonInput, attendee bool) ical.Prop {
	prop := ical.NewProp(name)
	prop.Value = "mailto:" + person.Address
	if person.Name != "" {
		prop.Params.Set(ical.ParamCommonName, person.Name)
	}
	if attendee {
		prop.Params.Set(ical.ParamParticipationStatus, "NEEDS-ACTION")
		prop.Params.Set(ical.ParamRSVP, "TRUE")
	}
	return *prop
}

// Encode renders the draft as one iCalendar object. Text values are escaped by the encoder library, so a
// line break in an argument can never start a property of its own.
func (d *EventDraft) Encode(op, uid string, sequence int, now time.Time) (string, error) {
	event := ical.NewEvent()
	event.Props.SetText(ical.PropUID, uid)
	event.Props.SetDateTime(ical.PropDateTimeStamp, now.UTC())
	event.Props.Set(d.start)
	event.Props.Set(d.end)
	event.Props.SetText(ical.PropSummary, d.summary)
	if d.description != "" {
		event.Props.SetText(ical.PropDescription, d.description)
	}
	if d.location != "" {
		event.Props.SetText(ical.PropLocation, d.location)
	}
	if d.status != "" {
		event.Props.SetText(ical.PropStatus, d.status)
	}
	if d.rrule != "" {
		rule := ical.NewProp(ical.PropRecurrenceRule)
		rule.Value = d.rrule
		event.Props.Set(rule)
	}
	if sequence > 0 {
		event.Props.SetText(ical.PropSequence, strconv.Itoa(sequence))
	}
	if d.organizer != nil {
		prop := personProp(ical.PropOrganizer, *d.organizer, false)
		event.Props.Set(&prop)
	}
	for _, person := range d.attendees {
		prop := personProp(ical.PropAttendee, person, true)
		event.Props.Add(&prop)
	}
	calendar := ical.NewCalendar()
	calendar.Props.SetText(ical.PropVersion, "2.0")
	calendar.Props.SetText(ical.PropProductID, d.productID)
	calendar.Children = append(calendar.Children, event.Component)
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(calendar); err != nil || buf.Len() > MaxEventBytes {
		return "", provider.Fail(op, "the event could not be encoded within the size limit")
	}
	return buf.String(), nil
}

// ExistingEvent is the identity of a stored event.
type ExistingEvent struct {
	UID      string
	Sequence int
}

// ExistingEventOf reads the UID and the SEQUENCE of the stored event. It refuses an object that holds
// anything but exactly one plain event, so an update never silently drops overrides of single occurrences.
func (s Server) ExistingEventOf(op string, data []byte) (found ExistingEvent, err error) {
	notValid := s.Name + " returned an event that is not a valid iCalendar object"
	defer func() {
		if recover() != nil {
			found, err = ExistingEvent{}, provider.InvalidResponse(op, notValid)
		}
	}()
	calendar, decodeErr := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if decodeErr != nil {
		return ExistingEvent{}, provider.InvalidResponse(op, notValid)
	}
	var chosen *ical.Component
	for _, child := range calendar.Children {
		switch {
		case child.Name == ical.CompTimezone:
		case child.Name == ical.CompEvent && chosen == nil && child.Props.Get(ical.PropRecurrenceID) == nil:
			chosen = child
		default:
			return ExistingEvent{}, provider.Fail(op, "this event holds overrides of single occurrences or other components that Qatlas does not write; change it in a calendar application")
		}
	}
	if chosen == nil {
		return ExistingEvent{}, provider.InvalidResponse(op, s.Name+" returned an object without an event")
	}
	uid, ok := ValidText(textOf(chosen, ical.PropUID), maxEventTextLength, false)
	if !ok || strings.TrimSpace(uid) == "" {
		return ExistingEvent{}, provider.InvalidResponse(op, s.Name+" returned an event without a usable UID")
	}
	sequence := 0
	if prop := chosen.Props.Get(ical.PropSequence); prop != nil {
		if n, convErr := strconv.Atoi(strings.TrimSpace(prop.Value)); convErr == nil && n >= 0 && n < maxSequence {
			sequence = n
		}
	}
	return ExistingEvent{UID: uid, Sequence: sequence}, nil
}
