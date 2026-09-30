package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Copilot in a repository: the coding agent assigned to an issue and the code review requested on a pull
// request. Both cover what the official GitHub MCP server offers as assign_copilot_to_issue,
// assign_copilot_to_issue_with_intent, and request_copilot_review, without its wait for the pull request the
// agent later opens: the answer reports the change and nothing more.

const (
	copilotAgentLogin    = "copilot-swe-agent"
	copilotReviewerLogin = "copilot-pull-request-reviewer[bot]"
	// copilotFeatures is the GraphQL feature flag GitHub requires for the agent assignment input.
	copilotFeatures    = "issues_copilot_assignment_api_support"
	maxInstructions    = 10000
	maxRationale       = 280
	maxActorPages      = 10
	maxKeptAssignees   = 100
	copilotUnavailable = "Copilot cannot be assigned in this repository: it is not among the actors that can be " +
		"assigned, so the account has no Copilot license with the coding agent or the repository does not allow it; " +
		"nothing was changed"
	copilotReviewUnavailable = "GitHub rejected the Copilot review request; Copilot code review may need a " +
		"Copilot license or may not be enabled for this repository; nothing was changed"
)

const confidenceSchema = `{"type":"string","enum":["LOW","MEDIUM","HIGH"]}`

const copilotAssignmentOutput = `{"type":"object","properties":{"number":{"type":"integer"},` +
	`"url":{"type":"string"},"is_suggestion":{"type":"boolean"}},` +
	`"required":["number","is_suggestion"],"additionalProperties":false}`

var copilotAssignmentsCreate = capability.Descriptor{
	ID:      Provider + ".copilotassignments.create",
	Version: 1,
	Title:   "Assign GitHub Copilot to an issue",
	Description: "Assign the Copilot coding agent to one issue of a repository an explicit connection allows, " +
		"keeping the issue's other assignees; optionally name the branch it starts from, extra instructions, and " +
		"the intent behind the choice (rationale and confidence), or record only a pending suggestion that does " +
		"not start the agent. A repository without Copilot as an assignable actor is refused and nothing changes",
	Tags:                       []string{"github", "issues", "copilot", "assignments", "create"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"number":`+numberSchema+`,"base_ref":`+refSchema+
		`,"custom_instructions":{"type":"string","maxLength":10000},"rationale":{"type":"string","maxLength":280},`+
		`"confidence":`+confidenceSchema+`,"is_suggestion":{"type":"boolean"}`, "number"),
	OutputSchema: json.RawMessage(copilotAssignmentOutput),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Issue number in the repository", Required: true},
		{Name: "base_ref", Description: "Branch the agent starts its work from; the default branch when absent"},
		{Name: "custom_instructions", Description: "Extra instructions for the agent beyond the issue body, at most 10000 characters"},
		{Name: "rationale", Description: "One sentence, at most 280 characters, on what led to choosing Copilot; needs confidence"},
		{Name: "confidence", Description: "LOW, MEDIUM, or HIGH; needs rationale"},
		{Name: "is_suggestion", Description: "true records a pending assignment that does not start the agent; needs rationale and confidence, and excludes base_ref and custom_instructions"},
	},
	Fields: []capability.Field{
		{Name: "number", Description: "Issue number"},
		{Name: "url", Description: "Address of the issue"},
		{Name: "is_suggestion", Description: "Whether only a pending suggestion was recorded"},
	},
	Examples: []capability.Example{{
		Description: "Assign Copilot to an issue",
		Arguments:   json.RawMessage(`{"number":42,"base_ref":"main","custom_instructions":"Add tests."}`),
	}},
}

var copilotReviewsRequest = capability.Descriptor{
	ID:      Provider + ".copilotreviews.request",
	Version: 1,
	Title:   "Request a GitHub Copilot review",
	Description: "Request Copilot as a reviewer of one pull request of a repository an explicit connection " +
		"allows; requesting it again leaves it requested. The answer is the pull request, as " +
		"github.pullrequests.get reports it",
	Tags:                       []string{"github", "pulls", "pullrequests", "copilot", "reviews", "request"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"number":`+numberSchema, "number"),
	OutputSchema:               pullsGet.OutputSchema,
	Arguments:                  []capability.Argument{{Name: "number", Description: "Pull request number in the repository", Required: true}},
	Fields:                     pullsGet.Fields,
	Examples: []capability.Example{{
		Description: "Request a Copilot review",
		Arguments:   json.RawMessage(`{"number":42}`),
	}},
}

