// Package twentycrm implements controlled company access to one Twenty workspace.
//
// Twenty generates its REST and GraphQL APIs from the schema of each workspace, so there is no global
// static field reference. This provider therefore talks to the generated REST core API directly (and to
// fixed GraphQL documents where only that API offers a feature), reads
// only the conservative core fields every workspace carries, and verifies during the connection test that
// the workspace schema still offers them. Company names and domains arrive from the provider and are
// treated as untrusted data: they are normalised into a stable Qatlas shape, passed through the output
// encoders, and never rendered or stored.
package twentycrm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/provider/ratelimit"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// Provider is the provider name used in the configuration and as the operation namespace.
const Provider = "twentycrm"

// cloudOrigin is the managed Twenty Cloud origin and the default a new service starts from. A self-hosted
// workspace configures its own HTTPS origin instead; the agent never supplies one.
const cloudOrigin = "https://api.twenty.com"

// roleAPIKey is the single secret role a Twenty credential must supply. It is used as a bearer token.
const roleAPIKey = "api-key"

// dataSensitivity classifies results as CRM records of the configured Twenty workspace. It is deliberately
// provider-specific; the architecture defines no global sensitivity taxonomy.
const dataSensitivity = "twentycrm-company-data"

// The generated REST core routes this provider uses, and the workspace API document it validates against.
const (
	companiesPath = "/rest/companies"
	schemaPath    = "/open-api/core"
)

// The Twenty record fields the stable projection depends on. A workspace that no longer offers all of them
// fails the connection test instead of answering a business call with an incomplete record.
var requiredCompanyFields = []string{"id", "name", "domainName", "createdAt", "updatedAt"}

// noRelations keeps every read at the record itself. Twenty returns related records at depth 1, which
// would silently widen a read beyond the companies this provider is allowed to report.
const noRelations = "0"

// Bounds of one request. The page size stays well below the documented maximum of 200, and the response
// limits bound a page of records and the generated workspace document without inviting an unbounded read.
const (
	defaultPageSize  = 25
	maxPageSize      = 100
	maxCursorLength  = 1024
	maxResponseBytes = 1 << 20
	maxSchemaBytes   = 8 << 20
	defaultTimeout   = 30 * time.Second
)

// minInterval spaces requests that share one API key. Twenty documents 100 requests per minute, which is
// one request per 600 milliseconds, and counts them per key across the whole workspace API.
const minInterval = 600 * time.Millisecond

// limiters holds the rate-limit budget of every API key this process has used.
var limiters = ratelimit.NewRegistry(minInterval)

var twentyReadRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: dataSensitivity,
}

// searchPattern bounds a search term to characters that cannot leave the value of a Twenty filter
// expression. Quote, backslash, bracket, colon, and comma are separators of that grammar and stay out.
// The backslashes are doubled because the pattern is embedded in a JSON schema string.
const searchPattern = `^[\\p{L}\\p{N} ._'+@&-]+$`

var companiesList = capability.Descriptor{
	ID:      Provider + ".companies.list",
	Version: 1,
	Title:   "List Twenty CRM companies",
	Description: "Search one bounded page of companies in the Twenty workspace of a connection, " +
		"by name or by primary domain; deleted true lists only the companies in the trash instead",
	Tags:     []string{"twentycrm", "crm", "companies", "list", "search"},
	Risk:     twentyReadRisk,
	Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"name_contains":{"type":"string","minLength":1,"maxLength":64,"pattern":"` + searchPattern + `"},` +
		`"domain_contains":{"type":"string","minLength":1,"maxLength":253,"pattern":"^[A-Za-z0-9.-]+$"},` +
		`"deleted":{"type":"boolean"},` +
		`"limit":{"type":"integer","minimum":1,"maximum":100},` +
		`"sort":{"type":"string","enum":["name","created_at","updated_at"]},` +
		`"direction":{"type":"string","enum":["asc","desc"]},` +
		`"cursor":{"type":"string","minLength":1,"maxLength":1024,"pattern":"^[A-Za-z0-9+/=_.:-]+$"}},` +
		`"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"companies":{"type":"array","items":{"type":"object","properties":{` +
		`"id":{"type":"string"},"name":{"type":"string"},"domain":{"type":"string"}},` +
		`"required":["id","name"],"additionalProperties":false}},` +
		`"next_cursor":{"type":"string"},"has_more":{"type":"boolean"}},` +
		`"required":["companies","has_more"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "name_contains", Description: "Return only companies whose name contains this text"},
		{Name: "domain_contains", Description: "Return only companies whose primary domain contains this text"},
		{Name: "deleted", Description: "True lists only deleted companies of the trash, which companies.restore can bring back; false when omitted"},
		{Name: "limit", Description: "Companies per page, from 1 through 100; 25 when omitted"},
		{Name: "sort", Description: "Sort property: name, created_at, or updated_at; created_at when omitted"},
		{Name: "direction", Description: "Sort direction asc or desc; desc when omitted"},
		{Name: "cursor", Description: "Opaque next_cursor of a previous page; the first page when omitted"},
	},
	Fields: []capability.Field{
		{Name: "companies", Description: "The companies on this page, untrusted data"},
		{Name: "next_cursor", Description: "Cursor of the following page, absent on the last page"},
		{Name: "has_more", Description: "True when the workspace holds a following page"},
	},
	Examples: []capability.Example{{
		Description: "Search the newest companies whose name contains a term",
		Arguments:   json.RawMessage(`{"name_contains":"Bike","limit":25,"sort":"created_at","direction":"desc"}`),
	}},
}

var companiesGet = capability.Descriptor{
	ID:          Provider + ".companies.get",
	Version:     1,
	Title:       "Get a Twenty CRM company",
	Description: "Read one company of the Twenty workspace of a connection by its record identifier",
	Tags:        []string{"twentycrm", "crm", "companies", "get"},
	Risk:        twentyReadRisk,
	Provider:    Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string","minLength":36,` +
		`"maxLength":36,"pattern":"^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$"}},` +
		`"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"id":{"type":"string"},"name":{"type":"string"},"domain":{"type":"string"},` +
		`"created_at":{"type":"string"},"updated_at":{"type":"string"}},` +
		`"required":["id","name"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "id", Description: "Company record identifier as a UUID, as returned by twentycrm.companies.list", Required: true},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Company record identifier"},
		{Name: "name", Description: "Company name, untrusted data"},
		{Name: "domain", Description: "Primary domain of the company, untrusted data"},
		{Name: "created_at", Description: "Creation timestamp of the record"},
		{Name: "updated_at", Description: "Last change timestamp of the record"},
	},
	Examples: []capability.Example{{
		Description: "Read one company by the identifier a list result reported",
		Arguments:   json.RawMessage(`{"id":"11111111-2222-3333-4444-555555555555"}`),
	}},
}

var companiesCreate = companyMutationDescriptor("create", capability.EffectCreate, capability.IdempotencyNonIdempotent,
	`{"type":"object","properties":{"name":{"type":"string","minLength":1,"maxLength":255},"domain":{"type":"string","maxLength":253,"pattern":"^[A-Za-z0-9.-]*$"}},"required":["name"],"additionalProperties":false}`,
	json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},"domain":{"type":"string"},"created_at":{"type":"string"},"updated_at":{"type":"string"}},"required":["id","name"],"additionalProperties":false}`),
	withRequired(companyNameArgument), companyDomainArgument)
