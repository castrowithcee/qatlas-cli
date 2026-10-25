package brave

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const (
	newsPath = "/res/v1/news/search"
	// Limits the news reference documents.
	newsMaxQueryRunes = 400
	newsMaxQueryWords = 50
	newsMaxCount      = 50
	newsMaxOffset     = 9
)

const (
	stringSchema  = `{"type":"string"}`
	integerSchema = `{"type":"integer"}`

	newsQuerySchema = `{"type":"object","properties":{"original":` + stringSchema + `,"altered":` + stringSchema + `,` +
		`"cleaned":` + stringSchema + `,"spellcheck_off":` + booleanSchema + `,"show_strict_warning":` + booleanSchema + `,` +
		`"search_operators":{"type":"object","properties":{"applied":` + booleanSchema + `,"cleaned_query":` +
		stringSchema + `,"sites":{"type":"array","items":` + stringSchema + `}},"additionalProperties":false}},` +
		`"additionalProperties":false}`
	newsItemSchema = `{"type":"object","properties":{"type":` + stringSchema + `,"title":` + stringSchema + `,` +
		`"url":` + stringSchema + `,"description":` + stringSchema + `,"age":` + stringSchema + `,` +
		`"page_age":` + stringSchema + `,"page_fetched":` + stringSchema + `,"fetched_content_timestamp":` +
		integerSchema + `,"profile":{"type":"object","properties":{"name":` + stringSchema + `,"url":` +
		stringSchema + `,"long_name":` + stringSchema + `,"img":` + stringSchema + `},"additionalProperties":false},` +
		`"meta_url":{"type":"object","properties":{"scheme":` + stringSchema + `,"netloc":` + stringSchema + `,` +
		`"hostname":` + stringSchema + `,"favicon":` + stringSchema + `,"path":` + stringSchema + `},` +
		`"additionalProperties":false},"breaking":` + booleanSchema + `,"thumbnail":{"type":"object","properties":` +
		`{"src":` + stringSchema + `,"original":` + stringSchema + `},"additionalProperties":false},` +
		`"extra_snippets":{"type":"array","items":` + stringSchema + `},"icons":{"type":"array","items":` +
		`{"type":"object","properties":{"href":` + stringSchema + `,"sizes":` + stringSchema + `,"rel":` +
		stringSchema + `,"type":` + stringSchema + `,"ext":` + stringSchema + `},"additionalProperties":false}}},` +
		`"additionalProperties":false}`
	newsOutputSchema = `{"type":"object","properties":{"type":` + stringSchema + `,"query":` + newsQuerySchema + `,` +
		`"results":{"type":"array","items":` + newsItemSchema + `},"next_cursor":` + stringSchema + `,` +
		`"has_more":` + booleanSchema + `,"quota":` + quotaSchema + `},"required":["results","has_more"],` +
		`"additionalProperties":false}`
)

