package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The gist tools list, read, create, change, and delete gists. A gist belongs to a user account, not to a
// repository or a project, so the tools take no target argument. A connection without targets reaches whatever
// its token reaches. A connection with targets must name at least one user as users/LOGIN and no repository and
// no project; it then reaches only the gists of the users it names: a list of a user needs that user as an
// owner target, a list of the account behind the token and a create need that account among them, and a gist
// id is read first and refused as not found unless its owner is one of them. Descriptions and file contents
// come from other accounts and are untrusted data. A description is cut at a fixed length, a file shows at
// most a fixed number of characters and all files together a fixed total, every cut says so, and a binary file
// is reported by its metadata only.
//
// Verified 2026-09-30 against https://docs.github.com/en/rest/gists/gists: the lists take since, per_page (at
// most 100), and page and answer 200; a gist is read with GET (200, 404), created with POST (201) from
// files, description, and public, changed with PATCH (200) whose files map renames a file with filename,
// replaces it with content, and removes it with null, and deleted with DELETE (204, 404). Classic tokens need
// the gist scope for secret gists and for every change. Per
// https://docs.github.com/en/rest/authentication/permissions-required-for-fine-grained-personal-access-tokens
// a fine-grained token needs the user permission Gists: write to create, update, or delete; GitHub lists no
// permission for the reads. A create is never idempotent; a change sets the named state and leaves the same
// state when repeated; GitHub documents no answer for repeating a delete, so a delete declares its
// idempotency as unknown. Every change is sent once and never repeated.

const (
	gistDescriptionLimit = 500
	gistFileLimit        = 20000
	gistTotalLimit       = 100000
	gistListedFiles      = 20
	gistShownFiles       = 50
	gistWriteFiles       = 20
	gistWriteBytes       = 1 << 20
	gistSensitivity      = "github-gists"
)

const (
	gistIDSchema       = `{"type":"string","minLength":1,"maxLength":64,"pattern":"^[0-9A-Fa-f]{1,64}$"}`
	gistFilenameSchema = `{"type":"string","minLength":1,"maxLength":255,"pattern":"^[^/\\x00-\\x1f\\x7f]+$"}`
	gistContentSchema  = `{"type":"string","minLength":1,"maxLength":262144}`
	gistDescSchema     = `{"type":"string","maxLength":1000}`
	gistLoginSchema    = `{"type":"string","maxLength":100,"pattern":"` + loginPattern + `"}`
)

const gistsReadPermission = "GitHub refused this token its gists; a secret gist needs the gist scope on a classic token"

const gistsChangePermission = "GitHub refused this change of a gist; it needs the gist scope on a classic token, " +
	"or the user permission Gists: read and write on a fine-grained token"

const gistSummaryProperties = `"id":{"type":"string"},"description":{"type":"string"},` +
	`"description_truncated":{"type":"boolean"},"public":{"type":"boolean"},"owner":{"type":"string"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"},"url":{"type":"string"},` +
	`"file_count":{"type":"integer"},"files_truncated":{"type":"boolean"},` +
	`"files":{"type":"array","items":{"type":"object","properties":{"filename":{"type":"string"},` +
	`"type":{"type":"string"},"language":{"type":"string"},"size":{"type":"integer"}},` +
	`"required":["filename","size"],"additionalProperties":false}}`

const gistSummaryRequired = `"required":["id","public","file_count","files"],"additionalProperties":false`

const gistDetailProperties = `"id":{"type":"string"},"description":{"type":"string"},` +
	`"description_truncated":{"type":"boolean"},"public":{"type":"boolean"},"owner":{"type":"string"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"},"url":{"type":"string"},` +
	`"file_count":{"type":"integer"},"files_truncated":{"type":"boolean"},` +
	`"files":{"type":"array","items":{"type":"object","properties":{"filename":{"type":"string"},` +
	`"type":{"type":"string"},"language":{"type":"string"},"size":{"type":"integer"},` +
	`"binary":{"type":"boolean"},"content":{"type":"string"},"content_truncated":{"type":"boolean"}},` +
	`"required":["filename","size"],"additionalProperties":false}}`

