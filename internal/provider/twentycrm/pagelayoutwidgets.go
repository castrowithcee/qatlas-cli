package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	pageLayoutWidgetsPath = "/rest/metadata/pageLayoutWidgets"

	dashboardWidgetTitleMax = 100
	// dashboardMarkdownMax keeps a note readable on a dashboard and far below the request size limit.
	dashboardMarkdownMax = 20000

	// The grid of Twenty is 12 columns wide and 100 rows high.
	widgetGridColumns = 12
	widgetGridRows    = 100

	widgetTypeRichText = "STANDALONE_RICH_TEXT"
	widgetTypeTable    = "RECORD_TABLE"

	pageLayoutWidgetCreateUncertain = "; this change may have taken effect, read the layout with " +
		"twentycrm.pagelayouts.get before repeating it, because repeating it can create a second widget"
	pageLayoutWidgetUpdateUncertain = "; this change may have taken effect, read the layout with " +
		"twentycrm.pagelayouts.get before repeating it"

	errWidgetPosition = "position needs row from 0 through 99, column from 0 through 11, row_span from 1 through 100, " +
		"and column_span from 1 through 12 as integers, with column + column_span at most 12 and row + row_span at most 100"
	errWidgetTitle    = "title must be 1 through 100 characters without control characters"
	errWidgetMarkdown = "markdown must be 1 through 20000 characters without control characters other than line breaks and tabs"
	errWidgetType     = "type must be STANDALONE_RICH_TEXT or RECORD_TABLE; markdown belongs to STANDALONE_RICH_TEXT " +
		"and view_id to RECORD_TABLE, and each type needs its own"
	errWidgetUpdate    = "give a title, a position, or both; the position is changed as a whole"
	errWidgetTabGrid   = "the tab is not a grid tab, so widgets cannot be placed in it by position"
	errWidgetTabFull   = "this dashboard tab already holds the most widgets that can be read back"
	errWidgetViewTable = "a table widget needs a view of its own: a custom table view of the whole workspace; standard " +
		"views, index views, personal views, and widget views are refused"
)

var widgetStatusTexts = provider.StatusTexts{
	Subject:    "Twenty",
	Auth:       "Twenty rejected the API key",
	Permission: "the workspace role of this API key may not read dashboard layouts; check the role of the API key in Twenty",
	NotFound:   "this Twenty workspace does not hold this dashboard widget",
}

var widgetTabStatusTexts = provider.StatusTexts{
	Subject:    "Twenty",
	Auth:       "Twenty rejected the API key",
	Permission: "the workspace role of this API key may not read dashboard layouts; check the role of the API key in Twenty",
	NotFound:   "this Twenty workspace does not hold this dashboard tab",
}

var pageLayoutWidgetUpdateRisk = capability.Risk{
	Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
	Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: layoutDataSensitivity,
}

const (
	widgetGridPositionSchema = `{"type":"object","properties":{"row":{"type":"integer","minimum":0,"maximum":99},` +
		`"column":{"type":"integer","minimum":0,"maximum":11},"row_span":{"type":"integer","minimum":1,"maximum":100},` +
		`"column_span":{"type":"integer","minimum":1,"maximum":12}},` +
		`"required":["row","column","row_span","column_span"],"additionalProperties":false}`

	widgetResultSchema = `{"type":"object","properties":{"id":{"type":"string"},"tab_id":{"type":"string"},` +
		`"type":{"type":"string"},"title":{"type":"string"},"position":{"type":"object","properties":{` +
		widgetPositionProps + `},"additionalProperties":false}},"required":["id","tab_id","type","title"],` +
		`"additionalProperties":false}`

	widgetWriteNote = "The change is visible to all members of the workspace. Needs the Twenty permission \"Layouts\" " +
		"and a connection without object targets, and is offered only by a connection whose tools list names it. " +
		"Sent once; after an unclear result read the layout before repeating. Titles are untrusted workspace data. " +
		"The position is a place in the 12-column grid of a grid tab"
)

var widgetPositionArgument = capability.Argument{Name: "position", Required: true, Description: "Place in the grid: row " +
	"0 through 99, column 0 through 11, row_span 1 through 100, column_span 1 through 12, all integers, column + " +
	"column_span at most 12 and row + row_span at most 100"}

