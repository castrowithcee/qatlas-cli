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

// webFixture carries several kinds of result in one answer, in the shapes of Brave's reference, plus the
// deprecated restaurant member and a member Brave may add later.
const webFixture = `{
  "type": "search",
  "query": {"original": "pizza münchen", "altered": "pizza munich", "cleaned": "pizza munich", "safesearch": true,
    "is_navigational": false, "is_geolocal": true, "local_decision": "drop", "local_locations_idx": 0,
    "is_trending": false, "is_news_breaking": false, "ask_for_location": false, "language": {"main": "de"},
    "spellcheck_off": false, "country": "de", "bad_results": false, "should_fallback": false, "lat": "48.1",
    "long": "11.5", "postal_code": "80331", "city": "Munich", "header_country": "DE",
    "more_results_available": true, "state": "BY", "custom_location_label": "Munich", "reddit_cluster": "r1",
    "summary_key": "sum-key-1", "show_strict_warning": false,
    "search_operators": {"applied": true, "cleaned_query": "pizza", "sites": ["example.com"]},
    "related_queries": ["pizza rezept", "pizza teig"], "added_later": 1},
  "mixed": {"type": "mixed", "main": [{"type": "web", "index": 0, "all": false}, {"type": "news", "all": true}],
    "top": [{"type": "infobox", "index": 0}], "side": [{"type": "videos", "all": true}]},
  "web": {"type": "search", "family_friendly": true, "results": [{
    "title": "Pizza", "url": "https://pizza.example/", "is_source_local": false, "is_source_both": false,
    "description": "Best pizza", "page_age": "2026-10-01T00:00:00", "page_fetched": "2026-10-20T00:00:00Z",
    "fetched_content_timestamp": 1760918400, "profile": {"name": "Pizza", "url": "https://pizza.example",
      "long_name": "Pizza Example", "img": "https://img.example/p.png"},
    "language": "de", "family_friendly": true, "type": "search_result", "subtype": "generic", "is_live": false,
    "meta_url": {"scheme": "https", "netloc": "pizza.example", "hostname": "pizza.example", "path": "› pizza"},
    "thumbnail": {"src": "https://img.example/t.jpg", "original": "https://pizza.example/t.jpg", "height": 100,
      "width": 200, "logo": false, "theme": "light"},
    "age": "2 days ago", "content_type": "text/html", "extra_snippets": ["more one", "more two"],
    "icons": [{"href": "https://pizza.example/i.png", "sizes": "32x32", "rel": "icon", "type": "image/png", "ext": "png"}],
    "deep_results": {"buttons": [{"type": "button_result", "title": "Menu", "url": "https://pizza.example/menu"}],
      "news": [{"title": "Pizza news", "url": "https://n.example/1", "breaking": true, "source": "N",
        "meta_url": {"hostname": "n.example"}}],
      "videos": [{"type": "video_result", "title": "Pizza video", "url": "https://v.example/1",
        "video": {"duration": "03:10", "views": "1000", "author": {"name": "A", "url": "https://v.example/a"}},
        "meta_url": {"hostname": "v.example", "favicon": "https://v.example/f.ico"}}],
      "images": [{"url": "https://i.example/1", "thumbnail": {"src": "https://i.example/t.jpg"},
        "properties": {"url": "https://i.example/o.jpg", "placeholder": "https://i.example/p.jpg", "width": 10}}]},
    "schemas": [{"@type": "Organization", "name": "Pizza"}, "free text"],
    "location": {"title": "Pizza Place", "url": "https://pizza.example/l", "id": "loc-1", "type": "location_result",
      "provider_url": "https://pizza.example/p", "coordinates": [48.1, 11.5], "zoom_level": 12,
      "postal_address": {"type": "PostalAddress", "country": "DE", "postalCode": "80331",
        "streetAddress": "Marienplatz 1", "addressLocality": "Munich", "displayAddress": "Marienplatz 1, Munich"},
      "opening_hours": {"current_day": [{"abbr_name": "Mon", "full_name": "Monday", "opens": "10:00", "closes": "22:00"}],
        "days": [[{"abbr_name": "Mon"}]]},
      "contact": {"telephone": "+49 1", "email": "contact-1"}, "price_range": "$$",
      "rating": {"ratingValue": 4.5, "bestRating": 5, "reviewCount": 120, "is_tripadvisor": false},
      "distance": {"value": 1.5, "units": "km"}, "categories": ["pizza"], "serves_cuisine": ["italian"],
      "reviews": {"results": [{"title": "Great", "description": "Nice", "date": "2026-01-01",
        "rating": {"ratingValue": 5, "bestRating": 5}, "author": {"name": "R"}, "review_url": "https://r.example/1",
        "language": "en"}], "viewMoreUrl": "https://r.example/all", "reviews_in_foreign_language": false},
      "pictures": {"viewMoreUrl": "https://p.example/all", "results": [{"src": "https://p.example/1.jpg"}]},
      "action": {"type": "reserve", "url": "https://pizza.example/reserve"}, "timezone": "Europe/Berlin",
      "timezone_offset": 3600, "icon_category": "restaurant",
      "results": [{"title": "Mention", "url": "https://m.example/", "meta_url": {"hostname": "m.example"}}]},
    "restaurant": {"title": "Deprecated", "url": "https://old.example/"},
    "video": {"duration": "01:00", "views": "5", "creator": "C", "requires_subscription": false, "tags": ["a"]},
    "movie": {"name": "Film", "release": "2020", "directors": [{"name": "D"}], "actors": [{"name": "A1"}],
      "rating": {"ratingValue": 8, "bestRating": 10}, "genre": ["drama"], "duration": "1h"},
    "faq": {"items": [{"question": "Q?", "answer": "A.", "title": "T", "url": "https://f.example/",
      "meta_url": {"scheme": "https", "netloc": "f.example", "favicon": "https://f.example/f.ico", "path": "p"}}]},
    "qa": {"question": "Why?", "answer": {"text": "Because", "author": "x", "upvoteCount": 3, "downvoteCount": 1}},
    "book": {"title": "Book", "author": [{"name": "Au"}], "price": {"price": "9", "priceCurrency": "EUR"},
      "pages": 100, "publisher": {"name": "Pub"}},
    "rating": {"ratingValue": 3, "bestRating": 5},
    "article": {"author": [{"name": "Writer"}], "date": "2026-01-01",
      "publisher": {"name": "Paper", "contact_points": [{"name": "Desk", "telephone": "1"}]},
      "isAccessibleForFree": true},
    "product": {"name": "Pan", "price": "20", "offers": [{"url": "https://s.example/pan", "price": "20"}]},
    "product_cluster": [{"name": "Pan 2", "price": "30", "gtin13": "123"}], "cluster_type": "news",
    "cluster": [{"title": "Related", "url": "https://c.example/"}],
    "creative_work": {"name": "Work"}, "music_recording": {"name": "Song"},
    "review": {"name": "Rev", "description": "d", "rating": {"ratingValue": 1, "bestRating": 5}},
    "recipe": {"title": "Dough", "url": "https://r.example/d", "servings": 4, "calories": 300,
      "instructions": [{"text": "Mix", "image": ["https://r.example/1.jpg"]}]},
    "software": {"name": "pkg", "stars": 10, "is_npm": true},
    "organization": {"name": "Org", "contact_points": [{"name": "CP"}]},
    "added_later": {"x": 1}
  }, {"title": "Sparse", "url": "https://sparse.example/", "description": null, "thumbnail": null}]},
  "discussions": {"type": "search", "mutated_by_goggles": false, "results": [{"title": "Thread",
    "url": "https://forum.example/t", "type": "discussion", "data": {"forum_name": "r/pizza", "num_answers": 4,
    "score": "10 points", "title": "Thread", "question": "Which?", "top_comment": "This"}}]},
  "infobox": {"type": "graph", "results": [{"title": "Pizza", "url": "https://wiki.example/pizza",
    "type": "infobox", "position": 1, "label": "Food", "category": "dish", "long_desc": "Long",
    "attributes": [["Origin", "Italy"], ["Type", "Dish"]], "website_url": "https://pizza.example",
    "ratings": [{"ratingValue": 4, "bestRating": 5}], "images": [{"src": "https://wiki.example/i.jpg"}],
    "found_in_urls": ["https://wiki.example/pizza"], "profiles": [{"name": "Wiki"}]}]},
  "locations": {"type": "locations", "results": [{"title": "Pizza Place", "id": "loc-1", "coordinates": [48.1, 11.5]}],
    "provider": {"name": "x"}},
  "news": {"type": "news", "mutated_by_goggles": true, "results": [{"title": "Pizza news", "url": "https://n.example/1",
    "age": "1 hour ago", "breaking": false, "meta_url": {"hostname": "n.example"},
    "icons": [{"href": "https://n.example/i.png"}]}]},
  "videos": {"type": "videos", "mutated_by_goggles": false, "results": [{"type": "video_result",
    "title": "Pizza video", "url": "https://v.example/1", "video": {"duration": "03:10", "views": 1000,
    "author": {"name": "A"}}, "meta_url": {"hostname": "v.example"}, "thumbnail": {"src": "https://v.example/t.jpg",
    "original": "https://v.example/o.jpg"}}]},
  "faq": {"type": "faq", "results": [{"question": "Q?", "answer": "A.", "title": "T", "url": "https://f.example/"}]},
  "summarizer": {"type": "summarizer", "key": "sum-key-1"},
  "rich": {"type": "rich", "hint": {"vertical": "weather", "callback_key": "cb-key-1"}}
}`

