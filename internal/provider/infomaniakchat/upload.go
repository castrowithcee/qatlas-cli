package infomaniakchat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
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
	// maxUploadBytes is the fixed local bound of one uploaded file; kChat's own limit may be lower.
	maxUploadBytes = 50 << 20
	// maxInlineFileBytes bounds a file that travels inline as base64, and maxInlineBase64 is its encoded length.
	maxInlineFileBytes = 4 << 20
	maxInlineBase64    = (maxInlineFileBytes + 2) / 3 * 4
	// maxFileNameBytes bounds the name of an uploaded file.
	maxFileNameBytes = 255
	// uploadTimeout replaces the short request timeout for the one request that carries file content.
	uploadTimeout = 10 * time.Minute
)

// uncertainUpload is appended to a failure of the upload request that may have reached kChat: the file may
// be stored although no confirmation arrived. kChat has no operation to list or remove an unattached upload.
const uncertainUpload = "; the file may have been uploaded and cannot be listed or removed through Qatlas, " +
	"repeating the upload may leave a duplicate"

var uploadNameSchema = `{"type":"string","minLength":1,"maxLength":` + itoa(maxFileNameBytes) + `}`

var filesUpload = capability.Descriptor{
	ID:      Provider + ".files.upload",
	Version: 1,
	Title:   "Upload a file to an Infomaniak kChat channel",
	Description: "Upload exactly one confirmed file to a channel this connection may reach, from local_path in a " +
		"directory the connection releases for reading, or inline from content_base64 up to 4 MiB with a name. " +
		"The result is a file_id to attach with messages.send; an upload that is never attached stays in kChat, " +
		"which offers no way to remove it. An unclear outcome is never repeated",
	Tags:       []string{"infomaniak", "kchat", "files", "upload"},
	Risk:       uploadRisk,
	Provider:   Provider,
	LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `,"name":` +
		uploadNameSchema + `,"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `,` +
		`"` + localfile.ContentArgument + `":{"type":"string","maxLength":` + strconv.Itoa(maxInlineBase64) + `}},` +
		`"required":["channel_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"file_id":` + idSchema + `,` +
		`"name":{"type":"string"},"size":{"type":"integer"},"mime_type":{"type":"string"}},` +
		`"required":["file_id","name","size"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		channelIDArgument,
		{Name: "name", Description: "File name without a path, 1 to " + itoa(maxFileNameBytes) + " bytes; " +
			"required with " + localfile.ContentArgument + " and not allowed with " + localfile.LocalPathArgument +
			", where the name of the local file is used"},
		localfile.UploadPathArgument(),
		{Name: localfile.ContentArgument, Description: "File content as base64, up to 4 MiB; instead of " +
			localfile.LocalPathArgument},
	},
	Fields: []capability.Field{
		{Name: "file_id", Description: "Identifier of the uploaded file, to pass in file_ids of messages.send"},
		{Name: "name", Description: "File name kChat reports, untrusted data"},
		{Name: "size", Description: "Number of bytes sent"},
		{Name: "mime_type", Description: "Media type as kChat reports it, untrusted data"},
	},
	Examples: []capability.Example{{Description: "Upload a local file to a channel",
		Arguments: json.RawMessage(`{"channel_id":"abc123channel00000000000000","local_path":"~/uploads/report.pdf"}`)}},
}

var uploadRisk = capability.Risk{
	Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
	Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: filesSensitivity,
}

// UploadResult is the answer of one confirmed upload. It never carries the content.
type UploadResult struct {
	FileID   string `json:"file_id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	MimeType string `json:"mime_type,omitempty"`
}

type uploadArguments struct {
	ChannelID string  `json:"channel_id"`
	Name      *string `json:"name"`
	LocalPath *string `json:"local_path"`
	Content   *string `json:"content_base64"`
}

