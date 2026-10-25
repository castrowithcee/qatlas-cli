---
description: >
  Describes the GitHub notification tools: the notification threads of the account behind a token, marking them read or done, thread and repository subscriptions, and how targets apply to notifications.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# GitHub notifications

The notification tools of the [GitHub provider](README.md).

## Notifications

Five tools read and manage the notification threads of the account behind the connection's token and their
subscriptions; the repository subscription is described with the
[repositories](repositories.md#repository-subscriptions). GitHub supports the notification endpoints only
with a classic token and Qatlas refuses any other token before a request; see
[Notifications](setup.md#notifications).

| Tool | Does |
| --- | --- |
| `github.notifications.list` | lists one bounded batch of notification threads with filters |
| `github.notifications.get` | reads one thread by its id |
| `github.notifications.dismiss` | marks a thread read or done |
| `github.notifications.mark-all` | marks all threads of the account or a repository read; listed only |
| `github.thread-subscriptions.set` | watches, ignores, or unsubscribes a thread |

`github.notifications.list` lists unread threads unless `include_read=true`. An entry's `id` is the
`thread_id` of the other tools, and `subject_title` is untrusted data from other accounts, cut at 500
characters with `title_truncated`. A cursor belongs to the repository and the filters that produced it, and
another filter or repository refuses it as an invalid request. `github.notifications.mark-all` marks every thread of the
account or of one `repository` read; GitHub answers 202 when it finishes in the background (`queued=true`) and
205 when it is done.

A thread carries the repository object GitHub sends with it and the REST addresses of the thread in
`links`; a repository outside the connection's targets is named only by its identifiers, and
`temp_clone_token` grants clone access without sign-in and is withheld. The addresses are untrusted data and are
never fetched.

A notification belongs to the account, not to a repository, so the targets act as follows. A thread tool reads
the thread first and refuses one whose repository the targets do not admit as `not-found`, without a change; a
repository target admits its repository, an owner target the repositories of that owner. A connection whose
targets name only projects has no repository to admit and is refused as an invalid request. The list and
`mark-all` take an optional `repository`, which must lie inside the targets, as a repository target or
through an owner target. Without `repository`, a connection whose targets name repositories needs the one
repository they name exactly, otherwise it is an invalid request; a connection without targets lists the
whole account; a connection whose targets name only owners lists the account narrowed to the repositories of
those owners. GitHub cannot narrow "mark everything read" to an owner, so `mark-all` without `repository` is
allowed only on a connection without any target. Every refusal happens before a credential is resolved,
except the thread check, which needs the credential for its one read.

Every change is sent once and is never retried, see [confirmed changes](README.md#confirmed-changes).
`github.notifications.mark-all` is offered only where the connection's `tools` list names it. GitHub documents
no answer for repeating a mark as done, a mark-all, or a delete, so those changes declare their idempotency as
unknown; a mark as read and setting a thread subscription to watch or ignore leave the same state when repeated. A
repeated mark as done, or a thread subscription change on a thread that is gone, can answer `not-found`
because the pre-read no longer finds the thread.

The not-recommended setup profile `notifications` ticks the list, the get, the dismiss, and both subscription
tools, the repository subscription included; `github.notifications.mark-all` stays unticked, and the recommended profile `read` is unchanged.