// webEnvironment answers with webFixture unless the test chooses another answer.
func webEnvironment(t *testing.T, body string) *environment {
	t.Helper()
	if body == "" {
		body = webFixture
	}
	return newEnvironment(t, &fakeBrave{answer: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "1, 15000")
		w.Header().Set("X-RateLimit-Policy", "1;w=1, 15000;w=2592000")
		w.Header().Set("X-RateLimit-Remaining", "1, 14523")
		w.Header().Set("X-RateLimit-Reset", "1, 1234567")
		fmt.Fprint(w, body)
	}})
}

func (e *environment) web(t *testing.T, arguments string) map[string]any {
	t.Helper()
	result, err := e.invoke("brave.web.search", "search", arguments)
	if err != nil {
		t.Fatalf("invoke %s = %v", arguments, err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("result %s: %v", result, err)
	}
	return got
}

// pick follows a path of member names and list positions through a decoded result.
func pick(t *testing.T, value any, path ...any) any {
	t.Helper()
	for _, step := range path {
		switch step := step.(type) {
		case string:
			object, ok := value.(map[string]any)
			if !ok {
				t.Fatalf("%v: not an object at %q", path, step)
			}
			value = object[step]
		case int:
			items, ok := value.([]any)
			if !ok || step >= len(items) {
				t.Fatalf("%v: no entry %d", path, step)
			}
			value = items[step]
		}
	}
	return value
}

func TestWebSearchGroupsEveryKindOfResult(t *testing.T) {
	got := webEnvironment(t, "").web(t, `{"q":"pizza münchen"}`)
	for _, group := range []string{"web", "discussions", "infobox", "locations", "news", "videos", "faq", "mixed",
		"summarizer", "rich", "query"} {
		if got[group] == nil {
			t.Errorf("group %s is missing", group)
		}
	}
	query := got["query"].(map[string]any)
	if _, ok := query["added_later"]; ok || query["altered"] != "pizza munich" || query["more_results_available"] != true ||
		query["summary_key"] != "sum-key-1" || pick(t, query, "language", "main") != "de" ||
		!reflect.DeepEqual(query["related_queries"], []any{"pizza rezept", "pizza teig"}) ||
		pick(t, query, "search_operators", "sites", 0) != "example.com" {
		t.Errorf("query = %v", query)
	}
	if got["type"] != "search" || pick(t, got, "summarizer", "key") != "sum-key-1" ||
		pick(t, got, "rich", "hint", "callback_key") != "cb-key-1" || pick(t, got, "rich", "hint", "vertical") != "weather" {
		t.Errorf("type, summarizer, or rich: %v / %v / %v", got["type"], got["summarizer"], got["rich"])
	}

	first := pick(t, got, "web", "results", 0).(map[string]any)
	if _, ok := first["restaurant"]; ok {
		t.Errorf("the deprecated restaurant is shown")
	}
	if _, ok := first["added_later"]; ok {
		t.Errorf("an unknown member is shown")
	}
	for _, name := range []string{"deep_results", "schemas", "location", "video", "movie", "faq", "qa", "book", "rating",
		"article", "product", "product_cluster", "cluster", "creative_work", "music_recording", "review", "recipe",
		"software", "organization", "icons", "extra_snippets", "thumbnail", "profile", "meta_url"} {
		if first[name] == nil {
			t.Errorf("web result lacks %s", name)
		}
	}
	if pick(t, first, "location", "id") != "loc-1" || pick(t, first, "location", "results", 0, "title") != "Mention" ||
		pick(t, first, "location", "postal_address", "displayAddress") != "Marienplatz 1, Munich" ||
		pick(t, first, "deep_results", "videos", 0, "video", "author", "name") != "A" ||
		pick(t, first, "deep_results", "images", 0, "properties", "placeholder") != "https://i.example/p.jpg" ||
		pick(t, first, "faq", "items", 0, "question") != "Q?" || pick(t, first, "qa", "answer", "upvoteCount") != float64(3) ||
		pick(t, first, "recipe", "instructions", 0, "image", 0) != "https://r.example/1.jpg" ||
		pick(t, first, "article", "publisher", "contact_points", 0, "telephone") != "1" ||
		pick(t, first, "product", "offers", 0, "price") != "20" ||
		pick(t, first, "location", "rating", "ratingValue") != 4.5 {
		t.Errorf("nested members of the first web result are wrong: %v", first)
	}
	// Members whose type Brave leaves open are shown as they are.
	if !reflect.DeepEqual(pick(t, first, "schemas", 0), map[string]any{"@type": "Organization", "name": "Pizza"}) ||
		pick(t, first, "schemas", 1) != "free text" ||
		!reflect.DeepEqual(pick(t, first, "location", "coordinates"), []any{48.1, 11.5}) ||
		!reflect.DeepEqual(pick(t, first, "meta_url"), map[string]any{"scheme": "https", "netloc": "pizza.example",
			"hostname": "pizza.example", "path": "› pizza"}) {
		t.Errorf("open members = %v / %v / %v", first["schemas"], pick(t, first, "location", "coordinates"), first["meta_url"])
	}
	sparse := pick(t, got, "web", "results", 1).(map[string]any)
	if len(sparse) != 2 || sparse["title"] != "Sparse" {
		t.Errorf("sparse result = %v", sparse)
	}
	if pick(t, got, "discussions", "results", 0, "data", "forum_name") != "r/pizza" ||
		pick(t, got, "discussions", "mutated_by_goggles") != false ||
		pick(t, got, "infobox", "results", 0, "attributes", 1, 0) != "Type" ||
		pick(t, got, "infobox", "results", 0, "profiles", 0, "name") != "Wiki" ||
		pick(t, got, "locations", "results", 0, "id") != "loc-1" || pick(t, got, "locations", "provider", "name") != "x" ||
		pick(t, got, "news", "results", 0, "breaking") != false || pick(t, got, "news", "mutated_by_goggles") != true ||
		pick(t, got, "videos", "results", 0, "video", "views") != float64(1000) ||
		pick(t, got, "faq", "results", 0, "answer") != "A." ||
		pick(t, got, "mixed", "main", 1, "all") != true || pick(t, got, "mixed", "top", 0, "type") != "infobox" ||
		pick(t, got, "mixed", "side", 0, "type") != "videos" {
		t.Errorf("other groups are wrong: %v", got)
	}
	if pick(t, got, "quota", "windows", 1, "remaining") != float64(14523) {
		t.Errorf("quota = %v", got["quota"])
	}
}

// sample builds a value that fills every member of a shape once.
func sample(s *shape) any {
	switch s.kind {
	case kInt:
		return float64(7)
	case kNum:
		return 1.5
	case kBool:
		return true
	case kAddr:
		return "https://e.example/x"
	case kObj:
		out := map[string]any{}
		for _, property := range s.props {
			out[property.name] = sample(property.shape)
		}
		return out
	case kArr:
		return []any{sample(s.elem)}
	case kJSON:
		return map[string]any{"k": []any{"v", 1.0}}
	}
	return "text"
}

// Every member of every shape passes the cleaning and the output schema, so no documented member is lost.
func TestWebSearchKeepsEveryMemberOfEveryShape(t *testing.T) {
	full := sample(webAnswerShape)
	cleaned, ok := webAnswerShape.clean(full)
	if !ok || !reflect.DeepEqual(cleaned, full) {
		t.Fatalf("cleaning changed a complete answer")
	}
	encoded, _ := json.Marshal(full)
	env := webEnvironment(t, string(encoded))
	got := env.web(t, `{"q":"x"}`)
	delete(got, "has_more")
	delete(got, "quota")
	delete(got, "next_cursor")
	if !reflect.DeepEqual(got, full) {
		t.Errorf("the tool result differs from a complete answer")
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(webOutputSchema), &schema); err != nil {
		t.Fatalf("output schema: %v", err)
	}
}

func TestWebSearchSendsEveryParameter(t *testing.T) {
	f := &fakeBrave{}
	f.answer = func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, webFixture) }
	env := newEnvironment(t, f)
	_, err := env.invoke("brave.web.search", "search", `{"q":" open  source ","country":"BR","search_lang":"pt-br",`+
		`"ui_lang":"pt-BR","count":15,"offset":3,"safesearch":"off","spellcheck":false,`+
		`"freshness":"2026-01-01to2026-02-28","text_decorations":true,"result_filter":["web","news"],`+
		`"units":"imperial","goggles":["https://raw.githubusercontent.com/o/r/main/a.goggle","$boost=3,site=dev.to\n$discard"],`+
		`"extra_snippets":true,"summary":true,"enable_rich_callback":true,"include_fetch_metadata":true,`+
		`"operators":false,"timezone":"America/Argentina/Salta","latitude":-24.78,"longitude":-65.41,`+
		`"city":"Salta","state":"A","state_name":"Salta Province","country_code":"AR","postal_code":"4400","no_cache":true}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	r := f.recorded()[0]
	want := url.Values{"q": {"open  source"}, "country": {"BR"}, "search_lang": {"pt-br"}, "ui_lang": {"pt-BR"},
		"count": {"15"}, "offset": {"3"}, "safesearch": {"off"}, "spellcheck": {"false"},
		"freshness": {"2026-01-01to2026-02-28"}, "text_decorations": {"true"}, "result_filter": {"web,news"},
		"units": {"imperial"}, "extra_snippets": {"true"}, "summary": {"1"}, "enable_rich_callback": {"true"},
		"include_fetch_metadata": {"true"}, "operators": {"false"},
		"goggles": {"https://raw.githubusercontent.com/o/r/main/a.goggle", "$boost=3,site=dev.to\n$discard"}}
	if !reflect.DeepEqual(r.query, want) {
		t.Errorf("query = %v\nwant %v", r.query, want)
	}
	if r.method != http.MethodGet || r.path != "/res/v1/web/search" || r.header.Get("X-Subscription-Token") != tokenValue ||
		strings.Contains(r.rawQuery, tokenValue) {
		t.Errorf("request = %+v", r)
	}
	for name, value := range map[string]string{"X-Loc-Lat": "-24.78", "X-Loc-Long": "-65.41", "X-Loc-Timezone": "America/Argentina/Salta",
		"X-Loc-City": "Salta", "X-Loc-State": "A", "X-Loc-State-Name": "Salta Province", "X-Loc-Country": "AR",
		"X-Loc-Postal-Code": "4400", "Cache-Control": "no-cache"} {
		if r.header.Get(name) != value {
			t.Errorf("header %s = %q, want %q", name, r.header.Get(name), value)
		}
	}

	// Without arguments beyond the query only the plain-text default is sent: the rest stays with Brave.
	if _, err := env.invoke("brave.web.search", "search", `{"q":"x"}`); err != nil {
		t.Fatal(err)
	}
	r = f.recorded()[1]
	if !reflect.DeepEqual(r.query, url.Values{"q": {"x"}, "text_decorations": {"false"}}) ||
		r.header.Get("Cache-Control") != "" || r.header.Get("X-Loc-Timezone") != "" || r.header.Get("X-Loc-Lat") != "" {
		t.Errorf("minimal request = %v, headers %v", r.query, r.header)
	}
	if _, err := env.invoke("brave.web.search", "search", `{"q":"x","summary":false,"offset":0,"no_cache":false,"text_decorations":false}`); err != nil {
		t.Fatal(err)
	}
	r = f.recorded()[2]
	if !reflect.DeepEqual(r.query, url.Values{"q": {"x"}, "text_decorations": {"false"}}) || r.header.Get("Cache-Control") != "" {
		t.Errorf("explicit defaults = %v", r.query)
	}
}

func TestWebSearchResultFilter(t *testing.T) {
	for filter, want := range map[string]string{
		`["web"]`:                               "web",
		`["news"]`:                              "news",
		`["query"]`:                             "query",
		`["locations","faq","infobox"]`:         "locations,faq,infobox",
		`["discussions","videos","summarizer"]`: "discussions,videos,summarizer",
		`["discussions","faq","infobox","news","query","summarizer","videos","web","locations"]`: "discussions,faq,infobox,news,query,summarizer,videos,web,locations",
	} {
		f := &fakeBrave{answer: func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"type":"search"}`) }}
		env := newEnvironment(t, f)
		if _, err := env.invoke("brave.web.search", "search", `{"q":"x","result_filter":`+filter+`}`); err != nil {
			t.Errorf("%s: %v", filter, err)
			continue
		}
		if got := f.recorded()[0].query.Get("result_filter"); got != want {
			t.Errorf("%s: result_filter = %q, want %q", filter, got, want)
		}
	}
}

