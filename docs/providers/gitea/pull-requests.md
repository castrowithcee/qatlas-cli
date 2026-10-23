---
description: >
  Describes the pull request tools of the Gitea and Forgejo providers: reading, changing, closing, and merging, token
  scopes, how each tool is bound to the targets, the differences between the products and versions, and the bounds
  of diffs and lists.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Pull requests

These tools read and change the pull requests of one repository. They hold for both providers unless a line says otherwise;
[Gitea](README.md) describes the shared configuration, targets, pagination, and errors, and
[Forgejo](../forgejo.md) lists what differs. Titles, bodies, branch names, file paths, commit messages, and diffs are
untrusted data. The data class of the tools is `<provider>-pull-request-data`; it holds personal data such as names
and the email addresses in commits.

| Tool | Effect | Provider |
| --- | --- | --- |
| `pull-requests.list` | read | Gitea, Forgejo |
| `pull-requests.get` | read | Gitea, Forgejo |
| `pull-requests.get-by-branches` | read | Gitea, Forgejo |
| `pull-requests.list-pinned` | read | Gitea, Forgejo |
| `pull-requests.check-merged` | read | Gitea, Forgejo |
| `commit-pull-requests.get` | read | Gitea, Forgejo |
| `pull-request-files.list` | read | Gitea, Forgejo |
| `pull-request-commits.list` | read | Gitea, Forgejo |
| `pull-request-diffs.get` | read | Gitea, Forgejo |
| `pull-requests.create` | create | Gitea, Forgejo |
| `pull-requests.update` | update | Gitea, Forgejo |
| `pull-requests.close` | update | Gitea, Forgejo |
| `pull-requests.reopen` | update | Gitea, Forgejo |
| `pull-requests.cancel-auto-merge` | update | Gitea, Forgejo |
| `pull-requests.merge` | update, listed-only | Gitea, Forgejo |
| `pull-request-branches.update` | update, listed-only | Gitea, Forgejo |

## Scopes

Presumably `read:repository` for the read tools and `write:repository` for the changing tools (the API serves pull requests in the repository route group); none was
checked against a live instance. A missing scope shows as `not-found`.

## Target binding

- `repository` is the base repository and is checked against the targets before the token is read.
- A pull request is bound by the canonical `full_name` of its base repository. Every tool that takes a `number`
  first reads the pull request and refuses one whose base lies outside the targets; `check-merged`, files, commits,
  and diffs therefore send two requests. `get-by-branches` and `commit-pull-requests.get` check the pull request
  they receive. Lists check every row and drop those outside the targets.
- The head repository of a fork, and the base repository, show identifiers only (ID, names, owner ID and login), so
  a repository outside the targets is not shown beyond them.
- Branch names and commit IDs are validated before they become a path segment (no control characters, no `..`, no
  backslash; a commit ID is 7 to 64 hexadecimal digits) and are escaped. `head` of `get-by-branches` takes
  `OWNER:BRANCH` for a branch of a fork.

## Differences between the products

| Subject | Forgejo | Gitea |
| --- | --- | --- |
| Branch filter of `list` | `base` and `head`, from Forgejo 17 | `base_branch` |
| Pull request member | `flow` | `content_version` |
| `create` | no reviewers | `reviewers`, `team_reviewers` |
| `update` | no version check | `content_version` of the pull request read, refused when title or body changed since |
| Merge body | `Do`, `MergeCommitID`, `MergeMessageField`, `MergeTitleField` | `do`, `merge_commit_id`, `merge_message_field`, `merge_title_field` |

A member or filter of the other product is refused before the token is read. On Forgejo, `base` or `head` on an instance
older than 17 is refused as not available; the earlier versions' API descriptions do not document them, and the
instance would otherwise ignore them and answer an unfiltered list.

## Bounds

- `pull-request-diffs.get` returns the `diff` or `patch` text cut at 256 KiB with `truncated`; `binary` includes
  binary changes.
- `pull-request-files.list` returns the files with their counts and addresses. The API sends no patch with a file,
  so there is no patch excerpt; the diff tool reads the changes.
- `pull-request-commits.list` leaves out the raw payload and signature of a commit verification, which are large
  signed blobs; `verified`, `reason`, and `signer` remain. Lists inside one object (files of a commit, labels,
  assignees) show at most 100 entries; `verification` and `files` of the commit list may be set to `false` to save
  the instance work.
- `list-pinned` shows the first 50 pinned pull requests.
- `check-merged` reports the instance's 204 as `true` and 404 as `false`, after the pull request itself was read, so
  a missing pull request is an error, not `false`.
- Pull request search is not offered; use `issues.search` of the issue tools.

## Changes and merge

- `create` binds the base repository by the path. `head` is a branch of the repository or `OWNER:BRANCH` of a fork,
  whose repository is not checked against the targets; the pull request shows it by identifiers only. `update`,
  `close`, `reopen`, and `cancel-auto-merge` read the pull request first (target binding above) and send one request
  that is never repeated. `update` retargets `base`, replaces assignees and labels, and sets `milestone` 0 to remove
  it; the state is changed only by `close` and `reopen`, the deprecated `assignee` is not offered.
- `close` and `reopen` return the pull request unchanged with `changed` false, without a request, when it already
  has the state. A merged pull request is not reopened. `cancel-auto-merge` answers `not-found` when nothing is
  scheduled.
- `merge` and `pull-request-branches.update` are offered only when the connection's `tools` list names them and are
  in no setup profile. `merge` requires `sha`, the complete head commit, and `do`. Qatlas reads the pull request:
  merged with exactly this head is a success without a request (`changed` false); merged with another head, or an
  open one whose head is not `sha`, is `invalid-request` naming both commits; otherwise one merge request carries
  `head_commit_id`, and the pull request is read once more for the result. A 409 reads it again and names the
  expected and current head; 405 (protection, missing review, failing check, conflict), 423 (archived), and Forgejo
  413 (quota) are `provider-error` with a reason and without provider text; Gitea 403 shows as `not-found` like every
  403. A timeout or 5xx is an open outcome and is not repeated.
- `do` is one of `merge`, `rebase`, `rebase-merge`, `squash`, `fast-forward-only`, `manually-merged`; the last
  requires `merge_commit_id` and no other style takes it. `merge_when_checks_succeed` schedules the merge
  (`auto_merge_scheduled`), `delete_branch_after_merge` deletes the head branch. `force_merge` is not offered since
  it bypasses protections.
- `pull-request-branches.update` merges or rebases (`style`) the base into the head branch. The instance takes no
  head condition, so `expected_head_sha` is compared with the head read just before the request; a push in that
  window is not detected.
