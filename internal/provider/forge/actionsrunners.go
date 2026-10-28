package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The runner tools read, pause, and remove the runners of a repository, an organization, or the user behind the
// token, on the same levels as the secrets and variables, and search the jobs that wait for a runner (Forgejo).
// The runner ID in the path binds a runner to its level, so no read comes first except the account proof of the
// user level. Registering a runner and the registration token are not offered: the projections below name every
// member they keep, and none of them is a credential.

const (
	runnersSensitivity = "-actions-runners"
	// maxRunnerRows bounds the rows of a list the instance answers without paging.
	maxRunnerRows   = 100
	maxRunnerLabels = 20
)

// runnerLabel is what one label of a runner can look like; the comma separates labels in the query, so it is
// never part of one.
var runnerLabel = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._:/@+=-]{0,99}$`)

const runnerLabelsSchema = `{"type":"array","maxItems":20,"items":{"type":"string","minLength":1,"maxLength":100,` +
	`"pattern":"^[A-Za-z0-9_][A-Za-z0-9._:/@+=-]{0,99}$"}}`

var giteaRunnerLabelMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the label"},
	{name: "name", kind: kindText, note: "Name of the label, untrusted data"},
	{name: "type", kind: kindText, note: "Type of the label"},
}

var actionsRunnerMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the runner"},
	{name: "name", kind: kindText, note: "Name of the runner, untrusted data"},
	{name: "status", kind: kindText, note: "Status of the runner, such as offline, idle, or active"},
	{name: "ephemeral", kind: kindFlag, note: "True for a runner that serves one job"},
	{name: "description", kind: kindText, only: forgejo, note: "Description of the runner, untrusted data"},
	{name: "uuid", kind: kindText, only: forgejo, note: "UUID of the runner"},
	{name: "version", kind: kindText, only: forgejo, note: "Version the runner reports about itself, untrusted data"},
	{name: "owner_id", kind: kindInteger, only: forgejo, note: "Numeric ID of the user or organization that owns the runner, 0 for a repository runner"},
	{name: "repo_id", kind: kindInteger, only: forgejo, note: "Numeric ID of the repository that owns the runner, 0 for another owner"},
	{name: "labels", kind: kindTexts, only: forgejo, note: "Labels of the runner, untrusted data"},
	{name: "busy", kind: kindFlag, only: gitea, note: "True while the runner runs a job"},
	{name: "disabled", kind: kindFlag, only: gitea, note: "True while the runner is paused"},
	{name: "labels", kind: kindObjects, sub: giteaRunnerLabelMembers, only: gitea, note: "Labels of the runner"},
}

func runnerSchema(p *product) string { return schemaOf(p, actionsRunnerMembers, "id") }

var runnerIDArgument = capability.Argument{Name: "runner_id", Required: true,
	Description: "Numeric ID of the runner, as actions-runners.list reports it as id; a runner of another level " +
		"is not found"}

const runnerLevelNote = " of a repository, an organization, or the user behind the token inside the connection's targets"

// runnerArguments are the arguments of every runner tool; the schema of a tool allows only its own.
type runnerArguments struct {
	Repository string   `json:"repository"`
	Owner      string   `json:"owner"`
	RunnerID   int64    `json:"runner_id"`
	Disabled   *bool    `json:"disabled"`
	Labels     []string `json:"labels"`
	Limit      int      `json:"limit"`
	Cursor     string   `json:"cursor"`
}

// readRunner decodes the arguments and chooses the level against the targets; a refusal comes before the token
// and before any request.
func readRunner(allowed allowlist, raw json.RawMessage, withID bool) (runnerArguments, actionsLevel, error) {
	var a runnerArguments
	if err := decodeArguments(raw, &a); err != nil {
		return a, actionsLevel{}, err
	}
	level, err := actionsLevelOf(allowed, a.Repository, a.Owner, true)
	if err != nil {
		return a, actionsLevel{}, err
	}
	if withID {
		if err := checkActionsID("runner_id", a.RunnerID); err != nil {
			return a, actionsLevel{}, err
		}
	}
	return a, level, nil
}

func (l actionsLevel) runnerResult(id int64, extra map[string]any) map[string]any {
	result := map[string]any{"runner_id": id}
	if l.repo {
		result["repository"] = l.name
	} else {
		result["owner"] = l.name
	}
	for key, value := range extra {
		result[key] = value
	}
	return result
}

func runnerChangeOutput(extra string, required string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"runner_id":{"type":"integer"},"repository":` +
		`{"type":"string"},"owner":{"type":"string"}` + extra + `},"required":["runner_id","` + required +
		`"],"additionalProperties":false}`)
}

