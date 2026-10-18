package twentycrm

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	dashboardsGroup = "dashboards"

	// layoutDataSensitivity classifies dashboard layouts: titles are workspace data.
	layoutDataSensitivity = "twentycrm-layout-data"

	pageLayoutsPath = "/rest/metadata/pageLayouts"

	// pageLayoutDashboard is the only layout type these tools read; Twenty serves every type by identifier.
	pageLayoutDashboard = "DASHBOARD"

	// A layout comes with all its tabs and widgets in one answer.
	pageLayoutResponseBytes = 4 << 20

	dashboardTabsMax    = 50
	dashboardWidgetsMax = 100
	dashboardOriginMax  = 300
)

var (
	widgetTypes = []string{"VIEW", "IFRAME", "FIELD", "FIELDS", "GRAPH", "STANDALONE_RICH_TEXT", "TIMELINE", "TASKS",
		"NOTES", "FILES", "EMAILS", "CALENDAR", "FIELD_RICH_TEXT", "WORKFLOW", "WORKFLOW_VERSION", "WORKFLOW_RUN",
		"FRONT_COMPONENT", "RECORD_TABLE", "EMAIL_THREAD", "CALL_RECORDING_SUMMARY", "CALL_RECORDING_TRANSCRIPT",
		"MESSAGE_CAMPAIGN_BODY", "MESSAGE_CAMPAIGN_DETAILS", "FORM_FIELD", "CHAT_THREADS", "CHAT"}
	chartTypes      = []string{"AGGREGATE_CHART", "PIE_CHART", "BAR_CHART", "LINE_CHART"}
	tabLayoutModes  = []string{"GRID", "VERTICAL_LIST", "CANVAS"}
	pageLayoutTypes = []string{pageLayoutDashboard}
)

// The list and the single read answer an unknown layout and a layout of another type alike.
var pageLayoutStatusTexts = provider.StatusTexts{
	Subject:    "Twenty",
	Auth:       "Twenty rejected the API key",
	Permission: "the workspace role of this API key may not read dashboard layouts; check the role of the API key in Twenty",
	NotFound:   "this Twenty workspace does not hold this dashboard layout",
}

var pageLayoutRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe, Confirmation: capability.ConfirmationNone,
	OpenWorld: true, DataSensitivity: layoutDataSensitivity,
}

const (
	pageLayoutNote = "Only dashboard layouts are read; layouts of other types are neither listed nor readable. " +
		"Titles are untrusted workspace data and may hold personal data. Widgets are reduced to type, title, " +
		"position, object, and chart type; no widget content, filter, or chart data is read"

	pageLayoutSummaryProps = `"id":{"type":"string"},"name":{"type":"string"},"type":{"type":"string"},` +
		`"tab_count":{"type":"number"}`

	widgetPositionProps = `"layout_mode":{"type":"string"},"row":{"type":"number"},"column":{"type":"number"},` +
		`"row_span":{"type":"number"},"column_span":{"type":"number"},"index":{"type":"number"}`

	widgetProps = `"id":{"type":"string"},"title":{"type":"string"},"type":{"type":"string"},` +
		`"object_metadata_id":{"type":"string"},"object":{"type":"string"},` +
		`"position":{"type":"object","properties":{` + widgetPositionProps + `},"additionalProperties":false},` +
		`"chart_type":{"type":"string"},"iframe_origin":{"type":"string"},"is_active":{"type":"boolean"}`

	tabProps = `"id":{"type":"string"},"title":{"type":"string"},"position":{"type":"number"},"icon":{"type":"string"},` +
		`"layout_mode":{"type":"string"},"is_active":{"type":"boolean"},` +
		`"widgets":{"type":"array","items":{"type":"object","properties":{` + widgetProps + `},` +
		`"required":["id","title","type"],"additionalProperties":false}}`

	pageLayoutSchemaProps = `"id":{"type":"string"},"name":{"type":"string"},"type":{"type":"string"},` +
		`"is_first_tab_pinned":{"type":"boolean"},` +
		`"tabs":{"type":"array","items":{"type":"object","properties":{` + tabProps + `},` +
		`"required":["id","title","widgets"],"additionalProperties":false}}`
)

