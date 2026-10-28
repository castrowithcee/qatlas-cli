package nextcloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
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
	maxDeckMentions   = 50
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
	// PrimaryKey is the user ID Deck keys the person by.
	PrimaryKey string `json:"primary_key,omitempty"`
}

// DeckLabel is a board label.
type DeckLabel struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Color   string `json:"color,omitempty"`
	BoardID string `json:"board_id,omitempty"`
	CardID  string `json:"card_id,omitempty"`
}

// DeckPermissions are the rights of the configured identity on a board; a right Deck did not report is left out.
type DeckPermissions struct {
	Read   *bool `json:"read,omitempty"`
	Edit   *bool `json:"edit,omitempty"`
	Manage *bool `json:"manage,omitempty"`
	Share  *bool `json:"share,omitempty"`
}

// DeckBoardSettings are the board settings Deck reports.
type DeckBoardSettings struct {
	NotifyDue string `json:"notify_due,omitempty"`
	Calendar  *bool  `json:"calendar,omitempty"`
}

// DeckBoard is the stable view of one board in a listing.
type DeckBoard struct {
	ID           string             `json:"id"`
	Title        string             `json:"title"`
	Color        string             `json:"color,omitempty"`
	Archived     bool               `json:"archived"`
	Owner        *DeckPerson        `json:"owner,omitempty"`
	Permissions  *DeckPermissions   `json:"permissions,omitempty"`
	Shared       *int64             `json:"shared,omitempty"`
	LastModified int64              `json:"last_modified,omitempty"`
	Settings     *DeckBoardSettings `json:"settings,omitempty"`
	ETag         string             `json:"etag,omitempty"`
	// Truncated is true when a string of this board was cut.
	Truncated bool `json:"truncated,omitempty"`
}

// DeckStackSummary is a stack of a board in a detailed board listing, without its cards.
type DeckStackSummary struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Order int64  `json:"order"`
}

// DeckBoardEntry is one board of a listing; with details it carries what boards.get reports and the stacks.
type DeckBoardEntry struct {
	DeckBoard
	Labels  []DeckLabel        `json:"labels,omitempty"`
	ACL     []DeckACL          `json:"acl,omitempty"`
	Members []DeckPerson       `json:"members,omitempty"`
	Stacks  []DeckStackSummary `json:"stacks,omitempty"`
}

// DeckBoardsResult is the list of the bound boards. Unchanged is true when Deck answered 304 Not Modified.
type DeckBoardsResult struct {
	Boards    []DeckBoardEntry `json:"boards"`
	Count     int              `json:"count"`
	Truncated bool             `json:"truncated,omitempty"`
	Unchanged bool             `json:"unchanged,omitempty"`
	ETag      string           `json:"etag,omitempty"`
}

// DeckACL is one sharing entry of a board.
type DeckACL struct {
	ID          string     `json:"id"`
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
	Truncated       bool             `json:"truncated,omitempty"`
	Type            string           `json:"type,omitempty"`
	AttachmentCount *int64           `json:"attachment_count,omitempty"`
	Attachments     []DeckAttachment `json:"attachments,omitempty"`
	CommentsUnread  *int64           `json:"comments_unread,omitempty"`
	Overdue         *int64           `json:"overdue,omitempty"`
	ETag            string           `json:"etag,omitempty"`
}

// DeckStack is one stack with its cards.
type DeckStack struct {
	ID    string     `json:"id"`
	Title string     `json:"title"`
	Order int64      `json:"order"`
	Cards []DeckCard `json:"cards"`
	// Truncated is true when the title of this stack was cut.
	Truncated    bool   `json:"truncated,omitempty"`
	BoardID      string `json:"board_id,omitempty"`
	LastModified int64  `json:"last_modified,omitempty"`
	ETag         string `json:"etag,omitempty"`
}

// DeckStacksResult is the stacks of one board. Unchanged is true when Deck answered 304 Not Modified.
type DeckStacksResult struct {
	Stacks    []DeckStack `json:"stacks"`
	Count     int         `json:"count"`
	Cards     int         `json:"cards"`
	Truncated bool        `json:"truncated,omitempty"`
	Unchanged bool        `json:"unchanged,omitempty"`
	ETag      string      `json:"etag,omitempty"`
}

