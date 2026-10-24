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

// The package tools list, read, link to a repository, unlink, and delete the packages of one owner, a user or an
// organization. A package belongs to its owner, so every tool needs the owner as an owner target of the same
// scope (users/NAME or orgs/NAME); a repository target does not release an owner. The owner in the path binds
// the package, and a row or answer of another owner is dropped or refused. Package contents travel through the
// registry protocols, not this API, and are not offered. The names of packages, versions, and files are
// untrusted: a name may hold a slash (a scope, a container path), so each one is a single escaped path segment.

const (
	packageSensitivity = "-packages"
	maxPackageSegment  = 255
	maxPackageQuery    = 100
	maxPackageFiles    = 200
)

// packageTypes are the package types the two products document; Gitea alone knows terraform.
var packageTypes = []string{"alpine", "cargo", "chef", "composer", "conan", "conda", "container", "cran", "debian",
	"generic", "go", "helm", "maven", "npm", "nuget", "pub", "pypi", "rpm", "rubygems", "swift", "vagrant"}

func packageTypesOf(p *product) []string {
	if p == gitea {
		return append(append([]string(nil), packageTypes...), "terraform")
	}
	return packageTypes
}

func packageTypeSchema(p *product) string {
	return `{"type":"string","enum":["` + strings.Join(packageTypesOf(p), `","`) + `"]}`
}

const (
	packageSegmentSchema = `{"type":"string","minLength":1,"maxLength":255}`
	packageQuerySchema   = `{"type":"string","minLength":1,"maxLength":100}`
	packageRepoSchema    = `{"type":"string","minLength":1,"maxLength":100,"pattern":"^[A-Za-z0-9_.-]+$"}`
	packageListedNote    = " Only a connection whose tools list names it offers this tool."
)

var packageMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the package version"},
	{name: "name", kind: kindText, note: "Name of the package, untrusted data"},
	{name: "type", kind: kindText, note: "Type of the package, such as npm or container"},
	{name: "version", kind: kindText, note: "Version of the package, untrusted data"},
	{name: "html_url", kind: kindText, address: true, note: "Address of the package page; left out when it carries " +
		"user info or a query"},
	{name: "created_at", kind: kindText, note: "Creation time"},
	{name: "creator", kind: kindObject, sub: personMembers, note: "User who published it; personal data"},
	{name: "owner", kind: kindObject, sub: personMembers, note: "Owner of the package; personal data"},
	{name: "repository", kind: kindObject, sub: repositoryMembers, note: "Repository the package is linked to, " +
		"absent when it is linked to none"},
}

// packageFileMembers lists the shown members; Forgejo reports the size as Size and Gitea as size, and both
// leave as size.
var packageFileMembers = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the file"},
	{name: "name", kind: kindText, note: "File name, untrusted data"},
	{name: "size", kind: kindInteger, note: "Size in bytes"},
	{name: "md5", kind: kindText, note: "MD5 hash"},
	{name: "sha1", kind: kindText, note: "SHA-1 hash"},
	{name: "sha256", kind: kindText, note: "SHA-256 hash"},
	{name: "sha512", kind: kindText, note: "SHA-512 hash"},
}

func packageFile(p *product, row object) map[string]any {
	out := project(p, packageFileMembers, row)
	delete(out, "size")
	key := "size"
	if p == forgejo {
		key = "Size"
	}
	if size, ok := row.integer(key); ok {
		out["size"] = size
	}
	return out
}

var (
	packageOwnerArgument = capability.Argument{Name: "owner", Target: kindOwner,
		Description: "Owner of the package as users/NAME or orgs/NAME, always needed; a connection with targets must name that " +
			"owner as an owner target of the same scope, and a repository target does not allow it"}
	packageNameArgument = capability.Argument{Name: "name", Required: true,
		Description: "Package name as the instance reports it; it may hold a slash"}
	packageVersionArgument = capability.Argument{Name: "version", Required: true,
		Description: "Package version as the instance reports it"}
	packageTypeArgument = capability.Argument{Name: "type", Required: true,
		Description: "Package type; Gitea alone knows terraform"}
)

