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

// placeFixture is one place in the members of Brave's reference, plus a member Brave may add later.
const placeFixture = `{"title": "Cafe Example", "url": "https://cafe.example/", "is_source_local": true,
  "is_source_both": false, "description": "A cafe", "page_age": "2026-10-01T00:00:00",
  "page_fetched": "2026-10-20T00:00:00Z", "fetched_content_timestamp": 1760918400,
  "profile": {"name": "Cafe", "url": "https://cafe.example", "long_name": "Cafe Example", "img": "https://img.example/c.png"},
  "language": "en", "family_friendly": true, "type": "location_result", "provider_url": "https://maps.example/c",
  "coordinates": [52.52, 13.405], "zoom_level": 14,
  "thumbnail": {"src": "https://img.example/t.jpg", "alt": "front", "height": 100, "width": 200, "bg_color": "#fff",
    "original": "https://cafe.example/t.jpg", "logo": false, "duplicated": false, "theme": "light"},
  "postal_address": {"type": "PostalAddress", "country": "DE", "postalCode": "10115", "streetAddress": "Main 1",
    "addressRegion": "BE", "addressLocality": "Berlin", "displayAddress": "Main 1, Berlin"},
  "opening_hours": {"current_day": [{"abbr_name": "Mon", "full_name": "Monday", "opens": "08:00", "closes": "18:00"}],
    "days": [[{"abbr_name": "Mon", "full_name": "Monday", "opens": "08:00", "closes": "18:00"}]]},
  "contact": {"email": "contact-1", "telephone": "+49 30 1"}, "price_range": "$$",
  "rating": {"ratingValue": 4.5, "bestRating": 5, "reviewCount": 80, "is_tripadvisor": true,
    "profile": {"name": "Trip", "url": "https://trip.example", "long_name": "Trip Advisor", "img": "https://img.example/ta.png"}},
  "distance": {"value": 1.5, "units": "km"},
  "profiles": [{"type": "external", "name": "trip.example", "url": "https://trip.example/c", "long_name": "Trip", "img": "https://img.example/i.png"}],
  "reviews": {"results": [{"title": "Nice", "description": "Good coffee", "date": "2026-01-01",
    "rating": {"ratingValue": 5, "bestRating": 5, "reviewCount": 1, "is_tripadvisor": true},
    "author": {"type": "person", "name": "Reviewer", "url": "https://trip.example/u", "email": "contact-2",
      "thumbnail": {"src": "https://img.example/u.jpg"}},
    "review_url": "https://trip.example/r/1", "language": "en"}],
    "viewMoreUrl": "https://trip.example/all", "reviews_in_foreign_language": true},
  "pictures": {"viewMoreUrl": "https://pics.example/all", "results": [{"src": "https://pics.example/1.jpg", "alt": "one"}]},
  "action": {"type": "reserve", "url": "https://cafe.example/reserve"}, "serves_cuisine": ["coffee"],
  "categories": ["cafe"], "icon_category": "cafe", "timezone": "Europe/Berlin", "timezone_offset": 3600,
  "id": "locCAFE1PLACE7ID=",
  "results": [{"title": "Mention", "url": "https://m.example/", "meta_url": {"scheme": "https", "netloc": "m.example",
    "hostname": "m.example", "favicon": "https://m.example/f.ico", "path": "› p"}}], "added_later": 1}`

// placesFixture fills every bucket of the place search answer.
var placesFixture = `{"type": "local_pois", "query": {"original": "cafe", "altered": "café", "spellcheck_off": false,
  "show_strict_warning": false},
  "results": [` + placeFixture + `],
  "cities": [{"type": "city", "name": "Berlin", "country": "DE", "coordinates": [52.5, 13.4],
    "thumbnail": {"src": "https://img.example/city.jpg", "width": 10}}],
  "countries": [{"type": "country", "name": "Germany", "country": "DE", "coordinates": [51, 10],
    "thumbnail": {"src": "https://img.example/country.jpg"}}],
  "regions": [{"type": "region", "name": "Berlin", "country": "DE", "coordinates": [52.5, 13.4],
    "thumbnail": {"src": "https://img.example/region.jpg"}}],
  "neighborhoods": [{"type": "neighborhood", "name": "Mitte", "country": "DE", "coordinates": [52.52, 13.4],
    "thumbnail": {"src": "https://img.example/hood.jpg"}}],
  "addresses": [{"type": "address", "name": "Main 1", "coordinates": [52.52, 13.4], "zoom_level": 17,
    "distance": {"value": 0.2, "units": "km"}, "postal_address": {"displayAddress": "Main 1, Berlin"},
    "pois": [` + placeFixture + `], "pois_nearby": [{"title": "Near", "id": "locNEAR1"}]}],
  "streets": [{"type": "street", "name": "Main", "coordinates": [52.52, 13.4], "zoom_level": 16,
    "distance": {"value": 0.3, "units": "km"}, "postal_address": {"displayAddress": "Main, Berlin"},
    "pois": [{"title": "On street", "id": "locSTREET1"}], "pois_nearby": [` + placeFixture + `]}],
  "mixed": [{"type": "results", "index": 0, "all": false}, {"type": "cities", "all": true}],
  "location": {"coordinates": [52.52, 13.405], "name": "Berlin", "country": "DE"}, "added_later": 1}`

