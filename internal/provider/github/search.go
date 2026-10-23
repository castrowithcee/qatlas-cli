package github

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The search tools read GitHub's own search index: repositories, code, issues, pull requests, commits,
// users, and organizations. They take search terms and GitHub qualifiers as one free argument, terms, never
// a query document or a route of their own, and they change nothing.
//
// A connection whose targets name a repository, an owner, or a project never reaches beyond them: before a
// credential is resolved, github.repositories.search, github.code.search, github.issues.search,
// github.pull-requests.search, and github.commits.search force the repo: and org:/user: qualifiers the
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
// github.issues.search, github.pull-requests.search, and github.commits.search force from the connection's
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
// so github.pull-requests.search never answers with plain issues, whatever a connection's targets allow.
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
	return searchOutputWith(key, properties, required, "")
}

// searchOutputWith is searchOutput with further top-level members of one search's own.
func searchOutputWith(key, properties, required, extra string) json.RawMessage {
	if extra != "" {
		extra += ","
	}
	return json.RawMessage(`{"type":"object","properties":{"` + key + `":{"type":"array","items":{"type":"object",` +
		`"properties":{` + properties + `},` + required + `}},` + extra + `"total_count":{"type":"integer"},` +
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

// issueSearchSortValues are the orders GitHub documents for the issue search.
var issueSearchSortValues = []string{"comments", "reactions", "reactions-+1", "reactions--1", "reactions-smile",
	"reactions-thinking_face", "reactions-heart", "reactions-tada", "interactions", "created", "updated"}

var issueSearchTypeValues = []string{"semantic", "hybrid"}

// issueSearchKeys are the filters and the switches only the issue search takes, beside the terms.
var issueSearchKeys = `"sort":{"type":"string","enum":["` + strings.Join(issueSearchSortValues, `","`) + `"]},` +
	`"order":{"type":"string","enum":["asc","desc"]},"advanced_search":{"type":"boolean"},` +
	`"search_type":{"type":"string","enum":["` + strings.Join(issueSearchTypeValues, `","`) + `"]},` +
	`"text_matches":{"type":"boolean"}`

// issueSearchItemProperties are the members of one issue search match: the compact view the pull request
// search shares, then the remainder of GitHub's issue and the search members.
var issueSearchItemProperties = issueSearchProperties + `,` + issueCoreProperties + `,` + issueWidthProperties +
	`,"repository_detail":` + restRepositorySchema + `,"score":{"type":"number"},` +
	`"text_matches":{"type":"array","maxItems":100,"items":` + textMatchSchema + `},` +
	`"text_matches_truncated":{"type":"boolean"}`

// pullSearchLinkKeys are the addresses of a pull request search match: those of the issue behind it and the
// diff and patch addresses of the pull request.
var pullSearchLinkKeys = append(append([]string(nil), issueLinkKeys...), "diff_url", "patch_url")

// pullSearchItemProperties are the members of one pull request search match: those of an issue search match
// with the pull request's own links and merge time.
var pullSearchItemProperties = issueSearchProperties + `,` + issueCoreProperties + `,` + issueWidthLeadProperties +
	`,"merged_at":{"type":"string"},"links":` + linksSchema(pullSearchLinkKeys) +
	`,"repository_detail":` + restRepositorySchema + `,"score":{"type":"number"},` +
	`"text_matches":{"type":"array","maxItems":100,"items":` + textMatchSchema + `},` +
	`"text_matches_truncated":{"type":"boolean"}`

// withoutField returns fields without the one named name.
func withoutField(fields []capability.Field, name string) []capability.Field {
	var out []capability.Field
	for _, field := range fields {
		if field.Name != name {
			out = append(out, field)
		}
	}
	return out
}

const issueSearchExtraProperties = `"search_type":{"type":"string"},` +
	`"lexical_fallback_reason":{"type":"array","maxItems":100,"items":{"type":"string"}},` +
	`"lexical_fallback_reason_truncated":{"type":"boolean"}`

// issueSearchArguments are the arguments both searches over GitHub's issue index take besides the terms.
var issueSearchArguments = append([]capability.Argument{
	{Name: "sort", Description: strings.Join(issueSearchSortValues, ", ") + "; GitHub's best match order " +
		"when omitted"},
	{Name: "order", Description: "asc or desc; applies with sort, desc when omitted"},
	{Name: "advanced_search", Description: "true or false sends GitHub's advanced_search switch as given; " +
		"GitHub's default when omitted"},
	{Name: "search_type", Description: "semantic or hybrid; GitHub's lexical search when omitted; GitHub " +
		"limits both to 10 requests a minute"},
	{Name: "text_matches", Description: "True adds text_matches, the places the terms matched; false when omitted"},
}, pagingArguments...)

// issueSearchResponseFields describe the members of the answer and of a match that both searches over
// GitHub's issue index add to their compact view.
var issueSearchResponseFields = []capability.Field{
	{Name: "search_type", Description: "Kind of search GitHub performed: lexical, semantic, or hybrid"},
	{Name: "lexical_fallback_reason", Description: "Why a semantic or hybrid search fell back to lexical " +
		"search, at most 100 reasons"},
	{Name: "lexical_fallback_reason_truncated", Description: "True when GitHub gave more reasons than shown"},
	repositoryDetailField,
	{Name: "score", Description: "Relevance score GitHub gave the match"},
	{Name: "text_matches", Description: "Where the terms matched, only when text_matches is set, at most 100: " +
		"object_type, property, fragment (cut at 1000 characters, fragment_truncated says so), matches (text " +
		"and indices), and links with the object_url; untrusted data, never fetched"},
	{Name: "text_matches_truncated", Description: "True when more text matches followed than shown"},
}

var issuesSearch = capability.Descriptor{
	ID:      Provider + ".issues.search",
	Version: 1,
	Title:   "Search GitHub issues",
	Description: "Search issues, never pull requests, GitHub's index covers with terms and qualifiers, " +
		"narrowed to the repositories and the owners a connection's targets allow when it names any",
	Tags:        []string{"github", "issues", "search", "discovery"},
	Risk:        readRisk,
	Provider:    Provider,
	InputSchema: inputSchema(`"terms":`+termsSchema+`,`+issueSearchKeys+`,`+pagingKeys, "terms"),
	OutputSchema: searchOutputWith("issues", issueSearchItemProperties, issueSearchRequired,
		issueSearchExtraProperties),
	Arguments: append([]capability.Argument{searchTermsArgument}, issueSearchArguments...),
	Fields: append(append(append(append(append([]capability.Field{
		{Name: "issues", Description: "Issues across repositories: number, title (untrusted data), " +
			"repository as OWNER/REPO, state, labels, assignees, updated_at, and url, then the remainder of " +
			"GitHub's issue below"},
		issueDraftField,
	}, issueSearchResponseFields...), issueCoreFields...), issueWidthFields...), searchCountFields...), pagingFields...),
	Examples: []capability.Example{{
		Description: "Search open issues labeled bug",
		Arguments:   json.RawMessage(`{"terms":"is:open label:bug","limit":10}`),
	}},
}

var pullRequestsSearch = capability.Descriptor{
	ID:      Provider + ".pull-requests.search",
	Version: 1,
	Title:   "Search GitHub pull requests",
	Description: "Search pull requests, never plain issues, GitHub's index covers with terms and qualifiers, " +
		"narrowed to the repositories and the owners a connection's targets allow when it names any",
	Tags:        []string{"github", "pulls", "pullrequests", "search", "discovery"},
	Risk:        readRisk,
	Provider:    Provider,
	InputSchema: inputSchema(`"terms":`+termsSchema+`,`+issueSearchKeys+`,`+pagingKeys, "terms"),
	OutputSchema: searchOutputWith("pull_requests", pullSearchItemProperties, issueSearchRequired,
		issueSearchExtraProperties),
	Arguments: append([]capability.Argument{searchTermsArgument}, issueSearchArguments...),
	Fields: append(append(append(append(append([]capability.Field{
		{Name: "pull_requests", Description: "Pull requests across repositories: number, title (untrusted " +
			"data), repository as OWNER/REPO, state, draft, labels, assignees, updated_at, and url, then the " +
			"remainder of GitHub's issue below"},
		{Name: "merged_at", Description: "When the pull request was merged; absent until then"},
		{Name: "links", Description: "REST url, repository_url, labels_url, comments_url, events_url, " +
			"timeline_url, parent_issue_url, diff_url, and patch_url by their GitHub names, untrusted data, " +
			"never fetched"},
	}, issueSearchResponseFields...), issueCoreFields...), withoutField(issueWidthFields, "links")...),
		searchCountFields...), pagingFields...),
	Examples: []capability.Example{{
		Description: "Search merged pull requests based on main",
		Arguments:   json.RawMessage(`{"terms":"is:merged base:main","limit":10}`),
	}},
}

var commitSearchProperties = `"sha":{"type":"string"},"message":{"type":"string"},"repository":{"type":"string"},` +
	`"author":{"type":"string"},"committed_at":{"type":"string"},` + commitWidthProperties + `,` +
	commitSearchExtraProperties

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
	InputSchema:  inputSchema(`"terms":`+termsSchema+`,`+commitSearchKeys+`,`+pagingKeys, "terms"),
	OutputSchema: searchOutput("commits", commitSearchProperties, commitSearchRequired),
	Arguments:    commitSearchArguments,
	Fields: append(append(append(append([]capability.Field{
		{Name: "commits", Description: "Compact commits across repositories: sha, message (untrusted data), " +
			"repository as OWNER/REPO, author, and committed_at, then the members of GitHub's commit below"},
	}, commitWidthFields...), commitSearchExtraFields...), searchCountFields...), pagingFields...),
	Examples: []capability.Example{{
		Description: "Search commits by an author since a date",
		Arguments:   json.RawMessage(`{"terms":"author:octocat committer-date:>2026-01-01","limit":10}`),
	}},
}

var userSearchProperties = `"login":{"type":"string"},"type":{"type":"string"},"url":{"type":"string"},` +
	accountPersonProperties + `,` + accountProfileProperties + `,` + searchExtrasProperties

// accountSearchKeys are the filters and the switch only the account searches take, beside the terms.
const accountSearchKeys = `"sort":{"type":"string","enum":["followers","repositories","joined"]},` +
	`"order":{"type":"string","enum":["asc","desc"]},"text_matches":{"type":"boolean"}`

var (
	accountSearchSortValues  = []string{"followers", "repositories", "joined"}
	accountSearchOrderValues = []string{"asc", "desc"}
)

// accountSearchArguments are the terms, the sort, the order, the text match switch, and the paging of the
// account searches.
var accountSearchArguments = append([]capability.Argument{searchTermsArgument,
	{Name: "sort", Description: "followers, repositories, or joined; GitHub's best match order when omitted"},
	{Name: "order", Description: "asc or desc; applies with sort, desc when omitted"},
	{Name: "text_matches", Description: "True adds text_matches, the places the terms matched; false when omitted"},
}, pagingArguments...)

var accountSearchFields = append(append(append(append([]capability.Field(nil), accountPersonFields...),
	accountProfileFields...), searchExtrasFields...), searchCountFields...)

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
	InputSchema:  inputSchema(`"terms":`+termsSchema+`,`+accountSearchKeys+`,`+pagingKeys, "terms"),
	OutputSchema: searchOutput("users", userSearchProperties, userSearchRequired),
	Arguments:    accountSearchArguments,
	Fields: append(append([]capability.Field{
		{Name: "users", Description: "Accounts: login, type (always User), url, and the members of GitHub's " +
			"search result, including a person's name and email address; untrusted data"},
	}, accountSearchFields...), pagingFields...),
	Examples: []capability.Example{{
		Description: "Search users by location",
		Arguments:   json.RawMessage(`{"terms":"location:berlin","limit":10}`),
	}},
}

