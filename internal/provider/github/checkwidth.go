package github

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The check width: the members of GitHub's check run, simple commit status, and combined commit status that
// pull-request-checks.list answers beside the compact, merged checks list. Texts are untrusted data and pass
// through the shared projection helpers; an address is never fetched. The repository of the combined status
// is left out, since the call names it. The filters act on the check runs only; the commit statuses have none.

const (
	checkNameLimit = 100
	maxCheckAppID  = maxActionsID
)

var (
	checkRunLinkKeys      = []string{"url", "details_url"}
	checkOutputLinkKeys   = []string{"annotations_url"}
	checkDeploymentLinks  = []string{"url", "statuses_url", "repository_url"}
	commitStatusLinkKeys  = []string{"url", "target_url", "avatar_url"}
	combinedStatusLinkKey = []string{"url", "commit_url"}
	checkRunStatusValues  = []string{"queued", "in_progress", "completed"}
	checkRunFilterValues  = []string{"latest", "all"}
)

// ---- Check runs

type checkOutputRaw struct {
	Title            *string `json:"title"`
	Summary          *string `json:"summary"`
	Text             *string `json:"text"`
	AnnotationsCount *int64  `json:"annotations_count"`
	links            Links
}

func (r *checkOutputRaw) UnmarshalJSON(data []byte) (err error) {
	type plain checkOutputRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, checkOutputLinkKeys)
	return err
}

type checkDeploymentRaw struct {
	ID                    *int64  `json:"id"`
	NodeID                *string `json:"node_id"`
	Task                  *string `json:"task"`
	OriginalEnvironment   *string `json:"original_environment"`
	Environment           *string `json:"environment"`
	Description           *string `json:"description"`
	CreatedAt             *string `json:"created_at"`
	UpdatedAt             *string `json:"updated_at"`
	TransientEnvironment  *bool   `json:"transient_environment"`
	ProductionEnvironment *bool   `json:"production_environment"`
	PerformedViaGitHubApp *appRaw `json:"performed_via_github_app"`
	links                 Links
}

func (r *checkDeploymentRaw) UnmarshalJSON(data []byte) (err error) {
	type plain checkDeploymentRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, checkDeploymentLinks)
	return err
}

type checkRunRaw struct {
	ID          *int64          `json:"id"`
	NodeID      *string         `json:"node_id"`
	ExternalID  *string         `json:"external_id"`
	HeadSHA     *string         `json:"head_sha"`
	Name        *string         `json:"name"`
	Status      *string         `json:"status"`
	Conclusion  *string         `json:"conclusion"`
	StartedAt   *string         `json:"started_at"`
	CompletedAt *string         `json:"completed_at"`
	HTMLURL     *string         `json:"html_url"`
	Output      *checkOutputRaw `json:"output"`
	CheckSuite  *struct {
		ID *int64 `json:"id"`
	} `json:"check_suite"`
	App          *appRaw             `json:"app"`
	PullRequests []*runPullRaw       `json:"pull_requests"`
	Deployment   *checkDeploymentRaw `json:"deployment"`
	links        Links
}

func (r *checkRunRaw) UnmarshalJSON(data []byte) (err error) {
	type plain checkRunRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, checkRunLinkKeys)
	return err
}

// checkRunEntry decodes one check run twice from the same object: the compact view and the width. A member
// the width cannot read is left out rather than failing a read the compact view already decoded.
type checkRunEntry struct {
	run  checkRunJSON
	wide checkRunRaw
}

func (e *checkRunEntry) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &e.run); err != nil {
		return err
	}
	_ = json.Unmarshal(data, &e.wide)
	return nil
}

// CheckRunOutput is the output a check run reported. Title, summary, and text are untrusted data.
type CheckRunOutput struct {
	Title            *string `json:"title,omitempty"`
	TitleTruncated   bool    `json:"title_truncated,omitempty"`
	Summary          *string `json:"summary,omitempty"`
	SummaryTruncated bool    `json:"summary_truncated,omitempty"`
	Text             *string `json:"text,omitempty"`
	TextTruncated    bool    `json:"text_truncated,omitempty"`
	AnnotationsCount *int64  `json:"annotations_count,omitempty"`
	Links            Links   `json:"links,omitempty"`
}

