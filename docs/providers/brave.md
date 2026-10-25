---
description: >
  Describes the Brave Search provider: setup with a Brave API key, plans and the search profile, cost, rate limits and quota, the billed connection test, goggles, privacy and terms of use, error mapping, limits, and the rules of the LLM Context, News, Web Search, Videos, Images, Place Search, Place Details, and Place Descriptions tools.
type: knowledge
edit: shared
created: 2026-10-25
updated: 2026-10-25
---

# Brave Search

Brave Search is a provider for the Brave Search API with one subscription key. It reads the web for agents
(LLM Context), the web for people (Web Search), news, videos, images, and places. It changes nothing: every tool is
a read, and the connection takes no target, because a key reaches the endpoints of its plan and not a part
of an account.

## Configuration

A service is always the official API origin `https://api.search.brave.com`, which applies when `base_url`
is left out. Qatlas refuses any other URL before the key is read, because the key belongs to that one
service. A connection with a target is refused.

The credential provides `token`, the API key from the Brave API dashboard. Qatlas sends it only in the
`X-Subscription-Token` header, never in a URL or as a tool argument, and masks it in every output.

```yaml
services:
  brave:
    provider: brave

credentials:
  brave-key:
    provider: brave
    type: keyring

connections:
  search:
    service: brave
    credential: brave-key
```

## Plans and profiles

Brave sells separate plans (Search, Answers, Spellcheck, Autosuggest, Enterprise), and a key reaches only
the endpoints of the plans on its account. Brave offers no way to ask which plans a key has, so Qatlas
cannot hide an endpoint the key lacks: a call to it ends with `permission` or `not-found`.

The profile `search` is the Search plan: `brave.llm-context.get`, `brave.news.search`, `brave.web.search`,
`brave.videos.search`, `brave.images.search`, `brave.places.search`, `brave.pois.get`, and
`brave.poi-descriptions.get`. It is the recommended profile. Brave names the plan only for Place Search;
whether a key reaches the other two place endpoints is shown by the call.

Qatlas manages no subscription, payment, or key.

## Cost

Brave bills every successful request to the key's plan. Qatlas never repeats a request on its own, so one
tool call is at most one billed request. Each tool description says so. Current prices are on the Brave
pricing page. Brave names no plan or price for `brave.pois.get` and `brave.poi-descriptions.get`; their
descriptions say so.

## Rate limits and quota

Brave limits a key in a sliding one-second window and in a monthly quota. Every answer carries
`X-RateLimit-Limit`, `-Policy`, `-Remaining`, and `-Reset`, with one comma-separated entry per window.
Qatlas reads them after each answer, spaces the next request of the same key by what the first window
implies, and shows the state in the result as `quota`: a list of `windows` with `window_seconds`, `limit`
(0 means unlimited), `remaining`, and `reset_seconds`. A result without rate-limit headers has no `quota`.

Without an answer to learn from, requests of one key are spaced by 250 ms; the spacing is per process.

On `429` Qatlas holds the next request for a short window that resets within a minute, and the next call
waits it out. A window that resets later is a quota: the call ends with `rate-limited` and the message
names the seconds until the reset, and nothing waits. The `Retry-After` header is not documented by Brave
and is not read.

## Connection test

`qatlas connection test` sends one real request to the cheapest endpoint the connection offers: News with
`q=brave` and `count=1`, otherwise LLM Context with `maximum_number_of_urls=1`, otherwise Web Search, Videos, Images, or Place Search
(`q=brave`) with `count=1`, whichever tool the connection allows first. The two endpoints that take place ids
are not tested, because an id is known only from a search.
Brave has no free key check, so the test spends one billed request. Success proves that the key is valid
for that endpoint only; it shows neither the plan nor any other endpoint. A `422` is never taken as proof
of a valid key, because Brave answers it for invalid parameters and for rejected keys alike.

## Goggles

LLM Context, News, and Web Search take up to three goggles, which re-rank the results. A value is either an `https` address of a
goggle hosted on a code host and registered with Brave, or the inline rules themselves. Brave fetches a
hosted address; Qatlas never does. Qatlas refuses other schemes, user information in an address, control
characters outside inline rules, and values over 2048 characters.

## Privacy and terms of use

