package github

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// The target arguments. Every tool takes the repository or the project it acts on; a tool that adds an issue
// to a project takes both, and a tool that lists what an owner holds takes the owner. Discovery says when an
// argument may be left out.
const (
	projectPattern = `^(users|orgs)/[A-Za-z0-9][A-Za-z0-9_-]{0,99}/projects/[1-9][0-9]{0,8}$`
	projectSchema  = `{"type":"string","maxLength":120,"pattern":"` + projectPattern + `",` +
		`"x-form":"users/LOGIN/projects/NUMBER or orgs/LOGIN/projects/NUMBER"}`
	ownerPattern = `^(users|orgs)/[A-Za-z0-9][A-Za-z0-9_-]{0,99}$`
	ownerSchema  = `{"type":"string","maxLength":105,"pattern":"` + ownerPattern + `",` +
		`"x-form":"users/LOGIN or orgs/LOGIN"}`
)

var repositoryArgument = capability.Argument{Name: "repository", Description: "Repository as OWNER/REPO; " +
	"optional when the connection's targets allow exactly one repository; must lie inside the targets when the " +
	"connection lists any"}

var projectArgument = capability.Argument{Name: "project", Description: "Project as users/LOGIN/projects/NUMBER " +
	"or orgs/LOGIN/projects/NUMBER; optional when the connection's targets allow exactly one project; must lie " +
	"inside the targets when the connection lists any"}

var ownerArgument = capability.Argument{Name: "owner", Description: "Owner as users/LOGIN for a user or " +
	"orgs/LOGIN for an organization; optional when the connection's targets name exactly one owner; must lie " +
	"inside the targets when the connection lists any, as an owner or through a target of this owner"}

var ownerField = capability.Field{Name: "owner", Description: "Owner the tool listed, as users/LOGIN or " +
	"orgs/LOGIN; the one a default chose when the argument was left out"}

var repositoryField = capability.Field{Name: "repository", Description: "Repository the tool acted on, as " +
	"OWNER/REPO; the one a default chose when the argument was left out"}

var projectField = capability.Field{Name: "project", Description: "Project the tool acted on, as " +
	"users/LOGIN/projects/NUMBER or orgs/LOGIN/projects/NUMBER; the one a default chose when the argument was " +
	"left out"}

// withTargetArgument adds the target argument of its kind to one tool: owner to the owner lists, project to
// the project tools, repository to every other tool. Every result names the target the tool acted on in a
// field of the same name; the project tools that add or create an issue also name its repository, which they
// take as well.
func withTargetArgument(d capability.Descriptor) capability.Descriptor {
	name, schema, argument := "repository", repoSchema, repositoryArgument
	fields := []capability.Field{repositoryField}
	if d.ID == projectsList.ID || d.ID == repositoriesList.ID {
		name, schema, argument = "owner", ownerSchema, ownerArgument
		fields = []capability.Field{ownerField}
	} else if strings.HasPrefix(d.ID, Provider+".project") {
		name, schema, argument = "project", projectSchema, projectArgument
		fields = []capability.Field{projectField}
		if d.ID == itemsAdd.ID || d.ID == projectIssuesCreate.ID {
			fields = append(fields, repositoryField)
		}
	}
	var input, properties map[string]json.RawMessage
	if json.Unmarshal(d.InputSchema, &input) != nil || json.Unmarshal(input["properties"], &properties) != nil {
		// Registration validates every schema, so a broken one still fails loudly there.
		return d
	}
	properties[name] = json.RawMessage(schema)
	input["properties"], _ = json.Marshal(properties)
	d.InputSchema, _ = json.Marshal(input)
	d.Arguments = append(append([]capability.Argument(nil), d.Arguments...), argument)

	var output, results map[string]json.RawMessage
	var required []string
	if json.Unmarshal(d.OutputSchema, &output) != nil || json.Unmarshal(output["properties"], &results) != nil ||
		(output["required"] != nil && json.Unmarshal(output["required"], &required) != nil) {
		return d
	}
	for _, field := range fields {
		results[field.Name] = json.RawMessage(`{"type":"string"}`)
		required = append(required, field.Name)
	}
	output["properties"], _ = json.Marshal(results)
	output["required"], _ = json.Marshal(required)
	d.OutputSchema, _ = json.Marshal(output)
	d.Fields = append(append([]capability.Field(nil), d.Fields...), fields...)
	return d
}

