package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/config"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
	"github.com/castrowithcee/qatlas-cli/internal/redact"
	"github.com/castrowithcee/qatlas-cli/internal/secret"
)

// liveSensitivity classifies the live tools: visits carry visitor IDs, IP addresses, user IDs, and locations,
// and even a counter with a segment can single out one visitor.
const liveSensitivity = "matomo-live-personal"

const (
	// maxActions bounds the actions returned for one visit.
	maxActions = 50
	// maxLatestSites bounds the websites one visits.get-latest-time call reads, one request each.
	maxLatestSites = 20
	maxSegment     = 2048
	maxDate        = 64
	maxTimestamp   = 9999999999
)

// liveSpacing keeps the Live requests of one token below the 200 requests per minute that Matomo Cloud
// allows for method=live per IP address.
var liveSpacing = 300 * time.Millisecond

type liveGate struct {
	slot chan struct{}
	last time.Time
}

var liveGates = struct {
	mu    sync.Mutex
	byKey map[instanceKey]*liveGate
}{byKey: map[instanceKey]*liveGate{}}

// enterLive admits one Live request at a time per instance and token across the process: Matomo Cloud runs
// at most eight raw-data queries at once per subdomain, and they are the heaviest requests. The returned
// function releases the gate and must be called once.
func (c *Client) enterLive(ctx context.Context, op string) (func(), error) {
	liveGates.mu.Lock()
	gate := liveGates.byKey[c.key]
	if gate == nil {
		gate = &liveGate{slot: make(chan struct{}, 1)}
		liveGates.byKey[c.key] = gate
	}
	liveGates.mu.Unlock()
	select {
	case gate.slot <- struct{}{}:
	case <-ctx.Done():
		return nil, provider.Waited(op, subject, ctx.Err())
	}
	if wait := liveSpacing - time.Since(gate.last); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			<-gate.slot
			return nil, provider.Waited(op, subject, ctx.Err())
		}
	}
	return func() {
		gate.last = time.Now()
		<-gate.slot
	}, nil
}

// callLive is call for a method of the Live plugin.
func (c *Client) callLive(ctx context.Context, op string, req requirement, method string, params url.Values, out any) error {
	release, err := c.enterLive(ctx, op)
	if err != nil {
		return err
	}
	defer release()
	return c.call(ctx, op, req, method, params, out)
}

const (
	siteKey      = `"idSite":{"type":"integer","minimum":1,"maximum":999999999}`
	periodKey    = `"period":{"type":"string","enum":["day","week","month","year","range"]}`
	dateKey      = `"date":{"type":"string","minLength":1,"maxLength":64,"pattern":"^[A-Za-z0-9,_:-]+$"}`
	segmentKey   = `"segment":{"type":"string","minLength":1,"maxLength":2048}`
	periodGroup  = periodKey + `,` + dateKey
	columnsEnum  = `{"type":"array","items":{"type":"string","enum":["visits","actions","visitors","visitsConverted"]},"minItems":1,"maxItems":4,"uniqueItems":true}`
	visitorsText = "Visitor IDs, IP addresses, user IDs, locations, and the addresses, titles, and referrers of visits " +
		"are personal data and untrusted text; they are shortened and addresses are never fetched"
)

var periodArguments = []capability.Argument{
	{Name: "period", Description: "day, week, month, year, or range; needs date"},
	{Name: "date", Description: "Date for period, such as today, yesterday, 2026-10-24, or 2026-10-01,2026-10-24 " +
		"for range; needs period. Without both the log is not restricted to a period"},
}

// periodParams validates period and date, which belong together.
func periodParams(period, date string) (url.Values, error) {
	params := url.Values{}
	if period == "" && date == "" {
		return params, nil
	}
	if !slices.Contains([]string{"day", "week", "month", "year", "range"}, period) || date == "" {
		return nil, invalidArgument("period (day, week, month, year, or range) and date must be given together")
	}
	if len(date) > maxDate || strings.Trim(date, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789,_:-") != "" {
		return nil, invalidArgument("date must hold up to 64 letters, digits, and the characters , _ : -")
	}
	params.Set("period", period)
	params.Set("date", date)
	return params, nil
}

func checkSegment(segment string) error {
	if len(segment) > maxSegment {
		return invalidArgument("segment must hold at most 2048 bytes")
	}
	return nil
}

