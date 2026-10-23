package github

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

// The Actions width: the members of GitHub's workflow, run, job, artifact, and Actions permission objects
// beyond the compact views the Actions read tools answered before. Texts are untrusted data and pass
// through the shared projection helpers; an address is never fetched. The logs_url of a run and the
// archive_download_url of an artifact are never read: each answers with a short-lived signed link that
// grants access without sign-in.

// Bounds of the Actions width. A job carries at most jobLabelLimit labels; the commit message of a run is
// cut at commitMessageLimit runes.
const (
	jobLabelLimit      = 50
	commitMessageLimit = projectionTextLimit
	sha1HexLength      = 40
	artifactNameLimit  = 256
)

var (
	workflowLinkKeys     = []string{"url", "badge_url"}
	runLinkKeys          = []string{"url", "jobs_url", "check_suite_url", "artifacts_url", "cancel_url", "rerun_url", "previous_attempt_url", "workflow_url"}
	jobLinkKeys          = []string{"url", "run_url", "check_run_url"}
	artifactLinkKeys     = []string{"url"}
	actionsSettingsLinks = []string{"selected_actions_url"}
	runPullLinkKeys      = []string{"url"}
)

// text returns untrusted text cleaned and bounded as a name.
func text(s string) string {
	return valueOr(nameText(&s))
}

// ---- Run members

type runCommitIdentityRaw struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// CommitIdentity is the author or committer of the head commit of a run.
type CommitIdentity struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

func (r *runCommitIdentityRaw) project() *CommitIdentity {
	if r == nil || (r.Name == "" && r.Email == "") {
		return nil
	}
	return &CommitIdentity{Name: text(r.Name), Email: text(r.Email)}
}

type runCommitRaw struct {
	ID        string                `json:"id"`
	TreeID    string                `json:"tree_id"`
	Message   *string               `json:"message"`
	Timestamp string                `json:"timestamp"`
	Author    *runCommitIdentityRaw `json:"author"`
	Committer *runCommitIdentityRaw `json:"committer"`
}

// RunHeadCommit is the head commit of a run. The message is untrusted data, cut with a marker.
type RunHeadCommit struct {
	ID               string          `json:"id,omitempty"`
	TreeID           string          `json:"tree_id,omitempty"`
	Message          string          `json:"message,omitempty"`
	MessageTruncated bool            `json:"message_truncated,omitempty"`
	Timestamp        string          `json:"timestamp,omitempty"`
	Author           *CommitIdentity `json:"author,omitempty"`
	Committer        *CommitIdentity `json:"committer,omitempty"`
}

func (r *runCommitRaw) project() *RunHeadCommit {
	if r == nil {
		return nil
	}
	message, cut := cleanText(r.Message, commitMessageLimit, true)
	out := &RunHeadCommit{ID: text(r.ID), TreeID: text(r.TreeID), Message: valueOr(message), MessageTruncated: cut,
		Timestamp: text(r.Timestamp), Author: r.Author.project(), Committer: r.Committer.project()}
	if *out == (RunHeadCommit{}) {
		return nil
	}
	return out
}

type referencedWorkflowRaw struct {
	Path string `json:"path"`
	SHA  string `json:"sha"`
	Ref  string `json:"ref"`
}

// ReferencedWorkflow is a reusable workflow a run called.
type ReferencedWorkflow struct {
	Path string `json:"path,omitempty"`
	SHA  string `json:"sha,omitempty"`
	Ref  string `json:"ref,omitempty"`
}

type runPullRepoRaw struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	links Links
}

func (r *runPullRepoRaw) UnmarshalJSON(data []byte) (err error) {
	type plain runPullRepoRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, runPullLinkKeys)
	return err
}

type runPullRefRaw struct {
	Ref  string          `json:"ref"`
	SHA  string          `json:"sha"`
	Repo *runPullRepoRaw `json:"repo"`
}

type runPullRaw struct {
	ID     int64          `json:"id"`
	Number int64          `json:"number"`
	Head   *runPullRefRaw `json:"head"`
	Base   *runPullRefRaw `json:"base"`
	links  Links
}

func (r *runPullRaw) UnmarshalJSON(data []byte) (err error) {
	type plain runPullRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, runPullLinkKeys)
	return err
}

