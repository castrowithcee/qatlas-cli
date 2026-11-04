package matomo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

const (
	// crashesSensitive covers crash messages, page and script addresses, and stack traces that sites and their visitors produce.
	crashesSensitive = "matomo-crashes"
	crashesPlugin    = "CrashAnalytics"
	maxCrashID       = 2147483647
	// maxCrashRows is the most rows the reference lets a crash list return in one request ("values above 10000 are capped").
	maxCrashRows = 10000
	// maxCrashStack and maxCrashGroupMembers are Qatlas's own limits; the reference states none.
	maxCrashStack        = 4096
	maxCrashGroupMembers = 100
)

func crashesRequirement(tool string) requirement {
	return requirement{tool: tool, plugin: crashesPlugin}
}

// crashSortColumns are the crash fields a list may be sorted by: the single-valued ones of a list row. The
// reference names no set of columns, so this is the narrower reading.
var crashSortColumns = []string{"idlogcrash", "crash_type", "message", "resource_uri", "resource_line",
	"resource_column", "datetime_first_seen", "datetime_last_seen"}

const (
	crashSortSchema = `"sort_column":{"type":"string","enum":["idlogcrash","crash_type","message","resource_uri",` +
		`"resource_line","resource_column","datetime_first_seen","datetime_last_seen"]},` +
		`"sort_order":{"type":"string","enum":["asc","desc"]}`
	idLogCrashSchema = `"idLogCrash":{"type":"integer","minimum":1,"maximum":2147483647}`
	crashProps       = `"idLogCrash":{"type":"integer"},"idSite":{"type":"integer"},"group_idLogCrash":{"type":"integer"},` +
		`"crash_type":{"type":"string"},"message":{"type":"string"},"resource_uri":{"type":"string"},` +
		`"resource_line":{"type":"integer"},"resource_column":{"type":"integer"},"stack_trace":{"type":"string"},` +
		`"category":{"type":"string"},"crash_page_url":{"type":"string"},` +
		`"datetime_first_seen":{"type":"string"},"datetime_first_seen_pretty":{"type":"string"},` +
		`"datetime_last_seen":{"type":"string"},"datetime_last_seen_pretty":{"type":"string"},` +
		`"datetime_last_reappeared":{"type":"string"},"datetime_last_reappeared_pretty":{"type":"string"},` +
		`"datetime_ignored_error":{"type":"string"},"datetime_ignored_error_pretty":{"type":"string"},` +
		`"date_first_seen":{"type":"string"},"date_first_seen_pretty":{"type":"string"},` +
		`"date_last_seen":{"type":"string"},"date_last_seen_pretty":{"type":"string"}`
)

func crashFields(prefix string) []capability.Field {
	return []capability.Field{
		{Name: prefix + "idLogCrash", Description: "Crash ID, used as idLogCrash by matomo.crashes.get"},
		{Name: prefix + "idSite", Description: "The website the crash belongs to"},
		{Name: prefix + "group_idLogCrash", Description: "ID of the parent crash when the crash was merged into a group; absent otherwise"},
		{Name: prefix + "crash_type", Description: "Type of the crash such as TypeError, untrusted"},
		{Name: prefix + "message", Description: "Crash message, untrusted and shortened"},
		{Name: prefix + "resource_uri", Description: "Address of the script that crashed, untrusted text that is never fetched"},
		{Name: prefix + "resource_line", Description: "Line in the script"},
		{Name: prefix + "resource_column", Description: "Column in the script"},
		{Name: prefix + "stack_trace", Description: "Stack trace, untrusted and shortened to 4096 bytes"},
		{Name: prefix + "category", Description: "Category of the crash; only matomo.crashes.get reads it"},
		{Name: prefix + "crash_page_url", Description: "Address of the page of the latest crash, untrusted text that is never " +
			"fetched; only matomo.crashes.get reads it"},
		{Name: prefix + "datetime_first_seen", Description: "Time the crash was first seen as Matomo stores it"},
		{Name: prefix + "datetime_first_seen_pretty", Description: "The same time as Matomo formats it for display"},
		{Name: prefix + "datetime_last_seen", Description: "Time the crash was last seen as Matomo stores it"},
		{Name: prefix + "datetime_last_seen_pretty", Description: "The same time as Matomo formats it for display"},
		{Name: prefix + "datetime_last_reappeared", Description: "Time the crash last reappeared; absent when it never did"},
		{Name: prefix + "datetime_last_reappeared_pretty", Description: "The same time as Matomo formats it for display"},
		{Name: prefix + "datetime_ignored_error", Description: "Time the crash was set to be ignored; absent when it is not"},
		{Name: prefix + "datetime_ignored_error_pretty", Description: "The same time as Matomo formats it for display"},
		{Name: prefix + "date_first_seen", Description: "Day the crash was first seen; only the list reads it"},
		{Name: prefix + "date_first_seen_pretty", Description: "The same day as Matomo formats it for display; only the list reads it"},
		{Name: prefix + "date_last_seen", Description: "Day the crash was last seen; only the list reads it"},
		{Name: prefix + "date_last_seen_pretty", Description: "The same day as Matomo formats it for display; only the list reads it"},
	}
}

