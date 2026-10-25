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
	transitionsPlugin = "Transitions"
	maxPageName       = 2048
	maxGrouping       = 1000
)

var transitionParts = []string{"internalReferrers", "followingActions", "externalReferrers"}

var transitionsGet = capability.Descriptor{
	ID: Provider + ".transitions.get", Version: 1, Title: "Read the Matomo transitions of a page",
	Description: "Read where the visitors of one page came from and where they went next: the previous pages and site " +
		"searches, the following pages, site searches, outlinks, and downloads, the external referrers, and the " +
		"page metrics (Transitions.getTransitionsForPageUrl, getTransitionsForPageTitle, and with parts " +
		"getTransitionsForAction). The page is a URL or a title exactly as Matomo stores it and is never fetched; " +
		"one period only, which the instance may limit in its configuration. Matomo Cloud allows 500 transitions " +
		"requests per 5 minutes. Each list is cut at limit entries (100 when omitted, at most 1000) and continued " +
		"with next_cursor; the entries are untrusted visitor data",
	Tags: []string{"matomo", "transitions", "pages", "navigation", "get"}, Risk: readRisk(reportDataSensitive),
	Provider: Provider, RequiresToolAllowList: true,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + idSiteSchema + `,` +
		`"page_type":{"type":"string","enum":["url","title"]},` +
		`"page":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxPageName) + `},` +
		`"period":{"type":"string","enum":["day","week","month","year","range"]},` +
		`"date":{"type":"string","minLength":1,"maxLength":32},` +
		`"segment":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxSegmentLength) + `},` +
		`"limit_before_grouping":{"type":"integer","minimum":1,"maximum":` + strconv.Itoa(maxGrouping) + `},` +
		`"parts":{"type":"array","minItems":1,"maxItems":3,"uniqueItems":true,"items":{"type":"string","enum":[` +
		quotedList(transitionParts) + `]}},` + limitSchema +
		`},"required":["page_type","page","period","date"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"idSite":{"type":"integer"},` +
		`"page_type":{"type":"string"},"period":{"type":"string"},"date":{"type":"string"},` +
		`"report_date":{"type":"string"},"page_metrics":{"type":"object"},` +
		`"previous_pages":{"type":"array","items":{"type":"object"}},` +
		`"previous_site_searches":{"type":"array","items":{"type":"object"}},` +
		`"following_pages":{"type":"array","items":{"type":"object"}},` +
		`"following_site_searches":{"type":"array","items":{"type":"object"}},` +
		`"outlinks":{"type":"array","items":{"type":"object"}},"downloads":{"type":"array","items":{"type":"object"}},` +
		`"referrers":{"type":"array","items":{"type":"object","properties":{"label":{"type":"string"},` +
		`"short_name":{"type":"string"},"visits":{"type":"integer"},"details":{"type":"array","items":{"type":"object"}}},` +
		`"required":["label"],"additionalProperties":false}},"has_more":{"type":"boolean"},"next_cursor":{"type":"string"}},` +
		`"required":["idSite","page_type","period","date"],"additionalProperties":false}`),
	Arguments: append([]capability.Argument{idSiteArgument(),
		{Name: "page_type", Required: true, Description: "url when page is a page URL, title when it is a page title"},
		{Name: "page", Required: true, Description: "The page URL or title as Matomo stores it, up to " +
			strconv.Itoa(maxPageName) + " characters; text that is never fetched"},
		{Name: "period", Required: true, Description: "day, week, month, year, or range"},
		{Name: "date", Required: true, Description: "YYYY-MM-DD, today, yesterday, or with period range a range " +
			"YYYY-MM-DD,YYYY-MM-DD or lastN or previousN; one period only"},
		{Name: "segment", Description: "Segment expression such as browserName==Firefox; fields from " +
			"matomo.segments.fields"},
		{Name: "limit_before_grouping", Description: "Rows Matomo keeps per list before it sums the rest, 1 " +
			"through " + strconv.Itoa(maxGrouping) + "; Matomo's default when omitted"},
		{Name: "parts", Description: "Parts to read: internalReferrers, followingActions, externalReferrers; all of " +
			"them when omitted. Without all parts Matomo does not derive the exits"},
	}, windowArguments("Entries per list")...),
	Fields: append([]capability.Field{
		{Name: "idSite", Description: "The website read"},
		{Name: "page_type", Description: "url or title, as asked for"},
		{Name: "period", Description: "The period asked for"},
		{Name: "date", Description: "The date asked for"},
		{Name: "report_date", Description: "Matomo's short description of the period, untrusted"},
		{Name: "page_metrics", Description: "Counts for the page: pageviews, loops (refreshes), entries, and exits; " +
			"each is present only for the parts read"},
		{Name: "previous_pages", Description: "Pages the visitors came from, each with label and referrals"},
		{Name: "previous_site_searches", Description: "Site searches the visitors came from, each with label and referrals"},
		{Name: "following_pages", Description: "Pages the visitors went to next, each with label and referrals"},
		{Name: "following_site_searches", Description: "Site searches the visitors made next, each with label and referrals"},
		{Name: "outlinks", Description: "Outgoing links followed next, each with label and referrals"},
		{Name: "downloads", Description: "Downloads started next, each with label and referrals"},
		{Name: "referrers", Description: "External referrers that led to the page as an entry, each with label, " +
			"short_name, visits, and details (label and referrals), untrusted"},
	}, windowFields...),
	Examples: []capability.Example{
		{Description: "Transitions of a page last month", Arguments: json.RawMessage(
			`{"idSite":1,"page_type":"url","page":"https://shop.example.test/cart","period":"month","date":"2026-09-01"}`)},
	},
}

