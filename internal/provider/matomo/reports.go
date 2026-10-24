package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"sort"
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

const (
	reportsSensitive = "matomo-report-catalog"
	// catalogTTL is how long the report catalog of one website is trusted. It holds structure only; a plugin
	// or goal change shows after at most this long.
	catalogTTL = 5 * time.Minute
	// maxCatalogEntries bounds the catalogs held per process; maxCatalogReports bounds one catalog.
	maxCatalogEntries = 64
	maxCatalogReports = 2000
	maxReportItems    = 200
	maxReportText     = 2048
	maxQueryLength    = 100
	languagePattern   = `^[a-z]{2,3}(-[a-z]{2,4})?$`
)

// Pair is a name with a value or label. Metrics are metric name and label; parameters are parameter name and
// the value the report needs for it.
type Pair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Report is one entry of the report catalog of a website. Text is untrusted provider data. ID is the
// uniqueId, stable only within the instance; Module, Action, and Parameters name what API.getProcessedReport
// is called with.
type Report struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Category             string `json:"category"`
	Subcategory          string `json:"subcategory,omitempty"`
	Module               string `json:"module,omitempty"`
	Action               string `json:"action,omitempty"`
	Parameters           []Pair `json:"parameters,omitempty"`
	Dimension            string `json:"dimension,omitempty"`
	Documentation        string `json:"documentation,omitempty"`
	Metrics              []Pair `json:"metrics,omitempty"`
	ProcessedMetrics     []Pair `json:"processed_metrics,omitempty"`
	IsSubtableReport     bool   `json:"is_subtable_report,omitempty"`
	SubtableAction       string `json:"subtable_action,omitempty"`
	MetricsDocumentation []Pair `json:"metrics_documentation,omitempty"`
}

// reportJSON is a catalog row as API.getReportMetadata answers. Maps arrive as [] when empty, so they stay raw.
type reportJSON struct {
	UniqueID             *loose          `json:"uniqueId"`
	Name                 *loose          `json:"name"`
	Category             *loose          `json:"category"`
	Subcategory          *loose          `json:"subcategory"`
	Module               *loose          `json:"module"`
	Action               *loose          `json:"action"`
	Parameters           json.RawMessage `json:"parameters"`
	Dimension            *loose          `json:"dimension"`
	Documentation        *loose          `json:"documentation"`
	Metrics              json.RawMessage `json:"metrics"`
	ProcessedMetrics     json.RawMessage `json:"processedMetrics"`
	MetricsDocumentation json.RawMessage `json:"metricsDocumentation"`
	IsSubtableReport     *loose          `json:"isSubtableReport"`
	SubtableAction       *loose          `json:"actionToLoadSubTables"`
}

