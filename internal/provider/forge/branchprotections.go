package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
)

// The branch protection tools read and change the protection rules of one repository inside the connection's
// targets. A rule is named by a branch name pattern (rule_name, for example main or release/*), not by a
// branch; no tool creates, renames, or deletes a branch. Rule names, patterns, and the user and team names of
// the allowlists are untrusted data. The rule name travels as one escaped path segment and is never sent as
// a path, a header, or a query. Lowering a protection is an ordinary change that needs confirmation.
//
// The swagger files of Forgejo 15, 16, and 17 and of Gitea 1.27 and 28 agree on the shared members. Only
// Forgejo knows apply_to_admins. Only Gitea knows priority, the force push members, the bypass allowlist,
// block_admin_merge_override, and, from 28, block_on_codeowner_reviews. branch_name is the deprecated twin of
// rule_name and neither sent nor shown.

const (
	protectionSensitivity = accessSensitivity
	maxRuleName           = 255
	maxProtectionEntries  = 100
	maxProtectionEntry    = 255
	maxProtectionPatterns = 4096
	maxApprovals          = 100
	maxPriority           = 1000000
)

// giteaCodeownerReviews is the first Gitea version whose swagger documents block_on_codeowner_reviews.
var giteaCodeownerReviews = version{28, 0, 0}

type protectionKind int

const (
	protectionFlag protectionKind = iota
	protectionCount
	protectionPriority
	protectionUsers
	protectionTeams
	protectionContexts
	protectionPatterns
)

// protectionField is one member a rule can be created or changed with. only limits it to one product; since
// names a later version of that product the member needs.
type protectionField struct {
	name  string
	kind  protectionKind
	only  *product
	since version
	note  string
}

func (f protectionField) offered(p *product) bool { return f.only == nil || f.only == p }

func (f protectionField) schema() string {
	switch f.kind {
	case protectionCount:
		return `{"type":"integer","minimum":0,"maximum":` + itoa(maxApprovals) + `}`
	case protectionPriority:
		return `{"type":"integer","minimum":0,"maximum":` + itoa(maxPriority) + `}`
	case protectionUsers:
		return `{"type":"array","maxItems":` + itoa(maxProtectionEntries) + `,"uniqueItems":true,"items":` +
			issueSchemaLogin + `}`
	case protectionTeams:
		return `{"type":"array","maxItems":` + itoa(maxProtectionEntries) + `,"uniqueItems":true,"items":` +
			`{"type":"string","minLength":1,"maxLength":100}}`
	case protectionContexts:
		return `{"type":"array","maxItems":` + itoa(maxProtectionEntries) + `,"uniqueItems":true,"items":` +
			`{"type":"string","minLength":1,"maxLength":` + itoa(maxProtectionEntry) + `}}`
	case protectionPatterns:
		return `{"type":"string","maxLength":` + itoa(maxProtectionPatterns) + `}`
	}
	return `{"type":"boolean"}`
}

