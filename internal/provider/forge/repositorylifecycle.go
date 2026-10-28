package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The repository lifecycle tools create, change, fork, generate, transfer, and delete repositories. Each handler
// has one fixed method and path. A repository argument is chosen against the targets; a new owner, whether an
// organization or the account behind the token, must be named by an owner target of the connection before
// anything is sent. Nothing here sends mirror credentials, an external tracker or wiki, or a Sudo value.

const (
	maxRepoDescription = 2048
	maxRepoWebsite     = 1024
	maxTemplateName    = 255
	maxTeamIDs         = 50

	organizationSchema = `{"type":"string","minLength":1,"maxLength":40,"pattern":"^[A-Za-z0-9_][A-Za-z0-9_.-]{0,39}$"}`
	repoNameSchema     = `{"type":"string","minLength":1,"maxLength":100,"pattern":"^[A-Za-z0-9_.-]+$"}`
	repoTextSchema     = `{"type":"string","maxLength":255}`
)

var organizationArgument = capability.Argument{Name: "organization", Description: "Organization name that " +
	"receives the repository; it must be named as orgs/NAME by the connection's targets. Left out, the account " +
	"behind the token receives it, which the targets must name as users/NAME"}

func repositoryPath(repo target) string {
	return "/repos/" + escape(repo.owner) + "/" + escape(repo.repo)
}

func fullName(repo target) string { return repo.owner + "/" + repo.repo }

// namesOwnerOfScope reports whether the list admits the owner by an owner target of exactly that scope.
func (a allowlist) namesOwnerOfScope(scope, name string) bool {
	if a.open() {
		return true
	}
	for _, entry := range a.targets {
		if entry.kind == kindOwner && entry.scope == scope && strings.EqualFold(entry.owner, name) {
			return true
		}
	}
	return false
}

// checkDestination checks where a new repository goes: the organization, which the targets name as orgs/NAME,
// or, left out, the account behind the token, for which they must name some user. The account itself is proven
// by reading it before the change.
func (a allowlist) checkDestination(organization string) error {
	if organization != "" {
		if !validName(organization) {
			return invalidArgument("organization must be an organization name")
		}
		if !a.namesOwnerOfScope("orgs", organization) {
			return outsideTargets("organization")
		}
		return nil
	}
	if a.open() {
		return nil
	}
	for _, entry := range a.targets {
		if entry.kind == kindOwner && entry.scope == "users" {
			return nil
		}
	}
	return invalidArgument("without organization the repository goes to the account behind the token, which the " +
		"connection's targets must name as users/NAME; pass organization or add that target")
}

// verifyOwnAccount is the evidence read before a change that lands in the account behind the token: that
// account must be a user the targets name.
func verifyOwnAccount(ctx context.Context, c *Client, op string, allowed allowlist) error {
	if allowed.open() {
		return nil
	}
	var account object
	if _, err := c.get(ctx, op, "/user", nil, &account); err != nil {
		return err
	}
	login, ok := account.text("login")
	if !ok || login == "" {
		return provider.InvalidResponse(op, c.product.name+" returned an account without a login")
	}
	if !allowed.namesOwnerOfScope("users", login) {
		return outsideTargets("account behind the token")
	}
	return nil
}

func repositoryNameCheck(field, value string) error {
	if !validRepoName(value) {
		return invalidArgument(field + " must be a repository name of 1 to 100 letters, digits, '.', '-', or '_'")
	}
	return nil
}

func checkOneLine(field, value string, max int) error {
	if utf8.RuneCountInString(value) > max || !plainText(value) {
		return invalidArgument(field + " must hold at most " + itoa(max) + " characters without control characters")
	}
	return nil
}

func checkTemplateName(field, value string, max int) error {
	if len(value) > max || !plainText(value) {
		return invalidArgument(field + " must hold at most " + itoa(max) + " characters without control characters")
	}
	return nil
}

// repositoryChange is the answer of a change that may move the repository out of the targets: the new owner or
// a new name can lie outside them, and then the answer carries no details.
func repositoryChange(p *product, allowed allowlist, key string, row object) map[string]any {
	name, _ := row.text("full_name")
	result := map[string]any{key: true, "reachable": false}
	if allowed.allowsFullName(name) {
		result["reachable"] = true
		result["repository"] = project(p, repositoryMembers, row)
	}
	return result
}

