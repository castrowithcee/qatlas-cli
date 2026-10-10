package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Managing participants only ever adds an existing user or group of the instance. An attendee ID reaches a
// request only after it was found in the participant list of the bound conversation, so no access to the
// outside is created and no other attendee is touched.

const (
	maxAttendeeIDLen = 18

	sourceUsers  = "users"
	sourceGroups = "groups"

	participantOwner = 1

	messageTalkNoAttendee = "this attendee is not a participant of the Talk conversation"
	messageTalkSelf       = "this identity cannot be removed from or changed in the conversation by this tool"
	messageTalkOwner      = "the owner of a Talk conversation cannot be removed or demoted"
	messageTalkNotUser    = "only a participant that is a user can be made or stopped being a moderator"
	messageTalkNoTarget   = "this Talk conversation or participant does not exist or is not accessible to this identity"

	uncertainTalkAdd       = "; the participant may have been added, check talkparticipants.list before repeating"
	uncertainTalkRemove    = "; the participant may have been removed, check talkparticipants.list before repeating"
	uncertainTalkModerator = "; the moderator right may have been changed, check talkparticipants.list before repeating"
)

// A user ID is the Nextcloud character set. A group ID may also contain a space, which must not lead or
// trail it.
var (
	userIDPattern  = regexp.MustCompile(`^[A-Za-z0-9_.@'-]{1,64}$`)
	groupIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.@'-]([A-Za-z0-9_.@' -]{0,62}[A-Za-z0-9_.@'-])?$`)
)

var (
	talkAddErrors = map[int]string{
		http.StatusBadRequest: "Talk refused to add this participant (for example the user or group does not exist or is already in the conversation)",
		http.StatusForbidden:  "this identity may not add participants to this Talk conversation",
	}
	talkRemoveErrors = map[int]string{
		http.StatusBadRequest: "Talk cannot remove this participant (for example it is the owner or the last moderator)",
		http.StatusForbidden:  "this identity may not remove participants from this Talk conversation",
	}
	talkModeratorErrors = map[int]string{
		http.StatusBadRequest: "Talk cannot change the moderator right of this participant (for example it is the owner)",
		http.StatusForbidden:  "this identity may not change moderator rights in this Talk conversation",
	}
)

// AddParticipantResult reports an accepted participant.
type AddParticipantResult struct {
	Added       bool   `json:"added"`
	Participant string `json:"participant"`
	Source      string `json:"source"`
}

// RemoveParticipantResult reports a removed participant.
type RemoveParticipantResult struct {
	Removed    bool   `json:"removed"`
	AttendeeID string `json:"attendee_id"`
}

// ModeratorResult reports a changed moderator right.
type ModeratorResult struct {
	AttendeeID string `json:"attendee_id"`
	Moderator  bool   `json:"moderator"`
}

func validAttendeeID(id string) bool { return digitsOnly(id) && len(id) <= maxAttendeeIDLen }

func validParticipant(participant, source string) bool {
	switch source {
	case sourceUsers:
		return userIDPattern.MatchString(participant)
	case sourceGroups:
		return groupIDPattern.MatchString(participant)
	}
	return false
}

func talkParticipantError(op string, err error, messages map[int]string) error {
	return talkNotFound(op, tagAdminError(op, err, messages), messageTalkNoTarget)
}

// AddParticipant adds an existing user or group after a single capability read.
func (c *Client) AddParticipant(ctx context.Context, token, participant, source string) (*AddParticipantResult, error) {
	const op = "add talk participant"
	if !validTalkToken(token) || !validParticipant(participant, source) {
		return nil, providerError(op, "the participant arguments are unusable")
	}
	if err := c.talkFeature(ctx, op, featureConversations); err != nil {
		return nil, err
	}
	form := url.Values{"newParticipant": {participant}, "source": {source}}
	_, err := c.ocsSend(ctx, op, http.MethodPost, ocsRequest{app: ocsTalkRooms, suffix: []string{token, "participants"}},
		form, uncertainTalkAdd)
	if err != nil {
		return nil, talkParticipantError(op, err, talkAddErrors)
	}
	return &AddParticipantResult{Added: true, Participant: participant, Source: source}, nil
}

