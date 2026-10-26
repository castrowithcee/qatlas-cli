package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The quota tools read the quota of the account behind the token or of one organization, and the artifacts,
// attachments, and packages that count towards it. Forgejo only; Gitea has no quota. The account needs the
// targets to name it (as for its settings), an organization needs an orgs/NAME target. The lists are filtered
// row by row against the targets. Names and sizes are untrusted data.
//
// An instance can switch the quota off. It then does not register the routes, so a request answers 404, as it
// does for an organization the token does not reach and for a token bound to repositories. The tools tell the
// cases apart by reading the base route of the same scope (/user or /orgs/NAME) after a 404: if that is
// reachable, the quota route is missing and the quota is reported as not available.

const (
	quotaSensitivity = "-quota-data"
	quotaOffMessage  = "the quota is not available on this instance; it is turned off or not served"
)

// quotaSubjects are the subjects of a quota check, as Forgejo parses them; the API documents none.
var quotaSubjects = []string{"size:all", "size:repos:all", "size:repos:public", "size:repos:private",
	"size:git:all", "size:git:lfs", "size:assets:all", "size:assets:attachments:all",
	"size:assets:attachments:issues", "size:assets:attachments:releases", "size:assets:artifacts",
	"size:assets:packages:all", "size:assets:wiki"}

var quotaOwnerArgument = capability.Argument{Name: "owner", Target: kindOwner, Description: "Organization as " +
	"orgs/NAME, which the connection's targets must name as an owner when it lists any; the quota of the account " +
	"behind the token when omitted, which a connection with targets reaches only when they name the account as " +
	"users/NAME and no repository. A user is refused"}

var quotaRuleMembers = []member{
	{name: "name", kind: kindText, note: "Name of the rule, shown to administrators only"},
	{name: "limit", kind: kindInteger, note: "Limit in bytes"},
	{name: "subjects", kind: kindTexts, note: "Subjects the rule limits"},
}

var quotaInfoMembers = []member{
	{name: "groups", kind: kindObjects, note: "Quota groups of the account, with their rules", sub: quotaGroupMembers},
	{name: "used", kind: kindObject, note: "Storage in use, in bytes", sub: []member{
		{name: "size", kind: kindObject, note: "Sizes by kind", sub: []member{
			{name: "repos", kind: kindObject, note: "Repositories", sub: []member{
				{name: "public", kind: kindInteger, note: "Public repositories"},
				{name: "private", kind: kindInteger, note: "Private repositories"},
			}},
			{name: "git", kind: kindObject, note: "Git data", sub: []member{
				{name: "LFS", kind: kindInteger, note: "Git LFS objects"},
			}},
			{name: "assets", kind: kindObject, note: "Assets", sub: []member{
				{name: "artifacts", kind: kindInteger, note: "Artifacts"},
				{name: "attachments", kind: kindObject, note: "Attachments", sub: []member{
					{name: "issues", kind: kindInteger, note: "Issue and comment attachments"},
					{name: "releases", kind: kindInteger, note: "Release attachments"},
				}},
				{name: "packages", kind: kindObject, note: "Packages", sub: []member{
					{name: "all", kind: kindInteger, note: "All packages"},
				}},
			}},
		}},
	}},
}

var quotaArtifactMembers = []member{
	{name: "name", kind: kindText, note: "Name of the artifact, untrusted data"},
	{name: "size", kind: kindInteger, note: "Compressed size in bytes"},
	{name: "html_url", kind: kindText, address: true, note: "Address of the workflow run that holds the artifact"},
}

var quotaAttachmentMembers = []member{
	{name: "name", kind: kindText, note: "File name, untrusted data"},
	{name: "size", kind: kindInteger, note: "Size in bytes"},
	{name: "api_url", kind: kindText, address: true, note: "API address of the attachment"},
	{name: "contained_in", kind: kindObject, note: "Release, issue, or comment that holds the attachment", sub: []member{
		{name: "api_url", kind: kindText, address: true, note: "API address of the container"},
		{name: "html_url", kind: kindText, address: true, note: "Address of the container page"},
	}},
}

