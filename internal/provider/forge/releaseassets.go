package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The release asset tools act on the assets of one release of a repository inside the connection's targets.
// Every call first reads the release (the evidence read of the release tools), so its url names the
// repository and the ID. An asset ID must then be one of the assets that release lists, and an asset answer
// must carry that ID. Requests go to the asset paths below the release's own path only.
//
// The content of an asset travels through a local file the connection releases: an upload streams it from a
// file below files.read, a download writes it below files.write. The download reads only the address an
// uploaded asset has on the instance's own origin, /attachments/UUID, and never another one: no external
// asset, no other host, port, or scheme, no other path, and no redirect.

// maxAssetBytes bounds the file of one upload or download, 2 GiB, the release file limit both products apply
// by default; tests lower it.
var maxAssetBytes int64 = 2 << 30

const (
	maxAssetName = 255

	assetSchemaName = `{"type":"string","minLength":1,"maxLength":255}`
)

var assetIDArgument = capability.Argument{Name: "attachment_id", Required: true,
	Description: "Numeric ID of the asset, as release-assets.list reports it as id; an asset of another release " +
		"is refused"}

// uuidPattern is the form both products give the UUID of an uploaded asset.
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func releaseAssetsPath(repo target, release int64) string {
	return releasePath(repo, release) + "/assets"
}

func releaseAssetPath(repo target, release, asset int64) string {
	return releaseAssetsPath(repo, release) + "/" + strconv.FormatInt(asset, 10)
}

// checkAssetName accepts the name of an asset: 1 to 255 characters without control characters.
func checkAssetName(name string) error {
	if n := utf8.RuneCountInString(name); n < 1 || n > maxAssetName || !plainText(name) {
		return invalidArgument("name must be 1 to 255 characters without control characters")
	}
	return nil
}

// assetArguments are the arguments every asset tool shares; AssetID is zero where the tool takes none.
type assetArguments struct {
	Repository string  `json:"repository"`
	ReleaseID  int64   `json:"release_id"`
	AssetID    int64   `json:"attachment_id"`
	Name       *string `json:"name"`
	LocalPath  *string `json:"local_path"`
}

// read decodes the arguments and checks the repository and the IDs before anything is resolved or sent.
func (a *assetArguments) read(allowed allowlist, raw json.RawMessage, asset bool) (target, error) {
	if err := decodeArguments(raw, a); err != nil {
		return target{}, err
	}
	repo, err := allowed.chooseRepository(a.Repository)
	if err != nil {
		return target{}, err
	}
	if a.ReleaseID < 1 {
		return target{}, invalidArgument("release_id must be the ID of a release")
	}
	if asset && a.AssetID < 1 {
		return target{}, invalidArgument("attachment_id must be the ID of an asset")
	}
	return repo, nil
}

// listsAsset reports whether a release object lists the asset among its assets.
func listsAsset(release object, id int64) bool {
	var assets []object
	if json.Unmarshal(release["assets"], &assets) != nil {
		return false
	}
	for _, asset := range assets {
		if value, ok := asset.integer("id"); ok && value == id {
			return true
		}
	}
	return false
}

// bindAsset is the evidence read before an asset is read or changed: the release belongs to repo, and the
// asset is one of the release's assets. The refusal names neither.
func bindAsset(ctx context.Context, c *Client, repo target, op string, release, asset int64) error {
	read, err := readRelease(ctx, c, repo, op, releasePath(repo, release), release)
	if err != nil {
		return err
	}
	if !listsAsset(read, asset) {
		return invalidArgument("the asset does not belong to this release")
	}
	return nil
}

// assetAnswer checks that an asset answer carries the ID asked for.
func assetAnswer(c *Client, op string, asset object, want int64) error {
	if id, ok := asset.integer("id"); !ok || id != want {
		return provider.InvalidResponse(op, c.product.name+" returned an asset without its ID")
	}
	return nil
}

