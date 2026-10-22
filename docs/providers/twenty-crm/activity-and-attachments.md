---
description: >
  Describes the Twenty CRM tools that link notes and tasks to records and read, download, and upload attachments.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Twenty CRM: notes, tasks, and attachments

## Linking notes and tasks

`activity-targets.list`, `activity-targets.create`, and `activity-targets.delete` (`read`, `create`, `delete`)
read, set, and remove the links between a note or a task (`activity`) and a record of any reachable object,
standard or custom. The field that carries the link comes from the workspace schema of the link object, never
from an argument. An object that the schema gives no single link relation cannot be linked.

- The note or task and the object must both be reachable through the connection, so with targets both
  `object/note` (or `object/task`) and the object are bound. A refusal comes before any secret is resolved
  and names neither object nor identifier.
- `list` names only links whose two sides are reachable and counts the others in `omitted`.
- Repeating `create` adds a duplicate link; the answer must name the requested link.
- `delete` removes only the link, never the note, task, or record. It reads the link first and removes it only
  when both sides are reachable. A connection offers it only when its `tools` list names it. Links are
  workspace data of the record data class.

## Attachments

`attachments.list` and `attachments.get` (`read`) read the files attached to a record of any reachable object;
`attachments.upload` (`create`, not idempotent) adds one. The linking field (`target<Object>Id`) comes from the
workspace schema, never from an argument. `list` counts attachments without a file or without exactly one
reachable target in `omitted`. Labels and content are untrusted.

- `get` needs exactly one target on a reachable object, else it answers as if the attachment did not exist.
- `upload` takes `local_path` in a directory released for reading, or inline content up to 4 MiB; 1 byte to
  100 MiB. It reads the record first, needs the Upload File right and create rights on attachments, sends each
  step once, and names the file id from the upload on. Twenty drops unfinished uploads after 24 hours; a
  completed file without an attachment stays.
- Qatlas uses a file address Twenty reports only if it is https on exactly the host and port of the connection,
  with the file's own path and the signed `token` as its only query. The request carries no API key and follows
  no redirect; the token appears in no result or error. External storage (another host) is not supported.