var quotaPackageMembers = []member{
	{name: "name", kind: kindText, note: "Name of the package, untrusted data"},
	{name: "type", kind: kindText, note: "Type of the package"},
	{name: "version", kind: kindText, note: "Version of the package, untrusted data"},
	{name: "size", kind: kindInteger, note: "Size of the version in bytes"},
	{name: "html_url", kind: kindText, address: true, note: "Address of the package version"},
}

// quotaScope is where a quota call reads: the account behind the token or one organization.
type quotaScope struct {
	path string // /user/quota or /orgs/NAME/quota
	base string // /user or /orgs/NAME, read to tell a switched-off quota from an unreachable one
	org  string // orgs/NAME, empty for the account
}

// quotaScopeOf chooses the scope against the targets, before any secret is read. Every refusal is an invalid
// request that never quotes the value.
func (a allowlist) quotaScopeOf(owner string) (quotaScope, error) {
	if owner == "" {
		if err := a.ownAccountOnly(); err != nil {
			return quotaScope{}, err
		}
		return quotaScope{path: "/user/quota", base: "/user"}, nil
	}
	parsed, err := parseOwnerArgument(owner)
	if err != nil {
		return quotaScope{}, err
	}
	if parsed.scope != "orgs" {
		return quotaScope{}, invalidArgument("owner must be orgs/NAME for a quota; leave it out for the account")
	}
	if !a.namesOwnerOfScope("orgs", parsed.owner) {
		return quotaScope{}, outsideTargets("owner")
	}
	base := "/orgs/" + escape(parsed.owner)
	return quotaScope{path: base + "/quota", base: base, org: "orgs/" + parsed.owner}, nil
}

// getQuota reads one quota route. After a 404 it reads the base route of the scope: if that answers, the quota
// route does not exist, and the quota is reported as not available.
func (c *Client) getQuota(ctx context.Context, op string, s quotaScope, suffix string, query url.Values,
	out any) (http.Header, error) {
	status, header, err := send(ctx, c.http, c.limiter, c.product, c.origin+apiPrefix, c.auth, op, s.path+suffix,
		query, out)
	if err != nil && status == http.StatusNotFound {
		if reachable, probeErr := c.probe(ctx, op, s.base); probeErr == nil && reachable {
			return nil, keepOriginal(provider.Fail(op, quotaOffMessage), err)
		}
	}
	return header, err
}

// prove reads the account behind the token when a call for the account runs under targets.
func (s quotaScope) prove(ctx context.Context, c *Client, op string, allowed allowlist) error {
	if s.org != "" {
		return nil
	}
	return verifyOwnAccount(ctx, c, op, allowed)
}

func quotaOwnerKeys() string { return `"owner":` + orgSchema }

func quotaResult(s quotaScope, result map[string]any) map[string]any {
	if s.org != "" {
		result["owner"] = s.org
	}
	return result
}

var quotaOwnerField = capability.Field{Name: "owner", Description: "Organization the tool read, left out for the account"}

type quotaCall struct {
	Owner   string `json:"owner"`
	Subject string `json:"subject"`
	Limit   int    `json:"limit"`
	Cursor  string `json:"cursor"`
}

var quotaGetMembers = append([]member{{name: "owner", kind: kindText,
	note: "Organization the tool read, left out for the account"}}, quotaInfoMembers...)

