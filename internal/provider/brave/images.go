package brave

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	imagesPath = "/res/v1/images/search"
	// Limits the image reference documents.
	imagesMaxQueryRunes = 400
	imagesMaxQueryWords = 50
	imagesMaxCount      = 200
)

var imageSafeSearchModes = []string{"off", "strict"}

const (
	imagesQuerySchema = `{"type":"object","properties":{"original":` + stringSchema + `,"altered":` + stringSchema + `,` +
		`"spellcheck_off":` + booleanSchema + `,"show_strict_warning":` + booleanSchema + `},` +
		`"additionalProperties":false}`
	imagesItemSchema = `{"type":"object","properties":{"type":` + stringSchema + `,"title":` + stringSchema + `,` +
		`"url":` + stringSchema + `,"source":` + stringSchema + `,"page_fetched":` + stringSchema + `,` +
		`"thumbnail":{"type":"object","properties":{"src":` + stringSchema + `,"width":` + integerSchema + `,` +
		`"height":` + integerSchema + `},"additionalProperties":false},"properties":{"type":"object","properties":` +
		`{"url":` + stringSchema + `,"placeholder":` + stringSchema + `,"width":` + integerSchema + `,"height":` +
		integerSchema + `},"additionalProperties":false},"meta_url":{"type":"object","properties":{"scheme":` +
		stringSchema + `,"netloc":` + stringSchema + `,"hostname":` + stringSchema + `,"favicon":` + stringSchema + `,` +
		`"path":` + stringSchema + `},"additionalProperties":false},"confidence":` + stringSchema + `},` +
		`"additionalProperties":false}`
	imagesOutputSchema = `{"type":"object","properties":{"type":` + stringSchema + `,"query":` + imagesQuerySchema + `,` +
		`"results":{"type":"array","items":` + imagesItemSchema + `},"extra":{"type":"object","properties":` +
		`{"might_be_offensive":` + booleanSchema + `},"additionalProperties":false},"quota":` + quotaSchema + `},` +
		`"required":["results"],"additionalProperties":false}`
)

// imagesSearch searches Brave Images. It has no pages: count reaches up to 200 results.
var imagesSearch = descriptor("images", "search", "Search Brave Images",
	"Search images in Brave's independent index and read up to 200 results in one answer, filtered by country, "+
		"language, and safe search. The answer carries addresses only; Qatlas fetches no image. safesearch=off "+
		"can return adult content.",
	[]string{"images", "web"},
	`{"type":"object","properties":{"q":{"type":"string","minLength":1,"maxLength":400},`+
		`"search_lang":`+searchLangSchema+`,"country":`+countrySchema+`,`+
		`"safesearch":`+enumSchema(imageSafeSearchModes)+`,"count":{"type":"integer","minimum":1,"maximum":200},`+
		`"spellcheck":`+booleanSchema+`,"no_cache":`+booleanSchema+`},"required":["q"],"additionalProperties":false}`,
	json.RawMessage(imagesOutputSchema),
	[]capability.Argument{
		{Name: "q", Description: "Search query, at most 400 characters and 50 words", Required: true},
		{Name: "search_lang", Description: "Language of the results as a Brave language code such as en or " +
			"pt-br; en when omitted"},
		{Name: "country", Description: "Country the results come from as a two-letter code such as US, or ALL " +
			"for worldwide; US when omitted"},
		{Name: "safesearch", Description: "off (no filtering except illegal content, may return adult content) " +
			"or strict; strict when omitted"},
		{Name: "count", Description: "Results, from 1 through 200; 50 when omitted; Brave may return fewer. " +
			"There is no paging"},
		{Name: "spellcheck", Description: "Whether Brave corrects the query; true when omitted. The corrected " +
			"query is then the one searched"},
		{Name: "no_cache", Description: "True asks Brave to skip its cache, as a best effort"},
	},
	[]capability.Field{
		{Name: "type", Description: "Type of the answer, images; each result carries its own type, image_result"},
		{Name: "query", Description: "How Brave read the query: original, altered by the spellchecker, " +
			"spellcheck_off, and show_strict_warning when safe search hid relevant adult results"},
		{Name: "results", Description: "Images, untrusted data"},
		{Name: "extra", Description: "might_be_offensive: true when the results may contain offensive content"},
		quotaField,
		{Name: "title", Description: "Title of the image, untrusted data"},
		{Name: "url", Description: "Address of the page the image was found on, untrusted text that is never fetched"},
		{Name: "source", Description: "Domain the image was found on"},
		{Name: "page_fetched", Description: "When Brave last fetched the page, YYYY-MM-DDTHH:MM:SSZ"},
		{Name: "thumbnail", Description: "Thumbnail: src, an address served by Brave, with width and height"},
		{Name: "properties", Description: "The image itself: url, the original address, placeholder, the address " +
			"of a low-resolution version served by Brave, width, and height. Addresses only, never fetched"},
		{Name: "meta_url", Description: "Parts of the page address: scheme, netloc, hostname, favicon, and path"},
		{Name: "confidence", Description: "Confidence level of the result, as Brave names it"},
	},
	[]capability.Example{{
		Description: "Find images of a subject",
		Arguments:   json.RawMessage(`{"q":"mountain landscape","count":20}`),
	}})

// imagesArguments are the arguments of brave.images.search.
type imagesArguments struct {
	Query      string `json:"q"`
	SearchLang string `json:"search_lang"`
	Country    string `json:"country"`
	Safesearch string `json:"safesearch"`
	Count      int    `json:"count"`
	Spellcheck *bool  `json:"spellcheck"`
	NoCache    *bool  `json:"no_cache"`
}

