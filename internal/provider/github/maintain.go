package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// Workflow maintenance and Actions administration of a repository. These tools are high-risk:
// they change what runs in the repository and with which rights, so each of them requires an allow-list
// and is offered only by a connection whose tools list names it. No profile a new connection starts with
// selects them.
//
// The workflow maintainer reads, creates, and updates workflow files, and only files directly below
// .github/workflows/ ending in .yml or .yaml: the path is checked before a credential is resolved, and an
// update names the blob it replaces, so a file that changed in the meantime is never overwritten. It also
// enables and disables workflows. Nothing deletes a file or writes any other path. The Actions administrator
// reads and changes whether Actions run in the repository, which actions they may use, and the default
// rights of the GITHUB_TOKEN. Every change is one request that is never repeated, and its answer names
// what changed without echoing a file's content.

// Data sensitivity of the maintainer and administrator tools.
const (
	workflowFileSensitivity   = "github-workflow-file"
	actionsSettingSensitivity = "github-actions-settings"
)

// Bounds of the maintainer tools.
const (
	maxCommitMessage = 1000
	maxWorkflowName  = 100
)

// Input patterns of the maintainer tools, doubled backslashes included for the JSON schemas. A workflow file
// path names one file directly below .github/workflows/ with an ASCII name; a blob SHA is the SHA-1 or
// SHA-256 object name GitHub reports.
const (
	workflowFilePattern = `^\\.github/workflows/[A-Za-z0-9_-][A-Za-z0-9._-]{0,99}\\.ya?ml$`
	blobSHAPattern      = `^[0-9a-f]{40}([0-9a-f]{24})?$`
)

const (
	workflowFileSchema  = `{"type":"string","minLength":23,"maxLength":123,"pattern":"` + workflowFilePattern + `"}`
	blobSHASchema       = `{"type":"string","minLength":40,"maxLength":64,"pattern":"` + blobSHAPattern + `"}`
	workflowTextSchema  = `{"type":"string","minLength":1,"maxLength":524288}`
	commitMessageSchema = `{"type":"string","minLength":1,"maxLength":1000}`
)

// guardedRisk is the contract of a maintainer or administrator tool: a read needs no confirmation, every
// change does.
func guardedRisk(effect capability.Effect, idempotency capability.Idempotency, sensitivity string) capability.Risk {
	confirmation := capability.ConfirmationRequired
	if effect == capability.EffectRead {
		confirmation = capability.ConfirmationNone
	}
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: confirmation, OpenWorld: true,
		DataSensitivity: sensitivity}
}

const workflowFileProperties = `"name":{"type":"string"},"path":{"type":"string"},"sha":{"type":"string"},` +
	`"size":{"type":"integer"}`

var workflowFilesList = capability.Descriptor{
	ID:      Provider + ".workflowfiles.list",
	Version: 1,
	Title:   "List GitHub workflow files",
	Description: "List the workflow files directly below .github/workflows/ of " +
		"a repository an explicit connection allows with their blob SHAs, without their content; offered only where the connection lists it",
	Tags:                       []string{"github", "actions", "workflows", "files", "list", "maintainer"},
	Risk:                       guardedRisk(capability.EffectRead, capability.IdempotencySafe, workflowFileSensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema:                inputSchema(`"ref":` + refSchema),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"files":{"type":"array","items":{"type":"object",` +
		`"properties":{` + workflowFileProperties + `},"required":["name","path","sha","size"],` +
		`"additionalProperties":false}}},"required":["files"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "ref", Description: "Branch or tag to read; the default branch when omitted"},
	},
	Fields: []capability.Field{
		{Name: "files", Description: "Workflow files ending in .yml or .yaml with name, path, blob SHA, and size in bytes"},
	},
	Examples: []capability.Example{{Description: "List the workflow files", Arguments: json.RawMessage(`{}`)}},
}

var workflowFilesGet = capability.Descriptor{
	ID:      Provider + ".workflowfiles.get",
	Version: 1,
	Title:   "Get a GitHub workflow file",
	Description: "Read one workflow file directly below .github/workflows/ of " +
		"a repository an explicit connection allows with its content and blob SHA; offered only where the connection lists it",
	Tags:                       []string{"github", "actions", "workflows", "files", "get", "maintainer"},
	Risk:                       guardedRisk(capability.EffectRead, capability.IdempotencySafe, workflowFileSensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema:                inputSchema(`"path":`+workflowFileSchema+`,"ref":`+refSchema, "path"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"sha":{"type":"string"},` +
		`"size":{"type":"integer"},"content":{"type":"string"}},"required":["path","sha","size","content"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "path", Description: "Workflow file such as .github/workflows/ci.yml; no other directory", Required: true},
		{Name: "ref", Description: "Branch or tag to read; the default branch when omitted"},
	},
	Fields: []capability.Field{
		{Name: "sha", Description: "Blob SHA an update of this file names as the version it replaces"},
		{Name: "content", Description: "Content of the file as text, untrusted data"},
	},
	Examples: []capability.Example{{
		Description: "Read the CI workflow",
		Arguments:   json.RawMessage(`{"path":".github/workflows/ci.yml"}`),
	}},
}

