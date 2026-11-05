---
description: >
  Describes the GitHub pull request review tools: conversation comments, reviews with bundled line comments, replies, review threads, requested reviewers, and the listed-only approve.
type: knowledge
edit: shared
created: 2026-11-05
updated: 2026-11-05
---

# GitHub pull request reviews

The pull request review tools of the [GitHub provider](README.md). Every tool takes `repository` as described under
[the target of a call](README.md#the-target-of-a-call); a change needs the confirmation described under
[confirmed changes](README.md#confirmed-changes). The pull requests themselves are described under
[pull requests](pull-requests.md).

## Pull request reviews and comments

The tools of this section read and write a pull request's conversation comments, its reviews, the line
comments a review bundles in, replies to an existing line comment, its review threads, and its requested
reviewers. `github.comments.*` is unchanged and keeps refusing a pull request number; the two conversation
comment tools of this section are its mirror image, refusing an issue number instead, described below. A
line comment is never written on its own: it exists only bundled into the one review
that creates it, in `github.pull-request-reviews.create` or `.approve`, or as a reply to an existing one in
`github.pull-request-review-comments.reply`. The eleven reads and changes below `approve` are offered by the
not-recommended setup profile `pull-request-reviews`; approving is in no profile, offered only while a
connection's `tools` list names `github.pull-request-reviews.approve`, because an approval given on the
connection's behalf is the one outcome no other review tool can reach.

| Tool | Does |
| --- | --- |
| `github.pull-request-comments.list` | lists the conversation comments of a pull request, oldest first |
| `github.pull-request-comments.create` | writes exactly one conversation comment |
| `github.pull-request-reviews.list` | lists the reviews of a pull request |
| `github.pull-request-reviews.create` | submits a review as `comment` or `request_changes`, with its line comments bundled in |
| `github.pull-request-reviews.approve` | approves, with an optional body and line comments; offered only where a connection's `tools` list names it |
| `github.pull-request-review-comments.list` | lists the line comments of a pull request, across every review |
| `github.pull-request-review-comments.reply` | writes exactly one reply in the thread of one line comment |
| `github.pull-request-review-threads.list` | lists the review threads of a pull request, each with its resolved state and its comments |
| `github.pull-request-review-threads.resolve` | marks one review thread resolved |
| `github.pull-request-review-threads.unresolve` | opens one resolved review thread again |
| `github.pull-request-reviewers.request` | requests users, teams, or both as reviewers |
| `github.pull-request-reviewers.remove` | removes users, teams, or both from the requested reviewers |

```yaml
connections:
  pull-reviewer:
    service: github
    credential: github-operator
    target: repos/octo-org/example
    tools: [github.pull-request-comments.list, github.pull-request-comments.create,
      github.pull-request-reviews.list, github.pull-request-reviews.create,
      github.pull-request-review-comments.list, github.pull-request-review-comments.reply,
      github.pull-request-review-threads.list, github.pull-request-review-threads.resolve,
      github.pull-request-review-threads.unresolve, github.pull-request-reviewers.request,
      github.pull-request-reviewers.remove]
  pull-approver:
    service: github
    credential: github-operator
    target: repos/octo-org/example
    tools: [github.pull-request-reviews.approve]
```

`github.pull-request-comments.list` and `.create` read and write a pull request's conversation comments through
the same Issue Comments REST route `github.comments.*` uses; an issue number is refused as `invalid-request`,
naming the number and the repository, the mirror image of the refusal `github.comments.*` gives a pull
request number.

`github.pull-request-reviews.create` and `.approve` require `sha`, the pull request's head commit exactly as
`github.pull-requests.get` reports it, sent to GitHub as `commit_id`, so a review never lands on a head the
caller did not see. `event` is required for `.create` and always an approval for `.approve`; `line_notes` bundles the review's line
comments into this one call, at most 50. The argument is named `line_notes`, not
`comments`, because a change tool's input schema may not offer an argument by that name. A head that changed
since it was read, GitHub reviewing one's own pull request, an unknown commit, or a line comment naming a
line outside the diff all answer 422; Qatlas re-reads the pull request once to tell a changed head apart
and refuses `invalid-request` naming the expected and the current commit, or, for another reason, answers
`provider-error` naming the likely causes. Neither is ever retried automatically.

`github.pull-request-review-comments.list` reads a pull request's line comments across every review. `sort`,
`direction`, and `since` (an RFC 3339 time, sent to GitHub in UTC) are part of the cursor binding when set, so a
continuation never pages with other filters.
`github.pull-request-review-comments.reply` takes `comment_id`, as the list reports it, and writes exactly one
reply in its thread; a `comment_id` GitHub does not hold answers not found, naming the review comment.

GitHub keeps whether a review thread is resolved only in GraphQL, never in the REST line comments read
above, so `github.pull-request-review-threads.list`, `.resolve`, and `.unresolve` read and change that state on
their own, by the thread's `id` as the list reports it. The list reports the first 50 comments of a thread, with
`comments_total_count` and `comments_truncated`: "A connection GitHub also serves as a list of its own is read with
that list; this tool reports its count." The line comments of a pull request are read with
`github.pull-request-review-comments.list`. `.resolve` and `.unresolve` also take `number`: before
either changes anything, Qatlas reads the thread node itself and confirms it is a thread of `number` in the
bound repository; a `thread_id` of another pull request or another repository is refused not found, naming
"this review thread", and nothing is sent. Resolving an already-resolved thread, or unresolving an
already-open one, is left as it is and reported in its current state.

`github.pull-request-reviewers.request` and `.remove` take users, teams of the repository's organization, or both,
at least one and at most 15 together, and answer with the pull request itself, the same shape
`github.pull-requests.get` reports, so `requested_reviewers` is read back after the change. A reviewer already
requested, one not requested, and the pull request's own author are all left as they are.