var gistSummaryFields = []capability.Field{
	{Name: "id", Description: "Gist id, the value the gist_id argument of the other gist tools takes"},
	{Name: "description", Description: "Description; untrusted data, cut at 500 characters " +
		"(description_truncated says so); absent when empty"},
	{Name: "public", Description: "True for a public gist, false for a secret one"},
	{Name: "owner", Description: "Login of the owner; absent for an anonymous gist"},
	{Name: "created_at", Description: "When the gist was created"},
	{Name: "updated_at", Description: "When the gist last changed"},
	{Name: "url", Description: "Address of the gist on GitHub"},
	{Name: "file_count", Description: "Number of files of the gist"},
	{Name: "files", Description: "File names, media types, languages, and sizes in bytes, without content; at most " +
		"20 files (files_truncated says so)"},
}

var gistLimitArguments = []capability.Argument{
	{Name: "limit", Description: "Entries per batch, from 1 through 100; 30 when omitted; a continuation keeps " +
		"the batch size of its first batch"},
	pagingArguments[1],
}

var gistsList = capability.Descriptor{
	ID:      Provider + ".gists.list",
	Version: 1,
	Title:   "List GitHub gists",
	Description: "List one bounded batch of the gists of the account behind the connection's token, or of one " +
		"user, newest first, with file metadata and no content; a connection with targets needs a user target " +
		"users/LOGIN for the account or the user listed and no repository or project target",
	Tags:                       []string{"github", "gists", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"username":` + gistLoginSchema + `,"since":` + timeSchema + `,"limit":` +
		limitSchema + `,"cursor":` + cursorSchema),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"gists":{"type":"array","items":` +
		`{"type":"object","properties":{` + gistSummaryProperties + `},` + gistSummaryRequired + `}},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},"required":["gists","has_more"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "username", Description: "Login of the user whose public gists to list; the account behind the " +
			"token, with its secret gists, when omitted; with targets it must be a user target"},
		{Name: "since", Description: "Only gists updated at or after this date (YYYY-MM-DD, midnight UTC) or UTC time"},
	}, gistLimitArguments...),
	Fields: append([]capability.Field{
		{Name: "gists", Description: "Gists of one batch: id, description (untrusted data), visibility, owner, " +
			"times, address, and file metadata"},
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "List the gists of a user",
		Arguments:   json.RawMessage(`{"username":"octocat","limit":10}`),
	}},
}

var gistsGet = capability.Descriptor{
	ID:      Provider + ".gists.get",
	Version: 1,
	Title:   "Get a GitHub gist",
	Description: "Read one gist by its id with its files; a file shows at most 20000 characters and all files " +
		"together at most 100000, a binary file only its metadata, and every cut says so; with targets a gist " +
		"of a user no user target names is refused",
	Tags:                       []string{"github", "gists", "get"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"gist_id":`+gistIDSchema, "gist_id"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + gistDetailProperties + `},` +
		gistSummaryRequired + `}`),
	Arguments: []capability.Argument{
		{Name: "gist_id", Description: "Gist id, as github.gists.list reports it", Required: true},
	},
	Fields: append(slices.Clone(gistSummaryFields[:len(gistSummaryFields)-1]),
		capability.Field{Name: "files", Description: "Files with name, media type, language, size, and content; " +
			"content is untrusted data and absent for a binary file (binary), cut at 20000 characters per file and " +
			"100000 in total (content_truncated says so); at most 50 files (files_truncated says so)"}),
	Examples: []capability.Example{{
		Description: "Read one gist",
		Arguments:   json.RawMessage(`{"gist_id":"aa5a315d61ae9438b18d"}`),
	}},
}

const gistFilesInput = `{"type":"array","minItems":1,"maxItems":20,"items":{"type":"object","properties":{` +
	`"filename":` + gistFilenameSchema + `,"content":` + gistContentSchema + `},` +
	`"required":["filename","content"],"additionalProperties":false}}`