func TestWebSearchRefusesInvalidArgumentsBeforeTheKey(t *testing.T) {
	for name, arguments := range map[string]string{
		"missing query":      `{}`,
		"blank query":        `{"q":"  "}`,
		"long query":         `{"q":"` + strings.Repeat("a", 601) + `"}`,
		"many words":         `{"q":"` + strings.TrimSpace(strings.Repeat("w ", 76)) + `"}`,
		"control character":  `{"q":"a\u0007b"}`,
		"unknown argument":   `{"q":"x","url":"https://x.example"}`,
		"goggles_id":         `{"q":"x","goggles_id":"abc"}`,
		"restaurant":         `{"q":"x","restaurant":true}`,
		"unknown country":    `{"q":"x","country":"XX"}`,
		"unknown language":   `{"q":"x","search_lang":"xx"}`,
		"unknown ui lang":    `{"q":"x","ui_lang":"de"}`,
		"safesearch":         `{"q":"x","safesearch":"none"}`,
		"units":              `{"q":"x","units":"nautical"}`,
		"freshness":          `{"q":"x","freshness":"pq"}`,
		"count zero":         `{"q":"x","count":0}`,
		"count high":         `{"q":"x","count":21}`,
		"offset high":        `{"q":"x","offset":10}`,
		"offset negative":    `{"q":"x","offset":-1}`,
		"offset and cursor":  `{"q":"x","offset":1,"cursor":"abcdefabcdefabcdefabcdef"}`,
		"foreign cursor":     `{"q":"x","cursor":"abcdefabcdefabcdefabcdef"}`,
		"filter unknown":     `{"q":"x","result_filter":["images"]}`,
		"filter empty":       `{"q":"x","result_filter":[]}`,
		"filter twice":       `{"q":"x","result_filter":["web","web"]}`,
		"filter text":        `{"q":"x","result_filter":"web,news"}`,
		"filter injection":   `{"q":"x","result_filter":["web&summary=1"]}`,
		"four goggles":       `{"q":"x","goggles":["a","b","c","d"]}`,
		"http goggles":       `{"q":"x","goggles":["http://example.com/a.goggle"]}`,
		"timezone path":      `{"q":"x","timezone":"../etc/passwd"}`,
		"timezone space":     `{"q":"x","timezone":"Europe/Ber lin"}`,
		"timezone long":      `{"q":"x","timezone":"` + strings.Repeat("a", 65) + `"}`,
		"timezone newline":   `{"q":"x","timezone":"UTC\r\nX-A: b"}`,
		"timezone empty":     `{"q":"x","timezone":""}`,
		"latitude alone":     `{"q":"x","latitude":10}`,
		"latitude range":     `{"q":"x","latitude":91,"longitude":0}`,
		"country code":       `{"q":"x","country_code":"de"}`,
		"city control":       `{"q":"x","city":"a\u0000b"}`,
		"wrong type":         `{"q":"x","summary":"yes"}`,
		"wrong rich type":    `{"q":"x","enable_rich_callback":1}`,
		"decorations string": `{"q":"x","text_decorations":"false"}`,
	} {
		f := &fakeBrave{}
		env := newEnvironment(t, f)
		_, err := env.invoke("brave.web.search", "search", arguments)
		if !isInvalidRequest(err) || *env.reads != 0 || len(f.recorded()) != 0 {
			t.Errorf("%s: err = %v, reads = %d, requests = %d, want invalid-request before the key",
				name, err, *env.reads, len(f.recorded()))
		}
	}
}

