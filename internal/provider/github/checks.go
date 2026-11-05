package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// The check tools read the check runs, annotations, and check suites of a repository. Check runs and suites
// are addressed by identifiers that GitHub scopes below the repository of the request. The answer of a
// check run or suite is the only place that names its repository, so it is compared with the selected
// repository before any value leaves the call. Titles, output texts, annotations, names, and addresses are
// untrusted data; an address is never fetched.

const checksReadPermissionMessage = "GitHub refused this token the checks of this repository; reading them " +
	"needs repo on a classic token for a private repository, or public_repo for a public one, or Checks: read " +
	"on a fine-grained token"

var (
	checkSuiteLinkKeys = []string{"url", "check_runs_url"}
	checkAnnotationKey = []string{"blob_href"}
)

const (
	checkRunRefFilters = `"check_name":{"type":"string","minLength":1,"maxLength":100},` +
		`"status":{"type":"string","enum":["queued","in_progress","completed"]},` +
		`"filter":{"type":"string","enum":["latest","all"]}`
	checkSuiteFilters = `"app_id":` + actionsIDSchema + `,"check_name":{"type":"string","minLength":1,"maxLength":100}`
)

var checkRefArgument = capability.Argument{Name: "ref", Description: "Branch, tag, or commit SHA whose checks " +
	"are read", Required: true}

var checkRunFilterArguments = []capability.Argument{
	{Name: "check_name", Description: "Return only check runs with exactly this name, up to 100 characters"},
	{Name: "status", Description: "Return only check runs in this status: queued, in_progress, or completed"},
	{Name: "filter", Description: "latest returns the latest check run of each name, all every one; GitHub's " +
		"own choice, latest, when omitted"},
}

var checkAppArgument = capability.Argument{Name: "app_id",
	Description: "Return only check runs of the GitHub App with this numeric id"}

var checkSuiteFilterArguments = []capability.Argument{
	{Name: "app_id", Description: "Return only check suites of the GitHub App with this numeric id"},
	{Name: "check_name", Description: "Return only check suites that hold a check run with exactly this name, up to 100 characters"},
}

var checkRunsTotalField = capability.Field{Name: "total_count",
	Description: "Number of check runs GitHub holds for the request and its filters"}

// ---- Descriptors

var checkRunsList = capability.Descriptor{
	ID:      Provider + ".check-runs.list",
	Version: 1,
	Title:   "List the check runs of a GitHub commit",
	Description: "List one bounded batch of the check runs of a branch, tag, or commit of a repository a " +
		"connection allows, with their output, app, pull requests, and deployment; by default only the latest " +
		"check run of each name",
	Tags:         []string{"github", "checks", "checkruns", "list"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(`"ref":`+refSchema+`,`+checkRunRefFilters+`,"app_id":`+actionsIDSchema+`,`+pagingKeys, "ref"),
	OutputSchema: checkListOutput("ref", `{"type":"string"}`, "check_runs", checkRunProperties),
	Arguments: append(append(append([]capability.Argument{checkRefArgument}, checkRunFilterArguments...),
		checkAppArgument), pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "ref", Description: "Branch, tag, or commit SHA the check runs were read at"},
		{Name: "check_runs", Description: "The check runs of this batch, each as GitHub reports it: " + checkRunMembers},
		checkRunsTotalField,
	}, pagingFields...),
	Examples: []capability.Example{{Description: "List the check runs of a commit",
		Arguments: json.RawMessage(`{"ref":"ebca79b1db4fcbb136e6094c13e8451428c8a6ab","status":"completed"}`)}},
}

