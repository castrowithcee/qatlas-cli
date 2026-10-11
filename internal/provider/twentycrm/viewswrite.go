package twentycrm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	viewNameMax = 100

	viewCreateUncertain = "; this change may have taken effect, list the views of the object with " +
		"twentycrm.views.list before repeating it, because repeating it can create a second view"
	viewUpdateUncertain = "; this change may have taken effect, read the view with twentycrm.views.get " +
		"before repeating it"

	errViewPermission = "the workspace role of this API key may not change views; check the role in Twenty, " +
		"the right is called Views"
	errViewArguments = "the arguments are not valid for this tool; check name, icon, object, type, and field identifiers"

	viewIconPattern = `^Icon[A-Za-z0-9]{1,60}$`
	viewDefaultIcon = "IconList"
)

var (
	// viewCreatableTypes are the view types a view is created with. Widget types and the personal visibility are
	// absent on purpose.
	viewCreatableTypes = []string{"TABLE", "KANBAN", "CALENDAR", "LIST"}

	viewCalendarLayouts = []string{"DAY", "WEEK", "MONTH"}
	viewIconRegexp      = regexp.MustCompile(viewIconPattern)

	// viewReservedNames and viewForbiddenNameChars are the name rules of Twenty (IsValidMetadataName).
	viewReservedNames = []string{"not", "or", "and", "Int", "Float", "Boolean", "String", "ID"}
)

const viewForbiddenNameChars = `'"\;.=*/`

func viewWriteRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: viewDataSensitivity}
}

const viewWriteNote = "The view is visible to all members of the workspace. Needs the Twenty permission \"Views\" and " +
	"a connection without object targets, and is offered only by a connection whose tools list names it. The " +
	"permission Views also lets a key change personal views of other users, so give it to an API key of its own. " +
	"Sent once; after an unclear result read before repeating. Names are untrusted workspace data"

const (
	viewNameSchema = `{"type":"string","minLength":1,"maxLength":100}`
	viewIconSchema = `{"type":"string","pattern":"` + viewIconPattern + `"}`
)

var viewResult = json.RawMessage(`{"type":"object","properties":{` + viewSummaryProps + `},` + viewSummaryRequired +
	`,"additionalProperties":false}`)

var viewsCreate = capability.Descriptor{
	ID: Provider + ".views.create", Version: 1, Title: "Create a Twenty CRM view",
	Description: "Create one view of the given type for one object of the Twenty workspace of a connection, without " +
		"columns, filters, sorts, or groups. A KANBAN view needs a SELECT field to group by; a CALENDAR view needs a " +
		"DATE or DATE_TIME field and a layout. The object and every field are checked against the data model first, " +
		"which needs the permission \"Data model\" as well. " + viewWriteNote,
	Tags:     []string{"twentycrm", "views", "create"},
	Risk:     viewWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + viewNameSchema + `,"object_id":` +
		uuidSchema + `,"type":` + enumSchema(viewCreatableTypes) + `,"icon":` + viewIconSchema +
		`,"main_group_by_field_id":` + uuidSchema + `,"calendar_layout":` + enumSchema(viewCalendarLayouts) +
		`,"calendar_field_id":` + uuidSchema + `,"calendar_end_field_id":` + uuidSchema +
		`},"required":["name","object_id"],"additionalProperties":false}`),
	OutputSchema: viewResult,
	Arguments: []capability.Argument{
		{Name: "name", Required: true, Description: "View name, at most 100 characters, without ' \" \\ ; . = * /"},
		{Name: "object_id", Required: true, Description: "UUID of the object, as returned by twentycrm.metaobjects.list"},
		{Name: "type", Description: "View type, one of " + strings.Join(viewCreatableTypes, ", ") + "; TABLE when omitted"},
		{Name: "icon", Description: "Icon name such as IconList, matching " + viewIconPattern + "; " + viewDefaultIcon + " when omitted"},
		{Name: "main_group_by_field_id", Description: "UUID of a SELECT field of the object; required for KANBAN, refused otherwise"},
		{Name: "calendar_layout", Description: "One of " + strings.Join(viewCalendarLayouts, ", ") + "; required for CALENDAR, refused otherwise"},
		{Name: "calendar_field_id", Description: "UUID of a DATE or DATE_TIME field of the object; required for CALENDAR, refused otherwise"},
		{Name: "calendar_end_field_id", Description: "UUID of an end field of the same type as calendar_field_id; CALENDAR only"},
	},
	Examples: []capability.Example{{Description: "Create a table view",
		Arguments: json.RawMessage(`{"name":"Open deals","object_id":"123e4567-e89b-42d3-a456-426614174000"}`)}},
}

