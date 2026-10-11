package dav

import (
	"bytes"
	"encoding/xml"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

var completedPattern = regexp.MustCompile(`^\d{8}T\d{6}Z$`)

// TaskInput is the structured content of a task to write.
type TaskInput struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Priority    int      `json:"priority"`
	Due         string   `json:"due"`
	Percent     int      `json:"percent"`
	Categories  []string `json:"categories"`
	ParentUID   string   `json:"parent_uid"`
}

// TaskDraft is a validated task, ready to be encoded with a UID.
type TaskDraft struct {
	title, description, status, parent, productID string
	priority, percent                             int
	due                                           *ical.Prop
	categories                                    []string
}

// TaskCarry is what a replacement takes over from the stored task: its sequence number, its creation time,
// an earlier completion time, and its relations, none of which an argument can set.
type TaskCarry struct {
	Sequence  int
	Created   *ical.Prop
	Completed string
	Related   []ical.Prop
}

// ParentUID is the parent relation the draft sets; only a new task can have one.
func (d *TaskDraft) ParentUID() string { return d.parent }

// NewTaskDraft validates every argument without any I/O. No message quotes a value.
func NewTaskDraft(op, productID string, input TaskInput) (*TaskDraft, error) {
	draft := &TaskDraft{productID: productID, priority: input.Priority, percent: input.Percent}
	var ok bool
	if draft.title, ok = ValidText(input.Title, MaxSummaryLength, false); !ok || strings.TrimSpace(draft.title) == "" {
		return nil, provider.Fail(op, "title is required, at most 256 bytes, without control characters")
	}
	if draft.description, ok = ValidText(input.Description, MaxTaskDescription, true); !ok {
		return nil, provider.Fail(op, "description must be at most 4096 bytes without control characters")
	}
	switch status := strings.ToUpper(input.Status); status {
	case "":
		draft.status = "NEEDS-ACTION"
	case "NEEDS-ACTION", "IN-PROCESS", "COMPLETED", "CANCELLED":
		draft.status = status
	default:
		return nil, provider.Fail(op, "status must be NEEDS-ACTION, IN-PROCESS, COMPLETED, or CANCELLED")
	}
	if draft.priority < 0 || draft.priority > 9 {
		return nil, provider.Fail(op, "priority must be between 0 and 9")
	}
	if draft.percent < 0 || draft.percent > 100 || draft.status == "COMPLETED" && draft.percent != 0 && draft.percent != 100 {
		return nil, provider.Fail(op, "percent must be between 0 and 100, and 100 for a completed task")
	}
	if draft.status == "COMPLETED" {
		draft.percent = 100
	}
	if input.Due != "" {
		due, err := dueProp(input.Due)
		if err != nil {
			return nil, provider.Fail(op, "due must be a date YYYY-MM-DD or an RFC 3339 time")
		}
		draft.due = due
	}
	if len(input.Categories) > maxTaskCategories {
		return nil, provider.Fail(op, "a task takes at most 20 categories")
	}
	for _, entry := range input.Categories {
		name, ok := ValidText(strings.TrimSpace(entry), maxCategoryLength, false)
		if !ok || name == "" {
			return nil, provider.Fail(op, "a category must be 1 to 64 bytes without control characters")
		}
		draft.categories = append(draft.categories, name)
	}
	if input.ParentUID != "" {
		if draft.parent, ok = ValidText(input.ParentUID, maxEventTextLength, false); !ok || strings.TrimSpace(draft.parent) == "" {
			return nil, provider.Fail(op, "parent_uid must be a UID without control characters")
		}
	}
	if _, err := draft.Encode(op, "placeholder", TaskCarry{}, time.Now()); err != nil {
		return nil, err
	}
	return draft, nil
}

func dueProp(value string) (*ical.Prop, error) {
	if day, err := time.Parse(dateLayout, value); err == nil {
		return dateProp(ical.PropDue, day), nil
	}
	moment, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return valueProp(ical.PropDue, moment.UTC().Format(icalUTC), ""), nil
}

// Encode renders the draft as one iCalendar object. Text values are escaped by the encoder library, so a
// line break in an argument can never start a property of its own. A completed task gets a completion time:
// the carried one, otherwise now.
func (d *TaskDraft) Encode(op, uid string, carry TaskCarry, now time.Time) (string, error) {
	task := ical.NewComponent(ical.CompToDo)
	task.Props.SetText(ical.PropUID, uid)
	task.Props.SetDateTime(ical.PropDateTimeStamp, now.UTC())
	task.Props.SetDateTime(ical.PropLastModified, now.UTC())
	if carry.Created != nil {
		task.Props.Set(carry.Created)
	}
	task.Props.SetText(ical.PropSummary, d.title)
	if d.description != "" {
		task.Props.SetText(ical.PropDescription, d.description)
	}
	task.Props.SetText(ical.PropStatus, d.status)
	if d.priority > 0 {
		task.Props.Set(intProp(ical.PropPriority, d.priority))
	}
	if d.percent > 0 {
		task.Props.Set(intProp(ical.PropPercentComplete, d.percent))
	}
	if d.status == "COMPLETED" {
		done := now.UTC().Format(icalUTC)
		if completedPattern.MatchString(carry.Completed) {
			done = carry.Completed
		}
		task.Props.Set(valueProp(ical.PropCompleted, done, ""))
	}
	if d.due != nil {
		task.Props.Set(d.due)
	}
	if len(d.categories) > 0 {
		prop := ical.NewProp(ical.PropCategories)
		prop.SetTextList(d.categories)
		task.Props.Set(prop)
	}
	if carry.Sequence > 0 {
		task.Props.Set(intProp(ical.PropSequence, carry.Sequence))
	}
	if d.parent != "" {
		prop := ical.NewProp(ical.PropRelatedTo)
		prop.SetText(d.parent)
		task.Props.Add(prop)
	}
	for i := range carry.Related {
		task.Props.Add(&carry.Related[i])
	}
	calendar := ical.NewCalendar()
	calendar.Props.SetText(ical.PropVersion, "2.0")
	calendar.Props.SetText(ical.PropProductID, d.productID)
	calendar.Children = append(calendar.Children, task)
	var buf bytes.Buffer
	if err := ical.NewEncoder(&buf).Encode(calendar); err != nil || buf.Len() > MaxEventBytes {
		return "", provider.Fail(op, "the task could not be encoded within the size limit")
	}
	return buf.String(), nil
}

