---
description: >
  Describes Nextcloud Notes tools and their safety boundaries.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-28
---

# Nextcloud notes

Group `notes`: notes and attachments.

The group `notes` (profile `notes-read`) needs a `notes` target; `notes/PATH` binds it and all below. The Notes API
filters categories exactly, so the list is read unfiltered and foreign notes are dropped locally, never shown or
counted; a chunk may be empty despite a next cursor. A foreign note or attachment reads like a missing note; attachments
stay note-relative, `local_path` follows `files.files.get`. Writes are in no profile: create, update, and
`notes.attachments.upload` need a bound category, update also its new one and the `etag`; delete needs a tools list.

The rules are in [Exclusions](README.md#exclusions). A note whose category lies on or below an excluded category
reads like a missing note, is left out of `notes.notes.list` and not counted, and takes its attachments with it. Create
and update refuse such a category, an update that moves a reachable note into one included, as a category the
connection does not hold. The category is normalised as the Notes app does before it is compared, so `Work/ .Secret`
and `Work/S*ecret` meet an exclusion of `Work/Secret`, and the comparison ignores case and Unicode composition.
A folder exclusion on or above the notes path hides the notes below it the same way.
