package nextcloud

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func calVEvent(lines ...string) string {
	return "BEGIN:VEVENT\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VEVENT\r\n"
}

func calICS(vevents ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n" + strings.Join(vevents, "") + "END:VCALENDAR\r\n"
}

// richEvent carries every member the get tool reports, an override, and a value of each kind that is omitted.
var richEvent = calICS(calVEvent(
	"DTSTAMP:19960704T120000Z", "UID:uid1@example.com",
	"ORGANIZER;CN=John Smith;SENT-BY=\"mailto:asst@example.com\";LANGUAGE=en:mailto:jsmith@example.com",
	"ATTENDEE;ROLE=REQ-PARTICIPANT;CUTYPE=GROUP;PARTSTAT=TENTATIVE;RSVP=TRUE;CN=Henry Cabot;MEMBER=\"mailto:g@example.org\":mailto:hcabot@example.com",
	"DTSTART;TZID=America/New_York:19970902T090000", "DTEND;TZID=Europe/Zurich:19970902T170000", "DURATION:PT1H",
	"SUMMARY:Annual Employee Review", "DESCRIPTION:Agenda", "LOCATION:Room 1", "CLASS:PRIVATE", "TRANSP:OPAQUE",
	"PRIORITY:2", "SEQUENCE:3", "GEO:37.386013;-122.082932", "URL:http://example.com/mytime.ics",
	"CREATED:19960329T133000Z", "LAST-MODIFIED:19960817T133000Z", "STATUS:CONFIRMED", "RRULE:FREQ=YEARLY",
	"EXDATE;TZID=America/New_York:19980902T090000", "RDATE;TZID=America/New_York:19990902T090000",
	"CATEGORIES:APPOINTMENT,EDUCATION", "COMMENT:Bring a laptop", "CONTACT:Jim Dolittle", "RESOURCES:EASEL",
	"RELATED-TO;RELTYPE=PARENT:parent@example.com", "REQUEST-STATUS:2.0;Success",
	"ATTACH;FMTTYPE=application/postscript:ftp://example.com/r.ps", "ATTACH;VALUE=BINARY;ENCODING=BASE64:AAAA",
	"ATTACH:https://example.com/doc?token=SECRETTOKEN",
	"BEGIN:VALARM", "TRIGGER;RELATED=END:-P2D", "ACTION:EMAIL", "ATTENDEE;CN=Alert:mailto:john_doe@example.com",
	"SUMMARY:Reminder", "DESCRIPTION:Send agenda", "REPEAT:4", "DURATION:PT15M", "END:VALARM",
), calVEvent("UID:uid1@example.com", "RECURRENCE-ID;RANGE=THISANDFUTURE;TZID=America/New_York:19980902T090000",
	"SUMMARY:Moved", "DTSTART;TZID=America/New_York:19980902T100000"))

