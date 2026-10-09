package nextcloud

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
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Notes are personal text; their sensitivity is its own class, like the one of shares.
const notesSensitivity = "nextcloud-notes"

// groupNotes is the tool group of the Notes tools.
const groupNotes = "notes"

// notesRoot are the fixed path segments of the Notes API (version 1.4) below the installation path.
var notesRoot = []string{"index.php", "apps", "notes", "api", "v1"}

// Bounds of the Notes reads.
const (
	maxNoteList         = 200
	defaultNoteList     = 50
	maxNoteContent      = 256 << 10
	maxNoteCursorLength = 256
	maxAttachmentPath   = 1024
	maxAttachmentSegs   = 32
	maxNoteIDLength     = 18
)

const (
	// messageNoteNotFound answers a note that does not exist and one outside the bound categories alike.
	messageNoteNotFound       = "this Nextcloud connection does not hold this note"
	messageAttachmentNotFound = "this Nextcloud connection does not hold this attachment"
	messageNoteTooLarge       = "the Nextcloud note attachment exceeds 4 MiB, use local_path"
)

const (
	noteIDSchema     = `{"type":"string","minLength":1,"maxLength":18,"pattern":"^[1-9][0-9]*$","x-form":"a note id of notes.list"}`
	noteCursorSchema = `{"type":"string","minLength":1,"maxLength":256,"pattern":"^[A-Za-z0-9_.,:=+/~-]+$","x-form":"the next_cursor of the previous notes.list"}`
	categorySchema   = `{"type":"string","minLength":1,"maxLength":1024}`
	noteSchema       = `{"type":"object","properties":{"id":{"type":"string"},"title":{"type":"string"},"category":{"type":"string"},` +
		`"favorite":{"type":"boolean"},"readonly":{"type":"boolean"},"modified_at":{"type":"string"},"etag":{"type":"string"},` +
		`"truncated":{"type":"boolean"}},"required":["id","title","category","favorite","readonly"],"additionalProperties":false}`
)

var noteIDArgument = capability.Argument{Name: "id", Required: true,
	Description: "Note to address, an id reported by notes.list"}

