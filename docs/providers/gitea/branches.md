---
description: >
  Describes the branch tools of the Gitea and Forgejo providers: what they read and change, the token scopes, the
  repository binding, the branch name rules, the differences between the products, and the limits.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-23
---

# Branches

The branch tools list, read, create, rename, and delete the branches of a repository, and on Gitea point a branch at
another commit. [Forgejo](../forgejo.md) names the differences. Branch protection rules are another tool group.

## Scopes

All tools need `read:repository` for a read and `write:repository` for a change (presumably; not checked against
a live instance).

## Target binding

Every path carries the repository, chosen against the connection's targets before the token is resolved; a
repository outside the targets is refused as `invalid-request` without quoting it. A branch is not an identifier of
its own: the instance resolves the name inside that repository, so no evidence read is needed.

## Branch names

A branch name is checked as a Git name before any request, with the rules of the [tag names](tags.md#tag-names), but
a slash between non-empty parts is allowed. It travels as one escaped path segment, so `feature/x` is sent as
`feature%2Fx`, and as plain text in a body.

## Behavior

- The signature verification of a branch's commit is shown without its raw payload and signature; identities are
  personal data. Names, messages, and paths are untrusted data.
- `branches.create` starts the branch at `old_ref_name` (branch, tag, or commit), at the default branch when it is
  left out. The deprecated `old_branch_name` is not offered.
- `branches.rename` changes the name with one request; the instance may refuse the default branch, a protected
  branch, or an existing name.
- `branches.update-ref` (Gitea only) sets the branch to `new_commit_id`. With `old_commit_id` the instance refuses
  the update when the tip differs. A force update is not offered, so only a fast-forward succeeds.
- `branches.delete` removes the branch for good.
- `branches.update-ref` and `branches.delete` are offered only to a connection whose `tools` list names them and
  are in no setup profile.

## Tools

| Tool | Effect | Provider |
| --- | --- | --- |
| `branches.list` | read | Gitea, Forgejo |
| `branches.get` | read | Gitea, Forgejo |
| `branches.create` | create | Gitea, Forgejo |
| `branches.rename` | update | Gitea, Forgejo |
| `branches.update-ref` | update, listed-only | Gitea |
| `branches.delete` | delete, listed-only | Gitea, Forgejo |
