---
description: >
  Describes the Matomo version and website tools: instance version and plugin activation, websites inside the targets, tracking code, tracking failures, opt-out code, website settings, consent manager detection, and website groups.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-24
---

# Matomo sites and version

The version and website tools of the [Matomo provider](README.md). Version checks and availability are described
under [Version and availability](README.md#version-and-availability); targets under [Scope](README.md#scope).

## Websites

With `site/ID` targets and no `pattern`, `matomo.sites.list` pages the bound IDs in order and reads only the
websites of the batch. With a `pattern`, it reads at most 1000 matching websites and keeps those inside the targets;
when the pattern matches that many, the call is refused as an invalid request asking for a narrower pattern, so a
bound website is never missing silently.
## Tracking code

`matomo.tracking-code.get` takes `kind`. `javascript` calls `SitesManager.getJavascriptTag` with every documented
option; `image` calls `SitesManager.getImageTrackingCode` with `actionName`, `idGoal`, `revenue`, and
`forceMatomoEndpoint`. Options of the other kind are refused. A view right is enough for the JavaScript tag; the image
method checks no right in Matomo, so Qatlas relies on the website targets alone for it.

The result is untrusted text, cut at 16384 bytes (`truncated`), to be reviewed by a person and never run. The image
code stays HTML-encoded as Matomo returns it. `piwikUrl` is only written into the code: it must be an `https` address
without user information, query, or fragment, at most 2048 characters. Without it the JavaScript tag uses the address
Matomo knows for itself and the image code the connection's base URL (Matomo adds the scheme itself, so the address
goes without one).

## Tracking failures

`matomo.tracking-failures.list` calls `CoreAdminHome.getTrackingFailures` and needs an admin right. Matomo answers for
every website the token administers, and for a Super User also for websites that do not exist, so Qatlas drops the rows
outside the connection's website targets before paging; an optional `idSite` narrows the list further. The request text
and the address come from visitors or attackers: they are untrusted, cut at 2048 characters, and never fetched, and the
request text can hold personal data, so the tool is in the `live` profile rather than `read`.

`matomo.tracking-failures.delete` removes the failure `idFailure` of one `idSite`, checked against the targets before
any secret is read; the ID counts per website. `matomo.tracking-failures.delete-all` takes no website, because the
Matomo method acts on the whole token: a Super User clears the entire table, any other admin every administered
website. It therefore needs a connection whose targets name `instance` and is offered only where the `tools` list names
it. Both send one request and never repeat it; an unclear result is read back with `matomo.tracking-failures.list`.

## Opt-out code

`matomo.opt-out-code.get` takes `kind`: `js` calls `CoreAdminHome.getOptOutJSEmbedCode` and `self-contained` calls
`CoreAdminHome.getOptOutSelfContainedEmbedCode`. Matomo checks no right and no website for either, so the tool is offered
to connections that reach websites and is in the `read` profile. The result is untrusted text for a person to review,
cut at 65536 bytes (`truncated`), and never run. Colors, size, and font are applied only with `applyStyling` and are
refused unless they fit a narrow form without quotes, semicolons, or brackets, narrower than Matomo's own check,
because Matomo writes them unescaped into the code. `matomoUrl` and `language` belong to `js` only, `cookiePath`,
`cookieDomain`, and `cookieSameSite` to `self-contained` only. `matomoUrl` follows the rules of `piwikUrl` above and
defaults to the connection's base URL. The self-contained code always speaks the language of the token's user.

## Website settings

`matomo.site-settings.get` reads `SitesManager.getSiteSettings`, which needs an admin right on the website and lists
the measurable settings the token's user may change, grouped by plugin. Every member Matomo reports is returned, in
snake case; free-form values (`value`, `default_value`, `available_values`, attributes, `extra_metadata`) keep their
JSON type and are cut to text above 8192 bytes. Matomo itself replaces password values with a placeholder. A
compliance policy can replace the stored value and lock the control, shown in `extra_metadata` and
`ui_control_attributes`.

## Consent manager

