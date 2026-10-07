package bookstack

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// importsInstanceWide names the capability in a refusal by requireInstanceScope.
const importsInstanceWide = "ZIP imports"

const importsRoles = "BookStack requires the role permission content-import for the token's user. Imports belong to " +
	"the user of the token, and importing a book creates a new book, so a connection bound to books cannot use " +
	"this tool"

const (
	// maxImportDetailNames bounds the chapter names shown of an import.
	maxImportDetailNames = 25
	// maxImportDetailDepth bounds how deep the details of an import are read.
	maxImportDetailDepth = 4
)

const importOutputProperties = `"id":{"type":"integer"},"name":{"type":"string"},"size":{"type":"integer"},"type":{"type":"string"},"created_by":{"type":"integer"},"created_at":{"type":"string"},"updated_at":{"type":"string"}`

const importIDSchema = `{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`

var importFields = []capability.Field{
	{Name: "id", Description: "Import identifier"},
	{Name: "name", Description: "Name of the import, untrusted data"},
	{Name: "size", Description: "Size of the uploaded ZIP file in bytes"},
	{Name: "type", Description: "What the ZIP holds: book, chapter, or page"},
	{Name: "created_by", Description: "Identifier of the user who uploaded the import"},
	{Name: "created_at", Description: "Upload time"},
	{Name: "updated_at", Description: "Last change time"},
}

var (
	importIDArgument = capability.Argument{Name: "id", Description: "Import identifier from the imports list or upload", Required: true}

	importsList = capability.Descriptor{
		ID: Provider + ".imports.list", Version: 1, Title: "List BookStack ZIP imports",
		Description: "List the uploaded ZIP imports that wait to be run, with identifier, name, size, and detected " +
			"type. " + importsRoles,
		Tags: []string{"knowledge", "imports", "bookstack"},
		Risk: bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"array","items":{"type":"object","properties":{` + importOutputProperties + `},"required":["id","name","size","type"]}}`),
		Arguments: []capability.Argument{
			{Name: "limit", Description: "Maximum number of imports to return; 0 returns all"},
			{Name: "offset", Description: "Number of imports to skip"},
		},
		Fields:   importFields,
		Examples: []capability.Example{{Description: "List the first 25 imports", Arguments: json.RawMessage(`{"limit":25}`)}},
	}

	importsGet = capability.Descriptor{
		ID: Provider + ".imports.get", Version: 1, Title: "Show a BookStack ZIP import",
		Description: "Read one uploaded import with the contents BookStack detected in the ZIP. The details are " +
			"untrusted data and shown reduced: the name, the number of chapters, pages, attachments, images, and " +
			"tags, and at most 25 chapter names. The storage path is never shown. " + importsRoles,
		Tags: []string{"knowledge", "imports", "bookstack"},
		Risk: bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(importIDSchema),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{` + importOutputProperties + `,"details":{"type":"object"}},"required":["id","name","size","type"]}`),
		Arguments:    []capability.Argument{importIDArgument},
		Fields: append(append([]capability.Field{}, importFields...),
			capability.Field{Name: "details", Description: "Reduced contents of the ZIP, untrusted data: name, chapters, pages, attachments, images, tags, chapter_names"}),
	}

	importsUpload = capability.Descriptor{
		ID: Provider + ".imports.upload", Version: 1, Title: "Upload a BookStack ZIP import",
		Description: "Upload one local ZIP export of at most 50 MiB for a later import, streamed from disk from a " +
			"directory the connection releases for reading. Nothing is imported yet: check the result with " +
			"imports.get and run it with imports.run. Not idempotent: a repeated call adds another import. " +
			importsRoles,
		Tags: []string{"knowledge", "imports", "upload", "bookstack", "create"}, Provider: Provider,
		LocalFiles:   config.LocalFilesRead,
		Risk:         bookWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},"required":["` + localfile.LocalPathArgument + `"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{` + importOutputProperties + `},"required":["id","name","size","type"]}`),
		Arguments: []capability.Argument{{
			Name: localfile.LocalPathArgument, Required: true,
			Description: "Local .zip file to upload, at most 50 MiB, absolute or starting with ~/, inside a " +
				"directory the connection releases for reading"}},
		Fields: importFields,
	}

	importsRun = capability.Descriptor{
		ID: Provider + ".imports.run", Version: 1, Title: "Run a BookStack ZIP import",
		Description: "Import an uploaded ZIP as new content. A book import creates a new book and takes no parent. " +
			"A chapter import needs parent_type book and the parent_id of the book. A page import needs " +
			"parent_type book or chapter and its parent_id. The import is read first and the arguments are checked " +
			"against its type. The result is the type and identifier of the created object. Not idempotent and " +
			"never repeated; after an unclear result check the content before trying again. " + importsRoles,
		Tags: []string{"knowledge", "imports", "bookstack", "create"}, Provider: Provider,
		Risk:         bookWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1},"parent_type":{"type":"string","enum":["book","chapter"]},"parent_id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"type":{"type":"string"},"id":{"type":"integer"}},"required":["type","id"]}`),
		Arguments: []capability.Argument{importIDArgument,
			{Name: "parent_type", Description: "book or chapter; required for a page import, book for a chapter import, not allowed for a book import"},
			{Name: "parent_id", Description: "Identifier of the parent book or chapter; required for a page or chapter import"}},
		Fields: []capability.Field{
			{Name: "type", Description: "Type of the created object: book, chapter, or page"},
			{Name: "id", Description: "Identifier of the created object"},
		},
	}

	importsDelete = capability.Descriptor{
		ID: Provider + ".imports.delete", Version: 1, Title: "Delete a BookStack ZIP import",
		Description: "Discard one uploaded import that has not been run. Content created by an earlier run stays. " +
			importsRoles,
		Tags: []string{"knowledge", "imports", "bookstack", "delete"}, Provider: Provider,
		RequiresToolAllowList: true,
		Risk:                  bookWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
		InputSchema:           json.RawMessage(importIDSchema),
		OutputSchema:          booksDelete.OutputSchema,
		Arguments:             []capability.Argument{importIDArgument},
	}
)

