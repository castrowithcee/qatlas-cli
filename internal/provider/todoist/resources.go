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
	IsArchived  *bool   `json:"is_archived"`
	IsDeleted   bool    `json:"is_deleted"`
	IsInbox     bool    `json:"inbox_project"`
	// WorkspaceID is set only on a project of a Todoist workspace, never on a personal one.
	WorkspaceID json.RawMessage `json:"workspace_id"`
	ViewStyle   string          `json:"view_style"`
	CreatedAt   *string         `json:"created_at"`
	UpdatedAt   *string         `json:"updated_at"`

	CanAssignTasks  *bool   `json:"can_assign_tasks"`
	CanComment      *bool   `json:"can_comment"`
	ChildOrder      *int64  `json:"child_order"`
	OrderKey        *string `json:"order_key"`
	IsCollapsed     *bool   `json:"is_collapsed"`
	Icon            *string `json:"icon"`
	CreatorUID      *string `json:"creator_uid"`
	IsFrozen        *bool   `json:"is_frozen"`
	DefaultOrder    *int64  `json:"default_order"`
	DefaultOrderKey *string `json:"default_order_key"`
	Access          *struct {
		Visibility    *string `json:"visibility"`
		Configuration *struct {
			HideCollaboratorDetails *bool `json:"hide_collaborator_details"`
			DisableDuplication      *bool `json:"disable_duplication"`
		} `json:"configuration"`
	} `json:"access"`
	Role                                *string         `json:"role"`
	FolderID                            json.RawMessage `json:"folder_id"`
	IsInviteOnly                        *bool           `json:"is_invite_only"`
	Status                              *string         `json:"status"`
	IsLinkSharingEnabled                *bool           `json:"is_link_sharing_enabled"`
	CollaboratorRoleDefault             *string         `json:"collaborator_role_default"`
	IsPendingDefaultCollaboratorInvites *bool           `json:"is_pending_default_collaborator_invites"`
	IsProjectInsightsEnabled            *bool           `json:"is_project_insights_enabled"`
}

func (r rawProject) archived() bool { return r.IsArchived != nil && *r.IsArchived }

