package bookstack

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxQueryChars   = 1000
	maxSearchCount  = 100
	defaultSearchN  = 20
	maxPreviewChars = 2000
	maxResultTags   = 50
	maxResultString = 1000
	// maxSearchScans bounds the search requests of one call that looks for bound pages among all hits.
	maxSearchScans = 20
)

var contentSearch = capability.Descriptor{
	ID: Provider + ".content.search", Version: 1, Title: "Search BookStack content",
	Description: "Full-text search over shelves, books, chapters, and pages, across the instance, in one book, or " +
		"in one chapter, page by page. The query may use the BookStack search syntax. Previews are untrusted HTML.",
	Tags:     []string{"knowledge", "search", "bookstack"},
	Risk:     bookstackReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","minLength":1,"maxLength":1000},` +
		`"book_id":{"type":"integer","minimum":1,"x-form":"BOOK_ID"},"chapter_id":{"type":"integer","minimum":1},` +
		`"page":{"type":"integer","minimum":1},"count":{"type":"integer","minimum":1,"maximum":100}},` +
		`"required":["query"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"results":{"type":"array","items":{"type":"object","properties":{` +
		`"type":{"type":"string"},"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},` +
		`"book_id":{"type":"integer"},"chapter_id":{"type":"integer"},"url":{"type":"string"},` +
		`"tags":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"value":{"type":"string"}}}},` +
		`"preview_name":{"type":"string"},"preview_content":{"type":"string"}}}},"total":{"type":"integer"}},"required":["results","total"]}`),
	Arguments: []capability.Argument{
		{Name: "query", Description: "Search text, 1 to 1000 characters; the BookStack search syntax is allowed", Required: true},
		{Name: "book_id", Description: "Search only in this book; mutually exclusive with chapter_id; required for a connection bound to several books unless chapter_id is given", Target: "book"},
		{Name: "chapter_id", Description: "Search only in this chapter; mutually exclusive with book_id"},
		{Name: "page", Description: "Page number from 1; 1 when omitted"},
		{Name: "count", Description: "Results per page, 1 to 100; 20 when omitted"},
	},
	Fields: []capability.Field{
		{Name: "results", Description: "Hits with type, id, name, slug, book_id, chapter_id, url, tags, preview_name, preview_content"},
		{Name: "total", Description: "Number of hits as BookStack estimates it"},
	},
	Examples: []capability.Example{{Description: "Search pages about backups in one book", Arguments: json.RawMessage(`{"query":"backup {type:page}","book_id":7}`)}},
}

