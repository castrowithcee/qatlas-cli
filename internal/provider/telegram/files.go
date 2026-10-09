package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
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

const (
	groupFiles = "files"

	// maxDownloadBytes is the Bot API's limit for a file a bot may download.
	maxDownloadBytes = 20 << 20
	// downloadTimeout bounds the whole transfer of one file, which the short request timeout would cut off.
	downloadTimeout = 5 * time.Minute
	maxFilePathLen  = 512
)

var fileRefArgument = capability.Argument{Name: "file_ref", Required: true,
	Description: "Signed file reference (media.file_ref) of a message in a bound chat, as telegram.updates.list returns it"}

const fileRefSchema = `{"type":"string","minLength":1,"maxLength":2048}`

var filesGet = capability.Descriptor{
	ID:          Provider + ".files.get",
	Version:     1,
	Title:       "Read the metadata of a Telegram file",
	Description: "Read the size and stable identifier of one file of a message in a bound chat, addressed by its signed file reference",
	Tags:        []string{"telegram", "files", "read"},
	Risk: capability.Risk{
		Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: dataSensitivity,
	},
	Provider: Provider,
	Group:    groupFiles,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"file_ref":` + fileRefSchema + `},` +
		`"required":["file_ref"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"file_unique_id":{"type":"string"},` +
		`"file_size":{"type":"integer"}},"required":["file_unique_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{fileRefArgument},
	Fields: []capability.Field{
		{Name: "file_unique_id", Description: "Identifier of the file that stays the same across bots and time"},
		{Name: "file_size", Description: "Size in bytes as Telegram reports it; absent when Telegram does not report one"},
	},
	Examples: []capability.Example{{
		Description: "Read the size of a file received in a bound chat",
		Arguments:   json.RawMessage(`{"file_ref":"AQ..."}`),
	}},
}

var filesDownload = capability.Descriptor{
	ID:      Provider + ".files.download",
	Version: 1,
	Title:   "Download a Telegram file to a local path",
	Description: "Write one file of a message in a bound chat, addressed by its signed file reference, to local_path, in a " +
		"directory the connection releases for writing. Files above 20 MB are refused. The content is never returned, " +
		"only its size and SHA-256. An existing local file is replaced only with confirmation, and an incomplete " +
		"transfer leaves no file",
	Tags: []string{"telegram", "files", "download", "local"},
	Risk: capability.Risk{
		Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: dataSensitivity,
	},
	Provider:   Provider,
	Group:      groupFiles,
	LocalFiles: config.LocalFilesWrite,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"file_ref":` + fileRefSchema + `,` +
		`"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},` +
		`"required":["file_ref","` + localfile.LocalPathArgument + `"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"file_unique_id":{"type":"string"},` +
		`"size":{"type":"integer"},"sha256":{"type":"string"}},` +
		`"required":["file_unique_id","size","sha256"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		fileRefArgument,
		func() capability.Argument {
			a := localfile.DownloadPathArgument()
			a.Required = true
			return a
		}(),
	},
	Fields: []capability.Field{
		{Name: "file_unique_id", Description: "Identifier of the file that stays the same across bots and time"},
		{Name: "size", Description: "Size of the written file in bytes"},
		{Name: "sha256", Description: "SHA-256 of the written content as hex"},
	},
	Examples: []capability.Example{{
		Description: "Write a file received in a bound chat to a released local directory",
		Arguments:   json.RawMessage(`{"file_ref":"AQ...","local_path":"~/downloads/photo.jpg"}`),
	}},
}

// DownloadResult is what files.download reports: metadata of the written file, never its content.
type DownloadResult struct {
	FileUniqueID string `json:"file_unique_id"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
}

// fileInfo is the part of Telegram's File object the tools use. FilePath is never reported.
type fileInfo struct {
	UniqueID string `json:"file_unique_id"`
	Size     int64  `json:"file_size"`
	Path     string `json:"file_path"`
}

func invokeFilesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeFilesGetWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeFilesGetWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "get file"
	var input struct {
		FileRef string `json:"file_ref"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	// Format, kind, and binding are checked before the credential is resolved.
	parsed, err := parseRef(resolved, refFile, input.FileRef)
	if err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	return client.fileMetadata(ctx, parsed)
}

func (c *Client) fileMetadata(ctx context.Context, parsed parsedRef) (map[string]any, error) {
	info, err := c.getFile(ctx, "get file", parsed)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"file_unique_id": capText(info.UniqueID, maxNameRunes)}
	if info.Size > 0 {
		out["file_size"] = info.Size
	}
	return out, nil
}

// getFile verifies the reference's signature, then resolves the file with the fixed getFile method.
func (c *Client) getFile(ctx context.Context, op string, parsed parsedRef) (fileInfo, error) {
	fileID, err := c.checkRef(parsed)
	if err != nil {
		return fileInfo{}, err
	}
	raw, err := c.call(ctx, spec{op: op, method: "getFile", limit: defaultResponseBytes, readOnly: true},
		struct {
			FileID string `json:"file_id"`
		}{FileID: fileID})
	if err != nil {
		return fileInfo{}, err
	}
	var info fileInfo
	if json.Unmarshal(raw, &info) != nil || info.UniqueID == "" || info.Size < 0 {
		return fileInfo{}, invalidResponse(op)
	}
	return info, nil
}

func invokeFilesDownload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeFilesDownloadWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeFilesDownloadWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "download file"
	var input struct {
		FileRef   string `json:"file_ref"`
		LocalPath string `json:"local_path"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	parsed, err := parseRef(resolved, refFile, input.FileRef)
	if err != nil {
		return nil, err
	}
	// The local target is prepared before the credential is resolved, so a path outside the release is
	// refused without secret access or provider I/O.
	download, err := localfile.CreateForDownload(ctx, resolved, input.LocalPath)
	if err != nil {
		return nil, err
	}
	done := false
	defer func() {
		if !done {
			_ = download.Abort()
		}
	}()
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	info, err := client.getFile(ctx, op, parsed)
	if err != nil {
		return nil, err
	}
	if info.Size > maxDownloadBytes {
		return nil, providerError(op, "the file is larger than the 20 MB Telegram allows bots to download")
	}
	filePath, ok := escapeFilePath(info.Path)
	if !ok {
		return nil, invalidResponse(op)
	}
	if info.Size > 0 {
		if err := download.ExpectSize(info.Size); err != nil {
			return nil, err
		}
	}
	response, err := client.openFile(ctx, op, filePath)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if info.Size > 0 && response.ContentLength >= 0 && response.ContentLength != info.Size {
		return nil, providerError(op, "the file size differs from the size Telegram reported")
	}
	// ReadFrom stops one byte past a reported size; the reader caps the read at 20 MB when none was reported.
	if _, err := download.ReadFrom(io.LimitReader(response.Body, maxDownloadBytes+1)); err != nil {
		return nil, transferError(op, err)
	}
	if download.Size() > maxDownloadBytes {
		return nil, providerError(op, "the file is larger than the 20 MB Telegram allows bots to download")
	}
	done = true
	if err := download.Commit(); err != nil {
		return nil, err
	}
	sum, _ := download.SHA256()
	return &DownloadResult{FileUniqueID: capText(info.UniqueID, maxNameRunes), Size: download.Size(), SHA256: sum}, nil
}

// escapeFilePath validates the file_path of a getFile answer and escapes it segment by segment. Telegram's
// path is a relative sequence of plain names; anything else is not followed.
func escapeFilePath(p string) (string, bool) {
	if p == "" || len(p) > maxFilePathLen || !utf8.ValidString(p) || strings.HasPrefix(p, "/") ||
		strings.ContainsAny(p, `\`) {
		return "", false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	segments := strings.Split(p, "/")
	for i, s := range segments {
		if s == "" || s == "." || s == ".." {
			return "", false
		}
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/"), true
}

// openFile starts the content transfer of one file. Method and path prefix are fixed, the client never
// follows a redirect, and the URL, which carries the token, appears in no error.
func (c *Client) openFile(ctx context.Context, op, escapedPath string) (*http.Response, error) {
	target := *c.base
	escaped := strings.TrimRight(target.EscapedPath(), "/") + "/file/bot" + c.token + "/" + escapedPath
	unescaped, err := url.PathUnescape(escaped)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	target.Path, target.RawPath = unescaped, escaped
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Accept", "*/*")
	long := *c.http
	long.Timeout = downloadTimeout
	response, err := long.Do(req)
	if err != nil {
		return nil, provider.Transport(op, "Telegram", err)
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		return nil, statusError(op, response.StatusCode)
	}
	return response, nil
}

// transferError reports a failed transfer without any provider text. A local file problem keeps its own
// error, which never names the path.
func transferError(op string, err error) error {
	var integrity *localfile.IntegrityError
	var path *localfile.PathError
	if errors.As(err, &integrity) || errors.As(err, &path) {
		return err
	}
	return providerError(op, "the transfer ended before the file was complete")
}
