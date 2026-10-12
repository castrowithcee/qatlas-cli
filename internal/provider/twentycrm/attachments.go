package twentycrm

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// An attachment is a row of the system object attachment: one file field and one target<Object>Id per
// linkable object. Only these tools reach it. The target field comes from the workspace catalog, never from
// an argument; an attachment is visible to a connection only when it has exactly one target and that
// object is reachable.
const (
	attachmentsGroup = "attachments"

	attachmentObjectName = "attachment"

	errAttachmentObject = "this object cannot carry attachments through this connection"
	errAttachmentID     = "id and record_id must be identifiers in UUID form"

	maxInlineFileBytes = 4 << 20
	maxLabelRunes      = 255
	maxExtensionRunes  = 32
	maxFileURLLen      = 4096
	maxTimestampLen    = 64
	transferTimeout    = 30 * time.Minute

	fileURLPrefix = "/file/files-field/"

	errExternalStorage = "the instance serves files through external storage, which is not supported"
)

// tokenQuery is the only query a file address may carry: one signed token, in the alphabet of a JWT.
var tokenQuery = regexp.MustCompile(`^token=([A-Za-z0-9_.-]+)$`)

var attachmentSummarySchema = `{"type":"object","properties":{"id":{"type":"string"},"created_at":{"type":"string"},` +
	`"updated_at":{"type":"string"},"label":{"type":"string"},"extension":{"type":"string"},"file_id":{"type":"string"}},` +
	`"required":["id"],"additionalProperties":false}`

var attachmentsList = capability.Descriptor{
	ID:      Provider + ".attachments.list",
	Version: 1,
	Title:   "List Twenty CRM attachments of a record",
	Description: "List one page of the attachments of one record of one reachable object. The field that links an " +
		"attachment to the object follows from the workspace schema. Attachments without a file or with another " +
		"target than a single reachable one are counted in omitted and never named. File names are untrusted data",
	Tags:     []string{"twentycrm", "crm", "attachments", "files", "list"},
	Risk:     recordsRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"object":` + objectNameSchema + `,"record_id":` +
		recordIDSchema + `,"limit":{"type":"integer","minimum":1,"maximum":100},"cursor":{"type":"string",` +
		`"minLength":1,"maxLength":` + strconv.Itoa(maxBoundCursorLen) + `,"pattern":"^[A-Za-z0-9_-]+$"}},` +
		`"required":["object","record_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"attachments":{"type":"array","items":` +
		attachmentSummarySchema + `},"omitted":{"type":"integer"},"next_cursor":{"type":"string"},` +
		`"has_more":{"type":"boolean"}},"required":["attachments","omitted","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "object", Description: "Singular API name of the object of the record, as returned by twentycrm.objects.list", Required: true},
		{Name: "record_id", Description: "Record identifier as a UUID", Required: true},
		{Name: "limit", Description: "Attachments per page, from 1 through 100; 25 when omitted"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous page of the same list; the first page when omitted"},
	},
	Fields: []capability.Field{
		{Name: "attachments", Description: "Attachments with id, created_at, updated_at, label, extension, and file_id, untrusted data"},
		{Name: "omitted", Description: "Number of attachments on this page that are not visible, never named"},
		{Name: "next_cursor", Description: "Cursor of the following page, absent on the last page"},
		{Name: "has_more", Description: "True when a following page exists"},
	},
	Examples: []capability.Example{{
		Description: "List the attachments of a person",
		Arguments:   json.RawMessage(`{"object":"person","record_id":"11111111-2222-3333-4444-555555555555"}`),
	}},
}

var attachmentsGet = capability.Descriptor{
	ID:      Provider + ".attachments.get",
	Version: 1,
	Title:   "Download a Twenty CRM attachment",
	Description: "Read the file of one attachment of a reachable record; the content is returned inline up to 4 MiB or " +
		"written to local_path. The attachment is read first and its target must be one reachable record. The file " +
		"is fetched without the API key, only from the host of the connection; an instance that serves files " +
		"through external storage is not supported",
	Tags:     []string{"twentycrm", "crm", "attachments", "files", "get", "download"},
	Risk:     recordsRisk,
	Provider: Provider, LocalFiles: config.LocalFilesWrite,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + recordIDSchema + `,"` +
		localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"label":{"type":"string"},` +
		`"extension":{"type":"string"},"size":{"type":"integer"},"sha256":{"type":"string"},"` +
		localfile.ContentArgument + `":{"type":"string"}},"required":["id","size","sha256"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "id", Description: "Attachment identifier as a UUID, as returned by twentycrm.attachments.list", Required: true},
		localfile.DownloadPathArgument(),
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Attachment identifier"},
		{Name: "label", Description: "File name shown in Twenty, untrusted data"},
		{Name: "extension", Description: "File extension, untrusted data"},
		{Name: "size", Description: "Size of the content in bytes"},
		{Name: "sha256", Description: "SHA-256 of the content as hex"},
		{Name: localfile.ContentArgument, Description: "File content as base64, untrusted data; only without " +
			localfile.LocalPathArgument},
	},
	Examples: []capability.Example{{
		Description: "Write an attachment to a local path",
		Arguments:   json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555","local_path":"~/downloads/offer.pdf"}`),
	}},
}