// pairs reads a name-to-scalar map that Matomo answers as an object, or as [] or false when it is empty.
// Entries are ordered by name and bounded; a non-scalar value is dropped.
func pairs(raw json.RawMessage) []Pair {
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil {
		return nil
	}
	names := make([]string, 0, len(members))
	for name := range members {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []Pair
	for _, name := range names {
		var value loose
		if len(out) >= maxReportItems || value.UnmarshalJSON(members[name]) != nil {
			continue
		}
		out = append(out, Pair{Name: bounded(name), Value: value.str(maxReportText)})
	}
	return out
}

// report converts a row; ok is false for a row without an ID or a name.
func (r reportJSON) report(withDocs bool) (Report, bool) {
	report := Report{
		ID: r.UniqueID.str(maxStringLength), Name: r.Name.str(maxStringLength), Category: r.Category.str(maxStringLength),
		Subcategory: r.Subcategory.str(maxStringLength), Module: r.Module.str(maxStringLength),
		Action: r.Action.str(maxStringLength), Parameters: pairs(r.Parameters), Dimension: r.Dimension.str(maxStringLength),
		Documentation: r.Documentation.str(maxReportText), Metrics: pairs(r.Metrics),
		ProcessedMetrics: pairs(r.ProcessedMetrics), SubtableAction: r.SubtableAction.str(maxStringLength),
	}
	if flag := r.IsSubtableReport.flag(); flag != nil {
		report.IsSubtableReport = *flag
	}
	if withDocs {
		report.MetricsDocumentation = pairs(r.MetricsDocumentation)
	}
	return report, report.ID != "" && report.Name != ""
}

// Catalog is the report catalog of one website in one language, in the order Matomo lists it.
type Catalog struct {
	reports []Report
}

// Find returns the report with this catalog ID.
func (c *Catalog) Find(id string) (Report, bool) {
	for _, report := range c.reports {
		if report.ID == id {
			return report, true
		}
	}
	return Report{}, false
}

// Require is Find with the refusal of a tool: a report the website's catalog lacks is not-found, whatever the
// reason, because a report is available only where the instance lists it.
func (c *Catalog) Require(op, id string) (Report, error) {
	report, ok := c.Find(id)
	if !ok {
		return Report{}, &provider.Error{Class: provider.ClassNotFound, Op: op,
			Message: "the report catalog of this website holds no report with this id; read it with matomo.reports.list"}
	}
	return report, nil
}

// Reports returns the catalog entries in catalog order.
func (c *Catalog) Reports() []Report { return slices.Clone(c.reports) }

// ReportOptions are the optional parameters of API.getReportMetadata that shape the catalog. The zero value
// asks for the default of the instance. Every field is part of the cache key.
type ReportOptions struct {
	// Language is the language code of the labels; empty for the language of the token's user.
	Language string
	// Period is day, week, month, year, or range; Date is the date or range it is resolved for. Some reports
	// depend on them.
	Period string
	Date   string
	// ShowSubtableReports also lists the subtable reports.
	ShowSubtableReports bool
}

type catalogKey struct {
	instance instanceKey
	site     int
	options  ReportOptions
}

type catalogEntry struct {
	catalog *Catalog
	expires time.Time
}

// catalogs holds report catalogs per instance and token, website, and language, on the clock of the instance
// cache. Only a successful read is held, and only structure, never report data. The token enters the key as
// the hash of instanceKeyOf.
var catalogs = struct {
	mu    sync.Mutex
	known map[catalogKey]catalogEntry
}{known: map[catalogKey]catalogEntry{}}

func resetCatalogs() {
	catalogs.mu.Lock()
	defer catalogs.mu.Unlock()
	catalogs.known = map[catalogKey]catalogEntry{}
}

func holdCatalog(key catalogKey, catalog *Catalog) {
	catalogs.mu.Lock()
	defer catalogs.mu.Unlock()
	at := now()
	for held, entry := range catalogs.known {
		if !at.Before(entry.expires) {
			delete(catalogs.known, held)
		}
	}
	for len(catalogs.known) >= maxCatalogEntries {
		var oldest catalogKey
		var soonest time.Time
		for held, entry := range catalogs.known {
			if soonest.IsZero() || entry.expires.Before(soonest) {
				oldest, soonest = held, entry.expires
			}
		}
		delete(catalogs.known, oldest)
	}
	catalogs.known[key] = catalogEntry{catalog: catalog, expires: at.Add(catalogTTL)}
}

// ReportCatalog returns the report catalog of one website, from the short-lived cache or from one
// API.getReportMetadata read. options carry the optional parameters of that method. It is the entry point
// for a tool that must validate a report choice, such as one that calls API.getProcessedReport:
// Catalog.Require(op, id) answers not-found for a report the website does not list.
// The caller has already checked idSite against the targets.
func (c *Client) ReportCatalog(ctx context.Context, op string, req requirement, idSite int, options ReportOptions) (*Catalog, error) {
	key := catalogKey{instance: c.key, site: idSite, options: options}
	catalogs.mu.Lock()
	entry, ok := catalogs.known[key]
	catalogs.mu.Unlock()
	if ok && now().Before(entry.expires) {
		return entry.catalog, nil
	}
	catalog, err := c.fetchCatalog(ctx, op, req, idSite, options, false)
	if err != nil {
		return nil, err
	}
	holdCatalog(key, catalog)
	return catalog, nil
}

// fetchCatalog reads the catalog from the instance. With docs the metrics documentation is requested and
// kept, which makes the answer larger; such a catalog is never held.
func (c *Client) fetchCatalog(ctx context.Context, op string, req requirement, idSite int, options ReportOptions,
	docs bool) (*Catalog, error) {
	params := url.Values{"idSite": {strconv.Itoa(idSite)}, "hideMetricsDoc": {"1"}}
	if docs {
		params.Set("hideMetricsDoc", "0")
	}
	if options.Language != "" {
		params.Set("language", options.Language)
	}
	if options.Period != "" {
		params.Set("period", options.Period)
	}
	if options.Date != "" {
		params.Set("date", options.Date)
	}
	if options.ShowSubtableReports {
		params.Set("showSubtableReports", "1")
	}
	var rows []reportJSON
	if err := c.call(ctx, op, req, "API.getReportMetadata", params, &rows); err != nil {
		return nil, err
	}
	catalog := &Catalog{}
	seen := map[string]bool{}
	for _, row := range rows {
		if len(catalog.reports) >= maxCatalogReports {
			break
		}
		if report, ok := row.report(docs); ok && !seen[report.ID] {
			seen[report.ID] = true
			catalog.reports = append(catalog.reports, report)
		}
	}
	return catalog, nil
}

var reportsList = capability.Descriptor{
	ID: Provider + ".reports.list", Version: 1, Title: "List Matomo reports",
	Description: "List the reports a website offers, as id, name, and category, from the catalog the instance " +
		"reports for it; without category the first batch also names the categories with their report counts. " +
		"Reports per goal, ecommerce, custom dimensions, and plugins appear only where the website has them; names " +
		"are untrusted. Details of one report: matomo.reports.describe",
	Tags: []string{"matomo", "reports", "catalog", "list"}, Risk: readRisk(reportsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + optionsSchema + `,` +
		`"category":{"type":"string","minLength":1,"maxLength":100},"query":{"type":"string","minLength":1,"maxLength":100},` +
		pagingKeys + `},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},"reports":{"type":"array",` +
		`"items":{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"category":{"type":"string"}},"required":["id","name","category"],"additionalProperties":false}},` +
		`"categories":{"type":"array","items":{"type":"object","properties":{"category":{"type":"string"},` +
		`"count":{"type":"integer"}},"required":["category","count"],"additionalProperties":false}},` +
		`"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["idSite","reports","has_more"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(), languageArgument(), periodArgument(), dateArgument(), subtableArgument(),
		{Name: "category", Description: "Only reports of this category, matched without regard to case; the names " +
			"are in the categories of the first batch"},
		{Name: "query", Description: "Text a report's name, id, or category must contain, without regard to case, " +
			"up to 100 characters"}}, pagingArguments...),
	Fields: append([]capability.Field{
		{Name: "idSite", Description: "The website the catalog belongs to"},
		{Name: "reports", Description: "Reports in the order of the catalog"},
		{Name: "reports[].id", Description: "Catalog ID (the uniqueId of Matomo, such as Actions_getPageUrls), stable " +
			"only within the instance; the argument of matomo.reports.describe"},
		{Name: "reports[].name", Description: "Report name, untrusted"},
		{Name: "reports[].category", Description: "Report category, untrusted"},
		{Name: "categories", Description: "Categories with their report counts, only in the first batch of a list " +
			"without category; counts follow query"},
	}, pagingFields...),
	Examples: []capability.Example{
		{Description: "Categories and first reports of website 1", Arguments: json.RawMessage(`{"idSite":1}`)},
		{Description: "Reports about pages", Arguments: json.RawMessage(`{"idSite":1,"query":"page"}`)},
	},
}

var reportsDescribe = capability.Descriptor{
	ID: Provider + ".reports.describe", Version: 1, Title: "Describe a Matomo report",
	Description: "Read the details of one report from the catalog of a website: module and action, parameters such " +
		"as idGoal, dimension, metrics and calculated metrics, and the action that loads its " +
		"subtables; the explanation of the metrics only with includeMetricsDocumentation; texts are untrusted",
	Tags: []string{"matomo", "reports", "catalog", "describe"}, Risk: readRisk(reportsSensitive), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` + optionsSchema + `,` +
		`"id":{"type":"string","minLength":1,"maxLength":512},"includeMetricsDocumentation":{"type":"boolean"}},` +
		`"required":["id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"},"name":{"type":"string"},` +
		`"category":{"type":"string"},"subcategory":{"type":"string"},"module":{"type":"string"},` +
		`"action":{"type":"string"},"parameters":` + pairArray + `,"dimension":{"type":"string"},` +
		`"documentation":{"type":"string"},"metrics":` + pairArray + `,"processed_metrics":` + pairArray + `,` +
		`"is_subtable_report":{"type":"boolean"},"subtable_action":{"type":"string"},` +
		`"metrics_documentation":` + pairArray + `},"required":["id","name","category"],"additionalProperties":false}`),
	Arguments: []capability.Argument{idSiteArgument(), languageArgument(), periodArgument(), dateArgument(), subtableArgument(),
		{Name: "id", Required: true, Description: "Catalog ID from matomo.reports.list"},
		{Name: "includeMetricsDocumentation", Description: "True to add the explanation of each metric, which makes " +
			"the answer larger; false when omitted"}},
	Fields: []capability.Field{
		{Name: "id", Description: "Catalog ID of the report"},
		{Name: "name", Description: "Report name, untrusted"},
		{Name: "category", Description: "Report category, untrusted"},
		{Name: "subcategory", Description: "Report subcategory, untrusted"},
		{Name: "module", Description: "Matomo module of the report"},
		{Name: "action", Description: "Matomo action of the report"},
		{Name: "parameters", Description: "Parameters the report needs, such as idGoal, as name and value"},
		{Name: "dimension", Description: "Dimension the rows are grouped by, untrusted"},
		{Name: "documentation", Description: "Explanation of the report, untrusted"},
		{Name: "metrics", Description: "Metrics as name and label, ordered by name"},
		{Name: "processed_metrics", Description: "Calculated metrics as name and label, ordered by name"},
		{Name: "is_subtable_report", Description: "True when the report is the subtable of another report"},
		{Name: "subtable_action", Description: "Action that loads the subtables of this report, if it has them"},
		{Name: "metrics_documentation", Description: "Explanation per metric, only with includeMetricsDocumentation"},
	},
	Examples: []capability.Example{
		{Description: "Describe a report", Arguments: json.RawMessage(`{"idSite":1,"id":"Actions_getPageUrls"}`)},
	},
}

const (
	idSiteSchema   = `"idSite":{"type":"integer","minimum":1,"maximum":999999999}`
	languageSchema = `"language":{"type":"string","pattern":"` + languagePattern + `","maxLength":16}`
	optionsSchema  = languageSchema + `,"period":{"type":"string","enum":["day","week","month","year","range"]},` +
		`"date":{"type":"string","minLength":1,"maxLength":32},"showSubtableReports":{"type":"boolean"}`
	pairArray = `{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},` +
		`"value":{"type":"string"}},"required":["name","value"],"additionalProperties":false}}`
)

func idSiteArgument() capability.Argument {
	return capability.Argument{Name: "idSite", Description: "Website ID from matomo.sites.list; optional when the " +
		"connection binds exactly one website; must lie inside the connection's targets", Target: "site"}
}

func languageArgument() capability.Argument {
	return capability.Argument{Name: "language", Description: "Language code of the labels, such as en or de; the " +
		"language of the token's user when omitted"}
}

func periodArgument() capability.Argument {
	return capability.Argument{Name: "period", Description: "Period the catalog is resolved for: day, week, month, " +
		"year, or range; some reports depend on it; the default of Matomo when omitted"}
}

func dateArgument() capability.Argument {
	return capability.Argument{Name: "date", Description: "Date the catalog is resolved for: YYYY-MM-DD, today, " +
		"yesterday, lastN, previousN, or a range YYYY-MM-DD,YYYY-MM-DD"}
}

func subtableArgument() capability.Argument {
	return capability.Argument{Name: "showSubtableReports", Description: "True to list the subtable reports too; " +
		"false when omitted"}
}

var periods = []string{"day", "week", "month", "year", "range"}

var datePattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}(,\d{4}-\d{2}-\d{2})?|today|yesterday|(last|previous)\d{1,4})$`)

// reportOptions validates the optional catalog arguments.
func reportOptions(language, period, date string, subtables bool) (ReportOptions, error) {
	if !validLanguage(language) {
		return ReportOptions{}, invalidArgument("language must be a language code such as en or de")
	}
	if period != "" && !slices.Contains(periods, period) {
		return ReportOptions{}, invalidArgument("period must be day, week, month, year, or range")
	}
	if date != "" && (len(date) > 32 || !datePattern.MatchString(date)) {
		return ReportOptions{}, invalidArgument("date must be YYYY-MM-DD, today, yesterday, lastN, previousN, " +
			"or a range YYYY-MM-DD,YYYY-MM-DD")
	}
	return ReportOptions{Language: language, Period: period, Date: date, ShowSubtableReports: subtables}, nil
}

func validLanguage(code string) bool {
	if code == "" {
		return true
	}
	if len(code) > 16 {
		return false
	}
	head, tail, cut := strings.Cut(code, "-")
	letters := func(s string, min, max int) bool {
		if len(s) < min || len(s) > max {
			return false
		}
		for _, r := range s {
			if r < 'a' || r > 'z' {
				return false
			}
		}
		return true
	}
	return letters(head, 2, 3) && (!cut || letters(tail, 2, 4))
}

// CategoryCount is one category of a catalog with the number of its entries.
type CategoryCount struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// ReportRef is the compact form of a catalog entry.
type ReportRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

// ReportsResult is the answer of reports.list.
type ReportsResult struct {
	IDSite     int             `json:"idSite"`
	Reports    []ReportRef     `json:"reports"`
	Categories []CategoryCount `json:"categories,omitempty"`
	HasMore    bool            `json:"has_more"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

