package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/helptopics"
	"github.com/castrowithcee/qatlas-cli/internal/output"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
)

const (
	mcpProtocolVersion    = "2026-07-28"
	mcpCacheTTLMillis     = 60 * 60 * 1000
	maxMCPMessageBytes    = 1 << 20
	maxMCPInFlight        = 16
	mcpParseError         = -32700
	mcpInvalidRequest     = -32600
	mcpMethodNotFound     = -32601
	mcpInvalidParams      = -32602
	mcpInternalError      = -32603
	mcpUnsupportedVersion = -32022
)

// mcpLegacyVersions are the handshake-based protocol versions an initialize request negotiates, newest
// first. Older ones lack structuredContent, which carries the machine-readable part of a refusal.
var mcpLegacyVersions = []string{"2025-11-25", "2025-06-18"}

// mcpVersionHint names every way to reach this server, for diagnostics a client may only be able to show.
var mcpVersionHint = "this server speaks MCP " + mcpProtocolVersion + " with per-request params._meta, or MCP " +
	strings.Join(mcpLegacyVersions, " or ") + " after an initialize request"

var errMCPMessageTooLarge = errors.New("MCP message exceeds size limit")

func newMCPCommand(opts *Options, registry *capability.Registry) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve the fixed agent tools over MCP stdio",
		Long: "The server offers the fixed MCP tools qatlas.search, qatlas.describe, and qatlas.invoke\n" +
			"over stdio, one JSON-RPC message per line. It speaks MCP 2026-07-28, where every request\n" +
			"declares io.modelcontextprotocol/protocolVersion and io.modelcontextprotocol/clientCapabilities\n" +
			"in params._meta, and MCP 2025-11-25 and 2025-06-18, which an initialize request negotiates\n" +
			"once for the process; initialize answers any other version with 2025-11-25. server/discover and\n" +
			"the initialize result return the guide 'qatlas agents' prints as their instructions, so a\n" +
			"client that only speaks MCP reads the same guide. qatlas.describe and qatlas.invoke take the\n" +
			"tool ID as operation. Arguments outside a tool's input schema fail with invalid-request naming\n" +
			"the field, such as '$.tool is not allowed'.\n\n" +
			"qatlas.describe returns the compact contract 'qatlas describe' prints as operation, beside the\n" +
			"connections that can run it; full set to true returns the complete descriptor with both\n" +
			"schemas instead, as 'qatlas describe --full' does.\n\n" +
			"qatlas.search answers from the local configuration alone: no provider is contacted and no\n" +
			"secret is read. Like 'qatlas tools' it returns only the tools a configured connection offers,\n" +
			"or with connection the tools that connection offers; all set to true adds the others, each\n" +
			"with the reason 'qatlas tools --all' names. It filters by query, provider, connection, and\n" +
			"effect and returns at most limit tools in stable ID order; an omitted, non-positive, or larger\n" +
			"limit becomes 50. The response carries the tools as operations, has_more, which is true\n" +
			"exactly when another match follows, and next_cursor, which is present only then. Each tool is\n" +
			"the entry 'qatlas tools' prints: id, title, effect, and connections, here a list of the\n" +
			"connection names that offer it, plus reason with all; qatlas.describe returns the rest.\n" +
			"Passing next_cursor back as cursor with the same filters returns the following page; a\n" +
			"request without cursor returns the first. A cursor that is malformed or belongs to other\n" +
			"filters fails with invalid-request.\n\n" +
			"list turns qatlas.search into an overview of what it searches: list providers returns the\n" +
			"providers 'qatlas providers' lists, and list connections the configured connections 'qatlas\n" +
			"connections' lists, of provider when given, as the same data in the same order. Beside list only\n" +
			"provider is allowed, and only with connections; any other argument fails with invalid-request.\n\n" +
			"A failed tool call is a result with isError set to true whose text is '<code>: <message>' and\n" +
			"whose structuredContent is an object with at least code and message, such as\n" +
			"{\"code\":\"unknown-operation\",\"message\":\"...\"}. Some codes add fields. A qatlas.invoke\n" +
			"refused with connection-ambiguous adds operation and connections: every candidate route with its\n" +
			"name and its description, which is empty where none is maintained. Nothing is chosen for the\n" +
			"caller; the next request names one of them as connection. A qatlas.invoke refused with\n" +
			"connection-selection adds the same fields; connections names the routes that offer the tool, and\n" +
			"is empty when none does. The text names the same candidates, each with its description shortened\n" +
			"to 80 characters, for a client that shows only the text. A request refused with\n" +
			"unsupported-capability adds operation, connection, and reason. Where a message points to\n" +
			"discovery, it names qatlas.search or qatlas.describe instead of a command.",
		Args: noArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			// Nobody watches the requests of a server, so the keyring must not wait for an unlock prompt.
			opts.unattended = true
			server := newMCPServer(opts, registry, c.OutOrStdout(), c.ErrOrStderr())
			return server.serve(c.Context(), c.InOrStdin())
		},
	}
}

