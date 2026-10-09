package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	uncertainStatus       = "; the status may have been set, read the presence before setting it again"
	uncertainCustomSet    = "; the custom status may have been set, set it again only if it is not in place"
	uncertainCustomClear  = "; the custom status may have been cleared, set a new one only if you still want it"
	uncertainProfile      = "; the profile may have been changed, read the own user before changing it again"
	maxCustomStatusText   = 100
	maxProfileNameLength  = 64
	maxProfilePositionLen = 128
	maxTimeLength         = 40
)

// clock is replaced by tests that need a fixed present.
var clock = time.Now

// presenceStatuses are the manual presence values kChat accepts; any other value is refused locally.
var presenceStatuses = []string{"online", "away", "dnd", "offline"}

// customDurations are the expiry choices of the kChat client; only date_and_time takes an explicit time and
// kChat itself computes the end of every other choice.
var customDurations = []string{"thirty_minutes", "one_hour", "four_hours", "today", "this_week", "date_and_time"}

const selfScope = "for the credential holder in every team of the instance (for the bot with a bot token), "

func selfChangeRisk() capability.Risk { return messageChangeRisk(capability.EffectUpdate) }

func quoted(values []string) string {
	return `"` + strings.Join(values, `","`) + `"`
}

var customEmojiSchema = `{"type":"string","maxLength":` + itoa(maxEmojiNameLength) + `,"pattern":"^[a-z0-9_+-]{1,` +
	itoa(maxEmojiNameLength) + `}$"}`

