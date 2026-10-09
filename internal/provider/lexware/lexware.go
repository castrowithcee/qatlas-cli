// Package lexware implements controlled access to vouchers, outgoing invoices, contacts, and articles of a
// Lexware Office organization.
//
// The provider talks to one fixed production gateway. It reads a bounded page of invoice metadata and the
// detail of one invoice selected by its validated identifier, reads bounded pages and single records of
// contacts and articles, bounded pages of the voucher list of every type and status, the payment status of
// one voucher, one bookkeeping voucher, the detail of one quotation, order confirmation, credit note,
// or delivery note, the organization profile, and capped reference lists (countries, payment conditions,
// posting categories, print layouts). It creates an invoice draft and issues a final invoice only through
// its own tool, which a connection offers solely when its tools list names it. Contact, address,
// article, voucher, and line-item content arrives from the provider and is treated as untrusted data: it is
// normalised into a stable Qatlas shape, passed through the output encoders, and never rendered or stored.
package lexware

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
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Provider is the provider name used in the configuration and as the operation namespace.
const Provider = "lexware"

// gateway is the fixed production API gateway. The provider never accepts a configured URL: a Lexware API
// key is bound to this one origin, so a second origin could only be a mistake or an exfiltration route.
const gateway = "https://api.lexware.io"

// roleAPIKey is the single secret role a Lexware credential must supply. It is used as a bearer token.
const roleAPIKey = "api-key"

// dataSensitivity classifies results as accounting data of the configured Lexware organization. It is
// deliberately provider-specific; the architecture defines no global sensitivity taxonomy.
const dataSensitivity = "lexware-invoice-data"

// The fixed voucher filters of the confirmed read-only workflow. An agent cannot widen them: the list
// operation is about open outgoing invoices, not about the whole voucher list.
const (
	fixedVoucherType   = "invoice"
	fixedVoucherStatus = "open"
	fixedArchived      = "false"
)

// Bounds of one request. The page size stays well below the documented maximum of 250, and the response
// limit bounds an invoice with many line items without inviting an unbounded read.
const (
	defaultPageSize  = 25
	maxPageSize      = 100
	maxPage          = 200
	maxResponseBytes = 1 << 20
	defaultTimeout   = 30 * time.Second
)

// minInterval spaces requests that share one API key. Lexware allows two requests per second across all
// of its endpoints, and counts them per key, so the spacing belongs to the key.
const minInterval = 500 * time.Millisecond

// limiters holds the rate-limit budget of every API key this process has used.
var limiters = ratelimit.NewRegistry(minInterval)

var lexwareReadRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: dataSensitivity,
}

var invoicesList = capability.Descriptor{
	ID:      Provider + ".invoices.list",
	Version: 1,
	Title:   "List open Lexware invoices",
	Description: "List one page of open outgoing invoices of an explicit Lexware Office connection; " +
		"Lexware also reports an invoice whose due date has passed with the transient status overdue",
	Tags:     []string{"lexware", "invoices", "list", "open", "overdue", "accounting"},
	Risk:     lexwareReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"voucher_number":{"type":"string","minLength":1,"maxLength":64,"pattern":"^[A-Za-z0-9 ._/-]+$"},` +
		`"voucher_date_from":{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"},` +
		`"voucher_date_to":{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"},` +
		`"page":{"type":"integer","minimum":0,"maximum":200},` +
		`"size":{"type":"integer","minimum":1,"maximum":100},` +
		`"sort":{"type":"string","enum":["voucher_date","voucher_number","created_date","updated_date"]},` +
		`"direction":{"type":"string","enum":["asc","desc"]}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"invoices":{"type":"array","items":{"type":"object","properties":{` +
		`"id":{"type":"string"},"voucher_number":{"type":"string"},"voucher_status":{"type":"string"},` +
		`"overdue":{"type":"boolean"},"voucher_date":{"type":"string"},"due_date":{"type":"string"},` +
		`"contact_name":{"type":"string"},"total_amount":{"type":"number"},` +
		`"open_amount":{"type":"number"},"currency":{"type":"string"}},` +
		`"required":["id","voucher_number","voucher_status","overdue","voucher_date"],` +
		`"additionalProperties":false}},` +
		`"page":{"type":"integer"},"size":{"type":"integer"},"total_pages":{"type":"integer"},` +
		`"total_invoices":{"type":"integer"},"last_page":{"type":"boolean"}},` +
		`"required":["invoices","page","size","total_pages","total_invoices","last_page"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "voucher_number", Description: "Return only the invoice carrying this invoice number"},
		{Name: "voucher_date_from", Description: "Earliest invoice date to return, as YYYY-MM-DD"},
		{Name: "voucher_date_to", Description: "Latest invoice date to return, as YYYY-MM-DD"},
		{Name: "page", Description: "Zero-based page to read, from 0 through 200"},
		{Name: "size", Description: "Invoices per page, from 1 through 100; 25 when omitted"},
		{Name: "sort", Description: "Sort property: voucher_date, voucher_number, created_date, or updated_date"},
		{Name: "direction", Description: "Sort direction asc or desc; desc when omitted"},
	},
	Fields: []capability.Field{
		{Name: "invoices", Description: "Metadata of the open and overdue invoices on this page, untrusted data"},
		{Name: "page", Description: "Zero-based index of the returned page"},
		{Name: "size", Description: "Page size the provider applied"},
		{Name: "total_pages", Description: "Number of pages the filter produces"},
		{Name: "total_invoices", Description: "Number of invoices the filter produces"},
		{Name: "last_page", Description: "True when this is the last page of the filter"},
	},
	Examples: []capability.Example{{
		Description: "Read the newest page of open and overdue invoices",
		Arguments:   json.RawMessage(`{"page":0,"size":25,"sort":"voucher_date","direction":"desc"}`),
	}},
}

var invoicesGet = capability.Descriptor{
	ID:          Provider + ".invoices.get",
	Version:     1,
	Title:       "Get a Lexware invoice",
	Description: "Read one outgoing invoice of an explicit Lexware Office connection by its identifier",
	Tags:        []string{"lexware", "invoices", "get", "accounting"},
	Risk:        lexwareReadRisk,
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","minLength":36,` +
		`"maxLength":36,"pattern":"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$"}},` +
		`"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"id":{"type":"string"},"voucher_number":{"type":"string"},"voucher_status":{"type":"string"},` +
		`"overdue":{"type":"boolean"},"voucher_date":{"type":"string"},"due_date":{"type":"string"},` +
		`"created_date":{"type":"string"},"updated_date":{"type":"string"},"archived":{"type":"boolean"},` +
		`"language":{"type":"string"},"title":{"type":"string"},"introduction":{"type":"string"},` +
		`"remark":{"type":"string"},"tax_type":{"type":"string"},"currency":{"type":"string"},` +
		`"total_net_amount":{"type":"number"},"total_gross_amount":{"type":"number"},` +
		`"total_tax_amount":{"type":"number"},` +
		`"contact":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"supplement":{"type":"string"},"street":{"type":"string"},"zip":{"type":"string"},` +
		`"city":{"type":"string"},"country_code":{"type":"string"}},"additionalProperties":false},` +
		`"line_items":{"type":"array","items":{"type":"object","properties":{` +
		`"type":{"type":"string"},"name":{"type":"string"},"description":{"type":"string"},` +
		`"quantity":{"type":"number"},"unit_name":{"type":"string"},` +
		`"unit_net_amount":{"type":"number"},"unit_gross_amount":{"type":"number"},` +
		`"tax_rate_percentage":{"type":"number"},"discount_percentage":{"type":"number"},` +
		`"amount":{"type":"number"}},"additionalProperties":false}}},` +
		`"required":["id","voucher_number","voucher_status","overdue","voucher_date","archived","line_items"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "id", Description: "Invoice identifier as a UUID, as returned by lexware.invoices.list", Required: true},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Invoice identifier"},
		{Name: "voucher_number", Description: "Invoice number"},
		{Name: "voucher_status", Description: "Provider status, for example open or overdue"},
		{Name: "overdue", Description: "True when the provider reports the transient status overdue"},
		{Name: "voucher_date", Description: "Invoice date"},
		{Name: "due_date", Description: "Date the payment is due"},
		{Name: "created_date", Description: "Creation timestamp"},
		{Name: "updated_date", Description: "Last change timestamp"},
		{Name: "archived", Description: "True when the invoice is archived in Lexware"},
		{Name: "language", Description: "Document language"},
		{Name: "title", Description: "Document title, untrusted data"},
		{Name: "introduction", Description: "Introductory text, untrusted data"},
		{Name: "remark", Description: "Closing text, untrusted data"},
		{Name: "tax_type", Description: "Tax condition of the invoice, for example net or gross"},
		{Name: "currency", Description: "Currency of the totals"},
		{Name: "total_net_amount", Description: "Total net amount"},
		{Name: "total_gross_amount", Description: "Total gross amount"},
		{Name: "total_tax_amount", Description: "Total tax amount"},
		{Name: "contact", Description: "Recipient name and address, untrusted data"},
		{Name: "line_items", Description: "Invoice positions, untrusted data"},
	},
	Examples: []capability.Example{{
		Description: "Read one invoice by the identifier a list result reported",
		Arguments:   json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555"}`),
	}},
}

