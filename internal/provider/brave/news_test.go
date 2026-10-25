package brave

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-cli/internal/provider"
)

// newsFixture carries every member the news reference documents, plus a member Brave may add later.
const newsFixture = `{
  "type": "news",
  "query": {
    "original": "open sorce",
    "altered": "open source",
    "cleaned": "open source",
    "spellcheck_off": false,
    "show_strict_warning": true,
    "search_operators": {"applied": true, "cleaned_query": "open source", "sites": ["example.com"]},
    "added_later": 1
  },
  "results": [
    {
      "type": "news_result",
      "title": "Open source wins",
      "url": "https://news.example.com/a?x=1",
      "description": "A <strong>description</strong>",
      "age": "2 days ago",
      "page_age": "2026-10-23T10:00:00",
      "page_fetched": "2026-10-24T08:00:00Z",
      "fetched_content_timestamp": 1761292800,
      "profile": {"name": "Example News", "url": "https://news.example.com", "long_name": "Example News Daily",
        "img": "https://imgs.example.com/profile.png"},
      "meta_url": {"scheme": "https", "netloc": "news.example.com", "hostname": "news.example.com",
        "favicon": "https://imgs.example.com/fav.ico", "path": "› a"},
      "breaking": true,
      "thumbnail": {"src": "https://imgs.example.com/t.jpg", "original": "https://news.example.com/t.jpg"},
      "extra_snippets": ["one", "two"],
      "icons": [{"href": "https://news.example.com/icon.png", "sizes": "32x32", "rel": "icon", "type": "image/png",
        "ext": "png"}],
      "added_later": {"x": 1}
    },
    {"type": "news_result", "title": "Sparse", "url": "https://sparse.example.org/", "description": null,
     "thumbnail": null, "profile": null}
  ]
}`

