package n8n

import (
	"errors"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
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
// workflow or only the workflows explicitly listed.
type scope struct {
	projects  []string
	workflows []string
}

// allowsWorkflow reports whether a workflow belongs to this connection's local workflow boundary. It says
// nothing about a project allow-list; that is checked separately, live, against the instance's own report of
// project membership, see (*Client).verifyWorkflowScope.
func (s scope) allowsWorkflow(workflowID string) bool {
	if len(s.workflows) == 0 {
		return true
	}
	return contains(s.workflows, workflowID)
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

// allowsAnyProject reports whether at least one of a workflow's reported projects is inside this
// connection's project allow-list. An empty allow-list admits everything, including a workflow that reports
// no project at all. A non-empty allow-list never admits a workflow that reports no project: there is
// nothing to compare it against, so it is treated as outside the scope rather than let through.
func (s scope) allowsAnyProject(projectIDs []string) bool {
	if len(s.projects) == 0 {
		return true
	}
	for _, id := range projectIDs {
		if contains(s.projects, id) {
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

// scopeOf reads the bound project and workflow allow-lists of one connection.
func scopeOf(resolved *config.Resolved) (scope, error) {
	values := resolved.Targets
	if len(values) == 0 && strings.TrimSpace(resolved.Target) != "" {
		values = []string{resolved.Target}
	}
	return parseScope(values)
}

// parseScope reads the configured targets of one connection: zero or more project/PROJECT_ID entries and
// zero or more workflow/WORKFLOW_ID entries, none named twice within its own kind. No error ever quotes a
// configured value.
func parseScope(values []string) (scope, error) {
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
	return bound, nil
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

// selectWorkflow checks a workflow_id argument against the connection's local workflow allow-list before
// any secret is resolved and before any request is sent. A workflow outside a configured allow-list is
// refused as an invalid request, never as a provider failure, because the connection's own configuration
// decided against it. Whether the workflow also belongs to an allowed project is checked separately, live,
// see (*Client).verifyWorkflowScope.
func selectWorkflow(resolved *config.Resolved, workflowID string) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if !validTargetID(workflowID) {
		return invalidRequest("workflow_id must be a usable n8n identifier")
	}
	if !bound.allowsWorkflow(workflowID) {
		return invalidRequest("workflow_id is outside the targets of this connection")
	}
	return nil
}

// selectProjectFilter checks a project_id argument a caller supplies directly (workflows.list's filter)
// against the connection's local project allow-list before any request is sent.
func selectProjectFilter(resolved *config.Resolved, projectID string) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if !validTargetID(projectID) {
		return invalidRequest("project_id must be a usable n8n identifier")
	}
	if !bound.allowsProject(projectID) {
		return invalidRequest("project_id is outside the targets of this connection")
	}
	return nil
}
