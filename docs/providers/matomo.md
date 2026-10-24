---
description: >
  Describes the Matomo provider: setup with a personal authentication token and a required base URL for
  on-premise and Matomo Cloud, website and instance targets, version and plugin availability, HTTP 200 error
  forms, rate limits, paging without a total, the report catalog, and the read tools.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-24
---

# Matomo

Matomo is a provider for one Matomo instance, on-premise or Matomo Cloud, through the Matomo HTTP API of
Matomo 5. The API is the same for both, and the token carries the rights of its Matomo user. This page
describes the foundation every Matomo tool shares: connection, targets, availability, errors, and paging, and
the first tools that read the version and the websites.

## Configuration

A service needs `base_url`; there is no default, not even for Matomo Cloud. It is the address of the
installation, `https://HOST[/PATH]`: an installation below a path is common, and a Matomo Cloud instance is
`https://SUBDOMAIN.matomo.cloud`. Qatlas refuses any other form (no `https`, user information, query,
fragment) before a secret is read, and `qatlas config validate` reports a missing address. A redirect is never
followed. Every request is a `POST` to `<base_url>/index.php`.

The credential provides `token`, a personal authentication token from Matomo under Personal, Security. Create it
for a Matomo user whose rights match what the connection should reach: the token has the view, write, or admin
rights of its user per website, or Super User rights. Qatlas does not check them in advance. The token is sent as
`Authorization: Bearer ...`, never in a URL, a query, or an argument.

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
refused. Methods without server paging are cut on the Qatlas side. With `site/ID` targets and no `pattern`,
`matomo.sites.list` pages the bound IDs in order and reads only the websites of the batch. With a `pattern`,
it reads at most 1000 matching websites and keeps those inside the targets; when the pattern matches that
many, the call is refused as an invalid request asking for a narrower pattern, so a bound website is never
missing silently.

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

## Tools

| Tool | Reads |
| --- | --- |
| `matomo.version.get` | the instance version and, with `plugin`, whether that plugin is activated |
| `matomo.sites.list` | websites of the token inside the targets, optionally matching `pattern` |
| `matomo.sites.get` | one website with its addresses, time zone, currency, and tracking settings |
| `matomo.reports.list` | the report catalog of a website as id, name, and category, with a category overview |
| `matomo.reports.describe` | the details of one report of the catalog |
| `matomo.segments.fields` | the segment fields of a website |
| `matomo.segments.suggest-values` | frequent values of a segment field |

All tools read, are safe to repeat, and need no confirmation. The terminal editor starts a new connection on the
setup profile `read`, which selects all of them. Names, addresses, settings, and values Matomo returns are
untrusted data: they are shortened, and an address is returned as text and never fetched. `matomo.version.get`
needs a view right on any website; the other tools need one on the website read.