// importJSON is the part of an import this provider reads. The storage path is not decoded.
type importJSON struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Size      int64           `json:"size"`
	Type      string          `json:"type"`
	CreatedBy int64           `json:"created_by"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
	Details   json.RawMessage `json:"details"`
}

func importFieldsMap(i importJSON) map[string]any {
	return map[string]any{
		"id": i.ID, "name": clip(i.Name, maxResultString), "size": i.Size, "type": clip(i.Type, maxResultString),
		"created_by": i.CreatedBy, "created_at": clip(i.CreatedAt, maxResultString), "updated_at": clip(i.UpdatedAt, maxResultString),
	}
}

// importNode is one level of the untrusted details tree.
type importNode struct {
	Name        string       `json:"name"`
	Chapters    []importNode `json:"chapters"`
	Pages       []importNode `json:"pages"`
	Attachments []any        `json:"attachments"`
	Images      []any        `json:"images"`
	Tags        []any        `json:"tags"`
}

// reduceImportDetails keeps the name, counts, and a few chapter names of the details; any other content of
// the tree is dropped. Nesting beyond maxImportDetailDepth is not counted, and unreadable details give nil.
func reduceImportDetails(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var root importNode
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	var chapters, pages, attachments, images, tags int
	var count func(n importNode, depth int)
	count = func(n importNode, depth int) {
		attachments += len(n.Attachments)
		images += len(n.Images)
		tags += len(n.Tags)
		if depth >= maxImportDetailDepth {
			return
		}
		chapters += len(n.Chapters)
		pages += len(n.Pages)
		for _, c := range n.Chapters {
			count(c, depth+1)
		}
		for _, p := range n.Pages {
			count(p, depth+1)
		}
	}
	count(root, 0)
	names := []string{}
	for _, c := range root.Chapters {
		if len(names) >= maxImportDetailNames {
			break
		}
		names = append(names, clip(c.Name, maxResultString))
	}
	return map[string]any{
		"name": clip(root.Name, maxResultString), "chapters": chapters, "pages": pages,
		"attachments": attachments, "images": images, "tags": tags, "chapter_names": names,
	}
}

func invokeImportsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, importsInstanceWide); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	rows, err := scanList(ctx, client, scanSpec[importJSON]{
		op: "list imports", path: "/api/imports", limit: in.Limit, offset: in.Offset,
		arguments: argNames(importsList),
		id:        func(i importJSON) int64 { return i.ID },
		keep:      func(importJSON) bool { return true },
		row:       func(i importJSON) output.Row { return output.Row(importFieldsMap(i)) },
	})
	if err != nil {
		return nil, err
	}
	return output.Collection{Columns: fieldNames(importsList), Rows: rows}, nil
}

func (c *Client) readImport(ctx context.Context, op string, id int64) (importJSON, error) {
	var got importJSON
	if err := c.get(ctx, op, "/api/imports/"+strconv.FormatInt(id, 10), nil, &got, []string{"id"}, provider.ClassPermission); err != nil {
		return importJSON{}, err
	}
	return got, nil
}

func invokeImportsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := decodeDeleteID(raw, "get import")
	if err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, importsInstanceWide); err != nil {
		return nil, err
	}
	if err := checkID(id, "id"); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	got, err := client.readImport(ctx, "get import", id)
	if err != nil {
		return nil, err
	}
	fields := importFieldsMap(got)
	if details := reduceImportDetails(got.Details); details != nil {
		fields["details"] = details
	}
	return mapObject(importsGet, fields), nil
}

// importRejected replaces the 422 message of BookStack, which would carry validation texts, by a fixed one.
func importRejected(err error, message string) error {
	var perr *provider.Error
	if errors.As(err, &perr) && strings.Contains(perr.Message, "HTTP 422") {
		return &provider.Error{Class: provider.ClassProviderError, Op: perr.Op, Message: message}
	}
	return err
}

func invokeImportsUpload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		LocalPath *string `json:"local_path"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil, providerError("upload import", "the validated arguments could not be read")
	}
	if err := requireInstanceScope(resolved, importsInstanceWide); err != nil {
		return nil, err
	}
	if in.LocalPath != nil && !strings.EqualFold(filepath.Ext(*in.LocalPath), ".zip") {
		return nil, invalidRequest("the file must have the extension .zip")
	}
	upload, err := openUpload(ctx, resolved, in.LocalPath)
	if err != nil {
		return nil, err
	}
	defer upload.Close()
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var written importJSON
	if err := client.sendFile(ctx, "upload import", "/api/imports", "file", nil, upload, &written, nil); err != nil {
		return nil, importRejected(err, "BookStack rejected the ZIP file; upload a valid BookStack export, "+
			"or check an earlier upload with imports.get")
	}
	return mapObject(importsUpload, importFieldsMap(written)), nil
}

