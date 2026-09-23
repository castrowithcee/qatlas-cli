package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
)

// The target arguments. Every tool takes the repository or the project it acts on; a tool that adds an issue
// to a project takes both. Discovery says when an argument may be left out.
const (
	projectPattern = `^(users|orgs)/[A-Za-z0-9][A-Za-z0-9_-]{0,99}/projects/[1-9][0-9]{0,8}$`
	projectSchema  = `{"type":"string","maxLength":120,"pattern":"` + projectPattern + `"}`
)

var repositoryArgument = capability.Argument{Name: "repository", Description: "Repository as OWNER/REPO; " +
	"optional when the connection's targets allow exactly one repository, or when the GitHub remote of the " +
	"working directory (origin, or its only remote) lies inside them; must lie inside the targets when the " +
	"connection lists any"}

var projectArgument = capability.Argument{Name: "project", Description: "Project as users/LOGIN/projects/NUMBER " +
	"or orgs/LOGIN/projects/NUMBER; optional when the connection's targets allow exactly one project; must lie " +
	"inside the targets when the connection lists any"}

// withTargetArgument adds the target argument of its kind to one tool: project to the project tools,
// repository to every other tool.
func withTargetArgument(d capability.Descriptor) capability.Descriptor {
	name, schema, argument := "repository", repoSchema, repositoryArgument
	if strings.HasPrefix(d.ID, Provider+".project") {
		name, schema, argument = "project", projectSchema, projectArgument
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
	return d
}

// allowlist is the targets of one connection. Empty, it allows every repository and project the token
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

// parseAllowlist reads the targets of one connection in any mix of projects, repositories, and patterns.
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

// allows reports whether the list admits one project or repository. GitHub compares owner and repository
// names without case.
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
	}
	return false
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

// choose returns the target a tool acts on. An explicit argument wins and must lie inside the list. Without
// one, the only target of its kind the list allows is the default and, for a repository, then the GitHub
// remote of the working directory on the service's host when the list allows it. A default never widens or
// narrows the list. Every refusal is an invalid request that names the next step and never quotes the value.
func (a allowlist) choose(ctx context.Context, kind targetKind, value, host string) (target, error) {
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
	if kind == kindRepository {
		if remote, ok := workingRepository(ctx, host); ok && a.allows(remote) {
			return remote, nil
		}
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

// selectTarget reads the repository or project argument of a tool and resolves the target it acts on
// against the connection's targets. It runs before a credential is resolved, so a refused target never
// becomes a secret read or a provider call.
func selectTarget(ctx context.Context, resolved *config.Resolved, kind targetKind, raw json.RawMessage) (target,
	error) {
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
	return allowed.choose(ctx, kind, value, webHost(resolved))
}

// webHost returns the host the repositories of the configured service are cloned from, or nothing for an
// unusable service, which then fails on its own.
func webHost(resolved *config.Resolved) string {
	base := resolved.BaseURL
	if strings.TrimSpace(base) == "" {
		base = defaultBaseURL
	}
	api, err := endpointsOf(base)
	if err != nil {
		return ""
	}
	return api.web
}

// remoteTimeout bounds the local git call that finds the remote of the working directory.
const remoteTimeout = 5 * time.Second

// workingRemotes returns the remote URLs of the git repository around the working directory of this
// process, by remote name. It only reads local git configuration; the package's own tests replace it.
var workingRemotes = func(ctx context.Context) map[string][]string {
	ctx, cancel := context.WithTimeout(ctx, remoteTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "git", "config", "--get-regexp", `^remote\..+\.url$`).Output()
	if err != nil {
		return nil
	}
	remotes := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		key, value, ok := strings.Cut(line, " ")
		name, isURL := strings.CutSuffix(strings.TrimPrefix(key, "remote."), ".url")
		if ok && isURL {
			remotes[name] = append(remotes[name], value)
		}
	}
	return remotes
}

// workingRepository returns the repository of the GitHub remote of the working directory: origin, or the only
// remote when there is no origin. The remote must live on host, the web host of the configured service, so
// a remote of another GitHub instance never becomes a target. Nothing of the URL is ever reported.
func workingRepository(ctx context.Context, host string) (target, bool) {
	if host == "" {
		return target{}, false
	}
	remotes := workingRemotes(ctx)
	urls, ok := remotes["origin"]
	if !ok && len(remotes) == 1 {
		for _, only := range remotes {
			urls = only
		}
	}
	if len(urls) != 1 {
		return target{}, false
	}
	remoteHost, path, ok := splitRemote(urls[0])
	if !ok || !strings.EqualFold(remoteHost, host) {
		return target{}, false
	}
	repository, err := parseArgument(kindRepository, strings.TrimSuffix(strings.Trim(path, "/"), ".git"))
	return repository, err == nil
}

// splitRemote reads the host and the path of a git remote URL: scheme://[user@]host[:port]/path or the scp
// form [user@]host:path.
func splitRemote(raw string) (string, string, bool) {
	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", "", false
		}
		return parsed.Hostname(), parsed.Path, true
	}
	head, path, ok := strings.Cut(raw, ":")
	if !ok || strings.Contains(head, "/") {
		return "", "", false
	}
	if _, host, found := strings.Cut(head, "@"); found {
		head = host
	}
	return head, path, true
}