// RunPullRepo is the repository on one side of a pull request of a run.
type RunPullRepo struct {
	ID    int64  `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Links Links  `json:"links,omitempty"`
}

// RunPullRef is one side of a pull request of a run.
type RunPullRef struct {
	Ref  string       `json:"ref,omitempty"`
	SHA  string       `json:"sha,omitempty"`
	Repo *RunPullRepo `json:"repo,omitempty"`
}

// RunPullRequest is a pull request GitHub associates with a run.
type RunPullRequest struct {
	ID     int64       `json:"id,omitempty"`
	Number int64       `json:"number,omitempty"`
	Head   *RunPullRef `json:"head,omitempty"`
	Base   *RunPullRef `json:"base,omitempty"`
	Links  Links       `json:"links,omitempty"`
}

func (r *runPullRefRaw) project() *RunPullRef {
	if r == nil {
		return nil
	}
	out := &RunPullRef{Ref: text(r.Ref), SHA: text(r.SHA)}
	if p := r.Repo; p != nil {
		out.Repo = &RunPullRepo{ID: p.ID, Name: text(p.Name), Links: p.links}
	}
	return out
}

// RunWidth holds the members of GitHub's workflow run every run read adds to its compact view.
type RunWidth struct {
	NodeID                       string               `json:"node_id,omitempty"`
	CheckSuiteID                 int64                `json:"check_suite_id,omitempty"`
	CheckSuiteNodeID             string               `json:"check_suite_node_id,omitempty"`
	Path                         string               `json:"path,omitempty"`
	ReferencedWorkflows          []ReferencedWorkflow `json:"referenced_workflows,omitempty"`
	ReferencedWorkflowsTruncated bool                 `json:"referenced_workflows_truncated,omitempty"`
	ActorDetail                  *RESTAccount         `json:"actor_detail,omitempty"`
	TriggeringActor              *RESTAccount         `json:"triggering_actor,omitempty"`
	HeadCommit                   *RunHeadCommit       `json:"head_commit,omitempty"`
	HeadRepository               *RESTRepository      `json:"head_repository,omitempty"`
	HeadRepositoryID             int64                `json:"head_repository_id,omitempty"`
	PullRequests                 []RunPullRequest     `json:"pull_requests,omitempty"`
	PullRequestsTruncated        bool                 `json:"pull_requests_truncated,omitempty"`
	Links                        Links                `json:"links,omitempty"`
}

// width projects the members beyond the compact view. The pull requests belong to the answer only when the
// request asked for them.
func (r *runJSON) width(allowed allowlist, pulls bool) RunWidth {
	w := RunWidth{NodeID: text(r.NodeID), CheckSuiteID: r.CheckSuiteID, CheckSuiteNodeID: text(r.CheckSuiteNodeID),
		Path: text(r.Path), ActorDetail: r.Actor.project(), TriggeringActor: r.TriggeringActor.project(),
		HeadCommit: r.HeadCommit.project(), HeadRepository: r.HeadRepository.project(allowed),
		HeadRepositoryID: r.HeadRepositoryID, Links: r.links}
	var workflows []ReferencedWorkflow
	for _, ref := range r.ReferencedWorkflows {
		if ref != nil {
			workflows = append(workflows, ReferencedWorkflow{Path: text(ref.Path), SHA: text(ref.SHA), Ref: text(ref.Ref)})
		}
	}
	w.ReferencedWorkflows, w.ReferencedWorkflowsTruncated = capped(workflows)
	if pulls {
		var requests []RunPullRequest
		for _, pull := range r.PullRequests {
			if pull != nil {
				requests = append(requests, RunPullRequest{ID: pull.ID, Number: pull.Number, Head: pull.Head.project(),
					Base: pull.Base.project(), Links: pull.links})
			}
		}
		w.PullRequests, w.PullRequestsTruncated = capped(requests)
	}
	return w
}

// ---- Job members

// JobWidth holds the members of GitHub's job every job read adds to its compact view.
type JobWidth struct {
	NodeID          string   `json:"node_id,omitempty"`
	HeadSHA         string   `json:"head_sha,omitempty"`
	HeadBranch      string   `json:"head_branch,omitempty"`
	CreatedAt       string   `json:"created_at,omitempty"`
	Labels          []string `json:"labels,omitempty"`
	LabelsTruncated bool     `json:"labels_truncated,omitempty"`
	RunnerID        int64    `json:"runner_id,omitempty"`
	RunnerName      string   `json:"runner_name,omitempty"`
	RunnerGroupID   int64    `json:"runner_group_id,omitempty"`
	RunnerGroupName string   `json:"runner_group_name,omitempty"`
	WorkflowName    string   `json:"workflow_name,omitempty"`
	Links           Links    `json:"links,omitempty"`
}

func (j *jobJSON) width() JobWidth {
	w := JobWidth{NodeID: text(j.NodeID), HeadSHA: text(j.HeadSHA), HeadBranch: text(j.HeadBranch),
		CreatedAt: text(j.CreatedAt), RunnerID: j.RunnerID, RunnerName: text(j.RunnerName),
		RunnerGroupID: j.RunnerGroupID, RunnerGroupName: text(j.RunnerGroupName),
		WorkflowName: text(j.WorkflowName), Links: j.links}
	for _, label := range j.Labels {
		if label != "" {
			w.Labels = append(w.Labels, text(label))
		}
	}
	if len(w.Labels) > jobLabelLimit {
		w.Labels, w.LabelsTruncated = w.Labels[:jobLabelLimit], true
	}
	return w
}

// ---- Artifact members

type artifactRunRaw struct {
	ID               int64  `json:"id"`
	RepositoryID     int64  `json:"repository_id"`
	HeadRepositoryID int64  `json:"head_repository_id"`
	HeadBranch       string `json:"head_branch"`
	HeadSHA          string `json:"head_sha"`
}

// ArtifactRun is the workflow run an artifact belongs to.
type ArtifactRun struct {
	ID               int64  `json:"id,omitempty"`
	RepositoryID     int64  `json:"repository_id,omitempty"`
	HeadRepositoryID int64  `json:"head_repository_id,omitempty"`
	HeadBranch       string `json:"head_branch,omitempty"`
	HeadSHA          string `json:"head_sha,omitempty"`
}

type artifactJSON struct {
	ID          int64           `json:"id"`
	NodeID      string          `json:"node_id"`
	Name        string          `json:"name"`
	SizeInBytes int64           `json:"size_in_bytes"`
	Expired     bool            `json:"expired"`
	CreatedAt   string          `json:"created_at"`
	ExpiresAt   string          `json:"expires_at"`
	UpdatedAt   string          `json:"updated_at"`
	Digest      string          `json:"digest"`
	WorkflowRun *artifactRunRaw `json:"workflow_run"`
	links       Links
}

func (a *artifactJSON) UnmarshalJSON(data []byte) (err error) {
	type plain artifactJSON
	if err = json.Unmarshal(data, (*plain)(a)); err != nil {
		return err
	}
	a.links, err = readLinks(data, artifactLinkKeys)
	return err
}

func (a *artifactJSON) view() Artifact {
	out := Artifact{ID: a.ID, Name: a.Name, SizeBytes: a.SizeInBytes, Expired: a.Expired, CreatedAt: a.CreatedAt,
		ExpiresAt: a.ExpiresAt, Digest: a.Digest, NodeID: text(a.NodeID), UpdatedAt: text(a.UpdatedAt), Links: a.links}
	if r := a.WorkflowRun; r != nil {
		run := ArtifactRun{ID: r.ID, RepositoryID: r.RepositoryID, HeadRepositoryID: r.HeadRepositoryID,
			HeadBranch: text(r.HeadBranch), HeadSHA: text(r.HeadSHA)}
		if run != (ArtifactRun{}) {
			out.WorkflowRun = &run
		}
	}
	return out
}

// ---- Actions permissions

type actionsPermissionsRaw struct {
	Enabled            *bool  `json:"enabled"`
	AllowedActions     string `json:"allowed_actions"`
	ShaPinningRequired *bool  `json:"sha_pinning_required"`
	links              Links
}

func (r *actionsPermissionsRaw) UnmarshalJSON(data []byte) (err error) {
	type plain actionsPermissionsRaw
	if err = json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	r.links, err = readLinks(data, actionsSettingsLinks)
	return err
}

// ---- Filters

// widthFilters returns the fingerprint parts of the filters added to the run and artifact lists. A filter that
// is not set adds nothing, so a request without them keeps the fingerprint it had before.
func (a *actionsArguments) widthFilters() []any {
	var parts []any
	if a.CheckSuiteID != nil {
		parts = append(parts, "check_suite_id", *a.CheckSuiteID)
	}
	if a.HeadSHA != "" {
		parts = append(parts, "head_sha", strings.ToLower(a.HeadSHA))
	}
	if a.IncludePullRequests {
		parts = append(parts, "include_pull_requests", true)
	}
	if a.Name != "" {
		parts = append(parts, "name", a.Name)
	}
	if a.Direction != "" {
		parts = append(parts, "direction", a.Direction)
	}
	return parts
}

// checkWidthFilters applies the bounds of the filters added to the run and artifact lists.
func checkWidthFilters(a *actionsArguments) error {
	if a.CheckSuiteID != nil && (*a.CheckSuiteID < 1 || *a.CheckSuiteID > maxActionsID) {
		return invalidRequest("check_suite_id must be a positive check suite identifier")
	}
	if a.HeadSHA != "" && !validHeadSHA(a.HeadSHA) {
		return invalidRequest("head_sha must be a full commit SHA of 40 hexadecimal characters")
	}
	if a.Name != "" {
		if utf8.RuneCountInString(a.Name) > artifactNameLimit || strings.ContainsFunc(a.Name, unicode.IsControl) {
			return invalidRequest(fmt.Sprintf("name must hold at most %d characters and no control character", artifactNameLimit))
		}
	}
	switch a.Direction {
	case "", "asc", "desc":
	default:
		return invalidRequest("direction must be asc or desc")
	}
	if a.Direction != "" && a.RunID < 1 {
		return invalidRequest("direction needs run_id")
	}
	return nil
}

func validHeadSHA(value string) bool {
	if len(value) != sha1HexLength {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

// ---- Schemas and fields

const maxActionsID = 9007199254740991

const (
	runFilterSchemas = `"check_suite_id":` + actionsIDSchema + `,` +
		`"head_sha":{"type":"string","minLength":40,"maxLength":40,"pattern":"^[0-9a-fA-F]{40}$"},` +
		`"include_pull_requests":{"type":"boolean"}`
	artifactFilterSchemas = `"name":{"type":"string","minLength":1,"maxLength":256},` +
		`"direction":{"type":"string","enum":["asc","desc"]}`
)

var runFilterArguments = []capability.Argument{
	{Name: "check_suite_id", Description: "Return only runs of this check suite"},
	{Name: "head_sha", Description: "Return only runs of this full commit SHA (40 hexadecimal characters)"},
	{Name: "include_pull_requests", Description: "True adds the pull requests of each run; false when omitted"},
}

var artifactFilterArguments = []capability.Argument{
	{Name: "name", Description: "Return only artifacts with exactly this name, up to 256 characters"},
	{Name: "direction", Description: "Sort direction of the artifacts, asc or desc; GitHub's order when omitted"},
}

var workflowWidthProperties = `"node_id":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"},` +
	`"deleted_at":{"type":"string"},"links":` + linksSchema(workflowLinkKeys)

var workflowWidthFields = []capability.Field{
	{Name: "node_id", Description: "GraphQL node id of the workflow"},
	{Name: "created_at", Description: "When the workflow was created"},
	{Name: "updated_at", Description: "When the workflow last changed"},
	{Name: "deleted_at", Description: "When the workflow was deleted; absent while it exists"},
	{Name: "links", Description: "REST url and badge_url of the workflow by their GitHub names, untrusted data, never fetched"},
}

var runIdentitySchema = objectSchema(`"name":{"type":"string"},"email":{"type":"string"}`)

var runWidthProperties = `"node_id":{"type":"string"},"check_suite_id":{"type":"integer"},` +
	`"check_suite_node_id":{"type":"string"},"path":{"type":"string"},` +
	`"referenced_workflows":{"type":"array","items":` +
	objectSchema(`"path":{"type":"string"},"sha":{"type":"string"},"ref":{"type":"string"}`) + `},` +
	`"referenced_workflows_truncated":{"type":"boolean"},` +
	detailProperty("actor", restAccountSchema, false) + `,"triggering_actor":` + restAccountSchema + `,` +
	`"head_commit":` + objectSchema(`"id":{"type":"string"},"tree_id":{"type":"string"},`+
	`"message":{"type":"string"},"message_truncated":{"type":"boolean"},"timestamp":{"type":"string"},`+
	`"author":`+runIdentitySchema+`,"committer":`+runIdentitySchema) + `,` +
	`"head_repository":` + restRepositorySchema + `,"head_repository_id":{"type":"integer"},` +
	`"pull_requests":{"type":"array","items":` + objectSchema(`"id":{"type":"integer"},"number":{"type":"integer"},`+
	`"head":`+runPullRefSchema+`,"base":`+runPullRefSchema+`,"links":`+linksSchema(runPullLinkKeys)) + `},` +
	`"pull_requests_truncated":{"type":"boolean"},"links":` + linksSchema(runLinkKeys)

var runPullRefSchema = objectSchema(`"ref":{"type":"string"},"sha":{"type":"string"},"repo":` +
	objectSchema(`"id":{"type":"integer"},"name":{"type":"string"},"links":`+linksSchema(runPullLinkKeys)))

var runWidthFields = []capability.Field{
	{Name: "node_id", Description: "GraphQL node id of the run"},
	{Name: "check_suite_id", Description: "Identifier of the check suite of the run"},
	{Name: "check_suite_node_id", Description: "GraphQL node id of the check suite"},
	{Name: "path", Description: "Path of the workflow file with the ref it ran at, untrusted data"},
	{Name: "referenced_workflows", Description: "Reusable workflows the run called, each with path, sha, and ref, " +
		"untrusted data; at most 100"},
	{Name: "referenced_workflows_truncated", Description: "True when the run called more workflows than listed"},
	detailField("actor", "Account", false),
	{Name: "triggering_actor", Description: "Account that triggered the latest attempt: " + restAccountMembers},
	{Name: "head_commit", Description: "Head commit with id, tree_id, message, timestamp, author, and committer " +
		"(each name and email); message and identities are untrusted data, and message_truncated marks a cut message"},
	{Name: "head_repository", Description: "Repository of the head commit, which differs from the repository of the " +
		"call for a fork; a repository outside the targets of the connection shows only id, node_id, and full_name"},
	{Name: "head_repository_id", Description: "Identifier of the repository of the head commit"},
	{Name: "pull_requests", Description: "Pull requests of the run with id, number, head, base (each ref, sha, and " +
		"repository id, name, and links), and links; only when include_pull_requests is true, at most 100"},
	{Name: "pull_requests_truncated", Description: "True when the run has more pull requests than listed"},
	{Name: "links", Description: "REST url and the other *_url addresses of the run by their GitHub names, " +
		"untrusted data, never fetched"},
}

var jobWidthProperties = `"node_id":{"type":"string"},"head_sha":{"type":"string"},"head_branch":{"type":"string"},` +
	`"created_at":{"type":"string"},"labels":{"type":"array","items":{"type":"string"}},` +
	`"labels_truncated":{"type":"boolean"},"runner_id":{"type":"integer"},"runner_name":{"type":"string"},` +
	`"runner_group_id":{"type":"integer"},"runner_group_name":{"type":"string"},"workflow_name":{"type":"string"},` +
	`"links":` + linksSchema(jobLinkKeys)

var jobWidthFields = []capability.Field{
	{Name: "node_id", Description: "GraphQL node id of the job"},
	{Name: "head_sha", Description: "Commit the job ran on"},
	{Name: "head_branch", Description: "Branch the job ran on, untrusted data"},
	{Name: "created_at", Description: "When the job was created"},
	{Name: "labels", Description: "Runner labels the job asked for, untrusted data; at most 50"},
	{Name: "labels_truncated", Description: "True when the job asked for more labels than listed"},
	{Name: "runner_id", Description: "Identifier of the runner that ran the job; absent until one is assigned"},
	{Name: "runner_name", Description: "Name of the runner that ran the job, untrusted data"},
	{Name: "runner_group_id", Description: "Identifier of the runner group of the job"},
	{Name: "runner_group_name", Description: "Name of the runner group, untrusted data"},
	{Name: "workflow_name", Description: "Name of the workflow, untrusted data"},
	{Name: "links", Description: "REST url, run_url, and check_run_url of the job by their GitHub names, untrusted " +
		"data, never fetched"},
}

var artifactWidthProperties = `"node_id":{"type":"string"},"updated_at":{"type":"string"},"workflow_run":` +
	objectSchema(`"id":{"type":"integer"},"repository_id":{"type":"integer"},"head_repository_id":{"type":"integer"},`+
		`"head_branch":{"type":"string"},"head_sha":{"type":"string"}`) +
	`,"links":` + linksSchema(artifactLinkKeys)

var artifactWidthFields = []capability.Field{
	{Name: "node_id", Description: "GraphQL node id of the artifact"},
	{Name: "updated_at", Description: "When the artifact last changed"},
	{Name: "workflow_run", Description: "Run the artifact belongs to: id, repository_id, head_repository_id, " +
		"head_branch, and head_sha"},
	{Name: "links", Description: "REST url of the artifact by its GitHub name, untrusted data, never fetched"},
}

var actionsPermissionsWidthProperties = `"sha_pinning_required":{"type":"boolean"},"links":` + linksSchema(actionsSettingsLinks)

var actionsPermissionsWidthFields = []capability.Field{
	{Name: "sha_pinning_required", Description: "True when workflows must pin actions to a full commit SHA"},
	{Name: "links", Description: "selected_actions_url, the REST address of the selected actions, untrusted data, never fetched"},
}