// attendee reads the participant list of the conversation, which replaces the capability read, and returns
// the participant with the given attendee ID.
func (c *Client) attendee(ctx context.Context, op, token, id string) (rawParticipant, error) {
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsTalkRooms, suffix: []string{token, "participants"},
		query: url.Values{"includeStatus": {"false"}}})
	if err != nil {
		return rawParticipant{}, talkNotFound(op, err, messageTalkNoRoom)
	}
	var items []rawParticipant
	if err := json.Unmarshal(data, &items); err != nil {
		return rawParticipant{}, invalidResponse(op, "the Nextcloud participant list could not be read")
	}
	for _, item := range items {
		if item.AttendeeID.String() == id {
			return item, nil
		}
	}
	return rawParticipant{}, &provider.Error{Class: provider.ClassNotFound, Op: op, Message: messageTalkNoAttendee}
}

// refuseAttendee keeps the owner and the own identity out of a change; leaving a conversation is not offered.
func (c *Client) refuseAttendee(op string, item rawParticipant) error {
	if kind, err := item.ParticipantType.Int64(); err == nil && kind == participantOwner {
		return &provider.Error{Class: provider.ClassPermission, Op: op, Message: messageTalkOwner}
	}
	if item.ActorType == "users" && strings.EqualFold(item.ActorID, c.user) {
		return &provider.Error{Class: provider.ClassPermission, Op: op, Message: messageTalkSelf}
	}
	return nil
}

// RemoveParticipant removes one participant listed in the bound conversation.
func (c *Client) RemoveParticipant(ctx context.Context, token, id string) (*RemoveParticipantResult, error) {
	const op = "remove talk participant"
	if !validTalkToken(token) || !validAttendeeID(id) {
		return nil, providerError(op, "the participant arguments are unusable")
	}
	item, err := c.attendee(ctx, op, token, id)
	if err != nil {
		return nil, err
	}
	if err := c.refuseAttendee(op, item); err != nil {
		return nil, err
	}
	_, err = c.ocsSend(ctx, op, http.MethodDelete, ocsRequest{app: ocsTalkRooms, suffix: []string{token, "attendees"}},
		url.Values{"attendeeId": {id}}, uncertainTalkRemove)
	if err != nil {
		return nil, talkParticipantError(op, err, talkRemoveErrors)
	}
	return &RemoveParticipantResult{Removed: true, AttendeeID: id}, nil
}

// SetModerator gives or takes the moderator right of one user listed in the bound conversation.
func (c *Client) SetModerator(ctx context.Context, token, id string, moderator bool) (*ModeratorResult, error) {
	const op = "set talk moderator"
	if !validTalkToken(token) || !validAttendeeID(id) {
		return nil, providerError(op, "the participant arguments are unusable")
	}
	item, err := c.attendee(ctx, op, token, id)
	if err != nil {
		return nil, err
	}
	if item.ActorType != "users" {
		return nil, &provider.Error{Class: provider.ClassProviderError, Op: op, Message: messageTalkNotUser}
	}
	if err := c.refuseAttendee(op, item); err != nil {
		return nil, err
	}
	method := http.MethodPost
	if !moderator {
		method = http.MethodDelete
	}
	_, err = c.ocsSend(ctx, op, method, ocsRequest{app: ocsTalkRooms, suffix: []string{token, "moderators"}},
		url.Values{"attendeeId": {id}}, uncertainTalkModerator)
	if err != nil {
		return nil, talkParticipantError(op, err, talkModeratorErrors)
	}
	return &ModeratorResult{AttendeeID: id, Moderator: moderator}, nil
}

const (
	attendeeIDSchema = `{"type":"string","pattern":"^[0-9]{1,18}$"}`
)

var (
	attendeeIDArgument = capability.Argument{Name: "attendee_id", Required: true,
		Description: "Numeric attendee_id of a participant, as reported by talkparticipants.list for the same conversation"}
)

