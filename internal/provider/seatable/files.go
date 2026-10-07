package seatable

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The account routes of the file tools. All three authenticate with the API token; the links they hand out
// are bearer links and stay inside this process.
const (
	uploadLinkPath   = "/api/v2.1/dtable/app-upload-link/"
	downloadLinkPath = "/api/v2.1/dtable/app-download-link/"
	assetPath        = "/api/v2.1/dtable/app-asset/"
)

const (
	// maxInlineFileBytes bounds a file that travels inline as base64.
	maxInlineFileBytes = 4 << 20
	// maxInlineBase64 is the encoded length of maxInlineFileBytes.
	maxInlineBase64 = (maxInlineFileBytes + 2) / 3 * 4
	// transferTimeout replaces the 30 s of the client for the transfer of file content only.
	transferTimeout = 30 * time.Minute
	maxFileNameLen  = 255
	maxCellAssets   = 1000

	// fileUncertain is appended to a failure of the upload or of the change that attaches the file, whose
	// result may be open. Qatlas never repeats such a request by itself.
	fileUncertain = "; the file may have been stored or attached, read the cell before repeating it"
	// assetUncertain is the same hint for deleting an asset.
	assetUncertain = "; the asset may have been deleted, read the cell before repeating it"
)

const fileRowProperties = `"table":` + tableSelectionSchema + `,"row_id":` + rowIDSchema + `,"column":` + columnSchema

const assetSelectorProperties = `,"name":{"type":"string","minLength":1,"maxLength":255},` +
	`"index":{"type":"integer","minimum":0,"maximum":999}`

var fileRowArguments = []capability.Argument{
	{Name: "table", Description: "Table reference returned by seatable.tables.list; required for an allow-list or * scope"},
	{Name: "row_id", Description: "Row identifier of 22 characters, as returned by seatable.rows.list", Required: true},
	{Name: "column", Description: "Name of a file or image column of the table", Required: true},
}

var assetSelectorArguments = []capability.Argument{
	{Name: "name", Description: "File name of the entry in the cell; alternative to index"},
	{Name: "index", Description: "Position of the entry in the cell, starting at 0; alternative to name"},
}

