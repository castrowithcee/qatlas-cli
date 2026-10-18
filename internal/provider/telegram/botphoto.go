package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// profileAttachName is the multipart part that the attach:// reference of the profile photo points to.
const profileAttachName = "profile"

var profileSetPhoto = capability.Descriptor{
	ID:      Provider + ".profile.set-photo",
	Version: 1,
	Title:   "Set the Telegram bot profile photo",
	Description: "Replace the profile photo of the bot with a static JPEG image from a local file up to 10 MB in a " +
		"directory the connection releases for reading; no file reference, URL, or animated photo. The file is " +
		"sent under a neutral name without its path. An unclear outcome is never repeated. Requires the bot target",
	Tags:     []string{"telegram", "bot", "profile", "photo", "files"},
	Risk:     capability.Risk{Effect: capability.EffectUpdate, Idempotency: capability.IdempotencyUnknown, Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
	Provider: Provider,
	Group:    groupBot, LocalFiles: config.LocalFilesRead,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"` + localfile.LocalPathArgument + `":` +
		localfile.LocalPathSchema + `},"required":["` + localfile.LocalPathArgument + `"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"updated":{"type":"boolean"}},` +
		`"required":["updated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: localfile.LocalPathArgument, Required: true, Description: "Local JPEG photo (.jpg or .jpeg), absolute " +
			"or starting with ~/, inside a directory the connection releases for reading, up to 10 MB"},
	},
	Fields: []capability.Field{{Name: "updated", Description: "True when Telegram accepted the photo"}},
	Examples: []capability.Example{{
		Description: "Set the bot profile photo",
		Arguments:   json.RawMessage(`{"local_path":"~/images/bot.jpg"}`),
	}},
}

var profileRemovePhoto = capability.Descriptor{
	ID:          Provider + ".profile.remove-photo",
	Version:     1,
	Title:       "Remove the Telegram bot profile photo",
	Description: "Remove the profile photo of the bot. Requires the bot target",
	Tags:        []string{"telegram", "bot", "profile", "photo", "delete"},
	Risk:        botSettingRisk(capability.EffectDelete),
	Provider:    Provider, RequiresToolAllowList: true,
	Group:        groupBot,
	InputSchema:  json.RawMessage(noArguments),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},"required":["deleted"],"additionalProperties":false}`),
	Fields:       []capability.Field{{Name: "deleted", Description: "True when Telegram accepted the removal"}},
	Examples:     []capability.Example{{Description: "Remove the bot profile photo", Arguments: json.RawMessage(`{}`)}},
}

func invokeProfileSetPhoto(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeProfileSetPhotoWith(ctx, resolved, secrets, red, raw, newHTTPClient())
}

func invokeProfileSetPhotoWith(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, httpClient *http.Client) (any, error) {
	const op = "set bot profile photo"
	var arguments struct {
		LocalPath *string `json:"local_path"`
	}
	if err := decodeStrict(raw, &arguments); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if arguments.LocalPath == nil {
		return nil, providerError(op, "local_path is required")
	}
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	if err := requireBotScope(resolved); err != nil {
		return nil, err
	}
	// Telegram accepts only a static JPEG here; the extension check keeps animated files out before any read.
	switch strings.ToLower(filepath.Ext(*arguments.LocalPath)) {
	case ".jpg", ".jpeg":
	default:
		return nil, providerError(op, "the profile photo must be a .jpg or .jpeg file")
	}
	// The release check and the size check happen before the credential is resolved or any byte is read.
	prepared, err := prepareSources(ctx, resolved, op, "", []string{kindPhoto}, []mediaSource{{LocalPath: arguments.LocalPath}})
	if err != nil {
		return nil, err
	}
	defer closeUploads(prepared)
	client, err := openWithHTTP(ctx, resolved, secrets, red, httpClient)
	if err != nil {
		return nil, err
	}
	items, err := client.load(op, prepared)
	if err != nil {
		return nil, err
	}
	return client.SetProfilePhoto(ctx, items[0])
}

func invokeProfileRemovePhoto(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	open, err := openBot(ctx, resolved, secrets, red, "remove bot profile photo")
	if err != nil {
		return nil, err
	}
	client, err := open()
	if err != nil {
		return nil, err
	}
	return client.RemoveProfilePhoto(ctx)
}

// SetProfilePhoto performs exactly one multipart setMyProfilePhoto request with the already opened local file.
func (c *Client) SetProfilePhoto(ctx context.Context, item mediaItem) (map[string]any, error) {
	const op = "set bot profile photo"
	if len(item.data) == 0 || len(item.data) > maxPhotoBytes {
		return nil, providerError(op, "the photo is empty or larger than Telegram accepts")
	}
	input, err := json.Marshal(struct {
		Type  string `json:"type"`
		Photo string `json:"photo"`
	}{"static", "attach://" + profileAttachName})
	if err != nil {
		return nil, providerError(op, "the request could not be encoded")
	}
	s := spec{op: op, method: "setMyProfilePhoto", limit: defaultResponseBytes}
	raw, err := c.withUploadTimeout().callMultipart(ctx, s, []field{{"photo", string(input)}},
		[]filePart{{field: profileAttachName, name: item.name, data: item.data}})
	if err != nil {
		return nil, err
	}
	var ok bool
	if json.Unmarshal(raw, &ok) != nil || !ok {
		return nil, withUncertainty(spec{}, invalidResponse(op))
	}
	return map[string]any{"updated": true}, nil
}

// RemoveProfilePhoto performs exactly one removeMyProfilePhoto request.
func (c *Client) RemoveProfilePhoto(ctx context.Context) (map[string]any, error) {
	const op = "remove bot profile photo"
	if err := c.confirmed(ctx, spec{op: op, method: "removeMyProfilePhoto", limit: defaultResponseBytes}, struct{}{}); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}
