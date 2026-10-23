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
	maxReactions       = 50
	maxReactionText    = 64
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

// optInt is an integer member that may be absent. A value that is not a whole number counts as absent.
type optInt struct {
	value int64
	set   bool
}

func (o *optInt) UnmarshalJSON(data []byte) error {
	if value, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil {
		o.value, o.set = value, true
	}
	return nil
}

func (o optInt) ptr() *int64 {
	if !o.set {
		return nil
	}
	return &o.value
}

// optBool is a flag member that may be absent. A number other than 0 and 1 counts as absent.
type optBool struct {
	value bool
	set   bool
}

func (o *optBool) UnmarshalJSON(data []byte) error {
	switch strings.TrimSpace(string(data)) {
	case "true", "1":
		o.value, o.set = true, true
	case "false", "0":
		o.value, o.set = false, true
	}
	return nil
}

func (o optBool) ptr() *bool {
	if !o.set {
		return nil
	}
	return &o.value
}

// extraText bounds and cleans a provider string that is reported as an extra member.
func extraText(value string) string {
	return cleanText(value, maxValueLength)
}

// Room is the stable Qatlas view of one conversation. Times are Unix seconds where the member is not
// last_activity; members the server does not deliver are absent.
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

	ID                    *int64   `json:"id,omitempty"`
	AttendeeID            *int64   `json:"attendee_id,omitempty"`
	ActorType             string   `json:"actor_type,omitempty"`
	ActorID               string   `json:"actor_id,omitempty"`
	Permissions           *int64   `json:"permissions,omitempty"`
	AttendeePermissions   *int64   `json:"attendee_permissions,omitempty"`
	CallPermissions       *int64   `json:"call_permissions,omitempty"`
	DefaultPermissions    *int64   `json:"default_permissions,omitempty"`
	ParticipantFlags      *int64   `json:"participant_flags,omitempty"`
	Listable              *int64   `json:"listable,omitempty"`
	MessageExpiration     *int64   `json:"message_expiration,omitempty"`
	LastPing              *int64   `json:"last_ping,omitempty"`
	HasCall               *bool    `json:"has_call,omitempty"`
	CallFlag              *int64   `json:"call_flag,omitempty"`
	CanStartCall          *bool    `json:"can_start_call,omitempty"`
	CanDeleteConversation *bool    `json:"can_delete_conversation,omitempty"`
	CanLeaveConversation  *bool    `json:"can_leave_conversation,omitempty"`
	IsFavorite            *bool    `json:"is_favorite,omitempty"`
	NotificationLevel     *int64   `json:"notification_level,omitempty"`
	LobbyState            *int64   `json:"lobby_state,omitempty"`
	LobbyTimer            *int64   `json:"lobby_timer,omitempty"`
	SIPEnabled            *int64   `json:"sip_enabled,omitempty"`
	CanEnableSIP          *bool    `json:"can_enable_sip,omitempty"`
	UnreadMentionDirect   *bool    `json:"unread_mention_direct,omitempty"`
	LastReadMessage       *int64   `json:"last_read_message,omitempty"`
	LastCommonReadMessage *int64   `json:"last_common_read_message,omitempty"`
	LastMessage           *Message `json:"last_message,omitempty"`
	ObjectType            string   `json:"object_type,omitempty"`
	ObjectID              string   `json:"object_id,omitempty"`
	BreakoutRoomMode      string   `json:"breakout_room_mode,omitempty"`
	BreakoutRoomStatus    string   `json:"breakout_room_status,omitempty"`
	Status                string   `json:"status,omitempty"`
	StatusIcon            string   `json:"status_icon,omitempty"`
	StatusMessage         string   `json:"status_message,omitempty"`
	StatusClearAt         *int64   `json:"status_clear_at,omitempty"`
	AvatarVersion         string   `json:"avatar_version,omitempty"`
	IsCustomAvatar        *bool    `json:"is_custom_avatar,omitempty"`
	CallStartTime         *int64   `json:"call_start_time,omitempty"`
	CallRecording         *int64   `json:"call_recording,omitempty"`
	RecordingConsent      *int64   `json:"recording_consent,omitempty"`
	MentionPermissions    *int64   `json:"mention_permissions,omitempty"`
	IsArchived            *bool    `json:"is_archived,omitempty"`
	RemoteServer          string   `json:"remote_server,omitempty"`
}

// RoomsResult is the list of bound conversations.
type RoomsResult struct {
	Rooms     []Room `json:"rooms"`
	Count     int    `json:"count"`
	Truncated bool   `json:"truncated,omitempty"`
}

// Participant is one member of a conversation.
type Participant struct {
	AttendeeID      string `json:"attendee_id,omitempty"`
	ActorType       string `json:"actor_type"`
	ActorID         string `json:"actor_id"`
	DisplayName     string `json:"display_name,omitempty"`
	ParticipantType string `json:"participant_type"`
	InCall          bool   `json:"in_call"`

	Permissions         *int64 `json:"permissions,omitempty"`
	AttendeePermissions *int64 `json:"attendee_permissions,omitempty"`
	LastPing            *int64 `json:"last_ping,omitempty"`
	InCallFlags         *int64 `json:"in_call_flags,omitempty"`
	RoomToken           string `json:"room_token,omitempty"`
	Status              string `json:"status,omitempty"`
	StatusIcon          string `json:"status_icon,omitempty"`
	StatusMessage       string `json:"status_message,omitempty"`
	PhoneNumber         string `json:"phone_number,omitempty"`
	ActorAvatarVersion  string `json:"actor_avatar_version,omitempty"`
}

