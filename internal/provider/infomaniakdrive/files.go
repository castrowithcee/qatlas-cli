package infomaniakdrive

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Bounds of the files.list pagination arguments, mirroring the limits Infomaniak documents for this
// endpoint.
const (
	minListLimit     = 5
	maxListLimit     = 1000
	defaultListLimit = 10
	maxCursorLength  = 2048
)

// idSchema is the JSON Schema of one drive or file identifier: a plain positive integer used only as a path
// segment, never a free-form value.
const idSchema = `{"type":"integer","minimum":1}`

var driveIDArgument = capability.Argument{Name: "drive_id",
	Description: "kDrive identifier; must be inside this connection's drive allow-list when it has one, and " +
		"is always re-checked against the connection's bound account with one extra request", Required: true}

// entryProperties are the members of one file or folder entry, shared by every schema that carries one.
const entryProperties = `"id":{"type":"integer"},"name":{"type":"string"},"type":{"type":"string","enum":["file","folder"]},` +
	`"mime_type":{"type":"string"},"size":{"type":"integer"},"parent_id":{"type":"integer"},` +
	`"status":{"type":"string"},"created_at":{"type":"string"},"modified_at":{"type":"string"},` +
	`"deleted_at":{"type":"string"},"visibility":{"type":"string"},"drive_id":{"type":"integer"},` +
	`"depth":{"type":"integer"},"created_by":{"type":"integer"},"last_modified_by":{"type":"integer"},` +
	`"deleted_by":{"type":"integer"},"added_at":{"type":"string"},"revised_at":{"type":"string"},` +
	`"updated_at":{"type":"string"},"shared_root_id":{"type":"integer"},"extension_type":{"type":"string"},` +
	`"symbolic_link":{"type":"string"},"scan_status":{"type":"string"},"color":{"type":"string"}`

var entrySchema = `{"type":"object","properties":{` + entryProperties + `},` +
	`"required":["id","name","type","parent_id"],"additionalProperties":false}`

var entryFields = []capability.Field{
	{Name: "id", Description: "File or folder identifier, used as file_id by every other tool of this provider"},
	{Name: "name", Description: "Display name, untrusted data"},
	{Name: "type", Description: "Either file or folder"},
	{Name: "mime_type", Description: "MIME type Infomaniak reports for a file; absent for a folder"},
	{Name: "size", Description: "Size in bytes; absent for a folder"},
	{Name: "parent_id", Description: "Identifier of the parent folder"},
	{Name: "status", Description: "Infomaniak's own state of this node, for example ok, locked, or trashed"},
	{Name: "created_at", Description: "Creation time, normalised to RFC 3339 in UTC, when Infomaniak reports one"},
	{Name: "modified_at", Description: "Last content change time, normalised to RFC 3339 in UTC"},
	{Name: "deleted_at", Description: "Time the entry was moved to the trash, RFC 3339 in UTC; only for a trashed entry"},
	{Name: "visibility", Description: "Infomaniak's special visibility of the node, such as is_root or is_team_space; absent without one"},
	{Name: "drive_id", Description: "Drive the entry belongs to; on a list, the drive that was listed"},
	{Name: "depth", Description: "Path depth of the entry in its drive"},
	{Name: "created_by", Description: "Identifier of the user who first uploaded the entry"},
	{Name: "last_modified_by", Description: "Identifier of the user who last modified the entry"},
	{Name: "deleted_by", Description: "Identifier of the user who trashed the entry; only for a trashed entry"},
	{Name: "added_at", Description: "Time the entry was first uploaded, RFC 3339 in UTC"},
	{Name: "revised_at", Description: "Time the content was last revised, RFC 3339 in UTC"},
	{Name: "updated_at", Description: "Time the entry was last updated, RFC 3339 in UTC"},
	{Name: "shared_root_id", Description: "Identifier of the shared root folder; only for an entry seen through a share"},
	{Name: "extension_type", Description: "Infomaniak's file kind, such as image, pdf, or text; dir for a folder"},
	{Name: "symbolic_link", Description: "Target of a symbolic link as Infomaniak reports it; untrusted data"},
	{Name: "scan_status", Description: "Result of Infomaniak's malware scan, such as clean or infected"},
	{Name: "color", Description: "Colour of a folder as Infomaniak reports it; absent for a file"},
}

