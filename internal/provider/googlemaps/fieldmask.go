package googlemaps

import (
	"encoding/json"
	"strings"
)

// Tier is the billing group of a Places field. The highest tier of a field mask decides the SKU Google
// bills for the request.
type Tier int

// The groups, ordered by cost. Atmosphere is the group Google bills as Enterprise + Atmosphere.
const (
	TierIDsOnly Tier = iota + 1
	TierEssentials
	TierPro
	TierEnterprise
	TierAtmosphere
)

// String names the SKU as the Place Details documentation does.
func (t Tier) String() string {
	switch t {
	case TierIDsOnly:
		return "IDs Only"
	case TierEssentials:
		return "Essentials"
	case TierPro:
		return "Pro"
	case TierEnterprise:
		return "Enterprise"
	case TierAtmosphere:
		return "Enterprise + Atmosphere"
	}
	return "unknown"
}

// placeFields maps every top-level field of a Place that Place Details can return to its group. Nested
// paths are not accepted: the group of a field is documented per top-level field. routingSummaries is a
// field of the search endpoints only and is handled by the search masks, so it is absent here.
var placeFields = map[string]Tier{
	"attributions": TierIDsOnly, "consumerAlert": TierIDsOnly, "id": TierIDsOnly, "movedPlace": TierIDsOnly,
	"movedPlaceId": TierIDsOnly, "name": TierIDsOnly, "photos": TierIDsOnly,

	"addressComponents": TierEssentials, "addressDescriptor": TierEssentials, "adrFormatAddress": TierEssentials,
	"formattedAddress": TierEssentials, "location": TierEssentials, "plusCode": TierEssentials,
	"postalAddress": TierEssentials, "shortFormattedAddress": TierEssentials, "types": TierEssentials,
	"viewport": TierEssentials,

	"accessibilityOptions": TierPro, "businessStatus": TierPro, "containingPlaces": TierPro,
	"displayName": TierPro, "entrances": TierPro, "googleMapsLinks": TierPro, "googleMapsTypeLabel": TierPro,
	"googleMapsUri": TierPro, "iconBackgroundColor": TierPro, "iconMaskBaseUri": TierPro,
	"navigationPoints": TierPro, "openingDate": TierPro, "primaryType": TierPro,
	"primaryTypeDisplayName": TierPro, "pureServiceAreaBusiness": TierPro, "subDestinations": TierPro,
	"timeZone": TierPro, "utcOffsetMinutes": TierPro,

	"currentOpeningHours": TierEnterprise, "currentSecondaryOpeningHours": TierEnterprise,
	"internationalPhoneNumber": TierEnterprise, "nationalPhoneNumber": TierEnterprise,
	"priceLevel": TierEnterprise, "priceRange": TierEnterprise, "rating": TierEnterprise,
	"regularOpeningHours": TierEnterprise, "regularSecondaryOpeningHours": TierEnterprise,
	"transitStation": TierEnterprise, "userRatingCount": TierEnterprise, "websiteUri": TierEnterprise,

	"allowsDogs": TierAtmosphere, "curbsidePickup": TierAtmosphere, "delivery": TierAtmosphere,
	"dineIn": TierAtmosphere, "editorialSummary": TierAtmosphere, "evChargeAmenitySummary": TierAtmosphere,
	"evChargeOptions": TierAtmosphere, "fuelOptions": TierAtmosphere, "generativeSummary": TierAtmosphere,
	"goodForChildren": TierAtmosphere, "goodForGroups": TierAtmosphere, "goodForWatchingSports": TierAtmosphere,
	"liveMusic": TierAtmosphere, "menuForChildren": TierAtmosphere, "neighborhoodSummary": TierAtmosphere,
	"outdoorSeating": TierAtmosphere, "parkingOptions": TierAtmosphere, "paymentOptions": TierAtmosphere,
	"reservable": TierAtmosphere, "restroom": TierAtmosphere, "reviewSummary": TierAtmosphere,
	"reviews": TierAtmosphere, "servesBeer": TierAtmosphere, "servesBreakfast": TierAtmosphere,
	"servesBrunch": TierAtmosphere, "servesCocktails": TierAtmosphere, "servesCoffee": TierAtmosphere,
	"servesDessert": TierAtmosphere, "servesDinner": TierAtmosphere, "servesLunch": TierAtmosphere,
	"servesVegetarianFood": TierAtmosphere, "servesWine": TierAtmosphere, "takeout": TierAtmosphere,
}

// Bounds of a field mask. The longest field name is 28 characters.
const (
	maxMaskPaths = 80
	maxMaskPath  = 64
	maxMaskBytes = 2048
)

