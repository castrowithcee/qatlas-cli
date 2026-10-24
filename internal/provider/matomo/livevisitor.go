package matomo

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
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
	// maxProfileVisits bounds the visits a profile carries; each holds up to maxActions actions.
	maxProfileVisits     = 20
	defaultProfileVisits = 10
	maxProfileRows       = 100
	maxProfileGroups     = 30
	maxProfileValues     = 20
)

var visitorIDPattern = regexp.MustCompile(`^[0-9A-Fa-f]{16}$`)

const visitorIDSchema = `"visitor_id":{"type":"string","pattern":"^[0-9A-Fa-f]{16}$","minLength":16,"maxLength":16}`

var visitorIDArgument = "Visitor ID of 16 hexadecimal characters, such as from matomo.visitors.get-latest-id or " +
	"matomo.visits.list; personal data"

// profileEnabled asks whether the instance keeps visitor profiles for the website. A disabled profile is an
// unsupported capability and ends the call before any further request.
func (c *Client) profileEnabled(ctx context.Context, op, tool string, id int) error {
	var raw json.RawMessage
	if err := c.callLive(ctx, op, baseRequirement(tool), "Live.isVisitorProfileEnabled",
		url.Values{"idSite": {strconv.Itoa(id)}}, &raw); err != nil {
		return err
	}
	var enabled bool
	if err := scalar(raw, &enabled); err != nil {
		return provider.InvalidResponse(op, "Matomo returned an invalid profile setting")
	}
	if !enabled {
		return c.unavailable(baseRequirement(tool), "visitor profiles are disabled for this website")
	}
	return nil
}

// objectList reads a list of objects of a row, cut at limit; more reports a cut.
func objectList(r row, key string, limit int) (list []row, more bool) {
	raw, found := r[key]
	if !found || json.Unmarshal(raw, &list) != nil {
		return nil, false
	}
	if len(list) > limit {
		return list[:limit], true
	}
	return list, false
}

func membersOf(r row, members []member) map[string]any {
	out := map[string]any{}
	readMembers(r, members, out)
	return out
}

// profileTotals are the counters and averages Matomo adds to a profile.
var profileTotals = []member{
	{"totalVisits", "total_visits", kInt, "Visits aggregated for the totals"},
	{"visitsAggregated", "visits_aggregated", kInt, "Visits the profile aggregates; Matomo caps it by its configuration"},
	{"totalVisitDuration", "total_visit_duration", kInt, "Seconds of all aggregated visits"},
	{"totalActions", "total_actions", kInt, "Actions of all aggregated visits"},
	{"totalPageViews", "total_page_views", kInt, "Page views"},
	{"totalUniquePageViews", "total_unique_page_views", kInt, "Distinct page addresses viewed"},
	{"totalRevisitedPages", "total_revisited_pages", kInt, "Pages viewed more than once"},
	{"totalOutlinks", "total_outlinks", kInt, "Outlink clicks"},
	{"totalDownloads", "total_downloads", kInt, "Downloads"},
	{"totalSearches", "total_searches", kInt, "Distinct site search keywords"},
	{"totalEvents", "total_events", kInt, "Events"},
	{"totalGoalConversions", "total_goal_conversions", kInt, "Goal conversions"},
	{"totalPageViewsWithTiming", "total_page_views_with_timing", kInt, "Page views with a generation time"},
	{"totalPageViewsWithLoadTime", "total_page_views_with_load_time", kInt, "Page views with a load time"},
	{"averagePageGenerationTime", "average_page_generation_time", kText, "Average page generation time in seconds as Matomo reports it"},
	{"averagePageLoadTime", "average_page_load_time", kText, "Average page load time in seconds as Matomo reports it"},
	{"hasLatLong", "has_lat_long", kFlag, "True when a visit has a latitude"},
	{"totalEcommerceRevenue", "total_ecommerce_revenue", kText, "Lifetime ecommerce revenue; only when ecommerce is enabled"},
	{"totalEcommerceConversions", "total_ecommerce_conversions", kInt, "Lifetime ecommerce orders; only when ecommerce is enabled"},
	{"totalEcommerceItems", "total_ecommerce_items", kInt, "Lifetime ordered items; only when ecommerce is enabled"},
	{"totalAbandonedCartsRevenue", "total_abandoned_carts_revenue", kText, "Revenue of abandoned carts; only when ecommerce is enabled"},
	{"totalAbandonedCarts", "total_abandoned_carts", kInt, "Abandoned carts; only when ecommerce is enabled"},
	{"totalAbandonedCartsItems", "total_abandoned_carts_items", kInt, "Items in abandoned carts; only when ecommerce is enabled"},
	{"userId", "user_id", kText, "User ID the website assigned to the visitor, personal data, untrusted"},
	{"visitorId", "visitor_id", kText, "Visitor ID, personal data"},
	{"nextVisitorId", "next_visitor_id", kText, "Visitor ID of the next visitor in the log, personal data; absent when none"},
	{"previousVisitorId", "previous_visitor_id", kText, "Visitor ID of the previous visitor in the log, personal data; absent when none"},
	{"hasMoreVisits", "has_more_visits", kFlag, "True when the visitor has more visits than Matomo aggregated"},
}

