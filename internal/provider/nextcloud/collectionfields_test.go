package nextcloud

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider/dav"
)

const calendarCollectionProps = `<c:calendar-description>Team plan</c:calendar-description>` +
	`<c:supported-calendar-data><c:calendar-data content-type="text/calendar" version="2.0"/>` +
	`</c:supported-calendar-data><c:max-resource-size>102400</c:max-resource-size>` +
	`<c:min-date-time>19000101T000000Z</c:min-date-time><c:max-date-time>20491231T235959Z</c:max-date-time>` +
	`<c:max-instances>100</c:max-instances><c:max-attendees-per-instance>25</c:max-attendees-per-instance>` +
	`<c:calendar-timezone>BEGIN:VCALENDAR&#13;&#10;VERSION:2.0&#13;&#10;BEGIN:VTIMEZONE&#13;&#10;` +
	`TZID:Europe/Zurich&#13;&#10;BEGIN:STANDARD&#13;&#10;TZOFFSETTO:+0100&#13;&#10;END:STANDARD&#13;&#10;` +
	`END:VTIMEZONE&#13;&#10;END:VCALENDAR</c:calendar-timezone>` +
	`<c:supported-collation-set><c:supported-collation>i;ascii-casemap</c:supported-collation>` +
	`<c:supported-collation>i;octet</c:supported-collation></c:supported-collation-set>` +
	`<d:sync-token>http://example.com/ns/sync/1234</d:sync-token>`

const bookCollectionProps = `<card:supported-address-data><card:address-data-type content-type="text/vcard" version="3.0"/>` +
	`</card:supported-address-data><card:max-resource-size>102400</card:max-resource-size>` +
	`<card:supported-collation-set><card:supported-collation>i;unicode-casemap</card:supported-collation>` +
	`</card:supported-collation-set>` +
	`<d:current-user-privilege-set><d:privilege><d:read/></d:privilege></d:current-user-privilege-set>` +
	`<d:sync-token>urn:example:token-7</d:sync-token>`