// protectionFields are the members of a create or an edit, in the order the swagger lists them.
var protectionFields = []protectionField{
	{name: "priority", kind: protectionPriority, only: gitea, note: "Gitea only: priority of the rule; a lower " +
		"number is matched first, 0 lets the instance choose"},
	{name: "enable_push", kind: protectionFlag, note: "Whether pushing to a matching branch is allowed at all"},
	{name: "enable_push_whitelist", kind: protectionFlag, note: "Whether only the push allowlist may push"},
	{name: "push_whitelist_usernames", kind: protectionUsers, note: "Users who may push"},
	{name: "push_whitelist_teams", kind: protectionTeams, note: "Teams who may push"},
	{name: "push_whitelist_deploy_keys", kind: protectionFlag, note: "Whether deploy keys may push"},
	{name: "enable_force_push", kind: protectionFlag, only: gitea, note: "Gitea only: whether force pushes are allowed"},
	{name: "enable_force_push_allowlist", kind: protectionFlag, only: gitea,
		note: "Gitea only: whether only the force push allowlist may force push"},
	{name: "force_push_allowlist_usernames", kind: protectionUsers, only: gitea,
		note: "Gitea only: users who may force push"},
	{name: "force_push_allowlist_teams", kind: protectionTeams, only: gitea,
		note: "Gitea only: teams who may force push"},
	{name: "force_push_allowlist_deploy_keys", kind: protectionFlag, only: gitea,
		note: "Gitea only: whether deploy keys may force push"},
	{name: "enable_merge_whitelist", kind: protectionFlag, note: "Whether only the merge allowlist may merge"},
	{name: "merge_whitelist_usernames", kind: protectionUsers, note: "Users who may merge"},
	{name: "merge_whitelist_teams", kind: protectionTeams, note: "Teams who may merge"},
	{name: "enable_bypass_allowlist", kind: protectionFlag, only: gitea,
		note: "Gitea only: whether the bypass allowlist may bypass the rule"},
	{name: "bypass_allowlist_usernames", kind: protectionUsers, only: gitea,
		note: "Gitea only: users who may bypass the rule"},
	{name: "bypass_allowlist_teams", kind: protectionTeams, only: gitea,
		note: "Gitea only: teams who may bypass the rule"},
	{name: "enable_status_check", kind: protectionFlag, note: "Whether a merge needs passing status checks"},
	{name: "status_check_contexts", kind: protectionContexts,
		note: "Status check names or patterns a merge needs; at most 100 of at most 255 characters"},
	{name: "required_approvals", kind: protectionCount, note: "Approvals a merge needs, 0 through 100"},
	{name: "enable_approvals_whitelist", kind: protectionFlag,
		note: "Whether only the approvals allowlist counts for the required approvals"},
	{name: "approvals_whitelist_username", kind: protectionUsers,
		note: "Users whose approval counts; the name is singular as the API spells it"},
	{name: "approvals_whitelist_teams", kind: protectionTeams, note: "Teams whose approval counts"},
	{name: "block_on_rejected_reviews", kind: protectionFlag, note: "Whether a rejected review blocks a merge"},
	{name: "block_on_official_review_requests", kind: protectionFlag,
		note: "Whether a pending official review request blocks a merge"},
	{name: "block_on_codeowner_reviews", kind: protectionFlag, only: gitea, since: giteaCodeownerReviews,
		note: "Gitea 28 and later only: whether a merge needs a code owner review"},
	{name: "block_on_outdated_branch", kind: protectionFlag, note: "Whether an outdated pull request branch blocks a merge"},
	{name: "dismiss_stale_approvals", kind: protectionFlag, note: "Whether new commits dismiss earlier approvals"},
	{name: "ignore_stale_approvals", kind: protectionFlag, note: "Whether approvals of older commits are ignored"},
	{name: "require_signed_commits", kind: protectionFlag, note: "Whether commits must be signed"},
	{name: "protected_file_patterns", kind: protectionPatterns,
		note: "Semicolon-separated file patterns that only the push allowlist may change, at most 4096 characters"},
	{name: "unprotected_file_patterns", kind: protectionPatterns,
		note: "Semicolon-separated file patterns that stay changeable despite the protection, at most 4096 characters"},
	{name: "apply_to_admins", kind: protectionFlag, only: forgejo,
		note: "Forgejo only: whether the rule also binds administrators"},
	{name: "block_admin_merge_override", kind: protectionFlag, only: gitea,
		note: "Gitea only: whether administrators may not merge past the rule"},
}

// protectionMembers are the members of a rule as the instance answers it.
var protectionMembers = func() []member {
	members := []member{{name: "rule_name", kind: kindText, max: maxRuleName,
		note: "Branch name pattern the rule covers, untrusted data"}}
	for _, f := range protectionFields {
		m := member{name: f.name, only: f.only, note: f.note}
		switch f.kind {
		case protectionCount, protectionPriority:
			m.kind = kindInteger
		case protectionUsers, protectionTeams, protectionContexts:
			m.kind = kindTexts
		case protectionPatterns:
			m.kind, m.max = kindText, 64<<10
		default:
			m.kind = kindFlag
		}
		members = append(members, m)
	}
	return append(members, member{name: "created_at", kind: kindText, note: "Time the rule was created"},
		member{name: "updated_at", kind: kindText, note: "Time the rule was last changed"})
}()

var protectionRepositoryMember = member{name: "repository", kind: kindText, note: "Repository as OWNER/REPO"}

func protectionOutputMembers() []member {
	return append([]member{protectionRepositoryMember}, protectionMembers...)
}

func protectionProperties(p *product) string {
	var properties []string
	for _, f := range protectionFields {
		if f.offered(p) {
			properties = append(properties, `"`+f.name+`":`+f.schema())
		}
	}
	return strings.Join(properties, ",")
}