type mcpServer struct {
	opts     *Options
	registry *capability.Registry
	stdout   io.Writer
	stderr   io.Writer
	timeout  time.Duration

	// legacy is the protocol version an initialize request negotiated, or empty. It is only touched by
	// the reading loop.
	legacy string

	coreMu   sync.Mutex
	outMu    sync.Mutex
	errMu    sync.Mutex
	mu       sync.Mutex
	pending  map[string]*mcpPending
	wg       sync.WaitGroup
	writeErr error
}

type mcpPending struct {
	cancel    context.CancelFunc
	cancelled bool
}

func newMCPServer(opts *Options, registry *capability.Registry, stdout, stderr io.Writer) *mcpServer {
	if opts.Redactor == nil {
		opts.Redactor = &redact.Redactor{}
	}
	return &mcpServer{
		opts: opts, registry: registry, stdout: stdout, stderr: stderr,
		timeout: invokeTimeout, pending: make(map[string]*mcpPending),
	}
}

func (s *mcpServer) serve(ctx context.Context, input io.Reader) error {
	reader := bufio.NewReader(input)
	for {
		line, err := readMCPLine(reader)
		switch {
		case err == nil:
			s.handle(ctx, line)
		case errors.Is(err, io.EOF):
			s.wg.Wait()
			return s.outputError()
		case errors.Is(err, errMCPMessageTooLarge):
			s.writeResponse(mcpErrorResponse(nil, mcpParseError,
				fmt.Sprintf("MCP message exceeds %d bytes", maxMCPMessageBytes), nil))
		default:
			s.cancelAll()
			s.wg.Wait()
			return fmt.Errorf("read MCP stdin: %w", err)
		}
	}
}

func readMCPLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	tooLarge := false
	for {
		fragment, prefix, err := reader.ReadLine()
		if len(line)+len(fragment) > maxMCPMessageBytes {
			tooLarge = true
		} else if !tooLarge {
			line = append(line, fragment...)
		}
		if err != nil {
			if errors.Is(err, io.EOF) && (len(line) > 0 || tooLarge) {
				if tooLarge {
					return nil, errMCPMessageTooLarge
				}
				return line, nil
			}
			return nil, err
		}
		if !prefix {
			if tooLarge {
				return nil, errMCPMessageTooLarge
			}
			return line, nil
		}
	}
}

type mcpMessage struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage
}