var pageLayoutsList = capability.Descriptor{
	ID:      Provider + ".page-layouts.list",
	Version: 1,
	Title:   "List Twenty CRM dashboard layouts",
	Description: "List one page of the dashboard layouts of the Twenty workspace of a connection without object " +
		"targets: id, name, and tab count, without tabs and widgets. " + pageLayoutNote,
	Tags:     []string{"twentycrm", "dashboards", "pagelayouts", "list"},
	Risk:     pageLayoutRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":` + workflowLimitSchema + `,"cursor":` +
		cursorSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"layouts":{"type":"array","items":{"type":"object",` +
		`"properties":{` + pageLayoutSummaryProps + `},"required":["id","name","type"],"additionalProperties":false}},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},"required":["layouts","has_more"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "limit", Description: "Layouts per page, from 1 through 100; 25 when omitted"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous page; the first page when omitted"},
	},
	Fields: []capability.Field{
		{Name: "layouts", Description: "The dashboard layouts on this page; a page may hold fewer than limit, untrusted data"},
		{Name: "next_cursor", Description: "Cursor of the following page, absent on the last page"},
		{Name: "has_more", Description: "True when the workspace holds a following page"},
	},
	Examples: []capability.Example{{Description: "List the first page of dashboard layouts", Arguments: json.RawMessage(`{}`)}},
}

var pageLayoutsGet = capability.Descriptor{
	ID:      Provider + ".page-layouts.get",
	Version: 1,
	Title:   "Get a Twenty CRM dashboard layout",
	Description: "Read one dashboard layout of the Twenty workspace of a connection without object targets with its " +
		"tabs and widgets. " + pageLayoutNote,
	Tags:     []string{"twentycrm", "dashboards", "pagelayouts", "get"},
	Risk:     pageLayoutRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `},` +
		`"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + pageLayoutSchemaProps + `},` +
		`"required":["id","name","type","tabs"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "id", Description: "UUID of the dashboard layout, as returned by twenty-crm.page-layouts.list", Required: true},
	},
	Fields: []capability.Field{
		{Name: "tabs", Description: "The tabs with their widgets; at most 50 tabs of at most 100 widgets each"},
		{Name: "iframe_origin", Description: "Of an embedded page only scheme, host, and port; never path, query, or credentials"},
		{Name: "type", Description: "Widget type; a type unknown to Qatlas is unknown"},
	},
	Examples: []capability.Example{{
		Description: "Read a dashboard layout",
		Arguments:   json.RawMessage(`{"id":"123e4567-e89b-42d3-a456-426614174000"}`),
	}},
}

// PageLayoutSummary is a dashboard layout without its tabs.
type PageLayoutSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	TabCount int    `json:"tab_count"`
}

// PageLayoutList is one page of dashboard layouts.
type PageLayoutList struct {
	Layouts    []PageLayoutSummary `json:"layouts"`
	NextCursor string              `json:"next_cursor,omitempty"`
	HasMore    bool                `json:"has_more"`
}

// WidgetPosition is where a widget sits in its tab; which members are set depends on the layout mode.
type WidgetPosition struct {
	LayoutMode string   `json:"layout_mode"`
	Row        *float64 `json:"row,omitempty"`
	Column     *float64 `json:"column,omitempty"`
	RowSpan    *float64 `json:"row_span,omitempty"`
	ColumnSpan *float64 `json:"column_span,omitempty"`
	Index      *float64 `json:"index,omitempty"`
}

// DashboardWidget is a widget reduced to what identifies and places it. Its configuration is not passed on.
type DashboardWidget struct {
	ID               string          `json:"id"`
	Title            string          `json:"title"`
	Type             string          `json:"type"`
	ObjectMetadataID string          `json:"object_metadata_id,omitempty"`
	Object           string          `json:"object,omitempty"`
	Position         *WidgetPosition `json:"position,omitempty"`
	ChartType        string          `json:"chart_type,omitempty"`
	IframeOrigin     string          `json:"iframe_origin,omitempty"`
	IsActive         *bool           `json:"is_active,omitempty"`
}

// DashboardTab is one tab of a dashboard layout.
type DashboardTab struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	Position   float64           `json:"position"`
	Icon       string            `json:"icon,omitempty"`
	LayoutMode string            `json:"layout_mode,omitempty"`
	IsActive   *bool             `json:"is_active,omitempty"`
	Widgets    []DashboardWidget `json:"widgets"`
}

// PageLayout is a dashboard layout with tabs and widgets. It carries neither creator nor application.
type PageLayout struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Type             string         `json:"type"`
	IsFirstTabPinned *bool          `json:"is_first_tab_pinned,omitempty"`
	Tabs             []DashboardTab `json:"tabs"`
}

type pageLayoutRecord struct {
	ID               string          `json:"id"`
	Name             *string         `json:"name"`
	Type             string          `json:"type"`
	IsFirstTabPinned *bool           `json:"isFirstTabPinned"`
	DeletedAt        *string         `json:"deletedAt"`
	Tabs             json.RawMessage `json:"tabs"`
}

// readable reports whether the layout is read at all: a live layout of the dashboard type.
func (r pageLayoutRecord) readable() bool {
	return r.DeletedAt == nil && r.Type == pageLayoutDashboard
}

// optionalNumber keeps a number only inside the bounds of number().
func optionalNumber(value *float64) *float64 {
	if value == nil || *value < -1e9 || *value > 1e9 {
		return nil
	}
	return value
}

