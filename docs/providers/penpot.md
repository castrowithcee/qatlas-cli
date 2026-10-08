---
description: >
  Describes the Penpot provider (beta): the access token and the Penpot Cloud or self-hosted base URL, the team and
  project targets, the read tools, the tools that manage projects and files, the comment tools that read and change, the media, export, and import tools, the webhook tools, the team and member tools, the bounds, errors, and the RPC forms it
  relies on.
type: knowledge
edit: shared
created: 2026-09-30
updated: 2026-10-04
---

# Penpot

This provider reads teams, projects, files, and pages of a Penpot instance, Penpot Cloud or self-hosted, with an
access token, creates, renames, deletes, and moves projects and files, creates and restores file snapshots, restores and permanently deletes deleted files, reads and manages the comments of a file, adds images to a file, exports and imports files as `.penpot` archives, and manages the webhooks, members, and existence of teams. It edits no file content beyond restoring a snapshot and adding images.

**Beta.** Penpot's backend RPC interface (`POST /api/rpc/command/<name>`) is documented only by its sources and
carries no stability promise. A command or a field can change with a Penpot release; the forms used here are listed
under "API forms" and were not tried against a live instance.

## Credential and base URL

The credential provides `access-token`: a Penpot access token, created in the profile's access tokens. The instance
needs the `access-tokens` flag enabled (Penpot Cloud has it; a self-hosted instance sets it in its configuration).
The token is sent as `Authorization: Token ...` and is registered with the redactor. **The token has no scopes**: it
acts for every team of its account. Qatlas narrows a connection through its targets, its `tools` list, and its
`permissions`; it does not narrow the token.

`base_url` is `https://design.penpot.app` by default. A self-hosted instance uses its own `https` URL, optionally
below an installation path; a URL with `http`, user info, a query, or a fragment is refused before any secret is
read. No redirect is followed, so the token never travels to another host.

```yaml
services:
  penpot-customer-a:
    provider: penpot
    base_url: https://design.penpot.app

credentials:
  penpot-customer-a-token:
    provider: penpot
    type: keyring
```

## Scope

A connection lists one or more teams as `team/TEAM_ID` targets (required) and, optionally, projects of those teams as
`project/PROJECT_ID` entries. IDs are UUIDs. Without a project entry, every project of the bound teams is reachable.

```yaml
connections:
  customer-a-design:
    service: penpot-customer-a
    credential: penpot-customer-a-token
    targets:
      - team/6b2c1e0a-1d4f-4e57-9c1a-0f6d3a8b2c11
      - project/0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d
```

- `team_id` (projects.list) must be one of the bound teams, and a `project_id` must be in the project allow-list when
  there is one. Both are checked before the credential is resolved and before any request is sent; the refusal does
  not name the ID.
- A project without an allow-list match is not enough on its own: before a project's files are listed or read, the
  projects of the bound teams are read (one `get-projects` request per team, until the project appears). A project
  outside them is refused as outside the targets.
- A file is bound through its project: `files.get` takes `project_id` and `file_id`, reads the project's file list,
  and refuses a `file_id` that is not in it, before the summary or page is requested. The comment tools bind the file
  the same way before anything else is read.
- A thread or comment ID is checked against the bound file before it is read, updated, or deleted: the thread must
  appear in the file's threads (`get-comment-threads`) and a comment in the thread's comments (`get-comments`).
  These reads are the only I/O before a refusal; the refusal does not name the ID. Threads and comments that report
  another file or thread are dropped.
- `teams.list` and `projects.list` show only the bound teams and allowed projects. A project that reports another
  team and a file that reports another project are dropped.

## Tools

