// Package bookstack implements controlled page access to a BookStack instance over its REST API.
//
// Page content arrives as HTML and Markdown. Qatlas treats both as untrusted data: it is passed through
// to the output encoders and never rendered, interpreted, or stored.
package bookstack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Provider is the provider name used in the configuration.
const Provider = "bookstack"

// dataSensitivity classifies results as content and metadata from the configured BookStack instance. It
// is deliberately provider-specific; the architecture defines no global sensitivity taxonomy.
const dataSensitivity = "bookstack-content"

// The secret roles a BookStack credential must supply.
const (
	roleTokenID     = "token-id"
	roleTokenSecret = "token-secret"
)

// maxCount is the largest page size the BookStack API accepts.
const maxCount = 500

// maxPageContentBytes bounds the content of a write request.
const maxPageContentBytes = 1 << 20

// Limits of a page write. They mirror the input schemas and are checked before any secret or request.
const (
	maxPageTags       = 50
	maxTagTextChars   = 255
	maxChangelogChars = 180
	maxPageNameChars  = 255
	// maxMutationBodyBytes bounds the encoded request; content is limited by maxPageContentBytes before,
	// and JSON escaping may enlarge it.
	maxMutationBodyBytes = 8 << 20
)

// tagsSchema is the shared input schema of the tags argument.
const tagsSchema = `{"type":"array","maxItems":50,"items":{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":255},"value":{"type":"string","maxLength":255}},"required":["name"],"additionalProperties":false}}`

// maxReadResponseBytes bounds every response that is read.
const maxReadResponseBytes = 16 << 20

// changeUncertain is appended to a failure of a change whose request may have reached BookStack. Qatlas
// never repeats such a request by itself.
const changeUncertain = "; this change may have taken effect, read the current state in BookStack before repeating it"

// permissionHint explains a refused action without relaying any provider text.
const permissionHint = "the token's user lacks the BookStack role permission for this action, or the token expired or lacks API access"

// defaultTimeout bounds every request. Without it a hanging server would block the command forever.
const defaultTimeout = 30 * time.Second

