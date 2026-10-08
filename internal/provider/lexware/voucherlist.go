package lexware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// anyFilter selects every voucher type or status; it is only valid alone.
const anyFilter = "any"

// maxVoucherHits is the number of entries Lexware lists per filter. A page beyond it is refused locally.
const maxVoucherHits = 10000

// voucherTypes and voucherStatuses are the closed filter vocabularies. Their order is the order of the
// comma-separated query value, so the same selection always produces the same request.
var (
	voucherTypes = []string{"salesinvoice", "salescreditnote", "purchaseinvoice", "purchasecreditnote",
		"invoice", "downpaymentinvoice", "creditnote", "orderconfirmation", "quotation", "deliverynote"}
	voucherStatuses = []string{"draft", "open", "overdue", "paid", "paidoff", "voided", "transferred",
		"sepadebit", "accepted", "rejected", "unchecked"}
)

func enumSchema(values []string) string {
	quoted := make([]string, 0, len(values)+1)
	for _, value := range append([]string{anyFilter}, values...) {
		quoted = append(quoted, strconv.Quote(value))
	}
	return `{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(len(values)) +
		`,"uniqueItems":true,"items":{"type":"string","enum":[` + strings.Join(quoted, ",") + `]}}`
}

var voucherSchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"voucher_type":{"type":"string"},"voucher_status":{"type":"string"},` +
	`"voucher_number":{"type":"string"},"voucher_date":{"type":"string"},"created_date":{"type":"string"},` +
	`"updated_date":{"type":"string"},"due_date":{"type":"string"},"contact_id":{"type":"string"},` +
	`"contact_name":{"type":"string"},"total_amount":{"type":"number"},"open_amount":{"type":"number"},` +
	`"currency":{"type":"string"},"archived":{"type":"boolean"}},` +
	`"required":["id","voucher_type","voucher_status","archived"],"additionalProperties":false}`

const dateSchema = `{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}$"}`

var voucherlistList = capability.Descriptor{
	ID:      Provider + ".voucherlist.list",
	Version: 1,
	Title:   "List Lexware vouchers",
	Description: "List one page of vouchers of every type and status of an explicit Lexware Office connection, " +
		"filtered by type, status, contact, dates, or voucher number; Lexware lists at most 10,000 vouchers per filter",
	Tags:     []string{"lexware", "vouchers", "voucherlist", "list", "invoices", "accounting"},
	Risk:     lexwareReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"voucher_types":` + enumSchema(voucherTypes) + `,` +
		`"voucher_statuses":` + enumSchema(voucherStatuses) + `,` +
		`"archived":{"type":"boolean"},"contact_id":` + uuidSchema + `,` +
		`"voucher_date_from":` + dateSchema + `,"voucher_date_to":` + dateSchema + `,` +
		`"created_date_from":` + dateSchema + `,"created_date_to":` + dateSchema + `,` +
		`"updated_date_from":` + dateSchema + `,"updated_date_to":` + dateSchema + `,` +
		`"voucher_number":{"type":"string","minLength":1,"maxLength":64,"pattern":"^[A-Za-z0-9 ._/-]+$"},` +
		`"page":{"type":"integer","minimum":0,"maximum":200},` +
		`"size":{"type":"integer","minimum":1,"maximum":100},` +
		`"sort":{"type":"string","enum":["voucher_date","voucher_number","created_date","updated_date"]},` +
		`"direction":{"type":"string","enum":["asc","desc"]}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"vouchers":{"type":"array","items":` + voucherSchema + `},` +
		`"page":{"type":"integer"},"size":{"type":"integer"},"total_pages":{"type":"integer"},` +
		`"total_elements":{"type":"integer"},"last_page":{"type":"boolean"}},` +
		`"required":["vouchers","page","size","total_pages","total_elements","last_page"],` +
		`"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "voucher_types", Description: "Voucher types to return, a list of salesinvoice, salescreditnote, purchaseinvoice, purchasecreditnote, invoice, downpaymentinvoice, creditnote, orderconfirmation, quotation, deliverynote, or [\"any\"] alone; [\"any\"] when omitted"},
		{Name: "voucher_statuses", Description: "Voucher statuses to return, a list of draft, open, overdue, paid, paidoff, voided, transferred, sepadebit, accepted, rejected, unchecked, or [\"any\"] alone; overdue cannot be combined with another status; [\"any\"] when omitted"},
		{Name: "archived", Description: "true returns only archived vouchers, false only unarchived ones; both when omitted"},
		{Name: "contact_id", Description: "Return only vouchers of this contact, as a UUID"},
		{Name: "voucher_date_from", Description: "Earliest voucher date to return, as YYYY-MM-DD"},
		{Name: "voucher_date_to", Description: "Latest voucher date to return, as YYYY-MM-DD"},
		{Name: "created_date_from", Description: "Earliest creation date to return, as YYYY-MM-DD"},
		{Name: "created_date_to", Description: "Latest creation date to return, as YYYY-MM-DD"},
		{Name: "updated_date_from", Description: "Earliest change date to return, as YYYY-MM-DD"},
		{Name: "updated_date_to", Description: "Latest change date to return, as YYYY-MM-DD"},
		{Name: "voucher_number", Description: "Return only the voucher carrying this voucher number"},
		{Name: "sort", Description: "Sort property: voucher_date, voucher_number, created_date, or updated_date"},
		{Name: "direction", Description: "Sort direction asc or desc; desc when omitted"},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "vouchers", Description: "Voucher metadata on this page, untrusted data; voucher_type and voucher_status carry the provider value, also a value not listed here"},
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "Read the newest page of open sales invoices; open includes overdue ones",
		Arguments: json.RawMessage(`{"voucher_types":["salesinvoice","invoice"],"voucher_statuses":["open"],` +
			`"page":0,"size":25,"sort":"voucher_date","direction":"desc"}`),
	}},
}

