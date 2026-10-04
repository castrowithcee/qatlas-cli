package makeapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The four tools of this file list, read, and test the connections (stored third-party credentials) of the
// bound team. A connection's secrets never leave Qatlas: every answer is decoded into a struct that holds
// only the allow-listed fields below, so tokens, metadata, scope lists, and anything else Make may add are
// dropped before they could be returned. Make's connection answer is not documented field by field beyond
// its property names, which is why the allow-list, not a deny-list, decides.
// API: GET /connections (teamId required; type[], cols[]), GET /connections/{id} (cols[]), and GET
// /connections/{id}/editable-data-schema, all connections:read, and POST /connections/{id}/test,
// connections:write, answering {"verified":boolean}; checked 2026-10-04 against developers.make.com's
// published API reference (api-reference/connections), not a live account.

const (
	// connectionsSensitivity labels the stored credentials these tools describe.
	connectionsSensitivity = "make-connections"
	// maxConnectionText bounds every string of a connection answer.
	maxConnectionText = 256
	// maxConnectionList bounds the connections one list answers with.
	maxConnectionList = maxListLimit
	// maxEditableParameters and maxEditableParameterLength bound the editable-schema field names.
	maxEditableParameters      = 64
	maxEditableParameterLength = 128
	// connectionTestUncertain marks a failure that could mean the test request nonetheless ran.
	connectionTestUncertain = "; the test may have run against the third-party service, read the connection " +
		"before testing it again"
)

// connectionCols is the cols[] group this provider asks Make for; it equals the allow-list, so Make is asked
// for nothing else either.
var connectionCols = []string{"id", "name", "accountName", "accountType", "packageName", "expire", "scoped",
	"teamId", "organizationId", "editable"}

var connectionIDSchema = idSchema

var connectionIDArgument = capability.Argument{Name: "connection_id", Required: true,
	Description: "Make connection identifier (not a Qatlas connection); its team is always re-checked live " +
		"against Make's own report, and a Qatlas connection with a scenario allow-list reaches no Make " +
		"connections at all, since they belong to no scenario"}

var connectionSummarySchema = `{"type":"object","properties":{` +
	`"id":{"type":"integer"},"name":{"type":"string"},"account_name":{"type":"string"},` +
	`"account_type":{"type":"string"},"package_name":{"type":"string"},"expire":{"type":"string"},` +
	`"scoped":{"type":"boolean"},"team_id":{"type":"integer"},"organization_id":{"type":"integer"},` +
	`"editable":{"type":"boolean"}},"required":["id","team_id"],"additionalProperties":false}`

var connectionSummaryFields = []capability.Field{
	{Name: "id", Description: "Make connection identifier, used as connection_id by the other connection tools"},
	{Name: "name", Description: "Connection name, untrusted data"},
	{Name: "account_name", Description: "Connection type as Make names it, for example google, untrusted data"},
	{Name: "account_type", Description: "Connection kind as Make reports it, untrusted data"},
	{Name: "package_name", Description: "App package the connection belongs to, untrusted data"},
	{Name: "expire", Description: "When the stored credential expires, as Make reports it"},
	{Name: "scoped", Description: "True when Make reports the connection's scopes as sufficient"},
	{Name: "team_id", Description: "Team of the connection; always the Qatlas connection's bound team"},
	{Name: "organization_id", Description: "Organization of the connection, when Make reports one"},
	{Name: "editable", Description: "True when Make reports the connection's data as editable"},
}

