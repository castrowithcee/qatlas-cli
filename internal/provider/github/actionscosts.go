package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The billable time of one workflow run and the deletion of its logs. Only usage numbers are read; a log
// archive or its download link is never requested, because a signed link acts like a short-lived credential.

// maxUsageJobRuns bounds the per-job entries answered for each runner operating system.
const maxUsageJobRuns = 500

const usageOutput = `{"type":"object","properties":{"run_id":{"type":"integer"},"run_duration_ms":{"type":"integer"},` +
	`"billable":{"type":"array","items":{"type":"object","properties":{"os":{"type":"string"},` +
	`"total_ms":{"type":"integer"},"jobs":{"type":"integer"},"job_runs":{"type":"array","items":{"type":"object",` +
	`"properties":{"job_id":{"type":"integer"},"duration_ms":{"type":"integer"}},` +
	`"required":["job_id","duration_ms"],"additionalProperties":false}}},` +
	`"required":["os","total_ms","jobs"],"additionalProperties":false}},"truncated":{"type":"boolean"}},` +
	`"required":["run_id","billable","truncated"],"additionalProperties":false}`

var runsUsage = capability.Descriptor{
	ID:      Provider + ".workflowruns.usage",
	Version: 1,
	Title:   "Get the billable time of a GitHub Actions run",
	Description: "Read the billable time of one workflow run of a repository an explicit connection allows, " +
		"in milliseconds for each GitHub-hosted runner operating system, re-runs included, without the " +
		"multiplier of macOS and Windows runners; GitHub is closing this endpoint down",
	Tags:                       []string{"github", "actions", "runs", "usage", "billing", "get"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"run_id":`+actionsIDSchema, "run_id"),
	OutputSchema:               json.RawMessage(usageOutput),
	Arguments:                  []capability.Argument{{Name: "run_id", Description: "Workflow run identifier", Required: true}},
	Fields: []capability.Field{
		{Name: "run_duration_ms", Description: "Duration of the whole run in milliseconds, absent when GitHub gives none"},
		{Name: "billable", Description: "Entries for UBUNTU, MACOS, and WINDOWS that GitHub reports: os, total_ms, " +
			"jobs, and job_runs with job_id and duration_ms"},
		{Name: "truncated", Description: "True when job_runs of an operating system were cut at 500 entries"},
	},
	Examples: []capability.Example{{Description: "Read the billable time of a run", Arguments: json.RawMessage(`{"run_id":30433642}`)}},
}

var runLogsDelete = capability.Descriptor{
	ID:      Provider + ".workflowrunlogs.delete",
	Version: 1,
	Title:   "Delete the logs of a GitHub Actions run",
	Description: "Delete all logs of one workflow run of a repository an explicit connection allows; the logs " +
		"cannot be restored. A run whose logs are already gone counts as done. Offered only by a connection " +
		"whose tools list names it",
	Tags:                       []string{"github", "actions", "runs", "logs", "delete"},
	Risk:                       guardedRisk(capability.EffectDelete, capability.IdempotencyIdempotent, logSensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema:                inputSchema(`"run_id":`+actionsIDSchema, "run_id"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"run_id":{"type":"integer"},` +
		`"deleted":{"type":"boolean"},"already_deleted":{"type":"boolean"}},` +
		`"required":["run_id","deleted","already_deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "run_id", Description: "Workflow run identifier", Required: true}},
	Fields: []capability.Field{
		{Name: "deleted", Description: "True once no logs of the run remain"},
		{Name: "already_deleted", Description: "True when GitHub reported the logs as already gone and Qatlas deleted nothing"},
	},
	Examples: []capability.Example{{Description: "Delete the logs of a run", Arguments: json.RawMessage(`{"run_id":30433642}`)}},
}

// RunUsage is the billable time of one run.
type RunUsage struct {
	RunID         int64         `json:"run_id"`
	RunDurationMS *int64        `json:"run_duration_ms,omitempty"`
	Billable      []RunnerUsage `json:"billable"`
	Truncated     bool          `json:"truncated"`
}

// RunnerUsage is the billable time on one runner operating system.
type RunnerUsage struct {
	OS      string     `json:"os"`
	TotalMS int64      `json:"total_ms"`
	Jobs    int64      `json:"jobs"`
	JobRuns []JobUsage `json:"job_runs,omitempty"`
}

// JobUsage is the billable duration of one job.
type JobUsage struct {
	JobID      int64 `json:"job_id"`
	DurationMS int64 `json:"duration_ms"`
}

type runnerUsageJSON struct {
	TotalMS int64      `json:"total_ms"`
	Jobs    int64      `json:"jobs"`
	JobRuns []JobUsage `json:"job_runs"`
}

type runUsageJSON struct {
	Billable struct {
		Ubuntu  *runnerUsageJSON `json:"UBUNTU"`
		Macos   *runnerUsageJSON `json:"MACOS"`
		Windows *runnerUsageJSON `json:"WINDOWS"`
	} `json:"billable"`
	RunDurationMS *int64 `json:"run_duration_ms"`
}

func (c *Client) runUsage(ctx context.Context, id int64) (*RunUsage, error) {
	const op = "get workflow run usage"
	var raw runUsageJSON
	if err := c.actionsRead(ctx, op, "runs/"+strconv.FormatInt(id, 10)+"/timing", nil, &raw); err != nil {
		return nil, err
	}
	usage := &RunUsage{RunID: id, RunDurationMS: raw.RunDurationMS, Billable: []RunnerUsage{}}
	for _, entry := range []struct {
		os    string
		usage *runnerUsageJSON
	}{{"UBUNTU", raw.Billable.Ubuntu}, {"MACOS", raw.Billable.Macos}, {"WINDOWS", raw.Billable.Windows}} {
		if entry.usage == nil {
			continue
		}
		view := RunnerUsage{OS: entry.os, TotalMS: entry.usage.TotalMS, Jobs: entry.usage.Jobs, JobRuns: entry.usage.JobRuns}
		if len(view.JobRuns) > maxUsageJobRuns {
			view.JobRuns = view.JobRuns[:maxUsageJobRuns]
			usage.Truncated = true
		}
		usage.Billable = append(usage.Billable, view)
	}
	return usage, nil
}

// RunLogsDeleted is the answer of a log deletion.
type RunLogsDeleted struct {
	RunID          int64 `json:"run_id"`
	Deleted        bool  `json:"deleted"`
	AlreadyDeleted bool  `json:"already_deleted"`
}

// deleteRunLogs deletes the logs of one run of the bound repository. The run is read first, so a missing
// run stays a not-found of the run. GitHub documents 204 only; a not-found answer to the delete of a run that
// exists is read as logs that are already gone. The delete is one request that is never repeated.
func (c *Client) deleteRunLogs(ctx context.Context, id int64) (*RunLogsDeleted, error) {
	const op = "delete workflow run logs"
	if _, err := c.run(ctx, op, id); err != nil {
		return nil, err
	}
	err := c.restChange(ctx, op, http.MethodDelete, c.actionsPath("runs/"+strconv.FormatInt(id, 10)+"/logs"),
		struct{}{}, nil)
	if err == nil {
		return &RunLogsDeleted{RunID: id, Deleted: true}, nil
	}
	var failure *provider.Error
	if errors.As(err, &failure) && failure.Class == provider.ClassNotFound {
		return &RunLogsDeleted{RunID: id, Deleted: true, AlreadyDeleted: true}, nil
	}
	return nil, actionsFailure(err, actionsChangePermission)
}