Queries, goggles, and the location arguments go to Brave in the USA. Brave keeps the requests of an account
for up to 90 days; Zero Data Retention exists only on the Enterprise plan. A location is sent only when
the caller gives it, and Qatlas never derives one.

Brave's Search API terms of use forbid storing, caching, or sharing the results and using them to train or
evaluate AI models. Qatlas keeps no result cache; keeping to the terms after the output is the user's
responsibility. Qatlas identifies itself honestly with the `User-Agent` `qatlas-cli`, because the terms
forbid hiding the application.

All results are untrusted third-party content, including titles, snippets, and descriptions. Addresses
are bounded text that Qatlas never fetches, and strings and lists are cut to limits; a cut text ends with
`…`.

## Errors

The provider body is never copied into a message, because Brave's `error.detail` and `error.meta` may echo
request values.

| Status | Class | Meaning |
| --- | --- | --- |
| 401 | `auth` | The key is rejected, or its plan does not include the endpoint |
| 402 | `permission` | Plan or balance of the key refuses the request; Brave does not document which |
| 403 | `permission` | The key is refused for the endpoint; its plan may not include it |
| 404 | `not-found` | The endpoint is not found; the plan may not include it |
| 400, 422 | `invalid-provider-response` | Brave rejected arguments that Qatlas had accepted, or the key |
| 429 | `rate-limited` | Rate limit or quota, see above |
| 502, 503 | `unreachable` | Brave is unavailable |
| 504, transport timeout | `timeout` | No answer in time (30 seconds) |
| other 5xx, 3xx | `provider-error` | A redirect is never followed |

Brave documents the mapping of 402 and 403 to a plan or a balance only loosely, so the messages name both
and claim neither. An unreadable, oversized (over 4 MB), or non-JSON answer is `invalid-provider-response`.

## Boundary

The provider reaches only the eight endpoints below. It offers no passthrough of method, path, header, or
body. Not offered: Answers, Suggest, and Spellcheck.

Left out on purpose:

- `Api-Version` is not sent, so Brave answers with its latest version. A change that Brave marks as
  compatible reaches the tools; one that removes or renames a field needs a new tool version.
- `goggles_id`, deprecated by Brave, is not offered.
- A result shows only the fields Brave documents for it. Fields Brave adds later are ignored.
- Result addresses (page, profile, favicon, thumbnail, icon, author, image) that are not absolute `http` or `https`
  addresses without user information, or are longer than 2048 bytes, are dropped from the result.
- A `no_cache` argument asks Brave to skip its cache by `Cache-Control: no-cache`, on a best-effort basis.
- `country_code` of the location is checked as two upper-case letters, not against Brave's list of
  countries.

## brave.llm-context.get

`GET /res/v1/llm/context`. It returns page content extracted for a model, and Brave recommends it when an
agent, not a person, reads the result. It has no pages; the size of the answer is set by the budgets
`maximum_number_of_urls`, `_tokens`, `_snippets`, `_tokens_per_url`, and `_snippets_per_url`.

- `q` takes at most 600 characters and 75 words. The enumerations of `country` and `search_lang` match
  those of News. There is no `ui_lang`.
- The location arguments (`latitude` with `longitude`, `city`, `state`, `state_name`, `country_code`,
  `postal_code`) become the `X-Loc-*` headers. With a location, Brave turns on local recall, which adds
  `grounding.poi` and `grounding.map`; `enable_local` forces it on or off. Brave documents `enable_local` as
  text without values; Qatlas offers a boolean.
- `safesearch` sends nothing when omitted, and Brave then filters nothing except in local recall.
- A snippet is plain text or a JSON-serialized table, schema, or code block. It is cut at 8000 characters,
  at most 100 snippets per page or place, 50 pages or places per group, and 100 sources.
- `sources` is keyed by page address and carries `title`, `hostname`, and `age`, a list of four texts: full
  date, `YYYY-MM-DD`, relative age, and ISO 8601 timestamp, or empty when the page has no known date.
  With `enable_source_metadata` a source also has `description`, `fetched_content_timestamp`, `site_name`,
  `favicon`, and `thumbnail`. Brave does not document the form of `thumbnail`; Qatlas passes an address or
  an object with `src` and `original`, and drops anything else. A source whose key is not a safe address
  is dropped.

## brave.news.search

