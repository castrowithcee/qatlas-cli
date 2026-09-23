package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// GitHub Actions of a repository connection. The observer tools read compact, paged metadata of workflows,
// runs, jobs, and artifacts, and one tool reads a bounded excerpt of one job log. The operator tools
// dispatch one named workflow or re-run or cancel one named run; they are executions, need confirmation,
// and are sent once. Every route lies below the bound repository, and nothing is stored: a log excerpt
// passes through memory into the answer only. Artifacts are zip archives, so only their metadata is read.

// logSensitivity classifies a job log: it may carry whatever a workflow printed.
const logSensitivity = "github-actions-log"

// Bounds of the Actions tools. A job log is read from its end: at most maxLogBytes of it are answered,
// and a storage that ignores the range request is read up to maxLogDownload before Qatlas gives up.
const (
	defaultLogLines   = 50
	maxLogLines       = 500
	defaultLogBytes   = 8 << 10
	minLogBytes       = 1 << 10
	maxLogBytes       = 64 << 10
	maxLogDownload    = 8 << 20
	maxFilteredRuns   = 1000
	maxPage           = 100000
	maxWorkflowFile   = 512 << 10
	maxDispatchInputs = 25
	maxInputLength    = 4096
	workflowsDir      = ".github/workflows/"
)

// Input patterns of the Actions tools, doubled backslashes included for the JSON schemas.
const (
	workflowPattern = `^([0-9]{1,19}|[A-Za-z0-9_.-]{1,100}\\.ya?ml)$`
	refPattern      = `^[A-Za-z0-9_][A-Za-z0-9._/-]{0,254}$`
	actorPattern    = `^[A-Za-z0-9][A-Za-z0-9_-]{0,99}(\\[bot\\])?$`
	eventPattern    = `^[a-z][a-z_]{0,49}$`
	timePattern     = `^[0-9]{4}-[0-9]{2}-[0-9]{2}(T[0-9]{2}:[0-9]{2}:[0-9]{2}Z)?$`
	inputPattern    = `^[A-Za-z_][A-Za-z0-9_-]{0,99}$`
)

const (
	actionsIDSchema = `{"type":"integer","minimum":1,"maximum":9007199254740991}`
	workflowSchema  = `{"type":"string","minLength":1,"maxLength":120,"pattern":"` + workflowPattern + `"}`
	refSchema       = `{"type":"string","minLength":1,"maxLength":255,"pattern":"` + refPattern + `"}`
	timeSchema      = `{"type":"string","maxLength":20,"pattern":"` + timePattern + `"}`
	limitSchema     = `{"type":"integer","minimum":1,"maximum":100}`
	pagingKeys      = `"limit":` + limitSchema + `,"cursor":` + cursorSchema
)

// runStatuses are the status and conclusion values the run list filters by.
var runStatuses = []string{"completed", "action_required", "cancelled", "failure", "neutral", "skipped", "stale",
	"success", "timed_out", "in_progress", "queued", "requested", "waiting", "pending"}

var pagingArguments = []capability.Argument{
	{Name: "limit", Description: "Entries per batch, from 1 through 100; 30 when omitted; a continuation keeps " +
		"the batch size of its first batch"},
	{Name: "cursor", Description: "Opaque next_cursor of a previous batch with the same filters; the first batch " +
		"when omitted"},
}

var pagingFields = []capability.Field{
	{Name: "next_cursor", Description: "Cursor of the following batch, absent when has_more is false"},
	{Name: "has_more", Description: "True when further matching entries follow"},
}