const workflowFileChangeOutput = `{"type":"object","properties":{"path":{"type":"string"},"branch":{"type":"string"},` +
	`"previous_sha":{"type":"string"},"sha":{"type":"string"},"size":{"type":"integer"},` +
	`"commit_sha":{"type":"string"},"commit_url":{"type":"string"}},"required":["path","sha","size","commit_sha"],` +
	`"additionalProperties":false}`

var workflowFileChangeFields = []capability.Field{
	{Name: "path", Description: "Path of the written workflow file"},
	{Name: "previous_sha", Description: "Blob SHA the change replaced; absent for a created file"},
	{Name: "sha", Description: "Blob SHA of the file after the change"},
	{Name: "size", Description: "Size of the file after the change in bytes; the content is never echoed"},
	{Name: "commit_sha", Description: "Commit GitHub created for the change"},
}

var workflowFileChangeArguments = []capability.Argument{
	{Name: "path", Description: "Workflow file such as .github/workflows/ci.yml; no other directory", Required: true},
	{Name: "content", Description: "Complete new content of the file as text, at most 512 KiB", Required: true},
	{Name: "message", Description: "Commit message, at most 1000 characters", Required: true},
	{Name: "branch", Description: "Branch to commit to; the default branch when omitted"},
}

var workflowFilesCreate = capability.Descriptor{
	ID:      Provider + ".workflowfiles.create",
	Version: 1,
	Title:   "Create a GitHub workflow file",
	Description: "Commit one new workflow file directly below .github/workflows/ of " +
		"a repository an explicit connection allows; fails when the file exists, so a repeated call writes nothing; offered only where " +
		"the connection lists it",
	Tags:                       []string{"github", "actions", "workflows", "files", "create", "maintainer"},
	Risk:                       guardedRisk(capability.EffectCreate, capability.IdempotencyIdempotent, workflowFileSensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema: inputSchema(`"path":`+workflowFileSchema+`,"content":`+workflowTextSchema+`,"message":`+
		commitMessageSchema+`,"branch":`+refSchema, "path", "content", "message"),
	OutputSchema: json.RawMessage(workflowFileChangeOutput),
	Arguments:    workflowFileChangeArguments,
	Fields:       workflowFileChangeFields,
	Examples: []capability.Example{{
		Description: "Add a lint workflow",
		Arguments: json.RawMessage(`{"path":".github/workflows/lint.yml","content":"name: Lint\non: [push]\n` +
			`jobs: {}\n","message":"ci: add lint workflow"}`),
	}},
}

var workflowFilesUpdate = capability.Descriptor{
	ID:      Provider + ".workflowfiles.update",
	Version: 1,
	Title:   "Update a GitHub workflow file",
	Description: "Replace the content of one workflow file directly below .github/workflows/ of " +
		"a repository an explicit connection allows, only while its blob SHA is still the given one, so a changed file is never " +
		"overwritten and a repeated call writes nothing; offered only where the connection lists it",
	Tags:                       []string{"github", "actions", "workflows", "files", "update", "maintainer"},
	Risk:                       guardedRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, workflowFileSensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema: inputSchema(`"path":`+workflowFileSchema+`,"sha":`+blobSHASchema+`,"content":`+workflowTextSchema+
		`,"message":`+commitMessageSchema+`,"branch":`+refSchema, "path", "sha", "content", "message"),
	OutputSchema: json.RawMessage(workflowFileChangeOutput),
	Arguments: append([]capability.Argument{workflowFileChangeArguments[0],
		{Name: "sha", Description: "Blob SHA of the version to replace, as github.workflowfiles.get reports it", Required: true},
	}, workflowFileChangeArguments[1:]...),
	Fields: workflowFileChangeFields,
	Examples: []capability.Example{{
		Description: "Update the CI workflow read before",
		Arguments: json.RawMessage(`{"path":".github/workflows/ci.yml","sha":"3d21ec53a331a6f037a91c368710b99387d012c1",` +
			`"content":"name: CI\non: [push, pull_request]\njobs: {}\n","message":"ci: also run on pull requests"}`),
	}},
}

const workflowStateOutput = `{"type":"object","properties":{"workflow_id":{"type":"integer"},"path":{"type":"string"},` +
	`"previous_state":{"type":"string"},"state":{"type":"string"}},` +
	`"required":["workflow_id","path","previous_state","state"],"additionalProperties":false}`

var workflowStateFields = []capability.Field{
	{Name: "previous_state", Description: "State of the workflow before the change"},
	{Name: "state", Description: "State GitHub accepted: active or disabled_manually"},
}

var workflowsEnable = capability.Descriptor{
	ID:      Provider + ".workflows.enable",
	Version: 1,
	Title:   "Enable a GitHub Actions workflow",
	Description: "Enable one workflow with a file below .github/workflows/ of " +
		"a repository an explicit connection allows, so GitHub runs it again; offered only where the connection lists it",
	Tags:                       []string{"github", "actions", "workflows", "enable", "maintainer"},
	Risk:                       guardedRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, workflowFileSensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema:                inputSchema(`"workflow":`+workflowSchema, "workflow"),
	OutputSchema:               json.RawMessage(workflowStateOutput),
	Arguments: []capability.Argument{
		{Name: "workflow", Description: "Workflow identifier or file name such as ci.yml", Required: true},
	},
	Fields:   workflowStateFields,
	Examples: []capability.Example{{Description: "Enable a workflow", Arguments: json.RawMessage(`{"workflow":"ci.yml"}`)}},
}

var workflowsDisable = capability.Descriptor{
	ID:      Provider + ".workflows.disable",
	Version: 1,
	Title:   "Disable a GitHub Actions workflow",
	Description: "Disable one workflow with a file below .github/workflows/ of " +
		"a repository an explicit connection allows, so GitHub no longer runs it; offered only where the connection lists it",
	Tags:                       []string{"github", "actions", "workflows", "disable", "maintainer"},
	Risk:                       guardedRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, workflowFileSensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema:                inputSchema(`"workflow":`+workflowSchema, "workflow"),
	OutputSchema:               json.RawMessage(workflowStateOutput),
	Arguments: []capability.Argument{
		{Name: "workflow", Description: "Workflow identifier or file name such as ci.yml", Required: true},
	},
	Fields:   workflowStateFields,
	Examples: []capability.Example{{Description: "Disable a workflow", Arguments: json.RawMessage(`{"workflow":"old.yml"}`)}},
}

// The values the Actions settings accept.
var (
	allowedActionsValues     = []string{"all", "local_only", "selected"}
	workflowPermissionValues = []string{"read", "write"}
)

const actionsPermissionsProperties = `"enabled":{"type":"boolean"},"allowed_actions":{"type":"string"}`

const workflowPermissionsProperties = `"default_workflow_permissions":{"type":"string"},` +
	`"can_approve_pull_request_reviews":{"type":"boolean"}`

func settingsOutput(properties, required string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` + properties + `},"required":[` + required + `],` +
		`"additionalProperties":false}`)
}

func settingsChangeOutput(properties, required string) json.RawMessage {
	setting := string(settingsOutput(properties, required))
	return json.RawMessage(`{"type":"object","properties":{"before":` + setting + `,"after":` + setting + `},` +
		`"required":["before","after"],"additionalProperties":false}`)
}

var settingsChangeFields = []capability.Field{
	{Name: "before", Description: "Settings GitHub reported before the change"},
	{Name: "after", Description: "Settings GitHub accepted with the change"},
}

var actionsPermissionsGet = capability.Descriptor{
	ID:      Provider + ".actionspermissions.get",
	Version: 1,
	Title:   "Get the GitHub Actions permissions of a repository",
	Description: "Read whether GitHub Actions run in a repository an explicit connection allows and which " +
		"actions they may use; offered only where the connection lists it",
	Tags:                       []string{"github", "actions", "settings", "permissions", "get", "admin"},
	Risk:                       guardedRisk(capability.EffectRead, capability.IdempotencySafe, actionsSettingSensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema:                inputSchema(""),
	OutputSchema:               settingsOutput(actionsPermissionsProperties, `"enabled"`),
	Fields: []capability.Field{
		{Name: "enabled", Description: "True when GitHub Actions run in the repository"},
		{Name: "allowed_actions", Description: "all, local_only, or selected; absent while Actions are disabled"},
	},
	Examples: []capability.Example{{Description: "Read the Actions permissions", Arguments: json.RawMessage(`{}`)}},
}

var actionsPermissionsUpdate = capability.Descriptor{
	ID:      Provider + ".actionspermissions.update",
	Version: 1,
	Title:   "Change the GitHub Actions permissions of a repository",
	Description: "Switch GitHub Actions on or off in a repository an explicit connection allows or choose which " +
		"actions they may use; a value left out stays as it is; offered only where the connection lists it",
	Tags:                       []string{"github", "actions", "settings", "permissions", "update", "admin"},
	Risk:                       guardedRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, actionsSettingSensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema: inputSchema(`"enabled":{"type":"boolean"},"allowed_actions":{"type":"string","enum":["` +
		strings.Join(allowedActionsValues, `","`) + `"]}`),
	OutputSchema: settingsChangeOutput(actionsPermissionsProperties, `"enabled"`),
	Arguments: []capability.Argument{
		{Name: "enabled", Description: "true to run GitHub Actions in the repository, false to switch them off"},
		{Name: "allowed_actions", Description: "all, local_only, or selected; selected keeps the selection maintained " +
			"in the repository settings"},
	},
	Fields: settingsChangeFields,
	Examples: []capability.Example{{
		Description: "Allow only actions of the repository's own owner",
		Arguments:   json.RawMessage(`{"allowed_actions":"local_only"}`),
	}},
}