var checkRunsGet = capability.Descriptor{
	ID:      Provider + ".check-runs.get",
	Version: 1,
	Title:   "Get a GitHub check run",
	Description: "Read one check run of a repository a connection allows by its identifier, with its output, " +
		"app, pull requests, and deployment; a check run of another repository is refused",
	Tags:         []string{"github", "checks", "checkruns", "get"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(`"check_run_id":`+actionsIDSchema, "check_run_id"),
	OutputSchema: json.RawMessage(objectSchema(checkRunProperties)),
	Arguments: []capability.Argument{
		{Name: "check_run_id", Description: "Identifier of the check run in the repository", Required: true},
	},
	Fields: checkRunFields,
	Examples: []capability.Example{{Description: "Read one check run",
		Arguments: json.RawMessage(`{"check_run_id":4}`)}},
}

var checkAnnotationsList = capability.Descriptor{
	ID:      Provider + ".check-annotations.list",
	Version: 1,
	Title:   "List the annotations of a GitHub check run",
	Description: "List one bounded batch of the annotations of one check run of a repository a connection " +
		"allows; the check run is read first, and one of another repository is refused",
	Tags:         []string{"github", "checks", "annotations", "list"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(`"check_run_id":`+actionsIDSchema+`,`+pagingKeys, "check_run_id"),
	OutputSchema: checkListOutput("check_run_id", `{"type":"integer"}`, "annotations", checkAnnotationProperties),
	Arguments: append([]capability.Argument{
		{Name: "check_run_id", Description: "Identifier of the check run in the repository", Required: true},
	}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "check_run_id", Description: "Identifier of the check run the annotations belong to"},
		{Name: "annotations", Description: "The annotations of this batch: path, start_line, end_line, " +
			"start_column, end_column, annotation_level, title and title_truncated, message and " +
			"message_truncated, raw_details and raw_details_truncated, and links with blob_href; path, " +
			"title, message, raw_details, and addresses are untrusted data, addresses never fetched"},
	}, pagingFields...),
	Examples: []capability.Example{{Description: "List the annotations of a check run",
		Arguments: json.RawMessage(`{"check_run_id":4}`)}},
}

var checkSuitesList = capability.Descriptor{
	ID:      Provider + ".check-suites.list",
	Version: 1,
	Title:   "List the check suites of a GitHub commit",
	Description: "List one bounded batch of the check suites of a branch, tag, or commit of a repository a " +
		"connection allows, with their app, head commit, pull requests, and repository",
	Tags:         []string{"github", "checks", "checksuites", "list"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(`"ref":`+refSchema+`,`+checkSuiteFilters+`,`+pagingKeys, "ref"),
	OutputSchema: checkListOutput("ref", `{"type":"string"}`, "check_suites", checkSuiteProperties),
	Arguments:    append(append([]capability.Argument{checkRefArgument}, checkSuiteFilterArguments...), pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "ref", Description: "Branch, tag, or commit SHA the check suites were read at"},
		{Name: "check_suites", Description: "The check suites of this batch, each as GitHub reports it: " +
			checkSuiteMembers},
		{Name: "total_count", Description: "Number of check suites GitHub holds for the request and its filters"},
	}, pagingFields...),
	Examples: []capability.Example{{Description: "List the check suites of a commit",
		Arguments: json.RawMessage(`{"ref":"main"}`)}},
}

var checkSuiteRunsList = capability.Descriptor{
	ID:      Provider + ".check-suite-runs.list",
	Version: 1,
	Title:   "List the check runs of a GitHub check suite",
	Description: "List one bounded batch of the check runs of one check suite of a repository a connection " +
		"allows; the check suite is read first, and one of another repository is refused; by default only the " +
		"latest check run of each name",
	Tags:     []string{"github", "checks", "checksuites", "checkruns", "list"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: inputSchema(`"check_suite_id":`+actionsIDSchema+`,`+checkRunRefFilters+`,`+pagingKeys,
		"check_suite_id"),
	OutputSchema: checkListOutput("check_suite_id", `{"type":"integer"}`, "check_runs", checkRunProperties),
	Arguments: append(append([]capability.Argument{
		{Name: "check_suite_id", Description: "Identifier of the check suite in the repository", Required: true},
	}, checkRunFilterArguments...), pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "check_suite_id", Description: "Identifier of the check suite the check runs belong to"},
		{Name: "check_runs", Description: "The check runs of this batch, each as GitHub reports it: " + checkRunMembers},
		checkRunsTotalField,
	}, pagingFields...),
	Examples: []capability.Example{{Description: "List the check runs of a check suite",
		Arguments: json.RawMessage(`{"check_suite_id":5}`)}},
}

