package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Requested reviewers of a pull request: people and teams asked to review it, distinct from the reviews
// they may later submit. Both tools answer with the pull request itself, the same shape
// github.pullrequests.get reports, so requested_reviewers is read back after the change.

const maxReviewers = 15

const reviewersSchema = `{"type":"array","maxItems":15,"items":{"type":"string","maxLength":100,"pattern":"` +
	loginPattern + `"}}`

const teamReviewersSchema = `{"type":"array","maxItems":15,"items":` + slugSchema + `}`

var reviewerArgumentDescriptors = []capability.Argument{
	{Name: "number", Description: "Pull request number in the repository", Required: true},
	{Name: "reviewers", Description: "Logins to name; name at least one of reviewers or team_reviewers"},
	{Name: "team_reviewers", Description: "Team slugs of the repository's organization to name"},
}

var pullRequestReviewersRequest = capability.Descriptor{
	ID:      Provider + ".pullrequestreviewers.request",
	Version: 1,
	Title:   "Request GitHub pull request reviewers",
	Description: "Request the given users, teams, or both as reviewers of one pull request of a repository " +
		"an explicit connection allows; a reviewer already requested, or the pull request's author, is left " +
		"as it is",
	Tags:                       []string{"github", "pulls", "pullrequests", "reviewers", "request"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"number":`+numberSchema+`,"reviewers":`+reviewersSchema+`,"team_reviewers":`+
		teamReviewersSchema, "number"),
	OutputSchema: pullsGet.OutputSchema,
	Arguments:    reviewerArgumentDescriptors,
	Fields:       pullsGet.Fields,
	Examples: []capability.Example{{
		Description: "Request a reviewer",
		Arguments:   json.RawMessage(`{"number":42,"reviewers":["hubot"]}`),
	}},
}

var pullRequestReviewersRemove = capability.Descriptor{
	ID:      Provider + ".pullrequestreviewers.remove",
	Version: 1,
	Title:   "Remove GitHub pull request reviewers",
	Description: "Remove the given users, teams, or both from the requested reviewers of one pull request " +
		"of a repository an explicit connection allows; a reviewer not requested is left as it is",
	Tags:                       []string{"github", "pulls", "pullrequests", "reviewers", "remove"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"number":`+numberSchema+`,"reviewers":`+reviewersSchema+`,"team_reviewers":`+
		teamReviewersSchema, "number"),
	OutputSchema: pullsGet.OutputSchema,
	Arguments:    reviewerArgumentDescriptors,
	Fields:       pullsGet.Fields,
	Examples: []capability.Example{{
		Description: "Remove a requested reviewer",
		Arguments:   json.RawMessage(`{"number":42,"reviewers":["hubot"]}`),
	}},
}

// pullRequestReviewerOperations binds the requested-reviewer tools to their handlers.
func pullRequestReviewerOperations() []capability.Operation {
	bind := func(descriptor capability.Descriptor,
		call func(context.Context, *Client, *reviewerArguments) (any, error)) capability.Operation {
		return capability.Operation{Descriptor: descriptor, Handler: reviewersHandler(descriptor.ID, call)}
	}
	return []capability.Operation{
		bind(pullRequestReviewersRequest, func(ctx context.Context, c *Client, a *reviewerArguments) (any, error) {
			return c.changeRequestedReviewers(ctx, http.MethodPost, a)
		}),
		bind(pullRequestReviewersRemove, func(ctx context.Context, c *Client, a *reviewerArguments) (any, error) {
			return c.changeRequestedReviewers(ctx, http.MethodDelete, a)
		}),
	}
}

// reviewerArguments holds the arguments both requested-reviewer tools share.
type reviewerArguments struct {
	Number        int      `json:"number"`
	Reviewers     []string `json:"reviewers"`
	TeamReviewers []string `json:"team_reviewers"`
}

func reviewersHandler(id string, call func(context.Context, *Client, *reviewerArguments) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments reviewerArguments
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, unreadable(id)
		}
		bound, err := selectTarget(resolved, kindRepository, raw)
		if err != nil {
			return nil, err
		}
		if err := checkReviewerArguments(&arguments); err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(call(ctx, client, &arguments))
	}
}

func checkReviewerArguments(a *reviewerArguments) error {
	if err := checkNumber(a.Number); err != nil {
		return err
	}
	if len(a.Reviewers) == 0 && len(a.TeamReviewers) == 0 {
		return invalidRequest("name at least one of reviewers or team_reviewers")
	}
	if len(a.Reviewers)+len(a.TeamReviewers) > maxReviewers {
		return invalidRequest(fmt.Sprintf("reviewers and team_reviewers accept at most %d entries together", maxReviewers))
	}
	for _, login := range a.Reviewers {
		if !validLogin(login) {
			return invalidRequest("reviewers must be GitHub logins")
		}
	}
	for _, slug := range a.TeamReviewers {
		if err := checkTeam("team_reviewers", slug); err != nil {
			return err
		}
	}
	return nil
}

func (a *reviewerArguments) payload() map[string]any {
	payload := map[string]any{}
	if len(a.Reviewers) > 0 {
		payload["reviewers"] = a.Reviewers
	}
	if len(a.TeamReviewers) > 0 {
		payload["team_reviewers"] = a.TeamReviewers
	}
	return payload
}

// changeRequestedReviewers requests or removes the given reviewers of one pull request of the bound
// repository, in one request that is never repeated.
func (c *Client) changeRequestedReviewers(ctx context.Context, method string, a *reviewerArguments) (*PullRequest, error) {
	op := "request pull request reviewers"
	if method == http.MethodDelete {
		op = "remove pull request reviewers"
	}
	var raw pullJSON
	if err := c.pullChange(ctx, op, method, c.repoPath("pulls/"+strconv.Itoa(a.Number)+"/requested_reviewers"),
		a.payload(), &raw); err != nil {
		return nil, err
	}
	return raw.full(), nil
}
