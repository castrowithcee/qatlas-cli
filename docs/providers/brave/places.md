---
description: >
  Describes the Brave place tools: place search by coordinates or name, short-lived place ids, place details, and AI descriptions.
type: knowledge
edit: shared
created: 2026-10-25
updated: 2026-10-25
---

# Brave places

The place tools of the [Brave Search provider](README.md). Configuration, plans, cost, rate limits, goggles,
privacy, errors, and the boundary are described there.

## brave.places.search

`GET /res/v1/local/place_search`. It returns places in one answer, up to 100 (`count`), with no paging.
Brave names the Search plan for it. The answer has the groups `results`, `cities`, `countries`, `regions`,
`neighborhoods`, `addresses`, `streets` (with `pois` and `pois_nearby`), `mixed`, `location`, and `query`.

- The area is given by `latitude` with `longitude`, which must come together, or by `location`, a name such
  as `tokyo japan`; both together are refused. Neither is needed: without them the search is global.
  Qatlas never derives a position. `radius` is a bias in meters, not a limit.
- `q` is optional and takes at most 400 characters and 50 words. Without it Brave returns general places of
  the area.
- `geoloc` is `<latitude>x<longitude>` such as `52.52x13.405`, checked for form and range. It only serves the
  distance values and does not select the area.
- `safesearch` is `strict` and `units` `metric` when omitted.
- A place `id` expires after about 8 hours. Qatlas does not store ids. They feed `brave.pois.get` and
  `brave.poi-descriptions.get`.
- Lists are cut at 20 entries, except `results` at 100 and `mixed` at 200. The reviews of a place are cut
  to 10, each at 1000 characters. `coordinates` and `opening_hours.days` are bounded JSON as for the [web
  search](web.md).

## brave.pois.get

`GET /res/v1/local/pois`. It returns the details of places by id: the members of a place of the place
search plus `reviews`, `pictures`, `action`, `icon_category`, and `timezone_offset`.

- `ids` takes 1 through 20 different ids and sends each as a repeated `ids` parameter. An id is letters,
  digits, `-`, `_`, and trailing `=`, up to 128 characters before the padding. Ids come from
  `brave.places.search` or from `locations.results[].id` of `brave.web.search`, and expire after about 8
  hours.
- `latitude` with `longitude`, together or not at all, becomes `X-Loc-Lat` and `X-Loc-Long`. Brave
  documents no `radius`, `count`, `country`, or `safesearch` here.
- Brave names neither plan nor price for this endpoint. Reviews and the other limits are as for Place
  Search.

## brave.poi-descriptions.get

`GET /res/v1/local/descriptions`. It returns one description per id, written by Brave's AI and so not
necessarily right.

- `ids` is as for `brave.pois.get`. Brave names neither plan nor price. A description is cut at 1500
  characters.
