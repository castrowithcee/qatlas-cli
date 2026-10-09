package lexware

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	accountSensitivity   = "lexware-account-data"
	referenceSensitivity = "lexware-reference-data"

	maxCountries      = 500
	maxReferenceItems = 200
)

var accountReadRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: accountSensitivity,
}

var referenceReadRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: referenceSensitivity,
}

const noArguments = `{"type":"object","properties":{},"additionalProperties":false}`

// listSchema describes a capped list result; item properties are written out by the caller.
func listSchema(key, itemProperties string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"` + key + `":{"type":"array","items":{"type":"object",` +
		`"properties":{` + itemProperties + `},"additionalProperties":false}},"truncated":{"type":"boolean"}},` +
		`"required":["` + key + `","truncated"],"additionalProperties":false}`)
}

const emptyExample = `{}`

func referenceExample(description string) []capability.Example {
	return []capability.Example{{Description: description, Arguments: json.RawMessage(emptyExample)}}
}

const truncatedField = "true when Lexware reported more entries than the cap and the list was cut"

var profileGet = capability.Descriptor{
	ID: Provider + ".profile.get", Version: 1,
	Title: "Get the Lexware organization profile",
	Description: "Read the organization, contract features, and tax settings of the Lexware Office organization " +
		"behind an explicit connection's API key; the name and email of the key creator are never returned",
	Tags: []string{"lexware", "profile", "organization", "features", "get"}, Risk: accountReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(noArguments),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization_id":{"type":"string"},` +
		`"company_name":{"type":"string"},"features":{"type":"array","items":{"type":"string"}},` +
		`"business_features":{"type":"array","items":{"type":"string"}},"tax_type":{"type":"string"},` +
		`"small_business":{"type":"boolean"},"distance_sales_principle":{"type":"string"}},` +
		`"required":["organization_id","features","business_features"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "organization_id", Description: "Identifier of the Lexware organization (untrusted data)"},
		{Name: "company_name", Description: "Company name of the organization (untrusted data)"},
		{Name: "features", Description: "Contract features of the organization"},
		{Name: "business_features", Description: "Business features of the organization"},
		{Name: "tax_type", Description: "Tax type of the organization, for example net or gross"},
		{Name: "small_business", Description: "Whether the organization is a small business"},
		{Name: "distance_sales_principle", Description: "Distance sales principle of the organization"},
	},
	Examples: referenceExample("Read the organization profile of the connection"),
}

var countriesList = capability.Descriptor{
	ID: Provider + ".countries.list", Version: 1, Title: "List Lexware countries",
	Description: "List the countries Lexware Office knows with their tax classification, at most 500 entries",
	Tags:        []string{"lexware", "countries", "list", "reference", "tax"}, Risk: referenceReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(noArguments),
	OutputSchema: listSchema("countries", `"country_code":{"type":"string"},"name_de":{"type":"string"},`+
		`"name_en":{"type":"string"},"tax_classification":{"type":"string"}`),
	Fields: []capability.Field{
		{Name: "countries", Description: "Countries: country_code, name_de, name_en, tax_classification (untrusted data)"},
		{Name: "truncated", Description: truncatedField},
	},
	Examples: referenceExample("List the countries and their tax classification"),
}

var paymentConditionsList = capability.Descriptor{
	ID: Provider + ".paymentconditions.list", Version: 1, Title: "List Lexware payment conditions",
	Description: "List the payment conditions of the organization to reference when creating vouchers, at most 200 entries",
	Tags:        []string{"lexware", "payment", "conditions", "list", "reference"}, Risk: referenceReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(noArguments),
	OutputSchema: listSchema("payment_conditions", `"id":{"type":"string"},"label_template":{"type":"string"},`+
		`"duration_days":{"type":"integer"},"discount_percentage":{"type":"number"},`+
		`"discount_range_days":{"type":"integer"},"organization_default":{"type":"boolean"}`),
	Fields: []capability.Field{
		{Name: "payment_conditions", Description: "Conditions: id, label_template, duration_days, discount_percentage, discount_range_days, organization_default (untrusted data)"},
		{Name: "truncated", Description: truncatedField},
	},
	Examples: referenceExample("List the payment conditions of the organization"),
}

var postingCategoriesList = capability.Descriptor{
	ID: Provider + ".postingcategories.list", Version: 1, Title: "List Lexware posting categories",
	Description: "List the posting categories of the organization to reference when creating vouchers, at most 200 " +
		"entries; an optional type filters locally",
	Tags: []string{"lexware", "posting", "categories", "list", "reference"}, Risk: referenceReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"type":{"type":"string","enum":["income","outgo"]}},` +
		`"additionalProperties":false}`),
	OutputSchema: listSchema("posting_categories", `"id":{"type":"string"},"name":{"type":"string"},`+
		`"type":{"type":"string"},"contact_required":{"type":"boolean"},"split_allowed":{"type":"boolean"},`+
		`"group_name":{"type":"string"}`),
	Arguments: []capability.Argument{
		{Name: "type", Description: "Optional category type, income or outgo, filtered locally"},
	},
	Fields: []capability.Field{
		{Name: "posting_categories", Description: "Categories: id, name, type, contact_required, split_allowed, group_name (untrusted data)"},
		{Name: "truncated", Description: truncatedField},
	},
	Examples: []capability.Example{{
		Description: "List only the income categories", Arguments: json.RawMessage(`{"type":"income"}`),
	}},
}