var notesList = capability.Descriptor{
	ID: Provider + ".notes.list", Version: 1, Title: "List Nextcloud notes",
	Description: "List the notes of the identity in the categories this connection binds, without their content, in " +
		"chunks of at most 200; a note outside the bound categories is never reported",
	Tags: []string{"nextcloud", "notes", "list"}, Risk: notesReadRisk(), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"category":` + categorySchema + `,"chunk_size":` +
		`{"type":"integer","minimum":1,"maximum":200},"chunk_cursor":` + noteCursorSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"notes":{"type":"array","items":` + noteSchema + `},` +
		`"count":{"type":"integer"},"truncated":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["notes","count","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "category", Description: "Only the notes of this category and its sub-categories; it must lie within the bound categories"},
		{Name: "chunk_size", Description: "Notes to ask Nextcloud for in one chunk, 1 to 200 (default 50); notes outside the bound categories are dropped from the chunk"},
		{Name: "chunk_cursor", Description: "The next_cursor of the previous chunk; omit it for the first chunk"},
	},
	Fields: []capability.Field{
		{Name: "notes", Description: "Notes of the chunk with id, title, category, favorite, readonly, modified_at, and etag; titles are untrusted data"},
		{Name: "count", Description: "Number of reported notes"},
		{Name: "truncated", Description: "True when Nextcloud answered with more notes than Qatlas reports"},
		{Name: "next_cursor", Description: "Cursor of the next chunk; absent after the last chunk"},
	},
	Examples: []capability.Example{{Description: "List the first chunk of notes", Arguments: json.RawMessage(`{"chunk_size":50}`)}},
}

var notesGet = capability.Descriptor{
	ID: Provider + ".notes.get", Version: 1, Title: "Read a Nextcloud note",
	Description: "Read one note in a category this connection binds, with its content up to 256 KiB; a note outside " +
		"the bound categories is answered like a missing one",
	Tags: []string{"nextcloud", "notes", "get", "content"}, Risk: notesReadRisk(), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + noteIDSchema + `},"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"title":{"type":"string"},` +
		`"category":{"type":"string"},"favorite":{"type":"boolean"},"readonly":{"type":"boolean"},"modified_at":{"type":"string"},` +
		`"etag":{"type":"string"},"truncated":{"type":"boolean"},"content":{"type":"string"},"content_size":{"type":"integer"},` +
		`"content_truncated":{"type":"boolean"}},"required":["id","title","category","favorite","readonly","content","content_size"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{noteIDArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Note that was read"},
		{Name: "title", Description: "Title, untrusted data"},
		{Name: "category", Description: "Category of the note, untrusted data"},
		{Name: "favorite", Description: "True for a favorite"},
		{Name: "readonly", Description: "True for a note the identity may not change"},
		{Name: "modified_at", Description: "Last change as RFC 3339 time"},
		{Name: "etag", Description: "Entity tag of the note"},
		{Name: "truncated", Description: "True when title or category was cut"},
		{Name: "content", Description: "Content of the note, untrusted data, cut at 256 KiB"},
		{Name: "content_size", Description: "Size of the full content in bytes"},
		{Name: "content_truncated", Description: "True when the content was cut"},
	},
	Examples: []capability.Example{{Description: "Read one note", Arguments: json.RawMessage(`{"id":"76"}`)}},
}

var notesAttachmentsGet = capability.Descriptor{
	ID: Provider + ".noteattachments.get", Version: 1, Title: "Read a Nextcloud note attachment",
	Description: "Read one image or file embedded in a note in a category this connection binds, inline as base64 up " +
		"to 4 MiB or written to local_path in a directory the connection releases for writing; the content of a " +
		"local download is never returned, only its metadata. An existing local file is replaced only with confirmation",
	Tags: []string{"nextcloud", "notes", "attachments", "get", "content", "download", "local"}, Risk: notesReadRisk(),
	Provider: Provider, LocalFiles: config.LocalFilesWrite,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + noteIDSchema + `,"path":` +
		`{"type":"string","minLength":1,"maxLength":1024,"x-form":"the path of the attachment as the note names it, relative to the note"},` +
		`"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},"required":["id","path"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"path":{"type":"string"},` +
		`"name":{"type":"string"},"content_base64":{"type":"string"},"content_type":{"type":"string"},"size":{"type":"integer"},` +
		`"sha256":{"type":"string"}},"required":["id","path","size"],"additionalProperties":false}`),
	Arguments: []capability.Argument{noteIDArgument,
		{Name: "path", Required: true, Description: "Attachment as the note names it, relative to the note such as .attachments.76/image.png; never absolute, no backslash, no empty, . or .. component"},
		localfile.DownloadPathArgument()},
	Fields: []capability.Field{
		{Name: "id", Description: "Note the attachment belongs to"},
		{Name: "path", Description: "Path of the attachment relative to the note"},
		{Name: "name", Description: "Last component of the path, untrusted data; only with local_path"},
		{Name: "content_base64", Description: "Attachment content as base64; only without local_path"},
		{Name: "content_type", Description: "MIME type Nextcloud reports, untrusted data"},
		{Name: "size", Description: "Size of the content in bytes"},
		{Name: "sha256", Description: "SHA-256 of the written content as hex; only with local_path"},
	},
	Examples: []capability.Example{{Description: "Write one embedded image to a released local directory",
		Arguments: json.RawMessage(`{"id":"76","path":".attachments.76/image.png","local_path":"~/downloads/image.png"}`)}},
}