// listOutput is the schema of one paged Actions list.
func listOutput(key, properties, required string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"` + key + `":{"type":"array","items":{"type":"object",` +
		`"properties":{` + properties + `},` + required + `}},"next_cursor":{"type":"string"},` +
		`"has_more":{"type":"boolean"}},"required":["` + key + `","has_more"],"additionalProperties":false}`)
}

func inputSchema(properties string, required ...string) json.RawMessage {
	names := make([]string, len(required))
	for i, name := range required {
		names[i] = strconv.Quote(name)
	}
	return json.RawMessage(`{"type":"object","properties":{` + properties + `},"required":[` +
		strings.Join(names, ",") + `],"additionalProperties":false}`)
}

const workflowProperties = `"id":{"type":"integer"},"name":{"type":"string"},"path":{"type":"string"},` +
	`"state":{"type":"string"},"url":{"type":"string"}`

const workflowRequired = `"required":["id","name","path","state"],"additionalProperties":false`

const runProperties = `"id":{"type":"integer"},"name":{"type":"string"},"title":{"type":"string"},` +
	`"workflow_id":{"type":"integer"},"run_number":{"type":"integer"},"attempt":{"type":"integer"},` +
	`"event":{"type":"string"},"status":{"type":"string"},"conclusion":{"type":"string"},` +
	`"branch":{"type":"string"},"head_sha":{"type":"string"},"actor":{"type":"string"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"},"started_at":{"type":"string"},` +
	`"url":{"type":"string"}`

const runRequired = `"required":["id","status"],"additionalProperties":false`

const jobProperties = `"id":{"type":"integer"},"run_id":{"type":"integer"},"name":{"type":"string"},` +
	`"status":{"type":"string"},"conclusion":{"type":"string"},"attempt":{"type":"integer"},` +
	`"started_at":{"type":"string"},"completed_at":{"type":"string"},"url":{"type":"string"}`

const jobRequired = `"required":["id","name","status"],"additionalProperties":false`

const stepsProperty = `"steps":{"type":"array","items":{"type":"object","properties":{` +
	`"number":{"type":"integer"},"name":{"type":"string"},"status":{"type":"string"},` +
	`"conclusion":{"type":"string"},"started_at":{"type":"string"},"completed_at":{"type":"string"}},` +
	`"required":["number","name","status"],"additionalProperties":false}}`

const artifactProperties = `"id":{"type":"integer"},"name":{"type":"string"},"size_bytes":{"type":"integer"},` +
	`"expired":{"type":"boolean"},"created_at":{"type":"string"},"expires_at":{"type":"string"},` +
	`"digest":{"type":"string"}`

const artifactRequired = `"required":["id","name","size_bytes","expired"],"additionalProperties":false`

var workflowsList = capability.Descriptor{
	ID:      Provider + ".workflows.list",
	Version: 1,
	Title:   "List GitHub Actions workflows",
	Description: "List one bounded batch of the workflows of the repository bound to an explicit connection, " +
		"without their files",
	Tags:                       []string{"github", "actions", "workflows", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(pagingKeys),
	OutputSchema:               listOutput("workflows", workflowProperties, workflowRequired),
	Arguments:                  pagingArguments,
	Fields: append([]capability.Field{
		{Name: "workflows", Description: "Workflows with identifier, name, file path, and state"},
	}, pagingFields...),
	Examples: []capability.Example{{Description: "List the workflows", Arguments: json.RawMessage(`{}`)}},
}

var workflowsGet = capability.Descriptor{
	ID:                         Provider + ".workflows.get",
	Version:                    1,
	Title:                      "Get a GitHub Actions workflow",
	Description:                "Read one workflow of the repository bound to an explicit connection, without its file",
	Tags:                       []string{"github", "actions", "workflows", "get"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"workflow":`+workflowSchema, "workflow"),
	OutputSchema:               json.RawMessage(`{"type":"object","properties":{` + workflowProperties + `},` + workflowRequired + `}`),
	Arguments: []capability.Argument{
		{Name: "workflow", Description: "Workflow identifier or file name such as ci.yml", Required: true},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Workflow identifier"},
		{Name: "path", Description: "Path of the workflow file"},
		{Name: "state", Description: "active, or why GitHub does not run the workflow"},
	},
	Examples: []capability.Example{{Description: "Read one workflow", Arguments: json.RawMessage(`{"workflow":"ci.yml"}`)}},
}

var runsList = capability.Descriptor{
	ID:      Provider + ".workflowruns.list",
	Version: 1,
	Title:   "List GitHub Actions workflow runs",
	Description: "List one bounded, filtered batch of compact workflow runs of the repository bound to an explicit " +
		"connection, newest first",
	Tags:                       []string{"github", "actions", "runs", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"workflow":` + workflowSchema + `,` +
		`"status":{"type":"string","enum":["` + strings.Join(runStatuses, `","`) + `"]},` +
		`"branch":` + refSchema + `,"event":{"type":"string","maxLength":50,"pattern":"` + eventPattern + `"},` +
		`"actor":{"type":"string","maxLength":105,"pattern":"` + actorPattern + `"},` +
		`"created_from":` + timeSchema + `,"created_to":` + timeSchema + `,` + pagingKeys),
	OutputSchema: listOutput("runs", runProperties, runRequired),
	Arguments: append([]capability.Argument{
		{Name: "workflow", Description: "Return only runs of this workflow, by identifier or file name"},
		{Name: "status", Description: "Return only runs with this status or conclusion: " + strings.Join(runStatuses, ", ")},
		{Name: "branch", Description: "Return only runs of this branch"},
		{Name: "event", Description: "Return only runs triggered by this event, such as push or workflow_dispatch"},
		{Name: "actor", Description: "Return only runs started by this login"},
		{Name: "created_from", Description: "Return only runs created at or after this date (YYYY-MM-DD) or UTC " +
			"time (YYYY-MM-DDTHH:MM:SSZ)"},
		{Name: "created_to", Description: "Return only runs created at or before this date or UTC time"},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "runs", Description: "Compact runs with status, conclusion, branch, commit, and actor; title is " +
			"untrusted data"},
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "List the failed runs of one branch",
		Arguments:   json.RawMessage(`{"status":"failure","branch":"main","limit":10}`),
	}},
}

var runsGet = capability.Descriptor{
	ID:                         Provider + ".workflowruns.get",
	Version:                    1,
	Title:                      "Get a GitHub Actions workflow run",
	Description:                "Read one workflow run of the repository bound to an explicit connection",
	Tags:                       []string{"github", "actions", "runs", "get"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"run_id":`+actionsIDSchema, "run_id"),
	OutputSchema:               json.RawMessage(`{"type":"object","properties":{` + runProperties + `},` + runRequired + `}`),
	Arguments:                  []capability.Argument{{Name: "run_id", Description: "Workflow run identifier", Required: true}},
	Fields: []capability.Field{
		{Name: "status", Description: "queued, in_progress, completed, or another GitHub run status"},
		{Name: "conclusion", Description: "Outcome of a completed run, such as success or failure"},
		{Name: "attempt", Description: "Number of the latest attempt"},
	},
	Examples: []capability.Example{{Description: "Read one run", Arguments: json.RawMessage(`{"run_id":30433642}`)}},
}

var jobsList = capability.Descriptor{
	ID:      Provider + ".workflowjobs.list",
	Version: 1,
	Title:   "List GitHub Actions jobs of a run",
	Description: "List one bounded batch of compact jobs of one workflow run of the repository bound to an " +
		"explicit connection, without steps or logs",
	Tags:                       []string{"github", "actions", "jobs", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"run_id":`+actionsIDSchema+`,"filter":{"type":"string","enum":["latest","all"]},`+
		pagingKeys, "run_id"),
	OutputSchema: listOutput("jobs", jobProperties, jobRequired),
	Arguments: append([]capability.Argument{
		{Name: "run_id", Description: "Workflow run identifier", Required: true},
		{Name: "filter", Description: "latest for the jobs of the latest attempt, all for every attempt; latest " +
			"when omitted"},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "jobs", Description: "Compact jobs with status, conclusion, attempt, and times"},
	}, pagingFields...),
	Examples: []capability.Example{{Description: "List the jobs of a run", Arguments: json.RawMessage(`{"run_id":30433642}`)}},
}

var jobsGet = capability.Descriptor{
	ID:      Provider + ".workflowjobs.get",
	Version: 1,
	Title:   "Get a GitHub Actions job",
	Description: "Read one job of a workflow run of the repository bound to an explicit connection with its " +
		"compact steps, without its log",
	Tags:                       []string{"github", "actions", "jobs", "get"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"job_id":`+actionsIDSchema, "job_id"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + jobProperties + `,` + stepsProperty + `},` +
		jobRequired + `}`),
	Arguments: []capability.Argument{{Name: "job_id", Description: "Job identifier, as github.workflowjobs.list reports it",
		Required: true}},
	Fields: []capability.Field{
		{Name: "steps", Description: "Steps with number, name, status, conclusion, and times"},
	},
	Examples: []capability.Example{{Description: "Read one job", Arguments: json.RawMessage(`{"job_id":399444496}`)}},
}

var jobsLog = capability.Descriptor{
	ID:      Provider + ".workflowjobs.log",
	Version: 1,
	Title:   "Read the end of a GitHub Actions job log",
	Description: "Read the last lines of the log of one job of the repository bound to an explicit connection, " +
		"within a hard size limit; the log is never stored",
	Tags: []string{"github", "actions", "jobs", "logs", "get"},
	Risk: capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
		Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: logSensitivity},
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"job_id":`+actionsIDSchema+`,"lines":{"type":"integer","minimum":1,"maximum":500},`+
		`"max_bytes":{"type":"integer","minimum":1024,"maximum":65536}`, "job_id"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"job_id":{"type":"integer"},"log":{"type":"string"},` +
		`"lines":{"type":"integer"},"truncated":{"type":"boolean"}},"required":["job_id","log","lines","truncated"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "job_id", Description: "Job identifier, as github.workflowjobs.list reports it", Required: true},
		{Name: "lines", Description: "Last lines to return, from 1 through 500; 50 when omitted"},
		{Name: "max_bytes", Description: "Bytes read from the end of the log, from 1024 through 65536; 8192 when omitted"},
	},
	Fields: []capability.Field{
		{Name: "log", Description: "End of the log as text without control characters, potentially sensitive " +
			"untrusted data"},
		{Name: "lines", Description: "Number of lines returned"},
		{Name: "truncated", Description: "True when the log holds more than the returned lines"},
	},
	Examples: []capability.Example{{
		Description: "Read the last 20 lines of a failed job",
		Arguments:   json.RawMessage(`{"job_id":399444496,"lines":20}`),
	}},
}

