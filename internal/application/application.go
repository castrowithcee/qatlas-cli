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
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
	"github.com/castrowithcee/qatlas-cli/internal/localfile"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/projectpath"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	maxSearchResults = 50
	// searchCursorBinding is the length of the filter fingerprint a search cursor carries.
	searchCursorBinding = 12
)

// Core owns the configured, provider-independent operations surface.
//
// It keeps the whole configuration and the projects of the current call (see SetProjects). Every public
// method first takes the view of the configuration those projects may see, and every discovery and invoke
// path reads that view alone, so a connection bound to another project is missing from all of them at once.
type Core struct {
	registry *capability.Registry
	all      *config.Config
	projects []string
	config   *config.Config
	secrets  *secret.Resolver
	redactor *redact.Redactor
	policy   Policy
	audit    io.Writer

	invokeLog    invokelog.Writer
	invokePath   string
	invokeClient *invokelog.ClientInfo
}

// Policy may reject a fully validated request after connection selection and before confirmation,
// credential resolution, or provider I/O. A nil policy allows the request.
type Policy func(context.Context, InvokeRequest, capability.Descriptor, *config.Resolved) error

// New returns an application core over one validated configuration. Until SetProjects names the projects
// of the call, it runs in no project: every connection bound to paths is left out, and every connection
// without paths is offered.
func New(registry *capability.Registry, cfg *config.Config, secrets *secret.Resolver, redactor *redact.Redactor) *Core {
	return &Core{registry: registry, all: cfg, config: cfg, secrets: secrets, redactor: redactor}
}

// SetProjects names the project directories the current call runs in, and replaces the ones named before.
// Each directory stands for the project it lies in, as projectpath.Roots resolves it: symbolic links
// resolved, the root of its Git working tree, and for a linked worktree also the repository's main working
// tree. A relative directory is taken relative to the working directory of this process. A connection bound
// to paths is offered from then on only when at least one of those projects lies inside one of its paths;
// a connection without paths is offered in every project and in none.
//
// The CLI passes its working directory. The MCP broker passes the same, or the roots its client names.
// SetProjects is not safe for use concurrently with any other method of the core.
func (c *Core) SetProjects(dirs []string) {
	var projects []string
	seen := map[string]bool{}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		for _, root := range projectpath.Roots(dir) {
			if !seen[root] {
				seen[root] = true
				projects = append(projects, root)
			}
		}
	}
	c.projects = projects
}

// scope takes the view of the configuration the projects of the current call may see. It is taken anew at
// the start of every public method, so the view follows both SetProjects and the configuration itself.
func (c *Core) scope() {
	if c.all != nil {
		c.config = c.all.ForProjects(c.projects)
	}
}

// SetPolicy installs the policy used by subsequent invocations.
func (c *Core) SetPolicy(policy Policy) { c.policy = policy }

// SetAudit sends request-bound mutation audit events to writer. The CLI supplies a request-local buffer
// that it flushes in stream-contract order; tests can use the same seam. Read operations and requests
// rejected before confirmation do not produce an event.
func (c *Core) SetAudit(writer io.Writer) { c.audit = writer }

// SetInvokeLog installs the invocation log that every subsequent Invoke call appends one entry to,
// whatever it does or returns: unlike the mutation audit event above, this covers every effect, confirmed
// or not, and every failure, including one before a connection is even selected. path is "cli" or "mcp";
// client is the MCP client's name and version from its initialize request, or nil on the CLI and wherever
// a client never declared one. A nil logger leaves invocation logging off, which no caller of this build
// does outside a test.
func (c *Core) SetInvokeLog(logger invokelog.Writer, path string, client *invokelog.ClientInfo) {
	c.invokeLog, c.invokePath, c.invokeClient = logger, path, client
}

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

// SearchHit is the bounded discovery view of one descriptor, and the entry of both discovery answers: it
// carries what an ordinary call needs, so describe is only one step away when the contract is unclear.
//
// Every value is a scalar, so the entries of an answer share one field set and TOON prints them as a table
// with one row per tool. Requires names the required arguments separated by "; ", each as name:form where
// the compact contract of describe gives a written form or the allowed values, and as the bare name
// otherwise; optional arguments and members of object arguments stay with describe. Confirm is true for a
// tool that needs confirmation. Connections names, separated by spaces, the connections that offer the tool
// only where they differ from those the answer names once at its top. Reason is set only by a request with
// All, on a tool the request's connections do not offer; a tool with a reason and no connections is offered
// by none. Hits marshals only the columns some entry uses.
type SearchHit struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Effect      capability.Effect `json:"effect"`
	Requires    string            `json:"requires"`
	Confirm     bool              `json:"confirm"`
	Connections string            `json:"connections"`
	Reason      config.Refusal    `json:"reason"`
}