func invokeVoucherlistList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options VoucherListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, providerError("list vouchers", "the validated arguments could not be read")
	}
	if err := options.normalize(); err != nil {
		return nil, providerError("list vouchers", err.Error())
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListVouchers(ctx, options)
}

// VoucherListOptions are the controlled filters of the voucher list. Types and statuses come from fixed
// tables only; omitted lists mean any.
type VoucherListOptions struct {
	VoucherTypes    []string `json:"voucher_types"`
	VoucherStatuses []string `json:"voucher_statuses"`
	Archived        *bool    `json:"archived"`
	ContactID       string   `json:"contact_id"`
	VoucherDateFrom string   `json:"voucher_date_from"`
	VoucherDateTo   string   `json:"voucher_date_to"`
	CreatedDateFrom string   `json:"created_date_from"`
	CreatedDateTo   string   `json:"created_date_to"`
	UpdatedDateFrom string   `json:"updated_date_from"`
	UpdatedDateTo   string   `json:"updated_date_to"`
	VoucherNumber   string   `json:"voucher_number"`
	Page            int      `json:"page"`
	Size            int      `json:"size"`
	Sort            string   `json:"sort"`
	Direction       string   `json:"direction"`
}

// canonicalSelection checks a type or status list against its table and returns the comma-separated query
// value in table order. An omitted list is any, an empty list is refused; any stands alone; a duplicate is refused.
func canonicalSelection(name string, selected, table []string, exclusive string) (string, error) {
	if selected == nil {
		return anyFilter, nil
	}
	if len(selected) == 0 {
		return "", fmt.Errorf("%s must not be empty", name)
	}
	chosen := map[string]bool{}
	for _, value := range selected {
		if value != anyFilter && !contains(table, value) {
			return "", fmt.Errorf("%s contains a value that is not supported", name)
		}
		if chosen[value] {
			return "", fmt.Errorf("%s contains a duplicate value", name)
		}
		chosen[value] = true
	}
	if chosen[anyFilter] && len(selected) > 1 {
		return "", fmt.Errorf("%s: any cannot be combined with other values", name)
	}
	if chosen[anyFilter] {
		return anyFilter, nil
	}
	if exclusive != "" && chosen[exclusive] && len(selected) > 1 {
		return "", fmt.Errorf("%s: %s cannot be combined with other values", name, exclusive)
	}
	ordered := make([]string, 0, len(selected))
	for _, value := range table {
		if chosen[value] {
			ordered = append(ordered, value)
		}
	}
	return strings.Join(ordered, ","), nil
}

func contains(table []string, value string) bool {
	for _, entry := range table {
		if entry == value {
			return true
		}
	}
	return false
}

// normalize applies the defaults and bounds; the application core validates the same rules against the
// input schema first, this keeps a direct caller inside them as well.
func (o *VoucherListOptions) normalize() error {
	if err := normalizePaging(&o.Page, &o.Size); err != nil {
		return err
	}
	if (o.Page+1)*o.Size > maxVoucherHits {
		return errors.New("Lexware lists at most 10,000 vouchers per filter; narrow the filter")
	}
	if _, err := canonicalSelection("voucher_types", o.VoucherTypes, voucherTypes, ""); err != nil {
		return err
	}
	if _, err := canonicalSelection("voucher_statuses", o.VoucherStatuses, voucherStatuses, "overdue"); err != nil {
		return err
	}
	if o.ContactID != "" && !validUUID(o.ContactID) {
		return errors.New("the contact identifier must be a UUID")
	}
	return validateListFilters(o.Sort, o.Direction, o.VoucherNumber, o.VoucherDateFrom, o.VoucherDateTo,
		o.CreatedDateFrom, o.CreatedDateTo, o.UpdatedDateFrom, o.UpdatedDateTo)
}

