package brave

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// imagesFixture carries every member the images reference documents, plus a member Brave may add later.
const imagesFixture = `{
  "type": "images",
  "query": {"original": "mountan", "altered": "mountain", "spellcheck_off": false, "show_strict_warning": true,
    "added_later": 1},
  "results": [
    {
      "type": "image_result",
      "title": "A mountain",
      "url": "https://pages.example.com/mountain",
      "source": "pages.example.com",
      "page_fetched": "2026-10-24T08:00:00Z",
      "thumbnail": {"src": "https://imgs.example.com/thumb.jpg", "width": 500, "height": 333},
      "properties": {"url": "https://pages.example.com/mountain.jpg",
        "placeholder": "https://imgs.example.com/placeholder.jpg", "width": 4000, "height": 2667},
      "meta_url": {"scheme": "https", "netloc": "pages.example.com", "hostname": "pages.example.com",
        "favicon": "https://imgs.example.com/fav.ico", "path": "› mountain"},
      "confidence": "high",
      "added_later": 1
    },
    {"type": "image_result", "title": "Sparse", "thumbnail": null, "properties": null}
  ],
  "extra": {"might_be_offensive": true}
}`

func imagesEnv(t *testing.T, body string) (*environment, *fakeBrave) {
	t.Helper()
	f := &fakeBrave{answer: reply(200, map[string]string{
		"X-RateLimit-Limit": "1, 15000", "X-RateLimit-Policy": "1;w=1, 15000;w=2592000",
		"X-RateLimit-Remaining": "1, 14523", "X-RateLimit-Reset": "1, 1234567",
	}, body)}
	return newEnvironment(t, f), f
}