var viewsUpdate = capability.Descriptor{
	ID: Provider + ".views.update", Version: 1, Title: "Update a Twenty CRM view",
	Description: "Rename one custom view of the Twenty workspace of a connection or change its icon; nothing else of " +
		"the view changes. Standard views, index views, personal views, and widget views are refused, and the last " +
		"two read like an unknown view. " + viewWriteNote,
	Tags:     []string{"twentycrm", "views", "update"},
	Risk:     viewWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `,"name":` + viewNameSchema +
		`,"icon":` + viewIconSchema + `},"required":["id"],"additionalProperties":false}`),
	OutputSchema: viewResult,
	Arguments: []capability.Argument{
		{Name: "id", Required: true, Description: "UUID of the view, as returned by twentycrm.views.list"},
		{Name: "name", Description: "New view name, same rules as on create"},
		{Name: "icon", Description: "New icon name, same rules as on create"},
	},
	Examples: []capability.Example{{Description: "Rename a view",
		Arguments: json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174000","name":"Won deals"}`)}},
}

type viewWriteArgs struct {
	ID                 string  `json:"id"`
	Name               *string `json:"name"`
	Icon               *string `json:"icon"`
	ObjectID           string  `json:"object_id"`
	Type               string  `json:"type"`
	MainGroupByFieldID string  `json:"main_group_by_field_id"`
	CalendarLayout     string  `json:"calendar_layout"`
	CalendarFieldID    string  `json:"calendar_field_id"`
	CalendarEndFieldID string  `json:"calendar_end_field_id"`
}

// viewChange is a locally checked request: the payload is built from validated values only.
type viewChange struct {
	id      string // view to change; empty on create
	payload map[string]any
	kind    string // type of a new view
	object  string // object of a new view
	main    string // grouping field of a new KANBAN view
	dates   []string
}

func readViewArgs(raw json.RawMessage) (*viewWriteArgs, error) {
	var args viewWriteArgs
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&args) != nil {
		return nil, invalidRequest(errViewArguments)
	}
	return &args, nil
}

func validViewName(value *string) bool {
	if value == nil || !cleanMetaText(*value) || strings.TrimSpace(*value) == "" ||
		utf8.RuneCountInString(*value) > viewNameMax || strings.ContainsAny(*value, viewForbiddenNameChars) {
		return false
	}
	return !contains(viewReservedNames, *value)
}

func validViewIcon(value *string) bool { return value != nil && viewIconRegexp.MatchString(*value) }

func optionalFieldID(id string) bool { return id == "" || validUUID(id) }

func newViewCreate(args *viewWriteArgs) (*viewChange, error) {
	kind := args.Type
	if kind == "" {
		kind = "TABLE"
	}
	icon := args.Icon
	if icon == nil {
		defaultIcon := viewDefaultIcon
		icon = &defaultIcon
	}
	if args.ID != "" || !validViewName(args.Name) || !validUUID(args.ObjectID) || !contains(viewCreatableTypes, kind) ||
		!validViewIcon(icon) || !optionalFieldID(args.MainGroupByFieldID) || !optionalFieldID(args.CalendarFieldID) ||
		!optionalFieldID(args.CalendarEndFieldID) {
		return nil, invalidRequest(errViewArguments)
	}
	change := &viewChange{kind: kind, object: args.ObjectID, main: args.MainGroupByFieldID}
	// The visibility is fixed: a personal view of a key without a user would belong to nobody.
	change.payload = map[string]any{"name": *args.Name, "objectMetadataId": args.ObjectID, "type": kind, "icon": *icon,
		"visibility": "WORKSPACE"}
	calendar := args.CalendarLayout != "" || args.CalendarFieldID != "" || args.CalendarEndFieldID != ""
	switch kind {
	case "KANBAN":
		if args.MainGroupByFieldID == "" || calendar {
			return nil, invalidRequest("a KANBAN view needs main_group_by_field_id and takes no calendar argument")
		}
		change.payload["mainGroupByFieldMetadataId"] = args.MainGroupByFieldID
	case "CALENDAR":
		if args.MainGroupByFieldID != "" || args.CalendarFieldID == "" || !contains(viewCalendarLayouts, args.CalendarLayout) {
			return nil, invalidRequest("a CALENDAR view needs calendar_layout and calendar_field_id and takes " +
				"no main_group_by_field_id")
		}
		change.payload["calendarLayout"] = args.CalendarLayout
		change.payload["calendarFieldMetadataId"] = args.CalendarFieldID
		change.dates = []string{args.CalendarFieldID}
		if args.CalendarEndFieldID != "" {
			change.payload["calendarEndFieldMetadataId"] = args.CalendarEndFieldID
			change.dates = append(change.dates, args.CalendarEndFieldID)
		}
	default:
		if args.MainGroupByFieldID != "" || calendar {
			return nil, invalidRequest("only KANBAN and CALENDAR views take grouping or calendar arguments")
		}
	}
	return change, nil
}

func newViewUpdate(args *viewWriteArgs) (*viewChange, error) {
	if !validUUID(args.ID) || args.ObjectID != "" || args.Type != "" || args.MainGroupByFieldID != "" ||
		args.CalendarLayout != "" || args.CalendarFieldID != "" || args.CalendarEndFieldID != "" ||
		(args.Name != nil && !validViewName(args.Name)) || (args.Icon != nil && !validViewIcon(args.Icon)) {
		return nil, invalidRequest(errViewArguments)
	}
	payload := map[string]any{}
	if args.Name != nil {
		payload["name"] = *args.Name
	}
	if args.Icon != nil {
		payload["icon"] = *args.Icon
	}
	if len(payload) == 0 {
		return nil, invalidRequest("nothing to change: pass name or icon")
	}
	return &viewChange{id: args.ID, payload: payload}, nil
}

