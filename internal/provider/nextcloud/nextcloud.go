// Package nextcloud implements controlled access to the Files app of one Nextcloud instance over WebDAV.
//
// A Nextcloud installation holds many identities, and every identity owns its own file tree below
// /remote.php/dav/files/{user-id}/. That cardinality is the configuration: a service is one instance with
// an optional installation path, a credential is one user ID together with one revocable app password,
// and a connection binds them to typed targets, among them at most one fixed root folder. An invoke
// request can name neither the instance, nor the identity, nor the root; it may only address a path below
// the root the connection is bound to. The tools of this package are the Files tools; the other target
// kinds are parsed and bound in targets.go.
//
// The adapter produces only explicit Files WebDAV requests (PROPFIND, GET, PUT, DELETE, MKCOL, MOVE, COPY, and
// the chunked upload methods) and fixed read-only OCS Sharing requests (ocs.go, shares.go); it reaches no
// other app of the instance. Names, DAV
// properties, and the whole multi-status document arrive from the provider and are treated as untrusted
// data: they are normalised into a stable metadata envelope, passed through the output encoders, and
// never rendered or stored.
package nextcloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Provider is the provider name used in the configuration and as the operation namespace.
const Provider = "nextcloud"

// The secret roles a Nextcloud credential must supply. Both belong to one identity: the user ID names it,
// and the app password authenticates it without ever exposing the account password or defeating a second
// factor. Nextcloud recommends an app password precisely for WebDAV clients, and it can be revoked alone.
const (
	roleUserID      = "user-id"
	roleAppPassword = "app-password"
)

// dataSensitivity classifies results as file metadata of the configured Nextcloud identity. It is
// deliberately provider-specific; the architecture defines no global sensitivity taxonomy.
const dataSensitivity = "nextcloud-files"

// filesRoot are the fixed path segments of the authenticated Files WebDAV endpoint. The user ID follows
// them, and the configured root folder follows the user ID.
var filesRoot = []string{"remote.php", "dav", "files"}

// uploadsRoot are the fixed path segments of the upload area for chunked uploads; the user ID follows them.
var uploadsRoot = []string{"remote.php", "dav", "uploads"}

// trashbinRoot are the fixed path segments of the trash bin; the user ID and the collections trash and
// restore follow them.
var trashbinRoot = []string{"remote.php", "dav", "trashbin"}

// methodPropfind is the HTTP method of every metadata read. The file operations add only their own fixed
// methods (GET, PUT, DELETE, and the chunked upload methods); no method comes from a request.
const methodPropfind = "PROPFIND"

// The two depths this adapter uses: the node itself, and the node plus its immediate children. A larger
// depth would walk the whole tree and is deliberately not offered.
const (
	depthSelf     = "0"
	depthChildren = "1"
)

// propfindBody asks for exactly the properties the normalised metadata is built from. Requesting a fixed
// set rather than allprop keeps the answer small and keeps an unknown provider property out of the result.
const propfindBody = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<d:propfind xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:prop>` +
	`<d:displayname/><d:resourcetype/><d:getcontenttype/><d:getcontentlength/>` +
	`<d:getlastmodified/><d:getetag/><oc:fileid/><oc:size/><oc:permissions/>` +
	`</d:prop></d:propfind>`

// Bounds of one request and one answer. They are deliberately conservative: a folder that exceeds them is
// refused before any of its data is handed on, rather than silently truncated into a list that looks
// complete. A caller who needs a larger folder narrows the connection root instead.
const (
	maxBodyBytes   = 4 << 20
	maxFileBytes   = 4 << 20
	maxEntries     = 500
	maxXMLDepth    = 20
	maxTextBytes   = 8 << 10
	maxPathLength  = 1024
	maxSegments    = 32
	maxSegmentLen  = 255
	maxUserIDLen   = 64
	maxValueLength = 1024
	defaultTimeout = 30 * time.Second
	// transferTimeout replaces the 30 s of the client for each request that carries file content to or
	// from a local path.
	transferTimeout = 30 * time.Minute
)

// maxPathUploadBytes is the largest local file one single PUT carries. A larger file goes through the
// chunked upload of Nextcloud; see chunked.go.
var maxPathUploadBytes int64 = 64 << 20

// The uncertain hints are appended to the failure of a file mutation whose request may have reached
// Nextcloud: the change may have been applied although no confirmation ever arrived. Qatlas never repeats
// such a request.
const (
	uncertainStored        = "; the file may have been stored, stat the file before repeating"
	uncertainDeleted       = "; the file may have been deleted, stat the file before repeating"
	uncertainFolderDeleted = "; the folder may have been deleted, stat the folder before repeating"
)

// pathPattern is the schema form of one path relative to the connection root: one or more segments
// separated by a single slash, where no segment is empty, "." or "..", and no character is a backslash or
// a percent sign. It therefore refuses an absolute path, an absolute URL, a traversal, a Windows
// separator, and every percent-encoded separator or double encoding before the core resolves a secret.
//
// qatlas-dev: refusing the percent sign outright also refuses a name that genuinely contains one; that
// name stays visible in a listing and needs a narrower connection root to be addressed.
const pathPattern = `^(?:\\.[^./\\\\%][^/\\\\%]*|\\.\\.[^/\\\\%]+|[^./\\\\%][^/\\\\%]*)` +
	`(?:/(?:\\.[^./\\\\%][^/\\\\%]*|\\.\\.[^/\\\\%]+|[^./\\\\%][^/\\\\%]*))*$`

// pathSchema is the optional relative path both operations accept. Omitting it addresses the connection
// root itself.
const pathSchema = `{"type":"string","minLength":1,"maxLength":1024,"pattern":"` + pathPattern + `"}`

// entrySchema is the stable envelope of one file or folder. It is the same shape in both operations, so a
// caller can hand a listed path straight to the stat operation.
const entrySchema = `{"type":"object","properties":{` +
	`"path":{"type":"string"},"name":{"type":"string"},` +
	`"type":{"type":"string","enum":["file","folder"]},` +
	`"content_type":{"type":"string"},"size":{"type":"integer"},"modified_at":{"type":"string"},` +
	`"etag":{"type":"string"},"file_id":{"type":"string"},"readable":{"type":"boolean"}},` +
	`"required":["path","name","type","size","readable"],"additionalProperties":false}`

var nextcloudReadRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: dataSensitivity,
}

// entryFields is the discovery metadata of the normalised envelope. Both operations report the same
// fields, so they are described once.
var entryFields = []capability.Field{
	{Name: "path", Description: "Path relative to the fixed root folder of this connection, empty for the root itself"},
	{Name: "name", Description: "Display name of the file or folder, untrusted data"},
	{Name: "type", Description: "Either file or folder"},
	{Name: "content_type", Description: "MIME type Nextcloud reports for a file"},
	{Name: "size", Description: "Size in bytes; for a folder the size Nextcloud keeps for its whole subtree"},
	{Name: "modified_at", Description: "Last change time, normalised to RFC 3339 in UTC"},
	{Name: "etag", Description: "Entity tag of the current version"},
	{Name: "file_id", Description: "Stable Nextcloud file identifier"},
	{Name: "readable", Description: "True when the effective permissions of this identity allow reading the node"},
}

var filesList = capability.Descriptor{
	ID:      Provider + ".files.list",
	Version: 1,
	Title:   "List Nextcloud files",
	Description: "List the immediate children of the fixed root folder, or of one folder below it, of an " +
		"explicit Nextcloud connection; the listing is one level deep and never reads file content",
	Tags:     []string{"nextcloud", "files", "webdav", "list", "folder"},
	Risk:     nextcloudReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"path":` + pathSchema + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"path":{"type":"string"},"entries":{"type":"array","items":` + entrySchema + `},` +
		`"count":{"type":"integer"}},` +
		`"required":["path","entries","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "path", Description: "Folder relative to the fixed root folder of this connection; the root itself when omitted"},
	},
	Fields: []capability.Field{
		{Name: "path", Description: "Folder that was listed, relative to the connection root"},
		{Name: "entries", Description: "Metadata of the immediate children, untrusted data; a child whose metadata the server refused is omitted"},
		{Name: "count", Description: "Number of reported children"},
	},
	Examples: []capability.Example{{
		Description: "List one folder below the fixed root of this connection; omitting the path lists that root itself",
		Arguments:   json.RawMessage(`{"path":"2026"}`),
	}},
}

