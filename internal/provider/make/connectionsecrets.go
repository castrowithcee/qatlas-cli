package makeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// The two tools of this file create a connection of the bound team and set the data of an existing one. A
// secret value never comes from the caller: it is a field of a forward credential that the Qatlas connection
// releases, named by the secret_ref argument, read only after the confirmation gate and merged, under its own
// field name, into the request body. The fields argument carries plain, non-secret values only.
// API (checked 2026-10-04 against developers.make.com's published API reference, api-reference/connections,
// not a live account):
//   - POST /connections?teamId=TEAM with {"accountName" (the connection name, max 128 characters),
//     "accountType" (the app's connection type), "scopes", and further properties the type needs, such as
//     clientId and clientSecret}, connections:write, answering {"connection":{...}}. accountVisibility is
//     deliberately not offered.
//   - POST /connections/{id}/set-data with an object of the parameters that GET .../editable-data-schema
//     lists, connections:write, answering {"changed":boolean}. The new data REPLACE the old: a field that is
//     missing is set empty. An OAuth connection must afterwards be reauthorized in Make's web interface by a
//     person; any other type uses the new data at once. A connection that cannot be edited is refused by
//     Make ("Cannot edit this connection").
// Operation IDs have three segments, so set-data is make.connections.setdata.

const (
	// connectionSecretsSensitivity labels the secrets these tools send to Make.
	connectionSecretsSensitivity = "make-connection-secrets"
	// maxConnectionFields bounds the plain fields of one request.
	maxConnectionFields = 50
	// maxConnectionFieldNameLength bounds one field name.
	maxConnectionFieldNameLength = 64
	// maxConnectionFieldTextLength bounds one plain string value, in characters.
	maxConnectionFieldTextLength = 1024
	// maxConnectionScopes and maxConnectionScopeLength bound the scopes of a new connection.
	maxConnectionScopes      = 50
	maxConnectionScopeLength = 256
	// maxSecretRefLength bounds the name of a referenced forward credential.
	maxSecretRefLength = 200

	needConnectionsCreate = "the connections:write scope"
	needConnectionsSet    = "the connections:write scope (and connections:read, which binds the connection) " +
		"and, for a connection restricted to its access list, a place on that list with permission to manage it"

	connectionCreateUncertain = "; the connection may have been created, list the connections before creating " +
		"it again"
	connectionSetUncertain = "; the data may have been replaced, test the connection before setting it again"
)

var connectionTypePattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,100}$`)

// reservedConnectionFields are the members the create tool builds itself; a plain field never names one.
var reservedConnectionFields = map[string]bool{"accountName": true, "accountType": true, "scopes": true,
	"accountVisibility": true, "teamId": true}

const connectionSecretNote = "Secret values are never an argument: secret_ref names a forward credential that " +
	"this Qatlas connection releases, and Make receives its fields, under their own names, as part of the " +
	"request body. A plain field with the same name as a field of that credential is refused. fields holds " +
	"plain, non-secret values only"

var connectionFieldsSchema = `{"type":"object","maxProperties":` + strconv.Itoa(maxConnectionFields) +
	`,"additionalProperties":{"anyOf":[{"type":"string","maxLength":` + strconv.Itoa(maxConnectionFieldTextLength) +
	`},{"type":"number"},{"type":"boolean"}]}}`

var connectionSecretRefSchema = `{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxSecretRefLength) + `}`

var connectionFieldsDescription = "Plain, non-secret connection fields as an object of strings (at most " +
	strconv.Itoa(maxConnectionFieldTextLength) + " characters), numbers, or booleans; at most " +
	strconv.Itoa(maxConnectionFields) + " fields, names of letters, digits, '_', '-' and '.'"

var connectionSecretRefArgument = capability.Argument{Name: "secret_ref", SecretRef: true,
	Description: "Name of a released forward credential whose fields supply the secret values of the " +
		"connection; never a value"}

func connectionSecretsRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: connectionSecretsSensitivity}
}

