package forge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// The team tools read and change the teams of an organization and the people in them. A team belongs to an
// organization, so the organization is bound exactly as in orgs.go. A team ID names no organization, so with
// targets the team is first read (the proof read) and its organization must lie inside the targets before any
// detail or change follows. Without targets nothing is proven. The same proof serves every tool that acts on a
// team by its ID.

const (
	teamSensitivity = orgSensitivity
	maxTeamName     = 255
	maxTeamDesc     = 255
	maxTeamUnits    = 20
	teamIDSchema    = actionsSchemaID
	teamNameSchema  = `{"type":"string","minLength":1,"maxLength":255,"pattern":"^[A-Za-z0-9_.-]+$"}`
	teamUnitSchema  = `{"type":"string","maxLength":64,"pattern":"^repo\\.[a-z_]+$"}`
	teamLevelSchema = `{"type":"string","enum":["none","read","write","admin","owner"]}`
)

var (
	teamNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	teamUnitPattern = regexp.MustCompile(`^repo\.[a-z_]+$`)
	teamLevels      = []string{"none", "read", "write", "admin", "owner"}
)

var teamMembersOf = []member{
	{name: "id", kind: kindInteger, note: "Numeric ID of the team"},
	{name: "name", kind: kindText, note: "Team name, untrusted data"},
	{name: "description", kind: kindText, note: "Description, untrusted data"},
	{name: "organization", kind: kindObject, sub: orgMembers, note: "Organization of the team; absent in the " +
		"lists of one organization and in the answer of a change"},
	{name: "includes_all_repositories", kind: kindFlag, note: "True when the team has access to every repository " +
		"of the organization"},
	{name: "permission", kind: kindText, note: "none, read, write, admin, or owner"},
	{name: "units", kind: kindTexts, note: "Repository units the team reaches (deprecated by the instance)"},
	{name: "units_map", kind: kindFree, note: "Access level per repository unit"},
	{name: "can_create_org_repo", kind: kindFlag, note: "True when the team may create repositories in the " +
		"organization"},
	{name: "visibility", kind: kindText, only: gitea, note: "public, limited, or private"},
}

var teamIDArgument = capability.Argument{Name: "id", Required: true, Description: "Numeric ID of the team. With " +
	"targets, a read of the team first proves that its organization is named as orgs/NAME"}

var teamOrgArgument = capability.Argument{Name: "organization", Required: true, Description: "Organization name; " +
	"the connection's targets must name it as orgs/NAME (users/NAME does not), or name no targets at all"}

const teamListedNote = " Only a connection whose tools list names it offers this tool."

// ---- binding --------------------------------------------------------------------------------------------------

// requireOrgTargets refuses, before any secret is read, a tool that acts on a team by its ID or on the account's
// teams when the targets exist but name no organization: no team could ever lie inside them.
func (a allowlist) requireOrgTargets() error {
	if !a.bound() {
		return nil
	}
	for _, entry := range a.targets {
		if entry.kind == kindOwner && entry.scope == "orgs" {
			return nil
		}
	}
	return invalidArgument("this connection's targets name no organization as orgs/NAME, so no team lies inside " +
		"them; use a connection that names one, or none")
}

func teamPath(id int64) string { return "/teams/" + itoa64(id) }

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

// teamOrganization reads the name of the organization a team object reports and whether every name it reports
// lies inside the targets. Without any name it proves nothing.
func (a allowlist) teamOrganization(team object) (string, bool) {
	org, ok := team.child("organization")
	if !ok {
		return "", false
	}
	var names []string
	for _, key := range []string{"username", "name"} {
		if value, ok := org.text(key); ok && value != "" {
			names = append(names, value)
		}
	}
	if len(names) == 0 {
		return "", false
	}
	for _, name := range names {
		if !validName(name) || !a.namesOwnerOfScope("orgs", name) {
			return "", false
		}
	}
	return names[0], true
}

