package brave

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	llmPath = "/res/v1/llm/context"
	// Limits the LLM Context reference documents.
	llmMaxQueryRunes = 600
	llmMaxQueryWords = 75
	// Bounds of what the answer shows: chunks are longer than search snippets, because a table or a code
	// block is one chunk.
	llmMaxItems          = 50
	llmMaxSnippets       = 100
	llmMaxSnippetRunes   = 8000
	llmMaxSources        = 100
	llmMaxAge            = 4
	llmMaxCount          = 50
	llmThresholdModesDoc = "disabled, strict, lenient, or balanced"
)

// llmThresholdModes are the values context_threshold_mode takes.
var llmThresholdModes = []string{"disabled", "strict", "lenient", "balanced"}

// intRange is the documented range of an integer argument.
type intRange struct {
	name     string
	min, max int
}

var (
	rangeCount       = intRange{"count", 1, llmMaxCount}
	rangeURLs        = intRange{"maximum_number_of_urls", 1, 50}
	rangeTokens      = intRange{"maximum_number_of_tokens", 1024, 32768}
	rangeSnippets    = intRange{"maximum_number_of_snippets", 1, 256}
	rangeURLTokens   = intRange{"maximum_number_of_tokens_per_url", 512, 8192}
	rangeURLSnippets = intRange{"maximum_number_of_snippets_per_url", 1, 100}
	llmIntegerRanges = []intRange{rangeCount, rangeURLs, rangeTokens, rangeSnippets, rangeURLTokens, rangeURLSnippets}
	llmRangeProps    = intProps(llmIntegerRanges)
	llmThresholdMode = enumSchema(llmThresholdModes)
)

const (
	llmItemSchema = `{"type":"object","properties":{"url":` + stringSchema + `,"name":` + stringSchema + `,` +
		`"title":` + stringSchema + `,"snippets":{"type":"array","items":` + stringSchema + `}},` +
		`"additionalProperties":false}`
	llmThumbnailSchema = `{"anyOf":[` + stringSchema + `,{"type":"object","properties":{"src":` + stringSchema +
		`,"original":` + stringSchema + `},"additionalProperties":false}]}`
	llmSourceSchema = `{"type":"object","properties":{"title":` + stringSchema + `,"hostname":` + stringSchema + `,` +
		`"age":{"type":"array","items":` + stringSchema + `},"description":` + stringSchema + `,` +
		`"fetched_content_timestamp":` + integerSchema + `,"site_name":` + stringSchema + `,"favicon":` +
		stringSchema + `,"thumbnail":` + llmThumbnailSchema + `},"additionalProperties":false}`
	llmOutputSchema = `{"type":"object","properties":{"grounding":{"type":"object","properties":{"generic":` +
		`{"type":"array","items":` + llmItemSchema + `},"poi":` + llmItemSchema + `,"map":{"type":"array",` +
		`"items":` + llmItemSchema + `}},"required":["generic"],"additionalProperties":false},"sources":` +
		`{"type":"object","additionalProperties":` + llmSourceSchema + `},"quota":` + quotaSchema + `},` +
		`"required":["grounding"],"additionalProperties":false}`
)

// intProps renders the integer arguments with their ranges as properties of an input schema.
func intProps(ranges []intRange) string {
	out := ""
	for _, r := range ranges {
		out += `"` + r.name + `":{"type":"integer","minimum":` + strconv.Itoa(r.min) + `,"maximum":` +
			strconv.Itoa(r.max) + `},`
	}
	return out
}