var statusSet = capability.Descriptor{
	ID:      Provider + ".status.set",
	Version: 1,
	Title:   "Set the own Infomaniak kChat presence",
	Description: "Set exactly one confirmed presence status (online, away, dnd, or offline) " + selfScope +
		"never of another user; dnd may carry an end time",
	Tags:     []string{"infomaniak", "kchat", "users", "status", "presence", "set"},
	Risk:     selfChangeRisk(),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":[` +
		quoted(presenceStatuses) + `]},"dnd_end_time":{"type":"string","maxLength":` + itoa(maxTimeLength) + `}},` +
		`"required":["status"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":` + idSchema + `,` +
		`"status":{"type":"string"},"dnd_end_time":{"type":"string"}},` +
		`"required":["user_id","status"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "status", Required: true, Description: "One of " + strings.Join(presenceStatuses, ", ")},
		{Name: "dnd_end_time", Description: "Only with dnd: RFC 3339 time in the future at which dnd ends"},
	},
	Fields: []capability.Field{
		{Name: "user_id", Description: "The token's own user"},
		{Name: "status", Description: "Presence status that was set"},
		{Name: "dnd_end_time", Description: "End of dnd as RFC 3339 in UTC, when one was set"},
	},
	Examples: []capability.Example{{Description: "Go away",
		Arguments: json.RawMessage(`{"status":"away"}`)}},
}

var customStatusSet = capability.Descriptor{
	ID:      Provider + ".customstatus.set",
	Version: 1,
	Title:   "Set the own Infomaniak kChat custom status",
	Description: "Set exactly one confirmed custom status (emoji and text, optionally expiring) " + selfScope +
		"never of another user; it replaces the current custom status",
	Tags:     []string{"infomaniak", "kchat", "users", "status", "custom", "set"},
	Risk:     selfChangeRisk(),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"emoji":` + customEmojiSchema + `,` +
		`"text":{"type":"string","maxLength":` + itoa(maxCustomStatusText) + `},` +
		`"duration":{"type":"string","enum":[` + quoted(customDurations) + `]},` +
		`"expires_at":{"type":"string","maxLength":` + itoa(maxTimeLength) + `}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":` + idSchema + `,` +
		`"emoji":{"type":"string"},"text":{"type":"string"},"duration":{"type":"string"},` +
		`"expires_at":{"type":"string"}},"required":["user_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "emoji", Description: "Emoji name without colons, 1 to " + itoa(maxEmojiNameLength) +
			" characters of lowercase letters, digits, underscore, plus, or hyphen"},
		{Name: "text", Description: "Status text of at most " + itoa(maxCustomStatusText) + " characters"},
		{Name: "duration", Description: "When the status ends, one of " + strings.Join(customDurations, ", ") +
			"; without it the status never expires"},
		{Name: "expires_at", Description: "Only with duration date_and_time, and required then: RFC 3339 time " +
			"in the future"},
	},
	Fields: []capability.Field{
		{Name: "user_id", Description: "The token's own user"},
		{Name: "emoji", Description: "Emoji that was set, when one was given"},
		{Name: "text", Description: "Text that was set, when one was given"},
		{Name: "duration", Description: "Duration that was set, when one was given"},
		{Name: "expires_at", Description: "Expiry as RFC 3339 in UTC, when one was given"},
	},
	Examples: []capability.Example{{Description: "Set a status for one hour",
		Arguments: json.RawMessage(`{"emoji":"calendar","text":"In a meeting","duration":"one_hour"}`)}},
}

var customStatusClear = capability.Descriptor{
	ID:      Provider + ".customstatus.clear",
	Version: 1,
	Title:   "Clear the own Infomaniak kChat custom status",
	Description: "Clear exactly one confirmed custom status " + selfScope + "never of another user; it can be " +
		"set again",
	Tags:        []string{"infomaniak", "kchat", "users", "status", "custom", "clear"},
	Risk:        selfChangeRisk(),
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":` + idSchema + `,` +
		`"cleared":{"type":"boolean"}},"required":["user_id","cleared"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "user_id", Description: "The token's own user"},
		{Name: "cleared", Description: "True once kChat confirmed the removal"},
	},
	Examples: []capability.Example{{Description: "Clear the custom status", Arguments: json.RawMessage(`{}`)}},
}

var profileUpdate = capability.Descriptor{
	ID:      Provider + ".profile.update",
	Version: 1,
	Title:   "Update the own Infomaniak kChat profile",
	Description: "Change exactly one confirmed set of the display fields nickname, first_name, last_name, and " +
		"position " + selfScope + "never of another user; other profile fields are never sent",
	Tags:     []string{"infomaniak", "kchat", "users", "profile", "update"},
	Risk:     selfChangeRisk(),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"nickname":{"type":"string","maxLength":` + itoa(maxProfileNameLength) + `},` +
		`"first_name":{"type":"string","maxLength":` + itoa(maxProfileNameLength) + `},` +
		`"last_name":{"type":"string","maxLength":` + itoa(maxProfileNameLength) + `},` +
		`"position":{"type":"string","maxLength":` + itoa(maxProfilePositionLen) + `}},` +
		`"minProperties":1,"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"user_id":` + idSchema + `,` +
		`"nickname":{"type":"string"},"first_name":{"type":"string"},"last_name":{"type":"string"},` +
		`"position":{"type":"string"}},"required":["user_id"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "nickname", Description: "New nickname of at most " + itoa(maxProfileNameLength) +
			" characters; empty clears it"},
		{Name: "first_name", Description: "New first name of at most " + itoa(maxProfileNameLength) +
			" characters; empty clears it"},
		{Name: "last_name", Description: "New last name of at most " + itoa(maxProfileNameLength) +
			" characters; empty clears it"},
		{Name: "position", Description: "New position of at most " + itoa(maxProfilePositionLen) +
			" characters; empty clears it"},
	},
	Fields: []capability.Field{
		{Name: "user_id", Description: "The token's own user"},
		{Name: "nickname", Description: "Nickname that was set, when one was given"},
		{Name: "first_name", Description: "First name that was set, when one was given"},
		{Name: "last_name", Description: "Last name that was set, when one was given"},
		{Name: "position", Description: "Position that was set, when one was given"},
	},
	Examples: []capability.Example{{Description: "Change the position",
		Arguments: json.RawMessage(`{"position":"Engineer"}`)}},
}

// StatusSet is the answer of one confirmed presence change.
type StatusSet struct {
	UserID     string `json:"user_id"`
	Status     string `json:"status"`
	DNDEndTime string `json:"dnd_end_time,omitempty"`
}

// CustomStatusSet is the answer of one confirmed custom status.
type CustomStatusSet struct {
	UserID    string `json:"user_id"`
	Emoji     string `json:"emoji,omitempty"`
	Text      string `json:"text,omitempty"`
	Duration  string `json:"duration,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// CustomStatusCleared is the answer of one confirmed removal of the custom status.
type CustomStatusCleared struct {
	UserID  string `json:"user_id"`
	Cleared bool   `json:"cleared"`
}

// ProfileUpdated carries exactly the display fields that were sent.
type ProfileUpdated struct {
	UserID    string  `json:"user_id"`
	Nickname  *string `json:"nickname,omitempty"`
	FirstName *string `json:"first_name,omitempty"`
	LastName  *string `json:"last_name,omitempty"`
	Position  *string `json:"position,omitempty"`
}

type statusArguments struct {
	Status     string `json:"status"`
	DNDEndTime string `json:"dnd_end_time"`
}

type customStatusArguments struct {
	Emoji     string `json:"emoji"`
	Text      string `json:"text"`
	Duration  string `json:"duration"`
	ExpiresAt string `json:"expires_at"`
}

type profileArguments struct {
	Nickname  *string `json:"nickname,omitempty"`
	FirstName *string `json:"first_name,omitempty"`
	LastName  *string `json:"last_name,omitempty"`
	Position  *string `json:"position,omitempty"`
}

func contains(list []string, value string) bool {
	for _, candidate := range list {
		if candidate == value {
			return true
		}
	}
	return false
}

// futureTime parses an RFC 3339 time that must lie after now.
func futureTime(name, value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, invalidRequest(name + " must be an RFC 3339 time")
	}
	if !parsed.After(clock()) {
		return time.Time{}, invalidRequest(name + " must lie in the future")
	}
	return parsed.UTC(), nil
}

