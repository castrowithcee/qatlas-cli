package n8n

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Bounds of the source control tools.
const (
	// maxSourceControlFiles bounds the files of one result and the files of one push.
	maxSourceControlFiles = 200
	maxPushFiles          = 50
	// maxCommitMessageLength mirrors the Public API's own limit (1 to 1000 characters, counted in UTF-16
	// units the way n8n's JavaScript does).
	maxCommitMessageLength = 1000
	maxPolicyViolations    = 10
	maxProviderTextLength  = 512
	// maxFolderPathLength bounds the names of a folder path; n8n nests folders far less deeply.
	maxFolderPathLength = 50
)

const sourceControlDataSensitivity = "n8n-source-control"

const sourceControlInstanceNote = "Source control is instance-wide and needs the Enterprise source control " +
	"feature: refused on a connection with project or workflow targets"

// pushFileTypes are the documented file types a push may name, without the generic "file" type, which would
// let a caller name an arbitrary repository file.
var pushFileTypes = []string{"credential", "workflow", "tags", "variables", "folders", "project", "datatable"}

var autoPublishModes = []string{"none", "all", "published"}

var sourceControlStatusRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: sourceControlDataSensitivity}

// sourceControlChangeRisk covers pull and push: both are listed-only, confirmed, and never retried.
var sourceControlChangeRisk = capability.Risk{Effect: capability.EffectExecute,
	Idempotency: capability.IdempotencyNonIdempotent, Confirmation: capability.ConfirmationRequired,
	OpenWorld: true, DataSensitivity: sourceControlDataSensitivity}

const sourceControlFileSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
	`"type":{"type":"string"},"status":{"type":"string"},"location":{"type":"string"},` +
	`"conflict":{"type":"boolean"},"publishing_error":{"type":"string"},` +
	`"publishing_error_reason":{"type":"string"},"policy_violations":{"type":"array","items":{"type":"object"}},` +
	`"policy_check_errors":{"type":"integer"}},"required":["id","type"],"additionalProperties":false}`

// sourceControlStatusFileSchema is the preview's file: sourceControlFileSchema with the members only the
// preview carries. policy_violations stays a list of objects, now with subject and rule.
const sourceControlStatusFileSchema = `{"type":"object","properties":{"id":{"type":"string"},` +
	`"name":{"type":"string"},"type":{"type":"string"},"status":{"type":"string"},"location":{"type":"string"},` +
	`"conflict":{"type":"boolean"},"publishing_error":{"type":"string"},` +
	`"publishing_error_reason":{"type":"string"},"policy_violations":{"type":"array","items":{"type":"object"}},` +
	`"policy_check_errors":{"type":"integer"},"file":{"type":"string"},"updated_at":{"type":"string"},` +
	`"pushed":{"type":"boolean"},"is_local_published":{"type":"boolean"},"is_remote_archived":{"type":"boolean"},` +
	`"parent_folder_id":{"type":"string"},"folder_path":{"type":"array","items":{"type":"string"}},` +
	`"remote_folder_path":{"type":"array","items":{"type":"string"}},"owner":{"type":"object","properties":{` +
	`"type":{"type":"string"},"project_id":{"type":"string"},"project_name":{"type":"string"}},` +
	`"additionalProperties":false},"publishing_error_review_request_id":{"type":"string"},` +
	`"publishing_error_version_id":{"type":"string"},"check_errors":{"type":"array","items":{"type":"object",` +
	`"properties":{"check_id":{"type":"string"},"correlation_id":{"type":"string"}},"additionalProperties":false}}},` +
	`"required":["id","type"],"additionalProperties":false}`

const sourceControlStatusFilesFieldNote = "Affected files, at most 200: id, name (untrusted data), type, status, " +
	"location, conflict, and any publishing error or import policy finding (untrusted data). The Git path " +
	"file, folder names, project name, policy subjects, and ids are untrusted text; file is never read"

const sourceControlFilesFieldNote = "Affected files, at most 200: id, name (untrusted data), type, status, " +
	"location, conflict, and any publishing error or import policy finding (untrusted data)"

var sourceControlFields = []capability.Field{
	{Name: "outcome", Description: "pulled or pushed when n8n accepted it; conflict when n8n refused it with a " +
		"conflict and changed nothing"},
	{Name: "conflict", Description: "True when n8n refused with a conflict; the files name the conflicting entries"},
	{Name: "files", Description: sourceControlFilesFieldNote},
	{Name: "count", Description: "Number of files n8n reported"},
	{Name: "truncated", Description: "True when n8n reported more files than are listed"},
}