// invoiceWriteRisk is shared by both invoice creations: each sends one non-repeatable request to the
// production organization.
var invoiceWriteRisk = capability.Risk{
	Effect:          capability.EffectCreate,
	Idempotency:     capability.IdempotencyNonIdempotent,
	Confirmation:    capability.ConfirmationRequired,
	OpenWorld:       true,
	DataSensitivity: dataSensitivity,
}

// invoiceInputSchema is the closed input of a draft and of an issued invoice. It has no finalize member:
// whether an invoice is final follows from the operation, never from an argument.
const invoiceInputSchema = `{"type":"object","properties":{` +
	`"voucher_date":{"type":"string","minLength":1,"maxLength":40},` +
	`"address":{"type":"object","properties":{"contact_id":{"type":"string","maxLength":36},"name":{"type":"string","maxLength":255},"supplement":{"type":"string","maxLength":255},"street":{"type":"string","maxLength":255},"city":{"type":"string","maxLength":255},"zip":{"type":"string","maxLength":32},"country_code":{"type":"string","minLength":2,"maxLength":2}},"additionalProperties":false},` +
	`"line_items":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"object","properties":{"type":{"type":"string","enum":["custom","text"]},"name":{"type":"string","minLength":1,"maxLength":255},"description":{"type":"string","maxLength":4096},"quantity":{"type":"number"},"unit_name":{"type":"string","maxLength":64},"currency":{"type":"string","minLength":3,"maxLength":3},"net_amount":{"type":"number"},"tax_rate_percentage":{"type":"number"},"discount_percentage":{"type":"number"}},"required":["type","name"],"additionalProperties":false}},` +
	`"currency":{"type":"string","minLength":3,"maxLength":3},"tax_type":{"type":"string","enum":["net","gross","vatfree"]},` +
	`"shipping_date":{"type":"string","minLength":1,"maxLength":40},"shipping_end_date":{"type":"string","minLength":1,"maxLength":40},"shipping_type":{"type":"string","enum":["delivery","deliveryperiod","service","serviceperiod","none"]},` +
	`"title":{"type":"string","maxLength":255},"introduction":{"type":"string","maxLength":4096},"remark":{"type":"string","maxLength":4096}},` +
	`"required":["voucher_date","address","line_items","currency","tax_type","shipping_type"],"additionalProperties":false}`

// invoiceOutputSchema is the result of both creations: the identifier and the timestamps Lexware reports.
const invoiceOutputSchema = `{"type":"object","properties":{"id":{"type":"string"},"created_date":{"type":"string"},"updated_date":{"type":"string"},"version":{"type":"integer"}},"required":["id"],"additionalProperties":false}`

var invoiceArguments = []capability.Argument{
	{Name: "voucher_date", Description: "Invoice date as an RFC 3339 timestamp", Required: true},
	{Name: "address", Description: "Recipient address object; contact_id references an existing contact as a UUID, otherwise name and country_code are required and supplement, street, zip and city complete it", Required: true},
	{Name: "line_items", Description: "1 to 100 invoice positions with type custom or text and a name; custom positions add quantity, unit_name, net_amount and tax_rate_percentage, and optionally currency and discount_percentage", Required: true},
	{Name: "currency", Description: "ISO 4217 currency code of 3 characters", Required: true},
	{Name: "tax_type", Description: "How amounts are taxed: net, gross or vatfree", Required: true},
	{Name: "shipping_type", Description: "Kind of delivery: delivery, deliveryperiod, service, serviceperiod or none", Required: true},
	{Name: "shipping_date", Description: "Delivery or service date as an RFC 3339 timestamp; required unless shipping_type is none"},
	{Name: "shipping_end_date", Description: "End of the delivery or service period as an RFC 3339 timestamp; required for deliveryperiod and serviceperiod"},
	{Name: "title", Description: "Invoice title, up to 255 characters"},
	{Name: "introduction", Description: "Introductory text above the positions, up to 4096 characters"},
	{Name: "remark", Description: "Closing remark below the positions, up to 4096 characters"},
}

