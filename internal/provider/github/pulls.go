package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Pull requests of a repository. GitHub's project items, issues, and comments still see a pull request only
// as an item, or refuse its number; these six tools read the pull requests themselves: their list, one pull
// request with its merge state, its changed files with a bounded patch excerpt, its commits, its diff within
// a hard size limit, and the check runs and combined commit status at its head commit. They offer no way to
// create, change, close, merge, or review a pull request, or to comment on one; that stays later work. Every
// route lies below the chosen repository, and they are offered only by the not-recommended setup profile
// pull-requests.

// Bounds of the pull request tools. A file patch is cut like a jobLog excerpt, and a diff download is capped
// outright instead of read in a tail, because it is read from its start.
const (
	maxPatchBytes = 4 << 10  // 4096 bytes kept of one file's patch; the rest is left out, never silently widened
	maxDiffBytes  = 64 << 10 // 65536 bytes kept of a pull request diff, read from its start
)

// headPattern accepts a branch name, or LOGIN:branch for a fork, as GitHub's head filter takes it.
const headPattern = `^([A-Za-z0-9][A-Za-z0-9_-]{0,99}:)?[A-Za-z0-9_][A-Za-z0-9._/-]{0,254}$`

// headSchema's maxLength is the 100-character login plus its colon plus the 255-character branch name.
const headSchema = `{"type":"string","minLength":1,"maxLength":356,"pattern":"` + headPattern + `"}`

const pullSummaryProperties = `"number":{"type":"integer"},"title":{"type":"string"},"state":{"type":"string"},` +
	`"draft":{"type":"boolean"},"author":{"type":"string"},"head_branch":{"type":"string"},` +
	`"head_sha":{"type":"string"},"base_branch":{"type":"string"},"labels":` + stringListSchema + `,` +
	`"merged":{"type":"boolean"},"created_at":{"type":"string"},"updated_at":{"type":"string"},` +
	`"url":{"type":"string"}`

const pullSummaryRequired = `"required":["number","title","state","draft","labels","merged"],"additionalProperties":false`

const pullFileProperties = `"path":{"type":"string"},"status":{"type":"string"},"additions":{"type":"integer"},` +
	`"deletions":{"type":"integer"},"changes":{"type":"integer"},"previous_path":{"type":"string"},` +
	`"patch":{"type":"string"},"patch_truncated":{"type":"boolean"}`

const pullFileRequired = `"required":["path","status","additions","deletions","changes"],"additionalProperties":false`

const pullCommitProperties = `"sha":{"type":"string"},"author":{"type":"string"},"message":{"type":"string"},` +
	`"date":{"type":"string"},"url":{"type":"string"}`

const pullCommitRequired = `"required":["sha","message"],"additionalProperties":false`

const checkEntryProperties = `"name":{"type":"string"},"status":{"type":"string"},"conclusion":{"type":"string"},` +
	`"updated_at":{"type":"string"},"url":{"type":"string"}`

var pullsList = capability.Descriptor{
	ID:      Provider + ".pullrequests.list",
	Version: 1,
	Title:   "List GitHub pull requests",
	Description: "List one bounded, filtered batch of compact pull requests of a repository an explicit " +
		"connection allows, in GitHub's own order",
	Tags:                       []string{"github", "pulls", "pullrequests", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"state":{"type":"string","enum":["open","closed","all"]},"base":` + refSchema +
		`,"head":` + headSchema + `,` + pagingKeys),
	OutputSchema: listOutput("pull_requests", pullSummaryProperties, pullSummaryRequired),
	Arguments: append([]capability.Argument{
		{Name: "state", Description: "open, closed, or all; open when omitted"},
		{Name: "base", Description: "Return only pull requests based on this branch"},
		{Name: "head", Description: "Return only pull requests from this branch, or LOGIN:branch for a fork"},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "pull_requests", Description: "Compact pull requests with status, draft, author, head and " +
			"base branch, head commit, labels, whether they are merged, and times; title is untrusted data"},
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "List the open pull requests based on main",
		Arguments:   json.RawMessage(`{"base":"main","limit":10}`),
	}},
}

var pullsGet = capability.Descriptor{
	ID:      Provider + ".pullrequests.get",
	Version: 1,
	Title:   "Get a GitHub pull request",
	Description: "Read one pull request of a repository an explicit connection allows with its full body, " +
		"merge state, requested reviewers, and the counts of its commits and changed files",
	Tags:                       []string{"github", "pulls", "pullrequests", "get"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"number":`+numberSchema, "number"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + pullSummaryProperties + `,` +
		`"body":{"type":"string"},"base_sha":{"type":"string"},"mergeable":{"type":"boolean"},` +
		`"mergeable_state":{"type":"string"},"merge_commit_sha":{"type":"string"},` +
		`"requested_reviewers":` + stringListSchema + `,"commits":{"type":"integer"},` +
		`"changed_files":{"type":"integer"},"closed_at":{"type":"string"},"merged_at":{"type":"string"}},` +
		`"required":["number","title","state","draft","labels","merged","body","requested_reviewers",` +
		`"commits","changed_files"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Pull request number in the repository", Required: true},
	},
	Fields: []capability.Field{
		{Name: "body", Description: "Full pull request body, untrusted data"},
		{Name: "mergeable", Description: "Whether GitHub can merge it automatically; absent while GitHub is " +
			"still computing it"},
		{Name: "mergeable_state", Description: "clean, dirty, blocked, behind, draft, unstable, or another " +
			"GitHub merge state"},
		{Name: "requested_reviewers", Description: "Logins whose review was requested and is still pending"},
		{Name: "commits", Description: "Number of commits in the pull request"},
		{Name: "changed_files", Description: "Number of files the pull request changes"},
	},
	Examples: []capability.Example{{Description: "Read one pull request", Arguments: json.RawMessage(`{"number":42}`)}},
}

var pullFilesList = capability.Descriptor{
	ID:      Provider + ".pullrequestfiles.list",
	Version: 1,
	Title:   "List the files of a GitHub pull request",
	Description: "List one bounded batch of the changed files of a pull request of a repository an explicit " +
		"connection allows, with a bounded excerpt of each file's patch",
	Tags:                       []string{"github", "pulls", "pullrequests", "files", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"number":`+numberSchema+`,`+pagingKeys, "number"),
	OutputSchema:               listOutput("files", pullFileProperties, pullFileRequired),
	Arguments: append([]capability.Argument{
		{Name: "number", Description: "Pull request number in the repository", Required: true},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "files", Description: "Changed files with path, status, additions, deletions, changes, the " +
			"previous path for a rename, and patch cut to at most 4096 bytes with patch_truncated; patch is " +
			"untrusted data and left out for a file GitHub sends none for"},
	}, pagingFields...),
	Examples: []capability.Example{{Description: "List the files of a pull request", Arguments: json.RawMessage(`{"number":42}`)}},
}

