package dav

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/emersion/go-ical"
)

// Bounds of the full event read.
const (
	MaxOverrides       = 50
	maxEventList       = 20
	maxEventDates      = 100
	maxPersonLinks     = 10
	maxAddressLength   = 1024
	maxRawValueLength  = 64
	maxCommentLength   = 1024
	maxRequestStatus   = 256
	maxEventCategory   = 64
	maxContactLength   = 256
	maxResourceLength  = 64
	maxAlarmsPerEvent  = 20
	maxAlarmAttendees  = 20
	maxAlarmAttachment = 20
)

// EventBody is every member of an event except its resource identity: the same form describes the main
// component and each override.
type EventBody struct {
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

	EndTimezone      string          `json:"end_timezone,omitempty"`
	Duration         string          `json:"duration,omitempty"`
	ExDates          []string        `json:"exdates,omitempty"`
	ExDatesTruncated bool            `json:"exdates_truncated,omitempty"`
	RDates           []string        `json:"rdates,omitempty"`
	RDatesTruncated  bool            `json:"rdates_truncated,omitempty"`
	RecurrenceID     string          `json:"recurrence_id,omitempty"`
	RecurrenceRange  string          `json:"recurrence_range,omitempty"`
	Class            string          `json:"class,omitempty"`
	Transp           string          `json:"transp,omitempty"`
	Priority         *int            `json:"priority,omitempty"`
	Sequence         *int            `json:"sequence,omitempty"`
	Geo              string          `json:"geo,omitempty"`
	URL              string          `json:"url,omitempty"`
	Created          string          `json:"created,omitempty"`
	LastModified     string          `json:"last_modified,omitempty"`
	DTStamp          string          `json:"dtstamp,omitempty"`
	Categories       []string        `json:"categories,omitempty"`
	CategoriesCut    bool            `json:"categories_truncated,omitempty"`
	Comments         []string        `json:"comments,omitempty"`
	CommentsCut      bool            `json:"comments_truncated,omitempty"`
	Contacts         []string        `json:"contacts,omitempty"`
	ContactsCut      bool            `json:"contacts_truncated,omitempty"`
	Resources        []string        `json:"resources,omitempty"`
	ResourcesCut     bool            `json:"resources_truncated,omitempty"`
	RelatedTo        []Relation      `json:"related_to,omitempty"`
	RelatedToCut     bool            `json:"related_to_truncated,omitempty"`
	RequestStatus    []RequestStatus `json:"request_status,omitempty"`
	RequestStatusCut bool            `json:"request_status_truncated,omitempty"`
	Attachments      []Attachment    `json:"attachments,omitempty"`
	AttachmentsCut   bool            `json:"attachments_truncated,omitempty"`
	Alarms           []Alarm         `json:"alarms,omitempty"`
	AlarmsTruncated  bool            `json:"alarms_truncated,omitempty"`
}

// Relation is a RELATED-TO property.
type Relation struct {
	UID     string `json:"uid,omitempty"`
	RelType string `json:"reltype,omitempty"`
}

// RequestStatus is a REQUEST-STATUS property.
type RequestStatus struct {
	Code        string `json:"code,omitempty"`
	Description string `json:"description,omitempty"`
	Data        string `json:"data,omitempty"`
}

// Attachment is an ATTACH property that points to a resource. The address is text and never fetched.
type Attachment struct {
	URI        string `json:"uri,omitempty"`
	FormatType string `json:"format_type,omitempty"`
}

// AlarmAttendee is a recipient of an EMAIL alarm.
type AlarmAttendee struct {
	Address string `json:"address,omitempty"`
	Name    string `json:"name,omitempty"`
}

// Alarm is a VALARM component.
type Alarm struct {
	Action             string          `json:"action,omitempty"`
	Trigger            string          `json:"trigger,omitempty"`
	TriggerRelated     string          `json:"trigger_related,omitempty"`
	TriggerAbsolute    bool            `json:"trigger_absolute,omitempty"`
	Duration           string          `json:"duration,omitempty"`
	Repeat             *int            `json:"repeat,omitempty"`
	Description        string          `json:"description,omitempty"`
	Summary            string          `json:"summary,omitempty"`
	Attendees          []AlarmAttendee `json:"attendees,omitempty"`
	AttendeesTruncated bool            `json:"attendees_truncated,omitempty"`
	Attachments        []Attachment    `json:"attachments,omitempty"`
	AttachmentsCut     bool            `json:"attachments_truncated,omitempty"`
}

