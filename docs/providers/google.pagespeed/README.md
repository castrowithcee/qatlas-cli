---
description: >
  Describes the Google PageSpeed provider: the API key and the two fixed service origins, the read profile, quotas, how the key travels, the bounds and omissions of the Lighthouse and Chrome UX Report results, error forms and their classes, rate spacing, the connection test, and the limits of what is verified.
type: knowledge
edit: shared
created: 2026-11-06
updated: 2026-11-06
---

# Google PageSpeed

Google PageSpeed is a provider for two Google services that share one API key: PageSpeed Insights, which
analyses a page with Lighthouse and adds Chrome field data, and the Chrome UX Report API, which returns the
field data of a page or origin now and as a history. It changes nothing: every tool is a read, and the
connection takes no target, because a key reaches what its Cloud project allows.

[Set up Google PageSpeed](setup.md) leads through creating the key. The tools are described in
[tools](tools.md).

## Configuration

The services are fixed: `https://pagespeedonline.googleapis.com` for PageSpeed Insights and
`https://chromeuxreport.googleapis.com` for the Chrome UX Report. Leave `base_url` out or set it to the first
one; Qatlas refuses any other value before the key is read, because the key belongs to these two services. A
connection with a target is refused.

The credential provides `api_key`. Neither service accepts the key in a header, so Qatlas sends it as the
query parameter `key`. That makes the full request URL a secret: it never reaches a message, a log, an error,
or a result, the key is masked in every output, and it is never a tool argument.

The profile `read` is the recommended one and holds all three tools.

## Pages that Google visits

`url` and `origin` name pages that Google analyses or looks up. Qatlas never fetches them. For
`pagespeed.run`, Google loads the page from the Google network, so the caller decides which page Google
visits. Qatlas checks only the form: an absolute `http` or `https` address of at most 2048 characters, without
spaces or control characters and without user information. Any other scheme, such as `file:`, is refused
before the key is read. An origin is a scheme, a host, and an optional port, without path, query, or fragment.

## Quotas

The Chrome UX Report API allows 150 queries per minute per Google Cloud project, free of charge. The sources
used for PageSpeed Insights name no quota (see Boundary). Quotas belong to the key's project and are set in
its Cloud console. One tool call is at most one request: Qatlas never repeats a request on its own.

## Results

Everything Google returns is untrusted third-party content, including the audit texts, URLs, and entity names
that come from the analysed page. Texts are cut at 200, 300, or 1000 characters, depending on the member, and
a cut text ends with `…`. Links must be absolute `http` or `https` addresses without user information, or they
are dropped; they are text and are never fetched. A value that has another JSON type than documented is
dropped, with one exception: the numeric members of the Chrome UX Report (`start`, `end`, `density`,
percentiles, fractions) are numbers or, as Google sends some, short strings such as `"0.05"` or `"NaN"`.

### PageSpeed Insights result

The result keeps Google's member names: `kind`, `id`, `captchaResult`, `analysisUTCTimestamp`, `version`,
`loadingExperience`, `originLoadingExperience`, and `lighthouseResult` with `requestedUrl`, `finalUrl`,
`finalDisplayedUrl`, `mainDocumentUrl`, `fetchTime`, `lighthouseVersion`, `userAgent`, `runWarnings`,
`runtimeError`, `configSettings`, `timing`, `environment`, `categories`, `categoryGroups`, `audits`,
`entities`, and `stackPacks`.

| Bound | Value |
| --- | --- |
| Answer read from Google | 10 MiB; a larger answer is `invalid-provider-response` |
| Audits | 300, the first by ID |
| Audit references per category | 200 |
| Entities | 100, with at most 20 origins each |
| Stack packs | 20, with at most 30 descriptions each |
| Run warnings | 20, 500 characters each |
| Loading experience metrics | 30, with at most 20 distribution buckets each |
| Small maps (category groups, credits) | 30 entries |

Left out on purpose:

- `lighthouseResult.fullPageScreenshot`, the final screenshot, the screenshot thumbnails, and every other
  base64 payload. The audits `final-screenshot` and `screenshot-thumbnails` stay, with only the summary
  below.
- The detail tables of the audits (network requests, nodes, items, debug data). Each audit keeps a `details`
  summary instead: its `type`, `overallSavingsMs`, `overallSavingsBytes` where the audit states them, and
  `itemCount`, the number of items.