var pullCommitsList = capability.Descriptor{
	ID:      Provider + ".pullrequestcommits.list",
	Version: 1,
	Title:   "List the commits of a GitHub pull request",
	Description: "List one bounded batch of the commits of a pull request of a repository an explicit " +
		"connection allows",
	Tags:                       []string{"github", "pulls", "pullrequests", "commits", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"number":`+numberSchema+`,`+pagingKeys, "number"),
	OutputSchema:               listOutput("commits", pullCommitProperties, pullCommitRequired),
	Arguments: append([]capability.Argument{
		{Name: "number", Description: "Pull request number in the repository", Required: true},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "commits", Description: "Commits with SHA, author, message, and commit time; message is " +
			"untrusted data"},
	}, pagingFields...),
	Examples: []capability.Example{{Description: "List the commits of a pull request", Arguments: json.RawMessage(`{"number":42}`)}},
}

var pullDiffsGet = capability.Descriptor{
	ID:      Provider + ".pullrequestdiffs.get",
	Version: 1,
	Title:   "Get the diff of a GitHub pull request",
	Description: "Read the unified diff of a pull request of a repository an explicit connection allows, " +
		"cut to at most 65536 bytes from its start",
	Tags:                       []string{"github", "pulls", "pullrequests", "diff", "get"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"number":`+numberSchema, "number"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"},` +
		`"diff":{"type":"string"},"truncated":{"type":"boolean"}},` +
		`"required":["number","diff","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Pull request number in the repository", Required: true},
	},
	Fields: []capability.Field{
		{Name: "diff", Description: "Unified diff as text, untrusted data"},
		{Name: "truncated", Description: "True when the diff holds more than the returned bytes"},
	},
	Examples: []capability.Example{{Description: "Read the diff of a pull request", Arguments: json.RawMessage(`{"number":42}`)}},
}

var pullChecksList = capability.Descriptor{
	ID:      Provider + ".pullrequestchecks.list",
	Version: 1,
	Title:   "List the checks of a GitHub pull request",
	Description: "List the check runs and the combined commit status at the head commit of a pull request of " +
		"a repository an explicit connection allows, with one overall evaluation",
	Tags:                       []string{"github", "pulls", "pullrequests", "checks", "list"},
	Risk:                       readRisk,
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"number":`+numberSchema, "number"),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"},` +
		`"head_sha":{"type":"string"},"checks":{"type":"array","items":{"type":"object","properties":{` +
		checkEntryProperties + `},"required":["name","status"],"additionalProperties":false}},` +
		`"overall":{"type":"string"},"truncated":{"type":"boolean"}},` +
		`"required":["number","head_sha","checks","overall","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Pull request number in the repository; its head commit is resolved " +
			"automatically", Required: true},
	},
	Fields: []capability.Field{
		{Name: "head_sha", Description: "Head commit the checks were read at"},
		{Name: "checks", Description: "Check runs and legacy commit statuses at the head commit, combined"},
		{Name: "overall", Description: "success, failure, pending, or none when nothing reported a check"},
		{Name: "truncated", Description: "True when GitHub reported more checks or statuses than were returned"},
	},
	Examples: []capability.Example{{Description: "List the checks of a pull request", Arguments: json.RawMessage(`{"number":42}`)}},
}

var pullsCreate = capability.Descriptor{
	ID:      Provider + ".pullrequests.create",
	Version: 1,
	Title:   "Create a GitHub pull request",
	Description: "Open one pull request in a repository an explicit connection allows; a repeated call opens " +
		"a second pull request",
	Tags:                       []string{"github", "pulls", "pullrequests", "create"},
	Risk:                       changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"title":`+titleSchema+`,"head":`+headSchema+`,"base":`+refSchema+`,"body":`+
		bodySchema+`,"draft":{"type":"boolean"},"maintainer_can_modify":{"type":"boolean"}`, "title", "head", "base"),
	OutputSchema: pullsGet.OutputSchema,
	Arguments: []capability.Argument{
		{Name: "title", Description: "Pull request title, 1 to 256 characters", Required: true},
		{Name: "head", Description: "Branch to merge, or LOGIN:branch for a fork", Required: true},
		{Name: "base", Description: "Branch to merge into", Required: true},
		{Name: "body", Description: "Pull request body in Markdown, at most 65536 characters; stored as given"},
		{Name: "draft", Description: "true opens it as a draft; false when omitted"},
		{Name: "maintainer_can_modify", Description: "true lets maintainers of the base repository push to " +
			"the head branch of a fork; GitHub's default when omitted"},
	},
	Fields: pullsGet.Fields,
	Examples: []capability.Example{{
		Description: "Open a pull request from a feature branch",
		Arguments:   json.RawMessage(`{"title":"Add retry logic","head":"feature/retry","base":"main"}`),
	}},
}

var pullsUpdate = capability.Descriptor{
	ID:      Provider + ".pullrequests.update",
	Version: 1,
	Title:   "Update a GitHub pull request",
	Description: "Replace the title, body, or base branch of one pull request of a repository an explicit " +
		"connection allows, mark it ready for review or convert it to a draft, or change whether maintainers " +
		"of the base repository may push to its head branch; fields left out stay unchanged",
	Tags:                       []string{"github", "pulls", "pullrequests", "update"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"number":`+numberSchema+`,"title":`+titleSchema+`,"body":`+bodySchema+`,"base":`+
		refSchema+`,"draft":{"type":"boolean"},"maintainer_can_modify":{"type":"boolean"}`, "number"),
	OutputSchema: pullsGet.OutputSchema,
	Arguments: []capability.Argument{
		{Name: "number", Description: "Pull request number in the repository", Required: true},
		{Name: "title", Description: "New pull request title, 1 to 256 characters"},
		{Name: "body", Description: "New pull request body in Markdown, at most 65536 characters; stored as given"},
		{Name: "base", Description: "New branch to merge into"},
		{Name: "draft", Description: "false marks it ready for review, true converts it to a draft"},
		{Name: "maintainer_can_modify", Description: "true lets maintainers of the base repository push to " +
			"the head branch of a fork, false revokes it"},
	},
	Fields: pullsGet.Fields,
	Examples: []capability.Example{{
		Description: "Mark a pull request ready for review",
		Arguments:   json.RawMessage(`{"number":42,"draft":false}`),
	}},
}

var pullsClose = capability.Descriptor{
	ID:      Provider + ".pullrequests.close",
	Version: 1,
	Title:   "Close a GitHub pull request",
	Description: "Close one pull request of a repository an explicit connection allows without merging it; a " +
		"pull request already closed, merged or not, is left as it is and reported with its current state",
	Tags:                       []string{"github", "pulls", "pullrequests", "close", "update"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"number":`+numberSchema, "number"),
	OutputSchema:               pullsGet.OutputSchema,
	Arguments: []capability.Argument{
		{Name: "number", Description: "Pull request number in the repository", Required: true},
	},
	Fields:   pullsGet.Fields,
	Examples: []capability.Example{{Description: "Close a pull request", Arguments: json.RawMessage(`{"number":42}`)}},
}

