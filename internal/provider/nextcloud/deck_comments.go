package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Card comments of a bound Deck board use the OCS API of the Deck app. Its comment routes resolve a card by
// its ID alone and ignore the board, so one read of the stacks of the bound board must show the card in the
// named stack before anything else is sent; a comment ID is only used after the newest comments of that card
// list it.
var ocsDeck = []string{"apps", "deck", "api", "v1.0"}

const (
	defaultDeckCommentLimit = 20
	maxDeckCommentLimit     = 100
	maxDeckCommentOffset    = 100000

	uncertainDeckComment = "; the change may have been applied, check deck.comments.list before repeating"

	messageDeckCommentNotAuthor = "only the author can change or delete a comment"
	messageDeckCommentForbidden = "this Nextcloud identity may not comment on this card"

	deckCommentMessageShape = `{"type":"string","minLength":1,"maxLength":1000}`
	deckCommentIDShape      = `{"type":"string","pattern":"^[0-9]{1,18}$","x-form":"a comment_id of deck.comments.list"}`
	deckCommentRefShape     = `"board_id":` + deckIDSchema + `,"stack_id":` + deckIDSchema + `,"card_id":` + deckIDSchema
)

const (
	deckMentionSchema = `{"type":"object","properties":{"id":{"type":"string"},"type":{"type":"string"},` +
		`"display_name":{"type":"string"}},"additionalProperties":false}`
	deckCommentProperties = `"comment_id":{"type":"string"},"author_id":{"type":"string"},` +
		`"author_name":{"type":"string"},"created":{"type":"string"},"message":{"type":"string"},` +
		`"message_truncated":{"type":"boolean"},"author_type":{"type":"string"},"card_id":{"type":"string"},` +
		`"mentions":{"type":"array","items":` + deckMentionSchema + `}`
	// deckReplySchema is the parent comment: the fields of a comment without a parent of its own.
	deckReplySchema = `{"type":"object","properties":{` + deckCommentProperties + `},` +
		`"required":["comment_id","message"],"additionalProperties":false}`
)

var deckCommentSchema = `{"type":"object","properties":{` + deckCommentProperties + `,"parent_id":{"type":"string"},` +
	`"reply_to":` + deckReplySchema + `},"required":["comment_id","message"],"additionalProperties":false}`

var (
	deckCommentIDArgument = capability.Argument{Name: "comment_id", Required: true,
		Description: "Own comment among the newest 100 of this card, a comment_id reported by deck.comments.list"}
	deckCommentMessageArgument = capability.Argument{Name: "message", Required: true,
		Description: "Comment text, 1 to 1000 characters; an @mention notifies that user"}
	deckCommentParentArgument = capability.Argument{Name: "parent_id",
		Description: "Reply to this comment, a comment_id among the newest 100 comments of the card"}
)