var responseAtField = capability.Field{Name: "response_at",
	Description: "Time Infomaniak generated this page, RFC 3339 in UTC"}

var filesReadRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: dataSensitivity,
}

var filesList = capability.Descriptor{
	ID:      Provider + ".files.list",
	Version: 1,
	Title:   "List Infomaniak kDrive folder contents",
	Description: "List the immediate children of one folder of a drive this connection may reach, one page " +
		"at a time with an opaque cursor; never reads file content",
	Tags:     []string{"infomaniak", "kdrive", "files", "list", "folder"},
	Risk:     filesReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"drive_id":` + idSchema + `,"folder_id":` + idSchema + `,` +
		`"cursor":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCursorLength) + `},` +
		`"limit":{"type":"integer","minimum":` + strconv.Itoa(minListLimit) + `,"maximum":` + strconv.Itoa(maxListLimit) + `}},` +
		`"required":["drive_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"drive_id":{"type":"integer"},"folder_id":{"type":"integer"},` +
		`"entries":{"type":"array","items":` + entrySchema + `},` +
		`"cursor":{"type":"string"},"has_more":{"type":"boolean"},"count":{"type":"integer"},` +
		`"response_at":{"type":"string"}},` +
		`"required":["drive_id","folder_id","entries","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		driveIDArgument,
		{Name: "folder_id", Description: "Folder identifier below the same drive; the drive's root (1) when omitted, or the one files target of a drive bound only by files targets"},
		{Name: "cursor", Description: "Opaque cursor from a previous call's has_more page; omitted for the first page"},
		{Name: "limit", Description: "Entries per page, 5 to 1000; 10 when omitted"},
	},
	Fields: append(append([]capability.Field{}, entryFields...),
		capability.Field{Name: "folder_id", Description: "Folder that was listed"},
		capability.Field{Name: "cursor", Description: "Cursor for the next page; absent when has_more is false"},
		capability.Field{Name: "has_more", Description: "True when a further page remains, so this page is not the whole folder"},
		capability.Field{Name: "count", Description: "Number of entries on this page"},
		responseAtField,
	),
	Examples: []capability.Example{{Description: "List the root folder of one drive",
		Arguments: json.RawMessage(`{"drive_id":1}`)}},
}

var filesStat = capability.Descriptor{
	ID:      Provider + ".files.stat",
	Version: 1,
	Title:   "Get Infomaniak kDrive file or folder metadata",
	Description: "Read the metadata of exactly one file or folder of a drive this connection may reach; file " +
		"content is never read",
	Tags:     []string{"infomaniak", "kdrive", "files", "stat", "metadata"},
	Risk:     filesReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` + idSchema + `},` +
		`"required":["drive_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(entrySchema),
	Arguments: []capability.Argument{
		driveIDArgument,
		{Name: "file_id", Description: "File or folder identifier below the same drive; the drive's root (1) when omitted"},
	},
	Fields: entryFields,
	Examples: []capability.Example{{Description: "Read the metadata of the root folder of one drive",
		Arguments: json.RawMessage(`{"drive_id":1}`)}},
}

var filesGet = capability.Descriptor{
	ID:      Provider + ".files.get",
	Version: 1,
	Title:   "Read Infomaniak kDrive file content",
	Description: "Read one bounded file of a drive this connection may reach, as base64; a folder identifier " +
		"reads Infomaniak's own zip archive of it, still bounded by the same size limit",
	Tags:     []string{"infomaniak", "kdrive", "files", "get", "content"},
	Risk:     filesReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` + idSchema + `},` +
		`"required":["drive_id","file_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"drive_id":{"type":"integer"},"file_id":{"type":"integer"},"content_base64":{"type":"string"},` +
		`"content_type":{"type":"string"},"size":{"type":"integer"}},` +
		`"required":["drive_id","file_id","content_base64","size"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		driveIDArgument,
		{Name: "file_id", Description: "File or folder identifier below the same drive", Required: true},
	},
	Fields: []capability.Field{
		{Name: "drive_id", Description: "Drive the content was read from"},
		{Name: "file_id", Description: "File or folder the content was read from"},
		{Name: "content_base64", Description: "Content, base64-encoded; refused instead of truncated once it exceeds the size limit"},
		{Name: "content_type", Description: "Content type Infomaniak reports for the download"},
		{Name: "size", Description: "Size of the returned content in bytes, after decoding"},
	},
	Examples: []capability.Example{{Description: "Read the content of one file",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":2}`)}},
}