// ParticipantsResult is the list of the participants of one bound conversation.
type ParticipantsResult struct {
	Participants []Participant `json:"participants"`
	Count        int           `json:"count"`
	Truncated    bool          `json:"truncated,omitempty"`
}

// MessageObject is a rich object a message refers to. Details holds its further members; access values such
// as tokens never appear in it.
type MessageObject struct {
	Key           string         `json:"key"`
	Type          string         `json:"type"`
	ID            string         `json:"id,omitempty"`
	Name          string         `json:"name,omitempty"`
	Details       map[string]any `json:"details,omitempty"`
	OmittedFields []string       `json:"omitted_fields,omitempty"`
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

	Token               string           `json:"token,omitempty"`
	IsReplyable         *bool            `json:"is_replyable,omitempty"`
	ExpirationTimestamp *int64           `json:"expiration_timestamp,omitempty"`
	Reactions           map[string]int64 `json:"reactions,omitempty"`
	ReactionsSelf       []string         `json:"reactions_self,omitempty"`
	Markdown            *bool            `json:"markdown,omitempty"`
	LastEditActorType   string           `json:"last_edit_actor_type,omitempty"`
	LastEditActorID     string           `json:"last_edit_actor_id,omitempty"`
	LastEditActorName   string           `json:"last_edit_actor_name,omitempty"`
	LastEditTime        string           `json:"last_edit_time,omitempty"`
	Silent              *bool            `json:"silent,omitempty"`
	Parent              *MessageParent   `json:"parent,omitempty"`
	OmittedFields       []string         `json:"omitted_fields,omitempty"`
}

// MessageParent is the message a reply answers: only its ID and deleted when it was deleted, otherwise the
// entry of the message without a parent of its own. The ID is the member of the parent that shadows the one
// of the embedded message.
type MessageParent struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted,omitempty"`
	*Message
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

	// Members the reads add. The attendee PIN, the session ID, the remote token, and the call identifier
	// are deliberately not members: they cannot be decoded, so they cannot reach a result.
	ID                    optInt          `json:"id"`
	AttendeeID            optInt          `json:"attendeeId"`
	ActorType             string          `json:"actorType"`
	ActorID               flexString      `json:"actorId"`
	Permissions           optInt          `json:"permissions"`
	AttendeePermissions   optInt          `json:"attendeePermissions"`
	CallPermissions       optInt          `json:"callPermissions"`
	DefaultPermissions    optInt          `json:"defaultPermissions"`
	ParticipantFlags      optInt          `json:"participantFlags"`
	Listable              optInt          `json:"listable"`
	MessageExpiration     optInt          `json:"messageExpiration"`
	LastPing              optInt          `json:"lastPing"`
	HasCall               optBool         `json:"hasCall"`
	CallFlag              optInt          `json:"callFlag"`
	CanStartCall          optBool         `json:"canStartCall"`
	CanDeleteConversation optBool         `json:"canDeleteConversation"`
	CanLeaveConversation  optBool         `json:"canLeaveConversation"`
	IsFavorite            optBool         `json:"isFavorite"`
	NotificationLevel     optInt          `json:"notificationLevel"`
	LobbyState            optInt          `json:"lobbyState"`
	LobbyTimer            optInt          `json:"lobbyTimer"`
	SIPEnabled            optInt          `json:"sipEnabled"`
	CanEnableSIP          optBool         `json:"canEnableSIP"`
	UnreadMentionDirect   optBool         `json:"unreadMentionDirect"`
	LastReadMessage       optInt          `json:"lastReadMessage"`
	LastCommonReadMessage optInt          `json:"lastCommonReadMessage"`
	LastMessage           json.RawMessage `json:"lastMessage"`
	ObjectType            string          `json:"objectType"`
	ObjectID              flexString      `json:"objectId"`
	BreakoutRoomMode      flexString      `json:"breakoutRoomMode"`
	BreakoutRoomStatus    flexString      `json:"breakoutRoomStatus"`
	Status                string          `json:"status"`
	StatusIcon            string          `json:"statusIcon"`
	StatusMessage         string          `json:"statusMessage"`
	StatusClearAt         optInt          `json:"statusClearAt"`
	AvatarVersion         flexString      `json:"avatarVersion"`
	IsCustomAvatar        optBool         `json:"isCustomAvatar"`
	CallStartTime         optInt          `json:"callStartTime"`
	CallRecording         optInt          `json:"callRecording"`
	RecordingConsent      optInt          `json:"recordingConsent"`
	MentionPermissions    optInt          `json:"mentionPermissions"`
	IsArchived            optBool         `json:"isArchived"`
	RemoteServer          string          `json:"remoteServer"`
}