// llmContext reads pre-extracted page content for an agent. The arguments are the documented parameters of
// the endpoint and the location headers; the endpoint has no pages.
var llmContext = descriptor("llm-context", "get", "Get Brave LLM Context",
	"Search the web and read pre-extracted page content for grounding: ranked sources with the relevant text "+
		"chunks of each page, sized by token and snippet budgets. Brave recommends this endpoint over Web Search "+
		"when an agent or model, not a person, reads the result; prefer it to answer a question from the web. "+
		"A location turns on local recall, which adds places and a point of interest.",
	[]string{"llm-context", "web", "grounding"},
	`{"type":"object","properties":{"q":{"type":"string","minLength":1,"maxLength":600},`+
		`"country":`+countrySchema+`,"search_lang":`+searchLangSchema+`,"safesearch":`+safeSearchSchema+`,`+
		llmRangeProps+
		`"spellcheck":`+booleanSchema+`,"operators":`+booleanSchema+`,"operators_fallback":`+booleanSchema+`,`+
		`"context_threshold_mode":`+llmThresholdMode+`,"goggles":`+gogglesSchema+`,`+
		`"freshness":`+freshnessSchema+`,"enable_local":`+booleanSchema+`,"enable_source_metadata":`+booleanSchema+`,`+
		locationInput+`,"no_cache":`+booleanSchema+`},"required":["q"],"additionalProperties":false}`,
	json.RawMessage(llmOutputSchema),
	[]capability.Argument{
		{Name: "q", Description: "Search query, at most 600 characters and 75 words", Required: true},
		{Name: "country", Description: "Country the results come from as a two-letter code such as US, or ALL " +
			"for worldwide; US when omitted"},
		{Name: "search_lang", Description: "Language of the results as a Brave language code such as en or " +
			"pt-br; en when omitted"},
		{Name: "safesearch", Description: "off, moderate, or strict; Brave filters nothing when omitted, except " +
			"for local recall, which stays strict"},
		{Name: "count", Description: "Search results considered when the context is selected, from 1 through 50; " +
			"20 when omitted"},
		{Name: "maximum_number_of_urls", Description: "Different URLs in the context, from 1 through 50; 20 when " +
			"omitted"},
		{Name: "maximum_number_of_tokens", Description: "Approximate token budget of the whole context, from " +
			"1024 through 32768; 8192 when omitted"},
		{Name: "maximum_number_of_snippets", Description: "Different snippets in the context, from 1 through " +
			"256; 50 when omitted"},
		{Name: "maximum_number_of_tokens_per_url", Description: "Token budget per URL, from 512 through 8192; " +
			"4096 when omitted"},
		{Name: "maximum_number_of_snippets_per_url", Description: "Snippets per URL, from 1 through 100; 50 " +
			"when omitted"},
		{Name: "context_threshold_mode", Description: "How strictly content must match the query to enter the " +
			"context: " + llmThresholdModesDoc + "; a threshold Brave calibrates when omitted"},
		{Name: "spellcheck", Description: "Whether Brave corrects the query; true when omitted"},
		{Name: "operators", Description: "Whether Brave applies search operators in the query; true when omitted"},
		{Name: "operators_fallback", Description: "Whether Brave falls back to the original results when " +
			"search operators find nothing; true when omitted"},
		{Name: "goggles", Description: "Up to 3 goggles that re-rank the results, each an https address of a " +
			"goggle registered with Brave or inline goggle rules; Brave fetches the address, Qatlas does not"},
		{Name: "freshness", Description: "Page age: pd (24 hours), pw (7 days), pm (31 days), py (365 days), or " +
			"a range as YYYY-MM-DDtoYYYY-MM-DD"},
		{Name: "enable_local", Description: "True forces local recall, false forces standard ranking; omitted, " +
			"Brave uses local recall when a location is given"},
		{Name: "enable_source_metadata", Description: "Whether each source also carries site_name, favicon, " +
			"thumbnail, description, and fetched_content_timestamp; false when omitted"},
		{Name: "latitude", Description: "Latitude of the user in degrees, from -90 through 90; with longitude"},
		{Name: "longitude", Description: "Longitude of the user in degrees, from -180 through 180; with latitude"},
		{Name: "city", Description: "City of the user"},
		{Name: "state", Description: "Region code of the user, up to three characters of an ISO 3166-2 code"},
		{Name: "state_name", Description: "Region name of the user"},
		{Name: "country_code", Description: "Two-letter ISO 3166-1 code of the country of the user"},
		{Name: "postal_code", Description: "Postal code of the user"},
		{Name: "no_cache", Description: "True asks Brave to skip its cache, as a best effort"},
	},
	[]capability.Field{
		{Name: "grounding", Description: "Extracted content by kind: generic for the ranked pages, and, with " +
			"local recall, poi and map"},
		{Name: "generic", Description: "Pages with extracted content, untrusted data"},
		{Name: "poi", Description: "The point of interest of a local query: name, url, title, snippets"},
		{Name: "map", Description: "Places of a local query: name, url, title, snippets"},
		{Name: "snippets", Description: "Text chunks relevant to the query, untrusted data. A chunk is plain text " +
			"or a JSON-serialized table, schema, or code block"},
		{Name: "sources", Description: "Metadata of the referenced pages, keyed by address: title, hostname, " +
			"age, and with enable_source_metadata description, fetched_content_timestamp, site_name, favicon, " +
			"and thumbnail"},
		{Name: "age", Description: "Date of the page in four positions: full date, YYYY-MM-DD, relative age, " +
			"ISO 8601 timestamp; empty when the page has no known date"},
		quotaField,
	},
	[]capability.Example{{
		Description: "Ground an answer in current web content with a small budget",
		Arguments:   json.RawMessage(`{"q":"how deep is the mediterranean sea","maximum_number_of_urls":5,"maximum_number_of_tokens":2048}`),
	}, {
		Description: "Find local places near a position",
		Arguments:   json.RawMessage(`{"q":"pizza restaurant","latitude":48.137,"longitude":11.575,"country_code":"DE"}`),
	}})

