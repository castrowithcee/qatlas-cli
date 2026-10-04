package n8n

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"unicode"
	"unicode/utf8"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Credentials are exposed as metadata only: id, name, type, and times. The response structs below have no
// field for a credential's data, so a stored value can never reach a result or an error, whatever n8n sends.
const (
	// maxCredentialLookupPages bounds the pages get and test read to find a credential's project binding,
	// because the Public API has no project information on GET /credentials/{id}.
	maxCredentialLookupPages = 20
	// maxCredentialTypeLength bounds a credential type name argument.
	maxCredentialTypeLength = 100
	// maxCredentialSchemaFields bounds the fields one schema view reports.
	maxCredentialSchemaFields = 200
	// maxCredentialTextLength bounds a name, a test message, or a schema field name, in characters.
	maxCredentialTextLength = 512
)

// credentialDataSensitivity marks credentials: metadata only, never stored values.
const credentialDataSensitivity = "n8n-credentials-metadata"

// credentialPermissionMessage is the one message of a 403 on a credentials endpoint. The body is never
// read into a message.
const credentialPermissionMessage = "n8n refused this credentials operation: license or role missing (the " +
	"API key's credential scope, its owner's role, or the instance's plan); Qatlas cannot tell which"

var credentialReadRisk = capability.Risk{Effect: capability.EffectRead, Idempotency: capability.IdempotencySafe,
	Confirmation: capability.ConfirmationNone, OpenWorld: true, DataSensitivity: credentialDataSensitivity}

// credentialTestRisk: the test call makes n8n use the stored secret against a third-party service.
var credentialTestRisk = capability.Risk{Effect: capability.EffectExecute, Idempotency: capability.IdempotencyIdempotent,
	Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: credentialDataSensitivity}

const credentialTypeSchema = `{"type":"string","minLength":1,"maxLength":100,"pattern":"^[A-Za-z0-9][A-Za-z0-9._-]*$"}`

var credentialItemSchema = `{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
	`"type":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"}},` +
	`"required":["id","name","type"],"additionalProperties":false}`

var credentialIDArgument = capability.Argument{Name: "credential_id",
	Description: "n8n credential identifier; with a project allow-list the credential must belong to one of its projects",
	Required:    true}

var credentialFields = []capability.Field{
	{Name: "id", Description: "Credential identifier"},
	{Name: "name", Description: "Credential name, untrusted data"},
	{Name: "type", Description: "Credential type name, for example httpBasicAuth"},
	{Name: "created_at", Description: "Creation time, as n8n reports it"},
	{Name: "updated_at", Description: "Last update time, as n8n reports it"},
}

const credentialScopeNote = "Metadata only, never a stored value. With a project allow-list only credentials shared " +
	"into those projects are reachable. Refused on a connection that restricts workflows by an allow-list"

var credentialsList = capability.Descriptor{
	ID: Provider + ".credentials.list", Version: 1, Title: "List n8n credentials",
	Description: "List the credentials (id, name, type, times) of the bound n8n instance, page by page with an " +
		"opaque cursor. " + credentialScopeNote,
	Tags: []string{"n8n", "credentials", "list", "automation"}, Risk: credentialReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"cursor":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxCursorLength) + `},` +
		`"limit":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxListLimit) + `}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"credentials":{"type":"array","items":` + credentialItemSchema + `},` +
		`"cursor":{"type":"string"},"has_more":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["credentials","has_more","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "cursor", Description: "Opaque cursor from a previous call's has_more page"},
		{Name: "limit", Description: "Credentials per page, 1 to 250; 100 when omitted"},
	},
	Fields: []capability.Field{
		{Name: "credentials", Description: "Credentials on this page after the allow-list was applied; id, name " +
			"(untrusted data), type, created_at, updated_at"},
		{Name: "cursor", Description: "Cursor for the next page; absent when has_more is false"},
		{Name: "has_more", Description: "True when a further page remains, even when this page is empty after filtering"},
		{Name: "count", Description: "Number of credentials on this page"},
	},
	Examples: []capability.Example{{Description: "List the first page of reachable credentials", Arguments: json.RawMessage(`{}`)}},
}