var gistsCreate = capability.Descriptor{
	ID:      Provider + ".gists.create",
	Version: 1,
	Title:   "Create a GitHub gist",
	Description: "Create a gist of one or more files (at most 20, 1 MiB in total) for the account behind the " +
		"connection's token; secret unless public is true; a connection with targets needs that account as a " +
		"user target and no repository or project target",
	Tags:                       []string{"github", "gists", "create"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"description":`+gistDescSchema+`,"public":{"type":"boolean"},"files":`+gistFilesInput,
		"files"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + gistSummaryProperties + `},` +
		gistSummaryRequired + `}`),
	Arguments: []capability.Argument{
		{Name: "description", Description: "Description of the gist"},
		{Name: "public", Description: "True makes the gist public; secret when omitted or false"},
		{Name: "files", Description: "Files as filename and content; filenames are unique and hold no slash", Required: true},
	},
	Fields: gistSummaryFields,
	Examples: []capability.Example{{
		Description: "Create a secret gist with one file",
		Arguments:   json.RawMessage(`{"description":"Notes","files":[{"filename":"notes.md","content":"# Notes"}]}`),
	}},
}

const gistUpdateFilesInput = `{"type":"array","minItems":1,"maxItems":20,"items":{"type":"object","properties":{` +
	`"filename":` + gistFilenameSchema + `,"content":` + gistContentSchema + `,"new_filename":` +
	gistFilenameSchema + `},"required":["filename"],"additionalProperties":false}}`

var gistsUpdate = capability.Descriptor{
	ID:      Provider + ".gists.update",
	Version: 1,
	Title:   "Update a GitHub gist",
	Description: "Change the description of a gist, and add, replace, or rename its files, or remove files, with " +
		"one request; the gist is read first when the connection has targets and a gist of a user no user target " +
		"names is refused; at least one change is required",
	Tags:                       []string{"github", "gists", "update"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"gist_id":`+gistIDSchema+`,"description":`+gistDescSchema+`,"files":`+
		gistUpdateFilesInput+`,"remove_files":{"type":"array","minItems":1,"maxItems":20,"items":`+
		gistFilenameSchema+`}`, "gist_id"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + gistSummaryProperties + `},` +
		gistSummaryRequired + `}`),
	Arguments: []capability.Argument{
		{Name: "gist_id", Description: "Gist id, as github.gists.list reports it", Required: true},
		{Name: "description", Description: "New description; an empty text clears it; unchanged when omitted"},
		{Name: "files", Description: "Files to write: filename, and content to add or replace the file, and " +
			"new_filename to rename it; at least one of content and new_filename"},
		{Name: "remove_files", Description: "Names of files to remove from the gist"},
	},
	Fields: gistSummaryFields,
	Examples: []capability.Example{{
		Description: "Replace the content of a file",
		Arguments:   json.RawMessage(`{"gist_id":"aa5a315d61ae9438b18d","files":[{"filename":"notes.md","content":"# New"}]}`),
	}},
}

var gistsDelete = capability.Descriptor{
	ID:      Provider + ".gists.delete",
	Version: 1,
	Title:   "Delete a GitHub gist",
	Description: "Delete one gist permanently; offered only where a connection's tools list names it; the gist " +
		"is read first when the connection has targets and a gist of a user no user target names is refused",
	Tags:                       []string{"github", "gists", "delete"},
	Risk:                       guardedRisk(capability.EffectDelete, capability.IdempotencyUnknown, gistSensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema:                inputSchema(`"gist_id":`+gistIDSchema, "gist_id"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"gist_id":{"type":"string"},` +
		`"deleted":{"type":"boolean"}},"required":["gist_id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "gist_id", Description: "Gist id, as github.gists.list reports it", Required: true},
	},
	Fields: []capability.Field{
		{Name: "gist_id", Description: "The gist that was deleted"},
		{Name: "deleted", Description: "True once GitHub accepted the delete"},
	},
	Examples: []capability.Example{{
		Description: "Delete a gist",
		Arguments:   json.RawMessage(`{"gist_id":"aa5a315d61ae9438b18d"}`),
	}},
}

func gistOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: gistsList, Handler: capability.Handler(invokeGistsList)},
		{Descriptor: gistsGet, Handler: capability.Handler(invokeGistsGet)},
		{Descriptor: gistsCreate, Handler: capability.Handler(invokeGistsCreate)},
		{Descriptor: gistsUpdate, Handler: capability.Handler(invokeGistsUpdate)},
		{Descriptor: gistsDelete, Handler: capability.Handler(invokeGistsDelete)},
	}
}