func protectionArguments(p *product) []capability.Argument {
	var arguments []capability.Argument
	for _, f := range protectionFields {
		if f.offered(p) {
			arguments = append(arguments, capability.Argument{Name: f.name, Description: f.note})
		}
	}
	return arguments
}

const ruleNameSchema = `{"type":"string","minLength":1,"maxLength":255}`

var ruleNameArgument = capability.Argument{Name: "rule_name", Required: true,
	Description: "Branch name pattern the rule covers, such as main or release/*; at most 255 characters"}

// checkProtectionText rejects an empty, overlong, or invalid text and one with control characters.
func checkProtectionText(field, value string, limit int) error {
	if value == "" || len(value) > limit || !utf8.ValidString(value) ||
		strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return invalidArgument(field + " must be text without control characters of at most " + itoa(limit) + " bytes")
	}
	return nil
}

// checkRuleName validates a rule name used as a path segment or in a body. A name of dots alone would be
// resolved as a path before the instance saw it.
func checkRuleName(value string) error {
	if value == "." || value == ".." {
		return invalidArgument("rule_name is not a usable branch name pattern")
	}
	return checkProtectionText("rule_name", value, maxRuleName)
}

func protectionsPath(repo target) string { return repositoryPath(repo) + "/branch_protections" }

func protectionPath(repo target, name string) string {
	return protectionsPath(repo) + "/" + escape(name)
}

// protectionBody reads the members of a create or an edit from the arguments. A member the product does not
// offer, a member of the wrong type, and a value outside its bounds are refused.
func protectionBody(p *product, given map[string]json.RawMessage) (map[string]any, error) {
	body := map[string]any{}
	for name, raw := range given {
		index := slices.IndexFunc(protectionFields, func(f protectionField) bool { return f.name == name })
		if index < 0 || !protectionFields[index].offered(p) {
			return nil, invalidArgument("an argument is not offered by " + p.name)
		}
		f := protectionFields[index]
		if string(raw) == "null" {
			return nil, invalidArgument(f.name + " must not be null")
		}
		value, err := protectionValue(f, raw)
		if err != nil {
			return nil, err
		}
		body[name] = value
	}
	return body, nil
}

func protectionValue(f protectionField, raw json.RawMessage) (any, error) {
	switch f.kind {
	case protectionFlag:
		var flag bool
		if json.Unmarshal(raw, &flag) != nil {
			return nil, invalidArgument(f.name + " must be true or false")
		}
		return flag, nil
	case protectionCount, protectionPriority:
		limit := int64(maxApprovals)
		if f.kind == protectionPriority {
			limit = maxPriority
		}
		var n int64
		if json.Unmarshal(raw, &n) != nil || n < 0 || n > limit {
			return nil, invalidArgument(f.name + " must be a count from 0 through " + itoa(int(limit)))
		}
		return n, nil
	case protectionPatterns:
		var text string
		if json.Unmarshal(raw, &text) != nil || len(text) > maxProtectionPatterns || !utf8.ValidString(text) ||
			strings.IndexFunc(text, unicode.IsControl) >= 0 {
			return nil, invalidArgument(f.name + " must be text without control characters of at most " +
				itoa(maxProtectionPatterns) + " bytes")
		}
		return text, nil
	}
	var list []string
	if json.Unmarshal(raw, &list) != nil || len(list) > maxProtectionEntries {
		return nil, invalidArgument(f.name + " must be a list of at most " + itoa(maxProtectionEntries) + " texts")
	}
	for _, entry := range list {
		var err error
		switch f.kind {
		case protectionUsers:
			err = userNameOf(f.name, entry)
		case protectionTeams:
			err = checkProtectionText(f.name, entry, 100)
		default:
			err = checkProtectionText(f.name, entry, maxProtectionEntry)
		}
		if err != nil {
			return nil, err
		}
	}
	if list == nil {
		list = []string{}
	}
	return list, nil
}

// protectionNeeds is the newer version a call's members require, for a member only later versions know.
func protectionNeeds(p *product, raw json.RawMessage) (version, bool) {
	var given map[string]json.RawMessage
	if decodeArguments(raw, &given) != nil {
		return version{}, false
	}
	var needed version
	found := false
	for _, f := range protectionFields {
		if _, ok := given[f.name]; ok && f.offered(p) && (f.since != version{}) && needed.less(f.since) {
			needed, found = f.since, true
		}
	}
	return needed, found
}