// readTeam is the proof read of a team: it reads the team, checks that the answer is the team asked for, and,
// with targets, that its organization lies inside them. A team outside them is refused like a team the token
// does not reach, without a name. It returns the team and its organization.
func readTeam(ctx context.Context, c *Client, allowed allowlist, op string, id int64) (object, string, error) {
	var team object
	if _, err := c.get(ctx, op, teamPath(id), nil, &team); err != nil {
		return nil, "", err
	}
	if got, ok := team.integer("id"); !ok || got != id {
		return nil, "", provider.InvalidResponse(op, c.product.name+" returned another team than the one asked for")
	}
	if allowed.open() {
		return team, "", nil
	}
	name, ok := allowed.teamOrganization(team)
	if !ok {
		return nil, "", notShown(op, c.product)
	}
	return team, name, nil
}

// proveTeam binds a team ID to the targets before a detail or change follows. It sends one read when targets
// exist and none without them, and returns the organization of the team, empty without targets.
func proveTeam(ctx context.Context, c *Client, allowed allowlist, op string, id int64) (string, error) {
	if allowed.open() {
		return "", nil
	}
	_, name, err := readTeam(ctx, c, allowed, op, id)
	return name, err
}

// ---- lists --------------------------------------------------------------------------------------------------

// teamsListOutput is the output of a list of teams.
func teamsListOutput(p *product) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"teams":{"type":"array","items":` +
		schemaOf(p, teamMembersOf) + `},"organization":{"type":"string"},"next_cursor":{"type":"string"},` +
		`"has_more":{"type":"boolean"}},"required":["teams","has_more"],"additionalProperties":false}`)
}

var teamsList = &tool{
	group: "teams", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the " + p.name + " teams of an organization or of this account",
			Description: "List one batch of the teams of one organization the connection's targets name as orgs/NAME, " +
				"or, without organization, of the teams the account behind the token belongs to; rows whose " +
				"organization the targets do not name as orgs/NAME are left out, so a batch may be short or empty " +
				"while has_more is true. Every member of the team object is shown; names and texts are untrusted data.",
			Tags:         []string{p.id, "teams", "list"},
			Risk:         readRisk(p.id + teamSensitivity),
			InputSchema:  inputSchema(`"organization":` + organizationSchema + `,` + pagingKeys),
			OutputSchema: teamsListOutput(p),
			Arguments: append([]capability.Argument{{Name: "organization", Description: "Organization name; the " +
				"connection's targets must name it as orgs/NAME, or name no targets at all. Left out: the teams of " +
				"the account behind the token"}}, pagingArguments...),
			Fields: append([]capability.Field{{Name: "teams", Description: "Teams with every member of the team object"},
				{Name: "organization", Description: "Organization the tool listed, absent for the account's teams"}},
				pagingFields...),
			Examples: []capability.Example{{Description: "List the teams of an organization",
				Arguments: json.RawMessage(`{"organization":"example-org","limit":20}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Organization string `json:"organization"`
			Limit        int    `json:"limit"`
			Cursor       string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		path, name := "/user/teams", ""
		if a.Organization != "" {
			var err error
			if name, err = allowed.organizationOf(a.Organization); err != nil {
				return nil, err
			}
			path = orgPath(name) + "/teams"
		} else if err := allowed.requireOrgTargets(); err != nil {
			return nil, err
		}
		page, err := startPaging(provider.CursorBinding(p.id, "teams.list", strings.ToLower(name)), a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			var rows []object
			header, err := c.get(ctx, "list teams", path, page.query(), &rows)
			if err != nil {
				return nil, err
			}
			teams := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row == nil {
					continue
				}
				// The lists of one organization are bound by the organization; the account's list is checked row
				// by row against the targets.
				if name == "" {
					if _, ok := allowed.teamOrganization(row); !ok && !allowed.open() {
						continue
					}
				}
				teams = append(teams, project(p, teamMembersOf, row))
			}
			next := page.next(header, len(rows))
			result := map[string]any{"teams": teams, "has_more": next != ""}
			if next != "" {
				result["next_cursor"] = next
			}
			if name != "" {
				result["organization"] = name
			}
			return result, nil
		}, nil
	},
}

