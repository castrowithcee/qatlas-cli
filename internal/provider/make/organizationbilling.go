package makeapi

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// make.organization.subscription, .usage and .payments read financial data of the organization an
// organization-mode connection is bound to; the organization is always the connection's target. Read only.
// API (checked 2026-10-06 against developers.make.com's published API reference, not a live account), all
// organizations:read:
//   - GET /organizations/{organizationId}/subscription: product{id,name,nextBill,price}, pauseStartsAt,
//     pauseEndsAt, isSubscriptionPaused.
//   - GET /organizations/{organizationId}/usage: {"data":[{date,operations,dataTransfer,centicredits}]}, 30 days.
//   - GET /organizations/{organizationId}/payments?pg[offset]&pg[limit]: {"payments":[{id,invoice_number,
//     created,type_name,status_name,payment_method,amount_total,currency_code,invoice_url,period_from,
//     period_to}]}.
// Narrower readings: product.price (shape undocumented), payment_method and invoice_url are never returned;
// the payment-method endpoints, discounts and every write on the subscription are not offered.

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
	Description: "Read plan name, next billing date, and pause state of the subscription of the organization " +
		"this connection is bound to; takes no arguments, changes nothing, returns no payment method. " +
		"Financial data. Only on an organization connection. Needs the organizations:read scope",
	Tags: []string{"make", "organization", "subscription", "billing"}, Risk: billingReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(emptyInput),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization_id":{"type":"integer"},` +
		`"plan_id":{"type":"string"},"plan_name":{"type":"string"},"next_bill":{"type":"string"},` +
		`"is_paused":{"type":"boolean"},"pause_starts_at":{"type":"string"},"pause_ends_at":{"type":"string"}},` +
		`"required":["organization_id"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "organization_id", Description: "The bound organization"},
		{Name: "plan_id", Description: "Plan identifier, untrusted"}, {Name: "plan_name", Description: "Plan name, untrusted"},
		{Name: "next_bill", Description: "Next billing date as Make reports it, untrusted"},
		{Name: "is_paused", Description: "True when the subscription is paused"},
		{Name: "pause_starts_at", Description: "Scheduled pause start, untrusted"},
		{Name: "pause_ends_at", Description: "Scheduled pause end, untrusted"}},
	Examples: []capability.Example{{Description: "Read the subscription", Arguments: json.RawMessage(`{}`)}},
}

var organizationUsage = capability.Descriptor{
	ID: Provider + ".organization.usage", Version: 1, Title: "Read the usage of the bound Make organization",
	Description: "Read the daily operations, data transfer, and centicredits of the organization this " +
		"connection is bound to for Make's last 30 days; takes no arguments, changes nothing. Financial data. " +
		"Only on an organization connection. Needs the organizations:read scope",
	Tags: []string{"make", "organization", "usage", "billing"}, Risk: billingReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(emptyInput),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization_id":{"type":"integer"},` +
		`"days":{"type":"array","items":{"type":"object","properties":{"date":{"type":"string"},` +
		`"operations":{"type":"integer"},"data_transfer":{"type":"integer"},"centicredits":{"type":"integer"}},` +
		`"required":["date"],"additionalProperties":false}},"count":{"type":"integer"}},` +
		`"required":["organization_id","days","count"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "organization_id", Description: "The bound organization"},
		{Name: "days", Description: "date (untrusted), operations, data_transfer in bytes, centicredits"},
		{Name: "count", Description: "Days returned, at most " + strconv.Itoa(maxUsageDays)}},
	Examples: []capability.Example{{Description: "Read the usage", Arguments: json.RawMessage(`{}`)}},
}

var organizationPayments = capability.Descriptor{
	ID: Provider + ".organization.payments", Version: 1, Title: "List the invoices of the bound Make organization",
	Description: "List invoice number, date, type, status, amount, currency, and period of the payments of the " +
		"organization this connection is bound to, newest first, page by page; changes nothing and returns " +
		"neither payment method nor invoice link. Financial data. Only on an organization connection. Needs " +
		"the organizations:read scope",
	Tags: []string{"make", "organization", "payments", "billing"}, Risk: billingReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"offset":{"type":"integer","minimum":0},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxPaymentsListed) + `}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"organization_id":{"type":"integer"},` +
		`"payments":{"type":"array","items":{"type":"object","properties":{"id":{"type":"integer"},` +
		`"invoice_number":{"type":"string"},"created":{"type":"string"},"type":{"type":"string"},` +
		`"status":{"type":"string"},"amount_total":{"type":"string"},"currency":{"type":"string"},` +
		`"period_from":{"type":"string"},"period_to":{"type":"string"}},"required":["id"],"additionalProperties":false}},` +
		`"offset":{"type":"integer"},"count":{"type":"integer"},"has_more":{"type":"boolean"}},` +
		`"required":["organization_id","payments","offset","count","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "offset", Description: "Payments to skip, 0 when omitted"},
		{Name: "limit", Description: "Payments per page, 1 to " + strconv.Itoa(maxPaymentsListed) + "; " +
			strconv.Itoa(defaultPaymentsListed) + " when omitted"}},
	Fields: []capability.Field{
		{Name: "organization_id", Description: "The bound organization"},
		{Name: "payments", Description: "id, invoice_number, created, type, status, amount_total, currency, period_from, period_to (all text untrusted)"},
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
	}{client.scope.orgID, scalarText(page.Product.ID), bounded(page.Product.Name), bounded(page.Product.NextBill),
		page.IsPaused, bounded(page.PauseStartsAt), bounded(page.PauseEndsAt)}
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

func invokeOrganizationUsage(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
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
	if err := client.get(ctx, "get organization usage", orgPath(client, "/usage"), nil, &page,
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

// OrganizationPayment is the allow-listed view of one payment: no payment method, no invoice link.
type OrganizationPayment struct {
	ID            int64  `json:"id"`
	InvoiceNumber string `json:"invoice_number,omitempty"`
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
	if offset < 0 || limit < 1 || limit > maxPaymentsListed {
		return nil, invalidRequest("offset must be 0 or more and limit between 1 and " + strconv.Itoa(maxPaymentsListed))
	}
	client, err := openOrganization(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var page struct {
		Payments []struct {
			ID            int64  `json:"id"`
			InvoiceNumber string `json:"invoice_number"`
			Created       string `json:"created"`
			TypeName      string `json:"type_name"`
			StatusName    string `json:"status_name"`
			AmountTotal   any    `json:"amount_total"`
			CurrencyCode  string `json:"currency_code"`
			PeriodFrom    string `json:"period_from"`
			PeriodTo      string `json:"period_to"`
		} `json:"payments"`
	}
	query := url.Values{"pg[offset]": {strconv.Itoa(offset)}, "pg[limit]": {strconv.Itoa(limit)},
		"pg[sortBy]": {"created"}, "pg[sortDir]": {"desc"}}
	if err := client.get(ctx, op, orgPath(client, "/payments"), query, &page, needOrganizationRead); err != nil {
		return nil, err
	}
	payments := make([]OrganizationPayment, 0, len(page.Payments))
	for _, p := range page.Payments {
		if len(payments) == limit {
			break
		}
		payments = append(payments, OrganizationPayment{ID: p.ID, InvoiceNumber: bounded(p.InvoiceNumber),
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