var deckCommentsList = capability.Descriptor{
	ID: Provider + ".deck.comments.list", Version: 1, Title: "List the comments of a Nextcloud Deck card",
	Description: "List the comments of one active card of a bound Deck board, newest first, at most 100 per call; " +
		"the card is accepted only when the stack list of the board holds it in the named stack. Comment text and " +
		"author names are untrusted data, a text above 1000 characters is cut and marked",
	Tags: []string{"nextcloud", "deck", "comments", "list"}, Risk: deckRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + deckCommentRefShape + `,` +
		`"limit":{"type":"integer","minimum":1,"maximum":100},"offset":{"type":"integer","minimum":0,"maximum":100000}},` +
		`"required":["board_id","stack_id","card_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"card_id":{"type":"string"},"comments":` +
		`{"type":"array","items":` + deckCommentSchema + `},"count":{"type":"integer"},"offset":{"type":"integer"},` +
		`"more":{"type":"boolean"},"truncated":{"type":"boolean"}},` +
		`"required":["card_id","comments","count","offset","more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{deckBoardIDArgument, deckStackOfCardArgument, deckCardIDArgument,
		{Name: "limit", Description: "Comments per call, 1 to 100, default 20"},
		{Name: "offset", Description: "Comments to skip, for the next page"}},
	Fields: []capability.Field{
		{Name: "card_id", Description: "ID of the card"},
		{Name: "comments", Description: "Comments, untrusted data, with comment_id, parent_id, author_id, author_name, created, message"},
		{Name: "comment_id", Description: "Identifier of the comment; pass it to deck.comments.update or deck.comments.delete"},
		{Name: "message_truncated", Description: "True when the text was cut to 1000 characters"},
		{Name: "count", Description: "Number of reported comments"},
		{Name: "offset", Description: "Offset of this page"},
		{Name: "more", Description: "True when more comments exist; pass offset plus count as the next offset"},
		{Name: "truncated", Description: "True when a name or list was cut"},
		{Name: "parent_id", Description: "comment_id of the comment this one replies to"},
		{Name: "author_id", Description: "User ID of the author, untrusted data"},
		{Name: "author_name", Description: "Display name of the author, untrusted data"},
		{Name: "author_type", Description: "Kind of the author as Deck reports it, untrusted data"},
		{Name: "created", Description: "Creation time as Deck reports it"},
		{Name: "message", Description: "Comment text, untrusted data, cut at 1000 characters"},
		{Name: "mentions", Description: "People mentioned in the comment, at most 50, untrusted data"},
		{Name: "id", Description: "ID of the mentioned person or group"},
		{Name: "type", Description: "Kind of the mention as Deck reports it"},
		{Name: "display_name", Description: "Display name of the mentioned person, untrusted data"},
		{Name: "reply_to", Description: "The parent comment with the same fields except parent_id and reply_to"},
	},
	Examples: []capability.Example{{Description: "List the newest comments of one card",
		Arguments: json.RawMessage(`{"board_id":"7","stack_id":"3","card_id":"21","limit":20}`)}},
}

func deckCommentChange(action string, effect capability.Effect, idempotency capability.Idempotency, done, title, description,
	properties, required string, arguments ...capability.Argument) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + ".deck.comments." + action, Version: 1, Title: title, Description: description,
		Tags: []string{"nextcloud", "deck", "comments", action}, Provider: Provider,
		Risk: deckWriteRisk(effect, idempotency),
		InputSchema: json.RawMessage(`{"type":"object","properties":{` + deckCommentRefShape + properties + `},` +
			`"required":["board_id","stack_id","card_id"` + required + `],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"` + done + `":{"type":"boolean"},"card_id":` +
			`{"type":"string"},"comment_id":{"type":"string"}},"required":["` + done + `","card_id"],"additionalProperties":false}`),
		Arguments: append([]capability.Argument{deckBoardIDArgument, deckStackOfCardArgument, deckCardIDArgument}, arguments...),
		Fields: []capability.Field{
			{Name: done, Description: "True when Deck applied the change"},
			{Name: "card_id", Description: "ID of the card"},
			{Name: "comment_id", Description: "The comment"},
		},
	}
}

var deckCommentsCreate = func() capability.Descriptor {
	d := deckCommentChange("create", capability.EffectCreate, capability.IdempotencyNonIdempotent, "created",
		"Comment on a Nextcloud Deck card",
		"Add one comment as the configured identity to one active card of a bound Deck board; an @mention notifies "+
			"that Nextcloud user, so the text leaves the connection. One read-only pre-check must show the card in the "+
			"named stack, and with parent_id a second one the parent among the newest 100 comments, then exactly one "+
			"request, which is never repeated",
		`,"message":`+deckCommentMessageShape+`,"parent_id":`+deckIDSchema, `,"message"`,
		deckCommentMessageArgument, deckCommentParentArgument)
	d.Examples = []capability.Example{{Description: "Comment on one card",
		Arguments: json.RawMessage(`{"board_id":"7","stack_id":"3","card_id":"21","message":"Checked, numbers match."}`)}}
	return d
}()

var deckCommentsUpdate = func() capability.Descriptor {
	d := deckCommentChange("update", capability.EffectUpdate, capability.IdempotencyIdempotent, "updated",
		"Change a Nextcloud Deck card comment",
		"Replace the text of one own comment of one active card of a bound Deck board; Deck refuses a comment of "+
			"another user. An @mention notifies that Nextcloud user. Read-only pre-checks must show the card in the "+
			"named stack and the comment among the newest 100 comments of the card, so older comments cannot be "+
			"changed; then exactly one request, which is never repeated",
		`,"comment_id":`+deckCommentIDShape+`,"message":`+deckCommentMessageShape, `,"comment_id","message"`,
		deckCommentIDArgument, deckCommentMessageArgument)
	d.Examples = []capability.Example{{Description: "Use a comment_id of deck.comments.list",
		Arguments: json.RawMessage(`{"board_id":"7","stack_id":"3","card_id":"21","comment_id":"42","message":"Checked twice."}`)}}
	return d
}()

var deckCommentsDelete = func() capability.Descriptor {
	d := deckCommentChange("delete", capability.EffectDelete, capability.IdempotencyIdempotent, "deleted",
		"Delete a Nextcloud Deck card comment",
		"Delete one own comment of one active card of a bound Deck board for good; Deck refuses a comment of another "+
			"user. Read-only pre-checks must show the card in the named stack and the comment among the newest 100 "+
			"comments of the card, so older comments cannot be deleted; then exactly one request, which is never "+
			"repeated",
		`,"comment_id":`+deckCommentIDShape, `,"comment_id"`, deckCommentIDArgument)
	d.RequiresToolAllowList = true
	d.Examples = []capability.Example{{Description: "Use a comment_id of deck.comments.list",
		Arguments: json.RawMessage(`{"board_id":"7","stack_id":"3","card_id":"21","comment_id":"42"}`)}}
	return d
}()

// DeckComment is the stable Qatlas view of one card comment. Every text in it is untrusted provider data.
type DeckComment struct {
	CommentID        string `json:"comment_id"`
	ParentID         string `json:"parent_id,omitempty"`
	AuthorID         string `json:"author_id,omitempty"`
	AuthorName       string `json:"author_name,omitempty"`
	Created          string `json:"created,omitempty"`
	Message          string `json:"message"`
	MessageTruncated bool   `json:"message_truncated,omitempty"`
	// AuthorType, CardID, Mentions, and ReplyTo carry what Deck reports besides the fields above.
	AuthorType string             `json:"author_type,omitempty"`
	CardID     string             `json:"card_id,omitempty"`
	Mentions   []DeckMention      `json:"mentions,omitempty"`
	ReplyTo    *DeckCommentParent `json:"reply_to,omitempty"`
}

// DeckMention is one mention Deck parsed from a comment; every text in it is untrusted provider data.
type DeckMention struct {
	ID          string `json:"id,omitempty"`
	Type        string `json:"type,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

