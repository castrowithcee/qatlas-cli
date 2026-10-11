package dav

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// Bounds of the task model.
const (
	MaxTaskDescription = 4096
	maxTaskCategories  = 20
	maxCategoryLength  = 64
)

// Task states a query can ask for.
const (
	TaskOpen      = "open"
	TaskCompleted = "completed"
)

// Task is one calendar task (VTODO).
type Task struct {
	ID          string   `json:"id"`
	UID         string   `json:"uid,omitempty"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status,omitempty"`
	Priority    int      `json:"priority,omitempty"`
	Due         string   `json:"due,omitempty"`
	Completed   string   `json:"completed,omitempty"`
	Percent     int      `json:"percent,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	ParentUID   string   `json:"parent_uid,omitempty"`
	ETag        string   `json:"etag,omitempty"`
}

var ErrNoTask = errors.New("no task")

// Done reports whether the task counts as finished.
func (t *Task) Done() bool {
	return t.Status == "COMPLETED" || t.Status == "CANCELLED" || t.Completed != ""
}

// MatchesState applies the state filter again to a parsed task, so a server that ignores a filter cannot
// widen the result. An empty state matches every task.
func (t *Task) MatchesState(state string) bool {
	switch state {
	case TaskOpen:
		return !t.Done()
	case TaskCompleted:
		return t.Status == "COMPLETED" || t.Completed != ""
	}
	return true
}

// TaskQueryBody asks for the tasks of a calendar. state is empty, TaskOpen, or TaskCompleted; dueFrom and
// dueTo are empty or in the form ParseTaskRange returns. They bound the DUE property, so a task without one
// never matches a range.
func TaskQueryBody(state, dueFrom, dueTo string) string {
	var filter strings.Builder
	if dueFrom != "" || dueTo != "" {
		filter.WriteString(`<c:prop-filter name="DUE"><c:time-range`)
		if dueFrom != "" {
			filter.WriteString(` start="` + dueFrom + `"`)
		}
		if dueTo != "" {
			filter.WriteString(` end="` + dueTo + `"`)
		}
		filter.WriteString(`/></c:prop-filter>`)
	}
	switch state {
	case TaskOpen:
		filter.WriteString(`<c:prop-filter name="COMPLETED"><c:is-not-defined/></c:prop-filter>`)
	case TaskCompleted:
		filter.WriteString(`<c:prop-filter name="COMPLETED"/>`)
	}
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">` +
		`<d:prop><d:getetag/><c:calendar-data/></d:prop>` +
		`<c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VTODO">` + filter.String() +
		`</c:comp-filter></c:comp-filter></c:filter></c:calendar-query>`
}

// ParseTaskRange validates an optional RFC 3339 due range. Either bound may be empty; given both, the end
// must follow the start. The results are in the UTC form of a CalDAV time-range.
func ParseTaskRange(op, start, end string) (string, string, error) {
	var from, to time.Time
	var err error
	if start != "" {
		if from, err = time.Parse(time.RFC3339, start); err != nil {
			return "", "", provider.Fail(op, "due_from must be an RFC 3339 time")
		}
	}
	if end != "" {
		if to, err = time.Parse(time.RFC3339, end); err != nil {
			return "", "", provider.Fail(op, "due_to must be an RFC 3339 time")
		}
	}
	if start != "" && end != "" && !to.After(from) {
		return "", "", provider.Fail(op, "due_to must be after due_from")
	}
	if start != "" {
		start = from.UTC().Format(utcStampLayout)
	}
	if end != "" {
		end = to.UTC().Format(utcStampLayout)
	}
	return start, end, nil
}

// ParseTask reads the first task of an iCalendar object, preferring the main one of a recurring series over
// its overrides. The description is capped at descriptionMax.
func ParseTask(data string, descriptionMax int) (task *Task, err error) {
	defer func() {
		if recover() != nil {
			task, err = nil, ErrNoTask
		}
	}()
	calendar, err := ical.NewDecoder(strings.NewReader(data)).Decode()
	if err != nil {
		return nil, err
	}
	var chosen *ical.Component
	for _, child := range calendar.Children {
		if child.Name != ical.CompToDo {
			continue
		}
		if child.Props.Get(ical.PropRecurrenceID) == nil {
			chosen = child
			break
		}
		if chosen == nil {
			chosen = child
		}
	}
	if chosen == nil {
		return nil, ErrNoTask
	}
	task = &Task{
		UID:         Clean(textOf(chosen, ical.PropUID), maxEventTextLength),
		Title:       Clean(textOf(chosen, ical.PropSummary), MaxSummaryLength),
		Description: CleanText(textOf(chosen, ical.PropDescription), descriptionMax, true),
		Status:      Clean(textOf(chosen, ical.PropStatus), 32),
		Priority:    boundedInt(chosen, ical.PropPriority, 9),
		Percent:     boundedInt(chosen, ical.PropPercentComplete, 100),
	}
	task.Due, _, _ = timeOf(chosen.Props.Get(ical.PropDue))
	task.Completed, _, _ = timeOf(chosen.Props.Get(ical.PropCompleted))
	for _, prop := range chosen.Props.Values(ical.PropCategories) {
		names, err := prop.TextList()
		if err != nil {
			continue
		}
		for _, name := range names {
			if name = Clean(strings.TrimSpace(name), maxCategoryLength); name != "" {
				if len(task.Categories) >= maxTaskCategories {
					break
				}
				task.Categories = append(task.Categories, name)
			}
		}
	}
	for _, prop := range chosen.Props.Values(ical.PropRelatedTo) {
		if kind := prop.Params.Get("RELTYPE"); kind == "" || strings.EqualFold(kind, "PARENT") {
			task.ParentUID = Clean(strings.TrimSpace(prop.Value), maxEventTextLength)
			break
		}
	}
	return task, nil
}

// boundedInt reads an integer property and yields 0 for a missing, malformed, or out-of-range value.
func boundedInt(component *ical.Component, name string, max int) int {
	prop := component.Props.Get(name)
	if prop == nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(prop.Value))
	if err != nil || n < 0 || n > max {
		return 0
	}
	return n
}