func (s *mcpServer) handle(parent context.Context, line []byte) {
	message, code, text := decodeMCPMessage(line)
	if code != 0 {
		// JSON-RPC notifications never receive a response, including when their params are malformed.
		if len(message.ID) == 0 && message.Method != "" {
			return
		}
		s.writeResponse(mcpErrorResponse(message.ID, code, text, nil))
		return
	}
	if len(message.ID) == 0 {
		if message.Method == "notifications/cancelled" {
			s.cancel(message.Params)
		}
		return
	}
	if message.Method == "initialize" {
		s.writeResponse(s.initialize(message))
		return
	}

	// After initialize, a request without a declared protocol version belongs to the negotiated legacy
	// session; one that declares a version is served per request, as if no session existed.
	if s.legacy == "" || declaresProtocolVersion(message.Params) {
		requested, err := requestProtocolVersion(message.Params)
		if err != nil {
			s.writeResponse(mcpErrorResponse(message.ID, mcpInvalidParams, err.Error(), nil))
			return
		}
		if requested != mcpProtocolVersion {
			s.writeResponse(mcpErrorResponse(message.ID, mcpUnsupportedVersion,
				"Unsupported protocol version", map[string]any{
					"supported": []string{mcpProtocolVersion}, "requested": requested,
				}))
			return
		}
	}

	switch message.Method {
	case "ping":
		if err := onlyParams(message.Params, "_meta"); err != nil {
			s.writeResponse(mcpErrorResponse(message.ID, mcpInvalidParams, err.Error(), nil))
			return
		}
		s.writeResponse(mcpResultResponse(message.ID, map[string]any{}))
	case "server/discover":
		if err := onlyParams(message.Params, "_meta"); err != nil {
			s.writeResponse(mcpErrorResponse(message.ID, mcpInvalidParams, err.Error(), nil))
			return
		}
		s.writeResponse(mcpResultResponse(message.ID, map[string]any{
			"resultType":        "complete",
			"supportedVersions": []string{mcpProtocolVersion},
			"capabilities":      map[string]any{"tools": map[string]any{}},
			"ttlMs":             mcpCacheTTLMillis,
			"cacheScope":        "public",
			"instructions":      helptopics.Agents().Text,
			"_meta":             mcpServerMeta(),
		}))
	case "tools/list":
		if err := onlyParams(message.Params, "_meta", "cursor"); err != nil {
			s.writeResponse(mcpErrorResponse(message.ID, mcpInvalidParams, err.Error(), nil))
			return
		}
		if cursorPresent(message.Params) {
			s.writeResponse(mcpErrorResponse(message.ID, mcpInvalidParams, "pagination cursor is not supported", nil))
			return
		}
		s.writeResponse(mcpResultResponse(message.ID, map[string]any{
			"resultType": "complete", "tools": mcpTools(),
			"ttlMs": mcpCacheTTLMillis, "cacheScope": "public", "_meta": mcpServerMeta(),
		}))
	case "tools/call":
		s.startToolCall(parent, message)
	default:
		s.writeResponse(mcpErrorResponse(message.ID, mcpMethodNotFound, "Method not found", nil))
	}
}

func decodeMCPMessage(line []byte) (mcpMessage, int, string) {
	var fields map[string]json.RawMessage
	if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &fields) != nil || fields == nil {
		return mcpMessage{}, mcpParseError, "Parse error: invalid JSON"
	}
	var rpcVersion, method string
	if json.Unmarshal(fields["jsonrpc"], &rpcVersion) != nil || rpcVersion != "2.0" ||
		json.Unmarshal(fields["method"], &method) != nil || method == "" {
		return mcpMessage{ID: validMCPID(fields["id"])}, mcpInvalidRequest, "Invalid Request"
	}
	id := fields["id"]
	if len(id) > 0 && len(validMCPID(id)) == 0 {
		return mcpMessage{}, mcpInvalidRequest, "Invalid Request"
	}
	params := fields["params"]
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(params, &object) != nil || object == nil {
		return mcpMessage{ID: id, Method: method}, mcpInvalidParams, "params must be an object"
	}
	return mcpMessage{ID: id, Method: method, Params: params}, 0, ""
}

func validMCPID(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return raw
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var number json.Number
	if decoder.Decode(&number) == nil {
		return raw
	}
	return nil
}

// initialize negotiates a legacy protocol version for the rest of the process: the requested one when this
// server speaks it, otherwise the newest it speaks, which the client may decline by disconnecting.
func (s *mcpServer) initialize(message mcpMessage) mcpResponse {
	var params struct {
		ProtocolVersion json.RawMessage `json:"protocolVersion"`
	}
	var requested string
	if json.Unmarshal(message.Params, &params) != nil ||
		json.Unmarshal(params.ProtocolVersion, &requested) != nil || requested == "" {
		return mcpErrorResponse(message.ID, mcpInvalidParams,
			"params.protocolVersion must be a non-empty string; "+mcpVersionHint, nil)
	}
	s.legacy = mcpLegacyVersions[0]
	for _, known := range mcpLegacyVersions {
		if requested == known {
			s.legacy = known
		}
	}
	return mcpResultResponse(message.ID, map[string]any{
		"protocolVersion": s.legacy,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]string{"name": "qatlas", "version": version},
		"instructions":    helptopics.Agents().Text,
	})
}

func declaresProtocolVersion(raw json.RawMessage) bool {
	var params struct {
		Meta map[string]json.RawMessage `json:"_meta"`
	}
	_ = json.Unmarshal(raw, &params)
	_, ok := params.Meta["io.modelcontextprotocol/protocolVersion"]
	return ok
}