// summaryMembers describe the first and last visit of a profile; the formatted date is left out.
var summaryMembers = []member{
	{"date", "date", kInt, "Unix time of the first action of the visit"},
	{"daysAgo", "days_ago", kInt, "Days since the visit"},
	{"referrerType", "referrer_type", kText, "Kind of referrer"},
	{"referrerUrl", "referrer_url", kURL, "Referrer address, untrusted text that is never fetched"},
	{"referralSummary", "referral_summary", kText, "Referrer in one text, untrusted"},
}

var (
	goalCountMembers  = []member{{"count", "conversions", kInt, ""}}
	searchMembers     = []member{{"keyword", "keyword", kText, ""}, {"searches", "searches", kInt, ""}}
	pageMembers       = []member{{"url", "url", kURL, ""}, {"count", "views", kInt, ""}}
	deviceMembers     = []member{{"type", "type", kText, ""}, {"count", "visits", kInt, ""}}
	deviceNameMembers = []member{{"name", "name", kText, ""}, {"count", "visits", kInt, ""}}
	countryMembers    = []member{{"country", "country_code", kText, ""}, {"nb_visits", "visits", kInt, ""},
		{"prettyName", "name", kText, ""}}
	continentMembers = []member{{"continent", "continent_code", kText, ""}, {"nb_visits", "visits", kInt, ""},
		{"prettyName", "name", kText, ""}}
	dimensionValueMembers = []member{{"value", "value", kText, ""}, {"count", "visits", kInt, ""}}
)

// goalTotals reads a map of "idgoal=N" keys to numbers; PHP answers an empty map as a list.
func goalTotals(r row, key, name string, kind kind) []map[string]any {
	var byGoal map[string]json.RawMessage
	if raw, found := r[key]; !found || json.Unmarshal(raw, &byGoal) != nil {
		return nil
	}
	ids := make([]int, 0, len(byGoal))
	values := map[int]json.RawMessage{}
	for k, v := range byGoal {
		if n, err := strconv.Atoi(strings.TrimPrefix(k, "idgoal=")); err == nil && n > 0 {
			ids = append(ids, n)
			values[n] = v
		}
	}
	slices.Sort(ids)
	if len(ids) > maxProfileRows {
		ids = ids[:maxProfileRows]
	}
	out := make([]map[string]any, 0, len(ids))
	for _, n := range ids {
		entry := map[string]any{"goal_id": n}
		if value, ok := (member{from: "v", to: name, kind: kind}).read(row{"v": values[n]}); ok {
			entry[name] = value
		}
		out = append(out, entry)
	}
	return out
}

