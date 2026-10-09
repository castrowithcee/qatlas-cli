package nextcloud

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Versions of Nextcloud (developer manual, "WebDAV versions"): below remote.php/dav/versions/<user>/versions/<file
// ID>/ every older version of a file is a child named by its timestamp. A GET reads one, and a MOVE onto
// remote.php/dav/versions/<user>/restore/target makes it the current version. The file ID comes from a stat of
// the requested path, never from the caller, so no version outside the connection root is addressable.
var versionsRoot = []string{"remote.php", "dav", "versions"}

const (
	// maxVersions bounds one listing; the newest versions come first and a cut is reported.
	maxVersions = 100
	// maxVersionNodes bounds the nodes the parser accepts for one file before the listing is cut.
	maxVersionNodes = 5000
	maxVersionIDLen = 20
	versionIDSchema = `{"type":"string","minLength":1,"maxLength":20,"pattern":"^[0-9]+$","x-form":"a version_id of versions.list"}`

	uncertainRestore = "; the version may have been restored, stat the file before repeating"
	messageChanged   = "the Nextcloud file changed since the given ETag was read"
)

var versionPathArgument = capability.Argument{Name: "path", Required: true,
	Description: "File relative to the fixed root folder of this connection; never a folder"}

var versionIDArgument = capability.Argument{Name: "version_id", Required: true,
	Description: "Version to address, a version_id reported by versions.list for the same path"}

var versionsList = capability.Descriptor{
	ID: Provider + ".versions.list", Version: 1, Title: "List Nextcloud file versions",
	Description: "List the older versions of one file below the fixed root of a connection, newest first, at most " +
		"100; file content is never read",
	Tags: []string{"nextcloud", "versions", "webdav", "list"}, Risk: nextcloudReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"path":` + pathSchema + `},"required":["path"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"versions":{"type":"array","items":` +
		`{"type":"object","properties":{"version_id":{"type":"string"},"size":{"type":"integer"},"modified_at":{"type":"string"},` +
		`"content_type":{"type":"string"},"etag":{"type":"string"}},"required":["version_id","size"],"additionalProperties":false}},` +
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},"required":["path","versions","count","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{versionPathArgument},
	Fields: []capability.Field{
		{Name: "path", Description: "File whose versions were listed"},
		{Name: "versions", Description: "Older versions, newest first, with version_id, size, modified_at, content_type, and etag; the current version is not among them"},
		{Name: "count", Description: "Number of reported versions"},
		{Name: "truncated", Description: "True when more versions exist than reported"},
	},
	Examples: []capability.Example{{Description: "List the versions of one file", Arguments: json.RawMessage(`{"path":"Reports/q1.pdf"}`)}},
}

var versionsGet = capability.Descriptor{
	ID: Provider + ".versions.get", Version: 1, Title: "Read or download a Nextcloud file version",
	Description: "Read one older version of a file below the fixed root of a connection, inline as base64 up to 4 MiB " +
		"or written to local_path in a directory the connection releases for writing; the content of a local " +
		"download is never returned, only its metadata. An existing local file is replaced only with confirmation",
	Tags: []string{"nextcloud", "versions", "webdav", "get", "content", "download", "local"}, Risk: nextcloudReadRisk,
	Provider: Provider, LocalFiles: config.LocalFilesWrite,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"path":` + pathSchema + `,"version_id":` + versionIDSchema + `,` +
		`"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},"required":["path","version_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"version_id":{"type":"string"},` +
		`"name":{"type":"string"},"content_base64":{"type":"string"},"content_type":{"type":"string"},"size":{"type":"integer"},` +
		`"sha256":{"type":"string"}},"required":["path","version_id","size"],"additionalProperties":false}`),
	Arguments: []capability.Argument{versionPathArgument, versionIDArgument, localfile.DownloadPathArgument()},
	Fields: []capability.Field{
		{Name: "path", Description: "Path of the file below the root folder"},
		{Name: "version_id", Description: "Version that was read"},
		{Name: "name", Description: "Name of the file, untrusted data; only with local_path"},
		{Name: "content_base64", Description: "Version content as base64; only without local_path"},
		{Name: "content_type", Description: "MIME type Nextcloud reports, untrusted data"},
		{Name: "size", Description: "Size of the content in bytes"},
		{Name: "sha256", Description: "SHA-256 of the written content as hex; only with local_path"},
	},
	Examples: []capability.Example{{Description: "Write one older version to a released local directory",
		Arguments: json.RawMessage(`{"path":"Reports/q1.pdf","version_id":"1760000000","local_path":"~/downloads/q1-old.pdf"}`)}},
}