func changeOutput(p *product, key string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"` + key + `":{"type":"boolean"},` +
		`"reachable":{"type":"boolean"},"repository":` + schemaOf(p, repositoryMembers) + `},` +
		`"required":["` + key + `","reachable"],"additionalProperties":false}`)
}

func changeFields(p *product, key, what string) []capability.Field {
	return []capability.Field{{Name: key, Description: "True once the instance " + what},
		{Name: "reachable", Description: "True when the repository's canonical name lies inside the connection's " +
			"targets; false when it moved or was renamed out of them, then no repository is shown"},
		{Name: "repository", Description: "Repository with every member of the repository object, only when reachable"}}
}

func requireRepositoryAnswer(p *product, op string, row object) error {
	if name, ok := row.text("full_name"); !ok || !strings.Contains(name, "/") {
		return unsure(provider.InvalidResponse(op, p.name+" returned a repository without its name"))
	}
	return nil
}

var repositoryChangeNote = " Only a connection whose targets still reach the result sees it."

// ---- create -------------------------------------------------------------------------------------------------

var objectFormats = `["sha1","sha256"]`
var trustModels = `["default","collaborator","committer","collaboratorcommitter"]`

var repositoriesCreate = &tool{
	group: "repositories", action: "create", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Create a " + p.name + " repository",
			Description: "Create one repository in an organization the connection's targets name as orgs/NAME, or " +
				"in the account behind the token when the targets name that user or none. The owner is " +
				"checked before the request. Not idempotent: a repeated call fails once the name exists.",
			Tags:        []string{p.id, "repositories", "create"},
			Risk:        changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, p.id+repositorySensitivity),
			InputSchema: inputSchema(createProperties(), "name"),
			OutputSchema: json.RawMessage(schemaOf(p, repositoryMembers, "id", "name",
				"full_name")),
			Arguments: append([]capability.Argument{organizationArgument}, repositoryCreateArguments...),
			Fields:    fieldsOf(p, repositoryMembers),
			Examples: []capability.Example{{Description: "Create a private repository in an organization",
				Arguments: json.RawMessage(`{"organization":"example","name":"project","private":true}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Organization string `json:"organization"`
			repositoryCreateOptions
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := allowed.checkDestination(a.Organization); err != nil {
			return nil, err
		}
		body, err := a.repositoryCreateOptions.body()
		if err != nil {
			return nil, err
		}
		path := "/user/repos"
		if a.Organization != "" {
			path = "/orgs/" + escape(a.Organization) + "/repos"
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "create repository"
			if a.Organization == "" {
				if err := verifyOwnAccount(ctx, c, op, allowed); err != nil {
					return nil, err
				}
			}
			var created object
			if err := c.change(ctx, op, http.MethodPost, path, body, &created); err != nil {
				return nil, err
			}
			if err := requireRepositoryAnswer(p, op, created); err != nil {
				return nil, err
			}
			return project(p, repositoryMembers, created), nil
		}, nil
	},
}

func createProperties() string {
	return `"organization":` + organizationSchema + `,` + repositoryCreateProperties()
}

// repositoryCreateProperties are the members of CreateRepoOption that a repository creation sends.
func repositoryCreateProperties() string {
	return `"name":` + repoNameSchema +
		`,"description":{"type":"string","maxLength":2048},"private":{"type":"boolean"},` +
		`"auto_init":{"type":"boolean"},"default_branch":` + branchSchemaName + `,"gitignores":` + repoTextSchema +
		`,"issue_labels":` + repoTextSchema + `,"license":` + repoTextSchema + `,"readme":` + repoTextSchema +
		`,"template":{"type":"boolean"},"trust_model":{"type":"string","enum":` + trustModels + `},` +
		`"object_format_name":{"type":"string","enum":` + objectFormats + `}`
}

var repositoryCreateArguments = []capability.Argument{
	{Name: "name", Required: true, Description: "Repository name, 1 to 100 letters, digits, '.', '-', '_'"},
	{Name: "description", Description: "Description, at most 2048 characters"},
	{Name: "private", Description: "True for a private repository"},
	{Name: "auto_init", Description: "True to create an initial commit"},
	{Name: "default_branch", Description: "Default branch of the new repository"},
	{Name: "gitignores", Description: "Gitignore templates, comma separated; see the gitignore " +
		"templates of the instance"},
	{Name: "issue_labels", Description: "Name of a label set"},
	{Name: "license", Description: "Name of a license template"},
	{Name: "readme", Description: "Name of a readme template"},
	{Name: "template", Description: "True to make the repository a template"},
	{Name: "trust_model", Description: "default, collaborator, committer, or collaboratorcommitter"},
	{Name: "object_format_name", Description: "sha1 or sha256"}}

// repositoryCreateOptions are the arguments that become the CreateRepoOption body.
type repositoryCreateOptions struct {
	Name             string  `json:"name"`
	Description      *string `json:"description"`
	Private          *bool   `json:"private"`
	AutoInit         *bool   `json:"auto_init"`
	DefaultBranch    string  `json:"default_branch"`
	Gitignores       string  `json:"gitignores"`
	IssueLabels      string  `json:"issue_labels"`
	License          string  `json:"license"`
	Readme           string  `json:"readme"`
	Template         *bool   `json:"template"`
	TrustModel       string  `json:"trust_model"`
	ObjectFormatName string  `json:"object_format_name"`
}

// body checks the options and returns the request body.
func (a repositoryCreateOptions) body() (map[string]any, error) {
	if err := repositoryNameCheck("name", a.Name); err != nil {
		return nil, err
	}
	body := map[string]any{"name": a.Name}
	if a.Description != nil {
		if err := checkOneLine("description", *a.Description, maxRepoDescription); err != nil {
			return nil, err
		}
		body["description"] = *a.Description
	}
	for name, value := range map[string]*bool{"private": a.Private, "auto_init": a.AutoInit, "template": a.Template} {
		if value != nil {
			body[name] = *value
		}
	}
	if a.DefaultBranch != "" {
		if err := checkRefName("default_branch", a.DefaultBranch, true); err != nil {
			return nil, err
		}
		body["default_branch"] = a.DefaultBranch
	}
	for name, value := range map[string]string{"gitignores": a.Gitignores, "issue_labels": a.IssueLabels,
		"license": a.License, "readme": a.Readme} {
		if value == "" {
			continue
		}
		if err := checkTemplateName(name, value, maxTemplateName); err != nil {
			return nil, err
		}
		body[name] = value
	}
	if a.TrustModel != "" {
		if !strings.Contains(trustModels, `"`+a.TrustModel+`"`) {
			return nil, invalidArgument("trust_model must be default, collaborator, committer, or collaboratorcommitter")
		}
		body["trust_model"] = a.TrustModel
	}
	if a.ObjectFormatName != "" {
		if a.ObjectFormatName != "sha1" && a.ObjectFormatName != "sha256" {
			return nil, invalidArgument("object_format_name must be sha1 or sha256")
		}
		body["object_format_name"] = a.ObjectFormatName
	}
	return body, nil
}

