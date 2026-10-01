package baserow

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
	"regexp"
	"strconv"
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

// The two upload routes of Baserow's user files. The official API schema lists the database token next to
// the user session for both of them.
const (
	uploadFilePath = "/api/user-files/upload-file/"
	uploadURLPath  = "/api/user-files/upload-via-url/"
	// userFilesDir is where Baserow serves stored files, below the host of the instance.
	userFilesDir = "/media/user_files/"
)

const (
	// maxInlineFileBytes bounds a file that travels inline as base64.
	maxInlineFileBytes = 4 << 20
	// maxInlineBase64 is the encoded length of maxInlineFileBytes.
	maxInlineBase64 = (maxInlineFileBytes + 2) / 3 * 4
	// transferTimeout replaces the 30 s of the client for the transfer of file content only.
	transferTimeout = 30 * time.Minute
	maxFileNameLen  = 255
	maxCellFiles    = 200
	maxFileURLLen   = 2048

	// fileUncertain is appended to a failure of the upload whose result may be open. Qatlas never repeats
	// such a request by itself.
	fileUncertain = "; the file may have been uploaded, read the row before repeating it"
	// attachUncertain is the same hint for the row change that attaches the uploaded file.
	attachUncertain = "; the file was uploaded, but the cell may hold it already, read the row before repeating it"
	// notAttached is appended when the row change was refused: the file exists but hangs on no row.
	notAttached = "; the file was uploaded but is not attached to the cell"
)

const fileRowProperties = `"table_id":` + idSchema + `,"row_id":` + idSchema +
	`,"field":{"type":"string","minLength":1,"maxLength":255}`

var fileRowArguments = []capability.Argument{tableIDArgument,
	{Name: "row_id", Required: true, Description: "Row identifier from baserow.rows.list"},
	{Name: "field", Required: true, Description: "Name of a file field of the table, from baserow.fields.list"}}

var filesUpload = capability.Descriptor{
	ID: Provider + ".files.upload", Version: 1, Title: "Upload a file to a Baserow cell",
	Description: "Upload a file to Baserow and append it to a file field of one row of a table allowed by the " +
		"connection. The file comes from local_path, inline from content_base64 up to 4 MiB, or from url, which " +
		"Baserow itself fetches; only an https URL on a public host name is accepted. Two requests run, the " +
		"upload and the row change; the cell is read first, so a concurrent change to it can be lost",
	Tags: []string{"baserow", "files", "upload", "cell", "rows"}, Provider: Provider,
	Risk: capability.Risk{Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: rowsSensitive},
	LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + fileRowProperties + `,` +
		`"name":{"type":"string","minLength":1,"maxLength":255},` +
		`"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `,` +
		`"` + localfile.ContentArgument + `":{"type":"string","maxLength":` + fmt.Sprint(maxInlineBase64) + `},` +
		`"url":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxFileURLLen) + `}},` +
		`"required":["table_id","row_id","field"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"size":{"type":"integer"},"sha256":{"type":"string"}},"required":["id","name","size"],` +
		`"additionalProperties":false}`),
	Arguments: append(append([]capability.Argument(nil), fileRowArguments...),
		capability.Argument{Name: "name", Description: "File name shown in the cell; required with " +
			localfile.ContentArgument + ", optional with url, not allowed with " + localfile.LocalPathArgument +
			", which uses the name of the local file"},
		localfile.UploadPathArgument(),
		capability.Argument{Name: localfile.ContentArgument, Description: "File content as base64, up to 4 MiB; " +
			"instead of " + localfile.LocalPathArgument + " and url"},
		capability.Argument{Name: "url", Description: "https URL of a file Baserow fetches itself; only a public host " +
			"name without user info, port, or numeric address; instead of " + localfile.LocalPathArgument + " and " +
			localfile.ContentArgument}),
	Fields: []capability.Field{
		{Name: "id", Description: "Name Baserow gave the stored file"},
		{Name: "name", Description: "File name shown in the cell"},
		{Name: "size", Description: "Size of the stored file in bytes"},
		{Name: "sha256", Description: "SHA-256 of the uploaded content as hex; for url as the stored name reports it"},
	},
	Examples: []capability.Example{{
		Description: "Attach a local file to a file field",
		Arguments:   json.RawMessage(`{"table_id":1,"row_id":7,"field":"Anhang","local_path":"~/uploads/offer.pdf"}`),
	}},
}

