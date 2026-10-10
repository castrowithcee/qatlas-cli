package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Managing conversations is limited to internal ones: a public conversation is never created, and one that
// became public is neither changed nor deleted. Talk numbers the types of a conversation; only these two
// are ever sent or accepted.
const (
	roomTypeOneToOne = 1
	roomTypeGroup    = 2

	maxRoomName            = 255
	maxRoomDescriptionText = 2000

	roomKindOneToOne = "one-to-one"
	roomKindGroup    = "group"
)

// A Nextcloud user ID is made of letters, digits, and _ . @ - '.
var inviteUserPattern = regexp.MustCompile(`^[A-Za-z0-9_.@'-]{1,64}$`)

const (
	messageTalkNeedsAllRooms = "creating a Talk conversation requires a connection bound to all Talk conversations (target talk)"
	messageTalkNotModerator  = "this identity is not an owner or moderator of this Talk conversation"
	messageTalkNotInternal   = "this Talk conversation is not an internal conversation that Qatlas manages"
	messageTalkNoUser        = "the user to invite does not exist or is not accessible to this identity"

	uncertainRoomCreate = "; the conversation may have been created, check talkrooms.list before repeating"
	uncertainRoomUpdate = "; the conversation may have been changed, check talkrooms.get before repeating"
	uncertainRoomDelete = "; the conversation may have been deleted, check talkrooms.list before repeating"
)

var (
	talkRoomCreateErrors = map[int]string{
		http.StatusBadRequest: "Talk refused to create this conversation (for example the name or the user is unusable)",
		http.StatusForbidden:  "this identity may not create this Talk conversation",
	}
	talkRoomUpdateErrors = map[int]string{
		http.StatusBadRequest: "Talk refused this change (for example the value is not accepted for this conversation)",
		http.StatusForbidden:  "this identity may not change this Talk conversation",
	}
	talkRoomDeleteErrors = map[int]string{
		http.StatusBadRequest: "Talk refused to delete this conversation",
		http.StatusForbidden:  "this identity may not delete this Talk conversation",
	}
)

// RoomUpdateResult reports the one changed property of a conversation.
type RoomUpdateResult struct {
	Updated bool   `json:"updated"`
	Token   string `json:"token"`
	Field   string `json:"field"`
}

// RoomDeleteResult reports a deleted conversation.
type RoomDeleteResult struct {
	Deleted bool   `json:"deleted"`
	Token   string `json:"token"`
}

type rawRoomState struct {
	Token           string      `json:"token"`
	Type            json.Number `json:"type"`
	ParticipantType json.Number `json:"participantType"`
	ObjectType      string      `json:"objectType"`
}

func validRoomName(name string) bool {
	return strings.TrimSpace(name) != "" && utf8.ValidString(name) && utf8.RuneCountInString(name) <= maxRoomName
}

func validRoomDescription(text string) bool {
	return utf8.ValidString(text) && utf8.RuneCountInString(text) <= maxRoomDescriptionText
}

// validRoomCreate requires the one property each kind needs and none of the other.
func validRoomCreate(kind string, hasName, hasUser bool, name, user string) bool {
	switch kind {
	case roomKindOneToOne:
		return !hasName && inviteUserPattern.MatchString(user)
	case roomKindGroup:
		return !hasUser && validRoomName(name)
	}
	return false
}

// talkRoomError gives a refusal a fixed message that fits Talk; it never carries provider text.
func talkRoomError(op string, err error, messages map[int]string, notFound string) error {
	return talkNotFound(op, tagAdminError(op, err, messages), notFound)
}

// managedRoom is the one read before a change. It requires an owner or moderator of an ordinary
// conversation of an internal type; the answer is the same for every refusal and never names the token.
func (c *Client) managedRoom(ctx context.Context, op, token string, types ...int64) error {
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsTalkRooms, suffix: []string{token},
		query: url.Values{"noStatusUpdate": {"1"}}})
	if err != nil {
		return talkNotFound(op, err, messageTalkNoRoom)
	}
	var raw rawRoomState
	if err := json.Unmarshal(data, &raw); err != nil || raw.Token != token {
		return invalidResponse(op, "the Nextcloud conversation could not be read")
	}
	if role, _ := raw.ParticipantType.Int64(); role != 1 && role != 2 {
		return &provider.Error{Class: provider.ClassPermission, Op: op, Message: messageTalkNotModerator}
	}
	kind, _ := raw.Type.Int64()
	for _, allowed := range types {
		if kind == allowed && raw.ObjectType == "" {
			return nil
		}
	}
	return &provider.Error{Class: provider.ClassPermission, Op: op, Message: messageTalkNotInternal}
}

