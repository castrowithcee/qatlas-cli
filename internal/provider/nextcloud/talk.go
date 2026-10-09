package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// groupTalk is the tool group of the Talk tools.
const groupTalk = "talk"

// Conversations, participants, and messages are personal communication, a class of their own.
const talkSensitivity = "nextcloud-talk"

// Bounds of the Talk reads.
const (
	maxRooms           = 500
	maxParticipants    = 500
	maxMessagesPerPage = 100
	defaultMessages    = 50
	maxMessageText     = 4 << 10
	maxDescription     = 2 << 10
	maxMessageParams   = 50
	maxCursorLength    = 18
)

// The Talk app answers below its own fixed segments. Conversations and participants are version 4 of their
// API, the chat is version 1.
var (
	ocsTalkRooms = []string{"apps", "spreed", "api", "v4", "room"}
	ocsTalkChat  = []string{"apps", "spreed", "api", "v1", "chat"}
)

// Features of the spreed capability that the reads depend on; they are checked instead of a version.
const (
	featureConversations = "conversation-v4"
	featureChat          = "chat-v2"
)

const (
	messageTalkMissing  = "the Talk app (spreed) is not available on this Nextcloud instance"
	messageTalkFeature  = "this Nextcloud Talk version does not offer a feature required by this operation"
	messageTalkNotBound = "this connection is not bound to Talk conversations"
	messageTalkForeign  = "this connection does not bind this Talk conversation"
	messageTalkNoRoom   = "this Talk conversation does not exist or is not accessible to this identity"
)

var roomTypes = map[int64]string{
	1: "one-to-one", 2: "group", 3: "public", 4: "changelog", 5: "former-one-to-one", 6: "note-to-self",
}

var participantTypes = map[int64]string{
	1: "owner", 2: "moderator", 3: "user", 4: "guest", 5: "user-self-joined", 6: "guest-moderator",
}

func nameOf(names map[int64]string, raw json.Number) string {
	value, err := raw.Int64()
	if err == nil {
		if name, ok := names[value]; ok {
			return name
		}
	}
	return "other"
}

// Room is the stable Qatlas view of one conversation. It carries no avatar, no federation address, and no
// last message.
type Room struct {
	Token           string `json:"token"`
	Name            string `json:"name,omitempty"`
	DisplayName     string `json:"display_name,omitempty"`
	Description     string `json:"description,omitempty"`
	Type            string `json:"type"`
	ReadOnly        bool   `json:"read_only"`
	HasPassword     bool   `json:"has_password"`
	ParticipantType string `json:"participant_type"`
	UnreadMessages  int64  `json:"unread_messages"`
	UnreadMention   bool   `json:"unread_mention"`
	LastActivity    string `json:"last_activity,omitempty"`
}