var invoiceFields = []capability.Field{
	{Name: "id", Description: "Identifier Lexware assigned to the new invoice"},
	{Name: "created_date", Description: "Creation timestamp reported by Lexware"},
	{Name: "updated_date", Description: "Last change timestamp reported by Lexware"},
	{Name: "version", Description: "Version counter reported by Lexware"},
}

var invoiceExamples = []capability.Example{{
	Description: "One service position for an existing contact, delivered on one day",
	Arguments: json.RawMessage(`{"voucher_date":"2026-09-12T00:00:00+02:00",` +
		`"address":{"contact_id":"11111111-2222-3333-4444-555555555555"},` +
		`"line_items":[{"type":"custom","name":"Consulting","quantity":2,"unit_name":"hour",` +
		`"net_amount":120,"tax_rate_percentage":19}],` +
		`"currency":"EUR","tax_type":"net","shipping_type":"service",` +
		`"shipping_date":"2026-09-12T00:00:00+02:00"}`),
}}

var invoicesCreate = capability.Descriptor{
	ID: Provider + ".invoices.create", Version: 2, Title: "Create a Lexware invoice draft",
	Description: "Create one outgoing invoice as a draft without invoice number; a draft can still be " +
		"changed in Lexware Office. Issuing the invoice is a separate tool",
	Tags:     []string{"lexware", "invoices", "create", "draft", "accounting"},
	Provider: Provider, Risk: invoiceWriteRisk,
	InputSchema:  json.RawMessage(invoiceInputSchema),
	OutputSchema: json.RawMessage(invoiceOutputSchema),
	Arguments:    invoiceArguments, Fields: invoiceFields, Examples: invoiceExamples,
}

var invoicesIssue = capability.Descriptor{
	ID: Provider + ".invoices.issue", Version: 1, Title: "Issue a Lexware invoice",
	Description: "Create one outgoing invoice and finalize it immediately: Lexware assigns the invoice " +
		"number, and the API can neither change nor delete the invoice afterwards. Offered only by a " +
		"connection whose tools list names it",
	Tags:     []string{"lexware", "invoices", "issue", "finalize", "accounting"},
	Provider: Provider, Risk: invoiceWriteRisk, RequiresToolAllowList: true,
	InputSchema:  json.RawMessage(invoiceInputSchema),
	OutputSchema: json.RawMessage(invoiceOutputSchema),
	Arguments:    invoiceArguments, Fields: invoiceFields, Examples: invoiceExamples,
}

// Register adds Lexware metadata, its read-only connection test, and the supported operations.
func Register(reg *capability.Registry) error {
	readTools := []string{invoicesList.ID, invoicesGet.ID, contactsList.ID, contactsGet.ID,
		articlesList.ID, articlesGet.ID, voucherlistList.ID, paymentsGet.ID, vouchersGet.ID,
		quotationsGet.ID, orderConfirmationsGet.ID, creditNotesGet.ID, deliveryNotesGet.ID,
		profileGet.ID, countriesList.ID, paymentConditionsList.ID, postingCategoriesList.ID, printLayoutsList.ID}
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Lexware Office", DefaultBaseURL: gateway,
		Description:        "Online accounting and invoicing service for small businesses",
		DefaultPermissions: []config.Permission{config.PermissionRead},
		SecretRoles: []config.SecretRole{{
			Name: roleAPIKey,
			Description: "Lexware private API key: the value shown once when you create a key under " +
				"Public API in Lexware Office; it is sent as the bearer token",
		}},
		Target: config.TargetMetadata{
			Label: "organization",
			Description: "optionally one organization/ORGANIZATION_ID target; without it the organization " +
				"follows from the API key, with it a key of another organization is refused before any request",
			Kinds: []config.TargetKind{{
				Name:        "organization",
				Description: "the Lexware organization the API key must belong to; optional, at most one",
				Forms:       []string{"organization/ORGANIZATION_ID"},
			}},
			Validate: validateOrganizationTarget,
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read invoices, vouchers, contacts, articles and reference data", Recommended: true,
			Description: "lists and reads invoices, vouchers, payment status, contacts, articles, the organization profile and reference data; creates " +
				"nothing in Lexware Office",
			Tools: readTools,
		}, {
			ID: "write", Title: "Master data and drafts",
			Description: "lists and reads invoices, vouchers, payment status, contacts, articles, the organization profile and reference data and " +
				"creates invoice drafts; issuing an invoice is never part of a profile",
			Tools: append(append([]string{}, readTools...), invoicesCreate.ID),
		}},
	}, TestConnection); err != nil {
		return err
	}
	if err := reg.RegisterTargetSuggester(Provider, SuggestTarget); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: invoicesList, Handler: capability.Handler(invokeInvoicesList)},
		capability.Operation{Descriptor: invoicesGet, Handler: capability.Handler(invokeInvoicesGet)},
		capability.Operation{Descriptor: invoicesCreate, Handler: capability.Handler(invokeInvoicesCreate)},
		capability.Operation{Descriptor: invoicesIssue, Handler: capability.Handler(invokeInvoicesIssue)},
		capability.Operation{Descriptor: contactsList, Handler: capability.Handler(invokeContactsList)},
		capability.Operation{Descriptor: contactsGet, Handler: capability.Handler(invokeContactsGet)},
		capability.Operation{Descriptor: articlesList, Handler: capability.Handler(invokeArticlesList)},
		capability.Operation{Descriptor: articlesGet, Handler: capability.Handler(invokeArticlesGet)},
		capability.Operation{Descriptor: voucherlistList, Handler: capability.Handler(invokeVoucherlistList)},
		capability.Operation{Descriptor: paymentsGet, Handler: capability.Handler(invokePaymentsGet)},
		capability.Operation{Descriptor: vouchersGet, Handler: capability.Handler(invokeVouchersGet)},
		capability.Operation{Descriptor: quotationsGet, Handler: salesVoucherHandler(quotationKind)},
		capability.Operation{Descriptor: orderConfirmationsGet, Handler: salesVoucherHandler(orderConfirmationKind)},
		capability.Operation{Descriptor: creditNotesGet, Handler: salesVoucherHandler(creditNoteKind)},
		capability.Operation{Descriptor: deliveryNotesGet, Handler: salesVoucherHandler(deliveryNoteKind)},
		capability.Operation{Descriptor: profileGet, Handler: capability.Handler(invokeProfileGet)},
		capability.Operation{Descriptor: countriesList, Handler: capability.Handler(invokeCountriesList)},
		capability.Operation{Descriptor: paymentConditionsList, Handler: capability.Handler(invokePaymentConditionsList)},
		capability.Operation{Descriptor: postingCategoriesList, Handler: capability.Handler(invokePostingCategoriesList)},
		capability.Operation{Descriptor: printLayoutsList, Handler: capability.Handler(invokePrintLayoutsList)},
	)
}

func invokeInvoicesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options ListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, providerError("list invoices", "the validated arguments could not be read")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListInvoices(ctx, options)
}

func invokeInvoicesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, providerError("get invoice", "the validated arguments could not be read")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetInvoice(ctx, arguments.ID)
}

type createInput struct {
	VoucherDate string `json:"voucher_date"`
	Address     struct {
		ContactID   string `json:"contact_id,omitempty"`
		Name        string `json:"name,omitempty"`
		Supplement  string `json:"supplement,omitempty"`
		Street      string `json:"street,omitempty"`
		City        string `json:"city,omitempty"`
		Zip         string `json:"zip,omitempty"`
		CountryCode string `json:"country_code,omitempty"`
	} `json:"address"`
	LineItems []struct {
		Type        string      `json:"type"`
		Name        string      `json:"name"`
		Description string      `json:"description,omitempty"`
		Quantity    json.Number `json:"quantity,omitempty"`
		UnitName    string      `json:"unit_name,omitempty"`
		Currency    string      `json:"currency,omitempty"`
		NetAmount   json.Number `json:"net_amount,omitempty"`
		TaxRate     json.Number `json:"tax_rate_percentage,omitempty"`
		Discount    json.Number `json:"discount_percentage,omitempty"`
	} `json:"line_items"`
	Currency        string `json:"currency"`
	TaxType         string `json:"tax_type"`
	ShippingDate    string `json:"shipping_date"`
	ShippingEndDate string `json:"shipping_end_date"`
	ShippingType    string `json:"shipping_type"`
	Title           string `json:"title,omitempty"`
	Introduction    string `json:"introduction,omitempty"`
	Remark          string `json:"remark,omitempty"`
}

func invokeInvoicesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeInvoicePost(ctx, resolved, secrets, red, raw, false)
}

func invokeInvoicesIssue(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeInvoicePost(ctx, resolved, secrets, red, raw, true)
}

// invokeInvoicePost validates the arguments, including every foreign identifier, before the credential is
// resolved or any provider I/O happens.
func invokeInvoicePost(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage, finalize bool) (any, error) {
	op := "create invoice"
	if finalize {
		op = "issue invoice"
	}
	var input createInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&input) != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := input.validate(); err != nil {
		return nil, providerError(op, err.Error())
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if finalize {
		return client.IssueInvoice(ctx, input)
	}
	return client.CreateInvoice(ctx, input)
}

func (input createInput) validate() error {
	if _, err := time.Parse(time.RFC3339, input.VoucherDate); err != nil {
		return errors.New("voucher_date must be an RFC 3339 timestamp")
	}
	if input.Address.ContactID != "" && !validUUID(input.Address.ContactID) {
		return errors.New("address.contact_id must be a UUID")
	}
	if input.Address.ContactID == "" && (input.Address.Name == "" || input.Address.CountryCode == "") {
		return errors.New("address needs contact_id or name and country_code")
	}
	if len(input.LineItems) == 0 {
		return errors.New("at least one line item is required")
	}
	for _, item := range input.LineItems {
		if item.Type == "custom" && (item.Quantity == "" || item.UnitName == "" || item.NetAmount == "" || item.TaxRate == "") {
			return errors.New("a custom line item needs quantity, unit_name, net_amount, and tax_rate_percentage")
		}
	}
	if input.ShippingType != "none" {
		if _, err := time.Parse(time.RFC3339, input.ShippingDate); err != nil {
			return errors.New("shipping_date must be an RFC 3339 timestamp")
		}
	}
	if strings.HasSuffix(input.ShippingType, "period") {
		if _, err := time.Parse(time.RFC3339, input.ShippingEndDate); err != nil {
			return errors.New("shipping_end_date is required for a shipping period")
		}
	}
	return nil
}

// Client binds one Lexware API key to the fixed gateway and to the rate limit that key shares.
type Client struct {
	auth    string
	http    *http.Client
	limiter *ratelimit.Limiter
	// binding is nil for a connection without an organization target.
	binding *binding
}

// Open resolves the API key of one selected connection and returns a client for the fixed gateway.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return open(ctx, resolved, secrets, red, nil)
}

// open is the internal seam. A caller may supply the rate limiter, and the package's own tests replace
// the transport, so no test ever reaches a productive Lexware organization.
func open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	lim *ratelimit.Limiter) (*Client, error) {
	if resolved == nil {
		return nil, providerError("open", "no connection was selected")
	}
	if !isGateway(resolved.BaseURL) {
		return nil, providerError("open",
			"a Lexware service must use the fixed API gateway "+gateway)
	}
	want, err := boundOrganization(resolved)
	if err != nil {
		return nil, err
	}
	if secrets == nil {
		return nil, providerError("open", "no credential resolver was configured")
	}
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, roleAPIKey)
	if err != nil {
		return nil, err
	}
	if !validAPIKey(value.Secret) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: "open", Message: "the Lexware API key is unusable"}
	}
	if red != nil {
		red.Add(value.Secret, "Bearer "+value.Secret)
	}
	if lim == nil {
		lim = limiters.For(value.Secret)
	}
	c := &Client{auth: "Bearer " + value.Secret, http: provider.NoRedirectClient(defaultTimeout, transport), limiter: lim}
	if want != "" {
		c.binding = &binding{want: want, entry: organizationEntryFor(resolved.Name, value.Secret)}
	}
	return c, nil
}

// isGateway reports whether a configured base URL names the fixed production gateway. A trailing slash is
// the only variation a configuration file may carry.
func isGateway(raw string) bool {
	return strings.TrimRight(strings.TrimSpace(raw), "/") == gateway
}

// transport carries every Lexware request. A nil value is Go's default transport; the package's own tests
// replace it with recorded responses.
var transport http.RoundTripper

// TestConnection performs the smallest authenticated read of the confirmed workflow and reports the
// stable outcome class. It reads one invoice metadata record and nothing else.
func TestConnection(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (provider.Class, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return "", err
	}
	return client.testConnection(ctx), nil
}

func (c *Client) testConnection(ctx context.Context) provider.Class {
	var page voucherListJSON
	if err := c.get(ctx, "test connection", "", "/v1/voucherlist", listQuery(ListOptions{Size: 1}), &page); err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class
		}
		return provider.ClassProviderError
	}
	return provider.ClassOK
}

