package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Board administration through the Deck REST API: a POST on the board collection creates a board, a PUT on
// a board changes title, color, and archived state together, and a DELETE marks it deleted, which Deck can
// undo; Qatlas offers no undo. The rights of the identity stay the upper bound.

const (
	maxDeckBoardTitle = 100
	// defaultDeckColor is the Deck blue; Deck requires a color on creation.
	defaultDeckColor = "0087C5"

	uncertainDeckBoard = "; the change may have been applied, check deckboards.list or deckboards.get before repeating"

	messageDeckManage       = "this connection or identity may not change this Deck board"
	messageDeckCreateTarget = "creating a Deck board needs the general deck target of the connection"
)

const (
	deckTitleSchema = `{"type":"string","minLength":1,"maxLength":100,"x-form":"a board title without control characters"}`
	deckColorSchema = `{"type":"string","pattern":"^[0-9a-fA-F]{6}$","x-form":"six hex digits without #"}`
)

func deckWriteRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: deckSensitivity}
}

var (
	deckTitleArgument = capability.Argument{Name: "title", Description: "Board title, 1 to 100 characters"}
	deckColorArgument = capability.Argument{Name: "color", Description: "Board color as six hex digits without #"}
)

var deckBoardsCreate = capability.Descriptor{
	ID: Provider + ".deckboards.create", Version: 1, Title: "Create a Nextcloud Deck board",
	Description: "Create exactly one confirmed Deck board owned by the Nextcloud identity; needs the general deck " +
		"target of the connection, which binds the new board; the color defaults to the Deck blue",
	Tags: []string{"nextcloud", "deck", "boards", "create"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"title":` + deckTitleSchema + `,"color":` +
		deckColorSchema + `},"required":["title"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"created":{"type":"boolean"},"board":` +
		deckBoardSchema + `},"required":["created","board"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: deckTitleArgument.Name, Description: deckTitleArgument.Description, Required: true}, deckColorArgument},
	Fields: []capability.Field{
		{Name: "created", Description: "True when Deck created the board"},
		{Name: "board", Description: "The new board with its ID, title, color, and archived flag; pass its ID on"},
	},
	Examples: []capability.Example{{Description: "Create a board", Arguments: json.RawMessage(`{"title":"Roadmap"}`)}},
}

var deckBoardsUpdate = capability.Descriptor{
	ID: Provider + ".deckboards.update", Version: 1, Title: "Change a Nextcloud Deck board",
	Description: "Change the title, color, or archived state of exactly one confirmed bound Deck board, at least " +
		"one field; the identity needs the manage right on the board. One read-only pre-check precedes exactly " +
		"one change request; fields not given keep their current value",
	Tags: []string{"nextcloud", "deck", "boards", "update", "archive"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"title":` +
		deckTitleSchema + `,"color":` + deckColorSchema + `,"archived":{"type":"boolean"}},` +
		`"required":["board_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"},"board":` +
		deckBoardSchema + `},"required":["updated","board"],"additionalProperties":false}`),
	Arguments: []capability.Argument{deckBoardIDArgument, deckTitleArgument, deckColorArgument,
		{Name: "archived", Description: "True archives the board, false restores it from the archive"}},
	Fields: []capability.Field{
		{Name: "updated", Description: "True when Deck applied the change"},
		{Name: "board", Description: "The board as Deck reports it after the change"},
	},
	Examples: []capability.Example{{Description: "Rename a board", Arguments: json.RawMessage(`{"board_id":"7","title":"Plan"}`)}},
}

var deckBoardsDelete = capability.Descriptor{
	ID: Provider + ".deckboards.delete", Version: 1, Title: "Delete a Nextcloud Deck board",
	Description: "Delete exactly one confirmed bound Deck board; the identity needs the manage right on the board. " +
		"Deck deletes softly and keeps the board recoverable, but Qatlas offers no restore. One read-only " +
		"pre-check precedes exactly one request",
	Tags: []string{"nextcloud", "deck", "boards", "delete"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema +
		`},"required":["board_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"},"board_id":` +
		`{"type":"string"}},"required":["deleted","board_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{deckBoardIDArgument},
	Fields: []capability.Field{
		{Name: "deleted", Description: "True when Deck deleted the board"},
		{Name: "board_id", Description: "ID of the deleted board"},
	},
	Examples: []capability.Example{{Description: "Use a board_id from deckboards.list", Arguments: json.RawMessage(`{"board_id":"7"}`)}},
	// Reachable only through a tools list, never through a profile.
	RequiresToolAllowList: true,
}

