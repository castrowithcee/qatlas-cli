package penpot

import (
	"context"
	"encoding/json"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	metadataSensitive = "penpot-metadata"
	designSensitive   = "penpot-design"
	maxTeamsListed    = 100
)

const uuidSchema = `{"type":"string","pattern":"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$"}`

var metadataRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: metadataSensitive}

var teamsList = capability.Descriptor{
	ID: Provider + ".teams.list", Version: 1, Title: "List Penpot teams",
	Description: "List the teams of the access token's account, restricted to the teams of the connection's " +
		"targets; names are untrusted provider data",
	Tags: []string{"penpot", "teams", "list", "design"}, Risk: metadataRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"teams":{"type":"array","items":` +
		`{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"is_default":{"type":"boolean"},"created_at":` + timestampSchema + `,"modified_at":` + timestampSchema +
		`,"photo_id":{"type":"string"},"features":` + featuresSchema + `,"permissions":` + teamPermissionsSchema +
		`,"subscription":` + teamSubscriptionSchema + `,"organization":` + teamOrganizationSchema +
		`},"required":["id","name"],"additionalProperties":false}},` +
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["teams","count","truncated"],"additionalProperties":false}`),
	Fields: []capability.Field{
		{Name: "teams", Description: "Teams with id (used as team_id by penpot.projects.list), name, is_default, created_at, " +
			"modified_at, photo_id, features, permissions (type, is_owner, is_admin, can_edit of the token's account), " +
			"subscription (type, status, seats; only when the instance has the subscriptions flag), and organization " +
			"(id, name, slug, owner_id, expired_license, sso_active, and permissions with create_teams, delete_teams, " +
			"move_teams, new_team_members; only when the instance has the admin-console flag). Names, slug, and " +
			"subscription and organization texts are untrusted provider data"},
		{Name: "count", Description: "Number of teams in this answer"},
		{Name: "truncated", Description: "True when the answer was cut at " + "100 teams"},
	},
	Examples: []capability.Example{{Description: "List the bound teams", Arguments: json.RawMessage(`{}`)}},
}

