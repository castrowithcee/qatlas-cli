package bookstack

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// maxUploadBytes bounds the file of one attachment upload; it is checked before any secret is resolved.
	maxUploadBytes = 50 << 20
	// uploadTimeout bounds the whole transfer of one upload.
	uploadTimeout = 30 * time.Minute
	// maxAttachmentNameChars and maxAttachmentLinkChars are the limits BookStack applies.
	maxAttachmentNameChars = 255
	maxAttachmentLinkChars = 2000
)

const notAFile = "the attachment is a link, not a file"

const attachmentWriteOutput = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"extension":{"type":"string"},"page_id":{"type":"integer"},"external":{"type":"boolean"},"order":{"type":"integer"},"created_at":{"type":"string"},"updated_at":{"type":"string"}},"required":["id","name","page_id","external"]}`

const attachmentBindNote = "On a connection bound to books the page is proven to lie in a bound book first, " +
	"and so is the page of an existing attachment"

var attachmentWriteFields = []capability.Field{
	{Name: "id", Description: "Attachment identifier"},
	{Name: "name", Description: "Name of the attachment, untrusted data"},
	{Name: "extension", Description: "File extension, empty for a link"},
	{Name: "page_id", Description: "Identifier of the page the attachment belongs to"},
	{Name: "external", Description: "True for a link, false for an uploaded file"},
	{Name: "order", Description: "Position on the page"},
	{Name: "created_at", Description: "Creation timestamp"},
	{Name: "updated_at", Description: "Last change timestamp"},
}

var (
	attachmentNameArgument = capability.Argument{Name: "name", Description: "Attachment name, 1 to 255 characters", Required: true}
	attachmentPageArgument = capability.Argument{Name: "page_id", Description: "Page the attachment belongs to", Required: true}

	attachmentsLink = capability.Descriptor{
		ID: Provider + ".attachments.link", Version: 1, Title: "Attach a link to a BookStack page",
		Description: "Add a link attachment to one page. The link must be an http or https URL of 1 to 2000 " +
			"characters without user information; Qatlas never fetches it. Not idempotent: a repeated call adds " +
			"another attachment. " + attachmentBindNote,
		Tags: []string{"knowledge", "attachments", "bookstack", "create"}, Provider: Provider,
		Risk:         bookWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"page_id":{"type":"integer","minimum":1},"name":{"type":"string","minLength":1,"maxLength":255},"link":{"type":"string","minLength":1,"maxLength":2000}},"required":["page_id","name","link"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(attachmentWriteOutput),
		Arguments: []capability.Argument{attachmentPageArgument, attachmentNameArgument,
			{Name: "link", Description: "http or https URL without user information, 1 to 2000 characters", Required: true}},
		Fields: attachmentWriteFields,
	}

	attachmentsUpload = capability.Descriptor{
		ID: Provider + ".attachments.upload", Version: 1, Title: "Upload a file attachment to a BookStack page",
		Description: "Upload one local file of at most 50 MiB as an attachment of a page, streamed from disk " +
			"from a directory the connection releases for reading. Not idempotent: a repeated call adds another " +
			"attachment. " + attachmentBindNote,
		Tags: []string{"knowledge", "attachments", "upload", "bookstack", "create"}, Provider: Provider,
		LocalFiles:   config.LocalFilesRead,
		Risk:         bookWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"page_id":{"type":"integer","minimum":1},"name":{"type":"string","minLength":1,"maxLength":255},"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},"required":["page_id","name","` + localfile.LocalPathArgument + `"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(attachmentWriteOutput),
		Arguments: []capability.Argument{attachmentPageArgument, attachmentNameArgument, {
			Name: localfile.LocalPathArgument, Required: true,
			Description: "Local file to upload, at most 50 MiB, absolute or starting with ~/, inside a directory " +
				"the connection releases for reading"}},
		Fields: attachmentWriteFields,
	}

	attachmentsUpdate = capability.Descriptor{
		ID: Provider + ".attachments.update", Version: 1, Title: "Update a BookStack attachment",
		Description: "Rename an attachment, change the target of a link attachment (BookStack refuses link " +
			"for a file), or move it to another page with page_id. Moving needs update permission on both pages " +
			"in BookStack. " + attachmentBindNote + "; for a move the target page too",
		Tags: []string{"knowledge", "attachments", "bookstack", "update"}, Provider: Provider,
		Risk:         bookWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1},"name":{"type":"string","minLength":1,"maxLength":255},"link":{"type":"string","minLength":1,"maxLength":2000},"page_id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(attachmentWriteOutput),
		Arguments: []capability.Argument{attachmentIDArgument,
			{Name: "name", Description: "New attachment name, 1 to 255 characters"},
			{Name: "link", Description: "New http or https URL of a link attachment, without user information, 1 to 2000 characters; BookStack refuses it for a file"},
			{Name: "page_id", Description: "Move the attachment to this page"}},
		Fields: attachmentWriteFields,
	}

	attachmentsReplace = capability.Descriptor{
		ID: Provider + ".attachments.replace", Version: 1, Title: "Replace the file of a BookStack attachment",
		Description: "Replace the file of an existing file attachment with one local file of at most 50 MiB, " +
			"streamed from disk from a directory the connection releases for reading; the name stays. A link " +
			"attachment is refused after one metadata read, also on a connection without books. The previous " +
			"file is gone afterwards. Not idempotent. " + attachmentBindNote,
		Tags: []string{"knowledge", "attachments", "upload", "bookstack", "update"}, Provider: Provider,
		LocalFiles:   config.LocalFilesRead,
		Risk:         bookWriteRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent),
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1},"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},"required":["id","` + localfile.LocalPathArgument + `"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(attachmentWriteOutput),
		Arguments: []capability.Argument{attachmentIDArgument, {
			Name: localfile.LocalPathArgument, Required: true,
			Description: "Local file to upload, at most 50 MiB, absolute or starting with ~/, inside a directory " +
				"the connection releases for reading"}},
		Fields: attachmentWriteFields,
	}

	attachmentsDelete = capability.Descriptor{
		ID: Provider + ".attachments.delete", Version: 1, Title: "Delete a BookStack attachment",
		Description: "Delete one attachment by identifier. Attachments are not kept in the recycle bin: the " +
			"deletion is final. " + attachmentBindNote,
		Tags: []string{"knowledge", "attachments", "bookstack", "delete"}, Provider: Provider,
		RequiresToolAllowList: true,
		Risk:                  bookWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
		InputSchema:           booksDelete.InputSchema,
		OutputSchema:          booksDelete.OutputSchema,
		Arguments:             []capability.Argument{attachmentIDArgument},
	}
)

// attachmentWriteJSON is the part of a change answer this provider shows.
type attachmentWriteJSON struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Extension  string `json:"extension"`
	UploadedTo int64  `json:"uploaded_to"`
	External   bool   `json:"external"`
	Order      int64  `json:"order"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