const sourceControlResultSchema = `{"type":"object","properties":{"outcome":{"type":"string",` +
	`"enum":["pulled","pushed","conflict"]},"conflict":{"type":"boolean"},"files":{"type":"array","items":` +
	sourceControlFileSchema + `},"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
	`"required":["outcome","conflict","files","count"],"additionalProperties":false}`

var sourceControlStatus = capability.Descriptor{
	ID: Provider + ".source-control.status", Version: 1, Title: "Preview n8n source control changes",
	Description: "Preview the pending changes between the bound n8n instance and its connected Git branch in " +
		"one direction: push (local changes not yet in Git) or pull (remote changes not yet imported). It " +
		"changes nothing. " + sourceControlInstanceNote,
	Tags: []string{"n8n", "sourcecontrol", "status", "automation"}, Risk: sourceControlStatusRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"direction":{"type":"string",` +
		`"enum":["push","pull"]}},"required":["direction"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"direction":{"type":"string"},` +
		`"files":{"type":"array","items":` + sourceControlStatusFileSchema + `},"count":{"type":"integer"},` +
		`"truncated":{"type":"boolean"}},"required":["direction","files","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "direction", Required: true,
		Description: "push or pull; there is no default"}},
	Fields: []capability.Field{
		{Name: "direction", Description: "The requested direction"},
		{Name: "files", Description: sourceControlStatusFilesFieldNote},
		{Name: "id", Description: "Object identifier"},
		{Name: "name", Description: "Object name, untrusted data"},
		{Name: "type", Description: "Object type, for example workflow or credential"},
		{Name: "status", Description: "Change status, for example new or modified"},
		{Name: "location", Description: "local or remote"},
		{Name: "conflict", Description: "True when the object conflicts"},
		{Name: "file", Description: "Path of the object's file in the instance's Git working folder, untrusted text"},
		{Name: "updated_at", Description: "Last update time, as n8n reports it"},
		{Name: "pushed", Description: "Whether n8n reports the object as pushed"},
		{Name: "is_local_published", Description: "Whether the local workflow is published"},
		{Name: "is_remote_archived", Description: "Whether the remote workflow is archived"},
		{Name: "parent_folder_id", Description: "Identifier of the parent folder"},
		{Name: "folder_path", Description: "Local folder names, outermost first, at most 50, untrusted data"},
		{Name: "remote_folder_path", Description: "Remote folder names, outermost first, at most 50, untrusted data"},
		{Name: "owner", Description: "Owning project: type, project_id, project_name (untrusted data)"},
		{Name: "publishing_error", Description: "Publishing error text, untrusted data"},
		{Name: "publishing_error_reason", Description: "Reason code of the publishing error"},
		{Name: "publishing_error_review_request_id", Description: "Review request the publishing error refers to"},
		{Name: "publishing_error_version_id", Description: "Workflow version the publishing error refers to"},
		{Name: "policy_violations", Description: "Import policy findings, at most 10, with kind, check_id, " +
			"message, subject, subject_type, scope, matched_rule_id; untrusted data"},
		{Name: "policy_check_errors", Description: "Number of import policy checks that failed to run"},
		{Name: "check_errors", Description: "Those failed checks, at most 10: check_id, correlation_id"},
		{Name: "count", Description: "Number of files n8n reported"},
		{Name: "truncated", Description: "True when n8n reported more files than are listed"},
	},
	Examples: []capability.Example{{Description: "Preview what a pull would import",
		Arguments: json.RawMessage(`{"direction":"pull"}`)}},
}

var sourceControlPull = capability.Descriptor{
	ID: Provider + ".source-control.pull", Version: 1, Title: "Pull n8n changes from Git",
	Description: "Import the connected Git branch into the bound n8n instance. This can overwrite the " +
		"instance's workflows, credentials, variables, and other objects with the repository's versions. " +
		"force: true discards local changes that would otherwise block the pull. auto_publish all publishes " +
		"every imported workflow and published publishes those that were published locally before the import; " +
		"none (the default) publishes nothing by itself. When n8n refuses with a conflict, the result has " +
		"outcome conflict and lists the conflicting files; nothing was imported and Qatlas does not retry. " +
		"The request is sent once and never repeated after an unclear result. " + sourceControlInstanceNote,
	Tags: []string{"n8n", "sourcecontrol", "pull", "automation"}, Risk: sourceControlChangeRisk,
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"force":{"type":"boolean"},` +
		`"auto_publish":{"type":"string","enum":["none","all","published"]}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(sourceControlResultSchema),
	Arguments: []capability.Argument{
		{Name: "force", Description: "true discards local changes to complete the pull; false when omitted"},
		{Name: "auto_publish", Description: "none (default), all (publish every imported workflow), or " +
			"published (publish those published locally before the import)"},
	},
	Fields: sourceControlFields,
	Examples: []capability.Example{{Description: "Pull without discarding or publishing anything",
		Arguments: json.RawMessage(`{}`)}},
}

var sourceControlPush = capability.Descriptor{
	ID: Provider + ".source-control.push", Version: 1, Title: "Push n8n changes to Git",
	Description: "Commit the named objects and push them to the connected Git branch, an external change. " +
		"files names 1 to 50 objects by id and type (credential, workflow, tags, variables, folders, " +
		"project, datatable). force: true pushes even objects with unresolved conflicts. When n8n refuses " +
		"with a conflict, the result has outcome conflict and lists the conflicting files; nothing was " +
		"pushed and Qatlas does not retry. The request is sent once and never repeated after an unclear " +
		"result. " + sourceControlInstanceNote,
	Tags: []string{"n8n", "sourcecontrol", "push", "automation"}, Risk: sourceControlChangeRisk,
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"commit_message":{"type":"string",` +
		`"minLength":1,"maxLength":` + strconv.Itoa(maxCommitMessageLength) + `},"files":{"type":"array",` +
		`"minItems":1,"maxItems":` + strconv.Itoa(maxPushFiles) + `,"items":{"type":"object","properties":{` +
		`"id":` + targetIDSchema + `,"type":{"type":"string","enum":["credential","workflow","tags",` +
		`"variables","folders","project","datatable"]}},"required":["id","type"],"additionalProperties":false}},` +
		`"force":{"type":"boolean"}},"required":["commit_message","files"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(sourceControlResultSchema),
	Arguments: []capability.Argument{
		{Name: "commit_message", Required: true, Description: "Commit message, 1 to 1000 characters; no control " +
			"characters except line feed"},
		{Name: "files", Required: true, Description: "1 to 50 objects, each {id, type}"},
		{Name: "force", Description: "true pushes even objects with unresolved conflicts; false when omitted"},
	},
	Fields: sourceControlFields,
	Examples: []capability.Example{{Description: "Push one workflow",
		Arguments: json.RawMessage(`{"commit_message":"Update workflow",` +
			`"files":[{"id":"2tUt1wbLX592XDdX","type":"workflow"}]}`)}},
}

type sourceControlFileJSON struct {
	ID                     string `json:"id"`
	Name                   string `json:"name"`
	Type                   string `json:"type"`
	Status                 string `json:"status"`
	Location               string `json:"location"`
	Conflict               bool   `json:"conflict"`
	PublishingError        string `json:"publishingError"`
	PublishingErrorDetails *struct {
		Reason                  string `json:"reason"`
		WorkflowReviewRequestID string `json:"workflowReviewRequestId"`
		VersionID               string `json:"versionId"`
	} `json:"publishingErrorDetails"`
	ContentImportPolicy *struct {
		Violations []struct {
			Kind          string `json:"kind"`
			CheckID       string `json:"checkId"`
			Message       string `json:"message"`
			Subject       string `json:"subject"`
			SubjectType   string `json:"subjectType"`
			Scope         string `json:"scope"`
			MatchedRuleID string `json:"matchedRuleId"`
		} `json:"violations"`
		CheckErrors []json.RawMessage `json:"checkErrors"`
	} `json:"contentImportPolicy"`
	// The members below are read by the status preview only.
	File             string   `json:"file"`
	UpdatedAt        string   `json:"updatedAt"`
	Pushed           *bool    `json:"pushed"`
	IsLocalPublished *bool    `json:"isLocalPublished"`
	IsRemoteArchived *bool    `json:"isRemoteArchived"`
	ParentFolderID   *string  `json:"parentFolderId"`
	FolderPath       []string `json:"folderPath"`
	RemoteFolderPath []string `json:"remoteFolderPath"`
	Owner            *struct {
		Type        string `json:"type"`
		ProjectID   string `json:"projectId"`
		ProjectName string `json:"projectName"`
	} `json:"owner"`
}

// PolicyViolation is one import policy finding; every value is untrusted data.
type PolicyViolation struct {
	Kind    string `json:"kind,omitempty"`
	CheckID string `json:"check_id"`
	Message string `json:"message"`
}

// SourceControlFile is the capped view of one affected object. Every text is untrusted data.
type SourceControlFile struct {
	ID                    string            `json:"id"`
	Name                  string            `json:"name,omitempty"`
	Type                  string            `json:"type"`
	Status                string            `json:"status,omitempty"`
	Location              string            `json:"location,omitempty"`
	Conflict              bool              `json:"conflict,omitempty"`
	PublishingError       string            `json:"publishing_error,omitempty"`
	PublishingErrorReason string            `json:"publishing_error_reason,omitempty"`
	PolicyViolations      []PolicyViolation `json:"policy_violations,omitempty"`
	PolicyCheckErrors     int               `json:"policy_check_errors,omitempty"`
}

// StatusPolicyViolation is an import policy finding of the status preview with its subject and rule.
type StatusPolicyViolation struct {
	PolicyViolation
	Subject       string `json:"subject,omitempty"`
	SubjectType   string `json:"subject_type,omitempty"`
	Scope         string `json:"scope,omitempty"`
	MatchedRuleID string `json:"matched_rule_id,omitempty"`
}

// PolicyCheckError is an import policy check that failed to run.
type PolicyCheckError struct {
	CheckID       string `json:"check_id,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

