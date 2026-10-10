package infomaniakchat

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Local bounds of the sidebar category fields this provider writes.
const (
	maxCategoryChannelIDs = 1000
	maxCategoryIDLength   = 80
	categoryCustom        = "custom"
)

// The suffixes of a create, update, and delete request whose result is unclear.
const (
	uncertainCategoryCreate = "; the category may have been created, list the categories before creating it again"
	uncertainCategoryUpdate = "; the category may have been changed, list the categories before changing it again"
	uncertainCategoryDelete = "; the category may have been deleted, list the categories before deleting it again"
)

// validCategoryID keeps a category identifier to one path segment of the character class kChat produces:
// a custom category carries a plain kChat identifier, a system category is <type>_<user_id>_<team_id>.
func validCategoryID(value string) bool {
	if value == "" || len(value) > maxCategoryIDLength {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

var categoryIDSchema = `{"type":"string","minLength":1,"maxLength":` + itoa(maxCategoryIDLength) +
	`,"pattern":"^[a-z0-9_]{1,` + itoa(maxCategoryIDLength) + `}$"}`

var categoryNameSchema = channelDisplayNameSchema

var categoryChannelIDsSchema = `{"type":"array","maxItems":` + itoa(maxCategoryChannelIDs) +
	`,"uniqueItems":true,"items":` + idSchema + `}`

var categoryEntrySchema = `{"type":"object","properties":{` +
	`"id":` + categoryIDSchema + `,"team_id":` + idSchema + `,"display_name":{"type":"string"},` +
	`"type":{"type":"string"},"sorting":{"type":"string"},"muted":{"type":"boolean"},` +
	`"collapsed":{"type":"boolean"},"channel_ids":{"type":"array","items":` + idSchema + `}},` +
	`"required":["id","team_id","display_name","type","muted","collapsed","channel_ids"],` +
	`"additionalProperties":false}`

var categoryEntryFields = []capability.Field{
	{Name: "id", Description: "Category identifier, used as category_id by the category tools"},
	{Name: "team_id", Description: "Team this category belongs to"},
	{Name: "display_name", Description: "Display name of the category, untrusted data"},
	{Name: "type", Description: "custom, or the system category favorites, channels, or direct_messages"},
	{Name: "sorting", Description: "How kChat sorts the category, as kChat reports it"},
	{Name: "muted", Description: "Whether the category is muted"},
	{Name: "collapsed", Description: "Whether the category is collapsed in the sidebar"},
	{Name: "channel_ids", Description: "Reachable channels of the category; channels this connection may not " +
		"reach, and direct and group channels, are never shown"},
}

var categoryIDArgument = capability.Argument{Name: "category_id", Required: true,
	Description: "Sidebar category identifier of the token's own user, from categories.list"}

var categoryChannelIDsArgument = capability.Argument{Name: "channel_ids",
	Description: "Channels of the category in this order, up to " + itoa(maxCategoryChannelIDs) + "; every one " +
		"must be a reachable channel of the team. Replaces the reachable channels of the category; channels " +
		"this connection may not reach stay in place. A channel moves out of its other category"}

var categoriesList = capability.Descriptor{
	ID:      Provider + ".categories.list",
	Version: 1,
	Title:   "List Infomaniak kChat sidebar categories",
	Description: "List the sidebar categories of the token's own user in one team this connection is bound to, " +
		"in the order kChat reports; only reachable channels are shown, up to " + itoa(maxListLimit) + " categories",
	Tags:     []string{"infomaniak", "kchat", "categories", "sidebar", "list"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `},` +
		`"required":["team_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `,` +
		`"categories":{"type":"array","items":` + categoryEntrySchema + `},"total":{"type":"integer"},` +
		`"count":{"type":"integer"}},"required":["team_id","categories","total","count"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{teamIDArgument},
	Fields: append(append([]capability.Field{}, categoryEntryFields...),
		capability.Field{Name: "total", Description: "Number of categories of the team"},
		capability.Field{Name: "count", Description: "Number of categories reported"},
	),
	Examples: []capability.Example{{Description: "List the sidebar categories of one bound team",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000"}`)}},
}

var categoriesCreate = capability.Descriptor{
	ID:      Provider + ".categories.create",
	Version: 1,
	Title:   "Create an Infomaniak kChat sidebar category",
	Description: "Create exactly one confirmed custom sidebar category of the token's own user in a team this " +
		"connection is bound to, optionally with reachable channels. Creating the same category again makes a " +
		"second one",
	Tags:     []string{"infomaniak", "kchat", "categories", "sidebar", "create"},
	Risk:     channelChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `,"display_name":` +
		categoryNameSchema + `,"channel_ids":` + categoryChannelIDsSchema + `},` +
		`"required":["team_id","display_name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(categoryEntrySchema),
	Arguments: []capability.Argument{teamIDArgument,
		{Name: "display_name", Required: true, Description: "Display name, 1 to " + itoa(maxChannelDisplayName) +
			" characters"},
		categoryChannelIDsArgument},
	Fields: categoryEntryFields,
	Examples: []capability.Example{{Description: "Create a custom category",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000","display_name":"Customers"}`)}},
}

var categoriesUpdate = capability.Descriptor{
	ID:      Provider + ".categories.update",
	Version: 1,
	Title:   "Change an Infomaniak kChat sidebar category",
	Description: "Rename a custom category and replace its reachable channels with one confirmed request. A " +
		"system category (favorites, channels, direct_messages) accepts only channel_ids. Every other " +
		"property of the category stays as kChat holds it",
	Tags:     []string{"infomaniak", "kchat", "categories", "sidebar", "update"},
	Risk:     channelChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `,"category_id":` +
		categoryIDSchema + `,"display_name":` + categoryNameSchema + `,"channel_ids":` + categoryChannelIDsSchema +
		`},"required":["team_id","category_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(categoryEntrySchema),
	Arguments: []capability.Argument{teamIDArgument, categoryIDArgument,
		{Name: "display_name", Description: "New display name of a custom category, 1 to " +
			itoa(maxChannelDisplayName) + " characters; at least one of display_name and channel_ids is required"},
		categoryChannelIDsArgument},
	Fields: categoryEntryFields,
	Examples: []capability.Example{{Description: "Move two channels into a category",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000","category_id":"abc123cat00000000000000000",` +
			`"channel_ids":["abc123chan0000000000000000"]}`)}},
}

