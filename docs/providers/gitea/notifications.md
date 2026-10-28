---
description: >
  Describes the notification tools of the Gitea and Forgejo providers: listing and counting the notifications of the
  account, reading and marking a thread, and marking the notifications of a repository or of the whole account.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-28
---

# Notifications

The `notifications` and `repository-notifications` tools hold for both providers. A notification thread belongs to
the account behind the token, not to a repository, so the targets bind a thread through its repository.

| Tool | Effect | Provider |
| --- | --- | --- |
| `notifications.list`, `repository-notifications.list` | read | both |
| `notifications.check-new` | read | both |
| `notifications.get` | read | both |
| `notifications.mark-thread` | update | both |
| `notifications.mark-all-read`, `repository-notifications.mark-read` | update, listed-only | both |

Watching a repository and the subscription to an issue are not part of this group; see
[Stars and watching](stars-and-watching.md) and [Issue subscriptions](issue-subscriptions.md).

## Targets

- `list` drops every row whose canonical repository lies outside the targets, so a batch may hold fewer rows than
  the instance sent; the end of the list still comes from the instance. `repository-notifications.list` takes
  `repository` like every repository tool.
- `get` and `mark-thread` read the thread first on a connection with targets or exclusions and refuse one whose
  canonical repository lies outside the targets or inside an exclusion, without naming it; `mark-thread` then sends
  nothing. Without either, `mark-thread` sends its one request directly.
- `check-new` counts over every repository of the account and `mark-all-read` marks over all of them. Both reach past
  any target and are refused before the secret and any request on a connection with targets.
- With [exclusions](README.md#exclusions) but no targets, `mark-all-read` is refused and `check-new` stays allowed
  as an aggregated count.
- `repository-notifications.mark-read` takes `repository`, checked against the targets before the secret; with
  targets, the canonical repository of the path is read first.

## Marking

- `mark-all-read` and `repository-notifications.mark-read` are listed-only: only a connection whose `tools` list
  names them offers them, and no setup profile does.
- `to-status` is `read`, `unread`, or `pinned`; `read` when left out. `status-types` and `last_read_at` narrow a
  bulk marking. Without `last_read_at` a repeat can reach threads that changed in between.
- A successful answer of the instance is discarded unread. Exactly one request is sent after the evidence reads. None
  is repeated after a timeout, an abort, or a 5xx answer; the error then says the change may have been applied.

## Differences

`subject-type` accepts `commit` on Gitea only; Forgejo refuses it before the secret is read. Subjects and repository
names are untrusted data.