var pullsReopen = capability.Descriptor{
	ID:      Provider + ".pullrequests.reopen",
	Version: 1,
	Title:   "Reopen a GitHub pull request",
	Description: "Open one closed pull request of a repository an explicit connection allows again; a pull " +
		"request already open is left as it is, and a merged pull request is refused with a clear message",
	Tags:                       []string{"github", "pulls", "pullrequests", "reopen", "update"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema:                inputSchema(`"number":`+numberSchema, "number"),
	OutputSchema:               pullsGet.OutputSchema,
	Arguments: []capability.Argument{
		{Name: "number", Description: "Pull request number in the repository", Required: true},
	},
	Fields:   pullsGet.Fields,
	Examples: []capability.Example{{Description: "Reopen a pull request", Arguments: json.RawMessage(`{"number":42}`)}},
}

const pullBranchUpdateOutput = `{"type":"object","properties":{"number":{"type":"integer"},` +
	`"accepted":{"type":"boolean"}},"required":["number","accepted"],"additionalProperties":false}`

var pullBranchesUpdate = capability.Descriptor{
	ID:      Provider + ".pullrequestbranches.update",
	Version: 1,
	Title:   "Update the head branch of a GitHub pull request",
	Description: "Merge the base branch into the head branch of one pull request of a repository an explicit " +
		"connection allows, only while its head is still the given commit, so a head that changed since is " +
		"never merged into blindly; GitHub queues the merge and answers before it finishes",
	Tags:                       []string{"github", "pulls", "pullrequests", "branches", "update"},
	Risk:                       changeRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	InputSchema: inputSchema(`"number":`+numberSchema+`,"expected_head_sha":`+blobSHASchema, "number",
		"expected_head_sha"),
	OutputSchema: json.RawMessage(pullBranchUpdateOutput),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Pull request number in the repository", Required: true},
		{Name: "expected_head_sha", Description: "Head commit the branch must still be at; " +
			"github.pullrequests.get reports the current one", Required: true},
	},
	Fields: []capability.Field{
		{Name: "accepted", Description: "True once GitHub queued the update; it runs asynchronously, read the " +
			"pull request again to see its new head commit"},
	},
	Examples: []capability.Example{{
		Description: "Update a pull request's branch with its base",
		Arguments:   json.RawMessage(`{"number":42,"expected_head_sha":"ebca79b1db4fcbb136e6094c13e8451428c8a6ab"}`),
	}},
}

const pullMergeOutput = `{"type":"object","properties":{"number":{"type":"integer"},"merged":{"type":"boolean"},` +
	`"sha":{"type":"string"}},"required":["number","merged"],"additionalProperties":false}`

var pullsMerge = capability.Descriptor{
	ID:      Provider + ".pullrequests.merge",
	Version: 1,
	Title:   "Merge a GitHub pull request",
	Description: "Merge one pull request of a repository an explicit connection allows, only while its head " +
		"is still the given commit; offered only where a connection's tools list names it, because a merge " +
		"into the wrong repository's default branch is the costliest mistake there. Already merged with " +
		"exactly this head commit is success; a head that changed since ends as an invalid request naming " +
		"both commits; a pull request GitHub cannot merge, for example one blocked by branch protection, " +
		"missing reviews, or failing checks, ends with the reason",
	Tags:                       []string{"github", "pulls", "pullrequests", "merge"},
	Risk:                       guardedRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, dataSensitivity),
	Provider:                   Provider,
	RequiresExplicitConnection: true,
	RequiresToolAllowList:      true,
	InputSchema: inputSchema(`"number":`+numberSchema+`,"sha":`+blobSHASchema+`,"method":{"type":"string",`+
		`"enum":["merge","squash","rebase"]},"commit_title":`+titleSchema+`,"commit_message":`+bodySchema,
		"number", "sha"),
	OutputSchema: json.RawMessage(pullMergeOutput),
	Arguments: []capability.Argument{
		{Name: "number", Description: "Pull request number in the repository", Required: true},
		{Name: "sha", Description: "Head commit the pull request must still be at; github.pullrequests.get " +
			"reports the current one", Required: true},
		{Name: "method", Description: "merge, squash, or rebase; the repository's default when omitted"},
		{Name: "commit_title", Description: "Title of the merge commit, 1 to 256 characters; GitHub's default " +
			"when omitted"},
		{Name: "commit_message", Description: "Extra detail for the merge commit, at most 65536 characters"},
	},
	Fields: []capability.Field{
		{Name: "merged", Description: "True once the pull request is merged"},
		{Name: "sha", Description: "SHA of the merge commit"},
	},
	Examples: []capability.Example{{
		Description: "Merge a pull request by squashing",
		Arguments: json.RawMessage(`{"number":42,"sha":"ebca79b1db4fcbb136e6094c13e8451428c8a6ab",` +
			`"method":"squash"}`),
	}},
}

// pullRequestOperations binds every pull request tool to its handler.
func pullRequestOperations() []capability.Operation {
	bind := func(descriptor capability.Descriptor, check func(*pullArguments, target) error,
		call func(context.Context, *Client, *pullArguments) (any, error)) capability.Operation {
		return capability.Operation{Descriptor: descriptor, Handler: pullsHandler(descriptor.ID, check, call)}
	}
	return []capability.Operation{
		bind(pullsList, checkPullsListArguments, func(ctx context.Context, c *Client, a *pullArguments) (any, error) {
			return c.listPulls(ctx, a)
		}),
		bind(pullsGet, checkPullNumberArgument, func(ctx context.Context, c *Client, a *pullArguments) (any, error) {
			return c.getPullRequest(ctx, a.Number)
		}),
		bind(pullFilesList, checkPullSubList("files"), func(ctx context.Context, c *Client, a *pullArguments) (any, error) {
			return c.listPullFiles(ctx, a)
		}),
		bind(pullCommitsList, checkPullSubList("commits"), func(ctx context.Context, c *Client, a *pullArguments) (any, error) {
			return c.listPullCommits(ctx, a)
		}),
		bind(pullDiffsGet, checkPullNumberArgument, func(ctx context.Context, c *Client, a *pullArguments) (any, error) {
			return c.pullRequestDiff(ctx, a.Number)
		}),
		bind(pullChecksList, checkPullNumberArgument, func(ctx context.Context, c *Client, a *pullArguments) (any, error) {
			return c.pullRequestChecks(ctx, a.Number)
		}),
		bind(pullsCreate, checkPullCreateArguments, func(ctx context.Context, c *Client, a *pullArguments) (any, error) {
			return c.createPullRequest(ctx, a)
		}),
		bind(pullsUpdate, checkPullUpdateArguments, func(ctx context.Context, c *Client, a *pullArguments) (any, error) {
			return c.updatePullRequest(ctx, a)
		}),
		bind(pullsClose, checkPullNumberArgument, func(ctx context.Context, c *Client, a *pullArguments) (any, error) {
			return c.closePullRequest(ctx, a.Number)
		}),
		bind(pullsReopen, checkPullNumberArgument, func(ctx context.Context, c *Client, a *pullArguments) (any, error) {
			return c.reopenPullRequest(ctx, a.Number)
		}),
		bind(pullBranchesUpdate, checkPullBranchUpdateArguments, func(ctx context.Context, c *Client, a *pullArguments) (any, error) {
			return c.updatePullRequestBranch(ctx, a.Number, a.ExpectedHeadSHA)
		}),
		bind(pullsMerge, checkPullMergeArguments, func(ctx context.Context, c *Client, a *pullArguments) (any, error) {
			return c.mergePullRequest(ctx, a)
		}),
	}
}