var filesGet = capability.Descriptor{
	ID: Provider + ".files.get", Version: 1, Title: "Download a file of a Baserow cell",
	Description: "Read a file stored in a file field of one row of a table allowed by the connection, chosen by " +
		"name or index from the current cell; the content is returned inline up to 4 MiB or written to local_path. " +
		"The file is fetched only from the host of the connection",
	Tags: []string{"baserow", "files", "get", "download", "cell", "rows"}, Provider: Provider,
	Risk: capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: rowsSensitive},
	LocalFiles: config.LocalFilesWrite,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + fileRowProperties + `,` +
		`"name":{"type":"string","minLength":1,"maxLength":255},` +
		`"index":{"type":"integer","minimum":0,"maximum":` + strconv.Itoa(maxCellFiles-1) + `},` +
		`"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},` +
		`"required":["table_id","row_id","field"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"size":{"type":"integer"},"sha256":{"type":"string"},"` + localfile.ContentArgument + `":{"type":"string"}},` +
		`"required":["name","size","sha256"],"additionalProperties":false}`),
	Arguments: append(append([]capability.Argument(nil), fileRowArguments...),
		capability.Argument{Name: "name", Description: "Name of the file in the cell, as shown or as Baserow stored it; " +
			"alternative to index"},
		capability.Argument{Name: "index", Description: "Position of the file in the cell, starting at 0; alternative to name"},
		localfile.DownloadPathArgument()),
	Fields: []capability.Field{
		{Name: "id", Description: "Name Baserow gave the stored file, untrusted data"},
		{Name: "name", Description: "File name shown in the cell, untrusted data"},
		{Name: "size", Description: "Size of the content in bytes"},
		{Name: "sha256", Description: "SHA-256 of the content as hex"},
		{Name: localfile.ContentArgument, Description: "File content as base64, untrusted data; only without " +
			localfile.LocalPathArgument},
	},
	Examples: []capability.Example{{
		Description: "Write the only file of a cell to a local path",
		Arguments:   json.RawMessage(`{"table_id":1,"row_id":7,"field":"Anhang","local_path":"~/downloads/offer.pdf"}`),
	}},
}

// FileResult is what upload and get report. Content is set for an inline download only.
type FileResult struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256,omitempty"`
	Content string `json:"content_base64,omitempty"`
}

type fileArguments struct {
	TableID   int64   `json:"table_id"`
	RowID     int64   `json:"row_id"`
	Field     string  `json:"field"`
	Name      *string `json:"name"`
	Index     *int    `json:"index"`
	LocalPath *string `json:"local_path"`
	Content   *string `json:"content_base64"`
	URL       *string `json:"url"`
}

// prepareFile settles what needs no provider I/O: the table boundary (before any secret is resolved), the
// identifiers, and the field name.
func prepareFile(op string, resolved *config.Resolved, raw json.RawMessage) (fileArguments, error) {
	var input fileArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	if err := selectTable(resolved, input.TableID); err != nil {
		return input, err
	}
	if input.RowID <= 0 || len(strconv.FormatInt(input.RowID, 10)) > maxIDDigits {
		return input, invalidRequest("row_id must be a positive integer")
	}
	if input.Field == "" || len(input.Field) > maxFieldNameLen {
		return input, invalidRequest("field must name a file field of 1 to " + strconv.Itoa(maxFieldNameLen) + " bytes")
	}
	return input, nil
}

// fileField finds the named field, which must hold files; an upload also needs it writable. No message
// quotes the name.
func fileField(fields []fieldJSON, name string, write bool) error {
	for _, field := range fields {
		if field.Name != name {
			continue
		}
		if field.Type != "file" {
			return invalidRequest("the named field does not hold files")
		}
		if write && field.ReadOnly {
			return invalidRequest("the named field is read-only and cannot be written")
		}
		return nil
	}
	return invalidRequest("the named field does not exist in this table; use the names of baserow.fields.list")
}

