// Package application implements the provider-independent Search, Describe, and Invoke use cases.
package application

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxSearchResults = 50
	// searchCursorBinding is the length of the filter fingerprint a search cursor carries.
	searchCursorBinding = 12
)

// Core owns the configured, provider-independent operations surface.
type Core struct {
	registry *capability.Registry
	config   *config.Config
	secrets  *secret.Resolver
	redactor *redact.Redactor
	policy   Policy
	audit    io.Writer
}

// Policy may reject a fully validated request after connection selection and before confirmation,
// credential resolution, or provider I/O. A nil policy allows the request.
type Policy func(context.Context, InvokeRequest, capability.Descriptor, *config.Resolved) error

// New returns an application core over one validated configuration.
func New(registry *capability.Registry, cfg *config.Config, secrets *secret.Resolver, redactor *redact.Redactor) *Core {
	return &Core{registry: registry, config: cfg, secrets: secrets, redactor: redactor}
}

// SetPolicy installs the policy used by subsequent invocations.
func (c *Core) SetPolicy(policy Policy) { c.policy = policy }

// SetAudit sends request-bound mutation audit events to writer. The CLI supplies a request-local buffer
// that it flushes in stream-contract order; tests can use the same seam. Read operations and requests
// rejected before confirmation do not produce an event.
func (c *Core) SetAudit(writer io.Writer) { c.audit = writer }

// SearchRequest filters the local operation catalog. Limit is capped even when omitted or non-positive.
// Cursor continues a previous page: it is the opaque next_cursor of a response to the same filters, and
// an omitted or empty cursor selects the first page.
type SearchRequest struct {
	Query      string            `json:"query,omitempty"`
	Provider   string            `json:"provider,omitempty"`
	Connection string            `json:"connection,omitempty"`
	Effect     capability.Effect `json:"effect,omitempty"`
	Limit      int               `json:"limit,omitempty"`
	Cursor     string            `json:"cursor,omitempty"`
}

