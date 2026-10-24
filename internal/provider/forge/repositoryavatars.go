package forge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// maxAvatarBytes bounds the image file; the API takes it base64 encoded inside one JSON body.
var maxAvatarBytes int64 = 4 << 20

// repositoryMirrorsSync asks the instance to pull an existing mirror. Creating a mirror is not offered.
var repositoryMirrorsSync = newRepositoryAction(repositoryAction{
	group: "repository-mirrors", action: "sync", since: everywhere,
	title: "Sync a %s pull mirror",
	description: "Ask the instance to pull the upstream of one existing pull mirror inside the connection's targets " +
		"now. The instance syncs asynchronously; a repository that is no mirror is refused by the instance. No mirror " +
		"is created and no mirror setting changes.",
	effect: capability.EffectExecute, idempotency: capability.IdempotencyNonIdempotent,
	suffix: "/mirror-sync", method: http.MethodPost, op: "sync mirror", key: "requested",
	tags: []string{"repository-mirrors", "sync", "execute"},
})

var repositoryAvatarsDelete = newRepositoryAction(repositoryAction{
	group: "repository-avatars", action: "delete", since: everywhere,
	title:       "Delete the avatar of a %s repository",
	description: "Remove the avatar of one repository inside the connection's targets; the instance shows its default image again.",
	effect:      capability.EffectDelete, idempotency: capability.IdempotencyIdempotent,
	suffix: "/avatar", method: http.MethodDelete, op: "delete repository avatar", key: "deleted",
	tags: []string{"repository-avatars", "delete"},
})

var repositoryAvatarsUpdate = &tool{
	group: "repository-avatars", action: "update", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Set the avatar of a " + p.name + " repository",
			Description: "Replace the avatar of one repository inside the connection's targets with a local image " +
				"file of at most 4 MiB, read from a directory the connection releases for reading and sent base64 " +
				"encoded. The instance may refuse a type or size its settings do not allow.",
			Tags:       []string{p.id, "repository-avatars", "update"},
			Risk:       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+repositorySensitivity),
			LocalFiles: config.LocalFilesRead,
			InputSchema: inputSchema(`"repository":`+repoSchema+`,"`+localfile.LocalPathArgument+`":`+
				localfile.LocalPathSchema, localfile.LocalPathArgument),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"updated":{"type":"boolean"}},"required":["repository","updated"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, {Name: localfile.LocalPathArgument, Required: true,
				Description: "Image file to upload, absolute or starting with ~/, inside a directory the " +
					"connection releases for reading"}},
			Fields: []capability.Field{{Name: "repository", Description: "Repository, as OWNER/REPO"},
				{Name: "updated", Description: "True once the instance accepted the image"}},
			Examples: []capability.Example{{Description: "Set the avatar from a local file",
				Arguments: json.RawMessage(`{"repository":"example/project","local_path":"~/images/logo.png"}`)}},
		}
	},
	local: func(ctx context.Context, p *product, allowed allowlist, resolved *config.Resolved,
		raw json.RawMessage) (run, func(), error) {
		var a struct {
			Repository string  `json:"repository"`
			LocalPath  *string `json:"local_path"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, nil, err
		}
		body, release, err := avatarBody(ctx, resolved, a.LocalPath, "set repository avatar")
		if err != nil {
			return nil, nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "set repository avatar", http.MethodPost, repositoryPath(repo)+"/avatar", body,
				nil); err != nil {
				return nil, err
			}
			return map[string]any{"repository": fullName(repo), "updated": true}, nil
		}, release, nil
	},
}

// avatarBody opens the released image file and returns the request body of an avatar upload with the function
// that releases the file. The file is opened and recognized before the token is resolved, so a path outside the
// released directories or a file that is no image is refused without secret access or a request.
func avatarBody(ctx context.Context, resolved *config.Resolved, localPath *string, op string) (map[string]any,
	func(), error) {
	if localPath == nil {
		return nil, nil, invalidArgument("local_path is required")
	}
	upload, err := localfile.OpenForUpload(ctx, resolved, *localPath)
	if err != nil {
		return nil, nil, err
	}
	release := func() { _ = upload.Close() }
	if upload.Size < 1 || upload.Size > maxAvatarBytes {
		release()
		return nil, nil, invalidArgument("the image must be a file of 1 byte to 4 MiB")
	}
	content, err := io.ReadAll(io.LimitReader(upload, upload.Size+1))
	if err != nil || int64(len(content)) != upload.Size {
		release()
		return nil, nil, provider.Fail(op, "the local file could not be read completely")
	}
	if !isImage(content) {
		release()
		return nil, nil, invalidArgument("the file is not a PNG, JPEG, GIF, or WebP image")
	}
	return map[string]any{"image": base64.StdEncoding.EncodeToString(content)}, release, nil
}

// isImage recognizes the formats an avatar can have by their signature, so a text or an archive never travels
// as an image.
func isImage(content []byte) bool {
	switch http.DetectContentType(content) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}
