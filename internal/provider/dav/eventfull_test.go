package dav

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// rfcEvent follows the examples of RFC 5545 section 3.6.1 and section 3.6.6 and carries every property the
// full read reports, one override with RANGE, and an EMAIL alarm.
var rfcEvent = icsOf(vevent(
	"DTSTAMP:19960704T120000Z", "UID:uid1@example.com",
	"ORGANIZER;CN=John Smith;DIR=\"ldap://example.com:6666/o=ABC,c=US??(cn=John%20Smith)\";SENT-BY=\"mailto:asst@example.com\";LANGUAGE=en:mailto:jsmith@example.com",
	"ATTENDEE;ROLE=REQ-PARTICIPANT;CUTYPE=GROUP;PARTSTAT=TENTATIVE;RSVP=TRUE;CN=Henry Cabot;LANGUAGE=de;",
	" MEMBER=\"mailto:ietf-calsch@example.org\",\"mailto:two@example.org\";DELEGATED-TO=\"mailto:jdoe@example.com\";",
	" DELEGATED-FROM=\"mailto:from@example.com\";SENT-BY=\"mailto:asst@example.com\";DIR=\"ldap://example.com/o=ABC\":mailto:hcabot@example.com",
	"DTSTART;TZID=America/New_York:19970902T090000", "DTEND;TZID=Europe/Zurich:19970902T170000",
	"DURATION:PT1H", "SUMMARY:Annual Employee Review", "CLASS:PRIVATE", "TRANSP:OPAQUE", "PRIORITY:2", "SEQUENCE:3",
	"GEO:37.386013;-122.082932", "URL:http://example.com/pub/calendars/jsmith/mytime.ics",
	"CREATED:19960329T133000Z", "LAST-MODIFIED:19960817T133000Z", "STATUS:CONFIRMED",
	"RRULE:FREQ=YEARLY", "EXDATE;TZID=America/New_York:19970902T090000,19980902T090000",
	"RDATE;VALUE=PERIOD:19960403T020000Z/19960403T040000Z", "RDATE;TZID=America/New_York:19990902T090000",
	"CATEGORIES:APPOINTMENT,EDUCATION", "CATEGORIES:MEETING", "COMMENT:Bring a laptop\\, please", "CONTACT:Jim Dolittle\\, ABC Industries",
	"RESOURCES:EASEL,PROJECTOR", "RELATED-TO;RELTYPE=PARENT:jsmith.part7.19960817T083000.xyzMail@example.com",
	"REQUEST-STATUS:2.0;Success", "REQUEST-STATUS:3.1;Invalid property value;DTSTART:96-Apr-01",
	"ATTACH;FMTTYPE=application/postscript:ftp://example.com/pub/reports/r-960812.ps",
	"ATTACH;FMTTYPE=text/plain;ENCODING=BASE64;VALUE=BINARY:VGhlIHF1aWNrIGJyb3du",
	"ATTACH:https://example.com/doc?token=secret", "ATTACH:https://user:pw@example.com/doc",
	"BEGIN:VALARM", "TRIGGER;RELATED=END:-P2D", "ACTION:EMAIL",
	"ATTENDEE;CN=Alert One:mailto:john_doe@example.com", "ATTENDEE:mailto:two@example.com",
	"SUMMARY:*** REMINDER: SEND AGENDA FOR WEEKLY STAFF MEETING ***",
	"DESCRIPTION:A draft agenda needs to be sent out to the attendees.", "REPEAT:4", "DURATION:PT15M",
	"ATTACH;FMTTYPE=application/msword:http://example.com/templates/agenda.doc", "END:VALARM",
	"BEGIN:VALARM", "TRIGGER;VALUE=DATE-TIME:19970317T133000Z", "ACTION:AUDIO", "END:VALARM",
), vevent(
	"DTSTAMP:19960704T120000Z", "UID:uid1@example.com", "RECURRENCE-ID;RANGE=THISANDFUTURE;TZID=America/New_York:19980902T090000",
	"SUMMARY:Moved review", "DTSTART;TZID=America/New_York:19980902T100000", "SEQUENCE:4",
))

