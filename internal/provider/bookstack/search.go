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
)

var contentSearch = capability.Descriptor{
	ID: Provider + ".content.search", Version: 1, Title: "Search BookStack content",
	Description: "Full-text search over shelves, books, chapters, and pages, across the instance, in one book, or " +
		"in one chapter, page by page. The query may use the BookStack search syntax. Previews are untrusted HTML.",
	Tags:     []string{"knowledge", "search", "bookstack"},
	Risk:     bookstackReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","minLength":1,"maxLength":1000},` +
		`"book_id":{"type":"integer","minimum":1},"chapter_id":{"type":"integer","minimum":1},` +
		`"page":{"type":"integer","minimum":1},"count":{"type":"integer","minimum":1,"maximum":100}},` +
		`"required":["query"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"results":{"type":"array","items":{"type":"object","properties":{` +
		`"type":{"type":"string"},"id":{"type":"integer"},"name":{"type":"string"},"slug":{"type":"string"},` +
		`"book_id":{"type":"integer"},"chapter_id":{"type":"integer"},"url":{"type":"string"},` +
		`"tags":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"value":{"type":"string"}}}},` +
		`"preview_name":{"type":"string"},"preview_content":{"type":"string"}}}},"total":{"type":"integer"}},"required":["results","total"]}`),
	Arguments: []capability.Argument{
		{Name: "query", Description: "Search text, 1 to 1000 characters; the BookStack search syntax is allowed", Required: true},
		{Name: "book_id", Description: "Search only in this book; mutually exclusive with chapter_id; required for a connection bound to several books unless chapter_id is given"},
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
	// Without a book or chapter, a bound connection searches its only book; several books need an argument.
	if target, err = bound.listBook(target); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.Search(ctx, in.Query, target, page, count)
}

// Search runs one search against a fixed endpoint. On a bound connection every hit is checked against the
// book of the target and foreign or unattributable hits are dropped.
func (c *Client) Search(ctx context.Context, query string, target listTarget, page, count int) (output.Object, error) {
	bookID := target.BookID
	path := "/api/search"
	switch {
	case target.ChapterID != 0:
		if c.scope.bound() {
			book, err := c.chapterBook(ctx, target.ChapterID)
			if err != nil {
				return output.Object{}, err
			}
			bookID = book
		}
		path = "/api/search/chapter/" + strconv.FormatInt(target.ChapterID, 10)
	case target.BookID != 0:
		path = "/api/search/book/" + strconv.FormatInt(target.BookID, 10)
	}
	q := url.Values{}
	q.Set("query", query)
	q.Set("page", strconv.Itoa(page))
	q.Set("count", strconv.Itoa(count))

	var found searchJSON
	if err := c.get(ctx, "search content", path, q, &found, argNames(contentSearch), provider.ClassPermission); err != nil {
		return output.Object{}, err
	}
	results := make([]map[string]any, 0, len(found.Data))
	for _, h := range found.Data {
		if c.scope.bound() || bookID != 0 {
			owner := h.BookID
			if h.Type == "book" {
				owner = h.ID
			}
			if h.Type == "bookshelf" || owner == 0 || (bookID != 0 && owner != bookID) ||
				(c.scope.bound() && !c.scope.allows(owner)) {
				continue
			}
			if target.ChapterID != 0 && h.Type != "chapter" && h.ChapterID != target.ChapterID {
				continue
			}
			if target.ChapterID != 0 && h.Type == "chapter" && h.ID != target.ChapterID {
				continue
			}
		}
		tags := []map[string]string{}
		for _, t := range h.Tags {
			if len(tags) >= maxResultTags {
				break
			}
			tags = append(tags, map[string]string{"name": clip(t.Name, maxResultString), "value": clip(t.Value, maxResultString)})
		}
		results = append(results, map[string]any{
			"type": clip(h.Type, maxResultString), "id": h.ID, "name": clip(h.Name, maxResultString),
			"slug": clip(h.Slug, maxResultString), "book_id": h.BookID, "chapter_id": h.ChapterID,
			"url": clip(h.URL, maxResultString), "tags": tags,
			"preview_name":    clip(h.PreviewHTML.Name, maxPreviewChars),
			"preview_content": clip(h.PreviewHTML.Content, maxPreviewChars),
		})
	}
	return output.Object{Fields: []output.Field{{Name: "results", Value: results}, {Name: "total", Value: found.Total}}}, nil
}

// clip shortens s to at most n characters without splitting one.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}