var notesSettingsGet = capability.Descriptor{
	ID: Provider + ".notesettings.get", Version: 1, Title: "Get the Nextcloud Notes settings",
	Description: "Read the Notes app settings of the identity: the folder that holds the notes and the file suffix of new notes",
	Tags:        []string{"nextcloud", "notes", "settings", "get"}, Risk: notesReadRisk(), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"notes_path":{"type":"string"},"file_suffix":{"type":"string"},` +
		`"truncated":{"type":"boolean"}},"required":["notes_path","file_suffix"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "notes_path", Description: "Folder below the Files root that holds the notes, untrusted data"},
		{Name: "file_suffix", Description: "File suffix of newly created notes, untrusted data"},
		{Name: "truncated", Description: "True when a value was cut"},
	},
	Examples: []capability.Example{{Description: "Read the settings", Arguments: json.RawMessage(`{}`)}},
}

func notesReadRisk() capability.Risk {
	risk := nextcloudReadRisk
	risk.DataSensitivity = notesSensitivity
	return risk
}

// Note is the stable Qatlas view of one note without its content.
type Note struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Category   string `json:"category"`
	Favorite   bool   `json:"favorite"`
	Readonly   bool   `json:"readonly"`
	ModifiedAt string `json:"modified_at,omitempty"`
	ETag       string `json:"etag,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// NoteList is one chunk of the notes of the bound categories.
type NoteList struct {
	Notes      []Note `json:"notes"`
	Count      int    `json:"count"`
	Truncated  bool   `json:"truncated"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// NoteContent is one note with its content.
type NoteContent struct {
	Note
	Content          string `json:"content"`
	ContentSize      int    `json:"content_size"`
	ContentTruncated bool   `json:"content_truncated,omitempty"`
}

// NoteAttachment is the inline content of one attachment.
type NoteAttachment struct {
	ID            string `json:"id"`
	Path          string `json:"path"`
	ContentBase64 string `json:"content_base64"`
	ContentType   string `json:"content_type,omitempty"`
	Size          int    `json:"size"`
}

// NoteAttachmentDownload is what a local download of an attachment reports: metadata, never content.
type NoteAttachmentDownload struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Name        string `json:"name"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
}

// NotesSettings are the documented settings of the Notes app.
type NotesSettings struct {
	NotesPath  string `json:"notes_path"`
	FileSuffix string `json:"file_suffix"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// requireNotes refuses a connection without a notes target locally, before any credential access or
// request, and names no other target.
func requireNotes(resolved *config.Resolved) (selection, error) {
	s, err := scopeOf(resolved)
	if err != nil {
		return selection{}, err
	}
	if !s.notes.bound() {
		return selection{}, &provider.Error{
			Class: provider.ClassPermission, Op: "open",
			Message: "this connection is not bound to notes",
		}
	}
	return s.notes, nil
}

// notesBound is folderBound for the Notes tools.
func notesBound(handler capability.Handler) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		if _, err := requireNotes(resolved); err != nil {
			return nil, err
		}
		return handler(ctx, resolved, secrets, red, raw)
	}
}

// coversCategory reports whether a category lies in what the connection binds: any category for the whole kind,
// otherwise a listed category or one below it. The comparison is exact and case-sensitive, and a category
// whose remainder below a listed one is not a plain path stays outside.
func (s selection) coversCategory(category string) bool {
	if s.all {
		return true
	}
	for _, id := range s.ids {
		if id != "" && inCategory(category, id) {
			return true
		}
	}
	return false
}

// inCategory reports whether category is base or lies below it.
func inCategory(category, base string) bool {
	if category == base {
		return true
	}
	rest, ok := strings.CutPrefix(category, base+"/")
	if !ok {
		return false
	}
	for _, segment := range strings.Split(rest, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func noteNotFound(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op, Message: messageNoteNotFound}
}

func attachmentNotFound(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op, Message: messageAttachmentNotFound}
}

type noteArguments struct {
	ID          string  `json:"id"`
	Category    string  `json:"category"`
	ChunkSize   *int    `json:"chunk_size"`
	ChunkCursor string  `json:"chunk_cursor"`
	Path        string  `json:"path"`
	LocalPath   *string `json:"local_path"`
}

// readNoteArguments decodes strictly. Every value is checked here, before any credential access.
func readNoteArguments(op string, raw json.RawMessage) (noteArguments, error) {
	var input noteArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := decoder.Decode(&input); err != nil {
			return input, providerError(op, "the validated arguments could not be read")
		}
	}
	return input, nil
}