type rawSection struct {
	ID          string  `json:"id"`
	ProjectID   string  `json:"project_id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Order       int     `json:"section_order"`
	IsArchived  *bool   `json:"is_archived"`
	IsDeleted   bool    `json:"is_deleted"`
	UserID      *string `json:"user_id"`
	AddedAt     *string `json:"added_at"`
	UpdatedAt   *string `json:"updated_at"`
	ArchivedAt  *string `json:"archived_at"`
	IsCollapsed *bool   `json:"is_collapsed"`
	OrderKey    *string `json:"order_key"`
}

func (r rawSection) archived() bool { return r.IsArchived != nil && *r.IsArchived }

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
	Lang        *string `json:"lang"`
}

// rawDuration is the planned duration of a task.
type rawDuration struct {
	Amount int64  `json:"amount"`
	Unit   string `json:"unit"`
}

// rawDeadline is the deadline of a task; Todoist answers its date and the language it was read in.
type rawDeadline struct {
	Date *string `json:"date"`
	Lang *string `json:"lang"`
}

type rawTask struct {
	ID            string       `json:"id"`
	ProjectID     string       `json:"project_id"`
	SectionID     *string      `json:"section_id"`
	ParentID      *string      `json:"parent_id"`
	UserID        *string      `json:"user_id"`
	AddedByUID    *string      `json:"added_by_uid"`
	AssignedByUID *string      `json:"assigned_by_uid"`
	ResponsibleID *string      `json:"responsible_uid"`
	CompletedByID *string      `json:"completed_by_uid"`
	Content       string       `json:"content"`
	Description   string       `json:"description"`
	Labels        []string     `json:"labels"`
	Priority      int          `json:"priority"`
	Due           *rawDue      `json:"due"`
	Deadline      *rawDeadline `json:"deadline"`
	Duration      *rawDuration `json:"duration"`
	IsCollapsed   *bool        `json:"is_collapsed"`
	Checked       *bool        `json:"checked"`
	ChildOrder    *int64       `json:"child_order"`
	DayOrder      *int64       `json:"day_order"`
	OrderKey      *string      `json:"order_key"`
	Completed     *int64       `json:"completed_count"`
	Postponed     *int64       `json:"postponed_count"`
	IsDeleted     bool         `json:"is_deleted"`
	AddedAt       *string      `json:"added_at"`
	UpdatedAt     *string      `json:"updated_at"`
	CompletedAt   *string      `json:"completed_at"`
}

type rawComment struct {
	ID         string         `json:"id"`
	TaskID     string         `json:"task_id"`
	ItemID     string         `json:"item_id"`
	ProjectID  string         `json:"project_id"`
	Content    string         `json:"content"`
	PostedAt   *string        `json:"posted_at"`
	PostedUID  *string        `json:"posted_uid"`
	IsDeleted  bool           `json:"is_deleted"`
	Attachment *rawAttachment `json:"file_attachment"`

	UIDsToNotify []json.RawMessage            `json:"uids_to_notify"`
	Reactions    map[string][]json.RawMessage `json:"reactions"`
}

type rawReminder struct {
	ID           string          `json:"id"`
	TaskID       string          `json:"task_id"`
	ItemID       string          `json:"item_id"`
	Type         string          `json:"type"`
	Due          *rawDue         `json:"due"`
	MinuteOffset *int            `json:"minute_offset"`
	IsUrgent     bool            `json:"is_urgent"`
	IsDeleted    bool            `json:"is_deleted"`
	NotifyUID    json.RawMessage `json:"notify_uid"`
}

type rawFilter struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Query       string  `json:"query"`
	Description string  `json:"description"`
	Color       string  `json:"color"`
	Order       int     `json:"item_order"`
	IsFavorite  bool    `json:"is_favorite"`
	IsFrozen    bool    `json:"is_frozen"`
	IsDeleted   bool    `json:"is_deleted"`
	OrderKey    *string `json:"order_key"`
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

	CanAssignTasks  *bool          `json:"can_assign_tasks,omitempty"`
	CanComment      *bool          `json:"can_comment,omitempty"`
	ChildOrder      *int64         `json:"child_order,omitempty"`
	OrderKey        string         `json:"order_key,omitempty"`
	IsCollapsed     *bool          `json:"is_collapsed,omitempty"`
	Icon            string         `json:"icon,omitempty"`
	CreatorUID      string         `json:"creator_uid,omitempty"`
	IsFrozen        *bool          `json:"is_frozen,omitempty"`
	DefaultOrder    *int64         `json:"default_order,omitempty"`
	DefaultOrderKey string         `json:"default_order_key,omitempty"`
	Access          *ProjectAccess `json:"access,omitempty"`
	Role            string         `json:"role,omitempty"`

	// The members below belong to workspace projects only.
	WorkspaceID                         string `json:"workspace_id,omitempty"`
	FolderID                            string `json:"folder_id,omitempty"`
	IsInviteOnly                        *bool  `json:"is_invite_only,omitempty"`
	Status                              string `json:"status,omitempty"`
	IsLinkSharingEnabled                *bool  `json:"is_link_sharing_enabled,omitempty"`
	CollaboratorRoleDefault             string `json:"collaborator_role_default,omitempty"`
	IsPendingDefaultCollaboratorInvites *bool  `json:"is_pending_default_collaborator_invites,omitempty"`
	IsProjectInsightsEnabled            *bool  `json:"is_project_insights_enabled,omitempty"`
}

// ProjectAccess tells who can see a project. Its settings exist for a public project only.
type ProjectAccess struct {
	Visibility    string               `json:"visibility,omitempty"`
	Configuration *ProjectAccessConfig `json:"configuration,omitempty"`
}

// ProjectAccessConfig holds the settings of a publicly shared project.
type ProjectAccessConfig struct {
	HideCollaboratorDetails *bool `json:"hide_collaborator_details,omitempty"`
	DisableDuplication      *bool `json:"disable_duplication,omitempty"`
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
	UserID      string `json:"user_id,omitempty"`
	AddedAt     string `json:"added_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	ArchivedAt  string `json:"archived_at,omitempty"`
	IsArchived  *bool  `json:"is_archived,omitempty"`
	IsCollapsed *bool  `json:"is_collapsed,omitempty"`
	OrderKey    string `json:"order_key,omitempty"`
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
	Lang        string `json:"lang,omitempty"`
}

