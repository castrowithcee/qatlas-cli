package infomaniakdav

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	// The zone database is embedded so that a time zone is checked the same way on every host.
	_ "time/tzdata"

	"github.com/emersion/go-ical"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// writeUncertain is appended to a failure of a write whose request may have reached Infomaniak. The write
	// is never repeated.
	writeUncertain = "; the change may have taken effect, read it before repeating it"
	maxTimezoneLen = 64
	maxAddressLen  = 254
	maxSequence    = 1000000
	productID      = "-//Qatlas//Infomaniak DAV//EN"
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

type personInput struct {
	Address string `json:"address"`
	Name    string `json:"name"`
}

type eventWriteArguments struct {
	Calendar    string        `json:"calendar"`
	ID          string        `json:"id"`
	ETag        string        `json:"etag"`
	Summary     string        `json:"summary"`
	Description string        `json:"description"`
	Location    string        `json:"location"`
	Start       string        `json:"start"`
	End         string        `json:"end"`
	AllDay      bool          `json:"all_day"`
	Timezone    string        `json:"timezone"`
	RRule       string        `json:"rrule"`
	Status      string        `json:"status"`
	Organizer   *personInput  `json:"organizer"`
	Attendees   []personInput `json:"attendees"`
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

// eventDraft is a validated event, ready to be encoded with a UID.
type eventDraft struct {
	summary, description, location, status, rrule string
	start, end                                    *ical.Prop
	organizer                                     *personInput
	attendees                                     []personInput
}

// validText accepts valid UTF-8 without control characters; line breaks are allowed and end up escaped.
func validText(value string, max int, lines bool) (string, bool) {
	if lines {
		value = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(value)
	}
	if len(value) > max || !utf8.ValidString(value) {
		return "", false
	}
	for _, r := range value {
		if lines && (r == '\n' || r == '\t') {
			continue
		}
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return value, true
}

func validAddress(address string) bool {
	if len(address) < 3 || len(address) > maxAddressLen || strings.ContainsAny(address, " <>\",;:()\\") {
		return false
	}
	parsed, err := mail.ParseAddress(address)
	return err == nil && parsed.Address == address && parsed.Name == ""
}

func checkPerson(op string, person personInput) (personInput, error) {
	name, ok := validText(person.Name, maxEventTextLength, false)
	if !ok || strings.Contains(name, `"`) || !validAddress(person.Address) {
		return person, providerError(op, "an organizer or attendee needs a plain e-mail address and a short name without control characters or quotes")
	}
	return personInput{Address: person.Address, Name: name}, nil
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
func timeProps(op string, input eventWriteArguments) (start, end *ical.Prop, err error) {
	bad := func(message string) (*ical.Prop, *ical.Prop, error) { return nil, nil, providerError(op, message) }
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

// newEventDraft validates every argument without any I/O. No message quotes a value.
func newEventDraft(op string, input eventWriteArguments) (*eventDraft, error) {
	draft := &eventDraft{}
	var ok bool
	if draft.summary, ok = validText(input.Summary, maxSummaryLength, true); !ok || strings.TrimSpace(draft.summary) == "" {
		return nil, providerError(op, "summary is required, at most 256 bytes, without control characters")
	}
	if draft.description, ok = validText(input.Description, maxEventDescription, true); !ok {
		return nil, providerError(op, "description must be at most 4096 bytes without control characters")
	}
	if draft.location, ok = validText(input.Location, maxLocationLength, true); !ok {
		return nil, providerError(op, "location must be at most 256 bytes without control characters")
	}
	switch status := strings.ToUpper(input.Status); status {
	case "", "TENTATIVE", "CONFIRMED", "CANCELLED":
		draft.status = status
	default:
		return nil, providerError(op, "status must be TENTATIVE, CONFIRMED, or CANCELLED")
	}
	if input.RRule != "" {
		if !validRRule(input.RRule) {
			return nil, providerError(op, "rrule must be a valid recurrence rule of at most 512 bytes without the RRULE: prefix")
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
	if len(input.Attendees) > maxAttendees {
		return nil, providerError(op, "an event takes at most 50 attendees")
	}
	seen := map[string]bool{}
	for _, entry := range input.Attendees {
		person, err := checkPerson(op, entry)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(person.Address)
		if seen[key] {
			return nil, providerError(op, "an attendee may be named only once")
		}
		seen[key] = true
		draft.attendees = append(draft.attendees, person)
	}
	if _, err := draft.encode(op, "placeholder", 0, time.Now()); err != nil {
		return nil, err
	}
	return draft, nil
}

func personProp(name string, person personInput, attendee bool) ical.Prop {
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

// encode renders the draft as one iCalendar object. Text values are escaped by the encoder library, so a
// line break in an argument can never start a property of its own.
func (d *eventDraft) encode(op, uid string, sequence int, now time.Time) (string, error) {
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
	calendar.Props.SetText(ical.PropProductID, productID)
	calendar.Children = append(calendar.Children, event.Component)
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(calendar); err != nil || buf.Len() > maxEventBytes {
		return "", providerError(op, "the event could not be encoded within the size limit")
	}
	return buf.String(), nil
}

// randomID returns a random version 4 UUID.
func randomID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	text := hex.EncodeToString(raw[:])
	return text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:], nil
}

// normalETag returns the entity tag in the form events.get reports it, or false for an unusable value. One
// surrounding pair of quotes is accepted.
func normalETag(value string) (string, bool) {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	if value == "" || value == "*" || len(value) > maxEventETagLength {
		return "", false
	}
	for i := 0; i < len(value); i++ {
		if value[i] <= 0x20 || value[i] >= 0x7f || value[i] == '"' {
			return "", false
		}
	}
	return value, true
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
	etag, ok := normalETag(input.ETag)
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
	draft, err := newEventDraft(op, input)
	if err != nil {
		return nil, err
	}
	uid, err := randomID()
	if err != nil {
		return nil, providerError(op, "an event id could not be generated")
	}
	body, err := draft.encode(op, uid, 0, time.Now())
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
	return &WriteResult{Calendar: input.Calendar, ID: id, UID: uid, ETag: etagOf(header.Get("ETag")), Created: true}, nil
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
	draft, err := newEventDraft(op, input)
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
	current, err := existingOf(data)
	if err != nil {
		return nil, err
	}
	if served := header.Get("ETag"); served != "" && etagOf(served) != etag {
		return nil, errPrecondition(op, "event")
	}
	body, err := draft.encode(op, current.uid, current.sequence+1, time.Now())
	if err != nil {
		return nil, err
	}
	out, err := client.mutate(ctx, op, eventKind, methodPut, path, etag, body)
	if err != nil {
		return nil, err
	}
	return &WriteResult{Calendar: input.Calendar, ID: input.ID, UID: current.uid, ETag: etagOf(out.Get("ETag")),
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

type existing struct {
	uid      string
	sequence int
}

// existingOf reads the UID and the SEQUENCE of the stored event. It refuses an object that holds anything
// but exactly one plain event, so an update never silently drops overrides of single occurrences.
func existingOf(data []byte) (found existing, err error) {
	const op = "update event"
	defer func() {
		if recover() != nil {
			found, err = existing{}, invalidResponse(op, "Infomaniak returned an event that is not a valid iCalendar object")
		}
	}()
	calendar, decodeErr := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if decodeErr != nil {
		return existing{}, invalidResponse(op, "Infomaniak returned an event that is not a valid iCalendar object")
	}
	var chosen *ical.Component
	for _, child := range calendar.Children {
		switch {
		case child.Name == ical.CompTimezone:
		case child.Name == ical.CompEvent && chosen == nil && child.Props.Get(ical.PropRecurrenceID) == nil:
			chosen = child
		default:
			return existing{}, providerError(op, "this event holds overrides of single occurrences or other components that Qatlas does not write; change it in a calendar application")
		}
	}
	if chosen == nil {
		return existing{}, invalidResponse(op, "Infomaniak returned an object without an event")
	}
	uid, ok := validText(textOf(chosen, ical.PropUID), maxEventTextLength, false)
	if !ok || strings.TrimSpace(uid) == "" {
		return existing{}, invalidResponse(op, "Infomaniak returned an event without a usable UID")
	}
	sequence := 0
	if prop := chosen.Props.Get(ical.PropSequence); prop != nil {
		if n, convErr := strconv.Atoi(strings.TrimSpace(prop.Value)); convErr == nil && n >= 0 && n < maxSequence {
			sequence = n
		}
	}
	return existing{uid: uid, sequence: sequence}, nil
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
		if errors.As(failure, &providerErr) && (providerErr.Class == provider.ClassTimeout ||
			providerErr.Cause == provider.CauseConnectionReset || providerErr.Cause == provider.CauseUnknown) {
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
