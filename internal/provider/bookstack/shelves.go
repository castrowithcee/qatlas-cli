package bookstack

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// maxShelfBooks bounds the books one write assigns to a shelf.
	maxShelfBooks = 500
	// shelvesInstanceWide names the capability in a refusal by requireInstanceScope.
	shelvesInstanceWide = "shelves"
)

const shelfListOutput = `{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},"description":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"}},"required":["id","name","slug","description","created_at","updated_at"]}}`

const shelfGetOutput = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},"description":{"type":"string"},"description_html":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"created_by":{"type":"object"},"updated_by":{"type":"object"},"owned_by":{"type":"object"},"tags":{"type":"array"},"books":{"type":"array"},"truncated":{"type":"boolean"}},"required":["id","name","slug","books","truncated"]}`

const shelfWriteOutput = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},"description":{"type":"string"},"description_html":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"created_by":{"type":"object"},"updated_by":{"type":"object"},"owned_by":{"type":"object"},"tags":{"type":"array"}},"required":["id","name","slug"]}`

const (
	shelfWriteProperties = `"description":{"type":"string","minLength":1,"maxLength":1900},"description_html":{"type":"string","minLength":1,"maxLength":2000},"tags":` + tagsSchema +
		`,"books":{"type":"array","maxItems":500,"uniqueItems":true,"items":{"type":"integer","minimum":1}}`
)