func TestImagesSearchReturnsEveryDocumentedField(t *testing.T) {
	env, f := imagesEnv(t, imagesFixture)
	result, err := env.invoke("brave.images.search", "search", `{"q":"mountan"}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	if r := f.recorded()[0]; r.method != http.MethodGet || r.path != "/res/v1/images/search" ||
		r.rawQuery != "q=mountan" || r.header.Get("X-Subscription-Token") != tokenValue {
		t.Errorf("request = %+v", r)
	}
	wantQuery := map[string]any{"original": "mountan", "altered": "mountain", "spellcheck_off": false,
		"show_strict_warning": true}
	if got["type"] != "images" || !reflect.DeepEqual(got["query"], wantQuery) ||
		!reflect.DeepEqual(got["extra"], map[string]any{"might_be_offensive": true}) {
		t.Errorf("answer = %v", got)
	}
	if _, ok := got["has_more"]; ok {
		t.Errorf("images have no paging: %v", got)
	}
	results := got["results"].([]any)
	want := map[string]any{
		"type": "image_result", "title": "A mountain", "url": "https://pages.example.com/mountain",
		"source": "pages.example.com", "page_fetched": "2026-10-24T08:00:00Z",
		"thumbnail": map[string]any{"src": "https://imgs.example.com/thumb.jpg", "width": float64(500),
			"height": float64(333)},
		"properties": map[string]any{"url": "https://pages.example.com/mountain.jpg",
			"placeholder": "https://imgs.example.com/placeholder.jpg", "width": float64(4000), "height": float64(2667)},
		"meta_url": map[string]any{"scheme": "https", "netloc": "pages.example.com", "hostname": "pages.example.com",
			"favicon": "https://imgs.example.com/fav.ico", "path": "› mountain"},
		"confidence": "high",
	}
	if len(results) != 2 || !reflect.DeepEqual(results[0], want) {
		t.Errorf("results = %v\nwant %v", results, want)
	}
	if second := results[1].(map[string]any); len(second) != 2 {
		t.Errorf("sparse result = %v", second)
	}
	if windows := got["quota"].(map[string]any)["windows"].([]any); len(windows) != 2 {
		t.Errorf("quota = %v", got["quota"])
	}
}

func TestImagesSearchSendsEveryParameter(t *testing.T) {
	env, f := imagesEnv(t, imagesFixture)
	_, err := env.invoke("brave.images.search", "search", `{"q":" mountain  lake ","search_lang":"de","country":"DE",`+
		`"safesearch":"off","count":200,"spellcheck":false,"no_cache":true}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	r := f.recorded()[0]
	want := url.Values{"q": {"mountain  lake"}, "search_lang": {"de"}, "country": {"DE"}, "safesearch": {"off"},
		"count": {"200"}, "spellcheck": {"false"}}
	if !reflect.DeepEqual(r.query, want) || r.header.Get("Cache-Control") != "no-cache" {
		t.Errorf("query = %v, cache-control %q", r.query, r.header.Get("Cache-Control"))
	}
	if _, err := env.invoke("brave.images.search", "search", `{"q":"x","safesearch":"strict"}`); err != nil {
		t.Errorf("safesearch strict: %v", err)
	}
	if _, err := env.invoke("brave.images.search", "search", `{"q":"x"}`); err != nil {
		t.Fatal(err)
	}
	all := f.recorded()
	if last := all[len(all)-1]; !reflect.DeepEqual(last.query, url.Values{"q": {"x"}}) || last.header.Get("Cache-Control") != "" {
		t.Errorf("minimal request = %v", last)
	}
}

func TestImagesSearchRefusesInvalidArgumentsBeforeTheKey(t *testing.T) {
	words := strings.TrimSpace(strings.Repeat("word ", 51))
	for name, arguments := range map[string]string{
		"missing query":     `{}`,
		"blank query":       `{"q":"   "}`,
		"long query":        `{"q":"` + strings.Repeat("a", 401) + `"}`,
		"many words":        `{"q":"` + words + `"}`,
		"control character": `{"q":"a\u0007b"}`,
		"offset":            `{"q":"x","offset":1}`,
		"cursor":            `{"q":"x","cursor":"abcdefabcdefabcdefabcdef"}`,
		"ui lang":           `{"q":"x","ui_lang":"en-US"}`,
		"freshness":         `{"q":"x","freshness":"pd"}`,
		"unknown country":   `{"q":"x","country":"XX"}`,
		"unknown language":  `{"q":"x","search_lang":"xx"}`,
		"moderate":          `{"q":"x","safesearch":"moderate"}`,
		"count zero":        `{"q":"x","count":0}`,
		"count high":        `{"q":"x","count":201}`,
		"wrong type":        `{"q":"x","spellcheck":"yes"}`,
	} {
		env, f := imagesEnv(t, imagesFixture)
		_, err := env.invoke("brave.images.search", "search", arguments)
		if !isInvalidRequest(err) || *env.reads != 0 || len(f.recorded()) != 0 {
			t.Errorf("%s: err = %v, reads = %d, requests = %d, want invalid-request before the key",
				name, err, *env.reads, len(f.recorded()))
		}
	}
}

func TestImagesTextAndAddressesAreBounded(t *testing.T) {
	long := func(n int) string { return strings.Repeat("ä", n) }
	var results []string
	for i := 0; i < 260; i++ {
		results = append(results, `{"title":"t"}`)
	}
	big := `{"type":"` + long(500) + `","query":{"original":"` + long(3000) + `"},"results":[{"title":"` + long(900) +
		`","source":"` + long(900) + `","confidence":"` + long(900) + `","url":"https://e.example/` +
		strings.Repeat("p", 3000) + `","thumbnail":{"src":"//e.example/t.jpg","width":1.5},` +
		`"properties":{"url":"` + userinfoURL("e.example", "/i.jpg") + `","placeholder":"javascript:alert(1)","height":1e300},` +
		`"meta_url":{"favicon":"data:image/png;base64,AAAA"}},` + strings.Join(results, ",") + `]}`
	env, _ := imagesEnv(t, big)
	result, err := env.invoke("brave.images.search", "search", `{"q":"x","count":200}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var got struct {
		Type    string
		Query   map[string]string
		Results []struct {
			Title, Source, Confidence, URL string
			Thumbnail                      struct {
				Src   string
				Width float64
			}
			Properties struct {
				URL, Placeholder string
				Height           *float64
			}
			MetaURL struct{ Favicon string } `json:"meta_url"`
		}
	}
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	runes := func(s string) int { return len([]rune(s)) }
	first := got.Results[0]
	if len(got.Results) != 200 || runes(got.Type) != 201 || runes(got.Query["original"]) != 1501 ||
		runes(first.Title) != 301 || runes(first.Source) != 201 || runes(first.Confidence) != 201 {
		t.Errorf("results = %d, lengths %d %d %d %d %d", len(got.Results), runes(got.Type), runes(got.Query["original"]),
			runes(first.Title), runes(first.Source), runes(first.Confidence))
	}
	if first.URL != "" || first.Thumbnail.Src != "" || first.Properties.URL != "" || first.Properties.Placeholder != "" ||
		first.MetaURL.Favicon != "" {
		t.Errorf("an unsafe address is shown: %+v", first)
	}
	if first.Thumbnail.Width != 1 || first.Properties.Height != nil {
		t.Errorf("sizes = %v / %v", first.Thumbnail.Width, first.Properties.Height)
	}
}

func TestImagesAnswersOtherwiseSparseStillPass(t *testing.T) {
	for name, body := range map[string]string{
		"empty object": `{}`,
		"empty hits":   `{"type":"images","results":[]}`,
		"null members": `{"type":null,"query":null,"results":null,"extra":null}`,
		"null entry": `{"results":[{"title":null,"url":null,"thumbnail":null,"properties":null,"meta_url":null,` +
			`"confidence":null}]}`,
	} {
		env, _ := imagesEnv(t, body)
		result, err := env.invoke("brave.images.search", "search", `{"q":"x"}`)
		if err != nil {
			t.Errorf("%s: %v %s", name, err, result)
		}
	}
}

func TestImagesErrorsKeepTheArgumentsOut(t *testing.T) {
	for _, status := range []int{400, 402, 403, 404, 422, 429, 500, 503} {
		env := newEnvironment(t, &fakeBrave{answer: reply(status, nil, errorBody(status))})
		_, err := env.invoke("brave.images.search", "search", `{"q":"secret query text"}`)
		if err == nil || strings.Contains(err.Error(), "secret query text") || strings.Contains(err.Error(), detailCanary) ||
			strings.Contains(err.Error(), tokenValue) || strings.Contains(err.Error(), "?") {
			t.Errorf("status %d: err = %v", status, err)
		}
	}
	env := newEnvironment(t, &fakeBrave{answer: reply(200, nil, `not json`)})
	if _, err := env.invoke("brave.images.search", "search", `{"q":"x"}`); err == nil {
		t.Errorf("an unreadable body passed")
	}
}
