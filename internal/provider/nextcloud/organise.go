package nextcloud

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The hints of an unclear organising request. The request was sent once and is never repeated.
const (
	uncertainFolder   = "; the folder may have been created, stat the folder before repeating"
	uncertainOrganise = "; the change may have been applied, stat source and target before repeating"
)

// Methods of the organising operations; each handler sends exactly one of them.
const (
	methodMkcol = "MKCOL"
	methodMove  = "MOVE"
	methodCopy  = "COPY"
)

func organiseRisk(effect capability.Effect) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: capability.IdempotencyNonIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity}
}

const folderPathArgument = "Path relative to the fixed root folder of this connection"

var foldersCreate = capability.Descriptor{
	ID: Provider + ".folders.create", Version: 1,
	Title: "Create a Nextcloud folder",
	Description: "Create exactly one confirmed folder below an existing folder of the fixed root of a connection; " +
		"a path that already exists or a missing parent folder is refused",
	Tags: []string{"nextcloud", "folders", "webdav", "create"}, Provider: Provider,
	Risk: organiseRisk(capability.EffectCreate),
	InputSchema: json.RawMessage(`{"type":"object","properties":{"path":` + pathSchema +
		`},"required":["path"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"created":{"type":"boolean"},` +
		`"path":{"type":"string"}},"required":["created"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "path", Required: true,
		Description: "Folder to create, relative to the fixed root folder of this connection; its parent must exist"}},
	Fields: []capability.Field{
		{Name: "created", Description: "True when Nextcloud created the folder"},
		{Name: "path", Description: "Path of the new folder"},
	},
	Examples: []capability.Example{{Description: "Create a folder", Arguments: json.RawMessage(`{"path":"2026/Invoices"}`)}},
}

var foldersDelete = capability.Descriptor{
	ID: Provider + ".folders.delete", Version: 1,
	Title: "Delete a Nextcloud folder",
	Description: "Delete one confirmed folder below the fixed root of a connection together with everything in it, " +
		"bound to the folder ETag; the root itself is never deleted. Nextcloud moves the folder to the trash bin " +
		"when the files_trashbin app is active, otherwise it is deleted for good",
	Tags: []string{"nextcloud", "folders", "webdav", "delete"}, Provider: Provider, RequiresToolAllowList: true,
	Risk: capability.Risk{Effect: capability.EffectDelete, Idempotency: capability.IdempotencyIdempotent,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
	InputSchema: json.RawMessage(`{"type":"object","properties":{"path":` + pathSchema + `,` +
		`"etag":{"type":"string","minLength":1,"maxLength":1024,"pattern":"[^*\\s\"]","x-form":"the ETag of the folder, never *"}},` +
		`"required":["path","etag"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},` +
		`"required":["deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "path", Required: true, Description: "Folder relative to the fixed root folder of this connection; never the root itself"},
		{Name: "etag", Required: true, Description: "Entity tag of the folder version to delete, from files.stat or files.list"},
	},
	Fields:   []capability.Field{{Name: "deleted", Description: "True when Nextcloud applied the deletion"}},
	Examples: []capability.Example{{Description: "Delete a folder with its content", Arguments: json.RawMessage(`{"path":"2026/Old","etag":"abc123"}`)}},
}

func transferDescriptor(action, done, title, description string, effect capability.Effect) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + ".files." + action, Version: 1, Title: title, Description: description,
		Tags: []string{"nextcloud", "files", "webdav", action}, Provider: Provider,
		Risk: organiseRisk(effect),
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":` + pathSchema + `,"destination":` +
			pathSchema + `},"required":["path","destination"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"` + done + `":{"type":"boolean"},` +
			`"path":{"type":"string"},"destination":{"type":"string"}},"required":["` + done + `"],"additionalProperties":false}`),
		Arguments: []capability.Argument{
			{Name: "path", Required: true, Description: "File or folder to " + action + ", relative to the fixed root folder of this connection; never the root itself"},
			{Name: "destination", Required: true, Description: "New path of the file or folder, relative to the same root; its parent folder must exist, the path itself must not, and a folder cannot go into itself"},
		},
		Fields: []capability.Field{
			{Name: done, Description: "True when Nextcloud applied the change"},
			{Name: "path", Description: "Source path"},
			{Name: "destination", Description: "Destination path"},
		},
		Examples: []capability.Example{{Description: "Use the same parent folder to rename",
			Arguments: json.RawMessage(`{"path":"2026/draft.txt","destination":"2026/final.txt"}`)}},
	}
}

var filesMove = transferDescriptor("move", "moved", "Move or rename a Nextcloud file or folder",
	"Move or rename exactly one confirmed file or folder below the fixed root of a connection, within that root; "+
		"an existing destination is never overwritten", capability.EffectUpdate)

var filesCopy = transferDescriptor("copy", "copied", "Copy a Nextcloud file or folder",
	"Copy exactly one confirmed file or folder below the fixed root of a connection, within that root; "+
		"an existing destination is never overwritten", capability.EffectCreate)

type transferArguments struct {
	Path        string `json:"path"`
	Destination string `json:"destination"`
}

// transferPaths reads and checks source and destination locally: both lie below the root, neither is the
// root, they differ, and the destination is no descendant of the source.
func transferPaths(op string, input transferArguments) (source, destination []string, err error) {
	source, srcErr := splitRelative(input.Path)
	destination, dstErr := splitRelative(input.Destination)
	if srcErr != nil || dstErr != nil || len(source) == 0 || len(destination) == 0 {
		return nil, nil, providerError(op, "source and destination must be paths below the connection root")
	}
	if equalSegments(source, destination) {
		return nil, nil, providerError(op, "source and destination must differ")
	}
	if len(destination) > len(source) && equalSegments(destination[:len(source)], source) {
		return nil, nil, providerError(op, "a folder cannot be moved or copied into itself")
	}
	return source, destination, nil
}

func invokeFoldersCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create folder"
	var input arguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	rel, err := splitRelative(input.Path)
	if err != nil || len(rel) == 0 {
		return nil, providerError(op, "a folder path below the connection root is required")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	response, err := client.webdav(ctx, op, methodMkcol, rel, nil, "", "", uncertainFolder)
	if err != nil {
		return nil, err
	}
	response.Body.Close()
	return map[string]any{"created": true, "path": input.Path}, nil
}

func invokeFilesMove(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeTransfer(ctx, resolved, secrets, red, raw, "move file", methodMove, "moved")
}

func invokeFilesCopy(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeTransfer(ctx, resolved, secrets, red, raw, "copy file", methodCopy, "copied")
}

// invokeTransfer sends one MOVE or COPY. Overwrite: F makes Nextcloud refuse an existing destination, and
// the Destination is built from the validated segments on the configured origin.
func invokeTransfer(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage, op, method, done string) (any, error) {
	var input transferArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	source, destination, err := transferPaths(op, input)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	headers := http.Header{"Destination": {client.requestURL(destination)}, "Overwrite": {"F"}}
	response, err := client.webdavWith(ctx, op, method, source, nil, "", "", uncertainOrganise, headers)
	if err != nil {
		return nil, err
	}
	response.Body.Close()
	return map[string]any{done: true, "path": input.Path, "destination": input.Destination}, nil
}
