package telegram

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxGiftsLimit    = 100
	maxGiftsOffset   = 256
	giftsResponseMax = 512 << 10
	isPublicSchema   = `"is_public":{"type":"boolean"}`
	isPublicArgument = "Whether the photo is the public one shown to everyone; omit to keep Telegram's default"
)

var businessSetPhoto = capability.Descriptor{
	ID:      Provider + ".business.setphoto",
	Version: 1,
	Title:   "Set the profile photo of a Telegram business account",
	Description: "Replace the profile photo of a bound business account with a static JPEG image from a local file up to " +
		"10 MB in a directory the connection releases for reading; no file reference, URL, or animated photo. The file " +
		"is sent under a neutral name without its path. An unclear outcome is never repeated",
	Tags: []string{"telegram", "business", "profile", "photo", "files"},
	Risk: capability.Risk{
		Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyUnknown,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: businessDataSensitivity,
	},
	Provider: Provider, Group: groupBusiness, LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + businessConnectionSchema + `,"` +
		localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `,` + isPublicSchema + `},` +
		`"required":["business","` + localfile.LocalPathArgument + `"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"}},` +
		`"required":["updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "business", Description: businessArgument, Required: true},
		{Name: localfile.LocalPathArgument, Required: true, Description: "Local JPEG photo (.jpg or .jpeg), absolute " +
			"or starting with ~/, inside a directory the connection releases for reading, up to 10 MB"},
		{Name: "is_public", Description: isPublicArgument},
	},
	Fields: []capability.Field{{Name: "updated", Description: "True when Telegram accepted the photo"}},
	Examples: []capability.Example{{
		Description: "Set the business account photo",
		Arguments:   json.RawMessage(`{"business":"<connection id>","local_path":"~/images/shop.jpg"}`),
	}},
}

var businessRemovePhoto = capability.Descriptor{
	ID:          Provider + ".business.removephoto",
	Version:     1,
	Title:       "Remove the profile photo of a Telegram business account",
	Description: "Remove the profile photo of a bound business account",
	Tags:        []string{"telegram", "business", "profile", "photo", "delete"},
	Risk: capability.Risk{
		Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: businessDataSensitivity,
	},
	Provider: Provider, RequiresToolAllowList: true, Group: groupBusiness,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + businessConnectionSchema + `,` + isPublicSchema + `},` +
		`"required":["business"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
		`"required":["deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "business", Description: businessArgument, Required: true},
		{Name: "is_public", Description: "Whether to remove the public photo instead of the private one; omit to keep Telegram's default"},
	},
	Fields:   []capability.Field{{Name: "deleted", Description: "True when Telegram accepted the removal"}},
	Examples: []capability.Example{{Description: "Remove the business account photo", Arguments: json.RawMessage(`{"business":"<connection id>"}`)}},
}

var businessGifts = capability.Descriptor{
	ID:      Provider + ".business.gifts",
	Version: 1,
	Title:   "List the gifts of a Telegram business account",
	Description: "List the gifts of a bound business account read-only, from 1 through 100 per page. Gift identifiers " +
		"that could move, convert, or upgrade a gift are never returned",
	Tags: []string{"telegram", "business", "gifts", "read"},
	Risk: capability.Risk{
		Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: businessDataSensitivity,
	},
	Provider: Provider, Group: groupBusiness,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + businessConnectionSchema + `,` +
		`"limit":{"type":"integer","minimum":1,"maximum":100},"offset":{"type":"string","minLength":1,"maxLength":256}},` +
		`"required":["business"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"total_count":{"type":"integer"},` +
		`"next_offset":{"type":"string"},"gifts":{"type":"array","items":{"type":"object","properties":{` +
		`"type":{"type":"string"},"gift_id":{"type":"string"},"name":{"type":"string"},"number":{"type":"integer"},` +
		`"send_date":{"type":"integer"},"is_saved":{"type":"boolean"},"text":{"type":"string"},` +
		`"sender":{"type":"object","properties":{"id":{"type":"integer"},"is_bot":{"type":"boolean"},` +
		`"first_name":{"type":"string"},"username":{"type":"string"}}},` +
		`"star_count":{"type":"integer"},"convert_star_count":{"type":"integer"}},"additionalProperties":false}}},` +
		`"required":["total_count","gifts"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "business", Description: businessArgument, Required: true},
		{Name: "limit", Description: "Page size from 1 through 100"},
		{Name: "offset", Description: "next_offset of a previous answer of this tool, passed back unchanged, up to 256 characters"},
	},
	Fields: []capability.Field{
		{Name: "total_count", Description: "Number of gifts of the account"},
		{Name: "next_offset", Description: "Offset of the next page; absent on the last page"},
		{Name: "gifts", Description: "Gifts with type, gift_id, name, number, send_date, is_saved, text, sender, " +
			"star_count, and convert_star_count; star values are read-only numbers"},
	},
	Examples: []capability.Example{{
		Description: "List the first gifts",
		Arguments:   json.RawMessage(`{"business":"<connection id>","limit":20}`),
	}},
}

