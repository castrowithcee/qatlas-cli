package forge

import (
	"errors"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// Input patterns of the target arguments. Users and organizations share one namespace of names of at most 40
// characters; a repository name has at most 100. The checks below mirror them and also refuse "." and "..".
const (
	ownerSchema    = `{"type":"string","maxLength":46,"pattern":"^(users|orgs)/[A-Za-z0-9_][A-Za-z0-9_.-]{0,39}$"}`
	repoSchema     = `{"type":"string","maxLength":141,"pattern":"^[A-Za-z0-9_][A-Za-z0-9_.-]{0,39}/[A-Za-z0-9_.-]{1,100}$"}`
	maxTargets     = 200
	maxNameLength  = 40
	maxRepoLength  = 100
	kindOwner      = "owner"
	kindRepository = "repository"
)

// target is one owner (scope users or orgs) or one repository of an owner.
type target struct {
	kind  string
	scope string
	owner string
	repo  string
}

func (t target) String() string {
	if t.kind == kindRepository {
		return "repos/" + t.owner + "/" + t.repo
	}
	return t.scope + "/" + t.owner
}

func validName(value string) bool {
	if value == "" || len(value) > maxNameLength || value == "." || value == ".." {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		alnum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
		if !alnum && (i == 0 || (c != '.' && c != '-')) {
			return false
		}
	}
	return true
}

func validRepoName(value string) bool {
	if value == "" || len(value) > maxRepoLength || value == "." || value == ".." {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '.' ||
			c == '-') {
			return false
		}
	}
	return true
}

// parseTarget reads users/NAME, orgs/NAME, or repos/OWNER/REPO. The error never quotes the value.
func parseTarget(raw string) (target, error) {
	parts := strings.Split(strings.TrimSpace(raw), "/")
	switch {
	case len(parts) == 2 && (parts[0] == "users" || parts[0] == "orgs") && validName(parts[1]):
		return target{kind: kindOwner, scope: parts[0], owner: parts[1]}, nil
	case len(parts) == 3 && parts[0] == "repos" && validName(parts[1]) && validRepoName(parts[2]):
		return target{kind: kindRepository, owner: parts[1], repo: parts[2]}, nil
	}
	return target{}, errors.New("a Forgejo or Gitea target must be users/NAME, orgs/NAME, or repos/OWNER/REPO")
}

// allowlist is the targets of one connection, plus the entries it excludes. Without targets, it allows whatever
// the token reaches; otherwise the owners it names with everything they own, and the repositories it names.
// An exclusion takes an owner with everything it owns, or one repository, out of that; it wins over every
// target. Names compare without case.
type allowlist struct {
	targets  []target
	excludes []target
}

// bound reports whether the list names targets.
func (a allowlist) bound() bool { return len(a.targets) > 0 }

// open reports whether nothing restricts the token.
func (a allowlist) open() bool { return len(a.targets) == 0 && len(a.excludes) == 0 }

func allowlistOf(resolved *config.Resolved) (allowlist, error) {
	list, err := parseAllowlist(provider.TargetsOf(resolved), provider.ExcludesOf(resolved))
	if err != nil {
		return allowlist{}, provider.Fail("open", err.Error())
	}
	return list, nil
}

// targetKey names an entry for the comparison of duplicates: users/NAME and orgs/NAME name the same owner,
// since users and organizations share their names.
func targetKey(t target) string { return strings.ToLower(t.kind + "/" + t.owner + "/" + t.repo) }

