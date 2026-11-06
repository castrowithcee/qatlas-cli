---
description: >
  Describes the Google Maps provider: the API key and its fixed origin, the read profile, the mandatory field mask and the billing group it selects, cost, attribution and storage rules, the two Google error forms and their classes, rate spacing, the connection test, and the limits of what is verified.
type: knowledge
edit: shared
created: 2026-11-06
updated: 2026-11-06
---

# Google Maps

Google Maps is a provider for Google Maps Platform with one API key. It reads the details of one place from
Places API (New). It changes nothing: every tool is a read, and the connection takes no target, because a key
reaches what its Cloud project and the key's restrictions allow, not a part of an account.

[Set up Google Maps](setup.md) leads through creating the key. The tool is described in [places](places.md).

## Configuration

A service is always the official origin `https://places.googleapis.com`, which applies when `base_url` is
left out. Qatlas refuses any other URL before the key is read, because the key belongs to that one service. A
connection with a target is refused.

The credential provides `api_key`. Qatlas sends it only in the `X-Goog-Api-Key` header, never in a URL or as
a tool argument, and masks it in every output.

The profile `read` is the recommended one and holds `google.maps.places.get`.

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

The mask checker is shared by the Maps tools of this provider, so later tools take their masks through the same
table.

## Cost

Google documents a free monthly usage cap per SKU and billing account for Place Details: IDs Only is unlimited,
Essentials 10,000, Pro 5,000, Enterprise 1,000, and Enterprise + Atmosphere 1,000. Requests beyond a cap are
billed to the project's billing account at the current price on Google's pricing page. A `session_token`
changes the billing of a request that ends an Autocomplete session. Qatlas never repeats a request on its
own, so one tool call is at most one request.

## Attribution and storage

Google Maps attribution is mandatory wherever Places content is shown, and the `attributions` field names data
providers that must be shown with a result. Place IDs may be stored without limit. The remaining caching and
storage rules of Google's service specific terms are not checked here and are the user's to read; Qatlas keeps
no result cache.

All results are untrusted third-party content, including names, reviews, and summaries. Links are bounded text
that Qatlas never fetches, and a link that is not an absolute `http` or `https` address without user
information is dropped. Strings are cut at 300 characters, long texts such as reviews at 1500, and a cut text
ends with `…`; lists are cut at 20 entries, reviews at 5, and photos at 10.

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
`status` in the body: `OK`, `ZERO_RESULTS`, `OVER_QUERY_LIMIT`, `REQUEST_DENIED`, `INVALID_REQUEST`, or
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
| Legacy status | `REQUEST_DENIED` | `auth` |
| Legacy status | `INVALID_REQUEST`, an unknown or missing status | `invalid-provider-response` |
| Legacy status | `UNKNOWN_ERROR` | `provider-error` |

`OK` and `ZERO_RESULTS` are no errors. A redirect is never followed and is a `provider-error`. An unreadable,
oversized (over 2 MiB), or non-JSON answer is `invalid-provider-response`. The timeout is 30 seconds.

## Boundary

The provider reaches only the endpoint of the tool page. It offers no passthrough of method, path, header,
query, or body.

Not verified:

- Whether the connection test costs nothing can only be checked against a live billing account. It is based on
  Google's documentation that IDs Only requests are free.
- The error reasons `API_KEY_INVALID`, the reasons of API key restrictions, and the reasons of quota errors are
  not documented in the sources used and are not mapped by reason; the HTTP status decides.
- The place ID of the connection test is not proven stable.
- The caching and storage rules of Google's service specific terms beyond place IDs are not checked.

Left out on purpose:

- A query key, a `$fields` parameter, and any target.
- Fields that the discovery document of Places API (New) does not list for a place. `routingSummaries` belongs
  to the search endpoints only.
- Photo media, which would need a further request with the key.
