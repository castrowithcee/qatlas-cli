package github

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// The traffic tools read the aggregate visitor counts GitHub keeps for the bound repository: the views and
// the clones of the last 14 days, per day or per week, and the ten most visited paths and referrers of the
// last 14 days. They carry only counts, never a visitor. GitHub shows traffic only to a token with push
// access, so a refusal names that. Paths, titles, and referrers come from visitors and are untrusted data.

const trafficPermission = "GitHub refused this token the traffic of this repository; GitHub shows it only to a " +
	"token with push access to the repository, which on a fine-grained token also needs Administration: read, " +
	"and on a classic token repo"

const (
	trafficPointProperties = `"timestamp":{"type":"string"},"count":{"type":"integer"},"uniques":{"type":"integer"}`
	trafficPointRequired   = `"required":["timestamp","count","uniques"],"additionalProperties":false`
	trafficTotals          = `"count":{"type":"integer"},"uniques":{"type":"integer"},`
)

func trafficSeriesOutput(key string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` + trafficTotals + `"` + key + `":{"type":"array",` +
		`"items":{"type":"object","properties":{` + trafficPointProperties + `},` + trafficPointRequired + `}}},` +
		`"required":["count","uniques","` + key + `"],"additionalProperties":false}`)
}

func trafficListOutput(key, properties, required string) json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"` + key + `":{"type":"array","items":{"type":"object",` +
		`"properties":{` + properties + `},` + required + `}}},"required":["` + key + `"],"additionalProperties":false}`)
}

const trafficPerSchema = `"per":{"type":"string","enum":["day","week"]}`

var trafficPerArgument = capability.Argument{Name: "per", Description: "day or week, the span each entry covers; " +
	"day when omitted"}

func trafficSeriesFields(key, name, who string) []capability.Field {
	return []capability.Field{
		{Name: "count", Description: "Total " + name + " of the last 14 days at the top, " + name + " of the span in an entry"},
		{Name: "uniques", Description: "Unique " + who + " of the last 14 days at the top, of the span in an entry, as aggregate numbers"},
		{Name: key, Description: "One entry per span with timestamp, count, and uniques"},
		{Name: "timestamp", Description: "Start of the span the entry covers"},
	}
}

