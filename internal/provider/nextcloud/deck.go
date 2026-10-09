package nextcloud

import (
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

// groupDeck is the tool group of the Deck tools.
const groupDeck = "deck"

// deckSensitivity classifies Deck results: boards, cards, and their members are collaboration content.
const deckSensitivity = "nextcloud-deck"

// deckRoot are the fixed path segments of the Deck REST API; index.php is part of them because the Deck
// routes are not guaranteed to resolve through pretty URLs.
var deckRoot = []string{"index.php", "apps", "deck", "api", "v1.1"}

// Bounds of the Deck reads. Titles, names, and descriptions are untrusted provider text: each string is cut
// at a limit and every cut is reported, instead of handing on an oversized value silently.
const (
	maxDeckBoards     = 200
	maxDeckStacks     = 100
	maxDeckCards      = 500
	maxDeckMembers    = 200
	maxDeckLabels     = 100
	maxDeckAssignees  = 50
	maxDeckIDLength   = 18
	maxDeckTitleBytes = 256
	maxDeckListText   = 1 << 10
	maxDeckCardText   = maxTextBytes
)

// messageDeckNotFound answers every 404 of the Deck API. A missing app and a missing board, stack, or card
// look alike to the client, and the answer names neither.
const messageDeckNotFound = "the Nextcloud Deck app is not available, or the board, stack, or card does not exist"

// messageDeckForeign answers a stack or card outside the bound board hierarchy exactly like a missing one.
const messageDeckForeign = "this Nextcloud connection does not hold this Deck object"

const messageDeckNotBound = "this connection is not bound to this Deck board"

// cutter shortens untrusted strings at a byte limit without splitting a character and remembers whether
// anything was cut.
type cutter struct{ hit bool }

func (c *cutter) text(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	c.hit = true
	end := limit
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}

// DeckPerson is a user, group, or team the instance names.
type DeckPerson struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
}

// DeckLabel is a board label.
type DeckLabel struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Color string `json:"color,omitempty"`
}

// DeckBoard is the stable view of one board in a listing.
type DeckBoard struct {
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	Color    string      `json:"color,omitempty"`
	Archived bool        `json:"archived"`
	Owner    *DeckPerson `json:"owner,omitempty"`
	// Truncated is true when a string of this board was cut.
	Truncated bool `json:"truncated,omitempty"`
}

// DeckBoardsResult is the list of the bound boards.
type DeckBoardsResult struct {
	Boards    []DeckBoard `json:"boards"`
	Count     int         `json:"count"`
	Truncated bool        `json:"truncated,omitempty"`
}

// DeckACL is one sharing entry of a board.
type DeckACL struct {
	Participant DeckPerson `json:"participant"`
	CanEdit     bool       `json:"can_edit"`
	CanShare    bool       `json:"can_share"`
	CanManage   bool       `json:"can_manage"`
	Owner       bool       `json:"owner"`
}

// DeckBoardDetail is one board with its labels, ACL, and members.
type DeckBoardDetail struct {
	DeckBoard
	Labels  []DeckLabel  `json:"labels"`
	ACL     []DeckACL    `json:"acl"`
	Members []DeckPerson `json:"members"`
}

// DeckCard is the stable view of a card; the description is untrusted data.
type DeckCard struct {
	ID           string       `json:"id"`
	StackID      string       `json:"stack_id,omitempty"`
	Title        string       `json:"title"`
	Description  string       `json:"description,omitempty"`
	Order        int64        `json:"order"`
	Archived     bool         `json:"archived"`
	Done         string       `json:"done,omitempty"`
	DueDate      string       `json:"due_date,omitempty"`
	Owner        *DeckPerson  `json:"owner,omitempty"`
	Labels       []DeckLabel  `json:"labels"`
	AssignedTo   []DeckPerson `json:"assigned_to"`
	CreatedAt    int64        `json:"created_at,omitempty"`
	LastModified int64        `json:"last_modified,omitempty"`
	// Truncated is true when a string of this card was cut.
	Truncated bool `json:"truncated,omitempty"`
}