// checkListOutput is the schema of a paged check list: the subject it was read for, the entries, the total
// GitHub reports, and the paging members.
func checkListOutput(subject, subjectSchema, key, entry string) json.RawMessage {
	total := `"total_count":{"type":"integer"},`
	if key == "annotations" {
		total = ""
	}
	return json.RawMessage(`{"type":"object","properties":{"` + subject + `":` + subjectSchema + `,"` + key +
		`":{"type":"array","items":` + objectSchema(entry) + `},` + total + `"next_cursor":{"type":"string"},` +
		`"has_more":{"type":"boolean"}},"required":["` + subject + `","` + key + `","has_more"],` +
		`"additionalProperties":false}`)
}

// checkRunFields documents the top-level members of one check run.
var checkRunFields = []capability.Field{
	{Name: "id", Description: "Identifier of the check run"},
	{Name: "node_id", Description: "GraphQL node id of the check run"},
	{Name: "external_id", Description: "Reference the app set for the check run, untrusted data"},
	{Name: "head_sha", Description: "Commit the check run belongs to"},
	{Name: "name", Description: "Name of the check run, untrusted data"},
	{Name: "status", Description: "Lifecycle phase, such as queued, in_progress, or completed"},
	{Name: "conclusion", Description: "Result of a completed check run, such as success or failure"},
	{Name: "started_at", Description: "When the check run started"},
	{Name: "completed_at", Description: "When the check run completed"},
	{Name: "url", Description: "Web address of the check run, untrusted data, never fetched"},
	{Name: "links", Description: "REST url and details_url of the check run by their GitHub names, untrusted " +
		"data, never fetched"},
	{Name: "output", Description: "Title, summary, and text, each cut with a marker (title_truncated, " +
		"summary_truncated, text_truncated), annotations_count, and links with annotations_url; untrusted data"},
	{Name: "check_suite", Description: "Check suite the check run belongs to: id"},
	{Name: "app", Description: "GitHub App that created the check run: " + restAppMembers},
	{Name: "pull_requests", Description: "Pull requests with this head commit or branch, at most 100: id, " +
		"number, head, base, links; not necessarily the ones that triggered the check run"},
	{Name: "pull_requests_truncated", Description: "True when the check run has more pull requests than listed"},
	{Name: "deployment", Description: "Deployment the check run belongs to: id, node_id, task, " +
		"original_environment, environment, description, created_at, updated_at, transient_environment, " +
		"production_environment, performed_via_github_app, links; untrusted data"},
}

// ---- Annotations

type checkAnnotationRaw struct {
	Path        *string `json:"path"`
	StartLine   *int64  `json:"start_line"`
	EndLine     *int64  `json:"end_line"`
	StartColumn *int64  `json:"start_column"`
	EndColumn   *int64  `json:"end_column"`
	Level       *string `json:"annotation_level"`
	Title       *string `json:"title"`
	Message     *string `json:"message"`
	RawDetails  *string `json:"raw_details"`
	links       Links
}

func (r *checkAnnotationRaw) UnmarshalJSON(data []byte) (err error) {
	type plain checkAnnotationRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, checkAnnotationKey)
	return err
}

// CheckAnnotation is one annotation of a check run. Path, title, message, and raw_details are untrusted data.
type CheckAnnotation struct {
	Path                *string `json:"path,omitempty"`
	StartLine           *int64  `json:"start_line,omitempty"`
	EndLine             *int64  `json:"end_line,omitempty"`
	StartColumn         *int64  `json:"start_column,omitempty"`
	EndColumn           *int64  `json:"end_column,omitempty"`
	AnnotationLevel     *string `json:"annotation_level,omitempty"`
	Title               *string `json:"title,omitempty"`
	TitleTruncated      bool    `json:"title_truncated,omitempty"`
	Message             *string `json:"message,omitempty"`
	MessageTruncated    bool    `json:"message_truncated,omitempty"`
	RawDetails          *string `json:"raw_details,omitempty"`
	RawDetailsTruncated bool    `json:"raw_details_truncated,omitempty"`
	Links               Links   `json:"links,omitempty"`
}