| Tool | Effect | Confirmation | RPC command | Does |
| --- | --- | --- | --- | --- |
| `penpot.teams.list` | read | none | `get-teams` | lists the bound teams |
| `penpot.projects.list` | read | none | `get-projects` | lists the projects of one bound team |
| `penpot.files.list` | read | none | `get-project-files` | lists the files of one project |
| `penpot.files.get` | read | none | `get-file-summary`, `get-page` | reads a file's library summary and one page |
| `penpot.projects.create` | create | required | `create-project` | creates a project in a bound team |
| `penpot.projects.rename` | update | required | `rename-project` | renames a project |
| `penpot.projects.delete` | delete | required, tool allow-list | `delete-project` | deletes a project with its files |
| `penpot.files.create` | create | required | `create-file` | creates an empty file in a project |
| `penpot.files.rename` | update | required | `rename-file` | renames a file |
| `penpot.files.move` | update | required | `move-files` | moves one file to another project |
| `penpot.files.delete` | delete | required, tool allow-list | `delete-file` | soft-deletes one file |
| `penpot.files.restore` | update | required | `restore-deleted-team-files` | restores one deleted file |
| `penpot.files.purge` | delete | required, tool allow-list | `permanently-delete-team-files` | permanently deletes one deleted file |
| `penpot.snapshots.create` | create | required | `create-file-snapshot` | creates a snapshot of a file |
| `penpot.snapshots.restore` | update | required, tool allow-list | `restore-file-snapshot` | overwrites a file with one of its snapshots |
| `penpot.libraries.list` | read | none | `get-file-libraries` | lists the libraries a file uses |
| `penpot.libraries.share` | update | required | `set-file-shared` | shares a file as a library for the whole team, or stops sharing it |
| `penpot.libraries.link` | update | required | `link-file-to-library` | links one file to a library file |
| `penpot.media.upload` | create | required | `upload-file-media-object` | stores an image from a local file or inline in a file |
| `penpot.media.fromurl` | create | required | `create-file-media-object-from-url` | lets Penpot fetch an image from an https URL into a file |
| `penpot.files.export` | read | none | `export-binfile` | writes a file as a `.penpot` archive to a local path |
| `penpot.files.import` | create | required | `import-binfile` | imports a local `.penpot` archive as a new file |
| `penpot.comments.threads` | read | none | `get-comment-threads` | lists the comment threads of a file |
| `penpot.comments.list` | read | none | `get-comments` | lists the comments of one thread |
| `penpot.comments.create` | create | required | `create-comment-thread` or `create-comment` | starts a thread or adds a comment |
| `penpot.comments.update` | update | required | `update-comment-thread` or `update-comment` | resolves or reopens a thread, or edits a comment |
| `penpot.comments.delete` | delete | required, tool allow-list | `delete-comment-thread` or `delete-comment` | deletes a thread or a comment |
| `penpot.webhooks.list` | read | none | `get-webhooks` | lists the webhooks of one bound team |
| `penpot.webhooks.update` | update | required | `update-webhook` | replaces payload type and active state of a webhook |
| `penpot.webhooks.delete` | delete | required, tool allow-list | `delete-webhook` | deletes a webhook |
| `penpot.members.list` | read | none | `get-team-members` | lists the members of one bound team |
| `penpot.members.setrole` | update | required, tool allow-list | `update-team-member-role` | sets a member's role to admin, editor, or viewer |
| `penpot.members.remove` | delete | required, tool allow-list | `delete-team-member` | removes a member from a bound team |
| `penpot.teams.create` | create | required, tool allow-list | `create-team` | creates a new team |
| `penpot.teams.delete` | delete | required, tool allow-list | `delete-team` | deletes a bound team with its projects and files |

The recommended read profile offers the four tools for teams, projects, and files; the comment tools are enabled
through the connection's permissions and `tools` list, and so are the project and file tools. `penpot.comments.delete`,
`penpot.projects.delete`, `penpot.files.delete`, `penpot.files.purge`, `penpot.webhooks.delete`, `penpot.snapshots.restore`, `penpot.members.setrole`, `penpot.members.remove`, `penpot.teams.create`, and `penpot.teams.delete` are offered only
when the `tools` list names them. Every tool names its command itself; there is no `command` argument, no free path, method, or body.
Reads use POST but are idempotent and safe.

`files.get` takes `project_id`, `file_id`, and optionally `page_id` (the first page of the file when omitted). Its
answer has `summary` (for components, variants, colors, and typographies a `count` and up to 20 `samples` with `id`
and `name`) and `page` (`id`, `name`, `shape_count`, `truncated`, and up to 200 `shapes` with `id`, `type`, `name`,
`parent_id`, `frame_id`, `x`, `y`, `width`, `height`). Shapes are listed in ID order. Fills, images, media
references, text content, and every other shape property are not returned (a deliberate limit: geometry and identity
only). A file's page list is not offered: the summary carries none, so other pages are reached only through a
known `page_id`.

## Projects and files