// rawPerson reads a participant that Deck writes as an object or, in older versions, as the user ID alone.
type rawPerson struct {
	UID         string      `json:"uid"`
	PrimaryKey  string      `json:"primaryKey"`
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
		Type: participantType(kind), PrimaryKey: c.text(raw.PrimaryKey, maxDeckTitleBytes),
	}
}

type rawLabel struct {
	ID      flexString `json:"id"`
	Title   string     `json:"title"`
	Color   string     `json:"color"`
	BoardID flexString `json:"boardId"`
	CardID  flexString `json:"cardId"`
}

// wholeNumber reads a count Deck may leave out, send as null, or send in another shape; anything that is not a
// whole number counts as absent.
type wholeNumber struct {
	value int64
	set   bool
}

func (n *wholeNumber) UnmarshalJSON(data []byte) error {
	if value, err := json.Number(strings.TrimSpace(string(data))).Int64(); err == nil {
		n.value, n.set = value, true
	}
	return nil
}

func (n wholeNumber) pointer() *int64 {
	if !n.set {
		return nil
	}
	value := n.value
	return &value
}

// decodeObject reads the members of a JSON object that decode; Deck sends an empty PHP array, a JSON list, where an
// object has no members, and that reads as no members.
func decodeObject(data []byte, into any) {
	if strings.HasPrefix(strings.TrimSpace(string(data)), "{") {
		_ = json.Unmarshal(data, into)
	}
}

type rawPermissions struct {
	Read   *bool `json:"PERMISSION_READ"`
	Edit   *bool `json:"PERMISSION_EDIT"`
	Manage *bool `json:"PERMISSION_MANAGE"`
	Share  *bool `json:"PERMISSION_SHARE"`
}

func (p *rawPermissions) UnmarshalJSON(data []byte) error {
	type plain rawPermissions
	decodeObject(data, (*plain)(p))
	return nil
}

type rawBoardSettings struct {
	NotifyDue *string `json:"notify-due"`
	Calendar  *bool   `json:"calendar"`
}

func (s *rawBoardSettings) UnmarshalJSON(data []byte) error {
	type plain rawBoardSettings
	decodeObject(data, (*plain)(s))
	return nil
}

type rawACL struct {
	ID          flexString  `json:"id"`
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
	// Permissions, Shared, LastModified, Settings, and ETag are the board members Deck reports besides the ones above.
	Permissions  rawPermissions   `json:"permissions"`
	Shared       wholeNumber      `json:"shared"`
	LastModified wholeNumber      `json:"lastModified"`
	Settings     rawBoardSettings `json:"settings"`
	ETag         string           `json:"ETag"`
	// Stacks is nil when the board answer carries no stack list, which differs from an empty one.
	Stacks *[]rawStack `json:"stacks"`
}

type rawAssignment struct {
	Participant rawPerson   `json:"participant"`
	Type        json.Number `json:"type"`
}

