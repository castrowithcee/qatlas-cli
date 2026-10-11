package twentycrm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	pageLayoutTabsPath = "/rest/metadata/pageLayoutTabs"

	dashboardTabTitleMax = 100

	pageLayoutTabUncertain = "; this change may have taken effect, read the layout with twentycrm.pagelayouts.get " +
		"before repeating it, because repeating it can create a second tab"

	errPageLayoutWritePermission = "the workspace role of this API key may not change dashboard layouts; check the " +
		"role in Twenty, the right is called Layouts"
	errPageLayoutTabArguments = "title must be 1 through 100 characters without control characters, and position " +
		"a number from 0 through 1000000"
	errPageLayoutTabFull = "this dashboard layout already holds the most tabs that can be read back"
)

var pageLayoutWriteRisk = capability.Risk{
	Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
	Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: layoutDataSensitivity,
}

var pagelayouttabsCreate = capability.Descriptor{
	ID: Provider + ".pagelayouttabs.create", Version: 1, Title: "Add a tab to a Twenty CRM dashboard",
	Description: "Add one empty tab in grid mode to a dashboard layout of the Twenty workspace of a connection. The " +
		"layout is read first and must be a live dashboard layout. The tab is visible to all members of the " +
		"workspace. Needs the Twenty permission \"Layouts\" and a connection without object targets, and is " +
		"offered only by a connection whose tools list names it. Sent once; after an unclear result read the " +
		"layout before repeating. Titles are untrusted workspace data",
	Tags:     []string{"twentycrm", "dashboards", "pagelayouttabs", "create"},
	Risk:     pageLayoutWriteRisk,
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"layout_id":` + uuidSchema + `,"title":` +
		`{"type":"string","minLength":1,"maxLength":100},"position":` + viewColumnPositionSchema +
		`},"required":["layout_id","title","position"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"layout_id":{"type":"string"},` +
		`"title":{"type":"string"},"position":{"type":"number"}},"required":["id","layout_id","title","position"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "layout_id", Required: true, Description: "UUID of the dashboard layout, as returned by twentycrm.pagelayouts.list"},
		{Name: "title", Required: true, Description: "Tab title, 1 through 100 characters, without control characters"},
		{Name: "position", Required: true, Description: "Position among the tabs, a number from 0 through 1000000"},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "The new tab"},
		{Name: "layout_id", Description: "The dashboard layout that holds the tab"},
		{Name: "title", Description: "Title of the tab, untrusted data"},
	},
	Examples: []capability.Example{{Description: "Add a tab",
		Arguments: json.RawMessage(`{"layout_id":"123e4567-e89b-42d3-a456-426614174000","title":"Overview","position":1}`)}},
}

// DashboardTabResult is the new tab as the create tool reports it.
type DashboardTabResult struct {
	ID       string  `json:"id"`
	LayoutID string  `json:"layout_id"`
	Title    string  `json:"title"`
	Position float64 `json:"position"`
}

// pageLayoutTabCreate is the whole body of a tab create; the layout mode is fixed.
type pageLayoutTabCreate struct {
	PageLayoutID string  `json:"pageLayoutId"`
	Title        string  `json:"title"`
	Position     float64 `json:"position"`
	LayoutMode   string  `json:"layoutMode"`
}

// pageLayoutTabWire is the positive projection of Twenty's tab: creator, application, and widgets are never decoded.
type pageLayoutTabWire struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Position     *float64 `json:"position"`
	PageLayoutID string   `json:"pageLayoutId"`
	LayoutMode   *string  `json:"layoutMode"`
	DeletedAt    *string  `json:"deletedAt"`
}

func parseCreatedTab(op string, raw json.RawMessage, want pageLayoutTabCreate) (*DashboardTabResult, error) {
	var wire pageLayoutTabWire
	if json.Unmarshal(raw, &wire) != nil || !validUUID(wire.ID) || !validUUID(wire.PageLayoutID) || wire.Position == nil {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable dashboard tab")
	}
	if !equalUUID(wire.PageLayoutID, want.PageLayoutID) || wire.Title != want.Title || *wire.Position != want.Position ||
		wire.DeletedAt != nil || (wire.LayoutMode != nil && *wire.LayoutMode != want.LayoutMode) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different dashboard tab than the requested one")
	}
	return &DashboardTabResult{ID: wire.ID, LayoutID: wire.PageLayoutID, Title: wire.Title, Position: *wire.Position}, nil
}

// CreatePageLayoutTab reads the layout, then sends exactly one POST.
func (c *Client) CreatePageLayoutTab(ctx context.Context, body pageLayoutTabCreate) (*DashboardTabResult, error) {
	const op = "add dashboard tab"
	record, err := c.readDashboardLayout(ctx, op, body.PageLayoutID)
	if err != nil {
		return nil, err
	}
	summary, err := record.summary(op)
	if err != nil {
		return nil, err
	}
	if summary.TabCount >= dashboardTabsMax {
		return nil, invalidRequest(errPageLayoutTabFull)
	}
	var answer json.RawMessage
	if err := c.changeWith(ctx, op, pageLayoutTabUncertain, http.MethodPost, pageLayoutTabsPath, body, &answer); err != nil {
		var failure *provider.Error
		if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
			failure.Message = errPageLayoutWritePermission
		}
		return nil, err
	}
	tab, err := parseCreatedTab(op, answer, body)
	if err != nil {
		return nil, uncertainly(err, pageLayoutTabUncertain)
	}
	return tab, nil
}

func invokePageLayoutTabsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := requireWorkspaceScope(resolved); err != nil {
		return nil, err
	}
	var args struct {
		LayoutID string   `json:"layout_id"`
		Title    string   `json:"title"`
		Position *float64 `json:"position"`
	}
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError("add dashboard tab", "the validated arguments could not be read")
	}
	if !validUUID(args.LayoutID) {
		return nil, invalidRequest("layout_id must be a UUID")
	}
	if !cleanMetaText(args.Title) || strings.TrimSpace(args.Title) == "" ||
		utf8.RuneCountInString(args.Title) > dashboardTabTitleMax || args.Position == nil ||
		*args.Position < 0 || *args.Position > viewColumnPositionMax {
		return nil, invalidRequest(errPageLayoutTabArguments)
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreatePageLayoutTab(ctx, pageLayoutTabCreate{PageLayoutID: strings.ToLower(args.LayoutID),
		Title: args.Title, Position: *args.Position, LayoutMode: "GRID"})
}