// appendCapped appends item unless the list is full; an item that does not fit marks the list as cut.
func appendCapped[T any](list []T, cut *bool, max int, item T) []T {
	if len(list) >= max {
		*cut = true
		return list
	}
	return append(list, item)
}

// fullFields fills the members that only the full read reports.
func fullFields(body *EventBody, comp *ical.Component, endZone string) {
	props := comp.Props
	if endZone != "UTC" {
		body.EndTimezone = endZone
	}
	body.Duration = rawOf(props.Get(ical.PropDuration), maxRawValueLength)
	for _, prop := range props.Values(ical.PropExceptionDates) {
		body.ExDates = dateList(body.ExDates, &body.ExDatesTruncated, prop)
	}
	for _, prop := range props.Values(ical.PropRecurrenceDates) {
		body.RDates = dateList(body.RDates, &body.RDatesTruncated, prop)
	}
	if prop := props.Get(ical.PropRecurrenceID); prop != nil {
		body.RecurrenceID, _, _ = timeOf(prop)
		body.RecurrenceRange = Clean(prop.Params.Get(ical.ParamRange), 32)
	}
	body.Class = Clean(textOf(comp, ical.PropClass), 32)
	body.Transp = Clean(textOf(comp, ical.PropTransparency), 32)
	body.Priority = intOf(props.Get(ical.PropPriority), 0, 9)
	body.Sequence = intOf(props.Get(ical.PropSequence), 0, 1<<31-1)
	body.Geo = geoOf(props.Get(ical.PropGeo))
	if prop := props.Get(ical.PropURL); prop != nil {
		body.URL = addressOf(prop.Value, false)
	}
	body.Created, _, _ = timeOf(props.Get(ical.PropCreated))
	body.LastModified, _, _ = timeOf(props.Get(ical.PropLastModified))
	body.DTStamp, _, _ = timeOf(props.Get(ical.PropDateTimeStamp))
	for _, prop := range props.Values(ical.PropCategories) {
		for _, text := range textValues(prop) {
			if text = Clean(text, maxEventCategory); text != "" {
				body.Categories = appendCapped(body.Categories, &body.CategoriesCut, maxEventList, text)
			}
		}
	}
	for _, prop := range props.Values(ical.PropResources) {
		for _, text := range textValues(prop) {
			if text = Clean(text, maxResourceLength); text != "" {
				body.Resources = appendCapped(body.Resources, &body.ResourcesCut, maxEventList, text)
			}
		}
	}
	for _, prop := range props.Values(ical.PropComment) {
		if text := CleanText(textOfProp(prop), maxCommentLength, true); text != "" {
			body.Comments = appendCapped(body.Comments, &body.CommentsCut, maxEventList, text)
		}
	}
	for _, prop := range props.Values(ical.PropContact) {
		if text := Clean(textOfProp(prop), maxContactLength); text != "" {
			body.Contacts = appendCapped(body.Contacts, &body.ContactsCut, maxEventList, text)
		}
	}
	for _, prop := range props.Values(ical.PropRelatedTo) {
		relation := Relation{UID: Clean(textOfProp(prop), maxEventTextLength),
			RelType: Clean(prop.Params.Get(ical.ParamRelationshipType), 32)}
		if relation.UID != "" {
			body.RelatedTo = appendCapped(body.RelatedTo, &body.RelatedToCut, maxEventList, relation)
		}
	}
	for _, prop := range props.Values(ical.PropRequestStatus) {
		if status := requestStatusOf(prop); status.Code != "" {
			body.RequestStatus = appendCapped(body.RequestStatus, &body.RequestStatusCut, maxEventList, status)
		}
	}
	for _, prop := range props.Values(ical.PropAttach) {
		if attachment, ok := attachmentOf(prop); ok {
			body.Attachments = appendCapped(body.Attachments, &body.AttachmentsCut, maxEventList, attachment)
		}
	}
	for _, child := range comp.Children {
		if child.Name == ical.CompAlarm {
			body.Alarms = appendCapped(body.Alarms, &body.AlarmsTruncated, maxAlarmsPerEvent, alarmOf(child))
		}
	}
}