func rowsOf(r row, key string, limit int, members []member) ([]map[string]any, bool) {
	list, more := objectList(r, key, limit)
	out := make([]map[string]any, 0, len(list))
	for _, one := range list {
		out = append(out, membersOf(one, members))
	}
	return out, more
}

func profileOf(r row, id int) map[string]any {
	out := membersOf(r, profileTotals)
	for _, side := range []struct{ from, to string }{{"firstVisit", "first_visit"}, {"lastVisit", "last_visit"}} {
		var summary row
		if raw, found := r[side.from]; found && json.Unmarshal(raw, &summary) == nil {
			out[side.to] = membersOf(summary, summaryMembers)
		}
	}
	if list := goalTotals(r, "totalConversionsByGoal", "conversions", kInt); len(list) > 0 {
		out["conversions_by_goal"] = list
	}
	if list := goalTotals(r, "totalRevenueByGoal", "revenue", kText); len(list) > 0 {
		out["revenue_by_goal"] = list
	}
	if list, more := rowsOf(r, "searches", maxProfileRows, searchMembers); len(list) > 0 {
		out["searches"] = list
		if more {
			out["searches_truncated"] = true
		}
	}
	if list, more := rowsOf(r, "visitedPages", maxProfileRows, pageMembers); len(list) > 0 {
		out["visited_pages"] = list
		if more {
			out["visited_pages_truncated"] = true
		}
	}
	devices, more := objectList(r, "devices", maxProfileGroups)
	if len(devices) > 0 {
		list := make([]map[string]any, 0, len(devices))
		for _, one := range devices {
			entry := membersOf(one, deviceMembers)
			if names, _ := rowsOf(one, "devices", maxProfileValues, deviceNameMembers); len(names) > 0 {
				entry["devices"] = names
			}
			list = append(list, entry)
		}
		out["devices"] = list
		if more {
			out["devices_truncated"] = true
		}
	}
	if list, more := rowsOf(r, "continents", maxProfileGroups, continentMembers); len(list) > 0 {
		out["continents"] = list
		if more {
			out["continents_truncated"] = true
		}
	}
	countries, more := objectList(r, "countries", maxProfileRows)
	if len(countries) > 0 {
		list := make([]map[string]any, 0, len(countries))
		for _, one := range countries {
			entry := membersOf(one, countryMembers)
			var cities []json.RawMessage
			if raw, found := one["cities"]; found && json.Unmarshal(raw, &cities) == nil {
				names := []string{}
				for _, c := range cities {
					var l loose
					if json.Unmarshal(c, &l) == nil && l.str(maxStringLength) != "" && len(names) < maxProfileRows {
						names = append(names, l.str(maxStringLength))
					}
				}
				if len(names) > 0 {
					entry["cities"] = names
				}
			}
			list = append(list, entry)
		}
		out["countries"] = list
		if more {
			out["countries_truncated"] = true
		}
	}
	var scopes map[string]json.RawMessage
	if raw, found := r["customDimensions"]; found && json.Unmarshal(raw, &scopes) == nil && len(scopes) > 0 {
		dims := map[string]any{}
		for _, scope := range []string{"visit", "action"} {
			list, _ := objectList(row{"s": scopes[scope]}, "s", maxProfileGroups)
			entries := make([]map[string]any, 0, len(list))
			for _, one := range list {
				entry := map[string]any{"name": one.text("name", maxStringLength)}
				if values, _ := rowsOf(one, "values", maxProfileValues, dimensionValueMembers); len(values) > 0 {
					entry["values"] = values
				}
				entries = append(entries, entry)
			}
			if len(entries) > 0 {
				dims[scope] = entries
			}
		}
		if len(dims) > 0 {
			out["custom_dimensions"] = dims
		}
	}
	visits, more := objectList(r, "lastVisits", maxProfileVisits)
	kept := make([]map[string]any, 0, len(visits))
	for _, one := range visits {
		// A visit of another website than asked for is dropped, so the targets hold whatever Matomo answers.
		if site := one.integer("idSite"); site != nil && *site != id {
			continue
		}
		kept = append(kept, visitOf(one, true))
	}
	out["last_visits"] = kept
	if more {
		out["last_visits_truncated"] = true
	}
	return out
}