type rawParticipant struct {
	AttendeeID      json.Number `json:"attendeeId"`
	ActorType       string      `json:"actorType"`
	ActorID         string      `json:"actorId"`
	DisplayName     string      `json:"displayName"`
	ParticipantType json.Number `json:"participantType"`
	InCall          optInt      `json:"inCall"`

	// The session IDs, the phone number's call identifier, and the like are deliberately not members.
	Permissions         optInt     `json:"permissions"`
	AttendeePermissions optInt     `json:"attendeePermissions"`
	LastPing            optInt     `json:"lastPing"`
	RoomToken           string     `json:"roomToken"`
	Status              string     `json:"status"`
	StatusIcon          string     `json:"statusIcon"`
	StatusMessage       string     `json:"statusMessage"`
	PhoneNumber         string     `json:"phoneNumber"`
	ActorAvatarVersion  flexString `json:"actorAvatarVersion"`
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
	Parent            json.RawMessage `json:"parent"`

	Token                    string          `json:"token"`
	IsReplyable              optBool         `json:"isReplyable"`
	ExpirationTimestamp      optInt          `json:"expirationTimestamp"`
	Reactions                json.RawMessage `json:"reactions"`
	ReactionsSelf            json.RawMessage `json:"reactionsSelf"`
	Markdown                 optBool         `json:"markdown"`
	LastEditActorType        string          `json:"lastEditActorType"`
	LastEditActorID          flexString      `json:"lastEditActorId"`
	LastEditActorDisplayName string          `json:"lastEditActorDisplayName"`
	LastEditTimestamp        json.Number     `json:"lastEditTimestamp"`
	Silent                   optBool         `json:"silent"`
	Deleted                  optBool         `json:"deleted"`
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

// lastMessageOf maps the last message of a conversation like an entry of the message list, without the
// parent and the reactions of the identity. A conversation without one carries an empty array or object.
func lastMessageOf(raw json.RawMessage) *Message {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	var message rawMessage
	if err := json.Unmarshal(raw, &message); err != nil || message.ID.String() == "" {
		return nil
	}
	mapped := messageView(message, false, false)
	return &mapped
}

// roomDetailOf adds the members of the read tools to the view. The user status of a one-to-one
// conversation is reported only when it was requested.
func roomDetailOf(raw rawRoom, withStatus bool) Room {
	room := roomOf(raw)
	room.ID, room.AttendeeID = raw.ID.ptr(), raw.AttendeeID.ptr()
	room.ActorType, room.ActorID = extraText(raw.ActorType), extraText(string(raw.ActorID))
	room.Permissions, room.AttendeePermissions = raw.Permissions.ptr(), raw.AttendeePermissions.ptr()
	room.CallPermissions, room.DefaultPermissions = raw.CallPermissions.ptr(), raw.DefaultPermissions.ptr()
	room.ParticipantFlags, room.Listable = raw.ParticipantFlags.ptr(), raw.Listable.ptr()
	room.MessageExpiration, room.LastPing = raw.MessageExpiration.ptr(), raw.LastPing.ptr()
	room.HasCall, room.CallFlag = raw.HasCall.ptr(), raw.CallFlag.ptr()
	room.CanStartCall, room.CanDeleteConversation = raw.CanStartCall.ptr(), raw.CanDeleteConversation.ptr()
	room.CanLeaveConversation, room.IsFavorite = raw.CanLeaveConversation.ptr(), raw.IsFavorite.ptr()
	room.NotificationLevel, room.LobbyState = raw.NotificationLevel.ptr(), raw.LobbyState.ptr()
	room.LobbyTimer, room.SIPEnabled = raw.LobbyTimer.ptr(), raw.SIPEnabled.ptr()
	room.CanEnableSIP, room.UnreadMentionDirect = raw.CanEnableSIP.ptr(), raw.UnreadMentionDirect.ptr()
	room.LastReadMessage, room.LastCommonReadMessage = raw.LastReadMessage.ptr(), raw.LastCommonReadMessage.ptr()
	room.LastMessage = lastMessageOf(raw.LastMessage)
	room.ObjectType, room.ObjectID = extraText(raw.ObjectType), extraText(string(raw.ObjectID))
	room.BreakoutRoomMode, room.BreakoutRoomStatus = extraText(string(raw.BreakoutRoomMode)), extraText(string(raw.BreakoutRoomStatus))
	room.AvatarVersion, room.IsCustomAvatar = extraText(string(raw.AvatarVersion)), raw.IsCustomAvatar.ptr()
	room.CallStartTime, room.CallRecording = raw.CallStartTime.ptr(), raw.CallRecording.ptr()
	room.RecordingConsent, room.MentionPermissions = raw.RecordingConsent.ptr(), raw.MentionPermissions.ptr()
	room.IsArchived, room.RemoteServer = raw.IsArchived.ptr(), extraText(raw.RemoteServer)
	if withStatus {
		room.Status, room.StatusIcon, room.StatusMessage = extraText(raw.Status), extraText(raw.StatusIcon), extraText(raw.StatusMessage)
		room.StatusClearAt = raw.StatusClearAt.ptr()
	}
	return room
}

// ListRooms reads the conversations of the identity and keeps those the connection binds.
// modifiedSince, when set, limits the list to conversations with newer activity; withStatus adds the user
// status of one-to-one conversations.
func (c *Client) ListRooms(ctx context.Context, talks selection, modifiedSince *int64, withStatus bool) (*RoomsResult, error) {
	const op = "list talk conversations"
	if modifiedSince != nil && *modifiedSince < 0 {
		return nil, providerError(op, "the conversation arguments are unusable")
	}
	if err := c.talkFeature(ctx, op, featureConversations); err != nil {
		return nil, err
	}
	query := url.Values{"noStatusUpdate": {"1"}}
	if modifiedSince != nil {
		query.Set("modifiedSince", strconv.FormatInt(*modifiedSince, 10))
	}
	if withStatus {
		query.Set("includeStatus", "true")
	}
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsTalkRooms, query: query})
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
		result.Rooms = append(result.Rooms, roomDetailOf(item, withStatus))
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
	room := roomDetailOf(raw, false)
	return &room, nil
}

