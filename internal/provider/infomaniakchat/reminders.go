package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const uncertainRemind = "; the reminder may have been set, do not set it again without the person's decision"

var messagesRemind = capability.Descriptor{
	ID:      Provider + ".messages.remind",
	Version: 1,
	Title:   "Set a reminder for an Infomaniak kChat message",
	Description: "Set one reminder for exactly one confirmed message of a channel this connection may reach, for " +
		"the token's own user only; the time must lie in the future and at most one year ahead",
	Tags: []string{"infomaniak", "kchat", "messages", "remind", "reminder"},
	Risk: capability.Risk{Effect: capability.EffectCreate, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"post_id":` + idSchema + `,` +
		`"target_time":{"type":"string","format":"date-time"}},` +
		`"required":["post_id","target_time"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"post_id":` + idSchema + `,"target_time":{"type":"string"}},` +
		`"required":["post_id","target_time"],"additionalProperties":false}`),
	Arguments: []capability.Argument{postIDArgument,
		{Name: "target_time", Required: true, Description: "RFC 3339 time of the reminder; after now and at most " +
			"one year ahead"}},
	Fields: []capability.Field{
		{Name: "post_id", Description: "Message the reminder was set for"},
		{Name: "target_time", Description: "Reminder time as sent, in UTC"},
	},
	Examples: []capability.Example{{Description: "Remind about a message",
		Arguments: json.RawMessage(`{"post_id":"abc123post00000000000000000","target_time":"2030-01-02T09:00:00Z"}`)}},
}

// Reminder is the answer of one confirmed reminder.
type Reminder struct {
	PostID     string `json:"post_id"`
	TargetTime string `json:"target_time"`
}

type remindArguments struct {
	PostID     string `json:"post_id"`
	TargetTime string `json:"target_time"`
}

// reminderBody is the whole body of the reminder request, built from the typed time.
type reminderBody struct {
	TargetTime int64 `json:"target_time"`
}

// reminderTime parses an RFC 3339 time that lies after now and at most one year ahead.
func reminderTime(value string) (time.Time, error) {
	parsed, err := futureTime("target_time", value)
	if err != nil {
		return time.Time{}, err
	}
	if parsed.After(clock().AddDate(1, 0, 0)) {
		return time.Time{}, invalidRequest("target_time must lie at most one year ahead")
	}
	return parsed, nil
}

func invokeRemind(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set message reminder"
	var input remindArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validMattermostID(input.PostID) {
		return nil, invalidRequest("post_id must be a kChat-style identifier")
	}
	target, err := reminderTime(input.TargetTime)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	post, err := client.verifyPostScope(ctx, resolved, op, input.PostID)
	if err != nil {
		return nil, err
	}
	return client.SetReminder(ctx, post.ID, target)
}

// SetReminder sets one reminder for the own user with exactly one request and never repeats it: a failure
// after the request may have reached kChat says so instead. kChat answers with a status only, which must be OK.
func (c *Client) SetReminder(ctx context.Context, postID string, target time.Time) (*Reminder, error) {
	const op = "set message reminder"
	var answer struct {
		Status string `json:"status"`
	}
	err := c.doWith(ctx, op, http.MethodPost, "/api/v4/users/me/posts/"+postID+"/reminder", nil,
		reminderBody{TargetTime: target.Unix()}, &answer, uncertainRemind)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(answer.Status, "ok") {
		return nil, invalidAnswer(op, uncertainRemind)
	}
	return &Reminder{PostID: postID, TargetTime: target.UTC().Format(time.RFC3339)}, nil
}
