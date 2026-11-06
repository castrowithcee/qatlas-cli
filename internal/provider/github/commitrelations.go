package github

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
)

// The commit relation tools read how one commit or one branch of the bound repository hangs together,
// without writing anything: github.branches.get reads one branch with its protection,
// github.commit-branches.list the branches whose head is a commit, and github.commit-pulls.list the pull
// requests a commit belongs to. Every route lies below the chosen repository. They belong to the code group
// with the other commit tools, although the pull requests they list are pull requests.
//
// A pull request of another repository, such as a fork's, is shown by its identifiers only, and so is the
// repository behind its head and base, exactly as the pull request tools show them.

// Permission messages of the commit relation tools. GitHub decides on every request; a message names what such
// a request needs without claiming what the configured token holds.
const (
	branchGetPermission = "GitHub refused this token the branch of this repository; reading it needs no scope " +
		"for a public repository, or repo on a classic token, or Contents: read on a fine-grained token, for a " +
		"private one"
	commitBranchesPermission = "GitHub refused this token the branches of this commit; reading them needs no " +
		"scope for a public repository, or repo on a classic token, or Contents: read on a fine-grained token, " +
		"for a private one"
	commitPullsPermission = "GitHub refused this token the pull requests of this commit; reading them needs no " +
		"scope for a public repository, or repo on a classic token, or Contents: read and Pull requests: read " +
		"on a fine-grained token, for a private one"
)

var commitSHASchema = `{"type":"string","minLength":4,"maxLength":40,"pattern":"^[0-9a-f]{4,40}$"}`

// ---- github.branches.get

var branchDetailProperties = `"name":{"type":"string"},"protected":{"type":"boolean"},"sha":{"type":"string"},` +
	`"commit":` + objectSchema(restCommitProperties) + `,"protection":` + restBranchProtectionSchema +
	`,"pattern":{"type":"string"},"required_approving_review_count":{"type":"integer"},` +
	`"link_relations":{"type":"object","maxProperties":2,"additionalProperties":{"type":"string"}},` +
	`"links":` + linksSchema([]string{"protection_url"})

