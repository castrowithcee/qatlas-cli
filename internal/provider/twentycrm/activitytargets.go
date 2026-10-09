package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// A note or a task is linked to a record through a noteTarget or taskTarget row. Both link objects are
// system objects that no records tool reaches; only these tools touch them. The link object, its route, and
// the field that points to a target object come from the workspace catalog (twenty-server's
// note-target.workspace-entity.ts and task-target.workspace-entity.ts: the activity relation plus one
// relation per linkable object), never from an argument. A link is visible to a connection only when the
// activity and the target object are both reachable through it.
const (
	activityGroup = "activitytargets"

	errActivityObject  = "this activity or object cannot be linked through this connection"
	errActivityID      = "activity_id, record_id, and id must be identifiers in UUID form"
	errActivityLookup  = "list needs either activity_id, or object together with record_id"
	errLinkUnavailable = "this link is not available through this connection"

	activityCreateUncertain = "; this link may have been created, and repeating the create can add a duplicate; " +
		"list the links of the record before creating again"
	activityDeleteUncertain = "; this link may have been removed, list the links before repeating the delete"
)

const activitySchema = `{"type":"string","enum":["note","task"]}`

const activityNote = "activity selects the link object: note or task. The note or task and the object must both be " +
	"reachable through the connection; the field that links them follows from the workspace schema. Links whose " +
	"other side is not reachable are counted in omitted and never named"

var activityArguments = []capability.Argument{
	{Name: "activity", Description: "note or task", Required: true},
}

var activityLinkSchema = `{"type":"object","properties":{"id":{"type":"string"},"activity_id":{"type":"string"},` +
	`"object":{"type":"string"},"record_id":{"type":"string"}},"required":["id","activity_id","object","record_id"],` +
	`"additionalProperties":false}`

var activityLinkFields = []capability.Field{
	{Name: "id", Description: "Link identifier, the id to delete"},
	{Name: "activity_id", Description: "Identifier of the note or task"},
	{Name: "object", Description: "Singular API name of the linked object"},
	{Name: "record_id", Description: "Identifier of the linked record"},
}

var activitytargetsList = capability.Descriptor{
	ID:      Provider + ".activitytargets.list",
	Version: 1,
	Title:   "List Twenty CRM note and task links",
	Description: "List one page of the links of one note or task, or of one record, to their counterparts. Give " +
		"activity_id for the links of a note or task, or object with record_id for the notes or tasks linked to a " +
		"record. " + activityNote,
	Tags:     []string{"twentycrm", "crm", "notes", "tasks", "links", "list"},
	Risk:     recordsRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"activity":` + activitySchema + `,"activity_id":` +
		recordIDSchema + `,"object":` + objectNameSchema + `,"record_id":` + recordIDSchema + `,` +
		`"limit":{"type":"integer","minimum":1,"maximum":100},"cursor":{"type":"string","minLength":1,"maxLength":` +
		strconv.Itoa(maxBoundCursorLen) + `,"pattern":"^[A-Za-z0-9_-]+$"}},"required":["activity"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"links":{"type":"array","items":` + activityLinkSchema +
		`},"omitted":{"type":"integer"},"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["links","omitted","has_more"],"additionalProperties":false}`),
	Arguments: append(append([]capability.Argument{}, activityArguments...),
		capability.Argument{Name: "activity_id", Description: "Identifier of the note or task whose links to list"},
		capability.Argument{Name: "object", Description: "Singular API name of the object of a record whose links to list; needs record_id"},
		capability.Argument{Name: "record_id", Description: "Record identifier as a UUID; needs object"},
		capability.Argument{Name: "limit", Description: "Links per page, from 1 through 100; 25 when omitted"},
		capability.Argument{Name: "cursor", Description: "Opaque next_cursor of a previous page of the same list; the first page when omitted"}),
	Fields: []capability.Field{
		{Name: "links", Description: "Links with id, activity_id, object, and record_id"},
		{Name: "omitted", Description: "Number of links on this page whose other side is not reachable, never named"},
		{Name: "next_cursor", Description: "Cursor of the following page, absent on the last page"},
		{Name: "has_more", Description: "True when a following page exists"},
	},
	Examples: []capability.Example{{
		Description: "List the links of a note",
		Arguments:   json.RawMessage(`{"activity":"note","activity_id":"11111111-2222-3333-4444-555555555555"}`),
	}},
}

