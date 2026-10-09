package lexware

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const resourceRecurringTemplate = "recurring template"

// recurringSortProperties maps the stable sort names of the template list to the provider's properties.
var recurringSortProperties = map[string]string{
	"created_date":        "createdDate",
	"updated_date":        "updatedDate",
	"next_execution_date": "nextExecutionDate",
}

// RecurringSettings is the execution schedule of a recurring template. The provider's error message of a
// failed execution is deliberately not read: only the fact that it failed is reported.
type RecurringSettings struct {
	StartDate           string `json:"start_date,omitempty"`
	EndDate             string `json:"end_date,omitempty"`
	Finalize            bool   `json:"finalize"`
	ShippingType        string `json:"shipping_type,omitempty"`
	ExecutionInterval   string `json:"execution_interval,omitempty"`
	NextExecutionDate   string `json:"next_execution_date,omitempty"`
	LastExecutionFailed bool   `json:"last_execution_failed"`
	ExecutionStatus     string `json:"execution_status,omitempty"`
	RetroactiveInvoice  *bool  `json:"retroactive_invoice,omitempty"`
}

// RecurringTemplateSummary is one entry of the template list.
type RecurringTemplateSummary struct {
	ID               string             `json:"id"`
	Version          int                `json:"version,omitempty"`
	CreatedDate      string             `json:"created_date,omitempty"`
	UpdatedDate      string             `json:"updated_date,omitempty"`
	Title            string             `json:"title,omitempty"`
	PaymentTermLabel string             `json:"payment_term_label,omitempty"`
	Currency         string             `json:"currency,omitempty"`
	TotalNetAmount   json.Number        `json:"total_net_amount,omitempty"`
	TotalGrossAmount json.Number        `json:"total_gross_amount,omitempty"`
	TotalTaxAmount   json.Number        `json:"total_tax_amount,omitempty"`
	Contact          *Contact           `json:"contact,omitempty"`
	Settings         *RecurringSettings `json:"settings,omitempty"`
}

// RecurringTemplateListResult is the normalised page of recurring templates.
type RecurringTemplateListResult struct {
	Templates     []RecurringTemplateSummary `json:"templates"`
	Page          int                        `json:"page"`
	Size          int                        `json:"size"`
	TotalPages    int                        `json:"total_pages"`
	TotalElements int                        `json:"total_elements"`
	LastPage      bool                       `json:"last_page"`
}

// RecurringTemplate is the stable Qatlas view of one template for recurring invoices. Everything a Lexware
// user typed is untrusted data.
type RecurringTemplate struct {
	ID               string             `json:"id"`
	Version          int                `json:"version,omitempty"`
	CreatedDate      string             `json:"created_date,omitempty"`
	UpdatedDate      string             `json:"updated_date,omitempty"`
	Language         string             `json:"language,omitempty"`
	Title            string             `json:"title,omitempty"`
	Introduction     string             `json:"introduction,omitempty"`
	Remark           string             `json:"remark,omitempty"`
	PaymentTermLabel string             `json:"payment_term_label,omitempty"`
	TaxType          string             `json:"tax_type,omitempty"`
	Currency         string             `json:"currency,omitempty"`
	TotalNetAmount   json.Number        `json:"total_net_amount,omitempty"`
	TotalGrossAmount json.Number        `json:"total_gross_amount,omitempty"`
	TotalTaxAmount   json.Number        `json:"total_tax_amount,omitempty"`
	Contact          *Contact           `json:"contact,omitempty"`
	Shipping         *Shipping          `json:"shipping,omitempty"`
	Settings         *RecurringSettings `json:"settings,omitempty"`
	LineItems        []LineItem         `json:"line_items"`
}

