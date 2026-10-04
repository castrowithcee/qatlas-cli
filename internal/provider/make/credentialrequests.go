package makeapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The four tools of this file create, list, read, and delete Make credential requests of the bound team. A
// credential request asks a person to enter the secret of a connection or key themselves through a link Make
// serves; no secret value is ever accepted, sent, or returned by these tools. Every answer is decoded into a
// struct holding only the allow-listed fields below.
// API (checked 2026-10-04 against developers.make.com's published API reference, api-reference/credential-
// requests, not a live account):
//   - GET /credential-requests/requests (teamId required; status, name, cols[]), credential-requests:read,
//     answering {"requests":[...]}.
//   - POST /credential-requests/requests/v2, credential-requests:write, body name, teamId, description,
//     credentials[] (appName, appModules, appVersion, nameOverride, description), and provider
//     {providerMakeUserId} (the newUser form, which invites by email, is deliberately not offered); answering {"request":{...},"publicUri":string}. The
//     deprecated POST /credential-requests/requests is not used.
//   - GET /credential-requests/requests/{requestId} (cols[]), credential-requests:read, answering {"request"}.
//   - DELETE /credential-requests/requests/{requestId}, credential-requests:write, optional confirmed=true
//     which also deletes the credentials (connections and keys) made from the request; answering
//     {"deleted":boolean}.
// Make documents the public link only on the create answer, so only create returns it; list and get never do.
// Tool ids have three segments, so the object is named credentialrequests.

const (
	// credentialRequestsSensitivity labels the request link, which is a way to enter secrets.
	credentialRequestsSensitivity = "make-credential-requests"

	maxCredentialRequestList   = maxListLimit
	maxCredentialRequestText   = 512
	maxCredentialNameLength    = 255
	maxCredentialDescription   = 512
	maxCredentialItems         = 16
	maxCredentialModules       = 32
	maxCredentialModuleLength  = 255
	maxCredentialAppNameLength = 135
	maxPublicLinkLength        = 2048
	maxAppVersion              = 9999

	needCredentialRequestsRead = "the credential-requests:read scope"
	needCredentialRequestsBind = "the credential-requests:write scope (and credential-requests:read, which " +
		"binds the request)"
	needCredentialRequestsCreate = "the credential-requests:write scope (and user:read, which proves the " +
		"person belongs to the team)"

	credentialRequestUncertain = "; the request may have been created or changed, list the credential " +
		"requests before repeating it"
)

var credentialRequestCols = []string{"id", "organizationId", "teamId", "name", "description", "status",
	"createdAt", "updatedAt", "expiresAt", "makeProvider"}

var (
	requestIDPattern  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	credAppPattern    = regexp.MustCompile(`^(app#)?[a-z][0-9a-z-]+[0-9a-z]$`)
	credModulePattern = regexp.MustCompile(`^(\*|[A-Za-z0-9_.-]+)$`)
	credStatusValues  = map[string]bool{"authorized": true, "declined": true, "incomplete": true, "invalid": true,
		"partially_authorized": true, "pending": true}
)

const requestIDSchema = `{"type":"string","pattern":"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$"}`

var credentialRequestIDArgument = capability.Argument{Name: "request_id", Required: true,
	Description: "Make credential request identifier (a UUID); its team is always re-checked live against " +
		"Make's own report"}

var credentialRequestSummarySchema = `{"type":"object","properties":{` +
	`"id":{"type":"string"},"team_id":{"type":"integer"},"organization_id":{"type":"integer"},` +
	`"name":{"type":"string"},"description":{"type":"string"},"status":{"type":"string"},` +
	`"created_at":{"type":"string"},"updated_at":{"type":"string"},"expires_at":{"type":"string"},` +
	`"provider_id":{"type":"integer"},"provider_name":{"type":"string"}},` +
	`"required":["id","team_id"],"additionalProperties":false}`