// Operations implemented by this provider.
var (
	bookstackReadRisk = capability.Risk{
		Effect:          capability.EffectRead,
		Idempotency:     capability.IdempotencySafe,
		Confirmation:    capability.ConfirmationNone,
		OpenWorld:       true,
		DataSensitivity: dataSensitivity,
	}

	pagesList = capability.Descriptor{
		ID:           Provider + ".pages.list",
		Version:      1,
		Title:        "List BookStack pages",
		Description:  "List pages of a knowledge base",
		Tags:         []string{"knowledge", "pages", "bookstack"},
		Risk:         bookstackReadRisk,
		Provider:     Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"book_id":{"type":"integer","minimum":1},"chapter_id":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},"book_id":{"type":"integer"},"chapter_id":{"type":"integer"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"priority":{"type":"integer"},"draft":{"type":"boolean"},"template":{"type":"boolean"},"owned_by":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"}}}},"required":["id","name","slug","book_id","chapter_id","created_at","updated_at"]}}`),
		Arguments: []capability.Argument{
			{Name: "book_id", Description: "Only pages of this book; mutually exclusive with chapter_id; required for a connection bound to several books"},
			{Name: "chapter_id", Description: "Only pages of this chapter; mutually exclusive with book_id"},
			{Name: "limit", Description: "Maximum number of pages to return; 0 returns all"},
			{Name: "offset", Description: "Number of pages to skip"},
		},
		Fields: []capability.Field{
			{Name: "id", Description: "Page identifier"},
			{Name: "name", Description: "Page title"},
			{Name: "slug", Description: "URL slug"},
			{Name: "book_id", Description: "Identifier of the containing book"},
			{Name: "chapter_id", Description: "Identifier of the containing chapter, 0 when there is none"},
			{Name: "created_at", Description: "Creation timestamp"},
			{Name: "updated_at", Description: "Last change timestamp"},
			{Name: "priority", Description: "Position among the siblings in the book or chapter"},
			{Name: "draft", Description: "Whether the page is an unpublished draft"},
			{Name: "template", Description: "Whether the page is a template"},
			{Name: "owned_by", Description: "Owner as id and name"},
		},
		Examples: []capability.Example{{
			Description: "List the first 25 pages",
			Arguments:   json.RawMessage(`{"limit":25,"offset":0}`),
		}},
	}

	pagesGet = capability.Descriptor{
		ID:           Provider + ".pages.get",
		Version:      1,
		Title:        "Get a BookStack page",
		Description:  "Read one page of a knowledge base",
		Tags:         []string{"knowledge", "pages", "bookstack"},
		Risk:         bookstackReadRisk,
		Provider:     Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},"book_id":{"type":"integer"},"chapter_id":{"type":"integer"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"html":{"type":"string"},"markdown":{"type":"string"},"raw_html":{"type":"string"},"priority":{"type":"integer"},"draft":{"type":"boolean"},"template":{"type":"boolean"},"revision_count":{"type":"integer"},"editor":{"type":"string"},"tags":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"value":{"type":"string"}}}},"created_by":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"}}},"updated_by":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"}}},"owned_by":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"}}}},"required":["id","name","slug","book_id","chapter_id","created_at","updated_at","html","markdown"]}`),
		Arguments:    []capability.Argument{{Name: "id", Description: "Page identifier", Required: true}},
		Fields: []capability.Field{
			{Name: "id", Description: "Page identifier"},
			{Name: "name", Description: "Page title"},
			{Name: "slug", Description: "URL slug"},
			{Name: "book_id", Description: "Identifier of the containing book"},
			{Name: "chapter_id", Description: "Identifier of the containing chapter, 0 when there is none"},
			{Name: "created_at", Description: "Creation timestamp"},
			{Name: "updated_at", Description: "Last change timestamp"},
			{Name: "html", Description: "Rendered page content, untrusted data"},
			{Name: "markdown", Description: "Markdown page content when the page uses the Markdown editor, untrusted data"},
			{Name: "raw_html", Description: "HTML as stored, what the editor shows, untrusted data"},
			{Name: "priority", Description: "Position among the siblings in the book or chapter"},
			{Name: "draft", Description: "Whether the page is an unpublished draft"},
			{Name: "template", Description: "Whether the page is a template"},
			{Name: "revision_count", Description: "Number of stored revisions"},
			{Name: "editor", Description: "Editor that last saved the page: wysiwyg or markdown"},
			{Name: "tags", Description: "Tags as name and value pairs, untrusted data"},
			{Name: "created_by", Description: "Creator as id and name"},
			{Name: "updated_by", Description: "Last editor as id and name"},
			{Name: "owned_by", Description: "Owner as id and name"},
		},
		Examples: []capability.Example{{
			Description: "Read page 42",
			Arguments:   json.RawMessage(`{"id":42}`),
		}},
	}

	pagesCreate = capability.Descriptor{
		ID: Provider + ".pages.create", Version: 1, Title: "Create a BookStack page",
		Description: "Create one page in a specified book or chapter from exactly one of HTML and Markdown, " +
			"with optional tags and priority. HTML containing data: images makes BookStack store them as gallery " +
			"images of the page when it is saved",
		Tags: []string{"knowledge", "pages", "bookstack", "create"}, Provider: Provider,
		Risk:         capability.Risk{Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent, Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":255},"book_id":{"type":"integer","minimum":1},"chapter_id":{"type":"integer","minimum":1},"markdown":{"type":"string","minLength":1,"maxLength":1048576},"html":{"type":"string","minLength":1,"maxLength":1048576},"tags":` + tagsSchema + `,"priority":{"type":"integer","minimum":0}},"required":["name"],"additionalProperties":false}`),
		OutputSchema: pagesGet.OutputSchema,
		Arguments: []capability.Argument{
			{Name: "name", Description: "Page title", Required: true},
			{Name: "book_id", Description: "Containing book; required when chapter_id is omitted"},
			{Name: "chapter_id", Description: "Containing chapter; mutually exclusive with book_id"},
			{Name: "markdown", Description: "Markdown page content; exactly one of markdown and html is required, at most 1 MiB"},
			{Name: "html", Description: "HTML page content; exactly one of html and markdown is required, at most 1 MiB; data: images become gallery images"},
			{Name: "tags", Description: "At most 50 tags as name and value pairs, each at most 255 characters"},
			{Name: "priority", Description: "Position among the siblings, 0 or more"},
		},
	}

	pagesUpdate = capability.Descriptor{
		ID: Provider + ".pages.update", Version: 1, Title: "Update a BookStack page",
		Description: "Change the title, HTML or Markdown, tags, priority of one page by identifier, record a changelog " +
			"entry, or move it into a bound book or chapter. tags replaces all existing tags of the page. HTML " +
			"containing data: images makes BookStack store them as gallery images of the page when it is saved. " +
			"Moving needs the delete permission on the page in BookStack in addition to the update permission",
		Tags: []string{"knowledge", "pages", "bookstack", "update"}, Provider: Provider,
		Risk:         capability.Risk{Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent, Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1},"name":{"type":"string","minLength":1,"maxLength":255},"markdown":{"type":"string","minLength":1,"maxLength":1048576},"html":{"type":"string","minLength":1,"maxLength":1048576},"tags":` + tagsSchema + `,"priority":{"type":"integer","minimum":0},"changelog":{"type":"string","minLength":1,"maxLength":180},"book_id":{"type":"integer","minimum":1},"chapter_id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`),
		OutputSchema: pagesGet.OutputSchema,
		Arguments: []capability.Argument{
			{Name: "id", Description: "Page identifier", Required: true},
			{Name: "name", Description: "New page title"},
			{Name: "markdown", Description: "New Markdown content; mutually exclusive with html, at most 1 MiB"},
			{Name: "html", Description: "New HTML content; mutually exclusive with markdown, at most 1 MiB; data: images become gallery images"},
			{Name: "tags", Description: "Replaces all tags of the page: at most 50 name and value pairs, each at most 255 characters; an empty list removes all tags"},
			{Name: "priority", Description: "New position among the siblings, 0 or more"},
			{Name: "changelog", Description: "Revision note of 1 to 180 characters"},
			{Name: "book_id", Description: "Move the page into this book; mutually exclusive with chapter_id"},
			{Name: "chapter_id", Description: "Move the page into this chapter; mutually exclusive with book_id"},
		},
	}

	pagesDelete = capability.Descriptor{
		ID: Provider + ".pages.delete", Version: 1, Title: "Delete a BookStack page",
		Description: "Delete one page by identifier; BookStack moves the page to the recycle bin",
		Tags:        []string{"knowledge", "pages", "bookstack", "delete"}, Provider: Provider,
		RequiresToolAllowList: true,
		Risk:                  capability.Risk{Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent, Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
		InputSchema:           json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`),
		OutputSchema:          json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},"required":["deleted"],"additionalProperties":false}`),
		Arguments:             []capability.Argument{{Name: "id", Description: "Page identifier", Required: true}},
	}
)