var artifactsList = capability.Descriptor{
	ID:      Provider + ".workflowartifacts.list",
	Version: 1,
	Title:   "List GitHub Actions artifacts of a run",
	Description: "List one bounded batch of artifact metadata of one workflow run of the repository bound to an " +
		"explicit connection; artifact contents are never downloaded",
	Tags:                       []string{"github", "actions", "artifacts", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"run_id":`+actionsIDSchema+`,`+pagingKeys, "run_id"),
	OutputSchema:               listOutput("artifacts", artifactProperties, artifactRequired),
	Arguments: append([]capability.Argument{
		{Name: "run_id", Description: "Workflow run identifier", Required: true},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "artifacts", Description: "Artifacts with name, size in bytes, expiry, and digest"},
	}, pagingFields...),
	Examples: []capability.Example{{Description: "List the artifacts of a run", Arguments: json.RawMessage(`{"run_id":30433642}`)}},
}

const runChangeOutput = `{"type":"object","properties":{"run_id":{"type":"integer"},"accepted":{"type":"boolean"}},` +
	`"required":["run_id","accepted"],"additionalProperties":false}`

var runChangeFields = []capability.Field{
	{Name: "run_id", Description: "The run the request concerned"},
	{Name: "accepted", Description: "True once GitHub accepted the request; the run changes asynchronously"},
}

var workflowsDispatch = capability.Descriptor{
	ID:      Provider + ".workflows.dispatch",
	Version: 1,
	Title:   "Dispatch a GitHub Actions workflow",
	Description: "Start one workflow_dispatch run of one workflow of the repository bound to an explicit connection " +
		"on a branch or tag, with inputs the workflow declares; a repeated call starts a second run",
	Tags:                       []string{"github", "actions", "workflows", "dispatch", "execute"},
	Risk:                       changeRisk(capability.EffectExecute, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"workflow":`+workflowSchema+`,"ref":`+refSchema+`,"inputs":{"type":"object"}`,
		"workflow", "ref"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"workflow_id":{"type":"integer"},` +
		`"path":{"type":"string"},"ref":{"type":"string"},"accepted":{"type":"boolean"}},` +
		`"required":["workflow_id","path","ref","accepted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "workflow", Description: "Workflow identifier or file name such as release.yml", Required: true},
		{Name: "ref", Description: "Branch or tag whose workflow file runs", Required: true},
		{Name: "inputs", Description: "Input values by name, each a string of at most 4096 characters; only inputs " +
			"the workflow file at ref declares for workflow_dispatch, every required one without a default; at most 25"},
	},
	Fields: []capability.Field{
		{Name: "workflow_id", Description: "Identifier of the dispatched workflow"},
		{Name: "accepted", Description: "True once GitHub accepted the dispatch; list the runs with event " +
			"workflow_dispatch to follow it"},
	},
	Examples: []capability.Example{{
		Description: "Dispatch a release workflow on main",
		Arguments:   json.RawMessage(`{"workflow":"release.yml","ref":"main","inputs":{"channel":"beta"}}`),
	}},
}

var runsRerun = capability.Descriptor{
	ID:      Provider + ".workflowruns.rerun",
	Version: 1,
	Title:   "Re-run a GitHub Actions workflow run",
	Description: "Re-run every job of one completed workflow run of the repository bound to an explicit " +
		"connection; a repeated call starts another attempt",
	Tags:                       []string{"github", "actions", "runs", "rerun", "execute"},
	Risk:                       changeRisk(capability.EffectExecute, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"run_id":`+actionsIDSchema, "run_id"),
	OutputSchema:               json.RawMessage(runChangeOutput),
	Arguments:                  []capability.Argument{{Name: "run_id", Description: "Completed workflow run identifier", Required: true}},
	Fields:                     runChangeFields,
	Examples:                   []capability.Example{{Description: "Re-run a run", Arguments: json.RawMessage(`{"run_id":30433642}`)}},
}

var runsRerunFailed = capability.Descriptor{
	ID:      Provider + ".workflowruns.rerunfailed",
	Version: 1,
	Title:   "Re-run the failed jobs of a GitHub Actions run",
	Description: "Re-run the failed jobs and their dependents of one completed workflow run of the repository bound " +
		"to an explicit connection; a repeated call starts another attempt",
	Tags:                       []string{"github", "actions", "runs", "rerun", "execute"},
	Risk:                       changeRisk(capability.EffectExecute, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"run_id":`+actionsIDSchema, "run_id"),
	OutputSchema:               json.RawMessage(runChangeOutput),
	Arguments:                  []capability.Argument{{Name: "run_id", Description: "Completed workflow run identifier", Required: true}},
	Fields:                     runChangeFields,
	Examples: []capability.Example{{Description: "Re-run the failed jobs of a run",
		Arguments: json.RawMessage(`{"run_id":30433642}`)}},
}

