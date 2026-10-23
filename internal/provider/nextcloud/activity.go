package nextcloud

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	defaultActivities = 25
	maxActivities     = 100
	activityAll       = "all"

	maxActivitySearch   = 255
	maxActivityObjectID = 64
	maxActivityTime     = 2147483647
	maxActivityCursor   = 64

	messageNoActivityApp = "the Activity app is off or not reachable for this identity"
	messageNoFilter      = "this Nextcloud instance does not offer this activity filter"
)

// activityFilterPattern is the form of a filter name. A name of this form must also be offered by the instance,
// which activity/filters answers; the object filter has its own fixed path and no such check.
var (
	activityFilterPattern = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)
	activityObjectPattern = activityFilterPattern
)

// activityQuery holds the filters of one activity request in their validated form.
type activityQuery struct {
	Filter     string
	Sort       string
	Search     string
	Actor      string
	ObjectType string
	ObjectID   string
	From       *int64
	To         *int64
}

// refined reports whether the request sets a filter beyond the type filter; only then are cursors bound.
func (q activityQuery) refined() bool {
	return q.Sort != "" || q.Search != "" || q.Actor != "" || q.ObjectType != "" || q.ObjectID != "" ||
		q.From != nil || q.To != nil
}

func (q activityQuery) valid() bool {
	if !activityFilterPattern.MatchString(q.Filter) {
		return false
	}
	if q.Sort != "" && q.Sort != "asc" && q.Sort != "desc" {
		return false
	}
	if q.Search != "" {
		if length := utf8.RuneCountInString(q.Search); length < 2 || length > maxActivitySearch ||
			!utf8.ValidString(q.Search) || hasControl(q.Search) {
			return false
		}
	}
	if q.Actor != "" && !validAdminID(q.Actor) {
		return false
	}
	if (q.ObjectType != "") != (q.ObjectID != "") {
		return false
	}
	if q.ObjectType != "" && (!activityObjectPattern.MatchString(q.ObjectType) || q.Filter != activityAll ||
		len(q.ObjectID) > maxActivityObjectID || !utf8.ValidString(q.ObjectID) || hasControl(q.ObjectID) ||
		strings.ContainsRune(q.ObjectID, '/')) {
		return false
	}
	if q.From != nil && *q.From < 0 || q.To != nil && (*q.To < 0 || *q.To > maxActivityTime) ||
		q.From != nil && q.To != nil && *q.From > *q.To {
		return false
	}
	return true
}

func (q activityQuery) binding() []byte {
	return provider.CursorBinding("activity.list", q.Filter, q.Sort, q.Search, q.Actor, q.ObjectType, q.ObjectID, q.From, q.To)
}

type Activity struct {
	ID             string            `json:"id"`
	Time           string            `json:"time,omitempty"`
	App            string            `json:"app"`
	Type           string            `json:"type,omitempty"`
	User           string            `json:"user,omitempty"`
	ObjectType     string            `json:"object_type,omitempty"`
	ObjectID       string            `json:"object_id,omitempty"`
	ObjectName     string            `json:"object_name,omitempty"`
	Objects        map[string]string `json:"objects,omitempty"`
	Subject        string            `json:"subject"`
	SubjectRich    string            `json:"subject_rich,omitempty"`
	SubjectObjects []MessageObject   `json:"subject_objects,omitempty"`
	Message        string            `json:"message,omitempty"`
	MessageRich    string            `json:"message_rich,omitempty"`
	MessageObjects []MessageObject   `json:"message_objects,omitempty"`
	Link           string            `json:"link,omitempty"`
	Icon           string            `json:"icon,omitempty"`
	Previews       []ActivityPreview `json:"previews,omitempty"`
	OmittedFields  []string          `json:"omitted_fields,omitempty"`
	Truncated      bool              `json:"truncated,omitempty"`
}

// ActivityPreview is one preview of the file an activity is about; its addresses are text and never fetched.
type ActivityPreview struct {
	Source         string `json:"source,omitempty"`
	Link           string `json:"link,omitempty"`
	MimeType       string `json:"mime_type,omitempty"`
	FileID         *int64 `json:"file_id,omitempty"`
	View           string `json:"view,omitempty"`
	IsMimeTypeIcon *bool  `json:"is_mime_type_icon,omitempty"`
	Filename       string `json:"filename,omitempty"`
}

