package infomaniakdav

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/emersion/go-ical"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const baseEvent = `"calendar":"work","summary":"Review","start":"2026-03-10T09:00:00","end":"2026-03-10T10:00:00","timezone":"Europe/Zurich"`

// writeFake answers discovery and hands the event resource to one handler.
func writeFake(t *testing.T, resource func(*http.Request) (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		if r.Method == "PROPFIND" {
			return davFake(t)(r)
		}
		return resource(r)
	}
}

func empty(status int, etag string) *http.Response {
	resp := xmlResponse(status, "")
	if etag != "" {
		resp.Header.Set("ETag", `"`+etag+`"`)
	}
	return resp
}

func writesOf(calls *[]call) []call {
	var out []call
	for _, c := range *calls {
		if c.method == "PUT" || c.method == "DELETE" {
			out = append(out, c)
		}
	}
	return out
}

func invokeWrite(handler capability.Handler, args string) (any, error) {
	return handler(context.Background(), resolved("calendar/work"), resolver(nil), nil, json.RawMessage(args))
}

func decodeICS(t *testing.T, data string) *ical.Calendar {
	t.Helper()
	calendar, err := ical.NewDecoder(strings.NewReader(data)).Decode()
	if err != nil {
		t.Fatalf("ics = %v\n%s", err, data)
	}
	return calendar
}

func TestEventsCreateSendsOneConditionalPut(t *testing.T) {
	calls := serve(t, writeFake(t, func(r *http.Request) (*http.Response, error) { return empty(201, "n1"), nil }))
	out, err := invokeWrite(invokeEventsCreate, `{`+baseEvent+`,"description":"Agenda","location":"Room 1",`+
		`"status":"confirmed","rrule":"FREQ=WEEKLY;COUNT=4","organizer":{"address":"me@example.org","name":"Me"},`+
		`"attendees":[{"address":"anna@example.org","name":"Anna, A."},{"address":"bob@example.org"}]}`)
	if err != nil {
		t.Fatalf("create = %v", err)
	}
	result := out.(*WriteResult)
	if !result.Created || result.ETag != "n1" || !strings.HasSuffix(result.ID, ".ics") || result.ID != result.UID+".ics" ||
		!validCollectionID(result.ID) {
		t.Fatalf("result = %+v", result)
	}
	puts := writesOf(calls)
	if len(puts) != 1 || puts[0].method != "PUT" || puts[0].path != "sync.infomaniak.com"+workPath+result.ID ||
		puts[0].header.Get("If-None-Match") != "*" || puts[0].header.Get("If-Match") != "" ||
		puts[0].header.Get("Content-Type") != "text/calendar; charset=utf-8" || puts[0].auth != basic() {
		t.Fatalf("puts = %+v", puts)
	}
	events := decodeICS(t, puts[0].body).Events()
	if len(events) != 1 {
		t.Fatalf("events = %d", len(events))
	}
	e := events[0]
	if uid, _ := e.Props.Text(ical.PropUID); uid != result.UID {
		t.Errorf("uid = %q", uid)
	}
	if p := e.Props.Get(ical.PropDateTimeStart); p.Value != "20260310T090000" || p.Params.Get("TZID") != "Europe/Zurich" {
		t.Errorf("dtstart = %+v", p)
	}
	if p := e.Props.Get(ical.PropRecurrenceRule); p.Value != "FREQ=WEEKLY;COUNT=4" {
		t.Errorf("rrule = %+v", p)
	}
	if status, _ := e.Props.Text(ical.PropStatus); status != "CONFIRMED" {
		t.Errorf("status = %q", status)
	}
	attendees := e.Props.Values(ical.PropAttendee)
	if len(attendees) != 2 || attendees[0].Value != "mailto:anna@example.org" ||
		attendees[0].Params.Get(ical.ParamCommonName) != "Anna, A." ||
		attendees[0].Params.Get(ical.ParamParticipationStatus) != "NEEDS-ACTION" {
		t.Errorf("attendees = %+v", attendees)
	}
	if o := e.Props.Get(ical.PropOrganizer); o == nil || o.Value != "mailto:me@example.org" {
		t.Errorf("organizer = %+v", o)
	}
	if e.Props.Get(ical.PropSequence) != nil {
		t.Error("a new event carries a SEQUENCE")
	}
}