func invokeFilesUpload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "upload file"
	input, err := prepareFile(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	var (
		source  io.Reader
		upload  *localfile.Upload
		name    string
		size    int64
		content []byte
		fetch   string
	)
	// The local file is opened before the credential is resolved, so a path outside the release is refused
	// without secret access or provider I/O.
	switch {
	case input.URL != nil:
		if input.LocalPath != nil || input.Content != nil {
			return nil, invalidRequest("url excludes " + localfile.LocalPathArgument + " and " + localfile.ContentArgument)
		}
		if err := checkPublicURL(*input.URL); err != nil {
			return nil, err
		}
		if input.Name != nil {
			name = *input.Name
			if !validFileName(name) {
				return nil, invalidRequest("the file name must be 1 to 255 characters without path separators or control characters")
			}
		}
		fetch = *input.URL
	default:
		kind, err := localfile.UploadSource(input.LocalPath, input.Content)
		if err != nil {
			return nil, err
		}
		if kind == localfile.SourceLocalPath {
			if input.Name != nil {
				return nil, invalidRequest("name is only used with " + localfile.ContentArgument + " or url")
			}
			upload, err = localfile.OpenForUpload(ctx, resolved, *input.LocalPath)
			if err != nil {
				return nil, err
			}
			defer upload.Close()
			name, size, source = upload.Name, upload.Size, upload
		} else {
			if input.Name == nil {
				return nil, invalidRequest("name is required with " + localfile.ContentArgument)
			}
			if len(*input.Content) > maxInlineBase64 {
				return nil, invalidRequest("inline content is limited to 4 MiB, use " + localfile.LocalPathArgument)
			}
			content, err = base64.StdEncoding.DecodeString(*input.Content)
			if err != nil {
				return nil, invalidRequest(localfile.ContentArgument + " is not valid base64")
			}
			if len(content) > maxInlineFileBytes {
				return nil, invalidRequest("inline content is limited to 4 MiB, use " + localfile.LocalPathArgument)
			}
			name, size, source = *input.Name, int64(len(content)), bytes.NewReader(content)
		}
		if !validFileName(name) {
			return nil, invalidRequest("the file name must be 1 to 255 characters without path separators or control characters")
		}
	}

	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	fields, err := client.readFields(ctx, op, input.TableID)
	if err != nil {
		return nil, err
	}
	if err := fileField(fields, input.Field, true); err != nil {
		return nil, err
	}
	cell, err := client.cellFiles(ctx, op, input)
	if err != nil {
		return nil, err
	}

	var stored storedFile
	if fetch != "" {
		body, err := json.Marshal(map[string]string{"url": fetch})
		if err != nil {
			return nil, providerError(op, "the request could not be built")
		}
		if red != nil {
			red.Add(fetch)
		}
		data, err := client.send(ctx, client.transferClient(), op, http.MethodPost, uploadURLPath, nil,
			bytes.NewReader(body), "application/json", "update", fileUncertain)
		if err != nil {
			return nil, err
		}
		if stored, err = decodeStored(op, data, -1); err != nil {
			return nil, err
		}
		if name == "" {
			name = stored.OriginalName
			if !validFileName(name) {
				name = ""
			}
		}
	} else {
		data, err := client.postFile(ctx, op, name, size, source)
		if err != nil {
			return nil, err
		}
		if stored, err = decodeStored(op, data, size); err != nil {
			return nil, err
		}
	}

	result := &FileResult{ID: stored.Name, Name: firstNonEmpty(name, stored.Name), Size: stored.Size}
	hash := storedHash(stored.Name)
	switch {
	case upload != nil:
		// The transport may stop at the announced size. Reading on proves that the file ended there.
		if _, err := io.Copy(io.Discard, upload); err != nil {
			return nil, providerError(op, "the local file changed while it was uploaded"+notAttached)
		}
		sum, ok := upload.SHA256()
		if !ok {
			return nil, providerError(op, "the local file could not be read completely"+notAttached)
		}
		result.SHA256 = sum
	case fetch == "":
		digest := sha256.Sum256(content)
		result.SHA256 = hex.EncodeToString(digest[:])
	default:
		result.SHA256 = hash
	}
	if fetch == "" && hash != "" && hash != result.SHA256 {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "Baserow stored content that differs from what was sent" + notAttached}
	}

	if err := client.attachFile(ctx, op, input, cell, stored.Name, name); err != nil {
		return nil, err
	}
	return result, nil
}

// storedFile is the answer of an upload: the user file Baserow created.
type storedFile struct {
	Name         string `json:"name"`
	OriginalName string `json:"original_name"`
	Size         int64  `json:"size"`
}