// Duration is the planned duration of a task: Amount whole Unit units, minute or day.
type Duration struct {
	Amount int64  `json:"amount"`
	Unit   string `json:"unit"`
}

// Deadline is the deadline of a task as Todoist reads it: the date and the language it was entered in.
type Deadline struct {
	Date string `json:"date"`
	Lang string `json:"lang,omitempty"`
}

// Task is the stable Qatlas view of one task; every list and every change shows the same members. Content,
// description, order key, unit, and language tags are untrusted data. Priority runs from 1 (normal) to 4
// (urgent). A member Todoist leaves out stays out; a counter, an order, or a flag Todoist sent as zero or
// false stays.
type Task struct {
	ID              string    `json:"id"`
	Content         string    `json:"content"`
	ProjectID       string    `json:"project_id"`
	SectionID       string    `json:"section_id,omitempty"`
	ParentID        string    `json:"parent_id,omitempty"`
	Labels          []string  `json:"labels"`
	Priority        int       `json:"priority"`
	Due             *Due      `json:"due,omitempty"`
	Deadline        string    `json:"deadline,omitempty"`
	DeadlineDetails *Deadline `json:"deadline_details,omitempty"`
	Duration        *Duration `json:"duration,omitempty"`
	Description     string    `json:"description,omitempty"`
	UserID          string    `json:"user_id,omitempty"`
	AddedByUID      string    `json:"added_by_uid,omitempty"`
	AssignedByUID   string    `json:"assigned_by_uid,omitempty"`
	ResponsibleUID  string    `json:"responsible_uid,omitempty"`
	CompletedByUID  string    `json:"completed_by_uid,omitempty"`
	IsCollapsed     *bool     `json:"is_collapsed,omitempty"`
	Checked         *bool     `json:"checked,omitempty"`
	ChildOrder      *int64    `json:"child_order,omitempty"`
	DayOrder        *int64    `json:"day_order,omitempty"`
	OrderKey        string    `json:"order_key,omitempty"`
	CompletedCount  *int64    `json:"completed_count,omitempty"`
	PostponedCount  *int64    `json:"postponed_count,omitempty"`
	AddedAt         string    `json:"added_at,omitempty"`
	UpdatedAt       string    `json:"updated_at,omitempty"`
	CompletedAt     string    `json:"completed_at,omitempty"`
}

// TaskList is one page of tasks.
type TaskList struct {
	Tasks []Task `json:"tasks"`
	Continuation
}

// Comment is the stable Qatlas view of one task or project comment. Content, the emoji, and the attachment
// are untrusted data; an attachment is described and its address shown only under shownAddress, never fetched.
type Comment struct {
	ID                    string              `json:"id"`
	Content               string              `json:"content"`
	PostedAt              string              `json:"posted_at,omitempty"`
	PostedBy              string              `json:"posted_by,omitempty"`
	Attachment            *Attachment         `json:"attachment,omitempty"`
	UIDsToNotify          []string            `json:"uids_to_notify,omitempty"`
	UIDsToNotifyTruncated bool                `json:"uids_to_notify_truncated,omitempty"`
	Reactions             map[string][]string `json:"reactions,omitempty"`
	ReactionsTruncated    bool                `json:"reactions_truncated,omitempty"`
}

