package makeapi

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The four tools of this file read only the connection's own bound team and its organization. None takes a
// team or organization argument: the identifiers always come from the connection's targets (team) or from
// the bound team's own answer (organization), so an agent can never name a foreign one.
// API: GET /teams/{teamId} and GET /teams/{teamId}/usage (teams:read), GET /teams/{teamId}/user-team-roles
// (teams:read), GET /organizations/{organizationId} (organizations:read), all checked 2026-10-02 against
// developers.make.com's published OpenAPI documents, not a live account.

const (
	// maxUsageDays bounds the daily usage records of make.team.usage; Make documents 30 days.
	maxUsageDays = 31
	// maxMembers bounds the roles one make.team.members answer returns.
	maxMembers = 200
	// maxLicenseEntries and maxLicenseKeyLength bound the license limits make.organization.get returns.
	maxLicenseEntries   = 64
	maxLicenseKeyLength = 64
)

const emptyInput = `{"type":"object","properties":{},"additionalProperties":false}`

var teamDetailSchema = `{"type":"object","properties":{` +
	`"id":{"type":"integer"},"name":{"type":"string"},"organization_id":{"type":"integer"},` +
	`"is_paused":{"type":"boolean"},"active_scenarios":{"type":"integer"},"active_apps":{"type":"integer"},` +
	`"operations_limit":{"type":"integer"},"transfer_limit":{"type":"integer"},` +
	`"consumed_operations":{"type":"integer"},"consumed_transfer":{"type":"integer"},` +
	`"consumed_centicredits":{"type":"integer"}},"required":["id","name","organization_id"],` +
	`"additionalProperties":false}`

var teamGet = capability.Descriptor{
	ID: Provider + ".team.get", Version: 1, Title: "Get the bound Make team",
	Description: "Read the bound team's name, organization, pause state, and limits and consumption; takes no " +
		"arguments, because the team is always the connection's own",
	Tags: []string{"make", "team", "get", "automation"}, Risk: makeReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(emptyInput), OutputSchema: json.RawMessage(teamDetailSchema),
	Fields: []capability.Field{
		{Name: "id", Description: "Team identifier; always the connection's bound team"},
		{Name: "name", Description: "Team name, untrusted data"},
		{Name: "organization_id", Description: "Organization the team belongs to"},
		{Name: "is_paused", Description: "True when Make reports the team as paused"},
		{Name: "active_scenarios", Description: "Active scenarios of the team"},
		{Name: "active_apps", Description: "Active apps of the team"},
		{Name: "operations_limit", Description: "Operations limit, as Make reports it"},
		{Name: "transfer_limit", Description: "Data transfer limit in bytes, as Make reports it"},
		{Name: "consumed_operations", Description: "Operations consumed in the current period"},
		{Name: "consumed_transfer", Description: "Data transferred in the current period, in bytes"},
		{Name: "consumed_centicredits", Description: "Credits consumed, in hundredths"},
	},
	Examples: []capability.Example{{Description: "Read the bound team", Arguments: json.RawMessage(`{}`)}},
}

