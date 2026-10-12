package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Labels and members on a card of a bound board. Two reads precede the one request: the board shows the labels
// and members it owns, the stacks show whether the card is in the named stack. Deck checks neither against the
// path, so anything the reads do not show is refused like a missing object, without naming it.

const deckUserSchema = `{"type":"string","minLength":1,"maxLength":64}`

const deckAssignResultSchema = `{"type":"object","properties":{"assigned":{"type":"boolean"},"card_id":` +
	`{"type":"string"},"%s":{"type":"string"}},"required":["assigned","card_id","%s"],"additionalProperties":false}`

func deckAssignResult(id string) json.RawMessage {
	return json.RawMessage(strings.ReplaceAll(deckAssignResultSchema, "%s", id))
}

var deckCardsLabels = capability.Descriptor{
	ID: Provider + ".deckcards.labels", Version: 1, Title: "Assign or remove a Nextcloud Deck card label",
	Description: "Assign a label to, or remove it from, exactly one confirmed active card of a bound Deck board; the " +
		"identity needs the edit right on the board. Two read-only pre-checks must show the label among the labels " +
		"of the board and the card in the named stack, then exactly one request. Labels are not created or changed",
	Tags: []string{"nextcloud", "deck", "cards", "labels"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"stack_id":` +
		deckIDSchema + `,"card_id":` + deckIDSchema + `,"label_id":` + deckIDSchema + `,"assign":{"type":"boolean"}},` +
		`"required":["board_id","stack_id","card_id","label_id","assign"],"additionalProperties":false}`),
	OutputSchema: deckAssignResult("label_id"),
	Arguments: []capability.Argument{deckBoardIDArgument, deckStackOfCardArgument, deckCardIDArgument,
		{Name: "label_id", Description: "Numeric ID of a label of this board, as reported by deckboards.get", Required: true},
		{Name: "assign", Description: "True assigns the label, false removes it", Required: true}},
	Fields: []capability.Field{
		{Name: "assigned", Description: "Whether the label is on the card after the change"},
		{Name: "card_id", Description: "ID of the card"},
		{Name: "label_id", Description: "ID of the label"},
	},
	Examples: []capability.Example{{Description: "Assign a label from deckboards.get",
		Arguments: json.RawMessage(`{"board_id":"7","stack_id":"3","card_id":"21","label_id":"4","assign":true}`)}},
}

var deckCardsAssignees = capability.Descriptor{
	ID: Provider + ".deckcards.assignees", Version: 1, Title: "Assign or unassign a Nextcloud Deck card member",
	Description: "Assign a member of a bound Deck board to, or remove them from, exactly one confirmed active card; the " +
		"identity needs the edit right on the board. Two read-only pre-checks must show the user among the members " +
		"or the owner of the board and the card in the named stack, then exactly one request. Deck may notify the " +
		"assigned user. Groups and teams are not assigned",
	Tags: []string{"nextcloud", "deck", "cards", "assignees"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"stack_id":` +
		deckIDSchema + `,"card_id":` + deckIDSchema + `,"user_id":` + deckUserSchema + `,"assign":{"type":"boolean"}},` +
		`"required":["board_id","stack_id","card_id","user_id","assign"],"additionalProperties":false}`),
	OutputSchema: deckAssignResult("user_id"),
	Arguments: []capability.Argument{deckBoardIDArgument, deckStackOfCardArgument, deckCardIDArgument,
		{Name: "user_id", Description: "Nextcloud user ID of a member of this board, as reported by deckboards.get", Required: true},
		{Name: "assign", Description: "True assigns the user, false removes the user", Required: true}},
	Fields: []capability.Field{
		{Name: "assigned", Description: "Whether the user is assigned to the card after the change"},
		{Name: "card_id", Description: "ID of the card"},
		{Name: "user_id", Description: "ID of the user"},
	},
	Examples: []capability.Example{{Description: "Assign a board member",
		Arguments: json.RawMessage(`{"board_id":"7","stack_id":"3","card_id":"21","user_id":"dora","assign":true}`)}},
}

type deckAssignArguments struct {
	BoardID string `json:"board_id"`
	StackID string `json:"stack_id"`
	CardID  string `json:"card_id"`
	LabelID string `json:"label_id"`
	UserID  string `json:"user_id"`
	Assign  *bool  `json:"assign"`
}

