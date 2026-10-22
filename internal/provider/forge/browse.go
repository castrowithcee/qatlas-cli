package forge

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Choosing targets reads what the token reaches, beyond the connection's targets and never through the tools:
// the account behind the token and its organizations as owners, and the repositories of one owner or of the
// token's own account. It only reads.

const browsePageSize = 50

func (p *product) browseList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, req config.TargetListRequest) (config.TargetPage, error) {
	var owner target
	switch req.Kind {
	case kindOwner:
	case kindRepository:
		if req.Parent != "" {
			parsed, err := parseTarget(req.Parent)
			if err != nil || parsed.kind != kindOwner {
				return config.TargetPage{}, invalidArgument("the parent must be users/NAME or orgs/NAME")
			}
			owner = parsed
		}
	default:
		return config.TargetPage{}, invalidArgument("the kind is not one of this provider")
	}
	page, err := startPaging(provider.CursorBinding(p.id, "browse", req.Kind, strings.ToLower(req.Parent)),
		browsePageSize, req.Cursor)
	if err != nil {
		return config.TargetPage{}, err
	}
	client, err := open(ctx, p, untargeted(resolved), secrets, red)
	if err != nil {
		return config.TargetPage{}, err
	}
	if req.Kind == kindOwner {
		return client.browseOwners(ctx, page)
	}
	return client.browseRepositories(ctx, owner, page)
}

// browseOwners offers the account behind the token on the first page, then its organizations.
func (c *Client) browseOwners(ctx context.Context, page paging) (config.TargetPage, error) {
	const op = "list owners"
	var result config.TargetPage
	if page.page == 1 {
		var account object
		if _, err := c.get(ctx, op, "/user", nil, &account); err != nil {
			return config.TargetPage{}, err
		}
		if login, _ := account.text("login"); validName(login) {
			result.Entries = append(result.Entries, ownerEntry("users", login))
		}
	}
	var orgs []object
	header, err := c.get(ctx, op, "/user/orgs", page.query(), &orgs)
	if err != nil {
		return config.TargetPage{}, err
	}
	for _, org := range orgs {
		if name, _ := org.text("name"); validName(name) {
			result.Entries = append(result.Entries, ownerEntry("orgs", name))
		}
	}
	result.Next = page.next(header, len(orgs))
	return result, nil
}

// browseRepositories lists the repositories of one owner, or without one those of the token's own account.
func (c *Client) browseRepositories(ctx context.Context, owner target, page paging) (config.TargetPage, error) {
	const op = "list repositories"
	path := "/user/repos"
	if owner.owner != "" {
		path = "/" + owner.scope + "/" + escape(owner.owner) + "/repos"
	}
	var rows []object
	header, err := c.get(ctx, op, path, page.query(), &rows)
	if err != nil {
		return config.TargetPage{}, err
	}
	var result config.TargetPage
	for _, row := range rows {
		fullName, _ := row.text("full_name")
		login, name, ok := strings.Cut(fullName, "/")
		if ok && validName(login) && validRepoName(name) {
			result.Entries = append(result.Entries, repositoryEntry(login, name))
		}
	}
	result.Next = page.next(header, len(rows))
	return result, nil
}

// untargeted is the connection without its targets: choosing reaches beyond them.
func untargeted(resolved *config.Resolved) *config.Resolved {
	copied := *resolved
	copied.Target, copied.Targets = "", nil
	return &copied
}

func ownerEntry(scope, name string) config.TargetEntry {
	return config.TargetEntry{Target: scope + "/" + name, Name: name, Kind: kindOwner, HasChildren: true}
}

func repositoryEntry(owner, name string) config.TargetEntry {
	return config.TargetEntry{Target: "repos/" + owner + "/" + name, Name: name, Kind: kindRepository, Path: owner}
}

// reservedPaths are first path segments of the web interface that are pages, not owners.
var reservedPaths = map[string]bool{
	"-": true, "admin": true, "api": true, "assets": true, "explore": true, "issues": true, "login": true,
	"milestones": true, "notifications": true, "org": true, "pulls": true, "repo": true, "user": true,
}

// browseResolveURL turns a web address of the instance into a target. It knows the forms the API's html_url
// members take: https://HOST/OWNER/REPO, its issues/N and pulls/N, and https://HOST/NAME. An address of another
// host is answered without a request.
func (p *product) browseResolveURL(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, origin, rawURL string) ([]config.TargetEntry, error) {
	instance, err := originOf(origin)
	parsed, parseErr := url.Parse(rawURL)
	if err != nil || parseErr != nil || parsed.Scheme != "https" || !strings.EqualFold("https://"+parsed.Host, instance) {
		return nil, nil
	}
	var segments []string
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	if len(segments) == 0 || reservedPaths[strings.ToLower(segments[0])] || !validName(segments[0]) {
		return nil, nil
	}
	switch {
	case len(segments) == 2 && validRepoName(segments[1]):
		return []config.TargetEntry{repositoryEntry(segments[0], segments[1])}, nil
	case len(segments) == 4 && validRepoName(segments[1]) && (segments[2] == "issues" || segments[2] == "pulls") &&
		segments[3] != "" && strings.Trim(segments[3], "0123456789") == "":
		return []config.TargetEntry{repositoryEntry(segments[0], segments[1])}, nil
	case len(segments) != 1:
		return nil, nil
	}
	client, err := open(ctx, p, untargeted(resolved), secrets, red)
	if err != nil {
		return nil, err
	}
	return client.resolveOwner(ctx, segments[0])
}

// resolveOwner tells an organization from a user: an organization answers below /orgs, a user only below
// /users. A name the token does not see yields no entry.
func (c *Client) resolveOwner(ctx context.Context, name string) ([]config.TargetEntry, error) {
	const op = "resolve owner"
	var answer object
	_, err := c.get(ctx, op, "/orgs/"+escape(name), nil, &answer)
	if err == nil {
		if canonical, _ := answer.text("name"); validName(canonical) && strings.EqualFold(canonical, name) {
			return []config.TargetEntry{ownerEntry("orgs", canonical)}, nil
		}
		return nil, nil
	}
	if !notFound(err) {
		return nil, err
	}
	answer = nil
	if _, err := c.get(ctx, op, "/users/"+escape(name), nil, &answer); err != nil {
		if notFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if login, _ := answer.text("login"); validName(login) && strings.EqualFold(login, name) {
		return []config.TargetEntry{ownerEntry("users", login)}, nil
	}
	return nil, nil
}

func notFound(err error) bool {
	var failure *provider.Error
	return errors.As(err, &failure) && failure.Class == provider.ClassNotFound
}
