package github

import (
	"context"
	"encoding/json"
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
	}
}

// pullArguments holds the arguments of every pull request tool; the input schema of each tool admits only
// its own. page and perPage are derived by the checks.
type pullArguments struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	Base   string `json:"base"`
	Head   string `json:"head"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`

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

func (a *pullArguments) query() url.Values {
	return url.Values{"per_page": {strconv.Itoa(a.perPage)}, "page": {strconv.Itoa(a.page)}}
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

// Permission messages of the pull request tools. GitHub decides on every request; a message names what such
// a request needs without claiming what the configured token holds.
const (
	pullsReadPermission = "GitHub refused this token the pull requests of this repository; reading them needs " +
		"repo on a classic token for a private repository, or public_repo for a public one, or Pull " +
		"requests: read on a fine-grained token"
	checksReadPermission = "GitHub refused this token the checks of this repository; reading them needs repo " +
		"on a classic token, or Checks: read and Commit statuses: read on a fine-grained token"
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
// changed_files arrive only on the single-pull-request route; the list route leaves them out.
type pullJSON struct {
	Number int     `json:"number"`
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
