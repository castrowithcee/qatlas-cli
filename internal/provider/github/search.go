package github

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The search tools read GitHub's own search index: repositories, code, issues, pull requests, commits,
// users, and organizations. They take search terms and GitHub qualifiers as one free argument, terms, never
// a query document or a route of their own, and they change nothing.
//
// A connection whose targets name a repository, an owner, or a project never reaches beyond them: before a
// credential is resolved, github.repositories.search, github.code.search, github.issues.search,
// github.pullrequests.search, and github.commits.search force the repo: and org:/user: qualifiers the
// targets allow ahead of the caller's terms, GitHub ANDs a forced qualifier with the terms and, repeated,
// unions several forced qualifiers of the same name, so several targets narrow the search to their combined
// reach and never beyond it; a repository pattern (repos/OWNER/*) is refused instead, because it carries no
// record of whether its owner is a user or an organization to force org: or user: with. Because a repeated
// repo:, org:, user:, or owner: qualifier from the caller could union the search back open, and OR and
// parentheses could rearrange which qualifiers bind to which terms, terms may not carry them while a target
// forces a qualifier of its own; a negated qualifier, such as -repo:, only narrows further and stays
// allowed. github.users.search and github.organizations.search reach every account GitHub's index holds
// unless a connection's targets narrow them: github.users.search accepts a connection without targets or
// one naming exactly one organization as an owner target, whose members org: then bounds the search to,
// since no other target gives it an account to narrow by; github.organizations.search accepts only a
// connection without targets, since no qualifier restricts which organizations a search may return to one
// target the way org: does for users.
//
// Every batch reports GitHub's total_count and incomplete_results alongside its compact, untrusted results,
// and pages by the same cursor contract as the other lists, bound to the tool, the forced qualifiers, and
// the exact terms.

// maxSearchTermsLength bounds the terms argument; GitHub's own query length limit is higher, but a search
// this provider forces a qualifier ahead of stays well inside it.
const maxSearchTermsLength = 256

const termsPattern = `^[^\\x00-\\x1f\\x7f]+$`

const termsSchema = `{"type":"string","minLength":1,"maxLength":256,"pattern":"` + termsPattern + `"}`

