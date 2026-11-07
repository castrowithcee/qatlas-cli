---
description: >
  Describes the Google Maps provider: the API key, its header and query paths, and its fixed origin, the read profile, the mandatory field mask, with and without the places. prefix, and the billing group it selects, the cost of autocomplete and photos, attribution and storage rules, the two Google error forms and their classes, rate spacing, the connection test, and the limits of what is verified.
type: knowledge
edit: shared
created: 2026-11-06
updated: 2026-11-07
---

# Google Maps

Google Maps is a provider for Google Maps Platform with one API key. It reads the details of one place,
searches places by text or around a point, predicts places from typed input, and resolves place photos to
short-lived addresses in Places API (New), and geocodes addresses and coordinates with the Geocoding API. It changes nothing: every tool is a read, and the connection takes no target, because a key
reaches what its Cloud project and the key's restrictions allow, not a part of an account.

[Set up Google Maps](setup.md) leads through creating the key. The tools are described in [places](places.md),
[photos](photos.md), and [geocoding](geocoding.md).

## Configuration

A service is always the official origin `https://places.googleapis.com`, which applies when `base_url` is
left out. Qatlas refuses any other URL before the key is read, because the key belongs to that one service. A
connection with a target is refused.

The credential provides `api_key`. The Places tools send it only in the `X-Goog-Api-Key` header. The geocoding
tools call the legacy Geocoding service, which takes no header, and send it as the `key` query parameter of
a fixed URL; they are the only requests that do so, see [geocoding](geocoding.md#endpoint-and-key). The key is
never a tool argument, never part of a message or result, and masked in every output.

The profile `read` is the recommended one and holds `google.maps.places.get`, `google.maps.places.search-text`,
`google.maps.places.search-nearby`, `google.maps.places.autocomplete`, `google.maps.photos.get`,
`google.maps.geocoding.geocode`, and `google.maps.geocoding.reverse`.

## Field mask and billing group

Google bills a Places request by the fields it asks for, so every request carries a field mask in the
`X-Goog-FieldMask` header. Qatlas requires `field_mask` and has no default. It refuses, before the key is read
and without a request, a missing or empty mask, any wildcard `*`, nested paths such as `displayName.text`, a
`places.` prefix, and every name that is not a top-level field of a place that Place Details can return.

The highest group among the fields decides the billed SKU:

| Group | Fields |
| --- | --- |
| IDs Only | `attributions`, `consumerAlert`, `id`, `movedPlace`, `movedPlaceId`, `name`, `photos` |
| Essentials | `addressComponents`, `addressDescriptor`, `adrFormatAddress`, `formattedAddress`, `location`, `plusCode`, `postalAddress`, `shortFormattedAddress`, `types`, `viewport` |
| Pro | `accessibilityOptions`, `businessStatus`, `containingPlaces`, `displayName`, `entrances`, `googleMapsLinks`, `googleMapsTypeLabel`, `googleMapsUri`, `iconBackgroundColor`, `iconMaskBaseUri`, `navigationPoints`, `openingDate`, `primaryType`, `primaryTypeDisplayName`, `pureServiceAreaBusiness`, `subDestinations`, `timeZone`, `utcOffsetMinutes` |
| Enterprise | `currentOpeningHours`, `currentSecondaryOpeningHours`, `internationalPhoneNumber`, `nationalPhoneNumber`, `priceLevel`, `priceRange`, `rating`, `regularOpeningHours`, `regularSecondaryOpeningHours`, `transitStation`, `userRatingCount`, `websiteUri` |
| Enterprise + Atmosphere | `allowsDogs`, `curbsidePickup`, `delivery`, `dineIn`, `editorialSummary`, `evChargeAmenitySummary`, `evChargeOptions`, `fuelOptions`, `generativeSummary`, `goodForChildren`, `goodForGroups`, `goodForWatchingSports`, `liveMusic`, `menuForChildren`, `neighborhoodSummary`, `outdoorSeating`, `parkingOptions`, `paymentOptions`, `reservable`, `restroom`, `reviewSummary`, `reviews`, `servesBeer`, `servesBreakfast`, `servesBrunch`, `servesCocktails`, `servesCoffee`, `servesDessert`, `servesDinner`, `servesLunch`, `servesVegetarianFood`, `servesWine`, `takeout` |

The mask checker is shared by the Maps tools of this provider, so every tool takes its mask through the same
table.

### Field mask of the searches

The two searches take the same names without a prefix, such as `displayName`, and the additional name
`routingSummaries`. Qatlas sends them as Google expects: `places.displayName` for a place field,
`routingSummaries` as it is, and, for the text search, `nextPageToken`, which paging needs and which Google
bills in no group. A caller's `places.` prefix, `nextPageToken`, any wildcard, and nested paths are refused
before the key is read. `places.get` still refuses `routingSummaries`.

The searches group the fields differently from Place Details: Text Search bills only `attributions`,
`consumerAlert`, `id`, `movedPlace`, `movedPlaceId`, and `name` as IDs Only and everything else of the first
two groups as Pro, and Nearby Search has no group below Pro. The SKUs are listed in
[places](places.md#billing-group-of-the-searches).

## Cost

Every tool call can be billed above the free usage of its SKU, and a search is billed once per request, so
each page of the text search is one request. The sources used do not state the free usage of the search SKUs.
Autocomplete and photos have no field mask and their own SKUs, described below.

Google documents a free monthly usage cap per SKU and billing account for Place Details: IDs Only is unlimited,
Essentials 10,000, Pro 5,000, Enterprise 1,000, and Enterprise + Atmosphere 1,000. Requests beyond a cap are
billed to the project's billing account at the current price on Google's pricing page. A `session_token`
changes the billing of a request that ends an Autocomplete session. Qatlas never repeats a request on its
own, so one tool call is at most one request.

### Autocomplete and photo SKUs

Google's price list shows `Autocomplete Requests` in the Essentials group with 10,000
free requests per month and billing account, and `Autocomplete Session Usage` as an unlimited SKU that shows up
as $0, because the `places.get` that ends the session is billed instead. `Places Photo` is in the Enterprise
group with 1,000 free requests. Prices and caps change; the price list is authoritative. See
[places](places.md#googlemapsplacesautocomplete) and [photos](photos.md).

## Attribution and storage

Google Maps attribution is mandatory wherever Places content is shown, and the `attributions` field names data
providers that must be shown with a result. A photo also has author attributions in the `authorAttributions` of
its `photos` entry, which must be shown wherever the image is shown. Place IDs may be stored without limit.
The remaining caching and
storage rules of Google's service specific terms are not checked here and are the user's to read; Qatlas keeps
no result cache.

All results are untrusted third-party content, including names, reviews, and summaries. Links are bounded text
that Qatlas never fetches, and a link that is not an absolute `http` or `https` address without user
information is dropped. Strings are cut at 300 characters, long texts such as reviews at 1500, and a cut text
ends with `…`; lists are cut at 20 entries, reviews at 5, and photos at 10. A photo name is never cut: one longer than
`photos.get` accepts (1295 characters) is left out.

## Rate spacing

Requests of one key are spaced by at least 250 ms within a process. This is a cautious start of Qatlas, not
a Google limit; the quota of a project is set in its Cloud console. On a `429` the next request of the key
waits one second, or the seconds of `Retry-After` up to one minute. The failed request is not repeated.

## Connection test

`qconnect connection test` reads one fixed place, the one of the examples in Google's Place Details
documentation, with the field mask `id`, the IDs Only group. Success proves that the key reaches Places API
(New) and that its project may call it. It does not prove Routes, Geocoding, or any other Maps API, and it does
not show the key's restrictions for them. A place ID can become obsolete; the test then ends as `not-found`.

## Errors

Qatlas' message never carries Google's text, because `message` and `details` may echo request values.
Google's own answer goes along as the original in the `provider` field of the error detail (untrusted,
redacted, and bounded): the `status`, the `message`, and the type and reason of each detail.

Google uses two error forms. Places API (New) answers a failure with an HTTP status and an object
`{"error":{"code","message","status","details":[...]}}`. The older web services answer with HTTP 200 and a
`status` in the body: `OK`, `ZERO_RESULTS`, `OVER_QUERY_LIMIT`, `OVER_DAILY_LIMIT`, `REQUEST_DENIED`, `INVALID_REQUEST`, or
`UNKNOWN_ERROR`. The provider reads both through one helper.

| Form | Value | Class |
| --- | --- | --- |
| Error object | 401 with `CREDENTIALS_MISSING` or `UNAUTHENTICATED` | `auth` |
| Error object | 403 with `PERMISSION_DENIED` | `permission` |
| Error object | 429 with `RATE_LIMIT_EXCEEDED` or `RESOURCE_EXHAUSTED` | `rate-limited` |
| Error object | 400 | `invalid-provider-response` (Qatlas checked the arguments before sending) |
| Error object | 404 | `not-found` |
| Error object | any other status or reason | by HTTP status: 401 `auth`, 403 `permission`, 429 `rate-limited`, 503 `unreachable`, 504 `timeout`, other `provider-error` |
| Legacy status | `OVER_QUERY_LIMIT` | `rate-limited` |
| Legacy status | `OVER_DAILY_LIMIT` | `permission` |
| Legacy status | `REQUEST_DENIED` | `auth` |
| Legacy status | `INVALID_REQUEST`, an unknown or missing status | `invalid-provider-response` |
| Legacy status | `UNKNOWN_ERROR` | `provider-error` |

`OK` and `ZERO_RESULTS` are no errors. A redirect is never followed and is a `provider-error`, except for the photo tool, where it
is `invalid-provider-response`. An unreadable,
oversized (over 2 MiB), or non-JSON answer is `invalid-provider-response`. The timeout is 30 seconds.

## Boundary

The provider reaches only the endpoint of the tool page. It offers no passthrough of method, path, header,
query, or body.

Not verified:

- The 60 places over all pages are Google's current limit, which it may change; Qatlas does not count them.
- The SKU of each field group of the searches follows the field lists of Google's search pages, and the free
  usage of the search SKUs is not stated there.
- Whether the connection test costs nothing can only be checked against a live billing account. It is based on
  Google's documentation that IDs Only requests are free.
- The error reasons `API_KEY_INVALID`, the reasons of API key restrictions, and the reasons of quota errors are
  not documented in the sources used and are not mapped by reason; the HTTP status decides.
- The place ID of the connection test is not proven stable.
- The caching and storage rules of Google's service specific terms beyond place IDs are not checked. Google
  states that a photo name cannot be cached and can expire.
- The cap of 1024 characters on a photo reference is a Qatlas bound, not a documented one. Free usage caps
  and SKU groups follow Google's price list and can change; Qatlas does not check them.
- The maximum length of the autocomplete `input` is not documented; Qatlas allows 1000 characters.
- Session tokens are accepted as 1 to 36 letters, digits, `-`, and `_`, a narrower reading of Google's "URL and
  filename safe base64 string with at most 36 ASCII characters", which would also allow `=`.

Left out on purpose:

- A query key outside geocoding, a `$fields` parameter, and any target.
- Fields that the discovery document of Places API (New) does not list for a place. `routingSummaries` belongs
  to the search endpoints only.
- The deprecated `maxResultCount` of the text search, the experimental `contextualContents`, the
  `searchAlongRouteParameters` that need a polyline of the Routes API, and the `routeModifiers` of the routing
  parameters.
- Photo media: the image is never loaded, its address is never fetched, and a redirect is never followed.
- A tool that creates session tokens: the token is the caller's string and is only passed on.
- `Address Validation`, which can also end an Autocomplete session, and every Geocoding endpoint.
