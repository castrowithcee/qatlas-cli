package lexware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	articleMayExist   = "; the article may have been created, check the article list before repeating it"
	articleMayChanged = "; the article may have been changed, read it before repeating the change"
)

var articleCreateRisk = capability.Risk{
	Effect:          capability.EffectCreate,
	Idempotency:     capability.IdempotencyNonIdempotent,
	Confirmation:    capability.ConfirmationRequired,
	OpenWorld:       true,
	DataSensitivity: articleSensitivity,
}

// The version read before the change makes a repeated update harmless: it fails as a conflict.
var articleUpdateRisk = capability.Risk{
	Effect:          capability.EffectUpdate,
	Idempotency:     capability.IdempotencyIdempotent,
	Confirmation:    capability.ConfirmationRequired,
	OpenWorld:       true,
	DataSensitivity: articleSensitivity,
}

// articleWriteProperties are the closed, writable article members shared by create and update.
const articleWriteProperties = `"title":{"type":"string","minLength":1,"maxLength":255},` +
	`"type":{"type":"string","enum":["PRODUCT","SERVICE"]},` +
	`"article_number":{"type":"string","minLength":1,"maxLength":64},` +
	`"gtin":{"type":"string","pattern":"^[0-9]{8,14}$"},` +
	`"unit_name":{"type":"string","minLength":1,"maxLength":64},` +
	`"description":{"type":"string","maxLength":4096},` +
	`"note":{"type":"string","maxLength":4096},` +
	`"price":{"type":"object","properties":{"net_price":{"type":"number","minimum":0},` +
	`"gross_price":{"type":"number","minimum":0},"leading_price":{"type":"string","enum":["NET","GROSS"]},` +
	`"tax_rate":{"type":"number","minimum":0,"maximum":100}},"required":["leading_price","tax_rate"],"additionalProperties":false}`

const articleWriteOutputSchema = `{"type":"object","properties":{"id":{"type":"string"},"created_date":{"type":"string"},` +
	`"updated_date":{"type":"string"},"version":{"type":"integer"}},"required":["id"],"additionalProperties":false}`

var articleWriteFields = []capability.Field{
	{Name: "id", Description: "Identifier of the article"},
	{Name: "created_date", Description: "Creation timestamp reported by Lexware"},
	{Name: "updated_date", Description: "Last change timestamp reported by Lexware"},
	{Name: "version", Description: "Version counter after the write, reported by Lexware"},
}

var articleWriteArguments = []capability.Argument{
	{Name: "title", Description: "Article title, 1 to 255 characters"},
	{Name: "type", Description: "PRODUCT or SERVICE"},
	{Name: "unit_name", Description: "Unit the price refers to, for example piece or hour, up to 64 characters"},
	{Name: "price", Description: "Object with leading_price NET or GROSS, tax_rate in percent, and the amount that leads: net_price for NET, gross_price for GROSS; Lexware derives the other amount"},
	{Name: "article_number", Description: "Article number, up to 64 characters"},
	{Name: "gtin", Description: "Global Trade Item Number of 8 to 14 digits"},
	{Name: "description", Description: "Article description, up to 4096 characters"},
	{Name: "note", Description: "Internal note, up to 4096 characters"},
}

// articleCreateArguments marks the members the creation schema requires.
func articleCreateArguments() []capability.Argument {
	arguments := append([]capability.Argument{}, articleWriteArguments...)
	for i := range arguments {
		switch arguments[i].Name {
		case "title", "type", "unit_name", "price":
			arguments[i].Required = true
		}
	}
	return arguments
}

var articlesCreate = capability.Descriptor{
	ID: Provider + ".articles.create", Version: 1, Title: "Create a Lexware article",
	Description: "Create one product or service in the Lexware Office article master data",
	Tags:        []string{"lexware", "articles", "create", "products", "services", "accounting"},
	Provider:    Provider, Risk: articleCreateRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + articleWriteProperties +
		`},"required":["title","type","unit_name","price"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(articleWriteOutputSchema),
	Arguments:    articleCreateArguments(),
	Fields:       articleWriteFields,
	Examples: []capability.Example{{
		Description: "A service priced per hour",
		Arguments: json.RawMessage(`{"title":"Consulting","type":"SERVICE","unit_name":"hour",` +
			`"price":{"net_price":120,"leading_price":"NET","tax_rate":19}}`),
	}},
}

var articlesUpdate = capability.Descriptor{
	ID: Provider + ".articles.update", Version: 1, Title: "Update a Lexware article",
	Description: "Change fields of one article: Qatlas reads the article, replaces only the given fields and " +
		"writes it back with the version it read. A change made by someone else in between is reported as a " +
		"conflict and never overwritten. A given price replaces the whole price",
	Tags:     []string{"lexware", "articles", "update", "products", "services", "accounting"},
	Provider: Provider, Risk: articleUpdateRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `,` +
		articleWriteProperties + `},"required":["id"],"minProperties":2,"additionalProperties":false}`),
	OutputSchema: json.RawMessage(articleWriteOutputSchema),
	Arguments: append([]capability.Argument{
		{Name: "id", Description: "Article identifier as a UUID, as returned by lexware.articles.list", Required: true},
	}, articleWriteArguments...),
	Fields: articleWriteFields,
	Examples: []capability.Example{{
		Description: "Change the title of one article",
		Arguments:   json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555","title":"Consulting hour"}`),
	}},
}

