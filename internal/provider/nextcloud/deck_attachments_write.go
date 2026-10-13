package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Attachments are added from a local file as deck_file and removed by ID. Both changes first read the board
// through the bound ID; delete also reads the attachment list, because Deck deletes by ID alone.

const (
	uncertainDeckAttachment = "; the change may have been applied, check deckattachments.list before repeating"
	maxAttachmentNameBytes  = 255
)

var deckAttachmentsUpload = capability.Descriptor{
	ID: Provider + ".deckattachments.upload", Version: 1, Title: "Upload a Nextcloud Deck card attachment",
	Description: "Attach exactly one confirmed local file as type deck_file to an active card of a bound Deck board. " +
		"local_path is a file up to 64 MiB in a directory the connection releases for reading; it keeps its file " +
		"name and its content is reported by hash and size only. One read of the board stacks, which must show the " +
		"card in the named stack, precedes exactly one request",
	Tags: []string{"nextcloud", "deck", "cards", "attachments", "upload", "local"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"stack_id":` +
		deckIDSchema + `,"card_id":` + deckIDSchema + `,"` + localfile.LocalPathArgument + `":` +
		localfile.LocalPathSchema + `},"required":["board_id","stack_id","card_id","` + localfile.LocalPathArgument +
		`"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"uploaded":{"type":"boolean"},"attachment_id":` +
		`{"type":"string"},"name":{"type":"string"},"size":{"type":"integer"},"sha256":{"type":"string"}},` +
		`"required":["uploaded","attachment_id","name","size"],"additionalProperties":false}`),
	Arguments: []capability.Argument{deckBoardIDArgument, deckStackOfCardArgument, deckCardIDArgument,
		{Name: localfile.LocalPathArgument, Required: true, Description: "Local file to attach, absolute or " +
			"starting with ~/, inside a directory the connection releases for reading"}},
	Fields: []capability.Field{
		{Name: "uploaded", Description: "True when Deck stored the attachment"},
		{Name: "attachment_id", Description: "ID of the new attachment"},
		{Name: "name", Description: "File name sent, from local_path"},
		{Name: "size", Description: "Size of the content in bytes"},
		{Name: "sha256", Description: "SHA-256 of the sent content as hex"},
	},
	Examples: []capability.Example{{Description: "Attach a released local file",
		Arguments: json.RawMessage(`{"board_id":"7","stack_id":"3","card_id":"21","local_path":"~/uploads/plan.pdf"}`)}},
}

var deckAttachmentsDelete = capability.Descriptor{
	ID: Provider + ".deckattachments.delete", Version: 1, Title: "Delete a Nextcloud Deck card attachment",
	Description: "Delete exactly one confirmed attachment of type deck_file of an active card of a bound Deck board; " +
		"Deck keeps it as deleted, Qatlas does not restore it. Two reads precede exactly one request: the board " +
		"stacks must show the card in the named stack and the attachment list of the card the attachment",
	Tags: []string{"nextcloud", "deck", "cards", "attachments", "delete"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"stack_id":` +
		deckIDSchema + `,"card_id":` + deckIDSchema + `,"attachment_id":` + deckIDSchema + `},` +
		`"required":["board_id","stack_id","card_id","attachment_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"},"attachment_id":` +
		`{"type":"string"}},"required":["deleted","attachment_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{deckBoardIDArgument, deckStackOfCardArgument, deckCardIDArgument,
		deckAttachmentIDArgument},
	Fields: []capability.Field{
		{Name: "deleted", Description: "True when Deck deleted the attachment"},
		{Name: "attachment_id", Description: "ID of the deleted attachment"},
	},
	Examples: []capability.Example{{Description: "Use IDs from deckattachments.list",
		Arguments: json.RawMessage(`{"board_id":"7","stack_id":"3","card_id":"21","attachment_id":"5"}`)}},
	RequiresToolAllowList: true,
}

// DeckAttachmentUpload reports an upload: metadata of the sent file, never content.
type DeckAttachmentUpload struct {
	Uploaded     bool   `json:"uploaded"`
	AttachmentID string `json:"attachment_id"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256,omitempty"`
}

// attachmentFileName checks the untrusted local file name; the multipart writer quotes it.
func attachmentFileName(op, name string) error {
	if name == "" || len(name) > maxAttachmentNameBytes || !utf8.ValidString(name) {
		return providerError(op, "the local file name is empty, too long, or not valid text")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return providerError(op, "the local file name holds control characters")
		}
	}
	return nil
}