var organizationSearchProperties = `"login":{"type":"string"},"url":{"type":"string"},"type":{"type":"string"},` +
	accountPersonProperties + `,` + accountProfileProperties + `,` + searchExtrasProperties

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
	InputSchema:  inputSchema(`"terms":`+termsSchema+`,`+accountSearchKeys+`,`+pagingKeys, "terms"),
	OutputSchema: searchOutput("organizations", organizationSearchProperties, organizationSearchRequired),
	Arguments:    accountSearchArguments,
	Fields: append(append([]capability.Field{
		{Name: "organizations", Description: "Organizations: login, url, and the members of GitHub's search " +
			"result; untrusted data"},
		{Name: "type", Description: "Account type, Organization"},
	}, accountSearchFields...), pagingFields...),
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

	// Sort, Order, and TextMatches are the account, commit, and issue searches' own, AdvancedSearch and
	// SearchType the issue search's; the others never carry them.
	Sort           string `json:"sort"`
	Order          string `json:"order"`
	TextMatches    bool   `json:"text_matches"`
	AdvancedSearch *bool  `json:"advanced_search"`
	SearchType     string `json:"search_type"`

	q                          string
	sort, order                string
	textMatches                bool
	advancedSearch, searchType string
	page, perPage              int
	binding                    []byte
	allowed                    allowlist
}

