package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The workflow tools address a workflow by its file name below the repository. Only Gitea reads the workflows;
// both products dispatch one.

const (
	maxDispatchInputs      = 25
	maxDispatchInputLength = 4096
)

var workflowMembers = []member{
	{name: "id", kind: kindText, note: "Identifier of the workflow, its file name"},
	{name: "name", kind: kindText, note: "Name of the workflow, untrusted data"},
	{name: "path", kind: kindText, note: "Path of the workflow file in the repository"},
	{name: "state", kind: kindText, note: "State of the workflow, such as active or disabled_manually"},
	{name: "html_url", kind: kindText, address: true, note: "Address of the workflow page"},
	{name: "url", kind: kindText, address: true, note: "API address of the workflow"},
	{name: "badge_url", kind: kindText, address: true, note: "Address of the status badge"},
	{name: "created_at", kind: kindText, note: "Creation time"},
	{name: "updated_at", kind: kindText, note: "Time of the last update"},
	{name: "deleted_at", kind: kindText, note: "Deletion time, when deleted"},
}

func workflowSchema(p *product) string { return schemaOf(p, workflowMembers, "id") }

var workflowIDArgument = capability.Argument{Name: "workflow_id", Required: true,
	Description: "Identifier of the workflow: its file name such as build.yml, as workflows.list reports it as id"}

