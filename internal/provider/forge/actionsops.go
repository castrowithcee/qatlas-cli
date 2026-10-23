package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The operating tools send exactly one request to a fixed path below the repository; the run or job ID in the
// path binds them to it, so no read comes first. A failure whose outcome is open, such as a timeout or a 5xx,
// is reported as uncertain and never repeated.

type runChangeSpec struct {
	action      string
	title       string
	summary     string
	suffix      string
	since       map[string]version
	idempotency capability.Idempotency
	// answers is true where Gitea answers with the run.
	answers bool
}

func acceptedOutput(p *product, answers bool, key, rowSchema, idKey string) json.RawMessage {
	schema := `{"type":"object","properties":{"` + idKey + `":{"type":"integer"},"accepted":{"type":"boolean"}`
	if answers && p == gitea {
		schema += `,"` + key + `":` + rowSchema
	}
	return json.RawMessage(schema + `},"required":["` + idKey + `","accepted"],"additionalProperties":false}`)
}

func acceptedFields(p *product, answers bool, key, idKey, what string) []capability.Field {
	fields := []capability.Field{{Name: idKey, Description: "ID of the " + what},
		{Name: "accepted", Description: "True once the instance accepted the request"}}
	if answers && p == gitea {
		fields = append(fields, capability.Field{Name: key, Description: "The " + what + " as the instance answers it"})
	}
	return fields
}