var filesStat = capability.Descriptor{
	ID:      Provider + ".files.stat",
	Version: 1,
	Title:   "Get Nextcloud file metadata",
	Description: "Read the metadata of exactly one file or folder below the fixed root folder of an " +
		"explicit Nextcloud connection; file content is never read",
	Tags:     []string{"nextcloud", "files", "webdav", "stat", "metadata"},
	Risk:     nextcloudReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"path":` + pathSchema + `},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(entrySchema),
	Arguments: []capability.Argument{
		{Name: "path", Description: "File or folder relative to the fixed root folder of this connection; the root itself when omitted"},
	},
	Fields: entryFields,
	Examples: []capability.Example{{
		Description: "Read the metadata of one file a listing reported",
		Arguments:   json.RawMessage(`{"path":"Reports/2026/q1.pdf"}`),
	}},
}

var filesGet = capability.Descriptor{
	ID: Provider + ".files.get", Version: 1, Title: "Read or download a Nextcloud file",
	Description: "Read one file below the fixed root of a connection, inline as base64 up to 4 MiB or written to " +
		"local_path in a directory the connection releases for writing; the content of a local download is never " +
		"returned, only its metadata. An existing local file is replaced only with confirmation",
	Tags: []string{"nextcloud", "files", "webdav", "get", "content", "download", "local"}, Risk: nextcloudReadRisk, Provider: Provider,
	LocalFiles: config.LocalFilesWrite,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"path":` + pathSchema + `,` +
		`"` + localfile.LocalPathArgument + `":` + localfile.LocalPathSchema + `},"required":["path"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"name":{"type":"string"},"content_base64":{"type":"string"},"content_type":{"type":"string"},"size":{"type":"integer"},"sha256":{"type":"string"},"etag":{"type":"string"}},"required":["path","size"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "path", Description: "File relative to the fixed root folder of this connection", Required: true},
		localfile.DownloadPathArgument(),
	},
	Fields: []capability.Field{
		{Name: "path", Description: "Path of the file below the root folder"},
		{Name: "name", Description: "Name of the file, untrusted data; only with local_path"},
		{Name: "content_base64", Description: "File content as base64; only without local_path"},
		{Name: "content_type", Description: "MIME type Nextcloud reports, untrusted data"},
		{Name: "size", Description: "Size of the content in bytes"},
		{Name: "sha256", Description: "SHA-256 of the written content as hex; only with local_path"},
		{Name: "etag", Description: "Entity tag of the version that was read"},
	},
	Examples: []capability.Example{{Description: "Write one file to a released local directory",
		Arguments: json.RawMessage(`{"path":"Reports/q1.pdf","local_path":"~/downloads/q1.pdf"}`)}},
}

var filesCreate = uploadDescriptor(fileMutationDescriptor("create", capability.EffectCreate, `{"type":"object","properties":{"path":`+pathSchema+`,"content_base64":{"type":"string","maxLength":5592408},"`+localfile.LocalPathArgument+`":`+localfile.LocalPathSchema+`},"required":["path"],"additionalProperties":false}`), false)
var filesUpdate = uploadDescriptor(fileMutationDescriptor("update", capability.EffectUpdate, `{"type":"object","properties":{"path":`+pathSchema+`,"content_base64":{"type":"string","maxLength":5592408},"`+localfile.LocalPathArgument+`":`+localfile.LocalPathSchema+`,"etag":{"type":"string","minLength":1,"maxLength":1024,"pattern":"[^*\\s\"]","x-form":"the ETag of an existing version, never *"}},"required":["path","etag"],"additionalProperties":false}`), true)
var filesDelete = allowListOnly(withArguments(fileMutationDescriptor("delete", capability.EffectDelete, `{"type":"object","properties":{"path":`+pathSchema+`,"etag":{"type":"string","minLength":1,"maxLength":1024}},"required":["path","etag"],"additionalProperties":false}`),
	capability.Argument{Name: "path", Description: "File relative to the fixed root folder of this connection", Required: true},
	capability.Argument{Name: "etag", Description: "Entity tag of the version to delete", Required: true}))

// allowListOnly marks the file delete as reachable only through a tools list, so no profile and no
// permission alone offers it.
func allowListOnly(d capability.Descriptor) capability.Descriptor {
	d.Version = 2
	d.RequiresToolAllowList = true
	d.Description += "; Nextcloud moves it to the trash bin when the files_trashbin app is active, otherwise it is deleted for good"
	return d
}

func withArguments(d capability.Descriptor, arguments ...capability.Argument) capability.Descriptor {
	d.Arguments = arguments
	return d
}

