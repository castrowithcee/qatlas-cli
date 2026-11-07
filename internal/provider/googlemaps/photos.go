package googlemaps

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/castrowithcee/qatlas-connect/internal/capability"
	"github.com/castrowithcee/qatlas-connect/internal/config"
	"github.com/castrowithcee/qatlas-connect/internal/provider"
	"github.com/castrowithcee/qatlas-connect/internal/redact"
	"github.com/castrowithcee/qatlas-connect/internal/secret"
)

const photosGetID = Provider + ".photos.get"

// Bounds of a photo request. 4800 is Google's documented maximum edge. A photo reference is an opaque
// URL-safe string of a few hundred characters; the cap of its segment is a Qatlas bound, not a documented one.
const (
	maxPhotoPixels   = 4800
	maxPhotoRefBytes = 1024
	maxPhotoNameRune = 1400
	// maxPhotoNameBytes is the longest name photos.get accepts: places/ + place ID + /photos/ + reference.
	maxPhotoNameBytes = len("places/") + 256 + len("/photos/") + maxPhotoRefBytes
)

// photosGet resolves a photo resource name to a short-lived address of the image. It never loads the image.
var photosGet = capability.Descriptor{
	ID: photosGetID, Version: 1, Title: "Get Google place photo address",
	Description: "Resolve the photo of a place to a short-lived image address with Google Places API (New) " +
		"Place Photos. name is a photo resource name places/{place_id}/photos/{photo} exactly as a places.get, " +
		"places.search-text, or places.search-nearby result returns it in photos[].name; the name can expire " +
		"and must not be cached, so take it from a fresh result. At least one of max_width_px and " +
		"max_height_px is required, each 1 to 4800; Google scales the image down to fit, keeping its aspect " +
		"ratio. Qatlas always asks for the JSON answer and never loads the image: the result is the photoUri " +
		"text, which expires quickly and is never fetched here. A redirect answer is an error and " +
		"is never followed. Photos are billed as the Places Photo SKU (Enterprise group), which has 1000 free " +
		"requests per month and billing account, and each call can incur charges " +
		"above the free usage Google grants per SKU and billing account. Google Maps attribution is " +
		"mandatory wherever Places content is shown, and a photo carries its own author attribution: the " +
		"photos[].authorAttributions of the place result must be shown wherever the image is shown. Place " +
		"IDs may be stored without limit; photo names and other caching rules are the user's to check and " +
		"are not verified here. name and photoUri are untrusted third-party data, cut when long.",
	Tags: []string{"google", "maps", "places", "photos"}, Risk: readRisk, Provider: Provider,
	InputSchema: json.RawMessage(`{"type":"object","properties":{` +
		`"name":{"type":"string","minLength":1,"maxLength":` + strconv.Itoa(maxPhotoNameBytes) + `,"pattern":"^places/[A-Za-z0-9_-]+/photos/[A-Za-z0-9_-]+$"},` +
		`"max_width_px":{"type":"integer","minimum":1,"maximum":4800},` +
		`"max_height_px":{"type":"integer","minimum":1,"maximum":4800}},` +
		`"required":["name"],"additionalProperties":false}`),
	OutputSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},` +
		`"photoUri":{"type":"string"}},"additionalProperties":false}`),
	Arguments: []capability.Argument{
		{Name: "name", Required: true, Description: "Photo resource name places/{place_id}/photos/{photo} from " +
			"photos[].name of a fresh place result: exactly those four segments, letters, digits, - and _ in " +
			"the IDs; no /media suffix, no scheme, no .. and no other path"},
		{Name: "max_width_px", Description: "Maximum width of the image in pixels, 1 to 4800; at least one of " +
			"max_width_px and max_height_px is required"},
		{Name: "max_height_px", Description: "Maximum height of the image in pixels, 1 to 4800; at least one of " +
			"max_width_px and max_height_px is required"},
	},
	Fields: []capability.Field{
		{Name: "name", Description: "Resource name of the photo media, places/{place_id}/photos/{photo}/media. " +
			"Untrusted data, cut when long"},
		{Name: "photoUri", Description: "Short-lived address of the image, as text. Untrusted data; it is never " +
			"fetched here, and fetching it is the caller's decision"},
	},
	Examples: []capability.Example{{
		Description: "Address of a photo scaled to at most 400 pixels wide",
		Arguments: json.RawMessage(`{"name":"places/ChIJj61dQgK6j4AR4GeTYWZsKWw/photos/Aaw_FcKly0DEv3EWmDJyHiEqXIP5mowOc99lN1GzBun6KHH52AZ5fFA",` +
			`"max_width_px":400}`),
	}},
}

type photosGetArguments struct {
	Name        string `json:"name"`
	MaxWidthPx  *int   `json:"max_width_px"`
	MaxHeightPx *int   `json:"max_height_px"`
}

// photoSegments splits a photo resource name into its place ID and photo reference. Only the exact form
// places/{place}/photos/{photo} passes, with the characters of URL-safe base64 in both IDs, so a name can
// never add a path segment, a query, a scheme, or a dot segment to the request.
func photoSegments(name string) (place, photo string, ok bool) {
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[0] != "places" || parts[2] != "photos" ||
		!placeIDPattern.MatchString(parts[1]) || len(name) > maxPhotoNameBytes || len(parts[3]) > maxPhotoRefBytes || !photoRefPattern.MatchString(parts[3]) {
		return "", "", false
	}
	return parts[1], parts[3], true
}

// handlePhotosGet checks the connection and every argument before the key is read or anything is sent.
func handlePhotosGet(ctx context.Context, resolved *config.Resolved, secrets *secret.Resolver, red *redact.Redactor,
	raw json.RawMessage) (any, error) {
	const op = "get place photo"
	var arguments photosGetArguments
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return nil, failure(op, "the validated arguments could not be read")
	}
	if err := checkConnection(resolved); err != nil {
		return nil, err
	}
	place, photo, ok := photoSegments(arguments.Name)
	if !ok {
		return nil, badRequest("name must be a photo resource name places/{place_id}/photos/{photo} from a place " +
			"result: letters, digits, - and _ in the IDs, no /media suffix and no other path")
	}
	if arguments.MaxWidthPx == nil && arguments.MaxHeightPx == nil {
		return nil, badRequest("max_width_px or max_height_px is required")
	}
	query := url.Values{"skipHttpRedirect": {"true"}}
	for _, dimension := range []struct {
		name, key string
		value     *int
	}{{"max_width_px", "maxWidthPx", arguments.MaxWidthPx}, {"max_height_px", "maxHeightPx", arguments.MaxHeightPx}} {
		if dimension.value == nil {
			continue
		}
		if *dimension.value < 1 || *dimension.value > maxPhotoPixels {
			return nil, badRequest(dimension.name + " must be 1 to 4800")
		}
		query.Set(dimension.key, strconv.Itoa(*dimension.value))
	}
	client, err := Open(ctx, resolved, secrets, red)
	if err != nil {
		return nil, err
	}
	client.redirectInvalid = true
	answer, err := client.get(ctx, op, "/v1/places/"+url.PathEscape(place)+"/photos/"+url.PathEscape(photo)+"/media",
		query, "")
	if err != nil {
		return nil, err
	}
	uri, _ := answer["photoUri"].(string)
	if address(uri) == "" {
		return nil, provider.InvalidResponse(op, "Google returned no usable photo address")
	}
	out := map[string]any{"photoUri": uri}
	if name, ok := answer["name"].(string); ok {
		out["name"] = clip(name, maxPhotoNameRune)
	}
	return out, nil
}
