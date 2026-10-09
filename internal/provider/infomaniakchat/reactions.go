package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	uncertainReactionAdd    = "; the reaction may have been added, list the reactions before adding it again"
	uncertainReactionRemove = "; the reaction may have been removed, list the reactions before removing it again"
	maxEmojiNameLength      = 64
)

// emojiNameSchema mirrors validEmojiName exactly.
var emojiNameSchema = `{"type":"string","minLength":1,"maxLength":` + itoa(maxEmojiNameLength) +
	`,"pattern":"^[a-z0-9_+-]{1,` + itoa(maxEmojiNameLength) + `}$"}`

var emojiNameArgument = capability.Argument{Name: "emoji_name", Required: true,
	Description: "Emoji name without colons, 1 to " + itoa(maxEmojiNameLength) +
		" characters of lowercase letters, digits, underscore, plus, or hyphen"}

func reactionChangeRisk(effect capability.Effect) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity}
}

var reactionEntrySchema = `{"type":"object","properties":{` +
	`"user_id":` + idSchema + `,"emoji_name":{"type":"string"},"created_at":{"type":"string"}},` +
	`"required":["user_id","emoji_name"],"additionalProperties":false}`

var reactionsList = capability.Descriptor{
	ID:      Provider + ".reactions.list",
	Version: 1,
	Title:   "List Infomaniak kChat reactions",
	Description: "List the reactions on one message of a channel this connection may reach, one page at a " +
		"time; each entry names the reacting user, the emoji, and the time",
	Tags:     []string{"infomaniak", "kchat", "reactions", "list"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"post_id":` + idSchema + `,` +
		`"page":{"type":"integer","minimum":1},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + itoa(maxListLimit) + `}},` +
		`"required":["post_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"post_id":` + idSchema + `,"reactions":{"type":"array","items":` + reactionEntrySchema + `},` +
		`"page":{"type":"integer"},"pages":{"type":"integer"},"total":{"type":"integer"},"count":{"type":"integer"}},` +
		`"required":["post_id","reactions","page","pages","total","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		postIDArgument,
		{Name: "page", Description: "1-based page of the reactions; the first page when omitted"},
		{Name: "limit", Description: "Reactions per page, 1 to " + itoa(maxListLimit) + "; " + itoa(defaultListLimit) + " when omitted"},
	},
	Fields: []capability.Field{
		{Name: "user_id", Description: "User who reacted"},
		{Name: "emoji_name", Description: "Emoji name, untrusted data"},
		{Name: "created_at", Description: "Reaction time, normalised to RFC 3339 in UTC, when kChat reports one"},
		{Name: "post_id", Description: "Message that was read"},
		{Name: "page", Description: "Page that was read"},
		{Name: "pages", Description: "Total number of pages"},
		{Name: "total", Description: "Number of reactions on the message"},
		{Name: "count", Description: "Number of reactions reported on this page"},
	},
	Examples: []capability.Example{{Description: "List the reactions of one message",
		Arguments: json.RawMessage(`{"post_id":"abc123post00000000000000000"}`)}},
}

var reactionsAdd = capability.Descriptor{
	ID:      Provider + ".reactions.add",
	Version: 1,
	Title:   "Add an Infomaniak kChat reaction",
	Description: "Add exactly one confirmed reaction of the token's own user to one message of a channel this " +
		"connection may reach; adding an existing reaction changes nothing",
	Tags:     []string{"infomaniak", "kchat", "reactions", "add"},
	Risk:     reactionChangeRisk(capability.EffectCreate),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"post_id":` + idSchema + `,"emoji_name":` +
		emojiNameSchema + `},"required":["post_id","emoji_name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"post_id":` + idSchema + `,"emoji_name":{"type":"string"},"user_id":` + idSchema + `},` +
		`"required":["post_id","emoji_name","user_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{postIDArgument, emojiNameArgument},
	Fields: []capability.Field{
		{Name: "post_id", Description: "Message that was reacted to"},
		{Name: "emoji_name", Description: "Emoji of the reaction"},
		{Name: "user_id", Description: "The token's own user, who reacted"},
	},
	Examples: []capability.Example{{Description: "React to one message",
		Arguments: json.RawMessage(`{"post_id":"abc123post00000000000000000","emoji_name":"thumbsup"}`)}},
}

var reactionsRemove = capability.Descriptor{
	ID:      Provider + ".reactions.remove",
	Version: 1,
	Title:   "Remove an Infomaniak kChat reaction",
	Description: "Remove exactly one confirmed reaction of the token's own user from one message of a channel " +
		"this connection may reach; reactions of other users are never removed",
	Tags:                  []string{"infomaniak", "kchat", "reactions", "remove", "delete"},
	Risk:                  reactionChangeRisk(capability.EffectDelete),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"post_id":` + idSchema + `,"emoji_name":` +
		emojiNameSchema + `},"required":["post_id","emoji_name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"post_id":` + idSchema + `,"emoji_name":{"type":"string"},"removed":{"type":"boolean"}},` +
		`"required":["post_id","emoji_name","removed"],"additionalProperties":false}`),
	Arguments: []capability.Argument{postIDArgument, emojiNameArgument},
	Fields: []capability.Field{
		{Name: "post_id", Description: "Message the reaction was removed from"},
		{Name: "emoji_name", Description: "Emoji of the removed reaction"},
		{Name: "removed", Description: "True once kChat confirmed the removal"},
	},
	Examples: []capability.Example{{Description: "Remove the own reaction from one message",
		Arguments: json.RawMessage(`{"post_id":"abc123post00000000000000000","emoji_name":"thumbsup"}`)}},
}

