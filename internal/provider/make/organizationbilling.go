package makeapi

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// make.organization.subscription, .usage and .payments read financial data of the organization an
// organization-mode connection is bound to; the organization is always the connection's target. Read only.
// All three need organizations:read:
//   - GET /organizations/{organizationId}/subscription: product{id,name,nextBill,price{id,price,currencyCode,
//     period},coupon{validFrom,validTo,price,currencyCode,productId,productName}}, pauseStartsAt, pauseEndsAt,
//     isSubscriptionPaused. Prices stay exact text, never a float.
//   - GET /organizations/{organizationId}/usage?organizationTimezone: {"data":[{date,operations,dataTransfer,
//     centicredits}]}, 30 days.
//   - GET /organizations/{organizationId}/payments?pg[offset]&pg[limit]&pg[sortBy]&pg[sortDir]:
//     {"payments":[{id,invoice_number,created,type_name,status_name,product_name,payment_method,amount_total,
//     currency_code,invoice_url,hosted_invoice_url,period_from,period_to}]}.
// Narrower readings: invoice_url and hosted_invoice_url may open an invoice without signing in, payment_method
// is payment card data, and product_name is documented as an object without properties, so none of them is
// returned; Make documents no values for pg[sortBy], so only pg[sortDir] is offered and the sort stays on
// created. The payment-method endpoints, discounts and every write on the subscription are not offered.

const (
	billingSensitivity    = "make-organization-financial-data"
	needOrganizationRead  = "the organizations:read scope"
	maxPaymentsListed     = 100
	defaultPaymentsListed = 25
)

var billingReadRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: billingSensitivity}