// readDeckAssignArguments decodes strictly and validates before any credential access; label selects the
// label tool, otherwise the user tool.
func readDeckAssignArguments(op string, raw json.RawMessage, label bool) (deckAssignArguments, error) {
	var input deckAssignArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Assign == nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	ids := []string{input.BoardID, input.StackID, input.CardID}
	if label {
		ids = append(ids, input.LabelID)
	} else if input.LabelID != "" || !validUserID(input.UserID) {
		return input, providerError(op, "the user ID is not valid")
	}
	if label && input.UserID != "" {
		return input, providerError(op, "the argument user_id is not accepted by this tool")
	}
	for _, id := range ids {
		if err := checkDeckID(op, id); err != nil {
			return input, err
		}
	}
	return input, nil
}

type deckLabelBody struct {
	LabelID int64 `json:"labelId"`
}

// deckUserBody names the user type explicitly: groups and teams are never assigned.
type deckUserBody struct {
	UserID string `json:"userId"`
	Type   int    `json:"type"`
}

// readBoardMembers reads the bound board for its labels and members; a deleted board is refused like a missing one.
func (c *Client) readBoardMembers(ctx context.Context, op, boardID string) (rawBoard, error) {
	var board rawBoard
	body, err := c.deckGet(ctx, op, "boards", boardID)
	if err != nil {
		return board, err
	}
	if err := json.Unmarshal(body, &board); err != nil || string(board.ID) != boardID {
		return board, invalidResponse(op, "the Nextcloud board could not be read")
	}
	if board.DeletedAt != 0 {
		return board, deckNotFound(op)
	}
	return board, nil
}

// isBoardUser reports whether the owner or a user member of the board has this ID; groups and teams do not count.
func isBoardUser(board rawBoard, userID string) bool {
	for _, person := range append([]rawPerson{board.Owner}, board.Users...) {
		if person.UID == userID && (person.Type == "" || person.Type == "0") {
			return true
		}
	}
	return false
}

func (c *Client) changeCardAssignment(ctx context.Context, op string, input deckAssignArguments, label bool) (any, error) {
	board, err := c.readBoardMembers(ctx, op, input.BoardID)
	if err != nil {
		return nil, err
	}
	var (
		action, subject string
		body            any
	)
	if label {
		id, err := strconv.ParseInt(input.LabelID, 10, 64)
		if err != nil {
			return nil, providerError(op, "a Deck ID is a number")
		}
		found := false
		for _, item := range board.Labels {
			found = found || string(item.ID) == input.LabelID
		}
		if !found {
			return nil, deckNotFound(op)
		}
		action, subject, body = "assignLabel", input.LabelID, deckLabelBody{LabelID: id}
		if !*input.Assign {
			action = "removeLabel"
		}
	} else {
		if !isBoardUser(board, input.UserID) {
			return nil, deckNotFound(op)
		}
		action, subject, body = "assignUser", input.UserID, deckUserBody{UserID: input.UserID}
		if !*input.Assign {
			action = "unassignUser"
		}
	}
	stacks, err := c.cardStacks(ctx, op, input.BoardID, false)
	if err != nil {
		return nil, err
	}
	if _, err := cardIn(op, stacks, input.StackID, input.CardID); err != nil {
		return nil, err
	}
	raw, err := c.deckSend(ctx, op, uncertainDeckCard, http.MethodPut, body, "boards", input.BoardID, "stacks",
		input.StackID, "cards", input.CardID, action)
	if err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, withUncertainty(invalidResponse(op, "the Nextcloud answer could not be read"), uncertainDeckCard)
	}
	var cut cutter
	key := "user_id"
	if label {
		key = "label_id"
	}
	return map[string]any{"assigned": *input.Assign, "card_id": input.CardID, key: cut.text(subject, maxDeckTitleBytes)}, nil
}

func invokeDeckAssignment(op string, label bool) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		input, err := readDeckAssignArguments(op, raw, label)
		if err != nil {
			return nil, err
		}
		client, err := openDeck(ctx, resolved, secrets, red, input.BoardID)
		if err != nil {
			return nil, err
		}
		return client.changeCardAssignment(ctx, op, input, label)
	}
}

var (
	invokeDeckCardsLabels    = invokeDeckAssignment("change Deck card label", true)
	invokeDeckCardsAssignees = invokeDeckAssignment("change Deck card assignee", false)
)