// prepareImagesSearch checks every argument; the request is built only from what passed.
func prepareImagesSearch(args imagesArguments) (run, error) {
	query, err := checkQuery(args.Query, imagesMaxQueryRunes, imagesMaxQueryWords)
	if err != nil {
		return nil, err
	}
	for _, check := range []error{
		checkEnum("search_lang", args.SearchLang, searchLanguages),
		checkEnum("country", args.Country, countries),
		checkEnum("safesearch", args.Safesearch, imageSafeSearchModes),
	} {
		if check != nil {
			return nil, check
		}
	}
	if args.Count != 0 && (args.Count < 1 || args.Count > imagesMaxCount) {
		return nil, badRequest("count must be between 1 and " + strconv.Itoa(imagesMaxCount))
	}
	values := url.Values{"q": {query}}
	text(values, "search_lang", args.SearchLang)
	text(values, "country", args.Country)
	text(values, "safesearch", args.Safesearch)
	if args.Count != 0 {
		values.Set("count", strconv.Itoa(args.Count))
	}
	flag(values, "spellcheck", args.Spellcheck)
	extra := noCache(args.NoCache)

	return func(ctx context.Context, client *Client) (any, error) {
		var answer imagesAnswer
		info, err := client.get(ctx, "search images", imagesPath, values, extra, &answer)
		if err != nil {
			return nil, err
		}
		return answer.result(info), nil
	}, nil
}

// imagesAnswer is the 200 answer of the images endpoint, read leniently: a member Brave adds is ignored, and
// one it leaves out is empty.
type imagesAnswer struct {
	Type    string        `json:"type"`
	Query   *imagesQuery  `json:"query"`
	Results []imagesEntry `json:"results"`
	Extra   *offensive    `json:"extra"`
}

type imagesQuery struct {
	Original          string `json:"original,omitempty"`
	Altered           string `json:"altered,omitempty"`
	SpellcheckOff     *bool  `json:"spellcheck_off,omitempty"`
	ShowStrictWarning *bool  `json:"show_strict_warning,omitempty"`
}

type imagesEntry struct {
	Type        string `json:"type,omitempty"`
	Title       string `json:"title,omitempty"`
	URL         string `json:"url,omitempty"`
	Source      string `json:"source,omitempty"`
	PageFetched string `json:"page_fetched,omitempty"`
	Thumbnail   *struct {
		Src    string   `json:"src,omitempty"`
		Width  *float64 `json:"width,omitempty"`
		Height *float64 `json:"height,omitempty"`
	} `json:"thumbnail,omitempty"`
	Properties *struct {
		URL         string   `json:"url,omitempty"`
		Placeholder string   `json:"placeholder,omitempty"`
		Width       *float64 `json:"width,omitempty"`
		Height      *float64 `json:"height,omitempty"`
	} `json:"properties,omitempty"`
	MetaURL *struct {
		Scheme   string `json:"scheme,omitempty"`
		Netloc   string `json:"netloc,omitempty"`
		Hostname string `json:"hostname,omitempty"`
		Favicon  string `json:"favicon,omitempty"`
		Path     string `json:"path,omitempty"`
	} `json:"meta_url,omitempty"`
	Confidence string `json:"confidence,omitempty"`
}

// imagesResult is what the tool returns.
type imagesResult struct {
	Type    string        `json:"type,omitempty"`
	Query   *imagesQuery  `json:"query,omitempty"`
	Results []imagesEntry `json:"results"`
	Extra   *offensive    `json:"extra,omitempty"`
	Quota   *quota        `json:"quota,omitempty"`
}

// result bounds every string and list of the answer.
func (a imagesAnswer) result(info rateInfo) imagesResult {
	out := imagesResult{Type: clip(a.Type, maxShortRunes), Results: []imagesEntry{}, Quota: quotaOf(info),
		Extra: a.Extra}
	if q := a.Query; q != nil {
		q.Original, q.Altered = clip(q.Original, maxTextRunes), clip(q.Altered, maxTextRunes)
		out.Query = q
	}
	for i, entry := range a.Results {
		if i == imagesMaxCount {
			break
		}
		out.Results = append(out.Results, entry.bounded())
	}
	return out
}

// bounded returns the entry with its text cut to the limits and its addresses reduced to those that are safe
// to show. Image addresses are text only: nothing here fetches them.
func (e imagesEntry) bounded() imagesEntry {
	e.Type, e.PageFetched = clip(e.Type, maxShortRunes), clip(e.PageFetched, maxShortRunes)
	e.Title, e.Source = clip(e.Title, maxTitleRunes), clip(e.Source, maxShortRunes)
	e.Confidence = clip(e.Confidence, maxShortRunes)
	e.URL = address(e.URL)
	if t := e.Thumbnail; t != nil {
		t.Src = address(t.Src)
		t.Width, t.Height = wholeNumber(t.Width), wholeNumber(t.Height)
	}
	if p := e.Properties; p != nil {
		p.URL, p.Placeholder = address(p.URL), address(p.Placeholder)
		p.Width, p.Height = wholeNumber(p.Width), wholeNumber(p.Height)
	}
	if m := e.MetaURL; m != nil {
		m.Scheme, m.Netloc = clip(m.Scheme, maxShortRunes), clip(m.Netloc, maxShortRunes)
		m.Hostname, m.Path = clip(m.Hostname, maxShortRunes), clip(m.Path, maxShortRunes)
		m.Favicon = address(m.Favicon)
	}
	return e
}
