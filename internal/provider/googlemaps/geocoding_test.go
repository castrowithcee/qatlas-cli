package googlemaps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

const geocodeBody = `{"status":"OK","results":[{"address_components":[{"long_name":"1600","short_name":"1600",` +
	`"types":["street_number"]}],"formatted_address":"1600 Amphitheatre Pkwy, Mountain View, CA 94043, USA",` +
	`"geometry":{"location":{"lat":37.4224,"lng":-122.0842},"location_type":"ROOFTOP",` +
	`"viewport":{"northeast":{"lat":37.4238,"lng":-122.0829},"southwest":{"lat":37.4211,"lng":-122.0856}},` +
	`"bounds":{"northeast":{"lat":1,"lng":2},"southwest":{"lat":0,"lng":1}}},"place_id":"` + placeID + `",` +
	`"plus_code":{"global_code":"849VCWC8+R9","compound_code":"CWC8+R9 Mountain View"},` +
	`"types":["street_address"],"partial_match":true,"postcode_localities":["A","B"],` +
	`"navigation_points":[{"location":{"latitude":1}}],"address_descriptor":{"landmarks":[{"place_id":"x",` +
	`"display_name":{"text":"Landmark","language_code":"en"},"straight_line_distance_meters":12.5,` +
	`"spatial_relationship":"NEAR","types":["park"]}],"areas":[{"place_id":"y","containment":"WITHIN"}]},` +
	`"unknownFuture":1}]}`

func geocodeEnv(t *testing.T, body string) *environment {
	return newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json; charset=UTF-8", body)})
}

func lastQuery(t *testing.T, env *environment) (recorded, url.Values) {
	t.Helper()
	requests := env.fake.recorded()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want one", len(requests))
	}
	query, err := url.ParseQuery(requests[0].rawQuery)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	return requests[0], query
}