func requestProtocolVersion(raw json.RawMessage) (string, error) {
	var params map[string]json.RawMessage
	if json.Unmarshal(raw, &params) != nil {
		return "", errors.New("params must be an object")
	}
	var meta map[string]json.RawMessage
	if json.Unmarshal(params["_meta"], &meta) != nil || meta == nil {
		return "", errors.New(`params._meta must be an object declaring "io.modelcontextprotocol/protocolVersion" ` +
			`and "io.modelcontextprotocol/clientCapabilities"; ` + mcpVersionHint)
	}
	var protocol string
	if json.Unmarshal(meta["io.modelcontextprotocol/protocolVersion"], &protocol) != nil || protocol == "" {
		return "", errors.New(`params._meta["io.modelcontextprotocol/protocolVersion"] must be a non-empty ` +
			`string; ` + mcpVersionHint)
	}
	var capabilities map[string]json.RawMessage
	if json.Unmarshal(meta["io.modelcontextprotocol/clientCapabilities"], &capabilities) != nil || capabilities == nil {
		return "", errors.New(`params._meta["io.modelcontextprotocol/clientCapabilities"] must be an object`)
	}
	if info, ok := meta["io.modelcontextprotocol/clientInfo"]; ok {
		var client struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		if json.Unmarshal(info, &client) != nil || client.Name == "" || client.Version == "" {
			return "", errors.New(`params._meta["io.modelcontextprotocol/clientInfo"] must be an object with ` +
				`a non-empty name and version`)
		}
	}
	return protocol, nil
}

func onlyParams(raw json.RawMessage, allowed ...string) error {
	var params map[string]json.RawMessage
	if json.Unmarshal(raw, &params) != nil {
		return errors.New("params must be an object")
	}
	set := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		set[name] = true
	}
	for name := range params {
		if !set[name] {
			return fmt.Errorf("unknown parameter %q", name)
		}
	}
	return nil
}

func cursorPresent(raw json.RawMessage) bool {
	var params map[string]json.RawMessage
	_ = json.Unmarshal(raw, &params)
	cursor, ok := params["cursor"]
	return ok && len(bytes.TrimSpace(cursor)) > 0 && !bytes.Equal(bytes.TrimSpace(cursor), []byte(`""`))
}

func (s *mcpServer) startToolCall(parent context.Context, message mcpMessage) {
	var params struct {
		Name           string          `json:"name"`
		Arguments      json.RawMessage `json:"arguments"`
		InputResponses json.RawMessage `json:"inputResponses"`
		RequestState   string          `json:"requestState"`
		Meta           json.RawMessage `json:"_meta"`
	}
	decoder := json.NewDecoder(bytes.NewReader(message.Params))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&params) != nil || params.Name == "" || len(params.InputResponses) > 0 || params.RequestState != "" {
		s.writeResponse(mcpErrorResponse(message.ID, mcpInvalidParams, "invalid tool call parameters", nil))
		return
	}
	if !knownMCPTool(params.Name) {
		s.writeResponse(mcpErrorResponse(message.ID, mcpInvalidParams, "Unknown tool: "+params.Name, nil))
		return
	}
	if len(params.Arguments) == 0 {
		params.Arguments = json.RawMessage(`{}`)
	}
	var arguments map[string]json.RawMessage
	if json.Unmarshal(params.Arguments, &arguments) != nil || arguments == nil {
		s.writeResponse(mcpErrorResponse(message.ID, mcpInvalidParams, "tool arguments must be an object", nil))
		return
	}

	key := string(bytes.TrimSpace(message.ID))
	requestContext, cancel := context.WithTimeout(parent, s.timeout)
	pending := &mcpPending{cancel: cancel}
	s.mu.Lock()
	if _, exists := s.pending[key]; exists {
		s.mu.Unlock()
		cancel()
		s.writeResponse(mcpErrorResponse(message.ID, mcpInvalidRequest, "request ID is already in progress", nil))
		return
	}
	if len(s.pending) >= maxMCPInFlight {
		s.mu.Unlock()
		cancel()
		s.writeResponse(mcpErrorResponse(message.ID, mcpInternalError, "too many in-flight requests", nil))
		return
	}
	s.pending[key] = pending
	s.wg.Add(1)
	s.mu.Unlock()

	go func() {
		defer s.wg.Done()
		defer cancel()
		result, audit, err := s.callTool(requestContext, params.Name, params.Arguments)
		s.writeAudit(audit)
		if err != nil && errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			err = pastDeadline(err, s.timeout)
		}
		response := mcpResultResponse(message.ID, toolResult(result, err, s.opts.Redactor))

		s.mu.Lock()
		if !pending.cancelled {
			s.writeResponse(response)
		}
		delete(s.pending, key)
		s.mu.Unlock()
	}()
}