var activitytargetsCreate = capability.Descriptor{
	ID:      Provider + ".activitytargets.create",
	Version: 1,
	Title:   "Link a Twenty CRM note or task to a record",
	Description: "Link one note or task to one record of one reachable object. " + activityNote +
		". Creating the same link again adds a duplicate",
	Tags:     []string{"twentycrm", "crm", "notes", "tasks", "links", "create"},
	Risk:     recordWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"activity":` + activitySchema + `,"activity_id":` +
		recordIDSchema + `,"object":` + objectNameSchema + `,"record_id":` + recordIDSchema + `},` +
		`"required":["activity","activity_id","object","record_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(activityLinkSchema),
	Arguments: append(append([]capability.Argument{}, activityArguments...),
		capability.Argument{Name: "activity_id", Description: "Identifier of the note or task", Required: true},
		capability.Argument{Name: "object", Description: "Singular API name of the object of the record, as returned by twentycrm.objects.list", Required: true},
		capability.Argument{Name: "record_id", Description: "Identifier of the record to link, as a UUID", Required: true}),
	Fields: activityLinkFields,
	Examples: []capability.Example{{
		Description: "Link a note to a person",
		Arguments: json.RawMessage(`{"activity":"note","activity_id":"11111111-2222-3333-4444-555555555555",` +
			`"object":"person","record_id":"66666666-7777-8888-9999-000000000000"}`),
	}},
}

var activitytargetsDelete = capability.Descriptor{
	ID:      Provider + ".activitytargets.delete",
	Version: 1,
	Title:   "Remove a Twenty CRM note or task link",
	Description: "Remove one link between a note or task and a record; the note, the task, and the record stay. " +
		"The link is read first and removed only when both of its sides are reachable. " + activityNote +
		". A connection offers this tool only when its tools list names it",
	Tags:     []string{"twentycrm", "crm", "notes", "tasks", "links", "delete"},
	Risk:     recordWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"activity":` + activitySchema + `,"id":` + recordIDSchema +
		`},"required":["activity","id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},"required":["deleted"],"additionalProperties":false}`),
	Arguments: append(append([]capability.Argument{}, activityArguments...),
		capability.Argument{Name: "id", Description: "Link identifier as a UUID, as returned by twentycrm.activitytargets.list", Required: true}),
	Examples: []capability.Example{{
		Description: "Remove a link of a task",
		Arguments:   json.RawMessage(`{"activity":"task","id":"11111111-2222-3333-4444-555555555555"}`),
	}},
}

// ActivityTarget is the stable view of one link.
type ActivityTarget struct {
	ID         string `json:"id"`
	ActivityID string `json:"activity_id"`
	Object     string `json:"object"`
	RecordID   string `json:"record_id"`
}

// ActivityTargetList is one page of links; Omitted counts those whose other side is not reachable.
type ActivityTargetList struct {
	Links      []ActivityTarget `json:"links"`
	Omitted    int              `json:"omitted"`
	NextCursor string           `json:"next_cursor,omitempty"`
	HasMore    bool             `json:"has_more"`
}

type activityInput struct {
	Activity   string `json:"activity"`
	ActivityID string `json:"activity_id"`
	Object     string `json:"object"`
	RecordID   string `json:"record_id"`
	ID         string `json:"id"`
	Limit      int    `json:"limit"`
	Cursor     string `json:"cursor"`
}

