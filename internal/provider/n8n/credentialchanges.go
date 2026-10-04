package n8n

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Credentials are created, updated, moved, and deleted here. A secret value never comes from the caller: it
// is a field of a forward credential that the connection releases, named by the secret_ref argument, read
// only after the confirmation gate and merged into the request's data object. The data argument carries
// plain, non-secret fields only.
const (
	// maxCredentialDataFields bounds the plain data fields of one request.
	maxCredentialDataFields = 50
	// maxCredentialFieldNameLength bounds one data field name.
	maxCredentialFieldNameLength = 64
	// maxCredentialFieldTextLength bounds one plain string value, in characters.
	maxCredentialFieldTextLength = 1024
	// maxSecretRefLength bounds the name of a referenced forward credential.
	maxSecretRefLength = 200
)

func credentialChangeRisk(effect capability.Effect, idempotency capability.Idempotency) capability.Risk {
	return capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired,
		OpenWorld: true, DataSensitivity: credentialDataSensitivity}
}

var credentialNameSchema = `{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCredentialTextLength) + `}`

var secretRefSchema = `{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxSecretRefLength) + `}`

var credentialDataSchema = `{"type":"object","maxProperties":` + strconv.Itoa(maxCredentialDataFields) +
	`,"additionalProperties":{"anyOf":[{"type":"string","maxLength":` + strconv.Itoa(maxCredentialFieldTextLength) +
	`},{"type":"number"},{"type":"boolean"}]}}`

const credentialSecretRefNote = "Secret values are never an argument: secret_ref names a forward credential that " +
	"this connection releases, and n8n receives its fields, under their own names, as part of the credential " +
	"data. A data field with the same name as a field of that credential is refused. data holds plain, " +
	"non-secret fields only"

var credentialNameArgument = capability.Argument{Name: "name",
	Description: "Credential name, 1 to " + strconv.Itoa(maxCredentialTextLength) + " characters, no control characters"}

var credentialDataArgument = capability.Argument{Name: "data",
	Description: "Plain, non-secret credential fields as an object of strings (at most " +
		strconv.Itoa(maxCredentialFieldTextLength) + " characters), numbers, or booleans; at most " +
		strconv.Itoa(maxCredentialDataFields) + " fields, names of letters, digits, '_', '-' and '.'"}

var credentialSecretRefArgument = capability.Argument{Name: "secret_ref", SecretRef: true,
	Description: "Name of a released forward credential whose fields supply the secret values of the " +
		"credential data; never a value"}

var credentialChangeFields = []capability.Field{
	{Name: "id", Description: "Credential identifier"},
	{Name: "name", Description: "Credential name, untrusted data"},
	{Name: "type", Description: "Credential type name"},
	{Name: "created_at", Description: "Creation time, as n8n reports it"},
	{Name: "updated_at", Description: "Last update time, as n8n reports it"},
}