var connectionsList = capability.Descriptor{
	ID: Provider + ".connections.list", Version: 1, Title: "List Make connections",
	Description: "List the Make connections (stored third-party credentials) of the bound team by allow-listed " +
		"metadata only; never a token, secret, scope list, or other stored value. Refused on a connection " +
		"with a scenario allow-list",
	Tags: []string{"make", "connections", "list", "automation"}, Risk: connectionsReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"type":{"type":"string","pattern":"^[A-Za-z0-9_.-]{1,64}$"}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"connections":{"type":"array","items":` + connectionSummarySchema + `},` +
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["connections","count","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "type", Description: "When set, list only connections of this Make connection type (account_name)"},
	},
	Fields: append(append([]capability.Field{}, connectionSummaryFields...),
		capability.Field{Name: "count", Description: "Number of connections returned, after the team boundary " +
			"was re-applied"},
		capability.Field{Name: "truncated", Description: "True when more than " + strconv.Itoa(maxConnectionList) +
			" connections were reported and the rest was dropped"},
	),
	Examples: []capability.Example{{Description: "List the team's connections", Arguments: json.RawMessage(`{}`)}},
}

var connectionsGet = capability.Descriptor{
	ID: Provider + ".connections.get", Version: 1, Title: "Get a Make connection",
	Description: "Read one Make connection of the bound team by allow-listed metadata only; never a token, " +
		"secret, scope list, or other stored value",
	Tags: []string{"make", "connections", "get", "automation"}, Risk: connectionsReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"connection_id":` + connectionIDSchema + `},` +
		`"required":["connection_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(connectionSummarySchema),
	Arguments:    []capability.Argument{connectionIDArgument},
	Fields:       connectionSummaryFields,
	Examples:     []capability.Example{{Description: "Read one connection", Arguments: json.RawMessage(`{"connection_id":1}`)}},
}

var connectionsEditableSchema = capability.Descriptor{
	ID: Provider + ".connections.editableschema", Version: 1, Title: "List a Make connection's editable parameters",
	Description: "List the names of the parameters Make allows to be updated on one connection of the bound " +
		"team; names only, never a value or default",
	Tags: []string{"make", "connections", "schema", "automation"}, Risk: connectionsReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"connection_id":` + connectionIDSchema + `},` +
		`"required":["connection_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"connection_id":{"type":"integer"},` +
		`"team_id":{"type":"integer"},"parameters":{"type":"array","items":{"type":"string"}},` +
		`"count":{"type":"integer"}},"required":["connection_id","team_id","parameters","count"],` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{connectionIDArgument},
	Fields: []capability.Field{
		{Name: "connection_id", Description: "The connection the parameters belong to"},
		{Name: "team_id", Description: "Team of the connection; always the Qatlas connection's bound team"},
		{Name: "parameters", Description: "Names of the editable parameters, untrusted data, at most " +
			strconv.Itoa(maxEditableParameters) + " of at most " + strconv.Itoa(maxEditableParameterLength) +
			" characters"},
		{Name: "count", Description: "Number of names returned"},
	},
	Examples: []capability.Example{{Description: "Read one connection's editable parameters",
		Arguments: json.RawMessage(`{"connection_id":1}`)}},
}

var connectionsTest = capability.Descriptor{
	ID: Provider + ".connections.test", Version: 1, Title: "Test a Make connection",
	Description: "Ask Make to verify one connection of the bound team: Make uses the stored credential against " +
		"the third-party service and reports whether it is still valid. It reaches a service outside Make " +
		"and is never retried, an unclear outcome is reported as uncertain. Needs the connections:read and " +
		"connections:write scopes",
	Tags: []string{"make", "connections", "test", "automation", "execute"},
	Risk: capability.Risk{Effect: capability.EffectExecute, Idempotency: capability.IdempotencyUnknown,
		Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: connectionsSensitivity},
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"connection_id":` + connectionIDSchema + `},` +
		`"required":["connection_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"connection_id":{"type":"integer"},` +
		`"team_id":{"type":"integer"},"verified":{"type":"boolean"}},` +
		`"required":["connection_id","team_id","verified"],"additionalProperties":false}`),
	Arguments: []capability.Argument{connectionIDArgument},
	Fields: []capability.Field{
		{Name: "connection_id", Description: "The connection that was tested"},
		{Name: "team_id", Description: "Team of the connection; always the Qatlas connection's bound team"},
		{Name: "verified", Description: "True when Make reports the stored credential as valid; Make reports " +
			"nothing else about the test"},
	},
	Examples: []capability.Example{{Description: "Test one connection", Arguments: json.RawMessage(`{"connection_id":1}`)}},
}