// ---- update -------------------------------------------------------------------------------------------------

// editField is one argument of repositories.update. The table is the whole list of what reaches the request:
// the mirror credentials and the external tracker and wiki of the API are not in it.
type editField struct {
	name   string
	only   *product
	note   string
	schema func(p *product) string
	check  func(p *product, raw json.RawMessage) (any, error)
}

func (f editField) offered(p *product) bool { return f.only == nil || f.only == p }

func flagField(name, note string, only *product) editField {
	return editField{name: name, only: only, note: note,
		schema: func(*product) string { return `{"type":"boolean"}` },
		check: func(_ *product, raw json.RawMessage) (any, error) {
			var value *bool
			if json.Unmarshal(raw, &value) != nil || value == nil {
				return nil, invalidArgument(name + " must be true or false")
			}
			return *value, nil
		}}
}

func textField(name, note string, max int) editField {
	return editField{name: name, note: note,
		schema: func(*product) string { return `{"type":"string","maxLength":` + itoa(max) + `}` },
		check: func(_ *product, raw json.RawMessage) (any, error) {
			var value *string
			if json.Unmarshal(raw, &value) != nil || value == nil {
				return nil, invalidArgument(name + " must be a text")
			}
			if err := checkOneLine(name, *value, max); err != nil {
				return nil, err
			}
			return *value, nil
		}}
}

// enumField takes the values of the product; a product without values does not offer the field.
func enumField(name, note string, values func(p *product) []string) editField {
	return editField{name: name, note: note,
		schema: func(p *product) string {
			encoded, _ := json.Marshal(values(p))
			return `{"type":"string","enum":` + string(encoded) + `}`
		},
		check: func(p *product, raw json.RawMessage) (any, error) {
			var value string
			if json.Unmarshal(raw, &value) != nil || !contains(values(p), value) {
				return nil, invalidArgument(name + " must be one of " + strings.Join(values(p), ", "))
			}
			return value, nil
		}}
}

func mergeStyleValues(p *product) []string {
	values := []string{"merge", "rebase", "rebase-merge", "squash", "fast-forward-only"}
	if p == forgejo {
		values = append(values, "manually-merged", "rebase-update-only")
	}
	return values
}

var trackerFlags = []string{"enable_time_tracker", "allow_only_contributors_to_track_time",
	"enable_issue_dependencies"}

var trackerField = editField{name: "internal_tracker", note: "Settings of the built-in issue tracker: " +
	"enable_time_tracker, allow_only_contributors_to_track_time, and enable_issue_dependencies, all three together, " +
	"since the instance resets the ones left out",
	schema: func(*product) string {
		var properties []string
		for _, name := range trackerFlags {
			properties = append(properties, `"`+name+`":{"type":"boolean"}`)
		}
		return `{"type":"object","properties":{` + strings.Join(properties, ",") + `},"required":["` +
			strings.Join(trackerFlags, `","`) + `"],"additionalProperties":false}`
	},
	check: func(_ *product, raw json.RawMessage) (any, error) {
		var values map[string]*bool
		if json.Unmarshal(raw, &values) != nil || len(values) != len(trackerFlags) {
			return nil, invalidArgument("internal_tracker must give " + strings.Join(trackerFlags, ", "))
		}
		out := map[string]any{}
		for _, name := range trackerFlags {
			if values[name] == nil {
				return nil, invalidArgument("internal_tracker must give " + strings.Join(trackerFlags, ", "))
			}
			out[name] = *values[name]
		}
		return out, nil
	}}