// validSearchTerms accepts any printable text without leading or trailing padding: GitHub's search grammar
// uses quotes, colons, commas, parentheses, and asterisks itself, so only control characters are refused
// here; checkForeignQualifiers refuses the substrings that could escape a forced qualifier.
func validSearchTerms(value string) bool {
	if value == "" || len([]rune(value)) > maxSearchTermsLength || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// foreignScopeQualifiers are the qualifier names that pick a search's owner or repository, the ones a target
// already forces; a positive one from the caller could union the search back open, since GitHub treats a
// repeated qualifier of the same name as their union.
var foreignScopeQualifiers = []string{"repo:", "org:", "user:", "owner:"}

// checkForeignQualifiers refuses a term list that could widen or rearrange a forced qualifier: a positive
// repo:, org:, user:, or owner: qualifier, since GitHub would union it with the one a target forces; the OR
// operator and parentheses, since either could change which terms a forced qualifier binds to. It runs only
// while a target forces a qualifier of its own; a connection without targets has no boundary to protect, and
// -repo: and its siblings stay allowed everywhere, since a negation only narrows further.
func checkForeignQualifiers(terms string, scoped bool) error {
	if !scoped {
		return nil
	}
	if strings.ContainsAny(terms, "()") {
		return invalidRequest("terms may not use parentheses while this connection's targets narrow the search")
	}
	for _, token := range strings.Fields(terms) {
		if token == "OR" {
			return invalidRequest("terms may not use the OR operator while this connection's targets narrow the search")
		}
		if strings.HasPrefix(token, "-") {
			continue
		}
		lower := strings.ToLower(token)
		for _, qualifier := range foreignScopeQualifiers {
			if strings.HasPrefix(lower, qualifier) {
				return invalidRequest("terms may not repeat " + qualifier + " while this connection's targets " +
					"already narrow the search by it; remove it, or use a connection whose targets allow the " +
					"wider search")
			}
		}
	}
	return nil
}

// searchScope builds the repo: and org:/user: qualifiers github.repositories.search, github.code.search,
// github.issues.search, github.pullrequests.search, and github.commits.search force from the connection's
// targets, so none of them ever reaches beyond what the targets allow. Every repository target forces its
// own repo:OWNER/REPO, and every owner target forces org:LOGIN or user:LOGIN of its own owner and scope, once
// per distinct owner. A repository pattern (repos/OWNER/*) carries no record of whether OWNER is a user or
// an organization, so it cannot be forced into org: or user: and is refused rather than guessed. A project
// target forces nothing: GitHub's search grammar has no qualifier for a project, and its owner is not a
// target of its own, exactly as accountWideAllowed and chooseOwner already treat a project pattern as not
// enough to stand for its owner; a search that a project target alone could not narrow is refused instead of
// widened to that owner's whole reach. Because of that, a non-empty allowlist that forces no qualifier at
// all, for example one naming only a project, is refused as well, so a targeted connection never sends an
// unscoped search.
func searchScope(allowed allowlist) (string, error) {
	if len(allowed) == 0 {
		return "", nil
	}
	var parts []string
	seenOwner := map[string]bool{}
	for _, entry := range allowed {
		switch entry.kind {
		case kindRepository:
			if entry.pattern() {
				return "", invalidRequest("search cannot be scoped to repos/" + entry.owner + "/*: it does not " +
					"record whether " + entry.owner + " is a user or an organization; add it as users/LOGIN or " +
					"orgs/LOGIN to the connection's targets, or list its repositories individually, to search " +
					"within it")
			}
			parts = append(parts, "repo:"+entry.owner+"/"+entry.repo)
		case kindOwner:
			key := entry.scope + "/" + strings.ToLower(entry.owner)
			if seenOwner[key] {
				continue
			}
			seenOwner[key] = true
			qualifier := "user:"
			if entry.scope == "orgs" {
				qualifier = "org:"
			}
			parts = append(parts, qualifier+entry.owner)
		}
	}
	if len(parts) == 0 {
		return "", invalidRequest("this connection's targets do not narrow a search: a project target alone " +
			"names no repository or owner qualifier a search could force; add a repository or an owner target, " +
			"or use a connection without targets")
	}
	return strings.Join(parts, " "), nil
}

// issuesSearchScope adds the fixed is:issue qualifier ahead of searchScope's target-derived qualifiers, so
// github.issues.search never answers with pull requests, whatever a connection's targets allow.
func issuesSearchScope(allowed allowlist) (string, error) {
	scope, err := searchScope(allowed)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace("is:issue " + scope), nil
}

// pullRequestsSearchScope adds the fixed is:pr qualifier ahead of searchScope's target-derived qualifiers,
// so github.pullrequests.search never answers with plain issues, whatever a connection's targets allow.
func pullRequestsSearchScope(allowed allowlist) (string, error) {
	scope, err := searchScope(allowed)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace("is:pr " + scope), nil
}

// userSearchScope forces type:user, so github.users.search never answers with organizations, and, when the
// connection's targets name exactly one organization as an owner target, org:LOGIN as well, the only GitHub
// qualifier that narrows a user search by ownership. Any other target combination, including a personal
// owner target, gives it nothing to narrow a user search by and is refused.
func userSearchScope(allowed allowlist) (string, error) {
	if len(allowed) == 0 {
		return "type:user", nil
	}
	owner, ok := allowed.only(kindOwner)
	if !ok || owner.scope != "orgs" {
		return "", invalidRequest("this connection's targets do not let github.users.search narrow the search: " +
			"it needs no targets, or exactly one target naming an organization as orgs/LOGIN, whose members " +
			"org: then narrows the search to; a personal owner, a repository, or a project target gives it " +
			"nothing to narrow a user search by")
	}
	return "type:user org:" + owner.owner, nil
}

// organizationSearchScope forces type:org. github.organizations.search is offered only to a connection
// without targets, because no GitHub qualifier restricts which organizations a search may return to one
// target the way org: restricts github.users.search to the members of one; a connection whose targets
// narrow every other search tool could not have that narrowing enforced here, so it is refused instead of
// left unscoped.
func organizationSearchScope(allowed allowlist) (string, error) {
	if len(allowed) > 0 {
		return "", invalidRequest("this connection's targets narrow other tools, but no GitHub qualifier " +
			"restricts which organizations a search may return to a target, so github.organizations.search is " +
			"offered only by a connection without targets")
	}
	return "type:org", nil
}

var searchTermsArgument = capability.Argument{Name: "terms", Required: true, Description: "Search terms and " +
	"GitHub qualifiers, such as language:go or is:merged; a connection whose targets narrow the search " +
	"refuses a repo:, org:, user:, or owner: qualifier here, since Qatlas already adds the one the targets " +
	"allow, and refuses the OR operator and parentheses, since either could widen or rearrange it; a negated " +
	"qualifier, such as -repo:, stays allowed"}

var searchCountFields = []capability.Field{
	{Name: "total_count", Description: "How many results GitHub counts in total, ahead of this batch's limit"},
	{Name: "incomplete_results", Description: "True when GitHub timed out scoring every result; the count and " +
		"this batch may be incomplete"},
}

// searchOutput is the schema of one paged, counted search batch.
func searchOutput(key, properties, required string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"` + key + `":{"type":"array","items":{"type":"object",` +
		`"properties":{` + properties + `},` + required + `}},"total_count":{"type":"integer"},` +
		`"incomplete_results":{"type":"boolean"},"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["` + key + `","total_count","incomplete_results","has_more"],"additionalProperties":false}`)
}

const repositorySearchProperties = `"repository":{"type":"string"},"description":{"type":"string"},` +
	`"visibility":{"type":"string"},"language":{"type":"string"},"stars":{"type":"integer"},` +
	`"fork":{"type":"boolean"},"archived":{"type":"boolean"},"updated_at":{"type":"string"},"url":{"type":"string"}`

const repositorySearchRequired = `"required":["repository","visibility","stars","fork","archived"],"additionalProperties":false`

var repositoriesSearch = capability.Descriptor{
	ID:      Provider + ".repositories.search",
	Version: 1,
	Title:   "Search GitHub repositories",
	Description: "Search repositories GitHub's index covers with terms and qualifiers, narrowed to the " +
		"repositories and the owners a connection's targets allow when it names any",
	Tags:         []string{"github", "repositories", "search", "discovery"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(`"terms":`+termsSchema+`,`+pagingKeys, "terms"),
	OutputSchema: searchOutput("repositories", repositorySearchProperties, repositorySearchRequired),
	Arguments:    append([]capability.Argument{searchTermsArgument}, pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "repositories", Description: "Compact repositories: repository as OWNER/REPO, description " +
			"(untrusted data), visibility, language, stars, fork, archived, and updated_at"},
	}, searchCountFields...), pagingFields...),
	Examples: []capability.Example{{
		Description: "Search Go repositories about a topic",
		Arguments:   json.RawMessage(`{"terms":"qatlas language:go","limit":10}`),
	}},
}