// maskKind says which endpoint a field mask is for. The groups of a field differ between Place Details and the
// searches, and the searches send paths below places.
type maskKind int

const (
	maskDetails maskKind = iota
	maskTextSearch
	maskNearbySearch
)

// routingSummaries is the one top-level member of a search answer next to places. It is named without the
// places. prefix, and Google bills it in the Enterprise + Atmosphere group of both searches.
const routingSummariesField = "routingSummaries"

// searchIDsOnly are the fields Text Search bills in its IDs Only group. Every other field that Place Details
// bills below Pro is Pro in a search, and Nearby Search has no group below Pro.
var searchIDsOnly = map[string]bool{
	"attributions": true, "consumerAlert": true, "id": true, "movedPlace": true, "movedPlaceId": true, "name": true,
}

// searchTier is the group a search bills for a field of Place Details.
func searchTier(kind maskKind, name string, tier Tier) Tier {
	switch {
	case kind == maskDetails:
		return tier
	case kind == maskTextSearch && searchIDsOnly[name]:
		return TierIDsOnly
	}
	return max(tier, TierPro)
}

// searchSKU names the SKU a search bills for its highest group, as Google's price list does.
func searchSKU(kind maskKind, tier Tier) string {
	name := "Text Search"
	if kind == maskNearbySearch {
		name = "Nearby Search"
	}
	switch tier {
	case TierIDsOnly:
		return name + " Essentials (IDs Only)"
	case TierAtmosphere:
		return name + " Enterprise + Atmosphere"
	}
	return name + " " + tier.String()
}

// fieldMask is a checked field mask: its header value and the highest group of its fields. routing is true
// when a search mask names routingSummaries.
type fieldMask struct {
	header  string
	tier    Tier
	routing bool
}

// parseFieldMask checks the field_mask of Place Details.
func parseFieldMask(raw json.RawMessage) (fieldMask, error) { return parseMask(raw, maskDetails) }

// parseMask checks the field_mask argument of kind, a list of paths or one comma-separated string, against the
// table of known paths before any secret is read or any request is built. There is no default and no
// wildcard, because Google bills by the fields a request names. A message names the rule and the position of
// the entry, never its value. A search mask names top-level fields of a place as Place Details does, plus
// routingSummaries; its header gets the places. prefix, and Text Search always gets nextPageToken, which its
// paging needs and which is free.
func parseMask(raw json.RawMessage, kind maskKind) (fieldMask, error) {
	var entries []string
	var text string
	var list []string
	switch {
	case len(raw) == 0 || string(raw) == "null":
		return fieldMask{}, badRequest("field_mask is required: name the fields to return; there is no default")
	case json.Unmarshal(raw, &text) == nil:
		if len(text) > maxMaskBytes {
			return fieldMask{}, badRequest("field_mask is too long")
		}
		entries = strings.Split(text, ",")
	case json.Unmarshal(raw, &list) == nil:
		entries = list
	default:
		return fieldMask{}, badRequest("field_mask must be a list of field paths or one comma-separated string")
	}
	if len(entries) == 0 {
		return fieldMask{}, badRequest("field_mask is required: name the fields to return; there is no default")
	}
	if len(entries) > maxMaskPaths {
		return fieldMask{}, badRequest("field_mask names too many fields")
	}
	var (
		paths   []string
		seen    = map[string]bool{}
		top     Tier
		routing bool
	)
	for i, entry := range entries {
		path := strings.TrimSpace(entry)
		position := i + 1
		switch {
		case path == "":
			return fieldMask{}, badRequest("field_mask entry " + itoa(position) + " is empty")
		case strings.Contains(path, "*"):
			return fieldMask{}, badRequest("field_mask entry " + itoa(position) +
				" is a wildcard; name the fields to return, a wildcard is never accepted")
		case len(path) > maxMaskPath:
			return fieldMask{}, badRequest("field_mask entry " + itoa(position) + " is too long")
		}
		var tier Tier
		sent := path
		if kind != maskDetails && path == routingSummariesField {
			tier, routing = TierAtmosphere, true
		} else {
			var known bool
			if tier, known = placeFields[path]; !known {
				return fieldMask{}, badRequest("field_mask entry " + itoa(position) + " is not a known top-level " +
					"field of a place; nested paths, a places. prefix, and fields of other endpoints are not accepted")
			}
			tier = searchTier(kind, path, tier)
			if kind != maskDetails {
				sent = "places." + path
			}
		}
		top = max(top, tier)
		if !seen[path] {
			seen[path] = true
			paths = append(paths, sent)
		}
	}
	if kind == maskTextSearch {
		paths = append(paths, "nextPageToken")
	}
	return fieldMask{header: strings.Join(paths, ","), tier: top, routing: routing}, nil
}
