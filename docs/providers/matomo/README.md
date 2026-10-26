---
description: >
  Describes the Matomo provider as a whole: setup with a personal authentication token and a required base URL for on-premise and Matomo Cloud, website and instance targets, version and plugin availability, HTTP 200 error forms, rate limits, paging without a total, the setup profiles, and the tool groups.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-26
---

# Matomo

Matomo is a provider for one Matomo instance, on-premise or Matomo Cloud, through the Matomo HTTP API of
Matomo 5. The API is the same for both, and the token carries the rights of its Matomo user. This page
describes the foundation every Matomo tool shares: connection, targets, availability, errors, paging, and the
setup profiles. The tools are sorted into groups; each group has a page: [sites and version](sites.md),
[reports](reports.md), [live data](live.md), [visitor profiles](visitors.md), [goals](goals.md),
[saved segments](segments.md), [annotations](annotations.md),
[custom dimensions](dimensions.md), [instance settings and reference data](instance.md),
[users and roles](users.md), and [privacy and data subject requests](privacy.md).

## Configuration

A service needs `base_url`; there is no default, not even for Matomo Cloud. It is the address of the
installation, `https://HOST[/PATH]`: an installation below a path is common, and a Matomo Cloud instance is
`https://SUBDOMAIN.matomo.cloud`. Qatlas refuses any other form (no `https`, user information, query,
fragment) before a secret is read, and `qatlas config validate` reports a missing address. A redirect is never
followed. Every request is a `POST` to `<base_url>/index.php`.

