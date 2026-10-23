package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Repository and organization rulesets. Every call addresses exactly one of the two: repository, checked
// against the connection's repository targets the way every other repository tool is, or organization,
// checked against its owner targets the way github.teams.list checks one; giving both or neither is refused
// before a credential is resolved, so a call never falls back to a target the caller did not name.
// github.rulesets.list and github.rulesets.get read rulesets; github.rulesets.create, .update, and .delete
// change them and are offered only where a connection's tools list names them, since a ruleset governs what
// a repository or an organization allows at all. github.rulesets.update replaces a ruleset as a whole, the
// way the Actions settings do, so sending the same definition again leaves GitHub in the same state.

// rulesetSensitivity classifies a ruleset as a governance setting of the repository or the organization it
// belongs to, distinct from the planning data the rest of this provider reads.
const rulesetSensitivity = "github-ruleset"

// Bounds of one ruleset definition. GitHub's own limits are not documented precisely; these keep a call
// within what this provider reads back in one bounded response.
const (
	maxRulesetRules        = 200
	maxRulesetBypassActors = 100
	maxRulesetInputBytes   = 64 << 10
)

const (
	rulesetTargetsSchema = `{"type":"array","minItems":1,"maxItems":3,"uniqueItems":true,"items":` +
		`{"type":"string","enum":["branch","tag","push"]}}`
	rulesetNameSchema        = `{"type":"string","minLength":1,"maxLength":100}`
	rulesetTargetSchema      = `{"type":"string","enum":["branch","tag","push","repository"]}`
	rulesetEnforcementSchema = `{"type":"string","enum":["disabled","active","evaluate"]}`
	// rulesetConditionsSchema, rulesetRuleSchema, and rulesetBypassActorSchema validate only the shape every
	// caller shares. qatlas-dev: GitHub defines several rule types and two condition shapes (ref_name for a
	// branch or a tag, repository_name for target repository), each with its own parameters, passed through
	// as given rather than modeled one by one; give the rule or the condition a schema of its own if a
	// caller needs one of them validated strictly.
	rulesetConditionsSchema = `{"type":"object"}`
	rulesetRuleSchema       = `{"type":"object","properties":{"type":{"type":"string","minLength":1,` +
		`"maxLength":100}},"required":["type"]}`
	rulesetRulesSchema       = `{"type":"array","maxItems":200,"items":` + rulesetRuleSchema + `}`
	rulesetBypassActorSchema = `{"type":"object","properties":{"actor_id":{"type":"integer","minimum":1},` +
		`"actor_type":{"type":"string","minLength":1,"maxLength":40},` +
		`"bypass_mode":{"type":"string","minLength":1,"maxLength":40}},"required":["actor_type"]}`
	rulesetBypassActorsSchema = `{"type":"array","maxItems":100,"items":` + rulesetBypassActorSchema + `}`
	rulesetIDSchema           = `{"type":"integer","minimum":1,"maximum":9007199254740991}`
)

// The exclusive target arguments of a rulesets tool: repository, checked the way selectTarget checks every
// repository tool, or organization, checked the way selectOrganization checks the organization tools;
// exactly one is required, so withTargetArgument does not add its own repository argument to these tools.
var rulesetRepositoryArgument = capability.Argument{Name: "repository", Description: "Repository as " +
	"OWNER/REPO whose rulesets this call addresses; exactly one of repository or organization is required, " +
	"never both; must lie inside the targets when the connection lists any"}

var rulesetOrganizationArgument = capability.Argument{Name: "organization", Description: "Organization as " +
	"orgs/LOGIN whose rulesets this call addresses; exactly one of repository or organization is required, " +
	"never both; must be an owner target the connection allows when it lists any"}

var rulesetRepositoryField = capability.Field{Name: "repository", Description: "Repository this call " +
	"addressed, as OWNER/REPO; present only for a repository ruleset"}

var rulesetOrganizationField = capability.Field{Name: "organization", Description: "Organization this call " +
	"addressed, as orgs/LOGIN; present only for an organization ruleset"}

var rulesetDefinitionArguments = []capability.Argument{
	{Name: "name", Description: "Name of the ruleset, 1 to 100 characters", Required: true},
	{Name: "target", Description: "branch, tag, push, or repository (an organization ruleset that selects " +
		"repositories instead of refs)", Required: true},
	{Name: "enforcement", Description: "disabled, active, or evaluate", Required: true},
	{Name: "conditions", Description: "Conditions the ruleset applies under, such as ref_name include and " +
		"exclude, or, for target repository, repository_name; GitHub's own shape, passed through as given"},
	{Name: "rules", Description: "Rules the ruleset enforces, each a {type, parameters} object of GitHub's " +
		"own rule types; at most 200"},
	{Name: "bypass_actors", Description: "Actors that may bypass the ruleset, each with actor_type and, " +
		"depending on it, actor_id and bypass_mode; at most 100"},
}