var credentialsGet = capability.Descriptor{
	ID: Provider + ".credentials.get", Version: 1, Title: "Read n8n credential metadata",
	Description: "Read the metadata (id, name, type, times) of one credential by ID. " + credentialScopeNote,
	Tags:        []string{"n8n", "credentials", "get", "automation"}, Risk: credentialReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"credential_id":` + targetIDSchema + `},` +
		`"required":["credential_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(credentialItemSchema),
	Arguments:    []capability.Argument{credentialIDArgument},
	Fields:       credentialFields,
	Examples: []capability.Example{{Description: "Read a credential's metadata",
		Arguments: json.RawMessage(`{"credential_id":"R2DjclaysHbqn778"}`)}},
}

var credentialsTest = capability.Descriptor{
	ID: Provider + ".credentials.test", Version: 1, Title: "Test an n8n credential",
	Description: "Ask n8n to test one credential's connection: n8n uses the stored secret against the " +
		"third-party service and answers with a status and a message (untrusted data). The request is sent " +
		"once and never repeated after an unclear result. " + credentialScopeNote,
	Tags: []string{"n8n", "credentials", "test", "automation"}, Risk: credentialTestRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"credential_id":` + targetIDSchema + `},` +
		`"required":["credential_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},` +
		`"status":{"type":"string"},"message":{"type":"string"}},` +
		`"required":["id","status"],"additionalProperties":false}`),
	Arguments: []capability.Argument{credentialIDArgument},
	Fields: []capability.Field{
		{Name: "id", Description: "Identifier of the tested credential"},
		{Name: "status", Description: "Test status as n8n reports it, usually OK or Error"},
		{Name: "message", Description: "Test message as n8n reports it, capped, untrusted data"},
	},
	Examples: []capability.Example{{Description: "Test a credential",
		Arguments: json.RawMessage(`{"credential_id":"R2DjclaysHbqn778"}`)}},
}

var credentialsSchema = capability.Descriptor{
	ID: Provider + ".credentials.schema", Version: 1, Title: "Read an n8n credential type schema",
	Description: "Read the field schema of one credential type by its type name: field names, types, and which " +
		"are required, in a bounded view. It holds no credential and is not tied to a project. Refused on a " +
		"connection that restricts workflows by an allow-list",
	Tags: []string{"n8n", "credentials", "schema", "automation"}, Risk: credentialReadRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"credential_type":` + credentialTypeSchema + `},` +
		`"required":["credential_type"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"credential_type":{"type":"string"},` +
		`"fields":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},` +
		`"type":{"type":"string"},"required":{"type":"boolean"}},"required":["name","required"],` +
		`"additionalProperties":false}},"truncated":{"type":"boolean"},"count":{"type":"integer"}},` +
		`"required":["credential_type","fields","count"],"additionalProperties":false}`),
	Arguments: []capability.Argument{{Name: "credential_type", Required: true,
		Description: "Credential type name, letters, digits, '.', '_' and '-', at most 100 characters"}},
	Fields: []capability.Field{
		{Name: "credential_type", Description: "The type name that was read"},
		{Name: "fields", Description: "Fields of the type, sorted by name, at most 200; name (untrusted data), " +
			"type, required"},
		{Name: "truncated", Description: "True when the schema had more fields than reported"},
		{Name: "count", Description: "Number of fields reported"},
	},
	Examples: []capability.Example{{Description: "Read a credential type's schema",
		Arguments: json.RawMessage(`{"credential_type":"httpBasicAuth"}`)}},
}

// credentialShareJSON is one entry of a listed credential's shared array. n8n reports the owning or
// sharing project as the entry's id (with its name and role); no other field names a project.
type credentialShareJSON struct {
	ID   string `json:"id"`
	Role string `json:"role"`
}

// ownerRole marks the owning project's entry of a shared array.
const ownerRole = "credential:owner"

func (s credentialShareJSON) projectID() string {
	return s.ID
}

// credentialJSON deliberately has no data field.
type credentialJSON struct {
	ID        string                `json:"id"`
	Name      string                `json:"name"`
	Type      string                `json:"type"`
	CreatedAt string                `json:"createdAt"`
	UpdatedAt string                `json:"updatedAt"`
	Shared    []credentialShareJSON `json:"shared"`
}

// CredentialSummary is the stable Qatlas view of one credential's metadata. Name is untrusted data.
type CredentialSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// CredentialsPage is one paginated, allow-list-filtered listing of credentials.
type CredentialsPage struct {
	Credentials []CredentialSummary `json:"credentials"`
	Cursor      string              `json:"cursor,omitempty"`
	HasMore     bool                `json:"has_more"`
	Count       int                 `json:"count"`
}

// CredentialTested is what credentials.test reports.
type CredentialTested struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// CredentialSchemaField is one field of a credential type.
type CredentialSchemaField struct {
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"`
	Required bool   `json:"required"`
}