var editFields = []editField{
	{name: "name", note: "New name of the repository; a repos/OWNER/REPO target no longer reaches it afterwards",
		schema: func(*product) string { return repoNameSchema },
		check: func(_ *product, raw json.RawMessage) (any, error) {
			var value string
			if json.Unmarshal(raw, &value) != nil || !validRepoName(value) {
				return nil, invalidArgument("name must be a repository name of 1 to 100 letters, digits, '.', '-', or '_'")
			}
			return value, nil
		}},
	textField("description", "Description, at most 2048 characters", maxRepoDescription),
	textField("website", "Website, at most 1024 characters", maxRepoWebsite),
	flagField("private", "True for a private repository", nil),
	flagField("template", "True to make the repository a template", nil),
	flagField("archived", "True to archive the repository", nil),
	{name: "default_branch", note: "Default branch",
		schema: func(*product) string { return branchSchemaName },
		check: func(_ *product, raw json.RawMessage) (any, error) {
			var value string
			if json.Unmarshal(raw, &value) != nil {
				return nil, invalidArgument("default_branch must be a branch name")
			}
			if err := checkRefName("default_branch", value, true); err != nil {
				return nil, err
			}
			return value, nil
		}},
	flagField("enable_prune", "Mirrors: remove obsolete remote-tracking references", nil),
	textField("mirror_interval", "Mirrors: sync interval such as 8h30m0s", 32),
	flagField("has_issues", "Enable the issue tracker", nil),
	trackerField,
	flagField("has_wiki", "Enable the wiki", nil),
	flagField("has_pull_requests", "Enable pull requests", nil),
	flagField("has_projects", "Enable projects", nil),
	flagField("has_releases", "Enable releases", nil),
	flagField("has_packages", "Enable packages", nil),
	flagField("has_actions", "Enable Actions", nil),
	flagField("ignore_whitespace_conflicts", "Ignore whitespace conflicts in merges", nil),
	flagField("allow_merge_commits", "Allow merge commits", nil),
	flagField("allow_rebase", "Allow rebase merges", nil),
	flagField("allow_rebase_explicit", "Allow rebase with a merge commit", nil),
	flagField("allow_squash_merge", "Allow squash merges", nil),
	flagField("allow_fast_forward_only_merge", "Allow fast-forward-only merges", nil),
	flagField("allow_rebase_update", "Allow updating a pull request by rebase", nil),
	flagField("default_delete_branch_after_merge", "Delete the head branch after a merge by default", nil),
	enumField("default_merge_style", "Default merge style", mergeStyleValues),
	enumField("default_update_style", "Default update style of a pull request",
		func(*product) []string { return []string{"merge", "rebase"} }),
	flagField("default_allow_maintainer_edit", "Allow maintainer edits by default", nil),
	textFieldFor("wiki_branch", "Forgejo only: branch of the wiki", forgejo),
	flagField("globally_editable_wiki", "Forgejo only: every signed-in user may edit the wiki", forgejo),
	enumFieldFor("projects_mode", "Gitea only: which projects the repository shows", gitea,
		[]string{"repo", "owner", "all"}),
	flagField("has_code", "Gitea only: enable the code unit", gitea),
	flagField("allow_manual_merge", "Gitea only: allow marking a pull request as merged manually", gitea),
	flagField("autodetect_manual_merge", "Gitea only: detect manual merges", gitea),
	flagField("allow_merge_update", "Gitea only: allow updating a pull request by merge", gitea),
}

func textFieldFor(name, note string, only *product) editField {
	field := textField(name, note, 255)
	field.only = only
	return field
}

func enumFieldFor(name, note string, only *product, values []string) editField {
	field := enumField(name, note, func(*product) []string { return values })
	field.only = only
	return field
}

func editProperties(p *product) string {
	properties := []string{`"repository":` + repoSchema}
	for _, field := range editFields {
		if field.offered(p) {
			properties = append(properties, `"`+field.name+`":`+field.schema(p))
		}
	}
	return strings.Join(properties, ",")
}

