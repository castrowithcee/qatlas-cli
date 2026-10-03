package infomaniakdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// dropboxSensitivity classifies a dropbox as its own class. Its URL lets anyone without an account upload
// into the folder, so it is an access capability, not metadata about the folder.
const dropboxSensitivity = "infomaniak-kdrive-dropboxes"

// Bounds of the arguments and of an answered dropbox.
const (
	maxDropboxAlias = 100
	maxDropboxText  = 256
	// maxDropboxFileSize is the largest per-file limit accepted, in bytes (1 TiB).
	maxDropboxFileSize int64 = 1 << 40
)

const (
	dropboxAliasSchema = `{"type":"string","minLength":1,"maxLength":100}`
	dropboxSizeSchema  = `{"type":"integer","minimum":1,"maximum":1099511627776}`
)

var dropboxSchema = `{"type":"object","properties":{` +
	`"id":{"type":"integer"},"uuid":{"type":"string"},"name":{"type":"string"},"url":{"type":"string"},` +
	`"users_count":{"type":"integer"},"created_at":{"type":"string"},"updated_at":{"type":"string"},` +
	`"last_uploaded_at":{"type":"string"},"has_password":{"type":"boolean"},"has_notification":{"type":"boolean"},` +
	`"has_validity":{"type":"boolean"},"has_size_limit":{"type":"boolean"}},` +
	`"required":["id"],"additionalProperties":false}`

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

var dropboxSettingArguments = []capability.Argument{
	{Name: "alias", Description: "Name of the dropbox, 1 to 100 characters"},
	{Name: "password", Description: "Password that protects the dropbox, 8 to 128 characters; never returned, " +
		"logged, or shown"},
	{Name: "valid_until", Description: "Time the dropbox expires, RFC 3339, in the future and within ten years"},
	{Name: "limit_file_size", Description: "Largest accepted file in bytes, 1 to 1099511627776"},
	{Name: "email_when_finished", Description: "Mail the dropbox owner when someone has uploaded"},
}

const dropboxSettingProperties = `"alias":` + dropboxAliasSchema + `,"password":` + linkPasswordSchema +
	`,"valid_until":` + linkExpirySchema + `,"limit_file_size":` + dropboxSizeSchema +
	`,"email_when_finished":` + linkFlagSchema

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
		objectIDSchema + `},"required":["drive_id","file_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(dropboxResultSchema),
	Arguments:    []capability.Argument{driveIDArgument, dropboxFolderArgument},
	Fields:       dropboxFields,
	Examples: []capability.Example{{Description: "Read the dropbox of one folder",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":43}`)}},
}

var dropboxCreate = capability.Descriptor{
	ID:      Provider + ".dropbox.create",
	Version: 1,
	Title:   "Create an Infomaniak kDrive dropbox",
	Description: "Create exactly one confirmed dropbox for a folder of a drive this connection may reach; anyone " +
		"holding its URL can upload into the folder without an account, so the folder becomes writable from " +
		"outside the drive; the password is never returned, and a plan without dropboxes refuses it",
	Tags:                  []string{"infomaniak", "kdrive", "dropbox", "upload", "create"},
	Risk:                  dropboxChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `,` + dropboxSettingProperties + `},"required":["drive_id","file_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(dropboxResultSchema),
	Arguments:    append([]capability.Argument{mutationDriveArgument, dropboxFolderArgument}, dropboxSettingArguments...),
	Fields:       dropboxFields,
	Examples: []capability.Example{{Description: "Create a dropbox that mails the owner after an upload",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":43,"alias":"Invoices","email_when_finished":true}`)}},
}

var dropboxUpdate = capability.Descriptor{
	ID:      Provider + ".dropbox.update",
	Version: 1,
	Title:   "Change an Infomaniak kDrive dropbox",
	Description: "Change exactly one confirmed dropbox of a folder of a drive this connection may reach; only the " +
		"given settings change, a setting cannot be removed, and the password is never returned",
	Tags:                  []string{"infomaniak", "kdrive", "dropbox", "upload", "update"},
	Risk:                  dropboxChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"drive_id":` + idSchema + `,"file_id":` +
		objectIDSchema + `,` + dropboxSettingProperties + `},"required":["drive_id","file_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(dropboxResultSchema),
	Arguments:    append([]capability.Argument{mutationDriveArgument, dropboxFolderArgument}, dropboxSettingArguments...),
	Fields:       dropboxFields,
	Examples: []capability.Example{{Description: "Limit uploads to 100 MiB per file",
		Arguments: json.RawMessage(`{"drive_id":1,"file_id":43,"limit_file_size":104857600}`)}},
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
	CreatedAt      *int64 `json:"created_at"`
	UpdatedAt      *int64 `json:"updated_at"`
	LastUploadedAt *int64 `json:"last_uploaded_at"`
	Capabilities   struct {
		HasPassword  bool `json:"has_password"`
		HasNotify    bool `json:"has_notification"`
		HasValidity  bool `json:"has_validity"`
		HasSizeLimit bool `json:"has_size_limit"`
	} `json:"capabilities"`
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
		HasSizeLimit: raw.Capabilities.HasSizeLimit}
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

type dropboxArguments struct {
	DriveID           int64  `json:"drive_id"`
	FileID            int64  `json:"file_id"`
	Alias             string `json:"alias"`
	Password          string `json:"password"`
	ValidUntil        string `json:"valid_until"`
	LimitFileSize     int64  `json:"limit_file_size"`
	EmailWhenFinished *bool  `json:"email_when_finished"`
}

