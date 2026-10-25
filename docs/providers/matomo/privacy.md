---
description: >
  Describes the Matomo privacy tools: finding, exporting, and deleting the visits of one person, reading the privacy settings, and reading the compliance policies, with their rights, targets, caps, and the data they leave out.
type: knowledge
edit: shared
created: 2026-10-25
updated: 2026-10-25
---

# Matomo privacy and data subject requests

The privacy tools of the [Matomo provider](README.md). Targets are described under [Scope](README.md#scope). Four tools
only read. `matomo.data-subjects.find` and `matomo.data-subjects.export` carry personal data of other people and need
admin rights; `matomo.privacy-settings.get` and `matomo.compliance.get` need Super User rights for most of their modes.
`matomo.data-subjects.delete` removes data for good, see [Deleting visits](#deleting-visits). None of the five tools
is in a setup profile.

## Finding and exporting a person

`matomo.data-subjects.find` calls `PrivacyManager.findDataSubjects` for one `idSite`; the `segment` decides who is
found, for example `visitorId==...` or `userId==...`. It needs an admin right on the website and the plugin `Live`;
without the plugin the tool ends as `unsupported-capability`, because Matomo would answer an empty list. Matomo also
answers an empty list, not an error, for a website whose visits log or visitor profile is disabled. The answer holds the
visit IDs with the reduced columns Matomo keeps (identity, device, browser, location); the icon columns are paths,
returned as text and never fetched. Matomo returns at most 401 visits: a list that reaches it sets
`matomo_limit_reached`, and the segment must be narrowed. The visits are cut into batches here (25 by default, at most
100) with an opaque cursor bound to the website and the segment. A row of another website, or one without visit and
website ID, is dropped. The segment is untrusted text of 1 to 2048 bytes without control characters.

`matomo.data-subjects.export` calls `PrivacyManager.exportDataSubjects` with the visits of `find`, given as `visits`
entries of `idSite` and `idVisit` (`id_site` and `id_visit` of `find`). It needs an admin right on every website of the
list. Every `idSite` is checked against the targets before a secret is read or Matomo is contacted, and the refusal
names neither the website nor a target. The answer is Matomo's export: one table per log table and plugin, with the
columns Matomo returns.

- **Batches.** The list holds up to 401 visits. One call exports 10 visits by default and at most 25 (`limit`);
  `next_cursor` continues the same list. The cursor is bound to the tool, the targets, and the whole list, so another
  list, other targets, another `limit`, or an altered cursor is refused.
- **Cap.** The rows of one call are returned up to 256 KiB. Rows beyond that are left out, the table is flagged with
  `truncated`, and `row_count` still gives the rows Matomo returned for it; a smaller `limit` returns the rest of a
  visit's tables. A call is cut instead of refused, because a single visit can exceed any cap. An answer of Matomo
  over 4 MiB ends as `invalid-provider-response`; export fewer visits.
- **Values.** Text is shortened to 2048 bytes and untrusted; nested values become text; rows that are no object become
  a row with the one member `value`. Rows of another website or visit than the batch are dropped.
- **Left out.** A column whose name contains token, password, secret, or API key, and a value that carries one (a
  `token_auth=` URL parameter, or a nested member named like one), because they are access values, not data about the
  person. Empty values are left out.

Both tools carry the data classification `matomo-data-subjects`.

## Deleting visits

`matomo.data-subjects.delete` calls `PrivacyManager.deleteDataSubjects` with the visits of `find`, given as `visits`
entries of `idSite` and `idVisit`, at most 401 and without repeats. It cannot be undone, is offered only where a
connection's `tools` list names it, needs `confirm`, sends exactly one request, and never repeats it after a timeout, a
5xx, or an unreadable answer; the error says so, and `find` shows the state before the call is repeated. It needs an
admin right on every website of the list. Every `idSite` is checked against the targets before a secret is read; one
foreign website refuses the whole call without naming it. A personal token needs no password confirmation, so the tool
takes no password. The answer gives the rows Matomo deleted per storage area (`areas`, ordered by name, cut at 100 with
`truncated`), and no area when Matomo reports none.

The anonymization of raw data and the data purge are not offered: Matomo asks for a password confirmation there.

## Privacy settings

`matomo.privacy-settings.get` chooses its Matomo method from `section`:

| `section` | `idSite` | Method | Needs |
| --- | --- | --- | --- |
| `settings` (default) | given | `getAnonymisationSettings` | admin right on the website |
| `settings` | none | `getAnonymisationSettings` | Super User; target `instance` |
| `visit-columns` | none | `getAvailableVisitColumnsToAnonymize` | Super User; target `instance` |
| `action-columns` | none | `getAvailableLinkVisitActionColumnsToAnonymize` | Super User; target `instance` |

`idSite` with a column section is refused. Without `idSite` the mode is refused before a secret is read unless the
targets name `instance`. Like the other tools with both a website and an instance-wide mode, the tool is offered only
to a connection that reaches websites. The settings are an allow-list of what Matomo documents; the options are the
choices the compliance policies still allow. `tracker_file_name` and `tracker_writable` are shown by Matomo to Super
Users only. `extra_metadata` appears only when a compliance policy controls a setting.

## Compliance policies

`matomo.compliance.get` is offered only to connections that name `instance`; every kind needs Super User rights.

| `kind` | Method | Arguments |
| --- | --- | --- |
| `policies` (default) | `getCompliancePolicies` | none |
| `status` | `getComplianceStatus` | `policy`, and `idSite` or `all_sites` |
| `settings` | `getCompliancePolicySettings` | `policy`, and `idSite` or `all_sites` |

`status` is deprecated in Matomo 6; it is offered as long as Matomo 5 has it. `settings` ends as
`unsupported-capability` when Matomo's granular compliance feature is not enabled. An unknown `policy` ends as
`not-found`. `idSite` must lie inside the website targets of the connection; `all_sites` reads the instance-wide state.

## Availability

`find` needs the plugin `Live`, the other tools the plugin `PrivacyManager`. A plugin that is not activated, or a method
the release lacks, ends as `unsupported-capability` after the version and plugin reads, without further requests, see
[Version and availability](README.md#version-and-availability).