// validFileName accepts a plain file name: valid UTF-8, no path separator, no control character, and not a
// relative path element.
func validFileName(name string) bool {
	if name == "" || len(name) > maxFileNameBytes || name == "." || name == ".." || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

func invokeFilesUpload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "upload file"
	var input uploadArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectChannel(resolved, input.ChannelID); err != nil {
		return nil, err
	}
	// The content is settled before the credential is resolved, so a source outside the release, an invalid
	// name, or an oversized file is refused without secret access or provider I/O.
	kind, err := localfile.UploadSource(input.LocalPath, input.Content)
	if err != nil {
		return nil, err
	}
	var (
		name   string
		size   int64
		body   io.Reader
		stream *localfile.Upload
	)
	if kind == localfile.SourceLocalPath {
		if input.Name != nil {
			return nil, invalidRequest("name is only used with " + localfile.ContentArgument)
		}
		stream, err = localfile.OpenForUpload(ctx, resolved, *input.LocalPath)
		if err != nil {
			return nil, err
		}
		defer stream.Close()
		name, size, body = stream.Name, stream.Size, stream
	} else {
		if input.Name == nil {
			return nil, invalidRequest("name is required with " + localfile.ContentArgument)
		}
		if len(*input.Content) > maxInlineBase64 {
			return nil, invalidRequest("inline content is limited to 4 MiB, use " + localfile.LocalPathArgument)
		}
		content, err := base64.StdEncoding.DecodeString(*input.Content)
		if err != nil {
			return nil, invalidRequest(localfile.ContentArgument + " is not valid base64")
		}
		if len(content) > maxInlineFileBytes {
			return nil, invalidRequest("inline content is limited to 4 MiB, use " + localfile.LocalPathArgument)
		}
		name, size, body = *input.Name, int64(len(content)), bytes.NewReader(content)
	}
	if !validFileName(name) {
		return nil, invalidRequest("the file name must be a plain name of 1 to " + itoa(maxFileNameBytes) +
			" bytes without a path or control characters")
	}
	if size > maxUploadBytes {
		return nil, invalidRequest("the file is larger than the upload limit of " + itoa(maxUploadBytes>>20) + " MiB")
	}
	multipartBody, err := buildMultipart(input.ChannelID, name, size, body)
	if err != nil {
		return nil, invalidRequest("the file name cannot be sent")
	}

	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if _, err := client.verifyChannelScope(ctx, op, input.ChannelID); err != nil {
		return nil, err
	}
	return client.upload(ctx, op, input.ChannelID, size, stream, multipartBody)
}

// buildMultipart frames the one file as a multipart/form-data body with exactly the two parts kChat takes:
// channel_id first, then the file. The content is streamed between the fixed prefix and suffix.
func buildMultipart(channelID, name string, size int64, content io.Reader) (rawBody, error) {
	disposition := mime.FormatMediaType("form-data", map[string]string{"name": "files", "filename": name})
	if disposition == "" {
		return rawBody{}, io.ErrUnexpectedEOF
	}
	var head bytes.Buffer
	writer := multipart.NewWriter(&head)
	if err := writer.WriteField("channel_id", channelID); err != nil {
		return rawBody{}, err
	}
	if _, err := writer.CreatePart(map[string][]string{"Content-Disposition": {disposition},
		"Content-Type": {"application/octet-stream"}}); err != nil {
		return rawBody{}, err
	}
	prefix := append([]byte(nil), head.Bytes()...)
	if err := writer.Close(); err != nil {
		return rawBody{}, err
	}
	suffix := append([]byte(nil), head.Bytes()[len(prefix):]...)
	return rawBody{
		reader:      io.MultiReader(bytes.NewReader(prefix), content, bytes.NewReader(suffix)),
		length:      int64(len(prefix)) + size + int64(len(suffix)),
		contentType: writer.FormDataContentType(),
	}, nil
}

// uploadAnswer is the subset of kChat's answer to an upload this provider reads.
type uploadAnswer struct {
	FileInfos []fileJSON `json:"file_infos"`
}

// upload sends the one upload request, once, and never repeats it.
func (c *Client) upload(ctx context.Context, op, channelID string, size int64, local *localfile.Upload,
	body rawBody) (*UploadResult, error) {
	long := *c.http
	long.Timeout = uploadTimeout
	var answer uploadAnswer
	if err := c.exchange(ctx, &long, op, http.MethodPost, "/api/v4/files", nil, body, &answer,
		uncertainUpload); err != nil {
		return nil, err
	}
	if local != nil {
		// The file must have ended where it was announced to, so what kChat stored is what was released.
		if _, err := io.Copy(io.Discard, local); err != nil {
			return nil, providerError(op, "the local file changed while it was uploaded"+uncertainUpload)
		}
		if _, ok := local.SHA256(); !ok {
			return nil, providerError(op, "the local file could not be read completely"+uncertainUpload)
		}
	}
	if len(answer.FileInfos) != 1 {
		return nil, provider.InvalidResponse(op, "kChat answered the upload without exactly one file"+uncertainUpload)
	}
	info := answer.FileInfos[0]
	if !validMattermostID(info.ID) || info.Size != size || (info.ChannelID != "" && info.ChannelID != channelID) {
		return nil, provider.InvalidResponse(op, "kChat answered the upload with an unusable file"+uncertainUpload)
	}
	return &UploadResult{FileID: info.ID, Name: bounded(info.Name), Size: info.Size, MimeType: bounded(info.MimeType)}, nil
}
