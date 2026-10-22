package forge

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The repository tools read the API's repository object with every member it documents. A link to another
// object that may lie outside the targets keeps only its identifiers: the parent of a fork, and the accounts and
// teams of a pending transfer. The owner, whose repository the targets allow, is shown as a person without the
// security details of that account.

var idMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID"},
	{name: "full_name", kind: kindText, note: "OWNER/REPO"},
}

var accountIDMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID"},
	{name: "login", kind: kindText, note: "User name"},
}

var teamIDMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID"},
	{name: "name", kind: kindText, note: "Team name"},
}

var repositoryMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the repository"},
	{name: "name", kind: kindText, note: "Repository name"},
	{name: "full_name", kind: kindText, note: "Canonical OWNER/REPO"},
	{name: "owner", kind: kindObject, sub: personMembers, note: "Owner as a user object, without the security " +
		"details of the account"},
	{name: "description", kind: kindText, note: "Description, untrusted data"},
	{name: "website", kind: kindText, note: "Website, untrusted data"},
	{name: "private", kind: kindFlag, note: "True for a private repository"},
	{name: "internal", kind: kindFlag, note: "True for a repository visible to signed-in users only"},
	{name: "fork", kind: kindFlag, note: "True for a fork"},
	{name: "parent", kind: kindObject, sub: idMembers, note: "Repository a fork was made from, identifiers only"},
	{name: "template", kind: kindFlag, note: "True for a template repository"},
	{name: "mirror", kind: kindFlag, note: "True for a mirror"},
	{name: "mirror_interval", kind: kindText, note: "Sync interval of a mirror"},
	{name: "mirror_updated", kind: kindText, note: "Time of the last mirror update"},
	{name: "mirror_last_sync_at", kind: kindText, only: gitea, note: "Time of the last mirror sync"},
	{name: "original_url", kind: kindText, address: true, note: "Address a mirror or migration came from; left " +
		"out when it carries user info or a query"},
	{name: "empty", kind: kindFlag, note: "True while the repository has no commits"},
	{name: "archived", kind: kindFlag, note: "True for an archived repository"},
	{name: "archived_at", kind: kindText, note: "Time it was archived"},
	{name: "size", kind: kindInteger, note: "Size in KiB"},
	{name: "language", kind: kindText, note: "Main language"},
	{name: "languages_url", kind: kindText, note: "API address of the language statistics"},
	{name: "licenses", kind: kindTexts, only: gitea, note: "Licenses the instance detected"},
	{name: "topics", kind: kindTexts, note: "Topics"},
	{name: "object_format_name", kind: kindText, note: "Object format of the Git repository, sha1 or sha256"},
	{name: "default_branch", kind: kindText, note: "Default branch"},
	{name: "default_target_branch", kind: kindText, only: gitea, note: "Default target branch of pull requests"},
	{name: "branch_count", kind: kindInteger, only: gitea, note: "Number of branches"},
	{name: "stars_count", kind: kindInteger, note: "Number of stars"},
	{name: "forks_count", kind: kindInteger, note: "Number of forks"},
	{name: "watchers_count", kind: kindInteger, note: "Number of watchers"},
	{name: "open_issues_count", kind: kindInteger, note: "Number of open issues"},
	{name: "open_pr_counter", kind: kindInteger, note: "Number of open pull requests"},
	{name: "release_counter", kind: kindInteger, note: "Number of releases"},
	{name: "has_code", kind: kindFlag, only: gitea, note: "True while the code unit is enabled"},
	{name: "has_issues", kind: kindFlag, note: "True while issues are enabled"},
	{name: "internal_tracker", kind: kindObject, note: "Settings of the built-in issue tracker", sub: []member{
		{name: "enable_time_tracker", kind: kindFlag, note: "True while time tracking is enabled"},
		{name: "allow_only_contributors_to_track_time", kind: kindFlag, note: "True while only contributors track time"},
		{name: "enable_issue_dependencies", kind: kindFlag, note: "True while issue dependencies are enabled"},
	}},
	{name: "external_tracker", kind: kindObject, note: "External issue tracker", sub: []member{
		{name: "external_tracker_url", kind: kindText, note: "Address of the tracker"},
		{name: "external_tracker_format", kind: kindText, note: "Address format of an issue"},
		{name: "external_tracker_style", kind: kindText, note: "numeric, alphanumeric, or regexp"},
		{name: "external_tracker_regexp_pattern", kind: kindText, note: "Pattern of an issue reference"},
	}},
	{name: "has_wiki", kind: kindFlag, note: "True while the wiki is enabled"},
	{name: "has_wiki_contents", kind: kindFlag, only: forgejo, note: "True once wiki pages were created"},
	{name: "globally_editable_wiki", kind: kindFlag, only: forgejo, note: "True when every signed-in user may edit the wiki"},
	{name: "wiki_branch", kind: kindText, only: forgejo, note: "Branch of the wiki"},
	{name: "external_wiki", kind: kindObject, note: "External wiki", sub: []member{
		{name: "external_wiki_url", kind: kindText, note: "Address of the wiki"},
	}},
	{name: "has_pull_requests", kind: kindFlag, note: "True while pull requests are enabled"},
	{name: "has_projects", kind: kindFlag, note: "True while projects are enabled"},
	{name: "projects_mode", kind: kindText, only: gitea, note: "Which projects the repository shows"},
	{name: "has_releases", kind: kindFlag, note: "True while releases are enabled"},
	{name: "has_packages", kind: kindFlag, note: "True while packages are enabled"},
	{name: "has_actions", kind: kindFlag, note: "True while Actions are enabled"},
	{name: "ignore_whitespace_conflicts", kind: kindFlag, note: "True when merges ignore whitespace conflicts"},
	{name: "allow_merge_commits", kind: kindFlag, note: "True when merge commits are allowed"},
	{name: "allow_rebase", kind: kindFlag, note: "True when rebase merges are allowed"},
	{name: "allow_rebase_explicit", kind: kindFlag, note: "True when rebase with a merge commit is allowed"},
	{name: "allow_squash_merge", kind: kindFlag, note: "True when squash merges are allowed"},
	{name: "allow_fast_forward_only_merge", kind: kindFlag, note: "True when fast-forward-only merges are allowed"},
	{name: "allow_rebase_update", kind: kindFlag, note: "True when a pull request may be updated by rebase"},
	{name: "allow_manual_merge", kind: kindFlag, only: gitea, note: "True when manual merges are allowed"},
	{name: "autodetect_manual_merge", kind: kindFlag, only: gitea, note: "True when manual merges are detected"},
	{name: "allow_merge_update", kind: kindFlag, only: gitea, note: "True when a pull request may be updated by merge"},
	{name: "default_delete_branch_after_merge", kind: kindFlag, note: "True when the head branch is deleted after a merge"},
	{name: "default_merge_style", kind: kindText, note: "Default merge style"},
	{name: "default_update_style", kind: kindText, note: "Default update style of a pull request"},
	{name: "default_allow_maintainer_edit", kind: kindFlag, note: "True when maintainers may edit by default"},
	{name: "permissions", kind: kindObject, note: "What the token's user may do", sub: []member{
		{name: "admin", kind: kindFlag, note: "Administration"},
		{name: "push", kind: kindFlag, note: "Write"},
		{name: "pull", kind: kindFlag, note: "Read"},
	}},
	{name: "repo_transfer", kind: kindObject, note: "Pending transfer, identifiers only", sub: []member{
		{name: "doer", kind: kindObject, sub: accountIDMembers, note: "Account that started the transfer"},
		{name: "recipient", kind: kindObject, sub: accountIDMembers, note: "Account the repository goes to"},
		{name: "teams", kind: kindObjects, sub: teamIDMembers, note: "Teams the repository goes to"},
	}},
	{name: "html_url", kind: kindText, note: "Address of the repository page"},
	{name: "url", kind: kindText, note: "API address of the repository"},
	{name: "link", kind: kindText, note: "Path of the repository"},
	{name: "clone_url", kind: kindText, note: "HTTP clone address"},
	{name: "ssh_url", kind: kindText, note: "SSH clone address"},
	{name: "wiki_clone_url", kind: kindText, only: forgejo, note: "HTTP clone address of the wiki"},
	{name: "wiki_ssh_url", kind: kindText, only: forgejo, note: "SSH clone address of the wiki"},
	{name: "avatar_url", kind: kindText, note: "Address of the avatar image"},
	{name: "created_at", kind: kindText, note: "Creation time"},
	{name: "updated_at", kind: kindText, note: "Time of the last update"},
}

