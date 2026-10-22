---
description: >
  Describes the GitHub tools for repository contents, trees, blame, commits, branches, and tags, repository writes, repository create, fork, and delete, collaborators, rulesets, custom properties, repository merge and branch settings, releases, security alerts, advisories, and code quality findings, discussions, gists, notification threads with their subscriptions, the account behind a connection's token, organization teams, starred repositories, the repository owner list, and the search of repositories, code, commits, users, and organizations.
type: knowledge
edit: shared
created: 2026-09-23
updated: 2026-10-22
---

# GitHub

The tool groups described here are code, repositories, releases, security, discussions, gists, notifications, and
accounts. Configuration, targets, errors, the shared search rules, confirmed changes, and the cursor contract
are described in [the provider overview](github/README.md); the issues, pull requests, projects, and Actions
groups have their own pages under [github/](github/README.md).

## Owners: repositories

`github.repositories.list` lists the repositories the owner owns (`users/LOGIN` or `orgs/LOGIN`), in name order,
archived ones included. A user's repositories are the ones the user owns, not those the user collaborates on.
What the list shows depends on the targets, see [owner lists](github/README.md#owner-lists). The repositories
need no scope for public ones, `repo` on a classic token for private ones, or access to the repositories on a
fine-grained token.

## Discovery: account, organization teams, and stars

`github.accounts.me` reads the login, display name, account type, and billing plan of the account behind the
connection's token; `github.teams.list` and `github.team-members.list` read the teams of an organization and
the members of one of them; `github.stars.list` reads the account's starred repositories, and
`github.stars.add` and `github.stars.remove` star and unstar one:

```sh
qatlas invoke github.accounts.me --connection discovery
qatlas invoke github.teams.list --connection discovery --arg owner=orgs/octo-org
qatlas invoke github.team-members.list --connection discovery --arg owner=orgs/octo-org --arg team=design
qatlas invoke github.stars.list --connection discovery
echo '{"repository":"octo-org/example"}' | qatlas invoke github.stars.add --connection stars --confirm
```

| Tool | Effect | Idempotency | Does |
| --- | --- | --- | --- |
| `github.accounts.me` | read | safe | reads the account behind the connection's token; no repository, project, or owner argument |
| `github.teams.list` | read | safe | lists the teams of an organization, in batches, with slug, name, description, and privacy |
| `github.team-members.list` | read | safe | lists the members of one team of an organization, in batches, by login |
| `github.stars.list` | read | safe | lists the repositories the account has starred, in batches; no repository or project argument |
| `github.stars.add` | create | idempotent | stars one repository; a repository already starred is left as it is |
| `github.stars.remove` | delete | idempotent | unstars one repository; a repository that is not starred is left as it is |

`github.teams.list` and `github.team-members.list` take `owner` as `orgs/LOGIN`: the organization must be a
target the connection names as an owner itself, exactly as `github.projects.create` requires, since a project
or a repository pattern of the organization is not enough. `owner` may be left out only when the targets name
exactly one owner entry; `users/LOGIN` is refused with `invalid-request`, because teams belong to an
organization, not a user. `github.team-members.list` also takes `team`, the slug `github.teams.list` reports.
Both follow the [cursor contract](github/README.md#cursor-contract), a `github.teams.list` cursor bound to the organization and
a `github.team-members.list` cursor bound to the organization and the team together.

`github.accounts.me` and `github.stars.list` take no `repository`, `project`, or `owner` argument: they read
data of the account behind the token as a whole. A connection whose targets name a repository or a project is
refused with `invalid-request` before a secret is read, because such a connection is scoped to those, and the
account behind its token may belong to a customer other than the one its targets name. A connection without
targets, or whose targets name only owners, is allowed; on such a connection, `github.stars.list` narrows its
answer to the starred repositories of the owners the targets name, when it names any. `github.stars.add` and
`github.stars.remove` take `repository` like every other repository tool and check it against the targets
before a secret is read.

Starring and unstarring are naturally idempotent: GitHub answers both with no content whatever the
repository's previous state. Before sending either, Qatlas reads whether the repository is already starred;
a repository already in the requested state is reported with `changed: false` and nothing further is sent.
Otherwise the change travels once, as every change here does, and the answer names the state afterward.

Reading the account behind the token needs no scope beyond its own identity, classic or fine-grained. The
organization team reads need `read:org` on a classic token, or Members read access of the organization on a
fine-grained one. Reading the account's starred repositories needs no scope for public ones, `repo` as well
for private ones, or Starring read access on a fine-grained token. Starring and unstarring need `public_repo`
on a classic token, `repo` as well for a private repository, or Starring read and write access on a
fine-grained token.

The terminal editor's setup profile `discovery` selects `github.accounts.me`,
`github.teams.list`, `github.team-members.list`, `github.stars.list`, and the seven
[search tools](github/README.md#search); it changes
nothing. The setup profile
`stars` selects `github.stars.list`, `github.stars.add`, and
`github.stars.remove`; every star or unstar needs its own confirmation. Neither is the recommended profile,
which stays `read`, unchanged.


## Search: repositories, code, commits, users, and organizations

`github.repositories.search`, `github.code.search`, `github.commits.search`, `github.users.search`, and
`github.organizations.search` read GitHub's own search index with `terms`, under the rules described in
[Search](github/README.md#search):

```sh
qatlas invoke github.repositories.search --connection discovery --arg terms="qatlas language:go"
qatlas invoke github.code.search --connection discovery --arg terms="handleRequest language:go"
qatlas invoke github.commits.search --connection discovery --arg terms="author:octocat"
qatlas invoke github.users.search --connection discovery --arg terms="location:berlin"
qatlas invoke github.organizations.search --connection discovery --arg terms="octo in:name"
```

| Tool | Searches | Forces | Each entry |
| --- | --- | --- | --- |
| `github.repositories.search` | repositories | the [target qualifiers](github/README.md#target-scoping) | `repository`, `description`, `visibility`, `language`, `stars`, `fork`, `archived`, `updated_at`, `url` |
| `github.code.search` | code, without file bodies or fragments | the [target qualifiers](github/README.md#target-scoping) | `path`, `repository`, `sha`, `url` |
| `github.commits.search` | commits | the [target qualifiers](github/README.md#target-scoping) | `sha`, `message`, `repository`, `author`, `committed_at`, `url` |
| `github.users.search` | personal accounts, never organizations | `type:user`, and, with one organization owner target, `org:LOGIN` of it | `login`, `type`, `url` |
| `github.organizations.search` | organizations, never personal accounts | `type:org` | `login`, `url` |

## Repository contents, tree, blame, commits, branches, and tags

`github.contents.get` reads one file or one directory listing of a repository a connection allows
at a ref; `github.trees.get` reads its Git tree at a ref, optionally every entry below every directory;
`github.blame.get` reads, through GraphQL, the commit that last changed each line of one file over a bounded
line range; `github.commits.list` and `github.commits.get` read its commits, filtered and paged, or one by
branch, tag, or commit SHA with its stats and its changed files; `github.branches.list` and
`github.tags.list` read its branches and its tags, paged; `github.tags.get` reads one tag, lightweight or
annotated, resolved through the Git refs and Git tags APIs. None of the seven writes, diffs two arbitrary
refs, or downloads an archive:

```sh
qatlas invoke github.contents.get --connection code
qatlas invoke github.contents.get --connection code --arg path=README.md
qatlas invoke github.trees.get --connection code --arg ref=main --arg recursive=true
qatlas invoke github.blame.get --connection code --arg path=internal/app.go --arg start_line=1 --arg end_line=100
qatlas invoke github.commits.list --connection code --arg ref=main --arg path=internal/app.go --arg since=2026-01-01
qatlas invoke github.commits.get --connection code --arg ref=ebca79b1db4fcbb136e6094c13e8451428c8a6ab
qatlas invoke github.branches.list --connection code
qatlas invoke github.tags.list --connection code
qatlas invoke github.tags.get --connection code --arg tag=v1.0.0
```

| Tool | Effect | Idempotency | Does |
| --- | --- | --- | --- |
| `github.contents.get` | read | safe | reads one file or one directory listing of a repository at a ref |
| `github.trees.get` | read | safe | reads the Git tree of a repository at a ref, optionally every entry below every directory |
| `github.blame.get` | read | safe | reads the commit behind each line of one file over a bounded line range |
| `github.commits.list` | read | safe | lists the commits of a repository, filtered by ref, path, author, and a commit date range |
| `github.commits.get` | read | safe | reads one commit by branch, tag, or commit SHA, with its stats and its changed files |
| `github.branches.list` | read | safe | lists the branches of a repository with their protection status and their latest commit SHA |
| `github.tags.list` | read | safe | lists the tags of a repository with the commit SHA each points at |
| `github.tags.get` | read | safe | reads one tag, lightweight or annotated |

`path` is a repository-relative path with no leading or trailing `/` and no empty, `.`, or `..` segment, in
valid UTF-8 without a control character; every segment is escaped on its own before it reaches GitHub, so it
can only ever address the path itself. `github.contents.get` leaves it out, or takes `""`, for the
repository root, a directory listing; `github.blame.get` requires it. `ref` is a branch, a tag, or a commit
SHA; `github.contents.get` and `github.blame.get` read the default branch, or its tip, when it is left out,
and `github.trees.get` requires it.

`github.contents.get` on a directory answers `entries`: `name`, `path`, `type` (`file`, `dir`, `symlink`, or
`submodule`), `size`, and `sha`, bounded to 1000 entries with `entries_truncated` visible past that. On a
file it answers `path`, `type`, `size`, and `sha`; a text file within 256 KiB is returned in full as
`content`, with `bytes` its length; past that bound the content is cut from its start to 256 KiB with
`truncated: true`, `bytes` naming what was kept and `size` the file's real size. A binary file, detected by a
NUL byte or invalid UTF-8 once its content is decoded, and a file past GitHub's own inline content limit of
about 1 MiB are metadata only, with `omitted` naming which: `binary` or `too_large`. A symlink and a
submodule are metadata only as well, with `target` naming the symlink's target path or the commit a
submodule points at.

`github.trees.get` answers `sha`, the tree GitHub read, and `entries`: `path`, `type` (`blob`, `tree`, or
`commit` for a submodule), `sha`, and, for a blob, `size`. `recursive: true` reads every entry below every
directory, not only the top level. GitHub itself may truncate a very large recursive tree, and Qatlas cuts
one past 2000 entries on its own; `truncated` is true whichever applied.

`github.blame.get` takes `start_line` and `end_line`, bounding the line range: 1 and `start_line`+99 when
either is left out, and the range may never exceed 2000 lines. GitHub's own blame carries no file content at
all, only the commit behind each line, so `ranges` never does either: each one names `start_line`,
`end_line`, the commit `sha`, `author` (the login when GitHub reports one, the commit author's name
otherwise), and `date`, clipped to the requested range on both sides. A ref GitHub cannot resolve, and a path
with no blame at that ref, both answer `not-found`.

`github.commits.list` filters by `ref` (a branch, a tag, or a commit SHA, GitHub's `sha` parameter), `path`
(a repository path commits must touch), `author` (a GitHub login), and `since`/`until` (a commit date, as
`YYYY-MM-DD` or a UTC time as `YYYY-MM-DDTHH:MM:SSZ`; a date becomes midnight UTC for `since` or the last
second of that day for `until`); `since` must not lie after `until`. It pages by GitHub's Link header since
the plain array route carries no total count, a cursor bound to the repository and every filter. Each entry
answers `sha`, `message` (untrusted data, cut to its first line and at most 200 characters), `author` and
`committer` (a login when GitHub reports one, a name otherwise), `date` (the commit date `since` and `until`
filter by), and `parents`, the number of parent commits; it carries no patch.

`github.commits.get` reads one commit by `ref` (a branch, a tag, or a commit SHA) with its full `message`
(untrusted data, cut to at most 4096 characters with `message_truncated`), `author`, `committer`, `date`,
`parents` (every parent's SHA), `additions`, `deletions`, and `total`, and `files`: `path`, `status`,
`additions`, `deletions`, `changes`, `previous_filename` for a rename, and `patch` (untrusted data, cut to at
most 4096 bytes with `patch_truncated`); a file GitHub sends no patch for carries none. GitHub's own route
already caps the files it returns at 300 without ever saying whether more exist, so Qatlas treats a batch
that reaches the same bound as cut as well, with `files_truncated` visible.

`github.branches.list` and `github.tags.list` page the same way, by GitHub's Link header with a cursor bound
to the repository; neither takes a filter. Each branch answers `name`, `protected`, and `sha`, its latest
commit; each tag answers `name` and `sha`, the commit it points at, since GitHub's plain tags list already
dereferences an annotated tag to the commit it tags.

`github.tags.get` takes `tag`, the tag name, and resolves it through the Git refs API rather than the plain
tags list, since only the Git refs and Git tags APIs name a lightweight tag's own object type and an
annotated tag's tagger and message. It answers `name`, `type` (`lightweight` or `annotated`), and `sha`: the
commit a lightweight tag points at, or an annotated tag's own tag object SHA. An annotated tag also answers
`target_sha` and `target_type` (the object, usually a commit, the tag object points at), `tagger`,
`tagged_at`, and `message` (untrusted data, cut to at most 4096 characters with `message_truncated`).

Reading contents, a tree, a file's blame, commits, branches, or tags needs no scope for a public repository,
or the following for a private one:

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.contents.get`, `github.trees.get`, `github.blame.get`, `github.commits.list`, `github.commits.get`, `github.branches.list`, `github.tags.list`, `github.tags.get` | `repo` | Contents: read |

The terminal editor's setup profile `repository-reader` selects the eight; it is not the
recommended profile, which stays `read`, unchanged.

## Repository writes: branches and file contents

`github.branches.create` makes one new branch of a repository a connection allows from `from`, a
branch, a tag, or a commit SHA, or, when `from` is left out, the repository's default branch, resolved
through the same commits route `github.commits.get` uses, so the branch always starts at a real commit;
`github.contents.put` creates a new file, or, while `sha` names its
current blob, updates an existing one; `github.contents.delete` deletes one file with its current blob `sha`
and is offered only where a connection's `tools` list names it, since deleting a file the wrong branch relied
on cannot be undone. `github.files.push` writes several files as one commit through the Git Data API and is
offered only where a connection's `tools` list names it, since several files land in one commit that cannot
be undone by halves. All four refuse a `path` below `.github/workflows/`, matched without regard to case,
which the workflow file tools maintain instead with their own `workflow` token requirement; none of the four
ever answers with a file's content, only its path, its blob SHA, and the commit GitHub made:

```sh
qatlas invoke github.branches.create --connection code --arg name=feature/login --arg from=main --confirm
qatlas invoke github.contents.put --connection code --arg path=docs/notes.md --arg content="# Notes" \
  --arg message="docs: add notes" --confirm
qatlas invoke github.contents.delete --connection code --arg path=docs/notes.md \
  --arg sha=3d21ec53a331a6f037a91c368710b99387d012c1 --arg message="docs: remove notes" --confirm
qatlas invoke github.files.push --connection code --arg branch=main --arg message="docs: add two pages" \
  --arg 'files=[{"path":"docs/a.md","content":"# A"},{"path":"docs/b.md","content":"# B"}]' --confirm
```

| Tool | Effect | Idempotency | Does |
| --- | --- | --- | --- |
| `github.branches.create` | create | non-idempotent | creates one branch from a branch, a tag, or a commit SHA |
| `github.contents.put` | update | non-idempotent | creates a new file, or updates an existing one while sha names its current blob |
| `github.contents.delete` | delete | unknown | deletes one file with its current blob sha; offered only where a connection's tools list names it |
| `github.files.push` | create | non-idempotent | writes 1 to 100 files as one commit on an existing branch; offered only where a connection's tools list names it |

`github.branches.create` answers `name`, `ref` (`refs/heads/` plus `name`), `sha` (the commit `from`, or the
default branch, resolved to), and `from` (the branch actually used, when it was left out). A branch name
already taken answers `invalid-request` with a clear message instead of a second attempt, since GitHub
refuses to create it with HTTP 422; `from` naming no commit, branch, or tag GitHub holds answers `not-found`.

`github.contents.put` takes `path`, `content` (the complete new content as UTF-8 text, at most 512 KiB),
`message` (a commit message, at most 1000 characters), `sha` (the blob SHA of the version to replace,
required to change an existing file and refused for a new one, as `github.contents.get` reports it), and
`branch` (the repository's default branch when left out). It answers `path`, `sha` (the file's blob SHA
after the change), `branch`, `commit_sha`, and `commit_url`. `github.contents.delete` takes `path`, the
required `sha` of the version to delete, `message`, and `branch`, and answers `path`, `branch`, `deleted`,
`commit_sha`, and `commit_url`.

A missing or a stale `sha` is not refused before I/O, since only GitHub knows the file's current state on the
given branch; the request reaches GitHub, which answers a conflict or a validation failure, and Qatlas then
reads the file again. Once that read proves the given `sha` is not the file's current one, or that a file
already exists although none was given, the refusal becomes `invalid-request`, naming the file's current SHA,
so the caller does not need a separate `github.contents.get` call first. When the current SHA matches the one
given, the conflict had another cause, such as a branch protection rule, and GitHub's original refusal is
kept unchanged; the same holds when the file cannot be read again at all.

`github.files.push` takes `branch` (required, no default branch fallback, since a push several files can
never be undone by halves is the one call where the target branch should always be explicit), `message`, and
`files` (1 to 100 entries of `path` and `content`, no path repeated, the combined content of every file
bounded as well as each file on its own), and optionally `expected_head_sha`, a commit SHA the branch is
expected to be at: when given and the branch is at another commit, the call is refused before any blob is
created, so a caller that already knows the branch moved fails cheaply instead of creating git objects that
would only be discarded. It reads the branch's current head and tree through the Git Data API, creates one
blob per file and one new tree above the read tree, creates one commit from that tree with the read head as
its only parent, and moves the branch to that commit with a ref update sent with `force` false, so GitHub
refuses it with HTTP 422 the moment the branch is no longer at the head this call read: nothing is written to
the branch, and the refusal becomes `invalid-request` naming the branch's current head, read again on a
best-effort basis, and the next step, reading the branch again and reapplying the change. This is the same
fast-forward guard `expected_head_sha` only lets a caller trigger earlier; the final ref update enforces it
either way. Every step is sent at most once; a failure while creating a blob, a tree, or a commit leaves the
branch exactly as it was, since only the ref update ever moves it, though the object it was creating may
still exist as a loose Git object with no effect until referenced. It answers `branch`, `commit_sha`,
`commit_url`, `parent_sha` (the commit the branch was at before the push), and `paths` (the paths written, in
the order given), never any file's content.

Reading contents needs no scope for a public repository, or the following for a private one, and the same
tokens after them are what creating a branch, creating, updating, or deleting a file, and pushing several
files as one commit need for either kind of repository:

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.branches.create`, `github.contents.put`, `github.contents.delete`, `github.files.push` | `repo` | Contents: read and write |

The terminal editor's setup profile `repository-writer` (not recommended) selects
the read eight plus `github.branches.create` and `github.contents.put`; `github.contents.delete` and
`github.files.push` are in no profile, offered only where a connection's `tools` list names them, like
`github.releases.delete`.

## Repository lifecycle and collaborators

`github.repositories.create` makes one new repository under the token's own account, or, with `owner`, under
an organization a connection names as an owner target; private by default. `github.repositories.
fork` forks a repository a connection allows into the token's own account or, with `organization`,
an organization the connection names as an owner target; GitHub answers this asynchronously, so a large
repository may still be importing once the call returns. Creating or forking under the token's own account,
rather than an organization, reads the account behind the token as a whole, the same account-wide guard the
account and star tools apply: it is refused when the connection's targets name a repository or a project,
since the account behind the token may belong to a customer other than the one those targets name.
`github.repositories.delete` deletes one repository permanently and is offered only where a connection's
`tools` list names it; `confirm_name` must repeat the repository as `owner/name` exactly, checked before any
credential is resolved, since this cannot be undone. `github.collaborators.list` reads the collaborators of a
repository with their login, account type, role, and permissions, filtered by `affiliation` and `permission`,
paged; it never answers an email address or another personal detail GitHub's collaborator object may carry:

```sh
qatlas invoke github.repositories.create --connection org --arg name=example --confirm
qatlas invoke github.repositories.create --connection org --arg owner=orgs/octo-org --arg name=example \
  --arg private=false --confirm
qatlas invoke github.repositories.fork --connection code --confirm
qatlas invoke github.repositories.fork --connection code --arg organization=orgs/octo-fork-org --confirm
qatlas invoke github.repositories.delete --connection code --arg confirm_name=octo-org/example --confirm
qatlas invoke github.collaborators.list --connection code --arg affiliation=outside
```

| Tool | Effect | Idempotency | Does |
| --- | --- | --- | --- |
| `github.repositories.create` | create | non-idempotent | creates one repository under the token's own account or an organization |
| `github.repositories.fork` | create | idempotent | forks a repository into the token's own account or an organization |
| `github.repositories.delete` | delete | unknown | deletes one repository permanently; offered only where a connection's tools list names it |
| `github.collaborators.list` | read | safe | lists the collaborators of a repository with their role and permissions |

`github.repositories.create` takes `owner` (an organization as `orgs/LOGIN`; a user login is refused, since
GitHub creates a repository only under the token's own account or an organization), `name` (required, 1 to
100 characters), `description` (at most 350 characters), `private` (true by default), and `auto_init` (false
by default; true creates an initial commit with a README). Left out, `owner` defaults to the connection's one
organization owner target, when it names exactly one; otherwise the repository is created under the token's
own account, subject to the account-wide guard above. It answers `repository` (`OWNER/REPO`), `owner` (the
account it actually landed under, as `users/LOGIN` or `orgs/LOGIN`), `private`, `default_branch`, and `url`. A
name already taken under the destination account answers `invalid-request` with a clear message instead of a
second attempt, since GitHub refuses it with HTTP 422.

`github.repositories.fork` takes `organization` (as `orgs/LOGIN`, the same restriction and target check as
`owner` above; no default, unlike create, since a fork always names a specific source repository and never
falls back to a target it merely happens to allow), `name` (renames the fork; the source repository's name
when left out), and `default_branch_only` (false by default; true forks only the default branch). It answers
`fork` (the new repository as `OWNER/REPO`), `private`, `url`, and `accepted` (true once GitHub queued the
fork). Forking a repository already forked into the same account does not create a second fork, so the tool
is idempotent.

`github.repositories.delete` takes only `confirm_name`, the repository's `owner/name` repeated exactly; a
mismatch is refused before any credential is resolved. It answers `deleted`.

`github.collaborators.list` takes `affiliation` (`outside`, `direct`, or `all`; `all` when omitted),
`permission` (`pull`, `triage`, `push`, `maintain`, or `admin`; every permission level when omitted), and the
paging arguments, a cursor bound to the repository and its filters. Each collaborator answers `login`,
`type`, `role_name`, and `permissions` (the booleans `pull`, `triage`, `push`, `maintain`, `admin`); no other
field GitHub's collaborator object may carry, such as an email address, ever reaches the answer.

Creating or forking a repository needs `repo` on a classic token, or Administration: read and write on a
fine-grained token; deleting one, listed-only, needs `delete_repo` as well on a classic token. Listing
collaborators needs `repo` on a classic token, or Metadata: read on a fine-grained token:

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.repositories.create`, `github.repositories.fork` | `repo` | Administration: read and write |
| `github.repositories.delete` | `repo` and `delete_repo` | Administration: read and write |
| `github.collaborators.list` | `repo` | Metadata: read |

The terminal editor's setup profile `repository-admin-reader` (not recommended) selects
`github.collaborators.list`, `github.rulesets.list`, `github.rulesets.get`, and `github.custom-properties.get`;
it is not the recommended profile, which stays `read`, unchanged.

## Rulesets: repository and organization governance

`github.rulesets.list` and `github.rulesets.get` read the rulesets of a repository or, with `organization`
instead of `repository`, an organization a connection allows. `github.rulesets.create`,
`github.rulesets.update`, and `github.rulesets.delete` change them and are offered only where a connection's
`tools` list names them, since a ruleset governs what a repository or an organization allows at all:

```sh
qatlas invoke github.rulesets.list --connection code --arg repository=octo-org/example
qatlas invoke github.rulesets.list --connection org --arg organization=orgs/octo-org
qatlas invoke github.rulesets.get --connection code --arg repository=octo-org/example --arg id=1420
echo '{"repository":"octo-org/example","name":"protect-main","target":"branch","enforcement":"active",
  "conditions":{"ref_name":{"include":["refs/heads/main"],"exclude":[]}},
  "rules":[{"type":"non_fast_forward"}]}' |
  qatlas invoke github.rulesets.create --connection governance --confirm
qatlas invoke github.rulesets.delete --connection governance --arg repository=octo-org/example --arg id=1420 \
  --confirm
```

| Tool | Effect | Idempotency | Confirmation | Does |
| --- | --- | --- | --- | --- |
| `github.rulesets.list` | read | safe | none | lists the rulesets of a repository or an organization, compactly |
| `github.rulesets.get` | read | safe | none | reads one ruleset with its conditions, rules, and bypass actors |
| `github.rulesets.create` | create | non-idempotent | required | creates one new ruleset; offered only where a connection's tools list names it |
| `github.rulesets.update` | update | idempotent | required | replaces one ruleset as a whole; offered only where a connection's tools list names it |
| `github.rulesets.delete` | delete | unknown | required | deletes one ruleset permanently; offered only where a connection's tools list names it |

Every call takes exactly one of `repository` (as `OWNER/REPO`, checked against the connection's repository
targets like every other repository tool) or `organization` (as `orgs/LOGIN`, checked against its owner
targets like `github.teams.list`); giving both or neither is an invalid request, checked before a credential
is resolved, so a call never falls back to a target it did not name. A ruleset identifier from one target is
unknown under another: `github.rulesets.get`, `.update`, and `.delete` answer `not-found` for an `id` GitHub
does not hold under the given repository or organization, whichever GitHub does not distinguish from one it
merely does not show to this token.

`github.rulesets.list` also takes `includes_parents` (true by default for a repository, also listing
rulesets it inherits from its organization; ignored for an organization, which has none to inherit) and the
paging arguments, a cursor bound to the target and `includes_parents`. It answers `rulesets`, each with `id`,
`name`, `target` (`branch`, `tag`, `push`, or `repository`), `enforcement`, and `source` (the repository or
organization it was read from).

`github.rulesets.create` and `github.rulesets.update` take a ruleset definition: `name` (1 to 100
characters), `target` (`branch`, `tag`, `push`, or `repository`, the last selecting repositories instead of
refs for an organization ruleset), `enforcement` (`disabled`, `active`, or `evaluate`), and, all optional,
`conditions` (an object, such as `ref_name` include and exclude, or, for target `repository`,
`repository_name`), `rules` (an array of at most 200 `{type, parameters}` objects, GitHub's own rule types),
and `bypass_actors` (an array of at most 100 objects, each with `actor_type` and, depending on it, `actor_id`
and `bypass_mode`). Every field is GitHub's own shape, passed through as given and read back the same way,
untrusted data; only the top-level required fields, the enums, and the array and byte-size bounds above are
checked before a credential is resolved. `github.rulesets.update` sends the whole definition in one `PUT`
that replaces the ruleset as a whole and is idempotent as long as nothing else changes the ruleset between
two identical calls; `github.rulesets.create` is not, since a repeated call creates a second ruleset.
`github.rulesets.get`, `.create`, and `.update` answer `id`, `name`, `target`, `enforcement`, `conditions`,
`rules`, and `bypass_actors`.

Reading the rulesets of a repository needs `repo` on a classic token, or Administration: read on a
fine-grained token; changing them, listed-only, needs Administration: read and write on a fine-grained token
instead. Reading or changing the rulesets of an organization needs `admin:org` on a classic token; the
fine-grained equivalent is, as far as GitHub documents it, a single Administration permission of the
organization, read or read and write:

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.rulesets.list`, `github.rulesets.get` (repository) | `repo` | Administration: read |
| `github.rulesets.create`, `github.rulesets.update`, `github.rulesets.delete` (repository) | `repo` | Administration: read and write |
| `github.rulesets.list`, `github.rulesets.get` (organization) | `admin:org` | Administration read (organization), as far as GitHub documents it |
| `github.rulesets.create`, `github.rulesets.update`, `github.rulesets.delete` (organization) | `admin:org` | Administration read and write (organization), as far as GitHub documents it |

## Custom properties

`github.custom-properties.get` reads the custom property values of a repository or, with `organization`
instead of `repository`, the custom property definitions (schema) an organization declares; exactly one of
the two is required, the same exclusive selection as [Rulesets](#rulesets-repository-and-organization-governance).
It never reads the values GitHub holds for the repositories of an organization: that GitHub route names
repositories by a bare list of names the caller supplies, and binding that list to the connection's own
repository targets would need a second, independent allow-list check this tool does not perform, so the
narrower reading is a schema read only. `github.custom-properties.set` changes them and is offered only where
a connection's `tools` list names it, since a custom property can gate what a ruleset condition matches: for
a repository it takes `properties` and sets or removes property values, a `value` of `null` removing one; for
an organization it takes `definitions` and creates or replaces property definitions, the smaller scope that
already covers the official custom properties write for a single repository, since that is offered through
the repository target above; only one of `properties` or `definitions` may be given, matching the exclusive
target:

```sh
qatlas invoke github.custom-properties.get --connection code --arg repository=octo-org/example
qatlas invoke github.custom-properties.get --connection org --arg organization=orgs/octo-org
echo '{"repository":"octo-org/example","properties":[{"property_name":"team","value":"atlas"},
  {"property_name":"cost_center","value":null}]}' |
  qatlas invoke github.custom-properties.set --connection governance --confirm
echo '{"organization":"orgs/octo-org","definitions":[{"property_name":"team","value_type":"single_select",
  "allowed_values":["atlas","hydra"]}]}' |
  qatlas invoke github.custom-properties.set --connection governance --confirm
```

| Tool | Effect | Idempotency | Confirmation | Does |
| --- | --- | --- | --- | --- |
| `github.custom-properties.get` | read | safe | none | reads the property values of a repository or the property schema of an organization |
| `github.custom-properties.set` | update | idempotent | required | sets or removes property values, or creates or replaces property definitions; offered only where a connection's tools list names it |

Every call takes exactly one of `repository` (as `OWNER/REPO`, checked against the connection's repository
targets like every other repository tool) or `organization` (as `orgs/LOGIN`, checked against its owner
targets like `github.rulesets.list`); giving both or neither is an invalid request, checked before a
credential is resolved.

`github.custom-properties.get` answers `properties` (present only for a repository call: each `property_name`
and `value`, a string, an array of strings for a multi-select property, or `null` while unset) or `schema`
(present only for an organization call: each `property_name`, `value_type` (`string`, `single_select`,
`multi_select`, or `true_false`), `required`, `default_value`, `description`, `allowed_values`, and
`values_editable_by` (`org_actors` or `org_and_repo_actors`)).

`github.custom-properties.set` takes `properties` (required with `repository`, refused with `organization`):
an array of at most 100 `{property_name, value}` entries, each value a string, an array of strings, or `null`
to remove that property. It takes `definitions` instead (required with `organization`, refused with
`repository`): an array of at most 100 property definitions, each with `property_name`, `value_type`, and,
optionally, `required`, `default_value`, `description`, `allowed_values`, and `values_editable_by`; a name the
organization already declares is replaced, so a repeated call is idempotent. GitHub answers a repository set
with no body, so the tool echoes back the values sent; an organization set answers the definitions GitHub
created or replaced. Every field is checked before a credential is resolved: the exclusive target and
argument, the array bounds a JSON schema's `maxItems` cannot enforce by itself, and the size of the whole
request.

Reading the custom property values of a repository needs Custom properties: read on a fine-grained token, or
`repo` on a classic token; changing them, listed-only, needs Custom properties: write instead. Reading or
changing the custom property schema of an organization needs the organization permission Custom properties,
read or read and write, on a fine-grained token, or, as far as GitHub documents it, `admin:org` on a classic
token; these fine-grained mappings are stated cautiously, since Qatlas cannot verify them against GitHub
live:

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.custom-properties.get` (repository) | `repo` | Custom properties: read |
| `github.custom-properties.set` (repository) | `repo` | Custom properties: write |
| `github.custom-properties.get` (organization) | `admin:org` | Custom properties: read (organization), as far as GitHub documents it |
| `github.custom-properties.set` (organization) | `admin:org` | Custom properties: read and write (organization), as far as GitHub documents it |

## Discussions

Four read tools and four write tools cover the GitHub Discussions of one repository, all through GraphQL and all taking
`repository` like the other repository tools; a repository outside the connection's targets is refused as an
invalid request before any credential is resolved. `github.discussion-categories.list` lists the categories
(`id`, `name`, `slug`, `description`, `emoji`, `is_answerable`, times). `github.discussions.list` lists
discussions, optionally filtered by `category` (a category `id`), `state` (`open` or `closed`), and `answered`
(`true` or `false`), ordered by `order_by` (`created_at` or `updated_at`) and `direction` (`asc` or `desc`);
each entry carries `number`, `title`, `body` (cut at 1000 characters, `body_truncated` says so), `author`,
`category`, `closed`, `locked`, `answered`, `upvote_count`, `comment_count`, `labels`, times, and `url`.
`github.discussions.get` reads one discussion by `number`, with its body cut at 20000 characters.
`github.discussion-comments.list` lists the top-level comments of one discussion, oldest first, with `author`,
`body` (cut at 4000 characters), `is_answer`, `upvote_count`, `reply_count`, times, and `url`. Replies are
counted; with `include_replies=true` each top-level comment also carries `replies` (at most 100, GitHub's
maximum, each with `id`, `database_id`, `author`, `body` cut at 4000 characters with `body_truncated`,
`is_answer`, `upvote_count`, times, and `url`), and `replies_truncated` says that more replies exist. The reply
bodies of one answer share a budget of 200000 characters; once it is spent, further reply bodies come back
empty with `body_truncated`. A cursor of a list without replies does not continue a list with them. Titles, bodies, and comments come from other accounts and are untrusted data.

The lists take `limit` (1 through 100, 30 when omitted) and an opaque `cursor`; a cursor belongs to the
repository and the filters that produced it, and another filter, category, or discussion refuses it as an
invalid request. A discussion that GitHub does not hold or does not show to the token is `not-found`, named
as "discussion #N".

Organization discussions are read through the repository that stores them, usually `ORG/.github`: name that
repository as `repository`, and it must be among the connection's targets. Qatlas does not fall back to the
`.github` repository of an organization on its own.

`github.discussions.create` starts a discussion from `title`, `body`, and a `category_id`;
`github.discussion-comments.create` adds a top-level comment to a discussion `number`, or, with `reply_to` (the
node `id` of a top-level comment of that discussion), a reply; GitHub allows one level of replies, so a reply
to a reply is refused. `github.discussion-comments.update` replaces the body of a comment or reply by its node
`id` (`comment_id`), and `github.discussion-comments.delete` removes one permanently. Before any mutation the
comment or category is read back and must belong to the chosen `repository`; a foreign one is refused
(`not-found`, or `invalid-request` for a reply to a reply or to a comment of another discussion) without a
change. Written bodies are not echoed. Every change needs `--confirm`, is sent once, and is never retried; an
unclear outcome says it may have been applied. Discussions are not closed, locked, marked as answered, or
edited as a whole, and polls are not written. Creating is not idempotent, since each request adds a new
object; updating is idempotent, since the same body leaves the same state; deleting has unknown idempotency,
since GitHub answers a second delete of the same comment with an error. Deleting is offered only where the
connection's `tools` list names `github.discussion-comments.delete`.

```sh
qatlas invoke github.discussion-categories.list --connection code
qatlas invoke github.discussions.list --connection code --arg category=DIC_kwDOExample --arg answered=false
qatlas invoke github.discussions.get --connection code --arg number=12
qatlas invoke github.discussion-comments.list --connection code --arg number=12 --arg limit=10 --arg include_replies=true
qatlas invoke github.discussion-comments.create --connection code --arg number=12 --arg body="Thanks" --confirm
```

| Tool | Effect | Idempotency | Confirmation | Does |
| --- | --- | --- | --- | --- |
| `github.discussion-categories.list` | read | safe | none | lists the discussion categories of a repository |
| `github.discussions.list` | read | safe | none | lists one bounded batch of discussions, filtered by category, state, and answered status |
| `github.discussions.get` | read | safe | none | reads one discussion by its number |
| `github.discussion-comments.list` | read | safe | none | lists one bounded batch of the top-level comments of a discussion, with their replies on request |
| `github.discussions.create` | create | non-idempotent | required | starts a discussion in a category of the repository |
| `github.discussion-comments.create` | create | non-idempotent | required | adds a comment or a reply |
| `github.discussion-comments.update` | update | idempotent | required | replaces the body of a comment or reply |
| `github.discussion-comments.delete` | delete | unknown | required | deletes a comment or reply; listed-only |

The not-recommended setup profile `discussions` ticks the read tools,
`github.discussions.create`, `github.discussion-comments.create`, and `github.discussion-comments.update`; the
delete stays unticked, and the recommended profile `read` is unchanged. Reading discussions needs
`public_repo` (public repositories) or `repo` on a classic token, or Discussions: read on a fine-grained
token; creating, changing, and deleting need `public_repo` or `repo` on a classic token, or Discussions: read
and write on a fine-grained token.

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| the four read tools | `public_repo` or `repo` | Discussions: read |
| `github.discussions.create`, `github.discussion-comments.create`, `.update`, `.delete` | `public_repo` or `repo` | Discussions: read and write |

## Notifications

Six tools read and manage the notification threads of the account behind the connection's token and its
subscriptions. GitHub supports the notification endpoints only with a personal access token (classic) that has
the `notifications` (or `repo`) scope: "These endpoints only support authentication using a personal access
token (classic)" and "All calls to these endpoints require the `notifications` or `repo` scopes"
(https://docs.github.com/en/rest/activity/notifications). A fine-grained token (`github_pat_...`) is therefore
refused with a `permission` failure before any request, and GitHub's own 403 names the same requirement. The
repository subscription is GitHub's watching endpoint and is left to GitHub's decision per request.

`github.notifications.list` lists one batch of threads, unread ones unless `include_read=true`, optionally
only those in which the account `participating`, updated `since` or `before` a date (`YYYY-MM-DD`, midnight
UTC) or UTC time, or of one `repository`. Each entry carries `id` (the `thread_id` of the other tools),
`repository`, `reason`, `unread`, `updated_at`, `last_read_at`, `subject_title` (cut at 500 characters,
`title_truncated` says so), `subject_type`, `subject_url`, and `latest_comment_url`. `limit` is 1 through 50
(30 when omitted) and `cursor` is the opaque `next_cursor`; a cursor belongs to the repository and the
filters that produced it, and another filter or repository refuses it as an invalid request. Titles come from
other accounts and are untrusted data. `github.notifications.get` reads one thread by `thread_id`.
`github.notifications.dismiss` marks a thread `read` or `done` (`state`); `github.thread-subscriptions.set`
subscribes to a thread (`action=watch`), mutes it (`ignore`), or removes the subscription (`delete`);
`github.repository-subscriptions.set` does the same for a repository, taking `repository` like the other
repository tools. `github.notifications.mark-all` marks every thread of the account or of one `repository`
read, optionally only those not updated after `last_read_at`; GitHub answers 202 when it finishes in the
background (`queued=true`) and 205 when it is done.

A notification belongs to the account, not to a repository, so the targets act as follows. A thread tool reads
the thread first and refuses one whose repository the targets do not admit as `not-found`, without a change; a
repository target admits its repository, an owner target the repositories of that owner. A connection whose
targets name only projects has no repository to admit and is refused as an invalid request. The list and
`mark-all` take an optional `repository`, which must lie inside the targets, as a repository target or through
an owner target. Without `repository`, a connection whose targets name repositories needs the one repository
they name exactly, otherwise it is an invalid request; a connection without targets lists the whole account; a
connection whose targets name only owners lists the account narrowed to the repositories of those owners.
GitHub cannot narrow "mark everything read" to an owner, so `mark-all` without `repository` is allowed only on
a connection without any target. Every refusal happens before a credential is resolved, except the thread
check, which needs the credential for its one read.

Every change needs `--confirm`, is sent once, and is never retried. `github.notifications.mark-all` is offered
only where the connection's `tools` list names it. GitHub documents no answer for repeating a mark as done, a
mark-all, or a delete, so those changes declare their idempotency as unknown; a mark as read and setting a
subscription to watch or ignore leave the same state when repeated. A repeated mark as done, or a thread
subscription change on a thread that is gone, can answer `not-found` because the pre-read no longer finds the
thread.

```sh
qatlas invoke github.notifications.list --connection code --arg participating=true --arg limit=20
qatlas invoke github.notifications.get --connection code --arg thread_id=1234567890
qatlas invoke github.notifications.dismiss --connection code --arg thread_id=1234567890 --arg state=done --confirm
qatlas invoke github.thread-subscriptions.set --connection code --arg thread_id=1234567890 --arg action=ignore --confirm
qatlas invoke github.repository-subscriptions.set --connection code --arg repository=octo-org/example --arg action=watch --confirm
qatlas invoke github.notifications.mark-all --connection code --arg repository=octo-org/example --confirm
```

| Tool | Effect | Idempotency | Confirmation | Does |
| --- | --- | --- | --- | --- |
| `github.notifications.list` | read | safe | none | lists one bounded batch of notification threads with filters |
| `github.notifications.get` | read | safe | none | reads one thread by its id |
| `github.notifications.dismiss` | update | unknown | required | marks a thread read or done |
| `github.notifications.mark-all` | update | unknown | required | marks all threads of the account or a repository read; listed-only |
| `github.thread-subscriptions.set` | update | unknown | required | watches, ignores, or unsubscribes a thread |
| `github.repository-subscriptions.set` | update | unknown | required | watches, ignores, or unsubscribes a repository |

The not-recommended setup profile `notifications` ticks the list, the get, the dismiss, and both subscription
tools; `github.notifications.mark-all` stays unticked, and the recommended profile `read` is unchanged.

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| the thread tools, `github.notifications.list`, `.get`, `.dismiss`, `.mark-all`, and `github.thread-subscriptions.set` | `notifications` or `repo` | not supported, refused by Qatlas |
| `github.repository-subscriptions.set` | decided by GitHub per request | decided by GitHub per request |

## Gists

Five tools list, read, create, change, and delete gists. A gist belongs to a user account, not to a repository
or a project, so these tools take no target argument. `github.gists.list` lists one bounded batch of the gists
of the account behind the connection's token, secret ones included, or with `username` the public gists of one
user, optionally updated `since` a date (`YYYY-MM-DD`, midnight UTC) or UTC time; `limit` is 1 through 100 (30
when omitted) and `cursor` is the opaque `next_cursor`, bound to the user and the filter, and another one
refuses it as an invalid request. An entry carries `id` (the `gist_id` of the other tools), `description`,
`public`, `owner`, times, `url`, `file_count`, and the file names, media types, languages, and sizes (at most 20
files, `files_truncated` says so), without content. `github.gists.get` reads one gist with its files: a file
shows at most 20000 characters and all files together at most 100000, at most 50 files are shown, and every
cut is marked (`content_truncated`, `files_truncated`); a binary file shows only its metadata
(`binary=true`). Descriptions (cut at 500 characters, `description_truncated`) and file contents come from
other accounts and are untrusted data. GitHub itself cuts a file over 1 MB, and a response over 4 MiB is
refused as an invalid response.

`github.gists.create` makes a gist from `files` (1 through 20 entries of `filename` and `content`, at most
1 MiB together); it is secret unless `public=true`. `github.gists.update` changes the `description` (an empty
text clears it), writes files (`files`: `filename` with `content` to add or replace it, `new_filename` to
rename it), and removes files (`remove_files`) in one request. `github.gists.delete` removes a gist
permanently and is offered only where the connection's `tools` list names it.

The targets act as follows. A connection without targets reaches whatever its token reaches. A connection with
targets must name at least one user as `users/LOGIN` and no repository and no project; otherwise every gist
tool is an invalid request before a credential is resolved. Such a connection reaches only the gists of the
users it names: `username` of the list must be one of them, the list without `username` and `create` need the
account behind the token among them (checked with one read of the account), and `get`, `update`, and `delete`
read the gist first and answer `not-found`, without a change, for a gist whose owner is no named user, or an
anonymous one. An organization owner target does not count, since a gist belongs to a user.

Every change needs `--confirm`, is sent once, and is never retried. A create is not idempotent; an update sets
the named state and leaves the same state when repeated; GitHub documents no answer for repeating a delete, so
it declares its idempotency as unknown, and a repeated delete can answer `not-found`.

```sh
qatlas invoke github.gists.list --connection code --arg username=octocat --arg limit=10
qatlas invoke github.gists.get --connection code --arg gist_id=aa5a315d61ae9438b18d
echo '{"description":"Notes","files":[{"filename":"notes.md","content":"# Notes"}]}' | qatlas invoke github.gists.create --connection code --confirm
qatlas invoke github.gists.delete --connection code --arg gist_id=aa5a315d61ae9438b18d --confirm
```

| Tool | Effect | Idempotency | Confirmation | Does |
| --- | --- | --- | --- | --- |
| `github.gists.list` | read | safe | none | lists one bounded batch of gists without content |
| `github.gists.get` | read | safe | none | reads one gist with bounded file contents |
| `github.gists.create` | create | non-idempotent | required | creates a secret gist by default |
| `github.gists.update` | update | idempotent | required | changes the description and writes, renames, or removes files |
| `github.gists.delete` | delete | unknown | required | deletes a gist permanently; listed-only |

The not-recommended setup profile `gists` ticks the list, the get, the create, and the update; `github.gists.delete`
stays unticked, and the recommended profile `read` is unchanged.

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.gists.list`, `github.gists.get` | `gist` for secret gists; none for public ones | GitHub lists no permission for the reads |
| `github.gists.create`, `.update`, `.delete` | `gist` | user permission Gists: read and write |

Sources: https://docs.github.com/en/rest/gists/gists and
https://docs.github.com/en/rest/authentication/permissions-required-for-fine-grained-personal-access-tokens
(checked 2026-09-30; the second lists the create, update, and delete routes under the user permission Gists with
write access and lists no gist read route).

## Security alerts

Six tools read the alerts of one repository; they change nothing, so an alert is neither closed, dismissed, nor
assigned. `github.code-scanning-alerts.list` filters by `state` (open, closed, dismissed, fixed), `severity`
(critical, high, medium, low, warning, note, error), `tool_name`, and `ref`; `github.dependabot-alerts.list`
filters by `state` (open, fixed, dismissed, auto_dismissed) and `severity` (critical, high, medium, low);
`github.secret-scanning-alerts.list` filters by `state` (open, resolved), `secret_type` (a comma-separated list of
type names), and `resolution` (false_positive, wont_fix, revoked, pattern_edited, pattern_deleted,
used_in_tests). Each list answers one bounded batch, newest first, with `limit`, `next_cursor`, and `has_more`;
a cursor belongs to the repository and the filters of its first batch. Each `.get` takes an `alert_number` as the
list reports it. Every tool takes the optional `repository` argument and is checked against the connection's
targets before a credential is resolved; an organization-wide alert list is not offered.

Alert texts (rule and advisory descriptions, messages, dismissal comments, paths) come from scanners and other
accounts and are untrusted data. Each is cut at 500 characters (4000 for the rule help and the advisory
description of a `.get`), and `truncated` says so.

A secret scanning alert never shows the found secret. The request asks GitHub to hide it (`hide_secret=true`),
and the answer is decoded into a shape that has no field for a secret value, a snippet, or a comment, so nothing
else GitHub sends can reach a result, an error, or a log. An answer that cannot be read or is too large fails
with a fixed message that quotes nothing of it. An alert reports its type, state, resolution, validity, times,
and the first location (kind, path, lines) only.

```sh
qatlas invoke github.code-scanning-alerts.list --connection code --arg state=open --arg severity=high
qatlas invoke github.dependabot-alerts.get --connection code --arg alert_number=7
qatlas invoke github.secret-scanning-alerts.list --connection code --arg state=open
```

| Tool | Effect | Idempotency | Confirmation | Does |
| --- | --- | --- | --- | --- |
| `github.code-scanning-alerts.list` | read | safe | none | lists code scanning alerts |
| `github.code-scanning-alerts.get` | read | safe | none | reads one code scanning alert with the rule help |
| `github.dependabot-alerts.list` | read | safe | none | lists Dependabot alerts |
| `github.dependabot-alerts.get` | read | safe | none | reads one Dependabot alert with the advisory description |
| `github.secret-scanning-alerts.list` | read | safe | none | lists secret scanning alerts without the secret |
| `github.secret-scanning-alerts.get` | read | safe | none | reads one secret scanning alert without the secret |

The not-recommended setup profile `security` ticks these six and the tools of the next subsection; the
recommended profile `read` is unchanged.

| Tool | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.code-scanning-alerts.*` | `security_events` or `repo`; `public_repo` for a public repository | Code scanning alerts: read |
| `github.dependabot-alerts.*` | `security_events` or `repo`; `public_repo` for a public repository | Dependabot alerts: read |
| `github.secret-scanning-alerts.*` | `repo` or `security_events` | Secret scanning alerts: read |

GitHub answers `not-found` for a repository without the feature enabled or not visible to the token, and code
scanning answers 403 without GitHub Advanced Security. Sources:
https://docs.github.com/en/rest/code-scanning/code-scanning,
https://docs.github.com/en/rest/dependabot/alerts,
https://docs.github.com/en/rest/secret-scanning/secret-scanning, and
https://docs.github.com/en/rest/authentication/permissions-required-for-fine-grained-personal-access-tokens
(checked 2026-09-30).

### Advisories and code quality findings

Five more tools read advisories and code quality findings; they change nothing, so an advisory is neither created,
edited, published, nor closed.

- `github.global-advisories.list` reads the GitHub Advisory Database, GitHub-wide. It filters by `ghsa_id`, `type`
  (reviewed, malware, unreviewed; GitHub answers reviewed when omitted), `cve_id`, `ecosystem` (actions, composer,
  erlang, go, maven, npm, nuget, other, pip, pub, rubygems, rust, swift), `severity` (unknown, low, medium, high,
  critical), `cwes` (a list of up to 20 numbers), `is_withdrawn` (true returns only withdrawn advisories; false adds
  no filter), `affects` (such as `package1,package2@1.0.0`), and `published`, `updated`, and `modified` (an ISO 8601
  date or range). `github.global-advisories.get` reads one advisory by `ghsa_id`, with its description and up to 20
  references.
- `github.repository-advisories.list` reads the repository security advisories of one repository, filtered by
  `state` (triage, draft, published, closed), `sort` (created, updated, published; created when omitted), and
  `direction` (asc, desc; desc when omitted). `github.organization-advisories.list` does the same for the
  repositories of an organization.
- `github.code-quality-findings.get` reads one code quality finding by `finding_number`, with its rule, location,
  and message. GitHub also lists findings; that list is not offered.

The global tools name no repository: a connection without targets, or one whose targets name only owners, may use
them, and a connection whose targets name a repository or a project is refused as `invalid-request` before a
credential is resolved. The organization list takes the optional `owner` argument as `orgs/LOGIN`, which must be
an organization the connection's targets name as an owner (or any organization on a connection without targets);
a user owner and a repository or project target alone are refused the same way. The repository list and the
finding read take the optional `repository` argument like the alert tools.

The lists page by the `after` cursor GitHub announces in its `Link` header; `next_cursor` carries it together
with the batch size of the first batch and belongs to the tool, the target, and the filters of that batch.

Advisory summaries, descriptions, references, package names, and finding messages come from other accounts and are
untrusted data. Summaries, rule descriptions, and messages are cut at 500 characters, a global advisory
description of a `.get` and a rule help at 4000, and the description of a repository advisory in a list at 2000;
vulnerabilities are capped at 20 per advisory, functions at 10 per vulnerability, references at 20, and `truncated`
says so. Draft and triage advisories hold non-public vulnerability details; they reach only a token whose account
GitHub allows to see them.

```sh
qatlas invoke github.global-advisories.list --connection open --arg ecosystem=npm --arg severity=critical
qatlas invoke github.repository-advisories.list --connection code --arg state=draft
qatlas invoke github.organization-advisories.list --connection org --arg owner=orgs/octo-org
qatlas invoke github.code-quality-findings.get --connection code --arg finding_number=12
```

| Tool | Effect | Idempotency | Confirmation | Does |
| --- | --- | --- | --- | --- |
| `github.global-advisories.list` | read | safe | none | lists global security advisories |
| `github.global-advisories.get` | read | safe | none | reads one global security advisory |
| `github.repository-advisories.list` | read | safe | none | lists the security advisories of a repository |
| `github.organization-advisories.list` | read | safe | none | lists the security advisories of an organization's repositories |
| `github.code-quality-findings.get` | read | safe | none | reads one code quality finding |

The not-recommended setup profile `security` ticks these five and the six alert tools, eleven in all; the
recommended profile `read` is unchanged.

| Tool | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.global-advisories.*` | none | none |
| `github.repository-advisories.list` | `repo` or `repository_advisories:read` for a private repository or an unpublished advisory | Repository security advisories: read |
| `github.organization-advisories.list` | `repo` or `repository_advisories:write`, and an organization owner or security manager | Repository security advisories: write |
| `github.code-quality-findings.get` | `repo`; `public_repo` for a public repository | Code quality: read |

The organization list needs the write-level permission although it only reads; that is how GitHub documents it.
GitHub answers the finding read with 403 when code quality is not enabled or the token may not read it, and the
repository list with `not-found` for a repository the token cannot see. Sources:
https://docs.github.com/en/rest/security-advisories/global-advisories,
https://docs.github.com/en/rest/security-advisories/repository-advisories, and
https://docs.github.com/en/rest/code-quality/code-quality (checked 2026-09-30).

The alert filters above match the official GitHub MCP server (MIT, `pkg/github/code_scanning.go`, `dependabot.go`,
`secret_scanning.go` at commit 85598ba): no filter of the official server is missing.

## Releases

The tools of this section list, read, and change the releases of a repository and the metadata of their
assets; asset content, tags, and release-notes generation without a release stay out of scope. The five reads
and changes are offered by the not-recommended setup profile `releases`; deleting is in no profile, since it
is offered only where a connection's `tools` list names `github.releases.delete`. Every route lies below the
chosen repository; no tool accepts an owner or a free REST path.

| Tool | Effect | Idempotency | Confirmation | Does |
| --- | --- | --- | --- | --- |
| `github.releases.list` | read | safe | none | lists compact releases, in GitHub's own order |
| `github.releases.get` | read | safe | none | reads one release by `id`, `tag`, or `latest` |
| `github.release-assets.list` | read | safe | none | lists the asset metadata of one release |
| `github.releases.create` | create | non-idempotent | required | cuts one release from `tag` |
| `github.releases.update` | update | idempotent | required | replaces fields of one release; left-out fields stay |

```yaml
connections:
  release-reader:
    service: github
    credential: github-reader
    target: repos/octo-org/example
    tools: [github.releases.list, github.releases.get, github.release-assets.list]
```

`github.releases.list` pages by GitHub's `Link` response header, like the Actions and pull request lists that
carry no total count; a release carries its `id`, `tag`, `name` (untrusted data), whether it is a draft or a
prerelease, its author, its times, and its URL. A draft appears only while the configured token can see it.
`github.releases.get` takes exactly one of `id`, `tag`, or `latest`; a lookup by `tag` never finds a draft,
because GitHub does not index a draft's tag, so a draft is only reachable by `id`. It adds the full body
(untrusted data), `target_commitish`, and `assets_count`.

```sh
qatlas invoke github.releases.list --connection release-reader --arg limit=10
qatlas invoke github.releases.get --connection release-reader --arg tag=v1.2.0
qatlas invoke github.release-assets.list --connection release-reader --arg id=1
```

`github.release-assets.list` reads only metadata: name and label (untrusted data), size in bytes, content
type, a digest when GitHub reports one, the download count, and times; an asset's content is never read.

### Changes

| Tool | Effect | Idempotency | Confirmation | Does |
| --- | --- | --- | --- | --- |
| `github.releases.create` | create | non-idempotent | required | `tag`, `target`, `name`, `body`, `draft`, `prerelease`, `generate_notes`, `make_latest` |
| `github.releases.update` | update | idempotent | required | replaces `name`, `body`, `tag`, `target`, `draft`, `prerelease`, or `make_latest`; left-out fields stay |

`github.releases.create` requires `tag`; when the tag does not exist yet, GitHub creates it at `target` (a
branch or a commit) once the release is published. A second create for a tag that already has a release is
refused as `invalid-request` naming the tag, without a second attempt, since GitHub holds at most one release
per tag. `github.releases.update` writes only the fields given; `draft: false` publishes a draft, and
`draft: true` converts a published release back to one. `make_latest` takes `"true"`, `"false"`, or
`"legacy"`, exactly as GitHub's REST field does.

```sh
echo '{"tag":"v1.2.0","name":"v1.2.0","draft":true}' |
  qatlas invoke github.releases.create --connection release-operator --confirm
echo '{"id":1,"draft":false}' | qatlas invoke github.releases.update --connection release-operator --confirm
```

`github.releases.delete` deletes one release by `id`, with its release notes and asset attachments; the tag
it was cut from stays. It is high risk, on the scale of the [pull request merge](github/pull-requests.md#merge-and-conflicts): a
connection offers it only while its `tools` list names it, whatever its `effects`, and no profile a new
connection starts with, recommended or not, may select it. A release already deleted answers `not-found`
naming it, like the other delete tools in this provider.

```yaml
connections:
  release-operator:
    service: github
    credential: github-operator
    target: repos/octo-org/example
    tools: [github.releases.list, github.releases.get, github.release-assets.list, github.releases.create,
      github.releases.update]
  release-deleter:
    service: github
    credential: github-operator
    target: repos/octo-org/example
    tools: [github.releases.get, github.releases.delete]
```

### Tokens for releases

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.releases.list`, `get`, `github.release-assets.list` | `repo` for a private repository; `public_repo` for a public one | Contents: read |
| `github.releases.create`, `update`, `delete` | `repo` | Contents: read and write |

## Repository merge and branch settings

`github.repository-settings.get` and `github.repository-settings.update` belong to the Actions administrator
group, so they are listed only, never part of a starting profile, and chosen with the profile `actions-admin`
or by name in a `tools` list. Both act on one repository inside the connection's targets; a repository
outside them is refused before a secret is read or GitHub is contacted, without naming it.

`get` answers exactly these fields and nothing else of the repository: `default_branch`, `visibility`,
`archived`, `allow_merge_commit`, `allow_squash_merge`, `allow_rebase_merge`, `allow_auto_merge`,
`allow_update_branch`, `delete_branch_on_merge`, `squash_merge_commit_title`, `squash_merge_commit_message`,
`merge_commit_title`, `merge_commit_message`, and `web_commit_signoff_required`. GitHub leaves the merge
fields out for a token without write access; they are absent then.

`update` accepts only these arguments: the booleans `allow_merge_commit`, `allow_squash_merge`,
`allow_rebase_merge`, `allow_auto_merge`, `allow_update_branch`, `delete_branch_on_merge`, and
`web_commit_signoff_required`, and the texts `squash_merge_commit_title` (`PR_TITLE` or
`COMMIT_OR_PR_TITLE`), `squash_merge_commit_message` (`PR_BODY`, `COMMIT_MESSAGES`, or `BLANK`),
`merge_commit_title` (`PR_TITLE` or `MERGE_MESSAGE`), and `merge_commit_message` (`PR_BODY`, `PR_TITLE`, or
`BLANK`). At least one value is required, and only the values given are sent in one `PATCH` request that is
never retried; after a timeout, a server error, or an unreadable answer the refusal says the change may have
been applied. Name, description, visibility, default branch, archiving, feature switches, security settings,
transfer, deletion, rulesets, and the Actions settings are not reachable. Giving `allow_merge_commit`,
`allow_squash_merge`, and `allow_rebase_merge` all as `false` is refused locally, before any request; the
current state is not read for that check. The answer names the settings the call gave with the values GitHub
reports, and carries no `before` value, since the change is the only request. A refusal for a missing right
names the permission below, never text from GitHub.

The tokens of these two tools follow the [credential rules](github/actions.md#credential) of the Actions
administrator:

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.repository-settings.get` | `repo` for a private repository | Metadata: read |
| `github.repository-settings.update` | `repo`, as a repository administrator | Administration: write |