// Register records this provider's operations and their existing command handlers.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "BookStack", DefaultPermissions: []config.Permission{config.PermissionRead},
		Description: "Self-hosted documentation platform for team knowledge",
		Groups:      toolGroups,
		SecretRoles: []config.SecretRole{
			{Name: roleTokenID, Description: "BookStack token ID: the value labeled Token ID when you create an API token; it is not a name you choose"},
			{Name: roleTokenSecret, Description: "BookStack token secret: the value labeled Token Secret when you create the same API token"},
		},
		Target: config.TargetMetadata{
			Label:       "books",
			Multiple:    true,
			Description: "optional books this connection is bound to; without a target the whole token scope is reachable",
			Kinds: []config.TargetKind{{
				Name:        "book",
				Description: "a book whose pages this connection may list, read, create, update, and delete",
				Forms:       []string{"book/BOOK_ID"},
			}},
			Validate:    validateTarget,
			ValidateSet: validateSet,
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read content", Recommended: true,
			Description: "lists and reads pages, books, chapters, shelves, tags, page comments, attachments and images of pages, reads instance information, and searches content; changes nothing in BookStack",
			Tools: []string{pagesList.ID, pagesGet.ID, contentSearch.ID, booksList.ID, booksGet.ID,
				chaptersList.ID, chaptersGet.ID, shelvesList.ID, shelvesGet.ID, tagsList.ID, tagsValues.ID, commentsList.ID,
				commentsGet.ID, attachmentsList.ID, attachmentsGet.ID, imagesList.ID, imagesGet.ID, systemGet.ID, contentExport.ID},
		}},
	}, func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor) (provider.Class, error) {
		client, err := Open(ctx, resolved, secrets, red)
		if err != nil {
			return "", err
		}
		return client.TestConnection(ctx), nil
	}); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: withGroup(pagesList), Handler: capability.Handler(invokePagesList)},
		capability.Operation{Descriptor: withGroup(pagesGet), Handler: capability.Handler(invokePagesGet)},
		capability.Operation{Descriptor: withGroup(pagesCreate), Handler: capability.Handler(invokePagesCreate)},
		capability.Operation{Descriptor: withGroup(pagesUpdate), Handler: capability.Handler(invokePagesUpdate)},
		capability.Operation{Descriptor: withGroup(pagesDelete), Handler: capability.Handler(invokePagesDelete)},
		capability.Operation{Descriptor: withGroup(contentSearch), Handler: capability.Handler(invokeContentSearch)},
		capability.Operation{Descriptor: withGroup(booksList), Handler: capability.Handler(invokeBooksList)},
		capability.Operation{Descriptor: withGroup(booksGet), Handler: capability.Handler(invokeBooksGet)},
		capability.Operation{Descriptor: withGroup(chaptersList), Handler: capability.Handler(invokeChaptersList)},
		capability.Operation{Descriptor: withGroup(chaptersGet), Handler: capability.Handler(invokeChaptersGet)},
		capability.Operation{Descriptor: withGroup(booksCreate), Handler: capability.Handler(invokeBooksCreate)},
		capability.Operation{Descriptor: withGroup(booksUpdate), Handler: capability.Handler(invokeBooksUpdate)},
		capability.Operation{Descriptor: withGroup(chaptersCreate), Handler: capability.Handler(invokeChaptersCreate)},
		capability.Operation{Descriptor: withGroup(chaptersUpdate), Handler: capability.Handler(invokeChaptersUpdate)},
		capability.Operation{Descriptor: withGroup(shelvesList), Handler: capability.Handler(invokeShelvesList)},
		capability.Operation{Descriptor: withGroup(shelvesGet), Handler: capability.Handler(invokeShelvesGet)},
		capability.Operation{Descriptor: withGroup(shelvesCreate), Handler: capability.Handler(invokeShelvesCreate)},
		capability.Operation{Descriptor: withGroup(shelvesUpdate), Handler: capability.Handler(invokeShelvesUpdate)},
		capability.Operation{Descriptor: withGroup(booksDelete), Handler: capability.Handler(invokeBooksDelete)},
		capability.Operation{Descriptor: withGroup(chaptersDelete), Handler: capability.Handler(invokeChaptersDelete)},
		capability.Operation{Descriptor: withGroup(shelvesDelete), Handler: capability.Handler(invokeShelvesDelete)},
		capability.Operation{Descriptor: withGroup(tagsList), Handler: capability.Handler(invokeTagsList)},
		capability.Operation{Descriptor: withGroup(tagsValues), Handler: capability.Handler(invokeTagsValues)},
		capability.Operation{Descriptor: withCommentsGroup(commentsList), Handler: capability.Handler(invokeCommentsList)},
		capability.Operation{Descriptor: withCommentsGroup(commentsGet), Handler: capability.Handler(invokeCommentsGet)},
		capability.Operation{Descriptor: withCommentsGroup(commentsCreate), Handler: capability.Handler(invokeCommentsCreate)},
		capability.Operation{Descriptor: withCommentsGroup(commentsUpdate), Handler: capability.Handler(invokeCommentsUpdate)},
		capability.Operation{Descriptor: withCommentsGroup(commentsDelete), Handler: capability.Handler(invokeCommentsDelete)},
		capability.Operation{Descriptor: withGroup(contentExport), Handler: capability.Handler(invokeContentExport)},
		capability.Operation{Descriptor: withGroup(contentDownload), Handler: capability.Handler(invokeContentDownload)},
		capability.Operation{Descriptor: withFilesGroup(attachmentsList), Handler: capability.Handler(invokeAttachmentsList)},
		capability.Operation{Descriptor: withFilesGroup(attachmentsGet), Handler: capability.Handler(invokeAttachmentsGet)},
		capability.Operation{Descriptor: withFilesGroup(attachmentsDownload), Handler: capability.Handler(invokeAttachmentsDownload)},
		capability.Operation{Descriptor: withFilesGroup(imagesList), Handler: capability.Handler(invokeImagesList)},
		capability.Operation{Descriptor: withFilesGroup(imagesGet), Handler: capability.Handler(invokeImagesGet)},
		capability.Operation{Descriptor: withFilesGroup(imagesDownload), Handler: capability.Handler(invokeImagesDownload)},
		capability.Operation{Descriptor: withFilesGroup(attachmentsLink), Handler: capability.Handler(invokeAttachmentsLink)},
		capability.Operation{Descriptor: withFilesGroup(attachmentsUpload), Handler: capability.Handler(invokeAttachmentsUpload)},
		capability.Operation{Descriptor: withFilesGroup(attachmentsUpdate), Handler: capability.Handler(invokeAttachmentsUpdate)},
		capability.Operation{Descriptor: withFilesGroup(attachmentsReplace), Handler: capability.Handler(invokeAttachmentsReplace)},
		capability.Operation{Descriptor: withFilesGroup(attachmentsDelete), Handler: capability.Handler(invokeAttachmentsDelete)},
		capability.Operation{Descriptor: withAdministrationGroup(systemGet), Handler: capability.Handler(invokeSystemGet)},
	)
}

func invokePagesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		BookID    *int64 `json:"book_id"`
		ChapterID *int64 `json:"chapter_id"`
		Limit     int    `json:"limit"`
		Offset    int    `json:"offset"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, err
	}
	var target listTarget
	if arguments.BookID != nil {
		target.BookID = *arguments.BookID
		if target.BookID <= 0 {
			return nil, invalidRequest("book_id must be a positive integer")
		}
	}
	if arguments.ChapterID != nil {
		target.ChapterID = *arguments.ChapterID
		if target.ChapterID <= 0 {
			return nil, invalidRequest("chapter_id must be a positive integer")
		}
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if _, err := bound.listBook(target); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListPages(ctx, target, arguments.Limit, arguments.Offset)
}

func invokePagesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetPage(ctx, strconv.FormatInt(arguments.ID, 10))
}

type pageMutation struct {
	Name      string     `json:"name,omitempty"`
	BookID    int64      `json:"book_id,omitempty"`
	ChapterID int64      `json:"chapter_id,omitempty"`
	HTML      string     `json:"html,omitempty"`
	Markdown  string     `json:"markdown,omitempty"`
	Tags      *[]tagJSON `json:"tags,omitempty"`
	Priority  *int64     `json:"priority,omitempty"`
	Changelog string     `json:"changelog,omitempty"`
}

// validate applies the local rules of a page write. They need no I/O, so a violation is refused before any
// secret is read.
func (m pageMutation) validate() error {
	if m.HTML != "" && m.Markdown != "" {
		return invalidRequest("html and markdown are mutually exclusive")
	}
	if len(m.HTML) > maxPageContentBytes || len(m.Markdown) > maxPageContentBytes {
		return invalidRequest("the page content exceeds the limit of 1 MiB")
	}
	if utf8.RuneCountInString(m.Name) > maxPageNameChars {
		return invalidRequest("name exceeds 255 characters")
	}
	if m.Tags != nil {
		if err := validateTags(*m.Tags); err != nil {
			return err
		}
	}
	if m.Priority != nil && *m.Priority < 0 {
		return invalidRequest("priority must be 0 or more")
	}
	if m.Changelog != "" && utf8.RuneCountInString(m.Changelog) > maxChangelogChars {
		return invalidRequest("changelog exceeds 180 characters")
	}
	return nil
}

// validateTags applies the limits of a tag list.
func validateTags(tags []tagJSON) error {
	if len(tags) > maxPageTags {
		return invalidRequest("tags holds more than 50 entries")
	}
	for _, tag := range tags {
		if tag.Name == "" {
			return invalidRequest("a tag needs a name")
		}
		if utf8.RuneCountInString(tag.Name) > maxTagTextChars || utf8.RuneCountInString(tag.Value) > maxTagTextChars {
			return invalidRequest("a tag name or value exceeds 255 characters")
		}
	}
	return nil
}

func invokePagesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input pageMutation
	if json.Unmarshal(raw, &input) != nil || (input.BookID == 0) == (input.ChapterID == 0) {
		return nil, providerError("create page", "exactly one of book_id or chapter_id is required")
	}
	if (input.HTML == "") == (input.Markdown == "") {
		return nil, invalidRequest("exactly one of html and markdown is required")
	}
	input.Changelog = ""
	if err := input.validate(); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if input.BookID != 0 {
		if err := bound.checkBook(input.BookID); err != nil {
			return nil, err
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreatePage(ctx, input)
}

func invokePagesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input struct {
		ID int64 `json:"id"`
		pageMutation
	}
	if json.Unmarshal(raw, &input) != nil {
		return nil, providerError("update page", "the validated arguments could not be read")
	}
	change := input.pageMutation
	if change.BookID != 0 && change.ChapterID != 0 {
		return nil, invalidRequest("book_id and chapter_id are mutually exclusive")
	}
	if change.Name == "" && change.HTML == "" && change.Markdown == "" && change.Tags == nil &&
		change.Priority == nil && change.Changelog == "" && change.BookID == 0 && change.ChapterID == 0 {
		return nil, invalidRequest("at least one field to change is required")
	}
	if err := change.validate(); err != nil {
		return nil, err
	}
	if change.BookID != 0 {
		bound, err := boundScope(resolved)
		if err != nil {
			return nil, err
		}
		if err := bound.checkBook(change.BookID); err != nil {
			return nil, err
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdatePage(ctx, strconv.FormatInt(input.ID, 10), change)
}

func invokePagesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input struct {
		ID int64 `json:"id"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return nil, providerError("delete page", "the validated arguments could not be read")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.DeletePage(ctx, strconv.FormatInt(input.ID, 10)); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}

