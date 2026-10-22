---
description: >
  Describes the source and version of the Penpot RPC forms the provider relies on, the behavior of Penpot it
  depends on, and the assumptions that were not verified against a live instance.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Penpot: API forms

Read from the Penpot 2.18.0 command sources and help.penpot.app (technical guide, integration), not against a live
instance. The request and answer fields are fixed by the tool descriptors and schemas in
`internal/provider/penpot`; this page keeps the source, the Penpot behavior the tools depend on, and the assumptions.

- Backend RPC `POST /api/rpc/command/<name>` with `Authorization: Token ...` (documented). None of the read commands
  paginates; `get-page` returns the first page when `page-id` is omitted.
- Pages (`files.clj` `get-page`, `common/types/page.cljc`, `shape.cljc`, `shape/layout.cljc`, `variant.cljc`):
  `get-page` returns the page record with its `objects` map of shapes, minus each shape's `thumbnail`; `object-id`
  (one UUID or a set, with `page-id` required) prunes `objects` to those shapes and their children; the schemas fix
  the page metadata and the attributes of each shape type, including the layout and variant attributes of a board.
  With the `fdata/path-data` feature a path may arrive as binary data instead of a JSON structure. Assumed: the
  attribute values arrive as JSON structures of the schema's shape, and such binary data as text.
- Listings (`teams.clj`, `projects.clj`, `files.clj`, `binfile/common.clj`, `nitrate.clj`): `get-teams` returns the
  columns of the `team` table with the account's permissions; the subscription is added only with the `subscriptions`
  flag, the organization only with the `admin-console` flag (teams with an expired organization license are then
  removed). `get-projects` returns deleted but recoverable projects since 2.12. `get-project-files` and
  `get-file-libraries` return only files that are not deleted. Assumed: timestamps are sent as text, sets such as
  `features` as lists, and a missing or null member is simply absent. `get-team-members` joins the membership row
  (`team_profile_rel`) with the profile's id, email, full name, photo, and activity; the row's team and profile IDs
  repeat the request and the member ID. `get-webhooks` selects id, URI, payload type, activity, error code and count,
  and the creating profile.
- Comments (`comments.clj`): the update and delete commands check that the caller owns the comment or thread and
  answer without a body. The reading commands return the thread and comment columns plus the author's name, email, and
  photo (`comment_thread` and `comment` tables, migrations 0031, 0077, and 0136); the threads also carry file name,
  team, project, and the caller's unread count. `share-id` is optional and never sent; the write commands send no
  `mentions`.
- Project and file management (`projects.clj`, `files.clj`, `files_create.clj`, `management.clj`): `delete-project`
  marks the project deleted and refuses the default project; `move-files` takes a set of file IDs and the target
  project, refuses a move into the same project, and answers without a body. Names are at most 250 characters. The
  optional `id` (user-provided UUID), `is-shared`, and `features` of the create commands are never sent.
- Libraries (`files.clj`): `get-file-libraries` returns the libraries the file uses, directly or through another
  library; `link-file-to-library` requires edit permission on both files and the same team. Assumed: the answers of
  `set-file-shared` and `link-file-to-library` are not read.
- Snapshots and deleted files (`files_snapshot.clj`, `files.clj`): `create-file-snapshot` needs edit permission;
  `restore-file-snapshot` and `delete-file` answer without a body. `restore-deleted-team-files` and
  `permanently-delete-team-files` take a set of file IDs and answer a server-sent event stream whose `end` event
  carries the IDs acted on. The permanent deletion acts on any file ID of the team, so the tool checks the
  deleted-file list first. Assumed: the stream lines are `event:` and `data:` with a JSON array in the `end` event,
  the answers of the other changes are read only as far as stated, and the response is delivered with
  `Accept: application/json`.
- Media and transfer (`rpc/commands/media.clj`, `binfile.clj`, `app/media.clj`, `app/http/sse.clj`):
  `upload-file-media-object` is multipart and its file part must have an image content type;
  `create-file-media-object-from-url` lets Penpot fetch the URL, follow up to three redirects, and needs a size and an
  image type in the answer; `export-binfile` answers a server-sent event stream whose `end` event carries the URI
  `<public-uri>/assets/by-id/<id>` of a temporary object; `import-binfile` is multipart with the archive as the
  `file` part, detects the version, and answers a server-sent event stream. Assumed: the multipart field names are the
  kebab-case parameter names, the part's content type becomes the `mtype`, the URI of the export is found by its
  `/assets/by-id/<uuid>` tail whatever its encoding, the UUIDs in the `end` event of an import are the new file IDs,
  and the temporary object is readable without the token.
- Webhooks (`webhooks.clj`): `update-webhook` requires `uri`, `mtype` (one of `application/json` and
  `application/transit+json`), and `is-active`; `uri` is always the stored one; it resets the error counters.
  `delete-webhook` answers without a body. Edit permission on the team is checked by Penpot. Assumed: the answers of
  the changes are not read, and a webhook ID is not tied to a team by the command itself, which is why the tool
  proves it through `get-webhooks` first.
- Teams (`teams.clj`, `common/types/team.cljc`): `update-team-member-role` accepts `owner`, `admin`, `editor`, and
  `viewer`; Qatlas sends the last three only. The optional `id`, `features`, `organization-id`, and `is-default` of
  `create-team` are never sent. `delete-team` answers without a body. Assumed: the answer of `create-team` carries the
  new ID as `id`, and the answers of the other changes are not read.
- Assumed, not documented: a `position` is sent as an object `{"x": ..., "y": ...}` and a thread's position is read
  the same way; a change that answers without a body is read as done; requests send kebab-case keys, responses are
  JSON when `Accept: application/json` is sent, and their keys are read case- and separator-insensitively (camelCase
  or kebab-case); the summary's categories carry `count` and `sample`; the answers of `create-project` and
  `create-file` carry the new ID as `id`, and the other management changes answer without a readable body that Qatlas
  needs; a page's `objects` map is keyed by shape ID. A different form yields empty fields, not an error.
