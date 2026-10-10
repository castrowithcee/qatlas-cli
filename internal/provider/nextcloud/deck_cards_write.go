package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Card changes through the Deck REST API below a bound board. Deck resolves a card by its ID alone and does
// not check the board of the path, so one pre-read of the bound board's stacks must show the card in the
// named stack and, for a creation or move, the target stack; anything else is refused like a missing object.
// An archived card can only be restored: it is read from the archived listing and changed in no other way.

const (
	modeMove = "move"

	maxDeckDueDate = 40

	uncertainDeckCard = "; the change may have been applied, check deckcards.get or deckstacks.list before repeating"

	messageDeckCardChanged = "the card changed since it was read, read it again"
)

const (
	deckDescriptionSchema = `{"type":"string","maxLength":8192,"x-form":"text up to 8 KiB, line breaks and tabs allowed"}`
	deckDueSchema         = `{"type":"string","maxLength":40,"x-form":"RFC 3339 time, or an empty string to clear it"}`
	deckCardResultSchema  = `{"type":"object","properties":{"%s":{"type":"boolean"},"card":` + deckCardSchema + `},` +
		`"required":["%s","card"],"additionalProperties":false}`
)

var (
	deckStackOfCardArgument = capability.Argument{
		Name: "stack_id", Description: "Numeric ID of the stack that holds the card, as reported by deckstacks.list", Required: true,
	}
	deckCardIDArgument = capability.Argument{
		Name: "card_id", Description: "Numeric card ID as reported by deckstacks.list", Required: true,
	}
	deckCardOrderArgument   = capability.Argument{Name: "order", Description: "Position in the stack, 0 to 10000; default last"}
	deckDescriptionArgument = capability.Argument{
		Name: "description", Description: "Card description, up to 8 KiB; control characters other than line breaks and tabs are refused",
	}
	deckDueArgument = capability.Argument{
		Name: "duedate", Description: "Due time as RFC 3339; an empty string clears it",
	}
)

func deckCardResult(flag string) json.RawMessage {
	return json.RawMessage(strings.ReplaceAll(deckCardResultSchema, "%s", flag))
}

var deckCardsCreate = capability.Descriptor{
	ID: Provider + ".deckcards.create", Version: 1, Title: "Create a Nextcloud Deck card",
	Description: "Create exactly one confirmed card in a stack of a bound Deck board; the identity needs the edit " +
		"right on the board. One read-only pre-check must show the stack in the board, then exactly one request; " +
		"without order the card goes last. Labels and assignments are not set",
	Tags: []string{"nextcloud", "deck", "cards", "create"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"stack_id":` +
		deckIDSchema + `,"title":` + deckTitleSchema + `,"description":` + deckDescriptionSchema + `,"duedate":` +
		deckDueSchema + `,"order":` + deckOrderSchema + `},"required":["board_id","stack_id","title"],` +
		`"additionalProperties":false}`),
	OutputSchema: deckCardResult("created"),
	Arguments: []capability.Argument{deckBoardIDArgument,
		{Name: "stack_id", Description: "Numeric ID of the target stack, as reported by deckstacks.list", Required: true},
		{Name: "title", Description: "Card title, 1 to 100 characters", Required: true},
		deckDescriptionArgument, deckDueArgument, deckCardOrderArgument},
	Fields: []capability.Field{
		{Name: "created", Description: "True when Deck created the card"},
		{Name: "card", Description: "The new card as Deck reports it, untrusted data"},
	},
	Examples: []capability.Example{{Description: "Add a card", Arguments: json.RawMessage(`{"board_id":"7","stack_id":"3","title":"Plan"}`)}},
}

var deckCardsUpdate = capability.Descriptor{
	ID: Provider + ".deckcards.update", Version: 1, Title: "Change a Nextcloud Deck card",
	Description: "Change the title, description, due time, or archived state of exactly one confirmed card of a bound " +
		"Deck board, at least one field; the identity needs the edit right on the board. last_modified from " +
		"deckcards.get or deckstacks.list must match the card as read by the one pre-check, else nothing is sent. " +
		"The card is sent back as read with the change; fields not given keep their value. An archived card can " +
		"only be restored with archived false, which takes no other field",
	Tags: []string{"nextcloud", "deck", "cards", "update", "archive"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"stack_id":` +
		deckIDSchema + `,"card_id":` + deckIDSchema + `,"last_modified":{"type":"integer","minimum":0},"title":` +
		deckTitleSchema + `,"description":` + deckDescriptionSchema + `,"duedate":` + deckDueSchema +
		`,"archived":{"type":"boolean"}},"required":["board_id","stack_id","card_id","last_modified"],` +
		`"additionalProperties":false}`),
	OutputSchema: deckCardResult("updated"),
	Arguments: []capability.Argument{deckBoardIDArgument, deckStackOfCardArgument, deckCardIDArgument,
		{Name: "last_modified", Description: "last_modified of the card as read; a card changed since is refused", Required: true},
		{Name: "title", Description: "New card title, 1 to 100 characters"},
		{Name: "description", Description: "New description, up to 8 KiB; an empty string clears it"},
		{Name: "duedate", Description: "New due time as RFC 3339; an empty string clears it"},
		{Name: "archived", Description: "True archives the card; false restores an archived card and takes no other field"}},
	Fields: []capability.Field{
		{Name: "updated", Description: "True when Deck applied the change"},
		{Name: "card", Description: "The card as Deck reports it after the change, untrusted data"},
	},
	Examples: []capability.Example{{Description: "Rename a card read earlier",
		Arguments: json.RawMessage(`{"board_id":"7","stack_id":"3","card_id":"21","last_modified":1700000100,"title":"Plan v2"}`)}},
}

