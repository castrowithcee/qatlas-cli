package brave

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	webPath = "/res/v1/web/search"
	// Limits the web search reference documents.
	webMaxQueryRunes = 600
	webMaxQueryWords = 75
	webMaxCount      = 20
	webMaxOffset     = 9
	maxTimezoneRunes = 64
)

var (
	// webResultFilters are the result types the filter accepts.
	webResultFilters = []string{"discussions", "faq", "infobox", "news", "query", "summarizer", "videos", "web",
		"locations"}
	unitSystems = []string{"imperial", "metric"}
	// timezonePattern is the shape of an IANA zone name such as Europe/Berlin or America/Argentina/Salta.
	// The names are not checked against a database, which a host may lack.
	timezonePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z0-9_+-]+){0,2}$`)
)

// timezoneSchema is the input schema of a time zone argument; checkTimezone checks the same again.
const timezoneSchema = `{"type":"string","minLength":1,"maxLength":64,` +
	`"pattern":"^[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z0-9_+-]+){0,2}$","x-form":"IANA name such as Europe/Berlin"}`

// webOutputSchema is the answer in Brave's member names, grouped by kind of result, with the paging
// members of the provider.
var webOutputSchema = `{"type":"object","properties":{` + webAnswerShape.propertiesSchema() +
	`,"next_cursor":` + stringSchema + `,"has_more":` + booleanSchema + `,"quota":` + quotaSchema + `},` +
	`"required":["has_more"],"additionalProperties":false}`

// webSearch searches the web. The arguments are the documented parameters of the endpoint and the location
// headers; offset and cursor are the two ways to a further page.
var webSearch = descriptor("web", "search", "Search the Brave web",
	"Search the web in Brave's independent index and read one page of results for a person: web results, "+
		"discussions, an infobox, places, news, videos, and questions with answers, in Brave's own groups. "+
		"Needs the Search plan of Brave. To answer a question from the web as an agent, prefer LLM Context. "+
		"A location is only used when given; result_filter limits the kinds of result.",
	[]string{"web"},
	`{"type":"object","properties":{"q":{"type":"string","minLength":1,"maxLength":600},`+
		`"country":`+countrySchema+`,"search_lang":`+searchLangSchema+`,"ui_lang":`+uiLangSchema+`,`+
		`"count":{"type":"integer","minimum":1,"maximum":20},"offset":{"type":"integer","minimum":0,"maximum":9},`+
		`"cursor":`+cursorSchema+`,"safesearch":`+safeSearchSchema+`,"spellcheck":`+booleanSchema+`,`+
		`"freshness":`+freshnessSchema+`,"text_decorations":`+booleanSchema+`,`+
		`"result_filter":{"type":"array","minItems":1,"maxItems":9,"uniqueItems":true,"items":`+
		enumSchema(webResultFilters)+`},"units":`+enumSchema(unitSystems)+`,"goggles":`+gogglesSchema+`,`+
		`"extra_snippets":`+booleanSchema+`,"summary":`+booleanSchema+`,"enable_rich_callback":`+booleanSchema+`,`+
		`"include_fetch_metadata":`+booleanSchema+`,"operators":`+booleanSchema+`,`+
		`"timezone":`+timezoneSchema+`,`+
		locationInput+`,"no_cache":`+booleanSchema+`},"required":["q"],"additionalProperties":false}`,
	json.RawMessage(webOutputSchema),
	[]capability.Argument{
		{Name: "q", Description: "Search query, at most 600 characters and 75 words", Required: true},
		{Name: "country", Description: "Country the results come from as a two-letter code such as US, or ALL " +
			"for worldwide; US when omitted"},
		{Name: "search_lang", Description: "Language of the results as a Brave language code such as en or " +
			"pt-br; en when omitted"},
		{Name: "ui_lang", Description: "Language of the interface texts as a locale such as en-US; en-US " +
			"when omitted"},
		{Name: "count", Description: "Web results per page, from 1 through 20; 20 when omitted; Brave may return " +
			"fewer. It does not limit the other kinds of result"},
		{Name: "offset", Description: "Zero-based page number, from 0 through 9, counted in pages of count " +
			"results; pages may overlap. Not together with cursor"},
		cursorArgument,
		{Name: "safesearch", Description: "off, moderate, or strict; moderate when omitted"},
		{Name: "spellcheck", Description: "Whether Brave corrects the query; true when omitted. The corrected " +
			"query is then the one searched"},
		{Name: "freshness", Description: "Page age: pd (24 hours), pw (7 days), pm (31 days), py (365 days), or " +
			"a range as YYYY-MM-DDtoYYYY-MM-DD"},
		{Name: "text_decorations", Description: "Whether display texts carry highlighting markers; false when " +
			"omitted, where Brave's own default is true"},
		{Name: "result_filter", Description: "Kinds of result to return, any of discussions, faq, infobox, news, " +
			"query, summarizer, videos, web, and locations; all kinds with data when omitted. The answer always " +
			"has type and query"},
		{Name: "units", Description: "metric or imperial; Brave decides when omitted"},
		{Name: "goggles", Description: "Up to 3 goggles that re-rank the results, each an https address of a " +
			"goggle registered with Brave or inline goggle rules; Brave fetches the address, Qatlas does not"},
		{Name: "extra_snippets", Description: "Whether web results carry up to 5 further excerpts"},
		{Name: "summary", Description: "True asks Brave for the summarizer key of the query, returned as " +
			"summarizer.key and query.summary_key. Qatlas offers no tool that uses it"},
		{Name: "enable_rich_callback", Description: "True asks for rich results of queries that have them, " +
			"returned as rich.hint.callback_key. Needs the Search plan"},
		{Name: "include_fetch_metadata", Description: "Whether results carry fetch metadata such as " +
			"page_fetched; false when omitted"},
		{Name: "operators", Description: "Whether Brave applies search operators in the query; true when omitted"},
		{Name: "timezone", Description: "IANA time zone of the user such as Europe/Berlin, sent as a location " +
			"header with the other location arguments"},
		{Name: "latitude", Description: "Latitude of the user in degrees, together with longitude"},
		{Name: "longitude", Description: "Longitude of the user in degrees, together with latitude"},
		{Name: "city", Description: "City of the user"},
		{Name: "state", Description: "State or region code of the user, up to three characters"},
		{Name: "state_name", Description: "State or region name of the user"},
		{Name: "country_code", Description: "Two upper-case letters, the country of the user"},
		{Name: "postal_code", Description: "Postal code of the user"},
		{Name: "no_cache", Description: "True asks Brave to skip its cache, as a best effort"},
	},
	[]capability.Field{
		{Name: "type", Description: "Type of the answer, search"},
		{Name: "query", Description: "How Brave read the query: original, altered and cleaned by the spellchecker, " +
			"language, country, location, local intent, search_operators, related_queries, and " +
			"more_results_available, which tells whether a further page exists"},
		{Name: "web", Description: "Web results with type, results, and family_friendly; a result has title, url, " +
			"description, age, profile, meta_url, thumbnail, extra_snippets, and, where the page has them, " +
			"deep_results and the structured data video, movie, faq, qa, book, rating, article, product, " +
			"product_cluster, cluster, creative_work, music_recording, review, recipe, software, " +
			"organization, and location. Untrusted data"},
		{Name: "discussions", Description: "Forum posts, results of the same shape as web results plus data " +
			"with forum_name, num_answers, score, title, question, and top_comment. Untrusted data"},
		{Name: "infobox", Description: "Knowledge cards with title, description, long_desc, attributes, " +
			"images, ratings, and more. Untrusted data"},
		{Name: "locations", Description: "Places with an id for the points-of-interest tools, address, " +
			"coordinates, opening_hours, contact, rating, reviews, and pictures. Untrusted data"},
		{Name: "news", Description: "News articles. Untrusted data"},
		{Name: "videos", Description: "Videos with duration, views, creator, and tags. Untrusted data"},
		{Name: "faq", Description: "Questions with answers and their source. Untrusted data"},
		{Name: "mixed", Description: "Brave's ranking: main, top, and side, each a list of entries with the type " +
			"of result, its index in that group, and all for the whole group"},
		{Name: "summarizer", Description: "type and key, present with summary"},
		{Name: "rich", Description: "hint with vertical and callback_key, present with enable_rich_callback " +
			"when the query has rich results"},
		nextCursorField,
		{Name: "has_more", Description: "True while a further page exists: query.more_results_available is " +
			"true and offset is below 9. Pages may overlap"},
		quotaField,
	},
	[]capability.Example{{
		Description: "Search the web and read the first page",
		Arguments:   json.RawMessage(`{"q":"python web frameworks","count":10}`),
	}, {
		Description: "Read only news and discussions of the last week for a German user",
		Arguments: json.RawMessage(`{"q":"Energiepreise","country":"DE","search_lang":"de","ui_lang":"de-DE",` +
			`"result_filter":["news","discussions"],"freshness":"pw","timezone":"Europe/Berlin"}`),
	}})

// webArguments are the arguments of brave.web.search.
type webArguments struct {
	Query                string   `json:"q"`
	Country              string   `json:"country"`
	SearchLang           string   `json:"search_lang"`
	UILang               string   `json:"ui_lang"`
	Count                int      `json:"count"`
	Offset               *int     `json:"offset"`
	Cursor               string   `json:"cursor"`
	Safesearch           string   `json:"safesearch"`
	Spellcheck           *bool    `json:"spellcheck"`
	Freshness            string   `json:"freshness"`
	TextDecorations      *bool    `json:"text_decorations"`
	ResultFilter         []string `json:"result_filter"`
	Units                string   `json:"units"`
	Goggles              []string `json:"goggles"`
	ExtraSnippets        *bool    `json:"extra_snippets"`
	Summary              *bool    `json:"summary"`
	EnableRichCallback   *bool    `json:"enable_rich_callback"`
	IncludeFetchMetadata *bool    `json:"include_fetch_metadata"`
	Operators            *bool    `json:"operators"`
	Timezone             string   `json:"timezone"`
	NoCache              *bool    `json:"no_cache"`
	location
}

// checkResultFilter refuses a kind Brave does not document and a kind named twice.
func checkResultFilter(values []string) error {
	if len(values) > len(webResultFilters) {
		return badRequest("result_filter names more kinds than Brave documents")
	}
	for i, value := range values {
		if !oneOf(webResultFilters, value) {
			return badRequest("result_filter has a kind Brave does not document")
		}
		if slices.Contains(values[:i], value) {
			return badRequest("result_filter names a kind twice")
		}
	}
	return nil
}

// checkTimezone accepts the shape of an IANA zone name.
func checkTimezone(value string) error {
	if value != "" && (len(value) > maxTimezoneRunes || !timezonePattern.MatchString(value)) {
		return badRequest("timezone must be an IANA zone name such as Europe/Berlin")
	}
	return nil
}

// prepareWebSearch checks every argument and fixes the page to read; the request is built only from what
// passed.
func prepareWebSearch(args webArguments) (run, error) {
	query, err := checkQuery(args.Query, webMaxQueryRunes, webMaxQueryWords)
	if err != nil {
		return nil, err
	}
	for _, check := range []error{
		checkEnum("search_lang", args.SearchLang, searchLanguages),
		checkEnum("ui_lang", args.UILang, uiLanguages),
		checkEnum("country", args.Country, countries),
		checkEnum("safesearch", args.Safesearch, safeSearchModes),
		checkEnum("units", args.Units, unitSystems),
		checkFreshness(args.Freshness),
		checkResultFilter(args.ResultFilter),
		checkTimezone(args.Timezone),
		args.location.check(),
	} {
		if check != nil {
			return nil, check
		}
	}
	goggles, err := checkGoggles(args.Goggles)
	if err != nil {
		return nil, err
	}
	if args.Count != 0 && (args.Count < 1 || args.Count > webMaxCount) {
		return nil, badRequest("count must be between 1 and " + strconv.Itoa(webMaxCount))
	}
	offset := 0
	switch {
	case args.Offset != nil && (*args.Offset < 0 || *args.Offset > webMaxOffset):
		return nil, badRequest("offset must be between 0 and " + strconv.Itoa(webMaxOffset))
	case args.Offset != nil && args.Cursor != "":
		return nil, badRequest("offset and cursor cannot be given together")
	case args.Offset != nil:
		offset = *args.Offset
	}
	// Brave marks display texts by default; Qatlas asks for plain text unless the caller wants the markers.
	decorations := args.TextDecorations != nil && *args.TextDecorations
	// The cursor carries only the offset, and an offset counts pages of count results, so everything that
	// shapes the result sequence, count and location included, is part of the binding.
	binding := provider.CursorBinding(webSearch.ID, query, args.SearchLang, args.UILang, args.Country,
		args.Safesearch, args.Count, args.Spellcheck, args.Freshness, decorations, args.ResultFilter, args.Units,
		goggles, args.ExtraSnippets, args.Summary, args.EnableRichCallback, args.IncludeFetchMetadata,
		args.Operators, args.Timezone, args.location)
	if args.Cursor != "" {
		if offset, err = pageCursor(binding, args.Cursor, webMaxOffset); err != nil {
			return nil, err
		}
	}

	values := url.Values{"q": {query}, "text_decorations": {strconv.FormatBool(decorations)}}
	text(values, "search_lang", args.SearchLang)
	text(values, "ui_lang", args.UILang)
	text(values, "country", args.Country)
	text(values, "safesearch", args.Safesearch)
	text(values, "freshness", args.Freshness)
	text(values, "units", args.Units)
	if args.Count != 0 {
		values.Set("count", strconv.Itoa(args.Count))
	}
	if offset != 0 {
		values.Set("offset", strconv.Itoa(offset))
	}
	if len(args.ResultFilter) > 0 {
		values.Set("result_filter", strings.Join(args.ResultFilter, ","))
	}
	flag(values, "spellcheck", args.Spellcheck)
	flag(values, "extra_snippets", args.ExtraSnippets)
	flag(values, "enable_rich_callback", args.EnableRichCallback)
	flag(values, "include_fetch_metadata", args.IncludeFetchMetadata)
	flag(values, "operators", args.Operators)
	// Brave documents summary as text and shows the value 1; false sends nothing, as for an omitted argument.
	if args.Summary != nil && *args.Summary {
		values.Set("summary", "1")
	}
	for _, goggle := range goggles {
		values.Add("goggles", goggle)
	}
	header := args.location.headers()
	if args.Timezone != "" {
		header.Set("X-Loc-Timezone", args.Timezone)
	}
	for name, value := range noCache(args.NoCache) {
		header[name] = value
	}

	return func(ctx context.Context, client *Client) (any, error) {
		var answer map[string]any
		info, err := client.get(ctx, "search web", webPath, values, header, &answer)
		if err != nil {
			return nil, err
		}
		return webResult(answer, offset, binding, info), nil
	}, nil
}

// webResult cleans the answer and adds the paging members. Brave ends the list with
// query.more_results_available; a missing signal is read as the end.
func webResult(answer map[string]any, offset int, binding []byte, info rateInfo) map[string]any {
	cleaned, _ := webAnswerShape.clean(answer)
	out, _ := cleaned.(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	more := false
	if query, ok := out["query"].(map[string]any); ok {
		more, _ = query["more_results_available"].(bool)
	}
	out["has_more"] = more && offset < webMaxOffset
	if more && offset < webMaxOffset {
		out["next_cursor"] = provider.EncodeCursor(binding, strconv.Itoa(offset+1))
	}
	if q := quotaOf(info); q != nil {
		out["quota"] = q
	}
	return out
}