func attachmentWriteObject(a attachmentWriteJSON) output.Object {
	return mapObject(attachmentsLink, map[string]any{
		"id": a.ID, "name": clip(a.Name, maxResultString), "extension": clip(a.Extension, maxResultString),
		"page_id": a.UploadedTo, "external": a.External, "order": a.Order,
		"created_at": a.CreatedAt, "updated_at": a.UpdatedAt,
	})
}

// checkAttachmentName applies the name rule of BookStack before any secret.
func checkAttachmentName(name string) error {
	if n := utf8.RuneCountInString(name); n < 1 || n > maxAttachmentNameChars {
		return invalidRequest("name must be 1 to 255 characters")
	}
	return nil
}

// checkAttachmentLink accepts an http or https URL without user information. The refusal does not quote it,
// and the URL is never requested.
func checkAttachmentLink(link string) error {
	const form = "link must be an http or https URL of 1 to 2000 characters without user information"
	if n := utf8.RuneCountInString(link); n < 1 || n > maxAttachmentLinkChars {
		return invalidRequest(form)
	}
	parsed, err := url.Parse(link)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.Opaque != "" || strings.ContainsAny(link, " \t\r\n") {
		return invalidRequest(form)
	}
	return nil
}

func invokeAttachmentsLink(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		PageID int64  `json:"page_id"`
		Name   string `json:"name"`
		Link   string `json:"link"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil, providerError("link attachment", "the validated arguments could not be read")
	}
	if err := checkID(in.PageID, "page_id"); err != nil {
		return nil, err
	}
	if err := checkAttachmentName(in.Name); err != nil {
		return nil, err
	}
	if err := checkAttachmentLink(in.Link); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.requirePageBound(ctx, strconv.FormatInt(in.PageID, 10)); err != nil {
		return nil, err
	}
	body := map[string]any{"name": in.Name, "uploaded_to": in.PageID, "link": in.Link}
	var written attachmentWriteJSON
	if err := client.mutate(ctx, "link attachment", http.MethodPost, "/api/attachments", body, &written, argNames(attachmentsLink)); err != nil {
		return nil, err
	}
	return attachmentWriteObject(written), nil
}

// openUpload opens the local file before any secret is resolved and holds it to the size limit.
func openUpload(ctx context.Context, resolved *config.Resolved, path *string) (*localfile.Upload, error) {
	if path == nil {
		return nil, invalidRequest("local_path is required")
	}
	upload, err := localfile.OpenForUpload(ctx, resolved, *path)
	if err != nil {
		return nil, err
	}
	if upload.Size > maxUploadBytes {
		_ = upload.Close()
		return nil, invalidRequest("the file is larger than the limit of 50 MiB")
	}
	return upload, nil
}

func invokeAttachmentsUpload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		PageID    int64   `json:"page_id"`
		Name      string  `json:"name"`
		LocalPath *string `json:"local_path"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil, providerError("upload attachment", "the validated arguments could not be read")
	}
	if err := checkID(in.PageID, "page_id"); err != nil {
		return nil, err
	}
	if err := checkAttachmentName(in.Name); err != nil {
		return nil, err
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
	if err := client.requirePageBound(ctx, strconv.FormatInt(in.PageID, 10)); err != nil {
		return nil, err
	}
	fields := [][2]string{{"name", in.Name}, {"uploaded_to", strconv.FormatInt(in.PageID, 10)}}
	var written attachmentWriteJSON
	if err := client.sendFile(ctx, "upload attachment", "/api/attachments", fields, upload, &written, argNames(attachmentsUpload)); err != nil {
		return nil, err
	}
	return attachmentWriteObject(written), nil
}