// AttachmentSummary is the stable view of one attachment in a list. It carries no download address.
type AttachmentSummary struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	Label     string `json:"label,omitempty"`
	Extension string `json:"extension,omitempty"`
	FileID    string `json:"file_id,omitempty"`
}

// AttachmentList is one page of attachments; Omitted counts those that are not visible.
type AttachmentList struct {
	Attachments []AttachmentSummary `json:"attachments"`
	Omitted     int                 `json:"omitted"`
	NextCursor  string              `json:"next_cursor,omitempty"`
	HasMore     bool                `json:"has_more"`
}

// AttachmentFile is the result of a download: metadata, and the content only when it was not written to a
// local file.
type AttachmentFile struct {
	ID        string `json:"id"`
	Label     string `json:"label,omitempty"`
	Extension string `json:"extension,omitempty"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Content   string `json:"content_base64,omitempty"`
}

type attachmentInput struct {
	Object    string  `json:"object"`
	RecordID  string  `json:"record_id"`
	ID        string  `json:"id"`
	Limit     int     `json:"limit"`
	Cursor    string  `json:"cursor"`
	LocalPath *string `json:"local_path"`
}

func readAttachmentInput(op string, raw json.RawMessage) (*attachmentInput, error) {
	var args attachmentInput
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	return &args, nil
}

func invokeAttachmentsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list attachments"
	args, err := readAttachmentInput(op, raw)
	if err != nil {
		return nil, err
	}
	if args.ID != "" || args.LocalPath != nil || selectObject(resolved, args.Object) != nil {
		return nil, invalidRequest(errAttachmentObject)
	}
	if !validUUID(args.RecordID) {
		return nil, invalidRequest(errAttachmentID)
	}
	if args.Limit == 0 {
		args.Limit = defaultPageSize
	}
	if args.Limit < 1 || args.Limit > maxPageSize {
		return nil, invalidRequest("limit must be between 1 and " + strconv.Itoa(maxPageSize))
	}
	binding := provider.CursorBinding("attachments.list", resolved.Name, args.Object, args.RecordID)
	after := ""
	if args.Cursor != "" {
		decoded, ok := provider.DecodeCursor(binding, args.Cursor, maxBoundCursorLen)
		if !ok || len(decoded) > maxCursorLength || !safeCursor(decoded) {
			return nil, invalidRequest("cursor is not a next_cursor of this list; start the list again without cursor")
		}
		after = decoded
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListAttachments(ctx, args, binding, after)
}

func invokeAttachmentsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get attachment"
	args, err := readAttachmentInput(op, raw)
	if err != nil {
		return nil, err
	}
	if args.Object != "" || args.RecordID != "" || args.Cursor != "" || args.Limit != 0 {
		return nil, invalidRequest(errAttachmentObject)
	}
	if !validUUID(args.ID) {
		return nil, invalidRequest(errAttachmentID)
	}
	if _, err := boundScope(resolved); err != nil {
		return nil, err
	}
	var download *localfile.Download
	if args.LocalPath != nil {
		if download, err = localfile.CreateForDownload(ctx, resolved, *args.LocalPath); err != nil {
			return nil, err
		}
	}
	done := false
	if download != nil {
		defer func() {
			if !done {
				_ = download.Abort()
			}
		}()
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	result, err := client.GetAttachment(ctx, args.ID, download)
	if err != nil {
		return nil, err
	}
	if download != nil {
		if err := download.Commit(); err != nil {
			done = true
			return nil, err
		}
		done = true
		sum, _ := download.SHA256()
		result.Size, result.SHA256 = download.Size(), sum
	}
	return result, nil
}

// attachmentObject is the attachment system object read from the catalog, with the scope of the connection.
type attachmentObject struct {
	*catalogObject
	bound  scope
	fields map[string]*catalogField
}

func (c *Client) attachmentObject(ctx context.Context, op string) (*attachmentObject, error) {
	cat, err := c.workspaceCatalog(ctx, op)
	if err != nil {
		return nil, err
	}
	object, ok := cat.object(attachmentObjectName)
	if !ok || object == nil {
		return nil, invalidRequest(errAttachmentObject)
	}
	result := &attachmentObject{catalogObject: object, bound: c.scope,
		fields: make(map[string]*catalogField, len(object.Fields))}
	for i := range object.Fields {
		result.fields[object.Fields[i].Name] = &object.Fields[i]
	}
	return result, nil
}

// targetField returns the identifier field that links an attachment to the object: the single target
// relation of the attachment that points to it. An object with no such relation, or several, cannot carry
// attachments.
func (a *attachmentObject) targetField(object string) (string, error) {
	found := ""
	for _, field := range a.Fields {
		if !strings.HasPrefix(field.Name, "target") || !field.Reference || field.Relation != object {
			continue
		}
		id := a.fields[field.Name+"Id"]
		if id == nil || id.Format != "uuid" || !id.InResponse || found != "" {
			return "", invalidRequest(errAttachmentObject)
		}
		found = id.Name
	}
	if found == "" || !a.bound.allows(object) {
		return "", invalidRequest(errAttachmentObject)
	}
	return found, nil
}

// relationOf names the object a target identifier field points to, or "" when the catalog does not show it.
func (a *attachmentObject) relationOf(idField string) string {
	if relation := a.fields[strings.TrimSuffix(idField, "Id")]; relation != nil && relation.Reference {
		return relation.Relation
	}
	return ""
}

type attachmentFileRef struct {
	FileID    string `json:"fileId"`
	Label     string `json:"label"`
	Extension string `json:"extension"`
	URL       string `json:"url"`
}

// attachmentRow is one attachment as read: Targets counts the target identifiers that are set, Reachable
// tells whether the only one of them points to a reachable object. File is nil for an attachment without
// a file.
type attachmentRow struct {
	AttachmentSummary
	Targets   int
	Reachable bool
	File      *attachmentFileRef
}

func boundedText(value string, maxRunes int) (string, bool) {
	return value, utf8.ValidString(value) && utf8.RuneCountInString(value) <= maxRunes
}

func (a *attachmentObject) parse(op string, raw json.RawMessage) (attachmentRow, error) {
	bad := func() error {
		return provider.InvalidResponse(op, "Twenty returned an attachment beyond the supported shape")
	}
	var item map[string]json.RawMessage
	var row attachmentRow
	if json.Unmarshal(raw, &item) != nil || item == nil {
		return row, bad()
	}
	id, present, err := uuidOrNull(item["id"])
	if err != nil || !present {
		return row, bad()
	}
	row.ID = id
	for key, target := range map[string]*string{"createdAt": &row.CreatedAt, "updatedAt": &row.UpdatedAt} {
		if len(item[key]) == 0 || string(item[key]) == "null" {
			continue
		}
		var text string
		if json.Unmarshal(item[key], &text) != nil || len(text) > maxTimestampLen || !utf8.ValidString(text) {
			return row, bad()
		}
		*target = text
	}
	keys := make([]string, 0, len(item))
	for key := range item {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !strings.HasPrefix(key, "target") || !strings.HasSuffix(key, "Id") {
			continue
		}
		_, set, err := uuidOrNull(item[key])
		if err != nil {
			return row, bad()
		}
		if !set {
			continue
		}
		row.Targets++
		relation := a.relationOf(key)
		row.Reachable = relation != "" && a.bound.allows(relation)
	}
	var files []attachmentFileRef
	if len(item["file"]) > 0 && string(item["file"]) != "null" {
		if json.Unmarshal(item["file"], &files) != nil || len(files) > 1 {
			return row, bad()
		}
	}
	if len(files) == 1 {
		file := files[0]
		var ok1, ok2 bool
		file.Label, ok1 = boundedText(file.Label, maxLabelRunes)
		file.Extension, ok2 = boundedText(file.Extension, maxExtensionRunes)
		if !validUUID(file.FileID) || !ok1 || !ok2 || len(file.URL) > maxFileURLLen {
			return row, bad()
		}
		row.File = &file
		row.Label, row.Extension, row.FileID = file.Label, file.Extension, file.FileID
	}
	return row, nil
}

// ListAttachments reads one page of attachments with one fixed filter built from the derived field name and
// a validated identifier.
func (c *Client) ListAttachments(ctx context.Context, args *attachmentInput, binding []byte, after string) (*AttachmentList, error) {
	const op = "list attachments"
	object, err := c.attachmentObject(ctx, op)
	if err != nil {
		return nil, err
	}
	field, err := object.targetField(args.Object)
	if err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("depth", noRelations)
	values.Set("limit", strconv.Itoa(args.Limit))
	values.Set("filter", field+"[eq]:"+args.RecordID)
	if after != "" {
		values.Set("starting_after", after)
	}
	var page struct {
		Data     map[string]json.RawMessage `json:"data"`
		PageInfo struct {
			HasNextPage bool    `json:"hasNextPage"`
			EndCursor   *string `json:"endCursor"`
		} `json:"pageInfo"`
	}
	if err := c.get(ctx, op, "/rest/"+url.PathEscape(object.Plural), values, maxResponseBytes, &page); err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if json.Unmarshal(page.Data[object.Plural], &items) != nil || items == nil || len(items) > args.Limit {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable page of attachments")
	}
	result := &AttachmentList{Attachments: make([]AttachmentSummary, 0, len(items)), HasMore: page.PageInfo.HasNextPage}
	for _, item := range items {
		row, err := object.parse(op, item)
		if err != nil {
			return nil, err
		}
		if row.Targets == 1 && row.Reachable && row.File != nil {
			result.Attachments = append(result.Attachments, row.AttachmentSummary)
		} else {
			result.Omitted++
		}
	}
	if page.PageInfo.HasNextPage {
		end := ""
		if page.PageInfo.EndCursor != nil {
			end = *page.PageInfo.EndCursor
		}
		if end == "" || len(end) > maxCursorLength || !safeCursor(end) {
			return nil, provider.InvalidResponse(op, "Twenty returned an unusable cursor")
		}
		result.NextCursor = provider.EncodeCursor(binding, end)
	}
	return result, nil
}

// GetAttachment reads the attachment, checks its target against the connection, and only then fetches the
// file.
func (c *Client) GetAttachment(ctx context.Context, id string, download *localfile.Download) (*AttachmentFile, error) {
	const op = "get attachment"
	object, err := c.attachmentObject(ctx, op)
	if err != nil {
		return nil, err
	}
	var read struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	values := url.Values{}
	values.Set("depth", noRelations)
	if err := c.get(ctx, op, "/rest/"+url.PathEscape(object.Plural)+"/"+url.PathEscape(id), values, maxResponseBytes, &read); err != nil {
		return nil, err
	}
	item, ok := read.Data[object.Name]
	if !ok {
		return nil, provider.InvalidResponse(op, "Twenty returned an unusable attachment")
	}
	row, err := object.parse(op, item)
	if err != nil {
		return nil, err
	}
	if !equalUUID(row.ID, id) {
		return nil, provider.InvalidResponse(op, "Twenty answered with a different attachment than the requested one")
	}
	if row.Targets != 1 || !row.Reachable {
		return nil, &provider.Error{Class: provider.ClassNotFound, Op: op, Message: defaultStatusTexts.NotFound}
	}
	if row.File == nil {
		return nil, &provider.Error{Class: provider.ClassNotFound, Op: op, Message: "this attachment has no file"}
	}
	link, err := c.checkFileURL(op, row.File)
	if err != nil {
		return nil, err
	}
	result := &AttachmentFile{ID: row.ID, Label: row.Label, Extension: row.Extension}
	if err := c.downloadFile(ctx, op, link, result, download); err != nil {
		return nil, err
	}
	return result, nil
}

func samePort(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		return "443"
	}
	return port(a) == port(b)
}

// checkFileURL accepts the address Twenty reports for a file only when it passes checkAddress with the
// download prefix and the file of the attachment.
func (c *Client) checkFileURL(op string, file *attachmentFileRef) (string, error) {
	return c.checkAddress(op, file.URL, fileURLPrefix, file.FileID)
}

// checkAddress accepts an address Twenty reports only when it is https on exactly the host and port of the
// connection, names exactly prefix plus fileID, and carries nothing but one token. The address is rebuilt from
// the origin and the token, never used as given, and never reported. An address on another host means the
// instance serves files from external storage, which is not supported.
func (c *Client) checkAddress(op, raw, prefix, fileID string) (string, error) {
	invalid := func() (string, error) {
		return "", provider.InvalidResponse(op, "Twenty answered with a file address outside the configured service")
	}
	base, err := url.Parse(c.origin)
	if err != nil || raw == "" {
		return invalid()
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" ||
		parsed.ForceQuery {
		return invalid()
	}
	if !strings.EqualFold(parsed.Hostname(), base.Hostname()) || !samePort(parsed, base) {
		return "", &provider.Error{Class: provider.ClassProviderError, Op: op, Message: errExternalStorage}
	}
	match := tokenQuery.FindStringSubmatch(parsed.RawQuery)
	if match == nil || parsed.Path != prefix+fileID || parsed.EscapedPath() != parsed.Path {
		return invalid()
	}
	if c.red != nil {
		c.red.Add(match[1], raw)
	}
	return c.origin + parsed.Path + "?token=" + match[1], nil
}

// downloadFile fetches the file without the API key: the signed token in the address is its only
// credential. With a download it streams into it, otherwise it returns the content inline up to 4 MiB.
// Redirects are not followed; Twenty answers with one when it serves files from external storage.
func (c *Client) downloadFile(ctx context.Context, op, link string, result *AttachmentFile, download *localfile.Download) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "Twenty", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	req.Header.Set("User-Agent", "qatlas-cli")
	transfer := *c.http
	transfer.Timeout = transferTimeout
	response, err := transfer.Do(req)
	if err != nil {
		return provider.Transport(op, "Twenty", err)
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode >= 300 && response.StatusCode < 400:
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: errExternalStorage}
	case response.StatusCode != http.StatusOK:
		return provider.ClassifyStatus(op, response.StatusCode, provider.StatusTexts{
			Subject:    "Twenty",
			Auth:       "Twenty refused to serve this file",
			Permission: "Twenty refused to serve this file",
			NotFound:   "Twenty no longer serves this file",
		})
	}
	if download != nil {
		if response.ContentLength >= 0 {
			if err := download.ExpectSize(response.ContentLength); err != nil {
				return err
			}
		}
		body := &trackedReader{Reader: response.Body}
		if _, err := download.ReadFrom(body); err != nil {
			if body.err != nil {
				return provider.Transport(op, "Twenty", body.err)
			}
			return err
		}
		return nil
	}
	tooLarge := "the file is larger than 4 MiB, use " + localfile.LocalPathArgument
	if response.ContentLength > maxInlineFileBytes {
		return providerError(op, tooLarge)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxInlineFileBytes+1))
	if err != nil {
		return provider.Transport(op, "Twenty", err)
	}
	if len(data) > maxInlineFileBytes {
		return providerError(op, tooLarge)
	}
	if response.ContentLength >= 0 && response.ContentLength != int64(len(data)) {
		return provider.InvalidResponse(op, "Twenty delivered a different amount of content than it announced")
	}
	digest := sha256.Sum256(data)
	result.Size, result.SHA256, result.Content = int64(len(data)), hex.EncodeToString(digest[:]), base64.StdEncoding.EncodeToString(data)
	return nil
}

// trackedReader remembers a failure of the source, so it can be told from a failure of the local file.
type trackedReader struct {
	io.Reader
	err error
}

func (t *trackedReader) Read(p []byte) (int, error) {
	n, err := t.Reader.Read(p)
	if err != nil && err != io.EOF {
		t.err = err
	}
	return n, err
}