type rawCard struct {
	ID              flexString      `json:"id"`
	StackID         flexString      `json:"stackId"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	Order           int64           `json:"order"`
	Archived        bool            `json:"archived"`
	Done            *string         `json:"done"`
	DueDate         *string         `json:"duedate"`
	StartDate       *string         `json:"startdate"`
	Type            string          `json:"type"`
	Color           *string         `json:"color"`
	Owner           rawPerson       `json:"owner"`
	Labels          []rawLabel      `json:"labels"`
	Assigned        []rawAssignment `json:"assignedUsers"`
	CreatedAt       int64           `json:"createdAt"`
	LastModified    int64           `json:"lastModified"`
	Attachments     []rawAttachment `json:"attachments"`
	AttachmentCount wholeNumber     `json:"attachmentCount"`
	CommentsUnread  wholeNumber     `json:"commentsUnread"`
	Overdue         wholeNumber     `json:"overdue"`
	ETag            string          `json:"ETag"`
}

type rawStack struct {
	ID           flexString  `json:"id"`
	Title        string      `json:"title"`
	Order        int64       `json:"order"`
	DeletedAt    int64       `json:"deletedAt"`
	Cards        []rawCard   `json:"cards"`
	BoardID      flexString  `json:"boardId"`
	LastModified wholeNumber `json:"lastModified"`
	ETag         string      `json:"ETag"`
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
			Color: c.text(label.Color, maxDeckTitleBytes), BoardID: c.text(string(label.BoardID), maxDeckTitleBytes),
			CardID: c.text(string(label.CardID), maxDeckTitleBytes),
		})
	}
	return labels
}

func boardOf(raw rawBoard) DeckBoard {
	var c cutter
	board := DeckBoard{
		ID: c.text(string(raw.ID), maxDeckTitleBytes), Title: c.text(raw.Title, maxDeckTitleBytes),
		Color: c.text(raw.Color, maxDeckTitleBytes), Archived: raw.Archived, Owner: personOf(&c, raw.Owner, ""),
		Shared: raw.Shared.pointer(), LastModified: raw.LastModified.value, ETag: c.text(raw.ETag, maxDeckTitleBytes),
	}
	if p := raw.Permissions; p.Read != nil || p.Edit != nil || p.Manage != nil || p.Share != nil {
		board.Permissions = &DeckPermissions{Read: p.Read, Edit: p.Edit, Manage: p.Manage, Share: p.Share}
	}
	if s := raw.Settings; s.NotifyDue != nil || s.Calendar != nil {
		board.Settings = &DeckBoardSettings{Calendar: s.Calendar}
		if s.NotifyDue != nil {
			board.Settings.NotifyDue = c.text(*s.NotifyDue, maxDeckTitleBytes)
		}
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
		CreatedAt: raw.CreatedAt, LastModified: raw.LastModified, Type: c.text(raw.Type, maxDeckTitleBytes),
		AttachmentCount: raw.AttachmentCount.pointer(), CommentsUnread: raw.CommentsUnread.pointer(),
		Overdue: raw.Overdue.pointer(), ETag: c.text(raw.ETag, maxDeckTitleBytes),
	}
	for _, attachment := range raw.Attachments {
		if attachment.DeletedAt != 0 || (attachment.CardID != "" && attachment.CardID != raw.ID) {
			continue
		}
		if len(card.Attachments) == maxDeckAttachments {
			c.hit = true
			break
		}
		card.Attachments = append(card.Attachments, attachment.view(&c))
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
	body, _, _, err := c.deckFetch(ctx, op, deckRead{}, suffix...)
	return body, err
}

// deckRead holds the validated optional parts of a Deck list read: an HTTP date for If-Modified-Since, an opaque
// entity tag for If-None-Match, and typed query values. Nothing in it is taken over as a free header or path.
type deckRead struct {
	modifiedSince string
	etag          string
	query         url.Values
}

// newDeckRead validates the filters of a list read before any credential access or request.
func newDeckRead(op, modifiedSince, etag string) (deckRead, error) {
	var read deckRead
	if modifiedSince != "" {
		parsed, err := time.Parse(time.RFC3339, modifiedSince)
		if err != nil || parsed.Year() < 1970 || parsed.Year() > 9999 {
			return read, providerError(op, "modified_since must be an RFC 3339 time")
		}
		read.modifiedSince = parsed.UTC().Format(http.TimeFormat)
	}
	if etag != "" {
		if !validListETag(etag) {
			return read, providerError(op, "the etag is unusable")
		}
		read.etag = etag
	}
	return read, nil
}

func (r deckRead) conditional() bool { return r.modifiedSince != "" || r.etag != "" }

// deckFetch performs one authenticated Deck GET below the fixed REST root. With a precondition a 304 answer is not an
// error: notModified reports it and the body is empty. Without one the answer is read as before.
func (c *Client) deckFetch(ctx context.Context, op string, read deckRead, suffix ...string) (json.RawMessage, http.Header, bool, error) {
	segments := append(append(append([]string{}, c.install...), deckRoot...), suffix...)
	target := c.origin + escapePath(segments)
	if len(read.query) > 0 {
		target += "?" + read.query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, nil, false, providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("Accept", "application/json")
	if read.modifiedSince != "" {
		req.Header.Set("If-Modified-Since", read.modifiedSince)
	}
	if read.etag != "" {
		req.Header.Set("If-None-Match", read.etag)
	}

	response, err := c.http.Do(req)
	if err != nil {
		return nil, nil, false, provider.Transport(op, "Nextcloud", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified && read.conditional() {
		return nil, response.Header, true, nil
	}
	if response.StatusCode == http.StatusNotFound {
		return nil, nil, false, withOriginal(
			&provider.Error{Class: provider.ClassNotFound, Op: op, Message: messageDeckNotFound}, response, deckFormat)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, nil, false, formatStatusError(op, response, deckFormat)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil || len(body) > maxBodyBytes {
		return nil, nil, false, invalidResponse(op, "the Nextcloud response could not be read within the size limit")
	}
	return body, response.Header, false, nil
}

// listETag takes the entity tag of a list answer when it is usable, and the requested one for a 304 without a tag.
func listETag(header http.Header, requested string, notModified bool) string {
	if tag := header.Get("ETag"); validListETag(tag) {
		return tag
	}
	if notModified {
		return requested
	}
	return ""
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
	return c.listDeckBoards(ctx, bound, deckRead{}, false)
}

// listDeckBoards reads the board list, optionally with preconditions and with details. Every board of the answer is
// checked against the bound boards, with or without details.
func (c *Client) listDeckBoards(ctx context.Context, bound selection, read deckRead, details bool) (*DeckBoardsResult, error) {
	const op = "list Deck boards"
	if details {
		read.query = url.Values{"details": {"true"}}
	}
	// A precondition and the entity tag cover every board, so they would show a change of an excluded one. With
	// exclusions the list is read unconditionally, modified_since is applied to the kept boards, and no tag is given.
	var since time.Time
	excluding := len(bound.excluded) > 0
	if excluding {
		since, _ = http.ParseTime(read.modifiedSince)
		read.modifiedSince, read.etag = "", ""
	}
	body, header, notModified, err := c.deckFetch(ctx, op, read, "boards")
	if err != nil {
		return nil, err
	}
	result := &DeckBoardsResult{Boards: []DeckBoardEntry{}, ETag: listETag(header, read.etag, notModified)}
	if excluding {
		result.ETag = ""
	}
	if notModified {
		result.Unchanged = true
		return result, nil
	}
	var items []rawBoard
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, invalidResponse(op, "the Nextcloud board list could not be read")
	}
	for _, item := range items {
		if item.ID == "" || item.DeletedAt != 0 || !bound.holds(string(item.ID)) {
			continue
		}
		if !since.IsZero() && item.LastModified.value <= since.Unix() {
			continue
		}
		if len(result.Boards) == maxDeckBoards {
			result.Truncated = true
			break
		}
		var entry DeckBoardEntry
		if !details {
			entry.DeckBoard = boardOf(item)
		} else {
			detail := boardDetailOf(item)
			entry.DeckBoard = detail.DeckBoard
			entry.Labels, entry.ACL, entry.Members = detail.Labels, detail.ACL, detail.Members
			var cut cutter
			if item.Stacks != nil {
				entry.Stacks = stackSummariesOf(&cut, *item.Stacks)
			}
			entry.Truncated = entry.Truncated || cut.hit
		}
		result.Truncated = result.Truncated || entry.Truncated
		result.Boards = append(result.Boards, entry)
	}
	result.Count = len(result.Boards)
	return result, nil
}

// stackSummariesOf reduces the stacks of a detailed board answer to ID, title, and order; deleted stacks and the
// cards are left out.
func stackSummariesOf(c *cutter, raw []rawStack) []DeckStackSummary {
	var stacks []DeckStackSummary
	for _, stack := range raw {
		if stack.DeletedAt != 0 || stack.ID == "" {
			continue
		}
		if len(stacks) == maxDeckStacks {
			c.hit = true
			break
		}
		stacks = append(stacks, DeckStackSummary{
			ID: c.text(string(stack.ID), maxDeckTitleBytes), Title: c.text(stack.Title, maxDeckTitleBytes), Order: stack.Order,
		})
	}
	return stacks
}

// boardDetailOf converts a board with its labels, ACL, and members.
func boardDetailOf(raw rawBoard) DeckBoardDetail {
	var cut cutter
	detail := DeckBoardDetail{
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
			ID: cut.text(string(entry.ID), maxDeckTitleBytes), Participant: *person,
			CanEdit: entry.Edit, CanShare: entry.Share, CanManage: entry.Manage, Owner: entry.Owner,
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
	return detail
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
	detail := boardDetailOf(raw)
	return &detail, nil
}

// readStacks reads the stacks of one board with their cards, either the active cards or the archived ones.
func (c *Client) readStacks(ctx context.Context, op, boardID string, archived bool) ([]rawStack, error) {
	stacks, _, _, err := c.readStacksWith(ctx, op, boardID, archived, deckRead{})
	return stacks, err
}

// readStacksWith is readStacks with the preconditions of a list read; it also returns the answer headers and whether
// Deck answered 304 Not Modified.
func (c *Client) readStacksWith(ctx context.Context, op, boardID string, archived bool, read deckRead) ([]rawStack, http.Header, bool, error) {
	suffix := []string{"boards", boardID, "stacks"}
	if archived {
		suffix = append(suffix, "archived")
	}
	body, header, notModified, err := c.deckFetch(ctx, op, read, suffix...)
	if err != nil || notModified {
		return nil, header, notModified, err
	}
	var stacks []rawStack
	if err := json.Unmarshal(body, &stacks); err != nil {
		return nil, nil, false, invalidResponse(op, "the Nextcloud stack list could not be read")
	}
	return stacks, header, false, nil
}

// ListDeckStacks reads the stacks of one board with their cards. Without archived the active cards are
// listed, with it the archived ones.
func (c *Client) ListDeckStacks(ctx context.Context, boardID string, archived bool, read deckRead) (*DeckStacksResult, error) {
	const op = "list Deck stacks"
	if err := checkDeckID(op, boardID); err != nil {
		return nil, err
	}
	stacks, header, notModified, err := c.readStacksWith(ctx, op, boardID, archived, read)
	if err != nil {
		return nil, err
	}
	result := &DeckStacksResult{Stacks: []DeckStack{}, ETag: listETag(header, read.etag, notModified), Unchanged: notModified}
	for _, raw := range stacks {
		if len(result.Stacks) == maxDeckStacks {
			result.Truncated = true
			break
		}
		var cut cutter
		stack := DeckStack{
			ID: cut.text(string(raw.ID), maxDeckTitleBytes), Title: cut.text(raw.Title, maxDeckTitleBytes),
			Order: raw.Order, Cards: []DeckCard{}, BoardID: cut.text(string(raw.BoardID), maxDeckTitleBytes),
			LastModified: raw.LastModified.value, ETag: cut.text(raw.ETag, maxDeckTitleBytes),
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
	if s.hides(id) {
		return false
	}
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
	return s.decks.excluding(s.excludes.decks, true), nil
}

// openDeck refuses a board the connection does not bind before any credential access or request, then opens
// the client. The refusal does not name the board.
func openDeck(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	boardID string) (*Client, error) {
	if err := requireBoard(resolved, boardID); err != nil {
		return nil, err
	}
	return open(ctx, resolved, secrets, red, false)
}

// requireBoard is the local part of openDeck: it needs neither credential nor request.
func requireBoard(resolved *config.Resolved, boardID string) error {
	bound, err := requireDeck(resolved)
	if err != nil {
		return err
	}
	if err := checkDeckID("open", boardID); err != nil {
		return err
	}
	if !bound.holds(boardID) {
		return &provider.Error{Class: provider.ClassPermission, Op: "open", Message: messageDeckNotBound}
	}
	return nil
}

type deckArguments struct {
	BoardID  string `json:"board_id"`
	StackID  string `json:"stack_id"`
	CardID   string `json:"card_id"`
	Archived bool   `json:"archived"`
	// Details, ModifiedSince, and ETag are the filters of the list tools.
	Details       bool   `json:"details"`
	ModifiedSince string `json:"modified_since"`
	ETag          string `json:"etag"`
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
	input, err := readDeckArguments("list Deck boards", raw)
	if err != nil {
		return nil, err
	}
	read, err := newDeckRead("list Deck boards", input.ModifiedSince, input.ETag)
	if err != nil {
		return nil, err
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.listDeckBoards(ctx, bound, read, input.Details)
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
	read, err := newDeckRead("list Deck stacks", input.ModifiedSince, input.ETag)
	if err != nil {
		return nil, err
	}
	client, err := openDeck(ctx, resolved, secrets, red, input.BoardID)
	if err != nil {
		return nil, err
	}
	return client.ListDeckStacks(ctx, input.BoardID, input.Archived, read)
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

	// deckModifiedSinceSchema and deckETagSchema are the preconditions of the board and stack lists.
	deckModifiedSinceSchema = `{"type":"string","minLength":20,"maxLength":40}`
	deckETagSchema          = `{"type":"string","minLength":1,"maxLength":128,"pattern":"^[A-Za-z0-9\"/_.:+=-]+$"}`

	deckPersonSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"type":{"type":"string","enum":["user","group","team"]},"primary_key":{"type":"string"}},"additionalProperties":false}`
	deckLabelSchema = `{"type":"object","properties":{"id":{"type":"string"},"title":{"type":"string"},` +
		`"color":{"type":"string"},"board_id":{"type":"string"},"card_id":{"type":"string"}},` +
		`"required":["id","title"],"additionalProperties":false}`
	deckACLSchema = `{"type":"object","properties":{"id":{"type":"string"},"participant":` + deckPersonSchema + `,` +
		`"can_edit":{"type":"boolean"},"can_share":{"type":"boolean"},"can_manage":{"type":"boolean"},` +
		`"owner":{"type":"boolean"}},"required":["id","participant","can_edit","can_share","can_manage","owner"],` +
		`"additionalProperties":false}`
	deckBoardProperties = `"id":{"type":"string"},"title":{"type":"string"},"color":{"type":"string"},` +
		`"archived":{"type":"boolean"},"owner":` + deckPersonSchema + `,` +
		`"permissions":{"type":"object","properties":{"read":{"type":"boolean"},"edit":{"type":"boolean"},` +
		`"manage":{"type":"boolean"},"share":{"type":"boolean"}},"additionalProperties":false},` +
		`"shared":{"type":"integer"},"last_modified":{"type":"integer"},` +
		`"settings":{"type":"object","properties":{"notify_due":{"type":"string"},"calendar":{"type":"boolean"}},` +
		`"additionalProperties":false},"etag":{"type":"string"},"truncated":{"type":"boolean"}`
	deckBoardSchema = `{"type":"object","properties":{` + deckBoardProperties + `},` +
		`"required":["id","title","archived"],"additionalProperties":false}`
	deckBoardEntrySchema = `{"type":"object","properties":{` + deckBoardProperties + `,` +
		`"labels":{"type":"array","items":` + deckLabelSchema + `},` +
		`"acl":{"type":"array","items":` + deckACLSchema + `},` +
		`"members":{"type":"array","items":` + deckPersonSchema + `},` +
		`"stacks":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},` +
		`"title":{"type":"string"},"order":{"type":"integer"}},"required":["id","title","order"],` +
		`"additionalProperties":false}}},"required":["id","title","archived"],"additionalProperties":false}`
	deckBoardDetailSchema = `{"type":"object","properties":{` + deckBoardProperties + `,` +
		`"labels":{"type":"array","items":` + deckLabelSchema + `},` +
		`"acl":{"type":"array","items":` + deckACLSchema + `},` +
		`"members":{"type":"array","items":` + deckPersonSchema + `}},` +
		`"required":["id","title","archived","labels","acl","members"],"additionalProperties":false}`
	deckCardSchema = `{"type":"object","properties":{"id":{"type":"string"},"stack_id":{"type":"string"},` +
		`"title":{"type":"string"},"description":{"type":"string"},"order":{"type":"integer"},` +
		`"archived":{"type":"boolean"},"done":{"type":"string"},"due_date":{"type":"string"},` +
		`"owner":` + deckPersonSchema + `,"labels":{"type":"array","items":` + deckLabelSchema + `},` +
		`"assigned_to":{"type":"array","items":` + deckPersonSchema + `},` +
		`"created_at":{"type":"integer"},"last_modified":{"type":"integer"},"truncated":{"type":"boolean"},` +
		`"type":{"type":"string"},"attachment_count":{"type":"integer"},` +
		`"attachments":{"type":"array","items":` + deckAttachmentSchema + `},` +
		`"comments_unread":{"type":"integer"},"overdue":{"type":"integer"},"etag":{"type":"string"}},` +
		`"required":["id","title","order","archived","labels","assigned_to"],"additionalProperties":false}`
	deckStackSchema = `{"type":"object","properties":{"id":{"type":"string"},"title":{"type":"string"},` +
		`"order":{"type":"integer"},"cards":{"type":"array","items":` + deckCardSchema + `},` +
		`"truncated":{"type":"boolean"},"board_id":{"type":"string"},"last_modified":{"type":"integer"},` +
		`"etag":{"type":"string"}},"required":["id","title","order","cards"],"additionalProperties":false}`
)

var deckRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: deckSensitivity,
}

var deckBoardIDArgument = capability.Argument{
	Name: "board_id", Description: "Numeric Deck board ID as reported by deck.boards.list; the connection must bind it",
	Required: true,
}

// mergeFields appends the fields of more whose names the base does not list yet.
func mergeFields(base []capability.Field, more ...[]capability.Field) []capability.Field {
	seen := map[string]bool{}
	for _, f := range base {
		seen[f.Name] = true
	}
	merged := append([]capability.Field{}, base...)
	for _, group := range more {
		for _, f := range group {
			if !seen[f.Name] {
				seen[f.Name] = true
				merged = append(merged, f)
			}
		}
	}
	return merged
}

// deckPersonFields, deckLabelFields, and deckBoardFields describe the members nested in the board and card results.
var (
	deckPersonFields = []capability.Field{
		{Name: "id", Description: "ID"},
		{Name: "name", Description: "Display name, untrusted data"},
		{Name: "type", Description: "Kind: user, group, or team for a person; the kind of the object elsewhere"},
		{Name: "primary_key", Description: "User ID Deck keys the person by"},
	}
	deckLabelFields = []capability.Field{
		{Name: "title", Description: "Title, untrusted data"},
		{Name: "color", Description: "Color"},
		{Name: "board_id", Description: "Board of a label"},
		{Name: "card_id", Description: "Card of a label, or of a comment"},
	}
	deckBoardFields = []capability.Field{
		{Name: "id", Description: "Board ID"},
		{Name: "title", Description: "Board title, untrusted data, cut at 256 bytes"},
		{Name: "color", Description: "Board color"},
		{Name: "archived", Description: "True for an archived board"},
		{Name: "owner", Description: "Owner of the board, untrusted data"},
		{Name: "permissions", Description: "Rights of the configured identity on the board: read, edit, manage, share"},
		{Name: "read", Description: "True when the identity may read the board"},
		{Name: "edit", Description: "True when the identity may edit the board"},
		{Name: "manage", Description: "True when the identity may manage the board"},
		{Name: "share", Description: "True when the identity may share the board"},
		{Name: "shared", Description: "Shared flag as Deck reports it, a whole number"},
		{Name: "last_modified", Description: "Last change as Unix seconds"},
		{Name: "settings", Description: "Board settings: notify_due and calendar"},
		{Name: "notify_due", Description: "Due date notification setting of the board"},
		{Name: "calendar", Description: "True when the board is shown in the calendar"},
		{Name: "etag", Description: "Entity tag of the board, or of the list; changes when a child changes"},
		{Name: "truncated", Description: "True when a string or list was cut"},
	}
	deckBoardDetailFields = []capability.Field{
		{Name: "labels", Description: "Labels of the board, untrusted data"},
		{Name: "acl", Description: "Sharing entries: who the board is shared with and their rights, untrusted data"},
		{Name: "participant", Description: "Person, group, or team a board is shared with"},
		{Name: "can_edit", Description: "True when the sharing entry may edit"},
		{Name: "can_share", Description: "True when the sharing entry may share"},
		{Name: "can_manage", Description: "True when the sharing entry may manage"},
		{Name: "members", Description: "People who can work on the board, untrusted data"},
	}
)

