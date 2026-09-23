package todoist

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The raw shapes below are the parts of the Todoist API v1 answers this provider reads. Everything else a
// resource carries is ignored, so a new provider field never reaches an agent unreviewed.

type rawProject struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	ParentID    *string `json:"parent_id"`
	Color       string  `json:"color"`
	Description string  `json:"description"`
	IsFavorite  bool    `json:"is_favorite"`
	IsShared    bool    `json:"is_shared"`
	IsArchived  bool    `json:"is_archived"`
	IsDeleted   bool    `json:"is_deleted"`
	IsInbox     bool    `json:"inbox_project"`
	// WorkspaceID is set only on a project of a Todoist workspace, never on a personal one.
	WorkspaceID json.RawMessage `json:"workspace_id"`
	ViewStyle   string          `json:"view_style"`
	CreatedAt   *string         `json:"created_at"`
	UpdatedAt   *string         `json:"updated_at"`
}

type rawSection struct {
	ID          string  `json:"id"`
	ProjectID   string  `json:"project_id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Order       int     `json:"section_order"`
	IsArchived  bool    `json:"is_archived"`
	IsDeleted   bool    `json:"is_deleted"`
}

type rawLabel struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Color      string `json:"color"`
	Order      *int   `json:"order"`
	IsFavorite bool   `json:"is_favorite"`
}

type rawDue struct {
	Date        string  `json:"date"`
	String      string  `json:"string"`
	IsRecurring bool    `json:"is_recurring"`
	Timezone    *string `json:"timezone"`
}

type rawTask struct {
	ID          string   `json:"id"`
	ProjectID   string   `json:"project_id"`
	SectionID   *string  `json:"section_id"`
	ParentID    *string  `json:"parent_id"`
	Content     string   `json:"content"`
	Description string   `json:"description"`
	Labels      []string `json:"labels"`
	Priority    int      `json:"priority"`
	Due         *rawDue  `json:"due"`
	Deadline    *struct {
		Date *string `json:"date"`
	} `json:"deadline"`
	NoteCount   int     `json:"note_count"`
	Checked     bool    `json:"checked"`
	IsDeleted   bool    `json:"is_deleted"`
	AddedAt     *string `json:"added_at"`
	UpdatedAt   *string `json:"updated_at"`
	CompletedAt *string `json:"completed_at"`
}

type rawComment struct {
	ID         string  `json:"id"`
	TaskID     string  `json:"task_id"`
	ItemID     string  `json:"item_id"`
	ProjectID  string  `json:"project_id"`
	Content    string  `json:"content"`
	PostedAt   *string `json:"posted_at"`
	PostedUID  *string `json:"posted_uid"`
	IsDeleted  bool    `json:"is_deleted"`
	Attachment *struct {
		FileName     string `json:"file_name"`
		FileType     string `json:"file_type"`
		ResourceType string `json:"resource_type"`
	} `json:"file_attachment"`
}

type rawReminder struct {
	ID           string  `json:"id"`
	TaskID       string  `json:"task_id"`
	ItemID       string  `json:"item_id"`
	Type         string  `json:"type"`
	Due          *rawDue `json:"due"`
	MinuteOffset *int    `json:"minute_offset"`
	IsUrgent     bool    `json:"is_urgent"`
	IsDeleted    bool    `json:"is_deleted"`
}

type rawFilter struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Query       string `json:"query"`
	Description string `json:"description"`
	Color       string `json:"color"`
	Order       int    `json:"item_order"`
	IsFavorite  bool   `json:"is_favorite"`
	IsFrozen    bool   `json:"is_frozen"`
	IsDeleted   bool   `json:"is_deleted"`
}

// Continuation is the cursor contract every list shares: has_more is true whenever Todoist announced a
// further page, whatever this page held after the scope and the filters were applied.
type Continuation struct {
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// Project is the stable Qatlas view of one project. Name and description are untrusted data.
type Project struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ParentID    string `json:"parent_id,omitempty"`
	Color       string `json:"color,omitempty"`
	IsFavorite  bool   `json:"is_favorite"`
	IsShared    bool   `json:"is_shared"`
	IsInbox     bool   `json:"is_inbox"`
	ViewStyle   string `json:"view_style,omitempty"`
	Description string `json:"description,omitempty"`
	IsArchived  bool   `json:"is_archived,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

// ProjectList is one page of projects.
type ProjectList struct {
	Projects []Project `json:"projects"`
	Continuation
}

// Section is the stable Qatlas view of one section.
type Section struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	Name        string `json:"name"`
	Order       int    `json:"order"`
	Description string `json:"description,omitempty"`
}

