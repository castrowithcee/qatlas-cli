package dav

import (
	"strings"
	"testing"
	"time"
)

var taskNow = time.Date(2026, 3, 10, 8, 30, 0, 0, time.UTC)

func TestTaskEncodeGolden(t *testing.T) {
	draft, err := NewTaskDraft("create task", "-//Test//EN", TaskInput{
		Title: "Send, invoice; now", Description: "line one\nline two", Status: "completed", Priority: 3,
		Due: "2026-03-12T10:00:00+01:00", Categories: []string{"work", "a,b"}, ParentUID: "mom",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := draft.Encode("create task", "u-1", TaskCarry{}, taskNow)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"BEGIN:VCALENDAR", "PRODID:-//Test//EN", "VERSION:2.0", "BEGIN:VTODO", "CATEGORIES:work,a\\,b",
		"COMPLETED:20260310T083000Z", "DESCRIPTION:line one\\nline two", "DTSTAMP:20260310T083000Z",
		"DUE:20260312T090000Z", "LAST-MODIFIED:20260310T083000Z", "PERCENT-COMPLETE:100", "PRIORITY:3",
		"RELATED-TO:mom", "STATUS:COMPLETED", "SUMMARY:Send\\, invoice\\; now", "UID:u-1", "END:VTODO",
		"END:VCALENDAR", ""}, "\r\n")
	if got != want {
		t.Errorf("task =\n%s\nwant\n%s", got, want)
	}
}

func TestTaskDraftRefusesInvalidFields(t *testing.T) {
	for name, input := range map[string]TaskInput{
		"no title":      {},
		"control":       {Title: "a\x00b"},
		"status":        {Title: "t", Status: "DONE"},
		"priority":      {Title: "t", Priority: 10},
		"percent":       {Title: "t", Percent: 101},
		"done percent":  {Title: "t", Status: "COMPLETED", Percent: 50},
		"due":           {Title: "t", Due: "tomorrow"},
		"empty tag":     {Title: "t", Categories: []string{" "}},
		"many tags":     {Title: "t", Categories: make([]string, 21)},
		"parent":        {Title: "t", ParentUID: "a\nb"},
		"long title":    {Title: strings.Repeat("x", 257)},
		"long describe": {Title: "t", Description: strings.Repeat("x", 4097)},
	} {
		if _, err := NewTaskDraft("create task", "-//Test//EN", input); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTaskEncodeCarriesCompletionAndKeepsRelations(t *testing.T) {
	stored := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\nBEGIN:VTODO\r\nUID:u9\r\nDTSTAMP:20260101T000000Z\r\n" +
		"CREATED:20260101T000000Z\r\nSEQUENCE:4\r\nCOMPLETED:20260102T030405Z\r\nRELATED-TO;RELTYPE=CHILD:kid\r\n" +
		"RELATED-TO:mom\r\nSUMMARY:S\r\nEND:VTODO\r\nEND:VCALENDAR\r\n"
	existing, err := Server{Name: "Nextcloud"}.ExistingTaskOf("update task", []byte(stored))
	if err != nil || existing.UID != "u9" || existing.Carry.Sequence != 5 {
		t.Fatalf("existing = %+v, %v", existing, err)
	}
	draft, err := NewTaskDraft("update task", "-//Test//EN", TaskInput{Title: "S", Status: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := draft.Encode("update task", existing.UID, existing.Carry, taskNow)
	for _, want := range []string{"COMPLETED:20260102T030405Z", "CREATED:20260101T000000Z", "SEQUENCE:5",
		"RELATED-TO;RELTYPE=CHILD:kid", "RELATED-TO:mom", "PERCENT-COMPLETE:100", "UID:u9"} {
		if !strings.Contains(got, want) {
			t.Errorf("task lacks %q:\n%s", want, got)
		}
	}
	reopened, _ := NewTaskDraft("update task", "-//Test//EN", TaskInput{Title: "S", Status: "IN-PROCESS", Percent: 40})
	got, _ = reopened.Encode("update task", "u9", existing.Carry, taskNow)
	if strings.Contains(got, "COMPLETED:") || !strings.Contains(got, "PERCENT-COMPLETE:40") {
		t.Errorf("reopened task:\n%s", got)
	}
}

func TestExistingTaskOfRefusesUnmodeledContent(t *testing.T) {
	wrap := func(inner string) string {
		return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\n" + inner + "END:VCALENDAR\r\n"
	}
	task := func(extra string) string {
		return wrap("BEGIN:VTODO\r\nUID:u1\r\nSUMMARY:S\r\n" + extra + "END:VTODO\r\n")
	}
	for name, data := range map[string]string{
		"rrule":   task("RRULE:FREQ=DAILY\r\n"),
		"alarm":   task("BEGIN:VALARM\r\nACTION:DISPLAY\r\nTRIGGER:-PT5M\r\nDESCRIPTION:x\r\nEND:VALARM\r\n"),
		"attach":  task("ATTACH:https://example.invalid/a\r\n"),
		"x prop":  task("X-CUSTOM:1\r\n"),
		"dtstart": task("DTSTART:20260310T090000Z\r\n"),
		"two":     wrap("BEGIN:VTODO\r\nUID:u1\r\nEND:VTODO\r\nBEGIN:VTODO\r\nUID:u2\r\nEND:VTODO\r\n"),
		"cal prop": "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//x//EN\r\nX-WR-CALNAME:n\r\n" +
			"BEGIN:VTODO\r\nUID:u1\r\nEND:VTODO\r\nEND:VCALENDAR\r\n",
	} {
		if _, err := (Server{Name: "Nextcloud"}).ExistingTaskOf("update task", []byte(data)); err == nil ||
			!strings.Contains(err.Error(), "does not write") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	event := wrap("BEGIN:VEVENT\r\nUID:e\r\nDTSTART:20260310T090000Z\r\nEND:VEVENT\r\n")
	if _, err := (Server{Name: "Nextcloud"}).ExistingTaskOf("update task", []byte(event)); err == nil ||
		!strings.Contains(err.Error(), "does not name a task") {
		t.Errorf("event: err = %v", err)
	}
	if _, err := (Server{Name: "Nextcloud"}).ExistingTaskOf("update task", []byte(task(""))); err != nil {
		t.Errorf("plain task: %v", err)
	}
}

func TestTaskParentQueryBodyEscapes(t *testing.T) {
	if got := TaskParentQueryBody(`a<b>&"`); !strings.Contains(got, `collation="i;octet">a&lt;b&gt;&amp;&#34;<`) {
		t.Errorf("body = %s", got)
	}
}
