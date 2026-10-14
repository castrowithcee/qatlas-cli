package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var noteAttachmentsUpload = capability.Descriptor{
	ID: Provider + ".noteattachments.upload", Version: 1, Title: "Upload a Nextcloud note attachment",
	Description: "Attach exactly one confirmed local file to a note in a category this connection binds. " +
		"local_path is a file up to 64 MiB in a directory the connection releases for reading; it keeps its file " +
		"name and its content is reported by hash and size only. One read-only GET of the note refuses a foreign " +
		"or read-only note, then one POST follows and is never repeated after an unclear result",
	Tags: []string{"nextcloud", "notes", "attachments", "upload", "local"}, Provider: Provider,
	Risk: notesWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + noteIDSchema + `,"` + localfile.LocalPathArgument +
		`":` + localfile.LocalPathSchema + `},"required":["id","` + localfile.LocalPathArgument + `"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"uploaded":{"type":"boolean"},"id":{"type":"string"},` +
		`"path":{"type":"string"},"name":{"type":"string"},"size":{"type":"integer"},"sha256":{"type":"string"},` +
		`"truncated":{"type":"boolean"}},"required":["uploaded","id","path","name","size"],"additionalProperties":false}`),
	Arguments: []capability.Argument{noteIDArgument,
		{Name: localfile.LocalPathArgument, Required: true, Description: "Local file to attach, absolute or " +
			"starting with ~/, inside a directory the connection releases for reading"}},
	Fields: []capability.Field{
		{Name: "uploaded", Description: "True when Nextcloud stored the attachment"},
		{Name: "id", Description: "Note the attachment belongs to"},
		{Name: "path", Description: "Path of the attachment relative to the note as Nextcloud reports it, untrusted data"},
		{Name: "name", Description: "File name sent, from local_path"},
		{Name: "size", Description: "Size of the content in bytes"},
		{Name: "sha256", Description: "SHA-256 of the sent content as hex"},
		{Name: "truncated", Description: "True when path was cut"},
	},
	Examples: []capability.Example{{Description: "Attach a released local image",
		Arguments: json.RawMessage(`{"id":"76","local_path":"~/uploads/image.png"}`)}},
}

// NoteAttachmentUpload reports an upload: metadata of the sent file, never content.
type NoteAttachmentUpload struct {
	Uploaded  bool   `json:"uploaded"`
	ID        string `json:"id"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

type noteAttachmentUploadArguments struct {
	ID        string  `json:"id"`
	LocalPath *string `json:"local_path"`
}

// UploadNoteAttachment sends the open local file as one multipart request after the note was found in a
// bound category and is writable.
func (c *Client) UploadNoteAttachment(ctx context.Context, sel selection, id string, upload *localfile.Upload) (*NoteAttachmentUpload, error) {
	const op = "upload note attachment"
	current, err := c.fetchNote(ctx, op, sel, id, false)
	if err != nil {
		return nil, err
	}
	if current.Readonly {
		return nil, noteReadOnly(op)
	}
	head, tail, contentType, err := multipartFrame(upload.Name, false)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	body := io.MultiReader(bytes.NewReader(head), upload, bytes.NewReader(tail))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.notesURL(nil, "attachment", id), body)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.ContentLength = int64(len(head)) + upload.Size + int64(len(tail))
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", contentType)
	response, err := c.transferClient().Do(req)
	if err != nil {
		return nil, sentTransportError(op, err, noteKind.uncertain)
	}
	defer response.Body.Close()
	switch status := response.StatusCode; {
	case status == http.StatusNotFound:
		return nil, noteNotFound(op)
	case status == http.StatusInsufficientStorage:
		return nil, statusError(op, status)
	case status < 200 || status >= 300:
		return nil, sentStatusError(op, status, noteKind.uncertain)
	}
	data, err := readLimited(response.Body, maxBodyBytes)
	var answer struct {
		Filename string `json:"filename"`
	}
	if err != nil || json.Unmarshal(data, &answer) != nil || answer.Filename == "" {
		return nil, withUncertainty(invalidResponse(op, "the Nextcloud attachment answer could not be read"), noteKind.uncertain)
	}
	path, cut := cutNoteText(answer.Filename, maxValueLength)
	sum, _ := upload.SHA256()
	return &NoteAttachmentUpload{Uploaded: true, ID: id, Path: path, Name: upload.Name, Size: upload.Size, SHA256: sum,
		Truncated: cut}, nil
}

func invokeNoteAttachmentsUpload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "upload note attachment"
	var input noteAttachmentUploadArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	sel, err := requireNotes(resolved)
	if err != nil {
		return nil, err
	}
	if !validNoteID(input.ID) {
		return nil, providerError(op, "the note ID must be an id reported by "+notesList.ID)
	}
	if input.LocalPath == nil {
		return nil, providerError(op, "local_path is required")
	}
	// The local file is checked before the credential is resolved or any request is made.
	upload, err := localfile.OpenForUpload(ctx, resolved, *input.LocalPath)
	if err != nil {
		return nil, err
	}
	defer upload.Close()
	if upload.Size > maxPathUploadBytes {
		return nil, providerError(op, "the local file exceeds the limit of 64 MiB for one note attachment")
	}
	if err := attachmentFileName(op, upload.Name); err != nil {
		return nil, err
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.UploadNoteAttachment(ctx, sel, input.ID, upload)
}