func TestWebSearchAcceptsTheEdgesOfEveryRange(t *testing.T) {
	for _, arguments := range []string{
		`{"q":"` + strings.Repeat("a", 600) + `"}`,
		`{"q":"` + strings.TrimSpace(strings.Repeat("w ", 75)) + `"}`,
		`{"q":"x","count":1,"offset":0}`, `{"q":"x","count":20,"offset":9}`,
		`{"q":"x","latitude":-90,"longitude":-180}`, `{"q":"x","latitude":90,"longitude":180}`,
		`{"q":"x","timezone":"UTC"}`, `{"q":"x","timezone":"Etc/GMT+5"}`,
	} {
		env := webEnvironment(t, `{"type":"search"}`)
		if _, err := env.invoke("brave.web.search", "search", arguments); err != nil {
			t.Errorf("%.60s: %v", arguments, err)
		}
	}
}

func TestWebSearchEndsWithMoreResultsAvailable(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		more bool
	}{
		"true":    {`{"type":"search","query":{"original":"x","more_results_available":true},"web":{"results":[]}}`, true},
		"false":   {`{"type":"search","query":{"original":"x","more_results_available":false},"web":{"results":[{"title":"t"}]}}`, false},
		"absent":  {`{"type":"search","query":{"original":"x"},"web":{"results":[{"title":"t"}]}}`, false},
		"no body": {`{}`, false},
		"text":    {`{"query":{"more_results_available":"true"}}`, false},
	} {
		env := webEnvironment(t, tc.body)
		got := env.web(t, `{"q":"x"}`)
		if got["has_more"] != tc.more || (got["next_cursor"] != nil) != tc.more {
			t.Errorf("%s: has_more = %v, next_cursor = %v, want more = %v", name, got["has_more"], got["next_cursor"], tc.more)
		}
	}
}

