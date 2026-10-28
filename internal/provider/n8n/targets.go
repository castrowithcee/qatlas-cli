package n8n

import (
	"errors"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The two target kinds a connection may combine, both optional and independent of each other. An n8n
// identifier, project or workflow, is the nanoid-style alphabet n8n's own generator produces today
// (letters, digits, hyphen, underscore) or a legacy numeric string from an instance that predates it; both
// are accepted as one opaque path segment, never interpreted further.
const (
	projectPrefix  = "project/"
	workflowPrefix = "workflow/"
	// maxTargetIDLength bounds a configured or argument identifier well above any real n8n project or
	// workflow ID, so a malformed value fails fast instead of becoming an oversized path segment.
	maxTargetIDLength = 64
)

// scope is the project and workflow boundary of one connection: independently, either every project of the
// bound instance (an empty project allow-list) or only the projects explicitly listed, and either every
// workflow or only the workflows explicitly listed. The exclusions are kept apart from the targets: they
// remove objects but never change which tools are offered, so instanceWide, requireInstanceScope, reaches
// and the gates that read projects or workflows read the targets only. A reachable project is one the
// targets allow and the exclusions do not name.
type scope struct {
	projects  []string
	workflows []string
	// excluded* win over every target: an excluded workflow is out even when it is shared into a reachable
	// project.
	excludedProjects  []string
	excludedWorkflows []string
}

// excludesProject and excludesWorkflow report a configured exclusion of exactly that ID.
func (s scope) excludesProject(projectID string) bool { return contains(s.excludedProjects, projectID) }
func (s scope) excludesWorkflow(workflowID string) bool {
	return contains(s.excludedWorkflows, workflowID)
}

// restrictsProjects reports whether a project can make an object unreachable, by a target or an exclusion.
// It decides where project membership must be proven; the target-only gates keep reading len(projects).
func (s scope) restrictsProjects() bool { return len(s.projects) > 0 || len(s.excludedProjects) > 0 }

// hasExclusions reports whether the connection excludes any project or workflow.
func (s scope) hasExclusions() bool {
	return len(s.excludedProjects) > 0 || len(s.excludedWorkflows) > 0
}

// reachesProject reports whether a project ID is allowed by the targets and not excluded. It does not
// treat an empty ID specially; callers that must fail closed on an unknown project check for it.
func (s scope) reachesProject(projectID string) bool {
	return s.allowsProject(projectID) && !s.excludesProject(projectID)
}

// allowsWorkflow reports whether a workflow belongs to this connection's local workflow boundary. It says
// nothing about a project allow-list; that is checked separately, live, against the instance's own report of
// project membership, see (*Client).verifyWorkflowScope.
func (s scope) allowsWorkflow(workflowID string) bool {
	return !s.excludesWorkflow(workflowID) && s.targetsWorkflow(workflowID)
}

// targetsWorkflow is the targets-only part of allowsWorkflow.
func (s scope) targetsWorkflow(workflowID string) bool {
	return len(s.workflows) == 0 || contains(s.workflows, workflowID)
}

// allowsProject reports whether a single project ID belongs to this connection's local project boundary. It
// is used for a project_id argument a caller supplies directly, such as workflows.list's filter; a workflow
// or execution named by ID is instead checked against the instance's own report, see allowsAnyProject.
func (s scope) allowsProject(projectID string) bool {
	if len(s.projects) == 0 {
		return true
	}
	return contains(s.projects, projectID)
}

// allowsAnyProject reports whether at least one of a workflow's reported projects is reachable. Without a
// project allow-list and without a project exclusion it admits everything, including a workflow that
// reports no project at all. Otherwise a workflow that reports no project is never admitted: there is
// nothing to compare it against, so it is treated as outside the scope rather than let through.
func (s scope) allowsAnyProject(projectIDs []string) bool {
	if !s.restrictsProjects() {
		return true
	}
	for _, id := range projectIDs {
		if id != "" && s.reachesProject(id) {
			return true
		}
	}
	return false
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// scopeOf reads the bound project and workflow allow-lists and the exclusions of one connection.
func scopeOf(resolved *config.Resolved) (scope, error) {
	return parseScope(provider.TargetsOf(resolved), provider.ExcludesOf(resolved)...)
}

// parseScope reads the configured targets of one connection: zero or more project/PROJECT_ID entries and
// zero or more workflow/WORKFLOW_ID entries, none named twice within its own kind. The optional exclusions
// use the same two forms, each named once and never equal to a target; the checks do not rely on the core
// validation, so an invalid exclusion fails closed here too. No error ever quotes a configured value.
func parseScope(values []string, excludes ...string) (scope, error) {
	var bound scope
	seenProjects, seenWorkflows := map[string]bool{}, map[string]bool{}
	for _, raw := range values {
		kind, id, err := parseTarget(strings.TrimSpace(raw))
		if err != nil {
			return scope{}, err
		}
		switch kind {
		case "project":
			if seenProjects[id] {
				return scope{}, errors.New("the n8n project target list names a project more than once")
			}
			seenProjects[id] = true
			bound.projects = append(bound.projects, id)
		case "workflow":
			if seenWorkflows[id] {
				return scope{}, errors.New("the n8n workflow target list names a workflow more than once")
			}
			seenWorkflows[id] = true
			bound.workflows = append(bound.workflows, id)
		}
	}
	if err := bound.parseExcludes(excludes); err != nil {
		return scope{}, err
	}
	return bound, nil
}

// parseExcludes reads the exclusions into the scope, which already holds the targets.
func (s *scope) parseExcludes(excludes []string) error {
	invalid := errors.New("an n8n exclusion must be project/PROJECT_ID or workflow/WORKFLOW_ID, named once " +
		"and different from every target")
	for _, raw := range excludes {
		kind, id, err := parseTarget(strings.TrimSpace(raw))
		if err != nil {
			return invalid
		}
		excluded, targets := &s.excludedProjects, s.projects
		if kind == "workflow" {
			excluded, targets = &s.excludedWorkflows, s.workflows
		}
		if contains(*excluded, id) || contains(targets, id) {
			return invalid
		}
		*excluded = append(*excluded, id)
	}
	return nil
}

// validateTarget checks the form of one configured target in isolation, before configuration validation
// checks the whole set with parseScope.
func validateTarget(raw string) error {
	_, _, err := parseTarget(strings.TrimSpace(raw))
	return err
}

// parseTarget reads one configured target as project/PROJECT_ID or workflow/WORKFLOW_ID.
func parseTarget(raw string) (kind, id string, err error) {
	switch {
	case strings.HasPrefix(raw, projectPrefix):
		kind, id = "project", strings.TrimPrefix(raw, projectPrefix)
	case strings.HasPrefix(raw, workflowPrefix):
		kind, id = "workflow", strings.TrimPrefix(raw, workflowPrefix)
	default:
		return "", "", errors.New("an n8n target must be project/PROJECT_ID or workflow/WORKFLOW_ID")
	}
	if !validTargetID(id) {
		return "", "", errors.New("an n8n target ID must be letters, digits, '-' or '_', 1 to " +
			strconv.Itoa(maxTargetIDLength) + " characters")
	}
	return kind, id, nil
}

// validTargetID keeps an identifier, whether configured or given as an agent argument, to the character
// class n8n's own generator produces. It is never used to build a URL, only a single path segment or query
// value of the fixed n8n Public API paths this provider calls.
func validTargetID(value string) bool {
	if value == "" || len(value) > maxTargetIDLength {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			continue
		default:
			return false
		}
	}
	return true
}

// boundScope reads the connection's scope, wrapping a parse failure as a provider error the same way a
// missing connection is: both are configuration problems that exist before any secret is resolved.
func boundScope(resolved *config.Resolved) (scope, error) {
	if resolved == nil {
		return scope{}, providerError("open", "no connection was selected")
	}
	bound, err := scopeOf(resolved)
	if err != nil {
		return scope{}, providerError("open", err.Error())
	}
	return bound, nil
}

// selectWorkflow checks a workflow_id argument against the connection's local workflow allow-list and
// exclusions before any secret is resolved and before any request is sent. A workflow outside a configured
// allow-list, or an excluded one, is refused as an invalid request, never as a provider failure, because
// the connection's own configuration decided against it; the message names neither. Whether the workflow
// also belongs to a reachable project is checked separately, live, see (*Client).verifyWorkflowScope.
func selectWorkflow(resolved *config.Resolved, workflowID string) error {
	excluded, err := selectWorkflowFilter(resolved, workflowID)
	if err != nil {
		return err
	}
	if excluded {
		return invalidRequest("workflow_id is outside the targets of this connection")
	}
	return nil
}

// selectWorkflowFilter is selectWorkflow for a workflow_id that only filters a list: it reports an excluded
// workflow instead of refusing it, so the list can answer with an empty page.
func selectWorkflowFilter(resolved *config.Resolved, workflowID string) (excluded bool, err error) {
	bound, err := boundScope(resolved)
	if err != nil {
		return false, err
	}
	if !validTargetID(workflowID) {
		return false, invalidRequest("workflow_id must be a usable n8n identifier")
	}
	if !bound.targetsWorkflow(workflowID) {
		return false, invalidRequest("workflow_id is outside the targets of this connection")
	}
	return bound.excludesWorkflow(workflowID), nil
}

// selectProjectFilter checks a project_id argument a caller supplies directly (a list filter) against the
// connection's local project allow-list before any request is sent. An excluded project is reported, not
// refused, so the list can answer with an empty page; see selectProjectTarget for a project that receives
// an object.
func selectProjectFilter(resolved *config.Resolved, projectID string) (excluded bool, err error) {
	bound, err := boundScope(resolved)
	if err != nil {
		return false, err
	}
	if !validTargetID(projectID) {
		return false, invalidRequest("project_id must be a usable n8n identifier")
	}
	if !bound.allowsProject(projectID) {
		return false, invalidRequest("project_id is outside the targets of this connection")
	}
	return bound.excludesProject(projectID), nil
}

// selectProjectTarget is selectProjectFilter for a project that receives an object: an excluded project is
// refused like one outside the targets, before any request.
func selectProjectTarget(resolved *config.Resolved, projectID string) error {
	excluded, err := selectProjectFilter(resolved, projectID)
	if err != nil {
		return err
	}
	if excluded {
		return invalidRequest("project_id is outside the targets of this connection")
	}
	return nil
}

// requireProjectID is the create gate for a connection that excludes a project: without project_id n8n
// places the object in the personal project of the key owner, which Qatlas cannot name through the Public
// API and so cannot prove reachable. The narrower reading demands an explicit project_id. Targets are not
// involved; the target gates of each create tool stay as they are.
func (s scope) requireProjectID(projectID string) error {
	if projectID == "" && len(s.excludedProjects) > 0 {
		return invalidRequest("project_id is required on this connection: without it n8n would use the " +
			"personal project of the API key owner, which cannot be checked")
	}
	return nil
}

// instanceWide reports whether the connection carries neither a project nor a workflow target, which is
// what an instance-wide tool requires: such a tool acts on objects no target can bound.
func (s scope) instanceWide() bool {
	return len(s.projects) == 0 && len(s.workflows) == 0
}

// requireInstanceScope is the local gate of every instance-wide tool. It admits only a connection without
// project and workflow targets and refuses any other one, before a secret is resolved and before any
// request is sent. what names the refused capability in the message, never a configured target.
func requireInstanceScope(resolved *config.Resolved, what string) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if !bound.instanceWide() {
		return invalidRequest("this connection has project or workflow targets, so it cannot use " + what +
			": it is instance-wide and only available on a connection without targets")
	}
	return nil
}

