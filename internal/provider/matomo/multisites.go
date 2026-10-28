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
	viewSites  = "sites"
	viewSite   = "site"
	viewGroups = "groups"
)

var multiSitesGet = capability.Descriptor{
	ID: Provider + ".multi-sites.get", Version: 1, Title: "Compare Matomo websites",
	Description: "Compare the websites of the token in one period, from the MultiSites plugin: view sites " +
		"(MultiSites.getAll) lists visits, actions, pageviews, hits, revenue, and their change against the " +
		"previous period per website, view site (MultiSites.getOne) the same for one website, and view groups " +
		"(MultiSites.getAllWithGroups) the websites ordered into their groups with totals. Only websites inside " +
		"the connection's targets are returned: Matomo computes the comparison for every website of the token and " +
		"the other rows are dropped here, so a batch can hold fewer entries than limit, even none, while has_more is " +
		"true. Unless the connection reaches every website (the site target without exclusions) the group rows, " +
		"numSites, and totals are left out, because Matomo sums them over all websites of the token. Websites without visits are left out unless enhanced is " +
		"set. Names, groups, and addresses are untrusted. Archiving on demand can take long, so range periods and " +
		"large segments may time out",
	Tags: []string{"matomo", "multisites", "websites", "compare", "analytics"}, Risk: readRisk(reportDataSensitive),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"view":{"type":"string","enum":["sites","site","groups"]},` + idSiteSchema + `,` +
		`"period":{"type":"string","enum":["day","week","month","year","range"]},` +
		`"date":{"type":"string","minLength":1,"maxLength":32},` +
		`"segment":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxSegmentLength) + `},` +
		`"enhanced":{"type":"boolean"},` + maxPatternSchema + `,` +
		`"columns":{"type":"array","minItems":1,"maxItems":` + strconv.Itoa(maxReportColumns) + `,"items":` + columnSchema + `},` +
		`"sort_column":` + columnSchema + `,"sort_order":{"type":"string","enum":["asc","desc"]},` + pagingKeys + `},` +
		`"required":["view","period","date"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"view":{"type":"string"},` +
		`"period":{"type":"string"},"date":{"type":"string"},"last_date":{"type":"string"},` +
		`"num_sites":{"type":"integer"},"totals":{"type":"object"},"site":{"type":"object"},` +
		`"rows":{"type":"array","items":{"type":"object"}},"has_more":{"type":"boolean"},` +
		`"next_cursor":{"type":"string"}},"required":["view","period","date"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "view", Required: true, Description: "sites for the comparison of all websites, site for one " +
			"website (idSite), groups for the websites in their groups with totals"},
		{Name: "idSite", Description: "Only with view site: website ID from matomo.sites.list; optional when the " +
			"connection binds exactly one website; must lie inside the connection's targets", Target: "site"},
		{Name: "period", Required: true, Description: "day, week, month, year, or range"},
		{Name: "date", Required: true, Description: "YYYY-MM-DD, today, yesterday, or for period range a range " +
			"YYYY-MM-DD,YYYY-MM-DD or lastN or previousN. Several periods in one call are not available"},
		{Name: "segment", Description: "Segment expression such as browserName==Firefox; fields from " +
			"matomo.segments.fields, values URL-encoded; Matomo then leaves out the AI chatbot requests"},
		{Name: "enhanced", Description: "Only with view sites or site: true to add conversions, ecommerce orders, " +
			"and ecommerce revenue and to keep websites without visits; false when omitted"},
		{Name: "pattern", Description: "Only with view sites or groups: text a website's name, address, or group " +
			"must contain, up to 100 characters; all websites when omitted"},
		{Name: "columns", Description: "Only with view sites: metrics to return, such as nb_visits, nb_actions, " +
			"nb_pageviews, hits, revenue; all of them when omitted"},
		{Name: "sort_column", Description: "Only with view sites or groups: column to sort by before paging, such " +
			"as nb_visits; the order of Matomo when omitted"},
		{Name: "sort_order", Description: "asc or desc; desc when omitted"},
		pagingArguments[0], pagingArguments[1],
	},
	Fields: append([]capability.Field{
		{Name: "view", Description: "The view that was read"},
		{Name: "period", Description: "The period asked for"},
		{Name: "date", Description: "The date asked for"},
		{Name: "last_date", Description: "Only with view groups: the date of the previous period the change is " +
			"measured against"},
		{Name: "num_sites", Description: "Only with view groups and a connection that reaches every website: the number of " +
			"websites after pattern, groups not counted"},
		{Name: "totals", Description: "Only with view groups and a connection that reaches every website: sums over the " +
			"websites with their previous values"},
		{Name: "site", Description: "With view site: the metrics of the website as an object"},
		{Name: "rows", Description: "With view sites or groups: one object per website, named by label, with the " +
			"metrics, their change (such as visits_evolution), idsite, group, and main_url; with view groups also " +
			"the group rows (isGroup) when the connection reaches every website. Names, groups, and addresses are untrusted"},
	}, pagingFields...),
	Examples: []capability.Example{
		{Description: "Compare all websites for last month", Arguments: json.RawMessage(
			`{"view":"sites","period":"month","date":"2026-09-01","sort_column":"nb_visits"}`)},
		{Description: "One website with conversions", Arguments: json.RawMessage(
			`{"view":"site","idSite":1,"period":"day","date":"yesterday","enhanced":true}`)},
	},
}

// MultiSitesResult is the answer of multi-sites.get.
type MultiSitesResult struct {
	View       string           `json:"view"`
	Period     string           `json:"period"`
	Date       string           `json:"date"`
	LastDate   string           `json:"last_date,omitempty"`
	NumSites   *int             `json:"num_sites,omitempty"`
	Totals     map[string]any   `json:"totals,omitempty"`
	Site       map[string]any   `json:"site,omitempty"`
	Rows       []map[string]any `json:"rows,omitempty"`
	HasMore    bool             `json:"has_more"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