var workflowPermissionsGet = capability.Descriptor{
	ID:      Provider + ".workflowpermissions.get",
	Version: 1,
	Title:   "Get the default GITHUB_TOKEN permissions of a repository",
	Description: "Read the default rights of the GITHUB_TOKEN of workflows in " +
		"a repository an explicit connection allows and whether workflows may approve pull request reviews; offered only where the connection lists it",
	Tags:                       []string{"github", "actions", "settings", "token", "get", "admin"},
	Risk:                       guardedRisk(capability.EffectRead, capability.IdempotencySafe, actionsSettingSensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema:                inputSchema(""),
	OutputSchema: settingsOutput(workflowPermissionsProperties,
		`"default_workflow_permissions","can_approve_pull_request_reviews"`),
	Fields: []capability.Field{
		{Name: "default_workflow_permissions", Description: "read or write"},
		{Name: "can_approve_pull_request_reviews", Description: "True when workflows may approve pull request reviews"},
	},
	Examples: []capability.Example{{Description: "Read the token defaults", Arguments: json.RawMessage(`{}`)}},
}

var workflowPermissionsUpdate = capability.Descriptor{
	ID:      Provider + ".workflowpermissions.update",
	Version: 1,
	Title:   "Change the default GITHUB_TOKEN permissions of a repository",
	Description: "Set the default rights of the GITHUB_TOKEN of workflows in " +
		"a repository an explicit connection allows or whether workflows may approve pull request reviews; a value left out stays as it is; offered " +
		"only where the connection lists it",
	Tags:                       []string{"github", "actions", "settings", "token", "update", "admin"},
	Risk:                       guardedRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, actionsSettingSensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema: inputSchema(`"default_workflow_permissions":{"type":"string","enum":["read","write"]},` +
		`"can_approve_pull_request_reviews":{"type":"boolean"}`),
	OutputSchema: settingsChangeOutput(workflowPermissionsProperties,
		`"default_workflow_permissions","can_approve_pull_request_reviews"`),
	Arguments: []capability.Argument{
		{Name: "default_workflow_permissions", Description: "read or write"},
		{Name: "can_approve_pull_request_reviews", Description: "true to let workflows approve pull request reviews"},
	},
	Fields: settingsChangeFields,
	Examples: []capability.Example{{
		Description: "Restrict the token to reads",
		Arguments:   json.RawMessage(`{"default_workflow_permissions":"read","can_approve_pull_request_reviews":false}`),
	}},
}

// maintainerTools and adminTools are the two high-risk tool groups and their profiles.
var (
	maintainerTools = []string{workflowFilesList.ID, workflowFilesGet.ID, workflowFilesCreate.ID,
		workflowFilesUpdate.ID, workflowsEnable.ID, workflowsDisable.ID}
	adminTools = []string{actionsPermissionsGet.ID, actionsPermissionsUpdate.ID, workflowPermissionsGet.ID,
		workflowPermissionsUpdate.ID}
)

// maintenanceOperations binds every maintainer and administrator tool to its handler. They share the handler
// of the Actions tools: arguments and scope are checked before a credential is resolved.
func maintenanceOperations() []capability.Operation {
	bind := func(descriptor capability.Descriptor, check func(*actionsArguments, target) error,
		call func(context.Context, *Client, *actionsArguments) (any, error)) capability.Operation {
		return capability.Operation{Descriptor: descriptor, Handler: actionsHandler(descriptor.ID, check, call)}
	}
	none := func(*actionsArguments, target) error { return nil }
	return []capability.Operation{
		bind(workflowFilesList, checkReadRef, func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			return c.listWorkflowFiles(ctx, a.Ref)
		}),
		bind(workflowFilesGet, checkFileRead, func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			return c.workflowFile(ctx, a.Path, a.Ref)
		}),
		bind(workflowFilesCreate, checkFileChange(false), func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			return c.writeWorkflowFile(ctx, a)
		}),
		bind(workflowFilesUpdate, checkFileChange(true), func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			return c.writeWorkflowFile(ctx, a)
		}),
		bind(workflowsEnable, checkWorkflowArgument, func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			return c.setWorkflowState(ctx, a.Workflow, true)
		}),
		bind(workflowsDisable, checkWorkflowArgument, func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			return c.setWorkflowState(ctx, a.Workflow, false)
		}),
		bind(actionsPermissionsGet, none, func(ctx context.Context, c *Client, _ *actionsArguments) (any, error) {
			return c.actionsPermissions(ctx, "get actions permissions")
		}),
		bind(actionsPermissionsUpdate, checkActionsPermissions, func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			return c.updateActionsPermissions(ctx, a)
		}),
		bind(workflowPermissionsGet, none, func(ctx context.Context, c *Client, _ *actionsArguments) (any, error) {
			return c.workflowPermissions(ctx, "get workflow permissions")
		}),
		bind(workflowPermissionsUpdate, checkWorkflowPermissions, func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			return c.updateWorkflowPermissions(ctx, a)
		}),
	}
}