// GistFile is the metadata of one file of a gist.
type GistFile struct {
	Filename         string `json:"filename"`
	Type             string `json:"type,omitempty"`
	Language         string `json:"language,omitempty"`
	Size             int    `json:"size"`
	Binary           bool   `json:"binary,omitempty"`
	Content          string `json:"content,omitempty"`
	ContentTruncated bool   `json:"content_truncated,omitempty"`
}

// Gist is the compact view of one gist.
type Gist struct {
	ID                   string     `json:"id"`
	Description          string     `json:"description,omitempty"`
	DescriptionTruncated bool       `json:"description_truncated,omitempty"`
	Public               bool       `json:"public"`
	Owner                string     `json:"owner,omitempty"`
	CreatedAt            string     `json:"created_at,omitempty"`
	UpdatedAt            string     `json:"updated_at,omitempty"`
	URL                  string     `json:"url,omitempty"`
	FileCount            int        `json:"file_count"`
	FilesTruncated       bool       `json:"files_truncated,omitempty"`
	Files                []GistFile `json:"files"`
}

// GistList is one batch of gists.
type GistList struct {
	Gists      []Gist `json:"gists"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// GistDeleted is the answer of github.gists.delete.
type GistDeleted struct {
	GistID  string `json:"gist_id"`
	Deleted bool   `json:"deleted"`
}

type gistJSON struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Public      bool   `json:"public"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	HTMLURL     string `json:"html_url"`
	Owner       *struct {
		Login string `json:"login"`
	} `json:"owner"`
	Files map[string]struct {
		Filename  string `json:"filename"`
		Type      string `json:"type"`
		Language  string `json:"language"`
		Size      int    `json:"size"`
		Encoding  string `json:"encoding"`
		Content   string `json:"content"`
		Truncated bool   `json:"truncated"`
	} `json:"files"`
}

func (g gistJSON) owner() string {
	if g.Owner == nil {
		return ""
	}
	return g.Owner.Login
}

func binaryFile(mediaType, encoding, content string) bool {
	if encoding != "" && !strings.EqualFold(encoding, "utf-8") {
		return true
	}
	if !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return true
	}
	for _, prefix := range []string{"image/", "audio/", "video/"} {
		if strings.HasPrefix(mediaType, prefix) && !strings.HasPrefix(mediaType, "image/svg") {
			return true
		}
	}
	switch mediaType {
	case "application/octet-stream", "application/zip", "application/pdf", "application/gzip":
		return true
	}
	return false
}

// view builds the compact view. With content, file contents are included under the per-file and total limits.
func (g gistJSON) view(op string, content bool) (Gist, error) {
	if g.ID == "" {
		return Gist{}, invalidEntry(op, "a gist")
	}
	description, cut := clipText(g.Description, gistDescriptionLimit)
	view := Gist{ID: g.ID, Description: description, DescriptionTruncated: cut, Public: g.Public, Owner: g.owner(),
		CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt, URL: g.HTMLURL, FileCount: len(g.Files), Files: []GistFile{}}
	names := make([]string, 0, len(g.Files))
	for name := range g.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	shown := gistListedFiles
	if content {
		shown = gistShownFiles
	}
	budget := gistTotalLimit
	for _, name := range names {
		if len(view.Files) >= shown {
			view.FilesTruncated = true
			break
		}
		file := g.Files[name]
		entry := GistFile{Filename: name, Type: file.Type, Language: file.Language, Size: file.Size}
		if content {
			if binaryFile(file.Type, file.Encoding, file.Content) {
				entry.Binary = true
			} else {
				limit := min(gistFileLimit, budget)
				entry.Content, entry.ContentTruncated = clipText(file.Content, limit)
				entry.ContentTruncated = entry.ContentTruncated || file.Truncated
				budget -= utf8.RuneCountInString(entry.Content)
			}
		}
		view.Files = append(view.Files, entry)
	}
	return view, nil
}

