---
description: >
  Describes the Infomaniak kDrive trash tools: the trash read, the confirmed move to the trash, restore, permanent
  deletion, and emptying, with their allow-list requirements, identifier rules, and one-shot change contract.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-27
---

# Infomaniak kDrive trash

The trash tools of the [Infomaniak kDrive provider](README.md). The effects and allow-list requirements are listed
in the [tool overview](README.md#tools); the other changes are described in [files](files.md#changes).

- `trash.list` reads one page of the top level of the trash: `limit` (5 to 1000, default 10) and an opaque
  `cursor`, with `has_more` and `cursor` answered as for `files.list`. Each entry is the usual entry plus
  `deleted_at`; names and times are untrusted data and names are length-bounded. A trashed folder's own
  contents are not listed.
- `files.trash` sends `DELETE /2/drive/{d}/files/{id}`, `trash.delete` sends `DELETE /2/drive/{d}/trash/{id}`,
  and `trash.empty` sends `DELETE /2/drive/{d}/trash`. Neither sends a body. `file_id` is a positive integer
  other than the drive's root `1`; `trash.empty` takes only `drive_id`. A trashed entry stays restorable until
  `trash.delete` or `trash.empty` removes it; that removal and the emptying cannot be undone.
- `trash.restore` takes `destination_id`, a required folder of the same drive (the root is `1`), because the
  specification requires `destination_directory_id` in the request body; Qatlas offers no restore to a place
  outside the drive. Restoring is the only trash change that needs just the `update` effect.
- On a drive with excluded files or folders, `trash.list`, `trash.restore`, `trash.delete`, and `trash.empty` are
  refused before the secret is read, as for a drive bound only by `files` targets: an entry no longer shows
  where it lay, so it cannot be told from an excluded one. `files.trash` stays, except for a folder above an
  excluded object, see [Exclusions](files.md#exclusions).
- The same rules as for the other changes apply: the drive goes through the checks under [Scope](README.md#scope) before the one
  request, `files.trash` proves its `file_id` like any object, the four other trash tools act on the whole drive, a refusal names no foreign value, an unclear outcome is reported
  and never repeated, and `asynchronous` is a `pending` change with the cancel handle when Infomaniak reports
  one. A trash change never reports an entry. A 403 is reported as `permission`; see
  [Fixing errors](setup.md#fixing-errors).