// Client talks to one BookStack instance with one credential.
type Client struct {
	scope scope
	base  *url.URL
	auth  string
	http  *http.Client
}

// Open builds a client for a resolved connection. The secrets come from the resolver, which owns the
// cascade and the redaction; this provider only asks for the two roles it needs and never returns them.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	base, err := baseOf(resolved.BaseURL)
	if err != nil {
		return nil, &provider.Error{Class: provider.ClassProviderError, Op: "open", Message: err.Error()}
	}

	tokenID, err := role(ctx, resolved, secrets, roleTokenID)
	if err != nil {
		return nil, err
	}
	tokenSecret, err := role(ctx, resolved, secrets, roleTokenSecret)
	if err != nil {
		return nil, err
	}
	if red != nil {
		red.Add(tokenID, tokenSecret, tokenID+":"+tokenSecret)
	}

	return &Client{
		scope: bound,
		base:  base,
		auth:  "Token " + tokenID + ":" + tokenSecret,
		http:  newHTTPClient(),
	}, nil
}

// transport carries every request. A nil value is Go's default transport; tests replace it.
var transport http.RoundTripper

// baseOf accepts an https URL of a BookStack instance, optionally below an installation path, never with
// user info, a query, or a fragment. The reason does not quote the URL.
func baseOf(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" {
		return nil, errors.New("a BookStack service needs a usable https URL, without user, query, or fragment")
	}
	return parsed, nil
}

// newHTTPClient never follows a redirect: it would carry the credential to a location the user did not
// configure and could turn a change into a different request.
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       defaultTimeout,
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// role resolves one secret role of the connection. Which stage delivers is not this provider's business:
// it needs the value, and the resolver decides where it comes from.
func role(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, name string) (string, error) {
	if secrets == nil {
		return "", &provider.Error{
			Class: provider.ClassProviderError, Op: "open",
			Message: "no credential resolver was configured",
		}
	}
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, name)
	if err != nil {
		return "", err
	}
	return value.Secret, nil
}

