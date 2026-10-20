package github

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Choosing targets reads what the token reaches, owners first and then the repositories or projects of one
// owner, or resolves a URL of the web interface. It only reads, never through the tools that filter by the
// connection's targets, so a person finds an entry the targets do not name yet.

const (
	browsePageSize = 50
	maxBrowseText  = 200
	maxBrowseTerms = 200
)

func targetBrowser() capability.TargetBrowser {
	return capability.TargetBrowser{List: browseList, ResolveURL: browseResolveURL}
}

// browseText bounds a name GitHub sent: control characters become spaces and the text is cut on a rune boundary.
func browseText(value string) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return ' '
		}
		return r
	}, value))
	if len(value) > maxBrowseText {
		cut := maxBrowseText
		for !utf8.RuneStart(value[cut]) {
			cut--
		}
		value = strings.TrimSpace(value[:cut])
	}
	return value
}

// browseClient opens the token of the connection without its targets: choosing reaches beyond them.
func browseClient(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (*Client, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	client.allowed = nil
	return client, nil
}

func browseList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	req config.TargetListRequest) (config.TargetPage, error) {
	var owner target
	switch req.Kind {
	case "owner":
	case "repository", "project":
		var err error
		owner, err = parseTarget(req.Parent)
		if err != nil || owner.kind != kindOwner {
			return config.TargetPage{}, invalidRequest("the parent must be users/LOGIN or orgs/LOGIN")
		}
	default:
		return config.TargetPage{}, invalidRequest("the kind is not one of this provider")
	}
	client, err := browseClient(ctx, resolved, secrets, red)
	if err != nil {
		return config.TargetPage{}, err
	}
	switch req.Kind {
	case "owner":
		return client.browseOwners(ctx, req.Cursor)
	case "repository":
		client.target = owner
		return client.browseRepositories(ctx, owner, req)
	}
	client.target = owner
	return client.browseProjects(ctx, owner, req.Cursor)
}

// browseOwners lists the account behind the token, on the first page, and the organizations it belongs to.
func (c *Client) browseOwners(ctx context.Context, cursor string) (config.TargetPage, error) {
	const op = "list owners"
	binding := fingerprint("browse owners")
	after, err := decodeCursor(binding, cursor)
	page := 1
	if err == nil && after != "" {
		page, err = strconv.Atoi(after)
		if err != nil || page < 2 || page > maxPage {
			err = invalidRequest("cursor is not a next_cursor of this list; start the list again without cursor")
		}
	}
	if err != nil {
		return config.TargetPage{}, err
	}
	var result config.TargetPage
	if page == 1 {
		var account struct {
			Login string `json:"login"`
		}
		if err := c.rest(ctx, op, "/user", &account); err != nil {
			return config.TargetPage{}, err
		}
		if validLogin(account.Login) {
			result.Entries = append(result.Entries, ownerEntry("users", account.Login))
		}
	}
	var orgs []struct {
		Login string `json:"login"`
	}
	hasNext, err := c.restPage(ctx, op, "/user/orgs",
		url.Values{"per_page": {strconv.Itoa(browsePageSize)}, "page": {strconv.Itoa(page)}}, &orgs)
	if err != nil {
		return config.TargetPage{}, err
	}
	for _, org := range orgs {
		if validLogin(org.Login) {
			result.Entries = append(result.Entries, ownerEntry("orgs", org.Login))
		}
	}
	if hasNext && page < maxPage {
		result.Next = encodeCursor(binding, strconv.Itoa(page+1))
	}
	return result, nil
}

func ownerEntry(scope, login string) config.TargetEntry {
	return config.TargetEntry{Target: scope + "/" + login, Name: login, Kind: "owner", HasChildren: true}
}

func repositoryEntry(owner, name string) config.TargetEntry {
	return config.TargetEntry{Target: "repos/" + owner + "/" + name, Name: name, Kind: "repository", Path: owner}
}

func projectEntry(scope, owner string, number int, title string) config.TargetEntry {
	if title = browseText(title); title == "" {
		title = "Project " + strconv.Itoa(number)
	}
	return config.TargetEntry{Target: scope + "/" + owner + "/projects/" + strconv.Itoa(number), Name: title,
		Kind: "project", Path: owner}
}

// browseRepositories lists the repositories the owner owns, or, with a query, the ones GitHub's repository
// search finds by name within that owner.
func (c *Client) browseRepositories(ctx context.Context, owner target,
	req config.TargetListRequest) (config.TargetPage, error) {
	if terms := searchTermsOf(req.Query); terms != "" {
		qualifier := "user:"
		if owner.scope == "orgs" {
			qualifier = "org:"
		}
		// The terms are one quoted phrase, so they cannot carry a qualifier or an operator of their own.
		arguments := searchArguments{Terms: `"` + terms + `" in:name`, Limit: browsePageSize, Cursor: req.Cursor}
		if err := checkSearchArguments("browse repositories", qualifier+owner.owner, false, &arguments); err != nil {
			return config.TargetPage{}, err
		}
		found, err := c.searchRepositories(ctx, &arguments)
		if err != nil {
			return config.TargetPage{}, err
		}
		result := config.TargetPage{Next: found.NextCursor, ServerSearch: true}
		for _, item := range found.Repositories {
			if login, name, _ := strings.Cut(item.Repository, "/"); strings.EqualFold(login, owner.owner) {
				result.Entries = append(result.Entries, repositoryEntry(login, name))
			}
		}
		return result, nil
	}
	options := ownerListOptions{Limit: browsePageSize, Cursor: req.Cursor}
	binding, after, err := options.normalize("browse repositories", owner)
	if err != nil {
		return config.TargetPage{}, err
	}
	list, err := c.listRepositories(ctx, options, binding, after)
	if err != nil {
		return config.TargetPage{}, err
	}
	result := config.TargetPage{Next: list.NextCursor}
	if req.Cursor == "" {
		// The first page offers the whole owner first: every repository it owns.
		result.Entries = append(result.Entries, config.TargetEntry{Target: "repos/" + owner.owner + "/*",
			Name: "All repositories of " + owner.owner, Kind: "repository", Path: owner.owner})
	}
	for _, item := range list.Repositories {
		login, name, _ := strings.Cut(item.Repository, "/")
		result.Entries = append(result.Entries, repositoryEntry(login, name))
	}
	return result, nil
}