var organizationSubscription = capability.Descriptor{
	ID: Provider + ".organization.subscription", Version: 1, Title: "Read the subscription of the bound Make organization",
	Description: "Read plan name, next billing date, plan price, coupon, and pause state of the subscription of " +
		"the organization this connection is bound to; takes no arguments, changes nothing, returns no payment " +
		"method. Financial data. Only on an organization connection. Needs the organizations:read scope",
	Tags: []string{"make", "organization", "subscription", "billing"}, Risk: billingReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(emptyInput),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization_id":{"type":"integer"},` +
		`"plan_id":{"type":"string"},"plan_name":{"type":"string"},"next_bill":{"type":"string"},` +
		`"is_paused":{"type":"boolean"},"pause_starts_at":{"type":"string"},"pause_ends_at":{"type":"string"},` +
		`"price_id":{"type":"integer"},"price":{"type":"string"},"price_currency":{"type":"string"},` +
		`"price_period":{"type":"string"},"coupon_valid_from":{"type":"string"},"coupon_valid_to":{"type":"string"},` +
		`"coupon_price":{"type":"string"},"coupon_currency":{"type":"string"},"coupon_product_id":{"type":"integer"},` +
		`"coupon_product_name":{"type":"string"}},` +
		`"required":["organization_id"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "organization_id", Description: "The bound organization"},
		{Name: "plan_id", Description: "Plan identifier, untrusted"}, {Name: "plan_name", Description: "Plan name, untrusted"},
		{Name: "next_bill", Description: "Next billing date as Make reports it, untrusted"},
		{Name: "is_paused", Description: "True when the subscription is paused"},
		{Name: "pause_starts_at", Description: "Scheduled pause start, untrusted"},
		{Name: "pause_ends_at", Description: "Scheduled pause end, untrusted"},
		{Name: "price_id", Description: "Identifier of the plan price"},
		{Name: "price", Description: "Plan price as the exact decimal text Make reports, untrusted"},
		{Name: "price_currency", Description: "Currency code of the plan price, untrusted"},
		{Name: "price_period", Description: "Billing period of the plan price, untrusted"},
		{Name: "coupon_valid_from", Description: "Start of the coupon validity, untrusted"},
		{Name: "coupon_valid_to", Description: "End of the coupon validity, untrusted"},
		{Name: "coupon_price", Description: "Coupon price as the exact decimal text Make reports, untrusted"},
		{Name: "coupon_currency", Description: "Currency code of the coupon price, untrusted"},
		{Name: "coupon_product_id", Description: "Identifier of the product the coupon applies to"},
		{Name: "coupon_product_name", Description: "Name of the product the coupon applies to, untrusted"}},
	Examples: []capability.Example{{Description: "Read the subscription", Arguments: json.RawMessage(`{}`)}},
}

var organizationUsage = capability.Descriptor{
	ID: Provider + ".organization.usage", Version: 1, Title: "Read the usage of the bound Make organization",
	Description: "Read the daily operations, data transfer, and centicredits of the organization this " +
		"connection is bound to for Make's last 30 days, by default in the caller's timezone; changes nothing. " +
		"Financial data. Only on an organization connection. Needs the organizations:read scope",
	Tags: []string{"make", "organization", "usage", "billing"}, Risk: billingReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(usageInputSchema), Arguments: usageArguments,
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization_id":{"type":"integer"},` +
		`"days":{"type":"array","items":{"type":"object","properties":{"date":{"type":"string"},` +
		`"operations":{"type":"integer"},"data_transfer":{"type":"integer"},"centicredits":{"type":"integer"}},` +
		`"required":["date"],"additionalProperties":false}},"count":{"type":"integer"}},` +
		`"required":["organization_id","days","count"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "organization_id", Description: "The bound organization"},
		{Name: "date", Description: "Day of a daily record, as Make reports it, untrusted"},
		{Name: "operations", Description: "Operations of that day"},
		{Name: "data_transfer", Description: "Data transferred that day, in bytes"},
		{Name: "centicredits", Description: "Credits consumed that day, in hundredths"},
		{Name: "count", Description: "Days returned, at most " + strconv.Itoa(maxUsageDays)}},
	Examples: []capability.Example{{Description: "Read the usage", Arguments: json.RawMessage(`{}`)}},
}

var organizationPayments = capability.Descriptor{
	ID: Provider + ".organization.payments", Version: 2, Title: "List the invoices of the bound Make organization",
	Description: "List invoice number, date, type, status, amount, currency, and period of the payments of the " +
		"organization this connection is bound to, newest first by default, page by page; changes nothing and returns " +
		"neither payment method nor invoice link. Financial data. Only on an organization connection. Needs " +
		"the organizations:read scope",
	Tags: []string{"make", "organization", "payments", "billing"}, Risk: billingReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"offset":{"type":"integer","minimum":0},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxPaymentsListed) + `},` +
		`"sort_dir":{"type":"string","enum":["asc","desc"]}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization_id":{"type":"integer"},` +
		`"payments":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},` +
		`"invoice_number":{"type":"integer"},"created":{"type":"string"},"type":{"type":"string"},` +
		`"status":{"type":"string"},"amount_total":{"type":"string"},"currency":{"type":"string"},` +
		`"period_from":{"type":"string"},"period_to":{"type":"string"}},"required":["id"],"additionalProperties":false}},` +
		`"offset":{"type":"integer"},"count":{"type":"integer"},"has_more":{"type":"boolean"}},` +
		`"required":["organization_id","payments","offset","count","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "offset", Description: "Payments to skip, 0 when omitted"},
		{Name: "limit", Description: "Payments per page, 1 to " + strconv.Itoa(maxPaymentsListed) + "; " +
			strconv.Itoa(defaultPaymentsListed) + " when omitted"},
		{Name: "sort_dir", Description: "asc or desc by creation date; desc when omitted"}},
	Fields: []capability.Field{
		{Name: "organization_id", Description: "The bound organization"},
		{Name: "id", Description: "Payment identifier as text, untrusted"},
		{Name: "invoice_number", Description: "Invoice number as a whole number; left out when Make's value is not one"},
		{Name: "created", Description: "Creation time as Make reports it, untrusted"},
		{Name: "type", Description: "Payment type name, untrusted"}, {Name: "status", Description: "Payment status name, untrusted"},
		{Name: "amount_total", Description: "Total amount as the exact text Make reports, untrusted"},
		{Name: "currency", Description: "Currency code, untrusted"},
		{Name: "period_from", Description: "Start of the billed period, untrusted"},
		{Name: "period_to", Description: "End of the billed period, untrusted"},
		{Name: "offset", Description: "Offset of this page"}, {Name: "count", Description: "Payments in this page"},
		{Name: "has_more", Description: "True when the page was full, so more may follow (Make reports no total)"}},
	Examples: []capability.Example{{Description: "Read the first page", Arguments: json.RawMessage(`{}`)}},
}

func orgPath(c *Client, suffix string) string {
	return "/organizations/" + strconv.FormatInt(c.scope.orgID, 10) + suffix
}

func invokeOrganizationSubscription(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := openOrganization(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var page struct {
		Product struct {
			ID       any    `json:"id"`
			Name     string `json:"name"`
			NextBill string `json:"nextBill"`
			// price and coupon are read leniently: a shape change must not fail the whole answer.
			Price  json.RawMessage `json:"price"`
			Coupon json.RawMessage `json:"coupon"`
		} `json:"product"`
		PauseStartsAt string `json:"pauseStartsAt"`
		PauseEndsAt   string `json:"pauseEndsAt"`
		IsPaused      bool   `json:"isSubscriptionPaused"`
	}
	if err := client.get(ctx, "get organization subscription", orgPath(client, "/subscription"), nil, &page,
		needOrganizationRead); err != nil {
		return nil, err
	}
	out := struct {
		OrganizationID int64  `json:"organization_id"`
		PlanID         string `json:"plan_id,omitempty"`
		PlanName       string `json:"plan_name,omitempty"`
		NextBill       string `json:"next_bill,omitempty"`
		IsPaused       bool   `json:"is_paused,omitempty"`
		PauseStartsAt  string `json:"pause_starts_at,omitempty"`
		PauseEndsAt    string `json:"pause_ends_at,omitempty"`
		PriceID        *int64 `json:"price_id,omitempty"`
		Price          string `json:"price,omitempty"`
		PriceCurrency  string `json:"price_currency,omitempty"`
		PricePeriod    string `json:"price_period,omitempty"`
		CouponFrom     string `json:"coupon_valid_from,omitempty"`
		CouponTo       string `json:"coupon_valid_to,omitempty"`
		CouponPrice    string `json:"coupon_price,omitempty"`
		CouponCurrency string `json:"coupon_currency,omitempty"`
		CouponProduct  *int64 `json:"coupon_product_id,omitempty"`
		CouponName     string `json:"coupon_product_name,omitempty"`
	}{OrganizationID: client.scope.orgID, PlanID: scalarText(page.Product.ID), PlanName: bounded(page.Product.Name),
		NextBill: bounded(page.Product.NextBill), IsPaused: page.IsPaused, PauseStartsAt: bounded(page.PauseStartsAt),
		PauseEndsAt: bounded(page.PauseEndsAt)}
	if price := rawObject(page.Product.Price); price != nil {
		out.PriceID = rawInt(price["id"])
		out.Price = rawText(price["price"])
		out.PriceCurrency = rawText(price["currencyCode"])
		out.PricePeriod = rawText(price["period"])
	}
	if coupon := rawObject(page.Product.Coupon); coupon != nil {
		out.CouponFrom = rawText(coupon["validFrom"])
		out.CouponTo = rawText(coupon["validTo"])
		out.CouponPrice = rawText(coupon["price"])
		out.CouponCurrency = rawText(coupon["currencyCode"])
		out.CouponProduct = rawInt(coupon["productId"])
		out.CouponName = rawText(coupon["productName"])
	}
	return &out, nil
}

// scalarText renders a JSON string or number as bounded text and drops everything else.
func scalarText(v any) string {
	switch x := v.(type) {
	case string:
		return bounded(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

// rawObject reads a JSON object and drops everything else.
func rawObject(raw json.RawMessage) map[string]json.RawMessage {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return nil
	}
	return object
}

// rawText renders a JSON string or number as bounded, cleaned text. A number keeps its exact literal, so a
// price or an identifier never takes a float64 detour; everything else is dropped.
func rawText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return cleanText(text, maxTextLength)
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil && len(raw) > 0 && raw[0] != '"' {
		return cleanText(number.String(), maxTextLength)
	}
	return ""
}

// rawInt reads a whole number from a JSON number or a numeric string and drops everything else.
func rawInt(raw json.RawMessage) *int64 {
	text := strings.TrimSpace(rawText(raw))
	if text == "" {
		return nil
	}
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return nil
	}
	return &n
}

// usageInputSchema and usageArguments are shared by make.team.usage and make.organization.usage.
const usageInputSchema = `{"type":"object","properties":{"organization_timezone":{"type":"boolean"}},` +
	`"additionalProperties":false}`

var usageArguments = []capability.Argument{{Name: "organization_timezone", Description: "True to cut the days " +
	"at midnight of the organization's timezone instead of the caller's; false when omitted"}}

// usageQuery maps the optional organization_timezone argument to Make's organizationTimezone query.
func usageQuery(op string, raw json.RawMessage) (url.Values, error) {
	var input struct {
		OrganizationTimezone *bool `json:"organization_timezone"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, providerError(op, "the validated arguments could not be read")
		}
	}
	if input.OrganizationTimezone == nil {
		return nil, nil
	}
	return url.Values{"organizationTimezone": {strconv.FormatBool(*input.OrganizationTimezone)}}, nil
}

