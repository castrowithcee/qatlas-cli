package infomaniakdav

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const calendarProps = `<c:supported-calendar-component-set><c:comp name="VEVENT"/><c:comp name="VTODO"/>` +
	`</c:supported-calendar-component-set>` +
	`<c:supported-calendar-data><c:calendar-data content-type="text/calendar" version="2.0"/>` +
	`</c:supported-calendar-data><c:max-resource-size>102400</c:max-resource-size>` +
	`<c:min-date-time>19000101T000000Z</c:min-date-time><c:max-date-time>20491231T235959Z</c:max-date-time>` +
	`<c:max-instances>100</c:max-instances><c:max-attendees-per-instance>25</c:max-attendees-per-instance>` +
	`<c:calendar-timezone>BEGIN:VCALENDAR&#13;&#10;VERSION:2.0&#13;&#10;BEGIN:VTIMEZONE&#13;&#10;` +
	`TZID:Europe/Zurich&#13;&#10;BEGIN:STANDARD&#13;&#10;TZOFFSETTO:+0100&#13;&#10;END:STANDARD&#13;&#10;` +
	`END:VTIMEZONE&#13;&#10;END:VCALENDAR</c:calendar-timezone>` +
	`<c:supported-collation-set><c:supported-collation>i;ascii-casemap</c:supported-collation>` +
	`<c:supported-collation>i;octet</c:supported-collation></c:supported-collation-set>` +
	`<d:current-user-privilege-set><d:privilege><d:read/></d:privilege><d:privilege><d:write/></d:privilege>` +
	`<d:privilege><c:read-free-busy/></d:privilege></d:current-user-privilege-set>` +
	`<d:sync-token>http://example.com/ns/sync/1234</d:sync-token>`

const bookProps = `<card:supported-address-data><card:address-data-type content-type="text/vcard" version="3.0"/>` +
	`</card:supported-address-data><card:max-resource-size>102400</card:max-resource-size>` +
	`<card:supported-collation-set><card:supported-collation>i;unicode-casemap</card:supported-collation>` +
	`</card:supported-collation-set>` +
	`<d:current-user-privilege-set><d:privilege><d:read/></d:privilege></d:current-user-privilege-set>` +
	`<d:sync-token>urn:example:token-7</d:sync-token>`

// withProps serves the collection listing of a home with extra properties on the work collection.
func withProps(t *testing.T, home, kind, props string) {
	t.Helper()
	serve(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == home {
			entry := collectionXML(home+"work/", kind, "Work")
			entry = strings.Replace(entry, "</d:prop>", props+"</d:prop>", 1)
			return xmlResponse(207, multistatus(entry)), nil
		}
		return davFake(t)(r)
	})
}

func TestCalendarsReportEveryCollectionProperty(t *testing.T) {
	withProps(t, calHome, "c:calendar", calendarProps)
	c, _ := open(t, "calendar/work")
	page, err := c.list(context.Background(), true)
	if err != nil || len(page.Collections) != 1 {
		t.Fatalf("page = %+v, err = %v", page, err)
	}
	size, instances, attendees := uint64(102400), uint64(100), uint64(25)
	want := Collection{ID: "work", Name: "Work", Description: "desc Work", Color: "#FF8800FF",
		Components:      []string{"VEVENT", "VTODO"},
		CalendarData:    []DataFormat{{ContentType: "text/calendar", Version: "2.0"}},
		MaxResourceSize: &size, MinDateTime: "1900-01-01T00:00:00Z", MaxDateTime: "2049-12-31T23:59:59Z",
		MaxInstances: &instances, MaxAttendeesPerInstance: &attendees, Timezone: "Europe/Zurich",
		Collations: []string{"i;ascii-casemap", "i;octet"}, Privileges: []string{"read", "write"},
		SyncToken: "http://example.com/ns/sync/1234"}
	if got := page.Collections[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("collection = %+v\nwant %+v", got, want)
	}
}