// ListOptions are the controlled filters of the list operation. The voucher type, the voucher status, and
// the archived flag are not part of it: they are fixed by the provider.
type ListOptions struct {
	VoucherNumber   string `json:"voucher_number"`
	VoucherDateFrom string `json:"voucher_date_from"`
	VoucherDateTo   string `json:"voucher_date_to"`
	Page            int    `json:"page"`
	Size            int    `json:"size"`
	Sort            string `json:"sort"`
	Direction       string `json:"direction"`
}

// sortProperties maps the stable Qatlas sort names to the provider's property names. An unknown name
// never reaches the provider.
var sortProperties = map[string]string{
	"voucher_date":   "voucherDate",
	"voucher_number": "voucherNumber",
	"created_date":   "createdDate",
	"updated_date":   "updatedDate",
}

// listQuery builds the complete query of one list request. Exactly the fixed filters, the requested page,
// and the controlled filters appear in it.
func listQuery(options ListOptions) url.Values {
	query := url.Values{}
	query.Set("voucherType", fixedVoucherType)
	query.Set("voucherStatus", fixedVoucherStatus)
	query.Set("archived", fixedArchived)
	query.Set("page", strconv.Itoa(options.Page))
	query.Set("size", strconv.Itoa(options.Size))

	setSort(query, options.Sort, options.Direction)
	setFilter(query, "voucherNumber", options.VoucherNumber)
	setFilter(query, "voucherDateFrom", options.VoucherDateFrom)
	setFilter(query, "voucherDateTo", options.VoucherDateTo)
	return query
}

// setSort sets the sort parameter from a stable Qatlas sort name and direction; voucher_date, descending
// is the default.
func setSort(query url.Values, sort, direction string) {
	property := sortProperties[sort]
	if property == "" {
		property = sortProperties["voucher_date"]
	}
	order := "DESC"
	if direction == "asc" {
		order = "ASC"
	}
	query.Set("sort", property+","+order)
}

// setFilter sets one optional filter parameter only when it has a value.
func setFilter(query url.Values, key, value string) {
	if value != "" {
		query.Set(key, value)
	}
}

// validateListFilters checks the sort and filter members every voucher list shares.
func validateListFilters(sort, direction, number string, dates ...string) error {
	if sort != "" && sortProperties[sort] == "" {
		return errors.New("the sort property is not supported")
	}
	if direction != "" && direction != "asc" && direction != "desc" {
		return errors.New("the sort direction must be asc or desc")
	}
	for _, date := range dates {
		if date == "" {
			continue
		}
		if _, err := time.Parse(time.DateOnly, date); err != nil {
			return errors.New("a voucher date filter must use the format YYYY-MM-DD")
		}
	}
	if len(number) > 64 {
		return errors.New("the voucher number filter is too long")
	}
	return nil
}

// ListResult is the normalised page of invoice metadata.
type ListResult struct {
	Invoices      []ListInvoice `json:"invoices"`
	Page          int           `json:"page"`
	Size          int           `json:"size"`
	TotalPages    int           `json:"total_pages"`
	TotalInvoices int           `json:"total_invoices"`
	LastPage      bool          `json:"last_page"`
}

// ListInvoice is the stable Qatlas view of one voucher list record. An amount Lexware does not report
// stays absent instead of being invented.
type ListInvoice struct {
	ID            string      `json:"id"`
	VoucherNumber string      `json:"voucher_number"`
	VoucherStatus string      `json:"voucher_status"`
	Overdue       bool        `json:"overdue"`
	VoucherDate   string      `json:"voucher_date"`
	DueDate       string      `json:"due_date,omitempty"`
	ContactName   string      `json:"contact_name,omitempty"`
	TotalAmount   json.Number `json:"total_amount,omitempty"`
	OpenAmount    json.Number `json:"open_amount,omitempty"`
	Currency      string      `json:"currency,omitempty"`
}

// ListInvoices reads exactly one page of open outgoing invoices. Lexware answers the fixed status filter
// with open invoices and, for an invoice whose due date has passed, with the transient status overdue;
// both belong to the workflow and are kept, with overdue made explicit.
func (c *Client) ListInvoices(ctx context.Context, options ListOptions) (*ListResult, error) {
	const op = "list invoices"
	if err := options.normalize(); err != nil {
		return nil, providerError(op, err.Error())
	}

	var page voucherListJSON
	if err := c.get(ctx, op, "", "/v1/voucherlist", listQuery(options), &page); err != nil {
		return nil, err
	}

	invoices := make([]ListInvoice, 0, len(page.Content))
	for _, entry := range page.Content {
		// The filters are the provider's job, but its answer is untrusted: a record outside the
		// confirmed workflow is dropped rather than reported as an open outgoing invoice.
		if entry.VoucherType != fixedVoucherType || entry.Archived {
			continue
		}
		if !validUUID(entry.ID) || entry.VoucherNumber == "" {
			return nil, &provider.Error{
				Class: provider.ClassInvalidResponse, Op: op,
				Message: "Lexware returned an invoice without a usable identifier",
			}
		}
		invoices = append(invoices, ListInvoice{
			ID: entry.ID, VoucherNumber: entry.VoucherNumber, VoucherStatus: entry.VoucherStatus,
			Overdue: entry.VoucherStatus == "overdue", VoucherDate: entry.VoucherDate,
			DueDate: entry.DueDate, ContactName: entry.ContactName, TotalAmount: entry.TotalAmount,
			OpenAmount: entry.OpenAmount, Currency: entry.Currency,
		})
	}

	return &ListResult{
		Invoices: invoices, Page: page.Number, Size: page.Size, TotalPages: page.TotalPages,
		TotalInvoices: page.TotalElements, LastPage: page.Last,
	}, nil
}

// normalize applies the defaults and the bounds of one list request. The application core validates the
// same rules against the input schema first; this keeps a direct caller inside them as well.
func (o *ListOptions) normalize() error {
	if err := normalizePaging(&o.Page, &o.Size); err != nil {
		return err
	}
	return validateListFilters(o.Sort, o.Direction, o.VoucherNumber, o.VoucherDateFrom, o.VoucherDateTo)
}

// normalizePaging applies the default page size and checks the page bounds every list operation shares.
func normalizePaging(page, size *int) error {
	if *size == 0 {
		*size = defaultPageSize
	}
	if *size < 1 || *size > maxPageSize {
		return fmt.Errorf("the page size must be between 1 and %d", maxPageSize)
	}
	if *page < 0 || *page > maxPage {
		return fmt.Errorf("the page must be between 0 and %d", maxPage)
	}
	return nil
}

// pagingJSON mirrors the page members of a Lexware page object; a list response embeds it.
type pagingJSON struct {
	Number        int  `json:"number"`
	Size          int  `json:"size"`
	TotalPages    int  `json:"totalPages"`
	TotalElements int  `json:"totalElements"`
	Last          bool `json:"last"`
}

