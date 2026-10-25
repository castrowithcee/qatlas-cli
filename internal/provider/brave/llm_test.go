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

// llmFixture carries every member the LLM Context reference documents, plus members Brave may add later.
const llmFixture = `{
  "grounding": {
    "generic": [
      {"url": "https://example.com/page", "title": "Page Title",
       "snippets": ["Relevant text chunk", "{\"table\":[[\"a\",\"b\"]]}"], "added_later": 1}
    ],
    "poi": {"name": "Business", "url": "https://business.example", "title": "Business site",
            "snippets": ["Opening hours"]},
    "map": [{"name": "Place", "url": "https://place.example", "title": "Place site", "snippets": ["Details"]}]
  },
  "sources": {
    "https://example.com/page": {
      "title": "Page Title", "hostname": "example.com",
      "age": ["Monday, January 15, 2024", "2024-01-15", "380 days ago", "2024-01-15T13:45:02Z"],
      "description": "About the page", "fetched_content_timestamp": 1761292800.0, "site_name": "Example",
      "favicon": "https://example.com/favicon.ico",
      "thumbnail": {"src": "https://imgs.example.com/t.jpg", "original": "https://example.com/t.jpg"}
    },
    "https://business.example": {"title": "Business", "hostname": "business.example", "age": []},
    "javascript:alert(1)": {"title": "not an address"}
  }
}`

func llmFake() *fakeBrave {
	return &fakeBrave{answer: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "50, 100000")
		w.Header().Set("X-RateLimit-Remaining", "49, 99999")
		w.Header().Set("X-RateLimit-Reset", "1, 2000000")
		w.Header().Set("X-RateLimit-Policy", "50;w=1, 100000;w=2592000")
		w.Write([]byte(llmFixture))
	}}
}

