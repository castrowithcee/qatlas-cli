package nextcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Sharing a file into a conversation is the share API with the Talk share type. Only a private conversation
// (one-to-one or group) qualifies: the participants of a public conversation include guests, who would
// reach the file. The share is read-only and carries nothing but the path and the conversation token.
const (
	ocsShareRoom = "10"

	messageTalkFilesPublic = "files can only be shared into a private one-to-one or group Talk conversation"
)

var talkFilesShare = capability.Descriptor{
	ID: Provider + ".talk.files.share", Version: 1, Title: "Share a Nextcloud file in a Talk conversation",
	Description: "Share exactly one file or folder below the fixed root folder, read-only, with one private " +
		"one-to-one or group Talk conversation the connection binds; every participant can then open it. Public " +
		"conversations are refused. Needs a folder and a talk target. One read-only check of the conversation " +
		"precedes exactly one request; revoke the share with shares.shares.delete",
	Tags:     []string{"nextcloud", "talk", "shares", "files", "share", "ocs"},
	Provider: Provider, Risk: talkWriteRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"token":` + talkTokenSchema + `,"path":` + pathSchema +
		`},"required":["token","path"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"shared":{"type":"boolean"},"share":` + shareSchema +
		`},"required":["shared","share"],"additionalProperties":false}`),
	Arguments: []capability.Argument{talkTokenArgument,
		{Name: "path", Required: true, Description: "File or folder relative to the fixed root folder of this connection"}},
	Fields: []capability.Field{{Name: "shared", Description: "True when Nextcloud created the share"},
		{Name: "share", Description: "The share as Nextcloud reports it; its ID revokes it with shares.shares.delete"}},
	Examples: []capability.Example{{Description: "Share a report in a conversation",
		Arguments: json.RawMessage(`{"token":"abcd1234","path":"2026/q1.pdf"}`)}},
	RequiresToolAllowList: true,
}

type talkFilesArguments struct {
	Token string `json:"token"`
	Path  string `json:"path"`
}

func invokeTalkFilesShare(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "share file in talk conversation"
	var input talkFilesArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	// Both bindings are checked before the path, the credential, or any request; the refusals name no target.
	if _, err := requireFolder(resolved); err != nil {
		return nil, err
	}
	if err := requireTalkToken(resolved, input.Token); err != nil {
		return nil, err
	}
	rel, err := splitRelative(input.Path)
	if err != nil || len(rel) == 0 {
		return nil, providerError(op, "the path must name a file or folder below the root folder of this connection")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.shareInRoom(ctx, op, input.Token, rel)
}

// shareInRoom reads the conversation once, refuses anything but a private ordinary one, and sends exactly
// one POST.
func (c *Client) shareInRoom(ctx context.Context, op, token string, rel []string) (any, error) {
	data, err := c.ocsGet(ctx, op, ocsRequest{app: ocsTalkRooms, suffix: []string{token},
		query: url.Values{"noStatusUpdate": {"1"}}})
	if err != nil {
		return nil, talkNotFound(op, err, messageTalkNoRoom)
	}
	var room rawRoomState
	if err := json.Unmarshal(data, &room); err != nil || room.Token != token {
		return nil, invalidResponse(op, "the Nextcloud conversation could not be read")
	}
	if kind, _ := room.Type.Int64(); kind != roomTypeOneToOne && kind != roomTypeGroup || room.ObjectType != "" {
		return nil, &provider.Error{Class: provider.ClassPermission, Op: op, Message: messageTalkFilesPublic}
	}
	form := url.Values{"shareType": {ocsShareRoom}, "shareWith": {token}, "path": {c.absolute(rel)},
		"permissions": {strconv.Itoa(permRead)}}
	answer, err := c.ocsSend(ctx, op, http.MethodPost, shareEndpoint, form, uncertainShareCreate)
	if err != nil {
		return nil, tagAdminError(op, err, shareManageErrors)
	}
	share, err := c.sharedAnswer(op, answer, uncertainShareCreate)
	if err != nil {
		return nil, err
	}
	return map[string]any{"shared": true, "share": share}, nil
}