// pullArguments holds the arguments of every pull request tool; the input schema of each tool admits only
// its own. page and perPage are derived by the checks. A boolean is a pointer, so a value left out stays
// apart from false, and an empty string always stays apart from an omitted one: no pull request field this
// provider writes accepts an empty string as a set value.
type pullArguments struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	Base   string `json:"base"`
	Head   string `json:"head"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`

	// The arguments of the change tools.
	Title               string  `json:"title"`
	Body                *string `json:"body"`
	Draft               *bool   `json:"draft"`
	MaintainerCanModify *bool   `json:"maintainer_can_modify"`
	SHA                 string  `json:"sha"`
	Method              string  `json:"method"`
	CommitTitle         string  `json:"commit_title"`
	CommitMessage       string  `json:"commit_message"`
	ExpectedHeadSHA     string  `json:"expected_head_sha"`

	page, perPage int
	binding       []byte
}

// pullsHandler decodes and checks the arguments and the repository before a credential is resolved, so a
// refused request never becomes a provider call.
func pullsHandler(id string, check func(*pullArguments, target) error,
	call func(context.Context, *Client, *pullArguments) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments pullArguments
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, unreadable(id)
		}
		bound, err := selectTarget(resolved, kindRepository, raw)
		if err != nil {
			return nil, err
		}
		if err := check(&arguments, bound); err != nil {
			return nil, err
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(call(ctx, client, &arguments))
	}
}

func checkPullNumberArgument(a *pullArguments, _ target) error {
	if a.Number < 1 || a.Number > 1000000000 {
		return invalidRequest("number must be a positive pull request number")
	}
	return nil
}

func checkPullsListArguments(a *pullArguments, bound target) error {
	limit, err := normalizeLimit(a.Limit)
	if err != nil {
		return err
	}
	switch a.State {
	case "":
		a.State = "open"
	case "open", "closed", "all":
	default:
		return invalidRequest("state must be open, closed, or all")
	}
	if a.Base != "" && !validRef(a.Base) {
		return invalidRequest("base must be a branch name")
	}
	if a.Head != "" && !validHead(a.Head) {
		return invalidRequest("head must be a branch, or LOGIN:branch for a fork")
	}
	a.binding = fingerprint("pulls", "list", bound.String(), a.State, a.Base, a.Head)
	a.page, a.perPage, err = pageOf(a.binding, a.Cursor, limit)
	return err
}

// checkPullSubList returns the check of one paged sub-list of a pull request: its files or its commits.
func checkPullSubList(list string) func(*pullArguments, target) error {
	return func(a *pullArguments, bound target) error {
		if err := checkPullNumberArgument(a, bound); err != nil {
			return err
		}
		limit, err := normalizeLimit(a.Limit)
		if err != nil {
			return err
		}
		a.binding = fingerprint("pulls", list, bound.String(), a.Number)
		a.page, a.perPage, err = pageOf(a.binding, a.Cursor, limit)
		return err
	}
}

// checkBoundedText refuses text over max runes; empty is always fine, since a caller uses it to mean "leave
// this to GitHub's default."
func checkBoundedText(name, value string, max int) error {
	if utf8.RuneCountInString(value) > max {
		return invalidRequest(fmt.Sprintf("%s must hold at most %d characters", name, max))
	}
	return nil
}

// checkPullTitle refuses a blank or oversized pull request or commit title.
func checkPullTitle(name, title string) error {
	if strings.TrimSpace(title) == "" {
		return invalidRequest(name + " must not be blank")
	}
	return checkBoundedText(name, title, maxTitleLength)
}

func checkPullCreateArguments(a *pullArguments, _ target) error {
	if err := checkPullTitle("title", a.Title); err != nil {
		return err
	}
	if a.Head == "" || !validHead(a.Head) {
		return invalidRequest("head must be a branch, or LOGIN:branch for a fork")
	}
	if a.Base == "" || !validRef(a.Base) {
		return invalidRequest("base must be a branch name")
	}
	if a.Body != nil {
		if err := checkBoundedText("body", *a.Body, maxBodyLength); err != nil {
			return err
		}
	}
	return nil
}

func checkPullUpdateArguments(a *pullArguments, bound target) error {
	if err := checkPullNumberArgument(a, bound); err != nil {
		return err
	}
	if a.Title == "" && a.Body == nil && a.Base == "" && a.MaintainerCanModify == nil && a.Draft == nil {
		return invalidRequest("name at least one of title, body, base, draft, or maintainer_can_modify to change")
	}
	if a.Title != "" {
		if err := checkPullTitle("title", a.Title); err != nil {
			return err
		}
	}
	if a.Body != nil {
		if err := checkBoundedText("body", *a.Body, maxBodyLength); err != nil {
			return err
		}
	}
	if a.Base != "" && !validRef(a.Base) {
		return invalidRequest("base must be a branch name")
	}
	return nil
}

func checkPullBranchUpdateArguments(a *pullArguments, bound target) error {
	if err := checkPullNumberArgument(a, bound); err != nil {
		return err
	}
	if !validCommitSHA(a.ExpectedHeadSHA) {
		return invalidRequest("expected_head_sha must be a full commit SHA")
	}
	return nil
}

// mergeMethods are the merge strategies GitHub accepts.
var mergeMethods = []string{"merge", "squash", "rebase"}

func checkPullMergeArguments(a *pullArguments, bound target) error {
	if err := checkPullNumberArgument(a, bound); err != nil {
		return err
	}
	if !validCommitSHA(a.SHA) {
		return invalidRequest("sha must be a full commit SHA")
	}
	if a.Method != "" && !containsFold(mergeMethods, a.Method) {
		return invalidRequest("method must be merge, squash, or rebase")
	}
	if a.CommitTitle != "" {
		if err := checkPullTitle("commit_title", a.CommitTitle); err != nil {
			return err
		}
	}
	if a.CommitMessage != "" {
		if err := checkBoundedText("commit_message", a.CommitMessage, maxBodyLength); err != nil {
			return err
		}
	}
	return nil
}

func (a *pullArguments) query() url.Values {
	return url.Values{"per_page": {strconv.Itoa(a.perPage)}, "page": {strconv.Itoa(a.page)}}
}

// pullUpdatePayload is the REST body an update sends: only the fields set travel, and draft travels through
// a GraphQL mutation instead, because GitHub's REST route has none for it.
func (a *pullArguments) pullUpdatePayload() map[string]any {
	payload := map[string]any{}
	if a.Title != "" {
		payload["title"] = a.Title
	}
	if a.Body != nil {
		payload["body"] = *a.Body
	}
	if a.Base != "" {
		payload["base"] = a.Base
	}
	if a.MaintainerCanModify != nil {
		payload["maintainer_can_modify"] = *a.MaintainerCanModify
	}
	return payload
}

// morePage reports whether a REST list without a total count continues after the page just read, based on
// whether GitHub's Link response header announced a following page, and returns the cursor of the next one.
func morePage(binding []byte, page, perPage int, hasNext bool) (bool, string) {
	if !hasNext || page >= maxPage {
		return false, ""
	}
	return true, encodeCursor(binding, strconv.Itoa(page+1)+":"+strconv.Itoa(perPage))
}