// CreateRoom creates one internal conversation after a single capability read: a one-to-one conversation
// with an existing user, or a group conversation with a name and no invited participants.
func (c *Client) CreateRoom(ctx context.Context, kind, name, user string) (*Room, error) {
	const op = "create talk conversation"
	if !validRoomCreate(kind, name != "", user != "", name, user) {
		return nil, providerError(op, "the conversation arguments are unusable")
	}
	form := url.Values{"roomType": {"2"}, "roomName": {name}}
	if kind == roomKindOneToOne {
		form = url.Values{"roomType": {"1"}, "invite": {user}, "source": {"users"}}
	}
	if err := c.talkFeature(ctx, op, featureConversations); err != nil {
		return nil, err
	}
	data, err := c.ocsSend(ctx, op, http.MethodPost, ocsRequest{app: ocsTalkRooms}, form, uncertainRoomCreate)
	if err != nil {
		return nil, talkRoomError(op, err, talkRoomCreateErrors, messageTalkNoUser)
	}
	var raw rawRoom
	if err := json.Unmarshal(data, &raw); err != nil || !validTalkToken(raw.Token) {
		return nil, withUncertainty(invalidResponse(op, "the Nextcloud conversation could not be read"), uncertainRoomCreate)
	}
	room := roomOf(raw)
	return &room, nil
}

// UpdateRoom changes the name or the description of a conversation the identity moderates. Exactly one
// property is changed per call, because each is its own request.
func (c *Client) UpdateRoom(ctx context.Context, token string, name, description *string) (*RoomUpdateResult, error) {
	const op = "update talk conversation"
	if !validTalkToken(token) || (name == nil) == (description == nil) ||
		name != nil && !validRoomName(*name) || description != nil && !validRoomDescription(*description) {
		return nil, providerError(op, "the conversation arguments are unusable")
	}
	if err := c.managedRoom(ctx, op, token, roomTypeGroup); err != nil {
		return nil, err
	}
	request := ocsRequest{app: ocsTalkRooms, suffix: []string{token}}
	form, field := url.Values{}, "name"
	if name != nil {
		form.Set("roomName", *name)
	} else {
		request.suffix = append(request.suffix, "description")
		form.Set("description", *description)
		field = "description"
	}
	if _, err := c.ocsSend(ctx, op, http.MethodPut, request, form, uncertainRoomUpdate); err != nil {
		return nil, talkRoomError(op, err, talkRoomUpdateErrors, messageTalkNoRoom)
	}
	return &RoomUpdateResult{Updated: true, Token: token, Field: field}, nil
}

// DeleteRoom deletes a conversation the identity moderates for all of its participants.
func (c *Client) DeleteRoom(ctx context.Context, token string) (*RoomDeleteResult, error) {
	const op = "delete talk conversation"
	if !validTalkToken(token) {
		return nil, providerError(op, "a conversation token is unusable")
	}
	if err := c.managedRoom(ctx, op, token, roomTypeOneToOne, roomTypeGroup); err != nil {
		return nil, err
	}
	if _, err := c.ocsSend(ctx, op, http.MethodDelete, ocsRequest{app: ocsTalkRooms, suffix: []string{token}}, nil,
		uncertainRoomDelete); err != nil {
		return nil, talkRoomError(op, err, talkRoomDeleteErrors, messageTalkNoRoom)
	}
	return &RoomDeleteResult{Deleted: true, Token: token}, nil
}

const (
	roomNameSchema        = `{"type":"string","minLength":1,"maxLength":255}`
	roomDescriptionSchema = `{"type":"string","maxLength":2000}`
	inviteUserSchema      = `{"type":"string","pattern":"^[A-Za-z0-9_.@'-]{1,64}$"}`
)