// connectionsReadRisk is makeReadRisk for stored credentials.
var connectionsReadRisk = capability.Risk{
	Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: connectionsSensitivity,
}

// connectionJSON is the allow-list of Make's connection object: nothing outside it is ever decoded.
type connectionJSON struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	AccountName    string `json:"accountName"`
	AccountType    string `json:"accountType"`
	PackageName    string `json:"packageName"`
	Expire         string `json:"expire"`
	Scoped         bool   `json:"scoped"`
	TeamID         int64  `json:"teamId"`
	OrganizationID int64  `json:"organizationId"`
	Editable       bool   `json:"editable"`
}

// ConnectionSummary is the stable, scope-checked view of one Make connection.
type ConnectionSummary struct {
	ID             int64  `json:"id"`
	Name           string `json:"name,omitempty"`
	AccountName    string `json:"account_name,omitempty"`
	AccountType    string `json:"account_type,omitempty"`
	PackageName    string `json:"package_name,omitempty"`
	Expire         string `json:"expire,omitempty"`
	Scoped         bool   `json:"scoped"`
	TeamID         int64  `json:"team_id"`
	OrganizationID int64  `json:"organization_id,omitempty"`
	Editable       bool   `json:"editable"`
}

func boundText(value string) string {
	if len(value) > maxConnectionText {
		return value[:maxConnectionText]
	}
	return value
}

func connectionSummaryOf(c connectionJSON) ConnectionSummary {
	return ConnectionSummary{ID: c.ID, Name: boundText(c.Name), AccountName: boundText(c.AccountName),
		AccountType: boundText(c.AccountType), PackageName: boundText(c.PackageName), Expire: boundText(c.Expire),
		Scoped: c.Scoped, TeamID: c.TeamID, OrganizationID: c.OrganizationID, Editable: c.Editable}
}

// allowsConnection applies the bound team and, when one is configured, the bound organization. A connection
// that reports no team never passes.
func (c *Client) allowsConnection(conn connectionJSON) bool {
	return c.scope.allowsTeam(conn.TeamID) && c.scope.allowsOrg(conn.OrganizationID)
}

// selectConnections refuses a connection with a scenario allow-list before any secret is read: Make
// connections belong to no scenario, so the narrower reading is that such a connection reaches none.
func selectConnections(resolved *config.Resolved) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if len(bound.scenarios) > 0 {
		return invalidRequest("Make connections are not scenario-bound; a connection with a scenario " +
			"allow-list cannot reach them")
	}
	return nil
}

func colsQuery(query url.Values) url.Values {
	query["cols[]"] = append([]string(nil), connectionCols...)
	return query
}

type connectionsListArguments struct {
	Type string `json:"type"`
}

// ConnectionsPage is one scope-filtered listing of connections.
type ConnectionsPage struct {
	Connections []ConnectionSummary `json:"connections"`
	Count       int                 `json:"count"`
	Truncated   bool                `json:"truncated"`
}

func invokeConnectionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list connections"
	var input connectionsListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectConnections(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	query := colsQuery(url.Values{"teamId": {strconv.FormatInt(client.scope.teamID, 10)}})
	if input.Type != "" {
		query.Set("type[]", input.Type)
	}
	var page struct {
		Connections []connectionJSON `json:"connections"`
	}
	if err := client.get(ctx, op, "/connections", query, &page, needConnectionsRead); err != nil {
		return nil, err
	}
	result := &ConnectionsPage{Connections: make([]ConnectionSummary, 0, len(page.Connections))}
	for _, conn := range page.Connections {
		if !client.allowsConnection(conn) {
			continue
		}
		if len(result.Connections) >= maxConnectionList {
			result.Truncated = true
			break
		}
		result.Connections = append(result.Connections, connectionSummaryOf(conn))
	}
	result.Count = len(result.Connections)
	return result, nil
}

