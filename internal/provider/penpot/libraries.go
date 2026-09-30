package penpot

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const maxLibrariesListed = 200

var librariesList = capability.Descriptor{
	ID: Provider + ".libraries.list", Version: 1, Title: "List the libraries of a Penpot file",
	Description: "List the shared libraries a file uses, directly or through another library. Only libraries inside " +
		"this connection's targets are described; other libraries are only counted. Names are untrusted provider data",
	Tags: []string{"penpot", "libraries", "list", "design"}, Risk: metadataRisk, Provider: Provider,
	InputSchema: schemaOf(`"project_id":`+uuidSchema+`,"file_id":`+uuidSchema, `"project_id","file_id"`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"libraries":{"type":"array","items":` +
		`{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"project_id":{"type":"string"},"is_shared":{"type":"boolean"},"is_indirect":{"type":"boolean"}},` +
		`"required":["id","name","project_id"],"additionalProperties":false}},` +
		`"count":{"type":"integer"},"outside_targets":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["libraries","count","outside_targets","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{projectIDArgument, fileIDArgument},
	Fields: []capability.Field{
		{Name: "libraries", Description: "Libraries inside the targets with id, name, project_id, is_shared, and is_indirect (used through another library)"},
		{Name: "count", Description: "Number of libraries in this answer"},
		{Name: "outside_targets", Description: "Number of libraries outside this connection's targets; they are not described"},
		{Name: "truncated", Description: "True when the answer was cut at 200 libraries"},
	},
	Examples: []capability.Example{{Description: "List the libraries of a file",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003"}`)}},
}

var librariesShare = capability.Descriptor{
	ID: Provider + ".libraries.share", Version: 1, Title: "Share or unshare a Penpot file as a library",
	Description: "Set whether one file is a shared library. Sharing makes the file's components, colors, and " +
		"typographies visible as a library to the whole team that owns it; unsharing removes the links of other files " +
		"and copies the library's assets into them. " + manageNote,
	Tags: []string{"penpot", "libraries", "share", "design"}, Provider: Provider,
	Risk: manageRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: schemaOf(`"project_id":`+uuidSchema+`,"file_id":`+uuidSchema+`,"shared":{"type":"boolean"}`,
		`"project_id","file_id","shared"`),
	OutputSchema: schemaOf(`"shared":{"type":"boolean"},"file_id":{"type":"string"},"project_id":{"type":"string"}`,
		`"shared","file_id","project_id"`),
	Arguments: []capability.Argument{projectIDArgument, fileIDArgument,
		{Name: "shared", Required: true, Description: "True to share the file as a library for the whole team, false to stop sharing it"}},
	Fields: []capability.Field{
		{Name: "shared", Description: "The state that was requested and accepted by Penpot"},
		{Name: "file_id", Description: "Identifier of the file"},
		{Name: "project_id", Description: "Identifier of the project"},
	},
	Examples: []capability.Example{{Description: "Share a file as a library",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003","shared":true}`)}},
}

var librariesLink = capability.Descriptor{
	ID: Provider + ".libraries.link", Version: 1, Title: "Link a Penpot file to a library",
	Description: "Link one file to a shared library file; both files are bound through their projects, which must be " +
		"inside this connection's targets. Penpot refuses links across teams, to the file itself, and circular links. " +
		manageNote,
	Tags: []string{"penpot", "libraries", "link", "design"}, Provider: Provider,
	Risk: manageRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	InputSchema: schemaOf(`"project_id":`+uuidSchema+`,"file_id":`+uuidSchema+`,"library_project_id":`+uuidSchema+
		`,"library_id":`+uuidSchema, `"project_id","file_id","library_project_id","library_id"`),
	OutputSchema: schemaOf(`"linked":{"type":"boolean"},"file_id":{"type":"string"},"library_id":{"type":"string"}`,
		`"linked","file_id","library_id"`),
	Arguments: []capability.Argument{projectIDArgument, fileIDArgument,
		{Name: "library_project_id", Required: true, Description: "Project of the library file; must be inside this connection's targets"},
		{Name: "library_id", Required: true, Description: "Library file identifier, a file of library_project_id that differs from file_id"}},
	Fields: []capability.Field{
		{Name: "linked", Description: "True when Penpot accepted the link"},
		{Name: "file_id", Description: "Identifier of the linked file"},
		{Name: "library_id", Description: "Identifier of the library file"},
	},
	Examples: []capability.Example{{Description: "Link a file to a library",
		Arguments: json.RawMessage(`{"project_id":"00000000-0000-0000-0000-000000000002","file_id":"00000000-0000-0000-0000-000000000003","library_project_id":"00000000-0000-0000-0000-000000000004","library_id":"00000000-0000-0000-0000-000000000005"}`)}},
}

// Library is one library a file uses.
type Library struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ProjectID  string `json:"project_id"`
	IsShared   bool   `json:"is_shared,omitempty"`
	IsIndirect bool   `json:"is_indirect,omitempty"`
}

// LibrariesResult is the answer of libraries.list.
type LibrariesResult struct {
	Libraries      []Library `json:"libraries"`
	Count          int       `json:"count"`
	OutsideTargets int       `json:"outside_targets"`
	Truncated      bool      `json:"truncated"`
}

// LibraryShared is the answer of libraries.share.
type LibraryShared struct {
	Shared    bool   `json:"shared"`
	FileID    string `json:"file_id"`
	ProjectID string `json:"project_id"`
}

// LibraryLinked is the answer of libraries.link.
type LibraryLinked struct {
	Linked    bool   `json:"linked"`
	FileID    string `json:"file_id"`
	LibraryID string `json:"library_id"`
}

type libraryArguments struct {
	ProjectID        string `json:"project_id"`
	FileID           string `json:"file_id"`
	LibraryProjectID string `json:"library_project_id"`
	LibraryID        string `json:"library_id"`
	Shared           *bool  `json:"shared"`
}

func readLibrary(op string, raw json.RawMessage) (libraryArguments, error) {
	var input libraryArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

func invokeLibrariesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list libraries"
	input, err := readLibrary(op, raw)
	if err != nil {
		return nil, err
	}
	client, _, fileID, err := boundFileOf(ctx, op, resolved, secrets, red,
		manageArguments{ProjectID: input.ProjectID, FileID: input.FileID})
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	if err := client.do(ctx, op, cmdFileLibraries, map[string]any{"file-id": fileID}, &answer); err != nil {
		return nil, err
	}
	entries, ok := objects(answer)
	if !ok {
		return nil, invalidResponse(op, "Penpot returned an invalid response")
	}
	result := &LibrariesResult{Libraries: []Library{}}
	for _, entry := range entries {
		id, project, team := entry.id("id"), entry.id("projectid"), entry.id("teamid")
		if id == "" || project == "" || !client.scope.allowsTeam(team) || !client.scope.allowsProject(project) {
			result.OutsideTargets++
			continue
		}
		if len(result.Libraries) >= maxLibrariesListed {
			result.Truncated = true
			continue
		}
		result.Libraries = append(result.Libraries, Library{ID: id, Name: entry.str("name"), ProjectID: project,
			IsShared: entry.boolean("isshared"), IsIndirect: entry.boolean("isindirect")})
	}
	result.Count = len(result.Libraries)
	return result, nil
}

func invokeLibrariesShare(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "share library"
	input, err := readLibrary(op, raw)
	if err != nil {
		return nil, err
	}
	if input.Shared == nil {
		return nil, invalidRequest("shared must be true or false")
	}
	client, projectID, fileID, err := boundFileOf(ctx, op, resolved, secrets, red,
		manageArguments{ProjectID: input.ProjectID, FileID: input.FileID})
	if err != nil {
		return nil, err
	}
	if _, err := client.change(ctx, op, cmdSetShared, map[string]any{"id": fileID, "is-shared": *input.Shared}); err != nil {
		return nil, err
	}
	return &LibraryShared{Shared: *input.Shared, FileID: fileID, ProjectID: projectID}, nil
}

func invokeLibrariesLink(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "link library"
	input, err := readLibrary(op, raw)
	if err != nil {
		return nil, err
	}
	libraryProject, err := selectProjectAs(resolved, "library_project_id", input.LibraryProjectID)
	if err != nil {
		return nil, err
	}
	libraryID, ok := parseUUID(input.LibraryID)
	if !ok {
		return nil, invalidRequest("library_id must be a UUID")
	}
	if fileID, ok := parseUUID(input.FileID); ok && fileID == libraryID {
		return nil, invalidRequest("library_id must differ from file_id")
	}
	client, _, fileID, err := boundFileOf(ctx, op, resolved, secrets, red,
		manageArguments{ProjectID: input.ProjectID, FileID: input.FileID})
	if err != nil {
		return nil, err
	}
	if _, err := client.fileOfAs(ctx, op, "library_project_id", "library_id", libraryProject, libraryID); err != nil {
		return nil, err
	}
	if _, err := client.change(ctx, op, cmdLinkLibrary, map[string]any{"file-id": fileID, "library-id": libraryID}); err != nil {
		return nil, err
	}
	return &LibraryLinked{Linked: true, FileID: fileID, LibraryID: libraryID}, nil
}
