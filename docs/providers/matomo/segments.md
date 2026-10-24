---
description: >
  Describes the Matomo saved-segment tools: reading, creating, replacing, starring, and deleting segments, the binding of an instance-wide segment ID to the connection's targets, the checked definition, and the rights and configuration limits Matomo enforces.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-24
---

# Matomo saved segments

The saved-segment tools of the [Matomo provider](README.md). The segment fields a definition can use come from
`matomo.segments.fields`, see [reports](reports.md). The profiles are described under
[Setup profiles](README.md#setup-profiles). All seven tools need the SegmentEditor plugin.

A segment ID counts for the whole instance, not per website. What decides is `enable_only_idsite`, the website a
segment belongs to; `0` stands for all websites.

## Binding to the targets

- A segment of one website is reachable when that website is among the connection's targets.
- A segment for all websites (`enable_only_idsite` 0) is reachable only when the connection binds `site` without an
  ID, or has no targets. A connection bound to `site/ID` targets never reaches it, not even when a list names one
  of its websites.
- `matomo.segments.list` checks every row against this rule. `matomo.segments.get` and `matomo.segments.update`
  read the segment once with `SegmentEditor.get` before anything else; a segment outside the targets, a missing one,
  and a deleted one all end as `not-found` without naming the website or the target.
- `matomo.segments.add` and `matomo.segments.update` write for all websites (`idSite` 0) only on such a wide
  connection; Matomo additionally requires the Super User right. `idSite` is checked before the secret is read.

## Tools

- `matomo.segments.list` returns the segments the token may use, in the order Matomo gives them (own segments
  first), cut on the Qatlas side into batches ([Paging](README.md#paging)). With `idSite` Matomo answers the
  segments of that website and those for all websites; the latter drop out on `site/ID` connections.
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

## Rights and configuration

Qatlas does not check rights; the answers of Matomo decide and end as `permission`.

- Creating a segment needs the access level of the instance setting `adding_segment_requires_access` in the
  `[General]` section (`view` by default) on the website; for all websites it needs the Super User right, and
  `allow_adding_segments_for_all_websites = 0` switches these segments off.
- Editing needs the creator or a Super User; a segment for all websites needs a Super User.
- `enabledAllUsers` needs a Super User. `autoArchive` needs browser-triggered archiving to be switched off
  (`enable_browser_archiving_triggering = 0`) and, for all websites, the Super User right; without
  `autoArchive` Matomo requires `enable_create_realtime_segments = 1`.

Both changes are confirmed by the caller and sent exactly once. After an unclear result (no answer in time, a reset
connection, an answer Qatlas cannot read) the error says the change may have been applied and is never repeated;
read the segment with `matomo.segments.get` or `matomo.segments.list` before trying again.

## Starring and deleting

`matomo.segments.star` and `matomo.segments.unstar` set or clear the star of a segment; `matomo.segments.delete`
deletes it. All three read the segment once with `SegmentEditor.get` and bind it to the targets exactly like
`matomo.segments.update`; a segment Qatlas cannot reach is not found, without quoting any value.

- The star is a property of the segment, not of the user: Matomo stores one flag and the login that set it, so
  everyone who sees the segment sees the star. Starring and unstarring need the same right as editing (the creator
  or a Super User; a segment for all websites needs a Super User). Both are idempotent and are in the `segments`
  profile.
- Deleting only marks the segment as deleted. It disappears for every user and from every report that uses it, and
  Matomo cleans up its stored data later; the tool cannot restore it, and a segment created again gets a new ID.
  Deleting needs the creator or a Super User (a segment for all websites needs a Super User). The tool is offered
  only where a connection's `tools` list names it and is in no profile.

Each change is confirmed and sent once; after an unclear result it is never repeated, see above.