var trafficViewsGet = capability.Descriptor{
	ID:      Provider + ".traffic-views.get",
	Version: 1,
	Title:   "Get GitHub repository views",
	Description: "Read the page views of a repository a connection allows over the last 14 days, per day or " +
		"per week, as aggregate counts; GitHub needs push access to the repository",
	Tags:         []string{"github", "repositories", "traffic", "views", "get"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(trafficPerSchema),
	OutputSchema: trafficSeriesOutput("views"),
	Arguments:    []capability.Argument{trafficPerArgument},
	Fields:       trafficSeriesFields("views", "views", "visitors"),
	Examples:     []capability.Example{{Description: "Read the views per week", Arguments: json.RawMessage(`{"per":"week"}`)}},
}

var trafficClonesGet = capability.Descriptor{
	ID:      Provider + ".traffic-clones.get",
	Version: 1,
	Title:   "Get GitHub repository clones",
	Description: "Read the clones of a repository a connection allows over the last 14 days, per day or per " +
		"week, as aggregate counts; GitHub needs push access to the repository",
	Tags:         []string{"github", "repositories", "traffic", "clones", "get"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(trafficPerSchema),
	OutputSchema: trafficSeriesOutput("clones"),
	Arguments:    []capability.Argument{trafficPerArgument},
	Fields:       trafficSeriesFields("clones", "clones", "cloners"),
	Examples:     []capability.Example{{Description: "Read the clones per day", Arguments: json.RawMessage(`{"per":"day"}`)}},
}

const (
	trafficPathProperties = `"path":{"type":"string"},"title":{"type":"string"},"count":{"type":"integer"},` +
		`"uniques":{"type":"integer"}`
	trafficReferrerProperties = `"referrer":{"type":"string"},"count":{"type":"integer"},"uniques":{"type":"integer"}`
)

var trafficPathsList = capability.Descriptor{
	ID:      Provider + ".traffic-paths.list",
	Version: 1,
	Title:   "List GitHub popular repository paths",
	Description: "List the ten most visited paths of a repository a connection allows over the last 14 days, " +
		"with their views as aggregate counts; GitHub needs push access to the repository",
	Tags:         []string{"github", "repositories", "traffic", "paths", "list"},
	Risk:         readRisk,
	Provider:     Provider,
	InputSchema:  inputSchema(""),
	OutputSchema: trafficListOutput("paths", trafficPathProperties, `"required":["path","title","count","uniques"],"additionalProperties":false`),
	Fields: []capability.Field{
		{Name: "paths", Description: "The most visited paths with path, title, count, and uniques"},
		{Name: "path", Description: "Visited path, cut at 4096 characters, untrusted data, never fetched"},
		{Name: "title", Description: "Title of the visited page, cut at 200 characters, untrusted data"},
		{Name: "count", Description: "Views of the path"},
		{Name: "uniques", Description: "Unique visitors of the path, an aggregate number"},
	},
	Examples: []capability.Example{{Description: "List the popular paths", Arguments: json.RawMessage(`{}`)}},
}

var trafficReferrersList = capability.Descriptor{
	ID:      Provider + ".traffic-referrers.list",
	Version: 1,
	Title:   "List GitHub popular repository referrers",
	Description: "List the ten referrers that sent the most visits to a repository a connection allows over " +
		"the last 14 days, as aggregate counts; GitHub needs push access to the repository",
	Tags:        []string{"github", "repositories", "traffic", "referrers", "list"},
	Risk:        readRisk,
	Provider:    Provider,
	InputSchema: inputSchema(""),
	OutputSchema: trafficListOutput("referrers", trafficReferrerProperties,
		`"required":["referrer","count","uniques"],"additionalProperties":false`),
	Fields: []capability.Field{
		{Name: "referrers", Description: "The top referrers with referrer, count, and uniques"},
		{Name: "referrer", Description: "Referring site, cut at 200 characters, untrusted data, never fetched"},
		{Name: "count", Description: "Visits from the referrer"},
		{Name: "uniques", Description: "Unique visitors from the referrer, an aggregate number"},
	},
	Examples: []capability.Example{{Description: "List the popular referrers", Arguments: json.RawMessage(`{}`)}},
}

type trafficArguments struct {
	Per string `json:"per"`
}

// TrafficPoint is the count of one day or week.
type TrafficPoint struct {
	Timestamp string `json:"timestamp"`
	Count     int64  `json:"count"`
	Uniques   int64  `json:"uniques"`
}

// TrafficViews is the view count of a repository.
type TrafficViews struct {
	Count   int64          `json:"count"`
	Uniques int64          `json:"uniques"`
	Views   []TrafficPoint `json:"views"`
}

// TrafficClones is the clone count of a repository.
type TrafficClones struct {
	Count   int64          `json:"count"`
	Uniques int64          `json:"uniques"`
	Clones  []TrafficPoint `json:"clones"`
}

// TrafficPath is one popular path.
type TrafficPath struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Count   int64  `json:"count"`
	Uniques int64  `json:"uniques"`
}

// TrafficPathList is the popular paths of a repository.
type TrafficPathList struct {
	Paths []TrafficPath `json:"paths"`
}

// TrafficReferrer is one popular referrer.
type TrafficReferrer struct {
	Referrer string `json:"referrer"`
	Count    int64  `json:"count"`
	Uniques  int64  `json:"uniques"`
}

// TrafficReferrerList is the popular referrers of a repository.
type TrafficReferrerList struct {
	Referrers []TrafficReferrer `json:"referrers"`
}

type trafficPointRaw struct {
	Timestamp *string `json:"timestamp"`
	Count     *int64  `json:"count"`
	Uniques   *int64  `json:"uniques"`
}

type trafficSeriesRaw struct {
	Count   *int64            `json:"count"`
	Uniques *int64            `json:"uniques"`
	Views   []trafficPointRaw `json:"views"`
	Clones  []trafficPointRaw `json:"clones"`
}

type trafficEntryRaw struct {
	Path     *string `json:"path"`
	Title    *string `json:"title"`
	Referrer *string `json:"referrer"`
	Count    *int64  `json:"count"`
	Uniques  *int64  `json:"uniques"`
}

func trafficOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: trafficViewsGet, Handler: trafficHandler(trafficViewsGet.ID, true,
			func(ctx context.Context, c *Client, per string) (any, error) {
				return c.trafficSeries(ctx, "views", per)
			})},
		{Descriptor: trafficClonesGet, Handler: trafficHandler(trafficClonesGet.ID, true,
			func(ctx context.Context, c *Client, per string) (any, error) {
				return c.trafficSeries(ctx, "clones", per)
			})},
		{Descriptor: trafficPathsList, Handler: trafficHandler(trafficPathsList.ID, false,
			func(ctx context.Context, c *Client, _ string) (any, error) { return c.trafficPaths(ctx) })},
		{Descriptor: trafficReferrersList, Handler: trafficHandler(trafficReferrersList.ID, false,
			func(ctx context.Context, c *Client, _ string) (any, error) { return c.trafficReferrers(ctx) })},
	}
}

