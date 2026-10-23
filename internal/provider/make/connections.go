package makeapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The four tools of this file list, read, and test the connections (stored third-party credentials) of the
// bound team. A connection's secrets never leave Qatlas: every answer is decoded into a struct that holds
// only the allow-listed fields below, so tokens, data, and anything else Make may add are dropped before
// they could be returned. Make's connection answer is not documented field by field beyond its property
// names, which is why the allow-list, not a deny-list, decides.
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
	// maxConnectionTypes, maxScopeCheckTypes, maxScopeCheckScopes, and maxScopeLength bound the list filters.
	maxConnectionTypes  = 20
	maxScopeCheckTypes  = 10
	maxScopeCheckScopes = 50
	maxScopeLength      = 256
	// maxReadScopes bounds the scopes one connection read returns.
	maxReadScopes = 200
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

// readConnectionCols is what the two read tools ask Make for: the allow-list plus the documented descriptive
// fields. The write tools keep asking for connectionCols only.
var readConnectionCols = append(append([]string{}, connectionCols...), "accountLabel", "metadata", "theme",
	"upgradeable", "scopesCnt", "uid", "connectedSystemId")

// listTypePattern is the shape of a connection type (account_name), shared by type, types, and the
// keys of scope_check.
const listTypePattern = `^[A-Za-z0-9_.-]{1,64}$`

var listTypeRegexp = regexp.MustCompile(listTypePattern)

var connectionIDSchema = idSchema

var connectionIDArgument = capability.Argument{Name: "connection_id", Required: true,
	Description: "Make connection identifier (not a Qatlas connection); its team is always re-checked live " +
		"against Make's own report, and a Qatlas connection with a scenario or folder allow-list reaches no Make " +
		"connections at all, since they belong to no scenario"}

const connectionSummaryProperties = `"id":{"type":"integer"},"name":{"type":"string"},"account_name":{"type":"string"},` +
	`"account_type":{"type":"string"},"package_name":{"type":"string"},"expire":{"type":"string"},` +
	`"scoped":{"type":"boolean"},"team_id":{"type":"integer"},"organization_id":{"type":"integer"},` +
	`"editable":{"type":"boolean"}`

var connectionSummarySchema = `{"type":"object","properties":{` + connectionSummaryProperties +
	`},"required":["id","team_id"],"additionalProperties":false}`

const connectionReadProperties = connectionSummaryProperties + `,` +
	`"account_label":{"type":"string"},"metadata_value":{"type":"string"},"metadata_type":{"type":"string"},` +
	`"theme":{"type":"string"},"upgradeable":{"type":"boolean"},"scopes_count":{"type":"integer"},` +
	`"uid":{"type":"string"},"connected_system_id":{"type":"string"}`

const connectionScopesProperties = `"scopes":{"type":"array","items":{"type":"object","properties":{` +
	`"id":{"type":"string"},"name":{"type":"string"},"account":{"type":"string"}},"additionalProperties":false}},` +
	`"scopes_truncated":{"type":"boolean"}`

// connectionListItemSchema and connectionGetSchema differ by the scopes, which Make allows in cols[] only on
// the single read.
var connectionListItemSchema = `{"type":"object","properties":{` + connectionReadProperties +
	`},"required":["id","team_id"],"additionalProperties":false}`

var connectionGetSchema = `{"type":"object","properties":{` + connectionReadProperties + `,` +
	connectionScopesProperties + `},"required":["id","team_id"],"additionalProperties":false}`

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

var connectionReadFields = []capability.Field{
	{Name: "account_label", Description: "Account label Make shows for the connection, untrusted data"},
	{Name: "metadata_value", Description: "Account metadata value (for example an email address of the " +
		"connected account), untrusted data"},
	{Name: "metadata_type", Description: "Kind of the metadata value as Make reports it, string or email"},
	{Name: "theme", Description: "Color of the connection's app as Make reports it, untrusted data"},
	{Name: "upgradeable", Description: "True when Make reports the connection as upgradeable"},
	{Name: "scopes_count", Description: "Number of scopes Make reports for the connection"},
	{Name: "uid", Description: "Connection uid as Make reports it, untrusted data"},
	{Name: "connected_system_id", Description: "Identifier of the connected third-party account as Make " +
		"reports it, untrusted data"},
}

