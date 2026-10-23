package infomaniakchat

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

var readRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: dataSensitivity,
}

// teamSettingsProperties are the schema members that teams.list and teams.get both carry beyond the
// identity members. The invitation ID is deliberately absent.
const teamSettingsProperties = `"email":{"type":"string"},"allowed_domains":{"type":"string"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"},"deleted_at":{"type":"string"},` +
	`"allow_open_invite":{"type":"boolean"},"policy_id":{"type":"string"}`

var teamSettingsFields = []capability.Field{
	{Name: "email", Description: "Contact address of the team as kChat reports it to the token, untrusted data"},
	{Name: "allowed_domains", Description: "Email domains allowed to join the team as kChat reports them to the " +
		"token, untrusted data"},
	{Name: "created_at", Description: "Creation time, normalised to RFC 3339 in UTC"},
	{Name: "updated_at", Description: "Time of the last change of the team, normalised to RFC 3339 in UTC"},
	{Name: "deleted_at", Description: "Time the team was archived, normalised to RFC 3339 in UTC"},
	{Name: "allow_open_invite", Description: "Whether any user of the instance may join the team"},
	{Name: "policy_id", Description: "Identifier of the data retention policy that applies to the team"},
}

var teamEntrySchema = `{"type":"object","properties":{` +
	`"id":` + idSchema + `,"name":{"type":"string"},"display_name":{"type":"string"},"type":{"type":"string"},` +
	`"description":{"type":"string"},` + teamSettingsProperties + `},` +
	`"required":["id","name","display_name","type"],"additionalProperties":false}`

var teamEntryFields = append([]capability.Field{
	{Name: "id", Description: "Team identifier, used as team_id by channels.list"},
	{Name: "name", Description: "URL-safe team handle, untrusted data"},
	{Name: "display_name", Description: "Display name of the team, untrusted data"},
	{Name: "type", Description: "kChat's own team type, for example O (open) or I (invite-only)"},
	{Name: "description", Description: "Description of the team, untrusted data"},
}, teamSettingsFields...)

// teamSettingsJSON holds the members of the kChat Team resource that teams.list and teams.get share beyond
// identity. A member of an unexpected type is ignored. The invitation ID is never decoded.
type teamSettingsJSON struct {
	Email           tolerant[string] `json:"email"`
	AllowedDomains  tolerant[string] `json:"allowed_domains"`
	CreateAt        flexInt          `json:"create_at"`
	UpdateAt        flexInt          `json:"update_at"`
	DeleteAt        flexInt          `json:"delete_at"`
	AllowOpenInvite tolerant[*bool]  `json:"allow_open_invite"`
	PolicyID        tolerant[string] `json:"policy_id"`
}

// TeamSettings is what kChat reports about the settings of a team; a value kChat leaves out stays out.
type TeamSettings struct {
	Email           string `json:"email,omitempty"`
	AllowedDomains  string `json:"allowed_domains,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
	DeletedAt       string `json:"deleted_at,omitempty"`
	AllowOpenInvite *bool  `json:"allow_open_invite,omitempty"`
	PolicyID        string `json:"policy_id,omitempty"`
}

func (j *teamSettingsJSON) settings() TeamSettings {
	return TeamSettings{Email: cleanText(j.Email.Value), AllowedDomains: cleanText(j.AllowedDomains.Value),
		CreatedAt: msToRFC3339(int64(j.CreateAt)), UpdatedAt: msToRFC3339(int64(j.UpdateAt)),
		DeletedAt: msToRFC3339(int64(j.DeleteAt)), AllowOpenInvite: j.AllowOpenInvite.Value,
		PolicyID: cleanText(j.PolicyID.Value)}
}

var teamsList = capability.Descriptor{
	ID:      Provider + ".teams.list",
	Version: 1,
	Title:   "List Infomaniak kChat teams",
	Description: "List the teams this connection is bound to that the current kChat token is actually a " +
		"member of, page by page, transparently",
	Tags:     []string{"infomaniak", "kchat", "teams", "list"},
	Risk:     readRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"page":{"type":"integer","minimum":1},"limit":{"type":"integer","minimum":1,"maximum":` +
		itoa(maxListLimit) + `}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"teams":{"type":"array","items":` + teamEntrySchema + `},` +
		`"page":{"type":"integer"},"pages":{"type":"integer"},"total":{"type":"integer"},"count":{"type":"integer"}},` +
		`"required":["teams","page","pages","total","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "page", Description: "1-based page of the connection's bound teams; the first page when omitted"},
		{Name: "limit", Description: "Teams per page, 1 to " + itoa(maxListLimit) + "; " + itoa(defaultListLimit) + " when omitted"},
	},
	Fields: append(append([]capability.Field{}, teamEntryFields...),
		capability.Field{Name: "page", Description: "Page that was read"},
		capability.Field{Name: "pages", Description: "Total number of pages of this connection's bound teams"},
		capability.Field{Name: "total", Description: "Total number of this connection's bound teams that the token is a member of"},
		capability.Field{Name: "count", Description: "Number of teams reported on this page"},
	),
	Examples: []capability.Example{{Description: "List the bound teams", Arguments: json.RawMessage(`{}`)}},
}

// teamJSON is the subset of the kChat Team resource this provider reads.
type teamJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`

	Description tolerant[string] `json:"description"`
	teamSettingsJSON
}

// TeamEntry is the stable Qatlas view of one kChat team.
type TeamEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	TeamSettings
}

// TeamsPage is one paginated listing of the teams this connection is bound to.
type TeamsPage struct {
	Teams []TeamEntry `json:"teams"`
	Page  int         `json:"page"`
	Pages int         `json:"pages"`
	Total int         `json:"total"`
	Count int         `json:"count"`
}

type teamsListArguments struct {
	Page  int `json:"page"`
	Limit int `json:"limit"`
}

func invokeTeamsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	var input teamsListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError("list teams", "the validated arguments could not be read")
	}
	page, limit := input.Page, input.Limit
	if page == 0 {
		page = 1
	}
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListTeams(ctx, page, limit)
}

// ListTeams reads the teams of the current token, keeps only those this connection is bound to, and pages
// the result itself: kChat's own list of a user's teams answers as one complete array with no pagination
// of its own.
func (c *Client) ListTeams(ctx context.Context, page, limit int) (*TeamsPage, error) {
	const op = "list teams"
	var teams []teamJSON
	if err := c.do(ctx, op, http.MethodGet, "/api/v4/users/me/teams", nil, nil, &teams, false); err != nil {
		return nil, err
	}
	bound := make([]TeamEntry, 0, len(teams))
	for i := range teams {
		t := &teams[i]
		if !c.scope.allowsTeam(t.ID) {
			continue
		}
		bound = append(bound, TeamEntry{ID: t.ID, Name: bounded(t.Name), DisplayName: bounded(t.DisplayName),
			Type: bounded(t.Type), Description: cleanText(t.Description.Value), TeamSettings: t.settings()})
	}
	window, pages, total := windowOf(bound, page, limit)
	return &TeamsPage{Teams: window, Page: page, Pages: pages, Total: total, Count: len(window)}, nil
}