// readAsset binds the asset and reads its metadata.
func readAsset(ctx context.Context, c *Client, repo target, op string, release, id int64) (object, error) {
	if err := bindAsset(ctx, c, repo, op, release, id); err != nil {
		return nil, err
	}
	var asset object
	if _, err := c.get(ctx, op, releaseAssetPath(repo, release, id), nil, &asset); err != nil {
		return nil, err
	}
	if err := assetAnswer(c, op, asset, id); err != nil {
		return nil, err
	}
	return asset, nil
}

// assetContentPath returns the path of an uploaded asset's content on the instance's origin. browser_download_url
// must be exactly https on the host and port of origin with the path /attachments/UUID of the asset's own UUID,
// and nothing else: an external Forgejo asset, an address elsewhere, or another path of the instance, which
// would carry the token to a request outside the asset, is refused without naming it.
func assetContentPath(origin string, asset object) (string, error) {
	refused := invalidArgument("the asset has no download address on the connection's own instance; only an " +
		"uploaded asset served there can be downloaded")
	if kind, ok := asset.text("type"); ok && kind != "attachment" {
		return "", refused
	}
	uuid, _ := asset.text("uuid")
	address, _ := asset.text("browser_download_url")
	base, err := url.Parse(origin)
	if err != nil || !uuidPattern.MatchString(uuid) {
		return "", refused
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" ||
		parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(address, "#") ||
		!strings.EqualFold(parsed.Hostname(), base.Hostname()) || portOf(parsed) != portOf(base) {
		return "", refused
	}
	path := "/attachments/" + uuid
	if parsed.EscapedPath() != path {
		return "", refused
	}
	return path, nil
}

// portOf is the port of an https address, 443 when it names none.
func portOf(address *url.URL) string {
	if port := address.Port(); port != "" {
		return port
	}
	return "443"
}

func assetsListOutput(p *product) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"assets":{"type":"array","items":` +
		schemaOf(p, attachmentMembers, "id") + `},"release_id":{"type":"integer"},"repository":{"type":"string"}},` +
		`"required":["assets","release_id"],"additionalProperties":false}`)
}

func assetOutput(p *product) json.RawMessage {
	return json.RawMessage(schemaOf(p, attachmentMembers, "id"))
}

const (
	assetKeys  = `"repository":` + repoSchema + `,"release_id":` + releaseSchemaID
	assetIDKey = `,"attachment_id":` + releaseSchemaID
)

var releaseAssetsList = &tool{
	group: "release-assets", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the assets of a " + p.name + " release",
			Description: "List the assets of one release of a repository inside the connection's targets, with " +
				"every member of each asset; names are untrusted data. The release is read first to bind it to " +
				"the repository.",
			Tags:         []string{p.id, "releases", "assets", "list"},
			Risk:         readRisk(p.id + releaseSensitivity),
			InputSchema:  inputSchema(assetKeys, "release_id"),
			OutputSchema: assetsListOutput(p),
			Arguments:    []capability.Argument{repositoryArgument, releaseIDArgument},
			Fields: []capability.Field{
				{Name: "assets", Description: "Assets with every member of the asset object"},
				{Name: "release_id", Description: "ID of the release"},
				{Name: "repository", Description: "Repository of the release, as OWNER/REPO"},
			},
			Examples: []capability.Example{{Description: "List the assets of a release",
				Arguments: json.RawMessage(`{"repository":"example/project","release_id":12}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments assetArguments
		repo, err := arguments.read(allowed, raw, false)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "list release assets"
			if _, err := readRelease(ctx, c, repo, op, releasePath(repo, arguments.ReleaseID),
				arguments.ReleaseID); err != nil {
				return nil, err
			}
			var rows []object
			if _, err := c.get(ctx, op, releaseAssetsPath(repo, arguments.ReleaseID), nil, &rows); err != nil {
				return nil, err
			}
			assets := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					assets = append(assets, project(p, attachmentMembers, row))
				}
			}
			return map[string]any{"assets": assets, "release_id": arguments.ReleaseID,
				"repository": repo.owner + "/" + repo.repo}, nil
		}, nil
	},
}