// protectionRuleTarget reads the repository and the rule name of a tool that names one rule.
func protectionRuleTarget(allowed allowlist, given map[string]json.RawMessage) (target, string, error) {
	var repository, name string
	if value, ok := given["repository"]; ok && json.Unmarshal(value, &repository) != nil {
		return target{}, "", invalidArgument("repository must be OWNER/REPO")
	}
	repo, err := allowed.chooseRepository(repository)
	if err != nil {
		return target{}, "", err
	}
	if json.Unmarshal(given["rule_name"], &name) != nil {
		return target{}, "", invalidArgument("rule_name must be text")
	}
	if err := checkRuleName(name); err != nil {
		return target{}, "", err
	}
	return repo, name, nil
}

// protectionRule reads the arguments of a rule tool, binds the repository, and validates the rule name; what
// is left in the returned map are the members of the body.
func protectionRule(allowed allowlist, raw json.RawMessage) (target, string, map[string]json.RawMessage, error) {
	var given map[string]json.RawMessage
	if err := decodeArguments(raw, &given); err != nil {
		return target{}, "", nil, err
	}
	repo, name, err := protectionRuleTarget(allowed, given)
	if err != nil {
		return target{}, "", nil, err
	}
	delete(given, "repository")
	delete(given, "rule_name")
	return repo, name, given, nil
}

func protectionResult(p *product, repo target, answer object) map[string]any {
	result := project(p, protectionMembers, answer)
	result["repository"] = fullName(repo)
	return result
}

var protectionRepositoryProperty = `"repository":` + repoSchema

const protectionExample = `{"repository":"example/project","rule_name":"main"}`

var protectionNameProperties = protectionRepositoryProperty + `,"rule_name":` + ruleNameSchema

func protectionObjectOutput(p *product) json.RawMessage {
	return json.RawMessage(schemaOf(p, protectionOutputMembers(), "repository", "rule_name"))
}

var branchProtectionsList = &tool{
	group: "branch-protections", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the branch protection rules of a " + p.name + " repository",
			Description: "List the branch protection rules of a repository inside the connection's targets with " +
				"every member of each rule. The API documents no paging, so the answer is one list of at most 100 " +
				"rules; truncated says that the instance sent more. Rule names, patterns, and the names in the " +
				"allowlists are untrusted data.",
			Tags:        []string{p.id, "branch-protections", "list"},
			Risk:        readRisk(p.id + protectionSensitivity),
			InputSchema: inputSchema(protectionRepositoryProperty),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"branch_protections":{"type":"array","items":` + schemaOf(p, protectionMembers) + `},` +
				`"truncated":{"type":"boolean"}},"required":["repository","branch_protections"],` +
				`"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument},
			Fields: []capability.Field{{Name: "repository", Description: "Repository the tool listed, as OWNER/REPO"},
				{Name: "branch_protections", Description: "Rules with every member of the rule object"},
				{Name: "truncated", Description: "True when the instance sent more rules than the tool returns"}},
			Examples: []capability.Example{{Description: "List the rules",
				Arguments: json.RawMessage(`{"repository":"example/project"}`)}},
		}
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
			var rows []object
			if _, err := c.get(ctx, "list branch protections", protectionsPath(repo), nil, &rows); err != nil {
				return nil, err
			}
			result := map[string]any{"repository": fullName(repo)}
			if len(rows) > maxListItems {
				rows, result["truncated"] = rows[:maxListItems], true
			}
			rules := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					rules = append(rules, project(p, protectionMembers, row))
				}
			}
			result["branch_protections"] = rules
			return result, nil
		}, nil
	},
}

