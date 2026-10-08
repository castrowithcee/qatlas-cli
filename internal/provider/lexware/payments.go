package lexware

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var paymentsGet = capability.Descriptor{
	ID:      Provider + ".payments.get",
	Version: 1,
	Title:   "Get the payment status of a Lexware voucher",
	Description: "Read the payment status, open amount, and payment items of one voucher of an explicit " +
		"Lexware Office connection by the voucher identifier",
	Tags:     []string{"lexware", "payments", "get", "vouchers", "accounting"},
	Risk:     lexwareReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"voucher_id":` + uuidSchema + `},` +
		`"required":["voucher_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"voucher_id":{"type":"string"},"payment_status":{"type":"string"},"voucher_type":{"type":"string"},` +
		`"voucher_status":{"type":"string"},"open_amount":{"type":"number"},"currency":{"type":"string"},` +
		`"paid_date":{"type":"string"},"payment_items":{"type":"array","items":{"type":"object","properties":{` +
		`"payment_item_type":{"type":"string"},"posting_date":{"type":"string"},"amount":{"type":"number"},` +
		`"currency":{"type":"string"},"exchange_rate":{"type":"number"}},"additionalProperties":false}}},` +
		`"required":["voucher_id","payment_items"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "voucher_id", Description: "Voucher identifier as a UUID, as returned by lexware.voucherlist.list or lexware.invoices.list", Required: true},
	},
	Fields: []capability.Field{
		{Name: "voucher_id", Description: "Identifier of the requested voucher"},
		{Name: "payment_status", Description: "Provider payment status, for example balanced, openRevenue, or openExpense"},
		{Name: "voucher_type", Description: "Voucher type reported by Lexware"},
		{Name: "voucher_status", Description: "Voucher status reported by Lexware"},
		{Name: "open_amount", Description: "Amount still open"},
		{Name: "currency", Description: "Currency of the open amount"},
		{Name: "paid_date", Description: "Timestamp of the last payment, when the voucher is paid"},
		{Name: "payment_items", Description: "Payments, credit notes, and other items booked against the voucher: payment_item_type, posting_date, amount, currency, exchange_rate"},
	},
	Examples: []capability.Example{{
		Description: "Read the payment status of one voucher by the identifier a list result reported",
		Arguments:   json.RawMessage(`{"voucher_id":"11111111-2222-3333-4444-555555555555"}`),
	}},
}

func invokePaymentsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		VoucherID string `json:"voucher_id"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, providerError("get payment status", "the validated arguments could not be read")
	}
	if !validUUID(arguments.VoucherID) {
		return nil, providerError("get payment status", "the voucher identifier must be a UUID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetPaymentStatus(ctx, arguments.VoucherID)
}

// PaymentStatus is the stable Qatlas view of the payment state of one voucher. The provider reports no
// identifier of its own; VoucherID echoes the validated request. Amounts keep the provider's exact digits.
type PaymentStatus struct {
	VoucherID     string        `json:"voucher_id"`
	PaymentStatus string        `json:"payment_status,omitempty"`
	VoucherType   string        `json:"voucher_type,omitempty"`
	VoucherStatus string        `json:"voucher_status,omitempty"`
	OpenAmount    json.Number   `json:"open_amount,omitempty"`
	Currency      string        `json:"currency,omitempty"`
	PaidDate      string        `json:"paid_date,omitempty"`
	PaymentItems  []PaymentItem `json:"payment_items"`
}

// PaymentItem is one booking against a voucher.
type PaymentItem struct {
	PaymentItemType string      `json:"payment_item_type,omitempty"`
	PostingDate     string      `json:"posting_date,omitempty"`
	Amount          json.Number `json:"amount,omitempty"`
	Currency        string      `json:"currency,omitempty"`
	ExchangeRate    json.Number `json:"exchange_rate,omitempty"`
}

// GetPaymentStatus reads exactly the payment status of a validated voucher identifier.
func (c *Client) GetPaymentStatus(ctx context.Context, voucherID string) (*PaymentStatus, error) {
	const op = "get payment status"
	if !validUUID(voucherID) {
		return nil, providerError(op, "the voucher identifier must be a UUID")
	}
	var raw struct {
		OpenAmount    json.Number `json:"openAmount"`
		Currency      string      `json:"currency"`
		PaymentStatus string      `json:"paymentStatus"`
		VoucherType   string      `json:"voucherType"`
		VoucherStatus string      `json:"voucherStatus"`
		PaidDate      string      `json:"paidDate"`
		PaymentItems  []struct {
			PaymentItemType string      `json:"paymentItemType"`
			PostingDate     string      `json:"postingDate"`
			Amount          json.Number `json:"amount"`
			Currency        string      `json:"currency"`
			ExchangeRate    json.Number `json:"exchangeRate"`
		} `json:"paymentItems"`
	}
	if err := c.get(ctx, op, resourcePayment, "/v1/payments/"+url.PathEscape(voucherID), nil, &raw); err != nil {
		return nil, err
	}
	items := make([]PaymentItem, 0, len(raw.PaymentItems))
	for _, item := range raw.PaymentItems {
		items = append(items, PaymentItem(item))
	}
	return &PaymentStatus{
		VoucherID: voucherID, PaymentStatus: raw.PaymentStatus, VoucherType: raw.VoucherType,
		VoucherStatus: raw.VoucherStatus, OpenAmount: raw.OpenAmount, Currency: raw.Currency,
		PaidDate: raw.PaidDate, PaymentItems: items,
	}, nil
}