// llmArguments are the arguments of brave.llm-context.get.
type llmArguments struct {
	Query                         string   `json:"q"`
	Country                       string   `json:"country"`
	SearchLang                    string   `json:"search_lang"`
	Safesearch                    string   `json:"safesearch"`
	Count                         *int     `json:"count"`
	MaximumNumberOfURLs           *int     `json:"maximum_number_of_urls"`
	MaximumNumberOfTokens         *int     `json:"maximum_number_of_tokens"`
	MaximumNumberOfSnippets       *int     `json:"maximum_number_of_snippets"`
	MaximumNumberOfTokensPerURL   *int     `json:"maximum_number_of_tokens_per_url"`
	MaximumNumberOfSnippetsPerURL *int     `json:"maximum_number_of_snippets_per_url"`
	ContextThresholdMode          string   `json:"context_threshold_mode"`
	Spellcheck                    *bool    `json:"spellcheck"`
	Operators                     *bool    `json:"operators"`
	OperatorsFallback             *bool    `json:"operators_fallback"`
	Goggles                       []string `json:"goggles"`
	Freshness                     string   `json:"freshness"`
	EnableLocal                   *bool    `json:"enable_local"`
	EnableSourceMetadata          *bool    `json:"enable_source_metadata"`
	NoCache                       *bool    `json:"no_cache"`
	location
}