// RoomsResult is the list of bound conversations.
type RoomsResult struct {
	Rooms     []Room `json:"rooms"`
	Count     int    `json:"count"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Participant is one member of a conversation.
type Participant struct {
	ActorType       string `json:"actor_type"`
	ActorID         string `json:"actor_id"`
	DisplayName     string `json:"display_name,omitempty"`
	ParticipantType string `json:"participant_type"`
	InCall          bool   `json:"in_call"`
}

// ParticipantsResult is the list of the participants of one bound conversation.
type ParticipantsResult struct {
	Participants []Participant `json:"participants"`
	Count        int           `json:"count"`
	Truncated    bool          `json:"truncated,omitempty"`
}

// MessageObject is a rich object a message refers to. It never carries a link, path, preview, or size.
type MessageObject struct {
	Key  string `json:"key"`
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// Message is one chat message with its placeholders resolved to names.
type Message struct {
	ID            string          `json:"id"`
	Time          string          `json:"time,omitempty"`
	ActorType     string          `json:"actor_type,omitempty"`
	ActorID       string          `json:"actor_id,omitempty"`
	ActorName     string          `json:"actor_name,omitempty"`
	Kind          string          `json:"kind"`
	SystemMessage string          `json:"system_message,omitempty"`
	Text          string          `json:"text"`
	Objects       []MessageObject `json:"objects,omitempty"`
	ParentID      string          `json:"parent_id,omitempty"`
	ReferenceID   string          `json:"reference_id,omitempty"`
	Truncated     bool            `json:"truncated,omitempty"`
}

// MessagesResult is one page of a conversation history, newest first. NextCursor continues with the older
// messages and is absent on the last page.
type MessagesResult struct {
	Messages   []Message `json:"messages"`
	Count      int       `json:"count"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

type rawRoom struct {
	Token           string      `json:"token"`
	Name            string      `json:"name"`
	DisplayName     string      `json:"displayName"`
	Description     string      `json:"description"`
	Type            json.Number `json:"type"`
	ReadOnly        json.Number `json:"readOnly"`
	HasPassword     bool        `json:"hasPassword"`
	ParticipantType json.Number `json:"participantType"`
	UnreadMessages  json.Number `json:"unreadMessages"`
	UnreadMention   bool        `json:"unreadMention"`
	LastActivity    json.Number `json:"lastActivity"`
}

type rawParticipant struct {
	ActorType       string      `json:"actorType"`
	ActorID         string      `json:"actorId"`
	DisplayName     string      `json:"displayName"`
	ParticipantType json.Number `json:"participantType"`
	InCall          json.Number `json:"inCall"`
}

type rawParameter struct {
	Type string     `json:"type"`
	ID   flexString `json:"id"`
	Name string     `json:"name"`
}

type rawMessage struct {
	ID                json.Number     `json:"id"`
	Timestamp         json.Number     `json:"timestamp"`
	ActorType         string          `json:"actorType"`
	ActorID           string          `json:"actorId"`
	ActorDisplayName  string          `json:"actorDisplayName"`
	MessageType       string          `json:"messageType"`
	SystemMessage     string          `json:"systemMessage"`
	Message           string          `json:"message"`
	ReferenceID       string          `json:"referenceId"`
	MessageParameters json.RawMessage `json:"messageParameters"`
	Parent            *struct {
		ID json.Number `json:"id"`
	} `json:"parent"`
}

// cut shortens a provider string to at most limit bytes without splitting a character.
func cut(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	end := limit
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end], true
}

func unixTime(raw json.Number) string {
	seconds, err := raw.Int64()
	if err != nil || seconds <= 0 {
		return ""
	}
	return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
}