var businessStarBalance = capability.Descriptor{
	ID:          Provider + ".business.starbalance",
	Version:     1,
	Title:       "Read the Star balance of a Telegram business account",
	Description: "Read the Star balance of a bound business account; nothing can be spent or transferred",
	Tags:        []string{"telegram", "business", "stars", "read"},
	Risk: capability.Risk{
		Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: businessDataSensitivity,
	},
	Provider: Provider, Group: groupBusiness,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + businessConnectionSchema + `},` +
		`"required":["business"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"amount":{"type":"integer"},` +
		`"nanostar_amount":{"type":"integer"}},"required":["amount","nanostar_amount"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "business", Description: businessArgument, Required: true}},
	Fields: []capability.Field{
		{Name: "amount", Description: "Whole Stars"},
		{Name: "nanostar_amount", Description: "Additional billionths of a Star"},
	},
	Examples: []capability.Example{{Description: "Read the Star balance", Arguments: json.RawMessage(`{"business":"<connection id>"}`)}},
}

func invokeBusinessSetPhoto(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeBusinessSetPhotoWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeBusinessSetPhotoWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "set business profile photo"
	var arguments struct {
		Business  string  `json:"business"`
		LocalPath *string `json:"local_path"`
		IsPublic  *bool   `json:"is_public"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if arguments.LocalPath == nil {
		return nil, providerError(op, "local_path is required")
	}
	if err := scopeBusiness(op, resolved, arguments.Business); err != nil {
		return nil, err
	}
	// Telegram accepts only a static JPEG here; the extension check keeps animated files out before any read.
	switch strings.ToLower(filepath.Ext(*arguments.LocalPath)) {
	case ".jpg", ".jpeg":
	default:
		return nil, providerError(op, "the profile photo must be a .jpg or .jpeg file")
	}
	// The release check and the size check happen before the credential is resolved or any byte is read.
	prepared, err := prepareSources(ctx, resolved, op, "", []string{kindPhoto}, []mediaSource{{LocalPath: arguments.LocalPath}})
	if err != nil {
		return nil, err
	}
	defer closeUploads(prepared)
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	items, err := client.load(op, prepared)
	if err != nil {
		return nil, err
	}
	return client.SetBusinessPhoto(ctx, arguments.Business, items[0], arguments.IsPublic)
}