func (a *searchArguments) query() url.Values {
	values := url.Values{"q": {a.q}, "per_page": {strconv.Itoa(a.perPage)}, "page": {strconv.Itoa(a.page)}}
	if a.sort != "" {
		values.Set("sort", a.sort)
	}
	if a.order != "" {
		values.Set("order", a.order)
	}
	if a.advancedSearch != "" {
		values.Set("advanced_search", a.advancedSearch)
	}
	if a.searchType != "" {
		values.Set("search_type", a.searchType)
	}
	return values
}

// accountOptions validates the sort, the order, and the text match switch of an account search and
// returns what they add to the cursor binding, nothing while none is set.
func (a *searchArguments) accountOptions() ([]any, error) {
	return a.sortedOptions(accountSearchSortValues, "sort must be followers, repositories, or joined")
}

// issueOptions validates the filters and the switches of the issue search and returns what they add to the
// cursor binding, nothing while none is set.
func (a *searchArguments) issueOptions() ([]any, error) {
	binding, err := a.sortedOptions(issueSearchSortValues, "sort must be one of "+
		strings.Join(issueSearchSortValues, ", "))
	if err != nil {
		return nil, err
	}
	if a.AdvancedSearch != nil {
		a.advancedSearch = strconv.FormatBool(*a.AdvancedSearch)
		binding = append(binding, "advanced_search", a.advancedSearch)
	}
	if a.SearchType != "" {
		if !containsFold(issueSearchTypeValues, a.SearchType) {
			return nil, invalidRequest("search_type must be semantic or hybrid")
		}
		a.searchType = strings.ToLower(a.SearchType)
		binding = append(binding, "search_type", a.searchType)
	}
	return binding, nil
}