var pagelayoutwidgetsCreate = capability.Descriptor{
	ID: Provider + ".pagelayoutwidgets.create", Version: 1, Title: "Add a widget to a Twenty CRM dashboard tab",
	Description: "Add one rich text widget (Markdown) or one table widget to a tab of a dashboard layout of the Twenty " +
		"workspace of a connection. Tab and layout are read first; the layout must be a live dashboard layout. A " +
		"table widget shows a custom table view of the whole workspace that is read first too, never an index, " +
		"standard, personal, or widget view, and takes the object of that view. Qatlas builds the widget " +
		"configuration itself. Other widget types are not offered. " + widgetWriteNote,
	Tags:     []string{"twentycrm", "dashboards", "pagelayoutwidgets", "create"},
	Risk:     pageLayoutWriteRisk,
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"tab_id":` + uuidSchema + `,"type":{"type":"string",` +
		`"enum":["STANDALONE_RICH_TEXT","RECORD_TABLE"]},"title":{"type":"string","minLength":1,"maxLength":100},` +
		`"position":` + widgetGridPositionSchema + `,"markdown":{"type":"string","minLength":1,"maxLength":20000},` +
		`"view_id":` + uuidSchema + `},"required":["tab_id","type","title","position"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(widgetResultSchema),
	Arguments: []capability.Argument{
		{Name: "tab_id", Required: true, Description: "UUID of the dashboard tab, as returned by twentycrm.pagelayouts.get"},
		{Name: "type", Required: true, Description: "STANDALONE_RICH_TEXT or RECORD_TABLE"},
		{Name: "title", Required: true, Description: "Widget title, 1 through 100 characters, without control characters"},
		widgetPositionArgument,
		{Name: "markdown", Description: "Only for STANDALONE_RICH_TEXT and required there: the text as Markdown, 1 through " +
			"20000 characters, without control characters other than line breaks and tabs"},
		{Name: "view_id", Description: "Only for RECORD_TABLE and required there: UUID of a custom table view of the " +
			"whole workspace, as returned by twentycrm.views.list; never an index, standard, or personal view"},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "The new widget; use it with twentycrm.pagelayoutwidgets.update"},
		{Name: "tab_id", Description: "The dashboard tab that holds the widget"},
		{Name: "title", Description: "Title of the widget, untrusted data"},
	},
	Examples: []capability.Example{{Description: "Add a note",
		Arguments: json.RawMessage(`{"tab_id":"123e4567-e89b-42d3-a456-426614174000","type":"STANDALONE_RICH_TEXT",` +
			`"title":"Notes","position":{"row":0,"column":0,"row_span":4,"column_span":6},"markdown":"# Goals"}`)}},
}