// allowlist is the targets of one connection. Empty, it allows every repository, project, and owner the token
// reaches; otherwise only the listed ones. The token stays an independent upper bound, so the effective
// scope is what both allow.
type allowlist []target

func allowlistOf(resolved *config.Resolved) (allowlist, error) {
	values := resolved.Targets
	if len(values) == 0 && strings.TrimSpace(resolved.Target) != "" {
		values = []string{resolved.Target}
	}
	return parseAllowlist(values)
}

// parseAllowlist reads the targets of one connection in any mix of projects, repositories, owners, and
// patterns.
func parseAllowlist(values []string) (allowlist, error) {
	list := make(allowlist, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		parsed, err := parseTarget(value)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(parsed.String())
		if seen[key] {
			return nil, errors.New("the GitHub target list names a target more than once")
		}
		seen[key] = true
		list = append(list, parsed)
	}
	return list, nil
}

// allows reports whether the list admits one project, repository, or owner. GitHub compares owner and
// repository names without case.
func (a allowlist) allows(t target) bool {
	if len(a) == 0 {
		return true
	}
	for _, entry := range a {
		if entry.kind != t.kind || !strings.EqualFold(entry.owner, t.owner) {
			continue
		}
		if t.kind == kindRepository && (entry.repo == "*" || strings.EqualFold(entry.repo, t.repo)) {
			return true
		}
		if t.kind == kindProject && entry.scope == t.scope && (entry.number == 0 || entry.number == t.number) {
			return true
		}
		if t.kind == kindOwner && entry.scope == t.scope {
			return true
		}
	}
	return false
}

// lists reports whether an owner tool may list the projects or the repositories of one owner: the list is
// empty or names the owner, or it names a project or repository of that owner, or all of them. A repository
// target carries no users or orgs, so its login alone decides.
func (a allowlist) lists(owner target, kind targetKind) bool {
	if a.allows(owner) {
		return true
	}
	for _, entry := range a {
		if entry.kind == kind && strings.EqualFold(entry.owner, owner.owner) &&
			(kind == kindRepository || entry.scope == owner.scope) {
			return true
		}
	}
	return false
}

// shows reports whether an owner list may show one of its projects or repositories: every one when the list
// allows the owner as a whole, otherwise only those it allows itself.
func (a allowlist) shows(owner, listed target) bool {
	return a.allows(owner) || a.allows(listed)
}

// only returns the one target of a kind the list allows: it names exactly one of that kind, and no pattern.
func (a allowlist) only(kind targetKind) (target, bool) {
	var found []target
	for _, entry := range a {
		if entry.kind == kind {
			found = append(found, entry)
		}
	}
	if len(found) == 1 && !found[0].pattern() {
		return found[0], true
	}
	return target{}, false
}

func (a allowlist) names(kind targetKind) bool {
	for _, entry := range a {
		if entry.kind == kind {
			return true
		}
	}
	return false
}

// chooseOwner returns the owner whose projects or repositories an owner tool lists. An explicit argument
// wins; without one, only an owner the list names exactly, and no other, is the default. The owner must be
// one the list lets the tool list. Every refusal is an invalid request that never quotes the value.
func (a allowlist) chooseOwner(kind targetKind, value string) (target, error) {
	const form = "users/LOGIN or orgs/LOGIN"
	var chosen target
	if value != "" {
		parsed, err := parseTarget(value)
		if err != nil || parsed.kind != kindOwner || strings.TrimSpace(value) != value {
			return target{}, invalidRequest("owner must be " + form)
		}
		chosen = parsed
		for _, entry := range a {
			if entry.kind == kindOwner && strings.EqualFold(entry.String(), chosen.String()) {
				chosen = entry
			}
		}
	} else if only, ok := a.only(kindOwner); ok {
		chosen = only
	} else {
		return target{}, invalidRequest("owner is required because the connection's targets do not name exactly " +
			"one; pass owner as " + form)
	}
	if !a.lists(chosen, kind) {
		name := "repositories"
		if kind == kindProject {
			name = "projects"
		}
		return target{}, invalidRequest("owner is outside the targets of this connection for its " + name +
			"; pass one they allow, or add the owner or one of its " + name + " to the connection's targets")
	}
	return chosen, nil
}

