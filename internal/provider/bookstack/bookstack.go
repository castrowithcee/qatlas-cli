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
	"time"

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

const maxPageContentBytes = 1 << 20

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
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},"book_id":{"type":"integer"},"chapter_id":{"type":"integer"},"created_at":{"type":"string"},"updated_at":{"type":"string"}},"required":["id","name","slug","book_id","chapter_id","created_at","updated_at"]}}`),
		Arguments: []capability.Argument{
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
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},"book_id":{"type":"integer"},"chapter_id":{"type":"integer"},"created_at":{"type":"string"},"updated_at":{"type":"string"},"html":{"type":"string"},"markdown":{"type":"string"}},"required":["id","name","slug","book_id","chapter_id","created_at","updated_at","html","markdown"]}`),
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
		},
		Examples: []capability.Example{{
			Description: "Read page 42",
			Arguments:   json.RawMessage(`{"id":42}`),
		}},
	}

	pagesCreate = capability.Descriptor{
		ID: Provider + ".pages.create", Version: 1, Title: "Create a BookStack page",
		Description: "Create one Markdown page in a specified book or chapter",
		Tags:        []string{"knowledge", "pages", "bookstack", "create"}, Provider: Provider,
		RequiresExplicitConnection: true,
		Risk:                       capability.Risk{Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent, Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
		InputSchema:                json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":255},"book_id":{"type":"integer","minimum":1},"chapter_id":{"type":"integer","minimum":1},"markdown":{"type":"string","minLength":1,"maxLength":1048576}},"required":["name","markdown"],"additionalProperties":false}`),
		OutputSchema:               pagesGet.OutputSchema,
		Arguments:                  []capability.Argument{{Name: "name", Description: "Page title", Required: true}, {Name: "book_id", Description: "Containing book; required when chapter_id is omitted"}, {Name: "chapter_id", Description: "Containing chapter; mutually exclusive with book_id"}, {Name: "markdown", Description: "Markdown page content", Required: true}},
	}

	pagesUpdate = capability.Descriptor{
		ID: Provider + ".pages.update", Version: 1, Title: "Update a BookStack page",
		Description: "Replace the title and/or Markdown of one page by identifier",
		Tags:        []string{"knowledge", "pages", "bookstack", "update"}, Provider: Provider,
		RequiresExplicitConnection: true,
		Risk:                       capability.Risk{Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent, Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
		InputSchema:                json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1},"name":{"type":"string","minLength":1,"maxLength":255},"markdown":{"type":"string","minLength":1,"maxLength":1048576}},"required":["id"],"additionalProperties":false}`),
		OutputSchema:               pagesGet.OutputSchema,
		Arguments:                  []capability.Argument{{Name: "id", Description: "Page identifier", Required: true}, {Name: "name", Description: "New page title"}, {Name: "markdown", Description: "New Markdown content"}},
	}

	pagesDelete = capability.Descriptor{
		ID: Provider + ".pages.delete", Version: 1, Title: "Delete a BookStack page",
		Description: "Permanently delete one page by identifier",
		Tags:        []string{"knowledge", "pages", "bookstack", "delete"}, Provider: Provider,
		RequiresExplicitConnection: true,
		Risk:                       capability.Risk{Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent, Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
		InputSchema:                json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`),
		OutputSchema:               json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},"required":["deleted"],"additionalProperties":false}`),
		Arguments:                  []capability.Argument{{Name: "id", Description: "Page identifier", Required: true}},
	}
)

// Register records this provider's operations and their existing command handlers.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "BookStack", DefaultPermissions: []config.Permission{config.PermissionRead},
		Description: "Self-hosted documentation platform for team knowledge",
		SecretRoles: []config.SecretRole{
			{Name: roleTokenID, Description: "BookStack token ID: the value labeled Token ID when you create an API token; it is not a name you choose"},
			{Name: roleTokenSecret, Description: "BookStack token secret: the value labeled Token Secret when you create the same API token"},
		},
		Target: config.TargetMetadata{Label: "target", Description: "optional provider-specific scope inside the service"},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read pages", Recommended: true,
			Description: "lists and reads pages; changes nothing in BookStack",
			Tools:       []string{pagesList.ID, pagesGet.ID},
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
		capability.Operation{Descriptor: pagesList, Handler: capability.Handler(invokePagesList)},
		capability.Operation{Descriptor: pagesGet, Handler: capability.Handler(invokePagesGet)},
		capability.Operation{Descriptor: pagesCreate, Handler: capability.Handler(invokePagesCreate)},
		capability.Operation{Descriptor: pagesUpdate, Handler: capability.Handler(invokePagesUpdate)},
		capability.Operation{Descriptor: pagesDelete, Handler: capability.Handler(invokePagesDelete)},
	)
}

func invokePagesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListPages(ctx, arguments.Limit, arguments.Offset)
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
	Name      string `json:"name,omitempty"`
	BookID    int64  `json:"book_id,omitempty"`
	ChapterID int64  `json:"chapter_id,omitempty"`
	Markdown  string `json:"markdown,omitempty"`
}

func invokePagesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input pageMutation
	if json.Unmarshal(raw, &input) != nil || (input.BookID == 0) == (input.ChapterID == 0) {
		return nil, providerError("create page", "exactly one of book_id or chapter_id is required")
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
		ID       int64   `json:"id"`
		Name     *string `json:"name"`
		Markdown *string `json:"markdown"`
	}
	if json.Unmarshal(raw, &input) != nil || (input.Name == nil && input.Markdown == nil) {
		return nil, providerError("update page", "name or markdown is required")
	}
	change := pageMutation{}
	if input.Name != nil {
		change.Name = *input.Name
	}
	if input.Markdown != nil {
		change.Markdown = *input.Markdown
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
	base *url.URL
	auth string
	http *http.Client
}

// Open builds a client for a resolved connection. The secrets come from the resolver, which owns the
// cascade and the redaction; this provider only asks for the two roles it needs and never returns them.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	base, err := url.Parse(resolved.BaseURL)
	if err != nil {
		return nil, &provider.Error{
			Class: provider.ClassProviderError, Op: "open",
			Message: fmt.Sprintf("connection %q has an unusable base URL", resolved.Name),
		}
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
		base: base,
		auth: "Token " + tokenID + ":" + tokenSecret,
		http: &http.Client{
			Timeout: defaultTimeout,
			// A redirect off the configured origin would carry the credential to a host the user never
			// configured, so it is refused rather than followed.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if sameOrigin(via[0].URL, req.URL) {
					return nil
				}
				return &redirectRefusedError{From: via[0].URL.Host}
			},
		},
	}, nil
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

// redirectRefusedError reports a redirect that would have left the configured origin. The credential is
// never sent to a host the user did not configure.
type redirectRefusedError struct{ From string }

func (e *redirectRefusedError) Error() string {
	return fmt.Sprintf("refused to follow a redirect from %s to a different origin", e.From)
}

func sameOrigin(a, b *url.URL) bool {
	return a.Scheme == b.Scheme && a.Host == b.Host
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

// ListPages returns pages, honouring limit and offset. A limit of zero or less returns every page the
// instance reports, fetched in pages of at most 500 records.
func (c *Client) ListPages(ctx context.Context, limit, offset int) (output.Collection, error) {
	columns := fieldNames(pagesList)
	rows := make([]output.Row, 0, 32)
	// An instance that ignores the offset would otherwise hand back the same records until the reported
	// total is reached, which looks like a complete list but is not one.
	seen := map[int64]bool{}

	for {
		count := maxCount
		if limit > 0 && limit-len(rows) < count {
			count = limit - len(rows)
		}

		query := url.Values{}
		query.Set("count", strconv.Itoa(count))
		query.Set("offset", strconv.Itoa(offset+len(rows)))
		query.Set("sort", "+id")

		var page listJSON
		if err := c.get(ctx, "list pages", "/api/pages", query, &page); err != nil {
			return output.Collection{}, err
		}

		added := 0
		for _, p := range page.Data {
			if seen[p.ID] {
				continue
			}
			seen[p.ID] = true
			rows = append(rows, listRow(p))
			added++
		}

		// No progress means the instance cannot deliver more, whatever its total claims.
		done := added == 0 ||
			(limit > 0 && len(rows) >= limit) ||
			offset+len(rows) >= page.Total
		if done {
			break
		}
	}

	return output.Collection{Columns: columns, Rows: rows}, nil
}

// GetPage returns one page including its untrusted content.
func (c *Client) GetPage(ctx context.Context, id string) (output.Object, error) {
	var page pageJSON
	if err := c.get(ctx, "get page", "/api/pages/"+url.PathEscape(id), nil, &page); err != nil {
		return output.Object{}, err
	}

	return output.Object{Fields: []output.Field{
		{Name: "id", Value: page.ID},
		{Name: "name", Value: page.Name},
		{Name: "slug", Value: page.Slug},
		{Name: "book_id", Value: page.BookID},
		{Name: "chapter_id", Value: page.ChapterID},
		{Name: "created_at", Value: page.CreatedAt},
		{Name: "updated_at", Value: page.UpdatedAt},
		{Name: "html", Value: page.HTML},
		{Name: "markdown", Value: page.Markdown},
	}}, nil
}

func (c *Client) CreatePage(ctx context.Context, input pageMutation) (output.Object, error) {
	var page pageJSON
	if err := c.mutate(ctx, "create page", http.MethodPost, "/api/pages", input, &page); err != nil {
		return output.Object{}, err
	}
	return pageObject(page), nil
}

func (c *Client) UpdatePage(ctx context.Context, id string, input pageMutation) (output.Object, error) {
	var page pageJSON
	if err := c.mutate(ctx, "update page", http.MethodPut, "/api/pages/"+url.PathEscape(id), input, &page); err != nil {
		return output.Object{}, err
	}
	return pageObject(page), nil
}

func (c *Client) DeletePage(ctx context.Context, id string) error {
	return c.mutate(ctx, "delete page", http.MethodDelete, "/api/pages/"+url.PathEscape(id), nil, nil)
}

func pageObject(page pageJSON) output.Object {
	return output.Object{Fields: []output.Field{{Name: "id", Value: page.ID}, {Name: "name", Value: page.Name}, {Name: "slug", Value: page.Slug}, {Name: "book_id", Value: page.BookID}, {Name: "chapter_id", Value: page.ChapterID}, {Name: "created_at", Value: page.CreatedAt}, {Name: "updated_at", Value: page.UpdatedAt}, {Name: "html", Value: page.HTML}, {Name: "markdown", Value: page.Markdown}}}
}

// TestConnection performs the smallest authenticated read and reports the stable outcome class.
func (c *Client) TestConnection(ctx context.Context) provider.Class {
	query := url.Values{}
	query.Set("count", "1")

	var page listJSON
	err := c.get(ctx, "test connection", "/api/pages", query, &page)
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
func (c *Client) get(ctx context.Context, op, path string, query url.Values, out any) error {
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
		return transportError(op, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return statusError(op, resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return &provider.Error{
			Class: provider.ClassProviderError, Op: op,
			Message: "the response was not valid JSON",
		}
	}
	return nil
}

func (c *Client) mutate(ctx context.Context, op, method, path string, input any, out any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil || len(encoded) > maxPageContentBytes+4096 {
			return providerError(op, "the request exceeds the size limit")
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.JoinPath(path).String(), body)
	if err != nil {
		return providerError(op, "could not build the request")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return transportError(op, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return statusError(op, resp)
	}
	if out == nil {
		read, err := io.Copy(io.Discard, io.LimitReader(resp.Body, maxPageContentBytes+1))
		if err != nil || read > maxPageContentBytes {
			return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "the response exceeded the size limit"}
		}
		return nil
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxPageContentBytes+1))
	if err != nil || len(responseBody) > maxPageContentBytes {
		return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "the response exceeded the size limit"}
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		return providerError(op, "the response was not valid JSON")
	}
	return nil
}

func providerError(op, message string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

// transportError classifies a failure that happened before a status code existed. The shared classifier
// owns the rules, so BookStack publishes the same class and the same transport cause as every other
// provider, and the original error text is never copied.
func transportError(op string, err error) error {
	// A refused redirect is a policy decision, not an unreachable server.
	var refused *redirectRefusedError
	if errors.As(err, &refused) {
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: refused.Error()}
	}
	return provider.Transport(op, "the server", err)
}

// statusError maps an HTTP status to a stable class. The provider message is passed through because
// BookStack reports the reason there; secrets are removed centrally before anything is shown.
func statusError(op string, resp *http.Response) error {
	message := ""
	if body, err := io.ReadAll(io.LimitReader(resp.Body, 4096)); err == nil {
		var parsed errorJSON
		if json.Unmarshal(body, &parsed) == nil {
			message = parsed.Error.Message
		}
	}
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}

	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden:
		return &provider.Error{Class: provider.ClassAuth, Op: op, Message: message}
	case resp.StatusCode == http.StatusTooManyRequests:
		return &provider.Error{Class: provider.ClassRateLimited, Op: op, Message: message}
	default:
		return &provider.Error{
			Class: provider.ClassProviderError, Op: op,
			Message: fmt.Sprintf("%s (HTTP %d)", message, resp.StatusCode),
		}
	}
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
	}
}

func fieldNames(c capability.Descriptor) []string {
	out := make([]string, len(c.Fields))
	for i, f := range c.Fields {
		out[i] = f.Name
	}
	return out
}