var rulesetFields = []capability.Field{
	{Name: "id", Description: "Numeric identifier of the ruleset"},
	{Name: "name", Description: "Name of the ruleset"},
	{Name: "target", Description: "branch, tag, push, or repository"},
	{Name: "enforcement", Description: "disabled, active, or evaluate"},
	{Name: "source", Description: "Repository or organization the ruleset is configured on, as GitHub names it, " +
		"untrusted data"},
	{Name: "source_type", Description: "Repository, Organization, or Enterprise"},
	{Name: "node_id", Description: "GraphQL node id of the ruleset"},
	{Name: "current_user_can_bypass", Description: "Whether the token's user can bypass the ruleset: always, " +
		"pull_requests_only, never, or exempt"},
	{Name: "created_at", Description: "Creation time"},
	{Name: "updated_at", Description: "Time of the last change"},
	{Name: "url", Description: "Web address of the ruleset, untrusted data, never fetched"},
	{Name: "links", Description: "REST url of the ruleset by its GitHub name, untrusted data, never fetched"},
	{Name: "conditions", Description: "Conditions the ruleset applies under, GitHub's own shape, untrusted data"},
	{Name: "rules", Description: "Rules the ruleset enforces, GitHub's own shape, untrusted data"},
	{Name: "bypass_actors", Description: "Actors that may bypass the ruleset, GitHub's own shape, untrusted data"},
}

var rulesetLinkKeys = []string{"url"}

// rulesetWidthProperties are the schema members of the ruleset views beyond the compact ones.
var rulesetWidthProperties = `"node_id":{"type":"string"},"source_type":{"type":"string"},` +
	`"current_user_can_bypass":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"},` +
	`"url":{"type":"string"},"links":` + linksSchema(rulesetLinkKeys)

var rulesetListOutput = `{"type":"object","properties":{"rulesets":{"type":"array","items":{"type":"object",` +
	`"properties":{"id":{"type":"integer"},"name":{"type":"string"},"target":{"type":"string"},` +
	`"enforcement":{"type":"string"},"source":{"type":"string"},"source_name":{"type":"string"},` +
	rulesetWidthProperties + `,"conditions":{"type":"object"},"conditions_omitted":{"type":"boolean"},` +
	`"rules":{"type":"array"},"rules_omitted":{"type":"boolean"},"bypass_actors":{"type":"array"},` +
	`"bypass_actors_omitted":{"type":"boolean"}},` +
	`"required":["id","name","target","enforcement","source"],"additionalProperties":false}},` +
	`"repository":{"type":"string"},"organization":{"type":"string"},"next_cursor":{"type":"string"},` +
	`"has_more":{"type":"boolean"}},"required":["rulesets","has_more"],"additionalProperties":false}`

var rulesetOutput = `{"type":"object","properties":{"id":{"type":"integer"},"name":{"type":"string"},` +
	`"target":{"type":"string"},"enforcement":{"type":"string"},"source":{"type":"string"},` +
	rulesetWidthProperties + `,"conditions":{"type":"object"},` +
	`"rules":{"type":"array"},"bypass_actors":{"type":"array"},"repository":{"type":"string"},` +
	`"organization":{"type":"string"}},` +
	`"required":["id","name","target","enforcement","conditions","rules","bypass_actors"],` +
	`"additionalProperties":false}`

const rulesetDeletedOutput = `{"type":"object","properties":{"deleted":{"type":"boolean"},` +
	`"repository":{"type":"string"},"organization":{"type":"string"}},"required":["deleted"],` +
	`"additionalProperties":false}`

var rulesetsList = capability.Descriptor{
	ID:      Provider + ".rulesets.list",
	Version: 1,
	Title:   "List GitHub rulesets",
	Description: "List one bounded batch of the rulesets of a repository or, with organization instead, an " +
		"organization a connection allows, in GitHub's list form; exactly one of repository or organization " +
		"is required",
	Tags:     []string{"github", "rulesets", "governance", "list"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: inputSchema(`"repository":` + repoSchema + `,"organization":` + ownerSchema +
		`,"includes_parents":{"type":"boolean"},"targets":` + rulesetTargetsSchema + `,` + pagingKeys),
	OutputSchema: json.RawMessage(rulesetListOutput),
	Arguments: append([]capability.Argument{rulesetRepositoryArgument, rulesetOrganizationArgument, {
		Name: "includes_parents", Description: "For a repository, true (the default) also lists rulesets " +
			"inherited from its organization; ignored for an organization, which has none to inherit",
	}, {
		Name: "targets", Description: "Only rulesets that apply to these targets: a list of branch, tag, " +
			"and push without repetition",
	}}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "rulesets", Description: "Rulesets: id, name, target, enforcement, and source (the repository " +
			"or organization they were read from, as OWNER/REPO or orgs/LOGIN), source_name and source_type " +
			"(where GitHub configures the ruleset), the members of github.rulesets.get, and, where GitHub " +
			"lists them, bypass_actors, conditions, and rules; a free-form member beyond the bounds is left " +
			"out and its *_omitted member says so; untrusted data"},
		rulesetRepositoryField, rulesetOrganizationField,
	}, pagingFields...),
	Examples: []capability.Example{{
		Description: "List the rulesets of a repository",
		Arguments:   json.RawMessage(`{"repository":"octo-org/example"}`),
	}, {
		Description: "List the rulesets of an organization",
		Arguments:   json.RawMessage(`{"organization":"orgs/octo-org"}`),
	}},
}

