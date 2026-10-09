package dav

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

var testServer = Server{Name: "Example", Origin: "https://dav.example.org"}

func classOf(err error) provider.Class {
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		return providerErr.Class
	}
	return ""
}

func icsOf(vevents ...string) string {
	return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\n" + strings.Join(vevents, "") + "END:VCALENDAR\r\n"
}

func vevent(lines ...string) string {
	return "BEGIN:VEVENT\r\n" + strings.Join(lines, "\r\n") + "\r\nEND:VEVENT\r\n"
}

func multistatus(entries ...string) string {
	return `<?xml version="1.0"?><d:multistatus xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav" ` +
		`xmlns:card="urn:ietf:params:xml:ns:carddav" xmlns:a="http://apple.com/ns/ical/">` +
		strings.Join(entries, "") + `</d:multistatus>`
}

func TestEntryCountAndDepthAreCapped(t *testing.T) {
	many := make([]string, MaxEntries+1)
	for i := range many {
		many[i] = `<d:response><d:href>/x/</d:href></d:response>`
	}
	if _, err := testServer.ParseMultiStatus("t", []byte(multistatus(many...))); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("entries err = %v", err)
	}
	deep := `<d:multistatus xmlns:d="DAV:">` + strings.Repeat("<d:x>", 40) + strings.Repeat("</d:x>", 40) + `</d:multistatus>`
	if _, err := testServer.ParseMultiStatus("t", []byte(deep)); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("depth err = %v", err)
	}
}

func TestMultiStatusErrorsNameTheServer(t *testing.T) {
	_, err := testServer.ParseMultiStatus("t", []byte("<broken"))
	if err == nil || !strings.Contains(err.Error(), "Example returned an invalid multi-status document") {
		t.Errorf("err = %v", err)
	}
}

func TestSegmentsRefuseUnsafeLocations(t *testing.T) {
	good, err := testServer.Segments("t", "https://dav.example.org/a/b%20c/")
	if err != nil || len(good) != 2 || good[1] != "b c" {
		t.Errorf("good = %v, err = %v", good, err)
	}
	for _, href := range []string{"", "https://other.example.org/a", "/a/../b", "/a/%2Fb", "/a?x=1", "/a#f", "a/b",
		"/a/%00", (&url.URL{Scheme: "https", User: url.User("u"), Host: "dav.example.org", Path: "/a"}).String()} {
		if _, err := testServer.Segments("t", href); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("href %q err = %v", href, err)
		}
	}
}

func TestEventTextIsBounded(t *testing.T) {
	event, err := ParseEvent(icsOf(vevent("UID:u", "DTSTART:20260310T090000Z",
		"SUMMARY:"+strings.Repeat("ä", 1000), "DESCRIPTION:"+strings.Repeat("b", 10000),
		"LOCATION:"+strings.Repeat("c", 1000))), true)
	if err != nil || len(event.Summary) > MaxSummaryLength || len(event.Description) > MaxEventDescription ||
		len(event.Location) > MaxLocationLength {
		t.Errorf("event = %d/%d/%d, err = %v", len(event.Summary), len(event.Description), len(event.Location), err)
	}
}

func TestEventDraftRoundTrip(t *testing.T) {
	draft, err := NewEventDraft("t", "-//Test//EN", EventInput{Summary: "Review", Start: "2026-03-10T09:00:00",
		End: "2026-03-10T10:00:00", Timezone: "Europe/Zurich", RRule: "FREQ=WEEKLY;COUNT=3"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := draft.Encode("t", "uid-1", 2, time.Now())
	if err != nil || !strings.Contains(body, "PRODID:-//Test//EN") || !strings.Contains(body, "SEQUENCE;VALUE=TEXT:2") {
		t.Fatalf("body = %q, err = %v", body, err)
	}
	event, err := ParseEvent(body, true)
	if err != nil || event.UID != "uid-1" || event.Start != "2026-03-10T09:00:00" || event.Timezone != "Europe/Zurich" ||
		event.RRule != "FREQ=WEEKLY;COUNT=3" {
		t.Errorf("event = %+v, err = %v", event, err)
	}
	existing, err := testServer.ExistingEventOf("t", []byte(body))
	if err != nil || existing.UID != "uid-1" || existing.Sequence != 2 {
		t.Errorf("existing = %+v, err = %v", existing, err)
	}
}

func TestEventDraftRefusesInvalidInput(t *testing.T) {
	base := EventInput{Summary: "x", Start: "2026-03-10T09:00:00", End: "2026-03-10T10:00:00", Timezone: "Europe/Zurich"}
	for name, mutate := range map[string]func(*EventInput){
		"no summary":    func(in *EventInput) { in.Summary = " " },
		"bad zone":      func(in *EventInput) { in.Timezone = "Mars/Base" },
		"local no zone": func(in *EventInput) { in.Timezone = "" },
		"end before":    func(in *EventInput) { in.End = "2026-03-10T08:00:00" },
		"bad rrule":     func(in *EventInput) { in.RRule = "FREQ=NEVER" },
		"bad status":    func(in *EventInput) { in.Status = "MAYBE" },
	} {
		input := base
		mutate(&input)
		if _, err := NewEventDraft("t", "p", input); classOf(err) != provider.ClassProviderError {
			t.Errorf("%s err = %v", name, err)
		}
	}
}

func TestContactCardRoundTripAndCaps(t *testing.T) {
	card, err := NewContactCard("t", ContactInput{Name: "Ada Lovelace",
		Emails: []TypedValue{{Value: "ada@example.org", Type: "work"}}, Note: "a\nb"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := EncodeContact("t", card, "uid-1")
	if err != nil {
		t.Fatal(err)
	}
	contact, err := ParseContact(body, true)
	if err != nil || contact.UID != "uid-1" || contact.Name != "Ada Lovelace" || len(contact.Emails) != 1 ||
		contact.Emails[0].Type != "work" || contact.Note != "a\nb" {
		t.Errorf("contact = %+v, err = %v", contact, err)
	}
	if uid, err := testServer.StoredContactUID("t", []byte(body)); err != nil || uid != "uid-1" {
		t.Errorf("uid = %q, err = %v", uid, err)
	}
	photo := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:A\r\nUID:u\r\nPHOTO:abc\r\nEND:VCARD\r\n"
	if _, err := testServer.StoredContactUID("t", []byte(photo)); classOf(err) != provider.ClassProviderError {
		t.Errorf("photo err = %v", err)
	}
	many := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:A\r\n" + strings.Repeat("EMAIL:a@example.org\r\n", MaxContactEntries+5) +
		"END:VCARD\r\n"
	parsed, err := ParseContact(many, true)
	if err != nil || len(parsed.Emails) != MaxContactEntries || len(parsed.Truncated) != 1 {
		t.Errorf("parsed = %+v, err = %v", parsed, err)
	}
}

func TestParseRangeAndETag(t *testing.T) {
	from, to, err := ParseRange("t", "2026-03-01T00:00:00+01:00", "2026-04-01T00:00:00Z")
	if err != nil || from != "20260228T230000Z" || to != "20260401T000000Z" {
		t.Errorf("range = %s/%s, err = %v", from, to, err)
	}
	if _, _, err := ParseRange("t", "2026-03-01T00:00:00Z", "2027-04-01T00:00:00Z"); err == nil {
		t.Error("range over 366 days accepted")
	}
	if got, ok := NormalETag(`"e1"`); !ok || got != "e1" {
		t.Errorf("etag = %q, %v", got, ok)
	}
	if _, ok := NormalETag("*"); ok {
		t.Error("wildcard etag accepted")
	}
}
