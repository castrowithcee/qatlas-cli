---
description: >
  Describes the GitHub pull request tools: pull request reads and changes, the listed-only merge and its conflicts, pull request search, Copilot reviews, and the check runs, annotations, and check suites of a commit.
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
[Pull request reviews and comments](pull-request-reviews.md#pull-request-reviews-and-comments). The six reads are offered by the not-recommended setup profile `pull-requests`; the not-recommended
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