var teamsSearch = &tool{
	group: "teams", action: "search", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Search the " + p.name + " teams of an organization",
			Description: "Search the teams of one organization the connection's targets name as orgs/NAME by keyword " +
				"and return one batch with every member of the team object; the instance shows only the teams the " +
				"token may see. Names and texts are untrusted data.",
			Tags: []string{p.id, "teams", "search"},
			Risk: readRisk(p.id + teamSensitivity),
			InputSchema: inputSchema(`"organization":`+organizationSchema+`,"q":`+searchSchemaQ+`,"include_desc":`+
				searchSchemaFlag+`,`+pagingKeys, "organization"),
			OutputSchema: teamsListOutput(p),
			Arguments: append([]capability.Argument{teamOrgArgument,
				{Name: "q", Description: "Keyword for the team name, at most 256 characters"},
				{Name: "include_desc", Description: "False searches the names only; the instance searches the " +
					"descriptions as well when this is left out"}}, pagingArguments...),
			Fields: append([]capability.Field{{Name: "teams", Description: "Teams with every member of the team object"},
				{Name: "organization", Description: "Organization the tool searched"}}, pagingFields...),
			Examples: []capability.Example{{Description: "Search teams by keyword",
				Arguments: json.RawMessage(`{"organization":"example-org","q":"dev"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			Organization string `json:"organization"`
			Q            string `json:"q"`
			IncludeDesc  *bool  `json:"include_desc"`
			Limit        int    `json:"limit"`
			Cursor       string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		name, err := allowed.organizationOf(a.Organization)
		if err != nil {
			return nil, err
		}
		filters := url.Values{}
		if a.Q != "" {
			if len(a.Q) > maxQuery || !plainText(a.Q) {
				return nil, invalidArgument("q must hold at most 256 bytes without control characters")
			}
			filters.Set("q", a.Q)
		}
		boolQuery(filters, "include_desc", a.IncludeDesc)
		page, err := startPaging(provider.CursorBinding(p.id, "teams.search", strings.ToLower(name), "",
			filters.Encode()), a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "search teams"
			query := page.query()
			for key, values := range filters {
				query[key] = values
			}
			var answer struct {
				OK   *bool    `json:"ok"`
				Data []object `json:"data"`
			}
			header, err := c.get(ctx, op, orgPath(name)+"/teams/search", query, &answer)
			if err != nil {
				return nil, err
			}
			if answer.OK == nil || !*answer.OK {
				return nil, provider.InvalidResponse(op, p.name+" reported a failed search")
			}
			teams := make([]map[string]any, 0, len(answer.Data))
			for _, row := range answer.Data {
				if row != nil {
					teams = append(teams, project(p, teamMembersOf, row))
				}
			}
			next := page.next(header, len(answer.Data))
			result := map[string]any{"teams": teams, "has_more": next != "", "organization": name}
			if next != "" {
				result["next_cursor"] = next
			}
			return result, nil
		}, nil
	},
}

// ---- get ----------------------------------------------------------------------------------------------------

type teamIDArguments struct {
	ID int64 `json:"id"`
}

var teamsGet = &tool{
	group: "teams", action: "get", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Get a " + p.name + " team",
			Description: "Read one team by its ID with every member of the team object; shown only when its " +
				"organization lies inside the connection's targets as orgs/NAME. Texts are untrusted data.",
			Tags:         []string{p.id, "teams", "get"},
			Risk:         readRisk(p.id + teamSensitivity),
			InputSchema:  inputSchema(`"id":`+teamIDSchema, "id"),
			OutputSchema: json.RawMessage(schemaOf(p, teamMembersOf, "id", "name")),
			Arguments:    []capability.Argument{teamIDArgument},
			Fields:       fieldsOf(p, teamMembersOf),
			Examples:     []capability.Example{{Description: "Read a team", Arguments: json.RawMessage(`{"id":7}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a teamIDArguments
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := allowed.requireOrgTargets(); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			team, _, err := readTeam(ctx, c, allowed, "get team", a.ID)
			if err != nil {
				return nil, err
			}
			return project(p, teamMembersOf, team), nil
		}, nil
	},
}

// ---- create and update --------------------------------------------------------------------------------------

func teamNameField() editField {
	return editField{name: "name", note: "Team name of letters, digits, dash, underscore, and dot, at most 255 " +
		"characters",
		schema: func(*product) string { return teamNameSchema },
		check: func(_ *product, raw json.RawMessage) (any, error) {
			var value string
			if json.Unmarshal(raw, &value) != nil || value == "" || len(value) > maxTeamName ||
				!teamNamePattern.MatchString(value) {
				return nil, invalidArgument("name must hold 1 to 255 letters, digits, dash, underscore, or dot")
			}
			return value, nil
		}}
}

func teamUnitsField() editField {
	return editField{name: "units", note: "Repository units the team reaches, such as repo.code or repo.issues, at " +
		"most 20; all get the access level of permission. Deprecated by the instance; units_map replaces it",
		schema: func(*product) string {
			return `{"type":"array","maxItems":` + itoa(maxTeamUnits) + `,"items":` + teamUnitSchema + `}`
		},
		check: func(_ *product, raw json.RawMessage) (any, error) {
			var values []string
			if json.Unmarshal(raw, &values) != nil || len(values) > maxTeamUnits {
				return nil, invalidArgument("units must be a list of at most 20 unit keys")
			}
			for _, value := range values {
				if len(value) > 64 || !teamUnitPattern.MatchString(value) {
					return nil, invalidArgument("units must hold unit keys such as repo.code")
				}
			}
			return values, nil
		}}
}

func teamUnitsMapField() editField {
	return editField{name: "units_map", note: "Access level per repository unit, as {\"repo.code\":\"read\"}, at " +
		"most 20 units; the levels are none, read, write, admin, and owner",
		schema: func(*product) string {
			return `{"type":"object","maxProperties":` + itoa(maxTeamUnits) + `,"additionalProperties":` +
				teamLevelSchema + `}`
		},
		check: func(_ *product, raw json.RawMessage) (any, error) {
			var values map[string]string
			if json.Unmarshal(raw, &values) != nil || values == nil || len(values) > maxTeamUnits {
				return nil, invalidArgument("units_map must map at most 20 unit keys to access levels")
			}
			for key, level := range values {
				if len(key) > 64 || !teamUnitPattern.MatchString(key) || !contains(teamLevels, level) {
					return nil, invalidArgument("units_map must map unit keys such as repo.code to none, read, " +
						"write, admin, or owner")
				}
			}
			return values, nil
		}}
}

// teamFields are the settings both CreateTeamOption and EditTeamOption document; visibility is Gitea's.
var teamFields = []editField{
	teamNameField(),
	orgTextField("description", "Description, at most 255 characters", maxTeamDesc, true),
	enumFieldFor("permission", "read, write, or admin; with admin the team reaches every unit", nil,
		[]string{"read", "write", "admin"}),
	flagField("includes_all_repositories", "True to give the team access to every repository of the organization; "+
		"the instance applies it only together with permission when the team is changed", nil),
	flagField("can_create_org_repo", "True to let the team create repositories in the organization", nil),
	teamUnitsField(),
	teamUnitsMapField(),
	enumFieldFor("visibility", "public, limited, or private; Gitea only", gitea, []string{"public", "limited", "private"}),
}

func teamFieldProperties(p *product, skip string) string {
	var properties []string
	for _, field := range teamFields {
		if field.offered(p) && field.name != skip {
			properties = append(properties, `"`+field.name+`":`+field.schema(p))
		}
	}
	return strings.Join(properties, ",")
}

func teamFieldArguments(p *product) []capability.Argument {
	var arguments []capability.Argument
	for _, field := range teamFields {
		if field.offered(p) {
			arguments = append(arguments, capability.Argument{Name: field.name, Description: field.note})
		}
	}
	return arguments
}

// teamBody checks the settings given and returns the request body. A setting only the other product documents
// is refused before anything is sent.
func teamBody(p *product, given map[string]json.RawMessage) (map[string]any, error) {
	body := map[string]any{}
	for key, value := range given {
		var found *editField
		for i := range teamFields {
			if teamFields[i].name == key {
				found = &teamFields[i]
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
	return body, nil
}

var teamsCreate = &tool{
	group: "teams", action: "create", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Create a " + p.name + " team",
			Description: "Create one team in an organization the connection's targets name as orgs/NAME with one " +
				"request. Unless permission is admin, units or units_map must name the units the team reaches. The " +
				"token must own the organization.",
			Tags:         []string{p.id, "teams", "create"},
			Risk:         changeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent, p.id+teamSensitivity),
			InputSchema:  inputSchema(orgProperties(teamFieldProperties(p, "")), "organization", "name"),
			OutputSchema: json.RawMessage(schemaOf(p, teamMembersOf, "id", "name")),
			Arguments:    append([]capability.Argument{teamOrgArgument}, withRequired(teamFieldArguments(p), "name")...),
			Fields:       fieldsOf(p, teamMembersOf),
			Examples: []capability.Example{{Description: "Create a team",
				Arguments: json.RawMessage(`{"organization":"example-org","name":"developers","permission":"write",` +
					`"units_map":{"repo.code":"write","repo.issues":"write"}}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		given, err := readGiven(raw)
		if err != nil {
			return nil, err
		}
		value, err := takeText(given, "organization")
		if err != nil {
			return nil, err
		}
		name, err := allowed.organizationOf(value)
		if err != nil {
			return nil, err
		}
		body, err := teamBody(p, given)
		if err != nil {
			return nil, err
		}
		teamName, _ := body["name"].(string)
		if teamName == "" {
			return nil, invalidArgument("name is required")
		}
		// The instance fails with a server error for a team without units unless it has the admin permission.
		_, units := body["units"]
		_, unitsMap := body["units_map"]
		if body["permission"] != "admin" && !units && !unitsMap {
			return nil, invalidArgument("pass units or units_map unless permission is admin")
		}
		return func(ctx context.Context, c *Client) (any, error) {
			const op = "create team"
			var created object
			if err := c.change(ctx, op, http.MethodPost, orgPath(name)+"/teams", body, &created); err != nil {
				return nil, err
			}
			if got, ok := created.text("name"); !ok || !strings.EqualFold(got, teamName) {
				return nil, unsure(provider.InvalidResponse(op, p.name+" returned another team than the one created"))
			}
			return project(p, teamMembersOf, created), nil
		}, nil
	},
}

func withRequired(arguments []capability.Argument, name string) []capability.Argument {
	for i := range arguments {
		if arguments[i].Name == name {
			arguments[i].Required = true
		}
	}
	return arguments
}

var teamsUpdate = &tool{
	group: "teams", action: "update", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Change a " + p.name + " team",
			Description: "Change the settings of one team by its ID with one request; only the arguments given " +
				"change. With targets, a read of the team first proves that its organization is named as orgs/NAME. " +
				"The token must own the organization; the instance keeps the owners team's permission.",
			Tags:         []string{p.id, "teams", "update"},
			Risk:         changeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent, p.id+teamSensitivity),
			InputSchema:  inputSchema(`"id":`+teamIDSchema+`,`+teamFieldProperties(p, ""), "id"),
			OutputSchema: json.RawMessage(schemaOf(p, teamMembersOf, "id", "name")),
			Arguments:    append([]capability.Argument{teamIDArgument}, teamFieldArguments(p)...),
			Fields:       fieldsOf(p, teamMembersOf),
			Examples: []capability.Example{{Description: "Change the description of a team",
				Arguments: json.RawMessage(`{"id":7,"description":"Developers"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		given, err := readGiven(raw)
		if err != nil {
			return nil, err
		}
		var id int64
		if value, ok := given["id"]; !ok || json.Unmarshal(value, &id) != nil || id < 1 {
			return nil, invalidArgument("id must be a team ID")
		}
		delete(given, "id")
		if err := allowed.requireOrgTargets(); err != nil {
			return nil, err
		}
		body, err := teamBody(p, given)
		if err != nil {
			return nil, err
		}
		if len(body) == 0 {
			return nil, invalidArgument("pass at least one setting to change")
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if _, err := proveTeam(ctx, c, allowed, "read team", id); err != nil {
				return nil, err
			}
			const op = "change team"
			var changed object
			if err := c.change(ctx, op, http.MethodPatch, teamPath(id), body, &changed); err != nil {
				return nil, err
			}
			if got, ok := changed.integer("id"); !ok || got != id {
				return nil, unsure(provider.InvalidResponse(op, p.name+" returned another team than the one asked for"))
			}
			return project(p, teamMembersOf, changed), nil
		}, nil
	},
}

var teamsDelete = &tool{
	group: "teams", action: "delete", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Delete a " + p.name + " team",
			Description: "Delete one team by its ID for good; its members lose the access the team gave. With " +
				"targets, a read of the team first proves that its organization is named as orgs/NAME. The token " +
				"must own the organization." + teamListedNote,
			Tags:                  []string{p.id, "teams", "delete"},
			Risk:                  changeRisk(capability.EffectDelete, capability.IdempotencyIdempotent, p.id+teamSensitivity),
			RequiresToolAllowList: true,
			InputSchema:           inputSchema(`"id":`+teamIDSchema, "id"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"deleted":` +
				`{"type":"boolean"}},"required":["id","deleted"],"additionalProperties":false}`),
			Arguments: []capability.Argument{teamIDArgument},
			Fields: []capability.Field{{Name: "id", Description: "Team the tool acted on"},
				{Name: "deleted", Description: "True once the instance accepted the request"}},
			Examples: []capability.Example{{Description: "Delete a team", Arguments: json.RawMessage(`{"id":7}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a teamIDArguments
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := allowed.requireOrgTargets(); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if _, err := proveTeam(ctx, c, allowed, "read team", a.ID); err != nil {
				return nil, err
			}
			if err := c.change(ctx, "delete team", http.MethodDelete, teamPath(a.ID), nil, nil); err != nil {
				return nil, err
			}
			return map[string]any{"id": a.ID, "deleted": true}, nil
		}, nil
	},
}

// ---- activities ---------------------------------------------------------------------------------------------

var teamActivitiesList = &tool{
	group: "team-activities", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the " + p.name + " activities of a team",
			Description: "List one batch of the activity feed of one team by its ID with every member of each " +
				"activity; with targets, the team's organization must be named as orgs/NAME and only activities of " +
				"its repositories inside the targets are shown. The feed names the users who acted, and its texts " +
				"are untrusted data.",
			Tags:        []string{p.id, "team-activities", "list"},
			Risk:        readRisk(p.id + activitySensitivity),
			InputSchema: inputSchema(`"id":`+teamIDSchema+`,"date":`+activitySchemaDt+`,`+pagingKeys, "id"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"activities":{"type":"array","items":` +
				schemaOf(p, activityMembers) + `},"id":{"type":"integer"},"next_cursor":{"type":"string"},` +
				`"has_more":{"type":"boolean"}},"required":["activities","id","has_more"],"additionalProperties":false}`),
			Arguments: append([]capability.Argument{teamIDArgument,
				{Name: "date", Description: "Only activities of this day, YYYY-MM-DD"}}, pagingArguments...),
			Fields: append([]capability.Field{{Name: "activities", Description: "Activities with every member of the " +
				"activity object"}, {Name: "id", Description: "Team the tool listed"}}, pagingFields...),
			Examples: []capability.Example{{Description: "List the activities of a day",
				Arguments: json.RawMessage(`{"id":7,"date":"2030-01-02"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			ID     int64  `json:"id"`
			Date   string `json:"date"`
			Limit  int    `json:"limit"`
			Cursor string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := allowed.requireOrgTargets(); err != nil {
			return nil, err
		}
		filters := url.Values{}
		if a.Date != "" {
			if _, err := time.Parse("2006-01-02", a.Date); err != nil {
				return nil, invalidArgument("date must be a day as YYYY-MM-DD")
			}
			filters.Set("date", a.Date)
		}
		page, err := startPaging(provider.CursorBinding(p.id, "team-activities.list", itoa64(a.ID), "",
			filters.Encode()), a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			org, err := proveTeam(ctx, c, allowed, "read team", a.ID)
			if err != nil {
				return nil, err
			}
			query := page.query()
			for key, values := range filters {
				query[key] = values
			}
			var rows []object
			header, err := c.get(ctx, "list team activities", teamPath(a.ID)+"/activities/feeds", query, &rows)
			if err != nil {
				return nil, err
			}
			activities := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row == nil {
					continue
				}
				if !allowed.open() {
					// An activity without a repository proves nothing about where it belongs; the repository must
					// be the team's organization's own and inside the targets.
					inner, ok := row.child("repo")
					if !ok {
						continue
					}
					fullName, _ := inner.text("full_name")
					if owner, _, _ := strings.Cut(fullName, "/"); !allowed.allowsFullName(fullName) ||
						!strings.EqualFold(owner, org) {
						continue
					}
				}
				activities = append(activities, project(p, activityMembers, row))
			}
			next := page.next(header, len(rows))
			result := map[string]any{"activities": activities, "has_more": next != "", "id": a.ID}
			if next != "" {
				result["next_cursor"] = next
			}
			return result, nil
		}, nil
	},
}

// ---- members ------------------------------------------------------------------------------------------------

var teamMembersList = &tool{
	group: "team-members", action: "list", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "List the members of a " + p.name + " team",
			Description: "List one batch of the members of one team by its ID with every member of the user object; " +
				"with targets, the team's organization must be named as orgs/NAME. Names and texts are untrusted " +
				"personal data.",
			Tags:        []string{p.id, "team-members", "list"},
			Risk:        readRisk(p.id + orgPeopleSensitivity),
			InputSchema: inputSchema(`"id":`+teamIDSchema+`,`+pagingKeys, "id"),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"members":` +
				`{"type":"array","items":` + schemaOf(p, personMembers) + `},"next_cursor":{"type":"string"},` +
				`"has_more":{"type":"boolean"}},"required":["id","members","has_more"],"additionalProperties":false}`),
			Arguments: append([]capability.Argument{teamIDArgument}, pagingArguments...),
			Fields: append([]capability.Field{{Name: "id", Description: "Team the tool listed"},
				{Name: "members", Description: "Users with every member of the user object"}}, pagingFields...),
			Examples: []capability.Example{{Description: "List the members of a team",
				Arguments: json.RawMessage(`{"id":7,"limit":20}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			ID     int64  `json:"id"`
			Limit  int    `json:"limit"`
			Cursor string `json:"cursor"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := allowed.requireOrgTargets(); err != nil {
			return nil, err
		}
		page, err := startPaging(provider.CursorBinding(p.id, "team-members.list", itoa64(a.ID)), a.Limit, a.Cursor)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if _, err := proveTeam(ctx, c, allowed, "read team", a.ID); err != nil {
				return nil, err
			}
			var rows []object
			header, err := c.get(ctx, "list team members", teamPath(a.ID)+"/members", page.query(), &rows)
			if err != nil {
				return nil, err
			}
			members := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if row != nil {
					members = append(members, project(p, personMembers, row))
				}
			}
			next := page.next(header, len(rows))
			result := map[string]any{"id": a.ID, "members": members, "has_more": next != ""}
			if next != "" {
				result["next_cursor"] = next
			}
			return result, nil
		}, nil
	},
}

var teamUserProperties = `"id":` + teamIDSchema + `,"username":` + issueSchemaLogin

var teamUserArguments = []capability.Argument{teamIDArgument, orgUserArgument}

func teamMemberOutput(key string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},"username":{"type":"string"},"` +
		key + `":{"type":"boolean"}},"required":["id","username","` + key + `"],"additionalProperties":false}`)
}