var runsCancel = capability.Descriptor{
	ID:      Provider + ".workflowruns.cancel",
	Version: 1,
	Title:   "Cancel a GitHub Actions workflow run",
	Description: "Ask GitHub to cancel one workflow run of the repository bound to an explicit connection that has " +
		"not completed; jobs end as GitHub cancels them",
	Tags:                       []string{"github", "actions", "runs", "cancel", "execute"},
	Risk:                       changeRisk(capability.EffectExecute, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"run_id":`+actionsIDSchema, "run_id"),
	OutputSchema:               json.RawMessage(runChangeOutput),
	Arguments:                  []capability.Argument{{Name: "run_id", Description: "Workflow run identifier", Required: true}},
	Fields:                     runChangeFields,
	Examples:                   []capability.Example{{Description: "Cancel a run", Arguments: json.RawMessage(`{"run_id":30433642}`)}},
}

// observerTools and operatorTools are the two tool groups of GitHub Actions and of their profiles.
var (
	observerTools = []string{workflowsList.ID, workflowsGet.ID, runsList.ID, runsGet.ID, jobsList.ID, jobsGet.ID,
		jobsLog.ID, artifactsList.ID}
	operatorTools = []string{workflowsDispatch.ID, runsRerun.ID, runsRerunFailed.ID, runsCancel.ID}
)

// actionsOperations binds every Actions tool to its handler.
func actionsOperations() []capability.Operation {
	bind := func(descriptor capability.Descriptor, check func(*actionsArguments, target) error,
		call func(context.Context, *Client, *actionsArguments) (any, error)) capability.Operation {
		return capability.Operation{Descriptor: descriptor, Handler: actionsHandler(descriptor.ID, check, call)}
	}
	return []capability.Operation{
		bind(workflowsList, listCheck("workflows"), func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			return c.listWorkflows(ctx, a)
		}),
		bind(workflowsGet, checkWorkflowArgument, func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			raw, err := c.workflow(ctx, "get workflow", a.Workflow)
			if err != nil {
				return nil, err
			}
			view := raw.view()
			return &view, nil
		}),
		bind(runsList, listCheck("runs"), func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			return c.listRuns(ctx, a)
		}),
		bind(runsGet, checkRunArgument, func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			raw, err := c.run(ctx, "get workflow run", a.RunID)
			if err != nil {
				return nil, err
			}
			view := raw.view()
			return &view, nil
		}),
		bind(jobsList, listCheck("jobs"), func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			return c.listJobs(ctx, a)
		}),
		bind(jobsGet, checkJobArgument, func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			return c.job(ctx, a.JobID)
		}),
		bind(jobsLog, checkLogArguments, func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			return c.jobLog(ctx, a.JobID, a.Lines, a.MaxBytes)
		}),
		bind(artifactsList, listCheck("artifacts"), func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			return c.listArtifacts(ctx, a)
		}),
		bind(workflowsDispatch, checkDispatchArguments, func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			return c.dispatchWorkflow(ctx, a.Workflow, a.Ref, a.inputs)
		}),
		bind(runsRerun, checkRunArgument, func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			return c.changeRun(ctx, "re-run workflow run", a.RunID, "rerun")
		}),
		bind(runsRerunFailed, checkRunArgument, func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			return c.changeRun(ctx, "re-run failed jobs", a.RunID, "rerun-failed-jobs")
		}),
		bind(runsCancel, checkRunArgument, func(ctx context.Context, c *Client, a *actionsArguments) (any, error) {
			return c.changeRun(ctx, "cancel workflow run", a.RunID, "cancel")
		}),
	}
}

// actionsArguments holds the arguments of every Actions tool; the input schema of each tool admits only its
// own. page, perPage, and inputs are derived by the checks.
type actionsArguments struct {
	Workflow    string         `json:"workflow"`
	RunID       int64          `json:"run_id"`
	JobID       int64          `json:"job_id"`
	Ref         string         `json:"ref"`
	Inputs      map[string]any `json:"inputs"`
	Status      string         `json:"status"`
	Branch      string         `json:"branch"`
	Event       string         `json:"event"`
	Actor       string         `json:"actor"`
	CreatedFrom string         `json:"created_from"`
	CreatedTo   string         `json:"created_to"`
	Filter      string         `json:"filter"`
	Lines       int            `json:"lines"`
	MaxBytes    int            `json:"max_bytes"`
	Limit       int            `json:"limit"`
	Cursor      string         `json:"cursor"`

	// The arguments of the maintainer and administrator tools. A boolean is a pointer, so a value left out
	// stays apart from false.
	Path                       string `json:"path"`
	Content                    string `json:"content"`
	SHA                        string `json:"sha"`
	Message                    string `json:"message"`
	Enabled                    *bool  `json:"enabled"`
	AllowedActions             string `json:"allowed_actions"`
	DefaultWorkflowPermissions string `json:"default_workflow_permissions"`
	CanApprove                 *bool  `json:"can_approve_pull_request_reviews"`

	list          string
	page, perPage int
	binding       []byte
	inputs        map[string]string
}

// actionsHandler decodes and checks the arguments and refuses a project connection before a credential is
// resolved, so a refused request never becomes a provider call.
func actionsHandler(id string, check func(*actionsArguments, target) error,
	call func(context.Context, *Client, *actionsArguments) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments actionsArguments
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, unreadable(id)
		}
		bound, err := requireKind(resolved, kindRepository, id)
		if err != nil {
			return nil, err
		}
		if err := check(&arguments, bound); err != nil {
			return nil, err
		}
		client, err := Open(resolved, secrets, red)
		if err != nil {
			return nil, err
		}
		return call(ctx, client, &arguments)
	}
}

func checkRunArgument(a *actionsArguments, _ target) error {
	if a.RunID < 1 {
		return invalidRequest("run_id must be a positive run identifier")
	}
	return nil
}

func checkJobArgument(a *actionsArguments, _ target) error {
	if a.JobID < 1 {
		return invalidRequest("job_id must be a positive job identifier")
	}
	return nil
}

func checkWorkflowArgument(a *actionsArguments, _ target) error {
	if !validWorkflow(a.Workflow) {
		return invalidRequest("workflow must be a workflow identifier or a file name ending in .yml or .yaml")
	}
	return nil
}

func checkLogArguments(a *actionsArguments, bound target) error {
	if err := checkJobArgument(a, bound); err != nil {
		return err
	}
	if a.Lines == 0 {
		a.Lines = defaultLogLines
	}
	if a.MaxBytes == 0 {
		a.MaxBytes = defaultLogBytes
	}
	if a.Lines < 1 || a.Lines > maxLogLines || a.MaxBytes < minLogBytes || a.MaxBytes > maxLogBytes {
		return invalidRequest(fmt.Sprintf("lines must be between 1 and %d and max_bytes between %d and %d",
			maxLogLines, minLogBytes, maxLogBytes))
	}
	return nil
}

// checkDispatchArguments applies the bounds of a dispatch before a credential is resolved. Whether the
// inputs are those the workflow declares is checked against its file once it was read.
func checkDispatchArguments(a *actionsArguments, bound target) error {
	if err := checkWorkflowArgument(a, bound); err != nil {
		return err
	}
	if !validRef(a.Ref) {
		return invalidRequest("ref must be a branch or tag name")
	}
	if len(a.Inputs) > maxDispatchInputs {
		return invalidRequest(fmt.Sprintf("inputs accepts at most %d values", maxDispatchInputs))
	}
	a.inputs = map[string]string{}
	for name, value := range a.Inputs {
		text, ok := value.(string)
		switch {
		case !validInputName(name):
			return invalidRequest("inputs contains a name no workflow input can carry")
		case !ok:
			return invalidRequest("an input value must be a string; write a boolean as \"true\" or \"false\"")
		case utf8.RuneCountInString(text) > maxInputLength:
			return invalidRequest(fmt.Sprintf("an input value must hold at most %d characters", maxInputLength))
		}
		a.inputs[name] = text
	}
	return nil
}

// listCheck returns the check of one Actions list: it applies the defaults and bounds of the filters and
// resolves the REST page the cursor continues at.
func listCheck(list string) func(*actionsArguments, target) error {
	return func(a *actionsArguments, bound target) error {
		a.list = list
		limit, err := normalizeLimit(a.Limit)
		if err != nil {
			return err
		}
		switch list {
		case "jobs", "artifacts":
			if err := checkRunArgument(a, bound); err != nil {
				return err
			}
		}
		switch a.Filter {
		case "":
			if list == "jobs" {
				a.Filter = "latest"
			}
		case "latest", "all":
		default:
			return invalidRequest("filter must be latest or all")
		}
		if a.Workflow != "" && !validWorkflow(a.Workflow) {
			return invalidRequest("workflow must be a workflow identifier or a file name ending in .yml or .yaml")
		}
		if a.Status != "" && !containsFold(runStatuses, a.Status) {
			return invalidRequest("status must be one of " + strings.Join(runStatuses, ", "))
		}
		if a.Branch != "" && !validRef(a.Branch) {
			return invalidRequest("branch must be a branch name")
		}
		if a.Event != "" && !validEvent(a.Event) {
			return invalidRequest("event must be a GitHub event name such as push")
		}
		if a.Actor != "" && !validLogin(strings.TrimSuffix(a.Actor, "[bot]")) {
			return invalidRequest("actor must be a GitHub login")
		}
		if err := checkCreated(a.CreatedFrom, a.CreatedTo); err != nil {
			return err
		}
		a.binding = fingerprint("actions", list, bound.String(), a.RunID, a.Filter, a.Workflow,
			strings.ToLower(a.Status), a.Branch, a.Event, strings.ToLower(a.Actor), a.CreatedFrom, a.CreatedTo)
		a.page, a.perPage, err = pageOf(a.binding, a.Cursor, limit)
		return err
	}
}

// pageOf returns the REST page and page size a list request reads. The REST lists are paged by number, so a
// cursor carries the page size of its first batch along.
func pageOf(binding []byte, cursor string, limit int) (int, int, error) {
	after, err := decodeCursor(binding, cursor)
	if err != nil || after == "" {
		return 1, limit, err
	}
	first, second, _ := strings.Cut(after, ":")
	page, pageErr := strconv.Atoi(first)
	perPage, sizeErr := strconv.Atoi(second)
	if pageErr != nil || sizeErr != nil || page < 2 || page > maxPage || perPage < 1 || perPage > maxLimit {
		return 0, 0, invalidRequest("cursor is not a next_cursor of this list")
	}
	return page, perPage, nil
}

// more reports whether a REST list continues after the page just read and returns the cursor of the next
// one. A short page ends the list whatever the reported total says.
func (a *actionsArguments) more(got, total int) (bool, string) {
	if got < a.perPage || a.page*a.perPage >= total || a.page >= maxPage {
		return false, ""
	}
	return true, encodeCursor(a.binding, strconv.Itoa(a.page+1)+":"+strconv.Itoa(a.perPage))
}

func (a *actionsArguments) query() url.Values {
	return url.Values{"per_page": {strconv.Itoa(a.perPage)}, "page": {strconv.Itoa(a.page)}}
}

func checkCreated(from, to string) error {
	var bounds [2]time.Time
	for i, value := range []string{from, to} {
		if value == "" {
			continue
		}
		parsed, err := time.Parse(time.DateOnly, value)
		if err != nil {
			parsed, err = time.Parse("2006-01-02T15:04:05Z", value)
		}
		if err != nil {
			return invalidRequest("created_from and created_to must be a date as YYYY-MM-DD or a UTC time as " +
				"YYYY-MM-DDTHH:MM:SSZ")
		}
		bounds[i] = parsed
	}
	if from != "" && to != "" && bounds[0].After(bounds[1]) {
		return invalidRequest("created_from must not lie after created_to")
	}
	return nil
}

// createdRange writes the time filter in the GitHub search syntax the run list accepts.
func createdRange(from, to string) string {
	switch {
	case from != "" && to != "":
		return from + ".." + to
	case from != "":
		return ">=" + from
	case to != "":
		return "<=" + to
	}
	return ""
}

// validWorkflow mirrors workflowPattern: a numeric identifier or the file name of a workflow.
func validWorkflow(value string) bool {
	if value != "" && len(value) <= 19 && strings.Trim(value, "0123456789") == "" {
		return true
	}
	name := strings.TrimSuffix(strings.TrimSuffix(value, ".yml"), ".yaml")
	if name == value || name == "" || len(name) > 100 {
		return false
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// validRef accepts a branch or tag name that stays one query value and one path argument of GitHub: the
// characters of refPattern, without the sequences Git refuses in a ref name.
func validRef(value string) bool {
	if value == "" || len(value) > 255 || strings.Contains(value, "..") || strings.Contains(value, "//") ||
		strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") || strings.HasSuffix(value, ".lock") {
		return false
	}
	for i, r := range value {
		alnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
		if !alnum && (i == 0 || (r != '.' && r != '/' && r != '-')) {
			return false
		}
	}
	return true
}

func validEvent(value string) bool {
	if value == "" || len(value) > 50 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	return strings.Trim(value, "abcdefghijklmnopqrstuvwxyz_") == ""
}

// validInputName mirrors inputPattern.
func validInputName(value string) bool {
	if value == "" || len(value) > 100 {
		return false
	}
	for i, r := range value {
		letter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
		if !letter && (i == 0 || !((r >= '0' && r <= '9') || r == '-')) {
			return false
		}
	}
	return true
}

// Permission messages of the Actions tools. GitHub decides on every request; a message names what such a
// request needs without claiming what the configured token holds.
const (
	actionsReadPermission = "GitHub refused this token the Actions data of this repository; reading it needs repo " +
		"on a classic token for a private repository, or Actions: read on a fine-grained token"
	actionsChangePermission = "GitHub refused this change of the Actions of this repository; it needs repo on a " +
		"classic token, or Actions: read and write on a fine-grained token, and a repository or organization " +
		"policy may forbid it as well"
	actionsFilePermission = "GitHub refused this token the workflow file; a dispatch reads its declared inputs " +
		"first, which needs repo on a classic token for a private repository, or Contents: read on a " +
		"fine-grained token"
)

// actionsFailure replaces the generic permission message of a refused Actions request.
func actionsFailure(err error, message string) error {
	var failure *provider.Error
	if errors.As(err, &failure) && failure.Class == provider.ClassPermission {
		refused := *failure
		refused.Message = message
		return &refused
	}
	return err
}

func (c *Client) repoPath(rest string) string {
	return fmt.Sprintf("/repos/%s/%s/%s", url.PathEscape(c.target.owner), url.PathEscape(c.target.repo), rest)
}

func (c *Client) actionsPath(rest string) string {
	return c.repoPath("actions/" + rest)
}

// actionsRead performs one bounded REST read of the Actions of the bound repository.
func (c *Client) actionsRead(ctx context.Context, op, path string, query url.Values, out any) error {
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return actionsFailure(c.rest(ctx, op, c.actionsPath(path), out), actionsReadPermission)
}

// WorkflowList is one batch of workflows.
type WorkflowList struct {
	Workflows  []Workflow `json:"workflows"`
	NextCursor string     `json:"next_cursor,omitempty"`
	HasMore    bool       `json:"has_more"`
}

// Workflow is the compact view of one workflow.
type Workflow struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	State string `json:"state"`
	URL   string `json:"url,omitempty"`
}

type workflowJSON struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
}

func (w workflowJSON) view() Workflow {
	return Workflow{ID: w.ID, Name: w.Name, Path: w.Path, State: w.State, URL: w.HTMLURL}
}

func invalidEntry(op, what string) error {
	return &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
		Message: "GitHub returned " + what + " without a usable identifier"}
}

func (c *Client) listWorkflows(ctx context.Context, a *actionsArguments) (*WorkflowList, error) {
	const op = "list workflows"
	var raw struct {
		TotalCount int            `json:"total_count"`
		Workflows  []workflowJSON `json:"workflows"`
	}
	if err := c.actionsRead(ctx, op, "workflows", a.query(), &raw); err != nil {
		return nil, err
	}
	result := &WorkflowList{Workflows: make([]Workflow, 0, len(raw.Workflows))}
	for _, workflow := range raw.Workflows {
		if workflow.ID < 1 {
			return nil, invalidEntry(op, "a workflow")
		}
		result.Workflows = append(result.Workflows, workflow.view())
	}
	result.HasMore, result.NextCursor = a.more(len(raw.Workflows), raw.TotalCount)
	return result, nil
}

// workflow reads one workflow of the bound repository by identifier or file name.
func (c *Client) workflow(ctx context.Context, op, workflow string) (workflowJSON, error) {
	var raw workflowJSON
	if err := c.actionsRead(ctx, op, "workflows/"+url.PathEscape(workflow), nil, &raw); err != nil {
		return raw, err
	}
	if raw.ID < 1 {
		return raw, invalidEntry(op, "a workflow")
	}
	return raw, nil
}

// RunList is one batch of workflow runs.
type RunList struct {
	Runs       []WorkflowRun `json:"runs"`
	NextCursor string        `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
}

// WorkflowRun is the compact view of one workflow run. Title is untrusted data.
type WorkflowRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name,omitempty"`
	Title      string `json:"title,omitempty"`
	WorkflowID int64  `json:"workflow_id,omitempty"`
	RunNumber  int    `json:"run_number,omitempty"`
	Attempt    int    `json:"attempt,omitempty"`
	Event      string `json:"event,omitempty"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion,omitempty"`
	Branch     string `json:"branch,omitempty"`
	HeadSHA    string `json:"head_sha,omitempty"`
	Actor      string `json:"actor,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
	URL        string `json:"url,omitempty"`
}

// runJSON is the part of a REST run this provider reads; a null value leaves its field empty.
type runJSON struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	DisplayTitle string `json:"display_title"`
	WorkflowID   int64  `json:"workflow_id"`
	RunNumber    int    `json:"run_number"`
	RunAttempt   int    `json:"run_attempt"`
	Event        string `json:"event"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	HeadBranch   string `json:"head_branch"`
	HeadSHA      string `json:"head_sha"`
	Actor        *struct {
		Login string `json:"login"`
	} `json:"actor"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	RunStartedAt string `json:"run_started_at"`
	HTMLURL      string `json:"html_url"`
}

func (r runJSON) view() WorkflowRun {
	run := WorkflowRun{ID: r.ID, Name: r.Name, Title: r.DisplayTitle, WorkflowID: r.WorkflowID,
		RunNumber: r.RunNumber, Attempt: r.RunAttempt, Event: r.Event, Status: r.Status, Conclusion: r.Conclusion,
		Branch: r.HeadBranch, HeadSHA: r.HeadSHA, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		StartedAt: r.RunStartedAt, URL: r.HTMLURL}
	if r.Actor != nil {
		run.Actor = r.Actor.Login
	}
	return run
}

// listRuns reads one page of runs, newest first. Pull requests are left out of every run, and GitHub
// answers at most maxFilteredRuns runs of a filtered list.
func (c *Client) listRuns(ctx context.Context, a *actionsArguments) (*RunList, error) {
	const op = "list workflow runs"
	query := a.query()
	query.Set("exclude_pull_requests", "true")
	filtered := false
	for key, value := range map[string]string{"status": strings.ToLower(a.Status), "branch": a.Branch,
		"event": a.Event, "actor": a.Actor, "created": createdRange(a.CreatedFrom, a.CreatedTo)} {
		if value != "" {
			query.Set(key, value)
			filtered = true
		}
	}
	path := "runs"
	if a.Workflow != "" {
		path = "workflows/" + url.PathEscape(a.Workflow) + "/runs"
	}
	var raw struct {
		TotalCount int       `json:"total_count"`
		Runs       []runJSON `json:"workflow_runs"`
	}
	if err := c.actionsRead(ctx, op, path, query, &raw); err != nil {
		return nil, err
	}
	result := &RunList{Runs: make([]WorkflowRun, 0, len(raw.Runs))}
	for _, run := range raw.Runs {
		if run.ID < 1 || run.Status == "" {
			return nil, invalidEntry(op, "a run")
		}
		result.Runs = append(result.Runs, run.view())
	}
	total := raw.TotalCount
	if filtered {
		total = min(total, maxFilteredRuns)
	}
	result.HasMore, result.NextCursor = a.more(len(raw.Runs), total)
	return result, nil
}

// run reads one run of the bound repository.
func (c *Client) run(ctx context.Context, op string, id int64) (runJSON, error) {
	var raw runJSON
	if err := c.actionsRead(ctx, op, "runs/"+strconv.FormatInt(id, 10), nil, &raw); err != nil {
		return raw, err
	}
	if raw.ID != id || raw.Status == "" {
		return raw, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "GitHub answered with a different run than the requested one"}
	}
	return raw, nil
}

// JobList is one batch of jobs of a run.
type JobList struct {
	Jobs       []WorkflowJob `json:"jobs"`
	NextCursor string        `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
}