var rulesetsGet = capability.Descriptor{
	ID:      Provider + ".rulesets.get",
	Version: 1,
	Title:   "Get a GitHub ruleset",
	Description: "Read one ruleset of a repository or, with organization instead, an organization an explicit " +
		"connection allows, with its conditions, rules, and bypass actors; exactly one of repository or " +
		"organization is required",
	Tags:     []string{"github", "rulesets", "governance", "get"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: inputSchema(`"repository":`+repoSchema+`,"organization":`+ownerSchema+`,"id":`+rulesetIDSchema+
		`,"includes_parents":{"type":"boolean"}`, "id"),
	OutputSchema: json.RawMessage(rulesetOutput),
	Arguments: []capability.Argument{
		{Name: "id", Description: "Numeric ruleset identifier, as github.rulesets.list reports it", Required: true},
		rulesetRepositoryArgument, rulesetOrganizationArgument,
		{Name: "includes_parents", Description: "For a repository, whether a ruleset inherited from its " +
			"organization may be read, true being GitHub's default; refused for an organization"},
	},
	Fields: append(append([]capability.Field{}, rulesetFields...), rulesetRepositoryField, rulesetOrganizationField),
	Examples: []capability.Example{{
		Description: "Read one ruleset of a repository",
		Arguments:   json.RawMessage(`{"repository":"octo-org/example","id":1420}`),
	}},
}

var rulesetsCreate = capability.Descriptor{
	ID:      Provider + ".rulesets.create",
	Version: 1,
	Title:   "Create a GitHub ruleset",
	Description: "Create one new ruleset in a repository or, with organization instead, an organization a " +
		"connection allows; exactly one of repository or organization is required; not idempotent, " +
		"since a repeated call creates a second ruleset; offered only where a connection's tools list names it",
	Tags:                  []string{"github", "rulesets", "governance", "create"},
	Risk:                  guardedRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, rulesetSensitivity),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: inputSchema(`"repository":`+repoSchema+`,"organization":`+ownerSchema+`,"name":`+rulesetNameSchema+
		`,"target":`+rulesetTargetSchema+`,"enforcement":`+rulesetEnforcementSchema+`,"conditions":`+
		rulesetConditionsSchema+`,"rules":`+rulesetRulesSchema+`,"bypass_actors":`+rulesetBypassActorsSchema,
		"name", "target", "enforcement"),
	OutputSchema: json.RawMessage(rulesetOutput),
	Arguments: append([]capability.Argument{rulesetRepositoryArgument, rulesetOrganizationArgument},
		rulesetDefinitionArguments...),
	Fields: append(append([]capability.Field{}, rulesetFields...), rulesetRepositoryField, rulesetOrganizationField),
	Examples: []capability.Example{{
		Description: "Create an active branch ruleset that blocks force pushes",
		Arguments: json.RawMessage(`{"repository":"octo-org/example","name":"protect-main","target":"branch",` +
			`"enforcement":"active","conditions":{"ref_name":{"include":["refs/heads/main"],"exclude":[]}},` +
			`"rules":[{"type":"non_fast_forward"}]}`),
	}},
}

var rulesetsUpdate = capability.Descriptor{
	ID:      Provider + ".rulesets.update",
	Version: 1,
	Title:   "Update a GitHub ruleset",
	Description: "Replace one ruleset of a repository or, with organization instead, an organization a " +
		"connection allows, as a whole: name, target, enforcement, conditions, rules, and bypass " +
		"actors all set to the given values, since GitHub replaces a ruleset together; exactly one of " +
		"repository or organization is required; idempotent, since sending the same definition again leaves " +
		"GitHub in the same state; offered only where a connection's tools list names it",
	Tags:                  []string{"github", "rulesets", "governance", "update"},
	Risk:                  guardedRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, rulesetSensitivity),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: inputSchema(`"repository":`+repoSchema+`,"organization":`+ownerSchema+`,"id":`+rulesetIDSchema+
		`,"name":`+rulesetNameSchema+`,"target":`+rulesetTargetSchema+`,"enforcement":`+rulesetEnforcementSchema+
		`,"conditions":`+rulesetConditionsSchema+`,"rules":`+rulesetRulesSchema+`,"bypass_actors":`+
		rulesetBypassActorsSchema, "id", "name", "target", "enforcement"),
	OutputSchema: json.RawMessage(rulesetOutput),
	Arguments: append([]capability.Argument{
		{Name: "id", Description: "Numeric ruleset identifier to replace, as github.rulesets.list reports it", Required: true},
		rulesetRepositoryArgument, rulesetOrganizationArgument,
	}, rulesetDefinitionArguments...),
	Fields: append(append([]capability.Field{}, rulesetFields...), rulesetRepositoryField, rulesetOrganizationField),
	Examples: []capability.Example{{
		Description: "Replace a ruleset's rules",
		Arguments: json.RawMessage(`{"repository":"octo-org/example","id":1420,"name":"protect-main",` +
			`"target":"branch","enforcement":"active",` +
			`"conditions":{"ref_name":{"include":["refs/heads/main"],"exclude":[]}},` +
			`"rules":[{"type":"deletion"},{"type":"non_fast_forward"}]}`),
	}},
}