func TestFullEventReadsEveryProperty(t *testing.T) {
	event, err := ParseEvent(rfcEvent, true)
	if err != nil {
		t.Fatal(err)
	}
	prio, seq := 2, 3
	yes := true
	repeat := 4
	if event.UID != "uid1@example.com" || event.Summary != "Annual Employee Review" || event.Status != "CONFIRMED" ||
		event.Start != "1997-09-02T09:00:00" || event.Timezone != "America/New_York" ||
		event.End != "1997-09-02T17:00:00" || event.EndTimezone != "Europe/Zurich" || event.Duration != "PT1H" ||
		event.RRule != "FREQ=YEARLY" || event.RecurrenceID != "" || event.RecurrenceRange != "" ||
		event.Class != "PRIVATE" || event.Transp != "OPAQUE" || *event.Priority != prio || *event.Sequence != seq ||
		event.Geo != "37.386013;-122.082932" || event.URL != "http://example.com/pub/calendars/jsmith/mytime.ics" ||
		event.Created != "1996-03-29T13:30:00Z" || event.LastModified != "1996-08-17T13:30:00Z" ||
		event.DTStamp != "1996-07-04T12:00:00Z" {
		t.Errorf("event = %+v", event)
	}
	for name, got := range map[string]any{
		"exdates":    event.ExDates,
		"rdates":     event.RDates,
		"categories": event.Categories,
		"comments":   event.Comments,
		"contacts":   event.Contacts,
		"resources":  event.Resources,
		"related_to": event.RelatedTo,
		"request":    event.RequestStatus,
		"attachment": event.Attachments,
	} {
		want := map[string]any{
			"exdates":    []string{"1997-09-02T09:00:00", "1998-09-02T09:00:00"},
			"rdates":     []string{"19960403T020000Z/19960403T040000Z", "1999-09-02T09:00:00"},
			"categories": []string{"APPOINTMENT", "EDUCATION", "MEETING"},
			"comments":   []string{"Bring a laptop, please"},
			"contacts":   []string{"Jim Dolittle, ABC Industries"},
			"resources":  []string{"EASEL", "PROJECTOR"},
			"related_to": []Relation{{UID: "jsmith.part7.19960817T083000.xyzMail@example.com", RelType: "PARENT"}},
			"request": []RequestStatus{{Code: "2.0", Description: "Success"},
				{Code: "3.1", Description: "Invalid property value", Data: "DTSTART:96-Apr-01"}},
			"attachment": []Attachment{{URI: "ftp://example.com/pub/reports/r-960812.ps", FormatType: "application/postscript"}},
		}[name]
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, want %#v", name, got, want)
		}
	}
	// The DIR of the RFC example carries a query and is omitted.
	wantOrganizer := Person{Address: "jsmith@example.com", Name: "John Smith", SentBy: "asst@example.com", Language: "en"}
	if !reflect.DeepEqual(event.Organizer, &wantOrganizer) {
		t.Errorf("organizer = %+v, want %+v", event.Organizer, wantOrganizer)
	}
	wantAttendee := Person{Address: "hcabot@example.com", Name: "Henry Cabot", Status: "TENTATIVE", Role: "REQ-PARTICIPANT",
		CUType: "GROUP", RSVP: &yes, Member: []string{"ietf-calsch@example.org", "two@example.org"},
		DelegatedTo: []string{"jdoe@example.com"}, DelegatedFrom: []string{"from@example.com"},
		SentBy: "asst@example.com", Dir: "ldap://example.com/o=ABC", Language: "de"}
	if len(event.Attendees) != 1 || !reflect.DeepEqual(event.Attendees[0], wantAttendee) {
		t.Errorf("attendees = %+v, want %+v", event.Attendees, wantAttendee)
	}
	if len(event.Alarms) != 2 {
		t.Fatalf("alarms = %+v", event.Alarms)
	}
	wantAlarm := Alarm{Action: "EMAIL", Trigger: "-P2D", TriggerRelated: "END", Duration: "PT15M", Repeat: &repeat,
		Summary:     "*** REMINDER: SEND AGENDA FOR WEEKLY STAFF MEETING ***",
		Description: "A draft agenda needs to be sent out to the attendees.",
		Attendees:   []AlarmAttendee{{Address: "john_doe@example.com", Name: "Alert One"}, {Address: "two@example.com"}},
		Attachments: []Attachment{{URI: "http://example.com/templates/agenda.doc", FormatType: "application/msword"}}}
	if !reflect.DeepEqual(event.Alarms[0], wantAlarm) {
		t.Errorf("alarm = %+v, want %+v", event.Alarms[0], wantAlarm)
	}
	if a := event.Alarms[1]; a.Action != "AUDIO" || a.Trigger != "19970317T133000Z" || !a.TriggerAbsolute {
		t.Errorf("absolute alarm = %+v", a)
	}
	if len(event.Overrides) != 1 || event.OverridesTruncated {
		t.Fatalf("overrides = %+v", event.Overrides)
	}
	override, four := event.Overrides[0], 4
	if override.RecurrenceID != "1998-09-02T09:00:00" || override.RecurrenceRange != "THISANDFUTURE" ||
		override.Summary != "Moved review" || override.Start != "1998-09-02T10:00:00" || *override.Sequence != four ||
		override.Timezone != "America/New_York" {
		t.Errorf("override = %+v", override)
	}
	encoded, _ := json.Marshal(event)
	if strings.Contains(string(encoded), "VGhl") || strings.Contains(string(encoded), "secret") ||
		strings.Contains(string(encoded), "pw@") {
		t.Errorf("encoded event leaks omitted values: %s", encoded)
	}
}

func TestCompactEventIsUnchanged(t *testing.T) {
	event, err := ParseEvent(rfcEvent, false)
	if err != nil {
		t.Fatal(err)
	}
	event.ID, event.ETag = "a.ics", "e1"
	encoded, _ := json.Marshal(event)
	const expected = `{"id":"a.ics","uid":"uid1@example.com","summary":"Annual Employee Review",` +
		`"start":"1997-09-02T09:00:00","end":"1997-09-02T17:00:00","timezone":"America/New_York",` +
		`"status":"CONFIRMED","rrule":"FREQ=YEARLY","etag":"e1"}`
	if string(encoded) != expected {
		t.Errorf("compact = %s\nwant %s", encoded, expected)
	}
}

