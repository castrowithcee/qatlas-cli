---
description: >
  Describes the lock tools of the Gitea and Forgejo providers: locking and unlocking the conversation of an issue or
  pull request.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-26
---

# Issue locks

The `issue-locks` tools hold for both providers; Forgejo has them from version 17 (see
[Forgejo](../forgejo.md)). Every tool takes `repository` (`OWNER/REPO`), checked against the targets before the
secret is resolved; it may be left out when the targets name exactly one repository.

| Tool | Effect | Provider |
| --- | --- | --- |
| `issue-locks.lock`, `issue-locks.unlock` | update | both |

## Changing

- Issues and pull requests share one index space, so both tools act on either by `number`.
- `lock` takes an optional `reason`, plain text without control characters. Neither product documents a list of
  values, so it is passed on as given. `unlock` takes no reason.
- The issue or pull request is read first and its canonical repository must lie inside the targets; only then
  the single change is sent. A repository, index, or reason that fails the check is refused before any request.
- Neither is repeated after a timeout, an abort, or a 5xx answer; the error then says the change may have been
  applied.