// newsSearch searches Brave News. The arguments are the documented parameters of the endpoint; offset and
// cursor are the two ways to a further page.
var newsSearch = descriptor("news", "search", "Search Brave News",
	"Search news articles in Brave's independent index and read one page of up to 50 results, filtered by "+
		"country, language, freshness, and safe search or re-ranked with goggles.",
	[]string{"news", "web"},
	`{"type":"object","properties":{"q":{"type":"string","minLength":1,"maxLength":400},`+
		`"search_lang":`+searchLangSchema+`,"ui_lang":`+uiLangSchema+`,"country":`+countrySchema+`,`+
		`"safesearch":`+safeSearchSchema+`,"count":{"type":"integer","minimum":1,"maximum":50},`+
		`"offset":{"type":"integer","minimum":0,"maximum":9},"cursor":`+cursorSchema+`,`+
		`"spellcheck":`+booleanSchema+`,"freshness":`+freshnessSchema+`,"extra_snippets":`+booleanSchema+`,`+
		`"goggles":`+gogglesSchema+`,"include_fetch_metadata":`+booleanSchema+`,"operators":`+booleanSchema+`,`+
		`"no_cache":`+booleanSchema+`},"required":["q"],"additionalProperties":false}`,
	json.RawMessage(newsOutputSchema),
	[]capability.Argument{
		{Name: "q", Description: "Search query, at most 400 characters and 50 words", Required: true},
		{Name: "search_lang", Description: "Language of the results as a Brave language code such as en or " +
			"pt-br; en when omitted"},
		{Name: "ui_lang", Description: "Language of the interface texts as a locale such as en-US; en-US " +
			"when omitted"},
		{Name: "country", Description: "Country the results come from as a two-letter code such as US, or ALL " +
			"for worldwide; US when omitted"},
		{Name: "safesearch", Description: "off, moderate, or strict; strict when omitted"},
		{Name: "count", Description: "Results per page, from 1 through 50; 20 when omitted; Brave may return fewer"},
		{Name: "offset", Description: "Zero-based page number, from 0 through 9, counted in pages of count " +
			"results; pages may overlap. Not together with cursor"},
		cursorArgument,
		{Name: "spellcheck", Description: "Whether Brave corrects the query; true when omitted. The corrected " +
			"query is then the one searched"},
		{Name: "freshness", Description: "Page age: pd (24 hours), pw (7 days), pm (31 days), py (365 days), or " +
			"a range as YYYY-MM-DDtoYYYY-MM-DD"},
		{Name: "extra_snippets", Description: "Whether each result carries up to 5 further excerpts"},
		{Name: "goggles", Description: "Up to 3 goggles that re-rank the results, each an https address of a " +
			"goggle registered with Brave or inline goggle rules; Brave fetches the address, Qatlas does not"},
		{Name: "include_fetch_metadata", Description: "Whether results carry fetch metadata such as " +
			"page_fetched; false when omitted"},
		{Name: "operators", Description: "Whether Brave applies search operators in the query; true when omitted"},
		{Name: "no_cache", Description: "True asks Brave to skip its cache, as a best effort"},
	},
	[]capability.Field{
		{Name: "type", Description: "Type of the answer, news; each result carries its own type, news_result"},
		{Name: "query", Description: "How Brave read the query: original, altered and cleaned by the spellchecker, " +
			"spellcheck_off, show_strict_warning when safe search hid relevant adult results, and " +
			"search_operators with applied, cleaned_query, and sites"},
		{Name: "results", Description: "News articles, untrusted data"},
		nextCursorField,
		{Name: "has_more", Description: "True while a further page exists: offset is below 9 and this page was " +
			"not empty. Brave names no end, so the last page may be empty"},
		quotaField,
		{Name: "title", Description: "Title of the article, untrusted data"},
		{Name: "url", Description: "Address of the article, untrusted text that is never fetched"},
		{Name: "description", Description: "Description of the article, untrusted data"},
		{Name: "age", Description: "Human-readable age such as 2 days ago"},
		{Name: "page_age", Description: "Date of the page, published or last modified"},
		{Name: "page_fetched", Description: "When Brave last fetched the page, YYYY-MM-DDTHH:MM:SSZ"},
		{Name: "fetched_content_timestamp", Description: "Timestamp of when Brave fetched the content"},
		{Name: "profile", Description: "Publisher: name, url, long_name, and img, an image address"},
		{Name: "meta_url", Description: "Parts of the address: scheme, netloc, hostname, favicon, and path"},
		{Name: "breaking", Description: "True for breaking news"},
		{Name: "thumbnail", Description: "Image of the article: src, an address served by Brave, and original"},
		{Name: "extra_snippets", Description: "Up to 5 further excerpts, untrusted data"},
		{Name: "icons", Description: "Icons of the source: href, sizes, rel, type, and ext"},
	},
	[]capability.Example{{
		Description: "Read the newest articles on a topic from the last week",
		Arguments:   json.RawMessage(`{"q":"open source licensing","freshness":"pw","count":10}`),
	}, {
		Description: "Read German news of Germany",
		Arguments:   json.RawMessage(`{"q":"Energiepreise","country":"DE","search_lang":"de","ui_lang":"de-DE"}`),
	}})

// newsArguments are the arguments of brave.news.search.
type newsArguments struct {
	Query                string   `json:"q"`
	SearchLang           string   `json:"search_lang"`
	UILang               string   `json:"ui_lang"`
	Country              string   `json:"country"`
	Safesearch           string   `json:"safesearch"`
	Count                int      `json:"count"`
	Offset               *int     `json:"offset"`
	Cursor               string   `json:"cursor"`
	Spellcheck           *bool    `json:"spellcheck"`
	Freshness            string   `json:"freshness"`
	ExtraSnippets        *bool    `json:"extra_snippets"`
	Goggles              []string `json:"goggles"`
	IncludeFetchMetadata *bool    `json:"include_fetch_metadata"`
	Operators            *bool    `json:"operators"`
	NoCache              *bool    `json:"no_cache"`
}