// SEQUENCE and the like are INTEGER; SetText would add VALUE=TEXT.
func intProp(name string, value int) *ical.Prop {
	prop := ical.NewProp(name)
	prop.Value = strconv.Itoa(value)
	return prop
}

// ExistingTask is the identity and the carried content of a stored task.
type ExistingTask struct {
	UID   string
	Carry TaskCarry
}

// managedTaskProps are the properties of a stored task that a replacement rebuilds from its arguments or
// carries over unchanged. Anything else would be lost, so it makes the replacement refuse.
var managedTaskProps = map[string]bool{
	ical.PropUID: true, ical.PropDateTimeStamp: true, ical.PropSummary: true, ical.PropDescription: true,
	ical.PropStatus: true, ical.PropPriority: true, ical.PropDue: true, ical.PropPercentComplete: true,
	ical.PropCompleted: true, ical.PropCategories: true, ical.PropRelatedTo: true, ical.PropSequence: true,
	ical.PropLastModified: true, ical.PropCreated: true,
}

// ExistingTaskOf reads the stored task for a replacement. It refuses an object that is no single plain task or
// carries a property or component that Qatlas does not model (recurrence, alarms, attachments, extensions),
// so a replacement never silently drops content. The carried sequence is already the next one.
func (s Server) ExistingTaskOf(op string, data []byte) (found ExistingTask, err error) {
	notValid := s.Name + " returned a task that is not a valid iCalendar object"
	defer func() {
		if recover() != nil {
			found, err = ExistingTask{}, provider.InvalidResponse(op, notValid)
		}
	}()
	calendar, decodeErr := ical.NewDecoder(bytes.NewReader(data)).Decode()
	if decodeErr != nil {
		return ExistingTask{}, provider.InvalidResponse(op, notValid)
	}
	unmodeled := provider.Fail(op, "this task carries content that Qatlas does not write (such as recurrence, alarms, attachments, or extensions); change it in a task application")
	var chosen *ical.Component
	for _, child := range calendar.Children {
		switch {
		case child.Name == ical.CompTimezone:
		case child.Name == ical.CompToDo && chosen == nil:
			chosen = child
		case child.Name == ical.CompEvent && chosen == nil:
			return ExistingTask{}, provider.Fail(op, "the id does not name a task")
		default:
			return ExistingTask{}, unmodeled
		}
	}
	if chosen == nil {
		return ExistingTask{}, provider.Fail(op, "the id does not name a task")
	}
	for name := range calendar.Props {
		if name != ical.PropVersion && name != ical.PropProductID && name != ical.PropCalendarScale {
			return ExistingTask{}, unmodeled
		}
	}
	if len(chosen.Children) > 0 {
		return ExistingTask{}, unmodeled
	}
	for name, values := range chosen.Props {
		if !managedTaskProps[name] || len(values) > 1 && name != ical.PropRelatedTo && name != ical.PropCategories {
			return ExistingTask{}, unmodeled
		}
	}
	uid, ok := ValidText(textOf(chosen, ical.PropUID), maxEventTextLength, false)
	if !ok || strings.TrimSpace(uid) == "" {
		return ExistingTask{}, provider.InvalidResponse(op, s.Name+" returned a task without a usable UID")
	}
	found = ExistingTask{UID: uid}
	if prop := chosen.Props.Get(ical.PropSequence); prop != nil {
		if n, convErr := strconv.Atoi(strings.TrimSpace(prop.Value)); convErr == nil && n >= 0 && n < maxSequence {
			found.Carry.Sequence = n
		}
	}
	found.Carry.Sequence++
	if prop := chosen.Props.Get(ical.PropCreated); prop != nil {
		found.Carry.Created = prop
	}
	if prop := chosen.Props.Get(ical.PropCompleted); prop != nil {
		found.Carry.Completed = strings.TrimSpace(prop.Value)
	}
	found.Carry.Related = chosen.Props.Values(ical.PropRelatedTo)
	return found, nil
}

// TaskParentQueryBody asks for the tasks with exactly this UID.
func TaskParentQueryBody(uid string) string {
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(uid))
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<c:calendar-query xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">` +
		`<d:prop><d:getetag/><c:calendar-data/></d:prop>` +
		`<c:filter><c:comp-filter name="VCALENDAR"><c:comp-filter name="VTODO">` +
		`<c:prop-filter name="UID"><c:text-match collation="i;octet">` + escaped.String() + `</c:text-match></c:prop-filter>` +
		`</c:comp-filter></c:comp-filter></c:filter></c:calendar-query>`
}
