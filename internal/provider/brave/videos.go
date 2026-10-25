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
	videosPath = "/res/v1/videos/search"
	// Limits the video reference documents.
	videosMaxQueryRunes = 400
	videosMaxQueryWords = 50
	videosMaxCount      = 50
	videosMaxOffset     = 9
	maxVideoTags        = 20
)

const (
	videosItemSchema = `{"type":"object","properties":{"type":` + stringSchema + `,"url":` + stringSchema + `,` +
		`"title":` + stringSchema + `,"description":` + stringSchema + `,"age":` + stringSchema + `,` +
		`"page_age":` + stringSchema + `,"page_fetched":` + stringSchema + `,"fetched_content_timestamp":` +
		integerSchema + `,"video":{"type":"object","properties":{"duration":` + stringSchema + `,"views":` +
		integerSchema + `,"creator":` + stringSchema + `,"publisher":` + stringSchema + `,"requires_subscription":` +
		booleanSchema + `,"tags":{"type":"array","items":` + stringSchema + `},"author":{"type":"object",` +
		`"properties":{"name":` + stringSchema + `,"url":` + stringSchema + `,"long_name":` + stringSchema + `,` +
		`"img":` + stringSchema + `},"additionalProperties":false}},"additionalProperties":false},` +
		`"meta_url":{"type":"object","properties":{"scheme":` + stringSchema + `,"netloc":` + stringSchema + `,` +
		`"hostname":` + stringSchema + `,"favicon":` + stringSchema + `,"path":` + stringSchema + `},` +
		`"additionalProperties":false},"thumbnail":{"type":"object","properties":{"src":` + stringSchema + `,` +
		`"original":` + stringSchema + `},"additionalProperties":false}},"additionalProperties":false}`
	videosOutputSchema = `{"type":"object","properties":{"type":` + stringSchema + `,"query":` + newsQuerySchema + `,` +
		`"results":{"type":"array","items":` + videosItemSchema + `},"extra":{"type":"object","properties":` +
		`{"might_be_offensive":` + booleanSchema + `},"additionalProperties":false},"next_cursor":` + stringSchema + `,` +
		`"has_more":` + booleanSchema + `,"quota":` + quotaSchema + `},"required":["results","has_more"],` +
		`"additionalProperties":false}`
)

// videosSearch searches Brave Videos. The arguments are the documented parameters of the endpoint; offset and
// cursor are the two ways to a further page.
var videosSearch = descriptor("videos", "search", "Search Brave Videos",
	"Search videos in Brave's independent index and read one page of up to 50 results, filtered by country, "+
		"language, freshness, and safe search.",
	[]string{"videos", "web"},
	`{"type":"object","properties":{"q":{"type":"string","minLength":1,"maxLength":400},`+
		`"search_lang":`+searchLangSchema+`,"ui_lang":`+uiLangSchema+`,"country":`+countrySchema+`,`+
		`"safesearch":`+safeSearchSchema+`,"count":{"type":"integer","minimum":1,"maximum":50},`+
		`"offset":{"type":"integer","minimum":0,"maximum":9},"cursor":`+cursorSchema+`,`+
		`"spellcheck":`+booleanSchema+`,"freshness":`+freshnessSchema+`,"include_fetch_metadata":`+booleanSchema+`,`+
		`"operators":`+booleanSchema+`,"no_cache":`+booleanSchema+`},"required":["q"],"additionalProperties":false}`,
	json.RawMessage(videosOutputSchema),
	[]capability.Argument{
		{Name: "q", Description: "Search query, at most 400 characters and 50 words", Required: true},
		{Name: "search_lang", Description: "Language of the results as a Brave language code such as en or " +
			"pt-br; en when omitted"},
		{Name: "ui_lang", Description: "Language of the interface texts as a locale such as en-US; en-US " +
			"when omitted"},
		{Name: "country", Description: "Country the results come from as a two-letter code such as US, or ALL " +
			"for worldwide; US when omitted"},
		{Name: "safesearch", Description: "off, moderate, or strict; moderate when omitted"},
		{Name: "count", Description: "Results per page, from 1 through 50; 20 when omitted; Brave may return fewer"},
		{Name: "offset", Description: "Zero-based page number, from 0 through 9, counted in pages of count " +
			"results; pages may overlap. Not together with cursor"},
		cursorArgument,
		{Name: "spellcheck", Description: "Whether Brave corrects the query; true when omitted. The corrected " +
			"query is then the one searched"},
		{Name: "freshness", Description: "Page age: pd (24 hours), pw (7 days), pm (31 days), py (365 days), or " +
			"a range as YYYY-MM-DDtoYYYY-MM-DD"},
		{Name: "include_fetch_metadata", Description: "Whether results carry fetch metadata such as " +
			"page_fetched; false when omitted"},
		{Name: "operators", Description: "Whether Brave applies search operators in the query; true when omitted"},
		{Name: "no_cache", Description: "True asks Brave to skip its cache, as a best effort"},
	},
	[]capability.Field{
		{Name: "type", Description: "Type of the answer, videos; each result carries its own type, video_result"},
		{Name: "query", Description: "How Brave read the query: original, altered and cleaned by the spellchecker, " +
			"spellcheck_off, show_strict_warning when safe search hid relevant adult results, and " +
			"search_operators with applied, cleaned_query, and sites"},
		{Name: "results", Description: "Videos, untrusted data"},
		{Name: "extra", Description: "might_be_offensive: true when the results may contain offensive content"},
		nextCursorField,
		{Name: "has_more", Description: "True while a further page exists: offset is below 9 and this page was " +
			"not empty. Brave names no end, so the last page may be empty"},
		quotaField,
		{Name: "title", Description: "Title of the video, untrusted data"},
		{Name: "url", Description: "Address of the video, untrusted text that is never fetched"},
		{Name: "description", Description: "Description of the video, untrusted data"},
		{Name: "age", Description: "Human-readable age such as 2 days ago"},
		{Name: "page_age", Description: "Date of the page, published or last modified"},
		{Name: "page_fetched", Description: "When Brave last fetched the page, YYYY-MM-DDTHH:MM:SSZ"},
		{Name: "fetched_content_timestamp", Description: "Timestamp of when Brave fetched the content"},
		{Name: "video", Description: "Metadata: duration, views, creator, publisher, requires_subscription, " +
			"tags, and author with name, url, long_name, and img, an image address"},
		{Name: "meta_url", Description: "Parts of the address: scheme, netloc, hostname, favicon, and path"},
		{Name: "thumbnail", Description: "Image of the video: src, an address served by Brave, and original"},
	},
	[]capability.Example{{
		Description: "Find recent videos on a topic from the last month",
		Arguments:   json.RawMessage(`{"q":"sourdough starter","freshness":"pm","count":10}`),
	}, {
		Description: "Read the second page of a search",
		Arguments:   json.RawMessage(`{"q":"sourdough starter","count":10,"offset":1}`),
	}})