// prepareNewsSearch checks every argument and fixes the page to read; the request is built only from what
// passed.
func prepareNewsSearch(args newsArguments) (run, error) {
	query, err := checkQuery(args.Query, newsMaxQueryRunes, newsMaxQueryWords)
	if err != nil {
		return nil, err
	}
	for _, check := range []error{
		checkEnum("search_lang", args.SearchLang, searchLanguages),
		checkEnum("ui_lang", args.UILang, uiLanguages),
		checkEnum("country", args.Country, countries),
		checkEnum("safesearch", args.Safesearch, safeSearchModes),
		checkFreshness(args.Freshness),
	} {
		if check != nil {
			return nil, check
		}
	}
	goggles, err := checkGoggles(args.Goggles)
	if err != nil {
		return nil, err
	}
	if args.Count != 0 && (args.Count < 1 || args.Count > newsMaxCount) {
		return nil, badRequest("count must be between 1 and " + strconv.Itoa(newsMaxCount))
	}
	offset := 0
	switch {
	case args.Offset != nil && (*args.Offset < 0 || *args.Offset > newsMaxOffset):
		return nil, badRequest("offset must be between 0 and " + strconv.Itoa(newsMaxOffset))
	case args.Offset != nil && args.Cursor != "":
		return nil, badRequest("offset and cursor cannot be given together")
	case args.Offset != nil:
		offset = *args.Offset
	}
	// The cursor carries only the offset, and an offset counts pages of count results, so everything that
	// shapes the result sequence, count included, is part of the binding.
	binding := provider.CursorBinding(newsSearch.ID, query, args.SearchLang, args.UILang, args.Country,
		args.Safesearch, args.Count, args.Spellcheck, args.Freshness, args.ExtraSnippets, goggles,
		args.IncludeFetchMetadata, args.Operators)
	if args.Cursor != "" {
		if offset, err = pageCursor(binding, args.Cursor, newsMaxOffset); err != nil {
			return nil, err
		}
	}

	values := url.Values{"q": {query}}
	text(values, "search_lang", args.SearchLang)
	text(values, "ui_lang", args.UILang)
	text(values, "country", args.Country)
	text(values, "safesearch", args.Safesearch)
	text(values, "freshness", args.Freshness)
	if args.Count != 0 {
		values.Set("count", strconv.Itoa(args.Count))
	}
	if offset != 0 {
		values.Set("offset", strconv.Itoa(offset))
	}
	flag(values, "spellcheck", args.Spellcheck)
	flag(values, "extra_snippets", args.ExtraSnippets)
	flag(values, "include_fetch_metadata", args.IncludeFetchMetadata)
	flag(values, "operators", args.Operators)
	for _, goggle := range goggles {
		values.Add("goggles", goggle)
	}
	extra := noCache(args.NoCache)

	return func(ctx context.Context, client *Client) (any, error) {
		var answer newsAnswer
		info, err := client.get(ctx, "search news", newsPath, values, extra, &answer)
		if err != nil {
			return nil, err
		}
		return answer.result(offset, binding, info), nil
	}, nil
}

// newsAnswer is the 200 answer of the news endpoint, read leniently: a member Brave adds is ignored, and
// one it leaves out is empty.
type newsAnswer struct {
	Type    string      `json:"type"`
	Query   *newsQuery  `json:"query"`
	Results []newsEntry `json:"results"`
}

type newsQuery struct {
	Original          string `json:"original,omitempty"`
	Altered           string `json:"altered,omitempty"`
	Cleaned           string `json:"cleaned,omitempty"`
	SpellcheckOff     *bool  `json:"spellcheck_off,omitempty"`
	ShowStrictWarning *bool  `json:"show_strict_warning,omitempty"`
	SearchOperators   *struct {
		Applied      *bool    `json:"applied,omitempty"`
		CleanedQuery string   `json:"cleaned_query,omitempty"`
		Sites        []string `json:"sites,omitempty"`
	} `json:"search_operators,omitempty"`
}