// recurringJSON mirrors the provider fields of a template, in list and detail form. The sales voucher
// members are decoded by the shared mapper.
type recurringJSON struct {
	invoiceJSON
	PaymentTermLabel string `json:"paymentTermLabel"`
	Settings         struct {
		StartDate           string `json:"startDate"`
		EndDate             string `json:"endDate"`
		Finalize            bool   `json:"finalize"`
		ShippingType        string `json:"shippingType"`
		ExecutionInterval   string `json:"executionInterval"`
		NextExecutionDate   string `json:"nextExecutionDate"`
		LastExecutionFailed bool   `json:"lastExecutionFailed"`
		ExecutionStatus     string `json:"executionStatus"`
		RetroactiveInvoice  *bool  `json:"retroactiveInvoice"`
	} `json:"recurringTemplateSettings"`
	RetroactiveInvoice *bool `json:"retroactiveInvoice"`
}

func (raw *recurringJSON) settings() *RecurringSettings {
	s := raw.Settings
	retroactive := s.RetroactiveInvoice
	if retroactive == nil {
		retroactive = raw.RetroactiveInvoice
	}
	settings := RecurringSettings{
		StartDate: s.StartDate, EndDate: s.EndDate, Finalize: s.Finalize, ShippingType: s.ShippingType,
		ExecutionInterval: s.ExecutionInterval, NextExecutionDate: s.NextExecutionDate,
		LastExecutionFailed: s.LastExecutionFailed, ExecutionStatus: s.ExecutionStatus,
		RetroactiveInvoice: retroactive,
	}
	if settings == (RecurringSettings{}) {
		return nil
	}
	return &settings
}

const recurringSettingsSchema = `{"type":"object","properties":{"start_date":{"type":"string"},` +
	`"end_date":{"type":"string"},"finalize":{"type":"boolean"},"shipping_type":{"type":"string"},` +
	`"execution_interval":{"type":"string"},"next_execution_date":{"type":"string"},` +
	`"last_execution_failed":{"type":"boolean"},"execution_status":{"type":"string"},` +
	`"retroactive_invoice":{"type":"boolean"}},"additionalProperties":false}`

const recurringTotals = `"currency":{"type":"string"},"total_net_amount":{"type":"number"},` +
	`"total_gross_amount":{"type":"number"},"total_tax_amount":{"type":"number"},`

const recurringSummarySchema = `{"type":"object","properties":{"id":{"type":"string"},` +
	`"version":{"type":"integer"},"created_date":{"type":"string"},"updated_date":{"type":"string"},` +
	`"title":{"type":"string"},"payment_term_label":{"type":"string"},` + recurringTotals +
	`"contact":` + voucherContactSchema + `,"settings":` + recurringSettingsSchema + `},` +
	`"required":["id"],"additionalProperties":false}`

var recurringSettingsFields = []capability.Field{
	{Name: "settings", Description: "Schedule: start_date, end_date, finalize, shipping_type, execution_interval " +
		"(WEEKLY, BIWEEKLY, MONTHLY, QUARTERLY, BIANNUALLY, ANNUALLY), next_execution_date, " +
		"last_execution_failed (the provider's error message is not shown), execution_status " +
		"(ACTIVE, PAUSED, ENDED), and retroactive_invoice when Lexware reports it"},
}

var recurringTemplatesList = capability.Descriptor{
	ID:          Provider + ".recurringtemplates.list",
	Version:     1,
	Title:       "List Lexware recurring invoice templates",
	Description: "List one page of templates for recurring invoices of an explicit Lexware Office connection",
	Tags:        []string{"lexware", "recurringtemplates", "list", "recurring", "accounting"},
	Risk:        lexwareReadRisk,
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"page":{"type":"integer","minimum":0,"maximum":200},` +
		`"size":{"type":"integer","minimum":1,"maximum":100},` +
		`"sort":{"type":"string","enum":["created_date","updated_date","next_execution_date"]},` +
		`"direction":{"type":"string","enum":["asc","desc"]}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"templates":{"type":"array","items":` + recurringSummarySchema + `},` +
		`"page":{"type":"integer"},"size":{"type":"integer"},"total_pages":{"type":"integer"},` +
		`"total_elements":{"type":"integer"},"last_page":{"type":"boolean"}},` +
		`"required":["templates","page","size","total_pages","total_elements","last_page"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "sort", Description: "Sort property: created_date, updated_date, or next_execution_date; created_date when omitted"},
		{Name: "direction", Description: "Sort direction: asc or desc; desc when omitted"},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "templates", Description: "Templates on this page, untrusted data: id, version, created_date, updated_date, " +
			"title, payment_term_label, currency, totals, contact, and settings"},
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "Read the templates whose next execution comes first",
		Arguments:   json.RawMessage(`{"page":0,"size":25,"sort":"next_execution_date","direction":"asc"}`),
	}},
}