// iframeOrigin reduces an embedded page address to scheme, host, and port. Path, query, fragment, and
// credentials often carry tokens, and an address that does not parse cleanly is dropped.
func iframeOrigin(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme != "http" && scheme != "https") || parsed.Hostname() == "" {
		return ""
	}
	origin := scheme + "://" + parsed.Host
	clean, cut := viewData(origin, dashboardOriginMax)
	if cut || clean != origin {
		return ""
	}
	return origin
}

func (r pageLayoutRecord) summary(op string) (PageLayoutSummary, error) {
	if !validUUID(r.ID) {
		return PageLayoutSummary{}, provider.InvalidResponse(op, "Twenty returned a dashboard layout without a usable identifier")
	}
	tabs := 0
	if items, ok := rawItems(r.Tabs); ok {
		for _, item := range items {
			var tab struct {
				DeletedAt *string `json:"deletedAt"`
			}
			if json.Unmarshal(item, &tab) == nil && tab.DeletedAt == nil {
				tabs++
			}
		}
	}
	return PageLayoutSummary{ID: r.ID, Name: metaText(r.Name, metaNameMax), Type: enumToken(pageLayoutTypes, r.Type),
		TabCount: tabs}, nil
}

func projectWidget(op string, item json.RawMessage, objects map[string]string) (*DashboardWidget, error) {
	var record struct {
		ID               string          `json:"id"`
		Title            *string         `json:"title"`
		Type             string          `json:"type"`
		ObjectMetadataID *string         `json:"objectMetadataId"`
		Position         json.RawMessage `json:"position"`
		Configuration    json.RawMessage `json:"configuration"`
		IsActive         *bool           `json:"isActive"`
		DeletedAt        *string         `json:"deletedAt"`
	}
	if json.Unmarshal(item, &record) != nil || !validUUID(record.ID) {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable dashboard widget")
	}
	if record.DeletedAt != nil {
		return nil, nil
	}
	objectID, err := optionalUUID(op, record.ObjectMetadataID)
	if err != nil {
		return nil, err
	}
	widget := &DashboardWidget{ID: record.ID, Title: metaText(record.Title, metaNameMax), Type: enumToken(widgetTypes, record.Type),
		ObjectMetadataID: objectID, Object: objects[strings.ToLower(objectID)], IsActive: record.IsActive}

	var position struct {
		LayoutMode string   `json:"layoutMode"`
		Row        *float64 `json:"row"`
		Column     *float64 `json:"column"`
		RowSpan    *float64 `json:"rowSpan"`
		ColumnSpan *float64 `json:"columnSpan"`
		Index      *float64 `json:"index"`
	}
	if !isNull(record.Position) && json.Unmarshal(record.Position, &position) == nil {
		widget.Position = &WidgetPosition{LayoutMode: enumToken(tabLayoutModes, position.LayoutMode),
			Row: optionalNumber(position.Row), Column: optionalNumber(position.Column),
			RowSpan: optionalNumber(position.RowSpan), ColumnSpan: optionalNumber(position.ColumnSpan),
			Index: optionalNumber(position.Index)}
	}

	// The configuration is read only for the two members named here; everything else in it is dropped.
	var configuration struct {
		ConfigurationType string  `json:"configurationType"`
		URL               *string `json:"url"`
	}
	if json.Unmarshal(record.Configuration, &configuration) == nil {
		switch widget.Type {
		case "GRAPH":
			if enumToken(chartTypes, configuration.ConfigurationType) != workflowUnknown {
				widget.ChartType = configuration.ConfigurationType
			}
		case "IFRAME":
			widget.IframeOrigin = iframeOrigin(deref(configuration.URL))
		}
	}
	return widget, nil
}