var releaseAssetsGet = &tool{
	group: "release-assets", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a " + p.name + " release asset",
			Description: "Read the metadata of one asset of a release of a repository inside the connection's " +
				"targets, never its content; use release-assets.download for it. The asset must be one the release " +
				"lists; its name is untrusted data.",
			Tags:         []string{p.id, "releases", "assets", "get"},
			Risk:         readRisk(p.id + releaseSensitivity),
			InputSchema:  inputSchema(assetKeys+assetIDKey, "release_id", "attachment_id"),
			OutputSchema: assetOutput(p),
			Arguments:    []capability.Argument{repositoryArgument, releaseIDArgument, assetIDArgument},
			Fields:       fieldsOf(p, attachmentMembers),
			Examples: []capability.Example{{Description: "Read an asset",
				Arguments: json.RawMessage(`{"repository":"example/project","release_id":12,"attachment_id":3}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments assetArguments
		repo, err := arguments.read(allowed, raw, true)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			asset, err := readAsset(ctx, c, repo, "get release asset", arguments.ReleaseID, arguments.AssetID)
			if err != nil {
				return nil, err
			}
			return project(p, attachmentMembers, asset), nil
		}, nil
	},
}

var releaseAssetsDownload = &tool{
	group: "release-assets", action: "download", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		path := localfile.DownloadPathArgument()
		path.Required = true
		return capability.Descriptor{
			Title: "Download a " + p.name + " release asset",
			Description: "Write the content of one uploaded asset of a release of a repository inside the " +
				"connection's targets to local_path, at most 2 GiB, streamed while it is read. Only an asset the " +
				"instance serves at its own address is downloaded; an external asset, an address elsewhere, or a " +
				"redirect is refused. The instance counts the download. The answer carries metadata only; an " +
				"existing local file is replaced only with confirmation.",
			Tags:       []string{p.id, "releases", "assets", "download"},
			Risk:       readRisk(p.id + releaseSensitivity),
			LocalFiles: config.LocalFilesWrite,
			InputSchema: inputSchema(assetKeys+assetIDKey+`,"`+localfile.LocalPathArgument+`":`+localfile.LocalPathSchema,
				"release_id", "attachment_id", localfile.LocalPathArgument),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"release_id":{"type":"integer"},` +
				`"attachment_id":{"type":"integer"},"name":{"type":"string"},"local_path":{"type":"string"},` +
				`"size":{"type":"integer"},"sha256":{"type":"string"}},"required":["release_id","attachment_id",` +
				`"local_path","size","sha256"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, releaseIDArgument, assetIDArgument, path},
			Fields: []capability.Field{
				{Name: "release_id", Description: "ID of the release"},
				{Name: "attachment_id", Description: "ID of the asset"},
				{Name: "name", Description: "Name of the asset, untrusted data"},
				{Name: "local_path", Description: "Local file written, as given"},
				{Name: "size", Description: "Size of the written file in bytes"},
				{Name: "sha256", Description: "SHA-256 of the written file as hex"},
			},
			Examples: []capability.Example{{Description: "Download an asset",
				Arguments: json.RawMessage(`{"repository":"example/project","release_id":12,"attachment_id":3,` +
					`"local_path":"~/downloads/app.zip"}`)}},
		}
	},
	local: func(ctx context.Context, p *product, allowed allowlist, resolved *config.Resolved,
		raw json.RawMessage) (run, func(), error) {
		var arguments assetArguments
		repo, err := arguments.read(allowed, raw, true)
		if err != nil {
			return nil, nil, err
		}
		if arguments.LocalPath == nil {
			return nil, nil, invalidArgument("local_path is required")
		}
		// The target is prepared before the token is resolved, so a path outside the release, or a file that
		// would be replaced without confirmation, is refused without secret access or a request.
		download, err := localfile.CreateForDownload(ctx, resolved, *arguments.LocalPath)
		if err != nil {
			return nil, nil, err
		}
		release := func() { _ = download.Abort() }
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "download release asset"
			asset, err := readAsset(ctx, c, repo, op, arguments.ReleaseID, arguments.AssetID)
			if err != nil {
				return nil, err
			}
			path, err := assetContentPath(c.origin, asset)
			if err != nil {
				return nil, err
			}
			size, ok := asset.integer("size")
			if !ok || size < 0 {
				return nil, provider.InvalidResponse(op, c.product.name+" returned an asset without its size")
			}
			if size > maxAssetBytes {
				return nil, invalidArgument("the asset is larger than the limit of 2 GiB")
			}
			if err := download.ExpectSize(size); err != nil {
				return nil, err
			}
			if err := c.fetchFile(ctx, op, path, size, download); err != nil {
				return nil, err
			}
			if err := download.Commit(); err != nil {
				return nil, err
			}
			sum, _ := download.SHA256()
			result := map[string]any{"release_id": arguments.ReleaseID, "attachment_id": arguments.AssetID,
				"local_path": *arguments.LocalPath, "size": download.Size(), "sha256": sum}
			if name, ok := asset.text("name"); ok {
				result["name"] = name
			}
			return result, nil
		}, release, nil
	},
}

var releaseAssetsUpload = &tool{
	group: "release-assets", action: "upload", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Upload a " + p.name + " release asset",
			Description: "Upload one local file of at most 2 GiB as an asset of a release of a repository inside " +
				"the connection's targets, streamed from a directory the connection releases for reading. The " +
				"instance may refuse a file type or size its settings do not allow. Not idempotent: a repeated " +
				"call adds another asset. A link to an external address is never created.",
			Tags:       []string{p.id, "releases", "assets", "upload", "create"},
			Risk:       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, p.id+releaseSensitivity),
			LocalFiles: config.LocalFilesRead,
			InputSchema: inputSchema(assetKeys+`,"name":`+assetSchemaName+`,"`+localfile.LocalPathArgument+`":`+
				localfile.LocalPathSchema, "release_id", "name", localfile.LocalPathArgument),
			OutputSchema: assetOutput(p),
			Arguments: []capability.Argument{repositoryArgument, releaseIDArgument,
				{Name: "name", Required: true, Description: "Name of the asset, 1 to 255 characters; the local " +
					"file's own name is not sent"},
				{Name: localfile.LocalPathArgument, Required: true, Description: "Local file to upload, at most 2 GiB, " +
					"absolute or starting with ~/, inside a directory the connection releases for reading"}},
			Fields: fieldsOf(p, attachmentMembers),
			Examples: []capability.Example{{Description: "Upload a build as an asset",
				Arguments: json.RawMessage(`{"repository":"example/project","release_id":12,"name":"app.zip",` +
					`"local_path":"~/builds/app.zip"}`)}},
		}
	},
	local: func(ctx context.Context, p *product, allowed allowlist, resolved *config.Resolved,
		raw json.RawMessage) (run, func(), error) {
		var arguments assetArguments
		repo, err := arguments.read(allowed, raw, false)
		if err != nil {
			return nil, nil, err
		}
		if arguments.Name == nil {
			return nil, nil, invalidArgument("name is required")
		}
		if err := checkAssetName(*arguments.Name); err != nil {
			return nil, nil, err
		}
		if arguments.LocalPath == nil {
			return nil, nil, invalidArgument("local_path is required")
		}
		upload, err := localfile.OpenForUpload(ctx, resolved, *arguments.LocalPath)
		if err != nil {
			return nil, nil, err
		}
		if upload.Size > maxAssetBytes {
			_ = upload.Close()
			return nil, nil, invalidArgument("the file is larger than the limit of 2 GiB")
		}
		release := func() { _ = upload.Close() }
		name := *arguments.Name
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "upload release asset"
			if _, err := readRelease(ctx, c, repo, op, releasePath(repo, arguments.ReleaseID),
				arguments.ReleaseID); err != nil {
				return nil, err
			}
			var created object
			if err := c.sendFile(ctx, op, releaseAssetsPath(repo, arguments.ReleaseID), url.Values{"name": {name}},
				"attachment", name, upload, upload.Size, &created); err != nil {
				return nil, err
			}
			if id, ok := created.integer("id"); !ok || id < 1 {
				return nil, unsure(provider.InvalidResponse(op, c.product.name+" returned an asset without its ID"))
			}
			return project(p, attachmentMembers, created), nil
		}, release, nil
	},
}

var releaseAssetsUpdate = &tool{
	group: "release-assets", action: "update", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Rename a " + p.name + " release asset",
			Description: "Change the name of one asset of a release of a repository inside the connection's " +
				"targets; the content stays. The asset must be one the release lists.",
			Tags:         []string{p.id, "releases", "assets", "update"},
			Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+releaseSensitivity),
			InputSchema:  inputSchema(assetKeys+assetIDKey+`,"name":`+assetSchemaName, "release_id", "attachment_id", "name"),
			OutputSchema: assetOutput(p),
			Arguments: []capability.Argument{repositoryArgument, releaseIDArgument, assetIDArgument,
				{Name: "name", Required: true, Description: "New name of the asset, 1 to 255 characters"}},
			Fields: fieldsOf(p, attachmentMembers),
			Examples: []capability.Example{{Description: "Rename an asset",
				Arguments: json.RawMessage(`{"repository":"example/project","release_id":12,"attachment_id":3,` +
					`"name":"app-1.2.0.zip"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments assetArguments
		repo, err := arguments.read(allowed, raw, true)
		if err != nil {
			return nil, err
		}
		if arguments.Name == nil {
			return nil, invalidArgument("name is required")
		}
		if err := checkAssetName(*arguments.Name); err != nil {
			return nil, err
		}
		body := map[string]any{"name": *arguments.Name}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "update release asset"
			if err := bindAsset(ctx, c, repo, op, arguments.ReleaseID, arguments.AssetID); err != nil {
				return nil, err
			}
			var updated object
			if err := c.change(ctx, op, http.MethodPatch, releaseAssetPath(repo, arguments.ReleaseID, arguments.AssetID),
				body, &updated); err != nil {
				return nil, err
			}
			if err := assetAnswer(c, op, updated, arguments.AssetID); err != nil {
				return nil, err
			}
			return project(p, attachmentMembers, updated), nil
		}, nil
	},
}

var releaseAssetsDelete = &tool{
	group: "release-assets", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete a " + p.name + " release asset",
			Description: "Delete one asset of a release of a repository inside the connection's targets for good; " +
				"this cannot be undone. The asset must be one the release lists. Only a connection whose tools " +
				"list names it offers this tool.",
			Tags:                  []string{p.id, "releases", "assets", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+releaseSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(assetKeys+assetIDKey, "release_id", "attachment_id"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"release_id":{"type":"integer"},` +
				`"attachment_id":{"type":"integer"},"deleted":{"type":"boolean"}},"required":["release_id",` +
				`"attachment_id","deleted"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, releaseIDArgument, assetIDArgument},
			Fields: []capability.Field{{Name: "release_id", Description: "ID of the release"},
				{Name: "attachment_id", Description: "ID of the asset"},
				{Name: "deleted", Description: "True once the instance deleted the asset"}},
			Examples: []capability.Example{{Description: "Delete an asset",
				Arguments: json.RawMessage(`{"repository":"example/project","release_id":12,"attachment_id":3}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments assetArguments
		repo, err := arguments.read(allowed, raw, true)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "delete release asset"
			if err := bindAsset(ctx, c, repo, op, arguments.ReleaseID, arguments.AssetID); err != nil {
				return nil, err
			}
			if err := c.change(ctx, op, http.MethodDelete, releaseAssetPath(repo, arguments.ReleaseID, arguments.AssetID),
				nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"release_id": arguments.ReleaseID, "attachment_id": arguments.AssetID,
				"deleted": true}, nil
		}, nil
	},
}