func (r *checkAnnotationRaw) project() CheckAnnotation {
	out := CheckAnnotation{Path: longText(r.Path), StartLine: r.StartLine, EndLine: r.EndLine,
		StartColumn: r.StartColumn, EndColumn: r.EndColumn, AnnotationLevel: nameText(r.Level), Links: r.links}
	out.Title, out.TitleTruncated = cleanText(r.Title, projectionTextLimit, false)
	out.Message, out.MessageTruncated = cleanText(r.Message, projectionBodyLimit, true)
	out.RawDetails, out.RawDetailsTruncated = cleanText(r.RawDetails, projectionBodyLimit, true)
	return out
}

var checkAnnotationProperties = `"path":{"type":"string"},"start_line":{"type":"integer"},` +
	`"end_line":{"type":"integer"},"start_column":{"type":"integer"},"end_column":{"type":"integer"},` +
	`"annotation_level":{"type":"string"},"title":{"type":"string"},"title_truncated":{"type":"boolean"},` +
	`"message":{"type":"string"},"message_truncated":{"type":"boolean"},"raw_details":{"type":"string"},` +
	`"raw_details_truncated":{"type":"boolean"},"links":` + linksSchema(checkAnnotationKey)

// ---- Check suites

type checkSuiteRaw struct {
	ID                   *int64         `json:"id"`
	NodeID               *string        `json:"node_id"`
	HeadBranch           *string        `json:"head_branch"`
	HeadSHA              *string        `json:"head_sha"`
	Status               *string        `json:"status"`
	Conclusion           *string        `json:"conclusion"`
	Before               *string        `json:"before"`
	After                *string        `json:"after"`
	PullRequests         []*runPullRaw  `json:"pull_requests"`
	App                  *appRaw        `json:"app"`
	Repository           *repositoryRaw `json:"repository"`
	CreatedAt            *string        `json:"created_at"`
	UpdatedAt            *string        `json:"updated_at"`
	HeadCommit           *runCommitRaw  `json:"head_commit"`
	LatestCheckRunsCount *int64         `json:"latest_check_runs_count"`
	Rerequestable        *bool          `json:"rerequestable"`
	RunsRerequestable    *bool          `json:"runs_rerequestable"`
	links                Links
}

func (r *checkSuiteRaw) UnmarshalJSON(data []byte) (err error) {
	type plain checkSuiteRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, checkSuiteLinkKeys)
	return err
}

// CheckSuite is one check suite as GitHub's check-suite object reports it.
type CheckSuite struct {
	ID                    *int64           `json:"id,omitempty"`
	NodeID                *string          `json:"node_id,omitempty"`
	HeadBranch            *string          `json:"head_branch,omitempty"`
	HeadSHA               *string          `json:"head_sha,omitempty"`
	Status                *string          `json:"status,omitempty"`
	Conclusion            *string          `json:"conclusion,omitempty"`
	Before                *string          `json:"before,omitempty"`
	After                 *string          `json:"after,omitempty"`
	PullRequests          []RunPullRequest `json:"pull_requests,omitempty"`
	PullRequestsTruncated bool             `json:"pull_requests_truncated,omitempty"`
	App                   *RESTApp         `json:"app,omitempty"`
	Repository            *RESTRepository  `json:"repository,omitempty"`
	CreatedAt             *string          `json:"created_at,omitempty"`
	UpdatedAt             *string          `json:"updated_at,omitempty"`
	HeadCommit            *RunHeadCommit   `json:"head_commit,omitempty"`
	LatestCheckRunsCount  *int64           `json:"latest_check_runs_count,omitempty"`
	Rerequestable         *bool            `json:"rerequestable,omitempty"`
	RunsRerequestable     *bool            `json:"runs_rerequestable,omitempty"`
	Links                 Links            `json:"links,omitempty"`
}