var branchesGet = capability.Descriptor{
	ID:      Provider + ".branches.get",
	Version: 1,
	Title:   "Get a GitHub branch",
	Description: "Read one branch of a repository a connection allows with its latest commit, its protection " +
		"where GitHub sends it, and its protection pattern and required approving review count",
	Tags:        []string{"github", "branches", "get"},
	Risk:        readRisk,
	Provider:    Provider,
	InputSchema: inputSchema(`"branch":`+refSchema, "branch"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + branchDetailProperties + `},` +
		`"required":["name","protected","sha"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "branch", Description: "Branch name", Required: true},
	},
	Fields: []capability.Field{
		{Name: "name", Description: "Branch name, untrusted data"},
		{Name: "protected", Description: "True when the branch is protected"},
		{Name: "sha", Description: "SHA of the latest commit of the branch"},
		{Name: "commit", Description: "Latest commit with the members of GitHub's commit: sha, node_id, url " +
			"(the web address), links, author_detail, committer_detail, commit (the git commit), " +
			"parents_details, and, where GitHub sends them, stats and files with patches cut to at most 4096 " +
			"bytes; untrusted data"},
		{Name: "protection", Description: "Protection of the branch: name, enabled, required_status_checks, " +
			"enforce_admins, required_pull_request_reviews, restrictions, and the switches " +
			"required_linear_history, allow_force_pushes, allow_deletions, block_creations, " +
			"required_conversation_resolution, required_signatures, lock_branch, and allow_fork_syncing, each " +
			"with its own links; users, teams, and apps at most 100 each; untrusted data"},
		{Name: "pattern", Description: "Branch protection pattern that covers the branch, untrusted data"},
		{Name: "required_approving_review_count", Description: "Number of approving reviews the protection requires"},
		{Name: "link_relations", Description: "html and self addresses of GitHub's _links by their names, " +
			"untrusted data, never fetched"},
		{Name: "links", Description: "REST protection_url by its GitHub name, untrusted data, never fetched"},
	},
	Examples: []capability.Example{{Description: "Read the main branch", Arguments: json.RawMessage(`{"branch":"main"}`)}},
}

// BranchDetail is one branch read by github.branches.get.
type BranchDetail struct {
	Name                         string                `json:"name"`
	Protected                    bool                  `json:"protected"`
	SHA                          string                `json:"sha"`
	Commit                       *RESTCommit           `json:"commit,omitempty"`
	Protection                   *RESTBranchProtection `json:"protection,omitempty"`
	Pattern                      *string               `json:"pattern,omitempty"`
	RequiredApprovingReviewCount *int64                `json:"required_approving_review_count,omitempty"`
	LinkRelations                map[string]string     `json:"link_relations,omitempty"`
	Links                        Links                 `json:"links,omitempty"`
}

type branchDetailRaw struct {
	Name                         string               `json:"name"`
	Protected                    bool                 `json:"protected"`
	Commit                       *commitRaw           `json:"commit"`
	Protection                   *branchProtectionRaw `json:"protection"`
	ProtectionURL                *string              `json:"protection_url"`
	Pattern                      *string              `json:"pattern"`
	RequiredApprovingReviewCount *int64               `json:"required_approving_review_count"`
	RelLinks                     map[string]*string   `json:"_links"`
}

func checkBranchGetArguments(a *commitLinkArguments, _ target, _ allowlist) error {
	if !validRef(a.Branch) {
		return invalidRequest("branch must be a valid branch name")
	}
	return nil
}

func (c *Client) getBranch(ctx context.Context, a *commitLinkArguments) (*BranchDetail, error) {
	const op = "get branch"
	var raw branchDetailRaw
	if err := c.rest(ctx, op, c.repoPath("branches/"+url.PathEscape(a.Branch)), &raw); err != nil {
		return nil, actionsFailure(err, branchGetPermission)
	}
	if raw.Name == "" || raw.Commit == nil || valueOr(raw.Commit.SHA) == "" {
		return nil, invalidEntry(op, "a branch")
	}
	commit := raw.Commit.project()
	detail := &BranchDetail{Name: *nameText(&raw.Name), Protected: raw.Protected, SHA: commit.SHA, Commit: &commit,
		Protection: raw.Protection.project(), Pattern: nameText(raw.Pattern),
		RequiredApprovingReviewCount: raw.RequiredApprovingReviewCount, Links: linkOf("protection_url", raw.ProtectionURL)}
	for _, name := range []string{"html", "self"} {
		if href := address(raw.RelLinks[name]); href != nil {
			if detail.LinkRelations == nil {
				detail.LinkRelations = map[string]string{}
			}
			detail.LinkRelations[name] = *href
		}
	}
	return detail, nil
}

// ---- github.commit-branches.list

var commitBranchesList = capability.Descriptor{
	ID:      Provider + ".commit-branches.list",
	Version: 1,
	Title:   "List the GitHub branches headed by a commit",
	Description: "List the branches of a repository a connection allows whose latest commit is the given " +
		"commit, each with its protection status; GitHub answers them in one batch, at most 100 are shown",
	Tags:        []string{"github", "commits", "branches", "list"},
	Risk:        readRisk,
	Provider:    Provider,
	InputSchema: inputSchema(`"commit_sha":`+commitSHASchema, "commit_sha"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"branches":{"type":"array","maxItems":100,` +
		`"items":{"type":"object","properties":{` + branchProperties + `},` + branchRequired + `}},` +
		`"branches_truncated":{"type":"boolean"}},"required":["branches"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "commit_sha", Description: "SHA of the commit, as hexadecimal characters in lower case", Required: true},
	},
	Fields: append([]capability.Field{
		{Name: "branches", Description: "Branches whose latest commit is the commit, at most 100, each with " +
			"name, whether it is protected, and its latest commit sha, then the members of GitHub's branch " +
			"below; empty when no branch ends at the commit"},
		{Name: "branches_truncated", Description: "True when GitHub answered more branches than shown"},
	}, branchWidthFields...),
	Examples: []capability.Example{{
		Description: "List the branches that end at one commit",
		Arguments:   json.RawMessage(`{"commit_sha":"ebca79b1db4fcbb136e6094c13e8451428c8a6ab"}`),
	}},
}

// CommitBranchList is the branches a commit heads.
type CommitBranchList struct {
	Branches          []Branch `json:"branches"`
	BranchesTruncated bool     `json:"branches_truncated,omitempty"`
}

func checkCommitSHAArguments(a *commitLinkArguments, _ target, _ allowlist) error {
	if !validSHA(a.CommitSHA) {
		return invalidRequest("commit_sha must be a hexadecimal commit SHA in lower case")
	}
	return nil
}

func (c *Client) listCommitBranches(ctx context.Context, a *commitLinkArguments) (*CommitBranchList, error) {
	const op = "list branches of a commit"
	var raw []branchJSON
	if err := c.rest(ctx, op, c.repoPath("commits/"+url.PathEscape(a.CommitSHA)+"/branches-where-head"), &raw); err != nil {
		return nil, actionsFailure(err, commitBranchesPermission)
	}
	raw, truncated := capped(raw)
	result := &CommitBranchList{Branches: make([]Branch, 0, len(raw)), BranchesTruncated: truncated}
	for _, branch := range raw {
		if branch.Name == "" || branch.Commit.SHA == "" {
			return nil, invalidEntry(op, "a branch")
		}
		result.Branches = append(result.Branches, branch.view())
	}
	return result, nil
}

// ---- github.commit-pulls.list

var commitPullsList = capability.Descriptor{
	ID:      Provider + ".commit-pulls.list",
	Version: 1,
	Title:   "List the GitHub pull requests of a commit",
	Description: "List one bounded batch of the pull requests of a repository a connection allows that " +
		"contain a commit, in GitHub's own order; a pull request of a repository outside the connection's " +
		"targets is shown by its identifiers only",
	Tags:         []string{"github", "commits", "pulls", "list"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(`"commit_sha":`+commitSHASchema+`,`+pagingKeys, "commit_sha"),
	OutputSchema: listOutput("pull_requests", restPullSimpleProperties, `"additionalProperties":false`),
	Arguments: append([]capability.Argument{
		{Name: "commit_sha", Description: "SHA of the commit, as hexadecimal characters in lower case", Required: true},
	}, pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "pull_requests", Description: "Pull requests that contain the commit, with the members of " +
			"GitHub's pull request below; title and body are untrusted data"},
	}, restPullSimpleFields...), pagingFields...),
	Examples: []capability.Example{{
		Description: "List the pull requests a commit is part of",
		Arguments:   json.RawMessage(`{"commit_sha":"ebca79b1db4fcbb136e6094c13e8451428c8a6ab","limit":10}`),
	}},
}

// CommitPullList is one batch of the pull requests of a commit.
type CommitPullList struct {
	PullRequests []RESTPullSimple `json:"pull_requests"`
	NextCursor   string           `json:"next_cursor,omitempty"`
	HasMore      bool             `json:"has_more"`
}

func checkCommitPagedArguments(list string) func(*commitLinkArguments, target, allowlist) error {
	return func(a *commitLinkArguments, bound target, _ allowlist) error {
		if list == "pulls" && !validSHA(a.CommitSHA) || list == "comments" && a.CommitSHA != "" && !validSHA(a.CommitSHA) {
			return invalidRequest("commit_sha must be a hexadecimal commit SHA in lower case")
		}
		limit, err := normalizeLimit(a.Limit)
		if err != nil {
			return err
		}
		a.binding = fingerprint("commit-"+list, "list", bound.String(), a.CommitSHA)
		a.page, a.perPage, err = pageOf(a.binding, a.Cursor, limit)
		return err
	}
}

func (a *commitLinkArguments) query() url.Values {
	return url.Values{"per_page": {strconv.Itoa(a.perPage)}, "page": {strconv.Itoa(a.page)}}
}

// outside reports whether the pull request belongs to a repository the connection's targets do not admit; a
// pull request without a base repository cannot be placed and counts as outside.
func (r *pullSimpleRaw) outside(allowed allowlist) bool {
	return r.Base == nil || r.Base.Repo == nil || !r.Base.Repo.admitted(allowed)
}

// identifiers is a pull request shown by its identifiers and the identifiers of its base repository alone.
func (r *pullSimpleRaw) identifiers(allowed allowlist) *RESTPullSimple {
	pull := &RESTPullSimple{ID: r.ID, NodeID: nameText(r.NodeID), Number: r.Number}
	if r.Base != nil && r.Base.Repo != nil {
		pull.Base = &RESTPullRef{Repo: r.Base.Repo.project(allowed)}
	}
	return pull
}

func (c *Client) listCommitPulls(ctx context.Context, a *commitLinkArguments) (*CommitPullList, error) {
	const op = "list pull requests of a commit"
	var raw []pullSimpleRaw
	hasNext, err := c.restPage(ctx, op, c.repoPath("commits/"+url.PathEscape(a.CommitSHA)+"/pulls"), a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, commitPullsPermission)
	}
	result := &CommitPullList{PullRequests: make([]RESTPullSimple, 0, len(raw))}
	for i := range raw {
		if raw[i].Number == nil || *raw[i].Number < 1 {
			return nil, invalidEntry(op, "a pull request")
		}
		if raw[i].outside(c.allowed) {
			result.PullRequests = append(result.PullRequests, *raw[i].identifiers(c.allowed))
			continue
		}
		result.PullRequests = append(result.PullRequests, *raw[i].project(c.allowed))
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

func commitRelationOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: branchesGet, Handler: commitLinkHandler(branchesGet.ID, checkBranchGetArguments,
			func(ctx context.Context, c *Client, a *commitLinkArguments) (any, error) { return c.getBranch(ctx, a) })},
		{Descriptor: commitBranchesList, Handler: commitLinkHandler(commitBranchesList.ID, checkCommitSHAArguments,
			func(ctx context.Context, c *Client, a *commitLinkArguments) (any, error) {
				return c.listCommitBranches(ctx, a)
			})},
		{Descriptor: commitPullsList, Handler: commitLinkHandler(commitPullsList.ID, checkCommitPagedArguments("pulls"),
			func(ctx context.Context, c *Client, a *commitLinkArguments) (any, error) {
				return c.listCommitPulls(ctx, a)
			})},
		{Descriptor: commitCommentsList, Handler: commitLinkHandler(commitCommentsList.ID,
			checkCommitPagedArguments("comments"),
			func(ctx context.Context, c *Client, a *commitLinkArguments) (any, error) {
				return c.listCommitComments(ctx, a)
			})},
		{Descriptor: commitCommentsGet, Handler: commitLinkHandler(commitCommentsGet.ID, checkCommitCommentGetArguments,
			func(ctx context.Context, c *Client, a *commitLinkArguments) (any, error) {
				return c.getCommitComment(ctx, a)
			})},
	}
}

// commitRelationSubject names the branch, the comparison, the commit relation, or the commit comment a path
// below a repository addresses: branches/BRANCH, compare/BASE...HEAD, comments, comments/ID, or the tails
// branches-where-head, pulls, and comments of commits/SHA. It names only values of the characters the input
// schemas allow, and is empty otherwise.
func commitRelationSubject(path string) string {
	if what := compareSubject(path); what != "" {
		return what
	}
	if rest, ok := strings.CutPrefix(path, "branches/"); ok && !strings.Contains(rest, "/") {
		if branch, err := url.PathUnescape(rest); err == nil && validRef(branch) {
			return "branch " + branch
		}
		return ""
	}
	if path == "comments" {
		return "the commit comments of the repository"
	}
	if rest, ok := strings.CutPrefix(path, "comments/"); ok {
		if id, err := strconv.ParseInt(rest, 10, 64); err == nil && id > 0 {
			return "commit comment " + rest
		}
		return ""
	}
	if rest, ok := strings.CutPrefix(path, "commits/"); ok {
		sha, tail, _ := strings.Cut(rest, "/")
		if !validSHA(sha) {
			return ""
		}
		switch tail {
		case "branches-where-head":
			return "the branches headed by commit " + sha
		case "pulls":
			return "the pull requests of commit " + sha
		case "comments":
			return "the comments of commit " + sha
		}
	}
	return ""
}
