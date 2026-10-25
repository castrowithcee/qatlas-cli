package brave

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	suggestPath = "/res/v1/suggest/search"
	// Limits the suggestion and spellcheck references document.
	suggestMaxQueryRunes = 400
	suggestMaxQueryWords = 50
	suggestMaxCount      = 20
)

const (
	suggestQuerySchema = `{"type":"object","properties":{"original":` + stringSchema + `},"additionalProperties":false}`
	suggestItemSchema  = `{"type":"object","properties":{"query":` + stringSchema + `,"type":` + stringSchema + `,` +
		`"title":` + stringSchema + `,"description":` + stringSchema + `,"img":` + stringSchema + `},` +
		`"additionalProperties":false}`
	suggestOutputSchema = `{"type":"object","properties":{"type":` + stringSchema + `,"query":` + suggestQuerySchema + `,` +
		`"results":{"type":"array","items":` + suggestItemSchema + `},"quota":` + quotaSchema + `},` +
		`"required":["results"],"additionalProperties":false}`
)

// suggestionsList reads query completions. It has no pages: count reaches up to 20 suggestions.
var suggestionsList = descriptor("suggestions", "list", "List Brave search suggestions",
	"List query completions Brave suggests for a partial query, filtered by language and country. Needs the "+
		"Autosuggest plan of Brave; whether a key of the Spellcheck plan reaches this endpoint is not documented. "+
		"A request costs 0.0005 USD. There is no paging. rich=true adds entity titles, descriptions, and image "+
		"addresses and needs a paid Autosuggest subscription; without it the call ends with a permission error.",
	[]string{"suggestions", "autosuggest"},
	`{"type":"object","properties":{"q":{"type":"string","minLength":1,"maxLength":400},`+
		`"lang":`+searchLangSchema+`,"country":`+countrySchema+`,`+
		`"count":{"type":"integer","minimum":1,"maximum":20},"rich":`+booleanSchema+`,`+
		`"no_cache":`+booleanSchema+`},"required":["q"],"additionalProperties":false}`,
	json.RawMessage(suggestOutputSchema),
	[]capability.Argument{
		{Name: "q", Description: "Partial query to complete, at most 400 characters and 50 words", Required: true},
		{Name: "lang", Description: "Language hint as a Brave language code such as en or pt-br; en when omitted"},
		{Name: "country", Description: "Country hint as a two-letter code such as US, or ALL; US when omitted"},
		{Name: "count", Description: "Suggestions, from 1 through 20; 5 when omitted; Brave may return fewer"},
		{Name: "rich", Description: "True enriches suggestions with title, description, and image address; " +
			"needs a paid Autosuggest subscription; false when omitted"},
		{Name: "no_cache", Description: "True asks Brave to skip its cache, as a best effort"},
	},
	[]capability.Field{
		{Name: "type", Description: "Type of the answer"},
		{Name: "query", Description: "original: the query as requested"},
		{Name: "results", Description: "Suggestions, untrusted data"},
		quotaField,
		{Name: "title", Description: "Enriched title of a suggestion, with rich=true"},
		{Name: "description", Description: "Enriched description of a suggestion, with rich=true"},
		{Name: "img", Description: "Enriched image address of a suggestion, untrusted text that is never fetched"},
	},
	[]capability.Example{{
		Description: "Complete a partial query",
		Arguments:   json.RawMessage(`{"q":"how to ","count":5}`),
	}})

// suggestArguments are the arguments of brave.suggestions.list.
type suggestArguments struct {
	Query   string `json:"q"`
	Lang    string `json:"lang"`
	Country string `json:"country"`
	Count   int    `json:"count"`
	Rich    *bool  `json:"rich"`
	NoCache *bool  `json:"no_cache"`
}

// prepareSuggestionsList checks every argument; the request is built only from what passed.
func prepareSuggestionsList(args suggestArguments) (run, error) {
	query, err := checkQuery(args.Query, suggestMaxQueryRunes, suggestMaxQueryWords)
	if err != nil {
		return nil, err
	}
	for _, check := range []error{
		checkEnum("lang", args.Lang, searchLanguages),
		checkEnum("country", args.Country, countries),
	} {
		if check != nil {
			return nil, check
		}
	}
	if args.Count != 0 && (args.Count < 1 || args.Count > suggestMaxCount) {
		return nil, badRequest("count must be between 1 and " + strconv.Itoa(suggestMaxCount))
	}
	values := url.Values{"q": {query}}
	text(values, "lang", args.Lang)
	text(values, "country", args.Country)
	if args.Count != 0 {
		values.Set("count", strconv.Itoa(args.Count))
	}
	flag(values, "rich", args.Rich)
	extra := noCache(args.NoCache)

	return func(ctx context.Context, client *Client) (any, error) {
		var answer suggestAnswer
		info, err := client.get(ctx, "list suggestions", suggestPath, values, extra, &answer)
		if err != nil {
			return nil, err
		}
		return answer.result(info), nil
	}, nil
}

// suggestAnswer is the 200 answer of the suggest endpoint, read leniently. The deprecated is_entity member
// is not read: type replaces it.
type suggestAnswer struct {
	Type    string         `json:"type"`
	Query   *originalQuery `json:"query"`
	Results []suggestEntry `json:"results"`
}

// originalQuery is the query object both endpoints return: only the original query.
type originalQuery struct {
	Original string `json:"original,omitempty"`
}

type suggestEntry struct {
	Query       string `json:"query,omitempty"`
	Type        string `json:"type,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Img         string `json:"img,omitempty"`
}

// suggestResult is what the tool returns.
type suggestResult struct {
	Type    string         `json:"type,omitempty"`
	Query   *originalQuery `json:"query,omitempty"`
	Results []suggestEntry `json:"results"`
	Quota   *quota         `json:"quota,omitempty"`
}

// result bounds every string and list of the answer.
func (a suggestAnswer) result(info rateInfo) suggestResult {
	out := suggestResult{Type: clip(a.Type, maxShortRunes), Results: []suggestEntry{}, Quota: quotaOf(info)}
	if q := a.Query; q != nil {
		out.Query = &originalQuery{Original: clip(q.Original, maxTextRunes)}
	}
	for i, entry := range a.Results {
		if i == suggestMaxCount {
			break
		}
		out.Results = append(out.Results, suggestEntry{
			Query: clip(entry.Query, maxTitleRunes), Type: clip(entry.Type, maxShortRunes),
			Title: clip(entry.Title, maxTitleRunes), Description: clip(entry.Description, maxTextRunes),
			// An image address is shown as text and never fetched.
			Img: address(entry.Img),
		})
	}
	return out
}