func fileMutationDescriptor(action string, effect capability.Effect, input string) capability.Descriptor {
	return capability.Descriptor{ID: Provider + ".files." + action, Version: 1,
		Title:       strings.ToUpper(action[:1]) + action[1:] + " a Nextcloud file",
		Description: strings.ToUpper(action[:1]) + action[1:] + " one file below the fixed root of a connection",
		Tags:        []string{"nextcloud", "files", "webdav", action}, Provider: Provider,
		Risk:        capability.Risk{Effect: effect, Idempotency: capability.IdempotencyIdempotent, Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
		InputSchema: json.RawMessage(input), OutputSchema: json.RawMessage(`{"type":"object","properties":{"` + action + `d":{"type":"boolean"},"etag":{"type":"string"}},"required":["` + action + `d"],"additionalProperties":false}`)}
}

// uploadDescriptor lets create and update take their content from a local file as well; a local upload
// reports only metadata.
func uploadDescriptor(d capability.Descriptor, etag bool) capability.Descriptor {
	action := strings.TrimPrefix(d.ID, Provider+".files.")
	d.LocalFiles = config.LocalFilesRead
	d.Description += "; the content comes from content_base64 up to 4 MiB or from local_path, a file in a directory " +
		"the connection releases for reading, which is then reported by metadata only; a file above 64 MiB is " +
		"uploaded in chunks"
	d.OutputSchema = json.RawMessage(`{"type":"object","properties":{"` + action + `d":{"type":"boolean"},"etag":{"type":"string"},` +
		`"path":{"type":"string"},"name":{"type":"string"},"size":{"type":"integer"},"sha256":{"type":"string"},` +
		`"method":{"type":"string","enum":["single","chunked"]}},` +
		`"required":["` + action + `d"],"additionalProperties":false}`)
	d.Arguments = []capability.Argument{
		{Name: "path", Description: "File relative to the fixed root folder of this connection", Required: true},
		{Name: localfile.ContentArgument, Description: "File content as base64, up to 4 MiB; instead of " + localfile.LocalPathArgument},
		localfile.UploadPathArgument(),
	}
	if etag {
		d.Arguments = append(d.Arguments, capability.Argument{Name: "etag", Description: "Entity tag of the version to replace", Required: true})
	}
	return d
}

// Register adds Nextcloud metadata, its read-only connection test, and the bounded file operations.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Nextcloud", DefaultPermissions: []config.Permission{config.PermissionRead},
		Description: "Self-hosted file sync, sharing, and collaboration platform",
		SecretRoles: []config.SecretRole{{
			Name: roleUserID,
			Description: "Nextcloud user ID of the identity to act as: the value shown as username in " +
				"Settings, Personal info, not the display name and not an email address unless the " +
				"account uses one as its user ID",
		}, {
			Name: roleAppPassword,
			Description: "Nextcloud app password of the same identity: Settings, Security, Devices and " +
				"sessions, create a new app password; it is revocable on its own and is what Nextcloud " +
				"expects from a WebDAV client, especially with two-factor or external authentication",
		}},
		Groups: toolGroups,
		Target: config.TargetMetadata{
			Label:    "targets",
			Required: true,
			Multiple: true,
			Description: "what this connection may reach: a Files folder as folder/PATH (folder alone is the " +
				"whole Files root), and calendar, addressbook, talk, deck, notes, account, or admin targets; a " +
				"single target field holds only a root folder such as Reports or Team/Reports, or / for the whole Files root",
			Kinds:          targetKinds,
			Validate:       validateTarget,
			ValidateSet:    validateTargetSet,
			ValidateSingle: validateSingleTarget,
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read files", Recommended: true,
			Description: "lists folders, reads file metadata and content; changes nothing below the root folder",
			Tools: []string{filesList.ID, filesStat.ID, filesGet.ID, sharesList.ID, sharesGet.ID,
				versionsList.ID, filesSearch.ID, favoritesList.ID, filesTagsList.ID},
		}, {
			ID: "write", Title: "Read and organise files",
			Description: "lists folders, reads files, creates folders, and moves, renames, or copies files and folders without overwriting",
			Tools: []string{filesList.ID, filesStat.ID, filesGet.ID, sharesList.ID, sharesGet.ID,
				versionsList.ID, filesSearch.ID, favoritesList.ID, filesTagsList.ID,
				foldersCreate.ID, filesMove.ID, filesCopy.ID},
		}, {
			ID: "talk-read", Title: "Read Talk conversations",
			Description: "lists bound Talk conversations and their participants and reads their messages; changes nothing",
			Tools:       []string{talkRoomsList.ID, talkRoomsGet.ID, talkParticipantsList.ID, talkMessagesList.ID},
		}},
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: grouped(filesList), Handler: folderBound(invokeFilesList)},
		capability.Operation{Descriptor: grouped(filesStat), Handler: folderBound(invokeFilesStat)},
		capability.Operation{Descriptor: grouped(filesGet), Handler: folderBound(invokeFilesGet)},
		capability.Operation{Descriptor: grouped(filesCreate), Handler: folderBound(invokeFilesCreate)},
		capability.Operation{Descriptor: grouped(filesUpdate), Handler: folderBound(invokeFilesUpdate)},
		capability.Operation{Descriptor: grouped(filesDelete), Handler: folderBound(invokeFilesDelete)},
		capability.Operation{Descriptor: grouped(foldersCreate), Handler: folderBound(invokeFoldersCreate)},
		capability.Operation{Descriptor: grouped(foldersDelete), Handler: folderBound(invokeFoldersDelete)},
		capability.Operation{Descriptor: grouped(filesMove), Handler: folderBound(invokeFilesMove)},
		capability.Operation{Descriptor: grouped(filesCopy), Handler: folderBound(invokeFilesCopy)},
		capability.Operation{Descriptor: grouped(versionsList), Handler: folderBound(invokeVersionsList)},
		capability.Operation{Descriptor: grouped(versionsGet), Handler: folderBound(invokeVersionsGet)},
		capability.Operation{Descriptor: grouped(versionsRestore), Handler: folderBound(invokeVersionsRestore)},
		capability.Operation{Descriptor: inGroup(sharesList, groupShares), Handler: folderBound(invokeSharesList)},
		capability.Operation{Descriptor: inGroup(sharesGet, groupShares), Handler: folderBound(invokeSharesGet)},
		capability.Operation{Descriptor: inGroup(shareesSearch, groupShares), Handler: accountBound(invokeShareesSearch)},
		capability.Operation{Descriptor: grouped(systemtagsList), Handler: accountBound(invokeSystemTagsList)},
		capability.Operation{Descriptor: grouped(systemtagsCreate), Handler: accountBound(invokeSystemTagsCreate)},
		capability.Operation{Descriptor: grouped(systemtagsUpdate), Handler: accountBound(invokeSystemTagsUpdate)},
		capability.Operation{Descriptor: grouped(systemtagsDelete), Handler: accountBound(invokeSystemTagsDelete)},
		capability.Operation{Descriptor: grouped(filesTagsList), Handler: folderBound(invokeFilesTagsList)},
		capability.Operation{Descriptor: grouped(filesTagsAdd), Handler: folderBound(invokeFilesTagsAdd)},
		capability.Operation{Descriptor: grouped(filesTagsRemove), Handler: folderBound(invokeFilesTagsRemove)},
		capability.Operation{Descriptor: grouped(trashList), Handler: folderBound(invokeTrashList)},
		capability.Operation{Descriptor: grouped(trashRestore), Handler: folderBound(invokeTrashRestore)},
		capability.Operation{Descriptor: grouped(trashDelete), Handler: folderBound(invokeTrashDelete)},
		capability.Operation{Descriptor: grouped(filesSearch), Handler: folderBound(invokeFilesSearch)},
		capability.Operation{Descriptor: grouped(favoritesList), Handler: folderBound(invokeFavoritesList)},
		capability.Operation{Descriptor: grouped(filesFavorite), Handler: folderBound(invokeFilesFavorite)},
		capability.Operation{Descriptor: inGroup(talkRoomsList, groupTalk), Handler: invokeTalkRoomsList},
		capability.Operation{Descriptor: inGroup(talkRoomsGet, groupTalk), Handler: invokeTalkRoomsGet},
		capability.Operation{Descriptor: inGroup(talkParticipantsList, groupTalk), Handler: invokeTalkParticipantsList},
		capability.Operation{Descriptor: inGroup(talkMessagesList, groupTalk), Handler: invokeTalkMessagesList},
	)
}

