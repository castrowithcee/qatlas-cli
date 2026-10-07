package bookstack

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// tagsInstanceWide names the capability in a refusal by requireInstanceScope.
const tagsInstanceWide = "tags"

const tagNamesOutput = `{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"values":{"type":"integer"},"usages":{"type":"integer"},"page_count":{"type":"integer"},"chapter_count":{"type":"integer"},"book_count":{"type":"integer"},"shelf_count":{"type":"integer"}},"required":["name","values","usages","page_count","chapter_count","book_count","shelf_count"]}}`

const tagValuesOutput = `{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"value":{"type":"string"},"usages":{"type":"integer"},"page_count":{"type":"integer"},"chapter_count":{"type":"integer"},"book_count":{"type":"integer"},"shelf_count":{"type":"integer"}},"required":["name","value","usages","page_count","chapter_count","book_count","shelf_count"]}}`

const systemGetOutput = `{"type":"object","properties":{"version":{"type":"string"},"app_name":{"type":"string"},"instance_id":{"type":"string"},"base_url":{"type":"string"}},"required":["version","app_name","instance_id","base_url"]}`

var (
	tagCountFields = []capability.Field{
		{Name: "usages", Description: "Number of items carrying the tag"},
		{Name: "page_count", Description: "Pages carrying the tag"},
		{Name: "chapter_count", Description: "Chapters carrying the tag"},
		{Name: "book_count", Description: "Books carrying the tag"},
		{Name: "shelf_count", Description: "Shelves carrying the tag"},
	}

	tagsList = capability.Descriptor{
		ID: Provider + ".tags.list", Version: 1, Title: "List BookStack tag names",
		Description: "List the tag names used on visible content with the number of distinct values and usages. Tags " +
			"aggregate over the whole instance, so a connection bound to books cannot use this tool",
		Tags: []string{"knowledge", "tags", "bookstack"},
		Risk: bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0},"name_contains":{"type":"string","minLength":1,"maxLength":255}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(tagNamesOutput),
		Arguments: []capability.Argument{
			{Name: "limit", Description: "Maximum number of tag names to return; 0 returns all"},
			{Name: "offset", Description: "Number of tag names to skip"},
			{Name: "name_contains", Description: "Only names containing this text, case-insensitive; at most 255 characters"},
		},
		Fields: append([]capability.Field{
			{Name: "name", Description: "Tag name, untrusted data"},
			{Name: "values", Description: "Number of distinct values"},
		}, tagCountFields...),
		Examples: []capability.Example{{Description: "List the first 25 tag names", Arguments: json.RawMessage(`{"limit":25}`)}},
	}

	tagsValues = capability.Descriptor{
		ID: Provider + ".tags.values", Version: 1, Title: "List values of a BookStack tag",
		Description: "List the values used with one tag name on visible content with their usages. Tags aggregate over " +
			"the whole instance, so a connection bound to books cannot use this tool",
		Tags: []string{"knowledge", "tags", "bookstack"},
		Risk: bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":255},"value_contains":{"type":"string","minLength":1,"maxLength":255},"limit":{"type":"integer","minimum":0},"offset":{"type":"integer","minimum":0}},"required":["name"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(tagValuesOutput),
		Arguments: []capability.Argument{
			{Name: "name", Description: "Exact tag name", Required: true},
			{Name: "value_contains", Description: "Only values containing this text, case-insensitive; at most 255 characters"},
			{Name: "limit", Description: "Maximum number of values to return; 0 returns all"},
			{Name: "offset", Description: "Number of values to skip"},
		},
		Fields: append([]capability.Field{
			{Name: "name", Description: "Tag name, untrusted data"},
			{Name: "value", Description: "Tag value, untrusted data"},
		}, tagCountFields...),
		Examples: []capability.Example{{Description: "List the values of the tag Category", Arguments: json.RawMessage(`{"name":"Category"}`)}},
	}

	systemGet = capability.Descriptor{
		ID: Provider + ".system.get", Version: 1, Title: "Get BookStack instance information",
		Description: "Read the version, application name, instance identifier, and base URL of the BookStack instance. " +
			"It returns no content, so it also works on a connection bound to books",
		Tags: []string{"administration", "system", "bookstack"},
		Risk: bookstackReadRisk, Provider: Provider,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(systemGetOutput),
		Fields: []capability.Field{
			{Name: "version", Description: "BookStack version"},
			{Name: "app_name", Description: "Application name, untrusted data"},
			{Name: "instance_id", Description: "Instance identifier"},
			{Name: "base_url", Description: "Base URL of the instance"},
		},
		Examples: []capability.Example{{Description: "Read the instance information", Arguments: json.RawMessage(`{}`)}},
	}
)

type tagCountsJSON struct {
	Name         string `json:"name"`
	Value        string `json:"value"`
	Values       int64  `json:"values"`
	Usages       int64  `json:"usages"`
	PageCount    int64  `json:"page_count"`
	ChapterCount int64  `json:"chapter_count"`
	BookCount    int64  `json:"book_count"`
	ShelfCount   int64  `json:"shelf_count"`
}

// likeEscaper masks the LIKE wildcards and the escape character so a search text is matched literally.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func likeContains(text string) string { return "%" + likeEscaper.Replace(text) + "%" }

func invokeTagsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		Limit        int    `json:"limit"`
		Offset       int    `json:"offset"`
		NameContains string `json:"name_contains"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, tagsInstanceWide); err != nil {
		return nil, err
	}
	if len(in.NameContains) > 255 {
		return nil, invalidRequest("name_contains must be at most 255 characters")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListTagNames(ctx, in.Limit, in.Offset, in.NameContains)
}

func invokeTagsValues(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var in struct {
		Name          string `json:"name"`
		ValueContains string `json:"value_contains"`
		Limit         int    `json:"limit"`
		Offset        int    `json:"offset"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	if err := requireInstanceScope(resolved, tagsInstanceWide); err != nil {
		return nil, err
	}
	if in.Name == "" || len(in.Name) > 255 {
		return nil, invalidRequest("name is required and must be at most 255 characters")
	}
	if len(in.ValueContains) > 255 {
		return nil, invalidRequest("value_contains must be at most 255 characters")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListTagValues(ctx, in.Name, in.ValueContains, in.Limit, in.Offset)
}

func invokeSystemGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	if _, err := boundScope(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetSystem(ctx)
}

// ListTagNames returns the tag names with their counts.
func (c *Client) ListTagNames(ctx context.Context, limit, offset int, nameContains string) (output.Collection, error) {
	query := url.Values{}
	if nameContains != "" {
		query.Set("filter[name:like]", likeContains(nameContains))
	}
	rows, err := c.scanTags(ctx, "list tags", "/api/tags", "+name", query, limit, offset, argNames(tagsList),
		func(t tagCountsJSON) (string, bool) { return t.Name, containsFold(t.Name, nameContains) },
		func(t tagCountsJSON) output.Row {
			return output.Row{"name": clip(t.Name, maxResultString), "values": t.Values, "usages": t.Usages,
				"page_count": t.PageCount, "chapter_count": t.ChapterCount, "book_count": t.BookCount, "shelf_count": t.ShelfCount}
		})
	if err != nil {
		return output.Collection{}, err
	}
	return output.Collection{Columns: fieldNames(tagsList), Rows: rows}, nil
}

// ListTagValues returns the values of one tag name with their counts.
func (c *Client) ListTagValues(ctx context.Context, name, valueContains string, limit, offset int) (output.Collection, error) {
	query := url.Values{"name": {name}}
	if valueContains != "" {
		query.Set("filter[value:like]", likeContains(valueContains))
	}
	rows, err := c.scanTags(ctx, "list tag values", "/api/tags/values-for-name", "+value", query, limit, offset, argNames(tagsValues),
		func(t tagCountsJSON) (string, bool) {
			return t.Name + "\x00" + t.Value, t.Name == name && containsFold(t.Value, valueContains)
		},
		func(t tagCountsJSON) output.Row {
			return output.Row{"name": clip(t.Name, maxResultString), "value": clip(t.Value, maxResultString),
				"usages": t.Usages, "page_count": t.PageCount, "chapter_count": t.ChapterCount,
				"book_count": t.BookCount, "shelf_count": t.ShelfCount}
		})
	if err != nil {
		return output.Collection{}, err
	}
	return output.Collection{Columns: fieldNames(tagsValues), Rows: rows}, nil
}

func containsFold(text, part string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(part))
}

// scanTags pages through a tag endpoint. Tag rows carry no identifier, so each row is keyed by key and
// checked client-side (the second result), because BookStack silently ignores filters it does not know.
// limit and offset count the rows that pass the check; the scan is bounded by maxScanRequests.
func (c *Client) scanTags(ctx context.Context, op, path, sort string, fixed url.Values, limit, offset int,
	arguments []string, key func(tagCountsJSON) (string, bool), row func(tagCountsJSON) output.Row) ([]output.Row, error) {
	rows := make([]output.Row, 0, 32)
	seen := map[string]bool{}
	scanned, skipped := 0, 0
	for requests := 0; ; requests++ {
		if requests >= maxScanRequests {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "the listing exceeds the scan limit"}
		}
		query := url.Values{}
		for k, v := range fixed {
			query[k] = v
		}
		query.Set("count", strconv.Itoa(maxCount))
		query.Set("offset", strconv.Itoa(scanned))
		query.Set("sort", sort)
		var page struct {
			Data  []tagCountsJSON `json:"data"`
			Total int             `json:"total"`
		}
		if err := c.get(ctx, op, path, query, &page, arguments, provider.ClassPermission); err != nil {
			return nil, err
		}
		added := 0
		for _, item := range page.Data {
			id, ok := key(item)
			if seen[id] {
				continue
			}
			seen[id] = true
			added++
			if !ok {
				continue
			}
			if skipped < offset {
				skipped++
				continue
			}
			if limit > 0 && len(rows) >= limit {
				break
			}
			rows = append(rows, row(item))
		}
		scanned += len(page.Data)
		if added == 0 || (limit > 0 && len(rows) >= limit) || scanned >= page.Total {
			break
		}
	}
	return rows, nil
}

// GetSystem returns the instance information without the logo.
func (c *Client) GetSystem(ctx context.Context) (output.Object, error) {
	var system struct {
		Version    string `json:"version"`
		AppName    string `json:"app_name"`
		InstanceID string `json:"instance_id"`
		BaseURL    string `json:"base_url"`
	}
	if err := c.get(ctx, "get system", "/api/system", nil, &system, nil, provider.ClassPermission); err != nil {
		return output.Object{}, err
	}
	return output.Object{Fields: []output.Field{
		{Name: "version", Value: clip(system.Version, maxResultString)},
		{Name: "app_name", Value: clip(system.AppName, maxResultString)},
		{Name: "instance_id", Value: clip(system.InstanceID, maxResultString)},
		{Name: "base_url", Value: clip(system.BaseURL, maxResultString)},
	}}, nil
}