// WorkflowJob is the compact view of one job; Steps is filled only for one job read on its own.
type WorkflowJob struct {
	ID          int64     `json:"id"`
	RunID       int64     `json:"run_id,omitempty"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion,omitempty"`
	Attempt     int       `json:"attempt,omitempty"`
	StartedAt   string    `json:"started_at,omitempty"`
	CompletedAt string    `json:"completed_at,omitempty"`
	URL         string    `json:"url,omitempty"`
	Steps       []JobStep `json:"steps,omitempty"`
}

// JobStep is the compact view of one step of a job.
type JobStep struct {
	Number      int    `json:"number"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion,omitempty"`
	StartedAt   string `json:"started_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

type jobJSON struct {
	ID          int64     `json:"id"`
	RunID       int64     `json:"run_id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	RunAttempt  int       `json:"run_attempt"`
	StartedAt   string    `json:"started_at"`
	CompletedAt string    `json:"completed_at"`
	HTMLURL     string    `json:"html_url"`
	Steps       []JobStep `json:"steps"`
}

func (j jobJSON) view() WorkflowJob {
	return WorkflowJob{ID: j.ID, RunID: j.RunID, Name: j.Name, Status: j.Status, Conclusion: j.Conclusion,
		Attempt: j.RunAttempt, StartedAt: j.StartedAt, CompletedAt: j.CompletedAt, URL: j.HTMLURL}
}

