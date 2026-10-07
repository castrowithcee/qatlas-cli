package bookstack

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Limits of a book or chapter write; BookStack counts characters.
const (
	maxDescriptionTextChars = 1900
	maxDescriptionHTMLChars = 2000
)

const outsideTemplate = "default_template_id is outside the books this connection is bound to"

func bookWriteRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity}
}

var (
	// containerProperties are the fields books and chapters share.
	containerProperties = `"description":{"type":"string","minLength":1,"maxLength":1900},"description_html":{"type":"string","minLength":1,"maxLength":2000},"tags":` + tagsSchema

	booksCreate = capability.Descriptor{
		ID: Provider + ".books.create", Version: 1, Title: "Create a BookStack book",
		Description: "Create one book with an optional description, tags, and default page template. Not available " +
			"for a connection bound to books, because a new book would lie outside them",
		Tags: []string{"knowledge", "books", "bookstack", "create"}, Provider: Provider,
		Risk:         bookWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":255},` + containerProperties + `,"default_template_id":{"type":"integer","minimum":1}},"required":["name"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(bookWriteOutput),
		Arguments: []capability.Argument{
			{Name: "name", Description: "Book title, 1 to 255 characters", Required: true},
			{Name: "description", Description: "Plain description, at most 1900 characters; mutually exclusive with description_html"},
			{Name: "description_html", Description: "Description as HTML, at most 2000 characters; mutually exclusive with description"},
			{Name: "tags", Description: "At most 50 tags as name and value pairs, each at most 255 characters"},
			{Name: "default_template_id", Description: "Identifier of a template page used for new pages of the book"},
		},
		Fields: bookWriteFields,
	}

	booksUpdate = capability.Descriptor{
		ID: Provider + ".books.update", Version: 1, Title: "Update a BookStack book",
		Description: "Change the name, description, tags, or default page template of one book by identifier. " +
			"tags replaces all existing tags of the book; default_template_id null removes the template",
		Tags: []string{"knowledge", "books", "bookstack", "update"}, Provider: Provider,
		Risk:         bookWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1},"name":{"type":"string","minLength":1,"maxLength":255},` + containerProperties + `,"default_template_id":{"type":["integer","null"],"minimum":1}},"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(bookWriteOutput),
		Arguments: []capability.Argument{
			{Name: "id", Description: "Book identifier", Required: true},
			{Name: "name", Description: "New book title, 1 to 255 characters"},
			{Name: "description", Description: "New plain description, at most 1900 characters; mutually exclusive with description_html"},
			{Name: "description_html", Description: "New description as HTML, at most 2000 characters; mutually exclusive with description"},
			{Name: "tags", Description: "Replaces all tags of the book: at most 50 name and value pairs, each at most 255 characters; an empty list removes all tags"},
			{Name: "default_template_id", Description: "Identifier of a template page; null removes the default template"},
		},
		Fields: bookWriteFields,
	}

	chaptersCreate = capability.Descriptor{
		ID: Provider + ".chapters.create", Version: 1, Title: "Create a BookStack chapter",
		Description: "Create one chapter in a specified book with an optional description, tags, priority, and " +
			"default page template",
		Tags: []string{"knowledge", "chapters", "bookstack", "create"}, Provider: Provider,
		Risk:         bookWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"book_id":{"type":"integer","minimum":1},"name":{"type":"string","minLength":1,"maxLength":255},` + containerProperties + `,"priority":{"type":"integer","minimum":0},"default_template_id":{"type":"integer","minimum":1}},"required":["book_id","name"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(chapterWriteOutput),
		Arguments: []capability.Argument{
			{Name: "book_id", Description: "Containing book", Required: true},
			{Name: "name", Description: "Chapter title, 1 to 255 characters", Required: true},
			{Name: "description", Description: "Plain description, at most 1900 characters; mutually exclusive with description_html"},
			{Name: "description_html", Description: "Description as HTML, at most 2000 characters; mutually exclusive with description"},
			{Name: "tags", Description: "At most 50 tags as name and value pairs, each at most 255 characters"},
			{Name: "priority", Description: "Position among the siblings in the book, 0 or more"},
			{Name: "default_template_id", Description: "Identifier of a template page used for new pages of the chapter"},
		},
		Fields: chapterWriteFields,
	}

	chaptersUpdate = capability.Descriptor{
		ID: Provider + ".chapters.update", Version: 1, Title: "Update a BookStack chapter",
		Description: "Change the name, description, tags, priority, or default page template of one chapter by " +
			"identifier, or move it into another bound book with book_id. tags replaces all existing tags of the " +
			"chapter; default_template_id null removes the template. Moving needs the delete permission on the " +
			"chapter in BookStack in addition to the update permission",
		Tags: []string{"knowledge", "chapters", "bookstack", "update"}, Provider: Provider,
		Risk:         bookWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1},"book_id":{"type":"integer","minimum":1},"name":{"type":"string","minLength":1,"maxLength":255},` + containerProperties + `,"priority":{"type":"integer","minimum":0},"default_template_id":{"type":["integer","null"],"minimum":1}},"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(chapterWriteOutput),
		Arguments: []capability.Argument{
			{Name: "id", Description: "Chapter identifier", Required: true},
			{Name: "book_id", Description: "Move the chapter into this book"},
			{Name: "name", Description: "New chapter title, 1 to 255 characters"},
			{Name: "description", Description: "New plain description, at most 1900 characters; mutually exclusive with description_html"},
			{Name: "description_html", Description: "New description as HTML, at most 2000 characters; mutually exclusive with description"},
			{Name: "tags", Description: "Replaces all tags of the chapter: at most 50 name and value pairs, each at most 255 characters; an empty list removes all tags"},
			{Name: "priority", Description: "New position among the siblings in the book, 0 or more"},
			{Name: "default_template_id", Description: "Identifier of a template page; null removes the default template"},
		},
		Fields: chapterWriteFields,
	}
)

