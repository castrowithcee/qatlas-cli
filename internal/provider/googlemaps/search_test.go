package googlemaps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
)

const textArguments = `{"text_query":"pizza in New York","field_mask":["id","displayName"]}`

func decode(t *testing.T, text string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("not JSON: %v: %s", err, text)
	}
	return out
}

func TestTextSearchSendsEveryFilterInTheBody(t *testing.T) {
	env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", `{}`)})
	_, err := env.invokeTool(textSearchID, "read", `{"text_query":"  pizza in New York ","field_mask":"id,displayName",`+
		`"included_type":"restaurant","strict_type_filtering":true,"open_now":true,"min_rating":4.2,`+
		`"price_levels":["PRICE_LEVEL_MODERATE","PRICE_LEVEL_EXPENSIVE","PRICE_LEVEL_MODERATE"],`+
		`"location_bias":{"circle":{"center":{"latitude":40.7,"longitude":-74},"radius_meters":500}},`+
		`"ev_options":{"connector_types":["EV_CONNECTOR_TYPE_NACS"],"minimum_charging_rate_kw":50},`+
		`"rank_preference":"DISTANCE","include_pure_service_area_businesses":false,`+
		`"include_future_opening_businesses":true,`+
		`"routing_parameters":{"origin":{"latitude":40.7,"longitude":-74},"travel_mode":"DRIVE","routing_preference":"TRAFFIC_AWARE"},`+
		`"language_code":"en","region_code":"us","page_size":5}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	requests := env.fake.recorded()
	if len(requests) != 1 {
		t.Fatalf("requests = %d", len(requests))
	}
	r := requests[0]
	if r.method != http.MethodPost || r.path != "/v1/places:searchText" || r.rawQuery != "" ||
		r.header.Get("X-Goog-FieldMask") != "places.id,places.displayName,nextPageToken" ||
		r.header.Get("X-Goog-Api-Key") != keyValue || r.header.Get("Content-Type") != "application/json" {
		t.Errorf("request = %+v", r)
	}
	want := `{"evOptions":{"connectorTypes":["EV_CONNECTOR_TYPE_NACS"],"minimumChargingRateKw":50},` +
		`"includePureServiceAreaBusinesses":false,"includeFutureOpeningBusinesses":true,"includedType":"restaurant",` +
		`"languageCode":"en","locationBias":{"circle":{"center":{"latitude":40.7,"longitude":-74},"radius":500}},` +
		`"minRating":4.2,"openNow":true,"pageSize":5,"priceLevels":["PRICE_LEVEL_MODERATE","PRICE_LEVEL_EXPENSIVE"],` +
		`"rankPreference":"DISTANCE","regionCode":"US","routingParameters":{"origin":{"latitude":40.7,"longitude":-74},` +
		`"routingPreference":"TRAFFIC_AWARE","travelMode":"DRIVE"},"strictTypeFiltering":true,"textQuery":"pizza in New York"}`
	var got, expected map[string]any
	_ = json.Unmarshal([]byte(r.body), &got)
	_ = json.Unmarshal([]byte(want), &expected)
	if fmt.Sprint(got) != fmt.Sprint(expected) {
		t.Errorf("body = %s\nwant   %s", r.body, want)
	}
	if strings.Contains(r.body+r.path+r.rawQuery, keyValue) {
		t.Error("the key reached the body or the URL")
	}
}

func TestTextSearchRectangleRestriction(t *testing.T) {
	env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", `{}`)})
	_, err := env.invokeTool(textSearchID, "read", `{"text_query":"pizza","field_mask":"id","location_restriction":`+
		`{"rectangle":{"low":{"latitude":40,"longitude":-75},"high":{"latitude":41,"longitude":-73}}}}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	body := decode(t, env.fake.recorded()[0].body)
	if fmt.Sprint(body["locationRestriction"]) != "map[rectangle:map[high:map[latitude:41 longitude:-73] low:map[latitude:40 longitude:-75]]]" {
		t.Errorf("body = %v", body)
	}
}

// pagedGoogle answers three pages of 20 places and ends without a token.
func pagedGoogle() *fakeGoogle {
	return &fakeGoogle{answer: func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		page := 0
		switch body["pageToken"] {
		case "token-2":
			page = 1
		case "token-3":
			page = 2
		}
		var places []string
		for i := 0; i < 20; i++ {
			places = append(places, fmt.Sprintf(`{"id":"p%d-%d"}`, page, i))
		}
		next := ""
		if page < 2 {
			next = fmt.Sprintf(`,"nextPageToken":"token-%d"`, page+2)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"places":[%s]%s}`, strings.Join(places, ","), next)
	}}
}

func TestTextSearchPagesUpToSixtyPlaces(t *testing.T) {
	f := pagedGoogle()
	env := newEnvironment(t, f)
	base := `"text_query":"pizza","field_mask":"id","open_now":true`
	arguments := `{` + base + `}`
	total := 0
	for page := 0; page < 3; page++ {
		result, err := env.invokeTool(textSearchID, "read", arguments)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		out := decode(t, result)
		total += len(out["places"].([]any))
		cursor, _ := out["next_cursor"].(string)
		if page < 2 {
			if cursor == "" || out["has_more"] != true || strings.Contains(cursor, "token-") {
				t.Fatalf("page %d: out = %s", page, result)
			}
			// page_size may change; every other argument stays.
			arguments = `{` + base + `,"page_size":20,"cursor":"` + cursor + `"}`
		} else if cursor != "" || out["has_more"] != false {
			t.Fatalf("last page: out = %s", result)
		}
	}
	if total != 60 {
		t.Errorf("places = %d, want 60", total)
	}
	requests := f.recorded()
	if len(requests) != 3 {
		t.Fatalf("requests = %d", len(requests))
	}
	for i, token := range []any{nil, "token-2", "token-3"} {
		body := decode(t, requests[i].body)
		if body["pageToken"] != token || body["openNow"] != true || body["textQuery"] != "pizza" {
			t.Errorf("request %d body = %s", i, requests[i].body)
		}
	}
}

func TestTextSearchCursorIsBoundToTheRequest(t *testing.T) {
	f := pagedGoogle()
	env := newEnvironment(t, f)
	result, err := env.invokeTool(textSearchID, "read", `{"text_query":"pizza","field_mask":"id"}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	cursor := decode(t, result)["next_cursor"].(string)
	altered := cursor[:len(cursor)-2] + "AA"
	for name, arguments := range map[string]string{
		"altered":      `{"text_query":"pizza","field_mask":"id","cursor":"` + altered + `"}`,
		"foreign":      `{"text_query":"pizza","field_mask":"id","cursor":"` + strings.Repeat("A", 40) + `"}`,
		"other query":  `{"text_query":"sushi","field_mask":"id","cursor":"` + cursor + `"}`,
		"other filter": `{"text_query":"pizza","field_mask":"id","open_now":true,"cursor":"` + cursor + `"}`,
		"other mask":   `{"text_query":"pizza","field_mask":"id,displayName","cursor":"` + cursor + `"}`,
		"raw token":    `{"text_query":"pizza","field_mask":"id","cursor":"token-2"}`,
	} {
		before := len(f.recorded())
		_, err := env.invokeTool(textSearchID, "read", arguments)
		var invalid *provider.InvalidRequestError
		if !errors.As(err, &invalid) {
			t.Errorf("%s: err = %v, want an invalid request", name, err)
		}
		if len(f.recorded()) != before {
			t.Errorf("%s: a request was sent", name)
		}
	}
	// The cursor of another tool never fits.
	_, err = handleNearbySearch(context.Background(), &config.Resolved{BaseURL: apiRoot}, nil, nil,
		json.RawMessage(`{"location_restriction":{"circle":{"center":{"latitude":1,"longitude":1},"radius_meters":1}},"field_mask":"id","cursor":"`+cursor+`"}`))
	if err == nil {
		t.Error("a nearby search accepted a cursor")
	}
}

func TestSearchRefusalsNeverReachTheKeyOrTheNetwork(t *testing.T) {
	const circle = `{"circle":{"center":{"latitude":1,"longitude":2},"radius_meters":100}}`
	const rect = `{"rectangle":{"low":{"latitude":1,"longitude":2},"high":{"latitude":3,"longitude":4}}}`
	const rectInverted = `{"rectangle":{"low":{"latitude":5,"longitude":2},"high":{"latitude":3,"longitude":4}}}`
	text := func(extra string) string { return `{"text_query":"pizza","field_mask":"id"` + extra + `}` }
	nearby := func(extra string) string {
		return `{"location_restriction":` + circle + `,"field_mask":"id"` + extra + `}`
	}
	for name, tc := range map[string]struct{ tool, arguments string }{
		"text: no mask":             {textSearchID, `{"text_query":"pizza"}`},
		"text: wildcard":            {textSearchID, `{"text_query":"pizza","field_mask":"*"}`},
		"text: places wildcard":     {textSearchID, `{"text_query":"pizza","field_mask":"places.*"}`},
		"text: places prefix":       {textSearchID, `{"text_query":"pizza","field_mask":"places.id"}`},
		"text: nextPageToken":       {textSearchID, `{"text_query":"pizza","field_mask":"nextPageToken"}`},
		"text: unknown":             {textSearchID, `{"text_query":"pizza","field_mask":"nope"}`},
		"text: no query":            {textSearchID, `{"field_mask":"id"}`},
		"text: blank query":         {textSearchID, `{"text_query":"   ","field_mask":"id"}`},
		"text: control char":        {textSearchID, `{"text_query":"a\u0000b","field_mask":"id"}`},
		"text: page size 0":         {textSearchID, text(`,"page_size":0`)},
		"text: page size 21":        {textSearchID, text(`,"page_size":21`)},
		"text: bias and restr":      {textSearchID, text(`,"location_bias":` + circle + `,"location_restriction":` + rect)},
		"text: restriction circ":    {textSearchID, text(`,"location_restriction":` + circle)},
		"text: bias both":           {textSearchID, text(`,"location_bias":{"circle":` + circle[10:len(circle)-1] + `,"rectangle":` + rect[15:len(rect)-1] + `}`)},
		"text: inverted rect":       {textSearchID, text(`,"location_restriction":` + rectInverted)},
		"text: radius too big":      {textSearchID, text(`,"location_bias":{"circle":{"center":{"latitude":1,"longitude":2},"radius_meters":50001}}`)},
		"text: bad latitude":        {textSearchID, text(`,"location_bias":{"circle":{"center":{"latitude":91,"longitude":2},"radius_meters":5}}`)},
		"text: rating 6":            {textSearchID, text(`,"min_rating":6`)},
		"text: free price":          {textSearchID, text(`,"price_levels":["PRICE_LEVEL_FREE"]`)},
		"text: strict no type":      {textSearchID, text(`,"strict_type_filtering":true`)},
		"text: bad type":            {textSearchID, text(`,"included_type":"Bad Type"`)},
		"text: rank popularity":     {textSearchID, text(`,"rank_preference":"POPULARITY"`)},
		"text: routing no origin":   {textSearchID, `{"text_query":"pizza","field_mask":["id","routingSummaries"]}`},
		"text: traffic walk":        {textSearchID, text(`,"routing_parameters":{"origin":{"latitude":1,"longitude":2},"travel_mode":"WALK","routing_preference":"TRAFFIC_AWARE"}`)},
		"text: empty ev":            {textSearchID, text(`,"ev_options":{}`)},
		"text: bad region":          {textSearchID, text(`,"region_code":"USA"`)},
		"text: extra":               {textSearchID, text(`,"page_token":"x"`)},
		"nearby: no circle":         {nearbySearchID, `{"field_mask":"id"}`},
		"nearby: rectangle":         {nearbySearchID, `{"location_restriction":` + rect + `,"field_mask":"id"}`},
		"nearby: radius 0":          {nearbySearchID, `{"location_restriction":{"circle":{"center":{"latitude":1,"longitude":2},"radius_meters":0}},"field_mask":"id"}`},
		"nearby: radius big":        {nearbySearchID, `{"location_restriction":{"circle":{"center":{"latitude":1,"longitude":2},"radius_meters":50001}},"field_mask":"id"}`},
		"nearby: no mask":           {nearbySearchID, `{"location_restriction":` + circle + `}`},
		"nearby: wildcard":          {nearbySearchID, nearby("")[:len(nearby(""))-len(`"id"}`)] + `"*"}`},
		"nearby: count 0":           {nearbySearchID, nearby(`,"max_result_count":0`)},
		"nearby: count 21":          {nearbySearchID, nearby(`,"max_result_count":21`)},
		"nearby: conflict":          {nearbySearchID, nearby(`,"included_types":["cafe"],"excluded_types":["cafe"]`)},
		"nearby: primary clash":     {nearbySearchID, nearby(`,"included_primary_types":["cafe"],"excluded_primary_types":["cafe"]`)},
		"nearby: rank relevance":    {nearbySearchID, nearby(`,"rank_preference":"RELEVANCE"`)},
		"nearby: cursor":            {nearbySearchID, nearby(`,"cursor":"x"`)},
		"nearby: routing no origin": {nearbySearchID, `{"location_restriction":` + circle + `,"field_mask":"routingSummaries"}`},
	} {
		env := newEnvironment(t, &fakeGoogle{})
		if _, err := env.invokeTool(tc.tool, "read", tc.arguments); err == nil {
			t.Errorf("%s: no error", name)
		}
		if len(env.fake.recorded()) != 0 || *env.reads != 0 {
			t.Errorf("%s: %d requests, %d secret reads", name, len(env.fake.recorded()), *env.reads)
		}
	}
}

func TestSearchHandlersCheckArgumentsThemselves(t *testing.T) {
	resolved := &config.Resolved{BaseURL: apiRoot}
	for tool, arguments := range map[string][]string{
		textSearchID: {`{"text_query":"p","field_mask":"id","page_size":0}`, `{"text_query":"p","field_mask":"id","page_size":21}`,
			`{"text_query":"p","field_mask":"*"}`, `{"text_query":"p"}`,
			`{"text_query":"p","field_mask":"id","location_bias":{"circle":{"center":{"latitude":1,"longitude":1},"radius_meters":1}},` +
				`"location_restriction":{"rectangle":{"low":{"latitude":1,"longitude":1},"high":{"latitude":2,"longitude":2}}}}`},
		nearbySearchID: {`{"field_mask":"id"}`, `{"field_mask":"id","location_restriction":{}}`,
			`{"field_mask":"id","max_result_count":21,"location_restriction":{"circle":{"center":{"latitude":1,"longitude":1},"radius_meters":1}}}`},
	} {
		handler := handleTextSearch
		if tool == nearbySearchID {
			handler = handleNearbySearch
		}
		for _, text := range arguments {
			_, err := handler(context.Background(), resolved, nil, nil, json.RawMessage(text))
			var invalid *provider.InvalidRequestError
			if !errors.As(err, &invalid) {
				t.Errorf("%s %s: err = %v, want an invalid request before any secret access", tool, text, err)
			}
		}
	}
}

func TestNearbySearchSendsTheCircleAndFilters(t *testing.T) {
	env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json",
		`{"places":[{"id":"a","displayName":{"text":"Cafe","languageCode":"en"},"extra":1}]}`)})
	result, err := env.invokeTool(nearbySearchID, "read", `{"location_restriction":{"circle":{"center":`+
		`{"latitude":37.79,"longitude":-122.39},"radius_meters":500}},"field_mask":["id","displayName"],`+
		`"included_types":["restaurant","restaurant","cafe"],"excluded_types":["bar"],`+
		`"included_primary_types":["restaurant"],"excluded_primary_types":["bar"],"max_result_count":7,`+
		`"rank_preference":"DISTANCE","include_future_opening_businesses":false,"language_code":"de","region_code":"de"}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	r := env.fake.recorded()[0]
	if r.method != http.MethodPost || r.path != "/v1/places:searchNearby" || r.rawQuery != "" ||
		r.header.Get("X-Goog-FieldMask") != "places.id,places.displayName" {
		t.Errorf("request = %+v", r)
	}
	want := map[string]any{"locationRestriction": map[string]any{"circle": map[string]any{
		"center": map[string]any{"latitude": 37.79, "longitude": -122.39}, "radius": 500.0}},
		"includedTypes": []any{"restaurant", "cafe"}, "excludedTypes": []any{"bar"},
		"includedPrimaryTypes": []any{"restaurant"}, "excludedPrimaryTypes": []any{"bar"}, "maxResultCount": 7.0,
		"rankPreference": "DISTANCE", "includeFutureOpeningBusinesses": false, "languageCode": "de", "regionCode": "DE"}
	if got := decode(t, r.body); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("body = %s", r.body)
	}
	out := decode(t, result)
	if out["sku"] != "Nearby Search Pro" || out["next_cursor"] != nil || out["has_more"] != nil {
		t.Errorf("result = %s", result)
	}
	place := out["places"].([]any)[0].(map[string]any)
	if _, leaked := place["extra"]; leaked || place["id"] != "a" {
		t.Errorf("place = %v", place)
	}
}

func TestSearchWithoutHitsIsAnEmptyList(t *testing.T) {
	// The handler result holds an empty list; the core leaves empty members out when it renders a result.
	out, err := searchResult("search", map[string]any{}, fieldMask{tier: TierIDsOnly}, maskTextSearch)
	if err != nil {
		t.Fatalf("searchResult: %v", err)
	}
	if places, ok := out["places"].([]any); !ok || places == nil || len(places) != 0 {
		t.Errorf("places = %#v, want an empty list", out["places"])
	}
	for _, tool := range []struct{ id, arguments string }{
		{textSearchID, textArguments},
		{nearbySearchID, `{"location_restriction":{"circle":{"center":{"latitude":1,"longitude":2},"radius_meters":10}},"field_mask":"id"}`},
	} {
		env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", `{}`)})
		result, err := env.invokeTool(tool.id, "read", tool.arguments)
		if err != nil {
			t.Fatalf("%s: %v", tool.id, err)
		}
		if places, _ := decode(t, result)["places"].([]any); len(places) != 0 {
			t.Errorf("%s: result = %s", tool.id, result)
		}
	}
}

func TestSearchReturnsRoutingSummariesAtTheIndexOfTheirPlace(t *testing.T) {
	env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json",
		`{"places":[{"id":"a"},{"id":"b"}],"routingSummaries":[{"legs":[{"duration":"300s","distanceMeters":1200}],`+
			`"directionsUri":"https://www.google.com/maps/dir/x","junk":1},{}]}`)})
	result, err := env.invokeTool(textSearchID, "read", `{"text_query":"pizza","field_mask":["id","routingSummaries"],`+
		`"routing_parameters":{"origin":{"latitude":1,"longitude":2},"travel_mode":"WALK"}}`)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	r := env.fake.recorded()[0]
	if r.header.Get("X-Goog-FieldMask") != "places.id,routingSummaries,nextPageToken" {
		t.Errorf("mask = %q", r.header.Get("X-Goog-FieldMask"))
	}
	out := decode(t, result)
	summaries := out["routingSummaries"].([]any)
	if len(summaries) != 2 || out["sku"] != "Text Search Enterprise + Atmosphere" {
		t.Fatalf("result = %s", result)
	}
	first := summaries[0].(map[string]any)
	leg := first["legs"].([]any)[0].(map[string]any)
	if leg["duration"] != "300s" || leg["distanceMeters"] != 1200.0 || first["junk"] != nil ||
		len(summaries[1].(map[string]any)) != 0 {
		t.Errorf("summaries = %v", summaries)
	}
	// Without the member in the mask, no summaries are returned or sent.
	env = newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", `{"places":[{"id":"a"}],"routingSummaries":[{}]}`)})
	result, _ = env.invokeTool(textSearchID, "read", textArguments)
	if decode(t, result)["routingSummaries"] != nil {
		t.Errorf("result = %s", result)
	}
}

func TestSearchMaskGroupsAndHeaders(t *testing.T) {
	for _, tc := range []struct {
		kind    maskKind
		raw     string
		header  string
		sku     string
		routing bool
	}{
		{maskTextSearch, `"id"`, "places.id,nextPageToken", "Text Search Essentials (IDs Only)", false},
		{maskTextSearch, `"id,photos"`, "places.id,places.photos,nextPageToken", "Text Search Pro", false},
		{maskTextSearch, `["id","formattedAddress"]`, "places.id,places.formattedAddress,nextPageToken", "Text Search Pro", false},
		{maskTextSearch, `["id","rating","id"]`, "places.id,places.rating,nextPageToken", "Text Search Enterprise", false},
		{maskTextSearch, `["reviews"]`, "places.reviews,nextPageToken", "Text Search Enterprise + Atmosphere", false},
		{maskTextSearch, `["id","routingSummaries"]`, "places.id,routingSummaries,nextPageToken", "Text Search Enterprise + Atmosphere", true},
		{maskNearbySearch, `"id"`, "places.id", "Nearby Search Pro", false},
		{maskNearbySearch, `["rating"]`, "places.rating", "Nearby Search Enterprise", false},
		{maskNearbySearch, `["servesWine","routingSummaries"]`, "places.servesWine,routingSummaries", "Nearby Search Enterprise + Atmosphere", true},
	} {
		mask, err := parseMask(json.RawMessage(tc.raw), tc.kind)
		if err != nil || mask.header != tc.header || searchSKU(tc.kind, mask.tier) != tc.sku || mask.routing != tc.routing {
			t.Errorf("%s: mask = %+v, sku = %q, err = %v", tc.raw, mask, searchSKU(tc.kind, mask.tier), err)
		}
	}
	// Place Details keeps its own groups and refuses the search member.
	if mask, err := parseFieldMask(json.RawMessage(`"id,formattedAddress"`)); err != nil || mask.header != "id,formattedAddress" ||
		mask.tier != TierEssentials {
		t.Errorf("details mask = %+v, err = %v", mask, err)
	}
	if _, err := parseFieldMask(json.RawMessage(`"routingSummaries"`)); err == nil {
		t.Error("place details accepted routingSummaries")
	}
}

func TestSearchErrorsMapLikePlaceDetails(t *testing.T) {
	for _, tool := range []struct{ id, arguments string }{
		{textSearchID, textArguments},
		{nearbySearchID, `{"location_restriction":{"circle":{"center":{"latitude":1,"longitude":2},"radius_meters":10}},"field_mask":"id"}`},
	} {
		for _, tc := range []struct {
			status        int
			state, reason string
			class         provider.Class
		}{
			{401, "UNAUTHENTICATED", "CREDENTIALS_MISSING", provider.ClassAuth},
			{403, "PERMISSION_DENIED", "", provider.ClassPermission},
			{429, "RESOURCE_EXHAUSTED", "RATE_LIMIT_EXCEEDED", provider.ClassRateLimited},
			{400, "INVALID_ARGUMENT", "", provider.ClassInvalidResponse},
		} {
			env := newEnvironment(t, &fakeGoogle{answer: reply(tc.status, "application/json", errorObject(tc.status, tc.state, tc.reason))})
			_, err := env.invokeTool(tool.id, "read", tool.arguments)
			var failed *provider.Error
			if !errors.As(err, &failed) || failed.Class != tc.class || failed.Original == nil {
				t.Fatalf("%s HTTP %d: err = %v, want class %q", tool.id, tc.status, err, tc.class)
			}
			if strings.Contains(err.Error(), detailCanary) || strings.Contains(err.Error(), keyValue) {
				t.Errorf("%s HTTP %d: message %q carries provider text", tool.id, tc.status, err.Error())
			}
			if len(env.fake.recorded()) != 1 {
				t.Errorf("%s HTTP %d: %d requests, a request is sent once", tool.id, tc.status, len(env.fake.recorded()))
			}
		}
	}
}

func TestSearchRefusesAnOverlongPageToken(t *testing.T) {
	env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json",
		`{"places":[],"nextPageToken":"`+strings.Repeat("a", maxPageTokenBytes+1)+`"}`)})
	_, err := env.invokeTool(textSearchID, "read", textArguments)
	if classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
}

func TestSearchRefusesAMalformedPlacesMember(t *testing.T) {
	env := newEnvironment(t, &fakeGoogle{answer: reply(200, "application/json", `{"places":"x"}`)})
	_, err := env.invokeTool(textSearchID, "read", textArguments)
	if classOf(err) != provider.ClassInvalidResponse {
		t.Errorf("err = %v", err)
	}
}