func (c *Client) listJobs(ctx context.Context, a *actionsArguments) (*JobList, error) {
	const op = "list workflow jobs"
	query := a.query()
	query.Set("filter", a.Filter)
	var raw struct {
		TotalCount int       `json:"total_count"`
		Jobs       []jobJSON `json:"jobs"`
	}
	if err := c.actionsRead(ctx, op, "runs/"+strconv.FormatInt(a.RunID, 10)+"/jobs", query, &raw); err != nil {
		return nil, err
	}
	result := &JobList{Jobs: make([]WorkflowJob, 0, len(raw.Jobs))}
	for _, job := range raw.Jobs {
		if job.ID < 1 {
			return nil, invalidEntry(op, "a job")
		}
		result.Jobs = append(result.Jobs, job.view())
	}
	result.HasMore, result.NextCursor = a.more(len(raw.Jobs), raw.TotalCount)
	return result, nil
}

// job reads one job of the bound repository with its steps.
func (c *Client) job(ctx context.Context, id int64) (*WorkflowJob, error) {
	const op = "get workflow job"
	var raw jobJSON
	if err := c.actionsRead(ctx, op, "jobs/"+strconv.FormatInt(id, 10), nil, &raw); err != nil {
		return nil, err
	}
	if raw.ID != id {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "GitHub answered with a different job than the requested one"}
	}
	job := raw.view()
	job.Steps = raw.Steps
	return &job, nil
}