type connectionArguments struct {
	ConnectionID int64 `json:"connection_id"`
}

// fetchConnection reads one connection and binds it back to this connection's team: a connection of another
// team (or organization) is refused without naming whatever it belongs to.
func (c *Client) fetchConnection(ctx context.Context, op string, id int64) (*connectionJSON, error) {
	var wrapper struct {
		Connection connectionJSON `json:"connection"`
	}
	path := "/connections/" + strconv.FormatInt(id, 10)
	if err := c.get(ctx, op, path, colsQuery(url.Values{}), &wrapper, needConnectionsRead); err != nil {
		return nil, err
	}
	if wrapper.Connection.ID != id || !c.allowsConnection(wrapper.Connection) {
		return nil, invalidRequest("connection_id is outside the targets of this connection")
	}
	return &wrapper.Connection, nil
}

// openConnection validates the id locally, then opens the client and binds the connection.
func openConnection(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (*Client, int64, *connectionJSON, error) {
	var input connectionArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, 0, nil, providerError(op, "the validated arguments could not be read")
	}
	if err := selectConnections(resolved); err != nil {
		return nil, 0, nil, err
	}
	if input.ConnectionID <= 0 {
		return nil, 0, nil, invalidRequest("connection_id must be a positive integer")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, 0, nil, err
	}
	conn, err := client.fetchConnection(ctx, op, input.ConnectionID)
	if err != nil {
		return nil, 0, nil, err
	}
	return client, input.ConnectionID, conn, nil
}

func invokeConnectionsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	_, _, conn, err := openConnection(ctx, "get connection", resolved, secrets, red, raw)
	if err != nil {
		return nil, err
	}
	return connectionSummaryOf(*conn), nil
}

// ConnectionEditableSchema lists only the names of the parameters Make allows to be edited.
type ConnectionEditableSchema struct {
	ConnectionID int64    `json:"connection_id"`
	TeamID       int64    `json:"team_id"`
	Parameters   []string `json:"parameters"`
	Count        int      `json:"count"`
}

func invokeConnectionsEditableSchema(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read connection editable schema"
	client, id, conn, err := openConnection(ctx, op, resolved, secrets, red, raw)
	if err != nil {
		return nil, err
	}
	var answer struct {
		EditableParameters []json.RawMessage `json:"editableParameters"`
	}
	path := "/connections/" + strconv.FormatInt(id, 10) + "/editable-data-schema"
	if err := client.get(ctx, op, path, nil, &answer, needConnectionsRead); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(answer.EditableParameters))
	for _, entry := range answer.EditableParameters {
		var name string
		if len(names) >= maxEditableParameters || json.Unmarshal(entry, &name) != nil || name == "" {
			continue
		}
		if len(name) > maxEditableParameterLength {
			name = name[:maxEditableParameterLength]
		}
		names = append(names, name)
	}
	return &ConnectionEditableSchema{ConnectionID: id, TeamID: conn.TeamID, Parameters: names, Count: len(names)}, nil
}

// ConnectionTest is the whole outcome of one test: Make reports only whether the credential verified.
type ConnectionTest struct {
	ConnectionID int64 `json:"connection_id"`
	TeamID       int64 `json:"team_id"`
	Verified     bool  `json:"verified"`
}

// invokeConnectionsTest reads and binds the connection first, then sends exactly one POST, never retried.
func invokeConnectionsTest(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "test connection"
	client, id, conn, err := openConnection(ctx, op, resolved, secrets, red, raw)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Verified *bool `json:"verified"`
	}
	if err := client.change(ctx, op, http.MethodPost, "/connections/"+strconv.FormatInt(id, 10)+"/test", nil,
		nil, &answer, needConnectionsWrite, connectionTestUncertain); err != nil {
		return nil, err
	}
	if answer.Verified == nil {
		return nil, invalidResponse(op, "Make did not report a verification result"+connectionTestUncertain)
	}
	return &ConnectionTest{ConnectionID: id, TeamID: conn.TeamID, Verified: *answer.Verified}, nil
}