var teamMembersCheck = &tool{
	group: "team-members", action: "check", since: everywhere,
	describe: func(p *product) capability.Descriptor {
		return capability.Descriptor{
			Title: "Check whether a user is a member of a " + p.name + " team",
			Description: "Ask whether one user belongs to one team by its ID; with targets, the team's organization " +
				"must be named as orgs/NAME. The answer is a boolean; a 404 is reported as no member, although the " +
				"instance also answers it for a user the token does not reach.",
			Tags:         []string{p.id, "team-members", "check"},
			Risk:         readRisk(p.id + orgPeopleSensitivity),
			InputSchema:  inputSchema(teamUserProperties, "id", "username"),
			OutputSchema: teamMemberOutput("member"),
			Arguments:    teamUserArguments,
			Fields: []capability.Field{{Name: "id", Description: "Team the tool asked"},
				{Name: "username", Description: "User that was checked"},
				{Name: "member", Description: "True when the instance answered yes; false when it answered 404"}},
			Examples: []capability.Example{{Description: "Check a user", Arguments: json.RawMessage(`{"id":7,"username":"octo"}`)}},
		}
	},
	prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
		var a struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		}
		if err := decodeArguments(raw, &a); err != nil {
			return nil, err
		}
		if err := orgUserOf(a.Username); err != nil {
			return nil, err
		}
		if err := allowed.requireOrgTargets(); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c *Client) (any, error) {
			if _, err := proveTeam(ctx, c, allowed, "read team", a.ID); err != nil {
				return nil, err
			}
			code, err := c.status(ctx, "check team member", teamPath(a.ID)+"/members/"+escape(a.Username))
			result := map[string]any{"id": a.ID, "username": a.Username}
			switch {
			case err == nil:
				result["member"] = true
			case code == http.StatusNotFound:
				result["member"] = false
			default:
				return nil, err
			}
			return result, nil
		}, nil
	},
}