// sendView sends exactly one writing request and returns the entity of the answer. Every failure whose
// request may have reached Twenty names the uncertainty.
func (c *Client) sendView(ctx context.Context, op, uncertain, method, path string, payload map[string]any) (json.RawMessage, error) {
	var body json.RawMessage
	if err := c.changeWith(ctx, op, uncertain, method, path, payload, &body); err != nil {
		var failure *provider.Error
		if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
			failure.Message = errViewPermission
		}
		return nil, err
	}
	entity, err := unwrapMeta(op, body, "view")
	if err != nil {
		return nil, uncertainly(err, uncertain)
	}
	return entity, nil
}

// projectChangedView accepts the answer only when it names the expected view: the requested identifier on an
// update, a valid new one on a create, and always the expected object, a workspace-wide view of a listed type.
func projectChangedView(op, uncertain string, entity json.RawMessage, id, object, kind string,
	objects map[string]string) (*ViewSummary, error) {
	var record viewRecord
	if json.Unmarshal(entity, &record) != nil {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable view"+uncertain)
	}
	if (id != "" && !equalUUID(record.ID, id)) || (id == "" && !validUUID(record.ID)) ||
		!equalUUID(record.ObjectMetadataID, object) || !record.listed() || (kind != "" && record.Type != kind) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different view than the requested one"+uncertain)
	}
	summary, err := record.summary(op, objects)
	if err != nil {
		return nil, uncertainly(err, uncertain)
	}
	return &summary, nil
}

func fieldOf(object *MetaObject, id string) *MetaField {
	for i := range object.Fields {
		if equalUUID(object.Fields[i].ID, id) {
			return &object.Fields[i]
		}
	}
	return nil
}

// checkViewFields tests the fields of a new view against the data model of its object.
func checkViewFields(object *MetaObject, change *viewChange) error {
	usable := func(field *MetaField) bool {
		return field != nil && field.IsActive != nil && *field.IsActive && !isTrue(field.IsSystem)
	}
	if change.kind == "KANBAN" {
		if field := fieldOf(object, change.main); !usable(field) || field.Type != "SELECT" {
			return invalidRequest("main_group_by_field_id must be an active SELECT field of the object")
		}
	}
	var first string
	for i, id := range change.dates {
		field := fieldOf(object, id)
		if !usable(field) || (field.Type != "DATE" && field.Type != "DATE_TIME") || (i > 0 && field.Type != first) {
			return invalidRequest("calendar_field_id and calendar_end_field_id must be active fields of the object " +
				"with the same type, DATE or DATE_TIME")
		}
		first = field.Type
	}
	return nil
}

// CreateView checks the object and its fields, then sends exactly one POST.
func (c *Client) CreateView(ctx context.Context, change *viewChange) (*ViewSummary, error) {
	const op = "create view"
	object, err := c.GetMetaObject(ctx, change.object)
	if err != nil {
		return nil, err
	}
	if isTrue(object.IsSystem) || (object.IsActive != nil && !*object.IsActive) {
		return nil, invalidRequest("views are created for active, non-system objects only")
	}
	if err := checkViewFields(object, change); err != nil {
		return nil, err
	}
	entity, err := c.sendView(ctx, op, viewCreateUncertain, http.MethodPost, viewsPath, change.payload)
	if err != nil {
		return nil, err
	}
	return projectChangedView(op, viewCreateUncertain, entity, "", change.object, change.kind,
		map[string]string{strings.ToLower(object.ID): object.NameSingular})
}

// UpdateView reads the view once, then sends exactly one PATCH. Only custom views change.
func (c *Client) UpdateView(ctx context.Context, change *viewChange) (*ViewSummary, error) {
	const op = "update view"
	current, err := c.readView(ctx, op, change.id)
	if err != nil {
		return nil, err
	}
	if !isTrue(current.IsCustom) || (current.Key != nil && *current.Key != "") {
		return nil, invalidRequest("only custom views can be changed; standard and index views are refused")
	}
	path := viewsPath + "/" + url.PathEscape(change.id)
	entity, err := c.sendView(ctx, op, viewUpdateUncertain, http.MethodPatch, path, change.payload)
	if err != nil {
		return nil, err
	}
	return projectChangedView(op, viewUpdateUncertain, entity, change.id, current.ObjectMetadataID, "", nil)
}

// invokeViewsWrite gates the connection, checks every argument, and only then opens the client.
func invokeViewsWrite(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, build func(*viewWriteArgs) (*viewChange, error),
	send func(*Client, context.Context, *viewChange) (*ViewSummary, error)) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	args, err := readViewArgs(raw)
	if err != nil {
		return nil, err
	}
	change, err := build(args)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return send(client, ctx, change)
}

func invokeViewsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeViewsWrite(ctx, resolved, secrets, red, raw, newViewCreate, (*Client).CreateView)
}

func invokeViewsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeViewsWrite(ctx, resolved, secrets, red, raw, newViewUpdate, (*Client).UpdateView)
}