// grouped sorts a Files tool into the files group.
func grouped(d capability.Descriptor) capability.Descriptor { return inGroup(d, groupFiles) }

func inGroup(d capability.Descriptor, group string) capability.Descriptor {
	d.Group = group
	return d
}

// arguments are the only inputs either operation accepts. The instance, the identity, and the root folder
// are configuration, so nothing here can move a request outside the selected connection.
type arguments struct {
	Path string `json:"path"`
}

func invokeFilesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input arguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list files", "the validated arguments could not be read")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListFiles(ctx, input.Path)
}

func invokeFilesStat(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input arguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("stat file", "the validated arguments could not be read")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.StatFile(ctx, input.Path)
}

type contentArguments struct {
	Path      string  `json:"path"`
	Content   *string `json:"content_base64"`
	LocalPath *string `json:"local_path"`
	ETag      string  `json:"etag"`
}

func openForContent(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (*Client, contentArguments, error) {
	var input contentArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, input, providerError("file operation", "the validated arguments could not be read")
	}
	client, err := Open(ctx, resolved, secrets, red)
	return client, input, err
}

func invokeFilesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input contentArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("get file", "the validated arguments could not be read")
	}
	if rel, err := splitRelative(input.Path); err != nil || len(rel) == 0 {
		return nil, providerError("get file", "a file path below the connection root is required")
	}
	if input.LocalPath == nil {
		client, err := Open(ctx, resolved, secrets, red)
		if err != nil {
			return nil, err
		}
		return client.GetFile(ctx, input.Path)
	}
	// The local target is prepared before the credential is resolved, so a path outside the release is
	// refused without secret access or provider I/O.
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
	result, err := client.DownloadFile(ctx, input.Path, download, &done)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func invokeFilesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeUpload(ctx, resolved, secrets, red, raw, "create file", "created", true)
}

func invokeFilesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeUpload(ctx, resolved, secrets, red, raw, "update file", "updated", false)
}

// invokeUpload serves create and update. Exactly one content source is accepted, and a local file is
// opened before the credential is resolved.
func invokeUpload(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage, op, key string, create bool) (any, error) {
	var input contentArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	match := ""
	if !create {
		match = input.ETag
		if !validETag(match) {
			return nil, providerError(op, "etag is not a usable file version")
		}
	}
	kind, err := localfile.UploadSource(input.LocalPath, input.Content)
	if err != nil {
		return nil, err
	}
	if kind == localfile.SourceContent {
		client, err := Open(ctx, resolved, secrets, red)
		if err != nil {
			return nil, err
		}
		etag, err := client.PutFile(ctx, op, input.Path, *input.Content, create, match)
		if err != nil {
			return nil, err
		}
		return map[string]any{key: true, "etag": etag}, nil
	}
	rel, err := splitRelative(input.Path)
	if err != nil || len(rel) == 0 {
		return nil, providerError(op, "a file path below the connection root is required")
	}
	upload, err := localfile.OpenForUpload(ctx, resolved, *input.LocalPath)
	if err != nil {
		return nil, err
	}
	defer upload.Close()
	chunked := upload.Size > maxPathUploadBytes
	if chunked && chunkCount(upload.Size) > maxChunks {
		return nil, providerError(op, "the local file needs more chunks than one Nextcloud upload allows")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	method := "single"
	var etag string
	if chunked {
		method = "chunked"
		etag, err = client.uploadChunked(ctx, op, rel, upload, create, match)
	} else {
		etag, err = client.putStream(ctx, op, rel, upload, create, match)
	}
	if err != nil {
		return nil, err
	}
	sum, ok := upload.SHA256()
	if !ok {
		return nil, providerError(op, "the local file changed while it was read")
	}
	return map[string]any{key: true, "etag": etag, "path": input.Path, "name": rel[len(rel)-1],
		"size": upload.Size, "sha256": sum, "method": method}, nil
}

func invokeFoldersDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input contentArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("delete folder", "the validated arguments could not be read")
	}
	// Refused before the credential is resolved: the root is never deletable and `*` is no version.
	if rel, err := splitRelative(input.Path); err != nil || len(rel) == 0 {
		return nil, providerError("delete folder", "a folder path below the connection root is required")
	}
	if !validETag(input.ETag) {
		return nil, providerError("delete folder", "etag is not a usable folder version")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.DeleteFolder(ctx, input.Path, input.ETag); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}

func invokeFilesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, input, err := openForContent(ctx, resolved, secrets, red, raw)
	if err != nil {
		return nil, err
	}
	if err := client.DeleteFile(ctx, input.Path, input.ETag); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}

// Client binds one identity of one configured Nextcloud instance to the fixed root folder of one
// connection. A client is opened per request by the application core and is never shared.
type Client struct {
	origin string
	// prefix are the decoded path segments up to and including the user ID: the optional installation
	// path, the fixed Files WebDAV segments, and the identity.
	prefix []string
	// uploads are the decoded segments of the upload area of the same identity, below which chunked
	// uploads create their one folder.
	uploads []string
	// trashbin are the decoded segments of the trash bin of the same identity.
	trashbin []string
	// install are the decoded segments of the optional installation path, below which the OCS API lives.
	install []string
	// user is the identity, which tells an own share from an incoming one.
	user string
	// root are the decoded segments of the fixed root folder below the Files root of that identity.
	root []string
	auth string
	http *http.Client
}

// Open resolves the identity of one selected connection and returns a client bound to its root folder.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return open(ctx, resolved, secrets, red, true)
}

// open resolves the identity. A client for the Files tools needs a folder target and refuses without one
// before any credential access; the connection test may run without it and then addresses the Files root
// of the identity.
func open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, needFolder bool) (*Client, error) {
	const op = "open"
	if resolved == nil {
		return nil, providerError(op, "no connection was selected")
	}
	origin, install, err := parseInstance(resolved.BaseURL)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	bound, err := scopeOf(resolved)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	if needFolder && !bound.hasFolder {
		_, err := requireFolder(resolved)
		return nil, err
	}
	root := bound.folder
	if secrets == nil {
		return nil, providerError(op, "no credential resolver was configured")
	}

	userID, err := role(ctx, resolved, secrets, roleUserID)
	if err != nil {
		return nil, err
	}
	if !validUserID(userID) {
		return nil, &provider.Error{
			Class: provider.ClassAuth, Op: op, Message: "the Nextcloud user ID is unusable",
		}
	}
	password, err := role(ctx, resolved, secrets, roleAppPassword)
	if err != nil {
		return nil, err
	}
	if !validPassword(password) {
		return nil, &provider.Error{
			Class: provider.ClassAuth, Op: op, Message: "the Nextcloud app password is unusable",
		}
	}

	// Every value that could carry the app password is registered before it can reach a diagnostic: the
	// password itself, the basic-auth pair, its encoding, and the complete header.
	pair := userID + ":" + password
	encoded := base64.StdEncoding.EncodeToString([]byte(pair))
	header := "Basic " + encoded
	if red != nil {
		red.Add(password, pair, encoded, header)
	}

	prefix := append(append([]string{}, install...), filesRoot...)
	prefix = append(prefix, userID)
	uploads := append(append([]string{}, install...), uploadsRoot...)
	uploads = append(uploads, userID)
	trashbin := append(append([]string{}, install...), trashbinRoot...)
	trashbin = append(trashbin, userID)
	client := &Client{origin: origin, prefix: prefix, uploads: uploads, trashbin: trashbin, install: install,
		user: userID, root: root, auth: header}
	client.http = provider.NoRedirectClient(defaultTimeout, transport)
	return client, nil
}