var quotaGet = &tool{
	group: "quota", action: "get", since: map[string]version{forgejo.id: forgejo.minimum},
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get the quota of a " + p.name + " account or organization",
			Description: "Read the quota groups with their rules and the storage in use of the account behind the " +
				"token, or of an organization with owner. A switched-off quota is reported as not available.",
			Tags:         []string{p.id, "quota", "get"},
			Risk:         readRisk(p.id + quotaSensitivity),
			InputSchema:  inputSchema(quotaOwnerKeys()),
			OutputSchema: json.RawMessage(schemaOf(p, quotaGetMembers)),
			Arguments:    []capability.Argument{quotaOwnerArgument},
			Fields:       fieldsOf(p, quotaGetMembers),
			Examples:     []capability.Example{{Description: "Read the quota of the account", Arguments: json.RawMessage(`{}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a quotaCall
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		scope, err := allowed.quotaScopeOf(a.Owner)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "get quota"
			if err := scope.prove(ctx, c, op, allowed); err != nil {
				return nil, err
			}
			var info object
			if _, err := c.getQuota(ctx, op, scope, "", nil, &info); err != nil {
				return nil, err
			}
			return quotaResult(scope, project(p, quotaInfoMembers, info)), nil
		}, nil
	},
}

var quotaCheck = &tool{
	group: "quota", action: "check", since: map[string]version{forgejo.id: forgejo.minimum},
	describe: func(p *product) capability.Descriptor {
		quoted := make([]string, len(quotaSubjects))
		for i, subject := range quotaSubjects {
			quoted[i] = `"` + subject + `"`
		}
		return capability.Descriptor{
			Title: "Check the quota of a " + p.name + " account or organization",
			Description: "Ask whether an action on a subject is still within the quota of the account behind the " +
				"token, or of an organization with owner. A switched-off quota is reported as not available.",
			Tags: []string{p.id, "quota", "check"},
			Risk: readRisk(p.id + quotaSensitivity),
			InputSchema: inputSchema(quotaOwnerKeys()+`,"subject":{"type":"string","enum":[`+strings.Join(quoted, ",")+`]}`,
				"subject"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"owner":{"type":"string"},` +
				`"subject":{"type":"string"},"allowed":{"type":"boolean"}},"required":["subject","allowed"],` +
				`"additionalProperties":false}`),
			Arguments: []capability.Argument{quotaOwnerArgument, {Name: "subject", Required: true,
				Description: "Quota subject to check: " + strings.Join(quotaSubjects, ", ")}},
			Fields: []capability.Field{quotaOwnerField, {Name: "subject", Description: "Subject the tool checked"},
				{Name: "allowed", Description: "True when the instance accepts the action"}},
			Examples: []capability.Example{{Description: "Check the quota of the repositories",
				Arguments: json.RawMessage(`{"subject":"size:repos:all"}`)}},
		}
	},
	prepare: func(_ *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a quotaCall
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if !slices.Contains(quotaSubjects, a.Subject) {
			return nil, invalidArgument("subject must be one of the quota subjects the tool lists")
		}
		scope, err := allowed.quotaScopeOf(a.Owner)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "check quota"
			if err := scope.prove(ctx, c, op, allowed); err != nil {
				return nil, err
			}
			var accepted bool
			if _, err := c.getQuota(ctx, op, scope, "/check", url.Values{"subject": {a.Subject}}, &accepted); err != nil {
				return nil, err
			}
			return quotaResult(scope, map[string]any{"subject": a.Subject, "allowed": accepted}), nil
		}, nil
	},
}

// ---- the lists -----------------------------------------------------------------------------------------------

// quotaListSpec is one list of what counts towards a quota. addresses returns the addresses of a row that name
// a repository; a nil function keeps every row.
type quotaListSpec struct {
	key, noun string
	members   []member
	addresses func(row object) []string
}