var deckCardsMove = capability.Descriptor{
	ID: Provider + ".deckcards.move", Version: 1, Title: "Move a Nextcloud Deck card",
	Description: "Move exactly one confirmed active card to another stack of the same bound Deck board or to another " +
		"position; the identity needs the edit right on the board. One read-only pre-check must show the card in " +
		"the named stack and the target stack in the board, then exactly one request; without order the card goes " +
		"last. Archived cards are not moved",
	Tags: []string{"nextcloud", "deck", "cards", "move", "reorder"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"stack_id":` +
		deckIDSchema + `,"card_id":` + deckIDSchema + `,"target_stack_id":` + deckIDSchema + `,"order":` +
		deckOrderSchema + `},"required":["board_id","stack_id","card_id","target_stack_id"],"additionalProperties":false}`),
	OutputSchema: deckCardResult("moved"),
	Arguments: []capability.Argument{deckBoardIDArgument, deckStackOfCardArgument, deckCardIDArgument,
		{Name: "target_stack_id", Description: "Numeric ID of the stack of this board to move the card to", Required: true},
		deckCardOrderArgument},
	Fields: []capability.Field{
		{Name: "moved", Description: "True when Deck moved the card"},
		{Name: "card", Description: "The card as Deck reports it after the move, untrusted data"},
	},
	Examples: []capability.Example{{Description: "Move a card to another stack",
		Arguments: json.RawMessage(`{"board_id":"7","stack_id":"3","card_id":"21","target_stack_id":"4"}`)}},
}

var deckCardsDelete = capability.Descriptor{
	ID: Provider + ".deckcards.delete", Version: 1, Title: "Delete a Nextcloud Deck card",
	Description: "Delete exactly one confirmed active card of a bound Deck board; the identity needs the edit right " +
		"on the board. Deck deletes softly, but Qatlas offers no restore. One read-only pre-check must show the " +
		"card in the named stack, then exactly one request; archived cards are not deleted",
	Tags: []string{"nextcloud", "deck", "cards", "delete"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"stack_id":` +
		deckIDSchema + `,"card_id":` + deckIDSchema + `},"required":["board_id","stack_id","card_id"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"},"card_id":` +
		`{"type":"string"}},"required":["deleted","card_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{deckBoardIDArgument, deckStackOfCardArgument, deckCardIDArgument},
	Fields: []capability.Field{
		{Name: "deleted", Description: "True when Deck deleted the card"},
		{Name: "card_id", Description: "ID of the deleted card"},
	},
	Examples: []capability.Example{{Description: "Use IDs from deckstacks.list", Arguments: json.RawMessage(`{"board_id":"7","stack_id":"3","card_id":"21"}`)}},
	// Reachable only through a tools list, never through a profile.
	RequiresToolAllowList: true,
}

type deckCardArguments struct {
	BoardID       string  `json:"board_id"`
	StackID       string  `json:"stack_id"`
	CardID        string  `json:"card_id"`
	TargetStackID string  `json:"target_stack_id"`
	LastModified  *int64  `json:"last_modified"`
	Title         *string `json:"title"`
	Description   *string `json:"description"`
	DueDate       *string `json:"duedate"`
	Order         *int64  `json:"order"`
	Archived      *bool   `json:"archived"`
}

// cleanDeckDescription accepts valid text up to 8 KiB with line breaks and tabs as the only control characters.
func cleanDeckDescription(op string, raw *string) error {
	if raw == nil {
		return nil
	}
	if len(*raw) > maxTextBytes || !utf8.ValidString(*raw) {
		return providerError(op, "the description must be valid text of at most 8 KiB")
	}
	for _, r := range *raw {
		if (r < 0x20 && r != '\n' && r != '\r' && r != '\t') || r == 0x7f {
			return providerError(op, "the description must not contain control characters other than line breaks and tabs")
		}
	}
	return nil
}

// cleanDeckDue accepts an RFC 3339 time or an empty string, which clears the due time.
func cleanDeckDue(op string, raw *string) error {
	if raw == nil || *raw == "" {
		return nil
	}
	if len(*raw) > maxDeckDueDate {
		return providerError(op, "the due time must be an RFC 3339 time")
	}
	if _, err := time.Parse(time.RFC3339, *raw); err != nil {
		return providerError(op, "the due time must be an RFC 3339 time")
	}
	return nil
}

// readDeckCardArguments decodes strictly and validates every value before any credential access. Each mode
// accepts exactly its own fields.
func readDeckCardArguments(op string, raw json.RawMessage, mode string) (deckCardArguments, error) {
	var input deckCardArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	ids := []string{input.BoardID, input.StackID}
	if mode != modeCreate {
		ids = append(ids, input.CardID)
	}
	if mode == modeMove {
		ids = append(ids, input.TargetStackID)
	}
	for _, id := range ids {
		if err := checkDeckID(op, id); err != nil {
			return input, err
		}
	}
	title, err := cleanDeckTitle(op, input.Title)
	if err != nil {
		return input, err
	}
	input.Title = title
	if err := cleanDeckDescription(op, input.Description); err != nil {
		return input, err
	}
	if err := cleanDeckDue(op, input.DueDate); err != nil {
		return input, err
	}
	if input.Order != nil && (*input.Order < 0 || *input.Order > maxDeckStackOrder) {
		return input, providerError(op, "the order must be a whole number from 0 to 10000")
	}
	if input.LastModified != nil && *input.LastModified < 0 {
		return input, providerError(op, "last_modified must be a non-negative number")
	}
	fields := map[string]bool{
		"card_id": input.CardID != "", "target_stack_id": input.TargetStackID != "", "last_modified": input.LastModified != nil,
		"title": input.Title != nil, "description": input.Description != nil, "duedate": input.DueDate != nil,
		"order": input.Order != nil, "archived": input.Archived != nil,
	}
	allowed := map[string][]string{
		modeCreate: {"title", "description", "duedate", "order"},
		modeUpdate: {"card_id", "last_modified", "title", "description", "duedate", "archived"},
		modeMove:   {"card_id", "target_stack_id", "order"},
		modeDelete: {"card_id"},
	}[mode]
	for name, given := range fields {
		if given && !slices.Contains(allowed, name) {
			return input, providerError(op, "the argument "+name+" is not accepted by this tool")
		}
	}
	switch mode {
	case modeCreate:
		if input.Title == nil {
			return input, providerError(op, "a title is required")
		}
	case modeUpdate:
		if input.LastModified == nil {
			return input, providerError(op, "last_modified is required")
		}
		changes := input.Title != nil || input.Description != nil || input.DueDate != nil || input.Archived != nil
		if !changes {
			return input, providerError(op, "at least one field to change is required")
		}
		if input.Archived != nil && !*input.Archived && (input.Title != nil || input.Description != nil || input.DueDate != nil) {
			return input, providerError(op, "restoring an archived card takes no other field")
		}
	}
	return input, nil
}

// deckCardState is the body of a card change. Deck needs title, type, owner, description, and order on every
// change and drops done when it is missing, so the whole read state goes back with the change.
type deckCardState struct {
	Title       string  `json:"title"`
	Type        string  `json:"type"`
	Owner       string  `json:"owner"`
	Description string  `json:"description"`
	Order       int64   `json:"order"`
	DueDate     *string `json:"duedate"`
	StartDate   *string `json:"startdate"`
	Archived    bool    `json:"archived"`
	Done        *string `json:"done"`
	Color       *string `json:"color,omitempty"`
}

type deckCardCreateState struct {
	Title       string  `json:"title"`
	Type        string  `json:"type"`
	Order       int64   `json:"order"`
	Description string  `json:"description"`
	DueDate     *string `json:"duedate,omitempty"`
}

type deckCardMoveState struct {
	StackID int64 `json:"stackId"`
	Order   int64 `json:"order"`
}

// cardStacks reads the stacks of the bound board once, with the active or the archived cards, and drops
// deleted stacks.
func (c *Client) cardStacks(ctx context.Context, op, boardID string, archived bool) ([]rawStack, error) {
	stacks, err := c.readStacks(ctx, op, boardID, archived)
	if err != nil {
		return nil, err
	}
	live := stacks[:0:0]
	for _, stack := range stacks {
		if stack.DeletedAt == 0 {
			live = append(live, stack)
		}
	}
	return live, nil
}

// cardIn finds the card in the named stack of the read; any other card is refused like a missing one.
func cardIn(op string, stacks []rawStack, stackID, cardID string) (rawCard, error) {
	stack, err := stackIn(op, stacks, stackID)
	if err != nil {
		return rawCard{}, err
	}
	for _, card := range stack.Cards {
		if string(card.ID) == cardID {
			return card, nil
		}
	}
	return rawCard{}, deckNotFound(op)
}

// endOrder is the position after the last card of the stack, ignoring the card that is being moved.
func endOrder(stack rawStack, skipID string) int64 {
	var order int64
	for _, card := range stack.Cards {
		if string(card.ID) != skipID && card.Order >= order {
			order = card.Order + 1
		}
	}
	return min(order, maxDeckStackOrder)
}

// readCardAnswer reads the card a change answered with; stackID is the stack it must be in, empty when any.
func readCardAnswer(op string, raw json.RawMessage, id, stackID string) (DeckCard, error) {
	var card rawCard
	if err := json.Unmarshal(raw, &card); err != nil || card.ID == "" || id != "" && string(card.ID) != id ||
		stackID != "" && string(card.StackID) != stackID {
		return DeckCard{}, withUncertainty(invalidResponse(op, "the Nextcloud card answer could not be read"), uncertainDeckCard)
	}
	return cardOf(card, maxDeckCardText), nil
}

func (c *Client) createDeckCard(ctx context.Context, op string, input deckCardArguments) (any, error) {
	stacks, err := c.cardStacks(ctx, op, input.BoardID, false)
	if err != nil {
		return nil, err
	}
	target, err := stackIn(op, stacks, input.StackID)
	if err != nil {
		return nil, err
	}
	state := deckCardCreateState{Title: *input.Title, Type: "plain", Order: endOrder(target, "")}
	if input.Order != nil {
		state.Order = *input.Order
	}
	if input.Description != nil {
		state.Description = *input.Description
	}
	if input.DueDate != nil && *input.DueDate != "" {
		state.DueDate = input.DueDate
	}
	raw, err := c.deckSend(ctx, op, uncertainDeckCard, http.MethodPost, state, "boards", input.BoardID, "stacks",
		input.StackID, "cards")
	if err != nil {
		return nil, err
	}
	card, err := readCardAnswer(op, raw, "", input.StackID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"created": true, "card": card}, nil
}

func (c *Client) updateDeckCard(ctx context.Context, op string, input deckCardArguments) (any, error) {
	restore := input.Archived != nil && !*input.Archived
	stacks, err := c.cardStacks(ctx, op, input.BoardID, restore)
	if err != nil {
		return nil, err
	}
	current, err := cardIn(op, stacks, input.StackID, input.CardID)
	if err != nil {
		return nil, err
	}
	if current.LastModified != *input.LastModified {
		return nil, providerError(op, messageDeckCardChanged)
	}
	if current.Owner.UID == "" {
		return nil, invalidResponse(op, "the owner of the card could not be read")
	}
	state := deckCardState{
		Title: current.Title, Type: current.Type, Owner: current.Owner.UID, Description: current.Description,
		Order: current.Order, DueDate: current.DueDate, StartDate: current.StartDate, Archived: current.Archived,
		Done: current.Done, Color: current.Color,
	}
	if state.Type == "" {
		state.Type = "plain"
	}
	if input.Title != nil {
		state.Title = *input.Title
	}
	if input.Description != nil {
		state.Description = *input.Description
	}
	if input.DueDate != nil {
		state.DueDate = nil
		if *input.DueDate != "" {
			state.DueDate = input.DueDate
		}
	}
	if input.Archived != nil {
		state.Archived = *input.Archived
	}
	raw, err := c.deckSend(ctx, op, uncertainDeckCard, http.MethodPut, state, "boards", input.BoardID, "stacks",
		input.StackID, "cards", input.CardID)
	if err != nil {
		return nil, err
	}
	card, err := readCardAnswer(op, raw, input.CardID, input.StackID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"updated": true, "card": card}, nil
}

func (c *Client) moveDeckCard(ctx context.Context, op string, input deckCardArguments) (any, error) {
	stacks, err := c.cardStacks(ctx, op, input.BoardID, false)
	if err != nil {
		return nil, err
	}
	if _, err := cardIn(op, stacks, input.StackID, input.CardID); err != nil {
		return nil, err
	}
	target, err := stackIn(op, stacks, input.TargetStackID)
	if err != nil {
		return nil, err
	}
	targetID, err := strconv.ParseInt(input.TargetStackID, 10, 64)
	if err != nil {
		return nil, providerError(op, "a Deck ID is a number")
	}
	state := deckCardMoveState{StackID: targetID, Order: endOrder(target, input.CardID)}
	if input.Order != nil {
		state.Order = *input.Order
	}
	raw, err := c.deckSend(ctx, op, uncertainDeckCard, http.MethodPut, state, "boards", input.BoardID, "stacks",
		input.StackID, "cards", input.CardID, "reorder")
	if err != nil {
		return nil, err
	}
	card, err := readMovedCard(op, raw, input.CardID, input.TargetStackID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"moved": true, "card": card}, nil
}

// readMovedCard reads the answer of a reorder, which Deck gives as the cards of the target stack; a single
// card is accepted too. The moved card must be in it, in the target stack.
func readMovedCard(op string, raw json.RawMessage, id, stackID string) (DeckCard, error) {
	var cards []rawCard
	if err := json.Unmarshal(raw, &cards); err != nil {
		return readCardAnswer(op, raw, id, stackID)
	}
	for _, card := range cards {
		if string(card.ID) == id {
			encoded, err := json.Marshal(card)
			if err != nil {
				break
			}
			return readCardAnswer(op, encoded, id, stackID)
		}
	}
	return DeckCard{}, withUncertainty(invalidResponse(op, "the Nextcloud card answer could not be read"), uncertainDeckCard)
}

func (c *Client) deleteDeckCard(ctx context.Context, op string, input deckCardArguments) (any, error) {
	stacks, err := c.cardStacks(ctx, op, input.BoardID, false)
	if err != nil {
		return nil, err
	}
	if _, err := cardIn(op, stacks, input.StackID, input.CardID); err != nil {
		return nil, err
	}
	raw, err := c.deckSend(ctx, op, uncertainDeckCard, http.MethodDelete, nil, "boards", input.BoardID, "stacks",
		input.StackID, "cards", input.CardID)
	if err != nil {
		return nil, err
	}
	if _, err := readCardAnswer(op, raw, input.CardID, ""); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "card_id": input.CardID}, nil
}

func invokeDeckCardsWrite(op, mode string, run func(*Client, context.Context, string, deckCardArguments) (any, error),
) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		input, err := readDeckCardArguments(op, raw, mode)
		if err != nil {
			return nil, err
		}
		client, err := openDeck(ctx, resolved, secrets, red, input.BoardID)
		if err != nil {
			return nil, err
		}
		return run(client, ctx, op, input)
	}
}

var (
	invokeDeckCardsCreate = invokeDeckCardsWrite("create Deck card", modeCreate, (*Client).createDeckCard)
	invokeDeckCardsUpdate = invokeDeckCardsWrite("update Deck card", modeUpdate, (*Client).updateDeckCard)
	invokeDeckCardsMove   = invokeDeckCardsWrite("move Deck card", modeMove, (*Client).moveDeckCard)
	invokeDeckCardsDelete = invokeDeckCardsWrite("delete Deck card", modeDelete, (*Client).deleteDeckCard)
)