// role resolves one secret role of the connection. Which stage of the cascade delivers is not this
// provider's business: it needs the value, and the resolver decides where it comes from.
func role(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, name string) (string, error) {
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, name)
	if err != nil {
		return "", err
	}
	return value.Secret, nil
}

// parseInstance validates the configured service and splits it into the origin and the optional
// installation path. A Nextcloud instance may live below a path such as /nextcloud, so the path is kept,
// but userinfo, a query, or a fragment would either carry a credential or rewrite every request.
func parseInstance(raw string) (string, []string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", nil, errors.New("a Nextcloud service needs a usable https URL")
	}
	if parsed.Scheme != "https" {
		return "", nil, errors.New("a Nextcloud service must use https")
	}
	if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Opaque != "" {
		return "", nil, errors.New(
			"a Nextcloud service must be an https URL without user, query, or fragment")
	}
	install, err := splitConfigured(parsed.Path)
	if err != nil {
		return "", nil, fmt.Errorf("the installation path of this Nextcloud service is unusable: %w", err)
	}
	return parsed.Scheme + "://" + parsed.Host, install, nil
}

// parseRoot reads the fixed root folder one connection is bound to. A single slash binds the whole Files
// root of the identity; anything else is a folder below it.
func parseRoot(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("a Nextcloud connection needs a fixed root folder as its target")
	}
	segments, err := splitConfigured(trimmed)
	if err != nil {
		return nil, fmt.Errorf("the configured Nextcloud root folder is unusable: %w", err)
	}
	return segments, nil
}

// splitConfigured reads a configured path into decoded segments. A leading and a trailing slash are
// accepted here, because a configuration field is written by a person; everything a request could be
// moved by is not.
func splitConfigured(raw string) ([]string, error) {
	trimmed := strings.Trim(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return nil, nil
	}
	return splitRelative(trimmed)
}

// splitRelative reads one path relative to the connection root into its segments. It mirrors the schema
// pattern in Go, so a direct caller stays inside the same rules as an agent request, and it adds the
// checks a regular expression cannot express safely.
func splitRelative(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > maxPathLength {
		return nil, errors.New("the path is too long")
	}
	if strings.HasPrefix(raw, "/") {
		return nil, errors.New("a path must be relative to the root folder of this connection")
	}
	if strings.Contains(raw, "://") {
		return nil, errors.New("a path must be relative to the root folder of this connection, not a URL")
	}
	segments := strings.Split(raw, "/")
	if len(segments) > maxSegments {
		return nil, errors.New("the path has too many components")
	}
	for _, segment := range segments {
		if err := checkSegment(segment); err != nil {
			return nil, err
		}
	}
	return segments, nil
}

// checkSegment rejects everything that could leave the connection root or change how the server resolves
// the path: an empty component, a relative component, a separator in any spelling, and a control
// character. Percent signs are refused outright, so no input can be encoded twice or smuggle a separator.
func checkSegment(segment string) error {
	switch {
	case segment == "":
		return errors.New("a path must not contain an empty component")
	case segment == "." || segment == "..":
		return errors.New("a path must not contain a relative component")
	case len(segment) > maxSegmentLen:
		return errors.New("a path component is too long")
	}
	for _, r := range segment {
		switch {
		case r == '/' || r == '\\':
			return errors.New("a path component must not contain a separator")
		case r == '%':
			return errors.New("a path must be written literally, not percent-encoded")
		case r < 0x20 || r == 0x7f:
			return errors.New("a path component must not contain control characters")
		}
	}
	return nil
}

// escapePath encodes decoded segments into a request path, escaping every segment exactly once.
func escapePath(segments []string) string {
	escaped := make([]string, len(segments))
	for i, segment := range segments {
		escaped[i] = url.PathEscape(segment)
	}
	return "/" + strings.Join(escaped, "/")
}

// requestURL builds the absolute URL of one node below the connection root. The origin, the installation
// path, the identity, and the root folder come from the configuration; only rel comes from the request.
func (c *Client) requestURL(rel []string) string {
	segments := append(append(append([]string{}, c.prefix...), c.root...), rel...)
	path := escapePath(segments)
	if len(c.root)+len(rel) == 0 {
		// The Files root of an identity is addressed as a collection.
		path += "/"
	}
	return c.origin + path
}

// transport carries every Nextcloud request. A nil value is Go's default transport; the package's own
// tests replace it with recorded responses.
var transport http.RoundTripper

// TestConnection performs the smallest safe authenticated read: one PROPFIND of depth 0 on the fixed root
// folder, or on the Files root of the identity when the connection binds no folder. It proves that the
// instance answers Files WebDAV, that the app password is accepted, and, with a folder, that the identity
// may read it. Nothing is written, no content is read, and no metadata is reported.
func TestConnection(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (provider.Class, error) {
	client, err := open(ctx, resolved, secrets, red, false)
	if err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return "", err
	}
	return client.testConnection(ctx)
}

func (c *Client) testConnection(ctx context.Context) (provider.Class, error) {
	const op = "test connection"

	entry, err := c.stat(ctx, op, nil, true)
	if err != nil {
		var providerErr *provider.Error
		if !errors.As(err, &providerErr) {
			return provider.ClassProviderError, nil
		}
		// A missing root is reported with its own explanation, because no stable class can say that the
		// instance and the credential are fine while the configured folder is not there.
		if providerErr.Class == provider.ClassProviderError && providerErr.Message == messageNotFound {
			if len(c.root) == 0 {
				return "", errors.New("this Nextcloud identity has no Files root at this instance")
			}
			return "", errors.New(
				"this Nextcloud identity does not hold the root folder this connection is bound to")
		}
		return providerErr.Class, nil
	}
	if entry.Type != typeFolder {
		return "", errors.New("the configured Nextcloud root is a file, not a folder")
	}
	if !entry.Readable {
		return "", errors.New("this Nextcloud identity may not read the configured root folder")
	}
	return provider.ClassOK, nil
}

// The two node types this provider reports.
const (
	typeFile   = "file"
	typeFolder = "folder"
)

