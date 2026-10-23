package infomaniakdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// dropboxSensitivity classifies a dropbox as its own class. Its URL lets anyone without an account upload
// into the folder, so it is an access capability, not metadata about the folder.
const dropboxSensitivity = "infomaniak-kdrive-dropboxes"

// maxDropboxText bounds a text of an answered dropbox.
const maxDropboxText = 256

// maxDropboxValue bounds the validity and the size limit of a dropbox, which Infomaniak documents as text.
const maxDropboxValue = 64

var dropboxSchema = `{"type":"object","properties":{` +
	`"id":{"type":"integer"},"uuid":{"type":"string"},"name":{"type":"string"},"url":{"type":"string"},` +
	`"users_count":{"type":"integer"},"created_at":{"type":"string"},"updated_at":{"type":"string"},` +
	`"last_uploaded_at":{"type":"string"},"has_password":{"type":"boolean"},"has_notification":{"type":"boolean"},` +
	`"has_validity":{"type":"boolean"},"has_size_limit":{"type":"boolean"},"created_by":{"type":"integer"},` +
	`"validity":{"type":"string"},"size":{"type":"string"},"directory":` + dropboxDirectorySchema + `,"user":` +
	dropboxUserSchema + `},"required":["id"],"additionalProperties":false}`

// dropboxDirectorySchema is the folder of a dropbox: an entry with the members its with values load.
var dropboxDirectorySchema = `{"type":"object","properties":{` + entryProperties + `,` + strings.Join([]string{
	member("capabilities", fileCapabilitiesSchema), member("path", schemaStr),
	member("parents", schemaList(parentEntrySchema)), member("parents_truncated", schemaBool),
}, ",") + `},"required":["id","name","type","parent_id"],"additionalProperties":false}`

var dropboxUserSchema = schemaOf(join(members(schemaStr, "display_name", "first_name", "last_name", "email",
	"avatar", "deleted_at"), members(schemaInt, "id"), members(schemaBool, "is_sso"))...)

// dropboxWithValues are the with values of dropbox.get. capabilities is always sent, so it is not offered.
var dropboxWithValues = []string{"user", "directory", "directory.capabilities", "directory.path", "directory.parents"}

var dropboxResultSchema = `{"type":"object","properties":{` +
	`"drive_id":{"type":"integer"},"file_id":{"type":"integer"},` +
	`"status":{"type":"string","enum":["done","pending"]},"dropbox":` + dropboxSchema + `},` +
	`"required":["drive_id","file_id"],"additionalProperties":false}`

var dropboxFields = []capability.Field{
	{Name: "drive_id", Description: "Drive the dropbox belongs to"},
	{Name: "file_id", Description: "Folder the dropbox belongs to"},
	{Name: "status", Description: "done when Infomaniak applied the change; pending when Infomaniak accepted it " +
		"and has not finished it yet, so read the dropbox before relying on it; absent for a read"},
	{Name: "dropbox", Description: "The dropbox; untrusted data; absent after an update or a delete"},
	{Name: "id", Description: "Infomaniak identifier of the dropbox"},
	{Name: "uuid", Description: "Infomaniak unique identifier of the dropbox"},
	{Name: "name", Description: "Name of the dropbox"},
	{Name: "url", Description: "Public upload URL, https only; whoever holds it can upload into the folder " +
		"without an account, so treat it as a secret; absent when Infomaniak returned a URL that is not https"},
	{Name: "users_count", Description: "Number of recorded users that uploaded into the dropbox"},
	{Name: "created_at", Description: "Creation time, RFC 3339 in UTC, when Infomaniak reports one"},
	{Name: "updated_at", Description: "Last change time, RFC 3339 in UTC, when Infomaniak reports one"},
	{Name: "last_uploaded_at", Description: "Time of the last upload, RFC 3339 in UTC, when there was one"},
	{Name: "has_password", Description: "Whether a password protects the dropbox; the password is never returned"},
	{Name: "has_notification", Description: "Whether the dropbox owner is mailed when someone uploads"},
	{Name: "has_validity", Description: "Whether the dropbox has an expiry"},
	{Name: "has_size_limit", Description: "Whether the dropbox limits the size of an uploaded file"},
	{Name: "created_by", Description: "Identifier of the user who created the dropbox"},
	{Name: "validity", Description: "Expiry of the dropbox as Infomaniak reports it, untrusted text of at most 64 bytes"},
	{Name: "size", Description: "Size limit of an uploaded file as Infomaniak reports it, untrusted text of at most 64 bytes"},
	{Name: "directory", Description: "Folder of the dropbox in the form of files.stat, untrusted data; only with " +
		"a directory value of with: capabilities with directory.capabilities, path with directory.path, parents " +
		"with directory.parents; when this connection binds the drive only by files targets, without path and " +
		"with only the targets and the folders below them as parents"},
	{Name: "user", Description: "User who created the dropbox: id, display_name, first_name, last_name, email, " +
		"is_sso, avatar (https only), deleted_at; personal data and untrusted; only with with user; security " +
		"details of the user are never returned"},
}

var dropboxReadRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: dropboxSensitivity,
}

func dropboxChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: dropboxSensitivity}
}

var dropboxFolderArgument = capability.Argument{Name: "file_id", Required: true,
	Description: "Folder of the same drive; never the drive's root"}

var dropboxGet = capability.Descriptor{
	ID:      Provider + ".dropbox.get",
	Version: 1,
	Title:   "Get the Infomaniak kDrive dropbox of a folder",
	Description: "Read the dropbox (upload link) of exactly one folder of a drive this connection may reach; the " +
		"URL is upload access to the folder and the password is never returned; a folder without a dropbox is not found",
	Tags:                  []string{"infomaniak", "kdrive", "dropbox", "upload", "get"},
	Risk:                  dropboxReadRisk,
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `,` + withInputSchema(dropboxWithValues) + `},"required":["drive_id","file_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(dropboxResultSchema),
	Arguments: []capability.Argument{driveIDArgument, dropboxFolderArgument,
		{Name: "with", Description: "Related data to load: user adds the creator, directory the folder, and " +
			"directory.capabilities, directory.path and directory.parents what their names say about it"}},
	Fields: dropboxFields,
	Examples: []capability.Example{{Description: "Read the dropbox of one folder",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":43}`)}},
}

var dropboxDelete = capability.Descriptor{
	ID:      Provider + ".dropbox.delete",
	Version: 1,
	Title:   "Delete an Infomaniak kDrive dropbox",
	Description: "Delete exactly one confirmed dropbox of a folder of a drive this connection may reach; the " +
		"folder and its files stay, and the upload URL stops working",
	Tags:                  []string{"infomaniak", "kdrive", "dropbox", "upload", "delete"},
	Risk:                  dropboxChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `},"required":["drive_id","file_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(dropboxResultSchema),
	Arguments:    []capability.Argument{mutationDriveArgument, dropboxFolderArgument},
	Fields:       dropboxFields,
	Examples: []capability.Example{{Description: "Delete the dropbox of one folder",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":43}`)}},
}

// dropboxJSON is the subset of the Infomaniak Dropbox resource this provider reads. It has no password field.
type dropboxJSON struct {
	ID             int64  `json:"id"`
	UUID           string `json:"uuid"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	UsersCount     int64  `json:"users_count"`
	CreatedBy      *int64 `json:"created_by"`
	CreatedAt      *int64 `json:"created_at"`
	UpdatedAt      *int64 `json:"updated_at"`
	LastUploadedAt *int64 `json:"last_uploaded_at"`
	Capabilities   struct {
		HasPassword  bool       `json:"has_password"`
		HasNotify    bool       `json:"has_notification"`
		HasValidity  bool       `json:"has_validity"`
		HasSizeLimit bool       `json:"has_size_limit"`
		Validity     scalarText `json:"validity"`
		Size         scalarText `json:"size"`
	} `json:"capabilities"`
	Directory *fileJSON    `json:"directory"`
	User      *appUserJSON `json:"user"`
}

// appUserJSON is the subset of the Infomaniak AppUser resource this provider reads. It deliberately has no
// security, type or teams member: the security details of a person are never decoded.
type appUserJSON struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Email       string `json:"email"`
	IsSSO       *bool  `json:"is_sso"`
	Avatar      string `json:"avatar"`
	DeletedAt   *int64 `json:"deleted_at"`
}

// DropboxUser is the stable Qatlas view of the user who created a dropbox. Every text is untrusted data.
type DropboxUser struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	FirstName   string `json:"first_name,omitempty"`
	LastName    string `json:"last_name,omitempty"`
	Email       string `json:"email,omitempty"`
	IsSSO       *bool  `json:"is_sso,omitempty"`
	Avatar      string `json:"avatar,omitempty"`
	DeletedAt   string `json:"deleted_at,omitempty"`
}

// Dropbox is the stable Qatlas view of one dropbox. It has no password.
type Dropbox struct {
	ID              int64  `json:"id"`
	UUID            string `json:"uuid,omitempty"`
	Name            string `json:"name,omitempty"`
	URL             string `json:"url,omitempty"`
	UsersCount      int64  `json:"users_count"`
	CreatedAt       string `json:"created_at,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
	LastUploadedAt  string `json:"last_uploaded_at,omitempty"`
	HasPassword     bool   `json:"has_password"`
	HasNotification bool   `json:"has_notification"`
	HasValidity     bool   `json:"has_validity"`
	HasSizeLimit    bool   `json:"has_size_limit"`
	CreatedBy       int64  `json:"created_by,omitempty"`
	Validity        string `json:"validity,omitempty"`
	Size            string `json:"size,omitempty"`
	// Directory and User exist only when GetDropbox was asked for them.
	Directory *Entry       `json:"directory,omitempty"`
	User      *DropboxUser `json:"user,omitempty"`
}

// DropboxResult is the stable answer of a dropbox read or change.
type DropboxResult struct {
	DriveID int64    `json:"drive_id"`
	FileID  int64    `json:"file_id"`
	Status  string   `json:"status,omitempty"`
	Dropbox *Dropbox `json:"dropbox,omitempty"`
}