// multipartFrame is the multipart body around a file of known size: field file, preceded by the Deck field
// type when withType is set. The file content streams between head and tail, so the memory use does not grow
// with the file.
func multipartFrame(name string, withType bool) (head, tail []byte, contentType string, err error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if withType {
		if err = w.WriteField("type", attachmentDeckFile); err != nil {
			return
		}
	}
	if _, err = w.CreateFormFile("file", name); err != nil {
		return
	}
	head = append([]byte(nil), buf.Bytes()...)
	if err = w.Close(); err != nil {
		return
	}
	return head, append([]byte(nil), buf.Bytes()[len(head):]...), w.FormDataContentType(), nil
}

// UploadDeckAttachment sends the open local file as one multipart request after the card was found in the
// stack of the bound board.
func (c *Client) UploadDeckAttachment(ctx context.Context, input deckAttachmentArguments, upload *localfile.Upload) (*DeckAttachmentUpload, error) {
	const op = "upload Deck attachment"
	stacks, err := c.cardStacks(ctx, op, input.BoardID, false)
	if err != nil {
		return nil, err
	}
	if _, err := cardIn(op, stacks, input.StackID, input.CardID); err != nil {
		return nil, err
	}
	head, tail, contentType, err := multipartFrame(upload.Name, true)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	body := io.MultiReader(bytes.NewReader(head), upload, bytes.NewReader(tail))
	length := int64(len(head)) + upload.Size + int64(len(tail))
	raw, err := c.deckDo(ctx, c.transferClient(), op, uncertainDeckAttachment, http.MethodPost, body, length, contentType,
		"boards", input.BoardID, "stacks", input.StackID, "cards", input.CardID, "attachments")
	if err != nil {
		return nil, err
	}
	var answer rawAttachment
	if err := json.Unmarshal(raw, &answer); err != nil || answer.ID == "" || checkDeckID(op, string(answer.ID)) != nil {
		return nil, withUncertainty(invalidResponse(op, "the Nextcloud attachment answer could not be read"), uncertainDeckAttachment)
	}
	sum, _ := upload.SHA256()
	return &DeckAttachmentUpload{Uploaded: true, AttachmentID: string(answer.ID), Name: upload.Name, Size: upload.Size,
		SHA256: sum}, nil
}

// DeleteDeckAttachment deletes one live deck_file attachment that the attachment list of the card shows.
func (c *Client) DeleteDeckAttachment(ctx context.Context, input deckAttachmentArguments) (any, error) {
	const op = "delete Deck attachment"
	list, err := c.cardAttachments(ctx, op, input.BoardID, input.StackID, input.CardID)
	if err != nil {
		return nil, err
	}
	item, err := findAttachment(op, list, input.AttachmentID)
	if err != nil {
		return nil, err
	}
	if item.Type != attachmentDeckFile {
		return nil, deckNotFound(op)
	}
	if _, err := c.deckSend(ctx, op, uncertainDeckAttachment, http.MethodDelete, nil, "boards", input.BoardID,
		"stacks", input.StackID, "cards", input.CardID, "attachments", attachmentDeckFile, input.AttachmentID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "attachment_id": input.AttachmentID}, nil
}

func invokeDeckAttachmentsUpload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "upload Deck attachment"
	input, err := readDeckAttachmentArguments(op, raw, false)
	if err != nil {
		return nil, err
	}
	if input.LocalPath == nil {
		return nil, providerError(op, "local_path is required")
	}
	// The binding and the local file are checked before the credential is resolved or any request is made.
	if err := requireBoard(resolved, input.BoardID); err != nil {
		return nil, err
	}
	upload, err := localfile.OpenForUpload(ctx, resolved, *input.LocalPath)
	if err != nil {
		return nil, err
	}
	defer upload.Close()
	if upload.Size > maxPathUploadBytes {
		return nil, providerError(op, "the local file exceeds the limit of 64 MiB for one Deck attachment")
	}
	if err := attachmentFileName(op, upload.Name); err != nil {
		return nil, err
	}
	client, err := openDeck(ctx, resolved, secrets, red, input.BoardID)
	if err != nil {
		return nil, err
	}
	return client.UploadDeckAttachment(ctx, input, upload)
}

func invokeDeckAttachmentsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := readDeckAttachmentArguments("delete Deck attachment", raw, true)
	if err != nil {
		return nil, err
	}
	client, err := openDeck(ctx, resolved, secrets, red, input.BoardID)
	if err != nil {
		return nil, err
	}
	return client.DeleteDeckAttachment(ctx, input)
}