var branchProtectionsGet = &tool{
	group: "branch-protections", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a branch protection rule of a " + p.name + " repository",
			Description: "Read one branch protection rule of a repository inside the connection's targets by its " +
				"name with every member of the rule; texts are untrusted data. The rule is found by its exact name, " +
				"not by a branch it covers.",
			Tags:         []string{p.id, "branch-protections", "get"},
			Risk:         readRisk(p.id + protectionSensitivity),
			InputSchema:  inputSchema(protectionNameProperties, "rule_name"),
			OutputSchema: protectionObjectOutput(p),
			Arguments:    []capability.Argument{repositoryArgument, ruleNameArgument},
			Fields:       fieldsOf(p, protectionOutputMembers()),
			Examples: []capability.Example{{Description: "Read a rule",
				Arguments: json.RawMessage(protectionExample)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		repo, name, _, err := protectionRule(allowed, raw)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var answer object
			if _, err := c.get(ctx, "get branch protection", protectionPath(repo, name), nil, &answer); err != nil {
				return nil, err
			}
			return protectionResult(p, repo, answer), nil
		}, nil
	},
}

// protectionWrite builds the create and the edit, which differ in the method, the path, and the required
// arguments.
func protectionWrite(action string) *tool {
	create := action == "create"
	return &tool{
		group: "branch-protections", action: action, since: everywhere, needs: protectionNeeds,
		describe: func(p *product) capability.Descriptor {
			effect, idempotency := capability.EffectUpdate, capability.IdempotencyIdempotent
			title := "Change a branch protection rule of a %s repository"
			description := "Change the given members of one branch protection rule of a repository inside the " +
				"connection's targets; members that are left out stay as they are and at least one must be given. " +
				"A list replaces the whole list, an empty list clears it. The rule name cannot be changed. " +
				"Lowering a protection is a change like any other and needs confirmation. "
			example := `{"repository":"example/project","rule_name":"main","required_approvals":2}`
			if create {
				effect, idempotency = capability.EffectCreate, capability.IdempotencyNonIdempotent
				title = "Create a branch protection rule in a %s repository"
				description = "Create exactly one branch protection rule for a branch name pattern in a repository " +
					"inside the connection's targets, with the given members; a member that is left out takes the " +
					"instance's default, so a rule without members blocks direct pushes. An existing rule name is " +
					"refused by the instance. "
				example = `{"repository":"example/project","rule_name":"release/*","required_approvals":1,` +
					`"enable_push":false}`
			}
			description += "The request is sent once and never repeated after an unclear outcome; the answer is the " +
				"rule as the instance reports it. " + productNote(p) + " The token must be an administrator of the " +
				"repository."
			required := []string{"rule_name"}
			return capability.Descriptor{
				Title:        strings.ReplaceAll(title, "%s", p.name),
				Description:  description,
				Tags:         []string{p.id, "branch-protections", action},
				Risk:         changeRisk(effect, idempotency, p.id+protectionSensitivity),
				InputSchema:  inputSchema(protectionNameProperties+","+protectionProperties(p), required...),
				OutputSchema: protectionObjectOutput(p),
				Arguments: append([]capability.Argument{repositoryArgument, ruleNameArgument},
					protectionArguments(p)...),
				Fields: fieldsOf(p, protectionOutputMembers()),
				Examples: []capability.Example{{Description: strings.ReplaceAll(title, "%s", p.name),
					Arguments: json.RawMessage(example)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			repo, name, given, err := protectionRule(allowed, raw)
			if err != nil {
				return nil, err
			}
			body, err := protectionBody(p, given)
			if err != nil {
				return nil, err
			}
			method, path := http.MethodPatch, protectionPath(repo, name)
			if create {
				method, path = http.MethodPost, protectionsPath(repo)
				body["rule_name"] = name
			} else if len(body) == 0 {
				return nil, invalidArgument("at least one member must be given")
			}
			return func(ctx context.Context, c *Client) (any, error) {
				var answer object
				if err := c.change(ctx, action+" branch protection", method, path, body, &answer); err != nil {
					return nil, err
				}
				result := protectionResult(p, repo, answer)
				if _, ok := result["rule_name"]; !ok {
					result["rule_name"] = name
				}
				return result, nil
			}, nil
		},
	}
}

func productNote(p *product) string {
	if p == forgejo {
		return "Forgejo alone knows apply_to_admins."
	}
	return "Gitea alone knows priority, the force push and bypass members, and block_admin_merge_override; " +
		"block_on_codeowner_reviews needs Gitea 28."
}

var (
	branchProtectionsCreate = protectionWrite("create")
	branchProtectionsUpdate = protectionWrite("update")
)

// maxReorderIDs bounds the list of a reorder.
const maxReorderIDs = 100

// branchProtectionsReorder sets the priority of the rules in the order of the IDs. The swagger names the IDs
// of the rules, but its rule object carries none, so the IDs come from outside this API. The instance
// applies the list to the rules of this repository only.
var branchProtectionsReorder = &tool{
	group: "branch-protections", action: "reorder", since: giteaOnly,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Reorder the branch protection rules of a " + p.name + " repository",
			Description: "Set the priority of the branch protection rules of a repository inside the connection's " +
				"targets to the order of the given rule IDs, first ID first; the first matching rule applies to a " +
				"branch, so a new order can lower the protection of a branch. The API does not show rule IDs, and " +
				"the priority argument of the update tool changes one rule by name. Gitea only. The request is sent once and " +
				"never repeated after an unclear outcome. The token must be an administrator of the repository.",
			Tags: []string{p.id, "branch-protections", "reorder"},
			Risk: changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+protectionSensitivity),
			InputSchema: inputSchema(protectionRepositoryProperty+`,"ids":{"type":"array","minItems":1,"maxItems":`+
				itoa(maxReorderIDs)+`,"uniqueItems":true,"items":{"type":"integer","minimum":1}}`, "ids"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"ids":{"type":"array","items":{"type":"integer"}},"reordered":{"type":"boolean"}},` +
				`"required":["repository","ids","reordered"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, {Name: "ids", Required: true,
				Description: "Numeric rule IDs in the new order of priority, at most 100, without repeats"}},
			Fields: []capability.Field{{Name: "repository", Description: "Repository as OWNER/REPO"},
				{Name: "ids", Description: "Rule IDs that were sent, in order"},
				{Name: "reordered", Description: "True once the instance accepted the request"}},
			Examples: []capability.Example{{Description: "Reorder the rules",
				Arguments: json.RawMessage(`{"repository":"example/project","ids":[3,1,2]}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Repository string  `json:"repository"`
			IDs        []int64 `json:"ids"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		repo, err := allowed.chooseRepository(a.Repository)
		if err != nil {
			return nil, err
		}
		if len(a.IDs) == 0 || len(a.IDs) > maxReorderIDs {
			return nil, invalidArgument("ids must list from 1 through " + itoa(maxReorderIDs) + " rule IDs")
		}
		seen := map[int64]bool{}
		for _, id := range a.IDs {
			if id < 1 || seen[id] {
				return nil, invalidArgument("ids must be positive and without repeats")
			}
			seen[id] = true
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "reorder branch protections", http.MethodPost, protectionsPath(repo)+"/priority",
				map[string][]int64{"ids": a.IDs}, nil); err != nil {
				return nil, err
			}
			return map[string]any{"repository": fullName(repo), "ids": a.IDs, "reordered": true}, nil
		}, nil
	},
}

var branchProtectionsDelete = &tool{
	group: "branch-protections", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete a branch protection rule of a " + p.name + " repository",
			Description: "Delete one branch protection rule of a repository inside the connection's targets for " +
				"good by its name; the branches it covered stay and lose its protection. The request is sent once " +
				"and never repeated after an unclear outcome. Only a connection whose tools list names it offers " +
				"this tool. The token must be an administrator of the repository.",
			Tags:                  []string{p.id, "branch-protections", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+protectionSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(protectionNameProperties, "rule_name"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"repository":{"type":"string"},` +
				`"rule_name":{"type":"string"},"deleted":{"type":"boolean"}},` +
				`"required":["repository","rule_name","deleted"],"additionalProperties":false}`),
			Arguments: []capability.Argument{repositoryArgument, ruleNameArgument},
			Fields: []capability.Field{{Name: "repository", Description: "Repository as OWNER/REPO"},
				{Name: "rule_name", Description: "Name of the rule"},
				{Name: "deleted", Description: "True once the instance deleted the rule"}},
			Examples: []capability.Example{{Description: "Delete a rule",
				Arguments: json.RawMessage(protectionExample)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		repo, name, given, err := protectionRule(allowed, raw)
		if err != nil {
			return nil, err
		}
		if len(given) != 0 {
			return nil, invalidArgument("an argument is not offered by this tool")
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if err := c.change(ctx, "delete branch protection", http.MethodDelete, protectionPath(repo, name), nil,
				nil); err != nil {
				return nil, err
			}
			return map[string]any{"repository": fullName(repo), "rule_name": name, "deleted": true}, nil
		}, nil
	},
}