const poisFixture = `{"type": "local_pois", "results": [` + placeFixture + `]}`

const descriptionsFixture = `{"type": "local_descriptions", "results": [
  {"type": "local_description", "id": "locCAFE1PLACE7ID=", "description": "A small cafe."},
  {"type": "local_description", "id": "locCAFE2PLACE7ID"}]}`

// placesEnv answers every request with body and rate-limit headers.
func placesEnv(t *testing.T, body string) (*environment, *fakeBrave) {
	t.Helper()
	f := &fakeBrave{answer: func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "1, 15000")
		w.Header().Set("X-RateLimit-Policy", "1;w=1, 15000;w=2592000")
		w.Header().Set("X-RateLimit-Remaining", "1, 14523")
		w.Header().Set("X-RateLimit-Reset", "1, 1234567")
		fmt.Fprint(w, body)
	}}
	return newEnvironment(t, f), f
}

func (e *environment) call(t *testing.T, tool, arguments string) map[string]any {
	t.Helper()
	result, err := e.invoke(tool, "search", arguments)
	if err != nil {
		t.Fatalf("invoke %s %s = %v", tool, arguments, err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("result %s: %v", result, err)
	}
	return got
}

func TestPlaceSearchShowsEveryBucket(t *testing.T) {
	env, _ := placesEnv(t, placesFixture)
	got := env.call(t, "brave.places.search", `{"q":"cafe","location":"berlin germany"}`)
	for _, bucket := range []string{"results", "cities", "countries", "regions", "neighborhoods", "addresses",
		"streets", "mixed", "location", "query"} {
		if got[bucket] == nil {
			t.Errorf("bucket %s is missing", bucket)
		}
	}
	if _, ok := got["added_later"]; ok || got["type"] != "local_pois" {
		t.Errorf("type or unknown member: %v", got)
	}
	place := pick(t, got, "results", 0).(map[string]any)
	if _, ok := place["added_later"]; ok || place["id"] != "locCAFE1PLACE7ID=" {
		t.Errorf("place = %v", place)
	}
	if pick(t, place, "reviews", "results", 0, "author", "name") != "Reviewer" ||
		pick(t, place, "reviews", "reviews_in_foreign_language") != true ||
		pick(t, place, "pictures", "results", 0, "src") != "https://pics.example/1.jpg" ||
		pick(t, place, "action", "type") != "reserve" || pick(t, place, "rating", "is_tripadvisor") != true ||
		pick(t, place, "rating", "profile", "name") != "Trip" || pick(t, place, "timezone_offset") != float64(3600) ||
		pick(t, place, "thumbnail", "theme") != "light" || pick(t, place, "results", 0, "meta_url", "hostname") != "m.example" ||
		!reflect.DeepEqual(pick(t, place, "coordinates"), []any{52.52, 13.405}) {
		t.Errorf("nested members are wrong: %v", place)
	}
	if pick(t, got, "cities", 0, "name") != "Berlin" || pick(t, got, "cities", 0, "thumbnail", "src") != "https://img.example/city.jpg" ||
		pick(t, got, "countries", 0, "country") != "DE" || pick(t, got, "regions", 0, "type") != "region" ||
		pick(t, got, "neighborhoods", 0, "name") != "Mitte" ||
		pick(t, got, "addresses", 0, "pois", 0, "id") != "locCAFE1PLACE7ID=" ||
		pick(t, got, "addresses", 0, "pois_nearby", 0, "id") != "locNEAR1" ||
		pick(t, got, "addresses", 0, "postal_address", "displayAddress") != "Main 1, Berlin" ||
		pick(t, got, "addresses", 0, "distance", "units") != "km" ||
		pick(t, got, "streets", 0, "pois", 0, "id") != "locSTREET1" ||
		pick(t, got, "streets", 0, "pois_nearby", 0, "title") != "Cafe Example" ||
		pick(t, got, "streets", 0, "zoom_level") != float64(16) ||
		pick(t, got, "mixed", 1, "all") != true || pick(t, got, "mixed", 0, "index") != float64(0) ||
		pick(t, got, "location", "name") != "Berlin" || pick(t, got, "location", "country") != "DE" ||
		!reflect.DeepEqual(pick(t, got, "location", "coordinates"), []any{52.52, 13.405}) ||
		pick(t, got, "query", "altered") != "café" || pick(t, got, "quota", "windows", 1, "remaining") != float64(14523) {
		t.Errorf("buckets are wrong: %v", got)
	}
}

// Every member of every shape passes the cleaning and the output schema, so no documented member is lost.
func TestPlaceShapesKeepEveryMember(t *testing.T) {
	for _, tc := range []struct {
		tool, schema string
		shape        *shape
	}{
		{"brave.places.search", placesOutputSchema, placesAnswerShape},
		{"brave.pois.get", string(poisGet.OutputSchema), poisAnswerShape},
		{"brave.poi-descriptions.get", string(poiDescriptionsGet.OutputSchema), descriptionsAnswerShape},
	} {
		full := sample(tc.shape)
		if cleaned, ok := tc.shape.clean(full); !ok || !reflect.DeepEqual(cleaned, full) {
			t.Fatalf("%s: cleaning changed a complete answer", tc.tool)
		}
		encoded, _ := json.Marshal(full)
		env, _ := placesEnv(t, string(encoded))
		arguments := `{"ids":["locA"]}`
		if tc.tool == "brave.places.search" {
			arguments = `{}`
		}
		got := env.call(t, tc.tool, arguments)
		delete(got, "quota")
		if !reflect.DeepEqual(got, full) {
			t.Errorf("%s: the tool result differs from a complete answer", tc.tool)
		}
		var schema map[string]any
		if err := json.Unmarshal([]byte(tc.schema), &schema); err != nil {
			t.Errorf("%s output schema: %v", tc.tool, err)
		}
	}
}

func TestPlaceSearchSendsEveryParameter(t *testing.T) {
	env, f := placesEnv(t, placesFixture)
	_, err := env.invoke("brave.places.search", "search", `{"q":" cafe  bar ","latitude":-24.78,"longitude":-65.41,`+
		`"radius":1500,"count":100,"country":"AR","search_lang":"es","ui_lang":"es-AR","units":"imperial",`+
		`"safesearch":"off","spellcheck":false,"geoloc":"-24.78x-65.41","no_cache":true}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	r := f.recorded()[0]
	want := url.Values{"q": {"cafe  bar"}, "latitude": {"-24.78"}, "longitude": {"-65.41"}, "radius": {"1500"},
		"count": {"100"}, "country": {"AR"}, "search_lang": {"es"}, "ui_lang": {"es-AR"}, "units": {"imperial"},
		"safesearch": {"off"}, "spellcheck": {"false"}, "geoloc": {"-24.78x-65.41"}}
	if !reflect.DeepEqual(r.query, want) || r.method != http.MethodGet || r.path != "/res/v1/local/place_search" ||
		r.header.Get("Cache-Control") != "no-cache" || r.header.Get("X-Subscription-Token") != tokenValue ||
		strings.Contains(r.rawQuery, tokenValue) {
		t.Errorf("request = %+v", r)
	}

	// A named location replaces the coordinates.
	if _, err := env.invoke("brave.places.search", "search", `{"location":"tokyo japan","count":1}`); err != nil {
		t.Fatal(err)
	}
	r = f.recorded()[1]
	if !reflect.DeepEqual(r.query, url.Values{"location": {"tokyo japan"}, "count": {"1"}}) || r.header.Get("Cache-Control") != "" {
		t.Errorf("location request = %v", r.query)
	}
	// Nothing is sent that the caller did not set, not even a query.
	if _, err := env.invoke("brave.places.search", "search", `{"no_cache":false,"radius":0}`); err != nil {
		t.Fatal(err)
	}
	r = f.recorded()[2]
	if !reflect.DeepEqual(r.query, url.Values{"radius": {"0"}}) || r.header.Get("X-Loc-Lat") != "" {
		t.Errorf("minimal request = %v", r.query)
	}
}

func TestPlaceSearchRefusesInvalidArgumentsBeforeTheKey(t *testing.T) {
	for name, arguments := range map[string]string{
		"latitude alone":       `{"latitude":52.5}`,
		"longitude alone":      `{"longitude":13.4}`,
		"latitude range":       `{"latitude":90.5,"longitude":1}`,
		"longitude range":      `{"latitude":1,"longitude":-181}`,
		"coordinates and name": `{"latitude":52.5,"longitude":13.4,"location":"berlin germany"}`,
		"blank location":       `{"location":" berlin "}`,
		"long location":        `{"location":"` + strings.Repeat("a", 101) + `"}`,
		"control in location":  `{"location":"ber\u0007lin"}`,
		"blank query":          `{"q":"   "}`,
		"long query":           `{"q":"` + strings.Repeat("a", 401) + `"}`,
		"many words":           `{"q":"` + strings.TrimSpace(strings.Repeat("w ", 51)) + `"}`,
		"control in query":     `{"q":"a\u0007b"}`,
		"radius negative":      `{"radius":-1}`,
		"radius high":          `{"radius":20000001}`,
		"radius text":          `{"radius":"10"}`,
		"count zero":           `{"count":0}`,
		"count high":           `{"count":101}`,
		"unknown country":      `{"country":"XX"}`,
		"unknown language":     `{"search_lang":"xx"}`,
		"unknown ui language":  `{"ui_lang":"xx-XX"}`,
		"unknown units":        `{"units":"nautical"}`,
		"unknown safesearch":   `{"safesearch":"loose"}`,
		"geoloc order":         `{"geoloc":"13.4,52.5"}`,
		"geoloc range":         `{"geoloc":"91x10"}`,
		"geoloc longitude":     `{"geoloc":"10x181"}`,
		"geoloc text":          `{"geoloc":"1x2x3"}`,
		"geoloc long":          `{"geoloc":"` + strings.Repeat("1", 41) + `"}`,
		"offset":               `{"offset":1}`,
		"cursor":               `{"cursor":"abcdefabcdefabcdefabcdef"}`,
		"wrong type":           `{"spellcheck":"yes"}`,
	} {
		env, f := placesEnv(t, placesFixture)
		_, err := env.invoke("brave.places.search", "search", arguments)
		if !isInvalidRequest(err) || *env.reads != 0 || len(f.recorded()) != 0 {
			t.Errorf("%s: err = %v, reads = %d, requests = %d, want invalid-request before the key",
				name, err, *env.reads, len(f.recorded()))
		}
	}
	// The error names the rule and never the value.
	env, _ := placesEnv(t, placesFixture)
	_, err := env.invoke("brave.places.search", "search", `{"latitude":52.5123}`)
	if err == nil || strings.Contains(err.Error(), "52.5123") || !strings.Contains(err.Error(), "together") {
		t.Errorf("err = %v", err)
	}
}

func TestPOIsSendEveryIDAsARepeatedParameter(t *testing.T) {
	env, f := placesEnv(t, poisFixture)
	_, err := env.invoke("brave.pois.get", "search", `{"ids":["locA1=","locB2","loc_C-3"],"search_lang":"de",`+
		`"ui_lang":"de-DE","units":"metric","latitude":52.52,"longitude":13.405,"no_cache":true}`)
	if err != nil {
		t.Fatalf("invoke = %v", err)
	}
	r := f.recorded()[0]
	if r.method != http.MethodGet || r.path != "/res/v1/local/pois" ||
		r.rawQuery != "ids=locA1%3D&ids=locB2&ids=loc_C-3&search_lang=de&ui_lang=de-DE&units=metric" {
		t.Errorf("request = %+v", r)
	}
	for name, value := range map[string]string{"X-Loc-Lat": "52.52", "X-Loc-Long": "13.405", "Cache-Control": "no-cache",
		"X-Subscription-Token": tokenValue} {
		if r.header.Get(name) != value {
			t.Errorf("header %s = %q, want %q", name, r.header.Get(name), value)
		}
	}
	if _, err := env.invoke("brave.pois.get", "search", `{"ids":["locA1"]}`); err != nil {
		t.Fatal(err)
	}
	r = f.recorded()[1]
	if r.rawQuery != "ids=locA1" || r.header.Get("X-Loc-Lat") != "" || r.header.Get("Cache-Control") != "" {
		t.Errorf("minimal request = %+v", r)
	}
}

func TestPOIIDBoundsAreOneThroughTwenty(t *testing.T) {
	ids := func(n int) string {
		var items []string
		for i := 0; i < n; i++ {
			items = append(items, fmt.Sprintf(`"loc%d"`, i))
		}
		return `[` + strings.Join(items, ",") + `]`
	}
	for _, tool := range []string{"brave.pois.get", "brave.poi-descriptions.get"} {
		for _, n := range []int{1, 20} {
			env, f := placesEnv(t, poisFixture)
			if _, err := env.invoke(tool, "search", `{"ids":`+ids(n)+`}`); err != nil {
				t.Errorf("%s with %d ids: %v", tool, n, err)
			}
			if got := f.recorded(); len(got) != 1 || len(got[0].query["ids"]) != n {
				t.Errorf("%s with %d ids: requests = %+v", tool, n, got)
			}
		}
		for name, arguments := range map[string]string{
			"missing":       `{}`,
			"none":          `{"ids":[]}`,
			"twenty-one":    `{"ids":` + ids(21) + `}`,
			"text":          `{"ids":"locA"}`,
			"empty id":      `{"ids":[""]}`,
			"long id":       `{"ids":["` + strings.Repeat("a", 129) + `"]}`,
			"space":         `{"ids":["loc A"]}`,
			"separator":     `{"ids":["locA&ids=locB"]}`,
			"slash":         `{"ids":["loc/A"]}`,
			"padding first": `{"ids":["=locA"]}`,
			"twice":         `{"ids":["locA","locA"]}`,
			"number":        `{"ids":[1]}`,
			"credential":    `{"ids":["locA"],"token":"x"}`,
		} {
			env, f := placesEnv(t, poisFixture)
			_, err := env.invoke(tool, "search", arguments)
			if !isInvalidRequest(err) || *env.reads != 0 || len(f.recorded()) != 0 {
				t.Errorf("%s %s: err = %v, reads = %d, requests = %d, want invalid-request before the key",
					tool, name, err, *env.reads, len(f.recorded()))
			}
		}
	}
	for name, arguments := range map[string]string{
		"latitude alone":  `{"ids":["locA"],"latitude":1}`,
		"longitude alone": `{"ids":["locA"],"longitude":1}`,
		"latitude range":  `{"ids":["locA"],"latitude":91,"longitude":1}`,
		"unknown units":   `{"ids":["locA"],"units":"x"}`,
		"unknown lang":    `{"ids":["locA"],"search_lang":"xx"}`,
		"country":         `{"ids":["locA"],"country":"US"}`,
	} {
		env, f := placesEnv(t, poisFixture)
		_, err := env.invoke("brave.pois.get", "search", arguments)
		if !isInvalidRequest(err) || *env.reads != 0 || len(f.recorded()) != 0 {
			t.Errorf("pois %s: err = %v, reads = %d, requests = %d", name, err, *env.reads, len(f.recorded()))
		}
	}
}

func TestDescriptionsRequestAndAnswer(t *testing.T) {
	env, f := placesEnv(t, descriptionsFixture)
	got := env.call(t, "brave.poi-descriptions.get", `{"ids":["locCAFE1PLACE7ID=","locCAFE2PLACE7ID"],"no_cache":true}`)
	r := f.recorded()[0]
	if r.path != "/res/v1/local/descriptions" || r.rawQuery != "ids=locCAFE1PLACE7ID%3D&ids=locCAFE2PLACE7ID" ||
		r.header.Get("Cache-Control") != "no-cache" || r.header.Get("X-Loc-Lat") != "" {
		t.Errorf("request = %+v", r)
	}
	if got["type"] != "local_descriptions" || pick(t, got, "results", 0, "type") != "local_description" ||
		pick(t, got, "results", 0, "id") != "locCAFE1PLACE7ID=" ||
		pick(t, got, "results", 0, "description") != "A small cafe." ||
		len(pick(t, got, "results", 1).(map[string]any)) != 2 {
		t.Errorf("answer = %v", got)
	}
}

func TestPOIAnswerShowsTheDetails(t *testing.T) {
	env, _ := placesEnv(t, poisFixture)
	got := env.call(t, "brave.pois.get", `{"ids":["locCAFE1PLACE7ID="]}`)
	place := pick(t, got, "results", 0).(map[string]any)
	if got["type"] != "local_pois" || place["id"] != "locCAFE1PLACE7ID=" ||
		pick(t, place, "reviews", "results", 0, "description") != "Good coffee" ||
		pick(t, place, "reviews", "viewMoreUrl") != "https://trip.example/all" ||
		pick(t, place, "pictures", "viewMoreUrl") != "https://pics.example/all" ||
		pick(t, place, "action", "url") != "https://cafe.example/reserve" ||
		pick(t, place, "icon_category") != "cafe" || pick(t, place, "opening_hours", "days", 0, 0, "opens") != "08:00" ||
		pick(t, place, "contact", "telephone") != "+49 30 1" || pick(t, place, "distance", "value") != 1.5 ||
		pick(t, place, "thumbnail", "alt") != "front" {
		t.Errorf("place = %v", place)
	}
}

func TestPlaceEndpointsMapStatusesWithoutProviderText(t *testing.T) {
	calls := map[string]string{
		"brave.places.search":        `{"q":"secret query text","location":"Secretville"}`,
		"brave.pois.get":             `{"ids":["locSecretId1"],"latitude":12.3456,"longitude":6.5432}`,
		"brave.poi-descriptions.get": `{"ids":["locSecretId1"]}`,
	}
	for tool, arguments := range calls {
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
		} {
			env := newEnvironment(t, &fakeBrave{answer: reply(tc.status, nil, errorBody(tc.status))})
			_, err := env.invoke(tool, "search", arguments)
			if classOf(err) != tc.class {
				t.Errorf("%s HTTP %d: class = %q (%v), want %q", tool, tc.status, classOf(err), err, tc.class)
				continue
			}
			for _, leak := range []string{detailCanary, tokenValue, "secret query text", "Secretville", "locSecretId1",
				"12.3456", "api.search.brave.com", "VALIDATION", "?"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("%s HTTP %d: error %q leaks %q", tool, tc.status, err, leak)
				}
			}
		}
		env := newEnvironment(t, &fakeBrave{answer: reply(200, nil, "not json")})
		if _, err := env.invoke(tool, "search", arguments); classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s: unreadable body: err = %v", tool, err)
		}
	}
}

func TestPlaceTextListsAndAddressesAreBounded(t *testing.T) {
	long := func(n int) string { return strings.Repeat("ä", n) }
	var reviews, places []string
	for i := 0; i < 15; i++ {
		reviews = append(reviews, `{"title":"`+long(900)+`","description":"`+long(5000)+`","review_url":"javascript:alert(1)",`+
			`"author":{"name":"a","url":"`+userinfoURL("e.example", "/u")+`"}}`)
	}
	for i := 0; i < 120; i++ {
		places = append(places, `{"title":"t`+fmt.Sprint(i)+`"}`)
	}
	big := `{"type":"` + long(500) + `","results":[{"title":"` + long(900) + `","description":"` + long(4000) + `",` +
		`"url":"https://e.example/` + strings.Repeat("p", 3000) + `","provider_url":"data:text/html,x","timezone":"` + long(500) + `",` +
		`"thumbnail":{"src":"//e.example/t.jpg","width":1.5},"coordinates":[1,2],` +
		`"reviews":{"results":[` + strings.Join(reviews, ",") + `],"viewMoreUrl":"ftp://e.example/"},` +
		`"pictures":{"results":[` + strings.Repeat(`{"src":"https://e.example/p.jpg"},`, 30) + `{"alt":"x"}]},` +
		`"action":{"type":"x","url":"` + userinfoURL("e.example", "/a") + `"}}` +
		`,` + strings.Join(places, ",") + `]}`
	env, _ := placesEnv(t, big)
	got := env.call(t, "brave.pois.get", `{"ids":["locA"]}`)
	runes := func(v any) int { return len([]rune(v.(string))) }
	place := pick(t, got, "results", 0).(map[string]any)
	reviewList := pick(t, place, "reviews", "results").([]any)
	first := reviewList[0].(map[string]any)
	if runes(got["type"]) != 201 || runes(place["title"]) != 301 || runes(place["description"]) != 1501 ||
		runes(place["timezone"]) != 201 {
		t.Errorf("place text lengths: %d %d %d %d", runes(got["type"]), runes(place["title"]), runes(place["description"]),
			runes(place["timezone"]))
	}
	if len(reviewList) != maxReviews || runes(first["description"]) != 1001 || runes(first["title"]) != 301 {
		t.Errorf("reviews = %d, description %d, title %d, want %d reviews of at most 1000 characters",
			len(reviewList), runes(first["description"]), runes(first["title"]), maxReviews)
	}
	if _, ok := first["review_url"]; ok {
		t.Errorf("an unsafe review address is shown")
	}
	if _, ok := pick(t, first, "author").(map[string]any)["url"]; ok {
		t.Errorf("an address with user information is shown")
	}
	for _, path := range [][]any{{"url"}, {"provider_url"}, {"thumbnail", "src"}, {"reviews", "viewMoreUrl"}, {"action", "url"}} {
		if value := place; pickOptional(value, path...) != nil {
			t.Errorf("unsafe address at %v is shown", path)
		}
	}
	if n := len(pick(t, place, "pictures", "results").([]any)); n != maxListItems {
		t.Errorf("pictures = %d, want %d", n, maxListItems)
	}
	if n := len(got["results"].([]any)); n != maxListItems {
		t.Errorf("pois results = %d, want %d", n, maxListItems)
	}

	// The place search shows up to 100 places, the count it accepts.
	env, _ = placesEnv(t, `{"results":[`+strings.Join(places, ",")+`]}`)
	got = env.call(t, "brave.places.search", `{"count":100}`)
	if n := len(got["results"].([]any)); n != placesMaxCount {
		t.Errorf("place results = %d, want %d", n, placesMaxCount)
	}
	// A description is bounded text.
	env, _ = placesEnv(t, `{"results":[{"id":"locA","description":"`+long(3000)+`"}]}`)
	got = env.call(t, "brave.poi-descriptions.get", `{"ids":["locA"]}`)
	if runes(pick(t, got, "results", 0, "description")) != 1501 {
		t.Errorf("description is not bounded")
	}
}

// pickOptional follows member names and returns nil where a member is absent.
func pickOptional(value map[string]any, path ...any) any {
	var current any = value
	for _, step := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[step.(string)]
	}
	return current
}

func TestPlaceAnswersThatAreSparseStillPass(t *testing.T) {
	for name, body := range map[string]string{
		"empty object": `{}`,
		"null members": `{"type":null,"query":null,"results":null,"cities":null,"mixed":null,"location":null}`,
		"null entries": `{"results":[{"title":null,"id":null,"thumbnail":null,"reviews":null}],"streets":[{"pois":null}]}`,
		"wrong types":  `{"results":"x","cities":{"a":1},"location":[1],"mixed":[1,"a"]}`,
	} {
		for _, call := range [][2]string{{"brave.places.search", `{}`}, {"brave.pois.get", `{"ids":["locA"]}`},
			{"brave.poi-descriptions.get", `{"ids":["locA"]}`}} {
			env, _ := placesEnv(t, body)
			if result, err := env.invoke(call[0], "search", call[1]); err != nil {
				t.Errorf("%s %s: %v %s", call[0], name, err, result)
			}
		}
	}
}

func TestPlaceToolDescriptionsStateCostIdsAndTerms(t *testing.T) {
	reg := registry(t)
	for _, tool := range []string{"brave.places.search", "brave.pois.get", "brave.poi-descriptions.get"} {
		d, _, ok := reg.Lookup(tool)
		if !ok {
			t.Fatalf("%s is not registered", tool)
		}
		for _, want := range []string{"8 hours", "billed", "terms of use", "untrusted"} {
			if !strings.Contains(d.Description, want) {
				t.Errorf("%s lacks %q", tool, want)
			}
		}
		if tool == "brave.places.search" && !strings.Contains(d.Description, "Search plan") {
			t.Errorf("%s does not name the plan", tool)
		}
		if tool != "brave.places.search" && !strings.Contains(d.Description, "neither the plan nor the cost") {
			t.Errorf("%s does not say that plan and cost are unnamed", tool)
		}
		if strings.Contains(strings.ToLower(d.Description+d.Title), "pages") {
			t.Errorf("%s mentions pages, which the tool catalog tests search for", tool)
		}
	}
}