// packageArguments are the arguments of every package tool; the schema of a tool allows only its own.
type packageArguments struct {
	Owner    string `json:"owner"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	RepoName string `json:"repo_name"`
	Query    string `json:"q"`
	Limit    int    `json:"limit"`
	Cursor   string `json:"cursor"`
}

// packageCall is what a call acts on after the checks: the owner as a target, and the fixed paths.
type packageCall struct {
	packageArguments
	owner target
}

func (c packageCall) ownerPath() string { return "/packages/" + escape(c.owner.owner) }
func (c packageCall) namePath() string {
	return c.ownerPath() + "/" + c.Type + "/" + escape(c.Name)
}
func (c packageCall) versionPath() string { return c.namePath() + "/" + escape(c.Version) }

// checkPackageSegment accepts a name or version as one path segment: no control characters, and no part between
// slashes that is a dot segment or the "-" that marks the fixed parts of a path, since an instance or proxy that
// decodes %2F before routing would otherwise reach another route.
func checkPackageSegment(field, value string) error {
	refused := value == "" || utf8.RuneCountInString(value) > maxPackageSegment || !plainText(value)
	for _, part := range strings.Split(value, "/") {
		refused = refused || part == "." || part == ".." || part == "-"
	}
	if refused {
		return invalidArgument(field + " must hold 1 to 255 characters without control characters, and no part " +
			"between slashes may be a dot segment or a single hyphen")
	}
	return nil
}

// readPackage decodes the arguments and checks them against the targets and the product; a refusal comes
// before the token and before any request. The flags name the arguments the tool reads.
func readPackage(p *product, allowed allowlist, raw json.RawMessage, needType, needName, needVersion, needRepo bool) (packageCall, error) {
	var a packageArguments
	if err := decodeArguments(raw, &a); err != nil {
		return packageCall{}, err
	}
	owner, err := parseOwnerArgument(a.Owner)
	if err != nil {
		return packageCall{}, err
	}
	if !allowed.namesOwner(owner) {
		return packageCall{}, outsideTargets("owner")
	}
	if needType || a.Type != "" {
		known := false
		for _, t := range packageTypesOf(p) {
			known = known || t == a.Type
		}
		if !known {
			return packageCall{}, invalidArgument("type must be a package type of " + p.name)
		}
	}
	if needName {
		if err := checkPackageSegment("name", a.Name); err != nil {
			return packageCall{}, err
		}
	}
	if needVersion {
		if err := checkPackageSegment("version", a.Version); err != nil {
			return packageCall{}, err
		}
	}
	if needRepo {
		if !validRepoName(a.RepoName) {
			return packageCall{}, invalidArgument("repo_name must be a repository name")
		}
		if !allowed.allowsRepository(owner.owner, a.RepoName) {
			return packageCall{}, outsideTargets("repository")
		}
	}
	if a.Query != "" && (utf8.RuneCountInString(a.Query) > maxPackageQuery || !plainText(a.Query)) {
		return packageCall{}, invalidArgument("q must hold at most 100 characters without control characters")
	}
	return packageCall{packageArguments: a, owner: owner}, nil
}

// ownedBy reports whether a package row names the owner of the call; a row without an owner proves nothing.
func (c packageCall) ownedBy(row object) bool {
	owner, ok := row.child("owner")
	if !ok {
		return false
	}
	login, ok := owner.text("login")
	return ok && strings.EqualFold(login, c.owner.owner)
}

func packageRows(p *product, c packageCall, rows []object) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if row != nil && c.ownedBy(row) {
			out = append(out, project(p, packageMembers, row))
		}
	}
	return out
}

func packageListOutput(p *product, extra string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"owner":{"type":"string"},` + extra +
		`"packages":{"type":"array","items":` + schemaOf(p, packageMembers, "id") + `},"total_count":{"type":"integer"},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["owner","packages","has_more"],"additionalProperties":false}`)
}

func packageListFields(what string) []capability.Field {
	return append([]capability.Field{
		{Name: "owner", Description: "Owner the tool listed, as users/NAME or orgs/NAME"},
		{Name: "packages", Description: what},
		{Name: "total_count", Description: "Number of entries the instance counts, where it reports one"},
	}, pagingFields...)
}

func packageListResult(c packageCall, rows []map[string]any, header http.Header, next string) map[string]any {
	result := map[string]any{"owner": c.owner.String(), "packages": rows, "has_more": next != ""}
	if total := totalCount(header); total != nil {
		result["total_count"] = *total
	}
	if next != "" {
		result["next_cursor"] = next
	}
	return result
}

// ---- reads --------------------------------------------------------------------------------------------------