// validWorkflowFile mirrors workflowFilePattern and refuses a doubled dot as well: the path names one file
// directly below .github/workflows/, in plain ASCII, so no separator, escape, or lookalike character can
// lead it anywhere else.
func validWorkflowFile(path string) bool {
	name, ok := strings.CutPrefix(path, workflowsDir)
	if !ok || strings.Contains(name, "..") {
		return false
	}
	stem := strings.TrimSuffix(strings.TrimSuffix(name, ".yml"), ".yaml")
	if stem == name || stem == "" || len(stem) > maxWorkflowName || stem[0] == '.' {
		return false
	}
	for _, r := range stem {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' ||
			r == '-') {
			return false
		}
	}
	return true
}

func validBlobSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	return strings.Trim(value, "0123456789abcdef") == ""
}

// checkText refuses what is not plain text: invalid UTF-8 and control characters other than tab, line
// feed, and carriage return.
func checkText(name, value string) error {
	if !utf8.ValidString(value) {
		return invalidRequest(name + " must be valid UTF-8 text")
	}
	for _, r := range value {
		if (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || r == 0x7f {
			return invalidRequest(name + " must be text without control characters")
		}
	}
	return nil
}

func checkReadRef(a *actionsArguments, _ target) error {
	if a.Ref != "" && !validRef(a.Ref) {
		return invalidRequest("ref must be a branch or tag name")
	}
	return nil
}

func checkFileRead(a *actionsArguments, bound target) error {
	if !validWorkflowFile(a.Path) {
		return invalidRequest("path must name a .yml or .yaml file directly below .github/workflows/")
	}
	return checkReadRef(a, bound)
}

// checkFileChange applies every bound of a file write before a credential is resolved: the path, the blob
// an update replaces, the content, the commit message, and the branch.
func checkFileChange(update bool) func(*actionsArguments, target) error {
	return func(a *actionsArguments, _ target) error {
		switch {
		case !validWorkflowFile(a.Path):
			return invalidRequest("path must name a .yml or .yaml file directly below .github/workflows/")
		case update && !validBlobSHA(a.SHA):
			return invalidRequest("sha must be the blob SHA of the version to replace, as github.workflowfiles.get " +
				"reports it")
		case !update && a.SHA != "":
			return invalidRequest("a new workflow file has no sha; change an existing one with github.workflowfiles.update")
		case a.Content == "" || len(a.Content) > maxWorkflowFile:
			return invalidRequest(fmt.Sprintf("content must hold between 1 byte and %d KiB", maxWorkflowFile>>10))
		case strings.TrimSpace(a.Message) == "" || utf8.RuneCountInString(a.Message) > maxCommitMessage:
			return invalidRequest(fmt.Sprintf("message must hold between 1 and %d characters", maxCommitMessage))
		case a.Branch != "" && !validRef(a.Branch):
			return invalidRequest("branch must be a branch name")
		}
		if err := checkText("content", a.Content); err != nil {
			return err
		}
		return checkText("message", a.Message)
	}
}

func checkActionsPermissions(a *actionsArguments, _ target) error {
	switch {
	case a.Enabled == nil && a.AllowedActions == "":
		return invalidRequest("give enabled, allowed_actions, or both")
	case a.AllowedActions != "" && !contains(allowedActionsValues, a.AllowedActions):
		return invalidRequest("allowed_actions must be one of " + strings.Join(allowedActionsValues, ", "))
	case a.AllowedActions != "" && a.Enabled != nil && !*a.Enabled:
		return invalidRequest("allowed_actions applies only while Actions are enabled")
	}
	return nil
}

func checkWorkflowPermissions(a *actionsArguments, _ target) error {
	switch {
	case a.DefaultWorkflowPermissions == "" && a.CanApprove == nil:
		return invalidRequest("give default_workflow_permissions, can_approve_pull_request_reviews, or both")
	case a.DefaultWorkflowPermissions != "" && !contains(workflowPermissionValues, a.DefaultWorkflowPermissions):
		return invalidRequest("default_workflow_permissions must be read or write")
	}
	return nil
}

// Permission messages of the maintainer and administrator tools. GitHub decides on every request; a message
// names what such a request needs without claiming what the configured token holds.
const (
	workflowFileReadPermission = "GitHub refused this token the workflow files of this repository; reading them " +
		"needs repo on a classic token for a private repository, or Contents: read on a fine-grained token"
	workflowFileChangePermission = "GitHub refused this change of a workflow file; it needs repo and workflow on a " +
		"classic token, or Contents: read and write and Workflows: read and write on a fine-grained token, and a " +
		"branch protection or repository rule may forbid it as well"
	settingsReadPermission = "GitHub refused this token the Actions settings of this repository; reading them " +
		"needs repo on a classic token of a repository administrator, or Administration: read on a fine-grained token"
	settingsChangePermission = "GitHub refused this change of the Actions settings of this repository; it needs " +
		"repo on a classic token of a repository administrator, or Administration: read and write on a fine-grained " +
		"token, and an organization or enterprise policy may forbid it as well"
)

// contentsPath is the Contents API route of one path of the bound repository, segment by segment escaped.
func (c *Client) contentsPath(path string, query url.Values) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	route := c.repoPath("contents/" + strings.Join(segments, "/"))
	if len(query) > 0 {
		route += "?" + query.Encode()
	}
	return route
}