var pagelayoutwidgetsUpdate = capability.Descriptor{
	ID: Provider + ".pagelayoutwidgets.update", Version: 1, Title: "Update a widget of a Twenty CRM dashboard",
	Description: "Change the title, the position, or both of one widget of a dashboard layout of the Twenty workspace " +
		"of a connection; nothing else changes, and the position is changed as a whole. Widget, tab, and layout are " +
		"read first; the layout must be a live dashboard layout. " + widgetWriteNote,
	Tags:     []string{"twentycrm", "dashboards", "pagelayoutwidgets", "update"},
	Risk:     pageLayoutWidgetUpdateRisk,
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `,"title":{"type":"string",` +
		`"minLength":1,"maxLength":100},"position":` + widgetGridPositionSchema + `},"required":["id"],` +
		`"minProperties":2,"additionalProperties":false}`),
	OutputSchema: json.RawMessage(widgetResultSchema),
	Arguments: []capability.Argument{
		{Name: "id", Required: true, Description: "UUID of the widget, as returned by twentycrm.pagelayouts.get"},
		{Name: "title", Description: "New title, 1 through 100 characters, without control characters"},
		{Name: "position", Description: "New place in the grid, all four members together: row 0 through 99, column " +
			"0 through 11, row_span 1 through 100, column_span 1 through 12, integers; column + column_span at most " +
			"12 and row + row_span at most 100"},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "The widget"},
		{Name: "tab_id", Description: "The dashboard tab that holds the widget"},
		{Name: "title", Description: "Title of the widget, untrusted data"},
	},
	Examples: []capability.Example{{Description: "Rename a widget",
		Arguments: json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174000","title":"Notes"}`)}},
}

// DashboardWidgetResult is the widget as the widget tools report it. It never carries content or configuration.
type DashboardWidgetResult struct {
	ID       string          `json:"id"`
	TabID    string          `json:"tab_id"`
	Type     string          `json:"type"`
	Title    string          `json:"title"`
	Position *WidgetPosition `json:"position,omitempty"`
}

// widgetGridPosition is the whole position of a widget in a grid tab.
type widgetGridPosition struct {
	LayoutMode string `json:"layoutMode"`
	Row        int    `json:"row"`
	Column     int    `json:"column"`
	RowSpan    int    `json:"rowSpan"`
	ColumnSpan int    `json:"columnSpan"`
}

type widgetRichTextConfiguration struct {
	ConfigurationType string `json:"configurationType"`
	Body              struct {
		Markdown string `json:"markdown"`
	} `json:"body"`
}

type widgetTableConfiguration struct {
	ConfigurationType string `json:"configurationType"`
	ViewID            string `json:"viewId"`
}

// pageLayoutWidgetCreate is the whole body of a widget create; Qatlas builds every member.
type pageLayoutWidgetCreate struct {
	PageLayoutTabID  string             `json:"pageLayoutTabId"`
	Title            string             `json:"title"`
	Type             string             `json:"type"`
	ObjectMetadataID string             `json:"objectMetadataId,omitempty"`
	Position         widgetGridPosition `json:"position"`
	Configuration    any                `json:"configuration"`
}

// pageLayoutWidgetPatch is the whole body of a widget update: title and position, nothing else.
type pageLayoutWidgetPatch struct {
	Title    *string             `json:"title,omitempty"`
	Position *widgetGridPosition `json:"position,omitempty"`
}

// pageLayoutWidgetWire is the positive projection of Twenty's widget: creator, application, configuration, and
// overrides are never decoded.
type pageLayoutWidgetWire struct {
	ID               string          `json:"id"`
	PageLayoutTabID  string          `json:"pageLayoutTabId"`
	Title            *string         `json:"title"`
	Type             string          `json:"type"`
	ObjectMetadataID *string         `json:"objectMetadataId"`
	Position         json.RawMessage `json:"position"`
	DeletedAt        *string         `json:"deletedAt"`
}

// wantedWidget is what an answer has to confirm.
type wantedWidget struct {
	id       string
	tabID    string
	typ      string
	title    *string
	objectID string
	position *widgetGridPosition
}

func widgetTextClean(value string, max int, multiline bool) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > max {
		return false
	}
	for _, r := range value {
		if multiline && (r == '\n' || r == '\t') {
			continue
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

// widgetTitle checks a title and returns it the way Twenty stores it: trimmed, with runs of white space
// collapsed, so that the answer can be compared with it.
func widgetTitle(title string) (string, bool) {
	if !widgetTextClean(title, dashboardWidgetTitleMax, false) {
		return "", false
	}
	return strings.Join(strings.Fields(title), " "), true
}

// widgetPositionArgs is the position argument; every member must be present.
type widgetPositionArgs struct {
	Row        *float64 `json:"row"`
	Column     *float64 `json:"column"`
	RowSpan    *float64 `json:"row_span"`
	ColumnSpan *float64 `json:"column_span"`
}

func wholeNumber(value *float64, min, max int) (int, bool) {
	if value == nil || *value < float64(min) || *value > float64(max) || *value != float64(int(*value)) {
		return 0, false
	}
	return int(*value), true
}

// gridPosition checks the grid rules of Twenty: columns 0 through 11 inside 12 columns, rows inside 100.
func gridPosition(args *widgetPositionArgs) (*widgetGridPosition, bool) {
	if args == nil {
		return nil, false
	}
	row, rowOK := wholeNumber(args.Row, 0, widgetGridRows-1)
	column, columnOK := wholeNumber(args.Column, 0, widgetGridColumns-1)
	rowSpan, rowSpanOK := wholeNumber(args.RowSpan, 1, widgetGridRows)
	columnSpan, columnSpanOK := wholeNumber(args.ColumnSpan, 1, widgetGridColumns)
	if !rowOK || !columnOK || !rowSpanOK || !columnSpanOK || column+columnSpan > widgetGridColumns ||
		row+rowSpan > widgetGridRows {
		return nil, false
	}
	return &widgetGridPosition{LayoutMode: "GRID", Row: row, Column: column, RowSpan: rowSpan, ColumnSpan: columnSpan}, true
}

// dashboardTabTarget is a live tab of a live dashboard layout.
type dashboardTabTarget struct {
	layoutMode string
	widgets    []json.RawMessage
}

// notFoundLike turns the not-found outcome of a deeper read into the one the caller named, so that a hidden
// element reads exactly like an unknown one.
func notFoundLike(op string, err error, texts provider.StatusTexts) error {
	var failure *provider.Error
	if errors.As(err, &failure) && failure.Class == provider.ClassNotFound {
		return provider.ClassifyStatus(op, http.StatusNotFound, texts)
	}
	return err
}

// readDashboardTab reads the tab, then its layout, which must be a live dashboard layout. Everything that is
// not a live tab of such a layout answers through texts as an unknown element.
func (c *Client) readDashboardTab(ctx context.Context, op, tabID string, texts provider.StatusTexts) (*dashboardTabTarget, error) {
	if !validUUID(tabID) {
		return nil, invalidRequest("the tab id must be a UUID")
	}
	var body json.RawMessage
	if err := c.getWith(ctx, op, pageLayoutTabsPath+"/"+url.PathEscape(tabID), nil, pageLayoutResponseBytes, &body,
		texts); err != nil {
		return nil, err
	}
	entity, err := unwrapMeta(op, body, "pageLayoutTab")
	if err != nil {
		return nil, err
	}
	var tab pageLayoutTabWire
	if json.Unmarshal(entity, &tab) != nil || !equalUUID(tab.ID, tabID) || !validUUID(tab.PageLayoutID) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different dashboard tab than the requested one")
	}
	if tab.DeletedAt != nil {
		return nil, provider.ClassifyStatus(op, http.StatusNotFound, texts)
	}
	layout, err := c.readDashboardLayout(ctx, op, tab.PageLayoutID)
	if err != nil {
		return nil, notFoundLike(op, err, texts)
	}
	tabs, ok := rawItems(layout.Tabs)
	if !ok || len(tabs) > dashboardTabsMax {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable or oversized dashboard layout")
	}
	for _, item := range tabs {
		var entry struct {
			ID         string          `json:"id"`
			LayoutMode *string         `json:"layoutMode"`
			DeletedAt  *string         `json:"deletedAt"`
			Widgets    json.RawMessage `json:"widgets"`
		}
		if json.Unmarshal(item, &entry) != nil || !equalUUID(entry.ID, tabID) {
			continue
		}
		widgets, ok := rawItems(entry.Widgets)
		if entry.DeletedAt != nil || !ok {
			break
		}
		return &dashboardTabTarget{layoutMode: deref(entry.LayoutMode), widgets: widgets}, nil
	}
	return nil, provider.ClassifyStatus(op, http.StatusNotFound, texts)
}

func (t *dashboardTabTarget) holds(widgetID string) bool {
	for _, item := range t.widgets {
		var entry struct {
			ID        string  `json:"id"`
			DeletedAt *string `json:"deletedAt"`
		}
		if json.Unmarshal(item, &entry) == nil && equalUUID(entry.ID, widgetID) && entry.DeletedAt == nil {
			return true
		}
	}
	return false
}

// readTableView reads the view of a table widget. Only a custom table view of the whole workspace passes; its
// object becomes the object of the widget.
func (c *Client) readTableView(ctx context.Context, op, id string) (string, error) {
	record, err := c.readView(ctx, op, id, viewStatusTexts)
	if err != nil {
		return "", err
	}
	if !validUUID(record.ObjectMetadataID) {
		return "", provider.InvalidResponse(op, "Twenty answered with a different view than the requested one")
	}
	if record.DeletedAt != nil {
		return "", provider.ClassifyStatus(op, http.StatusNotFound, viewStatusTexts)
	}
	if record.Type != "TABLE" || !isTrue(record.IsCustom) || (record.Key != nil && *record.Key != "") {
		return "", invalidRequest(errWidgetViewTable)
	}
	return record.ObjectMetadataID, nil
}

func samePosition(raw json.RawMessage, want *widgetGridPosition) bool {
	var got struct {
		LayoutMode string   `json:"layoutMode"`
		Row        *float64 `json:"row"`
		Column     *float64 `json:"column"`
		RowSpan    *float64 `json:"rowSpan"`
		ColumnSpan *float64 `json:"columnSpan"`
	}
	if json.Unmarshal(raw, &got) != nil || got.LayoutMode != want.LayoutMode {
		return false
	}
	return got.Row != nil && *got.Row == float64(want.Row) && got.Column != nil && *got.Column == float64(want.Column) &&
		got.RowSpan != nil && *got.RowSpan == float64(want.RowSpan) &&
		got.ColumnSpan != nil && *got.ColumnSpan == float64(want.ColumnSpan)
}

func projectWidgetPosition(raw json.RawMessage) *WidgetPosition {
	var position struct {
		LayoutMode string   `json:"layoutMode"`
		Row        *float64 `json:"row"`
		Column     *float64 `json:"column"`
		RowSpan    *float64 `json:"rowSpan"`
		ColumnSpan *float64 `json:"columnSpan"`
		Index      *float64 `json:"index"`
	}
	if isNull(raw) || json.Unmarshal(raw, &position) != nil {
		return nil
	}
	return &WidgetPosition{LayoutMode: enumToken(tabLayoutModes, position.LayoutMode),
		Row: optionalNumber(position.Row), Column: optionalNumber(position.Column),
		RowSpan: optionalNumber(position.RowSpan), ColumnSpan: optionalNumber(position.ColumnSpan),
		Index: optionalNumber(position.Index)}
}

// parseWidgetAnswer checks that the answer names the requested widget in the requested tab, and reports it.
func parseWidgetAnswer(op string, raw json.RawMessage, want wantedWidget) (*DashboardWidgetResult, error) {
	var wire pageLayoutWidgetWire
	if json.Unmarshal(raw, &wire) != nil || !validUUID(wire.ID) || !validUUID(wire.PageLayoutTabID) {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable dashboard widget")
	}
	different := (want.id != "" && !equalUUID(wire.ID, want.id)) || !equalUUID(wire.PageLayoutTabID, want.tabID) ||
		wire.Type != want.typ || wire.DeletedAt != nil || (want.title != nil && deref(wire.Title) != *want.title) ||
		(want.position != nil && !samePosition(wire.Position, want.position))
	if !different && want.objectID != "" {
		different = wire.ObjectMetadataID == nil || !equalUUID(*wire.ObjectMetadataID, want.objectID)
	}
	if different {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different dashboard widget than the requested one")
	}
	return &DashboardWidgetResult{ID: wire.ID, TabID: wire.PageLayoutTabID, Type: enumToken(widgetTypes, wire.Type),
		Title: metaText(wire.Title, metaNameMax), Position: projectWidgetPosition(wire.Position)}, nil
}

func mapLayoutWritePermission(err error) error {
	var failure *provider.Error
	if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
		failure.Message = errPageLayoutWritePermission
	}
	return err
}

// widgetCreateRequest is a checked create: the body plus the view whose object is read first.
type widgetCreateRequest struct {
	body   pageLayoutWidgetCreate
	viewID string
}

// CreatePageLayoutWidget reads the tab, its layout, and for a table the view, then sends exactly one POST.
func (c *Client) CreatePageLayoutWidget(ctx context.Context, request widgetCreateRequest) (*DashboardWidgetResult, error) {
	const op = "add dashboard widget"
	body := request.body
	tab, err := c.readDashboardTab(ctx, op, body.PageLayoutTabID, widgetTabStatusTexts)
	if err != nil {
		return nil, err
	}
	if tab.layoutMode != "GRID" {
		return nil, invalidRequest(errWidgetTabGrid)
	}
	if len(tab.widgets) >= dashboardWidgetsMax {
		return nil, invalidRequest(errWidgetTabFull)
	}
	if request.viewID != "" {
		if body.ObjectMetadataID, err = c.readTableView(ctx, op, request.viewID); err != nil {
			return nil, err
		}
	}
	var answer json.RawMessage
	if err := c.changeWith(ctx, op, pageLayoutWidgetCreateUncertain, http.MethodPost, pageLayoutWidgetsPath, body,
		&answer); err != nil {
		return nil, mapLayoutWritePermission(err)
	}
	widget, err := parseWidgetAnswer(op, answer, wantedWidget{tabID: body.PageLayoutTabID, typ: body.Type,
		title: &body.Title, objectID: body.ObjectMetadataID, position: &body.Position})
	if err != nil {
		return nil, uncertainly(err, pageLayoutWidgetCreateUncertain)
	}
	return widget, nil
}

// UpdatePageLayoutWidget reads the widget, its tab, and the layout, then sends exactly one PATCH.
func (c *Client) UpdatePageLayoutWidget(ctx context.Context, id string, patch pageLayoutWidgetPatch) (*DashboardWidgetResult, error) {
	const op = "update dashboard widget"
	if !validUUID(id) {
		return nil, invalidRequest("the widget id must be a UUID")
	}
	path := pageLayoutWidgetsPath + "/" + url.PathEscape(id)
	var current json.RawMessage
	if err := c.getWith(ctx, op, path, nil, pageLayoutResponseBytes, &current, widgetStatusTexts); err != nil {
		return nil, err
	}
	entity, err := unwrapMeta(op, current, "pageLayoutWidget")
	if err != nil {
		return nil, err
	}
	var wire pageLayoutWidgetWire
	if json.Unmarshal(entity, &wire) != nil || !equalUUID(wire.ID, id) || !validUUID(wire.PageLayoutTabID) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different dashboard widget than the requested one")
	}
	if wire.DeletedAt != nil {
		return nil, provider.ClassifyStatus(op, http.StatusNotFound, widgetStatusTexts)
	}
	tab, err := c.readDashboardTab(ctx, op, wire.PageLayoutTabID, widgetStatusTexts)
	if err != nil {
		return nil, err
	}
	if !tab.holds(id) {
		return nil, provider.ClassifyStatus(op, http.StatusNotFound, widgetStatusTexts)
	}
	if patch.Position != nil && tab.layoutMode != "GRID" {
		return nil, invalidRequest(errWidgetTabGrid)
	}
	var answer json.RawMessage
	if err := c.changeWith(ctx, op, pageLayoutWidgetUpdateUncertain, http.MethodPatch, path, patch, &answer); err != nil {
		return nil, mapLayoutWritePermission(err)
	}
	updated, err := parseWidgetAnswer(op, answer, wantedWidget{id: id, tabID: wire.PageLayoutTabID, typ: wire.Type,
		title: patch.Title, position: patch.Position})
	if err != nil {
		return nil, uncertainly(err, pageLayoutWidgetUpdateUncertain)
	}
	return updated, nil
}

func invokePageLayoutWidgetsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	var args struct {
		TabID    string              `json:"tab_id"`
		Type     string              `json:"type"`
		Title    string              `json:"title"`
		Position *widgetPositionArgs `json:"position"`
		Markdown *string             `json:"markdown"`
		ViewID   *string             `json:"view_id"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("add dashboard widget", "the validated arguments could not be read")
	}
	if !validUUID(args.TabID) {
		return nil, invalidRequest("tab_id must be a UUID")
	}
	title, ok := widgetTitle(args.Title)
	if !ok {
		return nil, invalidRequest(errWidgetTitle)
	}
	position, ok := gridPosition(args.Position)
	if !ok {
		return nil, invalidRequest(errWidgetPosition)
	}
	request := widgetCreateRequest{body: pageLayoutWidgetCreate{PageLayoutTabID: strings.ToLower(args.TabID), Title: title,
		Type: args.Type, Position: *position}}
	switch {
	case args.Type == widgetTypeRichText && args.Markdown != nil && args.ViewID == nil:
		if !widgetTextClean(*args.Markdown, dashboardMarkdownMax, true) {
			return nil, invalidRequest(errWidgetMarkdown)
		}
		configuration := widgetRichTextConfiguration{ConfigurationType: widgetTypeRichText}
		configuration.Body.Markdown = *args.Markdown
		request.body.Configuration = configuration
	case args.Type == widgetTypeTable && args.ViewID != nil && args.Markdown == nil:
		if !validUUID(*args.ViewID) {
			return nil, invalidRequest("view_id must be a UUID")
		}
		request.viewID = strings.ToLower(*args.ViewID)
		request.body.Configuration = widgetTableConfiguration{ConfigurationType: widgetTypeTable, ViewID: request.viewID}
	default:
		return nil, invalidRequest(errWidgetType)
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreatePageLayoutWidget(ctx, request)
}

func invokePageLayoutWidgetsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	var args struct {
		ID       string              `json:"id"`
		Title    *string             `json:"title"`
		Position *widgetPositionArgs `json:"position"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("update dashboard widget", "the validated arguments could not be read")
	}
	if !validUUID(args.ID) {
		return nil, invalidRequest("id must be a UUID")
	}
	if args.Title == nil && args.Position == nil {
		return nil, invalidRequest(errWidgetUpdate)
	}
	var patch pageLayoutWidgetPatch
	if args.Title != nil {
		title, ok := widgetTitle(*args.Title)
		if !ok {
			return nil, invalidRequest(errWidgetTitle)
		}
		patch.Title = &title
	}
	if args.Position != nil {
		position, ok := gridPosition(args.Position)
		if !ok {
			return nil, invalidRequest(errWidgetPosition)
		}
		patch.Position = position
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdatePageLayoutWidget(ctx, strings.ToLower(args.ID), patch)
}