func TestLLMContextReturnsEveryDocumentedField(t *testing.T) {
	f := llmFake()
	env := newEnvironment(t, f)
	result, err := env.invoke("brave.llm-context.get", "search", `{"q":"mediterranean"}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("result %s: %v", result, err)
	}
	grounding := got["grounding"].(map[string]any)
	wantGeneric := []any{map[string]any{"url": "https://example.com/page", "title": "Page Title",
		"snippets": []any{"Relevant text chunk", `{"table":[["a","b"]]}`}}}
	if !reflect.DeepEqual(grounding["generic"], wantGeneric) {
		t.Errorf("generic = %v", grounding["generic"])
	}
	wantPOI := map[string]any{"name": "Business", "url": "https://business.example", "title": "Business site",
		"snippets": []any{"Opening hours"}}
	if !reflect.DeepEqual(grounding["poi"], wantPOI) {
		t.Errorf("poi = %v", grounding["poi"])
	}
	wantMap := []any{map[string]any{"name": "Place", "url": "https://place.example", "title": "Place site",
		"snippets": []any{"Details"}}}
	if !reflect.DeepEqual(grounding["map"], wantMap) {
		t.Errorf("map = %v", grounding["map"])
	}
	sources := got["sources"].(map[string]any)
	if len(sources) != 2 {
		t.Errorf("sources = %v, want the two with an address key", sources)
	}
	wantSource := map[string]any{"title": "Page Title", "hostname": "example.com",
		"age":         []any{"Monday, January 15, 2024", "2024-01-15", "380 days ago", "2024-01-15T13:45:02Z"},
		"description": "About the page", "fetched_content_timestamp": float64(1761292800), "site_name": "Example",
		"favicon":   "https://example.com/favicon.ico",
		"thumbnail": map[string]any{"src": "https://imgs.example.com/t.jpg", "original": "https://example.com/t.jpg"}}
	if !reflect.DeepEqual(sources["https://example.com/page"], wantSource) {
		t.Errorf("source = %v", sources["https://example.com/page"])
	}
	if !reflect.DeepEqual(sources["https://business.example"],
		map[string]any{"title": "Business", "hostname": "business.example"}) {
		t.Errorf("sparse source = %v", sources["https://business.example"])
	}
	windows := got["quota"].(map[string]any)["windows"].([]any)
	if len(windows) != 2 || windows[1].(map[string]any)["remaining"] != float64(99999) {
		t.Errorf("quota = %v", got["quota"])
	}
	if _, paged := got["has_more"]; paged {
		t.Errorf("LLM Context has no pages: %v", got)
	}
	r := f.recorded()[0]
	if r.method != http.MethodGet || r.path != "/res/v1/llm/context" || !reflect.DeepEqual(r.query, url.Values{"q": {"mediterranean"}}) {
		t.Errorf("request = %+v, want a GET with the query only", r)
	}
	if r.header.Get("X-Subscription-Token") != tokenValue || r.header.Get("Api-Version") != "" {
		t.Errorf("headers = %v", r.header)
	}
}

func TestLLMContextSendsEveryParameter(t *testing.T) {
	f := llmFake()
	env := newEnvironment(t, f)
	_, err := env.invoke("brave.llm-context.get", "search", `{"q":" how deep ","country":"DE","search_lang":"de",`+
		`"safesearch":"moderate","count":50,"maximum_number_of_urls":7,"maximum_number_of_tokens":2048,`+
		`"maximum_number_of_snippets":256,"maximum_number_of_tokens_per_url":512,`+
		`"maximum_number_of_snippets_per_url":100,"context_threshold_mode":"balanced","spellcheck":false,`+
		`"operators":false,"operators_fallback":false,"freshness":"pw","enable_local":true,`+
		`"enable_source_metadata":true,"goggles":["https://raw.githubusercontent.com/o/r/main/a.goggle","$discard","$boost=2"],`+
		`"latitude":48.137,"longitude":-11.5,"city":"München","state":"BY","state_name":"Bayern",`+
		`"country_code":"DE","postal_code":"80331","no_cache":true}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	r := f.recorded()[0]
	want := url.Values{"q": {"how deep"}, "country": {"DE"}, "search_lang": {"de"}, "safesearch": {"moderate"},
		"count": {"50"}, "maximum_number_of_urls": {"7"}, "maximum_number_of_tokens": {"2048"},
		"maximum_number_of_snippets": {"256"}, "maximum_number_of_tokens_per_url": {"512"},
		"maximum_number_of_snippets_per_url": {"100"}, "context_threshold_mode": {"balanced"},
		"spellcheck": {"false"}, "operators": {"false"}, "operators_fallback": {"false"}, "freshness": {"pw"},
		"enable_local": {"true"}, "enable_source_metadata": {"true"},
		"goggles": {"https://raw.githubusercontent.com/o/r/main/a.goggle", "$discard", "$boost=2"}}
	if !reflect.DeepEqual(r.query, want) {
		t.Errorf("query = %v\nwant %v", r.query, want)
	}
	for header, value := range map[string]string{"X-Loc-Lat": "48.137", "X-Loc-Long": "-11.5", "X-Loc-City": "München",
		"X-Loc-State": "BY", "X-Loc-State-Name": "Bayern", "X-Loc-Country": "DE", "X-Loc-Postal-Code": "80331",
		"Cache-Control": "no-cache"} {
		if got := r.header.Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}
	if strings.Contains(r.rawQuery, "48.137") || strings.Contains(r.rawQuery, tokenValue) {
		t.Errorf("the location or the key reached the query: %s", r.rawQuery)
	}

	// Omitted arguments send nothing, and enable_local=false is sent.
	if _, err := env.invoke("brave.llm-context.get", "search", `{"q":"x","enable_local":false}`); err != nil {
		t.Fatal(err)
	}
	r = f.recorded()[1]
	if !reflect.DeepEqual(r.query, url.Values{"q": {"x"}, "enable_local": {"false"}}) ||
		r.header.Get("X-Loc-Lat") != "" || r.header.Get("Cache-Control") != "" {
		t.Errorf("minimal request = %v / %v", r.query, r.header)
	}
}

func TestLLMContextRefusesInvalidArgumentsBeforeTheKey(t *testing.T) {
	words := strings.TrimSpace(strings.Repeat("word ", 76))
	for name, arguments := range map[string]string{
		"missing query":      `{}`,
		"blank query":        `{"q":" "}`,
		"long query":         `{"q":"` + strings.Repeat("a", 601) + `"}`,
		"many words":         `{"q":"` + words + `"}`,
		"control character":  `{"q":"a\u0007b"}`,
		"unknown argument":   `{"q":"x","url":"https://x.example"}`,
		"page argument":      `{"q":"x","offset":1}`,
		"unknown country":    `{"q":"x","country":"XX"}`,
		"unknown language":   `{"q":"x","search_lang":"xx"}`,
		"ui language":        `{"q":"x","ui_lang":"en-US"}`,
		"safesearch":         `{"q":"x","safesearch":"none"}`,
		"freshness":          `{"q":"x","freshness":"2026-03-02to2026-03-01"}`,
		"count zero":         `{"q":"x","count":0}`,
		"count high":         `{"q":"x","count":51}`,
		"urls zero":          `{"q":"x","maximum_number_of_urls":0}`,
		"urls high":          `{"q":"x","maximum_number_of_urls":51}`,
		"tokens low":         `{"q":"x","maximum_number_of_tokens":1023}`,
		"tokens high":        `{"q":"x","maximum_number_of_tokens":32769}`,
		"snippets zero":      `{"q":"x","maximum_number_of_snippets":0}`,
		"snippets high":      `{"q":"x","maximum_number_of_snippets":257}`,
		"url tokens low":     `{"q":"x","maximum_number_of_tokens_per_url":511}`,
		"url tokens high":    `{"q":"x","maximum_number_of_tokens_per_url":8193}`,
		"url snippets zero":  `{"q":"x","maximum_number_of_snippets_per_url":0}`,
		"url snippets high":  `{"q":"x","maximum_number_of_snippets_per_url":101}`,
		"threshold mode":     `{"q":"x","context_threshold_mode":"loose"}`,
		"local as text":      `{"q":"x","enable_local":"auto"}`,
		"four goggles":       `{"q":"x","goggles":["a","b","c","d"]}`,
		"http goggles":       `{"q":"x","goggles":["http://example.com/a.goggle"]}`,
		"userinfo goggles":   `{"q":"x","goggles":["https://user:pw@example.com/a.goggle"]}`,
		"latitude alone":     `{"q":"x","latitude":10}`,
		"latitude high":      `{"q":"x","latitude":91,"longitude":0}`,
		"longitude high":     `{"q":"x","latitude":0,"longitude":181}`,
		"country code lower": `{"q":"x","country_code":"de"}`,
		"padded city":        `{"q":"x","city":" Berlin"}`,
		"control in city":    `{"q":"x","city":"Ber\nlin"}`,
		"long postal code":   `{"q":"x","postal_code":"` + strings.Repeat("1", 21) + `"}`,
	} {
		f := llmFake()
		env := newEnvironment(t, f)
		_, err := env.invoke("brave.llm-context.get", "search", arguments)
		if !isInvalidRequest(err) || *env.reads != 0 || len(f.recorded()) != 0 {
			t.Errorf("%s: err = %v, reads = %d, requests = %d, want invalid-request before the key",
				name, err, *env.reads, len(f.recorded()))
			continue
		}
		if strings.Contains(err.Error(), "word word") || strings.Contains(err.Error(), "example.com") {
			t.Errorf("%s: error %q quotes the value", name, err)
		}
	}
	// The bounds themselves pass.
	f := llmFake()
	env := newEnvironment(t, f)
	if _, err := env.invoke("brave.llm-context.get", "search", `{"q":"`+strings.Repeat("a", 600)+`","count":1,`+
		`"maximum_number_of_urls":50,"maximum_number_of_tokens":1024,"maximum_number_of_snippets":1,`+
		`"maximum_number_of_tokens_per_url":8192,"maximum_number_of_snippets_per_url":1}`); err != nil {
		t.Errorf("bounds refused: %v", err)
	}
	if _, err := env.invoke("brave.llm-context.get", "search", `{"q":"`+strings.TrimSpace(strings.Repeat("w ", 75))+`"}`); err != nil {
		t.Errorf("75 words refused: %v", err)
	}
}

func TestLLMContextTextAndListsAreBounded(t *testing.T) {
	long := func(n int) string { return strings.Repeat("ä", n) }
	var snippets, items []string
	for i := 0; i < 120; i++ {
		snippets = append(snippets, `"s"`)
	}
	snippets[0] = `"` + long(9000) + `"`
	for i := 0; i < 70; i++ {
		items = append(items, `{"url":"https://e.example/`+string(rune('a'+i%26))+`","title":"t"}`)
	}
	var sources []string
	for i := 0; i < 130; i++ {
		sources = append(sources, fmt.Sprintf(`"https://s.example/%d":{"title":"t"}`, i))
	}
	body := `{"grounding":{"generic":[{"url":"https://e.example/` + strings.Repeat("p", 3000) + `","title":"` + long(900) +
		`","snippets":[` + strings.Join(snippets, ",") + `]},` + strings.Join(items, ",") + `],` +
		`"poi":{"name":"` + long(900) + `","url":"ftp://x.example/"},"map":[{"url":"` + userinfoURL("x.example", "/") + `"}]},` +
		`"sources":{"https://e.example/a":{"title":"` + long(900) + `","hostname":"` + long(900) + `",` +
		`"age":["1","2","3","4","5"],"description":"` + long(5000) + `","site_name":"` + long(900) + `",` +
		`"favicon":"//x.example/f.ico","thumbnail":"javascript:alert(1)","fetched_content_timestamp":1.9},` +
		`"https://e.example/b":{"thumbnail":"https://x.example/t.png"},` +
		`"https://e.example/c":{"thumbnail":{"src":"data:image/png;base64,AA","original":"https://x.example/o.png"}},` +
		`"https://e.example/d":{"thumbnail":{"src":"data:image/png;base64,AA"}},` +
		`"https://e.example/e":{"thumbnail":7},` +
		strings.Join(sources, ",") + `}}`
	env := newEnvironment(t, &fakeBrave{answer: reply(200, nil, body)})
	result, err := env.invoke("brave.llm-context.get", "search", `{"q":"x"}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	var got struct {
		Grounding struct {
			Generic []llmItem
			POI     llmItem
			Map     []llmItem
		}
		Sources map[string]map[string]any
	}
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatal(err)
	}
	runes := func(s string) int { return len([]rune(s)) }
	first := got.Grounding.Generic[0]
	if len(got.Grounding.Generic) != 50 || len(first.Snippets) != 100 || runes(first.Snippets[0]) != 8001 ||
		runes(first.Title) != 301 || first.URL != "" {
		t.Errorf("generic: %d items, %d snippets, lengths %d/%d, url %q", len(got.Grounding.Generic),
			len(first.Snippets), runes(first.Snippets[0]), runes(first.Title), first.URL)
	}
	if runes(got.Grounding.POI.Name) != 301 || got.Grounding.POI.URL != "" || got.Grounding.Map[0].URL != "" {
		t.Errorf("poi/map = %+v / %+v", got.Grounding.POI, got.Grounding.Map)
	}
	// The first 100 keys are kept; the two without any safe member are left out by the empty-field rule.
	if len(got.Sources) != 98 {
		t.Errorf("%d sources, want 98", len(got.Sources))
	}
	a := got.Sources["https://e.example/a"]
	if runes(a["title"].(string)) != 301 || runes(a["hostname"].(string)) != 201 ||
		runes(a["description"].(string)) != 1501 || runes(a["site_name"].(string)) != 201 ||
		len(a["age"].([]any)) != 4 || a["fetched_content_timestamp"] != float64(1) {
		t.Errorf("source a = %v", a)
	}
	for _, unsafe := range []string{"favicon", "thumbnail"} {
		if _, ok := a[unsafe]; ok {
			t.Errorf("source a keeps an unsafe %s: %v", unsafe, a[unsafe])
		}
	}
	if b, ok := got.Sources["https://e.example/b"]; ok && b["thumbnail"] != "https://x.example/t.png" {
		t.Errorf("source b = %v", b)
	}
	if c, ok := got.Sources["https://e.example/c"]; ok &&
		!reflect.DeepEqual(c["thumbnail"], map[string]any{"original": "https://x.example/o.png"}) {
		t.Errorf("source c = %v", c)
	}
	for _, key := range []string{"d", "e"} {
		if s, ok := got.Sources["https://e.example/"+key]; ok && s["thumbnail"] != nil {
			t.Errorf("source %s keeps %v", key, s["thumbnail"])
		}
	}
}

func TestLLMContextSparseAnswersPass(t *testing.T) {
	for name, body := range map[string]string{
		"empty object": `{}`,
		"null members": `{"grounding":null,"sources":null}`,
		"null poi":     `{"grounding":{"generic":null,"poi":null,"map":null}}`,
		"null item":    `{"grounding":{"generic":[{"url":null,"snippets":null}]},"sources":{"https://a.example":{"age":null,"thumbnail":null}}}`,
	} {
		env := newEnvironment(t, &fakeBrave{answer: reply(200, nil, body)})
		if _, err := env.invoke("brave.llm-context.get", "search", `{"q":"x"}`); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestLLMContextErrorsMapToClasses(t *testing.T) {
	for status, class := range map[int]provider.Class{
		http.StatusBadRequest: provider.ClassInvalidResponse, http.StatusForbidden: provider.ClassPermission,
		http.StatusNotFound: provider.ClassNotFound, http.StatusUnprocessableEntity: provider.ClassInvalidResponse,
		http.StatusTooManyRequests: provider.ClassRateLimited, http.StatusInternalServerError: provider.ClassProviderError,
	} {
		env := newEnvironment(t, &fakeBrave{answer: reply(status, nil, errorBody(status))})
		_, err := env.invoke("brave.llm-context.get", "search", `{"q":"secret query text","latitude":1,"longitude":2}`)
		if classOf(err) != class {
			t.Errorf("HTTP %d: class = %q (%v), want %q", status, classOf(err), err, class)
			continue
		}
		for _, leak := range []string{detailCanary, tokenValue, "secret query text", "api.search.brave.com"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("HTTP %d: error %q leaks %q", status, err, leak)
			}
		}
	}
}

func TestLLMContextQuotaMessageOnExhaustedMonth(t *testing.T) {
	headers := map[string]string{"X-RateLimit-Limit": "50, 100", "X-RateLimit-Policy": "50;w=1, 100;w=2592000",
		"X-RateLimit-Remaining": "49, 0", "X-RateLimit-Reset": "1, 3600"}
	env := newEnvironment(t, &fakeBrave{answer: reply(429, headers, errorBody(429))})
	_, err := env.invoke("brave.llm-context.get", "search", `{"q":"x"}`)
	if classOf(err) != provider.ClassRateLimited || !strings.Contains(err.Error(), "quota") {
		t.Errorf("err = %v, want rate-limited with the quota message", err)
	}
}
