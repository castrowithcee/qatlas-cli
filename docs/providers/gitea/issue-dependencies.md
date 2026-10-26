---
description: >
  Describes the dependency tools of the Gitea and Forgejo providers: listing, adding, and removing what an issue or
  pull request depends on and what it blocks, how both ends are bound to the targets, and how a counterpart outside
  them is shown.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-26
---

# Issue dependencies

The `issue-dependencies` tools act on the issues and pull requests that one issue or pull request depends on, the
`issue-blocks` tools on those it blocks. The instance stores one relation and shows it from both ends, so adding
"A depends on B" also shows "B blocks A"; the two groups are views of the same data, split by direction. They hold
for both providers. Every tool takes `repository` (`OWNER/REPO`), checked against the targets before the secret is
resolved; it may be left out when the targets name exactly one repository.

| Tool | Effect | Provider |
| --- | --- | --- |
| `issue-dependencies.list`, `issue-blocks.list` | read | both |
| `issue-dependencies.add`, `issue-blocks.add` | create | both |
| `issue-dependencies.remove`, `issue-blocks.remove` | delete | both |

## Counterpart and binding

- `add` and `remove` name the counterpart by `dependency_number` or `blocked_number` and, when it lies in another
  repository, by `dependency_repository` or `blocked_repository`; without it the counterpart is in `repository`.
- Both ends must lie inside the targets. The counterpart repository is checked before the secret is resolved and a
  refusal is an `invalid-request` that does not name it. Then both issues are read: the canonical repository of each
  answer must lie inside the targets, and an issue that does not exist is not found. Only after that is the single
  change sent. An issue is not related to itself.
- The relation body carries owner, repository, and index of the counterpart; no free path or body reaches the instance.

## Adding and removing

- `add` sends one `POST` (201) and is not idempotent: the instance refuses a relation that exists or would close a
  cycle. `remove` sends one `DELETE` with the same body and detaches the relation only, neither issue; the
  instance documents no answer for a relation that does not exist, so its idempotency is declared unknown.
- An archived repository refuses a change of `issue-dependencies` (HTTP 423).
- Neither is repeated after a timeout, an abort, or a 5xx answer; the error then says the change may have been
  applied.

## Lists

- Both lists page with `limit` and `cursor` like the other paged lists; the cursor is bound to the repository and
  the issue.
- Each entry carries every member of the issue object that the product documents, as in the [issues](issues.md)
  tools. Titles, bodies, and names are untrusted data.
- An entry whose canonical repository lies outside the targets is not dropped: it shows only `number` and
  `repository.full_name` and sets `withheld`. An entry that names no repository is shown the same way, by its
  number alone. Without targets everything is shown in full.
