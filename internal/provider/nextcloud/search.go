package nextcloud

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Search and favorites of Nextcloud (Nextcloud developer manual, WebDAV search and basic): SEARCH on the
// DAV root with a basicsearch body scoped to the bound folder, REPORT oc:filter-files on the bound folder
// for the favorites, and PROPPATCH of oc:favorite to set or clear one. Qatlas builds every body from
// structured arguments; no request carries caller XML.

const (
	methodSearch    = "SEARCH"
	methodReport    = "REPORT"
	methodProppatch = "PROPPATCH"

	defaultSearchLimit = 50
	maxSearchLimit     = 200
	// maxFavoriteScan bounds the nodes of one favorites answer; the size is bounded by maxBodyBytes.
	maxFavoriteScan = 2000
)

const (
	uncertainFavorite = "; the favorite flag may have been changed, stat the path and list the favorites before repeating"

	messageFavoriteRefused = "Nextcloud refused to change the favorite flag of this path"
)

// entryPropsXML is the property set of a normalised entry, as the children of a d:prop element.
const entryPropsXML = `<d:displayname/><d:resourcetype/><d:getcontenttype/><d:getcontentlength/>` +
	`<d:getlastmodified/><d:getetag/><oc:fileid/><oc:size/><oc:permissions/>`

const xmlHeader = `<?xml version="1.0" encoding="UTF-8"?>`

const favoritesBody = xmlHeader +
	`<oc:filter-files xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><oc:filter-rules>` +
	`<oc:favorite>1</oc:favorite></oc:filter-rules><d:prop>` + entryPropsXML + `</d:prop></oc:filter-files>`

const searchResultSchema = `{"type":"object","properties":{"entries":{"type":"array","items":` + entrySchema +
	`},"count":{"type":"integer"},"truncated":{"type":"boolean"}},"required":["entries","count"],"additionalProperties":false}`

var entryListFields = append([]capability.Field{
	{Name: "entries", Description: "Matching files and folders, untrusted data, sorted by path"},
	{Name: "count", Description: "Number of reported entries"},
	{Name: "truncated", Description: "True when more matching entries may exist than reported"},
}, entryFields...)

var filesSearch = capability.Descriptor{
	ID: Provider + ".files.search", Version: 1,
	Title: "Search Nextcloud files",
	Description: "Search the files and folders below the fixed root folder of a connection with structured filters, " +
		"at least one of which is required; the filters combine with AND and the root itself is never a hit. " +
		"The server picks which matches a limit keeps; the result is sorted by path",
	Tags: []string{"nextcloud", "files", "webdav", "search"}, Provider: Provider,
	Risk: nextcloudReadRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"name_contains":{"type":"string","minLength":1,"maxLength":255},` +
		`"content_type_prefix":{"type":"string","minLength":1,"maxLength":127,"pattern":"^[A-Za-z0-9!#$&^_.+/*-]+$"},` +
		`"modified_after":{"type":"string","minLength":20,"maxLength":40,"x-form":"RFC 3339 time"},` +
		`"modified_before":{"type":"string","minLength":20,"maxLength":40,"x-form":"RFC 3339 time"},` +
		`"size_min":{"type":"integer","minimum":0},"size_max":{"type":"integer","minimum":0},` +
		`"type":{"type":"string","enum":["file","folder"]},"favorite":{"type":"boolean"},` +
		`"limit":{"type":"integer","minimum":1,"maximum":200}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(searchResultSchema),
	Arguments: []capability.Argument{
		{Name: "name_contains", Description: "Text the display name contains, matched case-insensitively and literally"},
		{Name: "content_type_prefix", Description: "Start of the MIME type, for example image/ or application/pdf"},
		{Name: "modified_after", Description: "Only entries changed after this RFC 3339 time, to the second"},
		{Name: "modified_before", Description: "Only entries changed before this RFC 3339 time, to the second"},
		{Name: "size_min", Description: "Smallest size in bytes, inclusive; a folder counts with the size of its subtree"},
		{Name: "size_max", Description: "Largest size in bytes, inclusive"},
		{Name: "type", Description: "Either file or folder"},
		{Name: "favorite", Description: "True for favorites only, false for entries that are no favorite"},
		{Name: "limit", Description: "Largest number of entries to report, 1 to 200, default 50"},
	},
	Fields:   entryListFields,
	Examples: []capability.Example{{Description: "Find PDFs with report in the name", Arguments: json.RawMessage(`{"name_contains":"report","content_type_prefix":"application/pdf"}`)}},
}