// DeckStack is one stack with its cards.
type DeckStack struct {
	ID    string     `json:"id"`
	Title string     `json:"title"`
	Order int64      `json:"order"`
	Cards []DeckCard `json:"cards"`
	// Truncated is true when the title of this stack was cut.
	Truncated bool `json:"truncated,omitempty"`
}

// DeckStacksResult is the stacks of one board.
type DeckStacksResult struct {
	Stacks    []DeckStack `json:"stacks"`
	Count     int         `json:"count"`
	Cards     int         `json:"cards"`
	Truncated bool        `json:"truncated,omitempty"`
}

// rawPerson reads a participant that Deck writes as an object or, in older versions, as the user ID alone.
type rawPerson struct {
	UID         string      `json:"uid"`
	DisplayName string      `json:"displayname"`
	Type        json.Number `json:"type"`
}

func (p *rawPerson) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "null" {
		return nil
	}
	if strings.HasPrefix(text, `"`) {
		return json.Unmarshal(data, &p.UID)
	}
	type plain rawPerson
	return json.Unmarshal(data, (*plain)(p))
}

var deckParticipantTypes = map[int64]string{0: "user", 1: "group", 7: "team"}

func participantType(raw json.Number) string {
	if value, err := raw.Int64(); err == nil {
		if name, ok := deckParticipantTypes[value]; ok {
			return name
		}
	}
	return ""
}

func personOf(c *cutter, raw rawPerson, fallbackType json.Number) *DeckPerson {
	if raw.UID == "" {
		return nil
	}
	kind := raw.Type
	if kind == "" {
		kind = fallbackType
	}
	return &DeckPerson{
		ID: c.text(raw.UID, maxDeckTitleBytes), Name: c.text(raw.DisplayName, maxDeckTitleBytes),
		Type: participantType(kind),
	}
}

type rawLabel struct {
	ID    flexString `json:"id"`
	Title string     `json:"title"`
	Color string     `json:"color"`
}

type rawACL struct {
	Participant rawPerson   `json:"participant"`
	Type        json.Number `json:"type"`
	Edit        bool        `json:"permissionEdit"`
	Share       bool        `json:"permissionShare"`
	Manage      bool        `json:"permissionManage"`
	Owner       bool        `json:"owner"`
}

type rawBoard struct {
	ID        flexString  `json:"id"`
	Title     string      `json:"title"`
	Color     string      `json:"color"`
	Archived  bool        `json:"archived"`
	DeletedAt int64       `json:"deletedAt"`
	Owner     rawPerson   `json:"owner"`
	Labels    []rawLabel  `json:"labels"`
	ACL       []rawACL    `json:"acl"`
	Users     []rawPerson `json:"users"`
}

type rawAssignment struct {
	Participant rawPerson   `json:"participant"`
	Type        json.Number `json:"type"`
}