var credentialsCreate = capability.Descriptor{
	ID: Provider + ".credentials.create", Version: 1, Title: "Create an n8n credential",
	Description: "Create one credential of a given type. " + credentialSecretRefNote + ". With a project allow-list " +
		"project_id is required and must be one of its projects; without one the credential is created in the " +
		"API key owner's personal project unless project_id says otherwise. A repeated call creates another " +
		"credential. Offered only by a connection whose tools list names it. " + credentialScopeNote,
	Tags: []string{"n8n", "credentials", "create", "automation"},
	Risk: credentialChangeRisk(capability.EffectCreate, capability.IdempotencyNonIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"name":` + credentialNameSchema + `,` +
		`"credential_type":` + credentialTypeSchema + `,"data":` + credentialDataSchema + `,` +
		`"secret_ref":` + secretRefSchema + `,"project_id":` + targetIDSchema + `},` +
		`"required":["name","credential_type"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(credentialItemSchema),
	Arguments: []capability.Argument{
		{Name: "name", Description: credentialNameArgument.Description, Required: true},
		{Name: "credential_type", Required: true,
			Description: "Credential type name, letters, digits, '.', '_' and '-', at most 100 characters"},
		credentialDataArgument, credentialSecretRefArgument,
		{Name: "project_id", Description: "Project to create the credential in; required with a project allow-list"},
	},
	Fields: credentialChangeFields,
	Examples: []capability.Example{{Description: "Create a basic-auth credential, the password from a released forward credential",
		Arguments: json.RawMessage(`{"name":"Reporting API","credential_type":"httpBasicAuth",` +
			`"data":{"user":"reporting"},"secret_ref":"n8n-reporting-secret"}`)}},
}

var credentialsUpdate = capability.Descriptor{
	ID: Provider + ".credentials.update", Version: 1, Title: "Update an n8n credential",
	Description: "Rename a credential and set its data. An update with data or secret_ref REPLACES the whole " +
		"stored data: a field that is in neither data nor the referenced forward credential, a stored secret " +
		"included, is lost, so give every plain field in data and every secret field through secret_ref. A " +
		"rename alone (name only) leaves the stored data unchanged. The type of a credential cannot be " +
		"changed. " + credentialSecretRefNote + ". At least one of name, data, or secret_ref is required. Offered only by a connection whose tools list " +
		"names it. " + credentialScopeNote,
	Tags: []string{"n8n", "credentials", "update", "automation"},
	Risk: credentialChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"credential_id":` + targetIDSchema + `,` +
		`"name":` + credentialNameSchema + `,"data":` + credentialDataSchema + `,"secret_ref":` + secretRefSchema + `},` +
		`"required":["credential_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(credentialItemSchema),
	Arguments: []capability.Argument{credentialIDArgument,
		{Name: "name", Description: credentialNameArgument.Description},
		{Name: "data", Description: credentialDataArgument.Description +
			"; with data or secret_ref the whole stored data is replaced, so give all plain fields"},
		{Name: "secret_ref", SecretRef: true, Description: credentialSecretRefArgument.Description +
			"; with data or secret_ref the whole stored data is replaced, so it must supply all secret fields"}},
	Fields: credentialChangeFields,
	Examples: []capability.Example{{Description: "Rotate the secrets: all plain fields in data, all secret fields from the reference",
		Arguments: json.RawMessage(`{"credential_id":"R2DjclaysHbqn778","data":{"user":"reporting"},` +
			`"secret_ref":"n8n-reporting-secret"}`)}},
}

var credentialsTransfer = capability.Descriptor{
	ID: Provider + ".credentials.transfer", Version: 1, Title: "Move an n8n credential to another project",
	Description: "Move one credential to another project. Only between projects of this connection's project " +
		"allow-list: the connection needs one, the credential must belong to one of its projects, and " +
		"destination_project_id must be one of them. Workflows that use the credential in other projects may lose " +
		"access to it. Offered only by a connection whose tools list names it. " + credentialScopeNote,
	Tags: []string{"n8n", "credentials", "transfer", "automation"},
	Risk: credentialChangeRisk(capability.EffectUpdate, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"credential_id":` + targetIDSchema + `,` +
		`"destination_project_id":` + targetIDSchema + `},` +
		`"required":["credential_id","destination_project_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},` +
		`"destination_project_id":{"type":"string"},"transferred":{"type":"boolean"}},` +
		`"required":["id","destination_project_id","transferred"],"additionalProperties":false}`),
	Arguments: []capability.Argument{credentialIDArgument,
		{Name: "destination_project_id", Required: true,
			Description: "Project to move the credential to; must be a project of this connection's allow-list"}},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the moved credential"},
		{Name: "destination_project_id", Description: "Project the credential was moved to"},
		{Name: "transferred", Description: "True when n8n accepted the move"},
	},
	Examples: []capability.Example{{Description: "Move a credential to another allowed project",
		Arguments: json.RawMessage(`{"credential_id":"R2DjclaysHbqn778","destination_project_id":"VmwOO9HeTEj20kxM"}`)}},
}

var credentialsDelete = capability.Descriptor{
	ID: Provider + ".credentials.delete", Version: 1, Title: "Delete an n8n credential",
	Description: "Delete one credential. Every workflow that uses it fails afterwards until another credential is " +
		"assigned, and the stored secret is lost. Offered only by a connection whose tools list names it. " +
		credentialScopeNote,
	Tags: []string{"n8n", "credentials", "delete", "automation"},
	Risk: credentialChangeRisk(capability.EffectDelete, capability.IdempotencyIdempotent), Provider: Provider,
	RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"credential_id":` + targetIDSchema + `},` +
		`"required":["credential_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},` +
		`"deleted":{"type":"boolean"}},"required":["id","deleted"],"additionalProperties":false}`),
	Arguments: []capability.Argument{credentialIDArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the deleted credential"},
		{Name: "deleted", Description: "True when n8n accepted the deletion"},
	},
	Examples: []capability.Example{{Description: "Delete a credential",
		Arguments: json.RawMessage(`{"credential_id":"R2DjclaysHbqn778"}`)}},
}

