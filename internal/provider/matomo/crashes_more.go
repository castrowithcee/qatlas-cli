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
	// maxCrashSearchTerm and maxCrashExcluded are Qatlas's own limits; the reference states none.
	maxCrashSearchTerm = 256
	maxCrashExcluded   = 100
	// maxRecentMinutes is the cap the public demo reported for lastMinutes ("at most 720"); the reference says 12 hours.
	maxRecentMinutes = 720
)

const (
	excludedCrashesSchema = `"excludeIdLogCrashes":{"type":"array","maxItems":100,"uniqueItems":true,` +
		`"items":{"type":"integer","minimum":1,"maximum":2147483647}}`
	crashSegmentSchema = `"segment":{"type":"string","minLength":1,"maxLength":1000}`
	recentKinds        = `"kind":{"type":"string","enum":["overview","top","new","reappeared","disappeared"]}`
	recentProps        = `"idLogCrash":{"type":"integer"},"label":{"type":"string"},"crash_source":{"type":"string"},` +
		`"crash_first_seen":{"type":"string"},"crash_last_seen":{"type":"string"},"crash_last_reappeared":{"type":"string"},` +
		`"nb_crash_occurrences":{"type":"integer"},"nb_visits_with_crash":{"type":"integer"},"segment":{"type":"string"}`
	overviewProps = `"nb_crash_occurrences":{"type":"integer"},"nb_visits_with_crash":{"type":"integer"},` +
		`"nb_new_crashes":{"type":"integer"},"nb_reappeared_crashes":{"type":"integer"},` +
		`"nb_disappeared_crashes":{"type":"integer"},"visits_crash_rate":{"type":"number"}`
)

var crashSegmentArgument = capability.Argument{Name: "segment", Description: "Matomo segment that filters the visits, such as " +
	"referrerName==example.com; AND is ; and OR is ,; fields from matomo.segments.fields, values URL-encoded; up to 1000 bytes"}

