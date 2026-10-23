---
description: >
  Describes the GitHub code tools: reading repository contents, Git trees, blame, commits, branches, and tags, the repository writes of branches and file contents including multi-file commits, and the code and commit search.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# GitHub code

The code tools of the [GitHub provider](README.md). Every tool takes `repository` as described under
[the target of a call](README.md#the-target-of-a-call); a change needs the confirmation described under
[confirmed changes](README.md#confirmed-changes).

## Repository contents, tree, blame, commits, branches, and tags

| Tool | Does |
| --- | --- |
| `github.contents.get` | reads one file or one directory listing of a repository at a ref |
| `github.trees.get` | reads the Git tree of a repository at a ref, optionally every entry below every directory |
| `github.blame.get` | reads the commit behind each line of one file over a bounded line range |
| `github.commits.list` | lists the commits of a repository, filtered by ref, path, author, committer, and a commit date range |
| `github.commits.get` | reads one commit by branch, tag, or commit SHA, with its stats and its changed files |
| `github.branches.list` | lists the branches of a repository with their protection status and their latest commit SHA, optionally only protected or only unprotected ones |
| `github.tags.list` | lists the tags of a repository with the commit SHA each points at |
| `github.tags.get` | reads one tag, lightweight or annotated, resolved through the Git refs and Git tags APIs |

None of them writes, diffs two arbitrary refs, or downloads an archive.

`path` is a repository-relative path with no leading or trailing `/` and no empty, `.`, or `..` segment, in
valid UTF-8 without a control character; every segment is escaped on its own before it reaches GitHub, so it
can only ever address the path itself. `github.contents.get` leaves it out, or takes `""`, for the
repository root, a directory listing; `github.blame.get` requires it. `ref` is a branch, a tag, or a commit
SHA; `github.contents.get` and `github.blame.get` read the default branch, or its tip, when it is left out,
and `github.trees.get` requires it.

`github.contents.get` on a directory is bounded to 1000 entries, with `entries_truncated` visible past that.
A text file within 256 KiB is returned in full as `content`; past that bound the content is cut from its
start to 256 KiB with `truncated: true`, `bytes` naming what was kept and `size` the file's real size. A
binary file, detected by a NUL byte or invalid UTF-8 once its content is decoded, and a file past GitHub's own
inline content limit of about 1 MiB are metadata only, with `omitted` naming which: `binary` or `too_large`.
A symlink and a submodule are metadata only as well, with `target` naming the symlink's target path or the
commit a submodule points at. The tools of this page report the object behind a flat member as described under
[result objects](README.md#result-objects). Left out, with the reason:

- `download_url` of a file, a directory entry, and a workflow file: "For a private repository GitHub answers
  `download_url` with a temporary token; it grants access without sign-in and is withheld."
- `_links`: "`_links` repeats the addresses of the `*_url` members as `href` objects; those addresses are reported
  once, in `links`."
- Accounts: "An account inside another object carries the fields GitHub's own REST answers give it; the full
  profile is read with the account tools."

`github.trees.get` with `recursive: true` reads every entry below every directory, not only the top level.
GitHub itself may truncate a very large recursive tree, and Qatlas cuts one past 2000 entries on its own;
`truncated` is true whichever applied.

`github.blame.get` takes `start_line` and `end_line`, bounding the line range: 1 and `start_line`+99 when
either is left out, and the range may never exceed 2000 lines. GitHub's own blame carries no file content at
all, only the commit behind each line, so `ranges` never does either; each one is clipped to the requested
range on both sides. `author` is the login when GitHub reports one, the commit author's name otherwise. A ref
GitHub cannot resolve, and a path with no blame at that ref, both answer `not-found`.

`github.commits.list` filters by `ref` (a branch, a tag, or a commit SHA, GitHub's `sha` parameter), `path`
(a repository path commits must touch), `author` (a GitHub login), `committer` (a GitHub login or an email
address, at most 256 characters), and `since`/`until` (a commit date, as
`YYYY-MM-DD` or a UTC time as `YYYY-MM-DDTHH:MM:SSZ`; a date becomes midnight UTC for `since` or the last
second of that day for `until`); `since` must not lie after `until`. It pages by GitHub's Link header since
the plain array route carries no total count, a cursor bound to the repository and every filter. A commit
`message` is untrusted data, cut to its first line and at most 200 characters; `date` is the commit date
`since` and `until` filter by, and `parents` the number of parent commits. An entry carries no patch. The git
commit's message, verification payload, and signature are cut at 4096 characters, each with a marker.

`github.commits.get` reads one commit by `ref` (a branch, a tag, or a commit SHA). Its full `message` is
untrusted data, cut to at most 4096 characters with `message_truncated`; each changed file's `patch` is
untrusted data as well, cut to at most 4096 bytes with `patch_truncated`, and a file GitHub sends no patch for
carries none. GitHub's own route already caps the files it returns at 300 without ever saying whether more
exist, so Qatlas treats a batch that reaches the same bound as cut as well, with `files_truncated` visible. The
`stats` of the commit are covered by `additions`, `deletions`, and `total`.

`github.branches.list` and `github.tags.list` page the same way, by GitHub's Link header with a cursor bound
to the repository; only `github.branches.list` takes a filter, `protected`. A branch's `protection` is reported
only where GitHub sends it with the branch in the list; Qatlas sends no further request for it. A tag's `sha` is
the commit it points at, since GitHub's plain tags list already dereferences an annotated tag to the commit it
tags.

`github.tags.get` takes `tag`, the tag name, and resolves it through the Git refs API rather than the plain
tags list, since only the Git refs and Git tags APIs name a lightweight tag's own object type and an
annotated tag's tagger and message. `sha` is the commit a lightweight tag points at, or an annotated tag's own
tag object SHA; an annotated tag also names the object, usually a commit, its tag object points at. The
annotated tag's `message` is untrusted data, cut to at most 4096 characters with `message_truncated`. The tag
object's own members, its tagger and its verification, exist only for an annotated tag; a lightweight tag
reports its Git ref.

Reading contents, a tree, a file's blame, commits, branches, or tags needs no scope for a public repository,
or the following for a private one:

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| the eight tools above | `repo` | Contents: read |

The terminal editor's setup profile `repository-reader` selects the eight; it is not the recommended profile,
which stays `read`, unchanged.

## Repository writes: branches and file contents

| Tool | Does |
| --- | --- |
| `github.branches.create` | creates one branch from a branch, a tag, or a commit SHA |
| `github.contents.put` | creates a new file, or updates an existing one while `sha` names its current blob |
| `github.contents.delete` | deletes one file with its current blob `sha`; offered only where a connection's `tools` list names it |
| `github.files.push` | writes 1 to 100 files as one commit on an existing branch; offered only where a connection's `tools` list names it |

`github.contents.delete` is listed only because deleting a file the wrong branch relied on cannot be undone;
`github.files.push` is listed only because several files land in one commit that cannot be undone by halves.
All four refuse a `path` below `.github/workflows/`, matched without regard to case, which the
[workflow file tools](actions.md) maintain instead with their own `workflow` token requirement; none of the
four ever answers with a file's content, only its path, its blob SHA, and the commit GitHub made.

`github.branches.create` makes the branch from `from`, or, when `from` is left out, from the repository's
default branch, resolved through the same commits route `github.commits.get` uses, so the branch always starts
at a real commit; the answer names the branch actually used when `from` was left out. A branch name already
taken answers `invalid-request` with a clear message instead of a second attempt, since GitHub refuses to
create it with HTTP 422; `from` naming no commit, branch, or tag GitHub holds answers `not-found`.

`github.contents.put` takes the complete new `content` as UTF-8 text, at most 512 KiB, a `message` of at most
1000 characters, and `branch` (the repository's default branch when left out). `sha` is the blob SHA of the
version to replace, as `github.contents.get` reports it: required to change an existing file and refused for a
new one. `github.contents.delete` requires the `sha` of the version to delete.

A missing or a stale `sha` is not refused before I/O, since only GitHub knows the file's current state on the
given branch; the request reaches GitHub, which answers a conflict or a validation failure, and Qatlas then
reads the file again. Once that read proves the given `sha` is not the file's current one, or that a file
already exists although none was given, the refusal becomes `invalid-request`, naming the file's current SHA,
so the caller does not need a separate `github.contents.get` call first. When the current SHA matches the one
given, the conflict had another cause, such as a branch protection rule, and GitHub's original refusal is
kept unchanged; the same holds when the file cannot be read again at all.

`github.files.push` requires `branch`, with no default branch fallback, since a push several files can never
be undone by halves is the one call where the target branch should always be explicit. It takes 1 to 100
`files` with no path repeated, the combined content of every file bounded as well as each file on its own,
and optionally `expected_head_sha`, a commit SHA the branch is expected to be at: when given and the branch is
at another commit, the call is refused before any blob is created, so a caller that already knows the branch
moved fails cheaply instead of creating git objects that would only be discarded. It reads the branch's
current head and tree through the Git Data API, creates one blob per file and one new tree above the read
tree, creates one commit from that tree with the read head as its only parent, and moves the branch to that
commit with a ref update sent with `force` false, so GitHub refuses it with HTTP 422 the moment the branch is
no longer at the head this call read: nothing is written to the branch, and the refusal becomes
`invalid-request` naming the branch's current head, read again on a best-effort basis, and the next step,
reading the branch again and reapplying the change. This is the same fast-forward guard `expected_head_sha`
only lets a caller trigger earlier; the final ref update enforces it either way. Every step is sent at most
once; a failure while creating a blob, a tree, or a commit leaves the branch exactly as it was, since only the
ref update ever moves it, though the object it was creating may still exist as a loose Git object with no
effect until referenced. The answer names `parent_sha`, the commit the branch was at before the push, and the
paths written in the order given, never any file's content.

Reading contents needs the tokens above for a private repository, and the same tokens after them are what
creating a branch, creating, updating, or deleting a file, and pushing several files as one commit need for
either kind of repository:

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.branches.create`, `github.contents.put`, `github.contents.delete`, `github.files.push` | `repo` | Contents: read and write |

The terminal editor's setup profile `repository-writer` (not recommended) selects the read eight plus
`github.branches.create` and `github.contents.put`; `github.contents.delete` and `github.files.push` are in no
profile, offered only where a connection's `tools` list names them, like
[`github.releases.delete`](releases.md#releases).

## Search

| Tool | Searches |
| --- | --- |
| `github.code.search` | code, without file bodies or fragments |
| `github.commits.search` | commits, optionally sorted by `sort` and `order`, with `text_matches` on request |

Both read GitHub's own search index with `terms`, under the rules described in [Search](README.md#search); they
force the [target qualifiers](README.md#target-scoping). `sort` and `order` are arguments of their own, never part
of `terms`. `github.code.search` shares GitHub's separate, lower
search rate limit, see [errors and rate limits](README.md#errors-and-rate-limits). The repository search is
described with the [repositories](repositories.md#search), the user and organization search with the
[accounts](accounts.md#search).