// SectionList is one page of sections.
type SectionList struct {
	Sections []Section `json:"sections"`
	Continuation
}

// Label is the stable Qatlas view of one personal label.
type Label struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Color      string `json:"color,omitempty"`
	Order      int    `json:"order"`
	IsFavorite bool   `json:"is_favorite"`
}

// LabelList is one page of personal labels.
type LabelList struct {
	Labels []Label `json:"labels"`
	Continuation
}

// Due is the due date of a task or a reminder. Date is YYYY-MM-DD, or a date with time for a timed one.
type Due struct {
	Date        string `json:"date"`
	String      string `json:"string,omitempty"`
	IsRecurring bool   `json:"is_recurring"`
	Timezone    string `json:"timezone,omitempty"`
}

// Task is the stable Qatlas view of one task. A list leaves the description out; content and description
// are untrusted data. Priority runs from 1 (normal) to 4 (urgent).
type Task struct {
	ID           string   `json:"id"`
	Content      string   `json:"content"`
	ProjectID    string   `json:"project_id"`
	SectionID    string   `json:"section_id,omitempty"`
	ParentID     string   `json:"parent_id,omitempty"`
	Labels       []string `json:"labels"`
	Priority     int      `json:"priority"`
	Due          *Due     `json:"due,omitempty"`
	Deadline     string   `json:"deadline,omitempty"`
	CommentCount int      `json:"comment_count"`
	Description  string   `json:"description,omitempty"`
	AddedAt      string   `json:"added_at,omitempty"`
	UpdatedAt    string   `json:"updated_at,omitempty"`
	CompletedAt  string   `json:"completed_at,omitempty"`
}

// TaskList is one page of tasks.
type TaskList struct {
	Tasks []Task `json:"tasks"`
	Continuation
}

// Comment is the stable Qatlas view of one task or project comment. Content and the attachment name are
// untrusted data; an attachment is described, never fetched.
type Comment struct {
	ID         string      `json:"id"`
	Content    string      `json:"content"`
	PostedAt   string      `json:"posted_at,omitempty"`
	PostedBy   string      `json:"posted_by,omitempty"`
	Attachment *Attachment `json:"attachment,omitempty"`
}

// Attachment describes the file of a comment without its address.
type Attachment struct {
	FileName     string `json:"file_name,omitempty"`
	FileType     string `json:"file_type,omitempty"`
	ResourceType string `json:"resource_type,omitempty"`
}

// CommentList is one page of comments of one task or one project.
type CommentList struct {
	Comments []Comment `json:"comments"`
	Continuation
}

// Reminder is the stable Qatlas view of one time-based reminder.
type Reminder struct {
	ID           string `json:"id"`
	TaskID       string `json:"task_id"`
	Type         string `json:"type"`
	Due          *Due   `json:"due,omitempty"`
	MinuteOffset *int   `json:"minute_offset,omitempty"`
	IsUrgent     bool   `json:"is_urgent"`
}

// ReminderList is one page of reminders.
type ReminderList struct {
	Reminders []Reminder `json:"reminders"`
	Continuation
}

// Filter is the stable Qatlas view of one saved filter. Its query is untrusted data and is never run on its
// own: a caller passes it to the task filter tool deliberately.
type Filter struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Query       string `json:"query"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color,omitempty"`
	Order       int    `json:"order"`
	IsFavorite  bool   `json:"is_favorite"`
	IsFrozen    bool   `json:"is_frozen"`
}

// FilterList is every saved filter of the account. The Sync endpoint answers them at once.
type FilterList struct {
	Filters []Filter `json:"filters"`
}