const codeSearchProperties = `"path":{"type":"string"},"repository":{"type":"string"},"sha":{"type":"string"},` +
	`"url":{"type":"string"}`

const codeSearchRequired = `"required":["path","repository"],"additionalProperties":false`

var codeSearch = capability.Descriptor{
	ID:      Provider + ".code.search",
	Version: 1,
	Title:   "Search GitHub code",
	Description: "Search code GitHub's index covers with terms and qualifiers, narrowed to the repositories " +
		"and the owners a connection's targets allow when it names any, without file bodies",
	Tags:         []string{"github", "code", "search", "discovery"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(`"terms":`+termsSchema+`,`+pagingKeys, "terms"),
	OutputSchema: searchOutput("files", codeSearchProperties, codeSearchRequired),
	Arguments:    append([]capability.Argument{searchTermsArgument}, pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "files", Description: "Compact code matches: path, repository as OWNER/REPO, sha, and url; " +
			"never a file body or a fragment"},
	}, searchCountFields...), pagingFields...),
	Examples: []capability.Example{{
		Description: "Search for a symbol inside one repository",
		Arguments:   json.RawMessage(`{"terms":"handleRequest language:go","limit":10}`),
	}},
}

const issueSearchProperties = `"number":{"type":"integer"},"title":{"type":"string"},"repository":{"type":"string"},` +
	`"state":{"type":"string"},"draft":{"type":"boolean"},"labels":` + stringListSchema + `,"assignees":` +
	stringListSchema + `,"updated_at":{"type":"string"},"url":{"type":"string"}`

const issueSearchRequired = `"required":["number","title","repository","state","labels","assignees"],"additionalProperties":false`

var issuesSearch = capability.Descriptor{
	ID:      Provider + ".issues.search",
	Version: 1,
	Title:   "Search GitHub issues",
	Description: "Search issues, never pull requests, GitHub's index covers with terms and qualifiers, " +
		"narrowed to the repositories and the owners a connection's targets allow when it names any, without bodies",
	Tags:         []string{"github", "issues", "search", "discovery"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(`"terms":`+termsSchema+`,`+pagingKeys, "terms"),
	OutputSchema: searchOutput("issues", issueSearchProperties, issueSearchRequired),
	Arguments:    append([]capability.Argument{searchTermsArgument}, pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "issues", Description: "Compact issues across repositories: number, title (untrusted data), " +
			"repository as OWNER/REPO, state, labels, assignees, and updated_at"},
	}, searchCountFields...), pagingFields...),
	Examples: []capability.Example{{
		Description: "Search open issues labeled bug",
		Arguments:   json.RawMessage(`{"terms":"is:open label:bug","limit":10}`),
	}},
}

