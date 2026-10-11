package infomaniakchat

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	// maxEmojiBytes is the fixed local bound of an emoji image.
	maxEmojiBytes = 1 << 20
	// emojiAdministered names the capability in refusals of the instance-wide gate.
	emojiAdministered = "kChat custom emoji administration"
	uncertainEmojiAdd = "; the emoji may have been created, list the emoji before creating it again"
	uncertainEmojiDel = "; the emoji may have been deleted, list the emoji before deleting it again"
)

// emojiNamePattern is the form of a custom emoji name.
var emojiNamePattern = regexp.MustCompile(`^[a-z0-9_+-]{1,64}$`)

const emojiScopeNote = "Instance-wide: only on a connection that binds every team of its token and has no " +
	"channel target."

var emojiCreate = capability.Descriptor{
	ID:      Provider + ".emoji.create",
	Version: 1,
	Title:   "Create an Infomaniak kChat custom emoji",
	Description: "Create exactly one custom emoji from a PNG, GIF, or JPEG image of up to 1 MiB in a local " +
		"directory the connection releases for reading, with one confirmed request as the token's own user. " +
		"An unclear outcome is never repeated. " + emojiScopeNote,
	Tags:       []string{"infomaniak", "kchat", "emoji", "administration", "create"},
	Risk:       botRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, capability.ConfirmationRequired),
	Provider:   Provider,
	LocalFiles: config.LocalFilesRead,
	// Like every emoji mutation, only a connection whose tools list names it offers it.
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"name":{"type":"string","pattern":"^[a-z0-9_+-]{1,64}$"},` +
		`"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},` +
		`"required":["name","` + localfile.LocalPathArgument + `"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(emojiSchema),
	Arguments: []capability.Argument{
		{Name: "name", Required: true, Description: "Emoji name, 1 to 64 lowercase letters, digits, underscores, " +
			"plus signs, or hyphens"},
		{Name: localfile.LocalPathArgument, Required: true, Description: "Local PNG, GIF, or JPEG file of up to " +
			"1 MiB, absolute or starting with ~/, inside a directory the connection releases for reading"},
	},
	Fields: emojiFields,
	Examples: []capability.Example{{Description: "Create an emoji from a local image",
		Arguments: json.RawMessage(`{"name":"party","local_path":"~/uploads/party.png"}`)}},
}

var emojiDelete = capability.Descriptor{
	ID:      Provider + ".emoji.delete",
	Version: 1,
	Title:   "Delete an Infomaniak kChat custom emoji",
	Description: "Delete exactly one custom emoji by its ID from emoji.list with one confirmed request, after " +
		"reading it once. An unclear outcome is never repeated. " + emojiScopeNote,
	Tags:                  []string{"infomaniak", "kchat", "emoji", "administration", "delete"},
	Risk:                  botRisk(capability.EffectDelete, capability.IdempotencyIdempotent, capability.ConfirmationRequired),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"emoji_id":` + idSchema + `},` +
		`"required":["emoji_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(emojiSchema),
	Arguments: []capability.Argument{{Name: "emoji_id", Required: true,
		Description: "Identifier of the custom emoji from emoji.list"}},
	Fields: emojiFields,
	Examples: []capability.Example{{Description: "Delete one emoji",
		Arguments: json.RawMessage(`{"emoji_id":"abc123emoji0000000000000"}`)}},
}

type emojiArguments struct {
	Name      string `json:"name"`
	LocalPath string `json:"local_path"`
	EmojiID   string `json:"emoji_id"`
}

// imageType returns the media type of a PNG, GIF, or JPEG image by its leading bytes, or "".
func imageType(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif"
	case bytes.HasPrefix(data, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	}
	return ""
}

func invokeEmojiCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create emoji"
	var input emojiArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !emojiNamePattern.MatchString(input.Name) {
		return nil, invalidRequest("name must be 1 to 64 lowercase letters, digits, underscores, plus signs, or hyphens")
	}
	if err := requireInstanceScope(resolved, emojiAdministered); err != nil {
		return nil, err
	}
	// The image is read and checked before the credential is resolved, so a source outside the release, a
	// wrong type, or an oversized file is refused without secret access or provider I/O.
	stream, err := localfile.OpenForUpload(ctx, resolved, input.LocalPath)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	if stream.Size > maxEmojiBytes {
		return nil, invalidRequest("the emoji image is larger than the limit of 1 MiB")
	}
	content, err := io.ReadAll(stream)
	if err != nil {
		return nil, providerError(op, "the local file could not be read completely")
	}
	fileType := imageType(content)
	if fileType == "" {
		return nil, invalidRequest("the emoji image must be a PNG, GIF, or JPEG file")
	}
	if !validFileName(stream.Name) {
		return nil, invalidRequest("the file name must be a plain name of 1 to " + itoa(maxFileNameBytes) +
			" bytes without a path or control characters")
	}

	client, err := openInstance(ctx, resolved, secrets, red, op, emojiAdministered)
	if err != nil {
		return nil, err
	}
	return client.CreateEmoji(ctx, input.Name, stream.Name, fileType, content)
}

// CreateEmoji sends exactly one multipart POST with the image and the emoji resource, whose creator is the
// token's own user, and never repeats it: a failure after the request may have reached kChat says so instead.
func (c *Client) CreateEmoji(ctx context.Context, name, fileName, fileType string, content []byte) (*EmojiEntry, error) {
	const op = "create emoji"
	self, err := c.ownUserID(ctx, op)
	if err != nil {
		return nil, err
	}
	resource, err := json.Marshal(struct {
		Name      string `json:"name"`
		CreatorID string `json:"creator_id"`
	}{name, self})
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	body, err := buildMultipart("emoji", string(resource), "image", fileName, fileType, int64(len(content)),
		bytes.NewReader(content))
	if err != nil {
		return nil, invalidRequest("the file name cannot be sent")
	}
	long := *c.http
	long.Timeout = uploadTimeout
	var created emojiJSON
	if err := c.exchange(ctx, &long, op, http.MethodPost, "/api/v4/emoji", nil, body, &created,
		uncertainEmojiAdd); err != nil {
		return nil, err
	}
	if !validMattermostID(created.ID) || created.Name != name || created.CreatorID != self {
		return nil, provider.InvalidResponse(op, "kChat answered with an unusable emoji"+uncertainEmojiAdd)
	}
	return emojiEntryOf(&created), nil
}

func emojiEntryOf(e *emojiJSON) *EmojiEntry {
	return &EmojiEntry{ID: e.ID, Name: bounded(e.Name), CreatorID: bounded(e.CreatorID),
		CreatedAt: msToRFC3339(e.CreateAt)}
}

func invokeEmojiDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete emoji"
	var input emojiArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validMattermostID(input.EmojiID) {
		return nil, invalidRequest("emoji_id must be a kChat-style identifier")
	}
	client, err := openInstance(ctx, resolved, secrets, red, op, emojiAdministered)
	if err != nil {
		return nil, err
	}
	return client.DeleteEmoji(ctx, input.EmojiID)
}

// DeleteEmoji reads the emoji once, requires it to be the one named, then sends exactly one DELETE and never
// repeats it: a failure after the request may have reached kChat says so instead.
func (c *Client) DeleteEmoji(ctx context.Context, emojiID string) (*EmojiEntry, error) {
	const op = "delete emoji"
	path := "/api/v4/emoji/" + url.PathEscape(emojiID)
	var emoji emojiJSON
	if err := c.do(ctx, op, http.MethodGet, path, nil, nil, &emoji, false); err != nil {
		return nil, err
	}
	if emoji.ID != emojiID {
		return nil, provider.InvalidResponse(op, "kChat returned an invalid response")
	}
	if err := c.doWith(ctx, op, http.MethodDelete, path, nil, nil, nil, uncertainEmojiDel); err != nil {
		return nil, err
	}
	return emojiEntryOf(&emoji), nil
}
