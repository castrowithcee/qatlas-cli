package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Note writes in a bound category through the Notes API: a POST creates, a PUT with If-Match changes, and a
// DELETE removes one note. Each sends exactly one mutating request after at most one read-only pre-check.

var noteKind = objectKind{"a", "note", "", "; the change may have been applied, check the note with notes.get " +
	"or notes.list before repeating it"}

const (
	// maxNoteTitle bounds a title in characters; Nextcloud derives the file name from it.
	maxNoteTitle = 255
	// maxNoteWriteContent is the content a write carries, the inline limit of the provider. It lies below the
	// request limit of CLI and MCP (8 MiB), which also holds the JSON envelope and escaping.
	maxNoteWriteContent = maxFileBytes

	noteTitleArgSchema    = `"title":{"type":"string","minLength":1,"maxLength":255}`
	noteContentArgSchema  = `"content":{"type":"string","maxLength":4194304}`
	noteCategoryArgSchema = `"category":` + categorySchema
	noteFavoriteArgSchema = `"favorite":{"type":"boolean"}`
	noteETagArgSchema     = `"etag":{"type":"string","minLength":1,"maxLength":1024}`
	noteWriteNoteSchema   = `"id":{"type":"string"},"title":{"type":"string"},"category":{"type":"string"},` +
		`"favorite":{"type":"boolean"},"readonly":{"type":"boolean"},"modified_at":{"type":"string"},"etag":{"type":"string"},` +
		`"truncated":{"type":"boolean"}`
)

// notesWriteRisk is open-world like every provider call; notes are personal text.
func notesWriteRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: notesSensitivity}
}

var (
	noteTitleArgument   = capability.Argument{Name: "title", Description: "Title, 1 to 255 characters without control characters"}
	noteContentArgument = capability.Argument{Name: "content",
		Description: "Content as text of at most 4 MiB; an empty string clears the content of a note"}
	noteCategoryArgument = capability.Argument{Name: "category",
		Description: "Category; it must lie within the bound categories"}
	noteFavoriteArgument = capability.Argument{Name: "favorite", Description: "True to mark the note as a favorite"}
	noteETagArgument     = capability.Argument{Name: "etag", Required: true,
		Description: "Entity tag of the note version to change, as notes.get or notes.list reports it"}
)

var noteWriteFields = []capability.Field{
	{Name: "id", Description: "Note id"},
	{Name: "title", Description: "Title Nextcloud stored, untrusted data"},
	{Name: "category", Description: "Category Nextcloud stored, untrusted data"},
	{Name: "favorite", Description: "True for a favorite"},
	{Name: "readonly", Description: "True for a note the identity may not change"},
	{Name: "modified_at", Description: "Last change as RFC 3339 time"},
	{Name: "etag", Description: "Entity tag of the stored version"},
	{Name: "truncated", Description: "True when title or category was cut"},
}

var notesCreate = capability.Descriptor{
	ID: Provider + ".notes.create", Version: 1, Title: "Create a Nextcloud note",
	Description: "Create one new note in a category this connection binds; without category it lands in the root " +
		"category, which a connection bound to single categories does not hold. One POST stores it, and the " +
		"request is never repeated after an unclear result",
	Tags: []string{"nextcloud", "notes", "note", "create"}, Provider: Provider,
	Risk: notesWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + noteTitleArgSchema + `,` + noteContentArgSchema + `,` +
		noteCategoryArgSchema + `,` + noteFavoriteArgSchema + `},"required":["title"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + noteWriteNoteSchema + `,"created":{"type":"boolean"}},` +
		`"required":["id","title","category","favorite","readonly","created"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "title", Required: true, Description: noteTitleArgument.Description},
		noteContentArgument, noteCategoryArgument, noteFavoriteArgument},
	Fields: append(append([]capability.Field{}, noteWriteFields...),
		capability.Field{Name: "created", Description: "True when Nextcloud stored the note"}),
	Examples: []capability.Example{{Description: "Create a note",
		Arguments: json.RawMessage(`{"title":"Ideas","content":"First idea","category":"Work"}`)}},
}