type rawCard struct {
	ID           flexString      `json:"id"`
	StackID      flexString      `json:"stackId"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Order        int64           `json:"order"`
	Archived     bool            `json:"archived"`
	Done         *string         `json:"done"`
	DueDate      *string         `json:"duedate"`
	Owner        rawPerson       `json:"owner"`
	Labels       []rawLabel      `json:"labels"`
	Assigned     []rawAssignment `json:"assignedUsers"`
	CreatedAt    int64           `json:"createdAt"`
	LastModified int64           `json:"lastModified"`
}

type rawStack struct {
	ID    flexString `json:"id"`
	Title string     `json:"title"`
	Order int64      `json:"order"`
	Cards []rawCard  `json:"cards"`
}

func labelsOf(c *cutter, raw []rawLabel) []DeckLabel {
	labels := []DeckLabel{}
	for _, label := range raw {
		if len(labels) == maxDeckLabels {
			c.hit = true
			break
		}
		labels = append(labels, DeckLabel{
			ID: c.text(string(label.ID), maxDeckTitleBytes), Title: c.text(label.Title, maxDeckTitleBytes),
			Color: c.text(label.Color, maxDeckTitleBytes),
		})
	}
	return labels
}

func boardOf(raw rawBoard) DeckBoard {
	var c cutter
	board := DeckBoard{
		ID: c.text(string(raw.ID), maxDeckTitleBytes), Title: c.text(raw.Title, maxDeckTitleBytes),
		Color: c.text(raw.Color, maxDeckTitleBytes), Archived: raw.Archived, Owner: personOf(&c, raw.Owner, ""),
	}
	board.Truncated = c.hit
	return board
}

func cardOf(raw rawCard, textLimit int) DeckCard {
	var c cutter
	card := DeckCard{
		ID: c.text(string(raw.ID), maxDeckTitleBytes), StackID: c.text(string(raw.StackID), maxDeckTitleBytes),
		Title: c.text(raw.Title, maxDeckTitleBytes), Description: c.text(raw.Description, textLimit),
		Order: raw.Order, Archived: raw.Archived, Owner: personOf(&c, raw.Owner, ""),
		Labels: labelsOf(&c, raw.Labels), AssignedTo: []DeckPerson{},
		CreatedAt: raw.CreatedAt, LastModified: raw.LastModified,
	}
	if raw.Done != nil {
		card.Done = c.text(*raw.Done, maxDeckTitleBytes)
	}
	if raw.DueDate != nil {
		card.DueDate = c.text(*raw.DueDate, maxDeckTitleBytes)
	}
	for _, assignment := range raw.Assigned {
		person := personOf(&c, assignment.Participant, assignment.Type)
		if person == nil {
			continue
		}
		if len(card.AssignedTo) == maxDeckAssignees {
			c.hit = true
			break
		}
		card.AssignedTo = append(card.AssignedTo, *person)
	}
	card.Truncated = c.hit
	return card
}

// deckGet performs one authenticated Deck GET below the fixed REST root and returns the body. The suffix is
// built from validated IDs and fixed words only; the body of a failed answer is never read into an error.
func (c *Client) deckGet(ctx context.Context, op string, suffix ...string) (json.RawMessage, error) {
	segments := append(append(append([]string{}, c.install...), deckRoot...), suffix...)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.origin+escapePath(segments), nil)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("Accept", "application/json")

	response, err := c.http.Do(req)
	if err != nil {
		return nil, provider.Transport(op, "Nextcloud", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, &provider.Error{Class: provider.ClassNotFound, Op: op, Message: messageDeckNotFound}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, statusError(op, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil || len(body) > maxBodyBytes {
		return nil, invalidResponse(op, "the Nextcloud response could not be read within the size limit")
	}
	return body, nil
}

func checkDeckID(op, id string) error {
	if id == "" || len(id) > maxDeckIDLength || !digitsOnly(id) {
		return providerError(op, "a Deck ID is a number")
	}
	return nil
}

func deckNotFound(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op, Message: messageDeckForeign}
}

// ListDeckBoards reads the boards of the identity and keeps those the connection binds.
func (c *Client) ListDeckBoards(ctx context.Context, bound selection) (*DeckBoardsResult, error) {
	const op = "list Deck boards"
	body, err := c.deckGet(ctx, op, "boards")
	if err != nil {
		return nil, err
	}
	var items []rawBoard
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, invalidResponse(op, "the Nextcloud board list could not be read")
	}
	result := &DeckBoardsResult{Boards: []DeckBoard{}}
	for _, item := range items {
		if item.ID == "" || item.DeletedAt != 0 || !bound.holds(string(item.ID)) {
			continue
		}
		if len(result.Boards) == maxDeckBoards {
			result.Truncated = true
			break
		}
		board := boardOf(item)
		result.Truncated = result.Truncated || board.Truncated
		result.Boards = append(result.Boards, board)
	}
	result.Count = len(result.Boards)
	return result, nil
}

// GetDeckBoard reads one board with its labels, ACL, and members.
func (c *Client) GetDeckBoard(ctx context.Context, boardID string) (*DeckBoardDetail, error) {
	const op = "get Deck board"
	if err := checkDeckID(op, boardID); err != nil {
		return nil, err
	}
	body, err := c.deckGet(ctx, op, "boards", boardID)
	if err != nil {
		return nil, err
	}
	var raw rawBoard
	if err := json.Unmarshal(body, &raw); err != nil || string(raw.ID) != boardID {
		return nil, invalidResponse(op, "the Nextcloud board could not be read")
	}
	var cut cutter
	detail := &DeckBoardDetail{
		DeckBoard: boardOf(raw), Labels: labelsOf(&cut, raw.Labels), ACL: []DeckACL{}, Members: []DeckPerson{},
	}
	for _, entry := range raw.ACL {
		person := personOf(&cut, entry.Participant, entry.Type)
		if person == nil {
			continue
		}
		if len(detail.ACL) == maxDeckMembers {
			cut.hit = true
			break
		}
		detail.ACL = append(detail.ACL, DeckACL{
			Participant: *person, CanEdit: entry.Edit, CanShare: entry.Share, CanManage: entry.Manage, Owner: entry.Owner,
		})
	}
	for _, member := range raw.Users {
		person := personOf(&cut, member, "")
		if person == nil {
			continue
		}
		if len(detail.Members) == maxDeckMembers {
			cut.hit = true
			break
		}
		detail.Members = append(detail.Members, *person)
	}
	detail.Truncated = detail.Truncated || cut.hit
	return detail, nil
}

// readStacks reads the stacks of one board with their cards, either the active cards or the archived ones.
func (c *Client) readStacks(ctx context.Context, op, boardID string, archived bool) ([]rawStack, error) {
	suffix := []string{"boards", boardID, "stacks"}
	if archived {
		suffix = append(suffix, "archived")
	}
	body, err := c.deckGet(ctx, op, suffix...)
	if err != nil {
		return nil, err
	}
	var stacks []rawStack
	if err := json.Unmarshal(body, &stacks); err != nil {
		return nil, invalidResponse(op, "the Nextcloud stack list could not be read")
	}
	return stacks, nil
}

// ListDeckStacks reads the stacks of one board with their cards. Without archived the active cards are
// listed, with it the archived ones.
func (c *Client) ListDeckStacks(ctx context.Context, boardID string, archived bool) (*DeckStacksResult, error) {
	const op = "list Deck stacks"
	if err := checkDeckID(op, boardID); err != nil {
		return nil, err
	}
	stacks, err := c.readStacks(ctx, op, boardID, archived)
	if err != nil {
		return nil, err
	}
	result := &DeckStacksResult{Stacks: []DeckStack{}}
	for _, raw := range stacks {
		if len(result.Stacks) == maxDeckStacks {
			result.Truncated = true
			break
		}
		var cut cutter
		stack := DeckStack{
			ID: cut.text(string(raw.ID), maxDeckTitleBytes), Title: cut.text(raw.Title, maxDeckTitleBytes),
			Order: raw.Order, Cards: []DeckCard{},
		}
		stack.Truncated = cut.hit
		result.Truncated = result.Truncated || cut.hit
		for _, item := range raw.Cards {
			if result.Cards == maxDeckCards {
				result.Truncated = true
				break
			}
			card := cardOf(item, maxDeckListText)
			result.Truncated = result.Truncated || card.Truncated
			stack.Cards = append(stack.Cards, card)
			result.Cards++
		}
		result.Stacks = append(result.Stacks, stack)
	}
	result.Count = len(result.Stacks)
	return result, nil
}

// holdsCard reports whether the stack of the board answer lists the card.
func holdsCard(stacks []rawStack, stackID, cardID string) (found, stackKnown bool) {
	for _, stack := range stacks {
		if string(stack.ID) != stackID {
			continue
		}
		for _, card := range stack.Cards {
			if string(card.ID) == cardID {
				return true, true
			}
		}
		return false, true
	}
	return false, false
}

// GetDeckCard reads one card. Deck resolves a card by its ID alone and ignores the board and stack of the
// path, so the card is accepted only when the board's own stack list, read first, holds it in that stack.
func (c *Client) GetDeckCard(ctx context.Context, boardID, stackID, cardID string) (*DeckCard, error) {
	const op = "get Deck card"
	for _, id := range []string{boardID, stackID, cardID} {
		if err := checkDeckID(op, id); err != nil {
			return nil, err
		}
	}
	stacks, err := c.readStacks(ctx, op, boardID, false)
	if err != nil {
		return nil, err
	}
	found, stackKnown := holdsCard(stacks, stackID, cardID)
	if !stackKnown {
		return nil, deckNotFound(op)
	}
	if !found {
		archived, err := c.readStacks(ctx, op, boardID, true)
		if err != nil {
			return nil, err
		}
		if found, _ = holdsCard(archived, stackID, cardID); !found {
			return nil, deckNotFound(op)
		}
	}
	body, err := c.deckGet(ctx, op, "boards", boardID, "stacks", stackID, "cards", cardID)
	if err != nil {
		return nil, err
	}
	var raw rawCard
	if err := json.Unmarshal(body, &raw); err != nil || string(raw.ID) != cardID || string(raw.StackID) != stackID {
		return nil, invalidResponse(op, "the Nextcloud card could not be read")
	}
	card := cardOf(raw, maxDeckCardText)
	return &card, nil
}

// holds reports whether the selection covers a board.
func (s selection) holds(id string) bool {
	if s.all {
		return true
	}
	for _, bound := range s.ids {
		if bound == id {
			return true
		}
	}
	return false
}

// deckBound wraps a Deck handler so a connection without a deck target refuses before the handler reads
// any argument, credential, or request, and names no other target.
func deckBound(handler capability.Handler) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
		red *redact.Redactor, raw json.RawMessage) (any, error) {
		if _, err := requireDeck(resolved); err != nil {
			return nil, err
		}
		return handler(ctx, resolved, secrets, red, raw)
	}
}

func requireDeck(resolved *config.Resolved) (selection, error) {
	s, err := scopeOf(resolved)
	if err != nil {
		return selection{}, err
	}
	if !s.decks.bound() {
		return selection{}, &provider.Error{
			Class: provider.ClassPermission, Op: "open", Message: "this connection is not bound to Deck boards",
		}
	}
	return s.decks, nil
}

// openDeck refuses a board the connection does not bind before any credential access or request, then opens
// the client. The refusal does not name the board.
func openDeck(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	boardID string) (*Client, error) {
	bound, err := requireDeck(resolved)
	if err != nil {
		return nil, err
	}
	if err := checkDeckID("open", boardID); err != nil {
		return nil, err
	}
	if !bound.holds(boardID) {
		return nil, &provider.Error{Class: provider.ClassPermission, Op: "open", Message: messageDeckNotBound}
	}
	return open(ctx, resolved, secrets, red, false)
}

type deckArguments struct {
	BoardID  string `json:"board_id"`
	StackID  string `json:"stack_id"`
	CardID   string `json:"card_id"`
	Archived bool   `json:"archived"`
}

func readDeckArguments(op string, raw json.RawMessage) (deckArguments, error) {
	var input deckArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

func invokeDeckBoardsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	bound, err := requireDeck(resolved)
	if err != nil {
		return nil, err
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.ListDeckBoards(ctx, bound)
}

func invokeDeckBoardsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := readDeckArguments("get Deck board", raw)
	if err != nil {
		return nil, err
	}
	client, err := openDeck(ctx, resolved, secrets, red, input.BoardID)
	if err != nil {
		return nil, err
	}
	return client.GetDeckBoard(ctx, input.BoardID)
}

func invokeDeckStacksList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := readDeckArguments("list Deck stacks", raw)
	if err != nil {
		return nil, err
	}
	client, err := openDeck(ctx, resolved, secrets, red, input.BoardID)
	if err != nil {
		return nil, err
	}
	return client.ListDeckStacks(ctx, input.BoardID, input.Archived)
}

func invokeDeckCardsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := readDeckArguments("get Deck card", raw)
	if err != nil {
		return nil, err
	}
	for _, id := range []string{input.StackID, input.CardID} {
		if err := checkDeckID("get Deck card", id); err != nil {
			return nil, err
		}
	}
	client, err := openDeck(ctx, resolved, secrets, red, input.BoardID)
	if err != nil {
		return nil, err
	}
	return client.GetDeckCard(ctx, input.BoardID, input.StackID, input.CardID)
}

const (
	deckIDSchema = `{"type":"string","pattern":"^[0-9]{1,18}$"}`

	deckPersonSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"type":{"type":"string","enum":["user","group","team"]}},"additionalProperties":false}`
	deckLabelSchema = `{"type":"object","properties":{"id":{"type":"string"},"title":{"type":"string"},` +
		`"color":{"type":"string"}},"required":["id","title"],"additionalProperties":false}`
	deckBoardProperties = `"id":{"type":"string"},"title":{"type":"string"},"color":{"type":"string"},` +
		`"archived":{"type":"boolean"},"owner":` + deckPersonSchema + `,"truncated":{"type":"boolean"}`
	deckBoardSchema = `{"type":"object","properties":{` + deckBoardProperties + `},` +
		`"required":["id","title","archived"],"additionalProperties":false}`
	deckBoardDetailSchema = `{"type":"object","properties":{` + deckBoardProperties + `,` +
		`"labels":{"type":"array","items":` + deckLabelSchema + `},` +
		`"acl":{"type":"array","items":{"type":"object","properties":{"participant":` + deckPersonSchema + `,` +
		`"can_edit":{"type":"boolean"},"can_share":{"type":"boolean"},"can_manage":{"type":"boolean"},` +
		`"owner":{"type":"boolean"}},"required":["participant","can_edit","can_share","can_manage","owner"],` +
		`"additionalProperties":false}},` +
		`"members":{"type":"array","items":` + deckPersonSchema + `}},` +
		`"required":["id","title","archived","labels","acl","members"],"additionalProperties":false}`
	deckCardSchema = `{"type":"object","properties":{"id":{"type":"string"},"stack_id":{"type":"string"},` +
		`"title":{"type":"string"},"description":{"type":"string"},"order":{"type":"integer"},` +
		`"archived":{"type":"boolean"},"done":{"type":"string"},"due_date":{"type":"string"},` +
		`"owner":` + deckPersonSchema + `,"labels":{"type":"array","items":` + deckLabelSchema + `},` +
		`"assigned_to":{"type":"array","items":` + deckPersonSchema + `},` +
		`"created_at":{"type":"integer"},"last_modified":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["id","title","order","archived","labels","assigned_to"],"additionalProperties":false}`
	deckStackSchema = `{"type":"object","properties":{"id":{"type":"string"},"title":{"type":"string"},` +
		`"order":{"type":"integer"},"cards":{"type":"array","items":` + deckCardSchema + `},` +
		`"truncated":{"type":"boolean"}},"required":["id","title","order","cards"],"additionalProperties":false}`
)

var deckRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: deckSensitivity,
}

var deckBoardIDArgument = capability.Argument{
	Name: "board_id", Description: "Numeric Deck board ID as reported by deckboards.list; the connection must bind it",
	Required: true,
}

var deckCardFields = []capability.Field{
	{Name: "id", Description: "Card ID"},
	{Name: "stack_id", Description: "ID of the stack that holds the card"},
	{Name: "title", Description: "Card title, untrusted data, cut at 256 bytes"},
	{Name: "description", Description: "Card description, untrusted data, cut at 8 KiB (1 KiB in a stack listing)"},
	{Name: "order", Description: "Position in the stack"},
	{Name: "archived", Description: "True for an archived card"},
	{Name: "done", Description: "Time the card was marked done, when it was"},
	{Name: "due_date", Description: "Due date, when set"},
	{Name: "owner", Description: "Creator of the card, untrusted data"},
	{Name: "labels", Description: "Labels of the card, untrusted data"},
	{Name: "assigned_to", Description: "People the card is assigned to, untrusted data"},
	{Name: "created_at", Description: "Creation time as Unix seconds"},
	{Name: "last_modified", Description: "Last change as Unix seconds"},
	{Name: "truncated", Description: "True when a string of the card was cut"},
}

var deckBoardsList = capability.Descriptor{
	ID: Provider + ".deckboards.list", Version: 1, Title: "List Nextcloud Deck boards",
	Description: "List the Deck boards of an explicit Nextcloud connection: all boards of the identity for a " +
		"deck target, or only the boards a deck/BOARD_ID target binds; deleted boards are omitted",
	Tags:        []string{"nextcloud", "deck", "boards", "list"},
	Risk:        deckRisk,
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"boards":{"type":"array","items":` +
		deckBoardSchema + `},"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["boards","count"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "boards", Description: "Bound boards with ID, title, color, archived flag, and owner, untrusted data"},
		{Name: "count", Description: "Number of reported boards"},
		{Name: "truncated", Description: "True when more than 200 boards matched or a string was cut"},
	},
	Examples: []capability.Example{{Description: "List the bound boards", Arguments: json.RawMessage(`{}`)}},
}