// copilotTools are the tools of the not-recommended setup profile copilot.
var copilotTools = []string{copilotAssignmentsCreate.ID, copilotReviewsRequest.ID}

// copilotOperations binds the Copilot tools to their handlers.
func copilotOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: copilotAssignmentsCreate, Handler: copilotHandler(copilotAssignmentsCreate.ID,
			func(ctx context.Context, c *Client, a *copilotArguments) (any, error) { return c.assignCopilot(ctx, a) })},
		{Descriptor: copilotReviewsRequest, Handler: copilotHandler(copilotReviewsRequest.ID,
			func(ctx context.Context, c *Client, a *copilotArguments) (any, error) {
				return c.requestCopilotReview(ctx, a)
			})},
	}
}

type copilotArguments struct {
	Number             int    `json:"number"`
	BaseRef            string `json:"base_ref"`
	CustomInstructions string `json:"custom_instructions"`
	Rationale          string `json:"rationale"`
	Confidence         string `json:"confidence"`
	IsSuggestion       *bool  `json:"is_suggestion"`
}

func copilotHandler(id string, call func(context.Context, *Client, *copilotArguments) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments copilotArguments
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, unreadable(id)
		}
		bound, err := selectTarget(resolved, kindRepository, raw)
		if err != nil {
			return nil, err
		}
		if err := arguments.check(id); err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(call(ctx, client, &arguments))
	}
}

// intent reports whether the caller named any intent field, which selects the object form of the assignees.
func (a *copilotArguments) intent() bool {
	return a.Rationale != "" || a.Confidence != "" || a.IsSuggestion != nil
}

func (a *copilotArguments) suggestion() bool { return a.IsSuggestion != nil && *a.IsSuggestion }

func (a *copilotArguments) check(id string) error {
	if err := checkNumber(a.Number); err != nil {
		return err
	}
	if id == copilotReviewsRequest.ID {
		return nil
	}
	if a.BaseRef != "" && !validRef(a.BaseRef) {
		return invalidRequest("base_ref must be a branch name")
	}
	for name, value := range map[string]string{"custom_instructions": a.CustomInstructions, "rationale": a.Rationale} {
		if err := checkText(name, value); err != nil {
			return err
		}
	}
	if utf8.RuneCountInString(a.CustomInstructions) > maxInstructions {
		return invalidRequest("custom_instructions accepts at most 10000 characters")
	}
	if utf8.RuneCountInString(a.Rationale) > maxRationale {
		return invalidRequest("rationale accepts at most 280 characters")
	}
	if a.intent() {
		if a.Rationale == "" || a.Confidence == "" {
			return invalidRequest("name rationale and confidence together")
		}
		switch a.Confidence {
		case "LOW", "MEDIUM", "HIGH":
		default:
			return invalidRequest("confidence must be LOW, MEDIUM, or HIGH")
		}
	}
	if a.suggestion() && (a.BaseRef != "" || a.CustomInstructions != "") {
		return invalidRequest("base_ref and custom_instructions cannot be combined with is_suggestion")
	}
	return nil
}

// CopilotAssignment is the answer of an assignment.
type CopilotAssignment struct {
	Number       int    `json:"number"`
	URL          string `json:"url,omitempty"`
	IsSuggestion bool   `json:"is_suggestion"`
}

// copilotQuery reads the repository, the issue with its assignees, and one page of the actors that can be
// assigned, all of the bound repository, so no node id ever comes from an argument.
const copilotQuery = `query($owner:String!,$name:String!,$number:Int!,$after:String){` +
	`repository(owner:$owner,name:$name){id issue(number:$number){id ` +
	`assignees(first:100){pageInfo{hasNextPage} nodes{id}}} ` +
	`suggestedActors(first:100,after:$after,capabilities:[CAN_BE_ASSIGNED]){` +
	`pageInfo{hasNextPage endCursor} nodes{__typename ... on Bot{id login}}}}}`

const copilotMutation = `mutation($input:UpdateIssueInput!){updateIssue(input:$input){issue{number url}}}`

type copilotData struct {
	Repository *struct {
		ID    string `json:"id"`
		Issue *struct {
			ID        string `json:"id"`
			Assignees struct {
				PageInfo struct {
					HasNextPage bool `json:"hasNextPage"`
				} `json:"pageInfo"`
				Nodes []struct {
					ID string `json:"id"`
				} `json:"nodes"`
			} `json:"assignees"`
		} `json:"issue"`
		SuggestedActors struct {
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []struct {
				TypeName string `json:"__typename"`
				ID       string `json:"id"`
				Login    string `json:"login"`
			} `json:"nodes"`
		} `json:"suggestedActors"`
	} `json:"repository"`
}