// validEmojiName keeps an emoji name to the characters of one safe path segment.
func validEmojiName(value string) bool {
	if value == "" || len(value) > maxEmojiNameLength {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '+' || r == '-' {
			continue
		}
		return false
	}
	return true
}

// reactionJSON is the subset of the kChat Reaction resource this provider reads.
type reactionJSON struct {
	UserID    string `json:"user_id"`
	PostID    string `json:"post_id"`
	EmojiName string `json:"emoji_name"`
	CreateAt  int64  `json:"create_at"`
}

// ReactionEntry is the stable Qatlas view of one reaction.
type ReactionEntry struct {
	UserID    string `json:"user_id"`
	EmojiName string `json:"emoji_name"`
	CreatedAt string `json:"created_at,omitempty"`
}

// ReactionsPage is one paginated listing of the reactions of one message.
type ReactionsPage struct {
	PostID    string          `json:"post_id"`
	Reactions []ReactionEntry `json:"reactions"`
	Page      int             `json:"page"`
	Pages     int             `json:"pages"`
	Total     int             `json:"total"`
	Count     int             `json:"count"`
}

// AddedReaction is the answer of one confirmed reaction.
type AddedReaction struct {
	PostID    string `json:"post_id"`
	EmojiName string `json:"emoji_name"`
	UserID    string `json:"user_id"`
}

// RemovedReaction is the answer of one confirmed removal.
type RemovedReaction struct {
	PostID    string `json:"post_id"`
	EmojiName string `json:"emoji_name"`
	Removed   bool   `json:"removed"`
}

type reactionsListArguments struct {
	PostID string `json:"post_id"`
	Page   int    `json:"page"`
	Limit  int    `json:"limit"`
}

type reactionArguments struct {
	PostID    string `json:"post_id"`
	EmojiName string `json:"emoji_name"`
}

func invokeReactionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list reactions"
	var input reactionsListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validMattermostID(input.PostID) {
		return nil, invalidRequest("post_id must be a kChat-style identifier")
	}
	page, limit := input.Page, input.Limit
	if page == 0 {
		page = 1
	}
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	post, err := client.verifyPostScope(ctx, resolved, op, input.PostID)
	if err != nil {
		return nil, err
	}
	return client.ListReactions(ctx, post.ID, page, limit)
}

