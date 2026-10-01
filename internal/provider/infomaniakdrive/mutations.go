package infomaniakdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// maxNameBytes is the longest file or folder name Infomaniak accepts, in bytes.
const maxNameBytes = 255

// maxObjectID bounds a file, folder, or destination identifier to the same eighteen digits a configured
// target allows, so a value is always a plain positive integer before it becomes a path segment.
const maxObjectID int64 = 999999999999999999

// Conflict choices of a move or a copy, as the Infomaniak OpenAPI specification (operations moveFile and
// copyFile) defines them. "version" is deliberately absent: it replaces the content of an existing file.
const (
	conflictError  = "error"
	conflictRename = "rename"
)

// conflictSchema is the fixed enum of the optional conflict argument; error is the default.
const conflictSchema = `{"type":"string","enum":["error","rename"]}`

// Result states of a change.
const (
	statusDone    = "done"
	statusPending = "pending"
)

// objectIDSchema is the JSON Schema of one file, folder, or destination identifier.
const objectIDSchema = `{"type":"integer","minimum":1,"maximum":999999999999999999}`

// nameSchema is the JSON Schema of one new file or folder name: 1 to 255 characters, never a slash or a
// control character. The byte length is checked again in code, because a character may take several bytes.
const nameSchema = `{"type":"string","minLength":1,"maxLength":255,"pattern":"^[^/\\x00-\\x1f\\x7f]+$"}`

var mutationOutputSchema = `{"type":"object","properties":{` +
	`"drive_id":{"type":"integer"},"file_id":{"type":"integer"},"destination_id":{"type":"integer"},` +
	`"status":{"type":"string","enum":["done","pending"]},"cancel_id":{"type":"string"},` +
	`"valid_until":{"type":"string"},"entry":` + entrySchema + `},` +
	`"required":["drive_id","status"],"additionalProperties":false}`

var mutationFields = []capability.Field{
	{Name: "drive_id", Description: "Drive the change was made in"},
	{Name: "file_id", Description: "File or folder the change was requested for"},
	{Name: "destination_id", Description: "Destination folder of a move or copy"},
	{Name: "status", Description: "done when Infomaniak applied the change; pending when Infomaniak accepted it and has not finished it yet, so read the drive before relying on it"},
	{Name: "cancel_id", Description: "Infomaniak's identifier to cancel the accepted change, when it reports one"},
	{Name: "valid_until", Description: "Time until which the change can be cancelled, RFC 3339 in UTC, when Infomaniak reports one"},
	{Name: "entry", Description: "The created folder or the copy, when Infomaniak returns it; untrusted data"},
}

func mutationRisk(effect capability.Effect) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity}
}

var mutationDriveArgument = capability.Argument{Name: "drive_id",
	Description: "kDrive identifier; must be inside this connection's drive allow-list when it has one, and " +
		"is always re-checked against the connection's bound account before the change", Required: true}

var conflictArgument = capability.Argument{Name: "conflict",
	Description: "What to do when the name already exists at the destination: error (default) refuses the " +
		"change and changes nothing; rename keeps both and gives the new one an available name"}

var foldersCreate = capability.Descriptor{
	ID:      Provider + ".folders.create",
	Version: 1,
	Title:   "Create an Infomaniak kDrive folder",
	Description: "Create exactly one confirmed folder with a new name below an existing folder of a drive this " +
		"connection may reach; a name that already exists is refused, never renamed or merged",
	Tags:     []string{"infomaniak", "kdrive", "folders", "create"},
	Risk:     mutationRisk(capability.EffectCreate),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"parent_id":` +
		objectIDSchema + `,"name":` + nameSchema + `},"required":["drive_id","parent_id","name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(mutationOutputSchema),
	Arguments: []capability.Argument{
		mutationDriveArgument,
		{Name: "parent_id", Description: "Folder of the same drive to create the folder in; the drive's root is 1", Required: true},
		{Name: "name", Description: "Name of the new folder, 1 to 255 bytes, without a slash", Required: true},
	},
	Fields: mutationFields,
	Examples: []capability.Example{{Description: "Create a folder in the root of one drive",
		Arguments: json.RawMessage(`{"drive_id":1,"parent_id":1,"name":"Reports"}`)}},
}