// Hits is the list of entries of a discovery answer. It marshals every entry with the same fields: id, title,
// and effect always, and requires, confirm, connections, and reason only when at least one entry has a
// value there, so an unused column costs nothing and the entries still form one table.
type Hits []SearchHit

// MarshalJSON writes the entries with a common field set, in the order of the fields of SearchHit.
func (h Hits) MarshalJSON() ([]byte, error) {
	var requires, confirm, connections, reason bool
	for _, hit := range h {
		requires = requires || hit.Requires != ""
		confirm = confirm || hit.Confirm
		connections = connections || hit.Connections != ""
		reason = reason || hit.Reason != ""
	}
	out := []byte{'['}
	for i, hit := range h {
		if i > 0 {
			out = append(out, ',')
		}
		fields := []struct {
			name  string
			use   bool
			value any
		}{
			{"id", true, hit.ID}, {"title", true, hit.Title}, {"effect", true, hit.Effect},
			{"requires", requires, hit.Requires}, {"confirm", confirm, hit.Confirm},
			{"connections", connections, hit.Connections}, {"reason", reason, hit.Reason},
		}
		out = append(out, '{')
		first := true
		for _, field := range fields {
			if !field.use {
				continue
			}
			value, err := json.Marshal(field.value)
			if err != nil {
				return nil, err
			}
			if !first {
				out = append(out, ',')
			}
			first = false
			out = append(out, fmt.Sprintf("%q:", field.name)...)
			out = append(out, value...)
		}
		out = append(out, '}')
	}
	return append(out, ']'), nil
}

// SearchResponse is the payload inside the CLI envelope. Connections names once every connection that
// offers one of the listed tools; a hit refers to connections only when it is offered by fewer of them.
// HasMore is true exactly when another match follows this page; NextCursor is then the cursor of the
// following page and absent otherwise.
type SearchResponse struct {
	Connections []string `json:"connections,omitempty"`
	Operations  Hits     `json:"operations"`
	HasMore     bool     `json:"has_more"`
	NextCursor  string   `json:"next_cursor,omitempty"`
}

// Search performs deterministic local discovery and never resolves credentials or calls a provider. Its
// response stays bounded: an omitted, non-positive, or oversized limit becomes maxSearchResults. Pages
// follow the stable ID order of the registry, and a cursor continues after the last ID of its page, so
// reading every page yields each match exactly once.
func (c *Core) Search(request SearchRequest) (SearchResponse, error) {
	c.scope()
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
	response.Connections, response.Operations = c.hits(request, descriptors)
	return response, nil
}

