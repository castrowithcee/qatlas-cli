package nextcloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Attachments of a card of a bound board. The board's own stack list, read first, must hold the card in the
// named stack, because Deck resolves a card by its ID alone. Only an attachment of type deck_file, read from
// the attachment list of that card, is downloaded; a reference into the Files tree is listed and refused.

const (
	maxDeckAttachments = 100
	attachmentDeckFile = "deck_file"
)

const messageAttachmentType = "only an attachment of type deck_file can be downloaded; a file reference is read with the Files tools"

var deckAttachmentIDArgument = capability.Argument{
	Name: "attachment_id", Description: "Numeric attachment ID as reported by deckattachments.list; only type deck_file",
	Required: true,
}

var deckAttachmentFields = []capability.Field{
	{Name: "id", Description: "Attachment ID"},
	{Name: "type", Description: "deck_file for a file stored with Deck, file for a reference into the Files tree"},
	{Name: "name", Description: "File name, untrusted data"},
	{Name: "size", Description: "Size in bytes as Deck reports it, when known"},
	{Name: "content_type", Description: "MIME type as Deck reports it, untrusted data"},
	{Name: "created_by", Description: "User ID of the uploader, untrusted data"},
	{Name: "created_at", Description: "Creation time as Unix seconds"},
	{Name: "last_modified", Description: "Last change as Unix seconds"},
	{Name: "downloadable", Description: "True for type deck_file, which deckattachments.get can read"},
	{Name: "truncated", Description: "True when a string was cut or the list was limited"},
}

const deckAttachmentSchema = `{"type":"object","properties":{"id":{"type":"string"},"type":{"type":"string"},` +
	`"name":{"type":"string"},"size":{"type":"integer"},"content_type":{"type":"string"},` +
	`"created_by":{"type":"string"},"created_at":{"type":"integer"},"last_modified":{"type":"integer"},` +
	`"downloadable":{"type":"boolean"}},"required":["id","type","name","downloadable"],"additionalProperties":false}`

var deckAttachmentsList = capability.Descriptor{
	ID: Provider + ".deckattachments.list", Version: 1, Title: "List Nextcloud Deck card attachments",
	Description: "List the attachments of exactly one active card of a bound Deck board: name, size, type, and " +
		"uploader, never content. Two reads: the stacks of the board must show the card in the named stack, then " +
		"the attachment list of the card. Deleted attachments are left out; at most 100 are listed",
	Tags: []string{"nextcloud", "deck", "cards", "attachments", "list"}, Provider: Provider, Risk: deckRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"stack_id":` +
		deckIDSchema + `,"card_id":` + deckIDSchema + `},"required":["board_id","stack_id","card_id"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"attachments":{"type":"array","items":` +
		deckAttachmentSchema + `},"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["attachments","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{deckBoardIDArgument, deckStackOfCardArgument, deckCardIDArgument},
	Fields: append([]capability.Field{{Name: "attachments", Description: "Attachments of the card"},
		{Name: "count", Description: "Number of attachments listed"}}, deckAttachmentFields...),
	Examples: []capability.Example{{Description: "List the attachments of one card",
		Arguments: json.RawMessage(`{"board_id":"7","stack_id":"3","card_id":"21"}`)}},
}