The management tools take IDs from the list tools and change one thing with one fixed command. Names have 1 to 250
characters (Penpot's own limit), are not blank, and carry no control characters; an answer never repeats a name, only
IDs.

- `projects.create` takes `team_id` and `name` and answers `project_id` and `team_id`. The team must be a bound team.
  **A connection with a project allow-list refuses it**, because a new project could not be inside that list.
- `projects.rename` takes `project_id` and `name`; `projects.delete` takes `project_id`. The project must pass the
  allow-list and lie in a bound team (proved with `get-projects`). Penpot marks a deleted project and removes it and its
  files after its deletion delay, and refuses the default project of a team. A project that is in the allow-list may be
  deleted; the boundary is not widened by that.
- `files.create` takes `project_id` and `name` and answers `file_id` and `project_id`; the project is bound as above.
- `files.rename` takes `project_id`, `file_id`, and `name`. The file is bound through its project (project located,
  file found in its file list) before anything is sent.
- `files.move` takes `project_id` (where the file is now), `file_id`, and `target_project_id`. Both projects must pass
  the allow-list and lie in bound teams, and they must differ; the file is bound through the source project. A move
  between two bound teams is allowed when Penpot allows it. Penpot drops library links of the moved file that would
  cross teams. The answer has `moved`, `file_id`, `project_id` (the new project), and `previous_project_id`.

Every change asks for `confirm`, sends exactly one request, and is never repeated. After a timeout, a connection
reset, a 5xx answer, or an unreadable answer (for a creation also an answer without the new ID) the error says that the
change may have taken effect; read the projects or files before trying again. A refusal for a target outside the
connection comes before the credential is resolved when the allow-list decides it, and otherwise after reads only; it
never names the target.

## Libraries

- `libraries.list` takes `project_id` and `file_id` (the file is bound through its project) and reads the libraries the
  file uses. Only libraries whose team is bound and whose project passes the allow-list are described (`id`, `name`,
  `project_id`, `is_shared`, `is_indirect`); every other library is only counted in `outside_targets`, never named.
  At most 200 libraries are returned.
- `libraries.share` takes `project_id`, `file_id`, and `shared`. **Sharing makes the file visible as a library to the
  whole team that owns it**, not only to this connection's projects. Unsharing removes the links of other files and
  copies the library's assets into them. The file is bound through its project first. The answer has `shared`,
  `file_id`, and `project_id`.
- `libraries.link` takes `project_id` and `file_id` (the file that uses the library) and `library_project_id` and
  `library_id` (the library file). Both files are bound through their projects, which must pass the allow-list and lie
  in bound teams, so a link is only made between bound files. Penpot itself refuses a link to the file itself, across
  teams, and a circular link. The answer has `linked`, `file_id`, and `library_id`.

`share` and `link` ask for `confirm`, send exactly one request, and are never repeated; after a timeout, a connection
reset, a 5xx answer, or an unreadable answer the error says that the change may have taken effect, so read the
libraries before trying again. Refusals never name the target.

## Snapshots and deleted files

The five tools use the read commands `get-file-snapshots` and `get-team-deleted-files` for their checks; none of them
is offered as a tool, so the IDs of older snapshots and deleted files come from the Penpot UI or from the answers of
earlier calls.

- `snapshots.create` takes `project_id`, `file_id`, and optionally `label` (1 to 250 characters; Penpot generates one
  when omitted). The file is bound through its project. Penpot limits snapshots per file and team and refuses beyond
  that. The answer has `created`, `snapshot_id`, `file_id`, `project_id`, and `revn`; the label is not repeated.
- `snapshots.restore` takes `project_id`, `file_id`, and `snapshot_id`. **It overwrites the current content of the
  file**; Penpot keeps a temporary backup snapshot of the state before. The snapshot ID must appear in the visible
  snapshots of the bound file (`get-file-snapshots`), else the call is refused before anything is changed.
- `files.delete` takes `project_id` and `file_id` (bound through its project) and soft-deletes the file: Penpot marks
  it, removes the library links that point to it, and removes it after its deletion delay.
- `files.restore` and `files.purge` take `project_id` and `file_id` of a file that is no longer in its project's file
  list. The file is bound through `get-team-deleted-files` of each bound team: it must appear there and report the
  given project, which must pass the allow-list. The team ID is taken from that binding; it is never an argument. A
  file that is not deleted, or that lies in another project, is refused without naming it. Exactly one file is sent.
  `files.restore` also removes the deletion mark of the file's project, as Penpot does. **`files.purge` deletes the
  file for good, before its deletion delay ends, and cannot be undone.**

Both commands answer with a server-sent event stream; the change counts as done only when its final event names the
file. An error event, or an answer that does not name the file, is reported as a failure; a stream that ends without
a final event is reported as uncertain. The same rules hold as for every change: `confirm` is required, exactly one
request is sent, it is never repeated, and after a timeout, a connection reset, a 5xx answer, or an unreadable answer
the error says that the change may have taken effect. Refusals never name the target.

## Media, export, and import

These four tools move content between Penpot and local files. Local files go through the directories the connection
releases (`files.read` for reading, `files.write` for writing); a path outside them is refused before the credential is
resolved and before any request is sent, and the refusal never names the path. Answers carry IDs and metadata only
(size, SHA-256), never file content.

- `media.upload` takes `project_id`, `file_id`, and either `local_path` or `content_base64` (up to 4 MiB; with a
  `name`, whose extension names the image type), optionally `name` and `is_local` (default `true`). Types: png, jpeg,
  gif, webp, svg, chosen by the file name extension. Images are limited to 64 MiB by Qatlas and by the instance's own
  limit. The file is bound through its project. The answer has `id` (the media object), `media_id`, `file_id`,
  `project_id`, `name`, `mime_type`, `width`, `height`, `size`, and `sha256`.
- `media.fromurl` takes `project_id`, `file_id`, `url`, and optionally `name` and `is_local`. **Penpot itself fetches the
  URL.** Qatlas accepts only an `https` URL of up to 2048 characters on the default port, without user info or
  fragment, whose host is a DNS name of at least two labels with an alphabetic last label. Every IP form (also decimal,
  octal, hexadecimal, or IPv6), single-label names, and the suffixes `localhost`, `local`, `internal`, `intranet`,
  `lan`, `home`, `corp`, `localdomain`, `home.arpa`, and `private` are refused before any secret or request. This check
  is syntactic: Qatlas does not resolve the name and cannot see where Penpot's DNS lookup or its (up to three)
  redirects lead, so the instance's own network controls remain the real boundary. The URL is not repeated in the
  answer (`id`, `media_id`, `file_id`, `project_id`, `name`, `mime_type`, `width`, `height`).
- `files.export` takes `project_id`, `file_id`, and `local_path`, and writes the file as a `.penpot` archive. It reads
  and changes nothing in Penpot except a temporary copy that Penpot keeps for about an hour. **Libraries are not
  included and their assets are not embedded** (`include-libraries` and `embed-assets` are always `false`), since
  library files may lie outside the connection's projects. An existing local file is replaced only when `confirm` is
  set; the archive is written to a temporary file first and made visible only when complete. Limit: 1 GiB. The answer
  has `file_id`, `project_id`, `size`, and `sha256`.
- `files.import` takes `project_id`, `name`, and `local_path` of a `.penpot` archive and creates a new file in that
  project (the project is located in the bound teams). Penpot's import has no overwrite parameter, so it never replaces
  a file, which is why the tool needs no tool allow-list. The archive is sent in one request, up to 512 MiB and the
  instance's own upload limit; chunked uploads are not used. Penpot checks the archive. The answer has `imported`,
  `project_id`, `file_ids` (up to 20, when Penpot reports them), `size`, and `sha256`.

`media.upload`, `media.fromurl`, and `files.import` ask for `confirm`, send exactly one request, and are never repeated;
after a timeout, a connection reset, a 5xx answer, an unreadable answer, or an import stream without a final event the
error says that the change may have taken effect, so read the file or project before trying again. Transfers use a
30 minute timeout instead of the 30 seconds of the other requests (`media.fromurl`: 2 minutes). The export's archive is
fetched from `/assets/by-id/<id>` on the connection's own origin, never from a host named in an answer, and without the
token.

## Comments

The comment tools take `project_id` and `file_id`, and work on the threads and comments of that file. Comment texts
and the people behind them are personal data: the tools have their own sensitivity class, `penpot-comments`. An
answer carries owner IDs, never names or email addresses, and no comment text is ever echoed after a change.

- `comments.threads` lists the file's threads with `id`, `page_id`, `page_name`, `frame_id`, `owner_id`, `x`, `y`,
  `is_resolved`, `comment_count`, `seqn`, an `excerpt` of the first comment (up to 750 bytes), and timestamps.
- `comments.list` takes `thread_id` and lists the comments, oldest first, with `id`, `owner_id`, `content` (up to
  3000 bytes), and timestamps.
- `comments.create` makes the form by its arguments. With `thread_id` it adds a comment to that thread
  (`create-comment`); without it, `page_id`, `frame_id`, and `position` (`x`, `y`) start a new thread
  (`create-comment-thread`), after the page has been read to prove that the frame lies on a page of the file. Mixing
  the two forms is refused. The text has 1 to 750 characters (Penpot's own limit). Qatlas sends no mentions; Penpot
  may still notify members of the team by email, according to their notification settings. The answer has
  `thread_id`, `comment_id`, and `file_id`.
- `comments.update` with `comment_id` and `content` edits a comment (`update-comment`); with `is_resolved` and
  without `comment_id` it resolves or reopens the thread (`update-comment-thread`).
- `comments.delete` with `comment_id` deletes that comment (`delete-comment`); without it, the whole thread with all
  its comments (`delete-comment-thread`). The deletion is final.

Penpot allows editing and deleting only for the comments and threads of the token's own account; another one is
refused by Penpot (a `provider-error`, without its text). A thread that has no comment is not listed by Penpot and
cannot be addressed.

Every change asks for `confirm`, sends exactly one request, and is never repeated. After a timeout, a connection
reset, a 5xx answer, or an unreadable answer the error says that the change may have taken effect; read the
comments of the file before trying again.

## Webhooks

The three webhook tools work on the webhooks of one team and take `team_id`, which must be a bound team (checked before
the credential is resolved and before any request; the refusal never names the team). Qatlas creates no permanent
access or data paths to the outside (webhooks, invitations, public links); reading, pausing, revoking, and deleting
remain, so no tool creates a webhook, changes a webhook's target URL, or invites a person.

- `webhooks.list` takes `team_id` and answers `webhooks` with `id`, `host`, `mtype`, `is_active`, `error_code`, and
  `error_count` (at most 100). **The target URL is never returned, only its host name**, since a URL may carry a
  secret in its path or query. Webhook secrets are not exposed by Penpot's list and are never handled.
- `webhooks.update` takes `team_id`, `webhook_id`, `mtype`, and `is_active` and answers `updated`, `webhook_id`, and
  `team_id`. It has no `url` argument: it reads the stored URL from `get-webhooks` of the bound team and sends it
  back unchanged, so the target never changes and never leaves the client. Penpot resets the error counters.
- `webhooks.delete` takes `team_id` and `webhook_id`.

A webhook ID is bound before an update or delete: it must appear in `get-webhooks` of the given bound team, else it is refused without naming it. **Webhooks
belong to the whole team, so a connection with a project allow-list refuses update and delete**; listing is
still allowed.

Every change asks for `confirm`, sends exactly one request, and is never repeated. After a timeout, a connection
reset, or a 5xx answer, the error says that the
change may have taken effect; list the webhooks before trying again.

## Teams and members

The five tools administer teams. `team_id` must be a bound team (checked before the credential is resolved and before
any request; the refusal never names the team). **There is no wildcard target and Qatlas never adds a target**: the
team that `teams.create` makes is outside every connection until you add `team/ID` to a connection yourself.

- `members.list` takes `team_id` and answers `members` with `id` (used as `member_id`), `email`, `role` (`owner`,
  `admin`, `editor`, or `viewer`), and `is_active` (at most 200). Email addresses are personal data: the tool has its own
  sensitivity class, `penpot-members`; names and photos are not returned.
- `members.setrole` takes `team_id`, `member_id`, and `role` (`admin`, `editor`, or `viewer`). The role `owner` is not
  offered, so ownership cannot be transferred. Penpot refuses a change of the owner and a change without admin rights.
  The answer has `updated`, `team_id`, `member_id`, and `role`.
- `members.remove` takes `team_id` and `member_id`. Penpot refuses to remove the token's own account, and an admin
  cannot remove the owner. The member must belong to the given team; Penpot looks the ID up in that team only.
- `teams.create` takes `name` (1 to 250 characters, none of `.`, `:`, `/`, which Penpot refuses) and answers `team_id`.
  The new team is owned by the token's account. Penpot limits the teams per account.
- `teams.delete` takes `team_id`. Penpot marks the team and its projects and files as deleted and removes them after
  the deletion delay; only the owner may delete, and the default team is refused.

Team and member changes affect the whole team, so **a connection with a project allow-list refuses all of
them, and `teams.create`**; `members.list` is still allowed. Every change asks for `confirm`, sends exactly one request,
and is never repeated. After a timeout, a connection reset, a 5xx answer, or (for `teams.create`)
an unreadable answer, the error says that the change may have taken effect; list the members or teams
before trying again. Emails are never part of an error message.

## Bounds

The commands document no pagination; every list is cut on the client: 100 teams, 100 webhooks, 200 members, 1000 projects, 1000 files, 500 threads, 200 comments of a thread. A
response is read up to 16 MiB; a larger one is an `invalid-provider-response`. Names are cut at 256 bytes. Targets
hold at most 20 teams and 200 projects. Requests to one token share a rate limit.

## Errors

Errors carry a stable class and never the provider's text or the token: `auth`, `permission`, `not-found`,
`rate-limited`, `timeout`, `unreachable`, `invalid-provider-response`, and `provider-error`. A 401 or 403 adds the hint
to check that the token is valid and that the `access-tokens` flag is enabled on the instance. The invoke log
contains no arguments and no results. Names and page contents are untrusted data of the provider; Qatlas never
renders, follows, or executes them.

## API forms

Read from the Penpot 2.18.0 command sources and help.penpot.app (technical guide, integration), not against a live
instance:

- Backend RPC `POST /api/rpc/command/<name>` with `Authorization: Token ...` (documented).
- `get-teams` (no parameters; a list of teams with `id`, `name`, `is-default`, `permissions`), `get-projects`
  (`team-id`; projects with `id`, `name`, `team-id`, `modified-at`, `is-pinned`, `count`), `get-project-files`
  (`project-id`; files with `id`, `project-id`, `name`, `modified-at`, `revn`, `is-shared`), `get-file-summary`
  (`id`; the library summary and the file `name`), `get-page` (`file-id`, optional `page-id`; first page when
  omitted; a page with `id`, `name`, and an `objects` map). None of them paginates.
- Comments (Penpot 2.18.0, `comments.clj`): `get-comment-threads` (`file-id`; threads with `id`, `file-id`, `page-id`,
  `page-name`, `frame-id`, `owner-id`, `position`, `is-resolved`, `seqn`, `count-comments`, the first comment as
  `content`, and owner name and email, which Qatlas drops), `get-comments` (`thread-id`; comments with `id`,
  `thread-id`, `file-id`, `owner-id`, `content`, `created-at`, `modified-at`), `create-comment-thread` (`file-id`,
  `page-id`, `frame-id`, `position`, `content` up to 750 characters; answers the thread with `id` and `comment-id`),
  `create-comment` (`thread-id`, `content`; answers the comment), `update-comment-thread` (`id`, `is-resolved`),
  `update-comment` (`id`, `content`), `delete-comment-thread` and `delete-comment` (`id`). The update and delete
  commands check that the caller owns the comment or thread and answer without a body. `share-id` and `mentions`
  are optional and never sent.
- Project and file management (Penpot 2.18.0, `projects.clj`, `files.clj`, `files_create.clj`, `management.clj`):
  `create-project` (`team-id`, `name`; answers the project with `id`), `rename-project` (`id`, `name`),
  `delete-project` (`id`; marks the project deleted, refuses the default project), `create-file` (`project-id`,
  `name`; answers the file with `id`), `rename-file` (`id`, `name`; answers `id`, `name`, timestamps), and
  `move-files` (`ids`, a set of file IDs, and `project-id`, the target; refuses a move into the same project; answers
  without a body). Names are at most 250 characters. The optional `id` (user-provided UUID), `is-shared`, and
  `features` of the create commands are never sent.
- Libraries (Penpot 2.18.0, `files.clj`): `get-file-libraries` (`file-id`; the libraries the file uses, directly or
  through another library, each with `id`, `name`, `project-id`, `team-id`, `is-shared`, `is-indirect`, and
  timestamps), `set-file-shared` (`id`, `is-shared`), and `link-file-to-library` (`file-id`, `library-id`; requires
  edit permission on both files and the same team, answers the libraries of the library). Assumed: the answers of the
  two changes are not read.
- Snapshots and deleted files (Penpot 2.18.0, `files_snapshot.clj`, `files.clj`): `get-file-snapshots` (`file-id`;
  the visible snapshots with `id`, `label`, `revn`, `created-by`), `create-file-snapshot` (`file-id`, optional
  `label`; needs edit permission; answers the snapshot with `id`, `revn`, `label`), `restore-file-snapshot` (`file-id`,
  `id`; answers without a body), `delete-file` (`id`; answers without a body), `get-team-deleted-files` (`team-id`;
  files with `id`, `project-id`, `team-id`, `name`, `will-be-deleted-at`), and `restore-deleted-team-files` and
  `permanently-delete-team-files` (`team-id`, `ids`, a set of file IDs; both answer a server-sent event stream whose
  `end` event carries the IDs acted on). The permanent deletion acts on any file ID of the team, so the tool checks the
  deleted-file list first. Assumed: the stream lines are `event:` and `data:` with a JSON array in the `end` event, the
  answers of the other changes are read only as far as stated, and the response is delivered with `Accept: application/json`.
- Media and transfer (Penpot 2.18.0, `rpc/commands/media.clj`, `binfile.clj`, `app/media.clj`, `app/http/sse.clj`):
  `upload-file-media-object` (multipart: `file-id`, `is-local`, `name` up to 250 characters, and `content`, a file part
  whose content type must be an image type; answers the file media object with `id`, `media-id`, `name`, `width`,
  `height`, `mtype`), `create-file-media-object-from-url` (`file-id`, `is-local`, `url`, optional `name`; Penpot fetches
  the URL, follows up to three redirects, and needs a size and an image type in the answer), `export-binfile`
  (`file-id`, `include-libraries`, `embed-assets`; answers a server-sent event stream whose `end` event carries the
  URI `<public-uri>/assets/by-id/<id>` of a temporary object), and `import-binfile` (multipart: `name`, `project-id`,
  and the archive as the `file` part; the version is detected; answers a server-sent event stream). Assumed: the
  multipart field names are the kebab-case parameter names, the part's content type becomes the `mtype`, the URI of the
  export is found by its `/assets/by-id/<uuid>` tail whatever its encoding, the UUIDs in the `end` event of an import
  are the new file IDs, and the temporary object is readable without the token.
- Webhooks (Penpot 2.18.0, `webhooks.clj`): `get-webhooks` (`team-id`; webhooks with `id`, `uri`, `mtype`, `is-active`,
  `error-code`, `error-count`, `profile-id`), `update-webhook` (`id`, `uri`, `mtype` one of `application/json` and
  `application/transit+json`, `is-active`, all required; `uri` is always the stored one; resets the error counters), and `delete-webhook`
  (`id`; answers without a body). Edit permission on the team is checked by Penpot. Assumed: the answers of the changes are not read, and a webhook ID is not
  tied to a team by the command itself, which is why the tool proves it through `get-webhooks` first.
- Teams (Penpot 2.18.0, `teams.clj`, `common/types/team.cljc`): `get-team-members` (`team-id`;
  rows with `id` (the profile), `email`, `name`, `is-owner`, `is-admin`, `can-edit`, `is-active`; Qatlas reads `id`,
  `email`, the flags, and `is-active`), `update-team-member-role` (`team-id`, `member-id`, `role` one of `owner`,
  `admin`, `editor`, `viewer`; Qatlas sends the last three only), `delete-team-member` (`team-id`, `member-id`),
  `create-team` (`name`; the optional `id`, `features`, `organization-id`, and `is-default`
  are never sent; answers the team with `id`), and `delete-team` (`id`; answers without a body). Assumed: the answer of
  `create-team` carries the new ID as `id`, and the answers of the other changes are not read.
- Assumed, not documented: a `position` is sent as an object `{"x": ..., "y": ...}` and a thread's position is read
  the same way; a change that answers without a body is read as done; requests send kebab-case keys, responses are JSON when `Accept: application/json` is sent,
  and their keys are read case- and separator-insensitively (camelCase or kebab-case); the summary's categories carry
  `count` and `sample`; the answers of `create-project` and `create-file` carry the new ID as `id`, and the other
  management changes answer without a readable body that Qatlas needs; a page's `objects` map is keyed by shape ID with `type`, `name`, `parent-id`, `frame-id`, `x`,
  `y`, `width`, `height`. A different form yields empty fields, not an error.