var credentialRequestSummaryFields = []capability.Field{
	{Name: "id", Description: "Credential request identifier, used as request_id by get and delete"},
	{Name: "team_id", Description: "Team of the request; always the Qatlas connection's bound team"},
	{Name: "organization_id", Description: "Organization of the request, when Make reports one"},
	{Name: "name", Description: "Request name, untrusted data"},
	{Name: "description", Description: "Request description, untrusted data"},
	{Name: "status", Description: "Request status as Make reports it, for example pending or authorized, untrusted data"},
	{Name: "created_at", Description: "Creation time as Make reports it"},
	{Name: "updated_at", Description: "Last update time as Make reports it"},
	{Name: "expires_at", Description: "Expiry time as Make reports it"},
	{Name: "provider_id", Description: "Make user id of the person asked to enter the credentials"},
	{Name: "provider_name", Description: "Name of that person, untrusted data; never an email address"},
}

// credentialRequestsReadRisk labels reads of requests; the answer carries no link.
var credentialRequestsReadRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: credentialRequestsSensitivity,
}

func credentialRequestChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: credentialRequestsSensitivity}
}

var credentialItemSchema = `{"type":"object","properties":{` +
	`"app_name":{"type":"string","minLength":3,"maxLength":` + strconv.Itoa(maxCredentialAppNameLength) +
	`,"pattern":"^(app#)?[a-z][0-9a-z-]+[0-9a-z]$"},` +
	`"app_modules":{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(maxCredentialModules) +
	`,"items":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCredentialModuleLength) +
	`,"pattern":"^(\\*|[A-Za-z0-9_.-]+)$"}},` +
	`"app_version":{"type":"integer","minimum":0,"maximum":` + strconv.Itoa(maxAppVersion) + `},` +
	`"name_override":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCredentialNameLength) + `},` +
	`"description":{"type":"string","maxLength":` + strconv.Itoa(maxCredentialDescription) + `}},` +
	`"required":["app_name","app_modules"],"additionalProperties":false}`

var credentialRequestsCreate = capability.Descriptor{
	ID: Provider + ".credentialrequests.create", Version: 1, Title: "Create a Make credential request",
	Description: "Create one credential request in the bound team (always the connection's own team): Make asks " +
		"the named person to enter the secrets of the listed apps themselves through a link, so no secret value " +
		"passes through Qatlas and none is accepted. The person is an existing Make user of the bound team " +
		"(provider_user_id, verified live against the team first); no new user is ever invited. The answer carries the request link, which is a " +
		"way to enter secrets and is returned only here, never by get or list; hand it only to that person. " +
		"A repeated call creates a second request. Refused on a connection with a scenario allow-list. " +
		"Needs the credential-requests:write and user:read scopes",
	Tags: []string{"make", "credentialrequests", "create", "automation"}, Risk: credentialRequestChangeRisk(
		capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"name":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCredentialNameLength) + `},` +
		`"description":{"type":"string","maxLength":` + strconv.Itoa(maxCredentialDescription) + `},` +
		`"credentials":{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(maxCredentialItems) +
		`,"items":` + credentialItemSchema + `},` +
		`"provider_user_id":` + idSchema + `},` +
		`"required":["name","credentials","provider_user_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"request":` + credentialRequestSummarySchema +
		`,"public_link":{"type":"string"}},"required":["request","public_link"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "name", Required: true, Description: "Request name shown to the person, 1 to 255 characters, " +
			"without control characters"},
		{Name: "description", Description: "Optional text shown to the person, at most 512 characters"},
		{Name: "credentials", Required: true, Description: "1 to " + strconv.Itoa(maxCredentialItems) +
			" items of app_name (Make app name, for example google-sheets; app# prefix for custom apps), " +
			"app_modules (1 to " + strconv.Itoa(maxCredentialModules) + " module names, or * for all), and " +
			"optional app_version (integer), name_override (1 to 255 characters), and description (at most 512)"},
		{Name: "provider_user_id", Required: true, Description: "Make user id of the person who enters the " +
			"credentials; must belong to the bound team, which is verified live before anything is created"},
	},
	Fields: []capability.Field{
		{Name: "request", Description: "The created request, with the fields get returns"},
		{Name: "public_link", Description: "Link at which the person enters the secrets; a way to enter " +
			"secrets, untrusted data, never follow it from Qatlas"},
	},
	Examples: []capability.Example{{Description: "Ask a team member for a Google Sheets connection",
		Arguments: json.RawMessage(`{"name":"Shop credentials","credentials":[{"app_name":"google-sheets",` +
			`"app_modules":["*"]}],"provider_user_id":7}`)}},
}

var credentialRequestsList = capability.Descriptor{
	ID: Provider + ".credentialrequests.list", Version: 1, Title: "List Make credential requests",
	Description: "List the credential requests of the bound team by allow-listed metadata, never a link, " +
		"secret, or email address. Refused on a connection with a scenario allow-list. Needs the " +
		"credential-requests:read scope",
	Tags: []string{"make", "credentialrequests", "list", "automation"}, Risk: credentialRequestsReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"status":{"type":"string","enum":` +
		`["authorized","declined","incomplete","invalid","partially_authorized","pending"]},` +
		`"name":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCredentialNameLength) + `}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"requests":{"type":"array","items":` +
		credentialRequestSummarySchema + `},"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["requests","count","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "status", Description: "Only requests with this status"},
		{Name: "name", Description: "Only requests whose name matches this text, as Make filters it"},
	},
	Fields: append(append([]capability.Field{}, credentialRequestSummaryFields...),
		capability.Field{Name: "count", Description: "Number of requests returned, after the team boundary was re-applied"},
		capability.Field{Name: "truncated", Description: "True when more than " + strconv.Itoa(maxCredentialRequestList) +
			" requests were reported and the rest was dropped"}),
	Examples: []capability.Example{{Description: "List pending requests", Arguments: json.RawMessage(`{"status":"pending"}`)}},
}