// prepareLLMContext checks every argument; the request is built only from what passed.
func prepareLLMContext(args llmArguments) (run, error) {
	query, err := checkQuery(args.Query, llmMaxQueryRunes, llmMaxQueryWords)
	if err != nil {
		return nil, err
	}
	for _, check := range []error{
		checkEnum("country", args.Country, countries),
		checkEnum("search_lang", args.SearchLang, searchLanguages),
		checkEnum("safesearch", args.Safesearch, safeSearchModes),
		checkEnum("context_threshold_mode", args.ContextThresholdMode, llmThresholdModes),
		checkFreshness(args.Freshness),
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
	values := url.Values{"q": {query}}
	for _, number := range []struct {
		limit intRange
		value *int
	}{
		{rangeCount, args.Count}, {rangeURLs, args.MaximumNumberOfURLs}, {rangeTokens, args.MaximumNumberOfTokens},
		{rangeSnippets, args.MaximumNumberOfSnippets}, {rangeURLTokens, args.MaximumNumberOfTokensPerURL},
		{rangeURLSnippets, args.MaximumNumberOfSnippetsPerURL},
	} {
		if number.value == nil {
			continue
		}
		if *number.value < number.limit.min || *number.value > number.limit.max {
			return nil, badRequest(number.limit.name + " must be between " + strconv.Itoa(number.limit.min) +
				" and " + strconv.Itoa(number.limit.max))
		}
		values.Set(number.limit.name, strconv.Itoa(*number.value))
	}
	text(values, "country", args.Country)
	text(values, "search_lang", args.SearchLang)
	text(values, "safesearch", args.Safesearch)
	text(values, "context_threshold_mode", args.ContextThresholdMode)
	text(values, "freshness", args.Freshness)
	flag(values, "spellcheck", args.Spellcheck)
	flag(values, "operators", args.Operators)
	flag(values, "operators_fallback", args.OperatorsFallback)
	flag(values, "enable_local", args.EnableLocal)
	flag(values, "enable_source_metadata", args.EnableSourceMetadata)
	for _, goggle := range goggles {
		values.Add("goggles", goggle)
	}
	header := args.location.headers()
	for name, value := range noCache(args.NoCache) {
		header[name] = value
	}

	return func(ctx context.Context, client *Client) (any, error) {
		var answer llmAnswer
		info, err := client.get(ctx, "get llm context", llmPath, values, header, &answer)
		if err != nil {
			return nil, err
		}
		return answer.result(info), nil
	}, nil
}

// llmAnswer is the 200 answer of the endpoint, read leniently: a member Brave adds is ignored, and one it
// leaves out is empty.
type llmAnswer struct {
	Grounding *struct {
		Generic []llmItem `json:"generic"`
		POI     *llmItem  `json:"poi"`
		Map     []llmItem `json:"map"`
	} `json:"grounding"`
	Sources map[string]llmSource `json:"sources"`
}

// llmItem is one page, point of interest, or place with its text chunks.
type llmItem struct {
	URL      string   `json:"url,omitempty"`
	Name     string   `json:"name,omitempty"`
	Title    string   `json:"title,omitempty"`
	Snippets []string `json:"snippets,omitempty"`
}

// llmSource is the metadata of one referenced page. Brave documents the thumbnail by name only, so it is
// read as it comes and reduced to an address or to the src/original pair.
type llmSource struct {
	Title                   string          `json:"title,omitempty"`
	Hostname                string          `json:"hostname,omitempty"`
	Age                     []string        `json:"age,omitempty"`
	Description             string          `json:"description,omitempty"`
	FetchedContentTimestamp *float64        `json:"fetched_content_timestamp,omitempty"`
	SiteName                string          `json:"site_name,omitempty"`
	Favicon                 string          `json:"favicon,omitempty"`
	Thumbnail               json.RawMessage `json:"thumbnail,omitempty"`
}

// llmGrounding is the grounding the tool returns.
type llmGrounding struct {
	Generic []llmItem `json:"generic"`
	POI     *llmItem  `json:"poi,omitempty"`
	Map     []llmItem `json:"map,omitempty"`
}

// llmResult is what the tool returns.
type llmResult struct {
	Grounding llmGrounding         `json:"grounding"`
	Sources   map[string]llmSource `json:"sources,omitempty"`
	Quota     *quota               `json:"quota,omitempty"`
}

// result bounds every string and list of the answer.
func (a llmAnswer) result(info rateInfo) llmResult {
	out := llmResult{Grounding: llmGrounding{Generic: []llmItem{}}, Quota: quotaOf(info)}
	if g := a.Grounding; g != nil {
		out.Grounding.Generic = boundedItems(g.Generic)
		if out.Grounding.Generic == nil {
			out.Grounding.Generic = []llmItem{}
		}
		if g.POI != nil {
			poi := g.POI.bounded()
			out.Grounding.POI = &poi
		}
		out.Grounding.Map = boundedItems(g.Map)
	}
	// Sources are keyed by address: a key that is not a safe address names nothing a reader could use.
	keys := make([]string, 0, len(a.Sources))
	for key := range a.Sources {
		if address(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for i, key := range keys {
		if i == llmMaxSources {
			break
		}
		if out.Sources == nil {
			out.Sources = map[string]llmSource{}
		}
		out.Sources[key] = a.Sources[key].bounded()
	}
	return out
}

func boundedItems(items []llmItem) []llmItem {
	var out []llmItem
	for i, item := range items {
		if i == llmMaxItems {
			break
		}
		out = append(out, item.bounded())
	}
	return out
}

func (i llmItem) bounded() llmItem {
	i.URL, i.Name, i.Title = address(i.URL), clip(i.Name, maxTitleRunes), clip(i.Title, maxTitleRunes)
	i.Snippets = clipAll(i.Snippets, llmMaxSnippets, llmMaxSnippetRunes)
	return i
}

func (s llmSource) bounded() llmSource {
	s.Title, s.Hostname = clip(s.Title, maxTitleRunes), clip(s.Hostname, maxShortRunes)
	s.Age = clipAll(s.Age, llmMaxAge, maxShortRunes)
	s.Description, s.SiteName = clip(s.Description, maxTextRunes), clip(s.SiteName, maxShortRunes)
	s.Favicon = address(s.Favicon)
	s.FetchedContentTimestamp = wholeNumber(s.FetchedContentTimestamp)
	s.Thumbnail = boundedThumbnail(s.Thumbnail)
	return s
}

// boundedThumbnail keeps a thumbnail given as an address or as src and original, with safe addresses only.
func boundedThumbnail(raw json.RawMessage) json.RawMessage {
	var plain string
	if json.Unmarshal(raw, &plain) == nil && raw != nil && raw[0] == '"' {
		if safe := address(plain); safe != "" {
			encoded, _ := json.Marshal(safe)
			return encoded
		}
		return nil
	}
	var pair struct {
		Src      string `json:"src,omitempty"`
		Original string `json:"original,omitempty"`
	}
	if json.Unmarshal(raw, &pair) != nil || (raw != nil && raw[0] != '{') {
		return nil
	}
	pair.Src, pair.Original = address(pair.Src), address(pair.Original)
	if pair.Src == "" && pair.Original == "" {
		return nil
	}
	encoded, _ := json.Marshal(pair)
	return encoded
}