// SourceControlOwner is the project an object belongs to.
type SourceControlOwner struct {
	Type        string `json:"type,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
}

// SourceControlStatusFile is one previewed object: the capped view pull and push report, plus what only the
// preview carries. File is a path inside the instance's Git working folder, shown as text and never read.
// PolicyViolations here shadows the embedded one, so the preview's violations carry subject and rule.
type SourceControlStatusFile struct {
	SourceControlFile
	File                         string                  `json:"file,omitempty"`
	UpdatedAt                    string                  `json:"updated_at,omitempty"`
	Pushed                       *bool                   `json:"pushed,omitempty"`
	IsLocalPublished             *bool                   `json:"is_local_published,omitempty"`
	IsRemoteArchived             *bool                   `json:"is_remote_archived,omitempty"`
	ParentFolderID               string                  `json:"parent_folder_id,omitempty"`
	FolderPath                   []string                `json:"folder_path,omitempty"`
	RemoteFolderPath             []string                `json:"remote_folder_path,omitempty"`
	Owner                        *SourceControlOwner     `json:"owner,omitempty"`
	PublishingErrorReviewRequest string                  `json:"publishing_error_review_request_id,omitempty"`
	PublishingErrorVersionID     string                  `json:"publishing_error_version_id,omitempty"`
	PolicyViolations             []StatusPolicyViolation `json:"policy_violations,omitempty"`
	CheckErrors                  []PolicyCheckError      `json:"check_errors,omitempty"`
}

// SourceControlStatus is what source-control.status reports.
type SourceControlStatus struct {
	Direction string                    `json:"direction"`
	Files     []SourceControlStatusFile `json:"files"`
	Count     int                       `json:"count"`
	Truncated bool                      `json:"truncated,omitempty"`
}

// SourceControlResult is what source-control.pull and source-control.push report. Outcome conflict is n8n's
// refusal, not a failure: nothing was changed.
type SourceControlResult struct {
	Outcome   string              `json:"outcome"`
	Conflict  bool                `json:"conflict"`
	Files     []SourceControlFile `json:"files"`
	Count     int                 `json:"count"`
	Truncated bool                `json:"truncated,omitempty"`
}

// summarizeStatusFiles is summarizeSourceControlFiles for the preview, with the additional members.
func summarizeStatusFiles(files []sourceControlFileJSON) []SourceControlStatusFile {
	base, _ := summarizeSourceControlFiles(files)
	out := make([]SourceControlStatusFile, 0, len(base))
	for i, item := range base {
		f := files[i]
		item.PolicyViolations = nil
		status := SourceControlStatusFile{SourceControlFile: item, File: cappedText(f.File),
			UpdatedAt: bounded(f.UpdatedAt), Pushed: f.Pushed, IsLocalPublished: f.IsLocalPublished,
			IsRemoteArchived: f.IsRemoteArchived, FolderPath: cappedPath(f.FolderPath),
			RemoteFolderPath: cappedPath(f.RemoteFolderPath)}
		if f.ParentFolderID != nil {
			status.ParentFolderID = bounded(*f.ParentFolderID)
		}
		if o := f.Owner; o != nil && (o.Type != "" || o.ProjectID != "" || o.ProjectName != "") {
			status.Owner = &SourceControlOwner{Type: bounded(o.Type), ProjectID: bounded(o.ProjectID),
				ProjectName: cappedText(o.ProjectName)}
		}
		if d := f.PublishingErrorDetails; d != nil {
			status.PublishingErrorReviewRequest = bounded(d.WorkflowReviewRequestID)
			status.PublishingErrorVersionID = bounded(d.VersionID)
		}
		if policy := f.ContentImportPolicy; policy != nil {
			for j, v := range policy.Violations {
				if j == maxPolicyViolations {
					break
				}
				status.PolicyViolations = append(status.PolicyViolations, StatusPolicyViolation{
					PolicyViolation: PolicyViolation{Kind: bounded(v.Kind), CheckID: bounded(v.CheckID),
						Message: boundedText(v.Message)},
					Subject: cappedText(v.Subject), SubjectType: bounded(v.SubjectType), Scope: bounded(v.Scope),
					MatchedRuleID: bounded(v.MatchedRuleID)})
			}
			for j, raw := range policy.CheckErrors {
				if j == maxPolicyViolations {
					break
				}
				var e struct {
					CheckID       string `json:"checkId"`
					CorrelationID string `json:"correlationId"`
				}
				if json.Unmarshal(raw, &e) == nil && (e.CheckID != "" || e.CorrelationID != "") {
					status.CheckErrors = append(status.CheckErrors, PolicyCheckError{CheckID: bounded(e.CheckID),
						CorrelationID: bounded(e.CorrelationID)})
				}
			}
		}
		out = append(out, status)
	}
	return out
}

// cappedPath keeps a folder path to its first maxFolderPathLength names, each cleaned and capped.
func cappedPath(path []string) []string {
	var out []string
	for i, name := range path {
		if i == maxFolderPathLength {
			break
		}
		out = append(out, cappedText(name))
	}
	return out
}

func summarizeSourceControlFiles(files []sourceControlFileJSON) ([]SourceControlFile, bool) {
	out := make([]SourceControlFile, 0, min(len(files), maxSourceControlFiles))
	for i, f := range files {
		if i == maxSourceControlFiles {
			break
		}
		item := SourceControlFile{ID: bounded(f.ID), Name: bounded(f.Name), Type: bounded(f.Type),
			Status: bounded(f.Status), Location: bounded(f.Location), Conflict: f.Conflict,
			PublishingError: boundedText(f.PublishingError)}
		if f.PublishingErrorDetails != nil {
			item.PublishingErrorReason = bounded(f.PublishingErrorDetails.Reason)
		}
		if f.ContentImportPolicy != nil {
			item.PolicyCheckErrors = len(f.ContentImportPolicy.CheckErrors)
			for j, v := range f.ContentImportPolicy.Violations {
				if j == maxPolicyViolations {
					break
				}
				item.PolicyViolations = append(item.PolicyViolations, PolicyViolation{Kind: bounded(v.Kind),
					CheckID: bounded(v.CheckID), Message: boundedText(v.Message)})
			}
		}
		out = append(out, item)
	}
	return out, len(files) > maxSourceControlFiles
}

// boundedText keeps a provider text, a publishing error or a policy message, to a short prefix.
func boundedText(value string) string {
	if len(value) <= maxProviderTextLength {
		return value
	}
	cut := maxProviderTextLength
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

func sourceControlResult(outcome string, conflict bool, files []sourceControlFileJSON) *SourceControlResult {
	listed, truncated := summarizeSourceControlFiles(files)
	return &SourceControlResult{Outcome: outcome, Conflict: conflict, Files: listed, Count: len(files),
		Truncated: truncated}
}

func prepareSourceControl(op string, resolved *config.Resolved, raw json.RawMessage, input any) error {
	if err := json.Unmarshal(raw, input); err != nil {
		return providerError(op, "the validated arguments could not be read")
	}
	return requireInstanceScope(resolved, "source control")
}

func invokeSourceControlStatus(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read source control status"
	var input struct {
		Direction string `json:"direction"`
	}
	if err := prepareSourceControl(op, resolved, raw, &input); err != nil {
		return nil, err
	}
	if input.Direction != "push" && input.Direction != "pull" {
		return nil, invalidRequest("direction must be push or pull")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var page struct {
		Data []sourceControlFileJSON `json:"data"`
	}
	if err := client.get(ctx, op, "/source-control/status", url.Values{"direction": {input.Direction}}, &page,
		maxResponseBytes); err != nil {
		return nil, permissionNeutral(err)
	}
	return &SourceControlStatus{Direction: input.Direction, Files: summarizeStatusFiles(page.Data),
		Count: len(page.Data), Truncated: len(page.Data) > maxSourceControlFiles}, nil
}

func invokeSourceControlPull(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "pull source control"
	var input struct {
		Force       bool   `json:"force"`
		AutoPublish string `json:"auto_publish"`
	}
	if err := prepareSourceControl(op, resolved, raw, &input); err != nil {
		return nil, err
	}
	if input.AutoPublish == "" {
		input.AutoPublish = "none"
	}
	if !contains(autoPublishModes, input.AutoPublish) {
		return nil, invalidRequest("auto_publish must be none, all, or published")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var files, conflicts []sourceControlFileJSON
	err = client.do(ctx, op, http.MethodPost, "/source-control/pull", nil,
		map[string]any{"force": input.Force, "autoPublish": input.AutoPublish}, &files, maxResponseBytes, true,
		&conflicts)
	if errors.Is(err, errConflict) {
		return sourceControlResult("conflict", true, conflicts), nil
	}
	if err != nil {
		return nil, permissionNeutral(err)
	}
	return sourceControlResult("pulled", false, files), nil
}

type pushFile struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

func invokeSourceControlPush(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "push source control"
	var input struct {
		CommitMessage string     `json:"commit_message"`
		Files         []pushFile `json:"files"`
		Force         bool       `json:"force"`
	}
	if err := prepareSourceControl(op, resolved, raw, &input); err != nil {
		return nil, err
	}
	if err := validCommitMessage(input.CommitMessage); err != nil {
		return nil, err
	}
	if len(input.Files) == 0 || len(input.Files) > maxPushFiles {
		return nil, invalidRequest("files must name 1 to " + strconv.Itoa(maxPushFiles) + " objects")
	}
	seen := map[pushFile]bool{}
	names := make([]map[string]string, 0, len(input.Files))
	for _, f := range input.Files {
		if !validTargetID(f.ID) {
			return nil, invalidRequest("every file id must be a usable n8n identifier")
		}
		if !contains(pushFileTypes, f.Type) {
			return nil, invalidRequest("every file type must be credential, workflow, tags, variables, " +
				"folders, project, or datatable")
		}
		if seen[f] {
			return nil, invalidRequest("files must not name an object twice")
		}
		seen[f] = true
		names = append(names, map[string]string{"id": f.ID, "type": f.Type})
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var pushed struct {
		Data []sourceControlFileJSON `json:"data"`
	}
	var conflicts struct {
		Conflicts []sourceControlFileJSON `json:"conflicts"`
	}
	err = client.do(ctx, op, http.MethodPost, "/source-control/push", nil,
		map[string]any{"commitMessage": input.CommitMessage, "fileNames": names, "force": input.Force}, &pushed,
		maxResponseBytes, true, &conflicts)
	if errors.Is(err, errConflict) {
		return sourceControlResult("conflict", true, conflicts.Conflicts), nil
	}
	if err != nil {
		return nil, permissionNeutral(err)
	}
	return sourceControlResult("pushed", false, pushed.Data), nil
}

// validCommitMessage keeps a commit message inside n8n's limit (1 to 1000 UTF-16 units) and, narrower than
// n8n, free of every control character except the line feed.
func validCommitMessage(message string) error {
	if strings.TrimSpace(message) == "" {
		return invalidRequest("commit_message must not be empty")
	}
	if !utf8.ValidString(message) || len(utf16.Encode([]rune(message))) > maxCommitMessageLength {
		return invalidRequest("commit_message must be valid text of at most " +
			strconv.Itoa(maxCommitMessageLength) + " characters")
	}
	for _, r := range message {
		if r != '\n' && (r < 0x20 || (r >= 0x7f && r <= 0x9f)) {
			return invalidRequest("commit_message must not contain control characters other than a line feed")
		}
	}
	return nil
}