var pullRequestsSearch = capability.Descriptor{
	ID:      Provider + ".pullrequests.search",
	Version: 1,
	Title:   "Search GitHub pull requests",
	Description: "Search pull requests, never plain issues, GitHub's index covers with terms and qualifiers, " +
		"narrowed to the repositories and the owners a connection's targets allow when it names any, without bodies",
	Tags:         []string{"github", "pulls", "pullrequests", "search", "discovery"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(`"terms":`+termsSchema+`,`+pagingKeys, "terms"),
	OutputSchema: searchOutput("pull_requests", issueSearchProperties, issueSearchRequired),
	Arguments:    append([]capability.Argument{searchTermsArgument}, pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "pull_requests", Description: "Compact pull requests across repositories: number, title " +
			"(untrusted data), repository as OWNER/REPO, state, draft, labels, assignees, and updated_at"},
	}, searchCountFields...), pagingFields...),
	Examples: []capability.Example{{
		Description: "Search merged pull requests based on main",
		Arguments:   json.RawMessage(`{"terms":"is:merged base:main","limit":10}`),
	}},
}

const commitSearchProperties = `"sha":{"type":"string"},"message":{"type":"string"},"repository":{"type":"string"},` +
	`"author":{"type":"string"},"committed_at":{"type":"string"},"url":{"type":"string"}`

const commitSearchRequired = `"required":["sha","repository"],"additionalProperties":false`

var commitsSearch = capability.Descriptor{
	ID:      Provider + ".commits.search",
	Version: 1,
	Title:   "Search GitHub commits",
	Description: "Search commits GitHub's index covers with terms and qualifiers, narrowed to the " +
		"repositories and the owners a connection's targets allow when it names any",
	Tags:         []string{"github", "commits", "search", "discovery"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(`"terms":`+termsSchema+`,`+pagingKeys, "terms"),
	OutputSchema: searchOutput("commits", commitSearchProperties, commitSearchRequired),
	Arguments:    append([]capability.Argument{searchTermsArgument}, pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "commits", Description: "Compact commits across repositories: sha, message (untrusted data), " +
			"repository as OWNER/REPO, author, and committed_at"},
	}, searchCountFields...), pagingFields...),
	Examples: []capability.Example{{
		Description: "Search commits by an author since a date",
		Arguments:   json.RawMessage(`{"terms":"author:octocat committer-date:>2026-01-01","limit":10}`),
	}},
}

const userSearchProperties = `"login":{"type":"string"},"type":{"type":"string"},"url":{"type":"string"}`

const userSearchRequired = `"required":["login","type"],"additionalProperties":false`

var usersSearch = capability.Descriptor{
	ID:      Provider + ".users.search",
	Version: 1,
	Title:   "Search GitHub users",
	Description: "Search personal GitHub accounts, never organizations, with terms and qualifiers; a " +
		"connection whose targets name an organization narrows the search to its members, and one whose " +
		"targets name anything else refuses it",
	Tags:         []string{"github", "users", "search", "discovery"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(`"terms":`+termsSchema+`,`+pagingKeys, "terms"),
	OutputSchema: searchOutput("users", userSearchProperties, userSearchRequired),
	Arguments:    append([]capability.Argument{searchTermsArgument}, pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "users", Description: "Compact accounts: login, type (always User), and url"},
	}, searchCountFields...), pagingFields...),
	Examples: []capability.Example{{
		Description: "Search users by location",
		Arguments:   json.RawMessage(`{"terms":"location:berlin","limit":10}`),
	}},
}

const organizationSearchProperties = `"login":{"type":"string"},"url":{"type":"string"}`

const organizationSearchRequired = `"required":["login"],"additionalProperties":false`

var organizationsSearch = capability.Descriptor{
	ID:      Provider + ".organizations.search",
	Version: 1,
	Title:   "Search GitHub organizations",
	Description: "Search organizations, never personal accounts, with terms and qualifiers; offered only by " +
		"a connection without targets, since no qualifier restricts which organizations a search may return " +
		"to one target",
	Tags:         []string{"github", "organizations", "search", "discovery"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(`"terms":`+termsSchema+`,`+pagingKeys, "terms"),
	OutputSchema: searchOutput("organizations", organizationSearchProperties, organizationSearchRequired),
	Arguments:    append([]capability.Argument{searchTermsArgument}, pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "organizations", Description: "Compact organizations: login and url"},
	}, searchCountFields...), pagingFields...),
	Examples: []capability.Example{{
		Description: "Search organizations by name",
		Arguments:   json.RawMessage(`{"terms":"octo in:name","limit":10}`),
	}},
}

// searchArguments are the terms and the paging of one search request. q is the exact GitHub query sent: the
// forced qualifiers ahead of the caller's terms. The cursor is bound to the tool, the forced qualifiers, and
// the exact terms, so a continuation never silently searches something else.
type searchArguments struct {
	Terms  string `json:"terms"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor"`

	q             string
	page, perPage int
	binding       []byte
}

func (a *searchArguments) query() url.Values {
	return url.Values{"q": {a.q}, "per_page": {strconv.Itoa(a.perPage)}, "page": {strconv.Itoa(a.page)}}
}

