---
description: >
  Describes the GitHub gist tools: listing and reading gists, creating, changing, and deleting them, how targets apply to gists that belong to a user, and the tokens.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# GitHub gists

The gist tools of the [GitHub provider](README.md).

## Gists

Five tools list, read, create, change, and delete gists. A gist belongs to a user account, not to a repository
or a project, so these tools take no target argument.

| Tool | Does |
| --- | --- |
| `github.gists.list` | lists one bounded batch of gists without content |
| `github.gists.get` | reads one gist with bounded file contents |
| `github.gists.create` | creates a secret gist by default |
| `github.gists.update` | changes the description and writes, renames, or removes files |
| `github.gists.delete` | deletes a gist permanently; listed only |

`github.gists.list` lists the gists of the account behind the connection's token, secret ones included, or
with `username` the public gists of one user; its cursor is bound to the user and the filter, and another one
refuses it as an invalid request. An entry carries no content, and the file list is cut at 20 files with
`files_truncated`. `github.gists.get` reads one gist with its files: a file shows at most 20000 characters and
all files together at most 100000, at most 50 files are shown, and every cut is marked (`content_truncated`,
`files_truncated`); a binary file shows only its metadata (`binary=true`). Descriptions (cut at 500
characters, `description_truncated`) and file contents come from other accounts and are untrusted data.
GitHub itself cuts a file over 1 MB, and a response over 4 MiB is refused as an invalid response.

A gist is answered with the members of GitHub's gist, its owner and user as accounts next to the flat owner
login, and the REST addresses of the gist and of each file in `links`. The id and the addresses of a secret
gist (`public` is `false`) are reported like those of a public one, since the id is the argument of the other
tools; whoever holds them can read a secret gist without signing in, so they are as sensitive as the
content. A gist forked from another names it in `fork_of` with its own members only. GitHub marks `forks` and
`history` of a single gist as deprecated, so `github.gists.get` leaves them out; `github.gists.list` reports
at most 10 of each, with `forks_truncated` and `history_truncated`. File contents are not read beyond the
bounded content `github.gists.get` already shows. An account inside another object carries the fields
GitHub's own REST answers give it; the full profile is read with the account tools.

`github.gists.create` makes a gist that is secret unless `public=true`. `github.gists.update` changes the
description (an empty text clears it), writes files, and removes files in one request. `github.gists.delete`
removes a gist permanently and is offered only where the connection's `tools` list names it.

The targets act as follows. A connection without targets reaches whatever its token reaches. A connection with
targets must name at least one user as `users/LOGIN` and no repository and no project; otherwise every gist
tool is an invalid request before a credential is resolved. Such a connection reaches only the gists of the
users it names: `username` of the list must be one of them, the list without `username` and `create` need the
account behind the token among them (checked with one read of the account), and `get`, `update`, and `delete`
read the gist first and answer `not-found`, without a change, for a gist whose owner is no named user, or an
anonymous one. An organization owner target does not count, since a gist belongs to a user.

Every change is sent once and is never retried, see [confirmed changes](README.md#confirmed-changes). A create
is not idempotent; an update sets the named state and leaves the same state when repeated; GitHub documents no
answer for repeating a delete, so it declares its idempotency as unknown, and a repeated delete can answer
`not-found`.

The not-recommended setup profile `gists` ticks the list, the get, the create, and the update;
`github.gists.delete` stays unticked, and the recommended profile `read` is unchanged.

| Tools | Classic token | Fine-grained token |
| --- | --- | --- |
| `github.gists.list`, `github.gists.get` | `gist` for secret gists; none for public ones | GitHub lists no permission for the reads |
| `github.gists.create`, `.update`, `.delete` | `gist` | user permission Gists: read and write |

Sources: https://docs.github.com/en/rest/gists/gists and
https://docs.github.com/en/rest/authentication/permissions-required-for-fine-grained-personal-access-tokens;
the second lists the create, update, and delete routes under the user permission Gists with write access and
lists no gist read route.