func TestWebSearchPagesUseABoundCursor(t *testing.T) {
	more := `{"type":"search","query":{"more_results_available":true}}`
	f := &fakeBrave{answer: func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, more) }}
	env := newEnvironment(t, f)
	// Offsets 1 to 9 are reached by cursor one after the other; the page at offset 9 ends the list however
	// much Brave still has.
	cursor := ""
	for offset := 0; offset <= 9; offset++ {
		arguments := `{"q":"alpha","count":5}`
		if cursor != "" {
			arguments = `{"q":"alpha","count":5,"cursor":"` + cursor + `"}`
		}
		got := env.web(t, arguments)
		sent := f.recorded()[len(f.recorded())-1].query.Get("offset")
		if want := map[bool]string{true: "", false: fmt.Sprint(offset)}[offset == 0]; sent != want {
			t.Errorf("page %d sent offset %q, want %q", offset, sent, want)
		}
		if offset == 9 {
			if got["has_more"] != false || got["next_cursor"] != nil {
				t.Errorf("offset 9 = %v / %v, want the end", got["has_more"], got["next_cursor"])
			}
			break
		}
		if got["has_more"] != true {
			t.Fatalf("page %d has no successor", offset)
		}
		cursor = got["next_cursor"].(string)
		if strings.Contains(cursor, "alpha") {
			t.Errorf("the cursor shows the query")
		}
	}
	// The offset argument reaches the same pages.
	last := env.web(t, `{"q":"x","offset":9}`)
	if last["has_more"] != false || last["next_cursor"] != nil {
		t.Errorf("offset 9 argument = %v", last)
	}
	if got := env.web(t, `{"q":"x","offset":8}`); got["has_more"] != true {
		t.Errorf("offset 8 argument = %v", got)
	}

	first := env.web(t, `{"q":"alpha","count":5,"country":"DE","timezone":"Europe/Berlin","latitude":1,"longitude":2}`)
	cursor = first["next_cursor"].(string)
	args := `"q":"alpha","count":5,"country":"DE","timezone":"Europe/Berlin","latitude":1,"longitude":2`
	// What does not shape the list leaves the cursor valid.
	if _, err := env.invoke("brave.web.search", "search", `{`+args+`,"no_cache":true,"cursor":"`+cursor+`"}`); err != nil {
		t.Errorf("cursor with no_cache = %v", err)
	}
	for name, arguments := range map[string]string{
		"another query":      `{"q":"beta","count":5,"country":"DE","timezone":"Europe/Berlin","latitude":1,"longitude":2,"cursor":"%s"}`,
		"another page size":  `{"q":"alpha","count":6,"country":"DE","timezone":"Europe/Berlin","latitude":1,"longitude":2,"cursor":"%s"}`,
		"another timezone":   `{"q":"alpha","count":5,"country":"DE","timezone":"Europe/Paris","latitude":1,"longitude":2,"cursor":"%s"}`,
		"no timezone":        `{"q":"alpha","count":5,"country":"DE","latitude":1,"longitude":2,"cursor":"%s"}`,
		"another location":   `{"q":"alpha","count":5,"country":"DE","timezone":"Europe/Berlin","latitude":1,"longitude":3,"cursor":"%s"}`,
		"another filter":     `{` + args + `,"result_filter":["web"],"cursor":"%s"}`,
		"another decoration": `{` + args + `,"text_decorations":true,"cursor":"%s"}`,
		"another units":      `{` + args + `,"units":"metric","cursor":"%s"}`,
		"another summary":    `{` + args + `,"summary":true,"cursor":"%s"}`,
		"another rich":       `{` + args + `,"enable_rich_callback":true,"cursor":"%s"}`,
		"other goggles":      `{` + args + `,"goggles":["$discard"],"cursor":"%s"}`,
		"cut":                `{` + args + `,"cursor":"%.10s"}`,
		"altered":            `{` + args + `,"cursor":"%s"}`,
		"offset zero":        `{` + args + `,"offset":0,"cursor":"%s"}`,
	} {
		altered := cursor
		if name == "altered" {
			altered = cursor[:len(cursor)-1] + map[bool]string{true: "A", false: "B"}[cursor[len(cursor)-1] != 'A']
		}
		before := len(f.recorded())
		_, err := env.invoke("brave.web.search", "search", fmt.Sprintf(arguments, altered))
		if !isInvalidRequest(err) || len(f.recorded()) != before {
			t.Errorf("%s: err = %v, want a refused cursor before any request", name, err)
		}
	}
}