// CheckRunSuite is the check suite a check run belongs to.
type CheckRunSuite struct {
	ID *int64 `json:"id,omitempty"`
}

// CheckRunDeployment is the deployment a check run belongs to.
type CheckRunDeployment struct {
	ID                    *int64   `json:"id,omitempty"`
	NodeID                *string  `json:"node_id,omitempty"`
	Task                  *string  `json:"task,omitempty"`
	OriginalEnvironment   *string  `json:"original_environment,omitempty"`
	Environment           *string  `json:"environment,omitempty"`
	Description           *string  `json:"description,omitempty"`
	CreatedAt             *string  `json:"created_at,omitempty"`
	UpdatedAt             *string  `json:"updated_at,omitempty"`
	TransientEnvironment  *bool    `json:"transient_environment,omitempty"`
	ProductionEnvironment *bool    `json:"production_environment,omitempty"`
	PerformedViaGitHubApp *RESTApp `json:"performed_via_github_app,omitempty"`
	Links                 Links    `json:"links,omitempty"`
}

// CheckRun is one check run of the head commit as GitHub's check-run object reports it.
type CheckRun struct {
	ID                    *int64              `json:"id,omitempty"`
	NodeID                *string             `json:"node_id,omitempty"`
	ExternalID            *string             `json:"external_id,omitempty"`
	HeadSHA               *string             `json:"head_sha,omitempty"`
	Name                  *string             `json:"name,omitempty"`
	Status                *string             `json:"status,omitempty"`
	Conclusion            *string             `json:"conclusion,omitempty"`
	StartedAt             *string             `json:"started_at,omitempty"`
	CompletedAt           *string             `json:"completed_at,omitempty"`
	URL                   *string             `json:"url,omitempty"`
	Links                 Links               `json:"links,omitempty"`
	Output                *CheckRunOutput     `json:"output,omitempty"`
	CheckSuite            *CheckRunSuite      `json:"check_suite,omitempty"`
	App                   *RESTApp            `json:"app,omitempty"`
	PullRequests          []RunPullRequest    `json:"pull_requests,omitempty"`
	PullRequestsTruncated bool                `json:"pull_requests_truncated,omitempty"`
	Deployment            *CheckRunDeployment `json:"deployment,omitempty"`
}

func (r *checkOutputRaw) project() *CheckRunOutput {
	if r == nil {
		return nil
	}
	out := &CheckRunOutput{AnnotationsCount: r.AnnotationsCount, Links: r.links}
	out.Title, out.TitleTruncated = cleanText(r.Title, projectionTextLimit, false)
	out.Summary, out.SummaryTruncated = cleanText(r.Summary, projectionBodyLimit, true)
	out.Text, out.TextTruncated = cleanText(r.Text, projectionBodyLimit, true)
	return out
}

func (r *checkDeploymentRaw) project() *CheckRunDeployment {
	if r == nil {
		return nil
	}
	return &CheckRunDeployment{ID: r.ID, NodeID: nameText(r.NodeID), Task: nameText(r.Task),
		OriginalEnvironment: nameText(r.OriginalEnvironment), Environment: nameText(r.Environment),
		Description: longText(r.Description), CreatedAt: nameText(r.CreatedAt), UpdatedAt: nameText(r.UpdatedAt),
		TransientEnvironment: r.TransientEnvironment, ProductionEnvironment: r.ProductionEnvironment,
		PerformedViaGitHubApp: r.PerformedViaGitHubApp.project(), Links: r.links}
}

