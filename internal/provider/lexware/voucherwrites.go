package lexware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	voucherMayExist = "; the voucher may have been created, check the voucher list before repeating it"

	// voucherStatusUnchecked is the only status the tool offers: a voucher that still awaits review.
	voucherStatusUnchecked = "unchecked"

	maxVoucherItems = 100
	maxVoucherSum   = 1e10

	// netVoucherMessage is the fixed refusal of a net voucher; Lexware forbids it for the status unchecked.
	netVoucherMessage = "tax_type net is not available here: Lexware accepts net vouchers only as booked vouchers"
)

var voucherCreateRisk = capability.Risk{
	Effect:          capability.EffectCreate,
	Idempotency:     capability.IdempotencyNonIdempotent,
	Confirmation:    capability.ConfirmationRequired,
	OpenWorld:       true,
	DataSensitivity: bookkeepingSensitivity,
}

const (
	amountSchema = `{"type":"number","minimum":0,"maximum":10000000000}`

	voucherItemSchema = `{"type":"object","properties":{"amount":` + amountSchema + `,"tax_amount":` + amountSchema +
		`,"tax_rate_percent":{"type":"number","minimum":0,"maximum":100},"category_id":` + uuidSchema +
		`},"required":["amount","tax_amount","tax_rate_percent","category_id"],"additionalProperties":false}`

	voucherWriteProperties = `"type":{"type":"string","enum":["salesinvoice","salescreditnote","purchaseinvoice","purchasecreditnote"]},` +
		`"voucher_number":{"type":"string","minLength":1,"maxLength":255},` +
		`"voucher_date":` + dateSchema + `,"due_date":` + dateSchema + `,` +
		`"total_gross_amount":` + amountSchema + `,"total_tax_amount":` + amountSchema + `,` +
		`"tax_type":{"type":"string","enum":["gross","net"]},` +
		`"contact_id":` + uuidSchema + `,"use_collective_contact":{"type":"boolean"},` +
		`"voucher_items":{"type":"array","minItems":1,"maxItems":100,"items":` + voucherItemSchema + `}`
)