var filesRename = capability.Descriptor{
	ID:      Provider + ".files.rename",
	Version: 1,
	Title:   "Rename an Infomaniak kDrive file or folder",
	Description: "Rename exactly one confirmed file or folder of a drive this connection may reach; a name that " +
		"already exists in the same folder is refused, and the drive's root cannot be renamed",
	Tags:     []string{"infomaniak", "kdrive", "files", "rename"},
	Risk:     mutationRisk(capability.EffectUpdate),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `,"name":` + nameSchema + `},"required":["drive_id","file_id","name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(mutationOutputSchema),
	Arguments: []capability.Argument{
		mutationDriveArgument,
		{Name: "file_id", Description: "File or folder to rename; never the drive's root", Required: true},
		{Name: "name", Description: "New name, 1 to 255 bytes, without a slash", Required: true},
	},
	Fields: mutationFields,
	Examples: []capability.Example{{Description: "Rename one file",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":43,"name":"report-final.pdf"}`)}},
}

var filesMove = capability.Descriptor{
	ID:      Provider + ".files.move",
	Version: 1,
	Title:   "Move an Infomaniak kDrive file or folder",
	Description: "Move exactly one confirmed file or folder into another folder of the same drive; a name that " +
		"already exists there is refused unless conflict is rename, and the drive's root cannot be moved",
	Tags:     []string{"infomaniak", "kdrive", "files", "move"},
	Risk:     mutationRisk(capability.EffectUpdate),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `,"destination_id":` + objectIDSchema + `,"conflict":` + conflictSchema +
		`},"required":["drive_id","file_id","destination_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(mutationOutputSchema),
	Arguments: []capability.Argument{
		mutationDriveArgument,
		{Name: "file_id", Description: "File or folder to move; never the drive's root", Required: true},
		{Name: "destination_id", Description: "Folder of the same drive to move it into; the drive's root is 1", Required: true},
		conflictArgument,
	},
	Fields: mutationFields,
	Examples: []capability.Example{{Description: "Move one file into a folder",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":43,"destination_id":42}`)}},
}

var filesCopy = capability.Descriptor{
	ID:      Provider + ".files.copy",
	Version: 1,
	Title:   "Copy an Infomaniak kDrive file or folder",
	Description: "Copy exactly one confirmed file or folder into another folder of the same drive; a name that " +
		"already exists there is refused unless conflict is rename, and the drive's root cannot be copied",
	Tags:     []string{"infomaniak", "kdrive", "files", "copy"},
	Risk:     mutationRisk(capability.EffectCreate),
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `,"destination_id":` + objectIDSchema + `,"conflict":` + conflictSchema +
		`},"required":["drive_id","file_id","destination_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(mutationOutputSchema),
	Arguments: []capability.Argument{
		mutationDriveArgument,
		{Name: "file_id", Description: "File or folder to copy; never the drive's root", Required: true},
		{Name: "destination_id", Description: "Folder of the same drive to copy it into; the drive's root is 1", Required: true},
		conflictArgument,
	},
	Fields: mutationFields,
	Examples: []capability.Example{{Description: "Copy one file into a folder",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":43,"destination_id":42}`)}},
}

// Change is the stable answer of one confirmed change.
type Change struct {
	DriveID       int64  `json:"drive_id"`
	FileID        int64  `json:"file_id,omitempty"`
	DestinationID int64  `json:"destination_id,omitempty"`
	Status        string `json:"status"`
	CancelID      string `json:"cancel_id,omitempty"`
	ValidUntil    string `json:"valid_until,omitempty"`
	Entry         *Entry `json:"entry,omitempty"`
}