// runnerRow reads the one runner an answer holds and checks that it is the runner asked for.
func runnerRow(p *product, c *Client, op string, row object, id int64) (map[string]any, error) {
	if got, ok := row.integer("id"); !ok || got != id {
		return nil, provider.InvalidResponse(op, c.product.name+" returned a runner other than the requested one")
	}
	return project(p, actionsRunnerMembers, row), nil
}

var actionsRunnersList = &tool{
	group: "actions-runners", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties := adminLevelProperties
		arguments := []capability.Argument{adminRepositoryArgument, adminOwnerArgument}
		description := "List one batch of the Actions runners" + runnerLevelNote + ", with every member of each " +
			"runner; names, descriptions, and labels are untrusted data. Only the runners the level owns are listed."
		if p == forgejo {
			properties += "," + pagingKeys
			arguments = append(arguments, pagingArguments...)
		} else {
			properties += `,"disabled":{"type":"boolean"}`
			arguments = append(arguments, capability.Argument{Name: "disabled", Description: "Gitea only: true lists " +
				"the paused runners, false the others; all when omitted"})
			description += " Gitea answers up to 100 runners at once and does not page."
		}
		return capability.Descriptor{
			Title:        "List " + p.name + " Actions runners",
			Description:  description,
			Tags:         []string{p.id, "actions", "runners", "list"},
			Risk:         readRisk(p.id + runnersSensitivity),
			InputSchema:  inputSchema(properties),
			OutputSchema: listOutput("runners", runnerSchema(p)),
			Arguments:    arguments,
			Fields:       listFields("runners", "Runners with every member of the runner object"),
			Examples: []capability.Example{{Description: "List the runners of a repository",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		a, level, err := readRunner(allowed, raw, false)
		if err != nil {
			return nil, err
		}
		path := level.base + "/runners"
		const op = "list actions runners"
		if p == forgejo {
			if a.Disabled != nil {
				return nil, invalidArgument("disabled is a Gitea argument; Forgejo keeps no paused state")
			}
			page, err := startPaging(provider.CursorBinding(p.id, "actions-runners.list", level.name, path), a.Limit,
				a.Cursor)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				return listAdmin(ctx, c, op, level, path, "runners", actionsRunnerMembers, page)
			}, nil
		}
		if a.Limit != 0 || a.Cursor != "" {
			return nil, invalidArgument("limit and cursor are Forgejo arguments; Gitea does not page the runners")
		}
		query := url.Values{}
		if a.Disabled != nil {
			query.Set("disabled", strconv.FormatBool(*a.Disabled))
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := level.verify(ctx, c, op); err != nil {
				return nil, err
			}
			var answer json.RawMessage
			if _, err := c.get(ctx, op, path, query, &answer); err != nil {
				return nil, err
			}
			rows, total, err := listAnswer(op, c, answer, "runners", true)
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil && len(out) < maxRunnerRows {
					out = append(out, project(p, actionsRunnerMembers, row))
				}
			}
			result := level.result("runners", out, total, "")
			result["has_more"] = len(rows) > maxRunnerRows
			return result, nil
		}, nil
	},
}

