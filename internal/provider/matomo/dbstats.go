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

const dbStatsPlugin = "DBStats"

// dbShape is how a DBStats method answers.
type dbShape int

const (
	dbTable dbShape = iota
	dbObject
	dbTriple
)

// dbSection is one DBStats method. Every section is one Matomo request: the sections differ in cost, because
// the individual summaries group every archive table by name, so a call never reads more than one.
type dbSection struct {
	name, method string
	shape        dbShape
	forceCache   bool
}

var dbSections = []dbSection{
	{name: "general-information", method: "DBStats.getGeneralInformation", shape: dbTriple},
	{name: "status", method: "DBStats.getDBStatus", shape: dbObject},
	{name: "usage-summary", method: "DBStats.getDatabaseUsageSummary"},
	{name: "tracker-tables", method: "DBStats.getTrackerDataSummary"},
	{name: "metric-tables", method: "DBStats.getMetricDataSummary"},
	{name: "metric-tables-by-year", method: "DBStats.getMetricDataSummaryByYear"},
	{name: "report-tables", method: "DBStats.getReportDataSummary"},
	{name: "report-tables-by-year", method: "DBStats.getReportDataSummaryByYear"},
	{name: "admin-tables", method: "DBStats.getAdminDataSummary"},
	{name: "individual-reports", method: "DBStats.getIndividualReportsSummary", forceCache: true},
	{name: "individual-metrics", method: "DBStats.getIndividualMetricsSummary", forceCache: true},
}

func dbSectionNames() []string {
	names := make([]string, len(dbSections))
	for i, s := range dbSections {
		names[i] = s.name
	}
	return names
}

