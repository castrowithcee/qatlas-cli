package telegram

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	groupPayments = "payments"
	// financialDataSensitivity classifies Star balances, Star transactions, and gifts, which carry a Star value.
	financialDataSensitivity = "telegram-financial"
	maxTransactionOffset     = 1 << 20
)

const (
	limitSchema      = `"limit":{"type":"integer","minimum":1,"maximum":100}`
	giftOffsetSchema = `"offset":{"type":"string","minLength":1,"maxLength":256}`
)

var (
	limitArgument      = capability.Argument{Name: "limit", Description: "Page size from 1 through 100"}
	giftOffsetArgument = capability.Argument{Name: "offset",
		Description: "next_offset of a previous answer of this tool, passed back unchanged, up to 256 characters"}
	giftsListField = capability.Field{Name: "gifts", Description: "Gifts with type, gift_id, name, number, send_date, " +
		"is_saved, text, sender, star_count, and convert_star_count; star values are read-only numbers"}
	giftsPageFields = []capability.Field{
		{Name: "total_count", Description: "Number of gifts of the owner"},
		{Name: "next_offset", Description: "Offset of the next page; absent on the last page"},
		giftsListField,
	}
)

func paymentsRisk() capability.Risk { return readRisk(financialDataSensitivity) }

var paymentsStarBalance = capability.Descriptor{
	ID: Provider + ".payments.starbalance", Version: 1, Title: "Read the Star balance of the Telegram bot",
	Description: "Read the Star balance of the bot; needs the bot target, and nothing can be spent or transferred",
	Tags:        []string{"telegram", "payments", "stars", "read"}, Risk: paymentsRisk(), Provider: Provider,
	Group: groupPayments, InputSchema: json.RawMessage(noArguments), OutputSchema: businessStarBalance.OutputSchema,
	Fields:   businessStarBalance.Fields,
	Examples: []capability.Example{{Description: "Read the Star balance", Arguments: json.RawMessage(`{}`)}},
}

var paymentsStarTransactions = capability.Descriptor{
	ID: Provider + ".payments.startransactions", Version: 1, Title: "List the Star transactions of the Telegram bot",
	Description: "List Star transactions of the bot read-only, from 1 through 100 per page; needs the bot target. " +
		"Counterparties show only their type and numeric identifier",
	Tags: []string{"telegram", "payments", "stars", "read"}, Risk: paymentsRisk(), Provider: Provider,
	Group: groupPayments,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"offset":{"type":"integer","minimum":0},` +
		limitSchema + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"transactions":{"type":"array","items":` +
		`{"type":"object","properties":{"id":{"type":"string"},"amount":{"type":"integer"},` +
		`"nanostar_amount":{"type":"integer"},"date":{"type":"integer"},` +
		`"source":` + partnerSchema + `,"receiver":` + partnerSchema + `},"additionalProperties":false}}},` +
		`"required":["transactions"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "offset", Description: "Number of transactions to skip, starting at zero"}, limitArgument},
	Fields: []capability.Field{{Name: "transactions",
		Description: "Transactions with id, amount, nanostar_amount, date, and the source and receiver partner"}},
	Examples: []capability.Example{{Description: "List the latest transactions", Arguments: json.RawMessage(`{"limit":20}`)}},
}

const partnerSchema = `{"type":"object","properties":{"type":{"type":"string"},"id":{"type":"integer"}},` +
	`"required":["type"],"additionalProperties":false}`

var giftsAvailable = capability.Descriptor{
	ID: Provider + ".gifts.available", Version: 1, Title: "List the gifts Telegram offers for sale",
	Description: "Read the public catalog of gifts that can be sent; needs the bot target, and nothing is bought or sent",
	Tags:        []string{"telegram", "gifts", "read"}, Risk: paymentsRisk(), Provider: Provider, Group: groupPayments,
	InputSchema: json.RawMessage(noArguments),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"gifts":{"type":"array","items":` +
		`{"type":"object","properties":{"gift_id":{"type":"string"},"star_count":{"type":"integer"},` +
		`"total_count":{"type":"integer"},"remaining_count":{"type":"integer"}},"additionalProperties":false}}},` +
		`"required":["gifts"],"additionalProperties":false}`),
	Fields: []capability.Field{{Name: "gifts",
		Description: "Catalog entries with gift_id, star_count, total_count, and remaining_count"}},
	Examples: []capability.Example{{Description: "List the catalog", Arguments: json.RawMessage(`{}`)}},
}