func validTalkToken(token string) bool {
	if len(token) < 4 || len(token) > 32 {
		return false
	}
	for i := 0; i < len(token); i++ {
		c := token[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

// covers reports whether the selection binds the conversation.
func (s selection) covers(token string) bool {
	if s.all {
		return true
	}
	for _, id := range s.ids {
		if id == token {
			return true
		}
	}
	return false
}

// requireTalk refuses a connection without a talk target locally, before any credential access or request.
func requireTalk(resolved *config.Resolved) (selection, error) {
	s, err := scopeOf(resolved)
	if err != nil {
		return selection{}, err
	}
	if !s.talks.bound() {
		return selection{}, &provider.Error{Class: provider.ClassPermission, Op: "open", Message: messageTalkNotBound}
	}
	return s.talks, nil
}

// requireTalkToken additionally refuses a conversation the connection does not bind. The answer is the same
// for a token that exists and one that does not, and never repeats the token.
func requireTalkToken(resolved *config.Resolved, token string) error {
	talks, err := requireTalk(resolved)
	if err != nil {
		return err
	}
	if !validTalkToken(token) || !talks.covers(token) {
		return &provider.Error{Class: provider.ClassPermission, Op: "open", Message: messageTalkForeign}
	}
	return nil
}

// talkFeature checks the spreed capability of the instance for every named feature with one read.
func (c *Client) talkFeature(ctx context.Context, op string, features ...string) error {
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsCloud, suffix: []string{"capabilities"}})
	if err != nil {
		return err
	}
	var document struct {
		Capabilities struct {
			Spreed *struct {
				Features []string `json:"features"`
			} `json:"spreed"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return invalidResponse(op, "the Nextcloud capabilities could not be read")
	}
	if document.Capabilities.Spreed == nil {
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: messageTalkMissing}
	}
	have := map[string]bool{}
	for _, feature := range document.Capabilities.Spreed.Features {
		have[feature] = true
	}
	for _, feature := range features {
		if !have[feature] {
			return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: messageTalkFeature}
		}
	}
	return nil
}

// talkNotFound replaces the generic path message of a 404 with one that fits Talk.
func talkNotFound(op string, err error, message string) error {
	var providerErr *provider.Error
	if errors.As(err, &providerErr) && providerErr.Message == messageNotFound {
		return &provider.Error{Class: provider.ClassNotFound, Op: op, Message: message}
	}
	return err
}

func roomOf(raw rawRoom) Room {
	description, _ := cut(raw.Description, maxDescription)
	read, _ := raw.ReadOnly.Int64()
	unread, _ := raw.UnreadMessages.Int64()
	return Room{
		Token: raw.Token, Name: bounded(raw.Name), DisplayName: bounded(raw.DisplayName), Description: description,
		Type: nameOf(roomTypes, raw.Type), ReadOnly: read == 1, HasPassword: raw.HasPassword,
		ParticipantType: nameOf(participantTypes, raw.ParticipantType), UnreadMessages: unread,
		UnreadMention: raw.UnreadMention, LastActivity: unixTime(raw.LastActivity),
	}
}

// ListRooms reads the conversations of the identity and keeps those the connection binds.
func (c *Client) ListRooms(ctx context.Context, talks selection) (*RoomsResult, error) {
	const op = "list talk conversations"
	if err := c.talkFeature(ctx, op, featureConversations); err != nil {
		return nil, err
	}
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsTalkRooms, query: url.Values{"noStatusUpdate": {"1"}}})
	if err != nil {
		return nil, talkNotFound(op, err, messageTalkMissing)
	}
	var items []rawRoom
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, invalidResponse(op, "the Nextcloud conversation list could not be read")
	}
	result := &RoomsResult{Rooms: []Room{}}
	for _, item := range items {
		if !validTalkToken(item.Token) || !talks.covers(item.Token) {
			continue
		}
		if len(result.Rooms) == maxRooms {
			result.Truncated = true
			break
		}
		result.Rooms = append(result.Rooms, roomOf(item))
	}
	result.Count = len(result.Rooms)
	return result, nil
}

// GetRoom reads one conversation; the caller has checked that the connection binds the token.
func (c *Client) GetRoom(ctx context.Context, token string) (*Room, error) {
	const op = "get talk conversation"
	if !validTalkToken(token) {
		return nil, providerError(op, "a conversation token is unusable")
	}
	if err := c.talkFeature(ctx, op, featureConversations); err != nil {
		return nil, err
	}
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsTalkRooms, suffix: []string{token},
		query: url.Values{"noStatusUpdate": {"1"}}})
	if err != nil {
		return nil, talkNotFound(op, err, messageTalkNoRoom)
	}
	var raw rawRoom
	if err := json.Unmarshal(data, &raw); err != nil || raw.Token != token {
		return nil, invalidResponse(op, "the Nextcloud conversation could not be read")
	}
	room := roomOf(raw)
	return &room, nil
}

// ListParticipants reads the participants of one conversation the caller has checked.
func (c *Client) ListParticipants(ctx context.Context, token string) (*ParticipantsResult, error) {
	const op = "list talk participants"
	if !validTalkToken(token) {
		return nil, providerError(op, "a conversation token is unusable")
	}
	if err := c.talkFeature(ctx, op, featureConversations); err != nil {
		return nil, err
	}
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsTalkRooms, suffix: []string{token, "participants"},
		query: url.Values{"includeStatus": {"false"}}})
	if err != nil {
		return nil, talkNotFound(op, err, messageTalkNoRoom)
	}
	var items []rawParticipant
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, invalidResponse(op, "the Nextcloud participant list could not be read")
	}
	result := &ParticipantsResult{Participants: []Participant{}}
	for _, item := range items {
		if len(result.Participants) == maxParticipants {
			result.Truncated = true
			break
		}
		call, _ := item.InCall.Int64()
		result.Participants = append(result.Participants, Participant{
			ActorType: bounded(item.ActorType), ActorID: bounded(item.ActorID), DisplayName: bounded(item.DisplayName),
			ParticipantType: nameOf(participantTypes, item.ParticipantType), InCall: call != 0,
		})
	}
	result.Count = len(result.Participants)
	return result, nil
}

// ListMessages reads one page of the history of a conversation the caller has checked. The request never
// waits for new messages and never moves the read marker or marks notifications as read.
func (c *Client) ListMessages(ctx context.Context, token string, limit int, cursor string) (*MessagesResult, error) {
	const op = "list talk messages"
	if !validTalkToken(token) {
		return nil, providerError(op, "a conversation token is unusable")
	}
	if limit < 1 || limit > maxMessagesPerPage || cursor != "" && (len(cursor) > maxCursorLength || !digitsOnly(cursor)) {
		return nil, providerError(op, "the message arguments are unusable")
	}
	if err := c.talkFeature(ctx, op, featureChat); err != nil {
		return nil, err
	}
	query := url.Values{
		"lookIntoFuture": {"0"}, "limit": {strconv.Itoa(limit)}, "setReadMarker": {"0"},
		"markNotificationsAsRead": {"0"}, "noStatusUpdate": {"1"}, "includeLastKnown": {"0"},
	}
	if cursor != "" {
		query.Set("lastKnownMessageId", cursor)
	}
	data, header, err := c.ocsGetHeader(ctx, op, ocsRequest{app: ocsTalkChat, suffix: []string{token}, query: query,
		emptyOnNotModified: true})
	if err != nil {
		return nil, talkNotFound(op, err, messageTalkNoRoom)
	}
	result := &MessagesResult{Messages: []Message{}}
	if data == nil {
		return result, nil
	}
	var items []rawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, invalidResponse(op, "the Nextcloud message list could not be read")
	}
	more := len(items) >= limit
	if len(items) > limit {
		items = items[:limit]
	}
	for _, item := range items {
		result.Messages = append(result.Messages, messageOf(item))
	}
	result.Count = len(result.Messages)
	if more && result.Count > 0 {
		next := strings.TrimSpace(header.Get("X-Chat-Last-Given"))
		if next == "" || len(next) > maxCursorLength || !digitsOnly(next) {
			next = result.Messages[result.Count-1].ID
		}
		result.NextCursor = next
	}
	return result, nil
}

// messageOf builds the view of one message. Placeholders such as {actor} or {file} are replaced by the name
// of the rich object; its link, path, preview, and size are never read, because they may carry an access
// token.
func messageOf(raw rawMessage) Message {
	var parameters map[string]rawParameter
	// An empty parameter set is an empty JSON array, which is not an object; it simply has no parameters.
	_ = json.Unmarshal(raw.MessageParameters, &parameters)
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > maxMessageParams {
		keys = keys[:maxMessageParams]
	}
	var objects []MessageObject
	var pairs []string
	for _, key := range keys {
		parameter := parameters[key]
		name, _ := cut(parameter.Name, maxValueLength)
		object := MessageObject{Key: bounded(key), Type: bounded(parameter.Type), Name: name}
		switch parameter.Type {
		case "user", "group", "guest":
			object.ID = bounded(string(parameter.ID))
		}
		objects = append(objects, object)
		replacement := name
		switch parameter.Type {
		case "user", "group", "guest", "call":
			replacement = "@" + name
		}
		if name == "" {
			replacement = "{" + key + "}"
		}
		pairs = append(pairs, "{"+key+"}", replacement)
	}
	text := raw.Message
	if len(pairs) > 0 {
		text = strings.NewReplacer(pairs...).Replace(text)
	}
	text, truncated := cut(text, maxMessageText)
	message := Message{
		ID: raw.ID.String(), Time: unixTime(raw.Timestamp), ActorType: bounded(raw.ActorType),
		ActorID: bounded(raw.ActorID), ActorName: bounded(raw.ActorDisplayName), Kind: bounded(raw.MessageType),
		SystemMessage: bounded(raw.SystemMessage), ReferenceID: bounded(raw.ReferenceID), Text: text, Objects: objects, Truncated: truncated,
	}
	if raw.Parent != nil && raw.Parent.ID.String() != "" {
		message.ParentID = raw.Parent.ID.String()
	}
	return message
}

const (
	talkTokenSchema = `{"type":"string","pattern":"^[A-Za-z0-9]{4,32}$"}`
	roomSchema      = `{"type":"object","properties":{"token":{"type":"string"},"name":{"type":"string"},` +
		`"display_name":{"type":"string"},"description":{"type":"string"},` +
		`"type":{"type":"string","enum":["one-to-one","group","public","changelog","former-one-to-one","note-to-self","other"]},` +
		`"read_only":{"type":"boolean"},"has_password":{"type":"boolean"},"participant_type":{"type":"string"},` +
		`"unread_messages":{"type":"integer"},"unread_mention":{"type":"boolean"},"last_activity":{"type":"string"}},` +
		`"required":["token","type","read_only","has_password","participant_type","unread_messages","unread_mention"],` +
		`"additionalProperties":false}`
	participantSchema = `{"type":"object","properties":{"actor_type":{"type":"string"},"actor_id":{"type":"string"},` +
		`"display_name":{"type":"string"},"participant_type":{"type":"string"},"in_call":{"type":"boolean"}},` +
		`"required":["actor_type","actor_id","participant_type","in_call"],"additionalProperties":false}`
	messageSchema = `{"type":"object","properties":{"id":{"type":"string"},"time":{"type":"string"},` +
		`"actor_type":{"type":"string"},"actor_id":{"type":"string"},"actor_name":{"type":"string"},` +
		`"kind":{"type":"string"},"system_message":{"type":"string"},"text":{"type":"string"},` +
		`"objects":{"type":"array","items":{"type":"object","properties":{"key":{"type":"string"},` +
		`"type":{"type":"string"},"id":{"type":"string"},"name":{"type":"string"}},` +
		`"required":["key","type"],"additionalProperties":false}},` +
		`"parent_id":{"type":"string"},"reference_id":{"type":"string"},"truncated":{"type":"boolean"}},` +
		`"required":["id","kind","text"],"additionalProperties":false}`
)

var talkRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: talkSensitivity,
}

var talkTokenArgument = capability.Argument{Name: "token", Required: true,
	Description: "Token of a conversation this connection binds, as reported by talkrooms.list"}

var roomFields = []capability.Field{
	{Name: "token", Description: "Token that identifies the conversation"},
	{Name: "name", Description: "Name of the conversation, untrusted data"},
	{Name: "display_name", Description: "Name as shown to the identity, untrusted data"},
	{Name: "description", Description: "Description, untrusted data, cut at 2 KiB"},
	{Name: "type", Description: "one-to-one, group, public, changelog, former-one-to-one, note-to-self, or other"},
	{Name: "read_only", Description: "True when the conversation accepts no new messages"},
	{Name: "has_password", Description: "True when a public conversation is protected by a password"},
	{Name: "participant_type", Description: "Role of the identity: owner, moderator, user, guest, user-self-joined, guest-moderator, or other"},
	{Name: "unread_messages", Description: "Number of unread messages"},
	{Name: "unread_mention", Description: "True when an unread message mentions the identity"},
	{Name: "last_activity", Description: "Time of the last activity in RFC 3339 UTC"},
}

func talkDescriptor(name, title, description string, input, output string, args []capability.Argument,
	fields []capability.Field, example capability.Example) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + "." + name, Version: 1, Title: title, Description: description,
		Tags: []string{"nextcloud", "talk", "chat", "read", "ocs"}, Risk: talkRisk, Provider: Provider,
		InputSchema: json.RawMessage(input), OutputSchema: json.RawMessage(output), Arguments: args,
		Fields: fields, Examples: []capability.Example{example},
	}
}

var talkRoomsList = talkDescriptor("talkrooms.list", "List Nextcloud Talk conversations",
	"List the Talk conversations of the identity that a talk target of the connection binds; a conversation "+
		"the connection does not bind is never reported",
	`{"type":"object","properties":{},"additionalProperties":false}`,
	`{"type":"object","properties":{"rooms":{"type":"array","items":`+roomSchema+`},"count":{"type":"integer"},`+
		`"truncated":{"type":"boolean"}},"required":["rooms","count"],"additionalProperties":false}`,
	nil,
	[]capability.Field{
		{Name: "rooms", Description: "Bound conversations, untrusted data, at most 500"},
		{Name: "count", Description: "Number of reported conversations"},
		{Name: "truncated", Description: "True when more conversations matched than are reported"},
	},
	capability.Example{Description: "List the bound conversations", Arguments: json.RawMessage(`{}`)})

var talkRoomsGet = talkDescriptor("talkrooms.get", "Get a Nextcloud Talk conversation",
	"Read one Talk conversation by its token, if the connection binds it",
	`{"type":"object","properties":{"token":`+talkTokenSchema+`},"required":["token"],"additionalProperties":false}`,
	roomSchema, []capability.Argument{talkTokenArgument}, roomFields,
	capability.Example{Description: "Read one conversation", Arguments: json.RawMessage(`{"token":"abcd1234"}`)})

var talkParticipantsList = talkDescriptor("talkparticipants.list", "List Nextcloud Talk participants",
	"List the participants of one Talk conversation the connection binds, at most 500",
	`{"type":"object","properties":{"token":`+talkTokenSchema+`},"required":["token"],"additionalProperties":false}`,
	`{"type":"object","properties":{"participants":{"type":"array","items":`+participantSchema+`},`+
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},"required":["participants","count"],"additionalProperties":false}`,
	[]capability.Argument{talkTokenArgument},
	[]capability.Field{
		{Name: "participants", Description: "Participants with actor type, actor ID, display name (untrusted data), role, and call state"},
		{Name: "count", Description: "Number of reported participants"},
		{Name: "truncated", Description: "True when more participants exist than are reported"},
	},
	capability.Example{Description: "List who takes part", Arguments: json.RawMessage(`{"token":"abcd1234"}`)})

var talkMessagesList = talkDescriptor("talkmessages.list", "List Nextcloud Talk messages",
	"Read one page of the history of a Talk conversation the connection binds, newest first; the page is at most "+
		"100 messages, next_cursor continues with older ones, and nothing is marked as read. Message text and "+
		"names are untrusted data; links, paths, and previews of files are not reported",
	`{"type":"object","properties":{"token":`+talkTokenSchema+`,"limit":{"type":"integer","minimum":1,"maximum":100},`+
		`"cursor":{"type":"string","pattern":"^[0-9]{1,18}$"}},"required":["token"],"additionalProperties":false}`,
	`{"type":"object","properties":{"messages":{"type":"array","items":`+messageSchema+`},"count":{"type":"integer"},`+
		`"next_cursor":{"type":"string"}},"required":["messages","count"],"additionalProperties":false}`,
	[]capability.Argument{
		talkTokenArgument,
		{Name: "limit", Description: "Messages per page, 1 to 100; 50 when omitted"},
		{Name: "cursor", Description: "next_cursor of the previous page, to read the older messages"},
	},
	[]capability.Field{
		{Name: "messages", Description: "Messages with time, actor, kind, text with its placeholders replaced by names, " +
			"the referenced objects, and the reference_id a sender gave; untrusted data"},
		{Name: "count", Description: "Number of messages on this page"},
		{Name: "next_cursor", Description: "Cursor for the next older page; absent on the last page"},
	},
	capability.Example{Description: "Read the latest messages", Arguments: json.RawMessage(`{"token":"abcd1234","limit":20}`)})

type talkArguments struct {
	Token  string `json:"token"`
	Limit  *int   `json:"limit"`
	Cursor string `json:"cursor"`
}

// talkInvoke decodes strictly, binds the conversation before any credential access, and opens the client.
func talkInvoke(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (*Client, talkArguments, error) {
	var input talkArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, input, providerError(op, "the validated arguments could not be read")
	}
	if err := requireTalkToken(resolved, input.Token); err != nil {
		return nil, input, err
	}
	client, err := open(ctx, resolved, secrets, red, false)
	return client, input, err
}

func invokeTalkRoomsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list talk conversations"
	if len(bytes.TrimSpace(raw)) > 0 {
		var none struct{}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&none); err != nil {
			return nil, providerError(op, "the validated arguments could not be read")
		}
	}
	talks, err := requireTalk(resolved)
	if err != nil {
		return nil, err
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.ListRooms(ctx, talks)
}

func invokeTalkRoomsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, input, err := talkInvoke(ctx, "get talk conversation", resolved, secrets, red, raw)
	if err != nil {
		return nil, err
	}
	return client.GetRoom(ctx, input.Token)
}

func invokeTalkParticipantsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, input, err := talkInvoke(ctx, "list talk participants", resolved, secrets, red, raw)
	if err != nil {
		return nil, err
	}
	return client.ListParticipants(ctx, input.Token)
}

func invokeTalkMessagesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, input, err := talkInvoke(ctx, "list talk messages", resolved, secrets, red, raw)
	if err != nil {
		return nil, err
	}
	limit := defaultMessages
	if input.Limit != nil {
		limit = *input.Limit
	}
	return client.ListMessages(ctx, input.Token, limit, input.Cursor)
}