var rulesetsDelete = capability.Descriptor{
	ID:      Provider + ".rulesets.delete",
	Version: 1,
	Title:   "Delete a GitHub ruleset",
	Description: "Delete one ruleset of a repository or, with organization instead, an organization a " +
		"connection allows permanently; exactly one of repository or organization is required; " +
		"offered only where a connection's tools list names it",
	Tags:                  []string{"github", "rulesets", "governance", "delete"},
	Risk:                  guardedRisk(capability.EffectDelete, capability.IdempotencyUnknown, rulesetSensitivity),
	Provider:              Provider,
	RequiresToolAllowList: true,
	InputSchema: inputSchema(`"repository":`+repoSchema+`,"organization":`+ownerSchema+`,"id":`+rulesetIDSchema,
		"id"),
	OutputSchema: json.RawMessage(rulesetDeletedOutput),
	Arguments: []capability.Argument{
		{Name: "id", Description: "Numeric ruleset identifier to delete, as github.rulesets.list reports it", Required: true},
		rulesetRepositoryArgument, rulesetOrganizationArgument,
	},
	Fields: []capability.Field{{Name: "deleted", Description: "True once GitHub deleted the ruleset"},
		rulesetRepositoryField, rulesetOrganizationField},
	Examples: []capability.Example{{
		Description: "Delete a ruleset",
		Arguments:   json.RawMessage(`{"repository":"octo-org/example","id":1420}`),
	}},
}

// rulesetsOperations binds every ruleset tool to its handler.
func rulesetsOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: rulesetsList, Handler: capability.Handler(invokeRulesetsList)},
		{Descriptor: rulesetsGet, Handler: capability.Handler(invokeRulesetsGet)},
		{Descriptor: rulesetsCreate, Handler: capability.Handler(invokeRulesetsCreate)},
		{Descriptor: rulesetsUpdate, Handler: capability.Handler(invokeRulesetsUpdate)},
		{Descriptor: rulesetsDelete, Handler: capability.Handler(invokeRulesetsDelete)},
	}
}

// selectRulesetTarget reads the exclusive repository or organization argument of a rulesets tool: a
// repository ruleset needs repository, an organization ruleset needs organization, and never both or
// neither, so a call never silently falls back to a target the caller did not name. It runs before a
// credential is resolved.
func selectRulesetTarget(resolved *config.Resolved, raw json.RawMessage) (target, error) {
	if resolved == nil {
		return target{}, providerError("open", "no connection was selected")
	}
	var arguments struct {
		Repository   string `json:"repository"`
		Organization string `json:"organization"`
	}
	if json.Unmarshal(raw, &arguments) != nil {
		return target{}, unreadable("select ruleset target")
	}
	hasRepository := strings.TrimSpace(arguments.Repository) != ""
	hasOrganization := strings.TrimSpace(arguments.Organization) != ""
	if hasRepository == hasOrganization {
		return target{}, invalidRequest("give exactly one of repository or organization, never both or neither")
	}
	if hasRepository {
		return selectTarget(resolved, kindRepository, raw)
	}
	allowed, err := allowlistOf(resolved)
	if err != nil {
		return target{}, providerError("open", err.Error())
	}
	bound, _, err := parseAllowedOrganization(allowed, "organization", arguments.Organization)
	return bound, err
}

// rulesetLocate adds the repository or the organization a rulesets tool addressed to its successful result,
// under that argument's own name; target.locate is not reused here because it names an owner target
// "owner", while a ruleset call names it "organization".
func (t target) rulesetLocate(value any, err error) (any, error) {
	if err != nil {
		return value, err
	}
	encoded, merr := json.Marshal(value)
	var fields map[string]json.RawMessage
	if merr != nil || json.Unmarshal(encoded, &fields) != nil || fields == nil {
		return value, nil
	}
	name := "repository"
	if t.kind == kindOwner {
		name = "organization"
	}
	fields[name], _ = json.Marshal(t.argument())
	return fields, nil
}

// rulesetsPath is the ruleset REST route of one path below the bound repository or organization.
func (c *Client) rulesetsPath(rest string) string {
	if c.target.kind == kindOwner {
		return c.orgPath(rest)
	}
	return c.repoPath(rest)
}

// Permission messages of the ruleset tools. GitHub decides on every request; a message names what such a
// request needs without claiming what the configured token holds. The fine-grained organization permission
// is documented as a single Administration category covering both reads and changes; this is stated
// cautiously, since Qatlas cannot verify it against GitHub live.
func rulesetReadPermission(t target) string {
	if t.kind == kindOwner {
		return "GitHub refused this token the rulesets of this organization; reading them needs admin:org on " +
			"a classic token, or, as far as GitHub documents it, Administration read access of the " +
			"organization on a fine-grained token"
	}
	return "GitHub refused this token the rulesets of this repository; reading them needs repo on a classic " +
		"token, or Administration: read on a fine-grained token"
}