var deckAttachmentsGet = capability.Descriptor{
	ID: Provider + ".deckattachments.get", Version: 1, Title: "Read or download a Nextcloud Deck card attachment",
	Description: "Read one attachment of type deck_file of an active card of a bound Deck board, inline as base64 up " +
		"to 4 MiB or written to local_path in a directory the connection releases for writing; the content of a " +
		"local download is never returned, only its metadata. Two reads precede the one download: the stacks of " +
		"the board must show the card in the named stack and the attachment list of the card the attachment. An " +
		"existing local file is replaced only with confirmation",
	Tags:     []string{"nextcloud", "deck", "cards", "attachments", "get", "download", "local"},
	Provider: Provider, Risk: deckRisk, LocalFiles: config.LocalFilesWrite,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"stack_id":` +
		deckIDSchema + `,"card_id":` + deckIDSchema + `,"attachment_id":` + deckIDSchema + `,"` +
		localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},` +
		`"required":["board_id","stack_id","card_id","attachment_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"content_base64":{"type":"string"},"content_type":{"type":"string"},"size":{"type":"integer"},` +
		`"sha256":{"type":"string"},"truncated":{"type":"boolean"}},"required":["id","size"],"additionalProperties":false}`),
	Arguments: []capability.Argument{deckBoardIDArgument, deckStackOfCardArgument, deckCardIDArgument,
		deckAttachmentIDArgument, localfile.DownloadPathArgument()},
	Fields: []capability.Field{
		{Name: "id", Description: "Attachment ID"},
		{Name: "name", Description: "File name, untrusted data"},
		{Name: "content_base64", Description: "File content as base64; only without local_path"},
		{Name: "content_type", Description: "MIME type Nextcloud reports, untrusted data"},
		{Name: "size", Description: "Size of the content in bytes"},
		{Name: "sha256", Description: "SHA-256 of the written content as hex; only with local_path"},
		{Name: "truncated", Description: "True when the name was cut"},
	},
	Examples: []capability.Example{{Description: "Write one attachment to a released local directory",
		Arguments: json.RawMessage(`{"board_id":"7","stack_id":"3","card_id":"21","attachment_id":"5","local_path":"~/downloads/plan.pdf"}`)}},
}

// DeckAttachment is the stable view of one attachment; name, type, and uploader are untrusted data.
type DeckAttachment struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Name         string `json:"name"`
	Size         int64  `json:"size,omitempty"`
	ContentType  string `json:"content_type,omitempty"`
	CreatedBy    string `json:"created_by,omitempty"`
	CreatedAt    int64  `json:"created_at,omitempty"`
	LastModified int64  `json:"last_modified,omitempty"`
	Downloadable bool   `json:"downloadable"`
}

// DeckAttachmentsResult is the list of the attachments of one card.
type DeckAttachmentsResult struct {
	Attachments []DeckAttachment `json:"attachments"`
	Count       int              `json:"count"`
	Truncated   bool             `json:"truncated,omitempty"`
}

// DeckAttachmentContent is an inline read.
type DeckAttachmentContent struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContentBase64 string `json:"content_base64"`
	ContentType   string `json:"content_type,omitempty"`
	Size          int    `json:"size"`
	Truncated     bool   `json:"truncated,omitempty"`
}

// DeckAttachmentDownload reports a local download: metadata of the written file, never content.
type DeckAttachmentDownload struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	Truncated   bool   `json:"truncated,omitempty"`
}

type rawAttachment struct {
	ID           flexString      `json:"id"`
	CardID       flexString      `json:"cardId"`
	Type         string          `json:"type"`
	Data         json.RawMessage `json:"data"`
	Extended     json.RawMessage `json:"extendedData"`
	CreatedBy    string          `json:"createdBy"`
	CreatedAt    int64           `json:"createdAt"`
	LastModified int64           `json:"lastModified"`
	DeletedAt    int64           `json:"deletedAt"`
}

// attachmentExtended is the part of extendedData that is read; Deck sends other shapes for other types.
type attachmentExtended struct {
	FileSize flexString `json:"filesize"`
	MimeType string     `json:"mimetype"`
}

// view converts the raw attachment, cutting every untrusted string.
func (a rawAttachment) view(c *cutter) DeckAttachment {
	var name string
	_ = json.Unmarshal(a.Data, &name)
	var extended attachmentExtended
	_ = json.Unmarshal(a.Extended, &extended)
	size, _ := strconv.ParseInt(string(extended.FileSize), 10, 64)
	return DeckAttachment{ID: c.text(string(a.ID), maxDeckTitleBytes), Type: c.text(a.Type, maxDeckTitleBytes),
		Name: c.text(name, maxDeckTitleBytes), Size: max(size, 0), ContentType: c.text(extended.MimeType, maxDeckTitleBytes),
		CreatedBy: c.text(a.CreatedBy, maxDeckTitleBytes), CreatedAt: a.CreatedAt, LastModified: a.LastModified,
		Downloadable: a.Type == attachmentDeckFile}
}