func TestNewsSearchReturnsEveryDocumentedField(t *testing.T) {
	f := &fakeBrave{}
	env := newEnvironment(t, f)
	result, err := env.invoke("brave.news.search", "search", `{"q":"open sorce"}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("result %s: %v", result, err)
	}
	query := got["query"].(map[string]any)
	operators := query["search_operators"].(map[string]any)
	if got["type"] != "news" || query["original"] != "open sorce" || query["altered"] != "open source" ||
		query["cleaned"] != "open source" || query["spellcheck_off"] != false || query["show_strict_warning"] != true ||
		operators["applied"] != true || operators["cleaned_query"] != "open source" ||
		!reflect.DeepEqual(operators["sites"], []any{"example.com"}) {
		t.Errorf("query = %v", query)
	}
	if _, ok := query["added_later"]; ok {
		t.Errorf("an unknown member is shown: %v", query)
	}
	results := got["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("%d results, want 2", len(results))
	}
	first := results[0].(map[string]any)
	want := map[string]any{
		"type": "news_result", "title": "Open source wins", "url": "https://news.example.com/a?x=1",
		"description": "A <strong>description</strong>", "age": "2 days ago", "page_age": "2026-10-23T10:00:00",
		"page_fetched": "2026-10-24T08:00:00Z", "fetched_content_timestamp": float64(1761292800), "breaking": true,
		"profile": map[string]any{"name": "Example News", "url": "https://news.example.com",
			"long_name": "Example News Daily", "img": "https://imgs.example.com/profile.png"},
		"meta_url": map[string]any{"scheme": "https", "netloc": "news.example.com", "hostname": "news.example.com",
			"favicon": "https://imgs.example.com/fav.ico", "path": "› a"},
		"thumbnail":      map[string]any{"src": "https://imgs.example.com/t.jpg", "original": "https://news.example.com/t.jpg"},
		"extra_snippets": []any{"one", "two"},
		"icons": []any{map[string]any{"href": "https://news.example.com/icon.png", "sizes": "32x32", "rel": "icon",
			"type": "image/png", "ext": "png"}},
	}
	if !reflect.DeepEqual(first, want) {
		t.Errorf("first result = %v\nwant %v", first, want)
	}
	second := results[1].(map[string]any)
	if second["title"] != "Sparse" || second["url"] != "https://sparse.example.org/" || len(second) != 3 {
		t.Errorf("sparse result = %v, want the type, title, and url only", second)
	}
	quota := got["quota"].(map[string]any)["windows"].([]any)
	if len(quota) != 2 {
		t.Fatalf("quota = %v", got["quota"])
	}
	month := quota[1].(map[string]any)
	if month["window_seconds"] != float64(2592000) || month["limit"] != float64(15000) ||
		month["remaining"] != float64(14523) || month["reset_seconds"] != float64(1234567) {
		t.Errorf("month window = %v", month)
	}
	if got["has_more"] != true || got["next_cursor"] == "" {
		t.Errorf("paging = %v / %v, want a further page", got["has_more"], got["next_cursor"])
	}
}

func TestNewsSearchSendsEveryParameter(t *testing.T) {
	f := &fakeBrave{}
	env := newEnvironment(t, f)
	_, err := env.invoke("brave.news.search", "search", `{"q":" open  source ","search_lang":"pt-br","ui_lang":"pt-BR",`+
		`"country":"BR","safesearch":"off","count":50,"offset":3,"spellcheck":false,"freshness":"2026-01-01to2026-02-28",`+
		`"extra_snippets":true,"goggles":["https://raw.githubusercontent.com/o/r/main/a.goggle","$boost=3,site=dev.to\n$discard"],`+
		`"include_fetch_metadata":true,"operators":false,"no_cache":true}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	r := f.recorded()[0]
	want := url.Values{"q": {"open  source"}, "search_lang": {"pt-br"}, "ui_lang": {"pt-BR"}, "country": {"BR"},
		"safesearch": {"off"}, "count": {"50"}, "offset": {"3"}, "spellcheck": {"false"},
		"freshness": {"2026-01-01to2026-02-28"}, "extra_snippets": {"true"}, "include_fetch_metadata": {"true"},
		"operators": {"false"}, "goggles": {"https://raw.githubusercontent.com/o/r/main/a.goggle",
			"$boost=3,site=dev.to\n$discard"}}
	if !reflect.DeepEqual(r.query, want) {
		t.Errorf("query = %v\nwant %v", r.query, want)
	}
	if r.header.Get("Cache-Control") != "no-cache" {
		t.Errorf("Cache-Control = %q", r.header.Get("Cache-Control"))
	}

	// Defaults stay with Brave: an omitted argument sends nothing.
	if _, err := env.invoke("brave.news.search", "search", `{"q":"x"}`); err != nil {
		t.Fatal(err)
	}
	r = f.recorded()[1]
	if !reflect.DeepEqual(r.query, url.Values{"q": {"x"}}) || r.header.Get("Cache-Control") != "" {
		t.Errorf("minimal query = %v, cache-control %q", r.query, r.header.Get("Cache-Control"))
	}
	if _, err := env.invoke("brave.news.search", "search", `{"q":"x","no_cache":false,"offset":0}`); err != nil {
		t.Fatal(err)
	}
	if r = f.recorded()[2]; r.header.Get("Cache-Control") != "" {
		t.Errorf("no_cache=false sent a header")
	}
}

func TestNewsSearchRefusesInvalidArgumentsBeforeTheKey(t *testing.T) {
	words := strings.TrimSpace(strings.Repeat("word ", 51))
	for name, arguments := range map[string]string{
		"missing query":      `{}`,
		"blank query":        `{"q":"   "}`,
		"long query":         `{"q":"` + strings.Repeat("a", 401) + `"}`,
		"many words":         `{"q":"` + words + `"}`,
		"control character":  `{"q":"a\u0007b"}`,
		"unknown argument":   `{"q":"x","url":"https://x.example"}`,
		"lower-case country": `{"q":"x","country":"de"}`,
		"unknown country":    `{"q":"x","country":"XX"}`,
		"unknown language":   `{"q":"x","search_lang":"xx"}`,
		"unknown ui lang":    `{"q":"x","ui_lang":"de"}`,
		"safesearch":         `{"q":"x","safesearch":"none"}`,
		"freshness word":     `{"q":"x","freshness":"pq"}`,
		"freshness no date":  `{"q":"x","freshness":"2026-02-30to2026-03-01"}`,
		"freshness order":    `{"q":"x","freshness":"2026-03-02to2026-03-01"}`,
		"count zero":         `{"q":"x","count":0}`,
		"count high":         `{"q":"x","count":51}`,
		"offset high":        `{"q":"x","offset":10}`,
		"offset negative":    `{"q":"x","offset":-1}`,
		"offset and cursor":  `{"q":"x","offset":1,"cursor":"abcdefabcdefabcdefabcdef"}`,
		"foreign cursor":     `{"q":"x","cursor":"abcdefabcdefabcdefabcdef"}`,
		"four goggles":       `{"q":"x","goggles":["a","b","c","d"]}`,
		"empty goggles":      `{"q":"x","goggles":[]}`,
		"http goggles":       `{"q":"x","goggles":["http://example.com/a.goggle"]}`,
		"ftp goggles":        `{"q":"x","goggles":["ftp://example.com/a.goggle"]}`,
		"userinfo goggles":   `{"q":"x","goggles":["https://user:pw@example.com/a.goggle"]}`,
		"long goggles":       `{"q":"x","goggles":["` + strings.Repeat("a", 2049) + `"]}`,
		"goggles control":    `{"q":"x","goggles":["a\u0000b"]}`,
		"repeated goggles":   `{"q":"x","goggles":["a","a"]}`,
		"wrong type":         `{"q":"x","spellcheck":"yes"}`,
	} {
		f := &fakeBrave{}
		env := newEnvironment(t, f)
		_, err := env.invoke("brave.news.search", "search", arguments)
		if !isInvalidRequest(err) || *env.reads != 0 || len(f.recorded()) != 0 {
			t.Errorf("%s: err = %v, reads = %d, requests = %d, want invalid-request before the key",
				name, err, *env.reads, len(f.recorded()))
			continue
		}
		if strings.Contains(err.Error(), "word word") || strings.Contains(err.Error(), "example.com") {
			t.Errorf("%s: error %q quotes the value", name, err)
		}
	}
}

func TestNewsPagesUseABoundCursor(t *testing.T) {
	f := &fakeBrave{}
	env := newEnvironment(t, f)
	first := page(t, env, `{"q":"alpha","count":2}`)
	if !first.HasMore || first.NextCursor == "" || strings.Contains(first.NextCursor, "alpha") {
		t.Fatalf("first page = %+v", first)
	}
	if got := f.recorded()[0].query; got.Get("offset") != "" {
		t.Errorf("first request offset = %q, want none", got.Get("offset"))
	}
	second := page(t, env, `{"q":"alpha","count":2,"cursor":"`+first.NextCursor+`"}`)
	if got := f.recorded()[1].query.Get("offset"); got != "1" || !second.HasMore {
		t.Errorf("second request offset = %q, more = %v, want 1 and more", got, second.HasMore)
	}
	// The cursor stays valid whatever else changes that does not shape the list.
	if _, err := env.invoke("brave.news.search", "search",
		`{"q":"alpha","count":2,"no_cache":true,"cursor":"`+first.NextCursor+`"}`); err != nil {
		t.Errorf("cursor with no_cache = %v", err)
	}

	for name, arguments := range map[string]string{
		"another query":     `{"q":"beta","count":2,"cursor":"%s"}`,
		"another page size": `{"q":"alpha","count":3,"cursor":"%s"}`,
		"no page size":      `{"q":"alpha","cursor":"%s"}`,
		"another country":   `{"q":"alpha","count":2,"country":"DE","cursor":"%s"}`,
		"another freshness": `{"q":"alpha","count":2,"freshness":"pd","cursor":"%s"}`,
		"other goggles":     `{"q":"alpha","count":2,"goggles":["$discard"],"cursor":"%s"}`,
		"cut":               `{"q":"alpha","count":2,"cursor":"%.10s"}`,
		"altered":           `{"q":"alpha","count":2,"cursor":"%s"}`,
	} {
		cursor := first.NextCursor
		if name == "altered" {
			cursor = cursor[:len(cursor)-1] + map[bool]string{true: "A", false: "B"}[cursor[len(cursor)-1] != 'A']
		}
		before := len(f.recorded())
		_, err := env.invoke("brave.news.search", "search", fmt.Sprintf(arguments, cursor))
		if !isInvalidRequest(err) || len(f.recorded()) != before {
			t.Errorf("%s: err = %v, want a refused cursor before any request", name, err)
		}
	}
}

func TestNewsPagingEnds(t *testing.T) {
	f := &fakeBrave{}
	env := newEnvironment(t, f)
	// The last offset has no further page, however full it is.
	last := page(t, env, `{"q":"x","offset":9}`)
	if last.HasMore || last.NextCursor != "" {
		t.Errorf("offset 9 = %+v, want the end", last)
	}
	// A cursor leads at most to offset 9, and the one before the last still points on.
	cursor := ""
	for offset := 0; offset < 9; offset++ {
		arguments := `{"q":"x"}`
		if cursor != "" {
			arguments = `{"q":"x","cursor":"` + cursor + `"}`
		}
		got := page(t, env, arguments)
		if !got.HasMore {
			t.Fatalf("page %d has no successor", offset)
		}
		cursor = got.NextCursor
	}
	if got := page(t, env, `{"q":"x","cursor":"`+cursor+`"}`); got.HasMore {
		t.Errorf("the page at offset 9 still has a successor")
	}
	// An empty page ends the list before the offsets do.
	empty := newEnvironment(t, &fakeBrave{answer: reply(200, nil, `{"type":"news","results":[]}`)})
	if got := page(t, empty, `{"q":"x"}`); got.HasMore || len(got.Results) != 0 {
		t.Errorf("empty page = %+v", got)
	}
	none := newEnvironment(t, &fakeBrave{answer: reply(200, nil, `{"type":"news"}`)})
	if got := page(t, none, `{"q":"x"}`); got.HasMore {
		t.Errorf("page without results = %+v", got)
	}
}

type pageResult struct {
	Results    []map[string]any `json:"results"`
	HasMore    bool             `json:"has_more"`
	NextCursor string           `json:"next_cursor"`
}

func page(t *testing.T, env *environment, arguments string) pageResult {
	t.Helper()
	result, err := env.invoke("brave.news.search", "search", arguments)
	if err != nil {
		t.Fatalf("invoke %s = %v", arguments, err)
	}
	var out pageResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("result %s: %v", result, err)
	}
	return out
}