// checkSearchArguments validates the terms, refuses the ones that could escape a forced qualifier while
// restricted names an active one, and binds the paging to the tool, the forced qualifiers, and the exact
// terms.
func checkSearchArguments(tool, forced string, restricted bool, a *searchArguments) error {
	if !validSearchTerms(a.Terms) {
		return invalidRequest("terms must be 1 to " + strconv.Itoa(maxSearchTermsLength) + " printable characters " +
			"without leading or trailing whitespace")
	}
	if err := checkForeignQualifiers(a.Terms, restricted); err != nil {
		return err
	}
	limit, err := normalizeLimit(a.Limit)
	if err != nil {
		return err
	}
	a.q = strings.TrimSpace(forced + " " + a.Terms)
	a.binding = fingerprint("search", tool, forced, a.Terms)
	a.page, a.perPage, err = pageOf(a.binding, a.Cursor, limit)
	return err
}

// searchHandler decodes the terms, builds the forced qualifiers scopeOf derives from the connection's
// targets, and refuses a request outside them, or one that could escape them, before a credential is
// resolved, so a refused search never becomes a secret read or a request to GitHub.
func searchHandler(tool string, scopeOf func(allowlist) (string, error),
	call func(context.Context, *Client, *searchArguments) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments searchArguments
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, unreadable(tool)
		}
		if resolved == nil {
			return nil, providerError("open", "no connection was selected")
		}
		allowed, err := allowlistOf(resolved)
		if err != nil {
			return nil, providerError("open", err.Error())
		}
		forced, err := scopeOf(allowed)
		if err != nil {
			return nil, err
		}
		if err := checkSearchArguments(tool, forced, len(allowed) > 0, &arguments); err != nil {
			return nil, err
		}
		client, err := Open(ctx, resolved, secrets, red)
		if err != nil {
			return nil, err
		}
		return call(ctx, client, &arguments)
	}
}

func searchOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: repositoriesSearch, Handler: searchHandler(repositoriesSearch.ID, searchScope,
			func(ctx context.Context, c *Client, a *searchArguments) (any, error) {
				return c.searchRepositories(ctx, a)
			})},
		{Descriptor: codeSearch, Handler: searchHandler(codeSearch.ID, searchScope,
			func(ctx context.Context, c *Client, a *searchArguments) (any, error) { return c.searchCode(ctx, a) })},
		{Descriptor: issuesSearch, Handler: searchHandler(issuesSearch.ID, issuesSearchScope,
			func(ctx context.Context, c *Client, a *searchArguments) (any, error) { return c.searchIssues(ctx, a) })},
		{Descriptor: pullRequestsSearch, Handler: searchHandler(pullRequestsSearch.ID, pullRequestsSearchScope,
			func(ctx context.Context, c *Client, a *searchArguments) (any, error) {
				return c.searchPullRequests(ctx, a)
			})},
		{Descriptor: commitsSearch, Handler: searchHandler(commitsSearch.ID, searchScope,
			func(ctx context.Context, c *Client, a *searchArguments) (any, error) { return c.searchCommits(ctx, a) })},
		{Descriptor: usersSearch, Handler: searchHandler(usersSearch.ID, userSearchScope,
			func(ctx context.Context, c *Client, a *searchArguments) (any, error) { return c.searchUsers(ctx, a) })},
		{Descriptor: organizationsSearch, Handler: searchHandler(organizationsSearch.ID, organizationSearchScope,
			func(ctx context.Context, c *Client, a *searchArguments) (any, error) {
				return c.searchOrganizations(ctx, a)
			})},
	}
}

// Permission messages of the search tools. GitHub decides on every request; a message names what such a
// request needs without claiming what the configured token holds.
const (
	repositorySearchPermission = "GitHub refused this token repository search; it needs no scope for public " +
		"results, or repo on a classic token, or Contents: read on a fine-grained token, for private ones"
	codeSearchPermission = "GitHub refused this token code search; it needs no scope for public code, or repo " +
		"on a classic token, or Contents: read on a fine-grained token, for private repositories"
	issueSearchPermission = "GitHub refused this token issue search; it needs no scope for public results, or " +
		"repo or public_repo on a classic token, or Issues: read on a fine-grained token, for private ones"
	pullRequestSearchPermission = "GitHub refused this token pull request search; it needs no scope for " +
		"public results, or repo or public_repo on a classic token, or Pull requests: read on a fine-grained " +
		"token, for private ones"
	commitSearchPermission = "GitHub refused this token commit search; it needs no scope for public results, " +
		"or repo on a classic token, or Contents: read on a fine-grained token, for private ones"
	userSearchPermission         = "GitHub refused this token user search, which needs no scope beyond the token's own identity"
	organizationSearchPermission = "GitHub refused this token organization search, which needs no scope " +
		"beyond the token's own identity"
)