// Attachment describes the file of a comment. Its addresses appear only when shownAddress allows them.
type Attachment struct {
	FileName     string     `json:"file_name,omitempty"`
	FileType     string     `json:"file_type,omitempty"`
	ResourceType string     `json:"resource_type,omitempty"`
	FileSize     *int64     `json:"file_size,omitempty"`
	UploadState  string     `json:"upload_state,omitempty"`
	FileURL      string     `json:"file_url,omitempty"`
	Image        string     `json:"image,omitempty"`
	ImageWidth   *int64     `json:"image_width,omitempty"`
	ImageHeight  *int64     `json:"image_height,omitempty"`
	FileDuration *int64     `json:"file_duration,omitempty"`
	TnS          *Thumbnail `json:"tn_s,omitempty"`
	TnM          *Thumbnail `json:"tn_m,omitempty"`
	TnL          *Thumbnail `json:"tn_l,omitempty"`
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
	NotifyUID    string `json:"notify_uid,omitempty"`
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
	OrderKey    string `json:"order_key,omitempty"`
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

// idText reads an identifier Todoist sends as a string, or as a number, without a detour through a float.
func idText(raw json.RawMessage) string {
	var id string
	if json.Unmarshal(raw, &id) == nil {
		return boundedText(id, maxTagText)
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		return boundedText(number.String(), maxTagText)
	}
	return ""
}

// projectOf is the one mapping of a project every project output shares, a personal and a workspace one
// alike. Texts Todoist composes itself and identifiers of other people are bounded.
func projectOf(raw rawProject) Project {
	project := Project{ID: raw.ID, Name: raw.Name, ParentID: text(raw.ParentID), Color: raw.Color,
		IsFavorite: raw.IsFavorite, IsShared: raw.IsShared, IsInbox: raw.IsInbox, ViewStyle: raw.ViewStyle,
		Description: raw.Description, IsArchived: raw.archived(),
		CreatedAt: text(raw.CreatedAt), UpdatedAt: text(raw.UpdatedAt),
		CanAssignTasks: raw.CanAssignTasks, CanComment: raw.CanComment, ChildOrder: raw.ChildOrder,
		OrderKey: boundedText(text(raw.OrderKey), maxOrderKey), IsCollapsed: raw.IsCollapsed,
		Icon: boundedText(text(raw.Icon), maxTagText), CreatorUID: boundedText(text(raw.CreatorUID), maxTagText),
		IsFrozen: raw.IsFrozen, DefaultOrder: raw.DefaultOrder,
		DefaultOrderKey: boundedText(text(raw.DefaultOrderKey), maxOrderKey),
		Role:            boundedText(text(raw.Role), maxTagText),
		WorkspaceID:     idText(raw.WorkspaceID), FolderID: idText(raw.FolderID), IsInviteOnly: raw.IsInviteOnly,
		Status: boundedText(text(raw.Status), maxTagText), IsLinkSharingEnabled: raw.IsLinkSharingEnabled,
		CollaboratorRoleDefault:             boundedText(text(raw.CollaboratorRoleDefault), maxTagText),
		IsPendingDefaultCollaboratorInvites: raw.IsPendingDefaultCollaboratorInvites,
		IsProjectInsightsEnabled:            raw.IsProjectInsightsEnabled}
	if access := raw.Access; access != nil {
		out := ProjectAccess{Visibility: boundedText(text(access.Visibility), maxTagText)}
		if config := access.Configuration; config != nil &&
			(config.HideCollaboratorDetails != nil || config.DisableDuplication != nil) {
			out.Configuration = &ProjectAccessConfig{HideCollaboratorDetails: config.HideCollaboratorDetails,
				DisableDuplication: config.DisableDuplication}
		}
		if out.Visibility != "" || out.Configuration != nil {
			project.Access = &out
		}
	}
	return project
}

func sectionOf(raw rawSection) Section {
	section := Section{ID: raw.ID, ProjectID: raw.ProjectID, Name: raw.Name, Order: raw.Order,
		Description: text(raw.Description), UserID: boundedText(text(raw.UserID), maxTagText),
		AddedAt: text(raw.AddedAt), UpdatedAt: text(raw.UpdatedAt), ArchivedAt: text(raw.ArchivedAt),
		IsArchived: raw.IsArchived, IsCollapsed: raw.IsCollapsed,
		OrderKey: boundedText(text(raw.OrderKey), maxOrderKey)}
	return section
}

func dueOf(raw *rawDue) *Due {
	if raw == nil || raw.Date == "" {
		return nil
	}
	return &Due{Date: raw.Date, String: raw.String, IsRecurring: raw.IsRecurring, Timezone: text(raw.Timezone),
		Lang: boundedText(text(raw.Lang), maxTagText)}
}

// taskOf is the one mapping of a task every task output shares. Names of other people are identifiers;
// the texts Todoist composes itself are bounded.
func taskOf(raw rawTask) Task {
	task := Task{ID: raw.ID, Content: raw.Content, ProjectID: raw.ProjectID, SectionID: text(raw.SectionID),
		ParentID: text(raw.ParentID), Labels: raw.Labels, Priority: raw.Priority, Due: dueOf(raw.Due),
		Description: raw.Description, UserID: boundedText(text(raw.UserID), maxTagText),
		AddedByUID:     boundedText(text(raw.AddedByUID), maxTagText),
		AssignedByUID:  boundedText(text(raw.AssignedByUID), maxTagText),
		ResponsibleUID: boundedText(text(raw.ResponsibleID), maxTagText),
		CompletedByUID: boundedText(text(raw.CompletedByID), maxTagText),
		IsCollapsed:    raw.IsCollapsed, Checked: raw.Checked, ChildOrder: raw.ChildOrder, DayOrder: raw.DayOrder,
		OrderKey:       boundedText(text(raw.OrderKey), maxOrderKey),
		CompletedCount: raw.Completed, PostponedCount: raw.Postponed,
		AddedAt: text(raw.AddedAt), UpdatedAt: text(raw.UpdatedAt), CompletedAt: text(raw.CompletedAt)}
	if task.Labels == nil {
		task.Labels = []string{}
	}
	if raw.Deadline != nil {
		task.Deadline = text(raw.Deadline.Date)
		if task.Deadline != "" {
			task.DeadlineDetails = &Deadline{Date: task.Deadline, Lang: boundedText(text(raw.Deadline.Lang), maxTagText)}
		}
	}
	if raw.Duration != nil && (raw.Duration.Amount != 0 || raw.Duration.Unit != "") {
		task.Duration = &Duration{Amount: raw.Duration.Amount, Unit: boundedText(raw.Duration.Unit, maxTagText)}
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
	// project is the project the list is narrowed to, if any; it must belong to the connection.
	project string
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
	if err := c.requireProject(ctx, call.op, call.project); err != nil {
		return nil, Continuation{}, err
	}
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
		ok, err := c.projectAllowed(ctx, call.op, raw.ID)
		if err != nil {
			return nil, err
		}
		if ok {
			result.Projects = append(result.Projects, projectOf(raw))
		}
	}
	return result, nil
}

func (c *Client) getProject(ctx context.Context, id string) (*Project, error) {
	raw, err := c.readProject(ctx, "get project", id)
	if err != nil {
		return nil, err
	}
	project := projectOf(raw)
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
	if raw.IsDeleted {
		return rawProject{}, outsideScope("project")
	}
	ok, err := c.projectAllowed(ctx, op, raw.ID)
	if err != nil {
		return rawProject{}, err
	}
	if !ok {
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
		if raw.IsDeleted || (projectID != "" && raw.ProjectID != projectID) {
			continue
		}
		ok, err := c.sectionAllowed(ctx, call.op, raw)
		if err != nil {
			return nil, err
		}
		if !ok {
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
	if raw.IsDeleted {
		return rawSection{}, outsideScope("section")
	}
	ok, err := c.sectionAllowed(ctx, op, raw)
	if err != nil {
		return rawSection{}, err
	}
	if !ok {
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
func (c *Client) listTasks(ctx context.Context, call listCall, window dueWindow) (*TaskList, error) {
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
		if raw.IsDeleted || !window.admits(raw.Due) {
			continue
		}
		ok, err := c.taskAllowed(ctx, call.op, raw)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		result.Tasks = append(result.Tasks, taskOf(raw))
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
	if raw.IsDeleted {
		return rawTask{}, outsideScope("task")
	}
	ok, err := c.taskAllowed(ctx, op, raw)
	if err != nil {
		return rawTask{}, err
	}
	if !ok {
		return rawTask{}, outsideScope("task")
	}
	return raw, nil
}

func (c *Client) getTask(ctx context.Context, id string) (*Task, error) {
	raw, err := c.readTask(ctx, "get task", id)
	if err != nil {
		return nil, err
	}
	task := taskOf(raw)
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
		comment.Attachment = attachmentOf(raw.Attachment)
	}
	comment.UIDsToNotify, comment.UIDsToNotifyTruncated = notifiedOf(raw.UIDsToNotify)
	comment.Reactions, comment.ReactionsTruncated = reactionsOf(raw.Reactions)
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
		IsUrgent: raw.IsUrgent, NotifyUID: idText(raw.NotifyUID)}
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
			IsFrozen: raw.IsFrozen, OrderKey: boundedText(text(raw.OrderKey), maxOrderKey)})
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