var versionsRestore = capability.Descriptor{
	ID: Provider + ".versions.restore", Version: 1, Title: "Restore a Nextcloud file version",
	Description: "Restore one older version of a file below the fixed root of a connection as its current version, " +
		"bound to the ETag of the current file; Nextcloud keeps the replaced content as a version",
	Tags: []string{"nextcloud", "versions", "webdav", "restore"}, Provider: Provider, Risk: organiseRisk(capability.EffectUpdate),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"path":` + pathSchema + `,"version_id":` + versionIDSchema + `,` +
		`"etag":{"type":"string","minLength":1,"maxLength":1024,"pattern":"[^*\\s\"]","x-form":"the ETag of the current file, never *"}},` +
		`"required":["path","version_id","etag"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"restored":{"type":"boolean"},"path":{"type":"string"},` +
		`"version_id":{"type":"string"}},"required":["restored"],"additionalProperties":false}`),
	Arguments: []capability.Argument{versionPathArgument, versionIDArgument,
		{Name: "etag", Required: true, Description: "Entity tag of the current file, from files.stat or files.list"}},
	Fields: []capability.Field{
		{Name: "restored", Description: "True when Nextcloud applied the restore"},
		{Name: "path", Description: "Path of the file"},
		{Name: "version_id", Description: "Version that was restored"},
	},
	Examples: []capability.Example{{Description: "Restore a version of a file",
		Arguments: json.RawMessage(`{"path":"Reports/q1.pdf","version_id":"1760000000","etag":"abc123"}`)}},
}

// Version is the stable Qatlas view of one older version of a file.
type Version struct {
	VersionID   string `json:"version_id"`
	Size        int64  `json:"size"`
	ModifiedAt  string `json:"modified_at,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	ETag        string `json:"etag,omitempty"`
}

// VersionList is the newest-first listing of the versions of one file.
type VersionList struct {
	Path      string    `json:"path"`
	Versions  []Version `json:"versions"`
	Count     int       `json:"count"`
	Truncated bool      `json:"truncated"`
}

// VersionContent is the inline content of one version.
type VersionContent struct {
	Path          string `json:"path"`
	VersionID     string `json:"version_id"`
	ContentBase64 string `json:"content_base64"`
	ContentType   string `json:"content_type,omitempty"`
	Size          int    `json:"size"`
}

// VersionDownload is what a local download of one version reports: metadata, never content.
type VersionDownload struct {
	Path        string `json:"path"`
	VersionID   string `json:"version_id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
}

type versionArguments struct {
	Path      string  `json:"path"`
	VersionID string  `json:"version_id"`
	LocalPath *string `json:"local_path"`
	ETag      string  `json:"etag"`
}

// readVersionArguments decodes strictly, so an argument the schema does not know, such as a file ID, is
// refused here as well. The path and the version ID are checked before any credential access.
func readVersionArguments(op string, raw json.RawMessage, needVersion bool) (versionArguments, []string, error) {
	var input versionArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return input, nil, providerError(op, "the validated arguments could not be read")
	}
	rel, err := splitRelative(input.Path)
	if err != nil || len(rel) == 0 {
		return input, nil, providerError(op, "a file path below the connection root is required")
	}
	if needVersion && !validVersionID(input.VersionID) {
		return input, nil, providerError(op, "the version ID must be a version_id reported by "+versionsList.ID)
	}
	return input, rel, nil
}

func validVersionID(value string) bool { return len(value) <= maxVersionIDLen && digitsOnly(value) }

func invokeVersionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list versions"
	input, rel, err := readVersionArguments(op, raw, false)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListVersions(ctx, input.Path, rel)
}

func invokeVersionsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get version"
	input, rel, err := readVersionArguments(op, raw, true)
	if err != nil {
		return nil, err
	}
	if input.LocalPath == nil {
		client, err := Open(ctx, resolved, secrets, red)
		if err != nil {
			return nil, err
		}
		return client.GetVersion(ctx, input.Path, rel, input.VersionID)
	}
	// As in files.get, the local target is prepared before the credential is resolved.
	download, err := localfile.CreateForDownload(ctx, resolved, *input.LocalPath)
	if err != nil {
		return nil, err
	}
	done := false
	defer func() {
		if !done {
			_ = download.Abort()
		}
	}()
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.DownloadVersion(ctx, input.Path, rel, input.VersionID, download, &done)
}

func invokeVersionsRestore(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "restore version"
	input, rel, err := readVersionArguments(op, raw, true)
	if err != nil {
		return nil, err
	}
	if !validETag(input.ETag) {
		return nil, providerError(op, "the ETag of the current file is required")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.RestoreVersion(ctx, input.Path, rel, input.VersionID, input.ETag)
}

// versionsPrefix are the decoded segments of the versions area of this identity.
func (c *Client) versionsPrefix() []string {
	return append(append(append([]string{}, c.install...), versionsRoot...), c.user)
}

// versionURL is the absolute URL of the versions of one file, or of one of them.
func (c *Client) versionURL(fileID string, version ...string) string {
	segments := append(append(c.versionsPrefix(), "versions", fileID), version...)
	return c.origin + escapePath(segments)
}

// versionFile stats the requested path, which must be a file, and returns its entry. The file ID of that
// entry is the only way a version is addressed.
func (c *Client) versionFile(ctx context.Context, op string, rel []string) (*Entry, error) {
	entry, err := c.stat(ctx, op, rel, false)
	if err != nil {
		return nil, err
	}
	if entry.Type != typeFile || entry.FileID == "" {
		return nil, providerError(op, "only a file has versions")
	}
	return entry, nil
}

// ListVersions reads the versions of one file with a single PROPFIND of depth 1.
func (c *Client) ListVersions(ctx context.Context, path string, rel []string) (*VersionList, error) {
	const op = "list versions"
	entry, err := c.versionFile(ctx, op, rel)
	if err != nil {
		return nil, err
	}
	bound := append(c.versionsPrefix(), "versions", entry.FileID)
	resources, err := c.propfindAt(ctx, op, c.versionURL(entry.FileID), propfindBody, depthChildren, false, maxVersionNodes)
	if err != nil {
		return nil, err
	}
	versions := make([]Version, 0, len(resources))
	for i := range resources {
		below, err := c.segmentsBelow(op, resources[i].href, bound)
		if err != nil {
			return nil, err
		}
		switch {
		case len(below) == 0:
			// The folder itself.
		case len(below) == 1:
			// Only a node with a timestamp name and readable properties is a version.
			if validVersionID(below[0]) && !resources[i].collection && resources[i].failure(op) == nil {
				versions = append(versions, versionOf(below[0], &resources[i]))
			}
		default:
			return nil, invalidResponse(op, messageForeignEntry)
		}
	}
	// Timestamps order numerically; the length decides first so that no integer parsing is needed.
	sort.Slice(versions, func(i, j int) bool {
		a, b := versions[i].VersionID, versions[j].VersionID
		if len(a) != len(b) {
			return len(a) > len(b)
		}
		return a > b
	})
	truncated := len(versions) > maxVersions
	if truncated {
		versions = versions[:maxVersions]
	}
	return &VersionList{Path: path, Versions: versions, Count: len(versions), Truncated: truncated}, nil
}

func versionOf(id string, res *resource) Version {
	version := Version{VersionID: id, Size: number(res.props[propContentLength]),
		ContentType: bounded(res.props[propContentType]), ETag: bounded(strings.Trim(res.props[propETag], `"`))}
	if parsed, err := http.ParseTime(res.props[propLastModified]); err == nil {
		version.ModifiedAt = parsed.UTC().Format(time.RFC3339)
	}
	return version
}