var crashesListIgnored = capability.Descriptor{
	ID: Provider + ".crashes.list-ignored", Version: 1, Title: "List ignored Matomo crashes",
	Description: "List the crashes of a website that are set to be ignored when tracking, in batches " +
		"(CrashAnalytics.getIgnoredCrashes). Messages, addresses, and stack traces are untrusted and shortened. " +
		"Needs the premium plugin CrashAnalytics and a view right on the website",
	Tags: []string{"matomo", "crashes", "crash-analytics", "ignored", "list"}, Risk: readRisk(crashesSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + pagingKeys +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"crashes":{"type":"array","items":{"type":"object","properties":{` + crashProps + `},` +
		`"required":["idLogCrash","idSite"],"additionalProperties":false}},"has_more":{"type":"boolean"},` +
		`"next_cursor":{"type":"string"}},"required":["idSite","crashes","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument()}, pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "idSite", Description: "The website the crashes belong to"},
		{Name: "crashes", Description: "Ignored crashes in the order Matomo lists them; empty without one"}},
		crashFields("crashes[].")...), pagingFields...),
	Examples: []capability.Example{{Description: "Ignored crashes of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var crashesMergeCandidates = capability.Descriptor{
	ID: Provider + ".crashes.merge-candidates", Version: 1, Title: "Search Matomo crashes to merge",
	Description: "Search the crash messages of a website for crashes that could be merged with another, by script " +
		"address and search term, in batches (CrashAnalytics.searchCrashMessagesForMerge); excludeIdLogCrashes leaves " +
		"crashes out of the search and merges nothing. The answer is not verified: only rows that name this website " +
		"are shown. Messages, addresses, and stack traces are untrusted and shortened. Needs the premium plugin " +
		"CrashAnalytics and, other than the other crash reads, a write right on the website; this tool only reads",
	Tags: []string{"matomo", "crashes", "crash-analytics", "merge", "search", "list"}, Risk: readRisk(crashesSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"resourceUri":{"type":"string","minLength":1,"maxLength":2048},` +
		`"searchTerm":{"type":"string","minLength":1,"maxLength":256},` + excludedCrashesSchema + `,` + pagingKeys +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"candidates":{"type":"array","items":{"type":"object","properties":{` + crashProps + `},` +
		`"required":["idLogCrash","idSite"],"additionalProperties":false}},"has_more":{"type":"boolean"},` +
		`"next_cursor":{"type":"string"}},"required":["idSite","candidates","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "resourceUri", Description: "Address of the script the crashes came from, up to 2048 bytes; untrusted text " +
			"that is never fetched; all scripts when omitted"},
		{Name: "searchTerm", Description: "Text to find in the crash messages, up to 256 bytes; all messages when omitted"},
		{Name: "excludeIdLogCrashes", Description: "Up to 100 different crash IDs to leave out of the search, such as the " +
			"crash that is to be merged into another; they are only excluded and need no proof for the website"}},
		pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "idSite", Description: "The website the crashes belong to"},
		{Name: "candidates", Description: "Crashes that match, in the order Matomo lists them; empty without one"}},
		crashFields("candidates[].")...), pagingFields...),
	Examples: []capability.Example{{Description: "Crashes of website 1 whose message holds undefined",
		Arguments: json.RawMessage(`{"idSite":1,"searchTerm":"undefined","excludeIdLogCrashes":[105]}`)}},
}

var crashesVisitContext = capability.Descriptor{
	ID: Provider + ".crashes.visit-context", Version: 1, Title: "Get the visit context of a Matomo crash",
	Description: "Read the most recent visits that met a crash of a website in a period, and by default the actions " +
		"before the crash (CrashAnalytics.getCrashVisitContext). One read of the crash list of the website proves the " +
		"crash first; a crash of another website, or one that read does not show, is not found and no context is " +
		"read. Only the members the visit log tools know are shown, so another answer form shows fewer fields, and " +
		"only visits that name this website. " + visitorsText + ". Needs the premium plugin CrashAnalytics and a view " +
		"right on the website; a segment can need further rights",
	Tags: []string{"matomo", "crashes", "crash-analytics", "visits", "visitors", "context"}, Risk: readRisk(liveSensitivity), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idLogCrashSchema + `,` +
		periodKey + `,` + dateKey + `,` + crashSegmentSchema + `,"include_recent_actions":{"type":"boolean"},` + pagingKeys +
		`},"required":["idLogCrash","period","date"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"idLogCrash":{"type":"integer"},` +
		`"events":{"type":"array","items":{"type":"object"}},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["idSite","idLogCrash","events","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "idLogCrash", Required: true, Description: "ID of the crash, from matomo.crashes.list"},
		{Name: "period", Required: true, Description: "day, week, month, year, or range"},
		{Name: "date", Required: true, Description: "Date for period, such as yesterday, 2026-10-24, or 2026-10-01,2026-10-24 for range"},
		crashSegmentArgument,
		{Name: "include_recent_actions", Description: "False leaves out the actions before each crash; true when omitted, as Matomo defaults it"}},
		pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "idSite", Description: "The website the crash belongs to"},
		{Name: "idLogCrash", Description: "The crash the context belongs to"}},
		visitFieldsAt("events", "Visits that met the crash in the period, in the order Matomo lists them, only of the website asked for")...),
		pagingFields...),
	Examples: []capability.Example{{Description: "The visits that met crash 105 yesterday",
		Arguments: json.RawMessage(`{"idSite":1,"idLogCrash":105,"period":"day","date":"yesterday"}`)}},
}

