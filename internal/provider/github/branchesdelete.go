package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
)

// github.branches.delete deletes one branch of the bound repository through the Git refs route, offered only
// where a connection's tools list names it. The repository's default branch is refused before the delete is
// sent, and expected_sha, when given, is compared with a fresh read of the ref first, so a branch that moved
// is never deleted. The delete is sent at most once; an unclear outcome is reported, never repeated.

const branchDeletedOutput = `{"type":"object","properties":{"branch":{"type":"string"},"ref":{"type":"string"},` +
	`"sha":{"type":"string"}},"required":["branch","ref"],"additionalProperties":false}`

var branchesDelete = capability.Descriptor{
	ID:      Provider + ".branches.delete",
	Version: 1,
	Title:   "Delete a GitHub branch",
	Description: "Delete one branch of a repository a connection allows, never the repository's default " +
		"branch; with expected_sha, only while the branch still points at that commit; offered only where a " +
		"connection's tools list names it, because a deleted branch cannot be restored by this tool",
	Tags:                  []string{"github", "branches", "delete"},
	Risk:                  guardedRisk(capability.EffectDelete, capability.IdempotencyUnknown, dataSensitivity),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema:           inputSchema(`"branch":`+refSchema+`,"expected_sha":`+blobSHASchema, "branch"),
	OutputSchema:          json.RawMessage(branchDeletedOutput),
	Arguments: []capability.Argument{
		{Name: "branch", Description: "Name of the branch to delete, without refs/heads/", Required: true},
		{Name: "expected_sha", Description: "Commit SHA the branch must point at; nothing is deleted when it " +
			"points elsewhere"},
	},
	Fields: []capability.Field{
		{Name: "branch", Description: "Name of the deleted branch"},
		{Name: "ref", Description: "Full ref of the deleted branch"},
		{Name: "sha", Description: "Commit the branch pointed at before the deletion, when it was read"},
	},
	Examples: []capability.Example{{
		Description: "Delete the head branch of a merged pull request",
		Arguments:   json.RawMessage(`{"branch":"feature/login"}`),
	}},
}

const branchesDeletePermission = "GitHub refused to delete this branch; it may be protected or covered by a " +
	"repository rule, and the token needs repo on a classic token, or Contents: read and write on a " +
	"fine-grained token"

// BranchDeleted is the answer to a deleted branch.
type BranchDeleted struct {
	Branch string `json:"branch"`
	Ref    string `json:"ref"`
	SHA    string `json:"sha,omitempty"`
}

func checkBranchesDeleteArguments(a *contentsWriteArguments) error {
	switch {
	case strings.HasPrefix(a.Branch, "refs/"):
		return invalidRequest("branch must be the branch name without refs/heads/")
	case !validRef(a.Branch):
		return invalidRequest("branch must be a usable branch name")
	case a.ExpectedSHA != "" && !validCommitSHA(a.ExpectedSHA):
		return invalidRequest("expected_sha must be a full commit SHA")
	}
	return nil
}

// deleteBranch refuses the default branch and a branch that moved before it sends one DELETE.
func (c *Client) deleteBranch(ctx context.Context, a *contentsWriteArguments) (*BranchDeleted, error) {
	const op = "delete branch"
	var repo struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.rest(ctx, op, "/repos/"+url.PathEscape(c.target.owner)+"/"+url.PathEscape(c.target.repo), &repo); err != nil {
		return nil, actionsFailure(err, branchesDeletePermission)
	}
	if repo.DefaultBranch == "" {
		return nil, invalidEntry(op, "a repository")
	}
	if a.Branch == repo.DefaultBranch {
		return nil, invalidRequest("the default branch of a repository is never deleted")
	}
	ref := "refs/heads/" + a.Branch
	result := &BranchDeleted{Branch: a.Branch, Ref: ref}
	if a.ExpectedSHA != "" {
		var raw struct {
			Ref    string `json:"ref"`
			Object struct {
				SHA string `json:"sha"`
			} `json:"object"`
		}
		if err := c.rest(ctx, op, c.gitRefHeadPath(a.Branch), &raw); err != nil {
			return nil, actionsFailure(err, branchesDeletePermission)
		}
		if raw.Ref != ref || !validCommitSHA(raw.Object.SHA) {
			return nil, invalidEntry(op, "a branch")
		}
		if !strings.EqualFold(raw.Object.SHA, a.ExpectedSHA) {
			return nil, invalidRequest("the branch no longer points at expected_sha, so nothing was deleted; " +
				"it points at " + raw.Object.SHA + "; read it again and decide whether to delete it")
		}
		result.SHA = raw.Object.SHA
	}
	if err := c.sendBranchDelete(ctx, op, a.Branch); err != nil {
		return nil, err
	}
	return result, nil
}

// sendBranchDelete sends the DELETE directly, instead of through restChange, so a 422 answer can be read once
// and told apart: a missing ref is not-found, a protected one is a permission refusal. Provider text never
// reaches a message.
func (c *Client) sendBranchDelete(ctx context.Context, op, branch string) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "GitHub", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.endpoints.rest+c.gitRefHeadPath(branch), nil)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	c.authorize(req)
	response, err := c.http.Do(req)
	defer c.limiter.HoldFor(mutationInterval)
	if err != nil {
		failure := provider.Transport(op, "GitHub", err)
		if failure.MayHaveArrived() {
			failure.Message += uncertain
		}
		return failure
	}
	defer response.Body.Close()
	c.observeRateLimit(response.Header)
	if response.StatusCode == http.StatusNoContent {
		return nil
	}
	if response.StatusCode >= 200 && response.StatusCode <= 299 {
		return invalidResponse(op, true)
	}
	data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	response.Body = io.NopCloser(bytes.NewReader(data))
	err = c.statusError(op, response, true)
	var failure *provider.Error
	if response.StatusCode == http.StatusUnprocessableEntity && errors.As(err, &failure) {
		text := bytes.ToLower(data)
		switch {
		case bytes.Contains(text, []byte("reference does not exist")):
			missing := notFound(op, c.restSubject(response.Request))
			missing.Original = failure.Original
			return missing
		case bytes.Contains(text, []byte("protected")) || bytes.Contains(text, []byte("rule")):
			failure.Class = provider.ClassPermission
			failure.Message = branchesDeletePermission
			return failure
		}
	}
	return actionsFailure(err, branchesDeletePermission)
}