var crashesList = capability.Descriptor{
	ID: Provider + ".crashes.list", Version: 1, Title: "List Matomo crashes",
	Description: "List the crashes the premium plugin CrashAnalytics tracked for a website, in batches, sorted by " +
		"sort_column and sort_order (CrashAnalytics.getAllCrashes). Without them the most recently seen crash comes " +
		"first. Messages, addresses, and stack traces are untrusted and shortened. The reports by period are read " +
		"with matomo.reports.get. Needs the premium plugin CrashAnalytics and a view right on the website",
	Tags: []string{"matomo", "crashes", "crash-analytics", "list"}, Risk: readRisk(crashesSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + crashSortSchema + `,` +
		pagingKeys + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"crashes":{"type":"array","items":{"type":"object","properties":{` + crashProps + `},` +
		`"required":["idLogCrash","idSite"],"additionalProperties":false}},"has_more":{"type":"boolean"},` +
		`"next_cursor":{"type":"string"}},"required":["idSite","crashes","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "sort_column", Description: "Crash field to sort by: idlogcrash, crash_type, message, resource_uri, " +
			"resource_line, resource_column, datetime_first_seen, or datetime_last_seen; datetime_last_seen when omitted"},
		{Name: "sort_order", Description: "asc or desc; desc when omitted"}},
		pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "idSite", Description: "The website the crashes belong to"},
		{Name: "crashes", Description: "Crashes in the order asked for; empty without one"}},
		crashFields("crashes[].")...), pagingFields...),
	Examples: []capability.Example{
		{Description: "The most recently seen crashes of website 1", Arguments: json.RawMessage(`{"idSite":1}`)},
		{Description: "The oldest crashes first", Arguments: json.RawMessage(
			`{"idSite":1,"sort_column":"datetime_first_seen","sort_order":"asc"}`)}},
}

var crashesGet = capability.Descriptor{
	ID: Provider + ".crashes.get", Version: 1, Title: "Get a Matomo crash",
	Description: "Read one crash of a website by its ID with its message, source, stack trace, and the page of the " +
		"latest crash (CrashAnalytics.getCrashSummary). A crash ID counts across websites and the summary names no " +
		"website, so one read of the crash list of the website proves it first; a crash of another website, or one " +
		"that read does not show, is not found. Messages, addresses, and stack traces are untrusted and shortened. " +
		"Needs the premium plugin CrashAnalytics and a view right on the website",
	Tags: []string{"matomo", "crashes", "crash-analytics", "get"}, Risk: readRisk(crashesSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + idLogCrashSchema + `},` +
		`"required":["idLogCrash"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{` + crashProps + `},` +
		`"required":["idLogCrash","idSite"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(),
		{Name: "idLogCrash", Required: true, Description: "ID of the crash, from matomo.crashes.list"}},
	Fields: crashFields(""),
	Examples: []capability.Example{{Description: "Crash 105 of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"idLogCrash":105}`)}},
}