// A cursor for an offset that Brave does not serve is refused even when its checksum is right.
func TestWebSearchRefusesACursorBeyondOffsetNine(t *testing.T) {
	binding := provider.CursorBinding(webSearch.ID, "x", "", "", "", "", 0, (*bool)(nil), "", false, []string(nil), "",
		[]string(nil), (*bool)(nil), (*bool)(nil), (*bool)(nil), (*bool)(nil), (*bool)(nil), "", location{})
	for _, inner := range []string{"10", "0", "-1", "x", ""} {
		env := webEnvironment(t, "")
		_, err := env.invoke("brave.web.search", "search", `{"q":"x","cursor":"`+provider.EncodeCursor(binding, inner)+`"}`)
		if !isInvalidRequest(err) {
			t.Errorf("offset %q: err = %v, want a refused cursor", inner, err)
		}
	}
	env := webEnvironment(t, "")
	if _, err := env.invoke("brave.web.search", "search", `{"q":"x","cursor":"`+provider.EncodeCursor(binding, "9")+`"}`); err != nil {
		t.Errorf("the cursor of offset 9 is refused: %v", err)
	}
}

func TestWebSearchSummaryAndRichAnswers(t *testing.T) {
	body := `{"type":"search","query":{"original":"weather in munich","summary_key":"k1"},` +
		`"summarizer":{"type":"summarizer","key":"k1"},"rich":{"type":"rich","hint":{"vertical":"weather","callback_key":"cb"}}}`
	f := &fakeBrave{answer: func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, body) }}
	env := newEnvironment(t, f)
	got := env.web(t, `{"q":"weather in munich","summary":true,"enable_rich_callback":true}`)
	if pick(t, got, "summarizer", "key") != "k1" || pick(t, got, "query", "summary_key") != "k1" ||
		pick(t, got, "rich", "hint", "callback_key") != "cb" || pick(t, got, "rich", "hint", "vertical") != "weather" ||
		pick(t, got, "summarizer", "type") != "summarizer" || pick(t, got, "rich", "type") != "rich" {
		t.Errorf("result = %v", got)
	}
	query := f.recorded()[0].query
	if query.Get("summary") != "1" || query.Get("enable_rich_callback") != "true" {
		t.Errorf("query = %v", query)
	}
	// Without the arguments Brave sends neither member and the tool shows none.
	plain := webEnvironment(t, `{"type":"search","query":{"original":"x"}}`).web(t, `{"q":"x"}`)
	if plain["summarizer"] != nil || plain["rich"] != nil {
		t.Errorf("plain = %v", plain)
	}
}