var (
	talkRoomsCreate = capability.Descriptor{
		ID: Provider + ".talkrooms.create", Version: 1, Title: "Create a Nextcloud Talk conversation",
		Description: "Create an internal Talk conversation: a one-to-one conversation with an existing user, or a group " +
			"conversation with a name and no participants besides this identity. Public conversations are never " +
			"created. Needs a connection bound to all Talk conversations. After an unclear outcome the conversation " +
			"may exist, so check talkrooms.list before creating again; a rate limit is reported as such and not repeated",
		Tags:     []string{"nextcloud", "talk", "chat", "create", "ocs"},
		Provider: Provider, Risk: talkWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
		InputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string","enum":["one-to-one","group"]},` +
			`"name":` + roomNameSchema + `,"invite":` + inviteUserSchema + `},"required":["kind"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(roomSchema),
		Arguments: []capability.Argument{
			{Name: "kind", Required: true, Description: "one-to-one or group"},
			{Name: "name", Description: "Name of the group conversation, 1 to 255 characters; required for group, not allowed for one-to-one"},
			{Name: "invite", Description: "User ID of an existing user to talk to; required for one-to-one, not allowed for group"},
		},
		Fields:   roomFields,
		Examples: []capability.Example{{Description: "Create a group", Arguments: json.RawMessage(`{"kind":"group","name":"Project"}`)}},
	}

	talkRoomsUpdate = capability.Descriptor{
		ID: Provider + ".talkrooms.update", Version: 1, Title: "Change a Nextcloud Talk conversation",
		Description: "Rename a group conversation or change its description, as owner or moderator of a conversation " +
			"the connection binds; give exactly one of name and description per call. Public conversations are not changed",
		Tags:     []string{"nextcloud", "talk", "chat", "update", "ocs"},
		Provider: Provider, Risk: talkWriteRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
		InputSchema: json.RawMessage(`{"type":"object","properties":{"token":` + talkTokenSchema + `,"name":` + roomNameSchema +
			`,"description":` + roomDescriptionSchema + `},"required":["token"],"minProperties":2,"maxProperties":2,` +
			`"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"},"token":{"type":"string"},` +
			`"field":{"type":"string","enum":["name","description"]}},"required":["updated","token","field"],"additionalProperties":false}`),
		Arguments: []capability.Argument{talkTokenArgument,
			{Name: "name", Description: "New name, 1 to 255 characters; exclusive with description"},
			{Name: "description", Description: "New description, at most 2000 characters, empty to clear it; exclusive with name"}},
		Fields: []capability.Field{{Name: "updated", Description: "True when Nextcloud applied the change"},
			{Name: "token", Description: "Token of the conversation"}, {Name: "field", Description: "name or description"}},
		Examples: []capability.Example{{Description: "Rename a conversation",
			Arguments: json.RawMessage(`{"token":"abcd1234","name":"Project Atlas"}`)}},
	}

	talkRoomsDelete = capability.Descriptor{
		ID: Provider + ".talkrooms.delete", Version: 1, Title: "Delete a Nextcloud Talk conversation",
		Description: "Delete a one-to-one or group conversation with all of its messages for every participant, as " +
			"owner or moderator of a conversation the connection binds. Public conversations are not deleted",
		Tags:     []string{"nextcloud", "talk", "chat", "delete", "ocs"},
		Provider: Provider, Risk: talkWriteRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
		InputSchema: json.RawMessage(`{"type":"object","properties":{"token":` + talkTokenSchema +
			`},"required":["token"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"},"token":{"type":"string"}},` +
			`"required":["deleted","token"],"additionalProperties":false}`),
		Arguments: []capability.Argument{talkTokenArgument},
		Fields: []capability.Field{{Name: "deleted", Description: "True when Nextcloud deleted the conversation"},
			{Name: "token", Description: "Token of the deleted conversation"}},
		Examples:              []capability.Example{{Description: "Delete a conversation", Arguments: json.RawMessage(`{"token":"abcd1234"}`)}},
		RequiresToolAllowList: true,
	}
)

type talkRoomArguments struct {
	Token       string  `json:"token"`
	Kind        string  `json:"kind"`
	Name        *string `json:"name"`
	User        *string `json:"invite"`
	Description *string `json:"description"`
}

func readTalkRoom(op string, raw json.RawMessage) (talkRoomArguments, error) {
	var input talkRoomArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

func invokeTalkRoomsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create talk conversation"
	input, err := readTalkRoom(op, raw)
	if err != nil {
		return nil, err
	}
	talks, err := requireTalk(resolved)
	if err != nil {
		return nil, err
	}
	if !talks.all {
		return nil, &provider.Error{Class: provider.ClassPermission, Op: "open", Message: messageTalkNeedsAllRooms}
	}
	var name, user string
	if input.Name != nil {
		name = *input.Name
	}
	if input.User != nil {
		user = *input.User
	}
	if input.Token != "" || input.Description != nil || !validRoomCreate(input.Kind, input.Name != nil, input.User != nil, name, user) {
		return nil, providerError(op, "the conversation arguments are unusable")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.CreateRoom(ctx, input.Kind, name, user)
}

func invokeTalkRoomsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update talk conversation"
	input, err := readTalkRoom(op, raw)
	if err != nil {
		return nil, err
	}
	if err := requireTalkToken(resolved, input.Token); err != nil {
		return nil, err
	}
	if input.Kind != "" || input.User != nil || (input.Name == nil) == (input.Description == nil) ||
		input.Name != nil && !validRoomName(*input.Name) || input.Description != nil && !validRoomDescription(*input.Description) {
		return nil, providerError(op, "the conversation arguments are unusable")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.UpdateRoom(ctx, input.Token, input.Name, input.Description)
}

func invokeTalkRoomsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete talk conversation"
	input, err := readTalkRoom(op, raw)
	if err != nil {
		return nil, err
	}
	if err := requireTalkToken(resolved, input.Token); err != nil {
		return nil, err
	}
	if input.Kind != "" || input.Name != nil || input.User != nil || input.Description != nil {
		return nil, providerError(op, "the conversation arguments are unusable")
	}
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		return nil, err
	}
	return client.DeleteRoom(ctx, input.Token)
}
