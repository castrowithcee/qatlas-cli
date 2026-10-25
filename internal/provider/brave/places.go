package brave

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-cli/internal/capability"
)

const (
	placesPath = "/res/v1/local/place_search"
	// Limits the place search reference documents or implies.
	placesMaxQueryRunes = 400
	placesMaxQueryWords = 50
	placesMaxCount      = 100
	// placesMaxRadius is half the circumference of the earth in meters: a larger bias reaches nothing more.
	placesMaxRadius = 20_000_000
	maxGeolocText   = 40
	maxMixedPlaces  = 200
	// maxReviews keeps the reviews of one place short: they are long texts of strangers.
	maxReviews = 10
)

var (
	geolocPattern = regexp.MustCompile(`^-?[0-9]{1,3}(\.[0-9]{1,10})?x-?[0-9]{1,3}(\.[0-9]{1,10})?$`)
	// coordinatesShape is a latitude and a longitude.
	coordinatesShape = listOf(3, decimal)

	// placeReviewsShape shows fewer and shorter reviews than the web search does.
	placeReviewsShape = object(p("results", listOf(maxReviews, object(p("title", title),
		p("description", snippet), p("date", short), p("rating", ratingShape), p("author", thingShape),
		p("review_url", addr), p("language", short)))), p("viewMoreUrl", addr),
		p("reviews_in_foreign_language", boolean))

	// placeShape is a place of the place search and of the points-of-interest endpoint alike.
	placeShape = object(join(placeMembers(placeReviewsShape), []prop{p("id", short), p("results",
		list(object(join(pageProps, []prop{p("meta_url", metaURLShape)})...)))})...)

	// areaShape is a city, a country, a region, or a neighborhood.
	areaShape = object(p("type", short), p("name", title), p("country", short), p("coordinates", coordinatesShape),
		p("thumbnail", thumbnailShape))
	// streetShape is an address or a street with the places on and near it.
	streetShape = object(p("type", short), p("name", title), p("coordinates", coordinatesShape),
		p("pois", list(placeShape)), p("pois_nearby", list(placeShape)), p("zoom_level", integer),
		p("distance", distanceShape), p("postal_address", postalAddressShape))

	// placesAnswerShape is the answer of the endpoint in Brave's own member names.
	placesAnswerShape = object(
		p("type", short),
		p("query", object(p("original", body), p("altered", body), p("spellcheck_off", boolean),
			p("show_strict_warning", boolean))),
		p("results", listOf(placesMaxCount, placeShape)),
		p("cities", list(areaShape)), p("countries", list(areaShape)), p("regions", list(areaShape)),
		p("neighborhoods", list(areaShape)),
		p("addresses", list(streetShape)), p("streets", list(streetShape)),
		p("mixed", listOf(maxMixedPlaces, mixedEntryShape)),
		p("location", object(p("coordinates", coordinatesShape), p("name", title), p("country", short))),
	)
)

// placesOutputSchema is the answer with the quota of the provider.
var placesOutputSchema = `{"type":"object","properties":{` + placesAnswerShape.propertiesSchema() +
	`,"quota":` + quotaSchema + `},"additionalProperties":false}`