// videosArguments are the arguments of brave.videos.search.
type videosArguments struct {
	Query                string `json:"q"`
	SearchLang           string `json:"search_lang"`
	UILang               string `json:"ui_lang"`
	Country              string `json:"country"`
	Safesearch           string `json:"safesearch"`
	Count                int    `json:"count"`
	Offset               *int   `json:"offset"`
	Cursor               string `json:"cursor"`
	Spellcheck           *bool  `json:"spellcheck"`
	Freshness            string `json:"freshness"`
	IncludeFetchMetadata *bool  `json:"include_fetch_metadata"`
	Operators            *bool  `json:"operators"`
	NoCache              *bool  `json:"no_cache"`
}

// prepareVideosSearch checks every argument and fixes the page to read; the request is built only from what
// passed.
func prepareVideosSearch(args videosArguments) (run, error) {
	query, err := checkQuery(args.Query, videosMaxQueryRunes, videosMaxQueryWords)
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
	if args.Count != 0 && (args.Count < 1 || args.Count > videosMaxCount) {
		return nil, badRequest("count must be between 1 and " + strconv.Itoa(videosMaxCount))
	}
	offset := 0
	switch {
	case args.Offset != nil && (*args.Offset < 0 || *args.Offset > videosMaxOffset):
		return nil, badRequest("offset must be between 0 and " + strconv.Itoa(videosMaxOffset))
	case args.Offset != nil && args.Cursor != "":
		return nil, badRequest("offset and cursor cannot be given together")
	case args.Offset != nil:
		offset = *args.Offset
	}
	// The cursor carries only the offset, and an offset counts pages of count results, so everything that
	// shapes the result sequence, count included, is part of the binding.
	binding := provider.CursorBinding(videosSearch.ID, query, args.SearchLang, args.UILang, args.Country,
		args.Safesearch, args.Count, args.Spellcheck, args.Freshness, args.IncludeFetchMetadata, args.Operators)
	if args.Cursor != "" {
		if offset, err = pageCursor(binding, args.Cursor, videosMaxOffset); err != nil {
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
	flag(values, "include_fetch_metadata", args.IncludeFetchMetadata)
	flag(values, "operators", args.Operators)
	extra := noCache(args.NoCache)

	return func(ctx context.Context, client *Client) (any, error) {
		var answer videosAnswer
		info, err := client.get(ctx, "search videos", videosPath, values, extra, &answer)
		if err != nil {
			return nil, err
		}
		return answer.result(offset, binding, info), nil
	}, nil
}

// videosAnswer is the 200 answer of the videos endpoint, read leniently: a member Brave adds is ignored, and
// one it leaves out is empty.
type videosAnswer struct {
	Type    string        `json:"type"`
	Query   *newsQuery    `json:"query"`
	Results []videosEntry `json:"results"`
	Extra   *offensive    `json:"extra"`
}

// offensive is the extra member of the video and image answers.
type offensive struct {
	MightBeOffensive *bool `json:"might_be_offensive,omitempty"`
}

type videosEntry struct {
	Type                    string   `json:"type,omitempty"`
	URL                     string   `json:"url,omitempty"`
	Title                   string   `json:"title,omitempty"`
	Description             string   `json:"description,omitempty"`
	Age                     string   `json:"age,omitempty"`
	PageAge                 string   `json:"page_age,omitempty"`
	PageFetched             string   `json:"page_fetched,omitempty"`
	FetchedContentTimestamp *float64 `json:"fetched_content_timestamp,omitempty"`
	Video                   *struct {
		Duration             string   `json:"duration,omitempty"`
		Views                *float64 `json:"views,omitempty"`
		Creator              string   `json:"creator,omitempty"`
		Publisher            string   `json:"publisher,omitempty"`
		RequiresSubscription *bool    `json:"requires_subscription,omitempty"`
		Tags                 []string `json:"tags,omitempty"`
		Author               *struct {
			Name     string `json:"name,omitempty"`
			URL      string `json:"url,omitempty"`
			LongName string `json:"long_name,omitempty"`
			Img      string `json:"img,omitempty"`
		} `json:"author,omitempty"`
	} `json:"video,omitempty"`
	MetaURL *struct {
		Scheme   string `json:"scheme,omitempty"`
		Netloc   string `json:"netloc,omitempty"`
		Hostname string `json:"hostname,omitempty"`
		Favicon  string `json:"favicon,omitempty"`
		Path     string `json:"path,omitempty"`
	} `json:"meta_url,omitempty"`
	Thumbnail *struct {
		Src      string `json:"src,omitempty"`
		Original string `json:"original,omitempty"`
	} `json:"thumbnail,omitempty"`
}

// videosResult is what the tool returns.
type videosResult struct {
	Type       string        `json:"type,omitempty"`
	Query      *newsQuery    `json:"query,omitempty"`
	Results    []videosEntry `json:"results"`
	Extra      *offensive    `json:"extra,omitempty"`
	NextCursor string        `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
	Quota      *quota        `json:"quota,omitempty"`
}

// result bounds every string and list of the answer and adds the cursor of the following page.
func (a videosAnswer) result(offset int, binding []byte, info rateInfo) videosResult {
	out := videosResult{Type: clip(a.Type, maxShortRunes), Results: []videosEntry{}, Quota: quotaOf(info),
		Extra: a.Extra}
	if q := a.Query; q != nil {
		out.Query = boundedQuery(q)
	}
	for i, entry := range a.Results {
		if i == videosMaxCount {
			break
		}
		out.Results = append(out.Results, entry.bounded())
	}
	// Brave names no end, so a page is the last when it is empty or the offsets are used up.
	if len(a.Results) > 0 && offset < videosMaxOffset {
		out.HasMore = true
		out.NextCursor = provider.EncodeCursor(binding, strconv.Itoa(offset+1))
	}
	return out
}

// boundedQuery cuts the text of the query echo to the limits.
func boundedQuery(q *newsQuery) *newsQuery {
	q.Original, q.Altered, q.Cleaned = clip(q.Original, maxTextRunes), clip(q.Altered, maxTextRunes),
		clip(q.Cleaned, maxTextRunes)
	if ops := q.SearchOperators; ops != nil {
		ops.CleanedQuery = clip(ops.CleanedQuery, maxTextRunes)
		ops.Sites = clipAll(ops.Sites, maxSites, maxShortRunes)
	}
	return q
}

// bounded returns the entry with its text cut to the limits and its addresses reduced to those that are safe
// to show.
func (e videosEntry) bounded() videosEntry {
	e.Type, e.Age = clip(e.Type, maxShortRunes), clip(e.Age, maxShortRunes)
	e.PageAge, e.PageFetched = clip(e.PageAge, maxShortRunes), clip(e.PageFetched, maxShortRunes)
	e.Title, e.Description = clip(e.Title, maxTitleRunes), clip(e.Description, maxTextRunes)
	e.URL = address(e.URL)
	e.FetchedContentTimestamp = wholeNumber(e.FetchedContentTimestamp)
	if v := e.Video; v != nil {
		v.Duration, v.Creator = clip(v.Duration, maxShortRunes), clip(v.Creator, maxShortRunes)
		v.Publisher = clip(v.Publisher, maxShortRunes)
		v.Views = wholeNumber(v.Views)
		v.Tags = clipAll(v.Tags, maxVideoTags, maxShortRunes)
		if a := v.Author; a != nil {
			a.Name, a.LongName = clip(a.Name, maxShortRunes), clip(a.LongName, maxShortRunes)
			a.URL, a.Img = address(a.URL), address(a.Img)
		}
	}
	if m := e.MetaURL; m != nil {
		m.Scheme, m.Netloc = clip(m.Scheme, maxShortRunes), clip(m.Netloc, maxShortRunes)
		m.Hostname, m.Path = clip(m.Hostname, maxShortRunes), clip(m.Path, maxShortRunes)
		m.Favicon = address(m.Favicon)
	}
	if t := e.Thumbnail; t != nil {
		t.Src, t.Original = address(t.Src), address(t.Original)
	}
	return e
}
