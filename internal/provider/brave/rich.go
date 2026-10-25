package brave

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const richPath = "/res/v1/web/rich"

// richKeyPattern is the narrow reading of a callback key: Brave documents neither its form nor its lifetime,
// and its examples are short alphanumeric strings. The key is sent as a query value, so nothing else may
// reach it.
var richKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// richOutputSchema is the answer in Brave's member names, with the quota of the provider.
var richOutputSchema = `{"type":"object","properties":{` + richAnswerShape.propertiesSchema() +
	`,"quota":` + quotaSchema + `},"additionalProperties":false}`

// richGet reads the rich result a web search announced with a callback key.
var richGet = descriptor("rich", "get", "Get Brave rich results",
	"Read the real-time result (weather, stock, cryptocurrency, currency, sports, calculator, definition, unit "+
		"conversion, Unix time, package tracking, news, or Formula 1) that brave.web.search announces with "+
		"enable_rich_callback as rich.hint.callback_key. Needs the Search plan of Brave, and every request is "+
		"billed (0.005 USD on the Search plan). Brave documents no lifetime of the key, and an expired or unknown "+
		"key ends with the provider's error. Several providers behind the verticals require attribution when "+
		"the data is shown.",
	[]string{"rich"},
	`{"type":"object","properties":{"callback_key":{"type":"string","minLength":1,"maxLength":128,`+
		`"pattern":"^[A-Za-z0-9_-]{1,128}$"},"no_cache":`+booleanSchema+`},"required":["callback_key"],`+
		`"additionalProperties":false}`,
	json.RawMessage(richOutputSchema),
	[]capability.Argument{
		{Name: "callback_key", Description: "Callback key from rich.hint.callback_key of brave.web.search with " +
			"enable_rich_callback, up to 128 letters, digits, underscores, or hyphens. It is a key, not an address", Required: true},
		{Name: "no_cache", Description: "True asks Brave to skip its cache, as a best effort"},
	},
	[]capability.Field{
		{Name: "type", Description: "Type of the answer, rich"},
		{Name: "results", Description: "Rich results. Each has type, subtype (the vertical), provider with " +
			"type, name, url, long_name, and img, language, and the object of its vertical, untrusted data: " +
			"calculator, cryptocurrency, currency, definitions, formula1, news, packagetracker, " +
			"american_football, football, baseball, basketball, ice_hockey, cricket, stock, unitconversion, " +
			"unixtimestamp, or weather"},
		{Name: "response_callback_info", Description: "vertical, callback_key, callback_status (success or " +
			"failure), and search_lang of the callback"},
		quotaField,
	},
	[]capability.Example{{
		Description: "Read the rich result a web search announced",
		Arguments:   json.RawMessage(`{"callback_key":"86d06abffc884e9ea281a40f62e0a5a6"}`),
	}})

// richArguments are the arguments of brave.rich.get.
type richArguments struct {
	CallbackKey string `json:"callback_key"`
	NoCache     *bool  `json:"no_cache"`
}

// prepareRichGet checks the key before any secret is read; the request is built only from what passed.
func prepareRichGet(args richArguments) (run, error) {
	if !richKeyPattern.MatchString(args.CallbackKey) {
		return nil, badRequest("callback_key must be 1 to 128 letters, digits, underscores, or hyphens")
	}
	values := url.Values{"callback_key": {args.CallbackKey}}
	extra := noCache(args.NoCache)
	return func(ctx context.Context, client *Client) (any, error) {
		var answer map[string]any
		info, err := client.get(ctx, "get rich result", richPath, values, extra, &answer)
		if err != nil {
			return nil, err
		}
		return richResult(answer, info), nil
	}, nil
}

// richResult cleans the answer and adds the quota. An answer that does not fit the reference is shown as far
// as it fits, because the request was billed already.
func richResult(answer map[string]any, info rateInfo) map[string]any {
	cleaned, _ := richAnswerShape.clean(answer)
	out, _ := cleaned.(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	if q := quotaOf(info); q != nil {
		out["quota"] = q
	}
	return out
}