// deckURL is the absolute URL of one Deck REST path: fixed words and validated IDs only.
func (c *Client) deckURL(suffix ...string) string {
	segments := append(append(append([]string{}, c.install...), deckRoot...), suffix...)
	return c.origin + escapePath(segments)
}

// deckAttachmentHeaders marks the download as an API request like every other Deck request.
var deckAttachmentHeaders = http.Header{"Ocs-Apirequest": {"true"}}

// cardAttachments checks the card against the stack list of the bound board, then reads its attachment list
// once. Deleted attachments and attachments of another card are dropped; the rest is in Deck's order.
func (c *Client) cardAttachments(ctx context.Context, op, boardID, stackID, cardID string) ([]rawAttachment, error) {
	stacks, err := c.cardStacks(ctx, op, boardID, false)
	if err != nil {
		return nil, err
	}
	if _, err := cardIn(op, stacks, stackID, cardID); err != nil {
		return nil, err
	}
	body, err := c.deckGet(ctx, op, "boards", boardID, "stacks", stackID, "cards", cardID, "attachments")
	if err != nil {
		return nil, err
	}
	var all []rawAttachment
	if err := json.Unmarshal(body, &all); err != nil {
		return nil, invalidResponse(op, "the Nextcloud attachment list could not be read")
	}
	live := all[:0:0]
	for _, item := range all {
		if item.DeletedAt == 0 && (item.CardID == "" || string(item.CardID) == cardID) {
			live = append(live, item)
		}
	}
	return live, nil
}

// findAttachment returns the live attachment of the card with this ID, or a refusal that names nothing.
func findAttachment(op string, list []rawAttachment, id string) (rawAttachment, error) {
	for _, item := range list {
		if string(item.ID) == id {
			return item, nil
		}
	}
	return rawAttachment{}, deckNotFound(op)
}

// ListDeckAttachments lists the attachments of one active card of the board.
func (c *Client) ListDeckAttachments(ctx context.Context, boardID, stackID, cardID string) (*DeckAttachmentsResult, error) {
	const op = "list Deck attachments"
	list, err := c.cardAttachments(ctx, op, boardID, stackID, cardID)
	if err != nil {
		return nil, err
	}
	result := &DeckAttachmentsResult{Attachments: []DeckAttachment{}}
	for _, item := range list {
		if len(result.Attachments) == maxDeckAttachments {
			result.Truncated = true
			break
		}
		var cut cutter
		result.Attachments = append(result.Attachments, item.view(&cut))
		result.Truncated = result.Truncated || cut.hit
	}
	result.Count = len(result.Attachments)
	return result, nil
}

// downloadableAttachment resolves the attachment of the card and refuses every type but deck_file before any
// download request.
func (c *Client) downloadableAttachment(ctx context.Context, op string, input deckAttachmentArguments) (rawAttachment, error) {
	list, err := c.cardAttachments(ctx, op, input.BoardID, input.StackID, input.CardID)
	if err != nil {
		return rawAttachment{}, err
	}
	item, err := findAttachment(op, list, input.AttachmentID)
	if err != nil {
		return item, err
	}
	if item.Type != attachmentDeckFile {
		return item, providerError(op, messageAttachmentType)
	}
	return item, nil
}

// attachmentURL is the v1.1 download route, which carries the attachment type as a path segment.
func (c *Client) attachmentURL(input deckAttachmentArguments) string {
	return c.deckURL("boards", input.BoardID, "stacks", input.StackID, "cards", input.CardID, "attachments",
		attachmentDeckFile, input.AttachmentID)
}

