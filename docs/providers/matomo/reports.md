---
description: >
  Describes the Matomo report tools: the report catalog, report details, segment fields and suggested values, reading one report for a period with bounded rows, filters, and archiving limits, the reads that API.getProcessedReport does not cover: row evolution, distinct counts, the website comparison, and the glossary, and the insights, transitions, and real-time AI chatbot reads.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-25
---

# Matomo reports

The report and segment tools of the [Matomo provider](README.md). Paging follows [Paging](README.md#paging).

## Report catalog

An agent finds reports without knowing Matomo method names. The catalog of a website is what the instance
lists for it in `API.getReportMetadata`, and nothing else: no report is derived from a method name. Reports per
goal, ecommerce, custom dimensions, site search, and plugins are in the catalog only where the website has them;
a report that is missing there is not available. Subtable reports are listed only with `showSubtableReports`.

- `idSite` is required, or taken from the one website the connection binds. A list of websites or `all` is not
  offered.
- A report is identified by its catalog ID, the `uniqueId` of Matomo, such as `Actions_getPageUrls`. It is stable
  only within the instance, so it is looked up in the catalog and never composed. An unknown ID is `not-found`.
- `matomo.reports.list` returns only `id`, `name`, and `category`, optionally filtered by `category` (exact, without
  regard to case) and `query` (a part of the name, ID, or category, without regard to case), in the order of the
  catalog and in batches. Without `category` the first batch also lists the categories with their counts, so the
  whole catalog need not be read.
- `matomo.reports.describe` returns the details of one report: module, action, parameters such as `idGoal`,
  dimension, metrics and calculated metrics, and the action that loads its subtables. The
  explanation of the metrics comes only with `includeMetricsDocumentation` and costs one more request.
- `language`, `period`, `date`, and `showSubtableReports` are optional and shape the catalog: labels differ per
  language, some reports depend on the period and date, and subtable reports are listed only on request. They
  are part of the cache key and of the cursor.
- The catalog is held for five minutes per instance and token, website, and language within a process. Only
  structure is held, no report data and no token; a failure is not held. A goal or plugin change shows after at
  most that long.
- `matomo.segments.fields` lists the segment fields of a website (identifier, name, type, category, and the
  operators of the segment syntax that fit the type) in batches, filtered by `category`.
  `matomo.segments.suggest-values` reads frequent values of one field; they are visitor data, at most 100, and
  empty when Matomo has none, and always empty when the instance disables suggested values
  (`enable_segment_suggested_values`). Neither changes segments.

## Reading a report

`matomo.reports.get` reads one report of a website through `API.getProcessedReport`. The report must be in the
catalog of the website (`report` is the catalog ID, an unknown one is `not-found` before any report is read);
module, method, and report parameters such as `idGoal` or `idDimension` come from the catalog entry, so there is
no argument for them. `period` and `date` are required: `day`, `week`, `month`, `year`, or `range` with a date,
`today`, `yesterday`, a range `YYYY-MM-DD,YYYY-MM-DD`, `lastN`, or `previousN`.

- **Row limits.** Matomo's own default comes from the instance (`API_datatable_default_limit`) and without
  `filter_limit` it returns all rows, so Qatlas always sends `filter_limit` as `limit + 1` with `filter_offset`,
  never `-1`. `limit` is 1 to 1000 and 100 when omitted; there is no way to ask for all rows. The extra row sets
  `has_more`; Matomo reports no total, so a count of rows is not available. `next_cursor` is bound to the
  arguments and to the `limit` of the first call, and offsets stop at 100000.
- **Time series.** With `day`, `week`, `month`, or `year` and a date range or `lastN`/`previousN`, Matomo answers one
  table per period in one call, and every row carries `period_label` (Matomo's localized period text, in
  chronological order). The limit holds over all periods: `limit` times the number of periods must not exceed 1000,
  otherwise the call is refused as `invalid-request` naming the largest `limit`; an omitted `limit` shrinks to fit
  (for 30 days it is 33). A report without a dimension, such as `VisitsSummary_get`, returns one row per period.
  `period=range` sums the dates into one table. Paging applies to every period's table alike.
- **Columns and metadata.** `columns` becomes `showColumns` (the `label` column always stays, calculated metrics are
  covered; with `flat` it needs Matomo 5.14.0). Values are raw numbers (`format_metrics=0`), strings are shortened
  to 512 characters, and nested row members such as comparisons are dropped. Matomo's response always holds the
  report metadata and the row metadata, and no parameter removes them, so Qatlas drops them on its side and returns
  `metadata` and `row_metadata` (for example `idsubdatatable` for `subtable`, and URLs) only with
  `include_metadata`. `hideMetricsDoc` is always on and the timer off. `totals` carries Matomo's report totals.
- **Filters.** `sort_column`, `sort_order`, `filter_column` with `filter_pattern` (a regular expression of the
  instance, up to 100 characters), `filter_excludelowpop` with `filter_excludelowpop_value`, `flat`, `subtable`, and
  `label` (at most 10 labels) map to Matomo's generic filters. Matomo skips its row limit when `label` is set, so
  Qatlas pages those rows itself. `segment` is checked for the shape of its terms; the fields are those of
  `matomo.segments.fields`.
- **Never sent.** `disable_generic_filters`, `disable_queued_filters`, `disable_root_datatable_post_processor`,
  `callback`, `jsoncallback`, `filter_limit=-1`, `idSite=all`, several websites, and raw `apiParameters`; a catalog
  parameter named like one of these makes the report unreadable instead of being sent.
- **Archiving limits.** Matomo cuts large tables when it archives them (for example Actions to 500 rows and
  Referrers to 1000, `datatable_archiving_maximum_rows_*`; `userid_users` to 50000) and sums the rest in a row
  labelled `Others`. The cut rows are not available through the API at any `limit`. When the instance archives on
  demand (`enable_browser_archiving_triggering`), a report, a `range` period, or a segment on a large website can run
  into the timeout.
- **Not covered.** Comparisons (`compareDates`, `comparePeriods`, `compareSegments`) are not offered.

Row labels, titles, URLs, search terms, and event names are visitor data and untrusted.

## Reads beyond `getProcessedReport`

Four tools read what `API.getProcessedReport` does not return. Each calls fixed methods of the Matomo API, takes no
module, method, or URL as an argument, and is offered only through the `tools` list of a connection, in no profile:
their answers can be large, and archiving them on demand can be slow.

- **Row evolution.** `matomo.reports.evolution` follows rows of a catalog report over time (`API.getRowEvolution`).
  The report comes from the catalog like in `matomo.reports.get`; only a report with a dimension can be followed,
  and of its catalog parameters only the entity IDs `idGoal` and `idDimension` are sent. `label` is required, so a
  call never reads every row of a report. `date` must name several periods, and `period` is `day`, `week`, `month`,
  or `year`; a single date and `period=range` are refused, because Matomo cannot answer them. The bound is the one of
  `matomo.reports.get`: labels times periods must not exceed 1000, so one label can cover up to 1000 periods and
  ten labels up to 100. One label answers every metric of the report, several labels answer one `column`. Matomo's
  comparison naming (`labelSeries`) is not offered.
- **Distinct counts.** `matomo.distinct-counts.get` reads one number per period; `kind` picks the Referrers or
  UserCountry method. A kind the instance lacks, because its plugin is inactive or its release is older, ends as
  `unsupported-capability`; AI assistants need Matomo 5.5.0, the release that made them a referrer type.
- **Website comparison.** `matomo.multi-sites.get` reads the MultiSites plugin. It is not instance-wide and needs no
  `instance` target, but Matomo computes the comparison for every website of the token. Rows outside the `site`
  targets are therefore dropped here, row by row, and a batch can be shorter than `limit` or empty while
  `has_more` is true. With targets that name single websites, the group rows, `num_sites`, and `totals` of the
  groups view are left out, because Matomo sums them over all websites of the token. Only one period per call is
  read, as Matomo's comparison refuses several.
- **Glossary.** `matomo.report-glossary.list` reads the explanation of the reports or of the metrics of a website.
  Matomo answers the whole glossary at once; Qatlas reads at most 2000 entries of it and returns them in batches.

## Insights, transitions, and AI chatbots

Three further reads have no entry in the report catalog. Each is in no profile and is offered only where the `tools`
list of a connection names it. They need a view right on the website and an `idSite` inside the targets. Rows are
cut at `limit` (1 to 1000, 100 when omitted) and continued with `next_cursor`, as under
[Reading a report](#reading-a-report); labels, titles, and URLs are visitor data and untrusted.

- `matomo.insights.list` takes `kind`, one fixed Matomo method of the `Insights` plugin: `insights`,
  `movers-and-shakers`, their two `-overview` forms over the reports Matomo puts in its overview, and `can-generate`,
  which tells whether a comparison period exists and takes no website. `insights` and `movers-and-shakers` name the
  report by its catalog ID in `report`. The comparison is on visits; each kind accepts only the thresholds its
  Matomo method has and refuses the others. Matomo's tables are cut on the Qatlas side, and a row of an overview
  carries `report_name`.
- `matomo.transitions.get` reads the previous pages, following pages, outlinks, downloads, and external referrers of
  one page, given as a URL or a title in `page_type` and `page`. The text is matched by Matomo against what it stored
  and is never fetched. Without `parts` the call is `getTransitionsForPageUrl` or `getTransitionsForPageTitle`; with
  `parts` it is `getTransitionsForAction` and Matomo derives no exits. The instance may allow only short periods for
  transitions. Matomo Cloud allows 500 requests per 5 minutes; a 429 is `rate-limited` and never repeated. `limit`
  applies to every list alike.
- `matomo.bot-realtime.get` takes `kind` `chatbots` or `top-pages` and reads the AI chatbot requests of the last
  `last_minutes` (1 to 720, Matomo's 30 when omitted). It needs Matomo 5.12.0 and the `BotTracking` plugin; on an
  older instance it ends as `unsupported-capability` without a Matomo request beyond the version and plugin check.

All three read one period or one table at a time: a date range or `lastN` with `day`, `week`, `month`, or `year`
(one table per period) is refused for insights and transitions; use `period=range`.