const bookWriteOutput = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},"description":{"type":"string"},"description_html":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"created_by":{"type":"object"},"updated_by":{"type":"object"},"owned_by":{"type":"object"},"default_template_id":{"type":"integer"},"tags":{"type":"array"}},"required":["id","name","slug"]}`

const chapterWriteOutput = `{"type":"object","properties":{"id":{"type":"integer"},"book_id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},"description":{"type":"string"},"description_html":{"type":"string"},"priority":{"type":"integer"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"created_by":{"type":"object"},"updated_by":{"type":"object"},"owned_by":{"type":"object"},"default_template_id":{"type":"integer"},"tags":{"type":"array"}},"required":["id","book_id","name","slug"]}`

var (
	bookWriteFields = []capability.Field{
		{Name: "id", Description: "Book identifier"},
		{Name: "name", Description: "Book title"},
		{Name: "slug", Description: "URL slug"},
		{Name: "description", Description: "Plain description, untrusted data"},
		{Name: "description_html", Description: "Description as HTML, untrusted data"},
		{Name: "created_at", Description: "Creation timestamp"},
		{Name: "updated_at", Description: "Last change timestamp"},
		{Name: "created_by", Description: "Creator with id and name"},
		{Name: "updated_by", Description: "Last editor with id and name"},
		{Name: "owned_by", Description: "Owner with id and name"},
		{Name: "default_template_id", Description: "Default page template, 0 when there is none"},
		{Name: "tags", Description: "At most 50 name/value pairs"},
	}
	chapterWriteFields = []capability.Field{
		{Name: "id", Description: "Chapter identifier"},
		{Name: "book_id", Description: "Identifier of the containing book"},
		{Name: "name", Description: "Chapter title"},
		{Name: "slug", Description: "URL slug"},
		{Name: "description", Description: "Plain description, untrusted data"},
		{Name: "description_html", Description: "Description as HTML, untrusted data"},
		{Name: "priority", Description: "Position among the siblings in the book"},
		{Name: "created_at", Description: "Creation timestamp"},
		{Name: "updated_at", Description: "Last change timestamp"},
		{Name: "created_by", Description: "Creator with id and name"},
		{Name: "updated_by", Description: "Last editor with id and name"},
		{Name: "owned_by", Description: "Owner with id and name"},
		{Name: "default_template_id", Description: "Default page template, 0 when there is none"},
		{Name: "tags", Description: "At most 50 name/value pairs"},
	}
)

// containerMutation is the request body of a book or chapter write and, decoded from the arguments, its input.
// DefaultTemplateID keeps the literal null that removes the template.
type containerMutation struct {
	Name              string          `json:"name,omitempty"`
	BookID            int64           `json:"book_id,omitempty"`
	Description       string          `json:"description,omitempty"`
	DescriptionHTML   string          `json:"description_html,omitempty"`
	Tags              *[]tagJSON      `json:"tags,omitempty"`
	Priority          *int64          `json:"priority,omitempty"`
	DefaultTemplateID json.RawMessage `json:"default_template_id,omitempty"`
}

// templateID returns the template page to bind: zero when none is given or the template is removed.
func (m containerMutation) templateID() (int64, error) {
	if len(m.DefaultTemplateID) == 0 || string(m.DefaultTemplateID) == "null" {
		return 0, nil
	}
	id, ok := parseID(string(m.DefaultTemplateID))
	if !ok {
		return 0, invalidRequest("default_template_id must be a positive integer or null")
	}
	return id, nil
}

func (m containerMutation) empty() bool {
	return m.Name == "" && m.BookID == 0 && m.Description == "" && m.DescriptionHTML == "" && m.Tags == nil &&
		m.Priority == nil && len(m.DefaultTemplateID) == 0
}

// validateText applies the name, description, and tag limits that books, chapters, and shelves share.
func validateText(name, description, descriptionHTML string, tags *[]tagJSON) error {
	if utf8.RuneCountInString(name) > maxPageNameChars {
		return invalidRequest("name exceeds 255 characters")
	}
	if description != "" && descriptionHTML != "" {
		return invalidRequest("description and description_html are mutually exclusive")
	}
	if utf8.RuneCountInString(description) > maxDescriptionTextChars {
		return invalidRequest("description exceeds 1900 characters")
	}
	if utf8.RuneCountInString(descriptionHTML) > maxDescriptionHTMLChars {
		return invalidRequest("description_html exceeds 2000 characters")
	}
	if tags != nil {
		return validateTags(*tags)
	}
	return nil
}

// validate applies the local rules of a write. They need no I/O, so a violation is refused before any secret.
func (m containerMutation) validate() error {
	if err := validateText(m.Name, m.Description, m.DescriptionHTML, m.Tags); err != nil {
		return err
	}
	if m.Priority != nil && *m.Priority < 0 {
		return invalidRequest("priority must be 0 or more")
	}
	if _, err := m.templateID(); err != nil {
		return err
	}
	return nil
}

func decodeContainer(raw json.RawMessage, op string) (int64, containerMutation, error) {
	var input struct {
		ID int64 `json:"id"`
		containerMutation
	}
	if json.Unmarshal(raw, &input) != nil {
		return 0, containerMutation{}, providerError(op, "the validated arguments could not be read")
	}
	return input.ID, input.containerMutation, nil
}

func invokeBooksCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	_, input, err := decodeContainer(raw, "create book")
	if err != nil {
		return nil, err
	}
	if input.Name == "" {
		return nil, invalidRequest("name is required")
	}
	if err := input.validate(); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if bound.bound() {
		return nil, invalidRequest("a connection bound to books cannot create books, a new book would lie outside them")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreateBook(ctx, input)
}

func invokeBooksUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, change, err := decodeContainer(raw, "update book")
	if err != nil {
		return nil, err
	}
	change.BookID, change.Priority = 0, nil
	if change.empty() {
		return nil, invalidRequest("at least one field to change is required")
	}
	if err := change.validate(); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if err := bound.checkBook(id); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateBook(ctx, id, change)
}

func invokeChaptersCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	_, input, err := decodeContainer(raw, "create chapter")
	if err != nil {
		return nil, err
	}
	if input.Name == "" {
		return nil, invalidRequest("name is required")
	}
	if err := input.validate(); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if err := bound.checkBook(input.BookID); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreateChapter(ctx, input)
}

func invokeChaptersUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, change, err := decodeContainer(raw, "update chapter")
	if err != nil {
		return nil, err
	}
	if id <= 0 {
		return nil, invalidRequest("id must be a positive integer")
	}
	if change.empty() {
		return nil, invalidRequest("at least one field to change is required")
	}
	if err := change.validate(); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if change.BookID != 0 {
		if err := bound.checkBook(change.BookID); err != nil {
			return nil, err
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateChapter(ctx, id, change)
}

// requireTemplateBound reads the template page once as evidence of its book; an unbound connection reads
// nothing, and so does a change that names no template.
func (c *Client) requireTemplateBound(ctx context.Context, input containerMutation) error {
	id, err := input.templateID()
	if err != nil || id == 0 || !c.scope.bound() {
		return err
	}
	var ref bookRef
	if err := c.get(ctx, "get page", "/api/pages/"+strconv.FormatInt(id, 10), nil, &ref, nil, provider.ClassPermission); err != nil {
		return err
	}
	if !c.scope.allows(ref.BookID) {
		return invalidRequest(outsideTemplate)
	}
	return nil
}

// CreateBook sends one create request. A connection bound to books may not create one.
func (c *Client) CreateBook(ctx context.Context, input containerMutation) (output.Object, error) {
	if c.scope.bound() {
		return output.Object{}, invalidRequest("a connection bound to books cannot create books, a new book would lie outside them")
	}
	if err := input.validate(); err != nil {
		return output.Object{}, err
	}
	var book bookJSON
	if err := c.mutate(ctx, "create book", http.MethodPost, "/api/books", input, &book, argNames(booksCreate)); err != nil {
		return output.Object{}, err
	}
	return bookWriteObject(book), nil
}

func (c *Client) UpdateBook(ctx context.Context, id int64, input containerMutation) (output.Object, error) {
	if err := input.validate(); err != nil {
		return output.Object{}, err
	}
	if err := c.scope.checkBook(id); err != nil {
		return output.Object{}, err
	}
	if err := c.requireTemplateBound(ctx, input); err != nil {
		return output.Object{}, err
	}
	var book bookJSON
	if err := c.mutate(ctx, "update book", http.MethodPut, "/api/books/"+strconv.FormatInt(id, 10), input, &book, argNames(booksUpdate)); err != nil {
		return output.Object{}, err
	}
	return bookWriteObject(book), nil
}

func (c *Client) CreateChapter(ctx context.Context, input containerMutation) (output.Object, error) {
	if err := input.validate(); err != nil {
		return output.Object{}, err
	}
	if err := c.scope.checkBook(input.BookID); err != nil {
		return output.Object{}, err
	}
	if err := c.requireTemplateBound(ctx, input); err != nil {
		return output.Object{}, err
	}
	var chapter chapterJSON
	if err := c.mutate(ctx, "create chapter", http.MethodPost, "/api/chapters", input, &chapter, argNames(chaptersCreate)); err != nil {
		return output.Object{}, err
	}
	return chapterWriteObject(chapter), nil
}

// UpdateChapter proves the chapter's book first; a foreign chapter, target book, or template page ends the call
// without a change.
func (c *Client) UpdateChapter(ctx context.Context, id int64, input containerMutation) (output.Object, error) {
	if err := input.validate(); err != nil {
		return output.Object{}, err
	}
	if input.BookID != 0 {
		if err := c.scope.checkBook(input.BookID); err != nil {
			return output.Object{}, err
		}
	}
	if c.scope.bound() {
		if _, err := c.chapterBook(ctx, id); err != nil {
			return output.Object{}, err
		}
	}
	if err := c.requireTemplateBound(ctx, input); err != nil {
		return output.Object{}, err
	}
	var chapter chapterJSON
	if err := c.mutate(ctx, "update chapter", http.MethodPut, "/api/chapters/"+strconv.FormatInt(id, 10), input, &chapter, argNames(chaptersUpdate)); err != nil {
		return output.Object{}, err
	}
	return chapterWriteObject(chapter), nil
}

func bookWriteObject(book bookJSON) output.Object {
	return output.Object{Fields: []output.Field{
		{Name: "id", Value: book.ID}, {Name: "name", Value: clip(book.Name, maxResultString)},
		{Name: "slug", Value: clip(book.Slug, maxResultString)},
		{Name: "description", Value: clip(book.Description, maxDescriptionChars)},
		{Name: "description_html", Value: clip(book.DescriptionHTML, maxDescriptionChars)},
		{Name: "created_at", Value: book.CreatedAt}, {Name: "updated_at", Value: book.UpdatedAt},
		{Name: "created_by", Value: reduceUser(book.CreatedBy)}, {Name: "updated_by", Value: reduceUser(book.UpdatedBy)},
		{Name: "owned_by", Value: reduceUser(book.OwnedBy)},
		{Name: "default_template_id", Value: book.DefaultTemplateID},
		{Name: "tags", Value: reduceTags(book.Tags)},
	}}
}

func chapterWriteObject(chapter chapterJSON) output.Object {
	return output.Object{Fields: []output.Field{
		{Name: "id", Value: chapter.ID}, {Name: "book_id", Value: chapter.BookID},
		{Name: "name", Value: clip(chapter.Name, maxResultString)}, {Name: "slug", Value: clip(chapter.Slug, maxResultString)},
		{Name: "description", Value: clip(chapter.Description, maxDescriptionChars)},
		{Name: "description_html", Value: clip(chapter.DescriptionHTML, maxDescriptionChars)},
		{Name: "priority", Value: chapter.Priority},
		{Name: "created_at", Value: chapter.CreatedAt}, {Name: "updated_at", Value: chapter.UpdatedAt},
		{Name: "created_by", Value: reduceUser(chapter.CreatedBy)}, {Name: "updated_by", Value: reduceUser(chapter.UpdatedBy)},
		{Name: "owned_by", Value: reduceUser(chapter.OwnedBy)},
		{Name: "default_template_id", Value: chapter.DefaultTemplateID},
		{Name: "tags", Value: reduceTags(chapter.Tags)},
	}}
}