// RepositorySearchResult is the compact view of one repository search match.
type RepositorySearchResult struct {
	Repository  string `json:"repository"`
	Description string `json:"description,omitempty"`
	Visibility  string `json:"visibility"`
	Language    string `json:"language,omitempty"`
	Stars       int    `json:"stars"`
	Fork        bool   `json:"fork"`
	Archived    bool   `json:"archived"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	URL         string `json:"url,omitempty"`
}

// RepositorySearchList is one batch of a repository search.
type RepositorySearchList struct {
	Repositories      []RepositorySearchResult `json:"repositories"`
	TotalCount        int                      `json:"total_count"`
	IncompleteResults bool                     `json:"incomplete_results"`
	NextCursor        string                   `json:"next_cursor,omitempty"`
	HasMore           bool                     `json:"has_more"`
}

func (c *Client) searchRepositories(ctx context.Context, a *searchArguments) (*RepositorySearchList, error) {
	const op = "search repositories"
	var raw struct {
		TotalCount int  `json:"total_count"`
		Incomplete bool `json:"incomplete_results"`
		Items      []struct {
			FullName        string `json:"full_name"`
			Description     string `json:"description"`
			Visibility      string `json:"visibility"`
			Language        string `json:"language"`
			StargazersCount int    `json:"stargazers_count"`
			Fork            bool   `json:"fork"`
			Archived        bool   `json:"archived"`
			UpdatedAt       string `json:"updated_at"`
			HTMLURL         string `json:"html_url"`
		} `json:"items"`
	}
	hasNext, err := c.restPage(ctx, op, "/search/repositories", a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, repositorySearchPermission)
	}
	result := &RepositorySearchList{Repositories: make([]RepositorySearchResult, 0, len(raw.Items)),
		TotalCount: raw.TotalCount, IncompleteResults: raw.Incomplete}
	for _, item := range raw.Items {
		if !validRepository(item.FullName) {
			return nil, invalidEntry(op, "a repository")
		}
		result.Repositories = append(result.Repositories, RepositorySearchResult{Repository: item.FullName,
			Description: item.Description, Visibility: strings.ToLower(item.Visibility), Language: item.Language,
			Stars: item.StargazersCount, Fork: item.Fork, Archived: item.Archived, UpdatedAt: item.UpdatedAt,
			URL: item.HTMLURL})
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// CodeSearchResult is the compact view of one code search match, without a file body or a fragment.
type CodeSearchResult struct {
	Path       string `json:"path"`
	Repository string `json:"repository"`
	SHA        string `json:"sha,omitempty"`
	URL        string `json:"url,omitempty"`
}

// CodeSearchList is one batch of a code search.
type CodeSearchList struct {
	Files             []CodeSearchResult `json:"files"`
	TotalCount        int                `json:"total_count"`
	IncompleteResults bool               `json:"incomplete_results"`
	NextCursor        string             `json:"next_cursor,omitempty"`
	HasMore           bool               `json:"has_more"`
}

func (c *Client) searchCode(ctx context.Context, a *searchArguments) (*CodeSearchList, error) {
	const op = "search code"
	var raw struct {
		TotalCount int  `json:"total_count"`
		Incomplete bool `json:"incomplete_results"`
		Items      []struct {
			Path       string `json:"path"`
			SHA        string `json:"sha"`
			HTMLURL    string `json:"html_url"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		} `json:"items"`
	}
	hasNext, err := c.restPage(ctx, op, "/search/code", a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, codeSearchPermission)
	}
	result := &CodeSearchList{Files: make([]CodeSearchResult, 0, len(raw.Items)), TotalCount: raw.TotalCount,
		IncompleteResults: raw.Incomplete}
	for _, item := range raw.Items {
		if item.Path == "" || !validRepository(item.Repository.FullName) {
			return nil, invalidEntry(op, "a code match")
		}
		result.Files = append(result.Files, CodeSearchResult{Path: item.Path, Repository: item.Repository.FullName,
			SHA: item.SHA, URL: item.HTMLURL})
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// issueSearchItemJSON is the REST search/issues item both the issue and the pull request search decode.
type issueSearchItemJSON struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	State     string `json:"state"`
	Draft     bool   `json:"draft"`
	UpdatedAt string `json:"updated_at"`
	HTMLURL   string `json:"html_url"`
	Labels    []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Assignees []struct {
		Login string `json:"login"`
	} `json:"assignees"`
	RepositoryURL string `json:"repository_url"`
}

// repository reads the owner/name the search item's repository_url names, which is
// https://HOST/repos/OWNER/REPO for GitHub.com and for GitHub Enterprise Server alike.
func (item issueSearchItemJSON) repository() (string, bool) {
	_, tail, ok := strings.Cut(item.RepositoryURL, "/repos/")
	return tail, ok && validRepository(tail)
}