var teamUsage = capability.Descriptor{
	ID: Provider + ".team.usage", Version: 1, Title: "Get the bound Make team's usage",
	Description: "Read the bound team's daily usage records (Make documents the last 30 days); takes no arguments",
	Tags:        []string{"make", "team", "usage", "automation"}, Risk: makeReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(emptyInput),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":{"type":"integer"},` +
		`"days":{"type":"array","items":{"type":"object","properties":{"date":{"type":"string"},` +
		`"operations":{"type":"integer"},"data_transfer":{"type":"integer"},"centicredits":{"type":"integer"}},` +
		`"required":["date"],"additionalProperties":false}},"count":{"type":"integer"}},` +
		`"required":["team_id","days","count"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "team_id", Description: "The connection's bound team"},
		{Name: "days", Description: "Daily records: date, operations, data_transfer in bytes, centicredits"},
		{Name: "count", Description: "Number of daily records returned, at most " + strconv.Itoa(maxUsageDays)},
	},
	Examples: []capability.Example{{Description: "Read the bound team's usage", Arguments: json.RawMessage(`{}`)}},
}

var teamMembers = capability.Descriptor{
	ID: Provider + ".team.members", Version: 1, Title: "List members of the bound Make team",
	Description: "List the bound team's members as user id and role id (Make's user-team-roles); no names or " +
		"email addresses; takes no arguments",
	Tags: []string{"make", "team", "members", "automation"}, Risk: makeReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(emptyInput),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"team_id":{"type":"integer"},` +
		`"members":{"type":"array","items":{"type":"object","properties":{"user_id":{"type":"integer"},` +
		`"role_id":{"type":"integer"},"changeable":{"type":"boolean"},"sso_pending":{"type":"boolean"}},` +
		`"required":["user_id","role_id"],"additionalProperties":false}},"count":{"type":"integer"},` +
		`"truncated":{"type":"boolean"}},"required":["team_id","members","count","truncated"],` +
		`"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "team_id", Description: "The connection's bound team"},
		{Name: "members", Description: "user_id, role_id (Make's usersRoleId), changeable, sso_pending"},
		{Name: "count", Description: "Members returned, at most " + strconv.Itoa(maxMembers)},
		{Name: "truncated", Description: "True when the page was full and further members may exist"},
	},
	Examples: []capability.Example{{Description: "List the bound team's members", Arguments: json.RawMessage(`{}`)}},
}

var organizationGet = capability.Descriptor{
	ID: Provider + ".organization.get", Version: 1, Title: "Get the bound team's Make organization",
	Description: "Read the license limits and zone of the organization the bound team belongs to; the " +
		"organization comes from the team's own answer, never from an argument",
	Tags: []string{"make", "organization", "get", "automation"}, Risk: makeReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(emptyInput),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"},` +
		`"zone":{"type":"string"},"license":{"type":"object"}},"required":["id"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "id", Description: "Organization identifier"},
		{Name: "zone", Description: "Zone hosting the organization, as Make reports it"},
		{Name: "license", Description: "License limits as flat scalar values (numbers, booleans, short " +
			"strings), at most " + strconv.Itoa(maxLicenseEntries) + " entries; nested values are dropped"},
	},
	Examples: []capability.Example{{Description: "Read the organization", Arguments: json.RawMessage(`{}`)}},
}

// TeamDetail is the stable view of the bound team.
type TeamDetail struct {
	ID                   int64  `json:"id"`
	Name                 string `json:"name"`
	OrganizationID       int64  `json:"organization_id"`
	IsPaused             bool   `json:"is_paused,omitempty"`
	ActiveScenarios      int64  `json:"active_scenarios,omitempty"`
	ActiveApps           int64  `json:"active_apps,omitempty"`
	OperationsLimit      int64  `json:"operations_limit,omitempty"`
	TransferLimit        int64  `json:"transfer_limit,omitempty"`
	ConsumedOperations   int64  `json:"consumed_operations,omitempty"`
	ConsumedTransfer     int64  `json:"consumed_transfer,omitempty"`
	ConsumedCenticredits int64  `json:"consumed_centicredits,omitempty"`
}

type teamJSON struct {
	ID                   int64  `json:"id"`
	Name                 string `json:"name"`
	OrganizationID       int64  `json:"organizationId"`
	IsPaused             bool   `json:"isPaused"`
	ActiveScenarios      int64  `json:"activeScenarios"`
	ActiveApps           int64  `json:"activeApps"`
	OperationsLimit      int64  `json:"operationsLimit"`
	TransferLimit        int64  `json:"transferLimit"`
	ConsumedOperations   int64  `json:"consumedOperations"`
	ConsumedTransfer     int64  `json:"consumedTransfer"`
	ConsumedCenticredits int64  `json:"consumedCenticredits"`
}

var teamCols = []string{"id", "name", "organizationId", "activeScenarios", "activeApps", "operationsLimit",
	"transferLimit", "consumedOperations", "consumedTransfer", "consumedCenticredits", "isPaused"}

// fetchTeam reads the bound team and re-checks that Make answered for that team.
func (c *Client) fetchTeam(ctx context.Context, op string) (*TeamDetail, error) {
	var wrapper struct {
		Team teamJSON `json:"team"`
	}
	query := url.Values{"cols[]": teamCols}
	if err := c.get(ctx, op, "/teams/"+strconv.FormatInt(c.scope.teamID, 10), query, &wrapper, needTeamsRead); err != nil {
		return nil, err
	}
	t := wrapper.Team
	if !c.scope.allowsTeam(t.ID) {
		return nil, invalidRequest("Make's answer does not belong to this connection's bound team")
	}
	if !c.scope.allowsOrg(t.OrganizationID) {
		return nil, invalidRequest("the bound team does not belong to the organization this connection is bound to")
	}
	return &TeamDetail{ID: t.ID, Name: bounded(t.Name), OrganizationID: t.OrganizationID, IsPaused: t.IsPaused,
		ActiveScenarios: t.ActiveScenarios, ActiveApps: t.ActiveApps, OperationsLimit: t.OperationsLimit,
		TransferLimit: t.TransferLimit, ConsumedOperations: t.ConsumedOperations,
		ConsumedTransfer: t.ConsumedTransfer, ConsumedCenticredits: t.ConsumedCenticredits}, nil
}

func invokeTeamGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.fetchTeam(ctx, "get team")
}

// UsageDay is one daily usage record.
type UsageDay struct {
	Date         string `json:"date"`
	Operations   int64  `json:"operations,omitempty"`
	DataTransfer int64  `json:"data_transfer,omitempty"`
	Centicredits int64  `json:"centicredits,omitempty"`
}

// TeamUsage is the bounded usage view of the bound team.
type TeamUsage struct {
	TeamID int64      `json:"team_id"`
	Days   []UsageDay `json:"days"`
	Count  int        `json:"count"`
}

func invokeTeamUsage(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var page struct {
		Data []struct {
			Date         string `json:"date"`
			Operations   int64  `json:"operations"`
			DataTransfer int64  `json:"dataTransfer"`
			Centicredits int64  `json:"centicredits"`
		} `json:"data"`
	}
	path := "/teams/" + strconv.FormatInt(client.scope.teamID, 10) + "/usage"
	if err := client.get(ctx, "get team usage", path, nil, &page, needTeamsRead); err != nil {
		return nil, err
	}
	days := make([]UsageDay, 0, len(page.Data))
	for _, d := range page.Data {
		if len(days) == maxUsageDays {
			break
		}
		days = append(days, UsageDay{Date: bounded(d.Date), Operations: d.Operations,
			DataTransfer: d.DataTransfer, Centicredits: d.Centicredits})
	}
	return &TeamUsage{TeamID: client.scope.teamID, Days: days, Count: len(days)}, nil
}

// TeamMember is one user's role in the bound team; no name or email address.
type TeamMember struct {
	UserID     int64 `json:"user_id"`
	RoleID     int64 `json:"role_id"`
	Changeable bool  `json:"changeable,omitempty"`
	SSOPending bool  `json:"sso_pending,omitempty"`
}

// TeamMembers is the bounded member list of the bound team.
type TeamMembers struct {
	TeamID    int64        `json:"team_id"`
	Members   []TeamMember `json:"members"`
	Count     int          `json:"count"`
	Truncated bool         `json:"truncated"`
}

func invokeTeamMembers(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var page struct {
		Roles []struct {
			UserID     int64 `json:"userId"`
			TeamID     int64 `json:"teamId"`
			RoleID     int64 `json:"usersRoleId"`
			Changeable bool  `json:"changeable"`
			SSOPending bool  `json:"ssoPending"`
		} `json:"userTeamRoles"`
	}
	query := url.Values{"pg[limit]": {strconv.Itoa(maxMembers)}}
	path := "/teams/" + strconv.FormatInt(client.scope.teamID, 10) + "/user-team-roles"
	if err := client.get(ctx, "list team members", path, query, &page, needTeamsRead); err != nil {
		return nil, err
	}
	members := make([]TeamMember, 0, len(page.Roles))
	for _, r := range page.Roles {
		if !client.scope.allowsTeam(r.TeamID) {
			continue
		}
		members = append(members, TeamMember{UserID: r.UserID, RoleID: r.RoleID, Changeable: r.Changeable,
			SSOPending: r.SSOPending})
	}
	return &TeamMembers{TeamID: client.scope.teamID, Members: members, Count: len(members),
		Truncated: len(page.Roles) >= maxMembers}, nil
}

// Organization is the deliberately narrow organization view: id, zone, and license limits only.
type Organization struct {
	ID      int64          `json:"id"`
	Zone    string         `json:"zone,omitempty"`
	License map[string]any `json:"license,omitempty"`
}

func invokeOrganizationGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	const op = "get organization"
	team, err := client.fetchTeam(ctx, op)
	if err != nil {
		return nil, err
	}
	if team.OrganizationID <= 0 {
		return nil, invalidResponse(op, "Make reported no organization for the bound team")
	}
	var wrapper struct {
		Organization struct {
			ID      int64                      `json:"id"`
			Zone    string                     `json:"zone"`
			License map[string]json.RawMessage `json:"license"`
		} `json:"organization"`
	}
	path := "/organizations/" + strconv.FormatInt(team.OrganizationID, 10)
	if err := client.get(ctx, op, path, nil, &wrapper, needOrgRead); err != nil {
		return nil, err
	}
	o := wrapper.Organization
	if o.ID != team.OrganizationID {
		return nil, invalidResponse(op, "Make's organization answer does not match the bound team's organization")
	}
	return &Organization{ID: o.ID, Zone: bounded(o.Zone), License: scalarLicense(o.License)}, nil
}

// scalarLicense keeps only flat scalar license values, bounded in count and size, in a stable key order.
func scalarLicense(raw map[string]json.RawMessage) map[string]any {
	keys := make([]string, 0, len(raw))
	for k := range raw {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]any{}
	for _, k := range keys {
		if len(out) == maxLicenseEntries {
			break
		}
		if len(k) == 0 || len(k) > maxLicenseKeyLength {
			continue
		}
		var v any
		if err := json.Unmarshal(raw[k], &v); err != nil {
			continue
		}
		switch value := v.(type) {
		case bool, float64:
			out[k] = value
		case string:
			out[k] = bounded(value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