func runChangeTool(spec runChangeSpec) *tool {
	return &tool{
		group: "workflow-runs", action: spec.action, since: spec.since,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title:        spec.title + " a " + p.name + " workflow run",
				Description:  spec.summary + " The request is sent once and never repeated after an unclear outcome.",
				Tags:         []string{p.id, "actions", "runs", spec.action, "execute"},
				Risk:         changeRisk(capability.EffectExecute, spec.idempotency, p.id+actionsSensitivity),
				InputSchema:  inputSchema(runKeys(""), "run_id"),
				OutputSchema: acceptedOutput(p, spec.answers, "run", runSchema(p), "run_id"),
				Arguments:    []capability.Argument{repositoryArgument, runIDArgument},
				Fields:       acceptedFields(p, spec.answers, "run", "run_id", "run"),
				Examples: []capability.Example{{Description: spec.title + " a run",
					Arguments: json.RawMessage(`{"repository":"example/project","run_id":42}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			a, repo, err := readRunArguments(allowed, raw, false)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				op := spec.action + " workflow run"
				var answer object
				var out any
				if spec.answers && p == gitea {
					out = &answer
				}
				if err := c.change(ctx, op, http.MethodPost, runPath(repo, a.RunID)+spec.suffix, nil, out); err != nil {
					return nil, err
				}
				result := map[string]any{"run_id": a.RunID, "accepted": true}
				if out != nil && answer != nil {
					result["run"] = projectRun(p, answer)
				}
				return result, nil
			}, nil
		},
	}
}

var (
	workflowRunsCancel = runChangeTool(runChangeSpec{action: "cancel", title: "Cancel", suffix: "/cancel",
		summary:     "Cancel one running or waiting workflow run of a repository inside the connection's targets.",
		since:       map[string]version{forgejo.id: forgejoActions16, gitea.id: giteaActions28},
		idempotency: capability.IdempotencyIdempotent, answers: true})
	workflowRunsForceCancel = runChangeTool(runChangeSpec{action: "force-cancel", title: "Force-cancel",
		summary: "Force-cancel one workflow run of a repository inside the connection's targets without waiting " +
			"for its jobs to stop; use it for a run that cannot be cancelled otherwise.",
		suffix: "/force-cancel", since: map[string]version{gitea.id: giteaActions28},
		idempotency: capability.IdempotencyIdempotent, answers: true})
	workflowRunsRerun = runChangeTool(runChangeSpec{action: "rerun", title: "Rerun",
		summary: "Start another attempt of one finished workflow run of a repository inside the connection's " +
			"targets. Not idempotent: each call starts an attempt.",
		suffix: "/rerun", since: map[string]version{forgejo.id: forgejoActions17, gitea.id: gitea.minimum},
		idempotency: capability.IdempotencyNonIdempotent, answers: true})
	workflowRunsRerunFailed = runChangeTool(runChangeSpec{action: "rerun-failed", title: "Rerun the failed jobs of",
		summary: "Start another attempt of only the failed jobs of one finished workflow run of a repository " +
			"inside the connection's targets. Not idempotent: each call starts an attempt.",
		suffix: "/rerun-failed-jobs", since: giteaOnly, idempotency: capability.IdempotencyNonIdempotent})
	workflowRunsApprove = runChangeTool(runChangeSpec{action: "approve", title: "Approve",
		summary: "Approve one workflow run of a repository inside the connection's targets that waits for an " +
			"approval, such as a run triggered from a fork, and so lets it run with the repository's secrets.",
		suffix: "/approve", since: map[string]version{gitea.id: giteaActions28},
		idempotency: capability.IdempotencyIdempotent, answers: true})
)

var workflowRunsDelete = &tool{
	group: "workflow-runs", action: "delete", since: sinceRunsDelete,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete a " + p.name + " workflow run",
			Description: "Delete one workflow run of a repository inside the connection's targets for good, with " +
				"its jobs and logs; this cannot be undone. Only a connection whose tools list names it offers this tool.",
			Tags:                  []string{p.id, "actions", "runs", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+actionsSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(runKeys(""), "run_id"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"run_id":{"type":"integer"},` +
				`"deleted":{"type":"boolean"}},"required":["run_id","deleted"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, runIDArgument},
			Fields: []capability.Field{{Name: "run_id", Description: "ID of the run"},
				{Name: "deleted", Description: "True once the instance deleted the run"}},
			Examples: []capability.Example{{Description: "Delete a run",
				Arguments: json.RawMessage(`{"repository":"example/project","run_id":42}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		a, repo, err := readRunArguments(allowed, raw, false)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "delete workflow run", http.MethodDelete, runPath(repo, a.RunID), nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"run_id": a.RunID, "deleted": true}, nil
		}, nil
	},
}

var workflowJobsRerun = &tool{
	group: "workflow-jobs", action: "rerun", since: sinceJobsGet,
	describe: func(p *product) capability.Descriptor {
		properties := jobKeys("")
		required := []string{"job_id"}
		arguments := []capability.Argument{repositoryArgument, jobIDArgument}
		if p == gitea {
			properties = runKeys(`,"job_id":` + actionsSchemaID)
			required = []string{"run_id", "job_id"}
			arguments = []capability.Argument{repositoryArgument, runIDArgument, jobIDArgument}
		}
		return capability.Descriptor{
			Title: "Rerun a " + p.name + " workflow job",
			Description: "Start another attempt of one job of a repository inside the connection's targets and of " +
				"the jobs that depend on it. Not idempotent: each call starts an attempt. The request is sent once " +
				"and never repeated after an unclear outcome.",
			Tags:         []string{p.id, "actions", "jobs", "rerun", "execute"},
			Risk:         changeRisk(capability.EffectExecute, capability.IdempotencyNonIdempotent, p.id+actionsSensitivity),
			InputSchema:  inputSchema(properties, required...),
			OutputSchema: acceptedOutput(p, true, "job", jobSchema(p), "job_id"),
			Arguments:    arguments,
			Fields:       acceptedFields(p, true, "job", "job_id", "job"),
			Examples: []capability.Example{{Description: "Rerun a job",
				Arguments: json.RawMessage(`{"repository":"example/project","job_id":7` + map[bool]string{true: `,"run_id":42`}[p == gitea] + `}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a jobArguments
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		if err := checkActionsID("job_id", a.JobID); err != nil {
			return nil, err
		}
		path := jobPath(repo, a.JobID) + "/rerun"
		if p == gitea {
			if err := checkActionsID("run_id", a.RunID); err != nil {
				return nil, err
			}
			path = runPath(repo, a.RunID) + "/jobs/" + strconv.FormatInt(a.JobID, 10) + "/rerun"
		} else if a.RunID != 0 {
			return nil, invalidArgument("run_id is a Gitea argument; Forgejo names the job only")
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var answer object
			var out any
			if p == gitea {
				out = &answer
			}
			if err := c.change(ctx, "rerun workflow job", http.MethodPost, path, nil, out); err != nil {
				return nil, err
			}
			result := map[string]any{"job_id": a.JobID, "accepted": true}
			if out != nil && answer != nil {
				result["job"] = projectJob(p, answer)
			}
			return result, nil
		}, nil
	},
}