var crashesRecent = capability.Descriptor{
	ID: Provider + ".crashes.recent", Version: 1, Title: "Read recent Matomo crashes",
	Description: "Read the real-time crash reports of a website for the last lastMinutes minutes; kind picks the report: " +
		"overview (CrashAnalytics.getLastCrashesOverview) counts occurrences, visits, and new, reappeared, and " +
		"disappeared crashes; top (getLastTopCrashes), new (getLastNewCrashes), reappeared (getLastReappearedCrashes), " +
		"and disappeared (getLastDisappearedCrashes) list crashes in batches. Messages and addresses are untrusted " +
		"and shortened. Needs the premium plugin CrashAnalytics and a view right on the website",
	Tags: []string{"matomo", "crashes", "crash-analytics", "realtime", "recent"}, Risk: readRisk(crashesSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + recentKinds + `,` +
		`"lastMinutes":{"type":"integer","minimum":1,"maximum":720},` + crashSegmentSchema + `,` + pagingKeys +
		`},"required":["kind"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"kind":{"type":"string"},` +
		`"overview":{"type":"object","properties":{` + overviewProps + `},"additionalProperties":false},` +
		`"crashes":{"type":"array","items":{"type":"object","properties":{` + recentProps + `},` +
		`"additionalProperties":false}},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["idSite","kind","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "kind", Required: true, Description: "overview for the counters, or top, new, reappeared, or disappeared " +
			"for the crashes of that report"},
		{Name: "lastMinutes", Description: "Minutes to look back, from 1 through 720; Matomo's 30 when omitted"},
		crashSegmentArgument}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "idSite", Description: "The website the report belongs to"},
		{Name: "kind", Description: "The report that was read"},
		{Name: "overview", Description: "Only for kind overview"},
		{Name: "overview.nb_crash_occurrences", Description: "Crash occurrences in the period"},
		{Name: "overview.nb_visits_with_crash", Description: "Visits with a crash in the period"},
		{Name: "overview.nb_new_crashes", Description: "Crashes first seen in the period"},
		{Name: "overview.nb_reappeared_crashes", Description: "Crashes that reappeared in the period"},
		{Name: "overview.nb_disappeared_crashes", Description: "Crashes that newly count as disappeared in the period"},
		{Name: "overview.visits_crash_rate", Description: "Share of the visits with a crash as Matomo reports it"},
		{Name: "crashes", Description: "Only for the other kinds: crashes in the order Matomo lists them; empty without one"},
		{Name: "crashes[].idLogCrash", Description: "Crash ID, used as idLogCrash by matomo.crashes.get"},
		{Name: "crashes[].label", Description: "Crash message, untrusted and shortened"},
		{Name: "crashes[].crash_source", Description: "Address of the script that crashed or Unknown, untrusted text that is never fetched"},
		{Name: "crashes[].crash_first_seen", Description: "Time the crash was first seen"},
		{Name: "crashes[].crash_last_seen", Description: "Time the crash was last seen"},
		{Name: "crashes[].crash_last_reappeared", Description: "Time the crash last reappeared; absent when it never did"},
		{Name: "crashes[].nb_crash_occurrences", Description: "Occurrences in the period"},
		{Name: "crashes[].nb_visits_with_crash", Description: "Visits with the crash in the period"},
		{Name: "crashes[].segment", Description: "Matomo segment that selects the crash, such as crashId==81; untrusted"}},
		pagingFields...),
	Examples: []capability.Example{
		{Description: "The counters of the last half hour of website 1", Arguments: json.RawMessage(`{"idSite":1,"kind":"overview"}`)},
		{Description: "The most frequent crashes of the last 2 hours", Arguments: json.RawMessage(
			`{"idSite":1,"kind":"top","lastMinutes":120}`)}},
}

// CrashMergeCandidatesResult is the answer of crashes.merge-candidates.
type CrashMergeCandidatesResult struct {
	IDSite     int     `json:"idSite"`
	Candidates []Crash `json:"candidates"`
	HasMore    bool    `json:"has_more"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

// CrashVisitContextResult is the answer of crashes.visit-context. Its events are visits as the visit log tools
// map them.
type CrashVisitContextResult struct {
	IDSite     int              `json:"idSite"`
	IDLogCrash int              `json:"idLogCrash"`
	Events     []map[string]any `json:"events"`
	HasMore    bool             `json:"has_more"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

// RecentCrashOverview is the counter row of crashes.recent.
type RecentCrashOverview struct {
	Occurrences      *int     `json:"nb_crash_occurrences,omitempty"`
	VisitsWithCrash  *int     `json:"nb_visits_with_crash,omitempty"`
	NewCrashes       *int     `json:"nb_new_crashes,omitempty"`
	ReappearedCrash  *int     `json:"nb_reappeared_crashes,omitempty"`
	DisappearedCrash *int     `json:"nb_disappeared_crashes,omitempty"`
	CrashRate        *float64 `json:"visits_crash_rate,omitempty"`
}

// RecentCrash is a crash of a real-time crash report. All text is untrusted provider data.
type RecentCrash struct {
	IDLogCrash         int    `json:"idLogCrash"`
	Label              string `json:"label,omitempty"`
	CrashSource        string `json:"crash_source,omitempty"`
	CrashFirstSeen     string `json:"crash_first_seen,omitempty"`
	CrashLastSeen      string `json:"crash_last_seen,omitempty"`
	CrashLastReappears string `json:"crash_last_reappeared,omitempty"`
	Occurrences        *int   `json:"nb_crash_occurrences,omitempty"`
	VisitsWithCrash    *int   `json:"nb_visits_with_crash,omitempty"`
	Segment            string `json:"segment,omitempty"`
}

// RecentCrashesResult is the answer of crashes.recent: the counters for kind overview, the crashes otherwise.
type RecentCrashesResult struct {
	IDSite     int                  `json:"idSite"`
	Kind       string               `json:"kind"`
	Overview   *RecentCrashOverview `json:"overview,omitempty"`
	Crashes    []RecentCrash        `json:"crashes,omitempty"`
	HasMore    bool                 `json:"has_more"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

// recentMethods are the fixed methods behind the kinds of crashes.recent.
var recentMethods = map[string]string{
	"overview": "CrashAnalytics.getLastCrashesOverview", "top": "CrashAnalytics.getLastTopCrashes",
	"new": "CrashAnalytics.getLastNewCrashes", "reappeared": "CrashAnalytics.getLastReappearedCrashes",
	"disappeared": "CrashAnalytics.getLastDisappearedCrashes",
}

func invokeCrashesListIgnored(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list ignored crashes"
	var args struct {
		IDSite *int   `json:"idSite"`
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	paging, err := startPage(provider.CursorBinding(crashesListIgnored.ID, scopeBinding(resolved), site), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	params := url.Values{"idSite": {strconv.Itoa(site)}}
	if err := client.call(ctx, op, crashesRequirement(crashesListIgnored.ID), "CrashAnalytics.getIgnoredCrashes", params, &answer); err != nil {
		return nil, err
	}
	crashes, err := siteCrashes(op, answer, site, nil)
	if err != nil {
		return nil, err
	}
	batch, next := slice(paging, crashes)
	return &CrashesResult{IDSite: site, Crashes: batch, HasMore: next != "", NextCursor: next}, nil
}

// siteCrashes reads a crash list and keeps the crashes that name the website and are not excluded; a row of
// another website, or one without a website, is dropped.
func siteCrashes(op string, answer json.RawMessage, site int, excluded []int) ([]Crash, error) {
	rows, valid := readRows[crashJSON](unwrapValue(answer))
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid crash list")
	}
	crashes := []Crash{}
	for i := range rows {
		if !rows[i].inSite(site) {
			continue
		}
		if crash, usable := rows[i].crash(site); usable && !slices.Contains(excluded, crash.IDLogCrash) {
			crashes = append(crashes, crash)
		}
	}
	return crashes, nil
}

func invokeCrashesMergeCandidates(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "search crashes to merge"
	var args struct {
		IDSite      *int   `json:"idSite"`
		ResourceURI string `json:"resourceUri"`
		SearchTerm  string `json:"searchTerm"`
		Excluded    []int  `json:"excludeIdLogCrashes"`
		Limit       int    `json:"limit"`
		Cursor      string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if len(args.ResourceURI) > maxUrlLength {
		return nil, invalidArgument("resourceUri must hold at most 2048 bytes")
	}
	if len(args.SearchTerm) > maxCrashSearchTerm {
		return nil, invalidArgument("searchTerm must hold at most 256 bytes")
	}
	if len(args.Excluded) > maxCrashExcluded {
		return nil, invalidArgument("excludeIdLogCrashes must hold at most 100 crash IDs")
	}
	seen := map[int]bool{}
	for _, id := range args.Excluded {
		if err := checkCrashID(&id); err != nil {
			return nil, invalidArgument("excludeIdLogCrashes must hold positive crash IDs")
		}
		if seen[id] {
			return nil, invalidArgument("excludeIdLogCrashes must hold different crash IDs")
		}
		seen[id] = true
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	excluded := slices.Clone(args.Excluded)
	slices.Sort(excluded)
	paging, err := startPage(provider.CursorBinding(crashesMergeCandidates.ID, scopeBinding(resolved), site,
		args.ResourceURI, args.SearchTerm, excluded), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "offset": {"0"}, "limit": {crashRowLimit(paging.want())}}
	if args.ResourceURI != "" {
		params.Set("resourceUri", args.ResourceURI)
	}
	if args.SearchTerm != "" {
		params.Set("searchTerm", args.SearchTerm)
	}
	if len(excluded) > 0 {
		params.Set("excludeIdLogCrashes", joinIDs(excluded))
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, crashesRequirement(crashesMergeCandidates.ID), "CrashAnalytics.searchCrashMessagesForMerge", params, &answer); err != nil {
		return nil, err
	}
	// Excluding needs no proof for the website: an ID only removes a row, so a foreign one removes nothing that is
	// shown. The same IDs are dropped here as well in case Matomo ignores the parameter.
	crashes, err := siteCrashes(op, answer, site, excluded)
	if err != nil {
		return nil, err
	}
	batch, next := slice(paging, crashes)
	return &CrashMergeCandidatesResult{IDSite: site, Candidates: batch, HasMore: next != "", NextCursor: next}, nil
}

func joinIDs(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}

func invokeCrashesVisitContext(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get crash visit context"
	var args struct {
		IDSite        *int   `json:"idSite"`
		IDLogCrash    *int   `json:"idLogCrash"`
		Period        string `json:"period"`
		Date          string `json:"date"`
		Segment       string `json:"segment"`
		RecentActions *bool  `json:"include_recent_actions"`
		Limit         int    `json:"limit"`
		Cursor        string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := checkCrashID(args.IDLogCrash); err != nil {
		return nil, err
	}
	if args.Period == "" || args.Date == "" {
		return nil, invalidArgument("period (day, week, month, year, or range) and date are required")
	}
	params, err := periodParams(args.Period, args.Date)
	if err != nil {
		return nil, err
	}
	if err := checkSegmentExpression(args.Segment); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	actions := args.RecentActions == nil || *args.RecentActions
	id := *args.IDLogCrash
	paging, err := startPage(provider.CursorBinding(crashesVisitContext.ID, scopeBinding(resolved), site, id,
		args.Period, args.Date, args.Segment, actions), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := crashesRequirement(crashesVisitContext.ID)
	// The crash ID counts across websites and the context names none: the one proof read comes first, and a crash it
	// does not show gets no context request.
	if err := client.proveCrash(ctx, op, req, site, id); err != nil {
		return nil, err
	}
	params.Set("idSite", strconv.Itoa(site))
	params.Set("idLogCrash", strconv.Itoa(id))
	params.Set("fetchRecentActions", map[bool]string{true: "1", false: "0"}[actions])
	if args.Segment != "" {
		params.Set("segment", args.Segment)
	}
	crashWindow(params, paging.want())
	var answer json.RawMessage
	if err := client.call(ctx, op, req, "CrashAnalytics.getCrashVisitContext", params, &answer); err != nil {
		return nil, err
	}
	rows, valid := readRows[row](unwrapValue(answer))
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid crash visit context")
	}
	events := make([]map[string]any, 0, len(rows))
	for _, one := range rows {
		// The answer form is not verified: a row counts only when it names this website, and only the members of the
		// visit log model are taken from it.
		owner := one.integer("idSite")
		if owner == nil {
			owner = one.integer("idsite")
		}
		if owner == nil || *owner != site {
			continue
		}
		if event := visitOf(one, actions); len(event) > 0 {
			events = append(events, event)
		}
	}
	batch, next := slice(paging, events)
	return &CrashVisitContextResult{IDSite: site, IDLogCrash: id, Events: batch, HasMore: next != "", NextCursor: next}, nil
}

type recentCrashJSON struct {
	ID             *loose          `json:"idlogcrash"`
	IDSite         *loose          `json:"idsite"`
	Label          *loose          `json:"label"`
	Source         *loose          `json:"crash_source"`
	FirstSeen      *loose          `json:"crash_first_seen"`
	LastSeen       *loose          `json:"crash_last_seen"`
	LastReappeared json.RawMessage `json:"crash_last_reappeared"`
	Occurrences    *loose          `json:"nb_crash_occurrences"`
	Visits         *loose          `json:"nb_visits_with_crash"`
	Segment        *loose          `json:"segment"`
}

type recentOverviewJSON struct {
	Occurrences *loose `json:"nb_crash_occurrences"`
	Visits      *loose `json:"nb_visits_with_crash"`
	New         *loose `json:"nb_new_crashes"`
	Reappeared  *loose `json:"nb_reappeared_crashes"`
	Disappeared *loose `json:"nb_disappeared_crashes"`
	Rate        *loose `json:"visits_crash_rate"`
}

func (o recentOverviewJSON) overview() *RecentCrashOverview {
	out := &RecentCrashOverview{Occurrences: o.Occurrences.number(), VisitsWithCrash: o.Visits.number(),
		NewCrashes: o.New.number(), ReappearedCrash: o.Reappeared.number(), DisappearedCrash: o.Disappeared.number()}
	if o.Rate != nil {
		if rate, err := strconv.ParseFloat(strings.TrimSpace(o.Rate.text), 64); err == nil && rate >= 0 && rate < 1e12 {
			out.CrashRate = &rate
		}
	}
	return out
}

func invokeCrashesRecent(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "read recent crashes"
	var args struct {
		IDSite      *int   `json:"idSite"`
		Kind        string `json:"kind"`
		LastMinutes *int   `json:"lastMinutes"`
		Segment     string `json:"segment"`
		Limit       int    `json:"limit"`
		Cursor      string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	method, known := recentMethods[args.Kind]
	if !known {
		return nil, invalidArgument("kind must be overview, top, new, reappeared, or disappeared")
	}
	if args.LastMinutes != nil && (*args.LastMinutes < 1 || *args.LastMinutes > maxRecentMinutes) {
		return nil, invalidArgument("lastMinutes must be between 1 and " + strconv.Itoa(maxRecentMinutes))
	}
	if err := checkSegmentExpression(args.Segment); err != nil {
		return nil, err
	}
	overview := args.Kind == "overview"
	if overview && (args.Limit != 0 || args.Cursor != "") {
		return nil, invalidArgument("limit and cursor do not apply to kind overview, which is one row")
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	minutes := ""
	if args.LastMinutes != nil {
		minutes = strconv.Itoa(*args.LastMinutes)
	}
	paging, err := startPage(provider.CursorBinding(crashesRecent.ID, scopeBinding(resolved), site, args.Kind, minutes,
		args.Segment), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}}
	if minutes != "" {
		params.Set("lastMinutes", minutes)
	}
	if args.Segment != "" {
		params.Set("segment", args.Segment)
	}
	if !overview {
		// Only the start of the report is asked for, as with the crash list: the batch is cut out on this side.
		params.Set("filter_limit", crashRowLimit(paging.want()))
	}
	var answer json.RawMessage
	if err := client.call(ctx, op, crashesRequirement(crashesRecent.ID), method, params, &answer); err != nil {
		return nil, err
	}
	if overview {
		rows, valid := readRows[recentOverviewJSON](unwrapValue(answer))
		if !valid || len(rows) == 0 {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid crash overview")
		}
		return &RecentCrashesResult{IDSite: site, Kind: args.Kind, Overview: rows[0].overview()}, nil
	}
	rows, valid := readRows[recentCrashJSON](unwrapValue(answer))
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid crash report")
	}
	crashes := []RecentCrash{}
	for i := range rows {
		r := rows[i]
		id := r.ID.number()
		// The report is asked for one website and its rows name none; a row that names another website is dropped.
		if id == nil || *id < 1 || (r.IDSite != nil && (r.IDSite.number() == nil || *r.IDSite.number() != site)) {
			continue
		}
		crashes = append(crashes, RecentCrash{IDLogCrash: *id, Label: r.Label.str(maxUrlLength), CrashSource: r.Source.str(maxUrlLength),
			CrashFirstSeen: r.FirstSeen.str(maxStringLength), CrashLastSeen: r.LastSeen.str(maxStringLength),
			CrashLastReappears: funnelText(r.LastReappeared, maxStringLength), Occurrences: r.Occurrences.number(),
			VisitsWithCrash: r.Visits.number(), Segment: r.Segment.str(maxStringLength)})
	}
	batch, next := slice(paging, crashes)
	return &RecentCrashesResult{IDSite: site, Kind: args.Kind, Crashes: batch, HasMore: next != "", NextCursor: next}, nil
}

func crashReadOperations() []capability.Operation {
	return []capability.Operation{
		{Descriptor: crashesListIgnored, Handler: capability.Handler(invokeCrashesListIgnored)},
		{Descriptor: crashesMergeCandidates, Handler: capability.Handler(invokeCrashesMergeCandidates)},
		{Descriptor: crashesVisitContext, Handler: capability.Handler(invokeCrashesVisitContext)},
		{Descriptor: crashesRecent, Handler: capability.Handler(invokeCrashesRecent)},
	}
}