var (
	shelfMetadataFields = []capability.Field{
		{Name: "id", Description: "Shelf identifier"},
		{Name: "name", Description: "Shelf title"},
		{Name: "slug", Description: "URL slug"},
		{Name: "description", Description: "Plain description, untrusted data"},
		{Name: "description_html", Description: "Description as HTML, untrusted data"},
		{Name: "created_at", Description: "Creation timestamp"},
		{Name: "updated_at", Description: "Last change timestamp"},
		{Name: "created_by", Description: "Creator with id and name"},
		{Name: "updated_by", Description: "Last editor with id and name"},
		{Name: "owned_by", Description: "Owner with id and name"},
		{Name: "tags", Description: "At most 50 name/value pairs"},
	}

	shelvesList = capability.Descriptor{
		ID: Provider + ".shelves.list", Version: 1, Title: "List BookStack shelves",
		Description: "List the shelves of a knowledge base. Shelves span the whole instance, so a connection bound " +
			"to books cannot use this tool",
		Tags: []string{"knowledge", "shelves", "bookstack"},
		Risk: bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(shelfListOutput),
		Arguments: []capability.Argument{
			{Name: "limit", Description: "Maximum number of shelves to return; 0 returns all"},
			{Name: "offset", Description: "Number of shelves to skip"},
		},
		Fields: []capability.Field{
			{Name: "id", Description: "Shelf identifier"},
			{Name: "name", Description: "Shelf title"},
			{Name: "slug", Description: "URL slug"},
			{Name: "description", Description: "Plain description, untrusted data"},
			{Name: "created_at", Description: "Creation timestamp"},
			{Name: "updated_at", Description: "Last change timestamp"},
		},
		Examples: []capability.Example{{Description: "List the first 25 shelves", Arguments: json.RawMessage(`{"limit":25,"offset":0}`)}},
	}

	shelvesGet = capability.Descriptor{
		ID: Provider + ".shelves.get", Version: 1, Title: "Get a BookStack shelf",
		Description: "Read one shelf with its books; the book list holds at most 2000 entries. Shelves span the whole " +
			"instance, so a connection bound to books cannot use this tool",
		Tags: []string{"knowledge", "shelves", "bookstack"},
		Risk: bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(shelfGetOutput),
		Arguments:    []capability.Argument{{Name: "id", Description: "Shelf identifier", Required: true}},
		Fields: append(append([]capability.Field{}, shelfMetadataFields...),
			capability.Field{Name: "books", Description: "Books on the shelf in shelf order with id, name, and slug"},
			capability.Field{Name: "truncated", Description: "True when the book list was cut at 2000 entries"}),
		Examples: []capability.Example{{Description: "Read shelf 3", Arguments: json.RawMessage(`{"id":3}`)}},
	}

	shelvesCreate = capability.Descriptor{
		ID: Provider + ".shelves.create", Version: 1, Title: "Create a BookStack shelf",
		Description: "Create one shelf with an optional description, tags, and an ordered list of books. Shelves span " +
			"the whole instance, so a connection bound to books cannot use this tool",
		Tags: []string{"knowledge", "shelves", "bookstack", "create"}, Provider: Provider,
		Risk:         bookWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":255},` + shelfWriteProperties + `},"required":["name"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(shelfWriteOutput),
		Arguments: []capability.Argument{
			{Name: "name", Description: "Shelf title, 1 to 255 characters", Required: true},
			{Name: "description", Description: "Plain description, at most 1900 characters; mutually exclusive with description_html"},
			{Name: "description_html", Description: "Description as HTML, at most 2000 characters; mutually exclusive with description"},
			{Name: "tags", Description: "At most 50 tags as name and value pairs, each at most 255 characters"},
			{Name: "books", Description: "Identifiers of the books on the shelf in shelf order: at most 500 unique positive integers"},
		},
		Fields: shelfMetadataFields,
	}

	shelvesUpdate = capability.Descriptor{
		ID: Provider + ".shelves.update", Version: 1, Title: "Update a BookStack shelf",
		Description: "Change the name, description, tags, or books of one shelf by identifier. tags replaces all " +
			"existing tags; books replaces all books on the shelf with the given ordered list, an empty list removes " +
			"every book from the shelf, and omitting books leaves them unchanged. remove_cover true removes the cover image. Shelves span the whole instance, " +
			"so a connection bound to books cannot use this tool",
		Tags: []string{"knowledge", "shelves", "bookstack", "update"}, Provider: Provider,
		Risk:         bookWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1},"name":{"type":"string","minLength":1,"maxLength":255},` + shelfWriteProperties + `,"remove_cover":{"type":"boolean"}},"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(shelfWriteOutput),
		Arguments: []capability.Argument{
			{Name: "id", Description: "Shelf identifier", Required: true},
			{Name: "name", Description: "New shelf title, 1 to 255 characters"},
			{Name: "description", Description: "New plain description, at most 1900 characters; mutually exclusive with description_html"},
			{Name: "description_html", Description: "New description as HTML, at most 2000 characters; mutually exclusive with description"},
			{Name: "tags", Description: "Replaces all tags of the shelf: at most 50 name and value pairs, each at most 255 characters; an empty list removes all tags"},
			{Name: "books", Description: "Replaces all books on the shelf with this ordered list of at most 500 unique positive integers; an empty list removes every book from the shelf; omit it to keep the books"},
			removeCoverArgument,
		},
		Fields: shelfMetadataFields,
	}
)

type shelfJSON struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	Slug            string    `json:"slug"`
	Description     string    `json:"description"`
	DescriptionHTML string    `json:"description_html"`
	CreatedAt       string    `json:"created_at"`
	UpdatedAt       string    `json:"updated_at"`
	CreatedBy       *userJSON `json:"created_by"`
	UpdatedBy       *userJSON `json:"updated_by"`
	OwnedBy         *userJSON `json:"owned_by"`
	Tags            []tagJSON `json:"tags"`
	Books           []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	} `json:"books"`
}

// shelfMutation is the request body of a shelf write and, decoded from the arguments, its input. Books keeps
// the difference between an omitted list (nil, sent as nothing) and an empty one (sent as []).
type shelfMutation struct {
	Name            string     `json:"name,omitempty"`
	Description     string     `json:"description,omitempty"`
	DescriptionHTML string     `json:"description_html,omitempty"`
	Tags            *[]tagJSON `json:"tags,omitempty"`
	Books           *[]int64   `json:"books,omitempty"`
	// Image is the JSON null that removes a cover, set from remove_cover and never taken from arguments.
	Image json.RawMessage `json:"image,omitempty"`
}

