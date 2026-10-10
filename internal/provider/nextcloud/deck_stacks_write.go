package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Stack administration through the Deck REST API below a bound board: a POST on the stack collection
// creates a stack, a PUT changes title and order together, and a DELETE removes the stack with its cards.
// Deck needs the manage right on the board for all three. The stack must be shown by the one pre-read of
// the bound board; Deck itself would accept a stack ID of another board in the path.

const (
	maxDeckStackOrder = 10000

	uncertainDeckStack = "; the change may have been applied, check deckstacks.list before repeating"

	messageDeckStackList = "the Nextcloud board answer does not list its stacks"
)

const (
	deckOrderSchema = `{"type":"integer","minimum":0,"maximum":10000}`
	deckStackBrief  = `{"type":"object","properties":{"id":{"type":"string"},"title":{"type":"string"},` +
		`"order":{"type":"integer"}},"required":["id","title","order"],"additionalProperties":false}`
)

var (
	deckStackIDArgument = capability.Argument{
		Name: "stack_id", Description: "Numeric Deck stack ID as reported by deckstacks.list for this board", Required: true,
	}
	deckOrderArgument = capability.Argument{Name: "order", Description: "Position of the stack in the board, 0 to 10000"}
)

var deckStacksCreate = capability.Descriptor{
	ID: Provider + ".deckstacks.create", Version: 1, Title: "Create a Nextcloud Deck stack",
	Description: "Create exactly one confirmed stack in a bound Deck board; the identity needs the manage right on " +
		"the board. One read-only pre-check precedes exactly one request; without order the stack goes last",
	Tags: []string{"nextcloud", "deck", "stacks", "create"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"title":` +
		deckTitleSchema + `,"order":` + deckOrderSchema + `},"required":["board_id","title"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"created":{"type":"boolean"},"stack":` +
		deckStackBrief + `},"required":["created","stack"],"additionalProperties":false}`),
	Arguments: []capability.Argument{deckBoardIDArgument,
		{Name: "title", Description: "Stack title, 1 to 100 characters", Required: true}, deckOrderArgument},
	Fields: []capability.Field{
		{Name: "created", Description: "True when Deck created the stack"},
		{Name: "stack", Description: "The new stack with ID, title, and order"},
	},
	Examples: []capability.Example{{Description: "Add a stack", Arguments: json.RawMessage(`{"board_id":"7","title":"Doing"}`)}},
}

var deckStacksUpdate = capability.Descriptor{
	ID: Provider + ".deckstacks.update", Version: 1, Title: "Change a Nextcloud Deck stack",
	Description: "Change the title or order of exactly one confirmed stack of a bound Deck board, at least one " +
		"field; the identity needs the manage right on the board. One read-only pre-check precedes exactly one " +
		"request; fields not given keep their current value",
	Tags: []string{"nextcloud", "deck", "stacks", "update", "reorder"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"stack_id":` +
		deckIDSchema + `,"title":` + deckTitleSchema + `,"order":` + deckOrderSchema + `},` +
		`"required":["board_id","stack_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"},"stack":` +
		deckStackBrief + `},"required":["updated","stack"],"additionalProperties":false}`),
	Arguments: []capability.Argument{deckBoardIDArgument, deckStackIDArgument,
		{Name: "title", Description: "New stack title, 1 to 100 characters"}, deckOrderArgument},
	Fields: []capability.Field{
		{Name: "updated", Description: "True when Deck applied the change"},
		{Name: "stack", Description: "The stack as Deck reports it after the change"},
	},
	Examples: []capability.Example{{Description: "Rename a stack", Arguments: json.RawMessage(`{"board_id":"7","stack_id":"3","title":"Done"}`)}},
}

var deckStacksDelete = capability.Descriptor{
	ID: Provider + ".deckstacks.delete", Version: 1, Title: "Delete a Nextcloud Deck stack",
	Description: "Delete exactly one confirmed stack of a bound Deck board; the cards it contains are deleted " +
		"with it. The identity needs the manage right on the board. One read-only pre-check precedes exactly " +
		"one request",
	Tags: []string{"nextcloud", "deck", "stacks", "delete"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"stack_id":` +
		deckIDSchema + `},"required":["board_id","stack_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"},"stack_id":` +
		`{"type":"string"}},"required":["deleted","stack_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{deckBoardIDArgument, deckStackIDArgument},
	Fields: []capability.Field{
		{Name: "deleted", Description: "True when Deck deleted the stack and its cards"},
		{Name: "stack_id", Description: "ID of the deleted stack"},
	},
	Examples: []capability.Example{{Description: "Use IDs from deckstacks.list", Arguments: json.RawMessage(`{"board_id":"7","stack_id":"3"}`)}},
	// Reachable only through a tools list, never through a profile.
	RequiresToolAllowList: true,
}

type deckStackArguments struct {
	BoardID string  `json:"board_id"`
	StackID string  `json:"stack_id"`
	Title   *string `json:"title"`
	Order   *int64  `json:"order"`
}