func (r *checkRunRaw) project() CheckRun {
	run := CheckRun{ID: r.ID, NodeID: nameText(r.NodeID), ExternalID: nameText(r.ExternalID),
		HeadSHA: nameText(r.HeadSHA), Name: nameText(r.Name), Status: nameText(r.Status),
		Conclusion: nameText(r.Conclusion), StartedAt: nameText(r.StartedAt), CompletedAt: nameText(r.CompletedAt),
		URL: address(r.HTMLURL), Links: r.links, Output: r.Output.project(), App: r.App.project(),
		Deployment: r.Deployment.project()}
	if r.CheckSuite != nil && r.CheckSuite.ID != nil {
		run.CheckSuite = &CheckRunSuite{ID: r.CheckSuite.ID}
	}
	var pulls []RunPullRequest
	for _, pull := range r.PullRequests {
		if pull != nil {
			pulls = append(pulls, RunPullRequest{ID: pull.ID, Number: pull.Number, Head: pull.Head.project(),
				Base: pull.Base.project(), Links: pull.links})
		}
	}
	run.PullRequests, run.PullRequestsTruncated = capped(pulls)
	return run
}

// ---- Commit statuses

type commitStatusRaw struct {
	ID          *int64  `json:"id"`
	NodeID      *string `json:"node_id"`
	State       *string `json:"state"`
	Context     *string `json:"context"`
	Description *string `json:"description"`
	Required    *bool   `json:"required"`
	CreatedAt   *string `json:"created_at"`
	UpdatedAt   *string `json:"updated_at"`
	links       Links
}

func (r *commitStatusRaw) UnmarshalJSON(data []byte) (err error) {
	type plain commitStatusRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, commitStatusLinkKeys)
	return err
}

// commitStatusEntry decodes one commit status twice, like checkRunEntry.
type commitStatusEntry struct {
	status commitStatusJSON
	wide   commitStatusRaw
}

func (e *commitStatusEntry) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &e.status); err != nil {
		return err
	}
	_ = json.Unmarshal(data, &e.wide)
	return nil
}

// CommitStatus is one legacy commit status as GitHub's simple-commit-status object reports it.
type CommitStatus struct {
	ID          *int64  `json:"id,omitempty"`
	NodeID      *string `json:"node_id,omitempty"`
	State       *string `json:"state,omitempty"`
	Context     *string `json:"context,omitempty"`
	Description *string `json:"description,omitempty"`
	Required    *bool   `json:"required,omitempty"`
	CreatedAt   *string `json:"created_at,omitempty"`
	UpdatedAt   *string `json:"updated_at,omitempty"`
	Links       Links   `json:"links,omitempty"`
}

func (r *commitStatusRaw) project() CommitStatus {
	return CommitStatus{ID: r.ID, NodeID: nameText(r.NodeID), State: nameText(r.State), Context: nameText(r.Context),
		Description: longText(r.Description), Required: r.Required, CreatedAt: nameText(r.CreatedAt),
		UpdatedAt: nameText(r.UpdatedAt), Links: r.links}
}

// ---- The two reads

type checkRunsAnswer struct {
	TotalCount int             `json:"total_count"`
	CheckRuns  []checkRunEntry `json:"check_runs"`
	wideTotal  *int64
}

func (a *checkRunsAnswer) UnmarshalJSON(data []byte) error {
	type plain checkRunsAnswer
	if err := json.Unmarshal(data, (*plain)(a)); err != nil {
		return err
	}
	var total struct {
		TotalCount *int64 `json:"total_count"`
	}
	_ = json.Unmarshal(data, &total)
	a.wideTotal = total.TotalCount
	return nil
}

type combinedStatusAnswer struct {
	TotalCount int                 `json:"total_count"`
	Statuses   []commitStatusEntry `json:"statuses"`
	State      *string             `json:"state"`
	SHA        *string             `json:"sha"`
	wideTotal  *int64
	links      Links
}

func (a *combinedStatusAnswer) UnmarshalJSON(data []byte) (err error) {
	type plain combinedStatusAnswer
	if err = json.Unmarshal(data, (*plain)(a)); err != nil {
		return err
	}
	var total struct {
		TotalCount *int64 `json:"total_count"`
	}
	_ = json.Unmarshal(data, &total)
	a.wideTotal = total.TotalCount
	a.links, _ = readLinks(data, combinedStatusLinkKey)
	return nil
}