func (r *checkSuiteRaw) project(allowed allowlist) CheckSuite {
	out := CheckSuite{ID: r.ID, NodeID: nameText(r.NodeID), HeadBranch: nameText(r.HeadBranch),
		HeadSHA: nameText(r.HeadSHA), Status: nameText(r.Status), Conclusion: nameText(r.Conclusion),
		Before: nameText(r.Before), After: nameText(r.After), App: r.App.project(),
		Repository: r.Repository.project(allowed), CreatedAt: nameText(r.CreatedAt), UpdatedAt: nameText(r.UpdatedAt),
		HeadCommit: r.HeadCommit.project(), LatestCheckRunsCount: r.LatestCheckRunsCount,
		Rerequestable: r.Rerequestable, RunsRerequestable: r.RunsRerequestable, Links: r.links}
	out.PullRequests, out.PullRequestsTruncated = projectRunPulls(r.PullRequests)
	return out
}

var checkSuiteProperties = `"id":{"type":"integer"},"node_id":{"type":"string"},"head_branch":{"type":"string"},` +
	`"head_sha":{"type":"string"},"status":{"type":"string"},"conclusion":{"type":"string"},` +
	`"before":{"type":"string"},"after":{"type":"string"},` +
	`"pull_requests":{"type":"array","maxItems":100,"items":` + checkRunPullSchema + `},` +
	`"pull_requests_truncated":{"type":"boolean"},"app":` + restAppSchema + `,"repository":` +
	restRepositorySchema + `,"created_at":{"type":"string"},"updated_at":{"type":"string"},` +
	`"head_commit":` + runHeadCommitSchema + `,"latest_check_runs_count":{"type":"integer"},` +
	`"rerequestable":{"type":"boolean"},"runs_rerequestable":{"type":"boolean"},` +
	`"links":` + linksSchema(checkSuiteLinkKeys)

const checkSuiteMembers = "id, node_id, head_branch, head_sha, status, conclusion, before, after, " +
	"pull_requests (id, number, head, base, links, at most 100) with pull_requests_truncated, app (" + restAppMembers +
	"), repository (a repository outside the targets of the connection shows only id, node_id, and full_name), " +
	"created_at, updated_at, head_commit (id, tree_id, message cut with message_truncated, timestamp, author, " +
	"committer), latest_check_runs_count, rerequestable, runs_rerequestable, and links (the REST url and " +
	"check_runs_url); names, texts, and addresses are untrusted data, addresses never fetched"

// ---- Output types

// CheckRunList is one batch of the check runs of a commit.
type CheckRunList struct {
	Ref        string     `json:"ref"`
	CheckRuns  []CheckRun `json:"check_runs"`
	TotalCount *int64     `json:"total_count,omitempty"`
	NextCursor string     `json:"next_cursor,omitempty"`
	HasMore    bool       `json:"has_more"`
}

// CheckSuiteRunList is one batch of the check runs of a check suite.
type CheckSuiteRunList struct {
	CheckSuiteID int64      `json:"check_suite_id"`
	CheckRuns    []CheckRun `json:"check_runs"`
	TotalCount   *int64     `json:"total_count,omitempty"`
	NextCursor   string     `json:"next_cursor,omitempty"`
	HasMore      bool       `json:"has_more"`
}

// CheckAnnotationList is one batch of the annotations of a check run.
type CheckAnnotationList struct {
	CheckRunID  int64             `json:"check_run_id"`
	Annotations []CheckAnnotation `json:"annotations"`
	NextCursor  string            `json:"next_cursor,omitempty"`
	HasMore     bool              `json:"has_more"`
}

// CheckSuiteList is one batch of the check suites of a commit.
type CheckSuiteList struct {
	Ref         string       `json:"ref"`
	CheckSuites []CheckSuite `json:"check_suites"`
	TotalCount  *int64       `json:"total_count,omitempty"`
	NextCursor  string       `json:"next_cursor,omitempty"`
	HasMore     bool         `json:"has_more"`
}