// ListParticipants reads the participants of one conversation the caller has checked.
// withStatus adds the user status of the participants.
func (c *Client) ListParticipants(ctx context.Context, token string, withStatus bool) (*ParticipantsResult, error) {
	const op = "list talk participants"
	if !validTalkToken(token) {
		return nil, providerError(op, "a conversation token is unusable")
	}
	if err := c.talkFeature(ctx, op, featureConversations); err != nil {
		return nil, err
	}
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsTalkRooms, suffix: []string{token, "participants"},
		query: url.Values{"includeStatus": {strconv.FormatBool(withStatus)}}})
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
		participant := Participant{
			AttendeeID: bounded(item.AttendeeID.String()), ActorType: bounded(item.ActorType), ActorID: bounded(item.ActorID), DisplayName: bounded(item.DisplayName),
			ParticipantType: nameOf(participantTypes, item.ParticipantType), InCall: item.InCall.value != 0,
			Permissions: item.Permissions.ptr(), AttendeePermissions: item.AttendeePermissions.ptr(),
			LastPing: item.LastPing.ptr(), InCallFlags: item.InCall.ptr(), RoomToken: extraText(item.RoomToken),
			PhoneNumber: extraText(item.PhoneNumber), ActorAvatarVersion: extraText(string(item.ActorAvatarVersion)),
		}
		if withStatus {
			participant.Status, participant.StatusIcon, participant.StatusMessage = extraText(item.Status), extraText(item.StatusIcon), extraText(item.StatusMessage)
		}
		result.Participants = append(result.Participants, participant)
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

// messageOf builds the view of one message entry.
func messageOf(raw rawMessage) Message {
	return messageView(raw, true, true)
}

// messageView builds the view of one message. Placeholders such as {actor} or {file} are replaced by the name
// of the rich object, whose further members go to its details. An entry carries its parent; the parent
// itself and the last message of a conversation do not. The last message carries no reactions of the identity.
func messageView(raw rawMessage, withParent, self bool) Message {
	var parameters map[string]map[string]json.RawMessage
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
		object := richObjectOf(key, parameters[key])
		objects = append(objects, object)
		replacement := object.Name
		switch object.Type {
		case "user", "group", "guest", "call":
			replacement = "@" + object.Name
		}
		if object.Name == "" {
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
		Token: bounded(raw.Token), IsReplyable: raw.IsReplyable.ptr(), Markdown: raw.Markdown.ptr(), Silent: raw.Silent.ptr(),
		LastEditActorType: bounded(raw.LastEditActorType), LastEditActorID: bounded(string(raw.LastEditActorID)),
		LastEditActorName: bounded(raw.LastEditActorDisplayName), LastEditTime: unixTime(raw.LastEditTimestamp),
	}
	if raw.ExpirationTimestamp.set && raw.ExpirationTimestamp.value > 0 {
		message.ExpirationTimestamp = raw.ExpirationTimestamp.ptr()
	}
	var omitted []string
	var cutReactions bool
	if message.Reactions, cutReactions = reactionsOf(raw.Reactions); cutReactions {
		omitted = append(omitted, "reactions")
	}
	if self {
		var cutSelf bool
		if message.ReactionsSelf, cutSelf = reactionsSelfOf(raw.ReactionsSelf); cutSelf {
			omitted = append(omitted, "reactions_self")
		}
	}
	message.OmittedFields = omitted
	var parent rawMessage
	if json.Unmarshal(raw.Parent, &parent) != nil || parent.ID.String() == "" {
		return message
	}
	message.ParentID = parent.ID.String()
	if withParent {
		message.Parent = &MessageParent{ID: message.ParentID, Deleted: true}
		if !parent.Deleted.value {
			view := messageView(parent, false, true)
			message.Parent = &MessageParent{ID: message.ParentID, Message: &view}
		}
	}
	return message
}

