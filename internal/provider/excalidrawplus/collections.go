package excalidrawplus

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var collectionSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
	`"is_default":{"type":"boolean"},"created":{"type":"string"},"updated":{"type":"string"},` +
	`"workspace":{"type":"string"},"creator":{"type":"string"},"updater":{"type":"string"},` +
	`"teams":{"type":"array","items":{"type":"string"},"maxItems":` + strconv.Itoa(maxListedIDs) + `},` +
	`"sort_order":{"type":"string"},"emoji":` + emojiSchema + `},` +
	`"required":["id","name"],"additionalProperties":false}`

var collectionFields = []capability.Field{
	{Name: "id", Description: "Collection identifier, usable as collection_id and in a collection/ID target"},
	{Name: "name", Description: "Collection name, untrusted data"},
	{Name: "is_default", Description: "True for the workspace's default collection"},
	{Name: "created", Description: "Creation time, as Excalidraw+ reports it"},
	{Name: "updated", Description: "Last update time, as Excalidraw+ reports it"},
	{Name: "workspace", Description: "Workspace identifier, untrusted data"},
	{Name: "creator", Description: "Identifier of the creating user, untrusted data"},
	{Name: "updater", Description: "Identifier of the last updating user, untrusted data"},
	{Name: "teams", Description: "Identifiers of the teams the collection belongs to, at most 100"},
	{Name: "sort_order", Description: "Opaque ordering key, untrusted data"},
	{Name: "emoji", Description: "Emoji of the collection with id, native, and unified, untrusted data"},
}

var collectionsList = capability.Descriptor{
	ID:      Provider + ".collections.list",
	Version: 1,
	Title:   "List Excalidraw+ collections",
	Description: "List the workspace's collections, restricted to this connection's collection allow-list; " +
		"page by page with a numeric offset",
	Tags:     []string{"excalidrawplus", "collections", "list", "whiteboard"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + pageSchema + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"collections":{"type":"array","items":` + collectionSchema + `},` +
		`"offset":{"type":"integer"},"limit":{"type":"integer"},"has_next_page":{"type":"boolean"},` +
		`"next_offset":{"type":"integer"},"count":{"type":"integer"}},` +
		`"required":["collections","offset","limit","has_next_page","count"],"additionalProperties":false}`),
	Arguments: pageArguments,
	Fields:    append(append([]capability.Field{}, collectionFields...), pageFields...),
	Examples:  []capability.Example{{Description: "List the first page of reachable collections", Arguments: json.RawMessage(`{}`)}},
}

// pageJSON is the pagination envelope Excalidraw+ documents for its list endpoints.
type pageJSON struct {
	Limit       int  `json:"limit"`
	Offset      int  `json:"offset"`
	HasNextPage bool `json:"hasNextPage"`
}

type collectionJSON struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
	Created   string `json:"created"`
	Updated   string `json:"updated"`
	IsDeleted bool   `json:"isDeleted"`

	Workspace string     `json:"workspace"`
	Creator   string     `json:"creator"`
	Updater   string     `json:"updater"`
	Teams     []string   `json:"teams"`
	SortOrder string     `json:"sortOrder"`
	Emoji     *emojiJSON `json:"emoji"`
}

type collectionsPageJSON struct {
	pageJSON
	Data []collectionJSON `json:"data"`
}

// Collection is the stable view of one collection.
type Collection struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default,omitempty"`
	Created   string `json:"created,omitempty"`
	Updated   string `json:"updated,omitempty"`

	Workspace string   `json:"workspace,omitempty"`
	Creator   string   `json:"creator,omitempty"`
	Updater   string   `json:"updater,omitempty"`
	Teams     []string `json:"teams,omitempty"`
	SortOrder string   `json:"sort_order,omitempty"`
	Emoji     *Emoji   `json:"emoji,omitempty"`
}

// CollectionsPage is one offset-paginated, allow-list-filtered listing.
type CollectionsPage struct {
	Collections []Collection `json:"collections"`
	Offset      int          `json:"offset"`
	Limit       int          `json:"limit"`
	HasNextPage bool         `json:"has_next_page"`
	NextOffset  *int         `json:"next_offset,omitempty"`
	Count       int          `json:"count"`
}

type pageArgs struct {
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

func (a pageArgs) query() url.Values {
	limit := a.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	return url.Values{"offset": {strconv.Itoa(a.Offset)}, "limit": {strconv.Itoa(limit)}}
}

func nextOffset(page pageJSON, requested pageArgs) *int {
	if !page.HasNextPage {
		return nil
	}
	limit := requested.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	next := requested.Offset + limit
	return &next
}

func invokeCollectionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input pageArgs
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list collections", "the validated arguments could not be read")
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if err := requireCollections(bound); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListCollections(ctx, input)
}

// ListCollections reads one page and drops every collection outside the connection's allow-list or in the
// trash. has_next_page is Excalidraw+'s own value for the unfiltered page.
func (c *Client) ListCollections(ctx context.Context, input pageArgs) (*CollectionsPage, error) {
	var page collectionsPageJSON
	if err := c.get(ctx, "list collections", "/collections", input.query(), &page, maxResponseBytes); err != nil {
		return nil, err
	}
	result := &CollectionsPage{Collections: []Collection{}, Offset: input.Offset, Limit: page.Limit,
		HasNextPage: page.HasNextPage, NextOffset: nextOffset(page.pageJSON, input)}
	if result.Limit == 0 {
		result.Limit = input.Limit
		if result.Limit == 0 {
			result.Limit = defaultListLimit
		}
	}
	for _, item := range page.Data {
		if item.IsDeleted || !validID(item.ID) || !c.scope.permits(item.ID) {
			continue
		}
		result.Collections = append(result.Collections, Collection{ID: item.ID, Name: boundedValue(item.Name),
			IsDefault: item.IsDefault, Created: boundedValue(item.Created), Updated: boundedValue(item.Updated),
			Workspace: cleanValue(item.Workspace), Creator: cleanValue(item.Creator), Updater: cleanValue(item.Updater),
			Teams: idsOf(item.Teams), SortOrder: cleanValue(item.SortOrder), Emoji: emojiOf(item.Emoji)})
	}
	result.Count = len(result.Collections)
	return result, nil
}