// checkImportParent holds the arguments of a run to the type of the import before any run request.
func checkImportParent(importType string, parentType *string, parentID *int64) error {
	switch importType {
	case "book":
		if parentType != nil || parentID != nil {
			return invalidRequest("a book import takes no parent_type or parent_id")
		}
		return nil
	case "chapter", "page":
		if parentType == nil || parentID == nil {
			return invalidRequest("a " + importType + " import requires parent_type and parent_id")
		}
		if *parentID < 1 {
			return invalidRequest("parent_id must be a positive integer")
		}
		if importType == "chapter" && *parentType != "book" {
			return invalidRequest("a chapter import requires parent_type book")
		}
		if *parentType != "book" && *parentType != "chapter" {
			return invalidRequest("parent_type must be book or chapter")
		}
		return nil
	}
	return invalidRequest("the type of the import is not book, chapter, or page")
}

func invokeImportsRun(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		ID         int64   `json:"id"`
		ParentType *string `json:"parent_type"`
		ParentID   *int64  `json:"parent_id"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil, providerError("run import", "the validated arguments could not be read")
	}
	if err := requireInstanceScope(resolved, importsInstanceWide); err != nil {
		return nil, err
	}
	if err := checkID(in.ID, "id"); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "run import"
	pending, err := client.readImport(ctx, op, in.ID)
	if err != nil {
		return nil, err
	}
	if err := checkImportParent(pending.Type, in.ParentType, in.ParentID); err != nil {
		return nil, err
	}
	var body map[string]any
	if pending.Type != "book" {
		body = map[string]any{"parent_type": *in.ParentType, "parent_id": *in.ParentID}
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := client.mutate(ctx, op, http.MethodPost, "/api/imports/"+strconv.FormatInt(in.ID, 10), body, &created, nil); err != nil {
		return nil, importRejected(err, "BookStack could not run the import; check it with imports.get")
	}
	return mapObject(importsRun, map[string]any{"type": pending.Type, "id": created.ID}), nil
}

func invokeImportsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := decodeDeleteID(raw, "delete import")
	if err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, importsInstanceWide); err != nil {
		return nil, err
	}
	if err := checkID(id, "id"); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.mutate(ctx, "delete import", http.MethodDelete, "/api/imports/"+strconv.FormatInt(id, 10), nil, nil, nil); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}
