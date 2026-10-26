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
[saved segments](segments.md), [annotations](annotations.md), [scheduled reports](scheduled-reports.md),
[custom alerts](alerts.md), [custom dimensions](dimensions.md), [instance settings and reference data](instance.md),
[users and roles](users.md), [SMS settings](mobile-messaging.md), [privacy and data subject requests](privacy.md),
[AI providers](ai-providers.md), [Tag Manager](tag-manager.md), and [tracking](tracking.md).

To connect Matomo step by step, see [Set up Matomo](setup.md).

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
tools](privacy.md#anonymizing-and-purging-raw-data), the [retention setters](privacy.md#setting-retention-periods),
and the IP anonymization setter with `randomizeConfigId` ([privacy settings](privacy.md#changing-privacy-settings))
send, in the request body.

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
other message is `invalid-provider-response`. The message never enters Qatlas' own error message. Matomo's answer
goes apart as the original answer in the `provider` field of the error detail (untrusted, redacted, limited): for
`auth`, `permission`, `not-found`, `provider-error`, and `invalid-provider-response` errors the message of an
application error, and for an HTTP status other than 200 the message of the same form or the text of a non-JSON
answer. A refused segment or name, an LDAP filter, or a setting ID and a missing method carry no original answer.

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
`matomo.tracking-failures.list`, `matomo.ip-locations.get`, `matomo.scheduled-reports.list`, and
`matomo.scheduled-reports.generate`, and the four [alert](alerts.md) read tools, because they carry personal data. The tracking failure deletions are in no profile and
`matomo.tracking-failures.delete-all` (target `instance`) is offered only via `tools`, as are `matomo.scheduled-reports.delete` and `matomo.alerts.delete`. The [privacy tools](privacy.md) `matomo.data-subjects.find`,
`matomo.data-subjects.export`, `matomo.privacy-settings.get`, and `matomo.compliance.get` (target `instance`) are in no
profile, because they carry personal data of other people and need admin or Super User rights. The
[erasure tool](privacy.md#deleting-visits) `matomo.data-subjects.delete`, `matomo.raw-data.anonymize` (targets
`instance` and the websites), and `matomo.data-purge.run` (target `instance`) are in no profile either and are
offered only via `tools`. The
[compliance setters](privacy.md#changing-compliance-policies) `matomo.compliance.set-policy-settings` and
`matomo.compliance.set-status` (target `instance`) are in no profile either; `matomo.compliance.enforce` is offered
only via `tools`. The [privacy setters](privacy.md#changing-privacy-settings)
`matomo.privacy-settings.set-anonymize-ip`, `matomo.do-not-track.enable`, and `matomo.do-not-track.disable` (target
`instance`) are in no profile. The [retention setters](privacy.md#setting-retention-periods) are in no profile
and the two deleting ones are offered only via `tools`.
The [report reads beyond `getProcessedReport`](reports.md#reads-beyond-getprocessedreport) `matomo.reports.evolution`,
`matomo.distinct-counts.get`, `matomo.multi-sites.get`, and `matomo.report-glossary.list` are in no profile and are
offered only via `tools`.
The [insights, transitions, and AI chatbot reads](reports.md#insights-transitions-and-ai-chatbots)
`matomo.insights.list`, `matomo.transitions.get`, and `matomo.bot-realtime.get` and the
[database statistics](instance.md#database-statistics) `matomo.database-statistics.get` (target `instance`) are in no
profile and are offered only via `tools`.
The [Tag Manager tools](tag-manager.md) `matomo.tag-manager-containers.list`, `matomo.tag-manager-containers.get`,
`matomo.tag-manager-containers.get-embed-code`, `matomo.tag-manager-versions.list`, `matomo.tag-manager-versions.get`,
`matomo.tag-manager-versions.export`, `matomo.tag-manager-versions.create`, `matomo.tag-manager-versions.update`,
`matomo.tag-manager-versions.delete`, `matomo.tag-manager-tags.list`, `matomo.tag-manager-tags.get`, `matomo.tag-manager-tags.add`,
`matomo.tag-manager-tags.update`, `matomo.tag-manager-tags.resume`,
`matomo.tag-manager-triggers.list`, `matomo.tag-manager-triggers.get`, `matomo.tag-manager-triggers.add`,
`matomo.tag-manager-triggers.update`, `matomo.tag-manager-triggers.delete`, `matomo.tag-manager-tags.delete`,
`matomo.tag-manager-tags.pause`, `matomo.tag-manager-variables.list`,
`matomo.tag-manager-variables.get`, `matomo.tag-manager-variables.add`, `matomo.tag-manager-variables.update`,
`matomo.tag-manager-variables.delete`, `matomo.tag-manager-variables.list-available`,
`matomo.tag-manager-references.list`, `matomo.tag-manager-catalog.list`,
`matomo.tag-manager-containers.create-default`, `matomo.tag-manager-containers.add`,
`matomo.tag-manager-containers.update`, and `matomo.tag-manager-containers.delete` are in no
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
`matomo.users.logout` (target `instance`) are in no profile; `matomo.trusted-hosts.set` is offered only via `tools`; `matomo.users.delete` (target `instance`), `matomo.access.set`, `matomo.capabilities.add`,
`matomo.capabilities.remove`, and the two `matomo.ldap-users.*` tools (target `instance`) are in no profile either and are offered only via `tools`.
The [tracking tools](tracking.md) are in no profile and are offered only via
`tools`, because they write raw data that cannot be undone.

## Tools

| Group | Tools |
| --- | --- |
| [sites and version](sites.md) | version, websites, tracking and opt-out code, tracking failures, website settings, consent manager, website groups |
| [instance settings and reference data](instance.md) | global website settings, archiving, trusted hosts, branding, reference lists, IP ranges, database statistics, diagnostics |
| [users and roles](users.md) | users, their access to websites, roles and capabilities, LDAP user counts and list, deleting and logging out users |
| [privacy and data subject requests](privacy.md) | finding, exporting, and deleting the visits of one person, privacy settings, retention, compliance policies |
| [reports](reports.md) | report catalog, reading reports, evolution, distinct counts, website comparison, glossary, insights, transitions, AI chatbots, segment fields |
| [live data](live.md) | visit log, live counters, latest visit time |
| [visitor profiles](visitors.md) | visitor profile, latest visitor ID, first visit of a visitor |
| [AI providers](ai-providers.md) | AI provider settings without API keys, disconnecting a provider |
| [goals](goals.md) | reading, creating, replacing, and deleting goals, goal recommendations |
| [saved segments](segments.md) | reading, creating, replacing, starring, and deleting saved segments |
| [annotations](annotations.md) | listing, reading, counting, adding, updating, and deleting annotations |
| [custom dimensions](dimensions.md) | listing, options, adding, and updating custom dimensions |
| [scheduled reports](scheduled-reports.md) | listing scheduled e-mail reports, rendering one as text, pausing one, deleting one |
| [custom alerts](alerts.md) | listing and reading the user's alerts, their triggered entries and past values, deleting an alert |
| [mobile messaging](mobile-messaging.md) | SMS settings, removing phone numbers and SMS credentials, SMS delegation |
| [tracking](tracking.md) | recording page views, events, internal searches, downloads or outlinks, goal conversions, e-commerce orders and cart updates, content impressions and interactions, several of these in one bulk request, and requests of AI chatbots |
| [Tag Manager](tag-manager.md) | containers and their creation, update, and deletion, versions and their export, creating, renaming, and deleting versions, tags, triggers, and variables and where they are used, changing triggers, tags, and variables of the draft, embed code and installation steps, catalog of contexts, environments, and templates |

The tools that read are safe to repeat and need no confirmation. Names, addresses, settings, patterns, and
values Matomo returns are untrusted data: they are shortened, and an address is returned as text and never
fetched. The rights each tool group needs are listed in [Set up Matomo](setup.md#rights-per-tool-group).