// CredentialSchema is the bounded view of a credential type's schema.
type CredentialSchema struct {
	CredentialType string                  `json:"credential_type"`
	Fields         []CredentialSchemaField `json:"fields"`
	Truncated      bool                    `json:"truncated,omitempty"`
	Count          int                     `json:"count"`
}

func credentialError(err error) error {
	if failure, ok := err.(*provider.Error); ok && failure.Class == provider.ClassPermission {
		failure.Message = credentialPermissionMessage
	}
	return err
}

// selectCredentials decides locally, before any secret or request, whether this connection may use
// credentials at all. A workflow allow-list cannot bind a credential, so it refuses every credential tool.
func selectCredentials(resolved *config.Resolved) (scope, error) {
	bound, err := boundScope(resolved)
	if err != nil {
		return scope{}, err
	}
	if len(bound.workflows) > 0 {
		return scope{}, invalidRequest("this connection restricts workflows by an allow-list, so it cannot use credentials")
	}
	return bound, nil
}

// ownsCredential: without a project allow-list every credential; with one only a credential whose owning
// project (entry with the owner role) is in it. A missing role or owner entry never matches.
func (s scope) ownsCredential(shared []credentialShareJSON) bool {
	if len(s.projects) == 0 {
		return true
	}
	for _, entry := range shared {
		if entry.Role == ownerRole && entry.ID != "" && contains(s.projects, entry.ID) {
			return true
		}
	}
	return false
}

// allowsCredential: without a project allow-list every credential; with one only a credential shared into
// at least one of its projects. A credential that reports no project is outside the scope.
func (s scope) allowsCredential(shared []credentialShareJSON) bool {
	if len(s.projects) == 0 {
		return true
	}
	for _, entry := range shared {
		if id := entry.projectID(); id != "" && contains(s.projects, id) {
			return true
		}
	}
	return false
}

// cappedText bounds a provider text by characters and replaces control characters.
func cappedText(value string) string {
	if !utf8.ValidString(value) {
		value = string([]rune(value))
	}
	out := make([]rune, 0, len(value))
	for _, r := range value {
		if len(out) >= maxCredentialTextLength {
			break
		}
		if unicode.IsControl(r) {
			r = ' '
		}
		out = append(out, r)
	}
	return string(out)
}

func summarizeCredential(c credentialJSON) CredentialSummary {
	return CredentialSummary{ID: bounded(c.ID), Name: cappedText(c.Name), Type: bounded(c.Type),
		CreatedAt: bounded(c.CreatedAt), UpdatedAt: bounded(c.UpdatedAt)}
}

// validCredentialType keeps a type name to the characters n8n's own type names use.
func validCredentialType(name string) error {
	if name == "" || len(name) > maxCredentialTypeLength {
		return invalidRequest("credential_type must be 1 to " + strconv.Itoa(maxCredentialTypeLength) + " characters")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case (c == '.' || c == '-' || c == '_') && i > 0:
		default:
			return invalidRequest("credential_type must be letters, digits, '.', '-' and '_', starting with a letter or digit")
		}
	}
	return nil
}

type credentialArguments struct {
	CredentialID   string `json:"credential_id"`
	CredentialType string `json:"credential_type"`
	Cursor         string `json:"cursor"`
	Limit          int    `json:"limit"`
}

func readCredentialArguments(op string, raw json.RawMessage) (credentialArguments, error) {
	var input credentialArguments
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, providerError(op, "the validated arguments could not be read")
	}
	return input, nil
}

type credentialsPageJSON struct {
	Data       []credentialJSON `json:"data"`
	NextCursor *string          `json:"nextCursor"`
}

func (c *Client) credentialsPage(ctx context.Context, op, cursor string, limit int) (*credentialsPageJSON, error) {
	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	var page credentialsPageJSON
	if err := c.get(ctx, op, "/credentials", query, &page, maxResponseBytes); err != nil {
		return nil, err
	}
	if page.NextCursor != nil && len(*page.NextCursor) > maxCursorLength {
		return nil, invalidResponse(op, "n8n reported an oversized page cursor")
	}
	return &page, nil
}