func TestAddressbooksReportEveryCollectionProperty(t *testing.T) {
	withProps(t, bookHome, "card:addressbook", bookProps)
	c, _ := open(t, "addressbook/work")
	page, err := c.list(context.Background(), false)
	if err != nil || len(page.Collections) != 1 {
		t.Fatalf("page = %+v, err = %v", page, err)
	}
	size := uint64(102400)
	want := Collection{ID: "work", Name: "Work", Description: "desc Work",
		AddressData:     []DataFormat{{ContentType: "text/vcard", Version: "3.0"}},
		MaxResourceSize: &size, Collations: []string{"i;unicode-casemap"}, Privileges: []string{"read"},
		SyncToken: "urn:example:token-7"}
	if got := page.Collections[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("collection = %+v\nwant %+v", got, want)
	}
}

func TestCollectionsWithoutNewPropertiesKeepTheirOutput(t *testing.T) {
	serve(t, davFake(t))
	c, _ := open(t, "calendar/work", "addressbook/main")
	cal, err := c.list(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cal.Collections[0])
	if want := `{"id":"work","name":"Work","description":"desc Work","color":"#FF8800FF"}`; string(raw) != want {
		t.Errorf("calendar = %s", raw)
	}
	book, err := c.list(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(book.Collections[0])
	if want := `{"id":"main","name":"Main","description":"desc Main"}`; string(raw) != want {
		t.Errorf("address book = %s", raw)
	}
}

func TestCollectionPropfindAsksForEveryPropertyInThreeRequests(t *testing.T) {
	for _, calendars := range []bool{true, false} {
		calls := serve(t, davFake(t))
		c, _ := open(t, "calendar/work", "addressbook/main")
		if _, err := c.list(context.Background(), calendars); err != nil {
			t.Fatal(err)
		}
		if len(*calls) != 3 {
			t.Fatalf("requests = %d, want 3", len(*calls))
		}
		body := (*calls)[2].body
		props := []string{"d:current-user-privilege-set", "d:sync-token", "c:supported-collation-set", "c:max-resource-size"}
		if calendars {
			props = append(props, "c:supported-calendar-component-set", "c:supported-calendar-data", "c:min-date-time",
				"c:max-date-time", "c:max-instances", "c:max-attendees-per-instance", "c:calendar-timezone")
		} else {
			props = append(props, "c:supported-address-data")
		}
		for _, prop := range props {
			if !strings.Contains(body, "<"+prop+"/>") {
				t.Errorf("calendars=%v: body lacks %s", calendars, prop)
			}
		}
	}
}

func TestCollectionPropertiesAreCappedAndInvalidValuesDropped(t *testing.T) {
	many := func(format string, n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteString(strings.ReplaceAll(format, "N", string(rune('a'+i%26))+strings.Repeat("x", i/26)))
		}
		return b.String()
	}
	props := `<c:supported-calendar-data>` + many(`<c:calendar-data content-type="text/N" version="2.0"/>`, 12) +
		`</c:supported-calendar-data><c:supported-collation-set>` +
		many(`<c:supported-collation>i;N</c:supported-collation>`, 20) + `</c:supported-collation-set>` +
		`<c:max-resource-size>-5</c:max-resource-size><c:min-date-time>1900-01-01</c:min-date-time>` +
		`<c:max-date-time>20491231T235959+0100</c:max-date-time><c:max-instances>1.5</c:max-instances>` +
		`<c:max-attendees-per-instance>+7</c:max-attendees-per-instance>` +
		`<c:calendar-timezone>BEGIN:VCALENDAR&#10;BEGIN:VTIMEZONE&#10;X-NOTE:` + strings.Repeat("a", 5000) +
		`&#10;TZID:Late/Zone&#10;END:VTIMEZONE</c:calendar-timezone>` +
		`<d:sync-token>` + strings.Repeat("t", 2000) + `</d:sync-token>`
	withProps(t, calHome, "c:calendar", props)
	c, _ := open(t, "calendar/work")
	page, err := c.list(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	got := page.Collections[0]
	if len(got.CalendarData) != 8 || len(got.Collations) != 16 {
		t.Errorf("formats = %d, collations = %d", len(got.CalendarData), len(got.Collations))
	}
	if got.MaxResourceSize != nil || got.MinDateTime != "" || got.MaxDateTime != "" || got.MaxInstances != nil ||
		got.MaxAttendeesPerInstance != nil || got.Timezone != "" {
		t.Errorf("invalid values were reported: %+v", got)
	}
	if len(got.SyncToken) != maxTokenLength {
		t.Errorf("sync token = %d bytes, want it cut at %d", len(got.SyncToken), maxTokenLength)
	}
}

func TestTimezoneFromCutValueNeedsCompleteTZIDLine(t *testing.T) {
	head := "BEGIN:VCALENDAR\nBEGIN:VTIMEZONE\nTZID:"
	for name, tc := range map[string]struct{ in, want string }{
		"complete":      {head + "Europe/Zurich\nEND:VTIMEZONE", "Europe/Zurich"},
		"cut in TZID":   {head + "Europe/Zur", ""},
		"cut after":     {head + "Europe/Zurich", ""},
		"folded":        {head + "Europe/\n Zurich\nEND:VTIMEZONE", "Europe/Zurich"},
		"parameter":     {"BEGIN:VTIMEZONE\nTZID;X-A=b:Asia/Tokyo\nEND:VTIMEZONE", "Asia/Tokyo"},
		"no timezone":   {"BEGIN:VCALENDAR\nEND:VCALENDAR", ""},
		"outside zone":  {"TZID:Nowhere\nBEGIN:VTIMEZONE\nEND:VTIMEZONE", ""},
		"after the end": {"BEGIN:VTIMEZONE\nEND:VTIMEZONE\nTZID:Late\nx", ""},
	} {
		if got := tzidOf(tc.in); got != tc.want {
			t.Errorf("%s: tzidOf = %q, want %q", name, got, tc.want)
		}
	}
}

func TestTooManyComponentsOrPrivilegesRefuseTheAnswer(t *testing.T) {
	for name, props := range map[string]string{
		"components": `<c:supported-calendar-component-set>` + strings.Repeat(`<c:comp name="VEVENT"/>`, 33) +
			`</c:supported-calendar-component-set>`,
		"privileges": `<d:current-user-privilege-set>` + strings.Repeat(`<d:privilege><d:read/></d:privilege>`, 33) +
			`</d:current-user-privilege-set>`,
	} {
		t.Run(name, func(t *testing.T) {
			withProps(t, calHome, "c:calendar", props)
			c, _ := open(t, "calendar/work")
			if _, err := c.list(context.Background(), true); classOf(err) != provider.ClassInvalidResponse {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestPrivilegesOutsideTheDAVNamespaceAreDropped(t *testing.T) {
	withProps(t, calHome, "c:calendar", `<d:current-user-privilege-set><d:privilege><c:read-free-busy/></d:privilege>`+
		`<d:privilege><d:all/></d:privilege></d:current-user-privilege-set>`)
	c, _ := open(t, "calendar/work")
	page, err := c.list(context.Background(), true)
	if err != nil || !reflect.DeepEqual(page.Collections[0].Privileges, []string{"all"}) {
		t.Errorf("page = %+v, err = %v", page, err)
	}
}

// TestCollectionFieldsMatchSchemaAndFields checks that every reported member is in the schema and in Fields.
func TestCollectionFieldsMatchSchemaAndFields(t *testing.T) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(collectionSchema), &schema); err != nil {
		t.Fatal(err)
	}
	fields := map[string]bool{}
	for _, f := range calendarsList.Fields {
		fields[f.Name] = true
	}
	same := map[string]bool{}
	for _, f := range addressbooksList.Fields {
		same[f.Name] = true
	}
	if !reflect.DeepEqual(fields, same) {
		t.Error("the two list tools declare different Fields")
	}
	for _, tc := range []struct {
		home, kind, props string
		calendars         bool
		target            string
	}{{calHome, "c:calendar", calendarProps, true, "calendar/work"},
		{bookHome, "card:addressbook", bookProps, false, "addressbook/work"}} {
		withProps(t, tc.home, tc.kind, tc.props)
		c, _ := open(t, tc.target)
		page, err := c.list(context.Background(), tc.calendars)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(page.Collections[0])
		var member map[string]any
		if err := json.Unmarshal(raw, &member); err != nil {
			t.Fatal(err)
		}
		var missing []string
		for key := range member {
			if _, ok := schema.Properties[key]; !ok {
				missing = append(missing, key+" (schema)")
			}
			if !fields[key] {
				missing = append(missing, key+" (fields)")
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			t.Errorf("%s: missing %v", tc.target, missing)
		}
	}
	for key := range schema.Properties {
		if !fields[key] {
			t.Errorf("schema member %s has no field", key)
		}
	}
}