var talkParticipantsAdd = capability.Descriptor{
	ID: Provider + ".talkparticipants.add", Version: 1, Title: "Add a Nextcloud Talk participant",
	Description: "Add an existing user or group of the instance to a Talk conversation the connection binds. " +
		"Guests, e-mail addresses, phone numbers, federated users, and teams are not added. An unclear outcome " +
		"may have changed the conversation, so check talkparticipants.list before repeating",
	Tags:     []string{"nextcloud", "talk", "participant", "add", "ocs"},
	Provider: Provider, Risk: talkWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"token":` + talkTokenSchema + `,"participant":` +
		`{"type":"string","pattern":"^[A-Za-z0-9_.@' -]{1,64}$"},"source":{"type":"string","enum":["users","groups"]}},` +
		`"required":["token","participant","source"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"added":{"type":"boolean"},"participant":{"type":"string"},` +
		`"source":{"type":"string"}},"required":["added","participant","source"],"additionalProperties":false}`),
	Arguments: []capability.Argument{talkTokenArgument,
		{Name: "participant", Required: true, Description: "ID of the user or group to add, 1 to 64 characters"},
		{Name: "source", Required: true, Description: "users or groups"}},
	Fields: []capability.Field{{Name: "added", Description: "True when Nextcloud accepted the participant"},
		{Name: "participant", Description: "The ID that was added"}, {Name: "source", Description: "users or groups"}},
	Examples: []capability.Example{{Description: "Add a user",
		Arguments: json.RawMessage(`{"token":"abcd1234","participant":"bob","source":"users"}`)}},
}

var talkParticipantsRemove = capability.Descriptor{
	ID: Provider + ".talkparticipants.remove", Version: 1, Title: "Remove a Nextcloud Talk participant",
	Description: "Remove one participant listed in a Talk conversation the connection binds. The owner and this " +
		"identity are refused. An unclear outcome may have changed the conversation, so check talkparticipants.list " +
		"before repeating",
	Tags:     []string{"nextcloud", "talk", "participant", "remove", "ocs"},
	Provider: Provider, Risk: talkWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"token":` + talkTokenSchema + `,"attendee_id":` +
		attendeeIDSchema + `},"required":["token","attendee_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"removed":{"type":"boolean"},"attendee_id":{"type":"string"}},` +
		`"required":["removed","attendee_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{talkTokenArgument, attendeeIDArgument},
	Fields: []capability.Field{{Name: "removed", Description: "True when Nextcloud removed the participant"},
		{Name: "attendee_id", Description: "ID of the removed participant"}},
	Examples: []capability.Example{{Description: "Remove a participant",
		Arguments: json.RawMessage(`{"token":"abcd1234","attendee_id":"12"}`)}},
	RequiresToolAllowList: true,
}

var talkParticipantsModerator = capability.Descriptor{
	ID: Provider + ".talkparticipants.moderator", Version: 1, Title: "Set a Nextcloud Talk moderator",
	Description: "Give or take the moderator right of one user listed in a Talk conversation the connection binds. " +
		"The owner and this identity are refused. An unclear outcome may have changed the conversation, so check " +
		"talkparticipants.list before repeating",
	Tags:     []string{"nextcloud", "talk", "participant", "moderator", "ocs"},
	Provider: Provider, Risk: talkWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"token":` + talkTokenSchema + `,"attendee_id":` +
		attendeeIDSchema + `,"moderator":{"type":"boolean"}},"required":["token","attendee_id","moderator"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"attendee_id":{"type":"string"},"moderator":{"type":"boolean"}},` +
		`"required":["attendee_id","moderator"],"additionalProperties":false}`),
	Arguments: []capability.Argument{talkTokenArgument, attendeeIDArgument,
		{Name: "moderator", Required: true, Description: "True to give the moderator right, false to take it"}},
	Fields: []capability.Field{{Name: "attendee_id", Description: "ID of the participant"},
		{Name: "moderator", Description: "True when given, false when taken"}},
	Examples: []capability.Example{{Description: "Make a participant moderator",
		Arguments: json.RawMessage(`{"token":"abcd1234","attendee_id":"12","moderator":true}`)}},
	RequiresToolAllowList: true,
}

func invokeTalkParticipantsAdd(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "add talk participant"
	input, err := readTalkWrite(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	if !validParticipant(input.Participant, input.Source) {
		return nil, providerError(op, "the participant arguments are unusable")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.AddParticipant(ctx, input.Token, input.Participant, input.Source)
}

func invokeTalkParticipantsRemove(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "remove talk participant"
	input, err := readTalkWrite(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	if !validAttendeeID(input.AttendeeID) {
		return nil, providerError(op, "the participant arguments are unusable")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.RemoveParticipant(ctx, input.Token, input.AttendeeID)
}

func invokeTalkParticipantsModerator(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set talk moderator"
	input, err := readTalkWrite(op, resolved, raw)
	if err != nil {
		return nil, err
	}
	if !validAttendeeID(input.AttendeeID) || input.Moderator == nil {
		return nil, providerError(op, "the participant arguments are unusable")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.SetModerator(ctx, input.Token, input.AttendeeID, *input.Moderator)
}
