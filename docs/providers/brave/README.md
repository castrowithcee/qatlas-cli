---
description: >
  Describes the Brave Search provider: setup with a Brave API key, plans and the search and answers profiles, cost, rate limits and quota, the billed connection test, goggles, privacy and terms of use, error mapping, limits, and the pages of the tool families.
type: knowledge
edit: shared
created: 2026-10-25
updated: 2026-10-25
---

# Brave Search

Brave Search is a provider for the Brave Search API with one subscription key. It reads the web for agents
(LLM Context), the web for people (Web Search), news, videos, images, places, and the real-time rich results
that a web search announces, and asks Brave for AI-written answers with citations. It changes nothing: every tool is a read, and the connection takes no target,
because a key reaches the endpoints of its plan and not a part of an account.

The tools are described per family: [LLM Context](llm-context.md), [web search and rich results](web.md),
[news](news.md), [videos and images](media.md), [places](places.md), and [Answers](answers.md).

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
`brave.videos.search`, `brave.images.search`, `brave.rich.get`, `brave.places.search`, `brave.pois.get`, and
`brave.poi-descriptions.get`. It is the recommended profile. Brave names the plan only for Place Search;
whether a key reaches the other two place endpoints is shown by the call.

The profile `answers` is the Answers plan: `brave.answers.ask` only. It is not recommended, and its tool is
listed only: a connection reaches it through its `tools` list, never through the `read` effect.

Qatlas manages no subscription, payment, or key.

## Cost

Brave bills every successful request to the key's plan. Qatlas never repeats a request on its own, so one
tool call is at most one billed request. Each tool description says so. Current prices are on the Brave
pricing page. Answers is billed by searches and tokens, see [Answers](answers.md). Brave names no plan or price for `brave.pois.get` and `brave.poi-descriptions.get`; their
descriptions say so.

## Rate limits and quota

Brave limits a key in a sliding one-second window and in a monthly quota. Every answer carries
`X-RateLimit-Limit`, `-Policy`, `-Remaining`, and `-Reset`, with one comma-separated entry per window.
Qatlas reads them after each answer, spaces the next request of the same key by what the first window
implies, and shows the state in the result as `quota`: a list of `windows` with `window_seconds`, `limit`
(0 means unlimited), `remaining`, and `reset_seconds`. A result without rate-limit headers has no `quota`.

Without an answer to learn from, requests of one key are spaced by 250 ms; the spacing is per process. After
an Answers request the next request of the key waits at least 500 ms.

On `429` Qatlas holds the next request for a short window that resets within a minute, and the next call
waits it out. A window that resets later is a quota: the call ends with `rate-limited` and the message
names the seconds until the reset, and nothing waits. The `Retry-After` header is not documented by Brave
and is not read.

## Connection test

`qatlas connection test` sends one real request to the cheapest endpoint the connection offers: News with
`q=brave` and `count=1`, otherwise LLM Context with `maximum_number_of_urls=1`, otherwise Web Search, Videos, Images, or Place Search
(`q=brave`) with `count=1`, whichever tool the connection allows first. The two endpoints that take place ids
are not tested, because an id is known only from a search, and Answers is never tested, because its cost
varies.
Brave has no free key check, so the test spends one billed request. Success proves that the key is valid
for that endpoint only; it shows neither the plan nor any other endpoint. A `422` is never taken as proof
of a valid key, because Brave answers it for invalid parameters and for rejected keys alike.

## Goggles

LLM Context, News, and Web Search take up to three goggles, which re-rank the results. A value is either an `https` address of a
goggle hosted on a code host and registered with Brave, or the inline rules themselves. Brave fetches a
hosted address; Qatlas never does. Qatlas refuses other schemes, user information in an address, control
characters outside inline rules, and values over 2048 characters.

## Privacy and terms of use

Queries, Answers questions, goggles, and the location arguments go to Brave in the USA. Brave keeps the requests of an account
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
| 504, transport timeout | `timeout` | No answer in time (30 seconds; Answers 55 seconds, the rate-limit wait included) |
| other 5xx, 3xx | `provider-error` | A redirect is never followed |

Brave documents the mapping of 402 and 403 to a plan or a balance only loosely, so the messages name both
and claim neither. An unreadable, oversized (over 4 MB; an Answers stream over 8 MiB), or non-JSON answer is `invalid-provider-response`.

## Boundary

The provider reaches only the ten endpoints of the tool pages. It offers no passthrough of method, path, header, or
body. Not offered: Suggest and Spellcheck.

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