func (s *mcpServer) callTool(ctx context.Context, name string, raw json.RawMessage) (any, []byte, error) {
	var audit bytes.Buffer
	// The published input schema is the contract; checking it first names the offending field.
	for _, tool := range mcpTools() {
		if tool.Name == name {
			if err := application.ValidateJSON(tool.InputSchema, raw); err != nil {
				return nil, nil, &application.InvalidRequestError{Message: err.Error()}
			}
		}
	}
	s.coreMu.Lock()
	core, err := applicationCore(s.opts, s.registry, name == "qatlas.invoke")
	s.coreMu.Unlock()
	if err != nil {
		return nil, nil, err
	}

	switch name {
	case "qatlas.search":
		var request struct {
			application.SearchRequest
			List string `json:"list"`
		}
		if err := decodeMCPArguments(raw, &request); err != nil {
			return nil, nil, err
		}
		if request.List != "" {
			response, err := s.list(core, raw, request.List, request.Provider)
			return response, nil, err
		}
		response, err := core.Search(request.SearchRequest)
		return response, nil, err
	case "qatlas.describe":
		var request application.DescribeRequest
		if err := decodeMCPArguments(raw, &request); err != nil {
			return nil, nil, err
		}
		response, err := core.Describe(request)
		if err != nil || request.Full {
			return response, nil, err
		}
		return response.Compact(), nil, nil
	case "qatlas.invoke":
		var request application.InvokeRequest
		if err := decodeMCPArguments(raw, &request); err != nil {
			return nil, nil, err
		}
		core.SetAudit(&audit)
		response, err := core.Invoke(ctx, request)
		return response, audit.Bytes(), err
	default:
		return nil, nil, fmt.Errorf("unknown MCP tool")
	}
}

// list answers the overview mode of qatlas.search: the providers 'qatlas providers' lists, or the
// connections 'qatlas connections [provider]' lists, as the same data. It is a mode of its own, so the
// arguments of the tool search are refused beside it, and provider is only the filter of connections.
func (s *mcpServer) list(core *application.Core, raw json.RawMessage, list, provider string) (any, error) {
	var arguments map[string]json.RawMessage
	_ = json.Unmarshal(raw, &arguments)
	names := make([]string, 0, len(arguments))
	for name := range arguments {
		if name != "list" && (name != "provider" || list != "connections") {
			names = append(names, name)
		}
	}
	if len(names) > 0 {
		sort.Strings(names)
		return nil, &application.InvalidRequestError{Message: fmt.Sprintf("$.%s is not allowed with list %s",
			names[0], list)}
	}
	if list == "providers" {
		return core.Providers(), nil
	}
	if provider != "" {
		if _, ok := s.registry.ProviderMetadata(provider); !ok {
			return nil, &application.InvalidRequestError{Message: fmt.Sprintf("unknown provider %q", provider) +
				application.DidYouMean(application.Suggest(provider, application.ProviderIDs(s.registry))) +
				"; leave the provider out to list every connection"}
		}
	}
	return core.Connections(provider), nil
}

func decodeMCPArguments(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &application.InvalidRequestError{Message: "tool arguments do not satisfy the request contract"}
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return &application.InvalidRequestError{Message: "tool arguments must contain exactly one object"}
	}
	return nil
}