// validHead mirrors headPattern: a branch name, or LOGIN:branch for a fork.
func validHead(value string) bool {
	login, branch, ok := strings.Cut(value, ":")
	if !ok {
		return validRef(value)
	}
	return validLogin(login) && validRef(branch)
}

// validSHA accepts a hex commit SHA, full or abbreviated, of the length Git and GitHub use.
func validSHA(value string) bool {
	if len(value) < 4 || len(value) > 40 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

// validCommitSHA accepts only a full commit SHA, the length a merge or a branch update compares exactly,
// unlike validSHA which also accepts an abbreviated one for a read.
func validCommitSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

// Permission messages of the pull request tools. GitHub decides on every request; a message names what such
// a request needs without claiming what the configured token holds.
const (
	pullsReadPermission = "GitHub refused this token the pull requests of this repository; reading them needs " +
		"repo on a classic token for a private repository, or public_repo for a public one, or Pull " +
		"requests: read on a fine-grained token"
	checksReadPermission = "GitHub refused this token the checks of this repository; reading them needs repo " +
		"on a classic token, or Checks: read and Commit statuses: read on a fine-grained token"
	pullsChangePermission = "GitHub refused this change of a pull request of this repository; it needs repo " +
		"on a classic token, or Pull requests: read and write on a fine-grained token"
	pullsMergePermission = "GitHub refused this merge of a pull request of this repository; it needs repo on " +
		"a classic token, or Pull requests: read and write plus Contents: read and write on a fine-grained token"
)

// pullRead performs one bounded REST read of a pull request resource of the bound repository.
func (c *Client) pullRead(ctx context.Context, op, path string, out any) error {
	return actionsFailure(c.rest(ctx, op, path, out), pullsReadPermission)
}

// pullReadPage performs one bounded, paged REST read of a pull request sub-list of the bound repository.
func (c *Client) pullReadPage(ctx context.Context, op, path string, query url.Values, out any) (bool, error) {
	hasNext, err := c.restPage(ctx, op, path, query, out)
	return hasNext, actionsFailure(err, pullsReadPermission)
}

// pullChange sends one bounded REST change of a pull request resource of the bound repository, once, with the
// change permission message of a refusal.
func (c *Client) pullChange(ctx context.Context, op, method, path string, body, out any) error {
	return actionsFailure(c.restChange(ctx, op, method, path, body, out), pullsChangePermission)
}

// PullRequestList is one batch of pull requests.
type PullRequestList struct {
	PullRequests []PullRequestSummary `json:"pull_requests"`
	NextCursor   string               `json:"next_cursor,omitempty"`
	HasMore      bool                 `json:"has_more"`
}

// PullRequestSummary is the compact list view of one pull request. Title is untrusted data.
type PullRequestSummary struct {
	Number     int      `json:"number"`
	Title      string   `json:"title"`
	State      string   `json:"state"`
	Draft      bool     `json:"draft"`
	Author     string   `json:"author,omitempty"`
	HeadBranch string   `json:"head_branch,omitempty"`
	HeadSHA    string   `json:"head_sha,omitempty"`
	BaseBranch string   `json:"base_branch,omitempty"`
	Labels     []string `json:"labels"`
	Merged     bool     `json:"merged"`
	CreatedAt  string   `json:"created_at,omitempty"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
	URL        string   `json:"url,omitempty"`
}

// PullRequest is the full view of one pull request. Title and Body are untrusted data.
type PullRequest struct {
	Number             int      `json:"number"`
	Title              string   `json:"title"`
	Body               string   `json:"body"`
	State              string   `json:"state"`
	Draft              bool     `json:"draft"`
	Author             string   `json:"author,omitempty"`
	HeadBranch         string   `json:"head_branch,omitempty"`
	HeadSHA            string   `json:"head_sha,omitempty"`
	BaseBranch         string   `json:"base_branch,omitempty"`
	BaseSHA            string   `json:"base_sha,omitempty"`
	Labels             []string `json:"labels"`
	RequestedReviewers []string `json:"requested_reviewers"`
	Merged             bool     `json:"merged"`
	Mergeable          *bool    `json:"mergeable,omitempty"`
	MergeableState     string   `json:"mergeable_state,omitempty"`
	MergeCommitSHA     string   `json:"merge_commit_sha,omitempty"`
	Commits            int      `json:"commits"`
	ChangedFiles       int      `json:"changed_files"`
	CreatedAt          string   `json:"created_at,omitempty"`
	UpdatedAt          string   `json:"updated_at,omitempty"`
	ClosedAt           string   `json:"closed_at,omitempty"`
	MergedAt           string   `json:"merged_at,omitempty"`
	URL                string   `json:"url,omitempty"`
}

// pullJSON is the part of a REST pull request this provider reads. mergeable, mergeable_state, commits, and
// changed_files arrive only on the single-pull-request route; the list route leaves them out. NodeID is the
// GraphQL identifier a draft change needs; it arrives on every route.
type pullJSON struct {
	Number int     `json:"number"`
	NodeID string  `json:"node_id"`
	Title  string  `json:"title"`
	Body   *string `json:"body"`
	State  string  `json:"state"`
	Draft  bool    `json:"draft"`
	User   *struct {
		Login string `json:"login"`
	} `json:"user"`
	Head struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"base"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	RequestedReviewers []struct {
		Login string `json:"login"`
	} `json:"requested_reviewers"`
	Merged         bool    `json:"merged"`
	Mergeable      *bool   `json:"mergeable"`
	MergeableState string  `json:"mergeable_state"`
	MergeCommitSHA string  `json:"merge_commit_sha"`
	Commits        int     `json:"commits"`
	ChangedFiles   int     `json:"changed_files"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	ClosedAt       *string `json:"closed_at"`
	MergedAt       *string `json:"merged_at"`
	HTMLURL        string  `json:"html_url"`
}

func (p pullJSON) summary() PullRequestSummary {
	s := PullRequestSummary{Number: p.Number, Title: p.Title, State: p.State, Draft: p.Draft,
		HeadBranch: p.Head.Ref, HeadSHA: p.Head.SHA, BaseBranch: p.Base.Ref, Labels: []string{},
		Merged: p.Merged, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, URL: p.HTMLURL}
	if p.User != nil {
		s.Author = p.User.Login
	}
	for _, label := range p.Labels {
		s.Labels = append(s.Labels, label.Name)
	}
	return s
}

func (p pullJSON) full() *PullRequest {
	s := p.summary()
	full := &PullRequest{Number: s.Number, Title: s.Title, State: s.State, Draft: s.Draft, Author: s.Author,
		HeadBranch: s.HeadBranch, HeadSHA: s.HeadSHA, BaseBranch: s.BaseBranch, BaseSHA: p.Base.SHA,
		Labels: s.Labels, RequestedReviewers: []string{}, Merged: s.Merged, Mergeable: p.Mergeable,
		MergeableState: p.MergeableState, MergeCommitSHA: p.MergeCommitSHA, Commits: p.Commits,
		ChangedFiles: p.ChangedFiles, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, URL: s.URL}
	if p.Body != nil {
		full.Body = *p.Body
	}
	if p.ClosedAt != nil {
		full.ClosedAt = *p.ClosedAt
	}
	if p.MergedAt != nil {
		full.MergedAt = *p.MergedAt
	}
	for _, reviewer := range p.RequestedReviewers {
		full.RequestedReviewers = append(full.RequestedReviewers, reviewer.Login)
	}
	return full
}

// pullRequest reads one pull request of the bound repository by number.
func (c *Client) pullRequest(ctx context.Context, op string, number int) (pullJSON, error) {
	var raw pullJSON
	if err := c.pullRead(ctx, op, c.repoPath("pulls/"+strconv.Itoa(number)), &raw); err != nil {
		return raw, err
	}
	if raw.Number != number {
		return raw, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "GitHub answered with a different pull request than the requested one"}
	}
	return raw, nil
}