var packagesList = &tool{
	group: "packages", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List " + p.name + " packages of an owner",
			Description: "List one batch of the packages of a user or an organization inside the connection's " +
				"targets, one row per package version, with every member of the package object; narrow by type or " +
				"a search text. Rows of another owner are left out. Names and versions are untrusted data.",
			Tags:         []string{p.id, "packages", "list"},
			Risk:         readRisk(p.id + packageSensitivity),
			InputSchema:  inputSchema(`"owner":` + ownerSchema + `,"type":` + packageTypeSchema(p) + `,"q":` + packageQuerySchema + `,` + pagingKeys),
			OutputSchema: packageListOutput(p, ""),
			Arguments: append([]capability.Argument{packageOwnerArgument,
				{Name: "type", Description: "Only packages of this type; Gitea alone knows terraform"},
				{Name: "q", Description: "Only packages whose name matches this text"}}, pagingArguments...),
			Fields: packageListFields("Package versions with every member of the package object"),
			Examples: []capability.Example{{Description: "List the npm packages of an organization",
				Arguments: json.RawMessage(`{"owner":"orgs/example","type":"npm"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		c, err := readPackage(p, allowed, raw, false, false, false, false)
		if err != nil {
			return nil, err
		}
		page, err := startPaging(provider.CursorBinding(p.id, "packages.list", c.owner.String(), c.Type, c.Query),
			c.Limit, c.Cursor)
		if err != nil {
			return nil, err
		}
		query := page.query()
		if c.Type != "" {
			query.Set("type", c.Type)
		}
		if c.Query != "" {
			query.Set("q", c.Query)
		}
		return func(ctx context.Context, cl *Client) (any, error) {
			var rows []object
			header, err := cl.get(ctx, "list packages", c.ownerPath(), query, &rows)
			if err != nil {
				return nil, err
			}
			return packageListResult(c, packageRows(p, c, rows), header, page.next(header, len(rows))), nil
		}, nil
	},
}

// packageVersionRow reads the one package version an answer holds and refuses another owner's.
func packageVersionRow(p *product, cl *Client, op string, c packageCall, row object) (map[string]any, error) {
	if row == nil || !c.ownedBy(row) {
		return nil, notShown(op, cl.product)
	}
	return project(p, packageMembers, row), nil
}

var packagesGet = &tool{
	group: "packages", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a " + p.name + " package version",
			Description: "Read one package version of a user or an organization inside the connection's targets with " +
				"every member of the package object; names and versions are untrusted data. The contents of the " +
				"package are not offered.",
			Tags:         []string{p.id, "packages", "get"},
			Risk:         readRisk(p.id + packageSensitivity),
			InputSchema:  inputSchema(packageProperties(p, true), "type", "name", "version"),
			OutputSchema: json.RawMessage(schemaOf(p, packageMembers, "id")),
			Arguments: []capability.Argument{packageOwnerArgument, packageTypeArgument, packageNameArgument,
				packageVersionArgument},
			Fields: fieldsOf(p, packageMembers),
			Examples: []capability.Example{{Description: "Read a package version",
				Arguments: json.RawMessage(`{"owner":"orgs/example","type":"npm","name":"example-package","version":"1.0.0"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		c, err := readPackage(p, allowed, raw, true, true, true, false)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, cl *Client) (any, error) {
			const op = "get package"
			var row object
			if _, err := cl.get(ctx, op, c.versionPath(), nil, &row); err != nil {
				return nil, err
			}
			return packageVersionRow(p, cl, op, c, row)
		}, nil
	},
}

// packageProperties is the input schema of a tool that names a package; withVersion adds the version.
func packageProperties(p *product, withVersion bool) string {
	properties := `"owner":` + ownerSchema + `,"type":` + packageTypeSchema(p) + `,"name":` + packageSegmentSchema
	if withVersion {
		properties += `,"version":` + packageSegmentSchema
	}
	return properties
}

var packageFilesList = &tool{
	group: "package-files", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the files of a " + p.name + " package version",
			Description: "List the files of one package version of a user or an organization inside the connection's " +
				"targets, with every member of the file object; Forgejo's Size is reported as size. At most 200 " +
				"files are shown. File names are untrusted data. The contents of the files are not offered.",
			Tags:        []string{p.id, "packages", "files", "list"},
			Risk:        readRisk(p.id + packageSensitivity),
			InputSchema: inputSchema(packageProperties(p, true), "type", "name", "version"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"owner":{"type":"string"},"files":` +
				`{"type":"array","items":` + schemaOf(p, packageFileMembers, "id") + `},"has_more":{"type":"boolean"}},` +
				`"required":["owner","files","has_more"],"additionalProperties":false}`),
			Arguments: []capability.Argument{packageOwnerArgument, packageTypeArgument, packageNameArgument,
				packageVersionArgument},
			Fields: []capability.Field{{Name: "owner", Description: "Owner the tool read, as users/NAME or orgs/NAME"},
				{Name: "files", Description: "Files with every member of the file object"},
				{Name: "has_more", Description: "True when the instance answered more files than are shown"}},
			Examples: []capability.Example{{Description: "List the files of a package version",
				Arguments: json.RawMessage(`{"owner":"orgs/example","type":"npm","name":"example-package","version":"1.0.0"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		c, err := readPackage(p, allowed, raw, true, true, true, false)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, cl *Client) (any, error) {
			var rows []object
			if _, err := cl.get(ctx, "list package files", c.versionPath()+"/files", nil, &rows); err != nil {
				return nil, err
			}
			files := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil && len(files) < maxPackageFiles {
					files = append(files, packageFile(p, row))
				}
			}
			return map[string]any{"owner": c.owner.String(), "files": files, "has_more": len(rows) > maxPackageFiles}, nil
		}, nil
	},
}