// CheckWidth holds the members the checks read adds to the compact, merged list: the check runs and commit
// statuses in full and the combined commit status.
type CheckWidth struct {
	CheckRuns           []CheckRun     `json:"check_runs,omitempty"`
	CheckRunsTotalCount *int64         `json:"check_runs_total_count,omitempty"`
	Statuses            []CommitStatus `json:"statuses,omitempty"`
	State               *string        `json:"state,omitempty"`
	TotalCount          *int64         `json:"total_count,omitempty"`
	SHA                 *string        `json:"sha,omitempty"`
	Links               Links          `json:"links,omitempty"`
}

func (runs *checkRunsAnswer) width(combined *combinedStatusAnswer) CheckWidth {
	w := CheckWidth{CheckRunsTotalCount: runs.wideTotal, State: nameText(combined.State),
		TotalCount: combined.wideTotal, SHA: nameText(combined.SHA), Links: combined.links}
	for i := range runs.CheckRuns {
		w.CheckRuns = append(w.CheckRuns, runs.CheckRuns[i].wide.project())
	}
	for i := range combined.Statuses {
		w.Statuses = append(w.Statuses, combined.Statuses[i].wide.project())
	}
	return w
}

// ---- Filters

// checkRunQuery is the query of the check run read: the page size, and each filter that is set. Without a
// filter it is the query the read always sent.
func (a *pullArguments) checkRunQuery() string {
	query := url.Values{"per_page": {"100"}}
	if a.CheckName != "" {
		query.Set("check_name", a.CheckName)
	}
	if a.CheckStatus != "" {
		query.Set("status", a.CheckStatus)
	}
	if a.CheckFilter != "" {
		query.Set("filter", a.CheckFilter)
	}
	if a.AppID != nil {
		query.Set("app_id", fmt.Sprint(*a.AppID))
	}
	return query.Encode()
}

// checkPullChecksArguments applies the bounds of the check run filters before any credential is resolved.
func checkPullChecksArguments(a *pullArguments, bound target) error {
	if err := checkPullNumberArgument(a, bound); err != nil {
		return err
	}
	if utf8.RuneCountInString(a.CheckName) > checkNameLimit || strings.ContainsFunc(a.CheckName, unicode.IsControl) {
		return invalidRequest(fmt.Sprintf("check_name must hold at most %d characters and no control character", checkNameLimit))
	}
	if a.CheckStatus != "" && !oneOf(a.CheckStatus, checkRunStatusValues) {
		return invalidRequest("status must be queued, in_progress, or completed")
	}
	if a.CheckFilter != "" && !oneOf(a.CheckFilter, checkRunFilterValues) {
		return invalidRequest("filter must be latest or all")
	}
	if a.AppID != nil && (*a.AppID < 1 || *a.AppID > maxCheckAppID) {
		return invalidRequest("app_id must be a positive app identifier")
	}
	return nil
}