var categoriesDelete = capability.Descriptor{
	ID:      Provider + ".categories.delete",
	Version: 1,
	Title:   "Delete an Infomaniak kChat sidebar category",
	Description: "Delete exactly one custom sidebar category of the token's own user with one confirmed " +
		"request; its channels return to the default category. System categories are not deletable",
	Tags:                  []string{"infomaniak", "kchat", "categories", "sidebar", "delete"},
	Risk:                  channelChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `,"category_id":` +
		categoryIDSchema + `},"required":["team_id","category_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `,` +
		`"category_id":` + categoryIDSchema + `,"deleted":{"type":"boolean"}},` +
		`"required":["team_id","category_id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{teamIDArgument, categoryIDArgument},
	Fields: []capability.Field{
		{Name: "team_id", Description: "Team of the category"},
		{Name: "category_id", Description: "Category that was deleted"},
		{Name: "deleted", Description: "True once kChat confirmed the deletion"},
	},
	Examples: []capability.Example{{Description: "Delete one custom category",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000","category_id":"abc123cat00000000000000000"}`)}},
}

// categoryJSON is the kChat SidebarCategory resource. An update writes it back as read.
type categoryJSON struct {
	ID          string   `json:"id"`
	UserID      string   `json:"user_id"`
	TeamID      string   `json:"team_id"`
	DisplayName string   `json:"display_name"`
	Type        string   `json:"type"`
	Sorting     string   `json:"sorting"`
	Muted       bool     `json:"muted"`
	Collapsed   bool     `json:"collapsed"`
	ChannelIDs  []string `json:"channel_ids"`
}

// CategoryEntry is the stable Qatlas view of one sidebar category; its channels are the reachable ones.
type CategoryEntry struct {
	ID          string   `json:"id"`
	TeamID      string   `json:"team_id"`
	DisplayName string   `json:"display_name"`
	Type        string   `json:"type"`
	Sorting     string   `json:"sorting,omitempty"`
	Muted       bool     `json:"muted"`
	Collapsed   bool     `json:"collapsed"`
	ChannelIDs  []string `json:"channel_ids"`
}

// CategoriesPage is the capped listing of the sidebar categories of one team.
type CategoriesPage struct {
	TeamID     string          `json:"team_id"`
	Categories []CategoryEntry `json:"categories"`
	Total      int             `json:"total"`
	Count      int             `json:"count"`
}

// CategoryDeleted is the answer of one confirmed category deletion.
type CategoryDeleted struct {
	TeamID     string `json:"team_id"`
	CategoryID string `json:"category_id"`
	Deleted    bool   `json:"deleted"`
}

func categoriesPath(teamID string) string {
	return "/api/v4/users/me/teams/" + url.PathEscape(teamID) + "/channels/categories"
}

// categoryOf reduces a category to the reachable channels; reachable maps a channel ID to its resource.
func categoryOf(cat *categoryJSON, reachable map[string]*channelJSON) CategoryEntry {
	ids := make([]string, 0, len(cat.ChannelIDs))
	for _, id := range cat.ChannelIDs {
		if _, ok := reachable[id]; ok {
			ids = append(ids, id)
		}
	}
	return CategoryEntry{ID: cat.ID, TeamID: cat.TeamID, DisplayName: bounded(cat.DisplayName),
		Type: cat.Type, Sorting: bounded(cat.Sorting), Muted: cat.Muted, Collapsed: cat.Collapsed, ChannelIDs: ids}
}

func knownCategoryType(kind string) bool {
	return kind == categoryCustom || kind == "favorites" || kind == "channels" || kind == "direct_messages"
}

// ownsCategory reports whether a category kChat answered with is a well-formed category of the own user in
// the requested team.
func ownsCategory(cat *categoryJSON, teamID, own string) bool {
	return validCategoryID(cat.ID) && cat.TeamID == teamID && cat.UserID == own && knownCategoryType(cat.Type)
}

// checkCategoryChannels refuses channel IDs that are repeated or outside the connection's local boundary,
// before any secret is read.
func checkCategoryChannels(resolved *config.Resolved, ids []string) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !validMattermostID(id) {
			return invalidRequest("channel_ids must hold kChat-style identifiers")
		}
		if seen[id] {
			return invalidRequest("channel_ids must not repeat a channel")
		}
		seen[id] = true
		if !bound.allowsChannel(id) {
			return invalidRequest("channel_ids must be inside the targets of this connection")
		}
	}
	return nil
}

