package infomaniakdav

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

// richEvent carries every member the get tool reports, an override, and a value of each kind that is omitted.
var richEvent = icsOf(vevent(
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
), vevent("UID:uid1@example.com", "RECURRENCE-ID;RANGE=THISANDFUTURE;TZID=America/New_York:19980902T090000",
	"SUMMARY:Moved", "DTSTART;TZID=America/New_York:19980902T100000"))

func TestEventsGetEveryMemberIsInSchemaAndFields(t *testing.T) {
	serve(t, eventsFake(t, "", func(*http.Request) (*http.Response, error) {
		resp := xmlResponse(200, richEvent)
		resp.Header.Set("ETag", `"e1"`)
		return resp, nil
	}))
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Version:     1,
		Services:    map[string]config.Service{"dav": {Provider: Provider}},
		Credentials: map[string]config.Credential{"dav": resolved().Secrets},
		Connections: map[string]config.Connection{"main": {Service: "dav", Credential: "dav",
			Effects: config.Effects(), Targets: []string{"calendar/work"}}},
	}
	red := &redact.Redactor{}
	response, err := application.New(reg, cfg, resolver(red), red).Invoke(context.Background(),
		application.InvokeRequest{Operation: eventsGet.ID, Connection: "main",
			Arguments: json.RawMessage(`{"calendar":"work","id":"u1.ics"}`)})
	if err != nil {
		t.Fatalf("get through the core = %v", err)
	}
	if strings.Contains(string(response.Result), "SECRETTOKEN") || strings.Contains(string(response.Result), "AAAA") {
		t.Errorf("omitted value reported: %s", response.Result)
	}
	var out struct {
		Event map[string]json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(response.Result, &out); err != nil {
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
	flag := func(name string) bool {
		return strings.HasSuffix(name, "_truncated") && fields[strings.TrimSuffix(name, "_truncated")]
	}
	var problems []string
	for key := range out.Event {
		if _, ok := schema.Properties.Event.Properties[key]; !ok {
			problems = append(problems, key+" (schema)")
		}
		if !fields[key] && !flag(key) && key != "attendees_truncated" {
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
		if !fields[key] && !flag(key) && key != "attendees_truncated" {
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
	for key := range overrides[0] {
		if _, ok := schema.Properties.Event.Properties[key]; !ok || key == "id" || key == "etag" || key == "overrides" {
			problems = append(problems, key+" (override schema)")
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Errorf("problems: %v", problems)
	}
}

func TestEventsListFieldsMatchSchema(t *testing.T) {
	series := icsOf(vevent("DTSTAMP:19960704T120000Z", "UID:s@example.com", "RECURRENCE-ID:20260303T100000Z",
		"DTSTART:20260303T100000Z", "DTEND:20260303T110000Z", "SUMMARY:Daily", "LOCATION:Room 1", "STATUS:CONFIRMED",
		"RRULE:FREQ=DAILY"))
	serve(t, eventsFake(t, multistatus(eventEntry(workPath+"s.ics", "e1", series)), nil))
	out, err := listEventsOf(t, []string{"calendar/work"}, `{"calendar":"work",`+rangeArgs+`,"expand":true}`)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	var page struct {
		Events []map[string]json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(raw, &page); err != nil || len(page.Events) != 1 {
		t.Fatalf("page = %s, err = %v", raw, err)
	}
	var schema struct {
		Properties struct {
			Events struct {
				Items struct {
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"items"`
			} `json:"events"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(eventsList.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	fields := map[string]bool{}
	for _, f := range eventsList.Fields {
		fields[f.Name] = true
	}
	for key := range page.Events[0] {
		if _, ok := schema.Properties.Events.Items.Properties[key]; !ok {
			t.Errorf("%s missing in the list schema", key)
		}
		if !fields[key] {
			t.Errorf("%s missing in the list fields", key)
		}
	}
	if _, ok := page.Events[0]["recurrence_id"]; !ok {
		t.Error("recurrence_id not reported")
	}
}

func TestEventsListReportsTheMembersOfGet(t *testing.T) {
	serve(t, eventsFake(t, multistatus(eventEntry(workPath+"u1.ics", "e1", richEvent)), func(*http.Request) (*http.Response, error) {
		resp := xmlResponse(200, richEvent)
		resp.Header.Set("ETag", `"e1"`)
		return resp, nil
	}))
	listed, err := listEventsOf(t, []string{"calendar/work"}, allArgs)
	if err != nil {
		t.Fatal(err)
	}
	got, err := invokeEventsGet(context.Background(), resolved("calendar/work"), resolver(nil), nil,
		json.RawMessage(`{"calendar":"work","id":"u1.ics"}`))
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Events []map[string]any `json:"events"`
	}
	var get struct {
		Event map[string]any `json:"event"`
	}
	for v, dst := range map[any]any{listed: &list, got: &get} {
		raw, _ := json.Marshal(v)
		if err := json.Unmarshal(raw, dst); err != nil {
			t.Fatal(err)
		}
	}
	if len(list.Events) != 1 || !reflect.DeepEqual(list.Events[0], get.Event) {
		t.Errorf("list = %v, get = %v", list.Events, get.Event)
	}
	if _, ok := get.Event["overrides"]; !ok {
		t.Error("example carries no overrides")
	}
}