// requireGistTargets refuses, before a credential is resolved, a gist tool on a connection whose targets are
// not a set of users: it names a repository or a project, or no user at all.
func requireGistTargets(resolved *config.Resolved) (allowlist, error) {
	if resolved == nil {
		return nil, providerError("open", "no connection was selected")
	}
	allowed, err := allowlistOf(resolved)
	if err != nil {
		return nil, providerError("open", err.Error())
	}
	if err := accountWideAllowed(allowed); err != nil {
		return nil, invalidRequest("this connection's targets name a repository or a project, and gists belong " +
			"to a user; use a connection without such targets, or one whose targets name users as users/LOGIN")
	}
	if len(allowed) > 0 && !allowed.namesUser("") {
		return nil, invalidRequest("the targets of this connection name no user; gists belong to a user, so add " +
			"users/LOGIN to its targets or use another connection")
	}
	return allowed, nil
}

// namesUser reports whether the list names the user as an owner target; an empty login asks for any user.
func (a allowlist) namesUser(login string) bool {
	for _, entry := range a {
		if entry.kind == kindOwner && entry.scope == "users" && (login == "" || strings.EqualFold(entry.owner, login)) {
			return true
		}
	}
	return false
}

// admitsGistOwner reports whether the list lets a tool touch a gist of the user: it has no targets, or names
// the user.
func (a allowlist) admitsGistOwner(login string) bool {
	return len(a) == 0 || (login != "" && a.namesUser(login))
}

func checkGistID(id string) error {
	if id == "" || len(id) > 64 || strings.Trim(id, "0123456789abcdefABCDEF") != "" {
		return invalidRequest("gist_id must be the hexadecimal id of a gist")
	}
	return nil
}

type gistListOptions struct {
	Username string `json:"username"`
	Since    string `json:"since"`
	Limit    int    `json:"limit"`
	Cursor   string `json:"cursor"`

	page, perPage int
	binding       []byte
}

func invokeGistsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var o gistListOptions
	if err := readArguments(raw, "list gists", &o); err != nil {
		return nil, err
	}
	allowed, err := requireGistTargets(resolved)
	if err != nil {
		return nil, err
	}
	if o.Username != "" {
		if !validLogin(o.Username) {
			return nil, invalidRequest("username must be a GitHub login")
		}
		if !allowed.admitsGistOwner(o.Username) {
			return nil, invalidRequest("username is outside the targets of this connection; pass a user they name " +
				"as users/LOGIN, or add the user to the connection's targets")
		}
	}
	limit := o.Limit
	if limit == 0 {
		limit = defaultLimit
	}
	if limit < 1 || limit > maxLimit {
		return nil, invalidRequest("limit must be between 1 and " + strconv.Itoa(maxLimit))
	}
	if o.Since, err = notificationTime("since", o.Since); err != nil {
		return nil, err
	}
	o.binding = fingerprint("gists", "list", strings.ToLower(o.Username), o.Since)
	if o.page, o.perPage, err = pageOf(o.binding, o.Cursor, limit); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.listGists(ctx, &o)
}

