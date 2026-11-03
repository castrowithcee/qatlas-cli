---
description: >
  Describes the Matomo crash tools: listing and reading the crashes of a website, crash groups, and crash types, ignoring, merging, and unmerging crashes, the binding of a crash to the connection's targets, the plugin requirement, the rights the public demo reports, and the limits of what the CrashAnalytics reference proves.
type: knowledge
edit: shared
created: 2026-11-03
updated: 2026-11-03
---

# Matomo crashes

The tools for crashes of the [Matomo provider](README.md). They need the premium plugin CrashAnalytics; an
instance without it refuses all of them as an unsupported capability, after one plugin check and without further
requests. The tools are in no [setup profile](README.md#setup-profiles). The reports of the plugin, which take a
period, are read with `matomo.reports.get` like any other report. The list of ignored crashes, merge candidates, the
visit context of a crash, and the real-time methods are not offered as reads.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.crashes.list` | reads the crashes of a website in batches, sorted by `sort_column` and `sort_order` |
| `matomo.crashes.get` | reads one crash of a website with its page address and category |
| `matomo.crash-groups.list` | reads the groups of merged crashes of a website in batches |
| `matomo.crash-types.list` | reads the different crash types of a website in batches |
| `matomo.crashes.set-ignored` | changes whether a crash is ignored when tracking; `ignore` is true when omitted |
| `matomo.crashes.merge` | merges 2 to 100 different crashes into one |
| `matomo.crashes.unmerge` | unmerges the group of a parent crash |

- `matomo.crashes.list` sorts by `datetime_last_seen`, newest first, unless told otherwise. The sortable columns are
  the single-valued fields of a crash; the reference names no set, so the list is the narrower reading.
- Every batch reads the list from its first row and cuts the batch out on the Qatlas side, because the public demo
  applied Matomo's offset twice and skipped rows. A list therefore reaches at most the first 10000 crashes, and a late
  batch of a website with long stack traces can exceed the 4 MiB answer limit and end as `invalid-provider-response`.
- `matomo.crash-groups.list` needs a write right on the website, other than the other crash tools.
- The three change tools need `confirm`, send exactly one change request, and never repeat it. After a timeout, a
  reset connection, a server error, or an unreadable answer the outcome is unclear and the error names the read that
  settles it. `merge` is the only one declared not idempotent: Matomo documents neither which crash becomes the parent
  nor what a merge does with a crash that is already merged. `set-ignored` and `unmerge` are idempotent in effect.

Crash messages, script and page addresses, types, and stack traces are untrusted provider data. They are shortened
(stack traces to 4096 bytes), an address is returned as text and never fetched, and a group shows at most 100
crashes.

## Targets

`idSite` must be inside the targets ([Scope](README.md#scope)) and is checked before any secret is read; a website
outside them is refused without naming it. A row that names another website, or none, is dropped from a crash list and
from a group; a group left without a crash is not shown.

A crash ID counts across websites, and the summary of a crash names no website. `matomo.crashes.get` therefore reads
the crash list of the website once and asks for the summary only when the list holds the crash for this website. A
crash of another website, one the list does not show, and one outside its first 10000 rows end as `not-found` without
naming another website. A summary that names another website is not shown either. On a website with thousands of
crashes this read can exceed the 4 MiB answer limit, and the tool then ends as `invalid-provider-response`.

The change tools bind their crashes the same way, with one read before the single change and no change when the read
does not show them (`not-found`). Which read depends on the tool:

- `matomo.crashes.set-ignored` with `ignore` true and `matomo.crashes.merge` read the crash list; `merge` needs every
  crash of its list in that one read.
- `matomo.crashes.set-ignored` with `ignore` false reads `getIgnoredCrashes`, so only a crash that is ignored on the
  website can stop being ignored. The public demo holds no ignored crash and therefore does not show whether an ignored
  crash stays in the crash list; the narrower reading is used, and a crash that is ignored and missing from the list
  cannot be ignored again.
- `matomo.crashes.unmerge` reads the group list, because the demo holds no group and so does not show whether the
  parent crash appears in the crash list. The ID must be the key of a group that holds a crash of the website, the
  parent ID as `matomo.crash-groups.list` shows it. The group list needs a write right, as does the change itself.

Merge and unmerge check on the Matomo side whether the crashes belong to the same website only from plugin version
5.0.11 on, so this read is required and not redundant.

## Limits

- The reference states no right for a method. The public demo answered an anonymous token with a view right on the
  website for the crash list, the summary, and the crash types, and with a write right for the crash groups. The rights
  are not proven per method. The change tools are expected to need a write right on the website, as the crash groups
  and the merge candidates do; the demo allows only reads, so no change was sent to any instance.
- The signatures come from the 6.x reference. The Marketplace lists CrashAnalytics 5.0.19 for Matomo 5; the
  signatures are not verified for Matomo 5. No version is checked per method, and an instance that lacks one ends as
  an unsupported capability.
- The answer of `getCrashGroups` is not verified: the reference names no shape and the demo refused the read. The tool
  assumes an object keyed by the ID of the parent crash whose values are lists of crashes with their website. A crash
  without a website is dropped, so an answer of another shape shows no crash instead of a wrong one.
- The answers of the three change methods are not shown by the reference or the demo. The tools accept the generic
  success form and treat any other answer as unclear. The rows of `getIgnoredCrashes` are assumed to carry the crash ID
  and the website like the crash list; a row without a website is dropped, so another shape ends as `not-found`.
- `idLogCrashes` goes to Matomo as one comma-separated list in the order given. The reference names a single ID, an
  array, and a comma-separated list; the list format is not verified against a running instance. The limit of 100
  crashes per merge is Qatlas's own.
- Whether the report catalog of `matomo.reports.list` holds every crash report is not verified.
- The reference types some date members as any value. They are returned as text when Matomo gives a scalar and left
  out otherwise.
- The limits on text, on crashes per group, and on the rows of a list are Qatlas's own, except the 10000 rows the
  reference names.
- The error form for a missing method, module, or inactive plugin is recognized by the common Matomo messages as an
  unsupported capability; an unknown crash ends as `not-found` by the common message.