// sendFile sends the one multipart upload request. The body is framed by hand, so its length is known and
// the file streams from disk without being held in memory; the transfer has its own long timeout.
func (c *Client) sendFile(ctx context.Context, op, path string, fields [][2]string, upload *localfile.Upload,
	out any, arguments []string) error {
	var head bytes.Buffer
	form := multipart.NewWriter(&head)
	for _, f := range fields {
		if err := form.WriteField(f[0], f[1]); err != nil {
			return providerError(op, "the request could not be built")
		}
	}
	if _, err := form.CreateFormFile("file", upload.Name); err != nil {
		return providerError(op, "the request could not be built")
	}
	tail := "\r\n--" + form.Boundary() + "--\r\n"
	body := io.MultiReader(bytes.NewReader(head.Bytes()), io.LimitReader(upload, upload.Size), strings.NewReader(tail))
	length := int64(head.Len()) + upload.Size + int64(len(tail))
	long := *c.http
	long.Timeout = uploadTimeout
	return c.send(ctx, &long, op, http.MethodPost, path, body, length, form.FormDataContentType(), out, arguments)
}

// attachmentMeta is what binding needs of an existing attachment.
type attachmentMeta struct {
	ID         int64 `json:"id"`
	UploadedTo int64 `json:"uploaded_to"`
	External   bool  `json:"external"`
}