var deckBoardsGet = capability.Descriptor{
	ID: Provider + ".deckboards.get", Version: 1, Title: "Get a Nextcloud Deck board",
	Description: "Read one bound Deck board with its labels, sharing entries, and members; a board the " +
		"connection does not bind is refused locally",
	Tags:     []string{"nextcloud", "deck", "boards", "get", "labels", "members"},
	Risk:     deckRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `},` +
		`"required":["board_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(deckBoardDetailSchema),
	Arguments:    []capability.Argument{deckBoardIDArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Board ID"},
		{Name: "title", Description: "Board title, untrusted data, cut at 256 bytes"},
		{Name: "color", Description: "Board color"},
		{Name: "archived", Description: "True for an archived board"},
		{Name: "owner", Description: "Owner of the board, untrusted data"},
		{Name: "labels", Description: "Labels of the board, untrusted data"},
		{Name: "acl", Description: "Sharing entries: who the board is shared with and their rights, untrusted data"},
		{Name: "members", Description: "People who can work on the board, untrusted data"},
		{Name: "truncated", Description: "True when a string or list was cut"},
	},
	Examples: []capability.Example{{
		Description: "Read one board a listing reported", Arguments: json.RawMessage(`{"board_id":"7"}`),
	}},
}

var deckStacksList = capability.Descriptor{
	ID: Provider + ".deckstacks.list", Version: 1, Title: "List Nextcloud Deck stacks",
	Description: "List the stacks of one bound Deck board with their cards; by default the active cards, with " +
		"archived the archived ones",
	Tags:     []string{"nextcloud", "deck", "stacks", "cards", "list"},
	Risk:     deckRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,` +
		`"archived":{"type":"boolean"}},"required":["board_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"stacks":{"type":"array","items":` +
		deckStackSchema + `},"count":{"type":"integer"},"cards":{"type":"integer"},` +
		`"truncated":{"type":"boolean"}},"required":["stacks","count","cards"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		deckBoardIDArgument,
		{Name: "archived", Description: "List the archived cards instead of the active ones"},
	},
	Fields: []capability.Field{
		{Name: "stacks", Description: "Stacks with ID, title, order, and their cards, untrusted data; at most 100"},
		{Name: "count", Description: "Number of reported stacks"},
		{Name: "cards", Description: "Number of reported cards across all stacks"},
		{Name: "truncated", Description: "True when more than 100 stacks or 500 cards matched, or a string was cut"},
	},
	Examples: []capability.Example{{
		Description: "List the stacks and cards of a board", Arguments: json.RawMessage(`{"board_id":"7"}`),
	}},
}

var deckCardsGet = capability.Descriptor{
	ID: Provider + ".deckcards.get", Version: 1, Title: "Get a Nextcloud Deck card",
	Description: "Read one card of a bound Deck board; the card is accepted only when the stack list of the " +
		"board holds it in the named stack, and any other card is reported as not found",
	Tags:     []string{"nextcloud", "deck", "cards", "get"},
	Risk:     deckRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,` +
		`"stack_id":` + deckIDSchema + `,"card_id":` + deckIDSchema + `},` +
		`"required":["board_id","stack_id","card_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(deckCardSchema),
	Arguments: []capability.Argument{
		deckBoardIDArgument,
		{Name: "stack_id", Description: "Numeric stack ID as reported by deckstacks.list", Required: true},
		{Name: "card_id", Description: "Numeric card ID as reported by deckstacks.list", Required: true},
	},
	Fields: deckCardFields,
	Examples: []capability.Example{{
		Description: "Read one card a stack listing reported", Arguments: json.RawMessage(`{"board_id":"7","stack_id":"3","card_id":"21"}`),
	}},
}