The credential provides `token`, a personal authentication token from Matomo under Personal, Security. Create it
for a Matomo user whose rights match what the connection should reach: the token has the view, write, or admin
rights of its user per website, or Super User rights. Qatlas does not check them in advance. The token is sent as
`Authorization: Bearer ...`, never in a URL, a query, or an argument. The credential may also provide the optional
`password-confirmation`, the Matomo password of the token's user, which only the [raw data
tools](privacy.md#anonymizing-and-purging-raw-data) send, in the request body.

```yaml
services:
  analytics:
    provider: matomo
    base_url: https://analytics.example.org

credentials:
  analytics-reader:
    provider: matomo
    type: keyring
```

Operating limits of the instance still apply and surface as authentication or permission errors:

- `only_allow_secure_auth_tokens` makes Matomo accept tokens created for secure use only, and a token of
  another kind is refused.
- `login_allowlist_ip` blocks requests from addresses outside its list, also for the reporting API, so the
  machine that runs Qatlas must be on it.
- A Matomo Cloud subscription and payment data are not available through the instance API.
- A premium plugin that is not installed, and every tool of a plugin that is not activated, is not available.

A successful `qatlas connection test` reads the instance version, requires Matomo 5 or newer, and then reads
each website the targets name once. It proves the token and those websites, not write rights or further
methods.

## Scope

A connection names its targets explicitly or leaves them out.

| Target | Binds |
| --- | --- |
| `site` | every website the token can view |
| `site/ID` | exactly this website; `ID` is the `idSite` shown by `matomo.sites.list` |
| `instance` | the Matomo instance itself, for tools that need Super User rights |

Several targets form a union. `site` and `site/ID` cannot be combined. Without targets a connection reaches
every website of the token and no instance-wide tool; `instance` must be bound explicitly.
A binding to `instance` alone reaches no website tool.

An `idSite` argument must lie inside the targets and is refused before a secret is read or Matomo is contacted,
without naming the website or a target. With `site` the website is proven by Matomo itself, which answers a
website the token cannot view as a permission error. Rows of a list outside the targets are dropped.

## Choosing targets

A person chooses `site/ID` from the websites the token can view, listed with their names in batches of 50 and
searchable by text at Matomo. The list ignores the connection's targets and only reads, through no tool and
for no agent. Names are untrusted and are shortened and cleaned. `instance` is entered by hand. No address of
the Matomo interface is resolved to a target.

## Version and availability

The first call on an instance reads `API.getMatomoVersion`. A release before 5.0 is refused; the connection test
reports it as a provider error and a tool as an unsupported capability. The release is held for ten minutes per
instance and token within a process, so a call sends no extra request; `matomo.version.get` always asks the
instance anew.

Matomo reads the token from the `Authorization` header since 5.4.0. For an older release the header is
ignored; the first call, which cannot know the release yet, therefore sends the header alone and, when
Matomo answers it as unauthenticated, repeats the version read once with the token also in the form field
`token_auth`. From then on `token_auth` is sent only while the held release is older than 5.4.0. If an instance
of 5.4.0 or newer answers only with the field, the token never reaches it through the header and the call fails
as an authentication error.

A tool that needs a later release or a plugin declares it. When the instance has the wrong release, or the plugin
is not activated according to `API.isPluginActivated` (held like the release), or Matomo answers that a method or
module does not exist, the tool ends as an `unsupported-capability` error that names the release or the plugin,
without further requests. There is no method matrix for Matomo 5.0 to 5.14; the answer of the instance decides.

## Errors

Matomo reports application errors as HTTP 200 with `{"result":"error","message":"..."}`, not as 401 or 403. Qatlas
reads the message of that answer only to choose a class: authentication (`auth`), missing right (`permission`),
unknown object (`not-found`), and a missing method, module, or inactive plugin (`unsupported-capability`). Any
other message is `invalid-provider-response`. The message is never copied into a Qatlas error.

Matomo translates its messages into the language of the token's user. The patterns are English; a token whose
user uses another interface language reports unrecognised errors as `invalid-provider-response`.

| Answer | Class |
| --- | --- |
| HTTP 400, other 4xx and 5xx | `provider-error` |
| HTTP 401 | `auth` |
| HTTP 403 | `permission` |
| HTTP 429 | `rate-limited` |
| HTTP 503 | `unreachable` |
| HTTP 504, no answer in time | `timeout` |
| HTML or XML, invalid JSON, an answer over 4 MiB | `invalid-provider-response` |

## Rate limits

On-premise Matomo has no API limit of its own. Matomo Cloud limits requests per IP address and answers with
HTTP 429; its body and headers are not documented. Qatlas spaces the requests of one token by a quarter second,
holds the token for five seconds after a 429 that names no wait (a named wait counts, up to a minute), and
never repeats a request.

## Paging

Matomo lists carry no total. A list tool reads one row more than its batch: only that extra row sets `has_more`,
so a full last page ends the list. `next_cursor` is an opaque cursor bound to the tool, the targets, and the
arguments that shape the list; a cursor that is altered, from another list, or paired with another `limit` is
refused. Methods without server paging are cut on the Qatlas side.

## Setup profiles

The terminal editor starts a new connection on the setup profile `read`, which selects the version, website,
report catalog, report, and segment field tools as well as `matomo.goals.list`, `matomo.goals.get`,
`matomo.segments.list`, `matomo.segments.get`, `matomo.annotations.list`, `matomo.annotations.get`,
`matomo.annotations.count`, `matomo.tracking-code.get`, `matomo.opt-out-code.get`, `matomo.site-settings.get`,
`matomo.consent-manager.detect`, `matomo.dimensions.list`, `matomo.reference-data.list`, and
`matomo.ip-ranges.expand`, `matomo.seo.rank`, and `matomo.js-tracker-install.get-status`; `matomo.site-groups.list` and `matomo.global-settings.get` need the `instance` target
and are in no profile. The [user tools](users.md) `matomo.users.list`, `matomo.users.get`,
`matomo.users.get-access`, and `matomo.roles.list` are in no profile, because they carry personal data and need admin
or Super User rights. The profile `live` adds the [live tools](live.md), the [visitor profile tools](visitors.md), and
`matomo.tracking-failures.list`, and `matomo.ip-locations.get`, because they carry personal data. The tracking failure deletions are in no profile and
`matomo.tracking-failures.delete-all` (target `instance`) is offered only via `tools`. The [privacy tools](privacy.md) `matomo.data-subjects.find`,
`matomo.data-subjects.export`, `matomo.privacy-settings.get`, and `matomo.compliance.get` (target `instance`) are in no
profile, because they carry personal data of other people and need admin or Super User rights. The
[erasure tool](privacy.md#deleting-visits) `matomo.data-subjects.delete`, `matomo.raw-data.anonymize` (targets
`instance` and the websites), and `matomo.data-purge.run` (target `instance`) are in no profile either and are
offered only via `tools`. The
[compliance setters](privacy.md#changing-compliance-policies) `matomo.compliance.set-policy-settings` and
`matomo.compliance.set-status` (target `instance`) are in no profile either; `matomo.compliance.enforce` is offered
only via `tools`.
The [report reads beyond `getProcessedReport`](reports.md#reads-beyond-getprocessedreport) `matomo.reports.evolution`,
`matomo.distinct-counts.get`, `matomo.multi-sites.get`, and `matomo.report-glossary.list` are in no profile and are
offered only via `tools`.
The [insights, transitions, and AI chatbot reads](reports.md#insights-transitions-and-ai-chatbots)
`matomo.insights.list`, `matomo.transitions.get`, and `matomo.bot-realtime.get` and the
[database statistics](instance.md#database-statistics) `matomo.database-statistics.get` (target `instance`) are in no
profile and are offered only via `tools`.
The profile `goals` adds `matomo.goals.add`,
`matomo.goals.update`, and the [goal recommendation tools](goals.md#goal-recommendations), which need a write
right; it is not recommended because it changes the goal configuration of a website. `matomo.goals.delete` is in
no profile, see [goals](goals.md). The profile `segments` adds `matomo.segments.add`, `matomo.segments.update`,
`matomo.segments.star`, and `matomo.segments.unstar`; it is not recommended because it changes the saved segments
of the instance, and `matomo.segments.delete` is in no profile. The profile `annotations` adds
`matomo.annotations.add` and `matomo.annotations.update`; it is not recommended because it changes the annotations
of a website, and the annotation deletions are in no profile. The profile `dimensions` adds
`matomo.dimension-options.list`, which needs a write right, `matomo.dimensions.add`, and
`matomo.dimensions.update`; it is not recommended because a new dimension uses a slot for good. The profile
`sites` adds `matomo.sites.update`, `matomo.site-urls.add`, `matomo.site-urls.set`, and
`matomo.sites.update-created-time`; it is not recommended because it changes the settings of a website, and
`matomo.sites.add` (target `instance`) `matomo.sites.delete`, `matomo.site-groups.rename` (target `instance`), the
eight `matomo.global-settings.set-*` tools, the [operations tools](instance.md#operations)
`matomo.archive-settings.set`, `matomo.trusted-hosts.set`, and `matomo.branding.set` (all target `instance`), and
`matomo.users.logout` (target `instance`) are in no profile; `matomo.trusted-hosts.set` is offered only via `tools`; `matomo.users.delete` (target `instance`), `matomo.access.set`, `matomo.capabilities.add`, and
`matomo.capabilities.remove` are in no profile either and are offered only via `tools`.

## Tools

| Tool | Group | Does |
| --- | --- | --- |
| `matomo.version.get` | [sites](sites.md) | reads the instance version and, with `plugin`, whether that plugin is activated |
| `matomo.sites.list` | [sites](sites.md) | reads websites of the token inside the targets, optionally matching `pattern` |
| `matomo.sites.get` | [sites](sites.md) | reads one website with its addresses, time zone, currency, and tracking settings |
| `matomo.sites.add` | [sites](sites.md) | creates a website (Super User, target `instance`; effect `create`) |
| `matomo.sites.update` | [sites](sites.md) | changes the settings of one website; lists not sent are kept (effect `update`) |
| `matomo.sites.delete` | [sites](sites.md) | deletes a website and its configuration (Super User, targets `instance` and the website; effect `delete`, only via `tools`) |
| `matomo.site-urls.add` | [sites](sites.md) | adds alias URLs to a website, an assignment for tracking only (effect `create`) |
| `matomo.site-urls.set` | [sites](sites.md) | replaces the alias URL list of a website (effect `update`) |
| `matomo.sites.update-created-time` | [sites](sites.md) | sets the creation date of up to 100 websites (effect `update`) |
| `matomo.site-groups.rename` | [sites](sites.md) | renames a website group instance-wide (Super User, target `instance`; effect `update`) |
| `matomo.tracking-code.get` | [sites](sites.md) | reads the JavaScript or image tracking code of a website as text |
| `matomo.opt-out-code.get` | [sites](sites.md#opt-out-code) | reads the opt-out embed code, script or self-contained, as text |
| `matomo.tracking-failures.list` | [sites](sites.md#tracking-failures) | reads the logged tracking failures inside the targets (admin right; profile `live`) |
| `matomo.tracking-failures.delete` | [sites](sites.md#tracking-failures) | deletes one tracking failure of a website (admin right; effect `delete`) |
| `matomo.tracking-failures.delete-all` | [sites](sites.md#tracking-failures) | deletes every tracking failure of the token, beyond the bound websites (admin right, target `instance`; effect `delete`, only via `tools`) |
| `matomo.site-settings.get` | [sites](sites.md) | reads the measurable settings of a website (admin right) |
| `matomo.consent-manager.detect` | [sites](sites.md) | reads the consent manager the Matomo server detects on the website |
| `matomo.site-groups.list` | [sites](sites.md) | reads website groups and their websites (Super User, target `instance`) |
| `matomo.global-settings.get` | [instance](instance.md) | reads the global website settings (admin right, target `instance`) |
| `matomo.global-settings.set-excluded-ips` | [instance](instance.md) | replaces the globally excluded IPs (Super User, target `instance`; effect `update`) |
| `matomo.global-settings.set-excluded-user-agents` | [instance](instance.md) | replaces the globally excluded user agents (Super User, target `instance`; effect `update`) |
| `matomo.global-settings.set-excluded-referrers` | [instance](instance.md) | replaces the globally excluded referrers (Super User, target `instance`; effect `update`) |
| `matomo.global-settings.set-excluded-query-parameters` | [instance](instance.md) | sets the global query parameter exclusion type and list (Super User, target `instance`; effect `update`) |
| `matomo.global-settings.set-search-parameters` | [instance](instance.md) | replaces the global site search keyword and category parameters (Super User, target `instance`; effect `update`) |
| `matomo.global-settings.set-keep-url-fragments` | [instance](instance.md) | sets whether URL fragments are kept by default (Super User, target `instance`; effect `update`) |
| `matomo.global-settings.set-default-currency` | [instance](instance.md) | sets the default currency (Super User, target `instance`; effect `update`) |
| `matomo.global-settings.set-default-timezone` | [instance](instance.md) | sets the default time zone (Super User, target `instance`; effect `update`) |
| `matomo.archive-settings.set` | [instance](instance.md#operations) | sets browser-triggered archiving and the validity of today's reports (Super User, target `instance`; effect `update`) |
| `matomo.trusted-hosts.set` | [instance](instance.md#operations) | replaces the trusted hosts; a wrong list locks the instance out (Super User, target `instance`; effect `update`, only via `tools`) |
| `matomo.branding.set` | [instance](instance.md#operations) | switches the custom logo and favicon on or off (Super User, target `instance`; effect `update`) |
| `matomo.database-statistics.get` | [instance](instance.md#database-statistics) | reads the size of the database by section: general information, server status, and table and report sizes (Super User, target `instance`, only via `tools`) |
| `matomo.reference-data.list` | [instance](instance.md) | reads currencies, time zones, measurable types, and country codes |
| `matomo.ip-ranges.expand` | [instance](instance.md) | reads the lowest and highest address of an IP range |
| `matomo.seo.rank` | [instance](instance.md#diagnostics) | reads SEO metrics of a host, which Matomo asks external services for |
| `matomo.ip-locations.get` | [instance](instance.md#diagnostics) | reads the location of an IP address from Matomo's geolocation (profile `live`) |
| `matomo.js-tracker-install.get-status` | [instance](instance.md#diagnostics) | reads whether a JavaScript tracker install test succeeded for a website |
| `matomo.brute-force-ips.unblock` | [instance](instance.md#diagnostics) | lifts all brute-force blocks (Super User, target `instance`; effect `update`, only via `tools`) |
| `matomo.users.list` | [users](users.md) | reads the users of a website with their role, or the users of the instance (admin right; the instance-wide mode needs the target `instance`) |
| `matomo.users.get` | [users](users.md) | reads one user, checks a login or email, lists the Super Users, or tells whether the token is a Super User (target `instance`) |
| `matomo.users.get-access` | [users](users.md) | reads which websites a user can access, or which users can access a website (admin right; Super User for the raw and instance-wide modes) |
| `matomo.users.delete` | [users](users.md) | deletes a user (admin or Super User, target `instance`; effect `delete`, only via `tools`) |
| `matomo.users.logout` | [users](users.md) | ends the web sessions of a user (Super User, target `instance`; effect `execute`) |
| `matomo.access.set` | [users](users.md) | sets or removes the role of a user on websites (admin; effect `update`, only via `tools`) |
| `matomo.capabilities.add` | [users](users.md) | adds capabilities of a user on websites (admin; effect `update`, only via `tools`) |
| `matomo.capabilities.remove` | [users](users.md) | removes capabilities of a user on websites (admin; effect `update`, only via `tools`) |
| `matomo.roles.list` | [users](users.md) | reads the roles and capabilities Matomo offers (admin right on some website) |
| `matomo.data-subjects.find` | [privacy](privacy.md) | finds the visits of one person on a website by a segment (admin right; Live plugin) |
| `matomo.data-subjects.export` | [privacy](privacy.md) | exports the data of found visits in batches of at most 25, capped at 256 KiB (admin right) |
| `matomo.data-subjects.delete` | [privacy](privacy.md) | deletes for good the data of found visits (admin right; effect `delete`, only via `tools`) |
| `matomo.raw-data.anonymize` | [privacy](privacy.md#anonymizing-and-purging-raw-data) | schedules the anonymization of the logs of listed websites for a date range (Super User, targets `instance` and the websites, role `password-confirmation`; effect `update`, only via `tools`) |
| `matomo.data-purge.run` | [privacy](privacy.md#anonymizing-and-purging-raw-data) | runs the configured data purge now (Super User, target `instance`, role `password-confirmation`; effect `execute`, only via `tools`) |
| `matomo.privacy-settings.get` | [privacy](privacy.md) | reads the anonymization settings of a website or the instance, or the columns that can be anonymized (admin right; Super User for the instance-wide modes) |
| `matomo.compliance.get` | [privacy](privacy.md) | reads the compliance policies and a policy's status or settings (Super User, target `instance`) |
| `matomo.compliance.set-policy-settings` | [privacy](privacy.md#changing-compliance-policies) | enforces or releases single settings of a compliance policy (Super User, target `instance`, Matomo 5.14.0 and feature flag; effect `update`) |
| `matomo.compliance.set-status` | [privacy](privacy.md#changing-compliance-policies) | switches the older whole-policy switch (Super User, target `instance`, Matomo 5.9.0, deprecated in Matomo 6; effect `update`) |
| `matomo.compliance.enforce` | [privacy](privacy.md#changing-compliance-policies) | enforces every toggleable setting of a policy, with final data effects (Super User, target `instance`, Matomo 5.14.0 and feature flag; effect `execute`, only via `tools`) |
| `matomo.reports.list` | [reports](reports.md) | reads the report catalog of a website as id, name, and category, with a category overview |
| `matomo.reports.describe` | [reports](reports.md) | reads the details of one report of the catalog |
| `matomo.reports.get` | [reports](reports.md) | reads one report of a website for a period, in bounded rows |
| `matomo.reports.archive` | [instance](instance.md#operations) | starts the archiving of the reports of a website for a period (Super User, targets `instance` and the website; effect `execute`) |
| `matomo.reports.evolution` | [reports](reports.md) | reads how rows of a report develop over periods (only via `tools`) |
| `matomo.distinct-counts.get` | [reports](reports.md) | reads how many different search engines, social networks, keywords, campaigns, referring websites, AI assistants, or countries a period had (only via `tools`) |
| `matomo.multi-sites.get` | [reports](reports.md) | reads the comparison of the websites of the token inside the targets (only via `tools`) |
| `matomo.report-glossary.list` | [reports](reports.md) | reads the explanation of the reports or metrics of a website (only via `tools`) |
| `matomo.insights.list` | [reports](reports.md#insights-transitions-and-ai-chatbots) | reads the notable changes between two periods: insights, movers and shakers, their overviews, or whether a comparison period exists (only via `tools`) |
| `matomo.transitions.get` | [reports](reports.md#insights-transitions-and-ai-chatbots) | reads the previous and following pages, outlinks, downloads, and external referrers of one page (only via `tools`) |
| `matomo.bot-realtime.get` | [reports](reports.md#insights-transitions-and-ai-chatbots) | reads the AI chatbots and the pages they requested within the last minutes; needs Matomo 5.12.0 and the BotTracking plugin (only via `tools`) |
| `matomo.segments.fields` | [reports](reports.md) | reads the segment fields of a website |
| `matomo.segments.suggest-values` | [reports](reports.md) | reads frequent values of a segment field |
| `matomo.visits.list` | [live](live.md) | reads the visit log of one website |
| `matomo.live-counters.get` | [live](live.md) | reads visits, actions, visitors, and converted visits of the last minutes |
| `matomo.visits.get-latest-time` | [live](live.md) | reads the time of the latest visit per website |
| `matomo.visitors.get-profile` | [visitors](visitors.md) | reads the profile of one visitor, bounded; needs visitor profiles enabled |
| `matomo.visitors.get-latest-id` | [visitors](visitors.md) | reads the visitor ID of the latest visit, optionally within a segment |
| `matomo.visits.get-first` | [visitors](visitors.md) | reads the first visit of one visitor without actions; needs visitor profiles enabled |
| `matomo.goals.list` | [goals](goals.md) | reads the goals of a website |
| `matomo.goals.get` | [goals](goals.md) | reads one goal |
| `matomo.goals.add` | [goals](goals.md) | creates a goal (effect `create`) |
| `matomo.goals.update` | [goals](goals.md) | replaces the definition of a goal (effect `update`) |
| `matomo.goals.delete` | [goals](goals.md) | deletes a goal and its conversions (effect `delete`, only via `tools`) |
| `matomo.goal-recommendations.list` | [goals](goals.md) | reads the saved goal recommendations of a website; needs a write right |
| `matomo.goal-recommendations.scan` | [goals](goals.md) | lets Matomo crawl the website and save goal recommendations, without AI (effect `execute`) |
| `matomo.goal-recommendations.dismiss` | [goals](goals.md) | dismisses one saved recommendation or all of a website (effect `update`) |
| `matomo.segments.list` | [segments](segments.md) | reads the saved segments inside the targets |
| `matomo.segments.get` | [segments](segments.md) | reads one saved segment by its instance-wide ID |
| `matomo.segments.add` | [segments](segments.md) | saves a segment (effect `create`) |
| `matomo.segments.update` | [segments](segments.md) | replaces a saved segment (effect `update`) |
| `matomo.segments.star` | [segments](segments.md) | stars a saved segment; the star belongs to the segment, not the user (effect `update`) |
| `matomo.segments.unstar` | [segments](segments.md) | removes the star from a saved segment (effect `update`) |
| `matomo.segments.delete` | [segments](segments.md) | deletes a saved segment, soft delete (effect `delete`, only via `tools`) |
| `matomo.annotations.list` | [annotations](annotations.md) | reads the annotations of a website in a date range, in batches |
| `matomo.annotations.get` | [annotations](annotations.md) | reads one annotation of a website |
| `matomo.annotations.add` | [annotations](annotations.md) | adds an annotation to a day (effect `create`; Matomo 5 needs view, Matomo 6 write) |
| `matomo.annotations.update` | [annotations](annotations.md) | changes the date, note, or star of an annotation (effect `update`) |
| `matomo.annotations.count` | [annotations](annotations.md) | counts the annotations of a website per period, with starred counts |
| `matomo.annotations.delete` | [annotations](annotations.md) | deletes one annotation for good (effect `delete`, only via `tools`) |
| `matomo.annotations.delete-all` | [annotations](annotations.md) | deletes every annotation of a website; Super User, needs the `instance` target (effect `delete`, only via `tools`) |
| `matomo.dimensions.list` | [dimensions](dimensions.md) | reads the custom dimensions of a website |
| `matomo.dimension-options.list` | [dimensions](dimensions.md) | reads the scopes with free slots and the extraction sources; needs a write right |
| `matomo.dimensions.add` | [dimensions](dimensions.md) | adds a custom dimension, using a slot for good (effect `create`) |
| `matomo.dimensions.update` | [dimensions](dimensions.md) | changes or pauses a custom dimension; Matomo cannot delete one (effect `update`) |

The tools that read are safe to repeat and need no confirmation. Names, addresses, settings, patterns, and
values Matomo returns are untrusted data: they are shortened, and an address is returned as text and never
fetched. `matomo.version.get` needs a view right on any website; the other tools need one on the website read,
the goal changes a write right.