func TestEventsCreateAllDayAndUTC(t *testing.T) {
	calls := serve(t, writeFake(t, func(r *http.Request) (*http.Response, error) { return empty(204, ""), nil }))
	out, err := invokeWrite(invokeEventsCreate, `{"calendar":"work","summary":"Holiday","all_day":true,"start":"2026-03-10"}`)
	if err != nil || out.(*WriteResult).ETag != "" {
		t.Fatalf("all day = %+v, %v", out, err)
	}
	e := decodeICS(t, writesOf(calls)[0].body).Events()[0]
	if s, en := e.Props.Get(ical.PropDateTimeStart), e.Props.Get(ical.PropDateTimeEnd); s.Value != "20260310" ||
		s.Params.Get("VALUE") != "DATE" || en.Value != "20260311" {
		t.Errorf("dates = %+v %+v", s, en)
	}
	if _, err := invokeWrite(invokeEventsCreate, `{"calendar":"work","summary":"U","start":"2026-03-10T09:00:00Z","end":"2026-03-10T10:00:00Z"}`); err != nil {
		t.Fatalf("utc = %v", err)
	}
	e = decodeICS(t, writesOf(calls)[1].body).Events()[0]
	if s := e.Props.Get(ical.PropDateTimeStart); s.Value != "20260310T090000Z" || s.Params.Get("TZID") != "" {
		t.Errorf("utc dtstart = %+v", s)
	}
}