var connectionScopeFields = []capability.Field{
	{Name: "scopes", Description: "Scopes granted to the connection, each with id, name, and account, " +
		"untrusted data, at most " + strconv.Itoa(maxReadScopes) + "; never a token or stored value"},
	{Name: "account", Description: "Account a scope entry belongs to, untrusted data"},
	{Name: "scopes_truncated", Description: "True when more scopes were reported than are returned"},
}

var connectionsList = capability.Descriptor{
	ID: Provider + ".connections.list", Version: 1, Title: "List Make connections",
	Description: "List the Make connections (stored third-party credentials) of the bound team by allow-listed " +
		"metadata only; never a token, secret, or other stored value. Refused on a connection " +
		"with a scenario or folder allow-list. scope_check asks Make whether the connections of the named " +
		"types hold the given scopes and fills scoped",
	Tags: []string{"make", "connections", "list", "automation"}, Risk: connectionsReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"type":{"type":"string","pattern":"` + listTypePattern + `"},` +
		`"types":{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(maxConnectionTypes) +
		`,"items":{"type":"string","pattern":"` + listTypePattern + `"}},` +
		`"scope_check":{"type":"object","minProperties":1,"maxProperties":` + strconv.Itoa(maxScopeCheckTypes) +
		`,"additionalProperties":{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(maxScopeCheckScopes) +
		`,"items":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxScopeLength) + `}}}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"connections":{"type":"array","items":` + connectionListItemSchema + `},` +
		`"count":{"type":"integer"},"truncated":{"type":"boolean"}},` +
		`"required":["connections","count","truncated"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "type", Description: "When set, list only connections of this Make connection type (account_name); " +
			"not together with types"},
		{Name: "types", Description: "1 to " + strconv.Itoa(maxConnectionTypes) + " Make connection types " +
			"(account_name); not together with type"},
		{Name: "scope_check", Description: "Object of connection type to 1 to " + strconv.Itoa(maxScopeCheckScopes) +
			" scopes of 1 to " + strconv.Itoa(maxScopeLength) + " characters, for at most " +
			strconv.Itoa(maxScopeCheckTypes) + " types; every type must be named in type or types. Make then " +
			"reports in scoped whether the connection of that type holds those scopes"},
	},
	Fields: append(append(append([]capability.Field{}, connectionSummaryFields...), connectionReadFields...),
		capability.Field{Name: "count", Description: "Number of connections returned, after the team boundary " +
			"was re-applied"},
		capability.Field{Name: "truncated", Description: "True when more than " + strconv.Itoa(maxConnectionList) +
			" connections were reported and the rest was dropped"},
	),
	Examples: []capability.Example{{Description: "List the team's connections", Arguments: json.RawMessage(`{}`)}},
}

var connectionsGet = capability.Descriptor{
	ID: Provider + ".connections.get", Version: 1, Title: "Get a Make connection",
	Description: "Read one Make connection of the bound team by allow-listed metadata and the scopes granted to " +
		"it; never a token, secret, or other stored value",
	Tags: []string{"make", "connections", "get", "automation"}, Risk: connectionsReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"connection_id":` + connectionIDSchema + `},` +
		`"required":["connection_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(connectionGetSchema),
	Arguments:    []capability.Argument{connectionIDArgument},
	Fields: append(append(append([]capability.Field{}, connectionSummaryFields...), connectionReadFields...),
		connectionScopeFields...),
	Examples: []capability.Example{{Description: "Read one connection", Arguments: json.RawMessage(`{"connection_id":1}`)}},
}

var connectionsEditableSchema = capability.Descriptor{
	ID: Provider + ".connections.editable-schema", Version: 1, Title: "List a Make connection's editable parameters",
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

// connectionDetailJSON adds the documented descriptive fields to the allow-list. Their JSON types are not
// guaranteed, so each is read tolerantly instead of failing the whole call.
type connectionDetailJSON struct {
	connectionJSON
	AccountLabel      json.RawMessage `json:"accountLabel"`
	Metadata          json.RawMessage `json:"metadata"`
	Theme             json.RawMessage `json:"theme"`
	Upgradeable       json.RawMessage `json:"upgradeable"`
	ScopesCnt         json.RawMessage `json:"scopesCnt"`
	UID               json.RawMessage `json:"uid"`
	ConnectedSystemID json.RawMessage `json:"connectedSystemId"`
	Scopes            json.RawMessage `json:"scopes"`
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

// ConnectionScope is one scope granted to a connection.
type ConnectionScope struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Account string `json:"account,omitempty"`
}

// ConnectionRead is the summary plus the descriptive fields of the read tools. The write tools keep returning
// ConnectionSummary.
type ConnectionRead struct {
	ConnectionSummary
	AccountLabel      string            `json:"account_label,omitempty"`
	MetadataValue     string            `json:"metadata_value,omitempty"`
	MetadataType      string            `json:"metadata_type,omitempty"`
	Theme             string            `json:"theme,omitempty"`
	Upgradeable       *bool             `json:"upgradeable,omitempty"`
	ScopesCount       *int64            `json:"scopes_count,omitempty"`
	UID               string            `json:"uid,omitempty"`
	ConnectedSystemID string            `json:"connected_system_id,omitempty"`
	Scopes            []ConnectionScope `json:"scopes,omitempty"`
	ScopesTruncated   bool              `json:"scopes_truncated,omitempty"`
}

func connectionReadOf(c connectionDetailJSON) ConnectionRead {
	read := ConnectionRead{ConnectionSummary: connectionSummaryOf(c.connectionJSON),
		AccountLabel: rawText(c.AccountLabel), Theme: rawText(c.Theme), UID: rawText(c.UID),
		ConnectedSystemID: rawText(c.ConnectedSystemID), Upgradeable: rawBool(c.Upgradeable),
		ScopesCount: rawInt(c.ScopesCnt)}
	if metadata := rawObject(c.Metadata); metadata != nil {
		read.MetadataValue, read.MetadataType = rawText(metadata["value"]), rawText(metadata["type"])
	}
	var scopes []map[string]json.RawMessage
	if json.Unmarshal(c.Scopes, &scopes) == nil {
		for _, scope := range scopes {
			if len(read.Scopes) >= maxReadScopes {
				read.ScopesTruncated = true
				break
			}
			entry := ConnectionScope{ID: rawText(scope["id"]), Name: rawText(scope["name"]),
				Account: rawText(scope["account"])}
			if entry != (ConnectionScope{}) {
				read.Scopes = append(read.Scopes, entry)
			}
		}
	}
	return read
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

// selectConnections refuses a connection with a scenario or folder allow-list before any secret is read: Make
// connections belong to no scenario, so the narrower reading is that such a connection reaches none.
func selectConnections(resolved *config.Resolved) error {
	bound, err := boundScope(resolved)
	if err != nil {
		return err
	}
	if bound.restricted() {
		return invalidRequest("Make connections are not scenario-bound; a connection with a scenario " +
			"allow-list cannot reach them")
	}
	return nil
}

func colsQuery(query url.Values) url.Values {
	return colsOf(connectionCols, query)
}

func colsOf(cols []string, query url.Values) url.Values {
	query["cols[]"] = append([]string(nil), cols...)
	return query
}

type connectionsListArguments struct {
	Type       string              `json:"type"`
	Types      []string            `json:"types"`
	ScopeCheck map[string][]string `json:"scope_check"`
}

// listFilters validates type, types, and scope_check locally, before any request, and returns the query
// parameters they stand for. scope_check keys become "<type>[]" parameters, so a type that would collide with
// another parameter of the request is refused.
func (a connectionsListArguments) listFilters() (types []string, check url.Values, err error) {
	if a.Type != "" && a.Types != nil {
		return nil, nil, invalidRequest("type and types cannot be combined")
	}
	if a.Type != "" {
		if !listTypeRegexp.MatchString(a.Type) {
			return nil, nil, invalidRequest("type must be a Make connection type")
		}
		types = []string{a.Type}
	}
	if a.Types != nil {
		if len(a.Types) == 0 || len(a.Types) > maxConnectionTypes {
			return nil, nil, invalidRequest("types needs 1 to " + strconv.Itoa(maxConnectionTypes) + " entries")
		}
		seen := map[string]bool{}
		for _, t := range a.Types {
			if !listTypeRegexp.MatchString(t) {
				return nil, nil, invalidRequest("types may hold only Make connection types")
			}
			if !seen[t] {
				seen[t] = true
				types = append(types, t)
			}
		}
	}
	if a.ScopeCheck == nil {
		return types, nil, nil
	}
	if len(a.ScopeCheck) == 0 || len(a.ScopeCheck) > maxScopeCheckTypes {
		return nil, nil, invalidRequest("scope_check needs 1 to " + strconv.Itoa(maxScopeCheckTypes) + " types")
	}
	check = url.Values{}
	for connType, scopes := range a.ScopeCheck {
		known := false
		for _, t := range types {
			known = known || t == connType
		}
		if !known {
			return nil, nil, invalidRequest("every scope_check type must be named in type or types")
		}
		if connType == "type" || connType == "cols" {
			return nil, nil, invalidRequest("scope_check cannot check a connection type that Make's own " +
				"parameters share a name with")
		}
		if len(scopes) == 0 || len(scopes) > maxScopeCheckScopes {
			return nil, nil, invalidRequest("scope_check needs 1 to " + strconv.Itoa(maxScopeCheckScopes) +
				" scopes per type")
		}
		for _, scope := range scopes {
			if n := utf8.RuneCountInString(scope); n == 0 || n > maxScopeLength || !utf8.ValidString(scope) || !noControl(scope) {
				return nil, nil, invalidRequest("a scope must be 1 to " + strconv.Itoa(maxScopeLength) +
					" characters without control characters")
			}
		}
		check[connType+"[]"] = append([]string(nil), scopes...)
	}
	return types, check, nil
}

// ConnectionsPage is one scope-filtered listing of connections.
type ConnectionsPage struct {
	Connections []ConnectionRead `json:"connections"`
	Count       int              `json:"count"`
	Truncated   bool             `json:"truncated"`
}

func invokeConnectionsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list connections"
	var input connectionsListArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	types, check, err := input.listFilters()
	if err != nil {
		return nil, err
	}
	if err := selectConnections(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	query := colsOf(readConnectionCols, url.Values{"teamId": {strconv.FormatInt(client.scope.teamID, 10)}})
	if len(types) > 0 {
		query["type[]"] = types
	}
	for key, scopes := range check {
		query[key] = scopes
	}
	var page struct {
		Connections []connectionDetailJSON `json:"connections"`
	}
	if err := client.get(ctx, op, "/connections", query, &page, needConnectionsRead); err != nil {
		return nil, err
	}
	result := &ConnectionsPage{Connections: make([]ConnectionRead, 0, len(page.Connections))}
	for _, conn := range page.Connections {
		if !client.allowsConnection(conn.connectionJSON) {
			continue
		}
		if len(result.Connections) >= maxConnectionList {
			result.Truncated = true
			break
		}
		conn.Scopes = nil // not asked for in a list; whatever Make adds anyway is never returned
		result.Connections = append(result.Connections, connectionReadOf(conn))
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
	detail, err := c.fetchConnectionCols(ctx, op, id, connectionCols)
	if err != nil {
		return nil, err
	}
	return &detail.connectionJSON, nil
}

func (c *Client) fetchConnectionCols(ctx context.Context, op string, id int64, cols []string) (*connectionDetailJSON, error) {
	var wrapper struct {
		Connection connectionDetailJSON `json:"connection"`
	}
	path := "/connections/" + strconv.FormatInt(id, 10)
	if err := c.get(ctx, op, path, colsOf(cols, url.Values{}), &wrapper, needConnectionsRead); err != nil {
		return nil, err
	}
	if wrapper.Connection.ID != id || !c.allowsConnection(wrapper.Connection.connectionJSON) {
		return nil, invalidRequest("connection_id is outside the targets of this connection")
	}
	return &wrapper.Connection, nil
}

// openConnection validates the id locally, then opens the client and binds the connection.
func openConnection(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (*Client, int64, *connectionJSON, error) {
	client, id, conn, err := openConnectionCols(ctx, op, resolved, secrets, red, raw, connectionCols)
	if err != nil {
		return nil, 0, nil, err
	}
	return client, id, &conn.connectionJSON, nil
}

func openConnectionCols(ctx context.Context, op string, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage, cols []string) (*Client, int64, *connectionDetailJSON, error) {
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
	conn, err := client.fetchConnectionCols(ctx, op, input.ConnectionID, cols)
	if err != nil {
		return nil, 0, nil, err
	}
	return client, input.ConnectionID, conn, nil
}

func invokeConnectionsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	// scopes is allowed in cols[] only on this single read.
	cols := append(append([]string{}, readConnectionCols...), "scopes")
	_, _, conn, err := openConnectionCols(ctx, "get connection", resolved, secrets, red, raw, cols)
	if err != nil {
		return nil, err
	}
	return connectionReadOf(*conn), nil
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
