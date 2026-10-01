package penpot

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const snapshotIDSchema = uuidSchema

var snapshotIDArgument = capability.Argument{Name: "snapshot_id", Required: true,
	Description: "Snapshot identifier; must be a visible snapshot of the file, for example the one snapshots.create returned"}

var snapshotsCreate = capability.Descriptor{
	ID: Provider + ".snapshots.create", Version: 1, Title: "Create a Penpot file snapshot",
	Description: "Create a named snapshot of the current state of one file of a project of a bound team. The file " +
		"is bound through its project first. Penpot limits the number of snapshots per file and team. " + manageNote,
	Tags: []string{"penpot", "snapshots", "create", "design"}, Provider: Provider,
	Risk:        manageRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: schemaOf(`"project_id":`+uuidSchema+`,"file_id":`+uuidSchema+`,"label":`+nameSchema, `"project_id","file_id"`),
	OutputSchema: schemaOf(`"created":{"type":"boolean"},"snapshot_id":{"type":"string"},"file_id":{"type":"string"},`+
		`"project_id":{"type":"string"},"revn":{"type":"integer"}`, `"created","snapshot_id","file_id","project_id"`),
	Arguments: []capability.Argument{projectIDArgument, fileIDArgument,
		{Name: "label", Description: "Snapshot label, 1 to 250 characters; Penpot generates one when omitted"}},
	Fields: []capability.Field{
		{Name: "created", Description: "True when Penpot created the snapshot"},
		{Name: "snapshot_id", Description: "Identifier of the new snapshot, used by penpot.snapshots.restore"},
		{Name: "file_id", Description: "Identifier of the file"},
		{Name: "project_id", Description: "Identifier of the project"},
		{Name: "revn", Description: "Revision number of the file at the snapshot"},
	},
	Examples: []capability.Example{{Description: "Snapshot a file before a larger change",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003","label":"Before redesign"}`)}},
}

var snapshotsRestore = capability.Descriptor{
	ID: Provider + ".snapshots.restore", Version: 1, Title: "Restore a Penpot file snapshot",
	Description: "Overwrite the current content of one file with one of its snapshots. The snapshot id is checked " +
		"against the visible snapshots of the bound file first. Penpot keeps a temporary backup snapshot of the " +
		"state before the restore; changes made in the file since the snapshot are replaced. Needs the tool " +
		"allow-list. " + manageNote,
	Tags: []string{"penpot", "snapshots", "restore", "design"}, Provider: Provider, RequiresToolAllowList: true,
	Risk: manageRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: schemaOf(`"project_id":`+uuidSchema+`,"file_id":`+uuidSchema+`,"snapshot_id":`+snapshotIDSchema,
		`"project_id","file_id","snapshot_id"`),
	OutputSchema: schemaOf(`"restored":{"type":"boolean"},"file_id":{"type":"string"},"project_id":{"type":"string"},`+
		`"snapshot_id":{"type":"string"}`, `"restored","file_id","project_id","snapshot_id"`),
	Arguments: []capability.Argument{projectIDArgument, fileIDArgument, snapshotIDArgument},
	Fields: []capability.Field{
		{Name: "restored", Description: "True when Penpot accepted the restore"},
		{Name: "file_id", Description: "Identifier of the file"},
		{Name: "project_id", Description: "Identifier of the project"},
		{Name: "snapshot_id", Description: "Identifier of the restored snapshot"},
	},
	Examples: []capability.Example{{Description: "Restore a snapshot",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003","snapshot_id":"00000000-0000-0000-0000-000000000005"}`)}},
}

var filesDelete = capability.Descriptor{
	ID: Provider + ".files.delete", Version: 1, Title: "Delete a Penpot file",
	Description: "Soft-delete one file of a project of a bound team: Penpot marks it as deleted, removes its " +
		"library links, and removes it for good after its deletion delay. penpot.files.restore brings it back " +
		"until then. Needs the tool allow-list. " + manageNote,
	Tags: []string{"penpot", "files", "delete", "design"}, Provider: Provider, RequiresToolAllowList: true,
	Risk:        manageRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: schemaOf(`"project_id":`+uuidSchema+`,"file_id":`+uuidSchema, `"project_id","file_id"`),
	OutputSchema: schemaOf(`"deleted":{"type":"boolean"},"file_id":{"type":"string"},"project_id":{"type":"string"}`,
		`"deleted","file_id","project_id"`),
	Arguments: []capability.Argument{projectIDArgument, fileIDArgument},
	Fields: []capability.Field{
		{Name: "deleted", Description: "True when Penpot accepted the deletion"},
		{Name: "file_id", Description: "Identifier of the file"},
		{Name: "project_id", Description: "Identifier of the project"},
	},
	Examples: []capability.Example{{Description: "Soft-delete a file",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003"}`)}},
}

var deletedFileProjectArgument = capability.Argument{Name: "project_id", Required: true,
	Description: "Project the file was deleted from; must be inside this connection's targets"}

var deletedFileArgument = capability.Argument{Name: "file_id", Required: true,
	Description: "Identifier of a deleted file of project_id, known from before its deletion"}

var filesRestore = capability.Descriptor{
	ID: Provider + ".files.restore", Version: 1, Title: "Restore a deleted Penpot file",
	Description: "Remove the deletion mark of one deleted file. The file is bound through the deleted files of " +
		"the bound team and its project; the team comes from that binding. Penpot also removes the deletion mark " +
		"of the file's project. " + manageNote,
	Tags: []string{"penpot", "files", "restore", "design"}, Provider: Provider,
	Risk:        manageRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: schemaOf(`"project_id":`+uuidSchema+`,"file_id":`+uuidSchema, `"project_id","file_id"`),
	OutputSchema: schemaOf(`"restored":{"type":"boolean"},"file_id":{"type":"string"},"project_id":{"type":"string"}`,
		`"restored","file_id","project_id"`),
	Arguments: []capability.Argument{deletedFileProjectArgument, deletedFileArgument},
	Fields: []capability.Field{
		{Name: "restored", Description: "True when Penpot restored the file"},
		{Name: "file_id", Description: "Identifier of the file"},
		{Name: "project_id", Description: "Identifier of the project"},
	},
	Examples: []capability.Example{{Description: "Restore a deleted file",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003"}`)}},
}

var filesPurge = capability.Descriptor{
	ID: Provider + ".files.purge", Version: 1, Title: "Permanently delete a Penpot file",
	Description: "Delete one already deleted file for good, without waiting for its deletion delay. This cannot be " +
		"undone. The file is bound through the deleted files of the bound team and its project; the team comes " +
		"from that binding, and a file that is not deleted is refused. Needs the tool allow-list. " + manageNote,
	Tags: []string{"penpot", "files", "purge", "delete", "design"}, Provider: Provider, RequiresToolAllowList: true,
	Risk:        manageRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema: schemaOf(`"project_id":`+uuidSchema+`,"file_id":`+uuidSchema, `"project_id","file_id"`),
	OutputSchema: schemaOf(`"purged":{"type":"boolean"},"file_id":{"type":"string"},"project_id":{"type":"string"}`,
		`"purged","file_id","project_id"`),
	Arguments: []capability.Argument{deletedFileProjectArgument, deletedFileArgument},
	Fields: []capability.Field{
		{Name: "purged", Description: "True when Penpot accepted the permanent deletion"},
		{Name: "file_id", Description: "Identifier of the file"},
		{Name: "project_id", Description: "Identifier of the project"},
	},
	Examples: []capability.Example{{Description: "Permanently delete a deleted file",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003"}`)}},
}

// SnapshotCreated, SnapshotRestored, FileDeleted, FileRestored, and FilePurged are the answers of the tools;
// they carry identifiers only.
type SnapshotCreated struct {
	Created    bool   `json:"created"`
	SnapshotID string `json:"snapshot_id"`
	FileID     string `json:"file_id"`
	ProjectID  string `json:"project_id"`
	Revn       int64  `json:"revn,omitempty"`
}

type SnapshotRestored struct {
	Restored   bool   `json:"restored"`
	FileID     string `json:"file_id"`
	ProjectID  string `json:"project_id"`
	SnapshotID string `json:"snapshot_id"`
}

type FileDeleted struct {
	Deleted   bool   `json:"deleted"`
	FileID    string `json:"file_id"`
	ProjectID string `json:"project_id"`
}

type FileRestored struct {
	Restored  bool   `json:"restored"`
	FileID    string `json:"file_id"`
	ProjectID string `json:"project_id"`
}

type FilePurged struct {
	Purged    bool   `json:"purged"`
	FileID    string `json:"file_id"`
	ProjectID string `json:"project_id"`
}

type recoveryArguments struct {
	ProjectID  string `json:"project_id"`
	FileID     string `json:"file_id"`
	SnapshotID string `json:"snapshot_id"`
	Label      string `json:"label"`
}

func readRecovery(op string, raw json.RawMessage) (recoveryArguments, error) {
	var input recoveryArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

func (a recoveryArguments) file() manageArguments {
	return manageArguments{ProjectID: a.ProjectID, FileID: a.FileID}
}

func invokeSnapshotsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create snapshot"
	input, err := readRecovery(op, raw)
	if err != nil {
		return nil, err
	}
	params := map[string]any{}
	if input.Label != "" {
		label, err := checkName(input.Label)
		if err != nil {
			return nil, invalidRequest("label must be valid text of 1 to 250 characters without control characters")
		}
		params["label"] = label
	}
	client, projectID, fileID, err := boundFileOf(ctx, op, resolved, secrets, red, input.file())
	if err != nil {
		return nil, err
	}
	params["file-id"] = fileID
	data, err := client.change(ctx, op, cmdCreateSnapshot, params)
	if err != nil {
		return nil, err
	}
	answer, ok := asObj(data)
	if !ok || answer.id("id") == "" {
		return nil, invalidResponse(op, "Penpot returned an invalid response"+changeUncertain)
	}
	return &SnapshotCreated{Created: true, SnapshotID: answer.id("id"), FileID: fileID, ProjectID: projectID,
		Revn: answer.integer("revn")}, nil
}

func invokeSnapshotsRestore(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "restore snapshot"
	input, err := readRecovery(op, raw)
	if err != nil {
		return nil, err
	}
	snapshotID, ok := parseUUID(input.SnapshotID)
	if !ok {
		return nil, invalidRequest("snapshot_id must be a UUID")
	}
	client, projectID, fileID, err := boundFileOf(ctx, op, resolved, secrets, red, input.file())
	if err != nil {
		return nil, err
	}
	var listed json.RawMessage
	if err := client.do(ctx, op, cmdSnapshots, map[string]any{"file-id": fileID}, &listed); err != nil {
		return nil, err
	}
	entries, ok := objects(listed)
	if !ok {
		return nil, invalidResponse(op, "Penpot returned an invalid response")
	}
	found := false
	for _, entry := range entries {
		if entry.id("id") == snapshotID {
			found = true
			break
		}
	}
	if !found {
		return nil, invalidRequest("snapshot_id is not a snapshot of this file")
	}
	if _, err := client.change(ctx, op, cmdRestoreSnapshot, map[string]any{"file-id": fileID, "id": snapshotID}); err != nil {
		return nil, err
	}
	return &SnapshotRestored{Restored: true, FileID: fileID, ProjectID: projectID, SnapshotID: snapshotID}, nil
}

func invokeFilesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete file"
	input, err := readRecovery(op, raw)
	if err != nil {
		return nil, err
	}
	client, projectID, fileID, err := boundFileOf(ctx, op, resolved, secrets, red, input.file())
	if err != nil {
		return nil, err
	}
	if _, err := client.change(ctx, op, cmdDeleteFile, map[string]any{"id": fileID}); err != nil {
		return nil, err
	}
	return &FileDeleted{Deleted: true, FileID: fileID, ProjectID: projectID}, nil
}

// deletedFileOf binds a deleted file: it is looked up in the deleted files of each bound team and must report
// the given project, which has passed the allow-list. It returns the team that holds the file. A deleted file
// that is not found, or that belongs to another project, is refused without naming it.
func (c *Client) deletedFileOf(ctx context.Context, op, projectID, fileID string) (string, error) {
	for _, teamID := range c.scope.teams {
		var raw json.RawMessage
		if err := c.do(ctx, op, cmdDeletedFiles, map[string]any{"team-id": teamID}, &raw); err != nil {
			return "", err
		}
		entries, ok := objects(raw)
		if !ok {
			return "", invalidResponse(op, "Penpot returned an invalid response")
		}
		for _, entry := range entries {
			if entry.id("id") != fileID {
				continue
			}
			if reported := entry.id("teamid"); reported != "" && reported != teamID {
				continue
			}
			if entry.id("projectid") != projectID || !c.scope.allowsProject(projectID) {
				continue
			}
			return teamID, nil
		}
	}
	return "", invalidRequest("file_id is not a deleted file of this project")
}

// boundDeletedFileOf is the common start of restore and purge: local checks first, then the credential, then
// the deleted file bound through the team's deleted files and its project.
func boundDeletedFileOf(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, input recoveryArguments) (*Client, string, string, string, error) {
	projectID, err := selectProject(resolved, input.ProjectID)
	if err != nil {
		return nil, "", "", "", err
	}
	fileID, ok := parseUUID(input.FileID)
	if !ok {
		return nil, "", "", "", invalidRequest("file_id must be a UUID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, "", "", "", err
	}
	teamID, err := client.deletedFileOf(ctx, op, projectID, fileID)
	if err != nil {
		return nil, "", "", "", err
	}
	return client, teamID, projectID, fileID, nil
}

// changeFiles sends restore or purge for exactly one file. Both commands answer with an event stream; its
// result is the set of files Penpot acted on, which must hold the file.
func (c *Client) changeFiles(ctx context.Context, op, command, teamID, fileID string) error {
	data, err := c.change(ctx, op, command, map[string]any{"team-id": teamID, "ids": []string{fileID}})
	if err != nil {
		return err
	}
	result, err := streamResult(op, data)
	if err != nil {
		return err
	}
	var done []string
	if err := json.Unmarshal(result, &done); err != nil {
		return invalidResponse(op, "Penpot returned an invalid response"+changeUncertain)
	}
	for _, id := range done {
		if parsed, ok := parseUUID(id); ok && parsed == fileID {
			return nil
		}
	}
	return providerError(op, "Penpot did not apply the change to the file")
}

func invokeFilesRestore(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "restore file"
	input, err := readRecovery(op, raw)
	if err != nil {
		return nil, err
	}
	client, teamID, projectID, fileID, err := boundDeletedFileOf(ctx, op, resolved, secrets, red, input)
	if err != nil {
		return nil, err
	}
	if err := client.changeFiles(ctx, op, cmdRestoreFiles, teamID, fileID); err != nil {
		return nil, err
	}
	return &FileRestored{Restored: true, FileID: fileID, ProjectID: projectID}, nil
}

func invokeFilesPurge(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "purge file"
	input, err := readRecovery(op, raw)
	if err != nil {
		return nil, err
	}
	client, teamID, projectID, fileID, err := boundDeletedFileOf(ctx, op, resolved, secrets, red, input)
	if err != nil {
		return nil, err
	}
	if err := client.changeFiles(ctx, op, cmdPurgeFiles, teamID, fileID); err != nil {
		return nil, err
	}
	return &FilePurged{Purged: true, FileID: fileID, ProjectID: projectID}, nil
}

// streamResult reads the result of a command that answers with server-sent events: the data of the "end" event.
// An "error" event is a failure whose text is never read into the message; a stream without a final event
// leaves the result open.
func streamResult(op string, data []byte) (json.RawMessage, error) {
	event, payload := "", []byte(nil)
	finish := func() (json.RawMessage, error, bool) {
		defer func() { event, payload = "", nil }()
		switch event {
		case "end":
			return json.RawMessage(payload), nil, true
		case "error":
			return nil, providerError(op, "Penpot rejected the operation"), true
		}
		return nil, nil, false
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		switch {
		case len(line) == 0:
			if result, err, done := finish(); done {
				return result, err
			}
		case bytes.HasPrefix(line, []byte("event:")):
			event = string(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("event:"))))
		case bytes.HasPrefix(line, []byte("data:")):
			payload = append(payload, bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))...)
		}
	}
	if result, err, done := finish(); done {
		return result, err
	}
	return nil, invalidResponse(op, "the Penpot event stream ended without a result"+changeUncertain)
}
