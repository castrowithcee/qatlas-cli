---
description: >
  Describes Nextcloud Notes tools and their safety boundaries.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-23
---

# Nextcloud notes

Group `notes`: notes and attachments.

The group `notes` (profile `notes-read`) needs a `notes` target; `notes/PATH` binds it and all below. The Notes API
filters categories exactly, so the list is read unfiltered and foreign notes are dropped locally, never shown or
counted; a chunk may be empty despite a next cursor. A foreign note or attachment reads like a missing note; attachments
stay note-relative, `local_path` follows `files.files.get`. Writes are in no profile: create, update, and
`notes.attachments.upload` need a bound category, update also its new one and the `etag`; delete needs a tools list.
