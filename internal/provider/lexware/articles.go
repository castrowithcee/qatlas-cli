package lexware

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// articleSensitivity classifies results as product and service master data of the configured organization.
const articleSensitivity = "lexware-article-data"

var articlesReadRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: articleSensitivity,
}

const articleSchema = `{"type":"object","properties":{"id":{"type":"string"},"version":{"type":"integer"},` +
	`"archived":{"type":"boolean"},"title":{"type":"string"},"description":{"type":"string"},` +
	`"type":{"type":"string"},"article_number":{"type":"string"},"gtin":{"type":"string"},` +
	`"note":{"type":"string"},"unit_name":{"type":"string"},` +
	`"price":{"type":"object","properties":{"net_price":{"type":"number"},"gross_price":{"type":"number"},` +
	`"leading_price":{"type":"string"},"tax_rate":{"type":"number"}},"additionalProperties":false}},` +
	`"required":["id","archived"],"additionalProperties":false}`

var articleFields = []capability.Field{
	{Name: "id", Description: "Article identifier"},
	{Name: "version", Description: "Version counter reported by Lexware"},
	{Name: "archived", Description: "True when the article is archived in Lexware"},
	{Name: "title", Description: "Article title, untrusted data"},
	{Name: "description", Description: "Article description, untrusted data"},
	{Name: "type", Description: "PRODUCT or SERVICE"},
	{Name: "article_number", Description: "Article number"},
	{Name: "gtin", Description: "Global Trade Item Number"},
	{Name: "note", Description: "Free-text note on the article, untrusted data"},
	{Name: "unit_name", Description: "Unit the price refers to, for example piece or hour"},
	{Name: "price", Description: "net_price, gross_price, leading_price (NET or GROSS) and tax_rate in percent"},
}

var articlesList = capability.Descriptor{
	ID:      Provider + ".articles.list",
	Version: 1,
	Title:   "List Lexware articles",
	Description: "List one page of products and services of an explicit Lexware Office connection, optionally " +
		"filtered by article number, GTIN, or type",
	Tags:     []string{"lexware", "articles", "list", "products", "services", "accounting"},
	Risk:     articlesReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"article_number":{"type":"string","minLength":1,"maxLength":64},` +
		`"gtin":{"type":"string","pattern":"^[0-9]{8,14}$"},` +
		`"type":{"type":"string","enum":["PRODUCT","SERVICE"]},` +
		`"page":{"type":"integer","minimum":0,"maximum":200},` +
		`"size":{"type":"integer","minimum":1,"maximum":100}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"articles":{"type":"array","items":` + articleSchema + `},` +
		`"page":{"type":"integer"},"size":{"type":"integer"},"total_pages":{"type":"integer"},` +
		`"total_elements":{"type":"integer"},"last_page":{"type":"boolean"}},` +
		`"required":["articles","page","size","total_pages","total_elements","last_page"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "article_number", Description: "Return the article carrying this article number"},
		{Name: "gtin", Description: "Return the article carrying this GTIN of 8 to 14 digits"},
		{Name: "type", Description: "Return only articles of this type: PRODUCT or SERVICE"},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "articles", Description: "Articles on this page, untrusted data; each carries the fields of lexware.articles.get"},
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "Read the first page of services",
		Arguments:   json.RawMessage(`{"type":"SERVICE","page":0,"size":25}`),
	}},
}

var articlesGet = capability.Descriptor{
	ID:          Provider + ".articles.get",
	Version:     1,
	Title:       "Get a Lexware article",
	Description: "Read one product or service of an explicit Lexware Office connection by its identifier",
	Tags:        []string{"lexware", "articles", "get", "products", "services", "accounting"},
	Risk:        articlesReadRisk,
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `},` +
		`"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(articleSchema),
	Arguments: []capability.Argument{
		{Name: "id", Description: "Article identifier as a UUID, as returned by lexware.articles.list", Required: true},
	},
	Fields: articleFields,
	Examples: []capability.Example{{
		Description: "Read one article by the identifier a list result reported",
		Arguments:   json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555"}`),
	}},
}

func invokeArticlesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options ArticleListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, providerError("list articles", "the validated arguments could not be read")
	}
	if err := options.normalize(); err != nil {
		return nil, providerError("list articles", err.Error())
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListArticles(ctx, options)
}

func invokeArticlesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, providerError("get article", "the validated arguments could not be read")
	}
	if !validUUID(arguments.ID) {
		return nil, providerError("get article", "the article identifier must be a UUID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetArticle(ctx, arguments.ID)
}

// ArticleListOptions are the controlled filters of the article list.
type ArticleListOptions struct {
	ArticleNumber string `json:"article_number"`
	GTIN          string `json:"gtin"`
	Type          string `json:"type"`
	Page          int    `json:"page"`
	Size          int    `json:"size"`
}

func (o *ArticleListOptions) normalize() error {
	if n := utf8.RuneCountInString(o.ArticleNumber); n > 64 {
		return errors.New("the article number filter is too long")
	}
	if o.GTIN != "" {
		if len(o.GTIN) < 8 || len(o.GTIN) > 14 || strings.Trim(o.GTIN, "0123456789") != "" {
			return errors.New("the GTIN filter must consist of 8 to 14 digits")
		}
	}
	if o.Type != "" && o.Type != "PRODUCT" && o.Type != "SERVICE" {
		return errors.New("the type filter must be PRODUCT or SERVICE")
	}
	return normalizePaging(&o.Page, &o.Size)
}

func articleQuery(options ArticleListOptions) url.Values {
	query := url.Values{}
	query.Set("page", strconv.Itoa(options.Page))
	query.Set("size", strconv.Itoa(options.Size))
	if options.ArticleNumber != "" {
		query.Set("articleNumber", options.ArticleNumber)
	}
	if options.GTIN != "" {
		query.Set("gtin", options.GTIN)
	}
	if options.Type != "" {
		query.Set("type", options.Type)
	}
	return query
}

// ArticleListResult is the normalised page of articles.
type ArticleListResult struct {
	Articles      []ArticleRecord `json:"articles"`
	Page          int             `json:"page"`
	Size          int             `json:"size"`
	TotalPages    int             `json:"total_pages"`
	TotalElements int             `json:"total_elements"`
	LastPage      bool            `json:"last_page"`
}

// ArticleRecord is the stable Qatlas view of one article. Free texts are untrusted data; an amount keeps
// the provider's exact digits.
type ArticleRecord struct {
	ID            string        `json:"id"`
	Version       int           `json:"version,omitempty"`
	Archived      bool          `json:"archived"`
	Title         string        `json:"title,omitempty"`
	Description   string        `json:"description,omitempty"`
	Type          string        `json:"type,omitempty"`
	ArticleNumber string        `json:"article_number,omitempty"`
	GTIN          string        `json:"gtin,omitempty"`
	Note          string        `json:"note,omitempty"`
	UnitName      string        `json:"unit_name,omitempty"`
	Price         *ArticlePrice `json:"price,omitempty"`
}

// ArticlePrice is the unit price of an article.
type ArticlePrice struct {
	NetPrice     json.Number `json:"net_price,omitempty"`
	GrossPrice   json.Number `json:"gross_price,omitempty"`
	LeadingPrice string      `json:"leading_price,omitempty"`
	TaxRate      json.Number `json:"tax_rate,omitempty"`
}

// articleJSON mirrors the provider fields the article operations read.
type articleJSON struct {
	ID            string `json:"id"`
	Version       int    `json:"version"`
	Archived      bool   `json:"archived"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	Type          string `json:"type"`
	ArticleNumber string `json:"articleNumber"`
	GTIN          string `json:"gtin"`
	Note          string `json:"note"`
	UnitName      string `json:"unitName"`
	Price         struct {
		NetPrice     json.Number `json:"netPrice"`
		GrossPrice   json.Number `json:"grossPrice"`
		LeadingPrice string      `json:"leadingPrice"`
		TaxRate      json.Number `json:"taxRate"`
	} `json:"price"`
}

func (raw articleJSON) record() ArticleRecord {
	record := ArticleRecord{
		ID: raw.ID, Version: raw.Version, Archived: raw.Archived, Title: raw.Title,
		Description: raw.Description, Type: raw.Type, ArticleNumber: raw.ArticleNumber, GTIN: raw.GTIN,
		Note: raw.Note, UnitName: raw.UnitName,
	}
	price := ArticlePrice(raw.Price)
	if price != (ArticlePrice{}) {
		record.Price = &price
	}
	return record
}

// ListArticles reads exactly one page of articles.
func (c *Client) ListArticles(ctx context.Context, options ArticleListOptions) (*ArticleListResult, error) {
	const op = "list articles"
	if err := options.normalize(); err != nil {
		return nil, providerError(op, err.Error())
	}
	var page struct {
		Content []articleJSON `json:"content"`
		pagingJSON
	}
	if err := c.get(ctx, op, "", "/v1/articles", articleQuery(options), &page); err != nil {
		return nil, err
	}
	articles := make([]ArticleRecord, 0, len(page.Content))
	for _, entry := range page.Content {
		if !validUUID(entry.ID) {
			return nil, &provider.Error{
				Class: provider.ClassInvalidResponse, Op: op,
				Message: "Lexware returned an article without a usable identifier",
			}
		}
		articles = append(articles, entry.record())
	}
	return &ArticleListResult{
		Articles: articles, Page: page.Number, Size: page.Size, TotalPages: page.TotalPages,
		TotalElements: page.TotalElements, LastPage: page.Last,
	}, nil
}

// GetArticle reads exactly the article of a validated identifier and performs no other provider I/O.
func (c *Client) GetArticle(ctx context.Context, id string) (*ArticleRecord, error) {
	const op = "get article"
	if !validUUID(id) {
		return nil, providerError(op, "the article identifier must be a UUID")
	}
	var raw articleJSON
	if err := c.get(ctx, op, resourceArticle, "/v1/articles/"+url.PathEscape(id), nil, &raw); err != nil {
		return nil, err
	}
	if !strings.EqualFold(raw.ID, id) {
		return nil, &provider.Error{
			Class: provider.ClassInvalidResponse, Op: op,
			Message: "Lexware answered with a different article than the requested one",
		}
	}
	record := raw.record()
	return &record, nil
}