// placesSearch searches places around coordinates or a named location. It has no pages: count reaches up to
// 100 places in one answer.
var placesSearch = descriptor("places", "search", "Search Brave places",
	"Search places such as restaurants, shops, and sights in Brave's local index around coordinates or a named "+
		"location, or read the general places of an area when no query is given. One answer carries the places "+
		"and, where Brave found them, cities, countries, regions, neighborhoods, addresses, and streets. The ids "+
		"of the places expire after about 8 hours and are the input of brave.pois.get and "+
		"brave.poi-descriptions.get. Needs the Search plan of Brave, and every request is billed. A position is "+
		"only used when given; Qatlas never derives one.",
	[]string{"places", "local"},
	`{"type":"object","properties":{"q":{"type":"string","minLength":1,"maxLength":400},`+
		`"latitude":{"type":"number","minimum":-90,"maximum":90},`+
		`"longitude":{"type":"number","minimum":-180,"maximum":180},`+
		`"location":{"type":"string","minLength":1,"maxLength":100},`+
		`"radius":{"type":"integer","minimum":0,"maximum":20000000},`+
		`"count":{"type":"integer","minimum":1,"maximum":100},`+
		`"country":`+countrySchema+`,"search_lang":`+searchLangSchema+`,"ui_lang":`+uiLangSchema+`,`+
		`"units":`+enumSchema(unitSystems)+`,"safesearch":`+safeSearchSchema+`,"spellcheck":`+booleanSchema+`,`+
		`"geoloc":{"type":"string","minLength":3,"maxLength":40,`+
		`"pattern":"^-?[0-9]{1,3}(\\.[0-9]{1,10})?x-?[0-9]{1,3}(\\.[0-9]{1,10})?$","x-form":"<latitude>x<longitude>"},`+
		`"no_cache":`+booleanSchema+`},"additionalProperties":false}`,
	json.RawMessage(placesOutputSchema),
	[]capability.Argument{
		{Name: "q", Description: "Search query, at most 400 characters and 50 words; without it Brave returns " +
			"the general places of the area"},
		{Name: "latitude", Description: "Latitude of the area in degrees, together with longitude. Not together " +
			"with location"},
		{Name: "longitude", Description: "Longitude of the area in degrees, together with latitude"},
		{Name: "location", Description: "Name of the area instead of coordinates, such as san francisco ca " +
			"united states, or tokyo japan for a place outside the US: city, state if any, and country name " +
			"without commas. Not together with latitude and longitude"},
		{Name: "radius", Description: "Bias towards places within this many meters of the coordinates, from 0; " +
			"not a strict limit. The search is global when omitted"},
		{Name: "count", Description: "Places, from 1 through 100; 20 when omitted; Brave may return fewer. " +
			"There is no further batch"},
		{Name: "country", Description: "Country scope as a two-letter code such as US, or ALL; US when omitted"},
		{Name: "search_lang", Description: "Language of the results as a Brave language code such as en or " +
			"pt-br; en when omitted"},
		{Name: "ui_lang", Description: "Language of the interface texts as a locale such as en-US; en-US " +
			"when omitted"},
		{Name: "units", Description: "metric (kilometers) or imperial (miles) for distances; metric when omitted"},
		{Name: "safesearch", Description: "off, moderate, or strict; strict when omitted"},
		{Name: "spellcheck", Description: "Whether Brave corrects the query; true when omitted. The corrected " +
			"query is then the one searched"},
		{Name: "geoloc", Description: "Position of the user as <latitude>x<longitude> such as 52.52x13.405, " +
			"used to compute the distance of each place; it does not select the area"},
		{Name: "no_cache", Description: "True asks Brave to skip its cache, as a best effort"},
	},
	[]capability.Field{
		{Name: "type", Description: "Type of the answer"},
		{Name: "query", Description: "How Brave read the query: original, altered by the spellchecker, " +
			"spellcheck_off, and show_strict_warning when safe search hid relevant adult results"},
		{Name: "results", Description: "Places with id, title, url, provider_url, description, coordinates, " +
			"postal_address, opening_hours, contact, rating, price_range, distance, categories, serves_cuisine, " +
			"thumbnail, pictures, reviews, profiles, action, timezone, and the web results about the place. " +
			"Untrusted data; the id expires after about 8 hours"},
		{Name: "cities", Description: "Cities that match, each with name, country, coordinates, and thumbnail"},
		{Name: "countries", Description: "Countries that match, in the shape of cities"},
		{Name: "regions", Description: "Regions that match, in the shape of cities"},
		{Name: "neighborhoods", Description: "Neighborhoods that match, in the shape of cities"},
		{Name: "addresses", Description: "Addresses that match, each with name, coordinates, postal_address, " +
			"distance, pois (places at the address), and pois_nearby"},
		{Name: "streets", Description: "Streets that match, in the shape of addresses"},
		{Name: "mixed", Description: "Brave's ranking: a list of entries with the type of result, its index in " +
			"that group, and all for the whole group"},
		{Name: "location", Description: "The area Brave searched: coordinates, name, and country"},
		quotaField,
	},
	[]capability.Example{{
		Description: "Find coffee shops around a position",
		Arguments:   json.RawMessage(`{"q":"coffee","latitude":52.52,"longitude":13.405,"radius":1500,"count":10}`),
	}, {
		Description: "Find places in a named city",
		Arguments:   json.RawMessage(`{"q":"pizza","location":"berlin germany","search_lang":"de","ui_lang":"de-DE"}`),
	}})

