---
description: >
  Describes the Matomo privacy tools: finding, exporting, and deleting the visits of one person, reading the privacy settings, and reading and changing the compliance policies, with their rights, targets, caps, and the data they leave out.
type: knowledge
edit: shared
created: 2026-10-25
updated: 2026-10-25
---

# Matomo privacy and data subject requests

The privacy tools of the [Matomo provider](README.md). Targets are described under [Scope](README.md#scope).
`matomo.data-subjects.find` and `matomo.data-subjects.export` carry personal data of other people and need admin
rights; `matomo.privacy-settings.get` and `matomo.compliance.get` need Super User rights for most of their modes.
`matomo.data-subjects.delete` removes data for good, see [Deleting visits](#deleting-visits), and the
[compliance setters](#changing-compliance-policies) change the policies. None of these tools is in a setup profile.

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

## Anonymizing and purging raw data

`matomo.raw-data.anonymize` and `matomo.data-purge.run` change the stored logs of the instance for good, need Super
User rights and the target `instance`, are offered only where a connection's `tools` list names them, need `confirm`,
and send exactly one request that is never repeated after a timeout, a 5xx, or an unreadable answer.

- `matomo.raw-data.anonymize` calls `PrivacyManager.anonymizeSomeRawData` for the explicit `idSites` (1 to 100, all inside
  the website targets, so the connection needs `instance` and websites; Matomo's `all` is not accepted, and one foreign
  website refuses the whole call without naming it) and one `date`: a day or a range as `YYYY-MM-DD`, never a relative
  date. At least one of `anonymizeIp`, `anonymizeLocation`, `anonymizeUserId`, or a column list
  (`unsetVisitColumns`, `unsetLinkVisitActionColumns`, up to 50 names each) is required; Matomo checks the column
  names. Matomo only schedules the job and its scheduled task runs it later, so `scheduled` means accepted, not done.
- `matomo.data-purge.run` calls `PrivacyManager.executeDataPurge`, which takes no argument. It deletes raw logs and
  report data of the whole instance by the retention periods configured in Matomo, at once; Matomo refuses it when its
  administrators disabled the purge settings. The [retention setters](#setting-retention-periods) set those periods.

Matomo asks for the password of the token's user on these methods and the retention setters even with a
token. The credential needs the
optional secret role `password-confirmation` for it. It is stored and redacted like the token, is never an argument,
and is sent only on these methods, in the request body. A connection whose credential lacks it refuses these tools
before any request. The other tools, `matomo.data-subjects.delete` among them, never send the password; the one
exception is [`randomizeConfigId`](#changing-privacy-settings).


## Setting retention periods

`matomo.privacy-settings.set-delete-logs`, `matomo.privacy-settings.set-delete-reports`, and
`matomo.privacy-settings.set-report-deletion-schedule` call `PrivacyManager.setDeleteLogsSettings`,
`setDeleteReportsSettings`, and `setScheduleReportDeletionSettings`. They need Super User rights, the target
`instance`, `confirm`, and the password role described above, and send exactly one request that is never repeated
after an unclear result. The first two make the scheduled purge delete raw logs or reports for good from then on, so
they are offered only where a connection's `tools` list names them and are in no profile; the schedule setter is not
listed-only.

Every argument is required: Matomo resets an omitted one to its default, so each call states the whole state. No
Qatlas tool reads the current purge settings, so check them in Matomo first. The age of logs is in days, the age of
reports in periods (months); Matomo itself raises an age below 1 or 2 to the minimum, which Qatlas refuses instead.
Matomo keeps the stored log age unchanged when a compliance policy forbids setting it, and still answers success.

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

## Changing privacy settings

Three tools change the global privacy settings; all need Super User rights, a confirmation, and a connection that
names `instance`, and they change every website of the instance. They are in no profile and send one request that is
not repeated.

- `matomo.privacy-settings.set-anonymize-ip` calls `setAnonymizeIpSettings` for the global settings only, never for one
  website. It replaces the global settings completely, so every argument is required; read the settings with
  `matomo.privacy-settings.get` first. A compliance policy can make Matomo keep a
  value while it answers success; read the settings afterwards.
- `matomo.do-not-track.enable` and `matomo.do-not-track.disable` call `activateDoNotTrack` and `deactivateDoNotTrack`.
  They take no argument.

Matomo asks for the password of the token's user only when `randomizeConfigId` is true. Then the credential needs the
secret role `password-confirmation`, which is sent in the request body only, and a connection without it refuses the
call before any request. Every other call, and both Do Not Track tools, never send the password.

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

## Changing compliance policies

Three tools change the compliance state; all need Super User rights, a confirmation, and a connection that names
`instance`, also for one website, because a change for a website can rewrite the state of the instance and of every
other website. They take `policy` and `idSite` or `all_sites` like `matomo.compliance.get`, never ask for a password
(Matomo requires one only for session tokens), and send one request that is not repeated. A policy that Matomo's
configuration file controls ends as `permission`.

| Tool | Method | From | Feature flag |
| --- | --- | --- | --- |
| `matomo.compliance.set-policy-settings` | `setCompliancePolicySettings` | Matomo 5.14.0 | yes |
| `matomo.compliance.enforce` | `enforceCompliancePolicySettings` | Matomo 5.14.0 | yes |
| `matomo.compliance.set-status` | `setComplianceStatus` | Matomo 5.9.0 | no |

The flag `GranularPrivacyCompliance` is off by default. While it is off, the granular tools and the `settings` kind of
`matomo.compliance.get` end as `unsupported-capability`; a Super User enables it with `[FeatureFlags]
GranularPrivacyCompliance_feature = "enabled"` in the Matomo configuration.

- **`set-policy-settings`.** Stores for each named setting only the state enforced or not; the setting itself stays
  unchanged and is overridden when read. `true` is additive across instance and website, an explicit `false` releases
  the policy for that level. A setting ID that is unknown or not toggleable ends as `invalid-request`, and Matomo then
  writes nothing. The answer has the form of `matomo.compliance.get` with kind `settings`.
- **`enforce`.** Enforces every toggleable setting of the policy at once, for `cnil_v1` among others IP anonymization,
  referrer shortening, user ID and visitor log off, and raw data kept at most 759 days. The data effect is final: data
  anonymized meanwhile stays anonymized, and the scheduled purge then deletes raw data older than 759 days when
  deleting is active and was set longer. Releasing the settings later sets them to explicitly not enforced; the earlier
  inherited state cannot be restored through the API. The tool is `execute`, in no profile, and offered only where a
  connection's `tools` list names it.
- **`set-status`.** The older whole-policy switch, deprecated in Matomo 6: while on, every setting without a state of
  its own counts as enforced, and off for a website under an active instance also turns the instance off. Matomo only
  echoes the value sent, so the result is read with `matomo.compliance.get`. Mixing it with `set-policy-settings`
  gives states that are hard to follow.

## Availability

`find` needs the plugin `Live`, the other tools the plugin `PrivacyManager`. A plugin that is not activated, or a method
the release lacks, ends as `unsupported-capability` after the version and plugin reads, without further requests, see
[Version and availability](README.md#version-and-availability).
