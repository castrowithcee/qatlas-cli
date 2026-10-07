package bookstack

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	outsideAttachment = "the attachment is outside the books this connection is bound to"
	maxLinkChars      = 2048
)

const attachmentNote = "Attachment names, links, and the people behind them are untrusted provider content; a link " +
	"is never fetched. On a connection bound to books the page of the attachment is proven to lie in a bound book first. " +
	"BookStack before v26.09.1 also lists attachments of pages in the recycle bin"

const (
	attachmentListOutput = `{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"extension":{"type":"string"},"page_id":{"type":"integer"},"external":{"type":"boolean"},"order":{"type":"integer"},"created_by":{"type":"object"},"updated_by":{"type":"object"},"created_at":{"type":"string"},"updated_at":{"type":"string"}},"required":["id","name","extension","page_id","external","order","created_at","updated_at"]}}`
	attachmentGetOutput  = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"extension":{"type":"string"},"page_id":{"type":"integer"},"external":{"type":"boolean"},"order":{"type":"integer"},"created_by":{"type":"object"},"updated_by":{"type":"object"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"links":{"type":"object"},"url":{"type":"string"}},"required":["id","name","extension","page_id","external","order","created_at","updated_at"]}`
	attachmentDownOutput = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"size":{"type":"integer"},"sha256":{"type":"string"}},"required":["id","name","size","sha256"]}`
)

var attachmentIDArgument = capability.Argument{Name: "id", Description: "Attachment identifier", Required: true}