// reactionsOf maps the reaction counts of a message, emoji to number. More reactions than the bound allow are
// left out as a whole and reported as cut.
func reactionsOf(raw json.RawMessage) (map[string]int64, bool) {
	var counts map[string]optInt
	if json.Unmarshal(raw, &counts) != nil || len(counts) == 0 {
		return nil, false
	}
	if len(counts) > maxReactions {
		return nil, true
	}
	reactions := map[string]int64{}
	for emoji, count := range counts {
		if len(emoji) > maxReactionText {
			return nil, true
		}
		if emoji = cleanText(emoji, maxReactionText); emoji != "" && count.set {
			reactions[emoji] = count.value
		}
	}
	if len(reactions) == 0 {
		return nil, false
	}
	return reactions, false
}

// reactionsSelfOf maps the emojis the identity reacted with, bounded like the reactions.
func reactionsSelfOf(raw json.RawMessage) ([]string, bool) {
	var emojis []string
	if json.Unmarshal(raw, &emojis) != nil || len(emojis) == 0 {
		return nil, false
	}
	if len(emojis) > maxReactions {
		return nil, true
	}
	var result []string
	for _, emoji := range emojis {
		if len(emoji) > maxReactionText {
			return nil, true
		}
		if emoji = cleanText(emoji, maxReactionText); emoji != "" {
			result = append(result, emoji)
		}
	}
	return result, false
}

const (
	roomDetailProperties = `"id":{"type":"integer"},"attendee_id":{"type":"integer"},"actor_type":{"type":"string"},` +
		`"actor_id":{"type":"string"},"permissions":{"type":"integer"},"attendee_permissions":{"type":"integer"},` +
		`"call_permissions":{"type":"integer"},"default_permissions":{"type":"integer"},` +
		`"participant_flags":{"type":"integer"},"listable":{"type":"integer"},"message_expiration":{"type":"integer"},` +
		`"last_ping":{"type":"integer"},"has_call":{"type":"boolean"},"call_flag":{"type":"integer"},` +
		`"can_start_call":{"type":"boolean"},"can_delete_conversation":{"type":"boolean"},` +
		`"can_leave_conversation":{"type":"boolean"},"is_favorite":{"type":"boolean"},` +
		`"notification_level":{"type":"integer"},"lobby_state":{"type":"integer"},"lobby_timer":{"type":"integer"},` +
		`"sip_enabled":{"type":"integer"},"can_enable_sip":{"type":"boolean"},"unread_mention_direct":{"type":"boolean"},` +
		`"last_read_message":{"type":"integer"},"last_common_read_message":{"type":"integer"},` +
		`"last_message":` + messageSchema + `,"object_type":{"type":"string"},"object_id":{"type":"string"},` +
		`"breakout_room_mode":{"type":"string"},"breakout_room_status":{"type":"string"},"status":{"type":"string"},` +
		`"status_icon":{"type":"string"},"status_message":{"type":"string"},"status_clear_at":{"type":"integer"},` +
		`"avatar_version":{"type":"string"},"is_custom_avatar":{"type":"boolean"},"call_start_time":{"type":"integer"},` +
		`"call_recording":{"type":"integer"},"recording_consent":{"type":"integer"},` +
		`"mention_permissions":{"type":"integer"},"is_archived":{"type":"boolean"},"remote_server":{"type":"string"}`
	talkTokenSchema = `{"type":"string","pattern":"^[A-Za-z0-9]{4,32}$"}`
	roomProperties  = `"token":{"type":"string"},"name":{"type":"string"},` +
		`"display_name":{"type":"string"},"description":{"type":"string"},` +
		`"type":{"type":"string","enum":["one-to-one","group","public","changelog","former-one-to-one","note-to-self","other"]},` +
		`"read_only":{"type":"boolean"},"has_password":{"type":"boolean"},"participant_type":{"type":"string"},` +
		`"unread_messages":{"type":"integer"},"unread_mention":{"type":"boolean"},"last_activity":{"type":"string"}`
	roomRequired = `"required":["token","type","read_only","has_password","participant_type","unread_messages","unread_mention"],` +
		`"additionalProperties":false}`
	roomSchema        = `{"type":"object","properties":{` + roomProperties + `},` + roomRequired
	roomDetailSchema  = `{"type":"object","properties":{` + roomProperties + `,` + roomDetailProperties + `},` + roomRequired
	participantSchema = `{"type":"object","properties":{"attendee_id":{"type":"string"},"actor_type":{"type":"string"},"actor_id":{"type":"string"},` +
		`"display_name":{"type":"string"},"participant_type":{"type":"string"},"in_call":{"type":"boolean"},` +
		`"permissions":{"type":"integer"},"attendee_permissions":{"type":"integer"},"last_ping":{"type":"integer"},` +
		`"in_call_flags":{"type":"integer"},"room_token":{"type":"string"},"status":{"type":"string"},` +
		`"status_icon":{"type":"string"},"status_message":{"type":"string"},"phone_number":{"type":"string"},` +
		`"actor_avatar_version":{"type":"string"}},` +
		`"required":["actor_type","actor_id","participant_type","in_call"],"additionalProperties":false}`
	messageProperties = `"id":{"type":"string"},"time":{"type":"string"},` +
		`"actor_type":{"type":"string"},"actor_id":{"type":"string"},"actor_name":{"type":"string"},` +
		`"kind":{"type":"string"},"system_message":{"type":"string"},"text":{"type":"string"},` +
		`"objects":{"type":"array","items":{"type":"object","properties":{"key":{"type":"string"},` +
		`"type":{"type":"string"},"id":{"type":"string"},"name":{"type":"string"},` +
		`"details":{"type":"object","maxProperties":20,"additionalProperties":{"type":["string","integer"]}},` +
		`"omitted_fields":{"type":"array","items":{"type":"string"}}},` +
		`"required":["key","type"],"additionalProperties":false}},` +
		`"parent_id":{"type":"string"},"reference_id":{"type":"string"},"truncated":{"type":"boolean"},` +
		`"token":{"type":"string"},"is_replyable":{"type":"boolean"},"expiration_timestamp":{"type":"integer"},` +
		`"reactions":{"type":"object","maxProperties":50,"additionalProperties":{"type":"integer"}},` +
		`"reactions_self":{"type":"array","items":{"type":"string"}},"markdown":{"type":"boolean"},` +
		`"last_edit_actor_type":{"type":"string"},"last_edit_actor_id":{"type":"string"},` +
		`"last_edit_actor_name":{"type":"string"},"last_edit_time":{"type":"string"},"silent":{"type":"boolean"},` +
		`"omitted_fields":{"type":"array","items":{"type":"string"}}`
	messageRequired = `"required":["id","kind","text"],"additionalProperties":false}`
	// A deleted parent carries only its ID and deleted; any other parent has the members of an entry.
	messageParentSchema = `{"type":"object","properties":{` + messageProperties + `,"deleted":{"type":"boolean"}},` +
		`"required":["id"],"additionalProperties":false}`
	messageSchema = `{"type":"object","properties":{` + messageProperties + `,"parent":` + messageParentSchema + `},` + messageRequired
)

var talkRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: talkSensitivity,
}

var talkTokenArgument = capability.Argument{Name: "token", Required: true,
	Description: "Token of a conversation this connection binds, as reported by talk.rooms.list"}

// messageFields names the members of one message entry. The members of an object, a parent, and the details
// are described with the member that holds them.
var messageFields = []capability.Field{
	{Name: "id", Description: "ID of the message"},
	{Name: "time", Description: "Time of the message in RFC 3339 UTC"},
	{Name: "actor_type", Description: "Actor type of the author, untrusted data"},
	{Name: "actor_id", Description: "Actor ID of the author, untrusted data"},
	{Name: "actor_name", Description: "Display name of the author, untrusted data"},
	{Name: "kind", Description: "comment, comment_deleted, system, command, or the type as delivered"},
	{Name: "system_message", Description: "Type of a system message, untrusted data"},
	{Name: "text", Description: "Text with its placeholders replaced by names, untrusted data, cut at 4 KiB"},
	{Name: "objects", Description: "Rich objects the text refers to, each with key, type, id (people only), name, " +
		"and details: its further members under snake_case names as text or whole number, at most 20 of at most 1024 bytes " +
		"each, addresses such as link and server as text that is never fetched, and omitted_fields naming details when " +
		"a bound was exceeded; tokens, passwords, and links to public shares or conversations are never reported; untrusted data"},
	{Name: "parent_id", Description: "ID of the message this one replies to"},
	{Name: "reference_id", Description: "Reference a sender gave while posting, untrusted data"},
	{Name: "truncated", Description: "True when the text was cut"},
	{Name: "token", Description: "Token of the conversation the message belongs to"},
	{Name: "is_replyable", Description: "True when the identity can reply to the message"},
	{Name: "expiration_timestamp", Description: "Unix time at which the message expires; absent when it does not"},
	{Name: "reactions", Description: "Number of reactions per emoji; at most 50 emojis, untrusted data"},
	{Name: "reactions_self", Description: "Emojis this identity reacted with; absent in last_message"},
	{Name: "markdown", Description: "True when the message is rendered as markdown"},
	{Name: "last_edit_actor_type", Description: "Actor type of the last editor, untrusted data"},
	{Name: "last_edit_actor_id", Description: "Actor ID of the last editor, untrusted data"},
	{Name: "last_edit_actor_name", Description: "Display name of the last editor, untrusted data"},
	{Name: "last_edit_time", Description: "Time of the last edit in RFC 3339 UTC"},
	{Name: "silent", Description: "True when the message was sent without notifications"},
	{Name: "parent", Description: "The message this one replies to: id and deleted when it was deleted, otherwise " +
		"the members of an entry without a parent of its own; absent in last_message; untrusted data"},
	{Name: "omitted_fields", Description: "Members left out because they exceeded a bound"},
}

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