func TestEventsCreateEscapesArguments(t *testing.T) {
	calls := serve(t, writeFake(t, func(r *http.Request) (*http.Response, error) { return empty(201, ""), nil }))
	evil := `x\r\nEND:VEVENT\r\nBEGIN:VEVENT\r\nATTENDEE:mailto:evil@example.org\nSEMI;COMMA,BACK\\`
	args := `{` + baseEvent + `,"summary":"` + evil + `","description":"` + evil + `","location":"` + evil + `"}`
	if _, err := invokeWrite(invokeEventsCreate, args); err != nil {
		t.Fatalf("create = %v", err)
	}
	body := writesOf(calls)[0].body
	lines := strings.Split(body, "\r\n")
	begins := 0
	for _, line := range lines {
		if line == "BEGIN:VEVENT" {
			begins++
		}
		if strings.HasPrefix(line, "ATTENDEE") {
			t.Errorf("attendee line injected: %q", line)
		}
	}
	if begins != 1 {
		t.Fatalf("injection reached the object:\n%s", body)
	}
	calendar := decodeICS(t, body)
	if len(calendar.Events()) != 1 || calendar.Events()[0].Props.Get(ical.PropAttendee) != nil {
		t.Fatalf("events = %+v", calendar.Events())
	}
	want := "x\n" + "END:VEVENT\nBEGIN:VEVENT\nATTENDEE:mailto:evil@example.org\nSEMI;COMMA,BACK\\"
	if got, _ := calendar.Events()[0].Props.Text(ical.PropSummary); got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

func TestEventsWritesRefuseBeforeAnyIO(t *testing.T) {
	calls := serve(t, writeFake(t, func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected %s", r.Method)
		return empty(500, ""), nil
	}))
	manyAttendees := make([]string, 51)
	for i := range manyAttendees {
		manyAttendees[i] = `{"address":"a` + string(rune('a'+i%26)) + string(rune('a'+i/26)) + `@example.org"}`
	}
	creates := map[string]string{
		"calendar not allowed": `{"calendar":"other","summary":"s","start":"2026-03-10","all_day":true}`,
		"calendar traversal":   `{"calendar":"../work","summary":"s","start":"2026-03-10","all_day":true}`,
		"id argument":          `{"calendar":"work","id":"x.ics","summary":"s","start":"2026-03-10","all_day":true}`,
		"etag argument":        `{"calendar":"work","etag":"e","summary":"s","start":"2026-03-10","all_day":true}`,
		"no summary":           `{"calendar":"work","start":"2026-03-10","all_day":true}`,
		"summary control":      `{"calendar":"work","summary":"a\u0000b","start":"2026-03-10","all_day":true}`,
		"summary too long":     `{"calendar":"work","summary":"` + strings.Repeat("a", 257) + `","start":"2026-03-10","all_day":true}`,
		"bad timezone":         `{"calendar":"work","summary":"s","start":"2026-03-10T09:00:00","end":"2026-03-10T10:00:00","timezone":"Mars/Base"}`,
		"timezone traversal":   `{"calendar":"work","summary":"s","start":"2026-03-10T09:00:00","end":"2026-03-10T10:00:00","timezone":"../../etc/passwd"}`,
		"timezone Local":       `{"calendar":"work","summary":"s","start":"2026-03-10T09:00:00","end":"2026-03-10T10:00:00","timezone":"Local"}`,
		"local without zone":   `{"calendar":"work","summary":"s","start":"2026-03-10T09:00:00","end":"2026-03-10T10:00:00"}`,
		"offset time":          `{"calendar":"work","summary":"s","start":"2026-03-10T09:00:00+01:00","end":"2026-03-10T10:00:00+01:00","timezone":"Europe/Zurich"}`,
		"end before start":     `{"calendar":"work","summary":"s","start":"2026-03-10T10:00:00Z","end":"2026-03-10T09:00:00Z"}`,
		"mixed kinds":          `{"calendar":"work","summary":"s","start":"2026-03-10T10:00:00Z","end":"2026-03-10T11:00:00","timezone":"Europe/Zurich"}`,
		"no end":               `{"calendar":"work","summary":"s","start":"2026-03-10T10:00:00Z"}`,
		"all day with zone":    `{"calendar":"work","summary":"s","all_day":true,"start":"2026-03-10","timezone":"Europe/Zurich"}`,
		"all day bad date":     `{"calendar":"work","summary":"s","all_day":true,"start":"2026-02-30"}`,
		"bad rrule":            `{` + baseEvent + `,"rrule":"FREQ=SOMETIMES"}`,
		"rrule prefix":         `{` + baseEvent + `,"rrule":"RRULE:FREQ=DAILY"}`,
		"rrule newline":        `{` + baseEvent + `,"rrule":"FREQ=DAILY\nATTENDEE:mailto:x@example.org"}`,
		"rrule too long":       `{` + baseEvent + `,"rrule":"FREQ=DAILY;BYDAY=` + strings.Repeat("MO,", 200) + `MO"}`,
		"bad status":           `{` + baseEvent + `,"status":"DONE"}`,
		"attendee not mail":    `{` + baseEvent + `,"attendees":[{"address":"anna"}]}`,
		"attendee with name":   `{` + baseEvent + `,"attendees":[{"address":"Anna <anna@example.org>"}]}`,
		"attendee injection":   `{` + baseEvent + `,"attendees":[{"address":"a@example.org\r\nX:y"}]}`,
		"attendee name quote":  `{` + baseEvent + `,"attendees":[{"address":"a@example.org","name":"A\"B"}]}`,
		"attendee name ctl":    `{` + baseEvent + `,"attendees":[{"address":"a@example.org","name":"A\nB"}]}`,
		"attendee twice":       `{` + baseEvent + `,"attendees":[{"address":"a@example.org"},{"address":"A@example.org"}]}`,
		"too many attendees":   `{` + baseEvent + `,"attendees":[` + strings.Join(manyAttendees, ",") + `]}`,
		"bad organizer":        `{` + baseEvent + `,"organizer":{"address":"nobody"}}`,
		"unknown argument":     `{` + baseEvent + `,"url":"https://evil.example"}`,
		"raw ics":              `{` + baseEvent + `,"ics":"BEGIN:VCALENDAR"}`,
		"description too long": `{` + baseEvent + `,"description":"` + strings.Repeat("a", 4097) + `"}`,
	}
	for name, args := range creates {
		t.Run("create "+name, func(t *testing.T) {
			_, err := invokeEventsCreate(context.Background(), resolved("calendar/work"), nil, nil, json.RawMessage(args))
			if err == nil || strings.Contains(err.Error(), "other") || strings.Contains(err.Error(), "evil") {
				t.Errorf("err = %v", err)
			}
		})
		if strings.Contains(args, `"calendar":"other"`) || strings.Contains(args, `"id"`) || strings.Contains(args, `"etag"`) ||
			strings.Contains(args, "../work") {
			continue
		}
		t.Run("update "+name, func(t *testing.T) {
			args := strings.Replace(args, `"calendar":"work"`, `"calendar":"work","id":"x.ics","etag":"e1"`, 1)
			if _, err := invokeEventsUpdate(context.Background(), resolved("calendar/work"), nil, nil, json.RawMessage(args)); err == nil {
				t.Error("accepted")
			}
		})
	}
	targets := map[string]string{
		"calendar not allowed": `{"calendar":"other","id":"x.ics","etag":"e"}`,
		"id slash":             `{"calendar":"work","id":"a/b.ics","etag":"e"}`,
		"id dot dot":           `{"calendar":"work","id":"..","etag":"e"}`,
		"id percent":           `{"calendar":"work","id":"a%2Fb","etag":"e"}`,
		"etag missing":         `{"calendar":"work","id":"x.ics"}`,
		"etag empty":           `{"calendar":"work","id":"x.ics","etag":""}`,
		"etag star":            `{"calendar":"work","id":"x.ics","etag":"*"}`,
		"etag inner quote":     `{"calendar":"work","id":"x.ics","etag":"a\"b"}`,
		"etag weak":            `{"calendar":"work","id":"x.ics","etag":"W/\"a\""}`,
		"etag control":         `{"calendar":"work","id":"x.ics","etag":"a\r\nb"}`,
		"etag space":           `{"calendar":"work","id":"x.ics","etag":"a b"}`,
		"etag too long":        `{"calendar":"work","id":"x.ics","etag":"` + strings.Repeat("a", 257) + `"}`,
		"free header":          `{"calendar":"work","id":"x.ics","etag":"e","method":"POST"}`,
	}
	for name, args := range targets {
		t.Run("delete "+name, func(t *testing.T) {
			if _, err := invokeEventsDelete(context.Background(), resolved("calendar/work"), nil, nil, json.RawMessage(args)); err == nil {
				t.Error("accepted")
			}
		})
	}
	if len(*calls) != 0 {
		t.Errorf("requests = %v, want none", *calls)
	}
	if _, err := invokeEventsDelete(context.Background(), resolved("calendar/work"), nil, nil,
		json.RawMessage(`{"calendar":"other","id":"x.ics","etag":"e"}`)); classOf(err) != provider.ClassPermission {
		t.Errorf("allow-list err = %v", err)
	}
}