func dropboxOf(raw dropboxJSON) (*Dropbox, bool) {
	if !validObjectID(raw.ID) {
		return nil, false
	}
	box := &Dropbox{ID: raw.ID, UUID: boundedText(raw.UUID, maxDropboxText), Name: boundedText(raw.Name, maxDropboxText),
		URL: safeLinkURL(raw.URL), UsersCount: raw.UsersCount, HasPassword: raw.Capabilities.HasPassword,
		HasNotification: raw.Capabilities.HasNotify, HasValidity: raw.Capabilities.HasValidity,
		HasSizeLimit: raw.Capabilities.HasSizeLimit, CreatedBy: optionalID(raw.CreatedBy),
		Validity: boundedText(string(raw.Capabilities.Validity), maxDropboxValue),
		Size:     boundedText(string(raw.Capabilities.Size), maxDropboxValue)}
	if raw.CreatedAt != nil && *raw.CreatedAt > 0 {
		box.CreatedAt = timeOf(*raw.CreatedAt)
	}
	if raw.UpdatedAt != nil && *raw.UpdatedAt > 0 {
		box.UpdatedAt = timeOf(*raw.UpdatedAt)
	}
	if raw.LastUploadedAt != nil && *raw.LastUploadedAt > 0 {
		box.LastUploadedAt = timeOf(*raw.LastUploadedAt)
	}
	return box, true
}

func dropboxPath(driveID, fileID int64) string {
	return fmt.Sprintf("/2/drive/%d/files/%d/dropbox", driveID, fileID)
}

func invokeDropboxGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get dropbox"
	var input struct {
		fileArguments
		With []string `json:"with"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validObjectID(input.FileID) || input.FileID == rootFileID {
		return nil, invalidRequest(dropboxFolderReason)
	}
	if err := checkValues(input.With, dropboxWithValues, "with"); err != nil {
		return nil, err
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID, input.FileID)
	if err != nil {
		return nil, err
	}
	return client.GetDropbox(ctx, input.DriveID, input.FileID, input.With)
}

const dropboxFolderReason = "file_id must be a positive integer other than the drive's root"

// GetDropbox reads the dropbox of exactly one folder; with loads related data. The capabilities are always
// requested, since the flags and limits of the dropbox come from them.
func (c *Client) GetDropbox(ctx context.Context, driveID, fileID int64, with []string) (*DropboxResult, error) {
	const op = "get dropbox"
	if err := checkValues(with, dropboxWithValues, "with"); err != nil {
		return nil, err
	}
	query := url.Values{"with": {withQuery("capabilities", with)}}
	var raw dropboxJSON
	if err := c.do(ctx, op, dropboxPath(driveID, fileID), query, &raw); err != nil {
		return nil, err
	}
	box, ok := dropboxOf(raw)
	if !ok {
		return nil, invalidResponse(op, "Infomaniak returned an invalid response")
	}
	askedDirectory := slices.ContainsFunc(with, func(v string) bool { return strings.HasPrefix(v, "directory") })
	if askedDirectory && raw.Directory != nil && raw.Directory.ID == fileID {
		var directoryWith []string
		for _, value := range with {
			if name, ok := strings.CutPrefix(value, "directory."); ok {
				directoryWith = append(directoryWith, name)
			}
		}
		entry := c.entryWith(driveID, *raw.Directory, directoryWith)
		box.Directory = &entry
	}
	if slices.Contains(with, "user") && raw.User != nil {
		box.User = userOf(*raw.User)
	}
	return &DropboxResult{DriveID: driveID, FileID: fileID, Dropbox: box}, nil
}

// userOf maps a user to the fields Qatlas returns; an address that is not a safe https URL is dropped.
func userOf(raw appUserJSON) *DropboxUser {
	if !validObjectID(raw.ID) {
		return nil
	}
	return &DropboxUser{ID: raw.ID, DisplayName: boundedText(raw.DisplayName, maxDropboxText),
		FirstName: boundedText(raw.FirstName, maxDropboxText), LastName: boundedText(raw.LastName, maxDropboxText),
		Email: boundedText(raw.Email, maxDropboxText), IsSSO: valuePointer(raw.IsSSO),
		Avatar: safeLinkURL(raw.Avatar), DeletedAt: positiveTime(raw.DeletedAt)}
}

func invokeDropboxDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete dropbox"
	var input fileArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validObjectID(input.FileID) || input.FileID == rootFileID {
		return nil, invalidRequest(dropboxFolderReason)
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID, input.FileID)
	if err != nil {
		return nil, err
	}
	return client.DeleteDropbox(ctx, input.DriveID, input.FileID)
}

// DeleteDropbox deletes exactly one dropbox, once.
func (c *Client) DeleteDropbox(ctx context.Context, driveID, fileID int64) (*DropboxResult, error) {
	const op = "delete dropbox"
	_, status, err := c.linkRequest(ctx, op, http.MethodDelete, dropboxPath(driveID, fileID), nil)
	if err != nil {
		return nil, err
	}
	return &DropboxResult{DriveID: driveID, FileID: fileID, Status: status}, nil
}