var printLayoutsList = capability.Descriptor{
	ID: Provider + ".printlayouts.list", Version: 1, Title: "List Lexware print layouts",
	Description: "List the print layouts of the organization, at most 200 entries; the Lexware contract needs the " +
		"INVOICING_PRO scope, otherwise the call is refused as a missing contract scope",
	Tags: []string{"lexware", "print", "layouts", "list", "reference"}, Risk: referenceReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(noArguments),
	OutputSchema: listSchema("print_layouts", `"id":{"type":"string"},"name":{"type":"string"},`+
		`"default":{"type":"boolean"}`),
	Fields: []capability.Field{
		{Name: "print_layouts", Description: "Layouts: id, name, default (untrusted data)"},
		{Name: "truncated", Description: truncatedField},
	},
	Examples: referenceExample("List the print layouts of the organization"),
}

// Profile is the allowlisted view of the organization profile. The raw model deliberately lacks the key
// creator's identity and the connection identifier, so they cannot reach any output or error.
type Profile struct {
	OrganizationID         string   `json:"organization_id"`
	CompanyName            string   `json:"company_name,omitempty"`
	Features               []string `json:"features"`
	BusinessFeatures       []string `json:"business_features"`
	TaxType                string   `json:"tax_type,omitempty"`
	SmallBusiness          *bool    `json:"small_business,omitempty"`
	DistanceSalesPrinciple string   `json:"distance_sales_principle,omitempty"`
}

// GetProfile reads the organization profile of the connection's API key. It is also the basis for binding a
// connection to an organization.
func (c *Client) GetProfile(ctx context.Context) (*Profile, error) {
	var raw struct {
		OrganizationID         string   `json:"organizationId"`
		CompanyName            string   `json:"companyName"`
		Features               []string `json:"features"`
		BusinessFeatures       []string `json:"businessFeatures"`
		TaxType                string   `json:"taxType"`
		SmallBusiness          *bool    `json:"smallBusiness"`
		DistanceSalesPrinciple string   `json:"distanceSalesPrinciple"`
	}
	if err := c.get(ctx, "get profile", "", "/v1/profile", nil, &raw); err != nil {
		return nil, err
	}
	p := Profile(raw)
	if p.Features == nil {
		p.Features = []string{}
	}
	if p.BusinessFeatures == nil {
		p.BusinessFeatures = []string{}
	}
	return &p, nil
}

// Country is one country with its tax classification.
type Country struct {
	CountryCode       string `json:"country_code"`
	NameDE            string `json:"name_de,omitempty"`
	NameEN            string `json:"name_en,omitempty"`
	TaxClassification string `json:"tax_classification,omitempty"`
}

// PaymentCondition is one payment condition of the organization.
type PaymentCondition struct {
	ID                  string      `json:"id"`
	LabelTemplate       string      `json:"label_template,omitempty"`
	DurationDays        int         `json:"duration_days"`
	DiscountPercentage  json.Number `json:"discount_percentage,omitempty"`
	DiscountRangeDays   int         `json:"discount_range_days,omitempty"`
	OrganizationDefault bool        `json:"organization_default"`
}

// PostingCategory is one posting category of the organization.
type PostingCategory struct {
	ID              string `json:"id"`
	Name            string `json:"name,omitempty"`
	Type            string `json:"type,omitempty"`
	ContactRequired bool   `json:"contact_required"`
	SplitAllowed    bool   `json:"split_allowed"`
	GroupName       string `json:"group_name,omitempty"`
}

// PrintLayout is one print layout of the organization.
type PrintLayout struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Default bool   `json:"default"`
}

// getCapped reads a fixed-path list and cuts it to limit entries after the optional local filter.
func getCapped[T any](ctx context.Context, c *Client, op, path string, limit int, keep func(T) bool) ([]T, bool, error) {
	var raw []T
	if err := c.get(ctx, op, "", path, nil, &raw); err != nil {
		return nil, false, err
	}
	out := make([]T, 0, min(len(raw), limit))
	for _, item := range raw {
		if keep != nil && !keep(item) {
			continue
		}
		if len(out) == limit {
			return out, true, nil
		}
		out = append(out, item)
	}
	return out, false, nil
}