var crashGroupsList = capability.Descriptor{
	ID: Provider + ".crash-groups.list", Version: 1, Title: "List Matomo crash groups",
	Description: "List the groups of merged crashes of a website, each with the parent crash ID and its crashes, in " +
		"batches (CrashAnalytics.getCrashGroups). Messages, addresses, and stack traces are untrusted and shortened. " +
		"Needs the premium plugin CrashAnalytics and, other than the other crash tools, a write right on the website",
	Tags: []string{"matomo", "crashes", "crash-analytics", "groups", "list"}, Risk: readRisk(crashesSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + pagingKeys +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"groups":{"type":"array","items":{"type":"object","properties":{"parent_idLogCrash":{"type":"integer"},` +
		`"crashes":{"type":"array","items":{"type":"object","properties":{` + crashProps + `},` +
		`"required":["idLogCrash","idSite"],"additionalProperties":false}}},` +
		`"required":["parent_idLogCrash","crashes"],"additionalProperties":false}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["idSite","groups","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument()}, pagingArguments...),
	Fields: append(append([]capability.Field{
		{Name: "idSite", Description: "The website the groups belong to"},
		{Name: "groups", Description: "Groups ordered by the ID of the parent crash; empty without one"},
		{Name: "groups[].parent_idLogCrash", Description: "ID of the parent crash the crashes were merged into"},
		{Name: "groups[].crashes", Description: "Crashes of the group that lie inside the website, at most 100"}},
		crashFields("groups[].crashes[].")...), pagingFields...),
	Examples: []capability.Example{{Description: "Crash groups of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

var crashTypesList = capability.Descriptor{
	ID: Provider + ".crash-types.list", Version: 1, Title: "List Matomo crash types",
	Description: "List the different crash types tracked for a website, such as TypeError, in batches " +
		"(CrashAnalytics.getCrashTypes). Needs the premium plugin CrashAnalytics and a view right on the website",
	Tags: []string{"matomo", "crashes", "crash-analytics", "types", "list"}, Risk: readRisk(crashesSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + pagingKeys +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"crash_types":{"type":"array","items":{"type":"string"}},"has_more":{"type":"boolean"},` +
		`"next_cursor":{"type":"string"}},"required":["idSite","crash_types","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument()}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "idSite", Description: "The website the crash types belong to"},
		{Name: "crash_types", Description: "Crash types in the order Matomo lists them, untrusted; empty without one"}},
		pagingFields...),
	Examples: []capability.Example{{Description: "Crash types of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

// Crash is a crash of a website. All text is untrusted provider data.
type Crash struct {
	IDLogCrash                   int    `json:"idLogCrash"`
	IDSite                       int    `json:"idSite"`
	GroupIDLogCrash              *int   `json:"group_idLogCrash,omitempty"`
	CrashType                    string `json:"crash_type,omitempty"`
	Message                      string `json:"message,omitempty"`
	ResourceURI                  string `json:"resource_uri,omitempty"`
	ResourceLine                 *int   `json:"resource_line,omitempty"`
	ResourceColumn               *int   `json:"resource_column,omitempty"`
	StackTrace                   string `json:"stack_trace,omitempty"`
	Category                     string `json:"category,omitempty"`
	CrashPageURL                 string `json:"crash_page_url,omitempty"`
	DatetimeFirstSeen            string `json:"datetime_first_seen,omitempty"`
	DatetimeFirstSeenPretty      string `json:"datetime_first_seen_pretty,omitempty"`
	DatetimeLastSeen             string `json:"datetime_last_seen,omitempty"`
	DatetimeLastSeenPretty       string `json:"datetime_last_seen_pretty,omitempty"`
	DatetimeLastReappeared       string `json:"datetime_last_reappeared,omitempty"`
	DatetimeLastReappearedPretty string `json:"datetime_last_reappeared_pretty,omitempty"`
	DatetimeIgnoredError         string `json:"datetime_ignored_error,omitempty"`
	DatetimeIgnoredErrorPretty   string `json:"datetime_ignored_error_pretty,omitempty"`
	DateFirstSeen                string `json:"date_first_seen,omitempty"`
	DateFirstSeenPretty          string `json:"date_first_seen_pretty,omitempty"`
	DateLastSeen                 string `json:"date_last_seen,omitempty"`
	DateLastSeenPretty           string `json:"date_last_seen_pretty,omitempty"`
}

// CrashesResult is the answer of crashes.list.
type CrashesResult struct {
	IDSite     int     `json:"idSite"`
	Crashes    []Crash `json:"crashes"`
	HasMore    bool    `json:"has_more"`
	NextCursor string  `json:"next_cursor,omitempty"`
}

// CrashGroup is a parent crash with the crashes merged into it.
type CrashGroup struct {
	ParentIDLogCrash int     `json:"parent_idLogCrash"`
	Crashes          []Crash `json:"crashes"`
}

// CrashGroupsResult is the answer of crash-groups.list.
type CrashGroupsResult struct {
	IDSite     int          `json:"idSite"`
	Groups     []CrashGroup `json:"groups"`
	HasMore    bool         `json:"has_more"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

// CrashTypesResult is the answer of crash-types.list.
type CrashTypesResult struct {
	IDSite     int      `json:"idSite"`
	CrashTypes []string `json:"crash_types"`
	HasMore    bool     `json:"has_more"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

// crashJSON is a crash as CrashAnalytics reports it. Members are read loosely and none is trusted. The members
// the reference types as any value are kept raw, since they are a date or null in practice.
type crashJSON struct {
	ID                           *loose          `json:"idlogcrash"`
	IDSite                       *loose          `json:"idsite"`
	GroupID                      json.RawMessage `json:"group_idlogcrash"`
	CrashType                    *loose          `json:"crash_type"`
	Message                      *loose          `json:"message"`
	ResourceURI                  *loose          `json:"resource_uri"`
	ResourceLine                 *loose          `json:"resource_line"`
	ResourceColumn               *loose          `json:"resource_column"`
	StackTrace                   *loose          `json:"stack_trace"`
	Category                     *loose          `json:"category"`
	CrashPageURL                 *loose          `json:"crash_page_url"`
	DatetimeFirstSeen            *loose          `json:"datetime_first_seen"`
	DatetimeFirstSeenPretty      *loose          `json:"datetime_first_seen_pretty"`
	DatetimeLastSeen             *loose          `json:"datetime_last_seen"`
	DatetimeLastSeenPretty       *loose          `json:"datetime_last_seen_pretty"`
	DatetimeLastReappeared       json.RawMessage `json:"datetime_last_reappeared"`
	DatetimeLastReappearedPretty json.RawMessage `json:"datetime_last_reappeared_pretty"`
	DatetimeIgnoredError         json.RawMessage `json:"datetime_ignored_error"`
	DatetimeIgnoredErrorPretty   json.RawMessage `json:"datetime_ignored_error_pretty"`
	DateFirstSeen                *loose          `json:"date_first_seen"`
	DateFirstSeenPretty          *loose          `json:"date_first_seen_pretty"`
	DateLastSeen                 *loose          `json:"date_last_seen"`
	DateLastSeenPretty           *loose          `json:"date_last_seen_pretty"`
}

// rawNumber reads a number from a member kept raw; anything else, and null, is absent.
func rawNumber(raw json.RawMessage) *int {
	var value loose
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value.number()
}

// crash converts a row; ok is false for a row without a usable crash ID. The website is the caller's, proven
// before the call.
func (c crashJSON) crash(site int) (Crash, bool) {
	id := c.ID.number()
	if id == nil || *id < 1 {
		return Crash{}, false
	}
	group := rawNumber(c.GroupID)
	if group != nil && *group < 1 {
		group = nil
	}
	return Crash{IDLogCrash: *id, IDSite: site, GroupIDLogCrash: group, CrashType: c.CrashType.str(maxStringLength),
		Message: c.Message.str(maxUrlLength), ResourceURI: c.ResourceURI.str(maxUrlLength), ResourceLine: c.ResourceLine.number(),
		ResourceColumn: c.ResourceColumn.number(), StackTrace: c.StackTrace.str(maxCrashStack), Category: c.Category.str(maxStringLength),
		CrashPageURL: c.CrashPageURL.str(maxUrlLength), DatetimeFirstSeen: c.DatetimeFirstSeen.str(maxStringLength),
		DatetimeFirstSeenPretty: c.DatetimeFirstSeenPretty.str(maxStringLength), DatetimeLastSeen: c.DatetimeLastSeen.str(maxStringLength),
		DatetimeLastSeenPretty:       c.DatetimeLastSeenPretty.str(maxStringLength),
		DatetimeLastReappeared:       funnelText(c.DatetimeLastReappeared, maxStringLength),
		DatetimeLastReappearedPretty: funnelText(c.DatetimeLastReappearedPretty, maxStringLength),
		DatetimeIgnoredError:         funnelText(c.DatetimeIgnoredError, maxStringLength),
		DatetimeIgnoredErrorPretty:   funnelText(c.DatetimeIgnoredErrorPretty, maxStringLength),
		DateFirstSeen:                c.DateFirstSeen.str(maxStringLength), DateFirstSeenPretty: c.DateFirstSeenPretty.str(maxStringLength),
		DateLastSeen: c.DateLastSeen.str(maxStringLength), DateLastSeenPretty: c.DateLastSeenPretty.str(maxStringLength)}, true
}

// inSite reports whether the row names the website. A row without a website cannot be proven and is dropped.
func (c crashJSON) inSite(site int) bool {
	row := c.IDSite.number()
	return row != nil && *row == site
}

func crashNotFound(op string) error {
	return &provider.Error{Class: provider.ClassNotFound, Op: op,
		Message: "Matomo has no such crash for this website inside the targets of this connection"}
}

// crashRowLimit is the filter_limit for a batch that ends at want rows, never above what Matomo returns.
func crashRowLimit(want int) string { return strconv.Itoa(min(want, maxCrashRows)) }

// crashWindow sets the rows of a crash list. Only the start of the list is asked for, never filter_offset: the
// public demo applied the offset twice, so a window from the middle cut off too many rows.
func crashWindow(params url.Values, want int) {
	params.Set("filter_offset", "0")
	params.Set("filter_limit", crashRowLimit(want))
}

func invokeCrashesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list crashes"
	var args struct {
		IDSite     *int   `json:"idSite"`
		SortColumn string `json:"sort_column"`
		SortOrder  string `json:"sort_order"`
		Limit      int    `json:"limit"`
		Cursor     string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	column, order := args.SortColumn, args.SortOrder
	if column == "" {
		column = "datetime_last_seen"
	}
	if order == "" {
		order = "desc"
	}
	if !slices.Contains(crashSortColumns, column) {
		return nil, invalidArgument("sort_column must be one of " + strings.Join(crashSortColumns, ", "))
	}
	if order != "asc" && order != "desc" {
		return nil, invalidArgument("sort_order must be asc or desc")
	}
	paging, err := startPage(provider.CursorBinding(crashesList.ID, scopeBinding(resolved), site, column, order),
		args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(site)}, "filter_sort_column": {column}, "filter_sort_order": {order}}
	crashWindow(params, paging.want())
	var answer json.RawMessage
	if err := client.call(ctx, op, crashesRequirement(crashesList.ID), "CrashAnalytics.getAllCrashes", params, &answer); err != nil {
		return nil, err
	}
	rows, valid := readRows[crashJSON](unwrapValue(answer))
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid crash list")
	}
	crashes := []Crash{}
	for i := range rows {
		// A row of another website, or one without a website, is dropped.
		if !rows[i].inSite(site) {
			continue
		}
		if crash, usable := rows[i].crash(site); usable {
			crashes = append(crashes, crash)
		}
	}
	batch, next := slice(paging, crashes)
	return &CrashesResult{IDSite: site, Crashes: batch, HasMore: next != "", NextCursor: next}, nil
}

type crashProofJSON struct {
	ID     *loose `json:"idlogcrash"`
	IDSite *loose `json:"idsite"`
}

// proveCrash is the one read that ties a crash ID to the website: the summary names no website, so the crash
// must appear in the list of the website. A crash outside the first rows of that list cannot be proven.
func (c *Client) proveCrash(ctx context.Context, op string, req requirement, site, id int) error {
	params := url.Values{"idSite": {strconv.Itoa(site)}}
	crashWindow(params, maxCrashRows)
	var answer json.RawMessage
	if err := c.call(ctx, op, req, "CrashAnalytics.getAllCrashes", params, &answer); err != nil {
		return err
	}
	rows, valid := readRows[crashProofJSON](unwrapValue(answer))
	if !valid {
		return provider.InvalidResponse(op, "Matomo returned an invalid crash list")
	}
	for _, row := range rows {
		found, owner := row.ID.number(), row.IDSite.number()
		if found != nil && *found == id && owner != nil && *owner == site {
			return nil
		}
	}
	return crashNotFound(op)
}

func invokeCrashesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get crash"
	var args struct {
		IDSite     *int `json:"idSite"`
		IDLogCrash *int `json:"idLogCrash"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if args.IDLogCrash == nil || *args.IDLogCrash < 1 || *args.IDLogCrash > maxCrashID {
		return nil, invalidArgument("idLogCrash must be a positive integer crash ID from matomo.crashes.list")
	}
	site, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := crashesRequirement(crashesGet.ID)
	id := *args.IDLogCrash
	if err := client.proveCrash(ctx, op, req, site, id); err != nil {
		return nil, err
	}
	var answer json.RawMessage
	params := url.Values{"idSite": {strconv.Itoa(site)}, "idLogCrash": {strconv.Itoa(id)}}
	if err := client.call(ctx, op, req, "CrashAnalytics.getCrashSummary", params, &answer); err != nil {
		return nil, err
	}
	trimmed := unwrapValue(answer)
	switch string(trimmed) {
	case "null", "false", "[]", "{}", "":
		return nil, crashNotFound(op)
	}
	rows, valid := groupMembers(trimmed)
	if !valid || len(rows) != 1 {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid crash")
	}
	crash, ok := rows[0].crash(site)
	if !ok {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid crash")
	}
	if crash.IDLogCrash != id {
		return nil, provider.InvalidResponse(op, "Matomo returned another crash than asked for")
	}
	// A summary that names a website must name this one.
	if rows[0].IDSite != nil && !rows[0].inSite(site) {
		return nil, crashNotFound(op)
	}
	return &crash, nil
}

// groupMembers reads the crashes of one group: a list, a list keyed by position, or one crash.
func groupMembers(raw json.RawMessage) ([]crashJSON, bool) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.HasPrefix(trimmed, []byte("{")) {
		var probe map[string]json.RawMessage
		if json.Unmarshal(trimmed, &probe) == nil {
			if _, single := probe["idlogcrash"]; single {
				var row crashJSON
				if json.Unmarshal(trimmed, &row) != nil {
					return nil, false
				}
				return []crashJSON{row}, true
			}
		}
	}
	return readRows[crashJSON](trimmed)
}

func invokeCrashGroupsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list crash groups"
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
	paging, err := startPage(provider.CursorBinding(crashGroupsList.ID, scopeBinding(resolved), site), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	params := url.Values{"idSite": {strconv.Itoa(site)}}
	if err := client.call(ctx, op, crashesRequirement(crashGroupsList.ID), "CrashAnalytics.getCrashGroups", params, &answer); err != nil {
		return nil, err
	}
	trimmed := unwrapValue(answer)
	var keyed map[string]json.RawMessage
	switch {
	case string(trimmed) == "[]" || string(trimmed) == "null" || string(trimmed) == "false":
	case !bytes.HasPrefix(trimmed, []byte("{")) || json.Unmarshal(trimmed, &keyed) != nil:
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid crash group list")
	}
	parents := make([]int, 0, len(keyed))
	members := map[int]json.RawMessage{}
	for key, value := range keyed {
		if parent, err := strconv.Atoi(key); err == nil && parent >= 1 {
			parents = append(parents, parent)
			members[parent] = value
		}
	}
	sort.Ints(parents)
	groups := []CrashGroup{}
	for _, parent := range parents {
		rows, valid := groupMembers(members[parent])
		if !valid {
			return nil, provider.InvalidResponse(op, "Matomo returned an invalid crash group")
		}
		group := CrashGroup{ParentIDLogCrash: parent, Crashes: []Crash{}}
		for i := range rows {
			if !rows[i].inSite(site) || len(group.Crashes) == maxCrashGroupMembers {
				continue
			}
			if crash, usable := rows[i].crash(site); usable {
				group.Crashes = append(group.Crashes, crash)
			}
		}
		if len(group.Crashes) > 0 {
			groups = append(groups, group)
		}
	}
	batch, next := slice(paging, groups)
	return &CrashGroupsResult{IDSite: site, Groups: batch, HasMore: next != "", NextCursor: next}, nil
}

func invokeCrashTypesList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list crash types"
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
	paging, err := startPage(provider.CursorBinding(crashTypesList.ID, scopeBinding(resolved), site), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	params := url.Values{"idSite": {strconv.Itoa(site)}, "filter_limit": {crashRowLimit(paging.want())}}
	if err := client.call(ctx, op, crashesRequirement(crashTypesList.ID), "CrashAnalytics.getCrashTypes", params, &answer); err != nil {
		return nil, err
	}
	rows, valid := readRows[loose](unwrapValue(answer))
	if !valid {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid crash type list")
	}
	types := []string{}
	for i := range rows {
		if text := rows[i].str(maxStringLength); text != "" {
			types = append(types, text)
		}
	}
	batch, next := slice(paging, types)
	return &CrashTypesResult{IDSite: site, CrashTypes: batch, HasMore: next != "", NextCursor: next}, nil
}

func crashesOperations() []capability.Operation {
	return append([]capability.Operation{
		{Descriptor: crashesList, Handler: capability.Handler(invokeCrashesList)},
		{Descriptor: crashesGet, Handler: capability.Handler(invokeCrashesGet)},
		{Descriptor: crashGroupsList, Handler: capability.Handler(invokeCrashGroupsList)},
		{Descriptor: crashTypesList, Handler: capability.Handler(invokeCrashTypesList)},
	}, append(crashReadOperations(), crashChangeOperations()...)...)
}
