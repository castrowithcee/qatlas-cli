---
description: >
  Describes the Brave web search and rich result tools: result groups, paging and end signal, text decorations, summary and rich callback keys, location, bounded JSON, the rich verticals, and third-party attribution.
type: knowledge
edit: shared
created: 2026-10-25
updated: 2026-10-25
---

# Brave web search

The web search and rich result tools of the [Brave Search provider](README.md). Configuration, plans, cost,
rate limits, goggles, privacy, errors, and the boundary are described there.

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
- `offset` and `cursor` work as for [News](news.md), with the difference that the end is Brave's signal:
  `has_more` is true while `query.more_results_available` is true and `offset` is below 9. An answer
  without the signal ends the list. Consecutive pages may overlap. A cursor is also bound to the location,
  `timezone`, and every filter.
- `text_decorations` is sent as `false` unless set, because Brave marks highlights in snippets by default.
- `summary` sends `summary=1` and yields `summarizer.key` and `query.summary_key`, which no tool uses;
  `enable_rich_callback` yields `rich.hint.callback_key`, which `brave.rich.get` reads.
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

## brave.rich.get

`GET /res/v1/web/rich`. It returns the real-time result that `brave.web.search` announces with
`enable_rich_callback` in `rich.hint.callback_key`. The Search plan is needed, and every request is billed.

- `callback_key` is a key, not an address: 1 to 128 letters, digits, underscores, or hyphens, checked
  before the API key is read. Brave documents neither the form nor the lifetime of the key, so Qatlas reads
  the form narrowly and cannot say how long a key lasts. An expired or unknown key ends with the error
  class of the status Brave gives, see [Errors](README.md#errors).
- The answer keeps Brave's members: `type`, `results`, and `response_callback_info`. A result has `type`,
  `subtype` (the vertical), `provider`, `language`, and the object of its vertical: `calculator`,
  `cryptocurrency`, `currency`, `definitions`, `formula1`, `news`, `packagetracker`, `american_football`,
  `football`, `baseball`, `basketball`, `ice_hockey`, `cricket`, `stock`, `unitconversion`,
  `unixtimestamp`, or `weather`. Every member the reference documents for them is shown.
  `callback_status` is `success` or `failure`; a failed callback is a normal answer with no result.
- Lists are cut at 100 entries and the time series of cryptocurrency and currency at 500. No other
  limit differs from Web Search.
- The reference does not type some members. They are shown as bounded JSON, cut as for Web Search:
  `calculator.answer`, `cryptocurrency.amount`, the entries of every `ts_*` time series, the `distance` of
  Formula 1 races, `news.articles[].bo_debug`, `football.content.statistics[].home` and `.away`,
  `football.content.aggregate_score.scores`, the `id` of leagues, games, and teams of every sport, and
  `cricket.content.games[].score.home` and `.away`. The reference types the `content` of the sports and the
  `conversion` of Unix time as any but lists their members; they are shown as objects with those members.
- No member of the reference is left out.
- Brave takes the data from third parties that require attribution when the data is shown: Wordnik
  (definitions), FMP (stock), Fixer (currency), CoinGecko (cryptocurrency), OpenWeatherMap (weather), Stats
  Perform (American football, cricket), and API Sports (baseball, basketball, football, ice hockey,
  Formula 1). Qatlas shows no attribution; whoever shows the data owns that duty, as a limit on reusing it.