func refQuery(ref string) url.Values {
	if ref == "" {
		return nil
	}
	return url.Values{"ref": {ref}}
}

// WorkflowFiles lists the workflow files of the bound repository.
type WorkflowFiles struct {
	Files []WorkflowFileEntry `json:"files"`
}

// WorkflowFileEntry is one workflow file without its content.
type WorkflowFileEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	SHA  string `json:"sha"`
	Size int    `json:"size"`
}

// listWorkflowFiles reads the directory .github/workflows/ and keeps the files a maintainer tool may name;
// subdirectories and other files are left out.
func (c *Client) listWorkflowFiles(ctx context.Context, ref string) (*WorkflowFiles, error) {
	const op = "list workflow files"
	var entries []struct {
		Type string `json:"type"`
		Name string `json:"name"`
		Path string `json:"path"`
		SHA  string `json:"sha"`
		Size int    `json:"size"`
	}
	if err := c.rest(ctx, op, c.contentsPath(strings.TrimSuffix(workflowsDir, "/"), refQuery(ref)), &entries); err != nil {
		return nil, actionsFailure(err, workflowFileReadPermission)
	}
	result := &WorkflowFiles{Files: []WorkflowFileEntry{}}
	for _, entry := range entries {
		if entry.Type != "file" || !validWorkflowFile(entry.Path) {
			continue
		}
		if !validBlobSHA(entry.SHA) {
			return nil, invalidEntry(op, "a workflow file")
		}
		result.Files = append(result.Files, WorkflowFileEntry{Name: entry.Name, Path: entry.Path, SHA: entry.SHA,
			Size: entry.Size})
	}
	return result, nil
}