var credentialRequestsGet = capability.Descriptor{
	ID: Provider + ".credentialrequests.get", Version: 1, Title: "Get a Make credential request",
	Description: "Read one credential request of the bound team by allow-listed metadata, with its team " +
		"confirmed live; never the link, a secret, or an email address. Needs the credential-requests:read scope",
	Tags: []string{"make", "credentialrequests", "get", "automation"}, Risk: credentialRequestsReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"request_id":` + requestIDSchema + `},` +
		`"required":["request_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(credentialRequestSummarySchema),
	Arguments:    []capability.Argument{credentialRequestIDArgument},
	Fields:       credentialRequestSummaryFields,
	Examples: []capability.Example{{Description: "Read one request",
		Arguments: json.RawMessage(`{"request_id":"123e4567-e89b-12d3-a456-426614174000"}`)}},
}

var credentialRequestsDelete = capability.Descriptor{
	ID: Provider + ".credentialrequests.delete", Version: 1, Title: "Delete a Make credential request",
	Description: "Delete one credential request of the bound team. Without confirm_credentials_deleted only " +
		"the request is deleted and the credentials already entered stay; with it true Make also deletes " +
		"the connections and keys created from the request, and every scenario using them stops working. " +
		"Nothing is repeated after an unclear outcome. Offered only when a connection's tools list names it, " +
		"in no profile. Needs the credential-requests:read and credential-requests:write scopes",
	Tags: []string{"make", "credentialrequests", "delete", "automation"}, Risk: credentialRequestChangeRisk(
		capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"request_id":` + requestIDSchema + `,` +
		`"confirm_credentials_deleted":{"type":"boolean"}},"required":["request_id"],` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"request_id":{"type":"string"},` +
		`"deleted":{"type":"boolean"},"credentials_deleted":{"type":"boolean"}},` +
		`"required":["request_id","deleted","credentials_deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{credentialRequestIDArgument,
		{Name: "confirm_credentials_deleted", Description: "Set true to also delete the connections and keys " +
			"created from the request; scenarios using them stop working. When omitted, they are kept"}},
	Fields: []capability.Field{
		{Name: "request_id", Description: "The request that was addressed"},
		{Name: "deleted", Description: "True when Make reports the request as deleted"},
		{Name: "credentials_deleted", Description: "True when confirmed=true was sent, so Make also deleted " +
			"the credentials made from the request"},
	},
	Examples: []capability.Example{{Description: "Delete a request and keep its credentials",
		Arguments: json.RawMessage(`{"request_id":"123e4567-e89b-12d3-a456-426614174000"}`)}},
}

type credentialRequestJSON struct {
	ID             string `json:"id"`
	OrganizationID int64  `json:"organizationId"`
	TeamID         int64  `json:"teamId"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Status         string `json:"status"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
	ExpiresAt      string `json:"expiresAt"`
	MakeProvider   struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"makeProvider"`
}