func (item issueSearchItemJSON) labels() []string {
	labels := make([]string, 0, len(item.Labels))
	for _, label := range item.Labels {
		labels = append(labels, label.Name)
	}
	return labels
}

func (item issueSearchItemJSON) assignees() []string {
	assignees := make([]string, 0, len(item.Assignees))
	for _, assignee := range item.Assignees {
		assignees = append(assignees, assignee.Login)
	}
	return assignees
}

// IssueSearchResult is the compact view of one issue search match, without its body.
type IssueSearchResult struct {
	Number     int      `json:"number"`
	Title      string   `json:"title"`
	Repository string   `json:"repository"`
	State      string   `json:"state"`
	Labels     []string `json:"labels"`
	Assignees  []string `json:"assignees"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
	URL        string   `json:"url,omitempty"`
}

// IssueSearchList is one batch of an issue search.
type IssueSearchList struct {
	Issues            []IssueSearchResult `json:"issues"`
	TotalCount        int                 `json:"total_count"`
	IncompleteResults bool                `json:"incomplete_results"`
	NextCursor        string              `json:"next_cursor,omitempty"`
	HasMore           bool                `json:"has_more"`
}

func (c *Client) searchIssues(ctx context.Context, a *searchArguments) (*IssueSearchList, error) {
	const op = "search issues"
	var raw struct {
		TotalCount int                   `json:"total_count"`
		Incomplete bool                  `json:"incomplete_results"`
		Items      []issueSearchItemJSON `json:"items"`
	}
	hasNext, err := c.restPage(ctx, op, "/search/issues", a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, issueSearchPermission)
	}
	result := &IssueSearchList{Issues: make([]IssueSearchResult, 0, len(raw.Items)), TotalCount: raw.TotalCount,
		IncompleteResults: raw.Incomplete}
	for _, item := range raw.Items {
		repository, ok := item.repository()
		if item.Number < 1 || !ok {
			return nil, invalidEntry(op, "an issue")
		}
		result.Issues = append(result.Issues, IssueSearchResult{Number: item.Number, Title: item.Title,
			Repository: repository, State: item.State, Labels: item.labels(), Assignees: item.assignees(),
			UpdatedAt: item.UpdatedAt, URL: item.HTMLURL})
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// PullRequestSearchResult is the compact view of one pull request search match, without its body.
type PullRequestSearchResult struct {
	Number     int      `json:"number"`
	Title      string   `json:"title"`
	Repository string   `json:"repository"`
	State      string   `json:"state"`
	Draft      bool     `json:"draft,omitempty"`
	Labels     []string `json:"labels"`
	Assignees  []string `json:"assignees"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
	URL        string   `json:"url,omitempty"`
}

// PullRequestSearchList is one batch of a pull request search.
type PullRequestSearchList struct {
	PullRequests      []PullRequestSearchResult `json:"pull_requests"`
	TotalCount        int                       `json:"total_count"`
	IncompleteResults bool                      `json:"incomplete_results"`
	NextCursor        string                    `json:"next_cursor,omitempty"`
	HasMore           bool                      `json:"has_more"`
}