// hits projects descriptors onto the entries of a discovery answer, and names the connections they share
// once: every connection that offers one of them, sorted by name. An entry keeps its own list only when a
// connection of that set does not offer it, or when it carries a reason: with a reason and without a list a
// tool is offered by none.
func (c *Core) hits(request SearchRequest, descriptors []capability.Descriptor) ([]string, Hits) {
	hits := make(Hits, 0, len(descriptors))
	offered := make([][]string, 0, len(descriptors))
	var connections []string
	seen := map[string]bool{}
	for _, descriptor := range descriptors {
		title := descriptor.Title
		if title == "" {
			title = descriptor.Description
		}
		names := c.connectionNamesFor(descriptor)
		for _, name := range names {
			if !seen[name] {
				seen[name] = true
				connections = append(connections, name)
			}
		}
		offered = append(offered, names)
		hits = append(hits, SearchHit{
			ID: descriptor.ID, Title: title, Effect: descriptor.Risk.Effect,
			Requires: strings.Join(requiredArguments(descriptor), "; "),
			Confirm:  descriptor.Risk.Confirmation == capability.ConfirmationRequired,
			Reason:   c.refusal(request, descriptor),
		})
	}
	sort.Strings(connections)
	for i := range hits {
		if hits[i].Reason == "" && len(offered[i]) == len(connections) {
			continue
		}
		hits[i].Connections = strings.Join(offered[i], " ")
	}
	return connections, hits
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
	c.scope()
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
//
// Unusable is present exactly while a listed connection cannot read its secrets from the vault: the error
// code an invoke through such a connection ends with, vault-locked while the vault is locked,
// approval-required while the vault has not approved the connection as it is configured now, or the code of
// a vault process that runs but cannot be asked, and empty for every other connection of the list.
type ConnectionSummary struct {
	Name        string  `json:"name"`
	Provider    string  `json:"provider"`
	Description string  `json:"description"`
	Permissions string  `json:"permissions"`
	Tools       string  `json:"tools"`
	Unusable    *string `json:"unusable,omitempty"`
	// Files names the local directories the connection releases, per direction; absent when none.
	Files *FilesRef `json:"files,omitempty"`
}

// ConnectionsResponse is the payload inside the CLI envelope.
type ConnectionsResponse struct {
	Connections []ConnectionSummary `json:"connections"`
}

// Connections lists the configured routes, of one provider when provider is not empty, sorted by provider
// and then by name. It answers from the configuration and, only for a listed connection that reads its
// secrets from the vault, from unusable, which reports without reading a secret why that connection cannot
// read them now, as the error its invoke would end with, or nil when it can. A nil unusable stands for a
// vault that never refuses.
func (c *Core) Connections(provider string, unusable func(*config.Resolved) error) ConnectionsResponse {
	c.scope()
	listed := func(name string) bool {
		return provider == "" || c.config.Services[c.config.Connections[name].Service].Provider == provider
	}
	reasons := map[string]string{}
	if unusable != nil {
		for name, connection := range c.config.Connections {
			if !listed(name) || c.config.Credentials[connection.Credential].Type != config.CredentialTypeVault {
				continue
			}
			resolved, err := c.connection(name)
			if err != nil {
				continue
			}
			if err := unusable(resolved); err != nil {
				reasons[name] = string(ErrorCode(err))
			}
		}
	}

	connections := make([]ConnectionSummary, 0)
	for name, connection := range c.config.Connections {
		if !listed(name) {
			continue
		}
		owner := c.config.Services[connection.Service].Provider
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
		summary := ConnectionSummary{
			Name: name, Provider: owner, Description: connection.Description,
			Permissions: strings.Join(effects, " "), Tools: tools, Files: filesRef(connection.Files),
		}
		if len(reasons) > 0 {
			reason := reasons[name]
			summary.Unusable = &reason
		}
		connections = append(connections, summary)
	}
	sort.Slice(connections, func(i, j int) bool {
		if connections[i].Provider != connections[j].Provider {
			return connections[i].Provider < connections[j].Provider
		}
		return connections[i].Name < connections[j].Name
	})
	return ConnectionsResponse{Connections: connections}
}

// ToolsResponse is the payload inside the CLI envelope: the entries and the connections of the search
// answer, without paging.
type ToolsResponse struct {
	Connections []string `json:"connections,omitempty"`
	Tools       Hits     `json:"tools"`
}

// Tools is the second step of discovery: the tools of one namespace, or of one targeted query. It applies
// the same filters as Search to the same descriptors in the same order and publishes the same entries.
//
// The catalog view answers what this installation offers, so a truncated answer would read as a complete
// one; the bounded Search response stays the contract of the request-bound agent surface.
func (c *Core) Tools(request SearchRequest) (ToolsResponse, error) {
	c.scope()
	descriptors, err := c.catalog(request, "", 0)
	if err != nil {
		return ToolsResponse{}, err
	}
	connections, tools := c.hits(request, descriptors)
	return ToolsResponse{Connections: connections, Tools: tools}, nil
}

// catalog filters the registry deterministically and returns the matching descriptors, which both discovery
// views project, so the compact index and the bounded search answer cannot disagree about which tools exist
// or in which order. Without a query the order is the registry's, sorted by ID. With one, the most relevant
// tool comes first and equal relevance keeps the ID order; see relevance. A non-empty after skips every
// descriptor up to and including that ID in this order. A limit of zero or less returns every match.
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
	type match struct {
		descriptor capability.Descriptor
		score      int
	}
	var matches []match
	for _, descriptor := range c.registry.All() {
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
		score, ok := c.relevance(request, descriptor, terms)
		if !ok {
			continue
		}
		matches = append(matches, match{descriptor, score})
	}
	if len(terms) > 0 {
		sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	}
	start := 0
	if after != "" {
		start = -1
		for i, m := range matches {
			if m.descriptor.ID == after {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return nil, &InvalidRequestError{Message: "cursor is not a next_cursor of this search; " +
				"start the search again without cursor"}
		}
	}
	descriptors := make([]capability.Descriptor, 0, len(matches)-start)
	for _, m := range matches[start:] {
		if limit > 0 && len(descriptors) == limit {
			break
		}
		descriptors = append(descriptors, m.descriptor)
	}
	return descriptors, nil
}

// Relevance weights of where a query term is found, the best place of each term counting once. The ID and
// the title name what a tool is; its description and tags say what it is for; the provider, its note, and
// the connections only say where it runs.
const (
	weightName    = 4
	weightPurpose = 2
	weightContext = 1
)

// relevance scores one tool for the terms of a query and reports whether every term occurs somewhere. The
// places are: the ID and title, the description and tags, and the description of the provider, the note
// the user keeps on that provider, and the descriptions of the connections that offer the tool, or with a
// connection filter of that connection alone. A word of a task such as "wiki" or "CRM" thus finds the
// tools of the provider or route it names, without a list of synonyms, and a tool whose title lacks the
// word but whose description has it still matches, below the tools that carry it in the title or ID.
func (c *Core) relevance(request SearchRequest, descriptor capability.Descriptor, terms []string) (int, bool) {
	metadata, _ := c.registry.ProviderMetadata(descriptor.Provider)
	name := strings.ToLower(descriptor.ID + " " + descriptor.Title)
	purpose := strings.ToLower(strings.Join(append([]string{descriptor.Description}, descriptor.Tags...), " "))
	place := []string{metadata.Description, c.config.ProviderNotes[descriptor.Provider]}
	for _, connection := range c.connectionNamesFor(descriptor) {
		if request.Connection == "" || connection == request.Connection {
			place = append(place, c.config.Connections[connection].Description)
		}
	}
	context := strings.ToLower(strings.Join(place, " "))
	score := 0
	for _, term := range terms {
		switch {
		case strings.Contains(name, term):
			score += weightName
		case strings.Contains(purpose, term):
			score += weightPurpose
		case strings.Contains(context, term):
			score += weightContext
		default:
			return 0, false
		}
	}
	return score, true
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
//
// Files is present only when the connection releases local directories, and names them per direction: Read
// for the directories tools may read files from, Write for those they may write files to. It is the one
// place discovery names a path, so a caller knows where a tool may work.
type ConnectionRef struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Files       *FilesRef `json:"files,omitempty"`
}

// FilesRef is the local directories a connection releases, as configured, per direction.
type FilesRef struct {
	Read  []string `json:"read,omitempty"`
	Write []string `json:"write,omitempty"`
}

// filesRef returns the discovery view of files, or nil when no directory is released.
func filesRef(files config.Files) *FilesRef {
	if files.Empty() {
		return nil
	}
	copied := files.Clone()
	if len(copied.Read) == 0 {
		copied.Read = nil
	}
	if len(copied.Write) == 0 {
		copied.Write = nil
	}
	return &FilesRef{Read: copied.Read, Write: copied.Write}
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
	c.scope()
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
	return DescribeResponse{Operation: PublishedDescriptor(descriptor), Connections: connections}, nil
}

// InvokeRequest is one direct, request-bound invocation. Confirmed is consumed only by this value and is
// never persisted or reused.
type InvokeRequest struct {
	Operation  string          `json:"operation"`
	Version    int             `json:"version,omitempty"`
	Connection string          `json:"connection,omitempty"`
	Arguments  json.RawMessage `json:"arguments"`
	Confirmed  bool            `json:"confirm,omitempty"`
	// Fields selects the members of the entries of the tool's result list; the provider never sees it.
	Fields []string `json:"fields,omitempty"`
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
//
// Every call, whatever it does or returns, appends one entry to the invocation log installed with
// SetInvokeLog: descriptor and resolved are declared here, before any early return, exactly so the deferred
// call below can still log the operation ID and the connection an earlier failure never reached.
func (c *Core) Invoke(ctx context.Context, request InvokeRequest) (response InvokeResponse, err error) {
	start := time.Now()
	var descriptor capability.Descriptor
	var resolved *config.Resolved
	defer func() { c.logInvoke(request, descriptor, resolved, start, err) }()
	c.scope()

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

	if err := validateFields(descriptor, request.Fields); err != nil {
		return InvokeResponse{}, err
	}

	resolved, err = c.selectConnection(request.Connection, descriptor)
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
	requestID := ""
	if descriptor.Risk.Confirmation == capability.ConfirmationRequired || descriptor.LocalFiles != "" {
		var requestErr error
		if requestID, requestErr = newRequestID(); requestErr != nil {
			return InvokeResponse{}, fmt.Errorf("create audit request ID: %w", requestErr)
		}
	}
	if descriptor.Risk.Confirmation == capability.ConfirmationRequired {
		defer func() {
			c.writeAudit(auditEvent{
				RequestID: requestID, Operation: descriptor.ID, Connection: resolved.Name,
				Confirmed: request.Confirmed, Result: auditResult(err), Time: time.Now().UTC(),
			})
		}()
	}
	// The secrets of the handler are resolved for the selected connection alone: an encrypted vault hands
	// them out only when it approved that connection as it is configured now.
	handlerCtx := secret.ForConnection(ctx, resolved)
	// The confirmation reaches the handler only from the request itself, also where the tool needs none of
	// its own: a tool with local file access asks for it before it replaces a file.
	if request.Confirmed {
		handlerCtx = capability.WithConfirmed(handlerCtx)
	}
	if descriptor.LocalFiles != "" {
		handlerCtx = capability.WithReplacedReporter(handlerCtx, func() {
			c.writeReplacedAudit(requestID, descriptor.ID, resolved.Name)
		})
	}
	value, err := handler(handlerCtx, resolved, c.secrets, c.redactor, request.Arguments)
	if err != nil {
		return InvokeResponse{}, mapLocalFileError(descriptor, err)
	}
	normalized, err := normalize(value)
	if err != nil {
		return InvokeResponse{}, &InvalidProviderResponseError{Operation: descriptor.ID}
	}
	normalized = redactValue(c.redactor, normalized)
	if err := validateValue(descriptor.OutputSchema, normalized); err != nil {
		return InvokeResponse{}, &InvalidProviderResponseError{Operation: descriptor.ID}
	}
	// Only what passed the output schema is compacted: empty values are dropped afterwards, for every
	// provider alike, so CLI and MCP publish the same result.
	result, err := json.Marshal(dropEmpty(selectFields(descriptor, request.Fields,
		dropImplied(descriptor, request.Arguments, normalized))))
	if err != nil {
		return InvokeResponse{}, &InvalidProviderResponseError{Operation: descriptor.ID}
	}
	return InvokeResponse{
		Operation: descriptor.ID, Version: descriptor.Version, Connection: resolved.Name, Result: result,
	}, nil
}

// logInvoke appends one invocation log entry for a completed call to Invoke, whatever it did or returned.
// descriptor and resolved are zero where the failure happened before they were resolved, in which case the
// entry falls back to what the request itself asked for, so an unknown operation or connection still gets a
// row instead of none. A log failure is reported as a warning through the same audit writer the CLI and the
// MCP broker already flush to their stderr, and never changes err or response themselves; so is an entry
// written without a check value only because the writer that would have signed it failed (see
// invokelog.UnsignedError).
func (c *Core) logInvoke(request InvokeRequest, descriptor capability.Descriptor, resolved *config.Resolved,
	start time.Time, err error) {
	if c.invokeLog == nil {
		return
	}
	fields := invokelog.Fields{
		Path: c.invokePath, Client: c.invokeClient, Operation: request.Operation, Version: request.Version,
		Connection: request.Connection, Result: auditResult(err), Duration: time.Since(start),
	}
	if descriptor.ID != "" {
		fields.Operation, fields.Version, fields.Effect = descriptor.ID, descriptor.Version, string(descriptor.Risk.Effect)
	}
	if resolved != nil {
		fields.Connection = resolved.Name
	}
	var unsigned *invokelog.UnsignedError
	switch logErr := c.invokeLog.Append(fields); {
	case logErr == nil:
	case errors.As(logErr, &unsigned):
		c.writeAuditText(fmt.Sprintf("qatlas: warning: the invocation log entry was written without a check value: %v",
			unsigned.Err))
	default:
		c.writeAuditText(fmt.Sprintf("qatlas: warning: could not write the invocation log: %v", logErr))
	}
}

// writeAuditText appends one warning line to the audit writer, beside the JSON audit event a confirmed
// mutation may also write there. Both share the writer because both are diagnostics the CLI and the MCP
// broker flush to stderr in the same place, under the same redaction, after the same call to Invoke.
func (c *Core) writeAuditText(line string) {
	if c.audit == nil {
		return
	}
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	_, _ = io.WriteString(c.audit, line)
}

type auditEvent struct {
	RequestID  string    `json:"request_id"`
	Operation  string    `json:"operation"`
	Connection string    `json:"connection"`
	Confirmed  bool      `json:"confirmed"`
	Result     string    `json:"result"`
	Time       time.Time `json:"time"`
}

// replacedAuditEvent records that a confirmed request replaced an existing local file. It names neither the
// file nor its directory.
type replacedAuditEvent struct {
	Event      string    `json:"event"`
	RequestID  string    `json:"request_id"`
	Operation  string    `json:"operation"`
	Connection string    `json:"connection"`
	Time       time.Time `json:"time"`
}

// auditEventLocalFileReplaced is the kind of a replacedAuditEvent.
const auditEventLocalFileReplaced = "local-file-replaced"

func (c *Core) writeReplacedAudit(requestID, operation, connection string) {
	if c.audit == nil {
		return
	}
	_ = json.NewEncoder(c.audit).Encode(replacedAuditEvent{
		Event: auditEventLocalFileReplaced, RequestID: requestID, Operation: operation,
		Connection: connection, Time: time.Now().UTC(),
	})
}

// mapLocalFileError turns the failures of the local file access into the errors of the core, so they carry
// the provider-independent codes. Any other error passes unchanged. None of the messages names a path.
func mapLocalFileError(descriptor capability.Descriptor, err error) error {
	var (
		pathErr      *localfile.PathError
		integrityErr *localfile.IntegrityError
	)
	switch {
	case errors.Is(err, localfile.ErrOverwriteNeedsConfirmation):
		return &ConfirmationRequiredError{Operation: descriptor.ID, Overwrite: true}
	case errors.As(err, &pathErr):
		return &InvalidRequestError{Message: pathErr.Error()}
	case errors.As(err, &integrityErr):
		return &InvalidProviderResponseError{Operation: descriptor.ID, Reason: integrityErr.Reason}
	}
	return err
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
	// The tool default wins over the provider default; a default that names a connection the tool cannot use
	// is refused, never skipped for the next stage. The view of the current project already leaves out a
	// default that names a connection bound to another project.
	for _, key := range []string{descriptor.ID, descriptor.Provider} {
		name := c.config.Defaults.Connections[key]
		if name == "" {
			continue
		}
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
		return nil, &capability.UnknownConnectionError{
			Name: name, Suggestion: Suggest(name, names), MayBeBound: c.all.UsesPaths(),
		}
	}
	service := c.config.Services[connection.Service]
	return &config.Resolved{
		Name: name, Provider: service.Provider, BaseURL: c.config.ServiceBaseURL(service), Options: service.Options,
		Target: connection.Target, Targets: append([]string(nil), connection.Targets...),
		Service: connection.Service, Credential: connection.Credential,
		Secrets:     c.config.Credentials[connection.Credential],
		Permissions: c.config.ConnectionPermissions(name),
		Tools:       connection.ToolsList(),
		Paths:       append([]string(nil), connection.Paths...),
		Files:       connection.Files.Clone(),
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
		case config.RefusalNoLocalFiles:
			if closest == config.RefusalEffect {
				closest = config.RefusalNoLocalFiles
			}
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

// connectionRef names one route with its description. The description is redacted here already, so a
// diagnostic that shortens it can never cut a known secret into a part the redactor no longer recognizes.
func (c *Core) connectionRef(name string) ConnectionRef {
	description := c.config.Connections[name].Description
	if c.redactor != nil {
		description = c.redactor.Apply(description)
	}
	files := filesRef(c.config.Connections[name].Files)
	if files != nil && c.redactor != nil {
		for _, list := range [][]string{files.Read, files.Write} {
			for i := range list {
				list[i] = c.redactor.Apply(list[i])
			}
		}
	}
	return ConnectionRef{Name: name, Description: description, Files: files}
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