func text(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func projectOf(raw rawProject, full bool) Project {
	project := Project{ID: raw.ID, Name: raw.Name, ParentID: text(raw.ParentID), Color: raw.Color,
		IsFavorite: raw.IsFavorite, IsShared: raw.IsShared, IsInbox: raw.IsInbox, ViewStyle: raw.ViewStyle}
	if full {
		project.Description, project.IsArchived = raw.Description, raw.IsArchived
		project.CreatedAt, project.UpdatedAt = text(raw.CreatedAt), text(raw.UpdatedAt)
	}
	return project
}

func sectionOf(raw rawSection) Section {
	return Section{ID: raw.ID, ProjectID: raw.ProjectID, Name: raw.Name, Order: raw.Order,
		Description: text(raw.Description)}
}

func dueOf(raw *rawDue) *Due {
	if raw == nil || raw.Date == "" {
		return nil
	}
	return &Due{Date: raw.Date, String: raw.String, IsRecurring: raw.IsRecurring, Timezone: text(raw.Timezone)}
}

func taskOf(raw rawTask, full bool) Task {
	task := Task{ID: raw.ID, Content: raw.Content, ProjectID: raw.ProjectID, SectionID: text(raw.SectionID),
		ParentID: text(raw.ParentID), Labels: raw.Labels, Priority: raw.Priority, Due: dueOf(raw.Due),
		CommentCount: raw.NoteCount, CompletedAt: text(raw.CompletedAt)}
	if task.Labels == nil {
		task.Labels = []string{}
	}
	if raw.Deadline != nil {
		task.Deadline = text(raw.Deadline.Date)
	}
	if full {
		task.Description, task.AddedAt, task.UpdatedAt = raw.Description, text(raw.AddedAt), text(raw.UpdatedAt)
	}
	return task
}

// decodeEach decodes every entry of one page and refuses an entry without an identifier, so a malformed
// answer never becomes a partial result.
func decodeEach[T any](op string, entries []json.RawMessage, id func(T) string) ([]T, error) {
	values := make([]T, 0, len(entries))
	for _, entry := range entries {
		var value T
		if err := json.Unmarshal(entry, &value); err != nil || id(value) == "" {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "Todoist returned an entry without a usable identifier"}
		}
		values = append(values, value)
	}
	return values, nil
}

// listCall is one normalised page request of a paginated list: the Todoist route and filters, the cursor
// binding, the batch size, and the Todoist cursor it continues with.
type listCall struct {
	op      string
	path    string
	query   url.Values
	binding []byte
	limit   int
	after   string
}

// newListCall checks the batch size and the cursor of one request before any credential is resolved. The
// binding covers the tool, the connection scope, and every normalised filter.
func newListCall(op, path string, query url.Values, limit int, cursor string, parts ...any) (listCall, error) {
	size, err := normalizeLimit(limit)
	if err != nil {
		return listCall{}, err
	}
	binding := fingerprint(parts...)
	size, after, err := decodeCursor(binding, cursor, size)
	if err != nil {
		return listCall{}, err
	}
	return listCall{op: op, path: path, query: query, binding: binding, limit: size, after: after}, nil
}

// run reads exactly the one page the call names and returns its entries and the continuation.
func (c *Client) run(ctx context.Context, call listCall) ([]json.RawMessage, Continuation, error) {
	entries, next, err := c.page(ctx, call.op, call.path, call.query, call.limit, call.after)
	if err != nil {
		return nil, Continuation{}, err
	}
	if next == "" {
		return entries, Continuation{}, nil
	}
	return entries, Continuation{NextCursor: encodeCursor(call.binding, call.limit, next), HasMore: true}, nil
}

func (c *Client) listProjects(ctx context.Context, call listCall) (*ProjectList, error) {
	entries, more, err := c.run(ctx, call)
	if err != nil {
		return nil, err
	}
	raws, err := decodeEach(call.op, entries, func(p rawProject) string { return p.ID })
	if err != nil {
		return nil, err
	}
	result := &ProjectList{Projects: []Project{}, Continuation: more}
	for _, raw := range raws {
		if c.scope.allows(raw.ID) {
			result.Projects = append(result.Projects, projectOf(raw, false))
		}
	}
	return result, nil
}

func (c *Client) getProject(ctx context.Context, id string) (*Project, error) {
	raw, err := c.readProject(ctx, "get project", id)
	if err != nil {
		return nil, err
	}
	project := projectOf(raw, true)
	return &project, nil
}

// readProject reads one project and refuses it when it lies outside the connection.
func (c *Client) readProject(ctx context.Context, op, id string) (rawProject, error) {
	var raw rawProject
	if err := c.get(ctx, op, "/projects/"+url.PathEscape(id), nil, &raw); err != nil {
		return rawProject{}, err
	}
	if raw.ID == "" {
		return rawProject{}, invalidResponse(op)
	}
	if raw.IsDeleted || !c.scope.allows(raw.ID) {
		return rawProject{}, outsideScope("project")
	}
	return raw, nil
}

// listSections reads one page of sections and keeps those of the connection's projects and, when one is
// named, of that project.
func (c *Client) listSections(ctx context.Context, call listCall, projectID string) (*SectionList, error) {
	entries, more, err := c.run(ctx, call)
	if err != nil {
		return nil, err
	}
	raws, err := decodeEach(call.op, entries, func(s rawSection) string { return s.ID })
	if err != nil {
		return nil, err
	}
	result := &SectionList{Sections: []Section{}, Continuation: more}
	for _, raw := range raws {
		if raw.IsDeleted || !c.scope.allows(raw.ProjectID) || (projectID != "" && raw.ProjectID != projectID) {
			continue
		}
		result.Sections = append(result.Sections, sectionOf(raw))
	}
	return result, nil
}