func TestGeocodeWithAddress(t *testing.T) {
	env := geocodeEnv(t, geocodeBody)
	result, err := env.invokeTool(geocodingGeocodeID, "read", `{"address":"1600 Amphitheatre Pkwy & more","language":"en",`+
		`"region":"US","bounds":{"southwest":{"latitude":36.5,"longitude":-123},"northeast":{"latitude":38,"longitude":-121.5}},`+
		`"address_descriptors":true}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	r, query := lastQuery(t, env)
	if r.method != http.MethodGet || r.path != "/maps/api/geocode/json" || r.header.Get("X-Test-Original-Host") != "maps.googleapis.com" {
		t.Errorf("request = %+v", r)
	}
	for name, want := range map[string]string{"address": "1600 Amphitheatre Pkwy & more", "language": "en", "region": "us",
		"bounds": "36.5,-123|38,-121.5", "extra_computations": "ADDRESS_DESCRIPTORS", "key": keyValue} {
		if query.Get(name) != want {
			t.Errorf("query %s = %q, want %q", name, query.Get(name), want)
		}
	}
	if r.header.Get("X-Goog-Api-Key") != "" {
		t.Error("the legacy request carries the key header")
	}
	var got struct {
		Status  string           `json:"status"`
		Results []map[string]any `json:"results"`
	}
	if json.Unmarshal([]byte(result), &got) != nil || got.Status != "OK" || len(got.Results) != 1 {
		t.Fatalf("result = %s", result)
	}
	first := got.Results[0]
	for _, name := range []string{"address_components", "formatted_address", "geometry", "place_id", "plus_code", "types",
		"partial_match", "postcode_localities", "address_descriptor"} {
		if _, ok := first[name]; !ok {
			t.Errorf("result lacks %s: %s", name, result)
		}
	}
	for _, name := range []string{"navigation_points", "unknownFuture"} {
		if _, ok := first[name]; ok {
			t.Errorf("result keeps %s", name)
		}
	}
	if geometry := first["geometry"].(map[string]any); geometry["location_type"] != "ROOFTOP" || geometry["bounds"] == nil ||
		geometry["viewport"] == nil {
		t.Errorf("geometry = %v", geometry)
	}
	if strings.Contains(result, keyValue) {
		t.Error("the key reached the result")
	}
}

func TestGeocodeWithComponentsAndAddress(t *testing.T) {
	env := geocodeEnv(t, geocodeBody)
	if _, err := env.invokeTool(geocodingGeocodeID, "read", `{"components":[{"type":"locality","value":"Santa Cruz"},`+
		`{"type":"country","value":"ES"}]}`); err != nil {
		t.Fatalf("components: %v", err)
	}
	if _, query := lastQuery(t, env); query.Get("components") != "locality:Santa Cruz|country:ES" || query.Has("address") {
		t.Errorf("query = %v", query)
	}
	env = geocodeEnv(t, geocodeBody)
	if _, err := env.invokeTool(geocodingGeocodeID, "read", `{"address":"Main St","components":[{"type":"postal_code","value":"94043"}]}`); err != nil {
		t.Fatalf("address with components: %v", err)
	}
	if _, query := lastQuery(t, env); query.Get("components") != "postal_code:94043" || query.Get("address") != "Main St" {
		t.Errorf("query = %v", query)
	}
}

func TestGeocodeWithPlaceID(t *testing.T) {
	env := geocodeEnv(t, geocodeBody)
	if _, err := env.invokeTool(geocodingGeocodeID, "read", `{"place_id":"`+placeID+`","language":"de"}`); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if _, query := lastQuery(t, env); query.Get("place_id") != placeID || query.Get("language") != "de" || query.Has("address") {
		t.Errorf("query = %v", query)
	}
}

func TestGeocodeRefusalsNeverReachTheKeyOrTheNetwork(t *testing.T) {
	country := `{"type":"country","value":"ES"}`
	for name, arguments := range map[string]string{
		"nothing":                 `{}`,
		"address and place id":    `{"address":"x","place_id":"` + placeID + `"}`,
		"components and place id": `{"components":[` + country + `],"place_id":"` + placeID + `"}`,
		"bounds with place id":    `{"place_id":"` + placeID + `","bounds":{"southwest":{"latitude":1,"longitude":1},"northeast":{"latitude":2,"longitude":2}}}`,
		"blank address":           `{"address":"   "}`,
		"control in address":      `{"address":"a\nb"}`,
		"pipe in value":           `{"components":[{"type":"locality","value":"a|country:DE"}]}`,
		"colon in value":          `{"components":[{"type":"locality","value":"a:b"}]}`,
		"unknown type":            `{"components":[{"type":"street","value":"a"}]}`,
		"country twice":           `{"components":[` + country + `,{"type":"country","value":"DE"}]}`,
		"route twice":             `{"components":[{"type":"route","value":"a"},{"type":"route","value":"b"}]}`,
		"empty components":        `{"components":[]}`,
		"bad place id":            `{"place_id":"a/b"}`,
		"bad language":            `{"address":"x","language":"en us"}`,
		"bad region":              `{"address":"x","region":"USA"}`,
		"bounds south of north":   `{"address":"x","bounds":{"southwest":{"latitude":5,"longitude":1},"northeast":{"latitude":2,"longitude":2}}}`,
		"bounds out of range":     `{"address":"x","bounds":{"southwest":{"latitude":-91,"longitude":1},"northeast":{"latitude":2,"longitude":2}}}`,
		"extra":                   `{"address":"x","key":"y"}`,
		"computation":             `{"address":"x","extra_computations":"BUILDING_AND_ENTRANCES"}`,
	} {
		env := newEnvironment(t, &fakeGoogle{})
		if _, err := env.invokeTool(geocodingGeocodeID, "read", arguments); err == nil {
			t.Errorf("%s: no error", name)
		}
		if len(env.fake.recorded()) != 0 || *env.reads != 0 {
			t.Errorf("%s: %d requests, %d secret reads", name, len(env.fake.recorded()), *env.reads)
		}
	}
	// The handlers check on their own what the schema checks first.
	resolved := &config.Resolved{BaseURL: apiRoot}
	for _, arguments := range []string{`{}`, `{"address":"x","place_id":"y"}`, `{"components":[{"type":"locality","value":"a|b"}]}`} {
		_, err := handleGeocode(context.Background(), resolved, nil, nil, json.RawMessage(arguments))
		var invalid *provider.InvalidRequestError
		if !errors.As(err, &invalid) {
			t.Errorf("%s: err = %v, want an invalid request before any secret access", arguments, err)
		}
	}
	_, err := handleReverse(context.Background(), resolved, nil, nil, json.RawMessage(`{"latlng":{"latitude":91,"longitude":0}}`))
	var invalid *provider.InvalidRequestError
	if !errors.As(err, &invalid) {
		t.Errorf("reverse: err = %v", err)
	}
}

func TestGeocodeEncodesValuesInTheQuery(t *testing.T) {
	env := geocodeEnv(t, geocodeBody)
	if _, err := env.invokeTool(geocodingGeocodeID, "read", `{"address":"a&key=evil#x"}`); err != nil {
		t.Fatal(err)
	}
	r, query := lastQuery(t, env)
	if query.Get("address") != "a&key=evil#x" || query.Get("key") != keyValue || len(query["key"]) != 1 {
		t.Errorf("query = %v (%s)", query, r.rawQuery)
	}
}

func TestReverse(t *testing.T) {
	env := geocodeEnv(t, `{"status":"OK","results":[{"formatted_address":"X","place_id":"p"}],`+
		`"address_descriptor":{"landmarks":[{"place_id":"l"}]}}`)
	result, err := env.invokeTool(geocodingReverseID, "read", `{"latlng":{"latitude":40.714224,"longitude":-73.961452},`+
		`"result_type":["street_address","route"],"location_type":["ROOFTOP","RANGE_INTERPOLATED"],"language":"en","region":"us"}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	r, query := lastQuery(t, env)
	for name, want := range map[string]string{"latlng": "40.714224,-73.961452", "result_type": "street_address|route",
		"location_type": "ROOFTOP|RANGE_INTERPOLATED", "language": "en", "region": "us", "key": keyValue} {
		if query.Get(name) != want {
			t.Errorf("query %s = %q, want %q", name, query.Get(name), want)
		}
	}
	if r.path != "/maps/api/geocode/json" || !strings.Contains(result, `"address_descriptor"`) || !strings.Contains(result, `"formatted_address":"X"`) {
		t.Errorf("path = %s, result = %s", r.path, result)
	}
}

func TestReverseRefusals(t *testing.T) {
	for name, arguments := range map[string]string{
		"missing":          `{}`,
		"latitude high":    `{"latlng":{"latitude":90.5,"longitude":0}}`,
		"latitude low":     `{"latlng":{"latitude":-91,"longitude":0}}`,
		"longitude high":   `{"latlng":{"latitude":0,"longitude":180.1}}`,
		"longitude low":    `{"latlng":{"latitude":0,"longitude":-181}}`,
		"no longitude":     `{"latlng":{"latitude":0}}`,
		"bad result type":  `{"latlng":{"latitude":0,"longitude":0},"result_type":["a|b"]}`,
		"bad location":     `{"latlng":{"latitude":0,"longitude":0},"location_type":["rooftop"]}`,
		"empty result":     `{"latlng":{"latitude":0,"longitude":0},"result_type":[]}`,
		"latlng as string": `{"latlng":"1,2"}`,
	} {
		env := newEnvironment(t, &fakeGoogle{})
		if _, err := env.invokeTool(geocodingReverseID, "read", arguments); err == nil {
			t.Errorf("%s: no error", name)
		}
		if len(env.fake.recorded()) != 0 || *env.reads != 0 {
			t.Errorf("%s: %d requests, %d secret reads", name, len(env.fake.recorded()), *env.reads)
		}
	}
	env := geocodeEnv(t, `{"status":"OK","results":[]}`)
	if _, err := env.invokeTool(geocodingReverseID, "read", `{"latlng":{"latitude":90,"longitude":-180}}`); err != nil {
		t.Errorf("the border values are valid: %v", err)
	}
}

// The handler returns an empty list; the core leaves an empty member out of the published result.
func TestZeroResultsIsAnEmptyList(t *testing.T) {
	for _, body := range []string{`{"status":"ZERO_RESULTS","results":[]}`, `{"status":"ZERO_RESULTS"}`} {
		env := geocodeEnv(t, body)
		resolver := secret.NewWith(func(string) string { return keyValue }, nil, nil, &redact.Redactor{})
		resolved := &config.Resolved{BaseURL: apiRoot, Credential: "maps-key", Secrets: coreConfig().Credentials["maps-key"]}
		got, err := handleGeocode(context.Background(), resolved, resolver, &redact.Redactor{}, json.RawMessage(`{"address":"nowhere"}`))
		answer, _ := got.(map[string]any)
		results, ok := answer["results"].([]any)
		if err != nil || !ok || len(results) != 0 || answer["status"] != "ZERO_RESULTS" {
			t.Errorf("%s: result = %v, err = %v", body, got, err)
		}
		if result, err := env.invokeTool(geocodingGeocodeID, "read", `{"address":"nowhere"}`); err != nil ||
			!strings.Contains(result, "ZERO_RESULTS") {
			t.Errorf("%s: invoke = %s, %v", body, result, err)
		}
	}
}

func TestGeocodeStatusInBodyMapsToClasses(t *testing.T) {
	for status, class := range map[string]provider.Class{
		"OVER_QUERY_LIMIT": provider.ClassRateLimited, "OVER_DAILY_LIMIT": provider.ClassPermission,
		"REQUEST_DENIED": provider.ClassAuth, "INVALID_REQUEST": provider.ClassInvalidResponse,
		"UNKNOWN_ERROR": provider.ClassProviderError, "SOMETHING_NEW": provider.ClassInvalidResponse,
	} {
		env := geocodeEnv(t, `{"status":"`+status+`","results":[],"error_message":"`+detailCanary+` `+keyValue+`"}`)
		_, err := env.invokeTool(geocodingGeocodeID, "read", `{"address":"x"}`)
		var failed *provider.Error
		if !errors.As(err, &failed) || failed.Class != class {
			t.Errorf("%s: err = %v, want class %q", status, err, class)
			continue
		}
		if strings.Contains(err.Error(), detailCanary) || strings.Contains(err.Error(), keyValue) {
			t.Errorf("%s: message carries provider text: %q", status, err.Error())
		}
		if status != "SOMETHING_NEW" && (failed.Original == nil || failed.Original.Entries[0].Message != detailCanary+" "+keyValue) {
			t.Errorf("%s: original = %+v", status, failed.Original)
		}
		if len(env.fake.recorded()) != 1 {
			t.Errorf("%s: %d requests, a request is sent once", status, len(env.fake.recorded()))
		}
	}
}

func TestGeocodeHTTPFailuresAndUnusableAnswers(t *testing.T) {
	for name, tc := range map[string]struct {
		answer func(http.ResponseWriter, *http.Request)
		class  provider.Class
	}{
		"403":      {reply(403, "application/json", `{"error_message":"`+detailCanary+`"}`), provider.ClassPermission},
		"429":      {reply(429, "application/json", `{}`), provider.ClassRateLimited},
		"500":      {reply(500, "text/plain", detailCanary), provider.ClassProviderError},
		"html":     {reply(200, "text/html", `<html>`+detailCanary+`</html>`), provider.ClassInvalidResponse},
		"no json":  {reply(200, "application/json", `{"status":`), provider.ClassInvalidResponse},
		"no state": {reply(200, "application/json", `{"results":[]}`), provider.ClassInvalidResponse},
		"too large": {func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"status":"OK","a":"`+strings.Repeat("x", maxResponseBytes)+`"}`)
		}, provider.ClassInvalidResponse},
		"redirect": {func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://evil.example/?"+r.URL.RawQuery, http.StatusFound)
		}, provider.ClassProviderError},
	} {
		env := newEnvironment(t, &fakeGoogle{answer: tc.answer})
		_, err := env.invokeTool(geocodingGeocodeID, "read", `{"address":"x"}`)
		if classOf(err) != tc.class {
			t.Errorf("%s: err = %v, want class %q", name, err, tc.class)
		}
		if err != nil && (strings.Contains(err.Error(), detailCanary) || strings.Contains(err.Error(), keyValue)) {
			t.Errorf("%s: message carries provider text or the key: %v", name, err)
		}
		if len(env.fake.recorded()) != 1 {
			t.Errorf("%s: requests = %d", name, len(env.fake.recorded()))
		}
	}
}

func TestGeocodeKeyNeverLeaks(t *testing.T) {
	env := geocodeEnv(t, geocodeBody)
	// A transport error quotes the whole URL, as Go's url.Error does.
	for _, cause := range []error{
		fmt.Errorf("Get %q: dial tcp: lookup failed", geocodingRoot+geocodingPath+"?key="+keyValue),
		&url.Error{Op: "Get", URL: geocodingRoot + geocodingPath + "?key=" + keyValue, Err: errors.New("boom " + keyValue)},
		context.DeadlineExceeded,
	} {
		transport = failingTransport{err: cause}
		_, err := env.invokeTool(geocodingGeocodeID, "read", `{"address":"x"}`)
		if err == nil || strings.Contains(err.Error(), keyValue) || strings.Contains(fmt.Sprintf("%+v", err), keyValue) {
			t.Errorf("err = %v", err)
		}
		var failed *provider.Error
		if errors.As(err, &failed) && failed.Original != nil {
			t.Errorf("original = %+v", failed.Original)
		}
	}
	if got := env.red.Apply("a " + keyValue + " b"); strings.Contains(got, keyValue) {
		t.Errorf("redactor leaves the key: %q", got)
	}
}

func TestGeocodeHostAndPathAreFixed(t *testing.T) {
	if geocodingRoot != "https://maps.googleapis.com" || geocodingPath != "/maps/api/geocode/json" {
		t.Fatal("endpoint changed")
	}
	env := geocodeEnv(t, geocodeBody)
	// A configured base URL never moves the geocoding request; a foreign one is refused before anything is read.
	if _, err := env.invokeTool(geocodingGeocodeID, "read", `{"address":"x"}`); err != nil {
		t.Fatal(err)
	}
	if r, query := lastQuery(t, env); r.path != geocodingPath || r.header.Get("X-Test-Original-Host") != "maps.googleapis.com" ||
		query.Get("key") != keyValue || strings.Contains(r.path, keyValue) {
		t.Errorf("request = %+v", r)
	}
	if _, err := handleGeocode(context.Background(), &config.Resolved{BaseURL: "https://evil.example"}, nil, nil,
		json.RawMessage(`{"address":"x"}`)); classOf(err) != provider.ClassProviderError {
		t.Errorf("foreign base URL: err = %v", err)
	}
	if _, err := env.invokeTool(geocodingGeocodeID, "bound", `{"address":"x"}`); err == nil || len(env.fake.recorded()) != 1 {
		t.Errorf("a connection with a target was served: %v", err)
	}
}

func TestGeocodingDescriptions(t *testing.T) {
	for _, descriptor := range []string{geocodingGeocode.Description, geocodingReverse.Description} {
		for _, bad := range []string{"absent", "pages"} {
			if strings.Contains(descriptor, bad) {
				t.Errorf("description contains %q, a search term of the tool listing tests", bad)
			}
		}
		for _, want := range []string{"Essentials", "10000", "attribution", "Place IDs may be stored", "untrusted", "v4beta"} {
			if !strings.Contains(descriptor, want) {
				t.Errorf("description lacks %q", want)
			}
		}
	}
}