func validNoteID(id string) bool {
	return len(id) <= maxNoteIDLength && digitsOnly(id) && id[0] != '0'
}

func validCursor(cursor string) bool {
	if cursor == "" || len(cursor) > maxNoteCursorLength {
		return false
	}
	for i := 0; i < len(cursor); i++ {
		c := cursor[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("_.,:=+/~-", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// checkAttachmentPath accepts only a path relative to the note: segments such as .attachments.76 are fine,
// an absolute path, a backslash, an empty, . or .. component, and a control character are not.
func checkAttachmentPath(path string) error {
	if path == "" || len(path) > maxAttachmentPath {
		return errors.New("the attachment path is empty or too long")
	}
	if strings.HasPrefix(path, "/") {
		return errors.New("the attachment path must be relative to the note")
	}
	segments := strings.Split(path, "/")
	if len(segments) > maxAttachmentSegs {
		return errors.New("the attachment path has too many components")
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsRune(segment, '\\') {
			return errors.New("the attachment path is not a plain relative path")
		}
		for _, r := range segment {
			if r < 0x20 || r == 0x7f {
				return errors.New("the attachment path must not contain control characters")
			}
		}
	}
	return nil
}

func invokeNotesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list notes"
	input, err := readNoteArguments(op, raw)
	if err != nil {
		return nil, err
	}
	sel, err := requireNotes(resolved)
	if err != nil {
		return nil, err
	}
	size := defaultNoteList
	if input.ChunkSize != nil {
		size = *input.ChunkSize
	}
	if size < 1 || size > maxNoteList {
		return nil, providerError(op, "the chunk size must be 1 to 200")
	}
	if input.ChunkCursor != "" && !validCursor(input.ChunkCursor) {
		return nil, providerError(op, "the chunk cursor is unusable")
	}
	if input.Category != "" {
		if err := checkCategory(input.Category); err != nil || !sel.coversCategory(input.Category) {
			// The refusal does not say which categories the connection binds.
			return nil, &provider.Error{Class: provider.ClassPermission, Op: op,
				Message: "this connection does not hold this category"}
		}
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.ListNotes(ctx, sel, input.Category, size, input.ChunkCursor)
}

func invokeNotesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get note"
	input, err := readNoteArguments(op, raw)
	if err != nil {
		return nil, err
	}
	sel, err := requireNotes(resolved)
	if err != nil {
		return nil, err
	}
	if !validNoteID(input.ID) {
		return nil, providerError(op, "the note ID must be an id reported by "+notesList.ID)
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.GetNote(ctx, sel, input.ID)
}

func invokeNotesAttachmentsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get note attachment"
	input, err := readNoteArguments(op, raw)
	if err != nil {
		return nil, err
	}
	sel, err := requireNotes(resolved)
	if err != nil {
		return nil, err
	}
	if !validNoteID(input.ID) {
		return nil, providerError(op, "the note ID must be an id reported by "+notesList.ID)
	}
	if err := checkAttachmentPath(input.Path); err != nil {
		return nil, providerError(op, err.Error())
	}
	if input.LocalPath == nil {
		client, err := open(ctx, resolved, secrets, red, false)
		if err != nil {
			return nil, err
		}
		return client.GetNoteAttachment(ctx, sel, input.ID, input.Path)
	}
	// As in files.get, the local target is prepared before the credential is resolved.
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
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.DownloadNoteAttachment(ctx, sel, input.ID, input.Path, download, &done)
}

func invokeNotesSettingsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get notes settings"
	if _, err := readNoteArguments(op, raw); err != nil {
		return nil, err
	}
	if _, err := requireNotes(resolved); err != nil {
		return nil, err
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.GetNotesSettings(ctx)
}

// notesURL is the absolute URL of one Notes API endpoint: fixed segments plus a validated ID.
func (c *Client) notesURL(query url.Values, suffix ...string) string {
	segments := append(append(append([]string{}, c.install...), notesRoot...), suffix...)
	target := c.origin + escapePath(segments)
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	return target
}

func readLimited(body io.Reader, limit int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, int64(limit)+1))
	if err == nil && len(data) > limit {
		err = errors.New("too large")
	}
	return data, err
}

// notesGetJSON reads one JSON answer of the Notes API. The body of a failed answer is never read.
func (c *Client) notesGetJSON(ctx context.Context, op, target string) ([]byte, http.Header, error) {
	headers := http.Header{}
	headers.Set("Accept", "application/json")
	headers.Set("OCS-APIRequest", "true")
	response, err := c.webdavTo(ctx, op, http.MethodGet, target, nil, "", "", "", headers)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	body, err := readLimited(response.Body, maxBodyBytes)
	if err != nil {
		return nil, nil, invalidResponse(op, "the Nextcloud response could not be read within the size limit")
	}
	return body, response.Header, nil
}

type rawNote struct {
	ID       json.Number `json:"id"`
	ETag     string      `json:"etag"`
	Readonly bool        `json:"readonly"`
	Modified json.Number `json:"modified"`
	Title    string      `json:"title"`
	Category string      `json:"category"`
	Content  string      `json:"content"`
	Favorite bool        `json:"favorite"`
}

// rawListNote marks a pruned entry of the list: it has neither title nor category.
type rawListNote struct {
	rawNote
	Title    *string `json:"title"`
	Category *string `json:"category"`
}

func (n rawNote) view() Note {
	title, cutTitle := cutText(n.Title, maxValueLength)
	category, cutCategory := cutText(n.Category, maxValueLength)
	note := Note{ID: n.ID.String(), Title: title, Category: category, Favorite: n.Favorite, Readonly: n.Readonly,
		ETag: bounded(n.ETag), Truncated: cutTitle || cutCategory}
	if seconds, err := n.Modified.Int64(); err == nil && seconds > 0 {
		note.ModifiedAt = time.Unix(seconds, 0).UTC().Format(time.RFC3339)
	}
	return note
}

// cutText cuts a string at a character boundary and says whether it did.
func cutText(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit], true
}

