package penpot

import (
	"context"
	"encoding/json"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// maxNameLength is Penpot's own limit of a project or file name, in characters.
const maxNameLength = 250

const nameSchema = `{"type":"string","minLength":1,"maxLength":250}`

func manageRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: metadataSensitive}
}

const manageNote = "Names are untrusted provider data; the change is sent once and never repeated"

func schemaOf(properties, required string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` + properties + `},"required":[` + required +
		`],"additionalProperties":false}`)
}

var nameArgument = capability.Argument{Name: "name", Required: true, Description: "New name, 1 to 250 characters"}

var projectsCreate = capability.Descriptor{
	ID: Provider + ".projects.create", Version: 1, Title: "Create a Penpot project",
	Description: "Create a project in one bound team. Refused on a connection that has a project allow-list. " + manageNote,
	Tags:        []string{"penpot", "projects", "create", "design"}, Provider: Provider,
	Risk:        manageRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: schemaOf(`"team_id":`+uuidSchema+`,"name":`+nameSchema, `"team_id","name"`),
	OutputSchema: schemaOf(`"project_id":{"type":"string"},"team_id":{"type":"string"}`,
		`"project_id","team_id"`),
	Arguments: []capability.Argument{teamIDArgument,
		{Name: "name", Required: true, Description: "Project name, 1 to 250 characters"}},
	Fields: []capability.Field{
		{Name: "project_id", Description: "Identifier of the new project, used as project_id by the file tools"},
		{Name: "team_id", Description: "Identifier of the team"},
	},
	Examples: []capability.Example{{Description: "Create a project",
		Arguments: json.RawMessage(`{"team_id":"00000000-0000-0000-0000-000000000001","name":"Website"}`)}},
}

var projectsRename = capability.Descriptor{
	ID: Provider + ".projects.rename", Version: 1, Title: "Rename a Penpot project",
	Description: "Rename one project of a bound team. " + manageNote,
	Tags:        []string{"penpot", "projects", "rename", "design"}, Provider: Provider,
	Risk:        manageRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: schemaOf(`"project_id":`+uuidSchema+`,"name":`+nameSchema, `"project_id","name"`),
	OutputSchema: schemaOf(`"renamed":{"type":"boolean"},"project_id":{"type":"string"}`,
		`"renamed","project_id"`),
	Arguments: []capability.Argument{projectIDArgument, nameArgument},
	Fields: []capability.Field{
		{Name: "renamed", Description: "True when Penpot accepted the change"},
		{Name: "project_id", Description: "Identifier of the project"},
	},
	Examples: []capability.Example{{Description: "Rename a project",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","name":"Website 2027"}`)}},
}