func profileFields() []capability.Field {
	fields := []capability.Field{
		{Name: "found", Description: "True when Matomo knows the visitor on this website"},
		{Name: "profile", Description: "The visitor profile, absent when found is false"},
	}
	for _, m := range profileTotals {
		fields = append(fields, capability.Field{Name: "profile." + m.to, Description: m.doc})
	}
	for _, side := range []string{"first_visit", "last_visit"} {
		fields = append(fields, capability.Field{Name: "profile." + side, Description: "Summary of the " +
			strings.TrimSuffix(side, "_visit") + " visit of the visitor"})
		for _, m := range summaryMembers {
			fields = append(fields, capability.Field{Name: "profile." + side + "." + m.to, Description: m.doc})
		}
	}
	add := func(name, doc string) {
		fields = append(fields, capability.Field{Name: "profile." + name, Description: doc})
	}
	add("conversions_by_goal", "Goal conversions per goal, up to 100: goal_id and conversions")
	add("revenue_by_goal", "Revenue per goal as Matomo reports it, up to 100: goal_id and revenue")
	add("searches", "Site search keywords with the number of searches, up to 100, untrusted: keyword and searches")
	add("searches_truncated", "True when the visitor has more search keywords than are returned")
	add("visited_pages", "Page addresses by views, most viewed first, up to 100, untrusted text that is never fetched: url and views")
	add("visited_pages_truncated", "True when the visitor viewed more pages than are returned")
	add("devices", "Device types with visits, up to 30: type, visits, and devices (name and visits, up to 20)")
	add("devices_truncated", "True when the visitor has more device types than are returned")
	add("continents", "Continents of the visits, up to 30: continent_code, visits, and name; personal data")
	add("countries", "Countries of the visits, up to 100: country_code, visits, name, and cities (up to 100); personal data")
	add("continents_truncated", "True when the list was cut")
	add("countries_truncated", "True when the list was cut")
	add("custom_dimensions", "Custom dimensions by scope, visit and action, up to 30 each, untrusted: name and values "+
		"(value and visits, up to 20)")
	add("last_visits", "The most recent visits of the visitor, newest first, in the shape of matomo.visits.list with "+
		"their actions (up to 50 each), only of the website asked for")
	add("last_visits_truncated", "True when Matomo returned more visits than the cap")
	return fields
}