var notesUpdate = capability.Descriptor{
	ID: Provider + ".notes.update", Version: 1, Title: "Change a Nextcloud note",
	Description: "Change the title, content, category, or favorite mark of one note, bound to its etag; only the " +
		"given fields change. The note and a new category must both lie within the categories this connection " +
		"binds. One read-only GET of the note refuses a foreign, read-only, or changed note, then one PUT with " +
		"If-Match follows and is never repeated after an unclear result",
	Tags: []string{"nextcloud", "notes", "note", "update"}, Provider: Provider,
	Risk: notesWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + noteIDSchema + `,` + noteETagArgSchema + `,` +
		noteTitleArgSchema + `,` + noteContentArgSchema + `,` + noteCategoryArgSchema + `,` + noteFavoriteArgSchema +
		`},"required":["id","etag"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + noteWriteNoteSchema + `,"updated":{"type":"boolean"}},` +
		`"required":["id","title","category","favorite","readonly","updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{noteIDArgument, noteETagArgument, noteTitleArgument, noteContentArgument,
		noteCategoryArgument, noteFavoriteArgument},
	Fields: append(append([]capability.Field{}, noteWriteFields...),
		capability.Field{Name: "updated", Description: "True when Nextcloud stored the new version"}),
	Examples: []capability.Example{{Description: "Move a note to another category",
		Arguments: json.RawMessage(`{"id":"76","etag":"e1","category":"Work/Done"}`)}},
}

var notesDelete = capability.Descriptor{
	ID: Provider + ".notes.delete", Version: 1, Title: "Delete a Nextcloud note",
	Description: "Delete one note in a category this connection binds. One read-only GET refuses a foreign or " +
		"read-only note, then one DELETE follows and is never repeated after an unclear result. Nextcloud keeps " +
		"no restore for it through Qatlas",
	Tags: []string{"nextcloud", "notes", "note", "delete"}, Provider: Provider,
	Risk: notesWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + noteIDSchema + `},"required":["id"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"deleted":{"type":"boolean"}},` +
		`"required":["id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{noteIDArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Note id"},
		{Name: "deleted", Description: "True when Nextcloud deleted the note"},
	},
	Examples: []capability.Example{{Description: "Delete a note", Arguments: json.RawMessage(`{"id":"76"}`)}},
	// Reachable only through a tools list, never through a profile.
	RequiresToolAllowList: true,
}

// NoteWriteResult is the result of the note create and update tools.
type NoteWriteResult struct {
	Note
	Created bool `json:"created,omitempty"`
	Updated bool `json:"updated,omitempty"`
}

// NoteDeleteResult is the result of the note delete tool.
type NoteDeleteResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

type noteWriteArguments struct {
	ID       string  `json:"id"`
	ETag     string  `json:"etag"`
	Title    *string `json:"title"`
	Content  *string `json:"content"`
	Category *string `json:"category"`
	Favorite *bool   `json:"favorite"`
}

// noteChange is the fixed body of a write; only given fields are sent.
type noteChange struct {
	Title    *string `json:"title,omitempty"`
	Content  *string `json:"content,omitempty"`
	Category *string `json:"category,omitempty"`
	Favorite *bool   `json:"favorite,omitempty"`
}

func noteReadOnly(op string) error {
	return &provider.Error{Class: provider.ClassPermission, Op: op,
		Message: "this note is read-only for this Nextcloud identity, nothing was changed"}
}

func categoryRefused(op string) error {
	// The refusal does not say which categories the connection binds.
	return &provider.Error{Class: provider.ClassPermission, Op: op, Message: "this connection does not hold this category"}
}

// checkNoteFields validates every given field before any secret or request, and refuses a category outside
// the binding, a new one of a category change included.
func checkNoteFields(op string, sel selection, input noteWriteArguments) error {
	if input.Title != nil && (strings.TrimSpace(*input.Title) == "" || utf8.RuneCountInString(*input.Title) > maxNoteTitle ||
		hasControl(*input.Title) || !utf8.ValidString(*input.Title)) {
		return providerError(op, "the title must be 1 to 255 characters without control characters")
	}
	if input.Content != nil && (len(*input.Content) > maxNoteWriteContent || !utf8.ValidString(*input.Content)) {
		return providerError(op, "the content must be valid text of at most 4 MiB")
	}
	if input.Category != nil {
		if err := checkCategory(*input.Category); err != nil || !sel.coversCategory(*input.Category) {
			return categoryRefused(op)
		}
	}
	return nil
}

func invokeNotesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create note"
	var input noteWriteArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	sel, err := requireNotes(resolved)
	if err != nil {
		return nil, err
	}
	if input.ID != "" || input.ETag != "" || input.Title == nil {
		return nil, providerError(op, "a new note needs a title and takes no id or etag")
	}
	if err := checkNoteFields(op, sel, input); err != nil {
		return nil, err
	}
	if input.Category == nil && !sel.coversCategory("") {
		return nil, categoryRefused(op)
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	stored, err := client.noteSend(ctx, op, http.MethodPost, "",
		noteChange{Title: input.Title, Content: input.Content, Category: input.Category, Favorite: input.Favorite})
	if err != nil {
		return nil, err
	}
	return &NoteWriteResult{Note: stored.view(), Created: true}, nil
}

func invokeNotesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update note"
	var input noteWriteArguments
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
	if !validETag(input.ETag) {
		return nil, providerError(op, "etag must be the entity tag of the note as the read tools report it")
	}
	if input.Title == nil && input.Content == nil && input.Category == nil && input.Favorite == nil {
		return nil, providerError(op, "give at least one of title, content, category, and favorite")
	}
	if err := checkNoteFields(op, sel, input); err != nil {
		return nil, err
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	current, err := client.fetchNote(ctx, op, sel, input.ID, false)
	if err != nil {
		return nil, err
	}
	if current.Readonly {
		return nil, noteReadOnly(op)
	}
	etag := strings.Trim(input.ETag, `"`)
	if strings.Trim(current.ETag, `"`) != etag {
		return nil, objectPrecondition(op, noteKind)
	}
	stored, err := client.noteSend(ctx, op, http.MethodPut, input.ID, noteChange{Title: input.Title,
		Content: input.Content, Category: input.Category, Favorite: input.Favorite}, etag)
	if err != nil {
		return nil, err
	}
	return &NoteWriteResult{Note: stored.view(), Updated: true}, nil
}

func invokeNotesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete note"
	var input noteWriteArguments
	if err := decodeEventWrite(op, raw, &input); err != nil {
		return nil, err
	}
	sel, err := requireNotes(resolved)
	if err != nil {
		return nil, err
	}
	if !validNoteID(input.ID) || input.ETag != "" || input.Title != nil || input.Content != nil ||
		input.Category != nil || input.Favorite != nil {
		return nil, providerError(op, "the note ID must be an id reported by "+notesList.ID)
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	current, err := client.fetchNote(ctx, op, sel, input.ID, false)
	if err != nil {
		return nil, err
	}
	if current.Readonly {
		return nil, noteReadOnly(op)
	}
	if _, err := client.noteSend(ctx, op, http.MethodDelete, input.ID, nil); err != nil {
		return nil, err
	}
	return &NoteDeleteResult{ID: input.ID, Deleted: true}, nil
}

// noteSend is the single write path of notes. It sends exactly one request to a fixed Notes endpoint and
// never repeats it; match, when given, travels as If-Match. A refusal of a client error, a full quota, or a
// redirect is clear; any other failure, including an unreadable answer, leaves the outcome open and carries
// the hint. The body of a failed answer is never read. A delete (nil change) has no answer to read.
func (c *Client) noteSend(ctx context.Context, op, method, id string, change any, match ...string) (*rawNote, error) {
	suffix := []string{"notes"}
	if id != "" {
		suffix = append(suffix, id)
	}
	var body io.Reader
	if change != nil {
		encoded, err := json.Marshal(change)
		if err != nil {
			return nil, providerError(op, "the request could not be built")
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.notesURL(nil, suffix...), body)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if len(match) > 0 {
		req.Header.Set("If-Match", `"`+match[0]+`"`)
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, sentTransportError(op, err, noteKind.uncertain)
	}
	defer response.Body.Close()
	switch status := response.StatusCode; {
	case status == http.StatusPreconditionFailed && len(match) > 0:
		return nil, objectPrecondition(op, noteKind)
	case status == http.StatusNotFound && id != "":
		return nil, noteNotFound(op)
	case status == http.StatusInsufficientStorage:
		return nil, statusError(op, status)
	case status < 200 || status >= 300:
		return nil, sentStatusError(op, status, noteKind.uncertain)
	case method == http.MethodDelete:
		return nil, nil
	}
	data, err := readLimited(response.Body, maxBodyBytes)
	var stored rawNote
	if err != nil || json.Unmarshal(data, &stored) != nil || !validNoteID(stored.ID.String()) ||
		id != "" && stored.ID.String() != id {
		return nil, withUncertainty(invalidResponse(op, "the Nextcloud note answer could not be read"), noteKind.uncertain)
	}
	return &stored, nil
}
