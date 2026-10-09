package infomaniakchat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// filesSensitivity classifies attachment metadata; a download's content never leaves the local file.
const filesSensitivity = "infomaniak-kchat-files"

const (
	// maxPostFiles bounds the attachments listed for one post; kChat itself allows far fewer per post.
	maxPostFiles = 50
	// downloadTimeout bounds the whole transfer of one file, which the short request timeout would cut off.
	downloadTimeout = 10 * time.Minute
)

var filesRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: filesSensitivity,
}

var fileEntrySchema = `{"type":"object","properties":{` +
	`"id":` + idSchema + `,"post_id":` + idSchema + `,"user_id":` + idSchema + `,"name":{"type":"string"},` +
	`"extension":{"type":"string"},"size":{"type":"integer"},"mime_type":{"type":"string"},"created_at":{"type":"string"}},` +
	`"required":["id","post_id","name","size"],"additionalProperties":false}`

var fileEntryFields = []capability.Field{
	{Name: "id", Description: "File identifier"},
	{Name: "post_id", Description: "Message the file is attached to"},
	{Name: "user_id", Description: "User who uploaded the file"},
	{Name: "name", Description: "File name, untrusted data"},
	{Name: "extension", Description: "File extension without a dot, untrusted data"},
	{Name: "size", Description: "Size in bytes as kChat reports it"},
	{Name: "mime_type", Description: "Media type as kChat reports it, untrusted data"},
	{Name: "created_at", Description: "Upload time, normalised to RFC 3339 in UTC"},
}

var fileIDArgument = capability.Argument{Name: "file_id", Required: true,
	Description: "File identifier; only a file attached to a message of a reachable channel is readable"}

