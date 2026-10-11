package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const emojiCatalog = "the kChat custom emoji catalog"

var emojiSchema = `{"type":"object","properties":{` +
	`"id":` + idSchema + `,"name":{"type":"string"},"creator_id":{"type":"string"},"created_at":{"type":"string"}},` +
	`"required":["id","name","creator_id"],"additionalProperties":false}`

var emojiList = capability.Descriptor{
	ID:      Provider + ".emoji.list",
	Version: 1,
	Title:   "List Infomaniak kChat custom emoji",
	Description: "List the custom emoji of the kChat instance sorted by name, one kChat page at a time, with ID, " +
		"name, creator, and creation time; no images. Instance-wide: only on a connection that binds every team " +
		"of its token and has no channel target.",
	Tags:     []string{"infomaniak", "kchat", "emoji", "administration", "list"},
	Risk:     botRisk(capability.EffectRead, capability.IdempotencySafe, capability.ConfirmationNone),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"page":{"type":"integer","minimum":1},` +
		`"per_page":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"emoji":{"type":"array","items":` + emojiSchema + `},` +
		`"page":{"type":"integer"},"per_page":{"type":"integer"},"count":{"type":"integer"},` +
		`"has_more":{"type":"boolean"}},` +
		`"required":["emoji","page","per_page","count","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "page", Description: "1-based page of kChat's own listing; the first page when omitted"},
		{Name: "per_page", Description: "Emoji per kChat page, 1 to " + itoa(maxListLimit) + "; " +
			itoa(defaultListLimit) + " when omitted"},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the custom emoji"},
		{Name: "name", Description: "Emoji name, untrusted data"},
		{Name: "creator_id", Description: "User identifier of the creator"},
		{Name: "created_at", Description: "Creation time in RFC 3339, when kChat reports one"},
		{Name: "page", Description: "Page that was read"},
		{Name: "per_page", Description: "Page size that was requested"},
		{Name: "count", Description: "Number of emoji reported on this page"},
		{Name: "has_more", Description: "True when kChat's page was full, so a further page may " +
			"exist; pages are never read on by themselves"},
	},
	Examples: []capability.Example{{Description: "List the custom emoji", Arguments: json.RawMessage(`{}`)}},
}

// emojiJSON is the subset of the kChat Emoji resource this provider reads.
type emojiJSON struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatorID string `json:"creator_id"`
	CreateAt  int64  `json:"create_at"`
}

// EmojiEntry is the stable Qatlas view of one custom emoji.
type EmojiEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatorID string `json:"creator_id"`
	CreatedAt string `json:"created_at,omitempty"`
}

// EmojiPage is one page of the custom emoji of the instance.
type EmojiPage struct {
	Emoji   []EmojiEntry `json:"emoji"`
	Page    int          `json:"page"`
	PerPage int          `json:"per_page"`
	Count   int          `json:"count"`
	HasMore bool         `json:"has_more"`
}

func invokeEmojiList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list emoji"
	var input botArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if input.Page == 0 {
		input.Page = 1
	}
	if input.PerPage == 0 {
		input.PerPage = defaultListLimit
	}
	client, err := openInstance(ctx, resolved, secrets, red, op, emojiCatalog)
	if err != nil {
		return nil, err
	}
	return client.ListEmoji(ctx, input.Page, input.PerPage)
}

// ListEmoji reads exactly one name-sorted page of kChat's custom emoji and never reads on by itself.
func (c *Client) ListEmoji(ctx context.Context, page, perPage int) (*EmojiPage, error) {
	const op = "list emoji"
	query := url.Values{"page": {strconv.Itoa(page - 1)}, "per_page": {strconv.Itoa(perPage)}, "sort": {"name"}}
	var emoji []emojiJSON
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/emoji", query, nil, &emoji, false); err != nil {
		return nil, err
	}
	entries := make([]EmojiEntry, 0, len(emoji))
	for i := range emoji {
		if !validMattermostID(emoji[i].ID) {
			continue
		}
		entries = append(entries, EmojiEntry{ID: emoji[i].ID, Name: bounded(emoji[i].Name),
			CreatorID: bounded(emoji[i].CreatorID), CreatedAt: msToRFC3339(emoji[i].CreateAt)})
	}
	return &EmojiPage{Emoji: entries, Page: page, PerPage: perPage, Count: len(entries),
		HasMore: len(emoji) >= perPage}, nil
}