// checkCategoryName validates a display name before any secret is read.
func checkCategoryName(name string) error {
	if !validChannelText(name, 1, maxChannelDisplayName, false) {
		return invalidRequest("display_name must be 1 to " + itoa(maxChannelDisplayName) +
			" characters without control characters")
	}
	return nil
}

// requireReachable refuses a channel the live team listing does not hold as reachable.
func requireReachable(ids []string, reachable map[string]*channelJSON) error {
	for _, id := range ids {
		if _, ok := reachable[id]; !ok {
			return invalidRequest("channel_ids must be reachable channels of the team")
		}
	}
	return nil
}

type categoriesListArguments struct {
	TeamID string `json:"team_id"`
}

func invokeCategoriesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list categories"
	var input categoriesListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectTeam(resolved, input.TeamID); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListCategories(ctx, input.TeamID)
}

// orderedCategories is kChat's answer of the category listing; the OpenAPI document wraps it in an array,
// so both forms are read.
type orderedCategories struct {
	Order      []string       `json:"order"`
	Categories []categoryJSON `json:"categories"`
}

// ListCategories reads the categories of the own user in one bound team with three reads, never
// follows further, and drops a category of another team or user.
func (c *Client) ListCategories(ctx context.Context, teamID string) (*CategoriesPage, error) {
	const op = "list categories"
	own, err := c.ownUserID(ctx, op)
	if err != nil {
		return nil, err
	}
	var data json.RawMessage
	if err := c.do(ctx, op, http.MethodGet, categoriesPath(teamID), nil, nil, &data, false); err != nil {
		return nil, err
	}
	var lists []orderedCategories
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '[' {
		err = json.Unmarshal(trimmed, &lists)
	} else {
		var one orderedCategories
		if err = json.Unmarshal(trimmed, &one); err == nil {
			lists = []orderedCategories{one}
		}
	}
	if err != nil {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response"}
	}
	reachable, err := c.reachableChannels(ctx, op, teamID)
	if err != nil {
		return nil, err
	}
	rank := map[string]int{}
	var all []categoryJSON
	for _, list := range lists {
		for _, id := range list.Order {
			if _, seen := rank[id]; !seen {
				rank[id] = len(rank)
			}
		}
		all = append(all, list.Categories...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		ri, okI := rank[all[i].ID]
		rj, okJ := rank[all[j].ID]
		return okI && (!okJ || ri < rj)
	})
	entries := make([]CategoryEntry, 0, len(all))
	for i := range all {
		if ownsCategory(&all[i], teamID, own) {
			entries = append(entries, categoryOf(&all[i], reachable))
		}
	}
	total := len(entries)
	if len(entries) > maxListLimit {
		entries = entries[:maxListLimit]
	}
	return &CategoriesPage{TeamID: teamID, Categories: entries, Total: total, Count: len(entries)}, nil
}