func oneOf(value string, values []string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// ---- Schemas and fields

const checkFilterSchemas = `"check_name":{"type":"string","minLength":1,"maxLength":100},` +
	`"status":{"type":"string","enum":["queued","in_progress","completed"]},` +
	`"filter":{"type":"string","enum":["latest","all"]},"app_id":` + actionsIDSchema

var checkFilterArguments = []capability.Argument{
	{Name: "check_name", Description: "Return only check runs with exactly this name, up to 100 characters; " +
		"commit statuses are not filtered"},
	{Name: "status", Description: "Return only check runs in this status: queued, in_progress, or completed"},
	{Name: "filter", Description: "latest returns the latest check run of each name, all every one; GitHub's " +
		"own choice, latest, when omitted"},
	{Name: "app_id", Description: "Return only check runs of the GitHub App with this numeric id"},
}

var checkRunPullSchema = objectSchema(`"id":{"type":"integer"},"number":{"type":"integer"},` +
	`"head":` + runPullRefSchema + `,"base":` + runPullRefSchema + `,"links":` + linksSchema(runPullLinkKeys))

var checkRunProperties = `"id":{"type":"integer"},"node_id":{"type":"string"},"external_id":{"type":"string"},` +
	`"head_sha":{"type":"string"},"name":{"type":"string"},"status":{"type":"string"},` +
	`"conclusion":{"type":"string"},"started_at":{"type":"string"},"completed_at":{"type":"string"},` +
	`"url":{"type":"string"},"links":` + linksSchema(checkRunLinkKeys) + `,"output":` +
	objectSchema(`"title":{"type":"string"},"title_truncated":{"type":"boolean"},"summary":{"type":"string"},`+
		`"summary_truncated":{"type":"boolean"},"text":{"type":"string"},"text_truncated":{"type":"boolean"},`+
		`"annotations_count":{"type":"integer"},"links":`+linksSchema(checkOutputLinkKeys)) + `,` +
	`"check_suite":` + objectSchema(`"id":{"type":"integer"}`) + `,"app":` + restAppSchema + `,` +
	`"pull_requests":{"type":"array","maxItems":100,"items":` + checkRunPullSchema + `},` +
	`"pull_requests_truncated":{"type":"boolean"},"deployment":` +
	objectSchema(`"id":{"type":"integer"},"node_id":{"type":"string"},"task":{"type":"string"},`+
		`"original_environment":{"type":"string"},"environment":{"type":"string"},"description":{"type":"string"},`+
		`"created_at":{"type":"string"},"updated_at":{"type":"string"},"transient_environment":{"type":"boolean"},`+
		`"production_environment":{"type":"boolean"},"performed_via_github_app":`+restAppSchema+`,`+
		`"links":`+linksSchema(checkDeploymentLinks))

var commitStatusProperties = `"id":{"type":"integer"},"node_id":{"type":"string"},"state":{"type":"string"},` +
	`"context":{"type":"string"},"description":{"type":"string"},"required":{"type":"boolean"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"},"links":` + linksSchema(commitStatusLinkKeys)

var checkWidthProperties = `"check_runs":{"type":"array","maxItems":100,"items":` + objectSchema(checkRunProperties) + `},` +
	`"check_runs_total_count":{"type":"integer"},` +
	`"statuses":{"type":"array","maxItems":100,"items":` + objectSchema(commitStatusProperties) + `},` +
	`"state":{"type":"string"},"total_count":{"type":"integer"},"sha":{"type":"string"},` +
	`"links":` + linksSchema(combinedStatusLinkKey)

var checkWidthFields = []capability.Field{
	{Name: "check_runs", Description: "The check runs read, at most 100, each as GitHub reports it: id, node_id, " +
		"external_id, head_sha, name, status, conclusion, started_at, completed_at, url (the web address), links " +
		"(the REST url and details_url), output (title, summary, and text, each cut with a marker, " +
		"annotations_count, and links with annotations_url), check_suite (id), app (" + restAppMembers + "), " +
		"pull_requests (id, number, head, base, links), and deployment (id, node_id, task, original_environment, " +
		"environment, description, created_at, updated_at, transient_environment, production_environment, " +
		"performed_via_github_app, links); names, texts, and addresses are untrusted data, addresses never fetched"},
	{Name: "check_runs_total_count", Description: "Number of check runs GitHub holds for the commit and the filters"},
	{Name: "statuses", Description: "The commit statuses read, at most 100: id, node_id, state, context, " +
		"description, required, created_at, updated_at, and links (url, target_url, avatar_url); untrusted data"},
	{Name: "state", Description: "State GitHub computes for the combined commit status, such as failure, pending, or success"},
	{Name: "total_count", Description: "Number of commit statuses GitHub holds for the commit"},
	{Name: "sha", Description: "Commit the combined status belongs to"},
	{Name: "links", Description: "url and commit_url of the combined status by their GitHub names, untrusted " +
		"data, never fetched"},
}