// ListCredentials reads one page of GET /credentials and keeps only the credentials this connection's
// project binding allows.
func (c *Client) ListCredentials(ctx context.Context, cursor string, limit int) (*CredentialsPage, error) {
	page, err := c.credentialsPage(ctx, "list credentials", cursor, limit)
	if err != nil {
		return nil, err
	}
	items := make([]CredentialSummary, 0, len(page.Data))
	for _, item := range page.Data {
		if c.scope.allowsCredential(item.Shared) {
			items = append(items, summarizeCredential(item))
		}
	}
	next := ""
	if page.NextCursor != nil {
		next = *page.NextCursor
	}
	return &CredentialsPage{Credentials: items, Cursor: next, HasMore: next != "", Count: len(items)}, nil
}

// verifyCredentialBinding finds the credential through the bounded paged list and checks it against the
// project allow-list; with owned set (changes) the owning project must be in it, otherwise any shared allowed
// project suffices. Without an allow-list it sends nothing. A credential that is absent, outside the
// binding, or whose search could not finish is refused alike, without naming any project.
func (c *Client) verifyCredentialBinding(ctx context.Context, op, id string, owned bool) error {
	if len(c.scope.projects) == 0 {
		return nil
	}
	cursor := ""
	for i := 0; i < maxCredentialLookupPages; i++ {
		page, err := c.credentialsPage(ctx, op, cursor, maxListLimit)
		if err != nil {
			return err
		}
		for _, item := range page.Data {
			if item.ID != id {
				continue
			}
			allowed := c.scope.allowsCredential(item.Shared)
			if owned {
				allowed = c.scope.ownsCredential(item.Shared)
			}
			if !allowed {
				return invalidRequest("the credential is outside the targets of this connection")
			}
			return nil
		}
		if page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		cursor = *page.NextCursor
	}
	return invalidRequest("the credential was not found among the credentials this connection can reach")
}

func invokeCredentialsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	input, err := readCredentialArguments("list credentials", raw)
	if err != nil {
		return nil, err
	}
	if _, err := selectCredentials(resolved); err != nil {
		return nil, err
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultListLimit
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	page, err := client.ListCredentials(ctx, input.Cursor, limit)
	return page, credentialError(err)
}

func invokeCredentialsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read credential"
	input, err := readCredentialArguments(op, raw)
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
	if err := client.verifyCredentialBinding(ctx, op, input.CredentialID, false); err != nil {
		return nil, credentialError(err)
	}
	var item credentialJSON
	if err := client.get(ctx, op, "/credentials/"+url.PathEscape(input.CredentialID), nil, &item,
		maxResponseBytes); err != nil {
		return nil, credentialError(err)
	}
	return summarizeCredential(item), nil
}

func invokeCredentialsTest(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "test credential"
	input, err := readCredentialArguments(op, raw)
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
	if err := client.verifyCredentialBinding(ctx, op, input.CredentialID, false); err != nil {
		return nil, credentialError(err)
	}
	var answer struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := client.change(ctx, op, http.MethodPost, "/credentials/"+url.PathEscape(input.CredentialID)+"/test",
		nil, nil, &answer, maxResponseBytes); err != nil {
		return nil, credentialError(err)
	}
	return &CredentialTested{ID: input.CredentialID, Status: cappedText(answer.Status),
		Message: cappedText(answer.Message)}, nil
}

func invokeCredentialsSchema(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read credential schema"
	input, err := readCredentialArguments(op, raw)
	if err != nil {
		return nil, err
	}
	if _, err := selectCredentials(resolved); err != nil {
		return nil, err
	}
	if err := validCredentialType(input.CredentialType); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var schema struct {
		Properties map[string]struct {
			Type any `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := client.get(ctx, op, "/credentials/schema/"+url.PathEscape(input.CredentialType), nil, &schema,
		maxResponseBytes); err != nil {
		return nil, credentialError(err)
	}
	required := map[string]bool{}
	for _, name := range schema.Required {
		required[name] = true
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	out := &CredentialSchema{CredentialType: input.CredentialType, Fields: []CredentialSchemaField{}}
	for _, name := range names {
		if len(out.Fields) >= maxCredentialSchemaFields {
			out.Truncated = true
			break
		}
		kind, _ := schema.Properties[name].Type.(string)
		out.Fields = append(out.Fields, CredentialSchemaField{Name: cappedText(name), Type: cappedText(kind),
			Required: required[name]})
	}
	out.Count = len(out.Fields)
	return out, nil
}