// Entry is the stable Qatlas view of one file or folder: where it sits below the connection root, what
// it is called, and the fixed metadata set the adapter asks for. Content is never part of it.
type Entry struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size"`
	ModifiedAt  string `json:"modified_at,omitempty"`
	ETag        string `json:"etag,omitempty"`
	FileID      string `json:"file_id,omitempty"`
	Readable    bool   `json:"readable"`
}

// ListResult is the normalised, one level deep listing of one folder below the connection root.
type ListResult struct {
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
	Count   int     `json:"count"`
}

type Content struct {
	Path          string `json:"path"`
	ContentBase64 string `json:"content_base64"`
	ContentType   string `json:"content_type,omitempty"`
	Size          int    `json:"size"`
	ETag          string `json:"etag,omitempty"`
}

// ListFiles reads the immediate children of the connection root, or of one folder below it, with a single
// PROPFIND of depth 1. The requested folder itself is normalised out of the children.
func (c *Client) ListFiles(ctx context.Context, path string) (*ListResult, error) {
	const op = "list files"
	rel, err := splitRelative(path)
	if err != nil {
		return nil, providerError(op, err.Error())
	}

	resources, err := c.propfind(ctx, op, rel, depthChildren)
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(resources))
	var self *Entry
	for i := range resources {
		relative, err := c.relativeOf(op, resources[i].href)
		if err != nil {
			return nil, err
		}
		switch {
		case len(relative) == len(rel):
			if !equalSegments(relative, rel) {
				return nil, invalidResponse(op, messageForeignEntry)
			}
			if self != nil {
				return nil, invalidResponse(op, "Nextcloud reported the requested folder twice")
			}
			if err := resources[i].failure(op); err != nil {
				return nil, err
			}
			self = c.entryOf(relative, &resources[i])
		case len(relative) == len(rel)+1 && equalSegments(relative[:len(rel)], rel):
			// A child whose properties the server refused carries no usable metadata, so it is left out
			// rather than reported as an entry the caller could act on.
			if resources[i].failure(op) == nil {
				entries = append(entries, *c.entryOf(relative, &resources[i]))
			}
		default:
			return nil, invalidResponse(op, messageForeignEntry)
		}
	}

	if self == nil {
		return nil, invalidResponse(op, "Nextcloud answered without the requested folder")
	}
	if self.Type != typeFolder {
		return nil, providerError(op, "this path is a file; read it with "+filesStat.ID)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return &ListResult{Path: self.Path, Entries: entries, Count: len(entries)}, nil
}

// StatFile reads the metadata of exactly one node below the connection root with a single PROPFIND of
// depth 0.
func (c *Client) StatFile(ctx context.Context, path string) (*Entry, error) {
	const op = "stat file"
	rel, err := splitRelative(path)
	if err != nil {
		return nil, providerError(op, err.Error())
	}
	return c.stat(ctx, op, rel, false)
}

func (c *Client) GetFile(ctx context.Context, path string) (*Content, error) {
	rel, err := splitRelative(path)
	if err != nil || len(rel) == 0 {
		return nil, providerError("get file", "a file path below the connection root is required")
	}
	body, header, err := c.readInline(ctx, "get file", c.requestURL(rel), "the Nextcloud file exceeds 4 MiB, use local_path")
	if err != nil {
		return nil, err
	}
	return &Content{Path: path, ContentBase64: base64.StdEncoding.EncodeToString(body), ContentType: bounded(header.Get("Content-Type")), Size: len(body), ETag: bounded(strings.Trim(header.Get("ETag"), `"`))}, nil
}

// readInline reads the GET of an absolute URL, built from checked segments, up to the inline limit.
func (c *Client) readInline(ctx context.Context, op, target, tooLarge string) ([]byte, http.Header, error) {
	response, err := c.webdavTo(ctx, op, http.MethodGet, target, nil, "", "", "", nil)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxFileBytes+1))
	if err != nil || len(body) > maxFileBytes {
		return nil, nil, invalidResponse(op, tooLarge)
	}
	return body, response.Header, nil
}

// transferClient is the client for a request that moves file content to or from a local path: the same
// client, which never follows a redirect, with a longer timeout.
func (c *Client) transferClient() *http.Client {
	client := *c.http
	client.Timeout = transferTimeout
	return &client
}

// putStream sends one PUT with the local file as its body. The result of a failed request is never
// retried.
func (c *Client) putStream(ctx context.Context, op string, rel []string, upload *localfile.Upload, create bool, match string) (string, error) {
	var body io.Reader = http.NoBody
	if upload.Size > 0 {
		body = upload
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.requestURL(rel), body)
	if err != nil {
		return "", providerError(op, "the request could not be built")
	}
	req.ContentLength = upload.Size
	req.Header.Set("Authorization", c.auth)
	if create {
		req.Header.Set("If-None-Match", "*")
	} else {
		req.Header.Set("If-Match", `"`+strings.Trim(match, `"`)+`"`)
	}
	response, err := c.transferClient().Do(req)
	if err != nil {
		return "", sentTransportError(op, err, uncertainStored)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", sentStatusError(op, response.StatusCode, uncertainStored)
	}
	return bounded(strings.Trim(response.Header.Get("ETag"), `"`)), nil
}

// DownloadResult is what get reports for a local download: metadata of the written file, never content.
type DownloadResult struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	ContentType string `json:"content_type,omitempty"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	ETag        string `json:"etag,omitempty"`
}

// DownloadFile checks the node below the connection root, then streams it into the prepared local file.
// done is set once the download is committed or must no longer be aborted by the caller.
func (c *Client) DownloadFile(ctx context.Context, path string, download *localfile.Download, done *bool) (*DownloadResult, error) {
	const op = "get file"
	rel, err := splitRelative(path)
	if err != nil || len(rel) == 0 {
		return nil, providerError(op, "a file path below the connection root is required")
	}
	entry, err := c.stat(ctx, op, rel, false)
	if err != nil {
		return nil, err
	}
	if entry.Type != typeFile {
		return nil, providerError(op, "only a file can be downloaded")
	}
	header, err := c.downloadTo(ctx, op, c.requestURL(rel), download, done)
	if err != nil {
		return nil, err
	}
	sum, _ := download.SHA256()
	etag := entry.ETag
	if etag == "" {
		etag = bounded(strings.Trim(header.Get("ETag"), `"`))
	}
	return &DownloadResult{Path: path, Name: entry.Name, ContentType: bounded(header.Get("Content-Type")),
		Size: download.Size(), SHA256: sum, ETag: etag}, nil
}

// downloadTo streams the GET of an absolute URL, built from checked segments, into the prepared local file
// and commits it. It returns the headers of the answer.
func (c *Client) downloadTo(ctx context.Context, op, target string, download *localfile.Download, done *bool) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	response, err := c.transferClient().Do(req)
	if err != nil {
		return nil, provider.Transport(op, "Nextcloud", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, statusError(op, response.StatusCode)
	}
	if response.ContentLength >= 0 {
		if err := download.ExpectSize(response.ContentLength); err != nil {
			return nil, err
		}
	}
	if _, err := download.ReadFrom(response.Body); err != nil {
		var integrity *localfile.IntegrityError
		var pathErr *localfile.PathError
		if errors.As(err, &integrity) || errors.As(err, &pathErr) {
			return nil, err
		}
		return nil, providerError(op, "the transfer ended before the file was complete")
	}
	*done = true
	if err := download.Commit(); err != nil {
		return nil, err
	}
	return response.Header, nil
}

