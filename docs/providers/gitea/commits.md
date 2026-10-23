---
description: >
  Describes the commit, comparison, commit status, and Git object tools of the Gitea and Forgejo providers: what
  they read and change, the token scopes, the repository binding, the argument rules, the differences between the
  products and versions, and the bounds of diffs, blobs, and lists.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-23
---

# Commits, comparison, status, and Git objects

[Gitea](README.md) describes the shared configuration, targets, pagination, and errors, and
[Forgejo](../forgejo.md) lists what differs. Commit messages, author names, paths, blob and note contents, tag
messages, and status texts are untrusted data. Names and email addresses in commits are personal data.

| Tool | Effect | Provider |
| --- | --- | --- |
| `commits.list`, `commits.get`, `commits.get-diff` | read | Gitea, Forgejo |
| `compare.get` | read | Gitea, Forgejo |
| `commit-statuses.list`, `commit-statuses.get-combined` | read | Gitea, Forgejo |
| `commit-statuses.create` | create | Gitea, Forgejo |
| `git-blobs.get`, `git-trees.get`, `git-refs.list`, `git-tags.get`, `git-notes.get` | read | Gitea, Forgejo |
| `git-blobs.list` | read | Forgejo |
| `git-notes.set` | update | Forgejo |
| `git-notes.delete` | delete, listed-only | Forgejo |

## Scopes

Presumably `read:repository` for the reads and `write:repository` for `commit-statuses.create` and
`git-notes.set|delete` (the API serves them in the repository route group); none was checked against a live
instance. A missing scope shows as `not-found`.

## Target binding

Every path carries the repository, chosen against the connection's targets before the token is read; a repository
outside the targets is refused as `invalid-request` without quoting it. A commit, status, blob, or note is not an
identifier of its own, so the instance binds it to the repository and no evidence read is needed.

## Arguments

- A commit, blob, tree, tag, or note ID is 7 to 64 hexadecimal digits. A branch is not accepted where an ID is
  required, although the API would resolve it.
- A reference (`sha` and `not` of `commits.list`, `ref`, the two sides of `basehead`) is checked as a Git name before
  any request and may hold slashes; each part is escaped as its own path segment.
- `path` of `commits.list` is normalized; an absolute path, a backslash, and any `..` part are refused.
- `compare.get` takes `BASE...HEAD` in `basehead`. A fork side such as `OWNER:BRANCH` is refused: the API
  description names no second repository, so a comparison never leaves the repository of the connection.
- `commit-statuses.list` takes exactly one of `ref` and `sha`.
- `git-trees.get` pages with `page` and `per_page`, not `limit`. `git-refs.list` is not paged by the instance: `ref`
  narrows the list to a name or prefix, and `truncated` marks a cut list.

## Changes

- `commit-statuses.create` sends one request that is never repeated and is not idempotent: each call adds a status.
  `target_url` must be an http or https address and is stored only; Qatlas never fetches it. A result address with
  user info or a query is left out.
- `git-notes.set` sends one request that is never repeated; the same text reaches the same state.
- `git-notes.delete` removes the note for good. It is offered only when the connection's `tools` list names it and
  is in no setup profile.

## Bounds

- Diffs, patches, comparisons, and blob contents are cut with `truncated`; a blob that is no UTF-8 text is reported
  with `binary` and its size, without content. An instance answer above the response limit is refused.
- Commit verification leaves out the raw payload and signature.
- Commit lists continue by the instance's report of a further page, never by the length of a batch.
- A member, filter, or switch of the other product is refused before the token is read; a switch on an instance
  older than the version that knows it is refused as not available, because the instance would otherwise ignore it.
