package cli

import (
	"encoding/json"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/application"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/invokelog"
)

// The invocation log of an MCP server records one entry per exchange of the baseline (initialize,
// server/discover, tools/list), the navigation (qatlas.search, qatlas.describe), and the invokes, each with
// the byte lengths of the JSON-RPC request line and response message. Both lengths exclude the line feed
// that ends a message, whichever way it went. An entry is appended only after the response was written, so
// a logging failure, which is a warning on stderr, can neither change nor delay a response.

// mcpExchange is what the log needs to know of one request, fixed when it was read.
type mcpExchange struct {
	start    time.Time
	reqBytes int
	client   *invokelog.ClientInfo
	// area is empty for an exchange that is not logged. operation is the reserved operation of a baseline or
	// navigation exchange; an invoke has none here, because the log never takes one from the client.
	area      invokelog.Area
	operation string
}

// classify decides whether the exchange is logged and under which area. Only the requests of a method this
// server answers with the baseline, and the three tools, are: ping, notifications, replies, unknown methods,
// and unknown tools are not.
func (e *mcpExchange) classify(message mcpMessage) {
	switch message.Method {
	case "initialize", "server/discover":
		// server/discover answers with the same instructions initialize does, so it counts as its baseline.
		e.area, e.operation = invokelog.AreaBaseline, invokelog.OperationMCPInitialize
	case "tools/list":
		e.area, e.operation = invokelog.AreaBaseline, invokelog.OperationMCPToolsList
	case "tools/call":
		var params struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(message.Params, &params)
		switch params.Name {
		case "qatlas.search":
			e.area, e.operation = invokelog.AreaNavigation, invokelog.OperationMCPSearch
		case "qatlas.describe":
			e.area, e.operation = invokelog.AreaNavigation, invokelog.OperationMCPDescribe
		case "qatlas.invoke":
			e.area = invokelog.AreaInvoke
		}
	}
}

// mcpCall carries what one tool call hands from the core to the log.
type mcpCall struct {
	// core holds the fields the application core logged for a qatlas.invoke that reached it.
	core *invokelog.Fields
	// dir locates the log once the configuration was loaded; it is empty before.
	dir string
}

// Append implements invokelog.Writer: the core hands over its entry, which is appended once the response is
// written.
func (c *mcpCall) Append(f invokelog.Fields) error {
	c.core = &f
	return nil
}

// reply writes a response and logs the exchange it answers, for exchanges that are answered on the reading
// loop. Tool calls that run in their own goroutine log through logToolCall.
func (s *mcpServer) reply(ex *mcpExchange, response mcpResponse) {
	n, ok := s.writeResponse(response)
	if !ok || ex.area == "" {
		return
	}
	resp := int64(n)
	s.appendEntry(ex, nil, invokelog.Fields{
		Path: "mcp", Client: ex.client, Operation: ex.operation, Result: rpcResult(response),
		Duration: time.Since(ex.start),
	}, &resp)
}

// logToolCall logs a tool call after its response was written, or not, where the call was cancelled. A
// cancelled qatlas.invoke that reached the core still ran, so it keeps its entry, without resp_bytes; any
// other cancelled call had no response and gets none.
func (s *mcpServer) logToolCall(ex *mcpExchange, call *mcpCall, err error, n int, written bool) {
	if ex.area == "" {
		return
	}
	var fields invokelog.Fields
	switch {
	case call.core != nil:
		fields = *call.core
	case written:
		fields = invokelog.Fields{Path: "mcp", Client: ex.client, Operation: ex.operation, Result: "success",
			Duration: time.Since(ex.start)}
		if err != nil {
			fields.Result = string(codeFor(err))
		}
	default:
		return
	}
	var resp *int64
	if written {
		resp = new(int64)
		*resp = int64(n)
	}
	s.appendEntry(ex, call, fields, resp)
}

// appendEntry completes fields with the session and the message sizes and appends them.
func (s *mcpServer) appendEntry(ex *mcpExchange, call *mcpCall, fields invokelog.Fields, resp *int64) {
	if s.session == "" {
		return
	}
	// A call that never resolved to a tool carries the name the client chose. It must not be able to take a
	// reserved name, or a client could forge baseline and navigation entries.
	if ex.area == invokelog.AreaInvoke && invokelog.IsReservedOperation(fields.Operation) {
		fields.Operation, fields.Version = "", 0
	}
	req := int64(ex.reqBytes)
	fields.Session, fields.ReqBytes, fields.RespBytes = s.session, &req, resp

	var dir string
	if call != nil && call.dir != "" {
		dir = call.dir
	} else {
		// Without a loadable configuration the exchange stays unlogged, as it always did.
		path, err := config.Path(s.opts.Config)
		if err != nil {
			return
		}
		if _, err := config.Load(path, s.registry); err != nil {
			return
		}
		dir = invokeLogDir(path)
	}
	// The log is appended to without holding any lock of the server but the one that guards the resolver:
	// an append takes a file lock and may ask the vault process.
	s.coreMu.Lock()
	writer := newInvokeLogWriter(s.opts, dir)
	s.coreMu.Unlock()
	if warning := application.InvokeLogWarning(writer.Append(fields)); warning != "" {
		s.writeAudit([]byte(warning))
	}
}

// rpcResult is the result of a logged exchange answered with a JSON-RPC response: success, or the error
// code as a word.
func rpcResult(response mcpResponse) string {
	if response.Error == nil {
		return "success"
	}
	switch response.Error.Code {
	case mcpParseError:
		return "parse-error"
	case mcpInvalidRequest:
		return "invalid-request"
	case mcpMethodNotFound:
		return "method-not-found"
	case mcpInvalidParams:
		return "invalid-params"
	case mcpInternalError:
		return "internal-error"
	case mcpUnsupportedVersion:
		return "unsupported-version"
	}
	return "error"
}