// ArtifactList is one batch of artifact metadata of a run.
type ArtifactList struct {
	Artifacts  []Artifact `json:"artifacts"`
	NextCursor string     `json:"next_cursor,omitempty"`
	HasMore    bool       `json:"has_more"`
}

// Artifact is the metadata of one artifact. Its content, a zip archive, is never read.
type Artifact struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	Expired   bool   `json:"expired"`
	CreatedAt string `json:"created_at,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Digest    string `json:"digest,omitempty"`
}

func (c *Client) listArtifacts(ctx context.Context, a *actionsArguments) (*ArtifactList, error) {
	const op = "list workflow artifacts"
	var raw struct {
		TotalCount int `json:"total_count"`
		Artifacts  []struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			SizeInBytes int64  `json:"size_in_bytes"`
			Expired     bool   `json:"expired"`
			CreatedAt   string `json:"created_at"`
			ExpiresAt   string `json:"expires_at"`
			Digest      string `json:"digest"`
		} `json:"artifacts"`
	}
	if err := c.actionsRead(ctx, op, "runs/"+strconv.FormatInt(a.RunID, 10)+"/artifacts", a.query(), &raw); err != nil {
		return nil, err
	}
	result := &ArtifactList{Artifacts: make([]Artifact, 0, len(raw.Artifacts))}
	for _, artifact := range raw.Artifacts {
		if artifact.ID < 1 {
			return nil, invalidEntry(op, "an artifact")
		}
		result.Artifacts = append(result.Artifacts, Artifact{ID: artifact.ID, Name: artifact.Name,
			SizeBytes: artifact.SizeInBytes, Expired: artifact.Expired, CreatedAt: artifact.CreatedAt,
			ExpiresAt: artifact.ExpiresAt, Digest: artifact.Digest})
	}
	result.HasMore, result.NextCursor = a.more(len(raw.Artifacts), raw.TotalCount)
	return result, nil
}

// JobLog is the end of one job log.
type JobLog struct {
	JobID     int64  `json:"job_id"`
	Log       string `json:"log"`
	Lines     int    `json:"lines"`
	Truncated bool   `json:"truncated"`
}

// jobLog reads the end of one job log. GitHub answers the log route with a redirect to a short-lived signed
// address, usually on a storage host of its own. That address is fetched without the token and without any
// other GitHub header, asks only for the last maxBytes, and is read within hard bounds; a further redirect
// is not followed. The log passes through memory into the answer only.
func (c *Client) jobLog(ctx context.Context, id int64, lines, maxBytes int) (*JobLog, error) {
	const op = "read job log"
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, &provider.Error{Class: provider.ClassTimeout, Op: op,
			Message: "the request ended while it waited for the GitHub rate limit"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.endpoints.rest+c.actionsPath("jobs/"+strconv.FormatInt(id, 10)+"/logs"), nil)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	c.authorize(req)
	response, err := c.http.Do(req)
	if err != nil {
		return nil, provider.Transport(op, "GitHub", err)
	}
	defer response.Body.Close()
	c.observeRateLimit(response.Header)

	switch {
	case response.StatusCode >= 300 && response.StatusCode < 400 && response.Header.Get("Location") != "":
		location, err := response.Location()
		if err != nil || location.Scheme != "https" || location.Host == "" || location.User != nil {
			return nil, providerError(op, "GitHub pointed to a log address Qatlas does not read")
		}
		download, err := http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
		if err != nil {
			return nil, providerError(op, "the request could not be built")
		}
		download.Header.Set("User-Agent", "qatlas-cli")
		download.Header.Set("Range", "bytes=-"+strconv.Itoa(maxBytes))
		stored, err := c.http.Do(download)
		if err != nil {
			return nil, provider.Transport(op, "the GitHub log storage", err)
		}
		defer stored.Body.Close()
		response = stored
	case response.StatusCode < 200 || response.StatusCode > 299:
		return nil, actionsFailure(c.statusError(op, response, false), actionsReadPermission)
	}

	data, cut, err := readTail(op, response, maxBytes)
	if err != nil {
		return nil, err
	}
	return excerpt(id, data, cut, lines), nil
}

// readTail reads at most maxBytes from the end of a log answer and reports whether earlier content was left
// out. A storage that serves the requested range answers 206; one that ignores it sends the whole log,
// which is read up to maxLogDownload with only its end kept.
func readTail(op string, response *http.Response, maxBytes int) ([]byte, bool, error) {
	switch response.StatusCode {
	case http.StatusRequestedRangeNotSatisfiable:
		return nil, false, nil
	case http.StatusPartialContent:
		data, err := io.ReadAll(io.LimitReader(response.Body, int64(maxBytes)+1))
		if err != nil || len(data) > maxBytes {
			return nil, false, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "the job log could not be read within the size limit"}
		}
		start, ok := strings.CutPrefix(response.Header.Get("Content-Range"), "bytes ")
		start, _, _ = strings.Cut(start, "-")
		return data, !ok || start != "0", nil
	case http.StatusOK:
		body := io.LimitReader(response.Body, maxLogDownload+1)
		tail := make([]byte, 0, 2*maxBytes)
		chunk := make([]byte, 32<<10)
		total := 0
		for {
			n, err := body.Read(chunk)
			total += n
			tail = append(tail, chunk[:n]...)
			if len(tail) > maxBytes {
				tail = append(tail[:0], tail[len(tail)-maxBytes:]...)
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, false, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
					Message: "the job log could not be read"}
			}
		}
		if total > maxLogDownload {
			return nil, false, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: fmt.Sprintf("the job log is larger than the %d MiB Qatlas reads when its storage does "+
					"not serve the end alone", maxLogDownload>>20)}
		}
		return tail, total > len(tail), nil
	}
	return nil, false, providerError(op, fmt.Sprintf("the GitHub log storage refused the download (HTTP %d)",
		response.StatusCode))
}

// ansiEscape matches the terminal color and cursor sequences runners write into logs.
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// excerpt turns the end of a log into at most lines lines of plain text. Invalid UTF-8, terminal sequences,
// and control characters other than tab and line feed are removed, so no binary content reaches the answer.
func excerpt(id int64, data []byte, cut bool, lines int) *JobLog {
	text := ansiEscape.ReplaceAllString(strings.ToValidUTF8(string(data), "�"), "")
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0xfeff {
			return -1
		}
		return r
	}, text)
	if i := strings.IndexByte(text, '\n'); cut && i >= 0 {
		text = text[i+1:]
	}
	text = strings.TrimRight(text, "\n")
	result := &JobLog{JobID: id, Truncated: cut}
	if text == "" {
		return result
	}
	all := strings.Split(text, "\n")
	if len(all) > lines {
		all, result.Truncated = all[len(all)-lines:], true
	}
	result.Log, result.Lines = strings.Join(all, "\n"), len(all)
	return result
}

// Dispatch is the answer to an accepted workflow dispatch.
type Dispatch struct {
	WorkflowID int64  `json:"workflow_id"`
	Path       string `json:"path"`
	Ref        string `json:"ref"`
	Accepted   bool   `json:"accepted"`
}

// dispatchInput is one input a workflow declares for workflow_dispatch.
type dispatchInput struct {
	Required bool     `yaml:"required"`
	Type     string   `yaml:"type"`
	Default  any      `yaml:"default"`
	Options  []string `yaml:"options"`
}

// dispatchInputs reads the workflow_dispatch trigger of one workflow file. ok is false when the file declares
// no such trigger.
func dispatchInputs(file []byte) (inputs map[string]dispatchInput, ok bool, err error) {
	var workflow struct {
		On yaml.Node `yaml:"on"`
	}
	if err := yaml.Unmarshal(file, &workflow); err != nil {
		return nil, false, err
	}
	on := workflow.On
	switch on.Kind {
	case yaml.ScalarNode:
		return nil, on.Value == "workflow_dispatch", nil
	case yaml.SequenceNode:
		for _, event := range on.Content {
			if event.Kind == yaml.ScalarNode && event.Value == "workflow_dispatch" {
				return nil, true, nil
			}
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(on.Content); i += 2 {
			if on.Content[i].Value != "workflow_dispatch" {
				continue
			}
			var trigger struct {
				Inputs map[string]dispatchInput `yaml:"inputs"`
			}
			if err := on.Content[i+1].Decode(&trigger); err != nil {
				return nil, false, err
			}
			return trigger.Inputs, true, nil
		}
	}
	return nil, false, nil
}

// checkDispatchInputs compares the given inputs with those the workflow declares: every given input must be
// declared and fit its type, and every required input without a default must be given.
func checkDispatchInputs(declared map[string]dispatchInput, given map[string]string) error {
	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}
	sort.Strings(names)
	known := "none"
	if len(names) > 0 {
		known = strings.Join(names, ", ")
	}
	provided := make([]string, 0, len(given))
	for name := range given {
		provided = append(provided, name)
	}
	sort.Strings(provided)
	for _, name := range provided {
		input, ok := declared[name]
		value := given[name]
		switch {
		case !ok:
			return invalidRequest("input " + name + " is not declared for workflow_dispatch by the workflow at " +
				"this ref; declared inputs: " + known)
		case input.Type == "boolean" && value != "true" && value != "false":
			return invalidRequest("input " + name + " takes true or false")
		case input.Type == "number":
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				return invalidRequest("input " + name + " takes a number")
			}
		case input.Type == "choice" && !contains(input.Options, value):
			return invalidRequest("input " + name + " takes one of its options: " + strings.Join(input.Options, ", "))
		}
	}
	for _, name := range names {
		if _, ok := given[name]; !ok && declared[name].Required && declared[name].Default == nil {
			return invalidRequest("input " + name + " is required by the workflow")
		}
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// dispatchWorkflow starts one workflow_dispatch run. The workflow and its file at ref are read first, so a
// workflow without the trigger or inputs it does not declare are refused before the one dispatch request,
// which is never repeated.
func (c *Client) dispatchWorkflow(ctx context.Context, workflow, ref string, inputs map[string]string) (*Dispatch, error) {
	const op = "dispatch workflow"
	flow, err := c.workflow(ctx, op, workflow)
	if err != nil {
		return nil, err
	}
	if flow.State != "active" {
		return nil, providerError(op, "the workflow is not active, so GitHub does not run it")
	}
	if !strings.HasPrefix(flow.Path, workflowsDir) {
		return nil, providerError(op, "the workflow has no workflow file that can be dispatched")
	}
	segments := strings.Split(flow.Path, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	var file struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
		Size     int    `json:"size"`
	}
	path := c.repoPath("contents/" + strings.Join(segments, "/") + "?ref=" + url.QueryEscape(ref))
	if err := c.rest(ctx, op, path, &file); err != nil {
		return nil, actionsFailure(err, actionsFilePermission)
	}
	if file.Type != "file" || file.Encoding != "base64" || file.Size > maxWorkflowFile {
		return nil, providerError(op, "the workflow file at this ref could not be read")
	}
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if err != nil || len(data) > maxWorkflowFile {
		return nil, invalidResponse(op, false)
	}
	declared, ok, err := dispatchInputs(data)
	if err != nil {
		return nil, providerError(op, "the workflow file at this ref is not a readable workflow")
	}
	if !ok {
		return nil, invalidRequest("the workflow does not declare workflow_dispatch at this ref")
	}
	if err := checkDispatchInputs(declared, inputs); err != nil {
		return nil, err
	}
	body := map[string]any{"ref": ref}
	if len(inputs) > 0 {
		body["inputs"] = inputs
	}
	if err := c.restChange(ctx, op, http.MethodPost,
		c.actionsPath("workflows/"+strconv.FormatInt(flow.ID, 10)+"/dispatches"), body, nil); err != nil {
		return nil, actionsFailure(err, actionsChangePermission)
	}
	return &Dispatch{WorkflowID: flow.ID, Path: flow.Path, Ref: ref, Accepted: true}, nil
}

// RunChange is the answer to an accepted re-run or cancel request.
type RunChange struct {
	RunID    int64 `json:"run_id"`
	Accepted bool  `json:"accepted"`
}

// changeRun re-runs or cancels one run of the bound repository. The run is read first: GitHub re-runs only a
// completed run and cancels only one that has not completed, and its refusal of either would otherwise read
// like a missing permission. The change itself is one request that is never repeated.
func (c *Client) changeRun(ctx context.Context, op string, id int64, action string) (*RunChange, error) {
	run, err := c.run(ctx, op, id)
	if err != nil {
		return nil, err
	}
	completed := run.Status == "completed"
	switch {
	case action == "cancel" && completed:
		return nil, providerError(op, "the run has already completed, so there is nothing to cancel")
	case action != "cancel" && !completed:
		return nil, providerError(op, "the run has not completed yet; GitHub re-runs only a completed run")
	}
	if err := c.restChange(ctx, op, http.MethodPost, c.actionsPath("runs/"+strconv.FormatInt(id, 10)+"/"+action),
		struct{}{}, nil); err != nil {
		return nil, actionsFailure(err, actionsChangePermission)
	}
	return &RunChange{RunID: id, Accepted: true}, nil
}
