package twentycrm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// maxUploadBytes is well below the 1 GB Twenty allows. The body streams from the file and is not bound by
	// the request limit of the invoke layer, but proxies in front of a server often cap uploads.
	maxUploadBytes = 100 << 20
	// maxInlineBase64 is the encoded length of maxInlineFileBytes.
	maxInlineBase64 = (maxInlineFileBytes + 2) / 3 * 4
	maxFileNameLen  = 255

	uploadURLPrefix = "/file-upload/"
	// uploadContentType is what Twenty's own server-side upload target reports, and what the PUT must send.
	uploadContentType = "application/octet-stream"

	// filesFieldFolder and attachmentFileField select the files field of the attachment object. The field is
	// addressed by its universal identifier, which is the same in every workspace, so no metadata is read.
	filesFieldFolder    = "FilesField"
	attachmentFileField = "20202020-15db-460e-8166-c7b5d87ad4be"

	errUploadPermission = "the workspace role of this API key lacks the Upload File permission; " +
		"grant it in the role of the API key in Twenty"
	errUploadName = "name must be 1 to 255 characters without path separators or control characters"

	// The step texts name what remains in Twenty. An upload that was not completed is removed by Twenty after
	// 24 hours; a completed file without an attachment stays until someone removes it there.
	pendingNote = "; the file was not completed and is not attached to the record; Twenty removes unfinished uploads after 24 hours"
)

var createFileUploadDocument = graphqlDocument{path: metadataPath, text: `mutation CreateFileUpload(` +
	`$filename: String!, $size: Float!, $fileFolder: FileFolder!, $fieldMetadataUniversalIdentifier: String) { ` +
	`createFileUpload(filename: $filename, size: $size, fileFolder: $fileFolder, ` +
	`fieldMetadataUniversalIdentifier: $fieldMetadataUniversalIdentifier) { fileId uploadUrl contentType } }`}

var completeFileUploadDocument = graphqlDocument{path: metadataPath, text: `mutation CompleteFileUpload(` +
	`$fileId: String!) { completeFileUpload(fileId: $fileId) { id } }`}