// placesArguments are the arguments of brave.places.search.
type placesArguments struct {
	Query      string   `json:"q"`
	Latitude   *float64 `json:"latitude"`
	Longitude  *float64 `json:"longitude"`
	Location   string   `json:"location"`
	Radius     *int     `json:"radius"`
	Count      int      `json:"count"`
	Country    string   `json:"country"`
	SearchLang string   `json:"search_lang"`
	UILang     string   `json:"ui_lang"`
	Units      string   `json:"units"`
	Safesearch string   `json:"safesearch"`
	Spellcheck *bool    `json:"spellcheck"`
	Geoloc     string   `json:"geoloc"`
	NoCache    *bool    `json:"no_cache"`
}

// checkGeoloc accepts the form <latitude>x<longitude> with both values in range.
func checkGeoloc(value string) error {
	if value == "" {
		return nil
	}
	latitude, longitude, found := strings.Cut(value, "x")
	if len(value) > maxGeolocText || !geolocPattern.MatchString(value) || !found {
		return badRequest("geoloc must be <latitude>x<longitude> in degrees, such as 52.52x13.405")
	}
	lat, latErr := strconv.ParseFloat(latitude, 64)
	lon, lonErr := strconv.ParseFloat(longitude, 64)
	if latErr != nil || lonErr != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return badRequest("geoloc must be <latitude>x<longitude> in degrees, such as 52.52x13.405")
	}
	return nil
}

// preparePlacesSearch checks every argument; the request is built only from what passed.
func preparePlacesSearch(args placesArguments) (run, error) {
	var query string
	if args.Query != "" {
		var err error
		if query, err = checkQuery(args.Query, placesMaxQueryRunes, placesMaxQueryWords); err != nil {
			return nil, err
		}
	}
	// The pair is checked by the shared location rules; Brave takes the pair as query parameters here.
	if err := (location{Latitude: args.Latitude, Longitude: args.Longitude}).check(); err != nil {
		return nil, err
	}
	if args.Location != "" && args.Latitude != nil {
		return nil, badRequest("location and latitude with longitude are alternatives; give one of them")
	}
	for _, check := range []error{
		checkLocationText("location", args.Location, maxLocationText),
		checkEnum("country", args.Country, countries),
		checkEnum("search_lang", args.SearchLang, searchLanguages),
		checkEnum("ui_lang", args.UILang, uiLanguages),
		checkEnum("units", args.Units, unitSystems),
		checkEnum("safesearch", args.Safesearch, safeSearchModes),
		checkGeoloc(args.Geoloc),
	} {
		if check != nil {
			return nil, check
		}
	}
	if args.Radius != nil && (*args.Radius < 0 || *args.Radius > placesMaxRadius) {
		return nil, badRequest("radius must be between 0 and " + strconv.Itoa(placesMaxRadius) + " meters")
	}
	if args.Count != 0 && (args.Count < 1 || args.Count > placesMaxCount) {
		return nil, badRequest("count must be between 1 and " + strconv.Itoa(placesMaxCount))
	}

	values := url.Values{}
	text(values, "q", query)
	if args.Latitude != nil {
		values.Set("latitude", strconv.FormatFloat(*args.Latitude, 'f', -1, 64))
		values.Set("longitude", strconv.FormatFloat(*args.Longitude, 'f', -1, 64))
	}
	text(values, "location", args.Location)
	if args.Radius != nil {
		values.Set("radius", strconv.Itoa(*args.Radius))
	}
	if args.Count != 0 {
		values.Set("count", strconv.Itoa(args.Count))
	}
	text(values, "country", args.Country)
	text(values, "search_lang", args.SearchLang)
	text(values, "ui_lang", args.UILang)
	text(values, "units", args.Units)
	text(values, "safesearch", args.Safesearch)
	flag(values, "spellcheck", args.Spellcheck)
	text(values, "geoloc", args.Geoloc)
	extra := noCache(args.NoCache)

	return func(ctx context.Context, client *Client) (any, error) {
		var answer map[string]any
		info, err := client.get(ctx, "search places", placesPath, values, extra, &answer)
		if err != nil {
			return nil, err
		}
		return shapedResult(placesAnswerShape, answer, info), nil
	}, nil
}

// shapedResult cleans an answer by its shape and adds the quota of the provider.
func shapedResult(answer *shape, raw map[string]any, info rateInfo) map[string]any {
	cleaned, _ := answer.clean(raw)
	out, _ := cleaned.(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	if q := quotaOf(info); q != nil {
		out["quota"] = q
	}
	return out
}