// pagingArguments and pagingFields are the descriptor entries of the shared page members.
var pagingArguments = []capability.Argument{
	{Name: "page", Description: "Zero-based page to read, from 0 through 200"},
	{Name: "size", Description: "Entries per page, from 1 through 100; 25 when omitted"},
}

var pagingFields = []capability.Field{
	{Name: "page", Description: "Zero-based index of the returned page"},
	{Name: "size", Description: "Page size the provider applied"},
	{Name: "total_pages", Description: "Number of pages the filter produces"},
	{Name: "total_elements", Description: "Number of entries the filter produces"},
	{Name: "last_page", Description: "True when this is the last page of the filter"},
}

// Invoice is the stable Qatlas view of one invoice. Everything a Lexware user typed is untrusted data.
type Invoice struct {
	ID               string      `json:"id"`
	VoucherNumber    string      `json:"voucher_number"`
	VoucherStatus    string      `json:"voucher_status"`
	Overdue          bool        `json:"overdue"`
	VoucherDate      string      `json:"voucher_date"`
	DueDate          string      `json:"due_date,omitempty"`
	CreatedDate      string      `json:"created_date,omitempty"`
	UpdatedDate      string      `json:"updated_date,omitempty"`
	Archived         bool        `json:"archived"`
	Language         string      `json:"language,omitempty"`
	Title            string      `json:"title,omitempty"`
	Introduction     string      `json:"introduction,omitempty"`
	Remark           string      `json:"remark,omitempty"`
	TaxType          string      `json:"tax_type,omitempty"`
	Currency         string      `json:"currency,omitempty"`
	TotalNetAmount   json.Number `json:"total_net_amount,omitempty"`
	TotalGrossAmount json.Number `json:"total_gross_amount,omitempty"`
	TotalTaxAmount   json.Number `json:"total_tax_amount,omitempty"`
	Contact          *Contact    `json:"contact,omitempty"`
	LineItems        []LineItem  `json:"line_items"`
}