func TestEventsGetEveryMemberIsInSchemaAndFields(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		response := xmlResponse(http.StatusOK, richEvent)
		response.Header = http.Header{"Etag": []string{`"e1"`}}
		return response, nil
	})
	result, err := calInvoke(t, []string{"calendar"}, "nextcloud.calendar.events.get",
		`{"calendar":"personal","id":"a.ics"}`)
	if err != nil {
		t.Fatalf("get through the core = %v", err)
	}
	if strings.Contains(string(result), "SECRETTOKEN") || strings.Contains(string(result), "AAAA") {
		t.Errorf("omitted value reported: %s", result)
	}
	var out struct {
		Event map[string]json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties struct {
			Event struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"event"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(eventsGet.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	fields := map[string]bool{}
	for _, f := range eventsGet.Fields {
		fields[f.Name] = true
	}
	covered := func(name string) bool {
		return fields[name] || name == "attendees_truncated" ||
			(strings.HasSuffix(name, "_truncated") && fields[strings.TrimSuffix(name, "_truncated")])
	}
	var problems []string
	for key := range out.Event {
		if _, ok := schema.Properties.Event.Properties[key]; !ok {
			problems = append(problems, key+" (schema)")
		}
		if !covered(key) {
			problems = append(problems, key+" (fields)")
		}
	}
	for key := range fields {
		if key == "count" || key == "truncated" || key == "all_day" || key == "recurrence_id" || key == "recurrence_range" {
			continue
		}
		if _, ok := out.Event[key]; !ok {
			problems = append(problems, key+" (not reported)")
		}
	}
	for key := range schema.Properties.Event.Properties {
		if !covered(key) {
			problems = append(problems, key+" (schema only)")
		}
	}
	var overrides []map[string]json.RawMessage
	if err := json.Unmarshal(out.Event["overrides"], &overrides); err != nil || len(overrides) != 1 {
		t.Fatalf("overrides = %s, err = %v", out.Event["overrides"], err)
	}
	for _, key := range []string{"recurrence_id", "recurrence_range"} {
		if _, ok := overrides[0][key]; !ok {
			problems = append(problems, key+" (not reported in the override)")
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Errorf("problems: %v", problems)
	}
}

func TestEventsListReportsTheMembersOfGet(t *testing.T) {
	serve(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "REPORT" {
			return xmlResponse(http.StatusMultiStatus, calMultistatus(
				eventNode(calHome+"personal/a.ics", "e1", richEvent))), nil
		}
		response := xmlResponse(http.StatusOK, richEvent)
		response.Header = http.Header{"Etag": []string{`"e1"`}}
		return response, nil
	})
	listed, err := listEvents(t, `{"calendar":"personal",`+filterRange+`}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := calInvoke(t, []string{"calendar/personal"}, "nextcloud.calendar.events.get",
		`{"calendar":"personal","id":"a.ics"}`)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Events []map[string]any `json:"events"`
	}
	var get struct {
		Event map[string]any `json:"event"`
	}
	if err := json.Unmarshal(listed, &list); err != nil || json.Unmarshal(got, &get) != nil || len(list.Events) != 1 {
		t.Fatalf("list = %s, get = %s, %v", listed, got, err)
	}
	if !reflect.DeepEqual(list.Events[0], get.Event) {
		t.Errorf("list = %v, get = %v", list.Events[0], get.Event)
	}
	for _, key := range []string{"description", "organizer", "attendees", "overrides"} {
		if _, ok := get.Event[key]; !ok {
			t.Errorf("example carries no %s", key)
		}
	}
	checkEventMembersDeclared(t, listed)
}

func TestEventsListExpandedOccurrencesCarryTheFullFields(t *testing.T) {
	data := calICS(
		calVEvent("UID:s@example.com", "RECURRENCE-ID:20260303T100000Z", "DTSTART:20260303T100000Z", "SUMMARY:Daily",
			"DESCRIPTION:Agenda", "LOCATION:Room 1", "ORGANIZER;CN=Boss:mailto:boss@example.com",
			"ATTENDEE;CN=Ada;PARTSTAT=ACCEPTED:mailto:ada@example.com"),
		calVEvent("UID:s@example.com", "RECURRENCE-ID:20260304T100000Z", "DTSTART:20260304T100000Z", "SUMMARY:Daily",
			"DESCRIPTION:Agenda", "LOCATION:Room 1", "ORGANIZER;CN=Boss:mailto:boss@example.com",
			"ATTENDEE;CN=Ada;PARTSTAT=ACCEPTED:mailto:ada@example.com"))
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, calMultistatus(eventNode(calHome+"personal/s.ics", "e1", data))), nil
	})
	result, err := listEvents(t, `{"calendar":"personal",`+filterRange+`,"expand":true}`)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(result, &out); err != nil || len(out.Events) != 2 {
		t.Fatalf("result = %s, %v", result, err)
	}
	for _, event := range out.Events {
		for _, key := range []string{"description", "organizer", "attendees", "recurrence_id"} {
			if _, ok := event[key]; !ok {
				t.Errorf("occurrence lacks %s: %v", key, event)
			}
		}
	}
	checkEventMembersDeclared(t, result)
}