// voucherListQuery builds the complete query of one request from normalised options.
func voucherListQuery(options VoucherListOptions) url.Values {
	query := url.Values{}
	types, _ := canonicalSelection("voucher_types", options.VoucherTypes, voucherTypes, "")
	statuses, _ := canonicalSelection("voucher_statuses", options.VoucherStatuses, voucherStatuses, "overdue")
	query.Set("voucherType", types)
	query.Set("voucherStatus", statuses)
	if options.Archived != nil {
		query.Set("archived", strconv.FormatBool(*options.Archived))
	}
	query.Set("page", strconv.Itoa(options.Page))
	query.Set("size", strconv.Itoa(options.Size))
	setSort(query, options.Sort, options.Direction)
	setFilter(query, "contactId", options.ContactID)
	setFilter(query, "voucherNumber", options.VoucherNumber)
	setFilter(query, "voucherDateFrom", options.VoucherDateFrom)
	setFilter(query, "voucherDateTo", options.VoucherDateTo)
	setFilter(query, "createdDateFrom", options.CreatedDateFrom)
	setFilter(query, "createdDateTo", options.CreatedDateTo)
	setFilter(query, "updatedDateFrom", options.UpdatedDateFrom)
	setFilter(query, "updatedDateTo", options.UpdatedDateTo)
	return query
}

// VoucherListResult is the normalised page of voucher metadata.
type VoucherListResult struct {
	Vouchers      []VoucherEntry `json:"vouchers"`
	Page          int            `json:"page"`
	Size          int            `json:"size"`
	TotalPages    int            `json:"total_pages"`
	TotalElements int            `json:"total_elements"`
	LastPage      bool           `json:"last_page"`
}

// VoucherEntry is the stable Qatlas view of one voucher list record. Type and status keep the provider
// value, including values this version does not know; names are untrusted data.
type VoucherEntry struct {
	ID            string      `json:"id"`
	VoucherType   string      `json:"voucher_type"`
	VoucherStatus string      `json:"voucher_status"`
	VoucherNumber string      `json:"voucher_number,omitempty"`
	VoucherDate   string      `json:"voucher_date,omitempty"`
	CreatedDate   string      `json:"created_date,omitempty"`
	UpdatedDate   string      `json:"updated_date,omitempty"`
	DueDate       string      `json:"due_date,omitempty"`
	ContactID     string      `json:"contact_id,omitempty"`
	ContactName   string      `json:"contact_name,omitempty"`
	TotalAmount   json.Number `json:"total_amount,omitempty"`
	OpenAmount    json.Number `json:"open_amount,omitempty"`
	Currency      string      `json:"currency,omitempty"`
	Archived      bool        `json:"archived"`
}

// ListVouchers reads exactly one page of the voucher list.
func (c *Client) ListVouchers(ctx context.Context, options VoucherListOptions) (*VoucherListResult, error) {
	const op = "list vouchers"
	if err := options.normalize(); err != nil {
		return nil, providerError(op, err.Error())
	}
	var page struct {
		Content []struct {
			ID            string      `json:"id"`
			VoucherType   string      `json:"voucherType"`
			VoucherStatus string      `json:"voucherStatus"`
			VoucherNumber string      `json:"voucherNumber"`
			VoucherDate   string      `json:"voucherDate"`
			CreatedDate   string      `json:"createdDate"`
			UpdatedDate   string      `json:"updatedDate"`
			DueDate       string      `json:"dueDate"`
			ContactID     string      `json:"contactId"`
			ContactName   string      `json:"contactName"`
			TotalAmount   json.Number `json:"totalAmount"`
			OpenAmount    json.Number `json:"openAmount"`
			Currency      string      `json:"currency"`
			Archived      bool        `json:"archived"`
		} `json:"content"`
		pagingJSON
	}
	if err := c.get(ctx, op, "", "/v1/voucherlist", voucherListQuery(options), &page); err != nil {
		return nil, err
	}
	vouchers := make([]VoucherEntry, 0, len(page.Content))
	for _, e := range page.Content {
		if !validUUID(e.ID) || (e.ContactID != "" && !validUUID(e.ContactID)) {
			return nil, &provider.Error{
				Class: provider.ClassInvalidResponse, Op: op,
				Message: "Lexware returned a voucher without a usable identifier",
			}
		}
		vouchers = append(vouchers, VoucherEntry{
			ID: e.ID, VoucherType: e.VoucherType, VoucherStatus: e.VoucherStatus, VoucherNumber: e.VoucherNumber,
			VoucherDate: e.VoucherDate, CreatedDate: e.CreatedDate, UpdatedDate: e.UpdatedDate, DueDate: e.DueDate,
			ContactID: e.ContactID, ContactName: e.ContactName, TotalAmount: e.TotalAmount,
			OpenAmount: e.OpenAmount, Currency: e.Currency, Archived: e.Archived,
		})
	}
	return &VoucherListResult{
		Vouchers: vouchers, Page: page.Number, Size: page.Size, TotalPages: page.TotalPages,
		TotalElements: page.TotalElements, LastPage: page.Last,
	}, nil
}