var projectsDelete = capability.Descriptor{
	ID: Provider + ".projects.delete", Version: 1, Title: "Delete a Penpot project",
	Description: "Delete one project of a bound team with all its files. Penpot marks the project as deleted and " +
		"removes it after its deletion delay; it refuses the default project of a team. A project that is in the " +
		"connection's project allow-list may be deleted. " + manageNote,
	Tags: []string{"penpot", "projects", "delete", "design"}, Provider: Provider, RequiresToolAllowList: true,
	Risk:         manageRisk(capability.EffectDelete, capability.IdempotencyIdempotent),
	InputSchema:  schemaOf(`"project_id":`+uuidSchema, `"project_id"`),
	OutputSchema: schemaOf(`"deleted":{"type":"boolean"},"project_id":{"type":"string"}`, `"deleted","project_id"`),
	Arguments:    []capability.Argument{projectIDArgument},
	Fields: []capability.Field{
		{Name: "deleted", Description: "True when Penpot accepted the deletion"},
		{Name: "project_id", Description: "Identifier of the project"},
	},
	Examples: []capability.Example{{Description: "Delete a project",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002"}`)}},
}

var filesCreate = capability.Descriptor{
	ID: Provider + ".files.create", Version: 1, Title: "Create a Penpot file",
	Description: "Create an empty file in one project of a bound team. " + manageNote,
	Tags:        []string{"penpot", "files", "create", "design"}, Provider: Provider,
	Risk:        manageRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	InputSchema: schemaOf(`"project_id":`+uuidSchema+`,"name":`+nameSchema, `"project_id","name"`),
	OutputSchema: schemaOf(`"file_id":{"type":"string"},"project_id":{"type":"string"}`,
		`"file_id","project_id"`),
	Arguments: []capability.Argument{projectIDArgument,
		{Name: "name", Required: true, Description: "File name, 1 to 250 characters"}},
	Fields: []capability.Field{
		{Name: "file_id", Description: "Identifier of the new file"},
		{Name: "project_id", Description: "Identifier of the project"},
	},
	Examples: []capability.Example{{Description: "Create a file",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","name":"Landing page"}`)}},
}

var filesRename = capability.Descriptor{
	ID: Provider + ".files.rename", Version: 1, Title: "Rename a Penpot file",
	Description: "Rename one file of a project of a bound team. The file is bound through its project first. " + manageNote,
	Tags:        []string{"penpot", "files", "rename", "design"}, Provider: Provider,
	Risk: manageRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: schemaOf(`"project_id":`+uuidSchema+`,"file_id":`+uuidSchema+`,"name":`+nameSchema,
		`"project_id","file_id","name"`),
	OutputSchema: schemaOf(`"renamed":{"type":"boolean"},"file_id":{"type":"string"},"project_id":{"type":"string"}`,
		`"renamed","file_id","project_id"`),
	Arguments: []capability.Argument{projectIDArgument, fileIDArgument, nameArgument},
	Fields: []capability.Field{
		{Name: "renamed", Description: "True when Penpot accepted the change"},
		{Name: "file_id", Description: "Identifier of the file"},
		{Name: "project_id", Description: "Identifier of the project"},
	},
	Examples: []capability.Example{{Description: "Rename a file",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003","name":"Landing page v2"}`)}},
}

var filesMove = capability.Descriptor{
	ID: Provider + ".files.move", Version: 1, Title: "Move a Penpot file",
	Description: "Move one file from its project to another project; both projects must be inside this connection's " +
		"targets and differ. Penpot removes library links of the file that would cross teams. " + manageNote,
	Tags: []string{"penpot", "files", "move", "design"}, Provider: Provider,
	Risk: manageRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: schemaOf(`"project_id":`+uuidSchema+`,"file_id":`+uuidSchema+`,"target_project_id":`+uuidSchema,
		`"project_id","file_id","target_project_id"`),
	OutputSchema: schemaOf(`"moved":{"type":"boolean"},"file_id":{"type":"string"},"project_id":{"type":"string"},`+
		`"previous_project_id":{"type":"string"}`, `"moved","file_id","project_id","previous_project_id"`),
	Arguments: []capability.Argument{
		{Name: "project_id", Required: true, Description: "Project that holds the file now; must be inside this connection's targets"},
		fileIDArgument,
		{Name: "target_project_id", Required: true, Description: "Project to move the file to; must differ from project_id and be inside this connection's targets"}},
	Fields: []capability.Field{
		{Name: "moved", Description: "True when Penpot accepted the move"},
		{Name: "file_id", Description: "Identifier of the file"},
		{Name: "project_id", Description: "Identifier of the project that now holds the file"},
		{Name: "previous_project_id", Description: "Identifier of the project the file was moved from"},
	},
	Examples: []capability.Example{{Description: "Move a file to another project",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003","target_project_id":"00000000-0000-0000-0000-000000000004"}`)}},
}

// ProjectCreated is the answer of projects.create; it carries identifiers only.
type ProjectCreated struct {
	ProjectID string `json:"project_id"`
	TeamID    string `json:"team_id"`
}

// ProjectRenamed and ProjectDeleted are the answers of projects.rename and projects.delete.
type ProjectRenamed struct {
	Renamed   bool   `json:"renamed"`
	ProjectID string `json:"project_id"`
}

type ProjectDeleted struct {
	Deleted   bool   `json:"deleted"`
	ProjectID string `json:"project_id"`
}

// FileCreated, FileRenamed, and FileMoved are the answers of files.create, files.rename, and files.move.
type FileCreated struct {
	FileID    string `json:"file_id"`
	ProjectID string `json:"project_id"`
}

type FileRenamed struct {
	Renamed   bool   `json:"renamed"`
	FileID    string `json:"file_id"`
	ProjectID string `json:"project_id"`
}

type FileMoved struct {
	Moved             bool   `json:"moved"`
	FileID            string `json:"file_id"`
	ProjectID         string `json:"project_id"`
	PreviousProjectID string `json:"previous_project_id"`
}

// manageArguments is the union of the arguments of the six tools.
type manageArguments struct {
	TeamID          string `json:"team_id"`
	ProjectID       string `json:"project_id"`
	FileID          string `json:"file_id"`
	TargetProjectID string `json:"target_project_id"`
	Name            string `json:"name"`
}

func readManage(op string, raw json.RawMessage) (manageArguments, error) {
	var input manageArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

// checkName accepts one project or file name: valid UTF-8, 1 to 250 characters, not blank, no control
// characters.
func checkName(name string) (string, error) {
	const reason = "name must be valid text of 1 to 250 characters without control characters"
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxNameLength {
		return "", invalidRequest(reason)
	}
	blank := true
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", invalidRequest(reason)
		}
		if !unicode.IsSpace(r) {
			blank = false
		}
	}
	if blank {
		return "", invalidRequest(reason)
	}
	return name, nil
}

func invokeProjectsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create project"
	input, err := readManage(op, raw)
	if err != nil {
		return nil, err
	}
	teamID, err := selectTeamForNewProject(resolved, input.TeamID)
	if err != nil {
		return nil, err
	}
	name, err := checkName(input.Name)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	data, err := client.change(ctx, op, cmdCreateProject, map[string]any{"team-id": teamID, "name": name})
	if err != nil {
		return nil, err
	}
	answer, ok := asObj(data)
	if !ok || answer.id("id") == "" {
		return nil, invalidResponse(op, "Penpot returned an invalid response"+changeUncertain)
	}
	return &ProjectCreated{ProjectID: answer.id("id"), TeamID: teamID}, nil
}

func invokeProjectsRename(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "rename project"
	input, err := readManage(op, raw)
	if err != nil {
		return nil, err
	}
	projectID, err := selectProject(resolved, input.ProjectID)
	if err != nil {
		return nil, err
	}
	name, err := checkName(input.Name)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.locateProject(ctx, op, projectID); err != nil {
		return nil, err
	}
	if _, err := client.change(ctx, op, cmdRenameProject, map[string]any{"id": projectID, "name": name}); err != nil {
		return nil, err
	}
	return &ProjectRenamed{Renamed: true, ProjectID: projectID}, nil
}

func invokeProjectsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete project"
	input, err := readManage(op, raw)
	if err != nil {
		return nil, err
	}
	projectID, err := selectProject(resolved, input.ProjectID)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.locateProject(ctx, op, projectID); err != nil {
		return nil, err
	}
	if _, err := client.change(ctx, op, cmdDeleteProject, map[string]any{"id": projectID}); err != nil {
		return nil, err
	}
	return &ProjectDeleted{Deleted: true, ProjectID: projectID}, nil
}

func invokeFilesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create file"
	input, err := readManage(op, raw)
	if err != nil {
		return nil, err
	}
	projectID, err := selectProject(resolved, input.ProjectID)
	if err != nil {
		return nil, err
	}
	name, err := checkName(input.Name)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.locateProject(ctx, op, projectID); err != nil {
		return nil, err
	}
	data, err := client.change(ctx, op, cmdCreateFile, map[string]any{"project-id": projectID, "name": name})
	if err != nil {
		return nil, err
	}
	answer, ok := asObj(data)
	if !ok || answer.id("id") == "" {
		return nil, invalidResponse(op, "Penpot returned an invalid response"+changeUncertain)
	}
	return &FileCreated{FileID: answer.id("id"), ProjectID: projectID}, nil
}

// boundFileOf is the common start of the file changes: local checks first, then the credential, then the
// file bound through its project.
func boundFileOf(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, input manageArguments) (*Client, string, string, error) {
	projectID, err := selectProject(resolved, input.ProjectID)
	if err != nil {
		return nil, "", "", err
	}
	fileID, ok := parseUUID(input.FileID)
	if !ok {
		return nil, "", "", invalidRequest("file_id must be a UUID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, "", "", err
	}
	if _, err := client.fileOf(ctx, op, projectID, fileID); err != nil {
		return nil, "", "", err
	}
	return client, projectID, fileID, nil
}

func invokeFilesRename(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "rename file"
	input, err := readManage(op, raw)
	if err != nil {
		return nil, err
	}
	name, err := checkName(input.Name)
	if err != nil {
		return nil, err
	}
	client, projectID, fileID, err := boundFileOf(ctx, op, resolved, secrets, red, input)
	if err != nil {
		return nil, err
	}
	if _, err := client.change(ctx, op, cmdRenameFile, map[string]any{"id": fileID, "name": name}); err != nil {
		return nil, err
	}
	return &FileRenamed{Renamed: true, FileID: fileID, ProjectID: projectID}, nil
}

func invokeFilesMove(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "move file"
	input, err := readManage(op, raw)
	if err != nil {
		return nil, err
	}
	target, err := selectProjectAs(resolved, "target_project_id", input.TargetProjectID)
	if err != nil {
		return nil, err
	}
	source, err := selectProject(resolved, input.ProjectID)
	if err != nil {
		return nil, err
	}
	if target == source {
		return nil, invalidRequest("target_project_id must differ from project_id")
	}
	client, projectID, fileID, err := boundFileOf(ctx, op, resolved, secrets, red, input)
	if err != nil {
		return nil, err
	}
	if err := client.locateProjectAs(ctx, op, "target_project_id", target); err != nil {
		return nil, err
	}
	if _, err := client.change(ctx, op, cmdMoveFiles, map[string]any{"ids": []string{fileID}, "project-id": target}); err != nil {
		return nil, err
	}
	return &FileMoved{Moved: true, FileID: fileID, ProjectID: target, PreviousProjectID: projectID}, nil
}
