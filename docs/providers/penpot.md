---
description: >
  Describes the Penpot provider (beta): the access token and the Penpot Cloud or self-hosted base URL, the team and
  project targets, the read tools, the tools that manage projects and files, the comment tools that read and change, the bounds, errors, and the RPC forms it
  relies on.
type: knowledge
edit: shared
created: 2026-09-30
updated: 2026-10-01
---

# Penpot

This provider reads teams, projects, files, and pages of a Penpot instance, Penpot Cloud or self-hosted, with an
access token, creates, renames, deletes, and moves projects and files, and reads and manages the comments of a file. It changes nothing else: no file content, no libraries.

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
| `penpot.comments.threads` | read | none | `get-comment-threads` | lists the comment threads of a file |
| `penpot.comments.list` | read | none | `get-comments` | lists the comments of one thread |
| `penpot.comments.create` | create | required | `create-comment-thread` or `create-comment` | starts a thread or adds a comment |
| `penpot.comments.update` | update | required | `update-comment-thread` or `update-comment` | resolves or reopens a thread, or edits a comment |
| `penpot.comments.delete` | delete | required, tool allow-list | `delete-comment-thread` or `delete-comment` | deletes a thread or a comment |

The recommended read profile offers the four tools for teams, projects, and files; the comment tools are enabled
through the connection's permissions and `tools` list, and so are the project and file tools. `penpot.comments.delete`
and `penpot.projects.delete` are offered only when the `tools` list names them. Every tool names its command itself; there is no `command` argument, no free path, method, or body.
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

## Bounds

The commands document no pagination; every list is cut on the client: 100 teams, 1000 projects, 1000 files, 500 threads, 200 comments of a thread. A
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
- Assumed, not documented: a `position` is sent as an object `{"x": ..., "y": ...}` and a thread's position is read
  the same way; a change that answers without a body is read as done; requests send kebab-case keys, responses are JSON when `Accept: application/json` is sent,
  and their keys are read case- and separator-insensitively (camelCase or kebab-case); the summary's categories carry
  `count` and `sample`; the answers of `create-project` and `create-file` carry the new ID as `id`, and the other
  management changes answer without a readable body that Qatlas needs; a page's `objects` map is keyed by shape ID with `type`, `name`, `parent-id`, `frame-id`, `x`,
  `y`, `width`, `height`. A different form yields empty fields, not an error.
