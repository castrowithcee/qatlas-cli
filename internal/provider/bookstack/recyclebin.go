package bookstack

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// recycleBinInstanceWide names the capability in a refusal by requireInstanceScope.
const recycleBinInstanceWide = "recycle bin"

const recycleBinRoles = "BookStack requires the role permissions settings-manage and restrictions-manage-all for the token's user. " +
	"The recycle bin covers the whole instance, so a connection bound to books cannot use this tool"

const recycleBinListOutput = `{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"deleted_by":{"type":"integer"},"created_at":{"type":"string"},"deletable_type":{"type":"string"},"deletable_id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},"book_id":{"type":"integer"},"chapter_id":{"type":"integer"},"parent_type":{"type":"string"},"parent_id":{"type":"integer"},"pages_count":{"type":"integer"},"chapters_count":{"type":"integer"}},"required":["id","deleted_by","created_at","deletable_type","deletable_id"]}}`

const deletionIDSchema = `{"type":"object","properties":{"deletion_id":{"type":"integer","minimum":1}},"required":["deletion_id"],"additionalProperties":false}`

var (
	recycleBinList = capability.Descriptor{
		ID: Provider + ".recyclebin.list", Version: 1, Title: "List the BookStack recycle bin",
		Description: "List the deleted pages, chapters, books, and shelves in the recycle bin with their deletion identifier. " +
			recycleBinRoles,
		Tags: []string{"administration", "recyclebin", "bookstack"},
		Risk: bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(recycleBinListOutput),
		Arguments: []capability.Argument{
			{Name: "limit", Description: "Maximum number of entries to return; 0 returns all"},
			{Name: "offset", Description: "Number of entries to skip"},
		},
		Fields: []capability.Field{
			{Name: "id", Description: "Deletion identifier for restore and destroy"},
			{Name: "deleted_by", Description: "Identifier of the user who deleted the item"},
			{Name: "created_at", Description: "Deletion time"},
			{Name: "deletable_type", Description: "page, chapter, book, or bookshelf"},
			{Name: "deletable_id", Description: "Identifier of the deleted item"},
			{Name: "name", Description: "Name of the deleted item, untrusted data"},
			{Name: "slug", Description: "Slug of the deleted item, untrusted data"},
			{Name: "book_id", Description: "Book of the deleted page or chapter"},
			{Name: "chapter_id", Description: "Chapter of the deleted page"},
			{Name: "parent_type", Description: "Type of the parent of the deleted item"},
			{Name: "parent_id", Description: "Identifier of the parent of the deleted item"},
			{Name: "pages_count", Description: "Pages inside a deleted book or chapter"},
			{Name: "chapters_count", Description: "Chapters inside a deleted book"},
		},
		Examples: []capability.Example{{Description: "List the first 25 entries", Arguments: json.RawMessage(`{"limit":25}`)}},
	}

	recycleBinRestore = capability.Descriptor{
		ID: Provider + ".recyclebin.restore", Version: 1, Title: "Restore from the BookStack recycle bin",
		Description: "Restore one deletion from the recycle bin together with the content deleted with it. " +
			recycleBinRoles,
		Tags: []string{"administration", "recyclebin", "bookstack", "update"}, Provider: Provider,
		Risk:         capability.Risk{Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent, Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
		InputSchema:  json.RawMessage(deletionIDSchema),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"restore_count":{"type":"integer"}},"required":["restore_count"]}`),
		Arguments:    []capability.Argument{{Name: "deletion_id", Description: "Deletion identifier from the recycle bin list", Required: true}},
	}

	recycleBinDestroy = capability.Descriptor{
		ID: Provider + ".recyclebin.destroy", Version: 1, Title: "Permanently destroy from the BookStack recycle bin",
		Description: "Permanently destroy one deletion in the recycle bin together with the content deleted with it. " +
			"This cannot be undone. " + recycleBinRoles,
		Tags: []string{"administration", "recyclebin", "bookstack", "delete"}, Provider: Provider,
		RequiresToolAllowList: true,
		Risk:                  capability.Risk{Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent, Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
		InputSchema:           json.RawMessage(deletionIDSchema),
		OutputSchema:          json.RawMessage(`{"type":"object","properties":{"delete_count":{"type":"integer"}},"required":["delete_count"]}`),
		Arguments:             []capability.Argument{{Name: "deletion_id", Description: "Deletion identifier from the recycle bin list", Required: true}},
	}
)

type recycleBinEntryJSON struct {
	ID            int64  `json:"id"`
	DeletedBy     int64  `json:"deleted_by"`
	CreatedAt     string `json:"created_at"`
	DeletableType string `json:"deletable_type"`
	DeletableID   int64  `json:"deletable_id"`
	Deletable     struct {
		Name          string `json:"name"`
		Slug          string `json:"slug"`
		BookID        *int64 `json:"book_id"`
		ChapterID     *int64 `json:"chapter_id"`
		PagesCount    *int64 `json:"pages_count"`
		ChaptersCount *int64 `json:"chapters_count"`
		Parent        *struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"parent"`
	} `json:"deletable"`
}

