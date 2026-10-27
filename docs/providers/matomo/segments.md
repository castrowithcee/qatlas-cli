---
description: >
  Describes the Matomo saved-segment tools: reading, creating, replacing, starring, and deleting segments, the binding of an instance-wide segment ID to the connection's targets, the checked definition, and the checks Matomo enforces.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-26
---

# Matomo saved segments

The saved-segment tools of the [Matomo provider](README.md). The segment fields a definition can use come from
`matomo.segments.fields`, see [reports](reports.md). The profiles are described under
[Setup profiles](README.md#setup-profiles). The rights, settings, and plugin are listed in
[rights per tool group](setup.md#rights-per-tool-group).

## Tools

| Tool | Does |
| --- | --- |
| `matomo.segments.list` | reads the saved segments inside the targets |
| `matomo.segments.get` | reads one saved segment by its instance-wide ID |
| `matomo.segments.add` | saves a segment (effect `create`) |
| `matomo.segments.update` | replaces a saved segment (effect `update`) |
| `matomo.segments.star` | stars a saved segment; the star belongs to the segment, not the user (effect `update`) |
| `matomo.segments.unstar` | removes the star from a saved segment (effect `update`) |
| `matomo.segments.delete` | deletes a saved segment, soft delete (effect `delete`, only via `tools`) |

A segment ID counts for the whole instance, not per website. What decides is `enable_only_idsite`, the website a
segment belongs to; `0` stands for all websites.

## Binding to the targets

Reading and writing are separate rules.

- A segment of one website is reachable, for reading and writing, when that website is among the connection's
  targets.
- A segment for all websites (`enable_only_idsite` 0) can be read on every connection that reaches a website, as
  Matomo shows it for any website the token views. Changing it (add, update, delete, star, unstar) needs a
  connection bound to `site` without an ID, or without targets; Matomo allows it to a Super User only. A
  `site/ID` connection never writes for all websites: without `idSite` it uses its only website or is refused.
- `matomo.segments.list` checks every row against the read rule. `matomo.segments.get` and every change read the
  segment once with `SegmentEditor.get` before anything else; a segment outside the targets, a change of a segment
  for all websites on a `site/ID` connection, a missing one, and a deleted one all end as `not-found`, before any
  change request, without naming the website or the target. `idSite` of `add` and `update` is checked before the
  secret is read.

## Tool behavior

- `matomo.segments.list` returns the segments the token may use, in the order Matomo gives them (own segments
  first), cut on the Qatlas side into batches ([Paging](README.md#paging)). With `idSite` Matomo answers the
  segments of that website and those for all websites.
- `matomo.segments.get` and the list return every field Matomo documents for a stored segment: `id`, `name`,
  `definition` (as stored, values URL-encoded), `hash`, `login` of the creator, `enable_all_users`,
  `enable_only_idsite`, `auto_archive`, `ts_created`, `ts_last_edit`, `deleted`, `starred`, and `starred_by`.
  Names, definitions, and logins are untrusted data.
- `matomo.segments.add` saves a segment and returns its new ID; it is not idempotent, a repeated call saves a second
  segment. Without `idSite` it uses the one website a connection binds, or else all websites.
- `matomo.segments.update` replaces name, definition, website, and settings as a whole: `idSite` keeps the current
  website when omitted (0 moves the segment to all websites), while `autoArchive` and `enabledAllUsers` fall back to
  false when omitted. Read the segment first with `matomo.segments.get`. A changed definition of a pre-processed
  segment makes Matomo process it again.

Deleting and starring segments are not offered.

## Definition

`definition` is checked before anything is sent: terms of a field, an operator, and a URL-encoded value, joined by
`;` for and and `,` for or, at most 20 terms and 1000 characters; free text is refused. Every field must be a segment
field of the website (for all websites, of the instance) as `matomo.segments.fields` lists it; a field it does not
list is refused with `invalid-request` before the change, without quoting it. Matomo checks the whole expression
again, and an expression it rejects also ends as `invalid-request`.

## Failures and confirmation

Qatlas does not check rights; the answers of Matomo decide and end as `permission`.

Both changes are confirmed by the caller and sent exactly once. After an unclear result (no answer in time, a reset
connection, an answer Qatlas cannot read) the error says the change may have been applied and is never repeated;
read the segment with `matomo.segments.get` or `matomo.segments.list` before trying again.

## Starring and deleting

`matomo.segments.star` and `matomo.segments.unstar` set or clear the star of a segment; `matomo.segments.delete`
deletes it. All three read the segment once with `SegmentEditor.get` and bind it to the targets exactly like
`matomo.segments.update`; a segment Qatlas cannot reach is not found, without quoting any value.

- The star is a property of the segment, not of the user: Matomo stores one flag and the login that set it, so
  everyone who sees the segment sees the star. Both are idempotent and are in the `segments` profile.
- Deleting only marks the segment as deleted. It disappears for every user and from every report that uses it, and
  Matomo cleans up its stored data later; the tool cannot restore it, and a segment created again gets a new ID.
  The tool is offered only where a connection's `tools` list names it and is in no profile.

Each change is confirmed and sent once; after an unclear result it is never repeated, see above.
