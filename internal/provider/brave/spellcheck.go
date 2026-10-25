package brave

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const spellcheckPath = "/res/v1/spellcheck/search"

const (
	spellcheckItemSchema   = `{"type":"object","properties":{"query":` + stringSchema + `},"additionalProperties":false}`
	spellcheckOutputSchema = `{"type":"object","properties":{"type":` + stringSchema + `,"query":` + suggestQuerySchema + `,` +
		`"results":{"type":"array","items":` + spellcheckItemSchema + `},"quota":` + quotaSchema + `},` +
		`"required":["results"],"additionalProperties":false}`
)

// spellcheckCheck corrects the spelling of a query.
var spellcheckCheck = descriptor("spellcheck", "check", "Check the spelling of a query with Brave",
	"Check the spelling of a phrase and read the corrected query, filtered by language and country. Needs the "+
		"Spellcheck plan of Brave; whether a key of the Autosuggest plan reaches this endpoint is not documented. "+
		"A request costs 0.0005 USD. results is empty when the phrase needs no correction.",
	[]string{"spellcheck"},
	`{"type":"object","properties":{"q":{"type":"string","minLength":1,"maxLength":400},`+
		`"lang":`+searchLangSchema+`,"country":`+countrySchema+`,"no_cache":`+booleanSchema+`},`+
		`"required":["q"],"additionalProperties":false}`,
	json.RawMessage(spellcheckOutputSchema),
	[]capability.Argument{
		{Name: "q", Description: "Phrase to check, at most 400 characters and 50 words", Required: true},
		{Name: "lang", Description: "Language hint as a Brave language code such as en or pt-br; en when omitted"},
		{Name: "country", Description: "Country hint as a two-letter code such as US, or ALL; US when omitted"},
		{Name: "no_cache", Description: "True asks Brave to skip its cache, as a best effort"},
	},
	[]capability.Field{
		{Name: "type", Description: "Type of the answer"},
		{Name: "query", Description: "original: the phrase as requested; each result carries query, the corrected " +
			"phrase, untrusted data"},
		{Name: "results", Description: "Corrections, untrusted data; empty when no correction is needed"},
		quotaField,
	},
	[]capability.Example{{
		Description: "Correct a misspelled phrase",
		Arguments:   json.RawMessage(`{"q":"artifial inteligence"}`),
	}})

// spellcheckArguments are the arguments of brave.spellcheck.check.
type spellcheckArguments struct {
	Query   string `json:"q"`
	Lang    string `json:"lang"`
	Country string `json:"country"`
	NoCache *bool  `json:"no_cache"`
}

// prepareSpellcheckCheck checks every argument; the request is built only from what passed.
func prepareSpellcheckCheck(args spellcheckArguments) (run, error) {
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
	values := url.Values{"q": {query}}
	text(values, "lang", args.Lang)
	text(values, "country", args.Country)
	extra := noCache(args.NoCache)

	return func(ctx context.Context, client *Client) (any, error) {
		var answer spellcheckAnswer
		info, err := client.get(ctx, "check spelling", spellcheckPath, values, extra, &answer)
		if err != nil {
			return nil, err
		}
		return answer.result(info), nil
	}, nil
}

type spellcheckAnswer struct {
	Type    string         `json:"type"`
	Query   *originalQuery `json:"query"`
	Results []struct {
		Query string `json:"query"`
	} `json:"results"`
}

type spellcheckEntry struct {
	Query string `json:"query"`
}

// spellcheckResult is what the tool returns; results is empty when no correction is needed.
type spellcheckResult struct {
	Type    string            `json:"type,omitempty"`
	Query   *originalQuery    `json:"query,omitempty"`
	Results []spellcheckEntry `json:"results"`
	Quota   *quota            `json:"quota,omitempty"`
}

// result bounds every string and list of the answer.
func (a spellcheckAnswer) result(info rateInfo) spellcheckResult {
	out := spellcheckResult{Type: clip(a.Type, maxShortRunes), Results: []spellcheckEntry{}, Quota: quotaOf(info)}
	if q := a.Query; q != nil {
		out.Query = &originalQuery{Original: clip(q.Original, maxTextRunes)}
	}
	for i, entry := range a.Results {
		if i == suggestMaxCount {
			break
		}
		out.Results = append(out.Results, spellcheckEntry{Query: clip(entry.Query, maxTextRunes)})
	}
	return out
}
