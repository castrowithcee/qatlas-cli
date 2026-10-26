---
description: >
  Describes the project board tools of the Gitea provider: projects, their columns, and the issues in a column on the
  repository, organization, and user level; the Gitea version they need, target and ID binding, the user level, issue
  binding, and limits.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-26
---

# Projects

`projects.*`, `project-columns.*`, and `project-issues.*` read and manage the project boards of Gitea 28 and later.
They are registered for Gitea only; Forgejo has no project API (see [Forgejo](../forgejo/README.md)). On Gitea 1.27 every
tool reports itself unavailable, with the version it needs, before its request. A board holds issues and pull requests
in columns; the API has no fields, views, iterations, or automations. Titles, descriptions, and column names are
untrusted data. Deleting a project or a column is offered only by a connection whose `tools` list names the tool and
is in no setup profile.

[Gitea](README.md) describes the shared configuration, targets, pagination, and errors.

## Levels and targets

Every tool takes exactly one of `repository` (`OWNER/REPO`) and `owner` (`orgs/NAME` or `users/NAME`); both or
neither is `invalid-request`. The handler picks the fixed path of the level: the repository's, the organization's, or
the token's own (`/user/projects`).

- `repository` follows the targets like every repository tool and has no default.
- `owner` needs the owner as an owner target of the same kind (`orgs/NAME` for `orgs/NAME`), or no targets at all. A
  `repos/OWNER/REPO` target reaches only the projects of that repository, never those of its owner.
- Both refusals come before the token is read and do not name the value.

## User level

The API serves another user's projects only as a public list. `projects.list` with `owner` `users/NAME` reads that
list (`/users/NAME/projects`). Every other operation on a user's projects acts on the account behind the token, so
`users/NAME` is accepted for them only when `GET /user` names that account (compared without case); otherwise the
call is `invalid-request` before anything is sent to the project paths. The token's own private projects are
therefore reached through the other tools, not through the list.

## IDs

- A project ID is bound by the path of its level; `projects.get`, `update`, and `delete` check that the answer carries
  the requested ID, and update and delete read the project first. `project-columns.list`, `create`, and `move` also
  read the project first.
- A column is read on its project's path before it is changed, deleted, made default, listed, or used as the target of
  an issue. The answer must carry the column ID and the project ID; otherwise the call fails and nothing is changed.
  `project-columns.list` drops rows of another project.
- `project-columns.move` takes every column ID of the project once, in the wanted order; the columns are read first and
  a differing set is refused.

## Issues

The API takes the internal ID of an issue, not its index, and offers no read by that ID. The issue tools therefore
take `issue_repository` and `number` and read the issue or pull request to get its ID.

- The repository level takes `issue_repository` only to repeat `repository`; the owner levels require it and it must
  belong to that owner. It must lie inside the targets. These checks come before the token is read.
- The evidence read of the issue comes before the project is touched. The canonical repository of the answer must
  lie inside the targets and on the level, otherwise the call is refused and no request goes to the project. A
  repository renamed since the argument was written is refused on the repository level.
- `project-issues.add`, `remove`, and `move` send the internal ID in the path. `move` sends the target column and
  `position` as `sorting`; without a position the issue goes to the end of the column. All three are idempotent.
- `project-issues.list` returns the issues and pull requests of a column, with the members of the issue object, and
  drops rows of a repository outside the targets.

## Fields

- A project carries the members the API documents, without the deprecated `is_closed` and `creator_id`.
  `projects.update` takes `state` (`open`, `closed`); `projects.list` takes `state` (`open`, `closed`, `all`).

## Tools

| Tool | Effect |
| --- | --- |
| `projects.list`, `projects.get`, `project-columns.list`, `project-columns.get`, `project-issues.list` | read |
| `projects.create`, `project-columns.create` | create |
| `projects.update`, `project-columns.update`, `project-columns.move`, `project-columns.set-default`, `project-issues.add`, `project-issues.remove`, `project-issues.move` | update |
| `projects.delete`, `project-columns.delete` | delete, listed only |