const (
	teamPermissionsSchema = `{"type":"object","properties":{"type":{"type":"string"},"is_owner":{"type":"boolean"},` +
		`"is_admin":{"type":"boolean"},"can_edit":{"type":"boolean"}},"additionalProperties":false}`
	teamSubscriptionSchema = `{"type":"object","properties":{"type":{"type":"string"},"status":{"type":"string"},` +
		`"seats":{"type":"integer"}},"additionalProperties":false}`
	organizationPermissionsSchema = `{"type":"object","properties":{"create_teams":{"type":"string"},` +
		`"delete_teams":{"type":"string"},"move_teams":{"type":"string"},"new_team_members":{"type":"string"}},` +
		`"additionalProperties":false}`
	teamOrganizationSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"slug":{"type":"string"},"owner_id":{"type":"string"},"expired_license":{"type":"boolean"},` +
		`"sso_active":{"type":"boolean"},"permissions":` + organizationPermissionsSchema + `},"additionalProperties":false}`
)

// Team is one team of the connection's targets.
type Team struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	IsDefault    bool              `json:"is_default,omitempty"`
	CreatedAt    string            `json:"created_at,omitempty"`
	ModifiedAt   string            `json:"modified_at,omitempty"`
	PhotoID      string            `json:"photo_id,omitempty"`
	Features     []string          `json:"features,omitempty"`
	Permissions  *TeamPermissions  `json:"permissions,omitempty"`
	Subscription *TeamSubscription `json:"subscription,omitempty"`
	Organization *TeamOrganization `json:"organization,omitempty"`
}

// TeamPermissions is what the token's account may do in a team.
type TeamPermissions struct {
	Type    string `json:"type,omitempty"`
	IsOwner *bool  `json:"is_owner,omitempty"`
	IsAdmin *bool  `json:"is_admin,omitempty"`
	CanEdit *bool  `json:"can_edit,omitempty"`
}

// TeamSubscription is the subscription of the team's owner; Penpot sends it with the subscriptions flag.
type TeamSubscription struct {
	Type   string `json:"type,omitempty"`
	Status string `json:"status,omitempty"`
	Seats  *int64 `json:"seats,omitempty"`
}

// OrganizationPermissions are the rules an organization sets for its teams.
type OrganizationPermissions struct {
	CreateTeams    string `json:"create_teams,omitempty"`
	DeleteTeams    string `json:"delete_teams,omitempty"`
	MoveTeams      string `json:"move_teams,omitempty"`
	NewTeamMembers string `json:"new_team_members,omitempty"`
}

// TeamOrganization is the organization of a team; Penpot sends it with the admin-console flag. The image
// addresses of the organization are never read.
type TeamOrganization struct {
	ID             string                   `json:"id"`
	Name           string                   `json:"name,omitempty"`
	Slug           string                   `json:"slug,omitempty"`
	OwnerID        string                   `json:"owner_id,omitempty"`
	ExpiredLicense *bool                    `json:"expired_license,omitempty"`
	SSOActive      *bool                    `json:"sso_active,omitempty"`
	Permissions    *OrganizationPermissions `json:"permissions,omitempty"`
}

func readTeamPermissions(o obj) *TeamPermissions {
	entry, ok := o.child("permissions")
	if !ok {
		return nil
	}
	out := &TeamPermissions{Type: entry.label("type"), IsOwner: entry.optBoolean("isowner"),
		IsAdmin: entry.optBoolean("isadmin"), CanEdit: entry.optBoolean("canedit")}
	if *out == (TeamPermissions{}) {
		return nil
	}
	return out
}

func readTeamSubscription(o obj) *TeamSubscription {
	entry, ok := o.child("subscription")
	if !ok {
		return nil
	}
	out := &TeamSubscription{Type: entry.label("type"), Status: entry.label("status"), Seats: entry.optInteger("seats")}
	if *out == (TeamSubscription{}) {
		return nil
	}
	return out
}

func readTeamOrganization(o obj) *TeamOrganization {
	entry, ok := o.child("organization")
	if !ok {
		return nil
	}
	id := entry.id("id")
	if id == "" {
		return nil
	}
	out := &TeamOrganization{ID: id, Name: entry.label("name"), Slug: entry.label("slug"), OwnerID: entry.id("ownerid"),
		ExpiredLicense: entry.optBoolean("expiredlicense"), SSOActive: entry.optBoolean("ssoactive")}
	if perms, ok := entry.child("permissions"); ok {
		read := &OrganizationPermissions{CreateTeams: perms.label("createteams"), DeleteTeams: perms.label("deleteteams"),
			MoveTeams: perms.label("moveteams"), NewTeamMembers: perms.label("newteammembers")}
		if *read != (OrganizationPermissions{}) {
			out.Permissions = read
		}
	}
	return out
}

// TeamsResult is the answer of teams.list.
type TeamsResult struct {
	Teams     []Team `json:"teams"`
	Count     int    `json:"count"`
	Truncated bool   `json:"truncated"`
}

func invokeTeamsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, _ json.RawMessage) (any, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListTeams(ctx, "list teams")
}

// ListTeams reads every team of the token and keeps only those of the connection's targets.
func (c *Client) ListTeams(ctx context.Context, op string) (*TeamsResult, error) {
	var raw json.RawMessage
	if err := c.do(ctx, op, cmdTeams, nil, &raw); err != nil {
		return nil, err
	}
	entries, ok := objects(raw)
	if !ok {
		return nil, invalidResponse(op, "Penpot returned an invalid response")
	}
	result := &TeamsResult{Teams: []Team{}}
	for _, entry := range entries {
		id := entry.id("id")
		if id == "" || !c.scope.allowsTeam(id) {
			continue
		}
		if len(result.Teams) >= maxTeamsListed {
			result.Truncated = true
			break
		}
		result.Teams = append(result.Teams, Team{ID: id, Name: entry.str("name"), IsDefault: entry.boolean("isdefault"),
			CreatedAt: entry.str("createdat"), ModifiedAt: entry.str("modifiedat"), PhotoID: entry.id("photoid"),
			Features: entry.stringList("features"), Permissions: readTeamPermissions(entry),
			Subscription: readTeamSubscription(entry), Organization: readTeamOrganization(entry)})
	}
	result.Count = len(result.Teams)
	return result, nil
}
