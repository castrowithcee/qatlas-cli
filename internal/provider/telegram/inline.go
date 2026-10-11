package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxInlineResults     = 50
	maxInlineIDBytes     = 64
	maxInlineTitleRunes  = 256
	maxInlineDescRunes   = 256
	maxInlineCacheTime   = 3600
	maxInlineQueryRunes  = 256
	maxInlineResultRunes = 64
)

var inlineAnswer = capability.Descriptor{
	ID:      Provider + ".inline.answer",
	Version: 1,
	Title:   "Answer a Telegram inline query",
	Description: "Answer the inline query behind one inline_ref of telegram.updates.list with text articles; " +
		"needs the bot target, and Telegram expects the answer within a few seconds of the query",
	Tags: []string{"telegram", "inline", "answer"},
	Risk: capability.Risk{
		Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity,
	},
	Provider: Provider, RequiresToolAllowList: true,
	Group: groupQueries,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"inline_ref":{"type":"string","minLength":1,"maxLength":2048},` +
		`"results":{"type":"array","minItems":1,"maxItems":50,"items":{"type":"object","properties":{` +
		`"id":{"type":"string","minLength":1,"maxLength":64},"title":{"type":"string","minLength":1,"maxLength":256},` +
		`"description":{"type":"string","maxLength":256},` +
		`"message_text":{"type":"string","minLength":1,"maxLength":4096},` +
		`"parse_mode":{"type":"string","enum":["HTML","MarkdownV2"]}},` +
		`"required":["id","title","message_text"],"additionalProperties":false}},` +
		`"cache_time":{"type":"integer","minimum":0,"maximum":3600},"is_personal":{"type":"boolean"}},` +
		`"required":["inline_ref","results"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"answered":{"type":"boolean"}},` +
		`"required":["answered"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "inline_ref", Description: "inline_ref of an inline_query update from telegram.updates.list", Required: true},
		{Name: "results", Description: "From 1 through 50 text articles, each with a unique id of 1 through 64 bytes, a title, " +
			"an optional description, a message_text of 1 through 4096 characters sent when the user picks it, and an " +
			"optional parse_mode HTML or MarkdownV2; no url, media, or keyboard", Required: true},
		{Name: "cache_time", Description: "Seconds Telegram may cache the answer, from 0 through 3600; Telegram's default when omitted"},
		{Name: "is_personal", Description: "Cache the answer only for the user who sent the query"},
	},
	Fields: []capability.Field{{Name: "answered", Description: "True when Telegram accepted the answer"}},
	Examples: []capability.Example{{
		Description: "Offer one text article for an inline query",
		Arguments:   json.RawMessage(`{"inline_ref":"<inline_ref>","results":[{"id":"1","title":"Hello","message_text":"Hello"}]}`),
	}},
}

type inlineResultArgs struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	MessageText string `json:"message_text"`
	ParseMode   string `json:"parse_mode"`
}

// checkInlineResults is the only gate for the answer content: it runs before any credential or request.
func checkInlineResults(op string, results []inlineResultArgs, cacheTime *int) error {
	if len(results) < 1 || len(results) > maxInlineResults {
		return providerError(op, "results must hold from 1 through 50 articles")
	}
	if cacheTime != nil && (*cacheTime < 0 || *cacheTime > maxInlineCacheTime) {
		return providerError(op, "cache_time must be from 0 through 3600")
	}
	seen := make(map[string]bool, len(results))
	for _, r := range results {
		switch {
		case len(r.ID) < 1 || len(r.ID) > maxInlineIDBytes || !utf8.ValidString(r.ID):
			return providerError(op, "a result id needs 1 through 64 valid bytes")
		case seen[r.ID]:
			return providerError(op, "result ids must be unique")
		case !validRunes(r.Title, 1, maxInlineTitleRunes):
			return providerError(op, "a result title needs 1 through 256 valid characters")
		case !validRunes(r.Description, 0, maxInlineDescRunes):
			return providerError(op, "a result description may have at most 256 valid characters")
		case !validRunes(r.MessageText, 1, maxMessageLength):
			return providerError(op, "a result message_text needs 1 through 4096 valid characters")
		case !validParseMode(r.ParseMode):
			return providerError(op, "parse_mode must be HTML or MarkdownV2")
		}
		seen[r.ID] = true
	}
	return nil
}

func validRunes(s string, minRunes, maxRunes int) bool {
	n := utf8.RuneCountInString(s)
	return utf8.ValidString(s) && n >= minRunes && n <= maxRunes
}

func invokeInlineAnswer(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeInlineAnswerWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeInlineAnswerWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "answer inline query"
	var arguments struct {
		InlineRef  string             `json:"inline_ref"`
		Results    []inlineResultArgs `json:"results"`
		CacheTime  *int               `json:"cache_time"`
		IsPersonal bool               `json:"is_personal"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := checkInlineResults(op, arguments.Results, arguments.CacheTime); err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	if err := requireBotScope(resolved); err != nil {
		return nil, err
	}
	// Format, kind, and binding are checked before the credential is resolved.
	parsed, err := parseRef(resolved, refInlineQuery, arguments.InlineRef)
	if err != nil {
		return nil, err
	}
	// An inline query belongs to no chat, so only a reference issued for the bot target is accepted.
	if parsed.binding != botTarget {
		return nil, providerError(op, "the reference does not belong to the bot target")
	}
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	return client.AnswerInline(ctx, parsed, arguments.Results, arguments.CacheTime, arguments.IsPersonal)
}

// AnswerInline verifies the reference's signature, then performs exactly one answerInlineQuery request. The
// results are rebuilt as article results with plain text content, so no url, media, or keyboard can reach it.
func (c *Client) AnswerInline(ctx context.Context, parsed parsedRef, results []inlineResultArgs, cacheTime *int,
	isPersonal bool) (map[string]any, error) {
	const op = "answer inline query"
	if err := checkInlineResults(op, results, cacheTime); err != nil {
		return nil, err
	}
	if parsed.binding != botTarget {
		return nil, providerError(op, "the reference does not belong to the bot target")
	}
	id, err := c.checkRef(parsed)
	if err != nil {
		return nil, err
	}
	type content struct {
		MessageText string `json:"message_text"`
		ParseMode   string `json:"parse_mode,omitempty"`
	}
	type article struct {
		Type        string  `json:"type"`
		ID          string  `json:"id"`
		Title       string  `json:"title"`
		Description string  `json:"description,omitempty"`
		Content     content `json:"input_message_content"`
	}
	articles := make([]article, len(results))
	for i, r := range results {
		articles[i] = article{"article", r.ID, r.Title, r.Description, content{r.MessageText, r.ParseMode}}
	}
	err = c.confirmed(ctx, spec{op: op, method: "answerInlineQuery", limit: defaultResponseBytes}, struct {
		InlineQueryID string    `json:"inline_query_id"`
		Results       []article `json:"results"`
		CacheTime     *int      `json:"cache_time,omitempty"`
		IsPersonal    bool      `json:"is_personal,omitempty"`
	}{id, articles, cacheTime, isPersonal})
	if err != nil {
		return nil, err
	}
	return map[string]any{"answered": true}, nil
}