// validObjectID reports whether a file, folder, or destination identifier is a plain positive integer in
// the same bounded range as a configured target.
func validObjectID(id int64) bool { return id >= 1 && id <= maxObjectID }

// validName reports whether a new file or folder name is valid UTF-8 of 1 to 255 bytes without a slash or a
// control character, and not one of the two special directory names. It never inspects the name beyond its
// shape.
func validName(name string) bool {
	if name == "" || len(name) > maxNameBytes || !utf8.ValidString(name) || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if r == '/' || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

const nameReason = "name must be 1 to 255 bytes of text without a slash or control character"

// prepare runs every local check of a change, in order: the identifiers, the drive allow-list, and, only
// then, the secret and the live ownership check of the drive. Nothing before the last step reads a secret or
// sends a request, and no refusal names a value the caller supplied.
func prepare(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	op string, driveID int64) (*Client, error) {
	if err := selectDrive(resolved, driveID); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.verifyDriveAccount(ctx, op, driveID); err != nil {
		return nil, err
	}
	return client, nil
}

type folderCreateArguments struct {
	DriveID  int64  `json:"drive_id"`
	ParentID int64  `json:"parent_id"`
	Name     string `json:"name"`
}

func invokeFoldersCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create folder"
	var input folderCreateArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validObjectID(input.ParentID) {
		return nil, invalidRequest("parent_id must be a positive integer")
	}
	if !validName(input.Name) {
		return nil, invalidRequest(nameReason)
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID)
	if err != nil {
		return nil, err
	}
	return client.CreateFolder(ctx, input.DriveID, input.ParentID, input.Name)
}

// CreateFolder creates exactly one folder, once. The API default for a name that exists is a refusal, so no
// conflict option is ever sent.
func (c *Client) CreateFolder(ctx context.Context, driveID, parentID int64, name string) (*Change, error) {
	const op = "create folder"
	path := fmt.Sprintf("/3/drive/%d/files/%d/directory", driveID, parentID)
	change, err := c.change(ctx, op, path, map[string]any{"name": name}, driveID)
	if err != nil {
		return nil, err
	}
	if change.Entry != nil && change.Entry.ParentID != parentID {
		return nil, invalidResponse(op, "Infomaniak returned an invalid response"+uncertain)
	}
	change.FileID = 0
	return change, nil
}

type renameArguments struct {
	DriveID int64  `json:"drive_id"`
	FileID  int64  `json:"file_id"`
	Name    string `json:"name"`
}

func invokeFilesRename(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "rename file"
	var input renameArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validObjectID(input.FileID) || input.FileID == rootFileID {
		return nil, invalidRequest("file_id must be a positive integer other than the drive's root")
	}
	if !validName(input.Name) {
		return nil, invalidRequest(nameReason)
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID)
	if err != nil {
		return nil, err
	}
	return client.RenameFile(ctx, input.DriveID, input.FileID, input.Name)
}

// RenameFile renames exactly one file or folder, once.
func (c *Client) RenameFile(ctx context.Context, driveID, fileID int64, name string) (*Change, error) {
	const op = "rename file"
	change, err := c.change(ctx, op, fmt.Sprintf("/2/drive/%d/files/%d/rename", driveID, fileID),
		map[string]any{"name": name}, driveID)
	if err != nil {
		return nil, err
	}
	change.FileID = fileID
	return change, nil
}

type transferArguments struct {
	DriveID       int64  `json:"drive_id"`
	FileID        int64  `json:"file_id"`
	DestinationID int64  `json:"destination_id"`
	Conflict      string `json:"conflict"`
}

func invokeFilesMove(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeTransfer(ctx, resolved, secrets, red, raw, "move file", (*Client).MoveFile)
}

func invokeFilesCopy(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeTransfer(ctx, resolved, secrets, red, raw, "copy file", (*Client).CopyFile)
}