- `lighthouseResult.i18n`, the icon data of stack packs, the deprecated `configSettings.emulatedFormFactor`,
  and the request parameter `captchaToken`.
- Categories other than `performance`, `accessibility`, `best-practices`, `seo`, `pwa`, and
  `agentic-browsing`.

The 10 MiB cap is a choice: a run of all categories carries two screenshots as base64 and is the largest
answer the tool can request. It is not measured against a live answer. An answer within the cap is cut to a
result of at most a few hundred kilobytes.

### Chrome UX Report result

`queryRecord` returns `record` (`key`, `collectionPeriod`, `metrics`) and `urlNormalizationDetails`. Each
metric has `histogram` (at most 20 bins), `percentiles.p75`, and `fractions` (at most 30). `queryHistoryRecord`
returns `record` with `key`, `collectionPeriods`, and `metrics`; each metric has `histogramTimeseries`,
`percentilesTimeseries.p75s`, and `fractionTimeseries`. Collection periods and every series are cut at 40
values, the most Google returns.

## Rate spacing

Requests of one key are spaced by at least 400 ms within a process, for both services together. That is the
Chrome UX Report limit of 150 per minute and a cautious start for PageSpeed Insights, not a Google limit. On a
`429` the next request of the key waits one second, or the seconds of `Retry-After` up to one minute. The
failed request is not repeated, and a missing `Retry-After` never leads to a retry.

## Connection test

`qconnect connection test` asks the Chrome UX Report API for one metric, `first_contentful_paint`, of the
origin `https://web.dev`, which is an origin of Google's own that the PageSpeed Insights documentation uses as
an example. An answer with data, or `NOT_FOUND`, proves that the key is valid and that the Chrome UX Report API
is enabled in its project; Google checks both before it looks the origin up. The test does not depend on the
origin having data.

**A passing test does not prove PageSpeed Insights.** It does not show that the PageSpeed Insights API is
enabled for the key's project, and it does not show what is left of its daily quota.

## Errors

Qatlas' message never carries Google's text, because `message` and `details` may echo request values. Google's
own answer goes along as the original in the `provider` field of the error detail (untrusted, redacted, and
bounded): the `status`, the `message`, and the type and reason of each detail.

Both services answer a failure with an HTTP status and the Google error object
`{"error":{"code","message","status","details":[...]}}`.

| Answer | Class |
| --- | --- |
| 400 with the reason `API_KEY_INVALID` | `auth` |
| Any other 400 | `provider-error` (the page may be one that Google cannot load) |
| 401 | `auth` |
| 403 | `permission` (check both APIs and the key's restrictions) |
| 404 `NOT_FOUND` of the Chrome UX Report | `not-found`: the origin or page is not in the dataset, a result and not a configuration fault |
| 429 | `rate-limited` |
| 503 | `unreachable` |
| 504, or no answer in 50 seconds | `timeout` |
| Any other status | `provider-error` |

A redirect is never followed and is a `provider-error`. An unreadable, oversized, or non-JSON answer is
`invalid-provider-response`. A timeout is not repeated. The timeout is 50 seconds, below the 60 seconds an
invoke may take; a `pagespeed.run` that needs longer ends as a timeout.

## Boundary

The provider reaches only the endpoints of the tool page. It offers no passthrough of method, path, header,
query, or body.

Not verified:

- No live analysis or lookup with a valid key was made. The shape of the results is taken from the discovery
  documents (PageSpeed Insights v5, revision 20261104; Chrome UX Report v1, revision 20261106) and the
  documentation pages; the size of a real Lighthouse answer is not measured.
- The `API_KEY_INVALID` answer (400) was observed once with a deliberately invalid key on both services; the
  documentation sources used do not state it. Other reasons, such as quota reasons, are not mapped by reason;
  the HTTP status decides.
- A quota failure is not observed. Neither the daily quota nor the per-minute quota of PageSpeed Insights is
  named in the sources used.
- Which metrics the history lookup accepts is not verified; the metric names are the ones the discovery
  document and the Chrome UX Report documentation list for the request, and Google decides.
- The members of the `details` summary other than `type` come from Lighthouse's result format, which the
  discovery document leaves freeform.
- The connection test does not show PageSpeed Insights or its quota.

Left out on purpose:

- The query key as an argument, `$fields`, and any target.
- The request parameter `captchaToken`.
- The form factor value `ALL_FORM_FACTORS`: omit `form_factor` for the aggregate.