func (c *Client) searchPullRequests(ctx context.Context, a *searchArguments) (*PullRequestSearchList, error) {
	const op = "search pull requests"
	var raw struct {
		TotalCount int                   `json:"total_count"`
		Incomplete bool                  `json:"incomplete_results"`
		Items      []issueSearchItemJSON `json:"items"`
	}
	hasNext, err := c.restPage(ctx, op, "/search/issues", a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, pullRequestSearchPermission)
	}
	result := &PullRequestSearchList{PullRequests: make([]PullRequestSearchResult, 0, len(raw.Items)),
		TotalCount: raw.TotalCount, IncompleteResults: raw.Incomplete}
	for _, item := range raw.Items {
		repository, ok := item.repository()
		if item.Number < 1 || !ok {
			return nil, invalidEntry(op, "a pull request")
		}
		result.PullRequests = append(result.PullRequests, PullRequestSearchResult{Number: item.Number,
			Title: item.Title, Repository: repository, State: item.State, Draft: item.Draft,
			Labels: item.labels(), Assignees: item.assignees(), UpdatedAt: item.UpdatedAt, URL: item.HTMLURL})
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// CommitSearchResult is the compact view of one commit search match.
type CommitSearchResult struct {
	SHA         string `json:"sha"`
	Message     string `json:"message,omitempty"`
	Repository  string `json:"repository"`
	Author      string `json:"author,omitempty"`
	CommittedAt string `json:"committed_at,omitempty"`
	URL         string `json:"url,omitempty"`
}

// CommitSearchList is one batch of a commit search.
type CommitSearchList struct {
	Commits           []CommitSearchResult `json:"commits"`
	TotalCount        int                  `json:"total_count"`
	IncompleteResults bool                 `json:"incomplete_results"`
	NextCursor        string               `json:"next_cursor,omitempty"`
	HasMore           bool                 `json:"has_more"`
}

func (c *Client) searchCommits(ctx context.Context, a *searchArguments) (*CommitSearchList, error) {
	const op = "search commits"
	var raw struct {
		TotalCount int  `json:"total_count"`
		Incomplete bool `json:"incomplete_results"`
		Items      []struct {
			SHA        string `json:"sha"`
			HTMLURL    string `json:"html_url"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
			Commit struct {
				Message string `json:"message"`
				Author  struct {
					Name string `json:"name"`
					Date string `json:"date"`
				} `json:"author"`
			} `json:"commit"`
		} `json:"items"`
	}
	hasNext, err := c.restPage(ctx, op, "/search/commits", a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, commitSearchPermission)
	}
	result := &CommitSearchList{Commits: make([]CommitSearchResult, 0, len(raw.Items)), TotalCount: raw.TotalCount,
		IncompleteResults: raw.Incomplete}
	for _, item := range raw.Items {
		if item.SHA == "" || !validRepository(item.Repository.FullName) {
			return nil, invalidEntry(op, "a commit")
		}
		result.Commits = append(result.Commits, CommitSearchResult{SHA: item.SHA,
			Message: item.Commit.Message, Repository: item.Repository.FullName, Author: item.Commit.Author.Name,
			CommittedAt: item.Commit.Author.Date, URL: item.HTMLURL})
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// UserSearchResult is the compact view of one user search match.
type UserSearchResult struct {
	Login string `json:"login"`
	Type  string `json:"type"`
	URL   string `json:"url,omitempty"`
}

// UserSearchList is one batch of a user search.
type UserSearchList struct {
	Users             []UserSearchResult `json:"users"`
	TotalCount        int                `json:"total_count"`
	IncompleteResults bool               `json:"incomplete_results"`
	NextCursor        string             `json:"next_cursor,omitempty"`
	HasMore           bool               `json:"has_more"`
}

func (c *Client) searchUsers(ctx context.Context, a *searchArguments) (*UserSearchList, error) {
	const op = "search users"
	var raw struct {
		TotalCount int  `json:"total_count"`
		Incomplete bool `json:"incomplete_results"`
		Items      []struct {
			Login   string `json:"login"`
			Type    string `json:"type"`
			HTMLURL string `json:"html_url"`
		} `json:"items"`
	}
	hasNext, err := c.restPage(ctx, op, "/search/users", a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, userSearchPermission)
	}
	result := &UserSearchList{Users: make([]UserSearchResult, 0, len(raw.Items)), TotalCount: raw.TotalCount,
		IncompleteResults: raw.Incomplete}
	for _, item := range raw.Items {
		if !validLogin(item.Login) || item.Type == "" {
			return nil, invalidEntry(op, "a user")
		}
		result.Users = append(result.Users, UserSearchResult{Login: item.Login, Type: item.Type, URL: item.HTMLURL})
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// OrganizationSearchResult is the compact view of one organization search match.
type OrganizationSearchResult struct {
	Login string `json:"login"`
	URL   string `json:"url,omitempty"`
}

// OrganizationSearchList is one batch of an organization search.
type OrganizationSearchList struct {
	Organizations     []OrganizationSearchResult `json:"organizations"`
	TotalCount        int                        `json:"total_count"`
	IncompleteResults bool                       `json:"incomplete_results"`
	NextCursor        string                     `json:"next_cursor,omitempty"`
	HasMore           bool                       `json:"has_more"`
}

func (c *Client) searchOrganizations(ctx context.Context, a *searchArguments) (*OrganizationSearchList, error) {
	const op = "search organizations"
	var raw struct {
		TotalCount int  `json:"total_count"`
		Incomplete bool `json:"incomplete_results"`
		Items      []struct {
			Login   string `json:"login"`
			HTMLURL string `json:"html_url"`
		} `json:"items"`
	}
	hasNext, err := c.restPage(ctx, op, "/search/users", a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, organizationSearchPermission)
	}
	result := &OrganizationSearchList{Organizations: make([]OrganizationSearchResult, 0, len(raw.Items)),
		TotalCount: raw.TotalCount, IncompleteResults: raw.Incomplete}
	for _, item := range raw.Items {
		if !validLogin(item.Login) {
			return nil, invalidEntry(op, "an organization")
		}
		result.Organizations = append(result.Organizations, OrganizationSearchResult{Login: item.Login, URL: item.HTMLURL})
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}