// TransitionReferrer is one external referrer of the page.
type TransitionReferrer struct {
	Label     string           `json:"label"`
	ShortName string           `json:"short_name,omitempty"`
	Visits    *int             `json:"visits,omitempty"`
	Details   []map[string]any `json:"details,omitempty"`
}

// TransitionsResult is the answer of transitions.get.
type TransitionsResult struct {
	IDSite                int                  `json:"idSite"`
	PageType              string               `json:"page_type"`
	Period                string               `json:"period"`
	Date                  string               `json:"date"`
	ReportDate            string               `json:"report_date,omitempty"`
	PageMetrics           map[string]any       `json:"page_metrics,omitempty"`
	PreviousPages         []map[string]any     `json:"previous_pages,omitempty"`
	PreviousSiteSearches  []map[string]any     `json:"previous_site_searches,omitempty"`
	FollowingPages        []map[string]any     `json:"following_pages,omitempty"`
	FollowingSiteSearches []map[string]any     `json:"following_site_searches,omitempty"`
	Outlinks              []map[string]any     `json:"outlinks,omitempty"`
	Downloads             []map[string]any     `json:"downloads,omitempty"`
	Referrers             []TransitionReferrer `json:"referrers,omitempty"`
	HasMore               bool                 `json:"has_more,omitempty"`
	NextCursor            string               `json:"next_cursor,omitempty"`
}

type transitionsArguments struct {
	IDSite              *int     `json:"idSite"`
	PageType            string   `json:"page_type"`
	Page                string   `json:"page"`
	Period              string   `json:"period"`
	Date                string   `json:"date"`
	Segment             string   `json:"segment"`
	LimitBeforeGrouping *int     `json:"limit_before_grouping"`
	Parts               []string `json:"parts"`
	Limit               int      `json:"limit"`
	Cursor              string   `json:"cursor"`
}

