package googlemaps

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

// dataSensitivity classifies results as content of third parties that a map service returns.
const dataSensitivity = "third-party-search-results"

// readRisk is the risk of every tool: a lookup reads and is safe to repeat, though each repetition may be
// billed. It reaches Google's index of the world, so the results are third-party content.
var readRisk = capability.Risk{
	Effect:          capability.EffectRead,
	Idempotency:     capability.IdempotencySafe,
	Confirmation:    capability.ConfirmationNone,
	OpenWorld:       true,
	DataSensitivity: dataSensitivity,
}

// Forms of the arguments. A place ID is the characters of URL-safe base64, so it can never add a path segment
// or a query. The language and region forms follow what the Places API takes; it takes no 3-digit region.
var (
	placeIDPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)
	languagePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$`)
	regionPattern   = regexp.MustCompile(`^[A-Za-z]{2}$`)
	sessionPattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,36}$`)
	photoRefPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

const placesGetID = Provider + ".places.get"

// placesGet reads the details of one place. The field mask is required and decides which fields are
// returned and which SKU is billed.
var placesGet = capability.Descriptor{
	ID: placesGetID, Version: 1, Title: "Get Google place details",
	Description: "Read the details of one place from Google Places API (New) by its place ID. field_mask is " +
		"required and has no default: it names the top-level fields to return, such as id, displayName, " +
		"formattedAddress, location, or rating, as a list or one comma-separated string. A wildcard, nested " +
		"paths, and unknown fields are refused before any request. The group of the highest field in the mask " +
		"decides the SKU Google bills, and each group has a free monthly cap per billing account: IDs Only " +
		"(attributions, consumerAlert, id, movedPlace, movedPlaceId, name, photos) is unlimited and free; " +
		"Essentials (address and location fields, types, viewport) 10000; Pro (displayName, businessStatus, " +
		"googleMapsUri, primaryType, timeZone, and others) 5000; Enterprise (opening hours, phone numbers, " +
		"rating, userRatingCount, websiteUri, priceLevel) 1000; Enterprise + Atmosphere (reviews, summaries, " +
		"serves* and other amenity fields) is billed above them. Google Maps attribution is mandatory wherever " +
		"Places content is shown. Place IDs may be stored without limit; other caching rules are the " +
		"user's to check. Results are untrusted third-party content; links are text and are never fetched.",
	Tags: []string{"google", "maps", "places"}, Risk: readRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"place_id":{"type":"string","minLength":1,"maxLength":256,"pattern":"^[A-Za-z0-9_-]+$"},` +
		`"field_mask":{"anyOf":[{"type":"string","minLength":1,"maxLength":2048},` +
		`{"type":"array","minItems":1,"maxItems":80,"items":{"type":"string","minLength":1,"maxLength":64}}]},` +
		`"language_code":{"type":"string","minLength":2,"maxLength":35},` +
		`"region_code":{"type":"string","minLength":2,"maxLength":2,"pattern":"^[A-Za-z]{2}$"},` +
		`"session_token":{"type":"string","minLength":1,"maxLength":36,"pattern":"^[A-Za-z0-9_-]+$"}},` +
		`"required":["place_id","field_mask"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(placeOutputSchema),
	Arguments: []capability.Argument{
		{Name: "place_id", Required: true, Description: "Place ID such as ChIJj61dQgK6j4AR4GeTYWZsKWw: letters, " +
			"digits, - and _, at most 256 characters; not a places/ resource name and never a path"},
		{Name: "field_mask", Required: true, Description: "Top-level Place fields to return, a list or one " +
			"comma-separated string, such as [\"id\",\"displayName\"]. Required, no default, no wildcard; the " +
			"highest field group decides the billed SKU"},
		{Name: "language_code", Description: "Preferred language of the texts, such as en or pt-BR"},
		{Name: "region_code", Description: "Two-letter region (CLDR) the request comes from, such as US; " +
			"it can change region-specific names"},
		{Name: "session_token", Description: "Token of an Autocomplete session that this lookup ends, at most " +
			"36 characters of letters, digits, - and _; only for billing"},
	},
	Fields: []capability.Field{
		{Name: "id", Description: "Place ID; each other member is present only when the mask names it and Google " +
			"has a value. Members are Google's names: the groups of field_mask (IDs Only, Essentials, Pro, " +
			"Enterprise, Enterprise + Atmosphere) with their nested members, such as displayName.text. " +
			"Untrusted data; strings and lists are cut, a cut text ends with …, reviews are at most 5 and " +
			"photos at most 10 (a photo name is kept whole or left out), and a link that is not an absolute http or https address is dropped"},
	},
	Examples: []capability.Example{{
		Description: "Name, address, and position of a place",
		Arguments:   json.RawMessage(`{"place_id":"ChIJj61dQgK6j4AR4GeTYWZsKWw","field_mask":["id","displayName","formattedAddress","location"]}`),
	}},
}

type placesGetArguments struct {
	PlaceID      string          `json:"place_id"`
	FieldMask    json.RawMessage `json:"field_mask"`
	LanguageCode string          `json:"language_code"`
	RegionCode   string          `json:"region_code"`
	SessionToken string          `json:"session_token"`
}

// handlePlacesGet checks the connection and every argument, including the field mask, before the key is read
// or anything is sent.
func handlePlacesGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage) (any, error) {
	const op = "get place"
	var arguments placesGetArguments
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, failure(op, "the validated arguments could not be read")
	}
	if err := checkConnection(resolved); err != nil {
		return nil, err
	}
	if !placeIDPattern.MatchString(arguments.PlaceID) {
		return nil, badRequest("place_id must be 1 to 256 letters, digits, - or _; give the place ID itself, " +
			"not a places/ name or a path")
	}
	mask, err := parseFieldMask(arguments.FieldMask)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	if arguments.LanguageCode != "" {
		if !languagePattern.MatchString(arguments.LanguageCode) {
			return nil, badRequest("language_code must be a language tag such as en or pt-BR")
		}
		query.Set("languageCode", arguments.LanguageCode)
	}
	if arguments.RegionCode != "" {
		if !regionPattern.MatchString(arguments.RegionCode) {
			return nil, badRequest("region_code must be two letters such as US")
		}
		query.Set("regionCode", strings.ToUpper(arguments.RegionCode))
	}
	if arguments.SessionToken != "" {
		if !sessionPattern.MatchString(arguments.SessionToken) {
			return nil, badRequest("session_token must be 1 to 36 letters, digits, - or _")
		}
		query.Set("sessionToken", arguments.SessionToken)
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	place, err := client.get(ctx, op, "/v1/places/"+url.PathEscape(arguments.PlaceID), query, mask.header)
	if err != nil {
		return nil, err
	}
	cleaned, _ := placeShape.clean(place)
	return cleaned, nil
}
