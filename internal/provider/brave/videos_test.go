package brave

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// videosFixture carries every member the videos reference documents, plus a member Brave may add later.
const videosFixture = `{
  "type": "videos",
  "query": {
    "original": "sourdough strter",
    "altered": "sourdough starter",
    "cleaned": "sourdough starter",
    "spellcheck_off": false,
    "show_strict_warning": true,
    "search_operators": {"applied": true, "cleaned_query": "sourdough starter", "sites": ["example.com"]},
    "added_later": 1
  },
  "results": [
    {
      "type": "video_result",
      "url": "https://videos.example.com/watch?v=1",
      "title": "Starter basics",
      "description": "How to <strong>start</strong>",
      "age": "3 days ago",
      "page_age": "2026-10-22T10:00:00",
      "page_fetched": "2026-10-24T08:00:00Z",
      "fetched_content_timestamp": 1761292800,
      "video": {"duration": "12:34", "views": 4200, "creator": "Baker", "publisher": "Example Video",
        "requires_subscription": true, "tags": ["bread", "baking"],
        "author": {"name": "Baker", "url": "https://videos.example.com/baker", "long_name": "Baker Official",
          "img": "https://imgs.example.com/baker.png"}, "added_later": 1},
      "meta_url": {"scheme": "https", "netloc": "videos.example.com", "hostname": "videos.example.com",
        "favicon": "https://imgs.example.com/fav.ico", "path": "› watch"},
      "thumbnail": {"src": "https://imgs.example.com/t.jpg", "original": "https://videos.example.com/t.jpg"},
      "added_later": {"x": 1}
    },
    {"type": "video_result", "title": "Sparse", "url": "https://sparse.example.org/", "video": null,
     "thumbnail": null}
  ],
  "extra": {"might_be_offensive": true}
}`

func videosEnv(t *testing.T) (*environment, *fakeBrave) {
	t.Helper()
	f := &fakeBrave{answer: reply(200, map[string]string{
		"X-RateLimit-Limit": "1, 15000", "X-RateLimit-Policy": "1;w=1, 15000;w=2592000",
		"X-RateLimit-Remaining": "1, 14523", "X-RateLimit-Reset": "1, 1234567",
	}, videosFixture)}
	return newEnvironment(t, f), f
}