// decodeStored reads the answer of an upload. A want of 0 or more is the size that was sent and must come back.
func decodeStored(op string, data []byte, want int64) (storedFile, error) {
	var stored storedFile
	if json.Unmarshal(data, &stored) != nil || !validStoredName(stored.Name) || stored.Size < 0 ||
		(want >= 0 && stored.Size != want) {
		return storedFile{}, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "Baserow answered the upload without a usable result" + fileUncertain}
	}
	stored.OriginalName = boundString(stored.OriginalName, maxFileNameLen)
	return stored, nil
}

// framedBody is a request body whose length is known in advance, so the file streams without being held in
// memory.
type framedBody struct {
	io.Reader
	length int64
}

func (f framedBody) announcedLength() int64 { return f.length }

// postFile sends the one upload request. The multipart body is framed by hand so its length is known.
func (c *Client) postFile(ctx context.Context, op, name string, size int64, source io.Reader) ([]byte, error) {
	var head bytes.Buffer
	form := multipart.NewWriter(&head)
	if _, err := form.CreateFormFile("file", name); err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	tail := "\r\n--" + form.Boundary() + "--\r\n"
	body := framedBody{
		Reader: io.MultiReader(bytes.NewReader(head.Bytes()), io.LimitReader(source, size), strings.NewReader(tail)),
		length: int64(head.Len()) + size + int64(len(tail)),
	}
	return c.send(ctx, c.transferClient(), op, http.MethodPost, uploadFilePath, nil, body,
		form.FormDataContentType(), "update", fileUncertain)
}

// attachFile appends the stored file to the files the cell held when it was read, with one row change. An
// empty visible name leaves the name to Baserow.
func (c *Client) attachFile(ctx context.Context, op string, input fileArguments, cell []cellFile, stored, visible string) error {
	entries := make([]map[string]string, 0, len(cell)+1)
	for _, file := range cell {
		entry := map[string]string{"name": file.Name}
		if validFileName(file.VisibleName) {
			entry["visible_name"] = file.VisibleName
		}
		entries = append(entries, entry)
	}
	added := map[string]string{"name": stored}
	if visible != "" {
		added["visible_name"] = visible
	}
	entries = append(entries, added)
	body, err := json.Marshal(map[string]any{input.Field: entries})
	if err != nil || len(body) > maxRequestBytes {
		return invalidRequest("the cell values exceed the request size limit" + notAttached)
	}
	_, err = c.send(ctx, c.http, op, http.MethodPatch, rowChangePath(input.TableID, input.RowID),
		url.Values{"user_field_names": {"true"}}, bytes.NewReader(body), "application/json", "update", attachUncertain)
	if err != nil {
		return appendHint(err, notAttached)
	}
	return nil
}

// cellFile is one file of a cell. Every value comes from the provider and is untrusted.
type cellFile struct {
	Name        string `json:"name"`
	VisibleName string `json:"visible_name"`
	URL         string `json:"url"`
	Size        int64  `json:"size"`
}

// cellFiles reads the current files of the named field of one row of the bound table.
func (c *Client) cellFiles(ctx context.Context, op string, input fileArguments) ([]cellFile, error) {
	var row map[string]json.RawMessage
	path := rowChangePath(input.TableID, input.RowID)
	if err := c.get(ctx, op, path, url.Values{"user_field_names": {"true"}}, &row); err != nil {
		return nil, err
	}
	value := bytes.TrimSpace(row[input.Field])
	if len(value) == 0 || string(value) == "null" {
		return nil, nil
	}
	var files []cellFile
	if json.Unmarshal(value, &files) != nil || len(files) > maxCellFiles {
		return nil, invalidResponse(op, "Baserow returned an unusable file cell")
	}
	for _, file := range files {
		if !validStoredName(file.Name) {
			return nil, invalidResponse(op, "Baserow returned an unusable file cell")
		}
	}
	return files, nil
}

func invokeFilesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get file"
	input, err := prepareFile(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	if input.Name != nil && input.Index != nil {
		return nil, invalidRequest("name and index exclude each other")
	}
	var download *localfile.Download
	if input.LocalPath != nil {
		if download, err = localfile.CreateForDownload(ctx, resolved, *input.LocalPath); err != nil {
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
	fields, err := client.readFields(ctx, op, input.TableID)
	if err != nil {
		return nil, err
	}
	if err := fileField(fields, input.Field, false); err != nil {
		return nil, err
	}
	cell, err := client.cellFiles(ctx, op, input)
	if err != nil {
		return nil, err
	}
	file, err := pickFile(op, cell, input)
	if err != nil {
		return nil, err
	}
	link, err := client.checkMediaURL(op, file)
	if err != nil {
		return nil, err
	}
	result, err := client.download(ctx, op, link, file, download)
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

// pickFile chooses one file of the current cell by name or index. Without a selector a cell with exactly
// one file is used. The address always comes from the cell, never from an argument.
func pickFile(op string, cell []cellFile, input fileArguments) (cellFile, error) {
	var picked []cellFile
	switch {
	case input.Index != nil:
		if *input.Index < len(cell) {
			picked = cell[*input.Index : *input.Index+1]
		}
	case input.Name != nil:
		for _, file := range cell {
			if file.VisibleName == *input.Name || file.Name == *input.Name {
				picked = append(picked, file)
			}
		}
	default:
		picked = cell
	}
	switch {
	case len(picked) == 0:
		return cellFile{}, providerError(op, "the cell holds no such file")
	case len(picked) > 1:
		return cellFile{}, providerError(op, "the selection is ambiguous, use index")
	}
	return picked[0], nil
}

// checkMediaURL accepts the address of a file only when it is https on the host of the connection and names
// exactly the stored file below the media directory of the instance. Another host, such as an object store or
// a CDN, cannot be shown to belong to the instance and is refused. A query (a signed address) is kept and
// registered with the redactor; the address is never reported.
func (c *Client) checkMediaURL(op string, file cellFile) (*url.URL, error) {
	refuse := func() (*url.URL, error) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "Baserow answered with a file address outside the configured service"}
	}
	base, err := url.Parse(c.origin)
	if err != nil || len(file.URL) > 4096 {
		return refuse()
	}
	parsed, err := url.Parse(strings.TrimSpace(file.URL))
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" ||
		!strings.EqualFold(parsed.Host, base.Host) {
		return refuse()
	}
	if parsed.Path != userFilesDir+file.Name && parsed.Path != strings.TrimSuffix(base.Path, "/")+userFilesDir+file.Name {
		return refuse()
	}
	if c.red != nil {
		c.red.Add(strings.TrimSpace(file.URL))
	}
	return parsed, nil
}

// download fetches the file without the token: the stored files are served publicly under an unguessable
// name, and the token belongs to the API only. With a download it streams into it, otherwise it returns the
// content inline up to 4 MiB.
func (c *Client) download(ctx context.Context, op string, link *url.URL, file cellFile, download *localfile.Download) (*FileResult, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, provider.Waited(op, "Baserow", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link.String(), nil)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("User-Agent", "qatlas-cli")
	response, err := c.transferClient().Do(req)
	if err != nil {
		return nil, provider.Transport(op, "Baserow", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, mediaStatusError(op, response.StatusCode)
	}
	hash := storedHash(file.Name)
	result := &FileResult{ID: bounded(file.Name), Name: bounded(firstNonEmpty(file.VisibleName, file.Name))}
	if download != nil {
		if response.ContentLength >= 0 {
			if err := download.ExpectSize(response.ContentLength); err != nil {
				return nil, err
			}
		}
		if hash != "" {
			if err := download.ExpectSHA256(hash); err != nil {
				return nil, err
			}
		}
		body := &trackedReader{Reader: response.Body}
		if _, err := download.ReadFrom(body); err != nil {
			if body.err != nil {
				return nil, provider.Transport(op, "Baserow", body.err)
			}
			return nil, err
		}
		return result, nil
	}
	if response.ContentLength > maxInlineFileBytes {
		return nil, providerError(op, "the file is larger than 4 MiB, use "+localfile.LocalPathArgument)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxInlineFileBytes+1))
	if err != nil {
		return nil, provider.Transport(op, "Baserow", err)
	}
	if len(data) > maxInlineFileBytes {
		return nil, providerError(op, "the file is larger than 4 MiB, use "+localfile.LocalPathArgument)
	}
	if response.ContentLength >= 0 && response.ContentLength != int64(len(data)) {
		return nil, invalidResponse(op, "Baserow delivered a different amount of content than it announced")
	}
	digest := sha256.Sum256(data)
	sum := hex.EncodeToString(digest[:])
	if hash != "" && !strings.EqualFold(hash, sum) {
		return nil, invalidResponse(op, "the downloaded content does not match the checksum in the file name")
	}
	result.Size, result.SHA256, result.Content = int64(len(data)), sum, base64.StdEncoding.EncodeToString(data)
	return result, nil
}

func mediaStatusError(op string, status int) error {
	switch {
	case status == http.StatusNotFound:
		return &provider.Error{Class: provider.ClassNotFound, Op: op, Message: "Baserow no longer serves this file"}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &provider.Error{Class: provider.ClassPermission, Op: op, Message: "Baserow refused to serve this file"}
	case status == http.StatusTooManyRequests:
		return &provider.Error{Class: provider.ClassRateLimited, Op: op, Message: "Baserow rate-limited the download"}
	case status == http.StatusServiceUnavailable:
		return &provider.Error{Class: provider.ClassUnreachable, Op: op, Message: "Baserow is unavailable or in maintenance"}
	case status == http.StatusGatewayTimeout:
		return &provider.Error{Class: provider.ClassTimeout, Op: op, Message: "Baserow did not serve the file in time"}
	case status >= 300 && status < 400:
		return &provider.Error{Class: provider.ClassProviderError, Op: op,
			Message: "Baserow answered with a redirect, which Qatlas does not follow for this request"}
	}
	return &provider.Error{Class: provider.ClassProviderError, Op: op,
		Message: "Baserow did not serve the file (HTTP " + strconv.Itoa(status) + ")"}
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

func appendHint(err error, hint string) error {
	var failure *provider.Error
	if errors.As(err, &failure) && !strings.HasSuffix(failure.Message, hint) {
		copied := *failure
		copied.Message += hint
		return &copied
	}
	return err
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// validFileName keeps a path, a control character, and an unusable length out of a shown file name.
func validFileName(name string) bool {
	if name == "" || len(name) > maxFileNameLen || !utf8.ValidString(name) || name == "." || name == ".." ||
		strings.ContainsAny(name, "/\\") {
		return false
	}
	return strings.IndexFunc(name, unicode.IsControl) < 0
}

// storedNamePattern is the form Baserow gives a stored file: a unique part, the SHA-256 of the content, and
// the extension.
var storedNamePattern = regexp.MustCompile(`^[A-Za-z0-9]{1,128}_([A-Za-z0-9]{1,128})\.[^\x00-\x1f\x7f/\\?#%]{0,100}$`)

func validStoredName(name string) bool {
	return len(name) <= maxFileNameLen && utf8.ValidString(name) && storedNamePattern.MatchString(name)
}

// storedHash returns the content hash a stored name carries, in lower case, or "" when the name carries none
// in the form of a SHA-256.
func storedHash(name string) string {
	match := storedNamePattern.FindStringSubmatch(name)
	if match == nil || len(match[1]) != sha256.Size*2 {
		return ""
	}
	if _, err := hex.DecodeString(match[1]); err != nil {
		return ""
	}
	return strings.ToLower(match[1])
}

// deniedSuffixes are names that never lead to a public host.
var deniedSuffixes = []string{"localhost", "local", "localdomain", "internal", "intranet", "lan", "home", "corp",
	"private", "test", "example", "invalid", "onion", "arpa", "alt"}

// checkPublicURL is the positive list for a URL Baserow fetches on its own: https, a public DNS host name,
// the default port, no user info, no fragment, no numeric address. The list is syntactic. Whether a name
// resolves to a private address is settled by Baserow's own guard when it fetches, not by Qatlas. No message
// quotes the URL.
func checkPublicURL(raw string) error {
	const reason = "url must be an https URL on a public host name, without user info, port, fragment, or numeric address"
	if raw == "" || len(raw) > maxFileURLLen || !utf8.ValidString(raw) || strings.ContainsAny(raw, "\\ ") ||
		strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return invalidRequest(reason)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" ||
		parsed.Port() != "" || strings.HasSuffix(parsed.Host, ":") || !publicHostName(strings.ToLower(parsed.Hostname())) {
		return invalidRequest(reason)
	}
	return nil
}

// publicHostName accepts a DNS name of ASCII labels with at least two labels and an alphabetic top-level
// label, which excludes every numeric address form, and refuses names reserved for internal use.
func publicHostName(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			ch := label[i]
			if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
				return false
			}
		}
	}
	tld := labels[len(labels)-1]
	if !strings.HasPrefix(tld, "xn--") {
		for i := 0; i < len(tld); i++ {
			if tld[i] < 'a' || tld[i] > 'z' {
				return false
			}
		}
	}
	for _, suffix := range deniedSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return false
		}
	}
	return true
}