var databaseStatisticsGet = capability.Descriptor{
	ID: Provider + ".database-statistics.get", Version: 1, Title: "Get the Matomo database statistics",
	Description: "Read the size of the Matomo database, one section per call: general-information (websites, users, " +
		"total size), status (the database server's uptime, threads, queries, and open tables), usage-summary (tracker, " +
		"metric, report, and other data), the tables of one group (tracker-tables, metric-tables, report-tables, " +
		"admin-tables, and the metric and report tables by year), and the size per report or metric name " +
		"(individual-reports, individual-metrics; with force_cache Matomo recalculates them, which is slow on a large " +
		"database). Table sections give at most 100 rows per call unless limit says otherwise (1 to 1000), continued " +
		"with next_cursor. It needs Super User rights and a connection bound to the instance target; the DBStats " +
		"plugin must be activated; names are untrusted",
	Tags: []string{"matomo", "database", "statistics", "size", "instance", "get"}, Risk: readRisk(instanceSensitive),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{"section":{"type":"string","enum":[` +
		quotedList(dbSectionNames()) + `]},"force_cache":{"type":"boolean"},` + limitSchema +
		`},"required":["section"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"section":{"type":"string"},` +
		`"general_information":{"type":"object","properties":{"website_count":{},"user_count":{},"total_size":{}},` +
		`"additionalProperties":false},"status":{"type":"object"},"rows":{"type":"array","items":{"type":"object"}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["section"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{
		{Name: "section", Required: true, Description: "What to read: " + strings.Join(dbSectionNames(), ", ")},
		{Name: "force_cache", Description: "True to recalculate instead of reading Matomo's cache; only for " +
			"individual-reports and individual-metrics; false when omitted"},
	}, windowArguments("Rows")...),
	Fields: append([]capability.Field{
		{Name: "section", Description: "The section read"},
		{Name: "general_information", Description: "For general-information: website_count, user_count, and total_size " +
			"(bytes of data and indexes), as Matomo reports them"},
		{Name: "status", Description: "For status: the members the database server reports, such as Uptime, Threads, " +
			"Questions, Slow queries, Flush tables, and Open tables; a value Matomo cannot read is the text unavailable"},
		{Name: "rows", Description: "For the table sections: rows with label (a table or name, or tracker_data, " +
			"metric_data, report_data, and other_data for usage-summary, or a year for the by-year sections) and " +
			"data_size, index_size, and row_count in bytes and rows; the individual sections add blob_size and name_size"},
	}, windowFields...),
	Examples: []capability.Example{
		{Description: "Read the size summary", Arguments: json.RawMessage(`{"section":"usage-summary"}`)},
		{Description: "Read the log tables", Arguments: json.RawMessage(`{"section":"tracker-tables"}`)},
	},
}

// DatabaseGeneralInformation is the answer of DBStats.getGeneralInformation.
type DatabaseGeneralInformation struct {
	WebsiteCount any `json:"website_count"`
	UserCount    any `json:"user_count"`
	TotalSize    any `json:"total_size"`
}

// DatabaseStatisticsResult is the answer of database-statistics.get.
type DatabaseStatisticsResult struct {
	Section            string                      `json:"section"`
	GeneralInformation *DatabaseGeneralInformation `json:"general_information,omitempty"`
	Status             map[string]any              `json:"status,omitempty"`
	Rows               []map[string]any            `json:"rows,omitempty"`
	HasMore            bool                        `json:"has_more,omitempty"`
	NextCursor         string                      `json:"next_cursor,omitempty"`
}

func invokeDatabaseStatisticsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get database statistics"
	var args struct {
		Section    string `json:"section"`
		ForceCache *bool  `json:"force_cache"`
		Limit      int    `json:"limit"`
		Cursor     string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	// Checked here as well as in Reaches: the tool needs the instance target, whatever the caller sends.
	if err := requireInstance(bound); err != nil {
		return nil, err
	}
	index := slices.IndexFunc(dbSections, func(s dbSection) bool { return s.name == args.Section })
	if index < 0 {
		return nil, invalidArgument("section must be one of " + strings.Join(dbSectionNames(), ", "))
	}
	section := dbSections[index]
	if args.ForceCache != nil && !section.forceCache {
		return nil, invalidArgument("force_cache applies only to individual-reports and individual-metrics")
	}
	if section.shape != dbTable && (args.Limit != 0 || args.Cursor != "") {
		return nil, invalidArgument("limit and cursor apply only to the table sections")
	}
	window, err := startWindow(provider.CursorBinding(databaseStatisticsGet.ID, args.Section, args.ForceCache), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	if args.ForceCache != nil {
		params.Set("forceCache", strconv.FormatBool(*args.ForceCache))
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer json.RawMessage
	req := requirement{tool: databaseStatisticsGet.ID, plugin: dbStatsPlugin}
	if err := client.call(ctx, op, req, section.method, params, &answer); err != nil {
		return nil, err
	}
	result := &DatabaseStatisticsResult{Section: section.name}
	switch section.shape {
	case dbObject:
		if !isEmptyJSON(answer) {
			if result.Status = cleanObject(answer); result.Status == nil {
				return nil, provider.InvalidResponse(op, "Matomo returned the database status in a form this tool does not read")
			}
		}
	case dbTriple:
		info, ok := generalInformationOf(answer)
		if !ok {
			return nil, provider.InvalidResponse(op, "Matomo returned the general information in a form this tool does not read")
		}
		result.GeneralInformation = info
	default:
		rows, err := tableRows(op, answer)
		if err != nil {
			return nil, err
		}
		batch, more := cutWindow(window, rows)
		result.Rows, result.HasMore, result.NextCursor = batch, more, window.next(more)
	}
	return result, nil
}

// generalInformationOf reads the three values Matomo answers as a list, or as an object keyed 0, 1, and 2.
func generalInformationOf(raw json.RawMessage) (*DatabaseGeneralInformation, bool) {
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil {
		var keyed map[string]json.RawMessage
		if json.Unmarshal(raw, &keyed) != nil {
			return nil, false
		}
		for i := range 3 {
			values = append(values, keyed[strconv.Itoa(i)])
		}
	}
	if len(values) != 3 {
		return nil, false
	}
	read := func(raw json.RawMessage) (any, bool) {
		value, ok := decodeValue(raw)
		if !ok {
			return nil, false
		}
		return cleanValue(value, maxValueDepth)
	}
	var out [3]any
	for i, value := range values {
		var ok bool
		if out[i], ok = read(value); !ok || out[i] == nil {
			return nil, false
		}
	}
	return &DatabaseGeneralInformation{WebsiteCount: out[0], UserCount: out[1], TotalSize: out[2]}, true
}