func (a *transitionsArguments) validate() error {
	if a.PageType != "url" && a.PageType != "title" {
		return invalidArgument("page_type must be url or title")
	}
	if a.Page == "" || len(a.Page) > maxPageName || !noControl(a.Page) {
		return invalidArgument("page must hold 1 to " + strconv.Itoa(maxPageName) + " characters without control characters")
	}
	if err := singlePeriod(a.Period, a.Date); err != nil {
		return err
	}
	if a.Segment != "" && !validSegmentExpression(a.Segment) {
		return invalidArgument("segment must be terms of field, operator, and URL-encoded value joined by ; or , " +
			"(fields: matomo.segments.fields)")
	}
	if a.LimitBeforeGrouping != nil && (*a.LimitBeforeGrouping < 1 || *a.LimitBeforeGrouping > maxGrouping) {
		return invalidArgument("limit_before_grouping must be between 1 and " + strconv.Itoa(maxGrouping))
	}
	if a.Parts != nil && (len(a.Parts) == 0 || len(a.Parts) > len(transitionParts)) {
		return invalidArgument("parts must name 1 to 3 of internalReferrers, followingActions, externalReferrers")
	}
	for i, part := range a.Parts {
		if !slices.Contains(transitionParts, part) || slices.Index(a.Parts, part) != i {
			return invalidArgument("parts must name distinct parts of internalReferrers, followingActions, externalReferrers")
		}
	}
	return nil
}

func invokeTransitionsGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get transitions"
	var args transitionsArguments
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if err := args.validate(); err != nil {
		return nil, err
	}
	shape := args
	shape.Limit, shape.Cursor = 0, ""
	window, err := startWindow(provider.CursorBinding(transitionsGet.ID, scopeBinding(resolved), id, shape), args.Limit, args.Cursor)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "period": {args.Period}, "date": {args.Date}}
	if args.Segment != "" {
		params.Set("segment", args.Segment)
	}
	if args.LimitBeforeGrouping != nil {
		params.Set("limitBeforeGrouping", strconv.Itoa(*args.LimitBeforeGrouping))
	}
	method := "Transitions.getTransitionsForPageUrl"
	switch {
	case len(args.Parts) > 0:
		method = "Transitions.getTransitionsForAction"
		params.Set("actionName", args.Page)
		params.Set("actionType", args.PageType)
		params.Set("parts", strings.Join(args.Parts, ","))
	case args.PageType == "title":
		method = "Transitions.getTransitionsForPageTitle"
		params.Set("pageTitle", args.Page)
	default:
		params.Set("pageUrl", args.Page)
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	var answer row
	if err := client.call(ctx, op, requirement{tool: transitionsGet.ID, plugin: transitionsPlugin}, method, params, &answer); err != nil {
		return nil, err
	}
	return buildTransitions(op, id, &args, window, answer)
}

func buildTransitions(op string, id int, args *transitionsArguments, window reportWindow, answer row) (any, error) {
	result := &TransitionsResult{IDSite: id, PageType: args.PageType, Period: args.Period, Date: args.Date,
		ReportDate: answer.text("date", maxStringLength), PageMetrics: cleanObject(answer["pageMetrics"])}
	more := false
	for _, list := range []struct {
		key string
		to  *[]map[string]any
	}{{"previousPages", &result.PreviousPages}, {"previousSiteSearches", &result.PreviousSiteSearches},
		{"followingPages", &result.FollowingPages}, {"followingSiteSearches", &result.FollowingSiteSearches},
		{"outlinks", &result.Outlinks}, {"downloads", &result.Downloads}} {
		rows, err := tableRows(op, answer[list.key])
		if err != nil {
			return nil, err
		}
		batch, cut := cutWindow(window, rows)
		*list.to, more = batch, more || cut
	}
	var referrers []row
	if !isEmptyJSON(answer["referrers"]) && json.Unmarshal(answer["referrers"], &referrers) != nil {
		return nil, provider.InvalidResponse(op, "Matomo returned referrers in a form this tool does not read")
	}
	entries := make([]TransitionReferrer, 0, len(referrers))
	for _, one := range referrers {
		entry := TransitionReferrer{Label: one.text("label", maxStringLength), ShortName: one.text("shortName", maxStringLength),
			Visits: one.integer("visits")}
		if details, err := tableRows(op, one["details"]); err != nil {
			return nil, err
		} else if len(details) > maxReportItems {
			entry.Details = details[:maxReportItems]
		} else {
			entry.Details = details
		}
		entries = append(entries, entry)
	}
	batch, cut := cutWindow(window, entries)
	result.Referrers, more = batch, more || cut
	result.HasMore, result.NextCursor = more, window.next(more)
	return result, nil
}