const storedEvent = "UID:stored-1\r\nSUMMARY:Old\r\nDTSTART:20260310T090000Z\r\nSEQUENCE:2\r\nBEGIN:VALARM\r\nACTION:DISPLAY\r\nEND:VALARM"

func updateFake(t *testing.T, stored, etag string, put func(*http.Request) (*http.Response, error)) func(*http.Request) (*http.Response, error) {
	return writeFake(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			resp := xmlResponse(200, stored)
			if etag != "" {
				resp.Header.Set("ETag", `"`+etag+`"`)
			}
			return resp, nil
		}
		return put(r)
	})
}

func TestEventsUpdateKeepsUIDAndSendsOneIfMatchPut(t *testing.T) {
	calls := serve(t, updateFake(t, icsOf(vevent(storedEvent)), "e1", func(r *http.Request) (*http.Response, error) {
		return empty(204, "e2"), nil
	}))
	out, err := invokeWrite(invokeEventsUpdate, `{`+baseEvent+`,"id":"x.ics","etag":"\"e1\""}`)
	if err != nil {
		t.Fatalf("update = %v", err)
	}
	if result := out.(*WriteResult); !result.Updated || result.UID != "stored-1" || result.ETag != "e2" || result.ID != "x.ics" {
		t.Errorf("result = %+v", result)
	}
	puts := writesOf(calls)
	if len(puts) != 1 || puts[0].header.Get("If-Match") != `"e1"` || puts[0].header.Get("If-None-Match") != "" ||
		puts[0].path != "sync.infomaniak.com"+workPath+"x.ics" {
		t.Fatalf("puts = %+v", puts)
	}
	e := decodeICS(t, puts[0].body).Events()[0]
	if uid, _ := e.Props.Text(ical.PropUID); uid != "stored-1" {
		t.Errorf("uid = %q", uid)
	}
	if seq := e.Props.Get(ical.PropSequence); seq == nil || seq.Value != "3" {
		t.Errorf("sequence = %+v", seq)
	}
	if len(e.Children) != 0 || strings.Contains(puts[0].body, "VALARM") {
		t.Error("an unmodelled component was carried over")
	}
}