var deckCardFields = mergeFields([]capability.Field{
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
	{Name: "type", Description: "Card type as Deck reports it, untrusted data"},
	{Name: "attachment_count", Description: "Number of attachments as Deck reports it"},
	{Name: "attachments", Description: "Attachments of the card in the form of deck.attachments.list, at most 100, untrusted data"},
	{Name: "comments_unread", Description: "Number of comments the identity has not read"},
	{Name: "overdue", Description: "Overdue state as Deck reports it, a whole number"},
	{Name: "etag", Description: "Entity tag of the card, or of the list; changes when a child changes"},
}, deckPersonFields, deckLabelFields, deckAttachmentFields)

var deckBoardsList = capability.Descriptor{
	ID: Provider + ".deck.boards.list", Version: 1, Title: "List Nextcloud Deck boards",
	Description: "List the Deck boards of an explicit Nextcloud connection: all boards of the identity for a " +
		"deck target, or only the boards a deck/BOARD_ID target binds; deleted boards are omitted. modified_since " +
		"limits the list to boards changed since a time, etag to boards changed since a previous list, and details " +
		"adds labels, members, sharing entries, and stack names to each board; an unchanged list is reported as unchanged",
	Tags:     []string{"nextcloud", "deck", "boards", "list"},
	Risk:     deckRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"modified_since":` + deckModifiedSinceSchema + `,` +
		`"etag":` + deckETagSchema + `,"details":{"type":"boolean"}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"boards":{"type":"array","items":` +
		deckBoardEntrySchema + `},"count":{"type":"integer"},"truncated":{"type":"boolean"},` +
		`"unchanged":{"type":"boolean"},"etag":{"type":"string"}},` +
		`"required":["boards","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "modified_since", Description: "RFC 3339 time; only boards changed since then are listed"},
		{Name: "etag", Description: "etag of a previous list; an unchanged list is reported as unchanged"},
		{Name: "details", Description: "Add labels, members, acl, and stacks (id, title, order) to each board"},
	},
	Fields: mergeFields([]capability.Field{
		{Name: "boards", Description: "Bound boards, untrusted data, at most 200"},
		{Name: "count", Description: "Number of reported boards"},
		{Name: "truncated", Description: "True when more than 200 boards matched or a string was cut"},
		{Name: "unchanged", Description: "True when Deck reported the list as not modified"},
		{Name: "stacks", Description: "Stacks of a board with id, title, and order, only with details"},
		{Name: "order", Description: "Position of a stack"},
	}, deckBoardFields, deckBoardDetailFields, deckPersonFields, deckLabelFields),
	Examples: []capability.Example{{Description: "List the bound boards", Arguments: json.RawMessage(`{}`)}},
}