func (m shelfMutation) empty() bool {
	return m.Name == "" && m.Description == "" && m.DescriptionHTML == "" && m.Tags == nil && m.Books == nil && len(m.Image) == 0
}

// validate applies the local rules of a write. They need no I/O, so a violation is refused before any secret.
func (m shelfMutation) validate() error {
	if err := validateText(m.Name, m.Description, m.DescriptionHTML, m.Tags); err != nil {
		return err
	}
	if m.Books == nil {
		return nil
	}
	if len(*m.Books) > maxShelfBooks {
		return invalidRequest("books holds more than 500 entries")
	}
	seen := map[int64]bool{}
	for _, id := range *m.Books {
		if id <= 0 || len(strconv.FormatInt(id, 10)) > maxIDDigits {
			return invalidRequest("books must hold positive integers")
		}
		if seen[id] {
			return invalidRequest("books names a book more than once")
		}
		seen[id] = true
	}
	return nil
}

func decodeShelf(raw json.RawMessage, op string) (int64, shelfMutation, error) {
	var input struct {
		ID int64 `json:"id"`
		shelfMutation
		RemoveCover bool `json:"remove_cover"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return 0, shelfMutation{}, providerError(op, "the validated arguments could not be read")
	}
	input.Image = nil
	if input.RemoveCover {
		input.Image = json.RawMessage("null")
	}
	return input.ID, input.shelfMutation, nil
}

func checkShelfID(id int64) error {
	if id <= 0 || len(strconv.FormatInt(id, 10)) > maxIDDigits {
		return invalidRequest("id must be a positive integer")
	}
	return nil
}

func invokeShelvesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, shelvesInstanceWide); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListShelves(ctx, in.Limit, in.Offset)
}

func invokeShelvesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, shelvesInstanceWide); err != nil {
		return nil, err
	}
	if err := checkShelfID(in.ID); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetShelf(ctx, in.ID)
}

func invokeShelvesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	_, input, err := decodeShelf(raw, "create shelf")
	if err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, shelvesInstanceWide); err != nil {
		return nil, err
	}
	if input.Name == "" {
		return nil, invalidRequest("name is required")
	}
	if err := input.validate(); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreateShelf(ctx, input)
}

func invokeShelvesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, change, err := decodeShelf(raw, "update shelf")
	if err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, shelvesInstanceWide); err != nil {
		return nil, err
	}
	if err := checkShelfID(id); err != nil {
		return nil, err
	}
	if change.empty() {
		return nil, invalidRequest("at least one field to change is required")
	}
	if err := change.validate(); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateShelf(ctx, id, change)
}

// ListShelves returns shelves of the instance.
func (c *Client) ListShelves(ctx context.Context, limit, offset int) (output.Collection, error) {
	rows, err := scanList(ctx, c, scanSpec[listedJSON]{
		op: "list shelves", path: "/api/shelves", limit: limit, offset: offset,
		arguments: argNames(shelvesList),
		id:        func(s listedJSON) int64 { return s.ID },
		keep:      func(listedJSON) bool { return true },
		row: func(s listedJSON) output.Row {
			return output.Row{
				"id": s.ID, "name": clip(s.Name, maxResultString), "slug": clip(s.Slug, maxResultString),
				"description": clip(s.Description, maxDescriptionChars), "created_at": s.CreatedAt, "updated_at": s.UpdatedAt,
			}
		},
	})
	if err != nil {
		return output.Collection{}, err
	}
	return output.Collection{Columns: fieldNames(shelvesList), Rows: rows}, nil
}

// GetShelf returns one shelf with its books.
func (c *Client) GetShelf(ctx context.Context, id int64) (output.Object, error) {
	var shelf shelfJSON
	if err := c.get(ctx, "get shelf", "/api/shelves/"+strconv.FormatInt(id, 10), nil, &shelf, nil, provider.ClassPermission); err != nil {
		return output.Object{}, err
	}
	truncated := len(shelf.Books) > maxTreeEntries
	books := make([]map[string]any, 0, min(len(shelf.Books), maxTreeEntries))
	for _, b := range shelf.Books {
		if len(books) >= maxTreeEntries {
			break
		}
		books = append(books, map[string]any{"id": b.ID, "name": clip(b.Name, maxResultString), "slug": clip(b.Slug, maxResultString)})
	}
	fields := shelfWriteObject(shelf).Fields
	fields = append(fields, output.Field{Name: "books", Value: books}, output.Field{Name: "truncated", Value: truncated})
	return output.Object{Fields: fields}, nil
}

// CreateShelf sends one create request.
func (c *Client) CreateShelf(ctx context.Context, input shelfMutation) (output.Object, error) {
	if err := input.validate(); err != nil {
		return output.Object{}, err
	}
	var shelf shelfJSON
	if err := c.mutate(ctx, "create shelf", http.MethodPost, "/api/shelves", input, &shelf, argNames(shelvesCreate)); err != nil {
		return output.Object{}, err
	}
	return shelfWriteObject(shelf), nil
}

// UpdateShelf sends one update request.
func (c *Client) UpdateShelf(ctx context.Context, id int64, input shelfMutation) (output.Object, error) {
	if err := input.validate(); err != nil {
		return output.Object{}, err
	}
	var shelf shelfJSON
	if err := c.mutate(ctx, "update shelf", http.MethodPut, "/api/shelves/"+strconv.FormatInt(id, 10), input, &shelf, argNames(shelvesUpdate)); err != nil {
		return output.Object{}, err
	}
	return shelfWriteObject(shelf), nil
}

func shelfWriteObject(shelf shelfJSON) output.Object {
	return output.Object{Fields: []output.Field{
		{Name: "id", Value: shelf.ID}, {Name: "name", Value: clip(shelf.Name, maxResultString)},
		{Name: "slug", Value: clip(shelf.Slug, maxResultString)},
		{Name: "description", Value: clip(shelf.Description, maxDescriptionChars)},
		{Name: "description_html", Value: clip(shelf.DescriptionHTML, maxDescriptionChars)},
		{Name: "created_at", Value: shelf.CreatedAt}, {Name: "updated_at", Value: shelf.UpdatedAt},
		{Name: "created_by", Value: reduceUser(shelf.CreatedBy)}, {Name: "updated_by", Value: reduceUser(shelf.UpdatedBy)},
		{Name: "owned_by", Value: reduceUser(shelf.OwnedBy)},
		{Name: "tags", Value: reduceTags(shelf.Tags)},
	}}
}

var shelvesDelete = capability.Descriptor{
	ID: Provider + ".shelves.delete", Version: 1, Title: "Delete a BookStack shelf",
	Description: "Delete one shelf by identifier; only the shelf is removed, its books stay. Not available for a " +
		"connection bound to books, because shelves are instance-wide",
	Tags: []string{"knowledge", "shelves", "bookstack", "delete"}, Provider: Provider,
	RequiresToolAllowList: true,
	Risk:                  bookWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema:           json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`),
	OutputSchema:          json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},"required":["deleted"],"additionalProperties":false}`),
	Arguments:             []capability.Argument{{Name: "id", Description: "Shelf identifier", Required: true}},
}

func invokeShelvesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	id, err := decodeDeleteID(raw, "delete shelf")
	if err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, shelvesInstanceWide); err != nil {
		return nil, err
	}
	if err := checkShelfID(id); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.DeleteShelf(ctx, id); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}

// DeleteShelf sends one delete request.
func (c *Client) DeleteShelf(ctx context.Context, id int64) error {
	return c.mutate(ctx, "delete shelf", http.MethodDelete, "/api/shelves/"+strconv.FormatInt(id, 10), nil, nil, argNames(shelvesDelete))
}