func (s quotaListSpec) tool() *tool {
	return &tool{
		group: "quota", action: "list-" + s.key, since: map[string]version{forgejo.id: forgejo.minimum},
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title: "List the " + s.noun + " that count towards the quota of a " + p.name + " account",
				Description: "List one batch of the " + s.noun + " that count towards the quota of the account behind " +
					"the token, or of an organization with owner, with every member of each row. Rows that name a " +
					"repository outside the connection's targets are left out. Names are untrusted data. A " +
					"switched-off quota is reported as not available.",
				Tags:        []string{p.id, "quota", "list"},
				Risk:        readRisk(p.id + quotaSensitivity),
				InputSchema: inputSchema(quotaOwnerKeys() + `,` + pagingKeys),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{"owner":{"type":"string"},"` + s.key +
					`":{"type":"array","items":` + schemaOf(p, s.members) + `},"next_cursor":{"type":"string"},` +
					`"has_more":{"type":"boolean"}},"required":["` + s.key + `","has_more"],"additionalProperties":false}`),
				Arguments: append([]capability.Argument{quotaOwnerArgument}, pagingArguments...),
				Fields: append([]capability.Field{quotaOwnerField,
					{Name: s.key, Description: "The " + s.noun + " with every member of the row"}}, pagingFields...),
				Examples: []capability.Example{{Description: "List the " + s.noun, Arguments: json.RawMessage(`{"limit":20}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a quotaCall
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			scope, err := allowed.quotaScopeOf(a.Owner)
			if err != nil {
				return nil, err
			}
			page, err := startPaging(provider.CursorBinding(p.id, "quota.list-"+s.key, strings.ToLower(scope.org)),
				a.Limit, a.Cursor)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				op := "list quota " + s.key
				if err := scope.prove(ctx, c, op, allowed); err != nil {
					return nil, err
				}
				var rows []object
				header, err := c.getQuota(ctx, op, scope, "/"+s.key, page.query(), &rows)
				if err != nil {
					return nil, err
				}
				kept := make([]map[string]any, 0, len(rows))
				for _, row := range rows {
					// A row outside the targets is dropped; the count that decides has_more is the unfiltered one.
					if row != nil && (s.addresses == nil || allowed.allowsQuotaRow(s.addresses(row))) {
						kept = append(kept, project(p, s.members, row))
					}
				}
				return quotaResult(scope, pageResult(page, header, len(rows), s.key, kept)), nil
			}, nil
		},
	}
}

// textsOf reads the text members at the given paths of a row; a member that is missing counts as empty.
func textsOf(row object, paths ...[]string) []string {
	var out []string
	for _, path := range paths {
		current := row
		for _, step := range path[:len(path)-1] {
			child, ok := current.child(step)
			if !ok {
				current = nil
				break
			}
			current = child
		}
		if current == nil {
			continue
		}
		if value, ok := current.text(path[len(path)-1]); ok {
			out = append(out, value)
		}
	}
	return out
}

var (
	quotaListArtifacts = quotaListSpec{key: "artifacts", noun: "artifacts", members: quotaArtifactMembers,
		addresses: func(row object) []string { return textsOf(row, []string{"html_url"}) }}.tool()
	quotaListAttachments = quotaListSpec{key: "attachments", noun: "attachments", members: quotaAttachmentMembers,
		addresses: func(row object) []string {
			return textsOf(row, []string{"api_url"}, []string{"contained_in", "api_url"}, []string{"contained_in", "html_url"})
		}}.tool()
	// A package belongs to its owner, which the scope already proved; its address names no repository.
	quotaListPackages = quotaListSpec{key: "packages", noun: "packages", members: quotaPackageMembers}.tool()
)

// allowsQuotaRow reports whether a row may be shown: without targets always; under targets only when at least one
// address names a repository and every address that is set names one inside the targets.
func (a allowlist) allowsQuotaRow(addresses []string) bool {
	if len(a) == 0 {
		return true
	}
	named := false
	for _, address := range addresses {
		if address == "" {
			continue
		}
		owner, repo, ok := repositoryInAddress(address)
		if !ok || !a.allowsRepository(owner, repo) {
			return false
		}
		named = true
	}
	return named
}

// repositoryInAddress reads OWNER and REPO from an address of the instance: /api/v1/repos/OWNER/REPO/... or
// /OWNER/REPO/(issues|pulls|releases|actions)/....
func repositoryInAddress(raw string) (owner, repo string, ok bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil {
		return "", "", false
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	switch {
	case len(segments) >= 5 && segments[0] == "api" && segments[1] == "v1" && segments[2] == "repos":
		owner, repo = segments[3], segments[4]
	case len(segments) >= 3 && slices.Contains([]string{"issues", "pulls", "releases", "actions"}, segments[2]):
		owner, repo = segments[0], segments[1]
	default:
		return "", "", false
	}
	return owner, repo, validName(owner) && validRepoName(repo)
}