func (c *Client) listPulls(ctx context.Context, a *pullArguments) (*PullRequestList, error) {
	const op = "list pull requests"
	query := a.query()
	query.Set("state", a.State)
	if a.Base != "" {
		query.Set("base", a.Base)
	}
	if a.Head != "" {
		query.Set("head", a.Head)
	}
	var raw []pullJSON
	hasNext, err := c.pullReadPage(ctx, op, c.repoPath("pulls"), query, &raw)
	if err != nil {
		return nil, err
	}
	result := &PullRequestList{PullRequests: make([]PullRequestSummary, 0, len(raw))}
	for _, pull := range raw {
		if pull.Number < 1 {
			return nil, invalidEntry(op, "a pull request")
		}
		result.PullRequests = append(result.PullRequests, pull.summary())
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

func (c *Client) getPullRequest(ctx context.Context, number int) (*PullRequest, error) {
	const op = "get pull request"
	raw, err := c.pullRequest(ctx, op, number)
	if err != nil {
		return nil, err
	}
	return raw.full(), nil
}

// PullRequestFileList is one batch of the changed files of a pull request.
type PullRequestFileList struct {
	Files      []PullRequestFile `json:"files"`
	NextCursor string            `json:"next_cursor,omitempty"`
	HasMore    bool              `json:"has_more"`
}

// PullRequestFile is the compact view of one changed file. Patch is untrusted data, cut to maxPatchBytes.
type PullRequestFile struct {
	Path           string `json:"path"`
	Status         string `json:"status"`
	Additions      int    `json:"additions"`
	Deletions      int    `json:"deletions"`
	Changes        int    `json:"changes"`
	PreviousPath   string `json:"previous_path,omitempty"`
	Patch          string `json:"patch,omitempty"`
	PatchTruncated bool   `json:"patch_truncated,omitempty"`
}

type pullFileJSON struct {
	Filename         string `json:"filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Changes          int    `json:"changes"`
	PreviousFilename string `json:"previous_filename"`
	Patch            string `json:"patch"`
}

func (f pullFileJSON) view() PullRequestFile {
	patch, truncated := truncatePatch(f.Patch)
	return PullRequestFile{Path: f.Filename, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions,
		Changes: f.Changes, PreviousPath: f.PreviousFilename, Patch: patch, PatchTruncated: truncated}
}

// truncatePatch bounds one file patch the way jobLog's excerpt bounds a log: at most maxPatchBytes, cut at a
// valid rune boundary, with the cut reported instead of silently shortened.
func truncatePatch(patch string) (string, bool) {
	if len(patch) <= maxPatchBytes {
		return patch, false
	}
	cut := patch[:maxPatchBytes]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut, true
}

func (c *Client) listPullFiles(ctx context.Context, a *pullArguments) (*PullRequestFileList, error) {
	const op = "list pull request files"
	var raw []pullFileJSON
	hasNext, err := c.pullReadPage(ctx, op, c.repoPath("pulls/"+strconv.Itoa(a.Number)+"/files"), a.query(), &raw)
	if err != nil {
		return nil, err
	}
	result := &PullRequestFileList{Files: make([]PullRequestFile, 0, len(raw))}
	for _, file := range raw {
		if file.Filename == "" {
			return nil, invalidEntry(op, "a pull request file")
		}
		result.Files = append(result.Files, file.view())
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// PullRequestCommitList is one batch of the commits of a pull request.
type PullRequestCommitList struct {
	Commits    []PullRequestCommit `json:"commits"`
	NextCursor string              `json:"next_cursor,omitempty"`
	HasMore    bool                `json:"has_more"`
}

// PullRequestCommit is the compact view of one commit. Message is untrusted data.
type PullRequestCommit struct {
	SHA     string `json:"sha"`
	Author  string `json:"author,omitempty"`
	Message string `json:"message"`
	Date    string `json:"date,omitempty"`
	URL     string `json:"url,omitempty"`
}

type pullCommitJSON struct {
	SHA    string `json:"sha"`
	Commit struct {
		Author struct {
			Name string `json:"name"`
			Date string `json:"date"`
		} `json:"author"`
		Message string `json:"message"`
	} `json:"commit"`
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
	HTMLURL string `json:"html_url"`
}

func (p pullCommitJSON) view() PullRequestCommit {
	author := p.Commit.Author.Name
	if p.Author != nil && p.Author.Login != "" {
		author = p.Author.Login
	}
	return PullRequestCommit{SHA: p.SHA, Author: author, Message: p.Commit.Message, Date: p.Commit.Author.Date,
		URL: p.HTMLURL}
}

func (c *Client) listPullCommits(ctx context.Context, a *pullArguments) (*PullRequestCommitList, error) {
	const op = "list pull request commits"
	var raw []pullCommitJSON
	hasNext, err := c.pullReadPage(ctx, op, c.repoPath("pulls/"+strconv.Itoa(a.Number)+"/commits"), a.query(), &raw)
	if err != nil {
		return nil, err
	}
	result := &PullRequestCommitList{Commits: make([]PullRequestCommit, 0, len(raw))}
	for _, commit := range raw {
		if commit.SHA == "" {
			return nil, invalidEntry(op, "a pull request commit")
		}
		result.Commits = append(result.Commits, commit.view())
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// PullRequestDiff is the diff of one pull request, cut to maxDiffBytes from its start. Diff is untrusted data.
type PullRequestDiff struct {
	Number    int    `json:"number"`
	Diff      string `json:"diff"`
	Truncated bool   `json:"truncated"`
}

// pullRequestDiff reads the unified diff of one pull request of the bound repository through the diff media
// type. The diff is read from its start up to maxDiffBytes and never downloaded beyond that bound; it passes
// through memory into the answer only.
func (c *Client) pullRequestDiff(ctx context.Context, number int) (*PullRequestDiff, error) {
	const op = "get pull request diff"
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, provider.Waited(op, "GitHub", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.endpoints.rest+c.repoPath("pulls/"+strconv.Itoa(number)), nil)
	if err != nil {
		return nil, providerError(op, "the request could not be built")
	}
	c.authorize(req)
	req.Header.Set("Accept", "application/vnd.github.diff")
	response, err := c.http.Do(req)
	if err != nil {
		return nil, provider.Transport(op, "GitHub", err)
	}
	defer response.Body.Close()
	c.observeRateLimit(response.Header)
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, actionsFailure(c.statusError(op, response, false), pullsReadPermission)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxDiffBytes+1))
	if err != nil {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "the diff could not be read"}
	}
	truncated := len(data) > maxDiffBytes
	if truncated {
		data = data[:maxDiffBytes]
	}
	return &PullRequestDiff{Number: number, Diff: strings.ToValidUTF8(string(data), ""), Truncated: truncated}, nil
}

// CheckEntry is one compact check run or legacy commit status.
type CheckEntry struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
	URL        string `json:"url,omitempty"`
}

// PullRequestChecks is the combined checks of one pull request at its head commit.
type PullRequestChecks struct {
	Number    int          `json:"number"`
	HeadSHA   string       `json:"head_sha"`
	Checks    []CheckEntry `json:"checks"`
	Overall   string       `json:"overall"`
	Truncated bool         `json:"truncated"`
}

type checkRunJSON struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
	DetailsURL  string `json:"details_url"`
	HTMLURL     string `json:"html_url"`
}

func (r checkRunJSON) entry() CheckEntry {
	updated := r.CompletedAt
	if updated == "" {
		updated = r.StartedAt
	}
	url := r.DetailsURL
	if url == "" {
		url = r.HTMLURL
	}
	return CheckEntry{Name: r.Name, Status: r.Status, Conclusion: r.Conclusion, UpdatedAt: updated, URL: url}
}

type commitStatusJSON struct {
	Context   string `json:"context"`
	State     string `json:"state"`
	TargetURL string `json:"target_url"`
	UpdatedAt string `json:"updated_at"`
}

// entry maps a legacy commit status into the vocabulary of the Checks API: pending stays in progress, and
// the reported state becomes the conclusion of a completed entry.
func (s commitStatusJSON) entry() CheckEntry {
	status, conclusion := "completed", s.State
	if s.State == "pending" {
		status, conclusion = "in_progress", ""
	}
	return CheckEntry{Name: s.Context, Status: status, Conclusion: conclusion, UpdatedAt: s.UpdatedAt, URL: s.TargetURL}
}

// overallState summarises the checks the way a required-checks gate would: any check still running or
// pending outranks a success, and any finished check that did not succeed outranks a pending one.
func overallState(checks []CheckEntry) string {
	if len(checks) == 0 {
		return "none"
	}
	pending, failing := false, false
	for _, entry := range checks {
		switch {
		case entry.Status != "completed":
			pending = true
		case entry.Conclusion == "success" || entry.Conclusion == "neutral" || entry.Conclusion == "skipped":
		default:
			failing = true
		}
	}
	switch {
	case failing:
		return "failure"
	case pending:
		return "pending"
	default:
		return "success"
	}
}

// pullRequestChecks reads the pull request to resolve its head commit, then the check runs and the combined
// commit status at that commit, and merges them into one compact, evaluated list.
func (c *Client) pullRequestChecks(ctx context.Context, number int) (*PullRequestChecks, error) {
	const op = "list pull request checks"
	pull, err := c.pullRequest(ctx, op, number)
	if err != nil {
		return nil, err
	}
	sha := pull.Head.SHA
	if !validSHA(sha) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
			Message: "GitHub answered the pull request without a usable head commit"}
	}
	var runs struct {
		TotalCount int            `json:"total_count"`
		CheckRuns  []checkRunJSON `json:"check_runs"`
	}
	if err := c.rest(ctx, op, c.repoPath("commits/"+sha+"/check-runs")+"?per_page=100", &runs); err != nil {
		return nil, actionsFailure(err, checksReadPermission)
	}
	var combined struct {
		TotalCount int                `json:"total_count"`
		Statuses   []commitStatusJSON `json:"statuses"`
	}
	if err := c.rest(ctx, op, c.repoPath("commits/"+sha+"/status")+"?per_page=100", &combined); err != nil {
		return nil, actionsFailure(err, checksReadPermission)
	}
	checks := make([]CheckEntry, 0, len(runs.CheckRuns)+len(combined.Statuses))
	for _, run := range runs.CheckRuns {
		checks = append(checks, run.entry())
	}
	for _, status := range combined.Statuses {
		checks = append(checks, status.entry())
	}
	return &PullRequestChecks{Number: number, HeadSHA: sha, Checks: checks, Overall: overallState(checks),
		Truncated: runs.TotalCount > len(runs.CheckRuns) || combined.TotalCount > len(combined.Statuses)}, nil
}

// createPullRequest opens one pull request in the bound repository with exactly one request, which is never
// repeated.
func (c *Client) createPullRequest(ctx context.Context, a *pullArguments) (*PullRequest, error) {
	const op = "create pull request"
	payload := map[string]any{"title": a.Title, "head": a.Head, "base": a.Base}
	if a.Body != nil {
		payload["body"] = *a.Body
	}
	if a.Draft != nil {
		payload["draft"] = *a.Draft
	}
	if a.MaintainerCanModify != nil {
		payload["maintainer_can_modify"] = *a.MaintainerCanModify
	}
	var raw pullJSON
	if err := c.pullChange(ctx, op, http.MethodPost, c.repoPath("pulls"), payload, &raw); err != nil {
		return nil, err
	}
	return raw.full(), nil
}

// The GraphQL mutations of a draft change. GitHub has no REST field for draft; both take the pull request's
// node identifier and answer whether it is a draft afterwards.
const (
	markReadyForReviewMutation = `mutation($id:ID!){ready:markPullRequestReadyForReview(` +
		`input:{pullRequestId:$id}){pullRequest{id isDraft}}}`
	convertToDraftMutation = `mutation($id:ID!){draft:convertPullRequestToDraft(` +
		`input:{pullRequestId:$id}){pullRequest{id isDraft}}}`
)

// setPullRequestDraft marks the pull request the node identifier names ready for review, or converts it to a
// draft, in one mutation that is never repeated.
func (c *Client) setPullRequestDraft(ctx context.Context, op, nodeID string, draft bool) error {
	if nodeID == "" {
		return invalidResponse(op, true)
	}
	document := markReadyForReviewMutation
	if draft {
		document = convertToDraftMutation
	}
	var answer struct {
		Ready *json.RawMessage `json:"ready"`
		Draft *json.RawMessage `json:"draft"`
	}
	if err := c.mutate(ctx, op, document, map[string]any{"id": nodeID}, &answer); err != nil {
		return err
	}
	if answer.Ready == nil && answer.Draft == nil {
		return invalidResponse(op, true)
	}
	return nil
}

// updatePullRequest replaces the named REST fields of one pull request in one request, sends the draft
// change of a separate GraphQL mutation when one was named, and reads the pull request again only when a
// draft change leaves its REST answer stale.
func (c *Client) updatePullRequest(ctx context.Context, a *pullArguments) (*PullRequest, error) {
	const op = "update pull request"
	current, err := c.pullRequest(ctx, op, a.Number)
	if err != nil {
		return nil, err
	}
	if payload := a.pullUpdatePayload(); len(payload) > 0 {
		var raw pullJSON
		if err := c.pullChange(ctx, op, http.MethodPatch, c.repoPath("pulls/"+strconv.Itoa(a.Number)), payload,
			&raw); err != nil {
			return nil, err
		}
		current = raw
	}
	if a.Draft != nil {
		if err := c.setPullRequestDraft(ctx, op, current.NodeID, *a.Draft); err != nil {
			return nil, err
		}
		refreshed, err := c.pullRequest(ctx, op, a.Number)
		if err != nil {
			return nil, err
		}
		current = refreshed
	}
	return current.full(), nil
}

// closePullRequest closes one open pull request. A pull request already closed, merged or not, is reported
// unchanged without a request, so a repeated close never fails.
func (c *Client) closePullRequest(ctx context.Context, number int) (*PullRequest, error) {
	const op = "close pull request"
	current, err := c.pullRequest(ctx, op, number)
	if err != nil {
		return nil, err
	}
	if current.State == "closed" {
		return current.full(), nil
	}
	var raw pullJSON
	if err := c.pullChange(ctx, op, http.MethodPatch, c.repoPath("pulls/"+strconv.Itoa(number)),
		map[string]any{"state": "closed"}, &raw); err != nil {
		return nil, err
	}
	return raw.full(), nil
}

// reopenPullRequest opens one closed pull request again. A pull request already open is reported unchanged
// without a request; a merged pull request cannot be reopened and is refused before one is sent.
func (c *Client) reopenPullRequest(ctx context.Context, number int) (*PullRequest, error) {
	const op = "reopen pull request"
	current, err := c.pullRequest(ctx, op, number)
	if err != nil {
		return nil, err
	}
	if current.State == "open" {
		return current.full(), nil
	}
	if current.Merged {
		return nil, invalidRequest(fmt.Sprintf("pull request #%d is already merged and cannot be reopened", number))
	}
	var raw pullJSON
	if err := c.pullChange(ctx, op, http.MethodPatch, c.repoPath("pulls/"+strconv.Itoa(number)),
		map[string]any{"state": "open"}, &raw); err != nil {
		return nil, err
	}
	return raw.full(), nil
}

// PullRequestBranchUpdate is the answer of a head branch update: GitHub queues the merge of the base branch
// into the head branch and answers before it finishes.
type PullRequestBranchUpdate struct {
	Number   int  `json:"number"`
	Accepted bool `json:"accepted"`
}

// pullBranchUpdateFailure names what a refused branch update most likely means; every other failure keeps
// its own message.
func pullBranchUpdateFailure(err error, number int) error {
	var failure *provider.Error
	if !errors.As(err, &failure) || failure.Message != rejectedMessage {
		return err
	}
	refused := *failure
	refused.Message = fmt.Sprintf("GitHub rejected the update of pull request #%d's head branch, so nothing "+
		"was queued; expected_head_sha may no longer be its current head commit, read the pull request again",
		number)
	return &refused
}

// updatePullRequestBranch merges the base branch into the head branch of one pull request, only while its
// head is still expectedHeadSHA, in one request that is never repeated; GitHub applies it asynchronously.
func (c *Client) updatePullRequestBranch(ctx context.Context, number int, expectedHeadSHA string) (*PullRequestBranchUpdate, error) {
	const op = "update pull request branch"
	if _, err := c.pullRequest(ctx, op, number); err != nil {
		return nil, err
	}
	err := c.pullChange(ctx, op, http.MethodPut, c.repoPath("pulls/"+strconv.Itoa(number)+"/update-branch"),
		map[string]any{"expected_head_sha": expectedHeadSHA}, nil)
	if err != nil {
		return nil, pullBranchUpdateFailure(err, number)
	}
	return &PullRequestBranchUpdate{Number: number, Accepted: true}, nil
}

// PullRequestMerge is the answer of a merge: the pull request is merged with the reported merge commit SHA.
type PullRequestMerge struct {
	Number int    `json:"number"`
	Merged bool   `json:"merged"`
	SHA    string `json:"sha,omitempty"`
}

// mergeFailure names what a refused merge most likely means. Its own permission message names the write
// access a merge needs beside a pull request change, Contents included. A conflict means GitHub compared sha
// against a head that has since moved; the pull request is read again to name the current one, and the
// refusal becomes an invalid request that names both, since the caller must read the current state before
// merging again. A pull request GitHub refuses to merge in its current state, for example branch protection,
// a missing review, or a failing check, keeps its own provider message naming likely reasons. Every other
// failure keeps its own message.
func (c *Client) mergeFailure(ctx context.Context, op string, number int, expected string, err error) error {
	var failure *provider.Error
	if !errors.As(err, &failure) {
		return err
	}
	if failure.Class == provider.ClassPermission {
		refused := *failure
		refused.Message = pullsMergePermission
		return &refused
	}
	switch failure.Message {
	case conflictMessage:
		current := "unknown"
		if pull, readErr := c.pullRequest(ctx, op, number); readErr == nil {
			current = pull.Head.SHA
		}
		return invalidRequest(fmt.Sprintf("GitHub refused to merge pull request #%d because its head branch "+
			"changed: expected head commit %s, now %s; read the pull request again and merge with its current "+
			"head commit, or accept the current one and merge again", number, expected, current))
	case methodNotAllowedMessage:
		return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: fmt.Sprintf(
			"GitHub refused to merge pull request #%d because it is not mergeable in its current state, for "+
				"example branch protection, a missing required review, or a failing check; check its merge "+
				"state and checks before trying again", number)}
	}
	return err
}

// mergePullRequest merges one pull request, only while its head is still sha, in one request that is never
// repeated. A pull request already merged with exactly this head commit is reported as merged without a
// request, so a repeated merge with the same sha never fails.
func (c *Client) mergePullRequest(ctx context.Context, a *pullArguments) (*PullRequestMerge, error) {
	const op = "merge pull request"
	current, err := c.pullRequest(ctx, op, a.Number)
	if err != nil {
		return nil, err
	}
	if current.Merged {
		if current.Head.SHA == a.SHA {
			return &PullRequestMerge{Number: a.Number, Merged: true, SHA: current.MergeCommitSHA}, nil
		}
		return nil, invalidRequest(fmt.Sprintf("pull request #%d is already merged with head commit %s, not %s",
			a.Number, current.Head.SHA, a.SHA))
	}
	payload := map[string]any{"sha": a.SHA}
	if a.Method != "" {
		payload["merge_method"] = a.Method
	}
	if a.CommitTitle != "" {
		payload["commit_title"] = a.CommitTitle
	}
	if a.CommitMessage != "" {
		payload["commit_message"] = a.CommitMessage
	}
	var raw struct {
		SHA    string `json:"sha"`
		Merged bool   `json:"merged"`
	}
	if err := c.restChange(ctx, op, http.MethodPut, c.repoPath("pulls/"+strconv.Itoa(a.Number)+"/merge"), payload,
		&raw); err != nil {
		return nil, c.mergeFailure(ctx, op, a.Number, a.SHA, err)
	}
	if !raw.Merged {
		return nil, invalidResponse(op, true)
	}
	return &PullRequestMerge{Number: a.Number, Merged: true, SHA: raw.SHA}, nil
}