// articleChanges holds the writable article members. A nil member is absent, so an update changes only
// what the caller named, and an empty string clears a text.
type articleChanges struct {
	ID            string        `json:"id"`
	Title         *string       `json:"title"`
	Type          *string       `json:"type"`
	ArticleNumber *string       `json:"article_number"`
	GTIN          *string       `json:"gtin"`
	UnitName      *string       `json:"unit_name"`
	Description   *string       `json:"description"`
	Note          *string       `json:"note"`
	Price         *ArticlePrice `json:"price"`
}

func decodeArticleChanges(op string, raw json.RawMessage) (articleChanges, error) {
	var input articleChanges
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

// validate re-checks the bounds the schema states, because the handler is also reachable without it.
func (a articleChanges) validate() error {
	for _, field := range []struct {
		name  string
		value *string
		max   int
		min   int
	}{{"title", a.Title, 255, 1}, {"article_number", a.ArticleNumber, 64, 1}, {"unit_name", a.UnitName, 64, 1},
		{"description", a.Description, 4096, 0}, {"note", a.Note, 4096, 0}} {
		if field.value == nil {
			continue
		}
		if n := utf8.RuneCountInString(*field.value); n < field.min || n > field.max {
			return errors.New(field.name + " has an unsupported length")
		}
	}
	if a.Type != nil && *a.Type != "PRODUCT" && *a.Type != "SERVICE" {
		return errors.New("type must be PRODUCT or SERVICE")
	}
	if a.GTIN != nil && (len(*a.GTIN) < 8 || len(*a.GTIN) > 14 || strings.Trim(*a.GTIN, "0123456789") != "") {
		return errors.New("gtin must consist of 8 to 14 digits")
	}
	if p := a.Price; p != nil {
		switch {
		case p.TaxRate == "":
			return errors.New("price.tax_rate is required")
		case p.LeadingPrice == "NET" && p.NetPrice == "":
			return errors.New("price.net_price is required for a NET price")
		case p.LeadingPrice == "GROSS" && p.GrossPrice == "":
			return errors.New("price.gross_price is required for a GROSS price")
		case p.LeadingPrice != "NET" && p.LeadingPrice != "GROSS":
			return errors.New("price.leading_price must be NET or GROSS")
		}
	}
	return nil
}

// priceMap is the provider price object. Only the leading amount is sent: Lexware derives the other, and a
// stale second amount would contradict the new one.
func (p ArticlePrice) priceMap() map[string]any {
	price := map[string]any{"leadingPrice": p.LeadingPrice, "taxRate": p.TaxRate}
	if p.LeadingPrice == "NET" {
		price["netPrice"] = p.NetPrice
	} else {
		price["grossPrice"] = p.GrossPrice
	}
	return price
}

// set writes the named members into an article object.
func (a articleChanges) set(object rawObject) error {
	for _, field := range []struct {
		key   string
		value *string
	}{{"title", a.Title}, {"type", a.Type}, {"articleNumber", a.ArticleNumber}, {"gtin", a.GTIN},
		{"unitName", a.UnitName}, {"description", a.Description}, {"note", a.Note}} {
		if field.value == nil {
			continue
		}
		if err := object.set(field.key, *field.value); err != nil {
			return err
		}
	}
	if a.Price != nil {
		// Members of the stored price Qatlas does not model stay; the amounts are replaced as a pair.
		price := object.object("price")
		delete(price, "netPrice")
		delete(price, "grossPrice")
		for key, value := range a.Price.priceMap() {
			if err := price.set(key, value); err != nil {
				return err
			}
		}
		return object.set("price", price)
	}
	return nil
}

func invokeArticlesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create article"
	input, err := decodeArticleChanges(op, raw)
	if err != nil {
		return nil, err
	}
	if input.ID != "" || input.Title == nil || input.Type == nil || input.UnitName == nil || input.Price == nil {
		return nil, providerError(op, "title, type, unit_name, and price are required")
	}
	if err := input.validate(); err != nil {
		return nil, providerError(op, err.Error())
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreateArticle(ctx, input)
}

func invokeArticlesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update article"
	input, err := decodeArticleChanges(op, raw)
	if err != nil {
		return nil, err
	}
	if !validUUID(input.ID) {
		return nil, providerError(op, "the article identifier must be a UUID")
	}
	changed := input
	changed.ID = ""
	if changed == (articleChanges{}) {
		return nil, providerError(op, "at least one field to change is required")
	}
	if err := input.validate(); err != nil {
		return nil, providerError(op, err.Error())
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateArticle(ctx, input)
}

// CreateArticle posts one article: exactly one request, never repeated.
func (c *Client) CreateArticle(ctx context.Context, input articleChanges) (*createResult, error) {
	const op = "create article"
	object := rawObject{}
	if err := input.set(object); err != nil {
		return nil, providerError(op, "the change could not be applied")
	}
	return c.postObject(ctx, op, "/v1/articles", object, articleMayExist, "an article")
}

// UpdateArticle reads the article and writes it back with the given fields replaced and the version it read.
func (c *Client) UpdateArticle(ctx context.Context, input articleChanges) (*createResult, error) {
	return c.updateObject(ctx, "update article", resourceArticle, "/v1/articles", input.ID,
		articleMayChanged, "an article", input.set)
}