func recycleBinRow(e recycleBinEntryJSON) output.Row {
	d := e.Deletable
	row := output.Row{
		"id": e.ID, "deleted_by": e.DeletedBy, "created_at": clip(e.CreatedAt, maxResultString),
		"deletable_type": clip(e.DeletableType, maxResultString), "deletable_id": e.DeletableID,
		"name": clip(d.Name, maxResultString), "slug": clip(d.Slug, maxResultString),
	}
	for key, value := range map[string]*int64{"book_id": d.BookID, "chapter_id": d.ChapterID,
		"pages_count": d.PagesCount, "chapters_count": d.ChaptersCount} {
		if value != nil {
			row[key] = *value
		}
	}
	if d.Parent != nil {
		row["parent_type"] = clip(d.Parent.Type, maxResultString)
		row["parent_id"] = d.Parent.ID
	}
	return row
}

func invokeRecycleBinList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, recycleBinInstanceWide); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListRecycleBin(ctx, in.Limit, in.Offset)
}

// ListRecycleBin returns the reduced entries of the recycle bin.
func (c *Client) ListRecycleBin(ctx context.Context, limit, offset int) (output.Collection, error) {
	rows, err := scanList(ctx, c, scanSpec[recycleBinEntryJSON]{
		op: "list recycle bin", path: "/api/recycle-bin", limit: limit, offset: offset,
		arguments: argNames(recycleBinList),
		id:        func(e recycleBinEntryJSON) int64 { return e.ID },
		keep:      func(recycleBinEntryJSON) bool { return true },
		row:       recycleBinRow,
	})
	if err != nil {
		return output.Collection{}, err
	}
	return output.Collection{Columns: fieldNames(recycleBinList), Rows: rows}, nil
}

func decodeDeletionID(raw json.RawMessage, op string) (int64, error) {
	var in struct {
		DeletionID int64 `json:"deletion_id"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return 0, providerError(op, "the validated arguments could not be read")
	}
	return in.DeletionID, nil
}

func invokeRecycleBinRestore(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := decodeDeletionID(raw, "restore from recycle bin")
	if err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, recycleBinInstanceWide); err != nil {
		return nil, err
	}
	if id <= 0 {
		return nil, invalidRequest("deletion_id must be a positive integer")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.RestoreFromRecycleBin(ctx, id)
}

func invokeRecycleBinDestroy(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := decodeDeletionID(raw, "destroy from recycle bin")
	if err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, recycleBinInstanceWide); err != nil {
		return nil, err
	}
	if id <= 0 {
		return nil, invalidRequest("deletion_id must be a positive integer")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.DestroyFromRecycleBin(ctx, id)
}

// RestoreFromRecycleBin sends one restore request.
func (c *Client) RestoreFromRecycleBin(ctx context.Context, id int64) (output.Object, error) {
	var result struct {
		RestoreCount int64 `json:"restore_count"`
	}
	if err := c.mutate(ctx, "restore from recycle bin", http.MethodPut, "/api/recycle-bin/"+strconv.FormatInt(id, 10),
		nil, &result, argNames(recycleBinRestore)); err != nil {
		return output.Object{}, err
	}
	return output.Object{Fields: []output.Field{{Name: "restore_count", Value: result.RestoreCount}}}, nil
}

// DestroyFromRecycleBin sends one destroy request.
func (c *Client) DestroyFromRecycleBin(ctx context.Context, id int64) (output.Object, error) {
	var result struct {
		DeleteCount int64 `json:"delete_count"`
	}
	if err := c.mutate(ctx, "destroy from recycle bin", http.MethodDelete, "/api/recycle-bin/"+strconv.FormatInt(id, 10),
		nil, &result, argNames(recycleBinDestroy)); err != nil {
		return output.Object{}, err
	}
	return output.Object{Fields: []output.Field{{Name: "delete_count", Value: result.DeleteCount}}}, nil
}