func rulesetChangePermission(t target) string {
	if t.kind == kindOwner {
		return "GitHub refused this change of an organization ruleset; it needs admin:org on a classic token, " +
			"or, as far as GitHub documents it, Administration read and write access of the organization on a " +
			"fine-grained token"
	}
	return "GitHub refused this change of a repository ruleset; it needs repo on a classic token, or " +
		"Administration: read and write on a fine-grained token"
}

// jsonArrayLen reports how many entries a JSON array argument holds, or 0 when it was left out; the input
// schema already guarantees the value is an array when it is given.
func jsonArrayLen(raw json.RawMessage) int {
	if len(bytes.TrimSpace(raw)) == 0 {
		return 0
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return 0
	}
	return len(items)
}

// checkRulesetSize applies the bounds a JSON schema's maxItems cannot enforce by itself: how many rules and
// bypass actors a definition may hold, and the size of the whole request.
func checkRulesetSize(raw json.RawMessage, rules, bypassActors json.RawMessage) error {
	if len(raw) > maxRulesetInputBytes {
		return invalidRequest(fmt.Sprintf("the ruleset definition must be at most %d KiB", maxRulesetInputBytes>>10))
	}
	if n := jsonArrayLen(rules); n > maxRulesetRules {
		return invalidRequest(fmt.Sprintf("rules accepts at most %d entries", maxRulesetRules))
	}
	if n := jsonArrayLen(bypassActors); n > maxRulesetBypassActors {
		return invalidRequest(fmt.Sprintf("bypass_actors accepts at most %d entries", maxRulesetBypassActors))
	}
	return nil
}

// rulesetDefinition holds the arguments of github.rulesets.create and github.rulesets.update. conditions,
// rules, and bypass_actors are passed through as GitHub's own JSON shapes; see rulesetConditionsSchema.
type rulesetDefinition struct {
	Name         string          `json:"name"`
	Target       string          `json:"target"`
	Enforcement  string          `json:"enforcement"`
	Conditions   json.RawMessage `json:"conditions"`
	Rules        json.RawMessage `json:"rules"`
	BypassActors json.RawMessage `json:"bypass_actors"`
}

// rulesetBody is the REST request body of a create or an update: the given fields only, so a caller that
// leaves conditions, rules, or bypass_actors out sends none of them, rather than an explicit empty one.
func rulesetBody(def *rulesetDefinition) map[string]any {
	body := map[string]any{"name": def.Name, "target": def.Target, "enforcement": def.Enforcement}
	if len(bytes.TrimSpace(def.Conditions)) > 0 {
		body["conditions"] = def.Conditions
	}
	if len(bytes.TrimSpace(def.Rules)) > 0 {
		body["rules"] = def.Rules
	}
	if len(bytes.TrimSpace(def.BypassActors)) > 0 {
		body["bypass_actors"] = def.BypassActors
	}
	return body
}

// rulesetJSON is the ruleset GitHub answers with, decoded once and turned into Ruleset by view. links holds
// the REST address and the web address GitHub names in _links.
type rulesetJSON struct {
	ID                   int64           `json:"id"`
	Name                 string          `json:"name"`
	Target               string          `json:"target"`
	Enforcement          string          `json:"enforcement"`
	NodeID               *string         `json:"node_id"`
	SourceType           *string         `json:"source_type"`
	Source               *string         `json:"source"`
	CurrentUserCanBypass *string         `json:"current_user_can_bypass"`
	CreatedAt            *string         `json:"created_at"`
	UpdatedAt            *string         `json:"updated_at"`
	Conditions           json.RawMessage `json:"conditions"`
	Rules                json.RawMessage `json:"rules"`
	BypassActors         json.RawMessage `json:"bypass_actors"`
	Links                *struct {
		Self *struct {
			Href *string `json:"href"`
		} `json:"self"`
		HTML *struct {
			Href *string `json:"href"`
		} `json:"html"`
	} `json:"_links"`
}

// width projects the members beyond the compact ones.
func (a rulesetJSON) width() RulesetWidth {
	width := RulesetWidth{NodeID: nameText(a.NodeID), SourceType: nameText(a.SourceType),
		CurrentUserCanBypass: nameText(a.CurrentUserCanBypass), CreatedAt: nameText(a.CreatedAt),
		UpdatedAt: nameText(a.UpdatedAt)}
	if a.Links != nil {
		if a.Links.HTML != nil {
			width.URL = address(a.Links.HTML.Href)
		}
		if a.Links.Self != nil {
			if self := address(a.Links.Self.Href); self != nil {
				width.Links = Links{"url": *self}
			}
		}
	}
	return width
}

// normalizedJSONObject reports the object GitHub sent, or {} in its place while it left the field out or
// sent null; it refuses anything that is not a JSON object.
func normalizedJSONObject(raw json.RawMessage) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return json.RawMessage(`{}`), true
	}
	var v map[string]any
	if json.Unmarshal(raw, &v) != nil {
		return nil, false
	}
	return raw, true
}