func TestEventsUpdateRefusesWithoutPut(t *testing.T) {
	cases := map[string]struct {
		stored, etag string
		class        provider.Class
	}{
		"etag changed":  {icsOf(vevent(storedEvent)), "other", provider.ClassProviderError},
		"override":      {icsOf(vevent(storedEvent), vevent("UID:stored-1", "RECURRENCE-ID:20260317T090000Z", "DTSTART:20260317T110000Z")), "e1", provider.ClassProviderError},
		"second event":  {icsOf(vevent(storedEvent), vevent("UID:two", "DTSTART:20260317T110000Z")), "e1", provider.ClassProviderError},
		"todo":          {icsOf("BEGIN:VTODO\r\nUID:t\r\nEND:VTODO\r\n"), "e1", provider.ClassProviderError},
		"not ical":      {"nope " + bodyCanary, "e1", provider.ClassInvalidResponse},
		"no uid":        {icsOf(vevent("SUMMARY:x", "DTSTART:20260310T090000Z")), "e1", provider.ClassInvalidResponse},
		"override only": {icsOf(vevent("UID:o", "RECURRENCE-ID:20260317T090000Z", "DTSTART:20260317T110000Z")), "e1", provider.ClassInvalidResponse},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			calls := serve(t, updateFake(t, c.stored, c.etag, func(*http.Request) (*http.Response, error) {
				t.Error("PUT sent")
				return empty(204, ""), nil
			}))
			_, err := invokeWrite(invokeEventsUpdate, `{`+baseEvent+`,"id":"x.ics","etag":"e1"}`)
			if err == nil || strings.Contains(err.Error(), bodyCanary) || len(writesOf(calls)) != 0 {
				t.Errorf("err = %v", err)
			}
			if name != "override only" && name != "no uid" && name != "not ical" && classOf(err) != c.class {
				t.Errorf("class = %v", classOf(err))
			}
		})
	}
}

func TestEventsUpdateAndDeleteReportPreconditionFailed(t *testing.T) {
	calls := serve(t, updateFake(t, icsOf(vevent(storedEvent)), "e1", func(*http.Request) (*http.Response, error) {
		return xmlResponse(412, bodyCanary), nil
	}))
	_, err := invokeWrite(invokeEventsUpdate, `{`+baseEvent+`,"id":"x.ics","etag":"e1"}`)
	if err == nil || !strings.Contains(err.Error(), "precondition failed") || strings.Contains(err.Error(), bodyCanary) ||
		strings.Contains(err.Error(), "may have taken effect") || len(writesOf(calls)) != 1 {
		t.Errorf("update err = %v", err)
	}
	calls = serve(t, writeFake(t, func(*http.Request) (*http.Response, error) { return xmlResponse(412, bodyCanary), nil }))
	_, err = invokeWrite(invokeEventsDelete, `{"calendar":"work","id":"x.ics","etag":"e1"}`)
	if err == nil || !strings.Contains(err.Error(), "precondition failed") || strings.Contains(err.Error(), bodyCanary) ||
		len(writesOf(calls)) != 1 {
		t.Errorf("delete err = %v", err)
	}
	serve(t, writeFake(t, func(*http.Request) (*http.Response, error) { return xmlResponse(412, bodyCanary), nil }))
	if _, err = invokeWrite(invokeEventsCreate, `{`+baseEvent+`}`); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("create err = %v", err)
	}
}

func TestEventsDeleteSendsOneIfMatchDelete(t *testing.T) {
	calls := serve(t, writeFake(t, func(*http.Request) (*http.Response, error) { return empty(204, ""), nil }))
	out, err := invokeWrite(invokeEventsDelete, `{"calendar":"work","id":"x.ics","etag":"e1"}`)
	if result, _ := out.(*WriteResult); err != nil || result == nil || !result.Deleted {
		t.Fatalf("delete = %+v, %v", out, err)
	}
	dels := writesOf(calls)
	if len(dels) != 1 || dels[0].method != "DELETE" || dels[0].header.Get("If-Match") != `"e1"` ||
		dels[0].path != "sync.infomaniak.com"+workPath+"x.ics" || dels[0].body != "" {
		t.Errorf("deletes = %+v", dels)
	}
	if len(*calls) != 3 {
		t.Errorf("requests = %d, want two discovery reads and one DELETE", len(*calls))
	}
}