var favoritesList = capability.Descriptor{
	ID: Provider + ".favorites.list", Version: 1,
	Title: "List Nextcloud favorites",
	Description: "List the files and folders below the fixed root folder of a connection that the identity marked as " +
		"favorite, at most 500 per call",
	Tags: []string{"nextcloud", "favorites", "webdav", "list"}, Provider: Provider,
	Risk:         nextcloudReadRisk,
	InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(searchResultSchema),
	Fields:       entryListFields,
	Examples:     []capability.Example{{Description: "List the favorites of this connection", Arguments: json.RawMessage(`{}`)}},
}

var filesFavorite = capability.Descriptor{
	ID: Provider + ".files.favorite", Version: 1,
	Title: "Mark or unmark a Nextcloud favorite",
	Description: "Set or clear the favorite flag of exactly one confirmed file or folder below the fixed root folder of " +
		"a connection; the root itself is refused and the content is never changed",
	Tags: []string{"nextcloud", "files", "webdav", "favorite"}, Provider: Provider,
	Risk: capability.Risk{Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
	InputSchema: json.RawMessage(`{"type":"object","properties":{"path":` + pathSchema +
		`,"favorite":{"type":"boolean"}},"required":["path","favorite"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"favorite":{"type":"boolean"},` +
		`"path":{"type":"string"}},"required":["favorite","path"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "path", Required: true, Description: "File or folder relative to the fixed root folder of this connection; never the root itself"},
		{Name: "favorite", Required: true, Description: "True to mark the entry as favorite, false to remove the mark"},
	},
	Fields: []capability.Field{
		{Name: "favorite", Description: "The flag Nextcloud applied"},
		{Name: "path", Description: "Path of the entry"},
	},
	Examples: []capability.Example{{Description: "Mark a file as favorite", Arguments: json.RawMessage(`{"path":"2026/q1.pdf","favorite":true}`)}},
}

type searchArguments struct {
	NameContains      string `json:"name_contains"`
	ContentTypePrefix string `json:"content_type_prefix"`
	ModifiedAfter     string `json:"modified_after"`
	ModifiedBefore    string `json:"modified_before"`
	SizeMin           *int64 `json:"size_min"`
	SizeMax           *int64 `json:"size_max"`
	Type              string `json:"type"`
	Favorite          *bool  `json:"favorite"`
	Limit             int    `json:"limit"`
}

type entryListResult struct {
	Entries   []Entry `json:"entries"`
	Count     int     `json:"count"`
	Truncated bool    `json:"truncated,omitempty"`
}

// xmlText escapes one value for element content. Every caller-supplied value passes through here.
func xmlText(value string) string {
	var out strings.Builder
	_ = xml.EscapeText(&out, []byte(value))
	return out.String()
}

// likeEscape masks the characters that are special to a SQL LIKE pattern. Nextcloud evaluates d:like as a
// database LIKE whose escape character is the backslash, so the backslash itself is masked first.
func likeEscape(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func comparison(operator, property, literal string) string {
	return "<d:" + operator + "><d:prop><" + property + "/></d:prop><d:literal>" + xmlText(literal) + "</d:literal></d:" + operator + ">"
}

// searchRequest is a checked search: the d:where conditions and the limit.
type searchRequest struct {
	conditions []string
	limit      int
}

// checkSearch validates the structured filters locally and turns them into where conditions.
func checkSearch(op string, input searchArguments) (*searchRequest, error) {
	req := &searchRequest{limit: input.Limit}
	if req.limit == 0 {
		req.limit = defaultSearchLimit
	}
	if req.limit < 1 || req.limit > maxSearchLimit {
		return nil, providerError(op, "limit must be between 1 and 200")
	}
	if input.NameContains != "" {
		if hasControl(input.NameContains) || len(input.NameContains) > 255 {
			return nil, providerError(op, "name_contains must be at most 255 bytes without control characters")
		}
		req.conditions = append(req.conditions, comparison("like", "d:displayname", "%"+likeEscape(input.NameContains)+"%"))
	}
	if input.ContentTypePrefix != "" {
		if hasControl(input.ContentTypePrefix) || len(input.ContentTypePrefix) > 127 || strings.ContainsAny(input.ContentTypePrefix, " \t") {
			return nil, providerError(op, "content_type_prefix must be the start of a MIME type")
		}
		req.conditions = append(req.conditions, comparison("like", "d:getcontenttype", likeEscape(input.ContentTypePrefix)+"%"))
	}
	var after, before time.Time
	for _, f := range []struct {
		raw      string
		operator string
		into     *time.Time
	}{{input.ModifiedAfter, "gt", &after}, {input.ModifiedBefore, "lt", &before}} {
		if f.raw == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, f.raw)
		if err != nil {
			return nil, providerError(op, "modified_after and modified_before must be RFC 3339 times")
		}
		*f.into = parsed
		req.conditions = append(req.conditions, comparison(f.operator, "d:getlastmodified", parsed.UTC().Format(http.TimeFormat)))
	}
	if !after.IsZero() && !before.IsZero() && !after.Before(before) {
		return nil, providerError(op, "modified_after must be earlier than modified_before")
	}
	if input.SizeMin != nil {
		if *input.SizeMin < 0 {
			return nil, providerError(op, "size_min must not be negative")
		}
		req.conditions = append(req.conditions, comparison("gte", "oc:size", strconv.FormatInt(*input.SizeMin, 10)))
	}
	if input.SizeMax != nil {
		if *input.SizeMax < 0 || (input.SizeMin != nil && *input.SizeMax < *input.SizeMin) {
			return nil, providerError(op, "size_max must not be negative or below size_min")
		}
		req.conditions = append(req.conditions, comparison("lte", "oc:size", strconv.FormatInt(*input.SizeMax, 10)))
	}
	switch input.Type {
	case "":
	case typeFolder:
		req.conditions = append(req.conditions, "<d:is-collection/>")
	case typeFile:
		req.conditions = append(req.conditions, "<d:not><d:is-collection/></d:not>")
	default:
		return nil, providerError(op, "type must be file or folder")
	}
	if input.Favorite != nil {
		value := "0"
		if *input.Favorite {
			value = "1"
		}
		req.conditions = append(req.conditions, comparison("eq", "oc:favorite", value))
	}
	if len(req.conditions) == 0 {
		return nil, providerError(op, "at least one filter is required")
	}
	return req, nil
}

// searchBody renders the basicsearch request scoped to the connection root.
func (c *Client) searchBody(req *searchRequest) string {
	where := req.conditions[0]
	if len(req.conditions) > 1 {
		where = "<d:and>" + strings.Join(req.conditions, "") + "</d:and>"
	}
	scope := escapePath(append([]string{"files", c.user}, c.root...))
	return xmlHeader + `<d:searchrequest xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:basicsearch>` +
		`<d:select><d:prop>` + entryPropsXML + `</d:prop></d:select>` +
		`<d:from><d:scope><d:href>` + xmlText(scope) + `</d:href><d:depth>infinity</d:depth></d:scope></d:from>` +
		`<d:where>` + where + `</d:where><d:orderby/>` +
		`<d:limit><d:nresults>` + strconv.Itoa(req.limit) + `</d:nresults></d:limit>` +
		`</d:basicsearch></d:searchrequest>`
}

// davRootURL is the absolute URL of the DAV root, where SEARCH is addressed.
func (c *Client) davRootURL() string {
	return c.origin + escapePath(append(append([]string{}, c.install...), "remote.php", "dav")) + "/"
}

// multistatusAt sends one fixed-method request with a fixed body and parses the 207 answer. A non-empty
// uncertain hint marks a request that changes data: an answer that cannot be read then carries the hint.
func (c *Client) multistatusAt(ctx context.Context, op, method, target, body, contentType, uncertain string, limit int, allowEmpty bool) ([]resource, error) {
	extra := http.Header{"Content-Type": {contentType}, "Accept": {"application/xml"}}
	response, err := c.webdavTo(ctx, op, method, target, strings.NewReader(body), "", "", uncertain, extra)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	resources, err := readMultiStatus(op, response, limit, allowEmpty)
	if err != nil && uncertain != "" {
		return nil, withUncertainty(err, uncertain)
	}
	return resources, err
}

func readMultiStatus(op string, response *http.Response, limit int, allowEmpty bool) ([]resource, error) {
	if response.StatusCode != http.StatusMultiStatus {
		return nil, invalidResponse(op, "Nextcloud did not answer with a multi-status document")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil || len(raw) > maxBodyBytes {
		return nil, invalidResponse(op, "the Nextcloud response could not be read within the size limit")
	}
	return parseMultiStatusBounded(op, raw, limit, allowEmpty)
}

// entriesBelowRoot keeps the answered nodes that lie below the connection root. A node outside it, the
// root itself, and a node the server refused are dropped, not reported.
func (c *Client) entriesBelowRoot(op string, resources []resource, limit int) *entryListResult {
	result := &entryListResult{Entries: []Entry{}}
	for i := range resources {
		relative, err := c.relativeOf(op, resources[i].href)
		if err != nil || len(relative) == 0 || resources[i].failure(op) != nil {
			continue
		}
		if len(result.Entries) >= limit {
			result.Truncated = true
			break
		}
		result.Entries = append(result.Entries, *c.entryOf(relative, &resources[i]))
	}
	sort.Slice(result.Entries, func(i, j int) bool { return result.Entries[i].Path < result.Entries[j].Path })
	result.Count = len(result.Entries)
	return result
}

// SearchFiles sends one SEARCH.
func (c *Client) SearchFiles(ctx context.Context, op string, req *searchRequest) (*entryListResult, error) {
	resources, err := c.multistatusAt(ctx, op, methodSearch, c.davRootURL(), c.searchBody(req), "text/xml; charset=utf-8", "", maxSearchLimit, true)
	if err != nil {
		return nil, err
	}
	result := c.entriesBelowRoot(op, resources, req.limit)
	result.Truncated = result.Truncated || len(resources) >= req.limit
	return result, nil
}

// ListFavorites sends one REPORT on the connection root.
func (c *Client) ListFavorites(ctx context.Context) (*entryListResult, error) {
	const op = "list favorites"
	resources, err := c.multistatusAt(ctx, op, methodReport, c.requestURL(nil), favoritesBody, "application/xml; charset=utf-8", "", maxFavoriteScan, true)
	if err != nil {
		return nil, err
	}
	return c.entriesBelowRoot(op, resources, maxEntries), nil
}

func invokeFilesSearch(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "search files"
	var input searchArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	req, err := checkSearch(op, input)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.SearchFiles(ctx, op, req)
}

func invokeFavoritesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListFavorites(ctx)
}

type favoriteArguments struct {
	Path     string `json:"path"`
	Favorite *bool  `json:"favorite"`
}

func favoriteBody(on bool) string {
	value := "0"
	if on {
		value = "1"
	}
	return xmlHeader + `<d:propertyupdate xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:set><d:prop>` +
		`<oc:favorite>` + value + `</oc:favorite></d:prop></d:set></d:propertyupdate>`
}

// invokeFilesFavorite sends exactly one PROPPATCH. It is never repeated: after an unclear outcome the flag
// may have been changed.
func invokeFilesFavorite(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set favorite"
	var input favoriteArguments
	if err := json.Unmarshal(raw, &input); err != nil || input.Favorite == nil {
		return nil, providerError(op, "path and favorite are required")
	}
	rel, err := splitRelative(input.Path)
	if err != nil || len(rel) == 0 {
		return nil, providerError(op, "a path below the connection root is required")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	resources, err := client.multistatusAt(ctx, op, methodProppatch, client.requestURL(rel), favoriteBody(*input.Favorite),
		"application/xml; charset=utf-8", uncertainFavorite, 1, false)
	if err != nil {
		return nil, err
	}
	res := resources[0]
	if relative, err := client.relativeOf(op, res.href); err != nil || !equalSegments(relative, rel) {
		return nil, withUncertainty(invalidResponse(op, messageForeignEntry), uncertainFavorite)
	}
	if !res.read {
		if code, ok := statusCodeOf(res.status); ok && (code < 200 || code >= 300) {
			return nil, statusError(op, code)
		}
		return nil, providerError(op, messageFavoriteRefused)
	}
	return map[string]any{"favorite": *input.Favorite, "path": input.Path}, nil
}