// searchTermsOf returns the query as plain search text: no quotes, no control characters, bounded.
func searchTermsOf(query string) string {
	terms := browseText(strings.ReplaceAll(query, `"`, " "))
	if len(terms) > maxBrowseTerms {
		terms = strings.TrimSpace(terms[:maxBrowseTerms])
		for !utf8.ValidString(terms) {
			terms = terms[:len(terms)-1]
		}
	}
	return terms
}

// browseProjects lists the projects of the owner, open and closed. GitHub offers no search for them.
func (c *Client) browseProjects(ctx context.Context, owner target, cursor string) (config.TargetPage, error) {
	options := ownerListOptions{Limit: browsePageSize, Cursor: cursor}
	binding, after, err := options.normalize("browse projects", owner)
	if err != nil {
		return config.TargetPage{}, err
	}
	list, err := c.listProjects(ctx, options, binding, after)
	if err != nil {
		return config.TargetPage{}, err
	}
	result := config.TargetPage{Next: list.NextCursor}
	for _, item := range list.Projects {
		result.Entries = append(result.Entries, projectEntry(owner.scope, owner.owner, item.Number, item.Title))
	}
	return result, nil
}

// webHostOf returns the one host of the web interface that belongs to the service whose API has the origin:
// github.com for https://api.github.com, SUBDOMAIN.ghe.com for https://api.SUBDOMAIN.ghe.com, and for GitHub
// Enterprise Server, which serves both below one host, that host. Any other origin names no web host.
func webHostOf(origin string) string {
	if origin == "" {
		return "github.com"
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return ""
	}
	host := strings.ToLower(parsed.Host)
	if host == "api.github.com" {
		return "github.com"
	}
	if sub, ok := strings.CutPrefix(host, "api."); ok {
		label, isGhe := strings.CutSuffix(sub, ".ghe.com")
		if isGhe && label != "" && !strings.Contains(label, ".") {
			return sub
		}
		return ""
	}
	return host
}

// reservedPaths are the first path segments of github.com that are pages of the service, not owners.
var reservedPaths = map[string]bool{
	"about": true, "account": true, "apps": true, "codespaces": true, "collections": true, "contact": true,
	"copilot": true, "customer-stories": true, "dashboard": true, "enterprise": true, "events": true,
	"explore": true, "features": true, "github-copilot": true, "issues": true, "join": true, "labs": true,
	"login": true, "logout": true, "marketplace": true, "mcp": true, "new": true, "notifications": true,
	"organizations": true, "orgs": true, "password_reset": true, "premium-support": true, "pricing": true,
	"pulls": true, "readme": true, "resources": true, "search": true, "security": true, "sessions": true,
	"settings": true, "signup": true, "site": true, "solutions": true, "sponsors": true, "stars": true,
	"team": true, "topics": true, "trending": true, "users": true, "watching": true,
}

// browseResolveURL turns a URL of the web interface into a repository, a project, or an owner. Only the web
// host of the service's API origin counts; a URL of another host is answered without a request.
func browseResolveURL(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	origin, rawURL string) ([]config.TargetEntry, error) {
	parsed, err := url.Parse(rawURL)
	host := webHostOf(origin)
	if err != nil || host == "" || !strings.EqualFold(parsed.Host, host) {
		return nil, nil
	}
	var segments []string
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	if len(segments) == 0 {
		return nil, nil
	}
	first := segments[0]
	if scope := strings.ToLower(first); scope == "orgs" || scope == "users" {
		if len(segments) < 4 || segments[2] != "projects" || !validLogin(segments[1]) {
			return nil, nil
		}
		number, ok := projectNumber(segments[3])
		if !ok {
			return nil, nil
		}
		return []config.TargetEntry{projectEntry(scope, segments[1], number, "")}, nil
	}
	if reservedPaths[strings.ToLower(first)] || !validLogin(first) {
		return nil, nil
	}
	if len(segments) >= 2 {
		if !validRepoName(segments[1]) {
			return nil, nil
		}
		return []config.TargetEntry{repositoryEntry(first, segments[1])}, nil
	}
	return browseOwnerURL(ctx, resolved, secrets, red, first)
}

// browseOwnerURL reads once whether a login is a user or an organization. GitHub's answer for a login it does
// not show to this token, or for an account of another type, is no entry.
func browseOwnerURL(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	login string) ([]config.TargetEntry, error) {
	client, err := browseClient(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var account struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	}
	if err := client.rest(ctx, "resolve owner", "/users/"+url.PathEscape(login), &account); err != nil {
		var failure *provider.Error
		if errors.As(err, &failure) && failure.Class == provider.ClassNotFound {
			return nil, nil
		}
		return nil, err
	}
	if !validLogin(account.Login) || !strings.EqualFold(account.Login, login) {
		return nil, nil
	}
	switch account.Type {
	case "User":
		return []config.TargetEntry{ownerEntry("users", account.Login)}, nil
	case "Organization":
		return []config.TargetEntry{ownerEntry("orgs", account.Login)}, nil
	}
	return nil, nil
}
