---
description: >
  Describes the three Google PageSpeed tools: pagespeed.run analyses a page with Lighthouse, crux.query-record and crux.query-history-record read Chrome UX Report field data now and as a time series, with their arguments, forms, and bounded output.
type: knowledge
edit: shared
created: 2026-11-06
updated: 2026-11-06
---

# Google PageSpeed tools

The tools of the [Google PageSpeed provider](README.md). Configuration, quotas, bounds, rate spacing, errors,
and the boundary are described there. Every tool has the effect `read` and is safe to repeat; each repetition
uses quota of the key's project. All results are untrusted third-party content.

`url` and `origin` are pages that Google analyses or looks up. Qatlas never fetches them; it checks only their
form, and refuses other schemes, user information, spaces, and control characters before the key is read.

## google.pagespeed.pagespeed.run

`GET https://pagespeedonline.googleapis.com/pagespeedonline/v5/runPagespeed`. Google loads the page you give
from the Google network and runs Lighthouse on it, so the caller decides which page Google visits. A run takes
seconds up to about a minute; after 50 seconds it ends as `timeout` and is not repeated.

- `url` is required: an absolute `http` or `https` address, a fragment allowed.
- `strategy` is `desktop` (default) or `mobile`.
- `category` is a list of `performance`, `accessibility`, `best-practices`, `seo`, `pwa` (deprecated by
  Lighthouse 12), and `agentic-browsing`. Without it only performance runs. The PageSpeed Insights
  documentation names the first four; the discovery document adds the last two.
- `locale` is a language tag such as `en` or `pt-BR`.
- `utm_campaign` and `utm_source` are 1 to 100 letters, digits, `.`, `_`, `~`, or `-`. They only label the
  request for Google's analytics.

The result has Google's member names; see [PageSpeed Insights result](README.md#pagespeed-insights-result) for
what it keeps, its bounds, and what it leaves out. Screenshots and the detail tables of the audits are not
part of it, and each audit's `details` is a summary of `type`, savings, and `itemCount`. `loadingExperience`
and `originLoadingExperience` are the Chrome field data of the page and of its origin; they are absent when
the Chrome UX Report holds none.

## google.pagespeed.crux.query-record

`POST https://chromeuxreport.googleapis.com/v1/records:queryRecord`. It returns the current record of one page
or one origin.

- `url` or `origin`: exactly one. Both together, or neither, is refused. A `url` takes no fragment; an
  `origin` is a scheme, a host, and an optional port.
- `form_factor` is `phone`, `desktop`, or `tablet`. Without it the data of all form factors is aggregated.
- `effective_connection_type` is `offline`, `slow-2G`, `2G`, `3G`, or `4G`. Without it all are aggregated.
- `metrics` is a list of metric names, each once: `first_contentful_paint`, `first_input_delay`,
  `largest_contentful_paint`, `cumulative_layout_shift`, `interaction_to_next_paint`,
  `experimental_interaction_to_next_paint`, `experimental_time_to_first_byte`,
  `largest_contentful_paint_resource_type`, `largest_contentful_paint_image_time_to_first_byte`,
  `largest_contentful_paint_image_resource_load_delay`, `largest_contentful_paint_image_resource_load_duration`,
  `largest_contentful_paint_image_element_render_delay`, `navigation_types`, `round_trip_time`,
  `experimental_ad_count`, `experimental_ad_density`, `experimental_ad_cpu`, and `experimental_ad_kilobytes`.
  Without it every metric Google has is returned. Google decides which names a lookup can answer.

The result has `record` with the `key` that was looked up, the `collectionPeriod`, and the `metrics`, each with
a `histogram`, the 75th percentile `percentiles.p75`, and, for enumerated metrics, `fractions`.
`urlNormalizationDetails` names the original and the normalised URL when Google changed the `url`.

When the page or origin is not in the dataset, Google answers `NOT_FOUND`. Qatlas reports the class
`not-found`: a result, not a configuration fault.

## google.pagespeed.crux.query-history-record

`POST https://chromeuxreport.googleapis.com/v1/records:queryHistoryRecord`. It returns the history of one page
or one origin as a time series over weekly collection periods.

- `url` or `origin`, `form_factor`, and `metrics` as for `crux.query-record`. The history takes no
  `effective_connection_type`.
- `collection_period_count` is a whole number from 1 to 40. Without it Google returns 25 periods. A value
  outside the range is refused before a request.

The result has `record` with the `key`, the `collectionPeriods` (first and last date of each), and the
`metrics`, each with `histogramTimeseries` (per bin, one density per period), `percentilesTimeseries.p75s`,
and `fractionTimeseries`. Every list has one value per collection period, at most 40, and `NaN` values arrive
as the string `"NaN"`. `urlNormalizationDetails` and `not-found` behave as for `crux.query-record`.