var actionsRunnersGet = &tool{
	group: "actions-runners", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a " + p.name + " Actions runner",
			Description: "Read one Actions runner" + runnerLevelNote + ", with every member of the runner; names " +
				"and labels are untrusted data",
			Tags:         []string{p.id, "actions", "runners", "get"},
			Risk:         readRisk(p.id + runnersSensitivity),
			InputSchema:  inputSchema(adminLevelProperties+`,"runner_id":`+actionsSchemaID, "runner_id"),
			OutputSchema: json.RawMessage(runnerSchema(p)),
			Arguments:    []capability.Argument{adminRepositoryArgument, adminOwnerArgument, runnerIDArgument},
			Fields:       fieldsOf(p, actionsRunnerMembers),
			Examples: []capability.Example{{Description: "Read a runner",
				Arguments: json.RawMessage(`{"repository":"example/project","runner_id":7}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		a, level, err := readRunner(allowed, raw, true)
		if err != nil {
			return nil, err
		}
		path := level.base + "/runners/" + strconv.FormatInt(a.RunnerID, 10)
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get actions runner"
			if err := level.verify(ctx, c, op); err != nil {
				return nil, err
			}
			var row object
			if _, err := c.get(ctx, op, path, nil, &row); err != nil {
				return nil, err
			}
			return runnerRow(p, c, op, row, a.RunnerID)
		}, nil
	},
}

var actionsRunnersUpdate = &tool{
	group: "actions-runners", action: "update", since: giteaOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Pause or resume a " + p.name + " Actions runner",
			Description: "Pause (disabled true) or resume (disabled false) one Actions runner" + runnerLevelNote +
				"; a paused runner takes no new job. Nothing else of the runner changes. Idempotent; the request " +
				"is sent once and never repeated after an unclear outcome, and the result carries the runner as " +
				"the instance answers it",
			Tags:         []string{p.id, "actions", "runners", "update"},
			Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+runnersSensitivity),
			InputSchema:  inputSchema(adminLevelProperties+`,"runner_id":`+actionsSchemaID+`,"disabled":{"type":"boolean"}`, "runner_id", "disabled"),
			OutputSchema: runnerChangeOutput(`,"runner":`+runnerSchema(p), "runner"),
			Arguments: []capability.Argument{adminRepositoryArgument, adminOwnerArgument, runnerIDArgument,
				{Name: "disabled", Required: true, Description: "True pauses the runner, false resumes it"}},
			Fields: []capability.Field{{Name: "runner_id", Description: "ID of the runner"},
				{Name: "repository", Description: "Repository the tool acted on, as OWNER/REPO"},
				{Name: "owner", Description: "Owner the tool acted on, on the owner level"},
				{Name: "runner", Description: "The runner as the instance answers it"}},
			Examples: []capability.Example{{Description: "Pause a runner",
				Arguments: json.RawMessage(`{"repository":"example/project","runner_id":7,"disabled":true}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		a, level, err := readRunner(allowed, raw, true)
		if err != nil {
			return nil, err
		}
		if a.Disabled == nil {
			return nil, invalidArgument("disabled is required")
		}
		path, body := level.base+"/runners/"+strconv.FormatInt(a.RunnerID, 10), map[string]any{"disabled": *a.Disabled}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "update actions runner"
			if err := level.verify(ctx, c, op); err != nil {
				return nil, err
			}
			var row object
			if _, err := c.changeQuery(ctx, op, http.MethodPatch, path, nil, body, &row); err != nil {
				return nil, err
			}
			runner, err := runnerRow(p, c, op, row, a.RunnerID)
			if err != nil {
				return nil, err
			}
			return level.runnerResult(a.RunnerID, map[string]any{"runner": runner}), nil
		}, nil
	},
}

var actionsRunnersDelete = &tool{
	group: "actions-runners", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete a " + p.name + " Actions runner",
			Description: "Remove the registration of one Actions runner" + runnerLevelNote + " for good; the runner " +
				"takes no further job until it is registered again. Idempotent; the request is sent once and never " +
				"repeated after an unclear outcome. Offered only by a connection whose tools list names it, in no " +
				"profile",
			Tags:                  []string{p.id, "actions", "runners", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+runnersSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(adminLevelProperties+`,"runner_id":`+actionsSchemaID, "runner_id"),
			OutputSchema:          runnerChangeOutput(`,"deleted":{"type":"boolean"}`, "deleted"),
			Arguments:             []capability.Argument{adminRepositoryArgument, adminOwnerArgument, runnerIDArgument},
			Fields: []capability.Field{{Name: "runner_id", Description: "ID of the runner"},
				{Name: "repository", Description: "Repository the tool acted on, as OWNER/REPO"},
				{Name: "owner", Description: "Owner the tool acted on, on the owner level"},
				{Name: "deleted", Description: "True once the instance removed the runner"}},
			Examples: []capability.Example{{Description: "Remove a runner of an organization",
				Arguments: json.RawMessage(`{"owner":"orgs/example","runner_id":7}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		a, level, err := readRunner(allowed, raw, true)
		if err != nil {
			return nil, err
		}
		path := level.base + "/runners/" + strconv.FormatInt(a.RunnerID, 10)
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "delete actions runner"
			if err := level.verify(ctx, c, op); err != nil {
				return nil, err
			}
			if err := c.change(ctx, op, http.MethodDelete, path, nil, nil); err != nil {
				return nil, err
			}
			return level.runnerResult(a.RunnerID, map[string]any{"deleted": true}), nil
		}, nil
	},
}

var actionsRunnerJobsSearch = &tool{
	group: "actions-runner-jobs", action: "search", since: forgejoOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Search " + p.name + " jobs waiting for a runner",
			Description: "Search the jobs that wait for a runner" + runnerLevelNote + ", by the labels they ask for, " +
				"with every member of each job; names and labels are untrusted data. Forgejo answers up to 100 jobs " +
				"at once and does not page. On an owner level, jobs of a repository inside an exclusion are left out.",
			Tags:         []string{p.id, "actions", "runners", "jobs", "search"},
			Risk:         readRisk(p.id + runnersSensitivity),
			InputSchema:  inputSchema(adminLevelProperties + `,"labels":` + runnerLabelsSchema),
			OutputSchema: listOutput("jobs", jobSchema(p)),
			Arguments: []capability.Argument{adminRepositoryArgument, adminOwnerArgument,
				{Name: "labels", Description: "Labels the jobs ask for, at most 20, each of 1 to 100 letters, digits, " +
					"and . _ : / @ + = -; all waiting jobs when omitted"}},
			Fields: listFields("jobs", "Waiting jobs with every member of the job object"),
			Examples: []capability.Example{{Description: "Search the jobs that wait for a docker runner",
				Arguments: json.RawMessage(`{"repository":"example/project","labels":["docker"]}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		a, level, err := readRunner(allowed, raw, false)
		if err != nil {
			return nil, err
		}
		if len(a.Labels) > maxRunnerLabels {
			return nil, invalidArgument("labels takes at most " + itoa(maxRunnerLabels) + " labels")
		}
		for _, label := range a.Labels {
			if !runnerLabel.MatchString(label) {
				return nil, invalidArgument("a label must hold 1 to 100 letters, digits, and . _ : / @ + = -")
			}
		}
		query := url.Values{}
		if len(a.Labels) > 0 {
			query.Set("labels", strings.Join(a.Labels, ","))
		}
		path := level.base + "/runners/jobs"
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "search runner jobs"
			if err := level.verify(ctx, c, op); err != nil {
				return nil, err
			}
			var rows []object
			if _, err := c.get(ctx, op, path, query, &rows); err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row == nil || len(out) >= maxRunnerRows {
					continue
				}
				// An owner level shows the jobs of all its repositories; one that an exclusion takes out, or whose
				// repository is unknown, is left out.
				if len(allowed.excludes) > 0 && !level.repo {
					name, named := jobRepository(c.origin, row)
					if !level.admits(allowed, name, named) {
						continue
					}
				}
				out = append(out, project(p, workflowJobMembers, row))
			}
			result := level.result("jobs", out, nil, "")
			result["has_more"] = len(rows) > maxRunnerRows
			return result, nil
		}, nil
	},
}

// workflowSwitch is the enable or disable of one workflow, a PUT without a body that answers 204.
func workflowSwitch(action, verb, what string) *tool {
	return &tool{
		group: "workflows", action: action, since: giteaOnly,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title: verb + " a " + p.name + " workflow",
				Description: verb + " one workflow of a repository inside the connection's targets" + what +
					" Idempotent; the request is sent once and never repeated after an unclear outcome.",
				Tags:         []string{p.id, "actions", "workflows", action},
				Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+actionsSensitivity),
				InputSchema:  inputSchema(`"repository":`+repoSchema+`,"workflow_id":`+workflowFileSchema, "workflow_id"),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"workflow_id":{"type":"string"},"repository":{"type":"string"},"accepted":{"type":"boolean"}},"required":["workflow_id","accepted"],"additionalProperties":false}`),
				Arguments:    []capability.Argument{repositoryArgument, workflowIDArgument},
				Fields: []capability.Field{{Name: "workflow_id", Description: "File name of the workflow"},
					{Name: "repository", Description: "Repository the tool acted on, as OWNER/REPO"},
					{Name: "accepted", Description: "True once the instance accepted the request"}},
				Examples: []capability.Example{{Description: verb + " a workflow",
					Arguments: json.RawMessage(`{"repository":"example/project","workflow_id":"build.yml"}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a struct {
				Repository string `json:"repository"`
				WorkflowID string `json:"workflow_id"`
			}
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			repo, err := allowed.chooseRepository(a.Repository)
			if err != nil {
				return nil, err
			}
			if !validWorkflowFile(a.WorkflowID) {
				return nil, invalidArgument("workflow_id must be the name of a workflow file such as build.yml")
			}
			path := actionsPath(repo) + "/workflows/" + escape(a.WorkflowID) + "/" + action
			return func(ctx context.Context, c *Client) (any, error) {
				if err := c.change(ctx, action+" workflow", http.MethodPut, path, nil, nil); err != nil {
					return nil, err
				}
				return map[string]any{"workflow_id": a.WorkflowID, "repository": repo.owner + "/" + repo.repo,
					"accepted": true}, nil
			}, nil
		},
	}
}

var (
	workflowsEnable = workflowSwitch("enable", "Enable", "; the workflow can run again, started by its events "+
		"and by a dispatch.")
	workflowsDisable = workflowSwitch("disable", "Disable", "; the workflow no longer runs, neither started by "+
		"its events nor by a dispatch, until it is enabled again.")
)