var vouchersCreate = capability.Descriptor{
	ID: Provider + ".vouchers.create", Version: 1, Title: "Create a Lexware bookkeeping voucher to check",
	Description: "Record one bookkeeping voucher with the status unchecked, so that it awaits review in Lexware Office. " +
		"Lexware does not allow net vouchers in this status, so tax_type must be gross. Name exactly one of " +
		"contact_id or use_collective_contact true. Positions carry amount, tax_amount, tax_rate_percent and " +
		"category_id from lexware.postingcategories.list. No files, remark or other fields are sent",
	Tags:     []string{"lexware", "vouchers", "create", "bookkeeping", "accounting"},
	Provider: Provider, Risk: voucherCreateRisk,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + voucherWriteProperties +
		`},"required":["type","voucher_date","total_gross_amount","total_tax_amount","tax_type","voucher_items"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(articleWriteOutputSchema),
	Arguments: []capability.Argument{
		{Name: "type", Description: "salesinvoice, salescreditnote, purchaseinvoice or purchasecreditnote", Required: true},
		{Name: "voucher_date", Description: "Voucher date as YYYY-MM-DD", Required: true},
		{Name: "total_gross_amount", Description: "Total gross amount, 0 to 10000000000", Required: true},
		{Name: "total_tax_amount", Description: "Total tax amount, 0 to 10000000000", Required: true},
		{Name: "tax_type", Description: "gross; net is refused because Lexware allows it only for booked vouchers", Required: true},
		{Name: "voucher_items", Description: "1 to 100 positions, each with amount, tax_amount, tax_rate_percent (0 to 100) and category_id as a UUID", Required: true},
		{Name: "voucher_number", Description: "Voucher number, up to 255 characters"},
		{Name: "due_date", Description: "Date the payment is due as YYYY-MM-DD"},
		{Name: "contact_id", Description: "Contact identifier as a UUID, as returned by lexware.contacts.list; exclusive with use_collective_contact"},
		{Name: "use_collective_contact", Description: "true books the voucher to the collective contact; exclusive with contact_id"},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the voucher"},
		{Name: "created_date", Description: "Creation timestamp reported by Lexware"},
		{Name: "updated_date", Description: "Last change timestamp reported by Lexware"},
		{Name: "version", Description: "Version counter after the write, reported by Lexware"},
	},
	Examples: []capability.Example{{
		Description: "A purchase invoice to a collective contact",
		Arguments: json.RawMessage(`{"type":"purchaseinvoice","voucher_number":"R-100","voucher_date":"2026-05-14",` +
			`"total_gross_amount":119,"total_tax_amount":19,"tax_type":"gross","use_collective_contact":true,` +
			`"voucher_items":[{"amount":119,"tax_amount":19,"tax_rate_percent":19,` +
			`"category_id":"11111111-2222-3333-4444-555555555555"}]}`),
	}},
}

// voucherInput holds the writable members of a bookkeeping voucher. Nothing else reaches the provider.
type voucherInput struct {
	Type                 string            `json:"type"`
	VoucherNumber        string            `json:"voucher_number"`
	VoucherDate          string            `json:"voucher_date"`
	DueDate              string            `json:"due_date"`
	TotalGrossAmount     json.Number       `json:"total_gross_amount"`
	TotalTaxAmount       json.Number       `json:"total_tax_amount"`
	TaxType              string            `json:"tax_type"`
	ContactID            string            `json:"contact_id"`
	UseCollectiveContact bool              `json:"use_collective_contact"`
	VoucherItems         []voucherItemData `json:"voucher_items"`
}

type voucherItemData struct {
	Amount         json.Number `json:"amount"`
	TaxAmount      json.Number `json:"tax_amount"`
	TaxRatePercent json.Number `json:"tax_rate_percent"`
	CategoryID     string      `json:"category_id"`
}

func validAmount(value json.Number, max float64) bool {
	f, err := strconv.ParseFloat(string(value), 64)
	return err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) && f >= 0 && f <= max
}

func validDay(value string) bool {
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

// validate re-checks the rules of the schema, because the handler is also reachable without it. It touches
// neither the credential nor the network.
func (v voucherInput) validate() error {
	switch v.Type {
	case "salesinvoice", "salescreditnote", "purchaseinvoice", "purchasecreditnote":
	default:
		return errors.New("type must be salesinvoice, salescreditnote, purchaseinvoice or purchasecreditnote")
	}
	switch v.TaxType {
	case "gross":
	case "net":
		return errors.New(netVoucherMessage)
	default:
		return errors.New("tax_type must be gross")
	}
	if n := len(v.VoucherNumber); n > 255 {
		return errors.New("voucher_number is too long")
	}
	if !validDay(v.VoucherDate) || (v.DueDate != "" && !validDay(v.DueDate)) {
		return errors.New("voucher_date and due_date must be dates as YYYY-MM-DD")
	}
	if !validAmount(v.TotalGrossAmount, maxVoucherSum) || !validAmount(v.TotalTaxAmount, maxVoucherSum) {
		return errors.New("total_gross_amount and total_tax_amount must be numbers from 0 to 10000000000")
	}
	if v.ContactID != "" && !validUUID(v.ContactID) {
		return errors.New("contact_id must be a UUID")
	}
	if (v.ContactID != "") == v.UseCollectiveContact {
		return errors.New("name exactly one of contact_id or use_collective_contact true")
	}
	if len(v.VoucherItems) == 0 || len(v.VoucherItems) > maxVoucherItems {
		return errors.New("voucher_items needs 1 to 100 positions")
	}
	for _, item := range v.VoucherItems {
		if !validAmount(item.Amount, maxVoucherSum) || !validAmount(item.TaxAmount, maxVoucherSum) ||
			!validAmount(item.TaxRatePercent, 100) {
			return errors.New("a position needs amount and tax_amount from 0 to 10000000000 and tax_rate_percent from 0 to 100")
		}
		if !validUUID(item.CategoryID) {
			return errors.New("category_id of a position must be a UUID")
		}
	}
	return nil
}

// voucherPayload builds the provider object from the schema fields only. The status is a parameter so that
// booking a voucher can reuse the mapping; the tool offers unchecked alone.
func voucherPayload(status string, v voucherInput) map[string]any {
	items := make([]map[string]any, 0, len(v.VoucherItems))
	for _, item := range v.VoucherItems {
		items = append(items, map[string]any{"amount": item.Amount, "taxAmount": item.TaxAmount,
			"taxRatePercent": item.TaxRatePercent, "categoryId": item.CategoryID})
	}
	payload := map[string]any{"type": v.Type, "voucherStatus": status, "voucherDate": v.VoucherDate,
		"totalGrossAmount": v.TotalGrossAmount, "totalTaxAmount": v.TotalTaxAmount, "taxType": v.TaxType,
		"voucherItems": items}
	if v.VoucherNumber != "" {
		payload["voucherNumber"] = v.VoucherNumber
	}
	if v.DueDate != "" {
		payload["dueDate"] = v.DueDate
	}
	if v.ContactID != "" {
		payload["contactId"] = v.ContactID
	} else if v.UseCollectiveContact {
		payload["useCollectiveContact"] = true
	}
	return payload
}

func invokeVouchersCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create voucher"
	var input voucherInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
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
	return client.CreateVoucher(ctx, input)
}

// CreateVoucher posts one voucher with the status unchecked: exactly one request, never repeated.
func (c *Client) CreateVoucher(ctx context.Context, input voucherInput) (*createResult, error) {
	const op = "create voucher"
	if err := input.validate(); err != nil {
		return nil, providerError(op, err.Error())
	}
	return c.postObject(ctx, op, "/v1/vouchers", voucherPayload(voucherStatusUnchecked, input),
		voucherMayExist, "a voucher")
}
