---
description: >
  Describes the tag tools of the Gitea and Forgejo providers: what they read and change, the token scopes, the
  repository binding, the tag name rules, the differences between the products, and the limits.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Tags

The tag tools list, read, create, and delete the Git tags of a repository. Gitea and Forgejo share them; a line
below names a difference. Tags are lightweight unless `tags.create` gets a message. Annotated tag objects, tag
protections, and releases are other tool groups.

## Scopes

All tools need `read:repository` for a read and `write:repository` for a change (presumably; not checked against
a live instance).

## Target binding

Every path carries the repository, chosen against the connection's targets before the token is resolved; a
repository outside the targets is refused as `invalid-request` without quoting it. A tag is not an identifier of
its own: the instance resolves the name inside that repository, so no evidence read is needed.

## Tag names

A tag name is checked as a Git name before any request: at most 255 characters, no control character, space,
`..`, `@{`, or any of `~^:?*[\`, not beginning with `-` or `.`, not ending with `.` or `.lock`. `tags.get` and
`tags.delete` send the name as one escaped path segment and therefore refuse a name with a slash (narrower than
Git). `tags.create` accepts a slash in `tag_name` and `target`, between non-empty parts, because the name travels
in the body.

## Behavior

- `tags.create` points the tag at `target` (branch name or commit hash), at the default branch when it is left
  out, and makes the tag annotated when `message` is set. An existing name is refused by the instance. The
  answer is the created tag.
- A tag has `name`, `id`, `message`, `commit`, `zipball_url`, and `tarball_url`; both addresses are left out when
  they carry user info or a query. Names and messages are untrusted data.
- `tags.delete` removes the tag for good; a release made for the tag stays. It is offered only to a connection
  whose `tools` list names it and is in no setup profile.

## Differences

Forgejo also reports `archive_download_count` (`zip`, `tar_gz`) on every tag; Gitea does not.

## Tools

| Tool | Effect | Provider |
| --- | --- | --- |
| `tags.list` | read | Gitea, Forgejo |
| `tags.get` | read | Gitea, Forgejo |
| `tags.create` | create | Gitea, Forgejo |
| `tags.delete` | delete, listed-only | Gitea, Forgejo |
