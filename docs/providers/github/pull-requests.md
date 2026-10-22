---
description: >
  Describes the GitHub pull request tools: pull request reads and changes, the listed-only merge and its conflicts, conversation comments, reviews with bundled line comments, replies, review threads, requested reviewers, the listed-only approve, pull request search, Copilot reviews, and the token each needs.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# GitHub pull requests

The pull request tools of the [GitHub provider](README.md). Every tool takes `repository` as described under
[the target of a call](README.md#the-target-of-a-call); a change needs the confirmation described under
[confirmed changes](README.md#confirmed-changes).

## Pull requests

GitHub's project items and its issue and comment tools still see a pull request only as an item, or refuse
its number as an `invalid-request`; the tools of this section read, create, change, close, reopen, and merge
the pull requests of a repository themselves. Reviewing a pull request, commenting on its conversation, and
line comments are a separate group of tools, described under
[Pull request reviews and comments](#pull-request-reviews-and-comments). The six reads are offered by the not-recommended setup profile `pull-requests`; the not-recommended
profile `pull-requests-operator` adds the changes, described under [Changes](#changes) below, but never the
merge, described under [Merge and conflicts](#merge-and-conflicts): it is offered only where a connection's
`tools` list names `github.pull-requests.merge`, because a merge into the wrong repository's default branch is
the costliest mistake there. Neither profile is offered by the recommended profile `read`. Every route lies
below the chosen repository; no tool accepts an owner or a free REST path.

| Tool | Does |
| --- | --- |
| `github.pull-requests.list` | lists compact pull requests |
| `github.pull-requests.get` | reads one pull request with its body, merge state, and requested reviewers |
| `github.pull-request-files.list` | lists its changed files with a bounded patch excerpt |
| `github.pull-request-commits.list` | lists its commits |
| `github.pull-request-diffs.get` | reads its unified diff, cut to at most 65536 bytes from its start |
| `github.pull-request-checks.list` | reads the check runs and the combined commit status at its head commit |

```yaml
connections:
  pull-reader:
    service: github
    credential: github-reader
    target: repos/octo-org/example
    tools: [github.pull-requests.list, github.pull-requests.get, github.pull-request-files.list,
      github.pull-request-commits.list, github.pull-request-diffs.get, github.pull-request-checks.list]
```

`github.pull-requests.list` filters by `state`, `base`, and `head` (a branch name, or `LOGIN:branch` for a fork), in
GitHub's own order. Title, body, branch names, and commit messages are untrusted data.
`github.pull-requests.get` reports `mergeable` as absent while GitHub is still computing it.

`github.pull-request-files.list` reads the changed files with a patch excerpt of at most 4096 bytes, cut at a valid
character boundary, with `patch_truncated` when GitHub's patch held more; a file GitHub sends no patch for, such
as a binary file, carries none. `github.pull-request-commits.list` reports a commit's author as the linked
GitHub login when GitHub reports one, otherwise the commit's author name; the message is untrusted data.
`github.pull-request-diffs.get` reads the unified diff through GitHub's diff media type, from its start, up to
the hard limit of 65536 bytes; `truncated` says when the diff held more. None of the three is ever read
unbounded.

`github.pull-request-checks.list` takes `number` alone: it reads the pull request to resolve its head commit,
then the check runs (the GitHub Checks API, which Actions and most third-party CI systems report through) and
the combined legacy commit status (the older Status API) at that commit, and merges them into one compact
list; a legacy status is mapped into the same vocabulary, with `pending` becoming `in_progress` without a
conclusion. `overall` summarises them:
`pending` while a check still runs, `failure` once a finished check did not succeed, `success` when every one
did, and `none` when nothing reported a check for the commit. `truncated` says when GitHub held more than the
100 check runs or 100 statuses read per call.

### Changes

| Tool | Does |
| --- | --- |
| `github.pull-requests.create` | opens one pull request |
| `github.pull-requests.update` | changes the given fields, marks it ready for review or a draft; left-out fields stay |
| `github.pull-requests.close` | closes it without merging; a pull request already closed, merged or not, is left as it is |
| `github.pull-requests.reopen` | opens a closed pull request again; a merged one is refused |
| `github.pull-request-branches.update` | merges the base branch into the head branch while the head is still a given commit |

`github.pull-requests.create` and `github.pull-requests.update` write `title` and `body` through REST, exactly
as the issue tools do. `github.pull-requests.update` writes a
`draft` change through a separate GraphQL mutation, `markPullRequestReadyForReview` or
`convertPullRequestToDraft`, because GitHub's REST route has no field for it; when both a REST field and
`draft` are given, the REST change is sent first, then the draft change, and the pull request is read once
more only then, so the answer never reports a draft state the REST answer could not have known about.
`github.pull-requests.close` and `github.pull-requests.reopen` read the pull request first: a repeated call on
a pull request already in the state asked for changes nothing and is success, like the issue tools; reopening
a pull request GitHub reports as merged is refused before a request is sent, because GitHub has no way to
reopen a merged pull request.

`github.pull-request-branches.update` requires `expected_head_sha`, the pull request's current head commit as
`github.pull-requests.get` reports it, so a head that changed since it was last read is never merged into
blindly; GitHub queues the merge of the base branch into the head branch and answers `accepted: true` before
it finishes, so the new head commit shows up only on a later read. A stale `expected_head_sha` is refused
without queuing anything.

### Merge and conflicts

`github.pull-requests.merge` merges one pull request with a REST `PUT`. It is high risk, on the scale of the
[listed-only workflow maintenance tools](actions.md#workflow-maintenance-and-actions-administration): a connection
offers it only while its `tools` list names it, whatever its `effects`, and no profile a new connection
starts with, recommended or not, may select it.

| Tool | Does |
| --- | --- |
| `github.pull-requests.merge` | merges one pull request; offered only where a connection's `tools` list names it |

`sha` is required: the pull request's current head commit, exactly as `github.pull-requests.get` reports it.
Qatlas reads the pull request first. Already merged with exactly that head commit is success without another
request, so a repeated merge with the same `sha` never fails; already merged with a different head commit is
refused as `invalid-request`, naming both commits. Otherwise Qatlas sends the merge exactly once and never
retries it:

- **409 Conflict** means GitHub compared `sha` against a head that changed since it was read. Qatlas reads
  the pull request again to name its now-current head and refuses as `invalid-request`, naming the expected
  and the current commit, so the next call can read the pull request again and merge with the head it now
  has, or accept it and merge again.
- **405 Method Not Allowed** means GitHub cannot merge the pull request in its current state, for example
  branch protection, a missing required review, or a failing check. Qatlas answers `provider-error` with that
  reason; the merge is never repeated automatically, since GitHub's decision only changes once whatever
  blocks the merge is resolved.

```yaml
connections:
  pull-operator:
    service: github
    credential: github-operator
    target: repos/octo-org/example
    tools: [github.pull-requests.list, github.pull-requests.get, github.pull-request-files.list,
      github.pull-request-commits.list, github.pull-request-diffs.get, github.pull-request-checks.list,
      github.pull-requests.create, github.pull-requests.update, github.pull-requests.close,
      github.pull-requests.reopen, github.pull-request-branches.update]
  pull-merger:
    service: github
    credential: github-operator
    target: repos/octo-org/example
    tools: [github.pull-requests.get, github.pull-requests.merge]
```

`pull-operator` never offers the merge, whatever its effects, because its `tools` list does not name it;
`pull-merger` offers only the read `github.pull-requests.get` and the merge, so a caller reads the current head
commit and merges with it in two calls on the same connection.

### Tokens for pull requests

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.pull-requests.list`, `get`, `github.pull-request-files.list`, `github.pull-request-commits.list`, `github.pull-request-diffs.get` | `repo` for a private repository; `public_repo` for a public one | Pull requests: read |
| `github.pull-request-checks.list` | `repo` | Checks: read, and Commit statuses: read |
| `github.pull-requests.create`, `update`, `close`, `reopen`, `github.pull-request-branches.update` | `repo` | Pull requests: read and write |
| `github.pull-requests.merge` | `repo` | Pull requests: read and write, plus Contents: read and write |

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

`github.pull-request-review-comments.list` reads a pull request's line comments across every review.
`github.pull-request-review-comments.reply` takes `comment_id`, as the list reports it, and writes exactly one
reply in its thread; a `comment_id` GitHub does not hold answers not found, naming the review comment.

GitHub keeps whether a review thread is resolved only in GraphQL, never in the REST line comments read
above, so `github.pull-request-review-threads.list`, `.resolve`, and `.unresolve` read and change that state on
their own, by the thread's `id` as the list reports it. `.resolve` and `.unresolve` also take `number`: before
either changes anything, Qatlas reads the thread node itself and confirms it is a thread of `number` in the
bound repository; a `thread_id` of another pull request or another repository is refused not found, naming
"this review thread", and nothing is sent. Resolving an already-resolved thread, or unresolving an
already-open one, is left as it is and reported in its current state.

`github.pull-request-reviewers.request` and `.remove` take users, teams of the repository's organization, or both,
at least one and at most 15 together, and answer with the pull request itself, the same shape
`github.pull-requests.get` reports, so `requested_reviewers` is read back after the change. A reviewer already
requested, one not requested, and the pull request's own author are all left as they are.

### Tokens for pull request reviews and comments

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.pull-request-reviews.list`, `github.pull-request-review-comments.list`, `github.pull-request-review-threads.list` | `repo` for a private repository; `public_repo` for a public one | Pull requests: read |
| `github.pull-request-reviews.create`, `.approve`, `github.pull-request-review-comments.reply`, `github.pull-request-review-threads.resolve`, `.unresolve`, `github.pull-request-reviewers.request`, `.remove` | `repo` | Pull requests: read and write |
| `github.pull-request-comments.list`, `.create` | as `github.comments.*`: `repo` or `public_repo` | Pull requests or Issues: read; read and write for `.create` |

## Pull request search

`github.pull-requests.search` searches pull requests, never plain issues: it forces `is:pr` ahead of `terms`, next
to the target qualifiers described under [Search](README.md#search).

## Copilot reviews

`github.copilot-reviews.request` requests Copilot as reviewer of one pull request of the bound repository and
answers with the pull request as `github.pull-requests.get` reports it. It is idempotent: requesting again
leaves Copilot requested. It does not wait for what Copilot does afterwards. GitHub answers a missing write
access with `permission`, and a request it rejects with an error that names the license or the repository
setting as the likely reason.

It covers the official tool `request_copilot_review` of the GitHub MCP server (MIT, `pkg/github/copilot.go` at
commit 85598ba). It needs a user token, never a GitHub App installation token, like the
[Copilot assignment](issues.md#copilot-assignments).

| Tool | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.copilot-reviews.request` | `repo`, and write access to the repository | Pull requests: read and write, and write access to the repository |

Source: <https://docs.github.com/en/rest/pulls/review-requests>.