// readSection reads one section and refuses it when it belongs to a project outside the connection.
func (c *Client) readSection(ctx context.Context, op, id string) (rawSection, error) {
	var raw rawSection
	if err := c.get(ctx, op, "/sections/"+url.PathEscape(id), nil, &raw); err != nil {
		return rawSection{}, err
	}
	if raw.ID == "" {
		return rawSection{}, invalidResponse(op)
	}
	if raw.IsDeleted || !c.scope.allows(raw.ProjectID) {
		return rawSection{}, outsideScope("section")
	}
	return raw, nil
}

func (c *Client) getSection(ctx context.Context, id string) (*Section, error) {
	raw, err := c.readSection(ctx, "get section", id)
	if err != nil {
		return nil, err
	}
	section := sectionOf(raw)
	return &section, nil
}

func (c *Client) listLabels(ctx context.Context, call listCall) (*LabelList, error) {
	entries, more, err := c.run(ctx, call)
	if err != nil {
		return nil, err
	}
	raws, err := decodeEach(call.op, entries, func(l rawLabel) string { return l.ID })
	if err != nil {
		return nil, err
	}
	result := &LabelList{Labels: make([]Label, 0, len(raws)), Continuation: more}
	for _, raw := range raws {
		result.Labels = append(result.Labels, labelOf(raw))
	}
	return result, nil
}

func labelOf(raw rawLabel) Label {
	label := Label{ID: raw.ID, Name: raw.Name, Color: raw.Color, IsFavorite: raw.IsFavorite}
	if raw.Order != nil {
		label.Order = *raw.Order
	}
	return label
}

// dueWindow is the structured due filter of a task list. Bounds are inclusive YYYY-MM-DD dates compared
// with the date part of a due date; without keeps only tasks that have none.
type dueWindow struct {
	from, to string
	without  bool
}

func (w dueWindow) admits(due *rawDue) bool {
	if w.without {
		return due == nil || due.Date == ""
	}
	if w.from == "" && w.to == "" {
		return true
	}
	if due == nil || len(due.Date) < len("2006-01-02") {
		return false
	}
	date := due.Date[:len("2006-01-02")]
	return (w.from == "" || date >= w.from) && (w.to == "" || date <= w.to)
}

// listTasks reads one page of tasks and keeps those of the connection's projects that match the due
// window. A short or empty page with has_more is how a filtered page looks; it never means the end.
func (c *Client) listTasks(ctx context.Context, call listCall, window dueWindow, full bool) (*TaskList, error) {
	entries, more, err := c.run(ctx, call)
	if err != nil {
		return nil, err
	}
	raws, err := decodeEach(call.op, entries, func(t rawTask) string { return t.ID })
	if err != nil {
		return nil, err
	}
	result := &TaskList{Tasks: []Task{}, Continuation: more}
	for _, raw := range raws {
		if raw.IsDeleted || !c.scope.allows(raw.ProjectID) || !window.admits(raw.Due) {
			continue
		}
		result.Tasks = append(result.Tasks, taskOf(raw, full))
	}
	return result, nil
}

// readTask reads one task and refuses it when it belongs to a project outside the connection.
func (c *Client) readTask(ctx context.Context, op, id string) (rawTask, error) {
	var raw rawTask
	if err := c.get(ctx, op, "/tasks/"+url.PathEscape(id), nil, &raw); err != nil {
		return rawTask{}, err
	}
	if raw.ID == "" {
		return rawTask{}, invalidResponse(op)
	}
	if raw.IsDeleted || !c.scope.allows(raw.ProjectID) {
		return rawTask{}, outsideScope("task")
	}
	return raw, nil
}

func (c *Client) getTask(ctx context.Context, id string) (*Task, error) {
	raw, err := c.readTask(ctx, "get task", id)
	if err != nil {
		return nil, err
	}
	task := taskOf(raw, true)
	return &task, nil
}