// fileJSON is the subset of the Infomaniak File/Directory resource this provider reads. Fields that only
// apply to a file are pointers, nil for a folder or when Infomaniak reports none.
type fileJSON struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Type           string  `json:"type"`
	MimeType       *string `json:"mime_type"`
	Size           *int64  `json:"size"`
	ParentID       int64   `json:"parent_id"`
	Status         string  `json:"status"`
	CreatedAt      *int64  `json:"created_at"`
	LastModifiedAt int64   `json:"last_modified_at"`
	DeletedAt      *int64  `json:"deleted_at"`
	Visibility     string  `json:"visibility"`
	DriveID        int64   `json:"drive_id"`
	Depth          *int64  `json:"depth"`
	CreatedBy      *int64  `json:"created_by"`
	LastModifiedBy *int64  `json:"last_modified_by"`
	DeletedBy      *int64  `json:"deleted_by"`
	AddedAt        *int64  `json:"added_at"`
	RevisedAt      *int64  `json:"revised_at"`
	UpdatedAt      *int64  `json:"updated_at"`
	SharedRootID   *int64  `json:"shared_root_id"`
	ExtensionType  string  `json:"extension_type"`
	SymbolicLink   string  `json:"symbolic_link"`
	ScanStatus     string  `json:"scan_status"`
	Color          string  `json:"color"`
	// Parents is every folder above the object, present only when it was asked for.
	Parents *[]parentJSON `json:"parents"`
}

// Entry is the stable Qatlas view of one file or folder. Content is never part of it.
type Entry struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	MimeType   string `json:"mime_type,omitempty"`
	Size       int64  `json:"size,omitempty"`
	ParentID   int64  `json:"parent_id"`
	Status     string `json:"status,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	ModifiedAt string `json:"modified_at,omitempty"`
	DeletedAt  string `json:"deleted_at,omitempty"`

	Visibility     string `json:"visibility,omitempty"`
	DriveID        int64  `json:"drive_id,omitempty"`
	Depth          *int64 `json:"depth,omitempty"`
	CreatedBy      int64  `json:"created_by,omitempty"`
	LastModifiedBy int64  `json:"last_modified_by,omitempty"`
	DeletedBy      int64  `json:"deleted_by,omitempty"`
	AddedAt        string `json:"added_at,omitempty"`
	RevisedAt      string `json:"revised_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
	SharedRootID   int64  `json:"shared_root_id,omitempty"`
	ExtensionType  string `json:"extension_type,omitempty"`
	SymbolicLink   string `json:"symbolic_link,omitempty"`
	ScanStatus     string `json:"scan_status,omitempty"`
	Color          string `json:"color,omitempty"`
}