// WorkflowFile is one workflow file with its content.
type WorkflowFile struct {
	Path    string `json:"path"`
	SHA     string `json:"sha"`
	Size    int    `json:"size"`
	Content string `json:"content"`
}

// workflowFile reads one workflow file. Anything but a text file of at most maxWorkflowFile bytes at exactly
// the requested path is refused.
func (c *Client) workflowFile(ctx context.Context, path, ref string) (*WorkflowFile, error) {
	const op = "get workflow file"
	var file struct {
		Type     string `json:"type"`
		Path     string `json:"path"`
		SHA      string `json:"sha"`
		Size     int    `json:"size"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := c.rest(ctx, op, c.contentsPath(path, refQuery(ref)), &file); err != nil {
		return nil, actionsFailure(err, workflowFileReadPermission)
	}
	if file.Type != "file" || file.Path != path || file.Size > maxWorkflowFile {
		return nil, providerError(op, fmt.Sprintf("the path is no workflow file of at most %d KiB", maxWorkflowFile>>10))
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if file.Encoding != "base64" || err != nil || len(data) != file.Size || !validBlobSHA(file.SHA) {
		return nil, invalidResponse(op, false)
	}
	if checkText("content", string(data)) != nil {
		return nil, providerError(op, "the workflow file is not text")
	}
	return &WorkflowFile{Path: file.Path, SHA: file.SHA, Size: file.Size, Content: string(data)}, nil
}

// WorkflowFileChange describes a written workflow file without its content.
type WorkflowFileChange struct {
	Path        string `json:"path"`
	Branch      string `json:"branch,omitempty"`
	PreviousSHA string `json:"previous_sha,omitempty"`
	SHA         string `json:"sha"`
	Size        int    `json:"size"`
	CommitSHA   string `json:"commit_sha"`
	CommitURL   string `json:"commit_url,omitempty"`
}

// writeWorkflowFile creates or, with the blob SHA it replaces, updates one workflow file in one request that
// is never repeated. GitHub refuses a create of an existing file and an update whose blob SHA is no longer
// the file's, so neither can overwrite a version the caller has not seen.
func (c *Client) writeWorkflowFile(ctx context.Context, a *actionsArguments) (*WorkflowFileChange, error) {
	op := "create workflow file"
	body := map[string]any{"message": a.Message, "content": base64.StdEncoding.EncodeToString([]byte(a.Content))}
	if a.SHA != "" {
		op = "update workflow file"
		body["sha"] = a.SHA
	}
	if a.Branch != "" {
		body["branch"] = a.Branch
	}
	var answer struct {
		Content struct {
			Path string `json:"path"`
			SHA  string `json:"sha"`
			Size int    `json:"size"`
		} `json:"content"`
		Commit struct {
			SHA     string `json:"sha"`
			HTMLURL string `json:"html_url"`
		} `json:"commit"`
	}
	if err := c.restChange(ctx, op, http.MethodPut, c.contentsPath(a.Path, nil), body, &answer); err != nil {
		return nil, fileWriteFailure(err, c.target, a.SHA != "")
	}
	if answer.Content.Path != a.Path || answer.Content.SHA == "" || answer.Commit.SHA == "" {
		return nil, invalidResponse(op, true)
	}
	return &WorkflowFileChange{Path: answer.Content.Path, Branch: a.Branch, PreviousSHA: a.SHA,
		SHA: answer.Content.SHA, Size: answer.Content.Size, CommitSHA: answer.Commit.SHA,
		CommitURL: answer.Commit.HTMLURL}, nil
}

// fileWriteFailure names what a refused file write most likely means. GitHub answers a stale blob SHA with a
// conflict and a create of an existing file, or an update with an unknown blob, as an invalid request.
func fileWriteFailure(err error, repository target, update bool) error {
	var failure *provider.Error
	if !errors.As(err, &failure) {
		return err
	}
	refused := *failure
	switch {
	case failure.Class == provider.ClassPermission:
		refused.Message = workflowFileChangePermission
	case failure.Message == conflictMessage && update:
		refused.Message = "the workflow file no longer has the given blob SHA, so nothing was written; read it " +
			"again with github.workflowfiles.get and apply the change to its current content"
	case failure.Message == conflictMessage:
		refused.Message = "GitHub refused to create the workflow file in the current state of the branch, so " +
			"nothing was written"
	case failure.Message == rejectedMessage && update:
		refused.Message = "GitHub rejected the update, so nothing was written; the blob SHA may not be the " +
			"file's current one on this branch, read it again with github.workflowfiles.get"
	case failure.Message == rejectedMessage:
		refused.Message = "GitHub rejected the new file, so nothing was written; a file may already exist at " +
			"this path, read it with github.workflowfiles.get and change it with github.workflowfiles.update"
	case failure.Class == provider.ClassNotFound:
		refused.Message = "GitHub does not hold " + subject{in: repository}.String() + " or this branch, or does " +
			"not show them to this token; a workflow file change also needs workflow on a classic token or " +
			"Workflows: read and write on a fine-grained one"
	default:
		return err
	}
	return &refused
}

// WorkflowState is the answer to an enabled or disabled workflow.
type WorkflowState struct {
	WorkflowID    int64  `json:"workflow_id"`
	Path          string `json:"path"`
	PreviousState string `json:"previous_state"`
	State         string `json:"state"`
}

// setWorkflowState enables or disables one workflow. The workflow is read first, so a workflow without a
// file below .github/workflows/ is refused and the answer names the state before the change; the change is
// one request that is never repeated.
func (c *Client) setWorkflowState(ctx context.Context, workflow string, enable bool) (*WorkflowState, error) {
	op, action, state := "disable workflow", "disable", "disabled_manually"
	if enable {
		op, action, state = "enable workflow", "enable", "active"
	}
	flow, err := c.workflow(ctx, op, workflow)
	if err != nil {
		return nil, err
	}
	if !validWorkflowFile(flow.Path) {
		return nil, providerError(op, "the workflow has no workflow file below .github/workflows/")
	}
	if err := c.restChange(ctx, op, http.MethodPut,
		c.actionsPath("workflows/"+strconv.FormatInt(flow.ID, 10)+"/"+action), struct{}{}, nil); err != nil {
		return nil, actionsFailure(err, actionsChangePermission)
	}
	return &WorkflowState{WorkflowID: flow.ID, Path: flow.Path, PreviousState: flow.State, State: state}, nil
}

// ActionsPermissions are the Actions permissions of the bound repository.
type ActionsPermissions struct {
	Enabled        bool   `json:"enabled"`
	AllowedActions string `json:"allowed_actions,omitempty"`
}

// WorkflowPermissions are the default GITHUB_TOKEN rights of the bound repository.
type WorkflowPermissions struct {
	DefaultWorkflowPermissions string `json:"default_workflow_permissions"`
	CanApprove                 bool   `json:"can_approve_pull_request_reviews"`
}

// SettingsChange names a settings change by the values before it and those GitHub accepted.
type SettingsChange[T any] struct {
	Before T `json:"before"`
	After  T `json:"after"`
}

func (c *Client) settingsRead(ctx context.Context, op, path string, out any) error {
	return actionsFailure(c.rest(ctx, op, c.actionsPath(path), out), settingsReadPermission)
}

// settingsChange sends one settings change, never repeated. GitHub refuses a setting an organization or
// enterprise fixes with a conflict.
func (c *Client) settingsChange(ctx context.Context, op, path string, body any) error {
	err := c.restChange(ctx, op, http.MethodPut, c.actionsPath(path), body, nil)
	var failure *provider.Error
	if errors.As(err, &failure) && failure.Message == conflictMessage {
		refused := *failure
		refused.Message = "GitHub refused the change in the current state of the repository; an organization or " +
			"enterprise policy may fix this setting, so nothing was changed"
		return &refused
	}
	return actionsFailure(err, settingsChangePermission)
}

func (c *Client) actionsPermissions(ctx context.Context, op string) (*ActionsPermissions, error) {
	var raw struct {
		Enabled        *bool  `json:"enabled"`
		AllowedActions string `json:"allowed_actions"`
	}
	if err := c.settingsRead(ctx, op, "permissions", &raw); err != nil {
		return nil, err
	}
	if raw.Enabled == nil {
		return nil, invalidResponse(op, false)
	}
	return &ActionsPermissions{Enabled: *raw.Enabled, AllowedActions: raw.AllowedActions}, nil
}

// updateActionsPermissions reads the current permissions, because GitHub replaces them as a whole, and sends
// them with the requested values in one change.
func (c *Client) updateActionsPermissions(ctx context.Context, a *actionsArguments) (*SettingsChange[ActionsPermissions], error) {
	const op = "update actions permissions"
	before, err := c.actionsPermissions(ctx, op)
	if err != nil {
		return nil, err
	}
	after := *before
	if a.Enabled != nil {
		after.Enabled = *a.Enabled
	}
	if a.AllowedActions != "" && !after.Enabled {
		return nil, invalidRequest("allowed_actions applies only while Actions are enabled; give enabled true as well")
	}
	if a.AllowedActions != "" {
		after.AllowedActions = a.AllowedActions
	}
	body := map[string]any{"enabled": after.Enabled}
	if !after.Enabled {
		after.AllowedActions = ""
	} else if after.AllowedActions != "" {
		body["allowed_actions"] = after.AllowedActions
	}
	if err := c.settingsChange(ctx, op, "permissions", body); err != nil {
		return nil, err
	}
	return &SettingsChange[ActionsPermissions]{Before: *before, After: after}, nil
}

func (c *Client) workflowPermissions(ctx context.Context, op string) (*WorkflowPermissions, error) {
	var raw struct {
		Default    string `json:"default_workflow_permissions"`
		CanApprove *bool  `json:"can_approve_pull_request_reviews"`
	}
	if err := c.settingsRead(ctx, op, "permissions/workflow", &raw); err != nil {
		return nil, err
	}
	if !contains(workflowPermissionValues, raw.Default) || raw.CanApprove == nil {
		return nil, invalidResponse(op, false)
	}
	return &WorkflowPermissions{DefaultWorkflowPermissions: raw.Default, CanApprove: *raw.CanApprove}, nil
}

// updateWorkflowPermissions reads the current token defaults and sends them with the requested values in one
// change.
func (c *Client) updateWorkflowPermissions(ctx context.Context, a *actionsArguments) (*SettingsChange[WorkflowPermissions], error) {
	const op = "update workflow permissions"
	before, err := c.workflowPermissions(ctx, op)
	if err != nil {
		return nil, err
	}
	after := *before
	if a.DefaultWorkflowPermissions != "" {
		after.DefaultWorkflowPermissions = a.DefaultWorkflowPermissions
	}
	if a.CanApprove != nil {
		after.CanApprove = *a.CanApprove
	}
	if err := c.settingsChange(ctx, op, "permissions/workflow", map[string]any{
		"default_workflow_permissions": after.DefaultWorkflowPermissions, "can_approve_pull_request_reviews": after.CanApprove,
	}); err != nil {
		return nil, err
	}
	return &SettingsChange[WorkflowPermissions]{Before: *before, After: after}, nil
}