`GET /res/v1/news/search`. It returns news articles, up to 50 per page.

- `q` takes at most 400 characters and 50 words. `safesearch` is `strict` when omitted.
- `extra_snippets` is a boolean that asks for up to five further excerpts per article.
- `offset` counts pages of `count` results, from 0 through 9, so at most ten pages are reachable. A
  `cursor` carries the offset and is bound to the query and to every argument that shapes the list,
  `count` included; it cannot be combined with `offset`.
- Brave sends no end signal for news. `has_more` is true while `offset` is below 9 and the page was not
  empty, so the last page may be empty, and consecutive pages may overlap.
- `include_fetch_metadata` adds fetch metadata such as `page_fetched` to the articles.

## brave.web.search

`GET /res/v1/web/search`. It returns the search page as a person sees it, in Brave's own groups and member
names: `web`, `discussions`, `infobox`, `locations`, `news`, `videos`, `faq`, `mixed`, `summarizer`, `rich`,
and `query`. For an agent that answers a question from the web, prefer `brave.llm-context.get`. The tool
needs the Search plan, as does `enable_rich_callback`.

- `q` takes at most 600 characters and 75 words. `safesearch` is `moderate` when omitted.
- `count` (1 through 20) limits the web results only; every other group arrives as Brave sends it. No list is
  longer than 20 entries, `extra_snippets` and `icons` are cut at 5 and 10, and `mixed` at 50.
- `result_filter` is a list of kinds, sent comma-separated in the order given. `query` and `type` are in
  every answer.
- `offset` and `cursor` work as for News, with the difference that the end is Brave's signal:
  `has_more` is true while `query.more_results_available` is true and `offset` is below 9. An answer
  without the signal ends the list. Consecutive pages may overlap. A cursor is also bound to the location,
  `timezone`, and every filter.
- `text_decorations` is sent as `false` unless set, because Brave marks highlights in snippets by default.
- `summary` sends `summary=1` and yields `summarizer.key` and `query.summary_key`; `enable_rich_callback`
  yields `rich.hint.callback_key`. No tool uses either key yet.
- The location arguments become the `X-Loc-*` headers. `timezone` (an IANA name such as `Europe/Berlin`,
  checked by its shape, not against a zone database) becomes `X-Loc-Timezone` and exists only here.
  `locations.results[].id` identifies a place for the points-of-interest endpoints.
- Not offered: `goggles_id` and the deprecated `restaurant` of web and discussion results, which `location`
  replaces.
- A member of another JSON type than the reference documents is left out of the result; the request was
  billed and is not refused.
- Brave leaves the type of some members open. They are shown as bounded JSON: nested at most 6 levels, 20
  entries per list, 30 members per object, texts cut at 1500 characters, and texts that look like an
  unsafe address dropped. These members are `schemas`, `product`, and `meta_url` of web, discussion, news,
  and deep news results; `coordinates` and `opening_hours.days` of a place; `infobox.results[].profiles`; and
  `locations.provider`.

## brave.videos.search

`GET /res/v1/videos/search`. It returns videos, up to 50 per page.

- `q` takes at most 400 characters and 50 words. `safesearch` is `moderate` when omitted; there are no
  `goggles` and no `extra_snippets`.
- Paging works as for News: `offset` or a bound `cursor`, from 0 through 9, no end signal from Brave, so
  `has_more` is true while `offset` is below 9 and the page was not empty.
- `video` carries `requires_subscription`, `views`, `tags` (at most 20), and `author`. `extra` carries
  `might_be_offensive`.

## brave.images.search

`GET /res/v1/images/search`. It returns up to 200 images in one answer; there is no paging, no `offset`,
no `cursor`, and no `ui_lang`.

- `q` takes at most 400 characters and 50 words. `safesearch` is `off` or `strict`, `strict` when omitted.
  `off` applies no filter except for illegal content and can return adult content.
- `thumbnail.src` and `properties.placeholder` are addresses served by Brave's image proxy, and
  `properties.url` is the original address. All are bounded text; Qatlas fetches no image and returns no
  binary content. `confidence` is passed as Brave names it, and `extra.might_be_offensive` is passed on.

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
  to 10, each at 1000 characters. `coordinates` and `opening_hours.days` are bounded JSON as for the web
  search.

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