type deckWriteArguments struct {
	BoardID  string  `json:"board_id"`
	Title    *string `json:"title"`
	Color    *string `json:"color"`
	Archived *bool   `json:"archived"`
}

// readDeckWriteArguments decodes strictly and validates every value before any credential access.
func readDeckWriteArguments(op string, raw json.RawMessage, mode string) (deckWriteArguments, error) {
	var input deckWriteArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	if mode == modeCreate {
		if input.BoardID != "" || input.Archived != nil {
			return input, providerError(op, "a new board takes only a title and a color")
		}
		if input.Title == nil {
			return input, providerError(op, "a title is required")
		}
	} else if err := checkDeckID(op, input.BoardID); err != nil {
		return input, err
	}
	if input.Title != nil {
		title := strings.TrimSpace(*input.Title)
		if title == "" || utf8.RuneCountInString(title) > maxDeckBoardTitle || hasControl(title) || !utf8.ValidString(title) {
			return input, providerError(op, "the title must be 1 to 100 characters without control characters")
		}
		input.Title = &title
	}
	if input.Color != nil && !validTagColor(*input.Color) {
		return input, providerError(op, "the color must be six hex digits without #")
	}
	changes := input.Title != nil || input.Color != nil || input.Archived != nil
	switch {
	case mode == modeDelete && changes:
		return input, providerError(op, "only a board_id is accepted")
	case mode == modeUpdate && !changes:
		return input, providerError(op, "at least one field to change is required")
	}
	return input, nil
}

// deckBoardState is the body of a board write; json.Marshal does the escaping.
type deckBoardState struct {
	Title    string `json:"title"`
	Color    string `json:"color"`
	Archived *bool  `json:"archived,omitempty"`
}

// deckSend sends one authenticated JSON change below the fixed REST root. A refusal of a client error or a
// redirect is clear; any other failure, including an unreadable answer, leaves the outcome open and carries
// the hint. It never repeats the request and never reads the body of a failed answer into an error.
func (c *Client) deckSend(ctx context.Context, op, method string, payload any, suffix ...string) (json.RawMessage, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, providerError(op, "the request could not be built")
		}
		body = bytes.NewReader(encoded)
	}
	segments := append(append(append([]string{}, c.install...), deckRoot...), suffix...)
	req, err := http.NewRequestWithContext(ctx, method, c.origin+escapePath(segments), body)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, sentTransportError(op, err, uncertainDeckBoard)
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusForbidden:
		return nil, &provider.Error{Class: provider.ClassPermission, Op: op, Message: messageDeckManage}
	case response.StatusCode == http.StatusNotFound:
		return nil, &provider.Error{Class: provider.ClassNotFound, Op: op, Message: messageDeckNotFound}
	case response.StatusCode < 200 || response.StatusCode >= 300:
		return nil, sentStatusError(op, response.StatusCode, uncertainDeckBoard)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil || len(raw) > maxBodyBytes {
		return nil, withUncertainty(invalidResponse(op, "the Nextcloud response could not be read within the size limit"), uncertainDeckBoard)
	}
	return raw, nil
}

// readBoardAnswer reads the board a change answered with; id is the expected ID, empty for a creation.
func readBoardAnswer(op string, raw json.RawMessage, id string) (rawBoard, error) {
	var board rawBoard
	if err := json.Unmarshal(raw, &board); err != nil || board.ID == "" || id != "" && string(board.ID) != id {
		return board, withUncertainty(invalidResponse(op, "the Nextcloud board answer could not be read"), uncertainDeckBoard)
	}
	return board, nil
}