// normalizedJSONArray is normalizedJSONObject for an array.
func normalizedJSONArray(raw json.RawMessage) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return json.RawMessage(`[]`), true
	}
	var v []json.RawMessage
	if json.Unmarshal(raw, &v) != nil {
		return nil, false
	}
	return raw, true
}

// view turns the decoded answer into a Ruleset, or refuses it as an invalid response: GitHub's own
// conditions, rules, and bypass actors are untrusted data, passed through once their shape is checked.
func (a rulesetJSON) view(op string, change bool) (*Ruleset, error) {
	if a.ID <= 0 || a.Name == "" || a.Target == "" || a.Enforcement == "" {
		return nil, invalidResponse(op, change)
	}
	conditions, ok := normalizedJSONObject(a.Conditions)
	if !ok {
		return nil, invalidResponse(op, change)
	}
	rules, ok := normalizedJSONArray(a.Rules)
	if !ok {
		return nil, invalidResponse(op, change)
	}
	bypassActors, ok := normalizedJSONArray(a.BypassActors)
	if !ok {
		return nil, invalidResponse(op, change)
	}
	return &Ruleset{ID: a.ID, Name: a.Name, Target: a.Target, Enforcement: a.Enforcement, Source: nameText(a.Source),
		RulesetWidth: a.width(), Conditions: conditions, Rules: rules, BypassActors: bypassActors}, nil
}

// RulesetWidth holds the members of GitHub's ruleset beyond the compact ones. Missing and null values stay
// out.
type RulesetWidth struct {
	NodeID               *string `json:"node_id,omitempty"`
	SourceType           *string `json:"source_type,omitempty"`
	CurrentUserCanBypass *string `json:"current_user_can_bypass,omitempty"`
	CreatedAt            *string `json:"created_at,omitempty"`
	UpdatedAt            *string `json:"updated_at,omitempty"`
	URL                  *string `json:"url,omitempty"`
	Links                Links   `json:"links,omitempty"`
}

// RulesetSummary is one entry of github.rulesets.list. Source is where the list was read from; SourceName is
// where GitHub configures the ruleset, which differs for a ruleset inherited from an organization. The
// free-form members are bounded: one beyond the bounds is left out and marked, never cut.
type RulesetSummary struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Target      string  `json:"target"`
	Enforcement string  `json:"enforcement"`
	Source      string  `json:"source"`
	SourceName  *string `json:"source_name,omitempty"`
	RulesetWidth
	Conditions          any  `json:"conditions,omitempty"`
	ConditionsOmitted   bool `json:"conditions_omitted,omitempty"`
	Rules               any  `json:"rules,omitempty"`
	RulesOmitted        bool `json:"rules_omitted,omitempty"`
	BypassActors        any  `json:"bypass_actors,omitempty"`
	BypassActorsOmitted bool `json:"bypass_actors_omitted,omitempty"`
}

// RulesetList is one batch of the rulesets of a repository or an organization.
type RulesetList struct {
	Rulesets   []RulesetSummary `json:"rulesets"`
	NextCursor string           `json:"next_cursor,omitempty"`
	HasMore    bool             `json:"has_more"`
}

// Ruleset is the full ruleset github.rulesets.get, .create, and .update return.
type Ruleset struct {
	ID          int64   `json:"id"`
	Name        string  `json:"name"`
	Target      string  `json:"target"`
	Enforcement string  `json:"enforcement"`
	Source      *string `json:"source,omitempty"`
	RulesetWidth
	Conditions   json.RawMessage `json:"conditions"`
	Rules        json.RawMessage `json:"rules"`
	BypassActors json.RawMessage `json:"bypass_actors"`
}

// RulesetDeleted is the answer to a deleted ruleset.
type RulesetDeleted struct {
	Deleted bool `json:"deleted"`
}

// rulesetTargets are the values the targets filter accepts, in the order the request and the cursor use.
var rulesetTargets = []string{"branch", "tag", "push"}

// rulesetListOptions are the filters and paging of github.rulesets.list.
type rulesetListOptions struct {
	IncludesParents *bool    `json:"includes_parents"`
	Targets         []string `json:"targets"`
	Limit           int      `json:"limit"`
	Cursor          string   `json:"cursor"`

	page, perPage int
	binding       []byte
	targets       string
}

// query is the REST query of one ruleset list page; includes_parents applies only to a repository, since an
// organization has no parent to inherit rulesets from.
func (o *rulesetListOptions) query(bound target) url.Values {
	values := url.Values{"per_page": {strconv.Itoa(o.perPage)}, "page": {strconv.Itoa(o.page)}}
	if bound.kind == kindRepository {
		includesParents := true
		if o.IncludesParents != nil {
			includesParents = *o.IncludesParents
		}
		values.Set("includes_parents", strconv.FormatBool(includesParents))
	}
	if o.targets != "" {
		values.Set("targets", o.targets)
	}
	return values
}

