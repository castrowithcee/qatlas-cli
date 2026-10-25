---
description: >
  Describes the GitHub discussion tools: the categories, discussions, and comments of one repository, how organization discussions are read, and creating discussions and writing comments and replies, with their limits.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# GitHub discussions

The discussion tools of the [GitHub provider](README.md).

## Discussions

Four read tools and four write tools cover the GitHub Discussions of one repository, all through GraphQL and
all taking `repository` like the other repository tools; a repository outside the connection's targets is
refused as an invalid request before any credential is resolved.

| Tool | Does |
| --- | --- |
| `github.discussion-categories.list` | lists the discussion categories of a repository |
| `github.discussions.list` | lists one bounded batch of discussions, filtered by category, state, and answered status |
| `github.discussions.get` | reads one discussion by its number |
| `github.discussion-comments.list` | lists one bounded batch of the top-level comments of a discussion, with their replies on request |
| `github.discussions.create` | starts a discussion in a category of the repository |
| `github.discussion-comments.create` | adds a comment or a reply |
| `github.discussion-comments.update` | replaces the body of a comment or reply |
| `github.discussion-comments.delete` | deletes a comment or reply; listed only |

A discussion body is cut at 1000 characters in a list (`body_truncated` says so) and at 20000 characters in
`github.discussions.get`. `github.discussion-comments.list` lists the top-level comments of one discussion,
oldest first, with a body cut at 4000 characters. Replies are counted; with `include_replies=true` each
top-level comment also carries at most 100 replies, GitHub's maximum, each body cut at 4000 characters with
`body_truncated`, and `replies_truncated` says that more replies exist. The reply bodies of one answer share a
budget of 200000 characters; once it is spent, further reply bodies come back empty with `body_truncated`. A
cursor of a list without replies does not continue a list with them. Titles, bodies, and comments come from
other accounts and are untrusted data.

A discussion, a comment, and a reply answer GitHub's Discussion and DiscussionComment in full. The repository
and the discussion a call names are fixed by the call and not repeated in every entry. Rendered variants
repeat the text field in another format and add no content. A connection GitHub also serves as a list of its
own is read with that list, so the comments, reactions, and edit history of a discussion and the reactions
and edit history of a comment are reported as counts, and reaction groups only for kinds somebody gave. The
labels and the poll options are read with their first page, at most 20 labels and, in
`github.discussions.get`, 20 poll options, in `github.discussions.list` 10, each with its total and a marker
when more exist. The chosen answer is named with its id, address, author, and time; its text is read with
`github.discussion-comments.list`. An account inside another object carries the fields GitHub's own REST
answers give it; the full profile is read with the account tools. The `viewer_*` flags describe what the
token's user may do and has done.

A cursor belongs to the repository and the filters that produced it, and another filter, category, or
discussion refuses it as an invalid request. A discussion that GitHub does not hold or does not show to the
token is `not-found`, named as "discussion #N".

Organization discussions are read through the repository that stores them, usually `ORG/.github`: name that
repository as `repository`, and it must be among the connection's targets. Qatlas does not fall back to the
`.github` repository of an organization on its own.

`github.discussion-comments.create` adds a top-level comment to a discussion `number`, or, with `reply_to`
(the node `id` of a top-level comment of that discussion), a reply; GitHub allows one level of replies, so a
reply to a reply is refused. `github.discussion-comments.update` replaces the body of a comment or reply by its
node `id` (`comment_id`), and `github.discussion-comments.delete` removes one permanently. Before any mutation
the comment or category is read back and must belong to the chosen `repository`; a foreign one is refused
(`not-found`, or `invalid-request` for a reply to a reply or to a comment of another discussion) without a
change. Written bodies are not echoed. Every change is sent once and is never retried, see
[confirmed changes](README.md#confirmed-changes); an unclear outcome says it may have been applied.
Discussions are not closed, locked, marked as answered, or edited as a whole, and polls are not written.
Creating is not idempotent, since each request adds a new object; updating is idempotent, since the same body
leaves the same state; deleting has unknown idempotency, since GitHub answers a second delete of the same
comment with an error. Deleting is offered only where the connection's `tools` list names
`github.discussion-comments.delete`.

The not-recommended setup profile `discussions` ticks the read tools, `github.discussions.create`,
`github.discussion-comments.create`, and `github.discussion-comments.update`; the delete stays unticked, and
the recommended profile `read` is unchanged.

The token rights are listed under [Discussions](setup.md#discussions).