var attachmentsUpload = capability.Descriptor{
	ID:      Provider + ".attachments.upload",
	Version: 1,
	Title:   "Upload a file as a Twenty CRM attachment",
	Description: "Upload a local file or inline content (up to 4 MiB) and attach it to one record of one reachable " +
		"object. The record is read first; then Twenty is asked for an upload address, the file is sent there " +
		"without the API key, the upload is completed, and the attachment is created; each request is sent " +
		"once. The role of the API key needs the Upload File permission and the right to create attachments. " +
		"An instance that serves files through external storage is not supported. A failure after the upload " +
		"began names the file id; a completed file without an attachment stays in Twenty",
	Tags:     []string{"twentycrm", "crm", "attachments", "files", "upload", "create"},
	Risk:     recordWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider, LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"object":` + objectNameSchema + `,"record_id":` +
		recordIDSchema + `,"name":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxFileNameLen) + `},"` +
		localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `,"` + localfile.ContentArgument +
		`":{"type":"string","maxLength":` + strconv.Itoa(maxInlineBase64) + `}},` +
		`"required":["object","record_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"file_id":{"type":"string"},` +
		`"label":{"type":"string"},"size":{"type":"integer"}},"required":["id","file_id","label","size"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "object", Description: "Singular API name of the object of the record, as returned by twenty-crm.objects.list", Required: true},
		{Name: "record_id", Description: "Record identifier as a UUID", Required: true},
		{Name: "name", Description: "File name shown in Twenty; required with " + localfile.ContentArgument +
			", not allowed with " + localfile.LocalPathArgument + ", which uses the name of the local file"},
		localfile.UploadPathArgument(),
		{Name: localfile.ContentArgument, Description: "File content as base64, up to 4 MiB; instead of " +
			localfile.LocalPathArgument},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the new attachment"},
		{Name: "file_id", Description: "Identifier of the stored file"},
		{Name: "label", Description: "File name shown in Twenty"},
		{Name: "size", Description: "Size of the uploaded content in bytes"},
	},
	Examples: []capability.Example{{
		Description: "Attach a local file to a person",
		Arguments: json.RawMessage(`{"object":"person","record_id":"11111111-2222-3333-4444-555555555555",` +
			`"local_path":"~/uploads/offer.pdf"}`),
	}},
}

// AttachmentUpload is the result of an upload: the new attachment and its file.
type AttachmentUpload struct {
	ID     string `json:"id"`
	FileID string `json:"file_id"`
	Label  string `json:"label"`
	Size   int64  `json:"size"`
}

type uploadInput struct {
	Object    string  `json:"object"`
	RecordID  string  `json:"record_id"`
	Name      *string `json:"name"`
	LocalPath *string `json:"local_path"`
	Content   *string `json:"content_base64"`
}

// validUploadName keeps a path, a control character, and an unusable length out of a shown file name.
func validUploadName(name string) bool {
	if name == "" || len(name) > maxFileNameLen || !utf8.ValidString(name) || name == "." || name == ".." ||
		strings.ContainsAny(name, "/\\") {
		return false
	}
	return strings.IndexFunc(name, unicode.IsControl) < 0
}

func invokeAttachmentsUpload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "upload attachment"
	var args uploadInput
	if json.Unmarshal(raw, &args) != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectObject(resolved, args.Object); err != nil {
		return nil, invalidRequest(errAttachmentObject)
	}
	if !validUUID(args.RecordID) {
		return nil, invalidRequest(errAttachmentID)
	}
	// The source is settled before the credential is resolved, so a refused path or size costs no secret
	// access and no provider I/O.
	kind, err := localfile.UploadSource(args.LocalPath, args.Content)
	if err != nil {
		return nil, err
	}
	var (
		source io.Reader
		upload *localfile.Upload
		name   string
		size   int64
	)
	if kind == localfile.SourceLocalPath {
		if args.Name != nil {
			return nil, invalidRequest("name is only used with " + localfile.ContentArgument)
		}
		if upload, err = localfile.OpenForUpload(ctx, resolved, *args.LocalPath); err != nil {
			return nil, err
		}
		defer upload.Close()
		name, size, source = upload.Name, upload.Size, upload
	} else {
		if args.Name == nil {
			return nil, invalidRequest("name is required with " + localfile.ContentArgument)
		}
		if len(*args.Content) > maxInlineBase64 {
			return nil, invalidRequest("inline content is limited to 4 MiB, use " + localfile.LocalPathArgument)
		}
		content, err := base64.StdEncoding.DecodeString(*args.Content)
		if err != nil {
			return nil, invalidRequest(localfile.ContentArgument + " is not valid base64")
		}
		if len(content) > maxInlineFileBytes {
			return nil, invalidRequest("inline content is limited to 4 MiB, use " + localfile.LocalPathArgument)
		}
		name, size, source = *args.Name, int64(len(content)), bytes.NewReader(content)
	}
	if !validUploadName(name) {
		return nil, invalidRequest(errUploadName)
	}
	if size <= 0 || size > maxUploadBytes {
		return nil, invalidRequest("the file must not be empty and may be at most 100 MiB")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UploadAttachment(ctx, &args, name, size, source, upload)
}

// UploadAttachment runs the five steps in order, each as exactly one request. The target record is read
// first, so a record that cannot be reached leaves no file behind.
func (c *Client) UploadAttachment(ctx context.Context, args *uploadInput, name string, size int64,
	source io.Reader, local *localfile.Upload) (*AttachmentUpload, error) {
	const op = "upload attachment"
	object, err := c.attachmentObject(ctx, op)
	if err != nil {
		return nil, err
	}
	field, err := object.targetField(args.Object)
	if err != nil {
		return nil, err
	}
	if _, err := c.GetRecord(ctx, &recordQuery{Object: args.Object, ID: args.RecordID}); err != nil {
		return nil, err
	}

	var created struct {
		CreateFileUpload struct {
			FileID      string `json:"fileId"`
			UploadURL   string `json:"uploadUrl"`
			ContentType string `json:"contentType"`
		} `json:"createFileUpload"`
	}
	variables := map[string]any{"filename": name, "size": size, "fileFolder": filesFieldFolder,
		"fieldMetadataUniversalIdentifier": attachmentFileField}
	if err := c.graphqlWith(ctx, op, "", createFileUploadDocument, variables, &created); err != nil {
		return nil, uploadPermission(err, "")
	}
	target := created.CreateFileUpload
	if !validUUID(target.FileID) {
		return nil, provider.InvalidResponse(op, "Twenty answered the upload request with an unusable target")
	}
	fileID := strings.ToLower(target.FileID)
	if target.ContentType != uploadContentType {
		return nil, provider.InvalidResponse(op, "Twenty answered the upload request with an unusable target"+pendingNote)
	}
	link, err := c.checkAddress(op, target.UploadURL, uploadURLPrefix, target.FileID)
	if err != nil {
		return nil, appendNote(err, pendingNote)
	}

	if err := c.putFile(ctx, op, link, size, source, fileID); err != nil {
		return nil, err
	}
	if local != nil {
		// The transport may stop at the announced size. Reading on proves that the file ended there.
		if _, err := io.Copy(io.Discard, local); err != nil {
			return nil, providerError(op, "the local file changed while it was uploaded"+pendingNote)
		}
	}

	var completed struct {
		CompleteFileUpload struct {
			ID string `json:"id"`
		} `json:"completeFileUpload"`
	}
	uncertain := completeUncertain(fileID)
	if err := c.graphqlWith(ctx, op, uncertain, completeFileUploadDocument,
		map[string]any{"fileId": fileID}, &completed); err != nil {
		return nil, uploadPermission(withHint(err, uncertain, pendingNote), pendingNote)
	}
	if !strings.EqualFold(completed.CompleteFileUpload.ID, fileID) {
		return nil, provider.InvalidResponse(op, "Twenty completed a different file than the uploaded one"+uncertain)
	}

	return c.createAttachment(ctx, op, object, field, args, fileID, name, size)
}

// putFile sends the content to the checked upload address. The signed token in the address is the only
// credential; the API key never leaves for it. From here on, a failure that may have delivered the content
// names the file.
func (c *Client) putFile(ctx context.Context, op, link string, size int64, source io.Reader, fileID string) error {
	uncertain := putUncertain(fileID)
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "Twenty", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, link, io.NopCloser(source))
	if err != nil {
		return providerError(op, "the request could not be built"+pendingNote)
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", uploadContentType)
	req.Header.Set("User-Agent", "qatlas-cli")
	transfer := *c.http
	transfer.Timeout = transferTimeout
	response, err := transfer.Do(req)
	if err != nil {
		failure := provider.Transport(op, "Twenty", err)
		if failure.MayHaveArrived() || failure.Class == provider.ClassUnreachable {
			failure.Message += uncertain
		}
		return failure
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return nil
	case response.StatusCode >= 300 && response.StatusCode < 400:
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: errExternalStorage + pendingNote}
	}
	failure := provider.ClassifyStatus(op, response.StatusCode, provider.StatusTexts{
		Subject:    "Twenty",
		Auth:       "Twenty refused the upload address; it may have expired",
		Permission: "Twenty refused the upload address; it may have expired",
		NotFound:   "Twenty no longer knows this upload",
	})
	// The upload address may point to external storage, whose answer is not Twenty's: only the status is kept.
	failure.Original = &provider.Original{Status: response.StatusCode}
	if response.StatusCode >= 500 {
		failure.Message += uncertain
	} else {
		failure.Message += pendingNote
	}
	return failure
}

// createAttachment links the completed file to the record with one POST whose body holds only the file and the
// target field, and checks that the answer names that file and one reachable target.
func (c *Client) createAttachment(ctx context.Context, op string, object *attachmentObject, field string,
	args *uploadInput, fileID, name string, size int64) (*AttachmentUpload, error) {
	uncertain := attachUncertain(fileID)
	body := map[string]any{"file": []map[string]string{{"fileId": fileID, "label": name}}, field: args.RecordID}
	path := "/rest/" + url.PathEscape(object.Plural) + "?depth=" + noRelations
	var response struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := c.changeWith(ctx, op, uncertain, http.MethodPost, path, body, &response); err != nil {
		return nil, withHint(err, uncertain, completedNote(fileID))
	}
	bad := func(message string) error { return provider.InvalidResponse(op, message+uncertain) }
	item, ok := response.Data["create"+strings.ToUpper(object.Name[:1])+object.Name[1:]]
	if !ok {
		return nil, bad("Twenty returned an unusable attachment")
	}
	row, err := object.parse(op, item)
	if err != nil {
		var failure *provider.Error
		if errors.As(err, &failure) {
			failure.Message += uncertain
		}
		return nil, err
	}
	if row.File == nil || !strings.EqualFold(row.File.FileID, fileID) || row.Targets != 1 || !row.Reachable {
		return nil, bad("Twenty answered with a different attachment than the requested one")
	}
	return &AttachmentUpload{ID: row.ID, FileID: fileID, Label: name, Size: size}, nil
}

func putUncertain(fileID string) string {
	return "; the content may have reached Twenty, but the upload (file id " + fileID + ") was not completed and " +
		"is not attached to the record; Twenty removes unfinished uploads after 24 hours"
}

func completeUncertain(fileID string) string {
	return "; the upload (file id " + fileID + ") may be complete but is not attached to the record; a completed " +
		"file without an attachment stays in Twenty"
}

func attachUncertain(fileID string) string {
	return "; the file (id " + fileID + ") is uploaded and the attachment may have been created; list the " +
		"attachments of the record before repeating, otherwise the file stays in Twenty without an attachment"
}

func completedNote(fileID string) string {
	return "; the file (id " + fileID + ") is uploaded but not attached to the record and stays in Twenty"
}

// appendNote appends note to the message of a provider failure.
func appendNote(err error, note string) error {
	var failure *provider.Error
	if errors.As(err, &failure) {
		copied := *failure
		copied.Message += note
		return &copied
	}
	return err
}

// withHint appends hint to a failure that carries neither uncertain nor hint yet.
func withHint(err error, uncertain, hint string) error {
	var failure *provider.Error
	if errors.As(err, &failure) && !strings.HasSuffix(failure.Message, uncertain) {
		return appendNote(err, hint)
	}
	return err
}

// uploadPermission names the Upload File right in a permission failure of an upload step.
func uploadPermission(err error, hint string) error {
	var failure *provider.Error
	if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
		copied := *failure
		copied.Message = errUploadPermission + hint
		return &copied
	}
	return err
}