var repositoriesUpdate = &tool{
	group: "repositories", action: "update", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		arguments := []capability.Argument{repositoryArgument}
		for _, field := range editFields {
			if field.offered(p) {
				arguments = append(arguments, capability.Argument{Name: field.name, Description: field.note})
			}
		}
		return capability.Descriptor{
			Title: "Change the settings of a " + p.name + " repository",
			Description: "Change the settings of one repository inside the connection's targets with one request; " +
				"only the arguments given change. Mirror credentials, an external issue tracker, and an external " +
				"wiki are not offered. A rename or a visibility change can make a repos/OWNER/REPO target " +
				"unreachable; the answer then says so without details." + repositoryChangeNote,
			Tags:         []string{p.id, "repositories", "update"},
			Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+repositorySensitivity),
			InputSchema:  inputSchema(editProperties(p)),
			OutputSchema: changeOutput(p, "updated"),
			Arguments:    arguments,
			Fields:       changeFields(p, "updated", "changed the repository"),
			Examples: []capability.Example{{Description: "Archive a repository",
				Arguments: json.RawMessage(`{"repository":"example/project","archived":true}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var given map[string]json.RawMessage
		if len(raw) == 0 {
			raw = json.RawMessage(`{}`)
		}
		if json.Unmarshal(raw, &given) != nil {
			return nil, invalidArgument("the arguments could not be read")
		}
		var repository string
		if value, ok := given["repository"]; ok {
			if json.Unmarshal(value, &repository) != nil {
				return nil, invalidArgument("repository must be OWNER/REPO")
			}
			delete(given, "repository")
		}
		repo, err := allowed.chooseRepository(repository)
		if err != nil {
			return nil, err
		}
		body := map[string]any{}
		for key, value := range given {
			var found *editField
			for i := range editFields {
				if editFields[i].name == key {
					found = &editFields[i]
				}
			}
			switch {
			case found == nil:
				return nil, invalidArgument("an argument is not offered by this tool")
			case !found.offered(p):
				return nil, otherProduct(p, found.name)
			}
			checked, err := found.check(p, value)
			if err != nil {
				return nil, err
			}
			body[key] = checked
		}
		if len(body) == 0 {
			return nil, invalidArgument("pass at least one setting to change")
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "change repository"
			var changed object
			if err := c.change(ctx, op, http.MethodPatch, repositoryPath(repo), body, &changed); err != nil {
				return nil, err
			}
			if err := requireRepositoryAnswer(p, op, changed); err != nil {
				return nil, err
			}
			return repositoryChange(p, allowed, "updated", changed), nil
		}, nil
	},
}

// ---- fork ---------------------------------------------------------------------------------------------------

var repositoriesFork = &tool{
	group: "repositories", action: "fork", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Fork a " + p.name + " repository",
			Description: "Fork one repository inside the connection's targets into an organization the targets name " +
				"as orgs/NAME, or into the account behind the token when the targets name that user or none. The " +
				"instance forks asynchronously and answers at once. Not idempotent.",
			Tags:        []string{p.id, "repositories", "fork", "create"},
			Risk:        changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, p.id+repositorySensitivity),
			InputSchema: inputSchema(`"repository":` + repoSchema + `,"organization":` + organizationSchema + `,"name":` + repoNameSchema),
			OutputSchema: json.RawMessage(schemaOf(p, repositoryMembers, "id", "name",
				"full_name")),
			Arguments: []capability.Argument{repositoryArgument, organizationArgument,
				{Name: "name", Description: "Name of the fork; the source's name when left out"}},
			Fields: fieldsOf(p, repositoryMembers),
			Examples: []capability.Example{{Description: "Fork a repository into an organization",
				Arguments: json.RawMessage(`{"repository":"example/project","organization":"example-fork"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository   string `json:"repository"`
			Organization string `json:"organization"`
			Name         string `json:"name"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		if err := allowed.checkDestination(a.Organization); err != nil {
			return nil, err
		}
		body := map[string]any{}
		if a.Organization != "" {
			body["organization"] = a.Organization
		}
		if a.Name != "" {
			if err := repositoryNameCheck("name", a.Name); err != nil {
				return nil, err
			}
			body["name"] = a.Name
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "fork repository"
			if a.Organization == "" {
				if err := verifyOwnAccount(ctx, c, op, allowed); err != nil {
					return nil, err
				}
			}
			var forked object
			if err := c.change(ctx, op, http.MethodPost, repositoryPath(repo)+"/forks", body, &forked); err != nil {
				return nil, err
			}
			if err := requireRepositoryAnswer(p, op, forked); err != nil {
				return nil, err
			}
			return project(p, repositoryMembers, forked), nil
		}, nil
	},
}

var repositoriesListForks = &tool{
	group: "repositories", action: "list-forks", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the forks of a " + p.name + " repository",
			Description: "List one batch of the forks of a repository inside the connection's targets with every " +
				"member of each repository; forks outside the targets are left out",
			Tags:         []string{p.id, "repositories", "forks", "list"},
			Risk:         readRisk(p.id + repositorySensitivity),
			InputSchema:  inputSchema(`"repository":` + repoSchema + `,` + pagingKeys),
			OutputSchema: issueListOutputOf(p, "repositories", repositoryMembers),
			Arguments:    append([]capability.Argument{repositoryArgument}, pagingArguments...),
			Fields: append([]capability.Field{
				{Name: "repositories", Description: "Forks with every member of the repository object"},
				{Name: "repository", Description: "Repository whose forks the tool listed, as OWNER/REPO"},
			}, pagingFields...),
			Examples: []capability.Example{{Description: "List the forks of a repository",
				Arguments: json.RawMessage(`{"repository":"example/project","limit":20}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string `json:"repository"`
			Limit      int    `json:"limit"`
			Cursor     string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		page, err := startPaging(provider.CursorBinding(p.id, "repositories.list-forks",
			strings.ToLower(fullName(repo))), a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var rows []object
			header, err := c.get(ctx, "list forks", repositoryPath(repo)+"/forks", page.query(), &rows)
			if err != nil {
				return nil, err
			}
			forks := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				name, _ := row.text("full_name")
				if row != nil && allowed.allowsFullName(name) {
					forks = append(forks, project(p, repositoryMembers, row))
				}
			}
			next := page.next(header, len(rows))
			result := map[string]any{"repositories": forks, "has_more": next != "", "repository": fullName(repo)}
			if next != "" {
				result["next_cursor"] = next
			}
			return result, nil
		}, nil
	},
}

// ---- generate -----------------------------------------------------------------------------------------------

var generateFlags = []struct{ name, note string }{
	{"avatar", "Include the avatar of the template"},
	{"git_content", "Include the Git content of the default branch"},
	{"git_hooks", "Include the Git hooks"},
	{"labels", "Include the labels"},
	{"protected_branch", "Include the protected branches"},
	{"topics", "Include the topics"},
	{"webhooks", "Include the webhooks"},
}

var repositoriesGenerate = &tool{
	group: "repositories", action: "generate", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties := `"template":` + repoSchema + `,"owner":` + ownerSchema + `,"name":` + repoNameSchema +
			`,"description":{"type":"string","maxLength":2048},"private":{"type":"boolean"},"default_branch":` +
			branchSchemaName
		arguments := []capability.Argument{{Name: "template", Required: true,
			Description: "Template repository as OWNER/REPO; it must lie inside the targets"},
			{Name: "owner", Required: true, Description: "Owner of the new repository as " +
				"users/NAME or orgs/NAME; the connection's targets must name it"},
			{Name: "name", Required: true, Description: "Name of the new repository"},
			{Name: "description", Description: "Description, at most 2048 characters"},
			{Name: "private", Description: "True for a private repository"},
			{Name: "default_branch", Description: "Default branch of the new repository"}}
		for _, flag := range generateFlags {
			properties += `,"` + flag.name + `":{"type":"boolean"}`
			arguments = append(arguments, capability.Argument{Name: flag.name, Description: flag.note})
		}
		return capability.Descriptor{
			Title: "Create a " + p.name + " repository from a template",
			Description: "Create one repository from a template repository inside the connection's targets, owned " +
				"by an owner the targets name. Not idempotent.",
			Tags:         []string{p.id, "repositories", "generate", "create"},
			Risk:         changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, p.id+repositorySensitivity),
			InputSchema:  inputSchema(properties, "template", "owner", "name"),
			OutputSchema: json.RawMessage(schemaOf(p, repositoryMembers, "id", "name", "full_name")),
			Arguments:    arguments,
			Fields:       fieldsOf(p, repositoryMembers),
			Examples: []capability.Example{{Description: "Generate a repository from a template",
				Arguments: json.RawMessage(`{"template":"example/starter","owner":"orgs/example","name":"project","git_content":true}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var given map[string]json.RawMessage
		if len(raw) == 0 {
			raw = json.RawMessage(`{}`)
		}
		if json.Unmarshal(raw, &given) != nil {
			return nil, invalidArgument("the arguments could not be read")
		}
		var a struct {
			Template      string  `json:"template"`
			Owner         string  `json:"owner"`
			Name          string  `json:"name"`
			Description   *string `json:"description"`
			Private       *bool   `json:"private"`
			DefaultBranch string  `json:"default_branch"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if a.Template == "" {
			return nil, invalidArgument("template is required")
		}
		template, err := allowed.chooseRepository(a.Template)
		if err != nil {
			return nil, err
		}
		owner, err := parseOwnerArgument(a.Owner)
		if err != nil {
			return nil, err
		}
		if !allowed.namesActionsOwner(owner) {
			return nil, outsideTargets("owner")
		}
		if err := repositoryNameCheck("name", a.Name); err != nil {
			return nil, err
		}
		body := map[string]any{"owner": owner.owner, "name": a.Name}
		if a.Description != nil {
			if err := checkOneLine("description", *a.Description, maxRepoDescription); err != nil {
				return nil, err
			}
			body["description"] = *a.Description
		}
		if a.Private != nil {
			body["private"] = *a.Private
		}
		if a.DefaultBranch != "" {
			if err := checkRefName("default_branch", a.DefaultBranch, true); err != nil {
				return nil, err
			}
			body["default_branch"] = a.DefaultBranch
		}
		for _, flag := range generateFlags {
			if value, ok := given[flag.name]; ok {
				var set *bool
				if json.Unmarshal(value, &set) != nil || set == nil {
					return nil, invalidArgument(flag.name + " must be true or false")
				}
				body[flag.name] = *set
			}
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "generate repository"
			var created object
			if err := c.change(ctx, op, http.MethodPost, repositoryPath(template)+"/generate", body,
				&created); err != nil {
				return nil, err
			}
			if err := requireRepositoryAnswer(p, op, created); err != nil {
				return nil, err
			}
			return project(p, repositoryMembers, created), nil
		}, nil
	},
}

// ---- fork synchronization -----------------------------------------------------------------------------------

var syncBranchArgument = capability.Argument{Name: "branch", Description: "Branch of the fork; the default " +
	"branch when left out; a slash is allowed and sent encoded"}

func syncPath(repo target, branch string) string {
	path := repositoryPath(repo) + "/sync_fork"
	if branch != "" {
		path += "/" + escape(branch)
	}
	return path
}

var repositoriesSyncFork = &tool{
	group: "repositories", action: "sync-fork", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		properties := `"repository":` + repoSchema + `,"branch":` + branchSchemaName
		arguments := []capability.Argument{repositoryArgument, syncBranchArgument}
		fields := []capability.Field{{Name: "repository", Description: "Repository that was synced, as OWNER/REPO"},
			{Name: "synced", Description: "True once the instance synced the fork"}}
		output := `"synced":{"type":"boolean"},"repository":{"type":"string"}`
		description := "Bring a fork inside the connection's targets up to date with its upstream branch, with " +
			"one request."
		if p == gitea {
			properties += `,"ff_only":{"type":"boolean"}`
			arguments = append(arguments, capability.Argument{Name: "ff_only", Description: "Gitea only: " +
				"true to refuse anything but a fast-forward"})
			fields = append(fields, capability.Field{Name: "merge_type", Description: "Gitea only: how the " +
				"instance brought the branch up to date"})
			output += `,"merge_type":{"type":"string"}`
			description += " Gitea merges the upstream branch (merge-upstream)."
		}
		return capability.Descriptor{
			Title:       "Sync a " + p.name + " fork with its upstream",
			Description: description,
			Tags:        []string{p.id, "repositories", "fork", "sync", "update"},
			Risk:        changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+repositorySensitivity),
			InputSchema: inputSchema(properties),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{` + output +
				`},"required":["synced","repository"],"additionalProperties":false}`),
			Arguments: arguments, Fields: fields,
			Examples: []capability.Example{{Description: "Sync the default branch of a fork",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string `json:"repository"`
			Branch     string `json:"branch"`
			FFOnly     *bool  `json:"ff_only"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		if a.Branch != "" {
			if err := checkRefName("branch", a.Branch, true); err != nil {
				return nil, err
			}
		}
		if a.FFOnly != nil && p != gitea {
			return nil, otherProduct(p, "ff_only")
		}
		// Forgejo names the branch in the path, Gitea in the body of merge-upstream.
		path, body := syncPath(repo, a.Branch), any(nil)
		if p == gitea {
			path = repositoryPath(repo) + "/merge-upstream"
			fields := map[string]any{}
			if a.Branch != "" {
				fields["branch"] = a.Branch
			}
			if a.FFOnly != nil {
				fields["ff_only"] = *a.FFOnly
			}
			body = fields
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "sync fork"
			result := map[string]any{"synced": true, "repository": fullName(repo)}
			if p == forgejo {
				if err := c.change(ctx, op, http.MethodPost, path, body, nil); err != nil {
					return nil, err
				}
				return result, nil
			}
			var answer object
			if err := c.change(ctx, op, http.MethodPost, path, body, &answer); err != nil {
				return nil, err
			}
			if mergeType, ok := answer.text("merge_type"); ok {
				result["merge_type"] = mergeType
			}
			return result, nil
		}, nil
	},
}

var syncStateMembers = []member{
	{name: "allowed", kind: kindFlag, note: "True when the fork can be synced"},
	{name: "base_commit", kind: kindText, note: "Commit of the upstream branch"},
	{name: "commits_behind", kind: kindInteger, note: "Number of commits the fork is behind"},
	{name: "fork_commit", kind: kindText, note: "Commit of the fork's branch"},
}

var repositoriesGetSyncState = &tool{
	group: "repositories", action: "get-sync-state", since: forgejoOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get the sync state of a " + p.name + " fork",
			Description: "Read how far a fork inside the connection's targets is behind its upstream branch, for " +
				"its default branch or one branch",
			Tags:         []string{p.id, "repositories", "fork", "sync", "get"},
			Risk:         readRisk(p.id + repositorySensitivity),
			InputSchema:  inputSchema(`"repository":` + repoSchema + `,"branch":` + branchSchemaName),
			OutputSchema: json.RawMessage(schemaOf(p, syncStateMembers)),
			Arguments:    []capability.Argument{repositoryArgument, syncBranchArgument},
			Fields:       fieldsOf(p, syncStateMembers),
			Examples: []capability.Example{{Description: "Read the sync state",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string `json:"repository"`
			Branch     string `json:"branch"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		if a.Branch != "" {
			if err := checkRefName("branch", a.Branch, true); err != nil {
				return nil, err
			}
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var state object
			if _, err := c.get(ctx, "get fork sync state", syncPath(repo, a.Branch), nil, &state); err != nil {
				return nil, err
			}
			return project(p, syncStateMembers, state), nil
		}, nil
	},
}

// ---- fixed single-request actions ---------------------------------------------------------------------------

// repositoryAction is a change of one repository with one fixed method and path suffix and no body.
type repositoryAction struct {
	group, action, title, description string
	since                             map[string]version
	listed                            bool
	effect                            capability.Effect
	idempotency                       capability.Idempotency
	suffix, method, op                string
	tags                              []string
	// key names the boolean of the answer; direct marks an answer that is the repository itself.
	key    string
	direct bool
	// moves marks an answer that may lie outside the targets.
	moves bool
}

func newRepositoryAction(s repositoryAction) *tool {
	return &tool{
		group: s.group, action: s.action, since: s.since,
		describe: func(p *product) capability.Descriptor {
			d := capability.Descriptor{
				Title:                 strings.ReplaceAll(s.title, "%s", p.name),
				Description:           s.description,
				Tags:                  append([]string{p.id}, s.tags...),
				Risk:                  changeRisk(s.effect, s.idempotency, p.id+repositorySensitivity),
				RequiresToolAllowList: s.listed,
				InputSchema:           inputSchema(`"repository":` + repoSchema),
				Arguments:             []capability.Argument{repositoryArgument},
				Examples: []capability.Example{{Description: s.title,
					Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
			}
			if s.listed {
				d.Description += " Only a connection whose tools list names it offers this tool."
			}
			switch {
			case s.direct:
				d.OutputSchema = json.RawMessage(schemaOf(p, repositoryMembers, "id", "name", "full_name"))
				d.Fields = fieldsOf(p, repositoryMembers)
			case s.moves:
				d.OutputSchema = changeOutput(p, s.key)
				d.Fields = changeFields(p, s.key, "applied the change")
			default:
				d.OutputSchema = json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},"` +
					s.key + `":{"type":"boolean"}},"required":["repository","` + s.key + `"],"additionalProperties":false}`)
				d.Fields = []capability.Field{{Name: "repository", Description: "Repository, as OWNER/REPO"},
					{Name: s.key, Description: "True once the instance accepted the request"}}
			}
			return d
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a struct {
				Repository string `json:"repository"`
			}
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			repo, err := allowed.chooseRepository(a.Repository)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				var answer object
				var out any
				if s.direct || s.moves {
					out = &answer
				}
				if err := c.change(ctx, s.op, s.method, repositoryPath(repo)+s.suffix, nil, out); err != nil {
					return nil, err
				}
				switch {
				case s.direct:
					if err := requireRepositoryAnswer(p, s.op, answer); err != nil {
						return nil, err
					}
					return project(p, repositoryMembers, answer), nil
				case s.moves:
					if err := requireRepositoryAnswer(p, s.op, answer); err != nil {
						return nil, err
					}
					return repositoryChange(p, allowed, s.key, answer), nil
				}
				return map[string]any{"repository": fullName(repo), s.key: true}, nil
			}, nil
		},
	}
}