func (c *Client) listGists(ctx context.Context, o *gistListOptions) (*GistList, error) {
	const op = "list gists"
	if o.Username == "" {
		if err := c.requireOwnAccount(ctx, op); err != nil {
			return nil, err
		}
	}
	path := "/gists"
	if o.Username != "" {
		path = "/users/" + url.PathEscape(o.Username) + "/gists"
	}
	query := url.Values{"per_page": {strconv.Itoa(o.perPage)}, "page": {strconv.Itoa(o.page)}}
	if o.Since != "" {
		query.Set("since", o.Since)
	}
	var raw []gistJSON
	hasNext, err := c.restPage(ctx, op, path, query, &raw)
	if err != nil {
		return nil, actionsFailure(err, gistsReadPermission)
	}
	result := &GistList{Gists: make([]Gist, 0, len(raw))}
	for _, entry := range raw {
		view, err := entry.view(op, false)
		if err != nil {
			return nil, err
		}
		result.Gists = append(result.Gists, view)
	}
	result.HasMore, result.NextCursor = morePage(o.binding, o.page, o.perPage, hasNext)
	return result, nil
}

// requireOwnAccount lets a connection with targets act for the account behind its token only when a user
// target names that account.
func (c *Client) requireOwnAccount(ctx context.Context, op string) error {
	if len(c.allowed) == 0 {
		return nil
	}
	account, err := c.getAccount(ctx)
	if err != nil {
		return err
	}
	if !c.allowed.namesUser(account.Login) {
		return invalidRequest("the account behind this connection's token is not one of the users its targets " +
			"name; add it as users/LOGIN to the targets or use another connection")
	}
	return nil
}

// gist reads one gist and refuses it as not found unless the connection's targets admit its owner.
func (c *Client) gist(ctx context.Context, op, id string) (gistJSON, error) {
	var g gistJSON
	if err := c.rest(ctx, op, "/gists/"+url.PathEscape(id), &g); err != nil {
		return gistJSON{}, actionsFailure(err, gistsReadPermission)
	}
	if g.ID == "" {
		return gistJSON{}, invalidEntry(op, "a gist")
	}
	if !c.allowed.admitsGistOwner(g.owner()) {
		return gistJSON{}, notFound(op, subject{what: "this gist"})
	}
	return g, nil
}

func invokeGistsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var a struct {
		GistID string `json:"gist_id"`
	}
	if err := readArguments(raw, "get gist", &a); err != nil {
		return nil, err
	}
	if err := checkGistID(a.GistID); err != nil {
		return nil, err
	}
	if _, err := requireGistTargets(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "get gist"
	g, err := client.gist(ctx, op, a.GistID)
	if err != nil {
		return nil, err
	}
	return g.view(op, true)
}

type gistWriteFile struct {
	Filename    string  `json:"filename"`
	Content     *string `json:"content"`
	NewFilename string  `json:"new_filename"`
}

func checkGistFilename(name string) error {
	if name == "" || utf8.RuneCountInString(name) > 255 || strings.Contains(name, "/") || strings.ContainsFunc(name,
		func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return invalidRequest("a filename must hold 1 to 255 characters, no slash, and no control character")
	}
	return nil
}

func invokeGistsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var a struct {
		Description string          `json:"description"`
		Public      bool            `json:"public"`
		Files       []gistWriteFile `json:"files"`
	}
	if err := readArguments(raw, "create gist", &a); err != nil {
		return nil, err
	}
	if len(a.Files) < 1 || len(a.Files) > gistWriteFiles {
		return nil, invalidRequest("files must hold 1 to 20 files")
	}
	files, size := map[string]any{}, 0
	for _, f := range a.Files {
		if err := checkGistFilename(f.Filename); err != nil {
			return nil, err
		}
		if _, dup := files[f.Filename]; dup {
			return nil, invalidRequest("a filename appears more than once")
		}
		if f.Content == nil || *f.Content == "" || f.NewFilename != "" {
			return nil, invalidRequest("every file of a new gist needs a filename and a content")
		}
		size += len(*f.Content)
		files[f.Filename] = map[string]string{"content": *f.Content}
	}
	if size > gistWriteBytes {
		return nil, invalidRequest("the files hold more than 1 MiB in total")
	}
	if _, err := requireGistTargets(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "create gist"
	if err := client.requireOwnAccount(ctx, op); err != nil {
		return nil, err
	}
	var created gistJSON
	body := map[string]any{"public": a.Public, "files": files}
	if a.Description != "" {
		body["description"] = a.Description
	}
	if err := client.restChange(ctx, op, http.MethodPost, "/gists", body, &created); err != nil {
		return nil, actionsFailure(err, gistsChangePermission)
	}
	return created.view(op, false)
}

func invokeGistsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var a struct {
		GistID      string          `json:"gist_id"`
		Description *string         `json:"description"`
		Files       []gistWriteFile `json:"files"`
		RemoveFiles []string        `json:"remove_files"`
	}
	if err := readArguments(raw, "update gist", &a); err != nil {
		return nil, err
	}
	if err := checkGistID(a.GistID); err != nil {
		return nil, err
	}
	if a.Description == nil && len(a.Files) == 0 && len(a.RemoveFiles) == 0 {
		return nil, invalidRequest("pass a description, files, or remove_files to change")
	}
	if len(a.Files) > gistWriteFiles || len(a.RemoveFiles) > gistWriteFiles {
		return nil, invalidRequest("files and remove_files hold at most 20 entries each")
	}
	files, size := map[string]any{}, 0
	for _, f := range a.Files {
		if err := checkGistFilename(f.Filename); err != nil {
			return nil, err
		}
		if _, dup := files[f.Filename]; dup {
			return nil, invalidRequest("a filename appears more than once")
		}
		if f.Content == nil && f.NewFilename == "" {
			return nil, invalidRequest("every file needs a content or a new_filename")
		}
		change := map[string]string{}
		if f.Content != nil {
			if *f.Content == "" {
				return nil, invalidRequest("a content must not be empty; use remove_files to remove a file")
			}
			size += len(*f.Content)
			change["content"] = *f.Content
		}
		if f.NewFilename != "" {
			if err := checkGistFilename(f.NewFilename); err != nil {
				return nil, err
			}
			change["filename"] = f.NewFilename
		}
		files[f.Filename] = change
	}
	for _, name := range a.RemoveFiles {
		if err := checkGistFilename(name); err != nil {
			return nil, err
		}
		if _, dup := files[name]; dup {
			return nil, invalidRequest("a filename appears more than once")
		}
		files[name] = nil
	}
	if size > gistWriteBytes {
		return nil, invalidRequest("the files hold more than 1 MiB in total")
	}
	if _, err := requireGistTargets(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "update gist"
	if len(client.allowed) > 0 {
		if _, err := client.gist(ctx, op, a.GistID); err != nil {
			return nil, err
		}
	}
	body := map[string]any{}
	if a.Description != nil {
		body["description"] = *a.Description
	}
	if len(files) > 0 {
		body["files"] = files
	}
	var updated gistJSON
	if err := client.restChange(ctx, op, http.MethodPatch, "/gists/"+url.PathEscape(a.GistID), body,
		&updated); err != nil {
		return nil, actionsFailure(err, gistsChangePermission)
	}
	return updated.view(op, false)
}

func invokeGistsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var a struct {
		GistID string `json:"gist_id"`
	}
	if err := readArguments(raw, "delete gist", &a); err != nil {
		return nil, err
	}
	if err := checkGistID(a.GistID); err != nil {
		return nil, err
	}
	if _, err := requireGistTargets(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "delete gist"
	if len(client.allowed) > 0 {
		if _, err := client.gist(ctx, op, a.GistID); err != nil {
			return nil, err
		}
	}
	if err := client.restChange(ctx, op, http.MethodDelete, "/gists/"+url.PathEscape(a.GistID), struct{}{},
		nil); err != nil {
		return nil, actionsFailure(err, gistsChangePermission)
	}
	return &GistDeleted{GistID: a.GistID, Deleted: true}, nil
}

// gistsSubject names what a path below /gists addresses. It names only what the input schema allows.
func gistsSubject(tail string) subject {
	tail, _, _ = strings.Cut(strings.Trim(tail, "/"), "?")
	if tail == "" {
		return subject{what: "the gists of the account"}
	}
	return subject{what: "this gist"}
}