func TestVideosSearchReturnsEveryDocumentedField(t *testing.T) {
	env, f := videosEnv(t)
	result, err := env.invoke("brave.videos.search", "search", `{"q":"sourdough strter"}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	r := f.recorded()[0]
	if r.method != http.MethodGet || r.path != "/res/v1/videos/search" || r.header.Get("X-Subscription-Token") != tokenValue ||
		r.rawQuery != "q=sourdough+strter" {
		t.Errorf("request = %+v", r)
	}
	query := got["query"].(map[string]any)
	operators := query["search_operators"].(map[string]any)
	if got["type"] != "videos" || query["original"] != "sourdough strter" || query["altered"] != "sourdough starter" ||
		query["cleaned"] != "sourdough starter" || query["spellcheck_off"] != false || query["show_strict_warning"] != true ||
		operators["applied"] != true || operators["cleaned_query"] != "sourdough starter" ||
		!reflect.DeepEqual(operators["sites"], []any{"example.com"}) {
		t.Errorf("query = %v", query)
	}
	if _, ok := query["added_later"]; ok {
		t.Errorf("an unknown member is shown: %v", query)
	}
	if !reflect.DeepEqual(got["extra"], map[string]any{"might_be_offensive": true}) {
		t.Errorf("extra = %v", got["extra"])
	}
	results := got["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("%d results, want 2", len(results))
	}
	want := map[string]any{
		"type": "video_result", "url": "https://videos.example.com/watch?v=1", "title": "Starter basics",
		"description": "How to <strong>start</strong>", "age": "3 days ago", "page_age": "2026-10-22T10:00:00",
		"page_fetched": "2026-10-24T08:00:00Z", "fetched_content_timestamp": float64(1761292800),
		"video": map[string]any{"duration": "12:34", "views": float64(4200), "creator": "Baker",
			"publisher": "Example Video", "requires_subscription": true, "tags": []any{"bread", "baking"},
			"author": map[string]any{"name": "Baker", "url": "https://videos.example.com/baker",
				"long_name": "Baker Official", "img": "https://imgs.example.com/baker.png"}},
		"meta_url": map[string]any{"scheme": "https", "netloc": "videos.example.com", "hostname": "videos.example.com",
			"favicon": "https://imgs.example.com/fav.ico", "path": "› watch"},
		"thumbnail": map[string]any{"src": "https://imgs.example.com/t.jpg", "original": "https://videos.example.com/t.jpg"},
	}
	if first := results[0].(map[string]any); !reflect.DeepEqual(first, want) {
		t.Errorf("first result = %v\nwant %v", first, want)
	}
	if second := results[1].(map[string]any); len(second) != 3 || second["title"] != "Sparse" {
		t.Errorf("sparse result = %v", second)
	}
	windows := got["quota"].(map[string]any)["windows"].([]any)
	if len(windows) != 2 || windows[1].(map[string]any)["remaining"] != float64(14523) {
		t.Errorf("quota = %v", got["quota"])
	}
	if got["has_more"] != true || got["next_cursor"] == "" {
		t.Errorf("paging = %v / %v", got["has_more"], got["next_cursor"])
	}
}

func TestVideosSearchSendsEveryParameter(t *testing.T) {
	env, f := videosEnv(t)
	_, err := env.invoke("brave.videos.search", "search", `{"q":" sourdough  starter ","search_lang":"pt-br",`+
		`"ui_lang":"pt-BR","country":"BR","safesearch":"strict","count":50,"offset":3,"spellcheck":false,`+
		`"freshness":"2026-01-01to2026-02-28","include_fetch_metadata":true,"operators":false,"no_cache":true}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	r := f.recorded()[0]
	want := url.Values{"q": {"sourdough  starter"}, "search_lang": {"pt-br"}, "ui_lang": {"pt-BR"}, "country": {"BR"},
		"safesearch": {"strict"}, "count": {"50"}, "offset": {"3"}, "spellcheck": {"false"},
		"freshness": {"2026-01-01to2026-02-28"}, "include_fetch_metadata": {"true"}, "operators": {"false"}}
	if r.path != "/res/v1/videos/search" || !reflect.DeepEqual(r.query, want) {
		t.Errorf("query = %v\nwant %v", r.query, want)
	}
	if r.header.Get("Cache-Control") != "no-cache" {
		t.Errorf("Cache-Control = %q", r.header.Get("Cache-Control"))
	}
	for _, mode := range []string{"off", "moderate", "strict"} {
		if _, err := env.invoke("brave.videos.search", "search", `{"q":"x","safesearch":"`+mode+`"}`); err != nil {
			t.Errorf("safesearch %s: %v", mode, err)
		}
	}
	if _, err := env.invoke("brave.videos.search", "search", `{"q":"x"}`); err != nil {
		t.Fatal(err)
	}
	all := f.recorded()
	if last := all[len(all)-1]; !reflect.DeepEqual(last.query, url.Values{"q": {"x"}}) || last.header.Get("Cache-Control") != "" {
		t.Errorf("minimal request = %v", last)
	}
}

func TestVideosSearchRefusesInvalidArgumentsBeforeTheKey(t *testing.T) {
	words := strings.TrimSpace(strings.Repeat("word ", 51))
	for name, arguments := range map[string]string{
		"missing query":     `{}`,
		"blank query":       `{"q":"   "}`,
		"long query":        `{"q":"` + strings.Repeat("a", 401) + `"}`,
		"many words":        `{"q":"` + words + `"}`,
		"control character": `{"q":"a\u0007b"}`,
		"unknown argument":  `{"q":"x","goggles":["$discard"]}`,
		"extra snippets":    `{"q":"x","extra_snippets":true}`,
		"unknown country":   `{"q":"x","country":"XX"}`,
		"unknown language":  `{"q":"x","search_lang":"xx"}`,
		"unknown ui lang":   `{"q":"x","ui_lang":"de"}`,
		"safesearch":        `{"q":"x","safesearch":"none"}`,
		"freshness word":    `{"q":"x","freshness":"pq"}`,
		"count zero":        `{"q":"x","count":0}`,
		"count high":        `{"q":"x","count":51}`,
		"offset high":       `{"q":"x","offset":10}`,
		"offset negative":   `{"q":"x","offset":-1}`,
		"offset and cursor": `{"q":"x","offset":1,"cursor":"abcdefabcdefabcdefabcdef"}`,
		"foreign cursor":    `{"q":"x","cursor":"abcdefabcdefabcdefabcdef"}`,
		"wrong type":        `{"q":"x","operators":"yes"}`,
	} {
		env, f := videosEnv(t)
		_, err := env.invoke("brave.videos.search", "search", arguments)
		if !isInvalidRequest(err) || *env.reads != 0 || len(f.recorded()) != 0 {
			t.Errorf("%s: err = %v, reads = %d, requests = %d, want invalid-request before the key",
				name, err, *env.reads, len(f.recorded()))
		}
	}
}

func videoPage(t *testing.T, env *environment, arguments string) pageResult {
	t.Helper()
	result, err := env.invoke("brave.videos.search", "search", arguments)
	if err != nil {
		t.Fatalf("invoke %s = %v", arguments, err)
	}
	var out pageResult
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestVideosPagesUseABoundCursor(t *testing.T) {
	env, f := videosEnv(t)
	first := videoPage(t, env, `{"q":"alpha","count":2}`)
	if !first.HasMore || first.NextCursor == "" || strings.Contains(first.NextCursor, "alpha") {
		t.Fatalf("first page = %+v", first)
	}
	if got := f.recorded()[0].query; got.Get("offset") != "" {
		t.Errorf("first request offset = %q, want none", got.Get("offset"))
	}
	second := videoPage(t, env, `{"q":"alpha","count":2,"cursor":"`+first.NextCursor+`"}`)
	if got := f.recorded()[1].query.Get("offset"); got != "1" || !second.HasMore {
		t.Errorf("second request offset = %q, more = %v", got, second.HasMore)
	}
	if _, err := env.invoke("brave.videos.search", "search",
		`{"q":"alpha","count":2,"no_cache":true,"cursor":"`+first.NextCursor+`"}`); err != nil {
		t.Errorf("cursor with no_cache = %v", err)
	}
	for name, arguments := range map[string]string{
		"another query":      `{"q":"beta","count":2,"cursor":"%s"}`,
		"another page size":  `{"q":"alpha","count":3,"cursor":"%s"}`,
		"no page size":       `{"q":"alpha","cursor":"%s"}`,
		"another safesearch": `{"q":"alpha","count":2,"safesearch":"off","cursor":"%s"}`,
		"another freshness":  `{"q":"alpha","count":2,"freshness":"pd","cursor":"%s"}`,
		"cut":                `{"q":"alpha","count":2,"cursor":"%.10s"}`,
		"altered":            `{"q":"alpha","count":2,"cursor":"%sA"}`,
	} {
		before := len(f.recorded())
		_, err := env.invoke("brave.videos.search", "search", fmt.Sprintf(arguments, first.NextCursor))
		if !isInvalidRequest(err) || len(f.recorded()) != before {
			t.Errorf("%s: err = %v, want a refused cursor before any request", name, err)
		}
	}
}

func TestVideosPagingEnds(t *testing.T) {
	env, _ := videosEnv(t)
	if last := videoPage(t, env, `{"q":"x","offset":9}`); last.HasMore || last.NextCursor != "" {
		t.Errorf("offset 9 = %+v, want the end", last)
	}
	cursor := ""
	for offset := 0; offset < 9; offset++ {
		arguments := `{"q":"x"}`
		if cursor != "" {
			arguments = `{"q":"x","cursor":"` + cursor + `"}`
		}
		got := videoPage(t, env, arguments)
		if !got.HasMore {
			t.Fatalf("page %d has no successor", offset)
		}
		cursor = got.NextCursor
	}
	if got := videoPage(t, env, `{"q":"x","cursor":"`+cursor+`"}`); got.HasMore {
		t.Errorf("the page at offset 9 still has a successor")
	}
	empty := newEnvironment(t, &fakeBrave{answer: reply(200, nil, `{"type":"videos","results":[]}`)})
	if got := videoPage(t, empty, `{"q":"x"}`); got.HasMore || len(got.Results) != 0 {
		t.Errorf("empty page = %+v", got)
	}
}

func TestVideosTextAndAddressesAreBounded(t *testing.T) {
	long := func(n int) string { return strings.Repeat("ä", n) }
	var tags, results []string
	for i := 0; i < 40; i++ {
		tags = append(tags, `"`+long(500)+`"`)
	}
	for i := 0; i < 80; i++ {
		results = append(results, `{"title":"t`+fmt.Sprint(i)+`"}`)
	}
	big := `{"type":"` + long(500) + `","results":[{"title":"` + long(900) + `","description":"` + long(5000) + `",` +
		`"url":"https://e.example/` + strings.Repeat("p", 3000) + `","fetched_content_timestamp":1.9,` +
		`"video":{"duration":"` + long(900) + `","views":1e300,"tags":[` + strings.Join(tags, ",") + `],` +
		`"author":{"name":"` + long(900) + `","url":"javascript:alert(1)","img":"` + userinfoURL("e.example", "/i.png") + `"}},` +
		`"meta_url":{"favicon":"data:image/png;base64,AAAA"},` +
		`"thumbnail":{"src":"//e.example/t.jpg","original":"https://e.example/t o.jpg"}},` +
		strings.Join(results, ",") + `]}`
	env := newEnvironment(t, &fakeBrave{answer: reply(200, nil, big)})
	result, err := env.invoke("brave.videos.search", "search", `{"q":"x"}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var got struct {
		Results []struct {
			Title, Description, URL string
			Timestamp               float64 `json:"fetched_content_timestamp"`
			Video                   struct {
				Duration string
				Views    *float64
				Tags     []string
				Author   struct{ Name, URL, Img string }
			}
			MetaURL   struct{ Favicon string } `json:"meta_url"`
			Thumbnail struct{ Src, Original string }
		}
	}
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	runes := func(s string) int { return len([]rune(s)) }
	first := got.Results[0]
	if len(got.Results) != 50 || runes(first.Title) != 301 || runes(first.Description) != 1501 ||
		runes(first.Video.Duration) != 201 || runes(first.Video.Author.Name) != 201 {
		t.Errorf("results = %d, lengths %d %d %d %d", len(got.Results), runes(first.Title), runes(first.Description),
			runes(first.Video.Duration), runes(first.Video.Author.Name))
	}
	if len(first.Video.Tags) != 20 || runes(first.Video.Tags[0]) != 201 || first.Video.Views != nil {
		t.Errorf("tags = %d, views = %v", len(first.Video.Tags), first.Video.Views)
	}
	if first.URL != "" || first.Video.Author.URL != "" || first.Video.Author.Img != "" || first.MetaURL.Favicon != "" ||
		first.Thumbnail.Src != "" || first.Thumbnail.Original != "" {
		t.Errorf("an unsafe address is shown: %+v", first)
	}
	if first.Timestamp != 1 {
		t.Errorf("timestamp = %v", first.Timestamp)
	}
}

func TestVideosAnswersOtherwiseSparseStillPass(t *testing.T) {
	for name, body := range map[string]string{
		"empty object": `{}`,
		"null members": `{"type":null,"query":null,"results":null,"extra":null}`,
		"null entry": `{"results":[{"title":null,"url":null,"video":{"tags":null,"author":null,` +
			`"requires_subscription":null,"views":null},"meta_url":null,"thumbnail":null}]}`,
	} {
		env := newEnvironment(t, &fakeBrave{answer: reply(200, nil, body)})
		if _, err := env.invoke("brave.videos.search", "search", `{"q":"x"}`); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestVideosErrorsKeepTheArgumentsOut(t *testing.T) {
	for _, status := range []int{400, 402, 403, 404, 422, 429, 500, 503} {
		env := newEnvironment(t, &fakeBrave{answer: reply(status, nil, errorBody(status))})
		_, err := env.invoke("brave.videos.search", "search", `{"q":"secret query text"}`)
		if err == nil || strings.Contains(err.Error(), "secret query text") || strings.Contains(err.Error(), detailCanary) ||
			strings.Contains(err.Error(), tokenValue) || strings.Contains(err.Error(), "?") {
			t.Errorf("status %d: err = %v", status, err)
		}
	}
	env := newEnvironment(t, &fakeBrave{answer: reply(200, nil, `not json`)})
	if _, err := env.invoke("brave.videos.search", "search", `{"q":"x"}`); err == nil {
		t.Errorf("an unreadable body passed")
	}
}
