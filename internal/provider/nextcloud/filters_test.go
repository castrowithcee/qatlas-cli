package nextcloud

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
)

const filterRange = `"start":"2026-03-01T00:00:00Z","end":"2026-04-01T00:00:00Z"`

func listEvents(t *testing.T, args string) (json.RawMessage, error) {
	t.Helper()
	return calInvoke(t, []string{"calendar/personal"}, "nextcloud.calendar.events.list", args)
}

func listContacts(t *testing.T, args string) (json.RawMessage, error) {
	t.Helper()
	return bookInvoke(t, []string{"addressbook/contacts"}, "nextcloud.contacts.contacts.list", args)
}

// checkEventMembersDeclared checks that every member the list reports is in the output schema and the fields. The
// reverse direction is not asked: the shared schema also holds members that only the get tool fills.
func checkEventMembersDeclared(t *testing.T, result json.RawMessage) {
	t.Helper()
	var out struct {
		Events []map[string]any `json:"events"`
	}
	var schema struct {
		Properties struct {
			Events struct {
				Items struct {
					Properties map[string]any `json:"properties"`
				} `json:"items"`
			} `json:"events"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(result, &out); err != nil || len(out.Events) == 0 {
		t.Fatalf("result = %s, %v", result, err)
	}
	if err := json.Unmarshal(eventsList.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	fields := map[string]bool{}
	for _, f := range eventsList.Fields {
		fields[f.Name] = true
	}
	for _, event := range out.Events {
		for key := range event {
			if _, ok := schema.Properties.Events.Items.Properties[key]; !ok || !fields[key] {
				t.Errorf("%s is not declared in schema and fields", key)
			}
		}
	}
}

func emptyEvents(t *testing.T) *[]call {
	return serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, calMultistatus()), nil
	})
}

func TestEventsListWithoutFilterArgumentsKeepsTheBody(t *testing.T) {
	calls := emptyEvents(t)
	if _, err := listEvents(t, `{"calendar":"personal",`+filterRange+`}`); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[0].body; got != dav.CalendarQueryBody("20260301T000000Z", "20260401T000000Z") {
		t.Errorf("body = %s", got)
	}
}

func TestEventsListSendsEveryFilterToTheReport(t *testing.T) {
	calls := emptyEvents(t)
	args := `{"calendar":"personal",` + filterRange + `,"property_filters":[` +
		`{"property":"SUMMARY","text":"R&D <x>","negate":true,"collation":"i;octet"},` +
		`{"property":"ATTENDEE","parameter":{"name":"PARTSTAT","text":"ACCEPTED"}},` +
		`{"property":"LOCATION","defined":false},` +
		`{"property":"CREATED","range":{"start":"2026-01-01T00:00:00Z","end":"2026-02-01T00:00:00Z"}}],` +
		`"alarm_range":{"start":"2026-03-01T00:00:00Z","end":"2026-03-08T00:00:00Z"},"expand":true}`
	if _, err := listEvents(t, args); err != nil {
		t.Fatal(err)
	}
	body := (*calls)[0].body
	for _, want := range []string{
		`<c:text-match collation="i;octet" negate-condition="yes">R&amp;D &lt;x&gt;</c:text-match>`,
		`<c:prop-filter name="ATTENDEE"><c:param-filter name="PARTSTAT"><c:text-match>ACCEPTED</c:text-match></c:param-filter></c:prop-filter>`,
		`<c:prop-filter name="LOCATION"><c:is-not-defined/></c:prop-filter>`,
		`<c:prop-filter name="CREATED"><c:time-range start="20260101T000000Z" end="20260201T000000Z"/></c:prop-filter>`,
		`<c:comp-filter name="VALARM"><c:time-range start="20260301T000000Z" end="20260308T000000Z"/></c:comp-filter>`,
		`<c:calendar-data><c:expand start="20260301T000000Z" end="20260401T000000Z"/></c:calendar-data>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %s: %s", want, body)
		}
	}
}