var repositoriesConvert = newRepositoryAction(repositoryAction{
	group: "repositories", action: "convert", since: forgejoOnly, listed: true,
	title:       "Convert a %s mirror into a normal repository",
	description: "Turn one pull mirror inside the connection's targets into a normal repository; the mirror link is gone for good.",
	effect:      capability.EffectUpdate, idempotency: capability.IdempotencyNonIdempotent,
	suffix: "/convert", method: http.MethodPost, op: "convert repository", direct: true,
	tags: []string{"repositories", "convert", "update"},
})

var repositoriesDelete = newRepositoryAction(repositoryAction{
	group: "repositories", action: "delete", since: everywhere, listed: true,
	title:       "Delete a %s repository",
	description: "Delete one repository inside the connection's targets for good, with its issues, pull requests, wiki, and releases.",
	effect:      capability.EffectDelete, idempotency: capability.IdempotencyIdempotent,
	method: http.MethodDelete, op: "delete repository", key: "deleted",
	tags: []string{"repositories", "delete"},
})

var repositoriesAcceptTransfer = newRepositoryAction(repositoryAction{
	group: "repositories", action: "accept-transfer", since: everywhere, listed: true,
	title: "Accept the transfer of a %s repository",
	description: "Accept a pending transfer of one repository inside the connection's targets to the account behind the " +
		"token." + repositoryChangeNote,
	effect: capability.EffectUpdate, idempotency: capability.IdempotencyNonIdempotent,
	suffix: "/transfer/accept", method: http.MethodPost, op: "accept repository transfer", key: "accepted", moves: true,
	tags: []string{"repositories", "transfer", "accept", "update"},
})