func alarmOf(comp *ical.Component) Alarm {
	alarm := Alarm{
		Action:      Clean(textOf(comp, ical.PropAction), 32),
		Duration:    rawOf(comp.Props.Get(ical.PropDuration), maxRawValueLength),
		Repeat:      intOf(comp.Props.Get(ical.PropRepeat), 0, 1<<31-1),
		Description: CleanText(textOf(comp, ical.PropDescription), maxCommentLength, true),
		Summary:     Clean(textOf(comp, ical.PropSummary), MaxSummaryLength),
	}
	if prop := comp.Props.Get(ical.PropTrigger); prop != nil {
		alarm.Trigger = rawOf(prop, maxRawValueLength)
		alarm.TriggerRelated = Clean(prop.Params.Get(ical.ParamRelated), 32)
		alarm.TriggerAbsolute = strings.EqualFold(prop.Params.Get(ical.ParamValue), "DATE-TIME")
	}
	for _, prop := range comp.Props.Values(ical.PropAttendee) {
		person := personOf(&prop, false)
		alarm.Attendees = appendCapped(alarm.Attendees, &alarm.AttendeesTruncated, maxAlarmAttendees,
			AlarmAttendee{Address: person.Address, Name: person.Name})
	}
	for _, prop := range comp.Props.Values(ical.PropAttach) {
		if attachment, ok := attachmentOf(prop); ok {
			alarm.Attachments = appendCapped(alarm.Attachments, &alarm.AttachmentsCut, maxAlarmAttachment, attachment)
		}
	}
	return alarm
}

func personOf(prop *ical.Prop, organizer bool) Person {
	person := Person{
		Address:  Clean(mailboxOf(prop.Value), maxEventTextLength),
		Name:     Clean(prop.Params.Get(ical.ParamCommonName), maxEventTextLength),
		Status:   Clean(prop.Params.Get(ical.ParamParticipationStatus), 32),
		SentBy:   addressOf(prop.Params.Get(ical.ParamSentBy), true),
		Dir:      addressOf(prop.Params.Get(ical.ParamDir), false),
		Language: Clean(prop.Params.Get(ical.ParamLanguage), 64),
	}
	if organizer {
		return person
	}
	person.Role = Clean(prop.Params.Get(ical.ParamRole), 32)
	person.CUType = Clean(prop.Params.Get(ical.ParamCalendarUserType), 32)
	switch strings.ToUpper(strings.TrimSpace(prop.Params.Get(ical.ParamRSVP))) {
	case "TRUE":
		yes := true
		person.RSVP = &yes
	case "FALSE":
		no := false
		person.RSVP = &no
	}
	person.Member, person.MemberTruncated = addressList(prop.Params.Values(ical.ParamMember))
	person.DelegatedTo, person.DelegatedToCut = addressList(prop.Params.Values(ical.ParamDelegatedTo))
	person.DelegatedFrom, person.DelegatedFromCut = addressList(prop.Params.Values(ical.ParamDelegatedFrom))
	return person
}

func addressList(values []string) (list []string, cut bool) {
	for _, value := range values {
		if address := addressOf(value, true); address != "" {
			list = appendCapped(list, &cut, maxPersonLinks, address)
		}
	}
	return list, cut
}

// mailboxOf removes a mailto: scheme from a calendar user address.
func mailboxOf(value string) string {
	address := strings.TrimSpace(value)
	if len(address) >= 7 && strings.EqualFold(address[:7], "mailto:") {
		address = address[7:]
	}
	return address
}

