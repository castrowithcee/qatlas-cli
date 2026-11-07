---
description: >
  Describes the Google Maps geocoding tools: geocoding.geocode turns an address, a components filter, or a place ID into coordinates and address data, geocoding.reverse turns a coordinate into addresses; the key in the query of the legacy endpoint, the argument forms and their rules, status forms and classes, cost, attribution and storage, and what is left out or not verified.
type: knowledge
edit: shared
created: 2026-11-07
updated: 2026-11-07
---

# Google Maps geocoding

The geocoding tools of the [Google Maps provider](README.md): [geocoding.geocode](#googlemapsgeocodinggeocode)
and [geocoding.reverse](#googlemapsgeocodingreverse). Both read and change nothing. The profile `read`
holds them. The key must belong to a Cloud project with the Geocoding API enabled and a billing account; the
connection test of the provider proves only Places API (New), see [Connection test](README.md#connection-test).

## Endpoint and key

Both tools call the legacy Geocoding web service, `GET https://maps.googleapis.com/maps/api/geocode/json`. The
host and the path are fixed; no argument and no configured `base_url` can change them, and a redirect is never
followed. The `base_url` of the connection still has to be the official Places origin, which the provider checks
for every tool.

The legacy service takes no key header, so Qatlas sends the key as the `key` query parameter. This is the only
request of the provider that does so; Places requests send the key in the `X-Goog-Api-Key` header. The URL is
never shown: no message, log entry, or result carries it, a transport error is reduced to a class without the
error text, and the key is registered with the redactor. A request is sent once and never repeated; the
requests of one key share the rate spacing of the provider and the timeout is 30 seconds.

The v4 Geocoding API (`v4beta`) is not used.

## google.maps.geocoding.geocode

Give `address`, `components`, or `place_id`.

- `address` is a street address or plus code of 1 to 1000 characters without control characters.
- `components` is a list of `{type, value}` with `type` `route`, `locality`, `administrative_area`,
  `postal_code`, or `country`. Qatlas builds Google's `type:value|type:value` form, so a value of 1 to 200
  characters may hold neither `|` nor `:` and no control character. `country`, `postal_code`, and `route`
  may appear once, because Google answers a repeat with `INVALID_REQUEST`. `route`, `locality`, and
  `administrative_area` influence results and do not restrict them; `postal_code` matches
  `postal_code` and `postal_code_prefix`; filters that exclude each other end in `ZERO_RESULTS`.
- `address` together with `components` is accepted: Google documents the components filter as an optional
  parameter of an address lookup. What `address` already says must not be repeated in `components`, because
  Google then may answer `ZERO_RESULTS`.
- `place_id` takes 1 to 256 letters, digits, `-`, and `_`. Google documents it as a request parameter of the
  same endpoint, without saying whether it may be combined with the others; Qatlas takes the narrower reading
  and refuses `place_id` together with `address`, `components`, or `bounds`.
- `bounds` is `{southwest, northeast}`, each `{latitude, longitude}`; the southwest must not lie north of the
  northeast. It biases an address or components lookup and restricts nothing.
- `region` is two letters, a ccTLD such as `de` or `uk`, and biases results. `language` is a language tag
  such as `en` or `pt-BR`.
- `address_descriptors` true sends `extra_computations=ADDRESS_DESCRIPTORS` and adds landmarks and areas near
  each result.

Anything else is refused before the key is read and without a request, and the refusal names the rule, not the
value.

## google.maps.geocoding.reverse

`latlng` is required: `{latitude, longitude}` with the latitude from -90 to 90 and the longitude from -180 to
180; a value outside is refused before any request.

- `result_type` is a list of address types Google documents: `street_address`, `route`, `intersection`,
  `political`, `country`, `administrative_area_level_1` to `_7`, `colloquial_area`, `locality`, `sublocality`
  and `sublocality_level_1` to `_5`, `neighborhood`, `premise`, `subpremise`, `plus_code`, `postal_code`,
  `natural_feature`, `airport`, `park`, `point_of_interest`.
- `location_type` is a list of `ROOFTOP`, `RANGE_INTERPOLATED`, `GEOMETRIC_CENTER`, `APPROXIMATE`.
- Both are post-filters: Google searches first and drops what does not match. Several values of one filter are
  an OR, both filters together an AND, and a filter can leave nothing.
- `language`, `region`, and `address_descriptors` as above. A reverse lookup returns one `address_descriptor`
  for the whole answer instead of one per result.

## Result

Both tools return `status` (`OK` or `ZERO_RESULTS`) and `results` in Google's order and member names:

- `address_components` with `long_name`, `short_name`, and `types`
- `formatted_address`, which Google says must not be parsed
- `geometry` with `location` (`lat`, `lng`), `location_type`, `viewport` and `bounds` (`northeast`,
  `southwest`)
- `place_id`, `plus_code` (`global_code`, `compound_code`), `types`, `partial_match`, `postcode_localities`
- `address_descriptor` when asked for: `landmarks` (at most 5, with `place_id`, `display_name`,
  `straight_line_distance_meters`, `travel_distance_meters`, `spatial_relationship`, `types`) and `areas` (at
  most 3, with `place_id`, `display_name`, `containment`)

Results are untrusted third-party content. Strings are cut at 300 characters, lists at 20 entries, with
exceptions: results at 50, address components and types at 40, postcode localities at 100. A cut text ends
with `…`. Members that Google does not document here, and `error_message`, are dropped. `ZERO_RESULTS` is no
error but an empty `results` list, which the core leaves out of the published result.

## Status and errors

Google answers with HTTP 200 and a `status` in the body. The body's `error_message` is provider text: it goes
along as the original in the `provider` field of the error detail and never into Qatlas' message.

| `status` | Class |
| --- | --- |
| `OK` | result |
| `ZERO_RESULTS` | empty `results` |
| `OVER_QUERY_LIMIT` | `rate-limited` |
| `OVER_DAILY_LIMIT` | `permission` |
| `REQUEST_DENIED` | `auth` |
| `INVALID_REQUEST` | `invalid-provider-response` (Qatlas checked the arguments before sending) |
| `UNKNOWN_ERROR` | `provider-error` (not repeated) |
| any other value or none | `invalid-provider-response` |

Google lists a missing or invalid key, disabled billing, a self-imposed cap, and an invalid payment method
together under `OVER_DAILY_LIMIT`. None of them clears by waiting and Google does not say which applies, so
Qatlas maps it to `permission`, the class that points at the key and its project. `REQUEST_DENIED` is `auth`.

An HTTP status other than 200 is mapped by status: 401 `auth`, 403 `permission`, 404 `not-found`, 429
`rate-limited` (the next request of the key waits), 503 `unreachable`, 504 `timeout`, a redirect or other status
`provider-error`. A non-JSON, unreadable, or oversized (over 2 MiB) answer is `invalid-provider-response`.

## Cost, attribution, and storage

Every call is one request and can be billed. Google bills all Geocoding requests, address, reverse, and place
ID, with the SKU Geocoding in the Essentials group, and its price list names a free usage of 10,000 events per
month for it (Google's pages: usage and billing, pricing). The price list may change and Qatlas does not read
it. Google states no billing for `extra_computations`; it lists address descriptors as experimental outside
India.

Google Maps attribution is mandatory wherever geocoding content is shown. Place IDs may be stored without
limit. Google's policy page allows temporary caching of latitude and longitude values for up to 30 consecutive
calendar days; the other storage rules of the service specific terms are not checked here and are the user's to
read. Qatlas keeps no result cache.

Addresses can identify people. The risk class of both tools is `google-maps-address-data`, which marks the
data as personal address data; the invoke log holds neither arguments nor results.

## Boundary

Left out on purpose:

- `extra_computations=BUILDING_AND_ENTRANCES` and the members `navigation_points`, entrances, building
  outlines, and grounds: Google's release notes list them as no longer supported in the legacy service.
- The v4 API (`v4beta`), the XML output, a key in a header, and any target.
- Address Validation and every other Maps service.

Not verified:

- Whether the legacy service takes an API token other than a key; only an API key is used.
- Whether `place_id` may be combined with other inputs; the narrower reading is used.
- The billing of `extra_computations` and any free usage beyond what Google's price list names.
- The reasons behind `OVER_DAILY_LIMIT` and `REQUEST_DENIED`; Google names several and the status alone decides.
- The members `info_messages` and `next_page_token`, which the pages used do not describe.