// SearchHit is the bounded discovery view of one descriptor.
type SearchHit struct {
	ID          string            `json:"id"`
	Version     int               `json:"version"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Tags        []string          `json:"tags"`
	Provider    string            `json:"provider"`
	Effect      capability.Effect `json:"effect"`
	Connections []string          `json:"connections"`
}

// SearchResponse is the payload inside the CLI envelope. HasMore is true exactly when another match
// follows this page; NextCursor is then the cursor of the following page and absent otherwise.
type SearchResponse struct {
	Operations []SearchHit `json:"operations"`
	HasMore    bool        `json:"has_more"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

// Search performs deterministic local discovery and never resolves credentials or calls a provider. Its
// response stays bounded: an omitted, non-positive, or oversized limit becomes maxSearchResults. Pages
// follow the stable ID order of the registry, and a cursor continues after the last ID of its page, so
// reading every page yields each match exactly once.
func (c *Core) Search(request SearchRequest) (SearchResponse, error) {
	limit := request.Limit
	if limit <= 0 || limit > maxSearchResults {
		limit = maxSearchResults
	}
	after, err := searchCursorAfter(request)
	if err != nil {
		return SearchResponse{}, err
	}
	// One extra match decides has_more without a second pass and without publishing that match.
	descriptors, err := c.catalog(request, after, limit+1)
	if err != nil {
		return SearchResponse{}, err
	}
	response := SearchResponse{HasMore: len(descriptors) > limit}
	if response.HasMore {
		descriptors = descriptors[:limit]
		response.NextCursor = searchCursor(request, descriptors[limit-1].ID)
	}
	hits := make([]SearchHit, 0, len(descriptors))
	for _, descriptor := range descriptors {
		title := descriptor.Title
		if title == "" {
			title = descriptor.Description
		}
		hits = append(hits, SearchHit{
			ID: descriptor.ID, Version: descriptor.Version, Title: title,
			Description: descriptor.Description, Tags: nonNilStrings(descriptor.Tags),
			Provider: descriptor.Provider, Effect: descriptor.Risk.Effect,
			Connections: c.connectionNamesFor(descriptor),
		})
	}
	response.Operations = hits
	return response, nil
}

// searchCursor encodes the continuation after the operation ID last. It binds the cursor to the filters of
// its request, so a cursor can never continue a different search.
func searchCursor(request SearchRequest, last string) string {
	return base64.RawURLEncoding.EncodeToString(append(searchFingerprint(request), last...))
}

// searchCursorAfter returns the operation ID a request continues after, or the empty string for the first
// page. A cursor that is malformed or belongs to other filters is an invalid request.
func searchCursorAfter(request SearchRequest) (string, error) {
	if request.Cursor == "" {
		return "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(request.Cursor)
	if err != nil || len(decoded) <= searchCursorBinding ||
		!bytes.Equal(decoded[:searchCursorBinding], searchFingerprint(request)) {
		return "", &InvalidRequestError{Message: "cursor is not a next_cursor of this search"}
	}
	return string(decoded[searchCursorBinding:]), nil
}

// searchFingerprint identifies the filters of a search. The query is normalized the way catalog matches
// it, so spellings that select the same operations share their cursors. Limit is not part of it: a
// continuation stays exact whatever page size the next request asks for.
func searchFingerprint(request SearchRequest) []byte {
	filters, _ := json.Marshal([]string{
		strings.Join(strings.Fields(strings.ToLower(request.Query)), " "),
		request.Provider, request.Connection, string(request.Effect),
	})
	sum := sha256.Sum256(filters)
	return sum[:searchCursorBinding]
}

// ProviderSummary is one namespace of the tool catalog: how many tools it offers and how many configured
// connections can run them. A provider without a connection stays listed with zero, so it is visible as
// unconfigured rather than silently missing.
type ProviderSummary struct {
	Provider    string `json:"provider"`
	Tools       int    `json:"tools"`
	Connections int    `json:"connections"`
}

// ProvidersResponse is the payload inside the CLI envelope.
type ProvidersResponse struct {
	Providers []ProviderSummary `json:"providers"`
}

// Providers answers the first step of discovery: which namespaces exist at all. It stays one line per
// provider however large the catalog grows, so a reader picks a namespace before paying for its tools.
func (c *Core) Providers() ProvidersResponse {
	// The namespaces are counted from the descriptors themselves, not from the provider metadata, so a
	// namespace that answers "tools" can never be missing here. Descriptors arrive sorted by ID, and the
	// namespace is the ID prefix, so first appearance is already alphabetical order.
	providers := make([]ProviderSummary, 0)
	at := map[string]int{}
	for _, descriptor := range c.registry.All() {
		index, seen := at[descriptor.Provider]
		if !seen {
			index = len(providers)
			at[descriptor.Provider] = index
			providers = append(providers, ProviderSummary{
				Provider: descriptor.Provider, Connections: len(c.connectionNamesWithAnyOperation(descriptor.Provider)),
			})
		}
		providers[index].Tools++
	}
	return ProvidersResponse{Providers: providers}
}

// ToolSummary is the index entry of one tool: the ID an invoke request carries, what the tool does, and
// whether it reads or changes the remote system. That is what choosing between the tools of one namespace
// needs; schemas, tags, examples and routes are one describe away.
type ToolSummary struct {
	ID     string            `json:"id"`
	Title  string            `json:"title"`
	Effect capability.Effect `json:"effect"`
}

// ToolsResponse is the payload inside the CLI envelope.
type ToolsResponse struct {
	Tools []ToolSummary `json:"tools"`
}

// Tools is the second step of discovery: the tools of one namespace, or of one targeted query. It applies
// the same filters as Search to the same descriptors but publishes only what picking a tool needs.
//
// The catalog view answers what this installation offers, so a truncated answer would read as a complete
// one; the bounded Search response stays the contract of the request-bound agent surface.
func (c *Core) Tools(request SearchRequest) (ToolsResponse, error) {
	descriptors, err := c.catalog(request, "", 0)
	if err != nil {
		return ToolsResponse{}, err
	}
	tools := make([]ToolSummary, 0, len(descriptors))
	for _, descriptor := range descriptors {
		title := descriptor.Title
		if title == "" {
			title = descriptor.Description
		}
		tools = append(tools, ToolSummary{ID: descriptor.ID, Title: title, Effect: descriptor.Risk.Effect})
	}
	return ToolsResponse{Tools: tools}, nil
}

// catalog filters the registry deterministically and returns the matching descriptors in registry order,
// which is sorted by ID. Both discovery views project this one result, so the compact index and the
// bounded search answer cannot disagree about which tools exist. A non-empty after skips every descriptor
// up to and including that ID. A limit of zero or less returns every match.
func (c *Core) catalog(request SearchRequest, after string, limit int) ([]capability.Descriptor, error) {
	if request.Effect != "" && !validEffect(request.Effect) {
		return nil, &InvalidRequestError{Message: fmt.Sprintf("unknown effect %q", request.Effect)}
	}

	var selectedProvider string
	if request.Connection != "" {
		resolved, err := c.connection(request.Connection)
		if err != nil {
			return nil, err
		}
		selectedProvider = resolved.Provider
	}

	terms := strings.Fields(strings.ToLower(request.Query))
	matches := make([]capability.Descriptor, 0)
	for _, descriptor := range c.registry.All() {
		if after != "" && descriptor.ID <= after {
			continue
		}
		if request.Provider != "" && descriptor.Provider != request.Provider {
			continue
		}
		if selectedProvider != "" && descriptor.Provider != selectedProvider {
			continue
		}
		if request.Connection != "" && !c.connectionAllows(request.Connection, descriptor) {
			continue
		}
		if request.Effect != "" && descriptor.Risk.Effect != request.Effect {
			continue
		}
		haystack := strings.ToLower(strings.Join(append([]string{
			descriptor.ID, descriptor.Title, descriptor.Description,
		}, descriptor.Tags...), " "))
		matched := true
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		matches = append(matches, descriptor)
		if len(matches) == limit {
			break
		}
	}
	return matches, nil
}

// DescribeRequest selects exactly one versioned descriptor. Connection only restricts its possible routes.
type DescribeRequest struct {
	Operation  string `json:"operation"`
	Version    int    `json:"version,omitempty"`
	Connection string `json:"connection,omitempty"`
}

// ConnectionRef is the discovery view of one configured route. Name is the stable value an invoke request
// carries; Description is the optional line its owner maintains and is empty when there is none. The
// description informs a person or an agent that already asked for this contract, and never selects a
// route by itself.
type ConnectionRef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// DescribeResponse is the complete operation contract and its possible configured routes.
type DescribeResponse struct {
	Operation   capability.Descriptor `json:"operation"`
	Connections []ConnectionRef       `json:"connections"`
}

// Describe returns one registered descriptor without contacting a provider.
func (c *Core) Describe(request DescribeRequest) (DescribeResponse, error) {
	descriptor, _, err := c.operation(request.Operation, request.Version)
	if err != nil {
		return DescribeResponse{}, err
	}
	connections := c.connectionRefs(descriptor)
	if request.Connection != "" {
		resolved, err := c.connection(request.Connection)
		if err != nil {
			return DescribeResponse{}, err
		}
		if resolved.Provider != descriptor.Provider || !c.connectionAllows(request.Connection, descriptor) {
			return DescribeResponse{}, &capability.UnsupportedError{
				Connection: request.Connection, Capability: request.Operation,
			}
		}
		connections = []ConnectionRef{c.connectionRef(request.Connection)}
	}
	return DescribeResponse{Operation: descriptor, Connections: connections}, nil
}

// InvokeRequest is one direct, request-bound invocation. Confirmed is consumed only by this value and is
// never persisted or reused.
type InvokeRequest struct {
	Operation  string          `json:"operation"`
	Version    int             `json:"version,omitempty"`
	Connection string          `json:"connection,omitempty"`
	Arguments  json.RawMessage `json:"arguments"`
	Confirmed  bool            `json:"confirm,omitempty"`
}

// InvokeResponse records the exact operation contract and route that produced Result.
type InvokeResponse struct {
	Operation  string          `json:"operation"`
	Version    int             `json:"version"`
	Connection string          `json:"connection"`
	Result     json.RawMessage `json:"result"`
}

// Invoke validates arguments before route selection, then applies policy and confirmation before
// dispatch. Provider output is normalized and validated before it can reach the caller.
func (c *Core) Invoke(ctx context.Context, request InvokeRequest) (response InvokeResponse, err error) {
	descriptor, rawHandler, err := c.operation(request.Operation, request.Version)
	if err != nil {
		return InvokeResponse{}, err
	}
	if len(request.Arguments) == 0 {
		request.Arguments = json.RawMessage(`{}`)
	}
	if err := validateJSON(descriptor.InputSchema, request.Arguments); err != nil {
		return InvokeResponse{}, &InvalidRequestError{Message: err.Error()}
	}

	resolved, err := c.selectConnection(request.Connection, descriptor)
	if err != nil {
		return InvokeResponse{}, err
	}
	if c.policy != nil {
		if err := c.policy(ctx, request, descriptor, resolved); err != nil {
			return InvokeResponse{}, &PolicyDeniedError{Operation: descriptor.ID}
		}
	}
	if descriptor.Risk.Confirmation == capability.ConfirmationRequired && !request.Confirmed {
		return InvokeResponse{}, &ConfirmationRequiredError{Operation: descriptor.ID}
	}

	handler := rawHandler
	if handler == nil {
		return InvokeResponse{}, fmt.Errorf("operation %q has an invalid handler", descriptor.ID)
	}
	if descriptor.Risk.Confirmation == capability.ConfirmationRequired {
		requestID, requestErr := newRequestID()
		if requestErr != nil {
			return InvokeResponse{}, fmt.Errorf("create audit request ID: %w", requestErr)
		}
		defer func() {
			c.writeAudit(auditEvent{
				RequestID: requestID, Operation: descriptor.ID, Connection: resolved.Name,
				Confirmed: request.Confirmed, Result: auditResult(err), Time: time.Now().UTC(),
			})
		}()
	}
	value, err := handler(ctx, resolved, c.secrets, c.redactor, request.Arguments)
	if err != nil {
		return InvokeResponse{}, err
	}
	normalized, err := normalize(value)
	if err != nil {
		return InvokeResponse{}, &InvalidProviderResponseError{Operation: descriptor.ID}
	}
	normalized = redactValue(c.redactor, normalized)
	if err := validateValue(descriptor.OutputSchema, normalized); err != nil {
		return InvokeResponse{}, &InvalidProviderResponseError{Operation: descriptor.ID}
	}
	result, err := json.Marshal(normalized)
	if err != nil {
		return InvokeResponse{}, &InvalidProviderResponseError{Operation: descriptor.ID}
	}
	return InvokeResponse{
		Operation: descriptor.ID, Version: descriptor.Version, Connection: resolved.Name, Result: result,
	}, nil
}

type auditEvent struct {
	RequestID  string    `json:"request_id"`
	Operation  string    `json:"operation"`
	Connection string    `json:"connection"`
	Confirmed  bool      `json:"confirmed"`
	Result     string    `json:"result"`
	Time       time.Time `json:"time"`
}

func newRequestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func auditResult(err error) string {
	if err == nil {
		return "success"
	}
	var providerErr *provider.Error
	if errors.As(err, &providerErr) {
		return string(providerErr.Class)
	}
	var invalid *InvalidProviderResponseError
	if errors.As(err, &invalid) {
		return string(provider.ClassInvalidResponse)
	}
	return "error"
}

func (c *Core) writeAudit(event auditEvent) {
	if c.audit == nil {
		return
	}
	// A failed stderr write must not turn a completed non-idempotent provider call into an apparent
	// invocation failure that invites a retry.
	_ = json.NewEncoder(c.audit).Encode(event)
}

func (c *Core) operation(id string, version int) (capability.Descriptor, capability.Handler, error) {
	if id == "" {
		return capability.Descriptor{}, nil, &InvalidRequestError{Message: "operation must not be empty"}
	}
	descriptor, handler, ok := c.registry.Lookup(id)
	if !ok || (version != 0 && descriptor.Version != version) {
		return capability.Descriptor{}, nil, &UnknownOperationError{Operation: id, Version: version}
	}
	return descriptor, handler, nil
}

func (c *Core) selectConnection(explicit string, descriptor capability.Descriptor) (*config.Resolved, error) {
	if explicit != "" {
		resolved, err := c.connection(explicit)
		if err != nil {
			return nil, err
		}
		if resolved.Provider != descriptor.Provider || !c.connectionAllows(explicit, descriptor) {
			return nil, &capability.UnsupportedError{Connection: explicit, Capability: descriptor.ID}
		}
		return resolved, nil
	}
	if descriptor.RequiresExplicitConnection {
		return nil, &ConnectionSelectionError{Operation: descriptor.ID, ExplicitRequired: true}
	}

	defaults := map[string]bool{}
	for _, key := range []string{descriptor.ID, descriptor.Provider} {
		if name := c.config.Defaults.Connections[key]; name != "" {
			defaults[name] = true
		}
	}
	if len(defaults) > 1 {
		return nil, &ConnectionAmbiguousError{Operation: descriptor.ID, Connections: sortedSet(defaults)}
	}
	for name := range defaults {
		resolved, err := c.connection(name)
		if err != nil {
			return nil, err
		}
		if resolved.Provider != descriptor.Provider || !c.connectionAllows(name, descriptor) {
			return nil, &capability.UnsupportedError{Connection: name, Capability: descriptor.ID}
		}
		return resolved, nil
	}

	connections := c.connectionNamesFor(descriptor)
	switch len(connections) {
	case 0:
		return nil, &ConnectionSelectionError{Operation: descriptor.ID}
	case 1:
		return c.connection(connections[0])
	default:
		return nil, &ConnectionAmbiguousError{Operation: descriptor.ID, Connections: connections}
	}
}

func (c *Core) connection(name string) (*config.Resolved, error) {
	connection, ok := c.config.Connections[name]
	if !ok {
		return nil, &capability.UnknownConnectionError{Name: name}
	}
	service := c.config.Services[connection.Service]
	return &config.Resolved{
		Name: name, Provider: service.Provider, BaseURL: service.BaseURL, Options: service.Options,
		Target: connection.Target, Targets: append([]string(nil), connection.Targets...),
		Service: connection.Service, Credential: connection.Credential,
		Secrets:     c.config.Credentials[connection.Credential],
		Permissions: c.config.ConnectionPermissions(name),
	}, nil
}

// connectionRefs is the described form of connectionNames: the same routes in the same order, each with
// the description its owner maintains.
func (c *Core) connectionRefs(descriptor capability.Descriptor) []ConnectionRef {
	names := c.connectionNamesFor(descriptor)
	refs := make([]ConnectionRef, len(names))
	for i, name := range names {
		refs[i] = c.connectionRef(name)
	}
	return refs
}

func (c *Core) connectionNamesFor(descriptor capability.Descriptor) []string {
	names := c.connectionNames(descriptor.Provider)
	allowed := names[:0]
	for _, name := range names {
		if c.connectionAllows(name, descriptor) {
			allowed = append(allowed, name)
		}
	}
	return allowed
}

func (c *Core) connectionNamesWithAnyOperation(providerID string) []string {
	names := c.connectionNames(providerID)
	descriptors := c.registry.Provider(providerID)
	allowed := names[:0]
	for _, name := range names {
		for _, descriptor := range descriptors {
			if c.connectionAllows(name, descriptor) {
				allowed = append(allowed, name)
				break
			}
		}
	}
	return allowed
}

func (c *Core) connectionAllows(name string, descriptor capability.Descriptor) bool {
	return c.config.ConnectionAllows(name, descriptor.ID, string(descriptor.Risk.Effect))
}

func (c *Core) connectionRef(name string) ConnectionRef {
	return ConnectionRef{Name: name, Description: c.config.Connections[name].Description}
}

func (c *Core) connectionNames(provider string) []string {
	names := make([]string, 0)
	for name, connection := range c.config.Connections {
		if c.config.Services[connection.Service].Provider == provider {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func validEffect(effect capability.Effect) bool {
	switch effect {
	case capability.EffectRead, capability.EffectCreate, capability.EffectUpdate,
		capability.EffectDelete, capability.EffectExecute:
		return true
	}
	return false
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string(nil), values...)
}

func sortedSet(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func normalize(value any) (any, error) {
	switch result := value.(type) {
	case output.Collection:
		rows := make([]any, len(result.Rows))
		for i, row := range result.Rows {
			object := make(map[string]any, len(result.Columns))
			for _, column := range result.Columns {
				object[column] = row[column]
			}
			rows[i] = object
		}
		value = rows
	case output.Object:
		object := make(map[string]any, len(result.Fields))
		for _, field := range result.Fields {
			object[field.Name] = field.Value
		}
		value = object
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var normalized any
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.UseNumber()
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func redactValue(redactor *redact.Redactor, value any) any {
	if redactor == nil {
		return value
	}
	switch value := value.(type) {
	case string:
		return redactor.Apply(value)
	case []any:
		for i := range value {
			value[i] = redactValue(redactor, value[i])
		}
	case map[string]any:
		for key := range value {
			value[key] = redactValue(redactor, value[key])
		}
	}
	return value
}