var visitorsGetProfile = capability.Descriptor{
	ID: Provider + ".visitors.get-profile", Version: 1, Title: "Get a Matomo visitor profile",
	Description: "Read the profile of one visitor of one website from Live.getVisitorProfile: totals, first and " +
		"last visit, viewed addresses, searches, devices, countries, goals, ecommerce, custom dimensions, and the most " +
		"recent visits with their actions; lists are capped; needs a view right and visitor profiles enabled on the " +
		"website, otherwise the tool is unavailable; " + visitorsText,
	Tags: []string{"matomo", "live", "visitors", "profile"}, Risk: readRisk(liveSensitivity), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + siteKey + `,` + visitorIDSchema + `,` + segmentKey +
		`,"limit_visits":{"type":"integer","minimum":1,"maximum":20}},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"found":{"type":"boolean"},` +
		`"profile":{"type":"object"}},"required":["found"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "idSite", Description: "Website ID from matomo.sites.list; optional when the connection binds " +
			"exactly one website; must lie inside the connection's targets", Target: "site"},
		{Name: "visitor_id", Description: visitorIDArgument + "; without it Matomo takes the most recent visitor"},
		{Name: "segment", Description: "Matomo segment that filters the visits of the profile and the adjacent " +
			"visitors; up to 2048 bytes"},
		{Name: "limit_visits", Description: "Most recent visits to list in last_visits, from 1 through 20; 10 by " +
			"default. The totals still aggregate every visit Matomo reads"},
	},
	Fields: profileFields(),
	Examples: []capability.Example{{Description: "Profile of a visitor of website 1",
		Arguments: json.RawMessage(`{"idSite":1,"visitor_id":"0123456789abcdef","limit_visits":5}`)}},
}

// ProfileResult is the answer of visitors.get-profile.
type ProfileResult struct {
	Found   bool           `json:"found"`
	Profile map[string]any `json:"profile,omitempty"`
}

func invokeVisitorsGetProfile(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get visitor profile"
	var args struct {
		IDSite      *int   `json:"idSite"`
		VisitorID   string `json:"visitor_id"`
		Segment     string `json:"segment"`
		LimitVisits *int   `json:"limit_visits"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if args.VisitorID != "" && !visitorIDPattern.MatchString(args.VisitorID) {
		return nil, invalidArgument("visitor_id must be 16 hexadecimal characters")
	}
	if err := checkSegment(args.Segment); err != nil {
		return nil, err
	}
	limit := defaultProfileVisits
	if args.LimitVisits != nil {
		if *args.LimitVisits < 1 || *args.LimitVisits > maxProfileVisits {
			return nil, invalidArgument("limit_visits must be between 1 and 20")
		}
		limit = *args.LimitVisits
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.profileEnabled(ctx, op, visitorsGetProfile.ID, id); err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "limitVisits": {strconv.Itoa(limit)}}
	if args.VisitorID != "" {
		params.Set("visitorId", args.VisitorID)
	}
	if args.Segment != "" {
		params.Set("segment", args.Segment)
	}
	var answer json.RawMessage
	if err := client.callLive(ctx, op, baseRequirement(visitorsGetProfile.ID), "Live.getVisitorProfile", params, &answer); err != nil {
		return nil, err
	}
	// Matomo answers an unknown visitor with an empty list, which is not an object.
	var profile row
	if json.Unmarshal(answer, &profile) != nil {
		profile = nil
	}
	if len(profile) == 0 {
		return &ProfileResult{}, nil
	}
	return &ProfileResult{Found: true, Profile: profileOf(profile, id)}, nil
}

var visitorsGetLatestID = capability.Descriptor{
	ID: Provider + ".visitors.get-latest-id", Version: 1, Title: "Get the ID of the latest Matomo visitor",
	Description: "Read the visitor ID of the most recent visit of one website, optionally within a segment, from " +
		"Live.getMostRecentVisitorId; Matomo searches the last 7 days, then a year, then all logs, which can be " +
		"slow; needs a view right; " + visitorsText,
	Tags: []string{"matomo", "live", "visitors", "latest"}, Risk: readRisk(liveSensitivity), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + siteKey + `,` + segmentKey +
		`},"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"found":{"type":"boolean"},` +
		`"visitor_id":{"type":"string"}},"required":["found"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "idSite", Description: "Website ID from matomo.sites.list; optional when the connection binds " +
			"exactly one website; must lie inside the connection's targets", Target: "site"},
		{Name: "segment", Description: "Matomo segment that filters the visits; up to 2048 bytes"},
	},
	Fields: []capability.Field{
		{Name: "found", Description: "True when the website has a matching visit"},
		{Name: "visitor_id", Description: "Visitor ID of the most recent matching visit, personal data; absent when none"},
	},
	Examples: []capability.Example{{Description: "Latest visitor of website 1", Arguments: json.RawMessage(`{"idSite":1}`)}},
}

// LatestVisitor is the answer of visitors.get-latest-id.
type LatestVisitor struct {
	Found     bool   `json:"found"`
	VisitorID string `json:"visitor_id,omitempty"`
}

func invokeVisitorsGetLatestID(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get latest visitor ID"
	var args struct {
		IDSite  *int   `json:"idSite"`
		Segment string `json:"segment"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if err := checkSegment(args.Segment); err != nil {
		return nil, err
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}}
	if args.Segment != "" {
		params.Set("segment", args.Segment)
	}
	var rawID json.RawMessage
	if err := client.callLive(ctx, op, baseRequirement(visitorsGetLatestID.ID), "Live.getMostRecentVisitorId", params, &rawID); err != nil {
		return nil, err
	}
	var found loose
	if err := scalar(rawID, &found); err != nil {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid visitor ID")
	}
	text := strings.TrimSpace(found.text)
	if text == "" || text == "false" {
		return &LatestVisitor{}, nil
	}
	if !visitorIDPattern.MatchString(text) {
		return nil, provider.InvalidResponse(op, "Matomo returned an invalid visitor ID")
	}
	return &LatestVisitor{Found: true, VisitorID: text}, nil
}

