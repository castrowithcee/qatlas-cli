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
//
// The catalog keeps only the tools a configured connection offers, or with Connection the tools that one
// connection offers. All keeps every tool of the other filters as well and says of each one nobody offers
// why not.
type SearchRequest struct {
	Query      string            `json:"query,omitempty"`
	Provider   string            `json:"provider,omitempty"`
	Connection string            `json:"connection,omitempty"`
	Effect     capability.Effect `json:"effect,omitempty"`
	All        bool              `json:"all,omitempty"`
	Limit      int               `json:"limit,omitempty"`
	Cursor     string            `json:"cursor,omitempty"`
}

// SearchHit is the bounded discovery view of one descriptor: the same entry as ToolSummary, so a search
// costs what the index costs, with the offering connections as a list. Description, version, and tags are
// one describe away. Connections names every connection that offers it; Reason is set only on a tool none
// of them offers, which only a request with All returns.
type SearchHit struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Effect      capability.Effect `json:"effect"`
	Connections []string          `json:"connections"`
	Reason      config.Refusal    `json:"reason,omitempty"`
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
			ID: descriptor.ID, Title: title, Effect: descriptor.Risk.Effect,
			Connections: c.connectionNamesFor(descriptor), Reason: c.refusal(request, descriptor),
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
		return "", &InvalidRequestError{Message: "cursor is not a next_cursor of this search; " +
			"start the search again without cursor"}
	}
	return string(decoded[searchCursorBinding:]), nil
}

// searchFingerprint identifies the filters of a search. The query is normalized the way catalog matches
// it, so spellings that select the same operations share their cursors. Limit is not part of it: a
// continuation stays exact whatever page size the next request asks for.
func searchFingerprint(request SearchRequest) []byte {
	filters, _ := json.Marshal([]string{
		strings.Join(strings.Fields(strings.ToLower(request.Query)), " "),
		request.Provider, request.Connection, string(request.Effect), fmt.Sprint(request.All),
	})
	sum := sha256.Sum256(filters)
	return sum[:searchCursorBinding]
}

// ProviderSummary is one namespace of the tool catalog: what kind of system it is, what it stands for in
// this installation, how many tools it offers, how many configured connections can run them, and how many
// are configured at all. A provider without a connection stays listed with zero, so it is visible as
// unconfigured rather than silently missing, and a connection that offers no tool counts in Configured
// but not in Connections, so the difference shows it.
//
// Description is the provider's own line and the same everywhere; Note is the line the user maintains in
// the configuration and is empty where there is none. Neither names a service, a URL, or a credential.
// The members are ordered by meaning: the identifier, its descriptions, then the counts.
type ProviderSummary struct {
	Provider    string `json:"provider"`
	Description string `json:"description"`
	Note        string `json:"note"`
	Tools       int    `json:"tools"`
	Connections int    `json:"connections"`
	Configured  int    `json:"configured"`
}

// ProvidersResponse is the payload inside the CLI envelope.
type ProvidersResponse struct {
	Providers []ProviderSummary `json:"providers"`
}

// ProviderIDs returns the ID of every provider the registry knows, sorted.
func ProviderIDs(registry *capability.Registry) []string {
	all := registry.ProviderMetadataAll()
	ids := make([]string, len(all))
	for i, metadata := range all {
		ids[i] = metadata.ID
	}
	return ids
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
			metadata, _ := c.registry.ProviderMetadata(descriptor.Provider)
			providers = append(providers, ProviderSummary{
				Provider: descriptor.Provider, Description: metadata.Description,
				Note:        c.config.ProviderNotes[descriptor.Provider],
				Connections: len(c.connectionNamesWithAnyOperation(descriptor.Provider)),
				Configured:  len(c.connectionNames(descriptor.Provider)),
			})
		}
		providers[index].Tools++
	}
	return ProvidersResponse{Providers: providers}
}