func TestEventsListFilterRefusalsComeBeforeAnyRequest(t *testing.T) {
	calls := emptyEvents(t)
	var nine []string
	for i := 0; i < 9; i++ {
		nine = append(nine, `{"property":"SUMMARY","text":"a"}`)
	}
	for name, filters := range map[string]string{
		"unknown property":  `"property_filters":[{"property":"X-FOO","text":"a"}]`,
		"unknown parameter": `"property_filters":[{"property":"ATTENDEE","parameter":{"name":"X","text":"a"}}]`,
		"unknown collation": `"property_filters":[{"property":"SUMMARY","text":"a","collation":"i;unicode-casemap"}]`,
		"text too long":     `"property_filters":[{"property":"SUMMARY","text":"` + strings.Repeat("a", 257) + `"}]`,
		"nine entries":      `"property_filters":[` + strings.Join(nine, ",") + `]`,
		"two conditions":    `"property_filters":[{"property":"CREATED","text":"a","defined":false}]`,
		"range on summary":  `"property_filters":[{"property":"SUMMARY","range":{"start":"2026-03-01T00:00:00Z","end":"2026-03-02T00:00:00Z"}}]`,
		"match type":        `"property_filters":[{"property":"SUMMARY","text":"a","match_type":"equals"}]`,
		"alarm too long":    `"alarm_range":{"start":"2026-01-01T00:00:00Z","end":"2027-06-01T00:00:00Z"}`,
		"alarm end first":   `"alarm_range":{"start":"2026-03-02T00:00:00Z","end":"2026-03-01T00:00:00Z"}`,
	} {
		if _, err := listEvents(t, `{"calendar":"personal",`+filterRange+`,`+filters+`}`); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestEventsListExpandReportsEachOccurrenceAgainstTheCalendarBinding(t *testing.T) {
	base := calHome + "personal/"
	occurrence := func(recurrence, start, summary string) string {
		return "BEGIN:VEVENT\r\nUID:s@example.com\r\nRECURRENCE-ID:" + recurrence + "\r\nDTSTART:" + start +
			"\r\nSUMMARY:" + summary + "\r\nDESCRIPTION:long text\r\nEND:VEVENT\r\n"
	}
	wrap := func(parts ...string) string {
		return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n" + strings.Join(parts, "") + "END:VCALENDAR\r\n"
	}
	series := wrap(occurrence("20260305T100000Z", "20260305T100000Z", "Daily"),
		occurrence("20260303T100000Z", "20260303T100000Z", "Daily"),
		occurrence("20260304T100000Z", "20260304T120000Z", "Moved"),
		occurrence("20260303T100000Z", "20260303T100000Z", "Daily"))
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, calMultistatus(
			eventNode(base+"series.ics", "e1", series),
			eventNode(calHome+"work/other.ics", "x", series),
			eventNode("https://evil.example.invalid"+base+"host.ics", "x", series),
			eventNode(base+"sub/deep.ics", "x", series),
			eventNode(base+"once.ics", "e2", ics("o", "Once", "20260301T080000Z", "")),
		)), nil
	})
	result, err := listEvents(t, `{"calendar":"personal",`+filterRange+`,"expand":true,"limit":3}`)
	if err != nil {
		t.Fatal(err)
	}
	var page EventsPage
	if err := json.Unmarshal(result, &page); err != nil || page.Count != 3 || !page.Truncated {
		t.Fatalf("page = %s, %v", result, err)
	}
	var got []string
	for _, event := range page.Events {
		got = append(got, event.ID+"|"+event.RecurrenceID)
	}
	want := []string{"once.ics|", "series.ics|2026-03-03T10:00:00Z", "series.ics|2026-03-04T10:00:00Z"}
	if strings.Join(got, ",") != strings.Join(want, ",") || page.Events[2].Start != "2026-03-04T12:00:00Z" ||
		page.Events[2].ETag != "e1" || page.Events[1].Description != "" {
		t.Errorf("events = %v, page = %s", got, result)
	}
	checkEventMembersDeclared(t, result)
}