// Contact is the invoice recipient as the invoice carries it.
type Contact struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Supplement  string `json:"supplement,omitempty"`
	Street      string `json:"street,omitempty"`
	Zip         string `json:"zip,omitempty"`
	City        string `json:"city,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
}

// LineItem is one invoice position with its unit price flattened into stable field names.
type LineItem struct {
	Type               string      `json:"type,omitempty"`
	Name               string      `json:"name,omitempty"`
	Description        string      `json:"description,omitempty"`
	Quantity           json.Number `json:"quantity,omitempty"`
	UnitName           string      `json:"unit_name,omitempty"`
	UnitNetAmount      json.Number `json:"unit_net_amount,omitempty"`
	UnitGrossAmount    json.Number `json:"unit_gross_amount,omitempty"`
	TaxRatePercentage  json.Number `json:"tax_rate_percentage,omitempty"`
	DiscountPercentage json.Number `json:"discount_percentage,omitempty"`
	Amount             json.Number `json:"amount,omitempty"`
}

// GetInvoice reads exactly the invoice of a validated identifier and performs no other provider I/O.
func (c *Client) GetInvoice(ctx context.Context, id string) (*Invoice, error) {
	const op = "get invoice"
	if !validUUID(id) {
		return nil, providerError(op, "the invoice identifier must be a UUID")
	}

	var raw invoiceJSON
	if err := c.get(ctx, op, resourceInvoice, "/v1/invoices/"+url.PathEscape(id), nil, &raw); err != nil {
		return nil, err
	}
	if !strings.EqualFold(raw.ID, id) {
		return nil, &provider.Error{
			Class: provider.ClassInvalidResponse, Op: op,
			Message: "Lexware answered with a different invoice than the requested one",
		}
	}

	voucher := raw.normalize()
	return &Invoice{
		ID: voucher.ID, VoucherNumber: voucher.VoucherNumber, VoucherStatus: voucher.VoucherStatus,
		Overdue: voucher.VoucherStatus == "overdue", VoucherDate: voucher.VoucherDate, DueDate: raw.DueDate,
		CreatedDate: voucher.CreatedDate, UpdatedDate: voucher.UpdatedDate, Archived: voucher.Archived,
		Language: voucher.Language, Title: voucher.Title, Introduction: voucher.Introduction,
		Remark: voucher.Remark, TaxType: voucher.TaxType, Currency: voucher.Currency,
		TotalNetAmount: voucher.TotalNetAmount, TotalGrossAmount: voucher.TotalGrossAmount,
		TotalTaxAmount: voucher.TotalTaxAmount, Contact: voucher.Contact, LineItems: voucher.LineItems,
	}, nil
}

type createResult struct {
	ID          string `json:"id"`
	CreatedDate string `json:"created_date,omitempty"`
	UpdatedDate string `json:"updated_date,omitempty"`
	Version     int    `json:"version,omitempty"`
}

// CreateInvoice posts one draft: the request carries no finalize query.
func (c *Client) CreateInvoice(ctx context.Context, input createInput) (*createResult, error) {
	return c.postInvoice(ctx, "create invoice", input, false)
}

// IssueInvoice posts one invoice with the fixed query finalize=true. Lexware then assigns the invoice
// number, and the API can no longer change or delete the invoice.
func (c *Client) IssueInvoice(ctx context.Context, input createInput) (*createResult, error) {
	return c.postInvoice(ctx, "issue invoice", input, true)
}

func (c *Client) postInvoice(ctx context.Context, op string, input createInput, finalize bool) (*createResult, error) {
	uncertain := invoiceMayExist
	if finalize {
		uncertain = invoiceMayBeIssued
	}
	address := map[string]any{"name": input.Address.Name, "supplement": input.Address.Supplement, "street": input.Address.Street, "city": input.Address.City, "zip": input.Address.Zip, "countryCode": input.Address.CountryCode}
	if input.Address.ContactID != "" {
		address = map[string]any{"contactId": input.Address.ContactID}
	}
	items := make([]map[string]any, 0, len(input.LineItems))
	for _, item := range input.LineItems {
		value := map[string]any{"type": item.Type, "name": item.Name}
		if item.Description != "" {
			value["description"] = item.Description
		}
		if item.Type == "custom" {
			currency := item.Currency
			if currency == "" {
				currency = input.Currency
			}
			value["quantity"] = item.Quantity
			value["unitName"] = item.UnitName
			value["unitPrice"] = map[string]any{"currency": currency, "netAmount": item.NetAmount, "taxRatePercentage": item.TaxRate}
			if item.Discount != "" {
				value["discountPercentage"] = item.Discount
			}
		}
		items = append(items, value)
	}
	shipping := map[string]string{"shippingType": input.ShippingType}
	if input.ShippingDate != "" {
		shipping["shippingDate"] = input.ShippingDate
	}
	if input.ShippingEndDate != "" {
		shipping["shippingEndDate"] = input.ShippingEndDate
	}
	payload := map[string]any{"voucherDate": input.VoucherDate, "address": address, "lineItems": items, "totalPrice": map[string]string{"currency": input.Currency}, "taxConditions": map[string]string{"taxType": input.TaxType}, "shippingConditions": shipping, "title": input.Title, "introduction": input.Introduction, "remark": input.Remark}
	for _, field := range []struct{ name, value string }{{"title", input.Title}, {"introduction", input.Introduction}, {"remark", input.Remark}} {
		if field.value == "" {
			delete(payload, field.name)
		}
	}
	query := url.Values{}
	if finalize {
		query.Set("finalize", "true")
	}
	var response struct {
		ID          string `json:"id"`
		CreatedDate string `json:"createdDate"`
		UpdatedDate string `json:"updatedDate"`
		Version     int    `json:"version"`
	}
	if err := c.post(ctx, op, "", "/v1/invoices", query, payload, &response, uncertain); err != nil {
		return nil, err
	}
	if !validUUID(response.ID) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "Lexware returned an invoice without a usable identifier" + uncertain}
	}
	return &createResult{ID: response.ID, CreatedDate: response.CreatedDate, UpdatedDate: response.UpdatedDate, Version: response.Version}, nil
}

// voucherListJSON mirrors the provider fields the list operation reads.
type voucherListJSON struct {
	Content []struct {
		ID            string      `json:"id"`
		VoucherType   string      `json:"voucherType"`
		VoucherStatus string      `json:"voucherStatus"`
		VoucherNumber string      `json:"voucherNumber"`
		VoucherDate   string      `json:"voucherDate"`
		DueDate       string      `json:"dueDate"`
		ContactName   string      `json:"contactName"`
		TotalAmount   json.Number `json:"totalAmount"`
		OpenAmount    json.Number `json:"openAmount"`
		Currency      string      `json:"currency"`
		Archived      bool        `json:"archived"`
	} `json:"content"`
	Number        int  `json:"number"`
	Size          int  `json:"size"`
	TotalPages    int  `json:"totalPages"`
	TotalElements int  `json:"totalElements"`
	Last          bool `json:"last"`
}

// invoiceJSON mirrors the provider fields of a sales voucher the get operations read. Invoices, quotations,
// order confirmations, credit notes, and delivery notes share this structure; a member a type does not
// carry stays empty.
type invoiceJSON struct {
	ID             string `json:"id"`
	CreatedDate    string `json:"createdDate"`
	UpdatedDate    string `json:"updatedDate"`
	Version        int    `json:"version"`
	Language       string `json:"language"`
	Archived       bool   `json:"archived"`
	VoucherStatus  string `json:"voucherStatus"`
	VoucherNumber  string `json:"voucherNumber"`
	VoucherDate    string `json:"voucherDate"`
	DueDate        string `json:"dueDate"`
	ExpirationDate string `json:"expirationDate"`
	DeliveryTerms  string `json:"deliveryTerms"`
	Address        struct {
		ContactID   string `json:"contactId"`
		Name        string `json:"name"`
		Supplement  string `json:"supplement"`
		Street      string `json:"street"`
		City        string `json:"city"`
		Zip         string `json:"zip"`
		CountryCode string `json:"countryCode"`
	} `json:"address"`
	LineItems []struct {
		Type        string      `json:"type"`
		Name        string      `json:"name"`
		Description string      `json:"description"`
		Quantity    json.Number `json:"quantity"`
		UnitName    string      `json:"unitName"`
		UnitPrice   struct {
			Currency          string      `json:"currency"`
			NetAmount         json.Number `json:"netAmount"`
			GrossAmount       json.Number `json:"grossAmount"`
			TaxRatePercentage json.Number `json:"taxRatePercentage"`
		} `json:"unitPrice"`
		DiscountPercentage json.Number `json:"discountPercentage"`
		LineItemAmount     json.Number `json:"lineItemAmount"`
	} `json:"lineItems"`
	TotalPrice struct {
		Currency         string      `json:"currency"`
		TotalNetAmount   json.Number `json:"totalNetAmount"`
		TotalGrossAmount json.Number `json:"totalGrossAmount"`
		TotalTaxAmount   json.Number `json:"totalTaxAmount"`
	} `json:"totalPrice"`
	TaxConditions struct {
		TaxType string `json:"taxType"`
	} `json:"taxConditions"`
	ShippingConditions struct {
		ShippingType    string `json:"shippingType"`
		ShippingDate    string `json:"shippingDate"`
		ShippingEndDate string `json:"shippingEndDate"`
	} `json:"shippingConditions"`
	RelatedVouchers []struct {
		ID            string `json:"id"`
		VoucherNumber string `json:"voucherNumber"`
		VoucherType   string `json:"voucherType"`
	} `json:"relatedVouchers"`
	Title        string `json:"title"`
	Introduction string `json:"introduction"`
	Remark       string `json:"remark"`
}

// normalize is the shared mapper of every sales voucher type: it flattens the provider shape into the stable
// Qatlas view. GetInvoice derives its unchanged output from it.
func (raw *invoiceJSON) normalize() *SalesVoucher {
	voucher := &SalesVoucher{
		ID: raw.ID, VoucherNumber: raw.VoucherNumber, VoucherStatus: raw.VoucherStatus,
		VoucherDate: raw.VoucherDate, ExpirationDate: raw.ExpirationDate, DeliveryTerms: raw.DeliveryTerms,
		CreatedDate: raw.CreatedDate, UpdatedDate: raw.UpdatedDate, Version: raw.Version,
		Archived: raw.Archived, Language: raw.Language, Title: raw.Title, Introduction: raw.Introduction,
		Remark: raw.Remark, TaxType: raw.TaxConditions.TaxType, Currency: raw.TotalPrice.Currency,
		TotalNetAmount: raw.TotalPrice.TotalNetAmount, TotalGrossAmount: raw.TotalPrice.TotalGrossAmount,
		TotalTaxAmount: raw.TotalPrice.TotalTaxAmount,
		LineItems:      make([]LineItem, 0, len(raw.LineItems)),
	}
	contact := Contact{
		ID: raw.Address.ContactID, Name: raw.Address.Name, Supplement: raw.Address.Supplement,
		Street: raw.Address.Street, Zip: raw.Address.Zip, City: raw.Address.City,
		CountryCode: raw.Address.CountryCode,
	}
	if contact != (Contact{}) {
		voucher.Contact = &contact
	}
	if shipping := Shipping(raw.ShippingConditions); shipping != (Shipping{}) {
		voucher.Shipping = &shipping
	}
	for _, related := range raw.RelatedVouchers {
		voucher.RelatedVouchers = append(voucher.RelatedVouchers, RelatedVoucher(related))
	}
	for _, item := range raw.LineItems {
		voucher.LineItems = append(voucher.LineItems, LineItem{
			Type: item.Type, Name: item.Name, Description: item.Description, Quantity: item.Quantity,
			UnitName: item.UnitName, UnitNetAmount: item.UnitPrice.NetAmount,
			UnitGrossAmount: item.UnitPrice.GrossAmount, TaxRatePercentage: item.UnitPrice.TaxRatePercentage,
			DiscountPercentage: item.DiscountPercentage, Amount: item.LineItemAmount,
		})
	}
	return voucher
}

// invoiceMayExist is appended to a failure of an invoice creation whose request may have reached Lexware:
// the invoice may exist although no usable confirmation arrived. Qatlas never repeats such a request.
const invoiceMayExist = "; the invoice may have been created, check the voucher list before repeating it"

// invoiceMayBeIssued is the hint of an issuing request: the invoice may already be final and numbered.
const invoiceMayBeIssued = "; the invoice may have been issued, check the voucher list before repeating it"

// get performs one bounded read against the fixed gateway and decodes the response into out.
func (c *Client) get(ctx context.Context, op, resource, path string, query url.Values, out any) error {
	return c.send(ctx, op, http.MethodGet, resource, path, query, nil, out, "")
}

// post performs one change against the fixed gateway; uncertain is the hint a mutation appends to a failure
// after its request may have reached Lexware.
func (c *Client) post(ctx context.Context, op, resource, path string, query url.Values, payload, out any, uncertain string) error {
	return c.send(ctx, op, http.MethodPost, resource, path, query, payload, out, uncertain)
}

// send is the only place that talks to the gateway. It sends exactly one request and never repeats it. A
// non-empty uncertain marks a change: it is appended to every failure after the request may have been
// delivered (timeout, reset, unknown transport cause, 5xx, or an unusable 2xx answer) and never to a failure
// that proves nothing was applied.
func (c *Client) send(ctx context.Context, op, method, resource, path string, query url.Values, payload, out any, uncertain string) error {
	// Every business request of every tool passes here, so a bound connection is verified before any of them.
	if err := c.verifyOrganization(ctx, op); err != nil {
		return err
	}
	return c.sendRaw(ctx, op, method, resource, path, query, payload, out, uncertain)
}

// sendRaw sends one request without the organization check; only the check itself may call it directly.
func (c *Client) sendRaw(ctx context.Context, op, method, resource, path string, query url.Values, payload, out any, uncertain string) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "Lexware", err)
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil || len(data) > maxResponseBytes {
			return providerError(op, "the request exceeds the size limit")
		}
		body = bytes.NewReader(data)
	}
	target := gateway + path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(req)
	if err != nil {
		failure := provider.Transport(op, "Lexware", err)
		if uncertain != "" && failure.MayHaveArrived() {
			failure.Message += uncertain
		}
		return failure
	}
	defer response.Body.Close()

	if ok := response.StatusCode == http.StatusOK || (method != http.MethodGet && response.StatusCode >= 200 && response.StatusCode < 300); !ok {
		failure := statusError(op, resource, response.StatusCode)
		if uncertain != "" && response.StatusCode >= 500 {
			failure.(*provider.Error).Message += uncertain
		}
		return failure
	}
	if failure := provider.ReadJSON(op, "Lexware", response.Body, maxResponseBytes, out); failure != nil {
		if uncertain != "" {
			failure.Message += uncertain
		}
		return failure
	}
	return nil
}

// statusError maps an HTTP status to a stable class. The provider message is never copied: Lexware echoes
// request detail into it, and the class plus the status is what a caller can act on.
//
// resource names the single object an operation addresses (a resource* constant) and is empty for lists,
// creations and the connection test; only a named object can be reported as missing.
func statusError(op, resource string, status int) error {
	switch status {
	case http.StatusUnauthorized:
		return &provider.Error{Class: provider.ClassAuth, Op: op, Message: "Lexware rejected the API key"}
	case http.StatusPaymentRequired:
		return &provider.Error{
			Class: provider.ClassPermission, Op: op,
			Message: "the Lexware contract does not include this API access; check the contract in Lexware",
		}
	case http.StatusForbidden:
		return &provider.Error{
			Class: provider.ClassPermission, Op: op,
			Message: "the API key is not permitted to perform this operation; check the rights of the API key in Lexware",
		}
	case http.StatusNotFound:
		if resource == "" {
			return &provider.Error{
				Class: provider.ClassProviderError, Op: op,
				Message: "Lexware did not find the requested endpoint or a referenced object (HTTP 404)",
			}
		}
		return &provider.Error{
			Class: provider.ClassNotFound, Op: op,
			Message: "Lexware does not hold this " + resource + " or does not show it to this API key",
		}
	case http.StatusBadRequest, http.StatusNotAcceptable:
		return &provider.Error{
			Class: provider.ClassProviderError, Op: op, Message: "Lexware rejected the request as invalid",
		}
	case http.StatusTooManyRequests:
		return &provider.Error{
			Class: provider.ClassRateLimited, Op: op, Message: "Lexware rate-limited the operation",
		}
	case http.StatusGatewayTimeout:
		return &provider.Error{Class: provider.ClassTimeout, Op: op, Message: "Lexware did not answer in time"}
	default:
		return &provider.Error{
			Class: provider.ClassProviderError, Op: op,
			Message: fmt.Sprintf("Lexware rejected the operation (HTTP %d)", status),
		}
	}
}

// Resource kinds of single-object operations, used in the not-found message.
const (
	resourceInvoice = "invoice"
	resourceContact = "contact"
	resourceArticle = "article"
	resourcePayment = "payment status"
	resourceVoucher = "voucher"
)

func providerError(op, message string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

// validAPIKey keeps an obviously unusable value out of a request. The real check is the provider's.
func validAPIKey(value string) bool {
	if len(value) < 8 || len(value) > 512 {
		return false
	}
	for _, r := range value {
		// A header value may not carry control characters, and a Lexware key never does.
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

// validUUID accepts the canonical 8-4-4-4-12 hexadecimal form and nothing else.
func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if b != '-' {
				return false
			}
			continue
		}
		hex := (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
		if !hex {
			return false
		}
	}
	return true
}
