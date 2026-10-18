package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Board sharing through the Deck REST API below a bound board: a POST on the ACL collection shares the board
// with a user or group, a PUT changes the three rights, and a DELETE revokes the entry. Qatlas requires the
// manage right for all three, narrower than Deck's share right. Entries for teams and the owner are neither
// created nor changed, and the ACL entry must be shown by the one pre-read of the bound board.

const (
	uncertainDeckACL = "; the change may have been applied, check deck.boards.get before repeating"

	maxDeckParticipantBytes = 64

	deckACLUser  = "user"
	deckACLGroup = "group"
)

const deckACLBrief = `{"type":"object","properties":{"id":{"type":"string"},"participant":{"type":"string"},` +
	`"type":{"type":"string","enum":["user","group"]},"can_edit":{"type":"boolean"},"can_share":{"type":"boolean"},` +
	`"can_manage":{"type":"boolean"}},"required":["id","can_edit","can_share","can_manage"],"additionalProperties":false}`

const (
	deckACLTypeSchema      = `{"type":"string","enum":["user","group"]}`
	deckParticipantSchema  = `{"type":"string","minLength":1,"maxLength":64,"x-form":"a user or group ID without control characters or slashes"}`
	deckACLRightProperties = `"can_edit":{"type":"boolean"},"can_share":{"type":"boolean"},"can_manage":{"type":"boolean"}`
)

var (
	deckACLIDArgument = capability.Argument{
		Name: "acl_id", Description: "Numeric sharing entry ID as reported by deck.boards.get for this board", Required: true,
	}
	deckACLRightArguments = []capability.Argument{
		{Name: "can_edit", Description: "Right to edit cards and stacks"},
		{Name: "can_share", Description: "Right to share the board"},
		{Name: "can_manage", Description: "Right to manage the board and its sharing"},
	}
)

func deckACLOutput(flag string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"` + flag + `":{"type":"boolean"},"acl":` + deckACLBrief +
		`},"required":["` + flag + `","acl"],"additionalProperties":false}`)
}

var deckACLAdd = capability.Descriptor{
	ID: Provider + ".deck.acl.add", Version: 1, Title: "Share a Nextcloud Deck board",
	Description: "Share exactly one confirmed bound Deck board with one existing user or group of the instance, which " +
		"extends access to it; rights not given are off. Teams are not supported. The identity needs the manage right " +
		"on the board; an existing entry of the same participant is refused. One read-only pre-check precedes exactly " +
		"one request",
	Tags: []string{"nextcloud", "deck", "sharing", "create"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"type":` +
		deckACLTypeSchema + `,"participant":` + deckParticipantSchema + `,` + deckACLRightProperties + `},` +
		`"required":["board_id","type","participant"]` + `,"additionalProperties":false}`),
	OutputSchema: deckACLOutput("added"),
	Arguments: append([]capability.Argument{deckBoardIDArgument,
		{Name: "type", Description: "Participant kind, user or group", Required: true},
		{Name: "participant", Description: "ID of an existing user or group, at most 64 characters", Required: true}},
		deckACLRightArguments...),
	Fields: []capability.Field{
		{Name: "added", Description: "True when Deck shared the board"},
		{Name: "acl", Description: "The new sharing entry with ID, participant, and rights"},
	},
	Examples:              []capability.Example{{Description: "Share with a group for editing", Arguments: json.RawMessage(`{"board_id":"7","type":"group","participant":"team-a","can_edit":true}`)}},
	RequiresToolAllowList: true,
}

