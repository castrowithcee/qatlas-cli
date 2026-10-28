---
description: >
  Describes the repository lifecycle tools of the Gitea and Forgejo providers: creating, changing, forking, generating
  from a template, syncing, transferring, and deleting repositories, pull mirrors, and repository avatars. It covers
  how a new owner is bound to the targets, what a change that moves a repository returns, the differences between the
  products, and the limits.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-28
---

# Repository lifecycle

These tools create, change, fork, and delete repositories of a connection's targets. Reading repositories is
described on the [provider page](README.md). [Forgejo](../forgejo/README.md) names the differences.

## Target binding

- The repository a tool acts on is chosen against the targets like everywhere else and refused as `invalid-request`
  before the token is read, without quoting it.
- A new owner is named by an owner target of the connection: `organization` of `repositories.create` and
  `repositories.fork` as `orgs/NAME`, `owner` of `repositories.generate` and `new_owner` of `repositories.transfer`
  as `users/NAME` or `orgs/NAME`. A `repos/OWNER/REPO` target names no owner, so such a connection can create,
  fork into, generate into, or transfer to nothing.
- Without `organization`, `repositories.create` and `repositories.fork` put the repository into the account behind
  the token. A connection with targets then needs a `users/NAME` target, and the account is proven before the change
  by `GET /user`, like the user level of the project and Actions administration tools; another account is refused.
  A connection without targets needs no proof.
- Exclusions ([Targets](README.md#exclusions)) refuse an excluded source or a new owner or name that is excluded for
  transfer, fork, generate, create, and rename; a fork hides an excluded `parent`.
- `repositories.generate` also needs the template inside the targets. `repositories.list-forks` drops rows outside
  the targets.

## Behavior

- `repositories.create` offers the template names `gitignores`, `issue_labels`, `license`, and `readme` as plain
  text; the instance resolves them. `object_format_name` and `trust_model` are enumerated.
- `repositories.update` sends only the given settings and needs at least one. It offers neither the mirror
  credentials (`mirror_username`, `mirror_password`, `mirror_token`) nor `external_tracker` and `external_wiki`;
  an argument only the other product has is refused as `invalid-request` before the token is read.
  `internal_tracker` takes all three of its switches together because the instance resets the ones left out.
  `mirror_interval` and `enable_prune` act on a pull mirror. `default_merge_style` has Forgejo's two additional values
  `manually-merged` and `rebase-update-only`.
- A rename or a visibility change can take a repository out of a `repos/OWNER/REPO` target. The answer of
  `repositories.update`, `repositories.transfer`, `repositories.accept-transfer`, and `repositories.reject-transfer`
  therefore carries `reachable`, and `repository` only when the result still lies inside the targets. Later calls
  need the new name in the targets.
- `repositories.fork` and `repositories.transfer` can be asynchronous or wait for the recipient: the answer shows the
  repository as the instance reports it, and a transfer to a user stays pending in `repo_transfer` until the
  recipient accepts it.
- `repositories.sync-fork` brings a fork up to date with its upstream branch. Forgejo sends `POST .../sync_fork`,
  with the branch in the path when given; Gitea sends `POST .../merge-upstream` with `branch` and `ff_only` and
  returns `merge_type`. `repositories.get-sync-state` (Forgejo only) reads `allowed`, `commits_behind`, and the two
  commits.
- `repositories.convert` (Forgejo only) turns a pull mirror into a normal repository for good.
- `repository-mirrors.sync` asks the instance to pull an existing mirror and returns at once; it creates no mirror
  and changes none.
- `repository-avatars.update` reads the image from `local_path` inside a directory the connection releases for
  reading, at most 4 MiB, and sends it base64 encoded. The signature must be PNG, JPEG, GIF, or WebP; any other
  file is refused before the token is read. A raw image is not an argument.
- `repositories.delete` is final: it removes the repository with everything in it.
- `repositories.delete`, `repositories.transfer`, `repositories.accept-transfer`, `repositories.reject-transfer`, and
  `repositories.convert` are offered only to a connection whose `tools` list names them and are in no setup profile.
- A change is sent once and never repeated; an open outcome is reported as described on the provider page.
  Repository names, descriptions, and websites in answers are untrusted data.

## Tools

| Tool | Effect | Provider |
| --- | --- | --- |
| `repositories.create` | create | Gitea, Forgejo |
| `repositories.update` | update | Gitea, Forgejo |
| `repositories.fork` | create | Gitea, Forgejo |
| `repositories.list-forks` | read | Gitea, Forgejo |
| `repositories.generate` | create | Gitea, Forgejo |
| `repositories.sync-fork` | update | Gitea, Forgejo |
| `repositories.get-sync-state` | read | Forgejo |
| `repositories.convert` | update, listed-only | Forgejo |
| `repositories.delete` | delete, listed-only | Gitea, Forgejo |
| `repositories.transfer` | update, listed-only | Gitea, Forgejo |
| `repositories.accept-transfer` | update, listed-only | Gitea, Forgejo |
| `repositories.reject-transfer` | update, listed-only | Gitea, Forgejo |
| `repository-mirrors.sync` | execute | Gitea, Forgejo |
| `repository-avatars.update` | update | Gitea, Forgejo |
| `repository-avatars.delete` | delete | Gitea, Forgejo |