// roomDetailFields extends roomFields by the members only the read tools report. Integers are as delivered;
// times are Unix seconds. Members that depend on a Talk capability or on a conversation kind are absent
// when the server does not deliver them.
var roomDetailFields = append(append([]capability.Field{}, roomFields...), []capability.Field{
	{Name: "id", Description: "Numeric ID of the conversation"},
	{Name: "attendee_id", Description: "Attendee ID of the identity in the conversation"},
	{Name: "actor_type", Description: "Actor type of the identity, untrusted data"},
	{Name: "actor_id", Description: "Actor ID of the identity, untrusted data"},
	{Name: "permissions", Description: "Effective permission bit mask of the identity"},
	{Name: "attendee_permissions", Description: "Permission bit mask set for the identity itself"},
	{Name: "call_permissions", Description: "Permission bit mask set for the identity in calls"},
	{Name: "default_permissions", Description: "Default permission bit mask of the conversation"},
	{Name: "participant_flags", Description: "In-call flags of the identity"},
	{Name: "listable", Description: "Scope in which the conversation is listable"},
	{Name: "message_expiration", Description: "Seconds after which messages expire; 0 for never"},
	{Name: "last_ping", Description: "Unix time of the last ping of the identity"},
	{Name: "has_call", Description: "True when a call is running"},
	{Name: "call_flag", Description: "Combined in-call flags of the running call"},
	{Name: "can_start_call", Description: "True when the identity may start a call"},
	{Name: "can_delete_conversation", Description: "True when the identity may delete the conversation"},
	{Name: "can_leave_conversation", Description: "True when the identity may leave the conversation"},
	{Name: "is_favorite", Description: "True when the identity marked the conversation as favorite"},
	{Name: "notification_level", Description: "Notification level of the identity"},
	{Name: "lobby_state", Description: "State of the lobby"},
	{Name: "lobby_timer", Description: "Unix time at which the lobby ends"},
	{Name: "sip_enabled", Description: "SIP dial-in state of the conversation"},
	{Name: "can_enable_sip", Description: "True when the identity may enable SIP dial-in"},
	{Name: "unread_mention_direct", Description: "True when an unread message mentions the identity directly"},
	{Name: "last_read_message", Description: "ID of the last message the identity read"},
	{Name: "last_common_read_message", Description: "ID of the last message everyone read"},
	{Name: "last_message", Description: "Last message in the form of a talk.messages.list entry without parent and reactions_self, untrusted data"},
	{Name: "object_type", Description: "Type of the object the conversation belongs to, untrusted data"},
	{Name: "object_id", Description: "ID of the object the conversation belongs to, untrusted data"},
	{Name: "breakout_room_mode", Description: "Breakout room mode as delivered"},
	{Name: "breakout_room_status", Description: "Breakout room status as delivered"},
	{Name: "status", Description: "User status of the other person of a one-to-one conversation; only with include_status"},
	{Name: "status_icon", Description: "Status icon, untrusted data; only with include_status"},
	{Name: "status_message", Description: "Status message, untrusted data; only with include_status"},
	{Name: "status_clear_at", Description: "Unix time at which the status is cleared; only with include_status"},
	{Name: "avatar_version", Description: "Version of the avatar"},
	{Name: "is_custom_avatar", Description: "True when the avatar was set by a person"},
	{Name: "call_start_time", Description: "Unix time at which the running call started"},
	{Name: "call_recording", Description: "Recording state of the call"},
	{Name: "recording_consent", Description: "Recording consent setting of the conversation"},
	{Name: "mention_permissions", Description: "Who may mention everyone"},
	{Name: "is_archived", Description: "True when the conversation is archived"},
	{Name: "remote_server", Description: "Address of the home server of a federated conversation, untrusted data, never fetched"},
}...)

