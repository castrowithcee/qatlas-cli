package infomaniakdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// trashEntrySchema is an entry plus the time it was moved to the trash.
var trashEntrySchema = `{"type":"object","properties":{` +
	`"id":{"type":"integer"},"name":{"type":"string"},"type":{"type":"string","enum":["file","folder"]},` +
	`"mime_type":{"type":"string"},"size":{"type":"integer"},"parent_id":{"type":"integer"},` +
	`"status":{"type":"string"},"created_at":{"type":"string"},"modified_at":{"type":"string"},` +
	`"deleted_at":{"type":"string"}},"required":["id","name","type","parent_id"],"additionalProperties":false}`

var trashListDescriptor = capability.Descriptor{
	ID:      Provider + ".trash.list",
	Version: 1,
	Title:   "List the Infomaniak kDrive trash",
	Description: "List the top-level entries of the trash of one drive this connection may reach, one page at a " +
		"time with an opaque cursor; never reads file content and never opens a trashed folder",
	Tags:     []string{"infomaniak", "kdrive", "trash", "list"},
	Risk:     filesReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,` +
		`"cursor":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCursorLength) + `},` +
		`"limit":{"type":"integer","minimum":` + strconv.Itoa(minListLimit) + `,"maximum":` + strconv.Itoa(maxListLimit) + `}},` +
		`"required":["drive_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"drive_id":{"type":"integer"},"entries":{"type":"array","items":` + trashEntrySchema + `},` +
		`"cursor":{"type":"string"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["drive_id","entries","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		driveIDArgument,
		{Name: "cursor", Description: "Opaque cursor from a previous call's has_more page; omitted for the first page"},
		{Name: "limit", Description: "Entries per page, 5 to 1000; 10 when omitted"},
	},
	Fields: append(append([]capability.Field{}, entryFields...),
		capability.Field{Name: "deleted_at", Description: "Time the entry was moved to the trash, RFC 3339 in UTC; untrusted data"},
		capability.Field{Name: "drive_id", Description: "Drive whose trash was listed"},
		capability.Field{Name: "cursor", Description: "Cursor for the next page; absent when has_more is false"},
		capability.Field{Name: "has_more", Description: "True when a further page remains, so this page is not the whole trash"},
		capability.Field{Name: "count", Description: "Number of entries on this page"},
	),
	Examples: []capability.Example{{Description: "List the trash of one drive",
		Arguments: json.RawMessage(`{"drive_id":1}`)}},
}

var trashFilesFields = []capability.Field{
	{Name: "drive_id", Description: "Drive the change was made in"},
	{Name: "file_id", Description: "File or folder the change was requested for"},
	{Name: "destination_id", Description: "Folder a restored entry was put into"},
	{Name: "status", Description: "done when Infomaniak applied the change; pending when Infomaniak accepted it and has not finished it yet, so read the trash before relying on it"},
	{Name: "cancel_id", Description: "Infomaniak's identifier to cancel the accepted change, when it reports one"},
	{Name: "valid_until", Description: "Time until which the change can be cancelled, RFC 3339 in UTC, when Infomaniak reports one"},
}

// trashOutputSchema is the change answer without an entry, since no trash change returns one.
var trashOutputSchema = `{"type":"object","properties":{` +
	`"drive_id":{"type":"integer"},"file_id":{"type":"integer"},"destination_id":{"type":"integer"},` +
	`"status":{"type":"string","enum":["done","pending"]},"cancel_id":{"type":"string"},` +
	`"valid_until":{"type":"string"}},"required":["drive_id","status"],"additionalProperties":false}`

var filesTrash = capability.Descriptor{
	ID:      Provider + ".files.trash",
	Version: 1,
	Title:   "Move an Infomaniak kDrive file or folder to the trash",
	Description: "Move exactly one confirmed file or folder of a drive this connection may reach to the trash, " +
		"where it can be restored until it is deleted for good; the drive's root cannot be trashed",
	Tags:                  []string{"infomaniak", "kdrive", "files", "trash", "delete"},
	Risk:                  mutationRisk(capability.EffectDelete),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `},"required":["drive_id","file_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(trashOutputSchema),
	Arguments: []capability.Argument{
		mutationDriveArgument,
		{Name: "file_id", Description: "File or folder to move to the trash; never the drive's root", Required: true},
	},
	Fields: trashFilesFields,
	Examples: []capability.Example{{Description: "Trash one file",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":43}`)}},
}

var trashRestore = capability.Descriptor{
	ID:      Provider + ".trash.restore",
	Version: 1,
	Title:   "Restore an Infomaniak kDrive trash entry",
	Description: "Restore exactly one confirmed top-level trash entry of a drive this connection may reach into " +
		"a folder of the same drive",
	Tags:     []string{"infomaniak", "kdrive", "trash", "restore"},
	Risk:     mutationRisk(capability.EffectUpdate),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `,"destination_id":` + objectIDSchema +
		`},"required":["drive_id","file_id","destination_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(trashOutputSchema),
	Arguments: []capability.Argument{
		mutationDriveArgument,
		{Name: "file_id", Description: "Trash entry to restore, as trash.list reports it", Required: true},
		{Name: "destination_id", Description: "Folder of the same drive to restore it into; the drive's root is 1", Required: true},
	},
	Fields: trashFilesFields,
	Examples: []capability.Example{{Description: "Restore one entry into the root",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":43,"destination_id":1}`)}},
}