func TestFullEventBoundsListsAndOverrides(t *testing.T) {
	var lines []string
	lines = append(lines, "UID:u", "DTSTART:20260310T090000Z")
	for i := 0; i < 101; i++ {
		lines = append(lines, "EXDATE:20260311T090000Z", "RDATE:20260312T090000Z")
	}
	for i := 0; i < 21; i++ {
		lines = append(lines, "CATEGORIES:c", "COMMENT:c", "CONTACT:c", "RESOURCES:r", "RELATED-TO:x",
			"REQUEST-STATUS:2.0;ok", "ATTACH:https://example.com/a", "BEGIN:VALARM", "TRIGGER:-PT5M", "ACTION:DISPLAY", "END:VALARM")
	}
	{
		lines = append(lines, "ATTENDEE;MEMBER=\"mailto:a@example.org\",\"mailto:b@example.org\",\"mailto:c@example.org\",\"mailto:d@example.org\","+
			"\"mailto:e@example.org\",\"mailto:f@example.org\",\"mailto:g@example.org\",\"mailto:h@example.org\",\"mailto:i@example.org\",\"mailto:j@example.org\","+
			"\"mailto:k@example.org\":mailto:p@example.org")
	}
	overrides := make([]string, 51)
	for i := range overrides {
		overrides[i] = vevent("UID:u", "RECURRENCE-ID:20260310T090000Z")
	}
	event, err := ParseEvent(icsOf(append([]string{vevent(lines...)}, overrides...)...), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(event.ExDates) != 100 || !event.ExDatesTruncated || len(event.RDates) != 100 || !event.RDatesTruncated ||
		len(event.Categories) != 20 || !event.CategoriesCut || len(event.Comments) != 20 || !event.CommentsCut ||
		len(event.Contacts) != 20 || !event.ContactsCut || len(event.Resources) != 20 || !event.ResourcesCut ||
		len(event.RelatedTo) != 20 || !event.RelatedToCut || len(event.RequestStatus) != 20 || !event.RequestStatusCut ||
		len(event.Attachments) != 20 || !event.AttachmentsCut || len(event.Alarms) != 20 || !event.AlarmsTruncated ||
		len(event.Overrides) != 50 || !event.OverridesTruncated {
		t.Errorf("bounds not applied: %+v", event)
	}
	if person := event.Attendees[0]; len(person.Member) != 10 || !person.MemberTruncated {
		t.Errorf("member = %v, cut %v", person.Member, person.MemberTruncated)
	}
	if event.Priority != nil || event.Sequence != nil || event.RecurrenceID != "" {
		t.Errorf("absent members reported: %+v", event)
	}
}

func TestFullEventOmitsUnsafeAndInvalidValues(t *testing.T) {
	event, err := ParseEvent(icsOf(vevent("UID:u", "DTSTART:20260310T090000Z", "DTEND:20260310T100000Z",
		"URL:https://example.com/e?token=abc", "PRIORITY:high", "SEQUENCE:-1", "GEO:not;geo", "DURATION:"+strings.Repeat("P", 70),
		"ATTENDEE;SENT-BY=\"mailto:a@example.org?subject=x\";DIR=\"https://example.com/d#frag\";"+
			"MEMBER=\"https://u:p@example.com/g\":mailto:p@example.org",
		"ATTACH:https://example.com/a#frag", "ATTACH;VALUE=BINARY;ENCODING=BASE64:AAAA")), true)
	if err != nil {
		t.Fatal(err)
	}
	person := event.Attendees[0]
	if event.URL != "" || event.Priority != nil || event.Sequence != nil || event.Geo != "" || event.Duration != "" ||
		person.SentBy != "" || person.Dir != "" || person.Member != nil || event.Attachments != nil ||
		event.EndTimezone != "" {
		t.Errorf("event = %+v", event)
	}
	if got, _ := json.Marshal(event.Attendees[0]); string(got) != `{"address":"p@example.org"}` {
		t.Errorf("attendee = %s", got)
	}
}

func TestEndTimezoneDiffersFromStart(t *testing.T) {
	event, err := ParseEvent(icsOf(vevent("UID:u", "DTSTART;TZID=Europe/Zurich:20260310T090000",
		"DTEND;TZID=Asia/Tokyo:20260310T180000")), true)
	if err != nil || event.Timezone != "Europe/Zurich" || event.EndTimezone != "Asia/Tokyo" {
		t.Errorf("event = %+v, err = %v", event, err)
	}
	event, err = ParseEvent(icsOf(vevent("UID:u", "DTSTART:20260310T090000Z", "DTEND:20260310T100000Z")), true)
	if err != nil || event.EndTimezone != "" {
		t.Errorf("utc event = %+v, err = %v", event, err)
	}
}