// reaches says whether a binding reaches a tool, mirroring what the call itself refuses. Instance-wide tools
// (requireInstanceScope) and project creation are refused by any project or workflow target and no target
// form reaches them. Credentials, variables, data tables, columns, rows, project changes, project members,
// and workflow creation are refused by any workflow target; a project target alone reaches them. A
// connection without targets, or whose targets do not parse, reaches everything; the checks at the call stay
// in force.
func reaches(tool string, targets []string) (bool, []string) {
	bound, err := parseScope(targets)
	if err != nil || bound.instanceWide() {
		return true, nil
	}
	switch {
	case strings.HasPrefix(tool, Provider+".tags."), strings.HasPrefix(tool, Provider+".users."),
		strings.HasPrefix(tool, Provider+".source-control."), tool == Provider+".audit.generate",
		tool == projectsCreate.ID:
		return false, nil
	case len(bound.workflows) == 0:
		return true, nil
	case strings.HasPrefix(tool, Provider+".credentials."), strings.HasPrefix(tool, Provider+".variables."),
		strings.HasPrefix(tool, Provider+".data-"), strings.HasPrefix(tool, Provider+".project-members."),
		tool == projectsUpdate.ID, tool == projectsDelete.ID, tool == workflowsCreate.ID:
		if len(bound.projects) > 0 {
			return false, nil
		}
		return false, []string{"project/PROJECT_ID"}
	}
	return true, nil
}