// dropboxBody checks the settings of a create or an update and returns the one body they allow: only the
// documented fields, only when given. No value is ever quoted in a refusal.
func dropboxBody(input dropboxArguments, create bool) (map[string]any, error) {
	body := map[string]any{}
	if input.Alias != "" {
		if len(input.Alias) > maxDropboxAlias || !utf8.ValidString(input.Alias) || boundedText(input.Alias, maxDropboxAlias) != input.Alias {
			return nil, invalidRequest("alias must be 1 to 100 bytes without a control character")
		}
		body["alias"] = input.Alias
	}
	if input.Password != "" {
		if !validLinkPassword(input.Password) {
			return nil, invalidRequest("password must be 8 to 128 characters without a control character")
		}
		body["password"] = input.Password
	}
	if input.ValidUntil != "" {
		at, err := time.Parse(time.RFC3339, input.ValidUntil)
		now := linkNow()
		if err != nil || !at.After(now) || at.After(now.Add(maxLinkHorizon)) {
			return nil, invalidRequest("valid_until must be an RFC 3339 time in the future, at most ten years ahead")
		}
		body["valid_until"] = at.Unix()
	}
	if input.LimitFileSize != 0 {
		if input.LimitFileSize < 1 || input.LimitFileSize > maxDropboxFileSize {
			return nil, invalidRequest("limit_file_size must be 1 to 1099511627776 bytes")
		}
		body["limit_file_size"] = input.LimitFileSize
	}
	if input.EmailWhenFinished != nil {
		body["email_when_finished"] = *input.EmailWhenFinished
	}
	if !create && len(body) == 0 {
		return nil, invalidRequest("an update needs at least one setting to change")
	}
	return body, nil
}

func dropboxPath(driveID, fileID int64) string {
	return fmt.Sprintf("/2/drive/%d/files/%d/dropbox", driveID, fileID)
}

func invokeDropboxGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get dropbox"
	var input fileArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if !validObjectID(input.FileID) || input.FileID == rootFileID {
		return nil, invalidRequest(dropboxFolderReason)
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID)
	if err != nil {
		return nil, err
	}
	return client.GetDropbox(ctx, input.DriveID, input.FileID)
}

const dropboxFolderReason = "file_id must be a positive integer other than the drive's root"

// GetDropbox reads the dropbox of exactly one folder.
func (c *Client) GetDropbox(ctx context.Context, driveID, fileID int64) (*DropboxResult, error) {
	const op = "get dropbox"
	var raw dropboxJSON
	if err := c.do(ctx, op, dropboxPath(driveID, fileID), nil, &raw); err != nil {
		return nil, err
	}
	box, ok := dropboxOf(raw)
	if !ok {
		return nil, invalidResponse(op, "Infomaniak returned an invalid response")
	}
	return &DropboxResult{DriveID: driveID, FileID: fileID, Dropbox: box}, nil
}

func invokeDropboxCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeDropboxChange(ctx, resolved, secrets, red, raw, "create dropbox", true)
}

func invokeDropboxUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeDropboxChange(ctx, resolved, secrets, red, raw, "update dropbox", false)
}

// invokeDropboxChange runs the local checks in order: the password joins the redactor first, then the
// identifiers and settings, then the drive checks, and only then the one request.
func invokeDropboxChange(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, op string, create bool) (any, error) {
	var input dropboxArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if red != nil && input.Password != "" {
		red.Add(input.Password)
	}
	if !validObjectID(input.FileID) || input.FileID == rootFileID {
		return nil, invalidRequest(dropboxFolderReason)
	}
	body, err := dropboxBody(input, create)
	if err != nil {
		return nil, err
	}
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID)
	if err != nil {
		return nil, err
	}
	if create {
		return client.CreateDropbox(ctx, input.DriveID, input.FileID, body)
	}
	return client.UpdateDropbox(ctx, input.DriveID, input.FileID, body)
}

// CreateDropbox creates exactly one dropbox, once. body holds the settings dropboxBody validated.
func (c *Client) CreateDropbox(ctx context.Context, driveID, fileID int64, body map[string]any) (*DropboxResult, error) {
	const op = "create dropbox"
	env, status, err := c.linkRequest(ctx, op, http.MethodPost, dropboxPath(driveID, fileID), body)
	if err != nil {
		return nil, err
	}
	result := &DropboxResult{DriveID: driveID, FileID: fileID, Status: status}
	var raw dropboxJSON
	if json.Unmarshal(env.Data, &raw) == nil {
		if box, ok := dropboxOf(raw); ok {
			result.Dropbox = box
		}
	}
	if result.Dropbox == nil && status == statusDone {
		return nil, invalidResponse(op, "Infomaniak returned an invalid response"+uncertain)
	}
	return result, nil
}

// UpdateDropbox changes exactly one dropbox, once. Infomaniak answers a bare flag, so no dropbox is returned.
func (c *Client) UpdateDropbox(ctx context.Context, driveID, fileID int64, body map[string]any) (*DropboxResult, error) {
	const op = "update dropbox"
	_, status, err := c.linkRequest(ctx, op, http.MethodPut, dropboxPath(driveID, fileID), body)
	if err != nil {
		return nil, err
	}
	return &DropboxResult{DriveID: driveID, FileID: fileID, Status: status}, nil
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
	client, err := prepare(ctx, resolved, secrets, red, op, input.DriveID)
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