// checkRulesetTargets validates the targets filter and returns it in the fixed order, comma-separated.
func checkRulesetTargets(given []string) (string, error) {
	if given == nil {
		return "", nil
	}
	if len(given) == 0 || len(given) > len(rulesetTargets) {
		return "", invalidRequest("targets must hold one to three of branch, tag, and push")
	}
	seen := map[string]bool{}
	for _, value := range given {
		if !slices.Contains(rulesetTargets, value) {
			return "", invalidRequest("targets accepts only branch, tag, and push")
		}
		if seen[value] {
			return "", invalidRequest("targets must not repeat a value")
		}
		seen[value] = true
	}
	ordered := make([]string, 0, len(given))
	for _, value := range rulesetTargets {
		if seen[value] {
			ordered = append(ordered, value)
		}
	}
	return strings.Join(ordered, ","), nil
}

// normalize applies the bounds of one ruleset list request and resolves the REST page its cursor continues
// at, bound to the target and its filters. The targets filter joins the binding only when given, so a cursor
// of an unfiltered list stays valid.
func (o *rulesetListOptions) normalize(bound target) error {
	limit, err := normalizeLimit(o.Limit)
	if err != nil {
		return err
	}
	if o.targets, err = checkRulesetTargets(o.Targets); err != nil {
		return err
	}
	includesParents := bound.kind == kindRepository && (o.IncludesParents == nil || *o.IncludesParents)
	parts := []any{"rulesets", "list", bound.String(), includesParents}
	if o.targets != "" {
		parts = append(parts, o.targets)
	}
	o.binding = fingerprint(parts...)
	o.page, o.perPage, err = pageOf(o.binding, o.Cursor, limit)
	return err
}

func invokeRulesetsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var options rulesetListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, unreadable(rulesetsList.ID)
	}
	bound, err := selectRulesetTarget(resolved, raw)
	if err != nil {
		return nil, err
	}
	if err := options.normalize(bound); err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.rulesetLocate(client.listRulesets(ctx, &options))
}

func (c *Client) listRulesets(ctx context.Context, o *rulesetListOptions) (*RulesetList, error) {
	const op = "list rulesets"
	var raw []rulesetJSON
	hasNext, err := c.restPage(ctx, op, c.rulesetsPath("rulesets"), o.query(c.target), &raw)
	if err != nil {
		return nil, actionsFailure(err, rulesetReadPermission(c.target))
	}
	source := c.target.argument()
	result := &RulesetList{Rulesets: make([]RulesetSummary, 0, len(raw))}
	for _, entry := range raw {
		if entry.ID <= 0 || entry.Name == "" {
			return nil, invalidEntry(op, "a ruleset")
		}
		summary := RulesetSummary{ID: entry.ID, Name: entry.Name, Target: entry.Target,
			Enforcement: entry.Enforcement, Source: source, SourceName: nameText(entry.Source),
			RulesetWidth: entry.width()}
		var ok bool
		if summary.Conditions, summary.ConditionsOmitted, ok = boundedRulesetMember[map[string]any](entry.Conditions); !ok {
			return nil, invalidEntry(op, "a ruleset")
		}
		if summary.Rules, summary.RulesOmitted, ok = boundedRulesetMember[[]any](entry.Rules); !ok {
			return nil, invalidEntry(op, "a ruleset")
		}
		if summary.BypassActors, summary.BypassActorsOmitted, ok = boundedRulesetMember[[]any](entry.BypassActors); !ok {
			return nil, invalidEntry(op, "a ruleset")
		}
		result.Rulesets = append(result.Rulesets, summary)
	}
	result.HasMore, result.NextCursor = morePage(o.binding, o.page, o.perPage, hasNext)
	return result, nil
}

// Bounds of a free-form ruleset member of a list entry. Beyond any of them it is left out and marked, never
// cut, since a cut rule would state a different rule.
const (
	rulesetMemberDepth   = 8
	rulesetMemberNodes   = 2000
	rulesetMemberText    = 1000
	rulesetMemberKeySize = 200
)

// boundedRulesetMember decodes a free-form member of the type T within the bounds. Null and empty values stay
// out; a value beyond the bounds is left out and reported as omitted; ok is false for a value of another type.
func boundedRulesetMember[T map[string]any | []any](raw json.RawMessage) (value any, omitted, ok bool) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, false, true
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if decoder.Decode(&decoded) != nil {
		return nil, false, false
	}
	typed, isType := decoded.(T)
	if !isType {
		return nil, false, false
	}
	nodes := 0
	cleaned, within := boundedRulesetValue(typed, 1, &nodes)
	if !within {
		return nil, true, true
	}
	if empty, _ := cleaned.(T); len(empty) == 0 {
		return nil, false, true
	}
	return cleaned, false, true
}

// boundedRulesetValue copies a decoded JSON value with control characters of its texts replaced and null
// members dropped, and reports false when it exceeds the depth, node, or text bounds.
func boundedRulesetValue(value any, depth int, nodes *int) (any, bool) {
	*nodes++
	if depth > rulesetMemberDepth || *nodes > rulesetMemberNodes {
		return nil, false
	}
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			if item == nil {
				continue
			}
			if utf8.RuneCountInString(key) > rulesetMemberKeySize {
				return nil, false
			}
			clean, ok := boundedRulesetValue(item, depth+1, nodes)
			if !ok {
				return nil, false
			}
			out[*mustClean(&key, rulesetMemberKeySize)] = clean
		}
		return out, true
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			if item == nil {
				continue
			}
			clean, ok := boundedRulesetValue(item, depth+1, nodes)
			if !ok {
				return nil, false
			}
			out = append(out, clean)
		}
		return out, true
	case string:
		if utf8.RuneCountInString(v) > rulesetMemberText {
			return nil, false
		}
		return *mustClean(&v, rulesetMemberText), true
	}
	return value, true
}

func invokeRulesetsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		ID              int64 `json:"id"`
		IncludesParents *bool `json:"includes_parents"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable(rulesetsGet.ID)
	}
	bound, err := selectRulesetTarget(resolved, raw)
	if err != nil {
		return nil, err
	}
	if arguments.IncludesParents != nil && bound.kind != kindRepository {
		return nil, invalidRequest("includes_parents applies only to a repository")
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.rulesetLocate(client.getRuleset(ctx, arguments.ID, arguments.IncludesParents))
}

func (c *Client) getRuleset(ctx context.Context, id int64, includesParents *bool) (*Ruleset, error) {
	const op = "get ruleset"
	var answer rulesetJSON
	path := c.rulesetsPath("rulesets/" + strconv.FormatInt(id, 10))
	if includesParents != nil {
		path += "?includes_parents=" + strconv.FormatBool(*includesParents)
	}
	if err := c.rest(ctx, op, path, &answer); err != nil {
		return nil, actionsFailure(err, rulesetReadPermission(c.target))
	}
	return answer.view(op, false)
}

func invokeRulesetsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var def rulesetDefinition
	if err := json.Unmarshal(raw, &def); err != nil {
		return nil, unreadable(rulesetsCreate.ID)
	}
	if err := checkRulesetSize(raw, def.Rules, def.BypassActors); err != nil {
		return nil, err
	}
	bound, err := selectRulesetTarget(resolved, raw)
	if err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.rulesetLocate(client.createRuleset(ctx, &def))
}

func (c *Client) createRuleset(ctx context.Context, def *rulesetDefinition) (*Ruleset, error) {
	const op = "create ruleset"
	var answer rulesetJSON
	if err := c.restChange(ctx, op, http.MethodPost, c.rulesetsPath("rulesets"), rulesetBody(def), &answer); err != nil {
		return nil, actionsFailure(err, rulesetChangePermission(c.target))
	}
	return answer.view(op, true)
}

// rulesetUpdate holds the arguments of github.rulesets.update: id plus the same definition create takes.
type rulesetUpdate struct {
	ID int64 `json:"id"`
	rulesetDefinition
}

func invokeRulesetsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments rulesetUpdate
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable(rulesetsUpdate.ID)
	}
	if err := checkRulesetSize(raw, arguments.Rules, arguments.BypassActors); err != nil {
		return nil, err
	}
	bound, err := selectRulesetTarget(resolved, raw)
	if err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.rulesetLocate(client.updateRuleset(ctx, arguments.ID, &arguments.rulesetDefinition))
}

// updateRuleset sends the ruleset's whole new definition in one PUT, never repeated: GitHub replaces a
// ruleset as a whole, so this is idempotent as long as no one else changes it between two identical calls.
func (c *Client) updateRuleset(ctx context.Context, id int64, def *rulesetDefinition) (*Ruleset, error) {
	const op = "update ruleset"
	var answer rulesetJSON
	if err := c.restChange(ctx, op, http.MethodPut, c.rulesetsPath("rulesets/"+strconv.FormatInt(id, 10)),
		rulesetBody(def), &answer); err != nil {
		return nil, actionsFailure(err, rulesetChangePermission(c.target))
	}
	return answer.view(op, true)
}

func invokeRulesetsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var arguments struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, unreadable(rulesetsDelete.ID)
	}
	bound, err := selectRulesetTarget(resolved, raw)
	if err != nil {
		return nil, err
	}
	client, err := openAt(ctx, resolved, secrets, red, bound)
	if err != nil {
		return nil, err
	}
	return bound.rulesetLocate(client.deleteRuleset(ctx, arguments.ID))
}

// deleteRuleset deletes one ruleset in a single request that is never repeated.
func (c *Client) deleteRuleset(ctx context.Context, id int64) (*RulesetDeleted, error) {
	const op = "delete ruleset"
	if err := c.restChange(ctx, op, http.MethodDelete, c.rulesetsPath("rulesets/"+strconv.FormatInt(id, 10)),
		struct{}{}, nil); err != nil {
		return nil, actionsFailure(err, rulesetChangePermission(c.target))
	}
	return &RulesetDeleted{Deleted: true}, nil
}

// rulesetsSubject names the ruleset a path segment below rulesets/ addresses, repository or organization
// alike; a bare rulesets path (a list or a create) names no single ruleset and is left to the caller's
// default subject.
func rulesetsSubject(path string) string {
	rest, ok := strings.CutPrefix(path, "rulesets/")
	if !ok || rest == "" {
		return ""
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		return ""
	}
	return "ruleset " + rest
}