func TestEventsListWithoutExpandOmitsRecurrenceID(t *testing.T) {
	base := calHome + "personal/"
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, calMultistatus(
			eventNode(base+"a.ics", "e1", ics("a", "A", "20260302T090000Z", "RRULE:FREQ=WEEKLY\r\n")))), nil
	})
	result, err := listEvents(t, `{"calendar":"personal",`+filterRange+`}`)
	if err != nil || strings.Contains(string(result), "recurrence_id") {
		t.Errorf("result = %s, %v", result, err)
	}
	checkEventMembersDeclared(t, result)
}

func TestExpandIsRefusedForAnUnboundCalendarBeforeAnyRequest(t *testing.T) {
	calls := emptyEvents(t)
	if _, err := calInvoke(t, []string{"calendar/personal"}, "nextcloud.calendar.events.list",
		`{"calendar":"work",`+filterRange+`,"expand":true}`); err == nil || len(*calls) != 0 {
		t.Errorf("err = %v, calls = %d", err, len(*calls))
	}
}

func TestEventsListCollationRefusalIsARequestError(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) { return xmlResponse(http.StatusForbidden, "canary"), nil })
	_, err := listEvents(t, `{"calendar":"personal",`+filterRange+
		`,"property_filters":[{"property":"SUMMARY","text":"a","collation":"i;octet"}]}`)
	if err == nil || !strings.Contains(err.Error(), "rejected the filter") || strings.Contains(err.Error(), "canary") {
		t.Errorf("err = %v", err)
	}
	_, err = listEvents(t, `{"calendar":"personal",`+filterRange+`}`)
	if err == nil || strings.Contains(err.Error(), "rejected the filter") {
		t.Errorf("without collation err = %v", err)
	}
}

func TestEventsGetRefusesFilterArguments(t *testing.T) {
	calls := emptyEvents(t)
	if _, err := calInvoke(t, []string{"calendar/personal"}, "nextcloud.calendar.events.get",
		`{"calendar":"personal","id":"a.ics","expand":true}`); err == nil || len(*calls) != 0 {
		t.Errorf("err = %v, calls = %d", err, len(*calls))
	}
}

func emptyBook(t *testing.T) *[]call {
	return serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, bookMultistatus()), nil
	})
}

func TestContactsListWithoutFilterArgumentsKeepsTheBody(t *testing.T) {
	calls := emptyBook(t)
	for args, query := range map[string]string{`{"addressbook":"contacts"}`: "", `{"addressbook":"contacts","query":"ada"}`: "ada"} {
		if _, err := listContacts(t, args); err != nil {
			t.Fatal(err)
		}
		if got := (*calls)[len(*calls)-1].body; got != contactQueryBody(query) {
			t.Errorf("body = %s", got)
		}
	}
}