// checkOutputDeclared lists the members of the first entry of a list result and checks that each one is declared in the
// output schema and in the fields, and that each declared member has a field.
func checkOutputDeclared(t *testing.T, descriptor capability.Descriptor, list string, result json.RawMessage) map[string]any {
	t.Helper()
	var raw map[string]json.RawMessage
	var entries []map[string]any
	if err := json.Unmarshal(result, &raw); err != nil || json.Unmarshal(raw[list], &entries) != nil || len(entries) == 0 {
		t.Fatalf("result = %s, %v", result, err)
	}
	var schema struct {
		Properties map[string]struct {
			Items struct {
				Properties map[string]any `json:"properties"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(descriptor.OutputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	declared := schema.Properties[list].Items.Properties
	fields := map[string]bool{}
	for _, f := range descriptor.Fields {
		fields[f.Name] = true
	}
	var problems []string
	for key := range entries[0] {
		if _, ok := declared[key]; !ok {
			problems = append(problems, key+" (schema)")
		}
		if !fields[key] {
			problems = append(problems, key+" (fields)")
		}
	}
	for key := range declared {
		if !fields[key] {
			problems = append(problems, key+" (schema without field)")
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Errorf("%s: undeclared members: %v", descriptor.ID, problems)
	}
	return entries[0]
}

func TestCalendarsListReportsEveryCollectionProperty(t *testing.T) {
	node := calendarNode(calHome+"team/", "Team", isCalendar, fullRights, "VEVENT", "VTODO")
	node = strings.Replace(node, "</d:prop>", calendarCollectionProps+"</d:prop>", 1)
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, calMultistatus(calendarNode(calHome, "Home", "", nil), node)), nil
	})
	result, err := calInvoke(t, []string{"calendar"}, "nextcloud.calendar.calendars.list", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var listed CalendarsResult
	if err := json.Unmarshal(result, &listed); err != nil || len(listed.Calendars) != 1 {
		t.Fatalf("result = %s, %v", result, err)
	}
	size, instances, attendees := uint64(102400), uint64(100), uint64(25)
	want := Calendar{ID: "team", Name: "Team", Description: "Team plan", Components: []string{"VEVENT", "VTODO"},
		CollectionProperties: dav.CollectionProperties{
			CalendarData:    []dav.CollectionFormat{{ContentType: "text/calendar", Version: "2.0"}},
			MaxResourceSize: &size, MinDateTime: "1900-01-01T00:00:00Z", MaxDateTime: "2049-12-31T23:59:59Z",
			MaxInstances: &instances, MaxAttendeesPerInstance: &attendees, Timezone: "Europe/Zurich",
			Collations: []string{"i;ascii-casemap", "i;octet"}, Privileges: []string{"read", "write", "write-properties", "write-content", "bind", "unbind"},
			SyncToken: "http://example.com/ns/sync/1234"}}
	if !reflect.DeepEqual(listed.Calendars[0], want) {
		t.Errorf("calendar = %+v\nwant %+v", listed.Calendars[0], want)
	}
	checkOutputDeclared(t, calendarsList, "calendars", result)
	body := (*calls)[0].body
	for _, prop := range []string{"c:calendar-description", "c:supported-calendar-data", "c:max-resource-size",
		"c:min-date-time", "c:max-date-time", "c:max-instances", "c:max-attendees-per-instance",
		"c:calendar-timezone", "c:supported-collation-set", "d:current-user-privilege-set", "d:sync-token",
		"c:supported-calendar-component-set", "oc:invite"} {
		if !strings.Contains(body, "<"+prop+"/>") {
			t.Errorf("body lacks %s", prop)
		}
	}
	for _, vendor := range []string{"calendar-color", "calendar-order", "getctag"} {
		if strings.Contains(body, vendor) {
			t.Errorf("body asks for %s", vendor)
		}
	}
}

func TestAddressbooksListReportsEveryCollectionProperty(t *testing.T) {
	node := strings.Replace(bookNode(contactHome+"contacts/", "Contacts", true), "</d:prop>", bookCollectionProps+"</d:prop>", 1)
	calls := serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, bookMultistatus(bookNode(contactHome, "Home", false), node)), nil
	})
	result, err := bookInvoke(t, []string{"addressbook"}, "nextcloud.contacts.address-books.list", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	var listed AddressbooksResult
	if err := json.Unmarshal(result, &listed); err != nil || len(listed.Addressbooks) != 1 {
		t.Fatalf("result = %s, %v", result, err)
	}
	size := uint64(102400)
	want := Addressbook{ID: "contacts", Name: "Contacts", Description: "Shared people",
		CollectionProperties: dav.CollectionProperties{
			AddressData:     []dav.CollectionFormat{{ContentType: "text/vcard", Version: "3.0"}},
			MaxResourceSize: &size, Collations: []string{"i;unicode-casemap"}, Privileges: []string{"read"},
			SyncToken: "urn:example:token-7"}}
	if !reflect.DeepEqual(listed.Addressbooks[0], want) {
		t.Errorf("address book = %+v\nwant %+v", listed.Addressbooks[0], want)
	}
	checkOutputDeclared(t, addressbooksList, "addressbooks", result)
	body := (*calls)[0].body
	for _, prop := range []string{"c:supported-address-data", "c:max-resource-size", "c:supported-collation-set",
		"d:current-user-privilege-set", "d:sync-token", "c:addressbook-description", "oc:invite"} {
		if !strings.Contains(body, "<"+prop+"/>") {
			t.Errorf("body lacks %s", prop)
		}
	}
}

func TestCollectionsWithoutNewPropertiesKeepTheirOutput(t *testing.T) {
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, calMultistatus(
			calendarNode(calHome+"personal/", "Personal", isCalendar, nil, "VEVENT"))), nil
	})
	result, err := calInvoke(t, []string{"calendar"}, "nextcloud.calendar.calendars.list", `{}`)
	if want := `{"calendars":[{"components":["VEVENT"],"id":"personal","name":"Personal","read_only":true}],"count":1}`; err != nil || string(result) != want {
		t.Errorf("calendars = %s, %v", result, err)
	}
	serve(t, func(*http.Request) (*http.Response, error) {
		return xmlResponse(http.StatusMultiStatus, bookMultistatus(bookNode(contactHome+"contacts/", "Contacts", true))), nil
	})
	result, err = bookInvoke(t, []string{"addressbook"}, "nextcloud.contacts.address-books.list", `{}`)
	if want := `{"addressbooks":[{"description":"Shared people","id":"contacts","name":"Contacts"}],"count":1}`; err != nil || string(result) != want {
		t.Errorf("address books = %s, %v", result, err)
	}
}