// choose returns the target a tool acts on. An explicit argument wins and must lie inside the list. Without
// one, the only target of its kind the list allows is the default; nothing else, such as the working
// directory, ever becomes one. A default never widens or narrows the list. Every refusal is an invalid
// request that names the next step and never quotes the value.
func (a allowlist) choose(kind targetKind, value string) (target, error) {
	name, form := "repository", "OWNER/REPO"
	if kind == kindProject {
		name, form = "project", "users/LOGIN/projects/NUMBER or orgs/LOGIN/projects/NUMBER"
	}
	if value != "" {
		chosen, err := parseArgument(kind, value)
		if err != nil {
			return target{}, invalidRequest(name + " must be " + form)
		}
		if !a.allows(chosen) {
			return target{}, invalidRequest(name + " is outside the targets of this connection; pass one they " +
				"allow, or add it to the connection's targets")
		}
		// A target the list names exactly keeps its configured spelling, and with it its cursors.
		for _, entry := range a {
			if !entry.pattern() && strings.EqualFold(entry.String(), chosen.String()) {
				return entry, nil
			}
		}
		return chosen, nil
	}
	if only, ok := a.only(kind); ok {
		return only, nil
	}
	if len(a) > 0 && !a.names(kind) {
		return target{}, invalidRequest("the targets of this connection allow no " + name + "; add one to its " +
			"targets or use another connection")
	}
	return target{}, invalidRequest(name + " is required because the connection's targets do not name exactly " +
		"one; pass " + name + " as " + form)
}

// parseArgument reads a repository argument as OWNER/REPO or a project argument as
// users/LOGIN/projects/NUMBER or orgs/LOGIN/projects/NUMBER. A pattern is never an argument.
func parseArgument(kind targetKind, value string) (target, error) {
	raw := value
	if kind == kindRepository {
		raw = "repos/" + value
	}
	parsed, err := parseTarget(raw)
	if err == nil && (parsed.kind != kind || parsed.pattern() || strings.TrimSpace(value) != value) {
		err = errors.New("not a single target of this kind")
	}
	return parsed, err
}

// selectOwner reads the owner argument of an owner tool that lists targets of one kind and resolves the owner
// against the connection's targets, before a credential is resolved.
func selectOwner(resolved *config.Resolved, kind targetKind, raw json.RawMessage) (target, error) {
	if resolved == nil {
		return target{}, providerError("open", "no connection was selected")
	}
	allowed, err := allowlistOf(resolved)
	if err != nil {
		return target{}, providerError("open", err.Error())
	}
	var arguments struct {
		Owner string `json:"owner"`
	}
	if json.Unmarshal(raw, &arguments) != nil {
		return target{}, unreadable("select owner")
	}
	return allowed.chooseOwner(kind, arguments.Owner)
}

// selectTarget reads the repository or project argument of a tool and resolves the target it acts on
// against the connection's targets. It runs before a credential is resolved, so a refused target never
// becomes a secret read or a provider call.
func selectTarget(resolved *config.Resolved, kind targetKind, raw json.RawMessage) (target, error) {
	if resolved == nil {
		return target{}, providerError("open", "no connection was selected")
	}
	allowed, err := allowlistOf(resolved)
	if err != nil {
		return target{}, providerError("open", err.Error())
	}
	var arguments struct {
		Repository string `json:"repository"`
		Project    string `json:"project"`
	}
	if json.Unmarshal(raw, &arguments) != nil {
		return target{}, unreadable("select target")
	}
	value := arguments.Repository
	if kind == kindProject {
		value = arguments.Project
	}
	return allowed.choose(kind, value)
}
