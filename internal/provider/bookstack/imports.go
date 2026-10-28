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
	"the user of the token, and importing a book creates a new book, so a connection with targets cannot use " +
	"this tool"

const (
	// maxImportDetailNames bounds the chapter names shown of an import.
	maxImportDetailNames = 25
	// maxImportDetailDepth bounds how deep the details of an import are read.
	maxImportDetailDepth = 4
	// maxImportContentEntries bounds the chapters and pages listed in the contents of an import together.
	maxImportContentEntries = 2000
)

const importOutputProperties = `"id":{"type":"integer"},"name":{"type":"string"},"size":{"type":"integer"},"type":{"type":"string"},"created_by":{"type":"integer"},"created_at":{"type":"string"},"updated_at":{"type":"string"}`

const importEntrySchema = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"priority":{"type":"integer"}}}`

const importDetailsSchema = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"chapters":{"type":"integer"},"pages":{"type":"integer"},"attachments":{"type":"integer"},"images":{"type":"integer"},"tags":{"type":"integer"},"chapter_names":{"type":"array","items":{"type":"string"}},` +
	`"contents":{"type":"object","properties":{"chapters":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"priority":{"type":"integer"},"pages":{"type":"array","items":` + importEntrySchema + `}}}},"pages":{"type":"array","items":` + importEntrySchema + `}}},` +
	`"contents_truncated":{"type":"boolean"}}}`

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
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0},` + importListSchema.properties() + `},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"array","items":{"type":"object","properties":{` + importOutputProperties + `},"required":["id","name","size","type"]}}`),
		Arguments: append([]capability.Argument{
			{Name: "limit", Description: "Maximum number of imports to return; 0 returns all"},
			{Name: "offset", Description: "Number of imports to skip"},
		}, importListSchema.arguments()...),
		Fields:   importFields,
		Examples: []capability.Example{{Description: "List the first 25 imports", Arguments: json.RawMessage(`{"limit":25}`)}},
	}

	importsGet = capability.Descriptor{
		ID: Provider + ".imports.get", Version: 1, Title: "Show a BookStack ZIP import",
		Description: "Read one uploaded import with the contents BookStack detected in the ZIP. The details are " +
			"untrusted data and shown reduced: the name, the number of chapters, pages, attachments, images, and " +
			"tags, and at most 25 chapter names, plus the chapters with their pages and the top-level pages (id, name, " +
			"priority; at most 2000 entries together). The storage path is never shown. " + importsRoles,
		Tags: []string{"knowledge", "imports", "bookstack"},
		Risk: bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(importIDSchema),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{` + importOutputProperties + `,"details":` + importDetailsSchema + `},"required":["id","name","size","type"]}`),
		Arguments:    []capability.Argument{importIDArgument},
		Fields: append(append([]capability.Field{}, importFields...),
			capability.Field{Name: "details", Description: "Reduced contents of the ZIP, untrusted data: id, name, chapters, pages, attachments, images, tags (the counts), chapter_names, contents (chapters with their pages, and top-level pages), contents_truncated"}),
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
	ID          json.RawMessage `json:"id"`
	Priority    json.RawMessage `json:"priority"`
	Name        string          `json:"name"`
	Chapters    []importNode    `json:"chapters"`
	Pages       []importNode    `json:"pages"`
	Attachments []any           `json:"attachments"`
	Images      []any           `json:"images"`
	Tags        []any           `json:"tags"`
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
	out := map[string]any{
		"name": clip(root.Name, maxResultString), "chapters": chapters, "pages": pages,
		"attachments": attachments, "images": images, "tags": tags, "chapter_names": names,
	}
	if id, ok := importInt(root.ID); ok {
		out["id"] = id
	}
	contents, cut := importContents(root)
	if contents != nil {
		out["contents"] = contents
	}
	if cut {
		out["contents_truncated"] = true
	}
	return out
}

// importInt reads a JSON integer without a floating-point detour; anything else is absent.
func importInt(raw json.RawMessage) (int64, bool) {
	n, err := strconv.ParseInt(string(raw), 10, 64)
	return n, err == nil
}