// addressOf renders an untrusted address as bounded text; it is never fetched. An address with user
// information, a query, or a fragment can carry an access token and is left out. With person set, a mailto:
// scheme is removed as for attendees.
func addressOf(value string, person bool) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "?#") {
		return ""
	}
	if person {
		value = mailboxOf(value)
	}
	if parsed, err := url.Parse(value); err != nil || parsed.User != nil || parsed.RawQuery != "" ||
		parsed.Fragment != "" || parsed.ForceQuery {
		return ""
	}
	return Clean(value, maxAddressLength)
}

// attachmentOf reads an ATTACH that points to a resource; embedded binary data and addresses that may carry
// a token are left out.
func attachmentOf(prop ical.Prop) (Attachment, bool) {
	if strings.EqualFold(prop.Params.Get(ical.ParamValue), "BINARY") ||
		strings.EqualFold(prop.Params.Get(ical.ParamEncoding), "BASE64") ||
		strings.EqualFold(prop.Params.Get(ical.ParamEncoding), "8BIT") {
		return Attachment{}, false
	}
	address := addressOf(prop.Value, false)
	if address == "" {
		return Attachment{}, false
	}
	return Attachment{URI: address, FormatType: Clean(prop.Params.Get(ical.ParamFormatType), 128)}, true
}

// rawOf reports a property value as written; one longer than max is left out instead of cut.
func rawOf(prop *ical.Prop, max int) string {
	if prop == nil {
		return ""
	}
	value := Clean(prop.Value, 1<<12)
	if len(value) > max {
		return ""
	}
	return value
}

// intOf reads a whole number within [low, high]; anything else yields nothing.
func intOf(prop *ical.Prop, low, high int) *int {
	if prop == nil {
		return nil
	}
	number, err := strconv.Atoi(strings.TrimSpace(prop.Value))
	if err != nil || number < low || number > high {
		return nil
	}
	return &number
}

// geoOf keeps the GEO value as the raw text lat;lon after checking that both halves are numbers.
func geoOf(prop *ical.Prop) string {
	value := rawOf(prop, maxRawValueLength)
	parts := strings.Split(value, ";")
	if len(parts) != 2 {
		return ""
	}
	for _, part := range parts {
		if _, err := strconv.ParseFloat(strings.TrimSpace(part), 64); err != nil {
			return ""
		}
	}
	return value
}

// dateList adds the values of one EXDATE or RDATE property in the form of the start. A period stays raw.
func dateList(list []string, cut *bool, prop ical.Prop) []string {
	zone := prop.Params.Get(ical.ParamTimezoneID)
	for _, raw := range strings.Split(prop.Value, ",") {
		raw = strings.TrimSpace(raw)
		var value string
		if strings.Contains(raw, "/") {
			if len(raw) <= maxRawValueLength {
				value = Clean(raw, maxRawValueLength)
			}
		} else {
			value, _, _ = timeValue(raw, zone)
		}
		if value != "" {
			list = appendCapped(list, cut, maxEventDates, value)
		}
	}
	return list
}

func textOfProp(prop ical.Prop) string {
	if text, err := prop.Text(); err == nil {
		return text
	}
	return prop.Value
}

// textValues splits a text list property into its unescaped values.
func textValues(prop ical.Prop) []string {
	if values, err := prop.TextList(); err == nil {
		return values
	}
	return []string{prop.Value}
}

// requestStatusOf reads REQUEST-STATUS: statcode ";" statdesc [";" extdata].
func requestStatusOf(prop ical.Prop) RequestStatus {
	var parts []string
	var current strings.Builder
	value := prop.Value
	for i := 0; i < len(value); i++ {
		switch c := value[i]; {
		case c == '\\' && i+1 < len(value):
			i++
			switch value[i] {
			case 'n', 'N':
				current.WriteByte('\n')
			default:
				current.WriteByte(value[i])
			}
		case c == ';':
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteByte(c)
		}
	}
	parts = append(parts, current.String())
	status := RequestStatus{Code: Clean(parts[0], maxRequestStatus)}
	if len(parts) > 1 {
		status.Description = Clean(parts[1], maxRequestStatus)
	}
	if len(parts) > 2 {
		status.Data = Clean(parts[2], maxRequestStatus)
	}
	return status
}