// readDeckStackArguments decodes strictly and validates every value before any credential access.
func readDeckStackArguments(op string, raw json.RawMessage, mode string) (deckStackArguments, error) {
	var input deckStackArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	if err := checkDeckID(op, input.BoardID); err != nil {
		return input, err
	}
	if mode == modeCreate {
		if input.StackID != "" {
			return input, providerError(op, "a new stack takes no stack_id")
		}
		if input.Title == nil {
			return input, providerError(op, "a title is required")
		}
	} else if err := checkDeckID(op, input.StackID); err != nil {
		return input, err
	}
	title, err := cleanDeckTitle(op, input.Title)
	if err != nil {
		return input, err
	}
	input.Title = title
	if input.Order != nil && (*input.Order < 0 || *input.Order > maxDeckStackOrder) {
		return input, providerError(op, "the order must be a whole number from 0 to 10000")
	}
	changes := input.Title != nil || input.Order != nil
	switch {
	case mode == modeDelete && changes:
		return input, providerError(op, "only a board_id and a stack_id are accepted")
	case mode == modeUpdate && !changes:
		return input, providerError(op, "at least one field to change is required")
	}
	return input, nil
}

// deckStackState is the body of a stack write; json.Marshal does the escaping.
type deckStackState struct {
	Title string `json:"title"`
	Order int64  `json:"order"`
}

// manageStacks reads the bound board once and returns its live stacks when the identity may manage it.
// A board answer without a stack list cannot prove membership and is refused rather than guessed.
func (c *Client) manageStacks(ctx context.Context, op, boardID string) ([]rawStack, error) {
	board, err := c.manageBoard(ctx, op, boardID)
	if err != nil {
		return nil, err
	}
	if board.Stacks == nil {
		return nil, invalidResponse(op, messageDeckStackList)
	}
	live := []rawStack{}
	for _, stack := range *board.Stacks {
		if stack.DeletedAt == 0 {
			live = append(live, stack)
		}
	}
	return live, nil
}

// stackIn finds the stack among those of the bound board; a stack of another board is refused like a
// missing one.
func stackIn(op string, stacks []rawStack, stackID string) (rawStack, error) {
	for _, stack := range stacks {
		if string(stack.ID) == stackID {
			return stack, nil
		}
	}
	return rawStack{}, deckNotFound(op)
}

// readStackAnswer reads the stack a change answered with; id is the expected ID, empty for a creation.
func readStackAnswer(op string, raw json.RawMessage, id string) (map[string]any, error) {
	var stack rawStack
	if err := json.Unmarshal(raw, &stack); err != nil || stack.ID == "" || id != "" && string(stack.ID) != id {
		return nil, withUncertainty(invalidResponse(op, "the Nextcloud stack answer could not be read"), uncertainDeckStack)
	}
	var cut cutter
	return map[string]any{"id": cut.text(string(stack.ID), maxDeckTitleBytes),
		"title": cut.text(stack.Title, maxDeckTitleBytes), "order": stack.Order}, nil
}

func (c *Client) createDeckStack(ctx context.Context, op string, input deckStackArguments) (any, error) {
	stacks, err := c.manageStacks(ctx, op, input.BoardID)
	if err != nil {
		return nil, err
	}
	var order int64
	if input.Order != nil {
		order = *input.Order
	} else {
		for _, stack := range stacks {
			if stack.Order >= order {
				order = stack.Order + 1
			}
		}
		order = min(order, maxDeckStackOrder)
	}
	raw, err := c.deckSend(ctx, op, uncertainDeckStack, http.MethodPost,
		deckStackState{Title: *input.Title, Order: order}, "boards", input.BoardID, "stacks")
	if err != nil {
		return nil, err
	}
	stack, err := readStackAnswer(op, raw, "")
	if err != nil {
		return nil, err
	}
	return map[string]any{"created": true, "stack": stack}, nil
}

func (c *Client) updateDeckStack(ctx context.Context, op string, input deckStackArguments) (any, error) {
	stacks, err := c.manageStacks(ctx, op, input.BoardID)
	if err != nil {
		return nil, err
	}
	current, err := stackIn(op, stacks, input.StackID)
	if err != nil {
		return nil, err
	}
	// Deck requires title and order on every change, so what is not given stays as read.
	state := deckStackState{Title: current.Title, Order: current.Order}
	if input.Title != nil {
		state.Title = *input.Title
	}
	if input.Order != nil {
		state.Order = *input.Order
	}
	raw, err := c.deckSend(ctx, op, uncertainDeckStack, http.MethodPut, state, "boards", input.BoardID, "stacks", input.StackID)
	if err != nil {
		return nil, err
	}
	stack, err := readStackAnswer(op, raw, input.StackID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"updated": true, "stack": stack}, nil
}

func (c *Client) deleteDeckStack(ctx context.Context, op string, input deckStackArguments) (any, error) {
	stacks, err := c.manageStacks(ctx, op, input.BoardID)
	if err != nil {
		return nil, err
	}
	if _, err := stackIn(op, stacks, input.StackID); err != nil {
		return nil, err
	}
	raw, err := c.deckSend(ctx, op, uncertainDeckStack, http.MethodDelete, nil, "boards", input.BoardID, "stacks", input.StackID)
	if err != nil {
		return nil, err
	}
	if _, err := readStackAnswer(op, raw, input.StackID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "stack_id": input.StackID}, nil
}

func invokeDeckStacksWrite(op, mode string, run func(*Client, context.Context, string, deckStackArguments) (any, error),
) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		input, err := readDeckStackArguments(op, raw, mode)
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
	invokeDeckStacksCreate = invokeDeckStacksWrite("create Deck stack", modeCreate, (*Client).createDeckStack)
	invokeDeckStacksUpdate = invokeDeckStacksWrite("update Deck stack", modeUpdate, (*Client).updateDeckStack)
	invokeDeckStacksDelete = invokeDeckStacksWrite("delete Deck stack", modeDelete, (*Client).deleteDeckStack)
)