`matomo.consent-manager.detect` calls `SitesManager.detectConsentManager`. The Matomo server, not Qatlas, fetches the
main address stored for the website to scan its content; Matomo caches the finding for a week. No address can be
given. `timeOut` (1 to 25 seconds, 20 when omitted) limits that fetch and stays below the 30 second limit of the call.
The result names the manager, Matomo's instruction address (text, never fetched), and whether it is connected; no
manager gives `detected` false.

## Website groups

`matomo.site-groups.list` needs Super User rights and a connection whose targets name `instance`; only such a
connection is offered the tool, and the call checks the target again before a secret is read. It is not part of the
`read` profile, which holds the tools that need no Super User right. Without `group` it pages the group names
(`SitesManager.getSitesGroups`; an empty name stands for websites without a group). With `group` it pages the
websites of that group (`SitesManager.getSitesFromGroup`) and keeps only those inside the website targets, so an
instance-only binding sees no website. The group names come from the whole instance and are not cut to the targets.

## Managing websites

`matomo.sites.add`, `matomo.sites.update`, and `matomo.sites.delete` change websites through `SitesManager.addSite`,
`SitesManager.updateSite`, and `SitesManager.deleteSite`. Each needs confirmation and is sent once and never
repeated; after an unclear result, read the website (`matomo.sites.get`, or `matomo.sites.list` after an add) before
trying again. Every documented parameter is offered, and the Matomo `message` never reaches an error.

- `sites.add` needs Super User rights and a connection whose targets name `instance`; it is offered only there and
  checks the target again before a secret is read. The result names the new `idSite`. The new website lies outside
  any `site/ID` target of the connection, so it is reachable only after the targets change, or with the target
  `site` without an ID. Not idempotent. The tool is in no profile, because the profile cannot bind the instance.
- `sites.update` takes `idSite` (checked against the targets before a secret is read, optional with exactly one bound
  website) and needs an admin right on it. `null` values stay unchanged in Matomo, so a value not given is left as it
  is, with one exception: Matomo clears the six comma-separated lists (`searchKeywordParameters`,
  `searchCategoryParameters`, `excludedIps`, `excludedQueryParameters`, `excludedUserAgents`, `excludedReferrers`)
  whenever they are not sent. Qatlas therefore reads the website once and sends the current value of each list that
  is not given (refused when a stored list is too long to be sent back faithfully); an empty text clears a list.
  `urls`, when given, replace the main and all alias addresses. `group` needs Super User rights; for others Matomo
  ignores it. It is in the `sites` profile, which is not recommended.
- `sites.delete` needs Super User rights, `idSite` given explicitly, a connection whose targets name `instance`, and
  the website inside its `site` targets; the website is read first to name it. It is listed-only (a connection's
  `tools` list) and in no profile. Matomo removes the website record and its configuration for good; it does not
  delete tracked logs and archives, and it refuses to delete the last website. A personal token needs no password
  confirmation (only a session token does), so the safeguard is the confirmation and the `tools` list alone.

## Website URLs, creation dates, and group names

`matomo.site-urls.add` (`SitesManager.addSiteAliasUrls`) and `matomo.site-urls.set` (`SitesManager.setSiteAliasUrls`)
need an admin right on the website. The URLs are an assignment for tracking only: up to 20 `http` or `https`
addresses without user information, at most 2000 characters each, never fetched. Neither tool touches the main URL.
`add` keeps the stored ones and skips duplicates; `set` replaces the whole list, and an empty list removes every
alias URL. The `inserted` count is Matomo's.

`matomo.sites.update-created-time` (`SitesManager.updateSiteCreatedTime`) takes 1 to 100 unique `idSites` and a
`minDate` as `YYYY-MM-DD`; Matomo stores the day before. It needs an admin right on every website, and one ID outside
the targets refuses the whole call before any secret is read. The method changes only the stored creation date.

`matomo.site-groups.rename` (`SitesManager.renameGroup`) needs Super User rights and a connection whose targets name
`instance`, like the group list. It renames the group on every website of the instance, also outside the website
targets; the old name must not be empty (that would address all websites without a group), an empty new name removes
the group. These write tools are in the `sites` profile, which is not recommended; the rename is in no profile.
Each sends exactly one request and is never repeated after an unclear result.