// CredentialRequestSummary is the stable, team-checked view of one request; it holds no link and no email.
type CredentialRequestSummary struct {
	ID             string `json:"id"`
	TeamID         int64  `json:"team_id"`
	OrganizationID int64  `json:"organization_id,omitempty"`
	Name           string `json:"name,omitempty"`
	Description    string `json:"description,omitempty"`
	Status         string `json:"status,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
	ExpiresAt      string `json:"expires_at,omitempty"`
	ProviderID     int64  `json:"provider_id,omitempty"`
	ProviderName   string `json:"provider_name,omitempty"`
}

func boundCredentialText(value string) string {
	if len(value) > maxCredentialRequestText {
		value = value[:maxCredentialRequestText]
	}
	return strings.ToValidUTF8(value, "")
}

func credentialRequestSummaryOf(r credentialRequestJSON, teamID int64) CredentialRequestSummary {
	return CredentialRequestSummary{ID: boundCredentialText(r.ID), TeamID: teamID, OrganizationID: r.OrganizationID,
		Name: boundCredentialText(r.Name), Description: boundCredentialText(r.Description),
		Status: boundCredentialText(r.Status), CreatedAt: boundCredentialText(r.CreatedAt),
		UpdatedAt: boundCredentialText(r.UpdatedAt), ExpiresAt: boundCredentialText(r.ExpiresAt),
		ProviderID: r.MakeProvider.ID, ProviderName: boundCredentialText(r.MakeProvider.Name)}
}

func (c *Client) allowsCredentialRequest(r credentialRequestJSON) bool {
	return c.scope.allowsTeam(r.TeamID) && c.scope.allowsOrg(r.OrganizationID)
}

func credentialRequestsQuery(query url.Values) url.Values {
	query["cols[]"] = append([]string(nil), credentialRequestCols...)
	return query
}

// selectCredentialRequests refuses a connection with a scenario allow-list before any secret is read:
// credential requests belong to no scenario, so the narrower reading is that such a connection reaches none.
func selectCredentialRequests(resolved *config.Resolved) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if len(bound.scenarios) > 0 {
		return invalidRequest("Make credential requests are not scenario-bound; a connection with a scenario " +
			"allow-list cannot reach them")
	}
	return nil
}

type credentialRequestArguments struct {
	RequestID string `json:"request_id"`
}

func credentialRequestPath(id string) string { return "/credential-requests/requests/" + id }

// fetchCredentialRequest reads one request and binds it back to this connection's team: a request of another
// team (or organization) is refused without naming whatever it belongs to.
func (c *Client) fetchCredentialRequest(ctx context.Context, op, id string) (*credentialRequestJSON, error) {
	var wrapper struct {
		Request credentialRequestJSON `json:"request"`
	}
	if err := c.get(ctx, op, credentialRequestPath(id), credentialRequestsQuery(url.Values{}), &wrapper,
		needCredentialRequestsRead); err != nil {
		return nil, err
	}
	if !strings.EqualFold(wrapper.Request.ID, id) || !c.allowsCredentialRequest(wrapper.Request) {
		return nil, invalidRequest("request_id is outside the targets of this connection")
	}
	return &wrapper.Request, nil
}

func openCredentialRequest(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (*Client, string, *credentialRequestJSON, error) {
	var input credentialRequestArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, "", nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectCredentialRequests(resolved); err != nil {
		return nil, "", nil, err
	}
	if !requestIDPattern.MatchString(input.RequestID) {
		return nil, "", nil, invalidRequest("request_id must be a credential request UUID")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, "", nil, err
	}
	request, err := client.fetchCredentialRequest(ctx, op, input.RequestID)
	if err != nil {
		return nil, "", nil, err
	}
	return client, input.RequestID, request, nil
}

func invokeCredentialRequestsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	client, _, request, err := openCredentialRequest(ctx, "get credential request", resolved, secrets, red, raw)
	if err != nil {
		return nil, err
	}
	return credentialRequestSummaryOf(*request, client.scope.teamID), nil
}

type credentialRequestsListArguments struct {
	Status string `json:"status"`
	Name   string `json:"name"`
}

// CredentialRequestsPage is one team-filtered listing.
type CredentialRequestsPage struct {
	Requests  []CredentialRequestSummary `json:"requests"`
	Count     int                        `json:"count"`
	Truncated bool                       `json:"truncated"`
}

func invokeCredentialRequestsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list credential requests"
	var input credentialRequestsListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if input.Status != "" && !credStatusValues[input.Status] {
		return nil, invalidRequest("status is not one of Make's credential request statuses")
	}
	if input.Name != "" {
		if err := validCredentialText("name", input.Name, maxCredentialNameLength, true); err != nil {
			return nil, err
		}
	}
	if err := selectCredentialRequests(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	query := credentialRequestsQuery(url.Values{"teamId": {strconv.FormatInt(client.scope.teamID, 10)}})
	if input.Status != "" {
		query.Set("status", input.Status)
	}
	if input.Name != "" {
		query.Set("name", input.Name)
	}
	var page struct {
		Requests []credentialRequestJSON `json:"requests"`
	}
	if err := client.get(ctx, op, "/credential-requests/requests", query, &page, needCredentialRequestsRead); err != nil {
		return nil, err
	}
	result := &CredentialRequestsPage{Requests: make([]CredentialRequestSummary, 0, len(page.Requests))}
	for _, request := range page.Requests {
		if !client.allowsCredentialRequest(request) {
			continue
		}
		if len(result.Requests) >= maxCredentialRequestList {
			result.Truncated = true
			break
		}
		result.Requests = append(result.Requests, credentialRequestSummaryOf(request, client.scope.teamID))
	}
	result.Count = len(result.Requests)
	return result, nil
}

type credentialRequestDeleteArguments struct {
	RequestID                string `json:"request_id"`
	ConfirmCredentialsDelete bool   `json:"confirm_credentials_deleted"`
}

// CredentialRequestDeletion is the answer of credentialrequests.delete.
type CredentialRequestDeletion struct {
	RequestID          string `json:"request_id"`
	Deleted            bool   `json:"deleted"`
	CredentialsDeleted bool   `json:"credentials_deleted"`
}

func invokeCredentialRequestsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete credential request"
	var input credentialRequestDeleteArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	client, id, _, err := openCredentialRequest(ctx, op, resolved, secrets, red, raw)
	if err != nil {
		return nil, err
	}
	var query url.Values
	if input.ConfirmCredentialsDelete {
		query = url.Values{"confirmed": {"true"}}
	}
	var answer struct {
		Deleted *bool `json:"deleted"`
	}
	if err := client.change(ctx, op, http.MethodDelete, credentialRequestPath(id), query, nil, &answer,
		needCredentialRequestsBind, credentialRequestUncertain); err != nil {
		return nil, err
	}
	if answer.Deleted == nil {
		return nil, invalidResponse(op, "Make did not report whether the request was deleted"+credentialRequestUncertain)
	}
	return &CredentialRequestDeletion{RequestID: id, Deleted: *answer.Deleted,
		CredentialsDeleted: *answer.Deleted && input.ConfirmCredentialsDelete}, nil
}

type credentialItemArguments struct {
	AppName      string   `json:"app_name"`
	AppModules   []string `json:"app_modules"`
	AppVersion   *int     `json:"app_version"`
	NameOverride string   `json:"name_override"`
	Description  string   `json:"description"`
}

type credentialRequestCreateArguments struct {
	Name           string                    `json:"name"`
	Description    string                    `json:"description"`
	Credentials    []credentialItemArguments `json:"credentials"`
	ProviderUserID int64                     `json:"provider_user_id"`
}

func validCredentialText(label, value string, max int, required bool) error {
	if (required && strings.TrimSpace(value) == "") || utf8.RuneCountInString(value) > max ||
		!utf8.ValidString(value) || strings.ContainsFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return invalidRequest(label + " must be at most " + strconv.Itoa(max) +
			" characters without control characters" + map[bool]string{true: " and not empty", false: ""}[required])
	}
	return nil
}

// buildCredentialRequestBody validates every typed field locally and returns the exact body to send.
func buildCredentialRequestBody(input credentialRequestCreateArguments, teamID int64) (map[string]any, error) {
	if err := validCredentialText("name", input.Name, maxCredentialNameLength, true); err != nil {
		return nil, err
	}
	if err := validCredentialText("description", input.Description, maxCredentialDescription, false); err != nil {
		return nil, err
	}
	if len(input.Credentials) == 0 || len(input.Credentials) > maxCredentialItems {
		return nil, invalidRequest("credentials needs 1 to " + strconv.Itoa(maxCredentialItems) + " items")
	}
	items := make([]map[string]any, 0, len(input.Credentials))
	for _, item := range input.Credentials {
		if len(item.AppName) < 3 || len(item.AppName) > maxCredentialAppNameLength || !credAppPattern.MatchString(item.AppName) {
			return nil, invalidRequest("app_name must be a Make app name such as google-sheets")
		}
		if len(item.AppModules) == 0 || len(item.AppModules) > maxCredentialModules {
			return nil, invalidRequest("app_modules needs 1 to " + strconv.Itoa(maxCredentialModules) + " module names")
		}
		for _, module := range item.AppModules {
			if len(module) > maxCredentialModuleLength || !credModulePattern.MatchString(module) {
				return nil, invalidRequest("app_modules may hold only module names or *")
			}
		}
		out := map[string]any{"appName": item.AppName, "appModules": item.AppModules}
		if item.AppVersion != nil {
			if *item.AppVersion < 0 || *item.AppVersion > maxAppVersion {
				return nil, invalidRequest("app_version must be between 0 and " + strconv.Itoa(maxAppVersion))
			}
			out["appVersion"] = *item.AppVersion
		}
		if item.NameOverride != "" {
			if err := validCredentialText("name_override", item.NameOverride, maxCredentialNameLength, true); err != nil {
				return nil, err
			}
			out["nameOverride"] = item.NameOverride
		}
		if item.Description != "" {
			if err := validCredentialText("credential description", item.Description, maxCredentialDescription, false); err != nil {
				return nil, err
			}
			out["description"] = item.Description
		}
		items = append(items, out)
	}
	body := map[string]any{"name": input.Name, "teamId": teamID, "credentials": items}
	if input.Description != "" {
		body["description"] = input.Description
	}
	if input.ProviderUserID <= 0 {
		return nil, invalidRequest("provider_user_id must be a positive integer")
	}
	body["provider"] = map[string]any{"providerMakeUserId": input.ProviderUserID}
	return body, nil
}

// CredentialRequestCreated is the answer of credentialrequests.create.
type CredentialRequestCreated struct {
	Request    CredentialRequestSummary `json:"request"`
	PublicLink string                   `json:"public_link"`
}

func invokeCredentialRequestsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create credential request"
	var input credentialRequestCreateArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectCredentialRequests(resolved); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	body, err := buildCredentialRequestBody(input, bound.teamID)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.requireTeamMember(ctx, op, input.ProviderUserID); err != nil {
		var invalid *application.InvalidRequestError
		if errors.As(err, &invalid) {
			return nil, invalidRequest("provider_user_id is not a member of the team of this connection")
		}
		return nil, err
	}
	var answer struct {
		Request   credentialRequestJSON `json:"request"`
		PublicURI string                `json:"publicUri"`
	}
	if err := client.change(ctx, op, http.MethodPost, "/credential-requests/requests/v2", nil, body, &answer,
		needCredentialRequestsCreate, credentialRequestUncertain); err != nil {
		return nil, err
	}
	if !requestIDPattern.MatchString(answer.Request.ID) {
		return nil, invalidResponse(op, "Make did not report the created request"+credentialRequestUncertain)
	}
	// The team was sent by this provider; a request reported in another team or organization is not
	// returned, but the creation already took effect.
	if (answer.Request.TeamID != 0 && !client.scope.allowsTeam(answer.Request.TeamID)) ||
		(answer.Request.OrganizationID != 0 && !client.scope.allowsOrg(answer.Request.OrganizationID)) {
		return nil, providerError(op, "Make did not keep the result inside this connection's targets; "+
			"the request already exists")
	}
	link, err := url.Parse(answer.PublicURI)
	if err != nil || link.Scheme != "https" || link.Host == "" || link.User != nil ||
		len(answer.PublicURI) > maxPublicLinkLength || !utf8.ValidString(answer.PublicURI) {
		return nil, invalidResponse(op, "Make did not report a usable request link; the request exists, "+
			"delete it and create it again"+credentialRequestUncertain)
	}
	return &CredentialRequestCreated{Request: credentialRequestSummaryOf(answer.Request, client.scope.teamID),
		PublicLink: answer.PublicURI}, nil
}