func (c *Client) PutFile(ctx context.Context, op, path, encoded string, create bool, match string) (string, error) {
	rel, err := splitRelative(path)
	if err != nil || len(rel) == 0 {
		return "", providerError(op, "a file path below the connection root is required")
	}
	content, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(content) > maxFileBytes {
		return "", providerError(op, "content_base64 is invalid or exceeds the size limit")
	}
	header := "If-None-Match"
	if !create {
		if !validETag(match) {
			return "", providerError(op, "etag is not a usable file version")
		}
		header = "If-Match"
	}
	response, err := c.webdav(ctx, op, http.MethodPut, rel, strings.NewReader(string(content)), header, match, uncertainStored)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	return bounded(strings.Trim(response.Header.Get("ETag"), `"`)), nil
}

func (c *Client) DeleteFile(ctx context.Context, path, etag string) error {
	rel, err := splitRelative(path)
	if err != nil || len(rel) == 0 {
		return providerError("delete file", "a file path below the connection root is required")
	}
	if !validETag(etag) {
		return providerError("delete file", "etag is not a usable file version")
	}
	entry, err := c.stat(ctx, "delete file", rel, false)
	if err != nil {
		return err
	}
	if entry.Type != typeFile {
		return providerError("delete file", "folders cannot be deleted by this operation")
	}
	response, err := c.webdav(ctx, "delete file", http.MethodDelete, rel, nil, "If-Match", etag, uncertainDeleted)
	if err != nil {
		return err
	}
	response.Body.Close()
	return nil
}

// DeleteFolder deletes a folder with all its content, bound to the ETag the caller read. The root is never
// deletable, and a path that is no folder is refused before the one DELETE.
func (c *Client) DeleteFolder(ctx context.Context, path, etag string) error {
	const op = "delete folder"
	rel, err := splitRelative(path)
	if err != nil || len(rel) == 0 {
		return providerError(op, "a folder path below the connection root is required")
	}
	if !validETag(etag) {
		return providerError(op, "etag is not a usable folder version")
	}
	entry, err := c.stat(ctx, op, rel, false)
	if err != nil {
		return err
	}
	if entry.Type != typeFolder {
		return providerError(op, "only folders can be deleted by this operation")
	}
	response, err := c.webdav(ctx, op, http.MethodDelete, rel, nil, "If-Match", etag, uncertainFolderDeleted)
	if err != nil {
		return err
	}
	response.Body.Close()
	return nil
}

func validETag(value string) bool {
	trimmed := strings.Trim(value, `"`)
	if strings.TrimSpace(trimmed) == "" || trimmed == "*" || len(trimmed) > maxValueLength {
		return false
	}
	for _, r := range trimmed {
		if r < 0x20 || r == 0x7f || r == '"' {
			return false
		}
	}
	return true
}

func (c *Client) webdav(ctx context.Context, op, method string, rel []string, body io.Reader, condition, value, uncertain string) (*http.Response, error) {
	return c.webdavWith(ctx, op, method, rel, body, condition, value, uncertain, nil)
}

// webdavWith is webdav with fixed extra request headers, which the calling operation builds itself.
func (c *Client) webdavWith(ctx context.Context, op, method string, rel []string, body io.Reader, condition, value, uncertain string, extra http.Header) (*http.Response, error) {
	return c.webdavTo(ctx, op, method, c.requestURL(rel), body, condition, value, uncertain, extra)
}