// importEntry is the shown form of a chapter or page of an import; absent values stay out.
func importEntry(n importNode) map[string]any {
	e := map[string]any{"name": clip(n.Name, maxResultString)}
	if id, ok := importInt(n.ID); ok {
		e["id"] = id
	}
	if p, ok := importInt(n.Priority); ok {
		e["priority"] = p
	}
	return e
}

// importContents lists the chapters with their pages and the top-level pages. The structure is fixed at those
// two levels; chapters and pages share one budget, and the second result reports that it cut the lists.
func importContents(root importNode) (map[string]any, bool) {
	budget := maxImportContentEntries
	cut := false
	var chapters, pages []map[string]any
	for _, c := range root.Chapters {
		if budget == 0 {
			cut = true
			break
		}
		budget--
		entry := importEntry(c)
		var inner []map[string]any
		for _, p := range c.Pages {
			if budget == 0 {
				cut = true
				break
			}
			budget--
			inner = append(inner, importEntry(p))
		}
		if len(inner) > 0 {
			entry["pages"] = inner
		}
		chapters = append(chapters, entry)
	}
	for _, p := range root.Pages {
		if budget == 0 {
			cut = true
			break
		}
		budget--
		pages = append(pages, importEntry(p))
	}
	if len(chapters) == 0 && len(pages) == 0 {
		return nil, cut
	}
	out := map[string]any{}
	if len(chapters) > 0 {
		out["chapters"] = chapters
	}
	if len(pages) > 0 {
		out["pages"] = pages
	}
	return out, cut
}

func invokeImportsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in pagingInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, importsInstanceWide); err != nil {
		return nil, err
	}
	query, err := parseListQuery(importListSchema, in.Sort, in.Filters)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListImports(ctx, in.Limit, in.Offset, query)
}

// ListImports returns the reduced pending imports. BookStack ignores a filter it does not know, so each row is
// checked here; limit and offset count the rows that remain.
func (c *Client) ListImports(ctx context.Context, limit, offset int, q listQuery) (output.Collection, error) {
	spec := scanSpec[importJSON]{
		op: "list imports", path: "/api/imports", limit: limit, offset: offset,
		arguments: argNames(importsList),
		id:        func(i importJSON) int64 { return i.ID },
		keep:      func(importJSON) bool { return true },
		row:       func(i importJSON) output.Row { return output.Row(importFieldsMap(i)) },
	}
	spec.withListQuery(q, importValue)
	rows, err := scanList(ctx, c, spec)
	if err != nil {
		return output.Collection{}, err
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

// importRejected replaces the 422 message by a fixed one; the validation texts of BookStack stay in Original.
func importRejected(err error, message string) error {
	var perr *provider.Error
	if errors.As(err, &perr) && strings.Contains(perr.Message, "HTTP 422") {
		return &provider.Error{Class: provider.ClassProviderError, Op: perr.Op, Message: message, Original: perr.Original}
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

// checkExcludedParent refuses a parent excluded by its own identifier before any secret or request. The type
// of the import is not known yet, so it only looks at a parent that is given in full.
func checkExcludedParent(resolved *config.Resolved, parentType *string, parentID *int64) error {
	if parentType == nil || parentID == nil || *parentID < 1 {
		return nil
	}
	switch *parentType {
	case "book":
		return boundExcluded(resolved, bookPrefix, *parentID)
	case "chapter":
		return boundExcluded(resolved, chapterPrefix, *parentID)
	}
	return nil
}

// requireImportParent proves the book or chapter an import runs into: not excluded itself, through its
// shelf, or through its book. The check reads the parent, so a parent that cannot be proven ends the call
// before the run request.
func (c *Client) requireImportParent(ctx context.Context, parentType string, parentID int64) error {
	if parentType == "book" {
		return c.requireBook(ctx, parentID)
	}
	_, err := c.chapterIn(ctx, parentID)
	return err
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
	if err := checkExcludedParent(resolved, in.ParentType, in.ParentID); err != nil {
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
		if err := client.requireImportParent(ctx, *in.ParentType, *in.ParentID); err != nil {
			return nil, err
		}
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