// manageBoard reads the board once and returns it only when the identity may manage it; a deleted board
// counts as missing. The refusal names no board.
func (c *Client) manageBoard(ctx context.Context, op, boardID string) (rawBoard, error) {
	var board rawBoard
	body, err := c.deckGet(ctx, op, "boards", boardID)
	if err != nil {
		return board, err
	}
	var permitted struct {
		rawBoard
		Permissions struct {
			Manage bool `json:"PERMISSION_MANAGE"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(body, &permitted); err != nil || string(permitted.ID) != boardID {
		return board, invalidResponse(op, "the Nextcloud board could not be read")
	}
	if permitted.DeletedAt != 0 {
		return board, &provider.Error{Class: provider.ClassNotFound, Op: op, Message: messageDeckNotFound}
	}
	if !permitted.Permissions.Manage {
		return board, &provider.Error{Class: provider.ClassPermission, Op: op, Message: messageDeckManage}
	}
	return permitted.rawBoard, nil
}

func (c *Client) createDeckBoard(ctx context.Context, op string, input deckWriteArguments) (any, error) {
	color := defaultDeckColor
	if input.Color != nil {
		color = *input.Color
	}
	raw, err := c.deckSend(ctx, op, http.MethodPost, deckBoardState{Title: *input.Title, Color: color}, "boards")
	if err != nil {
		return nil, err
	}
	board, err := readBoardAnswer(op, raw, "")
	if err != nil {
		return nil, err
	}
	return map[string]any{"created": true, "board": boardOf(board)}, nil
}

func (c *Client) updateDeckBoard(ctx context.Context, op string, input deckWriteArguments) (any, error) {
	current, err := c.manageBoard(ctx, op, input.BoardID)
	if err != nil {
		return nil, err
	}
	// Deck requires title, color, and archived on every change, so what is not given stays as read.
	state := deckBoardState{Title: current.Title, Color: strings.TrimPrefix(current.Color, "#"), Archived: &current.Archived}
	if input.Title != nil {
		state.Title = *input.Title
	}
	if input.Color != nil {
		state.Color = *input.Color
	}
	if input.Archived != nil {
		state.Archived = input.Archived
	}
	if !validTagColor(state.Color) {
		return nil, providerError(op, "the current color of the board is unusable; pass a color")
	}
	raw, err := c.deckSend(ctx, op, http.MethodPut, state, "boards", input.BoardID)
	if err != nil {
		return nil, err
	}
	board, err := readBoardAnswer(op, raw, input.BoardID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"updated": true, "board": boardOf(board)}, nil
}

func (c *Client) deleteDeckBoard(ctx context.Context, op string, input deckWriteArguments) (any, error) {
	if _, err := c.manageBoard(ctx, op, input.BoardID); err != nil {
		return nil, err
	}
	raw, err := c.deckSend(ctx, op, http.MethodDelete, nil, "boards", input.BoardID)
	if err != nil {
		return nil, err
	}
	if _, err := readBoardAnswer(op, raw, input.BoardID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "board_id": input.BoardID}, nil
}

func invokeDeckBoardsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create Deck board"
	input, err := readDeckWriteArguments(op, raw, modeCreate)
	if err != nil {
		return nil, err
	}
	bound, err := requireDeck(resolved)
	if err != nil {
		return nil, err
	}
	// A deck/ID binding would not hold the new board, so only the general target may create one.
	if !bound.all {
		return nil, &provider.Error{Class: provider.ClassPermission, Op: op, Message: messageDeckCreateTarget}
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.createDeckBoard(ctx, op, input)
}

func invokeDeckBoardsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update Deck board"
	input, err := readDeckWriteArguments(op, raw, modeUpdate)
	if err != nil {
		return nil, err
	}
	client, err := openDeck(ctx, resolved, secrets, red, input.BoardID)
	if err != nil {
		return nil, err
	}
	return client.updateDeckBoard(ctx, op, input)
}

func invokeDeckBoardsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete Deck board"
	input, err := readDeckWriteArguments(op, raw, modeDelete)
	if err != nil {
		return nil, err
	}
	client, err := openDeck(ctx, resolved, secrets, red, input.BoardID)
	if err != nil {
		return nil, err
	}
	return client.deleteDeckBoard(ctx, op, input)
}