// An unclear result is reported as such, with exactly one write request.
func TestEventsWritesAreNeverRepeatedAfterUnclearResult(t *testing.T) {
	failures := map[string]func(*http.Request) (*http.Response, error){
		"500":     func(*http.Request) (*http.Response, error) { return xmlResponse(500, bodyCanary), nil },
		"503":     func(*http.Request) (*http.Response, error) { return xmlResponse(503, bodyCanary), nil },
		"504":     func(*http.Request) (*http.Response, error) { return xmlResponse(504, bodyCanary), nil },
		"timeout": func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
		"reset":   func(*http.Request) (*http.Response, error) { return nil, syscall.ECONNRESET },
		"unknown": func(*http.Request) (*http.Response, error) { return nil, errors.New(bodyCanary) },
		"odd 2xx": func(*http.Request) (*http.Response, error) { return xmlResponse(202, bodyCanary), nil },
	}
	run := map[string]func() (any, error){
		"create": func() (any, error) { return invokeWrite(invokeEventsCreate, `{`+baseEvent+`}`) },
		"update": func() (any, error) {
			return invokeWrite(invokeEventsUpdate, `{`+baseEvent+`,"id":"x.ics","etag":"e1"}`)
		},
		"delete": func() (any, error) {
			return invokeWrite(invokeEventsDelete, `{"calendar":"work","id":"x.ics","etag":"e1"}`)
		},
	}
	for fname, failure := range failures {
		for rname, invoke := range run {
			t.Run(rname+" "+fname, func(t *testing.T) {
				calls := serve(t, updateFake(t, icsOf(vevent(storedEvent)), "e1", failure))
				_, err := invoke()
				if err == nil || !strings.Contains(err.Error(), "may have taken effect") ||
					strings.Contains(err.Error(), bodyCanary) || len(writesOf(calls)) != 1 {
					t.Errorf("err = %v, writes = %d", err, len(writesOf(calls)))
				}
			})
		}
	}
	// A refusal that provably did not reach the server stays a plain error.
	for status, class := range map[int]provider.Class{401: provider.ClassAuth, 403: provider.ClassPermission,
		404: provider.ClassNotFound, 429: provider.ClassRateLimited} {
		calls := serve(t, writeFake(t, func(*http.Request) (*http.Response, error) { return xmlResponse(status, bodyCanary), nil }))
		_, err := invokeWrite(invokeEventsDelete, `{"calendar":"work","id":"x.ics","etag":"e1"}`)
		if classOf(err) != class || strings.Contains(err.Error(), "may have taken effect") ||
			strings.Contains(err.Error(), bodyCanary) || len(writesOf(calls)) != 1 {
			t.Errorf("status %d: err = %v", status, err)
		}
	}
}

func TestEventsWriteRefusesRedirect(t *testing.T) {
	calls := serve(t, writeFake(t, func(*http.Request) (*http.Response, error) {
		resp := xmlResponse(307, "")
		resp.Header.Set("Location", "https://evil.example/x")
		return resp, nil
	}))
	_, err := invokeWrite(invokeEventsDelete, `{"calendar":"work","id":"x.ics","etag":"e1"}`)
	if err == nil || strings.Contains(err.Error(), "evil") || len(writesOf(calls)) != 1 {
		t.Errorf("err = %v", err)
	}
}

func TestEventsWritesThroughCoreNeedConfirmationAndToolList(t *testing.T) {
	calls := serve(t, writeFake(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == "DELETE" {
			return empty(204, ""), nil
		}
		return empty(201, "n"), nil
	}))
	reg := capability.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatal(err)
	}
	connection := func(tools []string) *application.Core {
		cfg := &config.Config{
			Version:     1,
			Services:    map[string]config.Service{"dav": {Provider: Provider}},
			Credentials: map[string]config.Credential{"dav": resolved().Secrets},
			Connections: map[string]config.Connection{"main": {Service: "dav", Credential: "dav",
				Permissions: config.Permissions(), Tools: tools, Targets: []string{"calendar/work"}}},
		}
		red := &redact.Redactor{}
		return application.New(reg, cfg, resolver(red), red)
	}
	core := connection(nil)
	request := application.InvokeRequest{Operation: eventsCreate.ID, Connection: "main",
		Arguments: json.RawMessage(`{` + baseEvent + `}`)}
	if _, err := core.Invoke(context.Background(), request); err == nil || len(*calls) != 0 {
		t.Fatalf("unconfirmed create: err = %v, requests = %d", err, len(*calls))
	}
	request.Confirmed = true
	if _, err := core.Invoke(context.Background(), request); err != nil || len(writesOf(calls)) != 1 {
		t.Fatalf("confirmed create: err = %v", err)
	}
	before := len(*calls)
	del := application.InvokeRequest{Operation: eventsDelete.ID, Connection: "main", Confirmed: true,
		Arguments: json.RawMessage(`{"calendar":"work","id":"x.ics","etag":"e1"}`)}
	if _, err := core.Invoke(context.Background(), del); err == nil || len(*calls) != before {
		t.Fatalf("delete without a tools list: err = %v", err)
	}
	listed := connection([]string{eventsDelete.ID})
	if _, err := listed.Invoke(context.Background(), del); err != nil || len(writesOf(calls)) != 2 {
		t.Fatalf("delete with a tools list: err = %v", err)
	}
}
