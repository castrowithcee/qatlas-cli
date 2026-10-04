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
	ID: Provider + ".sourcecontrol.status", Version: 1, Title: "Preview n8n source control changes",
	Description: "Preview the pending changes between the bound n8n instance and its connected Git branch in " +
		"one direction: push (local changes not yet in Git) or pull (remote changes not yet imported). It " +
		"changes nothing. " + sourceControlInstanceNote,
	Tags: []string{"n8n", "sourcecontrol", "status", "automation"}, Risk: sourceControlStatusRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"direction":{"type":"string",` +
		`"enum":["push","pull"]}},"required":["direction"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"direction":{"type":"string"},` +
		`"files":{"type":"array","items":` + sourceControlFileSchema + `},"count":{"type":"integer"},` +
		`"truncated":{"type":"boolean"}},"required":["direction","files","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "direction", Required: true,
		Description: "push or pull; there is no default"}},
	Fields: []capability.Field{
		{Name: "direction", Description: "The requested direction"},
		{Name: "files", Description: sourceControlFilesFieldNote},
		{Name: "count", Description: "Number of files n8n reported"},
		{Name: "truncated", Description: "True when n8n reported more files than are listed"},
	},
	Examples: []capability.Example{{Description: "Preview what a pull would import",
		Arguments: json.RawMessage(`{"direction":"pull"}`)}},
}

var sourceControlPull = capability.Descriptor{
	ID: Provider + ".sourcecontrol.pull", Version: 1, Title: "Pull n8n changes from Git",
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
	ID: Provider + ".sourcecontrol.push", Version: 1, Title: "Push n8n changes to Git",
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
		Reason string `json:"reason"`
	} `json:"publishingErrorDetails"`
	ContentImportPolicy *struct {
		Violations []struct {
			Kind    string `json:"kind"`
			CheckID string `json:"checkId"`
			Message string `json:"message"`
		} `json:"violations"`
		CheckErrors []json.RawMessage `json:"checkErrors"`
	} `json:"contentImportPolicy"`
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

// SourceControlStatus is what sourcecontrol.status reports.
type SourceControlStatus struct {
	Direction string              `json:"direction"`
	Files     []SourceControlFile `json:"files"`
	Count     int                 `json:"count"`
	Truncated bool                `json:"truncated,omitempty"`
}

// SourceControlResult is what sourcecontrol.pull and sourcecontrol.push report. Outcome conflict is n8n's
// refusal, not a failure: nothing was changed.
type SourceControlResult struct {
	Outcome   string              `json:"outcome"`
	Conflict  bool                `json:"conflict"`
	Files     []SourceControlFile `json:"files"`
	Count     int                 `json:"count"`
	Truncated bool                `json:"truncated,omitempty"`
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
	files, truncated := summarizeSourceControlFiles(page.Data)
	return &SourceControlStatus{Direction: input.Direction, Files: files, Count: len(page.Data),
		Truncated: truncated}, nil
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