var filesUpload = capability.Descriptor{
	ID: Provider + ".files.upload", Version: 1,
	Title: "Upload a file to a SeaTable cell",
	Description: "Store a file in the base and append it to a file or image column of one row of a table allowed " +
		"by the connection; the file comes from local_path or inline from content_base64 up to 4 MiB",
	Tags: []string{"seatable", "base", "files", "upload", "cell", "table"}, Provider: Provider,
	Risk: capability.Risk{Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
	LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + fileRowProperties + `,` +
		`"name":{"type":"string","minLength":1,"maxLength":255},` +
		`"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `,` +
		`"` + localfile.ContentArgument + `":{"type":"string","maxLength":` + fmt.Sprint(maxInlineBase64) + `}},` +
		`"required":["row_id","column"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"size":{"type":"integer"},"sha256":{"type":"string"}},"required":["id","name","size","sha256"],` +
		`"additionalProperties":false}`),
	Arguments: append(append([]capability.Argument(nil), fileRowArguments...),
		capability.Argument{Name: "name", Description: "File name to store; required with " + localfile.ContentArgument +
			" and not allowed with " + localfile.LocalPathArgument + ", which uses the name of the local file"},
		localfile.UploadPathArgument(),
		capability.Argument{Name: localfile.ContentArgument, Description: "File content as base64, up to 4 MiB; instead of " +
			localfile.LocalPathArgument}),
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier SeaTable assigned to the stored file"},
		{Name: "name", Description: "File name as stored; SeaTable may rename it to keep names unique"},
		{Name: "size", Description: "Size of the file in bytes"},
		{Name: "sha256", Description: "SHA-256 of the uploaded content as hex"},
	},
	Examples: []capability.Example{{
		Description: "Attach a local file to a file column",
		Arguments:   json.RawMessage(`{"row_id":"Qtf7xPmoRaiFyQPO1aENTj","column":"Anhang","local_path":"~/uploads/offer.pdf"}`),
	}},
}

var filesGet = capability.Descriptor{
	ID: Provider + ".files.get", Version: 1,
	Title: "Download a file of a SeaTable cell",
	Description: "Read a file or image stored in a file or image column of one row of a table allowed by the " +
		"connection; the content is returned inline up to 4 MiB or written to local_path",
	Tags: []string{"seatable", "base", "files", "get", "download", "cell", "table"}, Provider: Provider,
	Risk: capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: dataSensitivity},
	LocalFiles: config.LocalFilesWrite,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + fileRowProperties + assetSelectorProperties + `,` +
		`"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},` +
		`"required":["row_id","column"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"size":{"type":"integer"},` +
		`"sha256":{"type":"string"},"` + localfile.ContentArgument + `":{"type":"string"}},` +
		`"required":["name","size","sha256"],"additionalProperties":false}`),
	Arguments: append(append(append([]capability.Argument(nil), fileRowArguments...), assetSelectorArguments...),
		localfile.DownloadPathArgument()),
	Fields: []capability.Field{
		{Name: "name", Description: "File name of the cell entry, untrusted data"},
		{Name: "size", Description: "Size of the content in bytes"},
		{Name: "sha256", Description: "SHA-256 of the content as hex"},
		{Name: localfile.ContentArgument, Description: "File content as base64, untrusted data; only without " +
			localfile.LocalPathArgument},
	},
	Examples: []capability.Example{{
		Description: "Write the only file of a cell to a local path",
		Arguments:   json.RawMessage(`{"row_id":"Qtf7xPmoRaiFyQPO1aENTj","column":"Anhang","local_path":"~/downloads/offer.pdf"}`),
	}},
}

