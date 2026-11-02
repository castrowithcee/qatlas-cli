---
description: >
  Describes the Matomo custom report tools: listing and reading the configured custom reports of a website, the custom metrics of the instance and their websites, the options reports are built from, creating, replacing, copying, pausing, and resuming reports, the website and instance targets, the rights, the plugin requirement, and the limits of what the CustomReports reference proves.
type: knowledge
edit: shared
created: 2026-11-02
updated: 2026-11-02
---

# Matomo custom reports

The tools for the configuration of custom reports and custom metrics of the [Matomo provider](README.md). They
need the premium plugin CustomReports; an instance without it refuses all of them as an unsupported capability,
after one plugin check and without further requests. The tools are in no [setup profile](README.md#setup-profiles).
The data of a custom report is read with `matomo.reports.get` like any other report. Deleting reports and creating or
changing custom metrics is not offered.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.custom-reports.list` | reads the configured custom reports of a website, in batches |
| `matomo.custom-reports.get` | reads the configuration of one custom report of a website |
| `matomo.custom-metrics.list` | reads the custom metrics of the instance, or the websites with data for them |
| `matomo.custom-report-options.list` | reads what a report can be built from, for a website |
| `matomo.custom-reports.add` | creates a custom report on a website (not idempotent) |
| `matomo.custom-reports.update` | replaces the whole configuration of a custom report |
| `matomo.custom-reports.duplicate` | copies a custom report to one or more websites (not idempotent) |
| `matomo.custom-reports.pause` | pauses a custom report, or with `resume` true resumes it |

- `matomo.custom-reports.list` takes `skipCategoryMetadata`, which keeps only the category ID of each report.
- `matomo.custom-metrics.list` takes `kind`, which chooses the method: `metrics` for the custom metrics
  (`getCustomMetrics`), or `websites` for the websites with data for them (`getWebsiteMetricsForCustomMetric`).
  The websites are read in batches that continue after the last website of the previous batch; the batch size is
  at most 99, because one row more than the batch is asked for.
- `matomo.custom-report-options.list` reads the categories, report types, dimensions, and metrics a report can use,
  and the metrics a custom metric can be built from. Answers the reference gives no example for are returned as
  Matomo gives them, shortened.

## Changing reports

The change tools need `confirm`, send one request, and never repeat it: after an unclear result (timeout, reset
connection, server error, unreadable answer) the error says so and points to `matomo.custom-reports.list` or
`matomo.custom-reports.get` to read the report before trying again.

- `update` replaces the report as a whole. `name`, `reportType`, and `metricIds` are required; an optional setting
  that is not passed (also `subCategoryReportIds` and `multipleIdSites`) is not sent, so Matomo applies its default
  and may reset a value the report had. Nothing is filled in from the report: read it first and pass every setting
  to keep.
- `pause` calls exactly one method per call: `pauseCustomReport`, or `resumeCustomReport` when `resume` is true.
- `duplicate` takes `idDestinationSites`; the source website is the only destination when it is omitted.
- Identifiers (report type, metrics, dimensions, category, subcategory) hold letters, digits, underscore, dot, and
  hyphen; `segmentFilter` has the form of the segment tools. Reports of "all websites" are not offered, so `idSite`
  is always one website.

Report names, descriptions, segments, category names, and metric formulas are untrusted provider data. They are
shortened and returned as text; a report shows at most 100 dimensions and 100 metrics. The logins that created or
changed a report, and the preformatted dates, are not returned.

## Targets

The custom report tools take `idSite`, which must be inside the targets ([Scope](README.md#scope)) and is checked
before any secret is read; a website outside them is refused without naming it. The change tools check every
website in `multipleIdSites` and `idDestinationSites` the same way: one website outside the targets refuses the
whole call before any secret is read or request is sent.

A report ID is not tied to a website in the request, so the answer must carry the website it belongs to: a report
of another website, or an answer without a website, is not shown and ends as `not-found` without naming another
website; in a list such rows are dropped. A report that spans several websites (`multipleIdSites`) is shown only
when every website it names is inside the targets; one that names a website outside them, or in a form that cannot
be read as website IDs, is dropped from the list and is `not-found` when read by ID. `update`, `duplicate`, and `pause` read the report
once under this rule before the change; a report that does not pass is `not-found` and nothing is changed.

Custom metrics belong to the instance and carry no website. `matomo.custom-metrics.list` needs the target
`instance` ([Scope](README.md#scope)) and Super User rights, and is checked before any secret is read. The rows of
the websites cover the whole instance and are filtered by the website targets of the connection: rows of other
websites are dropped without a mention, so a batch can be empty while `has_more` is true. A connection that binds
no website is refused for `kind` `websites`.

## Rights of the options tool

The rights are not checked in advance. When Matomo refuses an option for the right of the token, the option is left
out of the result and named in `unavailable` with the reason `permission`; the result is never silently shortened.
The metrics for a custom metric need Super User rights, so they are asked for only when the connection binds
`instance`; otherwise they are named in `unavailable` with the reason `instance-target`, without a request. When
Matomo refuses every option that was asked for, the call fails with a permission error. Any other failure of one
option (unsupported plugin or method, rate limit, timeout, an unreadable answer) ends the whole call.

## Limits

- Matomo's demo states a view right for the configured reports and the categories, a write right on at least one
  website for the report types, and Super User rights for the custom metrics, their websites, and the metrics for a
  custom metric. Dimensions and metrics require a logged-in user; the level is not stated.
- The method signatures come from the Matomo 6 reference. They are not verified against Matomo 5 (CustomReports 5.6.2);
  no version is checked per method, and an instance that lacks one ends as an unsupported capability.
  By the plugin changelog `multipleIdSites` exists from CustomReports 5.2.0, pausing from 5.3.0, and copying from
  5.4.4.
- The reference gives no example for the change methods. Assumed answers: `addCustomReport` the ID of the new
  report, `updateCustomReport`, `pauseCustomReport`, and `resumeCustomReport` the generic success form,
  `duplicateCustomReport` an answer that does not say it failed, or an ID; any other form ends as an unclear result.
  The write right on the website is evidenced by the changelog entry of 5.6.2 ("missing write access").
- The shapes of the answers of `getCustomMetrics`, `getWebsiteMetricsForCustomMetric`, `getAvailableReportTypes`,
  `getAvailableDimensions`, `getAvailableMetrics`, and `getAvailableMetricsForCustomMetric` are not verified; they
  are returned as Matomo gives them, shortened, and a website row of `getWebsiteMetricsForCustomMetric` must carry
  `idsite` in ascending order or the call ends as an invalid response. The parameter types are derived. The shape of
  a report and of the categories is taken from the demo.
- The batch size of 99 websites and the limits of 100 entries per list are Qatlas's own.
- The error form for a missing method, module, or inactive plugin is recognized by the common Matomo messages as an
  unsupported capability; an unknown report ends as `not-found` by the common message or an empty answer.