// ListNotes reads one chunk of the notes without content. The server-side category filter matches exactly
// and misses sub-categories, so the whole list is asked for and every note outside the bound categories is
// dropped here, before anything is counted or returned.
func (c *Client) ListNotes(ctx context.Context, sel selection, filter string, size int, cursor string) (*NoteList, error) {
	const op = "list notes"
	query := url.Values{"exclude": {"content"}, "chunkSize": {strconv.Itoa(size)}}
	if cursor != "" {
		query.Set("chunkCursor", cursor)
	}
	body, header, err := c.notesGetJSON(ctx, op, c.notesURL(query, "notes"))
	if err != nil {
		return nil, err
	}
	var items []rawListNote
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, invalidResponse(op, "the Nextcloud note list could not be read")
	}
	result := &NoteList{Notes: []Note{}}
	for _, listed := range items {
		// The last chunk also carries the pruned notes, which are bare ids without title or category.
		if listed.Title == nil && listed.Category == nil {
			continue
		}
		item := listed.rawNote
		if listed.Title != nil {
			item.Title = *listed.Title
		}
		if listed.Category != nil {
			item.Category = *listed.Category
		}
		if !sel.coversCategory(item.Category) || filter != "" && !inCategory(item.Category, filter) {
			continue
		}
		if !validNoteID(item.ID.String()) {
			return nil, invalidResponse(op, "the Nextcloud note list could not be read")
		}
		if len(result.Notes) == maxNoteList {
			result.Truncated = true
			break
		}
		result.Notes = append(result.Notes, item.view())
	}
	result.Count = len(result.Notes)
	if next := header.Get("X-Notes-Chunk-Cursor"); next != "" && !result.Truncated {
		if !validCursor(next) {
			return nil, invalidResponse(op, "the Nextcloud chunk cursor is unusable")
		}
		result.NextCursor = next
	}
	return result, nil
}