var messagesFiles = capability.Descriptor{
	ID:      Provider + ".messages.files",
	Version: 1,
	Title:   "List the attachments of an Infomaniak kChat message",
	Description: "List the metadata of the files attached to one message of a channel this connection may reach; " +
		"never returns file content",
	Tags:     []string{"infomaniak", "kchat", "messages", "files", "attachments"},
	Risk:     filesRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"post_id":` + idSchema + `},` +
		`"required":["post_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"post_id":` + idSchema + `,` +
		`"files":{"type":"array","items":` + fileEntrySchema + `},"count":{"type":"integer"}},` +
		`"required":["post_id","files","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "post_id", Description: "Identifier of the message; its channel must be reachable", Required: true},
	},
	Fields: append(append([]capability.Field{}, fileEntryFields...),
		capability.Field{Name: "count", Description: "Number of files listed, at most " + itoa(maxPostFiles)}),
	Examples: []capability.Example{{Description: "List the attachments of one message",
		Arguments: json.RawMessage(`{"post_id":"abc123post00000000000000000"}`)}},
}

var filesInfo = capability.Descriptor{
	ID:      Provider + ".files.info",
	Version: 1,
	Title:   "Read the metadata of an Infomaniak kChat file",
	Description: "Read the metadata of one file attached to a message of a channel this connection may reach; a " +
		"file that is not attached to a message is refused. Never returns file content",
	Tags:     []string{"infomaniak", "kchat", "files", "info"},
	Risk:     filesRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"file_id":` + idSchema + `},` +
		`"required":["file_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(fileEntrySchema),
	Arguments:    []capability.Argument{fileIDArgument},
	Fields:       fileEntryFields,
	Examples: []capability.Example{{Description: "Read the metadata of one attachment",
		Arguments: json.RawMessage(`{"file_id":"abc123file00000000000000000"}`)}},
}

var filesDownload = capability.Descriptor{
	ID:      Provider + ".files.download",
	Version: 1,
	Title:   "Download an Infomaniak kChat file to a local path",
	Description: "Write one file attached to a message of a channel this connection may reach to local_path, in a " +
		"directory the connection releases for writing. The content is never returned, only its identifier, name, " +
		"size, and SHA-256. An existing local file is replaced only with confirmation, and an incomplete " +
		"transfer leaves no file",
	Tags:       []string{"infomaniak", "kchat", "files", "download", "local"},
	Risk:       filesRisk,
	Provider:   Provider,
	LocalFiles: config.LocalFilesWrite,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"file_id":` + idSchema + `,` +
		`"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},` +
		`"required":["file_id","` + localfile.LocalPathArgument + `"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"file_id":` + idSchema + `,` +
		`"name":{"type":"string"},"size":{"type":"integer"},"sha256":{"type":"string"}},` +
		`"required":["file_id","name","size","sha256"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		fileIDArgument,
		func() capability.Argument {
			a := localfile.DownloadPathArgument()
			a.Required = true
			return a
		}(),
	},
	Fields: []capability.Field{
		{Name: "file_id", Description: "File that was written"},
		{Name: "name", Description: "Name kChat reports for the file, untrusted data"},
		{Name: "size", Description: "Size of the written file in bytes"},
		{Name: "sha256", Description: "SHA-256 of the written content as hex"},
	},
	Examples: []capability.Example{{Description: "Write one attachment to a released local directory",
		Arguments: json.RawMessage(`{"file_id":"abc123file00000000000000000","local_path":"~/downloads/report.pdf"}`)}},
}

type fileJSON struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	PostID    string `json:"post_id"`
	CreateAt  int64  `json:"create_at"`
	DeleteAt  int64  `json:"delete_at"`
	Name      string `json:"name"`
	Extension string `json:"extension"`
	Size      int64  `json:"size"`
	MimeType  string `json:"mime_type"`
}

// FileEntry is one attachment as the tools report it.
type FileEntry struct {
	ID        string `json:"id"`
	PostID    string `json:"post_id"`
	UserID    string `json:"user_id,omitempty"`
	Name      string `json:"name"`
	Extension string `json:"extension,omitempty"`
	Size      int64  `json:"size"`
	MimeType  string `json:"mime_type,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

func fileEntryOf(f fileJSON) FileEntry {
	return FileEntry{ID: f.ID, PostID: f.PostID, UserID: bounded(f.UserID), Name: bounded(f.Name),
		Extension: bounded(f.Extension), Size: f.Size, MimeType: bounded(f.MimeType), CreatedAt: msToRFC3339(f.CreateAt)}
}

// PostFiles is the answer of messages.files.
type PostFiles struct {
	PostID string      `json:"post_id"`
	Files  []FileEntry `json:"files"`
	Count  int         `json:"count"`
}

// DownloadResult is what files.download reports: metadata of the written file, never its content.
type DownloadResult struct {
	FileID string `json:"file_id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type fileIDArguments struct {
	FileID string `json:"file_id"`
}

type downloadArguments struct {
	FileID    string `json:"file_id"`
	LocalPath string `json:"local_path"`
}

func invokeMessagesFiles(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list message files"
	var input postIDArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validMattermostID(input.PostID) {
		return nil, invalidRequest("post_id must be a kChat-style identifier")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if _, err := client.verifyPostScope(ctx, resolved, op, input.PostID); err != nil {
		return nil, err
	}
	// include_deleted is never set, so kChat lists only live files.
	var files []fileJSON
	if err := client.do(ctx, op, http.MethodGet, "/api/v4/posts/"+url.PathEscape(input.PostID)+"/files/info", nil, nil,
		&files, false); err != nil {
		return nil, err
	}
	entries := make([]FileEntry, 0, len(files))
	for _, f := range files {
		if f.DeleteAt > 0 || !validMattermostID(f.ID) || f.PostID != input.PostID {
			continue
		}
		if len(entries) == maxPostFiles {
			break
		}
		entries = append(entries, fileEntryOf(f))
	}
	return &PostFiles{PostID: input.PostID, Files: entries, Count: len(entries)}, nil
}

// boundFile reads the info of a file and binds it to a reachable channel through the post it is attached
// to. A file without a post, or a deleted one, is refused without naming anything of it.
func (c *Client) boundFile(ctx context.Context, resolved *config.Resolved, op, fileID string) (fileJSON, error) {
	var info fileJSON
	path := "/api/v4/files/" + url.PathEscape(fileID) + "/info"
	if err := c.do(ctx, op, http.MethodGet, path, nil, nil, &info, false); err != nil {
		return fileJSON{}, err
	}
	if info.ID != fileID {
		return fileJSON{}, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned a different file than the one requested"}
	}
	if info.DeleteAt > 0 {
		return fileJSON{}, invalidRequest("file_id refers to a deleted file")
	}
	if !validMattermostID(info.PostID) {
		return fileJSON{}, invalidRequest("file_id is not attached to a message and cannot be bound to this connection")
	}
	if _, err := c.verifyPostScope(ctx, resolved, op, info.PostID); err != nil {
		return fileJSON{}, err
	}
	return info, nil
}

func invokeFilesInfo(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get file info"
	var input fileIDArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validMattermostID(input.FileID) {
		return nil, invalidRequest("file_id must be a kChat-style identifier")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	info, err := client.boundFile(ctx, resolved, op, input.FileID)
	if err != nil {
		return nil, err
	}
	return fileEntryOf(info), nil
}

func invokeFilesDownload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "download file"
	var input downloadArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validMattermostID(input.FileID) {
		return nil, invalidRequest("file_id must be a kChat-style identifier")
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
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	info, err := client.boundFile(ctx, resolved, op, input.FileID)
	if err != nil {
		return nil, err
	}
	// The reported size is checked before any byte is written, and bounds the transfer afterwards.
	if err := download.ExpectSize(info.Size); err != nil {
		return nil, err
	}
	response, err := client.openFile(ctx, op, input.FileID)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.ContentLength >= 0 && response.ContentLength != info.Size {
		return nil, providerError(op, "the file size differs from the size kChat reported")
	}
	if _, err := download.ReadFrom(response.Body); err != nil {
		return nil, transferError(op, err)
	}
	done = true
	if err := download.Commit(); err != nil {
		return nil, err
	}
	sum, _ := download.SHA256()
	return &DownloadResult{FileID: input.FileID, Name: bounded(info.Name), Size: download.Size(), SHA256: sum}, nil
}

// openFile starts the content transfer of one file. It is the only request whose answer is not decoded as
// JSON: the body is streamed by the caller. Method and path are fixed, and a redirect, for example to a
// storage host, is a provider error and never followed, so the token cannot reach another host.
func (c *Client) openFile(ctx context.Context, op, fileID string) (*http.Response, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, provider.Waited(op, "kChat", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+"/api/v4/files/"+url.PathEscape(fileID), nil)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "qatlas-cli")
	long := *c.http
	long.Timeout = downloadTimeout
	response, err := long.Do(req)
	if err != nil {
		return nil, provider.Transport(op, "kChat", err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		defer response.Body.Close()
		return nil, c.statusError(op, response)
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