var visitsList = capability.Descriptor{
	ID: Provider + ".visits.list", Version: 1, Title: "List Matomo visits",
	Description: "Read the visit log of one website, newest first, in batches of 25 by default and at most 100, " +
		"from Live.getLastVisitsDetails; actions of a visit only with include_actions; needs a view right and a " +
		"visits log that is not disabled; " + visitorsText,
	Tags: []string{"matomo", "live", "visits", "visitors", "list"}, Risk: readRisk(liveSensitivity), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + siteKey + `,` + periodGroup + `,` + segmentKey +
		`,"min_timestamp":{"type":"integer","minimum":0,"maximum":9999999999},"include_actions":{"type":"boolean"},` +
		`"flat":{"type":"boolean"},"enhanced":{"type":"boolean"},` + pagingKeys + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"visits":{"type":"array","items":{"type":"object"}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},"required":["visits","has_more"],` +
		`"additionalProperties":false}`),
	Arguments: append(append([]capability.Argument{{Name: "idSite", Description: "Website ID from " +
		"matomo.sites.list; optional when the connection binds exactly one website; must lie inside the " +
		"connection's targets", Target: "site"}}, periodArguments...),
		append([]capability.Argument{
			{Name: "segment", Description: "Matomo segment that filters the visits, such as referrerName==example.test; " +
				"AND is ; and OR is ,; up to 2048 bytes"},
			{Name: "min_timestamp", Description: "Only visits whose last action is at or after this Unix timestamp, " +
				"for refreshing a log"},
			{Name: "include_actions", Description: "True adds the actions of each visit, up to 50; the batch " +
				"gets larger and slower. Omitted or false asks Matomo not to read actions"},
			{Name: "flat", Description: "True asks Matomo for flattened visits; the actions are still returned as a list"},
			{Name: "enhanced", Description: "True lets Matomo plugins enrich the visits; the fields below are unchanged"},
		}, pagingArguments...)...),
	Fields: append(visitFields(), pagingFields...),
	Examples: []capability.Example{
		{Description: "Latest visits of website 1", Arguments: json.RawMessage(`{"idSite":1}`)},
		{Description: "Yesterday's visits with their actions", Arguments: json.RawMessage(
			`{"idSite":1,"period":"day","date":"yesterday","include_actions":true,"limit":10}`)},
	},
}

// VisitsResult is the answer of visits.list.
type VisitsResult struct {
	Visits     []map[string]any `json:"visits"`
	HasMore    bool             `json:"has_more"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

func invokeVisitsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list visits"
	var args struct {
		IDSite         *int   `json:"idSite"`
		Period         string `json:"period"`
		Date           string `json:"date"`
		Segment        string `json:"segment"`
		MinTimestamp   *int   `json:"min_timestamp"`
		IncludeActions bool   `json:"include_actions"`
		Flat           bool   `json:"flat"`
		Enhanced       bool   `json:"enhanced"`
		Limit          int    `json:"limit"`
		Cursor         string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	params, err := periodParams(args.Period, args.Date)
	if err != nil {
		return nil, err
	}
	if err := checkSegment(args.Segment); err != nil {
		return nil, err
	}
	if args.MinTimestamp != nil && (*args.MinTimestamp < 0 || *args.MinTimestamp > maxTimestamp) {
		return nil, invalidArgument("min_timestamp must be a Unix timestamp")
	}
	binding := provider.CursorBinding(visitsList.ID, id, args.Period, args.Date, args.Segment, args.MinTimestamp,
		args.IncludeActions, args.Flat, args.Enhanced)
	paging, err := startPage(binding, args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params.Set("idSite", strconv.Itoa(id))
	if args.Segment != "" {
		params.Set("segment", args.Segment)
	}
	if args.MinTimestamp != nil {
		params.Set("minTimestamp", strconv.Itoa(*args.MinTimestamp))
	}
	params.Set("doNotFetchActions", map[bool]string{true: "0", false: "1"}[args.IncludeActions])
	if args.Flat {
		params.Set("flat", "1")
	}
	if args.Enhanced {
		params.Set("enhanced", "1")
	}
	paging.serverWindow(params)
	var listed []row
	if err := client.callLive(ctx, op, baseRequirement(visitsList.ID), "Live.getLastVisitsDetails", params, &listed); err != nil {
		return nil, err
	}
	kept := make([]map[string]any, 0, len(listed))
	for _, one := range listed {
		// A row of another website than asked for is dropped, so the targets hold whatever Matomo answers.
		if site := one.integer("idSite"); site != nil && *site != id {
			continue
		}
		kept = append(kept, visitOf(one, args.IncludeActions))
	}
	batch, next := sliceWindow(paging, kept)
	if batch == nil {
		batch = []map[string]any{}
	}
	return &VisitsResult{Visits: batch, HasMore: next != "", NextCursor: next}, nil
}

var liveCountersGet = capability.Descriptor{
	ID: Provider + ".live-counters.get", Version: 1, Title: "Get Matomo live counters",
	Description: "Read the visits, actions, visitors, and converted visits of one website over the last " +
		"last_minutes minutes, from Live.getCounters; needs a view right; a segment can single out visitors, " +
		"so the result counts as personal data; Matomo may round the counters when data rounding is enforced",
	Tags: []string{"matomo", "live", "counters", "realtime"}, Risk: readRisk(liveSensitivity), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + siteKey + `,` +
		`"last_minutes":{"type":"integer","minimum":1,"maximum":2880},` + segmentKey + `,` +
		`"show_columns":` + columnsEnum + `,"hide_columns":` + columnsEnum + `},` +
		`"required":["last_minutes"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"visits":{"type":"integer"},` +
		`"actions":{"type":"integer"},"visitors":{"type":"integer"},"visits_converted":{"type":"integer"}},` +
		`"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "idSite", Description: "Website ID from matomo.sites.list; optional when the connection binds " +
			"exactly one website; must lie inside the connection's targets", Target: "site"},
		{Name: "last_minutes", Required: true, Description: "Minutes to look back, from 1 through 2880"},
		{Name: "segment", Description: "Matomo segment that filters the counters; up to 2048 bytes"},
		{Name: "show_columns", Description: "Only these counters: visits, actions, visitors, visitsConverted"},
		{Name: "hide_columns", Description: "Leave out these counters"},
	},
	Fields: []capability.Field{
		{Name: "visits", Description: "Visits in the period"},
		{Name: "actions", Description: "Actions in the period"},
		{Name: "visitors", Description: "Distinct visitors in the period"},
		{Name: "visits_converted", Description: "Visits that converted a goal in the period"},
	},
	Examples: []capability.Example{{Description: "The last half hour of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"last_minutes":30}`)}},
}

// LiveCounters is the answer of live-counters.get; a counter that was not asked for is absent.
type LiveCounters struct {
	Visits          *int `json:"visits,omitempty"`
	Actions         *int `json:"actions,omitempty"`
	Visitors        *int `json:"visitors,omitempty"`
	VisitsConverted *int `json:"visits_converted,omitempty"`
}

var counterColumns = []string{"visits", "actions", "visitors", "visitsConverted"}

func columnList(name string, values []string) (string, error) {
	if len(values) > len(counterColumns) {
		return "", invalidArgument(name + " names too many counters")
	}
	for _, value := range values {
		if !slices.Contains(counterColumns, value) {
			return "", invalidArgument(name + " takes visits, actions, visitors, and visitsConverted")
		}
	}
	return strings.Join(values, ","), nil
}

func invokeLiveCountersGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get live counters"
	var args struct {
		IDSite      *int     `json:"idSite"`
		LastMinutes *int     `json:"last_minutes"`
		Segment     string   `json:"segment"`
		ShowColumns []string `json:"show_columns"`
		HideColumns []string `json:"hide_columns"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if args.LastMinutes == nil || *args.LastMinutes < 1 || *args.LastMinutes > 2880 {
		return nil, invalidArgument("last_minutes must be between 1 and 2880")
	}
	if err := checkSegment(args.Segment); err != nil {
		return nil, err
	}
	show, err := columnList("show_columns", args.ShowColumns)
	if err != nil {
		return nil, err
	}
	hide, err := columnList("hide_columns", args.HideColumns)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "lastMinutes": {strconv.Itoa(*args.LastMinutes)}}
	if args.Segment != "" {
		params.Set("segment", args.Segment)
	}
	if show != "" {
		params.Set("showColumns", show)
	}
	if hide != "" {
		params.Set("hideColumns", hide)
	}
	var rows []row
	if err := client.callLive(ctx, op, baseRequirement(liveCountersGet.ID), "Live.getCounters", params, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, provider.InvalidResponse(op, "Matomo returned no counters")
	}
	return &LiveCounters{Visits: rows[0].integer("visits"), Actions: rows[0].integer("actions"),
		Visitors: rows[0].integer("visitors"), VisitsConverted: rows[0].integer("visitsConverted")}, nil
}

var visitsGetLatestTime = capability.Descriptor{
	ID: Provider + ".visits.get-latest-time", Version: 1, Title: "Get the time of the latest Matomo visit",
	Description: "Read, per website, the time of the most recent visit (UTC), from " +
		"Live.getMostRecentVisitsDateTime, one request per website; needs a view right on each; only websites " +
		"inside the connection's targets are read",
	Tags: []string{"matomo", "live", "visits", "latest"}, Risk: readRisk(liveSensitivity), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"idSites":{"type":"array","items":{"type":"integer",` +
		`"minimum":1,"maximum":999999999},"minItems":1,"maxItems":20,"uniqueItems":true},` + periodGroup +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"sites":{"type":"array","items":{"type":"object",` +
		`"properties":{"id":{"type":"integer"},"latest_visit_time":{"type":"string"}},"required":["id"],` +
		`"additionalProperties":false}}},"required":["sites"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{{Name: "idSites", Description: "Up to 20 website IDs, each inside " +
		"the connection's targets; optional when the connection binds up to 20 single websites, which are then " +
		"all read", Target: "site"}}, periodArguments...),
	Fields: []capability.Field{
		{Name: "sites", Description: "One entry per website asked for, ordered by id"},
		{Name: "sites[].id", Description: "Website ID"},
		{Name: "sites[].latest_visit_time", Description: "Date and time of the latest visit in UTC as Matomo " +
			"reports it; absent when the website has no visit in the period"},
	},
	Examples: []capability.Example{{Description: "Latest visit of websites 1 and 2",
		Arguments: json.RawMessage(`{"idSites":[1,2]}`)}},
}

// LatestTimes is the answer of visits.get-latest-time.
type LatestTimes struct {
	Sites []LatestTime `json:"sites"`
}

// LatestTime is the latest visit of one website.
type LatestTime struct {
	ID              int    `json:"id"`
	LatestVisitTime string `json:"latest_visit_time,omitempty"`
}

// latestSites settles the websites of the call against the targets before any secret is read.
func latestSites(resolved *config.Resolved, given []int) ([]int, error) {
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	if len(given) == 0 {
		if !bound.singles() || len(bound.sites) > maxLatestSites {
			return nil, invalidArgument("idSites is required unless the connection binds at most 20 single websites")
		}
		given = bound.sites
	}
	if len(given) > maxLatestSites {
		return nil, invalidArgument("idSites names at most 20 websites")
	}
	ids := slices.Clone(given)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for _, id := range ids {
		if id < 1 || len(strconv.Itoa(id)) > maxIDDigits {
			return nil, invalidArgument("idSites must hold positive integers")
		}
		if !bound.allows(id) {
			return nil, invalidArgument(outsideTargets)
		}
	}
	return ids, nil
}

func invokeVisitsGetLatestTime(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get latest visit time"
	var args struct {
		IDSites []int  `json:"idSites"`
		Period  string `json:"period"`
		Date    string `json:"date"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	ids, err := latestSites(resolved, args.IDSites)
	if err != nil {
		return nil, err
	}
	period, err := periodParams(args.Period, args.Date)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	result := &LatestTimes{Sites: []LatestTime{}}
	for _, id := range ids {
		params := url.Values{"idSite": {strconv.Itoa(id)}}
		for name, values := range period {
			params[name] = values
		}
		var rawTime json.RawMessage
		if err := client.callLive(ctx, op, baseRequirement(visitsGetLatestTime.ID),
			"Live.getMostRecentVisitsDateTime", params, &rawTime); err != nil {
			return nil, err
		}
		var when string
		if err := scalar(rawTime, &when); err != nil {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid time")
		}
		result.Sites = append(result.Sites, LatestTime{ID: id, LatestVisitTime: boundString(strings.TrimSpace(when), maxStringLength)})
	}
	return result, nil
}