// ListCountries reads the countries, capped at maxCountries.
func (c *Client) ListCountries(ctx context.Context) ([]Country, bool, error) {
	type rawCountry struct {
		CountryCode       string `json:"countryCode"`
		NameDE            string `json:"countryNameDE"`
		NameEN            string `json:"countryNameEN"`
		TaxClassification string `json:"taxClassification"`
	}
	raw, truncated, err := getCapped[rawCountry](ctx, c, "list countries", "/v1/countries", maxCountries, nil)
	if err != nil {
		return nil, false, err
	}
	out := make([]Country, len(raw))
	for i, r := range raw {
		out[i] = Country(r)
	}
	return out, truncated, nil
}

// ListPaymentConditions reads the payment conditions, capped at maxReferenceItems.
func (c *Client) ListPaymentConditions(ctx context.Context) ([]PaymentCondition, bool, error) {
	type rawCondition struct {
		ID            string `json:"id"`
		LabelTemplate string `json:"paymentTermLabelTemplate"`
		Duration      int    `json:"paymentTermDuration"`
		Discount      struct {
			Percentage json.Number `json:"discountPercentage"`
			Range      int         `json:"discountRange"`
		} `json:"paymentDiscountConditions"`
		OrganizationDefault bool `json:"organizationDefault"`
	}
	raw, truncated, err := getCapped[rawCondition](ctx, c, "list payment conditions", "/v1/payment-conditions", maxReferenceItems, nil)
	if err != nil {
		return nil, false, err
	}
	out := make([]PaymentCondition, len(raw))
	for i, r := range raw {
		out[i] = PaymentCondition{ID: r.ID, LabelTemplate: r.LabelTemplate, DurationDays: r.Duration,
			DiscountPercentage: r.Discount.Percentage, DiscountRangeDays: r.Discount.Range,
			OrganizationDefault: r.OrganizationDefault}
	}
	return out, truncated, nil
}

// ListPostingCategories reads the posting categories, optionally filtered by type, capped at maxReferenceItems.
func (c *Client) ListPostingCategories(ctx context.Context, categoryType string) ([]PostingCategory, bool, error) {
	if categoryType != "" && categoryType != "income" && categoryType != "outgo" {
		return nil, false, providerError("list posting categories", "the type must be income or outgo")
	}
	type rawCategory struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		Type            string `json:"type"`
		ContactRequired bool   `json:"contactRequired"`
		SplitAllowed    bool   `json:"splitAllowed"`
		GroupName       string `json:"groupName"`
	}
	keep := func(r rawCategory) bool { return categoryType == "" || r.Type == categoryType }
	raw, truncated, err := getCapped(ctx, c, "list posting categories", "/v1/posting-categories", maxReferenceItems, keep)
	if err != nil {
		return nil, false, err
	}
	out := make([]PostingCategory, len(raw))
	for i, r := range raw {
		out[i] = PostingCategory(r)
	}
	return out, truncated, nil
}

// ListPrintLayouts reads the print layouts, capped at maxReferenceItems.
func (c *Client) ListPrintLayouts(ctx context.Context) ([]PrintLayout, bool, error) {
	type rawLayout struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Default bool   `json:"default"`
	}
	raw, truncated, err := getCapped[rawLayout](ctx, c, "list print layouts", "/v1/print-layouts", maxReferenceItems, nil)
	if err != nil {
		return nil, false, err
	}
	out := make([]PrintLayout, len(raw))
	for i, r := range raw {
		out[i] = PrintLayout(r)
	}
	return out, truncated, nil
}

func openClient(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return Open(ctx, resolved, secrets, red)
}

func invokeProfileGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := openClient(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetProfile(ctx)
}

// listResult wraps a capped list under its key.
func listResult[T any](key string, items []T, truncated bool, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	return map[string]any{key: items, "truncated": truncated}, nil
}

func invokeCountriesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := openClient(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	items, truncated, err := client.ListCountries(ctx)
	return listResult("countries", items, truncated, err)
}

func invokePaymentConditionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := openClient(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	items, truncated, err := client.ListPaymentConditions(ctx)
	return listResult("payment_conditions", items, truncated, err)
}

func invokePostingCategoriesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, providerError("list posting categories", "the validated arguments could not be read")
	}
	client, err := openClient(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	items, truncated, err := client.ListPostingCategories(ctx, arguments.Type)
	return listResult("posting_categories", items, truncated, err)
}

func invokePrintLayoutsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := openClient(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	items, truncated, err := client.ListPrintLayouts(ctx)
	return listResult("print_layouts", items, truncated, err)
}