// fetchNote reads one note and answers one outside the bound categories exactly like a missing one.
func (c *Client) fetchNote(ctx context.Context, op string, sel selection, id string, withContent bool) (*rawNote, error) {
	var query url.Values
	if !withContent {
		query = url.Values{"exclude": {"content"}}
	}
	body, _, err := c.notesGetJSON(ctx, op, c.notesURL(query, "notes", id))
	if err != nil {
		if isNotFound(err) {
			return nil, noteNotFound(op)
		}
		return nil, err
	}
	var note rawNote
	if err := json.Unmarshal(body, &note); err != nil || note.ID.String() != id {
		return nil, invalidResponse(op, "the Nextcloud note could not be read")
	}
	if !sel.coversCategory(note.Category) {
		return nil, noteNotFound(op)
	}
	return &note, nil
}

// GetNote reads one note in a bound category.
func (c *Client) GetNote(ctx context.Context, sel selection, id string) (*NoteContent, error) {
	note, err := c.fetchNote(ctx, "get note", sel, id, true)
	if err != nil {
		return nil, err
	}
	content, cut := cutText(note.Content, maxNoteContent)
	return &NoteContent{Note: note.view(), Content: content, ContentSize: len(note.Content), ContentTruncated: cut}, nil
}

// attachmentTarget builds the fixed attachment request; the path travels as a query value only.
func (c *Client) attachmentTarget(id, path string) string {
	return c.notesURL(url.Values{"path": {path}}, "attachment", id)
}

// GetNoteAttachment reads one attachment inline, up to the limit of files.get.
func (c *Client) GetNoteAttachment(ctx context.Context, sel selection, id, path string) (*NoteAttachment, error) {
	const op = "get note attachment"
	if _, err := c.fetchNote(ctx, op, sel, id, false); err != nil {
		return nil, err
	}
	body, header, err := c.readInline(ctx, op, c.attachmentTarget(id, path), messageNoteTooLarge)
	if err != nil {
		if isNotFound(err) {
			return nil, attachmentNotFound(op)
		}
		return nil, err
	}
	return &NoteAttachment{ID: id, Path: path, ContentBase64: base64.StdEncoding.EncodeToString(body),
		ContentType: bounded(header.Get("Content-Type")), Size: len(body)}, nil
}

// DownloadNoteAttachment streams one attachment into the prepared local file.
func (c *Client) DownloadNoteAttachment(ctx context.Context, sel selection, id, path string, download *localfile.Download, done *bool) (*NoteAttachmentDownload, error) {
	const op = "get note attachment"
	if _, err := c.fetchNote(ctx, op, sel, id, false); err != nil {
		return nil, err
	}
	header, err := c.downloadTo(ctx, op, c.attachmentTarget(id, path), download, done)
	if err != nil {
		if isNotFound(err) {
			return nil, attachmentNotFound(op)
		}
		return nil, err
	}
	sum, _ := download.SHA256()
	name, _ := cutText(path[strings.LastIndexByte(path, '/')+1:], maxValueLength)
	return &NoteAttachmentDownload{ID: id, Path: path, Name: name,
		ContentType: bounded(header.Get("Content-Type")), Size: download.Size(), SHA256: sum}, nil
}

// GetNotesSettings reads the documented settings and nothing else.
func (c *Client) GetNotesSettings(ctx context.Context) (*NotesSettings, error) {
	const op = "get notes settings"
	body, _, err := c.notesGetJSON(ctx, op, c.notesURL(nil, "settings"))
	if err != nil {
		return nil, err
	}
	var raw struct {
		NotesPath  string `json:"notesPath"`
		FileSuffix string `json:"fileSuffix"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, invalidResponse(op, "the Nextcloud notes settings could not be read")
	}
	path, cutPath := cutText(raw.NotesPath, maxValueLength)
	suffix, cutSuffix := cutText(raw.FileSuffix, maxValueLength)
	return &NotesSettings{NotesPath: path, FileSuffix: suffix, Truncated: cutPath || cutSuffix}, nil
}
