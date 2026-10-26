---
description: >
  Describes the time-tracking tools of the Gitea and Forgejo providers: the tracked times and stopwatches of an issue
  or pull request, the times of a repository and of a user, and the account-wide lists of the token's own times and
  stopwatches; index and row binding, the listed-only tools, how a repository without time tracking shows, and limits.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-26
---

# Time tracking

The time tools read and book tracked time on the issues and pull requests of a repository inside the connection's
targets and drive the stopwatches of the token's user. Gitea and Forgejo document the same endpoints and members, so
nothing here differs between them.

## Binding

- `repository` is chosen against the connection's targets before the token is resolved or a request is sent; it may
  only be left out when the targets name exactly one repository.
- Every change reads the issue or pull request of its index first. The answer must name a repository inside the
  targets; otherwise the call is refused as `invalid-request` before anything changes. A pull request index is
  accepted, since times and stopwatches belong to both.
- `issue-times.delete` additionally reads the times of the issue until it finds the entry and refuses an ID the
  list does not show. The API has no read of a single entry, so the narrower reading is the list of the issue; a
  user who sees only their own entries therefore cannot delete another's through this tool.
- `my-times.list` and `my-stopwatches.list` read the account behind the token across all repositories. Each row is
  checked by the repository it reports; rows without a repository or outside the targets are dropped, so a batch
  may hold fewer entries than the instance sent. Without targets everything the token reaches stays.
- `repository-times.list` and `issue-times.list` check the repository of every row as well, because a renamed
  repository answers under its old name.
- `user` and `user_name` are user names, validated as such.

## Differences between the tools

- `user-times.list` has no paging and no filter in the API; a list longer than 100 entries is cut and marked
  `truncated`. The other lists take `limit` and `cursor`, and `since` and `before` (RFC 3339), and, except the two
  account-wide ones, `user`.
- `issue-times.add` books for the token's user unless `user_name` names another; the instance decides whether the
  token may. Each call adds an entry and is not idempotent.
- `issue-stopwatches.stop` books the elapsed time as a tracked time entry. `issue-stopwatches.delete` discards the
  running stopwatch without booking anything; the elapsed time is lost. Starting a running stopwatch and stopping or
  discarding a missing one are refused by the instance (HTTP 409).
- Results show every documented member except the deprecated `issue_id` and `user_id`. The issue of a tracked time
  keeps identifiers only. User names and issue titles are untrusted personal data; the data class is the one of
  the issue tools.

## Time tracking turned off

A repository can have time tracking switched off. The instance then answers the time endpoints of a repository with
HTTP 400; the API documents that status for add, delete, reset, and the repository and user lists. Qatlas treats a
400 on any of the time endpoints as "not available" with a hint to the repository settings and never quotes the
instance's text. The stopwatch endpoints document only 403, 404, and 409, so a repository without time tracking
shows there as the usual `not-found`; the account-wide lists simply omit such repositories.

## Listed-only tools

`issue-times.delete` and `issue-times.reset` delete for good and are offered only by a connection whose `tools` list
names them; no setup profile holds them. `reset` removes the entries of every user of the issue.

## Tools

| Tool | Effect | Provider |
| --- | --- | --- |
| `issue-times.list`, `repository-times.list`, `user-times.list` | read | both |
| `my-times.list`, `my-stopwatches.list` | read | both |
| `issue-times.add` | create | both |
| `issue-stopwatches.start`, `issue-stopwatches.stop` | update | both |
| `issue-stopwatches.delete` | delete | both |
| `issue-times.delete`, `issue-times.reset` | delete, listed-only | both |
