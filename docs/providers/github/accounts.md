---
description: >
  Describes the GitHub account tools: the account behind a connection's token, the account-wide rule for tools that read it as a whole, organization teams and their members, and the user and organization search.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# GitHub accounts

The account tools of the [GitHub provider](README.md). The team reads take `owner` as described under
[the target of a call](README.md#the-target-of-a-call).

## Account-wide tools

| Tool | Does |
| --- | --- |
| `github.accounts.me` | reads the account behind the connection's token; no repository, project, or owner argument |

`github.accounts.me` and [`github.stars.list`](repositories.md#stars) take no `repository`, `project`, or
`owner` argument: they read data of the account behind the token as a whole. A connection whose targets name a
repository or a project is refused with `invalid-request` before a secret is read, because such a connection
is scoped to those, and the account behind its token may belong to a customer other than the one its targets
name. A connection without targets, or whose targets name only owners, is allowed. Creating or forking a
repository under the token's own account applies the same guard, see
[repository lifecycle](repositories.md#repository-lifecycle-and-collaborators).

Reading the account behind the token needs no scope beyond its own identity, classic or fine-grained.

The terminal editor's setup profile `discovery` selects `github.accounts.me`, `github.teams.list`,
`github.team-members.list`, `github.stars.list`, and the seven [search tools](README.md#search); it changes
nothing. It is not the recommended profile, which stays `read`, unchanged. The profile `stars` is described
under [stars](repositories.md#stars).

## Organization teams

| Tool | Does |
| --- | --- |
| `github.teams.list` | lists the teams of an organization, in batches |
| `github.team-members.list` | lists the members of one team of an organization, in batches, by login |

Both take `owner` as `orgs/LOGIN`: the organization must be a target the connection names as an owner itself,
exactly as `github.projects.create` requires, since a project or a repository pattern of the organization is
not enough. `owner` may be left out only when the targets name exactly one owner entry; `users/LOGIN` is
refused with `invalid-request`, because teams belong to an organization, not a user.
`github.team-members.list` also takes `team`, the slug `github.teams.list` reports. Both follow the
[cursor contract](README.md#cursor-contract), a `github.teams.list` cursor bound to the organization and a
`github.team-members.list` cursor bound to the organization and the team together.

The team reads need `read:org` on a classic token, or Members read access of the organization on a
fine-grained one.

## Search

| Tool | Searches |
| --- | --- |
| `github.users.search` | personal accounts, never organizations |
| `github.organizations.search` | organizations, never personal accounts |

Both read GitHub's own search index with `terms`, under the rules described in [Search](README.md#search). What
the targets force or refuse for them is described under [target scoping](README.md#target-scoping). The
repository search is described with the [repositories](repositories.md#search), the code and commit search
with the [code](code.md#search).
