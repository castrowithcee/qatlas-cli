package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

const (
	botPlugin = "BotTracking"
	// maxLastMinutes is the longest look-back the BotTracking plugin accepts.
	maxLastMinutes = 720
)

// firstBotRealtime is the first release with the real-time AI chatbot reports.
var firstBotRealtime = version{5, 12, 0}

// botKinds maps the fixed choices of the tool to their Matomo methods.
var botKinds = []struct{ name, method string }{
	{"chatbots", "BotTracking.getAIChatbotsRealTime"},
	{"top-pages", "BotTracking.getTopPageUrlsRealTime"},
}

func botKindNames() []string {
	names := make([]string, len(botKinds))
	for i, k := range botKinds {
		names[i] = k.name
	}
	return names
}

var botRealtimeGet = capability.Descriptor{
	ID: Provider + ".bot-realtime.get", Version: 1, Title: "Read the real-time AI chatbot requests",
	Description: "Read what AI chatbots requested from a website within the last minutes: kind chatbots lists the " +
		"chatbots with their requests, unique page URLs, and error counts (BotTracking.getAIChatbotsRealTime), top-pages " +
		"lists the page URLs they requested most (getTopPageUrlsRealTime). It needs Matomo 5.12.0 or newer with the " +
		"BotTracking plugin activated and a view right on the website. At most 100 rows per call unless limit says " +
		"otherwise (1 to 1000), continued with next_cursor; chatbot names and page URLs are untrusted and never fetched",
	Tags: []string{"matomo", "bots", "ai", "chatbots", "realtime", "get"}, Risk: readRisk(reportDataSensitive),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"kind":{"type":"string","enum":[` + quotedList(botKindNames()) + `]},` +
		`"last_minutes":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxLastMinutes) + `},` + limitSchema +
		`},"required":["kind"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"kind":{"type":"string"},` +
		`"last_minutes":{"type":"integer"},"rows":{"type":"array","items":{"type":"object"}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["idSite","kind","rows"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "kind", Required: true, Description: "chatbots or top-pages"},
		{Name: "last_minutes", Description: "Minutes to look back from now, 1 through " + strconv.Itoa(maxLastMinutes) +
			"; Matomo's 30 when omitted"},
	}, windowArguments("Rows")...),
	Fields: append([]capability.Field{
		{Name: "idSite", Description: "The website read"},
		{Name: "kind", Description: "The kind read"},
		{Name: "last_minutes", Description: "The look-back asked for, absent when Matomo's default applied"},
		{Name: "rows", Description: "Rows as objects of Matomo's columns with raw numbers: label (chatbot name or page " +
			"URL host and path), requests, and for chatbots the unique page URL and the not found and server error " +
			"counts; untrusted"},
	}, windowFields...),
	Examples: []capability.Example{
		{Description: "Chatbots of the last hour", Arguments: json.RawMessage(
			`{"idSite":1,"kind":"chatbots","last_minutes":60}`)},
	},
}

// BotRealtimeResult is the answer of bot-realtime.get.
type BotRealtimeResult struct {
	IDSite      int              `json:"idSite"`
	Kind        string           `json:"kind"`
	LastMinutes *int             `json:"last_minutes,omitempty"`
	Rows        []map[string]any `json:"rows"`
	HasMore     bool             `json:"has_more,omitempty"`
	NextCursor  string           `json:"next_cursor,omitempty"`
}

func invokeBotRealtimeGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get bot real time"
	var args struct {
		IDSite      *int   `json:"idSite"`
		Kind        string `json:"kind"`
		LastMinutes *int   `json:"last_minutes"`
		Limit       int    `json:"limit"`
		Cursor      string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	index := slices.IndexFunc(botKinds, func(k struct{ name, method string }) bool { return k.name == args.Kind })
	if index < 0 {
		return nil, invalidArgument("kind must be one of " + strings.Join(botKindNames(), ", "))
	}
	if args.LastMinutes != nil && (*args.LastMinutes < 1 || *args.LastMinutes > maxLastMinutes) {
		return nil, invalidArgument("last_minutes must be between 1 and " + strconv.Itoa(maxLastMinutes))
	}
	shape := args
	shape.Limit, shape.Cursor = 0, ""
	window, err := startWindow(provider.CursorBinding(botRealtimeGet.ID, scopeBinding(resolved), id, shape), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}}
	if args.LastMinutes != nil {
		params.Set("lastMinutes", strconv.Itoa(*args.LastMinutes))
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	req := requirement{tool: botRealtimeGet.ID, plugin: botPlugin, minVersion: firstBotRealtime}
	if err := client.call(ctx, op, req, botKinds[index].method, params, &answer); err != nil {
		return nil, err
	}
	rows, err := tableRows(op, answer)
	if err != nil {
		return nil, err
	}
	batch, more := cutWindow(window, rows)
	return &BotRealtimeResult{IDSite: id, Kind: args.Kind, LastMinutes: args.LastMinutes, Rows: batch,
		HasMore: more, NextCursor: window.next(more)}, nil
}