// SetBusinessPhoto performs exactly one multipart setBusinessAccountProfilePhoto request.
func (c *Client) SetBusinessPhoto(ctx context.Context, id string, item mediaItem, isPublic *bool) (map[string]any, error) {
	const op = "set business profile photo"
	if !validBusinessID(id) || !c.set.hasBusiness(id) {
		return nil, providerError(op, "this connection does not bind that business connection")
	}
	if len(item.data) == 0 || len(item.data) > maxPhotoBytes {
		return nil, providerError(op, "the photo is empty or larger than Telegram accepts")
	}
	input, err := json.Marshal(struct {
		Type  string `json:"type"`
		Photo string `json:"photo"`
	}{"static", "attach://" + profileAttachName})
	if err != nil {
		return nil, providerError(op, "the request could not be encoded")
	}
	fields := []field{{"business_connection_id", id}, {"photo", string(input)}}
	if isPublic != nil {
		fields = append(fields, field{"is_public", strconv.FormatBool(*isPublic)})
	}
	s := spec{op: op, method: "setBusinessAccountProfilePhoto", limit: defaultResponseBytes}
	raw, err := c.withUploadTimeout().callMultipart(ctx, s, fields,
		[]filePart{{field: profileAttachName, name: item.name, data: item.data}})
	if err != nil {
		return nil, err
	}
	var ok bool
	if json.Unmarshal(raw, &ok) != nil || !ok {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	return map[string]any{"updated": true}, nil
}

func invokeBusinessRemovePhoto(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeBusinessRemovePhotoWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeBusinessRemovePhotoWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "remove business profile photo"
	var arguments struct {
		Business string `json:"business"`
		IsPublic *bool  `json:"is_public"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := scopeBusiness(op, resolved, arguments.Business); err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	return client.RemoveBusinessPhoto(ctx, arguments.Business, arguments.IsPublic)
}

// RemoveBusinessPhoto performs exactly one removeBusinessAccountProfilePhoto request.
func (c *Client) RemoveBusinessPhoto(ctx context.Context, id string, isPublic *bool) (map[string]any, error) {
	const op = "remove business profile photo"
	if !validBusinessID(id) || !c.set.hasBusiness(id) {
		return nil, providerError(op, "this connection does not bind that business connection")
	}
	err := c.confirmed(ctx, spec{op: op, method: "removeBusinessAccountProfilePhoto", limit: defaultResponseBytes}, struct {
		ID       string `json:"business_connection_id"`
		IsPublic *bool  `json:"is_public,omitempty"`
	}{id, isPublic})
	if err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

type tgOwnedGift struct {
	Type             string  `json:"type"`
	SendDate         int64   `json:"send_date"`
	IsSaved          bool    `json:"is_saved"`
	Text             string  `json:"text"`
	SenderUser       *tgUser `json:"sender_user"`
	ConvertStarCount int64   `json:"convert_star_count"`
	Gift             struct {
		ID        string `json:"id"`
		GiftID    string `json:"gift_id"`
		BaseName  string `json:"base_name"`
		Number    int64  `json:"number"`
		StarCount int64  `json:"star_count"`
	} `json:"gift"`
}

// giftOut deliberately omits owned_gift_id and every transfer, upgrade, or price-for-action field: nothing
// returned here can address a financial action.
type giftOut struct {
	Type             string     `json:"type"`
	GiftID           string     `json:"gift_id,omitempty"`
	Name             string     `json:"name,omitempty"`
	Number           int64      `json:"number,omitempty"`
	SendDate         int64      `json:"send_date,omitempty"`
	IsSaved          bool       `json:"is_saved"`
	Text             string     `json:"text,omitempty"`
	Sender           *senderOut `json:"sender,omitempty"`
	StarCount        int64      `json:"star_count,omitempty"`
	ConvertStarCount int64      `json:"convert_star_count,omitempty"`
}

type giftsOut struct {
	TotalCount int64     `json:"total_count"`
	NextOffset string    `json:"next_offset,omitempty"`
	Gifts      []giftOut `json:"gifts"`
}

func invokeBusinessGifts(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeBusinessGiftsWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeBusinessGiftsWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "list business gifts"
	var arguments struct {
		Business string `json:"business"`
		Limit    *int   `json:"limit"`
		Offset   string `json:"offset"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if arguments.Limit != nil && (*arguments.Limit < 1 || *arguments.Limit > maxGiftsLimit) {
		return nil, providerError(op, "limit must be from 1 through 100")
	}
	if len([]rune(arguments.Offset)) > maxGiftsOffset {
		return nil, providerError(op, "offset is too long")
	}
	if err := scopeBusiness(op, resolved, arguments.Business); err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	return client.ListBusinessGifts(ctx, arguments.Business, arguments.Limit, arguments.Offset)
}

// ListBusinessGifts performs one getBusinessAccountGifts request; no filter or sort parameter is ever sent.
func (c *Client) ListBusinessGifts(ctx context.Context, id string, limit *int, offset string) (*giftsOut, error) {
	const op = "list business gifts"
	if !validBusinessID(id) || !c.set.hasBusiness(id) {
		return nil, providerError(op, "this connection does not bind that business connection")
	}
	raw, err := c.call(ctx, spec{op: op, method: "getBusinessAccountGifts", limit: giftsResponseMax, readOnly: true},
		struct {
			ID     string `json:"business_connection_id"`
			Offset string `json:"offset,omitempty"`
			Limit  *int   `json:"limit,omitempty"`
		}{id, offset, limit})
	if err != nil {
		return nil, err
	}
	var got struct {
		TotalCount int64         `json:"total_count"`
		Gifts      []tgOwnedGift `json:"gifts"`
		NextOffset string        `json:"next_offset"`
	}
	if json.Unmarshal(raw, &got) != nil {
		return nil, invalidResponse(op)
	}
	out := &giftsOut{TotalCount: got.TotalCount, NextOffset: capText(got.NextOffset, maxGiftsOffset), Gifts: []giftOut{}}
	for i, g := range got.Gifts {
		if i >= maxGiftsLimit {
			break
		}
		item := giftOut{Type: capText(g.Type, maxNameRunes), SendDate: g.SendDate, IsSaved: g.IsSaved,
			Text: capText(g.Text, maxNameRunes), Sender: sender(g.SenderUser), ConvertStarCount: g.ConvertStarCount,
			GiftID: capText(cmp.Or(g.Gift.ID, g.Gift.GiftID), maxNameRunes), Name: capText(g.Gift.BaseName, maxNameRunes),
			Number: g.Gift.Number, StarCount: g.Gift.StarCount}
		out.Gifts = append(out.Gifts, item)
	}
	return out, nil
}

func invokeBusinessStarBalance(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeBusinessStarBalanceWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeBusinessStarBalanceWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "get business star balance"
	var arguments struct {
		Business string `json:"business"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := scopeBusiness(op, resolved, arguments.Business); err != nil {
		return nil, err
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	return client.GetBusinessStarBalance(ctx, arguments.Business)
}

// GetBusinessStarBalance performs one getBusinessAccountStarBalance request.
func (c *Client) GetBusinessStarBalance(ctx context.Context, id string) (map[string]any, error) {
	const op = "get business star balance"
	if !validBusinessID(id) || !c.set.hasBusiness(id) {
		return nil, providerError(op, "this connection does not bind that business connection")
	}
	raw, err := c.call(ctx, spec{op: op, method: "getBusinessAccountStarBalance", limit: defaultResponseBytes, readOnly: true},
		struct {
			ID string `json:"business_connection_id"`
		}{id})
	if err != nil {
		return nil, err
	}
	var got struct {
		Amount         int64 `json:"amount"`
		NanostarAmount int64 `json:"nanostar_amount"`
	}
	if json.Unmarshal(raw, &got) != nil {
		return nil, invalidResponse(op)
	}
	return map[string]any{"amount": got.Amount, "nanostar_amount": got.NanostarAmount}, nil
}