func parseAllowlist(values, excludes []string) (allowlist, error) {
	if len(values) > maxTargets || len(excludes) > maxTargets {
		return allowlist{}, errors.New("the Forgejo or Gitea target list names too many targets")
	}
	list := allowlist{targets: make([]target, 0, len(values))}
	seen := map[string]bool{}
	for _, value := range values {
		parsed, err := parseTarget(value)
		if err != nil {
			return allowlist{}, err
		}
		key := targetKey(parsed)
		if seen[key] {
			return allowlist{}, errors.New("the Forgejo or Gitea target list names a target more than once")
		}
		seen[key] = true
		list.targets = append(list.targets, parsed)
	}
	named := map[string]bool{}
	for _, value := range excludes {
		parsed, err := parseTarget(value)
		if err != nil {
			return allowlist{}, errors.New("a Forgejo or Gitea exclusion must be users/NAME, orgs/NAME, or " +
				"repos/OWNER/REPO, without a wildcard")
		}
		key := targetKey(parsed)
		if named[key] || seen[key] {
			return allowlist{}, errors.New("a Forgejo or Gitea exclusion must be named once and may not be a target")
		}
		named[key] = true
		list.excludes = append(list.excludes, parsed)
	}
	return list, nil
}

// excluded reports whether an exclusion takes the repository OWNER/REPO out: its owner or the repository itself.
func (a allowlist) excluded(owner, repo string) bool {
	for _, entry := range a.excludes {
		if strings.EqualFold(entry.owner, owner) && (entry.kind == kindOwner || strings.EqualFold(entry.repo, repo)) {
			return true
		}
	}
	return false
}

// excludedOwner reports whether an exclusion takes the owner out as a whole.
func (a allowlist) excludedOwner(name string) bool {
	for _, entry := range a.excludes {
		if entry.kind == kindOwner && strings.EqualFold(entry.owner, name) {
			return true
		}
	}
	return false
}

// keepsRepositoryRow reports whether a row of an instance-wide list may be shown by the canonical OWNER/REPO it
// names: without exclusions every row; otherwise only one whose repository is known and not excluded.
func (a allowlist) keepsRepositoryRow(fullName string, named bool) bool {
	return len(a.excludes) == 0 || (named && !a.excludedFullName(fullName))
}

// allowsRepository reports whether the list admits the repository OWNER/REPO.
func (a allowlist) allowsRepository(owner, repo string) bool {
	if a.excluded(owner, repo) {
		return false
	}
	if !a.bound() {
		return true
	}
	for _, entry := range a.targets {
		if !strings.EqualFold(entry.owner, owner) {
			continue
		}
		if entry.kind == kindOwner || strings.EqualFold(entry.repo, repo) {
			return true
		}
	}
	return false
}

// allowsOwner reports whether the list admits an owner as a whole. An owner with an excluded repository is
// still admitted; its rows are filtered one by one.
func (a allowlist) allowsOwner(name string) bool {
	if a.excludedOwner(name) {
		return false
	}
	if !a.bound() {
		return true
	}
	for _, entry := range a.targets {
		if entry.kind == kindOwner && strings.EqualFold(entry.owner, name) {
			return true
		}
	}
	return false
}

// listsOwner reports whether a list of an owner's repositories may be read: the owner is allowed, or one of
// its repositories is. The rows are filtered either way.
func (a allowlist) listsOwner(name string) bool {
	if a.allowsOwner(name) {
		return true
	}
	if a.excludedOwner(name) {
		return false
	}
	for _, entry := range a.targets {
		if entry.kind == kindRepository && strings.EqualFold(entry.owner, name) {
			return true
		}
	}
	return false
}

func (a allowlist) namesRepository() bool {
	for _, entry := range a.targets {
		if entry.kind == kindRepository {
			return true
		}
	}
	return false
}

