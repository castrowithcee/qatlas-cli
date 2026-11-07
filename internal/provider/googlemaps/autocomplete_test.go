package googlemaps

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

const (
	photoName = "places/" + placeID + "/photos/Aaw_FcKly0DEv3EWmDJyHiEqXIP5mowOc99lN1GzBun6KHH52AZ5fFA"
	photoURI  = "https://lh3.googleusercontent.com/places/ABC=s400"
)

const suggestionsAnswer = `{"suggestions":[` +
	`{"placePrediction":{"place":"places/` + placeID + `","placeId":"` + placeID + `",` +
	`"text":{"text":"Pizza Place, New York, NY, USA","matches":[{"endOffset":5}]},` +
	`"structuredFormat":{"mainText":{"text":"Pizza Place","matches":[{"startOffset":0,"endOffset":5}]},` +
	`"secondaryText":{"text":"New York, NY, USA"}},"types":["restaurant","food"],"distanceMeters":1234,` +
	`"unknown":"dropped"}},` +
	`{"queryPrediction":{"text":{"text":"pizza near me","matches":[{"endOffset":5}]},` +
	`"structuredFormat":{"mainText":{"text":"pizza","matches":[{"endOffset":5}]}}}}]}`

func TestAutocompleteSendsTheBodyWithAndWithoutSessionToken(t *testing.T) {
	env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", suggestionsAnswer)})
	result, err := env.invokeTool(autocompleteID, "read", `{"input":" pizza","session_token":"abc-DEF_123",`+
		`"included_primary_types":["restaurant","restaurant","cafe"],"included_region_codes":["us","US","ca"],`+
		`"location_bias":{"circle":{"center":{"latitude":40.7,"longitude":-74},"radius_meters":500}},`+
		`"input_offset":3,"origin":{"latitude":40.7,"longitude":-74},"language_code":"en","region_code":"us",`+
		`"include_query_predictions":true,"include_pure_service_area_businesses":false,`+
		`"include_future_opening_businesses":true}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	requests := env.fake.recorded()
	if len(requests) != 1 {
		t.Fatalf("requests = %d", len(requests))
	}
	r := requests[0]
	if r.method != http.MethodPost || r.path != "/v1/places:autocomplete" || r.rawQuery != "" ||
		r.header.Get("X-Goog-Api-Key") != keyValue || r.header.Get("Content-Type") != "application/json" {
		t.Errorf("request = %+v", r)
	}
	if _, has := r.header["X-Goog-Fieldmask"]; has {
		t.Error("autocomplete sent a field mask")
	}
	want := `{"includeFutureOpeningBusinesses":true,"includePureServiceAreaBusinesses":false,` +
		`"includeQueryPredictions":true,"includedPrimaryTypes":["restaurant","cafe"],` +
		`"includedRegionCodes":["US","CA"],"input":" pizza","inputOffset":3,"languageCode":"en",` +
		`"locationBias":{"circle":{"center":{"latitude":40.7,"longitude":-74},"radius":500}},` +
		`"origin":{"latitude":40.7,"longitude":-74},"regionCode":"US","sessionToken":"abc-DEF_123"}`
	if r.body != want {
		t.Errorf("body = %s\nwant   %s", r.body, want)
	}
	if strings.Contains(r.path+r.rawQuery+r.body+result, keyValue) {
		t.Error("the key reached the URL, the body, or the result")
	}

	env = newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", suggestionsAnswer)})
	if _, err := env.invokeTool(autocompleteID, "read", `{"input":"pizza"}`); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if body := env.fake.recorded()[0].body; body != `{"input":"pizza"}` {
		t.Errorf("body without a token = %s", body)
	}
}

func TestAutocompleteRestrictionRectangleAndGroupType(t *testing.T) {
	env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", `{}`)})
	_, err := env.invokeTool(autocompleteID, "read", `{"input":"x","included_primary_types":["(cities)"],`+
		`"location_restriction":{"rectangle":{"low":{"latitude":1,"longitude":2},"high":{"latitude":3,"longitude":4}}}}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	want := `{"includedPrimaryTypes":["(cities)"],"input":"x","locationRestriction":{"rectangle":` +
		`{"high":{"latitude":3,"longitude":4},"low":{"latitude":1,"longitude":2}}}}`
	if body := env.fake.recorded()[0].body; body != want {
		t.Errorf("body = %s", body)
	}
}

func TestAutocompleteReturnsPlaceAndQueryPredictions(t *testing.T) {
	env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", suggestionsAnswer)})
	result, err := env.invokeTool(autocompleteID, "read", `{"input":"pizza","include_query_predictions":true}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	var got struct {
		Suggestions []map[string]map[string]any `json:"suggestions"`
	}
	if json.Unmarshal([]byte(result), &got) != nil || len(got.Suggestions) != 2 {
		t.Fatalf("result = %s", result)
	}
	place := got.Suggestions[0]["placePrediction"]
	if place["placeId"] != placeID || place["place"] != "places/"+placeID || place["distanceMeters"] != float64(1234) ||
		len(place["types"].([]any)) != 2 || place["unknown"] != nil {
		t.Errorf("place prediction = %v", place)
	}
	main := place["structuredFormat"].(map[string]any)["mainText"].(map[string]any)
	if main["text"] != "Pizza Place" || main["matches"].([]any)[0].(map[string]any)["endOffset"] != float64(5) {
		t.Errorf("structured format = %v", place["structuredFormat"])
	}
	query := got.Suggestions[1]["queryPrediction"]
	if query["text"].(map[string]any)["text"] != "pizza near me" || query["structuredFormat"] == nil {
		t.Errorf("query prediction = %v", query)
	}
}

func TestAutocompleteWithoutSuggestionsIsAnEmptyList(t *testing.T) {
	env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", `{}`)})
	// The handler result holds an empty list; the core leaves empty members out when it renders a result.
	out, err := autocompleteResult("autocomplete", map[string]any{})
	if list, ok := out["suggestions"].([]any); err != nil || !ok || list == nil || len(list) != 0 {
		t.Errorf("out = %#v, err = %v", out, err)
	}
	if _, err := env.invokeTool(autocompleteID, "read", `{"input":"zzzz"}`); err != nil {
		t.Errorf("err = %v", err)
	}
	env = newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", `{"suggestions":"x"}`)})
	if _, err := env.invokeTool(autocompleteID, "read", `{"input":"zzzz"}`); classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("malformed suggestions: err = %v", err)
	}
}

func TestAutocompleteRefusalsNeverReachTheKeyOrTheNetwork(t *testing.T) {
	const circle = `{"circle":{"center":{"latitude":1,"longitude":2},"radius_meters":100}}`
	const rect = `{"rectangle":{"low":{"latitude":1,"longitude":2},"high":{"latitude":3,"longitude":4}}}`
	for name, arguments := range map[string]string{
		"no input":        `{}`,
		"empty input":     `{"input":""}`,
		"blank input":     `{"input":"   "}`,
		"control char":    `{"input":"a\u0000b"}`,
		"long input":      `{"input":"` + strings.Repeat("a", 1001) + `"}`,
		"bias and restr":  `{"input":"x","location_bias":` + circle + `,"location_restriction":` + rect + `}`,
		"bias both":       `{"input":"x","location_bias":{"circle":` + circle[10:len(circle)-1] + `,"rectangle":` + rect[15:len(rect)-1] + `}}`,
		"empty area":      `{"input":"x","location_restriction":{}}`,
		"radius too big":  `{"input":"x","location_restriction":{"circle":{"center":{"latitude":1,"longitude":2},"radius_meters":50001}}}`,
		"inverted rect":   `{"input":"x","location_restriction":{"rectangle":{"low":{"latitude":5,"longitude":2},"high":{"latitude":3,"longitude":4}}}}`,
		"bad token":       `{"input":"x","session_token":"a b"}`,
		"long token":      `{"input":"x","session_token":"` + strings.Repeat("a", 37) + `"}`,
		"padded token":    `{"input":"x","session_token":"abc="}`,
		"offset too far":  `{"input":"abc","input_offset":4}`,
		"offset negative": `{"input":"abc","input_offset":-1}`,
		"six types":       `{"input":"x","included_primary_types":["a","b","c","d","e","f"]}`,
		"bad type":        `{"input":"x","included_primary_types":["Bad Type"]}`,
		"group mixed":     `{"input":"x","included_primary_types":["(regions)","cafe"]}`,
		"bad group":       `{"input":"x","included_primary_types":["(everything)"]}`,
		"sixteen regions": `{"input":"x","included_region_codes":["aa","ab","ac","ad","ae","af","ag","ah","ai","aj","ak","al","am","an","ao","ap"]}`,
		"bad region list": `{"input":"x","included_region_codes":["USA"]}`,
		"bad origin":      `{"input":"x","origin":{"latitude":91,"longitude":0}}`,
		"bad language":    `{"input":"x","language_code":"en us"}`,
		"bad region":      `{"input":"x","region_code":"USA"}`,
		"field mask":      `{"input":"x","field_mask":"id"}`,
		"extra":           `{"input":"x","key":"y"}`,
	} {
		env := newEnvironment(t, &fakeGoogle{})
		if _, err := env.invokeTool(autocompleteID, "read", arguments); err == nil {
			t.Errorf("%s: no error", name)
		}
		if len(env.fake.recorded()) != 0 || *env.reads != 0 {
			t.Errorf("%s: %d requests, %d secret reads", name, len(env.fake.recorded()), *env.reads)
		}
	}
}

func TestAutocompleteAndPhotoHandlersCheckArgumentsThemselves(t *testing.T) {
	resolved := &config.Resolved{BaseURL: apiRoot}
	for _, tc := range []struct {
		handler   func(context.Context, *config.Resolved, *secret.Resolver, *redact.Redactor, json.RawMessage) (any, error)
		arguments string
	}{
		{handleAutocomplete, `{"input":""}`},
		{handleAutocomplete, `{"input":"x","location_bias":{"circle":{"center":{"latitude":1,"longitude":1},"radius_meters":1}},` +
			`"location_restriction":{"circle":{"center":{"latitude":1,"longitude":1},"radius_meters":1}}}`},
		{handlePhotosGet, `{"name":"` + photoName + `"}`},
		{handlePhotosGet, `{"name":"places/a/../b/photos/c","max_width_px":10}`},
		{handlePhotosGet, `{"name":"` + photoName + `","max_width_px":4801}`},
	} {
		_, err := tc.handler(context.Background(), resolved, nil, nil, json.RawMessage(tc.arguments))
		var invalid *provider.InvalidRequestError
		if !errors.As(err, &invalid) {
			t.Errorf("%s: err = %v, want an invalid request before any secret access", tc.arguments, err)
		}
	}
}

func photoAnswer() *fakeGoogle {
	return &fakeGoogle{answer: reply(200, "application/json",
		`{"name":"`+photoName+`/media","photoUri":"`+photoURI+`"}`)}
}

func TestPhotoWithWidthAsksForJSONAndReturnsTheAddress(t *testing.T) {
	env := newEnvironment(t, photoAnswer())
	result, err := env.invokeTool(photosGetID, "read", `{"name":"`+photoName+`","max_width_px":400}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	got := decode(t, result)
	if got["photoUri"] != photoURI || got["name"] != photoName+"/media" || len(got) != 2 {
		t.Errorf("result = %s", result)
	}
	requests := env.fake.recorded()
	if len(requests) != 1 {
		t.Fatalf("requests = %d, the image address is never fetched", len(requests))
	}
	r := requests[0]
	if r.method != http.MethodGet || r.path != "/v1/"+photoName+"/media" ||
		r.rawQuery != "maxWidthPx=400&skipHttpRedirect=true" || r.body != "" ||
		r.header.Get("X-Goog-Api-Key") != keyValue {
		t.Errorf("request = %+v", r)
	}
	if _, has := r.header["X-Goog-Fieldmask"]; has {
		t.Error("the photo request sent a field mask")
	}
	if strings.Contains(r.path+r.rawQuery+r.body+result, keyValue) {
		t.Error("the key reached the URL, the body, or the result")
	}

	env = newEnvironment(t, photoAnswer())
	if _, err := env.invokeTool(photosGetID, "read", `{"name":"`+photoName+`","max_width_px":1,"max_height_px":4800}`); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if q := env.fake.recorded()[0].rawQuery; q != "maxHeightPx=4800&maxWidthPx=1&skipHttpRedirect=true" {
		t.Errorf("query = %s", q)
	}
}

func TestPhotoRefusalsNeverReachTheKeyOrTheNetwork(t *testing.T) {
	for name, arguments := range map[string]string{
		"no size":         `{"name":"` + photoName + `"}`,
		"width 0":         `{"name":"` + photoName + `","max_width_px":0}`,
		"width 4801":      `{"name":"` + photoName + `","max_width_px":4801}`,
		"height 0":        `{"name":"` + photoName + `","max_height_px":0}`,
		"height 4801":     `{"name":"` + photoName + `","max_height_px":4801}`,
		"no name":         `{"max_width_px":10}`,
		"empty name":      `{"name":"","max_width_px":10}`,
		"foreign path":    `{"name":"v1/places/a/photos/b","max_width_px":10}`,
		"other resource":  `{"name":"operations/a/photos/b","max_width_px":10}`,
		"dot dot place":   `{"name":"places/../photos/b","max_width_px":10}`,
		"dot dot photo":   `{"name":"places/a/photos/..","max_width_px":10}`,
		"dot dot between": `{"name":"places/a/../photos/b","max_width_px":10}`,
		"extra segment":   `{"name":"` + photoName + `/extra","max_width_px":10}`,
		"media suffix":    `{"name":"` + photoName + `/media","max_width_px":10}`,
		"missing photo":   `{"name":"places/a/photos/","max_width_px":10}`,
		"missing place":   `{"name":"places//photos/b","max_width_px":10}`,
		"photos only":     `{"name":"photos/b","max_width_px":10}`,
		"scheme":          `{"name":"https://places.googleapis.com/` + photoName + `","max_width_px":10}`,
		"leading slash":   `{"name":"/` + photoName + `","max_width_px":10}`,
		"query":           `{"name":"` + photoName + `?key=x","max_width_px":10}`,
		"encoded":         `{"name":"places/a%2Fb/photos/c","max_width_px":10}`,
		"backslash":       `{"name":"places/a\\b/photos/c","max_width_px":10}`,
		"space":           `{"name":"places/a b/photos/c","max_width_px":10}`,
		"long reference":  `{"name":"places/a/photos/` + strings.Repeat("a", 1025) + `","max_width_px":10}`,
		"skip redirect":   `{"name":"` + photoName + `","max_width_px":10,"skip_http_redirect":false}`,
		"extra":           `{"name":"` + photoName + `","max_width_px":10,"key":"x"}`,
	} {
		env := newEnvironment(t, &fakeGoogle{})
		if _, err := env.invokeTool(photosGetID, "read", arguments); err == nil {
			t.Errorf("%s: no error", name)
		}
		if len(env.fake.recorded()) != 0 || *env.reads != 0 {
			t.Errorf("%s: %d requests, %d secret reads", name, len(env.fake.recorded()), *env.reads)
		}
	}
}

func TestPhotoRedirectIsNeverFollowedAndIsAnInvalidResponse(t *testing.T) {
	f := &fakeGoogle{answer: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://lh3.googleusercontent.com/"+detailCanary)
		w.WriteHeader(http.StatusFound)
	}}
	env := newEnvironment(t, f)
	_, err := env.invokeTool(photosGetID, "read", `{"name":"`+photoName+`","max_height_px":100}`)
	if classOf(err) != provider.ClassInvalidResponse {
		t.Fatalf("err = %v, want invalid-provider-response", err)
	}
	if strings.Contains(err.Error(), detailCanary) || strings.Contains(err.Error(), keyValue) {
		t.Errorf("message carries the location or the key: %v", err)
	}
	if len(f.recorded()) != 1 {
		t.Errorf("requests = %d, a redirect is never followed", len(f.recorded()))
	}
}

func TestPhotoAnswerWithoutAUsableAddressIsAnInvalidResponse(t *testing.T) {
	for name, body := range map[string]string{
		"no address":   `{"name":"x"}`,
		"scheme":       `{"photoUri":"javascript:alert(1)"}`,
		"credentials":  `{"photoUri":"https://user:pw@example.com/a"}`,
		"not a string": `{"photoUri":5}`,
		"relative":     `{"photoUri":"/a/b"}`,
		"overlong":     `{"photoUri":"https://example.com/` + strings.Repeat("a", 3000) + `"}`,
	} {
		env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", body)})
		_, err := env.invokeTool(photosGetID, "read", `{"name":"`+photoName+`","max_width_px":10}`)
		if classOf(err) != provider.ClassInvalidResponse {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestPhotoNameOfTheAnswerIsCut(t *testing.T) {
	long := photoName + strings.Repeat("a", 2000)
	env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json",
		`{"name":"`+long+`","photoUri":"`+photoURI+`"}`)})
	result, err := env.invokeTool(photosGetID, "read", `{"name":"`+photoName+`","max_width_px":10}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if name := decode(t, result)["name"].(string); !strings.HasSuffix(name, "…") || len([]rune(name)) != maxPhotoNameRune+1 {
		t.Errorf("name has %d characters", len([]rune(name)))
	}
}

func TestAutocompleteAndPhotoErrorsMapLikePlaceDetails(t *testing.T) {
	tools := map[string]string{
		autocompleteID: `{"input":"pizza","session_token":"tok"}`,
		photosGetID:    `{"name":"` + photoName + `","max_width_px":100}`,
	}
	for tool, arguments := range tools {
		for _, tc := range []struct {
			status        int
			state, reason string
			class         provider.Class
		}{
			{401, "UNAUTHENTICATED", "CREDENTIALS_MISSING", provider.ClassAuth},
			{403, "PERMISSION_DENIED", "", provider.ClassPermission},
			{429, "RESOURCE_EXHAUSTED", "RATE_LIMIT_EXCEEDED", provider.ClassRateLimited},
			{400, "INVALID_ARGUMENT", "", provider.ClassInvalidResponse},
			{404, "NOT_FOUND", "", provider.ClassNotFound},
		} {
			env := newEnvironment(t, &fakeGoogle{answer: reply(tc.status, "application/json", errorObject(tc.status, tc.state, tc.reason))})
			_, err := env.invokeTool(tool, "read", arguments)
			var failed *provider.Error
			if !errors.As(err, &failed) || failed.Class != tc.class || failed.Original == nil || failed.Original.Status != tc.status {
				t.Fatalf("%s HTTP %d: err = %v, want class %q with Original", tool, tc.status, err, tc.class)
			}
			text := err.Error() + failed.Message
			for _, secret := range []string{keyValue, detailCanary, tc.state, "tok"} {
				if strings.Contains(text, secret) {
					t.Errorf("%s HTTP %d: message carries %q", tool, tc.status, secret)
				}
			}
			if len(env.fake.recorded()) != 1 {
				t.Errorf("%s HTTP %d: %d requests, a request is sent once", tool, tc.status, len(env.fake.recorded()))
			}
		}
	}
}

func TestAutocompleteAndPhotoDescriptionsStateCostAndAttribution(t *testing.T) {
	for _, tc := range []struct {
		description string
		wants       []string
	}{
		{autocomplete.Description, []string{"10000", "Autocomplete Session Usage", "places.get that ends it",
			"no field mask", "attribution", "untrusted"}},
		{photosGet.Description, []string{"Places Photo SKU", "Enterprise", "1000", "authorAttributions",
			"never loads the image", "never fetched", "redirect"}},
	} {
		for _, want := range tc.wants {
			if !strings.Contains(tc.description, want) {
				t.Errorf("description lacks %q", want)
			}
		}
		// The tool tests use these words as search terms of fixed expectations.
		for _, bad := range []string{"absent", "pages"} {
			if strings.Contains(tc.description, bad) {
				t.Errorf("description contains %q", bad)
			}
		}
	}
}

func TestLongPhotoNameSurvivesPlaceResultsAndFeedsPhotosGet(t *testing.T) {
	long := "places/" + placeID + "/photos/" + strings.Repeat("Ab_-", 100)
	tooLong := "places/" + strings.Repeat("p", 256) + "/photos/" + strings.Repeat("a", maxPhotoRefBytes+1)
	answer := func(name string) string {
		return `{"id":"` + placeID + `","photos":[{"name":"` + name + `","widthPx":10}]}`
	}
	photoOf := func(t *testing.T, result string) map[string]any {
		t.Helper()
		photos, _ := decode(t, result)["photos"].([]any)
		if len(photos) != 1 {
			t.Fatalf("result = %s", result)
		}
		return photos[0].(map[string]any)
	}
	env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", answer(long))})
	result, err := env.invoke("read", `{"place_id":"`+placeID+`","field_mask":"photos"}`)
	if err != nil || photoOf(t, result)["name"] != long {
		t.Fatalf("details: err = %v, result = %s", err, result)
	}
	env = newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", `{"places":[`+answer(long)+`]}`)})
	result, err = env.invokeTool(textSearchID, "read", `{"text_query":"x","field_mask":"photos"}`)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	places := decode(t, result)["places"].([]any)
	name := photoOf(t, `{"photos":`+mustJSON(t, places[0].(map[string]any)["photos"])+`}`)["name"].(string)
	if name != long {
		t.Fatalf("search name = %q", name)
	}
	env = newEnvironment(t, photoAnswer())
	if _, err := env.invokeTool(photosGetID, "read", `{"name":"`+name+`","max_width_px":10}`); err != nil {
		t.Errorf("photos.get refused the name of a result: %v", err)
	}

	env = newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", answer(tooLong))})
	result, err = env.invoke("read", `{"place_id":"`+placeID+`","field_mask":"photos"}`)
	if err != nil || photoOf(t, result)["name"] != nil {
		t.Errorf("a name beyond the limit must be left out: err = %v, result = %s", err, result)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