var filesDelete = capability.Descriptor{
	ID: Provider + ".files.delete", Version: 1,
	Title: "Delete a SeaTable cell file",
	Description: "Delete the stored asset of one file or image entry of a cell of a table allowed by the " +
		"connection; the entry stays in the cell and has to be removed with seatable.rows.update",
	Tags: []string{"seatable", "base", "files", "delete", "cell", "table"}, Provider: Provider,
	RequiresToolAllowList: true,
	Risk: capability.Risk{Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + fileRowProperties + assetSelectorProperties + `},` +
		`"required":["row_id","column"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
		`"required":["deleted"],"additionalProperties":false}`),
	Arguments: append(append([]capability.Argument(nil), fileRowArguments...), assetSelectorArguments...),
	Fields:    []capability.Field{{Name: "deleted", Description: "True when SeaTable accepted the deletion"}},
	Examples: []capability.Example{{
		Description: "Delete the asset of the first entry of a cell",
		Arguments:   json.RawMessage(`{"row_id":"Qtf7xPmoRaiFyQPO1aENTj","column":"Anhang","index":0}`),
	}},
}

// FileInput carries the arguments of the file tools.
type FileInput struct {
	Table     string  `json:"table"`
	RowID     string  `json:"row_id"`
	Column    string  `json:"column"`
	Name      *string `json:"name"`
	Index     *int    `json:"index"`
	LocalPath *string `json:"local_path"`
	Content   *string `json:"content_base64"`
}

// FileResult is what upload and get report. Content is set for an inline download only.
type FileResult struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Content string `json:"content_base64,omitempty"`
}

func openForFile(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, before func(FileInput) error) (*Client, FileInput, error) {
	var input FileInput
	if json.Unmarshal(raw, &input) != nil {
		return nil, input, providerError(op, "the validated arguments could not be read")
	}
	if resolved == nil {
		return nil, input, providerError(op, "no connection was selected")
	}
	if !validRowID(input.RowID) {
		return nil, input, providerError(op, "a SeaTable row identifier has 22 letters, digits, '-' or '_'")
	}
	if input.Column == "" || strings.HasPrefix(input.Column, systemPrefix) {
		return nil, input, providerError(op, "a file column must be named and cannot be a system column")
	}
	bound, err := parseScope(resolved)
	if err != nil {
		return nil, input, providerError(op, err.Error())
	}
	if _, err := bound.selectTarget(input.Table); err != nil {
		return nil, input, providerError(op, err.Error())
	}
	if before != nil {
		if err := before(input); err != nil {
			return nil, input, err
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	return client, input, err
}

func invokeFilesUpload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "upload file"
	var (
		source  io.Reader
		upload  *localfile.Upload
		name    string
		size    int64
		content []byte
	)
	// The local file is opened before the credential is resolved, so a path outside the release is
	// refused without secret access or provider I/O.
	client, input, err := openForFile(ctx, op, resolved, secrets, red, raw, func(input FileInput) error {
		kind, err := localfile.UploadSource(input.LocalPath, input.Content)
		if err != nil {
			return err
		}
		if kind == localfile.SourceLocalPath {
			if input.Name != nil {
				return providerError(op, "name is only used with "+localfile.ContentArgument)
			}
			upload, err = localfile.OpenForUpload(ctx, resolved, *input.LocalPath)
			if err != nil {
				return err
			}
			name, size, source = upload.Name, upload.Size, upload
			return nil
		}
		if input.Name == nil {
			return providerError(op, "name is required with "+localfile.ContentArgument)
		}
		if len(*input.Content) > maxInlineBase64 {
			return providerError(op, "inline content is limited to 4 MiB, use "+localfile.LocalPathArgument)
		}
		content, err = base64.StdEncoding.DecodeString(*input.Content)
		if err != nil {
			return providerError(op, localfile.ContentArgument+" is not valid base64")
		}
		if len(content) > maxInlineFileBytes {
			return providerError(op, "inline content is limited to 4 MiB, use "+localfile.LocalPathArgument)
		}
		name, size, source = *input.Name, int64(len(content)), bytes.NewReader(content)
		return nil
	})
	if upload != nil {
		defer upload.Close()
	}
	if err != nil {
		return nil, err
	}
	if !validFileName(name) {
		return nil, providerError(op, "the file name must be 1 to 255 characters without path separators or control characters")
	}
	result, err := client.UploadFile(ctx, input, name, size, source)
	if err != nil {
		return nil, err
	}
	if upload != nil {
		// The transport may stop at the announced size. Reading on proves that the file ended there.
		if _, err := io.Copy(io.Discard, upload); err != nil {
			return nil, providerError(op, "the local file changed while it was uploaded")
		}
		sum, ok := upload.SHA256()
		if !ok {
			return nil, providerError(op, "the local file could not be read completely")
		}
		result.SHA256 = sum
	} else {
		digest := sha256.Sum256(content)
		result.SHA256 = hex.EncodeToString(digest[:])
	}
	return result, nil
}

func invokeFilesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get file"
	var download *localfile.Download
	client, input, err := openForFile(ctx, op, resolved, secrets, red, raw, func(input FileInput) error {
		if input.LocalPath == nil {
			return nil
		}
		var err error
		download, err = localfile.CreateForDownload(ctx, resolved, *input.LocalPath)
		return err
	})
	if err != nil {
		return nil, err
	}
	done := false
	if download != nil {
		defer func() {
			if !done {
				_ = download.Abort()
			}
		}()
	}
	result, err := client.DownloadFile(ctx, input, download)
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

func invokeFilesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, input, err := openForFile(ctx, "delete file", resolved, secrets, red, raw, nil)
	if err != nil {
		return nil, err
	}
	if err := client.DeleteFile(ctx, input); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}

// cellAsset is one entry of a file or image cell. Its name comes from the cell and is untrusted; rel is the
// asset path below the base, empty for an entry that is not an asset of this base.
type cellAsset struct {
	name string
	rel  string
}

// fileColumn returns the type of the named column of the selected table, which must hold files or images.
func (c *Client) fileColumn(ctx context.Context, op string, selected target, column string) (string, error) {
	document, err := c.metadata(ctx, op)
	if err != nil {
		return "", err
	}
	for _, table := range document.Metadata.Tables {
		if !matchesTable(selected, table) {
			continue
		}
		for _, candidate := range table.Columns {
			if candidate.Name != column {
				continue
			}
			if candidate.Type != "file" && candidate.Type != "image" {
				return "", providerError(op, "the selected column does not hold files or images")
			}
			return candidate.Type, nil
		}
		return "", providerError(op, "the selected SeaTable table does not define this column")
	}
	return "", providerError(op, "the selected SeaTable table no longer exists")
}

// cellEntries reads the current entries of the file cell of one row as raw values.
func (c *Client) cellEntries(ctx context.Context, op string, input FileInput) (
	entries []json.RawMessage, access *baseAccess, kind string, err error) {
	selected, err := c.scope.selectTarget(input.Table)
	if err != nil {
		return nil, nil, "", providerError(op, err.Error())
	}
	if access, err = c.access(ctx, op); err != nil {
		return nil, nil, "", err
	}
	if kind, err = c.fileColumn(ctx, op, selected, input.Column); err != nil {
		return nil, nil, "", err
	}
	row, err := c.GetRowFrom(ctx, input.Table, input.RowID)
	if err != nil {
		return nil, nil, "", err
	}
	if value := bytes.TrimSpace(row.Values[input.Column]); len(value) > 0 && string(value) != "null" {
		if json.Unmarshal(value, &entries) != nil || len(entries) > maxCellAssets {
			return nil, nil, "", &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "SeaTable returned an unusable file cell"}
		}
	}
	return entries, access, kind, nil
}

// UploadFile stores size bytes of source in the base with one upload request and appends the file to the
// cell with one row update. The link of the upload is a bearer link and never leaves this function.
func (c *Client) UploadFile(ctx context.Context, input FileInput, name string, size int64, source io.Reader) (*FileResult, error) {
	const op = "upload file"
	entries, access, kind, err := c.cellEntries(ctx, op, input)
	if err != nil {
		return nil, err
	}

	var link struct {
		UploadLink      string `json:"upload_link"`
		WorkspaceID     int64  `json:"workspace_id"`
		ParentPath      string `json:"parent_path"`
		ImgRelativePath string `json:"img_relative_path"`
		FileRelative    string `json:"file_relative_path"`
	}
	if err := c.get(ctx, op, uploadLinkPath, nil, c.apiToken, maxResponseBytes, &link); err != nil {
		return nil, err
	}
	target, err := c.checkLink(op, link.UploadLink)
	if err != nil {
		return nil, err
	}
	relative := link.FileRelative
	if kind == "image" {
		relative = link.ImgRelativePath
	}
	relative = strings.Trim(relative, "/")
	workspace := link.WorkspaceID
	if workspace <= 0 {
		workspace = access.workspaceID
	}
	owner, isAsset := strings.CutPrefix(link.ParentPath, "/asset/")
	if !isAsset || !sameUUID(owner, access.uuid) || workspace <= 0 || !validRelativeDir(relative, kind+"s") {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "SeaTable answered the upload link for a different base or without a usable location"}
	}

	stored, err := c.postUpload(ctx, op, target, link.ParentPath, relative, name, size, source)
	if err != nil {
		return nil, err
	}
	location := fmt.Sprintf("/workspace/%d%s/%s/%s", workspace, link.ParentPath, relative, url.PathEscape(stored.Name))
	var entry json.RawMessage
	if kind == "image" {
		entry, err = json.Marshal(location)
	} else {
		entry, err = json.Marshal(map[string]any{"name": stored.Name, "size": stored.Size, "type": "file", "url": location})
	}
	if err != nil {
		return nil, providerError(op, "the cell entry could not be built")
	}
	cell, err := json.Marshal(append(entries, entry))
	if err != nil {
		return nil, providerError(op, "the cell entry could not be built")
	}
	path, token, encoded, err := c.prepareRows(ctx, op, input.Table, map[string]any{"updates": []any{
		map[string]any{"row_id": input.RowID, "row": map[string]json.RawMessage{input.Column: cell}}}},
		[]map[string]json.RawMessage{{input.Column: cell}}, true)
	if err != nil {
		return nil, appendHint(err, fileUncertain)
	}
	if err := c.changeOnce(ctx, op, http.MethodPut, path, token, encoded, fileUncertain); err != nil {
		return nil, appendHint(err, "; the file was stored but is not attached to the cell")
	}
	return &FileResult{ID: stored.ID, Name: stored.Name, Size: stored.Size}, nil
}

type storedFile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// postUpload sends the one upload request. The body is framed by hand so that its length is known and the
// file streams without being held in memory.
func (c *Client) postUpload(ctx context.Context, op string, link *url.URL, parent, relative, name string,
	size int64, source io.Reader) (storedFile, error) {
	var head bytes.Buffer
	form := multipart.NewWriter(&head)
	for _, field := range [][2]string{{"parent_dir", parent}, {"relative_path", relative}, {"replace", "0"}} {
		if err := form.WriteField(field[0], field[1]); err != nil {
			return storedFile{}, providerError(op, "the request could not be built")
		}
	}
	if _, err := form.CreateFormFile("file", name); err != nil {
		return storedFile{}, providerError(op, "the request could not be built")
	}
	tail := "\r\n--" + form.Boundary() + "--\r\n"

	address := *link
	query := address.Query()
	query.Set("ret-json", "1")
	address.RawQuery = query.Encode()
	body := io.MultiReader(bytes.NewReader(head.Bytes()), io.LimitReader(source, size), strings.NewReader(tail))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address.String(), body)
	if err != nil {
		return storedFile{}, providerError(op, "the request could not be built")
	}
	req.ContentLength = int64(head.Len()) + size + int64(len(tail))
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Accept", "application/json")

	response, err := c.transferClient().Do(req)
	if err != nil {
		return storedFile{}, uncertainTransport(op, err, fileUncertain)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure := statusError(op, response.StatusCode)
		if response.StatusCode >= 500 {
			return storedFile{}, appendHint(failure, fileUncertain)
		}
		return storedFile{}, failure
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	var stored []storedFile
	if err != nil || len(data) > maxResponseBytes || json.Unmarshal(data, &stored) != nil || len(stored) != 1 ||
		!validFileName(stored[0].Name) || stored[0].Size != size || len(stored[0].ID) > 128 {
		return storedFile{}, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "SeaTable answered the upload without a usable result" + fileUncertain}
	}
	return stored[0], nil
}

// DownloadFile reads the selected entry of a file cell. With a download it streams into it, otherwise it
// returns the content inline up to 4 MiB.
func (c *Client) DownloadFile(ctx context.Context, input FileInput, download *localfile.Download) (*FileResult, error) {
	const op = "get file"
	asset, err := c.selectAsset(ctx, op, input, false)
	if err != nil {
		return nil, err
	}
	var answer struct {
		DownloadLink string `json:"download_link"`
	}
	query := url.Values{}
	query.Set("path", asset.rel)
	if err := c.get(ctx, op, downloadLinkPath, query, c.apiToken, maxResponseBytes, &answer); err != nil {
		return nil, err
	}
	link, err := c.checkLink(op, answer.DownloadLink)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link.String(), nil)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	response, err := c.transferClient().Do(req)
	if err != nil {
		return nil, provider.Transport(op, "SeaTable", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, statusError(op, response.StatusCode)
	}
	if download != nil {
		if response.ContentLength >= 0 {
			if err := download.ExpectSize(response.ContentLength); err != nil {
				return nil, err
			}
		}
		body := &trackedReader{Reader: response.Body}
		if _, err := download.ReadFrom(body); err != nil {
			if body.err != nil {
				return nil, provider.Transport(op, "SeaTable", body.err)
			}
			return nil, err
		}
		return &FileResult{Name: asset.name}, nil
	}
	if response.ContentLength > maxInlineFileBytes {
		return nil, providerError(op, "the file is larger than 4 MiB, use "+localfile.LocalPathArgument)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxInlineFileBytes+1))
	if err != nil {
		return nil, provider.Transport(op, "SeaTable", err)
	}
	if len(data) > maxInlineFileBytes {
		return nil, providerError(op, "the file is larger than 4 MiB, use "+localfile.LocalPathArgument)
	}
	if response.ContentLength >= 0 && response.ContentLength != int64(len(data)) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "SeaTable delivered a different amount of content than it announced"}
	}
	digest := sha256.Sum256(data)
	return &FileResult{Name: asset.name, Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]),
		Content: base64.StdEncoding.EncodeToString(data)}, nil
}

// DeleteFile deletes the asset of the selected entry with one request. The cell is not changed.
func (c *Client) DeleteFile(ctx context.Context, input FileInput) error {
	const op = "delete file"
	asset, err := c.selectAsset(ctx, op, input, true)
	if err != nil {
		return err
	}
	query := url.Values{}
	query.Set("path", asset.rel)
	return c.changeOnce(ctx, op, http.MethodDelete, assetPath+"?"+query.Encode(), c.apiToken, nil, assetUncertain)
}

// selectAsset reads the current cell and picks one of its entries by name or index. The asset path always
// comes from the cell, never from an argument. Without a selector, a cell with exactly one entry is used;
// a deletion always needs a selector.
func (c *Client) selectAsset(ctx context.Context, op string, input FileInput, must bool) (cellAsset, error) {
	if input.Name != nil && input.Index != nil {
		return cellAsset{}, providerError(op, "name and index exclude each other")
	}
	if must && input.Name == nil && input.Index == nil {
		return cellAsset{}, providerError(op, "name or index is required")
	}
	entries, access, _, err := c.cellEntries(ctx, op, input)
	if err != nil {
		return cellAsset{}, err
	}
	assets := make([]cellAsset, len(entries))
	for i, entry := range entries {
		assets[i] = c.parseEntry(entry, access.uuid)
	}
	var picked []cellAsset
	switch {
	case input.Index != nil:
		if *input.Index < len(assets) {
			picked = assets[*input.Index : *input.Index+1]
		}
	case input.Name != nil:
		for _, asset := range assets {
			if asset.name == *input.Name {
				picked = append(picked, asset)
			}
		}
	default:
		picked = assets
	}
	switch {
	case len(picked) == 0:
		return cellAsset{}, providerError(op, "the cell holds no such file")
	case len(picked) > 1:
		return cellAsset{}, providerError(op, "the selection is ambiguous, use index")
	case picked[0].rel == "":
		return cellAsset{}, providerError(op, "this entry is not a file stored in this base")
	}
	return picked[0], nil
}

// parseEntry reads one entry of a file cell, an object for a file column or a string for an image column.
func (c *Client) parseEntry(raw json.RawMessage, uuid string) cellAsset {
	var address string
	var entry struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if json.Unmarshal(raw, &address) != nil {
		if json.Unmarshal(raw, &entry) != nil {
			return cellAsset{}
		}
		address = entry.URL
	}
	rel, ok := c.assetRelative(address, uuid)
	if !ok {
		return cellAsset{name: entry.Name}
	}
	name := entry.Name
	if name == "" {
		name = rel[strings.LastIndex(rel, "/")+1:]
	}
	return cellAsset{name: name, rel: rel}
}

// assetRelative returns the path of an asset below the base, "files/2026-03/name", from the address SeaTable
// keeps in a cell. Only files and images of this base on the configured origin qualify; custom folders and
// foreign addresses do not.
func (c *Client) assetRelative(address, uuid string) (string, bool) {
	if address == "" || strings.ContainsAny(address, "?#") {
		return "", false
	}
	path := address
	if strings.Contains(address, "://") {
		parsed, err := url.Parse(address)
		if err != nil || parsed.Scheme+"://"+parsed.Host != c.origin || parsed.User != nil {
			return "", false
		}
		path = parsed.EscapedPath()
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if (parts[i] != "asset" && parts[i] != "asset-preview") || !sameUUID(parts[i+1], uuid) {
			continue
		}
		rest := parts[i+2:]
		if len(rest) < 2 || (rest[0] != "files" && rest[0] != "images") {
			return "", false
		}
		decoded := make([]string, len(rest))
		for j, part := range rest {
			value, err := url.PathUnescape(part)
			if err != nil || value == "" || value == "." || value == ".." || strings.ContainsAny(value, "/\\") ||
				strings.IndexFunc(value, unicode.IsControl) >= 0 {
				return "", false
			}
			decoded[j] = value
		}
		return strings.Join(decoded, "/"), true
	}
	return "", false
}

// checkLink accepts a transfer address only on the configured origin, like the server of the token exchange
// but without the exemption for an empty value. The address is a bearer link: it is registered with the
// redactor and is neither logged nor reported.
func (c *Client) checkLink(op, raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || strings.TrimSpace(raw) == "" || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.Fragment != "" || parsed.Scheme+"://"+parsed.Host != c.origin {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "SeaTable answered with a transfer address outside the configured service"}
	}
	if c.redactor != nil {
		c.redactor.Add(strings.TrimSpace(raw), parsed.Path)
	}
	return parsed, nil
}

// transferClient is the client for the content of a file, with a timeout that fits a large transfer. It
// shares the transport and the refusal to follow redirects.
func (c *Client) transferClient() *http.Client {
	copied := *c.http
	copied.Timeout = transferTimeout
	return &copied
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

func uncertainTransport(op string, err error, hint string) error {
	failure := provider.Transport(op, "SeaTable", err)
	var providerErr *provider.Error
	if errors.As(failure, &providerErr) && (providerErr.Class == provider.ClassTimeout ||
		providerErr.Cause == provider.CauseConnectionReset || providerErr.Cause == provider.CauseUnknown) {
		providerErr.Message += hint
	}
	return failure
}

func appendHint(err error, hint string) error {
	var providerErr *provider.Error
	if errors.As(err, &providerErr) && !strings.HasSuffix(providerErr.Message, hint) {
		copied := *providerErr
		copied.Message += hint
		return &copied
	}
	return err
}

// validFileName keeps a path, a control character and an unusable length out of a stored file name.
func validFileName(name string) bool {
	if name == "" || len(name) > maxFileNameLen || !utf8.ValidString(name) || name == "." || name == ".." ||
		strings.ContainsAny(name, "/\\") {
		return false
	}
	return strings.IndexFunc(name, unicode.IsControl) < 0
}

// validRelativeDir accepts "files/2026-03", the month folder SeaTable picks, below the expected root.
func validRelativeDir(value, root string) bool {
	parts := strings.Split(value, "/")
	if len(parts) < 2 || parts[0] != root {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\") ||
			strings.IndexFunc(part, unicode.IsControl) >= 0 {
			return false
		}
	}
	return true
}

// sameUUID compares two base identifiers in either notation.
func sameUUID(a, b string) bool {
	a, b = strings.ToLower(strings.ReplaceAll(a, "-", "")), strings.ToLower(strings.ReplaceAll(b, "-", ""))
	return len(a) == 32 && a == b
}