func TestContactsListSendsEveryFilterToTheReport(t *testing.T) {
	calls := emptyBook(t)
	args := `{"addressbook":"contacts","test":"allof","property_filters":[` +
		`{"property":"FN","text":"A&B <x>","match_type":"starts-with","negate":true,"collation":"i;octet"},` +
		`{"property":"EMAIL","parameter":{"name":"TYPE","text":"work","match_type":"equals"}},` +
		`{"property":"TEL","defined":false}]}`
	if _, err := listContacts(t, args); err != nil {
		t.Fatal(err)
	}
	body := (*calls)[0].body
	for _, want := range []string{
		`<card:filter test="allof">`,
		`<card:text-match collation="i;octet" match-type="starts-with" negate-condition="yes">A&amp;B &lt;x&gt;</card:text-match>`,
		`<card:param-filter name="TYPE"><card:text-match match-type="equals">work</card:text-match></card:param-filter>`,
		`<card:prop-filter name="TEL"><card:is-not-defined/></card:prop-filter>`,
		`<card:address-data>` + contactListProps + `</card:address-data>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %s: %s", want, body)
		}
	}
	if strings.Contains(body, "<card:address-data/>") || strings.Contains(body, "PHOTO") {
		t.Errorf("the request may ask for full cards: %s", body)
	}
}

func TestContactsListFilterRefusalsComeBeforeAnyRequest(t *testing.T) {
	calls := emptyBook(t)
	var nine []string
	for i := 0; i < 9; i++ {
		nine = append(nine, `{"property":"FN","text":"a"}`)
	}
	for name, filters := range map[string]string{
		"unknown property":  `"property_filters":[{"property":"X-FOO","text":"a"}]`,
		"unknown parameter": `"property_filters":[{"property":"EMAIL","parameter":{"name":"X","text":"a"}}]`,
		"unknown match":     `"property_filters":[{"property":"FN","text":"a","match_type":"regex"}]`,
		"unknown collation": `"property_filters":[{"property":"FN","text":"a","collation":"i;x"}]`,
		"text too long":     `"property_filters":[{"property":"FN","text":"` + strings.Repeat("a", 257) + `"}]`,
		"nine entries":      `"property_filters":[` + strings.Join(nine, ",") + `]`,
		"both conditions":   `"property_filters":[{"property":"FN","text":"a","defined":false}]`,
		"no condition":      `"property_filters":[{"property":"FN"}]`,
		"unknown test":      `"test":"none","property_filters":[{"property":"FN","text":"a"}]`,
		"test alone":        `"test":"allof"`,
	} {
		if _, err := listContacts(t, `{"addressbook":"contacts",`+filters+`}`); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %d, want none", len(*calls))
	}
}

func TestContactsListQueryNarrowsFilteredResultsToo(t *testing.T) {
	book := contactHome + "contacts/"
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, bookMultistatus(
			cardNode(book+"ada.vcf", "e1", vcardOf("u-1", "Ada Lovelace", "")),
			cardNode(book+"bob.vcf", "e2", vcardOf("u-2", "Bob", "EMAIL:"+contactMail("ADA.b")+"\r\n")),
			cardNode(book+"cy.vcf", "e3", vcardOf("u-3", "Cy", "")),
		)), nil
	})
	result, err := listContacts(t, `{"addressbook":"contacts","query":"ada","property_filters":[{"property":"FN","text":"a"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	var page ContactsPage
	if err := json.Unmarshal(result, &page); err != nil || page.Count != 2 || page.Contacts[0].ID != "ada.vcf" ||
		page.Contacts[1].ID != "bob.vcf" {
		t.Errorf("page = %s, %v", result, err)
	}
	if strings.Contains((*calls)[0].body, "ada") || strings.Count((*calls)[0].body, "<card:prop-filter") != 1 {
		t.Errorf("body = %s", (*calls)[0].body)
	}
	checkOutputDeclared(t, contactsList, "contacts", result)
}

func TestContactsListCollationRefusalIsARequestError(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) { return xmlResponse(http.StatusForbidden, "canary"), nil })
	_, err := listContacts(t, `{"addressbook":"contacts","property_filters":[{"property":"FN","text":"a","collation":"i;octet"}]}`)
	if err == nil || !strings.Contains(err.Error(), "rejected the filter") {
		t.Errorf("err = %v", err)
	}
}

func TestContactsGetRefusesFilterArguments(t *testing.T) {
	calls := emptyBook(t)
	if _, err := bookInvoke(t, []string{"addressbook/contacts"}, "nextcloud.contacts.contacts.get",
		`{"addressbook":"contacts","id":"a.vcf","test":"allof"}`); err == nil || len(*calls) != 0 {
		t.Errorf("err = %v, calls = %d", err, len(*calls))
	}
}