// trafficHandler checks the repository and per before a credential is resolved, so a refused request never
// becomes a provider call.
func trafficHandler(id string, takesPer bool, call func(context.Context, *Client, string) (any, error)) capability.Handler {
	return func(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
		raw json.RawMessage) (any, error) {
		var arguments trafficArguments
		if err := json.Unmarshal(raw, &arguments); err != nil {
			return nil, unreadable(id)
		}
		bound, err := selectTarget(resolved, kindRepository, raw)
		if err != nil {
			return nil, err
		}
		if arguments.Per != "" && (!takesPer || (arguments.Per != "day" && arguments.Per != "week")) {
			return nil, invalidRequest("per must be day or week")
		}
		client, err := openAt(ctx, resolved, secrets, red, bound)
		if err != nil {
			return nil, err
		}
		return bound.locate(call(ctx, client, arguments.Per))
	}
}

func (c *Client) trafficRead(ctx context.Context, op, route, per string, out any) error {
	path := c.repoPath("traffic/" + route)
	if per != "" {
		path += "?" + url.Values{"per": {per}}.Encode()
	}
	return actionsFailure(c.rest(ctx, op, path, out), trafficPermission)
}

func trafficPoints(op string, raw []trafficPointRaw) ([]TrafficPoint, error) {
	points := make([]TrafficPoint, 0, len(raw))
	for _, p := range raw {
		if p.Timestamp == nil || p.Count == nil || p.Uniques == nil {
			return nil, invalidEntry(op, "a traffic entry")
		}
		points = append(points, TrafficPoint{Timestamp: *nameText(p.Timestamp), Count: *p.Count, Uniques: *p.Uniques})
	}
	return points, nil
}

func (c *Client) trafficSeries(ctx context.Context, key, per string) (any, error) {
	op := "get traffic " + key
	var raw trafficSeriesRaw
	if err := c.trafficRead(ctx, op, key, per, &raw); err != nil {
		return nil, err
	}
	if raw.Count == nil || raw.Uniques == nil {
		return nil, invalidEntry(op, "the traffic")
	}
	entries := raw.Views
	if key == "clones" {
		entries = raw.Clones
	}
	points, err := trafficPoints(op, entries)
	if err != nil {
		return nil, err
	}
	if key == "clones" {
		return &TrafficClones{Count: *raw.Count, Uniques: *raw.Uniques, Clones: points}, nil
	}
	return &TrafficViews{Count: *raw.Count, Uniques: *raw.Uniques, Views: points}, nil
}

func (c *Client) trafficPaths(ctx context.Context) (any, error) {
	const op = "list traffic paths"
	var raw []trafficEntryRaw
	if err := c.trafficRead(ctx, op, "popular/paths", "", &raw); err != nil {
		return nil, err
	}
	result := &TrafficPathList{Paths: make([]TrafficPath, 0, len(raw))}
	for _, e := range raw {
		if e.Path == nil || e.Count == nil || e.Uniques == nil {
			return nil, invalidEntry(op, "a traffic path")
		}
		path, _ := cleanText(e.Path, projectionPathLimit, false)
		title := ""
		if t := nameText(e.Title); t != nil {
			title = *t
		}
		result.Paths = append(result.Paths, TrafficPath{Path: *path, Title: title, Count: *e.Count, Uniques: *e.Uniques})
	}
	return result, nil
}

func (c *Client) trafficReferrers(ctx context.Context) (any, error) {
	const op = "list traffic referrers"
	var raw []trafficEntryRaw
	if err := c.trafficRead(ctx, op, "popular/referrers", "", &raw); err != nil {
		return nil, err
	}
	result := &TrafficReferrerList{Referrers: make([]TrafficReferrer, 0, len(raw))}
	for _, e := range raw {
		if e.Referrer == nil || e.Count == nil || e.Uniques == nil {
			return nil, invalidEntry(op, "a traffic referrer")
		}
		result.Referrers = append(result.Referrers, TrafficReferrer{Referrer: *nameText(e.Referrer),
			Count: *e.Count, Uniques: *e.Uniques})
	}
	return result, nil
}

// trafficSubject names the traffic a path below a repository addresses, and is empty for any other path.
func trafficSubject(path string) string {
	path, _, _ = strings.Cut(path, "?")
	switch path {
	case "traffic/views":
		return "the traffic views of this repository"
	case "traffic/clones":
		return "the traffic clones of this repository"
	case "traffic/popular/paths":
		return "the popular paths of this repository"
	case "traffic/popular/referrers":
		return "the popular referrers of this repository"
	}
	return ""
}