var deckBoardsGet = capability.Descriptor{
	ID: Provider + ".deck.boards.get", Version: 1, Title: "Get a Nextcloud Deck board",
	Description: "Read one bound Deck board with its labels, sharing entries, and members; a board the " +
		"connection does not bind is refused locally",
	Tags:     []string{"nextcloud", "deck", "boards", "get", "labels", "members"},
	Risk:     deckRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `},` +
		`"required":["board_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(deckBoardDetailSchema),
	Arguments:    []capability.Argument{deckBoardIDArgument},
	Fields:       mergeFields(deckBoardFields, deckBoardDetailFields, deckPersonFields, deckLabelFields),
	Examples: []capability.Example{{
		Description: "Read one board a listing reported", Arguments: json.RawMessage(`{"board_id":"7"}`),
	}},
}

var deckStacksList = capability.Descriptor{
	ID: Provider + ".deck.stacks.list", Version: 1, Title: "List Nextcloud Deck stacks",
	Description: "List the stacks of one bound Deck board with their cards; by default the active cards, with " +
		"archived the archived ones. modified_since limits the list to stacks changed since a time, etag to stacks " +
		"changed since a previous list; an unchanged list is reported as unchanged",
	Tags:     []string{"nextcloud", "deck", "stacks", "cards", "list"},
	Risk:     deckRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":` + deckIDSchema + `,` +
		`"archived":{"type":"boolean"},"modified_since":` + deckModifiedSinceSchema + `,"etag":` + deckETagSchema + `},` +
		`"required":["board_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"stacks":{"type":"array","items":` +
		deckStackSchema + `},"count":{"type":"integer"},"cards":{"type":"integer"},` +
		`"truncated":{"type":"boolean"},"unchanged":{"type":"boolean"},"etag":{"type":"string"}},` +
		`"required":["stacks","count","cards"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		deckBoardIDArgument,
		{Name: "archived", Description: "List the archived cards instead of the active ones"},
		{Name: "modified_since", Description: "RFC 3339 time; only stacks changed since then are listed"},
		{Name: "etag", Description: "etag of a previous list; an unchanged list is reported as unchanged"},
	},
	Fields: mergeFields([]capability.Field{
		{Name: "stacks", Description: "Stacks with ID, title, order, and their cards, untrusted data; at most 100"},
		{Name: "count", Description: "Number of reported stacks"},
		{Name: "cards", Description: "Number of reported cards across all stacks"},
		{Name: "truncated", Description: "True when more than 100 stacks or 500 cards matched, or a string was cut"},
		{Name: "unchanged", Description: "True when Deck reported the list as not modified"},
		{Name: "board_id", Description: "Board of a stack"},
	}, deckCardFields),
	Examples: []capability.Example{{
		Description: "List the stacks and cards of a board", Arguments: json.RawMessage(`{"board_id":"7"}`),
	}},
}

var deckCardsGet = capability.Descriptor{
	ID: Provider + ".deck.cards.get", Version: 1, Title: "Get a Nextcloud Deck card",
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
		{Name: "stack_id", Description: "Numeric stack ID as reported by deck.stacks.list", Required: true},
		{Name: "card_id", Description: "Numeric card ID as reported by deck.stacks.list", Required: true},
	},
	Fields: deckCardFields,
	Examples: []capability.Example{{
		Description: "Read one card a stack listing reported", Arguments: json.RawMessage(`{"board_id":"7","stack_id":"3","card_id":"21"}`),
	}},
}