type newsEntry struct {
	Type                    string   `json:"type,omitempty"`
	Title                   string   `json:"title,omitempty"`
	URL                     string   `json:"url,omitempty"`
	Description             string   `json:"description,omitempty"`
	Age                     string   `json:"age,omitempty"`
	PageAge                 string   `json:"page_age,omitempty"`
	PageFetched             string   `json:"page_fetched,omitempty"`
	FetchedContentTimestamp *float64 `json:"fetched_content_timestamp,omitempty"`
	Profile                 *struct {
		Name     string `json:"name,omitempty"`
		URL      string `json:"url,omitempty"`
		LongName string `json:"long_name,omitempty"`
		Img      string `json:"img,omitempty"`
	} `json:"profile,omitempty"`
	MetaURL *struct {
		Scheme   string `json:"scheme,omitempty"`
		Netloc   string `json:"netloc,omitempty"`
		Hostname string `json:"hostname,omitempty"`
		Favicon  string `json:"favicon,omitempty"`
		Path     string `json:"path,omitempty"`
	} `json:"meta_url,omitempty"`
	Breaking  *bool `json:"breaking,omitempty"`
	Thumbnail *struct {
		Src      string `json:"src,omitempty"`
		Original string `json:"original,omitempty"`
	} `json:"thumbnail,omitempty"`
	ExtraSnippets []string `json:"extra_snippets,omitempty"`
	Icons         []struct {
		Href  string `json:"href,omitempty"`
		Sizes string `json:"sizes,omitempty"`
		Rel   string `json:"rel,omitempty"`
		Type  string `json:"type,omitempty"`
		Ext   string `json:"ext,omitempty"`
	} `json:"icons,omitempty"`
}

// newsResult is what the tool returns.
type newsResult struct {
	Type       string      `json:"type,omitempty"`
	Query      *newsQuery  `json:"query,omitempty"`
	Results    []newsEntry `json:"results"`
	NextCursor string      `json:"next_cursor,omitempty"`
	HasMore    bool        `json:"has_more"`
	Quota      *quota      `json:"quota,omitempty"`
}

// result bounds every string and list of the answer and adds the cursor of the following page.
func (a newsAnswer) result(offset int, binding []byte, info rateInfo) newsResult {
	out := newsResult{Type: clip(a.Type, maxShortRunes), Results: []newsEntry{}, Quota: quotaOf(info)}
	if q := a.Query; q != nil {
		q.Original, q.Altered, q.Cleaned = clip(q.Original, maxTextRunes), clip(q.Altered, maxTextRunes),
			clip(q.Cleaned, maxTextRunes)
		if ops := q.SearchOperators; ops != nil {
			ops.CleanedQuery = clip(ops.CleanedQuery, maxTextRunes)
			ops.Sites = clipAll(ops.Sites, maxSites, maxShortRunes)
		}
		out.Query = q
	}
	for i, entry := range a.Results {
		if i == newsMaxCount {
			break
		}
		out.Results = append(out.Results, entry.bounded())
	}
	// Brave names no end, so a page is the last when it is empty or the offsets are used up.
	if len(a.Results) > 0 && offset < newsMaxOffset {
		out.HasMore = true
		out.NextCursor = provider.EncodeCursor(binding, strconv.Itoa(offset+1))
	}
	return out
}

// bounded returns the entry with its text cut to the limits and its addresses reduced to those that are safe
// to show.
func (e newsEntry) bounded() newsEntry {
	e.Type, e.Age = clip(e.Type, maxShortRunes), clip(e.Age, maxShortRunes)
	e.PageAge, e.PageFetched = clip(e.PageAge, maxShortRunes), clip(e.PageFetched, maxShortRunes)
	e.Title, e.Description = clip(e.Title, maxTitleRunes), clip(e.Description, maxTextRunes)
	e.URL = address(e.URL)
	e.FetchedContentTimestamp = wholeNumber(e.FetchedContentTimestamp)
	if p := e.Profile; p != nil {
		p.Name, p.LongName = clip(p.Name, maxShortRunes), clip(p.LongName, maxShortRunes)
		p.URL, p.Img = address(p.URL), address(p.Img)
	}
	if m := e.MetaURL; m != nil {
		m.Scheme, m.Netloc = clip(m.Scheme, maxShortRunes), clip(m.Netloc, maxShortRunes)
		m.Hostname, m.Path = clip(m.Hostname, maxShortRunes), clip(m.Path, maxShortRunes)
		m.Favicon = address(m.Favicon)
	}
	if t := e.Thumbnail; t != nil {
		t.Src, t.Original = address(t.Src), address(t.Original)
	}
	e.ExtraSnippets = clipAll(e.ExtraSnippets, maxExtraSnips, maxSnippetRunes)
	if len(e.Icons) > maxIcons {
		e.Icons = e.Icons[:maxIcons]
	}
	for i := range e.Icons {
		icon := &e.Icons[i]
		icon.Href = address(icon.Href)
		icon.Sizes, icon.Rel = clip(icon.Sizes, maxShortRunes), clip(icon.Rel, maxShortRunes)
		icon.Type, icon.Ext = clip(icon.Type, maxShortRunes), clip(icon.Ext, maxShortRunes)
	}
	return e
}