var connectionsCreate = capability.Descriptor{
	ID: Provider + ".connections.create", Version: 1, Title: "Create a Make connection",
	Description: "Create one connection (stored third-party credentials) in the bound team, always the Qatlas " +
		"connection's own team. connection_type is Make's type of the app's connection (accountType), scopes " +
		"are the optional scopes of that type, fields the plain properties the type needs, and the secret " +
		"properties, such as a client secret or password, come from secret_ref. " + connectionSecretNote +
		". A connection of an OAuth type is not authorized by this tool: a person has to complete the " +
		"authorization in Make's web interface. A repeated call creates another connection. The answer is " +
		"the allow-listed connection metadata, never a stored value. Needs the connections:write scope. " +
		"Offered only by a connection whose tools list names it, in no profile",
	Tags: []string{"make", "connections", "create", "automation"},
	Risk: connectionSecretsRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + connectionNameSchema + `,` +
		`"connection_type":{"type":"string","pattern":"^[A-Za-z0-9_.:-]{1,100}$"},` +
		`"scopes":{"type":"array","maxItems":` + strconv.Itoa(maxConnectionScopes) +
		`,"items":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxConnectionScopeLength) + `}},` +
		`"fields":` + connectionFieldsSchema + `,"secret_ref":` + connectionSecretRefSchema + `},` +
		`"required":["name","connection_type"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(connectionSummarySchema),
	Arguments: []capability.Argument{
		{Name: "name", Required: true, Description: "Connection name, 1 to " + strconv.Itoa(maxConnectionNameLength) +
			" characters, without control characters"},
		{Name: "connection_type", Required: true, Description: "Make connection type of the app (accountType), " +
			"letters, digits, '.', '_', ':' and '-', at most 100 characters"},
		{Name: "scopes", Description: "Scopes of the connection, as the type defines them; at most " +
			strconv.Itoa(maxConnectionScopes) + " of at most " + strconv.Itoa(maxConnectionScopeLength) + " characters"},
		{Name: "fields", Description: connectionFieldsDescription + "; accountName, accountType, scopes, " +
			"accountVisibility, and teamId are not accepted"},
		connectionSecretRefArgument,
	},
	Fields: connectionSummaryFields,
	Examples: []capability.Example{{Description: "Create a connection, the secret from a released forward credential",
		Arguments: json.RawMessage(`{"name":"Shop API","connection_type":"example-type",` +
			`"fields":{"host":"shop.example.com"},"secret_ref":"make-shop-secret"}`)}},
}

var connectionsSetData = capability.Descriptor{
	ID: Provider + ".connections.setdata", Version: 1, Title: "Set the data of a Make connection",
	Description: "Set the data of one connection of the bound team. The new data REPLACE the stored data: a " +
		"field that is in neither fields nor the referenced forward credential is set empty by Make, a stored " +
		"secret included, so give every plain field in fields and every secret field through secret_ref; " +
		"make.connections.editableschema lists the parameter names. A connection of an OAuth type must " +
		"afterwards be reauthorized by a person in Make's web interface (the Reauthorize button) before it " +
		"works again; any other type uses the new data at once. Make refuses a connection it cannot edit. " +
		"The connection is read and bound to the bound team first; one of fields or secret_ref is required. " +
		connectionSecretNote + ". Needs the connections:read and connections:write scopes. Offered only by a " +
		"connection whose tools list names it, in no profile",
	Tags: []string{"make", "connections", "setdata", "automation"},
	Risk: connectionSecretsRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"connection_id":` + connectionIDSchema + `,` +
		`"fields":` + connectionFieldsSchema + `,"secret_ref":` + connectionSecretRefSchema + `},` +
		`"required":["connection_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"connection_id":{"type":"integer"},` +
		`"team_id":{"type":"integer"},"changed":{"type":"boolean"}},` +
		`"required":["connection_id","team_id","changed"],"additionalProperties":false}`),
	Arguments: []capability.Argument{connectionIDArgument,
		{Name: "fields", Description: connectionFieldsDescription +
			"; the whole stored data is replaced, so give all plain fields"},
		{Name: "secret_ref", SecretRef: true, Description: connectionSecretRefArgument.Description +
			"; the whole stored data is replaced, so it must supply all secret fields"}},
	Fields: []capability.Field{
		{Name: "connection_id", Description: "The connection that was addressed"},
		{Name: "team_id", Description: "Team of the connection; always the Qatlas connection's bound team"},
		{Name: "changed", Description: "True when Make reports the data as changed; an OAuth connection still " +
			"needs a reauthorization in Make's web interface"},
	},
	Examples: []capability.Example{{Description: "Replace the data: all plain fields, all secret fields from the reference",
		Arguments: json.RawMessage(`{"connection_id":1,"fields":{"host":"shop.example.com"},` +
			`"secret_ref":"make-shop-secret"}`)}},
}

type connectionSecretArguments struct {
	Name           string         `json:"name"`
	ConnectionType string         `json:"connection_type"`
	Scopes         []string       `json:"scopes"`
	Fields         map[string]any `json:"fields"`
	SecretRef      string         `json:"secret_ref"`
}