var companiesUpdate = companyMutationDescriptor("update", capability.EffectUpdate, capability.IdempotencyIdempotent,
	`{"type":"object","properties":{"id":{"type":"string","minLength":36,"maxLength":36,"pattern":"^[0-9a-fA-F-]{36}$"},"name":{"type":"string","minLength":1,"maxLength":255},"domain":{"type":"string","maxLength":253,"pattern":"^[A-Za-z0-9.-]*$"}},"required":["id"],"additionalProperties":false}`,
	companiesCreate.OutputSchema, companyIDArgument, companyNameArgument, companyDomainArgument)

const companyIDOnlyInput = `{"type":"object","properties":{"id":{"type":"string","minLength":36,"maxLength":36,"pattern":"^[0-9a-fA-F-]{36}$"}},"required":["id"],"additionalProperties":false}`

var companiesDelete = companyDeleteDescriptor("delete", 2, "Move one company of the workspace of a connection to the Twenty trash. "+
	"The company stays recoverable with twentycrm.companies.restore and is found with twentycrm.companies.list deleted true. "+
	"A connection offers this tool only when its tools list names it")
var companiesDestroy = companyDeleteDescriptor("destroy", 1, "Permanently delete one company of the workspace of a connection. "+
	"This cannot be undone: the company is not moved to the trash and twentycrm.companies.restore cannot bring it back. "+
	"A connection offers this tool only when its tools list names it")
var companiesRestore = withDescription(companyMutationDescriptor("restore", capability.EffectUpdate, capability.IdempotencyIdempotent,
	companyIDOnlyInput, companiesCreate.OutputSchema, companyIDArgument),
	"Restore one company of the Twenty trash in the workspace of a connection, as found with twentycrm.companies.list deleted true")

func withDescription(d capability.Descriptor, description string) capability.Descriptor {
	d.Description = description
	return d
}

func companyDeleteDescriptor(action string, version int, description string) capability.Descriptor {
	d := companyMutationDescriptor(action, capability.EffectDelete, capability.IdempotencyIdempotent, companyIDOnlyInput,
		json.RawMessage(`{"type":"object","properties":{"deleted":{"type":"boolean"}},"required":["deleted"],"additionalProperties":false}`),
		companyIDArgument)
	d.Version, d.Description, d.RequiresToolAllowList = version, description, true
	return d
}

var (
	companyIDArgument     = capability.Argument{Name: "id", Description: "Company identifier of 36 characters, as returned by twentycrm.companies.list", Required: true}
	companyNameArgument   = capability.Argument{Name: "name", Description: "Name of the company, 1 to 255 characters"}
	companyDomainArgument = capability.Argument{Name: "domain", Description: "Primary domain of the company, up to 253 characters"}
)

func withRequired(a capability.Argument) capability.Argument {
	a.Required = true
	return a
}