func invokeReportsList(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "list reports"
	var args struct {
		IDSite   *int   `json:"idSite"`
		Language string `json:"language"`
		Period   string `json:"period"`
		Date     string `json:"date"`
		Subs     bool   `json:"showSubtableReports"`
		Category string `json:"category"`
		Query    string `json:"query"`
		Limit    int    `json:"limit"`
		Cursor   string `json:"cursor"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	options, err := reportOptions(args.Language, args.Period, args.Date, args.Subs)
	if err != nil {
		return nil, err
	}
	category, query := strings.TrimSpace(args.Category), strings.TrimSpace(args.Query)
	if args.Category != "" && category == "" || args.Query != "" && query == "" ||
		len(category) > maxQueryLength || len(query) > maxQueryLength {
		return nil, invalidArgument("category and query must hold 1 to 100 characters")
	}
	binding := provider.CursorBinding(reportsList.ID, scopeBinding(resolved), id, options, category, query)
	paging, err := startPage(binding, args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	catalog, err := client.ReportCatalog(ctx, op, baseRequirement(reportsList.ID), id, options)
	if err != nil {
		return nil, err
	}
	var matched []ReportRef
	var categories []CategoryCount
	for _, report := range catalog.reports {
		if query != "" && !containsFold(report.Name, query) && !containsFold(report.ID, query) &&
			!containsFold(report.Category, query) {
			continue
		}
		if category == "" {
			if i := slices.IndexFunc(categories, func(c CategoryCount) bool { return c.Category == report.Category }); i >= 0 {
				categories[i].Count++
			} else {
				categories = append(categories, CategoryCount{Category: report.Category, Count: 1})
			}
		} else if !strings.EqualFold(report.Category, category) {
			continue
		}
		matched = append(matched, ReportRef{ID: report.ID, Name: report.Name, Category: report.Category})
	}
	batch, next := slice(paging, matched)
	if batch == nil {
		batch = []ReportRef{}
	}
	result := &ReportsResult{IDSite: id, Reports: batch, HasMore: next != "", NextCursor: next}
	if args.Cursor == "" {
		result.Categories = categories
	}
	return result, nil
}

func invokeReportsDescribe(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "describe report"
	var args struct {
		IDSite   *int   `json:"idSite"`
		Language string `json:"language"`
		Period   string `json:"period"`
		Date     string `json:"date"`
		Subs     bool   `json:"showSubtableReports"`
		ID       string `json:"id"`
		Docs     bool   `json:"includeMetricsDocumentation"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	options, err := reportOptions(args.Language, args.Period, args.Date, args.Subs)
	if err != nil {
		return nil, err
	}
	if args.ID == "" || len(args.ID) > maxStringLength {
		return nil, invalidArgument("id must be a catalog ID from matomo.reports.list")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := baseRequirement(reportsDescribe.ID)
	// The catalog decides what exists. The documentation is the one detail the cached catalog lacks, so it
	// costs a second, uncached read after the catalog has proved the report.
	catalog, err := client.ReportCatalog(ctx, op, req, id, options)
	if err != nil {
		return nil, err
	}
	report, err := catalog.Require(op, args.ID)
	if err != nil {
		return nil, err
	}
	if args.Docs {
		full, err := client.fetchCatalog(ctx, op, req, id, options, true)
		if err != nil {
			return nil, err
		}
		if detailed, ok := full.Find(args.ID); ok {
			report = detailed
		}
	}
	return report, nil
}

func containsFold(text, part string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(part))
}

// scopeBinding is the part of a cursor binding that ties it to the targets of the connection.
func scopeBinding(resolved *config.Resolved) []any {
	bound, err := boundScope(resolved)
	if err != nil {
		return nil
	}
	sites := slices.Clone(bound.sites)
	slices.Sort(sites)
	return []any{bound.allSites, sites}
}