// CredentialDeleted is what credentials.delete reports.
type CredentialDeleted struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// CredentialTransferred is what credentials.transfer reports.
type CredentialTransferred struct {
	ID                   string `json:"id"`
	DestinationProjectID string `json:"destination_project_id"`
	Transferred          bool   `json:"transferred"`
}

type credentialChangeArguments struct {
	CredentialID         string         `json:"credential_id"`
	CredentialType       string         `json:"credential_type"`
	Name                 *string        `json:"name"`
	Data                 map[string]any `json:"data"`
	SecretRef            string         `json:"secret_ref"`
	ProjectID            string         `json:"project_id"`
	DestinationProjectID string         `json:"destination_project_id"`
}

// readCredentialChangeArguments keeps numbers as written so they are sent exactly as given.
func readCredentialChangeArguments(op string, raw json.RawMessage) (credentialChangeArguments, error) {
	var input credentialChangeArguments
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

// validCredentialName keeps a name non-empty, bounded, valid text without control characters.
func validCredentialName(name string) error {
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxCredentialTextLength {
		return invalidRequest("name must be valid text of 1 to " + strconv.Itoa(maxCredentialTextLength) + " characters")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return invalidRequest("name must not contain control characters")
		}
	}
	return nil
}

func validDataFieldName(name string) bool {
	if name == "" || len(name) > maxCredentialFieldNameLength {
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

// checkCredentialData bounds the plain data fields: a limited number of strictly named fields holding a
// short string, a number, or a boolean, never null, an object, or an array.
func checkCredentialData(data map[string]any) error {
	if len(data) > maxCredentialDataFields {
		return invalidRequest("data has more than " + strconv.Itoa(maxCredentialDataFields) + " fields")
	}
	for name, value := range data {
		if !validDataFieldName(name) {
			return invalidRequest("a data field name must be letters, digits, '_', '-' or '.', starting with a letter, " +
				"at most " + strconv.Itoa(maxCredentialFieldNameLength) + " characters")
		}
		switch v := value.(type) {
		case string:
			if !utf8.ValidString(v) || utf8.RuneCountInString(v) > maxCredentialFieldTextLength {
				return invalidRequest("a data value must be valid text of at most " +
					strconv.Itoa(maxCredentialFieldTextLength) + " characters")
			}
		case json.Number, bool:
		default:
			return invalidRequest("a data value must be a string, a number, or a boolean")
		}
	}
	return nil
}

// credentialDataBody builds the data object of a request: the validated plain fields, then the fields of the
// referenced forward credential, which the core released for this confirmed request. A plain field that
// shares a name with a secret field is refused, never overwritten. The error names no value.
func credentialDataBody(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	plain map[string]any, ref string) (map[string]any, error) {
	body := make(map[string]any, len(plain))
	for name, value := range plain {
		body[name] = value
	}
	if ref == "" {
		return body, nil
	}
	values, err := capability.ResolveSecretRef(ctx, resolved, secrets, ref)
	if err != nil {
		return nil, err
	}
	if err := capability.MergeSecretFields(body, values); err != nil {
		return nil, invalidRequest("a data field has the name of a field of the referenced secret")
	}
	return body, nil
}

// selectCredentialProject checks a project argument of a change against the project allow-list, locally.
func selectCredentialProject(bound scope, projectID, argument string) error {
	if !validTargetID(projectID) {
		return invalidRequest(argument + " must be a usable n8n identifier")
	}
	if !bound.allowsProject(projectID) {
		return invalidRequest(argument + " is outside the targets of this connection")
	}
	return nil
}

func invokeCredentialsCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "create credential"
	input, err := readCredentialChangeArguments(op, raw)
	if err != nil {
		return nil, err
	}
	bound, err := selectCredentials(resolved)
	if err != nil {
		return nil, err
	}
	name := ""
	if input.Name != nil {
		name = *input.Name
	}
	if err := validCredentialName(name); err != nil {
		return nil, err
	}
	if err := validCredentialType(input.CredentialType); err != nil {
		return nil, err
	}
	if err := checkCredentialData(input.Data); err != nil {
		return nil, err
	}
	if len(input.Data) == 0 && input.SecretRef == "" {
		return nil, invalidRequest("data or secret_ref is required")
	}
	switch {
	case input.ProjectID != "":
		if err := selectCredentialProject(bound, input.ProjectID, "project_id"); err != nil {
			return nil, err
		}
	case len(bound.projects) > 0:
		return nil, invalidRequest("project_id is required on a connection with a project allow-list")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	data, err := credentialDataBody(ctx, resolved, secrets, input.Data, input.SecretRef)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"name": name, "type": input.CredentialType, "data": data}
	if input.ProjectID != "" {
		body["projectId"] = input.ProjectID
	}
	var item credentialJSON
	if err := client.change(ctx, op, http.MethodPost, "/credentials", nil, body, &item, maxResponseBytes); err != nil {
		return nil, credentialError(err)
	}
	return summarizeCredential(item), nil
}

func invokeCredentialsUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "update credential"
	input, err := readCredentialChangeArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if _, err := selectCredentials(resolved); err != nil {
		return nil, err
	}
	if !validTargetID(input.CredentialID) {
		return nil, invalidRequest("credential_id must be a usable n8n identifier")
	}
	if input.Name != nil {
		if err := validCredentialName(*input.Name); err != nil {
			return nil, err
		}
	}
	if err := checkCredentialData(input.Data); err != nil {
		return nil, err
	}
	if input.Name == nil && len(input.Data) == 0 && input.SecretRef == "" {
		return nil, invalidRequest("name, data, or secret_ref is required")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.verifyCredentialBinding(ctx, op, input.CredentialID); err != nil {
		return nil, credentialError(err)
	}
	body := map[string]any{}
	if input.Name != nil {
		body["name"] = *input.Name
	}
	if len(input.Data) > 0 || input.SecretRef != "" {
		data, err := credentialDataBody(ctx, resolved, secrets, input.Data, input.SecretRef)
		if err != nil {
			return nil, err
		}
		// No isPartialData: n8n replaces the whole stored data object.
		body["data"] = data
	}
	var item credentialJSON
	if err := client.change(ctx, op, http.MethodPatch, "/credentials/"+url.PathEscape(input.CredentialID), nil,
		body, &item, maxResponseBytes); err != nil {
		return nil, credentialError(err)
	}
	return summarizeCredential(item), nil
}

func invokeCredentialsTransfer(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "transfer credential"
	input, err := readCredentialChangeArguments(op, raw)
	if err != nil {
		return nil, err
	}
	bound, err := selectCredentials(resolved)
	if err != nil {
		return nil, err
	}
	if len(bound.projects) == 0 {
		return nil, invalidRequest("a credential can be moved only on a connection with a project allow-list")
	}
	if !validTargetID(input.CredentialID) {
		return nil, invalidRequest("credential_id must be a usable n8n identifier")
	}
	if err := selectCredentialProject(bound, input.DestinationProjectID, "destination_project_id"); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.verifyCredentialBinding(ctx, op, input.CredentialID); err != nil {
		return nil, credentialError(err)
	}
	if err := client.change(ctx, op, http.MethodPut, "/credentials/"+url.PathEscape(input.CredentialID)+"/transfer",
		nil, map[string]any{"destinationProjectId": input.DestinationProjectID}, nil, maxResponseBytes); err != nil {
		return nil, credentialError(err)
	}
	return &CredentialTransferred{ID: input.CredentialID, DestinationProjectID: input.DestinationProjectID,
		Transferred: true}, nil
}

func invokeCredentialsDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "delete credential"
	input, err := readCredentialChangeArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if _, err := selectCredentials(resolved); err != nil {
		return nil, err
	}
	if !validTargetID(input.CredentialID) {
		return nil, invalidRequest("credential_id must be a usable n8n identifier")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.verifyCredentialBinding(ctx, op, input.CredentialID); err != nil {
		return nil, credentialError(err)
	}
	if err := client.change(ctx, op, http.MethodDelete, "/credentials/"+url.PathEscape(input.CredentialID), nil, nil,
		nil, maxResponseBytes); err != nil {
		return nil, credentialError(err)
	}
	return &CredentialDeleted{ID: input.CredentialID, Deleted: true}, nil
}