// The ways a connection's tools list shapes what it offers, as ConnectionSummary publishes them.
const (
	// ToolsAllPermitted: no tools list; every tool whose effect the permissions allow, except a tool that
	// requires an allow-list.
	ToolsAllPermitted = "all-permitted"
	// ToolsListed: a tools list; only the tools it names.
	ToolsListed = "listed"
	// ToolsNone: an explicitly empty tools list; no tool at all.
	ToolsNone = "none"
)

// ConnectionSummary is the discovery view of one configured route: the name an invoke request carries, the
// provider it reaches, the line its owner maintains, the effects its permissions allow, separated by single
// spaces, and how its tools list shapes what it offers. It never names a service, a URL, a credential, a
// target, or a secret source.
type ConnectionSummary struct {
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	Description string `json:"description"`
	Permissions string `json:"permissions"`
	Tools       string `json:"tools"`
}

// ConnectionsResponse is the payload inside the CLI envelope.
type ConnectionsResponse struct {
	Connections []ConnectionSummary `json:"connections"`
}

// Connections lists the configured routes, of one provider when provider is not empty, sorted by provider
// and then by name. It answers from the configuration alone.
func (c *Core) Connections(provider string) ConnectionsResponse {
	connections := make([]ConnectionSummary, 0)
	for name, connection := range c.config.Connections {
		owner := c.config.Services[connection.Service].Provider
		if provider != "" && owner != provider {
			continue
		}
		permitted := map[config.Permission]bool{}
		for _, permission := range c.config.ConnectionPermissions(name) {
			permitted[permission] = true
		}
		effects := make([]string, 0, len(permitted))
		for _, permission := range config.Permissions() {
			if permitted[permission] {
				effects = append(effects, string(permission))
			}
		}
		tools := ToolsAllPermitted
		switch {
		case connection.Tools != nil && len(connection.Tools) == 0:
			tools = ToolsNone
		case connection.Tools != nil:
			tools = ToolsListed
		}
		connections = append(connections, ConnectionSummary{
			Name: name, Provider: owner, Description: connection.Description,
			Permissions: strings.Join(effects, " "), Tools: tools,
		})
	}
	sort.Slice(connections, func(i, j int) bool {
		if connections[i].Provider != connections[j].Provider {
			return connections[i].Provider < connections[j].Provider
		}
		return connections[i].Name < connections[j].Name
	})
	return ConnectionsResponse{Connections: connections}
}