// project builds the tabs with their widgets. Entries Twenty reports as deleted are skipped.
func (r pageLayoutRecord) project(op string, objects map[string]string) (*PageLayout, error) {
	layout := &PageLayout{ID: r.ID, Name: metaText(r.Name, metaNameMax), Type: enumToken(pageLayoutTypes, r.Type),
		IsFirstTabPinned: r.IsFirstTabPinned, Tabs: []DashboardTab{}}
	tabs, ok := rawItems(r.Tabs)
	if !ok || len(tabs) > dashboardTabsMax {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable or oversized dashboard layout")
	}
	for _, item := range tabs {
		var record struct {
			ID         string          `json:"id"`
			Title      *string         `json:"title"`
			Position   *float64        `json:"position"`
			Icon       *string         `json:"icon"`
			LayoutMode *string         `json:"layoutMode"`
			IsActive   *bool           `json:"isActive"`
			DeletedAt  *string         `json:"deletedAt"`
			Widgets    json.RawMessage `json:"widgets"`
		}
		if json.Unmarshal(item, &record) != nil || !validUUID(record.ID) {
			return nil, provider.InvalidResponse(op, "Twenty returned an unusable dashboard tab")
		}
		if record.DeletedAt != nil {
			continue
		}
		tab := DashboardTab{ID: record.ID, Title: metaText(record.Title, metaNameMax), Position: number(record.Position),
			Icon: metaText(record.Icon, metaNameMax), IsActive: record.IsActive, Widgets: []DashboardWidget{}}
		if record.LayoutMode != nil {
			tab.LayoutMode = enumToken(tabLayoutModes, *record.LayoutMode)
		}
		widgets, ok := rawItems(record.Widgets)
		if !ok || len(widgets) > dashboardWidgetsMax {
			return nil, provider.InvalidResponse(op, "Twenty returned an unusable or oversized dashboard layout")
		}
		for _, raw := range widgets {
			widget, err := projectWidget(op, raw, objects)
			if err != nil {
				return nil, err
			}
			if widget != nil {
				tab.Widgets = append(tab.Widgets, *widget)
			}
		}
		layout.Tabs = append(layout.Tabs, tab)
	}
	return layout, nil
}

// ListPageLayouts reads one page of dashboard layouts.
func (c *Client) ListPageLayouts(ctx context.Context, connection string, limit int, cursor string) (*PageLayoutList, error) {
	const op = "list dashboard layouts"
	binding := provider.CursorBinding("page-layouts.list", connection)
	limit, after, err := workflowPaging(limit, cursor, binding)
	if err != nil {
		return nil, err
	}
	if after != "" && !validUUID(after) {
		return nil, invalidRequest("cursor is not a next_cursor of this request; start again without cursor")
	}
	values := url.Values{"limit": {strconv.Itoa(limit)}, "pageLayoutType": {pageLayoutDashboard}}
	if after != "" {
		values.Set("starting_after", after)
	}
	var page metaPage
	if err := c.getWith(ctx, op, pageLayoutsPath, values, pageLayoutResponseBytes, &page, pageLayoutStatusTexts); err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if json.Unmarshal(page.Data, &items) != nil || items == nil || len(items) > limit {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable page of dashboard layouts")
	}
	result := &PageLayoutList{Layouts: make([]PageLayoutSummary, 0, len(items)), HasMore: page.PageInfo.HasNextPage}
	for _, item := range items {
		var record pageLayoutRecord
		if json.Unmarshal(item, &record) != nil {
			return nil, provider.InvalidResponse(op, "Twenty returned an unusable dashboard layout")
		}
		if !record.readable() {
			continue
		}
		summary, err := record.summary(op)
		if err != nil {
			return nil, err
		}
		result.Layouts = append(result.Layouts, summary)
	}
	if result.NextCursor, err = page.nextCursor(op, binding); err != nil {
		return nil, err
	}
	return result, nil
}

// readDashboardLayout reads one layout record. A layout of another type, and a deleted one, read exactly like
// an unknown one. The write tools use it to check the target before they send anything.
func (c *Client) readDashboardLayout(ctx context.Context, op, id string) (*pageLayoutRecord, error) {
	if !validUUID(id) {
		return nil, invalidRequest("the layout id must be a UUID")
	}
	var body json.RawMessage
	if err := c.getWith(ctx, op, pageLayoutsPath+"/"+url.PathEscape(id), nil, pageLayoutResponseBytes, &body,
		pageLayoutStatusTexts); err != nil {
		return nil, err
	}
	entity, err := unwrapMeta(op, body, "pageLayout")
	if err != nil {
		return nil, err
	}
	var record pageLayoutRecord
	if json.Unmarshal(entity, &record) != nil || !equalUUID(record.ID, id) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different dashboard layout than the requested one")
	}
	if !record.readable() {
		return nil, provider.ClassifyStatus(op, 404, pageLayoutStatusTexts)
	}
	return &record, nil
}

// GetPageLayout reads one dashboard layout with its tabs and widgets.
func (c *Client) GetPageLayout(ctx context.Context, connection, id string) (*PageLayout, error) {
	const op = "get dashboard layout"
	record, err := c.readDashboardLayout(ctx, op, id)
	if err != nil {
		return nil, err
	}
	// Object names are a convenience; a catalog that cannot be read leaves the identifiers.
	objects, _ := c.objectCatalog(ctx, connection)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return record.project(op, objects)
}

func invokePageLayoutsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("list dashboard layouts", "the validated arguments could not be read")
	}
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListPageLayouts(ctx, resolved.Name, args.Limit, args.Cursor)
}

func invokePageLayoutsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var args struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("get dashboard layout", "the validated arguments could not be read")
	}
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	if !validUUID(args.ID) {
		return nil, invalidRequest("the layout id must be a UUID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetPageLayout(ctx, resolved.Name, args.ID)
}