var recurringTemplatesGet = capability.Descriptor{
	ID:          Provider + ".recurringtemplates.get",
	Version:     1,
	Title:       "Get a Lexware recurring invoice template",
	Description: "Read one template for recurring invoices of an explicit Lexware Office connection by its identifier",
	Tags:        []string{"lexware", "recurringtemplates", "get", "recurring", "accounting"},
	Risk:        lexwareReadRisk,
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":` + uuidSchema + `},` +
		`"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},` +
		`"version":{"type":"integer"},"created_date":{"type":"string"},"updated_date":{"type":"string"},` +
		`"language":{"type":"string"},"title":{"type":"string"},"introduction":{"type":"string"},` +
		`"remark":{"type":"string"},"payment_term_label":{"type":"string"},"tax_type":{"type":"string"},` +
		recurringTotals + `"contact":` + voucherContactSchema + `,"shipping":` + shippingSchema + `,` +
		`"settings":` + recurringSettingsSchema + `,"line_items":` + lineItemsSchema + `},` +
		`"required":["id","line_items"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "id", Required: true,
		Description: "Identifier of the template as a UUID, as returned by lexware.recurringtemplates.list"}},
	Fields: append([]capability.Field{
		{Name: "id", Description: "Identifier of the template"},
		{Name: "version", Description: "Version counter reported by Lexware"},
		{Name: "created_date", Description: "Creation timestamp"},
		{Name: "updated_date", Description: "Last change timestamp"},
		{Name: "language", Description: "Document language"},
		{Name: "title", Description: "Document title, untrusted data"},
		{Name: "introduction", Description: "Introductory text, untrusted data"},
		{Name: "remark", Description: "Closing text, untrusted data"},
		{Name: "payment_term_label", Description: "Payment term label, untrusted data"},
		{Name: "tax_type", Description: "Tax condition, for example net or gross"},
		{Name: "currency", Description: "Currency of the totals"},
		{Name: "total_net_amount", Description: "Total net amount"},
		{Name: "total_gross_amount", Description: "Total gross amount"},
		{Name: "total_tax_amount", Description: "Total tax amount"},
		{Name: "contact", Description: "Recipient name and address, untrusted data"},
		{Name: "shipping", Description: "Delivery or service type and period"},
		{Name: "line_items", Description: "Positions, untrusted data"},
	}, recurringSettingsFields...),
	Examples: []capability.Example{{
		Description: "Read one template by the identifier a template list reported",
		Arguments:   json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555"}`),
	}},
}

// RecurringTemplateListOptions are the controlled arguments of the template list.
type RecurringTemplateListOptions struct {
	Page      int    `json:"page"`
	Size      int    `json:"size"`
	Sort      string `json:"sort"`
	Direction string `json:"direction"`
}

func (o *RecurringTemplateListOptions) normalize() error {
	if o.Sort != "" && recurringSortProperties[o.Sort] == "" {
		return errors.New("the sort property is not supported")
	}
	if o.Direction != "" && o.Direction != "asc" && o.Direction != "desc" {
		return errors.New("the sort direction must be asc or desc")
	}
	return normalizePaging(&o.Page, &o.Size)
}

func recurringTemplateQuery(o RecurringTemplateListOptions) url.Values {
	property, order := "createdDate", "DESC"
	if p := recurringSortProperties[o.Sort]; p != "" {
		property = p
	}
	if o.Direction == "asc" {
		order = "ASC"
	}
	query := url.Values{}
	query.Set("page", strconv.Itoa(o.Page))
	query.Set("size", strconv.Itoa(o.Size))
	query.Set("sort", property+","+order)
	return query
}

func invokeRecurringTemplatesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options RecurringTemplateListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, providerError("list recurring templates", "the validated arguments could not be read")
	}
	if err := options.normalize(); err != nil {
		return nil, providerError("list recurring templates", err.Error())
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListRecurringTemplates(ctx, options)
}

func invokeRecurringTemplatesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, providerError("get recurring template", "the validated arguments could not be read")
	}
	if !validUUID(arguments.ID) {
		return nil, providerError("get recurring template", "the recurring template identifier must be a UUID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetRecurringTemplate(ctx, arguments.ID)
}

// ListRecurringTemplates reads exactly one page of recurring templates.
func (c *Client) ListRecurringTemplates(ctx context.Context, options RecurringTemplateListOptions) (*RecurringTemplateListResult, error) {
	const op = "list recurring templates"
	if err := options.normalize(); err != nil {
		return nil, providerError(op, err.Error())
	}
	var page struct {
		Content []recurringJSON `json:"content"`
		pagingJSON
	}
	if err := c.get(ctx, op, "", "/v1/recurring-templates", recurringTemplateQuery(options), &page); err != nil {
		return nil, err
	}
	templates := make([]RecurringTemplateSummary, 0, len(page.Content))
	for i := range page.Content {
		entry := &page.Content[i]
		if !validUUID(entry.ID) {
			return nil, &provider.Error{
				Class: provider.ClassInvalidResponse, Op: op,
				Message: "Lexware returned a recurring template without a usable identifier",
			}
		}
		voucher := entry.normalize()
		templates = append(templates, RecurringTemplateSummary{
			ID: voucher.ID, Version: voucher.Version, CreatedDate: voucher.CreatedDate,
			UpdatedDate: voucher.UpdatedDate, Title: voucher.Title, PaymentTermLabel: entry.PaymentTermLabel,
			Currency: voucher.Currency, TotalNetAmount: voucher.TotalNetAmount,
			TotalGrossAmount: voucher.TotalGrossAmount, TotalTaxAmount: voucher.TotalTaxAmount,
			Contact: voucher.Contact, Settings: entry.settings(),
		})
	}
	return &RecurringTemplateListResult{
		Templates: templates, Page: page.Number, Size: page.Size, TotalPages: page.TotalPages,
		TotalElements: page.TotalElements, LastPage: page.Last,
	}, nil
}

// GetRecurringTemplate reads exactly the template of a validated identifier and performs no other I/O.
func (c *Client) GetRecurringTemplate(ctx context.Context, id string) (*RecurringTemplate, error) {
	const op = "get recurring template"
	if !validUUID(id) {
		return nil, providerError(op, "the recurring template identifier must be a UUID")
	}
	var raw recurringJSON
	if err := c.get(ctx, op, resourceRecurringTemplate, "/v1/recurring-templates/"+url.PathEscape(id), nil, &raw); err != nil {
		return nil, err
	}
	if !strings.EqualFold(raw.ID, id) {
		return nil, &provider.Error{
			Class: provider.ClassInvalidResponse, Op: op,
			Message: "Lexware answered with a different recurring template than the requested one",
		}
	}
	voucher := raw.normalize()
	return &RecurringTemplate{
		ID: voucher.ID, Version: voucher.Version, CreatedDate: voucher.CreatedDate,
		UpdatedDate: voucher.UpdatedDate, Language: voucher.Language, Title: voucher.Title,
		Introduction: voucher.Introduction, Remark: voucher.Remark, PaymentTermLabel: raw.PaymentTermLabel,
		TaxType: voucher.TaxType, Currency: voucher.Currency, TotalNetAmount: voucher.TotalNetAmount,
		TotalGrossAmount: voucher.TotalGrossAmount, TotalTaxAmount: voucher.TotalTaxAmount,
		Contact: voucher.Contact, Shipping: voucher.Shipping, Settings: raw.settings(),
		LineItems: voucher.LineItems,
	}, nil
}