// sortedOptions validates the sort against values, the order, and the text match switch and returns what
// they add to the cursor binding, nothing while none is set.
func (a *searchArguments) sortedOptions(values []string, sortMessage string) ([]any, error) {
	var binding []any
	if a.Sort != "" {
		if !containsFold(values, a.Sort) {
			return nil, invalidRequest(sortMessage)
		}
		a.sort = strings.ToLower(a.Sort)
		binding = append(binding, "sort", a.sort)
	}
	if a.Order != "" {
		if !containsFold(accountSearchOrderValues, a.Order) {
			return nil, invalidRequest("order must be asc or desc")
		}
		a.order = strings.ToLower(a.Order)
		binding = append(binding, "order", a.order)
	}
	if a.TextMatches {
		a.textMatches = true
		binding = append(binding, "text_matches")
	}
	return binding, nil
}

// textMatchContext asks for the text match media type when the request set text_matches.
func (a *searchArguments) textMatchContext(ctx context.Context) context.Context {
	if !a.textMatches {
		return ctx
	}
	return context.WithValue(ctx, acceptKey{}, "application/vnd.github.text-match+json")
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
	binding := []any{"search", tool, forced, a.Terms}
	var options []any
	switch tool {
	case usersSearch.ID, organizationsSearch.ID:
		options, err = a.accountOptions()
	case issuesSearch.ID, pullRequestsSearch.ID:
		options, err = a.issueOptions()
	case commitsSearch.ID:
		options, err = a.sortedOptions(commitSearchSortValues, "sort must be author-date or committer-date")
	}
	if err != nil {
		return err
	}
	binding = append(binding, options...)
	a.binding = fingerprint(binding...)
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
		arguments.allowed = allowed
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
	PullRequest   *struct {
		MergedAt *string `json:"merged_at"`
		DiffURL  *string `json:"diff_url"`
		PatchURL *string `json:"patch_url"`
	} `json:"pull_request"`
	width issueWidthRaw
}