// ToolSummary is the index entry of one tool: the ID an invoke request carries, what the tool does,
// whether it reads or changes the remote system, and the connections that offer it. That is what choosing
// between the tools of one namespace needs; schemas, tags, examples and descriptions of the routes are one
// describe away.
//
// Connections holds the offering connection names separated by single spaces, so the index stays one
// table row per tool. Reason is present exactly in a listing of all tools: empty for an offered tool, and
// otherwise the refusal that says why no connection offers it.
type ToolSummary struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Effect      capability.Effect `json:"effect"`
	Connections string            `json:"connections"`
	Reason      *config.Refusal   `json:"reason,omitempty"`
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
		summary := ToolSummary{
			ID: descriptor.ID, Title: title, Effect: descriptor.Risk.Effect,
			Connections: strings.Join(c.connectionNamesFor(descriptor), " "),
		}
		if request.All {
			reason := c.refusal(request, descriptor)
			summary.Reason = &reason
		}
		tools = append(tools, summary)
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

	if request.Provider != "" {
		if _, ok := c.registry.ProviderMetadata(request.Provider); !ok {
			return nil, &InvalidRequestError{Message: fmt.Sprintf("unknown provider %q", request.Provider) +
				DidYouMean(Suggest(request.Provider, ProviderIDs(c.registry))) +
				"; leave the provider out to search every provider"}
		}
	}

	var selectedProvider string
	if request.Connection != "" {
		resolved, err := c.connection(request.Connection)
		if err != nil {
			return nil, unknownConnectionOf(err, request.Provider, "")
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
		if !request.All && c.refusal(request, descriptor) != "" {
			continue
		}
		if request.Effect != "" && descriptor.Risk.Effect != request.Effect {
			continue
		}
		haystack := strings.ToLower(strings.Join(c.searchText(request, descriptor), " "))
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

// searchText is everything a query term may match for one tool: its ID, title, description, and tags, the
// description of its provider and the note the user keeps on that provider, and the descriptions of the
// connections that offer it, or with a connection filter of that connection alone. A word of a task such
// as "wiki" or "CRM" thus finds the tools of the provider or route it names, without a list of synonyms.
func (c *Core) searchText(request SearchRequest, descriptor capability.Descriptor) []string {
	metadata, _ := c.registry.ProviderMetadata(descriptor.Provider)
	text := append([]string{descriptor.ID, descriptor.Title, descriptor.Description}, descriptor.Tags...)
	text = append(text, metadata.Description, c.config.ProviderNotes[descriptor.Provider])
	for _, name := range c.connectionNamesFor(descriptor) {
		if request.Connection == "" || name == request.Connection {
			text = append(text, c.config.Connections[name].Description)
		}
	}
	return text
}

// DescribeRequest selects exactly one versioned descriptor. Connection only restricts its possible routes.
// Full asks the publishing surface for the complete descriptor instead of its compact contract; Describe
// itself always answers with the complete one.
type DescribeRequest struct {
	Operation  string `json:"operation"`
	Version    int    `json:"version,omitempty"`
	Connection string `json:"connection,omitempty"`
	Full       bool   `json:"full,omitempty"`
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

// CompactDescribeResponse is what describe publishes unless the complete descriptor is asked for: the
// compact contract and the possible configured routes.
type CompactDescribeResponse struct {
	Operation   CompactDescriptor `json:"operation"`
	Connections []ConnectionRef   `json:"connections"`
}

// Compact returns the response with the compact contract in place of the complete descriptor.
func (r DescribeResponse) Compact() CompactDescribeResponse {
	return CompactDescribeResponse{Operation: Compact(r.Operation), Connections: r.Connections}
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
			return DescribeResponse{}, unknownConnectionOf(err, descriptor.Provider, descriptor.ID)
		}
		if reason := c.connectionRefusal(resolved, descriptor); reason != "" {
			return DescribeResponse{}, &capability.UnsupportedError{
				Connection: request.Connection, Capability: request.Operation, Reason: reason,
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
	if err := ValidateJSON(descriptor.InputSchema, request.Arguments); err != nil {
		return InvokeResponse{}, &InvalidRequestError{Message: err.Error()}
	}

	resolved, err := c.selectConnection(request.Connection, descriptor)
	if err != nil {
		return InvokeResponse{}, unknownConnectionOf(err, descriptor.Provider, descriptor.ID)
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

// auditResult is success or the code of the failure, the same code the diagnostic leads with.
func auditResult(err error) string {
	if err == nil {
		return "success"
	}
	return string(ErrorCode(err))
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
	if !ok {
		all := c.registry.All()
		ids := make([]string, len(all))
		for i, known := range all {
			ids[i] = known.ID
		}
		return capability.Descriptor{}, nil, &UnknownOperationError{Operation: id, Suggestion: Suggest(id, ids)}
	}
	if version != 0 && descriptor.Version != version {
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
		if reason := c.connectionRefusal(resolved, descriptor); reason != "" {
			return nil, &capability.UnsupportedError{Connection: explicit, Capability: descriptor.ID, Reason: reason}
		}
		return resolved, nil
	}
	if descriptor.RequiresExplicitConnection {
		return nil, &ConnectionSelectionError{
			Operation: descriptor.ID, ExplicitRequired: true, Connections: c.connectionRefs(descriptor),
		}
	}

	defaults := map[string]bool{}
	for _, key := range []string{descriptor.ID, descriptor.Provider} {
		if name := c.config.Defaults.Connections[key]; name != "" {
			defaults[name] = true
		}
	}
	if len(defaults) > 1 {
		// Conflicting defaults name the candidates, but only those that can actually take the operation.
		candidates := make([]ConnectionRef, 0, len(defaults))
		for _, name := range sortedSet(defaults) {
			if resolved, err := c.connection(name); err == nil && resolved.Provider == descriptor.Provider &&
				c.connectionAllows(name, descriptor) {
				candidates = append(candidates, c.connectionRef(name))
			}
		}
		return nil, &ConnectionAmbiguousError{Operation: descriptor.ID, Connections: candidates}
	}
	for name := range defaults {
		resolved, err := c.connection(name)
		if err != nil {
			return nil, err
		}
		if reason := c.connectionRefusal(resolved, descriptor); reason != "" {
			return nil, &capability.UnsupportedError{Connection: name, Capability: descriptor.ID, Reason: reason}
		}
		return resolved, nil
	}

	connections := c.connectionRefs(descriptor)
	switch len(connections) {
	case 0:
		return nil, &ConnectionSelectionError{Operation: descriptor.ID}
	case 1:
		return c.connection(connections[0].Name)
	default:
		return nil, &ConnectionAmbiguousError{Operation: descriptor.ID, Connections: connections}
	}
}

// unknownConnectionOf names the provider and the tool a request asked about in the unknown connection err
// reports, so its next step can point to the configured ones. Both are names of the registry, never a value;
// an empty one stays unknown. Any other error is returned as it is.
func unknownConnectionOf(err error, provider, operation string) error {
	var unknown *capability.UnknownConnectionError
	if errors.As(err, &unknown) {
		unknown.Provider, unknown.Operation = provider, operation
	}
	return err
}

func (c *Core) connection(name string) (*config.Resolved, error) {
	connection, ok := c.config.Connections[name]
	if !ok {
		names := make([]string, 0, len(c.config.Connections))
		for known := range c.config.Connections {
			names = append(names, known)
		}
		return nil, &capability.UnknownConnectionError{Name: name, Suggestion: Suggest(name, names)}
	}
	service := c.config.Services[connection.Service]
	return &config.Resolved{
		Name: name, Provider: service.Provider, BaseURL: c.config.ServiceBaseURL(service), Options: service.Options,
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
	return c.config.ConnectionAllows(name, descriptor.Tool())
}

// connectionRefusal is the refusal of one resolved connection: another provider's connection never offers
// the tool, and a connection of its provider answers by the configuration rule.
func (c *Core) connectionRefusal(resolved *config.Resolved, descriptor capability.Descriptor) config.Refusal {
	if resolved.Provider != descriptor.Provider {
		return config.RefusalOtherProvider
	}
	return c.config.ConnectionRefusal(resolved.Name, descriptor.Tool())
}

// refusal says why the catalog of request does not offer a tool, or nothing when it does. With a
// connection it is that connection's refusal. Otherwise a tool is offered when any connection of its
// provider offers it; when none does, the refusal of the connection that comes closest wins, in the order
// not-in-tools-list, requires-tool-allow-list, effect-not-permitted, and a provider without any connection
// is refused as no-connection. The choice is deterministic and names the smallest change that would offer
// the tool.
func (c *Core) refusal(request SearchRequest, descriptor capability.Descriptor) config.Refusal {
	if request.Connection != "" {
		resolved, err := c.connection(request.Connection)
		if err != nil {
			return config.RefusalNoConnection
		}
		return c.connectionRefusal(resolved, descriptor)
	}
	names := c.connectionNames(descriptor.Provider)
	if len(names) == 0 {
		return config.RefusalNoConnection
	}
	closest := config.RefusalEffect
	for _, name := range names {
		switch c.config.ConnectionRefusal(name, descriptor.Tool()) {
		case "":
			return ""
		case config.RefusalToolsList:
			closest = config.RefusalToolsList
		case config.RefusalToolAllowList:
			if closest != config.RefusalToolsList {
				closest = config.RefusalToolAllowList
			}
		}
	}
	return closest
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