// newActivityRequest reads the arguments and checks the activity and the object against the connection's
// targets, before any secret is resolved and before any request is sent. No error echoes a value.
func newActivityRequest(resolved *config.Resolved, op string, raw json.RawMessage) (*activityInput, error) {
	var args activityInput
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if args.Activity != "note" && args.Activity != "task" {
		return nil, invalidRequest(errActivityObject)
	}
	if err := selectObject(resolved, args.Activity); err != nil {
		return nil, invalidRequest(errActivityObject)
	}
	if args.Object != "" {
		if err := selectObject(resolved, args.Object); err != nil {
			return nil, invalidRequest(errActivityObject)
		}
	}
	for _, id := range []string{args.ActivityID, args.RecordID, args.ID} {
		if id != "" && !validUUID(id) {
			return nil, invalidRequest(errActivityID)
		}
	}
	return &args, nil
}

func invokeActivityTargetsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list activity links"
	args, err := newActivityRequest(resolved, op, raw)
	if err != nil {
		return nil, err
	}
	byActivity := args.ActivityID != "" && args.Object == "" && args.RecordID == ""
	byRecord := args.ActivityID == "" && args.Object != "" && args.RecordID != ""
	if byActivity == byRecord || args.ID != "" {
		return nil, invalidRequest(errActivityLookup)
	}
	if args.Limit == 0 {
		args.Limit = defaultPageSize
	}
	if args.Limit < 1 || args.Limit > maxPageSize {
		return nil, invalidRequest("limit must be between 1 and " + strconv.Itoa(maxPageSize))
	}
	binding := provider.CursorBinding("activitytargets.list", resolved.Name, args.Activity, args.ActivityID,
		args.Object, args.RecordID)
	after := ""
	if args.Cursor != "" {
		decoded, ok := provider.DecodeCursor(binding, args.Cursor, maxBoundCursorLen)
		if !ok || len(decoded) > maxCursorLength || !safeCursor(decoded) {
			return nil, invalidRequest("cursor is not a next_cursor of this list; start the list again without cursor")
		}
		after = decoded
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListActivityTargets(ctx, args, binding, after)
}

func invokeActivityTargetsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create activity link"
	args, err := newActivityRequest(resolved, op, raw)
	if err != nil {
		return nil, err
	}
	if args.ActivityID == "" || args.Object == "" || args.RecordID == "" || args.ID != "" {
		return nil, invalidRequest(errActivityObject)
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreateActivityTarget(ctx, args)
}

func invokeActivityTargetsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete activity link"
	args, err := newActivityRequest(resolved, op, raw)
	if err != nil {
		return nil, err
	}
	if args.ID == "" || args.ActivityID != "" || args.Object != "" || args.RecordID != "" {
		return nil, invalidRequest(errActivityObject)
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.DeleteActivityTarget(ctx, args); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}

// activityLink is the link object of one activity (noteTarget or taskTarget) read from the catalog, with the
// scope of the connection. The link object is a system object, so it is looked up in the catalog directly.
type activityLink struct {
	*catalogObject
	activity string
	bound    scope
	fields   map[string]*catalogField
}

func (c *Client) activityLink(ctx context.Context, op, activity string) (*activityLink, error) {
	if !c.scope.allows(activity) {
		return nil, invalidRequest(errActivityObject)
	}
	cat, err := c.workspaceCatalog(ctx, op)
	if err != nil {
		return nil, err
	}
	object, linkObject := cat.object(activity)
	link, hasLink := cat.object(activity + "Target")
	if !linkObject || !hasLink || object == nil {
		return nil, invalidRequest(errActivityObject)
	}
	result := &activityLink{catalogObject: link, activity: activity, bound: c.scope,
		fields: make(map[string]*catalogField, len(link.Fields))}
	for i := range link.Fields {
		result.fields[link.Fields[i].Name] = &link.Fields[i]
	}
	// The activity side must be a relation to the activity with a writable identifier.
	own, id := result.fields[activity], result.fields[activity+"Id"]
	if own == nil || own.Relation != activity || id == nil || !id.InResponse || !id.Creatable {
		return nil, invalidRequest(errActivityObject)
	}
	return result, nil
}