func invokeTransfer(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, op string,
	send func(*Client, context.Context, int64, int64, int64, string) (*Change, error)) (any, error) {
	var input transferArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validObjectID(input.FileID) || input.FileID == rootFileID {
		return nil, invalidRequest("file_id must be a positive integer other than the drive's root")
	}
	if !validObjectID(input.DestinationID) {
		return nil, invalidRequest("destination_id must be a positive integer")
	}
	if input.Conflict == "" {
		input.Conflict = conflictError
	}
	if input.Conflict != conflictError && input.Conflict != conflictRename {
		return nil, invalidRequest("conflict must be error or rename")
	}
	if input.FileID == input.DestinationID {
		return nil, invalidRequest("destination_id must differ from file_id")
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID)
	if err != nil {
		return nil, err
	}
	return send(client, ctx, input.DriveID, input.FileID, input.DestinationID, input.Conflict)
}

// MoveFile moves exactly one file or folder into a folder of the same drive, once. The conflict choice is
// always sent explicitly, so the outcome never depends on an API default.
func (c *Client) MoveFile(ctx context.Context, driveID, fileID, destinationID int64, conflict string) (*Change, error) {
	return c.transfer(ctx, "move file", "move", driveID, fileID, destinationID, conflict)
}

// CopyFile copies exactly one file or folder into a folder of the same drive, once, under the same rule.
// Infomaniak's own default for a copy is rename, which is why the choice is never left out.
func (c *Client) CopyFile(ctx context.Context, driveID, fileID, destinationID int64, conflict string) (*Change, error) {
	return c.transfer(ctx, "copy file", "copy", driveID, fileID, destinationID, conflict)
}

func (c *Client) transfer(ctx context.Context, op, verb string, driveID, fileID, destinationID int64,
	conflict string) (*Change, error) {
	path := fmt.Sprintf("/3/drive/%d/files/%d/%s/%d", driveID, fileID, verb, destinationID)
	change, err := c.change(ctx, op, path, map[string]any{"conflict": conflict}, driveID)
	if err != nil {
		return nil, err
	}
	change.FileID, change.DestinationID = fileID, destinationID
	if change.Entry != nil && change.Entry.ParentID != destinationID {
		return nil, invalidResponse(op, "Infomaniak returned an invalid response"+uncertain)
	}
	return change, nil
}

// actionJSON is the cancel handle Infomaniak reports for a change it can still undo.
type actionJSON struct {
	CancelID   string `json:"cancel_id"`
	ValidUntil int64  `json:"valid_until"`
}

// change sends exactly one POST and normalises the answer. "asynchronous" is accepted as a pending change
// with the cancel handle, not as a failure; "success" is a finished one; anything else is an unclear outcome.
func (c *Client) change(ctx context.Context, op, path string, body any, driveID int64) (*Change, error) {
	env, _, err := c.request(ctx, op, http.MethodPost, path, nil, body, true)
	if err != nil {
		return nil, err
	}
	result := &Change{DriveID: driveID}
	switch env.Result {
	case "success":
		result.Status = statusDone
	case "asynchronous":
		result.Status = statusPending
	default:
		return nil, invalidResponse(op, "Infomaniak reported an error for a response with an HTTP success status"+uncertain)
	}
	var action actionJSON
	if json.Unmarshal(env.Data, &action) == nil && action.CancelID != "" {
		result.CancelID = bounded(action.CancelID)
		if action.ValidUntil > 0 {
			result.ValidUntil = timeOf(action.ValidUntil)
		}
	}
	var item fileJSON
	if json.Unmarshal(env.Data, &item) == nil && item.ID > 0 && (item.Type == "dir" || item.Type == "file") {
		entry := entryOf(item)
		result.Entry = &entry
	}
	return result, nil
}

func timeOf(unix int64) string { return entryOf(fileJSON{LastModifiedAt: unix}).ModifiedAt }