// entryOf normalises one Infomaniak file or directory resource into the stable envelope. dir/file is
// reported as folder/file to match this repository's other file providers.
func entryOf(f fileJSON) Entry {
	entry := Entry{ID: f.ID, Name: bounded(f.Name), Type: "file", ParentID: f.ParentID, Status: bounded(f.Status)}
	if f.Type == "dir" {
		entry.Type = "folder"
	}
	if f.MimeType != nil {
		entry.MimeType = bounded(*f.MimeType)
	}
	if f.Size != nil && *f.Size >= 0 {
		entry.Size = *f.Size
	}
	entry.CreatedAt = positiveTime(f.CreatedAt)
	if f.LastModifiedAt > 0 {
		entry.ModifiedAt = time.Unix(f.LastModifiedAt, 0).UTC().Format(time.RFC3339)
	}
	entry.DeletedAt = positiveTime(f.DeletedAt)
	entry.AddedAt, entry.RevisedAt, entry.UpdatedAt = positiveTime(f.AddedAt), positiveTime(f.RevisedAt), positiveTime(f.UpdatedAt)
	entry.Visibility = boundedText(f.Visibility, 64)
	entry.DriveID = max(f.DriveID, 0)
	if f.Depth != nil && *f.Depth >= 0 {
		entry.Depth = valuePointer(f.Depth)
	}
	entry.CreatedBy, entry.LastModifiedBy, entry.DeletedBy = optionalID(f.CreatedBy), optionalID(f.LastModifiedBy), optionalID(f.DeletedBy)
	entry.SharedRootID = optionalID(f.SharedRootID)
	entry.ExtensionType, entry.ScanStatus = boundedText(f.ExtensionType, 64), boundedText(f.ScanStatus, 64)
	entry.SymbolicLink, entry.Color = boundedText(f.SymbolicLink, 1024), boundedText(f.Color, 64)
	return entry
}

// positiveTime renders an optional Unix-seconds timestamp as RFC 3339 UTC; absent and non-positive values
// stay empty.
func positiveTime(seconds *int64) string {
	if seconds == nil || *seconds <= 0 {
		return ""
	}
	return timestampOf(seconds)
}

// optionalID returns an optional identifier, or zero when it is absent or not positive.
func optionalID(id *int64) int64 {
	if id == nil || *id <= 0 {
		return 0
	}
	return *id
}

// navigatorJSON is the cursor pagination envelope Infomaniak reports as siblings of data for a folder
// listing.
type navigatorJSON struct {
	Cursor     string `json:"cursor"`
	HasMore    bool   `json:"has_more"`
	ResponseAt *int64 `json:"response_at"`
}

// FolderPage is one paginated listing of the immediate children of one folder.
type FolderPage struct {
	DriveID  int64   `json:"drive_id"`
	FolderID int64   `json:"folder_id"`
	Entries  []Entry `json:"entries"`
	Cursor   string  `json:"cursor,omitempty"`
	HasMore  bool    `json:"has_more"`
	Count    int     `json:"count"`
	// ResponseAt is the time Infomaniak generated the page.
	ResponseAt string `json:"response_at,omitempty"`
}

// Content is one bounded file's content, base64-encoded.
type Content struct {
	DriveID       int64  `json:"drive_id"`
	FileID        int64  `json:"file_id"`
	ContentBase64 string `json:"content_base64"`
	ContentType   string `json:"content_type,omitempty"`
	Size          int    `json:"size"`
}

type filesListArguments struct {
	DriveID  int64  `json:"drive_id"`
	FolderID int64  `json:"folder_id"`
	Cursor   string `json:"cursor"`
	Limit    int    `json:"limit"`
}

func invokeFilesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input filesListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list files", "the validated arguments could not be read")
	}
	if err := selectDrive(resolved, input.DriveID); err != nil {
		return nil, err
	}
	folderID, defaulted, err := listFolder(resolved, input)
	if err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.verifyDriveAccount(ctx, "list files", input.DriveID); err != nil {
		return nil, err
	}
	if !defaulted {
		// The entries of a folder inside the targets lie inside them too, so they need no proof of their own.
		if err := client.proveObjects(ctx, "list files", input.DriveID, folderID); err != nil {
			return nil, err
		}
	}
	return client.ListFolder(ctx, input.DriveID, folderID, input.Cursor, limit)
}

// listFolder names the folder to list. Without folder_id a drive bound as a whole lists its root; a drive
// bound as a subtree lists its one files target, and with several of them folder_id is required. A folder
// taken from the targets themselves needs no proof.
func listFolder(resolved *config.Resolved, input filesListArguments) (folderID int64, defaulted bool, err error) {
	if input.FolderID != 0 {
		return input.FolderID, false, nil
	}
	bound, err := scopeOf(resolved)
	if err != nil {
		return 0, false, providerError("list files", err.Error())
	}
	if bound.wholeDrive(input.DriveID) {
		return rootFileID, true, nil
	}
	roots := bound.subtreeRoots(input.DriveID)
	if len(roots) != 1 {
		return 0, false, invalidRequest("folder_id is required because this connection binds several files or folders of this drive")
	}
	return roots[0], true, nil
}