func companyMutationDescriptor(action string, effect capability.Effect, idempotency capability.Idempotency, input string, output json.RawMessage, arguments ...capability.Argument) capability.Descriptor {
	return capability.Descriptor{ID: Provider + ".companies." + action, Version: 1,
		Title:       strings.ToUpper(action[:1]) + action[1:] + " a Twenty CRM company",
		Description: strings.ToUpper(action[:1]) + action[1:] + " one company in the workspace of a connection",
		Tags:        []string{"twentycrm", "crm", "companies", action}, Provider: Provider,
		Risk:        capability.Risk{Effect: effect, Idempotency: idempotency, Confirmation: capability.ConfirmationRequired, OpenWorld: true, DataSensitivity: dataSensitivity},
		InputSchema: json.RawMessage(input), OutputSchema: output, Arguments: arguments}
}

// readTools are the tools of the read profile; the write profile adds the record writes to them.
var readTools = []string{companiesList.ID, companiesGet.ID, objectsList.ID, objectsGet.ID,
	recordsList.ID, recordsGet.ID, recordsSearch.ID, recordsSearchAll.ID, recordsGroupBy.ID, activitytargetsList.ID,
	workflowsList.ID, workflowsGet.ID, workflowRunsList.ID}

// Register adds Twenty metadata, its read-only connection test, and the bounded company operations.
func Register(reg *capability.Registry) error {
	if err := reg.RegisterProvider(config.ProviderMetadata{
		ID: Provider, Name: "Twenty CRM", DefaultBaseURL: cloudOrigin,
		Description:        "Open-source customer relationship management (CRM) system",
		DefaultPermissions: []config.Permission{config.PermissionRead},
		SecretRoles: []config.SecretRole{{
			Name: roleAPIKey,
			Description: "Twenty API key: the value shown once when you create a key under API & Webhooks; " +
				"its workspace role must grant only the objects, fields, and operations this connection needs",
		}},
		Target: config.TargetMetadata{
			Label:    "object",
			Multiple: true,
			Description: "optional objects as an allow-list; without targets the connection reaches every " +
				"non-system object its API key reaches",
			Kinds: []config.TargetKind{{
				Name:        "object",
				Description: "an object of the workspace whose records this connection may reach",
				Forms:       []string{"object/NAME"},
			}},
			Validate:    validateTarget,
			ValidateSet: validateSet,
		},
		Profiles: []config.ToolProfile{{
			ID: "read", Title: "Read companies, objects, and records", Recommended: true,
			Description: "lists and reads companies, the objects of the workspace, their records, also by structured conditions and counted by field, and the workflows and runs of the workspace; changes nothing in Twenty CRM",
			Tools:       readTools,
		}, {
			ID: "write", Title: "Read and write records",
			Description: "reads like the read profile and creates records and changes fields of records of the " +
				"reachable objects; deletes nothing and does not change companies through the company tools",
			Tools: append(append([]string{}, readTools...), recordsCreate.ID, recordsUpdate.ID, activitytargetsCreate.ID),
		}},
		Groups: toolGroups,
	}, TestConnection); err != nil {
		return err
	}
	return reg.Register(Provider,
		capability.Operation{Descriptor: inGroup(companiesList, companiesGroup), Handler: capability.Handler(invokeCompaniesList)},
		capability.Operation{Descriptor: inGroup(companiesGet, companiesGroup), Handler: capability.Handler(invokeCompaniesGet)},
		capability.Operation{Descriptor: inGroup(companiesCreate, companiesGroup), Handler: capability.Handler(invokeCompaniesCreate)},
		capability.Operation{Descriptor: inGroup(companiesUpdate, companiesGroup), Handler: capability.Handler(invokeCompaniesUpdate)},
		capability.Operation{Descriptor: inGroup(companiesDelete, companiesGroup), Handler: capability.Handler(invokeCompaniesDelete)},
		capability.Operation{Descriptor: inGroup(companiesDestroy, companiesGroup), Handler: capability.Handler(invokeCompaniesDestroy)},
		capability.Operation{Descriptor: inGroup(companiesRestore, companiesGroup), Handler: capability.Handler(invokeCompaniesRestore)},
		capability.Operation{Descriptor: inGroup(objectsList, objectsGroup), Handler: capability.Handler(invokeObjectsList)},
		capability.Operation{Descriptor: inGroup(objectsGet, objectsGroup), Handler: capability.Handler(invokeObjectsGet)},
		capability.Operation{Descriptor: inGroup(recordsList, recordsGroup), Handler: capability.Handler(invokeRecordsList)},
		capability.Operation{Descriptor: inGroup(recordsGet, recordsGroup), Handler: capability.Handler(invokeRecordsGet)},
		capability.Operation{Descriptor: inGroup(recordsGroupBy, recordsGroup), Handler: capability.Handler(invokeRecordsGroupBy)},
		capability.Operation{Descriptor: inGroup(recordsSearch, recordsGroup), Handler: capability.Handler(invokeRecordsSearch)},
		capability.Operation{Descriptor: inGroup(recordsSearchAll, recordsGroup), Handler: capability.Handler(invokeRecordsSearchAll)},
		capability.Operation{Descriptor: inGroup(recordsCreate, recordsGroup), Handler: capability.Handler(invokeRecordsCreate)},
		capability.Operation{Descriptor: inGroup(recordsUpdate, recordsGroup), Handler: capability.Handler(invokeRecordsUpdate)},
		capability.Operation{Descriptor: inGroup(activitytargetsList, activityGroup), Handler: capability.Handler(invokeActivityTargetsList)},
		capability.Operation{Descriptor: inGroup(activitytargetsCreate, activityGroup), Handler: capability.Handler(invokeActivityTargetsCreate)},
		capability.Operation{Descriptor: inGroup(activitytargetsDelete, activityGroup), Handler: capability.Handler(invokeActivityTargetsDelete)},
		capability.Operation{Descriptor: inGroup(workflowsList, workflowsGroup), Handler: capability.Handler(invokeWorkflowsList)},
		capability.Operation{Descriptor: inGroup(workflowsGet, workflowsGroup), Handler: capability.Handler(invokeWorkflowsGet)},
		capability.Operation{Descriptor: inGroup(workflowRunsList, workflowsGroup), Handler: capability.Handler(invokeWorkflowRunsList)},
		capability.Operation{Descriptor: inGroup(recordsDelete, recordsGroup), Handler: capability.Handler(invokeRecordsDelete)},
		capability.Operation{Descriptor: inGroup(recordsRestore, recordsGroup), Handler: capability.Handler(invokeRecordsRestore)},
		capability.Operation{Descriptor: inGroup(recordsDestroy, recordsGroup), Handler: capability.Handler(invokeRecordsDestroy)},
		capability.Operation{Descriptor: inGroup(recordsBatchCreate, recordsGroup), Handler: capability.Handler(invokeRecordsBatchCreate)},
		capability.Operation{Descriptor: inGroup(recordsBatchUpdate, recordsGroup), Handler: capability.Handler(invokeRecordsBatchUpdate)},
		capability.Operation{Descriptor: inGroup(recordsBatchDelete, recordsGroup), Handler: capability.Handler(invokeRecordsBatchDelete)},
		capability.Operation{Descriptor: inGroup(recordsDuplicates, recordsGroup), Handler: capability.Handler(invokeRecordsDuplicates)},
		capability.Operation{Descriptor: inGroup(recordsMergePreview, recordsGroup), Handler: capability.Handler(invokeRecordsMergePreview)},
		capability.Operation{Descriptor: inGroup(recordsMerge, recordsGroup), Handler: capability.Handler(invokeRecordsMerge)},
	)
}

func invokeCompaniesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := selectObject(resolved, companyObject); err != nil {
		return nil, err
	}
	var options ListOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, providerError("list companies", "the validated arguments could not be read")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.ListCompanies(ctx, options)
}

func invokeCompaniesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := selectObject(resolved, companyObject); err != nil {
		return nil, err
	}
	var arguments struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, providerError("get company", "the validated arguments could not be read")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.GetCompany(ctx, arguments.ID)
}

type companyMutationInput struct {
	ID     string  `json:"id"`
	Name   *string `json:"name"`
	Domain *string `json:"domain"`
}

func invokeCompaniesCreate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := selectObject(resolved, companyObject); err != nil {
		return nil, err
	}
	var input companyMutationInput
	if json.Unmarshal(raw, &input) != nil || input.Name == nil {
		return nil, providerError("create company", "the validated arguments could not be read")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.CreateCompany(ctx, *input.Name, input.Domain)
}

func invokeCompaniesUpdate(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := selectObject(resolved, companyObject); err != nil {
		return nil, err
	}
	var input companyMutationInput
	if json.Unmarshal(raw, &input) != nil || (input.Name == nil && input.Domain == nil) {
		return nil, providerError("update company", "name or domain is required")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.UpdateCompany(ctx, input.ID, input.Name, input.Domain)
}

func invokeCompaniesDelete(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeRemoval(ctx, resolved, secrets, red, raw, "delete company", true)
}

func invokeCompaniesDestroy(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	return invokeRemoval(ctx, resolved, secrets, red, raw, "destroy company", false)
}

func invokeRemoval(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage, op string, soft bool) (any, error) {
	if err := selectObject(resolved, companyObject); err != nil {
		return nil, err
	}
	var input companyMutationInput
	if json.Unmarshal(raw, &input) != nil {
		return nil, providerError(op, "the validated arguments could not be read")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if soft {
		err = client.DeleteCompany(ctx, input.ID)
	} else {
		err = client.DestroyCompany(ctx, input.ID)
	}
	if err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}

func invokeCompaniesRestore(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor, raw json.RawMessage) (any, error) {
	if err := selectObject(resolved, companyObject); err != nil {
		return nil, err
	}
	var input companyMutationInput
	if json.Unmarshal(raw, &input) != nil {
		return nil, providerError("restore company", "the validated arguments could not be read")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	return client.RestoreCompany(ctx, input.ID)
}

// mutationUncertain is appended to a failure of a change whose request may have reached Twenty. Qatlas
// sends a change once and never repeats it.
const mutationUncertain = "; this change may have taken effect, read the company before repeating it"

// Client binds one Twenty API key to the origin of one configured service and to the rate limit that key
// shares. Two workspaces are two connections with two keys, and neither can reach the other's origin.
type Client struct {
	origin  string
	auth    string
	http    *http.Client
	limiter *ratelimit.Limiter
	scope   scope
	catalog *catalog
}

// Open resolves the API key of one selected connection and returns a client for its configured origin.
func Open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor) (*Client, error) {
	return open(ctx, resolved, secrets, red, nil)
}

// open is the internal seam. A caller may supply the rate limiter, and the package's own tests replace
// the transport, so no test ever reaches a productive Twenty workspace.
func open(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	lim *ratelimit.Limiter) (*Client, error) {
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	origin, err := originOf(resolved.BaseURL)
	if err != nil {
		return nil, providerError("open", err.Error())
	}
	if secrets == nil {
		return nil, providerError("open", "no credential resolver was configured")
	}
	value, err := secrets.Resolve(ctx, resolved.Credential, resolved.Secrets, roleAPIKey)
	if err != nil {
		return nil, err
	}
	if !validAPIKey(value.Secret) {
		return nil, &provider.Error{Class: provider.ClassAuth, Op: "open", Message: "the Twenty API key is unusable"}
	}
	if red != nil {
		red.Add(value.Secret, "Bearer "+value.Secret)
	}
	if lim == nil {
		lim = limiters.For(value.Secret)
	}
	return &Client{origin: origin, auth: "Bearer " + value.Secret, http: provider.NoRedirectClient(defaultTimeout, transport), limiter: lim, scope: bound}, nil
}

// originOf validates the configured service origin and returns it without a trailing slash. Twenty Cloud
// and a self-hosted workspace are both just origins here: HTTPS, a host, and nothing else. Userinfo, a
// path, a query, or a fragment would either carry a credential or silently rewrite every request path.
func originOf(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", errors.New("a Twenty service needs a usable https origin")
	}
	if parsed.Scheme != "https" {
		return "", errors.New("a Twenty service must use https")
	}
	if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		strings.Trim(parsed.Path, "/") != "" || parsed.Opaque != "" {
		return "", errors.New("a Twenty service must be a bare https origin without user, path, query, or fragment")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

// transport carries every Twenty request. A nil value is Go's default transport; the package's own tests
// replace it with recorded responses.
var transport http.RoundTripper

// TestConnection verifies that the workspace behind one connection can serve this provider at all: its
// generated API document must still describe the company fields the stable projection reads, and the API
// key must be allowed to read companies. A workspace that fails either check fails here, before a business
// call could answer with an incomplete record.
func TestConnection(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor) (provider.Class, error) {
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		var providerErr *provider.Error
		if errors.As(err, &providerErr) {
			return providerErr.Class, nil
		}
		return "", err
	}
	return client.testConnection(ctx)
}

func (c *Client) testConnection(ctx context.Context) (provider.Class, error) {
	const op = "test connection"

	if !c.scope.bound() {
		// The smallest authenticated read comes first, because the workspace document is a public route
		// that answers an unusable key with an empty base schema instead of an authentication failure.
		if class, failed := classOfFailure(c.readOne(ctx, op, companiesPath)); failed {
			return class, nil
		}
	}

	cat, err := c.workspaceCatalog(ctx, op)
	if err != nil {
		class, _ := classOfFailure(err)
		return class, nil
	}
	if len(cat.objects) == 0 && c.scope.bound() {
		// An unusable key gets an empty base schema, and with it no way to tell a missing object from a
		// rejected key; the key is the likelier cause.
		return provider.ClassAuth, nil
	}
	if c.scope.bound() {
		for _, name := range c.scope.objects {
			object, ok := cat.object(name)
			if !ok {
				return "", &provider.Error{Class: provider.ClassInvalidResponse, Op: op,
					Message: "this Twenty workspace does not hold an object bound to this connection"}
			}
			if class, failed := classOfFailure(c.readOne(ctx, op, "/rest/"+url.PathEscape(object.Plural))); failed {
				return class, nil
			}
		}
	}
	if c.scope.allows(companyObject) {
		if err := checkCompany(cat, op); err != nil {
			// An incompatible workspace is reported with its own explanation, because no stable class can
			// say which part of the schema is missing.
			return "", err
		}
	}
	return provider.ClassOK, nil
}

// classOfFailure maps the error of a read to the class a connection test reports. failed is false for a nil
// error.
func classOfFailure(err error) (class provider.Class, failed bool) {
	if err == nil {
		return "", false
	}
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		return providerErr.Class, true
	}
	return provider.ClassProviderError, true
}

// readOne reads the smallest page of one object collection to prove the key may read it. The page is
// decoded and dropped.
func (c *Client) readOne(ctx context.Context, op, path string) error {
	query := url.Values{}
	query.Set("limit", "1")
	query.Set("depth", noRelations)
	var page json.RawMessage
	return c.get(ctx, op, path, query, maxResponseBytes, &page)
}

// checkCompany verifies that the workspace catalog offers the company routes and the record fields the
// stable company projection reads. The workspace document itself is never reported.
func checkCompany(cat *catalog, op string) error {
	object, ok := cat.object(companyObject)
	if !ok {
		return &provider.Error{
			Class: provider.ClassInvalidResponse, Op: op,
			Message: "this Twenty workspace does not describe a Company object",
		}
	}
	if "/rest/"+object.Plural != companiesPath {
		return &provider.Error{
			Class: provider.ClassInvalidResponse, Op: op,
			Message: "this Twenty workspace does not expose the company route /companies",
		}
	}
	fields := map[string]bool{}
	for _, field := range object.Fields {
		fields[field.Name] = true
	}
	missing := make([]string, 0, len(requiredCompanyFields))
	for _, field := range requiredCompanyFields {
		if !fields[field] {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		return &provider.Error{
			Class: provider.ClassInvalidResponse, Op: op,
			Message: "this Twenty workspace is missing the company fields " + strings.Join(missing, ", ") +
				", which Qatlas reads",
		}
	}
	return nil
}

// requireCompany refuses a company operation on a connection whose object targets leave out the company
// object. The refusal names no object.
func (c *Client) requireCompany() error {
	if !c.scope.allows(companyObject) {
		return invalidRequest(errObjectUnavailable)
	}
	return nil
}

// ListOptions are the controlled search, sort, and paging arguments of the list operation. Nothing else
// reaches the provider: the object and route are fixed.
type ListOptions struct {
	NameContains   string `json:"name_contains"`
	DomainContains string `json:"domain_contains"`
	Deleted        bool   `json:"deleted"`
	Limit          int    `json:"limit"`
	Sort           string `json:"sort"`
	Direction      string `json:"direction"`
	Cursor         string `json:"cursor"`
}

// sortFields maps the stable Qatlas sort names to the Twenty record fields. An unknown name never
// reaches the provider.
var sortFields = map[string]string{
	"name":       "name",
	"created_at": "createdAt",
	"updated_at": "updatedAt",
}

// deletedFilter is the fixed filter building block that selects only companies in the trash.
const deletedFilter = "deletedAt[is]:NOT_NULL"

// listQuery builds the complete query of one list request out of the validated options.
func listQuery(options ListOptions) url.Values {
	query := url.Values{}
	query.Set("limit", strconv.Itoa(options.Limit))
	query.Set("depth", noRelations)

	field := sortFields[options.Sort]
	if field == "" {
		field = sortFields["created_at"]
	}
	order := "DescNullsLast"
	if options.Direction == "asc" {
		order = "AscNullsFirst"
	}
	query.Set("order_by", field+"["+order+"]")

	filters := make([]string, 0, 3)
	if options.NameContains != "" {
		filters = append(filters, `name[ilike]:"%`+options.NameContains+`%"`)
	}
	if options.DomainContains != "" {
		filters = append(filters, `domainName.primaryLinkUrl[ilike]:"%`+options.DomainContains+`%"`)
	}
	if options.Deleted {
		filters = append(filters, deletedFilter)
	}
	if len(filters) > 0 {
		query.Set("filter", strings.Join(filters, ","))
	}
	if options.Cursor != "" {
		query.Set("starting_after", options.Cursor)
	}
	return query
}

// ListResult is the normalised page of companies.
type ListResult struct {
	Companies  []Company `json:"companies"`
	NextCursor string    `json:"next_cursor,omitempty"`
	HasMore    bool      `json:"has_more"`
}

// Company is the stable Qatlas view of one company record. It carries only the conservative core fields
// every Twenty workspace has; a workspace-specific custom field is never adopted into this contract.
type Company struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Domain    string `json:"domain,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// ListCompanies reads exactly one bounded page of companies.
func (c *Client) ListCompanies(ctx context.Context, options ListOptions) (*ListResult, error) {
	const op = "list companies"
	if err := c.requireCompany(); err != nil {
		return nil, err
	}
	if err := options.normalize(); err != nil {
		return nil, providerError(op, err.Error())
	}

	var page companiesPageJSON
	if err := c.get(ctx, op, companiesPath, listQuery(options), maxResponseBytes, &page); err != nil {
		return nil, err
	}

	companies := make([]Company, 0, len(page.Data.Companies))
	for _, record := range page.Data.Companies {
		if !validUUID(record.ID) {
			return nil, &provider.Error{
				Class: provider.ClassInvalidResponse, Op: op,
				Message: "Twenty returned a company without a usable identifier",
			}
		}
		companies = append(companies, Company{
			ID: record.ID, Name: record.Name, Domain: primaryDomain(record.DomainName),
		})
	}

	result := &ListResult{Companies: companies, HasMore: page.PageInfo.HasNextPage}
	if page.PageInfo.HasNextPage {
		result.NextCursor = page.PageInfo.EndCursor
	}
	return result, nil
}

// normalize applies the defaults and the bounds of one list request. The application core validates the
// same rules against the input schema first; this keeps a direct caller inside them as well.
func (o *ListOptions) normalize() error {
	if o.Limit == 0 {
		o.Limit = defaultPageSize
	}
	if o.Limit < 1 || o.Limit > maxPageSize {
		return fmt.Errorf("the page size must be between 1 and %d", maxPageSize)
	}
	if o.Sort != "" && sortFields[o.Sort] == "" {
		return errors.New("the sort property is not supported")
	}
	if o.Direction != "" && o.Direction != "asc" && o.Direction != "desc" {
		return errors.New("the sort direction must be asc or desc")
	}
	if len(o.Cursor) > maxCursorLength || !safeCursor(o.Cursor) {
		return errors.New("the cursor is not one this provider handed out")
	}
	if !safeSearchTerm(o.NameContains, searchNameMax) {
		return errors.New("the name filter contains characters a search cannot carry")
	}
	if !safeDomainTerm(o.DomainContains) {
		return errors.New("the domain filter is not a usable domain fragment")
	}
	return nil
}

// GetCompany reads exactly the company of a validated identifier and performs no other provider I/O.
func (c *Client) GetCompany(ctx context.Context, id string) (*Company, error) {
	const op = "get company"
	if err := c.requireCompany(); err != nil {
		return nil, err
	}
	if !validUUID(id) {
		return nil, providerError(op, "the company identifier must be a UUID")
	}

	query := url.Values{}
	query.Set("depth", noRelations)
	var response companyJSON
	if err := c.get(ctx, op, companiesPath+"/"+url.PathEscape(id), query, maxResponseBytes, &response); err != nil {
		return nil, err
	}
	record := response.Data.Company
	if !strings.EqualFold(record.ID, id) {
		return nil, &provider.Error{
			Class: provider.ClassInvalidResponse, Op: op,
			Message: "Twenty answered with a different company than the requested one",
		}
	}
	return &Company{
		ID: record.ID, Name: record.Name, Domain: primaryDomain(record.DomainName),
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}, nil
}

func companyPayload(name *string, domain *string) map[string]any {
	payload := map[string]any{}
	if name != nil {
		payload["name"] = *name
	}
	if domain != nil {
		link := *domain
		if link != "" {
			link = "https://" + link
		}
		payload["domainName"] = map[string]string{"primaryLinkUrl": link, "primaryLinkLabel": *domain}
	}
	return payload
}

func (c *Client) CreateCompany(ctx context.Context, name string, domain *string) (*Company, error) {
	return c.changeCompany(ctx, "create company", http.MethodPost, companiesPath, companyPayload(&name, domain), "")
}

func (c *Client) UpdateCompany(ctx context.Context, id string, name, domain *string) (*Company, error) {
	if !validUUID(id) {
		return nil, providerError("update company", "the company identifier must be a UUID")
	}
	return c.changeCompany(ctx, "update company", http.MethodPatch, companiesPath+"/"+url.PathEscape(id), companyPayload(name, domain), id)
}

// DeleteCompany moves one company to the Twenty trash, where it stays recoverable. The soft-delete
// parameter is part of the fixed path; nothing selects between soft and permanent deletion.
func (c *Client) DeleteCompany(ctx context.Context, id string) error {
	return c.removeCompany(ctx, "delete company", id, "?soft_delete=true")
}

// DestroyCompany deletes one company permanently. Twenty answers soft and permanent deletion alike, so
// the kind of deletion follows only from the request sent here.
func (c *Client) DestroyCompany(ctx context.Context, id string) error {
	return c.removeCompany(ctx, "destroy company", id, "")
}

func (c *Client) removeCompany(ctx context.Context, op, id, query string) error {
	if !validUUID(id) {
		return providerError(op, "the company identifier must be a UUID")
	}
	_, err := c.changeCompany(ctx, op, http.MethodDelete, companiesPath+"/"+url.PathEscape(id)+query, nil, id)
	return err
}

// RestoreCompany brings one company back from the Twenty trash.
func (c *Client) RestoreCompany(ctx context.Context, id string) (*Company, error) {
	if !validUUID(id) {
		return nil, providerError("restore company", "the company identifier must be a UUID")
	}
	return c.changeCompany(ctx, "restore company", http.MethodPatch, companiesPath+"/"+url.PathEscape(id)+"/restore", nil, id)
}

func (c *Client) changeCompany(ctx context.Context, op, method, path string, payload map[string]any, expectedID string) (*Company, error) {
	if err := c.requireCompany(); err != nil {
		return nil, err
	}
	var response companyMutationJSON
	if err := c.change(ctx, op, method, path, payload, &response); err != nil {
		return nil, err
	}
	record := response.record()
	if !validUUID(record.ID) || (expectedID != "" && !strings.EqualFold(record.ID, expectedID)) {
		return nil, &provider.Error{Class: provider.ClassInvalidResponse, Op: op, Message: "Twenty returned a company without the expected identifier" + mutationUncertain}
	}
	return &Company{ID: record.ID, Name: record.Name, Domain: primaryDomain(record.DomainName), CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}, nil
}

// companyRecordJSON mirrors the Twenty record fields this provider reads. DomainName stays raw because a
// workspace may carry it as a link object or, in an older schema, as plain text.
type companyRecordJSON struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	DomainName json.RawMessage `json:"domainName"`
	CreatedAt  string          `json:"createdAt"`
	UpdatedAt  string          `json:"updatedAt"`
}

type companiesPageJSON struct {
	Data struct {
		Companies []companyRecordJSON `json:"companies"`
	} `json:"data"`
	PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
}

type companyJSON struct {
	Data struct {
		Company companyRecordJSON `json:"company"`
	} `json:"data"`
}

// companyMutationJSON accepts the stable envelopes emitted by Twenty versions for generated REST
// mutations. The operation-specific field names differ, but the company record itself does not.
type companyMutationJSON struct {
	Data struct {
		Company        companyRecordJSON `json:"company"`
		CreateCompany  companyRecordJSON `json:"createCompany"`
		UpdateCompany  companyRecordJSON `json:"updateCompany"`
		DeleteCompany  companyRecordJSON `json:"deleteCompany"`
		RestoreCompany companyRecordJSON `json:"restoreCompany"`
	} `json:"data"`
}

func (r companyMutationJSON) record() companyRecordJSON {
	if r.Data.Company.ID != "" {
		return r.Data.Company
	}
	if r.Data.CreateCompany.ID != "" {
		return r.Data.CreateCompany
	}
	if r.Data.UpdateCompany.ID != "" {
		return r.Data.UpdateCompany
	}
	if r.Data.DeleteCompany.ID != "" {
		return r.Data.DeleteCompany
	}
	return r.Data.RestoreCompany
}

// primaryDomain reduces the Twenty link field to the one stable string this provider reports. A workspace
// that carries the field as plain text is read as that text.
func primaryDomain(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var link struct {
		PrimaryLinkLabel string `json:"primaryLinkLabel"`
		PrimaryLinkURL   string `json:"primaryLinkUrl"`
	}
	if json.Unmarshal(raw, &link) != nil {
		return ""
	}
	if link.PrimaryLinkLabel != "" {
		return link.PrimaryLinkLabel
	}
	return link.PrimaryLinkURL
}

// get performs one bounded read against the configured origin and decodes the response into out.
func (c *Client) get(ctx context.Context, op, path string, query url.Values, limit int64, out any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "Twenty", err)
	}

	target := c.origin + path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")

	response, err := c.http.Do(req)
	if err != nil {
		return provider.Transport(op, "Twenty", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return c.responseError(op, response)
	}
	if failure := provider.ReadJSON(op, "Twenty", response.Body, limit, out); failure != nil {
		return failure
	}
	return nil
}

func (c *Client) change(ctx context.Context, op, method, path string, payload any, out any) error {
	return c.changeWith(ctx, op, mutationUncertain, method, path, payload, out)
}

// changeWith sends one change and appends uncertain to every failure whose request may have taken effect.
func (c *Client) changeWith(ctx context.Context, op, uncertain, method, path string, payload any, out any) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return provider.Waited(op, "Twenty", err)
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil || len(encoded) > maxResponseBytes {
			return providerError(op, "the request exceeds the size limit")
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.origin+path, body)
	if err != nil {
		return providerError(op, "the request could not be built")
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(req)
	if err != nil {
		failure := provider.Transport(op, "Twenty", err)
		if failure.MayHaveArrived() {
			failure.Message += uncertain
		}
		return failure
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		failure := c.responseError(op, response)
		if response.StatusCode >= 500 {
			failure.Message += uncertain
		}
		return failure
	}
	if failure := provider.ReadJSON(op, "Twenty", response.Body, maxResponseBytes, out); failure != nil {
		failure.Message += uncertain
		return failure
	}
	return nil
}

// responseError maps an HTTP status to a stable class. The provider message is never copied: Twenty echoes
// request and record detail into it, and the class plus the status is what a caller can act on. A 429
// holds the rate limit of the key for the time Twenty asks for.
func (c *Client) responseError(op string, response *http.Response) *provider.Error {
	if response.StatusCode == http.StatusTooManyRequests {
		c.limiter.HoldFor(provider.RetryAfter(response.Header))
	}
	return provider.ClassifyStatus(op, response.StatusCode, provider.StatusTexts{
		Subject: "Twenty",
		Auth:    "Twenty rejected the API key",
		Permission: "the workspace role of this API key may not perform this operation; check the role of " +
			"the API key in Twenty",
		NotFound: "this Twenty workspace does not hold this record or object",
	})
}

func providerError(op, message string) error {
	return &provider.Error{Class: provider.ClassProviderError, Op: op, Message: message}
}

// validAPIKey keeps an obviously unusable value out of a request. The real check is the provider's.
func validAPIKey(value string) bool {
	if len(value) < 8 || len(value) > 4096 {
		return false
	}
	for _, r := range value {
		// A header value may not carry control characters, and a Twenty key never does.
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

// validUUID accepts the canonical 8-4-4-4-12 hexadecimal form and nothing else.
func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i := 0; i < len(value); i++ {
		b := value[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if b != '-' {
				return false
			}
			continue
		}
		hex := (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
		if !hex {
			return false
		}
	}
	return true
}

// The bounds the search terms share with the input schema.
const (
	searchNameMax   = 64
	searchDomainMax = 253
)

// safeSearchTerm mirrors the schema pattern in Go, so a direct caller cannot smuggle a filter separator
// into the value of a Twenty filter expression either.
func safeSearchTerm(value string, max int) bool {
	if value == "" {
		return true
	}
	if len([]rune(value)) > max {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == ' ', r == '.', r == '_', r == '\'', r == '+', r == '@', r == '&', r == '-':
		case r > 0x7f && (unicode.IsLetter(r) || unicode.IsDigit(r)):
		default:
			return false
		}
	}
	return true
}

func safeDomainTerm(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > searchDomainMax {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-':
		default:
			return false
		}
	}
	return true
}

// safeCursor accepts the opaque cursor alphabet this provider hands out and nothing else.
func safeCursor(value string) bool {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '+', r == '/', r == '=', r == '_', r == '.', r == ':', r == '-':
		default:
			return false
		}
	}
	return true
}