// readConnectionSecretArguments keeps numbers as written so they are sent exactly as given.
func readConnectionSecretArguments(op string, raw json.RawMessage) (connectionSecretArguments, error) {
	var input connectionSecretArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

func validConnectionFieldName(name string) bool {
	if name == "" || len(name) > maxConnectionFieldNameLength {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case (c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') && i > 0:
		default:
			return false
		}
	}
	return true
}

func noControl(text string) bool {
	return utf8.ValidString(text) && !strings.ContainsFunc(text, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// checkConnectionFields bounds the plain fields: a limited number of strictly named fields holding a short
// string, a number, or a boolean, never null, an object, or an array.
func checkConnectionFields(fields map[string]any) error {
	if len(fields) > maxConnectionFields {
		return invalidRequest("fields has more than " + strconv.Itoa(maxConnectionFields) + " fields")
	}
	for name, value := range fields {
		if !validConnectionFieldName(name) {
			return invalidRequest("a field name must be letters, digits, '_', '-' or '.', starting with a letter, " +
				"at most " + strconv.Itoa(maxConnectionFieldNameLength) + " characters")
		}
		switch v := value.(type) {
		case string:
			if !utf8.ValidString(v) || utf8.RuneCountInString(v) > maxConnectionFieldTextLength {
				return invalidRequest("a field value must be valid text of at most " +
					strconv.Itoa(maxConnectionFieldTextLength) + " characters")
			}
		case json.Number, bool:
		default:
			return invalidRequest("a field value must be a string, a number, or a boolean")
		}
	}
	return nil
}

// connectionFieldsBody builds the request body from the validated plain fields, then merges the fields of the
// referenced forward credential, which the core released for this confirmed request. A member that is already
// there is refused, never overwritten. The error names no value.
func connectionFieldsBody(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	body map[string]any, ref string) (map[string]any, error) {
	if ref == "" {
		return body, nil
	}
	values, err := capability.ResolveSecretRef(ctx, resolved, secrets, ref)
	if err != nil {
		return nil, err
	}
	if err := capability.MergeSecretFields(body, values); err != nil {
		return nil, invalidRequest("a field has the name of a field of the referenced secret or of a member " +
			"the tool sets itself")
	}
	return body, nil
}

func invokeConnectionsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create connection"
	input, err := readConnectionSecretArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if err := validConnectionName(input.Name); err != nil {
		return nil, err
	}
	if !connectionTypePattern.MatchString(input.ConnectionType) {
		return nil, invalidRequest("connection_type must be 1 to 100 letters, digits, '.', '_', ':' or '-'")
	}
	if len(input.Scopes) > maxConnectionScopes {
		return nil, invalidRequest("scopes has more than " + strconv.Itoa(maxConnectionScopes) + " entries")
	}
	for _, scope := range input.Scopes {
		if scope == "" || utf8.RuneCountInString(scope) > maxConnectionScopeLength || !noControl(scope) {
			return nil, invalidRequest("a scope must be 1 to " + strconv.Itoa(maxConnectionScopeLength) +
				" characters without control characters")
		}
	}
	if err := checkConnectionFields(input.Fields); err != nil {
		return nil, err
	}
	for name := range input.Fields {
		if reservedConnectionFields[name] {
			return nil, invalidRequest("fields must not contain accountName, accountType, scopes, " +
				"accountVisibility, or teamId")
		}
	}
	if err := selectConnections(resolved); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	body := make(map[string]any, len(input.Fields)+3)
	for name, value := range input.Fields {
		body[name] = value
	}
	body["accountName"] = input.Name
	body["accountType"] = input.ConnectionType
	if len(input.Scopes) > 0 {
		body["scopes"] = input.Scopes
	}
	if body, err = connectionFieldsBody(ctx, resolved, secrets, body, input.SecretRef); err != nil {
		return nil, err
	}
	var answer struct {
		Connection connectionJSON `json:"connection"`
	}
	query := url.Values{"teamId": {strconv.FormatInt(client.scope.teamID, 10)}}
	if err := client.change(ctx, op, http.MethodPost, "/connections", query, body, &answer,
		needConnectionsCreate, connectionCreateUncertain); err != nil {
		return nil, err
	}
	if answer.Connection.ID <= 0 {
		return nil, invalidResponse(op, "Make did not report the created connection"+connectionCreateUncertain)
	}
	if !client.allowsConnection(answer.Connection) {
		return nil, providerError(op, "Make did not keep the result inside this connection's targets; "+
			"the connection was already created")
	}
	return connectionSummaryOf(answer.Connection), nil
}

// ConnectionDataSet is the outcome of one set-data request.
type ConnectionDataSet struct {
	ConnectionID int64 `json:"connection_id"`
	TeamID       int64 `json:"team_id"`
	Changed      bool  `json:"changed"`
}

func invokeConnectionsSetData(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "set connection data"
	input, err := readConnectionSecretArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if err := checkConnectionFields(input.Fields); err != nil {
		return nil, err
	}
	if len(input.Fields) == 0 && input.SecretRef == "" {
		return nil, invalidRequest("fields or secret_ref is required")
	}
	client, id, conn, err := openConnection(ctx, op, resolved, secrets, red, raw)
	if err != nil {
		return nil, err
	}
	body := make(map[string]any, len(input.Fields))
	for name, value := range input.Fields {
		body[name] = value
	}
	if body, err = connectionFieldsBody(ctx, resolved, secrets, body, input.SecretRef); err != nil {
		return nil, err
	}
	var answer struct {
		Changed *bool `json:"changed"`
	}
	if err := client.change(ctx, op, http.MethodPost, connectionPath(id)+"/set-data", nil, body, &answer,
		needConnectionsSet, connectionSetUncertain); err != nil {
		return nil, err
	}
	if answer.Changed == nil {
		return nil, invalidResponse(op, "Make did not report whether the data changed"+connectionSetUncertain)
	}
	return &ConnectionDataSet{ConnectionID: id, TeamID: conn.TeamID, Changed: *answer.Changed}, nil
}