func invokeOrganizationUsage(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get organization usage"
	query, err := usageQuery(op, raw)
	if err != nil {
		return nil, err
	}
	client, err := openOrganization(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var page struct {
		Data []struct {
			Date         string `json:"date"`
			Operations   int64  `json:"operations"`
			DataTransfer int64  `json:"dataTransfer"`
			Centicredits int64  `json:"centicredits"`
		} `json:"data"`
	}
	if err := client.get(ctx, op, orgPath(client, "/usage"), query, &page,
		needOrganizationRead); err != nil {
		return nil, err
	}
	days := make([]UsageDay, 0, len(page.Data))
	for _, d := range page.Data {
		if len(days) == maxUsageDays {
			break
		}
		days = append(days, UsageDay{Date: bounded(d.Date), Operations: d.Operations,
			DataTransfer: d.DataTransfer, Centicredits: d.Centicredits})
	}
	return &struct {
		OrganizationID int64      `json:"organization_id"`
		Days           []UsageDay `json:"days"`
		Count          int        `json:"count"`
	}{client.scope.orgID, days, len(days)}, nil
}

// OrganizationPayment is the allow-listed view of one payment: no payment method, no invoice link, no
// product name.
type OrganizationPayment struct {
	ID            string `json:"id"`
	InvoiceNumber *int64 `json:"invoice_number,omitempty"`
	Created       string `json:"created,omitempty"`
	Type          string `json:"type,omitempty"`
	Status        string `json:"status,omitempty"`
	AmountTotal   string `json:"amount_total,omitempty"`
	Currency      string `json:"currency,omitempty"`
	PeriodFrom    string `json:"period_from,omitempty"`
	PeriodTo      string `json:"period_to,omitempty"`
}

func invokeOrganizationPayments(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list organization payments"
	var input struct {
		Offset *int `json:"offset"`
		Limit  *int `json:"limit"`
		// SortDir is the direction of the sort by created.
		SortDir *string `json:"sort_dir"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	offset, limit := 0, defaultPaymentsListed
	if input.Offset != nil {
		offset = *input.Offset
	}
	if input.Limit != nil {
		limit = *input.Limit
	}
	sortDir := "desc"
	if input.SortDir != nil {
		sortDir = *input.SortDir
	}
	if sortDir != "asc" && sortDir != "desc" {
		return nil, invalidRequest("sort_dir must be asc or desc")
	}
	if offset < 0 || limit < 1 || limit > maxPaymentsListed {
		return nil, invalidRequest("offset must be 0 or more and limit between 1 and " + strconv.Itoa(maxPaymentsListed))
	}
	client, err := openOrganization(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var page struct {
		Payments []struct {
			ID            json.RawMessage `json:"id"`
			InvoiceNumber json.RawMessage `json:"invoice_number"`
			Created       string          `json:"created"`
			TypeName      string          `json:"type_name"`
			StatusName    string          `json:"status_name"`
			AmountTotal   any             `json:"amount_total"`
			CurrencyCode  string          `json:"currency_code"`
			PeriodFrom    string          `json:"period_from"`
			PeriodTo      string          `json:"period_to"`
		} `json:"payments"`
	}
	query := url.Values{"pg[offset]": {strconv.Itoa(offset)}, "pg[limit]": {strconv.Itoa(limit)},
		"pg[sortBy]": {"created"}, "pg[sortDir]": {sortDir}}
	if err := client.get(ctx, op, orgPath(client, "/payments"), query, &page, needOrganizationRead); err != nil {
		return nil, err
	}
	payments := make([]OrganizationPayment, 0, len(page.Payments))
	for _, p := range page.Payments {
		if len(payments) == limit {
			break
		}
		payments = append(payments, OrganizationPayment{ID: rawText(p.ID), InvoiceNumber: rawInt(p.InvoiceNumber),
			Created: bounded(p.Created), Type: bounded(p.TypeName), Status: bounded(p.StatusName),
			AmountTotal: scalarText(p.AmountTotal), Currency: bounded(p.CurrencyCode),
			PeriodFrom: bounded(p.PeriodFrom), PeriodTo: bounded(p.PeriodTo)})
	}
	return &struct {
		OrganizationID int64                 `json:"organization_id"`
		Payments       []OrganizationPayment `json:"payments"`
		Offset         int                   `json:"offset"`
		Count          int                   `json:"count"`
		HasMore        bool                  `json:"has_more"`
	}{client.scope.orgID, payments, offset, len(payments), len(page.Payments) >= limit}, nil
}
