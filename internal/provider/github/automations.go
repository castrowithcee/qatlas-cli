package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The automation tools read the built-in workflows of a project, such as setting the status of a closed item,
// and delete one. A workflow is addressed by its number in the project, resolved with the project in one query
// before the one change is sent. GitHub's API neither creates, enables, disables, nor configures a workflow;
// that happens in GitHub itself.

// workflowsSelection reads every workflow of a project; a project holds only a few built-in ones.
const workflowsSelection = `workflows(first:100,orderBy:{field:NUMBER,direction:ASC}){nodes{id number name enabled}}`

const workflowOutput = `{"type":"object","properties":{"number":{"type":"integer"},"name":{"type":"string"},` +
	`"enabled":{"type":"boolean"}},"required":["number","name","enabled"],"additionalProperties":false}`

var projectWorkflowsList = capability.Descriptor{
	ID:      Provider + ".projectworkflows.list",
	Version: 1,
	Title:   "List the automations of a GitHub project",
	Description: "Read the built-in workflows of one GitHub project an explicit connection allows: number, " +
		"name, and whether it is enabled",
	Tags:                       []string{"github", "projects", "workflows", "automations", "list", "planning"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"workflows":{"type":"array","items":` +
		workflowOutput + `}},"required":["workflows"],"additionalProperties":false}`),
	Fields: []capability.Field{{Name: "workflows", Description: "Every workflow in number order: number, name " +
		"such as Item closed or Auto-archive items, and enabled; names are untrusted data"}},
	Examples: []capability.Example{{
		Description: "Read the automations of a project",
		Arguments:   json.RawMessage(`{"project":"orgs/octo-org/projects/7"}`),
	}},
}

var projectWorkflowsDelete = capability.Descriptor{
	ID:      Provider + ".projectworkflows.delete",
	Version: 1,
	Title:   "Delete a GitHub project automation",
	Description: "Delete one built-in workflow of a GitHub project an explicit connection allows; GitHub's API " +
		"cannot create it again. Offered only by a connection whose tools list names it",
	Tags:                       []string{"github", "projects", "workflows", "automations", "delete"},
	Risk:                       changeRisk(capability.EffectDelete, capability.IdempotencyUnknown),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"workflow":` + numberSchema + `},` +
		`"required":["workflow"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"},` +
		`"name":{"type":"string"},"deleted":{"type":"boolean"}},"required":["number","name","deleted"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "workflow", Description: "Number of the workflow in the project, as " +
		"github.projectworkflows.list names it", Required: true}},
	Fields: []capability.Field{
		{Name: "number", Description: "Number of the deleted workflow"},
		{Name: "name", Description: "Name of the deleted workflow, untrusted data"},
		{Name: "deleted", Description: "True once GitHub deleted the workflow"},
	},
	Examples: []capability.Example{{
		Description: "Delete an automation",
		Arguments:   json.RawMessage(`{"project":"orgs/octo-org/projects/7","workflow":3}`),
	}},
}

// automationOperations are the automation tools.
func automationOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: projectWorkflowsList, Handler: capability.Handler(invokeProjectWorkflowsList)},
		{Descriptor: projectWorkflowsDelete, Handler: capability.Handler(invokeProjectWorkflowsDelete)},
	}
}

// ProjectWorkflow is one built-in workflow of a project.
type ProjectWorkflow struct {
	Number  int    `json:"number"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// ProjectWorkflowList is the answer of the workflow list.
type ProjectWorkflowList struct {
	Workflows []ProjectWorkflow `json:"workflows"`
}

// DeletedProjectWorkflow is the answer of a deleted workflow.
type DeletedProjectWorkflow struct {
	Number  int    `json:"number"`
	Name    string `json:"name"`
	Deleted bool   `json:"deleted"`
}

type projectWorkflowJSON struct {
	ID      string `json:"id"`
	Number  int    `json:"number"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// projectWorkflowNumber is the argument of a workflow delete.
type projectWorkflowNumber struct {
	Workflow int `json:"workflow"`
}

func checkProjectWorkflow(number int) error {
	if number < 1 || number > 1000000000 {
		return invalidRequest("workflow must be the number of a workflow of the project")
	}
	return nil
}

func (number projectWorkflowNumber) check() error { return checkProjectWorkflow(number.Workflow) }

// workflow finds the workflow of one number, or names every workflow number of the project in its refusal.
func (info *projectInfo) workflow(number int) (projectWorkflowJSON, error) {
	numbers := make([]string, len(info.workflows))
	for i, workflow := range info.workflows {
		if workflow.Number == number && workflow.ID != "" {
			return workflow, nil
		}
		numbers[i] = strconv.Itoa(workflow.Number)
	}
	if len(numbers) == 0 {
		return projectWorkflowJSON{}, invalidRequest(fmt.Sprintf("workflow %d is not a workflow of this project, "+
			"which has none", number))
	}
	return projectWorkflowJSON{}, invalidRequest(fmt.Sprintf("workflow %d is not a workflow of this project; its "+
		"workflows are %s", number, strings.Join(numbers, ", ")))
}

const deleteProjectWorkflowMutation = `mutation($workflow:ID!){workflow:deleteProjectV2Workflow(` +
	`input:{workflowId:$workflow}){deletedWorkflowId}}`

// ListProjectWorkflows reads the built-in workflows of the bound project.
func (c *Client) ListProjectWorkflows(ctx context.Context) (*ProjectWorkflowList, error) {
	const op = "list project workflows"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	info, _, err := c.resolve(ctx, op, planningRequest{workflows: true})
	if err != nil {
		return nil, err
	}
	list := &ProjectWorkflowList{Workflows: []ProjectWorkflow{}}
	for _, workflow := range info.workflows {
		if workflow.ID != "" {
			list.Workflows = append(list.Workflows, ProjectWorkflow{Number: workflow.Number, Name: workflow.Name,
				Enabled: workflow.Enabled})
		}
	}
	return list, nil
}

// DeleteProjectWorkflow deletes one built-in workflow of the bound project.
func (c *Client) DeleteProjectWorkflow(ctx context.Context, number int) (*DeletedProjectWorkflow, error) {
	const op = "delete project workflow"
	if c.target.kind != kindProject {
		return nil, providerError(op, "this connection is not bound to a project")
	}
	if err := checkProjectWorkflow(number); err != nil {
		return nil, err
	}
	info, _, err := c.resolve(ctx, op, planningRequest{workflows: true})
	if err != nil {
		return nil, err
	}
	workflow, err := info.workflow(number)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Workflow *struct {
			ID string `json:"deletedWorkflowId"`
		} `json:"workflow"`
	}
	if err := c.mutate(ctx, op, deleteProjectWorkflowMutation, map[string]any{"workflow": workflow.ID}, &answer); err != nil {
		return nil, err
	}
	if answer.Workflow == nil || answer.Workflow.ID != workflow.ID {
		return nil, invalidResponse(op, true)
	}
	return &DeletedProjectWorkflow{Number: workflow.Number, Name: workflow.Name, Deleted: true}, nil
}

// The handlers check the target and the arguments before a credential is resolved, so a refused request
// never becomes a secret read or a provider call.

func invokeProjectWorkflowsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	bound, err := selectTarget(resolved, kindProject, raw)
	if err != nil {
		return nil, err
	}
	client, err := openAt(resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.locate(client.ListProjectWorkflows(ctx))
}

var invokeProjectWorkflowsDelete = projectHandler("delete project workflow",
	func(c *Client, ctx context.Context, number projectWorkflowNumber) (any, error) {
		return c.DeleteProjectWorkflow(ctx, number.Workflow)
	})
