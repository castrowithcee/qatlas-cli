package forge

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The Actions tools act on the workflow runs, jobs, and tasks of one repository inside the connection's
// targets. Gitea also lists runs and jobs of an organization or of the user behind the token (owner); that
// level needs the owner as a target of the same kind, and each row is checked against the targets again by the
// repository it names. A run or job ID travels in a fixed path below the repository, which binds it; an answer
// must carry the requested ID and, where it names its repository, a repository the targets allow. Forgejo
// added its Actions API in steps, so most operations name the oldest version that has them.

const (
	actionsSensitivity = "-actions-data"
	actionsSchemaID    = `{"type":"integer","minimum":1,"maximum":9007199254740991}`
	maxActionsFilter   = 255
	maxActionsEvents   = 20
)

// Versions the Actions operations first appeared in.
var (
	forgejoActions16 = version{16, 0, 0}
	forgejoActions17 = version{17, 0, 0}
	giteaActions28   = version{28, 0, 0}
)

// workflowFile is the name of a workflow file such as build.yml.
var workflowFile = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,254}$`)

var forgejoStatuses = []string{"unknown", "waiting", "running", "success", "failure", "cancelled", "skipped", "blocked"}

var (
	runIDArgument = capability.Argument{Name: "run_id", Required: true,
		Description: "Numeric ID of the workflow run, as workflow-runs.list reports it as id; a run of another " +
			"repository is not found"}
	jobIDArgument = capability.Argument{Name: "job_id", Required: true,
		Description: "Numeric ID of the job, as workflow-jobs.list reports it as id; a job of another repository " +
			"is not found"}
	attemptArgument = capability.Argument{Name: "attempt", Required: true,
		Description: "Number of the attempt of the run, from 1; the run reports its current one as run_attempt"}
	actionsOwnerArgument = capability.Argument{Name: "owner", Target: kindOwner,
		Description: "Gitea only: list the runs or jobs of an organization (orgs/NAME) or of the user behind the " +
			"token (users/NAME) instead of one repository; pass either repository or owner. A connection with " +
			"targets must name that owner as an owner target of the same kind; the rows are checked against " +
			"the targets by their repository"}
)

func actionsPath(repo target) string {
	return "/repos/" + escape(repo.owner) + "/" + escape(repo.repo) + "/actions"
}

// Members of the objects the Actions tools read. title, commit, branch, run_number, started_at, and
// completed_at are filled for both products, from the member each product names them.
var actionsRepositoryMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the repository"},
	{name: "name", kind: kindText, note: "Repository name"},
	{name: "full_name", kind: kindText, note: "Canonical OWNER/REPO"},
	{name: "html_url", kind: kindText, note: "Address of the repository page"},
	{name: "private", kind: kindFlag, note: "True for a private repository"},
	{name: "owner", kind: kindObject, sub: []member{
		{name: "id", kind: kindInteger, note: "Numeric ID of the owner"},
		{name: "login", kind: kindText, note: "Name of the owner"},
	}, note: "Owner of the repository"},
}

var minimalPullHeadMembers = []member{
	{name: "ref", kind: kindText, note: "Branch name, untrusted data"},
	{name: "sha", kind: kindText, note: "Commit ID"},
	{name: "repo", kind: kindObject, sub: []member{
		{name: "id", kind: kindInteger, note: "Numeric ID of the repository"},
		{name: "name", kind: kindText, note: "Repository name"},
		{name: "url", kind: kindText, note: "API address of the repository"},
	}, note: "Repository of the branch"},
}

var minimalPullMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the pull request"},
	{name: "number", kind: kindInteger, note: "Index of the pull request"},
	{name: "url", kind: kindText, note: "API address of the pull request"},
	{name: "base", kind: kindObject, sub: minimalPullHeadMembers, note: "Base branch"},
	{name: "head", kind: kindObject, sub: minimalPullHeadMembers, note: "Head branch"},
}

var workflowRunMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the run"},
	{name: "title", kind: kindText, note: "Title of the run, untrusted data; Gitea reports display_title"},
	{name: "status", kind: kindText, note: "Status of the run; Forgejo reports its conclusion here as well"},
	{name: "event", kind: kindText, note: "Event that triggered the run"},
	{name: "commit", kind: kindText, note: "Commit ID the run belongs to; Gitea reports head_sha, Forgejo commit_sha"},
	{name: "branch", kind: kindText, note: "Branch or reference of the run, untrusted data; Gitea reports head_branch, " +
		"Forgejo prettyref"},
	{name: "run_number", kind: kindInteger, note: "Number of the run in the repository; Forgejo reports index_in_repo"},
	{name: "started_at", kind: kindText, note: "Start time; Forgejo reports started"},
	{name: "completed_at", kind: kindText, note: "End time; Forgejo reports stopped"},
	{name: "html_url", kind: kindText, note: "Address of the run page"},
	{name: "repository", kind: kindObject, sub: actionsRepositoryMembers, note: "Repository of the run"},
	{name: "workflow_id", kind: kindText, only: forgejo, note: "Workflow file of the run"},
	{name: "trigger_event", kind: kindText, only: forgejo, note: "Event that triggered the run"},
	{name: "prettyref", kind: kindText, only: forgejo, note: "Short reference of the run, untrusted data"},
	{name: "commit_sha", kind: kindText, only: forgejo, note: "Commit ID the run belongs to"},
	{name: "index_in_repo", kind: kindInteger, only: forgejo, note: "Number of the run in the repository"},
	{name: "ScheduleID", kind: kindInteger, only: forgejo, note: "ID of the schedule that started the run, 0 for none"},
	{name: "approved_by", kind: kindInteger, only: forgejo, note: "ID of the user who approved the run"},
	{name: "need_approval", kind: kindFlag, only: forgejo, note: "True while the run waits for an approval"},
	{name: "is_fork_pull_request", kind: kindFlag, only: forgejo, note: "True for a run of a fork's pull request"},
	{name: "is_ref_deleted", kind: kindFlag, only: forgejo, note: "True when the run's reference was deleted"},
	{name: "trigger_user", kind: kindObject, sub: personMembers, only: forgejo, note: "User who triggered the run; " +
		"personal data"},
	{name: "created", kind: kindText, only: forgejo, note: "Creation time"},
	{name: "started", kind: kindText, only: forgejo, note: "Start time"},
	{name: "stopped", kind: kindText, only: forgejo, note: "End time"},
	{name: "updated", kind: kindText, only: forgejo, note: "Time of the last update"},
	{name: "duration", kind: kindInteger, only: forgejo, note: "Duration in nanoseconds"},
	{name: "display_title", kind: kindText, only: gitea, note: "Title of the run, untrusted data"},
	{name: "head_sha", kind: kindText, only: gitea, note: "Commit ID the run belongs to"},
	{name: "head_branch", kind: kindText, only: gitea, note: "Branch of the run, untrusted data"},
	{name: "head_repository", kind: kindObject, sub: actionsRepositoryMembers, only: gitea,
		note: "Repository of the head, which differs for a fork"},
	{name: "repository_id", kind: kindInteger, only: gitea, note: "Numeric ID of the repository"},
	{name: "path", kind: kindText, only: gitea, note: "Workflow file with its reference"},
	{name: "conclusion", kind: kindText, only: gitea, note: "Conclusion of a finished run"},
	{name: "run_attempt", kind: kindInteger, only: gitea, note: "Number of the current attempt"},
	{name: "actor", kind: kindObject, sub: personMembers, only: gitea, note: "User the run belongs to; personal data"},
	{name: "trigger_actor", kind: kindObject, sub: personMembers, only: gitea, note: "User who triggered the run; " +
		"personal data"},
	{name: "pull_requests", kind: kindObjects, sub: minimalPullMembers, only: gitea, note: "Pull requests of the run"},
	{name: "created_at", kind: kindText, only: gitea, note: "Creation time; Gitea 28 and later"},
	{name: "updated_at", kind: kindText, only: gitea, note: "Time of the last update; Gitea 28 and later"},
	{name: "url", kind: kindText, only: gitea, note: "API address of the run"},
	{name: "previous_attempt_url", kind: kindText, only: gitea, note: "API address of the previous attempt"},
	{name: "jobs_url", kind: kindText, only: gitea, note: "API address of the jobs; Gitea 28 and later"},
	{name: "logs_url", kind: kindText, only: gitea, note: "API address of the logs; Gitea 28 and later"},
	{name: "artifacts_url", kind: kindText, only: gitea, note: "API address of the artifacts; Gitea 28 and later"},
	{name: "cancel_url", kind: kindText, only: gitea, note: "API address to cancel; Gitea 28 and later"},
	{name: "rerun_url", kind: kindText, only: gitea, note: "API address to rerun; Gitea 28 and later"},
}

var forgejoJobStepMembers = []member{
	{name: "number", kind: kindInteger, note: "Number of the step"},
	{name: "name", kind: kindText, note: "Name of the step, untrusted data"},
	{name: "status", kind: kindText, note: "Status of the step"},
	{name: "started", kind: kindText, note: "Start time"},
	{name: "stopped", kind: kindText, note: "End time"},
}

var giteaJobStepMembers = []member{
	{name: "number", kind: kindInteger, note: "Number of the step"},
	{name: "name", kind: kindText, note: "Name of the step, untrusted data"},
	{name: "status", kind: kindText, note: "Status of the step"},
	{name: "conclusion", kind: kindText, note: "Conclusion of a finished step"},
	{name: "started_at", kind: kindText, note: "Start time"},
	{name: "completed_at", kind: kindText, note: "End time"},
}

var workflowJobMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the job"},
	{name: "name", kind: kindText, note: "Name of the job, untrusted data"},
	{name: "status", kind: kindText, note: "Status of the job"},
	{name: "run_id", kind: kindInteger, note: "Numeric ID of the run; Forgejo 16 and later"},
	{name: "attempt", kind: kindInteger, note: "Number of the attempt; Gitea reports run_attempt"},
	{name: "html_url", kind: kindText, note: "Address of the job page; Forgejo 17 and later"},
	{name: "handle", kind: kindText, only: forgejo, note: "Handle of the job in its workflow, untrusted data"},
	{name: "needs", kind: kindTexts, only: forgejo, note: "Jobs this job waits for"},
	{name: "runs_on", kind: kindTexts, only: forgejo, note: "Labels of the runner the job asks for"},
	{name: "owner_id", kind: kindInteger, only: forgejo, note: "Numeric ID of the owner"},
	{name: "repo_id", kind: kindInteger, only: forgejo, note: "Numeric ID of the repository"},
	{name: "task_id", kind: kindInteger, only: forgejo, note: "ID of the task of the latest attempt"},
	{name: "steps", kind: kindObjects, sub: forgejoJobStepMembers, only: forgejo, note: "Steps; Forgejo 17 and later"},
	{name: "conclusion", kind: kindText, only: gitea, note: "Conclusion of a finished job"},
	{name: "run_attempt", kind: kindInteger, only: gitea, note: "Number of the attempt"},
	{name: "run_url", kind: kindText, only: gitea, note: "API address of the run"},
	{name: "head_sha", kind: kindText, only: gitea, note: "Commit ID the job belongs to"},
	{name: "head_branch", kind: kindText, only: gitea, note: "Branch of the job, untrusted data"},
	{name: "labels", kind: kindTexts, only: gitea, note: "Labels of the runner the job asks for"},
	{name: "runner_id", kind: kindInteger, only: gitea, note: "Numeric ID of the runner"},
	{name: "runner_name", kind: kindText, only: gitea, note: "Name of the runner, untrusted data"},
	{name: "steps", kind: kindObjects, sub: giteaJobStepMembers, only: gitea, note: "Steps"},
	{name: "created_at", kind: kindText, only: gitea, note: "Creation time"},
	{name: "started_at", kind: kindText, only: gitea, note: "Start time"},
	{name: "completed_at", kind: kindText, only: gitea, note: "End time"},
	{name: "url", kind: kindText, only: gitea, note: "API address of the job"},
}

var actionTaskMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the task"},
	{name: "name", kind: kindText, note: "Name of the task, untrusted data"},
	{name: "display_title", kind: kindText, note: "Title of the run, untrusted data"},
	{name: "status", kind: kindText, note: "Status of the task"},
	{name: "event", kind: kindText, note: "Event that triggered the run"},
	{name: "head_branch", kind: kindText, note: "Branch of the run, untrusted data"},
	{name: "head_sha", kind: kindText, note: "Commit ID of the run"},
	{name: "run_number", kind: kindInteger, note: "Number of the run"},
	{name: "workflow_id", kind: kindText, note: "Workflow file"},
	{name: "created_at", kind: kindText, note: "Creation time"},
	{name: "run_started_at", kind: kindText, note: "Start time"},
	{name: "updated_at", kind: kindText, note: "Time of the last update"},
	{name: "url", kind: kindText, note: "API address of the task"},
}

// copyText and copyInteger fill a normalized member from the member the product names it.
func copyText(out map[string]any, to string, row object, from string) {
	if value, ok := row.text(from); ok {
		out[to] = value
	}
}

func copyInteger(out map[string]any, to string, row object, from string) {
	if value, ok := row.integer(from); ok {
		out[to] = value
	}
}

func projectRun(p *product, row object) map[string]any {
	out := project(p, workflowRunMembers, row)
	if p == gitea {
		copyText(out, "title", row, "display_title")
		copyText(out, "commit", row, "head_sha")
		copyText(out, "branch", row, "head_branch")
		return out
	}
	copyText(out, "commit", row, "commit_sha")
	copyText(out, "branch", row, "prettyref")
	copyInteger(out, "run_number", row, "index_in_repo")
	copyText(out, "started_at", row, "started")
	copyText(out, "completed_at", row, "stopped")
	return out
}

func projectJob(p *product, row object) map[string]any {
	out := project(p, workflowJobMembers, row)
	if p == gitea {
		copyInteger(out, "attempt", row, "run_attempt")
	}
	return out
}

func runSchema(p *product) string { return schemaOf(p, workflowRunMembers, "id") }
func jobSchema(p *product) string { return schemaOf(p, workflowJobMembers, "id") }

// actionsLevel is where a list reads: one repository, or for Gitea an organization or the user behind the token.
type actionsLevel struct {
	base  string
	name  string // OWNER/REPO, orgs/NAME, or users/NAME
	owner string // owner name on the owner level
	login string // user the token must belong to on the user level
	repo  bool
	// target is the repository of a repository level.
	target target
}

func repoLevel(repo target) actionsLevel {
	return actionsLevel{base: actionsPath(repo), name: repo.owner + "/" + repo.repo, repo: true, target: repo}
}

// namesActionsOwner reports whether the list admits the owner by an owner target of the same kind.
func (a allowlist) namesActionsOwner(owner target) bool {
	if a.open() {
		return true
	}
	for _, entry := range a.targets {
		if entry.kind == kindOwner && entry.scope == owner.scope && strings.EqualFold(entry.owner, owner.owner) {
			return true
		}
	}
	return false
}

// chooseActionsLevel reads exactly one of repository and owner. Only Gitea has an owner level for runs and jobs.
func chooseActionsLevel(p *product, allowed allowlist, repository, owner string) (actionsLevel, error) {
	if owner != "" && p != gitea {
		return actionsLevel{}, invalidArgument("owner is a Gitea argument; " + p.name + " lists the runs of one repository")
	}
	return actionsLevelOf(allowed, repository, owner)
}

// actionsLevelOf reads exactly one of repository and owner, on any product.
func actionsLevelOf(allowed allowlist, repository, owner string) (actionsLevel, error) {
	if owner == "" {
		repo, err := allowed.chooseRepository(repository)
		if err != nil {
			return actionsLevel{}, err
		}
		return repoLevel(repo), nil
	}
	if repository != "" {
		return actionsLevel{}, invalidArgument("pass either repository or owner")
	}
	parsed, err := parseOwnerArgument(owner)
	if err != nil {
		return actionsLevel{}, err
	}
	if !allowed.namesActionsOwner(parsed) {
		return actionsLevel{}, outsideTargets("owner")
	}
	if parsed.scope == "orgs" {
		return actionsLevel{base: "/orgs/" + escape(parsed.owner) + "/actions", name: parsed.String(), owner: parsed.owner}, nil
	}
	return actionsLevel{base: "/user/actions", name: parsed.String(), owner: parsed.owner, login: parsed.owner}, nil
}

// verify is the evidence read of the user level: /user/actions acts on the account behind the token, so the
// user named by owner must be that account.
func (l actionsLevel) verify(ctx context.Context, c *Client, op string) error {
	if l.login == "" {
		return nil
	}
	var account object
	if _, err := c.get(ctx, op, "/user", nil, &account); err != nil {
		return err
	}
	login, ok := account.text("login")
	if !ok || login == "" {
		return provider.InvalidResponse(op, c.product.name+" returned an account without a login")
	}
	if !strings.EqualFold(login, l.login) {
		return invalidArgument("the user level acts on the account behind the token only; it is not the user in " +
			"owner")
	}
	return nil
}

// admits checks the canonical OWNER/REPO a row names against the targets and, on the owner level, against the
// owner. A row that names none belongs to a repository level only.
func (l actionsLevel) admits(allowed allowlist, fullName string, named bool) bool {
	if !named {
		return l.repo
	}
	owner, _, _ := strings.Cut(fullName, "/")
	if !l.repo && !strings.EqualFold(owner, l.owner) {
		return false
	}
	return allowed.allowsFullName(fullName)
}

func runRepository(row object) (string, bool) {
	repository, ok := row.child("repository")
	if !ok {
		return "", false
	}
	name, ok := repository.text("full_name")
	return name, ok && name != ""
}

// jobRepository reads the repository a Gitea job names in its API or page address on the instance's own origin.
func jobRepository(origin string, row object) (string, bool) {
	base, err := url.Parse(origin)
	if err != nil {
		return "", false
	}
	for _, key := range []string{"url", "html_url"} {
		address, ok := row.text(key)
		if !ok {
			continue
		}
		parsed, err := url.Parse(address)
		if err != nil || !strings.EqualFold(parsed.Host, base.Host) {
			continue
		}
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if key == "url" {
			if len(parts) >= 6 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "repos" && parts[5] == "actions" {
				return parts[3] + "/" + parts[4], true
			}
			continue
		}
		if len(parts) >= 3 && parts[2] == "actions" {
			return parts[0] + "/" + parts[1], true
		}
	}
	return "", false
}

// textFilter reads a filter that is a list for Forgejo and one value for Gitea.
func textFilter(raw json.RawMessage, name string, list bool) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var values []string
	if list {
		if json.Unmarshal(raw, &values) != nil {
			return nil, invalidArgument(name + " must be a list of texts")
		}
	} else {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return nil, invalidArgument(name + " must be a text")
		}
		values = []string{value}
	}
	if len(values) > maxActionsEvents {
		return nil, invalidArgument(name + " takes at most 20 entries")
	}
	for _, value := range values {
		if value == "" || len(value) > 64 || !plainText(value) {
			return nil, invalidArgument(name + " takes entries of 1 to 64 characters without control characters")
		}
	}
	return values, nil
}

func plainFilter(name, value string) error {
	if len(value) > maxActionsFilter || !plainText(value) {
		return invalidArgument(name + " must be at most 255 characters without control characters")
	}
	return nil
}

func checkActionsID(name string, id int64) error {
	if id < 1 {
		return invalidArgument(name + " must be a numeric ID")
	}
	return nil
}

// listAnswer decodes a list answer that is a wrapper with a total and a key on the products that wrap it, and
// a plain array on the others.
func listAnswer(op string, c *Client, raw json.RawMessage, key string, wrapped bool) ([]object, *int64, error) {
	if !wrapped {
		var rows []object
		if json.Unmarshal(raw, &rows) != nil {
			return nil, nil, provider.InvalidResponse(op, c.product.name+" returned an invalid list")
		}
		return rows, nil, nil
	}
	var wrapper map[string]json.RawMessage
	if json.Unmarshal(raw, &wrapper) != nil || wrapper == nil {
		return nil, nil, provider.InvalidResponse(op, c.product.name+" returned an invalid list")
	}
	var rows []object
	if json.Unmarshal(wrapper[key], &rows) != nil {
		return nil, nil, provider.InvalidResponse(op, c.product.name+" returned an invalid list")
	}
	var total int64
	if json.Unmarshal(wrapper["total_count"], &total) != nil {
		return rows, nil, nil
	}
	return rows, &total, nil
}

// listOutput is the output schema of a list of rows.
func listOutput(key, rowSchema string) json.RawMessage {
	schema := `{"type":"object","properties":{"` + key + `":{"type":"array","items":` + rowSchema + `},` +
		`"total_count":{"type":"integer"},"repository":{"type":"string"},"owner":{"type":"string"},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},"required":["` + key +
		`","has_more"],"additionalProperties":false}`
	return json.RawMessage(schema)
}

func listFields(key, what string) []capability.Field {
	return append([]capability.Field{
		{Name: key, Description: what},
		{Name: "total_count", Description: "Number of entries the instance counts for the filters, where it " +
			"reports one"},
		{Name: "repository", Description: "Repository the tool listed, as OWNER/REPO"},
		{Name: "owner", Description: "Owner the tool listed, on the owner level"},
	}, pagingFields...)
}

func (l actionsLevel) result(key string, rows []map[string]any, total *int64, next string) map[string]any {
	if rows == nil {
		rows = []map[string]any{}
	}
	result := map[string]any{key: rows, "has_more": next != ""}
	if total != nil {
		result["total_count"] = *total
	}
	if l.repo {
		result["repository"] = l.name
	} else {
		result["owner"] = l.name
	}
	if next != "" {
		result["next_cursor"] = next
	}
	return result
}

// runAnswer checks a run answer: it must carry the ID asked for, and a repository it names must be allowed.
func runAnswer(c *Client, allowed allowlist, op string, row object, id int64) error {
	if got, ok := row.integer("id"); !ok || got != id {
		return provider.InvalidResponse(op, c.product.name+" returned a run without the requested ID")
	}
	if name, ok := runRepository(row); ok && !allowed.allowsFullName(name) {
		return provider.InvalidResponse(op, c.product.name+" returned a run of a repository outside the targets")
	}
	return nil
}