type searchHit struct {
	Type      string `json:"type"`
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	BookID    int64  `json:"book_id"`
	ChapterID int64  `json:"chapter_id"`
	URL       string `json:"url"`
	Tags      []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"tags"`
	PreviewHTML struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	} `json:"preview_html"`
}

type searchJSON struct {
	Data  []searchHit `json:"data"`
	Total int         `json:"total"`
}

type searchInput struct {
	Query     string `json:"query"`
	BookID    *int64 `json:"book_id"`
	ChapterID *int64 `json:"chapter_id"`
	Page      *int   `json:"page"`
	Count     *int   `json:"count"`
}

func invokeContentSearch(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in searchInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if n := utf8.RuneCountInString(in.Query); n < 1 || n > maxQueryChars {
		return nil, invalidRequest("query must be 1 to 1000 characters")
	}
	page, count := 1, defaultSearchN
	if in.Page != nil {
		page = *in.Page
	}
	if in.Count != nil {
		count = *in.Count
	}
	if page < 1 {
		return nil, invalidRequest("page must be at least 1")
	}
	if count < 1 || count > maxSearchCount {
		return nil, invalidRequest("count must be 1 to 100")
	}
	var target listTarget
	if in.BookID != nil {
		target.BookID = *in.BookID
		if target.BookID <= 0 {
			return nil, invalidRequest("book_id must be a positive integer")
		}
	}
	if in.ChapterID != nil {
		target.ChapterID = *in.ChapterID
		if target.ChapterID <= 0 {
			return nil, invalidRequest("chapter_id must be a positive integer")
		}
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	// Without a book or chapter, a bound connection searches its only book, its only chapter, or its pages;
	// any other binding needs an argument.
	if target, err = bound.listIn(target); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.Search(ctx, in.Query, target, page, count)
}

// Search runs one search against a fixed endpoint. On a bound connection every hit is checked against the
// connection and the target, and foreign or unattributable hits are dropped. A connection bound to pages alone
// scans the search of the instance for its pages, see searchPages.
func (c *Client) Search(ctx context.Context, query string, target listTarget, page, count int) (output.Object, error) {
	if target.pagesOnly {
		return c.searchPages(ctx, query, page, count)
	}
	bookID := target.BookID
	path := "/api/search"
	switch {
	case target.ChapterID != 0:
		book, err := c.chapterIn(ctx, target.ChapterID)
		if err != nil {
			return output.Object{}, err
		}
		bookID = book
		path = "/api/search/chapter/" + strconv.FormatInt(target.ChapterID, 10)
	case target.BookID != 0:
		if err := c.requireBook(ctx, target.BookID); err != nil {
			return output.Object{}, err
		}
		path = "/api/search/book/" + strconv.FormatInt(target.BookID, 10)
	}
	found, err := c.searchOnce(ctx, path, query, page, count)
	if err != nil {
		return output.Object{}, err
	}
	results := make([]map[string]any, 0, len(found.Data))
	for _, h := range found.Data {
		if (c.scope.bound() || bookID != 0) && !c.hitAllowed(h, target, bookID) {
			continue
		}
		results = append(results, hitResult(h))
	}
	return output.Object{Fields: []output.Field{{Name: "results", Value: results}, {Name: "total", Value: found.Total}}}, nil
}

func (c *Client) searchOnce(ctx context.Context, path, query string, page, count int) (searchJSON, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("page", strconv.Itoa(page))
	q.Set("count", strconv.Itoa(count))
	var found searchJSON
	err := c.get(ctx, "search content", path, q, &found, argNames(contentSearch), provider.ClassPermission)
	return found, err
}

// hitAllowed reports whether a hit lies in the book or chapter of the target and, on a bound connection, in
// the connection. A hit is attributed by its own book and chapter; a shelf or a hit without a book is dropped.
func (c *Client) hitAllowed(h searchHit, target listTarget, bookID int64) bool {
	owner := h.BookID
	if h.Type == "book" {
		owner = h.ID
	}
	if h.Type == "bookshelf" || owner == 0 || (bookID != 0 && owner != bookID) {
		return false
	}
	if target.ChapterID != 0 && h.Type != "chapter" && h.ChapterID != target.ChapterID {
		return false
	}
	if target.ChapterID != 0 && h.Type == "chapter" && h.ID != target.ChapterID {
		return false
	}
	if !c.scope.bound() || c.inBook(owner) {
		return true
	}
	switch h.Type {
	case "chapter":
		return c.scope.hasChapter(h.ID)
	case "page":
		return c.scope.hasPage(h.ID) || (h.ChapterID > 0 && c.scope.hasChapter(h.ChapterID))
	}
	return false
}

// searchPages answers a search of a connection bound to pages alone. BookStack has no search within one page,
// so the search of the instance is read in pages of 100 hits until every bound page is found or the hits run
// out, at most maxSearchScans requests; page and count then apply to the bound pages found, and total counts
// them, so no number of foreign hits is shown.
func (c *Client) searchPages(ctx context.Context, query string, page, count int) (output.Object, error) {
	var kept []map[string]any
	seen := map[string]bool{}
	found := map[int64]bool{}
	for server := 1; len(found) < len(c.scope.pages); server++ {
		if server > maxSearchScans {
			return output.Object{}, &provider.Error{Class: provider.ClassInvalidResponse, Op: "search content",
				Message: "the search exceeds the scan limit; narrow the query"}
		}
		answer, err := c.searchOnce(ctx, "/api/search", query, server, maxSearchCount)
		if err != nil {
			return output.Object{}, err
		}
		added := 0
		for _, h := range answer.Data {
			key := h.Type + "/" + strconv.FormatInt(h.ID, 10)
			if seen[key] {
				continue
			}
			seen[key] = true
			added++
			if h.Type == "page" && c.scope.hasPage(h.ID) && !found[h.ID] {
				found[h.ID] = true
				kept = append(kept, hitResult(h))
			}
		}
		if added == 0 || server*maxSearchCount >= answer.Total {
			break
		}
	}
	start := min((page-1)*count, len(kept))
	results := kept[start:min(start+count, len(kept))]
	if results == nil {
		results = []map[string]any{}
	}
	return output.Object{Fields: []output.Field{{Name: "results", Value: results}, {Name: "total", Value: len(kept)}}}, nil
}

func hitResult(h searchHit) map[string]any {
	tags := []map[string]string{}
	for _, t := range h.Tags {
		if len(tags) >= maxResultTags {
			break
		}
		tags = append(tags, map[string]string{"name": clip(t.Name, maxResultString), "value": clip(t.Value, maxResultString)})
	}
	return map[string]any{
		"type": clip(h.Type, maxResultString), "id": h.ID, "name": clip(h.Name, maxResultString),
		"slug": clip(h.Slug, maxResultString), "book_id": h.BookID, "chapter_id": h.ChapterID,
		"url": clip(h.URL, maxResultString), "tags": tags,
		"preview_name":    clip(h.PreviewHTML.Name, maxPreviewChars),
		"preview_content": clip(h.PreviewHTML.Content, maxPreviewChars),
	}
}

// clip shortens s to at most n characters without splitting one.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}