func TestNewsTextAndListsAreBounded(t *testing.T) {
	long := func(n int) string { return strings.Repeat("ä", n) }
	var results []string
	for i := 0; i < 80; i++ {
		results = append(results, `{"title":"t`+fmt.Sprint(i)+`","url":"https://e.example/`+fmt.Sprint(i)+`"}`)
	}
	big := `{"type":"` + long(500) + `","query":{"original":"` + long(3000) + `","search_operators":{"cleaned_query":"` +
		long(3000) + `","sites":[` + strings.TrimSuffix(strings.Repeat(`"s.example",`, 40), ",") + `]}},"results":[{` +
		`"title":"` + long(900) + `","description":"` + long(5000) + `","age":"` + long(900) + `",` +
		`"url":"https://e.example/` + strings.Repeat("p", 3000) + `",` +
		`"profile":{"name":"` + long(900) + `","url":"javascript:alert(1)","img":"data:image/png;base64,AAAA"},` +
		`"meta_url":{"hostname":"` + long(900) + `","favicon":"` + userinfoURL("e.example", "/f.ico") + `"},` +
		`"thumbnail":{"src":"//e.example/t.jpg","original":"https://e.example/t o.jpg"},` +
		`"fetched_content_timestamp":1.9,` +
		`"extra_snippets":["` + long(5000) + `","b","c","d","e","f","g"],` +
		`"icons":[` + strings.TrimSuffix(strings.Repeat(`{"href":"/relative.ico","sizes":"16x16"},`, 15), ",") + `]},` +
		strings.Join(results, ",") + `]}`
	env := newEnvironment(t, &fakeBrave{answer: reply(200, nil, big)})
	result, err := env.invoke("brave.news.search", "search", `{"q":"x"}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var got struct {
		Type  string         `json:"type"`
		Query map[string]any `json:"query"`
		List  []entryOut     `json:"results"`
	}
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	runes := func(s string) int { return len([]rune(s)) }
	first := got.List[0]
	if len(got.List) != 50 {
		t.Errorf("%d results, want the first 50", len(got.List))
	}
	if runes(got.Type) != 201 || runes(first.Title) != 301 || runes(first.Description) != 1501 ||
		runes(first.Age) != 201 || runes(first.Profile.Name) != 201 || runes(first.MetaURL.Hostname) != 201 {
		t.Errorf("text lengths %d %d %d %d %d %d", runes(got.Type), runes(first.Title), runes(first.Description),
			runes(first.Age), runes(first.Profile.Name), runes(first.MetaURL.Hostname))
	}
	if !strings.HasSuffix(first.Title, "…") {
		t.Errorf("a cut text carries no mark")
	}
	if len(first.ExtraSnippets) != 5 || runes(first.ExtraSnippets[0]) != 1001 || len(first.Icons) != 10 {
		t.Errorf("snippets = %d (%d), icons = %d", len(first.ExtraSnippets), runes(first.ExtraSnippets[0]), len(first.Icons))
	}
	if runes(got.Query["original"].(string)) != 1501 ||
		len(got.Query["search_operators"].(map[string]any)["sites"].([]any)) != 20 {
		t.Errorf("query = %v", got.Query)
	}
	// Addresses that are too long, not http(s), carry user information or spaces, or are relative are left out.
	if first.URL != "" || first.Profile.URL != "" || first.Profile.Img != "" || first.MetaURL.Favicon != "" ||
		first.Thumbnail.Src != "" || first.Thumbnail.Original != "" || first.Icons[0].Href != "" {
		t.Errorf("an unsafe address is shown: %+v", first)
	}
	if first.FetchedContentTimestamp != 1 {
		t.Errorf("timestamp = %v, want the whole number", first.FetchedContentTimestamp)
	}
	if got.List[1].URL != "https://e.example/0" {
		t.Errorf("a plain address was changed: %q", got.List[1].URL)
	}
}

type entryOut struct {
	Title, Description, Age, URL string
	FetchedContentTimestamp      float64 `json:"fetched_content_timestamp"`
	Profile                      struct{ Name, URL, Img string }
	MetaURL                      struct{ Hostname, Favicon string } `json:"meta_url"`
	Thumbnail                    struct{ Src, Original string }
	ExtraSnippets                []string `json:"extra_snippets"`
	Icons                        []struct{ Href string }
}

func TestNewsAnswersOtherwiseSparseStillPass(t *testing.T) {
	for name, body := range map[string]string{
		"empty object": `{}`,
		"only type":    `{"type":"news"}`,
		"null members": `{"type":null,"query":null,"results":null}`,
		"null entry members": `{"results":[{"title":null,"url":null,"profile":null,"meta_url":null,` +
			`"thumbnail":null,"extra_snippets":null,"icons":null,"breaking":null,"fetched_content_timestamp":null}]}`,
	} {
		env := newEnvironment(t, &fakeBrave{answer: reply(200, nil, body)})
		if _, err := env.invoke("brave.news.search", "search", `{"q":"x"}`); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestTheKeyAndProviderTextNeverReachAResult(t *testing.T) {
	body := `{"type":"news","results":[{"title":"Bearer ` + tokenValue + `","url":"https://e.example/"}]}`
	env := newEnvironment(t, &fakeBrave{answer: reply(200, nil, body)})
	result, err := env.invoke("brave.news.search", "search", `{"q":"x"}`)
	if err != nil || strings.Contains(result, tokenValue) {
		t.Errorf("result %s, err %v: the key reached the output", result, err)
	}
}

func TestNewsErrorsKeepTheArgumentsOut(t *testing.T) {
	env := newEnvironment(t, &fakeBrave{answer: reply(http.StatusInternalServerError, nil, errorBody(500))})
	_, err := env.invoke("brave.news.search", "search", `{"q":"secret query text","goggles":["$boost=1"]}`)
	if classOf(err) != provider.ClassProviderError || strings.Contains(err.Error(), "secret query text") ||
		strings.Contains(err.Error(), "boost") {
		t.Errorf("err = %v", err)
	}
}

// userinfoURL builds an address with user information, which every result address must lose.
func userinfoURL(host, path string) string {
	return (&url.URL{Scheme: "https", User: url.UserPassword("user", "pw"), Host: host, Path: path}).String()
}