// webdavTo sends one request to an absolute URL the calling operation built from the configured origin.
func (c *Client) webdavTo(ctx context.Context, op, method, target string, body io.Reader, condition, value, uncertain string, extra http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	for name, values := range extra {
		req.Header[name] = values
	}
	if condition == "If-None-Match" {
		req.Header.Set(condition, "*")
	} else if condition != "" {
		req.Header.Set(condition, `"`+strings.Trim(value, `"`)+`"`)
	}
	response, err := c.http.Do(req)
	if err != nil {
		if uncertain != "" {
			return nil, sentTransportError(op, err, uncertain)
		}
		return nil, provider.Transport(op, "Nextcloud", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		if uncertain != "" {
			return nil, sentStatusError(op, response.StatusCode, uncertain)
		}
		return nil, statusError(op, response.StatusCode)
	}
	return response, nil
}

// stat performs one depth 0 PROPFIND and normalises the single node it must answer with. Only the
// connection test asks for the capability check as well; a normal read must not fail because a proxy
// dropped a header.
func (c *Client) stat(ctx context.Context, op string, rel []string, capabilities bool) (*Entry, error) {
	resources, err := c.propfindWith(ctx, op, rel, depthSelf, capabilities)
	if err != nil {
		return nil, err
	}
	if len(resources) != 1 {
		return nil, invalidResponse(op, "Nextcloud answered with more than the requested node")
	}
	relative, err := c.relativeOf(op, resources[0].href)
	if err != nil {
		return nil, err
	}
	if !equalSegments(relative, rel) {
		return nil, invalidResponse(op, messageForeignEntry)
	}
	if err := resources[0].failure(op); err != nil {
		return nil, err
	}
	return c.entryOf(relative, &resources[0]), nil
}

// entryOf normalises one multi-status resource into the stable envelope. Every value stays untrusted
// provider data; only its length, its form, and its meaning are normalised.
func (c *Client) entryOf(relative []string, res *resource) *Entry {
	entry := &Entry{
		Path:     strings.Join(relative, "/"),
		Name:     bounded(res.props[propDisplayName]),
		Type:     typeFile,
		ETag:     bounded(strings.Trim(res.props[propETag], `"`)),
		Readable: readable(res.props[propPermissions]),
	}
	if res.collection {
		entry.Type = typeFolder
	}
	if entry.Name == "" {
		entry.Name = lastSegment(relative, c.root)
	}
	if entry.Type == typeFile {
		entry.ContentType = bounded(res.props[propContentType])
		entry.Size = number(res.props[propContentLength])
	} else {
		entry.Size = number(res.props[propSize])
	}
	if id := res.props[propFileID]; digitsOnly(id) {
		entry.FileID = id
	}
	if parsed, err := http.ParseTime(res.props[propLastModified]); err == nil {
		entry.ModifiedAt = parsed.UTC().Format(time.RFC3339)
	}
	return entry
}

// lastSegment names a node whose display name the server did not report: the last segment of its path,
// and for the connection root itself the last segment of that root.
func lastSegment(relative, root []string) string {
	if len(relative) > 0 {
		return relative[len(relative)-1]
	}
	if len(root) > 0 {
		return root[len(root)-1]
	}
	return ""
}

// readable reports the effective read permission of the identity. Nextcloud spells the permissions as
// letters, and G is the readable one. A server that reports none is taken at the word of its answer: it
// delivered the metadata, so the node is readable.
func readable(permissions string) bool {
	if permissions == "" {
		return true
	}
	return strings.ContainsAny(permissions, "Gg")
}

func number(raw string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func digitsOnly(value string) bool {
	if value == "" || len(value) > 32 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

// bounded keeps an oversized provider string out of the result without interpreting it.
func bounded(value string) string {
	if len(value) > maxValueLength {
		return value[:maxValueLength]
	}
	return value
}

func equalSegments(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// propfind performs one bounded PROPFIND and returns the resources of the multi-status answer.
func (c *Client) propfind(ctx context.Context, op string, rel []string, depth string) ([]resource, error) {
	return c.propfindWith(ctx, op, rel, depth, false)
}

// propfindWith is the single request path of this adapter. It is the only place that builds an HTTP
// request, and it can build no method other than PROPFIND.
func (c *Client) propfindWith(ctx context.Context, op string, rel []string, depth string,
	capabilities bool) ([]resource, error) {
	return c.propfindAt(ctx, op, c.requestURL(rel), propfindBody, depth, capabilities, maxEntries)
}

// propfindAt sends the one PROPFIND form of this adapter to an absolute URL built from checked segments,
// with one of the fixed property bodies.
func (c *Client) propfindAt(ctx context.Context, op, target, propBody, depth string, capabilities bool,
	limit int) ([]resource, error) {
	req, err := http.NewRequestWithContext(ctx, methodPropfind, target, strings.NewReader(propBody))
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.ContentLength = int64(len(propBody))
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("Depth", depth)

	response, err := c.http.Do(req)
	if err != nil {
		return nil, provider.Transport(op, "Nextcloud", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusMultiStatus {
		return nil, statusError(op, response.StatusCode)
	}
	if capabilities {
		if err := checkWebDAV(op, response.Header); err != nil {
			return nil, err
		}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes+1))
	if err != nil || len(body) > maxBodyBytes {
		return nil, invalidResponse(op, "the Nextcloud response could not be read within the size limit")
	}
	return parseMultiStatusMax(op, body, limit)
}

// checkWebDAV verifies that the instance announces WebDAV class 1, which is what the Files app serves. A
// server that announces nothing is accepted: a proxy may drop the header, and the multi-status answer
// itself is the stronger evidence.
func checkWebDAV(op string, header http.Header) error {
	announced := header.Get("Dav")
	if strings.TrimSpace(announced) == "" {
		return nil
	}
	for _, class := range strings.Split(announced, ",") {
		if strings.TrimSpace(class) == "1" {
			return nil
		}
	}
	return invalidResponse(op, "this server does not announce the WebDAV class the Files app needs")
}

// messageNotFound is the one classified message the connection test has to recognise, so it is written
// once rather than compared as free text in two places.
const messageNotFound = "this Nextcloud connection does not hold this path"

// messageForeignEntry covers every answer that names a node outside the folder the request addressed.
const messageForeignEntry = "Nextcloud answered with an entry outside the requested folder"

// messageRedirect answers every 3xx status. Qatlas follows no redirect, so the server did not act on the
// request; the message names neither the location nor the path.
const messageRedirect = "Nextcloud answered with a redirect, which Qatlas does not follow"

func isRedirect(status int) bool { return status >= 300 && status < 400 }

// statusError maps an HTTP status to a stable class. The provider body is never read into the message:
// Nextcloud echoes the request path into it, and the class plus the status is what a caller can act on.
func statusError(op string, status int) error {
	if isRedirect(status) {
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: messageRedirect}
	}
	switch status {
	case http.StatusUnauthorized:
		return &provider.Error{
			Class: provider.ClassAuth, Op: op,
			Message: "Nextcloud rejected the user ID or the app password",
		}
	case http.StatusForbidden:
		return &provider.Error{
			Class: provider.ClassPermission, Op: op,
			Message: "this Nextcloud identity may not perform this operation on the path; check the rights of " +
				"the user on the path in Nextcloud",
		}
	case http.StatusNotFound:
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: messageNotFound}
	case http.StatusPreconditionFailed:
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: "the Nextcloud file changed or already exists"}
	case http.StatusMethodNotAllowed, http.StatusConflict:
		return &provider.Error{
			Class: provider.ClassProviderError, Op: op, Message: "Nextcloud refused this operation on this path",
		}
	case http.StatusLocked:
		return &provider.Error{
			Class: provider.ClassProviderError, Op: op, Message: "this Nextcloud node is locked",
		}
	case http.StatusInsufficientStorage:
		return &provider.Error{
			Class: provider.ClassProviderError, Op: op,
			Message: "the storage quota of this Nextcloud identity is exhausted",
		}
	case http.StatusTooManyRequests:
		return &provider.Error{
			Class: provider.ClassRateLimited, Op: op, Message: "Nextcloud rate-limited the operation",
		}
	case http.StatusServiceUnavailable:
		return &provider.Error{
			Class: provider.ClassUnreachable, Op: op,
			Message: "Nextcloud is unavailable or in maintenance mode",
		}
	case http.StatusGatewayTimeout:
		return &provider.Error{
			Class: provider.ClassTimeout, Op: op, Message: "Nextcloud did not answer in time",
		}
	default:
		return &provider.Error{
			Class: provider.ClassProviderError, Op: op,
			Message: fmt.Sprintf("Nextcloud rejected the operation (HTTP %d)", status),
		}
	}
}

// sentTransportError is transportError for a request that changes a file: whatever ended it, the server may
// have acted on it.
func sentTransportError(op string, err error, hint string) error {
	return withUncertainty(provider.Transport(op, "Nextcloud", err), hint)
}

// sentStatusError is statusError for a request that changes a file: a client error is a clear refusal, any
// other non-success status leaves the outcome open. A redirect is a clear refusal: the server did not act.
func sentStatusError(op string, status int, hint string) error {
	if isRedirect(status) || status >= 400 && status < 500 {
		return statusError(op, status)
	}
	return withUncertainty(statusError(op, status), hint)
}

func withUncertainty(err error, hint string) error {
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		changed := *providerErr
		changed.Message += hint
		return &changed
	}
	return err
}

func providerError(op, message string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

func invalidResponse(op, message string) error {
	return &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: message}
}

// validUserID keeps an unusable identity out of a request path and out of the basic-auth header. The real
// check is the provider's; this one only refuses what would change the meaning of either.
func validUserID(value string) bool {
	if value == "" || len(value) > maxUserIDLen || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		switch {
		case r == '/', r == '\\', r == ':', r == '%', r == '?', r == '#':
			return false
		case r < 0x20 || r == 0x7f:
			return false
		}
	}
	return true
}

// validPassword keeps an obviously unusable app password out of a header. A Nextcloud app password is a
// printable ASCII string; a control character in one would be a header injection, not a credential.
func validPassword(value string) bool {
	if len(value) < 8 || len(value) > maxValueLength {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x21 || value[i] > 0x7e {
			return false
		}
	}
	return true
}