func newTeamMemberChange(action, key, method, title, text string, idempotency capability.Idempotency) *tool {
	return &tool{
		group: "team-members", action: action, since: everywhere,
		describe: func(p *product) capability.Descriptor {
			return capability.Descriptor{
				Title:        strings.ReplaceAll(title, "%s", p.name),
				Description:  text,
				Tags:         []string{p.id, "team-members", action, "update"},
				Risk:         changeRisk(capability.EffectUpdate, idempotency, p.id+orgPeopleSensitivity),
				InputSchema:  inputSchema(teamUserProperties, "id", "username"),
				OutputSchema: teamMemberOutput(key),
				Arguments:    teamUserArguments,
				Fields: []capability.Field{{Name: "id", Description: "Team the tool acted on"},
					{Name: "username", Description: "User the tool acted on"},
					{Name: key, Description: "True once the instance accepted the request"}},
				Examples: []capability.Example{{Description: strings.ReplaceAll(title, "%s", p.name),
					Arguments: json.RawMessage(`{"id":7,"username":"octo"}`)}},
			}
		},
		prepare: func(p *product, allowed allowlist, raw json.RawMessage) (run, error) {
			var a struct {
				ID       int64  `json:"id"`
				Username string `json:"username"`
			}
			if err := decodeArguments(raw, &a); err != nil {
				return nil, err
			}
			if err := orgUserOf(a.Username); err != nil {
				return nil, err
			}
			if err := allowed.requireOrgTargets(); err != nil {
				return nil, err
			}
			return func(ctx context.Context, c *Client) (any, error) {
				if _, err := proveTeam(ctx, c, allowed, "read team", a.ID); err != nil {
					return nil, err
				}
				if err := c.change(ctx, action+" team member", method, teamPath(a.ID)+"/members/"+escape(a.Username),
					nil, nil); err != nil {
					return nil, err
				}
				return map[string]any{"id": a.ID, "username": a.Username, key: true}, nil
			}, nil
		},
	}
}

var teamMembersAdd = newTeamMemberChange("add", "added", http.MethodPut, "Add a user to a %s team",
	"Add one existing user to one team by its ID; the user must already exist, an invitation by email address is "+
		"not offered. With targets, a read of the team first proves that its organization is named as orgs/NAME. "+
		"The token must own the organization.", capability.IdempotencyIdempotent)

var teamMembersRemove = newTeamMemberChange("remove", "removed", http.MethodDelete, "Remove a user from a %s team",
	"Remove one user from one team by its ID; the user loses the access the team gave. With targets, a read of the "+
		"team first proves that its organization is named as orgs/NAME. The token must own the organization.",
	capability.IdempotencyIdempotent)