// GetVersion reads one version inline, up to the limit of files.get.
func (c *Client) GetVersion(ctx context.Context, path string, rel []string, versionID string) (*VersionContent, error) {
	const op = "get version"
	entry, err := c.versionFile(ctx, op, rel)
	if err != nil {
		return nil, err
	}
	body, header, err := c.readInline(ctx, op, c.versionURL(entry.FileID, versionID), "the Nextcloud file version exceeds 4 MiB, use local_path")
	if err != nil {
		return nil, err
	}
	return &VersionContent{Path: path, VersionID: versionID, ContentBase64: base64.StdEncoding.EncodeToString(body),
		ContentType: bounded(header.Get("Content-Type")), Size: len(body)}, nil
}

// DownloadVersion streams one version into the prepared local file.
func (c *Client) DownloadVersion(ctx context.Context, path string, rel []string, versionID string, download *localfile.Download, done *bool) (*VersionDownload, error) {
	const op = "get version"
	entry, err := c.versionFile(ctx, op, rel)
	if err != nil {
		return nil, err
	}
	header, err := c.downloadTo(ctx, op, c.versionURL(entry.FileID, versionID), download, done)
	if err != nil {
		return nil, err
	}
	sum, _ := download.SHA256()
	return &VersionDownload{Path: path, VersionID: versionID, Name: entry.Name,
		ContentType: bounded(header.Get("Content-Type")), Size: download.Size(), SHA256: sum}, nil
}

// RestoreVersion makes one version the current one. One stat checks the ETag and yields the file ID, then
// one MOVE restores. The manual documents no condition for the MOVE, so a change between the stat and the
// MOVE is not detected; an unclear MOVE is reported as uncertain and never repeated.
func (c *Client) RestoreVersion(ctx context.Context, path string, rel []string, versionID, etag string) (any, error) {
	const op = "restore version"
	entry, err := c.versionFile(ctx, op, rel)
	if err != nil {
		return nil, err
	}
	if entry.ETag != strings.Trim(etag, `"`) {
		return nil, providerError(op, messageChanged)
	}
	destination := c.origin + escapePath(append(c.versionsPrefix(), "restore", "target"))
	headers := http.Header{"Destination": {destination}}
	response, err := c.webdavTo(ctx, op, methodMove, c.versionURL(entry.FileID, versionID), nil, "", "", uncertainRestore, headers)
	if err != nil {
		return nil, err
	}
	response.Body.Close()
	return map[string]any{"restored": true, "path": path, "version_id": versionID}, nil
}