func plainText(name, value string, max int) error {
	if utf8.RuneCountInString(value) > max {
		return invalidRequest(name + " must have at most " + itoa(max) + " characters")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return invalidRequest(name + " must not contain control characters")
		}
	}
	return nil
}

// selfClient validates nothing itself: callers validate locally first, then this opens the client and reads
// the own user, which is the only user these tools ever address.
func selfClient(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	op string) (*Client, string, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, "", err
	}
	own, err := client.ownUserID(ctx, op)
	if err != nil {
		return nil, "", err
	}
	return client, own, nil
}

func invalidAnswer(op, suffix string) error {
	return &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
		Message: "kChat returned an invalid response" + suffix}
}

func selfPath(own, tail string) string { return "/api/v4/users/" + url.PathEscape(own) + tail }

func invokeStatusSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set status"
	var input statusArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !contains(presenceStatuses, input.Status) {
		return nil, invalidRequest("status must be one of " + strings.Join(presenceStatuses, ", "))
	}
	var end time.Time
	if input.DNDEndTime != "" {
		if input.Status != "dnd" {
			return nil, invalidRequest("dnd_end_time is only allowed with status dnd")
		}
		parsed, err := futureTime("dnd_end_time", input.DNDEndTime)
		if err != nil {
			return nil, err
		}
		end = parsed
	}
	client, own, err := selfClient(ctx, resolved, secrets, red, op)
	if err != nil {
		return nil, err
	}
	return client.SetStatus(ctx, own, input.Status, end)
}

// SetStatus sends exactly one PUT for the own user and never repeats it.
func (c *Client) SetStatus(ctx context.Context, own, status string, dndEnd time.Time) (*StatusSet, error) {
	const op = "set status"
	body := struct {
		UserID     string `json:"user_id"`
		Status     string `json:"status"`
		DNDEndTime int64  `json:"dnd_end_time,omitempty"`
	}{UserID: own, Status: status}
	result := &StatusSet{UserID: own, Status: status}
	if !dndEnd.IsZero() {
		body.DNDEndTime = dndEnd.Unix()
		result.DNDEndTime = dndEnd.Format(time.RFC3339)
	}
	var answer struct {
		UserID string `json:"user_id"`
		Status string `json:"status"`
	}
	if err := c.doWith(ctx, op, http.MethodPut, selfPath(own, "/status"), nil, body, &answer,
		uncertainStatus); err != nil {
		return nil, err
	}
	if answer.UserID != own || answer.Status != status {
		return nil, invalidAnswer(op, uncertainStatus)
	}
	return result, nil
}

func invokeCustomStatusSet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set custom status"
	var input customStatusArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if input.Emoji == "" && input.Text == "" {
		return nil, invalidRequest("an emoji or a text is required; clear the custom status instead of emptying it")
	}
	if input.Emoji != "" && !validEmojiName(input.Emoji) {
		return nil, invalidRequest("emoji must be 1 to " + itoa(maxEmojiNameLength) +
			" characters of lowercase letters, digits, underscore, plus, or hyphen")
	}
	if err := plainText("text", input.Text, maxCustomStatusText); err != nil {
		return nil, err
	}
	if input.Duration != "" && !contains(customDurations, input.Duration) {
		return nil, invalidRequest("duration must be one of " + strings.Join(customDurations, ", "))
	}
	var expires time.Time
	switch {
	case input.Duration == "date_and_time" && input.ExpiresAt == "":
		return nil, invalidRequest("duration date_and_time requires expires_at")
	case input.Duration != "date_and_time" && input.ExpiresAt != "":
		return nil, invalidRequest("expires_at is only allowed with duration date_and_time")
	case input.ExpiresAt != "":
		parsed, err := futureTime("expires_at", input.ExpiresAt)
		if err != nil {
			return nil, err
		}
		expires = parsed
	}
	client, own, err := selfClient(ctx, resolved, secrets, red, op)
	if err != nil {
		return nil, err
	}
	return client.SetCustomStatus(ctx, own, input.Emoji, input.Text, input.Duration, expires)
}