func (item *issueSearchItemJSON) UnmarshalJSON(data []byte) error {
	type plain issueSearchItemJSON
	if err := json.Unmarshal(data, (*plain)(item)); err != nil {
		return err
	}
	item.width = readIssueWidth(data)
	return nil
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

// IssueSearchResult is one issue search match: the compact view, then the remainder of GitHub's issue and
// the search members.
type IssueSearchResult struct {
	Number     int      `json:"number"`
	Title      string   `json:"title"`
	Repository string   `json:"repository"`
	State      string   `json:"state"`
	Labels     []string `json:"labels"`
	Assignees  []string `json:"assignees"`
	UpdatedAt  string   `json:"updated_at,omitempty"`
	URL        string   `json:"url,omitempty"`

	IssueCore
	IssueWidth
	RepositoryDetail     *RESTRepository `json:"repository_detail,omitempty"`
	Score                *json.Number    `json:"score,omitempty"`
	TextMatches          []RESTTextMatch `json:"text_matches,omitempty"`
	TextMatchesTruncated bool            `json:"text_matches_truncated,omitempty"`
}

// IssueSearchList is one batch of an issue search.
type IssueSearchList struct {
	Issues                         []IssueSearchResult `json:"issues"`
	SearchType                     *string             `json:"search_type,omitempty"`
	LexicalFallbackReason          []string            `json:"lexical_fallback_reason,omitempty"`
	LexicalFallbackReasonTruncated bool                `json:"lexical_fallback_reason_truncated,omitempty"`
	TotalCount                     int                 `json:"total_count"`
	IncompleteResults              bool                `json:"incomplete_results"`
	NextCursor                     string              `json:"next_cursor,omitempty"`
	HasMore                        bool                `json:"has_more"`
}

func (c *Client) searchIssues(ctx context.Context, a *searchArguments) (*IssueSearchList, error) {
	const op = "search issues"
	var raw struct {
		TotalCount            int                   `json:"total_count"`
		Incomplete            bool                  `json:"incomplete_results"`
		Items                 []issueSearchItemJSON `json:"items"`
		SearchType            *string               `json:"search_type"`
		LexicalFallbackReason []*string             `json:"lexical_fallback_reason"`
	}
	hasNext, err := c.restPage(a.textMatchContext(ctx), op, "/search/issues", a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, issueSearchPermission)
	}
	result := &IssueSearchList{Issues: make([]IssueSearchResult, 0, len(raw.Items)), TotalCount: raw.TotalCount,
		IncompleteResults: raw.Incomplete, SearchType: shortText(raw.SearchType)}
	var reasons []string
	for _, reason := range raw.LexicalFallbackReason {
		if text := shortText(reason); text != nil {
			reasons = append(reasons, *text)
		}
	}
	result.LexicalFallbackReason, result.LexicalFallbackReasonTruncated = capped(reasons)
	for _, item := range raw.Items {
		repository, ok := item.repository()
		if item.Number < 1 || !ok {
			return nil, invalidEntry(op, "an issue")
		}
		// The forced qualifiers bound the search. advanced_search and search_type change how GitHub reads the
		// query, so with either set every match is checked against the targets as well, and an answer
		// outside them is refused rather than shown.
		owner, name, _ := strings.Cut(repository, "/")
		if (a.advancedSearch != "" || a.searchType != "") && len(a.allowed) > 0 &&
			!a.allowed.admitsRepository(target{kind: kindRepository, owner: owner, repo: name}) {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "GitHub answered with an issue outside the connection's targets"}
		}
		width := &item.width
		match := IssueSearchResult{Number: item.Number, Title: item.Title, Repository: repository, State: item.State,
			Labels: item.labels(), Assignees: item.assignees(), UpdatedAt: item.UpdatedAt, URL: item.HTMLURL,
			IssueCore: width.core(), IssueWidth: width.width(), RepositoryDetail: width.Repository.project(a.allowed),
			Score: width.Score}
		match.TextMatches, match.TextMatchesTruncated = projectTextMatches(width.TextMatches)
		result.Issues = append(result.Issues, match)
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// PullRequestSearchResult is one pull request search match: the compact view, then the remainder of GitHub's
// issue as the issue search reports it, the merge time, and the search members. Links holds the addresses of
// the issue and the diff and patch addresses of the pull request.
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

	IssueCore
	IssueWidth
	MergedAt             *string         `json:"merged_at,omitempty"`
	Links                Links           `json:"links,omitempty"`
	RepositoryDetail     *RESTRepository `json:"repository_detail,omitempty"`
	Score                *json.Number    `json:"score,omitempty"`
	TextMatches          []RESTTextMatch `json:"text_matches,omitempty"`
	TextMatchesTruncated bool            `json:"text_matches_truncated,omitempty"`
}

