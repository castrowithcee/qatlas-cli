---
description: >
  Describes the Penpot provider (beta): the access token and the Penpot Cloud or self-hosted base URL, the team,
  project, and file targets and how they bind tools, the tool overview with effects, confirmation, and RPC commands,
  profiles, the rules every change follows, the bounds, and the errors. Links to the pages for each area.
type: knowledge
edit: shared
created: 2026-09-30
updated: 2026-10-26
---

# Penpot

This provider reads teams, projects, files, and pages of a Penpot instance, Penpot Cloud or self-hosted, with an
access token, creates, renames, deletes, and moves projects and files, creates and restores file snapshots,
restores and permanently deletes deleted files, reads and manages the comments of a file, adds images to a file,
exports and imports files as `.penpot` archives, and manages the webhooks, members, and existence of teams. It
edits no file content beyond restoring a snapshot and adding images.

**Beta.** Penpot's backend RPC interface (`POST /api/rpc/command/<name>`) is documented only by its sources and
carries no stability promise. A command or a field can change with a Penpot release; the forms used here are listed
in [API forms](api-forms.md) and were not tried against a live instance.

To connect Qatlas to Penpot, see [Set up Penpot](setup.md): creating the token, adding it to Qatlas, the team role each
tool group needs, and fixing errors.

Pages by area:

- [Projects and files](projects-and-files.md): read tools, project and file management, snapshots, deleted files
- [Libraries](libraries.md)
- [Media, export, and import](media-and-transfer.md)
- [Comments](comments.md)
- [Webhooks](webhooks.md)
- [Teams and members](teams-and-members.md)
- [Set up Penpot](setup.md): token, roles, and fixing errors
- [API forms](api-forms.md): the RPC forms and the assumptions behind them

## Credential and base URL

The credential provides `access-token`: a Penpot access token; [Set up Penpot](setup.md#create-a-token) shows how
to create it. The instance needs the `access-tokens` flag enabled (Penpot Cloud has it; a self-hosted instance sets
it in its configuration). The token is sent as `Authorization: Token ...` and is registered with the redactor.
**The token has no scopes**: it acts for every team of its account. Qatlas narrows a connection through its
targets, its `tools` list or `effects`; it does not narrow the token.

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
`project/PROJECT_ID` entries and single files as `file/FILE_ID` entries. IDs are UUIDs. Without a project or file entry,
every project of the bound teams is reachable. Projects and files together are one allow-list within the bound teams:
a listed project is reachable as a whole, a listed file only itself (and what belongs to the file: pages, comments,
snapshots, libraries). A listed file does not open its project.

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
  there is one; for a tool that acts on a file, a listed `file_id` passes in place of its project. Both are checked
  before the credential is resolved and before any request is sent; the refusal does not name the ID.
- A project without an allow-list match is not enough on its own: before a project's files are listed or read, the
  projects of the bound teams are read (one `get-projects` request per team, until the project appears). A project
  outside them is refused as outside the targets.
- A connection bound to files only (no project entry) does not reach tools that act on a team or a project as a whole;
  they are shown as not reached and refused at the call, and the target form they need is named. A file outside the
  allow-list is refused before the credential is resolved, without a request to it.
- A file is bound through its project: a tool that takes `project_id` and `file_id` reads the project's file list and
  refuses a `file_id` that is not in it, before anything else is requested. The comment tools bind the file the same
  way before anything else is read.
- `teams.list` and `projects.list` show only the bound teams and allowed projects. A project that reports another
  team and a file that reports another project are dropped.

The tools of each area add their own binding rules on their pages.

## Choosing targets

The picker offers three levels: `team`, `project` below a team, and `file` below a project. It lists what the token
reaches, not only the current targets, and works on a draft connection without targets. Penpot's commands are not
paged, so each level is one bounded list without server-side search; names are untrusted data.

A URL of the connection's Penpot origin is turned into targets from the ids in its hash route: a
`#/dashboard/files?team-id=...&project-id=...` or the legacy `#/dashboard/team/TEAM_ID/projects/PROJECT_ID` gives
`team` and `project`, a `#/workspace?team-id=...&file-id=...` gives `team` and `file`. A URL of another origin or
without a usable id gives nothing; reading a URL sends no request.

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
| `penpot.media.from-url` | create | required | `create-file-media-object-from-url` | lets Penpot fetch an image from an https URL into a file |
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
| `penpot.members.set-role` | update | required, tool allow-list | `update-team-member-role` | sets a member's role to admin, editor, or viewer |
| `penpot.members.remove` | delete | required, tool allow-list | `delete-team-member` | removes a member from a bound team |
| `penpot.teams.create` | create | required, tool allow-list | `create-team` | creates a new team |
| `penpot.teams.delete` | delete | required, tool allow-list | `delete-team` | deletes a bound team with its projects and files |

## Profiles and permissions

The recommended read profile offers the four tools for teams, projects, and files; the comment tools are enabled
through the connection's `effects` or `tools` list, and so are the project and file tools.
`penpot.comments.delete`, `penpot.projects.delete`, `penpot.files.delete`, `penpot.files.purge`,
`penpot.webhooks.delete`, `penpot.snapshots.restore`, `penpot.members.set-role`, `penpot.members.remove`,
`penpot.teams.create`, and `penpot.teams.delete` are offered only when the `tools` list names them. Every tool
names its command itself; there is no `command` argument, no free path, method, or body. Reads use POST but are
idempotent and safe.

## Changes

Every change asks for `confirm`, sends exactly one request, and is never repeated. After a timeout, a connection
reset, a 5xx answer, or an unreadable answer the error says that the change may have taken effect; read the affected
projects, files, comments, libraries, or members before trying again. Refusals never name the target. A refusal for a
target outside the connection comes before the credential is resolved when the allow-list decides it, and otherwise
after reads only. The pages of the areas name where this rule differs or is extended.

## Bounds

The commands document no pagination; every list is cut on the client: 100 teams, 100 webhooks, 200 members, 1000
projects, 1000 files, 500 threads, 200 comments of a thread. A response is read up to 16 MiB; a larger one is an
`invalid-provider-response`. Names are cut at 256 bytes. Targets hold at most 20 teams and 200 projects. Requests
to one token share a rate limit.

## Errors

Errors carry a stable class and Qatlas' own message, never the token: `auth`, `permission`, `not-found`,
`rate-limited`, `timeout`, `unreachable`, `invalid-provider-response`, and `provider-error`. The message carries no
text of Penpot. A refusal by Penpot adds Penpot's answer apart from it, as untrusted data: the HTTP status and the
`type`, `code`, and `hint` of its error body, or the text of an answer that is not structured. How to read it and
what to do per symptom: [Fixing errors](setup.md#fixing-errors). The invoke log contains no arguments and no
results. Names and page contents are untrusted data of the provider; Qatlas never renders, follows, or executes
them.