func TestWebSearchTextAndListsAreBounded(t *testing.T) {
	long := func(n int) string { return strings.Repeat("ä", n) }
	var items []string
	for i := 0; i < 60; i++ {
		items = append(items, fmt.Sprintf(`{"title":"t%d","url":"https://e.example/%d"}`, i, i))
	}
	deep := `{"a":{"b":{"c":{"d":{"e":{"f":{"g":{"h":1}}}}}}}}`
	body := `{"type":"` + long(500) + `","query":{"original":"` + long(3000) + `","related_queries":[` +
		strings.TrimSuffix(strings.Repeat(`"r",`, 40), ",") + `],"search_operators":{"sites":[` +
		strings.TrimSuffix(strings.Repeat(`"s.example",`, 40), ",") + `]}},` +
		`"web":{"results":[{"title":"` + long(900) + `","description":"` + long(5000) + `","age":"` + long(900) + `",` +
		`"url":"https://e.example/` + strings.Repeat("p", 3000) + `",` +
		`"profile":{"name":"` + long(900) + `","url":"javascript:alert(1)","img":"data:image/png;base64,AAAA"},` +
		`"thumbnail":{"src":"//e.example/t.jpg","original":"https://e.example/t o.jpg"},` +
		`"fetched_content_timestamp":1.9,"is_live":"yes","family_friendly":1,"language":["de"],` +
		`"extra_snippets":["` + long(5000) + `","b","c","d","e","f","g"],` +
		`"icons":[` + strings.TrimSuffix(strings.Repeat(`{"href":"/relative.ico","sizes":"16x16"},`, 15), ",") + `],` +
		`"meta_url":{"hostname":"` + long(5000) + `","favicon":"` + userinfoURL("e.example", "/f.ico") +
		`","ok":"https://e.example/f.ico","other":"javascript:alert(1)"},` +
		`"schemas":[` + deep + `,[[[[[[[1]]]]]]],` + strings.TrimSuffix(strings.Repeat(`1,`, 30), ",") + `,"` + long(5000) + `"],` +
		`"location":{"coordinates":[` + strings.TrimSuffix(strings.Repeat(`1.5,`, 40), ",") + `]},` +
		`"product":{"` + strings.Repeat("k", 300) + `":"v","n":null}}` + `,` + strings.Join(items, ",") + `]}}`
	got := webEnvironment(t, body).web(t, `{"q":"x"}`)
	runes := func(s any) int { return len([]rune(s.(string))) }
	results := pick(t, got, "web", "results").([]any)
	first := results[0].(map[string]any)
	if len(results) != 20 {
		t.Errorf("%d web results, want the first 20", len(results))
	}
	if runes(got["type"]) != 201 || runes(first["title"]) != 301 || runes(first["description"]) != 1501 ||
		runes(first["age"]) != 201 || runes(pick(t, first, "profile", "name")) != 301 ||
		runes(pick(t, got, "query", "original")) != 1501 {
		t.Errorf("text lengths %d %d %d %d", runes(got["type"]), runes(first["title"]), runes(first["description"]), runes(first["age"]))
	}
	if !strings.HasSuffix(first["title"].(string), "…") {
		t.Errorf("a cut text carries no mark")
	}
	if len(first["extra_snippets"].([]any)) != 5 || runes(pick(t, first, "extra_snippets", 0)) != 1001 ||
		len(first["icons"].([]any)) != 10 || len(pick(t, got, "query", "related_queries").([]any)) != 20 ||
		len(pick(t, got, "query", "search_operators", "sites").([]any)) != 20 {
		t.Errorf("lists are not cut: %v", first["icons"])
	}
	// Addresses that are too long, not http(s), carry user information or spaces, or are relative are left out.
	for _, name := range []string{"url"} {
		if _, ok := first[name]; ok {
			t.Errorf("unsafe %s is shown", name)
		}
	}
	profile := first["profile"].(map[string]any)
	if profile["url"] != nil || profile["img"] != nil || first["thumbnail"] != nil ||
		pick(t, first, "icons", 0, "href") != nil {
		t.Errorf("an unsafe address is shown: %v / %v / %v", profile, first["thumbnail"], first["icons"])
	}
	// A member of another type than documented is left out, a number is made whole.
	if first["is_live"] != nil || first["family_friendly"] != nil || first["language"] != nil ||
		first["fetched_content_timestamp"] != float64(1) {
		t.Errorf("types: %v %v %v %v", first["is_live"], first["family_friendly"], first["language"],
			first["fetched_content_timestamp"])
	}
	// Open members are bounded as JSON: depth, length, member count, text, and addresses.
	meta := first["meta_url"].(map[string]any)
	if runes(meta["hostname"]) != 1501 || meta["favicon"] != nil || meta["other"] != nil || meta["ok"] != "https://e.example/f.ico" {
		t.Errorf("meta_url = %v", meta)
	}
	schemas := first["schemas"].([]any)
	if len(schemas) != 20 {
		t.Errorf("%d schemas, want 20", len(schemas))
	}
	// Nesting beyond the limit is cut, and a chain that ends empty disappears with its parents.
	if !reflect.DeepEqual(schemas[0], map[string]any{}) ||
		!reflect.DeepEqual(schemas[1], []any{[]any{[]any{[]any{[]any{}}}}}) {
		t.Errorf("depth: %v / %v", schemas[0], schemas[1])
	}
	if len(pick(t, first, "location", "coordinates").([]any)) != 20 {
		t.Errorf("coordinates are not cut")
	}
	product := first["product"].(map[string]any)
	if len(product) != 1 || product["n"] != nil || runes(func() any {
		for k := range product {
			return k
		}
		return ""
	}()) != 201 {
		t.Errorf("product = %v", product)
	}
	if results[1].(map[string]any)["url"] != "https://e.example/0" {
		t.Errorf("a plain address was changed: %v", results[1])
	}
}