// PullRequestSearchList is one batch of a pull request search.
type PullRequestSearchList struct {
	PullRequests                   []PullRequestSearchResult `json:"pull_requests"`
	SearchType                     *string                   `json:"search_type,omitempty"`
	LexicalFallbackReason          []string                  `json:"lexical_fallback_reason,omitempty"`
	LexicalFallbackReasonTruncated bool                      `json:"lexical_fallback_reason_truncated,omitempty"`
	TotalCount                     int                       `json:"total_count"`
	IncompleteResults              bool                      `json:"incomplete_results"`
	NextCursor                     string                    `json:"next_cursor,omitempty"`
	HasMore                        bool                      `json:"has_more"`
}

func (c *Client) searchPullRequests(ctx context.Context, a *searchArguments) (*PullRequestSearchList, error) {
	const op = "search pull requests"
	var raw struct {
		TotalCount            int                   `json:"total_count"`
		Incomplete            bool                  `json:"incomplete_results"`
		Items                 []issueSearchItemJSON `json:"items"`
		SearchType            *string               `json:"search_type"`
		LexicalFallbackReason []*string             `json:"lexical_fallback_reason"`
	}
	hasNext, err := c.restPage(a.textMatchContext(ctx), op, "/search/issues", a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, pullRequestSearchPermission)
	}
	result := &PullRequestSearchList{PullRequests: make([]PullRequestSearchResult, 0, len(raw.Items)),
		TotalCount: raw.TotalCount, IncompleteResults: raw.Incomplete, SearchType: shortText(raw.SearchType)}
	var reasons []string
	for _, reason := range raw.LexicalFallbackReason {
		if text := shortText(reason); text != nil {
			reasons = append(reasons, *text)
		}
	}
	result.LexicalFallbackReason, result.LexicalFallbackReasonTruncated = capped(reasons)
	for _, item := range raw.Items {
		repository, ok := item.repository()
		if item.Number < 1 || !ok {
			return nil, invalidEntry(op, "a pull request")
		}
		// The forced qualifiers bound the search. advanced_search and search_type change how GitHub reads the
		// query, so with either set every match is checked against the targets as well, and an answer
		// outside them is refused rather than shown.
		owner, name, _ := strings.Cut(repository, "/")
		if (a.advancedSearch != "" || a.searchType != "") && len(a.allowed) > 0 &&
			!a.allowed.admitsRepository(target{kind: kindRepository, owner: owner, repo: name}) {
			return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
				Message: "GitHub answered with a pull request outside the connection's targets"}
		}
		width := &item.width
		match := PullRequestSearchResult{Number: item.Number, Title: item.Title, Repository: repository,
			State: item.State, Draft: item.Draft, Labels: item.labels(), Assignees: item.assignees(),
			UpdatedAt: item.UpdatedAt, URL: item.HTMLURL, IssueCore: width.core(), IssueWidth: width.width(),
			Links: width.links, RepositoryDetail: width.Repository.project(a.allowed), Score: width.Score}
		if pull := item.PullRequest; pull != nil {
			match.MergedAt = shortText(pull.MergedAt)
			if link := address(filled(pull.DiffURL)); link != nil {
				match.Links = withLink(match.Links, "diff_url", *link)
			}
			if link := address(filled(pull.PatchURL)); link != nil {
				match.Links = withLink(match.Links, "patch_url", *link)
			}
		}
		match.TextMatches, match.TextMatchesTruncated = projectTextMatches(width.TextMatches)
		result.PullRequests = append(result.PullRequests, match)
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// withLink returns links with key set; links is copied, never changed.
func withLink(links Links, key, value string) Links {
	out := Links{key: value}
	for k, v := range links {
		out[k] = v
	}
	return out
}

