package lexware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// maxVoucherFileBytes is the documented Lexware limit for one uploaded file.
	maxVoucherFileBytes = 5 << 20

	fileMayExist      = "; the file may have been uploaded, check the voucher list before repeating it"
	fileMayBeAttached = "; the file may have been attached, read the voucher before repeating it"

	// fileFormatMessage is the fixed text of an HTTP 406 on an upload; Lexware's own text is never copied.
	fileFormatMessage = "Lexware does not accept this file; allowed are pdf, jpg, jpeg, png and xml up to 5 MB"
)

// voucherFileTypes maps the accepted extensions to the content type sent with the file.
var voucherFileTypes = map[string]string{
	".pdf": "application/pdf", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".xml": "application/xml",
}

var voucherFileRisk = capability.Risk{
	Effect:          capability.EffectCreate,
	Idempotency:     capability.IdempotencyNonIdempotent,
	Confirmation:    capability.ConfirmationRequired,
	OpenWorld:       true,
	DataSensitivity: bookkeepingSensitivity,
}

const localPathDescription = "Local file of type pdf, jpg, jpeg, png or xml up to 5 MB, absolute or starting with ~/, " +
	"inside a directory the connection releases for reading"

var filesUpload = capability.Descriptor{
	ID: Provider + ".files.upload", Version: 1,
	Title: "Upload a receipt file to Lexware as a new bookkeeping voucher",
	Description: "Upload one local pdf, jpg, jpeg, png or xml file of up to 5 MB from a directory the connection " +
		"releases for reading. Lexware creates a new bookkeeping voucher from it in the folder \"Zu prüfen\"; until " +
		"the text recognition has finished the voucher has the status blank. Lexware recognizes duplicates by " +
		"checksum and then reports the identifiers that already exist. The content is never returned",
	Tags:       []string{"lexware", "files", "upload", "vouchers", "bookkeeping", "accounting"},
	Provider:   Provider,
	Risk:       voucherFileRisk,
	LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"` + localfile.LocalPathArgument + `":` +
		localfile.LocalPathSchema + `},"required":["` + localfile.LocalPathArgument + `"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"file_id":{"type":"string"},` +
		`"voucher_id":{"type":"string"}},"required":["file_id","voucher_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: localfile.LocalPathArgument, Description: localPathDescription, Required: true},
	},
	Fields: []capability.Field{
		{Name: "file_id", Description: "Identifier of the stored file"},
		{Name: "voucher_id", Description: "Identifier of the bookkeeping voucher Lexware created, or found as a duplicate"},
	},
	Examples: []capability.Example{{
		Description: "Upload a receipt as a new voucher",
		Arguments:   json.RawMessage(`{"local_path":"~/receipts/hotel.pdf"}`),
	}},
}

var vouchersAttach = capability.Descriptor{
	ID: Provider + ".vouchers.attach", Version: 1,
	Title: "Attach a file to a Lexware bookkeeping voucher",
	Description: "Attach one local pdf, jpg, jpeg, png or xml file of up to 5 MB from a directory the connection " +
		"releases for reading to an existing bookkeeping voucher. Lexware recognizes duplicates by checksum and " +
		"then reports the identifiers that already exist. The content is never returned",
	Tags:       []string{"lexware", "vouchers", "attach", "files", "bookkeeping", "accounting"},
	Provider:   Provider,
	Risk:       voucherFileRisk,
	LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `,"` +
		localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},"required":["id","` +
		localfile.LocalPathArgument + `"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"file_id":{"type":"string"},` +
		`"voucher_id":{"type":"string"}},"required":["file_id","voucher_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "id", Description: "Bookkeeping voucher identifier as a UUID, as returned by lexware.voucher-list.list", Required: true},
		{Name: localfile.LocalPathArgument, Description: localPathDescription, Required: true},
	},
	Fields: []capability.Field{
		{Name: "file_id", Description: "Identifier of the stored file"},
		{Name: "voucher_id", Description: "Identifier of the bookkeeping voucher the file belongs to"},
	},
	Examples: []capability.Example{{
		Description: "Attach a scan to an existing voucher",
		Arguments:   json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555","local_path":"~/receipts/scan.png"}`),
	}},
}