var packageVersionsList = &tool{
	group: "package-versions", action: "list", since: giteaOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the versions of a " + p.name + " package",
			Description: "List one batch of the versions of one package of a user or an organization inside the " +
				"connection's targets, with every member of the package object. Rows of another owner are left out. " +
				"Names and versions are untrusted data.",
			Tags:         []string{p.id, "packages", "versions", "list"},
			Risk:         readRisk(p.id + packageSensitivity),
			InputSchema:  inputSchema(packageProperties(p, false)+`,`+pagingKeys, "type", "name"),
			OutputSchema: packageListOutput(p, ""),
			Arguments: append([]capability.Argument{packageOwnerArgument, packageTypeArgument, packageNameArgument},
				pagingArguments...),
			Fields: packageListFields("Package versions with every member of the package object"),
			Examples: []capability.Example{{Description: "List the versions of a package",
				Arguments: json.RawMessage(`{"owner":"orgs/example","type":"npm","name":"example-package"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		c, err := readPackage(p, allowed, raw, true, true, false, false)
		if err != nil {
			return nil, err
		}
		page, err := startPaging(provider.CursorBinding(p.id, "package-versions.list", c.owner.String(), c.Type,
			c.Name), c.Limit, c.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, cl *Client) (any, error) {
			var rows []object
			header, err := cl.get(ctx, "list package versions", c.namePath(), page.query(), &rows)
			if err != nil {
				return nil, err
			}
			return packageListResult(c, packageRows(p, c, rows), header, page.next(header, len(rows))), nil
		}, nil
	},
}

var packageVersionsGetLatest = &tool{
	group: "package-versions", action: "get-latest", since: giteaOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get the latest version of a " + p.name + " package",
			Description: "Read the latest version of one package of a user or an organization inside the " +
				"connection's targets with every member of the package object; names and versions are untrusted data.",
			Tags:         []string{p.id, "packages", "versions", "get"},
			Risk:         readRisk(p.id + packageSensitivity),
			InputSchema:  inputSchema(packageProperties(p, false), "type", "name"),
			OutputSchema: json.RawMessage(schemaOf(p, packageMembers, "id")),
			Arguments:    []capability.Argument{packageOwnerArgument, packageTypeArgument, packageNameArgument},
			Fields:       fieldsOf(p, packageMembers),
			Examples: []capability.Example{{Description: "Read the latest version of a package",
				Arguments: json.RawMessage(`{"owner":"orgs/example","type":"npm","name":"example-package"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		c, err := readPackage(p, allowed, raw, true, true, false, false)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, cl *Client) (any, error) {
			const op = "get latest package version"
			var row object
			if _, err := cl.get(ctx, op, c.namePath()+"/-/latest", nil, &row); err != nil {
				return nil, err
			}
			return packageVersionRow(p, cl, op, c, row)
		}, nil
	},
}

// ---- changes ------------------------------------------------------------------------------------------------

// packageChange is a change of one package with one fixed method and path and no body.
type packageChange struct {
	group, action, title, text string
	since                      map[string]version
	effect                     capability.Effect
	listed, withVersion        bool
	repo                       bool
	suffix, op, key            string
	method                     string
	tags                       []string
}

func newPackageChange(s packageChange) *tool {
	return &tool{
		group: s.group, action: s.action, since: s.since,
		describe: func(p *product) capability.Descriptor {
			text := s.text
			if s.listed {
				text += packageListedNote
			}
			properties := packageProperties(p, s.withVersion)
			required := []string{"type", "name"}
			arguments := []capability.Argument{packageOwnerArgument, packageTypeArgument, packageNameArgument}
			example := `{"owner":"orgs/example","type":"npm","name":"example-package"`
			output := `"owner":{"type":"string"},"type":{"type":"string"},"name":{"type":"string"},`
			fields := []capability.Field{{Name: "owner", Description: "Owner the tool acted on"},
				{Name: "type", Description: "Package type"}, {Name: "name", Description: "Package name"}}
			if s.withVersion {
				required = append(required, "version")
				arguments = append(arguments, packageVersionArgument)
				example += `,"version":"1.0.0"`
				output += `"version":{"type":"string"},`
				fields = append(fields, capability.Field{Name: "version", Description: "Package version"})
			}
			if s.repo {
				properties += `,"repo_name":` + packageRepoSchema
				required = append(required, "repo_name")
				arguments = append(arguments, capability.Argument{Name: "repo_name", Required: true,
					Description: "Name of a repository of the same owner that receives the link; the connection's " +
						"targets must allow OWNER/repo_name"})
				example += `,"repo_name":"project"`
				output += `"repo_name":{"type":"string"},`
				fields = append(fields, capability.Field{Name: "repo_name", Description: "Repository that was linked"})
			}
			fields = append(fields, capability.Field{Name: s.key, Description: "True once the instance accepted the request"})
			return capability.Descriptor{
				Title:                 strings.ReplaceAll(s.title, "%s", p.name),
				Description:           text,
				Tags:                  append([]string{p.id}, s.tags...),
				Risk:                  changeRisk(s.effect, capability.IdempotencyIdempotent, p.id+packageSensitivity),
				RequiresToolAllowList: s.listed,
				InputSchema:           inputSchema(properties, required...),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{` + output + `"` + s.key +
					`":{"type":"boolean"}},"required":["owner","type","name","` + s.key + `"],"additionalProperties":false}`),
				Arguments: arguments,
				Fields:    fields,
				Examples: []capability.Example{{Description: strings.ReplaceAll(s.title, "%s", p.name),
					Arguments: json.RawMessage(example + `}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			c, err := readPackage(p, allowed, raw, true, true, s.withVersion, s.repo)
			if err != nil {
				return nil, err
			}
			path := c.namePath()
			if s.withVersion {
				path = c.versionPath()
			}
			path += s.suffix
			if s.repo {
				path += escape(c.RepoName)
			}
			return func(ctx context.Context, cl *Client) (any, error) {
				if err := cl.change(ctx, s.op, s.method, path, nil, nil); err != nil {
					return nil, err
				}
				result := map[string]any{"owner": c.owner.String(), "type": c.Type, "name": c.Name, s.key: true}
				if s.withVersion {
					result["version"] = c.Version
				}
				if s.repo {
					result["repo_name"] = c.RepoName
				}
				return result, nil
			}, nil
		},
	}
}

var packagesLink = newPackageChange(packageChange{
	group: "packages", action: "link", since: everywhere, effect: capability.EffectUpdate, repo: true,
	method: http.MethodPost, suffix: "/-/link/", op: "link package", key: "linked", tags: []string{"packages", "link", "update"},
	title: "Link a %s package to a repository",
	text: "Link a package of a user or an organization inside the connection's targets, all its versions, to a " +
		"repository of the same owner. The change is the one request.",
})

var packagesUnlink = newPackageChange(packageChange{
	group: "packages", action: "unlink", since: everywhere, effect: capability.EffectUpdate,
	method: http.MethodPost, suffix: "/-/unlink", op: "unlink package", key: "unlinked",
	tags:  []string{"packages", "unlink", "update"},
	title: "Unlink a %s package from its repository",
	text: "Remove the link of a package of a user or an organization inside the connection's targets to its " +
		"repository. The package stays. The change is the one request.",
})

var packagesDeleteVersion = newPackageChange(packageChange{
	group: "packages", action: "delete-version", since: everywhere, effect: capability.EffectDelete, listed: true,
	withVersion: true, method: http.MethodDelete, op: "delete package version", key: "deleted",
	tags:  []string{"packages", "delete"},
	title: "Delete a version of a %s package",
	text: "Delete one version of a package of a user or an organization inside the connection's targets for good, " +
		"with its files. The change is the one request.",
})

var packagesDelete = newPackageChange(packageChange{
	group: "packages", action: "delete", since: giteaOnly, effect: capability.EffectDelete, listed: true,
	method: http.MethodDelete, op: "delete package", key: "deleted", tags: []string{"packages", "delete"},
	title: "Delete a %s package with all its versions",
	text: "Delete a package of a user or an organization inside the connection's targets for good, all its versions " +
		"and files. The change is the one request.",
})