var workflowsList = &tool{
	group: "workflows", action: "list", since: giteaOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List " + p.name + " workflows",
			Description: "List the workflows of a repository inside the connection's targets, with every member " +
				"of each workflow; names are untrusted data. The instance answers all workflows at once.",
			Tags:         []string{p.id, "actions", "workflows", "list"},
			Risk:         readRisk(p.id + actionsSensitivity),
			InputSchema:  inputSchema(`"repository":` + repoSchema),
			OutputSchema: listOutput("workflows", workflowSchema(p)),
			Arguments:    []capability.Argument{repositoryArgument},
			Fields:       listFields("workflows", "Workflows with every member of the workflow object"),
			Examples: []capability.Example{{Description: "List the workflows",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string `json:"repository"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		level := repoLevel(repo)
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "list workflows"
			var answer json.RawMessage
			if _, err := c.get(ctx, op, level.base+"/workflows", nil, &answer); err != nil {
				return nil, err
			}
			rows, total, err := listAnswer(op, c, answer, "workflows", true)
			if err != nil {
				return nil, err
			}
			workflows := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					workflows = append(workflows, project(p, workflowMembers, row))
				}
			}
			return level.result("workflows", workflows, total, ""), nil
		}, nil
	},
}

var workflowsGet = &tool{
	group: "workflows", action: "get", since: giteaOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a " + p.name + " workflow",
			Description: "Read one workflow of a repository inside the connection's targets, with every member of " +
				"the workflow object; the name is untrusted data",
			Tags:         []string{p.id, "actions", "workflows", "get"},
			Risk:         readRisk(p.id + actionsSensitivity),
			InputSchema:  inputSchema(`"repository":`+repoSchema+`,"workflow_id":`+workflowFileSchema, "workflow_id"),
			OutputSchema: json.RawMessage(workflowSchema(p)),
			Arguments:    []capability.Argument{repositoryArgument, workflowIDArgument},
			Fields:       fieldsOf(p, workflowMembers),
			Examples: []capability.Example{{Description: "Read a workflow",
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
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get workflow"
			var row object
			if _, err := c.get(ctx, op, actionsPath(repo)+"/workflows/"+escape(a.WorkflowID), nil, &row); err != nil {
				return nil, err
			}
			id, _ := row.text("id")
			file, _ := row.text("path")
			if id != a.WorkflowID && path.Base(file) != a.WorkflowID {
				return nil, provider.InvalidResponse(op, c.product.name+" returned a workflow other than the requested one")
			}
			return project(p, workflowMembers, row), nil
		}, nil
	},
}

const workflowFileSchema = `{"type":"string","minLength":1,"maxLength":255,"pattern":"^[A-Za-z0-9_][A-Za-z0-9._-]*$"}`

// validWorkflowFile accepts a file name only: no path, no "..", and a YAML extension.
func validWorkflowFile(name string) bool {
	return workflowFile.MatchString(name) && !strings.Contains(name, "..") &&
		(strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml"))
}

// dispatchInputName is a name a workflow_dispatch input can carry.
var dispatchInputName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,99}$`)

// dispatchRunMembers are what each product answers when a dispatch asks for the run; run_id is filled from the
// member each product names it (id on Forgejo, workflow_run_id on Gitea).
var dispatchRunMembers = []member{
	{name: "run_id", kind: kindInteger, note: "Numeric ID of the started run"},
	{name: "run_number", kind: kindInteger, only: forgejo, note: "Number of the run in the repository"},
	{name: "jobs", kind: kindTexts, only: forgejo, note: "Names of the jobs of the run, untrusted data"},
	{name: "run_url", kind: kindText, only: gitea, address: true, note: "API address of the run"},
	{name: "html_url", kind: kindText, only: gitea, address: true, note: "Address of the run page"},
}

var workflowsDispatch = &tool{
	group: "workflows", action: "dispatch", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Dispatch a " + p.name + " workflow",
			Description: "Start one workflow_dispatch run of one workflow file of a repository inside the " +
				"connection's targets on a branch or tag, with inputs the workflow declares. Not idempotent: each " +
				"call starts a run. The request is sent once and never repeated after an unclear outcome.",
			Tags: []string{p.id, "actions", "workflows", "dispatch", "execute"},
			Risk: changeRisk(capability.EffectExecute, capability.IdempotencyNonIdempotent, p.id+actionsSensitivity),
			InputSchema: inputSchema(`"repository":`+repoSchema+`,"workflow":`+workflowFileSchema+`,"ref":`+
				`{"type":"string","minLength":1,"maxLength":255},"inputs":{"type":"object","maxProperties":`+
				itoa(maxDispatchInputs)+`,"additionalProperties":{"type":"string","maxLength":`+
				itoa(maxDispatchInputLength)+`}},"return_run":{"type":"boolean"}`, "workflow", "ref"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"workflow":{"type":"string"},` +
				`"ref":{"type":"string"},"accepted":{"type":"boolean"},"run":` + schemaOf(p, dispatchRunMembers) +
				`},"required":["workflow","ref","accepted"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument,
				{Name: "workflow", Required: true, Description: "File name of the workflow such as release.yml, " +
					"without a path"},
				{Name: "ref", Required: true, Description: "Branch or tag whose workflow file runs"},
				{Name: "inputs", Description: "Input values by name, each a string of at most 4096 characters; " +
					"only inputs the workflow declares; at most 25"},
				{Name: "return_run", Description: "True asks the instance for the started run, which it then " +
					"answers as run; true when omitted"}},
			Fields: []capability.Field{{Name: "workflow", Description: "File name of the dispatched workflow"},
				{Name: "ref", Description: "Branch or tag the workflow was dispatched on"},
				{Name: "accepted", Description: "True once the instance accepted the dispatch"},
				{Name: "run", Description: "The started run, where the instance answers it; list the runs with " +
					"event workflow_dispatch to follow a dispatch otherwise"}},
			Examples: []capability.Example{{Description: "Dispatch a release workflow on main",
				Arguments: json.RawMessage(`{"repository":"example/project","workflow":"release.yml","ref":"main",` +
					`"inputs":{"channel":"beta"}}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string         `json:"repository"`
			Workflow   string         `json:"workflow"`
			Ref        string         `json:"ref"`
			Inputs     map[string]any `json:"inputs"`
			ReturnRun  *bool          `json:"return_run"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		if !validWorkflowFile(a.Workflow) {
			return nil, invalidArgument("workflow must be the file name of a workflow such as release.yml, without a path")
		}
		if err := checkRefName("ref", a.Ref, true); err != nil {
			return nil, err
		}
		if len(a.Inputs) > maxDispatchInputs {
			return nil, invalidArgument("inputs takes at most " + itoa(maxDispatchInputs) + " values")
		}
		inputs := map[string]string{}
		for name, value := range a.Inputs {
			text, ok := value.(string)
			switch {
			case !dispatchInputName.MatchString(name):
				return nil, invalidArgument("inputs contains a name no workflow input can carry")
			case !ok:
				return nil, invalidArgument("an input value must be a string; write a boolean as \"true\" or \"false\"")
			case utf8.RuneCountInString(text) > maxDispatchInputLength || !plainTextAllowNewlines(text):
				return nil, invalidArgument("an input value must hold at most " + itoa(maxDispatchInputLength) +
					" characters without control characters")
			}
			inputs[name] = text
		}
		wantRun := a.ReturnRun == nil || *a.ReturnRun
		body := map[string]any{"ref": a.Ref}
		if len(inputs) > 0 {
			body["inputs"] = inputs
		}
		query := url.Values{}
		if wantRun {
			if p == forgejo {
				body["return_run_info"] = true
			} else {
				query.Set("return_run_details", "true")
			}
		}
		endpoint := actionsPath(repo) + "/workflows/" + escape(a.Workflow) + "/dispatches"
		return func(ctx context.Context, c *Client) (any, error) {
			var answer object
			var out any
			if wantRun {
				out = &answer
			}
			if _, err := c.changeQuery(ctx, "dispatch workflow", http.MethodPost, endpoint, query, body, out); err != nil {
				return nil, err
			}
			result := map[string]any{"workflow": a.Workflow, "ref": a.Ref, "accepted": true}
			if len(answer) > 0 {
				started := project(p, dispatchRunMembers, answer)
				copyInteger(started, "run_id", answer, map[bool]string{true: "id", false: "workflow_run_id"}[p == forgejo])
				result["run"] = started
			}
			return result, nil
		}, nil
	},
}