// VoucherFile is the answer of an upload or an attachment.
type VoucherFile struct {
	FileID    string `json:"file_id"`
	VoucherID string `json:"voucher_id"`
}

// multipartFile is the payload of a file upload: fixed form fields and one file part.
type multipartFile struct {
	fields      [][2]string
	filename    string
	contentType string
	data        []byte
}

// encode builds the body once; the file is at most 5 MB, so it stays in memory.
func (m *multipartFile) encode() (*bytes.Buffer, string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, f := range m.fields {
		if err := w.WriteField(f[0], f[1]); err != nil {
			return nil, "", err
		}
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+
		strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", "", "\n", "").Replace(m.filename)+`"`)
	header.Set("Content-Type", m.contentType)
	part, err := w.CreatePart(header)
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(m.data); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &body, w.FormDataContentType(), nil
}

func invokeFilesUpload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "upload file"
	var input struct {
		LocalPath string `json:"local_path"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	file, err := readVoucherFile(ctx, op, resolved, input.LocalPath)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	file.fields = [][2]string{{"type", "voucher"}}
	var response struct {
		ID        string `json:"id"`
		VoucherID string `json:"voucherId"`
	}
	if err := client.postFile(ctx, op, "", "/v1/files", file, &response, fileMayExist); err != nil {
		return nil, err
	}
	if !validUUID(response.ID) || !validUUID(response.VoucherID) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "Lexware returned the upload without usable identifiers" + fileMayExist}
	}
	return &VoucherFile{FileID: response.ID, VoucherID: response.VoucherID}, nil
}

func invokeVouchersAttach(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "attach file"
	var input struct {
		ID        string `json:"id"`
		LocalPath string `json:"local_path"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validUUID(input.ID) {
		return nil, providerError(op, "the voucher identifier must be a UUID")
	}
	file, err := readVoucherFile(ctx, op, resolved, input.LocalPath)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var response struct {
		ID        string `json:"id"`
		VoucherID string `json:"voucherId"`
	}
	if err := client.postFile(ctx, op, resourceVoucher, "/v1/vouchers/"+url.PathEscape(input.ID)+"/files",
		file, &response, fileMayBeAttached); err != nil {
		return nil, err
	}
	// A voucher identifier in the answer must be the requested one.
	if !validUUID(response.ID) || (response.VoucherID != "" && !strings.EqualFold(response.VoucherID, input.ID)) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "Lexware returned the attachment without usable identifiers" + fileMayBeAttached}
	}
	return &VoucherFile{FileID: response.ID, VoucherID: input.ID}, nil
}

// readVoucherFile checks extension, release, type and size, then reads the whole file. It runs before the
// credential is resolved, so a refusal needs no secret access and no provider I/O.
func readVoucherFile(ctx context.Context, op string, resolved *config.Resolved, path string) (*multipartFile, error) {
	contentType, ok := voucherFileTypes[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil, providerError(op, "the file must be a pdf, jpg, jpeg, png or xml file")
	}
	upload, err := localfile.OpenForUpload(ctx, resolved, path)
	if err != nil {
		return nil, err
	}
	defer upload.Close()
	if upload.Size == 0 || upload.Size > maxVoucherFileBytes {
		return nil, providerError(op, "the file must not be empty or larger than 5 MB")
	}
	data, err := io.ReadAll(upload)
	if err != nil {
		var pathErr *localfile.PathError
		if errors.As(err, &pathErr) {
			return nil, err
		}
		return nil, providerError(op, "the local file could not be read completely")
	}
	return &multipartFile{filename: upload.Name, contentType: contentType, data: data}, nil
}

// postFile sends one multipart upload through the common send path. Lexware's text of a 406 is replaced by a
// fixed one.
func (c *Client) postFile(ctx context.Context, op, resource, path string, file *multipartFile, out any, uncertain string) error {
	err := c.send(ctx, op, "POST", resource, path, nil, file, out, uncertain)
	var failure *provider.Error
	if errors.As(err, &failure) && failure.Message == validationMessage {
		failure.Message = fileFormatMessage
	}
	return err
}