// ListFolder reads one page of the immediate children of one folder, cursor-paginated exactly as Infomaniak
// answers: it never follows has_more itself.
func (c *Client) ListFolder(ctx context.Context, driveID, folderID int64, cursor string, limit int) (*FolderPage, error) {
	const op = "list files"
	path := fmt.Sprintf("/3/drive/%d/files/%d/files", driveID, folderID)
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var items []fileJSON
	var meta navigatorJSON
	if err := c.doInto(ctx, op, path, query, &items, &meta); err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		entries = append(entries, entryOf(item))
	}
	return &FolderPage{DriveID: driveID, FolderID: folderID, Entries: entries, Cursor: meta.Cursor,
		HasMore: meta.HasMore, Count: len(entries), ResponseAt: positiveTime(meta.ResponseAt)}, nil
}

type fileArguments struct {
	DriveID int64 `json:"drive_id"`
	FileID  int64 `json:"file_id"`
}

func invokeFilesStat(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input fileArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("stat file", "the validated arguments could not be read")
	}
	if err := selectDrive(resolved, input.DriveID); err != nil {
		return nil, err
	}
	fileID := input.FileID
	if fileID == 0 {
		fileID = rootFileID
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.verifyDriveAccount(ctx, "stat file", input.DriveID); err != nil {
		return nil, err
	}
	return client.statProven(ctx, input.DriveID, fileID)
}

// StatFile reads the metadata of exactly one file or folder.
func (c *Client) StatFile(ctx context.Context, driveID, fileID int64) (*Entry, error) {
	const op = "stat file"
	path := fmt.Sprintf("/3/drive/%d/files/%d", driveID, fileID)
	var item fileJSON
	if err := c.do(ctx, op, path, nil, &item); err != nil {
		return nil, err
	}
	entry := entryOf(item)
	return &entry, nil
}

func invokeFilesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input fileArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("get file content", "the validated arguments could not be read")
	}
	if err := selectDrive(resolved, input.DriveID); err != nil {
		return nil, err
	}
	if input.FileID <= 0 {
		return nil, invalidRequest("file_id must be a positive integer")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.verifyDriveAccount(ctx, "get file content", input.DriveID); err != nil {
		return nil, err
	}
	if err := client.proveObjects(ctx, "get file content", input.DriveID, input.FileID); err != nil {
		return nil, err
	}
	return client.GetFileContent(ctx, input.DriveID, input.FileID)
}

// GetFileContent downloads one bounded file's content. Infomaniak documents that this endpoint may answer
// with a redirect to the actual storage location; newDownloadClient follows at most one, to an https
// location only, and never forwards the Authorization header past the configured API host.
func (c *Client) GetFileContent(ctx context.Context, driveID, fileID int64) (*Content, error) {
	const op = "get file content"
	response, err := c.openDownload(ctx, op, driveID, fileID)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxFileBytes+1))
	if err != nil || len(body) > maxFileBytes {
		return nil, providerError(op, "the Infomaniak file exceeds the size limit")
	}
	return &Content{
		DriveID: driveID, FileID: fileID, ContentBase64: base64.StdEncoding.EncodeToString(body),
		ContentType: bounded(response.Header.Get("Content-Type")), Size: len(body),
	}, nil
}

// openDownload sends the one content request through newDownloadClient and returns its successful
// response; the caller closes the body.
func (c *Client) openDownload(ctx context.Context, op string, driveID, fileID int64) (*http.Response, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, provider.Waited(op, "Infomaniak", err)
	}
	path := fmt.Sprintf("/2/drive/%d/files/%d/download", driveID, fileID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiRoot+path, nil)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "qatlas-cli")

	response, err := newDownloadClient().Do(req)
	if err != nil {
		return nil, provider.Transport(op, "Infomaniak", err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		defer response.Body.Close()
		return nil, c.statusError(op, response, false)
	}
	return response, nil
}