var giftsChat = capability.Descriptor{
	ID: Provider + ".gifts.chat", Version: 1, Title: "List the gifts of a Telegram chat",
	Description: "List the gifts of a bound chat read-only, from 1 through 100 per page. Gift identifiers that " +
		"could move, convert, or upgrade a gift are never returned",
	Tags: []string{"telegram", "gifts", "read"}, Risk: paymentsRisk(), Provider: Provider, Group: groupPayments,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + limitSchema + `,` +
		giftOffsetSchema + `},"additionalProperties":false}`),
	OutputSchema: businessGifts.OutputSchema,
	Arguments:    []capability.Argument{chatArgument, limitArgument, giftOffsetArgument},
	Fields:       giftsPageFields,
	Examples:     []capability.Example{{Description: "List the first gifts", Arguments: json.RawMessage(`{"limit":20}`)}},
}

var giftsUser = capability.Descriptor{
	ID: Provider + ".gifts.user", Version: 1, Title: "List the gifts of a chat member",
	Description: "List the gifts of a user verified as a member of a bound chat read-only, from 1 through 100 per " +
		"page. Gift identifiers that could move, convert, or upgrade a gift are never returned",
	Tags: []string{"telegram", "gifts", "read"}, Risk: paymentsRisk(), Provider: Provider, Group: groupPayments,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + chatSchema + `,` + userIDSchema + `,` +
		limitSchema + `,` + giftOffsetSchema + `},"required":["user_id"],"additionalProperties":false}`),
	OutputSchema: businessGifts.OutputSchema,
	Arguments:    []capability.Argument{chatArgument, userIDArgument, limitArgument, giftOffsetArgument},
	Fields:       giftsPageFields,
	Examples:     []capability.Example{{Description: "List the first gifts", Arguments: json.RawMessage(`{"user_id":123,"limit":20}`)}},
}

type partnerOut struct {
	Type string `json:"type"`
	ID   int64  `json:"id,omitempty"`
}

type transactionOut struct {
	ID             string      `json:"id"`
	Amount         int64       `json:"amount"`
	NanostarAmount int64       `json:"nanostar_amount"`
	Date           int64       `json:"date"`
	Source         *partnerOut `json:"source,omitempty"`
	Receiver       *partnerOut `json:"receiver,omitempty"`
}

type tgPartner struct {
	Type string `json:"type"`
	User *struct {
		ID int64 `json:"id"`
	} `json:"user"`
	Chat *struct {
		ID int64 `json:"id"`
	} `json:"chat"`
}

// partner keeps only the partner type and, for a user or chat partner, its numeric identifier; names, payloads,
// and every other field of the partner are dropped.
func partner(p *tgPartner) *partnerOut {
	if p == nil || p.Type == "" {
		return nil
	}
	out := &partnerOut{Type: capText(p.Type, maxNameRunes)}
	switch {
	case p.Type == "user" && p.User != nil:
		out.ID = p.User.ID
	case p.Type == "chat" && p.Chat != nil:
		out.ID = p.Chat.ID
	}
	return out
}

func invokePaymentsStarBalance(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage) (any, error) {
	return invokePaymentsStarBalanceWith(ctx, r, s, red, raw, newHTTPClient())
}

func invokePaymentsStarBalanceWith(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage, httpClient *http.Client) (any, error) {
	c, err := openBotWide(ctx, r, s, red, httpClient, "get bot star balance", raw, &struct{}{})
	if err != nil {
		return nil, err
	}
	return c.GetStarBalance(ctx)
}