// assignCopilot assigns Copilot to one issue of the bound repository in one mutation that is never repeated.
func (c *Client) assignCopilot(ctx context.Context, a *copilotArguments) (*CopilotAssignment, error) {
	const op = "assign copilot to an issue"
	var data copilotData
	var after any
	copilotID := ""
	for page := 0; page < maxActorPages && copilotID == ""; page++ {
		data = copilotData{}
		if err := c.graphql(ctx, op, copilotQuery, map[string]any{"owner": c.target.owner, "name": c.target.repo,
			"number": a.Number, "after": after}, &data); err != nil {
			return nil, err
		}
		repository := data.Repository
		if repository == nil {
			return nil, notFound(op, subject{in: c.target})
		}
		if repository.Issue == nil {
			return nil, notFound(op, subject{in: c.target, what: "issue #" + strconv.Itoa(a.Number)})
		}
		for _, actor := range repository.SuggestedActors.Nodes {
			if actor.TypeName == "Bot" && actor.Login == copilotAgentLogin && actor.ID != "" {
				copilotID = actor.ID
				break
			}
		}
		if !repository.SuggestedActors.PageInfo.HasNextPage || repository.SuggestedActors.PageInfo.EndCursor == "" {
			break
		}
		after = repository.SuggestedActors.PageInfo.EndCursor
	}
	if copilotID == "" {
		return nil, &provider.Error{Class: provider.ClassPermission, Op: op, Message: copilotUnavailable}
	}
	issue := data.Repository.Issue
	if issue.Assignees.PageInfo.HasNextPage || len(issue.Assignees.Nodes) > maxKeptAssignees {
		return nil, providerError(op, "the issue has more assignees than can be kept; nothing was changed")
	}

	input := map[string]any{"id": issue.ID}
	if a.intent() {
		assignees := make([]map[string]any, 0, len(issue.Assignees.Nodes)+1)
		for _, node := range issue.Assignees.Nodes {
			if node.ID != copilotID {
				assignees = append(assignees, map[string]any{"actorId": node.ID})
			}
		}
		assignees = append(assignees, map[string]any{"actorId": copilotID, "rationale": a.Rationale,
			"confidence": a.Confidence, "suggest": a.suggestion()})
		input["assignees"] = assignees
	} else {
		ids := make([]string, 0, len(issue.Assignees.Nodes)+1)
		for _, node := range issue.Assignees.Nodes {
			if node.ID != copilotID {
				ids = append(ids, node.ID)
			}
		}
		input["assigneeIds"] = append(ids, copilotID)
	}
	if !a.suggestion() {
		assignment := map[string]any{"customAgent": "", "customInstructions": a.CustomInstructions,
			"targetRepositoryId": data.Repository.ID}
		if a.BaseRef != "" {
			assignment["baseRef"] = a.BaseRef
		}
		input["agentAssignment"] = assignment
	}

	var result struct {
		UpdateIssue struct {
			Issue struct {
				Number int    `json:"number"`
				URL    string `json:"url"`
			} `json:"issue"`
		} `json:"updateIssue"`
	}
	ctx = context.WithValue(ctx, graphQLFeaturesKey{}, copilotFeatures)
	if err := c.mutate(ctx, op, copilotMutation, map[string]any{"input": input}, &result); err != nil {
		return nil, err
	}
	return &CopilotAssignment{Number: result.UpdateIssue.Issue.Number, URL: result.UpdateIssue.Issue.URL,
		IsSuggestion: a.suggestion()}, nil
}

const copilotReviewPermission = "GitHub refused this Copilot review request; it needs repo on a classic token, " +
	"or Pull requests: read and write on a fine-grained token, and write access to the repository"

// requestCopilotReview requests the Copilot reviewer bot on one pull request of the bound repository, in
// one request that is never repeated.
func (c *Client) requestCopilotReview(ctx context.Context, a *copilotArguments) (*PullRequest, error) {
	const op = "request a copilot review"
	var raw pullJSON
	err := c.restChange(ctx, op, http.MethodPost, c.repoPath("pulls/"+strconv.Itoa(a.Number)+"/requested_reviewers"),
		map[string]any{"reviewers": []string{copilotReviewerLogin}}, &raw)
	if err != nil {
		err = actionsFailure(err, copilotReviewPermission)
		var failure *provider.Error
		if errors.As(err, &failure) && failure.Message == rejectedMessage {
			return nil, providerError(op, copilotReviewUnavailable)
		}
		return nil, err
	}
	return raw.full(), nil
}
