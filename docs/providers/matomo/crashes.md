---
description: >
  Describes the Matomo crash tools: listing and reading the crashes of a website, crash groups, and crash types, the binding of a crash to the connection's targets, the plugin requirement, the rights the public demo reports, and the limits of what the CrashAnalytics reference proves.
type: knowledge
edit: shared
created: 2026-11-03
updated: 2026-11-03
---

# Matomo crashes

The read tools for crashes of the [Matomo provider](README.md). They need the premium plugin CrashAnalytics; an
instance without it refuses all of them as an unsupported capability, after one plugin check and without further
requests. The tools are in no [setup profile](README.md#setup-profiles). The reports of the plugin, which take a
period, are read with `matomo.reports.get` like any other report. Ignored crashes, merge candidates, the visit
context of a crash, and the real-time methods are not offered.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.crashes.list` | reads the crashes of a website in batches, sorted by `sort_column` and `sort_order` |
| `matomo.crashes.get` | reads one crash of a website with its page address and category |
| `matomo.crash-groups.list` | reads the groups of merged crashes of a website in batches |
| `matomo.crash-types.list` | reads the different crash types of a website in batches |

- `matomo.crashes.list` sorts by `datetime_last_seen`, newest first, unless told otherwise. The sortable columns are
  the single-valued fields of a crash; the reference names no set, so the list is the narrower reading.
- Every batch reads the list from its first row and cuts the batch out on the Qatlas side, because the public demo
  applied Matomo's offset twice and skipped rows. A list therefore reaches at most the first 10000 crashes, and a late
  batch of a website with long stack traces can exceed the 4 MiB answer limit and end as `invalid-provider-response`.
- `matomo.crash-groups.list` needs a write right on the website, other than the other crash tools.

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

## Limits

- The reference states no right for a method. The public demo answered an anonymous token with a view right on the
  website for the crash list, the summary, and the crash types, and with a write right for the crash groups. The rights
  are not proven per method.
- The signatures come from the 6.x reference. The Marketplace lists CrashAnalytics 5.0.19 for Matomo 5; the
  signatures are not verified for Matomo 5. No version is checked per method, and an instance that lacks one ends as
  an unsupported capability.
- The answer of `getCrashGroups` is not verified: the reference names no shape and the demo refused the read. The tool
  assumes an object keyed by the ID of the parent crash whose values are lists of crashes with their website. A crash
  without a website is dropped, so an answer of another shape shows no crash instead of a wrong one.
- Whether the report catalog of `matomo.reports.list` holds every crash report is not verified.
- The reference types some date members as any value. They are returned as text when Matomo gives a scalar and left
  out otherwise.
- The limits on text, on crashes per group, and on the rows of a list are Qatlas's own, except the 10000 rows the
  reference names.
- The error form for a missing method, module, or inactive plugin is recognized by the common Matomo messages as an
  unsupported capability; an unknown crash ends as `not-found` by the common message.