type categoriesCreateArguments struct {
	TeamID      string   `json:"team_id"`
	DisplayName string   `json:"display_name"`
	ChannelIDs  []string `json:"channel_ids"`
}

func invokeCategoriesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create category"
	var input categoriesCreateArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectTeam(resolved, input.TeamID); err != nil {
		return nil, err
	}
	if err := checkCategoryName(input.DisplayName); err != nil {
		return nil, err
	}
	if len(input.ChannelIDs) > maxCategoryChannelIDs {
		return nil, invalidRequest("channel_ids holds too many channels")
	}
	if err := checkCategoryChannels(resolved, input.ChannelIDs); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreateCategory(ctx, input)
}

// CreateCategory sends exactly one POST built from typed fields and never repeats it: a failure after the
// request may have reached kChat says so instead.
func (c *Client) CreateCategory(ctx context.Context, input categoriesCreateArguments) (*CategoryEntry, error) {
	const op = "create category"
	own, err := c.ownUserID(ctx, op)
	if err != nil {
		return nil, err
	}
	reachable := map[string]*channelJSON{}
	if len(input.ChannelIDs) > 0 {
		if reachable, err = c.reachableChannels(ctx, op, input.TeamID); err != nil {
			return nil, err
		}
		if err := requireReachable(input.ChannelIDs, reachable); err != nil {
			return nil, err
		}
	}
	ids := append([]string{}, input.ChannelIDs...)
	body := map[string]any{"team_id": input.TeamID, "user_id": own, "display_name": input.DisplayName,
		"type": categoryCustom, "channel_ids": ids}
	var cat categoryJSON
	if err := c.doWith(ctx, op, http.MethodPost, categoriesPath(input.TeamID), nil, body, &cat,
		uncertainCategoryCreate); err != nil {
		return nil, err
	}
	if !ownsCategory(&cat, input.TeamID, own) || cat.Type != categoryCustom {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainCategoryCreate}
	}
	entry := categoryOf(&cat, reachable)
	return &entry, nil
}

// readCategory reads one category of the own user and returns it only when it belongs to the team and the
// own user.
func (c *Client) readCategory(ctx context.Context, op, teamID, categoryID, own string) (*categoryJSON, error) {
	var cat categoryJSON
	if err := c.do(ctx, op, http.MethodGet, categoriesPath(teamID)+"/"+url.PathEscape(categoryID), nil, nil, &cat,
		false); err != nil {
		return nil, err
	}
	if cat.ID != categoryID || !ownsCategory(&cat, teamID, own) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response"}
	}
	return &cat, nil
}

type categoriesUpdateArguments struct {
	TeamID      string   `json:"team_id"`
	CategoryID  string   `json:"category_id"`
	DisplayName *string  `json:"display_name"`
	ChannelIDs  []string `json:"channel_ids"`
}

func invokeCategoriesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update category"
	var input categoriesUpdateArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectTeam(resolved, input.TeamID); err != nil {
		return nil, err
	}
	if !validCategoryID(input.CategoryID) {
		return nil, invalidRequest("category_id must be a kChat category identifier")
	}
	if input.DisplayName == nil && input.ChannelIDs == nil {
		return nil, invalidRequest("give at least one of display_name and channel_ids")
	}
	if input.DisplayName != nil {
		if err := checkCategoryName(*input.DisplayName); err != nil {
			return nil, err
		}
	}
	if len(input.ChannelIDs) > maxCategoryChannelIDs {
		return nil, invalidRequest("channel_ids holds too many channels")
	}
	if err := checkCategoryChannels(resolved, input.ChannelIDs); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateCategory(ctx, input)
}

// UpdateCategory reads the category and the reachable channels first, then sends exactly one PUT that
// changes only the display name and the reachable channels and writes every other property back as read.
// Channels of the category this connection may not reach are written back unchanged. It never repeats the
// PUT: a failure after the request may have reached kChat says so instead.
func (c *Client) UpdateCategory(ctx context.Context, input categoriesUpdateArguments) (*CategoryEntry, error) {
	const op = "update category"
	own, err := c.ownUserID(ctx, op)
	if err != nil {
		return nil, err
	}
	current, err := c.readCategory(ctx, op, input.TeamID, input.CategoryID, own)
	if err != nil {
		return nil, err
	}
	if input.DisplayName != nil && current.Type != categoryCustom {
		return nil, invalidRequest("only a custom category can be renamed")
	}
	reachable, err := c.reachableChannels(ctx, op, input.TeamID)
	if err != nil {
		return nil, err
	}
	if err := requireReachable(input.ChannelIDs, reachable); err != nil {
		return nil, err
	}
	body := *current
	if input.DisplayName != nil {
		body.DisplayName = *input.DisplayName
	}
	if input.ChannelIDs != nil {
		body.ChannelIDs = append([]string{}, input.ChannelIDs...)
		for _, id := range current.ChannelIDs {
			if _, ok := reachable[id]; !ok {
				body.ChannelIDs = append(body.ChannelIDs, id)
			}
		}
	}
	if body.ChannelIDs == nil {
		body.ChannelIDs = []string{}
	}
	var cat categoryJSON
	if err := c.doWith(ctx, op, http.MethodPut, categoriesPath(input.TeamID)+"/"+url.PathEscape(current.ID), nil,
		body, &cat, uncertainCategoryUpdate); err != nil {
		return nil, err
	}
	if cat.ID != current.ID || !ownsCategory(&cat, input.TeamID, own) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainCategoryUpdate}
	}
	entry := categoryOf(&cat, reachable)
	return &entry, nil
}

type categoriesDeleteArguments struct {
	TeamID     string `json:"team_id"`
	CategoryID string `json:"category_id"`
}

func invokeCategoriesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete category"
	var input categoriesDeleteArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectTeam(resolved, input.TeamID); err != nil {
		return nil, err
	}
	if !validCategoryID(input.CategoryID) {
		return nil, invalidRequest("category_id must be a kChat category identifier")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.DeleteCategory(ctx, input.TeamID, input.CategoryID)
}

// DeleteCategory reads the category, accepts only a custom one, and sends exactly one DELETE that it never
// repeats: a failure after the request may have reached kChat says so instead.
func (c *Client) DeleteCategory(ctx context.Context, teamID, categoryID string) (*CategoryDeleted, error) {
	const op = "delete category"
	own, err := c.ownUserID(ctx, op)
	if err != nil {
		return nil, err
	}
	current, err := c.readCategory(ctx, op, teamID, categoryID, own)
	if err != nil {
		return nil, err
	}
	if current.Type != categoryCustom {
		return nil, invalidRequest("only a custom category can be deleted")
	}
	var answer struct {
		ID string `json:"id"`
	}
	if err := c.doWith(ctx, op, http.MethodDelete, categoriesPath(teamID)+"/"+url.PathEscape(current.ID), nil, nil,
		&answer, uncertainCategoryDelete); err != nil {
		return nil, err
	}
	if answer.ID != "" && answer.ID != current.ID {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainCategoryDelete}
	}
	return &CategoryDeleted{TeamID: teamID, CategoryID: current.ID, Deleted: true}, nil
}