// pageJSON mirrors the BookStack API fields this provider reads.
type pageJSON struct {
	ID        int64  `json:"id"`
	BookID    int64  `json:"book_id"`
	ChapterID int64  `json:"chapter_id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	HTML      string `json:"html"`
	Markdown  string `json:"markdown"`

	RawHTML       string    `json:"raw_html"`
	Priority      int64     `json:"priority"`
	Draft         bool      `json:"draft"`
	Template      bool      `json:"template"`
	RevisionCount int64     `json:"revision_count"`
	Editor        string    `json:"editor"`
	Tags          []tagJSON `json:"tags"`
	CreatedBy     *userJSON `json:"created_by"`
	UpdatedBy     *userJSON `json:"updated_by"`
	OwnedBy       *userJSON `json:"owned_by"`
}

type listJSON struct {
	Data  []pageJSON `json:"data"`
	Total int        `json:"total"`
}

type errorJSON struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// maxScanRequests bounds how many list requests one call may send while it filters rows itself, so a server
// that ignores the filter or reports an endless total cannot keep the call reading.
const maxScanRequests = 200

// listTarget names the book or chapter a list is limited to; zero means not given.
type listTarget struct {
	BookID    int64
	ChapterID int64
}

// listBook applies the local rules of a list against the connection's books, before any request. It returns
// the target with the book that applies; a chapter still needs its evidence read.
func (s scope) listBook(t listTarget) (listTarget, error) {
	if t.BookID != 0 && t.ChapterID != 0 {
		return t, invalidRequest("book_id and chapter_id are mutually exclusive")
	}
	if t.BookID != 0 {
		return t, s.checkBook(t.BookID)
	}
	if !s.bound() || t.ChapterID != 0 {
		return t, nil
	}
	if len(s.books) > 1 {
		return t, invalidRequest(bookIDRequiredMsg)
	}
	t.BookID = s.books[0]
	return t, nil
}

// ListPages returns pages, honouring limit and offset. A limit of zero or less returns every page the
// instance reports, fetched in pages of at most 500 records. With a book or chapter, or a bound connection,
// every row is checked here against its book and chapter and foreign rows are dropped, because BookStack
// silently ignores a filter it does not know; limit and offset then count the rows that remain.
func (c *Client) ListPages(ctx context.Context, target listTarget, limit, offset int) (output.Collection, error) {
	target, err := c.scope.listBook(target)
	if err != nil {
		return output.Collection{}, err
	}
	if target.ChapterID != 0 && c.scope.bound() {
		book, err := c.chapterBook(ctx, target.ChapterID)
		if err != nil {
			return output.Collection{}, err
		}
		target.BookID = book
	}
	filtered := target.BookID != 0 || target.ChapterID != 0

	query := url.Values{}
	if target.BookID != 0 {
		query.Set("filter[book_id]", strconv.FormatInt(target.BookID, 10))
	}
	if target.ChapterID != 0 {
		query.Set("filter[chapter_id]", strconv.FormatInt(target.ChapterID, 10))
	}
	rows, err := scanList(ctx, c, scanSpec[pageJSON]{
		op: "list pages", path: "/api/pages", query: query, filtered: filtered, limit: limit, offset: offset,
		narrow: "book_id or chapter_id", arguments: argNames(pagesList),
		id: func(p pageJSON) int64 { return p.ID },
		keep: func(p pageJSON) bool {
			return !(target.BookID != 0 && p.BookID != target.BookID) &&
				!(target.ChapterID != 0 && p.ChapterID != target.ChapterID)
		},
		row: listRow,
	})
	if err != nil {
		return output.Collection{}, err
	}
	return output.Collection{Columns: fieldNames(pagesList), Rows: rows}, nil
}

// scanSpec describes one paginated list of a fixed endpoint: which rows belong to the caller and how a row
// is shown. query carries the fixed filters; count, offset, and sort are set by scanList.
type scanSpec[T any] struct {
	op, path, narrow string
	query            url.Values
	filtered         bool
	limit, offset    int
	arguments        []string
	id               func(T) int64
	keep             func(T) bool
	row              func(T) output.Row
}

// scanList pages through a list endpoint in records of at most 500. In filtered mode every row is checked
// with keep and foreign rows are dropped, so limit and offset count the remaining rows and the scan is
// bounded by maxScanRequests. An instance that ignores the offset or reports an endless total ends the scan
// as soon as a request brings no new record.
func scanList[T any](ctx context.Context, c *Client, spec scanSpec[T]) ([]output.Row, error) {
	rows := make([]output.Row, 0, 32)
	seen := map[int64]bool{}
	scanned := 0 // rows received in filtered mode, the offset sent to the server
	skipped := 0 // matching rows skipped for the caller's offset in filtered mode
	limit, offset := spec.limit, spec.offset

	for requests := 0; ; requests++ {
		if spec.filtered && requests >= maxScanRequests {
			message := "the listing exceeds the scan limit"
			if spec.narrow != "" {
				message += "; narrow it with " + spec.narrow
			}
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: spec.op, Message: message}
		}
		count := maxCount
		serverOffset := offset + len(rows)
		if spec.filtered {
			serverOffset = scanned
		} else if limit > 0 && limit-len(rows) < count {
			count = limit - len(rows)
		}

		query := url.Values{}
		for key, values := range spec.query {
			query[key] = values
		}
		query.Set("count", strconv.Itoa(count))
		query.Set("offset", strconv.Itoa(serverOffset))
		query.Set("sort", "+id")

		var page struct {
			Data  []T `json:"data"`
			Total int `json:"total"`
		}
		if err := c.get(ctx, spec.op, spec.path, query, &page, spec.arguments, provider.ClassPermission); err != nil {
			return nil, err
		}

		added := 0
		for _, item := range page.Data {
			if seen[spec.id(item)] {
				continue
			}
			seen[spec.id(item)] = true
			added++
			if !spec.keep(item) {
				continue
			}
			if spec.filtered && skipped < offset {
				skipped++
				continue
			}
			if limit > 0 && len(rows) >= limit {
				break
			}
			rows = append(rows, spec.row(item))
		}
		scanned += len(page.Data)

		// No progress means the instance cannot deliver more, whatever its total claims.
		reached := offset + len(rows)
		if spec.filtered {
			reached = scanned
		}
		if added == 0 || (limit > 0 && len(rows) >= limit) || reached >= page.Total {
			break
		}
	}
	return rows, nil
}

// bookRef is the part of a page or chapter read as evidence of the book it belongs to.
type bookRef struct {
	ID     int64 `json:"id"`
	BookID int64 `json:"book_id"`
}

// chapterBook reads a chapter once and returns its book if the connection is bound to it.
func (c *Client) chapterBook(ctx context.Context, id int64) (int64, error) {
	var ref bookRef
	if err := c.get(ctx, "get chapter", "/api/chapters/"+strconv.FormatInt(id, 10), nil, &ref, nil, provider.ClassPermission); err != nil {
		return 0, err
	}
	if !c.scope.allows(ref.BookID) {
		return 0, invalidRequest(outsideChapter)
	}
	return ref.BookID, nil
}

// requirePageBound reads a page once as evidence of its book before a change; an unbound connection reads
// nothing.
func (c *Client) requirePageBound(ctx context.Context, id string) error {
	if !c.scope.bound() {
		return nil
	}
	var ref bookRef
	if err := c.get(ctx, "get page", "/api/pages/"+url.PathEscape(id), nil, &ref, nil, provider.ClassPermission); err != nil {
		return err
	}
	if !c.scope.allows(ref.BookID) {
		return invalidRequest(outsidePage)
	}
	return nil
}

// GetPage returns one page including its untrusted content. On a bound connection this read is also the
// evidence that the page belongs to one of the books.
func (c *Client) GetPage(ctx context.Context, id string) (output.Object, error) {
	var page pageJSON
	if err := c.get(ctx, "get page", "/api/pages/"+url.PathEscape(id), nil, &page, argNames(pagesGet), provider.ClassPermission); err != nil {
		return output.Object{}, err
	}
	if c.scope.bound() && !c.scope.allows(page.BookID) {
		return output.Object{}, invalidRequest(outsidePage)
	}

	return pageObject(page), nil
}

func (c *Client) CreatePage(ctx context.Context, input pageMutation) (output.Object, error) {
	if c.scope.bound() {
		if input.BookID != 0 {
			if err := c.scope.checkBook(input.BookID); err != nil {
				return output.Object{}, err
			}
		}
		if input.ChapterID != 0 {
			if _, err := c.chapterBook(ctx, input.ChapterID); err != nil {
				return output.Object{}, err
			}
		}
	}
	var page pageJSON
	if err := c.mutate(ctx, "create page", http.MethodPost, "/api/pages", input, &page, argNames(pagesCreate)); err != nil {
		return output.Object{}, err
	}
	return pageObject(page), nil
}

func (c *Client) UpdatePage(ctx context.Context, id string, input pageMutation) (output.Object, error) {
	if err := input.validate(); err != nil {
		return output.Object{}, err
	}
	if c.scope.bound() && input.BookID != 0 {
		if err := c.scope.checkBook(input.BookID); err != nil {
			return output.Object{}, err
		}
	}
	// The source page is proven first; a foreign page or target chapter ends the call without a change.
	if err := c.requirePageBound(ctx, id); err != nil {
		return output.Object{}, err
	}
	if input.ChapterID != 0 && c.scope.bound() {
		if _, err := c.chapterBook(ctx, input.ChapterID); err != nil {
			return output.Object{}, err
		}
	}
	var page pageJSON
	if err := c.mutate(ctx, "update page", http.MethodPut, "/api/pages/"+url.PathEscape(id), input, &page, argNames(pagesUpdate)); err != nil {
		return output.Object{}, err
	}
	return pageObject(page), nil
}

func (c *Client) DeletePage(ctx context.Context, id string) error {
	if err := c.requirePageBound(ctx, id); err != nil {
		return err
	}
	return c.mutate(ctx, "delete page", http.MethodDelete, "/api/pages/"+url.PathEscape(id), nil, nil, argNames(pagesDelete))
}

func pageObject(page pageJSON) output.Object {
	return output.Object{Fields: []output.Field{
		{Name: "id", Value: page.ID}, {Name: "name", Value: page.Name}, {Name: "slug", Value: page.Slug},
		{Name: "book_id", Value: page.BookID}, {Name: "chapter_id", Value: page.ChapterID},
		{Name: "created_at", Value: page.CreatedAt}, {Name: "updated_at", Value: page.UpdatedAt},
		{Name: "html", Value: page.HTML}, {Name: "markdown", Value: page.Markdown},
		{Name: "raw_html", Value: page.RawHTML}, {Name: "priority", Value: page.Priority},
		{Name: "draft", Value: page.Draft}, {Name: "template", Value: page.Template},
		{Name: "revision_count", Value: page.RevisionCount}, {Name: "editor", Value: clip(page.Editor, maxResultString)},
		{Name: "tags", Value: reduceTags(page.Tags)},
		{Name: "created_by", Value: reduceUser(page.CreatedBy)}, {Name: "updated_by", Value: reduceUser(page.UpdatedBy)},
		{Name: "owned_by", Value: reduceUser(page.OwnedBy)},
	}}
}

// TestConnection performs the smallest authenticated read and reports the stable outcome class.
func (c *Client) TestConnection(ctx context.Context) provider.Class {
	query := url.Values{}
	query.Set("count", "1")

	var page listJSON
	err := c.get(ctx, "test connection", "/api/pages", query, &page, nil, provider.ClassAuth)
	if err == nil {
		return provider.ClassOK
	}
	var perr *provider.Error
	if errors.As(err, &perr) {
		return perr.Class
	}
	return provider.ClassProviderError
}

// get performs one read request and decodes the response into out.
func (c *Client) get(ctx context.Context, op, path string, query url.Values, out any, arguments []string,
	forbidden provider.Class) error {
	target := c.base.JoinPath(path)
	target.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: "could not build the request"}
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return transportError(op, err, false)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return statusError(op, resp, arguments, forbidden, false)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxReadResponseBytes+1))
	if err != nil || len(data) > maxReadResponseBytes {
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "the response could not be read within the size limit"}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return &provider.Error{
			Class: provider.ClassProviderError, Op: op,
			Message: "the response was not valid JSON",
		}
	}
	return nil
}

// mutate sends one change request, never repeated. A failure that may have reached BookStack says so.
func (c *Client) mutate(ctx context.Context, op, method, path string, input any, out any, arguments []string) error {
	var body io.Reader
	var length int64
	contentType := ""
	if input != nil {
		var encoded bytes.Buffer
		encoder := json.NewEncoder(&encoded)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(input); err != nil || encoded.Len() > maxMutationBodyBytes {
			return providerError(op, "the request exceeds the size limit")
		}
		body, length, contentType = &encoded, int64(encoded.Len()), "application/json"
	}
	return c.send(ctx, c.http, op, method, path, body, length, contentType, out, arguments)
}

// send is the one change request: body may be nil, a non-nil body has the announced length.
func (c *Client) send(ctx context.Context, httpClient *http.Client, op, method, path string, body io.Reader,
	length int64, contentType string, out any, arguments []string) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base.JoinPath(path).String(), body)
	if err != nil {
		return providerError(op, "could not build the request")
	}
	if body != nil {
		req.ContentLength = length
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return transportError(op, err, true)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusError(op, resp, arguments, provider.ClassPermission, true)
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxReadResponseBytes+1))
	if err != nil || len(responseBody) > maxReadResponseBytes {
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "the response could not be read within the size limit" + changeUncertain}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		return providerError(op, "the response was not valid JSON"+changeUncertain)
	}
	return nil
}

func argNames(d capability.Descriptor) []string {
	names := make([]string, len(d.Arguments))
	for i, a := range d.Arguments {
		names[i] = a.Name
	}
	return names
}

func providerError(op, message string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

// transportError classifies a failure that happened before a status code existed. The shared classifier
// owns the rules, so BookStack publishes the same class and the same transport cause as every other
// provider, and the original error text is never copied. For a change, a failure that may have reached the
// server carries the uncertainty hint.
func transportError(op string, err error, change bool) error {
	failure := provider.Transport(op, "the server", err)
	if change && (failure.Class == provider.ClassTimeout || failure.Cause == provider.CauseConnectionReset ||
		failure.Cause == provider.CauseUnknown) {
		failure.Message += changeUncertain
	}
	return failure
}

// statusError maps an HTTP status to a stable class. The provider body is only read for the field names of
// a validation failure; no provider text reaches the message. forbidden is the class of a 403.
func statusError(op string, resp *http.Response, arguments []string, forbidden provider.Class, change bool) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	status := resp.StatusCode
	switch {
	case status == http.StatusUnauthorized:
		return &provider.Error{Class: provider.ClassAuth, Op: op, Message: "BookStack rejected the API token"}
	case status == http.StatusForbidden && forbidden == provider.ClassAuth:
		return &provider.Error{Class: provider.ClassAuth, Op: op, Message: "BookStack refused the API token"}
	case status == http.StatusForbidden:
		return &provider.Error{Class: provider.ClassPermission, Op: op, Message: permissionHint}
	case status == http.StatusNotFound:
		return &provider.Error{Class: provider.ClassNotFound, Op: op, Message: "BookStack does not hold this resource or does not show it to this token"}
	case status == http.StatusTooManyRequests:
		return &provider.Error{Class: provider.ClassRateLimited, Op: op, Message: "BookStack rate-limited the operation"}
	case status == http.StatusUnprocessableEntity:
		message := "BookStack rejected the input (HTTP 422)"
		var parsed struct {
			Error struct {
				Validation map[string]json.RawMessage `json:"validation"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &parsed) == nil {
			var names []string
			for _, name := range arguments {
				if _, ok := parsed.Error.Validation[name]; ok {
					names = append(names, name)
				}
			}
			if len(names) > 0 {
				message += "; invalid: " + strings.Join(names, ", ")
			}
		}
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
	}
	message := fmt.Sprintf("BookStack answered with an unexpected status (HTTP %d)", status)
	if change && status >= 500 {
		message += changeUncertain
	}
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

func listRow(p pageJSON) output.Row {
	return output.Row{
		"id":         p.ID,
		"name":       p.Name,
		"slug":       p.Slug,
		"book_id":    p.BookID,
		"chapter_id": p.ChapterID,
		"created_at": p.CreatedAt,
		"updated_at": p.UpdatedAt,
		"priority":   p.Priority,
		"draft":      p.Draft,
		"template":   p.Template,
		"owned_by":   reduceUser(p.OwnedBy),
	}
}

func fieldNames(c capability.Descriptor) []string {
	out := make([]string, len(c.Fields))
	for i, f := range c.Fields {
		out[i] = f.Name
	}
	return out
}