// DeckCommentParent is the comment another comment replies to; Deck exposes only the next parent, so it has
// no parent of its own.
type DeckCommentParent struct {
	CommentID        string        `json:"comment_id"`
	AuthorID         string        `json:"author_id,omitempty"`
	AuthorName       string        `json:"author_name,omitempty"`
	Created          string        `json:"created,omitempty"`
	Message          string        `json:"message"`
	MessageTruncated bool          `json:"message_truncated,omitempty"`
	AuthorType       string        `json:"author_type,omitempty"`
	CardID           string        `json:"card_id,omitempty"`
	Mentions         []DeckMention `json:"mentions,omitempty"`
}

// DeckCommentList is one page of the comments of one card.
type DeckCommentList struct {
	CardID    string        `json:"card_id"`
	Comments  []DeckComment `json:"comments"`
	Count     int           `json:"count"`
	Offset    int           `json:"offset"`
	More      bool          `json:"more"`
	Truncated bool          `json:"truncated,omitempty"`
}

type rawMention struct {
	ID          flexString `json:"mentionId"`
	Type        string     `json:"mentionType"`
	DisplayName string     `json:"mentionDisplayName"`
}

type rawDeckComment struct {
	ID        json.Number  `json:"id"`
	ObjectID  json.Number  `json:"objectId"`
	Message   string       `json:"message"`
	ActorID   string       `json:"actorId"`
	ActorType string       `json:"actorType"`
	ActorName string       `json:"actorDisplayName"`
	Created   string       `json:"creationDateTime"`
	Mentions  []rawMention `json:"mentions"`
	ReplyTo   *rawReply    `json:"replyTo"`
}

