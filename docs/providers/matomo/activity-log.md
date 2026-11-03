---
description: >
  Describes the Matomo activity log tools: reading and counting log entries and listing activity types, the instance target, the rights, the plugin requirement, and the limits of what the ActivityLog reference proves.
type: knowledge
edit: shared
created: 2026-11-03
updated: 2026-11-03
---

# Matomo activity log

The activity log tools of the [Matomo provider](README.md). The log records who changed what in the instance. The
tools need the premium plugin ActivityLog; an instance without it refuses all of them as an unsupported capability,
after one plugin check and without further requests. They are in no [setup profile](README.md#setup-profiles)
because entries hold logins.

## Targets

The log belongs to the instance, not to a website. All three tools are offered only to a connection whose targets
name `instance`. They take no website and are bound to no website target.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.activity-log.list` | reads entries in batches, filtered by login, activity type, and period with date |
| `matomo.activity-log.count` | counts entries with the same filters |
| `matomo.activity-types.list` | reads the activity types, in batches |

- Logins and descriptions are personal data and untrusted text; they are shortened. An entry returns only its
  identifier, time, login, activity type, and description. Other members of an entry are not returned.
- Matomo reads a limit of -1 as unlimited. The tools never send it; they page with a bounded window.
- The activity types are not fixed. Use `matomo.activity-types.list` for the values of the filter.

## Limits

- Super Users see all entries and can filter by login; other users see only their own entries, and only from their
  registration on. The minimum level is set in the instance. Qatlas does not check it beforehand; Matomo's refusal
  ends the call as a permission error.
- The method signatures come from the Matomo 6 reference. They are not verified against Matomo 5 (ActivityLog 5.1.x);
  no version is checked per method, and an instance that lacks one ends as an unsupported capability.
- The plugin source is not public and the reference gives no example for any answer. The answers of all three methods
  are unverified. Assumed: entries as rows with `id`, `dateTime`, `userLogin`, `activityType`, and `description`, a
  count as a number, and types as rows with `id` and `name` or as bare texts.
- From ActivityLog 6.0.1 on, entries with the same time stand newest first.