// readAttachmentMeta reads the metadata of one attachment through the listing filtered by id, which carries
// no file content: one small request instead of an answer with up to 96 MiB of base64. BookStack ignores a
// filter it does not know, so the rows are searched for the id; a missing row is a not-found answer.
func (c *Client) readAttachmentMeta(ctx context.Context, op string, id int64) (attachmentMeta, error) {
	var answer struct {
		Data []attachmentMeta `json:"data"`
	}
	query := url.Values{"filter[id]": {strconv.FormatInt(id, 10)}}
	if err := c.get(ctx, op, "/api/attachments", query, &answer, nil, provider.ClassPermission); err != nil {
		return attachmentMeta{}, err
	}
	for _, row := range answer.Data {
		if row.ID == id {
			return row, nil
		}
	}
	return attachmentMeta{}, &provider.Error{Class: provider.ClassNotFound, Op: op,
		Message: "BookStack does not hold this resource or does not show it to this token"}
}

// requireAttachmentBound proves the page of an existing attachment on a bound connection. An unbound
// connection reads nothing, unless needFile asks for the type check of a file.
func (c *Client) requireAttachmentBound(ctx context.Context, op string, id int64, needFile bool) error {
	if !c.scope.bound() && !needFile {
		return nil
	}
	meta, err := c.readAttachmentMeta(ctx, op, id)
	if err != nil {
		return err
	}
	if c.scope.bound() {
		if err := c.bindAttachment(ctx, &attachmentJSON{UploadedTo: meta.UploadedTo}); err != nil {
			return err
		}
	}
	if needFile && meta.External {
		return invalidRequest(notAFile)
	}
	return nil
}

func invokeAttachmentsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		ID     int64   `json:"id"`
		Name   *string `json:"name"`
		Link   *string `json:"link"`
		PageID *int64  `json:"page_id"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil, providerError("update attachment", "the validated arguments could not be read")
	}
	if err := checkID(in.ID, "id"); err != nil {
		return nil, err
	}
	if in.Name == nil && in.Link == nil && in.PageID == nil {
		return nil, invalidRequest("at least one field to change is required")
	}
	body := map[string]any{}
	if in.Name != nil {
		if err := checkAttachmentName(*in.Name); err != nil {
			return nil, err
		}
		body["name"] = *in.Name
	}
	if in.Link != nil {
		if err := checkAttachmentLink(*in.Link); err != nil {
			return nil, err
		}
		body["link"] = *in.Link
	}
	if in.PageID != nil {
		if err := checkID(*in.PageID, "page_id"); err != nil {
			return nil, err
		}
		body["uploaded_to"] = *in.PageID
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "update attachment"
	if err := client.requireAttachmentBound(ctx, op, in.ID, false); err != nil {
		return nil, err
	}
	if in.PageID != nil {
		if err := client.requirePageBound(ctx, strconv.FormatInt(*in.PageID, 10)); err != nil {
			return nil, err
		}
	}
	var written attachmentWriteJSON
	if err := client.mutate(ctx, op, http.MethodPut, "/api/attachments/"+strconv.FormatInt(in.ID, 10), body, &written, argNames(attachmentsUpdate)); err != nil {
		return nil, err
	}
	return attachmentWriteObject(written), nil
}

func invokeAttachmentsReplace(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		ID        int64   `json:"id"`
		LocalPath *string `json:"local_path"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return nil, providerError("replace attachment", "the validated arguments could not be read")
	}
	if err := checkID(in.ID, "id"); err != nil {
		return nil, err
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
	const op = "replace attachment"
	if err := client.requireAttachmentBound(ctx, op, in.ID, true); err != nil {
		return nil, err
	}
	// BookStack takes a multipart body only on POST; _method makes it the update of the attachment.
	var written attachmentWriteJSON
	if err := client.sendFile(ctx, op, "/api/attachments/"+strconv.FormatInt(in.ID, 10),
		[][2]string{{"_method", "PUT"}}, upload, &written, argNames(attachmentsReplace)); err != nil {
		return nil, err
	}
	return attachmentWriteObject(written), nil
}

func invokeAttachmentsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := decodeDeleteID(raw, "delete attachment")
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
	const op = "delete attachment"
	if err := client.requireAttachmentBound(ctx, op, id, false); err != nil {
		return nil, err
	}
	if err := client.mutate(ctx, op, http.MethodDelete, "/api/attachments/"+strconv.FormatInt(id, 10), nil, nil, argNames(attachmentsDelete)); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}