func firstVisitFields() []capability.Field {
	fields := []capability.Field{
		{Name: "found", Description: "True when Matomo knows the visitor on this website"},
		{Name: "visit", Description: "The first visit of the visitor, absent when found is false; the actions are not read"},
	}
	for _, m := range visitMembers {
		fields = append(fields, capability.Field{Name: "visit." + m.to, Description: m.doc})
	}
	return append(fields, capability.Field{Name: "visit.custom_dimensions", Description: "Visit-scope custom " +
		"dimensions as dimensionN to value, up to 30, untrusted"})
}

var visitsGetFirst = capability.Descriptor{
	ID: Provider + ".visits.get-first", Version: 1, Title: "Get the first Matomo visit of a visitor",
	Description: "Read the first visit of one visitor of one website from Live.getFirstVisitForVisitorId, without " +
		"its actions; needs a view right and visitor profiles enabled on the website, otherwise the tool is " +
		"unavailable; " + visitorsText,
	Tags: []string{"matomo", "live", "visits", "visitors", "first"}, Risk: readRisk(liveSensitivity), Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` + siteKey + `,` + visitorIDSchema +
		`},"required":["visitor_id"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"found":{"type":"boolean"},` +
		`"visit":{"type":"object"}},"required":["found"],"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "idSite", Description: "Website ID from matomo.sites.list; optional when the connection binds " +
			"exactly one website; must lie inside the connection's targets", Target: "site"},
		{Name: "visitor_id", Required: true, Description: visitorIDArgument},
	},
	Fields:   firstVisitFields(),
	Examples: []capability.Example{{Description: "First visit of a visitor", Arguments: json.RawMessage(`{"idSite":1,"visitor_id":"0123456789abcdef"}`)}},
}

// FirstVisit is the answer of visits.get-first.
type FirstVisit struct {
	Found bool           `json:"found"`
	Visit map[string]any `json:"visit,omitempty"`
}

func invokeVisitsGetFirst(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver,
	red *redact.Redactor, raw json.RawMessage) (any, error) {
	const op = "get first visit"
	var args struct {
		IDSite    *int   `json:"idSite"`
		VisitorID string `json:"visitor_id"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return nil, err
	}
	id, err := selectSite(resolved, args.IDSite)
	if err != nil {
		return nil, err
	}
	if !visitorIDPattern.MatchString(args.VisitorID) {
		return nil, invalidArgument("visitor_id must be 16 hexadecimal characters")
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	if err := client.profileEnabled(ctx, op, visitsGetFirst.ID, id); err != nil {
		return nil, err
	}
	params := url.Values{"idSite": {strconv.Itoa(id)}, "visitorId": {args.VisitorID}}
	var rows []row
	if err := client.callLive(ctx, op, baseRequirement(visitsGetFirst.ID), "Live.getFirstVisitForVisitorId", params, &rows); err != nil {
		return nil, err
	}
	for _, one := range rows {
		if site := one.integer("idSite"); site != nil && *site != id {
			continue
		}
		return &FirstVisit{Found: true, Visit: visitOf(one, false)}, nil
	}
	return &FirstVisit{}, nil
}