// CommitSearchResult is the compact view of one commit search match.
type CommitSearchResult struct {
	SHA         string `json:"sha"`
	Message     string `json:"message,omitempty"`
	Repository  string `json:"repository"`
	Author      string `json:"author,omitempty"`
	CommittedAt string `json:"committed_at,omitempty"`

	// CommitWidth holds the members of GitHub's commit; its URL is shadowed by the web address kept here.
	URL string `json:"url,omitempty"`
	CommitWidth
	Committer            string          `json:"committer,omitempty"`
	Parents              *int            `json:"parents,omitempty"`
	RepositoryDetail     *RESTRepository `json:"repository_detail,omitempty"`
	Score                *json.Number    `json:"score,omitempty"`
	TextMatches          []RESTTextMatch `json:"text_matches,omitempty"`
	TextMatchesTruncated bool            `json:"text_matches_truncated,omitempty"`
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
		TotalCount int                `json:"total_count"`
		Incomplete bool               `json:"incomplete_results"`
		Items      []commitSearchItem `json:"items"`
	}
	hasNext, err := c.restPage(a.textMatchContext(ctx), op, "/search/commits", a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, commitSearchPermission)
	}
	result := &CommitSearchList{Commits: make([]CommitSearchResult, 0, len(raw.Items)), TotalCount: raw.TotalCount,
		IncompleteResults: raw.Incomplete}
	for _, item := range raw.Items {
		compact := &item.compact
		if compact.SHA == "" || !validRepository(compact.Repository.FullName) {
			return nil, invalidEntry(op, "a commit")
		}
		match := CommitSearchResult{SHA: compact.SHA, Message: compact.Commit.Message,
			Repository: compact.Repository.FullName, Author: compact.Commit.Author.Name,
			CommittedAt: compact.Commit.Author.Date, URL: compact.HTMLURL, CommitWidth: item.wide.width(),
			Score:            item.more.Score,
			RepositoryDetail: item.more.Repository.project(a.allowed)}
		if committer := item.wide.Committer; committer != nil {
			match.Committer = valueOr(nameText(committer.Login))
		}
		if item.wide.Parents != nil {
			parents := len(item.wide.Parents)
			match.Parents = &parents
		}
		match.TextMatches, match.TextMatchesTruncated = projectTextMatches(item.more.TextMatches)
		result.Commits = append(result.Commits, match)
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// UserSearchResult is the compact view of one user search match.
type UserSearchResult struct {
	Login string `json:"login"`
	Type  string `json:"type"`
	URL   string `json:"url,omitempty"`
	AccountPerson
	AccountProfile
	SearchExtras
}

// accountSearchRaw is one hit of an account search: the members the searches always answered, read as
// before, and the ones they answer in addition.
type accountSearchRaw struct {
	Login   string `json:"login"`
	Type    string `json:"type"`
	HTMLURL string `json:"html_url"`
	account accountRaw
	profile profileRaw
	extras  searchExtrasRaw
}

func (r *accountSearchRaw) UnmarshalJSON(data []byte) error {
	type plain accountSearchRaw
	if err := json.Unmarshal(data, (*plain)(r)); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &r.profile); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &r.extras); err != nil {
		return err
	}
	return json.Unmarshal(data, &r.account)
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
		TotalCount int                `json:"total_count"`
		Incomplete bool               `json:"incomplete_results"`
		Items      []accountSearchRaw `json:"items"`
	}
	hasNext, err := c.restPage(a.textMatchContext(ctx), op, "/search/users", a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, userSearchPermission)
	}
	result := &UserSearchList{Users: make([]UserSearchResult, 0, len(raw.Items)), TotalCount: raw.TotalCount,
		IncompleteResults: raw.Incomplete}
	for _, item := range raw.Items {
		if !validLogin(item.Login) || item.Type == "" {
			return nil, invalidEntry(op, "a user")
		}
		result.Users = append(result.Users, UserSearchResult{Login: item.Login, Type: item.Type, URL: item.HTMLURL,
			AccountPerson: item.account.person(), AccountProfile: item.profile.project(),
			SearchExtras: item.extras.project()})
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}