var deckACLUpdate = capability.Descriptor{
	ID: Provider + ".deck.acl.update", Version: 1, Title: "Change a Nextcloud Deck board sharing",
	Description: "Change the rights of exactly one confirmed sharing entry of a bound Deck board, at least one right; " +
		"rights not given keep their current value. Entries of teams and of the owner are refused. The identity " +
		"needs the manage right on the board. One read-only pre-check precedes exactly one request",
	Tags: []string{"nextcloud", "deck", "sharing", "update"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"acl_id":` +
		deckIDSchema + `,` + deckACLRightProperties + `},"required":["board_id","acl_id"],"additionalProperties":false}`),
	OutputSchema: deckACLOutput("updated"),
	Arguments:    append([]capability.Argument{deckBoardIDArgument, deckACLIDArgument}, deckACLRightArguments...),
	Fields: []capability.Field{
		{Name: "updated", Description: "True when Deck applied the change"},
		{Name: "acl", Description: "The sharing entry as Deck reports it after the change"},
	},
	Examples:              []capability.Example{{Description: "Withdraw the share right", Arguments: json.RawMessage(`{"board_id":"7","acl_id":"4","can_share":false}`)}},
	RequiresToolAllowList: true,
}

var deckACLRemove = capability.Descriptor{
	ID: Provider + ".deck.acl.remove", Version: 1, Title: "Revoke a Nextcloud Deck board sharing",
	Description: "Revoke exactly one confirmed sharing entry of a bound Deck board. Entries of teams and of the owner " +
		"are refused. The identity needs the manage right on the board. One read-only pre-check precedes exactly " +
		"one request",
	Tags: []string{"nextcloud", "deck", "sharing", "delete"}, Provider: Provider,
	Risk: deckWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,"acl_id":` +
		deckIDSchema + `},"required":["board_id","acl_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"removed":{"type":"boolean"},"acl_id":` +
		`{"type":"string"}},"required":["removed","acl_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{deckBoardIDArgument, deckACLIDArgument},
	Fields: []capability.Field{
		{Name: "removed", Description: "True when Deck revoked the sharing"},
		{Name: "acl_id", Description: "ID of the revoked sharing entry"},
	},
	Examples:              []capability.Example{{Description: "Use IDs from deck.boards.get", Arguments: json.RawMessage(`{"board_id":"7","acl_id":"4"}`)}},
	RequiresToolAllowList: true,
}

// deckACLRisk is open-world: sharing notifies the participant and widens who can reach the board.
type deckACLArguments struct {
	BoardID     string `json:"board_id"`
	ACLID       string `json:"acl_id"`
	Type        string `json:"type"`
	Participant string `json:"participant"`
	CanEdit     *bool  `json:"can_edit"`
	CanShare    *bool  `json:"can_share"`
	CanManage   *bool  `json:"can_manage"`
}

func (a deckACLArguments) hasRights() bool {
	return a.CanEdit != nil || a.CanShare != nil || a.CanManage != nil
}

// validDeckParticipant bounds the ID and keeps path and control characters out; Deck checks that it exists.
func validDeckParticipant(id string) bool {
	if id == "" || len(id) > maxDeckParticipantBytes || !utf8.ValidString(id) {
		return false
	}
	for _, r := range id {
		if unicode.IsControl(r) || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

// readDeckACLArguments decodes strictly and validates every value before any credential access.
func readDeckACLArguments(op string, raw json.RawMessage, mode string) (deckACLArguments, error) {
	var input deckACLArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	if err := checkDeckID(op, input.BoardID); err != nil {
		return input, err
	}
	if mode == modeCreate {
		if input.ACLID != "" {
			return input, providerError(op, "a new sharing takes no acl_id")
		}
		if input.Type != deckACLUser && input.Type != deckACLGroup {
			return input, providerError(op, "the type must be user or group")
		}
		if !validDeckParticipant(input.Participant) {
			return input, providerError(op, "the participant must be a user or group ID of at most 64 characters without slashes")
		}
		return input, nil
	}
	if err := checkDeckID(op, input.ACLID); err != nil {
		return input, err
	}
	if input.Type != "" || input.Participant != "" {
		return input, providerError(op, "only a board_id, an acl_id, and rights are accepted")
	}
	switch {
	case mode == modeDelete && input.hasRights():
		return input, providerError(op, "only a board_id and an acl_id are accepted")
	case mode == modeUpdate && !input.hasRights():
		return input, providerError(op, "at least one right to change is required")
	}
	return input, nil
}

// deckACLState is the body of a sharing change; Type and Participant are sent on creation only.
type deckACLState struct {
	Type        *int    `json:"type,omitempty"`
	Participant *string `json:"participant,omitempty"`
	Edit        bool    `json:"permissionEdit"`
	Share       bool    `json:"permissionShare"`
	Manage      bool    `json:"permissionManage"`
}

// entryKind names the participant kind of an ACL entry, empty for anything but a user or group.
func entryKind(entry rawACL) string {
	kind := entry.Participant.Type
	if kind == "" {
		kind = entry.Type
	}
	if name := participantType(kind); name == deckACLUser || name == deckACLGroup {
		return name
	}
	return ""
}

// changeableACL finds the entry among those of the bound board. An unknown entry is refused like a foreign
// one; team and owner entries are refused because this tool only handles user and group shares.
func changeableACL(op string, acl []rawACL, aclID string) (rawACL, error) {
	for _, entry := range acl {
		if string(entry.ID) != aclID {
			continue
		}
		if entry.Owner || entryKind(entry) == "" {
			return rawACL{}, providerError(op, "this sharing entry cannot be changed with this tool")
		}
		return entry, nil
	}
	return rawACL{}, deckNotFound(op)
}

// readACLAnswer reads the entry a change answered with; id is the expected ID, empty for a creation.
func readACLAnswer(op string, raw json.RawMessage, id, participant, kind string) (map[string]any, error) {
	var entry rawACL
	if err := json.Unmarshal(raw, &entry); err != nil || entry.ID == "" || id != "" && string(entry.ID) != id {
		return nil, withUncertainty(invalidResponse(op, "the Nextcloud sharing answer could not be read"), uncertainDeckACL)
	}
	var cut cutter
	return map[string]any{"id": cut.text(string(entry.ID), maxDeckTitleBytes), "participant": cut.text(participant, maxDeckTitleBytes),
		"type": kind, "can_edit": entry.Edit, "can_share": entry.Share, "can_manage": entry.Manage}, nil
}

func (c *Client) addDeckACL(ctx context.Context, op string, input deckACLArguments) (any, error) {
	board, err := c.manageBoard(ctx, op, input.BoardID)
	if err != nil {
		return nil, err
	}
	for _, entry := range board.ACL {
		if entry.Participant.UID == input.Participant && entryKind(entry) == input.Type {
			return nil, providerError(op, "the board is already shared with this participant, change the entry instead")
		}
	}
	kind := 0
	if input.Type == deckACLGroup {
		kind = 1
	}
	state := deckACLState{Type: &kind, Participant: &input.Participant}
	state.Edit, state.Share, state.Manage = isSet(input.CanEdit), isSet(input.CanShare), isSet(input.CanManage)
	raw, err := c.deckSend(ctx, op, uncertainDeckACL, http.MethodPost, state, "boards", input.BoardID, "acl")
	if err != nil {
		return nil, err
	}
	acl, err := readACLAnswer(op, raw, "", input.Participant, input.Type)
	if err != nil {
		return nil, err
	}
	return map[string]any{"added": true, "acl": acl}, nil
}

func isSet(flag *bool) bool { return flag != nil && *flag }

func (c *Client) updateDeckACL(ctx context.Context, op string, input deckACLArguments) (any, error) {
	board, err := c.manageBoard(ctx, op, input.BoardID)
	if err != nil {
		return nil, err
	}
	current, err := changeableACL(op, board.ACL, input.ACLID)
	if err != nil {
		return nil, err
	}
	// Deck takes all three rights on every change, so what is not given stays as read.
	state := deckACLState{Edit: current.Edit, Share: current.Share, Manage: current.Manage}
	if input.CanEdit != nil {
		state.Edit = *input.CanEdit
	}
	if input.CanShare != nil {
		state.Share = *input.CanShare
	}
	if input.CanManage != nil {
		state.Manage = *input.CanManage
	}
	raw, err := c.deckSend(ctx, op, uncertainDeckACL, http.MethodPut, state, "boards", input.BoardID, "acl", input.ACLID)
	if err != nil {
		return nil, err
	}
	acl, err := readACLAnswer(op, raw, input.ACLID, current.Participant.UID, entryKind(current))
	if err != nil {
		return nil, err
	}
	return map[string]any{"updated": true, "acl": acl}, nil
}

func (c *Client) removeDeckACL(ctx context.Context, op string, input deckACLArguments) (any, error) {
	board, err := c.manageBoard(ctx, op, input.BoardID)
	if err != nil {
		return nil, err
	}
	if _, err := changeableACL(op, board.ACL, input.ACLID); err != nil {
		return nil, err
	}
	// Deck answers a revocation with an empty or unspecified body, so only the status counts.
	if _, err := c.deckSend(ctx, op, uncertainDeckACL, http.MethodDelete, nil, "boards", input.BoardID, "acl", input.ACLID); err != nil {
		return nil, err
	}
	return map[string]any{"removed": true, "acl_id": input.ACLID}, nil
}

func invokeDeckACLWrite(op, mode string, run func(*Client, context.Context, string, deckACLArguments) (any, error),
) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		input, err := readDeckACLArguments(op, raw, mode)
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
	invokeDeckACLAdd    = invokeDeckACLWrite("share Deck board", modeCreate, (*Client).addDeckACL)
	invokeDeckACLUpdate = invokeDeckACLWrite("update Deck sharing", modeUpdate, (*Client).updateDeckACL)
	invokeDeckACLRemove = invokeDeckACLWrite("revoke Deck sharing", modeDelete, (*Client).removeDeckACL)
)