// ---- Arguments

// checkArguments holds the arguments of every check tool; the input schema of each tool admits only its own.
// page, perPage, and binding are derived by the checks.
type checkArguments struct {
	Ref          string `json:"ref"`
	CheckRunID   int64  `json:"check_run_id"`
	CheckSuiteID int64  `json:"check_suite_id"`
	CheckName    string `json:"check_name"`
	Status       string `json:"status"`
	Filter       string `json:"filter"`
	AppID        *int64 `json:"app_id"`
	Limit        int    `json:"limit"`
	Cursor       string `json:"cursor"`

	page, perPage int
	binding       []byte
}

func checksHandler(id string, check func(*checkArguments, target) error,
	call func(context.Context, *Client, *checkArguments) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments checkArguments
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

func checkIdentifier(name string, id int64) error {
	if id < 1 || id > maxActionsID {
		return invalidRequest(name + " must be a positive identifier")
	}
	return nil
}

// checkPaging derives the page of a list whose cursor is bound to the tool, the target, and every filter.
func (a *checkArguments) checkPaging(tool string, bound target, filters ...any) error {
	limit, err := normalizeLimit(a.Limit)
	if err != nil {
		return err
	}
	parts := append([]any{"checks", tool, bound.String()}, filters...)
	a.binding = fingerprint(parts...)
	a.page, a.perPage, err = pageOf(a.binding, a.Cursor, limit)
	return err
}

func checkRefArgumentValue(ref string) error {
	if !validRef(ref) {
		return invalidRequest("ref must be a branch, a tag, or a commit SHA")
	}
	return nil
}

func checkRunsListArguments(a *checkArguments, bound target) error {
	if err := checkRefArgumentValue(a.Ref); err != nil {
		return err
	}
	if err := checkFilterValues(a.CheckName, a.Status, a.Filter, a.AppID); err != nil {
		return err
	}
	return a.checkPaging("runs", bound, a.Ref, a.CheckName, a.Status, a.Filter, a.AppID)
}

func checkRunsGetArguments(a *checkArguments, _ target) error {
	return checkIdentifier("check_run_id", a.CheckRunID)
}

func checkAnnotationsListArguments(a *checkArguments, bound target) error {
	if err := checkIdentifier("check_run_id", a.CheckRunID); err != nil {
		return err
	}
	return a.checkPaging("annotations", bound, a.CheckRunID)
}

func checkSuitesListArguments(a *checkArguments, bound target) error {
	if err := checkRefArgumentValue(a.Ref); err != nil {
		return err
	}
	if err := checkFilterValues(a.CheckName, "", "", a.AppID); err != nil {
		return err
	}
	return a.checkPaging("suites", bound, a.Ref, a.CheckName, a.AppID)
}

func checkSuiteRunsListArguments(a *checkArguments, bound target) error {
	if err := checkIdentifier("check_suite_id", a.CheckSuiteID); err != nil {
		return err
	}
	if err := checkFilterValues(a.CheckName, a.Status, a.Filter, nil); err != nil {
		return err
	}
	return a.checkPaging("suite-runs", bound, a.CheckSuiteID, a.CheckName, a.Status, a.Filter)
}

// query is the page and each filter that is set.
func (a *checkArguments) query() url.Values {
	query := url.Values{"per_page": {strconv.Itoa(a.perPage)}, "page": {strconv.Itoa(a.page)}}
	if a.CheckName != "" {
		query.Set("check_name", a.CheckName)
	}
	if a.Status != "" {
		query.Set("status", a.Status)
	}
	if a.Filter != "" {
		query.Set("filter", a.Filter)
	}
	if a.AppID != nil {
		query.Set("app_id", strconv.FormatInt(*a.AppID, 10))
	}
	return query
}

// ---- Operations

func checkOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: checkRunsList, Handler: checksHandler(checkRunsList.ID, checkRunsListArguments,
			func(ctx context.Context, c *Client, a *checkArguments) (any, error) { return c.listCheckRuns(ctx, a) })},
		{Descriptor: checkRunsGet, Handler: checksHandler(checkRunsGet.ID, checkRunsGetArguments,
			func(ctx context.Context, c *Client, a *checkArguments) (any, error) { return c.getCheckRun(ctx, a) })},
		{Descriptor: checkAnnotationsList, Handler: checksHandler(checkAnnotationsList.ID, checkAnnotationsListArguments,
			func(ctx context.Context, c *Client, a *checkArguments) (any, error) {
				return c.listCheckAnnotations(ctx, a)
			})},
		{Descriptor: checkSuitesList, Handler: checksHandler(checkSuitesList.ID, checkSuitesListArguments,
			func(ctx context.Context, c *Client, a *checkArguments) (any, error) { return c.listCheckSuites(ctx, a) })},
		{Descriptor: checkSuiteRunsList, Handler: checksHandler(checkSuiteRunsList.ID, checkSuiteRunsListArguments,
			func(ctx context.Context, c *Client, a *checkArguments) (any, error) {
				return c.listCheckSuiteRuns(ctx, a)
			})},
	}
}