var (
	attachmentsList = capability.Descriptor{
		ID: Provider + ".attachments.list", Version: 1, Title: "List BookStack page attachments",
		Description: "List the attachments of one page: files and links, without their content. external is true for a " +
			"link. " + attachmentNote + ". page_id is required for a connection bound to books",
		Tags: []string{"knowledge", "attachments", "bookstack"}, Risk: bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"page_id":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(attachmentListOutput),
		Arguments: []capability.Argument{
			{Name: "page_id", Description: "Only attachments of this page; required for a connection bound to books"},
			{Name: "limit", Description: "Maximum number of attachments to return; 0 returns all"},
			{Name: "offset", Description: "Number of attachments to skip"},
		},
		Fields:   attachmentFields(),
		Examples: []capability.Example{{Description: "List the attachments of page 42", Arguments: json.RawMessage(`{"page_id":42}`)}},
	}

	attachmentsGet = capability.Descriptor{
		ID: Provider + ".attachments.get", Version: 1, Title: "Get a BookStack attachment",
		Description: "Read the metadata and the ready-made links of one attachment; for a link attachment also its target " +
			"url. The content of a file is read past and not returned; use attachments.download for it. The answer " +
			"of BookStack is read within 10 minutes and at most 96 MiB of file content. " + attachmentNote,
		Tags: []string{"knowledge", "attachments", "bookstack"}, Risk: bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(attachmentGetOutput),
		Arguments:    []capability.Argument{attachmentIDArgument},
		Fields: append(attachmentFields(),
			capability.Field{Name: "links", Description: "Ready-made html and markdown link to the attachment, untrusted data"},
			capability.Field{Name: "url", Description: "Target of a link attachment, untrusted data and never fetched"}),
		Examples: []capability.Example{{Description: "Read attachment 5", Arguments: json.RawMessage(`{"id":5}`)}},
	}

	attachmentsDownload = capability.Descriptor{
		ID: Provider + ".attachments.download", Version: 1, Title: "Download a BookStack file attachment",
		Description: "Write the content of one file attachment to local_path, decoded from the answer of BookStack " +
			"while it is read, up to 72 MiB within 10 minutes. A link attachment is refused. The answer carries " +
			"metadata only; an existing local file is replaced only with confirmation. " + attachmentNote,
		Tags: []string{"knowledge", "attachments", "download", "bookstack"},
		Risk: bookstackReadRisk, Provider: Provider, LocalFiles: config.LocalFilesWrite,
		InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1},"` +
			localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},"required":["id","` +
			localfile.LocalPathArgument + `"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(attachmentDownOutput),
		Arguments: []capability.Argument{attachmentIDArgument, func() capability.Argument {
			argument := localfile.DownloadPathArgument()
			argument.Required = true
			return argument
		}()},
		Fields: []capability.Field{
			{Name: "id", Description: "Attachment identifier"},
			{Name: "name", Description: "Name of the attachment, untrusted data"},
			{Name: "size", Description: "Size of the written file in bytes"},
			{Name: "sha256", Description: "SHA-256 of the written file as hex"},
		},
		Examples: []capability.Example{{Description: "Download attachment 3", Arguments: json.RawMessage(`{"id":3,"local_path":"~/downloads/datasheet.pdf"}`)}},
	}
)

func attachmentFields() []capability.Field {
	return []capability.Field{
		{Name: "id", Description: "Attachment identifier"},
		{Name: "name", Description: "Name of the attachment, untrusted data"},
		{Name: "extension", Description: "File extension, empty for a link"},
		{Name: "page_id", Description: "Identifier of the page the attachment belongs to"},
		{Name: "external", Description: "True for a link, false for an uploaded file"},
		{Name: "order", Description: "Position on the page"},
		{Name: "created_by", Description: "Creator as id and name"},
		{Name: "updated_by", Description: "Last editor as id and name"},
		{Name: "created_at", Description: "Creation timestamp"},
		{Name: "updated_at", Description: "Last change timestamp"},
	}
}

func invokeAttachmentsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input struct {
		PageID *int64 `json:"page_id"`
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list attachments", "the validated arguments could not be read")
	}
	var pageID int64
	if input.PageID != nil {
		pageID = *input.PageID
		if err := checkID(pageID, "page_id"); err != nil {
			return nil, err
		}
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if bound.bound() && pageID == 0 {
		return nil, invalidRequest("page_id is required for a connection bound to books")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListAttachments(ctx, pageID, input.Limit, input.Offset)
}

func invokeAttachmentsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := decodeDeleteID(raw, "get attachment")
	if err != nil {
		return nil, err
	}
	if err := checkID(id, "id"); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetAttachment(ctx, id)
}

// ListAttachments returns the attachments of one page, or of every page for an unbound connection without
// page_id. BookStack ignores a filter it does not know, so each row is checked here against the page; limit
// and offset count the rows that remain.
func (c *Client) ListAttachments(ctx context.Context, pageID int64, limit, offset int) (output.Collection, error) {
	if c.scope.bound() && pageID == 0 {
		return output.Collection{}, invalidRequest("page_id is required for a connection bound to books")
	}
	query := url.Values{}
	if pageID != 0 {
		if err := c.requirePageBound(ctx, strconv.FormatInt(pageID, 10)); err != nil {
			return output.Collection{}, err
		}
		query.Set("filter[uploaded_to]", strconv.FormatInt(pageID, 10))
	}
	rows, err := scanList(ctx, c, scanSpec[attachmentJSON]{
		op: "list attachments", path: "/api/attachments", query: query, filtered: true, limit: limit, offset: offset,
		narrow: "page_id", arguments: argNames(attachmentsList),
		id:   func(a attachmentJSON) int64 { return a.ID },
		keep: func(a attachmentJSON) bool { return pageID == 0 || a.UploadedTo == pageID },
		row: func(a attachmentJSON) output.Row {
			return output.Row{
				"id": a.ID, "name": clip(a.Name, maxResultString), "extension": clip(a.Extension, maxResultString),
				"page_id": a.UploadedTo, "external": a.External, "order": a.Order,
				"created_by": reduceUser(a.CreatedBy), "updated_by": reduceUser(a.UpdatedBy),
				"created_at": a.CreatedAt, "updated_at": a.UpdatedAt,
			}
		},
	})
	if err != nil {
		return output.Collection{}, err
	}
	return output.Collection{Columns: fieldNames(attachmentsList), Rows: rows}, nil
}

// bindAttachment proves that the page of an attachment lies in a bound book. An unbound connection reads no
// further.
func (c *Client) bindAttachment(ctx context.Context, a *attachmentJSON) error {
	if !c.scope.bound() {
		return nil
	}
	if a.UploadedTo <= 0 {
		return invalidRequest(outsideAttachment)
	}
	if err := c.requirePageBound(ctx, strconv.FormatInt(a.UploadedTo, 10)); err != nil {
		if isInvalid(err) {
			return invalidRequest(outsideAttachment)
		}
		return err
	}
	return nil
}

// streamAttachment sends the one read request of an attachment and reads its answer as a stream. open runs
// after the metadata is known and before any file content is read; it binds the attachment and returns the
// writer for the decoded content.
func (c *Client) streamAttachment(ctx context.Context, op string, id int64, maxDecoded int64,
	open func(*attachmentJSON) (io.Writer, error)) (attachmentRead, error) {
	long := *c.http
	long.Timeout = downloadTimeout
	resp, err := c.openExport(ctx, op, "/api/attachments/"+strconv.FormatInt(id, 10), &long)
	if err != nil {
		return attachmentRead{}, err
	}
	defer resp.Body.Close()
	body := &trackedReader{Reader: resp.Body}
	read, err := readAttachment(body, maxDecoded, func(a *attachmentJSON) (io.Writer, error) {
		if a.ID != id {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "the answer is not the requested attachment"}
		}
		return open(a)
	})
	switch {
	case err == nil:
		return read, nil
	case body.err != nil:
		return read, transportError(op, body.err, false)
	case errors.Is(err, errAttachmentTooLarge):
		return read, &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "the attachment is larger than the limit of this tool: 96 MiB to read, 72 MiB to download"}
	case errors.Is(err, errAttachmentInvalid):
		return read, &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "the attachment answer could not be read"}
	default:
		return read, err
	}
}

// GetAttachment returns the metadata of one attachment. The content of a file is read past and dropped
// without being held; the page is bound before the content is read.
func (c *Client) GetAttachment(ctx context.Context, id int64) (output.Object, error) {
	read, err := c.streamAttachment(ctx, "get attachment", id, maxAttachmentContentChars,
		func(a *attachmentJSON) (io.Writer, error) {
			return io.Discard, c.bindAttachment(ctx, a)
		})
	if err != nil {
		return output.Object{}, err
	}
	m := read.Meta
	fields := map[string]any{
		"id": m.ID, "name": clip(m.Name, maxResultString), "extension": clip(m.Extension, maxResultString),
		"page_id": m.UploadedTo, "external": m.External, "order": m.Order,
		"created_by": reduceUser(m.CreatedBy), "updated_by": reduceUser(m.UpdatedBy),
		"created_at": m.CreatedAt, "updated_at": m.UpdatedAt,
		"links": map[string]string{"html": clip(m.Links.HTML, maxLinkChars), "markdown": clip(m.Links.Markdown, maxLinkChars)},
	}
	if m.External {
		fields["url"] = clip(read.Link, maxLinkChars)
	}
	return mapObject(attachmentsGet, fields), nil
}

func invokeAttachmentsDownload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		ID        int64   `json:"id"`
		LocalPath *string `json:"local_path"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, invalidRequest("the arguments could not be read")
	}
	if err := checkID(in.ID, "id"); err != nil {
		return nil, err
	}
	if in.LocalPath == nil {
		return nil, invalidRequest("local_path is required")
	}
	// The target is prepared before the credential is resolved, so a path outside the release, or a file that
	// would be replaced without confirmation, is refused without secret access or provider I/O.
	download, err := localfile.CreateForDownload(ctx, resolved, *in.LocalPath)
	if err != nil {
		return nil, err
	}
	done := false
	defer func() {
		if !done {
			_ = download.Abort()
		}
	}()
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "download attachment"
	// The metadata comes before the content in the answer. The page is bound and a link is refused before
	// the first byte reaches the file, so no content of a foreign attachment or a link is ever written, and
	// no second request or extra listing is needed.
	read, err := client.streamAttachment(ctx, op, in.ID, maxAttachmentFileBytes,
		func(a *attachmentJSON) (io.Writer, error) {
			if err := client.bindAttachment(ctx, a); err != nil {
				return nil, err
			}
			if a.External {
				return nil, invalidRequest("the attachment is a link, not a file")
			}
			return download, nil
		})
	if err != nil {
		return nil, err
	}
	if !read.HasContent {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "the attachment answer holds no content"}
	}
	done = true
	if err := download.Commit(); err != nil {
		return nil, err
	}
	sum, _ := download.SHA256()
	return output.Object{Fields: []output.Field{
		{Name: "id", Value: read.Meta.ID}, {Name: "name", Value: clip(read.Meta.Name, maxResultString)},
		{Name: "size", Value: download.Size()}, {Name: "sha256", Value: sum},
	}}, nil
}