// ListReactions reads every reaction of one post, which kChat answers without pagination, and pages the
// result itself. A reaction kChat reports for another post is dropped.
func (c *Client) ListReactions(ctx context.Context, postID string, page, limit int) (*ReactionsPage, error) {
	const op = "list reactions"
	var reactions []reactionJSON
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/posts/"+url.PathEscape(postID)+"/reactions", nil, nil,
		&reactions, false); err != nil {
		return nil, err
	}
	entries := make([]ReactionEntry, 0, len(reactions))
	for _, r := range reactions {
		if r.PostID != postID {
			continue
		}
		entries = append(entries, ReactionEntry{UserID: bounded(r.UserID), EmojiName: bounded(r.EmojiName),
			CreatedAt: msToRFC3339(r.CreateAt)})
	}
	window, pages, total := windowOf(entries, page, limit)
	return &ReactionsPage{PostID: postID, Reactions: window, Page: page, Pages: pages, Total: total,
		Count: len(window)}, nil
}

// ownUserID reads the id of the token's own user. It is never taken from an argument, so a reaction can
// only be set or removed as the token's own user.
func (c *Client) ownUserID(ctx context.Context, op string) (string, error) {
	var me struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/users/me", nil, nil, &me, false); err != nil {
		return "", err
	}
	if !validMattermostID(me.ID) {
		return "", &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response"}
	}
	return me.ID, nil
}

// reactionTarget validates the arguments locally, then binds the post and reads the own user ID.
func reactionTarget(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, op string) (*Client, reactionArguments, string, error) {
	var input reactionArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, input, "", providerError(op, "the validated arguments could not be read")
	}
	if !validMattermostID(input.PostID) {
		return nil, input, "", invalidRequest("post_id must be a kChat-style identifier")
	}
	if !validEmojiName(input.EmojiName) {
		return nil, input, "", invalidRequest("emoji_name must be 1 to " + itoa(maxEmojiNameLength) +
			" characters of lowercase letters, digits, underscore, plus, or hyphen")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, input, "", err
	}
	if _, err := client.verifyPostScope(ctx, resolved, op, input.PostID); err != nil {
		return nil, input, "", err
	}
	userID, err := client.ownUserID(ctx, op)
	if err != nil {
		return nil, input, "", err
	}
	return client, input, userID, nil
}

func invokeReactionsAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, input, userID, err := reactionTarget(ctx, resolved, secrets, red, raw, "add reaction")
	if err != nil {
		return nil, err
	}
	return client.AddReaction(ctx, userID, input.PostID, input.EmojiName)
}

// AddReaction sends exactly one POST and never repeats it: a failure after the request may have reached
// kChat says so instead.
func (c *Client) AddReaction(ctx context.Context, userID, postID, emoji string) (*AddedReaction, error) {
	const op = "add reaction"
	body := map[string]string{"user_id": userID, "post_id": postID, "emoji_name": emoji}
	var reaction reactionJSON
	if err := c.doWith(ctx, op, http.MethodPost, "/api/v4/reactions", nil, body, &reaction,
		uncertainReactionAdd); err != nil {
		return nil, err
	}
	if reaction.UserID != userID || reaction.PostID != postID || reaction.EmojiName != emoji {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainReactionAdd}
	}
	return &AddedReaction{PostID: postID, EmojiName: emoji, UserID: userID}, nil
}

func invokeReactionsRemove(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, input, userID, err := reactionTarget(ctx, resolved, secrets, red, raw, "remove reaction")
	if err != nil {
		return nil, err
	}
	return client.RemoveReaction(ctx, userID, input.PostID, input.EmojiName)
}

// RemoveReaction sends exactly one DELETE for the token's own reaction and never repeats it.
func (c *Client) RemoveReaction(ctx context.Context, userID, postID, emoji string) (*RemovedReaction, error) {
	const op = "remove reaction"
	path := "/api/v4/users/" + url.PathEscape(userID) + "/posts/" + url.PathEscape(postID) + "/reactions/" +
		url.PathEscape(emoji)
	if err := c.doWith(ctx, op, http.MethodDelete, path, nil, nil, nil, uncertainReactionRemove); err != nil {
		return nil, err
	}
	return &RemovedReaction{PostID: postID, EmojiName: emoji, Removed: true}, nil
}