func invokePaymentsStarTransactions(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage) (any, error) {
	return invokePaymentsStarTransactionsWith(ctx, r, s, red, raw, newHTTPClient())
}

func invokePaymentsStarTransactionsWith(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "list star transactions"
	var a struct {
		Offset *int `json:"offset"`
		Limit  *int `json:"limit"`
	}
	if decodeStrict(raw, &a) != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if a.Offset != nil && (*a.Offset < 0 || *a.Offset > maxTransactionOffset) {
		return nil, providerError(op, "offset is out of range")
	}
	if err := checkGiftLimit(op, a.Limit); err != nil {
		return nil, err
	}
	c, err := openBotWide(ctx, r, s, red, httpClient, op, nil, nil)
	if err != nil {
		return nil, err
	}
	return c.ListStarTransactions(ctx, a.Offset, a.Limit)
}

func invokeGiftsAvailable(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage) (any, error) {
	return invokeGiftsAvailableWith(ctx, r, s, red, raw, newHTTPClient())
}

func invokeGiftsAvailableWith(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage, httpClient *http.Client) (any, error) {
	c, err := openBotWide(ctx, r, s, red, httpClient, "list available gifts", raw, &struct{}{})
	if err != nil {
		return nil, err
	}
	return c.ListAvailableGifts(ctx)
}

// openBotWide checks the bot target before the credential is resolved. When into is given, the arguments are
// decoded into it first.
func openBotWide(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor, httpClient *http.Client,
	op string, raw json.RawMessage, into any) (*Client, error) {
	if into != nil && decodeStrict(raw, into) != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if r == nil {
		return nil, providerError(op, "no connection was selected")
	}
	if err := requireBotScope(r); err != nil {
		return nil, err
	}
	return openWithHTTP(ctx, r, s, red, httpClient)
}

func checkGiftLimit(op string, limit *int) error {
	if limit != nil && (*limit < 1 || *limit > maxGiftsLimit) {
		return providerError(op, "limit must be from 1 through 100")
	}
	return nil
}

type giftPageArgs struct {
	Chat   string `json:"chat"`
	Limit  *int   `json:"limit"`
	Offset string `json:"offset"`
}

type giftUserArgs struct {
	giftPageArgs
	UserID int64 `json:"user_id"`
}

func (a giftPageArgs) check(op string) error {
	if err := checkGiftLimit(op, a.Limit); err != nil {
		return err
	}
	if len([]rune(a.Offset)) > maxGiftsOffset {
		return providerError(op, "offset is too long")
	}
	return nil
}

func invokeGiftsChat(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage) (any, error) {
	return invokeGiftsChatWith(ctx, r, s, red, raw, newHTTPClient())
}

func invokeGiftsChatWith(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "list chat gifts"
	var a giftPageArgs
	if decodeStrict(raw, &a) != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := a.check(op); err != nil {
		return nil, err
	}
	c, err := openChatWithHTTP(ctx, r, s, red, a.Chat, httpClient)
	if err != nil {
		return nil, err
	}
	return c.ListOwnedGifts(ctx, op, "getChatGifts", struct {
		ChatID string `json:"chat_id"`
		Offset string `json:"offset,omitempty"`
		Limit  *int   `json:"limit,omitempty"`
	}{c.target, a.Offset, a.Limit})
}

func invokeGiftsUser(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage) (any, error) {
	return invokeGiftsUserWith(ctx, r, s, red, raw, newHTTPClient())
}

func invokeGiftsUserWith(ctx context.Context, r *config.Resolved, s *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "list user gifts"
	var a giftUserArgs
	if decodeStrict(raw, &a) != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if a.UserID <= 0 {
		return nil, providerError(op, "invalid user")
	}
	if err := a.check(op); err != nil {
		return nil, err
	}
	c, err := openChatWithHTTP(ctx, r, s, red, a.Chat, httpClient)
	if err != nil {
		return nil, err
	}
	if err := c.requireUserMember(ctx, a.UserID); err != nil {
		return nil, err
	}
	return c.ListOwnedGifts(ctx, op, "getUserGifts", struct {
		UserID int64  `json:"user_id"`
		Offset string `json:"offset,omitempty"`
		Limit  *int   `json:"limit,omitempty"`
	}{a.UserID, a.Offset, a.Limit})
}