// OrganizationSearchResult is the compact view of one organization search match.
type OrganizationSearchResult struct {
	Login string  `json:"login"`
	URL   string  `json:"url,omitempty"`
	Type  *string `json:"type,omitempty"`
	AccountPerson
	AccountProfile
	SearchExtras
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
		TotalCount int                `json:"total_count"`
		Incomplete bool               `json:"incomplete_results"`
		Items      []accountSearchRaw `json:"items"`
	}
	hasNext, err := c.restPage(a.textMatchContext(ctx), op, "/search/users", a.query(), &raw)
	if err != nil {
		return nil, actionsFailure(err, organizationSearchPermission)
	}
	result := &OrganizationSearchList{Organizations: make([]OrganizationSearchResult, 0, len(raw.Items)),
		TotalCount: raw.TotalCount, IncompleteResults: raw.Incomplete}
	for _, item := range raw.Items {
		if !validLogin(item.Login) {
			return nil, invalidEntry(op, "an organization")
		}
		result.Organizations = append(result.Organizations, OrganizationSearchResult{Login: item.Login,
			URL: item.HTMLURL, Type: shortText(&item.Type), AccountPerson: item.account.person(),
			AccountProfile: item.profile.project(), SearchExtras: item.extras.project()})
	}
	result.HasMore, result.NextCursor = morePage(a.binding, a.page, a.perPage, hasNext)
	return result, nil
}
