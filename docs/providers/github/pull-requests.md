---
description: >
  Describes the GitHub pull request tools: pull request reads and changes, the listed-only merge and its conflicts, conversation comments, reviews with bundled line comments, replies, review threads, requested reviewers, the listed-only approve, pull request search, Copilot reviews, and the check runs, annotations, and check suites of a commit.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-11-05
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
[Pull request reviews and comments](#pull-request-reviews-and-comments). The seven reads are offered by the not-recommended setup profile `pull-requests`; the not-recommended
profile `pull-requests-operator` adds the changes, described under [Changes](#changes) below, but never the
merge, described under [Merge and conflicts](#merge-and-conflicts): it is offered only where a connection's
`tools` list names `github.pull-requests.merge`, because a merge into the wrong repository's default branch is
the costliest mistake there. Neither profile is offered by the recommended profile `read`. Every route lies
below the chosen repository; no tool accepts an owner or a free REST path.

| Tool | Does |
| --- | --- |
| `github.pull-requests.list` | lists pull requests |
| `github.pull-requests.get` | reads one pull request with its body, merge state, and reviewers |
| `github.pull-request-files.list` | lists its changed files with a bounded patch excerpt |
| `github.pull-request-commits.list` | lists its commits with their git commit data |
| `github.pull-request-diffs.get` | reads its unified diff, cut to at most 65536 bytes from its start |
| `github.pull-request-checks.list` | reads the check runs and the combined commit status at its head commit |
| `github.pull-request-checks.wait` | waits until those checks have finished |

```yaml
connections:
  pull-reader:
    service: github
    credential: github-reader
    target: repos/octo-org/example
    tools: [github.pull-requests.list, github.pull-requests.get, github.pull-request-files.list,
      github.pull-request-commits.list, github.pull-request-diffs.get, github.pull-request-checks.list]
```

`github.pull-requests.list` filters by `state`, `base`, and `head` (a branch name, or `LOGIN:branch` for a fork)
and orders by `sort` and `direction`; without them it keeps GitHub's own order. `sort` and `direction` are
part of the cursor binding, so a continuation never pages in another order. Title, body, branch names, and
commit messages are untrusted data, and a list entry carries its body cut, with `body_truncated`.
`github.pull-requests.get` reports `mergeable` and `rebaseable` as absent while GitHub is still computing them.

The pull request tools report the object behind a flat member as described under
[result objects](README.md#result-objects). The repositories of head and base are reported in full only where the
connection's targets admit them; a head from a fork outside the targets keeps only `id`, `node_id`, and
`full_name`. The repository's `temp_clone_token` is an access value and is never reported. Left out, with the
reason:

- `_links`, also of reviews and line comments: "`_links` repeats the addresses of the `*_url` members as `href`
  objects; those addresses are reported once, in `links`."
- Rendered body variants: "Rendered variants repeat the text field in another format and add no content."
- Accounts: "An account inside another object carries the fields GitHub's own REST answers give it; the full
  profile is read with the account tools."

`github.pull-request-files.list` reads the changed files with a patch excerpt of at most 4096 bytes, cut at a valid
character boundary, with `patch_truncated` when GitHub's patch held more; a file GitHub sends no patch for, such
as a binary file, carries none. `github.pull-request-commits.list` reports a commit's author as the linked
GitHub login when GitHub reports one, otherwise the commit's author name; the message is untrusted data. The git
commit's message, verification payload, and signature are cut at 4096 characters, each with a marker.
`github.pull-request-diffs.get` reads the unified diff through GitHub's diff media type, from its start, up to
the hard limit of 65536 bytes; `truncated` says when the diff held more. None of the three is ever read
unbounded.

`github.pull-request-checks.list` reads the pull request to resolve its head commit, then the check runs (the
GitHub Checks API, which Actions and most third-party CI systems report through) and the combined legacy commit
status (the older Status API) at that commit, and merges them into one compact list; a legacy status is mapped
into the same vocabulary, with `pending` becoming `in_progress` without a conclusion. The check runs and the
statuses are also reported in full, beside the merged list. `overall` summarises the merged list:
`pending` while a check still runs, `failure` once a finished check did not succeed, `success` when every one
did, and `none` when nothing reported a check for the commit. `truncated` says when GitHub held more than the
100 check runs or 100 statuses read per call.

The filters `check_name`, `status`, `filter`, and `app_id` act on the check runs only, in the one check run
request; the commit statuses have no filter and are always read whole. With a filter set, `overall` covers the
filtered check runs and every status, so a filter that leaves out a failing check run also leaves out its
failure.

### Waiting for the checks

`github.pull-request-checks.wait` reads what `github.pull-request-checks.list` reads, with its rights and risk and
without its filters, until the checks at one head commit have finished. `result` says how it ended:

| `result` | When |
| --- | --- |
| `pass` | every check finished and none failed; `success`, `neutral`, and `skipped` count as passed |
| `fail` | a finished check did not succeed, whatever its other conclusion, or a status is `failure` or `error`; it ends the wait at once, while other checks still run |
| `none` | three polls in a row found no check, since a check appears some seconds after a push |
| `timeout` | the limit passed |
| `pending` | only the broker: the 50 seconds of one call passed |
| `head-changed` | the pull request moved to another head commit, reported as `current_head_sha`; no check of either run is reported |

A check counts only once it reports its completion. The awaited commit is `head_sha`, a full SHA, otherwise the
head at the start of the call; the answer names it, and an agent continues after `pending` with it, so a push
between two calls ends as `head-changed` instead of with the old run. A poll that fails for a moment
(connection, timeout, rate limit, server error) is repeated, up to five failures in a row, which end the call
with the last error; a refused token or a missing pull request ends it at once. Names and links are untrusted
data and never fetched. `truncated` says when GitHub held more than the 100 check runs or statuses read, and
`pass` then covers only those.

One loop serves two forms, polling every 20 seconds. Through MCP a call returns after at most 50 seconds, below
the 60 seconds of an invoke, and an agent without a shell calls it again. With a shell, `qconnect wait
github.pull-request-checks.wait --arg number=42` waits for the end in one command, up to its `--timeout`, and
names the result in its exit code; the description of the tool names it as the preferred way.

### Check runs, annotations, and check suites

| Tool | Does |
| --- | --- |
| `github.check-runs.list` | lists the check runs of a branch, tag, or commit |
| `github.check-runs.get` | reads one check run |
| `github.check-annotations.list` | lists the annotations of one check run |
| `github.check-suites.list` | lists the check suites of a branch, tag, or commit |
| `github.check-suite-runs.list` | lists the check runs of one check suite |

The not-recommended profile `checks` offers the five reads; the recommended profile `read` does not. Unlike
`github.pull-request-checks.list`, they read the Checks API alone, name no pull request, and page with `limit` and
`cursor`. `github.check-runs.list` and `github.check-suite-runs.list` return only the latest check run of each
name unless `filter` is `all`. The output, app, pull requests, and deployment of a check run, and the app, head
commit, and pull requests of a check suite, are reported as GitHub describes them; names, titles, output
texts, annotations, and addresses are untrusted data, cut with a marker, and no address is fetched.

GitHub scopes a check run or check suite identifier below the repository of the request, but its answer is the
only place that names the repository. `github.check-runs.get` and `github.check-annotations.list` therefore
require the REST address of the check run to name the chosen repository, and `github.check-suite-runs.list` the
full name of the check suite; the annotations and the check runs are requested only after that read. An answer
that names another repository, or none, ends as `invalid-provider-response` without any of its values.

A refused read ends as `permission`, naming the rights of the [setup](setup.md#pull-requests); GitHub's 422 ends
as `invalid-request`. Creating, changing, or rerequesting a check run, rerequesting a check suite, and a check
suite's settings are open to a GitHub App only; GitHub refuses them to the token of a connection, so no tool
offers them.

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

The token rights are listed under [Pull requests](setup.md#pull-requests).

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

## Pull request search

`github.pull-requests.search` searches pull requests, never plain issues: it forces `is:pr` ahead of `terms`, next
to the target qualifiers described under [Search](search.md). It takes the filters of
[issue search](issues.md#issue-search) as arguments of their own, never part of `terms`, and binds them to the
cursor. With `advanced_search` or `search_type` set, GitHub reads the query differently, so every match is also
checked against the connection's targets, and an answer outside them is refused as an invalid response. With
`text_matches` set, GitHub reports where the terms matched; those fragments are untrusted data, cut and never
fetched. A match reports the members of an issue search match, and the pull request's merge time and its diff and
patch addresses in `links`.

## Copilot reviews

`github.copilot-reviews.request` requests Copilot as reviewer of one pull request of the bound repository and
answers with the pull request as `github.pull-requests.get` reports it. It is idempotent: requesting again
leaves Copilot requested. It does not wait for what Copilot does afterwards. GitHub answers a missing write
access with `permission`, and a request it rejects with an error that names the license or the repository
setting as the likely reason.

It covers the official tool `request_copilot_review` of the GitHub MCP server (MIT, `pkg/github/copilot.go` at
commit 85598ba). It needs a user token, never a GitHub App installation token, like the
[Copilot assignment](issues.md#copilot-assignments); the rights are listed under [Pull requests](setup.md#pull-requests).

Source: <https://docs.github.com/en/rest/pulls/review-requests>.