// rawReply is the parent comment; it is read without a parent of its own, so a nested reply is never followed.
type rawReply struct {
	ID        json.Number  `json:"id"`
	ObjectID  json.Number  `json:"objectId"`
	Message   string       `json:"message"`
	ActorID   string       `json:"actorId"`
	ActorType string       `json:"actorType"`
	ActorName string       `json:"actorDisplayName"`
	Created   string       `json:"creationDateTime"`
	Mentions  []rawMention `json:"mentions"`
}

type deckCommentArguments struct {
	BoardID   string `json:"board_id"`
	StackID   string `json:"stack_id"`
	CardID    string `json:"card_id"`
	CommentID string `json:"comment_id"`
	ParentID  string `json:"parent_id"`
	Message   string `json:"message"`
	Limit     *int   `json:"limit"`
	Offset    *int   `json:"offset"`
}

// readDeckCommentArguments decodes strictly and validates everything before any credential access.
func readDeckCommentArguments(op string, raw json.RawMessage, needID, needMessage bool) (deckCommentArguments, error) {
	var input deckCommentArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	for _, id := range []string{input.BoardID, input.StackID, input.CardID} {
		if err := checkDeckID(op, id); err != nil {
			return input, err
		}
	}
	list := !needID && !needMessage
	if (!needID && input.CommentID != "") || (needID && input.ParentID != "") || (!needMessage && input.Message != "") ||
		(!list && (input.Limit != nil || input.Offset != nil)) {
		return input, providerError(op, "an argument is not accepted by this tool")
	}
	if needID {
		if err := checkDeckID(op, input.CommentID); err != nil {
			return input, providerError(op, "the comment ID must be a comment_id reported by "+deckCommentsList.ID)
		}
	}
	if input.ParentID != "" {
		if err := checkDeckID(op, input.ParentID); err != nil {
			return input, providerError(op, "the parent ID must be a comment_id reported by "+deckCommentsList.ID)
		}
	}
	if needMessage && !validCommentMessage(input.Message) {
		return input, providerError(op, "the comment text must be 1 to 1000 characters of text")
	}
	return input, nil
}

// deckCommentsRequest addresses the comments of one card, or one comment of it.
func deckCommentsRequest(cardID string, commentID ...string) ocsRequest {
	return ocsRequest{app: ocsDeck, suffix: append([]string{"cards", cardID, "comments"}, commentID...)}
}

// deckCommentsOf reads one page of the comments of a card; the card has to be checked before.
func (c *Client) deckCommentsOf(ctx context.Context, op, cardID string, limit, offset int) ([]rawDeckComment, error) {
	request := deckCommentsRequest(cardID)
	request.query = url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
	data, err := c.ocsGet(ctx, op, request)
	if err != nil {
		return nil, deckCommentRefusal(op, err, "")
	}
	var items []rawDeckComment
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, invalidResponse(op, "the Nextcloud comment list could not be read")
	}
	return items, nil
}

// deckCommentRefusal turns the answers a comment request can meet into stable messages without provider text.
// A 404 looks alike for a missing card and a missing comment, so it names neither.
func deckCommentRefusal(op string, err error, forbidden string) error {
	var providerErr *provider.Error
	if !errors.As(err, &providerErr) {
		return err
	}
	switch {
	case forbidden != "" && providerErr.Class == provider.ClassPermission:
		return &provider.Error{Class: provider.ClassPermission, Op: op, Message: forbidden}
	case providerErr.Message == messageNotFound:
		return deckNotFound(op)
	}
	return err
}