// SetCustomStatus sends exactly one PUT for the own user and never repeats it.
func (c *Client) SetCustomStatus(ctx context.Context, own, emoji, text, duration string,
	expires time.Time) (*CustomStatusSet, error) {
	const op = "set custom status"
	body := struct {
		Emoji     string `json:"emoji,omitempty"`
		Text      string `json:"text,omitempty"`
		Duration  string `json:"duration,omitempty"`
		ExpiresAt string `json:"expires_at,omitempty"`
	}{Emoji: emoji, Text: text, Duration: duration}
	result := &CustomStatusSet{UserID: own, Emoji: emoji, Text: text, Duration: duration}
	if !expires.IsZero() {
		body.ExpiresAt = expires.Format(time.RFC3339)
		result.ExpiresAt = body.ExpiresAt
	}
	var answer struct {
		Status string `json:"status"`
	}
	if err := c.doWith(ctx, op, http.MethodPut, selfPath(own, "/status/custom"), nil, body, &answer,
		uncertainCustomSet); err != nil {
		return nil, err
	}
	if !strings.EqualFold(answer.Status, "ok") {
		return nil, invalidAnswer(op, uncertainCustomSet)
	}
	return result, nil
}

func invokeCustomStatusClear(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	const op = "clear custom status"
	client, own, err := selfClient(ctx, resolved, secrets, red, op)
	if err != nil {
		return nil, err
	}
	return client.ClearCustomStatus(ctx, own)
}

// ClearCustomStatus sends exactly one DELETE for the own user and never repeats it.
func (c *Client) ClearCustomStatus(ctx context.Context, own string) (*CustomStatusCleared, error) {
	const op = "clear custom status"
	var answer struct {
		Status string `json:"status"`
	}
	if err := c.doWith(ctx, op, http.MethodDelete, selfPath(own, "/status/custom"), nil, nil, &answer,
		uncertainCustomClear); err != nil {
		return nil, err
	}
	if !strings.EqualFold(answer.Status, "ok") {
		return nil, invalidAnswer(op, uncertainCustomClear)
	}
	return &CustomStatusCleared{UserID: own, Cleared: true}, nil
}

func invokeProfileUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update profile"
	var input profileArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if input.Nickname == nil && input.FirstName == nil && input.LastName == nil && input.Position == nil {
		return nil, invalidRequest("at least one of nickname, first_name, last_name, and position is required")
	}
	for _, field := range []struct {
		name  string
		value *string
		max   int
	}{{"nickname", input.Nickname, maxProfileNameLength}, {"first_name", input.FirstName, maxProfileNameLength},
		{"last_name", input.LastName, maxProfileNameLength}, {"position", input.Position, maxProfilePositionLen},
	} {
		if field.value == nil {
			continue
		}
		if err := plainText(field.name, *field.value, field.max); err != nil {
			return nil, err
		}
	}
	client, own, err := selfClient(ctx, resolved, secrets, red, op)
	if err != nil {
		return nil, err
	}
	return client.UpdateProfile(ctx, own, input)
}

// UpdateProfile patches only the four display fields that are set, with exactly one PUT, and never repeats it.
// The body is built from this typed struct alone, so no other user field can be sent.
func (c *Client) UpdateProfile(ctx context.Context, own string, fields profileArguments) (*ProfileUpdated, error) {
	const op = "update profile"
	var answer struct {
		ID string `json:"id"`
	}
	if err := c.doWith(ctx, op, http.MethodPut, selfPath(own, "/patch"), nil, fields, &answer,
		uncertainProfile); err != nil {
		return nil, err
	}
	if answer.ID != own {
		return nil, invalidAnswer(op, uncertainProfile)
	}
	return &ProfileUpdated{UserID: own, Nickname: fields.Nickname, FirstName: fields.FirstName,
		LastName: fields.LastName, Position: fields.Position}, nil
}