var trashDelete = capability.Descriptor{
	ID:      Provider + ".trash.delete",
	Version: 1,
	Title:   "Delete an Infomaniak kDrive trash entry for good",
	Description: "Permanently delete exactly one confirmed top-level trash entry of a drive this connection may " +
		"reach; it cannot be restored afterwards",
	Tags:                  []string{"infomaniak", "kdrive", "trash", "delete", "permanent"},
	Risk:                  mutationRisk(capability.EffectDelete),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `},"required":["drive_id","file_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(trashOutputSchema),
	Arguments: []capability.Argument{
		mutationDriveArgument,
		{Name: "file_id", Description: "Trash entry to delete for good, as trash.list reports it", Required: true},
	},
	Fields: trashFilesFields,
	Examples: []capability.Example{{Description: "Delete one trash entry for good",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":43}`)}},
}

var trashEmpty = capability.Descriptor{
	ID:      Provider + ".trash.empty",
	Version: 1,
	Title:   "Empty the Infomaniak kDrive trash",
	Description: "Permanently delete every entry in the trash of one drive this connection may reach; nothing " +
		"in it can be restored afterwards",
	Tags:                  []string{"infomaniak", "kdrive", "trash", "empty", "permanent"},
	Risk:                  mutationRisk(capability.EffectDelete),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema +
		`},"required":["drive_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(trashOutputSchema),
	Arguments:    []capability.Argument{mutationDriveArgument},
	Fields:       trashFilesFields,
	Examples: []capability.Example{{Description: "Empty the trash of one drive",
		Arguments: json.RawMessage(`{"drive_id":1}`)}},
}

// TrashEntry is the stable Qatlas view of one trash entry.
type TrashEntry struct {
	Entry
	DeletedAt string `json:"deleted_at,omitempty"`
}

// TrashPage is one paginated listing of the top level of a drive's trash.
type TrashPage struct {
	DriveID int64        `json:"drive_id"`
	Entries []TrashEntry `json:"entries"`
	Cursor  string       `json:"cursor,omitempty"`
	HasMore bool         `json:"has_more"`
	Count   int          `json:"count"`
}

type trashListArguments struct {
	DriveID int64  `json:"drive_id"`
	Cursor  string `json:"cursor"`
	Limit   int    `json:"limit"`
}

func invokeTrashList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list trash"
	var input trashListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID)
	if err != nil {
		return nil, err
	}
	return client.ListTrash(ctx, input.DriveID, input.Cursor, limit)
}

// ListTrash reads one page of the top level of a drive's trash, cursor-paginated exactly as Infomaniak
// answers: it never follows has_more itself.
func (c *Client) ListTrash(ctx context.Context, driveID int64, cursor string, limit int) (*TrashPage, error) {
	const op = "list trash"
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var items []fileJSON
	var meta navigatorJSON
	if err := c.doInto(ctx, op, fmt.Sprintf("/3/drive/%d/trash", driveID), query, &items, &meta); err != nil {
		return nil, err
	}
	entries := make([]TrashEntry, 0, len(items))
	for _, item := range items {
		entry := TrashEntry{Entry: entryOf(item)}
		if item.DeletedAt != nil && *item.DeletedAt > 0 {
			entry.DeletedAt = time.Unix(*item.DeletedAt, 0).UTC().Format(time.RFC3339)
		}
		entries = append(entries, entry)
	}
	return &TrashPage{DriveID: driveID, Entries: entries, Cursor: meta.Cursor, HasMore: meta.HasMore,
		Count: len(entries)}, nil
}

// invokeTrashFile runs the checks every trash change on one entry shares, then sends it once.
func invokeTrashFile(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, op string,
	send func(*Client, context.Context, int64, int64) (*Change, error)) (any, error) {
	var input fileArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validObjectID(input.FileID) || input.FileID == rootFileID {
		return nil, invalidRequest("file_id must be a positive integer other than the drive's root")
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID)
	if err != nil {
		return nil, err
	}
	return send(client, ctx, input.DriveID, input.FileID)
}

func invokeFilesTrash(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeTrashFile(ctx, resolved, secrets, red, raw, "trash file", (*Client).TrashFile)
}

func invokeTrashDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeTrashFile(ctx, resolved, secrets, red, raw, "delete trash entry", (*Client).DeleteTrashEntry)
}

// TrashFile moves exactly one file or folder to the trash, once.
func (c *Client) TrashFile(ctx context.Context, driveID, fileID int64) (*Change, error) {
	const op = "trash file"
	return c.trashChange(ctx, op, http.MethodDelete, fmt.Sprintf("/2/drive/%d/files/%d", driveID, fileID),
		nil, driveID, fileID)
}

// DeleteTrashEntry deletes exactly one trash entry for good, once.
func (c *Client) DeleteTrashEntry(ctx context.Context, driveID, fileID int64) (*Change, error) {
	const op = "delete trash entry"
	return c.trashChange(ctx, op, http.MethodDelete, fmt.Sprintf("/2/drive/%d/trash/%d", driveID, fileID),
		nil, driveID, fileID)
}

type trashRestoreArguments struct {
	DriveID       int64 `json:"drive_id"`
	FileID        int64 `json:"file_id"`
	DestinationID int64 `json:"destination_id"`
}

func invokeTrashRestore(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "restore trash entry"
	var input trashRestoreArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validObjectID(input.FileID) || input.FileID == rootFileID {
		return nil, invalidRequest("file_id must be a positive integer other than the drive's root")
	}
	if !validObjectID(input.DestinationID) {
		return nil, invalidRequest("destination_id must be a positive integer")
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID)
	if err != nil {
		return nil, err
	}
	return client.RestoreTrashEntry(ctx, input.DriveID, input.FileID, input.DestinationID)
}

// RestoreTrashEntry restores exactly one trash entry into a folder of the same drive, once.
func (c *Client) RestoreTrashEntry(ctx context.Context, driveID, fileID, destinationID int64) (*Change, error) {
	const op = "restore trash entry"
	change, err := c.trashChange(ctx, op, http.MethodPost, fmt.Sprintf("/2/drive/%d/trash/%d/restore", driveID, fileID),
		map[string]any{"destination_directory_id": destinationID}, driveID, fileID)
	if err != nil {
		return nil, err
	}
	change.DestinationID = destinationID
	return change, nil
}

func invokeTrashEmpty(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "empty trash"
	var input fileArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID)
	if err != nil {
		return nil, err
	}
	return client.EmptyTrash(ctx, input.DriveID)
}

// EmptyTrash deletes every entry of a drive's trash for good, once.
func (c *Client) EmptyTrash(ctx context.Context, driveID int64) (*Change, error) {
	return c.trashChange(ctx, "empty trash", http.MethodDelete, fmt.Sprintf("/2/drive/%d/trash", driveID), nil, driveID, 0)
}

// trashChange sends one trash change and never carries a created entry: none of these answers returns one.
func (c *Client) trashChange(ctx context.Context, op, method, path string, body any, driveID, fileID int64) (*Change, error) {
	change, err := c.changeWith(ctx, op, method, path, body, driveID)
	if err != nil {
		return nil, err
	}
	change.FileID, change.Entry = fileID, nil
	return change, nil
}