// participantFields names the members of one participant.
var participantFields = []capability.Field{
	{Name: "attendee_id", Description: "Attendee ID, the argument of remove and moderator changes"},
	{Name: "actor_type", Description: "Actor type, untrusted data"},
	{Name: "actor_id", Description: "Actor ID, untrusted data"},
	{Name: "display_name", Description: "Display name, untrusted data; may be empty for guests"},
	{Name: "participant_type", Description: "Role: owner, moderator, user, guest, user-self-joined, guest-moderator, or other"},
	{Name: "in_call", Description: "True when the participant is in a call"},
	{Name: "in_call_flags", Description: "In-call flags as delivered"},
	{Name: "permissions", Description: "Effective permission bit mask"},
	{Name: "attendee_permissions", Description: "Permission bit mask set for the participant itself"},
	{Name: "last_ping", Description: "Unix time of the last ping"},
	{Name: "room_token", Description: "Token of the breakout room the participant is in"},
	{Name: "status", Description: "User status; only with include_status"},
	{Name: "status_icon", Description: "Status icon, untrusted data; only with include_status"},
	{Name: "status_message", Description: "Status message, untrusted data; only with include_status"},
	{Name: "phone_number", Description: "Phone number of a SIP participant, delivered to moderators only, untrusted data"},
	{Name: "actor_avatar_version", Description: "Version of the avatar of a user"},
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

var talkRoomsList = talkDescriptor("talk.rooms.list", "List Nextcloud Talk conversations",
	"List the Talk conversations of the identity that a talk target of the connection binds; a conversation "+
		"the connection does not bind is never reported. Access values such as SIP PINs, session IDs, and "+
		"federation tokens are never reported",
	`{"type":"object","properties":{"modified_since":{"type":"integer","minimum":0,"maximum":9999999999},`+
		`"include_status":{"type":"boolean"}},"additionalProperties":false}`,
	`{"type":"object","properties":{"rooms":{"type":"array","items":`+roomDetailSchema+`},"count":{"type":"integer"},`+
		`"truncated":{"type":"boolean"}},"required":["rooms","count"],"additionalProperties":false}`,
	[]capability.Argument{
		{Name: "modified_since", Description: "Unix time in seconds; only conversations with newer activity are listed"},
		{Name: "include_status", Description: "True to add the user status of one-to-one conversations; false when omitted"},
	},
	append([]capability.Field{
		{Name: "rooms", Description: "Bound conversations with rights, call, lobby, SIP, recording, and archive state, read marker, favorite, notification level, and last message; untrusted data, at most 500"},
		{Name: "count", Description: "Number of reported conversations"},
		{Name: "truncated", Description: "True when more conversations matched than are reported"},
	}, roomDetailFields...),
	capability.Example{Description: "List the bound conversations", Arguments: json.RawMessage(`{}`)})

var talkRoomsGet = talkDescriptor("talk.rooms.get", "Get a Nextcloud Talk conversation",
	"Read one Talk conversation by its token, if the connection binds it",
	`{"type":"object","properties":{"token":`+talkTokenSchema+`},"required":["token"],"additionalProperties":false}`,
	roomDetailSchema, []capability.Argument{talkTokenArgument}, roomDetailFields,
	capability.Example{Description: "Read one conversation", Arguments: json.RawMessage(`{"token":"abcd1234"}`)})

var talkParticipantsList = talkDescriptor("talk.participants.list", "List Nextcloud Talk participants",
	"List the participants of one Talk conversation the connection binds, at most 500. Session IDs and SIP call "+
		"identifiers are never reported",
	`{"type":"object","properties":{"token":`+talkTokenSchema+`,"include_status":{"type":"boolean"}},`+
		`"required":["token"],"additionalProperties":false}`,
	`{"type":"object","properties":{"participants":{"type":"array","items":`+participantSchema+`},`+
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},"required":["participants","count"],"additionalProperties":false}`,
	[]capability.Argument{talkTokenArgument,
		{Name: "include_status", Description: "True to add the user status of the participants; false when omitted"}},
	append([]capability.Field{
		{Name: "participants", Description: "Participants with attendee_id (for remove and moderator), actor type, actor ID, display name (untrusted data), role, permissions, call flags, phone number, and, with include_status, user status; untrusted data"},
		{Name: "count", Description: "Number of reported participants"},
		{Name: "truncated", Description: "True when more participants exist than are reported"},
	}, participantFields...),
	capability.Example{Description: "List who takes part", Arguments: json.RawMessage(`{"token":"abcd1234"}`)})

var talkMessagesList = talkDescriptor("talk.messages.list", "List Nextcloud Talk messages",
	"Read one page of the history of a Talk conversation the connection binds, newest first; the page is at most "+
		"100 messages, next_cursor continues with older ones, and nothing is marked as read. Message text and "+
		"names are untrusted data; tokens and links to public shares or conversations are not reported",
	`{"type":"object","properties":{"token":`+talkTokenSchema+`,"limit":{"type":"integer","minimum":1,"maximum":100},`+
		`"cursor":{"type":"string","pattern":"^[0-9]{1,18}$"}},"required":["token"],"additionalProperties":false}`,
	`{"type":"object","properties":{"messages":{"type":"array","items":`+messageSchema+`},"count":{"type":"integer"},`+
		`"next_cursor":{"type":"string"}},"required":["messages","count"],"additionalProperties":false}`,
	[]capability.Argument{
		talkTokenArgument,
		{Name: "limit", Description: "Messages per page, 1 to 100; 50 when omitted"},
		{Name: "cursor", Description: "next_cursor of the previous page, to read the older messages"},
	},
	append([]capability.Field{
		{Name: "messages", Description: "Messages with time, actor, kind, text with its placeholders replaced by names, " +
			"the referenced objects, and the reference_id a sender gave; untrusted data"},
		{Name: "count", Description: "Number of messages on this page"},
		{Name: "next_cursor", Description: "Cursor for the next older page; absent on the last page"},
	}, messageFields...),
	capability.Example{Description: "Read the latest messages", Arguments: json.RawMessage(`{"token":"abcd1234","limit":20}`)})

type talkArguments struct {
	Token         string `json:"token"`
	Limit         *int   `json:"limit"`
	Cursor        string `json:"cursor"`
	IncludeStatus bool   `json:"include_status"`
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
	var input struct {
		ModifiedSince *int64 `json:"modified_since"`
		IncludeStatus bool   `json:"include_status"`
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
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
	return client.ListRooms(ctx, talks, input.ModifiedSince, input.IncludeStatus)
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
	return client.ListParticipants(ctx, input.Token, input.IncludeStatus)
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