// ListOwnedGifts performs one read-only gifts request; no filter or sort parameter is ever sent.
func (c *Client) ListOwnedGifts(ctx context.Context, op, method string, body any) (*giftsOut, error) {
	raw, err := c.call(ctx, spec{op: op, method: method, limit: giftsResponseMax, readOnly: true}, body)
	if err != nil {
		return nil, err
	}
	return decodeOwnedGifts(op, raw)
}

// GetStarBalance performs one getMyStarBalance request.
func (c *Client) GetStarBalance(ctx context.Context) (map[string]any, error) {
	const op = "get bot star balance"
	raw, err := c.call(ctx, spec{op: op, method: "getMyStarBalance", limit: defaultResponseBytes, readOnly: true},
		struct{}{})
	if err != nil {
		return nil, err
	}
	return decodeStarAmount(op, raw)
}

// ListStarTransactions performs one getStarTransactions request.
func (c *Client) ListStarTransactions(ctx context.Context, offset, limit *int) (any, error) {
	const op = "list star transactions"
	raw, err := c.call(ctx, spec{op: op, method: "getStarTransactions", limit: giftsResponseMax, readOnly: true},
		struct {
			Offset *int `json:"offset,omitempty"`
			Limit  *int `json:"limit,omitempty"`
		}{offset, limit})
	if err != nil {
		return nil, err
	}
	var got struct {
		Transactions []struct {
			ID             string     `json:"id"`
			Amount         int64      `json:"amount"`
			NanostarAmount int64      `json:"nanostar_amount"`
			Date           int64      `json:"date"`
			Source         *tgPartner `json:"source"`
			Receiver       *tgPartner `json:"receiver"`
		} `json:"transactions"`
	}
	if json.Unmarshal(raw, &got) != nil {
		return nil, invalidResponse(op)
	}
	out := make([]transactionOut, 0, min(len(got.Transactions), maxGiftsLimit))
	for _, t := range got.Transactions {
		if len(out) == maxGiftsLimit {
			break
		}
		out = append(out, transactionOut{capText(t.ID, maxNameRunes), t.Amount, t.NanostarAmount, t.Date,
			partner(t.Source), partner(t.Receiver)})
	}
	return struct {
		Transactions []transactionOut `json:"transactions"`
	}{out}, nil
}

// ListAvailableGifts performs one getAvailableGifts request; sticker files and upgrade prices are dropped.
func (c *Client) ListAvailableGifts(ctx context.Context) (any, error) {
	const op = "list available gifts"
	raw, err := c.call(ctx, spec{op: op, method: "getAvailableGifts", limit: giftsResponseMax, readOnly: true}, struct{}{})
	if err != nil {
		return nil, err
	}
	var got struct {
		Gifts []struct {
			ID             string `json:"id"`
			StarCount      int64  `json:"star_count"`
			TotalCount     int64  `json:"total_count"`
			RemainingCount int64  `json:"remaining_count"`
		} `json:"gifts"`
	}
	if json.Unmarshal(raw, &got) != nil {
		return nil, invalidResponse(op)
	}
	type item struct {
		GiftID         string `json:"gift_id"`
		StarCount      int64  `json:"star_count"`
		TotalCount     int64  `json:"total_count,omitempty"`
		RemainingCount int64  `json:"remaining_count,omitempty"`
	}
	out := make([]item, 0, min(len(got.Gifts), 256))
	for _, g := range got.Gifts {
		if len(out) == 256 {
			break
		}
		out = append(out, item{capText(g.ID, maxNameRunes), g.StarCount, g.TotalCount, g.RemainingCount})
	}
	return struct {
		Gifts []item `json:"gifts"`
	}{out}, nil
}