// onlyRepository is the default of a repository argument: the one repository the list names, when it names
// exactly one.
func (a allowlist) onlyRepository() (target, bool) {
	var found []target
	for _, entry := range a.targets {
		if entry.kind == kindRepository {
			found = append(found, entry)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return target{}, false
}

// allowsFullName checks a canonical OWNER/REPO from an answer.
func (a allowlist) allowsFullName(fullName string) bool {
	owner, repo, ok := strings.Cut(fullName, "/")
	if !ok || owner == "" || repo == "" {
		return false
	}
	return a.allowsRepository(owner, repo)
}

// parseOwnerArgument reads users/NAME or orgs/NAME.
func parseOwnerArgument(value string) (target, error) {
	parsed, err := parseTarget(value)
	if err != nil || parsed.kind != kindOwner || strings.TrimSpace(value) != value {
		return target{}, invalidArgument("owner must be users/NAME or orgs/NAME")
	}
	return parsed, nil
}

// chooseRepository returns the repository a tool acts on: the argument, which must lie inside the list, or
// the only repository the list names. Every refusal is an invalid request that never quotes the value.
func (a allowlist) chooseRepository(value string) (target, error) {
	if value == "" {
		if only, ok := a.onlyRepository(); ok {
			if a.excluded(only.owner, only.repo) {
				return target{}, unavailable("repository")
			}
			return only, nil
		}
		return target{}, invalidArgument("repository is required because the connection's targets do not name " +
			"exactly one; pass repository as OWNER/REPO")
	}
	owner, repo, ok := strings.Cut(value, "/")
	if !ok || !validName(owner) || !validRepoName(repo) {
		return target{}, invalidArgument("repository must be OWNER/REPO")
	}
	if a.excluded(owner, repo) {
		return target{}, unavailable("repository")
	}
	if !a.allowsRepository(owner, repo) {
		return target{}, outsideTargets("repository")
	}
	return target{kind: kindRepository, owner: owner, repo: repo}, nil
}

func outsideTargets(what string) error {
	return invalidArgument(what + " is outside the targets of this connection; pass one they allow, or add it " +
		"to the connection's targets")
}

// unavailable refuses an argument that an exclusion takes out. It names neither the object nor the exclusion,
// and reads like a refusal for a repository the token cannot see.
func unavailable(what string) error {
	return invalidArgument(what + " is not available on this connection; pass another one")
}

// outsideOwner is the refusal of an owner argument the list does not admit: one that an exclusion takes out is
// not available, any other lies outside the targets.
func (a allowlist) outsideOwner(what, name string) error {
	if a.excludedOwner(name) {
		return unavailable(what)
	}
	return outsideTargets(what)
}

// outsideAnswer is the refusal of an answer object (run, job, artifact) that names a repository the connection
// does not admit: an excluded one reads as not found, any other is an invalid response.
func (a allowlist) outsideAnswer(op string, p *product, fullName, object string) error {
	if owner, repo, ok := strings.Cut(fullName, "/"); ok && a.excluded(owner, repo) {
		return notShown(op, p)
	}
	return provider.InvalidResponse(op, p.name+" returned "+object+" of a repository outside the targets")
}

// outside is the refusal of an answer whose canonical repository the connection does not admit: one that an
// exclusion takes out reads as not found, as for a token without the right to read it; any other lies outside
// the targets.
func (a allowlist) outside(op string, p *product, fullName, what string) error {
	if owner, repo, ok := strings.Cut(fullName, "/"); ok && a.excluded(owner, repo) {
		return notShown(op, p)
	}
	return outsideTargets(what)
}

// accountWide refuses a read of the account behind the token as a whole on a connection whose targets name a
// repository: such a connection is scoped to it, and the account may belong to another customer than the
// repository. Owner targets, or none, allow it.
func (a allowlist) accountWide() error {
	if a.namesRepository() {
		return invalidArgument("this connection's targets name a repository, so it may not read the account " +
			"behind its token; use a connection without repository targets")
	}
	return nil
}

// reaches hides the account read from a connection whose targets name a repository.
func reaches(p *product) func(string, []string) (bool, []string) {
	return func(tool string, targets []string) (bool, []string) {
		list, err := parseAllowlist(targets, nil)
		if err != nil || tool != p.toolID(accountsMe) || !list.namesRepository() {
			return true, nil
		}
		return false, nil
	}
}