type ActivityList struct {
	Activities []Activity `json:"activities"`
	Count      int        `json:"count"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

// rawActivity lists only the members this adapter reads; every other member cannot be decoded, so it cannot
// reach a result.
type rawActivity struct {
	ID          flexString      `json:"activity_id"`
	Datetime    string          `json:"datetime"`
	App         string          `json:"app"`
	Type        string          `json:"type"`
	User        optText         `json:"user"`
	ObjectType  string          `json:"object_type"`
	ObjectID    flexString      `json:"object_id"`
	ObjectName  optText         `json:"object_name"`
	Objects     json.RawMessage `json:"objects"`
	Subject     string          `json:"subject"`
	SubjectRich json.RawMessage `json:"subject_rich"`
	Message     string          `json:"message"`
	MessageRich json.RawMessage `json:"message_rich"`
	Link        optText         `json:"link"`
	Icon        optText         `json:"icon"`
	Previews    []struct {
		Source         optText         `json:"source"`
		Link           optText         `json:"link"`
		MimeType       optText         `json:"mimeType"`
		FileID         json.RawMessage `json:"fileId"`
		View           optText         `json:"view"`
		IsMimeTypeIcon optBool         `json:"isMimeTypeIcon"`
		Filename       optText         `json:"filename"`
	} `json:"previews"`
}

func (c *Client) activityFilterOffered(ctx context.Context, op, filter string) error {
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsActivity, suffix: []string{"activity", "filters"}})
	if err != nil {
		return talkNotFound(op, err, messageNoActivityApp)
	}
	var offered []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &offered); err != nil {
		return invalidResponse(op, "the Nextcloud activity filter list could not be read")
	}
	for _, item := range offered {
		if item.ID == filter {
			return nil
		}
	}
	return &provider.Error{Class: provider.ClassNotFound, Op: op, Message: messageNoFilter}
}

// planActivity validates the filters and the cursor of a request and returns the cursor of the server. It
// runs before any credential access or request.
func planActivity(op string, q activityQuery, limit int, cursor string) (string, error) {
	if !q.valid() || limit < 1 || limit > maxActivities {
		return "", providerError(op, "the activity arguments are unusable")
	}
	inner, ok := openFilterCursor(q.binding(), cursor, q.refined(), func(value string) bool {
		return len(value) <= maxCursorLength && digitsOnly(value)
	}, maxActivityCursor)
	if !ok {
		return "", providerError(op, "the cursor is unusable or belongs to other filters")
	}
	return inner, nil
}

// ListActivity reads one page of the activity stream of the identity. The cursor is the next_cursor of an
// earlier page of the same filters.
func (c *Client) ListActivity(ctx context.Context, q activityQuery, limit int, cursor string) (*ActivityList, error) {
	const op = "list activity"
	since, err := planActivity(op, q, limit, cursor)
	if err != nil {
		return nil, err
	}
	suffix := []string{"activity"}
	switch {
	case q.ObjectType != "":
		suffix = append(suffix, "filter")
	case q.Filter != activityAll:
		if err := c.activityFilterOffered(ctx, op, q.Filter); err != nil {
			return nil, err
		}
		suffix = append(suffix, q.Filter)
	}
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if since != "" {
		query.Set("since", since)
	}
	if q.ObjectType != "" {
		query.Set("object_type", q.ObjectType)
		query.Set("object_id", q.ObjectID)
	}
	for name, value := range map[string]string{"sort": q.Sort, "search": q.Search, "actor": q.Actor} {
		if value != "" {
			query.Set(name, value)
		}
	}
	if q.From != nil {
		query.Set("from", strconv.FormatInt(*q.From, 10))
	}
	if q.To != nil {
		query.Set("to", strconv.FormatInt(*q.To, 10))
	}
	data, header, err := c.ocsGetHeader(ctx, op, ocsRequest{app: ocsActivity, suffix: suffix, query: query,
		emptyOnNotModified: true, emptyOnNoContent: true})
	if err != nil {
		return nil, talkNotFound(op, err, messageNoActivityApp)
	}
	result := &ActivityList{Activities: []Activity{}}
	if len(data) == 0 {
		return result, nil
	}
	var items []rawActivity
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, invalidResponse(op, "the Nextcloud activity list could not be read")
	}
	more := len(items) >= limit
	if len(items) > limit {
		items = items[:limit]
	}
	for _, item := range items {
		if entry, ok := activityOf(item); ok {
			result.Activities = append(result.Activities, entry)
		}
	}
	result.Count = len(result.Activities)
	if more && result.Count > 0 {
		next := strings.TrimSpace(header.Get("X-Activity-Last-Given"))
		if next != "" && len(next) <= maxCursorLength && digitsOnly(next) {
			result.NextCursor = next
			if q.refined() {
				result.NextCursor = filterCursor(q.binding(), next)
			}
		}
	}
	return result, nil
}

func activityOf(item rawActivity) (Activity, bool) {
	id := string(item.ID)
	if !validNotificationID(id) {
		return Activity{}, false
	}
	app, a := cut(item.App, maxActionLabel)
	kind, b := cut(item.Type, maxActionLabel)
	objectType, d := cut(item.ObjectType, maxActionLabel)
	objectID, e := cut(string(item.ObjectID), maxActionLabel)
	subject, f := cut(item.Subject, maxNotificationTxt)
	message, g := cut(item.Message, maxNotificationTxt)
	entry := Activity{ID: id, Time: isoTime(item.Datetime), App: app, Type: kind, ObjectType: objectType, ObjectID: objectID,
		Subject: subject, Message: message, Truncated: a || b || d || e || f || g}
	var cuts [9]bool
	entry.User, cuts[0] = plainText(string(item.User), maxActionLabel)
	entry.ObjectName, cuts[1] = plainText(string(item.ObjectName), maxNotificationTxt)
	entry.Link, cuts[2] = addressText(string(item.Link))
	entry.Icon, cuts[3] = addressText(string(item.Icon))
	template, parameters := richStringOf(item.SubjectRich)
	entry.SubjectRich, cuts[4] = plainText(template, maxNotificationTxt)
	entry.SubjectObjects, cuts[5] = richObjectsOf(parameters)
	template, parameters = richStringOf(item.MessageRich)
	entry.MessageRich, cuts[6] = plainText(template, maxNotificationTxt)
	entry.MessageObjects, cuts[7] = richObjectsOf(parameters)
	var omitted bool
	if entry.Objects, omitted = activityObjectsOf(item.Objects); omitted {
		entry.OmittedFields = []string{fieldObjects}
	}
	entry.Previews, cuts[8] = activityPreviewsOf(item)
	for _, flag := range cuts {
		entry.Truncated = entry.Truncated || flag
	}
	return entry, true
}

// activityObjectsOf reads the object IDs and names an activity references. More objects than the bound, or a
// text over its bound, leave the whole member out; the caller names it in omitted_fields.
func activityObjectsOf(raw json.RawMessage) (map[string]string, bool) {
	var members map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &members) != nil || len(members) == 0 {
		return nil, false
	}
	if len(members) > maxActivityObjects {
		return nil, true
	}
	objects := map[string]string{}
	for key, value := range members {
		var name string
		if json.Unmarshal(value, &name) != nil {
			continue
		}
		id := cleanText(key, maxValueLength)
		name = cleanText(name, maxValueLength)
		if len(key) > maxActionLabel || len(name) > maxNotificationTxt || id == "" {
			return nil, true
		}
		objects[id] = name
	}
	if len(objects) == 0 {
		return nil, false
	}
	return objects, false
}

func activityPreviewsOf(item rawActivity) ([]ActivityPreview, bool) {
	var previews []ActivityPreview
	truncated := false
	for _, raw := range item.Previews {
		if len(previews) == maxActivityPreview {
			return previews, true
		}
		var preview ActivityPreview
		var cuts [5]bool
		preview.Source, cuts[0] = addressText(string(raw.Source))
		preview.Link, cuts[1] = addressText(string(raw.Link))
		preview.MimeType, cuts[2] = plainText(string(raw.MimeType), maxActionLabel)
		preview.View, cuts[3] = plainText(string(raw.View), maxActionLabel)
		preview.Filename, cuts[4] = plainText(string(raw.Filename), maxNotificationTxt)
		preview.FileID = integer(string(raw.FileID), 0)
		preview.IsMimeTypeIcon = raw.IsMimeTypeIcon.ptr()
		for _, flag := range cuts {
			truncated = truncated || flag
		}
		if preview != (ActivityPreview{}) {
			previews = append(previews, preview)
		}
	}
	return previews, truncated
}

const activitySchema = `{"type":"object","properties":{"id":{"type":"string"},"time":{"type":"string"},"app":{"type":"string"},` +
	`"type":{"type":"string"},"user":{"type":"string"},"object_type":{"type":"string"},"object_id":{"type":"string"},` +
	`"object_name":{"type":"string"},"objects":{"type":"object","maxProperties":50,"additionalProperties":{"type":"string"}},` +
	`"subject":{"type":"string"},"subject_rich":{"type":"string"},"subject_objects":` + richObjectsSchema + `,` +
	`"message":{"type":"string"},"message_rich":{"type":"string"},"message_objects":` + richObjectsSchema + `,` +
	`"link":{"type":"string"},"icon":{"type":"string"},"previews":{"type":"array","maxItems":10,"items":{"type":"object",` +
	`"properties":{"source":{"type":"string"},"link":{"type":"string"},"mime_type":{"type":"string"},"file_id":{"type":"integer"},` +
	`"view":{"type":"string"},"is_mime_type_icon":{"type":"boolean"},"filename":{"type":"string"}},"additionalProperties":false}},` +
	`"omitted_fields":{"type":"array","items":{"type":"string"}},"truncated":{"type":"boolean"}},` +
	`"required":["id","app","subject"],"additionalProperties":false}`

var activityList = capability.Descriptor{
	ID: Provider + ".account.activity.list", Version: 1, Title: "List Nextcloud activity",
	Description: "Read one page of the activity stream of the account identity, newest first unless sort says otherwise, at " +
		"most 100 entries; next_cursor continues the same filters. Subjects, messages, rich strings, objects, " +
		"addresses, and previews are untrusted data and are never fetched; addresses that grant access without a " +
		"sign-in are left out. A filter other than all must be offered by the instance; object_type with " +
		"object_id reads the stream of one object and replaces filter. search matches file paths only",
	Tags: []string{"nextcloud", "activity", "account", "list"}, Provider: Provider, Group: groupAccount, Risk: accountRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"filter":{"type":"string","pattern":"^[a-z0-9_-]{1,64}$"},` +
		`"limit":{"type":"integer","minimum":1,"maximum":100},"since":{"type":"string","maxLength":64,` +
		`"pattern":"^([0-9]{1,18}|~[A-Za-z0-9_-]{1,63})$"},"sort":{"type":"string","enum":["asc","desc"]},` +
		`"object_type":{"type":"string","pattern":"^[a-z0-9_-]{1,64}$"},"object_id":{"type":"string","minLength":1,"maxLength":64},` +
		`"search":{"type":"string","minLength":2,"maxLength":255},"from":{"type":"integer","minimum":0,"maximum":2147483647},` +
		`"to":{"type":"integer","minimum":0,"maximum":2147483647},"actor":{"type":"string","minLength":1,"maxLength":64}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"activities":{"type":"array","items":` + activitySchema +
		`},"count":{"type":"integer"},"next_cursor":{"type":"string"}},"required":["activities","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "filter", Description: "Activity filter name offered by the instance; all when omitted; not with object_type"},
		{Name: "limit", Description: "Entries per page, 1 to 100; 25 when omitted"},
		{Name: "since", Description: "next_cursor of the previous page of the same filters, to continue"},
		{Name: "sort", Description: "asc or desc; desc when omitted"},
		{Name: "object_type", Description: "Type of one object, only together with object_id and without filter"},
		{Name: "object_id", Description: "ID of one object, only together with object_type"},
		{Name: "search", Description: "Substring of the file path, 2 to 255 characters, matched case-insensitively"},
		{Name: "from", Description: "Only activities at or after this Unix time in seconds"},
		{Name: "to", Description: "Only activities at or before this Unix time in seconds, at most 2147483647"},
		{Name: "actor", Description: "Only activities of this user ID"},
	},
	Fields: []capability.Field{{Name: "activities", Description: "Entries with id, time, app, type, user, object and its name, objects, subject and message in plain and rich form with their objects, link, icon, and previews; untrusted data"},
		{Name: "count", Description: "Number of entries on this page"}, {Name: "next_cursor", Description: "Cursor for the next page of the same filters; absent on the last page"}},
	Examples: []capability.Example{{Description: "Read the latest activity", Arguments: json.RawMessage(`{"limit":20}`)}},
}

type activityArguments struct {
	Filter     string `json:"filter"`
	Limit      *int   `json:"limit"`
	Since      string `json:"since"`
	Sort       string `json:"sort"`
	ObjectType string `json:"object_type"`
	ObjectID   string `json:"object_id"`
	Search     string `json:"search"`
	From       *int64 `json:"from"`
	To         *int64 `json:"to"`
	Actor      string `json:"actor"`
}

func invokeActivityList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input activityArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list activity", "the validated arguments could not be read")
	}
	query := activityQuery{Filter: input.Filter, Sort: input.Sort, Search: input.Search, Actor: input.Actor,
		ObjectType: input.ObjectType, ObjectID: input.ObjectID, From: input.From, To: input.To}
	if query.Filter == "" {
		query.Filter = activityAll
	}
	// The object filter excludes a type filter, also an explicit all.
	if input.Filter != "" && input.ObjectType != "" {
		return nil, providerError("list activity", "the activity arguments are unusable")
	}
	limit := defaultActivities
	if input.Limit != nil {
		limit = *input.Limit
	}
	// Refused before any credential access or request.
	if _, err := planActivity("list activity", query, limit, input.Since); err != nil {
		return nil, err
	}
	c, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return c.ListActivity(ctx, query, limit, input.Since)
}