// checkedDeckCard reads the stacks of the bound board once and requires the card in the named stack.
func (c *Client) checkedDeckCard(ctx context.Context, op string, input deckCommentArguments) error {
	stacks, err := c.cardStacks(ctx, op, input.BoardID, false)
	if err != nil {
		return err
	}
	_, err = cardIn(op, stacks, input.StackID, input.CardID)
	return err
}

func (c *Client) listDeckComments(ctx context.Context, op string, input deckCommentArguments, limit, offset int) (any, error) {
	if err := c.checkedDeckCard(ctx, op, input); err != nil {
		return nil, err
	}
	// One more than the page is requested, so a following page is reported without a second request.
	items, err := c.deckCommentsOf(ctx, op, input.CardID, limit+1, offset)
	if err != nil {
		return nil, err
	}
	list := &DeckCommentList{CardID: input.CardID, Comments: []DeckComment{}, Offset: offset}
	var cut cutter
	for _, item := range items {
		if !validDeckCommentItem(item, input.CardID) {
			continue
		}
		if len(list.Comments) >= limit {
			list.More = true
			break
		}
		message, messageCut := cutText(item.Message)
		comment := DeckComment{CommentID: string(item.ID), AuthorID: cut.text(item.ActorID, maxDeckTitleBytes),
			AuthorName: cut.text(item.ActorName, maxDeckTitleBytes), Created: cut.text(item.Created, maxDeckTitleBytes),
			Message: message, MessageTruncated: messageCut, AuthorType: cut.text(item.ActorType, maxDeckTitleBytes),
			CardID: commentCardID(item.ObjectID), Mentions: mentionsOf(&cut, item.Mentions)}
		if reply := item.ReplyTo; reply != nil && digitsOnly(string(reply.ID)) {
			comment.ParentID = string(reply.ID)
			parentMessage, parentCut := cutText(reply.Message)
			comment.ReplyTo = &DeckCommentParent{CommentID: string(reply.ID),
				AuthorID: cut.text(reply.ActorID, maxDeckTitleBytes), AuthorName: cut.text(reply.ActorName, maxDeckTitleBytes),
				Created: cut.text(reply.Created, maxDeckTitleBytes), Message: parentMessage, MessageTruncated: parentCut,
				AuthorType: cut.text(reply.ActorType, maxDeckTitleBytes), CardID: commentCardID(reply.ObjectID),
				Mentions: mentionsOf(&cut, reply.Mentions)}
		}
		list.Comments = append(list.Comments, comment)
	}
	list.Count = len(list.Comments)
	list.Truncated = cut.hit
	return list, nil
}

// commentCardID reports the card a comment names when it is a number.
func commentCardID(id json.Number) string {
	if digitsOnly(string(id)) {
		return string(id)
	}
	return ""
}

// mentionsOf converts the mentions of a comment, at most maxDeckMentions; a cut is reported through the cutter.
func mentionsOf(c *cutter, raw []rawMention) []DeckMention {
	var mentions []DeckMention
	for _, item := range raw {
		if len(mentions) == maxDeckMentions {
			c.hit = true
			break
		}
		mentions = append(mentions, DeckMention{ID: c.text(string(item.ID), maxDeckTitleBytes),
			Type: c.text(item.Type, maxDeckTitleBytes), DisplayName: c.text(item.DisplayName, maxDeckTitleBytes)})
	}
	return mentions
}

// validDeckCommentItem accepts a comment with a numeric ID that does not name another card.
func validDeckCommentItem(item rawDeckComment, cardID string) bool {
	return digitsOnly(string(item.ID)) && (item.ObjectID == "" || string(item.ObjectID) == cardID)
}

// requireComment shows a comment among the newest comments of the card, the only page that is read.
func (c *Client) requireComment(ctx context.Context, op, cardID, commentID string) error {
	items, err := c.deckCommentsOf(ctx, op, cardID, maxDeckCommentLimit, 0)
	if err != nil {
		return err
	}
	for _, item := range items {
		if string(item.ID) == commentID && validDeckCommentItem(item, cardID) {
			return nil
		}
	}
	return deckNotFound(op)
}