const repositorySensitivity = "-repository-data"

// orderValues are the sort orders Forgejo documents for the token's own repositories.
var orderValues = []string{"name", "id", "newest", "oldest", "recentupdate", "leastupdate",
	"reversealphabetically", "alphabetically", "reversesize", "size", "reversegitsize", "gitsize", "reverselfssize",
	"lfssize", "moststars", "feweststars", "mostforks", "fewestforks"}

var repositoriesList = &tool{
	group: "repositories", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties := `"owner":` + ownerSchema + `,` + pagingKeys
		arguments := []capability.Argument{{Name: "owner", Description: "Owner as users/NAME or orgs/NAME; left " +
			"out lists the repositories of the token's own account; must lie inside the targets when the " +
			"connection lists any, as an owner or through a repository of it"}}
		if p == forgejo {
			encoded, _ := json.Marshal(orderValues)
			properties += `,"order_by":{"type":"string","enum":` + string(encoded) + `}`
			arguments = append(arguments, capability.Argument{Name: "order_by", Description: "Sort order of the " +
				"token's own repositories: " + strings.Join(orderValues, ", ") + "; only without owner"})
		}
		return capability.Descriptor{
			Title: "List " + p.name + " repositories",
			Description: "List one batch of the repositories of an owner, or of the token's own account, with " +
				"every member of each repository; rows outside the connection's targets are left out",
			Tags:        []string{p.id, "repositories", "list"},
			Risk:        readRisk(p.id + repositorySensitivity),
			InputSchema: inputSchema(properties),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repositories":{"type":"array","items":` +
				schemaOf(p, repositoryMembers, "id", "name", "full_name") + `},"owner":{"type":"string"},` +
				`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
				`"required":["repositories","has_more"],"additionalProperties":false}`),
			Arguments: append(arguments, pagingArguments...),
			Fields: append([]capability.Field{
				{Name: "repositories", Description: "Repositories with every member of the repository object"},
				{Name: "owner", Description: "Owner the tool listed, as users/NAME or orgs/NAME; absent for the " +
					"token's own repositories"},
			}, pagingFields...),
			Examples: []capability.Example{{Description: "List the repositories of an organization",
				Arguments: json.RawMessage(`{"owner":"orgs/example","limit":20}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Owner   string `json:"owner"`
			OrderBy string `json:"order_by"`
			Limit   int    `json:"limit"`
			Cursor  string `json:"cursor"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		path, owner, ownerName := "/user/repos", "", ""
		if arguments.Owner != "" {
			parsed, err := parseOwnerArgument(arguments.Owner)
			if err != nil {
				return nil, err
			}
			if !allowed.listsOwner(parsed.owner) {
				return nil, outsideTargets("owner")
			}
			if arguments.OrderBy != "" {
				return nil, invalidArgument("order_by applies only to the token's own repositories, without owner")
			}
			path, owner, ownerName = "/"+parsed.scope+"/"+escape(parsed.owner)+"/repos", parsed.String(), parsed.owner
		}
		binding := provider.CursorBinding(p.id, "repositories", strings.ToLower(owner), arguments.OrderBy)
		page, err := startPaging(binding, arguments.Limit, arguments.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "list repositories"
			query := page.query()
			if arguments.OrderBy != "" {
				query.Set("order_by", arguments.OrderBy)
			}
			var rows []object
			header, err := c.get(ctx, op, path, query, &rows)
			if err != nil {
				return nil, err
			}
			repositories := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				// A row of an owner list must also belong to that owner.
				fullName, _ := row.text("full_name")
				login, _, _ := strings.Cut(fullName, "/")
				if allowed.allowsFullName(fullName) && (ownerName == "" || strings.EqualFold(login, ownerName)) {
					repositories = append(repositories, project(p, repositoryMembers, row))
				}
			}
			next := page.next(header, len(rows))
			result := map[string]any{"repositories": repositories, "has_more": next != ""}
			if next != "" {
				result["next_cursor"] = next
			}
			if owner != "" {
				result["owner"] = owner
			}
			return result, nil
		}, nil
	},
}

var repositoriesGet = &tool{
	group: "repositories", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a " + p.name + " repository",
			Description: "Read one repository with every member of the repository object, by OWNER/REPO or by its " +
				"numeric ID; the answer's canonical name must lie inside the connection's targets",
			Tags: []string{p.id, "repositories", "get"},
			Risk: readRisk(p.id + repositorySensitivity),
			InputSchema: inputSchema(`"repository":` + repoSchema +
				`,"id":{"type":"integer","minimum":1,"maximum":9007199254740991}`),
			OutputSchema: json.RawMessage(schemaOf(p, repositoryMembers, "id", "name", "full_name")),
			Arguments: []capability.Argument{
				{Name: "repository", Description: "Repository as OWNER/REPO; optional when the connection's targets " +
					"name exactly one repository or id is given; must lie inside the targets when the connection " +
					"lists any", Target: kindRepository},
				{Name: "id", Description: "Numeric repository ID instead of repository; the repository it names is " +
					"read once and returned only when it lies inside the connection's targets"},
			},
			Fields: fieldsOf(p, repositoryMembers),
			Examples: []capability.Example{{Description: "Read a repository",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var arguments struct {
			Repository string `json:"repository"`
			ID         int64  `json:"id"`
		}
		if err := decodeArguments(raw, &arguments); err != nil {
			return nil, err
		}
		var path string
		switch {
		case arguments.ID != 0 && arguments.Repository != "":
			return nil, invalidArgument("pass either repository or id, not both")
		case arguments.ID != 0:
			path = "/repositories/" + strconv.FormatInt(arguments.ID, 10)
		default:
			chosen, err := allowed.chooseRepository(arguments.Repository)
			if err != nil {
				return nil, err
			}
			path = "/repos/" + escape(chosen.owner) + "/" + escape(chosen.repo)
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get repository"
			var repository object
			if _, err := c.get(ctx, op, path, nil, &repository); err != nil {
				return nil, err
			}
			fullName, ok := repository.text("full_name")
			if !ok || !strings.Contains(fullName, "/") {
				return nil, provider.InvalidResponse(op, p.name+" returned a repository without its name")
			}
			if !allowed.allowsFullName(fullName) {
				return nil, outsideTargets("repository")
			}
			return project(p, repositoryMembers, repository), nil
		}, nil
	},
}
