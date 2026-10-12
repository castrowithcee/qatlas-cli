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

// Label administration through the Deck REST API below a bound board: a POST on the label collection
// creates a label, a PUT changes title and color together, and a DELETE removes the label from the board and
// its cards. Deck needs the manage right on the board for all three. The label must be shown by the one
// pre-read of the bound board; Deck itself would accept a label ID of another board in the path.

const uncertainDeckLabel = "; the change may have been applied, check deckboards.get before repeating"

const deckLabelBrief = `{"type":"object","properties":{"id":{"type":"string"},"title":{"type":"string"},` +
	`"color":{"type":"string"}},"required":["id","title","color"],"additionalProperties":false}`

var (
	deckLabelIDArgument = capability.Argument{
		Name: "label_id", Description: "Numeric Deck label ID as reported by deckboards.get for this board", Required: true,
	}
	deckLabelColorArgument = capability.Argument{Name: "color", Description: "Label color as six hex digits without #"}
)

var deckLabelsCreate = capability.Descriptor{
	ID: Provider + ".decklabels.create", Version: 1, Title: "Create a Nextcloud Deck label",
	Description: "Create exactly one confirmed label in a bound Deck board; the identity needs the manage right on " +
		"the board. One read-only pre-check precedes exactly one request",
	Tags: []string{"nextcloud", "deck", "labels", "create"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"title":` +
		deckTitleSchema + `,"color":` + deckColorSchema + `},"required":["board_id","title","color"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"created":{"type":"boolean"},"label":` +
		deckLabelBrief + `},"required":["created","label"],"additionalProperties":false}`),
	Arguments: []capability.Argument{deckBoardIDArgument,
		{Name: "title", Description: "Label title, 1 to 100 characters", Required: true},
		{Name: "color", Description: "Label color as six hex digits without #", Required: true}},
	Fields: []capability.Field{
		{Name: "created", Description: "True when Deck created the label"},
		{Name: "label", Description: "The new label with ID, title, and color"},
	},
	Examples: []capability.Example{{Description: "Add a label", Arguments: json.RawMessage(`{"board_id":"7","title":"Urgent","color":"E9322D"}`)}},
}

var deckLabelsUpdate = capability.Descriptor{
	ID: Provider + ".decklabels.update", Version: 1, Title: "Change a Nextcloud Deck label",
	Description: "Change the title or color of exactly one confirmed label of a bound Deck board, at least one " +
		"field; the identity needs the manage right on the board. One read-only pre-check precedes exactly one " +
		"request; fields not given keep their current value",
	Tags: []string{"nextcloud", "deck", "labels", "update"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"label_id":` +
		deckIDSchema + `,"title":` + deckTitleSchema + `,"color":` + deckColorSchema + `},` +
		`"required":["board_id","label_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"},"label":` +
		deckLabelBrief + `},"required":["updated","label"],"additionalProperties":false}`),
	Arguments: []capability.Argument{deckBoardIDArgument, deckLabelIDArgument,
		{Name: "title", Description: "New label title, 1 to 100 characters"}, deckLabelColorArgument},
	Fields: []capability.Field{
		{Name: "updated", Description: "True when Deck applied the change"},
		{Name: "label", Description: "The label as Deck reports it after the change"},
	},
	Examples: []capability.Example{{Description: "Recolor a label", Arguments: json.RawMessage(`{"board_id":"7","label_id":"3","color":"31CC7C"}`)}},
}

var deckLabelsDelete = capability.Descriptor{
	ID: Provider + ".decklabels.delete", Version: 1, Title: "Delete a Nextcloud Deck label",
	Description: "Delete exactly one confirmed label of a bound Deck board; Deck removes it from the cards that " +
		"carry it. The identity needs the manage right on the board. One read-only pre-check precedes exactly " +
		"one request",
	Tags: []string{"nextcloud", "deck", "labels", "delete"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"label_id":` +
		deckIDSchema + `},"required":["board_id","label_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"},"label_id":` +
		`{"type":"string"}},"required":["deleted","label_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{deckBoardIDArgument, deckLabelIDArgument},
	Fields: []capability.Field{
		{Name: "deleted", Description: "True when Deck deleted the label"},
		{Name: "label_id", Description: "ID of the deleted label"},
	},
	Examples: []capability.Example{{Description: "Use IDs from deckboards.get", Arguments: json.RawMessage(`{"board_id":"7","label_id":"3"}`)}},
	// Reachable only through a tools list, never through a profile.
	RequiresToolAllowList: true,
}

type deckLabelArguments struct {
	BoardID string  `json:"board_id"`
	LabelID string  `json:"label_id"`
	Title   *string `json:"title"`
	Color   *string `json:"color"`
}