// targetField returns the identifier field that links the object: the single relation of the link object
// that points to it. An object with no such relation, or several, cannot be linked.
func (l *activityLink) targetField(object string) (string, error) {
	found := ""
	for _, field := range l.Fields {
		if field.Name == l.activity || !field.Reference || field.Relation != object {
			continue
		}
		id := l.fields[field.Name+"Id"]
		if id == nil || id.Format != "uuid" || !id.InResponse || !id.Creatable || found != "" {
			return "", invalidRequest(errActivityObject)
		}
		found = id.Name
	}
	if found == "" || !l.bound.allows(object) {
		return "", invalidRequest(errActivityObject)
	}
	return found, nil
}

func uuidOrNull(raw json.RawMessage) (id string, present bool, err error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false, nil
	}
	if json.Unmarshal(raw, &id) != nil || !validUUID(id) {
		return "", false, errors.New("unusable identifier")
	}
	return id, true, nil
}

// parse reads one link row. visible is true only when the row has an activity and exactly one target whose
// object is reachable; a row with any other shape is never named.
func (l *activityLink) parse(op string, raw json.RawMessage) (link ActivityTarget, visible bool, err error) {
	bad := func() error { return provider.InvalidResponse(op, "Twenty returned a link beyond the supported shape") }
	var item map[string]json.RawMessage
	if json.Unmarshal(raw, &item) != nil || item == nil {
		return link, false, bad()
	}
	id, present, idErr := uuidOrNull(item["id"])
	if idErr != nil || !present {
		return link, false, bad()
	}
	activityID, hasActivity, err := uuidOrNull(item[l.activity+"Id"])
	if err != nil {
		return link, false, bad()
	}
	link = ActivityTarget{ID: id, ActivityID: activityID}
	targets, reachable := 0, false
	for _, field := range l.Fields {
		if field.Name == l.activity || !field.Reference {
			continue
		}
		recordID, hasRecord, err := uuidOrNull(item[field.Name+"Id"])
		if err != nil {
			return ActivityTarget{}, false, bad()
		}
		if !hasRecord {
			continue
		}
		targets++
		reachable = field.Relation != "" && l.bound.allows(field.Relation)
		link.Object, link.RecordID = field.Relation, recordID
	}
	return link, hasActivity && targets == 1 && reachable, nil
}

// ListActivityTargets reads one page of links with one fixed filter built from validated identifiers.
func (c *Client) ListActivityTargets(ctx context.Context, args *activityInput, binding []byte, after string) (*ActivityTargetList, error) {
	const op = "list activity links"
	link, err := c.activityLink(ctx, op, args.Activity)
	if err != nil {
		return nil, err
	}
	filter := link.activity + "Id[eq]:" + args.ActivityID
	if args.Object != "" {
		field, err := link.targetField(args.Object)
		if err != nil {
			return nil, err
		}
		filter = field + "[eq]:" + args.RecordID
	}
	values := url.Values{}
	values.Set("depth", noRelations)
	values.Set("limit", strconv.Itoa(args.Limit))
	values.Set("filter", filter)
	if after != "" {
		values.Set("starting_after", after)
	}
	var page struct {
		Data     map[string]json.RawMessage `json:"data"`
		PageInfo struct {
			HasNextPage bool    `json:"hasNextPage"`
			EndCursor   *string `json:"endCursor"`
		} `json:"pageInfo"`
	}
	if err := c.get(ctx, op, "/rest/"+url.PathEscape(link.Plural), values, maxResponseBytes, &page); err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if json.Unmarshal(page.Data[link.Plural], &items) != nil || items == nil || len(items) > args.Limit {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable page of links")
	}
	result := &ActivityTargetList{Links: make([]ActivityTarget, 0, len(items)), HasMore: page.PageInfo.HasNextPage}
	for _, item := range items {
		parsed, visible, err := link.parse(op, item)
		if err != nil {
			return nil, err
		}
		if visible {
			result.Links = append(result.Links, parsed)
		} else {
			result.Omitted++
		}
	}
	if page.PageInfo.HasNextPage {
		end := ""
		if page.PageInfo.EndCursor != nil {
			end = *page.PageInfo.EndCursor
		}
		if end == "" || len(end) > maxCursorLength || !safeCursor(end) {
			return nil, provider.InvalidResponse(op, "Twenty returned an unusable cursor")
		}
		result.NextCursor = provider.EncodeCursor(binding, end)
	}
	return result, nil
}

