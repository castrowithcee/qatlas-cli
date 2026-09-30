---
description: >
  Describes the Penpot provider (beta): the access token and the Penpot Cloud or self-hosted base URL, the team and
  project targets, the four read tools, the bounds on file and page reads, errors, and the RPC forms it relies on.
type: knowledge
edit: shared
created: 2026-09-30
updated: 2026-09-30
---

# Penpot

This provider reads teams, projects, files, and pages of a Penpot instance, Penpot Cloud or self-hosted, with an
access token. It changes nothing.

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
  and refuses a `file_id` that is not in it, before the summary or page is requested.
- `teams.list` and `projects.list` show only the bound teams and allowed projects. A project that reports another
  team and a file that reports another project are dropped.

## Tools

| Tool | Effect | Confirmation | RPC command | Does |
| --- | --- | --- | --- | --- |
| `penpot.teams.list` | read | none | `get-teams` | lists the bound teams |
| `penpot.projects.list` | read | none | `get-projects` | lists the projects of one bound team |
| `penpot.files.list` | read | none | `get-project-files` | lists the files of one project |
| `penpot.files.get` | read | none | `get-file-summary`, `get-page` | reads a file's library summary and one page |

The read profile offers all four. Every tool names its command itself; there is no `command` argument, no free
path, method, or body. Reads use POST but are idempotent and safe.

`files.get` takes `project_id`, `file_id`, and optionally `page_id` (the first page of the file when omitted). Its
answer has `summary` (for components, variants, colors, and typographies a `count` and up to 20 `samples` with `id`
and `name`) and `page` (`id`, `name`, `shape_count`, `truncated`, and up to 200 `shapes` with `id`, `type`, `name`,
`parent_id`, `frame_id`, `x`, `y`, `width`, `height`). Shapes are listed in ID order. Fills, images, media
references, text content, and every other shape property are not returned (a deliberate limit: geometry and identity
only). A file's page list is not offered: the summary carries none, so other pages are reached only through a
known `page_id`.

## Bounds

The commands document no pagination; every list is cut on the client: 100 teams, 1000 projects, 1000 files. A
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
- Assumed, not documented: requests send kebab-case keys, responses are JSON when `Accept: application/json` is sent,
  and their keys are read case- and separator-insensitively (camelCase or kebab-case); the summary's categories carry
  `count` and `sample`; a page's `objects` map is keyed by shape ID with `type`, `name`, `parent-id`, `frame-id`, `x`,
  `y`, `width`, `height`. A different form yields empty fields, not an error.