// readDeckLabelArguments decodes strictly and validates every value before any credential access.
func readDeckLabelArguments(op string, raw json.RawMessage, mode string) (deckLabelArguments, error) {
	var input deckLabelArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	if err := checkDeckID(op, input.BoardID); err != nil {
		return input, err
	}
	if mode == modeCreate {
		if input.LabelID != "" {
			return input, providerError(op, "a new label takes no label_id")
		}
		if input.Title == nil || input.Color == nil {
			return input, providerError(op, "a title and a color are required")
		}
	} else if err := checkDeckID(op, input.LabelID); err != nil {
		return input, err
	}
	title, err := cleanDeckTitle(op, input.Title)
	if err != nil {
		return input, err
	}
	input.Title = title
	if input.Color != nil && !validTagColor(*input.Color) {
		return input, providerError(op, "the color must be six hex digits without #")
	}
	changes := input.Title != nil || input.Color != nil
	switch {
	case mode == modeDelete && changes:
		return input, providerError(op, "only a board_id and a label_id are accepted")
	case mode == modeUpdate && !changes:
		return input, providerError(op, "at least one field to change is required")
	}
	return input, nil
}

// deckLabelState is the body of a label write; json.Marshal does the escaping.
type deckLabelState struct {
	Title string `json:"title"`
	Color string `json:"color"`
}

// labelIn finds the label among those of the bound board; a label of another board is refused like a
// missing one.
func labelIn(op string, labels []rawLabel, labelID string) (rawLabel, error) {
	for _, label := range labels {
		if string(label.ID) == labelID {
			return label, nil
		}
	}
	return rawLabel{}, deckNotFound(op)
}

// readLabelAnswer reads the label a change answered with; id is the expected ID, empty for a creation.
func readLabelAnswer(op string, raw json.RawMessage, id string) (map[string]any, error) {
	var label rawLabel
	if err := json.Unmarshal(raw, &label); err != nil || label.ID == "" || id != "" && string(label.ID) != id {
		return nil, withUncertainty(invalidResponse(op, "the Nextcloud label answer could not be read"), uncertainDeckLabel)
	}
	var cut cutter
	return map[string]any{"id": cut.text(string(label.ID), maxDeckTitleBytes),
		"title": cut.text(label.Title, maxDeckTitleBytes), "color": cut.text(label.Color, maxDeckTitleBytes)}, nil
}

func (c *Client) createDeckLabel(ctx context.Context, op string, input deckLabelArguments) (any, error) {
	if _, err := c.manageBoard(ctx, op, input.BoardID); err != nil {
		return nil, err
	}
	raw, err := c.deckSend(ctx, op, uncertainDeckLabel, http.MethodPost,
		deckLabelState{Title: *input.Title, Color: *input.Color}, "boards", input.BoardID, "labels")
	if err != nil {
		return nil, err
	}
	label, err := readLabelAnswer(op, raw, "")
	if err != nil {
		return nil, err
	}
	return map[string]any{"created": true, "label": label}, nil
}

func (c *Client) updateDeckLabel(ctx context.Context, op string, input deckLabelArguments) (any, error) {
	board, err := c.manageBoard(ctx, op, input.BoardID)
	if err != nil {
		return nil, err
	}
	current, err := labelIn(op, board.Labels, input.LabelID)
	if err != nil {
		return nil, err
	}
	// Deck requires title and color on every change, so what is not given stays as read.
	state := deckLabelState{Title: current.Title, Color: current.Color}
	if input.Title != nil {
		state.Title = *input.Title
	}
	if input.Color != nil {
		state.Color = *input.Color
	}
	raw, err := c.deckSend(ctx, op, uncertainDeckLabel, http.MethodPut, state, "boards", input.BoardID, "labels", input.LabelID)
	if err != nil {
		return nil, err
	}
	label, err := readLabelAnswer(op, raw, input.LabelID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"updated": true, "label": label}, nil
}

func (c *Client) deleteDeckLabel(ctx context.Context, op string, input deckLabelArguments) (any, error) {
	board, err := c.manageBoard(ctx, op, input.BoardID)
	if err != nil {
		return nil, err
	}
	if _, err := labelIn(op, board.Labels, input.LabelID); err != nil {
		return nil, err
	}
	// Deck answers a label deletion with an empty or unspecified body, so only the status counts.
	if _, err := c.deckSend(ctx, op, uncertainDeckLabel, http.MethodDelete, nil, "boards", input.BoardID, "labels", input.LabelID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "label_id": input.LabelID}, nil
}

func invokeDeckLabelsWrite(op, mode string, run func(*Client, context.Context, string, deckLabelArguments) (any, error),
) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		input, err := readDeckLabelArguments(op, raw, mode)
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
	invokeDeckLabelsCreate = invokeDeckLabelsWrite("create Deck label", modeCreate, (*Client).createDeckLabel)
	invokeDeckLabelsUpdate = invokeDeckLabelsWrite("update Deck label", modeUpdate, (*Client).updateDeckLabel)
	invokeDeckLabelsDelete = invokeDeckLabelsWrite("delete Deck label", modeDelete, (*Client).deleteDeckLabel)
)