// GetDeckAttachment reads one attachment inline, up to the limit of files.get.
func (c *Client) GetDeckAttachment(ctx context.Context, input deckAttachmentArguments) (*DeckAttachmentContent, error) {
	const op = "get Deck attachment"
	item, err := c.downloadableAttachment(ctx, op, input)
	if err != nil {
		return nil, err
	}
	var cut cutter
	view := item.view(&cut)
	const tooLarge = "the Nextcloud attachment exceeds 4 MiB, use local_path"
	if view.Size > maxFileBytes {
		return nil, invalidResponse(op, tooLarge)
	}
	body, header, err := c.readInlineWith(ctx, op, c.attachmentURL(input), tooLarge, deckAttachmentHeaders)
	if err != nil {
		return nil, notFoundAsAttachment(op, err)
	}
	return &DeckAttachmentContent{ID: input.AttachmentID, Name: view.Name, ContentBase64: base64.StdEncoding.EncodeToString(body),
		ContentType: bounded(header.Get("Content-Type")), Size: len(body), Truncated: cut.hit}, nil
}

// DownloadDeckAttachment streams one attachment into the prepared local file.
func (c *Client) DownloadDeckAttachment(ctx context.Context, input deckAttachmentArguments, download *localfile.Download, done *bool) (*DeckAttachmentDownload, error) {
	const op = "get Deck attachment"
	item, err := c.downloadableAttachment(ctx, op, input)
	if err != nil {
		return nil, err
	}
	var cut cutter
	view := item.view(&cut)
	header, err := c.downloadToWith(ctx, op, c.attachmentURL(input), download, done, deckAttachmentHeaders)
	if err != nil {
		return nil, notFoundAsAttachment(op, err)
	}
	sum, _ := download.SHA256()
	return &DeckAttachmentDownload{ID: input.AttachmentID, Name: view.Name,
		ContentType: bounded(header.Get("Content-Type")), Size: download.Size(), SHA256: sum, Truncated: cut.hit}, nil
}

// notFoundAsAttachment turns the 404 of the download into the same refusal as an attachment the list lacks.
func notFoundAsAttachment(op string, err error) error {
	if isNotFound(err) {
		return deckNotFound(op)
	}
	return err
}

type deckAttachmentArguments struct {
	BoardID      string  `json:"board_id"`
	StackID      string  `json:"stack_id"`
	CardID       string  `json:"card_id"`
	AttachmentID string  `json:"attachment_id"`
	LocalPath    *string `json:"local_path"`
}

// readDeckAttachmentArguments decodes and validates before any credential access; withAttachment selects get.
func readDeckAttachmentArguments(op string, raw json.RawMessage, withAttachment bool) (deckAttachmentArguments, error) {
	var input deckAttachmentArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	ids := []string{input.StackID, input.CardID}
	if withAttachment {
		ids = append(ids, input.AttachmentID)
	}
	for _, id := range ids {
		if err := checkDeckID(op, id); err != nil {
			return input, err
		}
	}
	return input, nil
}

func invokeDeckAttachmentsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := readDeckAttachmentArguments("list Deck attachments", raw, false)
	if err != nil {
		return nil, err
	}
	client, err := openDeck(ctx, resolved, secrets, red, input.BoardID)
	if err != nil {
		return nil, err
	}
	return client.ListDeckAttachments(ctx, input.BoardID, input.StackID, input.CardID)
}

func invokeDeckAttachmentsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := readDeckAttachmentArguments("get Deck attachment", raw, true)
	if err != nil {
		return nil, err
	}
	if input.LocalPath == nil {
		client, err := openDeck(ctx, resolved, secrets, red, input.BoardID)
		if err != nil {
			return nil, err
		}
		return client.GetDeckAttachment(ctx, input)
	}
	// As in files.get, the local target is prepared before the credential is resolved; the binding is checked
	// first so an unbound board leaves no local file behind.
	if err := requireBoard(resolved, input.BoardID); err != nil {
		return nil, err
	}
	download, err := localfile.CreateForDownload(ctx, resolved, *input.LocalPath)
	if err != nil {
		return nil, err
	}
	done := false
	defer func() {
		if !done {
			_ = download.Abort()
		}
	}()
	client, err := openDeck(ctx, resolved, secrets, red, input.BoardID)
	if err != nil {
		return nil, err
	}
	return client.DownloadDeckAttachment(ctx, input, download, &done)
}