// checksFailure names the rights a refused check read needs and turns GitHub's rejection of the request
// into an invalid request that carries no text of the answer.
func checksFailure(err error) error {
	var failure *provider.Error
	if errors.As(err, &failure) && failure.Class == provider.ClassProviderError && failure.Message == rejectedMessage {
		return invalidRequest("GitHub rejected this check read")
	}
	return actionsFailure(err, checksReadPermissionMessage)
}

// foreignCheck is the refusal of an answer that does not belong to the selected repository. It names no value
// of the answer.
func foreignCheck(op, what string) error {
	return &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
		Message: "GitHub answered " + what + " that does not belong to the selected repository, so nothing was returned"}
}

// ownsAddress reports whether the REST address of an object is the address rest below the selected
// repository. Only the path counts, below the path of the configured REST root and without regard to case.
func (c *Client) ownsAddress(address, rest string) bool {
	given, errGiven := url.Parse(address)
	root, errRoot := url.Parse(c.endpoints.rest)
	if errGiven != nil || errRoot != nil {
		return false
	}
	want := strings.TrimSuffix(root.Path, "/") + "/repos/" + c.target.owner + "/" + c.target.repo + "/" + rest
	return strings.EqualFold(given.Path, want)
}

// readCheckRun reads one check run of the selected repository and refuses an answer that is another check
// run or names another repository.
func (c *Client) readCheckRun(ctx context.Context, op string, id int64) (*checkRunRaw, error) {
	var raw checkRunRaw
	identifier := strconv.FormatInt(id, 10)
	if err := c.rest(ctx, op, c.repoPath("check-runs/"+identifier), &raw); err != nil {
		return nil, checksFailure(err)
	}
	if raw.ID == nil || *raw.ID != id {
		return nil, invalidEntry(op, "a check run")
	}
	if !c.ownsAddress(raw.links["url"], "check-runs/"+identifier) {
		return nil, foreignCheck(op, "a check run")
	}
	return &raw, nil
}

// readCheckSuite reads one check suite of the selected repository and refuses an answer that is another
// check suite or names another repository.
func (c *Client) readCheckSuite(ctx context.Context, op string, id int64) error {
	var raw checkSuiteRaw
	if err := c.rest(ctx, op, c.repoPath("check-suites/"+strconv.FormatInt(id, 10)), &raw); err != nil {
		return checksFailure(err)
	}
	if raw.ID == nil || *raw.ID != id {
		return invalidEntry(op, "a check suite")
	}
	owner := c.target.owner + "/" + c.target.repo
	if raw.Repository == nil || raw.Repository.FullName == nil || !strings.EqualFold(*raw.Repository.FullName, owner) {
		return foreignCheck(op, "a check suite")
	}
	return nil
}