func (c *Client) createDeckComment(ctx context.Context, op string, input deckCommentArguments) (any, error) {
	if err := c.checkedDeckCard(ctx, op, input); err != nil {
		return nil, err
	}
	form := url.Values{"message": {input.Message}}
	if input.ParentID != "" {
		if err := c.requireComment(ctx, op, input.CardID, input.ParentID); err != nil {
			return nil, err
		}
		form.Set("parentId", input.ParentID)
	}
	data, err := c.ocsSend(ctx, op, http.MethodPost, deckCommentsRequest(input.CardID), form, uncertainDeckComment)
	if err != nil {
		return nil, deckCommentRefusal(op, err, messageDeckCommentForbidden)
	}
	var created rawDeckComment
	if err := json.Unmarshal(data, &created); err != nil || !digitsOnly(string(created.ID)) {
		return nil, withUncertainty(invalidResponse(op, "the Nextcloud comment could not be read"), uncertainDeckComment)
	}
	return map[string]any{"created": true, "card_id": input.CardID, "comment_id": string(created.ID)}, nil
}

func (c *Client) updateDeckComment(ctx context.Context, op string, input deckCommentArguments) (any, error) {
	if err := c.checkedDeckCard(ctx, op, input); err != nil {
		return nil, err
	}
	if err := c.requireComment(ctx, op, input.CardID, input.CommentID); err != nil {
		return nil, err
	}
	_, err := c.ocsSend(ctx, op, http.MethodPut, deckCommentsRequest(input.CardID, input.CommentID),
		url.Values{"message": {input.Message}}, uncertainDeckComment)
	if err != nil {
		return nil, deckCommentRefusal(op, err, messageDeckCommentNotAuthor)
	}
	return map[string]any{"updated": true, "card_id": input.CardID, "comment_id": input.CommentID}, nil
}

func (c *Client) deleteDeckComment(ctx context.Context, op string, input deckCommentArguments) (any, error) {
	if err := c.checkedDeckCard(ctx, op, input); err != nil {
		return nil, err
	}
	if err := c.requireComment(ctx, op, input.CardID, input.CommentID); err != nil {
		return nil, err
	}
	_, err := c.ocsSend(ctx, op, http.MethodDelete, deckCommentsRequest(input.CardID, input.CommentID), nil,
		uncertainDeckComment)
	if err != nil {
		return nil, deckCommentRefusal(op, err, messageDeckCommentNotAuthor)
	}
	return map[string]any{"deleted": true, "card_id": input.CardID, "comment_id": input.CommentID}, nil
}

func invokeDeckCommentsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list Deck card comments"
	input, err := readDeckCommentArguments(op, raw, false, false)
	if err != nil {
		return nil, err
	}
	limit, offset := defaultDeckCommentLimit, 0
	if input.Limit != nil {
		limit = *input.Limit
	}
	if input.Offset != nil {
		offset = *input.Offset
	}
	if limit < 1 || limit > maxDeckCommentLimit || offset < 0 || offset > maxDeckCommentOffset {
		return nil, providerError(op, "limit must be 1 to 100 and offset 0 to 100000")
	}
	client, err := openDeck(ctx, resolved, secrets, red, input.BoardID)
	if err != nil {
		return nil, err
	}
	return client.listDeckComments(ctx, op, input, limit, offset)
}

func invokeDeckCommentChange(op string, needID, needMessage bool,
	change func(*Client, context.Context, string, deckCommentArguments) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		input, err := readDeckCommentArguments(op, raw, needID, needMessage)
		if err != nil {
			return nil, err
		}
		client, err := openDeck(ctx, resolved, secrets, red, input.BoardID)
		if err != nil {
			return nil, err
		}
		return change(client, ctx, op, input)
	}
}

var (
	invokeDeckCommentsCreate = invokeDeckCommentChange("create Deck card comment", false, true, (*Client).createDeckComment)
	invokeDeckCommentsUpdate = invokeDeckCommentChange("change Deck card comment", true, true, (*Client).updateDeckComment)
	invokeDeckCommentsDelete = invokeDeckCommentChange("delete Deck card comment", true, false, (*Client).deleteDeckComment)
)