var repositoriesRejectTransfer = newRepositoryAction(repositoryAction{
	group: "repositories", action: "reject-transfer", since: everywhere, listed: true,
	title: "Reject the transfer of a %s repository",
	description: "Reject a pending transfer of one repository inside the connection's targets to the account behind the " +
		"token." + repositoryChangeNote,
	effect: capability.EffectUpdate, idempotency: capability.IdempotencyNonIdempotent,
	suffix: "/transfer/reject", method: http.MethodPost, op: "reject repository transfer", key: "rejected", moves: true,
	tags: []string{"repositories", "transfer", "reject", "update"},
})

// ---- transfer -----------------------------------------------------------------------------------------------

var repositoriesTransfer = &tool{
	group: "repositories", action: "transfer", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Transfer a " + p.name + " repository",
			Description: "Start the transfer of one repository inside the connection's targets to a new owner the " +
				"targets name. A user must accept it; an organization may take it at once. The answer shows the " +
				"repository only while the targets still reach it. Only a connection whose tools list names it " +
				"offers this tool.",
			Tags:                  []string{p.id, "repositories", "transfer", "update"},
			Risk:                  changeRisk(capability.EffectUpdate, capability.IdempotencyNonIdempotent, p.id+repositorySensitivity),
			RequiresToolAllowList: true,
			InputSchema: inputSchema(`"repository":`+repoSchema+`,"new_owner":`+ownerSchema+
				`,"team_ids":{"type":"array","maxItems":50,"items":{"type":"integer","minimum":1,"maximum":9007199254740991}}`,
				"new_owner"),
			OutputSchema: changeOutput(p, "transferred"),
			Arguments: []capability.Argument{repositoryArgument,
				{Name: "new_owner", Required: true, Description: "New owner as users/NAME or " +
					"orgs/NAME; the connection's targets must name it"},
				{Name: "team_ids", Description: "IDs of teams of a new organization owner that get the repository, " +
					"at most 50"}},
			Fields: changeFields(p, "transferred", "started the transfer"),
			Examples: []capability.Example{{Description: "Transfer a repository to an organization",
				Arguments: json.RawMessage(`{"repository":"example/project","new_owner":"orgs/example-org"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string  `json:"repository"`
			NewOwner   string  `json:"new_owner"`
			TeamIDs    []int64 `json:"team_ids"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		owner, err := parseOwnerArgument(a.NewOwner)
		if err != nil {
			return nil, err
		}
		if !allowed.namesActionsOwner(owner) {
			return nil, outsideTargets("new_owner")
		}
		body := map[string]any{"new_owner": owner.owner}
		if len(a.TeamIDs) > 0 {
			if len(a.TeamIDs) > maxTeamIDs {
				return nil, invalidArgument("team_ids takes at most 50 IDs")
			}
			for _, id := range a.TeamIDs {
				if id < 1 {
					return nil, invalidArgument("team_ids takes IDs of teams")
				}
			}
			body["team_ids"] = a.TeamIDs
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "transfer repository"
			var moved object
			if err := c.change(ctx, op, http.MethodPost, repositoryPath(repo)+"/transfer", body, &moved); err != nil {
				return nil, err
			}
			if err := requireRepositoryAnswer(p, op, moved); err != nil {
				return nil, err
			}
			return repositoryChange(p, allowed, "transferred", moved), nil
		}, nil
	},
}
