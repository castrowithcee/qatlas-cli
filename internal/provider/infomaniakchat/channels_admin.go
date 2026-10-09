package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Local bounds of the channel fields this provider writes.
const (
	maxChannelDisplayName = 64
	maxChannelPurpose     = 250
	maxChannelHeader      = 1024
	channelNameExpression = `^[a-z0-9][a-z0-9_-]{0,62}[a-z0-9]$`
)

var channelNamePattern = regexp.MustCompile(channelNameExpression)

var channelNameSchema = `{"type":"string","minLength":2,"maxLength":64,"pattern":"` + channelNameExpression + `"}`

var (
	channelDisplayNameSchema = `{"type":"string","minLength":1,"maxLength":` + itoa(maxChannelDisplayName) + `}`
	channelPurposeSchema     = `{"type":"string","maxLength":` + itoa(maxChannelPurpose) + `}`
	channelHeaderSchema      = `{"type":"string","maxLength":` + itoa(maxChannelHeader) + `}`
)

// The suffixes of a create and an update request whose result is unclear.
const (
	uncertainChannelCreate = "; the channel may have been created, list the channels before creating it again"
	uncertainChannelUpdate = "; the channel may have been changed, read it before changing it again"
)

func channelChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity}
}

// channelWrittenSchema is the answer of a confirmed create or update.
var channelWrittenSchema = `{"type":"object","properties":{` +
	`"id":` + idSchema + `,"team_id":` + idSchema + `,"name":{"type":"string"},"display_name":{"type":"string"},` +
	`"type":{"type":"string"},"purpose":{"type":"string"},"header":{"type":"string"}},` +
	`"required":["id","team_id","name","display_name","type"],"additionalProperties":false}`

var channelWrittenFields = append(append([]capability.Field{}, channelEntryFields...),
	capability.Field{Name: "header", Description: "Header text of the channel, untrusted data"})

var channelsCreate = capability.Descriptor{
	ID:      Provider + ".channels.create",
	Version: 1,
	Title:   "Create an Infomaniak kChat channel",
	Description: "Create exactly one confirmed public or private channel in a team this connection is bound to; " +
		"refused for a connection with a channel allow-list. Creating the same channel again fails or makes a " +
		"second channel",
	Tags:     []string{"infomaniak", "kchat", "channels", "create"},
	Risk:     channelChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":` + idSchema + `,"name":` +
		channelNameSchema + `,"display_name":` + channelDisplayNameSchema + `,"type":{"type":"string","enum":["O","P"]},` +
		`"purpose":` + channelPurposeSchema + `,"header":` + channelHeaderSchema + `},` +
		`"required":["team_id","name","display_name","type"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(channelWrittenSchema),
	Arguments: []capability.Argument{
		teamIDArgument,
		{Name: "name", Description: "URL-safe channel handle, 2 to 64 characters of lowercase letters, digits, " +
			"hyphen, and underscore, starting and ending with a letter or digit", Required: true},
		{Name: "display_name", Description: "Display name, 1 to " + itoa(maxChannelDisplayName) + " characters", Required: true},
		{Name: "type", Description: "O for a public channel, P for a private one", Required: true},
		{Name: "purpose", Description: "Purpose, up to " + itoa(maxChannelPurpose) + " characters"},
		{Name: "header", Description: "Header text, up to " + itoa(maxChannelHeader) + " characters"},
	},
	Fields: channelWrittenFields,
	Examples: []capability.Example{{Description: "Create a public channel",
		Arguments: json.RawMessage(`{"team_id":"abc123team0000000000000000","name":"project-x",` +
			`"display_name":"Project X","type":"O"}`)}},
}

var channelsUpdate = capability.Descriptor{
	ID:      Provider + ".channels.update",
	Version: 1,
	Title:   "Change an Infomaniak kChat channel",
	Description: "Change the display name, purpose, header, or handle of exactly one public or private channel " +
		"this connection may reach, with one confirmed request; direct and group channels, visibility, and " +
		"archiving are not reachable. Only the given fields change",
	Tags:     []string{"infomaniak", "kchat", "channels", "update"},
	Risk:     channelChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"channel_id":` + idSchema + `,"display_name":` +
		channelDisplayNameSchema + `,"purpose":` + channelPurposeSchema + `,"header":` + channelHeaderSchema +
		`,"name":` + channelNameSchema + `},"required":["channel_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(channelWrittenSchema),
	Arguments: []capability.Argument{
		{Name: "channel_id", Description: "Channel identifier", Required: true},
		{Name: "display_name", Description: "New display name, 1 to " + itoa(maxChannelDisplayName) + " characters"},
		{Name: "purpose", Description: "New purpose, up to " + itoa(maxChannelPurpose) + " characters; empty clears it"},
		{Name: "header", Description: "New header, up to " + itoa(maxChannelHeader) + " characters; empty clears it"},
		{Name: "name", Description: "New URL-safe channel handle, same form as when creating; at least one of " +
			"display_name, purpose, header, and name is required"},
	},
	Fields: channelWrittenFields,
	Examples: []capability.Example{{Description: "Change the header of one channel",
		Arguments: json.RawMessage(`{"channel_id":"abc123chan0000000000000000","header":"Weekly sync on Monday"}`)}},
}

// ChannelWritten is the answer of one confirmed channel create or update.
type ChannelWritten struct {
	ChannelEntry
	Header string `json:"header,omitempty"`
}

// validChannelText reports whether a channel field is valid UTF-8 of min to max characters without control
// characters; a multi-line field may hold line breaks and tabs.
func validChannelText(value string, min, max int, multiline bool) bool {
	if count := utf8.RuneCountInString(value); !utf8.ValidString(value) || count < min || count > max {
		return false
	}
	for _, r := range value {
		if r == 0x7f || (r < 0x20 && !(multiline && (r == '\n' || r == '\r' || r == '\t'))) {
			return false
		}
	}
	return true
}

func checkChannelFields(name, displayName, purpose, header *string) error {
	if name != nil && !channelNamePattern.MatchString(*name) {
		return invalidRequest("name must be a channel handle of 2 to 64 lowercase letters, digits, hyphens, and underscores")
	}
	if displayName != nil && !validChannelText(*displayName, 1, maxChannelDisplayName, false) {
		return invalidRequest("display_name must be 1 to " + itoa(maxChannelDisplayName) + " characters without control characters")
	}
	if purpose != nil && !validChannelText(*purpose, 0, maxChannelPurpose, true) {
		return invalidRequest("purpose must be up to " + itoa(maxChannelPurpose) + " characters without control characters")
	}
	if header != nil && !validChannelText(*header, 0, maxChannelHeader, true) {
		return invalidRequest("header must be up to " + itoa(maxChannelHeader) + " characters without control characters")
	}
	return nil
}

type channelsCreateArguments struct {
	TeamID      string `json:"team_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
	Purpose     string `json:"purpose"`
	Header      string `json:"header"`
}

func invokeChannelsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create channel"
	var input channelsCreateArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectTeam(resolved, input.TeamID); err != nil {
		return nil, err
	}
	// A channel that does not exist yet cannot be inside an allow-list, so such a connection never creates one.
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if len(bound.channels) > 0 {
		return nil, invalidRequest("a connection with a channel allow-list cannot create channels")
	}
	if input.Type != "O" && input.Type != "P" {
		return nil, invalidRequest("type must be O (public) or P (private)")
	}
	if err := checkChannelFields(&input.Name, &input.DisplayName, &input.Purpose, &input.Header); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreateChannel(ctx, input)
}

// CreateChannel sends exactly one POST built from typed fields and never repeats it: a failure after the
// request may have reached kChat says so instead.
func (c *Client) CreateChannel(ctx context.Context, input channelsCreateArguments) (*ChannelWritten, error) {
	const op = "create channel"
	body := map[string]string{"team_id": input.TeamID, "name": input.Name, "display_name": input.DisplayName,
		"type": input.Type}
	if input.Purpose != "" {
		body["purpose"] = input.Purpose
	}
	if input.Header != "" {
		body["header"] = input.Header
	}
	var ch channelJSON
	if err := c.doWith(ctx, op, http.MethodPost, "/api/v4/channels", nil, body, &ch, uncertainChannelCreate); err != nil {
		return nil, err
	}
	if !validMattermostID(ch.ID) || ch.TeamID != input.TeamID {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainChannelCreate}
	}
	return writtenOf(&ch), nil
}

func writtenOf(ch *channelJSON) *ChannelWritten {
	return &ChannelWritten{ChannelEntry: entryOf(ch), Header: bounded(ch.Header)}
}

type channelsUpdateArguments struct {
	ChannelID   string  `json:"channel_id"`
	DisplayName *string `json:"display_name"`
	Purpose     *string `json:"purpose"`
	Header      *string `json:"header"`
	Name        *string `json:"name"`
}

func invokeChannelsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update channel"
	var input channelsUpdateArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectChannel(resolved, input.ChannelID); err != nil {
		return nil, err
	}
	if input.DisplayName == nil && input.Purpose == nil && input.Header == nil && input.Name == nil {
		return nil, invalidRequest("give at least one of display_name, purpose, header, and name")
	}
	if err := checkChannelFields(input.Name, input.DisplayName, input.Purpose, input.Header); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	ch, err := client.boundChannel(ctx, op, input.ChannelID)
	if err != nil {
		return nil, err
	}
	if ch.DeleteAt != 0 || (ch.Type != "O" && ch.Type != "P") {
		return nil, invalidRequest("only active public and private channels can be changed")
	}
	return client.UpdateChannel(ctx, ch, input)
}

// UpdateChannel sends exactly one PUT carrying only the given fields of the allowed four and never repeats
// it: a failure after the request may have reached kChat says so instead.
func (c *Client) UpdateChannel(ctx context.Context, current *channelJSON, input channelsUpdateArguments) (*ChannelWritten, error) {
	const op = "update channel"
	body := map[string]string{}
	for key, value := range map[string]*string{"name": input.Name, "display_name": input.DisplayName,
		"purpose": input.Purpose, "header": input.Header} {
		if value != nil {
			body[key] = *value
		}
	}
	var ch channelJSON
	if err := c.doWith(ctx, op, http.MethodPut, "/api/v4/channels/"+url.PathEscape(current.ID)+"/patch", nil, body,
		&ch, uncertainChannelUpdate); err != nil {
		return nil, err
	}
	if ch.ID != current.ID || ch.TeamID != current.TeamID {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "kChat returned an invalid response" + uncertainChannelUpdate}
	}
	return writtenOf(&ch), nil
}