func TestWebSearchAnswersOtherwiseSparseStillPass(t *testing.T) {
	for name, body := range map[string]string{
		"empty object": `{}`,
		"only type":    `{"type":"search"}`,
		"null":         `null`,
		"null groups": `{"query":null,"web":null,"discussions":null,"infobox":null,"locations":null,"news":null,` +
			`"videos":null,"faq":null,"mixed":null,"summarizer":null,"rich":null}`,
		"null results": `{"web":{"results":null},"news":{"results":[null,1,"x",{"title":null}]}}`,
		"wrong groups": `{"web":[],"news":"x","query":5,"mixed":{"main":{"a":1}}}`,
		"null members": `{"web":{"results":[{"title":null,"url":null,"profile":null,"meta_url":null,"thumbnail":null,` +
			`"extra_snippets":null,"icons":null,"location":null,"deep_results":null,"schemas":null}]}}`,
	} {
		env := webEnvironment(t, body)
		got := env.web(t, `{"q":"x"}`)
		if got["has_more"] != false {
			t.Errorf("%s: has_more = %v", name, got["has_more"])
		}
	}
}

func TestWebSearchAnswersThatCannotBeReadAreInvalid(t *testing.T) {
	var big strings.Builder
	big.WriteString(`{"web":{"results":[{"title":"`)
	big.WriteString(strings.Repeat("a", maxResponseBytes))
	big.WriteString(`"}]}}`)
	for name, answer := range map[string]func(http.ResponseWriter, *http.Request){
		"not json":  reply(200, nil, "<html>maintenance</html>"),
		"empty":     reply(200, nil, ""),
		"an array":  reply(200, nil, `[1,2]`),
		"too large": reply(200, nil, big.String()),
	} {
		env := newEnvironment(t, &fakeBrave{answer: answer})
		_, err := env.invoke("brave.web.search", "search", `{"q":"x"}`)
		if classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s: err = %v, want invalid-provider-response", name, err)
		}
	}
}

func TestWebSearchStatusesMapToClassesWithoutProviderText(t *testing.T) {
	for _, tc := range []struct {
		status int
		class  provider.Class
	}{
		{http.StatusBadRequest, provider.ClassInvalidResponse},
		{http.StatusUnauthorized, provider.ClassAuth},
		{http.StatusPaymentRequired, provider.ClassPermission},
		{http.StatusForbidden, provider.ClassPermission},
		{http.StatusNotFound, provider.ClassNotFound},
		{http.StatusUnprocessableEntity, provider.ClassInvalidResponse},
		{http.StatusTooManyRequests, provider.ClassRateLimited},
		{http.StatusInternalServerError, provider.ClassProviderError},
		{http.StatusBadGateway, provider.ClassUnreachable},
		{http.StatusServiceUnavailable, provider.ClassUnreachable},
		{http.StatusGatewayTimeout, provider.ClassTimeout},
	} {
		env := newEnvironment(t, &fakeBrave{answer: reply(tc.status, nil, errorBody(tc.status))})
		_, err := env.invoke("brave.web.search", "search", `{"q":"secret query text","timezone":"Europe/Berlin",`+
			`"city":"Secretville","goggles":["$boost=1"]}`)
		if classOf(err) != tc.class {
			t.Errorf("HTTP %d: class = %q (%v), want %q", tc.status, classOf(err), err, tc.class)
			continue
		}
		for _, leak := range []string{detailCanary, tokenValue, "secret query text", "Secretville", "Europe/Berlin", "boost",
			"api.search.brave.com", "VALIDATION"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("HTTP %d: error %q leaks %q", tc.status, err, leak)
			}
		}
	}
}

func TestWebSearchBetweenTheKeyAndTheResult(t *testing.T) {
	body := `{"type":"search","web":{"results":[{"title":"Bearer ` + tokenValue + `","url":"https://e.example/"}]}}`
	env := webEnvironment(t, body)
	result, err := env.invoke("brave.web.search", "search", `{"q":"x"}`)
	if err != nil || strings.Contains(result, tokenValue) {
		t.Errorf("result %s, err %v: the key reached the output", result, err)
	}
}

func TestWebSearchIsListedAndDescribesItsCost(t *testing.T) {
	descriptor := webSearch
	if descriptor.ID != "brave.web.search" || descriptor.Risk != readRisk {
		t.Fatalf("descriptor = %s / %+v", descriptor.ID, descriptor.Risk)
	}
	for _, want := range []string{"Search plan", "billed", "terms of use", "untrusted"} {
		if !strings.Contains(descriptor.Description, want) {
			t.Errorf("description lacks %q: %s", want, descriptor.Description)
		}
	}
	var input struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(descriptor.InputSchema, &input); err != nil {
		t.Fatal(err)
	}
	named := map[string]bool{}
	for _, argument := range descriptor.Arguments {
		named[argument.Name] = true
		if _, ok := input.Properties[argument.Name]; !ok {
			t.Errorf("argument %s is not in the input schema", argument.Name)
		}
	}
	for name := range input.Properties {
		if !named[name] {
			t.Errorf("input property %s has no argument description", name)
		}
	}
	for _, name := range []string{"q", "country", "search_lang", "ui_lang", "count", "offset", "safesearch", "spellcheck",
		"freshness", "text_decorations", "result_filter", "units", "goggles", "extra_snippets", "summary",
		"enable_rich_callback", "include_fetch_metadata", "operators", "timezone", "latitude", "longitude", "city",
		"state", "state_name", "country_code", "postal_code", "no_cache", "cursor"} {
		if !named[name] {
			t.Errorf("parameter %s is not offered", name)
		}
	}
	if named["goggles_id"] || named["restaurant"] {
		t.Errorf("a deprecated parameter is offered")
	}
}

func TestOnlyWebSearchTakesATimezone(t *testing.T) {
	for _, descriptor := range []string{string(newsSearch.InputSchema), string(llmContext.InputSchema)} {
		if strings.Contains(descriptor, "timezone") {
			t.Errorf("another tool takes a timezone")
		}
	}
}