func (s *mcpServer) cancel(raw json.RawMessage) {
	var params struct {
		RequestID json.RawMessage `json:"requestId"`
		Reason    string          `json:"reason"`
		Meta      json.RawMessage `json:"_meta"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&params) != nil || len(validMCPID(params.RequestID)) == 0 {
		return
	}
	key := string(bytes.TrimSpace(params.RequestID))
	s.mu.Lock()
	if pending := s.pending[key]; pending != nil && !pending.cancelled {
		pending.cancelled = true
		pending.cancel()
	}
	s.mu.Unlock()
}

func (s *mcpServer) cancelAll() {
	s.mu.Lock()
	for _, pending := range s.pending {
		pending.cancelled = true
		pending.cancel()
	}
	s.mu.Unlock()
}

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

func mcpTools() []mcpTool {
	return []mcpTool{
		{
			Name: "qatlas.search",
			Description: "Search the configured tool catalog. Returns the tools a configured connection offers " +
				"as operations, at most limit of them in stable ID order, each with id, title, effect, and the " +
				"connections that offer it; all adds the others with the reason no connection offers them. " +
				"qatlas.describe returns the rest of a contract. has_more is true exactly when another match " +
				"follows, and next_cursor, passed back as cursor with the same filters, returns the following page. " +
				"list returns an overview instead: providers lists every provider with its description, note, " +
				"and counts of tools, connections that can run them, and configured connections; connections " +
				"lists the configured connections, of provider when given, each with its description, " +
				"permitted effects, and tools list. list takes no other argument than provider with connections.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"list":{"type":"string","enum":["providers","connections"],"description":"Return the providers or the configured connections instead of tools; only provider may accompany connections"},"query":{"type":"string"},"provider":{"type":"string"},"connection":{"type":"string"},"effect":{"type":"string","enum":["read","create","update","delete","execute"]},"all":{"type":"boolean","description":"Also return the tools no connection offers, each with its reason"},"limit":{"type":"integer","description":"Page size; omitted, non-positive, or larger values become 50"},"cursor":{"type":"string","description":"Opaque next_cursor of a previous page with the same filters; the first page when omitted"}},"additionalProperties":false}`),
		},
		{
			Name: "qatlas.describe",
			Description: "Describe one versioned tool contract; operation is the tool ID. Returns the compact " +
				"contract: arguments and result fields as rows derived from the schemas, risk, examples, and the " +
				"connections that can run it; full returns the complete descriptor with both schemas.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"operation":{"type":"string"},"version":{"type":"integer"},"connection":{"type":"string"},"full":{"type":"boolean","description":"Return the complete descriptor with the input and output schema instead of the compact contract"}},"required":["operation"],"additionalProperties":false}`),
		},
		{
			Name:        "qatlas.invoke",
			Description: "Invoke one tool through a configured connection; operation is the tool ID",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"operation":{"type":"string"},"version":{"type":"integer"},"connection":{"type":"string"},"arguments":{"type":"object"},"confirm":{"type":"boolean"}},"required":["operation"],"additionalProperties":false}`),
		},
	}
}

func knownMCPTool(name string) bool {
	return name == "qatlas.search" || name == "qatlas.describe" || name == "qatlas.invoke"
}

func mcpServerMeta() map[string]any {
	return map[string]any{
		"io.modelcontextprotocol/serverInfo": map[string]string{"name": "qatlas", "version": version},
	}
}

func toolResult(data any, err error, redactor *redact.Redactor) map[string]any {
	result := map[string]any{"resultType": "complete", "_meta": mcpServerMeta()}
	if err != nil {
		err = withNextStep(err, routeMCP)
		code := codeFor(err)
		message := redactor.Error(err)
		detail := errorDetailFor(err, redactor)
		if detail == nil {
			// Every refusal carries at least its code and message, so a caller branches on structuredContent
			// alone. The CLI keeps writing a detail only where errorDetailFor adds something.
			detail = map[string]string{"code": string(code), "message": message}
		}
		result["content"] = []map[string]string{{"type": "text", "text": string(code) + ": " + message}}
		result["structuredContent"] = detail
		result["isError"] = true
		return result
	}
	encoded, marshalErr := json.Marshal(data)
	if marshalErr != nil {
		result["content"] = []map[string]string{{"type": "text", "text": "runtime: encode tool result"}}
		result["structuredContent"] = map[string]string{"code": string(output.CodeRuntime), "message": "encode tool result"}
		result["isError"] = true
		return result
	}
	result["content"] = []map[string]string{{"type": "text", "text": string(encoded)}}
	result["structuredContent"] = data
	result["isError"] = false
	return result
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func mcpResultResponse(id json.RawMessage, result any) mcpResponse {
	return mcpResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func mcpErrorResponse(id json.RawMessage, code int, message string, data any) mcpResponse {
	if len(id) == 0 {
		id = json.RawMessage(`null`)
	}
	return mcpResponse{JSONRPC: "2.0", ID: id, Error: &mcpRPCError{Code: code, Message: message, Data: data}}
}

func (s *mcpServer) writeResponse(response mcpResponse) {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	if s.writeErr == nil {
		s.writeErr = json.NewEncoder(s.stdout).Encode(response)
	}
}

func (s *mcpServer) outputError() error {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	if s.writeErr != nil {
		return fmt.Errorf("write MCP stdout: %w", s.writeErr)
	}
	return nil
}

func (s *mcpServer) writeAudit(audit []byte) {
	if len(bytes.TrimSpace(audit)) == 0 {
		return
	}
	s.errMu.Lock()
	defer s.errMu.Unlock()
	writeAudit(s.stderr, audit, s.opts.Redactor)
}
