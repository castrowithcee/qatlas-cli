package brave

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

const suggestFixture = `{
  "type": "suggest",
  "query": {"original": "how to"},
  "results": [
    {"query": "how to tie a tie", "type": "query", "is_entity": false, "added_later": 1},
    {"query": "brave", "type": "entity", "is_entity": true, "title": "Brave", "description": "A browser",
     "img": "https://imgs.example.com/brave.png"},
    {"query": "bad", "img": "javascript:alert(1)"}
  ]
}`

const spellcheckFixture = `{"type":"spellcheck","query":{"original":"artifial inteligence"},` +
	`"results":[{"query":"artificial intelligence"}]}`

func suggestEnv(t *testing.T, body string) (*environment, *fakeBrave) {
	t.Helper()
	f := &fakeBrave{answer: reply(200, map[string]string{
		"X-RateLimit-Limit": "1, 15000", "X-RateLimit-Policy": "1;w=1, 15000;w=2592000",
		"X-RateLimit-Remaining": "1, 14523", "X-RateLimit-Reset": "1, 1234567",
	}, body)}
	return newEnvironment(t, f), f
}

func TestSuggestionsReturnEveryDocumentedFieldWithoutIsEntity(t *testing.T) {
	env, f := suggestEnv(t, suggestFixture)
	result, err := env.invoke("brave.suggestions.list", "search", `{"q":"how to"}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	if r := f.recorded()[0]; r.method != http.MethodGet || r.path != "/res/v1/suggest/search" ||
		r.rawQuery != "q=how+to" || r.header.Get("X-Subscription-Token") != tokenValue {
		t.Errorf("request = %+v", r)
	}
	if strings.Contains(result, "is_entity") {
		t.Errorf("deprecated member is shown: %s", result)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	results := got["results"].([]any)
	want := map[string]any{"query": "brave", "type": "entity", "title": "Brave", "description": "A browser",
		"img": "https://imgs.example.com/brave.png"}
	if got["type"] != "suggest" || !reflect.DeepEqual(got["query"], map[string]any{"original": "how to"}) ||
		len(results) != 3 || !reflect.DeepEqual(results[1], want) {
		t.Errorf("answer = %v", got)
	}
	if _, ok := results[2].(map[string]any)["img"]; ok {
		t.Errorf("unsafe image address is shown: %v", results[2])
	}
	if windows := got["quota"].(map[string]any)["windows"].([]any); len(windows) != 2 {
		t.Errorf("quota = %v", got["quota"])
	}
}

func TestSuggestionsSendEveryParameter(t *testing.T) {
	env, f := suggestEnv(t, suggestFixture)
	if _, err := env.invoke("brave.suggestions.list", "search",
		`{"q":" how to ","lang":"de","country":"DE","count":20,"rich":true,"no_cache":true}`); err != nil {
		t.Fatalf("invoke = %v", err)
	}
	r := f.recorded()[0]
	want := url.Values{"q": {"how to"}, "lang": {"de"}, "country": {"DE"}, "count": {"20"}, "rich": {"true"}}
	if !reflect.DeepEqual(r.query, want) || r.header.Get("Cache-Control") != "no-cache" {
		t.Errorf("query = %v, cache-control %q", r.query, r.header.Get("Cache-Control"))
	}
}

func TestSpellcheckReturnsEveryDocumentedField(t *testing.T) {
	env, f := suggestEnv(t, spellcheckFixture)
	result, err := env.invoke("brave.spellcheck.check", "search",
		`{"q":" artifial inteligence ","lang":"en","country":"US","no_cache":true}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	r := f.recorded()[0]
	want := url.Values{"q": {"artifial inteligence"}, "lang": {"en"}, "country": {"US"}}
	if r.method != http.MethodGet || r.path != "/res/v1/spellcheck/search" || !reflect.DeepEqual(r.query, want) ||
		r.header.Get("Cache-Control") != "no-cache" || r.header.Get("X-Subscription-Token") != tokenValue {
		t.Errorf("request = %+v", r)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	if got["type"] != "spellcheck" || !reflect.DeepEqual(got["query"], map[string]any{"original": "artifial inteligence"}) ||
		!reflect.DeepEqual(got["results"], []any{map[string]any{"query": "artificial intelligence"}}) {
		t.Errorf("answer = %v", got)
	}
}

func TestSpellcheckWithoutCorrectionHasEmptyResults(t *testing.T) {
	for _, body := range []string{`{"type":"spellcheck","query":{"original":"brave"},"results":[]}`,
		`{"query":{"original":"brave"}}`, `{"results":null}`, `{}`} {
		env, _ := suggestEnv(t, body)
		result, err := env.invoke("brave.spellcheck.check", "search", `{"q":"brave"}`)
		if err != nil || strings.Contains(result, `"results":[{`) {
			t.Errorf("%s: err = %v, result = %s", body, err, result)
		}
	}
}

func TestSuggestAndSpellcheckRefuseInvalidArgumentsBeforeTheKey(t *testing.T) {
	words := strings.TrimSpace(strings.Repeat("word ", 51))
	common := map[string]string{
		"missing query":     `{}`,
		"blank query":       `{"q":"   "}`,
		"long query":        `{"q":"` + strings.Repeat("a", 401) + `"}`,
		"many words":        `{"q":"` + words + `"}`,
		"control character": `{"q":"a\u0007b"}`,
		"unknown country":   `{"q":"x","country":"XX"}`,
		"unknown language":  `{"q":"x","lang":"xx"}`,
		"offset":            `{"q":"x","offset":1}`,
		"cursor":            `{"q":"x","cursor":"abc"}`,
	}
	for tool, extra := range map[string]map[string]string{
		"brave.suggestions.list": {"count zero": `{"q":"x","count":0}`, "count high": `{"q":"x","count":21}`,
			"rich type": `{"q":"x","rich":"yes"}`},
		"brave.spellcheck.check": {"count": `{"q":"x","count":5}`, "rich": `{"q":"x","rich":true}`},
	} {
		cases := map[string]string{}
		for k, v := range common {
			cases[k] = v
		}
		for k, v := range extra {
			cases[k] = v
		}
		for name, arguments := range cases {
			env, f := suggestEnv(t, suggestFixture)
			_, err := env.invoke(tool, "search", arguments)
			if !isInvalidRequest(err) || *env.reads != 0 || len(f.recorded()) != 0 {
				t.Errorf("%s %s: err = %v, reads = %d, requests = %d", tool, name, err, *env.reads, len(f.recorded()))
			}
		}
	}
}

func TestSuggestAndSpellcheckTextIsBounded(t *testing.T) {
	long := func(n int) string { return strings.Repeat("ä", n) }
	var items []string
	for i := 0; i < 30; i++ {
		items = append(items, `{"query":"q"}`)
	}
	body := `{"type":"` + long(500) + `","query":{"original":"` + long(3000) + `"},"results":[{"query":"` + long(900) +
		`","type":"` + long(500) + `","title":"` + long(900) + `","description":"` + long(3000) + `","img":"` +
		userinfoURL("e.example", "/i.png") + `"},` + strings.Join(items, ",") + `]}`
	env, _ := suggestEnv(t, body)
	result, err := env.invoke("brave.suggestions.list", "search", `{"q":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Type    string
		Query   map[string]string
		Results []map[string]string
	}
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	runes := func(s string) int { return len([]rune(s)) }
	first := got.Results[0]
	if len(got.Results) != 20 || runes(got.Type) != 201 || runes(got.Query["original"]) != 1501 ||
		runes(first["query"]) != 301 || runes(first["type"]) != 201 || runes(first["title"]) != 301 ||
		runes(first["description"]) != 1501 || first["img"] != "" {
		t.Errorf("results = %d, first = %v", len(got.Results), first)
	}
	env, _ = suggestEnv(t, body)
	result, err = env.invoke("brave.spellcheck.check", "search", `{"q":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	var spell struct{ Results []map[string]string }
	if err := json.Unmarshal([]byte(result), &spell); err != nil {
		t.Fatal(err)
	}
	if len(spell.Results) != 20 || runes(spell.Results[0]["query"]) != 900 {
		t.Errorf("spellcheck results = %d", len(spell.Results))
	}
}

func TestSuggestAndSpellcheckErrorsKeepTheArgumentsOut(t *testing.T) {
	for _, tool := range []string{"brave.suggestions.list", "brave.spellcheck.check"} {
		for _, status := range []int{400, 402, 403, 404, 422, 429, 500, 503} {
			env := newEnvironment(t, &fakeBrave{answer: reply(status, nil, errorBody(status))})
			_, err := env.invoke(tool, "search", `{"q":"secret query text"}`)
			if err == nil || strings.Contains(err.Error(), "secret query text") || strings.Contains(err.Error(), detailCanary) ||
				strings.Contains(err.Error(), tokenValue) || strings.Contains(err.Error(), "?") {
				t.Errorf("%s status %d: err = %v", tool, status, err)
			}
		}
		env := newEnvironment(t, &fakeBrave{answer: reply(200, nil, `not json`)})
		if _, err := env.invoke(tool, "search", `{"q":"x"}`); err == nil {
			t.Errorf("%s: an unreadable body passed", tool)
		}
	}
}

func TestRichSuggestionsWithoutThePlanAreAPermissionError(t *testing.T) {
	env := newEnvironment(t, &fakeBrave{answer: reply(403, nil, errorBody(403))})
	_, err := env.invoke("brave.suggestions.list", "search", `{"q":"x","rich":true}`)
	if classOf(err) != provider.ClassPermission {
		t.Errorf("err = %v, want a permission error", err)
	}
}
