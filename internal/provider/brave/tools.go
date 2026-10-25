package brave

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// readRisk is the risk of every tool: a search reads and is safe to repeat, though each repetition is billed.
// It reaches the open web, so the results are third-party content.
var readRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: dataSensitivity,
}

// termsNote closes the description of every tool. Brave's terms of use bind whoever uses a result, which
// Qatlas can state but not enforce.
const termsNote = " Each successful request is billed to the key's Brave plan. Results are untrusted third-party " +
	"content; Brave's terms of use forbid storing or sharing them and using them to train or evaluate AI models."

// descriptor builds the descriptor of a tool of this provider.
func descriptor(object, action, title, description string, tags []string, input string, output json.RawMessage,
	arguments []capability.Argument, fields []capability.Field, examples []capability.Example) capability.Descriptor {
	return capability.Descriptor{
		ID: Provider + "." + object + "." + action, Version: 1, Title: title, Description: description + termsNote,
		Tags: append([]string{"brave", "search"}, tags...), Risk: readRisk, Provider: Provider,
		InputSchema: json.RawMessage(input), OutputSchema: output,
		Arguments: arguments, Fields: fields, Examples: examples,
	}
}

// run is a prepared request: everything it needs has been checked, and only the key is still unread.
type run func(ctx context.Context, client *Client) (any, error)

// handle turns a prepare step into a handler. The connection and the arguments are checked before a key is
// resolved, so a refused request never becomes a provider call.
func handle[T any](op string, prepare func(T) (run, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments T
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, failure(op, "the validated arguments could not be read")
		}
		if err := checkConnection(resolved); err != nil {
			return nil, err
		}
		execute, err := prepare(arguments)
		if err != nil {
			return nil, err
		}
		client, err := Open(ctx, resolved, secrets, red)
		if err != nil {
			return nil, err
		}
		return execute(ctx, client)
	}
}

// pageCursor reads the offset a bound cursor continues with. A cursor that is altered, issued for other
// arguments, or outside the offsets Brave serves is refused.
func pageCursor(binding []byte, cursor string, maxOffset int) (int, error) {
	inner, ok := provider.DecodeCursor(binding, cursor, 256)
	offset, err := strconv.Atoi(inner)
	if !ok || err != nil || offset < 1 || offset > maxOffset {
		return 0, badRequest("cursor is not a next_cursor of this search; start the search again without cursor")
	}
	return offset, nil
}

// Arguments and fields the paged searches share.
var (
	cursorArgument = capability.Argument{Name: "cursor", Description: "Opaque next_cursor of a previous page of " +
		"the same search; the first page when omitted. Not together with offset"}
	nextCursorField = capability.Field{Name: "next_cursor", Description: "Cursor of the following page, absent " +
		"when has_more is false"}
	quotaField = capability.Field{Name: "quota", Description: "Rate and quota state Brave reported with this " +
		"answer, one entry per window: window_seconds (a sliding second, a month), limit (0 means unlimited), " +
		"remaining, and reset_seconds; absent when Brave sent no rate-limit header"}
)