// CreateActivityTarget sends exactly one POST. The body holds the two identifiers and nothing else, and
// Twenty's answer must name the requested link.
func (c *Client) CreateActivityTarget(ctx context.Context, args *activityInput) (*ActivityTarget, error) {
	const op = "create activity link"
	link, err := c.activityLink(ctx, op, args.Activity)
	if err != nil {
		return nil, err
	}
	field, err := link.targetField(args.Object)
	if err != nil {
		return nil, err
	}
	body := map[string]any{link.activity + "Id": args.ActivityID, field: args.RecordID}
	path := "/rest/" + url.PathEscape(link.Plural) + "?depth=" + noRelations
	var response struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := c.changeWith(ctx, op, activityCreateUncertain, http.MethodPost, path, body, &response); err != nil {
		var failure *provider.Error
		if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
			failure.Message = "the workspace role of this API key may not create links between these objects; " +
				"check the object permissions of the role in Twenty"
		}
		return nil, err
	}
	uncertain := func(message string) error { return provider.InvalidResponse(op, message+activityCreateUncertain) }
	item, ok := response.Data["create"+strings.ToUpper(link.Name[:1])+link.Name[1:]]
	if !ok {
		return nil, uncertain("Twenty returned an unusable link")
	}
	created, visible, err := link.parse(op, item)
	if err != nil {
		var failure *provider.Error
		if errors.As(err, &failure) {
			failure.Message += activityCreateUncertain
		}
		return nil, err
	}
	if !visible || !strings.EqualFold(created.ActivityID, args.ActivityID) || created.Object != args.Object ||
		!strings.EqualFold(created.RecordID, args.RecordID) {
		return nil, uncertain("Twenty answered with a different link than the requested one")
	}
	return &created, nil
}

// DeleteActivityTarget reads the link first and removes it only when both of its sides are reachable; then
// it sends exactly one DELETE, without soft_delete, which removes the link and nothing else.
func (c *Client) DeleteActivityTarget(ctx context.Context, args *activityInput) error {
	const op = "delete activity link"
	link, err := c.activityLink(ctx, op, args.Activity)
	if err != nil {
		return err
	}
	path := "/rest/" + url.PathEscape(link.Plural) + "/" + url.PathEscape(args.ID)
	var read struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	values := url.Values{}
	values.Set("depth", noRelations)
	if err := c.get(ctx, op, path, values, maxResponseBytes, &read); err != nil {
		return err
	}
	item, ok := read.Data[link.Name]
	if !ok {
		return provider.InvalidResponse(op, "Twenty returned an unusable link")
	}
	existing, visible, err := link.parse(op, item)
	if err != nil {
		return err
	}
	if !strings.EqualFold(existing.ID, args.ID) {
		return provider.InvalidResponse(op, "Twenty answered with a different link than the requested one")
	}
	if !visible {
		return invalidRequest(errLinkUnavailable)
	}
	var response struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := c.changeWith(ctx, op, activityDeleteUncertain, http.MethodDelete, path, nil, &response); err != nil {
		return err
	}
	var removed struct {
		ID string `json:"id"`
	}
	raw := response.Data["delete"+strings.ToUpper(link.Name[:1])+link.Name[1:]]
	if json.Unmarshal(raw, &removed) != nil || !strings.EqualFold(removed.ID, args.ID) {
		return provider.InvalidResponse(op, "Twenty answered with a different link than the requested one"+activityDeleteUncertain)
	}
	return nil
}