// listComments reads one page of comments of one task or one project. A task of a project connection is
// read first, so the comments of a task outside it are never requested.
func (c *Client) listComments(ctx context.Context, call listCall, taskID, projectID string) (*CommentList, error) {
	if taskID != "" && !c.scope.all {
		if _, err := c.readTask(ctx, call.op, taskID); err != nil {
			return nil, err
		}
	}
	entries, more, err := c.run(ctx, call)
	if err != nil {
		return nil, err
	}
	raws, err := decodeEach(call.op, entries, func(n rawComment) string { return n.ID })
	if err != nil {
		return nil, err
	}
	result := &CommentList{Comments: []Comment{}, Continuation: more}
	for _, raw := range raws {
		parentTask := raw.TaskID
		if parentTask == "" {
			parentTask = raw.ItemID
		}
		// A comment that names a parent other than the one requested is never passed on; a project list
		// holds project comments only, never those of a task.
		if raw.IsDeleted || (taskID != "" && parentTask != "" && parentTask != taskID) ||
			(projectID != "" && (parentTask != "" || (raw.ProjectID != "" && raw.ProjectID != projectID))) {
			continue
		}
		result.Comments = append(result.Comments, commentOf(raw))
	}
	return result, nil
}

func commentOf(raw rawComment) Comment {
	comment := Comment{ID: raw.ID, Content: raw.Content, PostedAt: text(raw.PostedAt), PostedBy: text(raw.PostedUID)}
	if raw.Attachment != nil {
		comment.Attachment = &Attachment{FileName: raw.Attachment.FileName, FileType: raw.Attachment.FileType,
			ResourceType: raw.Attachment.ResourceType}
	}
	return comment
}

// listReminders reads one page of reminders. A project connection always names a task, which is read
// first, so the reminders of a task outside it are never requested.
func (c *Client) listReminders(ctx context.Context, call listCall, taskID string) (*ReminderList, error) {
	if !c.scope.all {
		if taskID == "" {
			return nil, invalidRequest(reminderTaskRequired)
		}
		if _, err := c.readTask(ctx, call.op, taskID); err != nil {
			return nil, err
		}
	}
	entries, more, err := c.run(ctx, call)
	if err != nil {
		return nil, err
	}
	raws, err := decodeEach(call.op, entries, func(r rawReminder) string { return r.ID })
	if err != nil {
		return nil, err
	}
	result := &ReminderList{Reminders: []Reminder{}, Continuation: more}
	for _, raw := range raws {
		parent := raw.TaskID
		if parent == "" {
			parent = raw.ItemID
		}
		// A reminder that names another task is never passed on. One that names none belongs to the task the
		// request was narrowed to, which was checked against the scope before.
		if raw.IsDeleted || (taskID != "" && parent != "" && parent != taskID) {
			continue
		}
		if parent == "" {
			parent = taskID
		}
		result.Reminders = append(result.Reminders, reminderOf(raw, parent))
	}
	return result, nil
}

func reminderOf(raw rawReminder, taskID string) Reminder {
	return Reminder{ID: raw.ID, TaskID: taskID, Type: raw.Type, Due: dueOf(raw.Due), MinuteOffset: raw.MinuteOffset,
		IsUrgent: raw.IsUrgent}
}

// listFilters reads the saved filters of the account through one read-only Sync request, the only route
// API v1 offers for them. It carries no command and asks for filters only.
func (c *Client) listFilters(ctx context.Context) (*FilterList, error) {
	const op = "list filters"
	if !c.scope.all {
		return nil, invalidRequest("saved filters belong to the whole account; only an account connection " +
			"reads them")
	}
	var answer struct {
		Filters []rawFilter `json:"filters"`
	}
	if err := c.syncRead(ctx, op, []string{"filters"}, &answer); err != nil {
		return nil, err
	}
	result := &FilterList{Filters: []Filter{}}
	for _, raw := range answer.Filters {
		if raw.ID == "" {
			return nil, invalidResponse(op)
		}
		if raw.IsDeleted {
			continue
		}
		result.Filters = append(result.Filters, Filter{ID: raw.ID, Name: raw.Name, Query: raw.Query,
			Description: raw.Description, Color: raw.Color, Order: raw.Order, IsFavorite: raw.IsFavorite,
			IsFrozen: raw.IsFrozen})
	}
	sort.SliceStable(result.Filters, func(i, j int) bool { return result.Filters[i].Order < result.Filters[j].Order })
	return result, nil
}

// parseInstant reads an RFC 3339 time argument and returns it in the UTC form Todoist expects.
func parseInstant(name, value string) (string, time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return "", time.Time{}, invalidRequest(name + " must be an RFC 3339 time such as 2026-09-01T00:00:00Z")
	}
	parsed = parsed.UTC()
	return parsed.Format("2006-01-02T15:04:05Z"), parsed, nil
}

// validDate reads a YYYY-MM-DD date strictly.
func validDate(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}