type multiSitesArguments struct {
	View       string   `json:"view"`
	IDSite     *int     `json:"idSite"`
	Period     string   `json:"period"`
	Date       string   `json:"date"`
	Segment    string   `json:"segment"`
	Enhanced   bool     `json:"enhanced"`
	Pattern    string   `json:"pattern"`
	Columns    []string `json:"columns"`
	SortColumn string   `json:"sort_column"`
	SortOrder  string   `json:"sort_order"`
	Limit      int      `json:"limit"`
	Cursor     string   `json:"cursor"`
}

// validate checks the arguments that need no instance; each argument belongs to certain views only.
func (a *multiSitesArguments) validate() error {
	paged := a.View == viewSites || a.View == viewGroups
	switch {
	case a.View != viewSites && a.View != viewSite && a.View != viewGroups:
		return invalidArgument("view must be sites, site, or groups")
	case a.IDSite != nil && a.View != viewSite:
		return invalidArgument("idSite belongs to view site only")
	case a.Enhanced && a.View == viewGroups:
		return invalidArgument("enhanced belongs to view sites and site only")
	case a.Pattern != "" && a.View == viewSite, a.Columns != nil && a.View != viewSites,
		(a.SortColumn != "" || a.SortOrder != "" || a.Limit != 0 || a.Cursor != "") && !paged:
		return invalidArgument("pattern, columns, sort_column, sort_order, limit, and cursor do not belong to view " + a.View)
	}
	plan, err := planPeriod(a.Period, a.Date)
	if err != nil {
		return err
	}
	if plan.multiple {
		return invalidArgument("several periods in one call are not available; give one date or period range")
	}
	if a.Segment != "" && !validSegmentExpression(a.Segment) {
		return invalidArgument("segment must be terms of field, operator, and URL-encoded value " +
			"joined by ; or , (fields: matomo.segments.fields)")
	}
	if pattern := strings.TrimSpace(a.Pattern); a.Pattern != "" && (pattern == "" || len(pattern) > maxPatternLength ||
		!noControl(pattern)) {
		return invalidArgument("pattern must hold 1 to 100 characters")
	}
	if a.Columns != nil && (len(a.Columns) == 0 || len(a.Columns) > maxReportColumns) {
		return invalidArgument("columns must hold 1 to " + strconv.Itoa(maxReportColumns) + " names")
	}
	for i, name := range a.Columns {
		if !validColumn(name) || slices.Index(a.Columns, name) != i {
			return invalidArgument("columns must be distinct column names such as nb_visits")
		}
	}
	if (a.SortColumn != "" && !validColumn(a.SortColumn)) || (a.SortOrder != "" && a.SortOrder != "asc" && a.SortOrder != "desc") {
		return invalidArgument("sort_column must be a column name and sort_order asc or desc")
	}
	return nil
}

func invokeMultiSitesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "compare websites"
	var args multiSitesArguments
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	if err := args.validate(); err != nil {
		return nil, err
	}
	bound, err := boundScope(resolved)
	if err != nil {
		return nil, err
	}
	site := 0
	if args.View == viewSite {
		if site, err = selectSite(resolved, args.IDSite); err != nil {
			return nil, err
		}
	} else if !bound.reachesSites() {
		return nil, invalidArgument("this connection is bound to no website")
	}
	var paging page
	if args.View != viewSite {
		binding := provider.CursorBinding(multiSitesGet.ID, scopeBinding(resolved), args.View, args.Period, args.Date,
			args.Segment, args.Enhanced, strings.TrimSpace(args.Pattern), args.Columns, args.SortColumn, args.SortOrder)
		if paging, err = startPage(binding, args.Limit, args.Cursor); err != nil {
			return nil, err
		}
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	req := requirement{tool: multiSitesGet.ID, plugin: "MultiSites"}
	params := url.Values{"period": {args.Period}, "date": {args.Date}, "format_metrics": {"0"}}
	if args.Segment != "" {
		params.Set("segment", args.Segment)
	}
	result := &MultiSitesResult{View: args.View, Period: args.Period, Date: args.Date}
	switch args.View {
	case viewSite:
		params.Set("idSite", strconv.Itoa(site))
		if args.Enhanced {
			params.Set("enhanced", "1")
		}
		var answer json.RawMessage
		if err := client.call(ctx, op, req, "MultiSites.getOne", params, &answer); err != nil {
			return nil, err
		}
		rows, ok := rowList(answer)
		if !ok || len(rows) > 1 {
			return nil, provider.InvalidResponse(op, "Matomo returned a comparison in a form this tool does not read")
		}
		result.Site = map[string]any{}
		if len(rows) == 1 {
			result.Site = reportRow(rows[0])
		}
		return result, nil
	case viewSites:
		if args.Enhanced {
			params.Set("enhanced", "1")
		}
		if args.Columns != nil {
			params.Set("showColumns", strings.Join(args.Columns, ","))
		}
		setPattern(params, args)
		paging.serverWindow(params)
		var answer json.RawMessage
		if err := client.call(ctx, op, req, "MultiSites.getAll", params, &answer); err != nil {
			return nil, err
		}
		rows, ok := rowList(answer)
		if !ok {
			return nil, provider.InvalidResponse(op, "Matomo returned a comparison in a form this tool does not read")
		}
		// Matomo skips the window when a report disables its generic filters; then it is cut here.
		if len(rows) > paging.limit+1 {
			rows = rows[min(paging.offset, len(rows)):]
		}
		finishRows(result, bound, paging, rows)
		return result, nil
	}
	setPattern(params, args)
	params.Set("filter_limit", strconv.Itoa(paging.limit+1))
	params.Set("filter_offset", strconv.Itoa(paging.offset))
	var answer struct {
		NumSites *loose          `json:"numSites"`
		Totals   json.RawMessage `json:"totals"`
		LastDate *loose          `json:"lastDate"`
		Sites    json.RawMessage `json:"sites"`
	}
	if err := client.call(ctx, op, req, "MultiSites.getAllWithGroups", params, &answer); err != nil {
		return nil, err
	}
	rows, ok := rowList(answer.Sites)
	if !ok {
		return nil, provider.InvalidResponse(op, "Matomo returned a comparison in a form this tool does not read")
	}
	result.LastDate = answer.LastDate.str(maxStringLength)
	// numSites and the totals sum over every website of the token, so they say something about websites
	// outside the targets unless the connection reaches all of them.
	if bound.everySite() {
		result.NumSites = answer.NumSites.number()
		result.Totals = cleanObject(answer.Totals)
	}
	finishRows(result, bound, paging, rows)
	return result, nil
}

func setPattern(params url.Values, args multiSitesArguments) {
	if pattern := strings.TrimSpace(args.Pattern); pattern != "" {
		params.Set("pattern", pattern)
	}
	if args.SortColumn != "" {
		params.Set("filter_sort_column", args.SortColumn)
	}
	if args.SortOrder != "" {
		params.Set("filter_sort_order", args.SortOrder)
	}
}

// finishRows cuts the batch out of the rows of the window, drops the rows outside the targets, and sets the
// cursor from the rows Matomo returned, not from those kept.
func finishRows(result *MultiSitesResult, bound scope, paging page, rows []json.RawMessage) {
	batch, next := sliceWindow(paging, rows)
	result.Rows = []map[string]any{}
	for _, one := range batch {
		row := reportRow(one)
		if multiSitesRowAllowed(bound, row) {
			result.Rows = append(result.Rows, row)
		}
	}
	result.HasMore, result.NextCursor = next != "", next
}

// multiSitesRowAllowed keeps a row of a website inside the targets. Unless the connection reaches every
// website, a row without a website ID, such as a group row that sums websites, is dropped.
func multiSitesRowAllowed(bound scope, row map[string]any) bool {
	if bound.everySite() {
		return true
	}
	if _, group := row["isGroup"]; group {
		return false
	}
	number, ok := row["idsite"].(json.Number)
	text := number.String()
	if !ok {
		text, ok = row["idsite"].(string)
	}
	id, err := strconv.Atoi(strings.TrimSpace(text))
	return ok && err == nil && bound.allows(id)
}