// checkRunsPage is the answer of a paged check run read.
type checkRunsPage struct {
	TotalCount *int64        `json:"total_count"`
	CheckRuns  []checkRunRaw `json:"check_runs"`
}

func projectCheckRuns(op string, page *checkRunsPage) ([]CheckRun, error) {
	runs := make([]CheckRun, 0, len(page.CheckRuns))
	for i := range page.CheckRuns {
		if page.CheckRuns[i].ID == nil {
			return nil, invalidEntry(op, "a check run")
		}
		runs = append(runs, page.CheckRuns[i].project())
	}
	return runs, nil
}

func (c *Client) listCheckRuns(ctx context.Context, a *checkArguments) (*CheckRunList, error) {
	const op = "list check runs"
	var page checkRunsPage
	hasNext, err := c.restPage(ctx, op, c.repoPath("commits/"+url.PathEscape(a.Ref)+"/check-runs"), a.query(), &page)
	if err != nil {
		return nil, checksFailure(err)
	}
	runs, err := projectCheckRuns(op, &page)
	if err != nil {
		return nil, err
	}
	result := &CheckRunList{Ref: a.Ref, CheckRuns: runs, TotalCount: page.TotalCount}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

func (c *Client) getCheckRun(ctx context.Context, a *checkArguments) (*CheckRun, error) {
	raw, err := c.readCheckRun(ctx, "get check run", a.CheckRunID)
	if err != nil {
		return nil, err
	}
	run := raw.project()
	return &run, nil
}

func (c *Client) listCheckAnnotations(ctx context.Context, a *checkArguments) (*CheckAnnotationList, error) {
	const op = "list check annotations"
	if _, err := c.readCheckRun(ctx, op, a.CheckRunID); err != nil {
		return nil, err
	}
	var raw []checkAnnotationRaw
	path := c.repoPath("check-runs/" + strconv.FormatInt(a.CheckRunID, 10) + "/annotations")
	hasNext, err := c.restPage(ctx, op, path, a.query(), &raw)
	if err != nil {
		return nil, checksFailure(err)
	}
	result := &CheckAnnotationList{CheckRunID: a.CheckRunID, Annotations: make([]CheckAnnotation, 0, len(raw))}
	for i := range raw {
		result.Annotations = append(result.Annotations, raw[i].project())
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

func (c *Client) listCheckSuites(ctx context.Context, a *checkArguments) (*CheckSuiteList, error) {
	const op = "list check suites"
	var page struct {
		TotalCount  *int64          `json:"total_count"`
		CheckSuites []checkSuiteRaw `json:"check_suites"`
	}
	hasNext, err := c.restPage(ctx, op, c.repoPath("commits/"+url.PathEscape(a.Ref)+"/check-suites"), a.query(), &page)
	if err != nil {
		return nil, checksFailure(err)
	}
	result := &CheckSuiteList{Ref: a.Ref, CheckSuites: make([]CheckSuite, 0, len(page.CheckSuites)),
		TotalCount: page.TotalCount}
	for i := range page.CheckSuites {
		if page.CheckSuites[i].ID == nil {
			return nil, invalidEntry(op, "a check suite")
		}
		result.CheckSuites = append(result.CheckSuites, page.CheckSuites[i].project(c.allowed))
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

func (c *Client) listCheckSuiteRuns(ctx context.Context, a *checkArguments) (*CheckSuiteRunList, error) {
	const op = "list check suite runs"
	if err := c.readCheckSuite(ctx, op, a.CheckSuiteID); err != nil {
		return nil, err
	}
	var page checkRunsPage
	path := c.repoPath("check-suites/" + strconv.FormatInt(a.CheckSuiteID, 10) + "/check-runs")
	hasNext, err := c.restPage(ctx, op, path, a.query(), &page)
	if err != nil {
		return nil, checksFailure(err)
	}
	runs, err := projectCheckRuns(op, &page)
	if err != nil {
		return nil, err
	}
	result := &CheckSuiteRunList{CheckSuiteID: a.CheckSuiteID, CheckRuns: runs, TotalCount: page.TotalCount}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}
